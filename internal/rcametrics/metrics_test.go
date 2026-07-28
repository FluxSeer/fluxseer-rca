package rcametrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
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
