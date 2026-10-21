package diagnostic

import (
	"testing"
	"time"
)

var accountingWindowStart = time.Date(2026, 10, 21, 1, 0, 0, 0, time.UTC)
var accountingWindowEnd = accountingWindowStart.Add(15 * time.Minute)

func accountingEvidence(id, targetUID string) EvidenceIdentity {
	return EvidenceIdentity{
		ID:            id,
		TargetUID:     targetUID,
		IncidentStart: accountingWindowStart,
		IncidentEnd:   accountingWindowEnd,
		Authorized:    true,
		Valid:         true,
	}
}

func accountingCandidate(id, targetUID, statement string, refs ...string) CandidateRecord {
	return CandidateRecord{
		DiagnosticID:      "scheduling",
		DiagnosticVersion: "v1",
		IncidentStart:     accountingWindowStart,
		IncidentEnd:       accountingWindowEnd,
		Candidate: CandidateClaim{
			ID:           id,
			Type:         ClaimRootCause,
			Statement:    statement,
			TargetUID:    targetUID,
			EvidenceRefs: refs,
		},
	}
}

func withAccountingEvidence(candidate CandidateRecord, evidence ...EvidenceIdentity) CandidateRecord {
	candidate.Evidence = evidence
	return candidate
}

func TestAccountCandidatesSingleValidCandidateIsProposed(t *testing.T) {
	candidate := withAccountingEvidence(
		accountingCandidate("candidate-1", "pod-1", "The scheduler reported insufficient memory for the Pod.", "event-1"),
		accountingEvidence("event-1", "pod-1"),
	)
	result := AccountCandidates([]CandidateRecord{candidate})

	if len(result.Resolutions) != 1 || result.Resolutions[0].Disposition != CandidateDispositionProposedClaim {
		t.Fatalf("expected one ProposedClaim resolution, got %#v", result)
	}
	if result.Summary.Total != 1 || !result.Summary.CoverageKnown || result.Summary.DispositionCoverage != 1 {
		t.Fatalf("expected complete accounting coverage, got %#v", result.Summary)
	}
}

func TestAccountCandidatesMissingEvidenceRetainsHypothesis(t *testing.T) {
	candidate := accountingCandidate("candidate-1", "pod-1", "The scheduler reported insufficient memory for the Pod.")
	result := AccountCandidates([]CandidateRecord{candidate})

	if len(result.Resolutions) != 1 || result.Resolutions[0].Disposition != CandidateDispositionRetainedHypothesis {
		t.Fatalf("expected missing evidence to retain a hypothesis, got %#v", result)
	}
}

func TestAccountCandidatesDistinguishesIncompleteFromInvalidEvidence(t *testing.T) {
	incomplete := accountingEvidence("event-incomplete", "pod-1")
	incomplete.Incomplete = true
	retained := AccountCandidates([]CandidateRecord{withAccountingEvidence(
		accountingCandidate("candidate-incomplete", "pod-1", "The scheduler reported insufficient memory for the Pod.", "event-incomplete"),
		incomplete,
	)})
	if retained.Resolutions[0].Disposition != CandidateDispositionRetainedHypothesis || retained.Resolutions[0].Reason != "EvidenceIncomplete" {
		t.Fatalf("expected incomplete evidence to retain a hypothesis, got %#v", retained)
	}

	invalid := accountingEvidence("event-invalid", "pod-1")
	invalid.Valid = false
	rejected := AccountCandidates([]CandidateRecord{withAccountingEvidence(
		accountingCandidate("candidate-invalid", "pod-1", "The scheduler reported insufficient memory for the Pod.", "event-invalid"),
		invalid,
	)})
	if rejected.Resolutions[0].Disposition != CandidateDispositionRejected || rejected.Resolutions[0].Reason != "EvidenceInvalid" {
		t.Fatalf("expected invalid evidence to reject candidate, got %#v", rejected)
	}
}

func TestAccountCandidatesRejectsUnauthorizedAndMismatchedEvidence(t *testing.T) {
	tests := []struct {
		name   string
		effect func(CandidateRecord) CandidateRecord
		want   string
	}{
		{
			name: "unauthorized",
			effect: func(candidate CandidateRecord) CandidateRecord {
				evidence := accountingEvidence("event-1", "pod-1")
				evidence.Authorized = false
				return withAccountingEvidence(candidate, evidence)
			},
			want: "EvidenceUnauthorized",
		},
		{
			name: "uid mismatch",
			effect: func(candidate CandidateRecord) CandidateRecord {
				return withAccountingEvidence(candidate, accountingEvidence("event-1", "pod-2"))
			},
			want: "EvidenceTargetUIDMismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := accountingCandidate("candidate-1", "pod-1", "The scheduler reported insufficient memory for the Pod.", "event-1")
			result := AccountCandidates([]CandidateRecord{tt.effect(candidate)})
			if len(result.Resolutions) != 1 || result.Resolutions[0].Disposition != CandidateDispositionRejected || result.Resolutions[0].Reason != tt.want {
				t.Fatalf("expected rejected candidate with reason %q, got %#v", tt.want, result)
			}
		})
	}
}

