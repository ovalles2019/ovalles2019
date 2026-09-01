{{/*
Platform-wide default values.

These live in a template rather than in the library chart's values.yaml because
Helm does not merge a library chart's values into the parent release — only the
consuming chart's values.yaml is read. A library chart that puts its defaults in
values.yaml therefore renders with every field nil, which is a genuinely
confusing failure the first time you meet it.

`platform.init` merges these underneath the service's own values. sprig's
`merge` mutates its first argument and lets existing keys win, so the service's
values always take precedence and only unset fields fall back to a default.

One sharp edge governs how these defaults are chosen. sprig's `merge` is
mergo underneath, and mergo treats a zero value — 0, false, "" — as absent.
A service that sets `adminPort: 0` to mean "no admin listener" therefore has
it silently replaced by the default, and there is no sprig function that can
express an explicit zero override.

So every default here is written so that the zero value is the *default* and
any meaningful setting is an opt-in: `adminPort` defaults to 0 and the two Go
services set 9090, rather than defaulting to 9090 and having the scorer try to
unset it. Read that as a constraint the templating language imposes, not a
stylistic preference.
*/}}
{{- define "platform.init" -}}
{{- $defaults := include "platform.defaultValues" . | fromYaml -}}
{{- $_ := merge .Values $defaults -}}
{{- end -}}

{{- define "platform.defaultValues" -}}
# Platform-wide defaults.
#
# Service charts inherit these and override only what genuinely differs. Every
# security control is on by default: a control a service has to opt into is one
# a service will eventually ship without.

global:
  environment: dev
  imageRegistry: ""
  partOf: fleet-platform

nameOverride: ""
fullnameOverride: ""
component: service

replicaCount: 2
revisionHistoryLimit: 5

rollingUpdate:
  # Never drop below the desired replica count during a rollout.
  maxUnavailable: 0
  maxSurge: 1

image:
  registry: ""
  repository: ""
  tag: ""
  # A digest, when set, overrides the tag and makes the rollout immutable.
  digest: ""
  pullPolicy: IfNotPresent

imagePullSecrets: []

serviceAccount:
  create: true
  name: ""
  # Off by default: only a workload that calls the Kubernetes API needs a token,
  # and mounting one everywhere hands every container a cluster credential.
  automount: false
  annotations: {}

service:
  # ClusterIP everywhere. Exactly one entry point is public, and it is an
  # Ingress in front of the gateway — not a LoadBalancer Service per workload,
  # which bills for an ELB each and puts internal services on the internet.
  type: ClusterIP
  port: 8080
  targetPort: 8080
  annotations: {}

# 0 means "no separate admin listener": probes and /metrics are served on the
# service port. Services with a second listener set this to 9090. See the note
# above on why the default has to be the zero value.
adminPort: 0

# Pod-level security context.
podSecurityContext:
  runAsNonRoot: true
  runAsUser: 10001
  runAsGroup: 10001
  fsGroup: 10001
  seccompProfile:
    # Blocks the ~300 syscalls a normal workload never issues, which is the
    # cheapest meaningful reduction in kernel attack surface available.
    type: RuntimeDefault

# Container-level security context. These four settings together are what the
# restricted Pod Security Standard requires.
containerSecurityContext:
  allowPrivilegeEscalation: false
  privileged: false
  readOnlyRootFilesystem: true
  runAsNonRoot: true
  capabilities:
    drop:
      - ALL

tmpSizeLimit: 64Mi

resources:
  requests:
    # The request is what the scheduler packs against and what HPA utilisation
    # is a percentage of. Setting it too high wastes half the cluster; too low
    # and the pod is throttled under exactly the load that should scale it out.
    cpu: 100m
    memory: 128Mi
  limits:
    # No CPU limit by default. A CPU limit throttles a container that is within
    # its budget as soon as it bursts, adding tail latency for no isolation
    # benefit that requests do not already provide. Memory is limited because
    # memory is not compressible: without a limit one leaking pod takes the node
    # and everything on it. See docs/adr/0012-no-cpu-limits.md.
    memory: 256Mi

goMaxProcsFromLimit: false

probes:
  # 0 defers to platform.monitoringPort, which resolves to adminPort when a
  # service has one and to the service port otherwise.
  port: 0
  startupPath: /startupz
  livenessPath: /healthz
  readinessPath: /readyz
  startup:
    # 30 x 2s = up to a minute to boot before the pod is declared failed.
    periodSeconds: 2
    failureThreshold: 30
    timeoutSeconds: 2
  liveness:
    periodSeconds: 10
    # Three strikes, not one: a single missed probe on a busy node is noise, and
    # restarting on it turns a latency blip into an outage.
    failureThreshold: 3
    timeoutSeconds: 3
  readiness:
    periodSeconds: 5
    failureThreshold: 3
    successThreshold: 1
    timeoutSeconds: 3

# Must exceed the application's own drain delay plus its shutdown grace, or the
# kubelet SIGKILLs the process mid-drain.
terminationGracePeriodSeconds: 45

autoscaling:
  enabled: false
  minReplicas: 2
  maxReplicas: 10
  targetCPUUtilizationPercentage: 70
  targetMemoryUtilizationPercentage: null
  customMetrics: []
  behavior:
    scaleUp:
      # Short window: under a real spike, waiting is the expensive option.
      stabilizationWindowSeconds: 30
      percent: 100
      pods: 4
    scaleDown:
      # Long window: the controller takes the highest recommendation over this
      # period, so a brief dip cannot remove capacity a later spike needs.
      stabilizationWindowSeconds: 300
      percent: 50

podDisruptionBudget:
  enabled: true
  # Must be strictly below the minimum replica count or no pod can ever be
  # evicted and node drains hang; the template enforces that.
  minAvailable: 1

topologySpreadConstraints:
  - maxSkew: 1
    topologyKey: topology.kubernetes.io/zone
    # ScheduleAnyway, not DoNotSchedule: a hard constraint means a pod stays
    # Pending when a zone is unavailable, so a zone outage becomes a capacity
    # outage. Best-effort spreading gives the availability benefit without that.
    whenUnsatisfiable: ScheduleAnyway
  - maxSkew: 1
    topologyKey: kubernetes.io/hostname
    whenUnsatisfiable: ScheduleAnyway

networkPolicy:
  # Default-deny plus an explicit allowlist. Kubernetes' own default is a flat
  # network where any pod can reach the database directly.
  enabled: true
  allowFrom: []
  allowTo: []
  allowExternalCIDRs: []
  allowMonitoring: true
  monitoringNamespace: monitoring
  allowIngressController: false
  ingressNamespace: ingress-nginx

metrics:
  path: /metrics
  podMonitor:
    enabled: false
    interval: 30s
    scrapeTimeout: 10s
    labels: {}

logLevel: info

# Non-secret configuration, rendered into a ConfigMap and hashed into the pod
# template so a change actually rolls the pods.
config: {}

# Plain environment variables.
env: {}

# Secret-derived environment variables. Values never appear in this file or in
# git: External Secrets syncs them from AWS Secrets Manager into a Secret, and
# this references that Secret by name.
envFromSecret: []

extraEnvFrom: []
extraVolumes: []
extraVolumeMounts: []

podAnnotations: {}
podLabels: {}
deploymentAnnotations: {}
nodeSelector: {}
tolerations: []
affinity: {}
priorityClassName: ""

{{- end -}}
