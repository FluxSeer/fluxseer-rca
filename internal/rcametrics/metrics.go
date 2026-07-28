package rcametrics

import (
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

var AllowedLabels = map[string][]string{
	"fluxagent_investigation_total":               {"namespace", "provider_type", "result", "root_cause_type"},
	"fluxagent_provider_requests_total":           {"provider_type", "result"},
	"fluxagent_provider_failures_total":           {"provider_type", "reason"},
	"fluxagent_datasource_query_duration_seconds": {"datasource_type", "result"},
	"fluxagent_evidence_truncated_total":          {"kind", "reason"},
	"fluxagent_claim_verification_total":          {"verification_status"},
	"fluxagent_deduplication_hits_total":          {"source"},
	"fluxagent_loop_prevention_total":             {"reason"},
	"fluxagent_status_update_conflicts_total":     {"resource"},
	"fluxagent_queue_depth":                       {"queue"},
}

var forbiddenLabels = map[string]struct{}{
	"execution_id":        {},
	"idempotency_key":     {},
	"request_name":        {},
	"request_uid":         {},
	"finding_fingerprint": {},
	"digest":              {},
	"evidence_digest":     {},
	"query":               {},
	"pod":                 {},
}

var (
	InvestigationTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fluxagent_investigation_total",
			Help: "Total FluxAgent investigations by namespace, provider type, result, and root cause type.",
		},
		AllowedLabels["fluxagent_investigation_total"],
	)
	ProviderRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fluxagent_provider_requests_total",
			Help: "Total FluxAgent provider requests by provider type and result.",
		},
		AllowedLabels["fluxagent_provider_requests_total"],
	)
	ProviderFailuresTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fluxagent_provider_failures_total",
			Help: "Total FluxAgent provider failures by provider type and low-cardinality reason.",
		},
		AllowedLabels["fluxagent_provider_failures_total"],
	)
	DatasourceQueryDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "fluxagent_datasource_query_duration_seconds",
			Help:    "FluxAgent datasource query duration by datasource type and result.",
			Buckets: prometheus.DefBuckets,
		},
		AllowedLabels["fluxagent_datasource_query_duration_seconds"],
	)
	EvidenceTruncatedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fluxagent_evidence_truncated_total",
			Help: "Total FluxAgent truncated evidence references by evidence kind and low-cardinality reason.",
		},
		AllowedLabels["fluxagent_evidence_truncated_total"],
	)
	ClaimVerificationTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fluxagent_claim_verification_total",
			Help: "Total FluxAgent RCA claims by verification status.",
		},
		AllowedLabels["fluxagent_claim_verification_total"],
	)
	DeduplicationHitsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fluxagent_deduplication_hits_total",
			Help: "Total FluxAgent deduplication hits by low-cardinality source.",
		},
		AllowedLabels["fluxagent_deduplication_hits_total"],
	)
	LoopPreventionTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fluxagent_loop_prevention_total",
			Help: "Total FluxAgent loop prevention decisions by reason.",
		},
		AllowedLabels["fluxagent_loop_prevention_total"],
	)
	StatusUpdateConflictsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fluxagent_status_update_conflicts_total",
			Help: "Total FluxAgent status update conflicts by resource type.",
		},
		AllowedLabels["fluxagent_status_update_conflicts_total"],
	)
	QueueDepth = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "fluxagent_queue_depth",
			Help: "FluxAgent controller queue depth by queue name.",
		},
		AllowedLabels["fluxagent_queue_depth"],
	)
)

func init() {
	crmetrics.Registry.MustRegister(
		InvestigationTotal,
		ProviderRequestsTotal,
		ProviderFailuresTotal,
		DatasourceQueryDurationSeconds,
		EvidenceTruncatedTotal,
		ClaimVerificationTotal,
		DeduplicationHitsTotal,
		LoopPreventionTotal,
		StatusUpdateConflictsTotal,
		QueueDepth,
	)
}

func RecordInvestigation(namespace, providerType, result, rootCauseType string) {
	InvestigationTotal.WithLabelValues(
		normalizeLabel(namespace, "unknown"),
		normalizeProviderType(providerType),
		normalizeResult(result),
		normalizeLabel(rootCauseType, "unknown"),
	).Inc()
}

func RecordProviderRequest(providerType, result string) {
	ProviderRequestsTotal.WithLabelValues(normalizeProviderType(providerType), normalizeResult(result)).Inc()
}

func RecordProviderFailure(providerType, reason string) {
	ProviderFailuresTotal.WithLabelValues(normalizeProviderType(providerType), normalizeReason(reason)).Inc()
}

func ObserveDatasourceQuery(datasourceType, result string, duration time.Duration) {
	DatasourceQueryDurationSeconds.WithLabelValues(normalizeLabel(datasourceType, "unknown"), normalizeResult(result)).Observe(duration.Seconds())
}

func RecordEvidenceTruncated(kind string, reason string) {
	EvidenceTruncatedTotal.WithLabelValues(normalizeLabel(kind, "unknown"), normalizeReason(reason)).Inc()
}

func RecordClaimVerification(status string) {
	ClaimVerificationTotal.WithLabelValues(normalizeLabel(status, "unknown")).Inc()
}

func RecordDeduplicationHit(source string) {
	DeduplicationHitsTotal.WithLabelValues(normalizeLabel(source, "unknown")).Inc()
}

func RecordLoopPrevention(reason string) {
	LoopPreventionTotal.WithLabelValues(normalizeReason(reason)).Inc()
}

func RecordStatusUpdateConflict(resource string) {
	StatusUpdateConflictsTotal.WithLabelValues(normalizeLabel(resource, "unknown")).Inc()
}

func ForbiddenLabelNames() map[string]struct{} {
	out := make(map[string]struct{}, len(forbiddenLabels))
	for key := range forbiddenLabels {
		out[key] = struct{}{}
	}
	return out
}

func normalizeProviderType(value string) string {
	return normalizeLabel(value, "heuristic")
}

func normalizeResult(value string) string {
	return normalizeLabel(value, "unknown")
}

func normalizeReason(value string) string {
	return normalizeLabel(value, "unknown")
}

func normalizeLabel(value string, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return fallback
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '_' || r == '-' || r == '.':
			return r
		default:
			return '_'
		}
	}, value)
}
