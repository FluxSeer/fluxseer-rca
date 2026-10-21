package investigation

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/FluxSeer/fluxseer-rca/internal/diagnostic"
)

var reconciliationWindowStart = time.Date(2026, 10, 21, 1, 0, 0, 0, time.UTC)
var reconciliationWindowEnd = reconciliationWindowStart.Add(15 * time.Minute)

func reconciliationEvidence(id, targetUID string) diagnostic.EvidenceIdentity {
	return diagnostic.EvidenceIdentity{
		ID:            id,
		TargetUID:     targetUID,
		IncidentStart: reconciliationWindowStart,
		IncidentEnd:   reconciliationWindowEnd,
		Authorized:    true,
		Valid:         true,
	}
}

func reconciliationResult(id, version string, claims ...diagnostic.CandidateClaim) diagnostic.Result {
	return diagnostic.Result{DiagnosticID: id, DiagnosticVersion: version, Claims: claims}
}

func reconciliationClaim(id string, claimType diagnostic.ClaimType, statement, targetUID string, refs ...string) diagnostic.CandidateClaim {
	return diagnostic.CandidateClaim{ID: id, Type: claimType, Statement: statement, TargetUID: targetUID, EvidenceRefs: refs}
}

func reconciliationInput(results []diagnostic.Result, evidence ...diagnostic.EvidenceIdentity) CandidateReconciliationInput {
	return CandidateReconciliationInput{
		Diagnostics:            results,
		Evidence:               evidence,
		EvidenceInventoryReady: true,
		IncidentStart:          reconciliationWindowStart,
		IncidentEnd:            reconciliationWindowEnd,
	}
}

func TestReconcileDiagnosticCandidatesMapsImagePullCandidate(t *testing.T) {
	input := reconciliationInput([]diagnostic.Result{
		reconciliationResult("image-pull", "v1", reconciliationClaim(
			"image-pull-failure", diagnostic.ClaimFailureMechanism,
			"The target container could not retrieve its configured image.", "pod-image", "event-image")),
	}, reconciliationEvidence("event-image", "pod-image"))

	result := ReconcileDiagnosticCandidates(input)
	if result.Status != ReconciliationCompleted || len(result.Records) != 1 {
		t.Fatalf("expected completed image-pull reconciliation, got %#v", result)
	}
	if result.Records[0].DiagnosticID != "image-pull" || result.Records[0].Candidate.TargetUID != "pod-image" {
		t.Fatalf("candidate identity was not preserved: %#v", result.Records[0])
	}
	if len(result.Accounting.Resolutions) != 1 || result.Accounting.Resolutions[0].Disposition != diagnostic.CandidateDispositionProposedClaim {
		t.Fatalf("expected proposed image-pull claim, got %#v", result.Accounting)
	}
}

func TestReconcileDiagnosticCandidatesPreservesSchedulingPodUID(t *testing.T) {
	input := reconciliationInput([]diagnostic.Result{
		reconciliationResult("scheduling", "v1", reconciliationClaim(
			"scheduling-mechanism-unschedulable/pod-1", diagnostic.ClaimFailureMechanism,
			"The scheduler reported that the Pod could not be scheduled.", "pod-1", "event-scheduling")),
	}, reconciliationEvidence("event-scheduling", "pod-1"))

	result := ReconcileDiagnosticCandidates(input)
	if len(result.Records) != 1 || result.Records[0].Candidate.TargetUID != "pod-1" {
		t.Fatalf("expected scheduling Pod UID to survive mapping, got %#v", result)
	}
}

