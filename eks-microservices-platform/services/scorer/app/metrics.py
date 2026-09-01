"""Per-instance Prometheus instruments.

Each application instance owns a fresh registry rather than sharing the process
global one. That mirrors internal/platform/telemetry in the Go services and
buys the same two things: a reimported module or a second app in the same test
process cannot raise a duplicate-timeseries error, and the scrape contains only
this service's metrics rather than whatever any imported library happened to
register.
"""

from __future__ import annotations

from dataclasses import dataclass

from prometheus_client import CollectorRegistry, Counter, Gauge, Histogram

# Matches the Go services' buckets so a single recording rule spans all three
# services. Buckets must bracket the SLO threshold in docs/slo.md or the
# burn-rate query interpolates across a bucket edge and quietly misreports.
LATENCY_BUCKETS = (0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10)


@dataclass(frozen=True)
class ScorerMetrics:
    registry: CollectorRegistry
    requests: Counter
    latency: Histogram
    in_flight: Gauge
    scored: Counter
    window_size: Histogram
    build_info: Gauge


def build_metrics(service: str, version: str, environment: str) -> ScorerMetrics:
    registry = CollectorRegistry()

    metrics = ScorerMetrics(
        registry=registry,
        requests=Counter(
            "http_requests_total",
            "Total HTTP requests handled, by route and response class.",
            # route is the registered path template, never the raw path: a
            # label carrying user input creates one series per distinct value.
            ["method", "route", "status", "status_class"],
            registry=registry,
        ),
        latency=Histogram(
            "http_request_duration_seconds",
            "HTTP request latency in seconds.",
            ["method", "route"],
            buckets=LATENCY_BUCKETS,
            registry=registry,
        ),
        in_flight=Gauge(
            "http_requests_in_flight",
            "HTTP requests currently being served.",
            registry=registry,
        ),
        scored=Counter(
            "scorer_windows_scored_total",
            "Windows scored, by outcome.",
            ["outcome"],
            registry=registry,
        ),
        window_size=Histogram(
            "scorer_window_size",
            "Readings per scoring request; request CPU cost tracks this, "
            "which is what makes the HPA signal meaningful.",
            buckets=(8, 32, 128, 512, 1024, 2048, 4096),
            registry=registry,
        ),
        build_info=Gauge(
            "service_build_info",
            "Build metadata, always 1. Join on this to annotate dashboards by version.",
            ["service", "version", "environment"],
            registry=registry,
        ),
    )

    metrics.build_info.labels(service, version, environment).set(1)
    return metrics
