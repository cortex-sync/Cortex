# Releasing Cortex

How a Cortex release is cut, the gates that must be green first, and how to verify
the result. The release is fully automated from a tag push - there is no manual
asset upload.

## How a release happens

Pushing a tag matching `v*.*.*` triggers `.github/workflows/release.yml`:

1. **`e2e` gate** - `make e2e` runs an end-to-end sync against a disposable Gitea
   over HTTPS.
2. **`verify` gate** - checks all **three** version-bearing files agree with the
   tag (`bin/VERSION`, `.claude-plugin/plugin.json`, `mcpb/manifest.json` - see
   the pre-tag checklist below), then re-runs CI's full gate set against the
   tagged commit: lint, vet, build, test + the 75% coverage floor, gosec,
   govulncheck, and gitleaks. The CI workflow also runs on the tag push, but as
   a separate workflow was never itself a release gate - `verify` closes that.
   Both `e2e` and `verify` run in parallel and must pass before anything below
   starts, so a version mismatch or a red gate is caught **before** goreleaser
   builds or publishes anything.
3. **`goreleaser release --clean`** - cross-compiles `cortex-git-server` for the
   five targets (linux/darwin `amd64`+`arm64`, windows `amd64`; no windows/arm64),
   builds `tar.gz` archives (`zip` for windows) named
   `cortex-git-server_<version>_<os>_<arch>`, generates `checksums.txt`, and
   creates the GitHub release **as a draft** (`.goreleaser.yaml` `release.draft:
   true`) - not yet public. Built with `GOTOOLCHAIN=go1.26.5`.
4. **`.mcpb` bundles** - packs one Claude Desktop / Cowork bundle per target and
   attaches them to the still-draft release.
5. **Publish** - only now is the release flipped public (`gh release edit
   --draft=false`). A failure at step 3 or 4 leaves a non-public draft instead
   of a live, partial release - delete the draft and retry rather than treating
   it as a shipped (if incomplete) release.

The launcher (`bin/cortex-git-launch.sh`) fetches
`cortex-git-server_<version>_<os>_<arch>.tar.gz` + `checksums.txt` from the release
for the tag in `bin/VERSION`, and verifies the SHA-256 (fail-closed). So the tag,
`bin/VERSION`, and the goreleaser archive names must all agree - they do by
construction (goreleaser's `name_template` matches the launcher's fetch pattern).

## Pre-tag checklist

- [ ] All **three** version-bearing files agree with the tag (see
      [CONTRIBUTING.md](../CONTRIBUTING.md#releases)): `.claude-plugin/plugin.json`
      `version` (no `v`), `bin/VERSION` (with `v`), and `mcpb/manifest.json`
      `version` (no `v`). The `verify` job checks this automatically and fails
      closed before any build starts - this is a local pre-check to catch it
      before pushing the tag, not the only gate.
- [ ] `make validate` is green (lint, vet, build, unit tests) - `verify` re-runs
      this (plus coverage, gosec, govulncheck, gitleaks) against the tagged
      commit regardless, but a red local run means a red release run.
- [ ] `make test-launcher` is green (SHA-256 integrity gate).
- [ ] `make e2e` passes locally, or you are confident the CI `e2e` gate will
      (it is a hard release gate).
- [ ] `go -C mcp/git-server mod tidy` is a no-op (clean tree) - goreleaser runs it
      as a before-hook, so a dirty tidy would change the release.
- [ ] The changelog will read well: goreleaser groups `feat`/`fix` commits and
      excludes `chore`/`docs`/`style` (see `.goreleaser.yaml`).
- [ ] `THIRD_PARTY_LICENSES.md` is current if dependencies changed (`make licenses`).

## Cutting the release

From an up-to-date `main`:

```shell
git tag v0.2.0        # annotated is fine too: git tag -a v0.2.0 -m "v0.2.0"
git push origin v0.2.0
```

Then watch the run: `gh run watch --repo cortex-sync/Cortex` (or the Actions tab).

## Post-release verification

- [ ] `gh release view v0.2.0 --repo cortex-sync/Cortex` lists the five archives,
      `checksums.txt`, and the `.mcpb` bundles.
- [ ] A clean install works end to end: on a machine with **no** local dev binary
      (`mcp/git-server/bin/cortex-git-server` absent) and no `~/.cache/cortex`,
      `/plugin install cortex@cortex` registers the `cortex-git` tools. The
      launcher should fetch, checksum-verify, and cache the release binary.
- [ ] `get_auth_status` responds (the server started), and a `git_status` on a
      known repo works.

## Current state - v0.2.0 (as of 2026-07-01)

Validated by this session (short of running goreleaser itself, which is not
installed locally and runs in CI):

- All three version files agree: `.claude-plugin/plugin.json` `0.2.0`,
  `bin/VERSION` `v0.2.0`, `mcpb/manifest.json` `0.2.0`.
- All five targets **cross-compile cleanly** with the release flags
  (`CGO_ENABLED=0`, `-s -w -X main.version=...`); the four unix archives were
  built and named exactly as the launcher fetches them, with a valid
  `checksums.txt`. Windows `amd64` compiles; only the local `zip` packaging step
  was skipped (CI has `zip`).
- `make validate`, `make test-launcher`, and `go mod tidy` are all green / no-op.

**Remaining human gate before tagging:** the hands-on Cowork `.mcpb` install test
on an unmanaged (non-OSA) machine. Once that passes, work the pre-tag checklist
above and push `v0.2.0`. Authenticode signing for managed hosts is a later (v0.3)
concern and does not block v0.2.0.
