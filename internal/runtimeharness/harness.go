// Package runtimeharness provides the scenario-neutral runtime qualification
// primitives used by cluster conformance suites. Scenario packages should
// provide setup and expected values; they should not reimplement orchestration,
// report separation, or global safety assertions.
package runtimeharness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
)

const (
	InternalReportSchemaVersion = "fluxseer-test-report/v1"
	PublicReportSchemaVersion   = "fluxseer-riskrule-report/v1"
	SnapshotSchemaVersion       = "fluxseer-runtime-snapshot/v1"
	SuiteSchemaVersion          = "v0.5-runtime-rca-matrix/v1"

	riskRuleLabelKey = "fluxseer-rca.aiops.platform/risk-rule"
)

// PublicReportGenerator must call the supported product report path. It is
// injected so the harness does not duplicate the CLI's public report logic.
type PublicReportGenerator func(context.Context, *Environment, string) ([]byte, error)

type RuntimeScenario struct {
	ID       string
	Name     string
	Setup    func(context.Context, *Environment) error
	Expected ExpectedRuntimeResult
	Public   bool
}

type ExpectedRuntimeResult struct {
	InvestigationPhase   string
	InvestigationOutcome string

	RootCauseType   string
	RootCauseEntity string

	RequiredEvidenceSources []string
	RequiredReasons         []string

	RiskSignalExpected bool
	RiskSignalPhase    string
	FailureReason      string

	MaxUnexpectedSideEffects int
}

type Environment struct {
	Client       client.Client
	Scheme       *runtime.Scheme
	Namespace    string
	ArtifactDir  string
	Timeout      time.Duration
	PollInterval time.Duration
	Now          func() time.Time
	PublicReport PublicReportGenerator

	created          []client.Object
	namespaceCreated bool
	riskRuleName     string
	scenarioID       string
	baseline         SideEffectSnapshot
}

func NewEnvironment(kubeClient client.Client, scheme *runtime.Scheme, namespace, artifactDir string) *Environment {
	return &Environment{
		Client:       kubeClient,
		Scheme:       scheme,
		Namespace:    namespace,
		ArtifactDir:  artifactDir,
		Timeout:      90 * time.Second,
		PollInterval: 250 * time.Millisecond,
		Now:          time.Now,
	}
}

func (e *Environment) beginScenario(scenario RuntimeScenario) error {
	if e == nil || e.Client == nil {
		return errors.New("runtime harness environment requires a Kubernetes client")
	}
	if strings.TrimSpace(scenario.ID) == "" || strings.TrimSpace(scenario.Name) == "" {
		return errors.New("runtime scenario requires a non-empty ID and name")
	}
	if scenario.Setup == nil {
		return fmt.Errorf("runtime scenario %q requires setup", scenario.ID)
	}
	if scenario.Expected.RootCauseType != "" && scenario.Expected.RootCauseEntity == "" {
		return fmt.Errorf("runtime scenario %q requires an exact root cause entity when root cause type is set", scenario.ID)
	}
	if e.Namespace == "" {
		return fmt.Errorf("runtime scenario %q requires an environment namespace", scenario.ID)
	}
	e.created = nil
	e.namespaceCreated = false
	e.riskRuleName = ""
	e.scenarioID = scenario.ID
	baseline, err := e.captureSideEffects(context.Background())
	if err != nil {
		return fmt.Errorf("capture baseline side effects: %w", err)
	}
	e.baseline = baseline
	return nil
}

// EnsureNamespace creates the scenario namespace only when it did not exist.
// Cleanup removes it only when this call created it.
func (e *Environment) EnsureNamespace(ctx context.Context) error {
	var namespace corev1.Namespace
	key := client.ObjectKey{Name: e.Namespace}
	if err := e.Client.Get(ctx, key, &namespace); err == nil {
		return nil
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get namespace %q: %w", e.Namespace, err)
	}
	namespace = corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: e.Namespace}}
	if err := e.Client.Create(ctx, &namespace); err != nil {
		return fmt.Errorf("create namespace %q: %w", e.Namespace, err)
	}
	e.created = append(e.created, &namespace)
	e.namespaceCreated = true
	return nil
}

// Create records objects for deterministic cleanup. Registering a RiskRule
// also gives the generic runner the name used to locate generated requests.
func (e *Environment) Create(ctx context.Context, object client.Object) error {
	if object == nil {
		return errors.New("cannot create a nil runtime fixture")
	}
	if err := e.Client.Create(ctx, object); err != nil {
		return err
	}
	e.created = append(e.created, object)
	if rule, ok := object.(*v1alpha1.RiskRule); ok {
		e.riskRuleName = rule.Name
	}
	return nil
}

// RegisterRiskRule lets setup functions use a separately managed RiskRule
// while keeping orchestration independent of scenario-specific object names.
func (e *Environment) RegisterRiskRule(name string) {
	e.riskRuleName = strings.TrimSpace(name)
}

type SideEffectSnapshot struct {
	RemediationPlans map[string]string `json:"remediationPlans,omitempty"`
	AgentActions     map[string]string `json:"agentActions,omitempty"`
}

