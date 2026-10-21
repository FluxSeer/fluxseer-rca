package investigation

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/diagnostic"
	"github.com/FluxSeer/fluxseer-rca/internal/verifier"
)

func TestRunShadowVerificationSafelyContainsPanic(t *testing.T) {
	result := runShadowVerificationSafely(context.Background(), ShadowVerificationInput{}, func(ShadowVerificationInput) ShadowVerificationResult {
		panic("test shadow panic")
	})
	if result.Status != ShadowVerificationFailed || result.Error != shadowVerificationErrorPanic {
		t.Fatalf("expected contained shadow panic, got %#v", result)
	}
}

func TestRunShadowVerificationSafelyHonorsContextDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	started := make(chan struct{})
	result := runShadowVerificationSafely(ctx, ShadowVerificationInput{}, func(ShadowVerificationInput) ShadowVerificationResult {
		close(started)
		<-ctx.Done()
		return ShadowVerificationResult{Status: ShadowVerificationCompleted}
	})
	<-started
	if result.Status != ShadowVerificationFailed || result.Error != shadowVerificationErrorContext {
		t.Fatalf("expected deadline-isolated shadow result, got %#v", result)
	}
}

func TestRunShadowVerificationSafelyKeepsLegacyInputUnchanged(t *testing.T) {
	input := ShadowVerificationInput{
		EvidenceRefs: []v1alpha1.EvidenceRef{{ID: "event-1", Kind: "event", Reason: "FailedScheduling", Summary: "scheduler event"}},
	}
	before := input
	result := runShadowVerificationSafely(context.Background(), input, func(got ShadowVerificationInput) ShadowVerificationResult {
		got.EvidenceRefs[0].Summary = "shadow-local-copy"
		return ShadowVerificationResult{Status: ShadowVerificationCompleted}
	})
	if result.Status != ShadowVerificationCompleted {
		t.Fatalf("expected shadow completion, got %#v", result)
	}
	if !reflect.DeepEqual(input, before) {
		t.Fatalf("shadow verification mutated shared input: before=%#v after=%#v", before, input)
	}
}

func TestVerifyReconciledCandidatesSupportsSchedulingMechanism(t *testing.T) {
	reconciliation := ReconcileDiagnosticCandidates(reconciliationInput([]diagnostic.Result{
		reconciliationResult("scheduling", "v1", reconciliationClaim(
			"scheduling-mechanism-unschedulable/pod-1", diagnostic.ClaimFailureMechanism,
			"The scheduler reported that the Pod could not be scheduled.", "pod-1", "event-1")),
	}, reconciliationEvidence("event-1", "pod-1")))
	result := VerifyReconciledCandidates(ShadowVerificationInput{
		Reconciliation: reconciliation,
		EvidenceRefs: []v1alpha1.EvidenceRef{{
			ID: "event-1", Kind: "event", Reason: "FailedScheduling", Summary: "0/1 nodes are available: Insufficient cpu.",
		}},
	})

	if result.Status != ShadowVerificationCompleted || len(result.Claims) != 1 {
		t.Fatalf("expected completed scheduling shadow verification, got %#v", result)
	}
	claim := result.Claims[0]
	if claim.Disposition != diagnostic.CandidateDispositionProposedClaim || claim.Verification != "Supported" || len(claim.EvidenceRefs) != 1 {
		t.Fatalf("expected supported scheduling mechanism with evidence lineage, got %#v", claim)
	}
	if claim.Statement != "The scheduler reported that the Pod could not be scheduled." || claim.VerifierStatement == claim.Statement {
		t.Fatalf("expected canonical verifier vocabulary without replacing lineage statement, got %#v", claim)
	}
}

func TestVerifyReconciledCandidatesSupportsImagePullWithoutRootCauseInference(t *testing.T) {
	reconciliation := ReconcileDiagnosticCandidates(reconciliationInput([]diagnostic.Result{
		reconciliationResult("image-pull", "v1", reconciliationClaim(
			"image-pull-failure", diagnostic.ClaimFailureMechanism,
			"The target container could not retrieve its configured image.", "pod-image", "event-image")),
	}, reconciliationEvidence("event-image", "pod-image")))
	result := VerifyReconciledCandidates(ShadowVerificationInput{
		Reconciliation: reconciliation,
		EvidenceRefs: []v1alpha1.EvidenceRef{{
			ID: "event-image", Kind: "event", Reason: "ErrImagePull", Summary: "failed to pull image: manifest unknown",
		}},
	})

	if result.Status != ShadowVerificationCompleted || len(result.Claims) != 1 || result.Claims[0].Verification != "Supported" {
		t.Fatalf("expected image-pull mechanism to be supported through shadow verifier, got %#v", result)
	}
	if result.Claims[0].Verification == "Confirmed" {
		t.Fatal("shadow verifier must not promote a diagnostic mechanism to Confirmed root cause")
	}
}

