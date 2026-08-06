#!/usr/bin/env bash
set -euo pipefail

namespace="${FLUXSEER_RCA_DEMO_NAMESPACE:-fluxseer-rca-demo}"
rule_name="${FLUXSEER_RCA_RULE_NAME:-fluxseer-rca-sample-latency}"
target_name="${FLUXSEER_RCA_TARGET_NAME:-fluxseer-rca-sample}"
mode="${1:-}"

if [[ -z "${mode}" ]]; then
  echo "usage: bash examples/kind/degraded-demo.sh <missing-datasource|capability-mismatch|provider-auth-failed|reset>"
  exit 1
fi

apply_hosted_provider_mocks() {
  kubectl apply -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: openai-demo-secret
  namespace: ${namespace}
type: Opaque
stringData:
  api-key: demo-openai-token
---
apiVersion: aiops.platform/v1alpha1
kind: ModelProvider
metadata:
  name: openai-auth-failed
  namespace: ${namespace}
spec:
  provider: openai
  model: gpt-5.1
  endpoint: http://fluxseer-rca-observability:8080/demo/providers/openai/auth-failed
  timeout: 2s
  maxTokens: 256
  apiKeySecretRef:
    name: openai-demo-secret
    key: api-key
EOF
}

case "${mode}" in
  missing-datasource)
    kubectl patch riskrule "${rule_name}" -n "${namespace}" --type json -p='[
      {"op":"replace","path":"/spec/signals/1/datasourceRef/name","value":"logs-missing"}
    ]'
    echo "patched RiskRule to reference missing datasource logs-missing"
    echo "expected conditions:"
    echo "- RiskRule: DatasourceResolved=False, Ready=False"
    echo "- RiskSignal: EvidenceCollectionReady=False"
    ;;
  capability-mismatch)
    kubectl patch riskrule "${rule_name}" -n "${namespace}" --type json -p='[
      {"op":"replace","path":"/spec/signals/0/queryType","value":"log"}
    ]'
    echo "patched RiskRule so prometheus datasource is asked to serve queryType=log"
    echo "expected conditions:"
    echo "- RiskRule: QueryTypeSupported=False, Ready=False"
    echo "- RiskSignal: EvidenceCollectionReady=False"
    ;;
  provider-auth-failed)
    apply_hosted_provider_mocks
    kubectl patch riskrule "${rule_name}" -n "${namespace}" --type json -p='[
      {"op":"replace","path":"/spec/ai/providerRef/name","value":"openai-auth-failed"}
    ]'
    echo "patched RiskRule RCA provider to hosted provider openai-auth-failed"
    echo "expected conditions:"
    echo "- RiskRule: Ready=True"
    echo "- RiskSignal: EvidenceCollectionReady=True, RCAReady=False (ProviderAuthFailed)"
    ;;
  reset)
    kubectl apply -k examples/riskrules -n "${namespace}"
    echo "reapplied baseline RiskRule manifests"
    ;;
  *)
    echo "unknown mode: ${mode}"
    echo "usage: bash examples/kind/degraded-demo.sh <missing-datasource|capability-mismatch|provider-auth-failed|reset>"
    exit 1
    ;;
esac

echo "waiting for controller reconcile..."
sleep 20

signal_name="$(kubectl get risksignal -n "${namespace}" \
  -l "fluxseer-rca.aiops.platform/risk-rule=${rule_name}" \
  --sort-by=.metadata.creationTimestamp \
  -o "jsonpath={range .items[?(@.spec.target.name==\"${target_name}\")]}{.metadata.name}{\"\\n\"}{end}" 2>/dev/null |
  tail -n1)"

kubectl get datasource,riskrule,risksignal -n "${namespace}" || true
kubectl describe riskrule "${rule_name}" -n "${namespace}"
if [[ -n "${signal_name}" ]]; then
  kubectl describe risksignal "${signal_name}" -n "${namespace}" || true
fi
