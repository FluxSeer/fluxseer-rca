{{- define "fluxagent.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "fluxagent.fullname" -}}
{{- default "fluxagent" .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "fluxagent.labels" -}}
app.kubernetes.io/name: {{ include "fluxagent.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
{{- end -}}

{{- define "fluxagent.selectorLabels" -}}
app.kubernetes.io/name: fluxagent
app.kubernetes.io/component: controller-manager
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "fluxagent.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default "fluxagent-controller-manager" .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "fluxagent.image" -}}
{{- $tag := default .Chart.AppVersion .Values.image.tag -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end -}}

{{- define "fluxagent.investigatorServiceAccountName" -}}
{{- default "fluxagent-investigator" .Values.agentAnalysis.investigatorServiceAccount.name -}}
{{- end -}}
