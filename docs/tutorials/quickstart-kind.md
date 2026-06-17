# Quickstart: kind Demo

This is the fastest end-to-end demo path for FluxAgent.

## What You Get

- a local kind cluster
- FluxAgent manager and CRDs
- a sample annotated application
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
5. installs the example manifests from `examples/kind`

## Expected Cluster State After `make demo-up`

You should see these workload families in `fluxagent-demo`:

- `fluxagent-controller-manager`
- `fluxagent-sample`
- `fluxagent-observability`

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
kubectl get risksignal -n fluxagent-demo
kubectl describe risksignal fluxagent-sample-observed-risk -n fluxagent-demo
make demo-status
```

Expected `RiskSignal` listing:

```bash
$ kubectl get risksignal -n fluxagent-demo
NAME                              AGE
fluxagent-sample-observed-risk    20s
```

Expected `describe` highlights:

```text
Name:         fluxagent-sample-observed-risk
Namespace:    fluxagent-demo
Kind:         RiskSignal
Phase:        Notified
Severity:     high
Signal Type:  workload.kubernetes_event
Confidence:   90
Evidence:
  - Source: kubernetes-events
    Reason: BackOff
  - Source: prometheus
    Summary: metric value 0.92 crossed threshold 0.20
  - Source: loki
    Summary: error timeout while calling upstream
```

Expected `demo-status` shape:

```bash
$ make demo-status
kubectl get deployment,pod,risksignal -n fluxagent-demo
NAME                                          READY   UP-TO-DATE   AVAILABLE   AGE
deployment.apps/fluxagent-controller-manager  1/1     1            1           2m
deployment.apps/fluxagent-observability       1/1     1            1           2m
deployment.apps/fluxagent-sample              0/1     1            0           2m

NAME                                   READY   STATUS             RESTARTS
pod/fluxagent-sample-xxxx              0/1     CrashLoopBackOff   3

NAME                              AGE
risksignal.aiops.platform/fluxagent-sample-observed-risk   1m
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
      "summary": "Kubernetes reported BackOff for fluxagent-sample",
      "body": "[kubernetes-events] Back-off restarting failed container\n[prometheus] metric value 0.92 crossed threshold 0.20\n[loki] error timeout while calling upstream",
      "fields": {
        "namespace": "fluxagent-demo",
        "severity": "high",
        "signalType": "workload.kubernetes_event"
      }
    }
  ]
}
```

## Screenshot Checklist

If you want screenshots for README or release notes, capture these three states:

1. `kubectl get deployment,pod,risksignal -n fluxagent-demo`
2. `kubectl describe risksignal fluxagent-sample-observed-risk -n fluxagent-demo`
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
