package modelgateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
	"github.com/FluxSeer/fluxseer-rca/internal/knowledge"
	"github.com/FluxSeer/fluxseer-rca/internal/model"
	"github.com/FluxSeer/fluxseer-rca/internal/model/openai"
	"github.com/FluxSeer/fluxseer-rca/internal/rule"
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

type namedCaptureProvider struct {
	name     string
	request  domain.ModelRequest
	requests int
}

func (p *namedCaptureProvider) Name() string {
	return p.name
}

func (p *namedCaptureProvider) Complete(_ context.Context, req domain.ModelRequest) (domain.ModelResponse, error) {
	p.request = req
	p.requests++
	return domain.ModelResponse{
		Provider:   p.Name(),
		Model:      p.name + "-test",
		Structured: true,
		Output: map[string]any{
			"riskTitle":       "Captured",
			"riskSummary":     "Captured fallback reasoning context.",
			"severity":        "medium",
			"confidenceScore": 70,
			"rationale":       "gateway fallback test",
			"rcaHypothesis":   "Fallback evidence supports reasoning.",
			"rcaCauses":       []string{"fallback evidence correlation"},
			"actionType":      "notification.sendSlack",
		},
	}, nil
}

type staticSecretResolver struct {
	apiKey string
}

func (r staticSecretResolver) ResolveAPIKey(context.Context, *v1alpha1.ModelProvider) (string, error) {
	return r.apiKey, nil
}

type rawFailingProvider struct {
	requests int
}

func (p *rawFailingProvider) Name() string {
	return "raw-failure"
}

func (p *rawFailingProvider) Complete(context.Context, domain.ModelRequest) (domain.ModelResponse, error) {
	p.requests++
	return domain.ModelResponse{}, errors.New("provider transport failed without a typed reason")
}

func TestGatewayTraceSummarizesUntypedProviderRequestFailure(t *testing.T) {
	providerRuntime := &rawFailingProvider{}
	provider := &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "raw-failure", Namespace: "fluxseer-rca-system"},
		Spec:       v1alpha1.ModelProviderSpec{Provider: providerRuntime.Name()},
	}
	gateway := &Gateway{
		Base:      knowledge.NewBase(),
		Providers: model.NewRegistry(providerRuntime),
	}

	_, trace, err := gateway.AnalyzeIngestionWithTrace(context.Background(), provider, domain.IngestionOutput{
		Context: domain.IncidentContext{
			Resource: domain.ResourceRef{Namespace: "prod", Name: "payments-api", Kind: "Deployment"},
			Summary:  "provider transport failure fixture",
		},
	})
	if err == nil {
		t.Fatal("expected provider request failure")
	}
	analyzeErr, ok := err.(*AnalyzeError)
	if !ok || analyzeErr.Reason != "ProviderRequestFailed" {
		t.Fatalf("expected stable ProviderRequestFailed error, got %T %#v", err, err)
	}
	if providerRuntime.requests != 1 {
		t.Fatalf("expected exactly one provider request, got %d", providerRuntime.requests)
	}
	if len(trace.Attempts) != 1 {
		t.Fatalf("expected one bounded gateway attempt, got %#v", trace.Attempts)
	}
	attempt := trace.Attempts[0]
	if attempt.Provider != provider || attempt.Result != "ProviderRequestFailed" || attempt.Reason != "ProviderRequestFailed" {
		t.Fatalf("expected ProviderRequestFailed gateway summary, got %#v", attempt)
	}
}

func TestGatewaySecretReaderFailuresDoNotCallHostedProvider(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	readFailureClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
				return errors.New("simulated Kubernetes API read failure")
			},
		}).
		Build()

	tests := []struct {
		name       string
		secrets    SecretResolver
		wantReason string
	}{
		{name: "reader unavailable", secrets: nil, wantReason: "SecretReaderUnavailable"},
		{name: "read failed", secrets: KubeSecretResolver{Client: readFailureClient}, wantReason: "SecretReadFailed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			providerRequests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				providerRequests++
				_ = r.Body.Close()
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer server.Close()

			provider := &v1alpha1.ModelProvider{
				ObjectMeta: metav1.ObjectMeta{Name: "openai-secret-failure", Namespace: "fluxseer-rca-system"},
				Spec: v1alpha1.ModelProviderSpec{
					Provider: "openai",
					Model:    "gpt-test",
					Endpoint: server.URL,
					APIKeySecretRef: &v1alpha1.SecretKeyRef{
						Name: "openai-secret",
						Key:  "api-key",
					},
					DataPolicy: v1alpha1.ModelProviderDataPolicy{AllowExternalTransmission: true},
				},
			}
			gateway := &Gateway{
				Base:      knowledge.NewBase(),
				Providers: model.NewRegistry(openai.Provider{Client: server.Client()}),
				Secrets:   tc.secrets,
			}

			_, trace, err := gateway.AnalyzeIngestionWithTrace(context.Background(), provider, domain.IngestionOutput{
				Context: domain.IncidentContext{
					Resource: domain.ResourceRef{Namespace: "prod", Name: "payments-api", Kind: "Deployment"},
					Summary:  "secret dependency failure fixture",
				},
			})
			if err == nil {
				t.Fatalf("expected %s", tc.wantReason)
			}
			analyzeErr, ok := err.(*AnalyzeError)
			if !ok || analyzeErr.Reason != tc.wantReason {
				t.Fatalf("expected %s, got %T %#v", tc.wantReason, err, err)
			}
			if providerRequests != 0 {
				t.Fatalf("expected providerRequestCount=0, got %d", providerRequests)
			}
			if len(trace.Attempts) != 1 || trace.Attempts[0].Reason != tc.wantReason {
				t.Fatalf("expected one %s gateway attempt, got %#v", tc.wantReason, trace.Attempts)
			}
		})
	}
}

