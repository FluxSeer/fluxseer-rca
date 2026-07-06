package investigation

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"fluxagent/api/v1alpha1"
	"fluxagent/internal/datasource"
	"fluxagent/internal/domain"
	"fluxagent/internal/modelgateway"
	"fluxagent/internal/rule"
)

type Issue struct {
	Reason  string
	Message string
}

type PreflightResult struct {
	Target             domain.ResourceRef
	Labels             map[string]string
	DatasourceNames    []string
	CollectionPlan     []CollectionStep
	Provider           *v1alpha1.ModelProvider
	TargetIssue        *Issue
	DatasourceIssue    *Issue
	QueryTypeIssue     *Issue
	ModelProviderIssue *Issue
}

func (r PreflightResult) FirstIssue() *Issue {
	if r.TargetIssue != nil {
		return r.TargetIssue
	}
	if r.DatasourceIssue != nil {
		return r.DatasourceIssue
	}
	if r.QueryTypeIssue != nil {
		return r.QueryTypeIssue
	}
	if r.ModelProviderIssue != nil {
		return r.ModelProviderIssue
	}
	return nil
}

func (r PreflightResult) Successful() bool {
	return r.FirstIssue() == nil
}

type EvidenceCollectionResult struct {
	Summary      string
	EvidenceRefs []v1alpha1.EvidenceRef
	Issue        *Issue
}

type CollectionStep struct {
	Name           string
	DatasourceName string
	QueryType      domain.QueryType
	Query          string
	Reasons        []string
}

type RCAResult struct {
	Reasoning *domain.ReasoningOutput
	Issue     *Issue
}

type Service struct {
	Client   client.Reader
	Registry *datasource.Registry
	Resolver modelgateway.ProviderResolver
	Gateway  *modelgateway.Gateway
}

func (s *Service) Preflight(ctx context.Context, namespace string, spec v1alpha1.InvestigationRequestSpec) (PreflightResult, error) {
	result := PreflightResult{}

	target, labels, targetIssue, err := s.resolveTarget(ctx, spec.Target)
	if err != nil {
		return result, err
	}
	result.Target = target
	result.Labels = labels
	result.TargetIssue = targetIssue

	datasourceNames, collectionPlan, datasourceIssue, queryTypeIssue := s.resolveCollectionPlan(spec, target, labels)
	result.DatasourceNames = datasourceNames
	result.CollectionPlan = collectionPlan
	result.DatasourceIssue = datasourceIssue
	result.QueryTypeIssue = queryTypeIssue

	provider, providerIssue, err := s.resolveProvider(ctx, namespace, spec.ModelProviderRef)
	if err != nil {
		return result, err
	}
	result.Provider = provider
	result.ModelProviderIssue = providerIssue

	return result, nil
}

func (s *Service) CollectEvidence(ctx context.Context, spec v1alpha1.InvestigationRequestSpec, preflight PreflightResult, now time.Time) (EvidenceCollectionResult, error) {
	result := EvidenceCollectionResult{}
	if !preflight.Successful() {
		result.Issue = preflight.FirstIssue()
		return result, nil
	}
	if s.Registry == nil {
		result.Issue = &Issue{
			Reason:  "DatasourceRegistryUnavailable",
			Message: "datasource registry is not configured",
		}
		return result, nil
	}

	window := spec.TimeRange.Lookback.Duration
	if window <= 0 {
		window = 15 * time.Minute
	}

	evidenceRefs := make([]v1alpha1.EvidenceRef, 0, len(preflight.CollectionPlan))
	totalRecords := 0
	for _, step := range preflight.CollectionPlan {
		source, ok := s.Registry.Get(step.DatasourceName)
		if !ok {
			result.Issue = &Issue{
				Reason:  "DataSourceNotFound",
				Message: fmt.Sprintf("datasource %q disappeared from the active registry before evidence collection", step.DatasourceName),
			}
			return result, nil
		}
		queryRequest := datasource.QueryRequest{
			Query:     step.Query,
			StartTime: now.Add(-window),
			EndTime:   now,
			Step:      time.Minute,
			Labels:    preflight.Labels,
			Target:    preflight.Target,
			QueryType: step.QueryType,
		}

		queryResult, err := source.Query(ctx, queryRequest)
		if err != nil {
			result.Issue = &Issue{
				Reason:  "DatasourceQueryFailed",
				Message: fmt.Sprintf("query datasource %q failed: %v", step.DatasourceName, err),
			}
			return result, nil
		}
		filtered := filterQueryResult(queryResult, step)
		normalized := normalizeEvidenceRefs(filtered, queryRequest)
		evidenceRefs = append(evidenceRefs, normalized...)
		totalRecords += len(filtered.Records)
	}

	result.EvidenceRefs = evidenceRefs
	result.Summary = fmt.Sprintf("collected %d evidence records from %d investigation queries", totalRecords, len(preflight.CollectionPlan))
	return result, nil
}

