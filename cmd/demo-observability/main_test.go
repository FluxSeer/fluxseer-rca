package main

import "testing"

func TestInferAppFromLabelMatcher(t *testing.T) {
	query := `sum(rate(http_requests_total{namespace="demo",app="fluxagent-baseline-crash",status=~"5.."}[5m]))`
	if got := inferApp(query); got != "fluxagent-baseline-crash" {
		t.Fatalf("expected app label to be parsed, got %q", got)
	}
}

func TestInferAppFallsBackToKnownDemoApps(t *testing.T) {
	query := `sum(rate(kube_pod_container_status_restarts_total{pod=~"fluxagent-sample.*"}[5m]))`
	if got := inferApp(query); got != "fluxagent-sample" {
		t.Fatalf("expected known demo app fallback, got %q", got)
	}
}
