# Enable Loki

Loki is optional in FluxSeer RCA and is used through `DataSource`, `RiskRule`, and `InvestigationRequest`.

## Local Run

```bash
export FLUXSEER_RCA_LOKI_URL=http://localhost:3100
GOWORK=off go run ./cmd/manager
```

## Cluster Run

```yaml
env:
  - name: FLUXSEER_RCA_LOKI_URL
    value: http://loki.monitoring.svc:3100
```

## DataSource And RiskRule

```yaml
apiVersion: aiops.platform/v1alpha1
kind: DataSource
metadata:
  name: loki
spec:
  type: loki
  endpoint: http://loki.monitoring.svc:3100
---
apiVersion: aiops.platform/v1alpha1
kind: RiskRule
metadata:
  name: loki-errors
spec:
  signals:
    - name: error-logs
      datasourceRef:
        name: loki
      queryType: log
      queryTemplate: |
        {namespace="demo",app="my-app"} |= "error"
      threshold:
        operator: count_gt
        value: 0
```

## What Happens

- FluxSeer RCA calls Loki `query_range`
- matching log lines are converted into evidence
- any non-empty result can create a `RiskSignal` or `InvestigationRequest` depending on rule policy
- log samples are redacted and bounded before they reach provider reasoning

The legacy Deployment annotation path is available only when `--enable-legacy-deployment-risk=true` is explicitly set.
