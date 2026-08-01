package controllers

import (
	"context"
	"fmt"
	"reflect"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"fluxagent/api/v1alpha1"
)

type RiskSignalReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	Enabled bool
	Now     func() time.Time
}

func (r *RiskSignalReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var riskSignal v1alpha1.RiskSignal
	if err := r.Get(ctx, req.NamespacedName, &riskSignal); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	now := time.Now
	if r.Now != nil {
		now = r.Now
	}

	ttlResult, finished, err := r.handleTTL(ctx, &riskSignal, now())
	if err != nil || finished {
		return ttlResult, err
	}
	if !r.Enabled {
		return ttlResult, nil
	}
	if hasUnverifiedInvestigationProjection(&riskSignal) {
		return ttlResult, nil
	}

	plan := &v1alpha1.RemediationPlan{}
	plan.Name = riskSignal.Name + "-plan"
	plan.Namespace = riskSignal.Namespace
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, plan, func() error {
		if err := controllerutil.SetControllerReference(&riskSignal, plan, r.Scheme); err != nil {
			return err
		}

		plan.Spec.Target = riskSignal.Spec.Target
		plan.Spec.RecommendedBy = "risk-signal-controller"
		plan.Spec.Severity = riskSignal.Spec.Severity
		plan.Spec.Confidence = riskSignal.Spec.Confidence
		plan.Spec.DryRun = riskSignal.Spec.DryRun
		plan.Spec.TTLSeconds = riskSignal.Spec.TTLSeconds
		plan.Spec.Summary = fmt.Sprintf("Derived remediation plan for %s", riskSignal.Spec.ActionType)
		plan.Spec.RollbackPlan = defaultRollbackPlan(riskSignal.Spec.ActionType)
		plan.Spec.References = evidenceSummaries(riskSignal.Spec.Evidence)
		plan.Spec.Steps = []v1alpha1.RemediationStep{
			{
				Name:        "prepare-action",
				ActionType:  riskSignal.Spec.ActionType,
				Description: fmt.Sprintf("Execute %s on target resource", riskSignal.Spec.ActionType),
				Parameters:  riskSignal.Spec.Parameters,
			},
		}
		return nil
	})
	if err != nil {
		return ctrl.Result{}, err
	}

	original := riskSignal.DeepCopy()
	setRiskSignalStatus(
		&riskSignal.Status,
		v1alpha1.PhaseReadyForApproval,
		"remediation plan materialized",
		riskSignal.Generation,
		now(),
	)
	if statusChangedRiskSignal(original, &riskSignal) {
		if err := r.Status().Update(ctx, &riskSignal); err != nil && !recordStatusUpdateConflict("RiskSignal", err) {
			return ctrl.Result{}, err
		}
	}

	return ttlResult, nil
}

func hasUnverifiedInvestigationProjection(riskSignal *v1alpha1.RiskSignal) bool {
	if riskSignal.Spec.InvestigationRef == nil {
		return false
	}
	for _, condition := range riskSignal.Status.Conditions {
		if condition.Type == conditionRCAReady && condition.Status == "False" {
			return true
		}
		if condition.Type == conditionRCAReady && condition.Status == "True" {
			return false
		}
	}
	switch riskSignal.Status.Phase {
	case "", v1alpha1.InvestigationOutcomeInconclusive, v1alpha1.InvestigationOutcomeNoIssueFound, v1alpha1.InvestigationOutcomeUnknown:
		return true
	default:
		return false
	}
}

func (r *RiskSignalReconciler) SetupWithManager(mgr ctrl.Manager) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		Named("risksignal-remediation").
		For(&v1alpha1.RiskSignal{})
	if r.Enabled {
		builder = builder.Owns(&v1alpha1.RemediationPlan{})
	}
	return builder.Complete(r)
}

func evidenceSummaries(evidence []v1alpha1.EvidenceRef) []string {
	out := make([]string, 0, len(evidence))
	for _, item := range evidence {
		if item.Link != "" {
			out = append(out, item.Link)
			continue
		}
		out = append(out, item.Summary)
	}
	return out
}

func statusChangedRiskSignal(before, after *v1alpha1.RiskSignal) bool {
	return !reflect.DeepEqual(before.Status, after.Status)
}

func (r *RiskSignalReconciler) handleTTL(ctx context.Context, riskSignal *v1alpha1.RiskSignal, now time.Time) (ctrl.Result, bool, error) {
	if riskSignal.Spec.TTLSeconds <= 0 || !riskSignal.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, false, nil
	}

	expiresAt := riskSignal.CreationTimestamp.Time.Add(time.Duration(riskSignal.Spec.TTLSeconds) * time.Second)
	if riskSignal.CreationTimestamp.IsZero() {
		expiresAt = now.Add(time.Duration(riskSignal.Spec.TTLSeconds) * time.Second)
	}
	if !now.Before(expiresAt) {
		if err := r.Delete(ctx, riskSignal); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, true, err
		}
		return ctrl.Result{}, true, nil
	}

	return ctrl.Result{RequeueAfter: expiresAt.Sub(now)}, false, nil
}
