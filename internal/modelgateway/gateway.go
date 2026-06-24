package modelgateway

import (
	"context"
	"fmt"
	"strings"
	"time"

	"fluxagent/api/v1alpha1"
	"fluxagent/internal/domain"
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

	base := g.Base
	if base == nil {
		base = knowledge.NewBase()
	}
	engine := reasoning.NewEngine(base, modelProvider)
	return engine.Analyze(ctx, buildIngestionOutput(target, matches, now))
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
