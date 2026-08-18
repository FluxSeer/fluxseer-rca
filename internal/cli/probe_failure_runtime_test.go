package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
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

type probeFailureRuntimeCase struct {
	id              string
	name            string
	eventMessage    string
	providerSummary string
	providerCause   string
	triggerRiskRule bool
	expected        runtimeharness.ExpectedRuntimeResult
}

type probeFailureRuntimeProvider struct {
	summary string
	cause   string
}

func (p probeFailureRuntimeProvider) Name() string { return "probe-failure-runtime" }

func (p probeFailureRuntimeProvider) Complete(context.Context, domain.ModelRequest) (domain.ModelResponse, error) {
	return domain.ModelResponse{
		Provider:   p.Name(),
		Model:      "deterministic-runtime-fixture",
		Structured: true,
		Output: map[string]any{
			"riskTitle":       "Probe failure risk",
			"riskSummary":     p.summary,
			"severity":        "high",
			"confidenceScore": 90,
			"rationale":       "A probe symptom is confirmed only with bounded probe configuration evidence.",
			"rcaHypothesis":   p.cause,
			"rcaCauses":       []string{p.cause},
			"actionType":      "notification.sendSlack",
		},
	}, nil
}

func TestProbeFailureRuntimeQualification(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	namespace := "fluxseer-runtime-probe-failure"
	cases := []probeFailureRuntimeCase{
		{
			id:              "probe-failure-config-mismatch",
			name:            "readiness probe failure with bounded configuration mismatch",
			eventMessage:    "Readiness probe failed: HTTP GET /ready returned connection refused.",
			providerSummary: "The checkout workload is not ready because its readiness probe configuration is inconsistent with the declared workload endpoint.",
			providerCause:   "The readiness probe configuration is inconsistent with the declared workload endpoint.",
			triggerRiskRule: true,
			expected: runtimeharness.ExpectedRuntimeResult{
				InvestigationPhase:       v1alpha1.PhaseCompleted,
				InvestigationOutcome:     v1alpha1.InvestigationOutcomeConfirmed,
				RootCauseType:            "ProbeFailure",
				RootCauseEntity:          "apps/v1/Deployment/" + namespace + "/probe-failure-config-mismatch",
				RequiredEvidenceSources:  []string{"kubernetes-events"},
				RequiredReasons:          []string{"Unhealthy", "ProbeConfigurationMismatch"},
				RiskSignalExpected:       true,
				RiskSignalPhase:          v1alpha1.PhaseConfirmed,
				FailureReason:            "",
				MaxUnexpectedSideEffects: 0,
			},
		},
		{
			id:              "probe-failure-event-only",
			name:            "readiness probe failure without causal configuration evidence",
			eventMessage:    "Readiness probe failed",
			providerSummary: "The workload readiness symptom remains under investigation.",
			providerCause:   "The readiness probe failure has no supported causal classification.",
			triggerRiskRule: false,
			expected: runtimeharness.ExpectedRuntimeResult{
				InvestigationPhase:       v1alpha1.PhaseCompleted,
				InvestigationOutcome:     v1alpha1.InvestigationOutcomeInconclusive,
				RootCauseType:            "",
				RootCauseEntity:          "",
				RequiredEvidenceSources:  []string{"kubernetes-events"},
				RequiredReasons:          []string{"Unhealthy"},
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
			if err := appsv1.AddToScheme(scheme); err != nil {
				t.Fatalf("add apps scheme: %v", err)
			}
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
					return setupProbeFailureRuntimeCase(ctx, env, caseSpec, now)
				},
			}
			runner := &runtimeharness.Runner{
				Environment:  environment,
				SuiteID:      "v0.5-runtime-rca-matrix",
				SuiteName:    "v0.5 Runtime RCA Qualification",
				RunID:        "probe-failure-runtime-qualification",
				SourceCommit: "test",
			}
			report, err := runner.Run(context.Background(), []runtimeharness.RuntimeScenario{scenario})
			if err != nil {
				t.Fatalf("run runtime harness: %v", err)
			}
			if report.Summary.Result != "PASS" || report.Summary.Passed != 1 {
				t.Fatalf("runtime qualification failed: %#v", report.Scenarios)
			}
			assertPublicProbeFailureReport(t, artifactDir, caseSpec, namespace)
		})
	}
}

