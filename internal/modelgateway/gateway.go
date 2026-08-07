package modelgateway

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
	"github.com/FluxSeer/fluxseer-rca/internal/evidence"
	"github.com/FluxSeer/fluxseer-rca/internal/knowledge"
	"github.com/FluxSeer/fluxseer-rca/internal/model"
	"github.com/FluxSeer/fluxseer-rca/internal/reasoning"
	"github.com/FluxSeer/fluxseer-rca/internal/rule"
)

type AnalyzeError struct {
	Reason  string
	Message string
}

func (e *AnalyzeError) Error() string {
	return e.Message
}

type Gateway struct {
	Base      *knowledge.Base
	Providers *model.Registry
	Redactor  evidence.Redactor
	Secrets   SecretResolver
	Resolver  ProviderResolver
}

type Trace struct {
	Attempts []Attempt
}

type Attempt struct {
	Provider *v1alpha1.ModelProvider
	Result   string
	Reason   string
}

type ProviderInputFunc func(*v1alpha1.ModelProvider) (domain.IngestionOutput, error)

const maxTraceAttempts = 8

func (g *Gateway) Analyze(ctx context.Context, provider *v1alpha1.ModelProvider, target domain.ResourceRef, matches []rule.Match, now time.Time) (domain.ReasoningOutput, error) {
	return g.AnalyzeIngestion(ctx, provider, buildIngestionOutput(target, matches, now))
}

func (g *Gateway) AnalyzeIngestion(ctx context.Context, provider *v1alpha1.ModelProvider, input domain.IngestionOutput) (domain.ReasoningOutput, error) {
	result, _, err := g.AnalyzeIngestionWithTrace(ctx, provider, input)
	return result, err
}

func (g *Gateway) AnalyzeIngestionWithTrace(ctx context.Context, provider *v1alpha1.ModelProvider, input domain.IngestionOutput) (domain.ReasoningOutput, Trace, error) {
	return g.AnalyzeIngestionWithProviderInputTrace(ctx, provider, func(*v1alpha1.ModelProvider) (domain.IngestionOutput, error) {
		return input, nil
	})
}

func (g *Gateway) AnalyzeIngestionWithProviderInputTrace(ctx context.Context, provider *v1alpha1.ModelProvider, inputForProvider ProviderInputFunc) (domain.ReasoningOutput, Trace, error) {
	trace := Trace{}
	result, err := g.analyzeIngestionWithFallback(ctx, provider, inputForProvider, map[string]struct{}{}, &trace)
	return result, trace, err
}

func (g *Gateway) analyzeIngestionWithFallback(ctx context.Context, provider *v1alpha1.ModelProvider, inputForProvider ProviderInputFunc, visited map[string]struct{}, trace *Trace) (domain.ReasoningOutput, error) {
	if provider == nil {
		return domain.ReasoningOutput{}, &AnalyzeError{
			Reason:  "ProviderUnavailable",
			Message: "model provider is nil",
		}
	}
	if g.Providers == nil {
		return domain.ReasoningOutput{}, &AnalyzeError{
			Reason:  "GatewayUnavailable",
			Message: "model gateway provider registry is not configured",
		}
	}

	if key := providerKey(provider); key != "" {
		visited[key] = struct{}{}
	}

	input, err := inputForProvider(provider)
	if err != nil {
		recordTraceAttempt(trace, provider, err)
		analyzeErr, ok := err.(*AnalyzeError)
		if !ok || !shouldAttemptProviderFallback(provider, analyzeErr.Reason) {
			return domain.ReasoningOutput{}, err
		}
		fallbackProvider, fallbackErr := g.resolveFallbackProvider(ctx, provider, analyzeErr, visited)
		if fallbackErr != nil {
			return domain.ReasoningOutput{}, fallbackErr
		}
		return g.analyzeIngestionWithFallback(ctx, fallbackProvider, inputForProvider, visited, trace)
	}

	result, err := g.analyzeSingleProvider(ctx, provider, input)
	if err != nil {
		recordTraceAttempt(trace, provider, err)
		analyzeErr, ok := err.(*AnalyzeError)
		if !ok || !shouldAttemptProviderFallback(provider, analyzeErr.Reason) {
			return domain.ReasoningOutput{}, err
		}
		fallbackProvider, fallbackErr := g.resolveFallbackProvider(ctx, provider, analyzeErr, visited)
		if fallbackErr != nil {
			return domain.ReasoningOutput{}, fallbackErr
		}
		return g.analyzeIngestionWithFallback(ctx, fallbackProvider, inputForProvider, visited, trace)
	}
	recordTraceAttempt(trace, provider, nil)
	return result, nil
}