func (s *Service) GenerateRCA(ctx context.Context, spec v1alpha1.InvestigationRequestSpec, preflight PreflightResult, evidenceResult EvidenceCollectionResult, now time.Time) (RCAResult, error) {
	result := RCAResult{}
	if !preflight.Successful() {
		result.Issue = preflight.FirstIssue()
		return result, nil
	}
	if evidenceResult.Issue != nil {
		result.Issue = evidenceResult.Issue
		return result, nil
	}
	if preflight.Provider == nil {
		result.Issue = &Issue{
			Reason:  "ProviderUnavailable",
			Message: "model provider is not available for RCA generation",
		}
		return result, nil
	}
	if s.Gateway == nil {
		result.Issue = &Issue{
			Reason:  "GatewayUnavailable",
			Message: "model gateway is not configured",
		}
		return result, nil
	}

	reasoning, err := s.Gateway.AnalyzeIngestion(ctx, preflight.Provider, buildInvestigationIngestionOutput(spec, preflight, evidenceResult, now))
	if err != nil {
		if analyzeErr, ok := err.(*modelgateway.AnalyzeError); ok {
			result.Issue = &Issue{
				Reason:  analyzeErr.Reason,
				Message: analyzeErr.Message,
			}
			return result, nil
		}
		return result, err
	}
	result.Reasoning = &reasoning
	return result, nil
}

func (s *Service) resolveTarget(ctx context.Context, targetRef v1alpha1.TargetRef) (domain.ResourceRef, map[string]string, *Issue, error) {
	if s.Client == nil {
		return domain.ResourceRef{}, nil, &Issue{
			Reason:  "TargetResolverUnavailable",
			Message: "investigation service client is not configured",
		}, nil
	}

	if !strings.EqualFold(strings.TrimSpace(targetRef.Kind), "Deployment") {
		return domain.ResourceRef{}, nil, &Issue{
			Reason:  "UnsupportedTargetKind",
			Message: fmt.Sprintf("target kind %q is not supported yet; only Deployment is currently supported", targetRef.Kind),
		}, nil
	}

	var deployment appsv1.Deployment
	key := types.NamespacedName{
		Namespace: targetRef.Namespace,
		Name:      targetRef.Name,
	}
	if err := s.Client.Get(ctx, key, &deployment); err != nil {
		if apierrors.IsNotFound(err) {
			return domain.ResourceRef{}, nil, &Issue{
				Reason:  "TargetNotFound",
				Message: fmt.Sprintf("target Deployment %s/%s was not found", key.Namespace, key.Name),
			}, nil
		}
		return domain.ResourceRef{}, nil, nil, err
	}

	return deploymentToResource(deployment), deploymentLabels(deployment), nil, nil
}

func (s *Service) resolveCollectionPlan(spec v1alpha1.InvestigationRequestSpec, target domain.ResourceRef, labels map[string]string) ([]string, []CollectionStep, *Issue, *Issue) {
	if len(spec.Queries) == 0 && len(spec.DataSources) == 0 {
		return nil, nil, &Issue{
			Reason:  "DataSourceNotSpecified",
			Message: "spec.dataSources or spec.queries must include at least one datasource reference",
		}, nil
	}
	if s.Registry == nil {
		return nil, nil, &Issue{
			Reason:  "DatasourceRegistryUnavailable",
			Message: "datasource registry is not configured",
		}, nil
	}

	if len(spec.Queries) > 0 {
		return s.resolveQueryPlan(spec, target, labels)
	}
	return s.resolveDefaultDatasourcePlan(spec, target, labels)
}

