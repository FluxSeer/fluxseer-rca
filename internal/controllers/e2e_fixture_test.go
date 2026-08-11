package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/datasource"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
	"github.com/FluxSeer/fluxseer-rca/internal/investigation"
	"github.com/FluxSeer/fluxseer-rca/internal/knowledge"
	"github.com/FluxSeer/fluxseer-rca/internal/model"
	"github.com/FluxSeer/fluxseer-rca/internal/modelgateway"
)

type rawToFinalFixture struct {
	Name             string                `json:"name"`
	Profile          string                `json:"profile"`
	Target           fixtureTarget         `json:"target"`
	Datasources      []fixtureDatasource   `json:"datasources"`
	ProviderResponse map[string]any        `json:"providerResponse"`
	ProviderUsage    fixtureProviderUsage  `json:"providerUsage,omitempty"`
	Expected         rawToFinalExpectation `json:"expected"`
	Baseline         *fixtureBaseline      `json:"baseline,omitempty"`
}

type fixtureProviderUsage struct {
	InputTokens  int64 `json:"inputTokens,omitempty"`
	OutputTokens int64 `json:"outputTokens,omitempty"`
}

type fixtureTarget struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
}

type fixtureDatasource struct {
	Name      string           `json:"name"`
	QueryType string           `json:"queryType"`
	Records   []map[string]any `json:"records"`
	Error     string           `json:"error,omitempty"`
}

type rawToFinalExpectation struct {
	Phase             string `json:"phase"`
	Outcome           string `json:"outcome"`
	RootCauseType     string `json:"rootCauseType"`
	ClaimVerification string `json:"claimVerification"`
	ProviderCalls     int    `json:"providerCalls"`
	FailureReason     string `json:"failureReason,omitempty"`
}

type fixtureBaseline struct {
	Scenario          string                       `json:"scenario"`
	ExpectedDiagnosis string                       `json:"expectedDiagnosis"`
	ExpectedEvidence  []fixtureEvidenceExpectation `json:"expectedEvidence"`
	HumanJudgment     string                       `json:"humanJudgment"`
	ExpectedIncident  bool                         `json:"expectedIncident,omitempty"`
}

type fixtureEvidenceExpectation struct {
	Kind   string `json:"kind"`
	Source string `json:"source"`
	Reason string `json:"reason,omitempty"`
}

type fixtureBaselineResult struct {
	Fixture                     string         `json:"fixture"`
	Scenario                    string         `json:"scenario"`
	ExpectedDiagnosis           string         `json:"expectedDiagnosis"`
	ExpectedPhase               string         `json:"expectedPhase"`
	ActualPhase                 string         `json:"actualPhase"`
	ExpectedOutcome             string         `json:"expectedOutcome"`
	ActualOutcome               string         `json:"actualOutcome"`
	ActualRootCauseType         string         `json:"actualRootCauseType,omitempty"`
	ExpectedFailureReason       string         `json:"expectedFailureReason,omitempty"`
	ActualFailureReason         string         `json:"actualFailureReason,omitempty"`
	FailureReasonCorrect        bool           `json:"failureReasonCorrect"`
	ExpectedIncident            bool           `json:"expectedIncident"`
	RootCauseTypeCorrect        bool           `json:"rootCauseTypeCorrect"`
	RootCauseEntityCorrect      bool           `json:"rootCauseEntityCorrect"`
	RootCauseEntityEvaluated    bool           `json:"rootCauseEntityEvaluated"`
	EvidencePrecision           float64        `json:"evidencePrecision"`
	EvidenceRecall              float64        `json:"evidenceRecall"`
	EvidenceEvaluationAvailable bool           `json:"evidenceEvaluationAvailable"`
	ClaimVerification           map[string]int `json:"claimVerification"`
	UnsupportedClaimRate        float64        `json:"unsupportedClaimRate"`
	UnsafeNoIssueFound          bool           `json:"unsafeNoIssueFound"`
	QueryCount                  int            `json:"queryCount"`
	ProviderRequestCount        int            `json:"providerRequestCount"`
	CheckpointReused            bool           `json:"checkpointReused"`
	DurationSeconds             int64          `json:"durationSeconds"`
	InputTokens                 int64          `json:"inputTokens"`
	OutputTokens                int64          `json:"outputTokens"`
	TokenUsageAvailable         bool           `json:"tokenUsageAvailable"`
	HumanJudgment               string         `json:"humanJudgment"`
	ExpectedEvidenceCount       int            `json:"expectedEvidenceCount"`
	MatchedExpectedEvidence     int            `json:"matchedExpectedEvidence"`
	CollectedEvidenceCount      int            `json:"collectedEvidenceCount"`
	MatchedCollectedEvidence    int            `json:"matchedCollectedEvidence"`
}

