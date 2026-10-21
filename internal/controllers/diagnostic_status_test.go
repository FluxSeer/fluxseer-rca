package controllers

import (
	"reflect"
	"strings"
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

func TestShadowReconciliationDoesNotChangeCanonicalRCAClaims(t *testing.T) {
	rca := investigation.RCAResult{Reasoning: &domain.ReasoningOutput{RiskSummary: "Pod remains pending"}}
	evidence := investigation.EvidenceCollectionResult{EvidenceRefs: []v1alpha1.EvidenceRef{{
		ID: "event-1", Kind: string(domain.QueryTypeEvent), Reason: "FailedScheduling", Summary: "scheduler reported no available node",
	}}}
	withoutShadow, withoutVerification := buildRCAClaims(rca, evidence, "")
	evidence.Reconciliation = &investigation.CandidateReconciliationResult{Status: investigation.ReconciliationCompleted}
	withShadow, withVerification := buildRCAClaims(rca, evidence, "")
	if !reflect.DeepEqual(withoutShadow, withShadow) || !reflect.DeepEqual(withoutVerification, withVerification) {
		t.Fatalf("shadow reconciliation changed canonical RCA output: without=%#v/%#v with=%#v/%#v", withoutShadow, withoutVerification, withShadow, withVerification)
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

func TestSchedulingMechanismClaimIsConfirmedWhileRootCauseRemainsCandidate(t *testing.T) {
	evidence := investigation.EvidenceCollectionResult{
		EvidenceRefs: []v1alpha1.EvidenceRef{{
			ID:      "event-1",
			Kind:    string(domain.QueryTypeEvent),
			Reason:  "FailedScheduling",
			Summary: "0/1 nodes are unavailable: scheduler details redacted",
		}},
		Diagnostics: []diagnostic.Result{{
			DiagnosticID: "scheduling",
			Claims: []diagnostic.CandidateClaim{
				{Type: diagnostic.ClaimFailureMechanism, Statement: "The scheduler reported that the Pod could not be scheduled.", EvidenceRefs: []string{"event-1"}},
				{Type: diagnostic.ClaimRootCause, Statement: "The scheduler reported a Node selector or affinity mismatch for the Pod.", EvidenceRefs: []string{"event-1"}},
			},
		}},
	}
	rca := investigation.RCAResult{Reasoning: &domain.ReasoningOutput{RiskSummary: "Pod remains pending"}}
	claims, _ := buildRCAClaims(rca, evidence, "")
	var mechanism *v1alpha1.RCAClaim
	for index := range claims {
		if isSchedulingMechanismClaim(claims[index]) {
			mechanism = &claims[index]
			break
		}
	}
	if mechanism == nil || mechanism.Verification != "Supported" {
		t.Fatalf("expected confirmed scheduling mechanism claim, got %#v", claims)
	}
	evaluation := evaluateCanonicalVerdict(rca, evidence, claims, 0.95, "")
	if evaluation.Outcome != v1alpha1.InvestigationOutcomeInconclusive {
		t.Fatalf("expected unverified root cause to remain inconclusive, got %#v", evaluation)
	}
	hypotheses := diagnosticAlternativeHypothesesWithoutConfirmedMechanism(evidence.Diagnostics, claims)
	if len(hypotheses) != 1 || hypotheses[0].Disposition != "DiagnosticCandidateUnverified" || !strings.Contains(hypotheses[0].Statement, "Node selector") {
		t.Fatalf("expected only the root-cause candidate to remain unverified, got %#v", hypotheses)
	}
}

func TestSchedulingMechanismClaimAcceptsVerifiedConditionEvidence(t *testing.T) {
	evidence := investigation.EvidenceCollectionResult{
		EvidenceRefs: []v1alpha1.EvidenceRef{{
			ID:      "pod-1",
			Kind:    "scheduling",
			Summary: "Pod prod/pending UID pod-1 scheduling state: phase=Pending nodeName= podScheduled=False coverage=Complete",
		}},
		Diagnostics: []diagnostic.Result{{
			DiagnosticID: "scheduling",
			Claims: []diagnostic.CandidateClaim{{
				Type:         diagnostic.ClaimFailureMechanism,
				Statement:    "The scheduler reported that the Pod could not be scheduled.",
				EvidenceRefs: []string{"pod-1"},
			}},
		}},
	}
	statement, ok := schedulingMechanismRCAClaim(evidence)
	if !ok || statement != schedulingMechanismClaimStatement {
		t.Fatalf("expected PodScheduled=False condition to support mechanism claim, got ok=%v statement=%q", ok, statement)
	}
	rca := investigation.RCAResult{Reasoning: &domain.ReasoningOutput{RiskSummary: "Pod remains pending"}}
	claims, _ := buildRCAClaims(rca, evidence, "")
	var mechanism *v1alpha1.RCAClaim
	for index := range claims {
		if isSchedulingMechanismClaim(claims[index]) {
			mechanism = &claims[index]
			break
		}
	}
	if mechanism == nil || mechanism.Verification != "Supported" {
		t.Fatalf("expected condition-backed mechanism claim to verify, got %#v", claims)
	}
}

func TestSchedulingProfileAcceptsConditionEvidenceWithoutEventRef(t *testing.T) {
	evidence := investigation.EvidenceCollectionResult{EvidenceRefs: []v1alpha1.EvidenceRef{{
		ID:      "pod-1",
		Kind:    "scheduling",
		Summary: "Pod prod/pending UID pod-1 scheduling state: phase=Pending nodeName= podScheduled=False coverage=Complete",
	}}}
	gate := evaluateEvidenceRequirements(v1alpha1.InvestigationRequestSpec{
		EvidenceRequirements: v1alpha1.EvidenceRequirements{Profile: "FailedScheduling"},
	}, evidence)
	if gate == nil || !gate.Complete || len(gate.Missing) != 0 {
		t.Fatalf("expected condition-backed scheduling profile to be complete, got %#v", gate)
	}
}

func TestGenericEventReferenceDoesNotConfirmSchedulingMechanism(t *testing.T) {
	statement, ok := schedulingMechanismRCAClaim(investigation.EvidenceCollectionResult{
		EvidenceRefs: []v1alpha1.EvidenceRef{{
			Kind:    string(domain.QueryTypeEvent),
			Reason:  "FailedScheduling",
			Summary: "0/2 nodes are unavailable",
		}},
	})
	if ok || statement != "" {
		t.Fatalf("expected uncorrelated generic event to remain unconfirmed, got ok=%v statement=%q", ok, statement)
	}
}
