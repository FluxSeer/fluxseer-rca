package investigation

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"fluxagent/api/v1alpha1"
	"fluxagent/internal/datasource"
	"fluxagent/internal/domain"
	evidencepkg "fluxagent/internal/evidence"
	"fluxagent/internal/knowledge"
	"fluxagent/internal/model"
	"fluxagent/internal/model/heuristic"
	"fluxagent/internal/modelgateway"
	"fluxagent/internal/rcametrics"
)

type clientObject = client.Object

func TestServicePreflightResolvesTargetDatasourcesAndProvider(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add apps scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add aiops scheme: %v", err)
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			&appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "open-api",
					Namespace: "prod",
					Labels:    map[string]string{"app": "open-api"},
				},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{"app": "open-api"},
						},
					},
				},
			},
			&v1alpha1.ModelProvider{
				ObjectMeta: metav1.ObjectMeta{Name: "heuristic-provider", Namespace: "fluxagent-system"},
				Spec: v1alpha1.ModelProviderSpec{
					Provider: "heuristic",
					Model:    "built-in",
				},
			},
		).
		Build()

	service := &Service{
		Client: client,
		Registry: datasource.NewRegistry(
			fakeDataSource{name: "kubernetes-events", queryType: domain.QueryTypeEvent},
			fakeDataSource{name: "prometheus", queryType: domain.QueryTypeMetric},
		),
		Resolver: modelgateway.KubeResolver{Client: client},
	}

	result, err := service.Preflight(context.Background(), "prod", v1alpha1.InvestigationRequestSpec{
		Target: v1alpha1.TargetRef{
			Namespace: "prod",
			Kind:      "Deployment",
			Name:      "open-api",
		},
		DataSources: []v1alpha1.LocalObjectReference{
			{Name: "kubernetes-events"},
			{Name: "prometheus"},
		},
		ModelProviderRef: v1alpha1.LocalObjectReference{Name: "heuristic-provider"},
	})
	if err != nil {
		t.Fatalf("preflight failed: %v", err)
	}
	if !result.Successful() {
		t.Fatalf("expected successful preflight, got %#v", result)
	}
	if result.Target.Name != "open-api" || result.Target.Namespace != "prod" {
		t.Fatalf("unexpected target %#v", result.Target)
	}
	if result.Provider == nil || result.Provider.Name != "heuristic-provider" {
		t.Fatalf("unexpected provider %#v", result.Provider)
	}
	if len(result.DatasourceNames) != 2 {
		t.Fatalf("expected two datasource names, got %#v", result.DatasourceNames)
	}
	if len(result.CollectionPlan) != 2 {
		t.Fatalf("expected two collection steps, got %#v", result.CollectionPlan)
	}
}

