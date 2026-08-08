# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
This project is currently pre-1.0 (`v0.4.x-beta`); breaking changes may still
occur between beta releases. Entries before `v0.4.0-beta.1` are not
retroactively reconstructed here; see the [Roadmap](ROADMAP.md) for the
release history and track direction.

## [Unreleased]

## [v0.4.0-beta.1]

Approval lifecycle and guardrails hardening with timeout-based escalation,
approval source preservation, RemediationPlanReconciler idempotency fix,
RiskRule investigationPolicy default to CreateRequest (canonical routing),
and WebSocket notification support for escalation events.

## [v0.3.0-beta.3]

Beta hardening prerelease with canonical RCA preflight, evidence gating,
direct `RiskRule` compatibility, runtime default hardening, least-privilege
RBAC defaults, GHCR images, Helm OCI packaging, and verified provenance.