type fixtureBaselineReport struct {
	SchemaVersion                string                  `json:"schemaVersion"`
	Corpus                       string                  `json:"corpus"`
	Result                       string                  `json:"result"`
	ScenarioCount                int                     `json:"scenarioCount"`
	RootCauseTypeAccuracy        float64                 `json:"rootCauseTypeAccuracy"`
	RootCauseEntityAccuracy      float64                 `json:"rootCauseEntityAccuracy"`
	EvidencePrecision            float64                 `json:"evidencePrecision"`
	EvidenceRecall               float64                 `json:"evidenceRecall"`
	ClaimVerification            map[string]int          `json:"claimVerification"`
	UnsupportedClaimRate         float64                 `json:"unsupportedClaimRate"`
	ContradictedClaimRate        float64                 `json:"contradictedClaimRate"`
	UnsafeNoIssueFoundRate       float64                 `json:"unsafeNoIssueFoundRate"`
	FailureContractAccuracy      float64                 `json:"failureContractAccuracy"`
	CheckpointReuseRate          float64                 `json:"checkpointReuseRate"`
	TotalQueries                 int                     `json:"totalQueries"`
	TotalProviderRequests        int                     `json:"totalProviderRequests"`
	TokenUsageAvailableScenarios int                     `json:"tokenUsageAvailableScenarios"`
	Scenarios                    []fixtureBaselineResult `json:"scenarios"`
}

type fixtureRCAProvider struct {
	output map[string]any
	usage  fixtureProviderUsage
	calls  int
}

func (p *fixtureRCAProvider) Name() string {
	return "fixture"
}

func (p *fixtureRCAProvider) Complete(context.Context, domain.ModelRequest) (domain.ModelResponse, error) {
	p.calls++
	return domain.ModelResponse{
		Provider:     p.Name(),
		Model:        "fixture-model",
		Structured:   true,
		Output:       p.output,
		InputTokens:  p.usage.InputTokens,
		OutputTokens: p.usage.OutputTokens,
	}, nil
}

