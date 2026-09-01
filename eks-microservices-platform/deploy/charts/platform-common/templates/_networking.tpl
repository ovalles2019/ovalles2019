{{/*
Service, ServiceAccount, ConfigMap and NetworkPolicy.
*/}}

{{- define "platform.service" -}}
{{- include "platform.init" . -}}
apiVersion: v1
kind: Service
metadata:
  name: {{ include "platform.fullname" . }}
  labels:
    {{- include "platform.labels" . | nindent 4 }}
  {{- with .Values.service.annotations }}
  annotations:
    {{- toYaml . | nindent 4 }}
  {{- end }}
spec:
  type: {{ .Values.service.type }}
  ports:
    - port: {{ .Values.service.port }}
      targetPort: http
      protocol: TCP
      name: http
  {{- /*
    The admin port is deliberately absent from the Service. Probes are polled by
    the kubelet directly on the pod IP and the ServiceMonitor scrapes pod
    endpoints, so nothing needs /metrics routable through a Service — and
    leaving it off keeps route names, versions and traffic volumes from being
    readable by anything that can reach the service.
  */}}
  selector:
    {{- include "platform.selectorLabels" . | nindent 4 }}
{{- end -}}

{{- define "platform.serviceaccount" -}}
{{- include "platform.init" . -}}
{{- if .Values.serviceAccount.create -}}
apiVersion: v1
kind: ServiceAccount
metadata:
  name: {{ include "platform.serviceAccountName" . }}
  labels:
    {{- include "platform.labels" . | nindent 4 }}
  {{- with .Values.serviceAccount.annotations }}
  annotations:
    {{- /*
      IRSA binds an IAM role to this ServiceAccount through the cluster's OIDC
      provider, so pods get short-lived, per-service AWS credentials from the
      token they already mount. The alternative — node instance-profile
      permissions — grants every pod on the node the union of what any of them
      needs.
    */}}
    {{- toYaml . | nindent 4 }}
  {{- end }}
automountServiceAccountToken: {{ .Values.serviceAccount.automount | default false }}
{{- end -}}
{{- end -}}

{{- define "platform.configmap" -}}
{{- include "platform.init" . -}}
{{- /*
  The platform-derived keys and the service's own `config` map are merged into
  one dict before rendering, rather than emitted as two blocks.

  Emitting them separately produces a duplicate key whenever a service or an
  environment overlay sets something the platform also sets — LOG_LEVEL is the
  obvious one — and a ConfigMap with a repeated key is invalid YAML that the API
  server rejects at apply time. `merge` lets the first argument win, so an
  explicit `config` entry overrides the platform default.
*/}}
{{- $platform := dict
      "SERVICE_VERSION" (.Values.image.tag | default .Chart.AppVersion)
      "ENVIRONMENT" (.Values.global.environment | default "dev")
      "LOG_LEVEL" (.Values.logLevel | default "info")
-}}
{{- $data := merge (deepCopy (.Values.config | default dict)) $platform -}}
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ include "platform.fullname" . }}
  labels:
    {{- include "platform.labels" . | nindent 4 }}
data:
  {{- range $key, $value := $data }}
  {{ $key }}: {{ $value | quote }}
  {{- end }}
{{- end -}}

{{/*
NetworkPolicy: default-deny with an explicit allowlist.

Kubernetes' default is a flat network where any pod can reach any other pod in
the cluster. That means one compromised container can talk straight to the
database, and lateral movement is free. A policy that selects a pod and lists no
ingress rules denies all ingress to it, so the two policies below establish
"nothing may reach this service unless named here".
*/}}
{{- define "platform.networkpolicy" -}}
{{- include "platform.init" . -}}
{{- if .Values.networkPolicy.enabled -}}
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: {{ include "platform.fullname" . }}
  labels:
    {{- include "platform.labels" . | nindent 4 }}
spec:
  podSelector:
    matchLabels:
      {{- include "platform.selectorLabels" . | nindent 6 }}
  policyTypes:
    - Ingress
    - Egress
  ingress:
    {{- /*
      Callers are named by label, not by IP: pod IPs change on every restart and
      a CIDR-based rule silently stops matching.
    */}}
    {{- range .Values.networkPolicy.allowFrom }}
    - from:
        - podSelector:
            matchLabels:
              app.kubernetes.io/name: {{ .name }}
          {{- with .namespace }}
          namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: {{ . }}
          {{- end }}
      ports:
        - protocol: TCP
          port: {{ $.Values.service.targetPort }}
    {{- end }}
    {{- if .Values.networkPolicy.allowMonitoring }}
    {{- /*
      Prometheus scrapes the admin port from its own namespace; without this
      rule every target goes down the moment the policy is applied.
    */}}
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: {{ .Values.networkPolicy.monitoringNamespace | default "monitoring" }}
      ports:
        - protocol: TCP
          port: {{ include "platform.monitoringPort" . }}
    {{- end }}
    {{- if .Values.networkPolicy.allowIngressController }}
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: {{ .Values.networkPolicy.ingressNamespace | default "ingress-nginx" }}
      ports:
        - protocol: TCP
          port: {{ .Values.service.targetPort }}
    {{- end }}
  egress:
    {{- /*
      DNS is allowed unconditionally. Omitting it is the classic mistake: every
      other egress rule is written by name, and without resolution none of them
      can ever match, so the pod appears to have no network at all.
    */}}
    - to:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: kube-system
          podSelector:
            matchLabels:
              k8s-app: kube-dns
      ports:
        - protocol: UDP
          port: 53
        - protocol: TCP
          port: 53
    {{- range .Values.networkPolicy.allowTo }}
    - to:
        - podSelector:
            matchLabels:
              app.kubernetes.io/name: {{ .name }}
          {{- with .namespace }}
          namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: {{ . }}
          {{- end }}
      ports:
        - protocol: TCP
          port: {{ .port }}
    {{- end }}
    {{- with .Values.networkPolicy.allowExternalCIDRs }}
    {{- range . }}
    - to:
        - ipBlock:
            cidr: {{ .cidr }}
      ports:
        {{- range .ports }}
        - protocol: TCP
          port: {{ . }}
        {{- end }}
    {{- end }}
    {{- end }}
{{- end -}}
{{- end -}}

{{/*
PodMonitor for the Prometheus Operator.

A PodMonitor rather than a ServiceMonitor for two reasons. The admin port is
deliberately not published on the Service, and a ServiceMonitor can only scrape
ports the Service exposes. And scraping through a Service load-balances across
replicas, so a per-pod counter becomes an unusable mixture of several pods'
values; a PodMonitor targets each pod endpoint directly.
*/}}
{{- define "platform.podmonitor" -}}
{{- include "platform.init" . -}}
{{- if .Values.metrics.podMonitor.enabled -}}
apiVersion: monitoring.coreos.com/v1
kind: PodMonitor
metadata:
  name: {{ include "platform.fullname" . }}
  labels:
    {{- include "platform.labels" . | nindent 4 }}
    {{- with .Values.metrics.podMonitor.labels }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
spec:
  selector:
    matchLabels:
      {{- include "platform.selectorLabels" . | nindent 6 }}
  podTargetLabels:
    - app.kubernetes.io/name
    - app.kubernetes.io/component
  podMetricsEndpoints:
    - port: {{ include "platform.monitoringPortName" . }}
      path: {{ .Values.metrics.path | default "/metrics" }}
      interval: {{ .Values.metrics.podMonitor.interval | default "30s" }}
      scrapeTimeout: {{ .Values.metrics.podMonitor.scrapeTimeout | default "10s" }}
{{- end -}}
{{- end -}}
