package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"fluxagent/api/v1alpha1"
	"fluxagent/internal/datasource"
	"fluxagent/internal/domain"
	"fluxagent/internal/knowledge"
	"fluxagent/internal/model"
	"fluxagent/internal/model/heuristic"
	"fluxagent/internal/model/local"
	"fluxagent/internal/model/openai"
	"fluxagent/internal/modelgateway"
)

type fakeRuleDataSource struct {
	name      string
	queryType domain.QueryType
	result    *datasource.QueryResult
	requests  []datasource.QueryRequest
}

func (f *fakeRuleDataSource) Name() string { return f.name }
func (f *fakeRuleDataSource) Type() string { return string(f.queryType) }
func (f *fakeRuleDataSource) Capabilities() datasource.Capabilities {
	return datasource.Capabilities{
		Metrics: f.queryType == domain.QueryTypeMetric,
		Logs:    f.queryType == domain.QueryTypeLog,
		Events:  f.queryType == domain.QueryTypeEvent,
		Traces:  f.queryType == domain.QueryTypeTrace,
	}
}
func (f *fakeRuleDataSource) HealthCheck(context.Context) error { return nil }
func (f *fakeRuleDataSource) Query(_ context.Context, req datasource.QueryRequest) (*datasource.QueryResult, error) {
	f.requests = append(f.requests, req)
	return f.result, nil
}

func TestRiskRuleReconcilerMarksRuleObservedAndRequeues(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add apps scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add core scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	now := time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC)
	rule := &v1alpha1.RiskRule{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
			Kind:       "RiskRule",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:       "latency-regression",
			Namespace:  "fluxagent-system",
			Generation: 1,
		},
		Spec: v1alpha1.RiskRuleSpec{
			Interval: metav1.Duration{Duration: 2 * time.Minute},
			Severity: "warning",
			Signals: []v1alpha1.RiskRuleSignal{
				{
					Name:  "p95-latency",
					Type:  "prometheus",
					Query: `histogram_quantile(0.95, rate(http_request_duration_seconds_bucket[5m]))`,
					Threshold: v1alpha1.RiskThreshold{
						Operator: ">",
						Value:    1.5,
					},
				},
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RiskRule{}).
		WithObjects(rule).
		Build()

	reconciler := &RiskRuleReconciler{
		Client: client,
		Scheme: scheme,
		Now:    func() time.Time { return now },
	}
	result, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: rule.Name, Namespace: rule.Namespace},
	})
	if err != nil {
		t.Fatalf("unexpected reconcile error: %v", err)
	}
	if result.RequeueAfter != 2*time.Minute {
		t.Fatalf("expected 2m requeue, got %s", result.RequeueAfter)
	}

	var stored v1alpha1.RiskRule
	if err := client.Get(context.Background(), types.NamespacedName{Name: rule.Name, Namespace: rule.Namespace}, &stored); err != nil {
		t.Fatalf("expected risk rule: %v", err)
	}
	if stored.Status.Phase != v1alpha1.PhaseObserved {
		t.Fatalf("expected observed phase, got %s", stored.Status.Phase)
	}
	if stored.Status.Message == "" {
		t.Fatal("expected status message to be populated")
	}
	if stored.Status.ObservedGeneration != 1 {
		t.Fatalf("expected observed generation 1, got %d", stored.Status.ObservedGeneration)
	}
}

func TestRiskRuleReconcilerCreatesIdempotentRiskSignalFromMatchingDeployment(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add apps scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	now := time.Date(2026, 6, 24, 11, 0, 0, 0, time.UTC)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "open-api",
			Namespace: "prod",
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "open-api"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "open-api"},
				},
			},
		},
	}
	ruleObj := &v1alpha1.RiskRule{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
			Kind:       "RiskRule",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:       "latency-regression",
			Namespace:  "fluxagent-system",
			Generation: 2,
		},
		Spec: v1alpha1.RiskRuleSpec{
			Interval: metav1.Duration{Duration: 2 * time.Minute},
			Window:   metav1.Duration{Duration: 10 * time.Minute},
			Severity: "warning",
			TargetSelector: v1alpha1.TargetSelector{
				NamespaceSelector: v1alpha1.NamespaceSelector{
					MatchNames: []string{"prod"},
				},
				WorkloadSelector: v1alpha1.WorkloadSelector{
					MatchLabels: map[string]string{"app": "open-api"},
					Kinds:       []string{"Deployment"},
				},
			},
			Signals: []v1alpha1.RiskRuleSignal{
				{
					Name:  "p95-latency",
					Type:  "prometheus",
					Query: `sum(rate(http_requests_total{namespace="{{ .namespace }}",app="{{ .app }}"}[5m]))`,
					Threshold: v1alpha1.RiskThreshold{
						Operator: ">",
						Value:    1.5,
					},
				},
			},
		},
	}

	promSource := &fakeRuleDataSource{
		name:      "prometheus",
		queryType: domain.QueryTypeMetric,
		result: &datasource.QueryResult{
			Source:    "prometheus",
			QueryType: domain.QueryTypeMetric,
			Records:   []map[string]any{{"value": "2.1"}},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RiskRule{}, &v1alpha1.RiskSignal{}).
		WithObjects(ruleObj, deployment).
		Build()

	reconciler := &RiskRuleReconciler{
		Client:   client,
		Scheme:   scheme,
		Registry: datasource.NewRegistry(promSource),
		Resolver: modelgateway.KubeResolver{Client: client},
		Gateway: &modelgateway.Gateway{
			Base:      knowledge.NewBase(),
			Providers: model.NewRegistry(heuristic.Provider{}),
		},
		Now: func() time.Time { return now },
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: ruleObj.Name, Namespace: ruleObj.Namespace}}
	if _, err := reconciler.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}
	if _, err := reconciler.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	if len(promSource.requests) != 2 {
		t.Fatalf("expected 2 datasource queries, got %d", len(promSource.requests))
	}
	if got := promSource.requests[0].Query; got != `sum(rate(http_requests_total{namespace="prod",app="open-api"}[5m]))` {
		t.Fatalf("unexpected rendered query: %s", got)
	}

	var signals v1alpha1.RiskSignalList
	if err := client.List(context.Background(), &signals); err != nil {
		t.Fatalf("failed to list risk signals: %v", err)
	}
	if len(signals.Items) != 1 {
		t.Fatalf("expected 1 risk signal, got %d", len(signals.Items))
	}

	riskSignal := signals.Items[0]
	if riskSignal.Namespace != "prod" {
		t.Fatalf("expected risk signal in prod, got %s", riskSignal.Namespace)
	}
	if riskSignal.Spec.Target.Name != "open-api" {
		t.Fatalf("expected target open-api, got %s", riskSignal.Spec.Target.Name)
	}
	if riskSignal.Status.Phase != v1alpha1.PhaseConfirmed {
		t.Fatalf("expected confirmed risk signal, got %s", riskSignal.Status.Phase)
	}
	if riskSignal.Spec.Parameters["riskRule"] != "latency-regression" {
		t.Fatalf("expected riskRule parameter to be set")
	}
	if riskSignal.Labels[labelRiskRule] != "latency-regression" {
		t.Fatalf("expected risk rule label to be set")
	}
	if riskSignal.Annotations[annotationDetectionSource] != "risk-rule" {
		t.Fatalf("expected risk-rule detection source annotation")
	}

	var storedRule v1alpha1.RiskRule
	if err := client.Get(context.Background(), types.NamespacedName{Name: ruleObj.Name, Namespace: ruleObj.Namespace}, &storedRule); err != nil {
		t.Fatalf("failed to fetch rule: %v", err)
	}
	if storedRule.Status.Message != "processed 1 targets; 1 produced RiskSignal" {
		t.Fatalf("unexpected rule status message: %s", storedRule.Status.Message)
	}
	if got := findCondition(riskSignal.Status.Conditions, conditionRCAReady); got != nil {
		t.Fatalf("expected no RCA condition when ai.rcaEnabled=false, got %#v", got)
	}
}

