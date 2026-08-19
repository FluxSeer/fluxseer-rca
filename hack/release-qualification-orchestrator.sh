#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"

: "${RELEASE_VERSION:?RELEASE_VERSION is required}"
: "${SOURCE_COMMIT:?SOURCE_COMMIT is required}"

REGISTRY="${REGISTRY:-ghcr.io}"
REPOSITORY_PREFIX="${REPOSITORY_PREFIX:-fluxseer/fluxseer-rca}"
RELEASE_CHART_NAME="${RELEASE_CHART_NAME:-fluxseer-rca}"
RELEASE_CHART_VERSION="${RELEASE_CHART_VERSION:-${RELEASE_VERSION#v}}"
RELEASE_OPERATOR_IMAGE_REPOSITORY="${RELEASE_OPERATOR_IMAGE_REPOSITORY:-${REGISTRY}/${REPOSITORY_PREFIX}/operator}"
RELEASE_DEMO_IMAGE_REPOSITORY="${RELEASE_DEMO_IMAGE_REPOSITORY:-${REGISTRY}/${REPOSITORY_PREFIX}/demo-observability}"
RELEASE_CHART_OCI="${RELEASE_CHART_OCI:-oci://${REGISTRY}/${REPOSITORY_PREFIX}/charts/${RELEASE_CHART_NAME}}"
RELEASE_QUALIFICATION_ARTIFACT_ROOT="${RELEASE_QUALIFICATION_ARTIFACT_ROOT:-${repo_root}/reports/release-qualification/${RELEASE_VERSION}}"
RELEASE_WORKFLOW="${RELEASE_WORKFLOW:-release.yml}"
RELEASE_WORKFLOW_REF="${RELEASE_WORKFLOW_REF:-main}"
PUBLISH_CANDIDATE="${PUBLISH_CANDIDATE:-false}"
RUN_RCA="${RUN_RCA:-true}"
RUN_REMEDIATION="${RUN_REMEDIATION:-true}"
RELEASE_TIMEOUT_SECONDS="${RELEASE_TIMEOUT_SECONDS:-1800}"
RELEASE_POLL_SECONDS="${RELEASE_POLL_SECONDS:-10}"
RELEASE_KIND_RCA_CLUSTER_NAME="${RELEASE_KIND_RCA_CLUSTER_NAME:-fluxseer-release-rca}"
RELEASE_KIND_REMEDIATION_CLUSTER_NAME="${RELEASE_KIND_REMEDIATION_CLUSTER_NAME:-fluxseer-release-remediation}"

export RELEASE_VERSION RELEASE_CHART_VERSION RELEASE_CHART_OCI RELEASE_OPERATOR_IMAGE_REPOSITORY
export RELEASE_QUALIFICATION_ARTIFACT_ROOT RELEASE_KIND_RCA_CLUSTER_NAME
export RELEASE_KIND_REMEDIATION_CLUSTER_NAME