func (s *Service) resolveDefaultDatasourcePlan(spec v1alpha1.InvestigationRequestSpec, target domain.ResourceRef, labels map[string]string) ([]string, []CollectionStep, *Issue, *Issue) {
	names := make([]string, 0, len(spec.DataSources))
	plan := make([]CollectionStep, 0, len(spec.DataSources))
	missing := make([]string, 0, len(spec.DataSources))
	for _, ref := range spec.DataSources {
		name := strings.TrimSpace(ref.Name)
		if name == "" {
			missing = append(missing, "<empty>")
			continue
		}
		source, ok := s.Registry.Get(name)
		if !ok {
			missing = append(missing, name)
			continue
		}
		step, queryTypeIssue := buildDefaultCollectionStep(source, target, labels)
		if queryTypeIssue != nil {
			return names, plan, nil, queryTypeIssue
		}
		names = append(names, name)
		plan = append(plan, step)
	}
	if len(missing) > 0 {
		return names, plan, &Issue{
			Reason:  "DataSourceNotFound",
			Message: fmt.Sprintf("datasource references were not found in the active registry: %s", strings.Join(missing, ", ")),
		}, nil
	}
	return names, plan, nil, nil
}

func (s *Service) resolveQueryPlan(spec v1alpha1.InvestigationRequestSpec, target domain.ResourceRef, labels map[string]string) ([]string, []CollectionStep, *Issue, *Issue) {
	names := make([]string, 0, len(spec.Queries))
	plan := make([]CollectionStep, 0, len(spec.Queries))
	missing := make([]string, 0, len(spec.Queries))
	for _, querySpec := range spec.Queries {
		name := strings.TrimSpace(querySpec.DatasourceRef.Name)
		if name == "" {
			missing = append(missing, "<empty>")
			continue
		}
		source, ok := s.Registry.Get(name)
		if !ok {
			missing = append(missing, name)
			continue
		}
		queryType, ok := rule.ParseQueryType(querySpec.QueryType)
		if !ok {
			return names, plan, nil, &Issue{
				Reason:  "CapabilityMismatch",
				Message: fmt.Sprintf("investigation query %q used unsupported queryType %q", firstNonEmpty(querySpec.Name, name), querySpec.QueryType),
			}
		}
		if !source.Capabilities().SupportsQueryType(queryType) {
			return names, plan, nil, &Issue{
				Reason:  "CapabilityMismatch",
				Message: fmt.Sprintf("datasource %q does not support queryType %q for investigation query %q", name, querySpec.QueryType, firstNonEmpty(querySpec.Name, name)),
			}
		}
		queryText, err := rule.RenderQuery(investigationQueryText(querySpec, queryType, target, labels), target, labels)
		if err != nil {
			return names, plan, nil, &Issue{
				Reason:  "QueryTemplateInvalid",
				Message: fmt.Sprintf("render investigation query %q failed: %v", firstNonEmpty(querySpec.Name, name), err),
			}
		}
		names = appendIfMissing(names, name)
		plan = append(plan, CollectionStep{
			Name:           firstNonEmpty(querySpec.Name, name),
			DatasourceName: name,
			QueryType:      queryType,
			Query:          queryText,
			Reasons:        append([]string(nil), querySpec.Reasons...),
		})
	}
	if len(missing) > 0 {
		return names, plan, &Issue{
			Reason:  "DataSourceNotFound",
			Message: fmt.Sprintf("datasource references were not found in the active registry: %s", strings.Join(missing, ", ")),
		}, nil
	}
	return names, plan, nil, nil
}

func (s *Service) resolveProvider(ctx context.Context, namespace string, ref v1alpha1.LocalObjectReference) (*v1alpha1.ModelProvider, *Issue, error) {
	if s.Resolver == nil {
		return nil, &Issue{
			Reason:  "ResolverUnavailable",
			Message: "model provider resolver is not configured",
		}, nil
	}

	provider, err := s.Resolver.Resolve(ctx, namespace, localRefOrNil(ref))
	if err != nil {
		if resolveErr, ok := err.(*modelgateway.ResolveError); ok {
			return nil, &Issue{
				Reason:  resolveErr.Reason,
				Message: resolveErr.Message,
			}, nil
		}
		return nil, nil, err
	}
	return provider, nil, nil
}

func localRefOrNil(ref v1alpha1.LocalObjectReference) *v1alpha1.LocalObjectReference {
	if strings.TrimSpace(ref.Name) == "" {
		return nil
	}
	return &ref
}

func deploymentLabels(deployment appsv1.Deployment) map[string]string {
	labels := make(map[string]string, len(deployment.Labels)+len(deployment.Spec.Template.Labels))
	for key, value := range deployment.Labels {
		labels[key] = value
	}
	for key, value := range deployment.Spec.Template.Labels {
		labels[key] = value
	}
	return labels
}

