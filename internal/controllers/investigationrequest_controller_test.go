package controllers

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"fluxagent/api/v1alpha1"
	"fluxagent/internal/datasource"
	"fluxagent/internal/domain"
	"fluxagent/internal/investigation"
	"fluxagent/internal/knowledge"
	"fluxagent/internal/model"
	"fluxagent/internal/model/heuristic"
	"fluxagent/internal/modelgateway"
	"fluxagent/internal/rcametrics"
	"fluxagent/internal/verifier"
)

func TestInvestigationRequestReconcilerCompletesWithRCA(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add apps scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add aiops scheme: %v", err)
	}
	now := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC)

	request := &v1alpha1.InvestigationRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "investigate-open-api",
			Namespace:  "fluxagent-system",
			Generation: 1,
			Annotations: map[string]string{
				annotationLineageSource:      "fluxagent-system/latency-regression",
				annotationLineageSourceUID:   "riskrule-uid",
				annotationLineageGeneration:  "4",
				annotationTargetUID:          "deployment-uid",
				annotationFindingFingerprint: "sha256:abc123",
				annotationInvestigationDepth: "0",
			},
		},
		Spec: v1alpha1.InvestigationRequestSpec{
			Target: v1alpha1.TargetRef{
				Namespace:  "prod",
				Kind:       "Deployment",
				Name:       "open-api",
				APIVersion: "apps/v1",
			},
			DataSources: []v1alpha1.LocalObjectReference{
				{Name: "kubernetes-events"},
			},
			ModelProviderRef: v1alpha1.LocalObjectReference{Name: "heuristic-provider"},
			Mode:             v1alpha1.InvestigationModeReadOnly,
		},
	}

	deployment := &appsv1.Deployment{
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
	}
	provider := &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "heuristic-provider", Namespace: "fluxagent-system", Generation: 3},
		Spec: v1alpha1.ModelProviderSpec{
			Provider: "heuristic",
			Model:    "built-in",
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.InvestigationRequest{}, &v1alpha1.RiskSignal{}).
		WithObjects(request, deployment, provider).
		Build()

	reconciler := &InvestigationRequestReconciler{
		Client: client,
		Scheme: scheme,
		Service: &investigation.Service{
			Client: client,
			Registry: datasource.NewRegistry(
				fakeInvestigationDataSource{name: "kubernetes-events", queryType: domain.QueryTypeEvent},
			),
			Resolver: modelgateway.KubeResolver{Client: client},
			Gateway: &modelgateway.Gateway{
				Base: knowledge.NewBase(),
				Providers: model.NewRegistry(
					heuristic.Provider{},
				),
			},
		},
		Now: func() time.Time { return now },
	}
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: request.Name, Namespace: request.Namespace},
	}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var stored v1alpha1.InvestigationRequest
	if err := client.Get(context.Background(), types.NamespacedName{Name: request.Name, Namespace: request.Namespace}, &stored); err != nil {
		t.Fatalf("get request: %v", err)
	}
	if stored.Status.Phase != v1alpha1.PhaseCompleted {
		t.Fatalf("expected completed phase, got %s", stored.Status.Phase)
	}
	if stored.Status.Outcome != v1alpha1.InvestigationOutcomeConfirmed {
		t.Fatalf("expected confirmed outcome, got %q", stored.Status.Outcome)
	}
	if stored.Status.Failure != nil {
		t.Fatalf("expected no workflow failure on completed investigation, got %#v", stored.Status.Failure)
	}
	if stored.Status.StartedAt == nil || !stored.Status.StartedAt.Equal(&metav1.Time{Time: now}) {
		t.Fatalf("expected startedAt %s, got %#v", now.Format(time.RFC3339), stored.Status.StartedAt)
	}
	if stored.Status.CompletedAt == nil || !stored.Status.CompletedAt.Equal(&metav1.Time{Time: now}) {
		t.Fatalf("expected completedAt %s, got %#v", now.Format(time.RFC3339), stored.Status.CompletedAt)
	}
	if stored.Status.Provider != "heuristic" {
		t.Fatalf("expected provider heuristic, got %q", stored.Status.Provider)
	}
	if stored.Status.Summary == "" || stored.Status.Hypothesis == "" {
		t.Fatalf("expected RCA summary and hypothesis, got summary=%q hypothesis=%q", stored.Status.Summary, stored.Status.Hypothesis)
	}
	if stored.Status.Confidence <= 0 {
		t.Fatalf("expected positive confidence, got %f", stored.Status.Confidence)
	}
	if len(stored.Status.EvidenceRefs) != 1 || stored.Status.EvidenceRefs[0].Kind != "event" {
		t.Fatalf("expected one event evidence ref, got %#v", stored.Status.EvidenceRefs)
	}
	if stored.Status.EvidenceRefs[0].ID != "evidence-001" {
		t.Fatalf("expected evidence id evidence-001, got %#v", stored.Status.EvidenceRefs[0])
	}
	if stored.Status.Verdict == nil {
		t.Fatal("expected structured RCA verdict")
	}
	if stored.Status.Verdict.Outcome != v1alpha1.InvestigationOutcomeConfirmed {
		t.Fatalf("expected confirmed verdict outcome, got %#v", stored.Status.Verdict)
	}
	if stored.Status.Verdict.RootCauseEntity.Name != "open-api" {
		t.Fatalf("expected root cause entity open-api, got %#v", stored.Status.Verdict.RootCauseEntity)
	}
	if stored.Status.Verdict.RootCauseType == "" {
		t.Fatalf("expected root cause type, got %#v", stored.Status.Verdict)
	}
	if stored.Status.Verdict.ConfidenceDetail == nil {
		t.Fatal("expected structured confidence detail")
	}
	if stored.Status.Verdict.ConfidenceDetail.ProviderScore != stored.Status.Confidence {
		t.Fatalf("expected provider confidence %f, got %#v", stored.Status.Confidence, stored.Status.Verdict.ConfidenceDetail)
	}
	if stored.Status.Verdict.ConfidenceDetail.VerifiedScore <= 0 || stored.Status.Verdict.ConfidenceDetail.VerifiedScore > stored.Status.Confidence {
		t.Fatalf("expected bounded verified confidence, got %#v", stored.Status.Verdict.ConfidenceDetail)
	}
	if stored.Status.Verdict.ConfidenceDetail.Level == "" || stored.Status.Verdict.ConfidenceDetail.Method != "HeuristicEvidenceCoverageV1" {
		t.Fatalf("expected confidence level and method, got %#v", stored.Status.Verdict.ConfidenceDetail)
	}
	if len(stored.Status.Claims) == 0 {
		t.Fatalf("expected structured claims, got %#v", stored.Status.Claims)
	}
	if stored.Status.Claims[0].Verification != verifier.VerificationUnsupported {
		t.Fatalf("expected summary claim unsupported without a direct evidence match, got %#v", stored.Status.Claims[0])
	}
	supportedClaimFound := false
	for _, claim := range stored.Status.Claims {
		if claim.Verification == verifier.VerificationSupported && len(claim.EvidenceRefs) == 1 && claim.EvidenceRefs[0] == "evidence-001" {
			if len(claim.EvidenceLinks) != 1 || claim.EvidenceLinks[0].EvidenceRef != "evidence-001" || claim.EvidenceLinks[0].Role != verifier.EvidenceRoleSupports || claim.EvidenceLinks[0].Strength != verifier.EvidenceStrengthDirect {
				t.Fatalf("expected direct supporting evidence link, got %#v", claim)
			}
			supportedClaimFound = true
		}
	}
	if !supportedClaimFound {
		t.Fatalf("expected at least one supported claim citing evidence-001, got %#v", stored.Status.Claims)
	}
	if stored.Status.Execution == nil || stored.Status.Execution.Provider != "heuristic" || stored.Status.Execution.AttemptCount != 1 {
		t.Fatalf("expected RCA execution metadata, got %#v", stored.Status.Execution)
	}
	if stored.Status.Execution.VerifierVersion != verifier.MethodHeuristicEvidenceCoverageV1 {
		t.Fatalf("expected verifier version on execution, got %#v", stored.Status.Execution)
	}
	if stored.Status.Execution.ID == "" || !hasPrefix(stored.Status.Execution.ID, "sha256:") {
		t.Fatalf("expected stable execution id, got %#v", stored.Status.Execution)
	}
	if stored.Status.Execution.State != "Finalized" {
		t.Fatalf("expected finalized execution state, got %#v", stored.Status.Execution)
	}
	if stored.Status.Execution.ProviderRef == nil ||
		stored.Status.Execution.ProviderRef.Name != "heuristic-provider" ||
		stored.Status.Execution.ProviderRef.Namespace != "fluxagent-system" {
		t.Fatalf("expected provider ref metadata, got %#v", stored.Status.Execution)
	}
	if stored.Status.Execution.ProviderGeneration != 3 ||
		stored.Status.Execution.ProviderType != "heuristic" ||
		stored.Status.Execution.Model != "built-in" {
		t.Fatalf("expected provider identity metadata, got %#v", stored.Status.Execution)
	}
	if stored.Status.Execution.RCASchemaVersion != "fluxagent-rca-result-v1" ||
		stored.Status.Execution.CanonicalizationVersion != "fluxagent-rca-json-v1" ||
		stored.Status.Execution.ReasoningPolicyVersion != "rca-v2-compat" ||
		stored.Status.Execution.ControllerVersion == "" {
		t.Fatalf("expected execution policy and controller versions, got %#v", stored.Status.Execution)
	}
	if len(stored.Status.Execution.Attempts) != 1 ||
		stored.Status.Execution.Attempts[0].ID != "attempt-001" ||
		stored.Status.Execution.Attempts[0].Result != "Completed" ||
		!hasPrefix(stored.Status.Execution.Attempts[0].IdempotencyKey, "sha256:") ||
		stored.Status.Execution.Attempts[0].StartedAt == nil ||
		stored.Status.Execution.Attempts[0].CompletedAt == nil {
		t.Fatalf("expected structured execution attempt metadata, got %#v", stored.Status.Execution.Attempts)
	}
	if stored.Status.Execution.ProviderResult == nil ||
		stored.Status.Execution.ProviderResult.SchemaVersion != "fluxagent-rca-result-v1" ||
		stored.Status.Execution.ProviderResult.Digest == nil ||
		stored.Status.Execution.ProviderResult.NormalizedResult == nil ||
		stored.Status.Execution.ProviderResult.NormalizedResult.RiskSummary == "" {
		t.Fatalf("expected normalized provider result checkpoint, got %#v", stored.Status.Execution.ProviderResult)
	}
	if stored.Status.Lineage == nil ||
		stored.Status.Lineage.Source.Kind != "RiskRule" ||
		stored.Status.Lineage.Source.Namespace != "fluxagent-system" ||
		stored.Status.Lineage.Source.Name != "latency-regression" ||
		stored.Status.Lineage.Source.UID != "riskrule-uid" ||
		stored.Status.Lineage.Source.Generation != 4 ||
		stored.Status.Lineage.TargetUID != "deployment-uid" ||
		stored.Status.Lineage.FindingFingerprint != "sha256:abc123" {
		t.Fatalf("expected investigation lineage, got %#v", stored.Status.Lineage)
	}
	if cond := findCondition(stored.Status.Conditions, conditionTargetResolved); cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("expected TargetResolved true condition, got %#v", cond)
	}
	if cond := findCondition(stored.Status.Conditions, conditionDatasourceResolved); cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("expected DatasourceResolved true condition, got %#v", cond)
	}
	if cond := findCondition(stored.Status.Conditions, conditionQueryTypeSupported); cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("expected QueryTypeSupported true condition, got %#v", cond)
	}
	if cond := findCondition(stored.Status.Conditions, conditionReady); cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != "InvestigationCompleted" {
		t.Fatalf("expected Ready true InvestigationCompleted, got %#v", cond)
	}
	if cond := findCondition(stored.Status.Conditions, conditionEvidenceReady); cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != "EvidenceCollected" {
		t.Fatalf("expected EvidenceCollectionReady true EvidenceCollected, got %#v", cond)
	}
	if cond := findCondition(stored.Status.Conditions, conditionRCAReady); cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != "ProviderSucceeded" {
		t.Fatalf("expected RCAReady true ProviderSucceeded, got %#v", cond)
	}
}

