{{/*
The shared Deployment.

Everything security- and lifecycle-related is a default here rather than a
per-service opt-in, because an opt-in hardening control is one a service will
eventually ship without.
*/}}
{{- define "platform.deployment" -}}
{{- include "platform.init" . -}}
{{- include "platform.validateImage" . -}}
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ include "platform.fullname" . }}
  labels:
    {{- include "platform.labels" . | nindent 4 }}
  annotations:
    {{- with .Values.deploymentAnnotations }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
spec:
  {{- /*
    replicas is omitted when the HPA owns it. Setting both makes Helm and the
    HPA fight: every `helm upgrade` resets the count to the chart value and the
    HPA scales it back, so a deploy during peak load briefly drops capacity.
  */}}
  {{- if not .Values.autoscaling.enabled }}
  replicas: {{ .Values.replicaCount }}
  {{- end }}
  revisionHistoryLimit: {{ .Values.revisionHistoryLimit | default 5 }}
  strategy:
    type: RollingUpdate
    rollingUpdate:
      {{- /*
        maxUnavailable 0 means capacity never dips below the desired count
        during a rollout; new pods must be Ready before old ones are removed.
      */}}
      maxUnavailable: {{ .Values.rollingUpdate.maxUnavailable | default 0 }}
      maxSurge: {{ .Values.rollingUpdate.maxSurge | default 1 }}
  selector:
    matchLabels:
      {{- include "platform.selectorLabels" . | nindent 6 }}
  template:
    metadata:
      labels:
        {{- include "platform.labels" . | nindent 8 }}
        {{- with .Values.podLabels }}
        {{- toYaml . | nindent 8 }}
        {{- end }}
      annotations:
        {{- /*
          Hashing the ConfigMap and Secret into the pod template is what makes a
          config change actually roll the pods. Without it `helm upgrade`
          reports success, the ConfigMap updates, and every running pod keeps
          serving the old configuration until something unrelated restarts it.
        */}}
        checksum/config: {{ include (print $.Template.BasePath "/configmap.yaml") . | sha256sum }}
        {{- with .Values.podAnnotations }}
        {{- toYaml . | nindent 8 }}
        {{- end }}
    spec:
      serviceAccountName: {{ include "platform.serviceAccountName" . }}
      {{- /*
        Off unless a workload genuinely calls the Kubernetes API. Mounting the
        token by default hands every compromised container a cluster credential.
      */}}
      automountServiceAccountToken: {{ .Values.serviceAccount.automount | default false }}
      {{- with .Values.imagePullSecrets }}
      imagePullSecrets:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- /*
        Must exceed the application's own drain delay plus its shutdown grace,
        or the kubelet SIGKILLs the process mid-drain and in-flight requests are
        dropped — the exact failure a graceful shutdown exists to prevent.
      */}}
      terminationGracePeriodSeconds: {{ .Values.terminationGracePeriodSeconds | default 45 }}
      securityContext:
        {{- toYaml .Values.podSecurityContext | nindent 8 }}
      {{- with .Values.priorityClassName }}
      priorityClassName: {{ . }}
      {{- end }}
      {{- with .Values.nodeSelector }}
      nodeSelector:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- with .Values.tolerations }}
      tolerations:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- with .Values.affinity }}
      affinity:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- /*
        Spread replicas across availability zones and then across nodes.
        Without this the scheduler is free to place every replica of a service
        on one node or in one AZ, and a single node or zone failure takes the
        whole service down however many replicas were requested.
      */}}
      {{- if .Values.topologySpreadConstraints }}
      topologySpreadConstraints:
        {{- range .Values.topologySpreadConstraints }}
        - maxSkew: {{ .maxSkew }}
          topologyKey: {{ .topologyKey }}
          whenUnsatisfiable: {{ .whenUnsatisfiable }}
          labelSelector:
            matchLabels:
              {{- include "platform.selectorLabels" $ | nindent 14 }}
        {{- end }}
      {{- end }}
      containers:
        - name: {{ .Chart.Name }}
          image: {{ include "platform.image" . }}
          imagePullPolicy: {{ .Values.image.pullPolicy }}
          securityContext:
            {{- toYaml .Values.containerSecurityContext | nindent 12 }}
          ports:
            - name: http
              containerPort: {{ .Values.service.targetPort }}
              protocol: TCP
            {{- if .Values.adminPort }}
            - name: admin
              containerPort: {{ .Values.adminPort }}
              protocol: TCP
            {{- end }}
          env:
            - name: POD_NAME
              valueFrom:
                fieldRef:
                  fieldPath: metadata.name
            - name: POD_NAMESPACE
              valueFrom:
                fieldRef:
                  fieldPath: metadata.namespace
            - name: NODE_NAME
              valueFrom:
                fieldRef:
                  fieldPath: spec.nodeName
            {{- /*
              GOMAXPROCS from the CPU limit, not the node's core count. Go
              otherwise sizes its scheduler to the host — 64 threads on a large
              node for a container limited to one core — which produces constant
              CFS throttling and latency the CPU graphs do not explain.
            */}}
            {{- if .Values.goMaxProcsFromLimit }}
            - name: GOMAXPROCS
              valueFrom:
                resourceFieldRef:
                  containerName: {{ .Chart.Name }}
                  resource: limits.cpu
            {{- end }}
            {{- range $key, $value := .Values.env }}
            - name: {{ $key }}
              value: {{ $value | quote }}
            {{- end }}
            {{- range .Values.envFromSecret }}
            - name: {{ .name }}
              valueFrom:
                secretKeyRef:
                  name: {{ .secretName }}
                  key: {{ .secretKey }}
            {{- end }}
          envFrom:
            - configMapRef:
                name: {{ include "platform.fullname" . }}
            {{- with .Values.extraEnvFrom }}
            {{- toYaml . | nindent 12 }}
            {{- end }}
          {{- /*
            Three probes with three distinct jobs.

            startup gates the other two, so a slow boot is not mistaken for a
            hung process and restarted forever. liveness checks only that the
            process responds — never a dependency, or a database blip restarts
            every pod at once. readiness gates traffic and is the one that flips
            during a drain.
          */}}
          startupProbe:
            httpGet:
              path: {{ .Values.probes.startupPath }}
              port: {{ .Values.probes.port | default (include "platform.monitoringPort" .) }}
            periodSeconds: {{ .Values.probes.startup.periodSeconds }}
            failureThreshold: {{ .Values.probes.startup.failureThreshold }}
            timeoutSeconds: {{ .Values.probes.startup.timeoutSeconds }}
          livenessProbe:
            httpGet:
              path: {{ .Values.probes.livenessPath }}
              port: {{ .Values.probes.port | default (include "platform.monitoringPort" .) }}
            periodSeconds: {{ .Values.probes.liveness.periodSeconds }}
            failureThreshold: {{ .Values.probes.liveness.failureThreshold }}
            timeoutSeconds: {{ .Values.probes.liveness.timeoutSeconds }}
          readinessProbe:
            httpGet:
              path: {{ .Values.probes.readinessPath }}
              port: {{ .Values.probes.port | default (include "platform.monitoringPort" .) }}
            periodSeconds: {{ .Values.probes.readiness.periodSeconds }}
            failureThreshold: {{ .Values.probes.readiness.failureThreshold }}
            successThreshold: {{ .Values.probes.readiness.successThreshold }}
            timeoutSeconds: {{ .Values.probes.readiness.timeoutSeconds }}
          resources:
            {{- toYaml .Values.resources | nindent 12 }}
          volumeMounts:
            {{- /*
              A read-only root filesystem needs an explicit writable /tmp;
              without it any library that writes a temp file crashes at runtime.
            */}}
            - name: tmp
              mountPath: /tmp
            {{- with .Values.extraVolumeMounts }}
            {{- toYaml . | nindent 12 }}
            {{- end }}
      volumes:
        - name: tmp
          emptyDir:
            sizeLimit: {{ .Values.tmpSizeLimit | default "64Mi" }}
        {{- with .Values.extraVolumes }}
        {{- toYaml . | nindent 8 }}
        {{- end }}
{{- end -}}