func TestGatewayBlocksHostedProviderBeforeHTTPRequestWhenExternalTransmissionDenied(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"should-not-be-created","choices":[{"message":{"content":"{\"riskTitle\":\"unexpected\",\"riskSummary\":\"unexpected\",\"severity\":\"low\",\"confidenceScore\":1,\"rationale\":\"unexpected\",\"rcaHypothesis\":\"unexpected\",\"rcaCauses\":[\"unexpected\"],\"actionType\":\"notification.sendSlack\"}"}}]}`))
	}))
	defer server.Close()

	provider := &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "openai-denied", Namespace: "fluxseer-rca-system"},
		Spec: v1alpha1.ModelProviderSpec{
			Provider: "openai",
			Model:    "gpt-test",
			Endpoint: server.URL,
			DataPolicy: v1alpha1.ModelProviderDataPolicy{
				AllowExternalTransmission: false,
			},
		},
	}
	gateway := &Gateway{
		Base:      knowledge.NewBase(),
		Providers: model.NewRegistry(openai.Provider{Client: server.Client()}),
		Secrets:   staticSecretResolver{apiKey: "test-token"},
	}

	_, err := gateway.AnalyzeIngestion(context.Background(), provider, domain.IngestionOutput{
		Context: domain.IncidentContext{
			Resource: domain.ResourceRef{Namespace: "prod", Name: "payments-api", Kind: "Deployment"},
			Summary:  "sensitive evidence must not leave the cluster",
		},
	})
	if err == nil {
		t.Fatal("expected hosted provider data policy denial")
	}
	analyzeErr, ok := err.(*AnalyzeError)
	if !ok || analyzeErr.Reason != "ProviderDataPolicyDenied" {
		t.Fatalf("expected ProviderDataPolicyDenied, got err=%T %#v", err, err)
	}
	if requests != 0 {
		t.Fatalf("expected providerRequestCount=0 when external transmission is denied, got %d", requests)
	}
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
		ObjectMeta: metav1.ObjectMeta{Name: "primary-openai", Namespace: "fluxseer-rca-system"},
		Spec: v1alpha1.ModelProviderSpec{
			Provider: "broken",
			Model:    "gpt-broken",
			FallbackProviderRef: v1alpha1.LocalObjectReference{
				Name: "fallback-capture",
			},
		},
	}
	fallback := &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "fallback-capture", Namespace: "fluxseer-rca-system"},
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
				"fluxseer-rca-system/fallback-capture": fallback,
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

func TestGatewayReevaluatesFallbackProviderExternalTransmissionPolicy(t *testing.T) {
	primary := &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "primary-broken", Namespace: "fluxseer-rca-system"},
		Spec: v1alpha1.ModelProviderSpec{
			Provider: "broken",
			Model:    "gpt-broken",
			FallbackProviderRef: v1alpha1.LocalObjectReference{
				Name: "fallback-openai",
			},
		},
	}
	fallback := &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "fallback-openai", Namespace: "fluxseer-rca-system"},
		Spec: v1alpha1.ModelProviderSpec{
			Provider: "openai",
			Model:    "gpt-5.1",
		},
	}
	openai := &namedCaptureProvider{name: "openai"}
	gateway := &Gateway{
		Base:      knowledge.NewBase(),
		Providers: model.NewRegistry(failingProvider{}, openai),
		Resolver: resolverStub{
			providers: map[string]*v1alpha1.ModelProvider{
				"fluxseer-rca-system/fallback-openai": fallback,
			},
		},
	}

	_, err := gateway.AnalyzeIngestion(context.Background(), primary, domain.IngestionOutput{
		Context: domain.IncidentContext{
			Resource: domain.ResourceRef{Namespace: "prod", Name: "payments-api", Kind: "Deployment"},
			Summary:  "error rate increased after rollout",
		},
	})
	if err == nil {
		t.Fatal("expected fallback hosted provider data policy denial")
	}
	analyzeErr, ok := err.(*AnalyzeError)
	if !ok {
		t.Fatalf("expected AnalyzeError, got %T", err)
	}
	if analyzeErr.Reason != "ProviderDataPolicyDenied" {
		t.Fatalf("expected ProviderDataPolicyDenied, got %q", analyzeErr.Reason)
	}
	if openai.requests != 0 {
		t.Fatalf("expected fallback hosted provider call to be blocked, got %d requests", openai.requests)
	}
}

func TestGatewayTraceRecordsPrimaryAndFallbackAttempts(t *testing.T) {
	primary := &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "primary-broken", Namespace: "fluxseer-rca-system"},
		Spec: v1alpha1.ModelProviderSpec{
			Provider: "broken",
			Model:    "gpt-broken",
			FallbackProviderRef: v1alpha1.LocalObjectReference{
				Name: "fallback-openai",
			},
		},
	}
	fallback := &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "fallback-openai", Namespace: "fluxseer-rca-system"},
		Spec: v1alpha1.ModelProviderSpec{
			Provider: "openai",
			Model:    "gpt-5.1",
		},
	}
	openai := &namedCaptureProvider{name: "openai"}
	gateway := &Gateway{
		Base:      knowledge.NewBase(),
		Providers: model.NewRegistry(failingProvider{}, openai),
		Resolver: resolverStub{
			providers: map[string]*v1alpha1.ModelProvider{
				"fluxseer-rca-system/fallback-openai": fallback,
			},
		},
	}

	_, trace, err := gateway.AnalyzeIngestionWithTrace(context.Background(), primary, domain.IngestionOutput{
		Context: domain.IncidentContext{
			Resource: domain.ResourceRef{Namespace: "prod", Name: "payments-api", Kind: "Deployment"},
			Summary:  "error rate increased after rollout",
		},
	})
	if err == nil {
		t.Fatal("expected fallback hosted provider policy denial")
	}
	if len(trace.Attempts) != 2 {
		t.Fatalf("expected primary and fallback attempts, got %#v", trace.Attempts)
	}
	if trace.Attempts[0].Provider.Name != "primary-broken" || trace.Attempts[0].Result != "ProviderUnavailable" {
		t.Fatalf("expected primary unavailable attempt, got %#v", trace.Attempts[0])
	}
	if trace.Attempts[1].Provider.Name != "fallback-openai" || trace.Attempts[1].Result != "ProviderDataPolicyDenied" {
		t.Fatalf("expected fallback policy denied attempt, got %#v", trace.Attempts[1])
	}
}

func TestGatewayAllowsLocalFallbackAfterHostedProviderPolicyDenial(t *testing.T) {
	primary := &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "primary-openai", Namespace: "fluxseer-rca-system"},
		Spec: v1alpha1.ModelProviderSpec{
			Provider: "openai",
			Model:    "gpt-5.1",
			FallbackProviderRef: v1alpha1.LocalObjectReference{
				Name: "fallback-capture",
			},
		},
	}
	fallback := &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "fallback-capture", Namespace: "fluxseer-rca-system"},
		Spec: v1alpha1.ModelProviderSpec{
			Provider: "capture",
		},
	}
	capture := &captureProvider{}
	gateway := &Gateway{
		Base:      knowledge.NewBase(),
		Providers: model.NewRegistry(capture),
		Resolver: resolverStub{
			providers: map[string]*v1alpha1.ModelProvider{
				"fluxseer-rca-system/fallback-capture": fallback,
			},
		},
	}

	result, err := gateway.AnalyzeIngestion(context.Background(), primary, domain.IngestionOutput{
		Context: domain.IncidentContext{
			Resource: domain.ResourceRef{Namespace: "prod", Name: "payments-api", Kind: "Deployment"},
			Summary:  "error rate increased after rollout",
		},
	})
	if err != nil {
		t.Fatalf("unexpected analyze error: %v", err)
	}
	if result.Provider != "capture" {
		t.Fatalf("expected local fallback provider capture, got %q", result.Provider)
	}
	if capture.request.ProviderHint != "capture" {
		t.Fatalf("expected fallback request to use capture provider, got %q", capture.request.ProviderHint)
	}
}

func TestGatewayReturnsFallbackResolutionFailure(t *testing.T) {
	primary := &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "primary-openai", Namespace: "fluxseer-rca-system"},
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
