package thresholds

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
)

func TestEnforcerSelectsHighestPriorityThreshold(t *testing.T) {
	enforcer := newEnforcer(t,
		&v1alpha1.NamespaceThreshold{
			ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: "policy-system"},
			Spec: v1alpha1.NamespaceThresholdSpec{
				Enabled:               true,
				Priority:              1,
				ActivePlansLimit:      100,
				PendingApprovalsLimit: 50,
			},
		},
		&v1alpha1.NamespaceThreshold{
			ObjectMeta: metav1.ObjectMeta{Name: "prod-limit", Namespace: "policy-system", ResourceVersion: "8"},
			Spec: v1alpha1.NamespaceThresholdSpec{
				Enabled:          true,
				Priority:         10,
				ActivePlansLimit: 2,
				NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
					"tier": "production",
				}},
			},
		},
	)

	resolution, err := enforcer.Resolve(context.Background(), ResolveRequest{
		Namespace:       "prod",
		NamespaceLabels: map[string]string{"tier": "production"},
	})
	if err != nil {
		t.Fatalf("resolve threshold: %v", err)
	}
	if resolution == nil || resolution.Threshold.Name != "prod-limit" {
		t.Fatalf("expected prod threshold, got %#v", resolution)
	}
	if resolution.Reference.Version != "8" {
		t.Fatalf("expected threshold version 8, got %#v", resolution.Reference)
	}
}

func TestEnforcerPrefersDirectNamespaceThresholdOnPriorityTie(t *testing.T) {
	enforcer := newEnforcer(t,
		&v1alpha1.NamespaceThreshold{
			ObjectMeta: metav1.ObjectMeta{Name: "selector", Namespace: "policy-system"},
			Spec: v1alpha1.NamespaceThresholdSpec{
				Enabled:  true,
				Priority: 5,
				NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
					"team": "payments",
				}},
				ActivePlansLimit: 10,
			},
		},
		&v1alpha1.NamespaceThreshold{
			ObjectMeta: metav1.ObjectMeta{Name: "direct", Namespace: "prod"},
			Spec: v1alpha1.NamespaceThresholdSpec{
				Enabled:          true,
				Priority:         5,
				ActivePlansLimit: 3,
			},
		},
	)

	resolution, err := enforcer.Resolve(context.Background(), ResolveRequest{
		Namespace:       "prod",
		NamespaceLabels: map[string]string{"team": "payments"},
	})
	if err != nil {
		t.Fatalf("resolve threshold: %v", err)
	}
	if resolution == nil || resolution.Threshold.Name != "direct" {
		t.Fatalf("expected direct threshold, got %#v", resolution)
	}
}

func TestEnforcerReportsUsageViolations(t *testing.T) {
	enforcer := newEnforcer(t, &v1alpha1.NamespaceThreshold{
		ObjectMeta: metav1.ObjectMeta{Name: "prod-limit", Namespace: "prod"},
		Spec: v1alpha1.NamespaceThresholdSpec{
			Enabled:               true,
			ActivePlansLimit:      2,
			PendingApprovalsLimit: 1,
		},
	})

	result, err := enforcer.Enforce(context.Background(), ResolveRequest{Namespace: "prod"}, Usage{
		ActivePlans:      3,
		PendingApprovals: 2,
	})
	if err != nil {
		t.Fatalf("enforce threshold: %v", err)
	}
	if result.Allowed || len(result.Violations) != 2 {
		t.Fatalf("expected two violations, got %#v", result)
	}
}

func TestEnforcerAllowsUnlimitedFieldsAndNoMatch(t *testing.T) {
	enforcer := newEnforcer(t, &v1alpha1.NamespaceThreshold{
		ObjectMeta: metav1.ObjectMeta{Name: "dev-limit", Namespace: "dev"},
		Spec: v1alpha1.NamespaceThresholdSpec{
			Enabled:               true,
			ActivePlansLimit:      0,
			PendingApprovalsLimit: 0,
		},
	})

	result, err := enforcer.Evaluate(context.Background(), ResolveRequest{Namespace: "prod"}, Usage{
		ActivePlans:      1000,
		PendingApprovals: 1000,
	})
	if err != nil {
		t.Fatalf("evaluate unmatched threshold: %v", err)
	}
	if !result.Allowed || result.Resolution != nil {
		t.Fatalf("expected no-match allow result, got %#v", result)
	}

	result, err = enforcer.Evaluate(context.Background(), ResolveRequest{Namespace: "dev"}, Usage{
		ActivePlans:      1000,
		PendingApprovals: 1000,
	})
	if err != nil {
		t.Fatalf("evaluate unlimited threshold: %v", err)
	}
	if !result.Allowed || len(result.Violations) != 0 {
		t.Fatalf("expected unlimited result, got %#v", result)
	}
}

