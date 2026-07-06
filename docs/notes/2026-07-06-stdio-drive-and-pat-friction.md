# Field findings - 2026-07-06 (sync friction, Claude Code / WSL2 Linux x86_64)

Notes from a real `/sync-profile` on an existing install (plugin v0.2.0, binary
already present and healthy). Two friction points: the MCP tools never reached the
session, and rotating an expired PAT was awkward. Neither is a code bug in the
server itself - both are integration / UX gaps.

## 1. cortex-git tools never reach the session, even with a healthy binary

**Symptom.** `/sync-profile` could not run: the `cortex-git` tools
(`git_status`, `git_commit_push`, `get_auth_status`, `set_credentials`, ...) were
not callable from the agent. `/reload-plugins` reported `1 plugin MCP server`, and
an earlier reconnect logged `Failed to reconnect to plugin:cortex:cortex-git:
ENOENT`. A full teardown did **not** fix it: `/plugin uninstall` -> `marketplace
remove` -> `marketplace add cortex-sync/Cortex` -> `install` -> `/reload-plugins`
-> **full session restart**. Tools still absent. `pgrep -af cortex-git-server`
found no running process despite the reload's "1 plugin MCP server" count.

**The binary is fine.** This is not the 2026-06-29 missing-asset blocker - v0.2.0
is published now. Verified directly:
- `bin/cortex-git-launch.sh --prefetch` -> `binary ready` (exit 0).
- Piping an MCP `initialize` into
  `~/.claude/plugins/data/cortex-cortex/bin/cortex-git-server-0.2.0-linux-amd64`
  returns `serverInfo: {name: cortex-git, version: 0.2.0}` and `tools/list`
  returns all 8 tools.

So the server works; the host (Claude Code) just is not spawning/exposing it to
the session, while still counting it in the reload summary.

**Suspected cause (unverified).** `.mcp.json` sets the command to
`${CLAUDE_PLUGIN_ROOT:-.}/bin/cortex-git-launch.sh`. Two candidates:
- the host may substitute `${CLAUDE_PLUGIN_ROOT}` but not honour the `:-.`
  shell-default form, yielding a bad path (matches the `ENOENT`); or
- the launched process exits fast enough that it is counted but never registers
  tools.
Not root-caused this session - flagged for follow-up.

**Workaround (what unblocked the sync).** Drive the server directly over stdio -
newline-delimited JSON-RPC piped into the binary. `initialize` ->
`notifications/initialized` -> `tools/call`. This uses the same binary and the
same credential store, so `git_status` / `get_auth_status` / `set_credentials` /
`git_commit_push` all work identically. A full commit + push of pending local
commits succeeded this way.

**Fix suggestions.**
- Verify the host expands `${CLAUDE_PLUGIN_ROOT:-.}`; if the default form is not
  honoured, use plain `${CLAUDE_PLUGIN_ROOT}/bin/...` (the plugin always has a
  root) so the path can't resolve to `./bin/...`.
- Have the launcher log startup/exit to a discoverable path so a silent
  non-registering exit is diagnosable (right now it looks like success).
- Add a self-test / health command, and document the stdio-drive as an emergency
  fallback in `docs/usage.md` troubleshooting.

## 2. Rotating an expired PAT is awkward and the failure is opaque

**Symptom.** The push failed with GitLab's generic
`authentication required: HTTP Basic: Access denied ... token incorrect, expired,
or improperly scoped` (and, via plain git, `could not read Username`). The stored
PAT (file backend, set ~4 weeks earlier) had simply **expired**.
`get_auth_status gitlab.com` reported `credentials found (user: ..., backend:
file)` - i.e. it confirms a credential *exists*, not that it is valid or
unexpired, so it gave false reassurance.

**Why rotation was painful.** `set_credentials` is only reachable through the MCP
tool. With the tools not exposed (issue 1), the only way to store a fresh PAT was
to **hand-drive `set_credentials` over stdio** via a throwaway script - reading
the token with `read -rs` and passing it by env var (never argv, never shell
history, never into the agent chat). Correct, but well beyond what a normal user
should need to rotate a token.

**Fix suggestions.**
- `get_auth_status` should do a lightweight validity probe against the host
  (authenticated no-op) and/or surface the token's expiry where the host API
  exposes it - and warn when expired / near expiry.
- On an auth-denied push, the error should explicitly suggest *"your stored token
  may be expired - run `set_credentials` to rotate"* rather than passing through
  the raw provider message.
- Provide a tool-independent way to set credentials that does not depend on the
  MCP tools being live - e.g. a `cortex-git-server set-credentials` subcommand or
  a shipped `scripts/set-credentials.sh` that does the `read -rs` + env-var
  pattern. Document it in `docs/usage.md` alongside the "don't paste PATs into the
  agent" guidance.
