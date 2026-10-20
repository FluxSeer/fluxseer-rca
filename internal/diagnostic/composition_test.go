package diagnostic

import (
	"testing"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
)

func TestPlannerKeepsImagePullAndSchedulingClaimsIsolated(t *testing.T) {
	evidence := EvidenceSet{
		EvidenceRefs: []v1alpha1.EvidenceRef{{
			ID: "image-event", Kind: "event", Reason: "ErrImagePull", Summary: "failed to pull image",
		}},
		Scheduling: []SchedulingObservation{
			{ID: "state-image", PodUID: "pod-image", Phase: "Pending", NodeName: "node-1", EvidenceRefs: []string{"state-image", "image-event"}},
			{ID: "event-scheduling", PodUID: "pod-scheduling", Phase: "Pending", NodeName: "", PodScheduledStatus: "False", EventPodUID: "pod-scheduling", EventReason: "FailedScheduling", EventMessage: "0/2 nodes are available: 2 Insufficient cpu.", EvidenceRefs: []string{"event-scheduling"}},
		},
	}

	results := NewPlanner(ImagePullDiagnostic{}, SchedulingDiagnostic{}).Evaluate(evidence)
	if len(results) != 2 || results[0].DiagnosticID != "image-pull" || results[1].DiagnosticID != "scheduling" {
		t.Fatalf("expected deterministic image-pull and scheduling results, got %#v", results)
	}

	for _, claim := range results[0].Claims {
		for _, ref := range claim.EvidenceRefs {
			if ref == "event-scheduling" {
				t.Fatalf("image-pull claim consumed scheduling evidence: %#v", claim)
			}
		}
	}
	for _, claim := range results[1].Claims {
		if claim.TargetUID != "pod-scheduling" {
			t.Fatalf("scheduling claim was not isolated to the scheduling Pod UID: %#v", claim)
		}
		for _, ref := range claim.EvidenceRefs {
			if ref == "image-event" {
				t.Fatalf("scheduling claim consumed image-pull evidence: %#v", claim)
			}
		}
	}
}
