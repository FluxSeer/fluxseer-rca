package statusbudget

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
)

func TestTruncateUTF8PreservesValidUTF8(t *testing.T) {
	out, originalBytes, retainedBytes, truncated := TruncateUTF8("延遲異常增加", 7)

	if !truncated {
		t.Fatal("expected truncation")
	}
	if originalBytes <= retainedBytes {
		t.Fatalf("expected retained bytes below original, got original=%d retained=%d", originalBytes, retainedBytes)
	}
	if !utf8.ValidString(out) {
		t.Fatalf("expected valid utf8, got %q", out)
	}
}

func TestEnforceInvestigationStatusBoundsEvidenceAndClaims(t *testing.T) {
	status := &v1alpha1.InvestigationRequestStatus{
		ResourceStatus: v1alpha1.ResourceStatus{Phase: v1alpha1.PhaseCompleted},
		Outcome:        v1alpha1.InvestigationOutcomeConfirmed,
		Verdict:        &v1alpha1.RCAVerdict{Outcome: v1alpha1.InvestigationOutcomeConfirmed, Summary: strings.Repeat("s", MaxSummaryBytes*2)},
		Execution:      &v1alpha1.RCAExecution{ID: "sha256:execution", State: "Finalized"},
	}
	for i := 0; i < MaxEvidenceRefs+10; i++ {
		status.EvidenceRefs = append(status.EvidenceRefs, v1alpha1.EvidenceRef{
			ID:            "evidence",
			ContentDigest: "sha256:digest",
			Summary:       strings.Repeat("e", MaxEvidenceSummaryBytes*2),
		})
	}
	for i := 0; i < MaxClaims+10; i++ {
		status.Claims = append(status.Claims, v1alpha1.RCAClaim{ID: "claim", Statement: strings.Repeat("c", MaxClaimStatementBytes*2), Verification: "Inferred"})
	}

	if !EnforceInvestigationStatus(status) {
		t.Fatal("expected status budget enforcement to truncate")
	}
	if len(status.EvidenceRefs) > MaxEvidenceRefs {
		t.Fatalf("expected bounded evidence refs, got %d", len(status.EvidenceRefs))
	}
	if len(status.Claims) > MaxClaims {
		t.Fatalf("expected bounded claims, got %d", len(status.Claims))
	}
	if len(status.EvidenceRefs[0].Summary) > MaxEvidenceSummaryBytes || !status.EvidenceRefs[0].Truncated {
		t.Fatalf("expected evidence summary truncation metadata, got %#v", status.EvidenceRefs[0])
	}
	if status.Execution == nil || status.Execution.ID == "" {
		t.Fatalf("expected execution identity to be retained, got %#v", status.Execution)
	}
}

func TestCompactEvidenceRefPreservesExistingByteMetadata(t *testing.T) {
	ref, truncated := CompactEvidenceRef(v1alpha1.EvidenceRef{
		Summary:       strings.Repeat("x", MaxEvidenceSummaryBytes),
		Truncated:     true,
		OriginalBytes: 4096,
		RetainedBytes: int32(MaxEvidenceSummaryBytes),
	})

	if truncated {
		t.Fatal("expected no new truncation when summary is already within budget")
	}
	if ref.OriginalBytes != 4096 || ref.RetainedBytes != int32(MaxEvidenceSummaryBytes) {
		t.Fatalf("expected existing byte metadata to be preserved, got %#v", ref)
	}
}
