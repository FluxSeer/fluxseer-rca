# Quickstart: kind Demo

This is the fastest end-to-end demo path for FluxSeer RCA.

## What You Get

- a local kind cluster
- FluxSeer RCA manager and CRDs
- a sample application selected by `RiskRule`
- `DataSource` resources for Prometheus, Loki, and Kubernetes Events
- a sample `ModelProvider`
- a fake observability service that simulates Prometheus, Loki, webhook, and hosted-provider failure endpoints
- a fault injection flow that produces a read-only `RiskSignal`

Helm installs also include a Kubernetes Events baseline rule pack by default. The kind demo keeps its explicit sample `RiskRule` so the walkthrough remains deterministic, but the chart baseline is available for users who want immediate coverage without writing their first rule from scratch.

## Prerequisites

- Docker
- `kind`
- `kubectl`
- Go toolchain

## Start the Demo

```bash
cd FluxSeer RCA
make demo-up
```

This target:

1. creates a kind cluster named `fluxseer-rca-demo`
2. builds the operator image
3. builds the fake observability image
4. loads both images into kind
5. installs the example manifests from `examples/kind`, including `DataSource`, `RiskRule`, and `ModelProvider`

## Expected Cluster State After `make demo-up`

You should see these workload families in `fluxseer-rca-demo`:

- `fluxseer-rca-controller-manager`
- `fluxseer-rca-sample`
- `fluxseer-rca-observability`

You should also see the control-plane resources:

- `riskrule.aiops.platform/fluxseer-rca-sample-latency`
- `datasource.aiops.platform/prometheus`
- `datasource.aiops.platform/loki`
- `datasource.aiops.platform/kubernetes-events`
- `modelprovider.aiops.platform/heuristic-provider`

Example:

```bash
$ kubectl get deployment -n fluxseer-rca-demo
NAME                           READY   UP-TO-DATE   AVAILABLE   AGE
fluxseer-rca-controller-manager   1/1     1            1           30s
fluxseer-rca-observability        1/1     1            1           20s
fluxseer-rca-sample               1/1     1            1           20s
```

## Inject a Fault

```bash
make inject-fault
```

This does two things:

- patches the sample app to crash
- flips the fake observability service into a faulted state

Example:

```bash
$ make inject-fault
kubectl patch deployment fluxseer-rca-sample -n fluxseer-rca-demo ...
deployment.apps/fluxseer-rca-sample patched
{"app":"fluxseer-rca-sample","faulted":true}
pod "curl-fault" deleted
```

## Inspect Results

```bash
kubectl get datasource,riskrule,modelprovider -n fluxseer-rca-demo
kubectl get risksignal -n fluxseer-rca-demo
kubectl describe datasource prometheus -n fluxseer-rca-demo
kubectl describe riskrule fluxseer-rca-sample-latency -n fluxseer-rca-demo
SIGNAL_NAME="$(kubectl get risksignal -n fluxseer-rca-demo \
  -l fluxseer-rca.aiops.platform/risk-rule=fluxseer-rca-sample-latency \
  --sort-by=.metadata.creationTimestamp \
  -o 'jsonpath={range .items[?(@.spec.target.name=="fluxseer-rca-sample")]}{.metadata.name}{"\n"}{end}' | tail -n1)"
kubectl describe risksignal "${SIGNAL_NAME}" -n fluxseer-rca-demo
make demo-status
```

Expected `RiskSignal` listing:

```bash
$ kubectl get risksignal -n fluxseer-rca-demo
NAME                                                        AGE
fluxseer-rca-sample-latency-elevated-5xx-rate-a1b2c3d4e5f6-risk   20s
```

Expected `describe` highlights:

```text
Name:         fluxseer-rca-sample-latency-elevated-5xx-rate-a1b2c3d4e5f6-risk
Namespace:    fluxseer-rca-demo
Kind:         RiskSignal
Phase:        Notified
Severity:     high
Signal Type:  prometheus
Confidence:   94
Rca Summary:  Multiple signals indicate elevated risk for fluxseer-rca-sample: elevated-5xx-rate, error-logs, unhealthy-events.
Evidence:
  - Source: prometheus
    Summary: metric value 0.92 crossed threshold 0.20
  - Source: loki
    Summary: error timeout while calling upstream (matched 2 log lines)
  - Source: kubernetes-events
    Summary: crash loop (matched 1 events)
```

`Confidence: 94` is a `RiskSignal` integer score on a `0-100` scale. Confidence is a heuristic or provider-derived ranking score, not a calibrated probability that the RCA is correct.

Expected condition checks:

- `DataSource/prometheus`: `Ready=True`, `Unsupported=False`
- `RiskRule/fluxseer-rca-sample-latency`: `DatasourceResolved=True`, `QueryTypeSupported=True`, `Ready=True`
- matching `RiskSignal` for `RiskRule/fluxseer-rca-sample-latency` and target `fluxseer-rca-sample`: `EvidenceCollectionReady=True`, `RCAReady=True`

`RCAReady=True` means an RCA result is available. It does not indicate that the target workload is healthy or remediated.

Expected `demo-status` shape:

```bash
$ make demo-status
kubectl get deployment,pod,datasource,riskrule,risksignal -n fluxseer-rca-demo
NAME                                          READY   UP-TO-DATE   AVAILABLE   AGE
deployment.apps/fluxseer-rca-controller-manager  1/1     1            1           2m
deployment.apps/fluxseer-rca-observability       1/1     1            1           2m
deployment.apps/fluxseer-rca-sample              0/1     1            0           2m

NAME                                   READY   STATUS             RESTARTS
pod/fluxseer-rca-sample-xxxx              0/1     CrashLoopBackOff   3

NAME                                                               AGE
risksignal.aiops.platform/fluxseer-rca-sample-latency-elevated-5xx-rate-a1b2c3d4e5f6-risk   1m
```

`make demo-status` also runs:

- `kubectl describe datasource prometheus -n fluxseer-rca-demo`
- `kubectl describe riskrule fluxseer-rca-sample-latency -n fluxseer-rca-demo`
- `kubectl describe risksignal "${SIGNAL_NAME}" -n fluxseer-rca-demo`

## Force a Degraded Case

You can intentionally force degraded conditions from the demo setup.

Missing datasource case:

```bash
make demo-degrade-missing-datasource
```

Expected signals:

- `RiskRule/fluxseer-rca-sample-latency`: `DatasourceResolved=False`, `Ready=False`
- matching `RiskSignal` for `RiskRule/fluxseer-rca-sample-latency` and target `fluxseer-rca-sample`: `EvidenceCollectionReady=False`

Capability mismatch case:

```bash
make demo-degrade-capability-mismatch
```

Expected signals:

- `RiskRule/fluxseer-rca-sample-latency`: `QueryTypeSupported=False`, `Ready=False`
- matching `RiskSignal` for `RiskRule/fluxseer-rca-sample-latency` and target `fluxseer-rca-sample`: `EvidenceCollectionReady=False`

Hosted provider auth failure case:

```bash
make demo-degrade-provider-auth-failed
```

Expected signals:

- `RiskRule/fluxseer-rca-sample-latency`: `Ready=True`
- matching `RiskSignal` for `RiskRule/fluxseer-rca-sample-latency` and target `fluxseer-rca-sample`: `EvidenceCollectionReady=True`, `RCAReady=False` with reason `ProviderAuthFailed`

Reset the demo rule:

```bash
make demo-reset-riskrule
```

Run both degraded cases in one recording-friendly sequence:

```bash
make demo-degrade-all
```

Run the full automated end-to-end validation:

```bash
make verify-e2e-kind
```

`verify-e2e-kind` performs:

1. `make demo-up`
2. wait for the manager, observability service, and sample app
3. `make inject-fault`
4. assert `RiskSignal` exists
5. assert `status.rcaSummary` is not empty
6. assert the fake webhook received a notification
7. assert recurring-rule degraded conditions for missing datasource, capability mismatch, and hosted-provider `ProviderAuthFailed`
8. assert operator-first investigation degraded cases, including hosted-provider `ProviderAuthFailed` and `ProviderRateLimited`
9. restore the baseline rule and clean up with `make demo-down`

Adjust the pause between sections when recording:

```bash
make demo-degrade-all DEMO_PAUSE_SECONDS=6
```

Webhook output can also be inspected through the fake observability state endpoint:

```bash
kubectl run curl-status -n fluxseer-rca-demo --restart=Never --rm -i \
  --image=curlimages/curl:8.8.0 -- \
  curl -s http://fluxseer-rca-observability:8080/demo/state
```

Expected webhook payload shape:

```json
{
  "faultedApps": {
    "fluxseer-rca-sample": true
  },
  "webhookEvents": [
    {
      "title": "RiskSignal detected: fluxseer-rca-sample-observed-risk",
      "summary": "elevated-5xx-rate crossed threshold for fluxseer-rca-sample | error-logs triggered for fluxseer-rca-sample | unhealthy-events detected 1 matching events for fluxseer-rca-sample",
      "body": "Summary: elevated-5xx-rate crossed threshold for fluxseer-rca-sample | error-logs triggered for fluxseer-rca-sample | unhealthy-events detected 1 matching events for fluxseer-rca-sample\nRule: fluxseer-rca-sample-latency\nTarget: fluxseer-rca-demo/fluxseer-rca-sample Deployment\nRCA Summary: Multiple signals indicate elevated risk for fluxseer-rca-sample: elevated-5xx-rate, error-logs, unhealthy-events.\nRCA Hypothesis: A recent release likely introduced elevated memory or startup failures.\n[prometheus] metric value 0.92 matched > 0.20\n[loki] error timeout while calling upstream (matched 2 log lines)\n[kubernetes-events] crash loop (matched 1 events)",
      "fields": {
        "namespace": "fluxseer-rca-demo",
        "severity": "high",
        "signalType": "prometheus",
        "riskRule": "fluxseer-rca-sample-latency",
        "origin": "risk-rule"
      }
    }
  ]
}
```

## Screenshot Checklist

If you want screenshots for README or release notes, capture these three states:

1. `kubectl get deployment,pod,riskrule,risksignal -n fluxseer-rca-demo`
2. `kubectl describe risksignal "${SIGNAL_NAME}" -n fluxseer-rca-demo`
3. `curl http://fluxseer-rca-observability:8080/demo/state`

## Recover the Demo

```bash
make recover-demo
```

Expected recovery shape:

```bash
$ make recover-demo
service/fluxseer-rca-sample unchanged
deployment.apps/fluxseer-rca-sample configured
{"app":"fluxseer-rca-sample","faulted":false}
pod "curl-recover" deleted
```

## Tear Down

```bash
make demo-down
```
