# Contributing

FluxAgent is designed as an open, adapter-driven Kubernetes operator. Contributions should preserve that direction.

## Principles

- Keep the core controller loop provider-neutral.
- Add new observability systems behind `internal/datasource` interfaces.
- Add new model providers behind `internal/model` interfaces.
- Prefer read-only and GitOps-first flows over direct mutation.
- Every executable action must remain guardrailed and auditable.

## Development

```bash
cd FluxAgent
GOWORK=off go test ./...
GOWORK=off go run ./cmd/fluxagent
GOWORK=off go run ./cmd/manager
```

## Pull Requests

- Include tests for new controllers, adapters, or policies.
- Document any new CRD fields or config surface.
- Do not hard-code vendor-specific assumptions into shared orchestration paths.
