package detector

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"fluxagent/internal/datasource"
	"fluxagent/internal/domain"
)

const (
	AnnotationEnabled             = "fluxagent.aiops.platform/enabled"
	AnnotationPrometheusQuery     = "fluxagent.aiops.platform/prometheus-query"
	AnnotationPrometheusThreshold = "fluxagent.aiops.platform/prometheus-threshold"
	AnnotationLokiQuery           = "fluxagent.aiops.platform/loki-query"
	AnnotationEventKeywords       = "fluxagent.aiops.platform/event-keywords"
)

type Request struct {
	Target      domain.ResourceRef
	Labels      map[string]string
	Annotations map[string]string
	Window      time.Duration
}

type Finding struct {
	SignalType string
	Severity   domain.Severity
	Confidence int
	Summary    string
	Evidence   []domain.Evidence
}

type Service struct {
	Registry *datasource.Registry
	Now      func() time.Time
}

func (s *Service) Detect(ctx context.Context, req Request) (*Finding, error) {
	if req.Annotations[AnnotationEnabled] != "true" {
		return nil, nil
	}

	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	current := now()

	window := req.Window
	if window <= 0 {
		window = 10 * time.Minute
	}

	promFinding, err := s.detectPrometheus(ctx, req, current, window)
	if err != nil {
		return nil, err
	}
	lokiFinding, err := s.detectLoki(ctx, req, current, window)
	if err != nil {
		return nil, err
	}
	eventFinding, err := s.detectEvents(ctx, req, current, window)
	if err != nil {
		return nil, err
	}

	merged := mergeFindings(promFinding, lokiFinding, eventFinding)
	if merged == nil {
		return nil, nil
	}
	return merged, nil
}

func (s *Service) detectPrometheus(ctx context.Context, req Request, now time.Time, window time.Duration) (*Finding, error) {
	source, ok := s.Registry.Get("prometheus")
	if !ok {
		return nil, nil
	}

	query := req.Annotations[AnnotationPrometheusQuery]
	if strings.TrimSpace(query) == "" {
		query = fmt.Sprintf(`sum(rate(http_requests_total{namespace="%s",app="%s",status=~"5.."}[5m]))`, req.Target.Namespace, labelApp(req))
	}

	result, err := source.Query(ctx, datasource.QueryRequest{
		Query:     query,
		StartTime: now.Add(-window),
		EndTime:   now,
		Step:      time.Minute,
		Labels:    req.Labels,
		Target:    req.Target,
		QueryType: domain.QueryTypeMetric,
	})
	if err != nil {
		return nil, err
	}

	threshold := parseThreshold(req.Annotations[AnnotationPrometheusThreshold], 0.2)
	for _, record := range result.Records {
		value := parseRecordFloat(record["value"])
		if value <= threshold {
			continue
		}
		return &Finding{
			SignalType: "rollout.latency_regression",
			Severity:   domain.SeverityMedium,
			Confidence: 72,
			Summary:    fmt.Sprintf("Prometheus detected elevated error or latency signal for %s", req.Target.Name),
			Evidence: []domain.Evidence{
				{
					Kind:    "metric",
					Source:  "prometheus",
					Query:   query,
					Summary: fmt.Sprintf("metric value %.2f crossed threshold %.2f", value, threshold),
				},
			},
		}, nil
	}

	return nil, nil
}

