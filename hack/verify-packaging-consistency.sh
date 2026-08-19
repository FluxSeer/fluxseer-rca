#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

version="${VERSION:?VERSION is required}"
chart_version="${CHART_VERSION:-${version#v}}"
image_repository="${IMAGE_REPOSITORY:-fluxseer/fluxseer-rca/operator}"
demo_image_repository="${DEMO_IMAGE_REPOSITORY:-fluxseer/fluxseer-rca/demo-observability}"
image_tag="${IMAGE_TAG:-$version}"
verify_source_chart_metadata="${VERIFY_SOURCE_CHART_METADATA:-true}"

if [[ -z "$version" || "$version" == "dev" ]]; then
  echo "VERSION must be a release candidate version for packaging verification" >&2
  exit 1
fi

chart_yaml="$root/charts/fluxseer-rca/Chart.yaml"
values_yaml="$root/charts/fluxseer-rca/values.yaml"

actual_chart_name="$(awk -F': *' '$1 == "name" {print $2; exit}' "$chart_yaml" | tr -d '"')"
actual_chart_version="$(awk -F': *' '$1 == "version" {print $2; exit}' "$chart_yaml" | tr -d '"')"
actual_app_version="$(awk -F': *' '$1 == "appVersion" {print $2; exit}' "$chart_yaml" | tr -d '"')"

if [[ "$actual_chart_name" != "fluxseer-rca" ]]; then
  echo "chart name mismatch: expected fluxseer-rca, got $actual_chart_name" >&2
  exit 1
fi

case "$verify_source_chart_metadata" in
  true)
    if [[ "$actual_chart_version" != "$chart_version" ]]; then
      echo "chart version mismatch: expected $chart_version, got $actual_chart_version" >&2
      exit 1
    fi

    if [[ "$actual_app_version" != "$version" ]]; then
      echo "chart appVersion mismatch: expected $version, got $actual_app_version" >&2
      exit 1
    fi
    ;;
  false)
    echo "source chart metadata match deferred to release packaging overrides" >&2
    ;;
  *)
    echo "VERIFY_SOURCE_CHART_METADATA must be true or false" >&2
    exit 1
    ;;
esac

if awk '
  $1 == "tag:" {
    value = $2
    gsub(/"/, "", value)
    if (value == "latest") found = 1
  }
  END { exit found ? 0 : 1 }
' "$values_yaml"; then
  echo "Helm values image.tag must not default to latest" >&2
  exit 1
fi

release_paths=(
  "$root/Makefile"
  "$root/charts/fluxseer-rca"
  "$root/config/manager"
  "$root/config/default"
  "$root/examples/kind"
  "$root/examples/fake-observability"
)

if grep -RIn --include='*.yaml' --include='*.yml' --include='Makefile' 'latest' "${release_paths[@]}"; then
  echo "release-managed paths must not contain unqualified latest image references" >&2
  exit 1
fi

default_render="$(mktemp)"
kind_render="$(mktemp)"
cleanup() {
  rm -f "$default_render" "$kind_render"
}
trap cleanup EXIT

IMAGE_REPOSITORY="$image_repository" DEMO_IMAGE_REPOSITORY="$demo_image_repository" IMAGE_TAG="$image_tag" \
  bash "$root/hack/render-release-kustomize.sh" config/default >"$default_render"
IMAGE_REPOSITORY="$image_repository" DEMO_IMAGE_REPOSITORY="$demo_image_repository" IMAGE_TAG="$image_tag" \
  bash "$root/hack/render-release-kustomize.sh" examples/kind >"$kind_render"

operator_image="$image_repository:$image_tag"
demo_image="$demo_image_repository:$image_tag"

if ! grep -Fq "image: $operator_image" "$default_render"; then
  echo "config/default does not render operator image $operator_image" >&2
  exit 1
fi

if ! grep -Fq "image: $operator_image" "$kind_render"; then
  echo "examples/kind does not render operator image $operator_image" >&2
  exit 1
fi

if ! grep -Fq "image: $demo_image" "$kind_render"; then
  echo "examples/kind does not render demo image $demo_image" >&2
  exit 1
fi

if grep -Fq ':latest' "$default_render" "$kind_render"; then
  echo "rendered release manifests must not contain :latest" >&2
  exit 1
fi

echo "packaging consistency verified"
