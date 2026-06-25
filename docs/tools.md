# MCP tools reference

The `cortex-git` MCP server exposes the tools below over stdio. Skills call
these; you normally don't invoke them directly.

**Credential resolution.** Every tool that touches a remote resolves the git
host and looks up a PAT, then authenticates over HTTPS Basic auth
(`username` + token). The host is taken from the repo's `origin` remote
(`git_commit_push`, `git_pull`) or parsed from the supplied URL (`git_clone`,
`git_init`). Credentials come from, in order:

1. **Environment variables** - `CORTEX_GIT_HOST`, `CORTEX_GIT_TOKEN`, and
   optionally `CORTEX_GIT_USERNAME` (defaults to `git`). Set all of them in the
   server's environment (a Cowork local-MCP config, a `.mcpb` `user_config`,
   CI) and they take precedence over the store - but only for the named host:
   the token is never offered to any other host. A token without
   `CORTEX_GIT_HOST` is ignored with a warning on stderr.
2. **The credential store** - OS keychain, or the encrypted-file fallback on
   headless platforms, managed via `set_credentials` / `delete_credentials`.

If neither yields credentials for the host, the tool returns
`no credentials found for <host> - run set_credentials first, or set
CORTEX_GIT_HOST/CORTEX_GIT_TOKEN in the server environment`.

See [credential storage](../SECURITY.md#credential-storage) for where tokens live.

---

## `git_status`

List changed files in a profile repo.

| Arg | Required | Description |
|---|---|---|
| `repo_path` | yes | Absolute path to the local profile repo |

**Returns:** porcelain-style lines (`<staging><worktree> <path>`) for each
changed/untracked file, or `nothing to commit, working tree clean`.
**Errors:** path is not a git repository.

## `git_commit_push`

Stage all changes, commit, and push to `origin`.

| Arg | Required | Description |
|---|---|---|
| `repo_path` | yes | Absolute path to the local profile repo |
| `message` | yes | Commit message |

**Behaviour:** stages everything not gitignored, then runs a **server-side
secret scan** over the content of the changed files; if any high-signal
credential pattern matches (AWS keys, Azure keys, private-key blocks, GitLab/GitHub
PATs, JWTs, Slack tokens, Google API keys, generic `secret = "..."` assignments)
the commit is refused before anything is written or pushed. Otherwise it commits
as `Cortex <cortex@local>` and pushes. **Returns** one of: `committed and pushed:
<hash>`; `no file changes; pushed pending local commit(s)` (clean worktree but
unpushed local commits - still pushed); `nothing to commit, already up to date`;
or `nothing to commit, working tree clean` (no `origin` remote configured).
**Errors:** no `origin` remote, no credentials, push rejected (e.g. remote
diverged - see `git_pull`), or `refusing to commit: N potential secret(s)
detected` followed by the offending `file:line (rule)` list (the matched secret
value is never echoed). The scan is a low-false-positive backstop; for
exhaustive coverage layer an optional gitleaks pre-commit hook (see
[SECURITY.md](../SECURITY.md)).

## `git_pull`

Pull the latest from `origin`, **force-updating** the local branch.

| Arg | Required | Description |
|---|---|---|
| `repo_path` | yes | Absolute path to the local profile repo |

**Behaviour:** last-write-wins (`Force: true`) - can overwrite local divergence.
**Returns:** `pulled latest changes (reset to origin/<branch>)` or `already up to date`.

## `git_clone`

Clone an existing (non-empty) remote to a local path. Used by the restore flow.

| Arg | Required | Description |
|---|---|---|
| `remote_url` | yes | HTTPS remote URL |
| `local_path` | yes | Local path to clone into |

**Note:** cannot clone an *empty* remote - use `git_init` for first-run setup.

## `git_init`

Initialise a new repo, add the remote, commit the files already in
`local_path`, and push. Used by first-run setup against a freshly created
**empty** remote (because go-git cannot clone an empty remote).

| Arg | Required | Description |
|---|---|---|
| `local_path` | yes | Local path containing the profile files to commit |
| `remote_url` | yes | HTTPS URL of the pre-created empty repo |
| `message` | yes | Initial commit message |

**Behaviour:** `git init` with `main` as the default branch, adds `origin`,
commits, pushes with an explicit refspec. Idempotent (tolerates an existing
repo/remote). **Returns:** `initialised <path>, pushed <hash> to <url> (branch main)`.
**Errors:** nothing to commit (write the profile files first), no credentials.

## `get_auth_status`

Report whether a PAT is available for a host, and where it comes from.

| Arg | Required | Description |
|---|---|---|
| `host` | yes | Git host (e.g. `gitlab.com`) |

**Returns:** `credentials found for <host> (user: <u>, source: env)` when the
`CORTEX_GIT_*` environment variables cover the host, otherwise
`credentials found for <host> (user: <u>, backend: <keychain|file>)`
or `no credentials stored for <host> (backend: <...>)`. Never errors, and never
echoes the token.

## `set_credentials`

Store a PAT for a host in the credential store (OS keychain, or the
encrypted-file fallback on headless platforms).

| Arg | Required | Description |
|---|---|---|
| `host` | yes | Git host (e.g. `gitlab.com`) |
| `username` | yes | Git username |
| `token` | yes | Personal Access Token (repo write scope) |

**Returns:** `credentials stored for <host> (backend: <keychain|file>)`, with a
note appended when a `CORTEX_GIT_TOKEN` environment override is active for the
host (the environment takes precedence over the store).
**Security note:** the token is passed as a tool argument, so it passes through
the model context/transcript - see [SECURITY.md](../SECURITY.md#pat-handling).

## `delete_credentials`

Remove the stored PAT for a host, e.g. to rotate a token.

| Arg | Required | Description |
|---|---|---|
| `host` | yes | Git host (e.g. `gitlab.com`) |

**Returns:** `credentials removed for <host>`, with a note appended when a
`CORTEX_GIT_TOKEN` environment override remains active for the host (deleting
from the store does not clear the environment). Idempotent - succeeds even if
no credentials were stored for the host.
