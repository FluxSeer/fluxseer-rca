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

	if result.Method != MethodDomainEvidenceCoverageV2 {
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

func TestVerifyClaimsDoesNotTreatUnhealthyAsHealthyContradiction(t *testing.T) {
	result := VerifyClaims(
		[]Claim{
			{ID: "claim-001", Statement: "Recent rollout changed the workload configuration"},
			{ID: "claim-002", Statement: "Pod memory usage crossed the configured limit"},
		},
		[]EvidenceRef{{
			ID:      "evidence-001",
			Kind:    "event",
			Reason:  "Unhealthy",
			Summary: "Synthetic readiness probe failure for FluxAgent RCA validation",
		}},
	)

	if result.CoverageScore != 0 {
		t.Fatalf("expected no root-cause evidence coverage, got %#v", result)
	}
	for _, claim := range result.Claims {
		if claim.Verification != VerificationUnsupported {
			t.Fatalf("expected unsupported rather than contradicted for Unhealthy event, got %#v", claim)
		}
		if len(claim.EvidenceLinks) != 0 {
			t.Fatalf("expected no contradictory evidence links, got %#v", claim)
		}
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

func TestVerifyClaimsAppliesDomainSpecificSupportRequirements(t *testing.T) {
	tests := []struct {
		name       string
		claim      string
		evidence   []EvidenceRef
		wantStatus string
		wantRefs   int
	}{
		{
			name:  "image pull requires image pull event",
			claim: "ImagePullBackOff is caused by a missing container image",
			evidence: []EvidenceRef{
				{ID: "evidence-001", Kind: "event", Reason: "ErrImagePull", Summary: "failed to pull image"},
			},
			wantStatus: VerificationSupported,
			wantRefs:   1,
		},
		{
			name:  "crash loop requires crashloop event",
			claim: "CrashLoopBackOff is causing repeated pod restarts",
			evidence: []EvidenceRef{
				{ID: "evidence-001", Kind: "event", Reason: "BackOff", Summary: "container crashed repeatedly"},
			},
			wantStatus: VerificationSupported,
			wantRefs:   1,
		},
		{
			name:  "oomkilled requires event and memory metric",
			claim: "OOMKilled is caused by memory pressure",
			evidence: []EvidenceRef{
				{ID: "evidence-001", Kind: "event", Reason: "OOMKilled", Summary: "container was killed after exceeding memory limit"},
				{ID: "evidence-002", Kind: "metric", Source: "prometheus", Summary: "container_memory_working_set_bytes above limit"},
			},
			wantStatus: VerificationSupported,
			wantRefs:   2,
		},
		{
			name:  "latency regression requires metric evidence",
			claim: "Latency regression is causing slow responses",
			evidence: []EvidenceRef{
				{ID: "evidence-001", Kind: "metric", Source: "prometheus", Summary: "p95 latency increased above threshold"},
			},
			wantStatus: VerificationSupported,
			wantRefs:   1,
		},
		{
			name:  "oomkilled event alone is insufficient",
			claim: "OOMKilled is caused by memory pressure",
			evidence: []EvidenceRef{
				{ID: "evidence-001", Kind: "event", Reason: "OOMKilled", Summary: "container was killed after exceeding memory limit"},
			},
			wantStatus: VerificationUnsupported,
			wantRefs:   0,
		},
		{
			name:  "latency claim is not supported by timeout log alone",
			claim: "Latency regression is causing slow responses",
			evidence: []EvidenceRef{
				{ID: "evidence-001", Kind: "log", Source: "loki", Summary: "request timeout observed"},
			},
			wantStatus: VerificationUnsupported,
			wantRefs:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := VerifyClaims([]Claim{{ID: "claim-001", Statement: tt.claim}}, tt.evidence)
			if len(result.Claims) != 1 {
				t.Fatalf("expected one claim result, got %#v", result)
			}
			if result.Claims[0].Verification != tt.wantStatus {
				t.Fatalf("expected %s, got %#v", tt.wantStatus, result.Claims[0])
			}
			if len(result.Claims[0].EvidenceRefs) != tt.wantRefs {
				t.Fatalf("expected %d evidence refs, got %#v", tt.wantRefs, result.Claims[0])
			}
		})
	}
}
