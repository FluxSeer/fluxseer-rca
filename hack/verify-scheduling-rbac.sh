#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
command -v helm >/dev/null 2>&1 || {
  echo "required command not found: helm" >&2
  exit 1
}

rendered="$(helm template fluxseer-rca "${root}/charts/fluxseer-rca" --namespace fluxseer-system)"
events_rule="$(printf '%s\n' "${rendered}" | grep -F -A2 'apiGroups: ["events.k8s.io"]' || true)"

grep -Fq 'resources: ["events"]' <<<"${events_rule}" || {
  echo "rendered ClusterRole is missing read-only events.k8s.io/events access" >&2
  exit 1
}
grep -Fq 'verbs: ["get", "list", "watch"]' <<<"${events_rule}" || {
  echo "rendered events.k8s.io/events rule is not read-only get/list/watch" >&2
  exit 1
}
if grep -Eq 'verbs: \[[^]]*(create|update|patch|delete)' <<<"${events_rule}"; then
  echo "rendered events.k8s.io/events rule contains a mutating verb" >&2
  exit 1
fi

echo "scheduling RBAC render contract passed"
