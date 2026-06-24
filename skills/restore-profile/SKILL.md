---
name: restore-profile
description: Restore the Cortex profile on a new device. Clones the profile repo and places the instruction file (CLAUDE.md for Claude, AGENTS.md for Codex) and memory files in the correct locations for the current AI tool.
---

# Restore Cortex profile on a new device

You are restoring the user's Cortex AI profile to this device from their Git repository.

## Steps

1. Ask the user for:
   - Their profile repo HTTPS URL (e.g. `https://gitlab.com/username/cortex-profile.git`)
   - Their Git username
   - Their Personal Access Token (PAT) for that host
   - Where they want the local profile repo stored (default: `~/cortex-profile`)

2. Derive the host from the repo URL (the hostname, e.g. `gitlab.com`). Use `set_credentials` with that `host`, the `username`, and the `token` to store the PAT. The credential store is chosen automatically: the OS keychain where available, or an encrypted file fallback on headless/WSL platforms.

3. Confirm with `get_auth_status` for that host before proceeding - it should report the credentials and the active backend. If it does not, stop and re-check the inputs.

4. Use `git_clone` with `remote_url` and `local_path` to clone the repo. Credentials are resolved automatically from the store using the host parsed from the URL.

5. Detect the target AI tool and place the profile. Ask the user to confirm if unsure.

   - **Claude Code CLI** - copy `CLAUDE.md` from the cloned repo to `~/.claude/CLAUDE.md`.
   - **Cowork / Claude Desktop** - copy `CLAUDE.md` to `~/Documents/CLAUDE.md` (the connected Documents folder).
   - **Codex CLI** - Codex uses a different instruction file (`AGENTS.md`) and has two setup tiers. Follow the **Codex CLI wire-up** below instead of copying `CLAUDE.md`.

6. For **Claude Code CLI / Cowork**, add the following block to the `CLAUDE.md` you just placed, if not already present (this is how `sync-profile` later finds the repo). The Codex wire-up below adds the equivalent block to `AGENTS.md`.

```
## Cortex configuration

- Profile repo path: [local_path]
- Remote: [remote_url]
- Host: [host]
```

7. Report success: profile restored, the instruction file placed at `[path]`, and memory files available at `[repo_path]/memory/`. For Codex Tier 2, also report the MCP server and skills wired up.

## Codex CLI wire-up

Codex uses a global instruction file (`AGENTS.md`), can run stdio MCP servers, and auto-discovers `SKILL.md` skills. There are **two ways to run Cortex on Codex** - pick based on how much you want Codex to do itself. Steps 1-4 above (clone via the `cortex-git` tools) need the MCP server, so run this restore from **Claude Code** (or use `scripts/install-codex.sh` plus a manual clone) - then place the result onto Codex below.

Resolve the Codex home directory first: `$CODEX_HOME` if set, otherwise `~/.codex`. Create it if missing.

### Tier 1 - profile consumer (recommended)

Codex reads your persona, working style, and memory; **sync stays host-side** (run `sync-profile` from Claude Code, or `scripts/install-codex.sh`). No MCP server and no skills run inside Codex, so there is **no network dependency**. Note it is *not* fully sandbox-independent: memory reading depends on Codex's filesystem sandbox (step c) - verify that first, it is the cheapest, highest-value check.

a. **Get the profile repo on disk.** If steps 1-4 (Claude Code) already cloned it, skip this. On a Codex-only machine with no Claude Code, clone it directly with a plain `git clone <remote_url> <local_path>` (use your PAT for HTTPS auth). Tier 1 only needs the files locally - it does **not** need the cortex-git MCP server.

b. **Instruction file.** Copy `adapters/codex.md` from the repo to `$CODEX_HOME/AGENTS.md` (fall back to `adapters/generic.md` if `codex.md` is absent). Lean by design, for Codex's instruction-file size limit (`project_doc_max_bytes`, default 32 KiB). If `AGENTS.md` already exists, confirm before overwriting.

c. **Memory - verify this, it is Tier 1's load-bearing assumption.** `AGENTS.md` points Codex at the profile repo's `memory/` directory, which is usually **outside** Codex's workspace. Under the default `workspace-write` sandbox those reads may be blocked - so confirm Codex can actually read the files. If it cannot, one of these becomes **required** (not an optional fallback): add the profile path to Codex's trusted/allowed paths, launch Codex from within the profile directory, or inline the key memory into `AGENTS.md` within the size cap.

