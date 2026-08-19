package runtimeharness

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
)

func TestRunnerOrchestratesRuntimeSnapshotReportsAndCleanup(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add FluxSeer scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	artifactDir := t.TempDir()
	environment := NewEnvironment(fake.NewClientBuilder().WithScheme(scheme).Build(), scheme, "runtime-harness-test", artifactDir)
	environment.Timeout = time.Second
	environment.PollInterval = time.Millisecond
	environment.PublicReport = func(_ context.Context, env *Environment, ruleName string) ([]byte, error) {
		return json.Marshal(map[string]any{
			"schemaVersion":         PublicReportSchemaVersion,
			"selection":             map[string]string{"namespace": env.Namespace, "riskRule": ruleName},
			"riskRule":              map[string]any{"apiVersion": "aiops.platform/v1alpha1", "kind": "RiskRule", "metadata": map[string]string{"name": ruleName, "namespace": env.Namespace}},
			"investigationRequests": []any{},
			"riskSignals":           []any{},
		})
	}

	scenario := RuntimeScenario{
		ID:     "generic-runtime-case",
		Name:   "generic runtime case",
		Public: true,
		Expected: ExpectedRuntimeResult{
			InvestigationPhase:       v1alpha1.PhaseCompleted,
			InvestigationOutcome:     v1alpha1.InvestigationOutcomeConfirmed,
			RootCauseType:            "ExampleCause",
			RootCauseEntity:          "apps/v1/Deployment/runtime-harness-test/example",
			RequiredEvidenceSources:  []string{"kubernetes-events"},
			RequiredReasons:          []string{"ExampleEvidence"},
			RiskSignalExpected:       true,
			RiskSignalPhase:          v1alpha1.PhaseConfirmed,
			MaxUnexpectedSideEffects: 0,
		},
		Setup: func(ctx context.Context, env *Environment) error {
			rule := &v1alpha1.RiskRule{ObjectMeta: metav1.ObjectMeta{Name: "example-rule", Namespace: env.Namespace}}
			if err := env.Create(ctx, rule); err != nil {
				return err
			}
			request := &v1alpha1.InvestigationRequest{
				ObjectMeta: metav1.ObjectMeta{Name: "example-request", Namespace: env.Namespace, Labels: map[string]string{riskRuleLabelKey: rule.Name}},
				Status: v1alpha1.InvestigationRequestStatus{
					ResourceStatus: v1alpha1.ResourceStatus{Phase: v1alpha1.PhaseCompleted},
					Outcome:        v1alpha1.InvestigationOutcomeConfirmed,
					Verdict:        &v1alpha1.RCAVerdict{RootCauseType: "ExampleCause", RootCauseEntity: v1alpha1.TargetRef{APIVersion: "apps/v1", Kind: "Deployment", Namespace: env.Namespace, Name: "example"}},
					EvidenceRefs:   []v1alpha1.EvidenceRef{{ID: "event-1", Kind: "event", Source: "kubernetes-events", Reason: "ExampleEvidence"}},
				},
			}
			if err := env.Create(ctx, request); err != nil {
				return err
			}
			signal := &v1alpha1.RiskSignal{
				ObjectMeta: metav1.ObjectMeta{Name: "example-signal", Namespace: env.Namespace, Labels: map[string]string{riskRuleLabelKey: rule.Name}},
				Status:     v1alpha1.RiskSignalStatus{ResourceStatus: v1alpha1.ResourceStatus{Phase: v1alpha1.PhaseConfirmed}},
			}
			return env.Create(ctx, signal)
		},
	}

	report, err := (&Runner{Environment: environment, RunID: "runtime-harness-test-run", SourceCommit: "test"}).Run(context.Background(), []RuntimeScenario{scenario})
	if err != nil {
		t.Fatalf("run harness: %v", err)
	}
	if report.Summary.Result != "PASS" || report.Summary.Passed != 1 || len(report.Scenarios) != 1 {
		t.Fatalf("expected passing harness report, got %#v", report)
	}
	if _, err := os.Stat(filepath.Join(artifactDir, "internal", "summary.json")); err != nil {
		t.Fatalf("expected internal report artifact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(artifactDir, "internal", "scenarios", scenario.ID, "snapshot.json")); err != nil {
		t.Fatalf("expected snapshot artifact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(artifactDir, "user-facing", "scenarios", scenario.ID, "report.json")); err != nil {
		t.Fatalf("expected public report artifact: %v", err)
	}
	if err := environment.Client.Get(context.Background(), NewObjectKey(environment.Namespace, "example-rule"), &v1alpha1.RiskRule{}); err == nil {
		t.Fatal("expected cleanup to remove RiskRule")
	}
}

func TestEvaluateContractRejectsProductContractViolations(t *testing.T) {
	base := RuntimeScenario{
		ID:   "contract-case",
		Name: "contract case",
		Expected: ExpectedRuntimeResult{
			InvestigationPhase:      v1alpha1.PhaseCompleted,
			InvestigationOutcome:    v1alpha1.InvestigationOutcomeConfirmed,
			RootCauseType:           "ExampleCause",
			RootCauseEntity:         "apps/v1/Deployment/runtime/example",
			RequiredEvidenceSources: []string{"kubernetes-events"},
			RequiredReasons:         []string{"ExampleEvidence"},
			RiskSignalExpected:      true,
			RiskSignalPhase:         v1alpha1.PhaseConfirmed,
		},
	}
	baseSnapshot := RuntimeSnapshot{
		Namespace:    "runtime",
		RiskRuleName: "example-rule",
		InvestigationRequest: &v1alpha1.InvestigationRequest{Status: v1alpha1.InvestigationRequestStatus{
			ResourceStatus: v1alpha1.ResourceStatus{Phase: v1alpha1.PhaseCompleted},
			Outcome:        v1alpha1.InvestigationOutcomeConfirmed,
			Verdict:        &v1alpha1.RCAVerdict{RootCauseType: "ExampleCause", RootCauseEntity: v1alpha1.TargetRef{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "runtime", Name: "example"}},
			EvidenceRefs:   []v1alpha1.EvidenceRef{{Source: "kubernetes-events", Reason: "ExampleEvidence"}},
		}},
		RiskSignals: []v1alpha1.RiskSignal{{Status: v1alpha1.RiskSignalStatus{ResourceStatus: v1alpha1.ResourceStatus{Phase: v1alpha1.PhaseConfirmed}}}},
	}

	tests := []struct {
		name   string
		mutate func(*RuntimeScenario, *RuntimeSnapshot)
	}{
		{name: "wrong phase", mutate: func(_ *RuntimeScenario, snapshot *RuntimeSnapshot) {
			snapshot.InvestigationRequest.Status.Phase = v1alpha1.PhaseReasoning
		}},
		{name: "wrong outcome", mutate: func(_ *RuntimeScenario, snapshot *RuntimeSnapshot) {
			snapshot.InvestigationRequest.Status.Outcome = v1alpha1.InvestigationOutcomeInconclusive
		}},
		{name: "fabricated root cause", mutate: func(scenario *RuntimeScenario, snapshot *RuntimeSnapshot) {
			scenario.Expected.RootCauseType = ""
			scenario.Expected.RootCauseEntity = ""
		}},
		{name: "missing evidence", mutate: func(_ *RuntimeScenario, snapshot *RuntimeSnapshot) {
			snapshot.InvestigationRequest.Status.EvidenceRefs = nil
		}},
		{name: "unexpected AgentAction", mutate: func(_ *RuntimeScenario, snapshot *RuntimeSnapshot) { snapshot.UnexpectedSideEffects.AgentActions = 1 }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scenario := base
			snapshot := baseSnapshot
			scenario.Expected.RequiredEvidenceSources = append([]string(nil), base.Expected.RequiredEvidenceSources...)
			scenario.Expected.RequiredReasons = append([]string(nil), base.Expected.RequiredReasons...)
			test.mutate(&scenario, &snapshot)
			_, differences := evaluateContract(scenario, snapshot)
			if len(differences) == 0 {
				t.Fatal("expected contract violation to fail")
			}
		})
	}
}

func TestValidatePublicReportRejectsInternalReportMixing(t *testing.T) {
	snapshot := RuntimeSnapshot{Namespace: "runtime", RiskRuleName: "example-rule"}
	valid := map[string]any{
		"schemaVersion":         PublicReportSchemaVersion,
		"selection":             map[string]string{"namespace": "runtime", "riskRule": "example-rule"},
		"riskRule":              map[string]any{},
		"investigationRequests": []any{},
		"riskSignals":           []any{},
	}
	data, _ := json.Marshal(valid)
	snapshot.PublicReport = data
	if err := validatePublicReport(snapshot); err != nil {
		t.Fatalf("expected valid public report: %v", err)
	}

	valid["assertions"] = []any{}
	data, _ = json.Marshal(valid)
	snapshot.PublicReport = data
	if err := validatePublicReport(snapshot); err == nil {
		t.Fatal("expected internal assertion fields to be rejected from public report")
	}

	valid["schemaVersion"] = InternalReportSchemaVersion
	delete(valid, "assertions")
	data, _ = json.Marshal(valid)
	snapshot.PublicReport = data
	if err := validatePublicReport(snapshot); err == nil {
		t.Fatal("expected internal report schema to be rejected as public report")
	}
}

func TestRunnerPreservesRuntimeFailureWhenPublicReportGenerationFails(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add FluxSeer scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	environment := NewEnvironment(fake.NewClientBuilder().WithScheme(scheme).Build(), scheme, "runtime-harness-failure", "")
	environment.Timeout = time.Second
	environment.PollInterval = time.Millisecond
	environment.PublicReport = func(context.Context, *Environment, string) ([]byte, error) {
		return nil, os.ErrInvalid
	}
	scenario := RuntimeScenario{
		ID:     "runtime-and-report-failure",
		Name:   "runtime and report failure",
		Public: true,
		Expected: ExpectedRuntimeResult{
			InvestigationPhase:   v1alpha1.PhaseCompleted,
			InvestigationOutcome: v1alpha1.InvestigationOutcomeConfirmed,
			RiskSignalExpected:   false,
		},
		Setup: func(ctx context.Context, env *Environment) error {
			rule := &v1alpha1.RiskRule{ObjectMeta: metav1.ObjectMeta{Name: "failure-rule", Namespace: env.Namespace}}
			if err := env.Create(ctx, rule); err != nil {
				return err
			}
			return env.Create(ctx, &v1alpha1.InvestigationRequest{
				ObjectMeta: metav1.ObjectMeta{Name: "failure-request", Namespace: env.Namespace, Labels: map[string]string{riskRuleLabelKey: rule.Name}},
				Status: v1alpha1.InvestigationRequestStatus{
					ResourceStatus: v1alpha1.ResourceStatus{Phase: v1alpha1.PhaseCompleted},
					Outcome:        v1alpha1.InvestigationOutcomeInconclusive,
				},
			})
		},
	}

	report, err := (&Runner{Environment: environment, RunID: "runtime-and-report-failure"}).Run(context.Background(), []RuntimeScenario{scenario})
	if err != nil {
		t.Fatalf("run harness: %v", err)
	}
	result := report.Scenarios[0]
	if result.Result != "FAIL" {
		t.Fatalf("expected failed scenario, got %#v", result)
	}
	paths := map[string]bool{}
	for _, difference := range result.Differences {
		paths[difference.Path] = true
	}
	if !paths["investigation.outcome"] || !paths["publicReport"] {
		t.Fatalf("expected both runtime and report failures to be preserved, got %#v", result.Differences)
	}
}
