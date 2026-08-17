package executor

import (
	"context"
	"errors"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/FluxSeer/fluxseer-rca/internal/domain"
)

func TestKubernetesExecutorRestartsAllowlistedDeployment(t *testing.T) {
	scheme := clientgoscheme.Scheme
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "payments",
			Name:      "payments-api",
			UID:       types.UID("deployment-uid"),
		},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"existing": "value"}}}},
	}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(deployment).Build()
	executor := KubernetesExecutor{Client: kubeClient, Now: func() time.Time { return time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC) }}
	request := ExecutorRequest{
		ExecutionID:    "exec-restart-1",
		IdempotencyKey: "sha256:restart-1",
		ActionType:     KubernetesRolloutRestartAction,
		Target:         domain.ResourceRef{Namespace: "payments", Kind: "Deployment", Name: "payments-api", APIVersion: "apps/v1"},
		TargetUID:      "deployment-uid",
	}

	result, err := executor.Execute(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected execution error: %v", err)
	}
	if result.Outcome != ExecutionOutcomeSucceeded || result.ExecutionID != request.ExecutionID {
		t.Fatalf("expected successful execution result, got %#v", result)
	}

	var updated appsv1.Deployment
	if err := kubeClient.Get(context.Background(), client.ObjectKey{Namespace: "payments", Name: "payments-api"}, &updated); err != nil {
		t.Fatalf("get updated deployment: %v", err)
	}
	if got := updated.Spec.Template.Annotations[kubernetesExecutionIDAnnotation]; got != request.ExecutionID {
		t.Fatalf("expected execution annotation %q, got %q", request.ExecutionID, got)
	}
	if updated.Spec.Template.Annotations["existing"] != "value" {
		t.Fatal("expected existing pod template annotation to be preserved")
	}

	recovered, found, err := executor.Resolve(context.Background(), request)
	if err != nil || !found || recovered.Outcome != ExecutionOutcomeSucceeded {
		t.Fatalf("expected read-after-write recovery, result=%#v found=%v err=%v", recovered, found, err)
	}
}

func TestKubernetesExecutorRejectsTargetUIDMismatch(t *testing.T) {
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "payments", Name: "payments-api", UID: types.UID("current-uid")},
	}
	kubeClient := fake.NewClientBuilder().WithScheme(clientgoscheme.Scheme).WithObjects(deployment).Build()
	executor := KubernetesExecutor{Client: kubeClient}
	request := ExecutorRequest{
		ExecutionID:    "exec-restart-2",
		IdempotencyKey: "sha256:restart-2",
		ActionType:     KubernetesRolloutRestartAction,
		Target:         domain.ResourceRef{Namespace: "payments", Kind: "Deployment", Name: "payments-api"},
		TargetUID:      "stale-uid",
	}

	result, err := executor.Execute(context.Background(), request)
	if err == nil || result.FailureReason != "TargetUIDMismatch" || result.Outcome != ExecutionOutcomeFailed {
		t.Fatalf("expected target UID rejection, result=%#v err=%v", result, err)
	}
	if deployment.Spec.Template.Annotations != nil {
		t.Fatal("target UID rejection must not mutate the deployment")
	}
}

func TestKubernetesExecutorMarksPatchTimeoutUnknown(t *testing.T) {
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "payments", Name: "payments-api", UID: types.UID("deployment-uid")},
	}
	baseClient := fake.NewClientBuilder().WithScheme(clientgoscheme.Scheme).WithObjects(deployment).Build()
	kubeClient := failingPatchClient{Client: baseClient, err: context.DeadlineExceeded}
	executor := KubernetesExecutor{Client: kubeClient}
	request := ExecutorRequest{
		ExecutionID:    "exec-restart-3",
		IdempotencyKey: "sha256:restart-3",
		ActionType:     KubernetesRolloutRestartAction,
		Target:         domain.ResourceRef{Namespace: "payments", Kind: "Deployment", Name: "payments-api"},
		TargetUID:      "deployment-uid",
	}

	result, err := executor.Execute(context.Background(), request)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) || result.Outcome != ExecutionOutcomeUnknown || result.FailureReason != "TimeoutAfterDispatch" {
		t.Fatalf("expected unknown timeout result, result=%#v err=%v", result, err)
	}
}

type failingPatchClient struct {
	client.Client
	err error
}

func (c failingPatchClient) Patch(ctx context.Context, obj client.Object, pt client.Patch, opts ...client.PatchOption) error {
	return c.err
}
