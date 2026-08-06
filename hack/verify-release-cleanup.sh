#!/usr/bin/env bash
set -euo pipefail

version="${VERSION:?VERSION is required}"

if [[ -z "${version}" || "${version}" == "dev" ]]; then
  echo "release VERSION must not be empty or dev" >&2
  exit 1
fi

for command_name in kind docker kubectl helm jq; do
  if ! command -v "${command_name}" >/dev/null 2>&1; then
    echo "missing required command: ${command_name}" >&2
    exit 1
  fi
done

if ! docker info >/dev/null 2>&1; then
  echo "docker daemon is not reachable" >&2
  exit 1
fi

clusters="$(kind get clusters 2>/dev/null || true)"
if [[ -n "${clusters}" ]]; then
  echo "kind clusters remain after release gate:" >&2
  echo "${clusters}" >&2
  exit 1
fi

running_release_containers="$(docker ps -a --format '{{.ID}} {{.Image}}' | grep -E ' fluxseer/fluxseer-rca/(operator|demo-observability):release-' || true)"
if [[ -n "${running_release_containers}" ]]; then
  echo "FluxSeer RCA release test containers are still running:" >&2
  echo "${running_release_containers}" >&2
  exit 1
fi

release_test_tags=(
  fluxseer/fluxseer-rca/operator:release-rulepack-test
  fluxseer/fluxseer-rca/demo-observability:release-rulepack-test
  fluxseer/fluxseer-rca/operator:release-e2e-test
  fluxseer/fluxseer-rca/demo-observability:release-e2e-test
  fluxseer/fluxseer-rca/operator:release-investigation-test
  fluxseer/fluxseer-rca/demo-observability:release-investigation-test
  fluxseer/fluxseer-rca/operator:release-identity-test
  fluxseer/fluxseer-rca/demo-observability:release-identity-test
  fluxseer/fluxseer-rca/operator:release-packaging-test
  fluxseer/fluxseer-rca/demo-observability:release-packaging-test
  fluxseer/fluxseer-rca/operator:release-reproducibility-test
  fluxseer/fluxseer-rca/demo-observability:release-reproducibility-test
  fluxseer/fluxseer-rca/operator:release-lifecycle-test
  fluxseer/fluxseer-rca/demo-observability:release-lifecycle-test
  fluxseer/fluxseer-rca/operator:release-upgrade-test
  fluxseer/fluxseer-rca/demo-observability:release-upgrade-test
)

docker image rm "${release_test_tags[@]}" >/dev/null 2>&1 || true

remaining_release_images="$(docker image ls --format '{{.Repository}}:{{.Tag}}' | grep -E '^fluxseer/fluxseer-rca/(operator|demo-observability):release-' || true)"
if [[ -n "${remaining_release_images}" ]]; then
  echo "FluxSeer RCA release test images remain:" >&2
  echo "${remaining_release_images}" >&2
  exit 1
fi

echo "release cleanup verified"
