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

	if riskSignal.Annotations[annotationNotificationAt] != "" {
		return ctrl.Result{}, nil
	}

	now := time.Now
	if r.Now != nil {
		now = r.Now
	}

	if err := r.Notifier.Notify(ctx, notifier.Message{
		Title:   fmt.Sprintf("RiskSignal detected: %s", riskSignal.Name),
		Summary: riskSignal.Status.Message,
		Body:    notificationBody(riskSignal),
		Fields:  notificationFields(riskSignal),
	}); err != nil {
		return ctrl.Result{}, err
	}

	original := riskSignal.DeepCopy()
	if riskSignal.Annotations == nil {
		riskSignal.Annotations = map[string]string{}
	}
	riskSignal.Annotations[annotationNotificationAt] = now().UTC().Format(time.RFC3339)
	riskSignal.Annotations[annotationNotificationSource] = notifierSource(riskSignal)
	if err := r.Update(ctx, &riskSignal); err != nil && !apierrors.IsConflict(err) {
		return ctrl.Result{}, err
	}

	if err := r.Get(ctx, req.NamespacedName, &riskSignal); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	setRiskSignalStatus(&riskSignal.Status, v1alpha1.PhaseNotified, original.Status.Message, riskSignal.Generation, now())
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

func notificationBody(riskSignal v1alpha1.RiskSignal) string {
	lines := []string{fmt.Sprintf("Summary: %s", riskSignal.Status.Message)}
	if riskSignal.Labels[labelRiskRule] != "" {
		lines = append(lines, fmt.Sprintf("Rule: %s", riskSignal.Labels[labelRiskRule]))
	}
	lines = append(lines, fmt.Sprintf("Target: %s", targetRefString(riskSignal.Spec.Target)))
	if strings.TrimSpace(riskSignal.Status.RCASummary) != "" {
		lines = append(lines, fmt.Sprintf("RCA Summary: %s", riskSignal.Status.RCASummary))
	}
	if strings.TrimSpace(riskSignal.Status.RCAHypothesis) != "" {
		lines = append(lines, fmt.Sprintf("RCA Hypothesis: %s", riskSignal.Status.RCAHypothesis))
	}
	for _, evidence := range riskSignal.Spec.Evidence {
		source := evidence.Source
		if source == "" {
			source = evidence.Kind
		}
		lines = append(lines, fmt.Sprintf("[%s] %s", source, evidence.Summary))
	}
	return strings.Join(lines, "\n")
}

func notificationFields(riskSignal v1alpha1.RiskSignal) map[string]any {
	fields := map[string]any{
		"namespace":  riskSignal.Namespace,
		"severity":   riskSignal.Spec.Severity,
		"signalType": riskSignal.Spec.SignalType,
		"confidence": riskSignal.Spec.Confidence,
		"target":     targetRefString(riskSignal.Spec.Target),
	}
	if riskSignal.Labels[labelRiskRule] != "" {
		fields["riskRule"] = riskSignal.Labels[labelRiskRule]
	}
	if riskSignal.Status.RCASummary != "" {
		fields["rcaSummary"] = riskSignal.Status.RCASummary
	}
	if riskSignal.Status.RCAProvider != "" {
		fields["rcaProvider"] = riskSignal.Status.RCAProvider
	}
	if source := notifierSource(riskSignal); source != "" {
		fields["origin"] = source
	}
	return fields
}

func notifierSource(riskSignal v1alpha1.RiskSignal) string {
	if riskSignal.Annotations[annotationDetectionSource] != "" {
		return riskSignal.Annotations[annotationDetectionSource]
	}
	return riskSignal.Labels[labelManagedBy]
}
