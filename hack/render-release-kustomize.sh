#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: IMAGE_REPOSITORY=... DEMO_IMAGE_REPOSITORY=... IMAGE_TAG=... bash hack/render-release-kustomize.sh <kustomization-dir>" >&2
  exit 2
fi

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
target="$1"
target_abs="$(cd "$root/$target" && pwd)"
target_rel="${target_abs#$root/}"

image_repository="${IMAGE_REPOSITORY:-fluxagent/operator}"
demo_image_repository="${DEMO_IMAGE_REPOSITORY:-fluxagent/demo-observability}"
image_tag="${IMAGE_TAG:-v0.2.0-beta.1}"

tmp="$(mktemp -d "$root/.tmp-kustomize.XXXXXX")"
cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT

cat >"$tmp/kustomization.yaml" <<EOF
resources:
  - ../$target_rel
images:
  - name: fluxagent/operator
    newName: $image_repository
    newTag: $image_tag
  - name: fluxagent/demo-observability
    newName: $demo_image_repository
    newTag: $image_tag
EOF

kubectl kustomize "$tmp"
