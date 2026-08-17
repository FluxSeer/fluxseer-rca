package guardrails

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
)

func TestPolicyEngineUsesResourceScopeBeforeHigherPriorityClusterPolicy(t *testing.T) {
	engine := newPolicyEngine(t,
		&v1alpha1.ApprovalPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-default", Namespace: "policy-system"},
			Spec: v1alpha1.ApprovalPolicySpec{
				Scope:    "Cluster",
				Enabled:  true,
				Priority: 100,
				SeverityRules: []v1alpha1.SeverityRule{{
					MinSeverity: v1alpha1.SeverityLow,
					MaxSeverity: v1alpha1.SeverityCritical,
					Action:      v1alpha1.ApprovalActionAuto,
				}},
			},
		},
		&v1alpha1.ApprovalPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "resource-policy", Namespace: "prod"},
			Spec: v1alpha1.ApprovalPolicySpec{
				Enabled:  true,
				Priority: 1,
				ResourceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
					"app": "checkout",
				}},
				ActionTypeRules: []v1alpha1.ActionTypeRule{{
					ActionType: "kubernetes.scaleDeployment",
					Action:     v1alpha1.ApprovalActionManual,
				}},
			},
		},
	)

	evaluation, err := engine.Evaluate(context.Background(), policyInput("prod", "kubernetes.scaleDeployment", domain.SeverityLow, map[string]string{"app": "checkout"}))
	if err != nil {
		t.Fatalf("evaluate policy: %v", err)
	}
	if evaluation.Decision.Action != domain.ApprovalManual {
		t.Fatalf("expected resource policy to require approval, got %s", evaluation.Decision.Action)
	}
	if evaluation.Policy == nil || evaluation.Policy.MatchLevel != PolicyMatchResource {
		t.Fatalf("expected resource policy provenance, got %#v", evaluation.Policy)
	}
}

func TestPolicyEngineUsesNamespaceSelectorBeforeClusterPolicy(t *testing.T) {
	engine := newPolicyEngine(t,
		&v1alpha1.ApprovalPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-default", Namespace: "policy-system"},
			Spec: v1alpha1.ApprovalPolicySpec{
				Scope:   "Cluster",
				Enabled: true,
				ActionTypeRules: []v1alpha1.ActionTypeRule{{
					ActionType: "runbook.triggerWorkflow",
					Action:     v1alpha1.ApprovalActionAuto,
				}},
			},
		},
		&v1alpha1.ApprovalPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "payments-policy", Namespace: "policy-system"},
			Spec: v1alpha1.ApprovalPolicySpec{
				Enabled: true,
				NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
					"team": "payments",
				}},
				ActionTypeRules: []v1alpha1.ActionTypeRule{{
					ActionType: "runbook.triggerWorkflow",
					Action:     v1alpha1.ApprovalActionReject,
				}},
			},
		},
	)

	evaluation, err := engine.Evaluate(context.Background(), policyInputWithNamespaceLabels("prod", "runbook.triggerWorkflow", domain.SeverityMedium, nil, map[string]string{"team": "payments"}))
	if err != nil {
		t.Fatalf("evaluate policy: %v", err)
	}
	if evaluation.Decision.Action != domain.ApprovalReject {
		t.Fatalf("expected namespace policy rejection, got %s", evaluation.Decision.Action)
	}
	if evaluation.Policy == nil || evaluation.Policy.MatchLevel != PolicyMatchNamespace {
		t.Fatalf("expected namespace policy provenance, got %#v", evaluation.Policy)
	}
}

func TestPolicyEngineAppliesSeverityTimeoutAndAuditVersion(t *testing.T) {
	policy := &v1alpha1.ApprovalPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "severity-policy",
			Namespace:       "prod",
			ResourceVersion: "17",
			Generation:      3,
		},
		Spec: v1alpha1.ApprovalPolicySpec{
			Enabled:                true,
			DefaultApprovalTimeout: 300,
			SeverityRules: []v1alpha1.SeverityRule{{
				MinSeverity: v1alpha1.SeverityHigh,
				MaxSeverity: v1alpha1.SeverityCritical,
				Action:      v1alpha1.ApprovalActionManual,
				TimeoutSec:  90,
			}},
		},
	}
	engine := newPolicyEngine(t, policy)

	evaluation, err := engine.Evaluate(context.Background(), policyInput("prod", "kubernetes.rolloutPause", domain.SeverityHigh, nil))
	if err != nil {
		t.Fatalf("evaluate policy: %v", err)
	}
	if evaluation.ApprovalTimeoutSeconds != 90 {
		t.Fatalf("expected severity timeout 90, got %d", evaluation.ApprovalTimeoutSeconds)
	}
	if evaluation.Policy == nil || evaluation.Policy.Version != "17" {
		t.Fatalf("expected resource version audit provenance, got %#v", evaluation.Policy)
	}
}

func TestPolicyEngineFallsBackWhenNoPolicyMatches(t *testing.T) {
	legacy := NewEngine(Policy{
		AllowedActionTypes:       []string{"kubernetes.scaleDeployment"},
		AutoApproveMaxSeverity:   domain.SeverityLow,
		RequireApprovalAtOrAbove: domain.SeverityMedium,
	})
	engine := NewPolicyEngine(newPolicyClient(t), legacy, true)

	evaluation, err := engine.Evaluate(context.Background(), policyInput("dev", "kubernetes.scaleDeployment", domain.SeverityLow, nil))
	if err != nil {
		t.Fatalf("evaluate fallback: %v", err)
	}
	if evaluation.Source != PolicySourceLegacy || evaluation.Policy != nil {
		t.Fatalf("expected legacy provenance, got source=%s policy=%#v", evaluation.Source, evaluation.Policy)
	}
	if evaluation.Decision.Action != domain.ApprovalAuto {
		t.Fatalf("expected legacy auto approval, got %s", evaluation.Decision.Action)
	}
}

