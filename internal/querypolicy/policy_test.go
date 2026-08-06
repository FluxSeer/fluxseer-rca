package querypolicy

import (
	"context"
	"testing"
	"time"

	"fluxseer/api/v1alpha1"
	"fluxseer/internal/datasource"
	"fluxseer/internal/domain"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestValidateTemplatesOnlyPolicy(t *testing.T) {
	source := policySource{
		policy: v1alpha1.DataSourceQueryPolicy{
			Mode:               v1alpha1.DataSourceQueryPolicyModeTemplatesOnly,
			AllowedTemplates:   []string{"workload-availability"},
			MaxRange:           metav1Duration(30 * time.Minute),
			RequireTargetScope: true,
		},
	}
	req := Request{
		TemplateName: "workload-availability",
		FromTemplate: true,
		Query:        `sum(rate(http_requests_total{namespace="prod",app="checkout"}[5m]))`,
		QueryType:    domain.QueryTypeMetric,
		Lookback:     10 * time.Minute,
		Target:       domain.ResourceRef{Namespace: "prod", Name: "checkout", Service: "checkout"},
		Labels:       map[string]string{"app": "checkout"},
	}
	if got := Validate(source, req); got.Decision != DecisionAllowed {
		t.Fatalf("expected query to be allowed, got %#v", got)
	}

	req.TemplateName = "custom-query"
	if got := Validate(source, req); got.Decision != DecisionRejected || got.Reason != ReasonTemplateNotAllowed {
		t.Fatalf("expected template rejection, got %#v", got)
	}

	req.TemplateName = "workload-availability"
	req.Lookback = time.Hour
	if got := Validate(source, req); got.Decision != DecisionRejected || got.Reason != ReasonRangeExceeded {
		t.Fatalf("expected range rejection, got %#v", got)
	}

	req.Lookback = 10 * time.Minute
	req.Query = `sum(rate(http_requests_total{namespace="prod",app=~"checkout|api"}[5m]))`
	if got := Validate(source, req); got.Decision != DecisionRejected || got.Reason != ReasonRegexDenied {
		t.Fatalf("expected regex rejection, got %#v", got)
	}

	req.Query = `sum(rate(http_requests_total{namespace="prod"}[5m]))`
	if got := Validate(source, req); got.Decision != DecisionRejected || got.Reason != ReasonTargetScopeRequired {
		t.Fatalf("expected target scope rejection, got %#v", got)
	}

	req.TemplateName = "workload-availability"
	req.FromTemplate = false
	req.Query = `sum(rate(http_requests_total{namespace="prod",app="checkout"}[5m]))`
	if got := Validate(source, req); got.Decision != DecisionRejected || got.Reason != ReasonTemplateRequired {
		t.Fatalf("expected raw query rejection, got %#v", got)
	}
}

func TestValidateLegacyUnrestrictedPolicyAllowsQueries(t *testing.T) {
	source := policySource{policy: v1alpha1.DataSourceQueryPolicy{Mode: v1alpha1.DataSourceQueryPolicyModeLegacyUnrestricted}}
	got := Validate(source, Request{Query: `rate(http_requests_total[5m])`, QueryType: domain.QueryTypeMetric})
	if got.Decision != DecisionAllowed || got.Reason != ReasonLegacyUnrestricted {
		t.Fatalf("expected legacy unrestricted allow, got %#v", got)
	}
}

func TestValidatePromQLBackendSyntaxPolicy(t *testing.T) {
	source := policySource{
		sourceType: "prometheus",
		policy: v1alpha1.DataSourceQueryPolicy{
			Mode:             v1alpha1.DataSourceQueryPolicyModeTemplatesOnly,
			AllowedTemplates: []string{"latency"},
			Prometheus: v1alpha1.PromQLPolicy{
				AllowedFunctions: []string{"rate", "sum"},
				AllowSubqueries:  boolPtr(false),
				AllowOffset:      boolPtr(false),
				AllowAtModifier:  boolPtr(false),
			},
		},
	}
	req := Request{
		TemplateName: "latency",
		FromTemplate: true,
		Query:        `sum(rate(http_requests_total{namespace="prod",app="checkout"}[5m]))`,
		QueryType:    domain.QueryTypeMetric,
		Lookback:     10 * time.Minute,
		Target:       domain.ResourceRef{Namespace: "prod", Name: "checkout", Service: "checkout"},
		Labels:       map[string]string{"app": "checkout"},
	}
	if got := Validate(source, req); got.Decision != DecisionAllowed {
		t.Fatalf("expected allowed PromQL syntax, got %#v", got)
	}

	req.Query = `histogram_quantile(0.95, rate(http_request_duration_seconds_bucket{namespace="prod",app="checkout"}[5m]))`
	if got := Validate(source, req); got.Decision != DecisionRejected || got.Reason != ReasonFunctionNotAllowed {
		t.Fatalf("expected function allowlist rejection, got %#v", got)
	}

	source.policy.Prometheus.AllowedFunctions = nil
	source.policy.Prometheus.DeniedFunctions = []string{"histogram_quantile"}
	if got := Validate(source, req); got.Decision != DecisionRejected || got.Reason != ReasonFunctionDenied {
		t.Fatalf("expected function denylist rejection, got %#v", got)
	}

	source.policy.Prometheus.DeniedFunctions = nil
	req.Query = `rate(http_requests_total{namespace="prod",app="checkout"}[30m:5m])`
	if got := Validate(source, req); got.Decision != DecisionRejected || got.Reason != ReasonSubqueryDenied {
		t.Fatalf("expected subquery rejection, got %#v", got)
	}

	req.Query = `rate(http_requests_total{namespace="prod",app="checkout"}[5m] offset 1h)`
	if got := Validate(source, req); got.Decision != DecisionRejected || got.Reason != ReasonOffsetDenied {
		t.Fatalf("expected offset rejection, got %#v", got)
	}

	req.Query = `rate(http_requests_total{namespace="prod",app="checkout"}[5m] @ 1700000000)`
	if got := Validate(source, req); got.Decision != DecisionRejected || got.Reason != ReasonAtModifierDenied {
		t.Fatalf("expected @ modifier rejection, got %#v", got)
	}
}

func TestValidatePromQLUnsetAllowFlagsPreserveCompatibility(t *testing.T) {
	source := policySource{
		sourceType: "prometheus",
		policy: v1alpha1.DataSourceQueryPolicy{
			Mode:             v1alpha1.DataSourceQueryPolicyModeTemplatesOnly,
			AllowedTemplates: []string{"latency"},
		},
	}
	req := Request{
		TemplateName: "latency",
		FromTemplate: true,
		Query:        `rate(http_requests_total{namespace="prod",app="checkout"}[30m:5m] @ 1700000000 offset 1h)`,
		QueryType:    domain.QueryTypeMetric,
		Lookback:     10 * time.Minute,
		Target:       domain.ResourceRef{Namespace: "prod", Name: "checkout", Service: "checkout"},
		Labels:       map[string]string{"app": "checkout"},
	}
	if got := Validate(source, req); got.Decision != DecisionAllowed {
		t.Fatalf("expected unset PromQL allow flags to preserve compatibility, got %#v", got)
	}
}

func TestValidateLogQLBackendSyntaxPolicy(t *testing.T) {
	source := policySource{
		sourceType: "loki",
		policy: v1alpha1.DataSourceQueryPolicy{
			Mode:             v1alpha1.DataSourceQueryPolicyModeTemplatesOnly,
			AllowedTemplates: []string{"errors"},
			Loki: v1alpha1.LogQLPolicy{
				AllowedPipelineStages: []string{"line_filter", "json"},
			},
		},
	}
	req := Request{
		TemplateName: "errors",
		FromTemplate: true,
		Query:        `{namespace="prod",app="checkout"} |= "error" | json`,
		QueryType:    domain.QueryTypeLog,
		Lookback:     10 * time.Minute,
		Target:       domain.ResourceRef{Namespace: "prod", Name: "checkout", Service: "checkout"},
		Labels:       map[string]string{"app": "checkout"},
	}
	if got := Validate(source, req); got.Decision != DecisionAllowed {
		t.Fatalf("expected allowed LogQL syntax, got %#v", got)
	}

	req.Query = `{namespace="prod",app="checkout"} |= "error" | regexp "trace=(?P<trace_id>.*)"`
	if got := Validate(source, req); got.Decision != DecisionRejected || got.Reason != ReasonPipelineStageNotAllowed {
		t.Fatalf("expected LogQL stage allowlist rejection, got %#v", got)
	}

	source.policy.Loki.AllowedPipelineStages = nil
	source.policy.Loki.DeniedPipelineStages = []string{"regexp"}
	if got := Validate(source, req); got.Decision != DecisionRejected || got.Reason != ReasonPipelineStageDenied {
		t.Fatalf("expected LogQL stage denylist rejection, got %#v", got)
	}
}

func metav1Duration(duration time.Duration) metav1.Duration {
	return metav1.Duration{Duration: duration}
}

func boolPtr(value bool) *bool {
	return &value
}

type policySource struct {
	sourceType string
	policy     v1alpha1.DataSourceQueryPolicy
}

func (p policySource) Name() string { return "prometheus" }
func (p policySource) Type() string {
	if p.sourceType != "" {
		return p.sourceType
	}
	return "prometheus"
}
func (p policySource) Capabilities() datasource.Capabilities {
	return datasource.Capabilities{Metrics: true}
}
func (p policySource) QueryPolicy() v1alpha1.DataSourceQueryPolicy { return p.policy }
func (p policySource) Query(context.Context, datasource.QueryRequest) (*datasource.QueryResult, error) {
	return nil, nil
}
func (p policySource) HealthCheck(context.Context) error { return nil }