func TestRawToFinalE2EFixtureReplay(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "e2e", "*.json"))
	if err != nil {
		t.Fatalf("glob e2e fixtures: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected e2e fixtures")
	}
	baselineResults := make([]fixtureBaselineResult, 0)
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			var fixture rawToFinalFixture
			if err := json.Unmarshal(data, &fixture); err != nil {
				t.Fatalf("decode fixture: %v", err)
			}
			reconciler, request, provider, queryCounter := fixtureReconciler(t, fixture)
			if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
				NamespacedName: types.NamespacedName{Name: request.Name, Namespace: request.Namespace},
			}); err != nil {
				t.Fatalf("reconcile fixture: %v", err)
			}

			var stored v1alpha1.InvestigationRequest
			if err := reconciler.Get(context.Background(), types.NamespacedName{Name: request.Name, Namespace: request.Namespace}, &stored); err != nil {
				t.Fatalf("get stored request: %v", err)
			}
			if stored.Status.Phase != fixture.Expected.Phase || stored.Status.Outcome != fixture.Expected.Outcome {
				t.Fatalf("expected phase=%s outcome=%s, got phase=%s outcome=%s", fixture.Expected.Phase, fixture.Expected.Outcome, stored.Status.Phase, stored.Status.Outcome)
			}
			if fixture.Expected.FailureReason != "" {
				if stored.Status.Failure == nil || stored.Status.Failure.Code != fixture.Expected.FailureReason {
					t.Fatalf("expected failureReason=%s, got %#v", fixture.Expected.FailureReason, stored.Status.Failure)
				}
			} else if stored.Status.Verdict == nil || stored.Status.Verdict.RootCauseType != fixture.Expected.RootCauseType {
				t.Fatalf("expected rootCauseType=%s, got %#v", fixture.Expected.RootCauseType, stored.Status.Verdict)
			}
			if !hasClaimVerification(stored.Status.Claims, fixture.Expected.ClaimVerification) {
				t.Fatalf("expected claim verification %s, got %#v", fixture.Expected.ClaimVerification, stored.Status.Claims)
			}
			if provider.calls != fixture.Expected.ProviderCalls {
				t.Fatalf("expected provider to be called %d times, got %d", fixture.Expected.ProviderCalls, provider.calls)
			}

			providerCalls := provider.calls
			queryCalls := queryCounter.calls
			if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
				NamespacedName: types.NamespacedName{Name: request.Name, Namespace: request.Namespace},
			}); err != nil {
				t.Fatalf("reconcile completed fixture: %v", err)
			}
			checkpointReused := provider.calls == providerCalls && queryCounter.calls == queryCalls
			if !checkpointReused {
				t.Fatalf("expected terminal checkpoint reuse, provider calls %d->%d query calls %d->%d", providerCalls, provider.calls, queryCalls, queryCounter.calls)
			}

			if fixture.Baseline != nil {
				result := evaluateFixtureBaseline(fixture, stored, queryCalls, providerCalls, checkpointReused)
				if result.EvidenceEvaluationAvailable && result.MatchedExpectedEvidence != result.ExpectedEvidenceCount {
					t.Fatalf("expected all baseline evidence to be retained, got %d/%d", result.MatchedExpectedEvidence, result.ExpectedEvidenceCount)
				}
				if !result.EvidenceEvaluationAvailable && result.CollectedEvidenceCount != 0 {
					t.Fatalf("expected no retained evidence for non-evidence baseline, got %d", result.CollectedEvidenceCount)
				}
				if !result.RootCauseTypeCorrect || !result.RootCauseEntityCorrect || !result.FailureReasonCorrect || result.UnsafeNoIssueFound {
					t.Fatalf("baseline diagnosis mismatch: %#v", result)
				}
				baselineResults = append(baselineResults, result)
			}
		})
	}
	if len(baselineResults) != 12 {
		t.Fatalf("expected twelve baseline fixtures, got %d", len(baselineResults))
	}
	report := aggregateFixtureBaseline(baselineResults)
	if reportPath := os.Getenv("FLUXSEER_RCA_EVALUATION_REPORT"); reportPath != "" {
		writeFixtureBaselineReport(t, reportPath, report)
	}
	t.Logf("RCA quality baseline: scenarios=%d rootCauseTypeAccuracy=%.2f evidencePrecision=%.2f evidenceRecall=%.2f unsupportedClaimRate=%.2f", report.ScenarioCount, report.RootCauseTypeAccuracy, report.EvidencePrecision, report.EvidenceRecall, report.UnsupportedClaimRate)
}

func fixtureReconciler(t *testing.T, fixture rawToFinalFixture) (*InvestigationRequestReconciler, *v1alpha1.InvestigationRequest, *fixtureRCAProvider, *fixtureQueryCounter) {
	t.Helper()
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
	target := fixtureTargetOrDefault(fixture.Target)
	request := &v1alpha1.InvestigationRequest{
		ObjectMeta: metav1.ObjectMeta{Name: fixture.Name, Namespace: "fluxseer-rca-system", Generation: 1},
		Spec: v1alpha1.InvestigationRequestSpec{
			Target: v1alpha1.TargetRef{
				Namespace:  target.Namespace,
				Kind:       target.Kind,
				Name:       target.Name,
				APIVersion: target.APIVersion,
			},
			DataSources:          fixtureDatasourceRefs(fixture.Datasources),
			ModelProviderRef:     v1alpha1.LocalObjectReference{Name: "fixture-provider"},
			Mode:                 v1alpha1.InvestigationModeReadOnly,
			EvidenceRequirements: v1alpha1.EvidenceRequirements{Profile: fixture.Profile},
		},
	}
	providerObj := &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "fixture-provider", Namespace: "fluxseer-rca-system"},
		Spec:       v1alpha1.ModelProviderSpec{Provider: "fixture", Model: "fixture-model"},
	}
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.InvestigationRequest{}, &v1alpha1.RiskSignal{}).
		WithObjects(append(fixtureTargetObjects(target), request, providerObj)...).
		Build()
	provider := &fixtureRCAProvider{output: fixture.ProviderResponse, usage: fixture.ProviderUsage}
	queryCounter := &fixtureQueryCounter{}
	return &InvestigationRequestReconciler{
		Client: client,
		Scheme: scheme,
		Service: &investigation.Service{
			Client: client,
			Registry: datasource.NewRegistry(
				fixtureDataSources(fixture.Datasources, queryCounter)...,
			),
			Resolver: modelgateway.KubeResolver{Client: client},
			Gateway: &modelgateway.Gateway{
				Base:      knowledge.NewBase(),
				Providers: model.NewRegistry(provider),
			},
		},
		Now: func() time.Time { return time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC) },
	}, request, provider, queryCounter
}

