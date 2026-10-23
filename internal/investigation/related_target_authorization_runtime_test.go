package investigation

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/authorization"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
)

func fixedRuntimeTestTime() time.Time {
	return time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
}

func TestAuthorizeRelatedTargetsUsesTrustedSnapshotAndEvidenceUID(t *testing.T) {
	scheme := runtimeScheme(t)
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "inventory", Namespace: "prod", UID: "service-inventory-uid"}}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(service).Build()
	runtimeService := &Service{Client: reader}
	preflight := PreflightResult{Target: domain.ResourceRef{Cluster: "in-cluster", Namespace: "prod", Kind: "Deployment", Name: "checkout"}}
	target := v1alpha1.TargetRef{Cluster: "in-cluster", Namespace: "prod", Kind: "Service", Name: "inventory", APIVersion: "v1"}
	refs := []v1alpha1.EvidenceRef{{ID: "log-1", Kind: "log", RelatedTargets: []v1alpha1.TargetRef{target}}}

	got := runtimeService.authorizeRelatedTargets(context.Background(), preflight, []domain.Observation{{
		ID:                         "log-1",
		RelatedTargets:             []domain.ResourceRef{{Cluster: "in-cluster", Namespace: "prod", Kind: "Service", Name: "inventory", APIVersion: "v1"}},
		RelatedTargetUIDAssertions: []string{"service-inventory-uid"},
	}}, refs)
	if !got.Ready || len(got.Decisions) != 1 {
		t.Fatalf("expected one ready authorization decision, got %#v", got)
	}
	if got.Decisions[0].Disposition != authorization.DispositionAuthorized || got.Decisions[0].Reason != authorization.ReasonTargetAuthorized {
		t.Fatalf("expected trusted target authorization, got %#v", got.Decisions[0])
	}
}

func TestAuthorizeRelatedTargetsLeavesHistoricalUIDUnresolved(t *testing.T) {
	scheme := runtimeScheme(t)
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "inventory", Namespace: "prod", UID: "service-current-uid"}}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(service).Build()
	runtimeService := &Service{Client: reader}
	preflight := PreflightResult{Target: domain.ResourceRef{Cluster: "in-cluster", Namespace: "prod", Kind: "Deployment", Name: "checkout"}}
	target := v1alpha1.TargetRef{Cluster: "in-cluster", Namespace: "prod", Kind: "Service", Name: "inventory", APIVersion: "v1"}
	got := runtimeService.authorizeRelatedTargets(context.Background(), preflight, nil, []v1alpha1.EvidenceRef{{ID: "log-1", Kind: "log", RelatedTargets: []v1alpha1.TargetRef{target}}})
	if len(got.Decisions) != 1 || got.Decisions[0].Disposition != authorization.DispositionUnresolved || got.Decisions[0].Reason != authorization.ReasonTargetIdentityUnavailable {
		t.Fatalf("expected unresolved historical identity, got %#v", got)
	}
}

func TestDiagnosticEvidenceInventoryUsesRelatedTargetAuthorization(t *testing.T) {
	target := v1alpha1.TargetRef{Cluster: "in-cluster", Namespace: "prod", Kind: "Service", Name: "inventory", APIVersion: "v1"}
	result := EvidenceCollectionResult{
		EvidenceRefs: []v1alpha1.EvidenceRef{{ID: "log-1", Kind: "log", RelatedTargets: []v1alpha1.TargetRef{target}}},
		RelatedTargetAuthorization: RelatedTargetAuthorizationContext{
			Ready: true,
			Decisions: []authorization.Decision{{
				EvidenceID:  "log-1",
				Target:      target,
				Disposition: authorization.DispositionRejected,
				Reason:      authorization.ReasonCrossScopeTarget,
			}},
		},
	}
	inventory := diagnosticEvidenceInventory(result, []string{"deployment-uid"}, fixedRuntimeTestTime(), fixedRuntimeTestTime().Add(15*time.Minute))
	if len(inventory) != 1 || inventory[0].Authorized {
		t.Fatalf("expected rejected related target evidence to stay out of trusted inventory, got %#v", inventory)
	}
}

func runtimeScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	return scheme
}
