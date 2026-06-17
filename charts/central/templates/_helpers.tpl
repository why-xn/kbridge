{{- define "central.name" -}}kbridge-central{{- end -}}

{{- define "central.fullname" -}}
{{- printf "%s-central" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "central.labels" -}}
app.kubernetes.io/name: {{ include "central.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
{{- end -}}

{{- define "central.selectorLabels" -}}
app.kubernetes.io/name: {{ include "central.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}
