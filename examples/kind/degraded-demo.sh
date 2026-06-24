#!/usr/bin/env bash
set -euo pipefail

namespace="${FLUXAGENT_DEMO_NAMESPACE:-fluxagent-demo}"
rule_name="${FLUXAGENT_RULE_NAME:-fluxagent-sample-latency}"
signal_name="${FLUXAGENT_SIGNAL_NAME:-fluxagent-sample-latency-fluxagent-sample-risk}"
mode="${1:-}"

if [[ -z "${mode}" ]]; then
  echo "usage: bash examples/kind/degraded-demo.sh <missing-datasource|capability-mismatch|reset>"
  exit 1
fi

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
  reset)
    kubectl apply -k examples/riskrules -n "${namespace}"
    echo "reapplied baseline RiskRule manifests"
    ;;
  *)
    echo "unknown mode: ${mode}"
    echo "usage: bash examples/kind/degraded-demo.sh <missing-datasource|capability-mismatch|reset>"
    exit 1
    ;;
esac

echo "waiting for controller reconcile..."
sleep 20

kubectl get datasource,riskrule,risksignal -n "${namespace}" || true
kubectl describe riskrule "${rule_name}" -n "${namespace}"
kubectl describe risksignal "${signal_name}" -n "${namespace}" || true
