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
