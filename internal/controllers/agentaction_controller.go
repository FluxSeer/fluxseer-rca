package controllers

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/canonicaldigest"
	"github.com/FluxSeer/fluxseer-rca/internal/escalation"
	"github.com/FluxSeer/fluxseer-rca/internal/executor"
	"github.com/FluxSeer/fluxseer-rca/internal/notifier"
)

// phaseTransitionEvent describes an AgentAction phase transition.
// Returns (eventType, reason, message) if phase changed, or (empty, empty, empty) if no change.
func phaseTransitionEvent(originalPhase, newPhase string) (string, string, string) {
	if originalPhase == newPhase {
		return "", "", ""
	}

	eventType := corev1.EventTypeNormal
	reason := ""
	message := ""

	// Map transitions to event reason and message
	transition := fmt.Sprintf("%s→%s", originalPhase, newPhase)
	switch transition {
	case "→WaitingApproval":
		reason = "ApprovalRequired"
		message = "AgentAction is waiting for approval"
	case "→Approved":
		reason = "ApprovalGranted"
		message = "AgentAction has been approved"
	case "→Rejected":
		reason = "ApprovalDenied"
		message = "AgentAction was rejected by guardrails"
		eventType = corev1.EventTypeWarning
	case "Approved→Executing", "WaitingApproval→Executing":
		reason = "ExecutionStarted"
		message = "AgentAction is now executing"
	case "WaitingApproval→Escalated":
		reason = "EscalationTriggered"
		message = "AgentAction approval timeout reached; escalating for review"
		eventType = corev1.EventTypeWarning
	case "Executing→Succeeded":
		reason = "ExecutionSucceeded"
		message = "AgentAction execution completed successfully"
	case "Executing→Failed":
		reason = "ExecutionFailed"
		message = "AgentAction execution failed"
		eventType = corev1.EventTypeWarning
	}

	if reason == "" {
		// Unknown transition; record it generically
		reason = "PhaseTransition"
		message = fmt.Sprintf("phase changed from %q to %q", originalPhase, newPhase)
	}

	return eventType, reason, message
}

// recordPhaseTransition is a nil-safe helper to emit Kubernetes Events for phase transitions.
// It checks if recorder is nil (test/unconfigured scenarios) before attempting to emit.
func recordPhaseTransition(recorder record.EventRecorder, object runtime.Object, fromPhase, toPhase string) {
	if recorder == nil || fromPhase == toPhase {
		return
	}

	eventType, reason, message := phaseTransitionEvent(fromPhase, toPhase)
	if reason != "" {
		recorder.Event(object, eventType, reason, message)
	}
}

type AgentActionReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	Executor          *executor.Router
	EscalationRouter  *escalation.Router
	PolicyPackEnabled bool
	Notifier          notifier.Notifier
	EventRecorder     record.EventRecorder
	Now               func() time.Time
}

