# Fleet Platform — production-grade microservices on EKS

A three-service platform on Kubernetes, built to demonstrate the parts of
platform engineering that only show up once a system has to survive contact with
production: graceful degradation, lossless rollouts, autoscaling that responds to
a real signal, policy enforced in CI, and infrastructure that can be reviewed
before it is applied.

**The whole Kubernetes layer runs locally, for free, in one command.** Only the
Terraform layer needs an AWS account.

```bash
make kind-up      # 3-node kind cluster, Calico, metrics-server, all 3 services
make load-test    # drive load until the scorer's HPA scales out
make kind-down
```

---

## What this is, and what it improves on

This started as a rebuild of a common tutorial-shaped EKS project: a WordPress
container packaged with `helm create`, a cluster created by an `eksctl` command
pasted into a README, and an `hpa.yaml` applied by hand. That project
demonstrates that Kubernetes can autoscale. It does not demonstrate the
decisions that make a platform work.

The differences are not "more features". They are the specific places where the
simple version breaks:

| | Tutorial approach | Here | Why it matters |
|---|---|---|---|
| Cluster | `eksctl create cluster` in a README | Terraform, remote state, locking | No state means nothing records what exists, no change can be reviewed, and a second engineer cannot work safely |
| Workload | One stock WordPress container | Three services with a real reason to be separate | "Microservices" with one service tests nothing about distribution |
| Autoscaling | HPA on a static web server | HPA on a CPU-bound scorer whose cost scales with payload | Load-testing nginx measures the load generator, not the service |
| HPA API | `autoscaling/v1`, no behaviour | `autoscaling/v2` with tuned scale-up and scale-down | Default behaviour visibly flaps on bursty load |
| Charts | Three copies of `helm create` | One library chart, hardened by default | An opt-in security control is one a service eventually ships without |
| Storage | RWO EBS + `LoadBalancer` + scale to 5 | Stateless services; one service owns the database | *That combination deadlocks*: five replicas cannot mount one ReadWriteOnce volume |
| Rollout | Rolling update, gated on readiness | Canary gated on the SLO metrics, auto-rollback | Readiness says "the process started", not "this version works" |
| Secrets | Whatever `kubectl create secret` did | External Secrets from AWS Secrets Manager | A credential in git is a credential in every clone, forever |
| Verification | Screenshots | 136 tests, strict schema validation, 1,914 policy checks | A screenshot proves it worked once, on one machine |

That last row is the one this project cares about most.

---

## Architecture

```
                    Ingress (one public entry point)
                              │
                    ┌─────────▼─────────┐
                    │      gateway      │   Go · API keys, per-client rate limit,
                    │                   │   circuit breakers, retries, OTel
                    └────┬─────────┬────┘
                         │         │
              enrichment │         │ scoring
                         │         │
              ┌──────────▼───┐  ┌──▼──────────────┐
              │   catalog    │  │     scorer      │
              │      Go      │  │ Python/FastAPI  │
              │  owns the DB │  │  CPU-bound,     │
              └──────┬───────┘  │  autoscaled     │
                     │          └─────────────────┘
              ┌──────▼───────┐
              │   Postgres   │
              └──────────────┘
```

Each service exists for a reason, and the reasons are different:

- **gateway** is I/O-bound and fans out. It is where resilience lives.
- **catalog** owns the database. Nothing else has a connection to it, which is
  what keeps the schema an implementation detail rather than a shared contract.
- **scorer** is stateless and CPU-bound. It is the only service that should
  autoscale, and the only one that does.

### The one design decision worth reading

`Handler.ScoreReading` in `internal/gateway/handler.go` decides what to do when
catalog is down but scorer is healthy.

Failing the whole request would be the obvious implementation — and it would
mean a non-essential enrichment path taking down the essential scoring path.
That is precisely how a microservice split makes availability *worse* than the
monolith it replaced: two services at 99.9% wired in series are 99.8%.

So the request succeeds, with `"degraded": true` and no device block. Scorer
being unavailable is different — there is no answer to give — and that is a real
503. The distinction is tested in both directions
(`TestScoreReadingDegradesWhenCatalogIsDown`,
`TestScoreReadingFailsWhenScorerIsDown`).

---

## What is actually verified

Everything below runs in `make verify` and in CI. This project deliberately
avoids claiming anything it cannot check.

```
$ make verify

gofmt / go vet                  clean
go test -race                   72 tests (126 with subtests), 4 packages
ruff check / ruff format        clean
pytest                          64 tests, 97% coverage
helm lint                       3 charts
kubeconform (strict, k8s 1.31)  75 resources, 0 invalid
conftest                        1,914 checks, 0 failures
conftest (negative fixture)     correctly rejected
promtool check rules            20 PromQL rules
terraform fmt -check            clean
terraform validate              valid against AWS provider 5.70
```

A few of these deserve explanation.

