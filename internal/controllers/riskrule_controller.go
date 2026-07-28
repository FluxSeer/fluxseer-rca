package controllers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"fluxagent/api/v1alpha1"
	"fluxagent/internal/canonicaldigest"
	"fluxagent/internal/datasource"
	"fluxagent/internal/domain"
	"fluxagent/internal/modelgateway"
	"fluxagent/internal/rule"
)

type RiskRuleReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Registry *datasource.Registry
	Resolver modelgateway.ProviderResolver
	Gateway  *modelgateway.Gateway
	Now      func() time.Time
}

type evaluationIssue struct {
	Reason  string
	Message string
}

type ruleEvaluationSummary struct {
	MissingDatasource  *evaluationIssue
	CapabilityMismatch *evaluationIssue
	QueryFailure       *evaluationIssue
}

const findingIdentitySchemaVersion = "fluxagent-finding-identity-v1"

func (r *RiskRuleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var rule v1alpha1.RiskRule
	if err := r.Get(ctx, req.NamespacedName, &rule); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	now := time.Now
	if r.Now != nil {
		now = r.Now
	}

	targets, riskCount, summary, err := r.reconcileTargets(ctx, &rule, now())
	if err != nil {
		return ctrl.Result{}, err
	}

	original := rule.DeepCopy()
	statusMessage := fmt.Sprintf("processed %d targets; %d produced %s", targets, riskCount, producedObjectKind(rule.Spec.InvestigationPolicy.Mode))
	if targets == 0 {
		statusMessage = "no matching targets discovered for risk rule"
	}
	setRiskRuleStatus(
		&rule.Status,
		v1alpha1.PhaseObserved,
		statusMessage,
		rule.Generation,
		now(),
	)
	applyRiskRuleConditions(&rule.Status, rule.Generation, summary, now())
	if statusChangedRiskRule(original, &rule) {
		if err := r.Status().Update(ctx, &rule); err != nil && !apierrors.IsConflict(err) {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{RequeueAfter: r.requeueAfter(rule.Spec.Interval)}, nil
}

func (r *RiskRuleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.RiskRule{}).
		Complete(r)
}

func (r *RiskRuleReconciler) requeueAfter(interval metav1.Duration) time.Duration {
	if interval.Duration > 0 {
		return interval.Duration
	}
	return time.Minute
}

func statusChangedRiskRule(before, after *v1alpha1.RiskRule) bool {
	return !reflect.DeepEqual(before.Status, after.Status)
}

func (r *RiskRuleReconciler) reconcileTargets(ctx context.Context, riskRule *v1alpha1.RiskRule, now time.Time) (int, int, ruleEvaluationSummary, error) {
	targets, err := rule.DiscoverTargets(ctx, r.Client, riskRule.Spec.TargetSelector)
	if err != nil {
		return 0, 0, ruleEvaluationSummary{}, err
	}
	if r.Registry == nil {
		return len(targets), 0, ruleEvaluationSummary{}, nil
	}

	riskCount := 0
	summary := ruleEvaluationSummary{}
	for _, target := range targets {
		matches, issues, err := r.evaluateTarget(ctx, riskRule, target, now)
		if err != nil {
			return len(targets), riskCount, summary, err
		}
		summary = mergeRuleEvaluationSummary(summary, issues)
		if len(matches) == 0 {
			continue
		}
		if investigationPolicyMode(riskRule.Spec.InvestigationPolicy.Mode) == v1alpha1.RiskRuleInvestigationModeCreateRequest {
			if err := r.upsertInvestigationRequest(ctx, riskRule, target, matches, now); err != nil {
				return len(targets), riskCount, summary, err
			}
		} else {
			rcaResult, err := r.analyzeRCA(ctx, riskRule, target, matches, now)
			if err != nil {
				return len(targets), riskCount, summary, err
			}
			if err := r.upsertRiskSignal(ctx, riskRule, target, matches, rcaResult, issues, now); err != nil {
				return len(targets), riskCount, summary, err
			}
		}
		riskCount++
	}
	return len(targets), riskCount, summary, nil
}

func (r *RiskRuleReconciler) evaluateTarget(ctx context.Context, riskRule *v1alpha1.RiskRule, target rule.Target, now time.Time) ([]rule.Match, ruleEvaluationSummary, error) {
	window := riskRule.Spec.Window.Duration
	if window <= 0 {
		window = 10 * time.Minute
	}

	matches := make([]rule.Match, 0, len(riskRule.Spec.Signals))
	summary := ruleEvaluationSummary{}
	for _, signal := range riskRule.Spec.Signals {
		sourceName, queryType, ok := rule.SourceRefForSignal(signal)
		if !ok {
			continue
		}
		source, found := r.Registry.Get(sourceName)
		if !found {
			summary.MissingDatasource = firstIssue(summary.MissingDatasource, evaluationIssue{
				Reason:  "DataSourceNotFound",
				Message: fmt.Sprintf("DataSource %q was not found for signal %q", sourceName, signal.Name),
			})
			continue
		}

		if !source.Capabilities().SupportsQueryType(queryType) {
			summary.CapabilityMismatch = firstIssue(summary.CapabilityMismatch, evaluationIssue{
				Reason:  "CapabilityMismatch",
				Message: fmt.Sprintf("DataSource %q does not support queryType %q for signal %q", sourceName, queryType, signal.Name),
			})
			continue
		}

		renderedQuery, err := rule.RenderQuery(rule.QueryTemplateForSignal(signal), target.Resource, target.Labels)
		if err != nil {
			return nil, summary, err
		}

		result, err := source.Query(ctx, datasource.QueryRequest{
			Query:     renderedQuery,
			StartTime: now.Add(-window),
			EndTime:   now,
			Step:      time.Minute,
			Labels:    target.Labels,
			Target:    target.Resource,
			QueryType: queryType,
		})
		if err != nil {
			summary.QueryFailure = firstIssue(summary.QueryFailure, evaluationIssue{
				Reason:  datasource.QueryErrorReason(err, "DatasourceQueryFailed"),
				Message: fmt.Sprintf("query DataSource %q failed for signal %q: %v", sourceName, signal.Name, err),
			})
			continue
		}

		evaluated := rule.EvaluateSignal(signalWithRenderedQuery(signal, renderedQuery), queryType, result, target.Resource, normalizeSeverity(riskRule.Spec.Severity))
		if evaluated == nil {
			continue
		}
		matches = append(matches, *evaluated)
	}
	return matches, summary, nil
}

type rcaResult struct {
	Reasoning    *domain.ReasoningOutput
	ProviderName string
	Condition    *metav1.Condition
}

func (r *RiskRuleReconciler) analyzeRCA(ctx context.Context, riskRule *v1alpha1.RiskRule, target rule.Target, matches []rule.Match, now time.Time) (rcaResult, error) {
	if !riskRule.Spec.AI.RCAEnabled {
		return rcaResult{}, nil
	}
	if r.Resolver == nil {
		return rcaResult{
			Condition: rcaCondition(metav1.ConditionFalse, "ResolverUnavailable", "model provider resolver is not configured", now),
		}, nil
	}
	provider, err := r.Resolver.Resolve(ctx, riskRule.Namespace, refOrNil(riskRule.Spec.AI.ProviderRef))
	if err != nil {
		if resolveErr, ok := err.(*modelgateway.ResolveError); ok {
			return rcaResult{
				Condition: rcaCondition(metav1.ConditionFalse, resolveErr.Reason, resolveErr.Message, now),
			}, nil
		}
		return rcaResult{}, err
	}
	if r.Gateway == nil {
		return rcaResult{
			ProviderName: provider.Name,
			Condition:    rcaCondition(metav1.ConditionFalse, "GatewayUnavailable", "model gateway is not configured", now),
		}, nil
	}

	reasoning, err := r.Gateway.Analyze(ctx, provider, target.Resource, matches, now)
	if err != nil {
		if analyzeErr, ok := err.(*modelgateway.AnalyzeError); ok {
			return rcaResult{
				ProviderName: provider.Name,
				Condition:    rcaCondition(metav1.ConditionFalse, analyzeErr.Reason, analyzeErr.Message, now),
			}, nil
		}
		return rcaResult{}, err
	}

	return rcaResult{
		Reasoning:    &reasoning,
		ProviderName: provider.Name,
		Condition:    rcaCondition(metav1.ConditionTrue, "ProviderSucceeded", fmt.Sprintf("RCA generated by ModelProvider %q", provider.Name), now),
	}, nil
}

func (r *RiskRuleReconciler) upsertRiskSignal(ctx context.Context, riskRule *v1alpha1.RiskRule, target rule.Target, matches []rule.Match, rca rcaResult, summary ruleEvaluationSummary, now time.Time) error {
	riskSignal := &v1alpha1.RiskSignal{}
	riskSignal.Name = riskSignalName(riskRule.Name, target.Resource.Name)
	riskSignal.Namespace = target.Resource.Namespace
	identity := findingIdentity(riskRule, target, matches, normalizedWindowBucket(riskRule.Spec.Window.Duration, now))

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, riskSignal, func() error {
		if riskSignal.Labels == nil {
			riskSignal.Labels = map[string]string{}
		}
		if riskSignal.Annotations == nil {
			riskSignal.Annotations = map[string]string{}
		}
		riskSignal.Labels[labelManagedBy] = "riskrule-controller"
		riskSignal.Labels[labelRiskRule] = riskRule.Name
		riskSignal.Annotations[annotationTargetRef] = target.Resource.Namespace + "/" + target.Resource.Name
		riskSignal.Annotations[annotationDetectionSource] = "risk-rule"
		applyFindingIdentityAnnotations(riskSignal.Annotations, identity)

		riskSignal.Spec.Target = resourceToTargetRef(target.Resource)
		riskSignal.Spec.SignalType = rule.SignalTypeForSignal(matches[0].Signal)
		riskSignal.Spec.FindingIdentity = identity
		riskSignal.Spec.Severity = normalizeSeverity(riskRule.Spec.Severity)
		riskSignal.Spec.Confidence = confidenceForSeverity(riskSignal.Spec.Severity, len(matches))
		riskSignal.Spec.DryRun = true
		riskSignal.Spec.TTLSeconds = int64(r.requeueAfter(riskRule.Spec.Interval).Seconds()) * 6
		riskSignal.Spec.Evidence = flattenEvidence(matches)
		riskSignal.Spec.ActionType = "notification.sendSlack"
		riskSignal.Spec.Parameters = map[string]string{
			"channel":                "webhook",
			"mode":                   "read-only",
			"riskRule":               riskRule.Name,
			"targetRef":              target.Resource.Namespace + "/" + target.Resource.Name,
			"summaryMode":            "rule-evaluated",
			"objectFindingIdentity":  identity.ObjectFindingIdentity,
			"logicalFindingIdentity": identity.LogicalFindingIdentity,
			"incidentOccurrence":     identity.IncidentOccurrence,
		}
		return nil
	})
	if err != nil {
		return err
	}

	original := riskSignal.DeepCopy()
	setRiskSignalStatus(&riskSignal.Status, v1alpha1.PhaseConfirmed, combineSummaries(matches), riskSignal.Generation, now)
	applyEvidenceConditions(&riskSignal.Status, riskSignal.Generation, summary, now)
	applyRCAResult(&riskSignal.Status, rca)
	if statusChangedRiskSignal(original, riskSignal) {
		if err := r.Status().Update(ctx, riskSignal); err != nil && !apierrors.IsConflict(err) {
			return err
		}
	}
	return nil
}

