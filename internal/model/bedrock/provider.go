package bedrock

import (
	"context"
	"fmt"

	"fluxagent/internal/domain"
)

type Provider struct {
	ModelID string
}

func (p Provider) Name() string {
	return "bedrock"
}

func (p Provider) Complete(_ context.Context, req domain.ModelRequest) (domain.ModelResponse, error) {
	if p.ModelID == "" {
		return domain.ModelResponse{}, fmt.Errorf("bedrock model ID is not configured")
	}
	return domain.ModelResponse{
		Provider:   p.Name(),
		Model:      p.ModelID,
		Structured: true,
		Output: map[string]any{
			"providerHint": req.ProviderHint,
		},
	}, nil
}
