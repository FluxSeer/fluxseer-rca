#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${script_dir}/common.sh"

log_section "Verify Notification"

wait_for_command "webhook event recorded" demo_state_has_webhook

state="$(demo_state)"
assert_contains "${state}" '"webhookEvents"' "expected webhook event payload in demo state"
assert_contains "${state}" 'RiskSignal detected' "expected notification title in demo state"
assert_contains "${state}" 'RCA Summary:' "expected RCA summary in webhook body"

echo "verified webhook payload in demo state"