func fixtureTargetOrDefault(target fixtureTarget) fixtureTarget {
	if target.Kind == "" {
		return fixtureTarget{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "prod", Name: "open-api"}
	}
	return target
}

func fixtureTargetObjects(target fixtureTarget) []client.Object {
	switch target.Kind {
	case "StatefulSet":
		return []client.Object{
			&appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{Name: target.Name, Namespace: target.Namespace, Labels: map[string]string{"app": target.Name}},
				Spec: appsv1.StatefulSetSpec{
					Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": target.Name}}},
				},
			},
		}
	case "DaemonSet":
		return []client.Object{
			&appsv1.DaemonSet{
				ObjectMeta: metav1.ObjectMeta{Name: target.Name, Namespace: target.Namespace, Labels: map[string]string{"app": target.Name}},
				Spec: appsv1.DaemonSetSpec{
					Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": target.Name}}},
				},
			},
		}
	case "Job":
		return []client.Object{
			&batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: target.Name, Namespace: target.Namespace, Labels: map[string]string{"app": target.Name}},
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": target.Name}}},
				},
			},
		}
	case "CronJob":
		return []client.Object{
			&batchv1.CronJob{
				ObjectMeta: metav1.ObjectMeta{Name: target.Name, Namespace: target.Namespace, Labels: map[string]string{"app": target.Name}},
				Spec: batchv1.CronJobSpec{
					JobTemplate: batchv1.JobTemplateSpec{Spec: batchv1.JobSpec{
						Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": target.Name}}},
					}},
				},
			},
		}
	case "Pod":
		return []client.Object{
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: target.Name, Namespace: target.Namespace, Labels: map[string]string{"app": target.Name}},
			},
		}
	default:
		return []client.Object{
			&appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: target.Name, Namespace: target.Namespace, Labels: map[string]string{"app": target.Name}},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": target.Name}}},
				},
			},
		}
	}
}

func fixtureDatasourceRefs(items []fixtureDatasource) []v1alpha1.LocalObjectReference {
	refs := make([]v1alpha1.LocalObjectReference, 0, len(items))
	for _, item := range items {
		refs = append(refs, v1alpha1.LocalObjectReference{Name: item.Name})
	}
	return refs
}

type fixtureQueryCounter struct {
	calls int
}

type countingFixtureDataSource struct {
	fakeInvestigationDataSource
	counter    *fixtureQueryCounter
	queryError string
}

func (f countingFixtureDataSource) Query(ctx context.Context, request datasource.QueryRequest) (*datasource.QueryResult, error) {
	f.counter.calls++
	if f.queryError != "" {
		return nil, errors.New(f.queryError)
	}
	return f.fakeInvestigationDataSource.Query(ctx, request)
}

func fixtureDataSources(items []fixtureDatasource, counter *fixtureQueryCounter) []datasource.DataSource {
	sources := make([]datasource.DataSource, 0, len(items))
	for _, item := range items {
		sources = append(sources, countingFixtureDataSource{
			fakeInvestigationDataSource: fakeInvestigationDataSource{
				name:      item.Name,
				queryType: domain.QueryType(item.QueryType),
				records:   item.Records,
			},
			counter:    counter,
			queryError: item.Error,
		})
	}
	return sources
}

func hasClaimVerification(claims []v1alpha1.RCAClaim, verification string) bool {
	if verification == "" {
		return true
	}
	for _, claim := range claims {
		if claim.Verification == verification {
			return true
		}
	}
	return false
}

