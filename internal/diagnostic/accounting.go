package diagnostic

import (
	"sort"
	"strings"
	"time"

	"github.com/FluxSeer/fluxseer-rca/internal/canonicaldigest"
)

const candidateAccountingCanonicalization = "fluxseer-rca-diagnostic-candidate-accounting-v1"

// CandidateDisposition describes how a diagnostic candidate was accounted for.
// It is intentionally independent from claim verification and the final RCA
// verdict.
type CandidateDisposition string

const (
	CandidateDispositionProposedClaim      CandidateDisposition = "ProposedClaim"
	CandidateDispositionRetainedHypothesis CandidateDisposition = "RetainedHypothesis"
	CandidateDispositionRejected           CandidateDisposition = "Rejected"
	CandidateDispositionDeduplicated       CandidateDisposition = "Deduplicated"
)

// EvidenceIdentity is the minimum provenance and authorization context needed
// by pure candidate accounting. The runtime adapter constructs these records
// from already filtered evidence; accounting never reads a provider.
type EvidenceIdentity struct {
	ID            string
	TargetUID     string
	IncidentStart time.Time
	IncidentEnd   time.Time
	Authorized    bool
	Valid         bool
	Incomplete    bool
}

// CandidateRecord attaches a candidate to the diagnostic and incident context
// that produced it. It is an internal adapter boundary and does not change the
// public CandidateClaim or CRD schema.
type CandidateRecord struct {
	DiagnosticID      string
	DiagnosticVersion string
	IncidentStart     time.Time
	IncidentEnd       time.Time
	Candidate         CandidateClaim
	Evidence          []EvidenceIdentity
}

type CandidateResolution struct {
	CandidateID          string
	CandidateFingerprint string
	ClaimFingerprint     string
	Disposition          CandidateDisposition
	CanonicalClaimID     string
	DuplicateOf          string
	Reason               string
}

type DispositionSummary struct {
	Total                   int
	ProposedClaimCount      int
	RetainedHypothesisCount int
	RejectedCandidateCount  int
	DeduplicatedCount       int
	DispositionCoverage     float64
	CoverageKnown           bool
}

type AccountingResult struct {
	Resolutions []CandidateResolution
	Summary     DispositionSummary
}

type accountingItem struct {
	record      CandidateRecord
	res         CandidateResolution
	state       CandidateDisposition
	reason      string
	eligible    bool
	identityKey string
}

// AccountCandidates validates, fingerprints, and deterministically accounts
// for every input candidate. It performs no claim verification and no metric
// mutation. Invalid or unauthorized evidence is rejected before deduplication;
// incomplete evidence remains an explicit hypothesis.
func AccountCandidates(records []CandidateRecord) AccountingResult {
	items := make([]accountingItem, len(records))
	for index, record := range records {
		item := accountingItem{record: normalizeCandidateRecord(record)}
		item.res = CandidateResolution{
			CandidateID:          item.record.Candidate.ID,
			CandidateFingerprint: FingerprintCandidate(item.record),
			ClaimFingerprint:     FingerprintClaim(item.record),
		}
		item.identityKey = item.record.Candidate.ID
		item.state, item.reason, item.eligible = validateCandidate(item.record)
		item.res.Disposition = item.state
		item.res.Reason = item.reason
		items[index] = item
	}

	// A CandidateID is an identity assertion. Reusing it for different content
	// is an identity conflict, not a deduplication opportunity.
	identityFingerprints := make(map[string]map[string]struct{})
	for _, item := range items {
		if item.identityKey == "" {
			continue
		}
		if identityFingerprints[item.identityKey] == nil {
			identityFingerprints[item.identityKey] = map[string]struct{}{}
		}
		identityFingerprints[item.identityKey][item.res.CandidateFingerprint] = struct{}{}
	}
	for index := range items {
		fingerprints := identityFingerprints[items[index].identityKey]
		if items[index].identityKey != "" && len(fingerprints) > 1 {
			items[index].state = CandidateDispositionRejected
			items[index].reason = "CandidateIdentityConflict"
			items[index].eligible = false
			items[index].res.Disposition = items[index].state
			items[index].res.Reason = items[index].reason
		}
	}

	// Only complete, authorized candidates participate in deduplication. This
	// prevents a weak or unauthorized candidate from being silently absorbed by
	// a valid one while still preserving its own accounting result.
	byClaim := make(map[string][]int)
	for index, item := range items {
		if item.eligible {
			byClaim[item.res.ClaimFingerprint] = append(byClaim[item.res.ClaimFingerprint], index)
		}
	}
	for _, indexes := range byClaim {
		sort.SliceStable(indexes, func(i, j int) bool {
			left, right := items[indexes[i]], items[indexes[j]]
			if left.res.CandidateFingerprint != right.res.CandidateFingerprint {
				return left.res.CandidateFingerprint < right.res.CandidateFingerprint
			}
			return left.res.CandidateID < right.res.CandidateID
		})
		canonical := items[indexes[0]]
		for position, index := range indexes {
			if position == 0 {
				items[index].state = CandidateDispositionProposedClaim
				items[index].res.Disposition = CandidateDispositionProposedClaim
				items[index].res.CanonicalClaimID = canonical.res.CandidateID
				items[index].res.Reason = "CandidateAcceptedForVerification"
				continue
			}
			items[index].state = CandidateDispositionDeduplicated
			items[index].res.Disposition = CandidateDispositionDeduplicated
			items[index].res.CanonicalClaimID = canonical.res.CandidateID
			items[index].res.DuplicateOf = canonical.res.CandidateID
			items[index].res.Reason = "DuplicateClaimFingerprint"
		}
	}

	resolutions := make([]CandidateResolution, 0, len(items))
	for _, item := range items {
		resolutions = append(resolutions, item.res)
	}
	sort.SliceStable(resolutions, func(i, j int) bool {
		left, right := resolutions[i], resolutions[j]
		if left.CandidateFingerprint != right.CandidateFingerprint {
			return left.CandidateFingerprint < right.CandidateFingerprint
		}
		if left.CandidateID != right.CandidateID {
			return left.CandidateID < right.CandidateID
		}
		return left.Disposition < right.Disposition
	})

	return AccountingResult{
		Resolutions: resolutions,
		Summary:     summarizeDispositions(resolutions),
	}
}

