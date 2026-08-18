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

const runtimeRiskRuleLabelKey = "fluxseer-rca.aiops.platform/risk-rule"

type servicePortRuntimeCase struct {
	id                 string
	name               string
	servicePorts       []corev1.ServicePort
	containerPorts     []corev1.ContainerPort
	providerCause      string
	triggerRiskRule    bool
	expected           runtimeharness.ExpectedRuntimeResult
	expectedReportRCA  string
	expectedReportRefs []string
}

type servicePortRuntimeProvider struct {
	cause string
}

func (p servicePortRuntimeProvider) Name() string { return "service-port-runtime" }

func (p servicePortRuntimeProvider) Complete(context.Context, domain.ModelRequest) (domain.ModelResponse, error) {
	return domain.ModelResponse{
		Provider:   p.Name(),
		Model:      "deterministic-runtime-fixture",
		Structured: true,
		Output: map[string]any{
			"riskTitle":       "Service port mismatch",
			"riskSummary":     "The Kubernetes Service target port does not match the workload container port.",
			"severity":        "high",
			"confidenceScore": 95,
			"rationale":       "Kubernetes Service and workload port evidence are directly correlated.",
			"rcaHypothesis":   p.cause,
			"rcaCauses":       []string{p.cause},
			"actionType":      "notification.sendSlack",
		},
	}, nil
}

func TestServicePortMismatchRuntimeQualification(t *testing.T) {
	now := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	namespace := "fluxseer-runtime-service-port"
	cases := []servicePortRuntimeCase{
		{
			id:   "service-port-numeric-mismatch",
			name: "numeric targetPort mismatch",
			servicePorts: []corev1.ServicePort{{
				Name: "http", Port: 80, TargetPort: intstr.FromInt(8080),
			}},
			containerPorts:  []corev1.ContainerPort{{Name: "http", ContainerPort: 3000}},
			providerCause:   "Service service-port-numeric-mismatch targetPort 8080 does not match the workload container port 3000.",
			triggerRiskRule: true,
			expected: runtimeharness.ExpectedRuntimeResult{
				InvestigationPhase:       v1alpha1.PhaseCompleted,
				InvestigationOutcome:     v1alpha1.InvestigationOutcomeConfirmed,
				RootCauseType:            "ServicePortMismatch",
				RootCauseEntity:          "apps/v1/Deployment/" + namespace + "/service-port-numeric-mismatch",
				RequiredEvidenceSources:  []string{"kubernetes-events"},
				RequiredReasons:          []string{"ServicePortMismatch"},
				RiskSignalExpected:       true,
				RiskSignalPhase:          v1alpha1.PhaseConfirmed,
				FailureReason:            "",
				MaxUnexpectedSideEffects: 0,
			},
			expectedReportRCA:  "ServicePortMismatch",
			expectedReportRefs: []string{"ServicePortMismatch"},
		},
		{
			id:   "service-port-named-resolution",
			name: "named targetPort resolution with a bounded mismatch",
			servicePorts: []corev1.ServicePort{
				{Name: "http", Port: 80, TargetPort: intstr.FromString("http")},
				{Name: "admin", Port: 81, TargetPort: intstr.FromInt(9090)},
			},
			containerPorts:  []corev1.ContainerPort{{Name: "http", ContainerPort: 3000}},
			providerCause:   "Service service-port-named-resolution targetPort 9090 does not match the workload container port 3000; named targetPort http resolves to container port 3000.",
			triggerRiskRule: true,
			expected: runtimeharness.ExpectedRuntimeResult{
				InvestigationPhase:       v1alpha1.PhaseCompleted,
				InvestigationOutcome:     v1alpha1.InvestigationOutcomeConfirmed,
				RootCauseType:            "ServicePortMismatch",
				RootCauseEntity:          "apps/v1/Deployment/" + namespace + "/service-port-named-resolution",
				RequiredEvidenceSources:  []string{"kubernetes-events"},
				RequiredReasons:          []string{"ServicePortMismatch", "ServicePortResolved"},
				RiskSignalExpected:       true,
				RiskSignalPhase:          v1alpha1.PhaseConfirmed,
				FailureReason:            "",
				MaxUnexpectedSideEffects: 0,
			},
			expectedReportRCA:  "ServicePortMismatch",
			expectedReportRefs: []string{"ServicePortMismatch", "ServicePortResolved"},
		},
		{
			id:   "service-port-unresolved-named-target",
			name: "unresolved named targetPort",
			servicePorts: []corev1.ServicePort{{
				Name: "http", Port: 80, TargetPort: intstr.FromString("http"),
			}},
			containerPorts:  []corev1.ContainerPort{{Name: "metrics", ContainerPort: 3000}},
			providerCause:   "Service service-port-unresolved-named-target targetPort http cannot be resolved to a named workload container port.",
			triggerRiskRule: false,
			expected: runtimeharness.ExpectedRuntimeResult{
				InvestigationPhase:       v1alpha1.PhaseCompleted,
				InvestigationOutcome:     v1alpha1.InvestigationOutcomeInconclusive,
				RootCauseType:            "",
				RootCauseEntity:          "",
				RequiredEvidenceSources:  []string{"kubernetes-events"},
				RequiredReasons:          []string{"TargetPortUnresolved", "RequiredEvidenceMissing"},
				RiskSignalExpected:       false,
				RiskSignalPhase:          "",
				FailureReason:            "",
				MaxUnexpectedSideEffects: 0,
			},
			expectedReportRCA:  "",
			expectedReportRefs: []string{"TargetPortUnresolved"},
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
					return setupServicePortRuntimeCase(ctx, env, caseSpec, now)
				},
			}
			runner := &runtimeharness.Runner{
				Environment:  environment,
				SuiteID:      "v0.5-runtime-rca-matrix",
				SuiteName:    "v0.5 Runtime RCA Qualification",
				RunID:        "service-port-runtime-qualification",
				SourceCommit: "test",
			}
			report, err := runner.Run(context.Background(), []runtimeharness.RuntimeScenario{scenario})
			if err != nil {
				t.Fatalf("run runtime harness: %v", err)
			}
			if report.Summary.Result != "PASS" || report.Summary.Passed != 1 {
				t.Fatalf("runtime qualification failed: %#v", report.Scenarios)
			}
			assertPublicServicePortReport(t, artifactDir, caseSpec, namespace)
		})
	}
}

