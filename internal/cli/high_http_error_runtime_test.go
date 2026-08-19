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
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/controllers"
	"github.com/FluxSeer/fluxseer-rca/internal/datasource"
	lokidatasource "github.com/FluxSeer/fluxseer-rca/internal/datasource/loki"
	prometheusdatasource "github.com/FluxSeer/fluxseer-rca/internal/datasource/prometheus"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
	"github.com/FluxSeer/fluxseer-rca/internal/investigation"
	"github.com/FluxSeer/fluxseer-rca/internal/knowledge"
	"github.com/FluxSeer/fluxseer-rca/internal/model"
	"github.com/FluxSeer/fluxseer-rca/internal/modelgateway"
	"github.com/FluxSeer/fluxseer-rca/internal/runtimeharness"
)

type highHTTPErrorRuntimeCase struct {
	id             string
	name           string
	causalEvidence bool
	expected       runtimeharness.ExpectedRuntimeResult
}

type highHTTPErrorRuntimeProvider struct{}

func (highHTTPErrorRuntimeProvider) Name() string { return "high-http-error-runtime" }

func (highHTTPErrorRuntimeProvider) Complete(context.Context, domain.ModelRequest) (domain.ModelResponse, error) {
	return domain.ModelResponse{
		Provider:   "high-http-error-runtime",
		Model:      "deterministic-runtime-fixture",
		Structured: true,
		Output: map[string]any{
			"riskTitle":       "Checkout HTTP error rate increased",
			"riskSummary":     "The checkout workload has a high HTTP 5xx error rate because the inventory dependency is unavailable.",
			"severity":        "high",
			"confidenceScore": 92,
			"rationale":       "Prometheus provides the HTTP 5xx symptom and Loki provides a linked inventory dependency failure.",
			"rcaHypothesis":   "The checkout workload returns HTTP 5xx responses because the inventory dependency is unavailable.",
			"rcaCauses": []string{
				"High HTTP 5xx error rate is caused by the inventory dependency being unavailable.",
			},
			"actionType": "notification.sendSlack",
		},
	}, nil
}

func TestHighHTTPErrorRuntimeQualification(t *testing.T) {
	now := time.Date(2026, 8, 18, 13, 0, 0, 0, time.UTC)
	namespace := "fluxseer-runtime-high-http"
	cases := []highHTTPErrorRuntimeCase{
		{
			id:             "high-http-error-with-causal-dependency",
			name:           "High HTTP error rate with linked inventory dependency evidence",
			causalEvidence: true,
			expected: runtimeharness.ExpectedRuntimeResult{
				InvestigationPhase:       v1alpha1.PhaseCompleted,
				InvestigationOutcome:     v1alpha1.InvestigationOutcomeConfirmed,
				RootCauseType:            "HighHTTPErrorRate",
				RootCauseEntity:          "v1/Service/" + namespace + "/inventory",
				RequiredEvidenceSources:  []string{"prometheus", "loki"},
				RiskSignalExpected:       true,
				RiskSignalPhase:          v1alpha1.PhaseConfirmed,
				FailureReason:            "",
				MaxUnexpectedSideEffects: 0,
			},
		},
		{
			id:             "high-http-error-without-causal-evidence",
			name:           "High HTTP error rate without causal dependency evidence",
			causalEvidence: false,
			expected: runtimeharness.ExpectedRuntimeResult{
				InvestigationPhase:       v1alpha1.PhaseCompleted,
				InvestigationOutcome:     v1alpha1.InvestigationOutcomeInconclusive,
				RootCauseType:            "",
				RootCauseEntity:          "",
				RequiredEvidenceSources:  []string{"prometheus"},
				RiskSignalExpected:       false,
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

			prometheusServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/query_range" {
					http.NotFound(w, r)
					return
				}
				_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"__name__":"http_5xx_error_rate","app":"checkout"},"values":[[1787058000,"0.25"]]}]}}`))
			}))
			defer prometheusServer.Close()

			lokiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/loki/api/v1/query_range" {
					http.NotFound(w, r)
					return
				}
				if !testCase.causalEvidence {
					_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[]}}`))
					return
				}
				_, _ = fmt.Fprintf(w, `{"status":"success","data":{"resultType":"streams","result":[{"stream":{"app":"checkout","dependency_kind":"Service","dependency_name":"inventory","dependency_namespace":%q,"dependency_api_version":"v1"},"values":[["1787058000000000000","upstream inventory unavailable: connection refused"]]}]}}`, namespace)
			}))
			defer lokiServer.Close()

			caseSpec := testCase
			scenario := runtimeharness.RuntimeScenario{
				ID:       caseSpec.id,
				Name:     caseSpec.name,
				Expected: caseSpec.expected,
				Public:   true,
				Setup: func(ctx context.Context, env *runtimeharness.Environment) error {
					return setupHighHTTPErrorRuntimeCase(ctx, env, caseSpec, now, prometheusServer.URL, lokiServer.URL)
				},
			}
			runner := &runtimeharness.Runner{
				Environment:  environment,
				SuiteID:      "v0.5-runtime-rca-matrix",
				SuiteName:    "v0.5 Runtime RCA Qualification",
				RunID:        "high-http-error-runtime-qualification",
				SourceCommit: "test",
			}
			report, err := runner.Run(context.Background(), []runtimeharness.RuntimeScenario{scenario})
			if err != nil {
				t.Fatalf("run runtime harness: %v", err)
			}
			if report.Summary.Result != "PASS" || report.Summary.Passed != 1 {
				t.Fatalf("runtime qualification failed: %#v", report.Scenarios)
			}
			assertPublicHighHTTPErrorReport(t, artifactDir, caseSpec, namespace)
		})
	}
}

