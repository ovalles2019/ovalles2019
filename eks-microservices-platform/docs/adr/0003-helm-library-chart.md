# 3. One library chart, not three copies of `helm create`

Status: accepted

## Context

`helm create` produces about 200 lines of scaffold per service. With three
services that is three copies, and they immediately begin to diverge: a fix to
one service's probe configuration does not reach the other two.

The scaffold is also unhardened by default. Its `securityContext` is an empty
map with commented-out suggestions, its `autoscaling` block is disabled while a
separate hand-written `hpa.yaml` is applied out of band, and its probes point
liveness and readiness at the same path.

## Decision

A library chart, `platform-common`, owns every template. Each service chart is
a `Chart.yaml`, a `values.yaml` of genuine differences, and eight one-line
templates that each call an included definition.

Security controls are defaults in the library, not per-service opt-ins. A
control a service has to opt into is one a service will eventually ship without.

## Consequences

- A platform-wide change is one edit. Adding topology spread constraints to
  every service was a single commit.
- Each service's `values.yaml` reads as a statement of what makes that service
  different, which is genuinely useful documentation.
- Two Helm behaviours had to be worked around, both documented in
  `_defaults.tpl`: a library chart's `values.yaml` is not merged into the parent
  release at all, and sprig's `merge` treats zero values as absent so an
  explicit `0` or `false` override is impossible to express.
- Cost: a level of indirection. Reading a service's rendered output requires
  knowing the library exists. `helm template` makes that recoverable.
