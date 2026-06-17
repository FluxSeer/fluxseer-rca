package guardrails

import (
	"fmt"
	"slices"

	"fluxagent/internal/domain"
)

type Policy struct {
	AllowedActionTypes       []string
	ProtectedNamespaces      []string
	AutoApproveMaxSeverity   domain.Severity
	RequireApprovalAtOrAbove domain.Severity
}

type ReviewInput struct {
	Resource    domain.ResourceRef
	Reasoning   domain.ReasoningOutput
	Environment string
}

type Engine struct {
	Policy Policy
}

func NewEngine(policy Policy) *Engine {
	return &Engine{Policy: policy}
}

func (e *Engine) Evaluate(input ReviewInput) domain.ApprovalDecision {
	action := input.Reasoning.Remediation.ActionType
	if !slices.Contains(e.Policy.AllowedActionTypes, action) {
		return domain.ApprovalDecision{
			Action: domain.ApprovalReject,
			Reason: fmt.Sprintf("action type %q is not allowlisted", action),
		}
	}

	if slices.Contains(e.Policy.ProtectedNamespaces, input.Resource.Namespace) && input.Reasoning.Severity == domain.SeverityHigh {
		return domain.ApprovalDecision{
			Action:       domain.ApprovalManual,
			Reason:       "high-severity action against protected namespace requires human approval",
			DryRunResult: "dry-run: target exists, policy passes, approval required",
		}
	}

	autoApproveMax := e.Policy.AutoApproveMaxSeverity
	if autoApproveMax == "" {
		autoApproveMax = domain.SeverityLow
	}

	approvalMin := e.Policy.RequireApprovalAtOrAbove
	if approvalMin == "" {
		approvalMin = domain.SeverityMedium
	}

	if severityRank(input.Reasoning.Severity) <= severityRank(autoApproveMax) {
		return domain.ApprovalDecision{
			Action:       domain.ApprovalAuto,
			ApprovedBy:   "fluxagent-policy-engine",
			Reason:       "low-risk recommendation auto approved",
			DryRunResult: "dry-run: safe to execute",
		}
	}

	if severityRank(input.Reasoning.Severity) >= severityRank(approvalMin) && input.Reasoning.Severity != domain.SeverityUnsafe {
		return domain.ApprovalDecision{
			Action:       domain.ApprovalManual,
			Reason:       "severity threshold requires human approval",
			DryRunResult: "dry-run: target exists, rollback plan present",
		}
	}

	return domain.ApprovalDecision{
		Action: domain.ApprovalReject,
		Reason: "unsafe or unsupported severity",
	}
}

func severityRank(severity domain.Severity) int {
	switch severity {
	case domain.SeverityLow:
		return 1
	case domain.SeverityMedium:
		return 2
	case domain.SeverityHigh:
		return 3
	case domain.SeverityUnsafe:
		return 4
	default:
		return 99
	}
}
