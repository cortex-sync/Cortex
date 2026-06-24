---
name: sync-profile
description: Sync the Cortex profile to Git. Commits any changed memory files and pushes to the remote. Run manually or called automatically on session handoff.
---

# Sync Cortex profile to Git

You are syncing the user's Cortex AI profile to their Git repository.

## Steps

1. Use `git_status` with `repo_path` to check what has changed in the profile repo.
   - If it reports "nothing to commit, working tree clean", report "Profile already up to date - nothing to sync." and stop.

2. **Safety gate (do not skip).** `git_commit_push` stages *every* changed and untracked file that is not gitignored - it does not filter. Before committing, inspect the file list from `git_status` and STOP if any path looks sensitive:
   - matches `*.env`, `*.pem`, `*.key`, `*.pfx`, `*secret*`, `*credential*`, `*token*`, `*.tfstate`
   - or is an unexpected file type for a profile repo (anything that is not markdown/text under `memory/` or `adapters/`)

   If anything matches, do not commit. Report the offending files and ask the user how to proceed (typically: add it to the profile repo's `.gitignore`, then re-run). The profile repo ships a `.gitignore` covering these patterns, but treat this gate as authoritative regardless.

   This filename check is your first line of defence. `git_commit_push` *also* enforces a server-side **content** scan (it inspects file bodies for credential patterns - AWS keys, private-key blocks, GitLab/GitHub PATs, JWTs, generic `secret = "..."` assignments) and refuses the commit if any are found, naming the offending `file:line`. That backstop catches a secret pasted into a memory file's text, which a filename check cannot - but do not rely on it as a substitute for this gate.

3. From the `git_status` file list, summarise what changed (which memory files were modified, added, or deleted).

4. Generate a concise commit message describing the changes. Format:
   `cortex: sync [date] - [brief summary of what changed]`
   Example: `cortex: sync 2026-06-03 - updated lessons.md with yor_trace clarification, active.md task updates`

5. Use `git_commit_push` with `repo_path` and `message`. Credentials and the remote host are resolved automatically from the repo's origin remote and the credential store - you do not pass them.

6. Report the result to the user: what was synced and the commit message used.

## Configuration

The profile repo path is stored in the user's instruction file under `## Cortex configuration`:
`CLAUDE.md` on Claude (`~/.claude/CLAUDE.md` or `~/Documents/CLAUDE.md`), or `AGENTS.md` on
Codex (`$CODEX_HOME/AGENTS.md`, default `~/.codex/AGENTS.md`). Check whichever applies to the
current tool. If not found, ask the user for the path and suggest they add the block to that file.

## Error handling

- If `git_commit_push` reports no credentials for the host: run `get_auth_status` for that host to confirm, then suggest `/restore-profile` or storing a PAT via `set_credentials`. `get_auth_status` also reports which backend (keychain or file) is active, which is useful when diagnosing a headless/WSL setup.
- If push fails due to a remote conflict: run `git_pull` first, then retry `git_commit_push`. Note `git_pull` is **last-write-wins** (it force-updates the local branch), so it can overwrite local divergence - mention this to the user before retrying if their local changes are substantial.
- If `git_commit_push` reports "refusing to commit: potential secret(s) detected", the server-side content scan blocked it. Report the named `file:line` and rule to the user; do not attempt to bypass it. Resolve by removing the secret from the file (rotate it if it was real - treat any committed-then-removed secret as compromised) or adding the path to `.gitignore`, then re-run.
- The safety gate in step 2 is the rule for what must never be committed - do not bypass it.
