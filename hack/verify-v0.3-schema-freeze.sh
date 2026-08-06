#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
chart="${root}/charts/fluxseer-rca"

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

echo "==> v0.3 schema freeze audit: Go tests"
(
  cd "${root}"
  GOWORK=off go test ./...
)

echo "==> v0.3 schema freeze audit: CRD source/chart consistency"
for source_crd in "${root}"/config/crd/bases/*.yaml; do
  name="$(basename "${source_crd}")"
  chart_crd="${chart}/crds/${name}"
  if [[ ! -f "${chart_crd}" ]]; then
    echo "missing Helm chart CRD: ${chart_crd}" >&2
    exit 1
  fi
  diff -u "${source_crd}" "${chart_crd}" >/dev/null
done

echo "==> v0.3 schema freeze audit: frozen baseline"
(
  cd "${root}"
  if ! git rev-parse --verify v0.3.0-beta.1^{commit} >/dev/null 2>&1; then
    echo "missing frozen baseline tag: v0.3.0-beta.1" >&2
    exit 1
  fi
  # charts/*/crds is intentionally excluded here: the chart directory was
  # renamed after v0.3.0-beta.1 was tagged, so diffing its new path against
  # the old tag would show a spurious full-file add. The CRD
  # source/chart consistency check above already proves the chart's CRDs
  # equal config/crd/bases in the current tree, and config/crd/bases is
  # diffed against the tag right here, so drift is still fully covered.
  git diff --exit-code v0.3.0-beta.1 -- api/v1alpha1 config/crd/bases >/dev/null
)

echo "==> v0.3 schema freeze audit: Kustomize render"
kubectl kustomize "${root}/config/default" >"${tmpdir}/config-default.yaml"
kubectl kustomize "${root}/examples/kind" >"${tmpdir}/examples-kind.yaml"

echo "==> v0.3 schema freeze audit: Helm lint and render"
helm lint "${chart}"
helm template fluxseer-rca "${chart}" --namespace fluxseer-rca-system >"${tmpdir}/helm-default.yaml"
helm template fluxseer-rca "${chart}" --namespace fluxseer-rca-system \
  --set rulePacks.prometheusBaseline.enabled=true \
  --set rulePacks.lokiBaseline.enabled=true \
  --set metrics.prometheusRule.enabled=true \
  --set metrics.grafanaDashboard.enabled=true \
  >"${tmpdir}/helm-full.yaml"

echo "==> v0.3 schema freeze audit: rule pack rendering"
bash "${root}/hack/verify-rule-packs.sh"

echo "==> v0.3 schema freeze audit: RBAC profile rendering"
bash "${root}/hack/verify-rbac-profiles.sh"

echo "==> v0.3 schema freeze audit: schema contract smoke checks"
grep -q "kind: PrometheusRule" "${tmpdir}/helm-full.yaml"
grep -q "fluxseer_rca_queue_depth" "${tmpdir}/helm-full.yaml"
grep -q "kind: RiskRule" "${tmpdir}/helm-full.yaml"
grep -q "kind: InvestigationRequest" "${root}/config/crd/bases/aiops.platform_investigationrequests.yaml"
grep -q "payloadRef:" "${root}/config/crd/bases/aiops.platform_investigationrequests.yaml"
grep -q "providerResult:" "${root}/config/crd/bases/aiops.platform_investigationrequests.yaml"
grep -q "classification:" "${root}/config/crd/bases/aiops.platform_investigationrequests.yaml"
grep -q "execution:" "${root}/config/crd/bases/aiops.platform_investigationrequests.yaml"

echo "v0.3 schema freeze audit gate passed"
