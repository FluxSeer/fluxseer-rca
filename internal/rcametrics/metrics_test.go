package rcametrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

func TestAllowedMetricLabelsRemainLowCardinality(t *testing.T) {
	for metricName, labels := range AllowedLabels {
		if len(labels) == 0 {
			t.Fatalf("metric %s must declare label contract", metricName)
		}
		for _, label := range labels {
			if _, forbidden := forbiddenLabels[label]; forbidden {
				t.Fatalf("metric %s uses forbidden high-cardinality label %q", metricName, label)
			}
		}
	}
}

func TestRCAQualityMetricLabelsAvoidResourceIdentity(t *testing.T) {
	for _, metricName := range []string{
		"fluxseer_rca_investigation_quality_total",
		"fluxseer_rca_evidence_sufficiency_total",
		"fluxseer_rca_required_evidence_checks_total",
		"fluxseer_rca_actionable_inconclusive_total",
	} {
		for _, label := range AllowedLabels[metricName] {
			switch label {
			case "namespace", "name", "uid", "target", "request", "query", "source_ref":
				t.Fatalf("metric %s uses resource-specific label %q", metricName, label)
			}
		}
	}
}

func TestRCAQualityMetricsAreRegistered(t *testing.T) {
	RecordInvestigationQuality("CrashLoopBackOffV2", "heuristic", "available", "completed", "confirmed")
	RecordEvidenceSufficiency("CrashLoopBackOffV2", true)
	RecordRequiredEvidenceCheck("CrashLoopBackOffV2", "containerTermination:LastTermination", "usable")
	RecordActionableInconclusive("CrashLoopBackOffV2", "termination_missing", true)
	RecordDiagnosticCollection("Scheduling", 1, "Success", 10)
	RecordDiagnosticEvidenceCoverage("Scheduling", "Success", "Complete")
	RecordInvestigationBudgetExhausted("Scheduling", "query_budget")
	RecordDiagnosticClaim("Scheduling", "Observation", "Candidate")
	metrics, err := crmetrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gather registered metrics: %v", err)
	}
	want := map[string]bool{
		"fluxseer_rca_investigation_quality_total":        false,
		"fluxseer_rca_evidence_sufficiency_total":         false,
		"fluxseer_rca_required_evidence_checks_total":     false,
		"fluxseer_rca_actionable_inconclusive_total":      false,
		"fluxseer_diagnostic_collection_requests_total":   false,
		"fluxseer_diagnostic_collection_duration_seconds": false,
		"fluxseer_diagnostic_evidence_coverage_total":     false,
		"fluxseer_investigation_budget_exhausted_total":   false,
		"fluxseer_diagnostic_claims_total":                false,
	}
	for _, metric := range metrics {
		if _, ok := want[metric.GetName()]; ok {
			want[metric.GetName()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("metric %s is not registered in the controller-runtime registry", name)
		}
	}
}

func TestDiagnosticMetricsNormalizeLabelsAndIncrement(t *testing.T) {
	beforeRequests := testutil.ToFloat64(DiagnosticCollectionRequestsTotal.WithLabelValues("scheduling", "partial"))
	RecordDiagnosticCollection("Scheduling", 2, "Partial", 25)
	if got := testutil.ToFloat64(DiagnosticCollectionRequestsTotal.WithLabelValues("scheduling", "partial")); got != beforeRequests+2 {
		t.Fatalf("expected diagnostic API request counter increment, before=%f after=%f", beforeRequests, got)
	}
	RecordDiagnosticEvidenceCoverage("Scheduling", "Partial", "InvestigationQueryBudgetExhausted")
	RecordInvestigationBudgetExhausted("Scheduling", "query_budget")
	RecordDiagnosticClaim("Scheduling", "FailureMechanism", "Candidate")
	if got := testutil.ToFloat64(DiagnosticEvidenceCoverageTotal.WithLabelValues("scheduling", "partial", "budget_exhausted")); got < 1 {
		t.Fatalf("expected budget coverage metric, got %f", got)
	}
	if got := testutil.ToFloat64(InvestigationBudgetExhaustedTotal.WithLabelValues("scheduling", "budget_exhausted")); got < 1 {
		t.Fatalf("expected budget exhaustion metric, got %f", got)
	}
	if got := testutil.ToFloat64(DiagnosticClaimsTotal.WithLabelValues("scheduling", "failure_mechanism", "candidate")); got < 1 {
		t.Fatalf("expected diagnostic claim metric, got %f", got)
	}
}