func TestRiskRuleReconcilerUsesDatasourceRefQueryTypeAndTemplate(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add apps scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	now := time.Date(2026, 6, 24, 11, 30, 0, 0, time.UTC)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "catalog-api",
			Namespace: "prod",
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "catalog-api"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "catalog-api"},
				},
			},
		},
	}
	ruleObj := &v1alpha1.RiskRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "catalog-error-logs",
			Namespace: "fluxagent-system",
		},
		Spec: v1alpha1.RiskRuleSpec{
			TargetSelector: v1alpha1.TargetSelector{
				NamespaceSelector: v1alpha1.NamespaceSelector{MatchNames: []string{"prod"}},
				WorkloadSelector:  v1alpha1.WorkloadSelector{MatchLabels: map[string]string{"app": "catalog-api"}},
			},
			Signals: []v1alpha1.RiskRuleSignal{
				{
					Name:          "error-logs",
					DatasourceRef: v1alpha1.LocalObjectReference{Name: "logs-main"},
					QueryType:     "log",
					QueryTemplate: `{namespace="{{ .namespace }}",app="{{ .labels.app }}"} |= "error"`,
					Threshold:     v1alpha1.RiskThreshold{Operator: "count_gt", Value: 0},
				},
			},
		},
	}
	logSource := &fakeRuleDataSource{
		name:      "loki",
		queryType: domain.QueryTypeLog,
		result: &datasource.QueryResult{
			Source:    "logs-main",
			QueryType: domain.QueryTypeLog,
			Records:   []map[string]any{{"line": "error upstream timeout"}},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RiskRule{}, &v1alpha1.RiskSignal{}).
		WithObjects(ruleObj, deployment).
		Build()

	registry := datasource.NewRegistry()
	registry.RegisterNamed("logs-main", logSource)

	reconciler := &RiskRuleReconciler{
		Client:   client,
		Scheme:   scheme,
		Registry: registry,
		Now:      func() time.Time { return now },
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: ruleObj.Name, Namespace: ruleObj.Namespace},
	}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	if len(logSource.requests) != 1 {
		t.Fatalf("expected 1 datasource query, got %d", len(logSource.requests))
	}
	if got := logSource.requests[0].Query; got != `{namespace="prod",app="catalog-api"} |= "error"` {
		t.Fatalf("unexpected rendered query: %s", got)
	}

	var signals v1alpha1.RiskSignalList
	if err := client.List(context.Background(), &signals, crclient.InNamespace("prod")); err != nil {
		t.Fatalf("list risk signals: %v", err)
	}
	if len(signals.Items) != 1 {
		t.Fatalf("expected one risk signal, got %d", len(signals.Items))
	}
	if signals.Items[0].Spec.SignalType != "log" {
		t.Fatalf("expected signal type log, got %s", signals.Items[0].Spec.SignalType)
	}
}

