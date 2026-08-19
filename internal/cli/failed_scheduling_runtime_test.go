package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/controllers"
	"github.com/FluxSeer/fluxseer-rca/internal/datasource"
	kubernetesdatasource "github.com/FluxSeer/fluxseer-rca/internal/datasource/kubernetes"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
	"github.com/FluxSeer/fluxseer-rca/internal/investigation"
	"github.com/FluxSeer/fluxseer-rca/internal/knowledge"
	"github.com/FluxSeer/fluxseer-rca/internal/model"
	"github.com/FluxSeer/fluxseer-rca/internal/modelgateway"
	"github.com/FluxSeer/fluxseer-rca/internal/runtimeharness"
)

type failedSchedulingRuntimeCase struct {
	id              string
	name            string
	eventMessage    string
	providerSummary string
	providerCause   string
	triggerRiskRule bool
	expected        runtimeharness.ExpectedRuntimeResult
}

type failedSchedulingRuntimeProvider struct {
	summary string
	cause   string
}

func (p failedSchedulingRuntimeProvider) Name() string { return "failed-scheduling-runtime" }

func (p failedSchedulingRuntimeProvider) Complete(context.Context, domain.ModelRequest) (domain.ModelResponse, error) {
	return domain.ModelResponse{
		Provider:   p.Name(),
		Model:      "deterministic-runtime-fixture",
		Structured: true,
		Output: map[string]any{
			"riskTitle":       "Pending pod scheduling risk",
			"riskSummary":     p.summary,
			"severity":        "high",
			"confidenceScore": 91,
			"rationale":       "The scheduler event is evaluated only when a bounded causal predicate is present.",
			"rcaHypothesis":   p.cause,
			"rcaCauses":       []string{p.cause},
			"actionType":      "notification.sendSlack",
		},
	}, nil
}

