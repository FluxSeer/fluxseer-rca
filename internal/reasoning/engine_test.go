package reasoning

import (
	"context"
	"strings"
	"testing"

	"fluxagent/internal/domain"
	"fluxagent/internal/knowledge"
	"fluxagent/internal/model"
)

type invalidStructuredProvider struct{}

func (p invalidStructuredProvider) Name() string {
	return "invalid"
}

func (p invalidStructuredProvider) Complete(context.Context, domain.ModelRequest) (domain.ModelResponse, error) {
	return domain.ModelResponse{
		Provider:   p.Name(),
		Model:      "invalid-test",
		Structured: true,
		Output: map[string]any{
			"riskTitle":     "Incomplete",
			"riskSummary":   "Provider omitted required RCA fields.",
			"severity":      "medium",
			"rationale":     "test fixture",
			"rcaHypothesis": "missing confidenceScore and rcaCauses",
			"actionType":    "notification.sendSlack",
		},
	}, nil
}

func TestEngineRejectsInvalidStructuredProviderResponse(t *testing.T) {
	engine := NewEngine(knowledge.NewBase(), invalidStructuredProvider{})
	_, err := engine.Analyze(context.Background(), domain.IngestionOutput{
		Context: domain.IncidentContext{
			Resource: domain.ResourceRef{Namespace: "prod", Name: "payments-api", Kind: "Deployment"},
			Summary:  "error rate increased",
		},
	})
	if err == nil {
		t.Fatal("expected invalid provider response error")
	}
	providerErr, ok := err.(*model.ProviderError)
	if !ok {
		t.Fatalf("expected ProviderError, got %T", err)
	}
	if providerErr.Reason != "InvalidProviderResponse" {
		t.Fatalf("expected InvalidProviderResponse, got %q", providerErr.Reason)
	}
	if !strings.Contains(providerErr.Message, "confidenceScore") {
		t.Fatalf("expected confidenceScore message, got %q", providerErr.Message)
	}
}
