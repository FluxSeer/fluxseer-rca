#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
chart="$root/charts/fluxseer-rca"

default_render="$(mktemp)"
all_render="$(mktemp)"
scoped_render="$(mktemp)"
scoped_values="$(mktemp)"
multikind_render="$(mktemp)"
multikind_values="$(mktemp)"
cleanup() {
  rm -f "$default_render" "$all_render" "$scoped_render" "$scoped_values" "$multikind_render" "$multikind_values"
}
trap cleanup EXIT

helm template fluxseer-rca "$chart" --namespace fluxseer-rca-system >"$default_render"
helm template fluxseer-rca "$chart" --namespace fluxseer-rca-system \
  --set rulePacks.prometheusBaseline.enabled=true \
  --set rulePacks.lokiBaseline.enabled=true >"$all_render"
cat >"$scoped_values" <<'VALUES'
rulePacks:
  defaultTargetSelector:
    namespaceSelector:
      matchNames: []
VALUES
helm template fluxseer-rca "$chart" --namespace fluxseer-rca-system -f "$scoped_values" >"$scoped_render"
cat >"$multikind_values" <<'VALUES'
rulePacks:
  defaultTargetSelector:
    namespaceSelector:
      matchNames:
        - prod
    workloadSelector:
      kinds:
        - Deployment
        - StatefulSet
        - DaemonSet
        - Job
        - CronJob
VALUES
helm template fluxseer-rca "$chart" --namespace fluxseer-rca-system -f "$multikind_values" >"$multikind_render"

require_contains() {
  file="$1"
  pattern="$2"
  if ! grep -Fq -- "$pattern" "$file"; then
    echo "expected render to contain: $pattern" >&2
    exit 1
  fi
}

require_not_contains() {
  file="$1"
  pattern="$2"
  if grep -Fq -- "$pattern" "$file"; then
    echo "expected render not to contain: $pattern" >&2
    exit 1
  fi
}

require_contains "$default_render" "name: fluxseer-rca-kubernetes-baseline"
require_contains "$default_render" "aiops.platform/rule-pack: kubernetes-baseline"
require_contains "$default_render" "name: crashloop-backoff"
require_contains "$default_render" "name: image-pull-failure"
require_contains "$default_render" "name: failed-scheduling"
require_contains "$default_render" "name: oom-killed"
require_contains "$default_render" "name: unhealthy-probe"
require_contains "$default_render" "name: deployment-unavailable"
require_contains "$default_render" '- "fluxseer-rca-system"'
require_not_contains "$default_render" "name: fluxseer-rca-prometheus-baseline"
require_not_contains "$default_render" "name: fluxseer-rca-loki-baseline"

require_contains "$all_render" "name: fluxseer-rca-prometheus-baseline"
require_contains "$all_render" "aiops.platform/rule-pack: prometheus-baseline"
require_contains "$all_render" "name: high-error-rate"
require_contains "$all_render" "name: high-latency"
require_contains "$all_render" "name: pod-restart-rate"
require_contains "$all_render" "name: cpu-saturation"
require_contains "$all_render" "name: memory-saturation"
require_contains "$all_render" "name: fluxseer-rca-loki-baseline"
require_contains "$all_render" "aiops.platform/rule-pack: loki-baseline"
require_contains "$all_render" "name: panic"
require_contains "$all_render" "name: fatal"
require_contains "$all_render" "name: exception"
require_contains "$all_render" "name: timeout"
require_contains "$all_render" "name: connection-refused"
require_contains "$all_render" '{{ .namespace }}'
require_contains "$all_render" '{{ .app }}'
require_contains "$all_render" '{{ .name }}'

if grep -F "kind: DataSource" "$all_render"; then
  echo "rule packs must not install external DataSource resources" >&2
  exit 1
fi

if grep -F "kind: ModelProvider" "$all_render"; then
  echo "rule packs must not install hosted ModelProvider resources" >&2
  exit 1
fi

require_contains "$scoped_render" "matchNames:"
require_contains "$scoped_render" "matchNames: []"
require_not_contains "$scoped_render" '- "fluxseer-rca-system"'

require_contains "$multikind_render" "- prod"
require_contains "$multikind_render" "- Deployment"
require_contains "$multikind_render" "- StatefulSet"
require_contains "$multikind_render" "- DaemonSet"
require_contains "$multikind_render" "- Job"
require_contains "$multikind_render" "- CronJob"

echo "rule pack rendering verified"
