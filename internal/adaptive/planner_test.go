package adaptive

import "testing"

func TestPlanStepsDisabledByDefault(t *testing.T) {
	got := PlanSteps(Config{}, Request{DesiredTemplates: []string{"latency"}})
	if got.Enabled || len(got.Steps) != 0 || got.Issue != nil {
		t.Fatalf("expected disabled empty plan, got %#v", got)
	}
}

func TestPlanStepsRequiresAllowlistedTemplates(t *testing.T) {
	got := PlanSteps(Config{Enabled: true, AllowedTemplates: []string{"latency"}, MaxSteps: 2, MaxQueries: 2}, Request{
		DesiredTemplates: []string{"latency"},
		Reason:           "latency claim needs more metric evidence",
	})
	if !got.Enabled || got.Issue != nil || len(got.Steps) != 1 {
		t.Fatalf("expected one adaptive step, got %#v", got)
	}
	if got.Steps[0].ID != "adaptive-step-001" || got.Steps[0].QueryTemplate != "latency" {
		t.Fatalf("unexpected step %#v", got.Steps[0])
	}
}

func TestPlanStepsRejectsTemplateOutsideAllowlist(t *testing.T) {
	got := PlanSteps(Config{Enabled: true, AllowedTemplates: []string{"latency"}, MaxSteps: 2, MaxQueries: 2}, Request{
		DesiredTemplates: []string{"raw-log-search"},
	})
	if got.Issue == nil || got.Issue.Reason != "TemplateNotAllowed" {
		t.Fatalf("expected TemplateNotAllowed, got %#v", got)
	}
}

func TestPlanStepsHonorsBudget(t *testing.T) {
	got := PlanSteps(Config{Enabled: true, AllowedTemplates: []string{"latency", "events"}, MaxSteps: 5, MaxQueries: 1}, Request{
		DesiredTemplates: []string{"latency", "events"},
	})
	if len(got.Steps) != 1 {
		t.Fatalf("expected one step due to budget, got %#v", got.Steps)
	}
}
