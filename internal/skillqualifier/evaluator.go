package skillqualifier

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"sigs.k8s.io/yaml"
)

const (
	CorpusSchemaVersion = "fluxseer-engineering-baseline-evals/v1"
	ResultSchemaVersion = "fluxseer-engineering-baseline-results/v1"
	ReportSchemaVersion = "fluxseer-engineering-baseline-qualification/v1"
	StatusPrepared      = "PREPARED"
	StatusPass          = "PASS"
	StatusFail          = "FAIL"
	StatusUnstable      = "UNSTABLE"
	StatusBlocked       = "BLOCKED"
	StatusNotApplicable = "NOT_APPLICABLE"
)

var validClasses = map[string]bool{
	"violation":           true,
	"safe-counterexample": true,
	"unrelated":           true,
}

var validDecisions = map[string]bool{
	"PASS":          true,
	"NEEDS_CHANGES": true,
	"BLOCKED":       true,
}

type Corpus struct {
	SchemaVersion string `json:"schemaVersion"`
	Skill         string `json:"skill"`
	Golden        bool   `json:"golden"`
	Purpose       string `json:"purpose"`
	Cases         []Case `json:"cases"`
}

type Case struct {
	ID       string       `json:"id"`
	Class    string       `json:"class"`
	Critical bool         `json:"critical"`
	Prompt   string       `json:"prompt"`
	Expected Expectations `json:"expected"`
}

type Expectations struct {
	SkillRequired bool     `json:"skillRequired"`
	Decision      []string `json:"decision"`
	MustIdentify  []string `json:"mustIdentify"`
	MustRecommend []string `json:"mustRecommend"`
	MustNotFlag   []string `json:"mustNotFlag"`
}

// CapturedRun is the structured output supplied by a local Agent session or
// external eval harness. It intentionally contains semantic tokens rather than
// free-text expected output, so wording changes do not invalidate a judgment.
type CapturedRun struct {
	SchemaVersion string           `json:"schemaVersion"`
	RunID         string           `json:"runId"`
	Cases         []CapturedResult `json:"cases"`
}

type CapturedResult struct {
	CaseID         string   `json:"case_id"`
	SkillActivated bool     `json:"skill_activated"`
	Decision       string   `json:"decision"`
	Identified     []string `json:"identified"`
	Recommended    []string `json:"recommended"`
	Flags          []string `json:"flags"`
	TraceRef       string   `json:"trace_ref,omitempty"`
}

type QualificationReport struct {
	SchemaVersion   string       `json:"schemaVersion"`
	ExecutionStatus string       `json:"executionStatus"`
	Status          string       `json:"status"`
	Summary         Summary      `json:"summary"`
	Cases           []CaseResult `json:"cases"`
}

type Summary struct {
	Cases          int                     `json:"cases"`
	Executed       int                     `json:"executed"`
	Activation     DimensionSummary        `json:"activation"`
	Correctness    DimensionSummary        `json:"correctness"`
	Restraint      DimensionSummary        `json:"restraint"`
	Actionability  DimensionSummary        `json:"actionability"`
	ClassResults   map[string]ClassSummary `json:"classResults"`
	FalseNegatives int                     `json:"falseNegatives"`
	FalsePositives int                     `json:"falsePositives"`
	Overall        string                  `json:"overall"`
}

type AggregateReport struct {
	SchemaVersion   string                `json:"schemaVersion"`
	ExecutionStatus string                `json:"executionStatus"`
	Status          string                `json:"status"`
	Runs            int                   `json:"runs"`
	Summary         AggregateSummary      `json:"summary"`
	Cases           []StabilityCaseResult `json:"cases"`
}

type AggregateSummary struct {
	Cases                  int                     `json:"cases"`
	Executions             int                     `json:"executions"`
	Activation             DimensionSummary        `json:"activation"`
	Correctness            DimensionSummary        `json:"correctness"`
	Restraint              DimensionSummary        `json:"restraint"`
	Actionability          DimensionSummary        `json:"actionability"`
	ClassResults           map[string]ClassSummary `json:"classResults"`
	SemanticPassed         int                     `json:"semanticPassed"`
	SemanticTotal          int                     `json:"semanticTotal"`
	CriticalFalseNegatives int                     `json:"criticalFalseNegatives"`
	UnsafeApprovals        int                     `json:"unsafeApprovals"`
	FalsePositives         int                     `json:"falsePositives"`
	UnstableCases          int                     `json:"unstableCases"`
	Overall                string                  `json:"overall"`
}

