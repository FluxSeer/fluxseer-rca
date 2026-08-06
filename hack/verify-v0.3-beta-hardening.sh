#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
chart="${root}/charts/fluxagent"

for command_name in go kubectl helm; do
  if ! command -v "${command_name}" >/dev/null 2>&1; then
    echo "required command not found: ${command_name}" >&2
    exit 1
  fi
done

tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "${tmpdir}"
}
trap cleanup EXIT INT TERM

echo "==> v0.3 beta hardening: Go tests"
(
  cd "${root}"
  GOWORK=off go test ./...
)

echo "==> v0.3 beta hardening: CRD source/chart consistency"
for source_crd in "${root}"/config/crd/bases/*.yaml; do
  name="$(basename "${source_crd}")"
  chart_crd="${chart}/crds/${name}"
  if [[ ! -f "${chart_crd}" ]]; then
    echo "missing Helm chart CRD: ${chart_crd}" >&2
    exit 1
  fi
  diff -u "${source_crd}" "${chart_crd}" >/dev/null
done

echo "==> v0.3 beta hardening: Kustomize render"
kubectl kustomize "${root}/config/default" >"${tmpdir}/config-default.yaml"
kubectl kustomize "${root}/examples/kind" >"${tmpdir}/examples-kind.yaml"

echo "==> v0.3 beta hardening: Helm lint and render"
helm lint "${chart}"
helm template fluxagent "${chart}" --namespace fluxagent-system >"${tmpdir}/helm-default.yaml"
helm template fluxagent "${chart}" --namespace fluxagent-system \
  --set rulePacks.prometheusBaseline.enabled=true \
  --set rulePacks.lokiBaseline.enabled=true \
  --set metrics.prometheusRule.enabled=true \
  --set metrics.grafanaDashboard.enabled=true \
  >"${tmpdir}/helm-full.yaml"

echo "==> v0.3 beta hardening: rule pack rendering"
bash "${root}/hack/verify-rule-packs.sh"

echo "==> v0.3 beta hardening: RBAC profile rendering"
bash "${root}/hack/verify-rbac-profiles.sh"

echo "==> v0.3 beta hardening: schema contract smoke checks"
grep -q "kind: PrometheusRule" "${tmpdir}/helm-full.yaml"
grep -q "fluxagent_queue_depth" "${tmpdir}/helm-full.yaml"
grep -q "kind: RiskRule" "${tmpdir}/helm-full.yaml"
grep -q "kind: InvestigationRequest" "${root}/config/crd/bases/aiops.platform_investigationrequests.yaml"
grep -q "payloadRef:" "${root}/config/crd/bases/aiops.platform_investigationrequests.yaml"
grep -q "providerResult:" "${root}/config/crd/bases/aiops.platform_investigationrequests.yaml"
grep -q "classification:" "${root}/config/crd/bases/aiops.platform_investigationrequests.yaml"
grep -q "execution:" "${root}/config/crd/bases/aiops.platform_investigationrequests.yaml"
grep -q "egressAttempts:" "${root}/config/crd/bases/aiops.platform_investigationrequests.yaml"

echo "v0.3 beta hardening gate passed"
