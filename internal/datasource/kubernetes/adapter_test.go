package kubernetes

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"fluxagent/internal/datasource"
	"fluxagent/internal/domain"
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
}
