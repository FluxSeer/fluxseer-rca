package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"fluxagent/internal/domain"
	"fluxagent/internal/model"
)

const defaultEndpoint = "https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent"

type Provider struct {
	Model     string
	Endpoint  string
	APIKey    string
	Timeout   time.Duration
	MaxTokens int
	Client    *http.Client
}

func (p Provider) Name() string {
	return "gemini"
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
			Message: "gemini model is not configured",
		}
	}
	if p.APIKey == "" {
		return domain.ModelResponse{}, &model.ProviderError{
			Reason:  "APIKeyMissing",
			Message: "gemini provider api key is not configured",
		}
	}

	userPrompt, err := model.StructuredUserPrompt(req)
	if err != nil {
		return domain.ModelResponse{}, err
	}
	payload, err := json.Marshal(map[string]any{
		"systemInstruction": map[string]any{
			"parts": []map[string]string{
				{"text": model.StructuredSystemPrompt(req.SystemPrompt)},
			},
		},
		"contents": []map[string]any{
			{
				"role": "user",
				"parts": []map[string]string{
					{"text": userPrompt},
				},
			},
		},
		"generationConfig": map[string]any{
			"responseMimeType": "application/json",
			"maxOutputTokens":  maxTokens(p.MaxTokens),
			"temperature":      0.2,
		},
	})
	if err != nil {
		return domain.ModelResponse{}, &model.ProviderError{
			Reason:  "ProviderUnavailable",
			Message: fmt.Sprintf("marshal gemini request: %v", err),
		}
	}

	endpoint := p.Endpoint
	if endpoint == "" {
		endpoint = fmt.Sprintf(defaultEndpoint, url.PathEscape(p.Model))
	}
	httpReq, err := model.NewJSONRequest(ctx, http.MethodPost, endpoint, payload)
	if err != nil {
		return domain.ModelResponse{}, &model.ProviderError{
			Reason:  "ProviderUnavailable",
			Message: fmt.Sprintf("create gemini request: %v", err),
		}
	}
	httpReq.Header.Set("x-goog-api-key", p.APIKey)

	resp, err := model.HTTPClient(p.Timeout, p.Client).Do(httpReq)
	if err != nil {
		return domain.ModelResponse{}, &model.ProviderError{
			Reason:  "ProviderUnavailable",
			Message: fmt.Sprintf("send gemini request: %v", err),
		}
	}
	defer resp.Body.Close()

	body, err := model.ReadHTTPBody(resp, p.Name())
	if err != nil {
		return domain.ModelResponse{}, err
	}

	var decoded struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return domain.ModelResponse{}, &model.ProviderError{
			Reason:  "InvalidProviderResponse",
			Message: fmt.Sprintf("decode gemini response: %v", err),
		}
	}
	if len(decoded.Candidates) == 0 {
		return domain.ModelResponse{}, &model.ProviderError{
			Reason:  "InvalidProviderResponse",
			Message: "gemini response did not include any candidates",
		}
	}
	for _, part := range decoded.Candidates[0].Content.Parts {
		if part.Text != "" {
			return model.ParseStructuredText(p.Name(), p.Model, part.Text)
		}
	}
	return domain.ModelResponse{}, &model.ProviderError{
		Reason:  "InvalidProviderResponse",
		Message: "gemini response did not include text content",
	}
}

func maxTokens(value int) int {
	if value > 0 {
		return value
	}
	return 800
}
