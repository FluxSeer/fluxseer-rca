package investigation

import (
	"testing"
	"time"

	"github.com/FluxSeer/fluxseer-rca/internal/diagnostic"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
)

func TestAdaptSchedulingEvidencePreservesIdentityCoverageAndProvenance(t *testing.T) {
	now := time.Date(2026, 10, 20, 2, 0, 0, 0, time.UTC)
	observations, refs := adaptSchedulingEvidence([]domain.SchedulingEvidence{{
		ID: "pod/pod-1", PodUID: "pod-1", Namespace: "prod", Name: "web-pod", Source: "kubernetes/pod",
		Coverage: domain.SchedulingCoverageComplete, CollectedAt: now, IncidentStart: now.Add(-time.Minute), IncidentEnd: now,
		Events: []domain.SchedulingEvent{{ID: "core/event-1", UID: "event-1", PodUID: "pod-1", Source: "kubernetes/core-v1", Reason: "FailedScheduling", Message: "Insufficient cpu", ObservedAt: now}},
	}})
	if len(observations) != 1 || observations[0].PodUID != "pod-1" || observations[0].Coverage != domain.SchedulingCoverageComplete {
		t.Fatalf("expected identity and coverage to survive adaptation, got %#v", observations)
	}
	if !observations[0].IncidentStart.Equal(now.Add(-time.Minute)) || observations[0].Source != "kubernetes/pod" {
		t.Fatalf("expected temporal/provenance context to survive adaptation, got %#v", observations[0])
	}
	if observations[0].EventPodUID != "pod-1" || observations[0].EventReason != "FailedScheduling" {
		t.Fatalf("expected correlated event selection, got %#v", observations[0])
	}
	if len(refs) != 2 || refs[0].Source != "kubernetes/core-v1" {
		t.Fatalf("expected pod and event references with source, got %#v", refs)
	}
}

func TestAdaptSchedulingEvidenceDropsUnauthorizedAndStaleEvents(t *testing.T) {
	now := time.Now().UTC()
	observations, refs := adaptSchedulingEvidence([]domain.SchedulingEvidence{
		{ID: "pod/secret", PodUID: "secret", Coverage: domain.SchedulingCoverageUnauthorized},
		{ID: "pod/stale", PodUID: "stale", Namespace: "prod", Name: "stale", Coverage: domain.SchedulingCoverageComplete, CollectedAt: now, Events: []domain.SchedulingEvent{{ID: "events/old", PodUID: "stale", Stale: true, Reason: "FailedScheduling", Message: "Insufficient cpu"}}},
	})
	if len(observations) != 1 || observations[0].PodUID != "stale" {
		t.Fatalf("expected unauthorized evidence to be omitted, got %#v", observations)
	}
	if len(refs) != 1 || refs[0].ID != "pod/stale" {
		t.Fatalf("expected stale event reference to be omitted, got %#v", refs)
	}
	result := (diagnostic.SchedulingDiagnostic{}).Evaluate(diagnostic.EvidenceSet{Scheduling: observations})
	if result.Status != diagnostic.StatusInconclusive || len(result.Claims) != 1 {
		t.Fatalf("expected stale event to yield only unassigned/observation-level uncertainty, got %#v", result)
	}
}

func TestAdaptSchedulingEvidenceMarksTruncationWithoutRootCause(t *testing.T) {
	observations, refs := adaptSchedulingEvidence([]domain.SchedulingEvidence{{
		ID: "pod/pod-1", PodUID: "pod-1", Namespace: "prod", Name: "web-pod", NodeName: "", Coverage: domain.SchedulingCoverageTruncated,
		CoverageReason: "event budget exhausted", Truncated: true,
		Events: []domain.SchedulingEvent{{ID: "events/sched-1", PodUID: "pod-1", Reason: "FailedScheduling", Message: "Insufficient cpu"}},
	}})
	if len(observations) != 1 || !observations[0].Truncated || len(refs) != 2 || !refs[1].Truncated {
		t.Fatalf("expected truncation metadata on observation and refs, got observations=%#v refs=%#v", observations, refs)
	}
	result := (diagnostic.SchedulingDiagnostic{}).Evaluate(diagnostic.EvidenceSet{Scheduling: observations})
	for _, claim := range result.Claims {
		if claim.Type == diagnostic.ClaimRootCause || claim.Type == diagnostic.ClaimFailureMechanism {
			t.Fatalf("truncated evidence must not support mechanism/root cause claims: %#v", result)
		}
	}
}
