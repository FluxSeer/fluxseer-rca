package ingestion

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"fluxagent/internal/domain"
	"fluxagent/internal/rcametrics"
)

func TestPipelineRecordsDeduplicationHits(t *testing.T) {
	before := testutil.ToFloat64(rcametrics.DeduplicationHitsTotal.WithLabelValues("ingestion_signal"))
	pipeline := NewPipeline()
	_, err := pipeline.Run(context.Background(), Request{
		ID: "incident-001",
		Signals: []domain.Signal{
			{
				Kind:      "log",
				Message:   "timeout calling upstream",
				Timestamp: time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC),
				Resource:  domain.ResourceRef{Namespace: "prod", Name: "checkout", Kind: "Deployment"},
			},
			{
				Kind:      "log",
				Message:   "timeout calling upstream",
				Timestamp: time.Date(2026, 7, 6, 12, 1, 0, 0, time.UTC),
				Resource:  domain.ResourceRef{Namespace: "prod", Name: "checkout", Kind: "Deployment"},
			},
		},
	})
	if err != nil {
		t.Fatalf("run pipeline: %v", err)
	}
	after := testutil.ToFloat64(rcametrics.DeduplicationHitsTotal.WithLabelValues("ingestion_signal"))
	if after != before+1 {
		t.Fatalf("expected one deduplication hit, before=%f after=%f", before, after)
	}
}
