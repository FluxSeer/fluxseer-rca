package verifier

import "testing"

func TestVerifyClaimsMarksRelevantEvidenceSupported(t *testing.T) {
	result := VerifyClaims(
		[]Claim{
			{ID: "claim-001", Statement: "Pods are restarting and entering backoff after rollout"},
			{ID: "claim-002", Statement: "CPU saturation is the likely cause"},
		},
		[]EvidenceRef{
			{ID: "evidence-001", Kind: "event", Summary: "BackOff restarting failed container"},
			{ID: "evidence-002", Kind: "metric", Summary: "cpu usage sustained above threshold"},
		},
	)

	if result.Method != MethodHeuristicEvidenceCoverageV1 {
		t.Fatalf("expected verifier method, got %#v", result)
	}
	if result.CoverageScore != 1 {
		t.Fatalf("expected full coverage score, got %#v", result)
	}
	for _, claim := range result.Claims {
		if claim.Verification != VerificationSupported {
			t.Fatalf("expected supported claim, got %#v", claim)
		}
		if len(claim.EvidenceRefs) == 0 {
			t.Fatalf("expected cited evidence refs, got %#v", claim)
		}
		if len(claim.EvidenceLinks) == 0 || claim.EvidenceLinks[0].Role != EvidenceRoleSupports || claim.EvidenceLinks[0].Strength != EvidenceStrengthDirect {
			t.Fatalf("expected direct supporting evidence link, got %#v", claim)
		}
	}
}

func TestVerifyClaimsMarksUnsupportedWhenEvidenceIsIrrelevant(t *testing.T) {
	result := VerifyClaims(
		[]Claim{
			{ID: "claim-001", Statement: "Database connection pool exhaustion caused errors"},
			{ID: "claim-002", Statement: "Pods are restarting"},
		},
		[]EvidenceRef{
			{ID: "evidence-001", Kind: "event", Summary: "BackOff restarting failed container"},
		},
	)

	if result.CoverageScore != 0.5 {
		t.Fatalf("expected half coverage score, got %#v", result)
	}
	if result.Claims[0].Verification != VerificationUnsupported || len(result.Claims[0].EvidenceRefs) != 0 {
		t.Fatalf("expected unrelated claim to be unsupported, got %#v", result.Claims[0])
	}
	if result.Claims[1].Verification != VerificationSupported || len(result.Claims[1].EvidenceRefs) != 1 {
		t.Fatalf("expected restart claim to be supported, got %#v", result.Claims[1])
	}
}

func TestVerifyClaimsMarksContradictedEvidence(t *testing.T) {
	result := VerifyClaims(
		[]Claim{{ID: "claim-001", Statement: "CPU saturation is causing errors"}},
		[]EvidenceRef{{ID: "evidence-001", Kind: "metric", Summary: "cpu usage normal below threshold"}},
	)

	if result.CoverageScore != 0 {
		t.Fatalf("expected contradicted claim not to raise coverage, got %#v", result)
	}
	if result.Claims[0].Verification != VerificationContradicted {
		t.Fatalf("expected contradicted claim, got %#v", result.Claims[0])
	}
	if len(result.Claims[0].EvidenceLinks) != 1 || result.Claims[0].EvidenceLinks[0].Role != EvidenceRoleContradicts {
		t.Fatalf("expected contradictory evidence link, got %#v", result.Claims[0])
	}
}

func TestVerifyClaimsWithoutEvidenceIsUnverified(t *testing.T) {
	result := VerifyClaims([]Claim{{ID: "claim-001", Statement: "Pods are restarting"}}, nil)

	if result.CoverageScore != 0 {
		t.Fatalf("expected zero coverage score, got %#v", result)
	}
	if result.Claims[0].Verification != VerificationUnverified {
		t.Fatalf("expected unverified claim, got %#v", result.Claims[0])
	}
}
