package investigation

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

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

func TestAuthorizeRelatedTargetsFailsClosedOnSnapshotErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "forbidden", err: apierrors.NewForbidden(schema.GroupResource{Resource: "services"}, "inventory", errors.New("denied"))},
		{name: "not-found", err: apierrors.NewNotFound(schema.GroupResource{Resource: "services"}, "inventory")},
		{name: "conflict", err: apierrors.NewConflict(schema.GroupResource{Resource: "services"}, "inventory", errors.New("conflict"))},
		{name: "timeout", err: context.DeadlineExceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtimeScheme(t)
			base := fake.NewClientBuilder().WithScheme(scheme).Build()
			calls := 0
			reader := interceptor.NewClient(base, interceptor.Funcs{
				Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, _ client.Object, opts ...client.GetOption) error {
					calls++
					return tt.err
				},
			})
			got := (&Service{Client: reader}).authorizeRelatedTargets(
				context.Background(),
				PreflightResult{Target: domain.ResourceRef{Cluster: "in-cluster", Namespace: "prod"}},
				nil,
				[]v1alpha1.EvidenceRef{{ID: "log-1", RelatedTargets: []v1alpha1.TargetRef{runtimeRelatedTarget()}}},
			)
			if calls != 1 {
				t.Fatalf("expected one bounded snapshot lookup, got %d", calls)
			}
			if len(got.Decisions) != 1 || got.Decisions[0].Disposition == authorization.DispositionAuthorized || got.Decisions[0].Reason != authorization.ReasonTargetIdentityUnavailable {
				t.Fatalf("snapshot error must fail closed as unresolved, got %#v", got.Decisions)
			}
		})
	}
}

func TestAuthorizeRelatedTargetsPassesSnapshotTimeoutContext(t *testing.T) {
	scheme := runtimeScheme(t)
	base := fake.NewClientBuilder().WithScheme(scheme).Build()
	deadlineSeen := false
	reader := interceptor.NewClient(base, interceptor.Funcs{
		Get: func(ctx context.Context, _ client.WithWatch, _ client.ObjectKey, _ client.Object, opts ...client.GetOption) error {
			_, deadlineSeen = ctx.Deadline()
			return context.DeadlineExceeded
		},
	})
	got := (&Service{Client: reader}).authorizeRelatedTargets(
		context.Background(),
		PreflightResult{Target: domain.ResourceRef{Cluster: "in-cluster", Namespace: "prod"}},
		nil,
		[]v1alpha1.EvidenceRef{{ID: "log-1", RelatedTargets: []v1alpha1.TargetRef{runtimeRelatedTarget()}}},
	)
	if !deadlineSeen {
		t.Fatal("snapshot lookup did not receive a cancellation deadline")
	}
	if len(got.Decisions) != 1 || got.Decisions[0].Disposition == authorization.DispositionAuthorized {
		t.Fatalf("timed out snapshot must not authorize target, got %#v", got.Decisions)
	}
}

