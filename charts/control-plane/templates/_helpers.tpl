{{- define "controlplane.name" -}}kbridge-control-plane{{- end -}}

{{- define "controlplane.fullname" -}}
{{- printf "%s-control-plane" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "controlplane.labels" -}}
app.kubernetes.io/name: {{ include "controlplane.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
{{- end -}}

{{- define "controlplane.selectorLabels" -}}
app.kubernetes.io/name: {{ include "controlplane.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}
