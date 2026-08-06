# kind Demo

This example is the intended open-source first-run path for FluxSeer RCA.

## Flow

1. Create a local kind cluster.
2. Install FluxSeer RCA CRDs, RBAC, and manager manifests.
3. Deploy the sample app, `DataSource`, `RiskRule`, `ModelProvider`, and fake observability services.
4. Inject a fault and watch FluxSeer RCA generate a read-only `RiskSignal`.

## Commands

```bash
cd FluxSeer RCA
make demo-up
kubectl get datasource,riskrule,modelprovider -n fluxseer-rca-demo
make inject-fault
kubectl get risksignal -n fluxseer-rca-demo
kubectl describe datasource prometheus -n fluxseer-rca-demo
kubectl describe riskrule fluxseer-rca-sample-latency -n fluxseer-rca-demo
SIGNAL_NAME="$(kubectl get risksignal -n fluxseer-rca-demo \
  -l fluxseer-rca.aiops.platform/risk-rule=fluxseer-rca-sample-latency \
  --sort-by=.metadata.creationTimestamp \
  -o 'jsonpath={range .items[?(@.spec.target.name=="fluxseer-rca-sample")]}{.metadata.name}{"\n"}{end}' | tail -n1)"
kubectl describe risksignal "${SIGNAL_NAME}" -n fluxseer-rca-demo
make demo-degrade-missing-datasource
make demo-degrade-capability-mismatch
make demo-degrade-provider-auth-failed
make demo-degrade-all
make demo-reset-riskrule
make demo-status
make verify-investigation-kind
make demo-down
```

Key condition checks:

- `DataSource/prometheus`: `Ready=True`
- `RiskRule/fluxseer-rca-sample-latency`: `DatasourceResolved=True`, `QueryTypeSupported=True`
- `RiskSignal/...`: `EvidenceCollectionReady=True`, `RCAReady=True`

Degraded demo helpers:

- `make demo-degrade-missing-datasource`
  Expected: `DatasourceResolved=False`
- `make demo-degrade-capability-mismatch`
  Expected: `QueryTypeSupported=False`
- `make demo-degrade-provider-auth-failed`
  Expected: `RiskSignal` keeps `EvidenceCollectionReady=True` but flips `RCAReady=False` with `ProviderAuthFailed`
- `make demo-degrade-all`
  Run missing datasource, reset, capability mismatch, then reset again
  Use `DEMO_PAUSE_SECONDS=<n>` to hold each section longer while recording
- `make demo-reset-riskrule`
  Reapply the baseline `RiskRule`
- `make verify-e2e-kind`
  Run the full `v0.2 alpha` gate, including both read-only `RiskSignal` flow and operator-first `InvestigationRequest` flow, plus hosted-provider degraded reasons such as `ProviderAuthFailed` and `ProviderRateLimited`
- `make verify-investigation-kind`
  Run a dedicated `InvestigationRequest` e2e flow through the `fluxseer investigate` CLI