func (r *RiskRuleReconciler) upsertInvestigationRequest(ctx context.Context, riskRule *v1alpha1.RiskRule, target rule.Target, matches []rule.Match, now time.Time) error {
	fingerprint := findingFingerprint(matches)
	windowBucket := normalizedWindowBucket(riskRule.Spec.Window.Duration, now)
	identity := findingIdentity(riskRule, target, matches, windowBucket)
	name := investigationRequestName(riskRule.Name, target.Resource.Name, identity.IncidentOccurrence)

	request := &v1alpha1.InvestigationRequest{}
	request.Name = name
	request.Namespace = riskRule.Namespace

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, request, func() error {
		if request.Labels == nil {
			request.Labels = map[string]string{}
		}
		if request.Annotations == nil {
			request.Annotations = map[string]string{}
		}
		request.Labels[labelManagedBy] = "riskrule-controller"
		request.Labels[labelRiskRule] = riskRule.Name
		request.Annotations[annotationDetectionSource] = "risk-rule"
		request.Annotations[annotationTargetRef] = target.Resource.Namespace + "/" + target.Resource.Name
		request.Annotations[annotationLineageSource] = riskRule.Namespace + "/" + riskRule.Name
		request.Annotations[annotationLineageSourceUID] = string(riskRule.UID)
		request.Annotations[annotationLineageGeneration] = strconv.FormatInt(riskRule.Generation, 10)
		request.Annotations[annotationTargetUID] = targetUID(target)
		request.Annotations[annotationFindingFingerprint] = fingerprint
		applyFindingIdentityAnnotations(request.Annotations, identity)
		request.Annotations[annotationInvestigationDepth] = "0"
		request.Annotations[annotationWindowBucket] = windowBucket

		request.Spec.Target = resourceToTargetRef(target.Resource)
		request.Spec.TimeRange = v1alpha1.InvestigationTimeRange{Lookback: riskRule.Spec.Window}
		request.Spec.Question = fmt.Sprintf("Investigate RiskRule %s for %s/%s", riskRule.Name, target.Resource.Namespace, target.Resource.Name)
		request.Spec.Queries = investigationQueriesFromMatches(matches)
		request.Spec.ModelProviderRef = riskRule.Spec.AI.ProviderRef
		request.Spec.Mode = v1alpha1.InvestigationModeReadOnly
		request.Spec.CreateRiskSignal = riskRule.Spec.InvestigationPolicy.CreateRiskSignal
		return nil
	})
	return err
}