// FingerprintCandidate identifies a specific diagnostic-produced candidate.
// Evidence identity is included, while raw evidence payload is intentionally
// excluded from the fingerprint.
func FingerprintCandidate(record CandidateRecord) string {
	evidence := make([]string, 0, len(record.Candidate.EvidenceRefs))
	byID := evidenceIdentityMap(record.Evidence)
	for _, id := range uniqueStrings(record.Candidate.EvidenceRefs) {
		identity := byID[id]
		evidence = append(evidence, strings.Join([]string{
			id,
			identity.TargetUID,
			canonicalTime(identity.IncidentStart),
			canonicalTime(identity.IncidentEnd),
		}, "|"))
	}
	sort.Strings(evidence)
	return canonicaldigest.String(candidateAccountingCanonicalization, map[string]any{
		"diagnosticID":      strings.TrimSpace(record.DiagnosticID),
		"diagnosticVersion": strings.TrimSpace(record.DiagnosticVersion),
		"candidateID":       strings.TrimSpace(record.Candidate.ID),
		"claimType":         string(record.Candidate.Type),
		"statement":         normalizeClaimStatement(record.Candidate.Statement),
		"targetUID":         strings.TrimSpace(record.Candidate.TargetUID),
		"incidentStart":     canonicalTime(record.IncidentStart),
		"incidentEnd":       canonicalTime(record.IncidentEnd),
		"evidence":          evidence,
	})
}

// FingerprintClaim identifies the logical claim for cross-diagnostic
// deduplication. Diagnostic identity and evidence references are deliberately
// excluded so equivalent claims can be linked without double weighting.
func FingerprintClaim(record CandidateRecord) string {
	return canonicaldigest.String(candidateAccountingCanonicalization, map[string]any{
		"claimType":     string(record.Candidate.Type),
		"statement":     normalizeClaimStatement(record.Candidate.Statement),
		"targetUID":     strings.TrimSpace(record.Candidate.TargetUID),
		"incidentStart": canonicalTime(record.IncidentStart),
		"incidentEnd":   canonicalTime(record.IncidentEnd),
	})
}

func normalizeCandidateRecord(record CandidateRecord) CandidateRecord {
	record.DiagnosticID = strings.TrimSpace(record.DiagnosticID)
	record.DiagnosticVersion = strings.TrimSpace(record.DiagnosticVersion)
	record.Candidate.ID = strings.TrimSpace(record.Candidate.ID)
	record.Candidate.Statement = strings.TrimSpace(record.Candidate.Statement)
	record.Candidate.TargetUID = strings.TrimSpace(record.Candidate.TargetUID)
	record.Candidate.EvidenceRefs = uniqueStrings(record.Candidate.EvidenceRefs)
	return record
}

