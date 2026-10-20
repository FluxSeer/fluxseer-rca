package diagnostic

import (
	"testing"

	"github.com/FluxSeer/fluxseer-rca/internal/domain"
)

func TestPlannerDeduplicatesAndOrdersDiagnostics(t *testing.T) {
	planner := NewPlanner(ImagePullDiagnostic{}, ImagePullDiagnostic{}, nil)
	if got := len(planner.diagnostics); got != 1 {
		t.Fatalf("expected one deduplicated diagnostic, got %d", got)
	}
	if planner.diagnostics[0].ID() != "image-pull" {
		t.Fatalf("unexpected diagnostic registry: %#v", planner.diagnostics)
	}
}

func TestPlannerEvaluatesMatchingImagePullDiagnostic(t *testing.T) {
	planner := DefaultPlanner()
	evidence := EvidenceSet{
		Observations: []domain.Observation{{
			ID:      "event-001",
			Type:    domain.ObservationTypeEvent,
			Summary: "failed to pull image",
			Value:   domain.ObservationValue{Event: &domain.EventObservation{Reason: "ErrImagePull"}},
		}},
		Scope: completeScope(),
	}
	results := planner.Evaluate(evidence)
	if len(results) != 1 || results[0].DiagnosticID != "image-pull" {
		t.Fatalf("expected one image-pull evaluation, got %#v", results)
	}
}
