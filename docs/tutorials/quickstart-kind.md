# Quickstart: kind Demo

This is the fastest end-to-end demo path for FluxAgent.

## What You Get

- a local kind cluster
- FluxAgent manager and CRDs
- a sample application selected by `RiskRule`
- `DataSource` resources for Prometheus, Loki, and Kubernetes Events
- a sample `ModelProvider`
- a fake observability service that simulates Prometheus, Loki, and webhook endpoints
- a fault injection flow that produces a read-only `RiskSignal`

## Prerequisites

- Docker
- `kind`
- `kubectl`
- Go toolchain

## Start the Demo

```bash
cd FluxAgent
make demo-up
```

This target:

1. creates a kind cluster named `fluxagent-demo`
2. builds the operator image
3. builds the fake observability image
4. loads both images into kind
5. installs the example manifests from `examples/kind`, including `DataSource`, `RiskRule`, and `ModelProvider`

## Expected Cluster State After `make demo-up`

You should see these workload families in `fluxagent-demo`:

- `fluxagent-controller-manager`
- `fluxagent-sample`
- `fluxagent-observability`

You should also see the control-plane resources:

- `riskrule.aiops.platform/fluxagent-sample-latency`
- `datasource.aiops.platform/prometheus`
- `datasource.aiops.platform/loki`
- `datasource.aiops.platform/kubernetes-events`
- `modelprovider.aiops.platform/heuristic-provider`

Example:

```bash
$ kubectl get deployment -n fluxagent-demo
NAME                           READY   UP-TO-DATE   AVAILABLE   AGE
fluxagent-controller-manager   1/1     1            1           30s
fluxagent-observability        1/1     1            1           20s
fluxagent-sample               1/1     1            1           20s
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
kubectl patch deployment fluxagent-sample -n fluxagent-demo ...
deployment.apps/fluxagent-sample patched
{"app":"fluxagent-sample","faulted":true}
pod "curl-fault" deleted
```

## Inspect Results

```bash
kubectl get datasource,riskrule,modelprovider -n fluxagent-demo
kubectl get risksignal -n fluxagent-demo
kubectl describe datasource prometheus -n fluxagent-demo
kubectl describe riskrule fluxagent-sample-latency -n fluxagent-demo
kubectl describe risksignal fluxagent-sample-latency-fluxagent-sample-risk -n fluxagent-demo
make demo-status
```

Expected `RiskSignal` listing:

```bash
$ kubectl get risksignal -n fluxagent-demo
NAME                                         AGE
fluxagent-sample-latency-fluxagent-sample-risk    20s
```

Expected `describe` highlights:

```text
Name:         fluxagent-sample-latency-fluxagent-sample-risk
Namespace:    fluxagent-demo
Kind:         RiskSignal
Phase:        Notified
Severity:     high
Signal Type:  prometheus
Confidence:   94
Rca Summary:  Multiple signals indicate elevated risk for fluxagent-sample: elevated-5xx-rate, error-logs, unhealthy-events.
Evidence:
  - Source: prometheus
    Summary: metric value 0.92 crossed threshold 0.20
  - Source: loki
    Summary: error timeout while calling upstream (matched 2 log lines)
  - Source: kubernetes-events
    Summary: crash loop (matched 1 events)
```

Expected condition checks:

- `DataSource/prometheus`: `Ready=True`, `Unsupported=False`
- `RiskRule/fluxagent-sample-latency`: `DatasourceResolved=True`, `QueryTypeSupported=True`, `Ready=True`
- `RiskSignal/fluxagent-sample-latency-fluxagent-sample-risk`: `EvidenceCollectionReady=True`, `RCAReady=True`

Expected `demo-status` shape:

