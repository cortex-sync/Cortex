# Using Cortex

Cortex keeps your AI profile (persona, instructions, memory) in a Git repo so it
follows you across devices and AI tools. This guide covers day-to-day use and
troubleshooting.

> **Install:** add the marketplace, then install the plugin:
>
> ```shell
> /plugin marketplace add cortex-sync/Cortex
> /plugin install cortex@cortex
> ```
>
> The plugin downloads its prebuilt MCP-server binary on first run (needs `curl`,
> `tar`, and `sha256sum`/`shasum`; on Windows, run under WSL). See the
> [README](../README.md#installation) for details. If that download fails (for
> example the release for the pinned version isn't published yet) the `cortex-git`
> tools won't appear - see
> [The `cortex-git` tools don't appear](#the-cortex-git-tools-dont-appear).

## Concepts

- **Profile repo** - a private Git repo holding your `CLAUDE.md`, `memory/`, and
  `adapters/`. The source of truth.
- **Credential store** - your Git PAT, kept in the OS keychain or an encrypted
  file. See [SECURITY.md](../SECURITY.md).
- **Skills** - the commands you run: `/setup`, `/sync-profile`,
  `/restore-profile`, `/promote-lessons`.

## First-run setup (`/setup`)

`/setup` runs a guided questionnaire, generates your profile, and pushes it.
Because Cortex doesn't create remote repos for you, there's one manual step:

1. Answer the questionnaire (identity, stack, persona, rules, Git host).
2. **Create an empty private repo** in your host's web UI (GitHub/GitLab/Azure
   DevOps). It must be *truly empty* - no README, no .gitignore, no licence.
   Copy the HTTPS clone URL.
3. Cortex stores your PAT (`set_credentials`), then initialises and pushes your
   generated profile to that repo (`git_init`).
4. Your `CLAUDE.md` is placed where the harness loads it
   (`~/.claude/CLAUDE.md` for Claude Code CLI, `~/Documents/CLAUDE.md` for Cowork).

Why the manual repo step? See the
[design note on first-run repo creation](design.md).

## Day to day (`/sync-profile`)

Run `/sync-profile` (or let it run on session handoff) to commit and push any
changed memory files. It will:

1. Check what changed.
2. Refuse to commit anything that looks like a secret (safety gate).
3. Commit with a generated message and push.

## New device (`/restore-profile`)

On a new machine, run `/restore-profile`. You'll provide your repo URL, Git
username, and PAT; Cortex stores the PAT, clones the repo, and places `CLAUDE.md`
in the right location for the platform.

## Promoting lessons (`/promote-lessons`)

Lifts project-level lessons worth keeping into your top-level memory, so they're
available in every future session. Runs on sync or on demand.

---

## Cowork / Claude Desktop (`.mcpb` bundle)

The `/plugin` install above is for the **Claude Code CLI**. The **Claude Desktop
app** (including **Cowork**) installs local MCP servers a different way: as a
`.mcpb` desktop-extension bundle. Cortex ships one so the `cortex-git` server
runs host-side and your PAT is supplied through Claude's own config UI - the same
model the Snyk extension uses.

**Install:**

1. Download `cortex-git_<version>_<os>_<arch>.mcpb` for your platform from the
   [GitHub release](https://github.com/cortex-sync/Cortex/releases).
2. In Claude Desktop, open the **Connectors** page and add the `.mcpb`.
3. When prompted, fill in the user config:
   - **Personal Access Token** (required, stored securely by Claude) - a PAT with
     read/write on your profile repo.
   - **Git host** (required, e.g. `github.com`) - the token is only ever offered
     to this host.
   - **Git username** (optional, defaults to `git`).
4. Connect a **git clone of your profile repo as a folder** so its root
   `CLAUDE.md` and `memory/` load. The skills (`/setup`, `/sync-profile`, etc.)
   then drive the server to commit/push/pull as usual.

The token is passed to the server via the `CORTEX_GIT_TOKEN` / `CORTEX_GIT_HOST`
/ `CORTEX_GIT_USERNAME` environment variables (see the manifest); it scopes to
the named host only and is never written to the transcript.

> **Building a bundle yourself:** `make mcpb` packs one for your host platform,
> or `make mcpb-all` packs every released target, into `dist/`. See
> [`scripts/pack-mcpb.sh`](../scripts/pack-mcpb.sh).

> **Managed/corporate machines:** desktop app-control (e.g. endpoint protection)
> may block the unsigned binary from running host-side. Until the Windows binary
> is code-signed, use the Claude Code CLI with host-side sync on those machines.

---

## Codex CLI

Cortex also runs on the **OpenAI Codex CLI**. Codex uses the same building blocks as
Claude Code - a global instruction file, stdio MCP servers, and `SKILL.md` skills - so
your profile carries over. Setup today is a small host-side step via
`scripts/install-codex.sh`. (Codex now also has a plugin/marketplace system, and Cortex
shipping as a native Codex plugin is planned - see `docs/TODO.md`; the script is the
supported route for now.) There are two tiers.

> **Coming from Claude Code?** On first run Codex may offer to *import your existing
> Claude setup*. You can decline that and use Cortex instead - the point of Cortex is a
> single **portable, Git-synced** profile that stays consistent across engines, rather
> than a one-time static copy living only in Codex. (Importing is harmless if you do it;
> Cortex's `AGENTS.md` is what Codex actually loads either way.) Just run the Tier 1 step
> below and Codex picks up your persona and memory.

### Tier 1 - profile consumer (recommended)

Codex loads your persona, working style, and memory; **sync stays host-side** (keep
running `/sync-profile` from Claude Code). No MCP server and no skills run inside Codex,
so there is **no network dependency**. (Verified on Codex 0.142.1: under the default
sandbox, memory reads work even when the profile repo sits outside the workspace - see
the note below.) This is the robust default.

You need your profile repo on disk. If it is not already cloned (e.g. from Claude Code),
clone it **without persisting your PAT**: `git clone https://<host>/<owner>/<repo>.git ~/cortex-profile`
and enter the token at the password prompt - don't put it in the URL (`https://user:token@host/...`
gets written into `.git/config` and your shell history), and make sure no `store` credential
helper is active. (If the `cortex-git` server is available, `set_credentials` + `git_clone`
is the sanctioned alternative - it keeps the PAT only in the credential store.) Tier 1 needs
only the files, not the MCP server. Then, from a Cortex checkout:

```shell
./scripts/install-codex.sh --profile-dir ~/cortex-profile
```

This copies your `adapters/codex.md` (the lean, Codex-native rendering of your profile,
generated by `/setup`) to `~/.codex/AGENTS.md` and appends a `## Cortex configuration` block
recording the profile repo path. Codex auto-loads `AGENTS.md`; your `memory/` files stay in
the profile repo, and that block is how the memory pointer (and a later Tier 2 `/sync-profile`)
resolves the repo path.

> **Memory reads under the sandbox:** verified on Codex 0.142.1 - under the default
> `workspace-write` sandbox Codex reads files **outside** the workspace (full-disk read;
> only writes are confined), so reading a profile repo at e.g. `~/cortex-profile` works
> with no extra setup. Only if you have tightened the sandbox to deny reads outside the
> workspace do you need a mitigation: grant the path read access in a permissions profile
> (`filesystem."/abs/path" = "read"`), launch Codex from within the profile directory, or
> keep the most important context inline in `AGENTS.md` (within Codex's ~32 KiB limit).

> **Secret-scan caveat:** the content scan that blocks committing a secret runs only on the
> `cortex-git` `git_commit_push` path (Tier 2, or any Claude-side sync). A Tier 1 host-side
> `git push` skips it - so sync via Claude Code (or Tier 2) when you want that gate.

### Tier 2 - native Cortex in Codex (advanced)

Run the `cortex-git` server and the Cortex skills *inside* Codex, so `/sync-profile`,
`/restore-profile`, etc. work natively:

```shell
./scripts/install-codex.sh --profile-dir ~/cortex-profile --with-mcp
```

`--with-mcp` registers the server (`codex mcp add cortex-git -- .../bin/cortex-git-launch.sh`)
and symlinks the four skills into `~/.agents/skills/`, where Codex auto-discovers them.
Your PAT stays in the credential store - it is **not** written into `config.toml`. (If you
ever must fall back to a token in `config.toml`, note it is plaintext there: `config.toml`
is not mode `0600`, and the value is also visible via `/proc/PID/environ`.)

**Prerequisite - allow the network (least privilege).** The server makes outbound HTTPS to
your Git host, and Codex blocks network by default. On current Codex (>= 0.14x), scope the
grant with a named permissions profile in `~/.codex/config.toml`:

```toml
[permissions.cortex]
extends = ":workspace"
network.enabled = true
network.domains = ["gitlab.com", "github.com"]  # just your Git host(s)
```

then select it with `default_permissions = "cortex"` (or pass `-P cortex`). Older Codex used
the legacy `[sandbox_workspace_write] network_access = true` form, which still works but is
deprecated. **`danger-full-access` also works but disables the *entire* sandbox** - all
filesystem-write and network confinement, for every command in the session - so treat it as a
session-scoped last resort, not the default.

> **Known issue - MCP tool calls cancelled under the sandbox.** Confirmed on Codex 0.142.1:
> invoking an MCP tool via `codex exec` is auto-cancelled ("user cancelled MCP tool call")
> across sandbox modes and approval settings - not yet verified in the interactive TUI (the
> normal day-to-day mode). If you hit it, prefer staying on **Tier 1** (host-side sync via
> Claude Code) over reaching for `danger-full-access`. If you'd rather not open the sandbox at
> all, stay on Tier 1.

### Windows

Codex runs natively on Windows (PowerShell). The POSIX launcher
(`bin/cortex-git-launch.sh`) won't run there, so point `command` directly at the
`cortex-git-server.exe` from the `.mcpb` bundle or the
[GitHub release](https://github.com/cortex-sync/Cortex/releases):

```toml
[mcp_servers.cortex-git]
command = "C:\\path\\to\\cortex-git-server.exe"
```

(A native PowerShell launcher with first-run download and checksum verification is a
planned enhancement; for now point at the release `.exe` directly.)

---

## Troubleshooting

### The `cortex-git` tools don't appear

The plugin looks loaded, but `set_credentials`, `git_clone`, and the rest aren't
available - so `/sync-profile` and `/restore-profile` report no usable git tools.
The MCP server binary never started, almost always because the launcher could not
obtain it. On first run `bin/cortex-git-launch.sh` fetches the prebuilt
`cortex-git-server` for the version in `bin/VERSION` from the matching GitHub
release; if that release (or its asset) isn't published, the download 404s and
the server exits before registering any tools. The launcher prints the URL it
tried on failure.

- **Installed from a source checkout or from `main`** - build the binary locally.
  The launcher prefers a local build over the download, so this takes effect
  immediately:

  ```shell
  cd mcp/git-server && make build   # needs Go; produces bin/cortex-git-server
  ```

  Then reload the plugin (`/reload-plugins`, or restart the session).
- **Installed a released version** - pick a version whose release assets exist,
  or report it if the tag in `bin/VERSION` has no published release.

**The binary itself is fine, but the tools still don't appear.** A different
(and more confusing) failure mode: `/reload-plugins` reports the server as
installed, a fresh install/restart doesn't help, but the tools are never
callable - and the binary turns out to be perfectly healthy. First, isolate
the binary/launcher from Claude Code's own plugin loading:

```shell
# from the installed plugin root, or a local checkout
bin/cortex-git-launch.sh --selftest
```

This fetches the binary if needed, sends it a synthetic MCP `initialize`, and
reports PASS/FAIL - if it prints the server's `serverInfo` and exits 0, the
binary and launcher are both working correctly and the problem is entirely on
Claude Code's side. Every run (of `--selftest`, `--prefetch`, or a normal
launch) also appends a timestamped line to `${CLAUDE_PLUGIN_DATA:-~/.cache/cortex}/launcher.log`,
so a launch that Claude Code never surfaces still leaves a trail - check it if
`--selftest` passes but tools still don't show up in a real session.

If `--selftest` passes and reloading/restarting still doesn't expose the
tools, this matches a known Claude Code issue with plugin-root `.mcp.json`
environment-variable expansion
([anthropics/claude-code#9427](https://github.com/anthropics/claude-code/issues/9427)) -
not something Cortex itself can fix. As an emergency fallback, the server can
be driven directly over stdio (newline-delimited JSON-RPC: `initialize` ->
`notifications/initialized` -> `tools/call`), using the same binary and
credential store the MCP tools would use:

```shell
printf '%s\n%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"manual","version":"1"}}}' \
  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  | "${CLAUDE_PLUGIN_DATA:-~/.cache/cortex}/bin/cortex-git-server-<version>-<os>-<arch>"
```

Then pipe further `tools/call` requests the same way. This is a stopgap for an
unresponsive host, not a fix - once the underlying Claude Code issue is
resolved, a normal reload should work again.

### `no credentials found for <host> - run set_credentials first`

No PAT is stored for that host. Run `get_auth_status <host>` to confirm (it also
tells you which backend is active), then store one via `set_credentials` or
re-run `/restore-profile`.

### Credentials won't save on WSL / headless Linux

On WSL2, containers, or headless Linux there's usually no OS keychain (no Secret
Service / `org.freedesktop.secrets`). Cortex automatically falls back to an
**encrypted file** at `~/.config/cortex/credentials.enc` - so this should "just
work". `get_auth_status` will report `backend: file`. If you'd rather use a real
keychain, run a desktop session with gnome-keyring/KWallet unlocked, and Cortex
will prefer it (`backend: keychain`). See [SECURITY.md](../SECURITY.md) for the
fallback's trade-offs.

### Clone fails on a brand-new repo

You cannot **clone** an empty repo (go-git limitation). First-run setup uses
`git_init` (initialise locally and push) instead - make sure you ran `/setup`,
not a manual clone, for a new profile.

### Push rejected / remote has diverged

Another device pushed since your last sync. Run `git_pull` then retry the sync.
Note `git_pull` is **last-write-wins** - it force-updates your local branch and
can overwrite local divergence, so check you're not discarding unsynced local
changes first.

### Which backend is storing my token, and where?

`get_auth_status <host>` reports `backend: keychain` or `backend: file`. The file
backend lives at `${XDG_CONFIG_HOME:-~/.config}/cortex/credentials.enc` (mode
`0600`, encrypted). If `CORTEX_CONFIG_DIR` is set in the server's environment it
overrides both: the file backend is forced (the keychain probe never runs) and
`credentials.enc` lives at `$CORTEX_CONFIG_DIR/credentials.enc` instead. When in
doubt, trust what `get_auth_status` reports - it reflects the live backend and path.

### A secret got blocked from syncing

That's the safety gate doing its job. Move the secret out of the profile repo,
or add it to the repo's `.gitignore`, then re-run `/sync-profile`.

### A sync, clone, or pull is taking too long

Network git operations (`git_clone`, `git_commit_push`, `git_pull`, and the
push inside `git_init`) are bounded by a timeout - **2 minutes by default** - so
a stalled remote or wedged connection can't hang a session indefinitely. They
also abort immediately if you cancel the tool call. If you hit the limit on a
genuinely large first clone over a slow link, raise it by setting
`CORTEX_GIT_TIMEOUT` to a Go duration (e.g. `CORTEX_GIT_TIMEOUT=5m` or `90s`) in
the MCP server's environment; an unset or invalid value uses the 2-minute
default.
