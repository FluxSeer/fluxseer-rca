package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/FluxSeer/fluxseer-rca/internal/canonicaldigest"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
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
	backend, err := r.executorFor(request.ActionType)
	if err != nil {
		return ExecutorResult{}, err
	}
	return backend.Execute(ctx, request)
}

// Resolve asks a backend to recover an uncertain external side effect. A
// backend that cannot resolve an action returns found=false; the controller
// must not fall back to a second Execute call.
func (r *Router) Resolve(ctx context.Context, request ExecutorRequest) (ExecutorResult, bool, error) {
	backend, err := r.executorFor(request.ActionType)
	if err != nil {
		return ExecutorResult{}, false, err
	}
	resolver, ok := backend.(ExecutionResolver)
	if !ok {
		return ExecutorResult{}, false, nil
	}
	return resolver.Resolve(ctx, request)
}

func (r *Router) CaptureBaseline(ctx context.Context, request ExecutorRequest) (EffectivenessBaseline, bool, error) {
	backend, err := r.executorFor(request.ActionType)
	if err != nil {
		return EffectivenessBaseline{}, false, err
	}
	capturer, ok := backend.(BaselineCapturer)
	if !ok {
		return EffectivenessBaseline{}, false, nil
	}
	return capturer.CaptureBaseline(ctx, request)
}

func (r *Router) ObserveHealth(ctx context.Context, request ExecutorRequest) (HealthSnapshot, bool, error) {
	backend, err := r.executorFor(request.ActionType)
	if err != nil {
		return HealthSnapshot{}, false, err
	}
	observer, ok := backend.(HealthObserver)
	if !ok {
		return HealthSnapshot{}, false, nil
	}
	return observer.ObserveHealth(ctx, request)
}

func (r *Router) executorFor(actionType string) (Executor, error) {
	switch {
	case strings.HasPrefix(actionType, "kubernetes."):
		return r.Kubernetes, nil
	case strings.HasPrefix(actionType, "gitops."):
		return r.GitOps, nil
	case strings.HasPrefix(actionType, "runbook."):
		return r.Runbook, nil
	case strings.HasPrefix(actionType, "notification."):
		return r.Notify, nil
	default:
		return nil, fmt.Errorf("no executor registered for action type %q", actionType)
	}
}

type KubernetesExecutor struct {
	Client client.Client
	Now    func() time.Time
}

const (
	KubernetesRolloutRestartAction  = "kubernetes.rolloutRestart"
	kubernetesRestartedByAnnotation = "fluxseer.io/restarted-by"
	KubernetesExecutionIDAnnotation = "fluxseer.io/execution-id"
	kubernetesExecutionIDAnnotation = KubernetesExecutionIDAnnotation
)

func (e KubernetesExecutor) Name() string {
	return "kubernetes-executor"
}

func (e KubernetesExecutor) Execute(ctx context.Context, action ExecutorRequest) (ExecutorResult, error) {
	now := time.Now
	if e.Now != nil {
		now = e.Now
	}
	startedAt := now()

	if e.Client == nil {
		return e.simulatedResult(action, startedAt, now()), nil
	}
	if err := validateKubernetesRestartRequest(action); err != nil {
		return kubernetesFailureResult(e.Name(), action, startedAt, now(), "ValidationFailed", err), err
	}

	var deployment appsv1.Deployment
	if err := e.Client.Get(ctx, types.NamespacedName{Namespace: action.Target.Namespace, Name: action.Target.Name}, &deployment); err != nil {
		result := kubernetesFailureResult(e.Name(), action, startedAt, now(), "TargetNotFound", err)
		return result, fmt.Errorf("get deployment %s/%s: %w", action.Target.Namespace, action.Target.Name, err)
	}
	if string(deployment.UID) != action.TargetUID {
		err := fmt.Errorf("target UID mismatch: expected %q, got %q", action.TargetUID, deployment.UID)
		return kubernetesFailureResult(e.Name(), action, startedAt, now(), "TargetUIDMismatch", err), err
	}
	if deployment.Spec.Template.Annotations != nil && deployment.Spec.Template.Annotations[kubernetesExecutionIDAnnotation] == action.ExecutionID {
		return kubernetesSuccessResult(e.Name(), action, startedAt, now(), &deployment, "rollout restart already recorded for execution identity"), nil
	}

	original := deployment.DeepCopy()
	if deployment.Spec.Template.Annotations == nil {
		deployment.Spec.Template.Annotations = map[string]string{}
	}
	deployment.Spec.Template.Annotations[kubernetesRestartedByAnnotation] = "fluxseer-rca"
	deployment.Spec.Template.Annotations[kubernetesExecutionIDAnnotation] = action.ExecutionID
	if err := e.Client.Patch(ctx, &deployment, client.MergeFrom(original)); err != nil {
		reason := "KubernetesPatchFailed"
		outcome := ExecutionOutcomeFailed
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			reason = "TimeoutAfterDispatch"
			outcome = ExecutionOutcomeUnknown
		}
		result := kubernetesFailureResult(e.Name(), action, startedAt, now(), reason, err)
		result.Outcome = outcome
		return result, fmt.Errorf("patch deployment %s/%s: %w", action.Target.Namespace, action.Target.Name, err)
	}

	return kubernetesSuccessResult(e.Name(), action, startedAt, now(), &deployment, "rollout restart annotation applied"), nil
}