func TestRiskRuleReconcilerSkipsCapabilityMismatch(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add apps scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "catalog-api", Namespace: "prod"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "catalog-api"}},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "catalog-api"}}},
		},
	}
	ruleObj := &v1alpha1.RiskRule{
		ObjectMeta: metav1.ObjectMeta{Name: "capability-mismatch", Namespace: "fluxagent-system"},
		Spec: v1alpha1.RiskRuleSpec{
			TargetSelector: v1alpha1.TargetSelector{
				NamespaceSelector: v1alpha1.NamespaceSelector{MatchNames: []string{"prod"}},
				WorkloadSelector:  v1alpha1.WorkloadSelector{MatchLabels: map[string]string{"app": "catalog-api"}},
			},
			Signals: []v1alpha1.RiskRuleSignal{
				{
					Name:          "bad-query-type",
					DatasourceRef: v1alpha1.LocalObjectReference{Name: "metrics-main"},
					QueryType:     "log",
					QueryTemplate: `sum(rate(http_requests_total{namespace="{{ .namespace }}"}[5m]))`,
					Threshold:     v1alpha1.RiskThreshold{Operator: "count_gt", Value: 0},
				},
			},
		},
	}
	metricSource := &fakeRuleDataSource{
		name:      "prometheus",
		queryType: domain.QueryTypeMetric,
		result: &datasource.QueryResult{
			Source:    "metrics-main",
			QueryType: domain.QueryTypeMetric,
			Records:   []map[string]any{{"value": "2.1"}},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RiskRule{}, &v1alpha1.RiskSignal{}).
		WithObjects(ruleObj, deployment).
		Build()

	registry := datasource.NewRegistry()
	registry.RegisterNamed("metrics-main", metricSource)

	reconciler := &RiskRuleReconciler{
		Client:   client,
		Scheme:   scheme,
		Registry: registry,
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: ruleObj.Name, Namespace: ruleObj.Namespace},
	}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	if len(metricSource.requests) != 0 {
		t.Fatalf("expected no datasource queries on capability mismatch, got %d", len(metricSource.requests))
	}
	var signals v1alpha1.RiskSignalList
	if err := client.List(context.Background(), &signals, crclient.InNamespace("prod")); err != nil {
		t.Fatalf("list risk signals: %v", err)
	}
	if len(signals.Items) != 0 {
		t.Fatalf("expected no risk signals, got %d", len(signals.Items))
	}
	var storedRule v1alpha1.RiskRule
	if err := client.Get(context.Background(), types.NamespacedName{Name: ruleObj.Name, Namespace: ruleObj.Namespace}, &storedRule); err != nil {
		t.Fatalf("get rule: %v", err)
	}
	if cond := findCondition(storedRule.Status.Conditions, conditionQueryTypeSupported); cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "CapabilityMismatch" {
		t.Fatalf("expected QueryTypeSupported false condition, got %#v", cond)
	}
	if cond := findCondition(storedRule.Status.Conditions, conditionReady); cond == nil || cond.Status != metav1.ConditionFalse {
		t.Fatalf("expected Ready false condition, got %#v", cond)
	}
}

func TestRiskRuleReconcilerMarksMissingDatasourceOnRuleAndRiskSignal(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add apps scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}
	now := time.Date(2026, 6, 24, 11, 45, 0, 0, time.UTC)

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "open-api", Namespace: "prod"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "open-api"}},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "open-api"}}},
		},
	}
	ruleObj := &v1alpha1.RiskRule{
		ObjectMeta: metav1.ObjectMeta{Name: "partial-evidence", Namespace: "fluxagent-system"},
		Spec: v1alpha1.RiskRuleSpec{
			TargetSelector: v1alpha1.TargetSelector{
				NamespaceSelector: v1alpha1.NamespaceSelector{MatchNames: []string{"prod"}},
				WorkloadSelector:  v1alpha1.WorkloadSelector{MatchLabels: map[string]string{"app": "open-api"}},
			},
			Signals: []v1alpha1.RiskRuleSignal{
				{
					Name:          "good-events",
					DatasourceRef: v1alpha1.LocalObjectReference{Name: "events-main"},
					QueryType:     "event",
					Reasons:       []string{"BackOff"},
				},
				{
					Name:          "missing-logs",
					DatasourceRef: v1alpha1.LocalObjectReference{Name: "logs-missing"},
					QueryType:     "log",
					QueryTemplate: `{namespace="{{ .namespace }}",app="{{ .labels.app }}"} |= "error"`,
					Threshold:     v1alpha1.RiskThreshold{Operator: "count_gt", Value: 0},
				},
			},
		},
	}
	eventSource := &fakeRuleDataSource{
		name:      "kubernetes-events",
		queryType: domain.QueryTypeEvent,
		result: &datasource.QueryResult{
			Source:    "events-main",
			QueryType: domain.QueryTypeEvent,
			Records:   []map[string]any{{"reason": "BackOff", "message": "BackOff restarting failed container"}},
		},
	}
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RiskRule{}, &v1alpha1.RiskSignal{}).
		WithObjects(ruleObj, deployment).
		Build()
	registry := datasource.NewRegistry()
	registry.RegisterNamed("events-main", eventSource)

	reconciler := &RiskRuleReconciler{
		Client:   client,
		Scheme:   scheme,
		Registry: registry,
		Now:      func() time.Time { return now },
	}
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: ruleObj.Name, Namespace: ruleObj.Namespace},
	}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var storedRule v1alpha1.RiskRule
	if err := client.Get(context.Background(), types.NamespacedName{Name: ruleObj.Name, Namespace: ruleObj.Namespace}, &storedRule); err != nil {
		t.Fatalf("get rule: %v", err)
	}
	if cond := findCondition(storedRule.Status.Conditions, conditionDatasourceResolved); cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "DataSourceNotFound" {
		t.Fatalf("expected DatasourceResolved false condition, got %#v", cond)
	}

	var signals v1alpha1.RiskSignalList
	if err := client.List(context.Background(), &signals, crclient.InNamespace("prod")); err != nil {
		t.Fatalf("list signals: %v", err)
	}
	if len(signals.Items) != 1 {
		t.Fatalf("expected one risk signal, got %d", len(signals.Items))
	}
	if cond := findCondition(signals.Items[0].Status.Conditions, conditionEvidenceReady); cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "DataSourceNotFound" {
		t.Fatalf("expected EvidenceCollectionReady false condition, got %#v", cond)
	}
}

