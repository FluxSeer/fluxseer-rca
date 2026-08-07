package solution

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
)

func TestAssessBuildsEvidenceLinkedCandidate(t *testing.T) {
	got := Assess(solutionRequest("Supported"))
	if len(got.Candidates) != 1 {
		t.Fatalf("expected one candidate, got %#v", got.Candidates)
	}
	candidate := got.Candidates[0]
	if candidate.Verification != VerificationSupported {
		t.Fatalf("expected supported candidate, got %#v", candidate)
	}
	if len(candidate.EvidenceRefs) != 1 || candidate.EvidenceRefs[0] != "evidence-001" {
		t.Fatalf("expected evidence link, got %#v", candidate.EvidenceRefs)
	}
	if !candidate.ApprovalRequired || candidate.ExecutionAuthorized {
		t.Fatalf("expected assessment to require approval without authorizing execution, got %#v", candidate)
	}
}

func TestAssessMarksCandidateUnverifiedWithoutSupportedEvidence(t *testing.T) {
	got := Assess(solutionRequest("Unsupported"))
	if len(got.Candidates) != 1 {
		t.Fatalf("expected one candidate, got %#v", got.Candidates)
	}
	if got.Candidates[0].Verification != VerificationUnverified {
		t.Fatalf("expected unverified candidate, got %#v", got.Candidates[0])
	}
	if len(got.Candidates[0].EvidenceRefs) != 0 {
		t.Fatalf("expected no evidence refs, got %#v", got.Candidates[0].EvidenceRefs)
	}
}

func solutionRequest(verification string) *v1alpha1.InvestigationRequest {
	return &v1alpha1.InvestigationRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout-latency", Namespace: "fluxseer-rca-system"},
		Status: v1alpha1.InvestigationRequestStatus{
			Claims: []v1alpha1.RCAClaim{{
				Statement:    "latency regression is caused by rollout",
				Verification: verification,
				EvidenceRefs: []string{"evidence-001"},
			}},
			Execution: &v1alpha1.RCAExecution{
				ProviderResult: &v1alpha1.RCAProviderResult{
					NormalizedResult: &v1alpha1.RCANormalizedResult{
						ActionType:        "rollout.pause",
						ActionDescription: "Pause the rollout while investigating the latency regression.",
						RollbackPlan:      []string{"resume rollout after latency returns to baseline"},
					},
				},
			},
		},
	}
}