func evaluateFixtureBaseline(fixture rawToFinalFixture, request v1alpha1.InvestigationRequest, queryCalls, providerCalls int, checkpointReused bool) fixtureBaselineResult {
	actualRootCauseType := ""
	actualFailureReason := ""
	actualEntity := v1alpha1.TargetRef{}
	if request.Status.Verdict != nil {
		actualRootCauseType = request.Status.Verdict.RootCauseType
		actualEntity = request.Status.Verdict.RootCauseEntity
	}
	if request.Status.Failure != nil {
		actualFailureReason = request.Status.Failure.Code
	}
	target := fixtureTargetOrDefault(fixture.Target)
	rootCauseEntityEvaluated := fixture.Expected.Outcome == v1alpha1.InvestigationOutcomeConfirmed
	rootCauseEntityCorrect := true
	if rootCauseEntityEvaluated {
		rootCauseEntityCorrect = actualEntity.Kind == target.Kind && actualEntity.Namespace == target.Namespace && actualEntity.Name == target.Name
	}
	evidenceEvaluationAvailable := len(fixture.Baseline.ExpectedEvidence) > 0

	matchedExpected := 0
	for _, expected := range fixture.Baseline.ExpectedEvidence {
		if evidenceExpectationPresent(expected, request.Status.EvidenceRefs) {
			matchedExpected++
		}
	}
	matchedActual := 0
	for _, actual := range request.Status.EvidenceRefs {
		if evidenceRefExpected(actual, fixture.Baseline.ExpectedEvidence) {
			matchedActual++
		}
	}

	verification := map[string]int{}
	unsupported := 0
	for _, claim := range request.Status.Claims {
		status := claim.Verification
		if status == "" {
			status = "Unspecified"
		}
		verification[status]++
		if status == "Unsupported" {
			unsupported++
		}
	}

	durationSeconds := int64(0)
	inputTokens := int64(0)
	outputTokens := int64(0)
	if request.Status.Execution != nil {
		durationSeconds = request.Status.Execution.DurationSeconds
		inputTokens = request.Status.Execution.InputTokens
		outputTokens = request.Status.Execution.OutputTokens
	}

	return fixtureBaselineResult{
		Fixture:                     fixture.Name,
		Scenario:                    fixture.Baseline.Scenario,
		ExpectedDiagnosis:           fixture.Baseline.ExpectedDiagnosis,
		ExpectedPhase:               fixture.Expected.Phase,
		ActualPhase:                 request.Status.Phase,
		ExpectedOutcome:             fixture.Expected.Outcome,
		ActualOutcome:               request.Status.Outcome,
		ActualRootCauseType:         actualRootCauseType,
		ExpectedFailureReason:       fixture.Expected.FailureReason,
		ActualFailureReason:         actualFailureReason,
		FailureReasonCorrect:        actualFailureReason == fixture.Expected.FailureReason,
		ExpectedIncident:            fixture.Baseline.ExpectedIncident || fixture.Expected.RootCauseType != "",
		RootCauseTypeCorrect:        actualRootCauseType == fixture.Expected.RootCauseType,
		RootCauseEntityCorrect:      rootCauseEntityCorrect,
		RootCauseEntityEvaluated:    rootCauseEntityEvaluated,
		EvidencePrecision:           ratio(matchedActual, len(request.Status.EvidenceRefs)),
		EvidenceRecall:              ratio(matchedExpected, len(fixture.Baseline.ExpectedEvidence)),
		EvidenceEvaluationAvailable: evidenceEvaluationAvailable,
		ClaimVerification:           verification,
		UnsupportedClaimRate:        ratio(unsupported, len(request.Status.Claims)),
		UnsafeNoIssueFound:          request.Status.Outcome == v1alpha1.InvestigationOutcomeNoIssueFound && fixture.Expected.Outcome != v1alpha1.InvestigationOutcomeNoIssueFound,
		QueryCount:                  queryCalls,
		ProviderRequestCount:        providerCalls,
		CheckpointReused:            checkpointReused,
		DurationSeconds:             durationSeconds,
		InputTokens:                 inputTokens,
		OutputTokens:                outputTokens,
		TokenUsageAvailable:         inputTokens > 0 || outputTokens > 0,
		HumanJudgment:               fixture.Baseline.HumanJudgment,
		ExpectedEvidenceCount:       len(fixture.Baseline.ExpectedEvidence),
		MatchedExpectedEvidence:     matchedExpected,
		CollectedEvidenceCount:      len(request.Status.EvidenceRefs),
		MatchedCollectedEvidence:    matchedActual,
	}
}