d. **Cortex config block.** Add the `## Cortex configuration` block (step 6) to `AGENTS.md` so a later in-Codex sync (Tier 2) can find the repo. Harmless under Tier 1.

That is all for Tier 1 - sync happens host-side and Codex re-reads the files each session.

### Tier 2 - native Cortex in Codex (advanced, opt-in)

Run `cortex-git` as an MCP server and the skills inside Codex, so `/sync-profile` etc. work natively. **Prerequisite - do this first or nothing works:** the server makes outbound HTTPS to your Git host, and Codex blocks network by default under `workspace-write`. The **preferred, least-privilege** fix is to allow network for the workspace (and allowlist just your Git host if you use `features.network_proxy`):

```toml
[sandbox_workspace_write]
network_access = true
```

`danger-full-access` also works but **disables the entire sandbox** - all filesystem-write and network confinement, for every command Codex runs in that session, not just the cortex-git server. Treat it as a session-scoped last resort, not the default. Heads-up: some Codex builds had a bug that cancelled MCP tool calls under `workspace-write`/`read-only` ("user cancelled MCP tool call") - reported on `0.125.0-alpha.3` and may already be fixed, so check whether your build is affected. If it is, **prefer staying on Tier 1** (host-side sync); reach for `danger-full-access` only as a temporary workaround. If you do not want to open the sandbox at all, stay on Tier 1.

d. **MCP server.** Easiest is the Codex CLI, which writes the `config.toml` entry for you:

   ```sh
   codex mcp add cortex-git -- <absolute path to bin/cortex-git-launch.sh in the Cortex checkout>
   ```

   Or add it by hand to `$CODEX_HOME/config.toml` (merge, do not clobber - only add if `[mcp_servers.cortex-git]` is absent):

   ```toml
   [mcp_servers.cortex-git]
   command = "<absolute path to bin/cortex-git-launch.sh>"
   ```

   **No `env` and no token are needed.** The launcher self-resolves its install root and binary cache, and the binary reads the PAT from the keychain / encrypted-file store that step 2 populated, keyed by the repo's host - single source of truth, no plaintext token on disk. *(Only if the credential store is genuinely unavailable on this machine should you add `env = { CORTEX_GIT_HOST = "[host]", CORTEX_GIT_USERNAME = "[username]", CORTEX_GIT_TOKEN = "[token]" }`. The token is then plaintext on disk - `config.toml` is **not** mode `0600`, and the value is also exposed via `/proc/PID/environ` to any process running as this user. Set `CORTEX_GIT_HOST` to exactly the repo's hostname (lowercase, no port) or the host-scoped lookup will miss. A partial env without the token does nothing: the binary ignores it and uses the store.)*

   On **native Windows** the POSIX launcher won't run - point `command` at the `cortex-git-server.exe` (from the `.mcpb` bundle or the GitHub release) instead. See `docs/usage.md` (Codex CLI > Windows).

e. **Skills.** Codex auto-discovers skills from `~/.agents/skills/` on startup (the `[[skills.config]]` table is only for *disabling* a discovered skill via `enabled = false`). The skills ship with the Cortex distribution, **not** the profile repo - locate the Cortex `skills/` directory (ask for the Cortex checkout/plugin path if unclear) and symlink each folder in (Codex follows symlinks, so a `git pull` in the checkout keeps them current):

   ```sh
   mkdir -p ~/.agents/skills
   ln -sfn <cortex>/skills/setup           ~/.agents/skills/setup
   ln -sfn <cortex>/skills/sync-profile    ~/.agents/skills/sync-profile
   ln -sfn <cortex>/skills/restore-profile ~/.agents/skills/restore-profile
   ln -sfn <cortex>/skills/promote-lessons ~/.agents/skills/promote-lessons
   ```

   Copying works too. Keep folder names matching each skill's `name:` frontmatter; rename on collision. Restart Codex to pick up newly discovered skills. `scripts/install-codex.sh` automates steps d and e.

## Notes

- The profile repo itself is the source of truth. The on-disk instruction file (`CLAUDE.md` or `AGENTS.md`) is a synced working copy.
- On Claude, sync-profile runs automatically on handoff. **Codex has no session-end hook**, so under Tier 1 sync is host-side and under Tier 2 it is run explicitly (`/sync-profile`).
- Never store PATs in files - they go to the OS keychain via `set_credentials`. The Codex `config.toml` deliberately carries no token for the same reason (the binary reads the store).
