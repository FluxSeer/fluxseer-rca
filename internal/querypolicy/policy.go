package querypolicy

import (
	"fmt"
	"strings"
	"time"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/datasource"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
)

const (
	DecisionAllowed  = "allowed"
	DecisionRejected = "rejected"

	ReasonAllowed                 = "allowed"
	ReasonLegacyUnrestricted      = "legacy_unrestricted"
	ReasonNoPolicy                = "no_policy"
	ReasonModeUnsupported         = "mode_unsupported"
	ReasonTemplateRequired        = "template_required"
	ReasonTemplateNotAllowed      = "template_not_allowed"
	ReasonRangeExceeded           = "range_exceeded"
	ReasonRegexDenied             = "regex_denied"
	ReasonTargetScopeRequired     = "target_scope_required"
	ReasonFunctionDenied          = "function_denied"
	ReasonFunctionNotAllowed      = "function_not_allowed"
	ReasonSubqueryDenied          = "subquery_denied"
	ReasonOffsetDenied            = "offset_denied"
	ReasonAtModifierDenied        = "at_modifier_denied"
	ReasonPipelineStageDenied     = "pipeline_stage_denied"
	ReasonPipelineStageNotAllowed = "pipeline_stage_not_allowed"
)

type Request struct {
	DatasourceName string
	TemplateName   string
	FromTemplate   bool
	Query          string
	QueryType      domain.QueryType
	Lookback       time.Duration
	Target         domain.ResourceRef
	Labels         map[string]string
}

type Decision struct {
	Decision string
	Reason   string
	Message  string
}

func Validate(source datasource.DataSource, req Request) Decision {
	policy, ok := datasourceQueryPolicy(source)
	if !ok {
		return Decision{Decision: DecisionAllowed, Reason: ReasonNoPolicy, Message: "datasource has no query policy"}
	}

	mode := strings.TrimSpace(policy.Mode)
	switch mode {
	case "", v1alpha1.DataSourceQueryPolicyModeLegacyUnrestricted:
		return Decision{Decision: DecisionAllowed, Reason: ReasonLegacyUnrestricted, Message: "datasource query policy allows legacy unrestricted queries"}
	case v1alpha1.DataSourceQueryPolicyModeTemplatesOnly:
	default:
		return Decision{Decision: DecisionRejected, Reason: ReasonModeUnsupported, Message: fmt.Sprintf("datasource queryPolicy.mode %q is not supported", policy.Mode)}
	}

	templateName := strings.TrimSpace(req.TemplateName)
	if templateName == "" || !req.FromTemplate {
		return Decision{Decision: DecisionRejected, Reason: ReasonTemplateRequired, Message: "datasource queryPolicy.mode=TemplatesOnly requires a named query template"}
	}
	if !templateAllowed(templateName, policy.AllowedTemplates) {
		return Decision{Decision: DecisionRejected, Reason: ReasonTemplateNotAllowed, Message: fmt.Sprintf("query template %q is not allowed by datasource queryPolicy.allowedTemplates", templateName)}
	}
	if policy.MaxRange.Duration > 0 && req.Lookback > policy.MaxRange.Duration {
		return Decision{Decision: DecisionRejected, Reason: ReasonRangeExceeded, Message: fmt.Sprintf("query lookback %s exceeds datasource queryPolicy.maxRange %s", req.Lookback, policy.MaxRange.Duration)}
	}
	if !policy.AllowRegexMatchers && containsRegexMatcher(req.Query) {
		return Decision{Decision: DecisionRejected, Reason: ReasonRegexDenied, Message: fmt.Sprintf("query template %q rendered a regex matcher denied by datasource queryPolicy", templateName)}
	}
	if policy.RequireTargetScope && !hasTargetScope(req) {
		return Decision{Decision: DecisionRejected, Reason: ReasonTargetScopeRequired, Message: fmt.Sprintf("query template %q did not render required target scope", templateName)}
	}
	if decision := validateBackendSyntax(source, policy, req); decision.Decision == DecisionRejected {
		return decision
	}
	return Decision{Decision: DecisionAllowed, Reason: ReasonAllowed, Message: "query policy allowed datasource query"}
}

func validateBackendSyntax(source datasource.DataSource, policy v1alpha1.DataSourceQueryPolicy, req Request) Decision {
	backend := strings.ToLower(strings.TrimSpace(source.Type()))
	switch {
	case backend == "prometheus" || req.QueryType == domain.QueryTypeMetric:
		return validatePromQLPolicy(policy.Prometheus, req)
	case backend == "loki" || req.QueryType == domain.QueryTypeLog:
		return validateLogQLPolicy(policy.Loki, req)
	default:
		return Decision{Decision: DecisionAllowed, Reason: ReasonAllowed, Message: "query policy has no backend-specific syntax restrictions for datasource"}
	}
}

func datasourceQueryPolicy(source datasource.DataSource) (v1alpha1.DataSourceQueryPolicy, bool) {
	if source == nil {
		return v1alpha1.DataSourceQueryPolicy{}, false
	}
	provider, ok := source.(datasource.QueryPolicyProvider)
	if !ok {
		return v1alpha1.DataSourceQueryPolicy{}, false
	}
	return provider.QueryPolicy(), true
}

func templateAllowed(templateName string, allowedTemplates []string) bool {
	if len(allowedTemplates) == 0 {
		return false
	}
	for _, candidate := range allowedTemplates {
		if strings.EqualFold(strings.TrimSpace(candidate), templateName) {
			return true
		}
	}
	return false
}

func containsRegexMatcher(query string) bool {
	return strings.Contains(query, "=~") ||
		strings.Contains(query, "!~") ||
		strings.Contains(query, "|~")
}

func hasTargetScope(req Request) bool {
	if req.QueryType == domain.QueryTypeEvent || req.QueryType == domain.QueryTypeDeploymentCondition {
		return true
	}
	query := req.Query
	namespace := firstNonEmpty(req.Target.Namespace, req.Labels["namespace"])
	if namespace != "" && !strings.Contains(query, fmt.Sprintf(`namespace="%s"`, namespace)) {
		return false
	}
	for _, value := range []string{req.Labels["app"], req.Target.Service, req.Target.Name} {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if strings.Contains(query, fmt.Sprintf(`app="%s"`, value)) ||
			strings.Contains(query, fmt.Sprintf(`workload="%s"`, value)) ||
			strings.Contains(query, fmt.Sprintf(`pod="%s"`, value)) ||
			strings.Contains(query, fmt.Sprintf(`container="%s"`, value)) {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
