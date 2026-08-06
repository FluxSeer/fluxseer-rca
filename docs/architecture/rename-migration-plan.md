# FluxAgent &rarr; FluxSeer RCA Rename Migration Plan

This is a paced plan, not a deadline. Nothing here blocks current `v0.3` work, and no phase starts until the phase before it is done. The trigger for Phase 1 is "the next release we already intend to make breaking changes in," not a calendar date.

## Current State Inventory

The naming surface is actually three names deep, not two:

| Name | Where it still lives | Scope |
| --- | --- | --- |
| `kube-ai-sre` | Helm chart **source directory** only: `charts/kube-ai-sre/` | Stale even relative to the old `fluxagent` brand — the chart's own `Chart.yaml` `name:` field and every publish path already say `fluxagent`. Nothing external depends on the directory name. |
| `fluxagent` | Go module path (`module fluxagent`, referenced by ~88 files' import paths), `cmd/fluxagent` / `cmd/manager` / `cmd/operator` / `cmd/demo-observability` binaries, Helm chart artifact name and OCI publish path (`ghcr.io/fluxseer/fluxagent/charts/fluxagent`), container image repo (`fluxagent/operator`), default namespace convention (`fluxagent-system`, `fluxagent-demo` — ~38 files), CI runner labels (`fluxagent-prod`/`fluxagent-test`) | This is the real compatibility surface. Renaming any of these is a breaking change for existing installs. |
| `FluxSeer RCA` / `FluxSeer` | Product name, README, positioning docs, GitHub org (`FluxSeer`) | Current external-facing identity. Already consistent in prose. |
| `aiops.platform` | CRD API group | Not brand-coupled at all — this is a Kubernetes API group name, conventionally neutral (like `apps/v1`). Not part of this plan; see Phase 2 note. |

## Principle

Don't break the frozen `v0.3` contract mid-beta. README already commits to this explicitly: *"Any future API-group or resource rename requires an explicit migration path; it is not treated as a cosmetic change after the v0.3 schema freeze."* This plan doesn't relitigate that — it only covers the source-code and artifact naming surface, which isn't frozen the same way.

## Phase 0 — Now, zero external risk

Can happen any time, independent of everything else:

1. **Done.** Renamed the Helm chart source directory `charts/kube-ai-sre/` &rarr; `charts/fluxagent/`, and every reference to it across CI (`ci.yml`, `release.yml`), `hack/*.sh`, `test/e2e/kind/*.sh`, and the maintained architecture doc (`docs/architecture/mermaid-diagrams.md`). The published chart name, OCI path, and `Chart.yaml` `name:` field were already `fluxagent` and didn't change, so no existing install is affected. One follow-on fix was needed: `hack/verify-v0.3-schema-freeze.sh` diffed the chart's CRDs against the frozen `v0.3.0-beta.1` tag by path, which broke once the path changed — the chart-path pathspec was dropped from that diff since the script's own CRD source/chart consistency check, combined with the `config/crd/bases` tag-diff right next to it, already covers the same drift transitively. Historical/dated docs (`docs/releases/*`, `docs/backlog/v0.2-release-reproducibility.md`, `docs/backlog/v0.3-beta.3-pr-summary.md`, `docs/release-checkpoint.md`) were deliberately left referencing the old path — they're point-in-time records, not current state. Verified via `helm lint`, `helm template`, `hack/verify-rule-packs.sh`, `hack/verify-rbac-profiles.sh`, `hack/verify-packaging-consistency.sh`, and `go test ./...`, all green. (Separately, `hack/verify-v0.3-schema-freeze.sh`'s frozen-baseline gate currently fails against the `v0.3.0-beta.1` tag — confirmed pre-existing, from 8 commits that landed before this rename; unrelated to it and out of scope for this doc.)
2. Going forward, new prose should say "FluxSeer RCA" / "FluxSeer" for the product and only say "fluxagent" when specifically naming a compatibility surface (a binary, a namespace, an image path) — this is already mostly true; treat it as a writing-convention check, not a rewrite.

## Phase 1 — Next intentional breaking-change release

Chosen target name: **`fluxseer`**. Bundle every breaking rename into **one** version bump rather than trickling them out across releases (each one individually forces existing installs to update references; spreading them out just multiplies the number of times operators have to do that). Started ahead of an actual breaking-change release window, at explicit user request; landing as separate reviewable commits rather than one PR since the sub-parts have independent risk profiles.

- **Done.** Go module path `fluxagent` &rarr; `fluxseer`, all ~88 files' import paths, `cmd/fluxagent` &rarr; `cmd/fluxseer`, `Makefile` (`APP`, `VERSION_PACKAGE`), and every `-X fluxagent/internal/version...` linker flag (`Dockerfile`, `examples/fake-observability/Dockerfile`, `hack/verify-build-reproducibility.sh`) — those flags set variables by fully-qualified package path, so they silently stop working (not error) if left stale, easy to miss. Also updated the two doc files with a real `go run ./cmd/fluxagent` command (`CONTRIBUTING.md`, `docs/tutorials/investigate-workload.md`). Verified: `go build ./...`, `go vet ./...`, `gofmt -l` (clean), `go mod verify`, full `go test ./...` (30 packages, 0 failures), and a manual `-ldflags "-X fluxseer/internal/version.Version=..."` build to confirm version stamping actually resolves post-rename, not just compiles.
- **Not done yet** — deliberately deferred, needs its own pass: binary/image *artifact* names (`/fluxagent-operator` inside the container, OCI label `io.fluxagent.git-dirty`), container image repository path (`fluxagent/operator`), Helm chart artifact name and OCI publish path, and the default namespace convention (`fluxagent-system` &rarr; new name). These aren't purely mechanical: the published path is `ghcr.io/fluxseer/fluxagent/operator` — `fluxseer` is already the org segment, so renaming the `fluxagent` sub-path to `fluxseer` too would produce a redundant `ghcr.io/fluxseer/fluxseer/operator`, which needs a real decision (drop the segment entirely vs. pick a distinct sub-path name), not a blind find-replace. The namespace convention also has the widest blast radius of anything in this plan — it's threaded through ~38 files including README quickstart commands users actually type, so it needs an explicit upgrade note for existing installs before it moves.
- CI env vars / self-hosted runner labels (`fluxagent-prod`/`fluxagent-test`) — internal only, no external compatibility concern, low priority, can ride along whenever convenient.

Exit criteria: a single dedicated migration doc (same spirit as the `v0.2` batch docs in this directory) enumerating old &rarr; new for every renamed surface, published alongside the release notes for whichever version finishes this.

## Phase 2 — CRD API group rename (optional, long-term, not currently planned)

README already flags that this needs "an explicit migration path" if it ever happens. Treat it as a separate decision requiring its own design doc — dual-serving old and new API groups (or a conversion path) during transition, since every existing installed custom resource is tied to `aiops.platform/v1alpha1`, and breaking that is far more disruptive than a source/artifact rename. Default position: don't do this unless there's a concrete reason to; nothing in Phase 0 or Phase 1 requires it.
