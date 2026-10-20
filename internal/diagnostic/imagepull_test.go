package diagnostic

import (
	"testing"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
	"github.com/FluxSeer/fluxseer-rca/internal/scope"
)

func TestImagePullDiagnosticProducesObservationAndMechanismClaims(t *testing.T) {
	diagnostic := ImagePullDiagnostic{}
	evidence := EvidenceSet{
		Observations: []domain.Observation{{
			ID:   "event-001",
			Type: domain.ObservationTypeEvent,
			Value: domain.ObservationValue{Event: &domain.EventObservation{
				Reason:  "ErrImagePull",
				Message: "failed to pull image",
			}},
			Summary: "failed to pull image",
		}},
		Scope: completeScope(),
	}

	if match := diagnostic.Match(evidence); !match.Applicable {
		t.Fatalf("expected image-pull diagnostic to match: %#v", match)
	}
	result := diagnostic.Evaluate(evidence)
	if result.Status != StatusSupported {
		t.Fatalf("expected supported failure mechanism, got %#v", result)
	}
	if len(result.Claims) != 2 || result.Claims[0].Type != ClaimObservation || result.Claims[1].Type != ClaimFailureMechanism {
		t.Fatalf("expected observation and mechanism claims, got %#v", result.Claims)
	}
	for _, claim := range result.Claims {
		if len(claim.EvidenceRefs) != 1 || claim.EvidenceRefs[0] != "event-001" {
			t.Fatalf("expected claim to link event evidence, got %#v", claim)
		}
	}
}

func TestImagePullDiagnosticProposesSpecificCauseWithoutConfirmingIt(t *testing.T) {
	diagnostic := ImagePullDiagnostic{}
	evidence := EvidenceSet{EvidenceRefs: []v1alpha1.EvidenceRef{{
		ID: "event-001", Kind: "event", Reason: "ErrImagePull", Summary: "failed to pull image: manifest unknown: image not found",
	}}, Scope: completeScope()}

	result := diagnostic.Evaluate(evidence)
	if result.Status != StatusSupported {
		t.Fatalf("expected supported diagnostic, got %#v", result.Status)
	}
	if len(result.Claims) != 3 || result.Claims[2].Type != ClaimRootCause {
		t.Fatalf("expected proposed root-cause claim, got %#v", result.Claims)
	}
	if result.Claims[2].Statement == "" || result.Claims[2].EvidenceRefs[0] != "event-001" {
		t.Fatalf("expected specific cause candidate linked to event, got %#v", result.Claims[2])
	}
	if result.Status == StatusConfirmed {
		t.Fatal("diagnostic must not independently confirm the RCA root cause")
	}
}

func TestImagePullDiagnosticKeepsTruncatedDetailInconclusive(t *testing.T) {
	diagnostic := ImagePullDiagnostic{}
	evidence := EvidenceSet{
		EvidenceRefs: []v1alpha1.EvidenceRef{{
			ID: "event-001", Kind: "event", Reason: "ImagePullBackOff", Summary: "pull access denied: authentication required", Truncated: true,
		}},
		Scope: completeScope(),
	}

	result := diagnostic.Evaluate(evidence)
	if result.Status != StatusInconclusive {
		t.Fatalf("expected truncated evidence to remain inconclusive, got %#v", result.Status)
	}
	if len(result.Claims) != 2 {
		t.Fatalf("expected no root-cause claim from truncated detail, got %#v", result.Claims)
	}
	if len(result.MissingRequirements) == 0 || result.MissingRequirements[0].ID != imagePullCauseRequirement {
		t.Fatalf("expected missing cause detail requirement, got %#v", result.MissingRequirements)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("expected truncation warning")
	}
}

func TestImagePullDiagnosticDoesNotMatchDeploymentWithoutPods(t *testing.T) {
	diagnostic := ImagePullDiagnostic{}
	evidence := EvidenceSet{Scope: scope.Result{
		Deployment: scope.ResourceRef{Namespace: "prod", Kind: "Deployment", Name: "web", UID: "deployment-1"},
		Complete:   true,
	}}
	if match := diagnostic.Match(evidence); match.Applicable {
		t.Fatalf("expected no-pod scope to be not applicable, got %#v", match)
	}
	result := diagnostic.Evaluate(evidence)
	if result.Status != StatusNotApplicable {
		t.Fatalf("expected not applicable result, got %#v", result.Status)
	}
}

func TestImagePullDiagnosticUsesEventObservationAndReferenceDeterministically(t *testing.T) {
	diagnostic := ImagePullDiagnostic{}
	evidence := EvidenceSet{
		Observations: []domain.Observation{{
			ID:   "event-002",
			Type: domain.ObservationTypeEvent,
			Value: domain.ObservationValue{Event: &domain.EventObservation{
				Reason: "ImagePullBackOff",
			}},
		}},
		EvidenceRefs: []v1alpha1.EvidenceRef{{ID: "event-001", Kind: "event", Reason: "ErrImagePull", Summary: "failed to pull image"}},
		Scope:        completeScope(),
	}
	result := diagnostic.Evaluate(evidence)
	if len(result.Claims) < 2 || len(result.Claims[0].EvidenceRefs) != 2 || result.Claims[0].EvidenceRefs[0] != "event-001" || result.Claims[0].EvidenceRefs[1] != "event-002" {
		t.Fatalf("expected sorted deterministic evidence references, got %#v", result.Claims)
	}
}

func completeScope() scope.Result {
	return scope.Result{
		Deployment: scope.ResourceRef{Namespace: "prod", Kind: "Deployment", Name: "web", UID: "deployment-1"},
		Pods:       []scope.ResourceRef{{Namespace: "prod", Kind: "Pod", Name: "web-pod", UID: "pod-1"}},
		Complete:   true,
	}
}
