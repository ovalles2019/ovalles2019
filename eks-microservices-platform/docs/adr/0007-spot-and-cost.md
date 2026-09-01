# 7. Spot instances for application workloads, on-demand for system

Status: accepted

## Context

Compute is most of a cluster's bill. Spot is roughly 70% cheaper, with the
caveat that a node can be reclaimed on two minutes' notice.

## Decision

Two node groups. `system` is on-demand and tainted; `application` is spot across
four instance types in two families.

## Reasoning

A spot reclaim is a node drain, and this platform is already built to survive
node drains: every service has a PodDisruptionBudget, topology spread across
zones and nodes, `maxUnavailable: 0` on rollouts, and a graceful shutdown that
fails readiness before it stops accepting. An interruption costs a
rescheduling, not availability.

That reasoning does not extend to cluster-wide components. A spot reclaim that
takes both CoreDNS replicas breaks name resolution for every pod at once, and
nothing else works while it does. Those run on-demand, and the taint keeps
ordinary workloads from consuming the capacity bought for them.

Four instance types, not one: a spot pool restricted to a single type is far
more likely to be simultaneously unavailable, and the resulting capacity failure
looks like a cluster problem rather than a market one.

## Consequences

- Roughly 60-70% off application compute.
- The availability configuration is load-bearing rather than decorative, and it
  is exercised continuously in normal operation rather than only during an
  upgrade.
- Cost: nodes disappear routinely. Anything that assumes a stable node identity
  or local state would break, which is a constraint worth stating explicitly.
