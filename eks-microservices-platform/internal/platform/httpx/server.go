package httpx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/ovalles2019/eks-microservices-platform/internal/platform/health"
	"github.com/ovalles2019/eks-microservices-platform/internal/platform/telemetry"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/sync/errgroup"
)

// ServerConfig describes the paired public and admin listeners a service runs.
//
// Probes and metrics live on a separate port from the API so they are never
// reachable through the Ingress or the public load balancer. Exposing /metrics
// on the same port as the API publishes route names, versions and traffic
// volumes to anyone who can reach the service.
type ServerConfig struct {
	PublicAddr string
	AdminAddr  string

	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration

	// ShutdownGrace bounds how long in-flight requests may take to finish
	// after the drain delay. It must be shorter than the pod's
	// terminationGracePeriodSeconds.
	ShutdownGrace time.Duration

	// DrainDelay is how long to keep serving after readiness flips to false,
	// giving the endpoints controller and every kube-proxy and ALB target group
	// time to stop routing here. Shutting the listener down immediately is the
	// usual cause of connection-refused blips during an otherwise healthy
	// rolling update.
	DrainDelay time.Duration
}

func (c *ServerConfig) withDefaults() {
	if c.PublicAddr == "" {
		c.PublicAddr = ":8080"
	}
	if c.AdminAddr == "" {
		c.AdminAddr = ":9090"
	}
	if c.ReadTimeout <= 0 {
		c.ReadTimeout = 5 * time.Second
	}
	if c.WriteTimeout <= 0 {
		c.WriteTimeout = 30 * time.Second
	}
	if c.IdleTimeout <= 0 {
		c.IdleTimeout = 120 * time.Second
	}
	if c.ShutdownGrace <= 0 {
		c.ShutdownGrace = 25 * time.Second
	}
	if c.DrainDelay <= 0 {
		c.DrainDelay = 5 * time.Second
	}
}

// AdminHandler builds the admin mux: probes plus the Prometheus scrape target.
func AdminHandler(reg *health.Registry, m *telemetry.Metrics) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", reg.LivenessHandler())
	mux.Handle("GET /readyz", reg.ReadinessHandler())
	mux.Handle("GET /startupz", reg.StartupHandler())
	mux.Handle("GET /metrics", promhttp.HandlerFor(m.Registry(), promhttp.HandlerOpts{
		// A scrape must never be the thing that takes the service down.
		ErrorHandling: promhttp.ContinueOnError,
	}))
	return mux
}

// Serve runs the public and admin listeners until SIGTERM or SIGINT, then
// drains and shuts down.
//
// The full sequence, which is what makes a rolling update lossless:
//
//	SIGTERM -> readiness false -> wait DrainDelay (endpoints propagate)
//	        -> stop accepting, finish in-flight within ShutdownGrace
//	        -> flush traces -> exit
func Serve(ctx context.Context, cfg ServerConfig, logger *slog.Logger, reg *health.Registry, public, admin http.Handler, flush telemetry.ShutdownFunc) error {
	cfg.withDefaults()

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	publicSrv := &http.Server{
		Addr:              cfg.PublicAddr,
		Handler:           public,
		ReadHeaderTimeout: cfg.ReadTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
	adminSrv := &http.Server{
		Addr:              cfg.AdminAddr,
		Handler:           admin,
		ReadHeaderTimeout: cfg.ReadTimeout,
		// The admin server deliberately has no write timeout shorter than a
		// slow scrape of a large registry.
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  cfg.IdleTimeout,
		ErrorLog:     slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		logger.Info("public listener started", slog.String("addr", cfg.PublicAddr))
		if err := publicSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("public listener: %w", err)
		}
		return nil
	})
	g.Go(func() error {
		logger.Info("admin listener started", slog.String("addr", cfg.AdminAddr))
		if err := adminSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("admin listener: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		<-gctx.Done()

		logger.Info("shutdown signal received, draining",
			slog.Duration("drain_delay", cfg.DrainDelay),
			slog.Duration("grace", cfg.ShutdownGrace),
		)

		// Fail readiness first so no new traffic is routed here, but keep
		// serving: requests already in flight, and those the data plane has not
		// yet stopped sending, still deserve a real response.
		reg.BeginDrain()
		time.Sleep(cfg.DrainDelay)

		// context.WithoutCancel: the parent context is already cancelled by the
		// signal, and reusing it would make Shutdown return instantly and
		// abandon every in-flight request.
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(gctx), cfg.ShutdownGrace)
		defer cancel()

		var shutdownErr error
		if err := publicSrv.Shutdown(shutdownCtx); err != nil {
			shutdownErr = fmt.Errorf("public shutdown: %w", err)
			logger.Error("public listener did not drain in time", slog.Any("error", err))
		}
		if err := adminSrv.Shutdown(shutdownCtx); err != nil {
			logger.Warn("admin listener did not drain in time", slog.Any("error", err))
		}

		// Flush spans last, so the shutdown path itself is traced.
		if flush != nil {
			flushCtx, flushCancel := context.WithTimeout(context.WithoutCancel(gctx), 5*time.Second)
			defer flushCancel()
			if err := flush(flushCtx); err != nil {
				logger.Warn("trace flush failed", slog.Any("error", err))
			}
		}

		logger.Info("shutdown complete")
		return shutdownErr
	})

	if err := g.Wait(); err != nil {
		return err
	}
	return nil
}