func TestRecordRCAQualityMetricsNormalizeLabelsAndIncrement(t *testing.T) {
	outcome := []string{"crashloopbackoffv2", "openai", "partial", "completed", "confirmed"}
	beforeOutcome := testutil.ToFloat64(InvestigationQualityTotal.WithLabelValues(outcome...))
	RecordInvestigationQuality("CrashLoopBackOffV2", "OpenAI", "Partial", "Completed", "Confirmed")
	afterOutcome := testutil.ToFloat64(InvestigationQualityTotal.WithLabelValues(outcome...))
	if afterOutcome != beforeOutcome+1 {
		t.Fatalf("expected RCA outcome counter increment, before=%f after=%f", beforeOutcome, afterOutcome)
	}

	unknownOutcome := []string{"other", "other", "other", "other", "other"}
	beforeUnknown := testutil.ToFloat64(InvestigationQualityTotal.WithLabelValues(unknownOutcome...))
	RecordInvestigationQuality("user-controlled-profile", "provider-instance-name", "unrecognized-state", "running", "unrecognized-outcome")
	afterUnknown := testutil.ToFloat64(InvestigationQualityTotal.WithLabelValues(unknownOutcome...))
	if afterUnknown != beforeUnknown+1 {
		t.Fatalf("expected unknown labels to collapse to fixed values, before=%f after=%f", beforeUnknown, afterUnknown)
	}

	beforeSufficiency := testutil.ToFloat64(EvidenceSufficiencyTotal.WithLabelValues("crashloopbackoffv2", "true"))
	RecordEvidenceSufficiency("CrashLoopBackOffV2", true)
	if got := testutil.ToFloat64(EvidenceSufficiencyTotal.WithLabelValues("crashloopbackoffv2", "true")); got != beforeSufficiency+1 {
		t.Fatalf("expected evidence sufficiency counter increment, before=%f after=%f", beforeSufficiency, got)
	}

	beforeCheck := testutil.ToFloat64(RequiredEvidenceChecksTotal.WithLabelValues("crashloopbackoffv2", "containertermination_lasttermination", "stale"))
	RecordRequiredEvidenceCheck("CrashLoopBackOffV2", "containerTermination:LastTermination", "Stale")
	if got := testutil.ToFloat64(RequiredEvidenceChecksTotal.WithLabelValues("crashloopbackoffv2", "containertermination_lasttermination", "stale")); got != beforeCheck+1 {
		t.Fatalf("expected required evidence check counter increment, before=%f after=%f", beforeCheck, got)
	}

	beforeActionable := testutil.ToFloat64(ActionableInconclusiveTotal.WithLabelValues("crashloopbackoffv2", "termination_missing", "true"))
	RecordActionableInconclusive("CrashLoopBackOffV2", "termination_missing", true)
	if got := testutil.ToFloat64(ActionableInconclusiveTotal.WithLabelValues("crashloopbackoffv2", "termination_missing", "true")); got != beforeActionable+1 {
		t.Fatalf("expected actionable inconclusive counter increment, before=%f after=%f", beforeActionable, got)
	}
}

func TestNormalizeLabelBoundsUnexpectedCharacters(t *testing.T) {
	if got := normalizeLabel("Provider/OpenAI prod", "unknown"); got != "provider_openai_prod" {
		t.Fatalf("expected sanitized label, got %q", got)
	}
	if got := normalizeLabel("", "unknown"); got != "unknown" {
		t.Fatalf("expected fallback label, got %q", got)
	}
}

