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

func TestAgentActionTTLDisabledDoesNotDeleteTerminalAction(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	finishedAt := metav1.NewTime(now.Add(-10 * time.Minute))
	action := newTerminalAgentAction(v1alpha1.PhaseSucceeded, 0, finishedAt)
	client := newAgentActionTTLClient(t, action)

	result, err := reconcileAgentActionTTL(client, now, action)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if result != (ctrl.Result{}) {
		t.Fatalf("expected no requeue with disabled TTL, got %#v", result)
	}
	assertAgentActionExists(t, client, action)
}

func TestAgentActionTTLDoesNotDeleteNonTerminalAction(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	action := newAgentAction(v1alpha1.PhaseWaitingApproval, 30, metav1.NewTime(now.Add(-time.Hour)))
	client := newAgentActionTTLClient(t, action)

	result, err := reconcileAgentActionTTL(client, now, action)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if result != (ctrl.Result{}) {
		t.Fatalf("expected no TTL requeue for non-terminal action, got %#v", result)
	}
	assertAgentActionExists(t, client, action)
}

func TestAgentActionTTLRequeuesFromFinishedAt(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	finishedAt := metav1.NewTime(now.Add(-30 * time.Second))
	action := newTerminalAgentAction(v1alpha1.PhaseSucceeded, 60, finishedAt)
	action.CreationTimestamp = metav1.NewTime(now.Add(-time.Hour))
	client := newAgentActionTTLClient(t, action)

	result, err := reconcileAgentActionTTL(client, now, action)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if result.RequeueAfter != 30*time.Second {
		t.Fatalf("expected requeue after 30s from finishedAt, got %s", result.RequeueAfter)
	}
	assertAgentActionExists(t, client, action)
}

func TestAgentActionTTLDeletesExpiredSucceededAction(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	finishedAt := metav1.NewTime(now.Add(-61 * time.Second))
	action := newTerminalAgentAction(v1alpha1.PhaseSucceeded, 60, finishedAt)
	client := newAgentActionTTLClient(t, action)

	result, err := reconcileAgentActionTTL(client, now, action)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if result != (ctrl.Result{}) {
		t.Fatalf("expected no requeue after deletion, got %#v", result)
	}
	assertAgentActionDeleted(t, client, action)
}

func TestAgentActionTTLDeletesExpiredFailedAndRejectedActions(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	for _, phase := range []string{v1alpha1.PhaseFailed, v1alpha1.PhaseRejected} {
		t.Run(phase, func(t *testing.T) {
			finishedAt := metav1.NewTime(now.Add(-2 * time.Minute))
			action := newTerminalAgentAction(phase, 60, finishedAt)
			action.Name = "expired-" + phase
			client := newAgentActionTTLClient(t, action)

			if _, err := reconcileAgentActionTTL(client, now, action); err != nil {
				t.Fatalf("reconcile failed: %v", err)
			}
			assertAgentActionDeleted(t, client, action)
		})
	}
}

func TestAgentActionTTLDoesNotDeleteTerminalActionWithoutFinishedAt(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	action := newTerminalAgentAction(v1alpha1.PhaseSucceeded, 60, metav1.Time{})
	action.CreationTimestamp = metav1.NewTime(now.Add(-time.Hour))
	client := newAgentActionTTLClient(t, action)

	if _, err := reconcileAgentActionTTL(client, now, action); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	assertAgentActionExists(t, client, action)
}

func TestAgentActionTTLReconcileIsIdempotentWhenActionIsAlreadyGone(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	action := newTerminalAgentAction(v1alpha1.PhaseSucceeded, 60, metav1.NewTime(now.Add(-time.Minute)))
	client := newAgentActionTTLClient(t)

	result, err := reconcileAgentActionTTL(client, now, action)
	if err != nil {
		t.Fatalf("reconcile of missing action failed: %v", err)
	}
	if result != (ctrl.Result{}) {
		t.Fatalf("expected empty result for missing action, got %#v", result)
	}
}

func reconcileAgentActionTTL(c client.Client, now time.Time, action *v1alpha1.AgentAction) (ctrl.Result, error) {
	return (&controllers.AgentActionReconciler{
		Client: c,
		Now:    func() time.Time { return now },
	}).Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: action.Name, Namespace: action.Namespace},
	})
}

func newAgentActionTTLClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add API scheme: %v", err)
	}
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.AgentAction{}).
		WithObjects(objects...).
		Build()
}

func newAgentAction(phase string, ttlSeconds int64, finishedAt metav1.Time) *v1alpha1.AgentAction {
	action := &v1alpha1.AgentAction{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "agent-action-ttl",
			Namespace: "fluxseer-rca-test",
		},
		Spec: v1alpha1.AgentActionSpec{
			Target: v1alpha1.TargetRef{
				Kind:      "Deployment",
				Name:      "demo",
				Namespace: "default",
			},
			ActionType: "kubernetes.rolloutPause",
			TTLSeconds: ttlSeconds,
		},
	}
	action.Status.Phase = phase
	if !finishedAt.IsZero() {
		action.Status.FinishedAt = &finishedAt
	}
	return action
}

func newTerminalAgentAction(phase string, ttlSeconds int64, finishedAt metav1.Time) *v1alpha1.AgentAction {
	return newAgentAction(phase, ttlSeconds, finishedAt)
}

func assertAgentActionExists(t *testing.T, c client.Client, action *v1alpha1.AgentAction) {
	t.Helper()
	var fetched v1alpha1.AgentAction
	err := c.Get(context.Background(), client.ObjectKeyFromObject(action), &fetched)
	if err != nil {
		t.Fatalf("expected AgentAction to exist: %v", err)
	}
}

func assertAgentActionDeleted(t *testing.T, c client.Client, action *v1alpha1.AgentAction) {
	t.Helper()
	var fetched v1alpha1.AgentAction
	err := c.Get(context.Background(), client.ObjectKeyFromObject(action), &fetched)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected AgentAction to be deleted, got err=%v", err)
	}
}
