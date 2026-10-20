package diagnostic

import (
	_ "embed"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
)

//go:embed testdata/scheduling-fixtures-v1.json
var schedulingFixtures []byte

type schedulingFixtureFile struct {
	SchemaVersion string              `json:"schemaVersion"`
	Fixtures      []schedulingFixture `json:"fixtures"`
}

type schedulingFixture struct {
	Name     string `json:"name"`
	Evidence struct {
		EvidenceRefs []v1alpha1.EvidenceRef `json:"evidenceRefs"`
		Scheduling   []struct {
			ID                  string   `json:"id"`
			PodUID              string   `json:"podUID"`
			EventPodUID         string   `json:"eventPodUID"`
			Phase               string   `json:"phase"`
			NodeName            string   `json:"nodeName"`
			PodScheduledStatus  string   `json:"podScheduledStatus"`
			PodScheduledReason  string   `json:"podScheduledReason"`
			PodScheduledMessage string   `json:"podScheduledMessage"`
			SchedulingGates     []string `json:"schedulingGates"`
			EventReason         string   `json:"eventReason"`
			EventMessage        string   `json:"eventMessage"`
			EvidenceRefs        []string `json:"evidenceRefs"`
			Truncated           bool     `json:"truncated"`
			Stale               bool     `json:"stale"`
		} `json:"scheduling"`
	} `json:"evidence"`
	Expected struct {
		Status   string   `json:"status"`
		ClaimIDs []string `json:"claimIDs"`
	} `json:"expected"`
}

func TestSchedulingDiagnosticReplaysContractFixtures(t *testing.T) {
	var file schedulingFixtureFile
	if err := json.Unmarshal(schedulingFixtures, &file); err != nil {
		t.Fatalf("decode scheduling fixtures: %v", err)
	}
	if file.SchemaVersion != "fluxseer-composable-scheduling-fixtures/v1" {
		t.Fatalf("unexpected fixture schema %q", file.SchemaVersion)
	}
	if len(file.Fixtures) < 9 {
		t.Fatalf("expected broad scheduling fixture matrix, got %d cases", len(file.Fixtures))
	}

	for _, fixture := range file.Fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			evidence := EvidenceSet{EvidenceRefs: fixture.Evidence.EvidenceRefs}
			for _, item := range fixture.Evidence.Scheduling {
				evidence.Scheduling = append(evidence.Scheduling, SchedulingObservation{
					ID: item.ID, PodUID: item.PodUID, EventPodUID: item.EventPodUID,
					Phase: item.Phase, NodeName: item.NodeName,
					PodScheduledStatus: item.PodScheduledStatus, PodScheduledReason: item.PodScheduledReason,
					PodScheduledMessage: item.PodScheduledMessage, SchedulingGates: append([]string(nil), item.SchedulingGates...),
					EventReason: item.EventReason, EventMessage: item.EventMessage,
					EvidenceRefs: append([]string(nil), item.EvidenceRefs...), Truncated: item.Truncated, Stale: item.Stale,
				})
			}
			result := (SchedulingDiagnostic{}).Evaluate(evidence)
			if string(result.Status) != fixture.Expected.Status {
				t.Fatalf("expected status %q, got %#v", fixture.Expected.Status, result)
			}
			claimIDs := make([]string, 0, len(result.Claims))
			for _, claim := range result.Claims {
				claimIDs = append(claimIDs, claim.ID)
				if claim.TargetUID == "" {
					t.Fatalf("claim must retain target Pod UID: %#v", claim)
				}
				if len(claim.EvidenceRefs) == 0 && claim.Type != ClaimObservation {
					t.Fatalf("claim must retain evidence references: %#v", claim)
				}
				if claim.Type == ClaimRootCause && result.Status == StatusConfirmed {
					t.Fatal("pure diagnostic must not confirm a root cause")
				}
			}
			if !reflect.DeepEqual(claimIDs, fixture.Expected.ClaimIDs) {
				t.Fatalf("expected claims %v, got %v", fixture.Expected.ClaimIDs, claimIDs)
			}
		})
	}
}

func TestSchedulingDiagnosticDoesNotMatchImagePullEvidenceWithoutSchedulingState(t *testing.T) {
	evidence := EvidenceSet{EvidenceRefs: []v1alpha1.EvidenceRef{{
		ID: "image-event", Kind: "event", Reason: "ErrImagePull", Summary: "failed to pull image",
	}}}
	if match := (SchedulingDiagnostic{}).Match(evidence); match.Applicable {
		t.Fatalf("expected image-pull-only evidence to be outside scheduling diagnostic: %#v", match)
	}
	result := (SchedulingDiagnostic{}).Evaluate(evidence)
	if result.Status != StatusNotApplicable || len(result.Claims) != 0 {
		t.Fatalf("expected no scheduling claims for image-pull-only evidence: %#v", result)
	}
}

func TestSchedulingDiagnosticClaimOrderIsStable(t *testing.T) {
	base := EvidenceSet{Scheduling: []SchedulingObservation{
		{ID: "event-1", PodUID: "pod-b", NodeName: "", PodScheduledStatus: "False", EventPodUID: "pod-b", EventReason: "FailedScheduling", EventMessage: "Insufficient cpu", EvidenceRefs: []string{"event-1"}},
		{ID: "event-2", PodUID: "pod-a", NodeName: "", PodScheduledStatus: "False", EventPodUID: "pod-a", EventReason: "FailedScheduling", EventMessage: "untolerated taint", EvidenceRefs: []string{"event-2"}},
	}}
	reversed := EvidenceSet{Scheduling: []SchedulingObservation{base.Scheduling[1], base.Scheduling[0]}}
	left := (SchedulingDiagnostic{}).Evaluate(base)
	right := (SchedulingDiagnostic{}).Evaluate(reversed)
	if !reflect.DeepEqual(left.Claims, right.Claims) {
		t.Fatalf("expected deterministic claim ordering, left=%#v right=%#v", left.Claims, right.Claims)
	}
}