func TestRecordStatusUpdateConflict(t *testing.T) {
	before := testutil.ToFloat64(StatusUpdateConflictsTotal.WithLabelValues("investigationrequest"))
	RecordStatusUpdateConflict("InvestigationRequest")
	after := testutil.ToFloat64(StatusUpdateConflictsTotal.WithLabelValues("investigationrequest"))
	if after != before+1 {
		t.Fatalf("expected status conflict counter increment, before=%f after=%f", before, after)
	}
}

func TestRecordDeduplicationHit(t *testing.T) {
	before := testutil.ToFloat64(DeduplicationHitsTotal.WithLabelValues("provider_checkpoint"))
	RecordDeduplicationHit("Provider Checkpoint")
	after := testutil.ToFloat64(DeduplicationHitsTotal.WithLabelValues("provider_checkpoint"))
	if after != before+1 {
		t.Fatalf("expected deduplication counter increment, before=%f after=%f", before, after)
	}
}

func TestRecordEvidenceTruncated(t *testing.T) {
	before := testutil.ToFloat64(EvidenceTruncatedTotal.WithLabelValues("log", "result_limit"))
	RecordEvidenceTruncated("Log", "Result Limit")
	after := testutil.ToFloat64(EvidenceTruncatedTotal.WithLabelValues("log", "result_limit"))
	if after != before+1 {
		t.Fatalf("expected evidence truncation counter increment, before=%f after=%f", before, after)
	}
}

func TestRecordQueryPolicyDecision(t *testing.T) {
	before := testutil.ToFloat64(QueryPolicyDecisionsTotal.WithLabelValues("prometheus", "rejected", "template_not_allowed"))
	RecordQueryPolicyDecision("Prometheus", "Rejected", "Template Not Allowed")
	after := testutil.ToFloat64(QueryPolicyDecisionsTotal.WithLabelValues("prometheus", "rejected", "template_not_allowed"))
	if after != before+1 {
		t.Fatalf("expected query policy decision counter increment, before=%f after=%f", before, after)
	}
}

func TestRecordQueryResultLimitExceeded(t *testing.T) {
	before := testutil.ToFloat64(QueryResultLimitExceededTotal.WithLabelValues("prometheus", "samples"))
	RecordQueryResultLimitExceeded("Prometheus", "Samples")
	after := testutil.ToFloat64(QueryResultLimitExceededTotal.WithLabelValues("prometheus", "samples"))
	if after != before+1 {
		t.Fatalf("expected query result limit counter increment, before=%f after=%f", before, after)
	}
}

func TestDatasourceSchedulerGauges(t *testing.T) {
	queueBefore := testutil.ToFloat64(DatasourceQueryQueueDepth.WithLabelValues("investigation"))
	inFlightBefore := testutil.ToFloat64(DatasourceQueriesInFlight.WithLabelValues("investigation"))

	AddDatasourceQueryQueueDepth("Investigation", 2)
	AddDatasourceQueriesInFlight("Investigation", 1)

	queueDuring := testutil.ToFloat64(DatasourceQueryQueueDepth.WithLabelValues("investigation"))
	inFlightDuring := testutil.ToFloat64(DatasourceQueriesInFlight.WithLabelValues("investigation"))
	if queueDuring != queueBefore+2 {
		t.Fatalf("expected datasource query queue depth increment, before=%f during=%f", queueBefore, queueDuring)
	}
	if inFlightDuring != inFlightBefore+1 {
		t.Fatalf("expected datasource queries in-flight increment, before=%f during=%f", inFlightBefore, inFlightDuring)
	}

	AddDatasourceQueryQueueDepth("Investigation", -2)
	AddDatasourceQueriesInFlight("Investigation", -1)

	queueAfter := testutil.ToFloat64(DatasourceQueryQueueDepth.WithLabelValues("investigation"))
	inFlightAfter := testutil.ToFloat64(DatasourceQueriesInFlight.WithLabelValues("investigation"))
	if queueAfter != queueBefore {
		t.Fatalf("expected datasource query queue depth to return to baseline, before=%f after=%f", queueBefore, queueAfter)
	}
	if inFlightAfter != inFlightBefore {
		t.Fatalf("expected datasource queries in-flight to return to baseline, before=%f after=%f", inFlightBefore, inFlightAfter)
	}
}
