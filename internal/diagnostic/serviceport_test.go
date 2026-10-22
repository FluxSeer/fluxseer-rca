package diagnostic

import (
	"testing"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
	"github.com/FluxSeer/fluxseer-rca/internal/scope"
	"k8s.io/apimachinery/pkg/types"
)

func TestServicePortDiagnosticProducesScopedCandidate(t *testing.T) {
	evidence := servicePortEvidenceSet()
	result := (ServicePortDiagnostic{}).Evaluate(evidence)
	if result.Status != StatusSupported || len(result.Claims) != 1 {
		t.Fatalf("expected one supported service-port candidate, got %#v", result)
	}
	claim := result.Claims[0]
	if claim.Type != ClaimRootCause || claim.TargetUID != "pod-1" || len(claim.EvidenceRefs) != 1 {
		t.Fatalf("expected target-scoped root-cause candidate, got %#v", claim)
	}
}

func TestServicePortDiagnosticDoesNotMatchWithoutSinglePodScope(t *testing.T) {
	evidence := servicePortEvidenceSet()
	evidence.Scope.Pods = append(evidence.Scope.Pods, scope.ResourceRef{UID: types.UID("pod-2")})
	if match := (ServicePortDiagnostic{}).Match(evidence); match.Applicable {
		t.Fatalf("expected multi-Pod service evidence to remain outside candidate scope: %#v", match)
	}
	result := (ServicePortDiagnostic{}).Evaluate(evidence)
	if result.Status != StatusInconclusive || len(result.Claims) != 0 {
		t.Fatalf("expected no unscoped service-port claim, got %#v", result)
	}
}

func servicePortEvidenceSet() EvidenceSet {
	return EvidenceSet{
		Scope: scope.Result{Complete: true, Pods: []scope.ResourceRef{{UID: types.UID("pod-1")}}},
		Observations: []domain.Observation{{
			ID:   "evidence-001",
			Type: domain.ObservationTypeServiceConfiguration,
			Value: domain.ObservationValue{ServiceConfiguration: &domain.ServiceConfigurationObservation{
				MismatchConfirmed: true,
			}},
		}},
		EvidenceRefs: []v1alpha1.EvidenceRef{{ID: "evidence-001", Kind: "serviceConfiguration", Reason: "ServicePortMismatch"}},
	}
}
