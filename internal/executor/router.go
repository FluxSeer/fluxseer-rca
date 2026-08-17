package executor

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type Executor interface {
	Name() string
	Execute(ctx context.Context, request ExecutorRequest) (ExecutorResult, error)
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

func (r *Router) Execute(ctx context.Context, request ExecutorRequest) (ExecutorResult, error) {
	switch {
	case strings.HasPrefix(request.ActionType, "kubernetes."):
		return r.Kubernetes.Execute(ctx, request)
	case strings.HasPrefix(request.ActionType, "gitops."):
		return r.GitOps.Execute(ctx, request)
	case strings.HasPrefix(request.ActionType, "runbook."):
		return r.Runbook.Execute(ctx, request)
	case strings.HasPrefix(request.ActionType, "notification."):
		return r.Notify.Execute(ctx, request)
	default:
		return ExecutorResult{}, fmt.Errorf("no executor registered for action type %q", request.ActionType)
	}
}

type KubernetesExecutor struct {
	Now func() time.Time
}

func (e KubernetesExecutor) Name() string {
	return "kubernetes-executor"
}

func (e KubernetesExecutor) Execute(_ context.Context, action ExecutorRequest) (ExecutorResult, error) {
	now := time.Now
	if e.Now != nil {
		now = e.Now
	}
	startedAt := now()
	finishedAt := now()

	summary := fmt.Sprintf(
		"Simulated %s on %s/%s approved by %s",
		action.ActionType,
		action.Target.Namespace,
		action.Target.Name,
		action.ApprovedBy,
	)

	return ExecutorResult{
		ExecutionID: action.ExecutionID,
		Outcome:     ExecutionOutcomeSucceeded,
		Executor:    e.Name(),
		Status:      "succeeded",
		Summary:     summary,
		Outputs:     map[string]string{"target": action.Target.Name, "dryRun": action.DryRunResult},
		StartedAt:   startedAt,
		FinishedAt:  finishedAt,
	}, nil
}

type GitOpsExecutor struct {
	Now func() time.Time
}

func (e GitOpsExecutor) Name() string {
	return "gitops-executor"
}

func (e GitOpsExecutor) Execute(_ context.Context, action ExecutorRequest) (ExecutorResult, error) {
	now := time.Now
	if e.Now != nil {
		now = e.Now
	}
	startedAt := now()
	finishedAt := now()

	return ExecutorResult{
		ExecutionID: action.ExecutionID,
		Outcome:     ExecutionOutcomeSucceeded,
		Executor:    e.Name(),
		Status:      "succeeded",
		Summary:     fmt.Sprintf("Simulated GitOps change for %s", action.Target.Name),
		Outputs:     map[string]string{"branch": "github.com/FluxSeer/fluxseer-rca/remediation", "actionType": action.ActionType},
		StartedAt:   startedAt,
		FinishedAt:  finishedAt,
	}, nil
}

type RunbookExecutor struct {
	Now func() time.Time
}

func (e RunbookExecutor) Name() string {
	return "runbook-executor"
}

func (e RunbookExecutor) Execute(_ context.Context, action ExecutorRequest) (ExecutorResult, error) {
	now := time.Now
	if e.Now != nil {
		now = e.Now
	}
	startedAt := now()
	finishedAt := now()

	return ExecutorResult{
		ExecutionID: action.ExecutionID,
		Outcome:     ExecutionOutcomeSucceeded,
		Executor:    e.Name(),
		Status:      "succeeded",
		Summary:     fmt.Sprintf("Simulated runbook execution for %s", action.Target.Service),
		Outputs:     map[string]string{"workflow": "incident-stabilization", "actionType": action.ActionType},
		StartedAt:   startedAt,
		FinishedAt:  finishedAt,
	}, nil
}
