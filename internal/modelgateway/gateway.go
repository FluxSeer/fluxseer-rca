package modelgateway

import (
	"context"
	"fmt"
	"strings"
	"time"

	"fluxagent/api/v1alpha1"
	"fluxagent/internal/domain"
	"fluxagent/internal/evidence"
	"fluxagent/internal/knowledge"
	"fluxagent/internal/model"
	"fluxagent/internal/reasoning"
	"fluxagent/internal/rule"
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
}

func (g *Gateway) Analyze(ctx context.Context, provider *v1alpha1.ModelProvider, target domain.ResourceRef, matches []rule.Match, now time.Time) (domain.ReasoningOutput, error) {
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

	providerType := strings.ToLower(strings.TrimSpace(provider.Spec.Provider))
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
	result, err := engine.Analyze(ctx, redactor.RedactIngestion(buildIngestionOutput(target, matches, now)))
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
	case "openai", "gemini", "claude", "bedrock":
		return true
	default:
		return false
	}
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
