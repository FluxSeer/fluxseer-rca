package rule

import (
	"fmt"
	"strconv"
	"strings"

	"fluxagent/api/v1alpha1"
	"fluxagent/internal/datasource"
	"fluxagent/internal/domain"
)

type Match struct {
	Signal   v1alpha1.RiskRuleSignal
	Summary  string
	Severity string
	Evidence []v1alpha1.EvidenceRef
}

func LegacySourceForSignalType(signalType string) (domain.QueryType, string, bool) {
	switch normalizeSignalType(signalType) {
	case "prometheus":
		return domain.QueryTypeMetric, "prometheus", true
	case "loki":
		return domain.QueryTypeLog, "loki", true
	case "kubernetesevent":
		return domain.QueryTypeEvent, "kubernetes-events", true
	default:
		return "", "", false
	}
}

func ParseQueryType(raw string) (domain.QueryType, bool) {
	switch normalizeSignalType(raw) {
	case "metric", "metrics", "prometheus":
		return domain.QueryTypeMetric, true
	case "log", "logs", "loki":
		return domain.QueryTypeLog, true
	case "event", "events", "kubernetesevent":
		return domain.QueryTypeEvent, true
	case "trace", "traces", "opentelemetry":
		return domain.QueryTypeTrace, true
	default:
		return "", false
	}
}

func QueryTypeForSignal(signal v1alpha1.RiskRuleSignal) (domain.QueryType, bool) {
	if queryType, ok := ParseQueryType(signal.QueryType); ok {
		return queryType, true
	}
	if queryType, _, ok := LegacySourceForSignalType(signal.Type); ok {
		return queryType, true
	}
	return "", false
}

func QueryTemplateForSignal(signal v1alpha1.RiskRuleSignal) string {
	if strings.TrimSpace(signal.QueryTemplate) != "" {
		return signal.QueryTemplate
	}
	return signal.Query
}

func SourceRefForSignal(signal v1alpha1.RiskRuleSignal) (string, domain.QueryType, bool) {
	if name := strings.TrimSpace(signal.DatasourceRef.Name); name != "" {
		queryType, ok := QueryTypeForSignal(signal)
		return name, queryType, ok
	}
	queryType, sourceName, ok := LegacySourceForSignalType(signal.Type)
	return sourceName, queryType, ok
}

func SignalTypeForSignal(signal v1alpha1.RiskRuleSignal) string {
	if strings.TrimSpace(signal.QueryType) != "" {
		return strings.ToLower(strings.TrimSpace(signal.QueryType))
	}
	if strings.TrimSpace(signal.Type) != "" {
		return strings.TrimSpace(signal.Type)
	}
	if queryType, ok := QueryTypeForSignal(signal); ok {
		return string(queryType)
	}
	return ""
}

func EvaluateSignal(signal v1alpha1.RiskRuleSignal, queryType domain.QueryType, result *datasource.QueryResult, target domain.ResourceRef, severity string) *Match {
	switch queryType {
	case domain.QueryTypeMetric:
		return evaluatePrometheusSignal(signal, result, target, severity)
	case domain.QueryTypeLog:
		return evaluateLokiSignal(signal, result, target, severity)
	case domain.QueryTypeEvent:
		return evaluateKubernetesEventSignal(signal, result, target, severity)
	default:
		return nil
	}
}

func normalizeSignalType(value string) string {
	return strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(value), "-", ""), "_", ""))
}

func evaluatePrometheusSignal(signal v1alpha1.RiskRuleSignal, result *datasource.QueryResult, target domain.ResourceRef, severity string) *Match {
	for _, record := range result.Records {
		value := parseFloat(record["value"])
		if !compareThreshold(value, signal.Threshold) {
			continue
		}
		return &Match{
			Signal:   signal,
			Severity: severity,
			Summary:  fmt.Sprintf("%s crossed threshold for %s", signal.Name, target.Name),
			Evidence: []v1alpha1.EvidenceRef{
				{
					Kind:    "metric",
					Source:  result.Source,
					Query:   signal.Query,
					Summary: fmt.Sprintf("metric value %.2f matched %s %.2f", value, signal.Threshold.Operator, signal.Threshold.Value),
				},
			},
		}
	}
	return nil
}