type SideEffectCounts struct {
	RemediationPlans int `json:"remediationPlans"`
	AgentActions     int `json:"agentActions"`
}

func (s SideEffectSnapshot) unexpectedSince(before SideEffectSnapshot) SideEffectCounts {
	return SideEffectCounts{
		RemediationPlans: newObjectsSince(before.RemediationPlans, s.RemediationPlans),
		AgentActions:     newObjectsSince(before.AgentActions, s.AgentActions),
	}
}

func newObjectsSince(before, after map[string]string) int {
	count := 0
	for key, uid := range after {
		if previous, ok := before[key]; !ok || previous != uid {
			count++
		}
	}
	return count
}

func (e *Environment) captureSideEffects(ctx context.Context) (SideEffectSnapshot, error) {
	var plans v1alpha1.RemediationPlanList
	if err := e.Client.List(ctx, &plans); err != nil {
		return SideEffectSnapshot{}, fmt.Errorf("list RemediationPlans: %w", err)
	}
	var actions v1alpha1.AgentActionList
	if err := e.Client.List(ctx, &actions); err != nil {
		return SideEffectSnapshot{}, fmt.Errorf("list AgentActions: %w", err)
	}
	snapshot := SideEffectSnapshot{RemediationPlans: map[string]string{}, AgentActions: map[string]string{}}
	for i := range plans.Items {
		snapshot.RemediationPlans[objectKey(&plans.Items[i])] = string(plans.Items[i].UID)
	}
	for i := range actions.Items {
		snapshot.AgentActions[objectKey(&actions.Items[i])] = string(actions.Items[i].UID)
	}
	return snapshot, nil
}

func objectKey(object client.Object) string {
	return object.GetNamespace() + "/" + object.GetName()
}

type RuntimeSnapshot struct {
	Namespace                 string                          `json:"namespace"`
	RiskRuleName              string                          `json:"riskRuleName"`
	RiskRule                  *v1alpha1.RiskRule              `json:"riskRule,omitempty"`
	InvestigationRequest      *v1alpha1.InvestigationRequest  `json:"investigationRequest,omitempty"`
	InvestigationRequests     []v1alpha1.InvestigationRequest `json:"investigationRequests"`
	RiskSignals               []v1alpha1.RiskSignal           `json:"riskSignals"`
	UnexpectedSideEffects     SideEffectCounts                `json:"unexpectedSideEffects"`
	PublicReport              json.RawMessage                 `json:"-"`
	PublicReportError         string                          `json:"publicReportError,omitempty"`
	RuntimeError              string                          `json:"runtimeError,omitempty"`
	InvestigationRequestCount int                             `json:"investigationRequestCount"`
}

func (e *Environment) waitForInvestigation(ctx context.Context) (*v1alpha1.InvestigationRequest, error) {
	if e.riskRuleName == "" {
		return nil, errors.New("scenario setup did not register a RiskRule")
	}
	timeout := e.Timeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	poll := e.PollInterval
	if poll <= 0 {
		poll = 250 * time.Millisecond
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		request, found, err := e.latestInvestigation(ctx)
		if err != nil {
			return nil, err
		}
		if found && isTerminalInvestigation(request.Status.Phase) {
			return request, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, fmt.Errorf("timed out waiting for InvestigationRequest for RiskRule %s/%s", e.Namespace, e.riskRuleName)
		case <-ticker.C:
		}
	}
}

func (e *Environment) latestInvestigation(ctx context.Context) (*v1alpha1.InvestigationRequest, bool, error) {
	var requests v1alpha1.InvestigationRequestList
	if err := e.Client.List(ctx, &requests, client.InNamespace(e.Namespace), client.MatchingLabels{riskRuleLabelKey: e.riskRuleName}); err != nil {
		return nil, false, fmt.Errorf("list InvestigationRequests for RiskRule %s/%s: %w", e.Namespace, e.riskRuleName, err)
	}
	if len(requests.Items) == 0 {
		return nil, false, nil
	}
	sort.Slice(requests.Items, func(i, j int) bool {
		if requests.Items[i].CreationTimestamp.Equal(&requests.Items[j].CreationTimestamp) {
			return requests.Items[i].Name > requests.Items[j].Name
		}
		return requests.Items[i].CreationTimestamp.After(requests.Items[j].CreationTimestamp.Time)
	})
	return &requests.Items[0], true, nil
}

func isTerminalInvestigation(phase string) bool {
	return phase == v1alpha1.PhaseCompleted || phase == v1alpha1.PhaseFailed
}

