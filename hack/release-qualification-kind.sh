#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"

profile="${1:-}"
case "${profile}" in
  rca|experimental) ;;
  *)
    echo "usage: $0 <rca|experimental>" >&2
    exit 2
    ;;
esac

: "${RELEASE_VERSION:?RELEASE_VERSION is required}"
: "${RELEASE_OPERATOR_IMAGE_REPOSITORY:?RELEASE_OPERATOR_IMAGE_REPOSITORY is required}"
: "${RELEASE_CHART_OCI:?RELEASE_CHART_OCI is required}"
: "${RELEASE_CHART_VERSION:?RELEASE_CHART_VERSION is required}"
: "${RELEASE_QUALIFICATION_ARTIFACT_ROOT:?RELEASE_QUALIFICATION_ARTIFACT_ROOT is required}"

export VERSION="${RELEASE_VERSION}"
export IMAGE_TAG="${RELEASE_VERSION}"
export LIVE_HARNESS_VERSION="${RELEASE_VERSION}"
export LIVE_HARNESS_IMAGE_TAG="${RELEASE_VERSION}"
export LIVE_HARNESS_IMAGE_REPOSITORY="${RELEASE_OPERATOR_IMAGE_REPOSITORY}"
export LIVE_HARNESS_ARTIFACT_ROOT="${RELEASE_QUALIFICATION_ARTIFACT_ROOT}"
export LIVE_HARNESS_KEEP_CLUSTER=false
export LIVE_HARNESS_TIMEOUT_SECONDS="${LIVE_HARNESS_TIMEOUT_SECONDS:-900}"
export LIVE_HARNESS_RELEASE_NAME="fluxseer-rca"
export LIVE_HARNESS_RELEASE_NAMESPACE="fluxseer-rca-system"

if [[ "${profile}" == "rca" ]]; then
  export LIVE_HARNESS_CLUSTER_NAME="${RELEASE_KIND_RCA_CLUSTER_NAME:-fluxseer-release-rca}"
  export LIVE_HARNESS_RBAC_PROFILE="readOnlyRCA"
  export LIVE_HARNESS_ENABLE_REMEDIATION=false
  export LIVE_HARNESS_ENABLE_POLICY_PACK=false
  export LIVE_HARNESS_ENABLE_EXPERIMENTAL_EXECUTOR=false
  export LIVE_HARNESS_VERIFY_READ_ONLY=true
  source "${repo_root}/test/e2e/kind/scenarios/run_representative_rca.sh"
else
  export LIVE_HARNESS_CLUSTER_NAME="${RELEASE_KIND_REMEDIATION_CLUSTER_NAME:-fluxseer-release-remediation}"
  export LIVE_HARNESS_RBAC_PROFILE="experimentalExecutor"
  export LIVE_HARNESS_ENABLE_REMEDIATION=true
  export LIVE_HARNESS_ENABLE_POLICY_PACK=false
  export LIVE_HARNESS_ENABLE_EXPERIMENTAL_EXECUTOR=true
  export LIVE_HARNESS_VERIFY_READ_ONLY=false
  source "${repo_root}/test/e2e/kind/remediation/run_safe_remediation.sh"
fi

live_harness_build_and_load_image() {
  log_section "Use Anonymous OCI Operator Image"
  echo "kind image loading is intentionally disabled for release qualification"
  echo "operator image: ${LIVE_HARNESS_OPERATOR_IMAGE_REF}"
}

live_harness_install() {
  log_section "Install FluxSeer From Anonymous OCI Chart"
  live_harness_helm upgrade --install "${LIVE_HARNESS_RELEASE_NAME}" "${RELEASE_CHART_OCI}" \
    --version "${RELEASE_CHART_VERSION}" \
    --namespace "${LIVE_HARNESS_RELEASE_NAMESPACE}" \
    --create-namespace \
    --wait \
    --timeout 180s \
    --set-string image.repository="${LIVE_HARNESS_IMAGE_REPOSITORY}" \
    --set-string image.tag="${LIVE_HARNESS_IMAGE_TAG}" \
    --set image.pullPolicy=IfNotPresent \
    --set rbac.profile="${LIVE_HARNESS_RBAC_PROFILE}" \
    --set controller.enableRemediation="${LIVE_HARNESS_ENABLE_REMEDIATION}" \
    --set controller.enablePolicyPack="${LIVE_HARNESS_ENABLE_POLICY_PACK}" \
    --set features.remediation.enabled="${LIVE_HARNESS_ENABLE_REMEDIATION}" \
    --set features.experimentalExecutor.enabled="${LIVE_HARNESS_ENABLE_EXPERIMENTAL_EXECUTOR}" \
    --set features.policyPack.enabled="${LIVE_HARNESS_ENABLE_POLICY_PACK}" \
    --set rulePacks.kubernetesBaseline.enabled=false \
    --set rulePacks.prometheusBaseline.enabled=false \
    --set rulePacks.lokiBaseline.enabled=false \
    --set rulePacks.applicationProfiles.enabled=false

  live_harness_wait_for_crds
  live_harness_kubectl rollout status \
    "deployment/${LIVE_HARNESS_RELEASE_NAME}-controller-manager" \
    -n "${LIVE_HARNESS_RELEASE_NAMESPACE}" \
    --timeout=180s
  live_harness_kubectl wait \
    --for=condition=Available=True \
    "deployment/${LIVE_HARNESS_RELEASE_NAME}-controller-manager" \
    -n "${LIVE_HARNESS_RELEASE_NAMESPACE}" \
    --timeout=180s
}

if [[ "${profile}" == "rca" ]]; then
  live_harness_run run_representative_rca_scenarios
else
  run_release_effective_remediation() {
    remediation_assert_rbac
    remediation_run_case effective Effective
  }

  live_harness_run run_release_effective_remediation
fi
