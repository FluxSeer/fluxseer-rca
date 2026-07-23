#!/usr/bin/env bash
set -euo pipefail

: "${VERSION:?VERSION is required}"
: "${GIT_COMMIT:?GIT_COMMIT is required}"
: "${GIT_DIRTY:?GIT_DIRTY is required}"
: "${BUILD_DATE:?BUILD_DATE is required}"
: "${OPERATOR_IMAGE_REF:?OPERATOR_IMAGE_REF is required}"
: "${DEMO_IMAGE_REF:?DEMO_IMAGE_REF is required}"

if [[ -z "${VERSION}" ]]; then
  echo "VERSION must not be empty" >&2
  exit 1
fi

if [[ ! "${GIT_COMMIT}" =~ ^[0-9a-f]{40}$ ]]; then
  echo "GIT_COMMIT must be a 40-character lowercase SHA: ${GIT_COMMIT}" >&2
  exit 1
fi

if [[ ! "${BUILD_DATE}" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]]; then
  echo "BUILD_DATE must be UTC RFC3339 seconds: ${BUILD_DATE}" >&2
  exit 1
fi

if [[ "${VERSION}" != "dev" && "${GIT_DIRTY}" != "false" ]]; then
  echo "release VERSION requires GIT_DIRTY=false, got ${GIT_DIRTY}" >&2
  exit 1
fi

json_value() {
  local json="$1"
  local key="$2"
  sed -nE 's/.*"'${key}'":"([^"]*)".*/\1/p' <<<"${json}"
}

assert_equal() {
  local label="$1"
  local expected="$2"
  local actual="$3"
  if [[ "${actual}" != "${expected}" ]]; then
    echo "${label}: expected ${expected}, got ${actual}" >&2
    exit 1
  fi
}

verify_image() {
  local image="$1"
  local title="$2"

  echo "verifying artifact identity for ${image}"

  local version_json
  version_json="$(docker run --rm "${image}" version --output=json)"

  assert_equal "${image} binary version" "${VERSION}" "$(json_value "${version_json}" version)"
  assert_equal "${image} binary gitCommit" "${GIT_COMMIT}" "$(json_value "${version_json}" gitCommit)"
  assert_equal "${image} binary gitDirty" "${GIT_DIRTY}" "$(json_value "${version_json}" gitDirty)"
  assert_equal "${image} binary buildDate" "${BUILD_DATE}" "$(json_value "${version_json}" buildDate)"

  assert_equal "${image} OCI title" "${title}" "$(docker image inspect "${image}" --format '{{ index .Config.Labels "org.opencontainers.image.title" }}')"
  assert_equal "${image} OCI version" "${VERSION}" "$(docker image inspect "${image}" --format '{{ index .Config.Labels "org.opencontainers.image.version" }}')"
  assert_equal "${image} OCI revision" "${GIT_COMMIT}" "$(docker image inspect "${image}" --format '{{ index .Config.Labels "org.opencontainers.image.revision" }}')"
  assert_equal "${image} OCI created" "${BUILD_DATE}" "$(docker image inspect "${image}" --format '{{ index .Config.Labels "org.opencontainers.image.created" }}')"
  assert_equal "${image} OCI dirty" "${GIT_DIRTY}" "$(docker image inspect "${image}" --format '{{ index .Config.Labels "io.fluxagent.git-dirty" }}')"
}

verify_image "${OPERATOR_IMAGE_REF}" "FluxAgent"
verify_image "${DEMO_IMAGE_REF}" "FluxAgent Demo Observability"

echo "artifact identity verified"
