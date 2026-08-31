# Runbook: autoscaler saturation

**Alerts:** `HPAAtMaxReplicas`, `HPAUnableToScale`

## HPAAtMaxReplicas

The autoscaler has no headroom. Further load degrades latency directly, because
there is nothing left to scale.

```bash
kubectl -n fleet-platform get hpa scorer
kubectl -n fleet-platform describe hpa scorer   # the scaling decisions and why
```

**Is the load legitimate?** Check request rate against the previous week. A
sudden step change from one client is more likely abuse or a client bug than
organic growth — the per-client rate limit exists for this, and
`RATE_LIMIT_RPS` can be lowered.

**If it is legitimate:** raise `autoscaling.maxReplicas` in the environment
overlay and commit. Then confirm the cluster can actually place the pods —
raising the ceiling achieves nothing if nodes are full, which turns this alert
into `HPAUnableToScale`.

## HPAUnableToScale

The HPA wants more replicas and cannot get them. Almost always the cluster, not
the HPA.

```bash
kubectl -n fleet-platform get pods --field-selector=status.phase=Pending
kubectl -n fleet-platform describe pod <pending-pod> | tail -20
```

The events at the bottom name the reason.

**Insufficient cpu/memory:** the cluster is full. Node groups scale on their own
but not instantly; check whether the node group is at its own `max_size`.

**Node affinity / taint mismatch:** the pod cannot go on the nodes that have
room. Application workloads must tolerate the `application` node group; the
`system` group is tainted deliberately (`docs/adr/0007-spot-and-cost.md`).

**Topology constraints:** the platform uses `whenUnsatisfiable: ScheduleAnyway`
everywhere precisely so this cannot block scheduling. If a pod is Pending on a
topology constraint, someone changed one to `DoNotSchedule`.

**No spot capacity:** the spot pool has no instances available. The node groups
list four instance types across two families to make this unlikely; if it
happens anyway, temporarily adding an on-demand group is the fastest route.

## The HPA reports `<unknown>` for a metric

metrics-server is not running or cannot scrape. The HPA cannot make any decision
at all in this state — it does not scale down, it simply does nothing.

```bash
kubectl -n kube-system get deployment metrics-server
kubectl top nodes    # fails the same way if metrics-server is broken
```

## Everything looks fine but replicas are flapping

Check `scaleDown.stabilizationWindowSeconds` — 300s in this platform. Under 60s,
the HPA reclaims pods after every short lull and immediately needs them back.
The conftest policy rejects anything under 60s for exactly this reason.