func (e KubernetesExecutor) CaptureBaseline(ctx context.Context, action ExecutorRequest) (EffectivenessBaseline, bool, error) {
	if e.Client == nil {
		return EffectivenessBaseline{}, false, nil
	}
	if err := validateKubernetesRestartRequest(action); err != nil {
		return EffectivenessBaseline{}, false, err
	}

	var deployment appsv1.Deployment
	if err := e.Client.Get(ctx, types.NamespacedName{Namespace: action.Target.Namespace, Name: action.Target.Name}, &deployment); err != nil {
		return EffectivenessBaseline{}, false, fmt.Errorf("get baseline deployment %s/%s: %w", action.Target.Namespace, action.Target.Name, err)
	}
	if string(deployment.UID) != action.TargetUID {
		return EffectivenessBaseline{}, false, fmt.Errorf("baseline target UID mismatch: expected %q, got %q", action.TargetUID, deployment.UID)
	}

	snapshot := deploymentHealthSnapshot(&deployment)
	baseline := EffectivenessBaseline{
		CapturedAt: e.currentTime(),
		Target:     action.Target,
		TargetUID:  string(deployment.UID),
		Health:     snapshot,
	}
	baseline.Digest = canonicaldigest.String(ExecutorIdentityJSONV1, struct {
		Target    domain.ResourceRef
		TargetUID string
		Health    HealthSnapshot
	}{Target: baseline.Target, TargetUID: baseline.TargetUID, Health: baseline.Health})
	return baseline, true, nil
}

func (e KubernetesExecutor) ObserveHealth(ctx context.Context, action ExecutorRequest) (HealthSnapshot, bool, error) {
	if e.Client == nil {
		return HealthSnapshot{}, false, nil
	}
	if err := validateKubernetesRestartRequest(action); err != nil {
		return HealthSnapshot{}, false, err
	}
	var deployment appsv1.Deployment
	if err := e.Client.Get(ctx, types.NamespacedName{Namespace: action.Target.Namespace, Name: action.Target.Name}, &deployment); err != nil {
		return HealthSnapshot{}, false, fmt.Errorf("get observed deployment %s/%s: %w", action.Target.Namespace, action.Target.Name, err)
	}
	if string(deployment.UID) != action.TargetUID {
		return HealthSnapshot{}, false, fmt.Errorf("observed target UID mismatch: expected %q, got %q", action.TargetUID, deployment.UID)
	}
	return deploymentHealthSnapshot(&deployment), true, nil
}

func (e KubernetesExecutor) currentTime() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func deploymentHealthSnapshot(deployment *appsv1.Deployment) HealthSnapshot {
	desired := int32(1)
	if deployment.Spec.Replicas != nil {
		desired = *deployment.Spec.Replicas
	}
	conditions := make([]HealthCondition, 0, len(deployment.Status.Conditions))
	for _, condition := range deployment.Status.Conditions {
		conditions = append(conditions, HealthCondition{
			Type:    string(condition.Type),
			Status:  string(condition.Status),
			Reason:  condition.Reason,
			Message: condition.Message,
		})
	}
	return HealthSnapshot{
		Generation:         deployment.Generation,
		ObservedGeneration: deployment.Status.ObservedGeneration,
		DesiredReplicas:    desired,
		UpdatedReplicas:    deployment.Status.UpdatedReplicas,
		AvailableReplicas:  deployment.Status.AvailableReplicas,
		ReadyReplicas:      deployment.Status.ReadyReplicas,
		Conditions:         conditions,
	}
}

