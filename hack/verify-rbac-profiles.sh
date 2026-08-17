#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
chart="${root}/charts/fluxseer-rca"

tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "${tmpdir}"
}
trap cleanup EXIT INT TERM

default_render="${tmpdir}/default.yaml"
legacy_render="${tmpdir}/legacy.yaml"
remediation_render="${tmpdir}/remediation.yaml"
policy_render="${tmpdir}/policy.yaml"
experimental_render="${tmpdir}/experimental.yaml"
invalid_experimental_render="${tmpdir}/invalid-experimental.yaml"
invalid_policy_render="${tmpdir}/invalid-policy.yaml"
default_clusterrole="${tmpdir}/default-clusterrole.yaml"
legacy_clusterrole="${tmpdir}/legacy-clusterrole.yaml"
remediation_clusterrole="${tmpdir}/remediation-clusterrole.yaml"
policy_clusterrole="${tmpdir}/policy-clusterrole.yaml"
experimental_clusterrole="${tmpdir}/experimental-clusterrole.yaml"

helm template fluxseer-rca "${chart}" --namespace fluxseer-rca-system >"${default_render}"
helm template fluxseer-rca "${chart}" --namespace fluxseer-rca-system \
  --set features.legacyDeploymentRisk.enabled=true >"${legacy_render}"
helm template fluxseer-rca "${chart}" --namespace fluxseer-rca-system \
  --set features.remediation.enabled=true \
  --set rbac.profile=remediation >"${remediation_render}"
helm template fluxseer-rca "${chart}" --namespace fluxseer-rca-system \
  --set features.remediation.enabled=true \
  --set features.policyPack.enabled=true >"${policy_render}"
helm template fluxseer-rca "${chart}" --namespace fluxseer-rca-system \
  --set features.remediation.enabled=true \
  --set features.experimentalExecutor.enabled=true \
  --set rbac.profile=experimentalExecutor >"${experimental_render}"

if helm template fluxseer-rca "${chart}" --namespace fluxseer-rca-system \
  --set features.experimentalExecutor.enabled=true \
  --set rbac.profile=experimentalExecutor >"${invalid_experimental_render}" 2>&1; then
  echo "expected experimentalExecutor profile to fail without remediation enabled" >&2
  exit 1
fi
if helm template fluxseer-rca "${chart}" --namespace fluxseer-rca-system \
  --set features.policyPack.enabled=true >"${invalid_policy_render}" 2>&1; then
  echo "expected policy pack to fail without remediation enabled" >&2
  exit 1
fi
assert_invalid_experimental_message() {
  if ! grep -Fq "requires features.remediation.enabled=true" "${invalid_experimental_render}"; then
    echo "invalid experimentalExecutor profile failed with unexpected message:" >&2
    cat "${invalid_experimental_render}" >&2
    exit 1
  fi
}
assert_invalid_policy_message() {
  if ! grep -Fq "policy pack requires features.remediation.enabled=true" "${invalid_policy_render}"; then
    echo "policy pack failed with unexpected message:" >&2
    cat "${invalid_policy_render}" >&2
    exit 1
  fi
}

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
extract_clusterrole "${legacy_render}" "${legacy_clusterrole}"
extract_clusterrole "${remediation_render}" "${remediation_clusterrole}"
extract_clusterrole "${policy_render}" "${policy_clusterrole}"
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
assert_contains "${default_render}" "--enable-policy-pack=false" "policy pack disabled by default"
assert_contains "${default_render}" "kind: Role" "namespaced provider Secret reader Role"
assert_contains "${default_render}" "name: fluxseer-rca-provider-secret-reader" "provider Secret reader RoleBinding"

assert_not_contains "${default_clusterrole}" 'resources: ["secrets"]' "cluster-wide Secret read in default ClusterRole"
assert_not_contains "${default_clusterrole}" 'resources: ["jobs"]' "Job mutation in default ClusterRole"
assert_not_contains "${default_clusterrole}" 'resources: ["configmaps"]' "ConfigMap mutation in default ClusterRole"
assert_not_contains "${default_clusterrole}" 'resources: ["remediationplans", "agentactions"]' "remediation write permissions in default ClusterRole"
assert_not_contains "${default_clusterrole}" 'resources: ["remediationplans/status", "agentactions/status"]' "remediation status permissions in default ClusterRole"
assert_not_contains "${default_clusterrole}" 'resources: ["approvalpolicies", "namespacethresholds", "escalationchains"]' "policy pack read permissions in default ClusterRole"

assert_contains "${policy_render}" "--enable-policy-pack=true" "policy pack opt-in"
assert_contains "${policy_render}" "--enable-remediation=true" "policy pack requires remediation"
assert_contains "${policy_clusterrole}" 'resources: ["approvalpolicies", "namespacethresholds", "escalationchains"]' "policy pack read permissions"

assert_contains "${legacy_render}" "--enable-legacy-deployment-risk=true" "legacy deployment watcher opt-in"
assert_contains "${legacy_render}" "--enable-remediation=false" "remediation remains disabled for legacy-only opt-in"
assert_contains "${legacy_clusterrole}" 'resources: ["deployments", "statefulsets", "daemonsets", "replicasets"]' "legacy profile has workload read permissions"
assert_not_contains "${legacy_clusterrole}" 'resources: ["remediationplans", "agentactions"]' "legacy profile does not add remediation writes"
assert_not_contains "${legacy_clusterrole}" 'resources: ["jobs"]' "legacy profile does not add Job mutation"
assert_not_contains "${legacy_clusterrole}" 'resources: ["configmaps"]' "legacy profile does not add ConfigMap mutation"

assert_contains "${remediation_render}" "--enable-remediation=true" "remediation controller opt-in"
assert_contains "${remediation_clusterrole}" 'resources: ["remediationplans", "agentactions"]' "remediation CRD permissions"
assert_contains "${remediation_clusterrole}" 'resources: ["remediationplans/status", "agentactions/status"]' "remediation status permissions"
assert_not_contains "${remediation_clusterrole}" 'resources: ["jobs"]' "Job mutation absent from remediation-only profile"
assert_not_contains "${remediation_clusterrole}" 'resources: ["configmaps"]' "ConfigMap mutation absent from remediation-only profile"

assert_contains "${experimental_clusterrole}" 'resources: ["jobs"]' "experimental executor Job permissions"
assert_contains "${experimental_clusterrole}" 'resources: ["configmaps"]' "experimental executor ConfigMap permissions"
assert_invalid_experimental_message
assert_invalid_policy_message

echo "RBAC profile verification passed"
