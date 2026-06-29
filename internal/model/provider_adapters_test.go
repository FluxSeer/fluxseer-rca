package model_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"fluxagent/internal/domain"
	"fluxagent/internal/model"
	"fluxagent/internal/model/claude"
	"fluxagent/internal/model/gemini"
	"fluxagent/internal/model/openai"
)

func TestOpenAIProviderCompletesStructuredResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer openai-token" {
			t.Fatalf("expected openai bearer auth, got %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"content": `{"riskTitle":"API degradation","riskSummary":"OpenAI correlated error spikes with rollout timing.","severity":"high","confidenceScore":91,"rationale":"correlated telemetry","rcaHypothesis":"The latest release increased upstream timeout rates.","rcaCauses":["rollout regression","upstream saturation"],"actionType":"notification.sendSlack"}`,
					},
				},
			},
		})
	}))
	defer server.Close()

	provider := openai.Provider{Client: server.Client()}.WithConfig(model.RuntimeConfig{
		Model:     "gpt-5.1",
		Endpoint:  server.URL,
		APIKey:    "openai-token",
		Timeout:   2 * time.Second,
		MaxTokens: 512,
	})

	resp, err := provider.Complete(context.Background(), domain.ModelRequest{
		SystemPrompt: "RCA",
		Messages:     []domain.ModelMessage{{Role: "user", Content: "summary"}},
		Context:      map[string]any{"service": "payments-api"},
	})
	if err != nil {
		t.Fatalf("unexpected openai error: %v", err)
	}
	if resp.Provider != "openai" {
		t.Fatalf("expected openai provider, got %q", resp.Provider)
	}
	if resp.Output["riskSummary"] != "OpenAI correlated error spikes with rollout timing." {
		t.Fatalf("unexpected risk summary: %#v", resp.Output["riskSummary"])
	}
}

func TestClaudeProviderCompletesStructuredResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "claude-token" {
			t.Fatalf("expected claude api key header, got %q", got)
		}
		if got := r.Header.Get("anthropic-version"); got == "" {
			t.Fatal("expected anthropic-version header")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{
					"type": "text",
					"text": `{"riskTitle":"Latency regression","riskSummary":"Claude identified a latency regression after deploy.","severity":"medium","confidenceScore":72,"rationale":"multiple weak signals","rcaHypothesis":"The deploy changed cache hit ratio.","rcaCauses":["cache miss burst"],"actionType":"notification.sendSlack"}`,
				},
			},
		})
	}))
	defer server.Close()

	provider := claude.Provider{Client: server.Client()}.WithConfig(model.RuntimeConfig{
		Model:     "claude-sonnet-4",
		Endpoint:  server.URL,
		APIKey:    "claude-token",
		Timeout:   2 * time.Second,
		MaxTokens: 512,
	})

	resp, err := provider.Complete(context.Background(), domain.ModelRequest{
		SystemPrompt: "RCA",
		Messages:     []domain.ModelMessage{{Role: "user", Content: "summary"}},
		Context:      map[string]any{"service": "payments-api"},
	})
	if err != nil {
		t.Fatalf("unexpected claude error: %v", err)
	}
	if resp.Provider != "claude" {
		t.Fatalf("expected claude provider, got %q", resp.Provider)
	}
}

func TestGeminiProviderCompletesStructuredResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-goog-api-key"); got != "gemini-token" {
			t.Fatalf("expected gemini api key header, got %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{
					"content": map[string]any{
						"parts": []map[string]string{
							{
								"text": `{"riskTitle":"Crash loop","riskSummary":"Gemini linked crash loops to bad startup config.","severity":"high","confidenceScore":88,"rationale":"event and log correlation","rcaHypothesis":"The new config introduced invalid startup arguments.","rcaCauses":["invalid startup args"],"actionType":"notification.sendSlack"}`,
							},
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	provider := gemini.Provider{Client: server.Client()}.WithConfig(model.RuntimeConfig{
		Model:     "gemini-2.5-pro",
		Endpoint:  server.URL,
		APIKey:    "gemini-token",
		Timeout:   2 * time.Second,
		MaxTokens: 512,
	})

	resp, err := provider.Complete(context.Background(), domain.ModelRequest{
		SystemPrompt: "RCA",
		Messages:     []domain.ModelMessage{{Role: "user", Content: "summary"}},
		Context:      map[string]any{"service": "payments-api"},
	})
	if err != nil {
		t.Fatalf("unexpected gemini error: %v", err)
	}
	if resp.Provider != "gemini" {
		t.Fatalf("expected gemini provider, got %q", resp.Provider)
	}
}