func TestRiskRuleReconcilerUsesReferencedHeuristicModelProvider(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add apps scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "payments-api",
			Namespace: "prod",
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "payments-api"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "payments-api"},
				},
			},
		},
	}
	ruleObj := &v1alpha1.RiskRule{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
			Kind:       "RiskRule",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "payments-oom",
			Namespace: "prod",
		},
		Spec: v1alpha1.RiskRuleSpec{
			Severity: "high",
			TargetSelector: v1alpha1.TargetSelector{
				NamespaceSelector: v1alpha1.NamespaceSelector{MatchNames: []string{"prod"}},
				WorkloadSelector: v1alpha1.WorkloadSelector{
					MatchLabels: map[string]string{"app": "payments-api"},
					Kinds:       []string{"Deployment"},
				},
			},
			Signals: []v1alpha1.RiskRuleSignal{
				{
					Name:      "unhealthy-events",
					Type:      "kubernetesEvent",
					Reasons:   []string{"BackOff"},
					Threshold: v1alpha1.RiskThreshold{Operator: "count_gt", Value: 0},
				},
			},
			AI: v1alpha1.RiskRuleAI{
				RCAEnabled:  true,
				ProviderRef: v1alpha1.LocalObjectReference{Name: "heuristic-provider"},
			},
		},
	}
	provider := &v1alpha1.ModelProvider{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
			Kind:       "ModelProvider",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "heuristic-provider",
			Namespace: "prod",
		},
		Spec: v1alpha1.ModelProviderSpec{
			Provider: "heuristic",
			Model:    "built-in",
		},
	}
	eventSource := &fakeRuleDataSource{
		name:      "kubernetes-events",
		queryType: domain.QueryTypeEvent,
		result: &datasource.QueryResult{
			Source:    "kubernetes-events",
			QueryType: domain.QueryTypeEvent,
			Records: []map[string]any{
				{"reason": "BackOff", "message": "Pod entered OOMKilled after rollout"},
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RiskRule{}, &v1alpha1.RiskSignal{}).
		WithObjects(ruleObj, deployment, provider).
		Build()
	reconciler := &RiskRuleReconciler{
		Client:   client,
		Scheme:   scheme,
		Registry: datasource.NewRegistry(eventSource),
		Resolver: modelgateway.KubeResolver{Client: client},
		Gateway: &modelgateway.Gateway{
			Base:      knowledge.NewBase(),
			Providers: model.NewRegistry(heuristic.Provider{}),
		},
		Now: func() time.Time { return now },
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: ruleObj.Name, Namespace: ruleObj.Namespace},
	}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var signals v1alpha1.RiskSignalList
	if err := client.List(context.Background(), &signals, crclient.InNamespace("prod")); err != nil {
		t.Fatalf("list risk signals: %v", err)
	}
	if len(signals.Items) != 1 {
		t.Fatalf("expected one risk signal, got %d", len(signals.Items))
	}
	riskSignal := signals.Items[0]
	if riskSignal.Status.RCAProvider != "heuristic-provider" {
		t.Fatalf("expected referenced model provider name, got %s", riskSignal.Status.RCAProvider)
	}
	if riskSignal.Status.RCASummary == "" || riskSignal.Status.RCAHypothesis == "" {
		t.Fatalf("expected RCA fields to be populated: %#v", riskSignal.Status)
	}
	if cond := findCondition(riskSignal.Status.Conditions, conditionRCAReady); cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("expected RCAReady true condition, got %#v", cond)
	}
}

func TestRiskRuleReconcilerFallsBackToDefaultHeuristicWhenProviderRefMissing(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add apps scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}
	now := time.Date(2026, 6, 24, 13, 0, 0, 0, time.UTC)

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-api", Namespace: "prod"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "payments-api"}},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "payments-api"}}},
		},
	}
	ruleObj := &v1alpha1.RiskRule{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-default-heuristic", Namespace: "prod"},
		Spec: v1alpha1.RiskRuleSpec{
			TargetSelector: v1alpha1.TargetSelector{
				NamespaceSelector: v1alpha1.NamespaceSelector{MatchNames: []string{"prod"}},
				WorkloadSelector:  v1alpha1.WorkloadSelector{MatchLabels: map[string]string{"app": "payments-api"}},
			},
			Signals: []v1alpha1.RiskRuleSignal{{Name: "unhealthy-events", Type: "kubernetesEvent", Reasons: []string{"BackOff"}}},
			AI:      v1alpha1.RiskRuleAI{RCAEnabled: true},
		},
	}
	eventSource := &fakeRuleDataSource{
		name:      "kubernetes-events",
		queryType: domain.QueryTypeEvent,
		result: &datasource.QueryResult{
			Source:    "kubernetes-events",
			QueryType: domain.QueryTypeEvent,
			Records:   []map[string]any{{"reason": "BackOff", "message": "BackOff restarting failed container"}},
		},
	}
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RiskRule{}, &v1alpha1.RiskSignal{}).
		WithObjects(ruleObj, deployment).
		Build()
	reconciler := &RiskRuleReconciler{
		Client:   client,
		Scheme:   scheme,
		Registry: datasource.NewRegistry(eventSource),
		Resolver: modelgateway.KubeResolver{Client: client},
		Gateway: &modelgateway.Gateway{
			Base:      knowledge.NewBase(),
			Providers: model.NewRegistry(heuristic.Provider{}),
		},
		Now: func() time.Time { return now },
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: ruleObj.Name, Namespace: ruleObj.Namespace},
	}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	var signals v1alpha1.RiskSignalList
	if err := client.List(context.Background(), &signals, crclient.InNamespace("prod")); err != nil {
		t.Fatalf("list risk signals: %v", err)
	}
	riskSignal := signals.Items[0]
	if riskSignal.Status.RCAProvider != "default-heuristic" {
		t.Fatalf("expected default heuristic provider, got %s", riskSignal.Status.RCAProvider)
	}
}