func TestInvestigationRequestReconcilerPromotesToRiskSignalWhenRequested(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add apps scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add aiops scheme: %v", err)
	}
	now := time.Date(2026, 7, 6, 10, 15, 0, 0, time.UTC)

	request := &v1alpha1.InvestigationRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "promote-open-api", Namespace: "fluxagent-system", Generation: 1},
		Spec: v1alpha1.InvestigationRequestSpec{
			Target: v1alpha1.TargetRef{
				Namespace: "prod",
				Kind:      "Deployment",
				Name:      "open-api",
			},
			Queries: []v1alpha1.InvestigationQuery{
				{
					Name: "unhealthy-events",
					DatasourceRef: v1alpha1.LocalObjectReference{
						Name: "kubernetes-events",
					},
					QueryType: "event",
					Reasons:   []string{"BackOff"},
				},
			},
			ModelProviderRef: v1alpha1.LocalObjectReference{Name: "heuristic-provider"},
			Mode:             v1alpha1.InvestigationModeReadOnly,
			CreateRiskSignal: true,
		},
	}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "open-api", Namespace: "prod", Labels: map[string]string{"app": "open-api"}},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "open-api"}},
			},
		},
	}
	provider := &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "heuristic-provider", Namespace: "fluxagent-system"},
		Spec:       v1alpha1.ModelProviderSpec{Provider: "heuristic", Model: "built-in"},
	}
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.InvestigationRequest{}, &v1alpha1.RiskSignal{}).
		WithObjects(request, deployment, provider).
		Build()

	reconciler := &InvestigationRequestReconciler{
		Client: client,
		Scheme: scheme,
		Service: &investigation.Service{
			Client: client,
			Registry: datasource.NewRegistry(
				fakeInvestigationDataSource{name: "kubernetes-events", queryType: domain.QueryTypeEvent},
			),
			Resolver: modelgateway.KubeResolver{Client: client},
			Gateway: &modelgateway.Gateway{
				Base: knowledge.NewBase(),
				Providers: model.NewRegistry(
					heuristic.Provider{},
				),
			},
		},
		Now: func() time.Time { return now },
	}
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: request.Name, Namespace: request.Namespace},
	}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var storedRequest v1alpha1.InvestigationRequest
	if err := client.Get(context.Background(), types.NamespacedName{Name: request.Name, Namespace: request.Namespace}, &storedRequest); err != nil {
		t.Fatalf("get request: %v", err)
	}
	if storedRequest.Status.LinkedRiskSignalRef == nil {
		t.Fatal("expected linked risk signal ref")
	}

	var riskSignal v1alpha1.RiskSignal
	if err := client.Get(context.Background(), types.NamespacedName{
		Name:      storedRequest.Status.LinkedRiskSignalRef.Name,
		Namespace: storedRequest.Status.LinkedRiskSignalRef.Namespace,
	}, &riskSignal); err != nil {
		t.Fatalf("get promoted risk signal: %v", err)
	}
	if riskSignal.Spec.Target.Name != "open-api" {
		t.Fatalf("unexpected risk signal target %#v", riskSignal.Spec.Target)
	}
	if riskSignal.Status.RCASummary == "" || riskSignal.Status.RCAHypothesis == "" {
		t.Fatalf("expected RCA fields on promoted risk signal, got %#v", riskSignal.Status)
	}
	if cond := findCondition(riskSignal.Status.Conditions, conditionRCAReady); cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("expected RCAReady true on promoted risk signal, got %#v", cond)
	}
}