type StabilityCaseResult struct {
	CaseID          string `json:"case_id"`
	Class           string `json:"class"`
	Critical        bool   `json:"critical"`
	Runs            int    `json:"runs"`
	Passed          int    `json:"passed"`
	Unstable        bool   `json:"unstable"`
	CriticalFailure bool   `json:"criticalFailure"`
}

type DimensionSummary struct {
	Passed int `json:"passed"`
	Total  int `json:"total"`
}

type ClassSummary struct {
	Passed int `json:"passed"`
	Total  int `json:"total"`
}

type CaseResult struct {
	CaseID         string          `json:"case_id"`
	Class          string          `json:"class"`
	Critical       bool            `json:"critical"`
	SkillActivated *bool           `json:"skill_activated"`
	Decision       string          `json:"decision"`
	Expected       Expectations    `json:"expected"`
	Actual         *CapturedResult `json:"actual"`
	Checks         *Checks         `json:"checks"`
	Score          *float64        `json:"score"`
	Pass           *bool           `json:"pass"`
	FailedChecks   []string        `json:"failed_checks,omitempty"`
}

type Checks struct {
	Activation    bool `json:"activation"`
	Correctness   bool `json:"correctness"`
	Restraint     bool `json:"restraint"`
	Actionability bool `json:"actionability"`
}

func LoadCorpus(path string) (Corpus, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Corpus{}, fmt.Errorf("read corpus %q: %w", path, err)
	}
	var corpus Corpus
	if err := yaml.Unmarshal(data, &corpus); err != nil {
		return Corpus{}, fmt.Errorf("decode corpus %q: %w", path, err)
	}
	if err := ValidateCorpus(corpus); err != nil {
		return Corpus{}, err
	}
	return corpus, nil
}

func LoadCapturedRun(path string) (CapturedRun, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return CapturedRun{}, fmt.Errorf("read captured run %q: %w", path, err)
	}
	var run CapturedRun
	if err := yaml.Unmarshal(data, &run); err != nil {
		return CapturedRun{}, fmt.Errorf("decode captured run %q: %w", path, err)
	}
	if run.SchemaVersion != ResultSchemaVersion {
		return CapturedRun{}, fmt.Errorf("captured run schema %q does not match %q", run.SchemaVersion, ResultSchemaVersion)
	}
	return run, nil
}

func ValidateCorpus(corpus Corpus) error {
	if corpus.SchemaVersion != CorpusSchemaVersion {
		return fmt.Errorf("corpus schema %q does not match %q", corpus.SchemaVersion, CorpusSchemaVersion)
	}
	if corpus.Skill == "" {
		return fmt.Errorf("corpus skill is required")
	}
	if len(corpus.Cases) == 0 {
		return fmt.Errorf("corpus must contain at least one case")
	}
	seen := map[string]bool{}
	classCounts := map[string]int{}
	for _, item := range corpus.Cases {
		if item.ID == "" || seen[item.ID] {
			return fmt.Errorf("case ids must be non-empty and unique: %q", item.ID)
		}
		seen[item.ID] = true
		if item.Prompt == "" {
			return fmt.Errorf("case %q prompt is required", item.ID)
		}
		if !validClasses[item.Class] {
			return fmt.Errorf("case %q has invalid class %q", item.ID, item.Class)
		}
		classCounts[item.Class]++
		if len(item.Expected.Decision) == 0 {
			return fmt.Errorf("case %q must define an expected decision", item.ID)
		}
		for _, decision := range item.Expected.Decision {
			if !validDecisions[decision] {
				return fmt.Errorf("case %q has invalid expected decision %q", item.ID, decision)
			}
		}
	}
	for _, class := range []string{"violation", "safe-counterexample", "unrelated"} {
		if classCounts[class] == 0 {
			return fmt.Errorf("corpus must include class %q", class)
		}
	}
	if corpus.Golden && (len(corpus.Cases) != 13 || classCounts["violation"] != 5 || classCounts["safe-counterexample"] != 5 || classCounts["unrelated"] != 3) {
		return fmt.Errorf("golden corpus must contain 13 cases split 5 violation, 5 safe-counterexample, 3 unrelated")
	}
	return nil
}

