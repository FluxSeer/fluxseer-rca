package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBuildInvestigationRequestWithDatasources(t *testing.T) {
	req, err := buildInvestigationRequest("deployment", "open-api", investigateOptions{
		targetNamespace:  "prod",
		requestNamespace: "fluxagent-system",
		requestName:      "investigate-open-api",
		question:         "Why did latency increase?",
		lookback:         20 * time.Minute,
		datasources:      []string{"kubernetes-events", "prometheus"},
		provider:         "openai-provider",
		createRiskSignal: true,
	})
	if err != nil {
		t.Fatalf("buildInvestigationRequest returned error: %v", err)
	}

	if req.Name != "investigate-open-api" {
		t.Fatalf("expected request name investigate-open-api, got %s", req.Name)
	}
	if req.Spec.Target.Kind != "Deployment" {
		t.Fatalf("expected normalized target kind Deployment, got %s", req.Spec.Target.Kind)
	}
	if req.Spec.Target.APIVersion != "apps/v1" {
		t.Fatalf("expected apps/v1 apiVersion, got %s", req.Spec.Target.APIVersion)
	}
	if len(req.Spec.DataSources) != 2 {
		t.Fatalf("expected 2 datasources, got %d", len(req.Spec.DataSources))
	}
	if req.Spec.DataSources[0].Name != "kubernetes-events" || req.Spec.DataSources[1].Name != "prometheus" {
		t.Fatalf("unexpected datasource names: %#v", req.Spec.DataSources)
	}
	if req.Spec.ModelProviderRef.Name != "openai-provider" {
		t.Fatalf("expected provider openai-provider, got %s", req.Spec.ModelProviderRef.Name)
	}
	if !req.Spec.CreateRiskSignal {
		t.Fatalf("expected createRiskSignal to be true")
	}
	if req.Spec.TimeRange.Lookback.Duration != 20*time.Minute {
		t.Fatalf("expected lookback 20m, got %s", req.Spec.TimeRange.Lookback.Duration)
	}
}

func TestBuildInvestigationRequestNormalizesSupportedTargetKinds(t *testing.T) {
	cases := []struct {
		inputKind      string
		wantKind       string
		wantAPIVersion string
	}{
		{inputKind: "deployment", wantKind: "Deployment", wantAPIVersion: "apps/v1"},
		{inputKind: "sts", wantKind: "StatefulSet", wantAPIVersion: "apps/v1"},
		{inputKind: "daemonset", wantKind: "DaemonSet", wantAPIVersion: "apps/v1"},
		{inputKind: "rs", wantKind: "ReplicaSet", wantAPIVersion: "apps/v1"},
		{inputKind: "po", wantKind: "Pod", wantAPIVersion: "v1"},
	}

	for _, tc := range cases {
		t.Run(tc.inputKind, func(t *testing.T) {
			req, err := buildInvestigationRequest(tc.inputKind, "open-api", investigateOptions{
				targetNamespace:  "prod",
				requestNamespace: "fluxagent-system",
				lookback:         15 * time.Minute,
				datasources:      []string{"kubernetes-events"},
			})
			if err != nil {
				t.Fatalf("buildInvestigationRequest returned error: %v", err)
			}
			if req.Spec.Target.Kind != tc.wantKind || req.Spec.Target.APIVersion != tc.wantAPIVersion {
				t.Fatalf("expected %s %s, got %#v", tc.wantKind, tc.wantAPIVersion, req.Spec.Target)
			}
		})
	}
}

