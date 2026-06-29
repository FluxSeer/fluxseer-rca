# Enable Hosted Model Providers

This tutorial explains how to wire FluxAgent RCA to hosted `openai`, `gemini`, or `claude` providers through `ModelProvider`.

## What This Does

FluxAgent keeps vendor choice out of `RiskRule` itself.

The flow is:

1. create a token `Secret`
2. create a `ModelProvider`
3. point `RiskRule.spec.ai.providerRef.name` at that provider
4. wait for `RiskSignal.status.rcaProvider` and `RCAReady=True`

## 1. Create A Secret

Examples:

- [config/samples/model-provider-openai-secret.yaml](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/config/samples/model-provider-openai-secret.yaml:1)
- [config/samples/model-provider-gemini-secret.yaml](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/config/samples/model-provider-gemini-secret.yaml:1)
- [config/samples/model-provider-claude-secret.yaml](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/config/samples/model-provider-claude-secret.yaml:1)

Replace the placeholder value, then apply:

```bash
kubectl apply -f config/samples/model-provider-openai-secret.yaml
```

## 2. Create A ModelProvider

Examples:

- [config/samples/model-provider-openai.yaml](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/config/samples/model-provider-openai.yaml:1)
- [config/samples/model-provider-gemini.yaml](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/config/samples/model-provider-gemini.yaml:1)
- [config/samples/model-provider-claude.yaml](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/config/samples/model-provider-claude.yaml:1)

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

## Failure Diagnosis

Common failure reasons surfaced through `RCAReady=False`:

- `ProviderNotFound`
- `ProviderUnsupported`
- `SecretRefMissing`
- `SecretNotFound`
- `SecretKeyMissing`
- `SecretValueEmpty`
- `APIKeyMissing`
- `ProviderUnavailable`
- `InvalidProviderResponse`

Examples:

- missing Secret: `SecretNotFound`
- empty or missing key inside Secret: `SecretKeyMissing` or `SecretValueEmpty`
- provider returned non-JSON or incomplete JSON: `InvalidProviderResponse`
- endpoint or vendor API unavailable: `ProviderUnavailable`

## Notes

- Hosted providers require real network reachability from the FluxAgent manager pod.
- FluxAgent redacts evidence before provider-bound reasoning.
- `RiskRule` remains read-only even when hosted providers are enabled.
- This path is suitable for RCA enrichment, not autonomous remediation.
