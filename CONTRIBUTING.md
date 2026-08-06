# Contributing

FluxSeer RCA is designed as an open, adapter-driven Kubernetes operator. Contributions should preserve that direction.

## Principles

- Keep the core controller loop provider-neutral.
- Add new observability systems behind `internal/datasource` interfaces.
- Add new model providers behind `internal/model` interfaces.
- Prefer read-only and GitOps-first flows over direct mutation.
- Every executable action must remain guardrailed and auditable.

## Development

```bash
cd fluxseer-rca
GOWORK=off go test ./...
GOWORK=off go run ./cmd/fluxseer
GOWORK=off go run ./cmd/manager
```

## Branching

- Fork the repository and open pull requests against `main`. `main` is the only branch external contributors should target — it is where CI runs on GitHub-hosted runners and where releases are tagged from.
- `test` is an internal pre-release validation branch tied to our own publishing pipeline. It is not a pull request target; you don't need to interact with it.
- Branches such as `feat/*`, `fix/*`, `docs/*`, and `ci/*` on the upstream repo are maintainer working branches, not shared integration branches. Don't base your work on them or expect them to stay stable.

## Pull Requests

- Include tests for new controllers, adapters, or policies.
- Document any new CRD fields or config surface.
- Do not hard-code vendor-specific assumptions into shared orchestration paths.