func TestLineageForReconcilePrefersStatusLineageWhenAnnotationsMissing(t *testing.T) {
	existing := &v1alpha1.InvestigationLineage{
		Source: v1alpha1.InvestigationLineageSource{
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
			Kind:       "RiskRule",
			Namespace:  "fluxagent-system",
			Name:       "latency-regression",
			UID:        "riskrule-uid",
			Generation: 4,
		},
		TargetUID:          "deployment-uid",
		FindingFingerprint: "sha256:abc123",
		InvestigationDepth: 0,
	}

	got := lineageForReconcile(existing, nil)
	if got == nil || got.Source.Kind != "RiskRule" || got.Source.UID != "riskrule-uid" || got.TargetUID != "deployment-uid" {
		t.Fatalf("expected existing status lineage to be preserved, got %#v", got)
	}
	got.Source.UID = "mutated"
	if existing.Source.UID != "riskrule-uid" {
		t.Fatalf("expected lineage copy to avoid mutating existing status, got %#v", existing)
	}
}

func TestValidateInvestigationQueryBudgetRejectsExcessiveLookback(t *testing.T) {
	message := validateInvestigationQueryBudget(v1alpha1.InvestigationRequestSpec{
		TimeRange: v1alpha1.InvestigationTimeRange{Lookback: metav1.Duration{Duration: 2 * time.Hour}},
		QueryBudget: v1alpha1.InvestigationQueryBudget{
			MaxTimeRange: metav1.Duration{Duration: 30 * time.Minute},
		},
		Queries: []v1alpha1.InvestigationQuery{
			{
				DatasourceRef: v1alpha1.LocalObjectReference{Name: "prometheus"},
				QueryType:     string(domain.QueryTypeMetric),
			},
		},
	})
	if !strings.Contains(message, "exceeds queryBudget.maxTimeRange") {
		t.Fatalf("expected maxTimeRange rejection, got %q", message)
	}
}

func TestValidateInvestigationQueryBudgetRejectsQueryCounts(t *testing.T) {
	message := validateInvestigationQueryBudget(v1alpha1.InvestigationRequestSpec{
		QueryBudget: v1alpha1.InvestigationQueryBudget{
			MaxQueriesTotal: 1,
		},
		Queries: []v1alpha1.InvestigationQuery{
			{DatasourceRef: v1alpha1.LocalObjectReference{Name: "prometheus"}, QueryType: string(domain.QueryTypeMetric)},
			{DatasourceRef: v1alpha1.LocalObjectReference{Name: "loki"}, QueryType: string(domain.QueryTypeLog)},
		},
	})
	if !strings.Contains(message, "exceeds queryBudget.maxQueriesTotal") {
		t.Fatalf("expected maxQueriesTotal rejection, got %q", message)
	}

	message = validateInvestigationQueryBudget(v1alpha1.InvestigationRequestSpec{
		QueryBudget: v1alpha1.InvestigationQueryBudget{
			MaxQueriesPerSource: 1,
		},
		Queries: []v1alpha1.InvestigationQuery{
			{DatasourceRef: v1alpha1.LocalObjectReference{Name: "prometheus"}, QueryType: string(domain.QueryTypeMetric)},
			{DatasourceRef: v1alpha1.LocalObjectReference{Name: "prometheus"}, QueryType: string(domain.QueryTypeMetric)},
		},
	})
	if !strings.Contains(message, "exceeds queryBudget.maxQueriesPerSource") {
		t.Fatalf("expected maxQueriesPerSource rejection, got %q", message)
	}
}

func TestValidateInvestigationQueryBudgetRejectsNegativeValues(t *testing.T) {
	tests := []struct {
		name   string
		budget v1alpha1.InvestigationQueryBudget
		want   string
	}{
		{
			name:   "maxTimeRange",
			budget: v1alpha1.InvestigationQueryBudget{MaxTimeRange: metav1.Duration{Duration: -time.Second}},
			want:   "queryBudget.maxTimeRange must not be negative",
		},
		{
			name:   "maxQueriesTotal",
			budget: v1alpha1.InvestigationQueryBudget{MaxQueriesTotal: -1},
			want:   "queryBudget.maxQueriesTotal must not be negative",
		},
		{
			name:   "maxQueriesPerSource",
			budget: v1alpha1.InvestigationQueryBudget{MaxQueriesPerSource: -1},
			want:   "queryBudget.maxQueriesPerSource must not be negative",
		},
		{
			name:   "maxConcurrentQueries",
			budget: v1alpha1.InvestigationQueryBudget{MaxConcurrentQueries: -1},
			want:   "queryBudget.maxConcurrentQueries must not be negative",
		},
		{
			name:   "maxCumulativeDuration",
			budget: v1alpha1.InvestigationQueryBudget{MaxCumulativeDuration: metav1.Duration{Duration: -time.Second}},
			want:   "queryBudget.maxCumulativeDuration must not be negative",
		},
		{
			name:   "maxCumulativeResponseBytes",
			budget: v1alpha1.InvestigationQueryBudget{MaxCumulativeResponseBytes: -1},
			want:   "queryBudget.maxCumulativeResponseBytes must not be negative",
		},
		{
			name: "metricsMaxSeries",
			budget: v1alpha1.InvestigationQueryBudget{ResultLimits: v1alpha1.QueryResultLimits{
				Metrics: v1alpha1.MetricResultLimits{MaxSeries: -1},
			}},
			want: "queryBudget.resultLimits.metrics.maxSeries must not be negative",
		},
		{
			name: "metricsMaxSamples",
			budget: v1alpha1.InvestigationQueryBudget{ResultLimits: v1alpha1.QueryResultLimits{
				Metrics: v1alpha1.MetricResultLimits{MaxSamples: -1},
			}},
			want: "queryBudget.resultLimits.metrics.maxSamples must not be negative",
		},
		{
			name: "logsMaxLines",
			budget: v1alpha1.InvestigationQueryBudget{ResultLimits: v1alpha1.QueryResultLimits{
				Logs: v1alpha1.LogResultLimits{MaxLines: -1},
			}},
			want: "queryBudget.resultLimits.logs.maxLines must not be negative",
		},
		{
			name: "logsMaxStreams",
			budget: v1alpha1.InvestigationQueryBudget{ResultLimits: v1alpha1.QueryResultLimits{
				Logs: v1alpha1.LogResultLimits{MaxStreams: -1},
			}},
			want: "queryBudget.resultLimits.logs.maxStreams must not be negative",
		},
		{
			name: "logsMaxEntries",
			budget: v1alpha1.InvestigationQueryBudget{ResultLimits: v1alpha1.QueryResultLimits{
				Logs: v1alpha1.LogResultLimits{MaxEntries: -1},
			}},
			want: "queryBudget.resultLimits.logs.maxEntries must not be negative",
		},
		{
			name: "eventsMaxRecords",
			budget: v1alpha1.InvestigationQueryBudget{ResultLimits: v1alpha1.QueryResultLimits{
				Events: v1alpha1.EventResultLimits{MaxRecords: -1},
			}},
			want: "queryBudget.resultLimits.events.maxRecords must not be negative",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message := validateInvestigationQueryBudget(v1alpha1.InvestigationRequestSpec{QueryBudget: tt.budget})
			if message != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, message)
			}
		})
	}
}