func evaluateLokiSignal(signal v1alpha1.RiskRuleSignal, result *datasource.QueryResult, target domain.ResourceRef, severity string) *Match {
	count := float64(len(result.Records))
	if !compareThreshold(count, normalizeCountThreshold(signal.Threshold)) {
		return nil
	}

	summary := fmt.Sprintf("%s matched %d log lines for %s", signal.Name, len(result.Records), target.Name)
	if len(result.Records) > 0 {
		if line, _ := result.Records[0]["line"].(string); line != "" {
			summary = line
		}
	}
	return &Match{
		Signal:   signal,
		Severity: severity,
		Summary:  fmt.Sprintf("%s triggered for %s", signal.Name, target.Name),
		Evidence: []v1alpha1.EvidenceRef{
			{
				Kind:    "log",
				Source:  result.Source,
				Query:   signal.Query,
				Summary: fmt.Sprintf("%s (matched %d log lines)", summary, len(result.Records)),
			},
		},
	}
}

func evaluateKubernetesEventSignal(signal v1alpha1.RiskRuleSignal, result *datasource.QueryResult, target domain.ResourceRef, severity string) *Match {
	reasons := make([]string, 0, len(signal.Reasons))
	for _, reason := range signal.Reasons {
		reasons = append(reasons, strings.ToLower(strings.TrimSpace(reason)))
	}
	if len(reasons) == 0 {
		reasons = []string{"unhealthy", "backoff", "failed", "oomkilled"}
	}

	evidence := make([]v1alpha1.EvidenceRef, 0, 3)
	matchCount := 0
	firstReason := ""
	firstMessage := ""
	for _, record := range result.Records {
		reason, _ := record["reason"].(string)
		message, _ := record["message"].(string)
		lower := strings.ToLower(reason + " " + message)
		for _, expected := range reasons {
			if !strings.Contains(lower, expected) {
				continue
			}
			matchCount++
			if firstReason == "" {
				firstReason = reason
				firstMessage = message
			}
			if len(evidence) < 3 {
				evidence = append(evidence, v1alpha1.EvidenceRef{
					Kind:    "event",
					Source:  result.Source,
					Reason:  reason,
					Summary: message,
				})
			}
			break
		}
	}
	if !compareThreshold(float64(matchCount), normalizeCountThreshold(signal.Threshold)) {
		return nil
	}
	if matchCount == 0 {
		return nil
	}
	return &Match{
		Signal:   signal,
		Severity: severity,
		Summary:  fmt.Sprintf("%s detected %d matching events for %s", signal.Name, matchCount, target.Name),
		Evidence: append([]v1alpha1.EvidenceRef{
			{
				Kind:    "event",
				Source:  result.Source,
				Reason:  firstReason,
				Summary: fmt.Sprintf("%s (matched %d events)", firstMessage, matchCount),
			},
		}, evidence...),
	}
}

func compareThreshold(value float64, threshold v1alpha1.RiskThreshold) bool {
	operator := strings.TrimSpace(threshold.Operator)
	switch operator {
	case ">", "gt", "":
		return value > threshold.Value
	case ">=", "gte", "count_gte":
		return value >= threshold.Value
	case "<", "lt":
		return value < threshold.Value
	case "<=", "lte":
		return value <= threshold.Value
	case "count_gt":
		return value > threshold.Value
	default:
		return value > threshold.Value
	}
}

func normalizeCountThreshold(threshold v1alpha1.RiskThreshold) v1alpha1.RiskThreshold {
	if strings.TrimSpace(threshold.Operator) != "" || threshold.Value != 0 {
		return threshold
	}
	return v1alpha1.RiskThreshold{
		Operator: "count_gt",
		Value:    0,
	}
}

func parseFloat(value any) float64 {
	switch cast := value.(type) {
	case float64:
		return cast
	case float32:
		return float64(cast)
	case int:
		return float64(cast)
	case int64:
		return float64(cast)
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(cast), 64)
		if err == nil {
			return parsed
		}
	}
	return 0
}
