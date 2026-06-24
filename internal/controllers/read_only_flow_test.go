package controllers

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"fluxagent/api/v1alpha1"
	"fluxagent/internal/datasource"
	"fluxagent/internal/detector"
	"fluxagent/internal/domain"
	"fluxagent/internal/notifier"
)

type fakeDataSource struct {
	name   string
	result *datasource.QueryResult
}

func (f fakeDataSource) Name() string { return f.name }
func (f fakeDataSource) Type() string { return string(f.result.QueryType) }
func (f fakeDataSource) Capabilities() datasource.Capabilities {
	return datasource.Capabilities{
		Metrics: f.result.QueryType == domain.QueryTypeMetric,
		Logs:    f.result.QueryType == domain.QueryTypeLog,
		Events:  f.result.QueryType == domain.QueryTypeEvent,
		Traces:  f.result.QueryType == domain.QueryTypeTrace,
	}
}
func (f fakeDataSource) HealthCheck(context.Context) error { return nil }
func (f fakeDataSource) Query(context.Context, datasource.QueryRequest) (*datasource.QueryResult, error) {
	return f.result, nil
}

type fakeNotifier struct {
	calls       int
	lastMessage notifier.Message
}

func (f *fakeNotifier) Name() string { return "fake" }
func (f *fakeNotifier) Notify(_ context.Context, message notifier.Message) error {
	f.calls++
	f.lastMessage = message
	return nil
}

func TestReadOnlyFlowCreatesRiskSignalAndNotifies(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	now := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "fluxagent-sample",
			Namespace: "fluxagent-demo",
			Annotations: map[string]string{
				detector.AnnotationEnabled: "true",
			},
			Labels: map[string]string{"app": "fluxagent-sample"},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "fluxagent-sample"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "fluxagent-sample"}},
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RiskSignal{}).
		WithObjects(deployment).
		Build()

	registry := datasource.NewRegistry(
		fakeDataSource{name: "prometheus", result: &datasource.QueryResult{Source: "prometheus", QueryType: domain.QueryTypeMetric, Records: []map[string]any{{"value": "0.5"}}}},
		fakeDataSource{name: "loki", result: &datasource.QueryResult{Source: "loki", QueryType: domain.QueryTypeLog, Records: []map[string]any{{"line": "error timeout"}}}},
		fakeDataSource{name: "kubernetes-events", result: &datasource.QueryResult{Source: "kubernetes-events", QueryType: domain.QueryTypeEvent, Records: []map[string]any{{"reason": "BackOff", "message": "crash loop"}}}},
	)

	reconciler := &DeploymentRiskReconciler{
		Client: client,
		Scheme: scheme,
		Detector: &detector.Service{
			Registry: registry,
			Now:      func() time.Time { return now },
		},
		Interval: 30 * time.Second,
		Now:      func() time.Time { return now },
	}
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: deployment.Name, Namespace: deployment.Namespace},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var riskSignal v1alpha1.RiskSignal
	if err := client.Get(context.Background(), types.NamespacedName{Name: "fluxagent-sample-observed-risk", Namespace: "fluxagent-demo"}, &riskSignal); err != nil {
		t.Fatalf("expected risk signal: %v", err)
	}
	if riskSignal.Status.Phase != v1alpha1.PhaseConfirmed {
		t.Fatalf("expected confirmed phase, got %s", riskSignal.Status.Phase)
	}

	notifier := &fakeNotifier{}
	notifyReconciler := &RiskSignalNotificationReconciler{
		Client:   client,
		Scheme:   scheme,
		Notifier: notifier,
		Now:      func() time.Time { return now },
	}
	if _, err := notifyReconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: riskSignal.Name, Namespace: riskSignal.Namespace},
	}); err != nil {
		t.Fatalf("unexpected notify error: %v", err)
	}
	if notifier.calls != 1 {
		t.Fatalf("expected 1 notification, got %d", notifier.calls)
	}
	if notifier.lastMessage.Fields["origin"] != "deployment-annotation" {
		t.Fatalf("expected deployment origin field, got %#v", notifier.lastMessage.Fields["origin"])
	}
}