func TestServicePreflightResolvesNonDeploymentTargets(t *testing.T) {
	cases := []struct {
		name               string
		target             v1alpha1.TargetRef
		object             clientObject
		wantKind           string
		wantAPIVersion     string
		wantService        string
		wantGeneratedLabel string
	}{
		{
			name: "statefulset",
			target: v1alpha1.TargetRef{
				Namespace:  "prod",
				Kind:       "StatefulSet",
				Name:       "orders-db",
				APIVersion: "apps/v1",
			},
			object: &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{Name: "orders-db", Namespace: "prod"},
				Spec: appsv1.StatefulSetSpec{
					Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "orders-db"}}},
				},
			},
			wantKind:           "StatefulSet",
			wantAPIVersion:     "apps/v1",
			wantService:        "orders-db",
			wantGeneratedLabel: `app="orders-db"`,
		},
		{
			name: "daemonset",
			target: v1alpha1.TargetRef{
				Namespace: "prod",
				Kind:      "DaemonSet",
				Name:      "node-agent",
			},
			object: &appsv1.DaemonSet{
				ObjectMeta: metav1.ObjectMeta{Name: "node-agent", Namespace: "prod", Labels: map[string]string{"app": "node-agent"}},
			},
			wantKind:           "DaemonSet",
			wantAPIVersion:     "apps/v1",
			wantService:        "node-agent",
			wantGeneratedLabel: `app="node-agent"`,
		},
		{
			name: "replicaset",
			target: v1alpha1.TargetRef{
				Namespace: "prod",
				Kind:      "ReplicaSet",
				Name:      "checkout-abc123",
			},
			object: &appsv1.ReplicaSet{
				ObjectMeta: metav1.ObjectMeta{Name: "checkout-abc123", Namespace: "prod"},
				Spec: appsv1.ReplicaSetSpec{
					Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "checkout"}}},
				},
			},
			wantKind:           "ReplicaSet",
			wantAPIVersion:     "apps/v1",
			wantService:        "checkout",
			wantGeneratedLabel: `app="checkout"`,
		},
		{
			name: "pod",
			target: v1alpha1.TargetRef{
				Namespace:  "prod",
				Kind:       "Pod",
				Name:       "checkout-abc123",
				APIVersion: "v1",
			},
			object: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "checkout-abc123", Namespace: "prod", Labels: map[string]string{"app": "checkout"}},
			},
			wantKind:           "Pod",
			wantAPIVersion:     "v1",
			wantService:        "checkout",
			wantGeneratedLabel: `app="checkout"`,
		},
		{
			name: "job",
			target: v1alpha1.TargetRef{
				Namespace:  "prod",
				Kind:       "Job",
				Name:       "backup",
				APIVersion: "batch/v1",
			},
			object: &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: "backup", Namespace: "prod"},
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "backup"}}},
				},
			},
			wantKind:           "Job",
			wantAPIVersion:     "batch/v1",
			wantService:        "backup",
			wantGeneratedLabel: `app="backup"`,
		},
		{
			name: "cronjob",
			target: v1alpha1.TargetRef{
				Namespace:  "prod",
				Kind:       "CronJob",
				Name:       "nightly",
				APIVersion: "batch/v1",
			},
			object: &batchv1.CronJob{
				ObjectMeta: metav1.ObjectMeta{Name: "nightly", Namespace: "prod"},
				Spec: batchv1.CronJobSpec{
					JobTemplate: batchv1.JobTemplateSpec{Spec: batchv1.JobSpec{
						Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "nightly"}}},
					}},
				},
			},
			wantKind:           "CronJob",
			wantAPIVersion:     "batch/v1",
			wantService:        "nightly",
			wantGeneratedLabel: `app="nightly"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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
			kubeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tc.object).
				Build()
			service := &Service{
				Client: kubeClient,
				Registry: datasource.NewRegistry(
					fakeDataSource{name: "prometheus", queryType: domain.QueryTypeMetric},
				),
				Resolver: modelgateway.KubeResolver{Client: kubeClient},
			}

			result, err := service.Preflight(context.Background(), "prod", v1alpha1.InvestigationRequestSpec{
				Target:      tc.target,
				DataSources: []v1alpha1.LocalObjectReference{{Name: "prometheus"}},
			})
			if err != nil {
				t.Fatalf("preflight failed: %v", err)
			}
			if result.TargetIssue != nil {
				t.Fatalf("expected target to resolve, got %#v", result.TargetIssue)
			}
			if result.Target.Kind != tc.wantKind || result.Target.APIVersion != tc.wantAPIVersion || result.Target.Service != tc.wantService {
				t.Fatalf("unexpected resolved target %#v", result.Target)
			}
			if len(result.CollectionPlan) != 1 {
				t.Fatalf("expected one generated collection step, got %#v", result.CollectionPlan)
			}
			if !strings.Contains(result.CollectionPlan[0].Query, tc.wantGeneratedLabel) {
				t.Fatalf("expected generated query to include %s, got %q", tc.wantGeneratedLabel, result.CollectionPlan[0].Query)
			}
		})
	}
}

func TestServicePreflightReportsMissingDatasource(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add apps scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add aiops scheme: %v", err)
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			&appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: "open-api", Namespace: "prod"},
			},
		).
		Build()

	service := &Service{
		Client: client,
		Registry: datasource.NewRegistry(
			fakeDataSource{name: "kubernetes-events", queryType: domain.QueryTypeEvent},
		),
		Resolver: modelgateway.KubeResolver{Client: client},
	}

	result, err := service.Preflight(context.Background(), "prod", v1alpha1.InvestigationRequestSpec{
		Target: v1alpha1.TargetRef{
			Namespace: "prod",
			Kind:      "Deployment",
			Name:      "open-api",
		},
		DataSources: []v1alpha1.LocalObjectReference{
			{Name: "missing-ds"},
		},
	})
	if err != nil {
		t.Fatalf("preflight failed: %v", err)
	}
	if result.DatasourceIssue == nil || result.DatasourceIssue.Reason != "DataSourceNotFound" {
		t.Fatalf("expected DataSourceNotFound, got %#v", result.DatasourceIssue)
	}
}

func TestServicePreflightRejectsAmbiguousEvidencePlanning(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add apps scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add aiops scheme: %v", err)
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			&appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: "open-api", Namespace: "prod"},
			},
		).
		Build()

	service := &Service{
		Client: client,
		Registry: datasource.NewRegistry(
			fakeDataSource{name: "prometheus", queryType: domain.QueryTypeMetric},
		),
		Resolver: modelgateway.KubeResolver{Client: client},
	}

	result, err := service.Preflight(context.Background(), "prod", v1alpha1.InvestigationRequestSpec{
		Target: v1alpha1.TargetRef{
			Namespace: "prod",
			Kind:      "Deployment",
			Name:      "open-api",
		},
		DataSources: []v1alpha1.LocalObjectReference{{Name: "prometheus"}},
		Queries: []v1alpha1.InvestigationQuery{{
			DatasourceRef: v1alpha1.LocalObjectReference{Name: "prometheus"},
			QueryType:     string(domain.QueryTypeMetric),
			Query:         "up",
		}},
	})
	if err != nil {
		t.Fatalf("preflight failed: %v", err)
	}
	if result.DatasourceIssue == nil || result.DatasourceIssue.Reason != "InvalidSpec" {
		t.Fatalf("expected InvalidSpec datasource issue, got %#v", result.DatasourceIssue)
	}
	if !strings.Contains(result.DatasourceIssue.Message, "mutually exclusive") {
		t.Fatalf("expected mutually exclusive message, got %q", result.DatasourceIssue.Message)
	}
}

func TestServicePreflightRejectsDisallowedQueryTemplate(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add apps scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add aiops scheme: %v", err)
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			&appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: "open-api", Namespace: "prod"},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "open-api"}},
					},
				},
			},
		).
		Build()
	service := &Service{
		Client: client,
		Registry: datasource.NewRegistry(fakeDataSource{
			name:      "prometheus",
			queryType: domain.QueryTypeMetric,
			queryPolicy: v1alpha1.DataSourceQueryPolicy{
				Mode:             v1alpha1.DataSourceQueryPolicyModeTemplatesOnly,
				AllowedTemplates: []string{"safe-template"},
			},
		}),
	}

	result, err := service.Preflight(context.Background(), "prod", v1alpha1.InvestigationRequestSpec{
		Target: v1alpha1.TargetRef{Namespace: "prod", Kind: "Deployment", Name: "open-api"},
		TimeRange: v1alpha1.InvestigationTimeRange{
			Lookback: metav1.Duration{Duration: 5 * time.Minute},
		},
		Queries: []v1alpha1.InvestigationQuery{
			{
				Name:          "unbounded-custom-query",
				DatasourceRef: v1alpha1.LocalObjectReference{Name: "prometheus"},
				QueryType:     string(domain.QueryTypeMetric),
				QueryTemplate: `sum(rate(http_requests_total{namespace="{{ .namespace }}",app="{{ .app }}"}[5m]))`,
			},
		},
	})
	if err != nil {
		t.Fatalf("preflight failed: %v", err)
	}
	if result.QueryTypeIssue == nil || result.QueryTypeIssue.Reason != "QueryPolicyRejected" {
		t.Fatalf("expected QueryPolicyRejected, got %#v", result.QueryTypeIssue)
	}
	if !strings.Contains(result.QueryTypeIssue.Message, "not allowed") {
		t.Fatalf("expected bounded policy message, got %q", result.QueryTypeIssue.Message)
	}
}

func TestServiceCollectEvidenceNormalizesResults(t *testing.T) {
	service := &Service{
		Registry: datasource.NewRegistry(
			fakeDataSource{
				name:      "prometheus",
				queryType: domain.QueryTypeMetric,
				records: []map[string]any{
					{"metric": "http_requests_total", "value": "0.95"},
				},
			},
			fakeDataSource{
				name:      "kubernetes-events",
				queryType: domain.QueryTypeEvent,
				records: []map[string]any{
					{"reason": "BackOff", "message": "container crashed"},
				},
			},
		),
	}

	result, err := service.CollectEvidence(context.Background(), v1alpha1.InvestigationRequestSpec{
		TimeRange: v1alpha1.InvestigationTimeRange{Lookback: metav1.Duration{Duration: 15 * time.Minute}},
	}, PreflightResult{
		Target: domain.ResourceRef{
			Namespace: "prod",
			Name:      "open-api",
			Kind:      "Deployment",
			Service:   "open-api",
		},
		Labels: map[string]string{"app": "open-api"},
		DatasourceNames: []string{
			"prometheus",
			"kubernetes-events",
		},
		CollectionPlan: []CollectionStep{
			{
				Name:           "prometheus",
				DatasourceName: "prometheus",
				QueryType:      domain.QueryTypeMetric,
				Query:          "metric-query",
			},
			{
				Name:           "kubernetes-events",
				DatasourceName: "kubernetes-events",
				QueryType:      domain.QueryTypeEvent,
				Query:          "recent-events",
			},
		},
	}, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("collect evidence failed: %v", err)
	}
	if result.Issue != nil {
		t.Fatalf("expected no issue, got %#v", result.Issue)
	}
	if len(result.EvidenceRefs) != 2 {
		t.Fatalf("expected two evidence refs, got %#v", result.EvidenceRefs)
	}
	if result.EvidenceRefs[0].Kind != "metric" {
		t.Fatalf("expected first evidence kind metric, got %#v", result.EvidenceRefs[0])
	}
	if result.EvidenceRefs[1].Kind != "event" || result.EvidenceRefs[1].Reason != "BackOff" {
		t.Fatalf("expected event evidence with reason BackOff, got %#v", result.EvidenceRefs[1])
	}
	if result.Summary != "collected 2 evidence records from 2 investigation queries" {
		t.Fatalf("unexpected summary %q", result.Summary)
	}
}

func TestServiceCollectEvidenceStoresNormalizedSnapshots(t *testing.T) {
	storeRoot := t.TempDir()
	service := &Service{
		Registry: datasource.NewRegistry(
			fakeDataSource{
				name:      "prometheus",
				queryType: domain.QueryTypeMetric,
				records: []map[string]any{
					{"metric": "http_requests_total", "value": "0.95"},
				},
			},
		),
		EvidenceStore: evidencepkg.LocalFilesystemStore{Root: storeRoot},
	}
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	result, err := service.CollectEvidence(context.Background(), v1alpha1.InvestigationRequestSpec{
		TimeRange: v1alpha1.InvestigationTimeRange{Lookback: metav1.Duration{Duration: 15 * time.Minute}},
		EvidenceRetention: v1alpha1.EvidenceRetentionPolicy{
			Mode:      v1alpha1.EvidenceRetentionModeNormalizedSnapshot,
			Retention: metav1.Duration{Duration: time.Hour},
			StorageRef: v1alpha1.LocalObjectReference{
				Name: evidencepkg.LocalFilesystemStoreName,
			},
		},
	}, PreflightResult{
		Target: domain.ResourceRef{Namespace: "prod", Name: "open-api", Kind: "Deployment"},
		Labels: map[string]string{"app": "open-api"},
		CollectionPlan: []CollectionStep{
			{
				Name:           "prometheus",
				DatasourceName: "prometheus",
				QueryType:      domain.QueryTypeMetric,
				Query:          "metric-query",
			},
		},
	}, now)
	if err != nil {
		t.Fatalf("collect evidence failed: %v", err)
	}
	if result.Issue != nil {
		t.Fatalf("expected no issue, got %#v", result.Issue)
	}
	if len(result.EvidenceRefs) != 1 || result.EvidenceRefs[0].PayloadRef == nil {
		t.Fatalf("expected payloadRef on retained evidence, got %#v", result.EvidenceRefs)
	}
	payloadRef := result.EvidenceRefs[0].PayloadRef
	if payloadRef.Scheme != "file" || !strings.HasPrefix(payloadRef.Digest, "sha256:") || payloadRef.RetentionClass != "normalized-snapshot" {
		t.Fatalf("unexpected payloadRef metadata: %#v", payloadRef)
	}
	if payloadRef.ExpiresAt == nil || !payloadRef.ExpiresAt.Time.Equal(now.Add(time.Hour)) {
		t.Fatalf("expected retention expiry, got %#v", payloadRef)
	}
	files, err := os.ReadDir(storeRoot)
	if err != nil {
		t.Fatalf("read evidence store: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected one snapshot file, got %d", len(files))
	}
}

func TestServiceCollectEvidenceRequiresConfiguredSnapshotStore(t *testing.T) {
	service := &Service{
		Registry: datasource.NewRegistry(
			fakeDataSource{
				name:      "prometheus",
				queryType: domain.QueryTypeMetric,
				records:   []map[string]any{{"metric": "http_requests_total", "value": "0.95"}},
			},
		),
	}
	result, err := service.CollectEvidence(context.Background(), v1alpha1.InvestigationRequestSpec{
		EvidenceRetention: v1alpha1.EvidenceRetentionPolicy{
			Mode: v1alpha1.EvidenceRetentionModeNormalizedSnapshot,
			StorageRef: v1alpha1.LocalObjectReference{
				Name: evidencepkg.LocalFilesystemStoreName,
			},
		},
	}, PreflightResult{
		Target: domain.ResourceRef{Namespace: "prod", Name: "open-api", Kind: "Deployment"},
		Labels: map[string]string{"app": "open-api"},
		CollectionPlan: []CollectionStep{
			{Name: "prometheus", DatasourceName: "prometheus", QueryType: domain.QueryTypeMetric, Query: "metric-query"},
		},
	}, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("collect evidence failed: %v", err)
	}
	if result.Issue == nil || result.Issue.Reason != "EvidenceRetentionStoreUnavailable" {
		t.Fatalf("expected store unavailable issue, got %#v", result.Issue)
	}
}

func TestServiceCollectEvidenceRejectsCumulativeResponseBudget(t *testing.T) {
	service := &Service{
		Registry: datasource.NewRegistry(
			fakeDataSource{
				name:      "loki",
				queryType: domain.QueryTypeLog,
				records: []map[string]any{
					{"line": "large response body"},
				},
			},
		),
	}

	result, err := service.CollectEvidence(context.Background(), v1alpha1.InvestigationRequestSpec{
		TimeRange: v1alpha1.InvestigationTimeRange{Lookback: metav1.Duration{Duration: 15 * time.Minute}},
		QueryBudget: v1alpha1.InvestigationQueryBudget{
			MaxCumulativeResponseBytes: 1,
		},
	}, PreflightResult{
		Target: domain.ResourceRef{Namespace: "prod", Name: "open-api", Kind: "Deployment"},
		Labels: map[string]string{"app": "open-api"},
		CollectionPlan: []CollectionStep{
			{
				Name:           "loki",
				DatasourceName: "loki",
				QueryType:      domain.QueryTypeLog,
				Query:          `{app="open-api"}`,
			},
		},
	}, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("collect evidence failed: %v", err)
	}
	if result.Issue == nil || result.Issue.Reason != "QueryBudgetExceeded" {
		t.Fatalf("expected QueryBudgetExceeded issue, got %#v", result.Issue)
	}
	if !strings.Contains(result.Issue.Message, "queryBudget.maxCumulativeResponseBytes") {
		t.Fatalf("expected response-byte budget message, got %q", result.Issue.Message)
	}
	if len(result.Observations) != 0 || len(result.EvidenceRefs) != 0 {
		t.Fatalf("expected no retained evidence after budget rejection, got observations=%#v refs=%#v", result.Observations, result.EvidenceRefs)
	}
}

func TestServiceCollectEvidenceRejectsCumulativeDurationBudget(t *testing.T) {
	service := &Service{
		Registry: datasource.NewRegistry(
			fakeDataSource{
				name:      "prometheus",
				queryType: domain.QueryTypeMetric,
				records: []map[string]any{
					{"metric": "http_requests_total", "value": "1"},
				},
				delay: time.Millisecond,
			},
		),
	}

	result, err := service.CollectEvidence(context.Background(), v1alpha1.InvestigationRequestSpec{
		TimeRange: v1alpha1.InvestigationTimeRange{Lookback: metav1.Duration{Duration: 15 * time.Minute}},
		QueryBudget: v1alpha1.InvestigationQueryBudget{
			MaxCumulativeDuration: metav1.Duration{Duration: time.Nanosecond},
		},
	}, PreflightResult{
		Target: domain.ResourceRef{Namespace: "prod", Name: "open-api", Kind: "Deployment"},
		Labels: map[string]string{"app": "open-api"},
		CollectionPlan: []CollectionStep{
			{
				Name:           "prometheus",
				DatasourceName: "prometheus",
				QueryType:      domain.QueryTypeMetric,
				Query:          "up",
			},
		},
	}, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("collect evidence failed: %v", err)
	}
	if result.Issue == nil || result.Issue.Reason != "QueryBudgetExceeded" {
		t.Fatalf("expected QueryBudgetExceeded issue, got %#v", result.Issue)
	}
	if !strings.Contains(result.Issue.Message, "queryBudget.maxCumulativeDuration") {
		t.Fatalf("expected duration budget message, got %q", result.Issue.Message)
	}
	if len(result.Observations) != 0 || len(result.EvidenceRefs) != 0 {
		t.Fatalf("expected no retained evidence after budget rejection, got observations=%#v refs=%#v", result.Observations, result.EvidenceRefs)
	}
}

func TestServiceCollectEvidenceEnforcesMaxConcurrentQueries(t *testing.T) {
	blocking := newBlockingDataSource("loki", domain.QueryTypeLog)
	defer blocking.release()
	queueBefore := testutil.ToFloat64(rcametrics.DatasourceQueryQueueDepth.WithLabelValues("investigation"))
	inFlightBefore := testutil.ToFloat64(rcametrics.DatasourceQueriesInFlight.WithLabelValues("investigation"))
	service := &Service{
		Registry: datasource.NewRegistry(blocking),
	}
	done := make(chan EvidenceCollectionResult, 1)
	errs := make(chan error, 1)

	go func() {
		result, err := service.CollectEvidence(context.Background(), v1alpha1.InvestigationRequestSpec{
			TimeRange: v1alpha1.InvestigationTimeRange{Lookback: metav1.Duration{Duration: 15 * time.Minute}},
			QueryBudget: v1alpha1.InvestigationQueryBudget{
				MaxConcurrentQueries: 2,
			},
		}, PreflightResult{
			Target: domain.ResourceRef{Namespace: "prod", Name: "open-api", Kind: "Deployment"},
			Labels: map[string]string{"app": "open-api"},
			CollectionPlan: []CollectionStep{
				{Name: "first", DatasourceName: "loki", QueryType: domain.QueryTypeLog, Query: "first"},
				{Name: "second", DatasourceName: "loki", QueryType: domain.QueryTypeLog, Query: "second"},
				{Name: "third", DatasourceName: "loki", QueryType: domain.QueryTypeLog, Query: "third"},
			},
		}, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))
		if err != nil {
			errs <- err
			return
		}
		done <- result
	}()

	blocking.waitForStarted(t, 2)
	blocking.assertNoAdditionalStart(t)
	queueDuring := testutil.ToFloat64(rcametrics.DatasourceQueryQueueDepth.WithLabelValues("investigation"))
	inFlightDuring := testutil.ToFloat64(rcametrics.DatasourceQueriesInFlight.WithLabelValues("investigation"))
	if queueDuring != queueBefore+1 {
		t.Fatalf("expected one queued datasource query, before=%f during=%f", queueBefore, queueDuring)
	}
	if inFlightDuring != inFlightBefore+2 {
		t.Fatalf("expected two in-flight datasource queries, before=%f during=%f", inFlightBefore, inFlightDuring)
	}
	blocking.release()

	select {
	case err := <-errs:
		t.Fatalf("collect evidence failed: %v", err)
	case result := <-done:
		if result.Issue != nil {
			t.Fatalf("expected no issue, got %#v", result.Issue)
		}
		if blocking.maxActive() != 2 {
			t.Fatalf("expected max active queries 2, got %d", blocking.maxActive())
		}
		if len(result.Observations) != 3 {
			t.Fatalf("expected three observations, got %#v", result.Observations)
		}
		got := []string{result.Observations[0].Summary, result.Observations[1].Summary, result.Observations[2].Summary}
		want := []string{"first", "second", "third"}
		for index := range want {
			if got[index] != want[index] {
				t.Fatalf("expected deterministic observation order %#v, got %#v", want, got)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for evidence collection")
	}
	queueAfter := testutil.ToFloat64(rcametrics.DatasourceQueryQueueDepth.WithLabelValues("investigation"))
	inFlightAfter := testutil.ToFloat64(rcametrics.DatasourceQueriesInFlight.WithLabelValues("investigation"))
	if queueAfter != queueBefore {
		t.Fatalf("expected datasource query queue depth to return to baseline, before=%f after=%f", queueBefore, queueAfter)
	}
	if inFlightAfter != inFlightBefore {
		t.Fatalf("expected datasource queries in-flight to return to baseline, before=%f after=%f", inFlightBefore, inFlightAfter)
	}
}

func TestServiceCollectEvidenceAppliesQueryResultLimits(t *testing.T) {
	tests := []struct {
		name             string
		queryType        domain.QueryType
		records          []map[string]any
		resultLimits     v1alpha1.QueryResultLimits
		wantObservations int
		wantTruncated    bool
	}{
		{
			name:      "metrics",
			queryType: domain.QueryTypeMetric,
			records: []map[string]any{
				{"metric": "http_requests_total", "value": "1"},
				{"metric": "http_requests_total", "value": "2"},
				{"metric": "http_requests_total", "value": "3"},
			},
			resultLimits: v1alpha1.QueryResultLimits{
				Metrics: v1alpha1.MetricResultLimits{MaxSamples: 2},
			},
			wantObservations: 2,
			wantTruncated:    true,
		},
		{
			name:      "logs",
			queryType: domain.QueryTypeLog,
			records: []map[string]any{
				{"line": "line 1"},
				{"line": "line 2"},
				{"line": "line 3"},
			},
			resultLimits: v1alpha1.QueryResultLimits{
				Logs: v1alpha1.LogResultLimits{MaxLines: 2},
			},
			wantObservations: 2,
			wantTruncated:    true,
		},
		{
			name:      "metrics-strictest-positive-limit",
			queryType: domain.QueryTypeMetric,
			records: []map[string]any{
				{"metric": "http_requests_total", "value": "1"},
				{"metric": "http_requests_total", "value": "2"},
				{"metric": "http_requests_total", "value": "3"},
			},
			resultLimits: v1alpha1.QueryResultLimits{
				Metrics: v1alpha1.MetricResultLimits{MaxSeries: 1, MaxSamples: 3},
			},
			wantObservations: 1,
			wantTruncated:    true,
		},
		{
			name:      "events",
			queryType: domain.QueryTypeEvent,
			records: []map[string]any{
				{"reason": "BackOff", "message": "event 1"},
				{"reason": "BackOff", "message": "event 2"},
				{"reason": "BackOff", "message": "event 3"},
			},
			resultLimits: v1alpha1.QueryResultLimits{
				Events: v1alpha1.EventResultLimits{MaxRecords: 2},
			},
			wantObservations: 2,
			wantTruncated:    true,
		},
		{
			name:      "unset",
			queryType: domain.QueryTypeLog,
			records: []map[string]any{
				{"line": "line 1"},
				{"line": "line 2"},
				{"line": "line 3"},
			},
			wantObservations: 3,
			wantTruncated:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &Service{
				Registry: datasource.NewRegistry(
					fakeDataSource{
						name:      tt.name,
						queryType: tt.queryType,
						records:   tt.records,
					},
				),
			}

			result, err := service.CollectEvidence(context.Background(), v1alpha1.InvestigationRequestSpec{
				TimeRange: v1alpha1.InvestigationTimeRange{Lookback: metav1.Duration{Duration: 15 * time.Minute}},
				QueryBudget: v1alpha1.InvestigationQueryBudget{
					ResultLimits: tt.resultLimits,
				},
			}, PreflightResult{
				Target: domain.ResourceRef{Namespace: "prod", Name: "open-api", Kind: "Deployment"},
				Labels: map[string]string{"app": "open-api"},
				CollectionPlan: []CollectionStep{
					{Name: tt.name, DatasourceName: tt.name, QueryType: tt.queryType, Query: tt.name},
				},
			}, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatalf("collect evidence failed: %v", err)
			}
			if result.Issue != nil {
				t.Fatalf("expected no issue, got %#v", result.Issue)
			}
			if len(result.Observations) != tt.wantObservations {
				t.Fatalf("expected %d observations, got %#v", tt.wantObservations, result.Observations)
			}
			for _, observation := range result.Observations {
				if observation.Truncated != tt.wantTruncated {
					t.Fatalf("expected truncated=%v, got %#v", tt.wantTruncated, observation)
				}
				if observation.OriginalCount != len(tt.records) || observation.RetainedCount != tt.wantObservations {
					t.Fatalf("expected count metadata original=%d retained=%d, got %#v", len(tt.records), tt.wantObservations, observation)
				}
			}
		})
	}
}

func TestServiceCollectEvidencePreservesNativeResultLimitMetadata(t *testing.T) {
	limit := &datasource.NativeResultLimit{
		Reason:        "NativeResultLimitExceeded",
		Dimension:     "samples",
		Limit:         2,
		OriginalCount: 5,
		RetainedCount: 2,
	}
	before := testutil.ToFloat64(rcametrics.QueryResultLimitExceededTotal.WithLabelValues("metric", "samples"))
	service := &Service{
		Registry: datasource.NewRegistry(
			fakeDataSource{
				name:        "prometheus",
				queryType:   domain.QueryTypeMetric,
				records:     []map[string]any{{"metric": "latency", "value": "1"}, {"metric": "latency", "value": "2"}},
				nativeLimit: limit,
			},
		),
	}

	result, err := service.CollectEvidence(context.Background(), v1alpha1.InvestigationRequestSpec{
		TimeRange: v1alpha1.InvestigationTimeRange{Lookback: metav1.Duration{Duration: 15 * time.Minute}},
		QueryBudget: v1alpha1.InvestigationQueryBudget{
			ResultLimits: v1alpha1.QueryResultLimits{
				Metrics: v1alpha1.MetricResultLimits{MaxSamples: 2},
			},
		},
	}, PreflightResult{
		Target: domain.ResourceRef{Namespace: "prod", Name: "open-api", Kind: "Deployment"},
		Labels: map[string]string{"app": "open-api"},
		CollectionPlan: []CollectionStep{
			{Name: "prometheus", DatasourceName: "prometheus", QueryType: domain.QueryTypeMetric, Query: "latency"},
		},
	}, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("collect evidence failed: %v", err)
	}
	if result.Issue != nil {
		t.Fatalf("expected no issue, got %#v", result.Issue)
	}
	if len(result.EvidenceRefs) != 2 {
		t.Fatalf("expected two evidence refs, got %#v", result.EvidenceRefs)
	}
	ref := result.EvidenceRefs[0]
	if !ref.Truncated || ref.TruncationReason != "NativeResultLimitExceeded" || ref.LimitDimension != "samples" || ref.Limit != 2 {
		t.Fatalf("expected native truncation metadata, got %#v", ref)
	}
	if ref.OriginalCount != 5 || ref.RetainedCount != 2 {
		t.Fatalf("expected native original/retained counts, got %#v", ref)
	}
	after := testutil.ToFloat64(rcametrics.QueryResultLimitExceededTotal.WithLabelValues("metric", "samples"))
	if after != before+1 {
		t.Fatalf("expected native result limit metric increment, before=%f after=%f", before, after)
	}
}

func TestServiceCollectEvidenceAppliesDataSourceClassification(t *testing.T) {
	service := &Service{
		Registry: datasource.NewRegistry(
			fakeDataSource{
				name:      "prometheus",
				queryType: domain.QueryTypeMetric,
				records:   []map[string]any{{"metric": "latency", "value": "1"}},
				classification: v1alpha1.DataClassification{
					Level:           v1alpha1.DataClassificationLevelConfidential,
					SensitivityTags: []string{v1alpha1.SensitivityTagCustomerData},
					Source:          v1alpha1.DataClassificationSourceExplicit,
					PolicyVersion:   v1alpha1.DataClassificationPolicyVersion,
				},
			},
		),
	}

	result, err := service.CollectEvidence(context.Background(), v1alpha1.InvestigationRequestSpec{
		TimeRange: v1alpha1.InvestigationTimeRange{Lookback: metav1.Duration{Duration: 15 * time.Minute}},
	}, PreflightResult{
		Target: domain.ResourceRef{Namespace: "prod", Name: "open-api", Kind: "Deployment"},
		Labels: map[string]string{"app": "open-api"},
		CollectionPlan: []CollectionStep{
			{Name: "prometheus", DatasourceName: "prometheus", QueryType: domain.QueryTypeMetric, Query: "latency"},
		},
	}, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("collect evidence failed: %v", err)
	}
	if len(result.EvidenceRefs) != 1 || result.EvidenceRefs[0].Classification == nil {
		t.Fatalf("expected classified evidence ref, got %#v", result.EvidenceRefs)
	}
	classification := result.EvidenceRefs[0].Classification
	if classification.Level != v1alpha1.DataClassificationLevelConfidential || !stringSliceContains(classification.SensitivityTags, v1alpha1.SensitivityTagCustomerData) {
		t.Fatalf("expected datasource classification to be inherited, got %#v", classification)
	}
}

func TestServiceCollectEvidenceBuildsNormalizedObservations(t *testing.T) {
	service := &Service{
		Registry: datasource.NewRegistry(
			fakeDataSource{
				name:      "loki",
				queryType: domain.QueryTypeLog,
				records: []map[string]any{
					{"line": "timeout token=secret-one"},
					{"line": "retry token=secret-two"},
					{"line": "rate limit token=secret-three"},
					{"line": "connection refused token=secret-four"},
					{"line": "pool exhausted token=secret-five"},
					{"line": "extra line token=secret-six"},
				},
			},
		),
	}

	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	result, err := service.CollectEvidence(context.Background(), v1alpha1.InvestigationRequestSpec{
		TimeRange: v1alpha1.InvestigationTimeRange{Lookback: metav1.Duration{Duration: 10 * time.Minute}},
	}, PreflightResult{
		Target: domain.ResourceRef{
			Namespace: "prod",
			Name:      "open-api",
			Kind:      "Deployment",
			Service:   "open-api",
		},
		Labels: map[string]string{"app": "open-api"},
		CollectionPlan: []CollectionStep{
			{
				Name:           "timeout-logs",
				DatasourceName: "loki",
				QueryType:      domain.QueryTypeLog,
				Query:          `{namespace="prod",app="open-api"} |= "timeout"`,
			},
		},
	}, now)
	if err != nil {
		t.Fatalf("collect evidence failed: %v", err)
	}
	if result.Issue != nil {
		t.Fatalf("expected no issue, got %#v", result.Issue)
	}
	if len(result.Observations) != 5 {
		t.Fatalf("expected five retained observations, got %#v", result.Observations)
	}
	if len(result.EvidenceRefs) != len(result.Observations) {
		t.Fatalf("expected evidence refs to match observations, got refs=%d observations=%d", len(result.EvidenceRefs), len(result.Observations))
	}
	first := result.Observations[0]
	if first.ID != "evidence-001" {
		t.Fatalf("expected stable observation id evidence-001, got %#v", first)
	}
	if first.SchemaVersion != "observation.fluxagent.io/v1alpha1" {
		t.Fatalf("expected schema version, got %#v", first)
	}
	if first.Type != domain.ObservationTypeLog || first.Value.Log == nil {
		t.Fatalf("expected log observation, got %#v", first)
	}
	if strings.Contains(first.Summary, "secret-one") || !strings.Contains(first.Summary, "[REDACTED]") {
		t.Fatalf("expected redacted summary before digest, got %q", first.Summary)
	}
	if !strings.HasPrefix(first.QueryDigest, "sha256:") || len(first.QueryDigest) != len("sha256:")+64 {
		t.Fatalf("expected sha256 query digest, got %q", first.QueryDigest)
	}
	if !strings.HasPrefix(first.ContentDigest, "sha256:") || len(first.ContentDigest) != len("sha256:")+64 {
		t.Fatalf("expected sha256 content digest, got %q", first.ContentDigest)
	}
	if first.DigestAlgorithm != "sha256" || first.DigestCanonicalization != "fluxagent-observation-json-v1" {
		t.Fatalf("expected observation digest metadata, got %#v", first)
	}
	if !first.Truncated || first.OriginalCount != 6 || first.RetainedCount != 5 {
		t.Fatalf("expected truncation metadata, got %#v", first)
	}
	if !first.TimeRange.Start.Equal(now.Add(-10*time.Minute)) || !first.TimeRange.End.Equal(now) {
		t.Fatalf("expected evidence time range from lookback, got %#v", first.TimeRange)
	}

	ref := result.EvidenceRefs[0]
	if ref.ID != first.ID || ref.QueryDigest != first.QueryDigest || ref.ContentDigest != first.ContentDigest {
		t.Fatalf("expected evidence ref to carry observation metadata, got ref=%#v observation=%#v", ref, first)
	}
	if ref.DigestAlgorithm != first.DigestAlgorithm || ref.DigestCanonicalization != first.DigestCanonicalization {
		t.Fatalf("expected evidence ref digest metadata, got ref=%#v observation=%#v", ref, first)
	}
	if ref.RedactionProfile != "default-v1" || !ref.Truncated || ref.OriginalCount != 6 || ref.RetainedCount != 5 {
		t.Fatalf("expected evidence ref truncation and redaction metadata, got %#v", ref)
	}
	if ref.CollectedAt == nil || !ref.CollectedAt.Time.Equal(now) {
		t.Fatalf("expected evidence ref collectedAt %s, got %#v", now.Format(time.RFC3339), ref.CollectedAt)
	}
}

func TestNormalizeObservationContentDigestExcludesCollectedAt(t *testing.T) {
	req := datasource.QueryRequest{
		Query:     `{namespace="prod"} |= "timeout"`,
		StartTime: time.Date(2026, 7, 6, 11, 50, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC),
		Target:    domain.ResourceRef{Namespace: "prod", Name: "open-api", Service: "open-api"},
		QueryType: domain.QueryTypeLog,
	}
	result := &datasource.QueryResult{
		Source:    "loki",
		QueryType: domain.QueryTypeLog,
		Records:   []map[string]any{{"line": "timeout token=secret-one"}},
	}
	record := map[string]any{"line": "timeout token=secret-one"}

	first := normalizeObservation(record, result, req, 0, 1, 1, false, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))
	second := normalizeObservation(record, result, req, 0, 1, 1, false, time.Date(2026, 7, 6, 12, 1, 0, 0, time.UTC))

	if first.ContentDigest != second.ContentDigest {
		t.Fatalf("expected content digest to ignore collectedAt, got first=%s second=%s", first.ContentDigest, second.ContentDigest)
	}
	if first.CollectedAt.Equal(second.CollectedAt) {
		t.Fatalf("expected collectedAt to remain distinct, got first=%s second=%s", first.CollectedAt, second.CollectedAt)
	}
}

func TestNormalizeObservationTruncatesLargeLogEvidence(t *testing.T) {
	req := datasource.QueryRequest{
		Query:     `{namespace="prod"} |= "timeout"`,
		StartTime: time.Date(2026, 7, 6, 11, 50, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC),
		Target:    domain.ResourceRef{Namespace: "prod", Name: "open-api", Service: "open-api"},
		QueryType: domain.QueryTypeLog,
	}
	result := &datasource.QueryResult{
		Source:    "loki",
		QueryType: domain.QueryTypeLog,
		Records:   []map[string]any{{"line": strings.Repeat("延遲", 1024)}},
	}

	observation := normalizeObservations(result, req, 0, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))[0]
	ref := evidenceRefsFromObservations([]domain.Observation{observation}, req, v1alpha1.QueryRetentionPolicy{})[0]

	if !observation.Truncated || observation.OriginalBytes <= observation.RetainedBytes {
		t.Fatalf("expected truncated observation byte metadata, got %#v", observation)
	}
	if len(observation.Summary) > 1024 {
		t.Fatalf("expected bounded summary bytes, got %d", len(observation.Summary))
	}
	if ref.OriginalBytes != int32(observation.OriginalBytes) || ref.RetainedBytes != int32(observation.RetainedBytes) || !ref.Truncated {
		t.Fatalf("expected evidence ref byte metadata, got ref=%#v observation=%#v", ref, observation)
	}
}

func TestNormalizeObservationTruncatesLargeEventEvidence(t *testing.T) {
	req := datasource.QueryRequest{
		Query:     "recent-events",
		StartTime: time.Date(2026, 7, 6, 11, 50, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC),
		Target:    domain.ResourceRef{Namespace: "prod", Name: "open-api", Service: "open-api"},
		QueryType: domain.QueryTypeEvent,
	}
	result := &datasource.QueryResult{
		Source:    "kubernetes-events",
		QueryType: domain.QueryTypeEvent,
		Records:   []map[string]any{{"reason": "BackOff", "message": strings.Repeat("container failed ", 200)}},
	}

	observation := normalizeObservations(result, req, 0, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))[0]

	if !observation.Truncated || observation.Value.Event == nil || observation.Value.Event.Message != observation.Summary {
		t.Fatalf("expected truncated event message to match compact summary, got %#v", observation)
	}
	if len(observation.Summary) > 1024 {
		t.Fatalf("expected bounded event summary bytes, got %d", len(observation.Summary))
	}
}

func TestServiceCollectEvidencePreservesDatasourceQueryReason(t *testing.T) {
	service := &Service{
		Registry: datasource.NewRegistry(
			fakeDataSource{
				name:      "prometheus",
				queryType: domain.QueryTypeMetric,
				queryErr: &datasource.QueryError{
					Reason:  "DatasourceAuthFailed",
					Message: "prometheus datasource returned 401",
				},
			},
		),
	}

	result, err := service.CollectEvidence(context.Background(), v1alpha1.InvestigationRequestSpec{}, PreflightResult{
		Target: domain.ResourceRef{
			Namespace: "prod",
			Name:      "open-api",
			Kind:      "Deployment",
			Service:   "open-api",
		},
		Labels:          map[string]string{"app": "open-api"},
		DatasourceNames: []string{"prometheus"},
		CollectionPlan: []CollectionStep{
			{
				Name:           "prometheus",
				DatasourceName: "prometheus",
				QueryType:      domain.QueryTypeMetric,
				Query:          "metric-query",
			},
		},
	}, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("collect evidence failed: %v", err)
	}
	if result.Issue == nil || result.Issue.Reason != "DatasourceAuthFailed" {
		t.Fatalf("expected DatasourceAuthFailed issue, got %#v", result.Issue)
	}
}

func TestServiceCollectEvidenceUsesConfiguredQueryPlan(t *testing.T) {
	const configuredQuery = `sum(rate(custom_metric_total{namespace="prod"}[2m]))`
	prom := &capturingDataSource{
		name:      "prometheus",
		queryType: domain.QueryTypeMetric,
		records: []map[string]any{
			{"metric": "custom_metric", "value": "3.14"},
		},
	}
	service := &Service{
		Registry: datasource.NewRegistry(prom),
	}

	result, err := service.CollectEvidence(context.Background(), v1alpha1.InvestigationRequestSpec{}, PreflightResult{
		Target: domain.ResourceRef{
			Namespace: "prod",
			Name:      "open-api",
			Kind:      "Deployment",
			Service:   "open-api",
		},
		Labels: map[string]string{"app": "open-api"},
		CollectionPlan: []CollectionStep{
			{
				Name:           "custom-metric",
				DatasourceName: "prometheus",
				QueryType:      domain.QueryTypeMetric,
				Query:          configuredQuery,
			},
		},
	}, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("collect evidence failed: %v", err)
	}
	if prom.lastQuery == nil || prom.lastQuery.Query != configuredQuery {
		t.Fatalf("expected configured query to be used, got %#v", prom.lastQuery)
	}
	if len(result.EvidenceRefs) != 1 || result.EvidenceRefs[0].Query != "" || result.EvidenceRefs[0].QueryDigest == "" {
		t.Fatalf("expected evidence to retain query digest only by default, got %#v", result.EvidenceRefs)
	}
}

func TestServiceCollectEvidencePassesAndEnforcesEventReasons(t *testing.T) {
	events := &capturingDataSource{
		name:      "kubernetes-events",
		queryType: domain.QueryTypeEvent,
		records: []map[string]any{
			{
				"reason":  "Scheduled",
				"message": "Successfully assigned pod; message mentions Unhealthy but reason is benign",
			},
			{
				"reason":  "Unhealthy",
				"message": "Readiness probe failed",
			},
		},
	}
	service := &Service{
		Registry: datasource.NewRegistry(events),
	}

	result, err := service.CollectEvidence(context.Background(), v1alpha1.InvestigationRequestSpec{}, PreflightResult{
		Target: domain.ResourceRef{
			Namespace: "prod",
			Name:      "open-api",
			Kind:      "Deployment",
			Service:   "open-api",
		},
		CollectionPlan: []CollectionStep{
			{
				Name:           "unhealthy-events",
				DatasourceName: "kubernetes-events",
				QueryType:      domain.QueryTypeEvent,
				Query:          "recent-events",
				Reasons:        []string{"Unhealthy"},
			},
		},
	}, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("collect evidence failed: %v", err)
	}
	if events.lastQuery == nil || len(events.lastQuery.Reasons) != 1 || events.lastQuery.Reasons[0] != "Unhealthy" {
		t.Fatalf("expected datasource request to carry reasons, got %#v", events.lastQuery)
	}
	if len(result.EvidenceRefs) != 1 || result.EvidenceRefs[0].Reason != "Unhealthy" {
		t.Fatalf("expected post-query exact reason filter to retain only Unhealthy, got %#v", result.EvidenceRefs)
	}
}

func TestServiceCollectEvidenceHonorsQueryRetentionPolicy(t *testing.T) {
	const secretQuery = `sum(rate(custom_metric_total{namespace="prod",token=super-secret}[2m]))`
	tests := []struct {
		name             string
		retention        v1alpha1.QueryRetentionPolicy
		wantQuery        string
		wantQueryPresent bool
		wantRedacted     bool
	}{
		{
			name:      "digest only",
			retention: v1alpha1.QueryRetentionPolicy{Mode: v1alpha1.QueryRetentionModeDigestOnly},
			wantQuery: "",
		},
		{
			name:             "full",
			retention:        v1alpha1.QueryRetentionPolicy{Mode: v1alpha1.QueryRetentionModeFull},
			wantQuery:        secretQuery,
			wantQueryPresent: true,
		},
		{
			name:         "redacted",
			retention:    v1alpha1.QueryRetentionPolicy{Mode: v1alpha1.QueryRetentionModeRedacted},
			wantRedacted: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prom := &capturingDataSource{
				name:      "prometheus",
				queryType: domain.QueryTypeMetric,
				records:   []map[string]any{{"metric": "custom_metric", "value": "3.14"}},
			}
			service := &Service{Registry: datasource.NewRegistry(prom)}
			result, err := service.CollectEvidence(context.Background(), v1alpha1.InvestigationRequestSpec{
				QueryRetention: tt.retention,
			}, PreflightResult{
				Target: domain.ResourceRef{
					Namespace: "prod",
					Name:      "open-api",
					Kind:      "Deployment",
					Service:   "open-api",
				},
				Labels: map[string]string{"app": "open-api"},
				CollectionPlan: []CollectionStep{{
					Name:           "custom-metric",
					DatasourceName: "prometheus",
					QueryType:      domain.QueryTypeMetric,
					Query:          secretQuery,
				}},
			}, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatalf("collect evidence failed: %v", err)
			}
			if len(result.EvidenceRefs) != 1 {
				t.Fatalf("expected one evidence ref, got %#v", result.EvidenceRefs)
			}
			if !tt.wantRedacted && result.EvidenceRefs[0].Query != tt.wantQuery {
				t.Fatalf("expected retained query %q, got %q", tt.wantQuery, result.EvidenceRefs[0].Query)
			}
			if tt.wantQueryPresent && result.EvidenceRefs[0].Query == "" {
				t.Fatal("expected query to be retained")
			}
			if tt.wantRedacted {
				if strings.Contains(result.EvidenceRefs[0].Query, "super-secret") || !strings.Contains(result.EvidenceRefs[0].Query, "[REDACTED]") {
					t.Fatalf("expected redacted retained query, got %q", result.EvidenceRefs[0].Query)
				}
			}
			if result.EvidenceRefs[0].QueryDigest == "" {
				t.Fatalf("expected query digest to remain available, got %#v", result.EvidenceRefs[0])
			}
		})
	}
}

func TestServicePreflightReportsCapabilityMismatchForConfiguredQueries(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add apps scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add aiops scheme: %v", err)
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			&appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: "open-api", Namespace: "prod"},
			},
		).
		Build()

	service := &Service{
		Client: client,
		Registry: datasource.NewRegistry(
			fakeDataSource{name: "kubernetes-events", queryType: domain.QueryTypeEvent},
		),
	}

	result, err := service.Preflight(context.Background(), "prod", v1alpha1.InvestigationRequestSpec{
		Target: v1alpha1.TargetRef{
			Namespace: "prod",
			Kind:      "Deployment",
			Name:      "open-api",
		},
		Queries: []v1alpha1.InvestigationQuery{
			{
				Name: "bad-query",
				DatasourceRef: v1alpha1.LocalObjectReference{
					Name: "kubernetes-events",
				},
				QueryType: "metric",
				Query:     "up",
			},
		},
	})
	if err != nil {
		t.Fatalf("preflight failed: %v", err)
	}
	if result.QueryTypeIssue == nil || result.QueryTypeIssue.Reason != "CapabilityMismatch" {
		t.Fatalf("expected CapabilityMismatch, got %#v", result.QueryTypeIssue)
	}
}

func TestServiceGenerateRCAReturnsReasoningOutput(t *testing.T) {
	service := &Service{
		Gateway: &modelgateway.Gateway{
			Base: knowledge.NewBase(),
			Providers: model.NewRegistry(
				heuristic.Provider{},
			),
		},
	}

	reasoning, err := service.GenerateRCA(context.Background(), v1alpha1.InvestigationRequestSpec{
		Question: "Why is open-api crashing after rollout?",
	}, PreflightResult{
		Target: domain.ResourceRef{
			Namespace: "prod",
			Name:      "open-api",
			Kind:      "Deployment",
			Service:   "open-api",
		},
		Provider: &v1alpha1.ModelProvider{
			ObjectMeta: metav1.ObjectMeta{Name: "heuristic-provider", Namespace: "fluxagent-system"},
			Spec: v1alpha1.ModelProviderSpec{
				Provider: "heuristic",
				Model:    "built-in",
			},
		},
	}, EvidenceCollectionResult{
		Summary: "collected 1 evidence records from 1 datasources",
		EvidenceRefs: []v1alpha1.EvidenceRef{
			{
				Kind:    "event",
				Source:  "kubernetes-events",
				Reason:  "BackOff",
				Summary: "container crashed",
			},
		},
	}, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("generate RCA failed: %v", err)
	}
	if reasoning.Issue != nil {
		t.Fatalf("expected no RCA issue, got %#v", reasoning.Issue)
	}
	if reasoning.Reasoning == nil {
		t.Fatal("expected reasoning output")
	}
	if reasoning.Reasoning.RCA.Hypothesis == "" {
		t.Fatalf("expected RCA hypothesis, got %#v", reasoning.Reasoning)
	}
	if reasoning.Reasoning.Confidence.Score <= 0 {
		t.Fatalf("expected positive confidence, got %#v", reasoning.Reasoning.Confidence)
	}
}

func TestServiceGenerateRCABlocksHostedProviderWithoutExternalTransmission(t *testing.T) {
	hosted := &capturingModelProvider{name: "openai"}
	service := &Service{
		Gateway: &modelgateway.Gateway{
			Base: knowledge.NewBase(),
			Providers: model.NewRegistry(
				hosted,
			),
		},
	}

	result, err := service.GenerateRCA(context.Background(), v1alpha1.InvestigationRequestSpec{}, PreflightResult{
		Target: domain.ResourceRef{Namespace: "prod", Name: "open-api", Kind: "Deployment"},
		Provider: &v1alpha1.ModelProvider{
			ObjectMeta: metav1.ObjectMeta{Name: "openai-provider", Namespace: "fluxagent-system"},
			Spec:       v1alpha1.ModelProviderSpec{Provider: "openai", Model: "test-model"},
		},
	}, EvidenceCollectionResult{
		Summary: "collected 1 evidence records from 1 datasources",
		EvidenceRefs: []v1alpha1.EvidenceRef{
			{Kind: "event", Source: "kubernetes-events", Summary: "BackOff"},
		},
	}, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("generate RCA failed: %v", err)
	}
	if result.Issue == nil || result.Issue.Reason != "ProviderDataPolicyDenied" {
		t.Fatalf("expected ProviderDataPolicyDenied issue, got %#v", result.Issue)
	}
	if hosted.calls != 0 {
		t.Fatalf("expected hosted provider not to be called, got %d", hosted.calls)
	}
	if len(result.EgressAttempts) != 1 {
		t.Fatalf("expected one rejected egress attempt, got %#v", result.EgressAttempts)
	}
	if result.EgressAttempts[0].Decision != "Rejected" || result.EgressAttempts[0].Result != "Rejected" {
		t.Fatalf("expected rejected hosted attempt, got %#v", result.EgressAttempts[0])
	}
}

func TestServiceGenerateRCARecordsFallbackProviderEgressAttempts(t *testing.T) {
	hosted := &capturingModelProvider{name: "openai"}
	service := &Service{
		Gateway: &modelgateway.Gateway{
			Base: knowledge.NewBase(),
			Providers: model.NewRegistry(
				failingModelProvider{name: "broken", reason: "ProviderUnavailable"},
				hosted,
			),
			Resolver: serviceResolverStub{
				providers: map[string]*v1alpha1.ModelProvider{
					"fluxagent-system/fallback-openai": {
						ObjectMeta: metav1.ObjectMeta{Name: "fallback-openai", Namespace: "fluxagent-system", Generation: 7},
						Spec: v1alpha1.ModelProviderSpec{
							Provider: "openai",
							Model:    "gpt-test",
						},
					},
				},
			},
		},
	}

	result, err := service.GenerateRCA(context.Background(), v1alpha1.InvestigationRequestSpec{}, PreflightResult{
		Target: domain.ResourceRef{Namespace: "prod", Name: "open-api", Kind: "Deployment"},
		Provider: &v1alpha1.ModelProvider{
			ObjectMeta: metav1.ObjectMeta{Name: "primary-broken", Namespace: "fluxagent-system", Generation: 3},
			Spec: v1alpha1.ModelProviderSpec{
				Provider: "broken",
				Model:    "broken-model",
				FallbackProviderRef: v1alpha1.LocalObjectReference{
					Name: "fallback-openai",
				},
			},
		},
	}, EvidenceCollectionResult{
		Summary: "collected 1 evidence records from 1 datasource",
		EvidenceRefs: []v1alpha1.EvidenceRef{
			{Kind: "metric", Source: "prometheus", Summary: "latency high"},
		},
	}, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("generate RCA failed: %v", err)
	}
	if result.Issue == nil || result.Issue.Reason != "ProviderDataPolicyDenied" {
		t.Fatalf("expected fallback ProviderDataPolicyDenied issue, got %#v", result.Issue)
	}
	if hosted.calls != 0 {
		t.Fatalf("expected fallback hosted provider not to be called, got %d", hosted.calls)
	}
	if len(result.EgressAttempts) != 2 {
		t.Fatalf("expected primary and fallback attempts, got %#v", result.EgressAttempts)
	}
	if result.EgressAttempts[0].ProviderRef == nil || result.EgressAttempts[0].ProviderRef.Name != "primary-broken" || result.EgressAttempts[0].Result != "ProviderUnavailable" {
		t.Fatalf("expected primary unavailable attempt, got %#v", result.EgressAttempts[0])
	}
	fallbackAttempt := result.EgressAttempts[1]
	if fallbackAttempt.ProviderRef == nil || fallbackAttempt.ProviderRef.Name != "fallback-openai" {
		t.Fatalf("expected fallback provider ref, got %#v", fallbackAttempt.ProviderRef)
	}
	if fallbackAttempt.Decision != "Rejected" || fallbackAttempt.Result != "ProviderDataPolicyDenied" || fallbackAttempt.Reason != "ProviderDataPolicyDenied" {
		t.Fatalf("expected fallback rejected policy attempt, got %#v", fallbackAttempt)
	}
	if fallbackAttempt.ProviderGeneration != 7 || fallbackAttempt.EvidenceBundleDigest == "" {
		t.Fatalf("expected fallback generation and bundle digest, got %#v", fallbackAttempt)
	}
}

func TestServiceGenerateRCAReevaluatesFallbackProviderClassificationPolicy(t *testing.T) {
	hosted := &capturingModelProvider{name: "openai"}
	service := &Service{
		Gateway: &modelgateway.Gateway{
			Base: knowledge.NewBase(),
			Providers: model.NewRegistry(
				failingModelProvider{name: "broken", reason: "ProviderUnavailable"},
				hosted,
			),
			Resolver: serviceResolverStub{
				providers: map[string]*v1alpha1.ModelProvider{
					"fluxagent-system/fallback-openai": {
						ObjectMeta: metav1.ObjectMeta{Name: "fallback-openai", Namespace: "fluxagent-system", Generation: 9},
						Spec: v1alpha1.ModelProviderSpec{
							Provider: "openai",
							Model:    "gpt-test",
							DataPolicy: v1alpha1.ModelProviderDataPolicy{
								AllowExternalTransmission: true,
								MaximumClassification:     "Internal",
							},
						},
					},
				},
			},
		},
	}

	result, err := service.GenerateRCA(context.Background(), v1alpha1.InvestigationRequestSpec{}, PreflightResult{
		Target: domain.ResourceRef{Namespace: "prod", Name: "open-api", Kind: "Deployment"},
		Provider: &v1alpha1.ModelProvider{
			ObjectMeta: metav1.ObjectMeta{Name: "primary-broken", Namespace: "fluxagent-system", Generation: 3},
			Spec: v1alpha1.ModelProviderSpec{
				Provider: "broken",
				Model:    "broken-model",
				FallbackProviderRef: v1alpha1.LocalObjectReference{
					Name: "fallback-openai",
				},
			},
		},
	}, EvidenceCollectionResult{
		Summary: "collected 1 evidence records from 1 datasource",
		EvidenceRefs: []v1alpha1.EvidenceRef{
			{
				Kind:    "log",
				Source:  "loki",
				Summary: "request body included confidential token",
				Classification: &v1alpha1.DataClassification{
					Level: "Confidential",
				},
			},
		},
	}, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("generate RCA failed: %v", err)
	}
	if result.Issue == nil || result.Issue.Reason != "ProviderDataPolicyRejected" {
		t.Fatalf("expected fallback ProviderDataPolicyRejected issue, got %#v", result.Issue)
	}
	if hosted.calls != 0 {
		t.Fatalf("expected fallback hosted provider not to be called, got %d", hosted.calls)
	}
	if len(result.EgressAttempts) != 2 {
		t.Fatalf("expected primary and fallback attempts, got %#v", result.EgressAttempts)
	}
	fallbackAttempt := result.EgressAttempts[1]
	if fallbackAttempt.ProviderRef == nil || fallbackAttempt.ProviderRef.Name != "fallback-openai" {
		t.Fatalf("expected fallback provider ref, got %#v", fallbackAttempt.ProviderRef)
	}
	if fallbackAttempt.Decision != "Rejected" || fallbackAttempt.Result != "ProviderDataPolicyRejected" {
		t.Fatalf("expected rejected fallback classification attempt, got %#v", fallbackAttempt)
	}
	if fallbackAttempt.MaximumClassificationAllowed != "Internal" || fallbackAttempt.MaximumClassificationObserved != "Confidential" || fallbackAttempt.MaximumClassificationSent != "" {
		t.Fatalf("expected fallback classification audit, got %#v", fallbackAttempt)
	}
}

func TestServiceGenerateRCAFiltersHostedProviderEvidenceByDataPolicy(t *testing.T) {
	hosted := &capturingModelProvider{name: "openai"}
	service := &Service{
		Gateway: &modelgateway.Gateway{
			Base: knowledge.NewBase(),
			Providers: model.NewRegistry(
				hosted,
			),
		},
	}

	result, err := service.GenerateRCA(context.Background(), v1alpha1.InvestigationRequestSpec{}, PreflightResult{
		Target: domain.ResourceRef{Namespace: "prod", Name: "open-api", Kind: "Deployment"},
		Provider: &v1alpha1.ModelProvider{
			ObjectMeta: metav1.ObjectMeta{Name: "openai-provider", Namespace: "fluxagent-system"},
			Spec: v1alpha1.ModelProviderSpec{
				Provider: "openai",
				Model:    "test-model",
				DataPolicy: v1alpha1.ModelProviderDataPolicy{
					AllowExternalTransmission: true,
					AllowedEvidenceKinds:      []string{"MetricObservation"},
					AllowLogSamples:           false,
					MaximumClassification:     "Internal",
				},
			},
		},
	}, EvidenceCollectionResult{
		Summary: "collected 2 evidence records from 2 datasources",
		EvidenceRefs: []v1alpha1.EvidenceRef{
			{Kind: "metric", Source: "prometheus", Summary: "latency high"},
			{Kind: "log", Source: "loki", Summary: "secret log sample"},
		},
		Observations: []domain.Observation{
			{Type: domain.ObservationTypeMetric, Summary: "latency high"},
			{Type: domain.ObservationTypeLog, Summary: "secret log sample", Value: domain.ObservationValue{Log: &domain.LogObservation{Line: "secret log sample"}}},
		},
	}, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("generate RCA failed: %v", err)
	}
	if result.Issue != nil {
		t.Fatalf("expected no RCA issue, got %#v", result.Issue)
	}
	if hosted.calls != 1 {
		t.Fatalf("expected hosted provider call, got %d", hosted.calls)
	}
	evidenceBundle, ok := hosted.lastRequest.Context["evidence"].(domain.EvidenceBundle)
	if !ok {
		t.Fatalf("expected evidence bundle in model request, got %#v", hosted.lastRequest.Context["evidence"])
	}
	if len(evidenceBundle.Logs) != 0 || len(evidenceBundle.References) != 1 || evidenceBundle.References[0].Kind != "metric" {
		t.Fatalf("expected only metric evidence to be sent, got %#v", evidenceBundle)
	}
}

func TestServiceGenerateRCARejectsClassificationAboveProviderPolicy(t *testing.T) {
	hosted := &capturingModelProvider{name: "openai"}
	service := &Service{
		Gateway: &modelgateway.Gateway{
			Base:      knowledge.NewBase(),
			Providers: model.NewRegistry(hosted),
		},
	}

	result, err := service.GenerateRCA(context.Background(), v1alpha1.InvestigationRequestSpec{}, PreflightResult{
		Target: domain.ResourceRef{Namespace: "prod", Name: "open-api", Kind: "Deployment"},
		Provider: &v1alpha1.ModelProvider{
			ObjectMeta: metav1.ObjectMeta{Name: "openai-provider", Namespace: "fluxagent-system"},
			Spec: v1alpha1.ModelProviderSpec{
				Provider: "openai",
				Model:    "test-model",
				DataPolicy: v1alpha1.ModelProviderDataPolicy{
					AllowExternalTransmission: true,
					AllowedEvidenceKinds:      []string{"LogObservation"},
					AllowLogSamples:           false,
					MaximumClassification:     "Internal",
				},
			},
		},
	}, EvidenceCollectionResult{
		Summary: "collected 1 evidence record from 1 datasource",
		EvidenceRefs: []v1alpha1.EvidenceRef{
			{
				Kind:             "log",
				Source:           "loki",
				Summary:          "log sample omitted by provider data policy",
				RedactionProfile: "default-v1",
				Classification: &v1alpha1.DataClassification{
					Level:           v1alpha1.DataClassificationLevelConfidential,
					SensitivityTags: []string{v1alpha1.SensitivityTagInfrastructureMetadata},
					Source:          v1alpha1.DataClassificationSourceDefault,
					PolicyVersion:   v1alpha1.DataClassificationPolicyVersion,
				},
			},
		},
	}, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("generate RCA failed: %v", err)
	}
	if result.Issue == nil || result.Issue.Reason != "ProviderDataPolicyRejected" || !strings.Contains(result.Issue.Message, "Confidential") {
		t.Fatalf("expected classification policy rejection, got %#v", result.Issue)
	}
	if hosted.calls != 0 {
		t.Fatalf("expected hosted provider not to be called, got %d", hosted.calls)
	}
}

func TestServiceGenerateRCARejectsDeniedSensitivityTags(t *testing.T) {
	hosted := &capturingModelProvider{name: "openai"}
	service := &Service{
		Gateway: &modelgateway.Gateway{
			Base:      knowledge.NewBase(),
			Providers: model.NewRegistry(hosted),
		},
	}

	result, err := service.GenerateRCA(context.Background(), v1alpha1.InvestigationRequestSpec{}, PreflightResult{
		Target: domain.ResourceRef{Namespace: "prod", Name: "open-api", Kind: "Deployment"},
		Provider: &v1alpha1.ModelProvider{
			ObjectMeta: metav1.ObjectMeta{Name: "openai-provider", Namespace: "fluxagent-system"},
			Spec: v1alpha1.ModelProviderSpec{
				Provider: "openai",
				Model:    "test-model",
				DataPolicy: v1alpha1.ModelProviderDataPolicy{
					AllowExternalTransmission: true,
					MaximumClassification:     "Restricted",
					DeniedSensitivityTags:     []string{"CredentialLike"},
				},
			},
		},
	}, EvidenceCollectionResult{
		Summary: "collected 1 evidence record from 1 datasource",
		EvidenceRefs: []v1alpha1.EvidenceRef{
			{
				Kind:             "event",
				Source:           "kubernetes-events",
				Summary:          "image pull failed",
				RedactionProfile: "default-v1",
				Classification: &v1alpha1.DataClassification{
					Level:           v1alpha1.DataClassificationLevelRestricted,
					SensitivityTags: []string{v1alpha1.SensitivityTagCredentialLike},
					Source:          v1alpha1.DataClassificationSourceContentDetection,
					PolicyVersion:   v1alpha1.DataClassificationPolicyVersion,
				},
			},
		},
	}, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("generate RCA failed: %v", err)
	}
	if result.Issue == nil || result.Issue.Reason != "ProviderDataPolicyRejected" || !strings.Contains(result.Issue.Message, "CredentialLike") {
		t.Fatalf("expected sensitivity tag policy rejection, got %#v", result.Issue)
	}
	if hosted.calls != 0 {
		t.Fatalf("expected hosted provider not to be called, got %d", hosted.calls)
	}
}

type fakeDataSource struct {
	name           string
	queryType      domain.QueryType
	records        []map[string]any
	queryErr       error
	delay          time.Duration
	queryPolicy    v1alpha1.DataSourceQueryPolicy
	nativeLimit    *datasource.NativeResultLimit
	classification v1alpha1.DataClassification
}

type capturingModelProvider struct {
	name        string
	calls       int
	lastRequest domain.ModelRequest
}

func (p *capturingModelProvider) Name() string { return p.name }

func (p *capturingModelProvider) Complete(_ context.Context, req domain.ModelRequest) (domain.ModelResponse, error) {
	p.calls++
	p.lastRequest = req
	return domain.ModelResponse{
		Provider:   p.name,
		Model:      "test-model",
		Structured: true,
		Output: map[string]any{
			"riskTitle":       "RCA",
			"riskSummary":     "RCA summary",
			"severity":        "low",
			"confidenceScore": 75,
			"rationale":       "test",
			"rcaHypothesis":   "test hypothesis",
			"rcaCauses":       []string{"test cause"},
			"actionType":      "notification.sendSlack",
		},
	}, nil
}

type failingModelProvider struct {
	name   string
	reason string
}

func (p failingModelProvider) Name() string { return p.name }

func (p failingModelProvider) Complete(context.Context, domain.ModelRequest) (domain.ModelResponse, error) {
	reason := p.reason
	if reason == "" {
		reason = "ProviderUnavailable"
	}
	return domain.ModelResponse{}, &model.ProviderError{
		Reason:  reason,
		Message: "provider is unavailable",
	}
}

type serviceResolverStub struct {
	providers map[string]*v1alpha1.ModelProvider
}

func (r serviceResolverStub) Resolve(_ context.Context, namespace string, ref *v1alpha1.LocalObjectReference) (*v1alpha1.ModelProvider, error) {
	if ref == nil || ref.Name == "" {
		return modelgateway.DefaultHeuristicProvider(namespace), nil
	}
	provider, ok := r.providers[namespace+"/"+ref.Name]
	if !ok {
		return nil, &modelgateway.ResolveError{
			Reason:  "ProviderNotFound",
			Message: "provider not found",
		}
	}
	return provider, nil
}

func (f fakeDataSource) Name() string { return f.name }

func (f fakeDataSource) Type() string { return string(f.queryType) }

func (f fakeDataSource) Capabilities() datasource.Capabilities {
	switch f.queryType {
	case domain.QueryTypeMetric:
		return datasource.Capabilities{Metrics: true}
	case domain.QueryTypeLog:
		return datasource.Capabilities{Logs: true}
	case domain.QueryTypeEvent:
		return datasource.Capabilities{Events: true}
	default:
		return datasource.Capabilities{}
	}
}

func (f fakeDataSource) Query(context.Context, datasource.QueryRequest) (*datasource.QueryResult, error) {
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	result := &datasource.QueryResult{Source: f.name, QueryType: f.queryType, Records: f.records, Summary: fmt.Sprintf("%s returned %d records", f.name, len(f.records))}
	if f.nativeLimit != nil {
		result.Truncated = true
		result.TruncationReason = f.nativeLimit.Reason
		result.LimitDimension = f.nativeLimit.Dimension
		result.Limit = f.nativeLimit.Limit
		result.OriginalRecordCount = int(f.nativeLimit.OriginalCount)
		result.RetainedRecordCount = int(f.nativeLimit.RetainedCount)
		result.NativeLimit = f.nativeLimit
	}
	return result, nil
}

func (f fakeDataSource) HealthCheck(context.Context) error { return nil }

func (f fakeDataSource) QueryPolicy() v1alpha1.DataSourceQueryPolicy { return f.queryPolicy }

func (f fakeDataSource) DataClassification() v1alpha1.DataClassification { return f.classification }

type blockingDataSource struct {
	name      string
	queryType domain.QueryType
	started   chan string
	releaseCh chan struct{}
	once      sync.Once
	mu        sync.Mutex
	active    int
	max       int
}

func newBlockingDataSource(name string, queryType domain.QueryType) *blockingDataSource {
	return &blockingDataSource{
		name:      name,
		queryType: queryType,
		started:   make(chan string, 8),
		releaseCh: make(chan struct{}),
	}
}

func (b *blockingDataSource) Name() string { return b.name }

func (b *blockingDataSource) Type() string { return string(b.queryType) }

func (b *blockingDataSource) Capabilities() datasource.Capabilities {
	return datasource.Capabilities{Logs: b.queryType == domain.QueryTypeLog}
}

func (b *blockingDataSource) Query(ctx context.Context, req datasource.QueryRequest) (*datasource.QueryResult, error) {
	b.mu.Lock()
	b.active++
	if b.active > b.max {
		b.max = b.active
	}
	b.mu.Unlock()
	b.started <- req.Query
	defer func() {
		b.mu.Lock()
		b.active--
		b.mu.Unlock()
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.releaseCh:
	}
	return &datasource.QueryResult{
		Source:    b.name,
		QueryType: b.queryType,
		Records:   []map[string]any{{"line": req.Query}},
		Summary:   req.Query,
	}, nil
}

func (b *blockingDataSource) HealthCheck(context.Context) error { return nil }

func (b *blockingDataSource) waitForStarted(t *testing.T, count int) {
	t.Helper()
	for index := 0; index < count; index++ {
		select {
		case <-b.started:
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for query %d to start", index+1)
		}
	}
}

func (b *blockingDataSource) assertNoAdditionalStart(t *testing.T) {
	t.Helper()
	select {
	case query := <-b.started:
		t.Fatalf("expected scheduler to hold additional queries, but %q started", query)
	case <-time.After(25 * time.Millisecond):
	}
}

func (b *blockingDataSource) release() {
	b.once.Do(func() {
		close(b.releaseCh)
	})
}

func (b *blockingDataSource) maxActive() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.max
}

type capturingDataSource struct {
	name      string
	queryType domain.QueryType
	records   []map[string]any
	lastQuery *datasource.QueryRequest
}

func (c *capturingDataSource) Name() string { return c.name }
func (c *capturingDataSource) Type() string { return string(c.queryType) }
func (c *capturingDataSource) Capabilities() datasource.Capabilities {
	switch c.queryType {
	case domain.QueryTypeMetric:
		return datasource.Capabilities{Metrics: true}
	case domain.QueryTypeLog:
		return datasource.Capabilities{Logs: true}
	case domain.QueryTypeEvent:
		return datasource.Capabilities{Events: true}
	default:
		return datasource.Capabilities{}
	}
}
func (c *capturingDataSource) Query(_ context.Context, req datasource.QueryRequest) (*datasource.QueryResult, error) {
	copied := req
	c.lastQuery = &copied
	return &datasource.QueryResult{Source: c.name, QueryType: c.queryType, Records: c.records}, nil
}
func (c *capturingDataSource) HealthCheck(context.Context) error { return nil }

func stringSliceContains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
