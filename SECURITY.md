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

## Runtime Security Boundary

The default Helm installation is read-only with respect to user workloads. It
uses read-only workload access (`get`, `list`, and `watch`) and writes only
FluxSeer RCA-owned resources and statuses.

The current experimental mutation path requires both:

```yaml
features:
  remediation:
    enabled: true
  experimentalExecutor:
    enabled: true
```

That path is limited to an allowlisted Kubernetes Deployment
`kubernetes.rolloutRestart` action. It validates the target and execution
identity, prevents duplicate execution, and performs bounded post-action
verification. Generic patch/apply/delete/exec, shell/SSH, GitOps, Runbook, and
autonomous mutation are not supported backends.

Model-provider output never directly calls the Kubernetes API. The guarded
sequence is:

```text
provider output -> RemediationPlan -> policy/approval -> executor validation
-> allowlisted mutation -> audit and verification
```

The experimental path is not recommended for unattended production use. Its
effectiveness classification describes only the configured observation window;
`Effective` is not a permanent guarantee that the underlying incident cause is
resolved.

Provider egress is opt-in. Evidence is classified and redacted before hosted
provider calls, and raw secrets, authorization headers, prompts, raw provider
responses, and unsupported raw snapshots are not stored by the default
contract. Users should review provider terms, network egress, and cluster RBAC
before enabling hosted providers or experimental remediation.

## Supported Versions

FluxSeer RCA is currently pre-1.0. The most recent published release is
`v0.4.0-beta.3`; security fixes target the default branch and the most recent
published release. The unreleased `v0.5-alpha.1` development slice is
experimental and is not a production-support commitment.

## Response

We aim to acknowledge new reports within a few business days and to keep reporters updated as a fix is developed. Coordinated disclosure timing is worked out with the reporter based on severity and complexity of the fix.
