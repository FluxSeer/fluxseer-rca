package querypolicy

import (
	"context"
	"testing"
	"time"

	"fluxagent/api/v1alpha1"
	"fluxagent/internal/datasource"
	"fluxagent/internal/domain"
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

func metav1Duration(duration time.Duration) metav1.Duration {
	return metav1.Duration{Duration: duration}
}

type policySource struct {
	policy v1alpha1.DataSourceQueryPolicy
}

func (p policySource) Name() string { return "prometheus" }
func (p policySource) Type() string { return "prometheus" }
func (p policySource) Capabilities() datasource.Capabilities {
	return datasource.Capabilities{Metrics: true}
}
func (p policySource) QueryPolicy() v1alpha1.DataSourceQueryPolicy { return p.policy }
func (p policySource) Query(context.Context, datasource.QueryRequest) (*datasource.QueryResult, error) {
	return nil, nil
}
func (p policySource) HealthCheck(context.Context) error { return nil }