func validateCandidate(record CandidateRecord) (CandidateDisposition, string, bool) {
	candidate := record.Candidate
	if candidate.ID == "" {
		return CandidateDispositionRejected, "MissingCandidateID", false
	}
	if record.DiagnosticID == "" || record.DiagnosticVersion == "" {
		return CandidateDispositionRejected, "MissingDiagnosticIdentity", false
	}
	if candidate.TargetUID == "" {
		return CandidateDispositionRejected, "MissingTargetUID", false
	}
	if candidate.Statement == "" {
		return CandidateDispositionRejected, "MissingCandidateStatement", false
	}
	switch candidate.Type {
	case ClaimObservation, ClaimFailureMechanism, ClaimRootCause:
	default:
		return CandidateDispositionRejected, "InvalidClaimType", false
	}
	if len(candidate.EvidenceRefs) == 0 {
		return CandidateDispositionRetainedHypothesis, "EvidenceRefsMissing", false
	}

	evidence := evidenceIdentityMap(record.Evidence)
	conflicts := evidenceIdentityConflicts(record.Evidence)
	incomplete := false
	for _, ref := range candidate.EvidenceRefs {
		identity, ok := evidence[ref]
		if !ok {
			return CandidateDispositionRejected, "EvidenceReferenceNotFound", false
		}
		if conflicts[ref] {
			return CandidateDispositionRejected, "EvidenceIdentityConflict", false
		}
		if !identity.Authorized {
			return CandidateDispositionRejected, "EvidenceUnauthorized", false
		}
		if !identity.Valid {
			return CandidateDispositionRejected, "EvidenceInvalid", false
		}
		if identity.TargetUID == "" {
			return CandidateDispositionRejected, "EvidenceTargetUIDMissing", false
		}
		if identity.TargetUID != candidate.TargetUID {
			return CandidateDispositionRejected, "EvidenceTargetUIDMismatch", false
		}
		if !incidentWindowCompatible(record.IncidentStart, record.IncidentEnd, identity.IncidentStart, identity.IncidentEnd) {
			return CandidateDispositionRejected, "EvidenceOutsideIncidentWindow", false
		}
		incomplete = incomplete || identity.Incomplete
	}
	if incomplete {
		return CandidateDispositionRetainedHypothesis, "EvidenceIncomplete", false
	}
	return CandidateDispositionProposedClaim, "CandidateAcceptedForVerification", true
}

func summarizeDispositions(resolutions []CandidateResolution) DispositionSummary {
	summary := DispositionSummary{Total: len(resolutions)}
	for _, resolution := range resolutions {
		switch resolution.Disposition {
		case CandidateDispositionProposedClaim:
			summary.ProposedClaimCount++
		case CandidateDispositionRetainedHypothesis:
			summary.RetainedHypothesisCount++
		case CandidateDispositionRejected:
			summary.RejectedCandidateCount++
		case CandidateDispositionDeduplicated:
			summary.DeduplicatedCount++
		}
	}
	if summary.Total > 0 {
		summary.CoverageKnown = true
		summary.DispositionCoverage = 1
	}
	return summary
}

func evidenceIdentityMap(values []EvidenceIdentity) map[string]EvidenceIdentity {
	result := make(map[string]EvidenceIdentity, len(values))
	for _, value := range values {
		id := strings.TrimSpace(value.ID)
		if id == "" {
			continue
		}
		if _, exists := result[id]; !exists {
			value.ID = id
			result[id] = value
		}
	}
	return result
}

func evidenceIdentityConflicts(values []EvidenceIdentity) map[string]bool {
	result := make(map[string]bool)
	first := make(map[string]string)
	for _, value := range values {
		id := strings.TrimSpace(value.ID)
		if id == "" {
			continue
		}
		fingerprint := canonicaldigest.String(candidateAccountingCanonicalization, map[string]any{
			"targetUID":     strings.TrimSpace(value.TargetUID),
			"incidentStart": canonicalTime(value.IncidentStart),
			"incidentEnd":   canonicalTime(value.IncidentEnd),
			"authorized":    value.Authorized,
			"valid":         value.Valid,
			"incomplete":    value.Incomplete,
		})
		if previous, exists := first[id]; exists && previous != fingerprint {
			result[id] = true
			continue
		}
		first[id] = fingerprint
	}
	return result
}

func incidentWindowCompatible(candidateStart, candidateEnd, evidenceStart, evidenceEnd time.Time) bool {
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

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func normalizeClaimStatement(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

func canonicalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return canonicaldigest.CanonicalTime(value)
}
