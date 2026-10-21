package investigation

import (
	"sort"
	"strings"
	"time"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/diagnostic"
	"github.com/FluxSeer/fluxseer-rca/internal/verifier"
)

// ShadowVerificationStatus is deliberately separate from the public RCA
// verdict. Phase 3A-3 verifies reconciled candidates without making the
// result authoritative for controllers or reports.
type ShadowVerificationStatus string

const (
	ShadowVerificationCompleted        ShadowVerificationStatus = "ShadowVerificationCompleted"
	ShadowVerificationFailed           ShadowVerificationStatus = "ShadowVerificationFailed"
	ShadowVerificationNoEligibleClaims ShadowVerificationStatus = "NoEligibleClaims"
)

const (
	shadowVerificationSkipped             = "Skipped"
	shadowVerificationErrorReconciliation = "ReconciliationNotCompleted"
	shadowVerificationErrorEvidence       = "TrustedEvidenceUnavailable"
)

// ShadowClaimVerification carries candidate lineage alongside the existing
// verifier result. It is internal and intentionally not serialized to the
// public RCA report in Phase 3A-3.
type ShadowClaimVerification struct {
	CandidateID          string
	CandidateFingerprint string
	ClaimFingerprint     string
	TargetUID            string
	Statement            string
	VerifierStatement    string
	Disposition          diagnostic.CandidateDisposition
	DuplicateOf          string
	Verification         string
	EvidenceRefs         []string
	EvidenceLinks        []verifier.EvidenceLink
	Reason               string
}

type ShadowVerificationResult struct {
	Status        ShadowVerificationStatus
	Claims        []ShadowClaimVerification
	CoverageScore float64
	Method        string
	Error         string
}

type ShadowVerificationInput struct {
	Reconciliation CandidateReconciliationResult
	EvidenceRefs   []v1alpha1.EvidenceRef
}

// VerifyReconciledCandidates sends only ProposedClaim resolutions through the
// existing verifier. Every other disposition remains explicitly skipped and
// retains its reconciliation lineage. The function has no side effects and
// never changes the canonical RCA result.
func VerifyReconciledCandidates(input ShadowVerificationInput) ShadowVerificationResult {
	if input.Reconciliation.Status == ReconciliationNoCandidates {
		return ShadowVerificationResult{Status: ShadowVerificationNoEligibleClaims}
	}
	if input.Reconciliation.Status != ReconciliationCompleted {
		return ShadowVerificationResult{Status: ShadowVerificationFailed, Error: shadowVerificationErrorReconciliation}
	}

	records := recordsByCandidateID(input.Reconciliation.Records)
	refs, refErr := trustedVerifierEvidence(input.EvidenceRefs)
	if refErr != "" {
		return ShadowVerificationResult{Status: ShadowVerificationFailed, Error: shadowVerificationErrorEvidence}
	}

	resolutions := append([]diagnostic.CandidateResolution(nil), input.Reconciliation.Accounting.Resolutions...)
	sort.SliceStable(resolutions, func(i, j int) bool {
		if resolutions[i].CandidateFingerprint != resolutions[j].CandidateFingerprint {
			return resolutions[i].CandidateFingerprint < resolutions[j].CandidateFingerprint
		}
		return resolutions[i].CandidateID < resolutions[j].CandidateID
	})
	claims := make([]ShadowClaimVerification, 0, len(resolutions))
	proposed := 0
	supported := 0
	method := ""
	failed := false
	for _, resolution := range resolutions {
		record, exists := records[resolution.CandidateID]
		if !exists {
			failed = true
			claims = append(claims, ShadowClaimVerification{
				CandidateID:          resolution.CandidateID,
				CandidateFingerprint: resolution.CandidateFingerprint,
				ClaimFingerprint:     resolution.ClaimFingerprint,
				Disposition:          resolution.Disposition,
				DuplicateOf:          resolution.DuplicateOf,
				Verification:         shadowVerificationSkipped,
				Reason:               shadowVerificationErrorEvidence,
			})
			continue
		}

		entry := ShadowClaimVerification{
			CandidateID:          resolution.CandidateID,
			CandidateFingerprint: resolution.CandidateFingerprint,
			ClaimFingerprint:     resolution.ClaimFingerprint,
			TargetUID:            record.Candidate.TargetUID,
			Statement:            record.Candidate.Statement,
			Disposition:          resolution.Disposition,
			DuplicateOf:          resolution.DuplicateOf,
			Verification:         shadowVerificationSkipped,
			Reason:               resolution.Reason,
		}
		if resolution.Disposition != diagnostic.CandidateDispositionProposedClaim {
			claims = append(claims, entry)
			continue
		}

		proposed++
		candidateRefs, ok := evidenceForCandidate(record, refs)
		if !ok {
			failed = true
			entry.Reason = shadowVerificationErrorEvidence
			claims = append(claims, entry)
			continue
		}
		entry.VerifierStatement = verifierStatement(record)
		verification := verifier.VerifyClaims([]verifier.Claim{{ID: record.Candidate.ID, Statement: entry.VerifierStatement}}, candidateRefs)
		if method == "" {
			method = verification.Method
		}
		if len(verification.Claims) == 0 {
			failed = true
			entry.Reason = shadowVerificationErrorEvidence
			claims = append(claims, entry)
			continue
		}
		verified := verification.Claims[0]
		entry.Verification = verified.Verification
		entry.EvidenceRefs = append([]string(nil), verified.EvidenceRefs...)
		entry.EvidenceLinks = append([]verifier.EvidenceLink(nil), verified.EvidenceLinks...)
		if verified.Verification == verifier.VerificationSupported {
			supported++
		}
		claims = append(claims, entry)
	}

	status := ShadowVerificationCompleted
	if failed {
		status = ShadowVerificationFailed
	}
	coverage := 0.0
	if proposed > 0 {
		coverage = float64(supported) / float64(proposed)
	}
	return ShadowVerificationResult{Status: status, Claims: claims, CoverageScore: coverage, Method: method}
}

