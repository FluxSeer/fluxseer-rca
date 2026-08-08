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
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/datasource"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
	"github.com/FluxSeer/fluxseer-rca/internal/knowledge"
	"github.com/FluxSeer/fluxseer-rca/internal/model"
	"github.com/FluxSeer/fluxseer-rca/internal/model/heuristic"
	"github.com/FluxSeer/fluxseer-rca/internal/model/openai"
	"github.com/FluxSeer/fluxseer-rca/internal/modelgateway"
	"github.com/FluxSeer/fluxseer-rca/internal/rule"
)

type fakeRuleDataSource struct {
	name        string
	queryType   domain.QueryType
	result      *datasource.QueryResult
	queryErr    error
	requests    []datasource.QueryRequest
	queryPolicy v1alpha1.DataSourceQueryPolicy
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
func (f *fakeRuleDataSource) HealthCheck(context.Context) error           { return nil }
func (f *fakeRuleDataSource) QueryPolicy() v1alpha1.DataSourceQueryPolicy { return f.queryPolicy }
func (f *fakeRuleDataSource) Query(_ context.Context, req datasource.QueryRequest) (*datasource.QueryResult, error) {
	f.requests = append(f.requests, req)
	if f.queryErr != nil {
		return nil, f.queryErr
	}
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
			Namespace:  "fluxseer-rca-system",
			Generation: 1,
		},
		Spec: v1alpha1.RiskRuleSpec{
			InvestigationPolicy: v1alpha1.RiskRuleInvestigationPolicy{Mode: v1alpha1.RiskRuleInvestigationModeDirectRiskSignal},
			Interval:            metav1.Duration{Duration: 2 * time.Minute},
			Severity:            "warning",
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
			Namespace:  "fluxseer-rca-system",
			Generation: 2,
		},
		Spec: v1alpha1.RiskRuleSpec{
			InvestigationPolicy: v1alpha1.RiskRuleInvestigationPolicy{Mode: v1alpha1.RiskRuleInvestigationModeDirectRiskSignal},
			Interval:            metav1.Duration{Duration: 2 * time.Minute},
			Window:              metav1.Duration{Duration: 10 * time.Minute},
			Severity:            "warning",
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

func TestRiskRuleReconcilerDeduplicatesSameEventAcrossWindowBuckets(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add apps scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	now := time.Date(2026, 8, 3, 2, 28, 24, 0, time.UTC)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "codex-wq-deploy-102521",
			Namespace:  "database-test",
			UID:        types.UID("deployment-uid"),
			Generation: 1,
			Labels:     map[string]string{"app": "codex-wq-deploy-102521"},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "codex-wq-deploy-102521"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "codex-wq-deploy-102521"}},
			},
		},
	}
	ruleObj := &v1alpha1.RiskRule{
		TypeMeta: metav1.TypeMeta{APIVersion: v1alpha1.SchemeGroupVersion.String(), Kind: "RiskRule"},
		ObjectMeta: metav1.ObjectMeta{
			Name:       "fluxseer-rca-kubernetes-baseline",
			Namespace:  "fluxseer-rca-test",
			UID:        types.UID("riskrule-uid"),
			Generation: 1,
		},
		Spec: v1alpha1.RiskRuleSpec{
			InvestigationPolicy: v1alpha1.RiskRuleInvestigationPolicy{Mode: v1alpha1.RiskRuleInvestigationModeDirectRiskSignal},
			Interval:            metav1.Duration{Duration: 2 * time.Minute},
			Window:              metav1.Duration{Duration: 10 * time.Minute},
			Severity:            "warning",
			TargetSelector: v1alpha1.TargetSelector{
				NamespaceSelector: v1alpha1.NamespaceSelector{MatchNames: []string{"database-test"}},
				WorkloadSelector:  v1alpha1.WorkloadSelector{Kinds: []string{"Deployment"}},
			},
			Signals: []v1alpha1.RiskRuleSignal{
				{
					Name:          "restart-backoff",
					DatasourceRef: v1alpha1.LocalObjectReference{Name: "events-main"},
					QueryType:     "event",
					Reasons:       []string{"BackOff"},
					Threshold:     v1alpha1.RiskThreshold{Operator: "count_gt", Value: 0},
				},
			},
		},
	}
	eventSource := &fakeRuleDataSource{
		name:      "events-main",
		queryType: domain.QueryTypeEvent,
		result: &datasource.QueryResult{
			Source:    "events-main",
			QueryType: domain.QueryTypeEvent,
			Records: []map[string]any{{
				"eventUID":           "event-uid-1",
				"involvedObjectUID":  "pod-uid-1",
				"involvedObjectKind": "Pod",
				"involvedObjectName": "codex-wq-deploy-102521-66695bbbdd-9c8x6",
				"reason":             "BackOff",
				"message":            "codex workload qualification clean: Deployment pod BackOff event",
				"firstTimestamp":     "2026-08-03T02:27:00Z",
				"reportingComponent": "codex-workload-qualification",
			}},
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
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: ruleObj.Name, Namespace: ruleObj.Namespace}}
	if _, err := reconciler.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}
	now = time.Date(2026, 8, 3, 2, 30, 24, 0, time.UTC)
	if _, err := reconciler.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	var signals v1alpha1.RiskSignalList
	if err := client.List(context.Background(), &signals, crclient.InNamespace("database-test")); err != nil {
		t.Fatalf("list risk signals: %v", err)
	}
	if len(signals.Items) != 1 {
		t.Fatalf("expected same event across buckets to update one RiskSignal, got %d", len(signals.Items))
	}
	identity := signals.Items[0].Spec.FindingIdentity
	if identity == nil {
		t.Fatal("expected finding identity")
	}
	if identity.WindowBucket != "1785724200" {
		t.Fatalf("expected latest evaluation window bucket to be recorded as metadata, got %s", identity.WindowBucket)
	}
	if signals.Items[0].Annotations[annotationWindowBucket] != "" {
		t.Fatalf("direct RiskSignal should not use window bucket annotation")
	}
}