func TestValidateEvidenceRetentionRejectsUnsupportedSnapshots(t *testing.T) {
	if message := validateEvidenceRetention(v1alpha1.EvidenceRetentionPolicy{}); message != "" {
		t.Fatalf("expected empty retention policy to be accepted, got %q", message)
	}
	if message := validateEvidenceRetention(v1alpha1.EvidenceRetentionPolicy{Mode: v1alpha1.EvidenceRetentionModeMetadataOnly}); message != "" {
		t.Fatalf("expected MetadataOnly retention policy to be accepted, got %q", message)
	}

	message := validateEvidenceRetention(v1alpha1.EvidenceRetentionPolicy{Mode: v1alpha1.EvidenceRetentionModeRawSnapshot})
	if !strings.Contains(message, "RawSnapshot") || !strings.Contains(message, "not supported") {
		t.Fatalf("expected RawSnapshot rejection, got %q", message)
	}

	message = validateEvidenceRetention(v1alpha1.EvidenceRetentionPolicy{Mode: v1alpha1.EvidenceRetentionModeNormalizedSnapshot})
	if !strings.Contains(message, "NormalizedSnapshot") || !strings.Contains(message, "not supported") {
		t.Fatalf("expected NormalizedSnapshot rejection, got %q", message)
	}
}

func TestProviderEgressAuditUsesFilteredMetadataOnly(t *testing.T) {
	provider := &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "openai-provider", Namespace: "fluxagent-system"},
		Spec: v1alpha1.ModelProviderSpec{
			Provider: "openai",
			DataPolicy: v1alpha1.ModelProviderDataPolicy{
				AllowExternalTransmission: true,
				AllowedEvidenceKinds:      []string{"MetricObservation", "LogObservation"},
				AllowLogSamples:           false,
				MaximumClassification:     "Internal",
			},
		},
	}
	audit := providerEgressAudit(provider, investigation.EvidenceCollectionResult{
		EvidenceRefs: []v1alpha1.EvidenceRef{
			{Kind: "metric", Source: "prometheus", Summary: "latency high"},
			{Kind: "log", Source: "loki", Summary: "secret log sample"},
			{Kind: "event", Source: "kubernetes-events", Summary: "BackOff"},
		},
	})
	if audit == nil {
		t.Fatal("expected hosted provider egress audit")
	}
	if audit.ProviderType != "openai" || audit.EvidenceBundleDigest == "" || !hasPrefix(audit.EvidenceBundleDigest, "sha256:") {
		t.Fatalf("expected provider type and digest, got %#v", audit)
	}
	if audit.LogSamplesIncluded {
		t.Fatalf("expected log samples to be excluded, got %#v", audit)
	}
	if len(audit.EvidenceKinds) != 2 || audit.EvidenceKinds[0] != "log" || audit.EvidenceKinds[1] != "metric" {
		t.Fatalf("expected filtered evidence kinds, got %#v", audit.EvidenceKinds)
	}
	if audit.MaximumClassificationSent != "Internal" {
		t.Fatalf("expected Internal max classification, got %#v", audit)
	}
}

func TestInvestigationRequestReconcilerBlocksRiskSignalSourceByDefault(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add apps scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add aiops scheme: %v", err)
	}
	request := loopPolicyTestRequest("risk-signal-loop", map[string]string{
		annotationLineageSource:      "fluxagent-system/discovered-open-api",
		annotationLineageSourceKind:  "RiskSignal",
		annotationLineageSourceAPI:   v1alpha1.SchemeGroupVersion.String(),
		annotationLineageSourceUID:   "risk-signal-uid",
		annotationTargetUID:          "deployment-uid",
		annotationInvestigationDepth: "0",
	})
	provider := &countingModelProvider{}
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.InvestigationRequest{}, &v1alpha1.RiskSignal{}).
		WithObjects(request).
		Build()
	reconciler := loopPolicyTestReconciler(client, scheme, provider)

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: request.Name, Namespace: request.Namespace},
	}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var stored v1alpha1.InvestigationRequest
	if err := client.Get(context.Background(), types.NamespacedName{Name: request.Name, Namespace: request.Namespace}, &stored); err != nil {
		t.Fatalf("get request: %v", err)
	}
	if stored.Status.Phase != v1alpha1.PhaseFailed || stored.Status.Outcome != v1alpha1.InvestigationOutcomeUnknown {
		t.Fatalf("expected failed unknown loop-prevented status, got phase=%s outcome=%s", stored.Status.Phase, stored.Status.Outcome)
	}
	if stored.Status.Failure == nil || stored.Status.Failure.Code != "RiskSignalSourceBlocked" {
		t.Fatalf("expected RiskSignalSourceBlocked failure, got %#v", stored.Status.Failure)
	}
	if provider.calls != 0 {
		t.Fatalf("expected provider not to be called when loop policy blocks request, got %d", provider.calls)
	}
}

func TestInvestigationRequestReconcilerEnforcesInvestigationDepthLimit(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add apps scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add aiops scheme: %v", err)
	}
	request := loopPolicyTestRequest("depth-limit", map[string]string{
		annotationLineageSource:      "fluxagent-system/latency-regression",
		annotationLineageSourceKind:  "RiskRule",
		annotationLineageSourceAPI:   v1alpha1.SchemeGroupVersion.String(),
		annotationLineageSourceUID:   "riskrule-uid",
		annotationTargetUID:          "deployment-uid",
		annotationInvestigationDepth: "1",
	})
	provider := &countingModelProvider{}
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.InvestigationRequest{}, &v1alpha1.RiskSignal{}).
		WithObjects(request).
		Build()
	reconciler := loopPolicyTestReconciler(client, scheme, provider)

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: request.Name, Namespace: request.Namespace},
	}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var stored v1alpha1.InvestigationRequest
	if err := client.Get(context.Background(), types.NamespacedName{Name: request.Name, Namespace: request.Namespace}, &stored); err != nil {
		t.Fatalf("get request: %v", err)
	}
	if stored.Status.Failure == nil || stored.Status.Failure.Code != "InvestigationDepthLimitExceeded" {
		t.Fatalf("expected InvestigationDepthLimitExceeded failure, got %#v", stored.Status.Failure)
	}
	if provider.calls != 0 {
		t.Fatalf("expected provider not to be called when depth policy blocks request, got %d", provider.calls)
	}
}

