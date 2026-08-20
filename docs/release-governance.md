# Release governance

This repository separates deterministic CI from release publication.

## CI and test artifacts

- Pull requests targeting `main` run deterministic validation only. They do
  not publish images and do not receive Harbor credentials.
- Pushes to `main` and `test` run the deterministic CI workflow.
- A push to `test` publishes test artifacts only after the deterministic jobs
  pass. The `test` branch must remain protected by a repository ruleset.

## Final releases

Final publication is manual through `FluxSeer RCA Release`.

- The workflow must use the `release` environment, with required reviewers and
  self-review disabled.
- Final releases must use an existing annotated `v*` tag and provide a
  successful `FluxSeer RCA Release Qualification` run ID for the exact source
  commit.
- The release workflow has no automatic tag-push trigger. This prevents an
  unqualified tag push from attempting publication.
- Release images publish BuildKit provenance and SBOM attestations.

The `main` branch, `test` branch, and `v*` tags should each have repository
rulesets. At minimum, the rulesets should prevent deletion and force-push;
branches should require a pull request, code-owner approval, and the required
deterministic checks. Tag creation and updates should be limited to release
maintainers.

All third-party GitHub Actions in the workflows are pinned to full commit SHAs.
When updating an action, change the SHA and retain the human-readable version
comment beside it.
