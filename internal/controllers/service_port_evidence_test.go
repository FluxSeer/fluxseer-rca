package controllers

import (
	"testing"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
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
