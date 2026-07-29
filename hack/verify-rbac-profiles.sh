#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
chart="${root}/charts/kube-ai-sre"

tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "${tmpdir}"
}
trap cleanup EXIT INT TERM

default_render="${tmpdir}/default.yaml"
remediation_render="${tmpdir}/remediation.yaml"
experimental_render="${tmpdir}/experimental.yaml"
default_clusterrole="${tmpdir}/default-clusterrole.yaml"
remediation_clusterrole="${tmpdir}/remediation-clusterrole.yaml"
experimental_clusterrole="${tmpdir}/experimental-clusterrole.yaml"

helm template fluxagent "${chart}" --namespace fluxagent-system >"${default_render}"
helm template fluxagent "${chart}" --namespace fluxagent-system \
  --set features.remediation.enabled=true \
  --set rbac.profile=remediation >"${remediation_render}"
helm template fluxagent "${chart}" --namespace fluxagent-system \
  --set features.remediation.enabled=true \
  --set features.experimentalExecutor.enabled=true \
  --set rbac.profile=experimentalExecutor >"${experimental_render}"

extract_clusterrole() {
  local source="$1"
  local target="$2"
  awk '
    /^kind: ClusterRole$/ { in_clusterrole = 1 }
    in_clusterrole { print }
    in_clusterrole && /^---$/ { exit }
  ' "${source}" >"${target}"
}

extract_clusterrole "${default_render}" "${default_clusterrole}"
extract_clusterrole "${remediation_render}" "${remediation_clusterrole}"
extract_clusterrole "${experimental_render}" "${experimental_clusterrole}"

assert_contains() {
  local file="$1"
  local pattern="$2"
  local message="$3"
  if ! grep -Fq -- "${pattern}" "${file}"; then
    echo "missing expected RBAC fragment: ${message}" >&2
    exit 1
  fi
}

assert_not_contains() {
  local file="$1"
  local pattern="$2"
  local message="$3"
  if grep -Fq -- "${pattern}" "${file}"; then
    echo "unexpected RBAC fragment: ${message}" >&2
    exit 1
  fi
}

assert_contains "${default_render}" "--enable-legacy-deployment-risk=false" "legacy deployment watcher disabled by default"
assert_contains "${default_render}" "--enable-remediation=false" "remediation disabled by default"
assert_contains "${default_render}" "kind: Role" "namespaced provider Secret reader Role"
assert_contains "${default_render}" "name: fluxagent-provider-secret-reader" "provider Secret reader RoleBinding"

assert_not_contains "${default_clusterrole}" 'resources: ["secrets"]' "cluster-wide Secret read in default ClusterRole"
assert_not_contains "${default_clusterrole}" 'resources: ["jobs"]' "Job mutation in default ClusterRole"
assert_not_contains "${default_clusterrole}" 'resources: ["configmaps"]' "ConfigMap mutation in default ClusterRole"
assert_not_contains "${default_clusterrole}" 'resources: ["remediationplans", "agentactions"]' "remediation write permissions in default ClusterRole"
assert_not_contains "${default_clusterrole}" 'resources: ["remediationplans/status", "agentactions/status"]' "remediation status permissions in default ClusterRole"

assert_contains "${remediation_render}" "--enable-remediation=true" "remediation controller opt-in"
assert_contains "${remediation_clusterrole}" 'resources: ["remediationplans", "agentactions"]' "remediation CRD permissions"
assert_contains "${remediation_clusterrole}" 'resources: ["remediationplans/status", "agentactions/status"]' "remediation status permissions"
assert_not_contains "${remediation_clusterrole}" 'resources: ["jobs"]' "Job mutation absent from remediation-only profile"
assert_not_contains "${remediation_clusterrole}" 'resources: ["configmaps"]' "ConfigMap mutation absent from remediation-only profile"

assert_contains "${experimental_clusterrole}" 'resources: ["jobs"]' "experimental executor Job permissions"
assert_contains "${experimental_clusterrole}" 'resources: ["configmaps"]' "experimental executor ConfigMap permissions"

echo "RBAC profile verification passed"