func evidenceExpectationPresent(expected fixtureEvidenceExpectation, actual []v1alpha1.EvidenceRef) bool {
	for _, ref := range actual {
		if ref.Kind == expected.Kind && ref.Source == expected.Source && (expected.Reason == "" || ref.Reason == expected.Reason) {
			return true
		}
	}
	return false
}

func evidenceRefExpected(actual v1alpha1.EvidenceRef, expected []fixtureEvidenceExpectation) bool {
	for _, item := range expected {
		if actual.Kind == item.Kind && actual.Source == item.Source && (item.Reason == "" || actual.Reason == item.Reason) {
			return true
		}
	}
	return false
}

func aggregateFixtureBaseline(results []fixtureBaselineResult) fixtureBaselineReport {
	rootCauseTypeCorrect := 0
	rootCauseEntityCorrect := 0
	rootCauseEntityEvaluated := 0
	matchedEvidence := 0
	expectedEvidence := 0
	collectedEvidence := 0
	unsupportedClaims := 0
	contradictedClaims := 0
	totalClaims := 0
	unsafeNoIssueFound := 0
	incidentScenarios := 0
	checkpointReused := 0
	totalQueries := 0
	totalProviderRequests := 0
	tokenUsageAvailable := 0
	failureContracts := 0
	failureContractsCorrect := 0
	claimVerification := map[string]int{}
	for _, result := range results {
		if result.RootCauseTypeCorrect {
			rootCauseTypeCorrect++
		}
		if result.RootCauseEntityEvaluated {
			rootCauseEntityEvaluated++
			if result.RootCauseEntityCorrect {
				rootCauseEntityCorrect++
			}
		}
		matchedEvidence += result.MatchedCollectedEvidence
		expectedEvidence += result.ExpectedEvidenceCount
		collectedEvidence += result.CollectedEvidenceCount
		unsupportedClaims += result.ClaimVerification["Unsupported"]
		contradictedClaims += result.ClaimVerification["Contradicted"]
		for status, count := range result.ClaimVerification {
			totalClaims += count
			claimVerification[status] += count
		}
		if result.ExpectedFailureReason != "" {
			failureContracts++
			if result.FailureReasonCorrect {
				failureContractsCorrect++
			}
		}
		if result.ExpectedIncident {
			incidentScenarios++
			if result.UnsafeNoIssueFound {
				unsafeNoIssueFound++
			}
		}
		if result.CheckpointReused {
			checkpointReused++
		}
		totalQueries += result.QueryCount
		totalProviderRequests += result.ProviderRequestCount
		if result.TokenUsageAvailable {
			tokenUsageAvailable++
		}
	}
	return fixtureBaselineReport{
		SchemaVersion:                "fluxseer-rca-quality-baseline-v3",
		Corpus:                       "operations-twelve-v3",
		Result:                       "PASS",
		ScenarioCount:                len(results),
		RootCauseTypeAccuracy:        ratio(rootCauseTypeCorrect, len(results)),
		RootCauseEntityAccuracy:      ratio(rootCauseEntityCorrect, rootCauseEntityEvaluated),
		EvidencePrecision:            ratio(matchedEvidence, collectedEvidence),
		EvidenceRecall:               ratio(matchedEvidence, expectedEvidence),
		ClaimVerification:            claimVerification,
		UnsupportedClaimRate:         ratio(unsupportedClaims, totalClaims),
		ContradictedClaimRate:        ratio(contradictedClaims, totalClaims),
		UnsafeNoIssueFoundRate:       ratio(unsafeNoIssueFound, incidentScenarios),
		FailureContractAccuracy:      ratio(failureContractsCorrect, failureContracts),
		CheckpointReuseRate:          ratio(checkpointReused, len(results)),
		TotalQueries:                 totalQueries,
		TotalProviderRequests:        totalProviderRequests,
		TokenUsageAvailableScenarios: tokenUsageAvailable,
		Scenarios:                    results,
	}
}

func writeFixtureBaselineReport(t *testing.T, path string, report fixtureBaselineReport) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create evaluation report directory: %v", err)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("encode evaluation report: %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write evaluation report: %v", err)
	}
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}
