package statusbudget

import (
	"encoding/json"
	"unicode/utf8"

	"fluxagent/api/v1alpha1"
)

const (
	MaxEvidenceRefs         = 32
	MaxClaims               = 16
	MaxEvidenceSummaryBytes = 1024
	MaxClaimStatementBytes  = 1024
	MaxSummaryBytes         = 2048
	MaxStatusBytes          = 65536
)

func TruncateUTF8(value string, maxBytes int) (string, int32, int32, bool) {
	originalBytes := len(value)
	if maxBytes <= 0 {
		return "", int32(originalBytes), 0, originalBytes > 0
	}
	if originalBytes <= maxBytes {
		return value, int32(originalBytes), int32(originalBytes), false
	}

	retained := 0
	for retained < len(value) {
		_, size := utf8.DecodeRuneInString(value[retained:])
		if retained+size > maxBytes {
			break
		}
		retained += size
	}
	return value[:retained], int32(originalBytes), int32(retained), true
}

func CompactEvidenceRef(ref v1alpha1.EvidenceRef) (v1alpha1.EvidenceRef, bool) {
	summary, originalBytes, retainedBytes, truncated := TruncateUTF8(ref.Summary, MaxEvidenceSummaryBytes)
	ref.Summary = summary
	if ref.OriginalBytes == 0 {
		ref.OriginalBytes = originalBytes
	}
	if truncated || ref.RetainedBytes == 0 {
		ref.RetainedBytes = retainedBytes
	}
	ref.Truncated = ref.Truncated || truncated
	return ref, truncated
}

func CompactEvidenceRefs(refs []v1alpha1.EvidenceRef) ([]v1alpha1.EvidenceRef, bool) {
	limit := len(refs)
	truncated := false
	if limit > MaxEvidenceRefs {
		limit = MaxEvidenceRefs
		truncated = true
	}
	out := make([]v1alpha1.EvidenceRef, 0, limit)
	for i := 0; i < limit; i++ {
		ref, refTruncated := CompactEvidenceRef(refs[i])
		truncated = truncated || refTruncated
		out = append(out, ref)
	}
	return out, truncated
}

func EnforceInvestigationStatus(status *v1alpha1.InvestigationRequestStatus) bool {
	truncated := false
	status.Summary, _, _, truncated = truncateStatusString(status.Summary, MaxSummaryBytes, truncated)
	status.Hypothesis, _, _, truncated = truncateStatusString(status.Hypothesis, MaxSummaryBytes, truncated)
	if status.Verdict != nil {
		status.Verdict.Summary, _, _, truncated = truncateStatusString(status.Verdict.Summary, MaxSummaryBytes, truncated)
	}
	if status.Failure != nil {
		status.Failure.Message, _, _, truncated = truncateStatusString(status.Failure.Message, MaxSummaryBytes, truncated)
	}
	for i := range status.Claims {
		status.Claims[i].Statement, _, _, truncated = truncateStatusString(status.Claims[i].Statement, MaxClaimStatementBytes, truncated)
	}
	if len(status.Claims) > MaxClaims {
		status.Claims = status.Claims[:MaxClaims]
		truncated = true
	}
	if compacted, refsTruncated := CompactEvidenceRefs(status.EvidenceRefs); refsTruncated {
		status.EvidenceRefs = compacted
		truncated = true
	}

	for statusSize(status) > MaxStatusBytes {
		switch {
		case len(status.EvidenceRefs) > 0:
			status.EvidenceRefs = status.EvidenceRefs[:len(status.EvidenceRefs)-1]
		case len(status.Claims) > 0:
			status.Claims = status.Claims[:len(status.Claims)-1]
		default:
			return true
		}
		truncated = true
	}
	return truncated
}

func truncateStatusString(value string, maxBytes int, alreadyTruncated bool) (string, int32, int32, bool) {
	out, _, _, truncated := TruncateUTF8(value, maxBytes)
	return out, 0, 0, alreadyTruncated || truncated
}

func statusSize(status *v1alpha1.InvestigationRequestStatus) int {
	payload, err := json.Marshal(status)
	if err != nil {
		return MaxStatusBytes + 1
	}
	return len(payload)
}