func setupServicePortRuntimeCase(ctx context.Context, env *runtimeharness.Environment, testCase servicePortRuntimeCase, now time.Time) error {
	appName := testCase.id
	labels := map[string]string{"app": appName}
	if err := env.Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: appName, Namespace: env.Namespace},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports:    append([]corev1.ServicePort(nil), testCase.servicePorts...),
		},
	}); err != nil {
		return fmt.Errorf("create Service fixture: %w", err)
	}
	if err := env.Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: appName, Namespace: env.Namespace, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Ports: append([]corev1.ContainerPort(nil), testCase.containerPorts...)}}},
			},
		},
	}); err != nil {
		return fmt.Errorf("create Deployment fixture: %w", err)
	}
	providerName := appName + "-provider"
	if err := env.Create(ctx, &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: providerName, Namespace: env.Namespace},
		Spec: v1alpha1.ModelProviderSpec{
			Provider:  "service-port-runtime",
			Model:     "deterministic-runtime-fixture",
			MaxTokens: 512,
		},
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
				WorkloadSelector:  v1alpha1.WorkloadSelector{MatchLabels: labels, Kinds: []string{"Deployment"}},
			},
			Signals: []v1alpha1.RiskRuleSignal{{
				Name:          "service-port-mismatch",
				DatasourceRef: v1alpha1.LocalObjectReference{Name: "kubernetes-events"},
				QueryType:     "serviceConfiguration",
				Threshold:     v1alpha1.RiskThreshold{Operator: "count_gt", Value: 0},
			}},
			AI: v1alpha1.RiskRuleAI{
				RCAEnabled:  false,
				ProviderRef: v1alpha1.LocalObjectReference{Name: providerName},
			},
			InvestigationPolicy: v1alpha1.RiskRuleInvestigationPolicy{
				Mode:             v1alpha1.RiskRuleInvestigationModeCreateRequest,
				CreateRiskSignal: true,
				EvidenceRequirements: v1alpha1.EvidenceRequirements{
					Profile: "serviceportmismatch",
				},
			},
		},
	}
	if err := env.Create(ctx, rule); err != nil {
		return fmt.Errorf("create RiskRule fixture: %w", err)
	}
	env.RegisterRiskRule(rule.Name)

	registry := datasource.NewRegistry(kubernetesdatasource.Adapter{Client: env.Client})
	if _, err := (&controllers.RiskRuleReconciler{
		Client:   env.Client,
		Scheme:   env.Scheme,
		Registry: registry,
		Now:      func() time.Time { return now },
	}).Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: rule.Name, Namespace: env.Namespace}}); err != nil {
		return fmt.Errorf("reconcile RiskRule fixture: %w", err)
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
	}
	if !testCase.triggerRiskRule {
		request := servicePortInvestigationRequest(rule, providerName, appName)
		requestName = request.Name
		if err := env.Create(ctx, request); err != nil {
			return fmt.Errorf("create InvestigationRequest fixture: %w", err)
		}
	}

	provider := servicePortRuntimeProvider{cause: testCase.providerCause}
	service := &investigation.Service{
		Client:   env.Client,
		Registry: registry,
		Resolver: modelgateway.KubeResolver{Client: env.Client},
		Gateway:  &modelgateway.Gateway{Base: knowledge.NewBase(), Providers: model.NewRegistry(provider)},
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

func servicePortInvestigationRequest(rule *v1alpha1.RiskRule, providerName, appName string) *v1alpha1.InvestigationRequest {
	return &v1alpha1.InvestigationRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      investigationRequestName(rule.Name, appName),
			Namespace: rule.Namespace,
			Labels:    map[string]string{runtimeRiskRuleLabelKey: rule.Name},
		},
		Spec: v1alpha1.InvestigationRequestSpec{
			Target: v1alpha1.TargetRef{APIVersion: "apps/v1", Kind: "Deployment", Namespace: rule.Namespace, Name: appName, Service: appName},
			Queries: []v1alpha1.InvestigationQuery{{
				Name:          "service-port-mismatch",
				DatasourceRef: v1alpha1.LocalObjectReference{Name: "kubernetes-events"},
				QueryType:     "serviceConfiguration",
			}},
			ModelProviderRef:     v1alpha1.LocalObjectReference{Name: providerName},
			Mode:                 v1alpha1.InvestigationModeReadOnly,
			CreateRiskSignal:     true,
			EvidenceRequirements: v1alpha1.EvidenceRequirements{Profile: "serviceportmismatch"},
			TimeRange:            v1alpha1.InvestigationTimeRange{Lookback: metav1.Duration{Duration: 10 * time.Minute}},
		},
	}
}

