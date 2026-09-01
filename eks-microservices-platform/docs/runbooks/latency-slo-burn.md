# Runbook: latency objective burn

**Alert:** `LatencyBudgetBurnCritical`

## What it means

More than 1% of requests are taking longer than 500ms, fast enough to breach the
latency objective. The service is up and returning correct answers — slowly.

## Rule out saturation first

This is the most common cause and the easiest to confirm.

```bash
kubectl -n fleet-platform get hpa
kubectl -n fleet-platform top pods
```

If the scorer's HPA is at `maxReplicas`, it has no headroom and every further
request queues. That is `HPAAtMaxReplicas`, and the fix is capacity — see
`runbooks/scaling-saturation.md`.

## Is it this service, or something it calls?

```promql
# Time spent in downstream calls, by dependency.
histogram_quantile(0.99, sum by (le, upstream) (rate(upstream_request_duration_seconds_bucket[5m])))
```

Compare against the service's own request duration. If downstream latency
accounts for most of it, the problem is one hop further down; repeat there.

## Check for CPU throttling

This platform sets no CPU limits precisely to avoid this
(`docs/adr/0012-no-cpu-limits.md`), so a non-zero value here means a limit was
reintroduced somewhere:

```promql
rate(container_cpu_cfs_throttled_seconds_total{namespace="fleet-platform"}[5m])
```

Throttling produces latency with *no* corresponding rise in CPU usage, because
the container was prevented from using more. It is easy to misdiagnose as a slow
dependency.

## Check the retry budget

A retry storm turns one slow dependency into a much slower one:

```promql
rate(upstream_retries_total[5m]) / rate(upstream_requests_total[5m])
```

A ratio approaching `UPSTREAM_RETRY_ATTEMPTS` means nearly every call is being
retried. The circuit breaker should be cutting that off — if it is not, the trip
threshold is too lenient for this failure mode.

## Check payload size

The scorer's cost is a function of window length by design:

```promql
histogram_quantile(0.99, rate(scorer_window_size_bucket[5m]))
```

A client that started sending 4096-point windows instead of 128-point ones will
raise latency without anything having broken.

## Mitigations

Scale out (raise `maxReplicas` if the HPA is pinned); reduce
`UPSTREAM_TIMEOUT` so slow calls fail fast rather than occupying capacity; lower
`RATE_LIMIT_RPS` to shed load deliberately rather than degrading for everyone.