func recordTraceAttempt(trace *Trace, provider *v1alpha1.ModelProvider, err error) {
	if trace == nil || provider == nil {
		return
	}
	if len(trace.Attempts) >= maxTraceAttempts {
		return
	}
	attempt := Attempt{Provider: provider, Result: "Succeeded", Reason: "Completed"}
	if err != nil {
		attempt.Result = "Failed"
		attempt.Reason = "ProviderRequestFailed"
		if analyzeErr, ok := err.(*AnalyzeError); ok {
			attempt.Result = analyzeErr.Reason
			attempt.Reason = analyzeErr.Reason
		}
	}
	trace.Attempts = append(trace.Attempts, attempt)
}

func (g *Gateway) analyzeSingleProvider(ctx context.Context, provider *v1alpha1.ModelProvider, input domain.IngestionOutput) (domain.ReasoningOutput, error) {
	providerType := strings.ToLower(strings.TrimSpace(provider.Spec.Provider))
	if requiresAPIKey(providerType) && !provider.Spec.DataPolicy.AllowExternalTransmission {
		return domain.ReasoningOutput{}, &AnalyzeError{
			Reason:  "ProviderDataPolicyDenied",
			Message: fmt.Sprintf("ModelProvider %q requires spec.dataPolicy.allowExternalTransmission=true before evidence can be sent to hosted provider %q", provider.Name, provider.Spec.Provider),
		}
	}
	modelProvider, ok := g.Providers.Get(providerType)
	if !ok {
		return domain.ReasoningOutput{}, &AnalyzeError{
			Reason:  "ProviderUnsupported",
			Message: fmt.Sprintf("model provider type %q is not supported by the gateway", provider.Spec.Provider),
		}
	}
	configuredProvider, err := g.configureProvider(ctx, modelProvider, provider)
	if err != nil {
		return domain.ReasoningOutput{}, err
	}

	base := g.Base
	if base == nil {
		base = knowledge.NewBase()
	}
	redactor := g.Redactor
	if redactor == nil {
		defaultRedactor := evidence.NewPatternRedactor()
		redactor = defaultRedactor
	}
	engine := reasoning.NewEngine(base, configuredProvider)
	result, err := engine.Analyze(ctx, redactor.RedactIngestion(input))
	if err != nil {
		if providerErr, ok := err.(*model.ProviderError); ok {
			return domain.ReasoningOutput{}, &AnalyzeError{
				Reason:  providerErr.Reason,
				Message: providerErr.Message,
			}
		}
		return domain.ReasoningOutput{}, err
	}
	return result, nil
}

func shouldAttemptProviderFallback(provider *v1alpha1.ModelProvider, reason string) bool {
	if provider == nil || strings.TrimSpace(provider.Spec.FallbackProviderRef.Name) == "" {
		return false
	}
	switch reason {
	case "ProviderUnavailable", "ProviderUnsupported", "ProviderDataPolicyDenied", "ProviderAuthFailed", "ProviderRateLimited", "ProviderRequestInvalid", "InvalidProviderResponse", "APIKeyMissing", "SecretRefMissing", "SecretReaderUnavailable", "SecretReadFailed", "SecretNotFound", "SecretKeyMissing", "SecretValueEmpty":
		return true
	default:
		return false
	}
}

func (g *Gateway) resolveFallbackProvider(ctx context.Context, provider *v1alpha1.ModelProvider, primaryErr *AnalyzeError, visited map[string]struct{}) (*v1alpha1.ModelProvider, error) {
	if g.Resolver == nil {
		return nil, &AnalyzeError{
			Reason:  "ResolverUnavailable",
			Message: fmt.Sprintf("ModelProvider %q failed with %s and fallback provider %q cannot be resolved because the gateway resolver is not configured", provider.Name, primaryErr.Reason, provider.Spec.FallbackProviderRef.Name),
		}
	}

	fallback, err := g.Resolver.Resolve(ctx, provider.Namespace, refOrNil(provider.Spec.FallbackProviderRef))
	if err != nil {
		if resolveErr, ok := err.(*ResolveError); ok {
			return nil, &AnalyzeError{
				Reason:  resolveErr.Reason,
				Message: fmt.Sprintf("ModelProvider %q failed with %s and fallback provider %q could not be resolved: %s", provider.Name, primaryErr.Reason, provider.Spec.FallbackProviderRef.Name, resolveErr.Message),
			}
		}
		return nil, err
	}
	if fallback == nil {
		return nil, &AnalyzeError{
			Reason:  "ProviderUnavailable",
			Message: fmt.Sprintf("ModelProvider %q failed with %s and fallback provider %q resolved to nil", provider.Name, primaryErr.Reason, provider.Spec.FallbackProviderRef.Name),
		}
	}
	if _, seen := visited[providerKey(fallback)]; seen {
		return nil, &AnalyzeError{
			Reason:  "ProviderFallbackLoop",
			Message: fmt.Sprintf("ModelProvider %q fallback chain loops through %q", provider.Name, fallback.Name),
		}
	}
	return fallback, nil
}