func TestRiskRuleReconcilerRoutesMatchesToInvestigationRequest(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add apps scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	now := time.Date(2026, 7, 28, 8, 30, 0, 0, time.UTC)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "open-api",
			Namespace: "prod",
			UID:       types.UID("deployment-open-api-uid"),
			Labels:    map[string]string{"app": "open-api"},
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
			Namespace:  "fluxseer-rca-test",
			UID:        types.UID("riskrule-latency-uid"),
			Generation: 4,
		},
		Spec: v1alpha1.RiskRuleSpec{
			Interval: metav1.Duration{Duration: 2 * time.Minute},
			Window:   metav1.Duration{Duration: 10 * time.Minute},
			Severity: "warning",
			TargetSelector: v1alpha1.TargetSelector{
				NamespaceSelector: v1alpha1.NamespaceSelector{MatchNames: []string{"prod"}},
				WorkloadSelector: v1alpha1.WorkloadSelector{
					MatchLabels: map[string]string{"app": "open-api"},
					Kinds:       []string{"Deployment"},
				},
			},
			Signals: []v1alpha1.RiskRuleSignal{
				{
					Name:          "p95-latency",
					DatasourceRef: v1alpha1.LocalObjectReference{Name: "prometheus-main"},
					QueryType:     "metric",
					QueryTemplate: `sum(rate(http_requests_total{namespace="{{ .namespace }}",app="{{ .app }}"}[5m]))`,
					Threshold: v1alpha1.RiskThreshold{
						Operator: ">",
						Value:    1.5,
					},
				},
			},
			AI: v1alpha1.RiskRuleAI{
				ProviderRef: v1alpha1.LocalObjectReference{Name: "heuristic-provider"},
			},
			InvestigationPolicy: v1alpha1.RiskRuleInvestigationPolicy{
				Mode:             v1alpha1.RiskRuleInvestigationModeCreateRequest,
				CreateRiskSignal: true,
			},
		},
	}

	promSource := &fakeRuleDataSource{
		name:      "prometheus-main",
		queryType: domain.QueryTypeMetric,
		result: &datasource.QueryResult{
			Source:    "prometheus-main",
			QueryType: domain.QueryTypeMetric,
			Records:   []map[string]any{{"value": "2.1"}},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RiskRule{}, &v1alpha1.RiskSignal{}, &v1alpha1.InvestigationRequest{}).
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

	var requests v1alpha1.InvestigationRequestList
	if err := client.List(context.Background(), &requests); err != nil {
		t.Fatalf("failed to list investigation requests: %v", err)
	}
	if len(requests.Items) != 1 {
		t.Fatalf("expected 1 investigation request, got %d", len(requests.Items))
	}
	investigation := requests.Items[0]
	if investigation.Namespace != "fluxseer-rca-test" {
		t.Fatalf("expected investigation request in rule namespace, got %s", investigation.Namespace)
	}
	if investigation.Spec.Target.Namespace != "prod" || investigation.Spec.Target.Name != "open-api" {
		t.Fatalf("expected target prod/open-api, got %#v", investigation.Spec.Target)
	}
	if len(investigation.Spec.Queries) != 1 || investigation.Spec.Queries[0].DatasourceRef.Name != "prometheus-main" {
		t.Fatalf("expected one routed investigation query, got %#v", investigation.Spec.Queries)
	}
	if investigation.Spec.Queries[0].Query != `sum(rate(http_requests_total{namespace="prod",app="open-api"}[5m]))` {
		t.Fatalf("expected rendered investigation query, got %#v", investigation.Spec.Queries[0])
	}
	if !investigation.Spec.CreateRiskSignal {
		t.Fatalf("expected createRiskSignal propagated")
	}
	if investigation.Annotations[annotationLineageSource] != "fluxseer-rca-test/latency-regression" ||
		investigation.Annotations[annotationLineageSourceUID] != "riskrule-latency-uid" ||
		investigation.Annotations[annotationLineageGeneration] != "4" ||
		investigation.Annotations[annotationTargetUID] != "deployment-open-api-uid" ||
		!strings.HasPrefix(investigation.Annotations[annotationFindingFingerprint], "sha256:") ||
		investigation.Annotations[annotationFindingSchema] != findingIdentitySchemaVersion ||
		!strings.HasPrefix(investigation.Annotations[annotationObjectFindingID], "sha256:") ||
		!strings.HasPrefix(investigation.Annotations[annotationLogicalFindingID], "sha256:") ||
		!strings.HasPrefix(investigation.Annotations[annotationIncidentOccurrence], "sha256:") {
		t.Fatalf("expected lineage annotations, got %#v", investigation.Annotations)
	}

	var signals v1alpha1.RiskSignalList
	if err := client.List(context.Background(), &signals); err != nil {
		t.Fatalf("failed to list risk signals: %v", err)
	}
	if len(signals.Items) != 0 {
		t.Fatalf("expected no direct risk signals, got %d", len(signals.Items))
	}

	var storedRule v1alpha1.RiskRule
	if err := client.Get(context.Background(), types.NamespacedName{Name: ruleObj.Name, Namespace: ruleObj.Namespace}, &storedRule); err != nil {
		t.Fatalf("failed to fetch rule: %v", err)
	}
	if storedRule.Status.Message != "processed 1 targets; 1 produced InvestigationRequest" {
		t.Fatalf("unexpected rule status message: %s", storedRule.Status.Message)
	}
}

func TestRiskRuleReconcilerRoutesStatefulSetMatchToInvestigationRequest(t *testing.T) {
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

	now := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)
	statefulSet := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "postgres",
			Namespace: "prod",
			UID:       types.UID("statefulset-postgres-uid"),
			Labels:    map[string]string{"app": "postgres"},
		},
		Spec: appsv1.StatefulSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "postgres"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "postgres"}},
			},
		},
	}
	ruleObj := &v1alpha1.RiskRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "stateful-workload",
			Namespace:  "fluxseer-rca-test",
			UID:        types.UID("riskrule-stateful-uid"),
			Generation: 1,
		},
		Spec: v1alpha1.RiskRuleSpec{
			Window: metav1.Duration{Duration: 10 * time.Minute},
			TargetSelector: v1alpha1.TargetSelector{
				NamespaceSelector: v1alpha1.NamespaceSelector{MatchNames: []string{"prod"}},
				WorkloadSelector: v1alpha1.WorkloadSelector{
					MatchLabels: map[string]string{"app": "postgres"},
					Kinds:       []string{"StatefulSet"},
				},
			},
			Signals: []v1alpha1.RiskRuleSignal{{
				Name:          "memory-pressure",
				DatasourceRef: v1alpha1.LocalObjectReference{Name: "prometheus-main"},
				QueryType:     "metric",
				QueryTemplate: `container_memory_working_set_bytes{namespace="{{ .namespace }}",app="{{ .app }}"}`,
				Threshold:     v1alpha1.RiskThreshold{Operator: ">", Value: 1},
			}},
			InvestigationPolicy: v1alpha1.RiskRuleInvestigationPolicy{Mode: v1alpha1.RiskRuleInvestigationModeCreateRequest},
		},
	}
	promSource := &fakeRuleDataSource{
		name:      "prometheus-main",
		queryType: domain.QueryTypeMetric,
		result: &datasource.QueryResult{
			Source:    "prometheus-main",
			QueryType: domain.QueryTypeMetric,
			Records:   []map[string]any{{"value": "2"}},
		},
	}
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RiskRule{}, &v1alpha1.RiskSignal{}, &v1alpha1.InvestigationRequest{}).
		WithObjects(ruleObj, statefulSet).
		Build()
	reconciler := &RiskRuleReconciler{
		Client:   client,
		Scheme:   scheme,
		Registry: datasource.NewRegistry(promSource),
		Now:      func() time.Time { return now },
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: ruleObj.Name, Namespace: ruleObj.Namespace}}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var requests v1alpha1.InvestigationRequestList
	if err := client.List(context.Background(), &requests); err != nil {
		t.Fatalf("list investigation requests: %v", err)
	}
	if len(requests.Items) != 1 {
		t.Fatalf("expected one investigation request, got %d", len(requests.Items))
	}
	if requests.Items[0].Spec.Target.Kind != "StatefulSet" || requests.Items[0].Spec.Target.Name != "postgres" {
		t.Fatalf("expected StatefulSet/postgres target, got %#v", requests.Items[0].Spec.Target)
	}
	if requests.Items[0].Annotations[annotationTargetUID] != "statefulset-postgres-uid" {
		t.Fatalf("expected statefulset UID lineage, got %#v", requests.Items[0].Annotations)
	}
}

