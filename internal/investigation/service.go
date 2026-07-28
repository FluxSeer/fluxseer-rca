package investigation

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"fluxagent/api/v1alpha1"
	"fluxagent/internal/canonicaldigest"
	"fluxagent/internal/datasource"
	"fluxagent/internal/domain"
	evidencepkg "fluxagent/internal/evidence"
	"fluxagent/internal/modelgateway"
	"fluxagent/internal/querypolicy"
	"fluxagent/internal/rcametrics"
	"fluxagent/internal/rule"
	"fluxagent/internal/statusbudget"
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
	Observations []domain.Observation
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

	collectedQueries, issue := s.collectDatasourceQueries(ctx, spec, preflight, now, window)
	if issue != nil {
		result.Issue = issue
		return result, nil
	}

	evidenceRefs := make([]v1alpha1.EvidenceRef, 0, len(preflight.CollectionPlan))
	observations := make([]domain.Observation, 0, len(preflight.CollectionPlan))
	totalRecords := 0
	for _, collected := range collectedQueries {
		limited := applyQueryResultLimits(collected.result, collected.step.QueryType, spec.QueryBudget.ResultLimits)
		filtered := filterQueryResult(limited, collected.step)
		normalized := normalizeObservations(filtered, collected.request, len(observations), now)
		for _, observation := range normalized {
			if observation.Truncated {
				rcametrics.RecordEvidenceTruncated(string(observation.Type), evidenceTruncationReason(filtered, observation))
			}
		}
		observations = append(observations, normalized...)
		evidenceRefs = append(evidenceRefs, evidenceRefsFromObservations(normalized, collected.request)...)
		totalRecords += len(filtered.Records)
	}

	result.EvidenceRefs = evidenceRefs
	result.Observations = observations
	result.Summary = fmt.Sprintf("collected %d evidence records from %d investigation queries", totalRecords, len(preflight.CollectionPlan))
	return result, nil
}

type collectedDatasourceQuery struct {
	index    int
	step     CollectionStep
	request  datasource.QueryRequest
	result   *datasource.QueryResult
	duration time.Duration
	bytes    int64
	issue    *Issue
}

func (s *Service) collectDatasourceQueries(ctx context.Context, spec v1alpha1.InvestigationRequestSpec, preflight PreflightResult, now time.Time, window time.Duration) ([]collectedDatasourceQuery, *Issue) {
	plan := preflight.CollectionPlan
	if len(plan) == 0 {
		return nil, nil
	}

	queryCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	concurrency := effectiveDatasourceQueryConcurrency(spec.QueryBudget, len(plan))
	schedulerName := "investigation"
	jobs := make(chan int, len(plan))
	results := make(chan collectedDatasourceQuery, len(plan))
	var wg sync.WaitGroup
	for worker := 0; worker < concurrency; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				select {
				case <-queryCtx.Done():
					rcametrics.AddDatasourceQueryQueueDepth(schedulerName, -1)
					continue
				default:
				}
				rcametrics.AddDatasourceQueryQueueDepth(schedulerName, -1)
				queryResult := func() collectedDatasourceQuery {
					rcametrics.AddDatasourceQueriesInFlight(schedulerName, 1)
					defer rcametrics.AddDatasourceQueriesInFlight(schedulerName, -1)
					return s.collectDatasourceQuery(queryCtx, preflight, now, window, index)
				}()
				results <- queryResult
			}
		}()
	}
	for index := range plan {
		rcametrics.AddDatasourceQueryQueueDepth(schedulerName, 1)
		jobs <- index
	}
	close(jobs)
	go func() {
		wg.Wait()
		close(results)
	}()

	collected := make([]collectedDatasourceQuery, len(plan))
	completed := make([]bool, len(plan))
	var cumulativeDuration time.Duration
	var cumulativeResponseBytes int64
	var firstIssue *Issue
	for queryResult := range results {
		if firstIssue != nil {
			continue
		}
		if queryResult.issue != nil {
			firstIssue = queryResult.issue
			cancel()
			continue
		}
		cumulativeDuration += queryResult.duration
		if maxDuration := spec.QueryBudget.MaxCumulativeDuration.Duration; maxDuration > 0 && cumulativeDuration > maxDuration {
			firstIssue = &Issue{
				Reason:  "QueryBudgetExceeded",
				Message: fmt.Sprintf("datasource query cumulative duration %s exceeds queryBudget.maxCumulativeDuration %s", cumulativeDuration, maxDuration),
			}
			cancel()
			continue
		}
		cumulativeResponseBytes += queryResult.bytes
		if maxBytes := spec.QueryBudget.MaxCumulativeResponseBytes; maxBytes > 0 && cumulativeResponseBytes > maxBytes {
			firstIssue = &Issue{
				Reason:  "QueryBudgetExceeded",
				Message: fmt.Sprintf("datasource query cumulative response bytes %d exceeds queryBudget.maxCumulativeResponseBytes %d", cumulativeResponseBytes, maxBytes),
			}
			cancel()
			continue
		}
		collected[queryResult.index] = queryResult
		completed[queryResult.index] = true
	}
	if firstIssue != nil {
		return nil, firstIssue
	}

	out := make([]collectedDatasourceQuery, 0, len(plan))
	for index := range plan {
		if completed[index] {
			out = append(out, collected[index])
		}
	}
	return out, nil
}

