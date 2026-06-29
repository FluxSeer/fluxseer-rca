package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"fluxagent/internal/domain"
	"fluxagent/internal/model"
)

const defaultEndpoint = "https://api.openai.com/v1/chat/completions"

type Provider struct {
	Model     string
	Endpoint  string
	APIKey    string
	Timeout   time.Duration
	MaxTokens int
	Client    *http.Client
}

func (p Provider) Name() string {
	return "openai"
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
			Message: "openai model is not configured",
		}
	}
	if p.APIKey == "" {
		return domain.ModelResponse{}, &model.ProviderError{
			Reason:  "APIKeyMissing",
			Message: "openai provider api key is not configured",
		}
	}

	userPrompt, err := model.StructuredUserPrompt(req)
	if err != nil {
		return domain.ModelResponse{}, err
	}
	payload, err := json.Marshal(map[string]any{
		"model": p.Model,
		"messages": []map[string]string{
			{"role": "system", "content": model.StructuredSystemPrompt(req.SystemPrompt)},
			{"role": "user", "content": userPrompt},
		},
		"response_format": map[string]string{"type": "json_object"},
		"max_tokens":      maxTokens(p.MaxTokens),
	})
	if err != nil {
		return domain.ModelResponse{}, &model.ProviderError{
			Reason:  "ProviderUnavailable",
			Message: fmt.Sprintf("marshal openai request: %v", err),
		}
	}

	httpReq, err := model.NewJSONRequest(ctx, http.MethodPost, firstNonEmpty(p.Endpoint, defaultEndpoint), payload)
	if err != nil {
		return domain.ModelResponse{}, &model.ProviderError{
			Reason:  "ProviderUnavailable",
			Message: fmt.Sprintf("create openai request: %v", err),
		}
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.APIKey)

	resp, err := model.HTTPClient(p.Timeout, p.Client).Do(httpReq)
	if err != nil {
		return domain.ModelResponse{}, &model.ProviderError{
			Reason:  "ProviderUnavailable",
			Message: fmt.Sprintf("send openai request: %v", err),
		}
	}
	defer resp.Body.Close()

	body, err := model.ReadHTTPBody(resp, p.Name())
	if err != nil {
		return domain.ModelResponse{}, err
	}

	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return domain.ModelResponse{}, &model.ProviderError{
			Reason:  "InvalidProviderResponse",
			Message: fmt.Sprintf("decode openai response: %v", err),
		}
	}
	if len(decoded.Choices) == 0 || decoded.Choices[0].Message.Content == "" {
		return domain.ModelResponse{}, &model.ProviderError{
			Reason:  "InvalidProviderResponse",
			Message: "openai response did not include a message content payload",
		}
	}
	return model.ParseStructuredText(p.Name(), p.Model, decoded.Choices[0].Message.Content)
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
