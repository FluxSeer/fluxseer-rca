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

1. Rename the Helm chart source directory `charts/kube-ai-sre/` &rarr; `charts/fluxagent/`. This only touches internal repo paths (Makefile, CI packaging scripts, `helm lint`/`helm package` invocations) — the published chart name, OCI path, and `Chart.yaml` `name:` field don't change, so no existing install is affected.
2. Going forward, new prose should say "FluxSeer RCA" / "FluxSeer" for the product and only say "fluxagent" when specifically naming a compatibility surface (a binary, a namespace, an image path) — this is already mostly true; treat it as a writing-convention check, not a rewrite.

## Phase 1 — Next intentional breaking-change release

Bundle every breaking rename into **one** version bump rather than trickling them out across releases (each one individually forces existing installs to update references; spreading them out just multiplies the number of times operators have to do that):

- Go module path `fluxagent` &rarr; new name. Mechanical but large (~88 files' import paths) — verify first whether anything external actually imports this as a Go library (unlikely for an operator, but check before assuming zero impact).
- Binary/`cmd/` names, container image repository path, Helm chart artifact name and OCI publish path.
- Default namespace convention (`fluxagent-system` &rarr; new name), with an explicit upgrade note since existing clusters have workloads and RBAC bound to the old namespace.
- CI env vars / self-hosted runner labels (`fluxagent-prod`/`fluxagent-test`) — internal only, no external compatibility concern, can ride along in the same PR for convenience.

Exit criteria: a single dedicated migration doc (same spirit as the `v0.2` batch docs in this directory) enumerating old &rarr; new for every renamed surface, published alongside the release notes for whichever version does this.

## Phase 2 — CRD API group rename (optional, long-term, not currently planned)

README already flags that this needs "an explicit migration path" if it ever happens. Treat it as a separate decision requiring its own design doc — dual-serving old and new API groups (or a conversion path) during transition, since every existing installed custom resource is tied to `aiops.platform/v1alpha1`, and breaking that is far more disruptive than a source/artifact rename. Default position: don't do this unless there's a concrete reason to; nothing in Phase 0 or Phase 1 requires it.
