#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="${root}/test/promql/request_rate_surge.test.yml"
image="${FLUXSEER_RCA_PROMTOOL_IMAGE:-prom/prometheus:v3.5.0}"

if command -v promtool >/dev/null 2>&1; then
  promtool test rules "${fixture}"
elif command -v docker >/dev/null 2>&1; then
  docker run --rm --entrypoint promtool \
    -v "${fixture}:/workspace/request_rate_surge.test.yml:ro" \
    "${image}" \
    test rules /workspace/request_rate_surge.test.yml
else
  echo "promtool or docker is required for traffic-pattern PromQL verification" >&2
  exit 1
fi

echo "request-rate-surge PromQL conformance passed: 5 cases"
