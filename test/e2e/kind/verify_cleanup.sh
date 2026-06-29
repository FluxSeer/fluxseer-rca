#!/usr/bin/env bash
set -euo pipefail

cluster_name="${FLUXAGENT_CLUSTER_NAME:-fluxagent-demo}"

if kind get clusters | grep -qx "${cluster_name}"; then
  echo "expected kind cluster ${cluster_name} to be cleaned up" >&2
  exit 1
fi

echo "verified cleanup for cluster ${cluster_name}"
