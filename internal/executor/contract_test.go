package executor

import (
	"context"
	"testing"
	"time"

	"github.com/FluxSeer/fluxseer-rca/internal/domain"
)

func TestExecutorRequestCarriesSafetyContext(t *testing.T) {
	request := ExecutorRequest{
		ExecutionID:    "exec-123",
		IdempotencyKey: "idem-123",
		ActionType:     "kubernetes.rolloutRestart",
		Target: domain.ResourceRef{
			Namespace: "payments",
			Kind:      "Deployment",
			Name:      "payments-api",
		},
		Parameters: map[string]any{"reason": "stuck rollout"},
		ApprovedPolicySnapshot: PolicySnapshot{
			Reference:          "ApprovalPolicy/payments-safe",
			Version:            "v3",
			Digest:             "sha256:policy",
			ObservedGeneration: 7,
		},
		Timeout: 30 * time.Second,
		Attempt: 1,
	}

	if request.ExecutionID == "" || request.IdempotencyKey == "" {
		t.Fatal("expected execution and idempotency identity")
	}
	if request.ApprovedPolicySnapshot.Digest == "" || request.Timeout <= 0 {
		t.Fatalf("expected policy snapshot and timeout, got %#v", request)
	}
}

func TestStringParametersConvertsCRDParameters(t *testing.T) {
	converted := StringParameters(map[string]string{"channel": "webhook", "reason": "restart"})
	if got, ok := converted["channel"].(string); !ok || got != "webhook" {
		t.Fatalf("expected string channel parameter, got %#v", converted["channel"])
	}
	if StringParameters(nil) != nil {
		t.Fatal("expected nil for empty parameters")
	}
}

func TestSimulationExecutorEmitsContractResult(t *testing.T) {
	when := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	result, err := (KubernetesExecutor{Now: func() time.Time { return when }}).Execute(context.Background(), ExecutorRequest{
		ExecutionID: "exec-123",
		Target:      domain.ResourceRef{Namespace: "payments", Name: "payments-api"},
		ActionType:  "kubernetes.rolloutRestart",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExecutionID != "exec-123" || result.Outcome != ExecutionOutcomeSucceeded {
		t.Fatalf("expected identity and succeeded outcome, got %#v", result)
	}
	if result.StartedAt.IsZero() || result.FinishedAt.IsZero() {
		t.Fatalf("expected execution timestamps, got %#v", result)
	}
}
