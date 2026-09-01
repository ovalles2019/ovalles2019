# 1. Record architecture decisions

Status: accepted

## Context

The interesting content of an infrastructure project is not the YAML. It is the
set of choices behind it — and those are invisible in the final state. A reader
looking at `autoscaling.enabled: false` on the catalog cannot tell whether that
was reasoned about or never considered.

That gap is also where infrastructure projects rot. Six months later nobody
remembers why the catalog is not autoscaled, so someone "fixes" it, and the
database runs out of connections during the next traffic peak.

## Decision

Every decision with a real alternative gets a short record here: the context,
the choice, what was rejected, and the consequences that were accepted.

Code comments carry the *local* reason ("utilisation is a percentage of the
request, not the limit"). ADRs carry the *systemic* one, where the reasoning is
longer than a comment and spans several files.

## Consequences

- A reviewer can evaluate the reasoning, not just the result.
- A future change that contradicts an ADR is visible as a contradiction.
- ADRs are immutable once accepted. A reversal is a new ADR that supersedes the
  old one, so the history of the system's thinking survives.
