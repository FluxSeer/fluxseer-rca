package scope

import (
	"context"
	"errors"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestResolverUsesControllerOwnerUIDAndKeepsMultipleReplicaSets(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add apps scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	controller := true
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "prod", UID: "deployment-1"},
		Spec: appsv1.DeploymentSpec{Selector: &metav1.LabelSelector{
			MatchLabels:      map[string]string{"app": "web"},
			MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "tier", Operator: metav1.LabelSelectorOpIn, Values: []string{"frontend"}}},
		}},
	}
	oldReplicaSet := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-old", Namespace: "prod", UID: "replicaset-old",
			Labels:          map[string]string{"app": "web", "tier": "frontend"},
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: "web", UID: deployment.UID, Controller: &controller}},
		},
	}
	newReplicaSet := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-new", Namespace: "prod", UID: "replicaset-new",
			Labels:          map[string]string{"app": "web", "tier": "frontend"},
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: "web", UID: deployment.UID, Controller: &controller}},
		},
	}
	decoyReplicaSet := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-decoy", Namespace: "prod", UID: "replicaset-decoy",
			Labels:          map[string]string{"app": "web", "tier": "frontend"},
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: "web", UID: "different-deployment", Controller: &controller}},
		},
	}
	oldPod := ownedPod("web-old-pod", "pod-old", "replicaset-old")
	newPod := ownedPod("web-new-pod", "pod-new", "replicaset-new")
	decoyPod := ownedPod("web-decoy-pod", "pod-decoy", "replicaset-decoy")
	reader := clientfake.NewClientBuilder().WithScheme(scheme).WithObjects(deployment, oldReplicaSet, newReplicaSet, decoyReplicaSet, oldPod, newPod, decoyPod).Build()

	result, err := NewResolver(reader, Limits{}).ResolveDeployment(context.Background(), "prod", "web")
	if err != nil {
		t.Fatalf("resolve deployment: %v", err)
	}
	if !result.Complete {
		t.Fatalf("expected complete scope, warnings=%#v", result.Warnings)
	}
	if result.Deployment.UID != deployment.UID {
		t.Fatalf("expected target UID %q, got %q", deployment.UID, result.Deployment.UID)
	}
	if got := resourceNames(result.ReplicaSets); len(got) != 2 || got[0] != "web-new" || got[1] != "web-old" {
		t.Fatalf("expected both valid ReplicaSets, got %#v", got)
	}
	if got := resourceNames(result.Pods); len(got) != 2 || got[0] != "web-new-pod" || got[1] != "web-old-pod" {
		t.Fatalf("expected only Pods owned by valid ReplicaSets, got %#v", got)
	}
	if !hasWarning(result.Warnings, WarningOwnerMismatch, "web-decoy") {
		t.Fatalf("expected owner mismatch warning for decoy ReplicaSet, warnings=%#v", result.Warnings)
	}
	if !hasWarning(result.Warnings, WarningOwnerMismatch, "web-decoy-pod") {
		t.Fatalf("expected owner mismatch warning for decoy Pod, warnings=%#v", result.Warnings)
	}
}

func TestResolverRejectsDeploymentWithoutSelector(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add apps scheme: %v", err)
	}
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "prod", UID: "deployment-1"}}
	reader := clientfake.NewClientBuilder().WithScheme(scheme).WithObjects(deployment).Build()

	result, err := NewResolver(reader, Limits{}).ResolveDeployment(context.Background(), "prod", "web")
	if err == nil || result.Complete {
		t.Fatalf("expected invalid selector failure, result=%#v err=%v", result, err)
	}
	if !hasWarning(result.Warnings, WarningInvalidTarget, "") {
		t.Fatalf("expected invalid target warning, warnings=%#v", result.Warnings)
	}
}

func TestResolverReportsPartialPodListFailure(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add apps scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	controller := true
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "prod", UID: "deployment-1"},
		Spec:       appsv1.DeploymentSpec{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}},
	}
	replicaSet := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
		Name: "web-rs", Namespace: "prod", UID: "replicaset-1", Labels: map[string]string{"app": "web"},
		OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: "web", UID: deployment.UID, Controller: &controller}},
	}}
	base := clientfake.NewClientBuilder().WithScheme(scheme).WithObjects(deployment, replicaSet).Build()
	reader := podListErrorReader{Reader: base}

	result, err := NewResolver(reader, Limits{}).ResolveDeployment(context.Background(), "prod", "web")
	if err == nil {
		t.Fatal("expected Pod list failure")
	}
	if result.Complete {
		t.Fatalf("expected incomplete scope, result=%#v", result)
	}
	if !hasWarning(result.Warnings, WarningPodList, "") {
		t.Fatalf("expected Pod list warning, warnings=%#v", result.Warnings)
	}
}

func TestResolverReturnsNotFoundAsIncomplete(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add apps scheme: %v", err)
	}
	reader := clientfake.NewClientBuilder().WithScheme(scheme).Build()

	result, err := NewResolver(reader, Limits{}).ResolveDeployment(context.Background(), "prod", "missing")
	if err == nil || !apierrors.IsNotFound(err) || result.Complete {
		t.Fatalf("expected not found incomplete result, result=%#v err=%v", result, err)
	}
	if !hasWarning(result.Warnings, WarningResourceNotFound, "") {
		t.Fatalf("expected not found warning, warnings=%#v", result.Warnings)
	}
}

type podListErrorReader struct {
	client.Reader
}

func (r podListErrorReader) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if _, ok := list.(*corev1.PodList); ok {
		return errors.New("pods forbidden")
	}
	return r.Reader.List(ctx, list, opts...)
}

func ownedPod(name string, uid, replicaSetUID types.UID) *corev1.Pod {
	controller := true
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: name, Namespace: "prod", UID: uid, Labels: map[string]string{"app": "web", "tier": "frontend"},
		OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "web", UID: replicaSetUID, Controller: &controller}},
	}}
}

func resourceNames(refs []ResourceRef) []string {
	result := make([]string, 0, len(refs))
	for _, ref := range refs {
		result = append(result, ref.Name)
	}
	return result
}

func hasWarning(warnings []ScopeWarning, code WarningCode, name string) bool {
	for _, warning := range warnings {
		if warning.Code != code {
			continue
		}
		if name == "" || warning.Resource != nil && warning.Resource.Name == name {
			return true
		}
	}
	return false
}
