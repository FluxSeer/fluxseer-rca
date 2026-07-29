# Enable Hosted Model Providers

This tutorial explains how to wire FluxAgent RCA to hosted `openai`, `gemini`, or `claude` providers through `ModelProvider`.

## What This Does

FluxAgent keeps vendor choice out of `RiskRule` itself.

The flow is:

1. create a token `Secret`
2. create a `ModelProvider`
3. point `RiskRule.spec.ai.providerRef.name` at that provider
4. wait for `RiskSignal.status.rcaProvider` and `RCAReady=True`

`RCAReady=True` means an RCA result is available. It does not indicate that the target workload is healthy or remediated.

## 1. Create A Secret

Provider credential `Secret` objects must be created in the same namespace as the `ModelProvider`. With the default Helm install, FluxAgent grants Secret read permission only in the controller namespace. Cross-namespace provider credentials require explicit additional RBAC and are not part of the default profile.

Examples:

- [config/samples/model-provider-openai-secret.yaml](../../config/samples/model-provider-openai-secret.yaml)
- [config/samples/model-provider-gemini-secret.yaml](../../config/samples/model-provider-gemini-secret.yaml)
- [config/samples/model-provider-claude-secret.yaml](../../config/samples/model-provider-claude-secret.yaml)

Replace the placeholder value, then apply:

```bash
kubectl apply -f config/samples/model-provider-openai-secret.yaml
```

## 2. Create A ModelProvider

Examples:

- [config/samples/model-provider-openai.yaml](../../config/samples/model-provider-openai.yaml)
- [config/samples/model-provider-gemini.yaml](../../config/samples/model-provider-gemini.yaml)
- [config/samples/model-provider-claude.yaml](../../config/samples/model-provider-claude.yaml)

Apply one or more:

```bash
kubectl apply -f config/samples/model-provider-openai.yaml
kubectl apply -f config/samples/model-provider-gemini.yaml
kubectl apply -f config/samples/model-provider-claude.yaml
```

## 3. Point RiskRule At The Provider

Edit your `RiskRule`:

```yaml
ai:
  rcaEnabled: true
  providerRef:
    name: openai-provider
```

Switch vendors by changing only `providerRef.name`:

- `openai-provider`
- `gemini-provider`
- `claude-provider`

## 4. Validate RCA Output

Check the rule and generated signal:

```bash
kubectl get riskrule -A
kubectl get risksignal -A
kubectl describe risksignal <signal-name> -n <namespace>
kubectl get risksignal <signal-name> -n <namespace> -o yaml
```

Expected fields:

- `status.rcaProvider`
- `status.rcaSummary`
- `status.rcaHypothesis`
- `status.conditions[type=RCAReady].status=True`
- for `InvestigationRequest`-based flows, `status.execution.egressAudit.decision=Allowed`

Hosted provider samples intentionally set:

```yaml
dataPolicy:
  allowExternalTransmission: true
  maximumClassification: Internal
  allowedEvidenceKinds:
    - MetricObservation
    - KubernetesEventObservation
    - DeploymentConditionObservation
  deniedSensitivityTags:
    - CredentialLike
    - PersonalData
  allowLogSamples: false
  requireRedaction: true
```

This allows low-risk metric, event, and deployment-condition metadata while keeping log samples and credential-like content out of hosted provider requests.

## Failure Diagnosis

Common failure reasons surfaced through `RCAReady=False`:

- `ProviderNotFound`
- `ProviderUnsupported`
- `ProviderAuthFailed`
- `ProviderRateLimited`
- `ProviderRequestInvalid`
- `SecretRefMissing`
- `SecretNotFound`
- `SecretKeyMissing`
- `SecretValueEmpty`
- `APIKeyMissing`
- `ProviderUnavailable`
- `InvalidProviderResponse`
- `ProviderDataPolicyDenied`
- `ProviderDataPolicyRejected`

Examples:

- missing Secret: `SecretNotFound`
- empty or missing key inside Secret: `SecretKeyMissing` or `SecretValueEmpty`
- invalid or unauthorized token: `ProviderAuthFailed`
- vendor rate limiting after retry exhaustion: `ProviderRateLimited`
- rejected model, endpoint path, or unsupported request shape: `ProviderRequestInvalid`
- provider returned non-JSON or incomplete JSON: `InvalidProviderResponse`
- endpoint or vendor API unavailable: `ProviderUnavailable`
- hosted provider configured without explicit external egress opt-in: `ProviderDataPolicyDenied`
- evidence exceeds `maximumClassification`, contains denied sensitivity tags, or lacks required redaction metadata: `ProviderDataPolicyRejected`

## Notes

- Hosted providers require real network reachability from the FluxAgent manager pod.
- Hosted provider `apiKeySecretRef` is namespaced to the `ModelProvider`; the API does not support arbitrary cross-namespace Secret references.
- FluxAgent redacts evidence before provider-bound reasoning, but redaction does not automatically lower data classification.
- If `spec.timeout` is omitted, hosted providers default to `15s` per request.
- Hosted providers retry transient timeout, `429`, and `5xx` failures up to 3 attempts total.
- `RiskRule` remains read-only even when hosted providers are enabled.
- This path is suitable for RCA enrichment, not autonomous remediation.