func TestInvestigationRequestReconcilerMarksInconclusiveWhenRequiredEvidenceMissing(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add apps scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add aiops scheme: %v", err)
	}
	request := evidenceRequirementTestRequest("latency-missing-metric", "LatencyRegression", "kubernetes-events")
	provider := &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "counting-provider", Namespace: "fluxagent-system"},
		Spec:       v1alpha1.ModelProviderSpec{Provider: "counting", Model: "test-model"},
	}
	counter := &countingModelProvider{}
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.InvestigationRequest{}, &v1alpha1.RiskSignal{}).
		WithObjects(request, evidenceRequirementDeployment(), provider).
		Build()
	reconciler := evidenceRequirementTestReconciler(client, scheme, counter, fakeInvestigationDataSource{name: "kubernetes-events", queryType: domain.QueryTypeEvent})

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: request.Name, Namespace: request.Namespace},
	}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var stored v1alpha1.InvestigationRequest
	if err := client.Get(context.Background(), types.NamespacedName{Name: request.Name, Namespace: request.Namespace}, &stored); err != nil {
		t.Fatalf("get request: %v", err)
	}
	if stored.Status.Phase != v1alpha1.PhaseCompleted || stored.Status.Outcome != v1alpha1.InvestigationOutcomeInconclusive {
		t.Fatalf("expected completed inconclusive status, got phase=%s outcome=%s", stored.Status.Phase, stored.Status.Outcome)
	}
	if stored.Status.Failure != nil {
		t.Fatalf("expected no workflow failure for evidence-gated inconclusive status, got %#v", stored.Status.Failure)
	}
	if len(stored.Status.MissingEvidence) != 1 || stored.Status.MissingEvidence[0].Source != string(domain.QueryTypeMetric) {
		t.Fatalf("expected missing metric evidence, got %#v", stored.Status.MissingEvidence)
	}
	if cond := findCondition(stored.Status.Conditions, conditionEvidenceReady); cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "RequiredEvidenceMissing" {
		t.Fatalf("expected EvidenceCollectionReady false RequiredEvidenceMissing, got %#v", cond)
	}
	if counter.calls != 0 {
		t.Fatalf("expected provider not to be called when required evidence is missing, got %d", counter.calls)
	}
}

func TestEvidenceNativeLimitDegradation(t *testing.T) {
	degradation := evidenceNativeLimitDegradation(investigation.EvidenceCollectionResult{
		EvidenceRefs: []v1alpha1.EvidenceRef{
			{
				ID:               "evidence-001",
				Truncated:        true,
				TruncationReason: "NativeResultLimitExceeded",
				LimitDimension:   "samples",
				Limit:            20000,
				OriginalCount:    75321,
				RetainedCount:    20000,
			},
		},
	})
	if degradation == nil || !degradation.Partial || len(degradation.Reasons) != 1 {
		t.Fatalf("expected partial native limit degradation, got %#v", degradation)
	}
	reason := degradation.Reasons[0]
	if reason.Code != "NativeResultLimitExceeded" || reason.Stage != v1alpha1.InvestigationStageEvidenceCollection || !strings.Contains(reason.Message, "samples") {
		t.Fatalf("expected native limit degradation reason, got %#v", reason)
	}
}

func TestInvestigationRequestReconcilerAllowsImagePullBackOffWhenEventEvidencePresent(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add apps scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add aiops scheme: %v", err)
	}
	request := evidenceRequirementTestRequest("imagepull-event-present", "ImagePullBackOff", "kubernetes-events")
	provider := &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "counting-provider", Namespace: "fluxagent-system"},
		Spec:       v1alpha1.ModelProviderSpec{Provider: "counting", Model: "test-model"},
	}
	counter := &countingModelProvider{}
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.InvestigationRequest{}, &v1alpha1.RiskSignal{}).
		WithObjects(request, evidenceRequirementDeployment(), provider).
		Build()
	reconciler := evidenceRequirementTestReconciler(client, scheme, counter, fakeInvestigationDataSource{name: "kubernetes-events", queryType: domain.QueryTypeEvent})

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: request.Name, Namespace: request.Namespace},
	}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var stored v1alpha1.InvestigationRequest
	if err := client.Get(context.Background(), types.NamespacedName{Name: request.Name, Namespace: request.Namespace}, &stored); err != nil {
		t.Fatalf("get request: %v", err)
	}
	if stored.Status.Phase != v1alpha1.PhaseCompleted || stored.Status.Outcome != v1alpha1.InvestigationOutcomeConfirmed {
		t.Fatalf("expected completed confirmed status, got phase=%s outcome=%s", stored.Status.Phase, stored.Status.Outcome)
	}
	if counter.calls != 1 {
		t.Fatalf("expected provider to be called once when required evidence is complete, got %d", counter.calls)
	}
}

