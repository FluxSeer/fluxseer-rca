#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

version="${VERSION:?VERSION is required}"
git_commit="${GIT_COMMIT:?GIT_COMMIT is required}"
git_dirty="${GIT_DIRTY:?GIT_DIRTY is required}"
build_date="${BUILD_DATE:?BUILD_DATE is required}"
source_date_epoch="${SOURCE_DATE_EPOCH:-$(git -C "$root" show -s --format=%ct HEAD)}"
image_repository="${IMAGE_REPOSITORY:-fluxagent/operator}"
demo_image_repository="${DEMO_IMAGE_REPOSITORY:-fluxagent/demo-observability}"
image_tag="${IMAGE_TAG:-$version}"
target_platform="${TARGET_PLATFORM:-linux/amd64}"

if [[ "$target_platform" != */* ]]; then
  echo "TARGET_PLATFORM must be formatted as os/arch, got $target_platform" >&2
  exit 1
fi

target_os="${target_platform%%/*}"
target_arch="${target_platform#*/}"

tmp="$(mktemp -d)"
operator_a="${image_repository}:${image_tag}-operator-repro-a"
operator_b="${image_repository}:${image_tag}-operator-repro-b"
demo_a="${demo_image_repository}:${image_tag}-demo-repro-a"
demo_b="${demo_image_repository}:${image_tag}-demo-repro-b"

cleanup() {
  docker image rm "$operator_a" "$operator_b" "$demo_a" "$demo_b" >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

ldflags="-X fluxseer/internal/version.Version=${version} -X fluxseer/internal/version.GitCommit=${git_commit} -X fluxseer/internal/version.GitDirty=${git_dirty} -X fluxseer/internal/version.BuildDate=${build_date}"

build_binary() {
  local pkg="$1"
  local out="$2"
  local cache="$3"

  cd "$root"
  GOWORK=off GOCACHE="$cache" CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" \
    go build -trimpath -buildvcs=false -ldflags "$ldflags" -o "$out" "$pkg"
}

verify_binary() {
  local name="$1"
  local pkg="$2"

  local out_a="$tmp/${name}-a"
  local out_b="$tmp/${name}-b"
  build_binary "$pkg" "$out_a" "$tmp/gocache-${name}-a"
  build_binary "$pkg" "$out_b" "$tmp/gocache-${name}-b"

  local digest_a
  local digest_b
  digest_a="$(sha256_file "$out_a")"
  digest_b="$(sha256_file "$out_b")"

  if [[ "$digest_a" != "$digest_b" ]]; then
    echo "$name binary digest differs" >&2
    echo "  first:  $digest_a" >&2
    echo "  second: $digest_b" >&2
    exit 1
  fi

  echo "$name binary digest: $digest_a"
}

image_id() {
  docker image inspect "$1" --format '{{.Id}}'
}

rootfs_layers() {
  docker image inspect "$1" --format '{{json .RootFS.Layers}}'
}

verify_image() {
  local name="$1"
  local dockerfile="$2"
  local tag_a="$3"
  local tag_b="$4"

  docker buildx build --no-cache --load \
    --platform "$target_platform" \
    --provenance=false \
    --sbom=false \
    --build-arg VERSION="$version" \
    --build-arg GIT_COMMIT="$git_commit" \
    --build-arg GIT_DIRTY="$git_dirty" \
    --build-arg BUILD_DATE="$build_date" \
    --build-arg SOURCE_DATE_EPOCH="$source_date_epoch" \
    -t "$tag_a" \
    -f "$dockerfile" "$root"

  docker buildx build --no-cache --load \
    --platform "$target_platform" \
    --provenance=false \
    --sbom=false \
    --build-arg VERSION="$version" \
    --build-arg GIT_COMMIT="$git_commit" \
    --build-arg GIT_DIRTY="$git_dirty" \
    --build-arg BUILD_DATE="$build_date" \
    --build-arg SOURCE_DATE_EPOCH="$source_date_epoch" \
    -t "$tag_b" \
    -f "$dockerfile" "$root"

  local id_a
  local id_b
  local layers_a
  local layers_b
  id_a="$(image_id "$tag_a")"
  id_b="$(image_id "$tag_b")"
  layers_a="$(rootfs_layers "$tag_a")"
  layers_b="$(rootfs_layers "$tag_b")"

  if [[ "$layers_a" != "$layers_b" ]]; then
    echo "$name image filesystem digest differs" >&2
    echo "  first layers:  $layers_a" >&2
    echo "  second layers: $layers_b" >&2
    exit 1
  fi

  if [[ "$id_a" != "$id_b" ]]; then
    echo "$name image config digest differs" >&2
    echo "  first image id:  $id_a" >&2
    echo "  second image id: $id_b" >&2
    docker image inspect "$tag_a" "$tag_b" >"$tmp/${name}-inspect.json"
    echo "  inspect output: $tmp/${name}-inspect.json" >&2
    exit 1
  fi

  echo "$name image digest: $id_a"
}

verify_binary "operator" ./cmd/operator
verify_binary "demo-observability" ./cmd/demo-observability
verify_image "operator" "$root/Dockerfile" "$operator_a" "$operator_b"
verify_image "demo-observability" "$root/examples/fake-observability/Dockerfile" "$demo_a" "$demo_b"

echo "build reproducibility verified"