func PreparedReport(corpus Corpus) QualificationReport {
	classResults := make(map[string]ClassSummary, len(validClasses))
	for _, item := range corpus.Cases {
		result := classResults[item.Class]
		result.Total++
		classResults[item.Class] = result
	}
	return QualificationReport{
		SchemaVersion:   ReportSchemaVersion,
		ExecutionStatus: StatusPrepared,
		Status:          StatusPrepared,
		Summary: Summary{
			Cases:        len(corpus.Cases),
			ClassResults: classResults,
			Overall:      StatusPrepared,
		},
		Cases: preparedCases(corpus.Cases),
	}
}

func Evaluate(corpus Corpus, run CapturedRun) (QualificationReport, error) {
	if err := ValidateCorpus(corpus); err != nil {
		return QualificationReport{}, err
	}
	if len(run.Cases) != len(corpus.Cases) {
		return QualificationReport{}, fmt.Errorf("captured run has %d cases; corpus requires %d", len(run.Cases), len(corpus.Cases))
	}
	byID := make(map[string]CapturedResult, len(run.Cases))
	for _, item := range run.Cases {
		if item.CaseID == "" || byID[item.CaseID].CaseID != "" {
			return QualificationReport{}, fmt.Errorf("captured case ids must be non-empty and unique: %q", item.CaseID)
		}
		if !validDecisions[item.Decision] {
			return QualificationReport{}, fmt.Errorf("captured case %q has invalid decision %q", item.CaseID, item.Decision)
		}
		byID[item.CaseID] = item
	}

	report := PreparedReport(corpus)
	report.ExecutionStatus = "EXECUTED"
	report.Summary.Executed = len(run.Cases)
	report.Cases = make([]CaseResult, 0, len(corpus.Cases))
	for _, item := range corpus.Cases {
		actual, ok := byID[item.ID]
		if !ok {
			return QualificationReport{}, fmt.Errorf("captured run is missing case %q", item.ID)
		}
		result := evaluateCase(item, actual)
		report.Cases = append(report.Cases, result)
		accumulateSummary(&report.Summary, item, result)
	}
	report.Summary.Overall = StatusPass
	report.Status = StatusPass
	for _, result := range report.Cases {
		if result.Pass == nil || !*result.Pass {
			report.Summary.Overall = StatusFail
			report.Status = StatusFail
			break
		}
	}
	return report, nil
}

// EvaluateRuns aggregates independently captured Agent runs. A mixed result
// for the same case is UNSTABLE, even if one run passed, because a behavioral
// gate must be repeatable before it can qualify a Skill.
func EvaluateRuns(corpus Corpus, runs []CapturedRun) (AggregateReport, error) {
	if len(runs) == 0 {
		return AggregateReport{}, fmt.Errorf("at least one captured run is required")
	}
	if err := ValidateCorpus(corpus); err != nil {
		return AggregateReport{}, err
	}
	perRun := make([]QualificationReport, 0, len(runs))
	for index, run := range runs {
		if run.RunID == "" {
			run.RunID = fmt.Sprintf("run-%d", index+1)
		}
		result, err := Evaluate(corpus, run)
		if err != nil {
			return AggregateReport{}, fmt.Errorf("evaluate %s: %w", run.RunID, err)
		}
		perRun = append(perRun, result)
	}

	report := AggregateReport{
		SchemaVersion:   ReportSchemaVersion,
		ExecutionStatus: "EXECUTED",
		Status:          StatusPass,
		Runs:            len(perRun),
		Summary: AggregateSummary{
			Cases:        len(corpus.Cases),
			Executions:   len(perRun),
			ClassResults: map[string]ClassSummary{},
		},
		Cases: make([]StabilityCaseResult, 0, len(corpus.Cases)),
	}

	for caseIndex, item := range corpus.Cases {
		stability := StabilityCaseResult{CaseID: item.ID, Class: item.Class, Critical: item.Critical, Runs: len(perRun)}
		passCount := 0
		for _, run := range perRun {
			caseResult := run.Cases[caseIndex]
			if caseResult.Pass != nil && *caseResult.Pass {
				passCount++
			}
			if caseResult.Checks != nil {
				accumulateAggregateDimensions(&report.Summary, *caseResult.Checks)
				if item.Critical && !caseResult.Checks.Correctness {
					stability.CriticalFailure = true
				}
				if item.Class == "violation" && caseResult.Decision == "PASS" {
					report.Summary.UnsafeApprovals++
				}
			}
		}
		stability.Passed = passCount
		stability.Unstable = passCount > 0 && passCount < len(perRun)
		if stability.Unstable {
			report.Summary.UnstableCases++
		}
		if stability.CriticalFailure {
			report.Summary.CriticalFalseNegatives++
		}
		if item.Class != "violation" && passCount < len(perRun) {
			report.Summary.FalsePositives++
		}
		report.Summary.SemanticPassed += passCount
		report.Summary.SemanticTotal += len(perRun)
		class := report.Summary.ClassResults[item.Class]
		class.Total += len(perRun)
		class.Passed += passCount
		report.Summary.ClassResults[item.Class] = class
		report.Cases = append(report.Cases, stability)
	}

	if report.Summary.UnstableCases > 0 {
		report.Status = StatusUnstable
	} else {
		for _, item := range report.Cases {
			if item.Passed != item.Runs || item.CriticalFailure {
				report.Status = StatusFail
				break
			}
		}
	}
	report.Summary.Overall = report.Status
	return report, nil
}

