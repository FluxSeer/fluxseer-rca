package controlplane

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/audit"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
	"github.com/FluxSeer/fluxseer-rca/internal/executor"
	"github.com/FluxSeer/fluxseer-rca/internal/guardrails"
	"github.com/FluxSeer/fluxseer-rca/internal/ingestion"
	"github.com/FluxSeer/fluxseer-rca/internal/reasoning"
)

type Orchestrator struct {
	Ingestion  *ingestion.Pipeline
	Reasoning  *reasoning.Engine
	Guardrails *guardrails.Engine
	Executor   *executor.Router
	Audit      *audit.Store
	Now        func() time.Time
}

type Result struct {
	Ingestion       domain.IngestionOutput    `json:"ingestion"`
	Reasoning       domain.ReasoningOutput    `json:"reasoning"`
	RiskSignal      *v1alpha1.RiskSignal      `json:"riskSignal"`
	RemediationPlan *v1alpha1.RemediationPlan `json:"remediationPlan"`
	AgentAction     *v1alpha1.AgentAction     `json:"agentAction"`
	Approval        domain.ApprovalDecision   `json:"approval"`
	Execution       *domain.ExecutionResult   `json:"execution,omitempty"`
	AuditEntries    []string                  `json:"auditEntries"`
}

func (o *Orchestrator) Run(ctx context.Context, req ingestion.Request) (Result, error) {
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}

	ingested, err := o.Ingestion.Run(ctx, req)
	if err != nil {
		return Result{}, err
	}
	o.Audit.Append("ingestion completed")

	reasoned, err := o.Reasoning.Analyze(ctx, ingested)
	if err != nil {
		return Result{}, err
	}
	o.Audit.Append("reasoning completed")

	riskSignal := buildRiskSignal(ingested, reasoned, now())
	plan := buildRemediationPlan(ingested, reasoned, now())
	decision := o.Guardrails.Evaluate(guardrails.ReviewInput{
		Resource:    ingested.Context.Resource,
		Reasoning:   reasoned,
		Environment: ingested.Context.Metadata["environment"],
	})
	o.Audit.Append(fmt.Sprintf("guardrail decision: %s", decision.Action))

	action := buildAgentAction(ingested, reasoned, decision, now())
	switch decision.Action {
	case domain.ApprovalAuto:
		action.Status.Phase = v1alpha1.PhaseApproved
	case domain.ApprovalManual:
		action.Status.Phase = v1alpha1.PhaseWaitingApproval
	case domain.ApprovalReject:
		action.Status.Phase = v1alpha1.PhaseRejected
		action.Status.Message = decision.Reason
	}

	result := Result{
		Ingestion:       ingested,
		Reasoning:       reasoned,
		RiskSignal:      riskSignal,
		RemediationPlan: plan,
		AgentAction:     action,
		Approval:        decision,
	}

	if decision.Action != domain.ApprovalAuto {
		result.AuditEntries = o.Audit.Entries()
		return result, nil
	}

	executed, err := o.Executor.Execute(ctx, executor.ApprovedAction{
		Resource:     ingested.Context.Resource,
		ActionType:   reasoned.Remediation.ActionType,
		Parameters:   reasoned.Remediation.Parameters,
		ApprovedBy:   decision.ApprovedBy,
		DryRunResult: decision.DryRunResult,
		RollbackPlan: reasoned.Remediation.RollbackPlan,
	})
	if err != nil {
		return Result{}, err
	}
	action.Status.Phase = v1alpha1.PhaseSucceeded
	action.Status.Message = executed.Summary
	o.Audit.Append("execution completed")
	result.Execution = &executed
	result.AuditEntries = o.Audit.Entries()
	return result, nil
}

func buildRiskSignal(input domain.IngestionOutput, reasoning domain.ReasoningOutput, now time.Time) *v1alpha1.RiskSignal {
	evidence := make([]v1alpha1.EvidenceRef, 0, len(input.Evidence.References))
	for _, ref := range input.Evidence.References {
		evidence = append(evidence, v1alpha1.EvidenceRef{
			Kind:    ref.Kind,
			Summary: ref.Summary,
			Link:    ref.Link,
		})
	}

	return &v1alpha1.RiskSignal{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
			Kind:       "RiskSignal",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      input.Context.Resource.Name + "-risk",
			Namespace: input.Context.Resource.Namespace,
		},
		Spec: v1alpha1.RiskSignalSpec{
			Target: v1alpha1.TargetRef{
				Cluster:    input.Context.Resource.Cluster,
				Namespace:  input.Context.Resource.Namespace,
				Kind:       input.Context.Resource.Kind,
				Name:       input.Context.Resource.Name,
				APIVersion: input.Context.Resource.APIVersion,
				Service:    input.Context.Resource.Service,
			},
			SignalType: "incident",
			ActionType: reasoning.Remediation.ActionType,
			Severity:   string(reasoning.Severity),
			Confidence: reasoning.Confidence.Score,
			DryRun:     true,
			TTLSeconds: 1800,
			Evidence:   evidence,
			Parameters: reasoning.Remediation.Parameters,
		},
		Status: v1alpha1.RiskSignalStatus{
			ResourceStatus: v1alpha1.ResourceStatus{
				Phase:     v1alpha1.PhaseRecommendation,
				Message:   reasoning.RiskSummary,
				UpdatedAt: metav1.NewTime(now),
			},
			RCASummary:    reasoning.RiskSummary,
			RCAHypothesis: reasoning.RCA.Hypothesis,
			RCAProvider:   reasoning.Provider,
		},
	}
}

