# Cortex - roadmap

**Status (2026-06-15):** **v0.1.1 is the latest release** (tagged 2026-06-12;
v0.1.0 before it), publicly installable
(`/plugin marketplace add LucasSymons/Cortex` -> `/plugin install cortex@cortex`).
GitHub Actions CI, Dependabot, and the goreleaser release pipeline are all green.
**v0.2.0 is prepared on branch `feat/cowork-mcpb-bundle`** (env-credentials + the
`.mcpb` desktop bundle); it is awaiting a hands-on Cowork test on an unmanaged
machine before the tag is cut.

**Next up:**
- **v0.2.0 (prepared, awaiting test + tag):** the **Cowork / Claude Desktop**
  surface, following the released v0.1.1. The env-credentials server change
  landed 2026-06-12, and the `.mcpb` desktop-extension bundle (manifest + packer
  + icon + release wiring + CI test) is built. Version is bumped to v0.2.0 across
  `bin/VERSION` (was `v0.1.0` - v0.1.1 shipped skills only, binary unchanged),
  `.claude-plugin/plugin.json`, and `mcpb/manifest.json`. Remaining before
  tagging: a hands-on Cowork test, then Authenticode signing for managed hosts
  (v0.3, see Publishing). See `## Cowork support`.
- **Community marketplace** submission still queued (manual,
  `clau.de/plugin-directory-submission`).

Open items, grouped by theme. Each becomes a branch + PR.

## Setup / onboarding UX

- [x] **(done 2026-06-12, v0.1.1) Import an existing setup.** `/setup` Section 0
      detects an existing `CLAUDE.md` (`~/.claude`, the Cowork Documents folder),
      offers import-and-adapt vs start-fresh, plays back what it found for
      confirmation, only asks the missing sections, and offers to bring existing
      memory files along.
- [ ] **Deeper guided persona builder.** When a user wants a full character, branch
      into a richer guided interview (name, background, personality, values, voice)
      rather than the current handful of questions. (v0.1.1 added a light version -
      "develop the character together" - this item is the full treatment.)
- [x] **(done 2026-06-12, v0.1.1) Clean "no persona" path.** Persona section now
      opens with three equal options; "no persona" generates no Persona section at
      all (no placeholder heading).
- [ ] **Memory path resolution on Claude Code CLI.** When `CLAUDE.md` is placed at
      `~/.claude/CLAUDE.md`, the relative `memory/` reference should resolve to the
      profile repo regardless of working directory. `/setup` and `/restore-profile`
      should make the memory path explicit (or place a pointer).

## Cowork support

Cortex targets **Claude Code CLI** today (binary + skills + `~/.claude/CLAUDE.md`).
**Cowork** (the agentic mode in the Claude Desktop app) is a *different runtime* from
both the CLI and the plain Desktop chat. The findings below are **ground truth observed
from inside a live Cowork session (2026-06-10)** and supersede earlier screenshot-era
guesses.

**How Cowork actually works (observed from inside):**
- **Runtime:** a sandboxed **Ubuntu 22 VM**. Connected folders mount at
  `/sessions/<id>/mnt/<folder>/` (session id changes - never hardcode). **Only connected
  folders persist** across sessions; the rest of the sandbox is ephemeral.
- **CLAUDE.md:** auto-loaded from the **root of each connected folder**, injected as a
  `<system-reminder>`. Multiple connected folders each contribute their `CLAUDE.md`.
- **Skills:** `SKILL.md` files (plugins land under `mnt/.remote-plugins/plugin_<id>/`);
  invoking one is **prompt injection**, no subprocess. They work unchanged.
- **MCP - two paths:** the *plugin* connectors are **remote `"type": "http"`** (e.g.
  `mcp.atlassian.com`) - which is all Cowork-Bree saw, because her machine had no local
  servers configured. But the canonical doc (`claude.com/docs/cowork/3p/extensions`)
  confirms Cowork **also supports local MCP servers**: user-added via **Settings >
  Developer** (gated by the `isLocalDevMcpEnabled` admin toggle) or as a **`.mcpb`**
  installed from the **Connectors** page. Chrome control is exactly this - a local
  server driving local Chrome. **So a local binary MCP server CAN run for a Cowork
  session** (bridged from `claude_desktop_config.json`).
- **Network:** the sandbox has no direct egress - all traffic goes via a host proxy with
  a **host-controlled allowlist**. `github.com`/`gitlab.com` return **HTTP 403**; only
  allowlisted MCP hosts work. So git-over-HTTPS from inside Cowork is blocked.
- **Plugin install:** Cowork unpacks a `.plugin` bundle into `.remote-plugins/` (skills +
  an optional remote-HTTP `.mcp.json`) - a **subset** of the CLI `.claude-plugin` format:
  skills + remote MCP, **no binary launcher**.

