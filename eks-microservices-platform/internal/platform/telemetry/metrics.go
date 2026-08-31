// Package telemetry wires the three signals the platform's SLOs are computed
// from: structured logs, RED metrics and distributed traces.
package telemetry

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Metrics holds the instruments every service exposes.
//
// Labels are deliberately low-cardinality. Route is the registered mux pattern
// (`/api/v1/devices/{id}`), never the raw path: a label that carries user input
// multiplies into one time series per distinct value and is the usual way a
// team takes down its own Prometheus.
type Metrics struct {
	registry *prometheus.Registry

	RequestsTotal    *prometheus.CounterVec
	RequestDuration  *prometheus.HistogramVec
	RequestsInFlight prometheus.Gauge
	ResponseSize     *prometheus.HistogramVec

	UpstreamRequestsTotal   *prometheus.CounterVec
	UpstreamRequestDuration *prometheus.HistogramVec
	UpstreamRetriesTotal    *prometheus.CounterVec

	BreakerState       *prometheus.GaugeVec
	BreakerTransitions *prometheus.CounterVec

	BuildInfo *prometheus.GaugeVec
}

// NewMetrics registers the platform instruments on a fresh registry.
//
// A private registry rather than the default one keeps a test binary from
// panicking on duplicate registration and keeps stray library metrics out of
// the scrape.
func NewMetrics(service, version, environment string) *Metrics {
	reg := prometheus.NewRegistry()

	// Latency buckets are chosen around the documented SLO thresholds in
	// docs/slo.md (p99 < 500ms). Buckets that do not bracket the objective make
	// the burn-rate query interpolate across a bucket edge and quietly lie.
	latencyBuckets := []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

	m := &Metrics{
		registry: reg,
		RequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total HTTP requests handled, by route and response class.",
		}, []string{"method", "route", "status", "status_class"}),
		RequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency in seconds.",
			Buckets: latencyBuckets,
		}, []string{"method", "route"}),
		RequestsInFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "http_requests_in_flight",
			Help: "HTTP requests currently being served.",
		}),
		ResponseSize: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_response_size_bytes",
			Help:    "HTTP response body size in bytes.",
			Buckets: prometheus.ExponentialBuckets(64, 4, 8),
		}, []string{"route"}),
		UpstreamRequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "upstream_requests_total",
			Help: "Calls to downstream services, by outcome.",
		}, []string{"upstream", "outcome"}),
		UpstreamRequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "upstream_request_duration_seconds",
			Help:    "Downstream call latency in seconds.",
			Buckets: latencyBuckets,
		}, []string{"upstream"}),
		UpstreamRetriesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "upstream_retries_total",
			Help: "Retries issued against downstream services.",
		}, []string{"upstream"}),
		BreakerState: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "circuit_breaker_state",
			Help: "Circuit breaker state (0=closed, 1=half_open, 2=open).",
		}, []string{"upstream"}),
		BreakerTransitions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "circuit_breaker_transitions_total",
			Help: "Circuit breaker state transitions.",
		}, []string{"upstream", "from", "to"}),
		BuildInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "service_build_info",
			Help: "Build metadata, always 1. Join on this to annotate dashboards by version.",
		}, []string{"service", "version", "environment"}),
	}

	reg.MustRegister(
		m.RequestsTotal,
		m.RequestDuration,
		m.RequestsInFlight,
		m.ResponseSize,
		m.UpstreamRequestsTotal,
		m.UpstreamRequestDuration,
		m.UpstreamRetriesTotal,
		m.BreakerState,
		m.BreakerTransitions,
		m.BuildInfo,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	m.BuildInfo.WithLabelValues(service, version, environment).Set(1)
	return m
}

// Registry exposes the underlying registry for the /metrics handler.
func (m *Metrics) Registry() *prometheus.Registry { return m.registry }
