package controllers

import (
	"testing"

	"github.com/FluxSeer/fluxseer-rca/internal/diagnostic"
)

func TestDiagnosticAlternativeHypothesesRemainExplicitlyUnverified(t *testing.T) {
	results := []diagnostic.Result{{
		Claims: []diagnostic.CandidateClaim{
			{Type: diagnostic.ClaimObservation, Statement: "Container waiting with ErrImagePull", EvidenceRefs: []string{"event-1"}},
			{Type: diagnostic.ClaimFailureMechanism, Statement: "The target container could not retrieve its configured image.", EvidenceRefs: []string{"event-1"}},
			{Type: diagnostic.ClaimRootCause, Statement: "The registry rejected access to the requested image.", EvidenceRefs: []string{"event-2"}},
		},
	}}
	hypotheses := diagnosticAlternativeHypotheses(results)
	if len(hypotheses) != 2 {
		t.Fatalf("expected mechanism and root-cause candidates, got %#v", hypotheses)
	}
	for _, hypothesis := range hypotheses {
		if hypothesis.Disposition != "DiagnosticCandidateUnverified" {
			t.Fatalf("expected explicit unverified disposition, got %#v", hypothesis)
		}
	}
	if hypotheses[0].EvidenceRefs[0] != "event-1" || hypotheses[1].EvidenceRefs[0] != "event-2" {
		t.Fatalf("expected evidence links to remain attached, got %#v", hypotheses)
	}
}
