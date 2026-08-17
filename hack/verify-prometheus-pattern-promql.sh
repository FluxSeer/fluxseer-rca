#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="${root}/test/promql/prometheus_baseline_patterns.test.yml"
image="${FLUXSEER_RCA_PROMTOOL_IMAGE:-prom/prometheus:v3.5.0}"

if command -v promtool >/dev/null 2>&1; then
  promtool test rules "${fixture}"
elif command -v docker >/dev/null 2>&1; then
  docker run --rm --entrypoint promtool \
    -v "${fixture}:/workspace/prometheus_baseline_patterns.test.yml:ro" \
    "${image}" \
    test rules /workspace/prometheus_baseline_patterns.test.yml
else
  echo "promtool or docker is required for Prometheus pattern verification" >&2
  exit 1
fi

echo "Prometheus baseline pattern PromQL conformance passed: high-error-rate and high-latency"