// Resolve implements read-after-write recovery for an uncertain Kubernetes
// mutation. It never patches the target and never falls back to Execute.
func (e KubernetesExecutor) Resolve(ctx context.Context, action ExecutorRequest) (ExecutorResult, bool, error) {
	if e.Client == nil {
		return ExecutorResult{}, false, nil
	}
	if action.ActionType != KubernetesRolloutRestartAction {
		return ExecutorResult{}, false, nil
	}
	if err := validateKubernetesRestartRequest(action); err != nil {
		return kubernetesFailureResult(e.Name(), action, time.Time{}, time.Time{}, "ValidationFailed", err), false, err
	}

	var deployment appsv1.Deployment
	if err := e.Client.Get(ctx, types.NamespacedName{Namespace: action.Target.Namespace, Name: action.Target.Name}, &deployment); err != nil {
		if apierrors.IsNotFound(err) {
			return ExecutorResult{}, false, nil
		}
		return ExecutorResult{}, false, err
	}
	if string(deployment.UID) != action.TargetUID {
		err := fmt.Errorf("target UID mismatch: expected %q, got %q", action.TargetUID, deployment.UID)
		return kubernetesFailureResult(e.Name(), action, time.Time{}, time.Time{}, "TargetUIDMismatch", err), false, err
	}
	if deployment.Spec.Template.Annotations == nil || deployment.Spec.Template.Annotations[kubernetesExecutionIDAnnotation] != action.ExecutionID {
		return ExecutorResult{}, false, nil
	}
	return kubernetesSuccessResult(e.Name(), action, time.Time{}, time.Now(), &deployment, "rollout restart recovered by read-after-write"), true, nil
}

func (e KubernetesExecutor) simulatedResult(action ExecutorRequest, startedAt, finishedAt time.Time) ExecutorResult {

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
	}
}

func validateKubernetesRestartRequest(action ExecutorRequest) error {
	if action.ActionType != KubernetesRolloutRestartAction {
		return fmt.Errorf("unsupported Kubernetes action type %q", action.ActionType)
	}
	if action.Target.Kind != "Deployment" {
		return fmt.Errorf("rollout restart requires Deployment target, got %q", action.Target.Kind)
	}
	if action.Target.APIVersion != "" && action.Target.APIVersion != "apps/v1" {
		return fmt.Errorf("rollout restart requires apps/v1 target, got %q", action.Target.APIVersion)
	}
	if action.Target.Namespace == "" || action.Target.Name == "" {
		return fmt.Errorf("rollout restart requires namespace and name")
	}
	if action.TargetUID == "" {
		return fmt.Errorf("rollout restart requires target UID")
	}
	if action.ExecutionID == "" || action.IdempotencyKey == "" {
		return fmt.Errorf("rollout restart requires execution and idempotency identity")
	}
	return nil
}

func kubernetesSuccessResult(executorName string, action ExecutorRequest, startedAt, finishedAt time.Time, deployment *appsv1.Deployment, summary string) ExecutorResult {
	result := ExecutorResult{
		ExecutionID: action.ExecutionID,
		Outcome:     ExecutionOutcomeSucceeded,
		Executor:    executorName,
		Status:      "succeeded",
		Summary:     summary,
		ExternalRef: fmt.Sprintf("apps/v1/Deployment/%s/%s", deployment.Namespace, deployment.Name),
		FinishedAt:  finishedAt,
		Outputs: map[string]string{
			"target":      deployment.Name,
			"targetUID":   string(deployment.UID),
			"executionID": action.ExecutionID,
		},
	}
	if !startedAt.IsZero() {
		result.StartedAt = startedAt
	}
	return result
}

func kubernetesFailureResult(executorName string, action ExecutorRequest, startedAt, finishedAt time.Time, reason string, err error) ExecutorResult {
	result := ExecutorResult{
		ExecutionID:   action.ExecutionID,
		Outcome:       ExecutionOutcomeFailed,
		FailureReason: reason,
		Executor:      executorName,
		Status:        "failed",
		Summary:       err.Error(),
		FinishedAt:    finishedAt,
		Retryable:     false,
	}
	if !startedAt.IsZero() {
		result.StartedAt = startedAt
	}
	return result
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
