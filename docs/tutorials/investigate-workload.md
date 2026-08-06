# Investigate A Workload

This tutorial shows the operator-first investigation path built around `InvestigationRequest`.

Use it when you want FluxSeer RCA to investigate one workload now, collect evidence, run RCA, and optionally promote the result into `RiskSignal`.

## What You Need

- a Kubernetes cluster with FluxSeer RCA installed
- `kubectl`
- Go toolchain if you want to run the local `fluxseer` CLI from source
- at least one `DataSource`
- optional `ModelProvider`

If you omit `spec.modelProviderRef`, FluxSeer RCA falls back to the built-in heuristic provider.

## 1. Deploy FluxSeer RCA

```bash
kubectl create namespace fluxseer-rca-system
kubectl apply -k config/default
```

## 2. Create Datasources And Optional Provider

```bash
kubectl apply -f config/samples/datasource-kubernetes-events.yaml
kubectl apply -f config/samples/datasource-prometheus.yaml
kubectl apply -f config/samples/datasource-loki.yaml
kubectl apply -f config/samples/model-provider.yaml
```

Check readiness:

```bash
kubectl get datasource,modelprovider -A
```

## 3. Create An InvestigationRequest Directly

Use the sample CRD when you want a fully explicit investigation plan:

```bash
kubectl apply -f config/samples/investigation-request.yaml
kubectl get investigationrequest -n fluxseer-rca-system
kubectl describe investigationrequest investigate-open-api -n fluxseer-rca-system
```

That sample uses:

- `queries[]` for explicit per-datasource collection steps
- `createRiskSignal: true` so successful RCA is promoted into a `RiskSignal`

## 4. Use The `fluxseer investigate` CLI

The CLI creates `InvestigationRequest` objects for you.

Simple mode uses repeated `--datasource` flags and lets the controller choose default queries from datasource capabilities:

```bash
GOWORK=off go run ./cmd/fluxseer investigate deployment open-api \
  -n prod \
  --datasource kubernetes-events \
  --datasource prometheus \
  --datasource loki \
  --question "Why did open-api error rate increase after the rollout?" \
  --provider heuristic-provider \
  --wait
```

Advanced mode uses a query file that maps directly to `spec.queries[]`:

```bash
GOWORK=off go run ./cmd/fluxseer investigate deployment open-api \
  -n prod \
  --query-file config/samples/investigation-queries.yaml \
  --question "Why did open-api latency increase after the latest rollout?" \
  --provider heuristic-provider \
  --create-risk-signal \
  --wait
```

Useful flags:

- `--request-namespace`: namespace that stores the `InvestigationRequest`, default `fluxseer-rca-system`
- `--request-name`: explicit object name instead of generated name
- `--lookback`: evidence window, default `15m`
- `--timeout`: wait timeout when `--wait=true`, default `90s`

If you set `spec.ttlSeconds`, FluxSeer RCA keeps the finished `InvestigationRequest` until `status.completedAt + ttlSeconds`, then removes it automatically. This does not delete any promoted `RiskSignal`.

## 5. Inspect Results

```bash
kubectl get investigationrequest -n fluxseer-rca-system
kubectl get investigationrequest -n fluxseer-rca-system -o yaml
kubectl get risksignal -n prod
```

Successful investigations surface:

- `status.phase=Completed`
- `status.summary`
- `status.hypothesis`
- `status.confidence`
- `status.provider`
- `status.evidenceRefs`
- `status.linkedRiskSignalRef` when `createRiskSignal: true`

Key conditions:

- `Ready`
- `TargetResolved`
- `DatasourceResolved`
- `QueryTypeSupported`
- `EvidenceCollectionReady`
- `RCAReady`
- `Degraded`

## 6. Diagnose Failures

Common degraded reasons:

- `DataSourceNotFound`
- `CapabilityMismatch`
- `ProviderNotFound`
- `ProviderUnavailable`
- `InvalidProviderResponse`

Example:

```bash
kubectl describe investigationrequest <name> -n fluxseer-rca-system
```

Look at `status.conditions` to see which stage failed and whether FluxSeer RCA marked the request as degraded because an optional dependency was unavailable.