func firstIssue(current *evaluationIssue, candidate evaluationIssue) *evaluationIssue {
	if current != nil {
		return current
	}
	copy := candidate
	return &copy
}

func mergeRuleEvaluationSummary(current, next ruleEvaluationSummary) ruleEvaluationSummary {
	if current.MissingDatasource == nil && next.MissingDatasource != nil {
		copy := *next.MissingDatasource
		current.MissingDatasource = &copy
	}
	if current.CapabilityMismatch == nil && next.CapabilityMismatch != nil {
		copy := *next.CapabilityMismatch
		current.CapabilityMismatch = &copy
	}
	if current.QueryFailure == nil && next.QueryFailure != nil {
		copy := *next.QueryFailure
		current.QueryFailure = &copy
	}
	return current
}

func applyRiskRuleConditions(status *v1alpha1.RiskRuleStatus, generation int64, summary ruleEvaluationSummary, now time.Time) {
	if summary.MissingDatasource != nil {
		setStatusCondition(&status.Conditions, conditionDatasourceResolved, metav1.ConditionFalse, summary.MissingDatasource.Reason, summary.MissingDatasource.Message, generation, now)
	} else {
		setStatusCondition(&status.Conditions, conditionDatasourceResolved, metav1.ConditionTrue, "AllDatasourcesResolved", "all referenced datasources were resolved", generation, now)
	}

	if summary.CapabilityMismatch != nil {
		setStatusCondition(&status.Conditions, conditionQueryTypeSupported, metav1.ConditionFalse, summary.CapabilityMismatch.Reason, summary.CapabilityMismatch.Message, generation, now)
	} else {
		setStatusCondition(&status.Conditions, conditionQueryTypeSupported, metav1.ConditionTrue, "AllQueryTypesSupported", "all datasource query types were supported", generation, now)
	}

	if summary.MissingDatasource != nil || summary.CapabilityMismatch != nil || summary.QueryFailure != nil {
		message := firstNonEmptyIssueMessage(summary)
		reason := firstNonEmptyIssueReason(summary)
		setStatusCondition(&status.Conditions, conditionReady, metav1.ConditionFalse, reason, message, generation, now)
		setStatusCondition(&status.Conditions, conditionDegraded, metav1.ConditionTrue, reason, message, generation, now)
		return
	}

	setStatusCondition(&status.Conditions, conditionReady, metav1.ConditionTrue, "EvaluationReady", "risk rule evaluation inputs are ready", generation, now)
	setStatusCondition(&status.Conditions, conditionDegraded, metav1.ConditionFalse, "NoDegradation", "no datasource resolution or capability issues detected", generation, now)
}

