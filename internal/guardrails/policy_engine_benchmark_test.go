package guardrails

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
)

func BenchmarkPolicyEngineEvaluate(b *testing.B) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		b.Fatalf("add API scheme: %v", err)
	}
	policy := &v1alpha1.ApprovalPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-policy", Namespace: "prod", ResourceVersion: "17"},
		Spec: v1alpha1.ApprovalPolicySpec{
			Enabled: true,
			ResourceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
				"app": "payments",
			}},
			ActionTypeRules: []v1alpha1.ActionTypeRule{{
				ActionType: "kubernetes.scaleDeployment",
				Action:     v1alpha1.ApprovalActionManual,
			}},
		},
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(policy).Build()
	engine := NewPolicyEngine(reader, nil, true)
	input := PolicyReviewInput{
		ReviewInput: ReviewInput{
			Resource: domain.ResourceRef{Namespace: "prod", Kind: "Deployment", Name: "payments"},
			Reasoning: domain.ReasoningOutput{
				Severity:    domain.SeverityMedium,
				Remediation: domain.Remediation{ActionType: "kubernetes.scaleDeployment"},
			},
		},
		ResourceLabels: map[string]string{"app": "payments"},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := engine.Evaluate(context.Background(), input); err != nil {
			b.Fatalf("evaluate policy: %v", err)
		}
	}
}