func TestFailedSchedulingRuntimeQualification(t *testing.T) {
	now := time.Date(2026, 8, 18, 11, 0, 0, 0, time.UTC)
	namespace := "fluxseer-runtime-failed-scheduling"
	cases := []failedSchedulingRuntimeCase{
		{
			id:              "failed-scheduling-insufficient-memory",
			name:            "FailedScheduling with bounded insufficient memory predicate",
			eventMessage:    "0/3 nodes are available: 3 Insufficient memory.",
			providerSummary: "The Pending Pod cannot be scheduled because eligible nodes have insufficient memory.",
			providerCause:   "FailedScheduling reports 3 Insufficient memory nodes for the Pending Pod.",
			triggerRiskRule: true,
			expected: runtimeharness.ExpectedRuntimeResult{
				InvestigationPhase:       v1alpha1.PhaseCompleted,
				InvestigationOutcome:     v1alpha1.InvestigationOutcomeConfirmed,
				RootCauseType:            "SchedulingFailure",
				RootCauseEntity:          "v1/Pod/" + namespace + "/failed-scheduling-insufficient-memory",
				RequiredEvidenceSources:  []string{"kubernetes-events"},
				RequiredReasons:          []string{"FailedScheduling"},
				RiskSignalExpected:       true,
				RiskSignalPhase:          v1alpha1.PhaseConfirmed,
				FailureReason:            "",
				MaxUnexpectedSideEffects: 0,
			},
		},
		{
			id:              "failed-scheduling-unmapped-predicate",
			name:            "FailedScheduling without bounded causal predicate",
			eventMessage:    "0/3 nodes are blocked: reason code redacted.",
			providerSummary: "The scheduler incident remains under investigation.",
			providerCause:   "No supported causal classification exists.",
			triggerRiskRule: false,
			expected: runtimeharness.ExpectedRuntimeResult{
				InvestigationPhase:       v1alpha1.PhaseCompleted,
				InvestigationOutcome:     v1alpha1.InvestigationOutcomeInconclusive,
				RootCauseType:            "",
				RootCauseEntity:          "",
				RequiredEvidenceSources:  []string{"kubernetes-events"},
				RequiredReasons:          []string{"FailedScheduling"},
				RiskSignalExpected:       false,
				RiskSignalPhase:          "",
				FailureReason:            "",
				MaxUnexpectedSideEffects: 0,
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.id, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatalf("add core scheme: %v", err)
			}
			if err := v1alpha1.AddToScheme(scheme); err != nil {
				t.Fatalf("add FluxSeer scheme: %v", err)
			}
			kubeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&v1alpha1.RiskRule{}, &v1alpha1.InvestigationRequest{}, &v1alpha1.RiskSignal{}).
				Build()
			artifactDir := t.TempDir()
			environment := runtimeharness.NewEnvironment(kubeClient, scheme, namespace, artifactDir)
			environment.Timeout = 2 * time.Second
			environment.PollInterval = time.Millisecond
			environment.Now = func() time.Time { return now }
			environment.PublicReport = func(ctx context.Context, env *runtimeharness.Environment, ruleName string) ([]byte, error) {
				report, err := buildRiskRuleReport(ctx, env.Client, env.Namespace, ruleName)
				if err != nil {
					return nil, err
				}
				return json.Marshal(report)
			}

			caseSpec := testCase
			scenario := runtimeharness.RuntimeScenario{
				ID:       caseSpec.id,
				Name:     caseSpec.name,
				Expected: caseSpec.expected,
				Public:   true,
				Setup: func(ctx context.Context, env *runtimeharness.Environment) error {
					return setupFailedSchedulingRuntimeCase(ctx, env, caseSpec, now)
				},
			}
			runner := &runtimeharness.Runner{
				Environment:  environment,
				SuiteID:      "v0.5-runtime-rca-matrix",
				SuiteName:    "v0.5 Runtime RCA Qualification",
				RunID:        "failed-scheduling-runtime-qualification",
				SourceCommit: "test",
			}
			report, err := runner.Run(context.Background(), []runtimeharness.RuntimeScenario{scenario})
			if err != nil {
				t.Fatalf("run runtime harness: %v", err)
			}
			if report.Summary.Result != "PASS" || report.Summary.Passed != 1 {
				t.Fatalf("runtime qualification failed: %#v", report.Scenarios)
			}
			assertPublicFailedSchedulingReport(t, artifactDir, caseSpec, namespace)
		})
	}
}