**What this means for Cortex in Cowork:**
- ✅ **Skills work today, unchanged** - `/setup`, `/sync-profile`, `/restore-profile`,
  `/promote-lessons` run as prompt injection.
- ✅ **Profile loads** - connect a git clone of the profile repo as a folder; its root
  `CLAUDE.md` auto-loads and `memory/` is readable via the file tools.
- ✅ **The `cortex-git` binary CAN run in Cowork as a local MCP server** (Settings >
  Developer, or a `.mcpb` via Connectors). **Resolved 2026-06-10:** (a) Settings >
  Developer **is enabled** on Lucas's Origin-managed account; (b) local servers run
  **host-side** (strong evidence: the Snyk extension holds its token in **env vars**, and
  Chrome control drives the *local* browser) -> **host network, reaches git hosts**.
  Caveats: reliability (open Cowork local-MCP bugs), and **corporate app-control**
  (next bullet).
- ⛔ **(2026-06-12) Corporate app-control blocks unsigned binaries host-side.** Ground
  truth from the work PC: Origin Secure Access (OSA) **blocked a locally cross-built
  `cortex-git-server.exe`** the moment it was executed on the Windows host (run from a
  `\\wsl.localhost\...` path; "blocked to protect this device", no publisher). The exe
  never ran - WSL interop returned exit 0 with empty output, so test for *output*, not
  just exit code. Implications: host-side local MCP (sub-task (ii)) is gated on OSA for
  managed machines even with a goreleaser release, because release binaries are
  **unsigned** (the cosign item is artifact verification, not Windows Authenticode).
  Snyk's extension passes because it ships signed by a known publisher. The **WSL-side
  Linux binary is unaffected** - CLI Cortex keeps working. Plan: managed machines use
  host-side sync from WSL (the planned fallback); validate the Snyk-pattern wire-up on
  an unmanaged personal machine; Authenticode signing (see Publishing) + a one-off IT
  publisher-trust request is the real fix for corporate hosts.

**Viable Cowork path (no binary):**
- Deliver the **skills** to Cowork (its plugin install / a `.plugin` bundle).
- Put the **profile** (CLAUDE.md + memory) in a **connected folder that is a git clone**
  of the profile repo - Cowork reads it; OneDrive drops out.
