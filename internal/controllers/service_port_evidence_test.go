package controllers

import (
	"testing"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
	"github.com/FluxSeer/fluxseer-rca/internal/investigation"
	"github.com/FluxSeer/fluxseer-rca/internal/verifier"
)

func TestInferRootCauseTypePromotesServicePortMismatchOnlyWithServiceEvidence(t *testing.T) {
	claims := []v1alpha1.RCAClaim{{
		Statement:    "Service targetPort 8080 does not match the checkout container port 3000",
		Verification: verifier.VerificationSupported,
	}}
	serviceEvidence := investigation.EvidenceCollectionResult{EvidenceRefs: []v1alpha1.EvidenceRef{{
		Kind:    "serviceConfiguration",
		Reason:  "ServicePortMismatch",
		Summary: "Service targetPort=8080 resolved against containerPort=3000; mismatchConfirmed=true",
	}}}
	if got := inferRootCauseTypeFromClaims(claims, serviceEvidence); got != "ServicePortMismatch" {
		t.Fatalf("expected bounded ServicePortMismatch taxonomy, got %q", got)
	}

	eventOnly := investigation.EvidenceCollectionResult{EvidenceRefs: []v1alpha1.EvidenceRef{{
		Kind:    "event",
		Reason:  "ConfigurationMismatch",
		Summary: "Service targetPort 8080 does not match the checkout container port 3000",
	}}}
	if got := inferRootCauseTypeFromClaims(claims, eventOnly); got != "ConfigurationMismatch" {
		t.Fatalf("expected generic configuration taxonomy without service evidence, got %q", got)
	}
}

func TestProfileBackedServicePortClaimOverridesUnrelatedUnsupportedProviderClaims(t *testing.T) {
	evidence := investigation.EvidenceCollectionResult{EvidenceRefs: []v1alpha1.EvidenceRef{
		{
			ID:      "service-port-evidence",
			Kind:    string(domain.QueryTypeServiceConfiguration),
			Reason:  "ServicePortMismatch",
			Summary: "Service targetPort=8080 resolved against containerPort=3000; mismatchConfirmed=true",
		},
	}}
	rca := investigation.RCAResult{Reasoning: &domain.ReasoningOutput{
		RiskSummary: "Service is degraded",
		RCA: domain.RCASummary{Causes: []string{
			"Recent rollout changed workload behavior",
			"Pod memory usage crossed safe threshold",
		}},
	}}

	claims, verification := buildRCAClaims(rca, evidence, "serviceportmismatch")
	evaluation := evaluateCanonicalVerdict(rca, evidence, claims, 0.95)
	if evaluation.Outcome != v1alpha1.InvestigationOutcomeConfirmed {
		t.Fatalf("expected profile-backed RCA to be confirmed, got %s (claims=%#v, verification=%#v)", evaluation.Outcome, claims, verification)
	}
	if evaluation.RootCauseType != "ServicePortMismatch" {
		t.Fatalf("expected ServicePortMismatch root cause, got %q", evaluation.RootCauseType)
	}
	if len(evaluation.MissingEvidence) != 0 {
		t.Fatalf("expected no unrelated missing evidence, got %#v", evaluation.MissingEvidence)
	}
	if len(claims) != 4 || claims[3].Verification != verifier.VerificationSupported {
		t.Fatalf("expected supported profile-backed claim, got %#v", claims)
	}
}

func TestProfileBackedServicePortClaimRejectsUnresolvedNamedTarget(t *testing.T) {
	evidence := investigation.EvidenceCollectionResult{EvidenceRefs: []v1alpha1.EvidenceRef{{
		ID:      "unresolved-target-port",
		Kind:    string(domain.QueryTypeServiceConfiguration),
		Reason:  "TargetPortUnresolved",
		Summary: "Service targetPort=http could not be resolved to a named workload container port",
	}}}
	if servicePortMismatchEvidencePresent(evidence) {
		t.Fatal("unresolved named targetPort must not satisfy ServicePortMismatch evidence")
	}
	if statement, ok := profileBackedRCAClaim("serviceportmismatch", evidence); ok {
		t.Fatalf("unexpected profile-backed claim %q for unresolved named targetPort", statement)
	}
}