func (s *Service) collectDatasourceQuery(ctx context.Context, preflight PreflightResult, now time.Time, window time.Duration, index int) collectedDatasourceQuery {
	step := preflight.CollectionPlan[index]
	collected := collectedDatasourceQuery{
		index: index,
		step:  step,
		request: datasource.QueryRequest{
			Query:     step.Query,
			StartTime: now.Add(-window),
			EndTime:   now,
			Step:      time.Minute,
			Labels:    preflight.Labels,
			Target:    preflight.Target,
			QueryType: step.QueryType,
		},
	}
	source, ok := s.Registry.Get(step.DatasourceName)
	if !ok {
		collected.issue = &Issue{
			Reason:  "DataSourceNotFound",
			Message: fmt.Sprintf("datasource %q disappeared from the active registry before evidence collection", step.DatasourceName),
		}
		return collected
	}
	queryStart := time.Now()
	queryResult, err := source.Query(ctx, collected.request)
	queryDuration := time.Since(queryStart)
	collected.duration = queryDuration
	queryResultLabel := "success"
	if err != nil {
		queryResultLabel = datasource.QueryErrorReason(err, "failed")
		rcametrics.ObserveDatasourceQuery(source.Type(), queryResultLabel, queryDuration)
		collected.issue = &Issue{
			Reason:  datasource.QueryErrorReason(err, "DatasourceQueryFailed"),
			Message: fmt.Sprintf("query datasource %q failed: %v", step.DatasourceName, err),
		}
		return collected
	}
	rcametrics.ObserveDatasourceQuery(source.Type(), queryResultLabel, queryDuration)
	collected.result = queryResult
	collected.bytes = queryResultResponseBytes(queryResult)
	return collected
}

func effectiveDatasourceQueryConcurrency(budget v1alpha1.InvestigationQueryBudget, planSize int) int {
	if planSize <= 0 {
		return 0
	}
	concurrency := int(budget.MaxConcurrentQueries)
	if concurrency <= 0 {
		concurrency = 1
	}
	if concurrency > planSize {
		return planSize
	}
	return concurrency
}

func queryResultResponseBytes(result *datasource.QueryResult) int64 {
	if result == nil {
		return 0
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return 0
	}
	return int64(len(payload))
}

