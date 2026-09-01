// Package config loads service configuration from the environment.
//
// Every field is validated at startup and a service refuses to boot on bad
// input. Failing loudly at boot turns a misconfiguration into a CrashLoopBackOff
// that never passes its readiness probe, so a bad rollout stalls instead of
// replacing healthy pods with broken ones.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Loader accumulates errors across every lookup so a single boot reports all
// configuration problems at once rather than one per restart.
type Loader struct {
	errs []error
}

// NewLoader returns an empty Loader.
func NewLoader() *Loader { return &Loader{} }

// Err returns the joined error for every failed lookup, or nil.
func (l *Loader) Err() error {
	if len(l.errs) == 0 {
		return nil
	}
	return errors.Join(l.errs...)
}

func (l *Loader) fail(key string, err error) {
	l.errs = append(l.errs, fmt.Errorf("config %s: %w", key, err))
}

// String returns the value of key, or def when unset or empty.
func (l *Loader) String(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// RequiredString returns the value of key and records an error when it is unset.
func (l *Loader) RequiredString(key string) string {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		l.fail(key, errors.New("is required but not set"))
		return ""
	}
	return v
}

// Int returns key parsed as an int, or def when unset.
func (l *Loader) Int(key string, def int) int {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		l.fail(key, fmt.Errorf("%q is not an integer", raw))
		return def
	}
	return v
}

// Duration returns key parsed as a Go duration (e.g. "250ms"), or def when unset.
func (l *Loader) Duration(key string, def time.Duration) time.Duration {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return def
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		l.fail(key, fmt.Errorf("%q is not a duration", raw))
		return def
	}
	if v <= 0 {
		l.fail(key, fmt.Errorf("%q must be positive", raw))
		return def
	}
	return v
}

// Float returns key parsed as a float64, or def when unset.
func (l *Loader) Float(key string, def float64) float64 {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return def
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		l.fail(key, fmt.Errorf("%q is not a number", raw))
		return def
	}
	return v
}

// Bool returns key parsed as a bool, or def when unset.
func (l *Loader) Bool(key string, def bool) bool {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return def
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		l.fail(key, fmt.Errorf("%q is not a boolean", raw))
		return def
	}
	return v
}

// Runtime holds the settings every service in the platform shares.
type Runtime struct {
	ServiceName string
	Version     string
	Environment string
	LogLevel    string

	HTTPAddr        string
	AdminAddr       string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownGrace   time.Duration
	RequestTimeout  time.Duration
	OTLPEndpoint    string
	TraceSampleRate float64
}

// LoadRuntime reads the settings shared by every service.
//
// ShutdownGrace must stay below the pod's terminationGracePeriodSeconds or the
// kubelet SIGKILLs the process mid-drain; deploy/charts/platform-common keeps
// the two in step.
func LoadRuntime(l *Loader, serviceName string) Runtime {
	return Runtime{
		ServiceName: serviceName,
		Version:     l.String("SERVICE_VERSION", "dev"),
		Environment: l.String("ENVIRONMENT", "local"),
		LogLevel:    l.String("LOG_LEVEL", "info"),

		HTTPAddr:        l.String("HTTP_ADDR", ":8080"),
		AdminAddr:       l.String("ADMIN_ADDR", ":9090"),
		ReadTimeout:     l.Duration("HTTP_READ_TIMEOUT", 5*time.Second),
		WriteTimeout:    l.Duration("HTTP_WRITE_TIMEOUT", 30*time.Second),
		IdleTimeout:     l.Duration("HTTP_IDLE_TIMEOUT", 120*time.Second),
		ShutdownGrace:   l.Duration("SHUTDOWN_GRACE", 25*time.Second),
		RequestTimeout:  l.Duration("REQUEST_TIMEOUT", 10*time.Second),
		OTLPEndpoint:    l.String("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		TraceSampleRate: l.Float("OTEL_TRACE_SAMPLE_RATE", 0.1),
	}
}