func setupHighHTTPErrorRuntimeCase(ctx context.Context, env *runtimeharness.Environment, testCase highHTTPErrorRuntimeCase, now time.Time, prometheusURL, lokiURL string) error {
	targetName := "checkout"
	providerName := testCase.id + "-provider"
	if err := env.Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: targetName, Namespace: env.Namespace, Labels: map[string]string{"app": targetName}},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": targetName}},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": targetName}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "checkout", Image: "example.invalid/checkout:qualification"}}}},
		},
	}); err != nil {
		return fmt.Errorf("create Deployment fixture: %w", err)
	}
	if err := env.Create(ctx, &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: providerName, Namespace: env.Namespace},
		Spec:       v1alpha1.ModelProviderSpec{Provider: "high-http-error-runtime", Model: "deterministic-runtime-fixture", MaxTokens: 512},
	}); err != nil {
		return fmt.Errorf("create ModelProvider fixture: %w", err)
	}
	rule := &v1alpha1.RiskRule{
		ObjectMeta: metav1.ObjectMeta{Name: testCase.id + "-rule", Namespace: env.Namespace},
		Spec: v1alpha1.RiskRuleSpec{
			Window:   metav1.Duration{Duration: 10 * time.Minute},
			Severity: "high",
			AI:       v1alpha1.RiskRuleAI{ProviderRef: v1alpha1.LocalObjectReference{Name: providerName}},
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

	queries := []v1alpha1.InvestigationQuery{
		{Name: "http-5xx", DatasourceRef: v1alpha1.LocalObjectReference{Name: "prometheus"}, QueryType: "metric", Query: "http_5xx_error_rate"},
	}
	if testCase.causalEvidence {
		queries = append(queries, v1alpha1.InvestigationQuery{Name: "dependency-errors", DatasourceRef: v1alpha1.LocalObjectReference{Name: "loki"}, QueryType: "log", Query: `{app="checkout"} |= "unavailable"`})
	}
	request := &v1alpha1.InvestigationRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      investigationRequestName(rule.Name, targetName),
			Namespace: env.Namespace,
			Labels:    map[string]string{runtimeRiskRuleLabelKey: rule.Name},
		},
		Spec: v1alpha1.InvestigationRequestSpec{
			Target:               v1alpha1.TargetRef{APIVersion: "apps/v1", Kind: "Deployment", Namespace: env.Namespace, Name: targetName},
			Queries:              queries,
			ModelProviderRef:     v1alpha1.LocalObjectReference{Name: providerName},
			Mode:                 v1alpha1.InvestigationModeReadOnly,
			CreateRiskSignal:     true,
			EvidenceRequirements: v1alpha1.EvidenceRequirements{Profile: "HighHTTPErrorRate"},
			TimeRange:            v1alpha1.InvestigationTimeRange{Lookback: metav1.Duration{Duration: 10 * time.Minute}},
		},
	}
	if err := env.Create(ctx, request); err != nil {
		return fmt.Errorf("create InvestigationRequest fixture: %w", err)
	}

	registry := datasource.NewRegistry(
		prometheusdatasource.Adapter{BaseURL: prometheusURL},
		lokidatasource.Adapter{BaseURL: lokiURL},
	)
	service := &investigation.Service{
		Client:   env.Client,
		Registry: registry,
		Resolver: modelgateway.KubeResolver{Client: env.Client},
		Gateway: &modelgateway.Gateway{
			Base:      knowledge.NewBase(),
			Providers: model.NewRegistry(highHTTPErrorRuntimeProvider{}),
		},
	}
	if _, err := (&controllers.InvestigationRequestReconciler{
		Client:  env.Client,
		Scheme:  env.Scheme,
		Service: service,
		Now:     func() time.Time { return now },
	}).Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: request.Name, Namespace: env.Namespace}}); err != nil {
		return fmt.Errorf("reconcile InvestigationRequest fixture: %w", err)
	}
	return nil
}