**The manifests are validated against real schemas**, not just parsed as YAML.
`kubeconform` checks all 75 rendered resources — 3 services × 3 environments,
plus the Argo and External Secrets manifests — against the Kubernetes 1.31 API
schemas and the actual CRD schemas for `PodMonitor`, `Rollout`,
`AnalysisTemplate`, `ApplicationSet` and `ExternalSecret`. This caught a genuine
bug during development: a duplicate `LOG_LEVEL` key in a rendered ConfigMap that
the API server would have rejected mid-deploy.

**The policies are tested in both directions.** 1,914 passing checks prove
nothing on their own — a rule with a typo in its path expression passes silently
forever. `make policy-negative` asserts that conftest still *rejects*
`policy/testdata/insecure-deployment.yaml`, which trips 21 distinct rules.

**Terraform is validated against the real provider**, not just formatted. About
1,250 lines of infrastructure across VPC, EKS, IAM, ECR and KMS, checked by
`terraform validate` with AWS provider 5.70 loaded.

### What is *not* verified here

Honesty matters more than the appearance of completeness:

- **No live EKS cluster was created.** The Terraform is validated, not applied.
  Running it costs roughly $122/month in dev (see `docs/cost.md`).
- **Images are not built in this environment** (no Docker daemon available).
  The Dockerfiles are written and reviewed but not executed here; CI builds them.
- **The load test's HPA behaviour has not been observed end to end.** The script
  is written against the real metrics and thresholds; running it needs a cluster.

Everything in the `make verify` list above was actually run.

---

## Layout

```
cmd/                       gateway and catalog binaries
internal/
  platform/                shared: config, health, httpx, resilience, telemetry
  gateway/                 auth, rate limiting, fan-out handler
  catalog/                 device domain, Postgres and in-memory repositories
services/scorer/           Python FastAPI service
deploy/
  charts/platform-common/  library chart — every template lives here
  charts/{gateway,catalog,scorer}/
  env/                     per-environment values + per-service image records
  argocd/                  ApplicationSet, AppProject, Rollout + AnalysisTemplates
  observability/           SLO rules, External Secrets
infra/terraform/           VPC, EKS, IAM, ECR, KMS, GitHub OIDC
policy/                    conftest security policies + negative fixture
local/                     kind cluster, docker compose, Prometheus, OTel
test/load/                 k6 profile designed to exercise the HPA
docs/adr/                  12 architecture decision records
docs/runbooks/             three incident runbooks
```

---

## Things worth looking at

If you only read a few files, these are the ones carrying the actual thinking:

**`internal/platform/resilience/`** — a hand-written circuit breaker and retry
policy, with the most thorough tests in the repository. The breaker deliberately
does not count caller cancellations as dependency failures (a client timeout
says nothing about the dependency's health), and retries use symmetric jitter
because unjittered retries synchronise every caller into a thundering herd.

**`deploy/charts/platform-common/templates/_defaults.tpl`** — documents two Helm
behaviours that are easy to lose an afternoon to: a library chart's
`values.yaml` is *not* merged into the parent release, and sprig's `merge`
treats `0` and `false` as absent, so an explicit zero override cannot be
expressed. The defaults are structured around that constraint.

**`policy/security.rego`** — every rule states the consequence of violating it,
not just the rule. A policy failure with no explanation gets suppressed.

**`docs/adr/0012-no-cpu-limits.md`** — why this platform sets memory limits but
not CPU limits, and why the two resources need opposite policies.

**`docs/slo.md`** — multi-window burn-rate alerting, and why the latency SLI is
computed from bucket counts rather than `histogram_quantile`.

---

## Running it

### Locally, with Kubernetes

```bash
make kind-up
kubectl -n fleet-platform port-forward svc/gateway 8080:8080

curl -s localhost:8080/api/v1/devices -H 'content-type: application/json' \
  -d '{"id":"pump-01","name":"West Pump","site":"west"}'

curl -s localhost:8080/api/v1/readings/score -H 'content-type: application/json' \
  -d '{"device_id":"pump-01","readings":[10,10.1,10,10.2,10,10.1,10,500]}'

kubectl -n fleet-platform get hpa scorer -w   # in another terminal
make load-test
```

The kind cluster has three workers with distinct zone labels and Calico rather
than kindnet — so the topology spread constraints and the default-deny
NetworkPolicies are actually enforced, instead of applying cleanly and doing
nothing.

### Locally, without Kubernetes

```bash
make dev     # docker compose: all services, Postgres, Prometheus, OTel
```

### On AWS

```bash
make tf-plan ENV=dev     # review before applying anything
terraform -chdir=infra/terraform apply -var-file=envs/dev.tfvars
```

Read `docs/cost.md` first, and set a billing alarm.

---

## Requirements

`make verify` needs: Go 1.25, Python 3.11+, helm 3.16, kubeconform, conftest,
terraform 1.9, promtool.
`make kind-up` additionally needs: docker, kind, kubectl.
