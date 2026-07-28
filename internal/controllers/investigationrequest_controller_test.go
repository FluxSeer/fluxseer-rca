package controllers

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"fluxagent/api/v1alpha1"
	"fluxagent/internal/datasource"
	"fluxagent/internal/domain"
	"fluxagent/internal/investigation"
	"fluxagent/internal/knowledge"
	"fluxagent/internal/model"
	"fluxagent/internal/model/heuristic"
	"fluxagent/internal/modelgateway"
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
		ObjectMeta: metav1.ObjectMeta{Name: "investigate-open-api", Namespace: "fluxagent-system", Generation: 1},
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
	if stored.Status.Verdict.ConfidenceDetail.VerifiedScore != stored.Status.Confidence {
		t.Fatalf("expected verified confidence %f, got %#v", stored.Status.Confidence, stored.Status.Verdict.ConfidenceDetail)
	}
	if stored.Status.Verdict.ConfidenceDetail.Level == "" || stored.Status.Verdict.ConfidenceDetail.Method != "ProviderScoreV1" {
		t.Fatalf("expected confidence level and method, got %#v", stored.Status.Verdict.ConfidenceDetail)
	}
	if len(stored.Status.Claims) == 0 {
		t.Fatalf("expected structured claims, got %#v", stored.Status.Claims)
	}
	if stored.Status.Claims[0].Verification != "Supported" {
		t.Fatalf("expected first claim supported, got %#v", stored.Status.Claims[0])
	}
	if len(stored.Status.Claims[0].EvidenceRefs) != 1 || stored.Status.Claims[0].EvidenceRefs[0] != "evidence-001" {
		t.Fatalf("expected claim to cite evidence-001, got %#v", stored.Status.Claims[0])
	}
	if stored.Status.Execution == nil || stored.Status.Execution.Provider != "heuristic" || stored.Status.Execution.Attempts != 1 {
		t.Fatalf("expected RCA execution metadata, got %#v", stored.Status.Execution)
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
	if stored.Status.Execution.ReasoningPolicyVersion != "rca-v2-compat" || stored.Status.Execution.ControllerVersion == "" {
		t.Fatalf("expected execution policy and controller versions, got %#v", stored.Status.Execution)
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