func (e *Environment) collect(ctx context.Context, request *v1alpha1.InvestigationRequest) (RuntimeSnapshot, error) {
	if request == nil {
		return RuntimeSnapshot{Namespace: e.Namespace, RiskRuleName: e.riskRuleName}, errors.New("cannot collect runtime snapshot without an InvestigationRequest")
	}
	var signals v1alpha1.RiskSignalList
	if err := e.Client.List(ctx, &signals); err != nil {
		return RuntimeSnapshot{}, fmt.Errorf("list RiskSignals: %w", err)
	}
	filteredSignals := make([]v1alpha1.RiskSignal, 0, len(signals.Items))
	for i := range signals.Items {
		if signals.Items[i].Labels[riskRuleLabelKey] == e.riskRuleName || linkedTo(request, &signals.Items[i]) {
			filteredSignals = append(filteredSignals, signals.Items[i])
		}
	}
	var requests v1alpha1.InvestigationRequestList
	if err := e.Client.List(ctx, &requests, client.InNamespace(e.Namespace), client.MatchingLabels{riskRuleLabelKey: e.riskRuleName}); err != nil {
		return RuntimeSnapshot{}, fmt.Errorf("list InvestigationRequests: %w", err)
	}
	var rule v1alpha1.RiskRule
	if err := e.Client.Get(ctx, client.ObjectKey{Namespace: e.Namespace, Name: e.riskRuleName}, &rule); err != nil {
		return RuntimeSnapshot{}, fmt.Errorf("get RiskRule %s/%s: %w", e.Namespace, e.riskRuleName, err)
	}
	after, err := e.captureSideEffects(ctx)
	if err != nil {
		return RuntimeSnapshot{}, err
	}
	return RuntimeSnapshot{
		Namespace:                 e.Namespace,
		RiskRuleName:              e.riskRuleName,
		RiskRule:                  &rule,
		InvestigationRequest:      request,
		InvestigationRequests:     requests.Items,
		RiskSignals:               filteredSignals,
		UnexpectedSideEffects:     after.unexpectedSince(e.baseline),
		InvestigationRequestCount: len(requests.Items),
	}, nil
}

func linkedTo(request *v1alpha1.InvestigationRequest, signal *v1alpha1.RiskSignal) bool {
	ref := request.Status.LinkedRiskSignalRef
	return ref != nil && ref.Namespace == signal.Namespace && ref.Name == signal.Name
}

func (e *Environment) collectWithoutRequest(ctx context.Context, runtimeErr error) (RuntimeSnapshot, error) {
	after, err := e.captureSideEffects(ctx)
	if err != nil {
		return RuntimeSnapshot{}, err
	}
	return RuntimeSnapshot{
		Namespace:             e.Namespace,
		RiskRuleName:          e.riskRuleName,
		InvestigationRequests: []v1alpha1.InvestigationRequest{},
		UnexpectedSideEffects: after.unexpectedSince(e.baseline),
		RuntimeError:          runtimeErr.Error(),
	}, nil
}

// Cleanup removes generated resources first, then setup-owned resources in
// reverse creation order. NotFound is intentionally ignored so cleanup is
// deterministic after a controller or TTL already removed an object.
func (e *Environment) Cleanup(ctx context.Context) error {
	var cleanupErrs []string
	deleteList := func(objects []client.Object) {
		for i := range objects {
			if err := e.Client.Delete(ctx, objects[i]); err != nil && !apierrors.IsNotFound(err) {
				cleanupErrs = append(cleanupErrs, fmt.Sprintf("%s %s: %v", objects[i].GetObjectKind().GroupVersionKind().Kind, objectKey(objects[i]), err))
			}
		}
	}

	if e.riskRuleName != "" {
		// Controller-created actions and plans may not inherit the RiskRule
		// label. Delete only objects absent from the pre-scenario baseline so
		// cleanup cannot remove unrelated user resources.
		var allActions v1alpha1.AgentActionList
		if err := e.Client.List(ctx, &allActions); err == nil {
			items := make([]client.Object, 0)
			for i := range allActions.Items {
				if _, existed := e.baseline.AgentActions[objectKey(&allActions.Items[i])]; !existed {
					items = append(items, &allActions.Items[i])
				}
			}
			deleteList(items)
		} else {
			cleanupErrs = append(cleanupErrs, fmt.Sprintf("list all AgentActions: %v", err))
		}
		var allPlans v1alpha1.RemediationPlanList
		if err := e.Client.List(ctx, &allPlans); err == nil {
			items := make([]client.Object, 0)
			for i := range allPlans.Items {
				if _, existed := e.baseline.RemediationPlans[objectKey(&allPlans.Items[i])]; !existed {
					items = append(items, &allPlans.Items[i])
				}
			}
			deleteList(items)
		} else {
			cleanupErrs = append(cleanupErrs, fmt.Sprintf("list all RemediationPlans: %v", err))
		}
		var actions v1alpha1.AgentActionList
		if err := e.Client.List(ctx, &actions, client.InNamespace(e.Namespace), client.MatchingLabels{riskRuleLabelKey: e.riskRuleName}); err == nil {
			items := make([]client.Object, len(actions.Items))
			for i := range actions.Items {
				items[i] = &actions.Items[i]
			}
			deleteList(items)
		} else {
			cleanupErrs = append(cleanupErrs, fmt.Sprintf("list AgentActions: %v", err))
		}
		var plans v1alpha1.RemediationPlanList
		if err := e.Client.List(ctx, &plans, client.InNamespace(e.Namespace), client.MatchingLabels{riskRuleLabelKey: e.riskRuleName}); err == nil {
			items := make([]client.Object, len(plans.Items))
			for i := range plans.Items {
				items[i] = &plans.Items[i]
			}
			deleteList(items)
		} else {
			cleanupErrs = append(cleanupErrs, fmt.Sprintf("list RemediationPlans: %v", err))
		}
		var signals v1alpha1.RiskSignalList
		if err := e.Client.List(ctx, &signals); err == nil {
			items := make([]client.Object, 0, len(signals.Items))
			for i := range signals.Items {
				if signals.Items[i].Labels[riskRuleLabelKey] == e.riskRuleName {
					items = append(items, &signals.Items[i])
				}
			}
			deleteList(items)
		} else {
			cleanupErrs = append(cleanupErrs, fmt.Sprintf("list RiskSignals: %v", err))
		}
		var requests v1alpha1.InvestigationRequestList
		if err := e.Client.List(ctx, &requests, client.InNamespace(e.Namespace), client.MatchingLabels{riskRuleLabelKey: e.riskRuleName}); err == nil {
			items := make([]client.Object, len(requests.Items))
			for i := range requests.Items {
				items[i] = &requests.Items[i]
			}
			deleteList(items)
		} else {
			cleanupErrs = append(cleanupErrs, fmt.Sprintf("list InvestigationRequests: %v", err))
		}
	}

	for i := len(e.created) - 1; i >= 0; i-- {
		deleteList([]client.Object{e.created[i]})
	}
	e.created = nil
	if len(cleanupErrs) > 0 {
		return errors.New(strings.Join(cleanupErrs, "; "))
	}
	return nil
}