func applyEvidenceConditions(status *v1alpha1.RiskSignalStatus, generation int64, summary ruleEvaluationSummary, now time.Time) {
	if summary.MissingDatasource != nil || summary.CapabilityMismatch != nil || summary.QueryFailure != nil {
		message := firstNonEmptyIssueMessage(summary)
		reason := "PartialEvidence"
		if summary.MissingDatasource != nil {
			reason = summary.MissingDatasource.Reason
		} else if summary.CapabilityMismatch != nil {
			reason = summary.CapabilityMismatch.Reason
		} else if summary.QueryFailure != nil {
			reason = summary.QueryFailure.Reason
		}
		setStatusCondition(&status.Conditions, conditionEvidenceReady, metav1.ConditionFalse, reason, message, generation, now)
		return
	}
	setStatusCondition(&status.Conditions, conditionEvidenceReady, metav1.ConditionTrue, "AllEvidenceSourcesReady", "all datasource references resolved and supported the requested query types", generation, now)
}

func firstNonEmptyIssueMessage(summary ruleEvaluationSummary) string {
	if summary.MissingDatasource != nil && strings.TrimSpace(summary.MissingDatasource.Message) != "" {
		return summary.MissingDatasource.Message
	}
	if summary.CapabilityMismatch != nil && strings.TrimSpace(summary.CapabilityMismatch.Message) != "" {
		return summary.CapabilityMismatch.Message
	}
	if summary.QueryFailure != nil && strings.TrimSpace(summary.QueryFailure.Message) != "" {
		return summary.QueryFailure.Message
	}
	return "no evaluation issues recorded"
}

