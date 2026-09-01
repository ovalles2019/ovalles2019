package httpx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/ovalles2019/eks-microservices-platform/internal/platform/resilience"
	"github.com/ovalles2019/eks-microservices-platform/internal/platform/telemetry"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// ErrUpstreamUnavailable is returned when a dependency could not be reached
// within its budget, including when its breaker is open.
var ErrUpstreamUnavailable = errors.New("upstream unavailable")

// UpstreamConfig describes one downstream dependency.
type UpstreamConfig struct {
	Name    string
	BaseURL string
	// Timeout bounds a single attempt, not the whole retry loop; the caller's
	// context bounds the loop.
	Timeout time.Duration
	Retry   resilience.RetryConfig
	Breaker resilience.BreakerConfig
}

// Upstream is a resilient client for one downstream service.
type Upstream struct {
	name    string
	baseURL string
	client  *http.Client
	retry   resilience.RetryConfig
	breaker *resilience.Breaker
	metrics *telemetry.Metrics
	logger  *slog.Logger
}

// NewUpstream builds a client with connection pooling, tracing, retries and a
// circuit breaker already wired in.
func NewUpstream(cfg UpstreamConfig, m *telemetry.Metrics, logger *slog.Logger) *Upstream {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 2 * time.Second
	}

	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   2 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		// Pods are long-lived and talk to a small set of services, so a
		// generous per-host pool avoids paying TCP and TLS setup on most calls.
		// The Go default MaxIdleConnsPerHost of 2 silently serialises fan-out.
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   3 * time.Second,
		ExpectContinueTimeout: time.Second,
		ForceAttemptHTTP2:     true,
	}

	u := &Upstream{
		name:    cfg.Name,
		baseURL: cfg.BaseURL,
		client: &http.Client{
			// otelhttp injects traceparent, so a request crossing services
			// stays on one trace.
			Transport: otelhttp.NewTransport(transport),
			Timeout:   cfg.Timeout,
		},
		retry:   cfg.Retry,
		metrics: m,
		logger:  logger.With(slog.String("upstream", cfg.Name)),
	}

	breakerCfg := cfg.Breaker
	breakerCfg.OnStateChange = func(from, to resilience.State) {
		if m != nil {
			m.BreakerTransitions.WithLabelValues(cfg.Name, from.String(), to.String()).Inc()
			m.BreakerState.WithLabelValues(cfg.Name).Set(stateValue(to))
		}
		u.logger.Warn("circuit breaker state change",
			slog.String("from", from.String()),
			slog.String("to", to.String()),
		)
	}
	u.breaker = resilience.NewBreaker(cfg.Name, breakerCfg)

	if m != nil {
		m.BreakerState.WithLabelValues(cfg.Name).Set(stateValue(resilience.StateClosed))
	}

	u.retry.OnRetry = func(attempt int, delay time.Duration, err error) {
		if m != nil {
			m.UpstreamRetriesTotal.WithLabelValues(cfg.Name).Inc()
		}
		u.logger.Debug("retrying upstream call",
			slog.Int("attempt", attempt),
			slog.Duration("delay", delay),
			slog.Any("error", err),
		)
	}

	return u
}

func stateValue(s resilience.State) float64 {
	switch s {
	case resilience.StateClosed:
		return 0
	case resilience.StateHalfOpen:
		return 1
	default:
		return 2
	}
}

// BreakerState exposes the current breaker state for readiness reporting.
func (u *Upstream) BreakerState() resilience.State { return u.breaker.State() }

// Name returns the dependency name.
func (u *Upstream) Name() string { return u.name }

// Response is a fully-read upstream response.
type Response struct {
	Status int
	Body   []byte
}

// Get issues a GET against the upstream, retrying only failures that a retry
// could plausibly fix.
func (u *Upstream) Get(ctx context.Context, path string, headers map[string]string) (*Response, error) {
	return u.do(ctx, http.MethodGet, path, nil, headers)
}

// Post issues a POST against the upstream.
//
// POST is retried here because every write endpoint in this platform is
// idempotent by contract — the gateway forwards an Idempotency-Key and the
// handlers are pure scoring or upsert operations. Retrying a non-idempotent
// POST would silently duplicate work, so this is a property of these services
// rather than a safe default.
func (u *Upstream) Post(ctx context.Context, path string, body []byte, headers map[string]string) (*Response, error) {
	return u.do(ctx, http.MethodPost, path, body, headers)
}

func (u *Upstream) do(ctx context.Context, method, path string, body []byte, headers map[string]string) (*Response, error) {
	start := time.Now()
	var result *Response

	err := resilience.Retry(ctx, u.retry, func(ctx context.Context, attempt int) error {
		return u.breaker.Do(ctx, func(ctx context.Context) error {
			resp, err := u.attempt(ctx, method, path, body, headers, attempt)
			if err != nil {
				return err
			}
			result = resp
			return nil
		})
	})

	outcome := "success"
	switch {
	case errors.Is(err, resilience.ErrBreakerOpen):
		outcome = "breaker_open"
	case err != nil:
		outcome = "error"
	case result != nil && result.Status >= 500:
		outcome = "server_error"
	case result != nil && result.Status >= 400:
		outcome = "client_error"
	}

	if u.metrics != nil {
		u.metrics.UpstreamRequestsTotal.WithLabelValues(u.name, outcome).Inc()
		u.metrics.UpstreamRequestDuration.WithLabelValues(u.name).Observe(time.Since(start).Seconds())
	}

	if err != nil {
		if errors.Is(err, resilience.ErrBreakerOpen) {
			return nil, fmt.Errorf("%w: %s breaker open", ErrUpstreamUnavailable, u.name)
		}
		return nil, fmt.Errorf("%w: %s: %v", ErrUpstreamUnavailable, u.name, err)
	}
	return result, nil
}

func (u *Upstream) attempt(ctx context.Context, method, path string, body []byte, headers map[string]string, attempt int) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, u.baseURL+path, bodyReader(body))
	if err != nil {
		// A malformed URL will be malformed on every attempt.
		return nil, resilience.Permanently(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	// Surfacing the attempt number lets the receiving service tell a retry
	// storm apart from genuine traffic growth in its own logs.
	req.Header.Set("X-Retry-Attempt", strconv.Itoa(attempt))
	if id := RequestIDFrom(ctx); id != "" {
		req.Header.Set(RequestIDHeader, id)
	}

	resp, err := u.client.Do(req)
	if err != nil {
		// Transport errors (connection refused, reset, timeout) are exactly the
		// transient class retries exist for.
		return nil, err
	}
	defer func() {
		// Draining before closing lets the connection return to the pool
		// instead of being torn down and re-dialled on the next call.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
	}()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}

	switch {
	case resp.StatusCode >= 500, resp.StatusCode == http.StatusTooManyRequests:
		// Retryable, and counted as a failure by the breaker.
		return nil, fmt.Errorf("upstream %s returned %d", u.name, resp.StatusCode)
	case resp.StatusCode >= 400:
		// A 4xx is the caller's fault and will not change on retry. It is
		// returned as a successful call so the breaker does not trip: a client
		// sending bad requests says nothing about the dependency's health.
		return &Response{Status: resp.StatusCode, Body: payload}, nil
	}
	return &Response{Status: resp.StatusCode, Body: payload}, nil
}

// bodyReader returns a fresh reader over b.
//
// A new one is built per attempt rather than reusing a single reader, because a
// retry with a drained reader would send an empty body and turn a transient
// failure into a confusing 400.
func bodyReader(b []byte) io.Reader {
	if b == nil {
		return nil
	}
	return bytes.NewReader(b)
}
