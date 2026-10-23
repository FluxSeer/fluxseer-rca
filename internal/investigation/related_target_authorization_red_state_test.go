package investigation

import (
	"testing"
	"time"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
)

func TestDiagnosticEvidenceInventoryRejectsUnverifiedRelatedTarget(t *testing.T) {
	start := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	result := EvidenceCollectionResult{
		EvidenceRefs: []v1alpha1.EvidenceRef{{
			ID:     "log-cross-namespace",
			Kind:   "log",
			Source: "loki",
			RelatedTargets: []v1alpha1.TargetRef{{
				Cluster:   "test-cluster",
				Namespace: "payments",
				Kind:      "Service",
				Name:      "inventory",
			}},
		}},
	}

	inventory := diagnosticEvidenceInventory(result, []string{"deployment-checkout-uid"}, start, start.Add(15*time.Minute))
	if len(inventory) != 1 {
		t.Fatalf("expected one inventory entry, got %#v", inventory)
	}
	if inventory[0].Authorized || !inventory[0].Valid {
		t.Fatalf("expected unverified related target evidence to be excluded from trusted inventory: inventory=%#v", inventory[0])
	}
	t.Logf("RED_STATE baseline marked related target evidence authorized; fixed path now rejects it: %#v", inventory[0])
}
