# Contributing to Cortex

These are the rules we follow for all work on Cortex. They exist to keep the
codebase **slim, maintainable, and easy to reason about**. Keep changes small
and focused; prefer deleting code to adding it.

## Principles

- **Single static binary, no runtime deps.** The MCP server must stay a pure-Go
  binary - `go-git` for git (no system `git` at runtime), no CGO. This is a
  deliberate distribution and AV-friendliness choice.
- **HTTPS + PAT only.** No SSH transport.
- **Secrets never touch disk in plaintext.** PATs live in the OS keychain, or
  the AES-256-GCM encrypted-file fallback - never in config files or commits.
- **Business logic lives in `internal/`.** `cmd/server/main.go` only wires MCP
  tools to thin handlers; keep git/keychain logic in the `internal` packages.

## Prerequisites

- Go 1.26+ (build pinned to 1.26.4; the module requires 1.26)
- [lefthook](https://github.com/evilmartians/lefthook), `gitleaks`, and
  `commitizen` (`cz`) for the git hooks
- `python3` with `PyYAML` for the `check-yaml` hook (`commitizen` already
  brings `python3`; `PyYAML` ships with many Python distributions)
- Install the hooks once: `make hooks-install`

## Workflow

1. Branch off `main` - never commit directly to `main`.
   Use a descriptive prefix: `feat/`, `fix/`, `refactor/`, `ci/`, `docs/`.
2. Make the change, with tests.
3. Run `make fmt && make validate` (gofmt, `go vet`, build, **and tests**)
   before every commit.
4. Commit using [Conventional Commits](https://www.conventionalcommits.org)
   (`feat:`, `fix:`, `chore:`, `ci:`, `docs:`, `refactor:`, `test:`). The
   `commit-msg` hook enforces this.
5. Open a pull request against `main`. CI must be green (lint, test, gitleaks)
   before merge. Squash or keep history tidy; let the PR delete the branch.

Stop and ask before any destructive git operation.

## Code style

- **Formatting:** `gofmt` (enforced by hook and CI). No exceptions.
- **British English** in comments, docs, log messages, and user-facing strings.
- **Docstrings on every exported function**, starting with the function name.
- **Wrap errors with context:** `fmt.Errorf("doing X: %w", err)`. Never discard
  an error silently.
- **No dead code.** If it is not called, delete it.
- Match the surrounding code's idiom and density; don't introduce a new pattern
  where an existing one fits.

## Tests

The suite is now comprehensive and CI enforces a **70% coverage floor** (see
`.github/workflows/ci.yml`), so changes should be **test-driven by default**:
write or adjust the failing test first, watch it fail, then implement until it
passes. This is the expected workflow now, not an afterthought - the floor will
reject a change that drops coverage below it anyway, and behaviour changes to the
git wrapper have already shipped this way (e.g. the last-write-wins pull and the
unpushed-commit flush fixes).

- Every exported function in `internal/` should have coverage.
- **Tests must run offline and hermetically** - no real network, no real OS
  keychain, no shared global state left behind. Use `t.TempDir()` for repos.
- Use `keyring.MockInit()` for keychain tests; use a local bare repo for git
  network operations (see `internal/git/git_test.go` `TestSyncRoundTrip`).
- Prefer table-driven tests for pure functions.
- **Pin deliberate or destructive behaviour** with a test that documents the
  intent (e.g. `TestPullDiscardsUnpushedLocalCommits`), so any future change to
  that contract is a conscious one.
- Beyond Go: `make mcpb-check` verifies a built `.mcpb`, and `make test-launcher`
  proves the launcher's SHA-256 integrity gate fails closed. Both run in CI.
- `make validate` (fmt, lint, vet, tests) must pass before committing; the HTTPS
  end-to-end round-trip is `make e2e` (needs Docker).

## Running from a local checkout

Cortex is not published to a plugin marketplace yet, so for now you run it from
a local checkout. This is the canonical local-run guide - the README and usage
guide point here rather than repeating the steps.

The bundled `.mcp.json` resolves the server binary through
`${CLAUDE_PLUGIN_ROOT}`, which Claude Code only sets when it loads Cortex **as a
plugin**. Opening this repo as an ordinary project leaves the variable
unexpanded, so the `cortex-git` server will not start - that is expected, not a
bug.

1. Build the server binary: `make build` (outputs
   `mcp/git-server/bin/cortex-git-server`). Use `make build-all` to
   cross-compile for all platforms (darwin/linux/windows, amd64/arm64).
2. Launch Claude Code with the checkout loaded as a plugin:
   `claude --plugin-dir /path/to/cortex`. This sets `${CLAUDE_PLUGIN_ROOT}` to
   the checkout, so the path resolves.
3. After changing plugin files, run `/reload-plugins` to pick up the changes
   without restarting.

Setting `${CLAUDE_PLUGIN_ROOT}` by hand is not a supported workflow - use
`--plugin-dir`. Marketplace install instructions will be added once publishing
lands.

## Security rules

- Never commit credentials or secrets - `gitleaks` runs locally (pre-commit)
  **and** in CI (cannot be bypassed with `--no-verify`).
- PATs are accepted only via `set_credentials` and stored in the credential
  store; never written to files or echoed back.
- The `profile-template/.gitignore` is the profile repo's safety net - keep its
  secret patterns in sync with the `sync-profile` skill's safety gate.

## Dependencies

Keep the dependency tree small - justify every new dependency in the PR. Cortex
is a personal project running on home CI, so public registries (Go module
proxy, Docker Hub, ghcr.io) are acceptable; pin versions for anything in CI.

## Project layout

```
.claude-plugin/plugin.json   Plugin manifest
.mcp.json                    Registers the cortex-git MCP server
skills/                      Claude skills (prose; setup, sync, restore, promote)
mcp/git-server/
  cmd/server/main.go         MCP tool registration + thin handlers
  internal/git/              go-git wrapper (all git logic + tests)
  internal/keychain/         credential store: keychain + encrypted-file fallback
profile-template/            files the setup skill seeds a new profile repo with
docs/design.md               architecture and decision log
```

## Releases

Releases are driven by goreleaser + Conventional Commits (semver). Before
tagging `vX.Y.Z`, bump the version in **all three** version-bearing files so
they agree with the tag:

- `.claude-plugin/plugin.json` (`version`, no `v` prefix)
- `bin/VERSION` (the launcher fetches the binary from this tag's release; keep
  the `v` prefix)
- `mcpb/manifest.json` (`version`, no `v` prefix) - the release pipeline
  **fails closed** if this does not match the tag, so the `.mcpb` bundles can't
  ship mis-versioned.

Then commit, `git tag -a vX.Y.Z -m ...`, and push the tag. The release workflow
runs e2e, then goreleaser (binaries + `checksums.txt`), then packs and attaches
the `.mcpb` desktop-extension bundles (one per target via `make mcpb-all`).

For the full pre-tag and post-release checklist, see
[docs/RELEASING.md](docs/RELEASING.md).