func TestRiskRuleReconcilerMarksRCAConditionFalseWhenProviderMissing(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add apps scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}
	now := time.Date(2026, 6, 24, 14, 0, 0, 0, time.UTC)

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-api", Namespace: "prod"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "payments-api"}},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "payments-api"}}},
		},
	}
	ruleObj := &v1alpha1.RiskRule{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-missing-provider", Namespace: "prod"},
		Spec: v1alpha1.RiskRuleSpec{
			TargetSelector: v1alpha1.TargetSelector{
				NamespaceSelector: v1alpha1.NamespaceSelector{MatchNames: []string{"prod"}},
				WorkloadSelector:  v1alpha1.WorkloadSelector{MatchLabels: map[string]string{"app": "payments-api"}},
			},
			Signals: []v1alpha1.RiskRuleSignal{{Name: "unhealthy-events", Type: "kubernetesEvent", Reasons: []string{"BackOff"}}},
			AI: v1alpha1.RiskRuleAI{
				RCAEnabled:  true,
				ProviderRef: v1alpha1.LocalObjectReference{Name: "missing-provider"},
			},
		},
	}
	eventSource := &fakeRuleDataSource{
		name:      "kubernetes-events",
		queryType: domain.QueryTypeEvent,
		result: &datasource.QueryResult{
			Source:    "kubernetes-events",
			QueryType: domain.QueryTypeEvent,
			Records:   []map[string]any{{"reason": "BackOff", "message": "BackOff restarting failed container"}},
		},
	}
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RiskRule{}, &v1alpha1.RiskSignal{}).
		WithObjects(ruleObj, deployment).
		Build()
	reconciler := &RiskRuleReconciler{
		Client:   client,
		Scheme:   scheme,
		Registry: datasource.NewRegistry(eventSource),
		Resolver: modelgateway.KubeResolver{Client: client},
		Gateway: &modelgateway.Gateway{
			Base:      knowledge.NewBase(),
			Providers: model.NewRegistry(heuristic.Provider{}),
		},
		Now: func() time.Time { return now },
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: ruleObj.Name, Namespace: ruleObj.Namespace},
	}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	var signals v1alpha1.RiskSignalList
	if err := client.List(context.Background(), &signals, crclient.InNamespace("prod")); err != nil {
		t.Fatalf("list risk signals: %v", err)
	}
	riskSignal := signals.Items[0]
	if riskSignal.Status.RCAProvider != "" {
		t.Fatalf("expected no RCA provider when lookup fails, got %s", riskSignal.Status.RCAProvider)
	}
	cond := findCondition(riskSignal.Status.Conditions, conditionRCAReady)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "ProviderNotFound" {
		t.Fatalf("expected ProviderNotFound false condition, got %#v", cond)
	}
}