func TestAccountCandidatesDeduplicatesSameClaimAcrossDiagnostics(t *testing.T) {
	first := withAccountingEvidence(
		accountingCandidate("candidate-a", "pod-1", "The scheduler reported insufficient memory for the Pod.", "event-a"),
		accountingEvidence("event-a", "pod-1"),
	)
	second := withAccountingEvidence(
		accountingCandidate("candidate-b", "pod-1", "The scheduler reported insufficient memory for the Pod.", "event-b"),
		accountingEvidence("event-b", "pod-1"),
	)
	second.DiagnosticID = "provider"
	second.DiagnosticVersion = "v1"
	result := AccountCandidates([]CandidateRecord{second, first})

	if len(result.Resolutions) != 2 {
		t.Fatalf("expected two resolutions, got %#v", result)
	}
	proposed, deduplicated := 0, 0
	for _, resolution := range result.Resolutions {
		switch resolution.Disposition {
		case CandidateDispositionProposedClaim:
			proposed++
		case CandidateDispositionDeduplicated:
			deduplicated++
			if resolution.DuplicateOf == "" || resolution.CanonicalClaimID == "" {
				t.Fatalf("deduplicated candidate lost canonical linkage: %#v", resolution)
			}
		}
	}
	if proposed != 1 || deduplicated != 1 {
		t.Fatalf("expected one proposed and one deduplicated candidate, got %#v", result)
	}
}

func TestAccountCandidatesDoesNotMergeDifferentPodUIDs(t *testing.T) {
	first := withAccountingEvidence(
		accountingCandidate("candidate-a", "pod-1", "The scheduler reported insufficient memory for the Pod.", "event-a"),
		accountingEvidence("event-a", "pod-1"),
	)
	second := withAccountingEvidence(
		accountingCandidate("candidate-b", "pod-2", "The scheduler reported insufficient memory for the Pod.", "event-b"),
		accountingEvidence("event-b", "pod-2"),
	)
	result := AccountCandidates([]CandidateRecord{first, second})

	for _, resolution := range result.Resolutions {
		if resolution.Disposition != CandidateDispositionProposedClaim {
			t.Fatalf("different Pod UIDs must not be deduplicated, got %#v", result)
		}
	}
}

func TestAccountCandidatesRejectsCandidateIDConflict(t *testing.T) {
	first := withAccountingEvidence(
		accountingCandidate("candidate-1", "pod-1", "The scheduler reported insufficient memory for the Pod.", "event-a"),
		accountingEvidence("event-a", "pod-1"),
	)
	second := withAccountingEvidence(
		accountingCandidate("candidate-1", "pod-1", "The scheduler reported an untolerated Node taint for the Pod.", "event-b"),
		accountingEvidence("event-b", "pod-1"),
	)
	result := AccountCandidates([]CandidateRecord{first, second})

	if len(result.Resolutions) != 2 {
		t.Fatalf("expected both conflicting inputs to be accounted, got %#v", result)
	}
	for _, resolution := range result.Resolutions {
		if resolution.Disposition != CandidateDispositionRejected || resolution.Reason != "CandidateIdentityConflict" {
			t.Fatalf("expected identity conflict rejection, got %#v", result)
		}
	}
}

func TestAccountCandidatesRejectsMissingCandidateID(t *testing.T) {
	candidate := withAccountingEvidence(
		accountingCandidate("candidate-1", "pod-1", "The scheduler reported insufficient memory for the Pod.", "event-1"),
		accountingEvidence("event-1", "pod-1"),
	)
	candidate.Candidate.ID = ""
	result := AccountCandidates([]CandidateRecord{candidate})
	if result.Resolutions[0].Disposition != CandidateDispositionRejected || result.Resolutions[0].Reason != "MissingCandidateID" {
		t.Fatalf("expected missing CandidateID rejection, got %#v", result)
	}
}

