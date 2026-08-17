# FluxAgent &rarr; FluxSeer RCA Rename Migration Plan

This was originally planned as a paced, multi-release effort. Phases 0 and 1 ended up landing together in one session at explicit user request, once it became clear only the requester's own test cluster runs this product today (no external installs, dashboards, or replay archives depend on the old identifiers). Phase 2 remains deliberately out of scope. This doc is now a record of what changed, not a forward plan.

## Current State Inventory (as of completion)

| Name | Status |
| --- | --- |
| `kube-ai-sre` | Gone. Was a stale Helm chart source-directory name predating even the `fluxagent` brand. |
| `fluxagent` | Gone from source, build artifacts, Kubernetes resource naming, and current-state docs. Still accurate, and intentionally left alone, in historical/dated docs (`docs/releases/*`, `docs/release-checkpoint.md`, `docs/backlog/v0.2-beta.md`, `docs/backlog/v0.2-release-reproducibility.md`, `docs/architecture/v0.2-read-only-rca.md`, `docs/architecture/v0.2-adapter-neutral-backlog.md`, `docs/backlog/archived-decisions.md`, `docs/backlog/v0.3-product-rename.md` — the original, superseded rename proposal). |
| `fluxseer` | Go module path, CLI binary (`cmd/fluxseer`), internal package identity, OCI label domain (`io.fluxseer.git-dirty`), and internal-only schema/digest version strings whose own suffix already contained `-rca-` (to avoid a doubled `fluxseer-rca-rca-...`). |
| `fluxseer-rca` | Everything that identifies *this specific product* in a namespace future FluxSeer products would also need to share without colliding: Helm chart name and OCI publish path, container image repository path, Kubernetes namespace convention (`fluxseer-rca-system`, `fluxseer-rca-demo`), K8s resource names, annotation/label key domain (`fluxseer-rca.aiops.platform/...`), Prometheus metric names (`fluxseer_rca_*`, underscored — Prometheus names can't contain hyphens), and most schema/digest version strings. |
| `aiops.platform` | Unchanged. Not brand-coupled — a Kubernetes API group name, conventionally neutral like `apps/v1`. Not part of this rename; see Phase 2 note below. |

## Naming decision this plan settled on

Explicit user direction: don't treat "org = product." `FluxSeer` is the umbrella brand; `FluxSeer RCA` is currently its only product but won't stay that way. Surfaces where a future sibling product would need to coexist without colliding use the product-scoped `fluxseer-rca` token (matching the actual GitHub repo name, not an invented one). Surfaces with no realistic cross-product collision risk (the Go module — nothing else lives in this repo; the CLI binary and its own usage text; internal digest/schema version constants scoped to this codebase) kept the shorter bare `fluxseer`.

## What was done

**Phase 0 (chart directory rename)** — `charts/kube-ai-sre/` &rarr; `charts/fluxagent/` (intermediate step, later renamed again below), and every reference across CI, `hack/*.sh`, `test/e2e/kind/*.sh`, and `docs/architecture/mermaid-diagrams.md`. `hack/verify-v0.3-schema-freeze.sh`'s frozen-baseline diff against the `v0.3.0-beta.1` tag had to drop the chart-path pathspec (comparing a renamed path against an old tag that never had that path produces a spurious full-file diff); the script's own CRD source/chart consistency check plus the `config/crd/bases` tag-diff next to it still cover the same drift. That script's frozen-baseline gate independently fails against a real, pre-existing schema drift from 8 commits that landed before this rename work started — confirmed via `git log` before touching anything, unrelated to this rename.

**Phase 1a (Go module identity)** — `module fluxagent` &rarr; `module fluxseer`, all ~88 files' import paths, `cmd/fluxagent` &rarr; `cmd/fluxseer`, `Makefile` (`APP`, `VERSION_PACKAGE`), and every `-X fluxagent/internal/version...` linker flag across both Dockerfiles and `hack/verify-build-reproducibility.sh` (these set variables by fully-qualified package path and silently no-op, not error, if left stale).

**Phase 1b (brand strings in Go source)** — Prometheus metric names, Kubernetes annotation/label key prefixes (`internal/controllers/helpers.go`, `internal/detector/service.go`), the leader-election ID, and digest/schema canonicalization identifiers (`internal/canonicaldigest`, `internal/evidence`, `internal/solution`, `internal/replay`, `internal/controllers/riskrule_controller.go`, `internal/controllers/investigationrequest_controller.go`, `api/v1alpha1/types.go`). Two golden test fixtures under `internal/investigation/testdata/normalization/` were regenerated via `UPDATE_GOLDEN=1` rather than hand-edited, since their `contentDigest` values are computed from the renamed canonicalization strings, not literal text.

**Phase 1c (Helm chart, config, examples, CI)** — Chart directory renamed again, `charts/fluxagent/` &rarr; `charts/fluxseer-rca/` (superseding the Phase 0 intermediate name once the product-scoping decision was made). `_helpers.tpl` definitions and every call site, resource names, RBAC, service account, rule pack names, Grafana dashboard and PrometheusRule metric references (updated to match the renamed `fluxseer_rca_*` metric names), `values.yaml` image repository, both Dockerfiles' binary output name and OCI labels, `config/` Kustomize manifests, `examples/`, `hack/*.sh`, `test/e2e/kind/*.sh`, `Makefile`, `ci.yml`, `release.yml`.

**Phase 1d (docs)** — Current/maintained docs updated to the new naming. Historical and dated docs (release notes, freeze reports, superseded proposals, archived-decisions) deliberately left alone — they're point-in-time records. The v0.4 release now uses the `fluxseer` / `fluxseer-rca` identity; references to the v0.3 `fluxagent` artifacts are retained only where they describe historical compatibility.

Verified throughout: `go build ./...`, `go vet ./...`, `gofmt -l` (clean), `go mod verify`, full `go test ./...` (30 packages, 0 failures), `helm lint` + full `helm template` render (zero `fluxagent` in rendered output), `kubectl kustomize` for `config/default` and `examples/kind`, and the runnable `hack/verify-*.sh` scripts.

## Phase 2 — CRD API group rename (still not planned)

README already flags that this needs "an explicit migration path" if it ever happens. Treat it as a separate decision requiring its own design doc — dual-serving old and new API groups (or a conversion path) during transition, since every existing installed custom resource is tied to `aiops.platform/v1alpha1`, and breaking that is far more disruptive than the source/artifact rename above. Default position: don't do this unless there's a concrete reason to.
