package diagnostic

import (
	_ "embed"
	"encoding/json"
	"testing"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
	"github.com/FluxSeer/fluxseer-rca/internal/scope"
)

// This fixture is derived from the v1.34.1 live qualification after removing
// cluster-specific names. It keeps the normalized evidence and expected
// diagnostic claims replayable without requiring a Kubernetes API.
//
//go:embed testdata/imagepull-failure-v1.json
var imagePullFixture []byte

func TestImagePullDiagnosticReplaysLiveDerivedFixture(t *testing.T) {
	var fixture struct {
		SchemaVersion string `json:"schemaVersion"`
		Evidence      struct {
			Observations []struct {
				ID      string `json:"id"`
				Type    string `json:"type"`
				Reason  string `json:"reason"`
				Message string `json:"message"`
				Summary string `json:"summary"`
			} `json:"observations"`
			EvidenceRefs []struct {
				ID      string `json:"id"`
				Kind    string `json:"kind"`
				Reason  string `json:"reason"`
				Summary string `json:"summary"`
			} `json:"evidenceRefs"`
		} `json:"evidence"`
		Expected struct {
			DiagnosticID                 string   `json:"diagnosticID"`
			Status                       string   `json:"status"`
			ClaimIDs                     []string `json:"claimIDs"`
			RootCauseCandidateUnverified bool     `json:"rootCauseCandidateIsUnverified"`
		} `json:"expected"`
	}
	if err := json.Unmarshal(imagePullFixture, &fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if fixture.SchemaVersion != "fluxseer-composable-diagnostics-fixture/v1" {
		t.Fatalf("unexpected fixture schema %q", fixture.SchemaVersion)
	}

	evidence := EvidenceSet{Scope: scope.Result{Complete: true}}
	for _, item := range fixture.Evidence.Observations {
		evidence.Observations = append(evidence.Observations, domain.Observation{
			ID:      item.ID,
			Type:    domain.ObservationTypeEvent,
			Summary: item.Summary,
			Value: domain.ObservationValue{Event: &domain.EventObservation{
				Reason: item.Reason, Message: item.Message,
			}},
		})
	}
	for _, item := range fixture.Evidence.EvidenceRefs {
		evidence.EvidenceRefs = append(evidence.EvidenceRefs, v1alpha1.EvidenceRef{
			ID: item.ID, Kind: item.Kind, Reason: item.Reason, Summary: item.Summary,
		})
	}

	result := (ImagePullDiagnostic{}).Evaluate(evidence)
	if result.DiagnosticID != fixture.Expected.DiagnosticID || string(result.Status) != fixture.Expected.Status {
		t.Fatalf("unexpected diagnostic identity/status: %#v", result)
	}
	if len(result.Claims) != len(fixture.Expected.ClaimIDs) {
		t.Fatalf("expected claim IDs %v, got %#v", fixture.Expected.ClaimIDs, result.Claims)
	}
	for index, want := range fixture.Expected.ClaimIDs {
		if result.Claims[index].ID != want {
			t.Fatalf("expected claim[%d]=%q, got %#v", index, want, result.Claims[index])
		}
	}
	if !fixture.Expected.RootCauseCandidateUnverified || result.Status == StatusConfirmed {
		t.Fatalf("expected root-cause candidate to remain unverified, got %#v", result)
	}
}