func TestReconcileDiagnosticCandidatesAccountsEveryMultiDiagnosticClaim(t *testing.T) {
	input := reconciliationInput([]diagnostic.Result{
		reconciliationResult("image-pull", "v1", reconciliationClaim("image-pull-failure", diagnostic.ClaimFailureMechanism, "The image cannot be retrieved.", "pod-1", "event-image")),
		reconciliationResult("scheduling", "v1", reconciliationClaim("scheduling-failure", diagnostic.ClaimFailureMechanism, "The Pod cannot be scheduled.", "pod-2", "event-scheduling")),
	}, reconciliationEvidence("event-image", "pod-1"), reconciliationEvidence("event-scheduling", "pod-2"))

	result := ReconcileDiagnosticCandidates(input)
	if result.Status != ReconciliationCompleted || len(result.Accounting.Resolutions) != 2 || result.Accounting.Summary.Total != 2 {
		t.Fatalf("expected one resolution per diagnostic candidate, got %#v", result)
	}
	if result.Accounting.Summary.DispositionCoverage != 1 {
		t.Fatalf("expected complete disposition coverage, got %#v", result.Accounting.Summary)
	}
}

func TestReconcileDiagnosticCandidatesRejectsUnauthorizedEvidenceWithoutExposure(t *testing.T) {
	evidence := reconciliationEvidence("event-1", "pod-1")
	evidence.Authorized = false
	result := ReconcileDiagnosticCandidates(reconciliationInput([]diagnostic.Result{
		reconciliationResult("scheduling", "v1", reconciliationClaim("candidate-1", diagnostic.ClaimRootCause, "A candidate root cause.", "pod-1", "event-1")),
	}, evidence))

	if result.Status != ReconciliationCompleted || len(result.Accounting.Resolutions) != 1 {
		t.Fatalf("unauthorized evidence must be accounted, got %#v", result)
	}
	resolution := result.Accounting.Resolutions[0]
	if resolution.Disposition != diagnostic.CandidateDispositionRejected || resolution.Reason != "EvidenceUnauthorized" {
		t.Fatalf("expected unauthorized rejection, got %#v", resolution)
	}
	if strings.Contains(result.Error, "event-1") || strings.Contains(result.Error, "pod-1") {
		t.Fatalf("adapter error unexpectedly exposed evidence identity: %q", result.Error)
	}
}

func TestReconcileDiagnosticCandidatesRejectsUIDAndWindowMismatch(t *testing.T) {
	evidence := reconciliationEvidence("event-1", "pod-other")
	result := ReconcileDiagnosticCandidates(reconciliationInput([]diagnostic.Result{
		reconciliationResult("scheduling", "v1", reconciliationClaim("candidate-1", diagnostic.ClaimRootCause, "A candidate root cause.", "pod-1", "event-1")),
	}, evidence))
	if result.Accounting.Resolutions[0].Reason != "EvidenceTargetUIDMismatch" {
		t.Fatalf("expected UID mismatch rejection, got %#v", result.Accounting.Resolutions[0])
	}

	evidence = reconciliationEvidence("event-1", "pod-1")
	evidence.IncidentStart = reconciliationWindowEnd.Add(time.Hour)
	evidence.IncidentEnd = evidence.IncidentStart.Add(time.Minute)
	result = ReconcileDiagnosticCandidates(reconciliationInput([]diagnostic.Result{
		reconciliationResult("scheduling", "v1", reconciliationClaim("candidate-1", diagnostic.ClaimRootCause, "A candidate root cause.", "pod-1", "event-1")),
	}, evidence))
	if result.Accounting.Resolutions[0].Reason != "EvidenceOutsideIncidentWindow" {
		t.Fatalf("expected incident window rejection, got %#v", result.Accounting.Resolutions[0])
	}
}

func TestReconcileDiagnosticCandidatesDeduplicatesSameLogicalClaim(t *testing.T) {
	statement := "The scheduler reported insufficient CPU for the Pod."
	result := ReconcileDiagnosticCandidates(reconciliationInput([]diagnostic.Result{
		reconciliationResult("provider", "v1", reconciliationClaim("provider-candidate", diagnostic.ClaimRootCause, statement, "pod-1", "event-provider")),
		reconciliationResult("scheduling", "v1", reconciliationClaim("scheduling-candidate", diagnostic.ClaimRootCause, statement, "pod-1", "event-scheduling")),
	}, reconciliationEvidence("event-provider", "pod-1"), reconciliationEvidence("event-scheduling", "pod-1")))

	if result.Accounting.Summary.ProposedClaimCount != 1 || result.Accounting.Summary.DeduplicatedCount != 1 {
		t.Fatalf("expected one canonical claim and one duplicate, got %#v", result.Accounting.Summary)
	}
	for _, resolution := range result.Accounting.Resolutions {
		if resolution.Disposition == diagnostic.CandidateDispositionDeduplicated && resolution.DuplicateOf == "" {
			t.Fatalf("deduplicated result lost DuplicateOf: %#v", resolution)
		}
	}
}