func TestInvestigationRequestReconcilerReusesProviderCompletedCheckpoint(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add apps scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add aiops scheme: %v", err)
	}
	now := time.Date(2026, 7, 6, 10, 30, 0, 0, time.UTC)

	request := &v1alpha1.InvestigationRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "checkpointed-open-api", Namespace: "fluxagent-system", Generation: 1},
		Spec: v1alpha1.InvestigationRequestSpec{
			Target: v1alpha1.TargetRef{
				Namespace: "prod",
				Kind:      "Deployment",
				Name:      "open-api",
			},
			DataSources: []v1alpha1.LocalObjectReference{{Name: "kubernetes-events"}},
			ModelProviderRef: v1alpha1.LocalObjectReference{
				Name: "counting-provider",
			},
			Mode: v1alpha1.InvestigationModeReadOnly,
		},
	}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "open-api", Namespace: "prod", Labels: map[string]string{"app": "open-api"}},
		Spec:       appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "open-api"}}}},
	}
	provider := &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "counting-provider", Namespace: "fluxagent-system", Generation: 7},
		Spec:       v1alpha1.ModelProviderSpec{Provider: "counting", Model: "test-model"},
	}
	counter := &countingModelProvider{}
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.InvestigationRequest{}).
		WithObjects(request, deployment, provider).
		Build()
	service := &investigation.Service{
		Client:   client,
		Registry: datasource.NewRegistry(fakeInvestigationDataSource{name: "kubernetes-events", queryType: domain.QueryTypeEvent}),
		Resolver: modelgateway.KubeResolver{Client: client},
		Gateway: &modelgateway.Gateway{
			Base: knowledge.NewBase(),
			Providers: model.NewRegistry(
				counter,
			),
		},
	}
	preflight, err := service.Preflight(context.Background(), request.Namespace, request.Spec)
	if err != nil {
		t.Fatalf("preflight failed: %v", err)
	}
	evidence, err := service.CollectEvidence(context.Background(), request.Spec, preflight, now)
	if err != nil {
		t.Fatalf("collect evidence failed: %v", err)
	}
	startedAt := metav1.NewTime(now)
	request.Status.StartedAt = &startedAt
	request.Status.Lineage = lineageFromAnnotations(request.Annotations)
	executionID := investigationExecutionID(request, preflight, evidence)
	reasoning := checkpointReasoningOutput()
	request.Status.Execution = buildRCAExecution(request, preflight, evidence, investigation.RCAResult{Reasoning: &reasoning}, executionID, executionStateProviderCompleted, now)
	request.Status.ResourceStatus = v1alpha1.ResourceStatus{
		Phase:              v1alpha1.PhaseReasoning,
		Message:            "provider result persisted for RCA verification",
		ObservedGeneration: request.Generation,
		UpdatedAt:          metav1.NewTime(now),
	}
	if err := client.Status().Update(context.Background(), request); err != nil {
		t.Fatalf("seed checkpoint status: %v", err)
	}

	reconciler := &InvestigationRequestReconciler{
		Client:  client,
		Scheme:  scheme,
		Service: service,
		Now:     func() time.Time { return now },
	}
	dedupHitsBefore := testutil.ToFloat64(rcametrics.DeduplicationHitsTotal.WithLabelValues("provider_checkpoint"))
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: request.Name, Namespace: request.Namespace},
	}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	dedupHitsAfter := testutil.ToFloat64(rcametrics.DeduplicationHitsTotal.WithLabelValues("provider_checkpoint"))
	if dedupHitsAfter != dedupHitsBefore+1 {
		t.Fatalf("expected provider checkpoint deduplication metric increment, before=%f after=%f", dedupHitsBefore, dedupHitsAfter)
	}
	if counter.calls != 0 {
		t.Fatalf("expected checkpoint reuse without provider call, got %d calls", counter.calls)
	}

	var stored v1alpha1.InvestigationRequest
	if err := client.Get(context.Background(), types.NamespacedName{Name: request.Name, Namespace: request.Namespace}, &stored); err != nil {
		t.Fatalf("get request: %v", err)
	}
	if stored.Status.Phase != v1alpha1.PhaseCompleted || stored.Status.Execution == nil || stored.Status.Execution.State != executionStateFinalized {
		t.Fatalf("expected finalized status from checkpoint, got %#v", stored.Status)
	}
	if stored.Status.Execution.ProviderResult == nil || stored.Status.Execution.ProviderResult.Digest == nil {
		t.Fatalf("expected provider result checkpoint to remain persisted, got %#v", stored.Status.Execution)
	}
	if stored.Status.Execution.ProviderResult.ProviderRequestID != "provider-request-checkpoint-123" {
		t.Fatalf("expected provider request id to remain in checkpoint, got %#v", stored.Status.Execution.ProviderResult)
	}
	if len(stored.Status.Execution.Attempts) != 1 || stored.Status.Execution.Attempts[0].ProviderRequestID != "provider-request-checkpoint-123" {
		t.Fatalf("expected provider request id on finalized attempt, got %#v", stored.Status.Execution.Attempts)
	}
}

func TestInvestigationRequestReconcilerSkipsCompletedGenerationAndSchedulesTTL(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add aiops scheme: %v", err)
	}
	completedAt := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC)
	now := completedAt.Add(2 * time.Minute)

	request := &v1alpha1.InvestigationRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "ttl-active", Namespace: "fluxagent-system", Generation: 1},
		Spec: v1alpha1.InvestigationRequestSpec{
			Target:     v1alpha1.TargetRef{Namespace: "prod", Kind: "Deployment", Name: "open-api"},
			TTLSeconds: 300,
		},
		Status: v1alpha1.InvestigationRequestStatus{
			ResourceStatus: v1alpha1.ResourceStatus{
				Phase:              v1alpha1.PhaseCompleted,
				Message:            "completed",
				ObservedGeneration: 1,
				UpdatedAt:          metav1.NewTime(completedAt),
			},
			StartedAt:   &metav1.Time{Time: completedAt.Add(-1 * time.Minute)},
			CompletedAt: &metav1.Time{Time: completedAt},
			Summary:     "existing summary",
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.InvestigationRequest{}).
		WithObjects(request).
		Build()

	reconciler := &InvestigationRequestReconciler{
		Client: client,
		Scheme: scheme,
		Now:    func() time.Time { return now },
	}
	result, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: request.Name, Namespace: request.Namespace},
	})
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if result.RequeueAfter != 3*time.Minute {
		t.Fatalf("expected ttl requeue after 3m, got %s", result.RequeueAfter)
	}

	var stored v1alpha1.InvestigationRequest
	if err := client.Get(context.Background(), types.NamespacedName{Name: request.Name, Namespace: request.Namespace}, &stored); err != nil {
		t.Fatalf("get request: %v", err)
	}
	if stored.Status.Phase != v1alpha1.PhaseCompleted {
		t.Fatalf("expected completed phase to remain unchanged, got %s", stored.Status.Phase)
	}
	if stored.Status.CompletedAt == nil || !stored.Status.CompletedAt.Equal(&metav1.Time{Time: completedAt}) {
		t.Fatalf("expected completedAt %s, got %#v", completedAt.Format(time.RFC3339), stored.Status.CompletedAt)
	}
	if stored.Status.Summary != "existing summary" {
		t.Fatalf("expected summary to be preserved, got %q", stored.Status.Summary)
	}
}

func TestInvestigationRequestReconcilerDeletesExpiredCompletedRequest(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add aiops scheme: %v", err)
	}
	completedAt := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC)
	now := completedAt.Add(10 * time.Minute)

	request := &v1alpha1.InvestigationRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "ttl-expired", Namespace: "fluxagent-system", Generation: 1},
		Spec: v1alpha1.InvestigationRequestSpec{
			Target:     v1alpha1.TargetRef{Namespace: "prod", Kind: "Deployment", Name: "open-api"},
			TTLSeconds: 300,
		},
		Status: v1alpha1.InvestigationRequestStatus{
			ResourceStatus: v1alpha1.ResourceStatus{
				Phase:              v1alpha1.PhaseFailed,
				Message:            "provider unavailable",
				ObservedGeneration: 1,
				UpdatedAt:          metav1.NewTime(completedAt),
			},
			CompletedAt: &metav1.Time{Time: completedAt},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.InvestigationRequest{}).
		WithObjects(request).
		Build()

	reconciler := &InvestigationRequestReconciler{
		Client: client,
		Scheme: scheme,
		Now:    func() time.Time { return now },
	}
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: request.Name, Namespace: request.Namespace},
	}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var stored v1alpha1.InvestigationRequest
	err := client.Get(context.Background(), types.NamespacedName{Name: request.Name, Namespace: request.Namespace}, &stored)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected request to be deleted after ttl, got err=%v", err)
	}
}

