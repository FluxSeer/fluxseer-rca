package controllers

import (
	"context"
	"fmt"
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
	if !r.Enabled {
		return ctrl.Result{}, nil
	}

	var riskSignal v1alpha1.RiskSignal
	if err := r.Get(ctx, req.NamespacedName, &riskSignal); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	now := time.Now
	if r.Now != nil {
		now = r.Now
	}

	plan := &v1alpha1.RemediationPlan{}
	plan.Name = riskSignal.Name + "-plan"
	plan.Namespace = riskSignal.Namespace
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, plan, func() error {
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
	setResourceStatus(
		&riskSignal.Status,
		v1alpha1.PhaseReadyForApproval,
		"remediation plan materialized",
		riskSignal.Generation,
		now(),
	)
	if statusChangedRiskSignal(original, &riskSignal) {
		if err := r.Status().Update(ctx, &riskSignal); err != nil && !apierrors.IsConflict(err) {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *RiskSignalReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.RiskSignal{}).
		Owns(&v1alpha1.RemediationPlan{}).
		Complete(r)
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
	return before.Status != after.Status
}
