# Enable Prometheus

Prometheus is optional in FluxAgent, but it is already wired into the read-only detector.

## Local Run

Export the Prometheus base URL before starting the manager:

```bash
export FLUXAGENT_PROMETHEUS_URL=http://localhost:9090
GOWORK=off go run ./cmd/manager
```

## Cluster Run

Set the environment variable on the manager deployment:

```yaml
env:
  - name: FLUXAGENT_PROMETHEUS_URL
    value: http://prometheus.monitoring.svc:9090
```

## Workload Annotation

Add annotations to a target `Deployment`:

```yaml
metadata:
  annotations:
    fluxagent.aiops.platform/enabled: "true"
    fluxagent.aiops.platform/prometheus-query: |
      sum(rate(http_requests_total{namespace="demo",app="my-app",status=~"5.."}[5m]))
    fluxagent.aiops.platform/prometheus-threshold: "0.2"
```

## What Happens

- FluxAgent calls Prometheus `query_range`
- returned series are parsed into evidence records
- values above the threshold create a medium-severity finding
- the resulting evidence is attached to `RiskSignal.spec.evidence`