func investigationRequestName(ruleName, appName string) string {
	return ruleName + "-" + appName
}

func assertPublicServicePortReport(t *testing.T, artifactDir string, testCase servicePortRuntimeCase, namespace string) {
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
	if testCase.expectedReportRCA != "" {
		if request.Status.Verdict == nil || request.Status.Verdict.RootCauseType != testCase.expectedReportRCA {
			t.Fatalf("public report root cause mismatch: %#v", request.Status.Verdict)
		}
		if runtimeharness.TargetIdentity(request.Status.Verdict.RootCauseEntity) != testCase.expected.RootCauseEntity {
			t.Fatalf("public report root cause entity mismatch: got=%s want=%s", runtimeharness.TargetIdentity(request.Status.Verdict.RootCauseEntity), testCase.expected.RootCauseEntity)
		}
	} else if request.Status.Verdict == nil || request.Status.Verdict.RootCauseType != "" || runtimeharness.TargetIdentity(request.Status.Verdict.RootCauseEntity) != "" {
		t.Fatalf("public report fabricated root cause: %#v", request.Status.Verdict)
	}
	if testCase.expected.FailureReason == "" {
		if request.Status.Failure != nil && request.Status.Failure.Code != "" {
			t.Fatalf("public report contains unexpected failure reason: %#v", request.Status.Failure)
		}
	} else if request.Status.Failure == nil || request.Status.Failure.Code != testCase.expected.FailureReason {
		t.Fatalf("public report failure reason mismatch: %#v", request.Status.Failure)
	}
	reasons := map[string]bool{}
	for _, ref := range request.Status.EvidenceRefs {
		if ref.Source != "kubernetes-events" || ref.Kind != "serviceConfiguration" {
			t.Fatalf("public report evidence is not normalized service configuration: %#v", ref)
		}
		reasons[ref.Reason] = true
	}
	for _, required := range testCase.expectedReportRefs {
		if !reasons[required] {
			t.Fatalf("public report missing evidence reason %q: %#v", required, request.Status.EvidenceRefs)
		}
	}
	if testCase.expected.RiskSignalExpected && len(report.RiskSignals) != 1 {
		t.Fatalf("public report expected one projected RiskSignal, got %d", len(report.RiskSignals))
	}
	if testCase.expected.RiskSignalExpected {
		signal := report.RiskSignals[0]
		if signal.Status.Phase != testCase.expected.RiskSignalPhase || signal.Spec.InvestigationRef == nil || signal.Spec.InvestigationRef.Name != request.Name {
			t.Fatalf("public report RiskSignal projection mismatch: %#v", signal)
		}
		if signal.Status.Projection == nil || signal.Status.Projection.ProjectedFrom == nil || signal.Status.Projection.ProjectedFrom.Name != request.Name {
			t.Fatalf("public report missing RiskSignal RCA projection: %#v", signal.Status.Projection)
		}
		if len(signal.Spec.Evidence) == 0 {
			t.Fatal("public report RiskSignal is missing evidence refs")
		}
		for _, ref := range signal.Spec.Evidence {
			if ref.Kind != "serviceConfiguration" || ref.Source != "kubernetes-events" {
				t.Fatalf("public report RiskSignal evidence is not normalized service configuration: %#v", ref)
			}
		}
	}
	if !testCase.expected.RiskSignalExpected && len(report.RiskSignals) != 0 {
		t.Fatalf("public report expected no RiskSignal, got %d", len(report.RiskSignals))
	}
}
