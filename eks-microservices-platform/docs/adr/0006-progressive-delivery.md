# 6. Canary with automated analysis, not a plain rolling update

Status: accepted

## Context

A Kubernetes rolling update is gated only on the readiness probe. A build that
starts, passes readiness and then returns 500s on a code path the probe never
touches will roll out to 100% of traffic without complaint. Readiness answers
"is the process up", not "is this version behaving".

The gap between "deployed" and "someone noticed" is then however long it takes
an alert to fire and a human to respond — minutes at best, at full traffic.

## Decision

The gateway deploys as an Argo Rollout with a canary strategy. Traffic moves
10% → 30% → 60% → 100%, and each promotion is gated on an AnalysisTemplate that
queries the same Prometheus metrics the SLO is computed from. A regression
aborts the rollout and shifts traffic back automatically.

## Consequences

- A bad build affects roughly 10% of traffic for about five minutes rather than
  100% of traffic until someone intervenes.
- Rollback is automatic and needs no human.
- Two details that matter and are easy to get wrong, both handled in
  `deploy/argocd/rollout-gateway.yaml`: the analysis queries guard their
  denominator with `or vector(1)`, because a canary serving no traffic yields
  `0/0` = NaN and aborts a perfectly good rollout during a quiet period; and
  `failureLimit` is 2 rather than 0, because aborting on a single scrape gap
  makes every deploy a coin flip and the team turns the analysis off.
- Cost: a deploy takes ~25 minutes instead of ~2. That is the correct trade for
  the edge service; the internal services use ordinary rolling updates.
