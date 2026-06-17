package gemini

import (
	"context"
	"fmt"

	"fluxagent/internal/domain"
)

type Provider struct {
	Model string
}

func (p Provider) Name() string {
	return "gemini"
}

func (p Provider) Complete(_ context.Context, req domain.ModelRequest) (domain.ModelResponse, error) {
	if p.Model == "" {
		return domain.ModelResponse{}, fmt.Errorf("gemini model is not configured")
	}
	return domain.ModelResponse{
		Provider:   p.Name(),
		Model:      p.Model,
		Structured: true,
		Output: map[string]any{
			"providerHint": req.ProviderHint,
		},
	}, nil
}