```bash
$ make demo-status
kubectl get deployment,pod,datasource,riskrule,risksignal -n fluxagent-demo
NAME                                          READY   UP-TO-DATE   AVAILABLE   AGE
deployment.apps/fluxagent-controller-manager  1/1     1            1           2m
deployment.apps/fluxagent-observability       1/1     1            1           2m
deployment.apps/fluxagent-sample              0/1     1            0           2m

NAME                                   READY   STATUS             RESTARTS
pod/fluxagent-sample-xxxx              0/1     CrashLoopBackOff   3

NAME                                                      AGE
risksignal.aiops.platform/fluxagent-sample-latency-fluxagent-sample-risk   1m
```

`make demo-status` also runs:

- `kubectl describe datasource prometheus -n fluxagent-demo`
- `kubectl describe riskrule fluxagent-sample-latency -n fluxagent-demo`
- `kubectl describe risksignal fluxagent-sample-latency-fluxagent-sample-risk -n fluxagent-demo`

## Force a Degraded Case

You can intentionally force degraded conditions from the demo setup.

Missing datasource case:

```bash
make demo-degrade-missing-datasource
```

Expected signals:

- `RiskRule/fluxagent-sample-latency`: `DatasourceResolved=False`, `Ready=False`
- `RiskSignal/fluxagent-sample-latency-fluxagent-sample-risk`: `EvidenceCollectionReady=False`

Capability mismatch case:

```bash
make demo-degrade-capability-mismatch
```

Expected signals:

- `RiskRule/fluxagent-sample-latency`: `QueryTypeSupported=False`, `Ready=False`
- `RiskSignal/fluxagent-sample-latency-fluxagent-sample-risk`: `EvidenceCollectionReady=False`

Reset the demo rule:

```bash
make demo-reset-riskrule
```

Run both degraded cases in one recording-friendly sequence:

```bash
make demo-degrade-all
```

Adjust the pause between sections when recording:

```bash
make demo-degrade-all DEMO_PAUSE_SECONDS=6
```

Webhook output can also be inspected through the fake observability state endpoint:

```bash
kubectl run curl-status -n fluxagent-demo --restart=Never --rm -i \
  --image=curlimages/curl:8.8.0 -- \
  curl -s http://fluxagent-observability:8080/demo/state
```

Expected webhook payload shape:

```json
{
  "faultedApps": {
    "fluxagent-sample": true
  },
  "webhookEvents": [
    {
      "title": "RiskSignal detected: fluxagent-sample-observed-risk",
      "summary": "elevated-5xx-rate crossed threshold for fluxagent-sample | error-logs triggered for fluxagent-sample | unhealthy-events detected 1 matching events for fluxagent-sample",
      "body": "Summary: elevated-5xx-rate crossed threshold for fluxagent-sample | error-logs triggered for fluxagent-sample | unhealthy-events detected 1 matching events for fluxagent-sample\nRule: fluxagent-sample-latency\nTarget: fluxagent-demo/fluxagent-sample Deployment\nRCA Summary: Multiple signals indicate elevated risk for fluxagent-sample: elevated-5xx-rate, error-logs, unhealthy-events.\nRCA Hypothesis: A recent release likely introduced elevated memory or startup failures.\n[prometheus] metric value 0.92 matched > 0.20\n[loki] error timeout while calling upstream (matched 2 log lines)\n[kubernetes-events] crash loop (matched 1 events)",
      "fields": {
        "namespace": "fluxagent-demo",
        "severity": "high",
        "signalType": "prometheus",
        "riskRule": "fluxagent-sample-latency",
        "origin": "risk-rule"
      }
    }
  ]
}
```

## Screenshot Checklist

If you want screenshots for README or release notes, capture these three states:

1. `kubectl get deployment,pod,riskrule,risksignal -n fluxagent-demo`
2. `kubectl describe risksignal fluxagent-sample-latency-fluxagent-sample-risk -n fluxagent-demo`
3. `curl http://fluxagent-observability:8080/demo/state`

## Recover the Demo

```bash
make recover-demo
```

Expected recovery shape:

```bash
$ make recover-demo
service/fluxagent-sample unchanged
deployment.apps/fluxagent-sample configured
{"app":"fluxagent-sample","faulted":false}
pod "curl-recover" deleted
```

## Tear Down

```bash
make demo-down
```
