{{/*
HorizontalPodAutoscaler, autoscaling/v2.

The reference this project improves on used autoscaling/v1, which supports a
single CPU target and no scaling behaviour. v2 is what allows multiple metrics
and, more importantly, an explicit policy for how fast to scale in each
direction — without which the controller's defaults produce visible flapping.
*/}}
{{- define "platform.hpa" -}}
{{- include "platform.init" . -}}
{{- if .Values.autoscaling.enabled -}}
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: {{ include "platform.fullname" . }}
  labels:
    {{- include "platform.labels" . | nindent 4 }}
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: {{ include "platform.fullname" . }}
  minReplicas: {{ .Values.autoscaling.minReplicas }}
  maxReplicas: {{ .Values.autoscaling.maxReplicas }}
  metrics:
    {{- if .Values.autoscaling.targetCPUUtilizationPercentage }}
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          {{- /*
            Utilisation is a percentage of the *request*, not the limit. A
            target of 70 with a request of 200m scales at 140m of actual use.
            Reading it as a percentage of the limit is the single most common
            way an HPA ends up scaling at the wrong point.
          */}}
          averageUtilization: {{ .Values.autoscaling.targetCPUUtilizationPercentage }}
    {{- end }}
    {{- if .Values.autoscaling.targetMemoryUtilizationPercentage }}
    - type: Resource
      resource:
        name: memory
        target:
          type: Utilization
          averageUtilization: {{ .Values.autoscaling.targetMemoryUtilizationPercentage }}
    {{- end }}
    {{- with .Values.autoscaling.customMetrics }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
  behavior:
    scaleUp:
      {{- /*
        Short stabilisation on the way up: under a genuine traffic spike,
        waiting is the expensive option.
      */}}
      stabilizationWindowSeconds: {{ .Values.autoscaling.behavior.scaleUp.stabilizationWindowSeconds }}
      selectPolicy: Max
      policies:
        - type: Percent
          value: {{ .Values.autoscaling.behavior.scaleUp.percent }}
          periodSeconds: 60
        - type: Pods
          value: {{ .Values.autoscaling.behavior.scaleUp.pods }}
          periodSeconds: 60
    scaleDown:
      {{- /*
        Long stabilisation on the way down. The controller takes the highest
        recommendation over this window, so a brief dip in load cannot remove
        capacity that a spike thirty seconds later needs back. Scaling down too
        eagerly is what turns a bursty workload into a permanently flapping one.
      */}}
      stabilizationWindowSeconds: {{ .Values.autoscaling.behavior.scaleDown.stabilizationWindowSeconds }}
      selectPolicy: Min
      policies:
        - type: Percent
          value: {{ .Values.autoscaling.behavior.scaleDown.percent }}
          periodSeconds: 60
{{- end -}}
{{- end -}}

{{/*
PodDisruptionBudget.

This is what makes a node drain — a cluster upgrade, a Karpenter consolidation,
a spot interruption — cost zero availability. Without it, `kubectl drain` will
happily evict every replica of a service at once.
*/}}
{{- define "platform.pdb" -}}
{{- include "platform.init" . -}}
{{- if .Values.podDisruptionBudget.enabled -}}
{{- $minReplicas := .Values.replicaCount -}}
{{- if .Values.autoscaling.enabled -}}
{{- $minReplicas = .Values.autoscaling.minReplicas -}}
{{- end -}}
{{- /*
  A PDB that can never be satisfied blocks every voluntary eviction forever,
  which stalls cluster upgrades and leaves nodes stuck draining. minAvailable
  must therefore be strictly below the smallest replica count the service can
  reach, so this fails the render rather than the cluster.
*/}}
{{- if and (kindIs "int" .Values.podDisruptionBudget.minAvailable) (ge (int .Values.podDisruptionBudget.minAvailable) (int $minReplicas)) -}}
{{- fail (printf "%s: podDisruptionBudget.minAvailable (%v) must be less than the minimum replica count (%v), or no pod can ever be evicted and node drains will hang" .Chart.Name .Values.podDisruptionBudget.minAvailable $minReplicas) -}}
{{- end -}}
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: {{ include "platform.fullname" . }}
  labels:
    {{- include "platform.labels" . | nindent 4 }}
spec:
  minAvailable: {{ .Values.podDisruptionBudget.minAvailable }}
  selector:
    matchLabels:
      {{- include "platform.selectorLabels" . | nindent 6 }}
{{- end -}}
{{- end -}}
