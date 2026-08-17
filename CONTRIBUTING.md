# Contributing

FluxSeer RCA is designed as an open, adapter-driven Kubernetes operator. Contributions should preserve that direction.

Participation in this project is governed by the [Code of Conduct](CODE_OF_CONDUCT.md). Please report security vulnerabilities per [SECURITY.md](SECURITY.md) rather than as public issues.

## Principles

- Keep the core controller loop provider-neutral.
- Add new observability systems behind `internal/datasource` interfaces.
- Add new model providers behind `internal/model` interfaces.
- Prefer read-only and GitOps-first flows over direct mutation.
- Every executable action must remain guardrailed and auditable.
- Preserve the distinction between detection, evidence sufficiency, claim
  verification, and the final verdict/outcome.
- Detection success must not imply RCA confirmation.
- An RCA verdict must not be more specific than the strongest
  evidence-supported causal claim.

Use the terminology in the [product and API glossary](docs/glossary.md) when
adding detection patterns, evidence profiles, status fields, tests, or product
documentation. Application-specific parameterized queries are signal
templates; do not count them as new built-in detection patterns unless the
project ships and maintains the detector as an official rule-pack default.

## Development

```bash
cd fluxseer-rca
GOWORK=off go test ./...
GOWORK=off go run ./cmd/fluxseer
GOWORK=off go run ./cmd/manager
```

Run the linter before opening a pull request:

```bash
make lint
```

This runs `golangci-lint` with the repository's `.golangci.yml` config. CI runs the same check on every pull request.

## Developer Certificate of Origin

This project does not require a separate CLA. By submitting a pull request, you certify that you wrote the contribution yourself, or otherwise have the right to submit it under the project's [Apache License, Version 2.0](LICENSE), per the [Developer Certificate of Origin](https://developercertificate.org/).

## Branching

- Fork the repository and open pull requests against `main`. `main` is the only branch external contributors should target — it is where CI runs on GitHub-hosted runners and where releases are tagged from.
- `test` is an internal pre-release validation branch tied to our own publishing pipeline. It is not a pull request target; you don't need to interact with it.
- Branches such as `feat/*`, `fix/*`, `docs/*`, and `ci/*` on the upstream repo are maintainer working branches, not shared integration branches. Don't base your work on them or expect them to stay stable.

## Pull Requests

- Include tests for new controllers, adapters, or policies.
- Document any new CRD fields or config surface.
- Do not hard-code vendor-specific assumptions into shared orchestration paths.