func TestPolicyEngineSkipsDisabledPolicies(t *testing.T) {
	legacy := NewEngine(Policy{
		AllowedActionTypes:       []string{"kubernetes.scaleDeployment"},
		AutoApproveMaxSeverity:   domain.SeverityLow,
		RequireApprovalAtOrAbove: domain.SeverityMedium,
	})
	engine := newPolicyEngineWithLegacy(t, legacy, &v1alpha1.ApprovalPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "disabled", Namespace: "dev"},
		Spec: v1alpha1.ApprovalPolicySpec{
			Enabled: false,
			ActionTypeRules: []v1alpha1.ActionTypeRule{{
				ActionType: "kubernetes.scaleDeployment",
				Action:     v1alpha1.ApprovalActionReject,
			}},
		},
	})

	evaluation, err := engine.Evaluate(context.Background(), policyInput("dev", "kubernetes.scaleDeployment", domain.SeverityLow, nil))
	if err != nil {
		t.Fatalf("evaluate disabled policy: %v", err)
	}
	if evaluation.Source != PolicySourceLegacy || evaluation.Decision.Action != domain.ApprovalAuto {
		t.Fatalf("expected disabled policy fallback, got %#v", evaluation)
	}
}

func TestPolicyEngineNilResourceSelectorDoesNotMatch(t *testing.T) {
	// Contract test: a nil ResourceSelector does not match when requesting a different namespace.
	// The policy may still match via cluster scope or namespace selector.
	engine := newPolicyEngine(t,
		&v1alpha1.ApprovalPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "action-only", Namespace: "prod"},
			Spec: v1alpha1.ApprovalPolicySpec{
				Enabled: true,
				// No ResourceSelector (nil), no Scope (defaults to namespace-scoped)
				ActionTypeRules: []v1alpha1.ActionTypeRule{{
					ActionType: "kubernetes.scaleDeployment",
					Action:     v1alpha1.ApprovalActionManual,
				}},
			},
		},
	)

	// Even though action type matches, nil ResourceSelector + different namespace means policy does not apply.
	evaluation, err := engine.Evaluate(context.Background(), policyInput("dev", "kubernetes.scaleDeployment", domain.SeverityLow, map[string]string{"app": "checkout"}))
	if err != nil {
		t.Fatalf("evaluate nil selector: %v", err)
	}
	// Should fallback to legacy since policy doesn't match
	if evaluation.Source != PolicySourceLegacy {
		t.Fatalf("expected fallback to legacy with nil ResourceSelector on different namespace, got %#v", evaluation)
	}
}

func TestPolicyEngineClusterScopeMatchesWithoutSelectors(t *testing.T) {
	// Contract test: Cluster scope matches without ResourceSelector.
	engine := newPolicyEngine(t,
		&v1alpha1.ApprovalPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-default", Namespace: "policy-system"},
			Spec: v1alpha1.ApprovalPolicySpec{
				Scope:   "Cluster",
				Enabled: true,
				// No ResourceSelector (nil)
				ActionTypeRules: []v1alpha1.ActionTypeRule{{
					ActionType: "kubernetes.scaleDeployment",
					Action:     v1alpha1.ApprovalActionAuto,
				}},
			},
		},
	)

	// Cluster scope should match even without ResourceSelector.
	evaluation, err := engine.Evaluate(context.Background(), policyInput("prod", "kubernetes.scaleDeployment", domain.SeverityLow, nil))
	if err != nil {
		t.Fatalf("evaluate cluster scope: %v", err)
	}
	if evaluation.Decision.Action != domain.ApprovalAuto || evaluation.Policy == nil || evaluation.Policy.MatchLevel != PolicyMatchCluster {
		t.Fatalf("expected cluster scope match, got %#v", evaluation)
	}
}

func newPolicyEngine(t *testing.T, policies ...*v1alpha1.ApprovalPolicy) *PolicyEngine {
	t.Helper()
	return NewPolicyEngine(newPolicyClient(t, policies...), nil, true)
}

func newPolicyEngineWithLegacy(t *testing.T, legacy *Engine, policies ...*v1alpha1.ApprovalPolicy) *PolicyEngine {
	t.Helper()
	return NewPolicyEngine(newPolicyClient(t, policies...), legacy, true)
}

func newPolicyClient(t *testing.T, policies ...*v1alpha1.ApprovalPolicy) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add API scheme: %v", err)
	}
	objects := make([]client.Object, 0, len(policies))
	for _, policy := range policies {
		objects = append(objects, policy)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func policyInput(namespace, actionType string, severity domain.Severity, resourceLabels map[string]string) PolicyReviewInput {
	return policyInputWithNamespaceLabels(namespace, actionType, severity, resourceLabels, nil)
}

func policyInputWithNamespaceLabels(namespace, actionType string, severity domain.Severity, resourceLabels, namespaceLabels map[string]string) PolicyReviewInput {
	return PolicyReviewInput{
		ReviewInput: ReviewInput{
			Resource: domain.ResourceRef{Namespace: namespace, Name: "target", Kind: "Deployment"},
			Reasoning: domain.ReasoningOutput{
				Severity:    severity,
				Remediation: domain.Remediation{ActionType: actionType},
			},
		},
		ResourceLabels:  resourceLabels,
		NamespaceLabels: namespaceLabels,
	}
}
