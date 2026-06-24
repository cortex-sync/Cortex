# Cortex - roadmap

**Status (2026-06-15):** **v0.1.1 is the latest release** (tagged 2026-06-12;
v0.1.0 before it), publicly installable
(`/plugin marketplace add cortex-sync/Cortex` -> `/plugin install cortex@cortex`).
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

## Security review (go-live, 2026-06-16)

Findings from a multi-agent security review (each adversarially verified before
inclusion). Original verdict: **close, but two things block public release** - the
one true credential bug (C1) and reconciling `SECURITY.md` so it stops claiming
guarantees the code does not deliver (M5/M8/M9). **Both blockers are now cleared**
(C1 via #19; the truth-in-docs reconciliation, M5/M6/M7/M8/M9, via #20). Everything
else is hardening, sequenced below. Supply-chain fixes are framed for a **public**
project (Sigstore keyless signing, SLSA provenance, SHA/digest pinning) - no
internal mirror.

### Blockers - must fix before going public

- [x] **(done 2026-06-16) C1 (critical) - HTTPS enforcement bypassed on push/pull;
      PAT could travel cleartext.** `mcp/git-server/internal/git/git.go` - `RemoteHost`
      now resolves the origin via `RequireHTTPS` (was `ParseHost`), so the push/pull
      credential gate fails closed on any non-https or userinfo origin before a PAT is
      read or sent, matching the clone/init paths. The unsafe `ParseHost` primitive was
      deleted (internal pkg, no external consumers). Regression test
      `TestRemoteHostRejectsInsecureOrigin` covers http/ssh/userinfo origins; `make
      validate` green. Note: this makes the SECURITY.md "https enforced / fails closed"
      claim true on the push/pull path - the truth-in-docs item below still needs M5/M8/M9.
- [x] **(done 2026-06-23, PR #20) Truth-in-docs - reconcile `SECURITY.md` with
      reality.** Each over-promise was resolved by fixing the control where it could
      deliver and dialling the claim back where it could not: the "https enforced /
      fails closed" line (C1, via #19), the content-scan coverage and oversized/binary
      behaviour (M8 - now "scans the readable head, skips binary", not "fails closed"),
      the machine-binding caveat (M9 - documents the unbound fallback and the
      `CORTEX_MACHINE_ID` override), and secret-scan coverage (M5/M6/M7). `make
      validate` green.

### Credential handling

- [x] **(done 2026-06-23, PR #20) M9 (medium) - encrypted-file key silently degrades
      to a public constant.** `internal/keychain/file_store.go` (`deriveKey`,
      `machineID`). `machineID` now reports whether the identifier is genuinely
      machine-bound; when it is not (no `/etc/machine-id`, falling back to hostname or
      the constant) `deriveKey` emits a one-time loud stderr warning that the store is
      PORTABLE, instead of degrading silently. A new `CORTEX_MACHINE_ID` env override
      lets headless/container deployments supply a stable secret identifier to restore
      real binding. Took the "warn loudly + opt-in restore" path rather than refusing
      the backend outright, so existing stores are unaffected. Regression tests cover
      the override, the machine binding, and the warn-once behaviour; `make validate`
      green. *(The "Passphrase mode" enhancement remains the upgrade path for real
      at-rest crypto.)*
- [ ] **L2 (low) - `set_credentials`/`delete_credentials` model-callable, no
      confirmation.** `cmd/server/main.go:276-298`. `delete_credentials` silently wipes
      a stored PAT from model-supplied args (and "succeeds" even if none was stored).
      **Fix:** require explicit user confirmation for credential mutations and echo
      the affected host; consider driving these from a user-initiated skill rather
      than a model-callable tool.
- [ ] **L4 (low) - `hostsEqual` does no IDN/punycode normalisation.**
      `cmd/server/envcreds.go:63-68`. Only lowercases/trims; a punycode/Unicode
      confusable origin can make env-token scoping behave inconsistently (compounded
      by C1). **Fix:** normalise both hosts via `golang.org/x/net/idna` before exact
      comparison; reject non-normalisable hosts.
- [ ] *(info, optional)* enforce `0700` on the credentials dir when it pre-exists
      (`internal/keychain/file_store.go:158-161`); add a regression test asserting no
      credential-handler output ever contains the token (PAT-in-logs verified clean
      today - keep it that way).

### Git operations / tool surface

- [ ] **M1 (medium) - all path-taking git tools accept arbitrary, unvalidated paths.**
      `cmd/server/main.go:168-258`. Model-supplied `repo_path`/`local_path` go straight
      to go-git with no `Clean`/`Abs`, no allowlist, no confinement. A prompt-injection
      can point ops outside scope (`git_status` freely reachable for path disclosure).
      **Fix:** confine all path args to a configurable root (e.g. `CORTEX_REPO_ROOT`),
      reject relative paths and anything resolving outside it after `Abs` +
      `EvalSymlinks` - mirror the `os.Root` confinement already used in `secretscan`.
      `git_init` also reuses an existing repo and stages `All:true` - refuse a
      non-empty pre-existing dir unless it is the expected profile repo.
- [ ] **M2 (medium) - `git_pull` does a destructive `HardReset` with no gate.**
      `internal/git/git.go:139-188`, invoked at `cmd/server/main.go:197`. Discards
      diverging commits and uncommitted changes onto origin's tip - no confirmation,
      dry-run, or backup. **Fix:** gate the hard reset behind an explicit destructive
      flag, refuse to reset a dirty worktree unless opted in (or auto-stash), and
      confine to the profile root per M1. *(Related: the existing "Better pull conflict
      strategy than last-write-wins" enhancement.)*

### Secret scanning (control fitness)

- [x] **(done 2026-06-23, PR #20) M8 (medium) - content scan fails open for binary,
      oversized, and deleted files.** `internal/secretscan/secretscan.go`;
      `internal/git/git.go`. `scanFile` now scans the first `maxFileSize` of an
      oversized file (head scanned, tail out of scope) instead of skipping it, and
      uses git's first-8 KiB NUL heuristic so a text file with a late stray NUL is
      still scanned. An overlong single line is tolerated (data blob, not pasted
      prose) without failing the commit. Resolution: rather than hard "fail closed"
      on every unscannable file, the gate is documented as a best-effort guard for
      accidental pastes into text, with `SECURITY.md` and `git.go` comments dialled
      back to match - the `.gitignore` and filename gate cover binary/tail residue.
      Regression tests cover oversized-head, late-NUL, and overlong-line cases; `make
      validate` green.
- [x] **(done 2026-06-23, PR #20) M5 (medium) - AWS secret access key not detected
      (only the `AKIA` key ID).** `internal/secretscan/secretscan.go`. Added an
      `aws-secret-access-key` rule keyed on the conventional assignment context near a
      40-char value, plus an `azure-storage-key` rule for `AccountKey=`/
      `SharedAccessKey=` connection-string values. `SECURITY.md` coverage list
      updated. Regression tests added; `make validate` green.
- [x] **(done 2026-06-23, PR #20) M6 (medium) - generic secret rule misses all
      unquoted assignments.** `internal/secretscan/secretscan.go`. The
      `generic-secret-assignment` rule now matches a quoted OR an unquoted value
      (bounded to non-whitespace), catching the dominant `DB_PASSWORD=...` /
      `client_secret: ...` `.env`/YAML shapes. False positives on prose kept low by
      requiring the compound identifiers (which English writes with spaces); a
      clean-prose regression test guards this. `make validate` green.
- [x] **(done 2026-06-23, PR #20) M7 (medium) - PEM `ENCRYPTED PRIVATE KEY` header
      not matched.** `internal/secretscan/secretscan.go`. `ENCRYPTED ` added to the
      `private-key-block` header alternation. Regression test covers the PKCS#8
      encrypted-key header; `make validate` green.
- [ ] **L3 (low) - gitleaks allowlist whitelists entire test files.**
      `.gitleaks.toml:21-26`. Blanket path exemptions for `secretscan_test.go`/
      `git_test.go` mean a real secret added to those files is invisible to CI and the
      hook. **Fix:** scope the allowlist to specific fixture strings, or use
      commit/line-level allowlisting.

### Supply chain / release integrity (do M3 + M4 + M12 as one piece of work)

- [ ] **M3 (medium) - release pipeline produces no signed/attested artifacts.**
      `.goreleaser.yaml:46-77`; `.github/workflows/release.yml`. Emits `checksums.txt`
      only - no signing, SBOM, or provenance; `.mcpb` bundles attached unsigned.
      **Fix (public-friendly):** add a goreleaser `signs:` stanza using **cosign
      keyless** (Sigstore + GitHub OIDC - no key management), add `sboms:` (syft), and
      add `actions/attest-build-provenance` for SLSA provenance in `release.yml`.
      *(Supersedes the optional "cosign-sign release artifacts" item below - promote it
      from optional to a go-live hardening task.)*
- [ ] **M4 (medium) - launcher checksum is self-referential, not anchored.**
      `bin/cortex-git-launch.sh:64-89`. Fetches the binary **and** `checksums.txt` from
      the same `$CORTEX_GIT_RELEASE_BASE` and verifies one against the other - protects
      against in-transit corruption only, not a compromised host or a malicious mirror.
      First-run code-exec path carrying the PAT. **Fix:** verify a cosign signature over
      `checksums.txt` against the repo's keyless OIDC identity (`cosign verify-blob
      --certificate-identity ...`), or pin per-platform SHA-256 inside the shipped repo.
      Treat `CORTEX_GIT_RELEASE_BASE` overrides as lowering trust (test builds only).
- [ ] **M12 (medium) - `.mcpb` bundle has no integrity verification; manifest injects
      PAT as plaintext env.** `scripts/pack-mcpb.sh:96-124`; `mcpb/manifest.json:24-28,
      37-42`; `release.yml:57`. The `.mcpb` is a plain zip with no checksum/signature of
      its own, and the manifest passes the token as `CORTEX_GIT_TOKEN` (readable via
      `/proc/PID/environ`). **Fix:** publish + cosign-sign checksums for the `.mcpb`
      bundles and verify on install; document the env-readability exposure. *(Folds into
      the existing ".mcpb binary is a separate build, not in checksums.txt" known gap -
      close them together.)*
- [ ] **M10 (medium) - all GitHub Actions pinned by mutable tag, not commit SHA.**
      `.github/workflows/*` (`checkout@v6`, `setup-go@v6`, `golangci-lint-action@v9`,
      `codecov-action@v5`, `codeql-action/*@v3`, `goreleaser-action@v7`). Tag-move
      injection risk, worst in `release.yml` (`contents: write` + tokens). **Fix:** pin
      every `uses:` to a full 40-char commit SHA (version in a trailing comment);
      Dependabot already present to bump them.
- [ ] **M11 (medium) - gitleaks binary downloaded with no integrity verification.**
      `.github/workflows/ci.yml:176-179`. `curl`'d + `install`'d with no checksum, then
      run as the secret-scanning gate. **Fix:** pin and verify the tarball SHA-256
      against a repo-committed value before `tar`/`install` (public GitHub release is
      fine - just verify it).
- [ ] **L1 (low) - lefthook installed via `go install ...@latest`.** `Makefile:54`.
      Moving target for the binary that drives every dev's pre-commit hooks. **Fix:**
      pin to an explicit tagged version (public GOPROXY is fine for an OSS project) -
      consistent with the already-pinned golangci-lint/gosec/govulncheck.
- [ ] **L5 (low) - e2e Gitea image on a mutable Docker Hub tag.** `e2e/Dockerfile:6`
      (`FROM gitea/gitea:1.26`). The e2e job gates releases, so a re-pushed tag changes
      release-blocking behaviour. **Fix:** pin by digest (`gitea/gitea@sha256:...`).
- [ ] **L6 (low) - e2e TLS private key written world-readable (`chmod 644`).**
      `e2e/gen-certs.sh:36`. Disposable localhost-only self-signed key, minimal impact,
      but readable by other local users mid-run. **Fix:** prefer `640` with a shared
      group; ensure `certs/` is gitignored.

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

## Codex support

A second AI-tool surface alongside Claude (the "cross-AI portability" goal in
`docs/design.md` §8). **OpenAI Codex CLI has converged on the same three primitives
Claude Code uses** - a global instruction file (`AGENTS.md`), stdio MCP servers, and
`SKILL.md` skills - so this is an *adapter* job, not a rebuild, and **needs no Go server
change**. Verified 2026-06-24 against the Codex docs + a read of the Cortex credential
code; full reasoning in the approved plan `~/.claude/plans/jolly-tumbling-cherny.md`.

**Direction: two tiers** - because Codex's *sandbox*, not skills or creds, is the real
constraint (same shape as the Cowork finding):
- **Tier 1 (primary): Codex as profile consumer** - `AGENTS.md` + memory as files Codex
  reads; sync stays host-side (the Claude Code Cortex owns it). No MCP server, no
  skills-in-Codex, no sandbox dependency. The robust default.
- **Tier 2 (opt-in): native Cortex in Codex** - `cortex-git` MCP server + skills running
  inside Codex. Gated on opening the sandbox network (+ a current MCP-cancel bug);
  documented, not turnkey.

**How Codex maps to Cortex (verified against the Codex docs):**
- **Instructions:** Codex reads a **global** `~/.codex/AGENTS.md` (and
  `AGENTS.override.md`) ahead of project-level `AGENTS.md` files, concatenated
  root-down. Home dir is `~/.codex` (override via `CODEX_HOME`). This is the direct
  analogue of `~/.claude/CLAUDE.md`. **Caveat:** instruction files are truncated at
  `project_doc_max_bytes` (**default 32 KiB**) - the full `CLAUDE.md` is too large, so
  AGENTS.md should be generated from the lean **`adapters/generic.md`**, which exists
  for exactly this (a non-Claude, portable profile).
- **MCP:** Codex runs **stdio binary** MCP servers (`[mcp_servers.<id>]` in
  `~/.codex/config.toml`: `command`/`args`/`env`/`cwd`), best added via `codex mcp add
  cortex-git -- <launcher>` (it writes the entry). The launcher slots in as `command`.
  **The block is command-only** - verified in `cmd/server/envcreds.go`: with the PAT in
  the keychain/file store, the binary resolves creds by the repo's host with no env vars;
  a partial env (host+username, no token) is ignored (`envCredentials` returns false on
  an empty token - no store shadow). `CORTEX_GIT_TOKEN` in `config.toml` is a
  plaintext-on-disk fallback only. **The catch is the sandbox** (see findings below), not
  the creds.
- **Skills:** Codex uses the same `SKILL.md` format (YAML frontmatter `name` +
  `description`, name matches the folder) and **invokes skills implicitly by
  description match** by default - so `/setup`, `/sync-profile`, `/restore-profile`,
  `/promote-lessons` fire the same way they do in Claude. **Auto-discovered** from
  `~/.agents/skills/` (user), `.agents/skills/` (repo), `/etc/codex/skills` (admin);
  Codex **follows symlinks**. `[[skills.config]]` only *disables* a discovered skill, it
  is NOT the registration mechanism - so skills install by symlink/copy (the easy part).
- **Skill bodies are already portable:** they call the git tools by **bare name**
  (`git_status`, `git_commit_push`, ...) with no `mcp__plugin_cortex_*` prefixes and no
  `/plugin` framing - which is exactly how Codex surfaces MCP tools. The only
  Claude-coupling is the hardcoded platform detection in `setup`/`restore-profile`
  (writes `~/.claude/CLAUDE.md` or `~/Documents/CLAUDE.md`).

**Verified runtime findings (2026-06-24) that shaped the direction:**
- **Sandbox blocks the network.** Under the default `workspace-write`, network is OFF by
  default and the sandbox applies to MCP-server subprocesses - so the server's git HTTPS
  is blocked unless `[sandbox_workspace_write].network_access = true` (+ host allowlist
  via `features.network_proxy`) or `danger-full-access`. Plus a current bug cancels MCP
  tool calls under workspace-write/read-only (works under danger-full-access). This is
  why Tier 1 routes sync host-side.
- **No SessionEnd hook** (only a per-turn `Stop`; openai/codex#20603 open) -> no
  auto-sync-on-handoff parity; sync is host-side (Tier 1) or explicit `/sync-profile`
  (Tier 2).
- **`codex mcp add`** writes the `config.toml` entry -> no fragile TOML hand-editing.
- **Codex is native on Windows now** (PowerShell, restricted-token/ACL sandbox) - the
  POSIX launcher won't run there; point `command` at the release `.exe`.
- **Skill bodies already portable** - bare tool names (`git_status`...), no
  `mcp__plugin_*` prefixes, no `/plugin` framing.

**No plugin/marketplace in Codex** - distribution is the `scripts/install-codex.sh`
bootstrap (does what `/plugin install` does on Claude) + the restore/setup skill branch.

**Sub-tasks for Codex (priority order):**
(i) ✅ **(done 2026-06-24) Generate the AGENTS.md adapter from `adapters/generic.md`**
(not the full `CLAUDE.md` - 32 KiB cap), neutralising the Claude-specific lines.
First artefact lives in Lucas's profile repo at `adapters/codex.md` (follows the
`gemini.md`/`chatgpt.md`/`generic.md` adapter convention; deploys to
`~/.codex/AGENTS.md`). Transform applied: dropped the "paste into a system prompt"
framing (AGENTS.md is auto-loaded), changed `if called "Claude", correct to "Bree"`
-> any other name, and **added a `## Memory` pointer** so Codex actually reads the
`memory/` files (`generic.md` had none) - the one deliberate addition beyond a pure
transform. The product mechanism (a skill that regenerates this adapter, per
`design.md` §8) is folded into sub-task (ii);
(ii) ✅ **(done 2026-06-24) Added a Codex branch to `restore-profile` and `setup`.**
`restore-profile` now has a **Codex CLI wire-up** section: resolve `$CODEX_HOME`
(default `~/.codex`), copy `adapters/codex.md` -> `$CODEX_HOME/AGENTS.md` (fallback
`generic.md`), merge a `[mcp_servers.cortex-git]` block into `config.toml` (launcher as
`command` - it self-resolves root/cache, no Claude env needed), and wire the four
skills by **symlinking them into `~/.agents/skills/`**, which Codex auto-discovers on
startup (the skills ship with the distribution, not the profile repo). **Corrected a
first-pass mistake:** `[[skills.config]]` is NOT the registration mechanism - it is
optional and only *disables* a discovered skill (`enabled = false`); discovery is purely
by directory scan (`~/.agents/skills/` user-level, `.agents/skills/` repo-level,
`/etc/codex/skills` admin), and Codex follows symlinks. So **skills are the easy part of
Codex support** (drop/symlink files, no installer); the MCP binary is the fiddly part
(no auto-fetch, launcher path + Windows gap). `setup` now also generates `adapters/codex.md`
(step 3) and places it for Codex (step 8, delegating to the restore wire-up).
**Credential-handling decision (deviates from this item's original "PAT via env"
wording):** the PAT is **NOT** inlined into `config.toml`. The launcher execs the
binary, which reads creds from the keychain / encrypted-file store that `set_credentials`
already populated - single source of truth, no plaintext token on disk, consistent with
"never PATs in files". `config.toml` carries only `CORTEX_GIT_HOST`/`_USERNAME` to scope
the lookup; `CORTEX_GIT_TOKEN` is documented as a fallback **only** when the store is
unavailable, with a plaintext-on-disk warning. Both skills instruct **merge, don't
clobber** existing `config.toml`, and to restart Codex after editing it;
(iii) ✅ **(done 2026-06-24)** `sync-profile` now finds the `## Cortex configuration`
repo path from `AGENTS.md` (`$CODEX_HOME/AGENTS.md`) as well as `CLAUDE.md`.
(iv) ✅ **(done 2026-06-24, docs)** Native-Windows Codex can't run the POSIX launcher;
`docs/usage.md` documents pointing `command` at the `cortex-git-server.exe` (from the
`.mcpb`/release). *Future enhancement:* a native PowerShell launcher with first-run
download + SHA-256 verify (own branch + test, mirroring `test-launcher.sh`).
(v) ✅ **(verified - resolved)** Codex has **no SessionEnd hook** (only a per-turn
`Stop`; openai/codex#20603). Not a blocker - Tier 1 syncs host-side, Tier 2 runs
`/sync-profile` explicitly. A `codex` shell wrapper that syncs on exit is a later nicety.
(vi) ✅ **(verified - the key finding)** Under the default `workspace-write` sandbox
Codex **blocks the MCP server's outbound network** (and a current bug cancels MCP calls);
Tier 2 needs `network_access = true` (+ host allowlist) or `danger-full-access`. Memory
*reads* of a profile repo outside the workspace may also be blocked under Tier 1 -
mitigations documented in `usage.md` / `restore-profile`. **Open hands-on checks:** Tier
2 end-to-end push under `network_access=true`, and exact `codex mcp add` env/cwd flags on
the installed build.
(vii) ✅ **(done 2026-06-24) `scripts/install-codex.sh`** - the host bootstrap (what
`/plugin install` does on Claude): `--profile-dir DIR` places `AGENTS.md` (Tier 1);
`--with-mcp` symlinks the 4 skills into `~/.agents/skills/` and runs `codex mcp add`
(Tier 2), printing the sandbox-network prerequisite. Idempotent; never touches the
profile git state or creds. Smoke-tested (placement, backup-on-conflict, symlink
idempotency, codex-absent fallback).
(viii) ✅ **(done 2026-06-24)** `docs/usage.md` has a `## Codex CLI` section (Tier 1 /
Tier 2 / Windows).

**Refs:** Codex docs (config-reference, mcp, guides/agents-md, skills,
concepts/sandboxing, agent-approvals-security, hooks, windows); openai/codex#20603
(SessionEnd request). Plan: `~/.claude/plans/jolly-tumbling-cherny.md`.

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