func setupProbeFailureRuntimeCase(ctx context.Context, env *runtimeharness.Environment, testCase probeFailureRuntimeCase, now time.Time) error {
	appName := testCase.id
	labels := map[string]string{"app": appName}
	if err := env.Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: appName, Namespace: env.Namespace, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{
					Name:  "app",
					Image: "example.invalid/checkout:qualification",
					Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 3000}},
					ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
						Path: "/ready", Port: intstr.FromInt(8080), Scheme: corev1.URISchemeHTTP,
					}}},
				}}},
			},
		},
	}); err != nil {
		return fmt.Errorf("create Deployment fixture: %w", err)
	}
	if err := env.Create(ctx, &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: appName + "-unhealthy", Namespace: env.Namespace},
		InvolvedObject: corev1.ObjectReference{Kind: "Deployment", Name: appName},
		Reason:         "Unhealthy",
		Message:        testCase.eventMessage,
		Type:           corev1.EventTypeWarning,
	}); err != nil {
		return fmt.Errorf("create Unhealthy Event fixture: %w", err)
	}
	providerName := appName + "-provider"
	if err := env.Create(ctx, &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: providerName, Namespace: env.Namespace},
		Spec:       v1alpha1.ModelProviderSpec{Provider: "probe-failure-runtime", Model: "deterministic-runtime-fixture", MaxTokens: 512},
	}); err != nil {
		return fmt.Errorf("create ModelProvider fixture: %w", err)
	}
	rule := probeFailureRiskRule(env.Namespace, appName, labels, providerName)
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
		request := probeFailureInvestigationRequest(rule, providerName, appName)
		requestName = request.Name
		if err := env.Create(ctx, request); err != nil {
			return fmt.Errorf("create InvestigationRequest fixture: %w", err)
		}
	}

	service := &investigation.Service{
		Client:   env.Client,
		Registry: registry,
		Resolver: modelgateway.KubeResolver{Client: env.Client},
		Gateway:  &modelgateway.Gateway{Base: knowledge.NewBase(), Providers: model.NewRegistry(probeFailureRuntimeProvider{summary: testCase.providerSummary, cause: testCase.providerCause})},
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

func probeFailureRiskRule(namespace, appName string, labels map[string]string, providerName string) *v1alpha1.RiskRule {
	return &v1alpha1.RiskRule{
		ObjectMeta: metav1.ObjectMeta{Name: appName + "-rule", Namespace: namespace},
		Spec: v1alpha1.RiskRuleSpec{
			Window: metav1.Duration{Duration: 10 * time.Minute}, Severity: "high",
			TargetSelector: v1alpha1.TargetSelector{
				NamespaceSelector: v1alpha1.NamespaceSelector{MatchNames: []string{namespace}},
				WorkloadSelector:  v1alpha1.WorkloadSelector{MatchLabels: labels, Kinds: []string{"Deployment"}},
			},
			Signals: []v1alpha1.RiskRuleSignal{
				{Name: "probe-failure", DatasourceRef: v1alpha1.LocalObjectReference{Name: "kubernetes-events"}, QueryType: "event", Reasons: []string{"Unhealthy"}, Threshold: v1alpha1.RiskThreshold{Operator: "count_gt", Value: 0}},
				{Name: "probe-configuration", DatasourceRef: v1alpha1.LocalObjectReference{Name: "kubernetes-events"}, QueryType: "probeConfiguration", Threshold: v1alpha1.RiskThreshold{Operator: "count_gt", Value: 0}},
			},
			AI: v1alpha1.RiskRuleAI{ProviderRef: v1alpha1.LocalObjectReference{Name: providerName}},
			InvestigationPolicy: v1alpha1.RiskRuleInvestigationPolicy{
				Mode: v1alpha1.RiskRuleInvestigationModeCreateRequest, CreateRiskSignal: true,
				EvidenceRequirements: v1alpha1.EvidenceRequirements{Profile: "ProbeFailure"},
			},
		},
	}
}

func probeFailureInvestigationRequest(rule *v1alpha1.RiskRule, providerName, appName string) *v1alpha1.InvestigationRequest {
	return &v1alpha1.InvestigationRequest{
		ObjectMeta: metav1.ObjectMeta{Name: appName + "-manual-investigation", Namespace: rule.Namespace, Labels: map[string]string{runtimeRiskRuleLabelKey: rule.Name}},
		Spec: v1alpha1.InvestigationRequestSpec{
			Target:           v1alpha1.TargetRef{APIVersion: "apps/v1", Kind: "Deployment", Namespace: rule.Namespace, Name: appName},
			Queries:          []v1alpha1.InvestigationQuery{{Name: "probe-failure", DatasourceRef: v1alpha1.LocalObjectReference{Name: "kubernetes-events"}, QueryType: "event", Reasons: []string{"Unhealthy"}}},
			ModelProviderRef: providerNameRef(providerName), Mode: v1alpha1.InvestigationModeReadOnly, CreateRiskSignal: true,
			EvidenceRequirements: v1alpha1.EvidenceRequirements{Profile: "ProbeFailure"},
			TimeRange:            v1alpha1.InvestigationTimeRange{Lookback: metav1.Duration{Duration: 10 * time.Minute}},
		},
	}
}

func providerNameRef(name string) v1alpha1.LocalObjectReference {
	return v1alpha1.LocalObjectReference{Name: name}
}

func assertPublicProbeFailureReport(t *testing.T, artifactDir string, testCase probeFailureRuntimeCase, namespace string) {
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
	if request.Spec.Target.Name != testCase.id {
		t.Fatalf("public report lost investigation target: %#v", request.Spec.Target)
	}
	if testCase.expected.RootCauseType == "" {
		if request.Status.Verdict == nil || request.Status.Verdict.RootCauseType != "" || runtimeharness.TargetIdentity(request.Status.Verdict.RootCauseEntity) != "" {
			t.Fatalf("public report fabricated root cause: %#v", request.Status.Verdict)
		}
	} else if request.Status.Verdict == nil || request.Status.Verdict.RootCauseType != testCase.expected.RootCauseType || runtimeharness.TargetIdentity(request.Status.Verdict.RootCauseEntity) != testCase.expected.RootCauseEntity {
		t.Fatalf("public report root cause mismatch: %#v", request.Status.Verdict)
	}
	if request.Status.Failure != nil && request.Status.Failure.Code != testCase.expected.FailureReason {
		t.Fatalf("public report failure reason mismatch: %#v", request.Status.Failure)
	}
	reasons := map[string]bool{}
	for _, ref := range request.Status.EvidenceRefs {
		if ref.Source != "kubernetes-events" {
			t.Fatalf("public report evidence source mismatch: %#v", ref)
		}
		reasons[ref.Reason] = true
	}
	for _, required := range testCase.expected.RequiredReasons {
		if !reasons[required] {
			t.Fatalf("public report missing evidence reason %q: %#v", required, request.Status.EvidenceRefs)
		}
	}
	if testCase.expected.RiskSignalExpected {
		if len(report.RiskSignals) != 1 || report.RiskSignals[0].Status.Phase != testCase.expected.RiskSignalPhase {
			t.Fatalf("public report RiskSignal projection mismatch: %#v", report.RiskSignals)
		}
		signal := report.RiskSignals[0]
		if signal.Spec.InvestigationRef == nil || signal.Spec.InvestigationRef.Name != request.Name || signal.Status.Projection == nil || signal.Status.Projection.ProjectedFrom == nil || signal.Status.Projection.ProjectedFrom.Name != request.Name {
			t.Fatalf("public report missing RiskSignal investigation projection: %#v", signal)
		}
	} else if len(report.RiskSignals) != 0 {
		t.Fatalf("public report expected no RiskSignal, got %d", len(report.RiskSignals))
	}
}
