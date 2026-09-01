# 8. In-process rate limiting, not a shared store

Status: accepted

## Context

The gateway limits requests per client. The limiter state can live in each
replica's memory, or in a shared store such as Redis.

## Decision

An in-process token bucket per replica, keyed by authenticated client.

## Reasoning

With N replicas, each enforces `limit/N` and the aggregate is approximately the
intended limit. The approximation degrades when load is unevenly distributed
across replicas — but the Service load-balances, so it mostly is not.

A Redis-backed limiter is exact, and pays for it with a network round trip on
the hot path of every request and a new dependency whose failure has to be
designed for: fail open, and the limit silently stops existing; fail closed, and
a Redis blip becomes a total outage. Neither is attractive for a control whose
purpose is to protect against abuse rather than to meter billing.

Two implementation details matter more than the exactness question. The limiter
is keyed by authenticated client rather than source IP, because behind a load
balancer every request appears to come from a handful of NAT addresses and an
IP-keyed limit is meaningless. And idle buckets are evicted — a limiter keyed by
client identity that never evicts is an unbounded map an attacker fills by
rotating keys until the pod is OOMKilled.

## Consequences

- No dependency, no added latency, no new failure mode.
- The effective limit varies with replica count, which is acceptable for abuse
  protection and would not be for billing.
- If exact global limiting is ever needed, this ADR is superseded.
