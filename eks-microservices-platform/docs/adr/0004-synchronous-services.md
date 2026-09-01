# 4. Synchronous HTTP between services, not an event bus

Status: accepted

## Context

Three services need to talk. The reflex in a microservices project is to put a
message broker between them and call it decoupled.

## Decision

Synchronous HTTP, with timeouts, bounded retries and circuit breakers on every
call.

## Alternatives considered

**Kafka or SQS between the gateway and the scorer.** The scoring path is
request/response: a caller submits readings and wants a score back. Making that
asynchronous means inventing a correlation mechanism, a result store and a
polling or callback API — reconstructing request/response on top of a broker,
plus a broker to operate.

Asynchrony earns its complexity when the work outlives the request (batch
scoring, retraining, notification fan-out). None of that is in scope here, and
adding a broker to demonstrate that I can add a broker would be exactly the
kind of resume-driven design this project is trying to avoid.

## Consequences

- Failure is immediate and visible rather than deferred into a queue, which is
  why the resilience layer (`internal/platform/resilience`) is the most heavily
  tested code in the repository.
- The gateway degrades rather than failing when catalog is unavailable — see
  `Handler.ScoreReading`. That decision is where synchronous calls would
  otherwise make availability *worse* than a monolith.
- If a write path that outlives its request is ever added, this ADR is superseded
  rather than stretched.
