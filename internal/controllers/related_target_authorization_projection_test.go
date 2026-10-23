package controllers

import (
	"testing"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/authorization"
	"github.com/FluxSeer/fluxseer-rca/internal/investigation"
)

func TestRiskSignalEvidenceRefsRemoveRejectedRelatedTargets(t *testing.T) {
	target := v1alpha1.TargetRef{Cluster: "in-cluster", Namespace: "payments", Kind: "Service", Name: "inventory", APIVersion: "v1"}
	ref := v1alpha1.EvidenceRef{ID: "log-1", Kind: "log", Source: "loki", RelatedTargets: []v1alpha1.TargetRef{target}}
	got := riskSignalEvidenceRefs(investigation.EvidenceCollectionResult{
		EvidenceRefs: []v1alpha1.EvidenceRef{ref},
		RelatedTargetAuthorization: investigation.RelatedTargetAuthorizationContext{
			Ready: true,
			Decisions: []authorization.Decision{{
				EvidenceID:  ref.ID,
				Target:      target,
				Disposition: authorization.DispositionRejected,
				Reason:      authorization.ReasonCrossScopeTarget,
			}},
		},
	})
	if len(got) != 1 || len(got[0].RelatedTargets) != 0 {
		t.Fatalf("expected rejected related target to be removed from RiskSignal projection, got %#v", got)
	}
	if len(ref.RelatedTargets) != 1 {
		t.Fatalf("expected source evidence to remain unchanged, got %#v", ref)
	}
}

func TestRiskSignalEvidenceRefsRetainAuthorizedRelatedTargets(t *testing.T) {
	target := v1alpha1.TargetRef{Cluster: "in-cluster", Namespace: "prod", Kind: "Service", Name: "inventory", APIVersion: "v1"}
	ref := v1alpha1.EvidenceRef{ID: "log-1", Kind: "log", Source: "loki", RelatedTargets: []v1alpha1.TargetRef{target}}
	got := riskSignalEvidenceRefs(investigation.EvidenceCollectionResult{
		EvidenceRefs: []v1alpha1.EvidenceRef{ref},
		RelatedTargetAuthorization: investigation.RelatedTargetAuthorizationContext{
			Ready: true,
			Decisions: []authorization.Decision{{
				EvidenceID:  ref.ID,
				Target:      target,
				Disposition: authorization.DispositionAuthorized,
			}},
		},
	})
	if len(got) != 1 || len(got[0].RelatedTargets) != 1 || got[0].RelatedTargets[0].Name != "inventory" {
		t.Fatalf("expected authorized related target to remain in RiskSignal projection, got %#v", got)
	}
}

func TestVerifierEvidenceRefsPreserveImplicitIDsAfterAuthorizationFiltering(t *testing.T) {
	target := v1alpha1.TargetRef{Cluster: "in-cluster", Namespace: "payments", Kind: "Service", Name: "inventory", APIVersion: "v1"}
	got := verifierEvidenceRefs(investigation.EvidenceCollectionResult{
		EvidenceRefs: []v1alpha1.EvidenceRef{
			{Kind: "log", Source: "loki", RelatedTargets: []v1alpha1.TargetRef{target}},
			{Kind: "metric", Source: "prometheus"},
		},
		RelatedTargetAuthorization: investigation.RelatedTargetAuthorizationContext{
			Ready: true,
			Decisions: []authorization.Decision{{
				EvidenceID:  "",
				Target:      target,
				Disposition: authorization.DispositionRejected,
			}},
		},
	})
	if len(got) != 1 || got[0].ID != "evidence-002" {
		t.Fatalf("expected filtered evidence to retain its original implicit ID, got %#v", got)
	}
}
