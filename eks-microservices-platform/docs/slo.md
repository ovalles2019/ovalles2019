# Service level objectives

## Why these numbers

An SLO is only useful if it is a decision-making tool. The test is: does missing
it change what the team does next? If the answer is no, it is a vanity metric.

These are deliberately not 99.99%. Four nines is 4.3 minutes of budget per
month, which is less than one bad deploy — so it would be breached constantly,
and a permanently-breached objective is one everyone learns to ignore.

| Service | SLI | Objective | 30-day budget |
|---|---|---|---|
| gateway | Requests not returning 5xx | 99.9% | 43m 12s |
| gateway | Requests completing within 500ms | 99% | 7h 12m |
| catalog | Requests not returning 5xx | 99.9% | 43m 12s |
| scorer  | Requests not returning 5xx | 99.5% | 3h 36m |
| scorer  | Requests completing within 500ms | 99% | 7h 12m |

The scorer's availability objective is deliberately looser. It is the autoscaled
service, so it absorbs load spikes by adding pods — and a brief period of
saturation while the HPA reacts is expected behaviour, not an incident. Holding
it to the gateway's objective would page someone for the system working as
designed.

## What is deliberately not measured

**Uptime.** "Is the pod running" is not a user-visible property. A pod can be
running and serving errors.

**Latency as an average.** An average hides the tail, and the tail is what users
experience. Everything here is a percentile or a threshold count.

**Per-pod metrics as an objective.** Users do not experience pods.

## How the latency SLI is computed

From histogram bucket counts, not `histogram_quantile`:

```promql
(
  sum(rate(http_request_duration_seconds_count[5m]))
    -
  sum(rate(http_request_duration_seconds_bucket{le="0.5"}[5m]))
)
  / sum(rate(http_request_duration_seconds_count[5m]))
```

`histogram_quantile` interpolates *within* a bucket and is not aggregatable
across pods, so a fleet-wide p99 from it is an estimate of an estimate. Counting
requests above a bucket boundary is exact — provided a boundary sits exactly at
the objective, which is why `0.5` appears in the bucket list in
`internal/platform/telemetry/metrics.go` and in `services/scorer/app/metrics.py`.
Changing the objective without changing the buckets silently breaks this.

## Error budget policy

The budget is what the objective permits. At 99.9%, that is 0.1% of requests.

- **Budget remaining:** ship. The budget exists to be spent on change.
- **Budget exhausted:** feature work stops; reliability work only, until the
  trailing 30-day window recovers.
- **Two consecutive months exhausted:** the objective was wrong, or the system
  needs structural work. Revisit the objective explicitly rather than letting it
  quietly become aspirational.

## Alerting: burn rate, not thresholds

Alerts are defined in `deploy/observability/slo-rules.yaml`.

A naive "error rate > 1% for 5 minutes" alert has no concept of budget. It fires
on a two-minute blip that costs nothing, and it fires exactly as loudly for a
slow bleed that will exhaust a month of budget in three days. Both wake someone
up, so both eventually get ignored — and an ignored alert is worse than no
alert, because it creates the belief that something is watching.

Burn rate is the error rate divided by the rate the SLO can afford. Burn rate 1
spends the budget exactly over 30 days. Alerting on it means severity tracks how
fast the budget is actually being spent.

| Burn rate | Budget gone in | Long window | Short window | Action |
|---|---|---|---|---|
| 14.4x | ~2 days | 1h | 5m | Page |
| 6x | ~5 days | 6h | 30m | Page |
| 3x | ~10 days | 1d | 2h | Ticket |
| 1x | 30 days | 3d | 6h | Ticket |

Every alert pairs a long window with a short one. The long window establishes
that real budget is being spent; the short window confirms it is *still
happening*. Without the short window an alert keeps firing for hours after an
incident has resolved, because the long window still contains it.

## The alert that catches the silent failure

`NoTrafficReceived` exists because every ratio-based alert above is silent at
zero traffic — zero divided by zero is not a number, and the expression simply
does not fire. A total outage where nothing reaches the service at all therefore
looks exactly like perfect health on every SLO dashboard.
