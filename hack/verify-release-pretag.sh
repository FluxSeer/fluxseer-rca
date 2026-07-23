#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
version="${VERSION:?VERSION is required}"

if [[ -z "${version}" || "${version}" == "dev" ]]; then
  echo "release VERSION must not be empty or dev" >&2
  exit 1
fi

if [[ -n "$(git -C "${root}" status --porcelain)" ]]; then
  echo "working tree must be clean before tagging" >&2
  git -C "${root}" status --short >&2
  exit 1
fi

git -C "${root}" fetch origin main --quiet

head_sha="$(git -C "${root}" rev-parse HEAD)"
origin_sha="$(git -C "${root}" rev-parse origin/main)"
if [[ "${head_sha}" != "${origin_sha}" ]]; then
  echo "HEAD must match origin/main before tagging" >&2
  echo "HEAD:        ${head_sha}" >&2
  echo "origin/main: ${origin_sha}" >&2
  exit 1
fi

if git -C "${root}" rev-parse -q --verify "refs/tags/${version}" >/dev/null; then
  echo "tag ${version} already exists locally" >&2
  exit 1
fi

if git -C "${root}" ls-remote --exit-code --tags origin "refs/tags/${version}" >/dev/null 2>&1; then
  echo "tag ${version} already exists on origin" >&2
  exit 1
fi

VERSION="${version}" bash "${root}/hack/verify-release-cleanup.sh"

echo "release pretag verified"