func applyQueryResultLimits(result *datasource.QueryResult, queryType domain.QueryType, limits v1alpha1.QueryResultLimits) *datasource.QueryResult {
	if result == nil || len(result.Records) == 0 {
		return result
	}
	maxRecords := maxQueryResultRecords(queryType, limits)
	if maxRecords <= 0 || int64(len(result.Records)) <= maxRecords {
		return result
	}

	limited := *result
	limited.Records = append([]map[string]any(nil), result.Records[:maxRecords]...)
	limited.Truncated = true
	limited.OriginalRecordCount = len(result.Records)
	limited.RetainedRecordCount = len(limited.Records)
	limited.Summary = fmt.Sprintf("%s; result limit retained %d of %d records", result.Summary, len(limited.Records), len(result.Records))
	return &limited
}

func evidenceTruncationReason(result *datasource.QueryResult, observation domain.Observation) string {
	if result != nil && result.Truncated {
		return "result_limit"
	}
	if observation.OriginalBytes > observation.RetainedBytes {
		return "status_budget"
	}
	return "evidence_limit"
}

func maxQueryResultRecords(queryType domain.QueryType, limits v1alpha1.QueryResultLimits) int64 {
	switch queryType {
	case domain.QueryTypeMetric:
		return minPositiveLimit(limits.Metrics.MaxSamples, limits.Metrics.MaxSeries)
	case domain.QueryTypeLog:
		return limits.Logs.MaxLines
	case domain.QueryTypeEvent, domain.QueryTypeDeploymentCondition:
		return limits.Events.MaxRecords
	default:
		return 0
	}
}

func minPositiveLimit(limits ...int64) int64 {
	minLimit := int64(0)
	for _, limit := range limits {
		if limit <= 0 {
			continue
		}
		if minLimit == 0 || limit < minLimit {
			minLimit = limit
		}
	}
	return minLimit
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

	filteredEvidence, policyIssue := applyProviderDataPolicy(preflight.Provider, evidenceResult)
	if policyIssue != nil {
		result.Issue = policyIssue
		return result, nil
	}

	providerType := modelProviderType(preflight.Provider)
	reasoning, err := s.Gateway.AnalyzeIngestion(ctx, preflight.Provider, buildInvestigationIngestionOutput(spec, preflight, filteredEvidence, now))
	if err != nil {
		resultLabel := "failed"
		if analyzeErr, ok := err.(*modelgateway.AnalyzeError); ok {
			resultLabel = analyzeErr.Reason
			rcametrics.RecordProviderRequest(providerType, resultLabel)
			rcametrics.RecordProviderFailure(providerType, analyzeErr.Reason)
			result.Issue = &Issue{
				Reason:  analyzeErr.Reason,
				Message: analyzeErr.Message,
			}
			return result, nil
		}
		rcametrics.RecordProviderRequest(providerType, resultLabel)
		rcametrics.RecordProviderFailure(providerType, "ProviderRequestFailed")
		return result, err
	}
	rcametrics.RecordProviderRequest(providerType, "success")
	result.Reasoning = &reasoning
	return result, nil
}

func modelProviderType(provider *v1alpha1.ModelProvider) string {
	if provider == nil {
		return "heuristic"
	}
	return provider.Spec.Provider
}

func applyProviderDataPolicy(provider *v1alpha1.ModelProvider, evidenceResult EvidenceCollectionResult) (EvidenceCollectionResult, *Issue) {
	if provider == nil || !isHostedProvider(provider.Spec.Provider) {
		return evidenceResult, nil
	}
	policy := provider.Spec.DataPolicy
	if !policy.AllowExternalTransmission {
		return evidenceResult, &Issue{
			Reason:  "ProviderDataPolicyDenied",
			Message: fmt.Sprintf("ModelProvider %q requires spec.dataPolicy.allowExternalTransmission=true before evidence can be sent to hosted provider %q", provider.Name, provider.Spec.Provider),
		}
	}

	filtered := evidenceResult
	filtered.EvidenceRefs = filterEvidenceRefsForProviderPolicy(evidenceResult.EvidenceRefs, policy)
	filtered.Observations = filterObservationsForProviderPolicy(evidenceResult.Observations, policy)
	filtered.Summary = fmt.Sprintf("%s; provider data policy retained %d evidence refs", evidenceResult.Summary, len(filtered.EvidenceRefs))
	return filtered, nil
}