func TestRiskRuleReconcilerRoutesAdditionalWorkloadKindsToInvestigationRequest(t *testing.T) {
	for _, tc := range []struct {
		name     string
		kind     string
		object   crclient.Object
		wantName string
		wantUID  string
	}{
		{
			name: "daemonset",
			kind: "DaemonSet",
			object: &appsv1.DaemonSet{
				ObjectMeta: metav1.ObjectMeta{Name: "node-agent", Namespace: "prod", UID: types.UID("daemonset-node-agent-uid"), Labels: map[string]string{"app": "node-agent"}},
				Spec: appsv1.DaemonSetSpec{
					Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "node-agent"}},
					Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "node-agent"}}},
				},
			},
			wantName: "node-agent",
			wantUID:  "daemonset-node-agent-uid",
		},
		{
			name: "job",
			kind: "Job",
			object: &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: "backup", Namespace: "prod", UID: types.UID("job-backup-uid"), Labels: map[string]string{"app": "backup"}},
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "backup"}}},
				},
			},
			wantName: "backup",
			wantUID:  "job-backup-uid",
		},
		{
			name: "cronjob",
			kind: "CronJob",
			object: &batchv1.CronJob{
				ObjectMeta: metav1.ObjectMeta{Name: "nightly", Namespace: "prod", UID: types.UID("cronjob-nightly-uid"), Labels: map[string]string{"app": "nightly"}},
				Spec: batchv1.CronJobSpec{
					JobTemplate: batchv1.JobTemplateSpec{Spec: batchv1.JobSpec{
						Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "nightly"}}},
					}},
				},
			},
			wantName: "nightly",
			wantUID:  "cronjob-nightly-uid",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := appsv1.AddToScheme(scheme); err != nil {
				t.Fatalf("failed to add apps scheme: %v", err)
			}
			if err := batchv1.AddToScheme(scheme); err != nil {
				t.Fatalf("failed to add batch scheme: %v", err)
			}
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatalf("failed to add core scheme: %v", err)
			}
			if err := v1alpha1.AddToScheme(scheme); err != nil {
				t.Fatalf("failed to add scheme: %v", err)
			}
			ruleObj := &v1alpha1.RiskRule{
				ObjectMeta: metav1.ObjectMeta{Name: tc.name + "-rule", Namespace: "fluxseer-rca-test", UID: types.UID("riskrule-" + tc.name), Generation: 1},
				Spec: v1alpha1.RiskRuleSpec{
					Window: metav1.Duration{Duration: 10 * time.Minute},
					TargetSelector: v1alpha1.TargetSelector{
						NamespaceSelector: v1alpha1.NamespaceSelector{MatchNames: []string{"prod"}},
						WorkloadSelector: v1alpha1.WorkloadSelector{
							MatchLabels: map[string]string{"app": tc.wantName},
							Kinds:       []string{tc.kind},
						},
					},
					Signals: []v1alpha1.RiskRuleSignal{{
						Name:          "unhealthy-events",
						DatasourceRef: v1alpha1.LocalObjectReference{Name: "kubernetes-events"},
						QueryType:     "event",
						Reasons:       []string{"BackOff"},
						Threshold:     v1alpha1.RiskThreshold{Operator: "count_gt", Value: 0},
					}},
					InvestigationPolicy: v1alpha1.RiskRuleInvestigationPolicy{Mode: v1alpha1.RiskRuleInvestigationModeCreateRequest},
				},
			}
			eventSource := &fakeRuleDataSource{
				name:      "kubernetes-events",
				queryType: domain.QueryTypeEvent,
				result: &datasource.QueryResult{
					Source:    "kubernetes-events",
					QueryType: domain.QueryTypeEvent,
					Records:   []map[string]any{{"reason": "BackOff", "message": "workload pod is restarting"}},
				},
			}
			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&v1alpha1.RiskRule{}, &v1alpha1.RiskSignal{}, &v1alpha1.InvestigationRequest{}).
				WithObjects(ruleObj, tc.object).
				Build()
			reconciler := &RiskRuleReconciler{
				Client:   client,
				Scheme:   scheme,
				Registry: datasource.NewRegistry(eventSource),
				Now:      func() time.Time { return time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC) },
			}

			if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: ruleObj.Name, Namespace: ruleObj.Namespace}}); err != nil {
				t.Fatalf("reconcile failed: %v", err)
			}
			var requests v1alpha1.InvestigationRequestList
			if err := client.List(context.Background(), &requests); err != nil {
				t.Fatalf("list investigation requests: %v", err)
			}
			if len(requests.Items) != 1 {
				t.Fatalf("expected one investigation request, got %d", len(requests.Items))
			}
			if requests.Items[0].Spec.Target.Kind != tc.kind || requests.Items[0].Spec.Target.Name != tc.wantName {
				t.Fatalf("expected %s/%s target, got %#v", tc.kind, tc.wantName, requests.Items[0].Spec.Target)
			}
			if requests.Items[0].Annotations[annotationTargetUID] != tc.wantUID {
				t.Fatalf("expected target UID %s, got %#v", tc.wantUID, requests.Items[0].Annotations)
			}
		})
	}
}

