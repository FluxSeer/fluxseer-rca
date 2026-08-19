package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
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
	lokidatasource "github.com/FluxSeer/fluxseer-rca/internal/datasource/loki"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
	"github.com/FluxSeer/fluxseer-rca/internal/investigation"
	"github.com/FluxSeer/fluxseer-rca/internal/knowledge"
	"github.com/FluxSeer/fluxseer-rca/internal/model"
	"github.com/FluxSeer/fluxseer-rca/internal/modelgateway"
	"github.com/FluxSeer/fluxseer-rca/internal/runtimeharness"
)

type crashLoopBackOffRuntimeCase struct {
	id              string
	name            string
	applicationLog  string
	triggerRiskRule bool
	expected        runtimeharness.ExpectedRuntimeResult
}

type crashLoopBackOffRuntimeProvider struct {
	applicationLog string
}

func (p crashLoopBackOffRuntimeProvider) Name() string { return "crashloopbackoff-runtime" }

func (p crashLoopBackOffRuntimeProvider) Complete(context.Context, domain.ModelRequest) (domain.ModelResponse, error) {
	return domain.ModelResponse{
		Provider:   p.Name(),
		Model:      "deterministic-runtime-fixture",
		Structured: true,
		Output: map[string]any{
			"riskTitle":       "CrashLoopBackOff risk",
			"riskSummary":     "The workload is repeatedly restarting.",
			"severity":        "high",
			"confidenceScore": 91,
			"rationale":       "A BackOff symptom is confirmed with bounded application log evidence.",
			"rcaHypothesis":   fmt.Sprintf("The application failure shown by %s caused the observed CrashLoopBackOff.", p.applicationLog),
			"rcaCauses":       []string{"The application failed during startup and repeatedly restarted."},
			"actionType":      "notification.sendSlack",
		},
	}, nil
}

func TestCrashLoopBackOffRuntimeQualification(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	namespace := "fluxseer-runtime-crashloop"
	cases := []crashLoopBackOffRuntimeCase{
		{
			id:              "crashloop-application-startup-failure",
			name:            "CrashLoopBackOff with bounded application startup evidence",
			applicationLog:  "panic during startup: invalid configuration for checkout",
			triggerRiskRule: true,
			expected: runtimeharness.ExpectedRuntimeResult{
				InvestigationPhase:       v1alpha1.PhaseCompleted,
				InvestigationOutcome:     v1alpha1.InvestigationOutcomeConfirmed,
				RootCauseType:            "CrashLoop",
				RootCauseEntity:          "apps/v1/Deployment/" + namespace + "/crashloop-application-startup-failure",
				RequiredEvidenceSources:  []string{"kubernetes-events", "loki"},
				RequiredReasons:          []string{"BackOff"},
				RiskSignalExpected:       true,
				RiskSignalPhase:          v1alpha1.PhaseConfirmed,
				FailureReason:            "",
				MaxUnexpectedSideEffects: 0,
			},
		},
		{
			id:              "crashloop-event-only",
			name:            "CrashLoopBackOff without application causal evidence",
			applicationLog:  "",
			triggerRiskRule: false,
			expected: runtimeharness.ExpectedRuntimeResult{
				InvestigationPhase:       v1alpha1.PhaseCompleted,
				InvestigationOutcome:     v1alpha1.InvestigationOutcomeInconclusive,
				RootCauseType:            "",
				RootCauseEntity:          "",
				RequiredEvidenceSources:  []string{"kubernetes-events"},
				RequiredReasons:          []string{"BackOff"},
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

			lokiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/loki/api/v1/query_range" {
					http.NotFound(w, r)
					return
				}
				if testCase.applicationLog == "" {
					if err := json.NewEncoder(w).Encode(map[string]any{
						"status": "success",
						"data":   map[string]any{"resultType": "streams", "result": []any{}},
					}); err != nil {
						t.Fatalf("encode empty Loki response: %v", err)
					}
					return
				}
				if err := json.NewEncoder(w).Encode(map[string]any{
					"status": "success",
					"data": map[string]any{
						"resultType": "streams",
						"result": []any{map[string]any{
							"stream": map[string]string{"namespace": namespace, "app": testCase.id},
							"values": [][]string{{"1787054400000000000", testCase.applicationLog}},
						}},
					},
				}); err != nil {
					t.Fatalf("encode Loki response: %v", err)
				}
			}))

			caseSpec := testCase
			scenario := runtimeharness.RuntimeScenario{
				ID:       caseSpec.id,
				Name:     caseSpec.name,
				Expected: caseSpec.expected,
				Public:   true,
				Setup: func(ctx context.Context, env *runtimeharness.Environment) error {
					return setupCrashLoopBackOffRuntimeCase(ctx, env, caseSpec, now, lokiServer.URL)
				},
			}
			runner := &runtimeharness.Runner{
				Environment:  environment,
				SuiteID:      "v0.5-runtime-rca-matrix",
				SuiteName:    "v0.5 Runtime RCA Qualification",
				RunID:        "crashloopbackoff-runtime-qualification",
				SourceCommit: "test",
			}
			report, err := runner.Run(context.Background(), []runtimeharness.RuntimeScenario{scenario})
			lokiServer.Close()
			if err != nil {
				t.Fatalf("run runtime harness: %v", err)
			}
			if report.Summary.Result != "PASS" || report.Summary.Passed != 1 {
				t.Fatalf("runtime qualification failed: %#v", report.Scenarios)
			}
			assertPublicCrashLoopBackOffReport(t, artifactDir, caseSpec, namespace)
		})
	}
}

