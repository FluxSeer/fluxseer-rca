# Enable Prometheus

Prometheus is optional in FluxSeer RCA and is used through `DataSource`, `RiskRule`, and `InvestigationRequest`.

## Local Run

Export the Prometheus base URL before starting the manager:

```bash
export FLUXSEER_RCA_PROMETHEUS_URL=http://localhost:9090
GOWORK=off go run ./cmd/manager
```

## Cluster Run

Set the environment variable on the manager deployment:

```yaml
env:
  - name: FLUXSEER_RCA_PROMETHEUS_URL
    value: http://prometheus.monitoring.svc:9090
```

## DataSource And RiskRule

Create a `DataSource` and reference it from a `RiskRule` or `InvestigationRequest` query:

```yaml
apiVersion: aiops.platform/v1alpha1
kind: DataSource
metadata:
  name: prometheus
spec:
  type: prometheus
  endpoint: http://prometheus.monitoring.svc:9090
---
apiVersion: aiops.platform/v1alpha1
kind: RiskRule
metadata:
  name: prometheus-latency
spec:
  signals:
    - name: elevated-5xx-rate
      datasourceRef:
        name: prometheus
      queryType: metric
      queryTemplate: |
        sum(rate(http_requests_total{namespace="demo",app="my-app",status=~"5.."}[5m]))
      threshold:
        operator: ">"
        value: 0.2
```

## What Happens

- FluxSeer RCA calls Prometheus `query_range`
- returned series are parsed into evidence records
- values above the threshold can create a `RiskSignal` or `InvestigationRequest` depending on rule policy
- the resulting evidence is stored as compact evidence references

The legacy Deployment annotation path is available only when `--enable-legacy-deployment-risk=true` is explicitly set.
