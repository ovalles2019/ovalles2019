# 11. One uvicorn worker per scorer pod

Status: accepted

## Context

uvicorn can fork N worker processes per container. The scorer could run 4
workers in one pod, or 4 pods with 1 worker each.

## Decision

One worker per pod. The HPA scales pods.

## Reasoning

Two layers of concurrency make the signal the HPA reads much harder to reason
about. With multiple workers, a pod's CPU utilisation is an average across
processes that may be unevenly loaded, so "70% of the request" no longer
corresponds to any particular worker being busy — and the HPA is making
decisions from that number.

One worker per pod means pod CPU is worker CPU. The utilisation target maps
directly onto how busy the thing serving requests actually is, the scheduler can
bin-pack at a finer granularity, and a pod that wedges takes out one worker's
worth of capacity rather than four.

The usual argument for multiple workers — amortising interpreter and container
overhead — matters most when memory per process is large. The scorer's is not.

## Consequences

- The HPA's CPU signal is directly interpretable.
- Slightly more per-pod overhead in exchange for that clarity.
- `WORKERS` remains configurable, so this is a default rather than a constraint.
