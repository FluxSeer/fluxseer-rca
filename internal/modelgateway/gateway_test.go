package modelgateway

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"fluxagent/api/v1alpha1"
	"fluxagent/internal/domain"
	"fluxagent/internal/knowledge"
	"fluxagent/internal/model"
	"fluxagent/internal/rule"
)

type captureProvider struct {
	request domain.ModelRequest
}

func (p *captureProvider) Name() string {
	return "capture"
}

func (p *captureProvider) Complete(_ context.Context, req domain.ModelRequest) (domain.ModelResponse, error) {
	p.request = req
	return domain.ModelResponse{
		Provider:   p.Name(),
		Model:      "capture-test",
		Structured: true,
		Output: map[string]any{
			"riskTitle":       "Captured",
			"riskSummary":     "Captured redacted reasoning context.",
			"severity":        "medium",
			"confidenceScore": 70,
			"rationale":       "gateway test",
			"rcaHypothesis":   "Redacted evidence still supports reasoning.",
			"rcaCauses":       []string{"redacted evidence correlation"},
			"actionType":      "notification.sendSlack",
		},
	}, nil
}

func TestGatewayRedactsEvidenceBeforeProviderCall(t *testing.T) {
	provider := &captureProvider{}
	gateway := &Gateway{
		Base:      knowledge.NewBase(),
		Providers: model.NewRegistry(provider),
	}
	modelProvider := &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "capture-provider", Namespace: "prod"},
		Spec: v1alpha1.ModelProviderSpec{
			Provider: "capture",
		},
	}

	matches := []rule.Match{
		{
			Signal:   v1alpha1.RiskRuleSignal{Name: "error-logs", Type: "loki"},
			Summary:  "error token=super-secret hit Authorization: Bearer abc123456789",
			Severity: "high",
			Evidence: []v1alpha1.EvidenceRef{
				{
					Kind:    "log",
					Source:  "loki",
					Summary: "password=letmein token=super-secret",
					Query:   `{app="payments"} |= "Authorization: Bearer abc123456789"`,
				},
			},
		},
	}

	_, err := gateway.Analyze(context.Background(), modelProvider, domain.ResourceRef{
		Namespace: "prod",
		Name:      "payments-api",
		Kind:      "Deployment",
		Service:   "payments-api",
	}, matches, time.Date(2026, 6, 28, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("unexpected analyze error: %v", err)
	}

	if got := provider.request.Messages[0].Content; got == "" || containsSecret(got) {
		t.Fatalf("expected redacted summary in provider message, got %q", got)
	}
	evidenceValue, ok := provider.request.Context["evidence"].(domain.EvidenceBundle)
	if !ok {
		t.Fatalf("expected evidence bundle in request context, got %#v", provider.request.Context["evidence"])
	}
	if len(evidenceValue.Logs) != 1 || containsSecret(evidenceValue.Logs[0]) {
		t.Fatalf("expected redacted log evidence, got %#v", evidenceValue.Logs)
	}
	if len(evidenceValue.References) != 1 || containsSecret(evidenceValue.References[0].Query) {
		t.Fatalf("expected redacted reference query, got %#v", evidenceValue.References)
	}
}

func containsSecret(input string) bool {
	for _, fragment := range []string{"super-secret", "abc123456789", "letmein"} {
		if strings.Contains(input, fragment) {
			return true
		}
	}
	return false
}

type failingProvider struct {
	model string
}

func (p failingProvider) Name() string {
	return "broken"
}

func (p failingProvider) WithConfig(config model.RuntimeConfig) model.Provider {
	if config.Model != "" {
		p.model = config.Model
	}
	return p
}

func (p failingProvider) Complete(context.Context, domain.ModelRequest) (domain.ModelResponse, error) {
	return domain.ModelResponse{}, &model.ProviderError{
		Reason:  "ProviderUnavailable",
		Message: fmt.Sprintf("provider model %q is unavailable", p.model),
	}
}

