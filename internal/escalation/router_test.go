package escalation

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
)

func TestRouterResolvesExplicitReference(t *testing.T) {
	chain := &v1alpha1.EscalationChain{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-chain", Namespace: "prod", ResourceVersion: "12"},
		Spec: v1alpha1.EscalationChainSpec{
			Enabled: true,
			Stages:  []v1alpha1.EscalationStage{{Name: "on-call"}},
		},
	}
	router := newRouter(t, chain)

	route, err := router.Resolve(context.Background(), ResolveRequest{
		Namespace:          "prod",
		EscalationChainRef: "payments-chain",
	})
	if err != nil {
		t.Fatalf("resolve explicit chain: %v", err)
	}
	if route == nil || route.Chain.Name != "payments-chain" {
		t.Fatalf("expected payments chain, got %#v", route)
	}
	if route.Reference.Version != "12" {
		t.Fatalf("expected resource version audit reference, got %#v", route.Reference)
	}
}

func TestRouterSelectsHighestPriorityMatchingChain(t *testing.T) {
	router := newRouter(t,
		&v1alpha1.EscalationChain{
			ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: "prod"},
			Spec: v1alpha1.EscalationChainSpec{
				Enabled:  true,
				Priority: 0,
				Stages:   []v1alpha1.EscalationStage{{Name: "default-stage"}},
			},
		},
		&v1alpha1.EscalationChain{
			ObjectMeta: metav1.ObjectMeta{Name: "checkout", Namespace: "prod"},
			Spec: v1alpha1.EscalationChainSpec{
				Enabled:  true,
				Priority: 1,
				ResourceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
					"app": "checkout",
				}},
				Stages: []v1alpha1.EscalationStage{{Name: "checkout-stage"}},
			},
		},
	)

	route, err := router.Resolve(context.Background(), ResolveRequest{
		Namespace:      "prod",
		ResourceLabels: map[string]string{"app": "checkout"},
	})
	if err != nil {
		t.Fatalf("resolve matching chain: %v", err)
	}
	if route == nil || route.Chain.Name != "checkout" {
		t.Fatalf("expected selector-specific chain to win, got %#v", route)
	}
}

func TestRouterReturnsNilWhenNoChainMatches(t *testing.T) {
	router := newRouter(t, &v1alpha1.EscalationChain{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout", Namespace: "prod"},
		Spec: v1alpha1.EscalationChainSpec{
			Enabled: true,
			ResourceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
				"app": "checkout",
			}},
			Stages: []v1alpha1.EscalationStage{{Name: "checkout-stage"}},
		},
	})

	route, err := router.Resolve(context.Background(), ResolveRequest{
		Namespace:      "prod",
		ResourceLabels: map[string]string{"app": "billing"},
	})
	if err != nil {
		t.Fatalf("resolve no-match chain: %v", err)
	}
	if route != nil {
		t.Fatalf("expected no route, got %#v", route)
	}
}

func TestRouterRejectsMissingExplicitReference(t *testing.T) {
	router := newRouter(t)
	if _, err := router.Resolve(context.Background(), ResolveRequest{
		Namespace:          "prod",
		EscalationChainRef: "missing",
	}); err == nil {
		t.Fatal("expected missing explicit chain to return an error")
	}
}

func TestRouterNilResourceSelectorMatchesAllResources(t *testing.T) {
	// Contract test: a nil ResourceSelector in EscalationChain matches all resources.
	router := newRouter(t,
		&v1alpha1.EscalationChain{
			ObjectMeta: metav1.ObjectMeta{Name: "default-chain", Namespace: "prod"},
			Spec: v1alpha1.EscalationChainSpec{
				Enabled:  true,
				Priority: 0,
				// No ResourceSelector (nil) = applies to all resources
				Stages: []v1alpha1.EscalationStage{{Name: "default-stage"}},
			},
		},
	)

	// Should match any resource labels (or none).
	route, err := router.Resolve(context.Background(), ResolveRequest{
		Namespace:      "prod",
		ResourceLabels: map[string]string{"app": "checkout"},
	})
	if err != nil {
		t.Fatalf("resolve with nil selector: %v", err)
	}
	if route == nil || route.Chain.Name != "default-chain" {
		t.Fatalf("expected nil selector to match all resources, got %#v", route)
	}

	// Also match when no resource labels provided.
	route, err = router.Resolve(context.Background(), ResolveRequest{
		Namespace: "prod",
	})
	if err != nil {
		t.Fatalf("resolve with nil selector (no labels): %v", err)
	}
	if route == nil || route.Chain.Name != "default-chain" {
		t.Fatalf("expected nil selector to match with empty labels, got %#v", route)
	}
}

func newRouter(t *testing.T, chains ...*v1alpha1.EscalationChain) *Router {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add API scheme: %v", err)
	}
	objects := make([]client.Object, 0, len(chains))
	for _, chain := range chains {
		objects = append(objects, chain)
	}
	return NewRouter(fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build())
}
