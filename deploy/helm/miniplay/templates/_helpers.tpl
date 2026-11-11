{{/* The Secret name, or an error that stops rendering. There is no default on purpose. */}}
{{- define "miniplay.secret" -}}
{{- required "Set existingSecret to the name of a Secret you created; this chart holds no secrets (see values.yaml)" .Values.existingSecret -}}
{{- end -}}

{{- define "miniplay.labels" -}}
app.kubernetes.io/name: {{ .name }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/part-of: miniplay
app.kubernetes.io/managed-by: {{ .root.Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .root.Chart.Name .root.Chart.Version }}
{{- end -}}

{{- define "miniplay.selector" -}}
app.kubernetes.io/name: {{ .name }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
{{- end -}}

{{- define "miniplay.fullname" -}}
{{- printf "%s-%s" .root.Release.Name .name -}}
{{- end -}}

{{- define "miniplay.image" -}}
{{- printf "%s/%s:%s" .root.Values.image.registry .name .root.Values.image.tag -}}
{{- end -}}

{{/* Run as a non-root user with no extra privileges. The image's USER is 10001. */}}
{{- define "miniplay.podSecurity" -}}
runAsNonRoot: true
runAsUser: 10001
seccompProfile:
  type: RuntimeDefault
{{- end -}}

{{- define "miniplay.containerSecurity" -}}
allowPrivilegeEscalation: false
readOnlyRootFilesystem: true
capabilities:
  drop: ["ALL"]
{{- end -}}

{{/* One environment variable read from the chart's required Secret. */}}
{{- define "miniplay.secretEnv" -}}
- name: {{ .key }}
  valueFrom:
    secretKeyRef:
      name: {{ include "miniplay.secret" .root }}
      key: {{ .key }}
{{- end -}}

{{/*
A Deployment and, when `port` is set, a Service. Takes:
  root, name, config (replicas, port, resources), secretKeys (list), env (list of
  name/value maps), extraEnv (rendered YAML), volumes/volumeMounts (rendered YAML),
  scheme, and autoscaled (leave replicas to the autoscaler).
*/}}
{{- define "miniplay.workload" -}}
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ include "miniplay.fullname" . }}
  labels:
    {{- include "miniplay.labels" . | nindent 4 }}
spec:
  {{- if not .autoscaled }}
  replicas: {{ .config.replicas }}
  {{- end }}
  selector:
    matchLabels:
      {{- include "miniplay.selector" . | nindent 6 }}
  template:
    metadata:
      labels:
        {{- include "miniplay.labels" . | nindent 8 }}
    spec:
      automountServiceAccountToken: false
      securityContext:
        {{- include "miniplay.podSecurity" . | nindent 8 }}
      containers:
        - name: {{ .name }}
          image: {{ include "miniplay.image" . }}
          imagePullPolicy: {{ .root.Values.image.pullPolicy }}
          securityContext:
            {{- include "miniplay.containerSecurity" . | nindent 12 }}
          env:
            {{- if .config.port }}
            - name: MINIPLAY_HTTP_ADDR
              value: {{ printf "0.0.0.0:%v" .config.port | quote }}
            {{- end }}
            {{- range .env }}
            - name: {{ .name }}
              value: {{ .value | quote }}
            {{- end }}
            {{- $root := .root }}
            {{- range .secretKeys }}
            {{- include "miniplay.secretEnv" (dict "root" $root "key" .) | nindent 12 }}
            {{- end }}
          {{- if .config.port }}
          ports:
            - name: http
              containerPort: {{ .config.port }}
          readinessProbe:
            httpGet:
              path: {{ .healthPath | default "/health" }}
              port: http
              {{- if .scheme }}
              scheme: {{ .scheme }}
              {{- end }}
            periodSeconds: 5
          livenessProbe:
            httpGet:
              path: {{ .healthPath | default "/health" }}
              port: http
              {{- if .scheme }}
              scheme: {{ .scheme }}
              {{- end }}
            initialDelaySeconds: 10
            periodSeconds: 15
          {{- end }}
          resources:
            {{- toYaml .config.resources | nindent 12 }}
          {{- with .volumeMounts }}
          volumeMounts:
            {{- . | nindent 12 }}
          {{- end }}
      {{- with .volumes }}
      volumes:
        {{- . | nindent 8 }}
      {{- end }}
{{- if .config.port }}
---
apiVersion: v1
kind: Service
metadata:
  name: {{ include "miniplay.fullname" . }}
  labels:
    {{- include "miniplay.labels" . | nindent 4 }}
spec:
  type: {{ .config.serviceType | default "ClusterIP" }}
  selector:
    {{- include "miniplay.selector" . | nindent 4 }}
  ports:
    - name: http
      port: {{ .config.port }}
      targetPort: http
{{- end }}
{{- end -}}

{{/* The URL other services use to reach a named service. */}}
{{- define "miniplay.url" -}}
{{- printf "http://%s-%s:%v" .root.Release.Name .name .port -}}
{{- end -}}