func TestRiskRuleReconcilerUsesReferencedLocalModelProvider(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add apps scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	now := time.Date(2026, 6, 28, 10, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var request struct {
			Model   string              `json:"model"`
			Request domain.ModelRequest `json:"request"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Model != "llama3.1:8b" {
			t.Fatalf("expected model from ModelProvider spec, got %q", request.Model)
		}
		if request.Request.Context["evidence"] == nil {
			t.Fatalf("expected evidence context in local provider request")
		}
		if err := json.NewEncoder(w).Encode(domain.ModelResponse{
			Provider:   "local",
			Model:      request.Model,
			Structured: true,
			Output: map[string]any{
				"riskTitle":       "Rollout regression",
				"riskSummary":     "Local provider correlated crash loops with rollout timing.",
				"severity":        "high",
				"confidenceScore": 91,
				"rationale":       "local endpoint reasoning",
				"rcaHypothesis":   "The latest image introduced unstable startup behavior.",
				"rcaCauses":       []string{"image regression", "startup failure"},
				"actionType":      "notification.sendSlack",
			},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-api", Namespace: "prod"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "payments-api"}},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "payments-api"}}},
		},
	}
	ruleObj := &v1alpha1.RiskRule{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-local-provider", Namespace: "prod"},
		Spec: v1alpha1.RiskRuleSpec{
			Severity: "high",
			TargetSelector: v1alpha1.TargetSelector{
				NamespaceSelector: v1alpha1.NamespaceSelector{MatchNames: []string{"prod"}},
				WorkloadSelector:  v1alpha1.WorkloadSelector{MatchLabels: map[string]string{"app": "payments-api"}},
			},
			Signals: []v1alpha1.RiskRuleSignal{{Name: "unhealthy-events", Type: "kubernetesEvent", Reasons: []string{"BackOff"}}},
			AI: v1alpha1.RiskRuleAI{
				RCAEnabled:  true,
				ProviderRef: v1alpha1.LocalObjectReference{Name: "local-provider"},
			},
		},
	}
	provider := &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "local-provider", Namespace: "prod"},
		Spec: v1alpha1.ModelProviderSpec{
			Provider: "local",
			Model:    "llama3.1:8b",
			Endpoint: server.URL,
			Timeout:  metav1.Duration{Duration: 2 * time.Second},
		},
	}
	eventSource := &fakeRuleDataSource{
		name:      "kubernetes-events",
		queryType: domain.QueryTypeEvent,
		result: &datasource.QueryResult{
			Source:    "kubernetes-events",
			QueryType: domain.QueryTypeEvent,
			Records:   []map[string]any{{"reason": "BackOff", "message": "BackOff restarting failed container"}},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RiskRule{}, &v1alpha1.RiskSignal{}).
		WithObjects(ruleObj, deployment, provider).
		Build()
	reconciler := &RiskRuleReconciler{
		Client:   client,
		Scheme:   scheme,
		Registry: datasource.NewRegistry(eventSource),
		Resolver: modelgateway.KubeResolver{Client: client},
		Gateway: &modelgateway.Gateway{
			Base:      knowledge.NewBase(),
			Providers: model.NewRegistry(heuristic.Provider{}, local.Provider{Client: server.Client()}),
		},
		Now: func() time.Time { return now },
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: ruleObj.Name, Namespace: ruleObj.Namespace},
	}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	var signals v1alpha1.RiskSignalList
	if err := client.List(context.Background(), &signals, crclient.InNamespace("prod")); err != nil {
		t.Fatalf("list risk signals: %v", err)
	}
	if len(signals.Items) != 1 {
		t.Fatalf("expected one risk signal, got %d", len(signals.Items))
	}
	riskSignal := signals.Items[0]
	if riskSignal.Status.RCAProvider != "local-provider" {
		t.Fatalf("expected local provider name, got %s", riskSignal.Status.RCAProvider)
	}
	if riskSignal.Status.RCASummary != "Local provider correlated crash loops with rollout timing." {
		t.Fatalf("expected local provider RCA summary, got %q", riskSignal.Status.RCASummary)
	}
	if cond := findCondition(riskSignal.Status.Conditions, conditionRCAReady); cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("expected RCAReady true condition, got %#v", cond)
	}
}

func TestRiskRuleReconcilerUsesReferencedOpenAIModelProvider(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add apps scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add core scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	now := time.Date(2026, 6, 29, 9, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer openai-token" {
			t.Fatalf("expected openai auth header, got %q", got)
		}
		if err := json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"content": `{"riskTitle":"Crash loop after rollout","riskSummary":"OpenAI correlated crash loops with the latest rollout.","severity":"high","confidenceScore":93,"rationale":"events and logs aligned after deploy","rcaHypothesis":"The new image introduced unstable startup behavior.","rcaCauses":["image regression","startup failure"],"actionType":"notification.sendSlack"}`,
					},
				},
			},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-api", Namespace: "prod"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "payments-api"}},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "payments-api"}}},
		},
	}
	ruleObj := &v1alpha1.RiskRule{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-openai-provider", Namespace: "fluxagent-system"},
		Spec: v1alpha1.RiskRuleSpec{
			Severity: "high",
			TargetSelector: v1alpha1.TargetSelector{
				NamespaceSelector: v1alpha1.NamespaceSelector{MatchNames: []string{"prod"}},
				WorkloadSelector:  v1alpha1.WorkloadSelector{MatchLabels: map[string]string{"app": "payments-api"}},
			},
			Signals: []v1alpha1.RiskRuleSignal{{Name: "unhealthy-events", Type: "kubernetesEvent", Reasons: []string{"BackOff"}}},
			AI: v1alpha1.RiskRuleAI{
				RCAEnabled:  true,
				ProviderRef: v1alpha1.LocalObjectReference{Name: "openai-provider"},
			},
		},
	}
	provider := &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "openai-provider", Namespace: "fluxagent-system"},
		Spec: v1alpha1.ModelProviderSpec{
			Provider: "openai",
			Model:    "gpt-5.1",
			Endpoint: server.URL,
			Timeout:  metav1.Duration{Duration: 2 * time.Second},
			APIKeySecretRef: &v1alpha1.SecretKeyRef{
				Name: "openai-secret",
				Key:  "api-key",
			},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "openai-secret", Namespace: "fluxagent-system"},
		Data:       map[string][]byte{"api-key": []byte("openai-token")},
	}
	eventSource := &fakeRuleDataSource{
		name:      "kubernetes-events",
		queryType: domain.QueryTypeEvent,
		result: &datasource.QueryResult{
			Source:    "kubernetes-events",
			QueryType: domain.QueryTypeEvent,
			Records:   []map[string]any{{"reason": "BackOff", "message": "BackOff restarting failed container"}},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RiskRule{}, &v1alpha1.RiskSignal{}).
		WithObjects(ruleObj, deployment, provider, secret).
		Build()
	reconciler := &RiskRuleReconciler{
		Client:   client,
		Scheme:   scheme,
		Registry: datasource.NewRegistry(eventSource),
		Resolver: modelgateway.KubeResolver{Client: client},
		Gateway: &modelgateway.Gateway{
			Base:      knowledge.NewBase(),
			Providers: model.NewRegistry(openai.Provider{}),
			Secrets:   modelgateway.KubeSecretResolver{Client: client},
		},
		Now: func() time.Time { return now },
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: ruleObj.Name, Namespace: ruleObj.Namespace},
	}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	var signals v1alpha1.RiskSignalList
	if err := client.List(context.Background(), &signals, crclient.InNamespace("prod")); err != nil {
		t.Fatalf("list risk signals: %v", err)
	}
	riskSignal := signals.Items[0]
	if riskSignal.Status.RCAProvider != "openai-provider" {
		t.Fatalf("expected openai provider name, got %s", riskSignal.Status.RCAProvider)
	}
	if riskSignal.Status.RCASummary != "OpenAI correlated crash loops with the latest rollout." {
		t.Fatalf("unexpected RCA summary: %q", riskSignal.Status.RCASummary)
	}
}

func TestRiskRuleReconcilerMarksRCAConditionFalseWhenProviderAuthFails(t *testing.T) {
	testRiskRuleReconcilerHostedProviderFailure(t, http.StatusUnauthorized, nil, "ProviderAuthFailed")
}

func TestRiskRuleReconcilerMarksRCAConditionFalseWhenProviderRateLimited(t *testing.T) {
	testRiskRuleReconcilerHostedProviderFailure(t, http.StatusTooManyRequests, map[string]string{"Retry-After": "1"}, "ProviderRateLimited")
}

func testRiskRuleReconcilerHostedProviderFailure(t *testing.T, statusCode int, headers map[string]string, expectedReason string) {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add apps scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add core scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	now := time.Date(2026, 6, 29, 9, 30, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer openai-token" {
			t.Fatalf("expected openai auth header, got %q", got)
		}
		for key, value := range headers {
			w.Header().Set(key, value)
		}
		w.WriteHeader(statusCode)
		if err := json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": http.StatusText(statusCode),
			},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-api", Namespace: "prod"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "payments-api"}},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "payments-api"}}},
		},
	}
	ruleObj := &v1alpha1.RiskRule{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-openai-hosted-failure", Namespace: "fluxagent-system"},
		Spec: v1alpha1.RiskRuleSpec{
			TargetSelector: v1alpha1.TargetSelector{
				NamespaceSelector: v1alpha1.NamespaceSelector{MatchNames: []string{"prod"}},
				WorkloadSelector:  v1alpha1.WorkloadSelector{MatchLabels: map[string]string{"app": "payments-api"}},
			},
			Signals: []v1alpha1.RiskRuleSignal{{Name: "unhealthy-events", Type: "kubernetesEvent", Reasons: []string{"BackOff"}}},
			AI: v1alpha1.RiskRuleAI{
				RCAEnabled:  true,
				ProviderRef: v1alpha1.LocalObjectReference{Name: "openai-provider"},
			},
		},
	}
	provider := &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "openai-provider", Namespace: "fluxagent-system"},
		Spec: v1alpha1.ModelProviderSpec{
			Provider: "openai",
			Model:    "gpt-5.1",
			Endpoint: server.URL,
			Timeout:  metav1.Duration{Duration: 2 * time.Second},
			APIKeySecretRef: &v1alpha1.SecretKeyRef{
				Name: "openai-secret",
				Key:  "api-key",
			},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "openai-secret", Namespace: "fluxagent-system"},
		Data:       map[string][]byte{"api-key": []byte("openai-token")},
	}
	eventSource := &fakeRuleDataSource{
		name:      "kubernetes-events",
		queryType: domain.QueryTypeEvent,
		result: &datasource.QueryResult{
			Source:    "kubernetes-events",
			QueryType: domain.QueryTypeEvent,
			Records:   []map[string]any{{"reason": "BackOff", "message": "BackOff restarting failed container"}},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RiskRule{}, &v1alpha1.RiskSignal{}).
		WithObjects(ruleObj, deployment, provider, secret).
		Build()
	reconciler := &RiskRuleReconciler{
		Client:   client,
		Scheme:   scheme,
		Registry: datasource.NewRegistry(eventSource),
		Resolver: modelgateway.KubeResolver{Client: client},
		Gateway: &modelgateway.Gateway{
			Base:      knowledge.NewBase(),
			Providers: model.NewRegistry(openai.Provider{}),
			Secrets:   modelgateway.KubeSecretResolver{Client: client},
		},
		Now: func() time.Time { return now },
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: ruleObj.Name, Namespace: ruleObj.Namespace},
	}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	var signals v1alpha1.RiskSignalList
	if err := client.List(context.Background(), &signals, crclient.InNamespace("prod")); err != nil {
		t.Fatalf("list risk signals: %v", err)
	}
	if len(signals.Items) != 1 {
		t.Fatalf("expected one risk signal, got %d", len(signals.Items))
	}
	riskSignal := signals.Items[0]
	if cond := findCondition(riskSignal.Status.Conditions, conditionEvidenceReady); cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("expected EvidenceCollectionReady true condition, got %#v", cond)
	}
	cond := findCondition(riskSignal.Status.Conditions, conditionRCAReady)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != expectedReason {
		t.Fatalf("expected %s RCA false condition, got %#v", expectedReason, cond)
	}
}

func TestRiskRuleReconcilerMarksRCAConditionFalseWhenProviderSecretMissing(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add apps scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add core scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}
	now := time.Date(2026, 6, 29, 10, 0, 0, 0, time.UTC)

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-api", Namespace: "prod"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "payments-api"}},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "payments-api"}}},
		},
	}
	ruleObj := &v1alpha1.RiskRule{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-openai-missing-secret", Namespace: "fluxagent-system"},
		Spec: v1alpha1.RiskRuleSpec{
			TargetSelector: v1alpha1.TargetSelector{
				NamespaceSelector: v1alpha1.NamespaceSelector{MatchNames: []string{"prod"}},
				WorkloadSelector:  v1alpha1.WorkloadSelector{MatchLabels: map[string]string{"app": "payments-api"}},
			},
			Signals: []v1alpha1.RiskRuleSignal{{Name: "unhealthy-events", Type: "kubernetesEvent", Reasons: []string{"BackOff"}}},
			AI: v1alpha1.RiskRuleAI{
				RCAEnabled:  true,
				ProviderRef: v1alpha1.LocalObjectReference{Name: "openai-provider"},
			},
		},
	}
	provider := &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "openai-provider", Namespace: "fluxagent-system"},
		Spec: v1alpha1.ModelProviderSpec{
			Provider: "openai",
			Model:    "gpt-5.1",
			APIKeySecretRef: &v1alpha1.SecretKeyRef{
				Name: "missing-secret",
				Key:  "api-key",
			},
		},
	}
	eventSource := &fakeRuleDataSource{
		name:      "kubernetes-events",
		queryType: domain.QueryTypeEvent,
		result: &datasource.QueryResult{
			Source:    "kubernetes-events",
			QueryType: domain.QueryTypeEvent,
			Records:   []map[string]any{{"reason": "BackOff", "message": "BackOff restarting failed container"}},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RiskRule{}, &v1alpha1.RiskSignal{}).
		WithObjects(ruleObj, deployment, provider).
		Build()
	reconciler := &RiskRuleReconciler{
		Client:   client,
		Scheme:   scheme,
		Registry: datasource.NewRegistry(eventSource),
		Resolver: modelgateway.KubeResolver{Client: client},
		Gateway: &modelgateway.Gateway{
			Base:      knowledge.NewBase(),
			Providers: model.NewRegistry(openai.Provider{}),
			Secrets:   modelgateway.KubeSecretResolver{Client: client},
		},
		Now: func() time.Time { return now },
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: ruleObj.Name, Namespace: ruleObj.Namespace},
	}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	var signals v1alpha1.RiskSignalList
	if err := client.List(context.Background(), &signals, crclient.InNamespace("prod")); err != nil {
		t.Fatalf("list risk signals: %v", err)
	}
	cond := findCondition(signals.Items[0].Status.Conditions, conditionRCAReady)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "SecretNotFound" {
		t.Fatalf("expected SecretNotFound RCA false condition, got %#v", cond)
	}
}

func TestRiskRuleReconcilerMarksRCAConditionFalseWhenProviderResponseInvalid(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add apps scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add core scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}
	now := time.Date(2026, 6, 29, 11, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{"content": `{"riskSummary":""}`},
				},
			},
		})
	}))
	defer server.Close()

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-api", Namespace: "prod"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "payments-api"}},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "payments-api"}}},
		},
	}
	ruleObj := &v1alpha1.RiskRule{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-openai-invalid-response", Namespace: "fluxagent-system"},
		Spec: v1alpha1.RiskRuleSpec{
			TargetSelector: v1alpha1.TargetSelector{
				NamespaceSelector: v1alpha1.NamespaceSelector{MatchNames: []string{"prod"}},
				WorkloadSelector:  v1alpha1.WorkloadSelector{MatchLabels: map[string]string{"app": "payments-api"}},
			},
			Signals: []v1alpha1.RiskRuleSignal{{Name: "unhealthy-events", Type: "kubernetesEvent", Reasons: []string{"BackOff"}}},
			AI: v1alpha1.RiskRuleAI{
				RCAEnabled:  true,
				ProviderRef: v1alpha1.LocalObjectReference{Name: "openai-provider"},
			},
		},
	}
	provider := &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "openai-provider", Namespace: "fluxagent-system"},
		Spec: v1alpha1.ModelProviderSpec{
			Provider: "openai",
			Model:    "gpt-5.1",
			Endpoint: server.URL,
			APIKeySecretRef: &v1alpha1.SecretKeyRef{
				Name: "openai-secret",
				Key:  "api-key",
			},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "openai-secret", Namespace: "fluxagent-system"},
		Data:       map[string][]byte{"api-key": []byte("openai-token")},
	}
	eventSource := &fakeRuleDataSource{
		name:      "kubernetes-events",
		queryType: domain.QueryTypeEvent,
		result: &datasource.QueryResult{
			Source:    "kubernetes-events",
			QueryType: domain.QueryTypeEvent,
			Records:   []map[string]any{{"reason": "BackOff", "message": "BackOff restarting failed container"}},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RiskRule{}, &v1alpha1.RiskSignal{}).
		WithObjects(ruleObj, deployment, provider, secret).
		Build()
	reconciler := &RiskRuleReconciler{
		Client:   client,
		Scheme:   scheme,
		Registry: datasource.NewRegistry(eventSource),
		Resolver: modelgateway.KubeResolver{Client: client},
		Gateway: &modelgateway.Gateway{
			Base:      knowledge.NewBase(),
			Providers: model.NewRegistry(openai.Provider{}),
			Secrets:   modelgateway.KubeSecretResolver{Client: client},
		},
		Now: func() time.Time { return now },
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: ruleObj.Name, Namespace: ruleObj.Namespace},
	}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	var signals v1alpha1.RiskSignalList
	if err := client.List(context.Background(), &signals, crclient.InNamespace("prod")); err != nil {
		t.Fatalf("list risk signals: %v", err)
	}
	cond := findCondition(signals.Items[0].Status.Conditions, conditionRCAReady)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "InvalidProviderResponse" {
		t.Fatalf("expected InvalidProviderResponse RCA false condition, got %#v", cond)
	}
}

func TestRiskSignalNotificationIncludesRiskRuleMetadata(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	riskSignal := &v1alpha1.RiskSignal{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
			Kind:       "RiskSignal",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "latency-regression-open-api-risk",
			Namespace: "prod",
			Labels: map[string]string{
				labelRiskRule: "latency-regression",
			},
			Annotations: map[string]string{
				annotationDetectionSource: "risk-rule",
			},
		},
		Spec: v1alpha1.RiskSignalSpec{
			Target: v1alpha1.TargetRef{
				Namespace: "prod",
				Name:      "open-api",
				Kind:      "Deployment",
			},
			SignalType: "prometheus",
			Severity:   "medium",
			Confidence: 69,
			Evidence: []v1alpha1.EvidenceRef{
				{Kind: "metric", Source: "prometheus", Summary: "metric value 2.10 matched > 1.50"},
			},
		},
		Status: v1alpha1.RiskSignalStatus{
			ResourceStatus: v1alpha1.ResourceStatus{
				Phase:   v1alpha1.PhaseConfirmed,
				Message: "p95-latency crossed threshold for open-api",
			},
			RCASummary:  "A recent rollout saturated the upstream retry path.",
			RCAProvider: "heuristic-provider",
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RiskSignal{}).
		WithObjects(riskSignal).
		Build()
	notifier := &fakeNotifier{}

	reconciler := &RiskSignalNotificationReconciler{
		Client:   client,
		Scheme:   scheme,
		Notifier: notifier,
		Now:      func() time.Time { return time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC) },
	}
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: riskSignal.Name, Namespace: riskSignal.Namespace},
	}); err != nil {
		t.Fatalf("unexpected notify error: %v", err)
	}

	if notifier.lastMessage.Fields["riskRule"] != "latency-regression" {
		t.Fatalf("expected riskRule field, got %#v", notifier.lastMessage.Fields["riskRule"])
	}
	if notifier.lastMessage.Fields["origin"] != "risk-rule" {
		t.Fatalf("expected origin risk-rule, got %#v", notifier.lastMessage.Fields["origin"])
	}
	if !strings.Contains(notifier.lastMessage.Body, "Rule: latency-regression") {
		t.Fatalf("expected rule line in notification body, got %q", notifier.lastMessage.Body)
	}
	if !strings.Contains(notifier.lastMessage.Body, "RCA Summary: A recent rollout saturated the upstream retry path.") {
		t.Fatalf("expected RCA summary in notification body, got %q", notifier.lastMessage.Body)
	}
	if notifier.lastMessage.Fields["rcaProvider"] != "heuristic-provider" {
		t.Fatalf("expected RCA provider field, got %#v", notifier.lastMessage.Fields["rcaProvider"])
	}
}

func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}
