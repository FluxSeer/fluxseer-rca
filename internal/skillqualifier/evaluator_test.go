package skillqualifier

import "testing"

func TestLoadCorpusAndPreparedReport(t *testing.T) {
	corpus := testCorpus()
	if err := ValidateCorpus(corpus); err != nil {
		t.Fatalf("validate corpus: %v", err)
	}
	report := PreparedReport(corpus)
	if report.ExecutionStatus != StatusPrepared || report.Status != StatusPrepared || report.Summary.Overall != StatusPrepared {
		t.Fatalf("unexpected prepared report: %#v", report)
	}
	if report.Summary.Cases != 3 || len(report.Cases) != 3 {
		t.Fatalf("expected three prepared cases, got %#v", report)
	}
}

func TestEvaluateScoresSemanticChecks(t *testing.T) {
	corpus := testCorpus()
	run := CapturedRun{
		SchemaVersion: ResultSchemaVersion,
		Cases: []CapturedResult{
			{CaseID: "violation", SkillActivated: true, Decision: "NEEDS_CHANGES", Identified: []string{"public_contract"}, Recommended: []string{"deprecation_migration"}},
			{CaseID: "safe", SkillActivated: true, Decision: "PASS", Identified: []string{"measured_optimization"}, Recommended: []string{"retain_regression_benchmark"}},
			{CaseID: "unrelated", SkillActivated: false, Decision: "PASS"},
		},
	}
	report, err := Evaluate(corpus, run)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if report.Summary.Overall != "PASS" {
		t.Fatalf("expected PASS, got %#v", report.Summary)
	}
	if report.Summary.FalseNegatives != 0 || report.Summary.FalsePositives != 0 {
		t.Fatalf("unexpected error counts: %#v", report.Summary)
	}
	for name, dimension := range map[string]DimensionSummary{
		"activation":    report.Summary.Activation,
		"correctness":   report.Summary.Correctness,
		"restraint":     report.Summary.Restraint,
		"actionability": report.Summary.Actionability,
	} {
		if dimension.Passed != dimension.Total || dimension.Total != len(corpus.Cases) {
			t.Fatalf("%s summary does not cover every case: %#v", name, dimension)
		}
	}
}

func TestEvaluateRejectsRestraintViolation(t *testing.T) {
	corpus := testCorpus()
	run := CapturedRun{
		SchemaVersion: ResultSchemaVersion,
		Cases: []CapturedResult{
			{CaseID: "violation", SkillActivated: true, Decision: "NEEDS_CHANGES", Identified: []string{"public_contract"}, Recommended: []string{"deprecation_migration"}},
			{CaseID: "safe", SkillActivated: true, Decision: "PASS", Identified: []string{"measured_optimization"}, Recommended: []string{"retain_regression_benchmark"}, Flags: []string{"premature_optimization"}},
			{CaseID: "unrelated", SkillActivated: false, Decision: "PASS"},
		},
	}
	report, err := Evaluate(corpus, run)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if report.Summary.Overall != "FAIL" || report.Summary.FalsePositives != 1 {
		t.Fatalf("expected restraint failure, got %#v", report.Summary)
	}
}

func TestEvaluateRunsMarksMixedResultsUnstable(t *testing.T) {
	corpus := testCorpus()
	passingRun := CapturedRun{
		RunID:         "run-1",
		SchemaVersion: ResultSchemaVersion,
		Cases: []CapturedResult{
			{CaseID: "violation", SkillActivated: true, Decision: "NEEDS_CHANGES", Identified: []string{"public_contract"}, Recommended: []string{"deprecation_migration"}},
			{CaseID: "safe", SkillActivated: true, Decision: "PASS", Identified: []string{"measured_optimization"}, Recommended: []string{"retain_regression_benchmark"}},
			{CaseID: "unrelated", SkillActivated: false, Decision: "PASS"},
		},
	}
	flakyRun := passingRun
	flakyRun.RunID = "run-2"
	flakyRun.Cases = append([]CapturedResult(nil), passingRun.Cases...)
	flakyRun.Cases[1].Flags = []string{"premature_optimization"}

	report, err := EvaluateRuns(corpus, []CapturedRun{passingRun, flakyRun})
	if err != nil {
		t.Fatalf("evaluate runs: %v", err)
	}
	if report.Status != StatusUnstable || report.Summary.UnstableCases != 1 {
		t.Fatalf("expected unstable report, got %#v", report)
	}
	if report.Summary.SemanticPassed != 5 || report.Summary.SemanticTotal != 6 {
		t.Fatalf("unexpected semantic totals: %#v", report.Summary)
	}
	if report.Summary.Executions != 6 {
		t.Fatalf("expected one execution per case per run, got %#v", report.Summary)
	}
}

func testCorpus() Corpus {
	return Corpus{
		SchemaVersion: CorpusSchemaVersion,
		Skill:         "engineering-baseline",
		Cases: []Case{
			{ID: "violation", Class: "violation", Critical: true, Prompt: "remove a public field", Expected: Expectations{SkillRequired: true, Decision: []string{"NEEDS_CHANGES"}, MustIdentify: []string{"public_contract"}, MustRecommend: []string{"deprecation_migration"}}},
			{ID: "safe", Class: "safe-counterexample", Prompt: "measured optimization", Expected: Expectations{SkillRequired: true, Decision: []string{"PASS"}, MustIdentify: []string{"measured_optimization"}, MustRecommend: []string{"retain_regression_benchmark"}, MustNotFlag: []string{"premature_optimization"}}},
			{ID: "unrelated", Class: "unrelated", Prompt: "fix typo", Expected: Expectations{SkillRequired: false, Decision: []string{"PASS"}}},
		},
	}
}
