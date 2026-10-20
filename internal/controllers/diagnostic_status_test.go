package controllers

import (
	"testing"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/diagnostic"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
	"github.com/FluxSeer/fluxseer-rca/internal/investigation"
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

func TestImagePullProfileClaimDistinguishesFailureFromBackOffState(t *testing.T) {
	tests := []struct {
		name    string
		reason  string
		summary string
		want    string
	}{
		{
			name:    "ErrImagePull event only confirms image pull failure",
			reason:  "ErrImagePull",
			summary: "failed to pull image",
			want:    "ImagePullFailure is confirmed by Kubernetes image-pull failure event evidence.",
		},
		{
			name:    "explicit ImagePullBackOff evidence confirms backoff state",
			reason:  "ImagePullBackOff",
			summary: "container is backing off image pulls",
			want:    "ImagePullBackOff is confirmed by explicit Kubernetes ImagePullBackOff event evidence.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			statement, ok := imagePullProfileClaim(investigation.EvidenceCollectionResult{
				EvidenceRefs: []v1alpha1.EvidenceRef{{
					ID: "event-1", Kind: string(domain.QueryTypeEvent), Reason: tt.reason, Summary: tt.summary,
				}},
			})
			if !ok || statement != tt.want {
				t.Fatalf("unexpected profile claim ok=%v statement=%q", ok, statement)
			}
		})
	}
}
