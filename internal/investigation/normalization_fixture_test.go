package investigation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fluxseer/api/v1alpha1"
	"fluxseer/internal/datasource"
	"fluxseer/internal/domain"
)

type normalizationFixture struct {
	Name        string              `json:"name"`
	CollectedAt string              `json:"collectedAt"`
	Query       fixtureQueryRequest `json:"query"`
	Result      fixtureQueryResult  `json:"result"`
}

type fixtureQueryRequest struct {
	Query     string             `json:"query"`
	StartTime string             `json:"startTime"`
	EndTime   string             `json:"endTime"`
	Step      string             `json:"step"`
	Labels    map[string]string  `json:"labels,omitempty"`
	Target    domain.ResourceRef `json:"target"`
	QueryType domain.QueryType   `json:"queryType"`
}

type fixtureQueryResult struct {
	Source    string           `json:"source"`
	QueryType domain.QueryType `json:"queryType"`
	Summary   string           `json:"summary,omitempty"`
	Records   []map[string]any `json:"records"`
}

type normalizationGolden struct {
	Observations []domain.Observation   `json:"observations"`
	EvidenceRefs []v1alpha1.EvidenceRef `json:"evidenceRefs"`
}

func TestNormalizationGoldenFixtures(t *testing.T) {
	inputs, err := filepath.Glob("testdata/normalization/*.input.json")
	if err != nil {
		t.Fatalf("glob normalization fixtures: %v", err)
	}
	if len(inputs) == 0 {
		t.Fatal("expected normalization fixtures")
	}

	for _, inputPath := range inputs {
		inputPath := inputPath
		name := strings.TrimSuffix(filepath.Base(inputPath), ".input.json")
		t.Run(name, func(t *testing.T) {
			fixture := readNormalizationFixture(t, inputPath)
			got := runNormalizationFixture(t, fixture)
			gotJSON := marshalGolden(t, got)

			goldenPath := strings.TrimSuffix(inputPath, ".input.json") + ".golden.json"
			if os.Getenv("UPDATE_GOLDEN") == "1" {
				if err := os.WriteFile(goldenPath, gotJSON, 0o644); err != nil {
					t.Fatalf("update golden fixture %s: %v", goldenPath, err)
				}
				return
			}

			wantJSON, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden fixture %s: %v", goldenPath, err)
			}
			if strings.TrimSpace(string(gotJSON)) != strings.TrimSpace(string(wantJSON)) {
				t.Fatalf("normalization golden mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", name, gotJSON, wantJSON)
			}
		})
	}
}

func readNormalizationFixture(t *testing.T, path string) normalizationFixture {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	var fixture normalizationFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("parse fixture %s: %v", path, err)
	}
	return fixture
}

func runNormalizationFixture(t *testing.T, fixture normalizationFixture) normalizationGolden {
	t.Helper()
	collectedAt := parseFixtureTime(t, fixture.CollectedAt)
	step, err := time.ParseDuration(fixture.Query.Step)
	if err != nil {
		t.Fatalf("parse query step: %v", err)
	}
	req := datasource.QueryRequest{
		Query:     fixture.Query.Query,
		StartTime: parseFixtureTime(t, fixture.Query.StartTime),
		EndTime:   parseFixtureTime(t, fixture.Query.EndTime),
		Step:      step,
		Labels:    fixture.Query.Labels,
		Target:    fixture.Query.Target,
		QueryType: fixture.Query.QueryType,
	}
	result := &datasource.QueryResult{
		Source:    fixture.Result.Source,
		QueryType: fixture.Result.QueryType,
		Summary:   fixture.Result.Summary,
		Records:   fixture.Result.Records,
	}
	observations := normalizeObservations(result, req, 0, collectedAt)
	return normalizationGolden{
		Observations: observations,
		EvidenceRefs: evidenceRefsFromObservations(observations, req, v1alpha1.QueryRetentionPolicy{}),
	}
}

func parseFixtureTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse time %q: %v", value, err)
	}
	return parsed
}

func marshalGolden(t *testing.T, golden normalizationGolden) []byte {
	t.Helper()
	data, err := json.MarshalIndent(golden, "", "  ")
	if err != nil {
		t.Fatalf("marshal golden: %v", err)
	}
	return append(data, '\n')
}