func TestAuthorizeRelatedTargetsBoundsSnapshotLookupAttempts(t *testing.T) {
	scheme := runtimeScheme(t)
	baseObjects := make([]client.Object, 0, maxRelatedTargetSnapshots+8)
	refs := make([]v1alpha1.EvidenceRef, 0, maxRelatedTargetSnapshots+8)
	observations := make([]domain.Observation, 0, maxRelatedTargetSnapshots+8)
	for i := 0; i < maxRelatedTargetSnapshots+8; i++ {
		name := fmt.Sprintf("dependency-%02d", i)
		uid := types.UID(fmt.Sprintf("dependency-uid-%02d", i))
		baseObjects = append(baseObjects, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "prod", UID: uid}})
		target := runtimeRelatedTarget()
		target.Name = name
		id := fmt.Sprintf("log-%02d", i)
		refs = append(refs, v1alpha1.EvidenceRef{ID: id, RelatedTargets: []v1alpha1.TargetRef{target}})
		observations = append(observations, domain.Observation{ID: id, RelatedTargetUIDAssertions: []string{string(uid)}})
	}
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(baseObjects...).Build()
	calls := 0
	reader := interceptor.NewClient(base, interceptor.Funcs{
		Get: func(ctx context.Context, intercepted client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			calls++
			return intercepted.Get(ctx, key, obj, opts...)
		},
	})
	got := (&Service{Client: reader}).authorizeRelatedTargets(
		context.Background(),
		PreflightResult{Target: domain.ResourceRef{Cluster: "in-cluster", Namespace: "prod"}},
		observations,
		refs,
	)
	if calls != maxRelatedTargetSnapshots {
		t.Fatalf("expected %d bounded snapshot lookups, got %d", maxRelatedTargetSnapshots, calls)
	}
	authorized, unresolved := 0, 0
	for _, decision := range got.Decisions {
		switch decision.Disposition {
		case authorization.DispositionAuthorized:
			authorized++
		case authorization.DispositionUnresolved:
			unresolved++
		}
	}
	if authorized != maxRelatedTargetSnapshots || unresolved != 8 {
		t.Fatalf("expected bounded authorization split %d/%d, got authorized=%d unresolved=%d decisions=%#v", maxRelatedTargetSnapshots, 8, authorized, unresolved, got.Decisions)
	}
}

func TestAuthorizeRelatedTargetsStopsAfterContextCancellation(t *testing.T) {
	scheme := runtimeScheme(t)
	base := fake.NewClientBuilder().WithScheme(scheme).Build()
	calls := 0
	reader := interceptor.NewClient(base, interceptor.Funcs{
		Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, _ client.Object, opts ...client.GetOption) error {
			calls++
			return nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := (&Service{Client: reader}).authorizeRelatedTargets(
		ctx,
		PreflightResult{Target: domain.ResourceRef{Cluster: "in-cluster", Namespace: "prod"}},
		nil,
		[]v1alpha1.EvidenceRef{{ID: "log-1", RelatedTargets: []v1alpha1.TargetRef{runtimeRelatedTarget()}}},
	)
	if calls != 0 {
		t.Fatalf("canceled context must prevent snapshot lookups, got %d", calls)
	}
	if len(got.Decisions) != 1 || got.Decisions[0].Disposition == authorization.DispositionAuthorized {
		t.Fatalf("canceled context must fail closed, got %#v", got.Decisions)
	}
}

func TestAuthorizedTargetFailsClosedOnConflictingDecisions(t *testing.T) {
	target := runtimeRelatedTarget()
	context := RelatedTargetAuthorizationContext{
		Ready: true,
		Decisions: []authorization.Decision{
			{EvidenceID: "log-1", Target: target, Disposition: authorization.DispositionAuthorized},
			{EvidenceID: "log-1", Target: target, Disposition: authorization.DispositionRejected, Reason: authorization.ReasonTargetUIDMismatch},
		},
	}
	if context.AuthorizedTarget("log-1", target) || context.AuthorizedEvidenceRef(v1alpha1.EvidenceRef{ID: "log-1", RelatedTargets: []v1alpha1.TargetRef{target}}) {
		t.Fatal("conflicting decisions must not authorize an evidence relation")
	}
}

func TestAuthorizeRelatedTargetsRejectsDuplicateTargetUIDConflict(t *testing.T) {
	scheme := runtimeScheme(t)
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "inventory", Namespace: "prod", UID: "uid-a"}}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(service).Build()
	target := runtimeRelatedTarget()
	got := (&Service{Client: reader}).authorizeRelatedTargets(
		context.Background(),
		PreflightResult{Target: domain.ResourceRef{Cluster: "in-cluster", Namespace: "prod"}},
		[]domain.Observation{{ID: "log-1", RelatedTargetUIDAssertions: []string{"uid-a", "uid-b"}}},
		[]v1alpha1.EvidenceRef{{ID: "log-1", RelatedTargets: []v1alpha1.TargetRef{target, target}}},
	)
	if len(got.Decisions) != 2 || got.AuthorizedEvidenceRef(v1alpha1.EvidenceRef{ID: "log-1", RelatedTargets: []v1alpha1.TargetRef{target, target}}) {
		t.Fatalf("duplicate target UID conflict must fail closed, got %#v", got)
	}
	for _, decision := range got.Decisions {
		if decision.Reason != authorization.ReasonTargetAssertionConflict || decision.Disposition != authorization.DispositionRejected {
			t.Fatalf("expected assertion conflict rejection, got %#v", decision)
		}
	}
}