func (s *Service) detectLoki(ctx context.Context, req Request, now time.Time, window time.Duration) (*Finding, error) {
	source, ok := s.Registry.Get("loki")
	if !ok {
		return nil, nil
	}

	query := req.Annotations[AnnotationLokiQuery]
	if strings.TrimSpace(query) == "" {
		query = fmt.Sprintf(`{namespace="%s",app="%s"} |= "error"`, req.Target.Namespace, labelApp(req))
	}

	result, err := source.Query(ctx, datasource.QueryRequest{
		Query:     query,
		StartTime: now.Add(-window),
		EndTime:   now,
		Step:      time.Minute,
		Labels:    req.Labels,
		Target:    req.Target,
		QueryType: domain.QueryTypeLog,
	})
	if err != nil {
		return nil, err
	}

	if len(result.Records) == 0 {
		return nil, nil
	}

	line, _ := result.Records[0]["line"].(string)
	return &Finding{
		SignalType: "workload.error_logs",
		Severity:   domain.SeverityMedium,
		Confidence: 68,
		Summary:    fmt.Sprintf("Loki found error logs for %s", req.Target.Name),
		Evidence: []domain.Evidence{
			{
				Kind:    "log",
				Source:  "loki",
				Query:   query,
				Summary: line,
			},
		},
	}, nil
}

func (s *Service) detectEvents(ctx context.Context, req Request, now time.Time, window time.Duration) (*Finding, error) {
	source, ok := s.Registry.Get("kubernetes-events")
	if !ok {
		return nil, nil
	}

	result, err := source.Query(ctx, datasource.QueryRequest{
		Query:     "recent-events",
		StartTime: now.Add(-window),
		EndTime:   now,
		Step:      time.Minute,
		Labels:    req.Labels,
		Target:    req.Target,
		QueryType: domain.QueryTypeEvent,
	})
	if err != nil {
		return nil, err
	}

	keywords := splitKeywords(req.Annotations[AnnotationEventKeywords])
	if len(keywords) == 0 {
		keywords = []string{"backoff", "oomkilled", "unhealthy", "failed"}
	}

	for _, record := range result.Records {
		reason, _ := record["reason"].(string)
		message, _ := record["message"].(string)
		lower := strings.ToLower(reason + " " + message)
		for _, keyword := range keywords {
			if !strings.Contains(lower, keyword) {
				continue
			}
			return &Finding{
				SignalType: "workload.kubernetes_event",
				Severity:   domain.SeverityHigh,
				Confidence: 90,
				Summary:    fmt.Sprintf("Kubernetes reported %s for %s", reason, req.Target.Name),
				Evidence: []domain.Evidence{
					{
						Kind:    "event",
						Source:  "kubernetes-events",
						Reason:  reason,
						Summary: message,
					},
				},
			}, nil
		}
	}

	return nil, nil
}

func mergeFindings(items ...*Finding) *Finding {
	var merged *Finding
	for _, item := range items {
		if item == nil {
			continue
		}
		if merged == nil {
			copy := *item
			copy.Evidence = append([]domain.Evidence(nil), item.Evidence...)
			merged = &copy
			continue
		}
		if severityRank(item.Severity) > severityRank(merged.Severity) {
			merged.Severity = item.Severity
			merged.SignalType = item.SignalType
			merged.Summary = item.Summary
		}
		if item.Confidence > merged.Confidence {
			merged.Confidence = item.Confidence
		}
		merged.Evidence = append(merged.Evidence, item.Evidence...)
	}
	return merged
}

func severityRank(severity domain.Severity) int {
	switch severity {
	case domain.SeverityLow:
		return 1
	case domain.SeverityMedium:
		return 2
	case domain.SeverityHigh:
		return 3
	case domain.SeverityUnsafe:
		return 4
	default:
		return 0
	}
}

func labelApp(req Request) string {
	if app := req.Labels["app"]; app != "" {
		return app
	}
	if app := req.Labels["app.kubernetes.io/name"]; app != "" {
		return app
	}
	return req.Target.Name
}

func parseThreshold(raw string, fallback float64) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return fallback
	}
	return value
}

func parseRecordFloat(value any) float64 {
	switch typed := value.(type) {
	case string:
		parsed, _ := strconv.ParseFloat(typed, 64)
		return parsed
	case float64:
		return typed
	case int:
		return float64(typed)
	default:
		return 0
	}
}

func splitKeywords(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.ToLower(strings.TrimSpace(part))
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
