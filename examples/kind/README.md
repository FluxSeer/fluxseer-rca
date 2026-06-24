# kind Demo

This example is the intended open-source first-run path for FluxAgent.

## Flow

1. Create a local kind cluster.
2. Install FluxAgent CRDs, RBAC, and manager manifests.
3. Deploy the sample app, `DataSource`, `RiskRule`, `ModelProvider`, and fake observability services.
4. Inject a fault and watch FluxAgent generate a read-only `RiskSignal`.

## Commands

```bash
cd FluxAgent
make demo-up
kubectl get datasource,riskrule,modelprovider -n fluxagent-demo
make inject-fault
kubectl get risksignal -n fluxagent-demo
kubectl describe datasource prometheus -n fluxagent-demo
kubectl describe riskrule fluxagent-sample-latency -n fluxagent-demo
kubectl describe risksignal fluxagent-sample-latency-fluxagent-sample-risk -n fluxagent-demo
make demo-degrade-missing-datasource
make demo-degrade-capability-mismatch
make demo-degrade-all
make demo-reset-riskrule
make demo-status
make demo-down
```

Key condition checks:

- `DataSource/prometheus`: `Ready=True`
- `RiskRule/fluxagent-sample-latency`: `DatasourceResolved=True`, `QueryTypeSupported=True`
- `RiskSignal/...`: `EvidenceCollectionReady=True`, `RCAReady=True`

Degraded demo helpers:

- `make demo-degrade-missing-datasource`
  Expected: `DatasourceResolved=False`
- `make demo-degrade-capability-mismatch`
  Expected: `QueryTypeSupported=False`
- `make demo-degrade-all`
  Run missing datasource, reset, capability mismatch, then reset again
  Use `DEMO_PAUSE_SECONDS=<n>` to hold each section longer while recording
- `make demo-reset-riskrule`
  Reapply the baseline `RiskRule`
