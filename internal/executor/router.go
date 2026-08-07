package executor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/FluxSeer/fluxseer-rca/internal/domain"
)

type ApprovedAction struct {
	Resource     domain.ResourceRef
	ActionType   string
	Parameters   map[string]string
	ApprovedBy   string
	DryRunResult string
	RollbackPlan []string
}

type Executor interface {
	Name() string
	Execute(ctx context.Context, action ApprovedAction) (domain.ExecutionResult, error)
}

type Router struct {
	Kubernetes Executor
	GitOps     Executor
	Runbook    Executor
	Notify     Executor
	Now        func() time.Time
}

func NewRouter(kubernetes Executor, gitops Executor, runbook Executor, notify Executor) *Router {
	return &Router{
		Kubernetes: kubernetes,
		GitOps:     gitops,
		Runbook:    runbook,
		Notify:     notify,
		Now:        time.Now,
	}
}

func (r *Router) Execute(ctx context.Context, action ApprovedAction) (domain.ExecutionResult, error) {
	switch {
	case strings.HasPrefix(action.ActionType, "kubernetes."):
		return r.Kubernetes.Execute(ctx, action)
	case strings.HasPrefix(action.ActionType, "gitops."):
		return r.GitOps.Execute(ctx, action)
	case strings.HasPrefix(action.ActionType, "runbook."):
		return r.Runbook.Execute(ctx, action)
	case strings.HasPrefix(action.ActionType, "notification."):
		return r.Notify.Execute(ctx, action)
	default:
		return domain.ExecutionResult{}, fmt.Errorf("no executor registered for action type %q", action.ActionType)
	}
}

type KubernetesExecutor struct {
	Now func() time.Time
}

func (e KubernetesExecutor) Name() string {
	return "kubernetes-executor"
}

func (e KubernetesExecutor) Execute(_ context.Context, action ApprovedAction) (domain.ExecutionResult, error) {
	now := time.Now
	if e.Now != nil {
		now = e.Now
	}

	summary := fmt.Sprintf(
		"Simulated %s on %s/%s approved by %s",
		action.ActionType,
		action.Resource.Namespace,
		action.Resource.Name,
		action.ApprovedBy,
	)

	return domain.ExecutionResult{
		Executor:   e.Name(),
		Status:     "succeeded",
		Summary:    summary,
		Outputs:    map[string]string{"target": action.Resource.Name, "dryRun": action.DryRunResult},
		FinishedAt: now(),
	}, nil
}

type GitOpsExecutor struct {
	Now func() time.Time
}

func (e GitOpsExecutor) Name() string {
	return "gitops-executor"
}

func (e GitOpsExecutor) Execute(_ context.Context, action ApprovedAction) (domain.ExecutionResult, error) {
	now := time.Now
	if e.Now != nil {
		now = e.Now
	}

	return domain.ExecutionResult{
		Executor:   e.Name(),
		Status:     "succeeded",
		Summary:    fmt.Sprintf("Simulated GitOps change for %s", action.Resource.Name),
		Outputs:    map[string]string{"branch": "github.com/FluxSeer/fluxseer-rca/remediation", "actionType": action.ActionType},
		FinishedAt: now(),
	}, nil
}

type RunbookExecutor struct {
	Now func() time.Time
}

func (e RunbookExecutor) Name() string {
	return "runbook-executor"
}

func (e RunbookExecutor) Execute(_ context.Context, action ApprovedAction) (domain.ExecutionResult, error) {
	now := time.Now
	if e.Now != nil {
		now = e.Now
	}

	return domain.ExecutionResult{
		Executor:   e.Name(),
		Status:     "succeeded",
		Summary:    fmt.Sprintf("Simulated runbook execution for %s", action.Resource.Service),
		Outputs:    map[string]string{"workflow": "incident-stabilization", "actionType": action.ActionType},
		FinishedAt: now(),
	}, nil
}