func buildRemediationPlan(input domain.IngestionOutput, reasoning domain.ReasoningOutput, now time.Time) *v1alpha1.RemediationPlan {
	return &v1alpha1.RemediationPlan{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
			Kind:       "RemediationPlan",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      input.Context.Resource.Name + "-plan",
			Namespace: input.Context.Resource.Namespace,
		},
		Spec: v1alpha1.RemediationPlanSpec{
			Target: v1alpha1.TargetRef{
				Cluster:    input.Context.Resource.Cluster,
				Namespace:  input.Context.Resource.Namespace,
				Kind:       input.Context.Resource.Kind,
				Name:       input.Context.Resource.Name,
				APIVersion: input.Context.Resource.APIVersion,
				Service:    input.Context.Resource.Service,
			},
			RecommendedBy: reasoning.Provider,
			Severity:      string(reasoning.Severity),
			Confidence:    reasoning.Confidence.Score,
			DryRun:        true,
			TTLSeconds:    1800,
			Summary:       reasoning.Remediation.Description,
			RollbackPlan:  reasoning.Remediation.RollbackPlan,
			References:    append(append([]string{}, reasoning.RunbookRefs...), reasoning.ServiceDocs...),
			Steps: []v1alpha1.RemediationStep{
				{
					Name:        "stabilize-workload",
					ActionType:  reasoning.Remediation.ActionType,
					Description: reasoning.Remediation.Description,
					Parameters:  reasoning.Remediation.Parameters,
				},
			},
		},
		Status: v1alpha1.ResourceStatus{
			Phase:     v1alpha1.PhaseReadyForApproval,
			Message:   "plan created from reasoning output",
			UpdatedAt: metav1.NewTime(now),
		},
	}
}

func buildAgentAction(input domain.IngestionOutput, reasoning domain.ReasoningOutput, decision domain.ApprovalDecision, now time.Time) *v1alpha1.AgentAction {
	return &v1alpha1.AgentAction{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
			Kind:       "AgentAction",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      input.Context.Resource.Name + "-action",
			Namespace: input.Context.Resource.Namespace,
		},
		Spec: v1alpha1.AgentActionSpec{
			Target: v1alpha1.TargetRef{
				Cluster:    input.Context.Resource.Cluster,
				Namespace:  input.Context.Resource.Namespace,
				Kind:       input.Context.Resource.Kind,
				Name:       input.Context.Resource.Name,
				APIVersion: input.Context.Resource.APIVersion,
				Service:    input.Context.Resource.Service,
			},
			ActionType:   reasoning.Remediation.ActionType,
			Parameters:   reasoning.Remediation.Parameters,
			ApprovedBy:   decision.ApprovedBy,
			DryRunResult: decision.DryRunResult,
			TTLSeconds:   1800,
			RollbackPlan: reasoning.Remediation.RollbackPlan,
		},
		Status: v1alpha1.AgentActionStatus{
			ResourceStatus: v1alpha1.ResourceStatus{
				Phase:     v1alpha1.PhasePolicyChecked,
				Message:   decision.Reason,
				UpdatedAt: metav1.NewTime(now),
			},
			Approval: &v1alpha1.AgentActionApprovalStatus{
				Approved:           decision.Action == domain.ApprovalAuto,
				ApprovedBy:         decision.ApprovedBy,
				Source:             "ControlPlane",
				ApprovedGeneration: 1,
				ApprovedAt:         approvalTime(decision, now),
			},
			DryRunResult: &v1alpha1.AgentActionDryRunStatus{
				Result:     decision.DryRunResult,
				RecordedAt: ptrTime(now),
			},
		},
	}
}

func approvalTime(decision domain.ApprovalDecision, now time.Time) *metav1.Time {
	if decision.Action != domain.ApprovalAuto {
		return nil
	}
	return ptrTime(now)
}

func ptrTime(t time.Time) *metav1.Time {
	mt := metav1.NewTime(t)
	return &mt
}