func TestAccountCandidatesRejectsEvidenceOutsideIncidentWindow(t *testing.T) {
	evidence := accountingEvidence("event-1", "pod-1")
	evidence.IncidentStart = accountingWindowEnd.Add(time.Minute)
	evidence.IncidentEnd = evidence.IncidentStart.Add(time.Minute)
	candidate := withAccountingEvidence(
		accountingCandidate("candidate-1", "pod-1", "The scheduler reported insufficient memory for the Pod.", "event-1"),
		evidence,
	)
	result := AccountCandidates([]CandidateRecord{candidate})
	if result.Resolutions[0].Disposition != CandidateDispositionRejected || result.Resolutions[0].Reason != "EvidenceOutsideIncidentWindow" {
		t.Fatalf("expected out-of-window evidence rejection, got %#v", result)
	}
}

func TestFingerprintSeparatesCandidateIdentityFromClaimIdentity(t *testing.T) {
	first := withAccountingEvidence(
		accountingCandidate("candidate-a", "pod-1", "The scheduler reported insufficient memory for the Pod.", "event-a"),
		accountingEvidence("event-a", "pod-1"),
	)
	second := withAccountingEvidence(
		accountingCandidate("candidate-b", "pod-1", " the scheduler reported   insufficient memory for the pod. ", "event-b"),
		accountingEvidence("event-b", "pod-1"),
	)
	second.DiagnosticID = "provider"

	if FingerprintClaim(first) != FingerprintClaim(second) {
		t.Fatalf("equivalent logical claims should share ClaimFingerprint")
	}
	if FingerprintCandidate(first) == FingerprintCandidate(second) {
		t.Fatalf("different diagnostic candidates should not share CandidateFingerprint")
	}
}

func TestAccountCandidatesKeepsDuplicateInputAccounting(t *testing.T) {
	candidate := withAccountingEvidence(
		accountingCandidate("candidate-1", "pod-1", "The scheduler reported insufficient memory for the Pod.", "event-1"),
		accountingEvidence("event-1", "pod-1"),
	)
	result := AccountCandidates([]CandidateRecord{candidate, candidate})
	if len(result.Resolutions) != 2 || result.Summary.ProposedClaimCount != 1 || result.Summary.DeduplicatedCount != 1 {
		t.Fatalf("expected duplicate inputs to remain accounted, got %#v", result)
	}
}

func TestAccountCandidatesIsDeterministicAcrossInputOrder(t *testing.T) {
	first := withAccountingEvidence(
		accountingCandidate("candidate-a", "pod-1", "The scheduler reported insufficient memory for the Pod.", "event-a"),
		accountingEvidence("event-a", "pod-1"),
	)
	second := withAccountingEvidence(
		accountingCandidate("candidate-b", "pod-1", "The scheduler reported insufficient memory for the Pod.", "event-b"),
		accountingEvidence("event-b", "pod-1"),
	)
	left := AccountCandidates([]CandidateRecord{first, second})
	right := AccountCandidates([]CandidateRecord{second, first})

	if !equalAccountingResults(left, right) {
		t.Fatalf("expected deterministic accounting, left=%#v right=%#v", left, right)
	}
}

func TestAccountCandidatesEmptySetIsNotSuccessfulCoverage(t *testing.T) {
	result := AccountCandidates(nil)
	if result.Summary.Total != 0 || result.Summary.CoverageKnown {
		t.Fatalf("expected empty accounting coverage to be N/A, got %#v", result.Summary)
	}
}

func TestAccountCandidatesRejectedCandidateCannotBeWashedIntoValidDeduplication(t *testing.T) {
	valid := withAccountingEvidence(
		accountingCandidate("candidate-valid", "pod-1", "The scheduler reported insufficient memory for the Pod.", "event-valid"),
		accountingEvidence("event-valid", "pod-1"),
	)
	unauthorized := withAccountingEvidence(
		accountingCandidate("candidate-unauthorized", "pod-1", "The scheduler reported insufficient memory for the Pod.", "event-unauthorized"),
		func() EvidenceIdentity {
			evidence := accountingEvidence("event-unauthorized", "pod-1")
			evidence.Authorized = false
			return evidence
		}(),
	)
	result := AccountCandidates([]CandidateRecord{unauthorized, valid})

	if len(result.Resolutions) != 2 {
		t.Fatalf("expected both candidates to be accounted, got %#v", result)
	}
	for _, resolution := range result.Resolutions {
		if resolution.CandidateID == "candidate-unauthorized" && resolution.Disposition != CandidateDispositionRejected {
			t.Fatalf("unauthorized candidate was washed into a valid disposition: %#v", result)
		}
	}
}

func equalAccountingResults(left, right AccountingResult) bool {
	if left.Summary != right.Summary || len(left.Resolutions) != len(right.Resolutions) {
		return false
	}
	for index := range left.Resolutions {
		if left.Resolutions[index] != right.Resolutions[index] {
			return false
		}
	}
	return true
}
