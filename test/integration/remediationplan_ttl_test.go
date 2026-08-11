package integration

import (
	"context"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/controllers"
)

func TestRemediationPlanTTLDisabledDoesNotDeleteTerminalPlan(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	finishedAt := metav1.NewTime(now.Add(-10 * time.Minute))
	plan := newTerminalRemediationPlan(v1alpha1.PhaseRejected, 0, finishedAt)
	client := newRemediationPlanTTLClient(t, plan)

	result, err := reconcileRemediationPlanTTL(client, now, plan)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if result != (ctrl.Result{}) {
		t.Fatalf("expected no requeue with disabled TTL, got %#v", result)
	}
	assertRemediationPlanExists(t, client, plan)
}

func TestRemediationPlanTTLRespectRetainAnnotation(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	finishedAt := metav1.NewTime(now.Add(-2 * time.Minute))
	plan := newTerminalRemediationPlan(v1alpha1.PhaseRejected, 60, finishedAt)
	plan.Annotations = map[string]string{
		"fluxseer-rca.aiops.platform/retain": "true",
	}
	client := newRemediationPlanTTLClient(t, plan)

	result, err := reconcileRemediationPlanTTL(client, now, plan)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if result != (ctrl.Result{}) {
		t.Fatalf("expected no requeue with retain annotation, got %#v", result)
	}
	assertRemediationPlanExists(t, client, plan)
}

func TestRemediationPlanTTLDoesNotDeleteWhenActionsNotTerminal(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	plan := newTerminalRemediationPlan(v1alpha1.PhaseApproved, 60, metav1.Time{})
	action := newAgentAction(v1alpha1.PhaseExecuting, 60, metav1.Time{})
	action.Name = plan.Name + "-action"
	action.Namespace = plan.Namespace
	client := newRemediationPlanTTLClient(t, plan, action)

	result, err := reconcileRemediationPlanTTL(client, now, plan)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if result != (ctrl.Result{}) {
		t.Fatalf("expected no TTL requeue for non-terminal plan, got %#v", result)
	}
	assertRemediationPlanExists(t, client, plan)
}

func TestRemediationPlanTTLRequeuesFromFinishedAt(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	finishedAt := metav1.NewTime(now.Add(-30 * time.Second))
	plan := newTerminalRemediationPlan(v1alpha1.PhaseRejected, 60, finishedAt)
	client := newRemediationPlanTTLClient(t, plan)

	result, err := reconcileRemediationPlanTTL(client, now, plan)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if result.RequeueAfter != 30*time.Second {
		t.Fatalf("expected requeue after 30s from finishedAt, got %s", result.RequeueAfter)
	}
	assertRemediationPlanExists(t, client, plan)
}

func TestRemediationPlanTTLDeletesExpiredRejectedPlan(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	finishedAt := metav1.NewTime(now.Add(-61 * time.Second))
	plan := newTerminalRemediationPlan(v1alpha1.PhaseRejected, 60, finishedAt)
	client := newRemediationPlanTTLClient(t, plan)

	result, err := reconcileRemediationPlanTTL(client, now, plan)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if result != (ctrl.Result{}) {
		t.Fatalf("expected no requeue after deletion, got %#v", result)
	}
	assertRemediationPlanDeleted(t, client, plan)
}

func TestRemediationPlanTTLDeletesExpiredPlanWithTerminalActions(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	finishedAt := metav1.NewTime(now.Add(-2 * time.Minute))
	plan := newTerminalRemediationPlan(v1alpha1.PhaseSucceeded, 60, finishedAt)
	action := newAgentAction(v1alpha1.PhaseSucceeded, 60, finishedAt)
	action.Name = plan.Name + "-action"
	action.Namespace = plan.Namespace
	controller := true
	action.OwnerReferences = []metav1.OwnerReference{{
		APIVersion: v1alpha1.SchemeGroupVersion.String(),
		Kind:       "RemediationPlan",
		Name:       plan.Name,
		UID:        plan.UID,
		Controller: &controller,
	}}
	client := newRemediationPlanTTLClient(t, plan, action)

	result, err := reconcileRemediationPlanTTL(client, now, plan)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if result != (ctrl.Result{}) {
		t.Fatalf("expected no requeue after deletion, got %#v", result)
	}
	assertRemediationPlanDeleted(t, client, plan)
	assertAgentActionOwnedByPlan(t, client, action, plan)
}

