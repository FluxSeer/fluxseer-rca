package executor

import (
	"testing"

	"github.com/FluxSeer/fluxseer-rca/internal/domain"
)

func TestBuildExecutionIdentityIsDeterministic(t *testing.T) {
	base := IdentityInput{
		ActionRef:      "payments/action",
		AgentActionUID: "uid-123",
		Generation:     4,
		ActionIndex:    0,
		Target:         domain.ResourceRef{Namespace: "payments", Kind: "Deployment", Name: "payments-api"},
		TargetUID:      "target-uid-1",
		ActionDigest:   "sha256:action",
		ActionType:     "kubernetes.rolloutRestart",
		Parameters:     map[string]any{"reason": "stuck rollout", "grace": 30},
	}

	first := BuildExecutionIdentity(base)
	second := BuildExecutionIdentity(base)
	if first != second {
		t.Fatalf("expected stable identity, first=%#v second=%#v", first, second)
	}
	if first.ExecutionID == first.IdempotencyKey {
		t.Fatal("execution ID and idempotency key must have different semantics")
	}
	if first.ExecutionID == "" || first.IdempotencyKey == "" {
		t.Fatalf("expected both identities, got %#v", first)
	}
}

func TestBuildExecutionIdentityChangesWithActionIdentity(t *testing.T) {
	base := IdentityInput{
		AgentActionUID: "uid-123",
		Generation:     4,
		Target:         domain.ResourceRef{Namespace: "payments", Kind: "Deployment", Name: "payments-api"},
		ActionType:     "kubernetes.rolloutRestart",
	}
	first := BuildExecutionIdentity(base)
	base.Generation++
	second := BuildExecutionIdentity(base)
	if first.IdempotencyKey == second.IdempotencyKey || first.ExecutionID == second.ExecutionID {
		t.Fatalf("expected generation change to produce new identity, first=%#v second=%#v", first, second)
	}
}
