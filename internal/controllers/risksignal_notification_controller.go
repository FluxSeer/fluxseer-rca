package controllers

import (
	"context"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"fluxagent/api/v1alpha1"
	"fluxagent/internal/notifier"
)

const notificationAnnotation = "fluxagent.aiops.platform/notified-at"

type RiskSignalNotificationReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Notifier notifier.Notifier
	Now      func() time.Time
}

func (r *RiskSignalNotificationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var riskSignal v1alpha1.RiskSignal
	if err := r.Get(ctx, req.NamespacedName, &riskSignal); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if riskSignal.Annotations[notificationAnnotation] != "" {
		return ctrl.Result{}, nil
	}

	now := time.Now
	if r.Now != nil {
		now = r.Now
	}

	body := make([]string, 0, len(riskSignal.Spec.Evidence))
	for _, evidence := range riskSignal.Spec.Evidence {
		body = append(body, fmt.Sprintf("[%s] %s", evidence.Source, evidence.Summary))
	}

	if err := r.Notifier.Notify(ctx, notifier.Message{
		Title:   fmt.Sprintf("RiskSignal detected: %s", riskSignal.Name),
		Summary: riskSignal.Status.Message,
		Body:    strings.Join(body, "\n"),
		Fields: map[string]any{
			"namespace":  riskSignal.Namespace,
			"severity":   riskSignal.Spec.Severity,
			"signalType": riskSignal.Spec.SignalType,
		},
	}); err != nil {
		return ctrl.Result{}, err
	}

	original := riskSignal.DeepCopy()
	if riskSignal.Annotations == nil {
		riskSignal.Annotations = map[string]string{}
	}
	riskSignal.Annotations[notificationAnnotation] = now().UTC().Format(time.RFC3339)
	if err := r.Update(ctx, &riskSignal); err != nil && !apierrors.IsConflict(err) {
		return ctrl.Result{}, err
	}

	if err := r.Get(ctx, req.NamespacedName, &riskSignal); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	setResourceStatus(&riskSignal.Status, v1alpha1.PhaseNotified, original.Status.Message, riskSignal.Generation, now())
	if statusChangedRiskSignal(original, &riskSignal) {
		if err := r.Status().Update(ctx, &riskSignal); err != nil && !apierrors.IsConflict(err) {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *RiskSignalNotificationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.RiskSignal{}).
		Complete(r)
}
