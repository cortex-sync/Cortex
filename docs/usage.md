# Using Cortex

Cortex keeps your AI profile (persona, instructions, memory) in a Git repo so it
follows you across devices and AI tools. This guide covers day-to-day use and
troubleshooting.

> **Install:** add the marketplace, then install the plugin:
>
> ```shell
> /plugin marketplace add LucasSymons/Cortex
> /plugin install cortex@cortex
> ```
>
> The plugin downloads its prebuilt MCP-server binary on first run (needs `curl`,
> `tar`, and `sha256sum`/`shasum`; on Windows, run under WSL). See the
> [README](../README.md#installation) for details.

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
   [GitHub release](https://github.com/LucasSymons/Cortex/releases).
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

## Troubleshooting

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
`0600`, encrypted).

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