func TestInvestigationRequestReconcilerRejectsInvalidTarget(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add aiops scheme: %v", err)
	}
	now := time.Date(2026, 7, 6, 10, 5, 0, 0, time.UTC)

	request := &v1alpha1.InvestigationRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "invalid-request", Namespace: "fluxagent-system", Generation: 1},
		Spec: v1alpha1.InvestigationRequestSpec{
			Target: v1alpha1.TargetRef{
				Kind: "Deployment",
			},
			DataSources: []v1alpha1.LocalObjectReference{
				{Name: "kubernetes-events"},
			},
			Mode: v1alpha1.InvestigationModeReadOnly,
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.InvestigationRequest{}).
		WithObjects(request).
		Build()

	reconciler := &InvestigationRequestReconciler{
		Client: client,
		Scheme: scheme,
		Service: &investigation.Service{
			Client:   client,
			Registry: datasource.NewRegistry(fakeInvestigationDataSource{name: "kubernetes-events", queryType: domain.QueryTypeEvent}),
			Gateway: &modelgateway.Gateway{
				Base: knowledge.NewBase(),
				Providers: model.NewRegistry(
					heuristic.Provider{},
				),
			},
		},
		Now: func() time.Time { return now },
	}
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: request.Name, Namespace: request.Namespace},
	}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var stored v1alpha1.InvestigationRequest
	if err := client.Get(context.Background(), types.NamespacedName{Name: request.Name, Namespace: request.Namespace}, &stored); err != nil {
		t.Fatalf("get request: %v", err)
	}
	if stored.Status.Phase != v1alpha1.PhaseFailed {
		t.Fatalf("expected failed phase, got %s", stored.Status.Phase)
	}
	if stored.Status.Outcome != v1alpha1.InvestigationOutcomeUnknown {
		t.Fatalf("expected unknown outcome for failed workflow, got %q", stored.Status.Outcome)
	}
	if stored.Status.Failure == nil ||
		stored.Status.Failure.Code != "TargetInvalid" ||
		stored.Status.Failure.Stage != v1alpha1.InvestigationStageValidation ||
		stored.Status.Failure.Retryable {
		t.Fatalf("expected non-retryable validation failure, got %#v", stored.Status.Failure)
	}
	if cond := findCondition(stored.Status.Conditions, conditionReady); cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "TargetInvalid" {
		t.Fatalf("expected Ready false TargetInvalid, got %#v", cond)
	}
	if cond := findCondition(stored.Status.Conditions, conditionTargetResolved); cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "TargetInvalid" {
		t.Fatalf("expected TargetResolved false TargetInvalid, got %#v", cond)
	}
}

func TestInvestigationRequestReconcilerMarksDatasourceResolutionFailure(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add apps scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add aiops scheme: %v", err)
	}
	now := time.Date(2026, 7, 6, 10, 10, 0, 0, time.UTC)

	request := &v1alpha1.InvestigationRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "missing-ds", Namespace: "fluxagent-system", Generation: 1},
		Spec: v1alpha1.InvestigationRequestSpec{
			Target: v1alpha1.TargetRef{
				Namespace: "prod",
				Kind:      "Deployment",
				Name:      "open-api",
			},
			DataSources: []v1alpha1.LocalObjectReference{
				{Name: "prometheus"},
			},
			Mode: v1alpha1.InvestigationModeReadOnly,
		},
	}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "open-api", Namespace: "prod"},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.InvestigationRequest{}).
		WithObjects(request, deployment).
		Build()

	reconciler := &InvestigationRequestReconciler{
		Client: client,
		Scheme: scheme,
		Service: &investigation.Service{
			Client:   client,
			Registry: datasource.NewRegistry(fakeInvestigationDataSource{name: "kubernetes-events", queryType: domain.QueryTypeEvent}),
			Resolver: modelgateway.KubeResolver{Client: client},
			Gateway: &modelgateway.Gateway{
				Base: knowledge.NewBase(),
				Providers: model.NewRegistry(
					heuristic.Provider{},
				),
			},
		},
		Now: func() time.Time { return now },
	}
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: request.Name, Namespace: request.Namespace},
	}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var stored v1alpha1.InvestigationRequest
	if err := client.Get(context.Background(), types.NamespacedName{Name: request.Name, Namespace: request.Namespace}, &stored); err != nil {
		t.Fatalf("get request: %v", err)
	}
	if stored.Status.Phase != v1alpha1.PhaseFailed {
		t.Fatalf("expected failed phase, got %s", stored.Status.Phase)
	}
	if stored.Status.Outcome != v1alpha1.InvestigationOutcomeUnknown {
		t.Fatalf("expected unknown outcome for failed workflow, got %q", stored.Status.Outcome)
	}
	if stored.Status.Failure == nil ||
		stored.Status.Failure.Code != "DataSourceNotFound" ||
		stored.Status.Failure.Stage != v1alpha1.InvestigationStageEvidenceCollection ||
		stored.Status.Failure.Retryable {
		t.Fatalf("expected non-retryable datasource failure, got %#v", stored.Status.Failure)
	}
	if stored.Status.Degradation == nil ||
		!stored.Status.Degradation.Partial ||
		len(stored.Status.Degradation.Reasons) != 1 ||
		stored.Status.Degradation.Reasons[0].Code != "DataSourceNotFound" ||
		stored.Status.Degradation.Reasons[0].Stage != v1alpha1.InvestigationStageEvidenceCollection {
		t.Fatalf("expected structured datasource degradation reason, got %#v", stored.Status.Degradation)
	}
	if cond := findCondition(stored.Status.Conditions, conditionDatasourceResolved); cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "DataSourceNotFound" {
		t.Fatalf("expected DatasourceResolved false DataSourceNotFound, got %#v", cond)
	}
	if cond := findCondition(stored.Status.Conditions, conditionDegraded); cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != "DataSourceNotFound" {
		t.Fatalf("expected Degraded true DataSourceNotFound, got %#v", cond)
	}
}

func TestInvestigationRequestReconcilerMarksQueryTypeMismatch(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add apps scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add aiops scheme: %v", err)
	}
	now := time.Date(2026, 7, 6, 10, 20, 0, 0, time.UTC)

	request := &v1alpha1.InvestigationRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "bad-query", Namespace: "fluxagent-system", Generation: 1},
		Spec: v1alpha1.InvestigationRequestSpec{
			Target: v1alpha1.TargetRef{
				Namespace: "prod",
				Kind:      "Deployment",
				Name:      "open-api",
			},
			Queries: []v1alpha1.InvestigationQuery{
				{
					Name: "metric-on-events",
					DatasourceRef: v1alpha1.LocalObjectReference{
						Name: "kubernetes-events",
					},
					QueryType: "metric",
					Query:     "up",
				},
			},
			ModelProviderRef: v1alpha1.LocalObjectReference{Name: "heuristic-provider"},
			Mode:             v1alpha1.InvestigationModeReadOnly,
		},
	}
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "open-api", Namespace: "prod"}}
	provider := &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "heuristic-provider", Namespace: "fluxagent-system"},
		Spec:       v1alpha1.ModelProviderSpec{Provider: "heuristic", Model: "built-in"},
	}
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.InvestigationRequest{}).
		WithObjects(request, deployment, provider).
		Build()

	reconciler := &InvestigationRequestReconciler{
		Client: client,
		Scheme: scheme,
		Service: &investigation.Service{
			Client: client,
			Registry: datasource.NewRegistry(
				fakeInvestigationDataSource{name: "kubernetes-events", queryType: domain.QueryTypeEvent},
			),
			Resolver: modelgateway.KubeResolver{Client: client},
			Gateway: &modelgateway.Gateway{
				Base: knowledge.NewBase(),
				Providers: model.NewRegistry(
					heuristic.Provider{},
				),
			},
		},
		Now: func() time.Time { return now },
	}
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: request.Name, Namespace: request.Namespace},
	}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var stored v1alpha1.InvestigationRequest
	if err := client.Get(context.Background(), types.NamespacedName{Name: request.Name, Namespace: request.Namespace}, &stored); err != nil {
		t.Fatalf("get request: %v", err)
	}
	if stored.Status.Phase != v1alpha1.PhaseFailed {
		t.Fatalf("expected failed phase, got %s", stored.Status.Phase)
	}
	if stored.Status.Outcome != v1alpha1.InvestigationOutcomeUnknown {
		t.Fatalf("expected unknown outcome for failed workflow, got %q", stored.Status.Outcome)
	}
	if stored.Status.Failure == nil ||
		stored.Status.Failure.Code != "CapabilityMismatch" ||
		stored.Status.Failure.Stage != v1alpha1.InvestigationStageEvidenceCollection {
		t.Fatalf("expected capability mismatch failure, got %#v", stored.Status.Failure)
	}
	if cond := findCondition(stored.Status.Conditions, conditionQueryTypeSupported); cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "CapabilityMismatch" {
		t.Fatalf("expected QueryTypeSupported false CapabilityMismatch, got %#v", cond)
	}
}

