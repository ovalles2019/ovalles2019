# Runbook: error budget burn

**Alerts:** `ErrorBudgetBurnCritical`, `ErrorBudgetBurnWarning`, `CircuitBreakerOpen`

## What the alert means

Not "there are errors" — errors are always present at some rate. It means errors
are arriving fast enough that the 30-day budget will be exhausted before the
window closes. Critical implies roughly two days at the current rate.

## First: is it real?

```bash
# Which service, and how bad?
kubectl -n fleet-platform get pods -o wide

# Error rate by service and route over the last 15 minutes.
#   sum by (service, route) (rate(http_requests_total{status_class="5xx"}[15m]))
#     / sum by (service, route) (rate(http_requests_total[15m]))
```

If the rate is near zero on the current window, the alert has already resolved
and the long window has not caught up. The short window pairing should prevent
that; if it happens repeatedly, the short window is too long.

## Narrow it down

**1. Did something just deploy?**

```bash
kubectl -n fleet-platform rollout history deployment/<service>
argocd app history <service>-prod
```

The overwhelmingly most common cause. If a deploy correlates, roll back first
and diagnose after:

```bash
kubectl -n fleet-platform rollout undo deployment/<service>
# or, preferred, revert the commit so git stays the source of truth:
git revert <sha> && git push
```

For the gateway, the Rollout should already have aborted on its own. If it did
not, the analysis thresholds need looking at once the incident is over.

**2. Is one dependency responsible?**

```bash
kubectl -n fleet-platform port-forward svc/gateway 9090:9090
curl -s localhost:9090/../api/v1/dependencies   # via the public port: 8080
```

`CircuitBreakerOpen` firing alongside means the gateway has already decided a
dependency is unhealthy and is shedding load to it. That is the system working —
find out why the dependency is sick rather than why the breaker opened.

**3. Is it the database?**

Only the catalog talks to Postgres. If catalog is the failing service:

```bash
kubectl -n fleet-platform logs -l app.kubernetes.io/name=catalog --tail=100 | grep -i error
kubectl -n fleet-platform get pods -l app.kubernetes.io/name=catalog -o json \
  | jq '.items[].status.containerStatuses[].restartCount'
```

Connection exhaustion is the failure to rule out first — see
`docs/adr/0009-catalog-not-autoscaled.md` for why the replica count is capped.
Check `DB_MAX_CONNS x replicas` against the instance's `max_connections`.

**4. Is it one route, or all of them?**

A single route failing points at a code path. Everything failing points at a
dependency, a config change, or resource exhaustion.

## Note on degraded responses

A scoring response with `"degraded": true` is a **success**, not an error. It
means catalog was unavailable and the gateway served a correct score without
device enrichment — the deliberate behaviour described in
`docs/adr/0004-synchronous-services.md`. A rising degraded rate with a flat
error rate means catalog is struggling while the scoring path is fine.

## If nothing above explains it

```bash
# Correlate a specific failing request end to end.
kubectl -n fleet-platform logs -l app.kubernetes.io/part-of=fleet-platform \
  --tail=500 | jq -r 'select(.level=="error") | "\(.service) \(.request_id) \(.msg)"'
```

Every service logs `request_id` and `trace_id`. Take a failing request's ID and
follow it across all three services.

## Stopping the bleeding

In order of preference: revert the deploy; scale out if it is saturation
(`kubectl scale`); shed load by lowering `RATE_LIMIT_RPS`; disable graceful
degradation (`DEGRADE_ON_CATALOG_FAILURE=false`) only if degraded responses are
themselves causing harm downstream.