func TestVerifyReconciledCandidatesSupportsServicePortDiagnostic(t *testing.T) {
	reconciliation := ReconcileDiagnosticCandidates(reconciliationInput([]diagnostic.Result{
		{
			DiagnosticID:      "service-port",
			DiagnosticVersion: "v1",
			Claims: []diagnostic.CandidateClaim{{
				ID:           "service-port-mismatch",
				Type:         diagnostic.ClaimRootCause,
				Statement:    "The Service targetPort does not match the resolved workload container port.",
				TargetUID:    "pod-service",
				EvidenceRefs: []string{"service-evidence"},
			}},
		},
	}, reconciliationEvidence("service-evidence", "pod-service")))
	result := VerifyReconciledCandidates(ShadowVerificationInput{
		Reconciliation: reconciliation,
		EvidenceRefs: []v1alpha1.EvidenceRef{{
			ID:      "service-evidence",
			Kind:    "serviceConfiguration",
			Reason:  "ServicePortMismatch",
			Summary: "Service targetPort does not match the workload container port.",
		}},
	})
	if result.Status != ShadowVerificationCompleted || len(result.Claims) != 1 {
		t.Fatalf("expected completed service-port shadow verification, got %#v", result)
	}
	claim := result.Claims[0]
	if claim.Verification != verifier.VerificationSupported || claim.VerifierStatement == claim.Statement {
		t.Fatalf("expected service-port canonical verifier vocabulary to be supported without replacing lineage, got %#v", claim)
	}
}

func TestVerifyReconciledCandidatesSkipsNonProposedDispositions(t *testing.T) {
	unauthorized := reconciliationEvidence("event-unauthorized", "pod-1")
	unauthorized.Authorized = false
	incomplete := reconciliationEvidence("event-incomplete", "pod-2")
	incomplete.Incomplete = true
	input := ReconcileDiagnosticCandidates(reconciliationInput([]diagnostic.Result{
		reconciliationResult("scheduling", "v1",
			reconciliationClaim("rejected", diagnostic.ClaimRootCause, "A rejected candidate.", "pod-1", "event-unauthorized"),
			reconciliationClaim("retained", diagnostic.ClaimRootCause, "A retained candidate.", "pod-2", "event-incomplete"),
			reconciliationClaim("", diagnostic.ClaimRootCause, "A candidate without identity.", "pod-3", "event-missing-id"),
		)}, unauthorized, incomplete))
	result := VerifyReconciledCandidates(ShadowVerificationInput{
		Reconciliation: input,
		EvidenceRefs: []v1alpha1.EvidenceRef{
			{ID: "event-unauthorized", Kind: "event", Reason: "FailedScheduling", Summary: "scheduler event"},
			{ID: "event-incomplete", Kind: "event", Reason: "FailedScheduling", Summary: "scheduler event", Truncated: true},
		},
	})

	if result.Status != ShadowVerificationCompleted || len(result.Claims) != 3 {
		t.Fatalf("expected completed accounting with skipped non-proposed candidates, got %#v", result)
	}
	for _, claim := range result.Claims {
		if claim.Verification != shadowVerificationSkipped {
			t.Fatalf("non-proposed candidate entered verifier: %#v", claim)
		}
	}
}

func TestVerifyReconciledCandidatesDoesNotDoubleVerifyDeduplicatedClaim(t *testing.T) {
	statement := "The scheduler reported insufficient CPU for the Pod."
	reconciliation := ReconcileDiagnosticCandidates(reconciliationInput([]diagnostic.Result{
		reconciliationResult("provider", "v1", reconciliationClaim("provider-candidate", diagnostic.ClaimRootCause, statement, "pod-1", "event-provider")),
		reconciliationResult("scheduling", "v1", reconciliationClaim("scheduling-candidate", diagnostic.ClaimRootCause, statement, "pod-1", "event-scheduling")),
	}, reconciliationEvidence("event-provider", "pod-1"), reconciliationEvidence("event-scheduling", "pod-1")))
	result := VerifyReconciledCandidates(ShadowVerificationInput{
		Reconciliation: reconciliation,
		EvidenceRefs: []v1alpha1.EvidenceRef{
			{ID: "event-provider", Kind: "event", Reason: "FailedScheduling", Summary: "Insufficient cpu"},
			{ID: "event-scheduling", Kind: "event", Reason: "FailedScheduling", Summary: "Insufficient cpu"},
		},
	})
	if len(result.Claims) != 2 {
		t.Fatalf("expected both lineage results, got %#v", result)
	}
	verified, skipped := 0, 0
	for _, claim := range result.Claims {
		if claim.Disposition == diagnostic.CandidateDispositionProposedClaim {
			verified++
			if claim.Verification != "Supported" {
				t.Fatalf("canonical candidate was not verified: %#v", claim)
			}
		}
		if claim.Disposition == diagnostic.CandidateDispositionDeduplicated {
			skipped++
			if claim.Verification != shadowVerificationSkipped || claim.DuplicateOf == "" {
				t.Fatalf("deduplicated candidate was reverified or lost lineage: %#v", claim)
			}
		}
	}
	if verified != 1 || skipped != 1 {
		t.Fatalf("expected one verified and one skipped duplicate, got %#v", result.Claims)
	}
}

