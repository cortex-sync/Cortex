# Security

Cortex stores and syncs personal AI context (persona, memory, instructions) to
a Git repository, using a Personal Access Token (PAT) for transport. This
document describes how credentials and content are protected, the trade-offs we
have accepted, and how to report a problem.

## Reporting a vulnerability

Please **do not** open a public issue for a security problem. Instead, use
GitHub's private vulnerability reporting: open the repository's **Security** tab
and choose **Report a vulnerability**, or go straight to
[the advisory form](https://github.com/cortex-sync/Cortex/security/advisories/new).
The report is a private advisory visible only to the maintainers, so it stays
confidential until a fix ships.

We'll acknowledge and work a fix before any public disclosure.

## Credential storage

PATs are stored per-host under the `cortex` service name. A backend is selected
automatically at runtime:

| Platform | Backend | Notes |
|---|---|---|
| macOS / Windows | OS keychain | Keychain / Credential Manager |
| Linux with a desktop session | OS keychain | gnome-keyring / KWallet via Secret Service |
| Headless Linux / WSL2 / container / CI | Encrypted file | No Secret Service available |

Selection works by probing the OS keychain once: a working-but-empty keychain
selects the keychain backend; a "Secret Service unavailable" error falls back to
the file. `get_auth_status` / `set_credentials` report which backend is active.

Setting `CORTEX_CONFIG_DIR` in the server's environment overrides selection
entirely: the encrypted-file backend is pinned at
`$CORTEX_CONFIG_DIR/credentials.enc` and the keychain probe never runs, on every
platform. Intended for isolated E2E test runs and headless deployments that want
a deterministic store location; the file-backend security trade-offs above apply.

### Environment-injected credentials

The PAT can instead be supplied via `CORTEX_GIT_HOST` / `CORTEX_GIT_TOKEN` /
`CORTEX_GIT_USERNAME` in the server's environment, for hosts that manage the
secret themselves (a Cowork local-MCP config, a `.mcpb` `user_config`, CI).
Environment credentials take precedence over the store but are scoped to the
named host only - the token is never offered to any other host, and a token
without `CORTEX_GIT_HOST` is ignored. The token is then only as protected as
the environment that holds it: prefer the host platform's secret handling
(e.g. a `.mcpb` `sensitive` user_config field) over plaintext config files.

### Encrypted-file fallback

When no OS keychain is available, credentials are written to
`${XDG_CONFIG_HOME:-~/.config}/cortex/credentials.enc`:

- **AES-256-GCM**, with the random nonce prefixed to the ciphertext.
- File mode `0600`, written atomically (temp file + rename).
- The key is derived (SHA-256) from a machine-bound identifier
  (`/etc/machine-id`, or `CORTEX_MACHINE_ID` if set) mixed with the current
  user's id and a fixed domain string, so the file is **non-portable** and never
  stored as plaintext.

**Accepted limitation.** The key is derived automatically with no user
passphrase, so any process running as the same user on the same machine can
decrypt the file. This is **obfuscation-at-rest** - comparable to, and stronger
than, plaintext stores like `git-credential-store` or the `gh` CLI - **not** a
hardware-backed keychain. It exists so that WSL (a primary target, since Windows
users run Claude Code there) and CI work without per-machine keyring setup. A
passphrase mode for stronger at-rest protection is tracked in
[docs/TODO.md](docs/TODO.md).

**Environments with no machine id.** Some containers and minimal images ship
without `/etc/machine-id`. There the key derivation falls back to a guessable
value, so the store is **portable** - it could be decrypted off the machine -
and Cortex emits a one-time warning on stderr. To restore machine binding, set
`CORTEX_MACHINE_ID` in the server environment to a stable, secret value (e.g. an
injected platform secret), or use an OS keychain instead of the file backend.

## PAT handling

- **Minimum scopes.** Use the least privilege that allows repo read/write:
  - GitHub: `repo`
  - GitLab: `write_repository`
  - Azure DevOps: `Code: Read & Write`
- PATs are **never written to files** by Cortex (other than the encrypted store
  above) and are **never echoed back** in tool output.
- **Transport is HTTPS + PAT only.** No SSH. The `https://` scheme is enforced
  by `RequireHTTPS` (`internal/git`) on every path that sends the PAT, failing
  closed: `git_clone` and `git_init` validate the caller-supplied URL, and
  `git_commit_push` and `git_pull` re-validate the repo's stored origin before
  any credential is resolved. `http`, `file`, `git`, `ssh`, scp-style,
  schemeless, empty, and credential-embedding (`user:token@host`) URLs are all
  rejected, so a PAT can never travel over cleartext or an unexpected transport.

> **Caveat - tokens in the transcript.** `set_credentials` receives the PAT as a
> tool argument, which means the raw token passes through the model's context
> and is recorded in the Claude Code conversation transcript on disk. The store
> itself is safe, but the transcript is not. Treat a PAT entered this way as
> exposed to anything that can read your transcripts, and rotate it if that
> matters to you. A non-transcript entry path is tracked in
> [docs/TODO.md](docs/TODO.md).

## Protecting repo content

- **Secret scanning, two layers:** `gitleaks` runs as a local pre-commit hook
  (lefthook) **and** as a CI job that cannot be bypassed with `--no-verify`.
- **Profile `.gitignore`:** new profile repos are seeded from
  `profile-template/.gitignore`, which ignores `*.env`, `*.pem`, `*.key`,
  `*secret*`, `*credential*`, `*token*`, `*.tfstate`, etc. This matters because
  `git_commit_push` / `git_init` stage *all* non-ignored files.
- **Sync safety gate, two layers:** the `sync-profile` skill inspects the
  changed-file list and refuses anything matching sensitive *filename* patterns
  (first line of defence, at the LLM layer). Behind it, `git_commit_push`
  enforces a **server-side content scan**: it reads the body of every changed
  file and refuses the commit if a high-signal credential pattern matches (AWS
  access-key IDs and secret keys, Azure storage / Service Bus keys, private-key
  blocks including encrypted ones, GitLab/GitHub PATs, JWTs, and quoted *or*
  unquoted `secret = ...` assignments), reporting the offending `file:line
  (rule)` without ever echoing the secret value. A file the scanner cannot read
  in full - binary content, or larger than 5 MiB - is **failed closed**: the
  commit is blocked rather than letting the unscanned content through (add the
  path to `.gitignore` if it is intentional). This backstop holds even if the
  skill-level gate is skipped, and catches secrets pasted into a file's *text*
  that a filename check cannot. The ruleset targets common credential shapes and
  is tuned for low false positives, so it is not exhaustive.
- **Optional: gitleaks pre-commit hook on the profile repo (extra protection).**
  The built-in content scan is the always-on, zero-dependency baseline. Users who
  want gitleaks' comprehensive ruleset on their *profile* repo can add it as a
  local pre-commit hook in that repo - it complements, rather than replaces, the
  server-side gate:

  ```yaml
  # ~/your-profile-repo/.pre-commit-config.yaml
  repos:
    - repo: https://github.com/gitleaks/gitleaks
      rev: v8.21.2
      hooks:
        - id: gitleaks
  ```

  Then `pre-commit install` in the profile repo. Note this guards commits made
  with the `git` CLI directly; the MCP server's own commit path is always
  covered by the built-in scan regardless.
- **PII:** the setup and sync skills screen memory content for PII before
  committing. This is best-effort, at the LLM layer.

## Repository privacy

A **private** profile repo is strongly recommended and is the default the setup
skill steers towards. Because Cortex does not call host APIs, it cannot enforce
visibility - the user creates the repo in their host's web UI, so this is
advisory. Do not point Cortex at a public repo unless you intend the contents to
be public.

## Threat model summary

| Concern | Mitigation |
|---|---|
| PAT stolen from disk | OS keychain, or encrypted-file fallback (`0600`, machine-bound via `/etc/machine-id` / `CORTEX_MACHINE_ID`; portable with a warning where neither exists) |
| PAT in transcript | Documented caveat; rotate; non-transcript entry tracked |
| Secrets committed to the repo | Server-side content scan in `git_commit_push` (fails closed), filename sync gate, profile `.gitignore`, optional gitleaks pre-commit hook |
| Token over cleartext / odd transport | HTTPS + PAT only (`https://` scheme enforced, fails closed) |
| Content exposure | Private repo recommended; PII screening at skill layer |