type Assertion struct {
	ID       string `json:"id"`
	Result   string `json:"result"`
	Expected any    `json:"expected"`
	Actual   any    `json:"actual"`
}

type Difference struct {
	Path     string `json:"path"`
	Expected any    `json:"expected"`
	Actual   any    `json:"actual"`
}

type ScenarioResult struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Result      string       `json:"result"`
	Expected    any          `json:"expected"`
	Actual      any          `json:"actual"`
	Assertions  []Assertion  `json:"assertions"`
	Differences []Difference `json:"differences"`
	Artifacts   []string     `json:"artifacts"`
}

type InternalReport struct {
	SchemaVersion      string           `json:"schemaVersion"`
	SuiteSchemaVersion string           `json:"suiteSchemaVersion"`
	Suite              ReportSuite      `json:"suite"`
	Run                ReportRun        `json:"run"`
	Summary            ReportSummary    `json:"summary"`
	Scenarios          []ScenarioResult `json:"scenarios"`
}

type ReportSuite struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Tier string `json:"tier"`
}

type ReportRun struct {
	ID           string         `json:"id"`
	SourceCommit string         `json:"sourceCommit"`
	SourceDirty  bool           `json:"sourceDirty"`
	Environment  map[string]any `json:"environment"`
}

type ReportSummary struct {
	Result string `json:"result"`
	Total  int    `json:"total"`
	Passed int    `json:"passed"`
	Failed int    `json:"failed"`
}

type Runner struct {
	Environment  *Environment
	SuiteID      string
	SuiteName    string
	RunID        string
	SourceCommit string
	SourceDirty  bool
}

func (r *Runner) Run(ctx context.Context, scenarios []RuntimeScenario) (InternalReport, error) {
	if r == nil || r.Environment == nil {
		return InternalReport{}, errors.New("runtime harness runner requires an environment")
	}
	if r.SuiteID == "" {
		r.SuiteID = "runtime-rca-conformance"
	}
	if r.SuiteName == "" {
		r.SuiteName = "Runtime RCA Conformance"
	}
	report := InternalReport{
		SchemaVersion:      InternalReportSchemaVersion,
		SuiteSchemaVersion: SuiteSchemaVersion,
		Suite:              ReportSuite{ID: r.SuiteID, Name: r.SuiteName, Tier: "cluster"},
		Run:                ReportRun{ID: r.RunID, SourceCommit: r.SourceCommit, SourceDirty: r.SourceDirty, Environment: map[string]any{"namespace": r.Environment.Namespace}},
		Scenarios:          []ScenarioResult{},
	}
	if report.Run.ID == "" {
		report.Run.ID = r.Environment.Now().UTC().Format("20060102T150405Z")
	}
	if report.Run.SourceCommit == "" {
		report.Run.SourceCommit = "unknown"
	}
	for _, scenario := range scenarios {
		result := r.runScenario(ctx, scenario)
		report.Scenarios = append(report.Scenarios, result)
	}
	report.Summary = summarize(report.Scenarios)
	if r.Environment.ArtifactDir != "" {
		if err := writeInternalReport(r.Environment.ArtifactDir, report); err != nil {
			return report, err
		}
	}
	return report, nil
}

