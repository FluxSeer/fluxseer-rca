package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"fluxagent/internal/domain"
	"fluxagent/internal/model"
)

const defaultEndpoint = "https://api.anthropic.com/v1/messages"

type Provider struct {
	Model     string
	Endpoint  string
	APIKey    string
	Timeout   time.Duration
	MaxTokens int
	Client    *http.Client
}

func (p Provider) Name() string {
	return "claude"
}

func (p Provider) WithConfig(config model.RuntimeConfig) model.Provider {
	if config.Model != "" {
		p.Model = config.Model
	}
	if config.Endpoint != "" {
		p.Endpoint = config.Endpoint
	}
	if config.Timeout > 0 {
		p.Timeout = config.Timeout
	}
	if config.MaxTokens > 0 {
		p.MaxTokens = config.MaxTokens
	}
	if config.APIKey != "" {
		p.APIKey = config.APIKey
	}
	return p
}

func (p Provider) Complete(ctx context.Context, req domain.ModelRequest) (domain.ModelResponse, error) {
	if p.Model == "" {
		return domain.ModelResponse{}, &model.ProviderError{
			Reason:  "ProviderUnavailable",
			Message: "claude model is not configured",
		}
	}
	if p.APIKey == "" {
		return domain.ModelResponse{}, &model.ProviderError{
			Reason:  "APIKeyMissing",
			Message: "claude provider api key is not configured",
		}
	}

	userPrompt, err := model.StructuredUserPrompt(req)
	if err != nil {
		return domain.ModelResponse{}, err
	}
	payload, err := json.Marshal(map[string]any{
		"model":      p.Model,
		"max_tokens": maxTokens(p.MaxTokens),
		"system":     model.StructuredSystemPrompt(req.SystemPrompt),
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]string{
					{"type": "text", "text": userPrompt},
				},
			},
		},
	})
	if err != nil {
		return domain.ModelResponse{}, &model.ProviderError{
			Reason:  "ProviderUnavailable",
			Message: fmt.Sprintf("marshal claude request: %v", err),
		}
	}

	httpReq, err := model.NewJSONRequest(ctx, http.MethodPost, firstNonEmpty(p.Endpoint, defaultEndpoint), payload)
	if err != nil {
		return domain.ModelResponse{}, &model.ProviderError{
			Reason:  "ProviderUnavailable",
			Message: fmt.Sprintf("create claude request: %v", err),
		}
	}
	httpReq.Header.Set("x-api-key", p.APIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := model.HTTPClient(p.Timeout, p.Client).Do(httpReq)
	if err != nil {
		return domain.ModelResponse{}, &model.ProviderError{
			Reason:  "ProviderUnavailable",
			Message: fmt.Sprintf("send claude request: %v", err),
		}
	}
	defer resp.Body.Close()

	body, err := model.ReadHTTPBody(resp, p.Name())
	if err != nil {
		return domain.ModelResponse{}, err
	}

	var decoded struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return domain.ModelResponse{}, &model.ProviderError{
			Reason:  "InvalidProviderResponse",
			Message: fmt.Sprintf("decode claude response: %v", err),
		}
	}
	for _, item := range decoded.Content {
		if item.Type == "text" && item.Text != "" {
			return model.ParseStructuredText(p.Name(), p.Model, item.Text)
		}
	}
	return domain.ModelResponse{}, &model.ProviderError{
		Reason:  "InvalidProviderResponse",
		Message: "claude response did not include text content",
	}
}

func maxTokens(value int) int {
	if value > 0 {
		return value
	}
	return 800
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