func firstNonEmptyIssueReason(summary ruleEvaluationSummary) string {
	if summary.MissingDatasource != nil && strings.TrimSpace(summary.MissingDatasource.Reason) != "" {
		return summary.MissingDatasource.Reason
	}
	if summary.CapabilityMismatch != nil && strings.TrimSpace(summary.CapabilityMismatch.Reason) != "" {
		return summary.CapabilityMismatch.Reason
	}
	if summary.QueryFailure != nil && strings.TrimSpace(summary.QueryFailure.Reason) != "" {
		return summary.QueryFailure.Reason
	}
	return "EvaluationDegraded"
}

func signalWithRenderedQuery(signal v1alpha1.RiskRuleSignal, renderedQuery string) v1alpha1.RiskRuleSignal {
	signal.Query = renderedQuery
	if signal.QueryTemplate == "" {
		signal.QueryTemplate = renderedQuery
	}
	return signal
}

func investigationPolicyMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case v1alpha1.RiskRuleInvestigationModeCreateRequest:
		return v1alpha1.RiskRuleInvestigationModeCreateRequest
	default:
		return v1alpha1.RiskRuleInvestigationModeDirectRiskSignal
	}
}

func producedObjectKind(mode string) string {
	if investigationPolicyMode(mode) == v1alpha1.RiskRuleInvestigationModeCreateRequest {
		return "InvestigationRequest"
	}
	return "RiskSignal"
}

func investigationQueriesFromMatches(matches []rule.Match) []v1alpha1.InvestigationQuery {
	queries := make([]v1alpha1.InvestigationQuery, 0, len(matches))
	for _, match := range matches {
		sourceName, queryType, ok := rule.SourceRefForSignal(match.Signal)
		if !ok {
			continue
		}
		queries = append(queries, v1alpha1.InvestigationQuery{
			Name:          match.Signal.Name,
			DatasourceRef: v1alpha1.LocalObjectReference{Name: sourceName},
			QueryType:     string(queryType),
			Query:         match.Signal.Query,
			QueryTemplate: match.Signal.QueryTemplate,
			Reasons:       append([]string(nil), match.Signal.Reasons...),
		})
	}
	return queries
}

func normalizedWindowBucket(window time.Duration, now time.Time) string {
	if window <= 0 {
		window = 10 * time.Minute
	}
	return strconv.FormatInt(now.UTC().Truncate(window).Unix(), 10)
}

func findingFingerprint(matches []rule.Match) string {
	payload := make([]map[string]any, 0, len(matches))
	for _, match := range matches {
		evidence := make([]map[string]string, 0, len(match.Evidence))
		for _, ref := range match.Evidence {
			evidence = append(evidence, map[string]string{
				"kind":    ref.Kind,
				"source":  ref.Source,
				"query":   ref.Query,
				"summary": ref.Summary,
				"reason":  ref.Reason,
			})
		}
		sort.Slice(evidence, func(i, j int) bool {
			return evidence[i]["kind"]+evidence[i]["source"]+evidence[i]["summary"] < evidence[j]["kind"]+evidence[j]["source"]+evidence[j]["summary"]
		})
		payload = append(payload, map[string]any{
			"signal":   match.Signal.Name,
			"summary":  match.Summary,
			"severity": match.Severity,
			"evidence": evidence,
		})
	}
	sort.Slice(payload, func(i, j int) bool {
		return fmt.Sprint(payload[i]["signal"], payload[i]["summary"]) < fmt.Sprint(payload[j]["signal"], payload[j]["summary"])
	})
	return canonicaldigest.String(canonicaldigest.ObservationJSONV1, payload)
}

