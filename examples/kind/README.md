# kind Demo

This example is the intended open-source first-run path for FluxAgent.

## Flow

1. Create a local kind cluster.
2. Install FluxAgent CRDs, RBAC, and manager manifests.
3. Deploy the sample app and fake observability services.
4. Inject a fault and watch FluxAgent generate a read-only `RiskSignal`.

## Commands

```bash
cd FluxAgent
make demo-up
make inject-fault
kubectl get risksignal -n fluxagent-demo
kubectl describe risksignal fluxagent-sample-observed-risk -n fluxagent-demo
make demo-status
make demo-down
```