func (g *Gateway) configureProvider(ctx context.Context, provider model.Provider, obj *v1alpha1.ModelProvider) (model.Provider, error) {
	configurable, ok := provider.(model.ConfigurableProvider)
	if !ok {
		return provider, nil
	}

	spec := obj.Spec
	config := model.RuntimeConfig{
		Model:     spec.Model,
		Endpoint:  spec.Endpoint,
		Timeout:   spec.Timeout.Duration,
		MaxTokens: spec.MaxTokens,
	}
	if requiresAPIKey(spec.Provider) {
		if g.Secrets == nil {
			return nil, &AnalyzeError{
				Reason:  "SecretReaderUnavailable",
				Message: fmt.Sprintf("model provider %q requires apiKeySecretRef but no secret resolver is configured", obj.Name),
			}
		}
		apiKey, err := g.Secrets.ResolveAPIKey(ctx, obj)
		if err != nil {
			return nil, err
		}
		config.APIKey = apiKey
	}
	return configurable.WithConfig(config), nil
}

func requiresAPIKey(providerType string) bool {
	switch strings.ToLower(strings.TrimSpace(providerType)) {
	case "openai", "gemini", "claude":
		return true
	default:
		return false
	}
}

func providerKey(provider *v1alpha1.ModelProvider) string {
	if provider == nil {
		return ""
	}
	return provider.Namespace + "/" + provider.Name
}

func refOrNil(ref v1alpha1.LocalObjectReference) *v1alpha1.LocalObjectReference {
	if strings.TrimSpace(ref.Name) == "" {
		return nil
	}
	copy := ref
	return &copy
}

func buildIngestionOutput(target domain.ResourceRef, matches []rule.Match, now time.Time) domain.IngestionOutput {
	signals := make([]domain.Signal, 0, len(matches))
	references := make([]domain.Evidence, 0, len(matches))
	logs := []string{}
	events := []string{}
	metrics := map[string]float64{}
	timeline := make([]domain.TimelineEvent, 0, len(matches))

	for index, match := range matches {
		kind := signalKind(match.Signal.Type)
		severity := normalizeMatchSeverity(match.Severity)
		signals = append(signals, domain.Signal{
			ID:        fmt.Sprintf("rule-signal-%d", index+1),
			Kind:      kind,
			Source:    signalSource(match.Signal.Type),
			Severity:  severity,
			Message:   match.Summary,
			Resource:  target,
			Timestamp: now,
		})
		timeline = append(timeline, domain.TimelineEvent{
			Timestamp: now,
			Kind:      kind,
			Summary:   match.Summary,
		})

		for _, evidence := range match.Evidence {
			references = append(references, domain.Evidence{
				Kind:    evidence.Kind,
				Source:  evidence.Source,
				Summary: evidence.Summary,
				Query:   evidence.Query,
				Reason:  evidence.Reason,
				Link:    evidence.Link,
			})
			switch evidence.Kind {
			case "log":
				logs = append(logs, evidence.Summary)
			case "event":
				events = append(events, evidence.Summary)
			case "metric":
				metrics[match.Signal.Name] = float64(len(match.Evidence))
			}
		}
	}

	return domain.IngestionOutput{
		Context: domain.IncidentContext{
			ID:          fmt.Sprintf("riskrule-%s-%s", target.Namespace, target.Name),
			Cluster:     target.Cluster,
			Service:     target.Service,
			Resource:    target,
			Summary:     combineMatchSummaries(matches),
			Signals:     signals,
			GeneratedAt: now,
		},
		Evidence: domain.EvidenceBundle{
			Logs:       logs,
			Metrics:    metrics,
			Events:     events,
			References: references,
		},
		Signals: signals,
		Timeline: domain.ResourceTimeline{
			Resource: target,
			Events:   timeline,
		},
		DedupedFrom: len(matches),
	}
}

func combineMatchSummaries(matches []rule.Match) string {
	parts := make([]string, 0, len(matches))
	for _, match := range matches {
		parts = append(parts, match.Summary)
	}
	return strings.Join(parts, " | ")
}

func signalKind(signalType string) string {
	switch strings.ToLower(strings.TrimSpace(signalType)) {
	case "prometheus":
		return "metric"
	case "loki":
		return "log"
	case "kubernetesevent":
		return "event"
	default:
		return "signal"
	}
}

func signalSource(signalType string) string {
	switch strings.ToLower(strings.TrimSpace(signalType)) {
	case "prometheus":
		return "prometheus"
	case "loki":
		return "loki"
	case "kubernetesevent":
		return "kubernetes-events"
	default:
		return signalType
	}
}

func normalizeMatchSeverity(value string) domain.Severity {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(domain.SeverityLow):
		return domain.SeverityLow
	case string(domain.SeverityHigh):
		return domain.SeverityHigh
	case string(domain.SeverityUnsafe):
		return domain.SeverityUnsafe
	default:
		return domain.SeverityMedium
	}
}
