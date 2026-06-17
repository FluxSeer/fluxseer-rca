package local

import (
	"context"
	"fmt"

	"fluxagent/internal/domain"
)

type Provider struct {
	Endpoint string
	Model    string
}

func (p Provider) Name() string {
	return "local"
}

func (p Provider) Complete(_ context.Context, req domain.ModelRequest) (domain.ModelResponse, error) {
	if p.Endpoint == "" {
		return domain.ModelResponse{}, fmt.Errorf("local model endpoint is not configured")
	}
	return domain.ModelResponse{
		Provider:   p.Name(),
		Model:      p.Model,
		Structured: true,
		Output: map[string]any{
			"providerHint": req.ProviderHint,
			"endpoint":     p.Endpoint,
		},
	}, nil
}