func TestLoadQueriesFileWrappedPayload(t *testing.T) {
	path := writeTempQueryFile(t, `
queries:
  - name: unhealthy-events
    datasourceRef:
      name: kubernetes-events
    queryType: event
    reasons:
      - Failed
  - name: error-rate
    datasourceRef:
      name: prometheus
    queryType: metric
    queryTemplate: |
      sum(rate(http_requests_total{namespace="{{ .namespace }}"}[5m]))
`)

	queries, err := loadQueriesFile(path)
	if err != nil {
		t.Fatalf("loadQueriesFile returned error: %v", err)
	}
	if len(queries) != 2 {
		t.Fatalf("expected 2 queries, got %d", len(queries))
	}
	if queries[0].DatasourceRef.Name != "kubernetes-events" {
		t.Fatalf("expected kubernetes-events datasource, got %s", queries[0].DatasourceRef.Name)
	}
	if len(queries[0].Reasons) != 1 || queries[0].Reasons[0] != "Failed" {
		t.Fatalf("unexpected reasons: %#v", queries[0].Reasons)
	}
}

func TestLoadQueriesFileTopLevelList(t *testing.T) {
	path := writeTempQueryFile(t, `
- name: error-logs
  datasourceRef:
    name: loki
  queryType: log
  query: '{namespace="prod",app="open-api"} |= "error"'
`)

	queries, err := loadQueriesFile(path)
	if err != nil {
		t.Fatalf("loadQueriesFile returned error: %v", err)
	}
	if len(queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(queries))
	}
	if queries[0].QueryType != "log" {
		t.Fatalf("expected log query type, got %s", queries[0].QueryType)
	}
}

func TestBuildInvestigationRequestRequiresDatasourceOrQueryFile(t *testing.T) {
	_, err := buildInvestigationRequest("deployment", "open-api", investigateOptions{
		targetNamespace:  "prod",
		requestNamespace: "fluxagent-system",
		lookback:         15 * time.Minute,
	})
	if err == nil {
		t.Fatalf("expected missing datasource/query validation error")
	}
}

func TestBuildInvestigationRequestWithQueryFile(t *testing.T) {
	path := writeTempQueryFile(t, `
queries:
  - name: error-rate
    datasourceRef:
      name: prometheus
    queryType: metric
    queryTemplate: |
      sum(rate(http_requests_total{namespace="{{ .namespace }}"}[5m]))
`)

	req, err := buildInvestigationRequest("deployment", "open-api", investigateOptions{
		targetNamespace:  "prod",
		requestNamespace: "fluxagent-system",
		requestName:      "investigate-open-api",
		lookback:         15 * time.Minute,
		queryFile:        path,
		createRiskSignal: true,
	})
	if err != nil {
		t.Fatalf("buildInvestigationRequest returned error: %v", err)
	}
	if len(req.Spec.Queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(req.Spec.Queries))
	}
	if len(req.Spec.DataSources) != 0 {
		t.Fatalf("expected datasources to stay empty when using queries, got %#v", req.Spec.DataSources)
	}
}

func TestParseInvestigateArgsSupportsUsageOrder(t *testing.T) {
	opts, kind, name, err := parseInvestigateArgs([]string{
		"deployment",
		"open-api",
		"--namespace", "prod",
		"--datasource", "prometheus",
		"--create-risk-signal",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseInvestigateArgs returned error: %v", err)
	}
	if kind != "deployment" || name != "open-api" {
		t.Fatalf("unexpected target %s %s", kind, name)
	}
	if opts.targetNamespace != "prod" {
		t.Fatalf("expected namespace prod, got %s", opts.targetNamespace)
	}
	if len(opts.datasources) != 1 || opts.datasources[0] != "prometheus" {
		t.Fatalf("unexpected datasources: %#v", opts.datasources)
	}
	if !opts.createRiskSignal {
		t.Fatalf("expected createRiskSignal true")
	}
}

func TestRunVersionJSON(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := Run([]string{"version", "--output=json"}, &stdout, &stderr); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"version"`)) {
		t.Fatalf("expected version JSON, got %q", stdout.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"gitCommit"`)) {
		t.Fatalf("expected gitCommit JSON, got %q", stdout.String())
	}
}

func writeTempQueryFile(t *testing.T, content string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "queries.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write query file: %v", err)
	}
	return path
}