func deploymentToResource(deployment appsv1.Deployment) domain.ResourceRef {
	service := deployment.Labels["app"]
	if service == "" {
		service = deployment.Spec.Template.Labels["app"]
	}
	if service == "" {
		service = deployment.Name
	}
	return domain.ResourceRef{
		Cluster:    "in-cluster",
		Namespace:  deployment.Namespace,
		Kind:       "Deployment",
		Name:       deployment.Name,
		APIVersion: "apps/v1",
		Service:    service,
	}
}

func buildDefaultCollectionStep(source datasource.DataSource, target domain.ResourceRef, labels map[string]string) (CollectionStep, *Issue) {
	switch {
	case source.Capabilities().Events:
		return CollectionStep{
			Name:           source.Name(),
			DatasourceName: source.Name(),
			QueryType:      domain.QueryTypeEvent,
			Query:          "recent-events",
		}, nil
	case source.Capabilities().Metrics:
		return CollectionStep{
			Name:           source.Name(),
			DatasourceName: source.Name(),
			QueryType:      domain.QueryTypeMetric,
			Query:          fmt.Sprintf(`sum(rate(http_requests_total{namespace="%s",app="%s",status=~"5.."}[5m]))`, target.Namespace, labelApp(labels, target)),
		}, nil
	case source.Capabilities().Logs:
		return CollectionStep{
			Name:           source.Name(),
			DatasourceName: source.Name(),
			QueryType:      domain.QueryTypeLog,
			Query:          fmt.Sprintf(`{namespace="%s",app="%s"} |= "error"`, target.Namespace, labelApp(labels, target)),
		}, nil
	default:
		return CollectionStep{}, &Issue{
			Reason:  "CapabilityMismatch",
			Message: fmt.Sprintf("datasource %q does not support a default investigation query type", source.Name()),
		}
	}
}

func investigationQueryText(querySpec v1alpha1.InvestigationQuery, queryType domain.QueryType, target domain.ResourceRef, labels map[string]string) string {
	if strings.TrimSpace(querySpec.QueryTemplate) != "" {
		return querySpec.QueryTemplate
	}
	if strings.TrimSpace(querySpec.Query) != "" {
		return querySpec.Query
	}
	switch queryType {
	case domain.QueryTypeMetric:
		return fmt.Sprintf(`sum(rate(http_requests_total{namespace="%s",app="%s",status=~"5.."}[5m]))`, target.Namespace, labelApp(labels, target))
	case domain.QueryTypeLog:
		return fmt.Sprintf(`{namespace="%s",app="%s"} |= "error"`, target.Namespace, labelApp(labels, target))
	case domain.QueryTypeEvent:
		return "recent-events"
	default:
		return ""
	}
}

func normalizeEvidenceRefs(result *datasource.QueryResult, req datasource.QueryRequest) []v1alpha1.EvidenceRef {
	if result == nil {
		return nil
	}

	const maxEvidencePerDatasource = 5
	evidenceRefs := make([]v1alpha1.EvidenceRef, 0, min(len(result.Records), maxEvidencePerDatasource))
	for index, record := range result.Records {
		if index >= maxEvidencePerDatasource {
			break
		}
		evidenceRefs = append(evidenceRefs, normalizeEvidenceRef(record, result, req))
	}
	return evidenceRefs
}

func normalizeEvidenceRef(record map[string]any, result *datasource.QueryResult, req datasource.QueryRequest) v1alpha1.EvidenceRef {
	switch result.QueryType {
	case domain.QueryTypeMetric:
		metricName, _ := record["metric"].(string)
		rawValue := fmt.Sprint(record["value"])
		summary := fmt.Sprintf("metric %s returned value %s", firstNonEmpty(metricName, "unknown"), rawValue)
		if parsed, err := strconv.ParseFloat(rawValue, 64); err == nil {
			summary = fmt.Sprintf("metric %s returned value %.2f", firstNonEmpty(metricName, "unknown"), parsed)
		}
		return v1alpha1.EvidenceRef{
			Kind:    "metric",
			Source:  result.Source,
			Query:   req.Query,
			Summary: summary,
		}
	case domain.QueryTypeLog:
		line, _ := record["line"].(string)
		return v1alpha1.EvidenceRef{
			Kind:    "log",
			Source:  result.Source,
			Query:   req.Query,
			Summary: firstNonEmpty(strings.TrimSpace(line), result.Summary),
		}
	case domain.QueryTypeEvent:
		reason, _ := record["reason"].(string)
		message, _ := record["message"].(string)
		return v1alpha1.EvidenceRef{
			Kind:    "event",
			Source:  result.Source,
			Query:   req.Query,
			Reason:  reason,
			Summary: firstNonEmpty(strings.TrimSpace(message), result.Summary),
		}
	default:
		return v1alpha1.EvidenceRef{
			Kind:    "signal",
			Source:  result.Source,
			Query:   req.Query,
			Summary: result.Summary,
		}
	}
}

