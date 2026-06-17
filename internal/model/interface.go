package model

import (
	"context"

	"fluxagent/internal/domain"
)

type Provider interface {
	Name() string
	Complete(ctx context.Context, req domain.ModelRequest) (domain.ModelResponse, error)
}

type Registry struct {
	providers map[string]Provider
}

func NewRegistry(items ...Provider) *Registry {
	registry := &Registry{providers: map[string]Provider{}}
	for _, item := range items {
		registry.providers[item.Name()] = item
	}
	return registry
}

func (r *Registry) Register(provider Provider) {
	if r.providers == nil {
		r.providers = map[string]Provider{}
	}
	r.providers[provider.Name()] = provider
}

func (r *Registry) Get(name string) (Provider, bool) {
	provider, ok := r.providers[name]
	return provider, ok
}