func assertPublicHighHTTPErrorReport(t *testing.T, artifactDir string, testCase highHTTPErrorRuntimeCase, namespace string) {
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
	if request.Spec.Target.Name != "checkout" {
		t.Fatalf("public report lost investigation target: %#v", request.Spec.Target)
	}
	if request.Status.Verdict == nil || request.Status.Verdict.RootCauseType != testCase.expected.RootCauseType || runtimeharness.TargetIdentity(request.Status.Verdict.RootCauseEntity) != testCase.expected.RootCauseEntity {
		if testCase.expected.RootCauseType == "" && request.Status.Verdict != nil && request.Status.Verdict.RootCauseType == "" && runtimeharness.TargetIdentity(request.Status.Verdict.RootCauseEntity) == "" {
			// Expected negative contract.
		} else {
			t.Fatalf("public report root cause mismatch: %#v", request.Status.Verdict)
		}
	}
	if request.Status.Failure != nil && request.Status.Failure.Code != testCase.expected.FailureReason {
		t.Fatalf("public report failure reason mismatch: %#v", request.Status.Failure)
	}
	sources := map[string]bool{}
	for _, ref := range request.Status.EvidenceRefs {
		sources[ref.Source] = true
	}
	for _, required := range testCase.expected.RequiredEvidenceSources {
		if !sources[required] {
			t.Fatalf("public report missing evidence source %q: %#v", required, request.Status.EvidenceRefs)
		}
	}
	if testCase.causalEvidence {
		var causalRefID string
		for _, ref := range request.Status.EvidenceRefs {
			if ref.Source == "loki" && len(ref.RelatedTargets) == 1 {
				causalRefID = ref.ID
				if runtimeharness.TargetIdentity(ref.RelatedTargets[0]) != testCase.expected.RootCauseEntity {
					t.Fatalf("public report causal dependency mismatch: %#v", ref.RelatedTargets)
				}
			}
		}
		if causalRefID == "" {
			t.Fatal("public report missing explicit Loki causal dependency ref")
		}
		linked := false
		for _, claim := range request.Status.Claims {
			if claim.Verification != "Supported" {
				continue
			}
			for _, evidenceLink := range claim.EvidenceLinks {
				if evidenceLink.EvidenceRef == causalRefID && evidenceLink.Role == "Supports" {
					linked = true
				}
			}
		}
		if !linked {
			t.Fatalf("public report causal Loki evidence is not linked from a supported claim: %#v", request.Status.Claims)
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
