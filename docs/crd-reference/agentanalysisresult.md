# `AgentAnalysisResult` Reference

`AgentAnalysisResult` records the lifecycle and output of an opt-in CLI-based second-pass analysis.

## API

- Group: `aiops.platform`
- Version: `v1alpha1`
- Kind: `AgentAnalysisResult`

Source schema: [api/v1alpha1/agentexecutor_types.go](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/api/v1alpha1/agentexecutor_types.go:1)

## Purpose

Represent the result contract for `AgentExecutor` jobs. The current scaffold creates the object, records the execution key, links the Kubernetes Job, and tracks Job completion or failure.

## YAML Schema

### `spec`

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `spec.sourceRef` | object | yes | Source object, currently a `RiskSignal`. |
| `spec.executorRef` | object | yes | Referenced `AgentExecutor`. |
| `spec.executionKey` | string | yes | Idempotency key derived from source evidence and executor configuration. |
| `spec.ttlSeconds` | integer | no | Retention hint copied from the source signal. |

### `status`

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `status.phase` | string | no | Current lifecycle phase. |
| `status.message` | string | no | Human-readable status message. |
| `status.observedGeneration` | integer | no | Last generation observed by the controller. |
| `status.updatedAt` | string | no | Timestamp of the latest status update. |
| `status.jobRef` | object | no | Kubernetes Job started for this analysis. |
| `status.summary` | string | no | Future parsed summary. |
| `status.rootCause` | string | no | Future parsed root-cause statement. |
| `status.confidence` | number | no | Future parsed confidence score. |
| `status.validationSteps` | array | no | Future validation steps. |
| `status.recommendations` | array | no | Future recommendations. |
| `status.missingEvidence` | array | no | Future missing evidence requests. |
| `status.startedAt` | string | no | Start timestamp. |
| `status.completedAt` | string | no | Completion timestamp. |
| `status.conditions` | array | no | Machine-readable lifecycle conditions. |

Typical phases:

- `Executing`
- `Succeeded`
- `Failed`

Current condition types:

- `AgentJobReady`

## Idempotency

The controller calculates an execution key from:

- source `RiskSignal` UID
- source generation
- source spec
- persisted RCA summary, hypothesis, and provider
- executor name and spec

For the same execution key, FluxAgent reuses the same result and Job names instead of starting another paid agent execution.

## Sample

See [config/samples/agent-analysis-risksignal.yaml](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/config/samples/agent-analysis-risksignal.yaml:1).
