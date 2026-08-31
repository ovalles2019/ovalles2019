# 9. The catalog is not autoscaled; the scorer is

Status: accepted

## Context

The reflex is to put an HPA on everything. It is the wrong reflex: horizontal
autoscaling helps when a service's bottleneck is its own CPU, and hurts when the
bottleneck is a resource shared across replicas.

## Decision

Only the scorer autoscales.

## Reasoning

**Scorer — autoscaled.** Stateless, CPU-bound, and its request cost is a
function of the payload (scoring a 2048-point window is real arithmetic). Adding
replicas adds capacity, linearly and with no shared contention. This is the
textbook case, and it is why the platform has a workload like this at all: an
HPA on a stock web server produces a graph that demonstrates the load generator,
not the service.

**Catalog — not autoscaled.** Its constraint is the database connection pool,
not CPU. The number that matters is:

```
DB_MAX_CONNS x replicas  <  the instance's max_connections
```

At `DB_MAX_CONNS=10` and a `db.t3.medium` (~100 connections, minus AWS's
reserved superuser slots and anything else connecting), the ceiling is around 8
replicas. An HPA scaling on CPU knows nothing about that ceiling. Under load it
would scale out, exhaust connections, and take down *every* replica — the
failure mode is strictly worse than the saturation it was trying to relieve.

Scaling the catalog means scaling the database first, or introducing a
connection pooler such as PgBouncer, which decouples replica count from
connection count. Either is a deliberate change, not something an autoscaler
should discover at 3am.

**Gateway — not autoscaled.** I/O-bound: it spends nearly all its time waiting
on catalog and scorer. Its replica count is set for availability, not
throughput.

## Consequences

- The catalog's capacity is a documented, deliberate ceiling rather than an
  emergent one.
- The `HPAAtMaxReplicas` alert covers the scorer running out of headroom.
- Raising catalog capacity requires reasoning about the database, which is
  exactly the reasoning that should happen.