func isHostedProvider(providerType string) bool {
	switch strings.ToLower(strings.TrimSpace(providerType)) {
	case "openai", "claude", "gemini":
		return true
	default:
		return false
	}
}

func filterEvidenceRefsForProviderPolicy(refs []v1alpha1.EvidenceRef, policy v1alpha1.ModelProviderDataPolicy) []v1alpha1.EvidenceRef {
	out := make([]v1alpha1.EvidenceRef, 0, len(refs))
	for _, ref := range refs {
		if !evidenceKindAllowed(ref.Kind, policy.AllowedEvidenceKinds) {
			continue
		}
		filtered := ref
		if strings.EqualFold(ref.Kind, "log") && !policy.AllowLogSamples {
			filtered.Summary = "log sample omitted by provider data policy"
			filtered.Query = ""
			filtered.Link = ""
		}
		out = append(out, filtered)
	}
	return out
}

func filterObservationsForProviderPolicy(observations []domain.Observation, policy v1alpha1.ModelProviderDataPolicy) []domain.Observation {
	out := make([]domain.Observation, 0, len(observations))
	for _, observation := range observations {
		if !evidenceKindAllowed(string(observation.Type), policy.AllowedEvidenceKinds) {
			continue
		}
		filtered := observation
		if observation.Type == domain.ObservationTypeLog && !policy.AllowLogSamples {
			filtered.Summary = "log sample omitted by provider data policy"
			if filtered.Value.Log != nil {
				logValue := *filtered.Value.Log
				logValue.Line = filtered.Summary
				filtered.Value.Log = &logValue
			}
		}
		out = append(out, filtered)
	}
	return out
}

func evidenceKindAllowed(kind string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	normalizedKind := normalizeEvidencePolicyKind(kind)
	for _, candidate := range allowed {
		if normalizeEvidencePolicyKind(candidate) == normalizedKind {
			return true
		}
	}
	return false
}

func normalizeEvidencePolicyKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	kind = strings.TrimSuffix(kind, "observation")
	switch kind {
	case "kubernetesevent":
		return "event"
	case "metric":
		return "metric"
	case "log":
		return "log"
	case "deploymentcondition":
		return "deploymentcondition"
	default:
		return kind
	}
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
	lookback := effectiveInvestigationLookback(spec)
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
		decision := querypolicy.Validate(source, querypolicy.Request{
			DatasourceName: name,
			TemplateName:   step.Name,
			FromTemplate:   true,
			Query:          step.Query,
			QueryType:      step.QueryType,
			Lookback:       lookback,
			Target:         target,
			Labels:         labels,
		})
		rcametrics.RecordQueryPolicyDecision(source.Type(), decision.Decision, decision.Reason)
		if decision.Decision == querypolicy.DecisionRejected {
			return names, plan, nil, &Issue{
				Reason:  "QueryPolicyRejected",
				Message: decision.Message,
			}
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
	lookback := effectiveInvestigationLookback(spec)
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
		templateName := firstNonEmpty(querySpec.Name, name)
		decision := querypolicy.Validate(source, querypolicy.Request{
			DatasourceName: name,
			TemplateName:   templateName,
			FromTemplate:   strings.TrimSpace(querySpec.QueryTemplate) != "",
			Query:          queryText,
			QueryType:      queryType,
			Lookback:       lookback,
			Target:         target,
			Labels:         labels,
		})
		rcametrics.RecordQueryPolicyDecision(source.Type(), decision.Decision, decision.Reason)
		if decision.Decision == querypolicy.DecisionRejected {
			return names, plan, nil, &Issue{
				Reason:  "QueryPolicyRejected",
				Message: decision.Message,
			}
		}
		names = appendIfMissing(names, name)
		plan = append(plan, CollectionStep{
			Name:           templateName,
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

func effectiveInvestigationLookback(spec v1alpha1.InvestigationRequestSpec) time.Duration {
	if spec.TimeRange.Lookback.Duration > 0 {
		return spec.TimeRange.Lookback.Duration
	}
	return 15 * time.Minute
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

func normalizeObservations(result *datasource.QueryResult, req datasource.QueryRequest, offset int, collectedAt time.Time) []domain.Observation {
	if result == nil {
		return nil
	}

	const maxEvidencePerDatasource = 5
	originalCount := len(result.Records)
	retainedCount := min(originalCount, maxEvidencePerDatasource)
	truncated := result.Truncated || originalCount > retainedCount
	if result.OriginalRecordCount > originalCount {
		originalCount = result.OriginalRecordCount
	}
	if result.RetainedRecordCount > 0 && result.RetainedRecordCount < retainedCount {
		retainedCount = result.RetainedRecordCount
	}
	observations := make([]domain.Observation, 0, retainedCount)
	for index, record := range result.Records {
		if index >= maxEvidencePerDatasource {
			break
		}
		observations = append(observations, normalizeObservation(record, result, req, offset+index, originalCount, retainedCount, truncated, collectedAt))
	}
	return observations
}

func normalizeObservation(record map[string]any, result *datasource.QueryResult, req datasource.QueryRequest, index int, originalCount int, retainedCount int, truncated bool, collectedAt time.Time) domain.Observation {
	redactor := evidencepkg.NewPatternRedactor()
	source := firstNonEmpty(result.Source, req.Target.Service, req.Target.Name)
	queryDigest := canonicaldigest.String(canonicaldigest.ObservationJSONV1, map[string]any{
		"capability": req.QueryType,
		"query":      req.Query,
	})
	obs := domain.Observation{
		ID:               fmt.Sprintf("evidence-%03d", index+1),
		SchemaVersion:    "observation.fluxagent.io/v1alpha1",
		DataSourceRef:    source,
		Capability:       req.QueryType,
		QueryDigest:      queryDigest,
		TimeRange:        domain.TimeRange{Start: req.StartTime.UTC(), End: req.EndTime.UTC()},
		RedactionProfile: "default-v1",
		Truncated:        truncated,
		OriginalCount:    originalCount,
		RetainedCount:    retainedCount,
		CollectedAt:      collectedAt.UTC(),
	}
	switch result.QueryType {
	case domain.QueryTypeMetric:
		metricName, _ := record["metric"].(string)
		rawValue := fmt.Sprint(record["value"])
		value := 0.0
		summary := fmt.Sprintf("metric %s returned value %s", firstNonEmpty(metricName, "unknown"), rawValue)
		if parsed, err := strconv.ParseFloat(rawValue, 64); err == nil {
			value = parsed
			summary = fmt.Sprintf("metric %s returned value %.2f", firstNonEmpty(metricName, "unknown"), parsed)
		}
		obs.Type = domain.ObservationTypeMetric
		obs.Summary = redactor.RedactText(summary)
		obs.Value = domain.ObservationValue{Metric: &domain.MetricObservation{Name: firstNonEmpty(metricName, "unknown"), Value: value}}
	case domain.QueryTypeLog:
		line, _ := record["line"].(string)
		obs.Type = domain.ObservationTypeLog
		obs.Summary = redactor.RedactText(firstNonEmpty(strings.TrimSpace(line), result.Summary))
		obs.Value = domain.ObservationValue{Log: &domain.LogObservation{Line: obs.Summary}}
	case domain.QueryTypeEvent:
		reason, _ := record["reason"].(string)
		message, _ := record["message"].(string)
		obs.Type = domain.ObservationTypeEvent
		obs.Summary = redactor.RedactText(firstNonEmpty(strings.TrimSpace(message), result.Summary))
		obs.Value = domain.ObservationValue{Event: &domain.EventObservation{Reason: redactor.RedactText(reason), Message: obs.Summary}}
	case domain.QueryTypeDeploymentCondition:
		reason, _ := record["reason"].(string)
		message, _ := record["message"].(string)
		conditionType, _ := record["type"].(string)
		status, _ := record["status"].(string)
		obs.Type = domain.ObservationTypeDeploymentCondition
		obs.Summary = redactor.RedactText(firstNonEmpty(strings.TrimSpace(message), result.Summary))
		obs.Value = domain.ObservationValue{DeploymentCondition: &domain.DeploymentConditionObservation{
			Type:    redactor.RedactText(conditionType),
			Status:  redactor.RedactText(status),
			Reason:  redactor.RedactText(reason),
			Message: obs.Summary,
		}}
	default:
		obs.Type = domain.ObservationTypeEvent
		obs.Summary = redactor.RedactText(result.Summary)
		obs.Value = domain.ObservationValue{Event: &domain.EventObservation{Message: obs.Summary}}
	}
	obs.DigestAlgorithm = canonicaldigest.AlgorithmSHA256
	obs.DigestCanonicalization = canonicaldigest.ObservationJSONV1
	summary, originalBytes, retainedBytes, summaryTruncated := statusbudget.TruncateUTF8(obs.Summary, statusbudget.MaxEvidenceSummaryBytes)
	obs.Summary = summary
	obs.OriginalBytes = int(originalBytes)
	obs.RetainedBytes = int(retainedBytes)
	obs.Truncated = obs.Truncated || summaryTruncated
	if obs.Value.Log != nil {
		obs.Value.Log.Line = obs.Summary
	}
	if obs.Value.Event != nil {
		obs.Value.Event.Message = obs.Summary
	}
	if obs.Value.DeploymentCondition != nil {
		obs.Value.DeploymentCondition.Message = obs.Summary
	}
	obs.ContentDigest = canonicaldigest.String(canonicaldigest.ObservationJSONV1, map[string]any{
		"schemaVersion":    obs.SchemaVersion,
		"dataSourceRef":    obs.DataSourceRef,
		"capability":       obs.Capability,
		"queryDigest":      obs.QueryDigest,
		"timeRange":        obs.TimeRange,
		"type":             obs.Type,
		"value":            obs.Value,
		"summary":          obs.Summary,
		"redactionProfile": obs.RedactionProfile,
		"truncated":        obs.Truncated,
		"originalCount":    obs.OriginalCount,
		"retainedCount":    obs.RetainedCount,
		"originalBytes":    obs.OriginalBytes,
		"retainedBytes":    obs.RetainedBytes,
	})
	return obs
}

func evidenceRefsFromObservations(observations []domain.Observation, req datasource.QueryRequest) []v1alpha1.EvidenceRef {
	refs := make([]v1alpha1.EvidenceRef, 0, len(observations))
	for _, observation := range observations {
		collectedAt := metav1Time(observation.CollectedAt)
		ref := v1alpha1.EvidenceRef{
			ID:                     observation.ID,
			Kind:                   string(observation.Type),
			Source:                 observation.DataSourceRef,
			Summary:                observation.Summary,
			Query:                  req.Query,
			QueryDigest:            observation.QueryDigest,
			ContentDigest:          observation.ContentDigest,
			DigestAlgorithm:        observation.DigestAlgorithm,
			DigestCanonicalization: observation.DigestCanonicalization,
			RedactionProfile:       observation.RedactionProfile,
			Truncated:              observation.Truncated,
			OriginalCount:          int32(observation.OriginalCount),
			RetainedCount:          int32(observation.RetainedCount),
			OriginalBytes:          int32(observation.OriginalBytes),
			RetainedBytes:          int32(observation.RetainedBytes),
			CollectedAt:            &collectedAt,
		}
		if observation.Value.Event != nil {
			ref.Reason = observation.Value.Event.Reason
		}
		if observation.Value.DeploymentCondition != nil {
			ref.Reason = observation.Value.DeploymentCondition.Reason
		}
		refs = append(refs, ref)
	}
	return refs
}

func metav1Time(value time.Time) metav1.Time {
	return metav1.NewTime(value)
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
			Logs:         logs,
			Metrics:      metrics,
			Events:       events,
			References:   references,
			Observations: append([]domain.Observation(nil), evidenceResult.Observations...),
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
