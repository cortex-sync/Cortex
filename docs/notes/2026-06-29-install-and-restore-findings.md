# Field findings - 2026-06-29 (clean install re-test, Claude Code / Linux x86_64)

Informational triage notes from a real install + restore-profile attempt. Two
issues, one a hard blocker.

## 1. v0.2.0 is non-functional from a clean install - MCP server has no binary

**Symptom.** After `/plugin marketplace add cortex-sync/Cortex`,
`/plugin install cortex@cortex`, `/reload-plugins`, the `cortex-git` MCP server's
tools (`set_credentials`, `git_clone`, `git_commit_push`, `git_status`,
`git_pull`, `get_auth_status`) never register. `sync-profile` and
`restore-profile` therefore cannot run - they report no usable git tools.

**Root cause.** `bin/cortex-git-launch.sh` resolves the server binary by
downloading the GitHub release asset for the tag in `bin/VERSION` (`v0.2.0`).
That release has **no `cortex-git-server` asset**, so the launcher fails:

```
cortex-git launcher: fetching cortex-git-server_0.2.0_linux_amd64.tar.gz (v0.2.0)...
curl: (22) ... 404
cortex-git launcher: download failed:
  https://github.com/cortex-sync/Cortex/releases/download/v0.2.0/cortex-git-server_0.2.0_linux_amd64.tar.gz
```

With no release asset, no `~/.cache/cortex` cache, and no dev build at
`mcp/git-server/bin/cortex-git-server`, the MCP server command exits non-zero ->
the server never starts -> no tools. From the user's side it looks like the
plugin "loaded" (reload reports the MCP server) but silently does nothing.

**Reproduce.** Clean machine (no `~/.cache/cortex`, no dev binary) -> install ->
tools absent. Confirmed `bin/VERSION` = `v0.2.0`.

**Fixes.**
- **(a) Publish the release binaries + `checksums.txt`** for the tag in
  `bin/VERSION`. This is the real fix - the plugin is unusable for end users
  without it. Note the launcher fail-closes on SHA-256, so `checksums.txt` must
  ship alongside the archives.
- **(b) Dev fallback** (what unblocked this session): `cd mcp/git-server &&
  make build` (Go 1.26 fine) produces `mcp/git-server/bin/cortex-git-server`,
  which the launcher prefers over the download. Then `/reload-plugins`. Verified
  the built binary speaks MCP (`initialize` -> `serverInfo: cortex-git`).

**Suggestion.** A missing binary currently surfaces only as "tools don't exist."
Consider a louder install-time / SessionStart check (the launcher already has
`--prefetch`), and/or documenting the `make build` step in `docs/usage.md`'s
install section as the stop-gap until release assets are automated.

## 2. restore-profile clobbers a non-empty local CLAUDE.md - no merge path

This is the uncovered cell already sketched in `docs/profile-merge-sketch.md`
(PR #21), hit live this session.

**What happened.** restore-profile Step 5 for Claude Code is effectively
`cp <repo>/CLAUDE.md -> ~/.claude/CLAUDE.md`, gated only by a
confirm-before-overwrite prompt. There is no merge. The live case was an existing
**153-line local `~/.claude/CLAUDE.md`** (substantial personal rules) plus an
existing remote profile repo - so the only outcomes the skill offers are
**clobber or abort**. The user explicitly wanted a *merge*, which the skill
cannot do.

**Recommendation** (matches the merge sketch): when a non-empty local instruction
file exists, restore-profile should:
1. Back it up (`CLAUDE.md.bak`) first.
2. Merge rather than copy: preserve the local rules, fold in the profile's
   persona / working-style sections + the `## Cortex configuration` block, and
   surface genuine conflicts for the user to resolve.
3. Fall back to a straight `cp` only when there is no meaningful local file.

Per the sketch this is achievable with the existing MCP git tools (no Go
changes). The restore-profile skill text should also be updated - today it
instructs "the user's current CLAUDE.md is being replaced by the synced copy,"
which is the behaviour we want to change.

---
*Captured from a Claude Code session, 2026-06-29. Informational - not a code
change.*
