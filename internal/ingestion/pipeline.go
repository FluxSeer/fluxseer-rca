package ingestion

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"fluxseer/internal/domain"
	"fluxseer/internal/rcametrics"
)

type Request struct {
	ID       string
	Signals  []domain.Signal
	Metadata map[string]string
}

type Pipeline struct {
	Now func() time.Time
}

func NewPipeline() *Pipeline {
	return &Pipeline{
		Now: time.Now,
	}
}

func (p *Pipeline) Run(_ context.Context, req Request) (domain.IngestionOutput, error) {
	if len(req.Signals) == 0 {
		return domain.IngestionOutput{}, fmt.Errorf("ingestion pipeline requires at least one signal")
	}

	normalized := p.normalize(req.Signals)
	deduped := p.deduplicate(normalized)
	sort.Slice(deduped, func(i, j int) bool {
		return deduped[i].Timestamp.Before(deduped[j].Timestamp)
	})

	primary := deduped[len(deduped)-1]
	context := domain.IncidentContext{
		ID:          req.ID,
		Cluster:     primary.Resource.Cluster,
		Service:     primary.Resource.Service,
		Resource:    primary.Resource,
		Summary:     summarizeSignals(deduped),
		Signals:     deduped,
		Metadata:    cloneMap(req.Metadata),
		GeneratedAt: p.Now(),
	}

	evidence := domain.EvidenceBundle{
		Logs:    collectMessagesByKind(deduped, "log"),
		Events:  collectMessagesByKind(deduped, "event"),
		Traces:  collectMessagesByKind(deduped, "trace"),
		Metrics: collectMetrics(deduped),
		References: []domain.Evidence{
			{
				Kind:    "incident-context",
				Summary: "AI-ready incident context",
			},
		},
	}

	timeline := domain.ResourceTimeline{
		Resource: primary.Resource,
		Events:   make([]domain.TimelineEvent, 0, len(deduped)),
	}

	for _, signal := range deduped {
		timeline.Events = append(timeline.Events, domain.TimelineEvent{
			Timestamp: signal.Timestamp,
			Kind:      signal.Kind,
			Summary:   signal.Message,
		})
	}

	return domain.IngestionOutput{
		Context:     context,
		Evidence:    evidence,
		Signals:     deduped,
		Timeline:    timeline,
		DedupedFrom: len(req.Signals),
	}, nil
}

func (p *Pipeline) normalize(signals []domain.Signal) []domain.Signal {
	normalized := make([]domain.Signal, 0, len(signals))
	for _, signal := range signals {
		item := signal
		item.Kind = strings.ToLower(strings.TrimSpace(item.Kind))
		item.Source = strings.TrimSpace(item.Source)
		if item.Attributes == nil {
			item.Attributes = map[string]string{}
		}
		if item.Resource.Kind == "" {
			item.Resource.Kind = "Deployment"
		}
		normalized = append(normalized, item)
	}
	return normalized
}

func (p *Pipeline) deduplicate(signals []domain.Signal) []domain.Signal {
	seen := map[string]struct{}{}
	deduped := make([]domain.Signal, 0, len(signals))
	for _, signal := range signals {
		key := strings.Join([]string{
			signal.Kind,
			signal.Resource.Namespace,
			signal.Resource.Name,
			signal.Message,
		}, "|")
		if _, exists := seen[key]; exists {
			rcametrics.RecordDeduplicationHit("ingestion_signal")
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, signal)
	}
	return deduped
}

func summarizeSignals(signals []domain.Signal) string {
	parts := make([]string, 0, len(signals))
	for _, signal := range signals {
		parts = append(parts, signal.Message)
	}
	slices.Sort(parts)
	return strings.Join(parts, " | ")
}

func collectMessagesByKind(signals []domain.Signal, kind string) []string {
	values := make([]string, 0)
	for _, signal := range signals {
		if signal.Kind == kind {
			values = append(values, signal.Message)
		}
	}
	return values
}

func collectMetrics(signals []domain.Signal) map[string]float64 {
	metrics := map[string]float64{}
	for _, signal := range signals {
		if signal.Kind != "metric" {
			continue
		}
		if value, ok := signal.Attributes["value"]; ok {
			var parsed float64
			fmt.Sscanf(value, "%f", &parsed)
			metrics[signal.Attributes["metric"]] = parsed
		}
	}
	return metrics
}

func cloneMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
