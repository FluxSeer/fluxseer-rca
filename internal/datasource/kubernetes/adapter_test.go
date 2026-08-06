package kubernetes

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"fluxseer/api/v1alpha1"
	"fluxseer/internal/datasource"
	"fluxseer/internal/domain"
)

func TestAdapterQueryFiltersEventsByTarget(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&corev1.Event{
			ObjectMeta: metav1.ObjectMeta{Name: "event-1", Namespace: "demo"},
			InvolvedObject: corev1.ObjectReference{
				Kind: "Pod",
				Name: "demo-app-123",
			},
			Reason:  "BackOff",
			Message: "container crashed",
		},
	).Build()

	adapter := Adapter{Client: client}
	result, err := adapter.Query(context.Background(), datasource.QueryRequest{
		Target:    domain.ResourceRef{Namespace: "demo", Kind: "Deployment", Name: "demo-app"},
		QueryType: domain.QueryTypeEvent,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Records) != 1 {
		t.Fatalf("expected one record, got %d", len(result.Records))
	}
	if result.NativeCounts.Records != 1 {
		t.Fatalf("expected native event count, got %#v", result.NativeCounts)
	}
}

func TestAdapterQueryMatchesWorkloadEventsThroughRelatedPods(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "postgres-0",
				Namespace: "demo",
				Labels:    map[string]string{"app": "postgres"},
			},
		},
		&corev1.Event{
			ObjectMeta: metav1.ObjectMeta{Name: "event-1", Namespace: "demo"},
			InvolvedObject: corev1.ObjectReference{
				Kind: "Pod",
				Name: "postgres-0",
			},
			Reason:  "OOMKilled",
			Message: "container was killed after exceeding memory limit",
		},
	).Build()

	adapter := Adapter{Client: client}
	result, err := adapter.Query(context.Background(), datasource.QueryRequest{
		Target:    domain.ResourceRef{Namespace: "demo", Kind: "StatefulSet", Name: "postgres"},
		Labels:    map[string]string{"app": "postgres"},
		QueryType: domain.QueryTypeEvent,
		Reasons:   []string{"OOMKilled"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Records) != 1 || result.Records[0]["reason"] != "OOMKilled" {
		t.Fatalf("expected related pod OOMKilled event, got %#v", result.Records)
	}
}

func TestAdapterQueryFiltersEventsByRequestedReasonsBeforeNativeLimit(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&corev1.Event{
			ObjectMeta: metav1.ObjectMeta{Name: "event-1", Namespace: "demo"},
			InvolvedObject: corev1.ObjectReference{
				Kind: "Pod",
				Name: "demo-app-1",
			},
			Reason:        "Scheduled",
			Message:       "Successfully assigned demo/demo-app-1 to worker; later Unhealthy event exists",
			LastTimestamp: metav1.Unix(10, 0),
		},
		&corev1.Event{
			ObjectMeta: metav1.ObjectMeta{Name: "event-2", Namespace: "demo"},
			InvolvedObject: corev1.ObjectReference{
				Kind: "Pod",
				Name: "demo-app-1",
			},
			Reason:        "Unhealthy",
			Message:       "Readiness probe failed",
			LastTimestamp: metav1.Unix(20, 0),
		},
	).Build()

	adapter := Adapter{Client: client}
	result, err := adapter.Query(context.Background(), datasource.QueryRequest{
		Target:    domain.ResourceRef{Namespace: "demo", Kind: "Deployment", Name: "demo-app"},
		QueryType: domain.QueryTypeEvent,
		Reasons:   []string{"Unhealthy"},
		ResultLimits: v1alpha1.QueryResultLimits{
			Events: v1alpha1.EventResultLimits{MaxRecords: 1},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.NativeCounts.Records != 1 {
		t.Fatalf("expected native count after reason filtering, got %#v", result.NativeCounts)
	}
	if len(result.Records) != 1 || result.Records[0]["reason"] != "Unhealthy" {
		t.Fatalf("expected only Unhealthy event, got %#v", result.Records)
	}
	if result.Truncated {
		t.Fatalf("expected reason filtering before native limit to avoid truncation, got %#v", result)
	}
}

func TestAdapterQueryEnforcesKubernetesNativeEventRecordLimit(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&corev1.Event{
			ObjectMeta: metav1.ObjectMeta{Name: "event-2", Namespace: "demo"},
			InvolvedObject: corev1.ObjectReference{
				Kind: "Pod",
				Name: "demo-app-2",
			},
			Reason:        "BackOff",
			Message:       "second",
			LastTimestamp: metav1.Unix(20, 0),
		},
		&corev1.Event{
			ObjectMeta: metav1.ObjectMeta{Name: "event-1", Namespace: "demo"},
			InvolvedObject: corev1.ObjectReference{
				Kind: "Pod",
				Name: "demo-app-1",
			},
			Reason:        "Failed",
			Message:       "first",
			LastTimestamp: metav1.Unix(10, 0),
		},
	).Build()

	adapter := Adapter{Client: client}
	result, err := adapter.Query(context.Background(), datasource.QueryRequest{
		Target:    domain.ResourceRef{Namespace: "demo", Kind: "Deployment", Name: "demo-app"},
		QueryType: domain.QueryTypeEvent,
		ResultLimits: v1alpha1.QueryResultLimits{
			Events: v1alpha1.EventResultLimits{MaxRecords: 1},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.NativeCounts.Records != 2 {
		t.Fatalf("expected original native record count, got %#v", result.NativeCounts)
	}
	if len(result.Records) != 1 || result.Records[0]["message"] != "first" {
		t.Fatalf("expected earliest retained event, got %#v", result.Records)
	}
	if result.NativeLimit == nil || result.NativeLimit.Dimension != "records" || result.NativeLimit.OriginalCount != 2 || result.NativeLimit.RetainedCount != 1 {
		t.Fatalf("expected native record limit metadata, got %#v", result.NativeLimit)
	}
}

func TestAdapterQueryReturnsDeploymentConditions(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add apps scheme: %v", err)
	}

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "demo-app", Namespace: "demo"},
			Status: appsv1.DeploymentStatus{
				Conditions: []appsv1.DeploymentCondition{
					{
						Type:    appsv1.DeploymentAvailable,
						Status:  corev1.ConditionFalse,
						Reason:  "MinimumReplicasUnavailable",
						Message: "Deployment does not have minimum availability.",
					},
				},
			},
		},
	).Build()

	adapter := Adapter{Client: client}
	result, err := adapter.Query(context.Background(), datasource.QueryRequest{
		Target:    domain.ResourceRef{Namespace: "demo", Kind: "Deployment", Name: "demo-app"},
		QueryType: domain.QueryTypeDeploymentCondition,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.QueryType != domain.QueryTypeDeploymentCondition {
		t.Fatalf("expected deployment condition query type, got %s", result.QueryType)
	}
	if len(result.Records) != 1 {
		t.Fatalf("expected one record, got %d", len(result.Records))
	}
	if result.Records[0]["reason"] != "MinimumReplicasUnavailable" {
		t.Fatalf("unexpected condition record: %#v", result.Records[0])
	}
}

func TestAdapterQueryReturnsWorkloadStatusForNonDeploymentKinds(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add apps scheme: %v", err)
	}
	if err := batchv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add batch scheme: %v", err)
	}
	replicas := int32(2)
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{Name: "postgres", Namespace: "demo"},
			Spec:       appsv1.StatefulSetSpec{Replicas: &replicas},
			Status:     appsv1.StatefulSetStatus{ReadyReplicas: 1},
		},
		&appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{Name: "node-agent", Namespace: "demo"},
			Status:     appsv1.DaemonSetStatus{DesiredNumberScheduled: 3, NumberReady: 2},
		},
		&batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: "backup", Namespace: "demo"},
			Status:     batchv1.JobStatus{Failed: 1},
		},
		&batchv1.CronJob{
			ObjectMeta: metav1.ObjectMeta{Name: "nightly", Namespace: "demo"},
			Status:     batchv1.CronJobStatus{Active: []corev1.ObjectReference{{Name: "nightly-123"}}},
		},
	).Build()

	adapter := Adapter{Client: client}
	for _, tc := range []struct {
		kind       string
		name       string
		wantType   string
		wantStatus string
	}{
		{kind: "StatefulSet", name: "postgres", wantType: "Ready", wantStatus: "False"},
		{kind: "DaemonSet", name: "node-agent", wantType: "Ready", wantStatus: "False"},
		{kind: "Job", name: "backup", wantType: "Failed", wantStatus: "True"},
		{kind: "CronJob", name: "nightly", wantType: "Scheduled", wantStatus: "False"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			result, err := adapter.Query(context.Background(), datasource.QueryRequest{
				Target:    domain.ResourceRef{Namespace: "demo", Kind: tc.kind, Name: tc.name},
				QueryType: domain.QueryTypeDeploymentCondition,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(result.Records) == 0 {
				t.Fatalf("expected workload status records for %s", tc.kind)
			}
			record := result.Records[0]
			if record["type"] != tc.wantType || record["status"] != tc.wantStatus {
				t.Fatalf("unexpected status record for %s: %#v", tc.kind, record)
			}
		})
	}
}