func filterQueryResult(result *datasource.QueryResult, step CollectionStep) *datasource.QueryResult {
	if result == nil {
		return nil
	}
	if step.QueryType != domain.QueryTypeEvent || len(step.Reasons) == 0 {
		return result
	}

	records := make([]map[string]any, 0, len(result.Records))
	for _, record := range result.Records {
		reason, _ := record["reason"].(string)
		message, _ := record["message"].(string)
		lower := strings.ToLower(reason + " " + message)
		for _, expected := range step.Reasons {
			if strings.Contains(lower, strings.ToLower(strings.TrimSpace(expected))) {
				records = append(records, record)
				break
			}
		}
	}

	filtered := *result
	filtered.Records = records
	filtered.Summary = fmt.Sprintf("%s filtered to %d matching records", firstNonEmpty(step.Name, result.Source), len(records))
	return &filtered
}

func labelApp(labels map[string]string, target domain.ResourceRef) string {
	if labels["app"] != "" {
		return labels["app"]
	}
	if target.Service != "" {
		return target.Service
	}
	return target.Name
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func appendIfMissing(items []string, candidate string) []string {
	for _, item := range items {
		if item == candidate {
			return items
		}
	}
	return append(items, candidate)
}

func buildInvestigationIngestionOutput(spec v1alpha1.InvestigationRequestSpec, preflight PreflightResult, evidenceResult EvidenceCollectionResult, now time.Time) domain.IngestionOutput {
	signals := make([]domain.Signal, 0, len(evidenceResult.EvidenceRefs))
	references := make([]domain.Evidence, 0, len(evidenceResult.EvidenceRefs))
	logs := []string{}
	events := []string{}
	metrics := map[string]float64{}
	timeline := make([]domain.TimelineEvent, 0, len(evidenceResult.EvidenceRefs))

	for index, evidenceRef := range evidenceResult.EvidenceRefs {
		kind := normalizeEvidenceKind(evidenceRef.Kind)
		signals = append(signals, domain.Signal{
			ID:        fmt.Sprintf("investigation-signal-%d", index+1),
			Kind:      kind,
			Source:    firstNonEmpty(evidenceRef.Source, evidenceRef.Kind),
			Severity:  domain.SeverityMedium,
			Message:   evidenceRef.Summary,
			Resource:  preflight.Target,
			Timestamp: now,
		})
		timeline = append(timeline, domain.TimelineEvent{
			Timestamp: now,
			Kind:      kind,
			Summary:   evidenceRef.Summary,
		})
		references = append(references, domain.Evidence{
			Kind:    evidenceRef.Kind,
			Source:  evidenceRef.Source,
			Summary: evidenceRef.Summary,
			Query:   evidenceRef.Query,
			Reason:  evidenceRef.Reason,
			Link:    evidenceRef.Link,
		})

		switch evidenceRef.Kind {
		case "metric":
			logicalKey := firstNonEmpty(evidenceRef.Source, fmt.Sprintf("metric-%d", index))
			metrics[logicalKey] = float64(index + 1)
		case "log":
			logs = append(logs, evidenceRef.Summary)
		case "event":
			events = append(events, firstNonEmpty(evidenceRef.Reason, evidenceRef.Summary))
		}
	}

	contextSummary := evidenceResult.Summary
	if strings.TrimSpace(spec.Question) != "" {
		contextSummary = strings.TrimSpace(spec.Question) + " | " + evidenceResult.Summary
	}

	return domain.IngestionOutput{
		Context: domain.IncidentContext{
			ID:       fmt.Sprintf("investigation-%s-%s", preflight.Target.Namespace, preflight.Target.Name),
			Cluster:  preflight.Target.Cluster,
			Service:  preflight.Target.Service,
			Resource: preflight.Target,
			Summary:  contextSummary,
			Signals:  signals,
			Metadata: map[string]string{
				"question": strings.TrimSpace(spec.Question),
				"mode":     firstNonEmpty(spec.Mode, v1alpha1.InvestigationModeReadOnly),
			},
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
			Resource: preflight.Target,
			Events:   timeline,
		},
		DedupedFrom: len(evidenceResult.EvidenceRefs),
	}
}

func normalizeEvidenceKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "metric":
		return "metric"
	case "log":
		return "log"
	case "event":
		return "event"
	default:
		return "signal"
	}
}
