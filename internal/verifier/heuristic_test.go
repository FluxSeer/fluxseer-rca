package verifier

import "testing"

func TestVerifyClaimsMarksRelevantEvidenceSupported(t *testing.T) {
	result := VerifyClaims(
		[]Claim{
			{ID: "claim-001", Statement: "Pods are restarting and entering backoff"},
			{ID: "claim-002", Statement: "CPU saturation is the likely cause"},
		},
		[]EvidenceRef{
			{ID: "evidence-001", Kind: "event", Summary: "BackOff restarting failed container"},
			{ID: "evidence-002", Kind: "metric", Summary: "cpu usage sustained above threshold"},
			{ID: "evidence-003", Kind: "log", Source: "loki", Summary: "fatal startup error: invalid configuration"},
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

func TestVerifyClaimsDoesNotPromoteBackOffEventTextToCausalEvidence(t *testing.T) {
	result := VerifyClaims(
		[]Claim{
			{ID: "claim-001", Statement: "Pods are restarting and entering backoff"},
			{ID: "claim-002", Statement: "Recent rollout changed workload behavior"},
			{ID: "claim-003", Statement: "Pod memory usage crossed safe threshold"},
		},
		[]EvidenceRef{{
			ID:      "evidence-001",
			Kind:    "event",
			Reason:  "BackOff",
			Summary: "Back-off restarting failed container after recent rollout changed workload behavior and pod memory usage crossed safe threshold",
		}},
	)

	if result.CoverageScore != 0 {
		t.Fatalf("expected no CrashLoop claim to be covered without application evidence, got %#v", result)
	}
	for _, claim := range result.Claims {
		switch claim.ID {
		case "claim-001":
			if claim.Verification != VerificationUnsupported || len(claim.EvidenceRefs) != 0 {
				t.Fatalf("expected BackOff symptom unsupported without application evidence, got %#v", claim)
			}
		case "claim-002", "claim-003":
			if claim.Verification != VerificationUnsupported || len(claim.EvidenceRefs) != 0 {
				t.Fatalf("expected causal claim unsupported without typed evidence, got %#v", claim)
			}
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
			{ID: "evidence-002", Kind: "log", Source: "loki", Summary: "panic during startup: invalid configuration"},
		},
	)

	if result.CoverageScore != 0.5 {
		t.Fatalf("expected half coverage score, got %#v", result)
	}
	if result.Claims[0].Verification != VerificationUnsupported || len(result.Claims[0].EvidenceRefs) != 0 {
		t.Fatalf("expected unrelated claim to be unsupported, got %#v", result.Claims[0])
	}
	if result.Claims[1].Verification != VerificationSupported || len(result.Claims[1].EvidenceRefs) != 2 {
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

func TestVerifyClaimsRequiresMetricAndCausalLogForHighHTTPError(t *testing.T) {
	claim := Claim{ID: "claim-001", Statement: "High HTTP error rate is caused by the inventory dependency being unavailable"}
	positive := VerifyClaims([]Claim{claim}, []EvidenceRef{
		{ID: "evidence-001", Kind: "metric", Source: "prometheus", Summary: "HTTP 5xx error rate elevated above threshold"},
		{ID: "evidence-002", Kind: "log", Source: "loki", Summary: "inventory dependency unavailable: connection refused"},
	})
	if positive.Claims[0].Verification != VerificationSupported || len(positive.Claims[0].EvidenceRefs) != 2 {
		t.Fatalf("expected high HTTP causal claim to link metric and log evidence, got %#v", positive.Claims[0])
	}

	metricOnly := VerifyClaims([]Claim{claim}, []EvidenceRef{
		{ID: "evidence-001", Kind: "metric", Source: "prometheus", Summary: "HTTP 5xx error rate elevated above threshold"},
	})
	if metricOnly.Claims[0].Verification != VerificationUnsupported || len(metricOnly.Claims[0].EvidenceRefs) != 0 {
		t.Fatalf("expected metric-only high HTTP causal claim to remain unsupported, got %#v", metricOnly.Claims[0])
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
			Summary: "Synthetic readiness probe failure for FluxSeer RCA validation",
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

func TestVerifyProbeFailureRequiresConfigurationEvidence(t *testing.T) {
	claim := Claim{ID: "probe-claim", Statement: "The readiness probe failed because the configured probe port does not match the container port."}
	eventOnly := VerifyClaims([]Claim{claim}, []EvidenceRef{{
		ID: "event-001", Kind: "event", Reason: "Unhealthy", Summary: "Readiness probe failed",
	}})
	if eventOnly.Claims[0].Verification != VerificationUnsupported {
		t.Fatalf("expected event-only probe claim to remain unsupported, got %#v", eventOnly.Claims[0])
	}

	withConfiguration := VerifyClaims([]Claim{claim}, []EvidenceRef{
		{ID: "event-001", Kind: "event", Reason: "Unhealthy", Summary: "Readiness probe failed"},
		{ID: "probe-001", Kind: "probeConfiguration", Reason: "ProbeConfigurationMismatch", Summary: "readiness probe port 8080 does not match container port 3000; mismatchConfirmed=true"},
	})
	if withConfiguration.Claims[0].Verification != VerificationSupported || len(withConfiguration.Claims[0].EvidenceRefs) != 2 {
		t.Fatalf("expected probe claim to require and link both evidence types, got %#v", withConfiguration.Claims[0])
	}
}

func TestVerifyClaimsDoesNotTreatNegativeHealthStatesAsContradictions(t *testing.T) {
	tests := []struct {
		name      string
		statement string
		summary   string
	}{
		{name: "not ready", statement: "checkout-api pod is not ready", summary: "readiness probe failed and checkout-api pod is not ready"},
		{name: "not available", statement: "payments service is not available", summary: "payments service is not available after a refused connection"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := VerifyClaims(
				[]Claim{{ID: "claim-001", Statement: tt.statement}},
				[]EvidenceRef{{ID: "evidence-001", Kind: "event", Summary: tt.summary}},
			)
			if result.Claims[0].Verification != VerificationSupported {
				t.Fatalf("expected negative health state to support rather than contradict the claim, got %#v", result.Claims[0])
			}
		})
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
			name:  "generic image pull failure requires image pull event",
			claim: "The workload has an image pull failure",
			evidence: []EvidenceRef{
				{ID: "evidence-001", Kind: "event", Reason: "ErrImagePull", Summary: "failed to pull image"},
			},
			wantStatus: VerificationSupported,
			wantRefs:   1,
		},
		{
			name:  "generic image pull event does not prove missing image",
			claim: "ImagePullBackOff is caused by a missing container image",
			evidence: []EvidenceRef{
				{ID: "evidence-001", Kind: "event", Reason: "ErrImagePull", Summary: "failed to pull image"},
			},
			wantStatus: VerificationUnsupported,
			wantRefs:   0,
		},
		{
			name:  "manifest unknown supports missing image claim",
			claim: "ImagePullBackOff is caused by a missing container image",
			evidence: []EvidenceRef{
				{ID: "evidence-001", Kind: "event", Reason: "ErrImagePull", Summary: "failed to pull image: manifest unknown: image not found"},
			},
			wantStatus: VerificationSupported,
			wantRefs:   1,
		},
		{
			name:  "generic image pull event does not prove registry authentication failure",
			claim: "Image pull failed because registry authentication was denied",
			evidence: []EvidenceRef{
				{ID: "evidence-001", Kind: "event", Reason: "ErrImagePull", Summary: "failed to pull image"},
			},
			wantStatus: VerificationUnsupported,
			wantRefs:   0,
		},
		{
			name:  "pull access denied supports registry authentication failure",
			claim: "Image pull failed because registry authentication was denied",
			evidence: []EvidenceRef{
				{ID: "evidence-001", Kind: "event", Reason: "ErrImagePull", Summary: "pull access denied: authentication required"},
			},
			wantStatus: VerificationSupported,
			wantRefs:   1,
		},
		{
			name:  "generic image pull event does not prove registry DNS failure",
			claim: "Image pull failed because the registry DNS lookup failed",
			evidence: []EvidenceRef{
				{ID: "evidence-001", Kind: "event", Reason: "ErrImagePull", Summary: "failed to pull image"},
			},
			wantStatus: VerificationUnsupported,
			wantRefs:   0,
		},
		{
			name:  "no such host supports registry DNS failure",
			claim: "Image pull failed because the registry DNS lookup failed",
			evidence: []EvidenceRef{
				{ID: "evidence-001", Kind: "event", Reason: "ErrImagePull", Summary: "dial registry.example: no such host"},
			},
			wantStatus: VerificationSupported,
			wantRefs:   1,
		},
		{
			name:  "generic image pull event does not prove registry unavailability",
			claim: "Image pull failed because the registry was unavailable",
			evidence: []EvidenceRef{
				{ID: "evidence-001", Kind: "event", Reason: "ErrImagePull", Summary: "failed to pull image"},
			},
			wantStatus: VerificationUnsupported,
			wantRefs:   0,
		},
		{
			name:  "registry timeout supports registry unavailability",
			claim: "Image pull failed because the registry was unavailable",
			evidence: []EvidenceRef{
				{ID: "evidence-001", Kind: "event", Reason: "ErrImagePull", Summary: "registry timeout: service unavailable"},
			},
			wantStatus: VerificationSupported,
			wantRefs:   1,
		},
		{
			name:  "crash loop requires event and application log",
			claim: "CrashLoopBackOff is causing repeated pod restarts",
			evidence: []EvidenceRef{
				{ID: "evidence-001", Kind: "event", Reason: "BackOff", Summary: "container crashed repeatedly"},
				{ID: "evidence-002", Kind: "log", Source: "loki", Summary: "fatal startup error: invalid configuration"},
			},
			wantStatus: VerificationSupported,
			wantRefs:   2,
		},
		{
			name:  "crash loop event alone is insufficient",
			claim: "CrashLoopBackOff is causing repeated pod restarts",
			evidence: []EvidenceRef{
				{ID: "evidence-001", Kind: "event", Reason: "BackOff", Summary: "container crashed repeatedly"},
			},
			wantStatus: VerificationUnsupported,
			wantRefs:   0,
		},
		{
			name:  "memory pressure requires event and memory metric",
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
			name:  "memory event alone is insufficient",
			claim: "OOMKilled is caused by memory pressure",
			evidence: []EvidenceRef{
				{ID: "evidence-001", Kind: "event", Reason: "OOMKilled", Summary: "container was killed after exceeding memory limit"},
			},
			wantStatus: VerificationUnsupported,
			wantRefs:   0,
		},
		{
			name:  "rollout requires deployment condition evidence",
			claim: "Recent rollout changed workload behavior",
			evidence: []EvidenceRef{
				{ID: "evidence-001", Kind: "deploymentcondition", Source: "kubernetes", Summary: "deployment generation changed and new ReplicaSet became Progressing"},
			},
			wantStatus: VerificationSupported,
			wantRefs:   1,
		},
		{
			name:  "event text alone does not prove rollout",
			claim: "Recent rollout changed workload behavior",
			evidence: []EvidenceRef{
				{ID: "evidence-001", Kind: "event", Reason: "BackOff", Summary: "BackOff after recent rollout changed workload behavior"},
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