func (r *Runner) runScenario(ctx context.Context, scenario RuntimeScenario) (result ScenarioResult) {
	result = ScenarioResult{ID: scenario.ID, Name: scenario.Name, Result: "FAIL", Expected: expectedMap(scenario.Expected), Actual: map[string]any{}}
	if err := r.Environment.beginScenario(scenario); err != nil {
		result.Differences = append(result.Differences, Difference{Path: "scenario.setup", Expected: "runnable", Actual: err.Error()})
		result.Assertions = append(result.Assertions, failedAssertion("scenario.setup", "runnable", err.Error()))
		return result
	}
	defer func() {
		if err := r.Environment.Cleanup(context.Background()); err != nil {
			result.Differences = append(result.Differences, Difference{Path: "cleanup", Expected: "no cleanup error", Actual: err.Error()})
			result.Assertions = append(result.Assertions, failedAssertion("cleanup", "no cleanup error", err.Error()))
			result.Result = "FAIL"
		}
	}()
	if err := r.Environment.EnsureNamespace(ctx); err != nil {
		result.Differences = append(result.Differences, Difference{Path: "orchestration.namespace", Expected: "created or already exists", Actual: err.Error()})
		result.Assertions = append(result.Assertions, failedAssertion("orchestration.namespace", "created or already exists", err.Error()))
		return result
	}
	if err := scenario.Setup(ctx, r.Environment); err != nil {
		result.Differences = append(result.Differences, Difference{Path: "orchestration.setup", Expected: "setup succeeds", Actual: err.Error()})
		result.Assertions = append(result.Assertions, failedAssertion("orchestration.setup", "setup succeeds", err.Error()))
		return result
	}
	request, waitErr := r.Environment.waitForInvestigation(ctx)
	var snapshot RuntimeSnapshot
	if waitErr != nil {
		var collectErr error
		snapshot, collectErr = r.Environment.collectWithoutRequest(ctx, waitErr)
		if collectErr != nil {
			snapshot = RuntimeSnapshot{Namespace: r.Environment.Namespace, RiskRuleName: r.Environment.riskRuleName, RuntimeError: fmt.Sprintf("%v; collect runtime state: %v", waitErr, collectErr)}
		}
	} else {
		var collectErr error
		snapshot, collectErr = r.Environment.collect(ctx, request)
		if collectErr != nil {
			snapshot = RuntimeSnapshot{
				Namespace:             r.Environment.Namespace,
				RiskRuleName:          r.Environment.riskRuleName,
				InvestigationRequest:  request,
				InvestigationRequests: []v1alpha1.InvestigationRequest{*request},
				RuntimeError:          collectErr.Error(),
			}
		}
	}
	result.Actual = actualMap(snapshot)
	result.Assertions, result.Differences = evaluateContract(scenario, snapshot)
	if scenario.Public {
		if r.Environment.PublicReport == nil {
			result.Assertions = append(result.Assertions, failedAssertion("public-report.generator", "configured", "not configured"))
			result.Differences = append(result.Differences, Difference{Path: "publicReport", Expected: "configured generator", Actual: "not configured"})
		} else {
			publicReport, err := r.Environment.PublicReport(ctx, r.Environment, r.Environment.riskRuleName)
			if err != nil {
				snapshot.PublicReportError = err.Error()
				result.Assertions = append(result.Assertions, failedAssertion("public-report.generator", "generated", err.Error()))
				result.Differences = append(result.Differences, Difference{Path: "publicReport", Expected: "generated", Actual: err.Error()})
			} else {
				snapshot.PublicReport = append(json.RawMessage(nil), publicReport...)
				if err := validatePublicReport(snapshot); err != nil {
					result.Assertions = append(result.Assertions, failedAssertion("public-report.contract", "valid public report", err.Error()))
					result.Differences = append(result.Differences, Difference{Path: "publicReport", Expected: PublicReportSchemaVersion, Actual: err.Error()})
				} else {
					result.Assertions = append(result.Assertions, passedAssertion("public-report.contract", PublicReportSchemaVersion, PublicReportSchemaVersion))
				}
			}
		}
	} else if len(snapshot.PublicReport) > 0 {
		result.Assertions = append(result.Assertions, failedAssertion("public-report.unexpected", "absent", "present"))
		result.Differences = append(result.Differences, Difference{Path: "publicReport", Expected: "absent", Actual: "present"})
	}
	result.Actual = actualMap(snapshot)
	if r.Environment.ArtifactDir != "" {
		if err := writeScenarioArtifacts(r.Environment.ArtifactDir, scenario, snapshot); err != nil {
			result.Assertions = append(result.Assertions, failedAssertion("artifacts.write", "written", err.Error()))
			result.Differences = append(result.Differences, Difference{Path: "artifacts", Expected: "written", Actual: err.Error()})
		} else {
			result.Artifacts = scenarioArtifactPaths(r.Environment.ArtifactDir, scenario.ID, scenario.Public)
		}
	}
	if len(result.Differences) == 0 {
		result.Result = "PASS"
	}
	return result
}

