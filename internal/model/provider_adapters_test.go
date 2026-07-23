package model_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestOpenAIProviderRetriesRateLimitAndCompletes(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"slow down"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"content": `{"riskTitle":"Recovered","riskSummary":"OpenAI responded after retry.","severity":"medium","confidenceScore":65,"rationale":"retry succeeded","rcaHypothesis":"transient rate limit cleared.","rcaCauses":["provider throttling"],"actionType":"notification.sendSlack"}`,
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
	})
	if err != nil {
		t.Fatalf("unexpected openai error: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
	if resp.Output["riskSummary"] != "OpenAI responded after retry." {
		t.Fatalf("unexpected risk summary after retry: %#v", resp.Output["riskSummary"])
	}
}

func TestOpenAIProviderRejectsIncompleteStructuredContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"content": `{"riskTitle":"Incomplete","riskSummary":"missing confidence","severity":"high","rationale":"test","rcaHypothesis":"provider omitted required fields","rcaCauses":["bad shape"],"actionType":"notification.sendSlack"}`,
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

	_, err := provider.Complete(context.Background(), domain.ModelRequest{
		SystemPrompt: "RCA",
		Messages:     []domain.ModelMessage{{Role: "user", Content: "summary"}},
	})
	if err == nil {
		t.Fatal("expected openai invalid response error")
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

func TestClaudeProviderMapsUnauthorizedStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad api key"}`))
	}))
	defer server.Close()

	provider := claude.Provider{Client: server.Client()}.WithConfig(model.RuntimeConfig{
		Model:     "claude-sonnet-4",
		Endpoint:  server.URL,
		APIKey:    "claude-token",
		Timeout:   2 * time.Second,
		MaxTokens: 512,
	})

	_, err := provider.Complete(context.Background(), domain.ModelRequest{
		SystemPrompt: "RCA",
		Messages:     []domain.ModelMessage{{Role: "user", Content: "summary"}},
	})
	if err == nil {
		t.Fatal("expected claude error")
	}
	providerErr, ok := err.(*model.ProviderError)
	if !ok {
		t.Fatalf("expected ProviderError, got %T", err)
	}
	if providerErr.Reason != "ProviderAuthFailed" {
		t.Fatalf("expected ProviderAuthFailed, got %q", providerErr.Reason)
	}
}

func TestGeminiProviderMapsInvalidRequestStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"unsupported model"}`))
	}))
	defer server.Close()

	provider := gemini.Provider{Client: server.Client()}.WithConfig(model.RuntimeConfig{
		Model:     "gemini-2.5-pro",
		Endpoint:  server.URL,
		APIKey:    "gemini-token",
		Timeout:   2 * time.Second,
		MaxTokens: 512,
	})

	_, err := provider.Complete(context.Background(), domain.ModelRequest{
		SystemPrompt: "RCA",
		Messages:     []domain.ModelMessage{{Role: "user", Content: "summary"}},
	})
	if err == nil {
		t.Fatal("expected gemini error")
	}
	providerErr, ok := err.(*model.ProviderError)
	if !ok {
		t.Fatalf("expected ProviderError, got %T", err)
	}
	if providerErr.Reason != "ProviderRequestInvalid" {
		t.Fatalf("expected ProviderRequestInvalid, got %q", providerErr.Reason)
	}
}
