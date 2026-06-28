package local

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"fluxagent/internal/domain"
	"fluxagent/internal/model"
)

type Provider struct {
	Endpoint string
	Model    string
	Timeout  time.Duration
	Client   *http.Client
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
	return p
}

func (p Provider) Complete(ctx context.Context, req domain.ModelRequest) (domain.ModelResponse, error) {
	if p.Endpoint == "" {
		return domain.ModelResponse{}, fmt.Errorf("local model endpoint is not configured")
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
		return domain.ModelResponse{}, fmt.Errorf("create local model request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient().Do(httpReq)
	if err != nil {
		return domain.ModelResponse{}, fmt.Errorf("send local model request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return domain.ModelResponse{}, fmt.Errorf("read local model response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return domain.ModelResponse{}, fmt.Errorf("local model endpoint returned %s: %s", resp.Status, bytes.TrimSpace(body))
	}

	var out domain.ModelResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return domain.ModelResponse{}, fmt.Errorf("decode local model response: %w", err)
	}
	if out.Provider == "" {
		out.Provider = p.Name()
	}
	if out.Model == "" {
		out.Model = p.Model
	}
	return out, nil
}

func (p Provider) httpClient() *http.Client {
	if p.Client == nil && p.Timeout == 0 {
		return http.DefaultClient
	}

	base := http.DefaultClient
	if p.Client != nil {
		base = p.Client
	}
	copy := *base
	if p.Timeout > 0 {
		copy.Timeout = p.Timeout
	}
	return &copy
}