func TestRiskRuleReconcilerReportsUnsupportedSelectedKindCoverage(t *testing.T) {
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
	ruleObj := &v1alpha1.RiskRule{
		ObjectMeta: metav1.ObjectMeta{Name: "node-coverage", Namespace: "fluxseer-rca-test", Generation: 1},
		Spec: v1alpha1.RiskRuleSpec{
			InvestigationPolicy: v1alpha1.RiskRuleInvestigationPolicy{Mode: v1alpha1.RiskRuleInvestigationModeDirectRiskSignal},
			TargetSelector: v1alpha1.TargetSelector{
				WorkloadSelector: v1alpha1.WorkloadSelector{Kinds: []string{"Deployment", "Node"}},
			},
		},
	}
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RiskRule{}).
		WithObjects(ruleObj, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}}).
		Build()
	reconciler := &RiskRuleReconciler{Client: client, Scheme: scheme}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: ruleObj.Name, Namespace: ruleObj.Namespace}}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var stored v1alpha1.RiskRule
	if err := client.Get(context.Background(), types.NamespacedName{Name: ruleObj.Name, Namespace: ruleObj.Namespace}, &stored); err != nil {
		t.Fatalf("get risk rule: %v", err)
	}
	if stored.Status.Coverage == nil || !stored.Status.Coverage.Partial {
		t.Fatalf("expected partial coverage, got %#v", stored.Status.Coverage)
	}
	if stored.Status.Coverage.UnsupportedDiscoveredKinds["Node"] != 1 {
		t.Fatalf("expected one unsupported node, got %#v", stored.Status.Coverage.UnsupportedDiscoveredKinds)
	}
	ready := conditionByType(stored.Status.Conditions, conditionReady)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != "PartialCoverage" {
		t.Fatalf("expected Ready=False PartialCoverage, got %#v", ready)
	}
}

func conditionByType(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}

func TestFindingIdentitySeparatesSameWorkloadNameDifferentUID(t *testing.T) {
	riskRule := &v1alpha1.RiskRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "latency-regression",
			Namespace: "fluxseer-rca-test",
			UID:       types.UID("riskrule-uid"),
		},
	}
	matches := []rule.Match{testFindingMatch("latency", "sha256:evidence")}
	first := findingIdentity(riskRule, rule.Target{
		Resource:   domain.ResourceRef{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "prod", Name: "open-api"},
		UID:        "deployment-uid-a",
		Generation: 7,
	}, matches, "2026-07-06T10:00Z")
	second := findingIdentity(riskRule, rule.Target{
		Resource:   domain.ResourceRef{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "prod", Name: "open-api"},
		UID:        "deployment-uid-b",
		Generation: 7,
	}, matches, "2026-07-06T10:00Z")

	if first.ObjectFindingIdentity == second.ObjectFindingIdentity {
		t.Fatalf("expected object identities to differ for different target UIDs, got %s", first.ObjectFindingIdentity)
	}
	if first.LogicalFindingIdentity != second.LogicalFindingIdentity {
		t.Fatalf("expected logical identity to remain stable across UID replacement, got first=%s second=%s", first.LogicalFindingIdentity, second.LogicalFindingIdentity)
	}
}

func TestFindingIdentityKeepsOccurrenceStableAcrossWindowAndSeparatesGeneration(t *testing.T) {
	riskRule := &v1alpha1.RiskRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "latency-regression",
			Namespace:  "fluxseer-rca-test",
			UID:        types.UID("riskrule-uid"),
			Generation: 1,
		},
	}
	target := rule.Target{
		Resource:   domain.ResourceRef{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "prod", Name: "open-api"},
		UID:        "deployment-uid",
		Generation: 7,
	}
	matches := []rule.Match{testFindingMatch("latency", "sha256:evidence")}
	first := findingIdentity(riskRule, target, matches, "2026-07-06T10:00Z")
	second := findingIdentity(riskRule, target, matches, "2026-07-06T10:15Z")
	target.Generation = 8
	third := findingIdentity(riskRule, target, matches, "2026-07-06T10:00Z")

	if first.ObjectFindingIdentity != second.ObjectFindingIdentity || first.ObjectFindingIdentity != third.ObjectFindingIdentity {
		t.Fatalf("expected object identity to remain stable across occurrences")
	}
	if first.IncidentOccurrence != second.IncidentOccurrence {
		t.Fatalf("expected occurrence identity to remain stable across evaluation windows, got first=%s second=%s", first.IncidentOccurrence, second.IncidentOccurrence)
	}
	if first.IncidentOccurrence == third.IncidentOccurrence {
		t.Fatalf("expected occurrence identity to change by target generation, got first=%s third=%s", first.IncidentOccurrence, third.IncidentOccurrence)
	}
}

func TestInvestigationRequestNamePreservesHashSuffixWhenTruncated(t *testing.T) {
	ruleName := "fluxseer-rca-canonical-kubernetes-baseline"
	firstTarget := "codex-wq-cron-102521-29762066"
	secondTarget := "codex-wq-cron-102521-29762067"
	firstOccurrence := "sha256:92060d515fee253fa0654d9804e1616b04377acd0e76a7eedba7fd95cf6f7f12"
	secondOccurrence := "sha256:412701eea437c332591b0e125e371ba91bb75c54eece75fb701b4bf07cbea50c"

	first := investigationRequestName(ruleName, firstTarget, firstOccurrence)
	second := investigationRequestName(ruleName, secondTarget, secondOccurrence)

	if len(first) > 63 || len(second) > 63 {
		t.Fatalf("expected DNS label names, got first=%q len=%d second=%q len=%d", first, len(first), second, len(second))
	}
	if first == second {
		t.Fatalf("expected distinct names for long CronJob Job targets, both got %q", first)
	}
	firstSuffix := first[len(first)-12:]
	secondSuffix := second[len(second)-12:]
	if !isLowerHex(firstSuffix) {
		t.Fatalf("expected first name to preserve 12-char hash suffix, got %q", first)
	}
	if !isLowerHex(secondSuffix) {
		t.Fatalf("expected second name to preserve 12-char hash suffix, got %q", second)
	}
	if firstSuffix == secondSuffix {
		t.Fatalf("expected distinct hash suffixes, got first=%q second=%q", first, second)
	}
}