func setupFailedSchedulingRuntimeCase(ctx context.Context, env *runtimeharness.Environment, testCase failedSchedulingRuntimeCase, now time.Time) error {
	appName := testCase.id
	labels := map[string]string{"app": appName}
	if err := env.Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: appName, Namespace: env.Namespace, Labels: labels},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "example.invalid/checkout:qualification"}}},
		Status:     corev1.PodStatus{Phase: corev1.PodPending},
	}); err != nil {
		return fmt.Errorf("create Pending Pod fixture: %w", err)
	}
	if err := env.Create(ctx, &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: appName + "-failed-scheduling", Namespace: env.Namespace},
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: appName},
		Reason:         "FailedScheduling",
		Message:        testCase.eventMessage,
		Type:           corev1.EventTypeWarning,
		LastTimestamp:  metav1.NewTime(now),
	}); err != nil {
		return fmt.Errorf("create FailedScheduling Event fixture: %w", err)
	}
	providerName := appName + "-provider"
	if err := env.Create(ctx, &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: providerName, Namespace: env.Namespace},
		Spec:       v1alpha1.ModelProviderSpec{Provider: "failed-scheduling-runtime", Model: "deterministic-runtime-fixture", MaxTokens: 512},
	}); err != nil {
		return fmt.Errorf("create ModelProvider fixture: %w", err)
	}
	rule := &v1alpha1.RiskRule{
		ObjectMeta: metav1.ObjectMeta{Name: appName + "-rule", Namespace: env.Namespace},
		Spec: v1alpha1.RiskRuleSpec{
			Window:   metav1.Duration{Duration: 10 * time.Minute},
			Severity: "high",
			TargetSelector: v1alpha1.TargetSelector{
				NamespaceSelector: v1alpha1.NamespaceSelector{MatchNames: []string{env.Namespace}},
				WorkloadSelector:  v1alpha1.WorkloadSelector{MatchLabels: labels, Kinds: []string{"Pod"}},
			},
			Signals: []v1alpha1.RiskRuleSignal{{
				Name:          "failed-scheduling",
				DatasourceRef: v1alpha1.LocalObjectReference{Name: "kubernetes-events"},
				QueryType:     "event",
				Reasons:       []string{"FailedScheduling"},
				Threshold:     v1alpha1.RiskThreshold{Operator: "count_gt", Value: 0},
			}},
			AI: v1alpha1.RiskRuleAI{ProviderRef: v1alpha1.LocalObjectReference{Name: providerName}},
			InvestigationPolicy: v1alpha1.RiskRuleInvestigationPolicy{
				Mode:             v1alpha1.RiskRuleInvestigationModeCreateRequest,
				CreateRiskSignal: true,
			},
		},
	}
	if err := env.Create(ctx, rule); err != nil {
		return fmt.Errorf("create RiskRule fixture: %w", err)
	}
	env.RegisterRiskRule(rule.Name)

	registry := datasource.NewRegistry(kubernetesdatasource.Adapter{Client: env.Client})
	if testCase.triggerRiskRule {
		if _, err := (&controllers.RiskRuleReconciler{
			Client:   env.Client,
			Scheme:   env.Scheme,
			Registry: registry,
			Now:      func() time.Time { return now },
		}).Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: rule.Name, Namespace: env.Namespace}}); err != nil {
			return fmt.Errorf("reconcile RiskRule fixture: %w", err)
		}
	}

	requestName := ""
	if testCase.triggerRiskRule {
		var requests v1alpha1.InvestigationRequestList
		if err := env.Client.List(ctx, &requests, client.InNamespace(env.Namespace), client.MatchingLabels{runtimeRiskRuleLabelKey: rule.Name}); err != nil {
			return fmt.Errorf("list generated InvestigationRequests: %w", err)
		}
		if len(requests.Items) != 1 {
			return fmt.Errorf("expected one generated InvestigationRequest, got %d", len(requests.Items))
		}
		requestName = requests.Items[0].Name
	} else {
		request := failedSchedulingInvestigationRequest(rule, providerName, appName)
		requestName = request.Name
		if err := env.Create(ctx, request); err != nil {
			return fmt.Errorf("create InvestigationRequest fixture: %w", err)
		}
	}

	service := &investigation.Service{
		Client:   env.Client,
		Registry: registry,
		Resolver: modelgateway.KubeResolver{Client: env.Client},
		Gateway: &modelgateway.Gateway{
			Base:      knowledge.NewBase(),
			Providers: model.NewRegistry(failedSchedulingRuntimeProvider{summary: testCase.providerSummary, cause: testCase.providerCause}),
		},
	}
	if _, err := (&controllers.InvestigationRequestReconciler{
		Client:  env.Client,
		Scheme:  env.Scheme,
		Service: service,
		Now:     func() time.Time { return now },
	}).Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: requestName, Namespace: env.Namespace}}); err != nil {
		return fmt.Errorf("reconcile InvestigationRequest fixture: %w", err)
	}
	return nil
}

