// Command gateway is the platform's public edge: it authenticates callers,
// enforces per-client rate limits, and fans out to the catalog and scorer
// services behind circuit breakers.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/ovalles2019/eks-microservices-platform/internal/gateway"
	"github.com/ovalles2019/eks-microservices-platform/internal/platform/config"
	"github.com/ovalles2019/eks-microservices-platform/internal/platform/health"
	"github.com/ovalles2019/eks-microservices-platform/internal/platform/httpx"
	"github.com/ovalles2019/eks-microservices-platform/internal/platform/resilience"
	"github.com/ovalles2019/eks-microservices-platform/internal/platform/telemetry"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const serviceName = "gateway"

func main() {
	if err := run(); err != nil {
		// Write to stderr directly: the failure may be that the logger's own
		// configuration was invalid.
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	l := config.NewLoader()
	rt := config.LoadRuntime(l, serviceName)

	cfg := struct {
		CatalogURL              string
		ScorerURL               string
		UpstreamTimeout         time.Duration
		RetryAttempts           int
		RateLimitRPS            float64
		RateLimitBurst          int
		APIKeys                 string
		DegradeOnCatalogFailure bool
	}{
		CatalogURL:      l.String("CATALOG_URL", "http://catalog:8080"),
		ScorerURL:       l.String("SCORER_URL", "http://scorer:8080"),
		UpstreamTimeout: l.Duration("UPSTREAM_TIMEOUT", 2*time.Second),
		RetryAttempts:   l.Int("UPSTREAM_RETRY_ATTEMPTS", 3),
		RateLimitRPS:    l.Float("RATE_LIMIT_RPS", 100),
		RateLimitBurst:  l.Int("RATE_LIMIT_BURST", 200),
		APIKeys:         l.String("API_KEYS", ""),
		// Defaults to on: a catalog outage should cost enrichment, not the
		// whole scoring path. See internal/gateway.Handler.ScoreReading.
		DegradeOnCatalogFailure: l.Bool("DEGRADE_ON_CATALOG_FAILURE", true),
	}

	if err := l.Err(); err != nil {
		return err
	}

	logger := newLogger(rt)
	slog.SetDefault(logger)

	ctx := context.Background()
	flush, err := telemetry.SetupTracing(ctx, telemetry.TracingConfig{
		ServiceName: rt.ServiceName,
		Version:     rt.Version,
		Environment: rt.Environment,
		Endpoint:    rt.OTLPEndpoint,
		SampleRate:  rt.TraceSampleRate,
	})
	if err != nil {
		return fmt.Errorf("tracing: %w", err)
	}

	metrics := telemetry.NewMetrics(rt.ServiceName, rt.Version, rt.Environment)
	probes := health.New(rt.ServiceName, rt.Version, 2*time.Second)

	retry := resilience.RetryConfig{
		MaxAttempts:    cfg.RetryAttempts,
		BaseDelay:      50 * time.Millisecond,
		MaxDelay:       time.Second,
		JitterFraction: 0.3,
	}
	breaker := resilience.BreakerConfig{
		MinimumRequests:   20,
		FailureRatio:      0.5,
		OpenTimeout:       15 * time.Second,
		HalfOpenMaxCalls:  3,
		HalfOpenSuccesses: 2,
		Window:            30 * time.Second,
	}

	upstreams := gateway.Upstreams{
		Catalog: httpx.NewUpstream(httpx.UpstreamConfig{
			Name: "catalog", BaseURL: cfg.CatalogURL, Timeout: cfg.UpstreamTimeout,
			Retry: retry, Breaker: breaker,
		}, metrics, logger),
		Scorer: httpx.NewUpstream(httpx.UpstreamConfig{
			Name: "scorer", BaseURL: cfg.ScorerURL, Timeout: cfg.UpstreamTimeout,
			Retry: retry, Breaker: breaker,
		}, metrics, logger),
	}

	// Readiness reports the breaker rather than probing the dependency.
	//
	// Actively calling catalog on every readiness probe would add synthetic
	// load exactly when it is struggling. And readiness stays true with an open
	// breaker on purpose: the gateway still serves degraded scores and every
	// other route, so pulling it out of the load balancer would convert a
	// partial outage into a total one.
	for _, up := range []*httpx.Upstream{upstreams.Catalog, upstreams.Scorer} {
		up := up
		probes.Register(up.Name()+"_breaker", func(context.Context) error {
			if state := up.BreakerState(); state == resilience.StateOpen {
				logger.Warn("dependency breaker is open", slog.String("upstream", up.Name()))
			}
			return nil
		})
	}

	keys := gateway.ParseKeySpec(cfg.APIKeys)
	if keys.Len() == 0 {
		logger.Warn("no API keys configured; the gateway will accept unauthenticated requests",
			slog.String("hint", "set API_KEYS, or leave empty only for local development"))
	}

	limiter := gateway.NewRateLimiter(gateway.RateLimiterConfig{
		RequestsPerSecond: cfg.RateLimitRPS,
		Burst:             cfg.RateLimitBurst,
	})
	defer limiter.Close()

	handler := gateway.NewHandler(upstreams, logger, cfg.DegradeOnCatalogFailure)

	// Order matters. Recovery is outermost so it catches panics from every
	// layer below; auth precedes the rate limiter so the limit is keyed by the
	// authenticated client rather than a shared NAT address; the timeout sits
	// closest to the handler so it bounds handler work, not queueing.
	public := httpx.Chain(
		handler.Routes(),
		httpx.RequestID(),
		httpx.Logging(logger),
		httpx.Recovery(),
		httpx.SecurityHeaders(),
		httpx.Metrics(metrics),
		httpx.MaxBodyBytes(1<<20),
		gateway.Authenticate(keys),
		limiter.Middleware(gateway.RateLimitKey),
		httpx.Timeout(rt.RequestTimeout),
	)
	// otelhttp outermost so the server span covers the entire request,
	// including time spent waiting on the rate limiter.
	instrumented := otelhttp.NewHandler(public, "gateway",
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			if r.Pattern != "" {
				return r.Pattern
			}
			return r.Method + " unmatched"
		}),
	)

	probes.MarkStarted()
	logger.Info("gateway starting",
		slog.String("version", rt.Version),
		slog.String("environment", rt.Environment),
		slog.String("catalog_url", cfg.CatalogURL),
		slog.String("scorer_url", cfg.ScorerURL),
		slog.Bool("auth_enabled", keys.Len() > 0),
		slog.Bool("degrade_on_catalog_failure", cfg.DegradeOnCatalogFailure),
	)

	return httpx.Serve(ctx, httpx.ServerConfig{
		PublicAddr:    rt.HTTPAddr,
		AdminAddr:     rt.AdminAddr,
		ReadTimeout:   rt.ReadTimeout,
		WriteTimeout:  rt.WriteTimeout,
		IdleTimeout:   rt.IdleTimeout,
		ShutdownGrace: rt.ShutdownGrace,
	}, logger, probes, instrumented, httpx.AdminHandler(probes, metrics), flush)
}

func newLogger(rt config.Runtime) *slog.Logger {
	level := slog.LevelInfo
	if err := level.UnmarshalText([]byte(rt.LogLevel)); err != nil {
		level = slog.LevelInfo
	}
	// JSON to stdout: the container runtime captures stdout and the collector
	// parses structured fields. Writing text or logging to a file inside the
	// container loses both.
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})).With(
		slog.String("service", rt.ServiceName),
		slog.String("version", rt.Version),
		slog.String("environment", rt.Environment),
	)
}