func TestReconcileDiagnosticCandidatesDoesNotDoubleWeightDuplicateEvidence(t *testing.T) {
	result := ReconcileDiagnosticCandidates(reconciliationInput([]diagnostic.Result{
		reconciliationResult("scheduling", "v1", reconciliationClaim("candidate-1", diagnostic.ClaimRootCause, "The scheduler reported insufficient CPU for the Pod.", "pod-1", "event-core", "event-api")),
	}, reconciliationEvidence("event-core", "pod-1"), reconciliationEvidence("event-api", "pod-1")))
	if result.Accounting.Summary.ProposedClaimCount != 1 || len(result.Accounting.Resolutions) != 1 {
		t.Fatalf("duplicate provider references must not create extra claims, got %#v", result.Accounting)
	}
}

func TestReconcileDiagnosticCandidatesFailsWhenAdapterPreconditionsFail(t *testing.T) {
	tests := []struct {
		name  string
		input CandidateReconciliationInput
		want  string
	}{
		{
			name: "missing diagnostic identity",
			input: reconciliationInput([]diagnostic.Result{
				reconciliationResult("", "v1", reconciliationClaim("candidate-1", diagnostic.ClaimObservation, "An observation.", "pod-1", "event-1")),
			}, reconciliationEvidence("event-1", "pod-1")),
			want: reconciliationErrorMissingDiagnosticIdentity,
		},
		{
			name: "inventory unavailable",
			input: func() CandidateReconciliationInput {
				input := reconciliationInput([]diagnostic.Result{
					reconciliationResult("scheduling", "v1", reconciliationClaim("candidate-1", diagnostic.ClaimObservation, "An observation.", "pod-1", "event-1")),
				}, reconciliationEvidence("event-1", "pod-1"))
				input.EvidenceInventoryReady = false
				return input
			}(),
			want: reconciliationErrorEvidenceInventory,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ReconcileDiagnosticCandidates(tt.input)
			if result.Status != ReconciliationFailed || result.Error != tt.want {
				t.Fatalf("expected reconciliation failure %q, got %#v", tt.want, result)
			}
			if result.Status == ReconciliationNoCandidates {
				t.Fatalf("adapter failure must not be reported as NoCandidates")
			}
		})
	}
}

func TestReconcileDiagnosticCandidatesReturnsNoCandidatesWithUnknownCoverage(t *testing.T) {
	result := ReconcileDiagnosticCandidates(CandidateReconciliationInput{EvidenceInventoryReady: false})
	if result.Status != ReconciliationNoCandidates || result.Accounting.Summary.CoverageKnown {
		t.Fatalf("expected NoCandidates with unknown coverage, got %#v", result)
	}
}

func TestReconcileDiagnosticCandidatesIsDeterministicAcrossInputOrder(t *testing.T) {
	first := reconciliationResult("scheduling", "v1", reconciliationClaim("candidate-a", diagnostic.ClaimRootCause, "The scheduler reported insufficient CPU for the Pod.", "pod-1", "event-a"))
	second := reconciliationResult("provider", "v1", reconciliationClaim("candidate-b", diagnostic.ClaimRootCause, "The scheduler reported insufficient CPU for the Pod.", "pod-1", "event-b"))
	evidence := []diagnostic.EvidenceIdentity{reconciliationEvidence("event-a", "pod-1"), reconciliationEvidence("event-b", "pod-1")}
	left := ReconcileDiagnosticCandidates(reconciliationInput([]diagnostic.Result{first, second}, evidence...))
	right := ReconcileDiagnosticCandidates(reconciliationInput([]diagnostic.Result{second, first}, evidence[1], evidence[0]))
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("reconciliation changed with input order:\nleft=%#v\nright=%#v", left, right)
	}
}