func failedSchedulingInvestigationRequest(rule *v1alpha1.RiskRule, providerName, podName string) *v1alpha1.InvestigationRequest {
	return &v1alpha1.InvestigationRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      investigationRequestName(rule.Name, podName),
			Namespace: rule.Namespace,
			Labels:    map[string]string{runtimeRiskRuleLabelKey: rule.Name},
		},
		Spec: v1alpha1.InvestigationRequestSpec{
			Target: v1alpha1.TargetRef{APIVersion: "v1", Kind: "Pod", Namespace: rule.Namespace, Name: podName},
			Queries: []v1alpha1.InvestigationQuery{{
				Name:          "failed-scheduling",
				DatasourceRef: v1alpha1.LocalObjectReference{Name: "kubernetes-events"},
				QueryType:     "event",
				Reasons:       []string{"FailedScheduling"},
			}},
			ModelProviderRef: v1alpha1.LocalObjectReference{Name: providerName},
			Mode:             v1alpha1.InvestigationModeReadOnly,
			CreateRiskSignal: true,
			TimeRange:        v1alpha1.InvestigationTimeRange{Lookback: metav1.Duration{Duration: 10 * time.Minute}},
		},
	}
}

func assertPublicFailedSchedulingReport(t *testing.T, artifactDir string, testCase failedSchedulingRuntimeCase, namespace string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(artifactDir, "user-facing", "scenarios", testCase.id, "report.json"))
	if err != nil {
		t.Fatalf("read public report: %v", err)
	}
	var report riskRuleReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("decode public report: %v", err)
	}
	if report.SchemaVersion != riskRuleReportSchemaVersion || report.Selection.Namespace != namespace || len(report.InvestigationRequests) != 1 {
		t.Fatalf("unexpected public report selection or request count: %#v", report)
	}
	request := report.InvestigationRequests[0]
	if request.Status.Phase != testCase.expected.InvestigationPhase || request.Status.Outcome != testCase.expected.InvestigationOutcome {
		t.Fatalf("public report investigation status mismatch: phase=%s outcome=%s", request.Status.Phase, request.Status.Outcome)
	}
	if testCase.expected.RootCauseType == "" {
		if request.Status.Verdict == nil || request.Status.Verdict.RootCauseType != "" || runtimeharness.TargetIdentity(request.Status.Verdict.RootCauseEntity) != "" {
			t.Fatalf("public report fabricated root cause: %#v", request.Status.Verdict)
		}
	} else {
		if request.Status.Verdict == nil || request.Status.Verdict.RootCauseType != testCase.expected.RootCauseType || runtimeharness.TargetIdentity(request.Status.Verdict.RootCauseEntity) != testCase.expected.RootCauseEntity {
			t.Fatalf("public report root cause mismatch: %#v", request.Status.Verdict)
		}
	}
	if request.Status.Failure != nil && request.Status.Failure.Code != testCase.expected.FailureReason {
		t.Fatalf("public report failure reason mismatch: %#v", request.Status.Failure)
	}
	if len(request.Status.EvidenceRefs) == 0 {
		t.Fatal("public report is missing scheduler evidence refs")
	}
	for _, ref := range request.Status.EvidenceRefs {
		if ref.Kind != "event" || ref.Source != "kubernetes-events" || ref.Reason != "FailedScheduling" {
			t.Fatalf("public report scheduler evidence mismatch: %#v", ref)
		}
	}
	if testCase.expected.RiskSignalExpected {
		if len(report.RiskSignals) != 1 {
			t.Fatalf("public report expected one projected RiskSignal, got %d", len(report.RiskSignals))
		}
		signal := report.RiskSignals[0]
		if signal.Status.Phase != testCase.expected.RiskSignalPhase || signal.Spec.InvestigationRef == nil || signal.Spec.InvestigationRef.Name != request.Name {
			t.Fatalf("public report RiskSignal projection mismatch: %#v", signal)
		}
		if signal.Status.Projection == nil || signal.Status.Projection.ProjectedFrom == nil || signal.Status.Projection.ProjectedFrom.Name != request.Name {
			t.Fatalf("public report missing RiskSignal projection: %#v", signal.Status.Projection)
		}
	} else if len(report.RiskSignals) != 0 {
		t.Fatalf("public report expected no RiskSignal, got %d", len(report.RiskSignals))
	}
}