func (r *AgentActionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var action v1alpha1.AgentAction
	if err := r.Get(ctx, req.NamespacedName, &action); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	now := time.Now
	if r.Now != nil {
		now = r.Now
	}

	if action.Status.Effectiveness != nil && action.Status.Effectiveness.Phase == v1alpha1.EffectivenessPhaseVerifying {
		return r.reconcileEffectivenessVerification(ctx, &action, now())
	}
	if action.Status.Phase == v1alpha1.PhaseSucceeded || action.Status.Phase == v1alpha1.PhaseFailed || action.Status.Phase == v1alpha1.PhaseRejected {
		return r.reconcileTerminalStateTTL(ctx, &action, now())
	}
	if action.Status.Phase == v1alpha1.PhaseExecuting && action.Status.Execution != nil && action.Status.Execution.ExecutionID != "" {
		// The dispatch identity was durably recorded before the backend call.
		// A resync or controller restart must not issue a second side effect;
		// ask an identity-aware backend to recover the external result instead.
		request := executorRequestForPersistedAction(&action)
		if r.Executor != nil {
			result, found, err := r.Executor.Resolve(ctx, request)
			if err != nil {
				return ctrl.Result{}, err
			}
			if found {
				return r.recordExecutionSuccess(ctx, &action, request, result, now())
			}
		}
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	if !isAgentActionApproved(&action) {
		return r.reconcilePending(ctx, &action, now())
	}

	executionTarget := targetToResource(action.Spec.Target)
	executionParameters := executor.StringParameters(action.Spec.Parameters)
	actionDigest := agentActionSpecDigest(&action)
	targetUID := ""
	if action.Annotations != nil {
		targetUID = strings.TrimSpace(action.Annotations[annotationTargetUID])
	}
	identity := executor.BuildExecutionIdentity(executor.IdentityInput{
		ActionRef:      action.Namespace + "/" + action.Name,
		AgentActionUID: string(action.UID),
		Generation:     action.Generation,
		ActionIndex:    0,
		Target:         executionTarget,
		TargetUID:      targetUID,
		ActionDigest:   actionDigest,
		ActionType:     action.Spec.ActionType,
		Parameters:     executionParameters,
	})
	original := action.DeepCopy()
	startedAt := metav1.NewTime(now())
	setResourceStatus(&action.Status.ResourceStatus, v1alpha1.PhaseExecuting, "approved action is executing", action.Generation, startedAt.Time)
	approval := action.Status.Approval
	approvedBy := action.Status.Approval.ApprovedBy
	source := action.Status.Approval.Source
	approvedAt := approval.ApprovedAt
	if !action.Status.Approval.Approved {
		// Manual-approval path: guardrails already evaluated this exact
		// action content (proven by the digest match in
		// isAgentActionApproved) and required human approval rather than
		// auto-approving or rejecting it. A human has since supplied
		// spec.approvedBy, so we can now treat it as approved.
		approvedBy = action.Spec.ApprovedBy
		source = "ManualApprovalConfirmed"
		approvedAt = &startedAt
	}
	action.Status.Approval = &v1alpha1.AgentActionApprovalStatus{
		Approved:           true,
		ApprovedBy:         approvedBy,
		Source:             source,
		ActionDigest:       agentActionSpecDigest(&action),
		ApprovedGeneration: action.Generation,
		ApprovedAt:         approvedAt,
		DecidedAt:          approval.DecidedAt,
		DecidedBy:          approval.DecidedBy,
		EscalatedAt:        approval.EscalatedAt,
	}
	executionRequest := executor.ExecutorRequest{
		ExecutionID:    identity.ExecutionID,
		IdempotencyKey: identity.IdempotencyKey,
		ActionDigest:   actionDigest,
		ActionType:     action.Spec.ActionType,
		ActionIndex:    0,
		Target:         executionTarget,
		TargetUID:      targetUID,
		Parameters:     executionParameters,
		ApprovedBy:     approvedBy,
		DryRunResult:   action.Spec.DryRunResult,
		RollbackPlan:   action.Spec.RollbackPlan,
		Attempt:        1,
	}
	baseline, baselineFound, err := r.Executor.CaptureBaseline(ctx, executionRequest)
	if err != nil {
		return r.failBeforeDispatch(ctx, &action, executionRequest, "BaselineCaptureFailed", err, now())
	}
	action.Status.Execution = &v1alpha1.AgentActionExecutionStatus{
		Phase:          "Executing",
		ExecutionID:    executionRequest.ExecutionID,
		IdempotencyKey: executionRequest.IdempotencyKey,
		Attempt:        int32(executionRequest.Attempt),
		StartedAt:      &startedAt,
	}
	if baselineFound {
		action.Status.Effectiveness = &v1alpha1.AgentActionEffectivenessStatus{
			Phase:    v1alpha1.EffectivenessPhaseNotVerified,
			Message:  "immutable pre-action health baseline captured before dispatch",
			Baseline: effectivenessBaselineFromExecutor(baseline, &action),
		}
	}
	if statusChangedAction(original, &action) {
		if err := r.Status().Update(ctx, &action); err != nil {
			recordStatusUpdateConflict("AgentAction", err)
			return ctrl.Result{}, err
		}
		// Emit Event only after successful status update
		recordPhaseTransition(r.EventRecorder, &action, original.Status.Phase, action.Status.Phase)
	}

	result, err := r.Executor.Execute(ctx, executionRequest)
	if err != nil {
		if err := r.Get(ctx, req.NamespacedName, &action); err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
		finishedAt := metav1.NewTime(now())
		if result.Outcome == executor.ExecutionOutcomeUnknown {
			setResourceStatus(&action.Status.ResourceStatus, v1alpha1.PhaseExecuting, "execution outcome is unknown; waiting for backend recovery", action.Generation, finishedAt.Time)
			executionStatus := action.Status.Execution
			if executionStatus == nil {
				executionStatus = &v1alpha1.AgentActionExecutionStatus{}
			}
			executionStatus.Phase = "Unknown"
			executionStatus.Outcome = string(result.Outcome)
			executionStatus.FailureReason = result.FailureReason
			if executionStatus.FailureReason == "" {
				executionStatus.FailureReason = err.Error()
			}
			executionStatus.Summary = err.Error()
			executionStatus.FinishedAt = &finishedAt
			executionStatus.Retryable = false
			action.Status.Execution = executionStatus
			if updateErr := r.Status().Update(ctx, &action); updateErr != nil {
				recordStatusUpdateConflict("AgentAction", updateErr)
				return ctrl.Result{}, updateErr
			}
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		setResourceStatus(&action.Status.ResourceStatus, v1alpha1.PhaseFailed, err.Error(), action.Generation, finishedAt.Time)
		originalExecution := action.Status.Phase
		action.Status.FinishedAt = &finishedAt
		executionStatus := action.Status.Execution
		if executionStatus == nil {
			executionStatus = &v1alpha1.AgentActionExecutionStatus{}
		}
		executionStatus.Phase = "Failed"
		executionStatus.Outcome = string(executor.ExecutionOutcomeFailed)
		executionStatus.FailureReason = err.Error()
		executionStatus.Summary = err.Error()
		executionStatus.FinishedAt = &finishedAt
		executionStatus.Retryable = false
		action.Status.Execution = executionStatus
		if updateErr := r.Status().Update(ctx, &action); updateErr != nil {
			recordStatusUpdateConflict("AgentAction", updateErr)
			return ctrl.Result{}, updateErr
		}
		// Emit Event only after successful status update
		recordPhaseTransition(r.EventRecorder, &action, originalExecution, action.Status.Phase)
		return ctrl.Result{}, err
	}

	if err := r.Get(ctx, req.NamespacedName, &action); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return r.recordExecutionSuccess(ctx, &action, executionRequest, result, now())
}

func executorRequestForPersistedAction(action *v1alpha1.AgentAction) executor.ExecutorRequest {
	request := executor.ExecutorRequest{
		ActionType:   action.Spec.ActionType,
		Target:       targetToResource(action.Spec.Target),
		Parameters:   executor.StringParameters(action.Spec.Parameters),
		ApprovedBy:   action.Spec.ApprovedBy,
		DryRunResult: action.Spec.DryRunResult,
		RollbackPlan: action.Spec.RollbackPlan,
		Attempt:      1,
	}
	if action.Status.Approval != nil {
		request.ActionDigest = action.Status.Approval.ActionDigest
		request.ApprovedBy = action.Status.Approval.ApprovedBy
	}
	if action.Status.Execution != nil {
		request.ExecutionID = action.Status.Execution.ExecutionID
		request.IdempotencyKey = action.Status.Execution.IdempotencyKey
		request.Attempt = int(action.Status.Execution.Attempt)
	}
	if action.Annotations != nil {
		request.TargetUID = strings.TrimSpace(action.Annotations[annotationTargetUID])
	}
	return request
}

func (r *AgentActionReconciler) failBeforeDispatch(ctx context.Context, action *v1alpha1.AgentAction, request executor.ExecutorRequest, reason string, dispatchErr error, now time.Time) (ctrl.Result, error) {
	finishedAt := metav1.NewTime(now)
	setResourceStatus(&action.Status.ResourceStatus, v1alpha1.PhaseFailed, dispatchErr.Error(), action.Generation, finishedAt.Time)
	action.Status.FinishedAt = &finishedAt
	action.Status.Execution = &v1alpha1.AgentActionExecutionStatus{
		Phase:          "Failed",
		Outcome:        string(executor.ExecutionOutcomeFailed),
		ExecutionID:    request.ExecutionID,
		IdempotencyKey: request.IdempotencyKey,
		Attempt:        int32(request.Attempt),
		FailureReason:  reason,
		Summary:        dispatchErr.Error(),
		FinishedAt:     &finishedAt,
		Retryable:      false,
	}
	action.Status.Effectiveness = &v1alpha1.AgentActionEffectivenessStatus{
		Phase:   v1alpha1.EffectivenessPhaseNotVerified,
		Message: "post-action verification was not started because the pre-action baseline could not be captured",
	}
	if err := r.Status().Update(ctx, action); err != nil {
		recordStatusUpdateConflict("AgentAction", err)
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, dispatchErr
}

func (r *AgentActionReconciler) recordExecutionSuccess(ctx context.Context, action *v1alpha1.AgentAction, request executor.ExecutorRequest, result executor.ExecutorResult, now time.Time) (ctrl.Result, error) {
	originalPhase := action.Status.Phase
	finishedAt := metav1.NewTime(now)
	setResourceStatus(&action.Status.ResourceStatus, v1alpha1.PhaseSucceeded, result.Summary, action.Generation, finishedAt.Time)
	action.Status.FinishedAt = &finishedAt
	executionID := result.ExecutionID
	if executionID == "" {
		executionID = request.ExecutionID
	}
	executionStatus := &v1alpha1.AgentActionExecutionStatus{
		Phase:          "Succeeded",
		Outcome:        string(result.Outcome),
		ExecutionID:    executionID,
		FailureReason:  result.FailureReason,
		Executor:       result.Executor,
		Summary:        result.Summary,
		FinishedAt:     &finishedAt,
		ExternalRef:    result.ExternalRef,
		Retryable:      result.Retryable,
		IdempotencyKey: request.IdempotencyKey,
		Attempt:        int32(request.Attempt),
	}
	if executionStatus.Outcome == "" {
		executionStatus.Outcome = string(executor.ExecutionOutcomeSucceeded)
	}
	if !result.StartedAt.IsZero() {
		startedAt := metav1.NewTime(result.StartedAt)
		executionStatus.StartedAt = &startedAt
	} else if action.Status.Execution != nil {
		executionStatus.StartedAt = action.Status.Execution.StartedAt
	}
	action.Status.Execution = executionStatus
	effectiveness := &v1alpha1.AgentActionEffectivenessStatus{
		Phase:   v1alpha1.EffectivenessPhaseNotVerified,
		Message: "post-action remediation effectiveness verification is unavailable without a pre-action baseline",
	}
	if action.Status.Effectiveness != nil && action.Status.Effectiveness.Baseline != nil {
		effectiveness = action.Status.Effectiveness
		effectiveness.Phase = v1alpha1.EffectivenessPhaseVerifying
		effectiveness.Outcome = ""
		effectiveness.Message = "execution succeeded; waiting for the target to settle before read-only verification"
		effectiveness.StartedAt = &finishedAt
		settlingUntil := metav1.NewTime(now.Add(effectivenessSettlingPeriod))
		observationUntil := metav1.NewTime(now.Add(effectivenessSettlingPeriod + effectivenessObservationWindow))
		effectiveness.SettlingUntil = &settlingUntil
		effectiveness.ObservationUntil = &observationUntil
	}
	action.Status.Effectiveness = effectiveness
	if err := r.Status().Update(ctx, action); err != nil {
		recordStatusUpdateConflict("AgentAction", err)
		return ctrl.Result{}, err
	}
	recordPhaseTransition(r.EventRecorder, action, originalPhase, action.Status.Phase)
	return ctrl.Result{}, nil
}

const (
	effectivenessSettlingPeriod         = 5 * time.Minute
	effectivenessObservationWindow      = 2 * time.Minute
	effectivenessVerificationDataSource = "kubernetes-events"
)

func effectivenessBaselineFromExecutor(baseline executor.EffectivenessBaseline, action *v1alpha1.AgentAction) *v1alpha1.EffectivenessBaseline {
	conditions := make([]v1alpha1.EffectivenessHealthCondition, 0, len(baseline.Health.Conditions))
	for _, condition := range baseline.Health.Conditions {
		conditions = append(conditions, v1alpha1.EffectivenessHealthCondition{
			Type:    condition.Type,
			Status:  condition.Status,
			Reason:  condition.Reason,
			Message: condition.Message,
		})
	}
	capturedAt := metav1.NewTime(baseline.CapturedAt)
	return &v1alpha1.EffectivenessBaseline{
		CapturedAt: &capturedAt,
		Target:     action.Spec.Target,
		TargetUID:  baseline.TargetUID,
		Health: &v1alpha1.EffectivenessHealthSnapshot{
			Generation:         baseline.Health.Generation,
			ObservedGeneration: baseline.Health.ObservedGeneration,
			DesiredReplicas:    baseline.Health.DesiredReplicas,
			UpdatedReplicas:    baseline.Health.UpdatedReplicas,
			AvailableReplicas:  baseline.Health.AvailableReplicas,
			ReadyReplicas:      baseline.Health.ReadyReplicas,
			Conditions:         conditions,
		},
		Digest: baseline.Digest,
	}
}

func (r *AgentActionReconciler) reconcileEffectivenessVerification(ctx context.Context, action *v1alpha1.AgentAction, now time.Time) (ctrl.Result, error) {
	effectiveness := action.Status.Effectiveness
	if effectiveness == nil || effectiveness.Baseline == nil || action.Status.Execution == nil || action.Status.Execution.Outcome != string(executor.ExecutionOutcomeSucceeded) {
		return ctrl.Result{}, nil
	}
	if effectiveness.SettlingUntil != nil && now.Before(effectiveness.SettlingUntil.Time) {
		return ctrl.Result{RequeueAfter: effectiveness.SettlingUntil.Sub(now)}, nil
	}

	if effectiveness.VerificationRef == nil {
		verification, err := r.createEffectivenessVerification(ctx, action)
		if err != nil {
			return ctrl.Result{}, err
		}
		action.Status.Effectiveness.VerificationRef = &v1alpha1.NamespacedObjectReference{
			Name:      verification.Name,
			Namespace: verification.Namespace,
		}
		action.Status.Effectiveness.Message = "read-only verification investigation created; waiting for post-action evidence"
		if err := r.Status().Update(ctx, action); err != nil {
			recordStatusUpdateConflict("AgentAction", err)
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	if effectiveness.ObservationUntil != nil && now.Before(effectiveness.ObservationUntil.Time) {
		return ctrl.Result{RequeueAfter: effectiveness.ObservationUntil.Sub(now)}, nil
	}
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

func (r *AgentActionReconciler) createEffectivenessVerification(ctx context.Context, action *v1alpha1.AgentAction) (*v1alpha1.InvestigationRequest, error) {
	if r.Scheme == nil {
		return nil, fmt.Errorf("AgentAction reconciler requires a scheme to create verification InvestigationRequest")
	}
	name := effectivenessVerificationName(action)
	verification := &v1alpha1.InvestigationRequest{}
	verification.Name = name
	verification.Namespace = action.Namespace
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, verification, func() error {
		if err := controllerutil.SetControllerReference(action, verification, r.Scheme); err != nil {
			return err
		}
		if verification.Labels == nil {
			verification.Labels = map[string]string{}
		}
		if verification.Annotations == nil {
			verification.Annotations = map[string]string{}
		}
		verification.Labels[labelManagedBy] = "agentaction-controller"
		verification.Annotations[annotationLineageSource] = action.Namespace + "/" + action.Name
		verification.Annotations[annotationLineageSourceKind] = "AgentAction"
		verification.Annotations[annotationLineageSourceAPI] = v1alpha1.SchemeGroupVersion.String()
		verification.Annotations[annotationLineageSourceUID] = string(action.UID)
		verification.Annotations[annotationTargetUID] = action.Status.Effectiveness.Baseline.TargetUID
		verification.Annotations[annotationInvestigationDepth] = "0"
		verification.Spec.Target = action.Spec.Target
		verification.Spec.Purpose = v1alpha1.InvestigationPurposeEffectivenessVerification
		verification.Spec.Correlation = &v1alpha1.InvestigationCorrelation{
			AgentActionRef: v1alpha1.NamespacedObjectReference{Name: action.Name, Namespace: action.Namespace},
			ExecutionID:    action.Status.Execution.ExecutionID,
			BaselineDigest: action.Status.Effectiveness.Baseline.Digest,
		}
		verification.Spec.TimeRange = v1alpha1.InvestigationTimeRange{Lookback: metav1.Duration{Duration: effectivenessObservationWindow}}
		verification.Spec.Question = fmt.Sprintf("Verify whether %s recovered after execution %s", targetRefString(action.Spec.Target), action.Status.Execution.ExecutionID)
		verification.Spec.Queries = []v1alpha1.InvestigationQuery{{
			Name:          "post-action-workload-events",
			DatasourceRef: v1alpha1.LocalObjectReference{Name: effectivenessVerificationDataSource},
			QueryType:     "event",
			Reasons:       []string{"BackOff", "Unhealthy", "Failed", "FailedCreate", "ProgressDeadlineExceeded"},
		}}
		verification.Spec.Mode = v1alpha1.InvestigationModeReadOnly
		verification.Spec.CreateRiskSignal = false
		return nil
	})
	if err != nil {
		return nil, err
	}
	return verification, nil
}

func effectivenessVerificationName(action *v1alpha1.AgentAction) string {
	digest := canonicaldigest.String(canonicaldigest.RCAJSONV1, struct {
		ActionUID   string
		ExecutionID string
	}{ActionUID: string(action.UID), ExecutionID: action.Status.Execution.ExecutionID})
	suffix := strings.TrimPrefix(digest, canonicaldigest.AlgorithmSHA256+":")
	if len(suffix) > 16 {
		suffix = suffix[:16]
	}
	prefix := action.Name
	maxPrefix := 63 - len("-verify-") - len(suffix)
	if len(prefix) > maxPrefix {
		prefix = prefix[:maxPrefix]
	}
	return prefix + "-verify-" + suffix
}

// reconcilePending handles an AgentAction that isAgentActionApproved has
// rejected. It distinguishes two cases that must not be treated the same
// way. An action that guardrails never validly evaluated (no approval record
// yet, or the digest no longer matches the current spec because the action
// was created directly or edited after approval was requested) is reset to a
// fresh Pending/WaitingApproval state, same as before this method existed.
// An action that guardrails did evaluate and correctly left waiting on a
// human must NOT be reset the same way: isAgentActionApproved only honors
// spec.approvedBy once status.approval.source is still
// "ManualApprovalRequired", so overwriting that source to "Pending" on every
// reconcile -- which the previous, single-branch version of this code did --
// would permanently strip the action's ability to ever be approved as soon
// as anything (a resync, the timeout check below) reconciles it again before
// a human acts. That case instead only advances the approval timeout.
func (r *AgentActionReconciler) reconcilePending(ctx context.Context, action *v1alpha1.AgentAction, now time.Time) (ctrl.Result, error) {
	digest := agentActionSpecDigest(action)
	if action.Status.Approval == nil || action.Status.Approval.ActionDigest != digest {
		original := action.DeepCopy()
		setResourceStatus(&action.Status.ResourceStatus, v1alpha1.PhaseWaitingApproval, "waiting for human approval", action.Generation, now)
		action.Status.Approval = &v1alpha1.AgentActionApprovalStatus{
			Approved:           false,
			Source:             "Pending",
			ActionDigest:       digest,
			ApprovedGeneration: action.Generation,
		}
		if statusChangedAction(original, action) {
			if err := r.Status().Update(ctx, action); err != nil {
				recordStatusUpdateConflict("AgentAction", err)
				return ctrl.Result{}, err
			}
			// Emit Event only after successful status update
			recordPhaseTransition(r.EventRecorder, action, original.Status.Phase, action.Status.Phase)
		}
		return ctrl.Result{}, nil
	}

	return r.reconcileApprovalTimeout(ctx, action, now)
}

// reconcileApprovalTimeout escalates an AgentAction that has been validly
// WaitingApproval for longer than spec.approvalTimeoutSeconds. It anchors
// the wait window to status.updatedAt -- the timestamp already recorded when
// the action first entered WaitingApproval -- and, as long as the deadline
// has not passed, returns a RequeueAfter without writing any status at all.
// Writing status on every pass (even "no change" writes that only refresh a
// timestamp) would slide status.updatedAt forward each time and the timeout
// would never trip.
func (r *AgentActionReconciler) reconcileApprovalTimeout(ctx context.Context, action *v1alpha1.AgentAction, now time.Time) (ctrl.Result, error) {
	if action.Status.Phase == v1alpha1.PhaseEscalated || action.Spec.ApprovalTimeoutSeconds <= 0 || action.Status.UpdatedAt.IsZero() {
		return ctrl.Result{}, nil
	}

	deadline := action.Status.UpdatedAt.Add(time.Duration(action.Spec.ApprovalTimeoutSeconds) * time.Second)
	if now.Before(deadline) {
		return ctrl.Result{RequeueAfter: deadline.Sub(now)}, nil
	}

	var route *escalation.Route
	if r.Notifier != nil && r.EscalationRouter != nil {
		resolved, err := r.EscalationRouter.Resolve(ctx, escalation.ResolveRequest{
			Namespace:          action.Namespace,
			EscalationChainRef: action.Annotations[annotationEscalationChainRef],
			ResourceLabels:     action.Labels,
		})
		if err != nil {
			return ctrl.Result{}, err
		}
		route = resolved
	}

	if r.Notifier != nil {
		// Notify before persisting Escalated, mirroring
		// RiskSignalNotificationReconciler's notify-then-mark-done ordering:
		// a failed delivery leaves the phase as WaitingApproval so the next
		// reconcile retries the notification instead of losing it silently.
		attemptAt := metav1.NewTime(now)
		attemptCount := int32(1)
		if action.Status.Notification != nil {
			attemptCount = action.Status.Notification.RetryCount + 1
		}
		action.Status.Notification = &v1alpha1.AgentActionNotificationStatus{
			LastAttemptAt: &attemptAt,
			RetryCount:    attemptCount,
		}
		if err := r.Status().Update(ctx, action); err != nil {
			recordStatusUpdateConflict("AgentAction", err)
			return ctrl.Result{}, err
		}

		fields := map[string]any{
			"namespace":  action.Namespace,
			"name":       action.Name,
			"actionType": action.Spec.ActionType,
		}
		if route != nil {
			fields["escalationChain"] = route.Reference.Name
			fields["escalationChainVersion"] = route.Reference.Version
			fields["escalationStageCount"] = len(route.Chain.Spec.Stages)
		}
		notifyErr := r.Notifier.Notify(ctx, notifier.Message{
			Title:   "FluxSeer RCA approval escalation",
			Summary: fmt.Sprintf("%s exceeded its %ds approval timeout", targetRefString(action.Spec.Target), action.Spec.ApprovalTimeoutSeconds),
			Body:    action.Status.Message,
			Fields:  fields,
		})
		if notifyErr != nil {
			action.Status.Notification.LastError = notifyErr.Error()
			if updateErr := r.Status().Update(ctx, action); updateErr != nil {
				recordStatusUpdateConflict("AgentAction", updateErr)
				return ctrl.Result{}, updateErr
			}
			if r.EventRecorder != nil {
				r.EventRecorder.Eventf(
					action,
					corev1.EventTypeWarning,
					"NotificationRetryFailed",
					"escalation notification attempt %d failed: %v",
					attemptCount,
					notifyErr,
				)
			}
			return ctrl.Result{}, notifyErr
		}
		action.Status.Notification.LastError = ""
	}

	original := action.DeepCopy()
	setResourceStatus(&action.Status.ResourceStatus, v1alpha1.PhaseEscalated, "approval timeout exceeded, escalation notified", action.Generation, now)
	escalatedAt := metav1.NewTime(now)
	action.Status.Approval.EscalatedAt = &escalatedAt
	if statusChangedAction(original, action) {
		if err := r.Status().Update(ctx, action); err != nil {
			recordStatusUpdateConflict("AgentAction", err)
			return ctrl.Result{}, err
		}
		// Emit Event only after successful status update
		recordPhaseTransition(r.EventRecorder, action, original.Status.Phase, action.Status.Phase)
	}
	return ctrl.Result{}, nil
}

// isAgentActionApproved reports whether an AgentAction is safe to execute.
// It never trusts spec.approvedBy on its own: that field is a plain,
// user-writable CR spec field, so treating it as sufficient proof of
// approval would let anyone with RBAC write access to AgentAction bypass
// guardrails.Engine entirely (create or edit an AgentAction directly,
// skipping RemediationPlan, with spec.approvedBy pre-filled). Approval must
// instead trace back to status.approval, which only RemediationPlanReconciler
// writes after actually evaluating the action against guardrails -- and the
// digest comparison ensures the action content guardrails evaluated is the
// same content about to execute, so editing spec after approval (or
// approving) invalidates it rather than silently executing changed
// parameters under a stale approval.
func isAgentActionApproved(action *v1alpha1.AgentAction) bool {
	approval := action.Status.Approval
	if approval == nil {
		return false
	}
	if approval.ActionDigest != agentActionSpecDigest(action) {
		return false
	}
	if approval.Approved {
		return true
	}
	// Manual approval is only honored once guardrails has already evaluated
	// this exact action and required human approval -- not for actions
	// guardrails rejected, and not for actions that never went through
	// guardrails at all (e.g. an AgentAction created directly, bypassing
	// RemediationPlan).
	return approval.Source == "ManualApprovalRequired" && action.Spec.ApprovedBy != ""
}

// agentActionSpecDigest fingerprints the action content that guardrails
// evaluated (target, action type, parameters, dry-run result, rollback
// plan). ApprovedBy is deliberately excluded: it identifies who approved the
// action, not what the action does, and a human is expected to fill it in
// after the digest was first computed during a ManualApprovalRequired
// evaluation, so including it would make the digest never match again once
// approved.
func agentActionSpecDigest(action *v1alpha1.AgentAction) string {
	if action == nil {
		return ""
	}
	spec := action.Spec
	spec.ApprovedBy = ""
	return canonicaldigest.String(canonicaldigest.RCAJSONV1, spec)
}

// reconcileTerminalStateTTL handles TTL-based deletion for terminal AgentActions.
// Terminal phases are Succeeded, Failed, and Rejected.
// Returns a requeue result if the action should be kept (not yet expired),
// or deletes the action and returns empty result if it has expired.
func (r *AgentActionReconciler) reconcileTerminalStateTTL(ctx context.Context, action *v1alpha1.AgentAction, now time.Time) (ctrl.Result, error) {
	// TTL is disabled if not set or zero
	if action.Spec.TTLSeconds == 0 {
		return ctrl.Result{}, nil
	}

	// Check for retain annotation - if present, never delete
	if action.Annotations != nil {
		if _, ok := action.Annotations["fluxseer-rca.aiops.platform/retain"]; ok {
			return ctrl.Result{}, nil
		}
	}

	// Need finishedAt to calculate expiration. If missing, don't delete (conservative).
	if action.Status.FinishedAt == nil {
		return ctrl.Result{}, nil
	}

	// Calculate when this action should expire
	expireTime := action.Status.FinishedAt.Add(time.Duration(action.Spec.TTLSeconds) * time.Second)

	if now.After(expireTime) {
		// TTL expired, delete the action
		if err := r.Delete(ctx, action); err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
		return ctrl.Result{}, nil
	}

	// Not yet expired, requeue after the remaining time
	remainingTTL := expireTime.Sub(now)
	return ctrl.Result{RequeueAfter: remainingTTL}, nil
}

func (r *AgentActionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.AgentAction{}).
		Owns(&v1alpha1.InvestigationRequest{})
	if r.PolicyPackEnabled {
		builder = builder.Watches(&v1alpha1.EscalationChain{}, handler.EnqueueRequestsFromMapFunc(r.mapPendingEscalationActions))
	}
	return builder.Complete(r)
}

// mapPendingEscalationActions requeues waiting actions affected by a chain change.
func (r *AgentActionReconciler) mapPendingEscalationActions(ctx context.Context, chain client.Object) []reconcile.Request {
	var actions v1alpha1.AgentActionList
	if err := r.List(ctx, &actions, client.InNamespace(chain.GetNamespace())); err != nil {
		return nil
	}
	requests := make([]reconcile.Request, 0, len(actions.Items))
	for i := range actions.Items {
		action := &actions.Items[i]
		if action.Status.Phase != v1alpha1.PhaseWaitingApproval {
			continue
		}
		if ref := action.Annotations[annotationEscalationChainRef]; ref != "" && ref != chain.GetName() {
			continue
		}
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(action)})
	}
	return requests
}
