# Security Policy

## Reporting a Vulnerability

Please report security vulnerabilities privately using [GitHub Security Advisories](https://github.com/FluxSeer/fluxseer-rca/security/advisories/new) for this repository. Do not open a public issue for suspected vulnerabilities.

Include as much detail as you can:

- Affected version(s) or commit
- Component (operator, adapters, Helm chart, CLI, etc.)
- Steps to reproduce, and the impact if exploited
- Any relevant logs, CRD manifests, or configuration (with secrets redacted)

## Scope

This policy covers the FluxSeer RCA operator, CLI, Helm chart, and CRDs in this repository. It does not cover the security of infrastructure a user chooses to run the operator on, or of third-party model providers (OpenAI, Claude, Gemini) it integrates with.

## Supported Versions

FluxSeer RCA is currently pre-1.0 (`v0.3.x-beta`). Security fixes are made against the `main` branch and the most recent published release; older betas are not separately patched.

## Response

We aim to acknowledge new reports within a few business days and to keep reporters updated as a fix is developed. Coordinated disclosure timing is worked out with the reporter based on severity and complexity of the fix.