func TestRemediationPlanTTLDoesNotDeleteTerminalPlanWithoutFinishedAt(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	plan := newTerminalRemediationPlan(v1alpha1.PhaseRejected, 60, metav1.Time{})
	plan.CreationTimestamp = metav1.NewTime(now.Add(-time.Hour))
	client := newRemediationPlanTTLClient(t, plan)

	if _, err := reconcileRemediationPlanTTL(client, now, plan); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	assertRemediationPlanExists(t, client, plan)
}

func TestRemediationPlanTTLReconcileIsIdempotentWhenPlanIsAlreadyGone(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	plan := newTerminalRemediationPlan(v1alpha1.PhaseRejected, 60, metav1.NewTime(now.Add(-time.Minute)))
	client := newRemediationPlanTTLClient(t)

	result, err := reconcileRemediationPlanTTL(client, now, plan)
	if err != nil {
		t.Fatalf("reconcile of missing plan failed: %v", err)
	}
	if result != (ctrl.Result{}) {
		t.Fatalf("expected empty result for missing plan, got %#v", result)
	}
}

func reconcileRemediationPlanTTL(c client.Client, now time.Time, plan *v1alpha1.RemediationPlan) (ctrl.Result, error) {
	return (&controllers.RemediationPlanTTLReconciler{
		Client: c,
		Now:    func() time.Time { return now },
	}).Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: plan.Name, Namespace: plan.Namespace},
	})
}

func newRemediationPlanTTLClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add API scheme: %v", err)
	}
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RemediationPlan{}, &v1alpha1.AgentAction{}).
		WithObjects(objects...).
		Build()
}

func newRemediationPlan(phase string, ttlSeconds int64, finishedAt metav1.Time) *v1alpha1.RemediationPlan {
	plan := &v1alpha1.RemediationPlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "remediation-plan-ttl",
			Namespace: "fluxseer-rca-test",
		},
		Spec: v1alpha1.RemediationPlanSpec{
			Target: v1alpha1.TargetRef{
				Kind:      "Deployment",
				Name:      "demo",
				Namespace: "default",
			},
			Severity:   "High",
			Confidence: 80,
			TTLSeconds: ttlSeconds,
		},
	}
	plan.Status.Phase = phase
	if !finishedAt.IsZero() {
		plan.Status.FinishedAt = &finishedAt
	}
	return plan
}

func newTerminalRemediationPlan(phase string, ttlSeconds int64, finishedAt metav1.Time) *v1alpha1.RemediationPlan {
	return newRemediationPlan(phase, ttlSeconds, finishedAt)
}

func assertRemediationPlanExists(t *testing.T, c client.Client, plan *v1alpha1.RemediationPlan) {
	t.Helper()
	var fetched v1alpha1.RemediationPlan
	err := c.Get(context.Background(), client.ObjectKeyFromObject(plan), &fetched)
	if err != nil {
		t.Fatalf("expected RemediationPlan to exist: %v", err)
	}
}

func assertRemediationPlanDeleted(t *testing.T, c client.Client, plan *v1alpha1.RemediationPlan) {
	t.Helper()
	var fetched v1alpha1.RemediationPlan
	err := c.Get(context.Background(), client.ObjectKeyFromObject(plan), &fetched)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected RemediationPlan to be deleted, got err=%v", err)
	}
}

func assertAgentActionOwnedByPlan(t *testing.T, c client.Client, action *v1alpha1.AgentAction, plan *v1alpha1.RemediationPlan) {
	t.Helper()
	var fetched v1alpha1.AgentAction
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(action), &fetched); err != nil {
		t.Fatalf("expected AgentAction to remain available for Kubernetes garbage collection: %v", err)
	}
	for _, owner := range fetched.OwnerReferences {
		if owner.Kind == "RemediationPlan" && owner.Name == plan.Name && owner.Controller != nil && *owner.Controller {
			return
		}
	}
	t.Fatalf("expected AgentAction to be controlled by RemediationPlan %s", plan.Name)
}