func investigationRequestName(ruleName, targetName, incidentOccurrence string) string {
	suffixSource := incidentOccurrence
	sum := sha256.Sum256([]byte(suffixSource))
	suffix := hex.EncodeToString(sum[:])[:12]
	name := strings.ToLower(ruleName + "-" + targetName + "-" + suffix)
	name = strings.ReplaceAll(name, "_", "-")
	name = strings.ReplaceAll(name, ".", "-")
	if len(name) <= 63 {
		return strings.TrimSuffix(name, "-")
	}
	return strings.TrimSuffix(name[:63], "-")
}

func findingIdentity(riskRule *v1alpha1.RiskRule, target rule.Target, matches []rule.Match, windowBucket string) *v1alpha1.FindingIdentity {
	findingType := findingType(matches)
	attributes := normalizedFindingAttributes(matches)
	object := canonicaldigest.String(canonicaldigest.ObservationJSONV1, map[string]any{
		"schemaVersion": findingIdentitySchemaVersion,
		"sourceUID":     string(riskRule.UID),
		"targetUID":     targetUID(target),
		"findingType":   findingType,
		"attributes":    attributes,
	})
	logical := canonicaldigest.String(canonicaldigest.ObservationJSONV1, map[string]any{
		"schemaVersion": findingIdentitySchemaVersion,
		"source": map[string]any{
			"apiVersion": v1alpha1.SchemeGroupVersion.String(),
			"kind":       "RiskRule",
			"namespace":  riskRule.Namespace,
			"name":       riskRule.Name,
		},
		"target": map[string]any{
			"apiVersion": target.Resource.APIVersion,
			"kind":       target.Resource.Kind,
			"namespace":  target.Resource.Namespace,
			"name":       target.Resource.Name,
		},
		"findingType": findingType,
		"attributes":  attributes,
	})
	occurrence := canonicaldigest.String(canonicaldigest.ObservationJSONV1, map[string]any{
		"schemaVersion":         findingIdentitySchemaVersion,
		"objectFindingIdentity": object,
		"sourceGeneration":      riskRule.Generation,
		"targetGeneration":      target.Generation,
		"windowBucket":          windowBucket,
	})
	return &v1alpha1.FindingIdentity{
		SchemaVersion:          findingIdentitySchemaVersion,
		ObjectFindingIdentity:  object,
		LogicalFindingIdentity: logical,
		IncidentOccurrence:     occurrence,
		FindingType:            findingType,
		TargetGeneration:       target.Generation,
		WindowBucket:           windowBucket,
	}
}

func applyFindingIdentityAnnotations(annotations map[string]string, identity *v1alpha1.FindingIdentity) {
	if annotations == nil || identity == nil {
		return
	}
	annotations[annotationFindingSchema] = identity.SchemaVersion
	annotations[annotationFindingType] = identity.FindingType
	annotations[annotationObjectFindingID] = identity.ObjectFindingIdentity
	annotations[annotationLogicalFindingID] = identity.LogicalFindingIdentity
	annotations[annotationIncidentOccurrence] = identity.IncidentOccurrence
	annotations[annotationTargetGeneration] = strconv.FormatInt(identity.TargetGeneration, 10)
}

func findingType(matches []rule.Match) string {
	types := make([]string, 0, len(matches))
	for _, match := range matches {
		types = appendStringIfMissing(types, rule.SignalTypeForSignal(match.Signal))
	}
	sort.Strings(types)
	return strings.Join(types, "+")
}