func expectedMap(expected ExpectedRuntimeResult) map[string]any {
	return map[string]any{
		"investigation": map[string]any{"phase": expected.InvestigationPhase, "outcome": expected.InvestigationOutcome, "failureReason": expected.FailureReason},
		"rootCause":     map[string]any{"type": expected.RootCauseType, "entity": expected.RootCauseEntity},
		"evidence":      map[string]any{"sources": expected.RequiredEvidenceSources, "reasons": expected.RequiredReasons},
		"riskSignal":    map[string]any{"expected": expected.RiskSignalExpected, "phase": expected.RiskSignalPhase},
		"sideEffects":   map[string]any{"maxUnexpected": expected.MaxUnexpectedSideEffects},
	}
}

func actualMap(snapshot RuntimeSnapshot) map[string]any {
	actual := map[string]any{
		"riskSignals": map[string]any{"count": len(snapshot.RiskSignals), "phases": riskSignalPhases(snapshot.RiskSignals)},
		"sideEffects": snapshot.UnexpectedSideEffects,
	}
	if snapshot.InvestigationRequest != nil {
		request := snapshot.InvestigationRequest
		actual["investigation"] = map[string]any{"name": request.Name, "phase": request.Status.Phase, "outcome": request.Status.Outcome, "failureReason": failureReason(request)}
		actual["rootCause"] = rootCauseMap(request)
		actual["evidence"] = map[string]any{"sources": evidenceSources(request), "reasons": evidenceReasons(request)}
	} else {
		actual["investigation"] = nil
		actual["rootCause"] = nil
		actual["evidence"] = map[string]any{"sources": []string{}, "reasons": []string{}}
	}
	if snapshot.RuntimeError != "" {
		actual["runtimeError"] = snapshot.RuntimeError
	}
	if snapshot.PublicReportError != "" {
		actual["publicReportError"] = snapshot.PublicReportError
	}
	return actual
}

func evaluateContract(scenario RuntimeScenario, snapshot RuntimeSnapshot) ([]Assertion, []Difference) {
	expected := scenario.Expected
	var assertions []Assertion
	var differences []Difference
	assertEqual := func(id string, want, got any) {
		if fmt.Sprint(want) == fmt.Sprint(got) {
			assertions = append(assertions, passedAssertion(id, want, got))
			return
		}
		assertions = append(assertions, failedAssertion(id, want, got))
		differences = append(differences, Difference{Path: id, Expected: want, Actual: got})
	}

	request := snapshot.InvestigationRequest
	var phase, outcome, failure string
	if request != nil {
		phase, outcome, failure = request.Status.Phase, request.Status.Outcome, failureReason(request)
	}
	assertEqual("investigation.phase", expected.InvestigationPhase, phase)
	assertEqual("investigation.outcome", expected.InvestigationOutcome, outcome)
	assertEqual("investigation.failureReason", expected.FailureReason, failure)

	var rootType, rootEntity string
	if request != nil && request.Status.Verdict != nil {
		rootType = request.Status.Verdict.RootCauseType
		rootEntity = TargetIdentity(request.Status.Verdict.RootCauseEntity)
	}
	if expected.RootCauseType == "" {
		assertEqual("invariant.no-fabricated-root-cause.type", "", rootType)
		assertEqual("invariant.no-fabricated-root-cause.entity", "", rootEntity)
		causeCount := 0
		for _, signal := range snapshot.RiskSignals {
			causeCount += len(signal.Status.RCACauses)
		}
		assertEqual("invariant.no-fabricated-root-cause.riskSignalCauses", 0, causeCount)
	} else {
		assertEqual("rootCause.type", expected.RootCauseType, rootType)
		assertEqual("rootCause.entity", expected.RootCauseEntity, rootEntity)
	}

	sources := []string{}
	reasons := []string{}
	if request != nil {
		sources = evidenceSources(request)
		reasons = evidenceReasons(request)
	}
	assertSubset := func(id string, required, actual []string) {
		missing := missingValues(required, actual)
		if len(missing) == 0 {
			assertions = append(assertions, passedAssertion(id, required, actual))
			return
		}
		assertions = append(assertions, failedAssertion(id, required, actual))
		differences = append(differences, Difference{Path: id, Expected: required, Actual: map[string]any{"values": actual, "missing": missing}})
	}
	assertSubset("evidence.sources", expected.RequiredEvidenceSources, sources)
	assertSubset("evidence.reasons", expected.RequiredReasons, reasons)

	if expected.RiskSignalExpected {
		if len(snapshot.RiskSignals) == 0 {
			assertions = append(assertions, failedAssertion("riskSignal.expected", true, false))
			differences = append(differences, Difference{Path: "riskSignal", Expected: "present", Actual: "absent"})
		} else {
			assertions = append(assertions, passedAssertion("riskSignal.expected", true, true))
			if expected.RiskSignalPhase != "" {
				phases := riskSignalPhases(snapshot.RiskSignals)
				assertSubset("riskSignal.phases", []string{expected.RiskSignalPhase}, phases)
			}
			if request != nil && request.Status.LinkedRiskSignalRef != nil && !containsRiskSignal(snapshot.RiskSignals, *request.Status.LinkedRiskSignalRef) {
				assertions = append(assertions, failedAssertion("riskSignal.projection", "linked signal present", "linked signal absent"))
				differences = append(differences, Difference{Path: "riskSignal.projection", Expected: request.Status.LinkedRiskSignalRef, Actual: snapshot.RiskSignals})
			} else {
				assertions = append(assertions, passedAssertion("riskSignal.projection", "linked or direct public signal", "linked or direct public signal"))
			}
		}
	} else {
		assertEqual("riskSignal.expected", 0, len(snapshot.RiskSignals))
	}

	unexpected := snapshot.UnexpectedSideEffects.RemediationPlans + snapshot.UnexpectedSideEffects.AgentActions
	if unexpected <= expected.MaxUnexpectedSideEffects {
		assertions = append(assertions, passedAssertion("invariant.no-unauthorized-remediation-side-effect", expected.MaxUnexpectedSideEffects, unexpected))
	} else {
		assertions = append(assertions, failedAssertion("invariant.no-unauthorized-remediation-side-effect", expected.MaxUnexpectedSideEffects, unexpected))
		differences = append(differences, Difference{Path: "sideEffects.unexpected", Expected: expected.MaxUnexpectedSideEffects, Actual: unexpected})
	}

	if snapshot.RuntimeError == "" {
		assertions = append(assertions, passedAssertion("runtime.execution", "completed", "completed"))
	} else {
		assertions = append(assertions, failedAssertion("runtime.execution", "completed", snapshot.RuntimeError))
		differences = append(differences, Difference{Path: "runtime.execution", Expected: "completed", Actual: snapshot.RuntimeError})
	}
	return assertions, differences
}

