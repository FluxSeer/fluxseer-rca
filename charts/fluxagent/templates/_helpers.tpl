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

{{- define "fluxagent.effectiveRbacProfile" -}}
{{- if .Values.rbac.profile -}}
{{- .Values.rbac.profile -}}
{{- else if .Values.features.experimentalExecutor.enabled -}}
experimentalExecutor
{{- else if or .Values.controller.enableRemediation .Values.features.remediation.enabled -}}
remediation
{{- else -}}
readOnlyRCA
{{- end -}}
{{- end -}}

{{- define "fluxagent.rulePackTargetSelector" -}}
{{- $root := .root -}}
{{- $selector := default dict .selector -}}
{{- $namespaceSelector := default dict $selector.namespaceSelector -}}
{{- $workloadSelector := default dict $selector.workloadSelector -}}
targetSelector:
  namespaceSelector:
{{- if hasKey $namespaceSelector "matchNames" }}
{{- if empty $namespaceSelector.matchNames }}
    matchNames: []
{{- else }}
    matchNames:
{{ toYaml $namespaceSelector.matchNames | nindent 6 }}
{{- end }}
{{- else }}
    matchNames:
      - {{ $root.Release.Namespace | quote }}
{{- end }}
  workloadSelector:
{{- if hasKey $workloadSelector "matchLabels" }}
    matchLabels:
{{ toYaml $workloadSelector.matchLabels | nindent 6 }}
{{- end }}
{{- if hasKey $workloadSelector "kinds" }}
{{- if empty $workloadSelector.kinds }}
    kinds: []
{{- else }}
    kinds:
{{ toYaml $workloadSelector.kinds | nindent 6 }}
{{- end }}
{{- else }}
    kinds:
      - Deployment
{{- end }}
{{- end -}}
