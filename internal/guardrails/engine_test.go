package guardrails

import (
	"testing"

	"fluxagent/internal/domain"
)

func TestEvaluateAutoApproveLowSeverity(t *testing.T) {
	engine := NewEngine(Policy{
		AllowedActionTypes:       []string{"kubernetes.scaleDeployment"},
		AutoApproveMaxSeverity:   domain.SeverityLow,
		RequireApprovalAtOrAbove: domain.SeverityMedium,
	})

	decision := engine.Evaluate(ReviewInput{
		Resource: domain.ResourceRef{Namespace: "dev"},
		Reasoning: domain.ReasoningOutput{
			Severity: domain.SeverityLow,
			Remediation: domain.Remediation{
				ActionType: "kubernetes.scaleDeployment",
			},
		},
	})

	if decision.Action != domain.ApprovalAuto {
		t.Fatalf("expected auto approval, got %s", decision.Action)
	}
}

func TestEvaluateRequiresApprovalForProtectedHighSeverity(t *testing.T) {
	engine := NewEngine(Policy{
		AllowedActionTypes:       []string{"kubernetes.rolloutPause"},
		ProtectedNamespaces:      []string{"prod"},
		AutoApproveMaxSeverity:   domain.SeverityLow,
		RequireApprovalAtOrAbove: domain.SeverityMedium,
	})

	decision := engine.Evaluate(ReviewInput{
		Resource: domain.ResourceRef{Namespace: "prod"},
		Reasoning: domain.ReasoningOutput{
			Severity: domain.SeverityHigh,
			Remediation: domain.Remediation{
				ActionType: "kubernetes.rolloutPause",
			},
		},
	})

	if decision.Action != domain.ApprovalManual {
		t.Fatalf("expected manual approval, got %s", decision.Action)
	}
}

func TestEvaluateRejectsUnknownAction(t *testing.T) {
	engine := NewEngine(Policy{
		AllowedActionTypes:       []string{"kubernetes.scaleDeployment"},
		AutoApproveMaxSeverity:   domain.SeverityLow,
		RequireApprovalAtOrAbove: domain.SeverityMedium,
	})

	decision := engine.Evaluate(ReviewInput{
		Resource: domain.ResourceRef{Namespace: "dev"},
		Reasoning: domain.ReasoningOutput{
			Severity: domain.SeverityLow,
			Remediation: domain.Remediation{
				ActionType: "gitops.deleteMainBranch",
			},
		},
	})

	if decision.Action != domain.ApprovalReject {
		t.Fatalf("expected rejection, got %s", decision.Action)
	}
}
