package reasoning

import (
	"context"
	"strings"
	"testing"

	"github.com/FluxSeer/fluxseer-rca/internal/domain"
	"github.com/FluxSeer/fluxseer-rca/internal/knowledge"
	"github.com/FluxSeer/fluxseer-rca/internal/model"
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

type requestIDProvider struct{}

func (p requestIDProvider) Name() string {
	return "request-id-provider"
}

func (p requestIDProvider) Complete(context.Context, domain.ModelRequest) (domain.ModelResponse, error) {
	score := 81
	return domain.ModelResponse{
		Provider:          p.Name(),
		Model:             "request-id-test",
		ProviderRequestID: "provider-request-abc",
		InputTokens:       144,
		OutputTokens:      55,
		Structured:        true,
		Output: map[string]any{
			"riskTitle":       "Latency regression",
			"riskSummary":     "Provider returned a request id.",
			"severity":        "high",
			"confidenceScore": score,
			"rationale":       "test fixture",
			"rcaHypothesis":   "upstream latency increased",
			"rcaCauses":       []string{"upstream latency"},
			"actionType":      "notification.sendSlack",
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

func TestEngineCarriesProviderRequestID(t *testing.T) {
	engine := NewEngine(knowledge.NewBase(), requestIDProvider{})
	result, err := engine.Analyze(context.Background(), domain.IngestionOutput{
		Context: domain.IncidentContext{
			Resource: domain.ResourceRef{Namespace: "prod", Name: "payments-api", Kind: "Deployment"},
			Summary:  "latency increased",
		},
	})
	if err != nil {
		t.Fatalf("unexpected analyze error: %v", err)
	}
	if result.ProviderRequestID != "provider-request-abc" {
		t.Fatalf("expected provider request id, got %q", result.ProviderRequestID)
	}
	if result.InputTokens != 144 || result.OutputTokens != 55 {
		t.Fatalf("expected token usage 144/55, got %d/%d", result.InputTokens, result.OutputTokens)
	}
}
