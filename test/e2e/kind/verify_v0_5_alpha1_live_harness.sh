#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${script_dir}/live_qualification_harness.sh"

live_harness_run

echo "verify-v0.5-alpha1-live-harness passed"
