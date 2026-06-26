{{- define "kubepilot.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "kubepilot.fullname" -}}
{{- printf "%s-%s" .Release.Name (include "kubepilot.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "kubepilot.labels" -}}
app.kubernetes.io/name: {{ include "kubepilot.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}

{{- define "kubepilot.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kubepilot.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "kubepilot.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "kubepilot.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}
