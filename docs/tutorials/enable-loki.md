# Enable Loki

Loki is optional in FluxAgent, but it is already wired into the read-only detector.

## Local Run

```bash
export FLUXAGENT_LOKI_URL=http://localhost:3100
GOWORK=off go run ./cmd/manager
```

## Cluster Run

```yaml
env:
  - name: FLUXAGENT_LOKI_URL
    value: http://loki.monitoring.svc:3100
```

## Workload Annotation

```yaml
metadata:
  annotations:
    fluxagent.aiops.platform/enabled: "true"
    fluxagent.aiops.platform/loki-query: |
      {namespace="demo",app="my-app"} |= "error"
```

## What Happens

- FluxAgent calls Loki `query_range`
- matching log lines are converted into evidence
- any non-empty result creates a medium-severity finding
- the first log line is used as the evidence summary