func passedAssertion(id string, expected, actual any) Assertion {
	return Assertion{ID: id, Result: "PASS", Expected: expected, Actual: actual}
}

func failedAssertion(id string, expected, actual any) Assertion {
	return Assertion{ID: id, Result: "FAIL", Expected: expected, Actual: actual}
}

func evidenceSources(request *v1alpha1.InvestigationRequest) []string {
	set := map[string]struct{}{}
	for _, ref := range request.Status.EvidenceRefs {
		if ref.Source != "" {
			set[ref.Source] = struct{}{}
		}
	}
	return sortedSet(set)
}

func evidenceReasons(request *v1alpha1.InvestigationRequest) []string {
	set := map[string]struct{}{}
	for _, ref := range request.Status.EvidenceRefs {
		if ref.Reason != "" {
			set[ref.Reason] = struct{}{}
		}
	}
	for _, condition := range request.Status.Conditions {
		if condition.Reason != "" {
			set[condition.Reason] = struct{}{}
		}
	}
	if request.Status.Failure != nil && request.Status.Failure.Code != "" {
		set[request.Status.Failure.Code] = struct{}{}
	}
	if request.Status.Degradation != nil {
		for _, reason := range request.Status.Degradation.Reasons {
			if reason.Code != "" {
				set[reason.Code] = struct{}{}
			}
		}
	}
	return sortedSet(set)
}

func failureReason(request *v1alpha1.InvestigationRequest) string {
	if request.Status.Failure == nil {
		return ""
	}
	return request.Status.Failure.Code
}

func rootCauseMap(request *v1alpha1.InvestigationRequest) map[string]string {
	if request.Status.Verdict == nil {
		return map[string]string{"type": "", "entity": ""}
	}
	return map[string]string{"type": request.Status.Verdict.RootCauseType, "entity": TargetIdentity(request.Status.Verdict.RootCauseEntity)}
}

// TargetIdentity is the exact, stable representation used by RootCauseEntity
// expectations. It deliberately includes API version, kind, namespace, and
// name so a same-named object in another scope cannot satisfy an assertion.
func TargetIdentity(target v1alpha1.TargetRef) string {
	if target.Kind == "" && target.Name == "" && target.Namespace == "" && target.APIVersion == "" {
		return ""
	}
	return fmt.Sprintf("%s/%s/%s/%s", target.APIVersion, target.Kind, target.Namespace, target.Name)
}

func riskSignalPhases(signals []v1alpha1.RiskSignal) []string {
	set := map[string]struct{}{}
	for _, signal := range signals {
		set[signal.Status.Phase] = struct{}{}
	}
	return sortedSet(set)
}

func containsRiskSignal(signals []v1alpha1.RiskSignal, ref v1alpha1.NamespacedObjectReference) bool {
	for _, signal := range signals {
		if signal.Namespace == ref.Namespace && signal.Name == ref.Name {
			return true
		}
	}
	return false
}

