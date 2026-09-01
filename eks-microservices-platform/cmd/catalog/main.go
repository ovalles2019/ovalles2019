// Command catalog owns the device registry and is the only service that holds
// a connection to the devices database.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/ovalles2019/eks-microservices-platform/internal/catalog"
	"github.com/ovalles2019/eks-microservices-platform/internal/platform/config"
	"github.com/ovalles2019/eks-microservices-platform/internal/platform/health"
	"github.com/ovalles2019/eks-microservices-platform/internal/platform/httpx"
	"github.com/ovalles2019/eks-microservices-platform/internal/platform/telemetry"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const serviceName = "catalog"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	l := config.NewLoader()
	rt := config.LoadRuntime(l, serviceName)

	store := l.String("CATALOG_STORE", "postgres")
	dbMaxConns := l.Int("DB_MAX_CONNS", 10)
	dbMinConns := l.Int("DB_MIN_CONNS", 2)
	dbConnectTimeout := l.Duration("DB_CONNECT_TIMEOUT", 5*time.Second)
	migrateOnBoot := l.Bool("DB_MIGRATE_ON_BOOT", true)

	// The DSN is read from the environment, which in a cluster is populated
	// from a Secret synced out of AWS Secrets Manager by External Secrets. It
	// is never baked into the image or committed to a values file.
	dsn := os.Getenv("DATABASE_URL")

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

	repo, cleanup, err := openStore(ctx, store, dsn, catalog.PoolConfig{
		DSN:            dsn,
		MaxConns:       int32(dbMaxConns),
		MinConns:       int32(dbMinConns),
		ConnectTimeout: dbConnectTimeout,
	}, migrateOnBoot, logger)
	if err != nil {
		return err
	}
	defer cleanup()

	// Readiness is gated on the database because this service has nothing
	// useful to return without it. That is the opposite of the gateway, which
	// stays ready through a dependency outage precisely because it does.
	probes.Register("database", repo.Ping)

	handler := catalog.NewHandler(repo, logger)

	public := httpx.Chain(
		handler.Routes(),
		httpx.RequestID(),
		httpx.Logging(logger),
		httpx.Recovery(),
		httpx.SecurityHeaders(),
		httpx.Metrics(metrics),
		httpx.MaxBodyBytes(256<<10),
		httpx.Timeout(rt.RequestTimeout),
	)
	instrumented := otelhttp.NewHandler(public, serviceName,
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			if r.Pattern != "" {
				return r.Pattern
			}
			return r.Method + " unmatched"
		}),
	)

	// MarkStarted comes after migrations, so the startup probe covers schema
	// work and a slow migration is not mistaken for a hung process.
	probes.MarkStarted()
	logger.Info("catalog starting",
		slog.String("version", rt.Version),
		slog.String("environment", rt.Environment),
		slog.String("store", store),
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

// openStore selects the repository implementation.
//
// The in-memory store must be requested explicitly. Falling back to it when the
// DSN is missing would let a misconfigured production pod come up healthy,
// serve an empty catalog, and silently discard every write.
func openStore(ctx context.Context, store, dsn string, poolCfg catalog.PoolConfig, migrate bool, logger *slog.Logger) (catalog.Repository, func(), error) {
	switch store {
	case "memory":
		logger.Warn("using the in-memory store; all data is lost on restart",
			slog.String("hint", "set CATALOG_STORE=postgres and DATABASE_URL outside local development"))
		return catalog.NewMemoryRepository(), func() {}, nil

	case "postgres":
		if dsn == "" {
			return nil, nil, errors.New("config DATABASE_URL: is required when CATALOG_STORE=postgres")
		}
		repo, err := catalog.NewPostgresRepository(ctx, poolCfg)
		if err != nil {
			return nil, nil, err
		}
		if migrate {
			migrateCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if err := repo.Migrate(migrateCtx); err != nil {
				repo.Close()
				return nil, nil, err
			}
			logger.Info("schema applied")
		}
		return repo, repo.Close, nil

	default:
		return nil, nil, fmt.Errorf("config CATALOG_STORE: %q is not one of postgres, memory", store)
	}
}

func newLogger(rt config.Runtime) *slog.Logger {
	level := slog.LevelInfo
	if err := level.UnmarshalText([]byte(rt.LogLevel)); err != nil {
		level = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})).With(
		slog.String("service", rt.ServiceName),
		slog.String("version", rt.Version),
		slog.String("environment", rt.Environment),
	)
}