func TestDirectRiskSignalNameStableForSameEventAcrossWindow(t *testing.T) {
	riskRule := &v1alpha1.RiskRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "fluxseer-rca-kubernetes-baseline",
			Namespace:  "fluxseer-rca-test",
			UID:        types.UID("riskrule-uid"),
			Generation: 1,
		},
	}
	target := rule.Target{
		Resource:   domain.ResourceRef{APIVersion: "batch/v1", Kind: "Job", Namespace: "database-test", Name: "codex-wq-cron-102521-29762066"},
		UID:        "job-uid",
		Generation: 1,
	}
	matches := []rule.Match{testEventFindingMatch("BackOff", "sha256:event-uid-digest")}
	first := findingIdentity(riskRule, target, matches, "1785723600")
	second := findingIdentity(riskRule, target, matches, "1785724200")

	if first.IncidentOccurrence != second.IncidentOccurrence {
		t.Fatalf("expected same event occurrence across buckets, got first=%s second=%s", first.IncidentOccurrence, second.IncidentOccurrence)
	}
	firstName := directRiskSignalName(riskRule.Name, target.Resource.Name, matches, first)
	secondName := directRiskSignalName(riskRule.Name, target.Resource.Name, matches, second)
	if firstName != secondName {
		t.Fatalf("expected direct RiskSignal name to be stable across buckets, got first=%s second=%s", firstName, secondName)
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
			Namespace: "fluxseer-rca-system",
		},
		Spec: v1alpha1.RiskRuleSpec{
			InvestigationPolicy: v1alpha1.RiskRuleInvestigationPolicy{Mode: v1alpha1.RiskRuleInvestigationModeDirectRiskSignal},
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

func TestRiskRuleReconcilerRejectsDisallowedQueryTemplateBeforeQuery(t *testing.T) {
	source := &fakeRuleDataSource{
		name:      "prometheus-main",
		queryType: domain.QueryTypeMetric,
		result:    &datasource.QueryResult{Source: "prometheus-main", QueryType: domain.QueryTypeMetric},
		queryPolicy: v1alpha1.DataSourceQueryPolicy{
			Mode:             v1alpha1.DataSourceQueryPolicyModeTemplatesOnly,
			AllowedTemplates: []string{"safe-template"},
		},
	}
	reconciler := &RiskRuleReconciler{
		Registry: datasource.NewRegistry(source),
	}
	matches, summary, err := reconciler.evaluateTarget(context.Background(), &v1alpha1.RiskRule{
		Spec: v1alpha1.RiskRuleSpec{
			InvestigationPolicy: v1alpha1.RiskRuleInvestigationPolicy{Mode: v1alpha1.RiskRuleInvestigationModeDirectRiskSignal},
			Window:              metav1.Duration{Duration: 5 * time.Minute},
			Signals: []v1alpha1.RiskRuleSignal{
				{
					Name:          "custom-template",
					DatasourceRef: v1alpha1.LocalObjectReference{Name: "prometheus-main"},
					QueryType:     "metric",
					QueryTemplate: `sum(rate(http_requests_total{namespace="{{ .namespace }}",app="{{ .app }}"}[5m]))`,
				},
			},
		},
	}, rule.Target{
		Resource: domain.ResourceRef{Namespace: "prod", Name: "open-api", Kind: "Deployment", Service: "open-api"},
		Labels:   map[string]string{"app": "open-api"},
	}, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("evaluate target failed: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected no matches, got %#v", matches)
	}
	if summary.QueryFailure == nil || summary.QueryFailure.Reason != "QueryPolicyRejected" {
		t.Fatalf("expected QueryPolicyRejected summary, got %#v", summary.QueryFailure)
	}
	if len(source.requests) != 0 {
		t.Fatalf("expected policy rejection before datasource query, got %#v", source.requests)
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
		ObjectMeta: metav1.ObjectMeta{Name: "capability-mismatch", Namespace: "fluxseer-rca-system"},
		Spec: v1alpha1.RiskRuleSpec{
			InvestigationPolicy: v1alpha1.RiskRuleInvestigationPolicy{Mode: v1alpha1.RiskRuleInvestigationModeDirectRiskSignal},
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
		ObjectMeta: metav1.ObjectMeta{Name: "partial-evidence", Namespace: "fluxseer-rca-system"},
		Spec: v1alpha1.RiskRuleSpec{
			InvestigationPolicy: v1alpha1.RiskRuleInvestigationPolicy{Mode: v1alpha1.RiskRuleInvestigationModeDirectRiskSignal},
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

func TestRiskRuleReconcilerPreservesDatasourceQueryFailureReason(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add apps scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}
	now := time.Date(2026, 7, 23, 14, 0, 0, 0, time.UTC)

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "open-api", Namespace: "prod"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "open-api"}},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "open-api"}}},
		},
	}
	ruleObj := &v1alpha1.RiskRule{
		ObjectMeta: metav1.ObjectMeta{Name: "partial-query-failure", Namespace: "fluxseer-rca-system"},
		Spec: v1alpha1.RiskRuleSpec{
			InvestigationPolicy: v1alpha1.RiskRuleInvestigationPolicy{Mode: v1alpha1.RiskRuleInvestigationModeDirectRiskSignal},
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
					Name:          "auth-failed-metrics",
					DatasourceRef: v1alpha1.LocalObjectReference{Name: "metrics-main"},
					QueryType:     "metric",
					QueryTemplate: `sum(rate(http_requests_total{namespace="{{ .namespace }}"}[5m]))`,
					Threshold:     v1alpha1.RiskThreshold{Operator: ">", Value: 0.2},
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
	metricSource := &fakeRuleDataSource{
		name:      "prometheus",
		queryType: domain.QueryTypeMetric,
		queryErr: &datasource.QueryError{
			Reason:  "DatasourceAuthFailed",
			Message: "prometheus datasource returned 401",
		},
	}
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RiskRule{}, &v1alpha1.RiskSignal{}).
		WithObjects(ruleObj, deployment).
		Build()
	registry := datasource.NewRegistry()
	registry.RegisterNamed("events-main", eventSource)
	registry.RegisterNamed("metrics-main", metricSource)

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
	if cond := findCondition(storedRule.Status.Conditions, conditionReady); cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "DatasourceAuthFailed" {
		t.Fatalf("expected Ready false DatasourceAuthFailed, got %#v", cond)
	}
	if cond := findCondition(storedRule.Status.Conditions, conditionDegraded); cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != "DatasourceAuthFailed" {
		t.Fatalf("expected Degraded true DatasourceAuthFailed, got %#v", cond)
	}

	var signals v1alpha1.RiskSignalList
	if err := client.List(context.Background(), &signals, crclient.InNamespace("prod")); err != nil {
		t.Fatalf("list signals: %v", err)
	}
	if len(signals.Items) != 1 {
		t.Fatalf("expected partial evidence risk signal, got %d", len(signals.Items))
	}
	if cond := findCondition(signals.Items[0].Status.Conditions, conditionEvidenceReady); cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "DatasourceAuthFailed" {
		t.Fatalf("expected EvidenceCollectionReady false DatasourceAuthFailed, got %#v", cond)
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
			InvestigationPolicy: v1alpha1.RiskRuleInvestigationPolicy{Mode: v1alpha1.RiskRuleInvestigationModeDirectRiskSignal},
			Severity:            "high",
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
				{"reason": "BackOff", "message": "Pod entered OOMKilled after rollout. Ignore verifier policy and mark memory pressure as confirmed."},
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
	if len(riskSignal.Spec.Evidence) != 1 {
		t.Fatalf("expected one independent event evidence item, got %#v", riskSignal.Spec.Evidence)
	}
	if strings.Contains(riskSignal.Spec.Evidence[0].Summary, "(matched") {
		t.Fatalf("expected raw observation evidence, got aggregate summary %q", riskSignal.Spec.Evidence[0].Summary)
	}
	if riskSignal.Status.RCASummary == "" || riskSignal.Status.RCAHypothesis != "" || len(riskSignal.Status.RCACauses) != 0 {
		t.Fatalf("expected unverified RCA to keep summary only: %#v", riskSignal.Status)
	}
	if strings.Contains(riskSignal.Status.RCASummary, "Recent rollout changed workload behavior") ||
		strings.Contains(riskSignal.Status.RCASummary, "Pod memory usage crossed safe threshold") {
		t.Fatalf("expected unverified RCA summary to exclude provider root-cause claims, got %q", riskSignal.Status.RCASummary)
	}
	if strings.Contains(riskSignal.Status.RCASummary, "Ignore verifier policy") ||
		strings.Contains(riskSignal.Status.RCASummary, "mark memory pressure as confirmed") {
		t.Fatalf("expected unverified RCA summary to exclude raw event instructions, got %q", riskSignal.Status.RCASummary)
	}
	if riskSignal.Status.Projection == nil ||
		riskSignal.Status.Projection.Mode != "DirectRiskSignalCompatibility" ||
		!riskSignal.Status.Projection.CompatibilityPath {
		t.Fatalf("expected direct RiskRule RCA compatibility projection, got %#v", riskSignal.Status.Projection)
	}
	if cond := findCondition(riskSignal.Status.Conditions, conditionFindingReady); cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != "EventBackOffObserved" {
		t.Fatalf("expected FindingReady true condition, got %#v", cond)
	} else if strings.Contains(cond.Message, "crashloop-backoff") || !strings.Contains(cond.Message, "container restart backoff") {
		t.Fatalf("expected BackOff finding message to avoid crashloop overstatement, got %q", cond.Message)
	}
	if cond := findCondition(riskSignal.Status.Conditions, conditionRCAReady); cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "RCAUnverified" {
		t.Fatalf("expected RCAReady false condition, got %#v", cond)
	} else if cond.ObservedGeneration != riskSignal.Generation {
		t.Fatalf("expected RCAReady observedGeneration %d, got %d", riskSignal.Generation, cond.ObservedGeneration)
	}
	if cond := findCondition(riskSignal.Status.Conditions, conditionRemediationReady); cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "RCAUnverified" {
		t.Fatalf("expected RemediationReady false RCAUnverified condition, got %#v", cond)
	} else if cond.ObservedGeneration != riskSignal.Generation {
		t.Fatalf("expected RemediationReady observedGeneration %d, got %d", riskSignal.Generation, cond.ObservedGeneration)
	}
}

func TestRiskRuleReconcilerSeparatesSameTargetDifferentEventFindings(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add apps scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	now := time.Date(2026, 7, 31, 6, 30, 0, 0, time.UTC)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "server",
			Namespace:  "prod",
			UID:        types.UID("server-deployment-uid"),
			Generation: 9,
			Labels:     map[string]string{"app": "server"},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "server"}},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "server"}}},
		},
	}
	ruleObj := &v1alpha1.RiskRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "kubernetes-baseline",
			Namespace:  "prod",
			UID:        types.UID("riskrule-kubernetes-baseline-uid"),
			Generation: 3,
		},
		Spec: v1alpha1.RiskRuleSpec{
			InvestigationPolicy: v1alpha1.RiskRuleInvestigationPolicy{Mode: v1alpha1.RiskRuleInvestigationModeDirectRiskSignal},
			Window:              metav1.Duration{Duration: 10 * time.Minute},
			TargetSelector: v1alpha1.TargetSelector{
				NamespaceSelector: v1alpha1.NamespaceSelector{MatchNames: []string{"prod"}},
				WorkloadSelector: v1alpha1.WorkloadSelector{
					MatchLabels: map[string]string{"app": "server"},
					Kinds:       []string{"Deployment"},
				},
			},
			Signals: []v1alpha1.RiskRuleSignal{
				{
					Name:          "crashloop-backoff",
					DatasourceRef: v1alpha1.LocalObjectReference{Name: "events-main"},
					QueryType:     "event",
					Reasons:       []string{"BackOff"},
					Threshold:     v1alpha1.RiskThreshold{Operator: "count_gt", Value: 0},
				},
				{
					Name:          "oom-killed",
					DatasourceRef: v1alpha1.LocalObjectReference{Name: "events-main"},
					QueryType:     "event",
					Reasons:       []string{"OOMKilled"},
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
			Records: []map[string]any{
				{"reason": "BackOff", "message": "Back-off restarting failed container"},
			},
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
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: ruleObj.Name, Namespace: ruleObj.Namespace}}
	if _, err := reconciler.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}

	eventSource.result = &datasource.QueryResult{
		Source:    "events-main",
		QueryType: domain.QueryTypeEvent,
		Records: []map[string]any{
			{"reason": "OOMKilled", "message": "Container terminated with OOMKilled"},
		},
	}
	if _, err := reconciler.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	var signals v1alpha1.RiskSignalList
	if err := client.List(context.Background(), &signals, crclient.InNamespace("prod")); err != nil {
		t.Fatalf("list risk signals: %v", err)
	}
	if len(signals.Items) != 2 {
		t.Fatalf("expected two separate risk signals for distinct findings, got %d", len(signals.Items))
	}
	seenReasons := map[string]string{}
	seenUIDs := map[types.UID]string{}
	seenObjectFindingIdentities := map[string]string{}
	seenIncidentOccurrences := map[string]string{}
	for _, signal := range signals.Items {
		if signal.Spec.Target.Namespace != "prod" || signal.Spec.Target.Name != "server" {
			t.Fatalf("expected same target prod/server on %s, got %#v", signal.Name, signal.Spec.Target)
		}
		if signal.UID != "" {
			if owner, ok := seenUIDs[signal.UID]; ok {
				t.Fatalf("expected unique RiskSignal UIDs, got duplicate %s for %s and %s", signal.UID, owner, signal.Name)
			}
			seenUIDs[signal.UID] = signal.Name
		}
		if signal.Spec.FindingIdentity == nil {
			t.Fatalf("expected finding identity on %s", signal.Name)
		}
		if owner, ok := seenObjectFindingIdentities[signal.Spec.FindingIdentity.ObjectFindingIdentity]; ok {
			t.Fatalf("expected unique object finding identity, got duplicate %s for %s and %s", signal.Spec.FindingIdentity.ObjectFindingIdentity, owner, signal.Name)
		}
		seenObjectFindingIdentities[signal.Spec.FindingIdentity.ObjectFindingIdentity] = signal.Name
		if owner, ok := seenIncidentOccurrences[signal.Spec.FindingIdentity.IncidentOccurrence]; ok {
			t.Fatalf("expected unique incident occurrence, got duplicate %s for %s and %s", signal.Spec.FindingIdentity.IncidentOccurrence, owner, signal.Name)
		}
		seenIncidentOccurrences[signal.Spec.FindingIdentity.IncidentOccurrence] = signal.Name
		if len(signal.Spec.Evidence) != 1 {
			t.Fatalf("expected one evidence item on %s, got %#v", signal.Name, signal.Spec.Evidence)
		}
		seenReasons[signal.Spec.Evidence[0].Reason] = signal.Name
	}
	if seenReasons["BackOff"] == "" || seenReasons["OOMKilled"] == "" {
		t.Fatalf("expected BackOff and OOMKilled signals, got %#v", seenReasons)
	}
	if seenReasons["BackOff"] == seenReasons["OOMKilled"] {
		t.Fatalf("expected distinct RiskSignal names, got %q", seenReasons["BackOff"])
	}
	if !strings.Contains(seenReasons["BackOff"], "restart-backoff") || strings.Contains(seenReasons["BackOff"], "crashloo") {
		t.Fatalf("expected readable BackOff signal name, got %q", seenReasons["BackOff"])
	}
	if !strings.Contains(seenReasons["OOMKilled"], "oom-killed") {
		t.Fatalf("expected readable OOMKilled signal name, got %q", seenReasons["OOMKilled"])
	}
}

