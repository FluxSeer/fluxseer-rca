package rcametrics

import (
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	InvestigationQualityTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fluxseer_rca_investigation_quality_total",
			Help: "Persisted terminal RCA investigations by bounded profile, provider class, required-evidence availability, phase, and outcome.",
		},
		AllowedLabels["fluxseer_rca_investigation_quality_total"],
	)
	EvidenceSufficiencyTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fluxseer_rca_evidence_sufficiency_total",
			Help: "Persisted terminal investigations with a supported evidence profile, partitioned by whether all required checks completed with usable evidence.",
		},
		AllowedLabels["fluxseer_rca_evidence_sufficiency_total"],
	)
	RequiredEvidenceChecksTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fluxseer_rca_required_evidence_checks_total",
			Help: "Required evidence check executions by bounded profile, check, and result.",
		},
		AllowedLabels["fluxseer_rca_required_evidence_checks_total"],
	)
	ActionableInconclusiveTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fluxseer_rca_actionable_inconclusive_total",
			Help: "Completed inconclusive investigations by bounded evidence gap and whether a specific read-only diagnostic next step is supported.",
		},
		AllowedLabels["fluxseer_rca_actionable_inconclusive_total"],
	)
)

func init() {
	crmetrics.Registry.MustRegister(
		InvestigationQualityTotal,
		EvidenceSufficiencyTotal,
		RequiredEvidenceChecksTotal,
		ActionableInconclusiveTotal,
	)
}

func RecordInvestigationQuality(profile, providerClass, evidenceAvailability, phase, outcome string) {
	InvestigationQualityTotal.WithLabelValues(
		normalizeProfile(profile),
		normalizeProviderClass(providerClass),
		normalizeEvidenceAvailability(evidenceAvailability),
		normalizePhase(phase),
		normalizeOutcome(outcome),
	).Inc()
}

func RecordEvidenceSufficiency(profile string, sufficient bool) {
	EvidenceSufficiencyTotal.WithLabelValues(
		normalizeProfile(profile),
		boolLabel(sufficient),
	).Inc()
}

func RecordRequiredEvidenceCheck(profile, check, result string) {
	RequiredEvidenceChecksTotal.WithLabelValues(
		normalizeProfile(profile),
		normalizeEvidenceCheck(check),
		normalizeEvidenceCheckResult(result),
	).Inc()
}

func RecordActionableInconclusive(profile, gapReason string, actionable bool) {
	ActionableInconclusiveTotal.WithLabelValues(
		normalizeProfile(profile),
		normalizeEvidenceGap(gapReason),
		boolLabel(actionable),
	).Inc()
}

func normalizeProfile(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return "none"
	case "crashloopbackoff", "crashloopbackoffv2", "imagepullbackoff", "oomkilled", "latencyregression", "rolloutlatencyregression", "serviceportmismatch", "failedscheduling", "probefailure":
		return strings.ToLower(strings.TrimSpace(value))
	case "schedulingsuccess":
		return "failedscheduling"
	case "highhttperror", "highhttperrorrate":
		return "highhttperror"
	default:
		return "other"
	}
}

func normalizeProviderClass(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "heuristic":
		return "heuristic"
	case "openai":
		return "openai"
	case "claude", "anthropic":
		return "claude"
	case "gemini":
		return "gemini"
	default:
		return "other"
	}
}

func normalizeEvidenceAvailability(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "available", "partial", "unavailable", "not_applicable":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "other"
	}
}

func normalizePhase(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "completed", "failed":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "other"
	}
}

func normalizeOutcome(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "confirmed", "inconclusive", "unknown":
		return strings.ToLower(strings.TrimSpace(value))
	case "noissuefound", "no_issue_found":
		return "no_issue_found"
	default:
		return "other"
	}
}

func normalizeEvidenceCheck(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "event:crashloopbackoff":
		return "event_crashloopbackoff"
	case "log:applicationfailure":
		return "log_applicationfailure"
	case "containertermination:lasttermination":
		return "containertermination_lasttermination"
	case "event:imagepullbackoff":
		return "event_imagepullbackoff"
	case "event:oomkilled":
		return "event_oomkilled"
	case "metric:memory":
		return "metric_memory"
	case "metric:latency":
		return "metric_latency"
	case "deploymentcondition:rollout":
		return "deploymentcondition_rollout"
	case "serviceconfiguration:serviceportmismatch":
		return "serviceconfiguration_serviceportmismatch"
	case "event:failedscheduling":
		return "event_failedscheduling"
	case "probeconfiguration:probeconfigurationmismatch":
		return "probeconfiguration_probeconfigurationmismatch"
	case "metric:http5xx":
		return "metric_http5xx"
	case "log:causaldependency":
		return "log_causaldependency"
	default:
		return "other"
	}
}

func normalizeEvidenceCheckResult(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "usable", "empty", "missing", "stale", "truncated", "failed", "unavailable":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "other"
	}
}

func normalizeEvidenceGap(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "none", "multiple", "other", "application_evidence_missing", "application_log_required", "termination_missing", "termination_stale", "termination_truncated":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "other"
	}
}

func boolLabel(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