if [[ ! "${RELEASE_VERSION}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+-rc\.[0-9A-Za-z.-]+$ ]]; then
  echo "release qualification requires a candidate version like v0.5.0-alpha.2-rc.<sha>" >&2
  exit 2
fi
if [[ ! "${SOURCE_COMMIT}" =~ ^[0-9a-fA-F]{40}$ ]]; then
  echo "SOURCE_COMMIT must be a full 40-character commit SHA" >&2
  exit 2
fi
if [[ "${PUBLISH_CANDIDATE}" != "true" && "${PUBLISH_CANDIDATE}" != "false" ]]; then
  echo "PUBLISH_CANDIDATE must be true or false" >&2
  exit 2
fi

mkdir -p "${RELEASE_QUALIFICATION_ARTIFACT_ROOT}"
log_file="${RELEASE_QUALIFICATION_ARTIFACT_ROOT}/orchestrator.log"
exec > >(tee -a "${log_file}") 2>&1

credential_root="$(mktemp -d "${TMPDIR:-/tmp}/fluxseer-release-qualification.XXXXXX")"
export DOCKER_CONFIG="${credential_root}/docker"
export HELM_CONFIG_HOME="${credential_root}/helm"
export HELM_CACHE_HOME="${credential_root}/helm-cache"
export HELM_DATA_HOME="${credential_root}/helm-data"
export HELM_REGISTRY_CONFIG="${HELM_CONFIG_HOME}/registry/config.json"
mkdir -p "${DOCKER_CONFIG}" "${HELM_CONFIG_HOME}/registry" "${HELM_CACHE_HOME}" "${HELM_DATA_HOME}"
printf '{}\n' >"${HELM_REGISTRY_CONFIG}"
unset DOCKER_AUTH_CONFIG

cleanup() {
  local exit_code=$?
  trap - EXIT INT TERM

  if command -v kind >/dev/null 2>&1; then
    for cluster in "${RELEASE_KIND_RCA_CLUSTER_NAME}" "${RELEASE_KIND_REMEDIATION_CLUSTER_NAME}"; do
      kind delete cluster --name "${cluster}" >/dev/null 2>&1 || true
      if kind get clusters 2>/dev/null | grep -qx "${cluster}"; then
        echo "cleanup failed: kind cluster remains: ${cluster}" >&2
        exit_code=1
      fi
    done
  fi

  if [[ -d "${credential_root}" ]]; then
    rm -rf "${credential_root}"
  fi
  if [[ -e "${credential_root}" ]]; then
    echo "cleanup failed: temporary credential state remains: ${credential_root}" >&2
    exit_code=1
  fi

  exit "${exit_code}"
}
trap cleanup EXIT INT TERM

run_gate() {
  local name="$1"
  shift
  log_section "Release Qualification: ${name}"
  "$@"
  echo "PASS ${name}"
}

log_section() {
  printf '\n%s\n' "============================================================"
  printf '%s\n' "$1"
  printf '%s\n' "============================================================"
}

verify_source() {
  local resolved
  resolved="$(git rev-parse "${SOURCE_COMMIT}^{commit}")"
  local head
  head="$(git rev-parse HEAD)"
  [[ "${resolved}" == "${SOURCE_COMMIT}" ]]
  [[ "${head}" == "${SOURCE_COMMIT}" ]]
  [[ -z "$(git status --porcelain)" ]]
  echo "sourceCommit=${SOURCE_COMMIT}"
}

wait_for_candidate_publication() {
  command -v gh >/dev/null 2>&1 || { echo "gh is required to dispatch candidate publication" >&2; return 1; }
  [[ -n "${GH_TOKEN:-}" ]] || { echo "GH_TOKEN is required to dispatch candidate publication" >&2; return 1; }

  local dispatched_after
  dispatched_after="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  gh workflow run "${RELEASE_WORKFLOW}" \
    --ref "${RELEASE_WORKFLOW_REF}" \
    -f "version=${RELEASE_VERSION}" \
    -f "source_ref=${SOURCE_COMMIT}" \
    -f candidate=true \
    -f create_github_release=false \
    -f release_only=false

  local deadline=$((SECONDS + RELEASE_TIMEOUT_SECONDS))
  local run_id=""
  until [[ -n "${run_id}" ]]; do
    run_id="$(gh run list --workflow "${RELEASE_WORKFLOW}" --event workflow_dispatch --limit 20 \
      --json databaseId,createdAt \
      | jq -r --arg after "${dispatched_after}" '.[] | select(.createdAt >= $after) | .databaseId' | head -n1)"
    if [[ -n "${run_id}" ]]; then
      break
    fi
    if (( SECONDS >= deadline )); then
      echo "timed out waiting for candidate publication workflow run" >&2
      return 1
    fi
    sleep "${RELEASE_POLL_SECONDS}"
  done

  gh run watch "${run_id}" --exit-status
  local conclusion
  conclusion="$(gh run view "${run_id}" --json conclusion --jq '.conclusion')"
  [[ "${conclusion}" == "success" ]]
  echo "candidatePublicationRun=${run_id}"
}

verify_anonymous_distribution() {
  local operator_ref="${RELEASE_OPERATOR_IMAGE_REPOSITORY}:${RELEASE_VERSION}"
  local demo_ref="${RELEASE_DEMO_IMAGE_REPOSITORY}:${RELEASE_VERSION}"
  local pull_dir="${RELEASE_QUALIFICATION_ARTIFACT_ROOT}/anonymous-pulls"
  mkdir -p "${pull_dir}"

  docker pull --platform linux/amd64 "${operator_ref}"
  docker pull --platform linux/amd64 "${demo_ref}"
  docker buildx imagetools inspect "${operator_ref}" >"${pull_dir}/operator-imagetools.txt"
  docker buildx imagetools inspect "${demo_ref}" >"${pull_dir}/demo-imagetools.txt"

  helm pull "${RELEASE_CHART_OCI}" \
    --version "${RELEASE_CHART_VERSION}" \
    --destination "${pull_dir}"
  helm show chart "${RELEASE_CHART_OCI}" --version "${RELEASE_CHART_VERSION}" \
    | tee "${pull_dir}/chart-metadata.txt"
  grep -Fxq "version: ${RELEASE_CHART_VERSION}" "${pull_dir}/chart-metadata.txt"
  grep -Fxq "appVersion: ${RELEASE_VERSION}" "${pull_dir}/chart-metadata.txt"
}

run_kind_profile() {
  local profile="$1"
  local profile_root="${RELEASE_QUALIFICATION_ARTIFACT_ROOT}/${profile}-clean-room"
  mkdir -p "${profile_root}"
  RELEASE_QUALIFICATION_ARTIFACT_ROOT="${profile_root}" \
    bash "${script_dir}/release-qualification-kind.sh" "${profile}"
}

log_section "Release Qualification Orchestrator"
echo "version=${RELEASE_VERSION}"
echo "sourceCommit=${SOURCE_COMMIT}"
echo "candidatePublication=${PUBLISH_CANDIDATE}"
echo "operatorImage=${RELEASE_OPERATOR_IMAGE_REPOSITORY}:${RELEASE_VERSION}"
echo "chart=${RELEASE_CHART_OCI}:${RELEASE_CHART_VERSION}"

run_gate "source" verify_source
if [[ "${PUBLISH_CANDIDATE}" == "true" ]]; then
  run_gate "candidate publication" wait_for_candidate_publication
fi
run_gate "anonymous distribution" verify_anonymous_distribution
if [[ "${RUN_RCA}" == "true" ]]; then
  run_gate "default clean-room" run_kind_profile rca
fi
if [[ "${RUN_REMEDIATION}" == "true" ]]; then
  run_gate "experimental clean-room" run_kind_profile experimental
fi

log_section "Release Qualification Orchestrator Completed"
echo "All enabled qualification gates passed."
