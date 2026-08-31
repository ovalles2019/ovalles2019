# 12. Memory limits, but no CPU limits

Status: accepted

## Context

Kubernetes lets a container declare a request (what the scheduler reserves) and
a limit (a hard ceiling). The habit is to set both for both resources.

## Decision

Requests for CPU and memory. A limit for memory only.

## Reasoning

The two resources behave completely differently under pressure, and the same
policy is wrong for both.

**CPU is compressible.** Exceeding a CPU limit does not fail anything; the
kernel's CFS quota throttles the container. That throttling is applied within
each 100ms period, so a container that briefly needs more than its limit is
stalled for the remainder of the period even when the node is otherwise idle.
The result is tail latency with no corresponding signal on the CPU graphs — the
container looks like it is using less than its limit, because it was prevented
from using more.

CPU *requests* already provide the isolation that matters. Under contention the
scheduler apportions CPU in proportion to requests, so a noisy neighbour cannot
starve a well-behaved pod. The limit adds throttling without adding protection.

This matters most for the scorer: throttling a scoring pod at its limit adds
latency during exactly the spike the HPA is trying to respond to.

**Memory is not compressible.** There is no throttling; a process either has the
page or it does not. Without a limit, one leaking pod expands until the node
runs out and the kubelet begins evicting arbitrary pods — so one service's bug
takes down unrelated services on the same node. A memory limit converts that
into an OOMKill of the pod actually responsible.

## Consequences

- No CFS throttling, so latency reflects real work.
- A memory leak is contained to the pod that has it.
- Cost: CPU usage can burst above the request when the node has spare capacity,
  so node-level CPU is less predictable. Requests keep it bounded under
  contention, which is when it matters.
- This is a well-known but not universal position. In a hard multi-tenant
  cluster with untrusted workloads, CPU limits are defensible; this cluster runs
  one team's services.
