package rcametrics

import (
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

var AllowedLabels = map[string][]string{
	"fluxseer_rca_investigation_total":               {"namespace", "provider_type", "result", "root_cause_type"},
	"fluxseer_rca_provider_requests_total":           {"provider_type", "result"},
	"fluxseer_rca_provider_failures_total":           {"provider_type", "reason"},
	"fluxseer_rca_datasource_query_duration_seconds": {"datasource_type", "result"},
	"fluxseer_rca_evidence_truncated_total":          {"kind", "reason"},
	"fluxseer_rca_claim_verification_total":          {"verification_status"},
	"fluxseer_rca_query_policy_decisions_total":      {"backend", "decision", "reason"},
	"fluxseer_rca_query_result_limit_exceeded_total": {"backend_type", "dimension"},
	"fluxseer_rca_datasource_query_queue_depth":      {"scheduler"},
	"fluxseer_rca_datasource_queries_in_flight":      {"scheduler"},
	"fluxseer_rca_deduplication_hits_total":          {"source"},
	"fluxseer_rca_loop_prevention_total":             {"reason"},
	"fluxseer_rca_status_update_conflicts_total":     {"resource"},
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
			Name: "fluxseer_rca_investigation_total",
			Help: "Total FluxSeer RCA investigations by namespace, provider type, result, and root cause type.",
		},
		AllowedLabels["fluxseer_rca_investigation_total"],
	)
	ProviderRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fluxseer_rca_provider_requests_total",
			Help: "Total FluxSeer RCA provider requests by provider type and result.",
		},
		AllowedLabels["fluxseer_rca_provider_requests_total"],
	)
	ProviderFailuresTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fluxseer_rca_provider_failures_total",
			Help: "Total FluxSeer RCA provider failures by provider type and low-cardinality reason.",
		},
		AllowedLabels["fluxseer_rca_provider_failures_total"],
	)
	DatasourceQueryDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "fluxseer_rca_datasource_query_duration_seconds",
			Help:    "FluxSeer RCA datasource query duration by datasource type and result.",
			Buckets: prometheus.DefBuckets,
		},
		AllowedLabels["fluxseer_rca_datasource_query_duration_seconds"],
	)
	EvidenceTruncatedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fluxseer_rca_evidence_truncated_total",
			Help: "Total FluxSeer RCA truncated evidence references by evidence kind and low-cardinality reason.",
		},
		AllowedLabels["fluxseer_rca_evidence_truncated_total"],
	)
	ClaimVerificationTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fluxseer_rca_claim_verification_total",
			Help: "Total FluxSeer RCA claims by verification status.",
		},
		AllowedLabels["fluxseer_rca_claim_verification_total"],
	)
	QueryPolicyDecisionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fluxseer_rca_query_policy_decisions_total",
			Help: "Total FluxSeer RCA query policy decisions by backend, decision, and low-cardinality reason.",
		},
		AllowedLabels["fluxseer_rca_query_policy_decisions_total"],
	)
	QueryResultLimitExceededTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fluxseer_rca_query_result_limit_exceeded_total",
			Help: "Total FluxSeer RCA datasource query result native limit exceedances by backend type and dimension.",
		},
		AllowedLabels["fluxseer_rca_query_result_limit_exceeded_total"],
	)
	DatasourceQueryQueueDepth = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "fluxseer_rca_datasource_query_queue_depth",
			Help: "FluxSeer RCA datasource queries waiting for a scheduler slot.",
		},
		AllowedLabels["fluxseer_rca_datasource_query_queue_depth"],
	)
	DatasourceQueriesInFlight = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "fluxseer_rca_datasource_queries_in_flight",
			Help: "FluxSeer RCA datasource queries currently executing in the datasource scheduler.",
		},
		AllowedLabels["fluxseer_rca_datasource_queries_in_flight"],
	)
	DeduplicationHitsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fluxseer_rca_deduplication_hits_total",
			Help: "Total FluxSeer RCA deduplication hits by low-cardinality source.",
		},
		AllowedLabels["fluxseer_rca_deduplication_hits_total"],
	)
	LoopPreventionTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fluxseer_rca_loop_prevention_total",
			Help: "Total FluxSeer RCA loop prevention decisions by reason.",
		},
		AllowedLabels["fluxseer_rca_loop_prevention_total"],
	)
	StatusUpdateConflictsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fluxseer_rca_status_update_conflicts_total",
			Help: "Total FluxSeer RCA status update conflicts by resource type.",
		},
		AllowedLabels["fluxseer_rca_status_update_conflicts_total"],
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
		QueryPolicyDecisionsTotal,
		QueryResultLimitExceededTotal,
		DatasourceQueryQueueDepth,
		DatasourceQueriesInFlight,
		DeduplicationHitsTotal,
		LoopPreventionTotal,
		StatusUpdateConflictsTotal,
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

func RecordQueryPolicyDecision(backend string, decision string, reason string) {
	QueryPolicyDecisionsTotal.WithLabelValues(normalizeLabel(backend, "unknown"), normalizeLabel(decision, "unknown"), normalizeReason(reason)).Inc()
}

func RecordQueryResultLimitExceeded(backendType string, dimension string) {
	QueryResultLimitExceededTotal.WithLabelValues(normalizeLabel(backendType, "unknown"), normalizeLabel(dimension, "unknown")).Inc()
}

func AddDatasourceQueryQueueDepth(scheduler string, delta float64) {
	DatasourceQueryQueueDepth.WithLabelValues(normalizeLabel(scheduler, "unknown")).Add(delta)
}

func AddDatasourceQueriesInFlight(scheduler string, delta float64) {
	DatasourceQueriesInFlight.WithLabelValues(normalizeLabel(scheduler, "unknown")).Add(delta)
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