- **Sync happens host-side, not in Cowork:** keep that clone current with `git` from the
  CLI Cortex or a host-side `git pull` (the sandbox can't reach git hosts). Cowork is a
  read-mostly consumer; memory edits it writes to the folder are pushed by the next
  host-side sync.

**Autonomous git sync *inside* Cowork is viable** (Lucas's account: Settings > Developer
enabled, local servers run host-side with host network). Wire `cortex-git` in via
Settings > Developer (or a `.mcpb`) and **pass the PAT as an env var** (the Snyk pattern)
- which needs the env-credentials server change below. Fallbacks if it proves flaky:
host-side sync (CLI / scheduled `git pull`), or a hosted HTTP MCP.

**Surface matrix.**
- **Claude Code CLI:** full (binary + skills + `~/.claude/CLAUDE.md`). Done.
- **Cowork agent:** skills + connected-folder profile; the binary is runnable as a
  local MCP server (Settings > Developer / `.mcpb`) subject to the admin toggle +
  verification, else host-side sync.
- **Desktop chat (non-Cowork):** *could* run the binary via `.mcpb` / Local MCP servers -
  a separate surface, lower priority. (`.mcpb` v0.3 supports `server.type: "binary"` +
  `platform_overrides` + `user_config` for the PAT, if we ever pursue it.)
- **Browser / mobile:** out (no local runtime).

**Sub-tasks for Cowork (priority order):** (i) ✅ **(done 2026-06-12) server change -
read creds from env** (`CORTEX_GIT_HOST` / `_USERNAME` / `_TOKEN`) so a local-MCP config
can inject the PAT via env, Snyk-style - env takes precedence over the store, scoped to
the named host only (`cmd/server/envcreds.go`); (ii) wire `cortex-git` into Cowork as a local MCP server (Settings >
Developer -> Windows `.exe` + env vars) and connect a git-clone folder for the profile -
**validate on an unmanaged personal machine first**; on the work PC this is gated on
OSA / a signed binary (see the ⛔ finding above);
(iii) deliver the skills to Cowork; (iv) ✅ **(built 2026-06-15) a `.mcpb` for one-click install** -
`mcpb/manifest.json` (manifest_version 0.3, `server.type: binary`, the 8 `git_*`/`*_credentials`
tools, `user_config` token[sensitive]/host/username mapped 1:1 to the `CORTEX_GIT_*` env vars,
`win32` platform_override for the `.exe`), packed by `scripts/pack-mcpb.sh` (`make mcpb` for the
host target, `make mcpb-all` for the 5 release targets). Packs a **clean staging dir
(`manifest.json` + single binary [+ optional `icon.png`] only), NOT the repo** (avoids Snyk's
`.circleci`/`.vscode`/`node_modules` mistake); portable archiver (mcpb|zip|python3) that preserves
the binary's `0755` exec bit. Smoke-tested: linux/amd64 bundle built, entries at root with correct
modes, manifest parses, bundled binary runs `--version` = 0.2.0. Install docs added to
`docs/usage.md`. **(2026-06-15) Remaining-for-(iv) closed except signing:** (a) ✅ **icon** -
`mcpb/icon.png` (512x512, original artwork = a node-graph on an indigo->cyan gradient, generated by
`scripts/gen-icon.py`, ships MIT with the repo; manifest `icon` field set); (b) ✅ **release wiring** -
`release.yml` packs `make mcpb-all` and `gh release upload`s the bundles after goreleaser, with a
guard that fails the release if `mcpb/manifest.json` version != tag; (d) ✅ **version** - bumped to
**v0.2.0** across `bin/VERSION` + `.claude-plugin/plugin.json` + `mcpb/manifest.json` (v0.2.0 is the
release after the already-tagged v0.1.1; `bin/VERSION` moves off `v0.1.0` because v0.2.0 is the first
release with a changed binary, whereas v0.1.1 was skills-only; release recipe in CONTRIBUTING.md updated); (c) ⛔ **STILL
OPEN (Lucas, v0.3)** - the bundled binary is **unsigned** -> managed-host app-control (OSA) blocks it;
Authenticode signing is the real fix (see Publishing). **All of (iv) is on branch
`feat/cowork-mcpb-bundle`, awaiting Lucas's hands-on test on an unmanaged home machine (tonight) before
tagging v0.2.0.** (v) *(fallback only)* a hosted HTTP MCP.

**Real template (Snyk's local MCP server, observed 2026-06-10):** a `.mcpb`-managed
**stdio** server - `command` = the binary, `args: mcp -t stdio`, and **`env:
SNYK_TOKEN=${user_config.snyk_token}`** with a `user_config.snyk_token` field, shown
"running" host-side. Cortex maps 1:1: `command` = `cortex-git-server[.exe]` (stdio by
default, no subcommand), `env: { CORTEX_GIT_TOKEN: ${user_config.token}, CORTEX_GIT_HOST:
..., CORTEX_GIT_USERNAME: ... }`, `user_config.token` (`sensitive: true`). Confirms the
env-creds server change is exactly the Snyk model. **Real `manifest.json` files read
2026-06-10** from the packaged-app path
`%LOCALAPPDATA%\Packages\Claude_pzs8sxrjxfjjc\LocalCache\Roaming\Claude\Claude Extensions\<ext>\manifest.json`
(MSIX redirect, not plain `%APPDATA%`). Confirmed schema: `manifest_version: "0.3"`,
`server` (`type`/`entry_point`/`mcp_config`), `user_config` (sensitive secrets),
`tools[]` (+ `tools_generated`), `prompts[]`, `compatibility.platforms`, `keywords`,
`license`, `icon`. Snyk/Filesystem are `type: "node"` (one entry point); **Cortex is
`type: "binary"`, so it needs `platform_overrides` per OS (darwin/linux/win32) - or
per-platform `.mcpb` bundles - with arch (amd64/arm64) via a macOS universal build or a
wrapper. Add a `tools[]` block for the 8 `cortex-git` tools.

## Publishing / install

- [x] Shipped in **v0.1.0**: binary launcher (fetch + SHA-256 verify into
      `${CLAUDE_PLUGIN_DATA}`), `SessionStart` warm hook, `marketplace.json`,
      `plugin.json` polish, the goreleaser release pipeline (`checksums.txt`), and a
      verified end-to-end install + real-host sync test.
- [x] **(2026-06-12) Launcher distribution model re-validated** against current plugin
      docs (`code.claude.com/docs/en/plugins.md`): download-on-first-run into
      `${CLAUDE_PLUGIN_DATA}` (persists across updates, deleted on last uninstall
      unless `--keep-data`) + SHA-256 verify + `SessionStart` prefetch **remains the
      documented pattern** - no first-class platform-binary mechanism for plugins
      exists as of v2.1.173, and recent changelogs even improved tolerance of
      endpoint-security scanning delaying new binaries. Keep as-is for v0.2.0;
      `.mcpb` `platform_overrides` stays Desktop/Cowork-only.
- [x] **(resolved 2026-06-12 - no action)** `startupTimeout` per-server field: verified
      against `mcp-configuration.md` that **no such field exists** (an earlier doc check
      claimed otherwise - it was wrong). The only knobs are the global `MCP_TIMEOUT`
      env var (startup, all servers) and the per-server `timeout` field (tool
      execution, not startup). The SessionStart prefetch hook remains the right
      mitigation for first-run download time; nothing to add to `.mcp.json`.
- [ ] *(Optional)* cosign-sign release artifacts and verify the signature in the
      launcher, on top of the existing SHA-256 check.
- [ ] *(Known gap, low priority)* the binary inside each `.mcpb` is a **separate
      build** from the goreleaser tar.gz binary (different ldflags, no
      commit/date) and is **not covered by `checksums.txt`**. Acceptable for now
      (desktop users install the `.mcpb` directly from the release page, and CI's
      `mcpb` job structurally verifies every bundle). If we want byte-identical,
      checksum-covered bundles, have `pack-mcpb.sh` reuse goreleaser's already-built
      `dist/` binaries instead of rebuilding, and add the `.mcpb` files to the
      checksum set.
- [ ] **Authenticode-sign the Windows release binary** - **target: v0.3** (pull
      forward if it blocks the Cowork wire-up). Required for corporate app-control
      hosts (see the OSA finding under `## Cowork support`). Plan: apply to SignPath
      Foundation (juried application, project owner submits), then restructure the
      release job so the `.exe` is signed *before* `checksums.txt` is computed -
      signing changes the binary bytes, and the launcher's SHA-256 check is
      fail-closed. Options
      researched 2026-06-12: **SignPath Foundation** (free for OSS, CI-native via
      GitHub Actions, publisher shows "SignPath Foundation", manual approval per
      release) is the front-runner; **Certum Open Source** (~EUR 69 + VAT first year
      incl. smartcard, ~EUR 30/yr renewal, own-name publisher, local signing only);
      **SSL.com IV + eSigner** (~USD 129/yr + USD 20/mo, own-name, CI-automatable);
      **Azure Artifact Signing** (USD 9.99/mo, best CI story, but **individuals in
      Australia not eligible** as of 2026-05 - re-check periodically). Notes: EV buys
      nothing for SmartScreen since 2024-03; new signed publishers still accumulate
      SmartScreen reputation over time; cert validity is capped at ~458 days since
      2026-03 so everything is effectively a subscription.
- [ ] *(Optional)* Submit to the `anthropics/claude-plugins-community` marketplace
      via `clau.de/plugin-directory-submission`.

## Enhancements (later)

- [ ] **Migrate `mcp-go` 0.18 -> 0.5x.** Dependabot's grouped bump (old PR #2, closed)
      failed CI across the board - the 0.5x API is a breaking change for the whole
      server surface (tool registration, request argument access). Dependabot now
      ignores the dependency until this lands (`.github/dependabot.yml`); plan it as
      its own branch with the full test suite as the safety net.
- [ ] **Passphrase mode** for the encrypted-file credential fallback, for real at-rest
      protection on headless boxes (currently machine-bound obfuscation).
- [ ] **Real `git_diff`** - a content-level change preview (the stub was removed as it
      duplicated `git_status`).
- [ ] **Better pull conflict strategy** than last-write-wins (`Force: true`).
- [x] **(done 2026-06-12) `golangci-lint`** v2.12.2: curated config at
      `mcp/git-server/.golangci.yml` (standard set + errorlint, gocritic, revive,
      misspell locale UK with an `initialize` ignore for the MCP API name, and
      friends), `make lint` (pinned version, auto-installs via GOPROXY, also lints
      the e2e build tag) now runs as part of `make validate`, and the CI lint job
      runs golangci-lint-action@v9. First run caught three real wrapped-error
      comparison bugs in `internal/git` (fixed with `errors.Is`).
- [x] **(done 2026-06-12) `CORTEX_CONFIG_DIR` / force-file-backend override** - when
      set, the encrypted-file backend is pinned at `$CORTEX_CONFIG_DIR/credentials.enc`
      and the keychain probe never runs (`keychain.selectStore`). Read once at first
      use (selection is process-cached). Existing tests keep `XDG_CONFIG_HOME`
      isolation; new E2E work should prefer `CORTEX_CONFIG_DIR` - it is deterministic
      on macOS too, where `XDG_CONFIG_HOME` is ignored and a desktop keyring would
      otherwise win.

## Code-review leftovers (optional)

- [x] **(done 2026-06-12)** Note the weakened-key fallback in code: `machineID`
      (`internal/keychain/file_store.go`) now carries a security note that all key
      inputs are non-secret and points at passphrase mode as the upgrade path.
- [x] **(done 2026-06-12)** Unique temp file in the file store: `fileStore.save`
      uses `os.CreateTemp` in the credentials dir instead of a fixed `path + ".tmp"`.

## Docs

- [x] Install/setup steps finalised in `README.md` and `docs/usage.md` (v0.1.0).