func TestAuthorizeRelatedTargetsCachesSnapshotAcrossEvidenceRefs(t *testing.T) {
	scheme := runtimeScheme(t)
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "inventory", Namespace: "prod", UID: "uid-a"}}
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(service).Build()
	calls := 0
	reader := interceptor.NewClient(base, interceptor.Funcs{
		Get: func(ctx context.Context, intercepted client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			calls++
			return intercepted.Get(ctx, key, obj, opts...)
		},
	})
	target := runtimeRelatedTarget()
	refs := []v1alpha1.EvidenceRef{
		{ID: "log-1", RelatedTargets: []v1alpha1.TargetRef{target}},
		{ID: "log-2", RelatedTargets: []v1alpha1.TargetRef{target}},
	}
	observations := []domain.Observation{
		{ID: "log-1", RelatedTargetUIDAssertions: []string{"uid-a"}},
		{ID: "log-2", RelatedTargetUIDAssertions: []string{"uid-a"}},
	}
	got := (&Service{Client: reader}).authorizeRelatedTargets(
		context.Background(),
		PreflightResult{Target: domain.ResourceRef{Cluster: "in-cluster", Namespace: "prod"}},
		observations,
		refs,
	)
	if calls != 1 {
		t.Fatalf("expected one cached snapshot lookup, got %d", calls)
	}
	for _, ref := range refs {
		if !got.AuthorizedEvidenceRef(ref) {
			t.Fatalf("same UID relation should remain authorized, ref=%#v decisions=%#v", ref, got.Decisions)
		}
	}
}

func TestAuthorizeRelatedTargetsRejectsCrossEvidenceUIDConflict(t *testing.T) {
	scheme := runtimeScheme(t)
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "inventory", Namespace: "prod", UID: "uid-a"}}
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(service).Build()
	calls := 0
	reader := interceptor.NewClient(base, interceptor.Funcs{
		Get: func(ctx context.Context, intercepted client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			calls++
			return intercepted.Get(ctx, key, obj, opts...)
		},
	})
	target := runtimeRelatedTarget()
	refs := []v1alpha1.EvidenceRef{
		{ID: "log-1", RelatedTargets: []v1alpha1.TargetRef{target}},
		{ID: "log-2", RelatedTargets: []v1alpha1.TargetRef{target}},
	}
	got := (&Service{Client: reader}).authorizeRelatedTargets(
		context.Background(),
		PreflightResult{Target: domain.ResourceRef{Cluster: "in-cluster", Namespace: "prod"}},
		[]domain.Observation{
			{ID: "log-1", RelatedTargetUIDAssertions: []string{"uid-a"}},
			{ID: "log-2", RelatedTargetUIDAssertions: []string{"uid-b"}},
		},
		refs,
	)
	if calls != 1 {
		t.Fatalf("expected one cached snapshot lookup despite cross-evidence conflict, got %d", calls)
	}
	for _, ref := range refs {
		if got.AuthorizedEvidenceRef(ref) {
			t.Fatalf("cross-evidence UID conflict must invalidate every relation, ref=%#v decisions=%#v", ref, got.Decisions)
		}
	}
}

func runtimeRelatedTarget() v1alpha1.TargetRef {
	return v1alpha1.TargetRef{Cluster: "in-cluster", Namespace: "prod", Kind: "Service", Name: "inventory", APIVersion: "v1"}
}

func runtimeScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	return scheme
}