func normalizedFindingAttributes(matches []rule.Match) []map[string]any {
	attributes := make([]map[string]any, 0, len(matches))
	for _, match := range matches {
		evidenceDigests := make([]string, 0, len(match.Evidence))
		for _, ref := range match.Evidence {
			evidenceDigests = append(evidenceDigests, firstNonEmptyString(ref.ContentDigest, ref.QueryDigest, ref.Kind+":"+ref.Source+":"+ref.Summary))
		}
		sort.Strings(evidenceDigests)
		attributes = append(attributes, map[string]any{
			"signal":          match.Signal.Name,
			"signalType":      rule.SignalTypeForSignal(match.Signal),
			"severity":        match.Severity,
			"evidenceDigests": evidenceDigests,
		})
	}
	sort.Slice(attributes, func(i, j int) bool {
		return fmt.Sprint(attributes[i]["signal"], attributes[i]["signalType"]) < fmt.Sprint(attributes[j]["signal"], attributes[j]["signalType"])
	})
	return attributes
}

func appendStringIfMissing(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func targetUID(target rule.Target) string {
	if strings.TrimSpace(target.UID) != "" {
		return target.UID
	}
	return target.Resource.APIVersion + "/" + target.Resource.Kind + "/" + target.Resource.Namespace + "/" + target.Resource.Name
}

func resourceToTargetRef(resource domain.ResourceRef) v1alpha1.TargetRef {
	return v1alpha1.TargetRef{
		Cluster:    resource.Cluster,
		Namespace:  resource.Namespace,
		Kind:       resource.Kind,
		Name:       resource.Name,
		APIVersion: resource.APIVersion,
		Service:    resource.Service,
	}
}

func flattenEvidence(matches []rule.Match) []v1alpha1.EvidenceRef {
	evidence := make([]v1alpha1.EvidenceRef, 0, len(matches))
	for _, match := range matches {
		evidence = append(evidence, match.Evidence...)
	}
	return evidence
}

func combineSummaries(matches []rule.Match) string {
	summaries := make([]string, 0, len(matches))
	for _, match := range matches {
		summaries = append(summaries, match.Summary)
	}
	return strings.Join(summaries, " | ")
}

func normalizeSeverity(severity string) string {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case string(domain.SeverityLow), string(domain.SeverityMedium), string(domain.SeverityHigh), string(domain.SeverityUnsafe):
		return strings.ToLower(strings.TrimSpace(severity))
	case "warning":
		return string(domain.SeverityMedium)
	case "critical":
		return string(domain.SeverityHigh)
	default:
		return string(domain.SeverityMedium)
	}
}

func confidenceForSeverity(severity string, matches int) int {
	base := 65
	switch severity {
	case string(domain.SeverityHigh):
		base = 86
	case string(domain.SeverityUnsafe):
		base = 92
	case string(domain.SeverityLow):
		base = 58
	}
	if matches > 1 {
		base += 4 * (matches - 1)
	}
	if base > 99 {
		return 99
	}
	return base
}

func riskSignalName(ruleName, targetName string) string {
	name := strings.ToLower(ruleName + "-" + targetName + "-risk")
	name = strings.ReplaceAll(name, "_", "-")
	name = strings.ReplaceAll(name, ".", "-")
	if len(name) <= 63 {
		return name
	}
	return strings.TrimSuffix(name[:63], "-")
}

func applyRCAResult(status *v1alpha1.RiskSignalStatus, rca rcaResult) {
	status.RCASummary = ""
	status.RCAHypothesis = ""
	status.RCAProvider = ""
	status.RCACauses = nil

	if rca.Reasoning != nil {
		status.RCASummary = rca.Reasoning.RiskSummary
		status.RCAHypothesis = rca.Reasoning.RCA.Hypothesis
		status.RCAProvider = rca.ProviderName
		status.RCACauses = make([]v1alpha1.RCACause, 0, len(rca.Reasoning.RCA.Causes))
		for _, cause := range rca.Reasoning.RCA.Causes {
			status.RCACauses = append(status.RCACauses, v1alpha1.RCACause{
				Cause:      cause,
				Confidence: rca.Reasoning.Confidence.Score,
			})
		}
	}

	apimeta.RemoveStatusCondition(&status.Conditions, conditionRCAReady)
	if rca.Condition != nil {
		apimeta.SetStatusCondition(&status.Conditions, *rca.Condition)
	}
}

func rcaCondition(status metav1.ConditionStatus, reason, message string, now time.Time) *metav1.Condition {
	return &metav1.Condition{
		Type:               conditionRCAReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.NewTime(now),
	}
}

func refOrNil(ref v1alpha1.LocalObjectReference) *v1alpha1.LocalObjectReference {
	if strings.TrimSpace(ref.Name) == "" {
		return nil
	}
	return &ref
}