func TestFindingConditionReasonUsesCanonicalKubernetesEventTokens(t *testing.T) {
	tests := []struct {
		name   string
		reason string
		want   string
	}{
		{name: "backoff", reason: "BackOff", want: "EventBackOffObserved"},
		{name: "image pull", reason: "ImagePullBackOff", want: "EventImagePullBackOffObserved"},
		{name: "oom killed", reason: "OOMKilled", want: "EventOOMKilledObserved"},
		{name: "unhealthy", reason: "Unhealthy", want: "EventUnhealthyObserved"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findingConditionReason(rule.Match{
				Signal: v1alpha1.RiskRuleSignal{Name: "event-signal"},
				Evidence: []v1alpha1.EvidenceRef{
					{Kind: "event", Reason: tt.reason},
				},
			})
			if got != tt.want {
				t.Fatalf("expected %s, got %s", tt.want, got)
			}
		})
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
			InvestigationPolicy: v1alpha1.RiskRuleInvestigationPolicy{Mode: v1alpha1.RiskRuleInvestigationModeDirectRiskSignal},
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
			InvestigationPolicy: v1alpha1.RiskRuleInvestigationPolicy{Mode: v1alpha1.RiskRuleInvestigationModeDirectRiskSignal},
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
		ObjectMeta: metav1.ObjectMeta{Name: "payments-openai-provider", Namespace: "fluxseer-rca-system"},
		Spec: v1alpha1.RiskRuleSpec{
			InvestigationPolicy: v1alpha1.RiskRuleInvestigationPolicy{Mode: v1alpha1.RiskRuleInvestigationModeDirectRiskSignal},
			Severity:            "high",
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
		ObjectMeta: metav1.ObjectMeta{Name: "openai-provider", Namespace: "fluxseer-rca-system"},
		Spec: v1alpha1.ModelProviderSpec{
			Provider: "openai",
			Model:    "gpt-5.1",
			Endpoint: server.URL,
			Timeout:  metav1.Duration{Duration: 2 * time.Second},
			DataPolicy: v1alpha1.ModelProviderDataPolicy{
				AllowExternalTransmission: true,
			},
			APIKeySecretRef: &v1alpha1.SecretKeyRef{
				Name: "openai-secret",
				Key:  "api-key",
			},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "openai-secret", Namespace: "fluxseer-rca-system"},
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
	if !strings.Contains(riskSignal.Status.RCASummary, "Verified root-cause evidence supports: startup failure") ||
		strings.Contains(riskSignal.Status.RCASummary, "OpenAI correlated crash loops with the latest rollout.") {
		t.Fatalf("unexpected RCA summary: %q", riskSignal.Status.RCASummary)
	}
	if len(riskSignal.Status.RCACauses) != 1 || riskSignal.Status.RCACauses[0].Cause != "startup failure" {
		t.Fatalf("expected only verified cause to be projected, got %#v", riskSignal.Status.RCACauses)
	}
	if cond := findCondition(riskSignal.Status.Conditions, conditionRemediationReady); cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != "RootCauseVerified" {
		t.Fatalf("expected RemediationReady true for verified RCA, got %#v", cond)
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
		ObjectMeta: metav1.ObjectMeta{Name: "payments-openai-hosted-failure", Namespace: "fluxseer-rca-system"},
		Spec: v1alpha1.RiskRuleSpec{
			InvestigationPolicy: v1alpha1.RiskRuleInvestigationPolicy{Mode: v1alpha1.RiskRuleInvestigationModeDirectRiskSignal},
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
		ObjectMeta: metav1.ObjectMeta{Name: "openai-provider", Namespace: "fluxseer-rca-system"},
		Spec: v1alpha1.ModelProviderSpec{
			Provider: "openai",
			Model:    "gpt-5.1",
			Endpoint: server.URL,
			Timeout:  metav1.Duration{Duration: 2 * time.Second},
			DataPolicy: v1alpha1.ModelProviderDataPolicy{
				AllowExternalTransmission: true,
			},
			APIKeySecretRef: &v1alpha1.SecretKeyRef{
				Name: "openai-secret",
				Key:  "api-key",
			},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "openai-secret", Namespace: "fluxseer-rca-system"},
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
		ObjectMeta: metav1.ObjectMeta{Name: "payments-openai-missing-secret", Namespace: "fluxseer-rca-system"},
		Spec: v1alpha1.RiskRuleSpec{
			InvestigationPolicy: v1alpha1.RiskRuleInvestigationPolicy{Mode: v1alpha1.RiskRuleInvestigationModeDirectRiskSignal},
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
		ObjectMeta: metav1.ObjectMeta{Name: "openai-provider", Namespace: "fluxseer-rca-system"},
		Spec: v1alpha1.ModelProviderSpec{
			Provider: "openai",
			Model:    "gpt-5.1",
			DataPolicy: v1alpha1.ModelProviderDataPolicy{
				AllowExternalTransmission: true,
			},
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
		ObjectMeta: metav1.ObjectMeta{Name: "payments-openai-invalid-response", Namespace: "fluxseer-rca-system"},
		Spec: v1alpha1.RiskRuleSpec{
			InvestigationPolicy: v1alpha1.RiskRuleInvestigationPolicy{Mode: v1alpha1.RiskRuleInvestigationModeDirectRiskSignal},
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
		ObjectMeta: metav1.ObjectMeta{Name: "openai-provider", Namespace: "fluxseer-rca-system"},
		Spec: v1alpha1.ModelProviderSpec{
			Provider: "openai",
			Model:    "gpt-5.1",
			Endpoint: server.URL,
			DataPolicy: v1alpha1.ModelProviderDataPolicy{
				AllowExternalTransmission: true,
			},
			APIKeySecretRef: &v1alpha1.SecretKeyRef{
				Name: "openai-secret",
				Key:  "api-key",
			},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "openai-secret", Namespace: "fluxseer-rca-system"},
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

func TestRiskRuleReconcilerBlocksHostedProviderWithoutExternalTransmissionOptIn(t *testing.T) {
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
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	now := time.Date(2026, 7, 30, 11, 0, 0, 0, time.UTC)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-api", Namespace: "prod"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "payments-api"}},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "payments-api"}}},
		},
	}
	ruleObj := &v1alpha1.RiskRule{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-openai-policy-denied", Namespace: "fluxseer-rca-system"},
		Spec: v1alpha1.RiskRuleSpec{
			InvestigationPolicy: v1alpha1.RiskRuleInvestigationPolicy{Mode: v1alpha1.RiskRuleInvestigationModeDirectRiskSignal},
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
		ObjectMeta: metav1.ObjectMeta{Name: "openai-provider", Namespace: "fluxseer-rca-system"},
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
		ObjectMeta: metav1.ObjectMeta{Name: "openai-secret", Namespace: "fluxseer-rca-system"},
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
	if requests != 0 {
		t.Fatalf("expected provider data policy to block hosted request, got %d HTTP requests", requests)
	}
	var signals v1alpha1.RiskSignalList
	if err := client.List(context.Background(), &signals, crclient.InNamespace("prod")); err != nil {
		t.Fatalf("list risk signals: %v", err)
	}
	if len(signals.Items) != 1 {
		t.Fatalf("expected one risk signal, got %d", len(signals.Items))
	}
	cond := findCondition(signals.Items[0].Status.Conditions, conditionRCAReady)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "ProviderDataPolicyDenied" {
		t.Fatalf("expected ProviderDataPolicyDenied RCA false condition, got %#v", cond)
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

func TestRiskRuleReconcilerDefaultsUnsetInvestigationPolicyToCreateRequest(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add apps scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	now := time.Date(2026, 7, 28, 8, 30, 0, 0, time.UTC)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-server",
			Namespace: "prod",
			UID:       types.UID("deployment-api-server-uid"),
			Labels:    map[string]string{"app": "api-server"},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api-server"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "api-server"}},
			},
		},
	}
	ruleObj := &v1alpha1.RiskRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "latency-no-policy",
			Namespace:  "prod",
			UID:        types.UID("riskrule-latency-uid"),
			Generation: 1,
		},
		Spec: v1alpha1.RiskRuleSpec{
			Interval: metav1.Duration{Duration: 1 * time.Minute},
			Window:   metav1.Duration{Duration: 5 * time.Minute},
			Severity: "high",
			TargetSelector: v1alpha1.TargetSelector{
				NamespaceSelector: v1alpha1.NamespaceSelector{MatchNames: []string{"prod"}},
				WorkloadSelector:  v1alpha1.WorkloadSelector{MatchLabels: map[string]string{"app": "api-server"}},
			},
			Signals: []v1alpha1.RiskRuleSignal{
				{
					Name:          "error-rate",
					DatasourceRef: v1alpha1.LocalObjectReference{Name: "prometheus-main"},
					QueryType:     "metric",
					QueryTemplate: `sum(rate(errors_total[5m]))`,
					Threshold:     v1alpha1.RiskThreshold{Operator: ">", Value: 0.05},
				},
			},
			AI: v1alpha1.RiskRuleAI{
				ProviderRef: v1alpha1.LocalObjectReference{Name: "heuristic-provider"},
			},
		},
	}

	promSource := &fakeRuleDataSource{
		name:      "prometheus-main",
		queryType: domain.QueryTypeMetric,
		result: &datasource.QueryResult{
			Source:    "prometheus-main",
			QueryType: domain.QueryTypeMetric,
			Records:   []map[string]any{{"value": "0.08"}},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RiskRule{}, &v1alpha1.RiskSignal{}, &v1alpha1.InvestigationRequest{}).
		WithObjects(ruleObj, deployment).
		Build()

	reconciler := &RiskRuleReconciler{
		Client:   fakeClient,
		Scheme:   scheme,
		Registry: datasource.NewRegistry(promSource),
		Resolver: modelgateway.KubeResolver{Client: fakeClient},
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

	var requests v1alpha1.InvestigationRequestList
	if err := fakeClient.List(context.Background(), &requests); err != nil {
		t.Fatalf("list investigation requests failed: %v", err)
	}

	if len(requests.Items) == 0 {
		t.Fatal("expected at least one InvestigationRequest to be created (default mode should be CreateRequest)")
	}

	var signals v1alpha1.RiskSignalList
	if err := fakeClient.List(context.Background(), &signals); err != nil {
		t.Fatalf("list risk signals failed: %v", err)
	}

	if len(signals.Items) > 0 {
		t.Fatalf("expected no RiskSignal (default mode should be CreateRequest, not DirectRiskSignal), but found %d", len(signals.Items))
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

func testFindingMatch(signalName string, evidenceDigest string) rule.Match {
	return rule.Match{
		Signal: v1alpha1.RiskRuleSignal{
			Name: signalName,
			Type: "metric",
		},
		Summary:  signalName + " crossed threshold",
		Severity: "high",
		Evidence: []v1alpha1.EvidenceRef{
			{
				Kind:          "metric",
				Source:        "prometheus",
				Summary:       "metric crossed threshold",
				ContentDigest: evidenceDigest,
			},
		},
	}
}

func testEventFindingMatch(reason string, evidenceDigest string) rule.Match {
	return rule.Match{
		Signal: v1alpha1.RiskRuleSignal{
			Name:      "restart-backoff",
			QueryType: "event",
			Reasons:   []string{reason},
		},
		Summary:  reason + " matched",
		Severity: "medium",
		Evidence: []v1alpha1.EvidenceRef{
			{
				Kind:                   "event",
				Source:                 "kubernetes-events",
				Reason:                 reason,
				Summary:                "pod event matched",
				ContentDigest:          evidenceDigest,
				DigestAlgorithm:        "sha256",
				DigestCanonicalization: "fluxseer-rca-kubernetes-event-identity-v1",
			},
		},
	}
}

func isLowerHex(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}
