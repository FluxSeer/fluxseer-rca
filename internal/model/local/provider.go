package local

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"fluxagent/internal/domain"
	"fluxagent/internal/model"
)

type Provider struct {
	Endpoint  string
	Model     string
	Timeout   time.Duration
	MaxTokens int
	APIKey    string
	Client    *http.Client
}

func (p Provider) Name() string {
	return "local"
}

func (p Provider) WithConfig(config model.RuntimeConfig) model.Provider {
	if config.Endpoint != "" {
		p.Endpoint = config.Endpoint
	}
	if config.Model != "" {
		p.Model = config.Model
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
	if p.Endpoint == "" {
		return domain.ModelResponse{}, &model.ProviderError{
			Reason:  "ProviderUnavailable",
			Message: "local model endpoint is not configured",
		}
	}

	payload, err := json.Marshal(struct {
		Model   string              `json:"model,omitempty"`
		Request domain.ModelRequest `json:"request"`
	}{
		Model:   p.Model,
		Request: req,
	})
	if err != nil {
		return domain.ModelResponse{}, fmt.Errorf("marshal local model request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Endpoint, bytes.NewReader(payload))
	if err != nil {
		return domain.ModelResponse{}, &model.ProviderError{
			Reason:  "ProviderUnavailable",
			Message: fmt.Sprintf("create local model request: %v", err),
		}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.APIKey)
	}

	resp, err := p.httpClient().Do(httpReq)
	if err != nil {
		return domain.ModelResponse{}, &model.ProviderError{
			Reason:  "ProviderUnavailable",
			Message: fmt.Sprintf("send local model request: %v", err),
		}
	}
	defer resp.Body.Close()

	body, err := model.ReadHTTPBody(resp, p.Name())
	if err != nil {
		return domain.ModelResponse{}, err
	}

	var out domain.ModelResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return domain.ModelResponse{}, &model.ProviderError{
			Reason:  "InvalidProviderResponse",
			Message: fmt.Sprintf("decode local model response: %v", err),
		}
	}
	if out.Provider == "" {
		out.Provider = p.Name()
	}
	if out.Model == "" {
		out.Model = p.Model
	}
	if err := model.ValidateModelResponse(out); err != nil {
		return domain.ModelResponse{}, err
	}
	return out, nil
}

func (p Provider) httpClient() *http.Client {
	return model.HTTPClient(p.Timeout, p.Client)
}