func TestEnforcerSkipsDisabledThreshold(t *testing.T) {
	enforcer := newEnforcer(t, &v1alpha1.NamespaceThreshold{
		ObjectMeta: metav1.ObjectMeta{Name: "disabled", Namespace: "prod"},
		Spec: v1alpha1.NamespaceThresholdSpec{
			Enabled:          false,
			ActivePlansLimit: 1,
		},
	})

	result, err := enforcer.Evaluate(context.Background(), ResolveRequest{Namespace: "prod"}, Usage{ActivePlans: 10})
	if err != nil {
		t.Fatalf("evaluate disabled threshold: %v", err)
	}
	if !result.Allowed || result.Resolution != nil {
		t.Fatalf("expected disabled threshold to be ignored, got %#v", result)
	}
}

func TestEnforcerNilSelectorOnlyMatchesOwnNamespace(t *testing.T) {
	// Contract test: a nil NamespaceSelector only matches the threshold's own namespace (safe default).
	enforcer := newEnforcer(t,
		&v1alpha1.NamespaceThreshold{
			ObjectMeta: metav1.ObjectMeta{Name: "prod-only", Namespace: "prod"},
			Spec: v1alpha1.NamespaceThresholdSpec{
				Enabled:          true,
				ActivePlansLimit: 5,
				// No NamespaceSelector (nil) = only applies to this namespace
			},
		},
	)

	// Should match when requesting the same namespace.
	resolution, err := enforcer.Resolve(context.Background(), ResolveRequest{Namespace: "prod"})
	if err != nil {
		t.Fatalf("resolve same namespace: %v", err)
	}
	if resolution == nil || resolution.Threshold.Name != "prod-only" {
		t.Fatalf("expected nil selector to match same namespace, got %#v", resolution)
	}

	// Should NOT match a different namespace.
	resolution, err = enforcer.Resolve(context.Background(), ResolveRequest{Namespace: "dev"})
	if err != nil {
		t.Fatalf("resolve different namespace: %v", err)
	}
	if resolution != nil {
		t.Fatalf("expected nil selector to NOT match different namespace, got %#v", resolution)
	}
}

func TestEnforcerExplicitEmptySelectorMatchesAllNamespaces(t *testing.T) {
	// Contract test: an explicit empty NamespaceSelector matches all namespaces.
	enforcer := newEnforcer(t,
		&v1alpha1.NamespaceThreshold{
			ObjectMeta: metav1.ObjectMeta{Name: "global-limit", Namespace: "policy-system"},
			Spec: v1alpha1.NamespaceThresholdSpec{
				Enabled:          true,
				ActivePlansLimit: 100,
				// Explicit empty selector = applies to all namespaces
				NamespaceSelector: &metav1.LabelSelector{},
			},
		},
	)

	// Should match any namespace.
	resolution, err := enforcer.Resolve(context.Background(), ResolveRequest{Namespace: "prod"})
	if err != nil {
		t.Fatalf("resolve with empty selector: %v", err)
	}
	if resolution == nil || resolution.Threshold.Name != "global-limit" {
		t.Fatalf("expected empty selector to match all namespaces, got %#v", resolution)
	}

	// Should also match another namespace.
	resolution, err = enforcer.Resolve(context.Background(), ResolveRequest{Namespace: "dev"})
	if err != nil {
		t.Fatalf("resolve different namespace with empty selector: %v", err)
	}
	if resolution == nil || resolution.Threshold.Name != "global-limit" {
		t.Fatalf("expected empty selector to match dev namespace, got %#v", resolution)
	}
}

func newEnforcer(t *testing.T, thresholds ...*v1alpha1.NamespaceThreshold) *Enforcer {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add API scheme: %v", err)
	}
	objects := make([]client.Object, 0, len(thresholds))
	for _, threshold := range thresholds {
		objects = append(objects, threshold)
	}
	return NewEnforcer(fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build())
}