func sortedSet(set map[string]struct{}) []string {
	values := make([]string, 0, len(set))
	for value := range set {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

func missingValues(required, actual []string) []string {
	set := map[string]struct{}{}
	for _, value := range actual {
		set[value] = struct{}{}
	}
	missing := []string{}
	for _, value := range required {
		if _, ok := set[value]; !ok {
			missing = append(missing, value)
		}
	}
	return missing
}

func validatePublicReport(snapshot RuntimeSnapshot) error {
	var report map[string]any
	if err := json.Unmarshal(snapshot.PublicReport, &report); err != nil {
		return fmt.Errorf("decode public report: %w", err)
	}
	if report["schemaVersion"] != PublicReportSchemaVersion {
		return fmt.Errorf("expected schemaVersion %q, got %v", PublicReportSchemaVersion, report["schemaVersion"])
	}
	selection, ok := report["selection"].(map[string]any)
	if !ok {
		return errors.New("public report selection is missing")
	}
	if selection["namespace"] != snapshot.Namespace || selection["riskRule"] != snapshot.RiskRuleName {
		return fmt.Errorf("public report selection does not match %s/%s", snapshot.Namespace, snapshot.RiskRuleName)
	}
	if _, ok := report["riskRule"]; !ok {
		return errors.New("public report RiskRule is missing")
	}
	if requests, ok := report["investigationRequests"].([]any); !ok || requests == nil {
		return errors.New("public report investigationRequests must be an array")
	}
	if signals, ok := report["riskSignals"].([]any); !ok || signals == nil {
		return errors.New("public report riskSignals must be an array")
	}
	if containsInternalReportField(report) {
		return errors.New("public report contains internal validation fields")
	}
	return nil
}

func containsInternalReportField(value any) bool {
	forbidden := map[string]struct{}{"expected": {}, "actual": {}, "assertions": {}, "differences": {}, "sideEffects": {}, "runtimeError": {}}
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if _, ok := forbidden[key]; ok {
				return true
			}
			if containsInternalReportField(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsInternalReportField(child) {
				return true
			}
		}
	}
	return false
}

type snapshotArtifact struct {
	SchemaVersion         string                          `json:"schemaVersion"`
	ScenarioID            string                          `json:"scenarioID"`
	Namespace             string                          `json:"namespace"`
	RiskRuleName          string                          `json:"riskRuleName"`
	RiskRule              *v1alpha1.RiskRule              `json:"riskRule,omitempty"`
	InvestigationRequest  *v1alpha1.InvestigationRequest  `json:"investigationRequest,omitempty"`
	InvestigationRequests []v1alpha1.InvestigationRequest `json:"investigationRequests"`
	RiskSignals           []v1alpha1.RiskSignal           `json:"riskSignals"`
	UnexpectedSideEffects SideEffectCounts                `json:"unexpectedSideEffects"`
}

func writeScenarioArtifacts(root string, scenario RuntimeScenario, snapshot RuntimeSnapshot) error {
	internalDir := filepath.Join(root, "internal", "scenarios", scenario.ID)
	if err := os.MkdirAll(internalDir, 0o755); err != nil {
		return err
	}
	artifact := snapshotArtifact{SchemaVersion: SnapshotSchemaVersion, ScenarioID: scenario.ID, Namespace: snapshot.Namespace, RiskRuleName: snapshot.RiskRuleName, RiskRule: snapshot.RiskRule, InvestigationRequest: snapshot.InvestigationRequest, InvestigationRequests: snapshot.InvestigationRequests, RiskSignals: snapshot.RiskSignals, UnexpectedSideEffects: snapshot.UnexpectedSideEffects}
	if err := writeJSON(filepath.Join(internalDir, "snapshot.json"), artifact); err != nil {
		return err
	}
	if len(snapshot.PublicReport) > 0 {
		publicDir := filepath.Join(root, "user-facing", "scenarios", scenario.ID)
		if err := os.MkdirAll(publicDir, 0o755); err != nil {
			return err
		}
		if err := writeBytes(filepath.Join(publicDir, "report.json"), snapshot.PublicReport); err != nil {
			return err
		}
	}
	return nil
}

func scenarioArtifactPaths(root, id string, public bool) []string {
	paths := []string{filepath.ToSlash(filepath.Join("internal", "scenarios", id, "snapshot.json"))}
	if public {
		paths = append(paths, filepath.ToSlash(filepath.Join("user-facing", "scenarios", id, "report.json")))
	}
	return paths
}

func writeInternalReport(root string, report InternalReport) error {
	if err := os.MkdirAll(filepath.Join(root, "internal"), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return writeBytes(filepath.Join(root, "internal", "summary.json"), append(data, '\n'))
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeBytes(path, append(data, '\n'))
}

func writeBytes(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	_, err = file.Write(data)
	return err
}

func summarize(scenarios []ScenarioResult) ReportSummary {
	summary := ReportSummary{Result: "PASS", Total: len(scenarios)}
	for _, scenario := range scenarios {
		if scenario.Result == "PASS" {
			summary.Passed++
		} else {
			summary.Failed++
		}
	}
	if summary.Failed > 0 {
		summary.Result = "FAIL"
	}
	return summary
}

// WriteInternalReport exposes the stable internal report serializer for
// runners that orchestrate outside Runner.Run.
func WriteInternalReport(w io.Writer, report InternalReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

// NewObjectKey is a small helper for scenario setup and assertions that need
// stable Kubernetes object identity without importing controller internals.
func NewObjectKey(namespace, name string) types.NamespacedName {
	return types.NamespacedName{Namespace: namespace, Name: name}
}
