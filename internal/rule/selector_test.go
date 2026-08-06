package rule

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"fluxseer/api/v1alpha1"
)

func TestDiscoverTargetsSupportsWorkloadKinds(t *testing.T) {
	scheme := testScheme(t)
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "prod", Labels: map[string]string{"app": "web"}}},
			&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "prod", Labels: map[string]string{"app": "db"}}},
			&appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "agent", Namespace: "prod", Labels: map[string]string{"app": "agent"}}},
			&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "backup", Namespace: "prod", Labels: map[string]string{"app": "backup"}}},
			&batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: "nightly", Namespace: "prod", Labels: map[string]string{"app": "nightly"}}},
		).
		Build()

	targets, err := DiscoverTargets(context.Background(), client, v1alpha1.TargetSelector{
		NamespaceSelector: v1alpha1.NamespaceSelector{MatchNames: []string{"prod"}},
		WorkloadSelector: v1alpha1.WorkloadSelector{Kinds: []string{
			"Deployment",
			"StatefulSet",
			"DaemonSet",
			"Job",
			"CronJob",
		}},
	})
	if err != nil {
		t.Fatalf("discover targets: %v", err)
	}
	got := map[string]bool{}
	for _, target := range targets {
		got[target.Resource.Kind+"/"+target.Resource.Name] = true
	}
	for _, expected := range []string{"Deployment/web", "StatefulSet/db", "DaemonSet/agent", "Job/backup", "CronJob/nightly"} {
		if !got[expected] {
			t.Fatalf("expected target %s in %#v", expected, got)
		}
	}
}

func TestDiscoverTargetsCanonicalizesPodOwnerToDeployment(t *testing.T) {
	scheme := testScheme(t)
	controller := true
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "prod", UID: types.UID("deployment-uid")},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web"}}},
		},
	}
	replicaSet := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-abc",
			Namespace: "prod",
			UID:       types.UID("rs-uid"),
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       "web",
				UID:        types.UID("deployment-uid"),
				Controller: &controller,
			}},
		},
		Spec: appsv1.ReplicaSetSpec{
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web"}}},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-abc-123",
			Namespace: "prod",
			UID:       types.UID("pod-uid"),
			Labels:    map[string]string{"app": "web"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1",
				Kind:       "ReplicaSet",
				Name:       "web-abc",
				UID:        types.UID("rs-uid"),
				Controller: &controller,
			}},
		},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(deployment, replicaSet, pod).Build()

	targets, err := DiscoverTargets(context.Background(), client, v1alpha1.TargetSelector{
		NamespaceSelector: v1alpha1.NamespaceSelector{MatchNames: []string{"prod"}},
		WorkloadSelector:  v1alpha1.WorkloadSelector{Kinds: []string{"Pod"}, MatchLabels: map[string]string{"app": "web"}},
	})
	if err != nil {
		t.Fatalf("discover targets: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected one canonical target, got %d", len(targets))
	}
	if targets[0].Resource.Kind != "Deployment" || targets[0].Resource.Name != "web" {
		t.Fatalf("expected Deployment/web, got %s/%s", targets[0].Resource.Kind, targets[0].Resource.Name)
	}
	if targets[0].UID != "deployment-uid" {
		t.Fatalf("expected canonical deployment UID, got %s", targets[0].UID)
	}
	if len(targets[0].Pods) != 1 || targets[0].Pods[0].Name != "web-abc-123" {
		t.Fatalf("expected pod reference on canonical target, got %#v", targets[0].Pods)
	}
}

func TestDiscoverCoverageReportsUnsupportedSelectedKinds(t *testing.T) {
	scheme := testScheme(t)
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}},
			&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-2"}},
		).
		Build()

	coverage, err := DiscoverCoverage(context.Background(), client, v1alpha1.TargetSelector{
		WorkloadSelector: v1alpha1.WorkloadSelector{Kinds: []string{"Deployment", "Node"}},
	})
	if err != nil {
		t.Fatalf("discover coverage: %v", err)
	}
	if !coverage.Partial {
		t.Fatal("expected partial coverage")
	}
	if coverage.UnsupportedDiscoveredKinds["Node"] != 2 {
		t.Fatalf("expected two unsupported nodes, got %#v", coverage.UnsupportedDiscoveredKinds)
	}
}

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add apps scheme: %v", err)
	}
	if err := batchv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add batch scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add aiops scheme: %v", err)
	}
	return scheme
}
