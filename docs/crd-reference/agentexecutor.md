# `AgentExecutor` Reference

`AgentExecutor` configures an opt-in CLI-based investigation runtime.

## API

- Group: `aiops.platform`
- Version: `v1alpha1`
- Kind: `AgentExecutor`

Source schema: [api/v1alpha1/agentexecutor_types.go](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/api/v1alpha1/agentexecutor_types.go:1)

## Purpose

Define how FluxAgent should start a bounded Kubernetes Job for second-pass analysis after a `RiskSignal` already has RCA output.

Supported executor types:

- `codex-cli`
- `claude-cli`
- `gemini-cli`

This CRD is not a `ModelProvider`. It represents a tool-using CLI runtime and remains opt-in.

## Trigger

Add these annotations to a `RiskSignal`:

```yaml
metadata:
  annotations:
    fluxagent.aiops.platform/agent-analysis: enabled
    fluxagent.aiops.platform/agent-executor: codex-cli
```

The controller only creates a Job when agent analysis is enabled on the manager and the `RiskSignal` has RCA output or `RCAReady=True`.

## YAML Schema

### `spec`

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `spec.type` | string | yes | Executor type: `codex-cli`, `claude-cli`, or `gemini-cli`. |
| `spec.image` | string | yes | Container image that contains the CLI runtime and wrapper dependencies. |
| `spec.command` | array | no | Container command. |
| `spec.args` | array | no | Container args. |
| `spec.credentialEnvName` | string | no | Environment variable name that receives the credential. |
| `spec.credentialSecretRef` | object | no | Secret key reference for the workload-scoped credential. |
| `spec.serviceAccountName` | string | no | ServiceAccount used by the Job. Defaults to `fluxagent-investigator`. |
| `spec.timeoutSeconds` | integer | no | Job `activeDeadlineSeconds`. Defaults to 900. |
| `spec.backoffLimit` | integer | no | Job retry limit. Defaults to zero value. |
| `spec.ttlSecondsAfterFinished` | integer | no | Job cleanup TTL. Defaults to 3600. |
| `spec.maxToolCalls` | integer | no | Budget hint exposed as `FLUXAGENT_MAX_TOOL_CALLS`. |

## Runtime Contract

The executor image should contain `/fluxagent-agent-executor` plus the selected CLI runtime.

FluxAgent mounts:

- `/var/run/fluxagent/evidence/risk-signal.json`
- `/var/run/fluxagent/evidence/prompt.txt`
- `/var/run/fluxagent/result/`

FluxAgent also sets:

- `FLUXAGENT_ANALYSIS_RESULT_NAME`
- `FLUXAGENT_EVIDENCE_PATH`
- `FLUXAGENT_PROMPT_PATH`
- `FLUXAGENT_RESULT_PATH`
- `FLUXAGENT_MAX_TOOL_CALLS`, when configured

The wrapper executes the configured CLI, stores raw CLI output at `FLUXAGENT_RESULT_PATH`, parses structured JSON from stdout, and writes parsed fields into `AgentAnalysisResult.status`.

Expected structured output:

```json
{
  "summary": "Short investigation summary.",
  "rootCause": "Most likely root cause.",
  "confidence": 0.82,
  "validationSteps": ["Check the latest rollout."],
  "recommendations": ["Rollback the bad image."],
  "missingEvidence": ["Application startup logs before restart."]
}
```

The parser accepts direct JSON, JSONL lines containing the same fields, or JSON embedded in a text field or fenced block.

## Credential Boundary

Use workload-scoped credentials such as project API keys, service-account API keys, or enterprise access tokens. Do not package local OAuth caches, ChatGPT sessions, Codex Remote sessions, or interactive CLI auth files as Kubernetes secrets.

## Sample

See:

- [config/samples/agent-executor-codex.yaml](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/config/samples/agent-executor-codex.yaml:1)
- [config/samples/agent-executor-claude.yaml](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/config/samples/agent-executor-claude.yaml:1)
- [config/samples/agent-executor-gemini.yaml](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/config/samples/agent-executor-gemini.yaml:1)