func recordsByCandidateID(records []diagnostic.CandidateRecord) map[string]diagnostic.CandidateRecord {
	result := make(map[string]diagnostic.CandidateRecord, len(records))
	for _, record := range records {
		id := strings.TrimSpace(record.Candidate.ID)
		if id == "" {
			continue
		}
		if _, exists := result[id]; !exists {
			result[id] = record
		}
	}
	return result
}

type verifierEvidenceEntry struct {
	ref verifier.EvidenceRef
}

func trustedVerifierEvidence(refs []v1alpha1.EvidenceRef) (map[string]verifierEvidenceEntry, string) {
	entries := make(map[string]verifierEvidenceEntry, len(refs))
	for _, source := range refs {
		id := strings.TrimSpace(source.ID)
		if id == "" {
			continue
		}
		candidate := verifier.EvidenceRef{ID: id, Kind: source.Kind, Source: source.Source, Summary: source.Summary, Reason: source.Reason}
		if existing, exists := entries[id]; exists {
			if existing.ref.Kind != candidate.Kind || existing.ref.Source != candidate.Source || existing.ref.Summary != candidate.Summary || existing.ref.Reason != candidate.Reason {
				return nil, shadowVerificationErrorEvidence
			}
			continue
		}
		entries[id] = verifierEvidenceEntry{ref: candidate}
	}
	return entries, ""
}

func evidenceForCandidate(record diagnostic.CandidateRecord, refs map[string]verifierEvidenceEntry) ([]verifier.EvidenceRef, bool) {
	identityByID := make(map[string][]diagnostic.EvidenceIdentity)
	for _, identity := range record.Evidence {
		id := strings.TrimSpace(identity.ID)
		if id == "" {
			continue
		}
		identityByID[id] = append(identityByID[id], identity)
	}
	result := make([]verifier.EvidenceRef, 0, len(record.Candidate.EvidenceRefs))
	seen := make(map[string]struct{}, len(record.Candidate.EvidenceRefs))
	for _, refID := range record.Candidate.EvidenceRefs {
		refID = strings.TrimSpace(refID)
		if refID == "" {
			return nil, false
		}
		if _, duplicate := seen[refID]; duplicate {
			continue
		}
		seen[refID] = struct{}{}
		entry, exists := refs[refID]
		if !exists {
			return nil, false
		}
		identities := identityByID[refID]
		if len(identities) != 1 {
			return nil, false
		}
		identity := identities[0]
		if !identity.Authorized || !identity.Valid || identity.Incomplete || identity.TargetUID == "" || identity.TargetUID != record.Candidate.TargetUID || !shadowIncidentWindowCompatible(record.IncidentStart, record.IncidentEnd, identity.IncidentStart, identity.IncidentEnd) {
			return nil, false
		}
		result = append(result, entry.ref)
	}
	if len(result) == 0 {
		return nil, false
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, true
}

func shadowIncidentWindowCompatible(candidateStart, candidateEnd, evidenceStart, evidenceEnd time.Time) bool {
	if candidateStart.IsZero() && candidateEnd.IsZero() && evidenceStart.IsZero() && evidenceEnd.IsZero() {
		return true
	}
	if candidateStart.IsZero() || candidateEnd.IsZero() || evidenceStart.IsZero() || evidenceEnd.IsZero() {
		return false
	}
	if candidateEnd.Before(candidateStart) || evidenceEnd.Before(evidenceStart) {
		return false
	}
	return !candidateEnd.Before(evidenceStart) && !evidenceEnd.Before(candidateStart)
}

func verifierStatement(record diagnostic.CandidateRecord) string {
	// The existing verifier's scheduling domain profile is keyed to the
	// canonical mechanism name. Keep the original candidate statement in the
	// lineage while using the canonical verifier vocabulary in Shadow Mode.
	if record.DiagnosticID == "scheduling" && record.Candidate.Type == diagnostic.ClaimFailureMechanism && strings.HasPrefix(record.Candidate.ID, "scheduling-mechanism-unschedulable/") {
		return "SchedulingUnschedulable is confirmed by UID-correlated scheduler condition or FailedScheduling evidence."
	}
	return record.Candidate.Statement
}
