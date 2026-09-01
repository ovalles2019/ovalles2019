{{/*
Naming and labelling helpers.

Every object the platform creates carries the same label set, which is what
makes `kubectl get all -l app.kubernetes.io/part-of=fleet-platform` return the
whole system and lets one NetworkPolicy or PodDisruptionBudget selector be
written once and stay correct.
*/}}

{{- define "platform.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Fullname is the object name prefix.

Truncated to 63 characters because it becomes a label value and, via the
Service, a DNS label. A name that overflows produces an object the API server
rejects at apply time, which is a confusing failure to debug in CI.
*/}}
{{- define "platform.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "platform.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Selector labels are the immutable subset.

A Deployment's selector cannot be changed after creation, so version must never
appear here: a chart bump would otherwise produce an invalid update that fails
mid-rollout and needs the Deployment deleted to recover.
*/}}
{{- define "platform.selectorLabels" -}}
app.kubernetes.io/name: {{ include "platform.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "platform.labels" -}}
helm.sh/chart: {{ include "platform.chart" . }}
{{ include "platform.selectorLabels" . }}
app.kubernetes.io/version: {{ .Values.image.tag | default .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: {{ .Values.global.partOf | default "fleet-platform" }}
app.kubernetes.io/component: {{ .Values.component | default "service" }}
{{- with .Values.global.environment }}
platform.fleet.io/environment: {{ . | quote }}
{{- end }}
{{- end -}}

{{- define "platform.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "platform.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
The port Prometheus scrapes and probes are polled on.

A service with a second listener sets adminPort, so probes and /metrics are
never reachable through the Ingress. A single-listener runtime (uvicorn serves
one app on one port) leaves adminPort unset and everything resolves to the
service port, rather than rendering a probe against a port nothing is
listening on.
*/}}
{{- define "platform.monitoringPort" -}}
{{- if .Values.adminPort -}}
{{- .Values.adminPort -}}
{{- else -}}
{{- .Values.service.targetPort -}}
{{- end -}}
{{- end -}}

{{/*
Name of the container port Prometheus scrapes.

Derived rather than configured: it is always determined by whether the service
has a separate admin listener, so it cannot be set inconsistently with
adminPort.
*/}}
{{- define "platform.monitoringPortName" -}}
{{- if .Values.adminPort -}}admin{{- else -}}http{{- end -}}
{{- end -}}

{{/*
Resolve the image reference.

A digest, when supplied, wins over the tag. Deploying by digest is what makes a
rollout immutable: a tag can be repointed at different content after the fact,
so two pods from the "same" release can be running different code.
*/}}
{{- define "platform.image" -}}
{{- $registry := .Values.image.registry | default .Values.global.imageRegistry -}}
{{- $repository := .Values.image.repository -}}
{{- if .Values.image.digest -}}
{{- if $registry -}}
{{- printf "%s/%s@%s" $registry $repository .Values.image.digest -}}
{{- else -}}
{{- printf "%s@%s" $repository .Values.image.digest -}}
{{- end -}}
{{- else -}}
{{- $tag := .Values.image.tag | default .Chart.AppVersion -}}
{{- if $registry -}}
{{- printf "%s/%s:%s" $registry $repository $tag -}}
{{- else -}}
{{- printf "%s:%s" $repository $tag -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Fail the render when a production release is missing a pinned image.

Catching this at `helm template` time in CI is the difference between a blocked
pipeline and an environment where nobody can say which commit is running.
*/}}
{{- define "platform.validateImage" -}}
{{- if and (eq (.Values.global.environment | default "dev") "prod") (not .Values.image.digest) -}}
{{- if or (eq (.Values.image.tag | default "") "latest") (eq (.Values.image.tag | default "") "") -}}
{{- fail (printf "%s: a production release requires image.digest or an explicit non-latest image.tag" .Chart.Name) -}}
{{- end -}}
{{- end -}}
{{- end -}}
