package reasoning

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"fluxseer/internal/domain"
	"fluxseer/internal/knowledge"
)

type providerRCAFixture struct {
	Provider string         `json:"provider"`
	Model    string         `json:"model"`
	Response map[string]any `json:"response"`
	Expected expectedRCA    `json:"expected"`
}

type expectedRCA struct {
	RiskTitle       string   `json:"riskTitle"`
	RiskSummary     string   `json:"riskSummary"`
	Severity        string   `json:"severity"`
	ConfidenceScore int      `json:"confidenceScore"`
	Hypothesis      string   `json:"hypothesis"`
	Causes          []string `json:"causes"`
	ActionType      string   `json:"actionType"`
}

type fixtureProvider struct {
	provider string
	model    string
	output   map[string]any
}

func (p fixtureProvider) Name() string {
	return p.provider
}

func (p fixtureProvider) Complete(context.Context, domain.ModelRequest) (domain.ModelResponse, error) {
	return domain.ModelResponse{
		Provider:   p.provider,
		Model:      p.model,
		Structured: true,
		Output:     p.output,
	}, nil
}

func TestProviderRCAGoldenFixtures(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "provider-rca", "*.json"))
	if err != nil {
		t.Fatalf("glob provider RCA fixtures: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected provider RCA fixtures")
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			var fixture providerRCAFixture
			if err := json.Unmarshal(data, &fixture); err != nil {
				t.Fatalf("decode fixture: %v", err)
			}
			engine := NewEngine(knowledge.NewBase(), fixtureProvider{
				provider: fixture.Provider,
				model:    fixture.Model,
				output:   fixture.Response,
			})
			got, err := engine.Analyze(context.Background(), domain.IngestionOutput{
				Context: domain.IncidentContext{
					Resource: domain.ResourceRef{Namespace: "prod", Name: "checkout-api", Kind: "Deployment"},
					Summary:  "provider RCA fixture",
				},
			})
			if err != nil {
				t.Fatalf("analyze fixture: %v", err)
			}
			if got.Provider != fixture.Provider {
				t.Fatalf("expected provider %q, got %q", fixture.Provider, got.Provider)
			}
			if got.RiskTitle != fixture.Expected.RiskTitle ||
				got.RiskSummary != fixture.Expected.RiskSummary ||
				string(got.Severity) != fixture.Expected.Severity ||
				got.Confidence.Score != fixture.Expected.ConfidenceScore ||
				got.RCA.Hypothesis != fixture.Expected.Hypothesis ||
				got.Remediation.ActionType != fixture.Expected.ActionType {
				t.Fatalf("unexpected normalized RCA\nwant=%#v\ngot=%#v", fixture.Expected, got)
			}
			if len(got.RCA.Causes) != len(fixture.Expected.Causes) {
				t.Fatalf("expected causes %#v, got %#v", fixture.Expected.Causes, got.RCA.Causes)
			}
			for index, want := range fixture.Expected.Causes {
				if got.RCA.Causes[index] != want {
					t.Fatalf("expected cause[%d]=%q, got %#v", index, want, got.RCA.Causes)
				}
			}
		})
	}
}