func TestVerifyReconciledCandidatesKeepsUIDsIsolated(t *testing.T) {
	reconciliation := ReconcileDiagnosticCandidates(reconciliationInput([]diagnostic.Result{
		reconciliationResult("scheduling", "v1",
			reconciliationClaim("candidate-pod-1", diagnostic.ClaimRootCause, "The scheduler reported insufficient CPU for the Pod.", "pod-1", "event-1"),
			reconciliationClaim("candidate-pod-2", diagnostic.ClaimRootCause, "The scheduler reported insufficient CPU for the Pod.", "pod-2", "event-2"),
		)}, reconciliationEvidence("event-1", "pod-1"), reconciliationEvidence("event-2", "pod-2")))
	result := VerifyReconciledCandidates(ShadowVerificationInput{
		Reconciliation: reconciliation,
		EvidenceRefs: []v1alpha1.EvidenceRef{
			{ID: "event-1", Kind: "event", Reason: "FailedScheduling", Summary: "Insufficient cpu"},
			{ID: "event-2", Kind: "event", Reason: "FailedScheduling", Summary: "Insufficient cpu"},
		},
	})
	if len(result.Claims) != 2 {
		t.Fatalf("expected two isolated shadow claims, got %#v", result)
	}
	for _, claim := range result.Claims {
		if claim.Verification != "Supported" || len(claim.EvidenceRefs) != 1 {
			t.Fatalf("expected isolated supported claim, got %#v", claim)
		}
		if claim.TargetUID != "pod-1" && claim.TargetUID != "pod-2" {
			t.Fatalf("unexpected target UID: %#v", claim)
		}
	}
}

func TestVerifyReconciledCandidatesFailsClosedForEvidenceDrift(t *testing.T) {
	reconciliation := ReconcileDiagnosticCandidates(reconciliationInput([]diagnostic.Result{
		reconciliationResult("scheduling", "v1", reconciliationClaim("candidate-1", diagnostic.ClaimRootCause, "The scheduler reported insufficient CPU for the Pod.", "pod-1", "event-1")),
	}, reconciliationEvidence("event-1", "pod-1")))
	result := VerifyReconciledCandidates(ShadowVerificationInput{
		Reconciliation: reconciliation,
		EvidenceRefs: []v1alpha1.EvidenceRef{
			{ID: "event-1", Kind: "event", Reason: "FailedScheduling", Summary: "first content"},
			{ID: "event-1", Kind: "event", Reason: "FailedScheduling", Summary: "conflicting content"},
		},
	})
	if result.Status != ShadowVerificationFailed || result.Error != shadowVerificationErrorEvidence {
		t.Fatalf("expected fail-closed evidence drift result, got %#v", result)
	}
}

func TestVerifyReconciledCandidatesIsDeterministic(t *testing.T) {
	first := reconciliationResult("scheduling", "v1", reconciliationClaim("candidate-a", diagnostic.ClaimRootCause, "The scheduler reported insufficient CPU for the Pod.", "pod-1", "event-a"))
	second := reconciliationResult("provider", "v1", reconciliationClaim("candidate-b", diagnostic.ClaimRootCause, "The scheduler reported insufficient CPU for the Pod.", "pod-1", "event-b"))
	evidence := []diagnostic.EvidenceIdentity{reconciliationEvidence("event-a", "pod-1"), reconciliationEvidence("event-b", "pod-1")}
	leftReconciliation := ReconcileDiagnosticCandidates(reconciliationInput([]diagnostic.Result{first, second}, evidence...))
	rightReconciliation := ReconcileDiagnosticCandidates(reconciliationInput([]diagnostic.Result{second, first}, evidence[1], evidence[0]))
	left := VerifyReconciledCandidates(ShadowVerificationInput{
		Reconciliation: leftReconciliation,
		EvidenceRefs: []v1alpha1.EvidenceRef{
			{ID: "event-a", Kind: "event", Reason: "FailedScheduling", Summary: "Insufficient cpu"},
			{ID: "event-b", Kind: "event", Reason: "FailedScheduling", Summary: "Insufficient cpu"},
		},
	})
	right := VerifyReconciledCandidates(ShadowVerificationInput{
		Reconciliation: rightReconciliation,
		EvidenceRefs: []v1alpha1.EvidenceRef{
			{ID: "event-b", Kind: "event", Reason: "FailedScheduling", Summary: "Insufficient cpu"},
			{ID: "event-a", Kind: "event", Reason: "FailedScheduling", Summary: "Insufficient cpu"},
		},
	})
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("shadow verification changed with input order: left=%#v right=%#v", left, right)
	}
}