type resolverStub struct {
	providers map[string]*v1alpha1.ModelProvider
	err       error
}

func (r resolverStub) Resolve(_ context.Context, namespace string, ref *v1alpha1.LocalObjectReference) (*v1alpha1.ModelProvider, error) {
	if r.err != nil {
		return nil, r.err
	}
	if ref == nil || ref.Name == "" {
		return DefaultHeuristicProvider(namespace), nil
	}
	provider, ok := r.providers[namespace+"/"+ref.Name]
	if !ok {
		return nil, &ResolveError{
			Reason:  "ProviderNotFound",
			Message: fmt.Sprintf("ModelProvider %q was not found in namespace %q", ref.Name, namespace),
		}
	}
	return provider, nil
}

func TestGatewayFallsBackToSecondaryProvider(t *testing.T) {
	primary := &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "primary-openai", Namespace: "fluxagent-system"},
		Spec: v1alpha1.ModelProviderSpec{
			Provider: "broken",
			Model:    "gpt-broken",
			FallbackProviderRef: v1alpha1.LocalObjectReference{
				Name: "fallback-capture",
			},
		},
	}
	fallback := &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "fallback-capture", Namespace: "fluxagent-system"},
		Spec: v1alpha1.ModelProviderSpec{
			Provider: "capture",
		},
	}
	capture := &captureProvider{}
	gateway := &Gateway{
		Base:      knowledge.NewBase(),
		Providers: model.NewRegistry(failingProvider{}, capture),
		Resolver: resolverStub{
			providers: map[string]*v1alpha1.ModelProvider{
				"fluxagent-system/fallback-capture": fallback,
			},
		},
	}

	result, err := gateway.AnalyzeIngestion(context.Background(), primary, domain.IngestionOutput{
		Context: domain.IncidentContext{
			Resource: domain.ResourceRef{
				Namespace: "prod",
				Name:      "payments-api",
				Kind:      "Deployment",
				Service:   "payments-api",
			},
			Summary: "error rate increased after rollout",
		},
	})
	if err != nil {
		t.Fatalf("unexpected analyze error: %v", err)
	}
	if result.Provider != "capture" {
		t.Fatalf("expected fallback provider capture, got %q", result.Provider)
	}
	if capture.request.ProviderHint != "capture" {
		t.Fatalf("expected fallback request to use capture provider, got %q", capture.request.ProviderHint)
	}
}

func TestGatewayReturnsFallbackResolutionFailure(t *testing.T) {
	primary := &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "primary-openai", Namespace: "fluxagent-system"},
		Spec: v1alpha1.ModelProviderSpec{
			Provider: "broken",
			Model:    "gpt-broken",
			FallbackProviderRef: v1alpha1.LocalObjectReference{
				Name: "missing-provider",
			},
		},
	}
	gateway := &Gateway{
		Base:      knowledge.NewBase(),
		Providers: model.NewRegistry(failingProvider{}),
		Resolver:  resolverStub{},
	}

	_, err := gateway.AnalyzeIngestion(context.Background(), primary, domain.IngestionOutput{
		Context: domain.IncidentContext{
			Resource: domain.ResourceRef{
				Namespace: "prod",
				Name:      "payments-api",
				Kind:      "Deployment",
			},
			Summary: "error rate increased after rollout",
		},
	})
	if err == nil {
		t.Fatal("expected analyze error")
	}
	analyzeErr, ok := err.(*AnalyzeError)
	if !ok {
		t.Fatalf("expected AnalyzeError, got %T", err)
	}
	if analyzeErr.Reason != "ProviderNotFound" {
		t.Fatalf("expected ProviderNotFound, got %q", analyzeErr.Reason)
	}
	if !strings.Contains(analyzeErr.Message, "fallback provider") {
		t.Fatalf("expected fallback provider context in error, got %q", analyzeErr.Message)
	}
}
