package controllers

import (
	"testing"
	"time"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
	"github.com/FluxSeer/fluxseer-rca/internal/investigation"
)

func TestCrashLoopBackOffV2RequiresFreshCompleteTerminationEvidence(t *testing.T) {
	start := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	end := start.Add(10 * time.Minute)
	spec := v1alpha1.InvestigationRequestSpec{
		EvidenceRequirements: v1alpha1.EvidenceRequirements{Profile: "CrashLoopBackOffV2"},
		Queries: []v1alpha1.InvestigationQuery{{
			Name: "backoff", DatasourceRef: v1alpha1.LocalObjectReference{Name: "kubernetes-events"}, QueryType: "event", Reasons: []string{"BackOff"},
		}},
	}
	evidence := investigation.EvidenceCollectionResult{
		EvidenceRefs: []v1alpha1.EvidenceRef{
			{ID: "event-1", Kind: "event", Source: "kubernetes-events", Reason: "BackOff", Summary: "Back-off restarting failed container"},
			{ID: "termination-1", Kind: "containerTermination", Source: "kubernetes-events", Summary: "container app last terminated"},
		},
		Observations: []domain.Observation{{
			Capability: domain.QueryTypeContainerTermination,
			TimeRange:  domain.TimeRange{Start: start, End: end},
			Value: domain.ObservationValue{ContainerTermination: &domain.ContainerTerminationObservation{
				PodName: "checkout-123", ContainerName: "app", LastTerminatedReason: "Error", LastTerminatedExitCode: 1,
				LastTerminatedFinishedAt: start.Add(5 * time.Minute).Format(time.RFC3339),
			}},
		}},
	}

	if missing := missingSemanticEvidenceCoverage("CrashLoopBackOffV2", spec, evidence); len(missing) != 0 {
		t.Fatalf("expected complete in-window Event and termination evidence, got %#v", missing)
	}

	evidence.Observations[0].Value.ContainerTermination.LastTerminatedFinishedAt = start.Add(-time.Second).Format(time.RFC3339)
	missing := missingSemanticEvidenceCoverage("CrashLoopBackOffV2", spec, evidence)
	if len(missing) != 1 || missing[0].Reason != "ContainerTerminationEvidenceStale" {
		t.Fatalf("expected stale termination status to be identified, got %#v", missing)
	}

	coverage := evidenceCoverageAudit("CrashLoopBackOffV2", spec, evidence, missing)
	if coverage == nil || len(coverage.IncompleteChecks) != 1 || coverage.IncompleteChecks[0] != "containerTermination:LastTermination" {
		t.Fatalf("expected explicit incomplete termination coverage, got %#v", coverage)
	}

	evidence.Observations[0].Truncated = true
	missing = missingSemanticEvidenceCoverage("CrashLoopBackOffV2", spec, evidence)
	if len(missing) != 1 || missing[0].Reason != "ContainerTerminationEvidenceTruncated" {
		t.Fatalf("expected truncated termination status to be identified, got %#v", missing)
	}

	evidence.Observations = nil
	missing = missingSemanticEvidenceCoverage("CrashLoopBackOffV2", spec, evidence)
	if len(missing) != 1 || missing[0].Reason != "ContainerTerminationEvidenceMissing" {
		t.Fatalf("expected absent termination status to be identified, got %#v", missing)
	}
}