func accumulateAggregateDimensions(summary *AggregateSummary, checks Checks) {
	dimensions := []struct {
		value  bool
		target *DimensionSummary
	}{
		{checks.Activation, &summary.Activation},
		{checks.Correctness, &summary.Correctness},
		{checks.Restraint, &summary.Restraint},
		{checks.Actionability, &summary.Actionability},
	}
	for _, dimension := range dimensions {
		dimension.target.Total++
		if dimension.value {
			dimension.target.Passed++
		}
	}
}

func evaluateCase(item Case, actual CapturedResult) CaseResult {
	checks := Checks{
		Activation:    actual.SkillActivated == item.Expected.SkillRequired,
		Correctness:   containsAny(item.Expected.Decision, actual.Decision) && containsAll(actual.Identified, item.Expected.MustIdentify),
		Restraint:     containsNone(actual.Flags, item.Expected.MustNotFlag),
		Actionability: containsAll(actual.Recommended, item.Expected.MustRecommend),
	}
	passed := checks.Activation && checks.Correctness && checks.Restraint && checks.Actionability
	score := float64(0)
	for _, value := range []bool{checks.Activation, checks.Correctness, checks.Restraint, checks.Actionability} {
		if value {
			score++
		}
	}
	score /= 4
	failed := make([]string, 0, 4)
	if !checks.Activation {
		failed = append(failed, "activation")
	}
	if !checks.Correctness {
		failed = append(failed, "correctness")
	}
	if !checks.Restraint {
		failed = append(failed, "restraint")
	}
	if !checks.Actionability {
		failed = append(failed, "actionability")
	}
	return CaseResult{
		CaseID:         item.ID,
		Class:          item.Class,
		Critical:       item.Critical,
		SkillActivated: &actual.SkillActivated,
		Decision:       actual.Decision,
		Expected:       item.Expected,
		Actual:         &actual,
		Checks:         &checks,
		Score:          &score,
		Pass:           &passed,
		FailedChecks:   failed,
	}
}

func accumulateSummary(summary *Summary, item Case, result CaseResult) {
	checks := *result.Checks
	for value, target := range map[bool]*DimensionSummary{
		checks.Activation:    &summary.Activation,
		checks.Correctness:   &summary.Correctness,
		checks.Restraint:     &summary.Restraint,
		checks.Actionability: &summary.Actionability,
	} {
		(*target).Total++
		if value {
			(*target).Passed++
		}
	}
	class := summary.ClassResults[item.Class]
	class.Total++
	if result.Pass != nil && *result.Pass {
		class.Passed++
	} else if item.Class == "violation" && !checks.Correctness {
		summary.FalseNegatives++
	} else if item.Class != "violation" {
		summary.FalsePositives++
	}
	summary.ClassResults[item.Class] = class
}

func preparedCases(cases []Case) []CaseResult {
	results := make([]CaseResult, 0, len(cases))
	for _, item := range cases {
		results = append(results, CaseResult{CaseID: item.ID, Class: item.Class, Critical: item.Critical, Expected: item.Expected})
	}
	return results
}

func containsAny(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func containsAll(values, expected []string) bool {
	for _, item := range expected {
		if !containsAny(values, item) {
			return false
		}
	}
	return true
}

func containsNone(values, forbidden []string) bool {
	for _, item := range forbidden {
		if containsAny(values, item) {
			return false
		}
	}
	return true
}

func MarshalReport(report QualificationReport) ([]byte, error) {
	return json.MarshalIndent(report, "", "  ")
}

func MarshalAggregateReport(report AggregateReport) ([]byte, error) {
	return json.MarshalIndent(report, "", "  ")
}

func SortedClassNames(summary Summary) []string {
	names := make([]string, 0, len(summary.ClassResults))
	for name := range summary.ClassResults {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
