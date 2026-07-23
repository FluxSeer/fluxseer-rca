#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
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

if [[ -n "$(git -C "${root}" status --porcelain)" ]]; then
  echo "working tree must be clean for release artifact verification" >&2
  git -C "${root}" status --short >&2
  exit 1
fi

echo "release inputs verified"