type fakeInvestigationDataSource struct {
	name      string
	queryType domain.QueryType
}

func (f fakeInvestigationDataSource) Name() string { return f.name }
func (f fakeInvestigationDataSource) Type() string { return string(f.queryType) }
func (f fakeInvestigationDataSource) Capabilities() datasource.Capabilities {
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
func (f fakeInvestigationDataSource) Query(context.Context, datasource.QueryRequest) (*datasource.QueryResult, error) {
	records := []map[string]any{}
	switch f.queryType {
	case domain.QueryTypeEvent:
		records = []map[string]any{
			{"reason": "BackOff", "message": "container crashed"},
		}
	case domain.QueryTypeMetric:
		records = []map[string]any{
			{"metric": "http_requests_total", "value": "0.95"},
		}
	case domain.QueryTypeLog:
		records = []map[string]any{
			{"line": "error timeout"},
		}
	}
	return &datasource.QueryResult{Source: f.name, QueryType: f.queryType, Records: records}, nil
}
func (f fakeInvestigationDataSource) HealthCheck(context.Context) error { return nil }

func loopPolicyTestRequest(name string, annotations map[string]string) *v1alpha1.InvestigationRequest {
	return &v1alpha1.InvestigationRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   "fluxagent-system",
			Generation:  1,
			Annotations: annotations,
		},
		Spec: v1alpha1.InvestigationRequestSpec{
			Target: v1alpha1.TargetRef{
				Namespace:  "prod",
				Kind:       "Deployment",
				Name:       "open-api",
				APIVersion: "apps/v1",
			},
			DataSources: []v1alpha1.LocalObjectReference{
				{Name: "kubernetes-events"},
			},
			ModelProviderRef: v1alpha1.LocalObjectReference{Name: "counting-provider"},
			Mode:             v1alpha1.InvestigationModeReadOnly,
			CreateRiskSignal: true,
		},
	}
}

func loopPolicyTestReconciler(kubeClient client.Client, scheme *runtime.Scheme, provider *countingModelProvider) *InvestigationRequestReconciler {
	return &InvestigationRequestReconciler{
		Client: kubeClient,
		Scheme: scheme,
		Service: &investigation.Service{
			Client: kubeClient,
			Registry: datasource.NewRegistry(
				fakeInvestigationDataSource{name: "kubernetes-events", queryType: domain.QueryTypeEvent},
			),
			Resolver: modelgateway.KubeResolver{Client: kubeClient},
			Gateway: &modelgateway.Gateway{
				Base: knowledge.NewBase(),
				Providers: model.NewRegistry(
					provider,
				),
			},
		},
		Now: func() time.Time { return time.Date(2026, 7, 6, 10, 45, 0, 0, time.UTC) },
	}
}

func evidenceRequirementTestRequest(name string, profile string, datasourceName string) *v1alpha1.InvestigationRequest {
	return &v1alpha1.InvestigationRequest{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "fluxagent-system", Generation: 1},
		Spec: v1alpha1.InvestigationRequestSpec{
			Target: v1alpha1.TargetRef{
				Namespace:  "prod",
				Kind:       "Deployment",
				Name:       "open-api",
				APIVersion: "apps/v1",
			},
			DataSources: []v1alpha1.LocalObjectReference{{Name: datasourceName}},
			ModelProviderRef: v1alpha1.LocalObjectReference{
				Name: "counting-provider",
			},
			Mode: v1alpha1.InvestigationModeReadOnly,
			EvidenceRequirements: v1alpha1.EvidenceRequirements{
				Profile: profile,
			},
		},
	}
}

func evidenceRequirementDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "open-api", Namespace: "prod", Labels: map[string]string{"app": "open-api"}},
		Spec:       appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "open-api"}}}},
	}
}

func evidenceRequirementTestReconciler(kubeClient client.Client, scheme *runtime.Scheme, provider *countingModelProvider, sources ...datasource.DataSource) *InvestigationRequestReconciler {
	return &InvestigationRequestReconciler{
		Client: kubeClient,
		Scheme: scheme,
		Service: &investigation.Service{
			Client:   kubeClient,
			Registry: datasource.NewRegistry(sources...),
			Resolver: modelgateway.KubeResolver{Client: kubeClient},
			Gateway: &modelgateway.Gateway{
				Base: knowledge.NewBase(),
				Providers: model.NewRegistry(
					provider,
				),
			},
		},
		Now: func() time.Time { return time.Date(2026, 7, 6, 11, 0, 0, 0, time.UTC) },
	}
}

type countingModelProvider struct {
	calls int
}

func (p *countingModelProvider) Name() string { return "counting" }
func (p *countingModelProvider) Complete(context.Context, domain.ModelRequest) (domain.ModelResponse, error) {
	p.calls++
	return domain.ModelResponse{
		Provider:   "counting",
		Model:      "test-model",
		Structured: true,
		Output: map[string]any{
			"riskTitle":       "Provider should not be called",
			"riskSummary":     "Provider should not be called",
			"severity":        "low",
			"confidenceScore": 10,
			"rationale":       "unexpected call",
			"rcaHypothesis":   "unexpected call",
			"rcaCauses":       []string{"unexpected"},
			"actionType":      "notification.sendSlack",
		},
	}, nil
}

func checkpointReasoningOutput() domain.ReasoningOutput {
	return domain.ReasoningOutput{
		RiskTitle:   "Crash loop after rollout",
		RiskSummary: "Checkpointed RCA summary",
		Severity:    domain.SeverityHigh,
		Confidence: domain.Confidence{
			Score:            91,
			Severity:         domain.SeverityHigh,
			Rationale:        "checkpointed provider result",
			EvidenceCoverage: "events",
		},
		RCA: domain.RCASummary{
			Hypothesis: "The latest rollout introduced startup failures.",
			Causes:     []string{"startup failure"},
			Evidence:   []string{"BackOff"},
		},
		Remediation: domain.Remediation{
			ActionType:  "notification.sendSlack",
			Description: "Notify operators",
		},
		Provider:          "counting",
		ProviderRequestID: "provider-request-checkpoint-123",
	}
}

func hasPrefix(value string, prefix string) bool {
	return len(value) >= len(prefix) && value[:len(prefix)] == prefix
}