func setupCrashLoopBackOffRuntimeCase(ctx context.Context, env *runtimeharness.Environment, testCase crashLoopBackOffRuntimeCase, now time.Time, lokiURL string) error {
	labels := map[string]string{"app": testCase.id}
	if err := env.Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: testCase.id, Namespace: env.Namespace, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{
					Name:  "app",
					Image: "example.invalid/checkout:qualification",
				}}},
			},
		},
	}); err != nil {
		return fmt.Errorf("create Deployment fixture: %w", err)
	}
	if err := env.Create(ctx, &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: testCase.id + "-backoff", Namespace: env.Namespace},
		InvolvedObject: corev1.ObjectReference{Kind: "Deployment", Name: testCase.id},
		Reason:         "BackOff",
		Message:        "Back-off restarting failed container",
		Type:           corev1.EventTypeWarning,
		LastTimestamp:  metav1.NewTime(now),
	}); err != nil {
		return fmt.Errorf("create BackOff Event fixture: %w", err)
	}
	providerName := testCase.id + "-provider"
	if err := env.Create(ctx, &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: providerName, Namespace: env.Namespace},
		Spec:       v1alpha1.ModelProviderSpec{Provider: "crashloopbackoff-runtime", Model: "deterministic-runtime-fixture", MaxTokens: 512},
	}); err != nil {
		return fmt.Errorf("create ModelProvider fixture: %w", err)
	}
	rule := crashLoopBackOffRiskRule(env.Namespace, testCase.id, labels, providerName)
	if err := env.Create(ctx, rule); err != nil {
		return fmt.Errorf("create RiskRule fixture: %w", err)
	}
	env.RegisterRiskRule(rule.Name)

	registry := datasource.NewRegistry(
		kubernetesdatasource.Adapter{Client: env.Client},
		lokidatasource.Adapter{BaseURL: lokiURL},
	)
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
		request := crashLoopBackOffInvestigationRequest(rule, providerName, testCase.id)
		if err := env.Create(ctx, request); err != nil {
			return fmt.Errorf("create InvestigationRequest fixture: %w", err)
		}
		requestName = request.Name
	}

	service := &investigation.Service{
		Client:   env.Client,
		Registry: registry,
		Resolver: modelgateway.KubeResolver{Client: env.Client},
		Gateway:  &modelgateway.Gateway{Base: knowledge.NewBase(), Providers: model.NewRegistry(crashLoopBackOffRuntimeProvider{applicationLog: testCase.applicationLog})},
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

func crashLoopBackOffRiskRule(namespace, appName string, labels map[string]string, providerName string) *v1alpha1.RiskRule {
	return &v1alpha1.RiskRule{
		ObjectMeta: metav1.ObjectMeta{Name: appName + "-rule", Namespace: namespace},
		Spec: v1alpha1.RiskRuleSpec{
			Window: metav1.Duration{Duration: 10 * time.Minute}, Severity: "high",
			TargetSelector: v1alpha1.TargetSelector{
				NamespaceSelector: v1alpha1.NamespaceSelector{MatchNames: []string{namespace}},
				WorkloadSelector:  v1alpha1.WorkloadSelector{MatchLabels: labels, Kinds: []string{"Deployment"}},
			},
			Signals: []v1alpha1.RiskRuleSignal{
				{Name: "crashloop-event", DatasourceRef: v1alpha1.LocalObjectReference{Name: "kubernetes-events"}, QueryType: "event", Reasons: []string{"BackOff"}, Threshold: v1alpha1.RiskThreshold{Operator: "count_gt", Value: 0}},
				{Name: "application-startup-failure", DatasourceRef: v1alpha1.LocalObjectReference{Name: "loki"}, QueryType: "log", Threshold: v1alpha1.RiskThreshold{Operator: "count_gt", Value: 0}},
			},
			AI: v1alpha1.RiskRuleAI{ProviderRef: v1alpha1.LocalObjectReference{Name: providerName}},
			InvestigationPolicy: v1alpha1.RiskRuleInvestigationPolicy{
				Mode: v1alpha1.RiskRuleInvestigationModeCreateRequest, CreateRiskSignal: true,
				EvidenceRequirements: v1alpha1.EvidenceRequirements{Profile: "CrashLoopBackOff"},
			},
		},
	}
}

func crashLoopBackOffInvestigationRequest(rule *v1alpha1.RiskRule, providerName, appName string) *v1alpha1.InvestigationRequest {
	return &v1alpha1.InvestigationRequest{
		ObjectMeta: metav1.ObjectMeta{Name: appName + "-manual-investigation", Namespace: rule.Namespace, Labels: map[string]string{runtimeRiskRuleLabelKey: rule.Name}},
		Spec: v1alpha1.InvestigationRequestSpec{
			Target:           v1alpha1.TargetRef{APIVersion: "apps/v1", Kind: "Deployment", Namespace: rule.Namespace, Name: appName},
			Queries:          []v1alpha1.InvestigationQuery{{Name: "crashloop-event", DatasourceRef: v1alpha1.LocalObjectReference{Name: "kubernetes-events"}, QueryType: "event", Reasons: []string{"BackOff"}}},
			ModelProviderRef: providerNameRef(providerName), Mode: v1alpha1.InvestigationModeReadOnly, CreateRiskSignal: true,
			EvidenceRequirements: v1alpha1.EvidenceRequirements{Profile: "CrashLoopBackOff"},
			TimeRange:            v1alpha1.InvestigationTimeRange{Lookback: metav1.Duration{Duration: 10 * time.Minute}},
		},
	}
}

func assertPublicCrashLoopBackOffReport(t *testing.T, artifactDir string, testCase crashLoopBackOffRuntimeCase, namespace string) {
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
	sources := map[string]bool{}
	reasons := map[string]bool{}
	for _, ref := range request.Status.EvidenceRefs {
		sources[ref.Source] = true
		reasons[ref.Reason] = true
	}
	for _, required := range testCase.expected.RequiredEvidenceSources {
		if !sources[required] {
			t.Fatalf("public report missing evidence source %q: %#v", required, request.Status.EvidenceRefs)
		}
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
	} else if len(report.RiskSignals) != 0 {
		t.Fatalf("public report expected no RiskSignal, got %d", len(report.RiskSignals))
	}
}
