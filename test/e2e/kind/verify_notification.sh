#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${script_dir}/common.sh"

log_section "Verify Notification"

wait_for_command "webhook event recorded" demo_state_has_webhook

wait_for_command "webhook payload contains event list" demo_state_has_event_list

wait_for_command "webhook payload contains notification title" demo_state_has_notification_title

wait_for_command "webhook payload contains RCA summary" demo_state_has_rca_summary

echo "verified webhook payload in demo state"
