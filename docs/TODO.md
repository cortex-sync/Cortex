# Cortex - roadmap

**Status (2026-07-03):** **v0.2.0 is the latest release** (tagged + published
2026-07-01; v0.1.1 and v0.1.0 before it), publicly installable
(`/plugin marketplace add cortex-sync/Cortex` -> `/plugin install cortex@cortex`).
The release carries the env-credentials server change + the 5 `.mcpb` Cowork
bundles; the clean-install launcher fetch + SHA path is verified end-to-end.
GitHub Actions CI, Dependabot, and the goreleaser release pipeline are all green.

**Next up:**
- **Cowork `.mcpb` hands-on test (post-release fast-follow):** v0.2.0 shipped the
  `.mcpb` bundles WITHOUT a hands-on Cowork/Claude Desktop install test (pushed
  forward without it). Install a `cortex-git_0.2.0_*.mcpb` on an unmanaged machine
  and confirm it works; cut v0.2.1 if broken. See `## Cowork support`.
- **v0.3 Authenticode signing** for managed hosts - decided: **SignPath Foundation**
  (see `docs/CODE_SIGNING.md`). See `## Publishing / install`.
- **Community marketplace** submission still queued (manual,
  `clau.de/plugin-directory-submission`).

Open items, grouped by theme. Each becomes a branch + PR.

## Gap review (2026-07-03)

Findings from a 4-lens multi-agent gap review (Go server, CI/release/supply chain,
skills/adapters, docs), each adversarially verified before inclusion; the two HIGH
credential items were additionally re-verified by hand against the code. Overall
verdict: **repo is in solid shape** (0 open Dependabot/CodeQL alerts, all Actions
SHA-pinned, version strings agree, v0.2.0 fully published) - these are the gaps that
survived. Highest-leverage cluster first.

### Credential misdirection (NEW - not covered by M1/M2/store-key; do as one host/URL-validation pass)

- [x] **(done 2026-07-03, branch `fix/origin-url-validation`) (high) `git_init` on a
      pre-existing repo pushes to the OLD origin, sending
      the wrong host's PAT to an unvalidated URL.** `internal/git/git.go:232-237`
      tolerates `gogit.ErrRemoteExists` without checking the existing origin URL, then
      pushes to the default "origin" (`git.go:269`) - whatever is in `.git/config` -
      while `gitInitHandler` (`cmd/server/main.go:241-246`) ran `RequireHTTPS` +
      `resolveCreds` against the *argument* `remote_url`. A prompt-injected
      `git_init(local_path=<repo whose origin is http://attacker/x>, remote_url=https://gitlab.com/u/r.git)`
      resolves the gitlab PAT and BasicAuth-pushes it to the attacker host in cleartext,
      bypassing the fail-closed design. **Fix:** on `ErrRemoteExists`, require the
      existing origin URL to equal `remoteURL` (or pass it through `RequireHTTPS` and
      require host equality with the resolved credential host); error otherwise.
      Bundle with M1's "refuse non-empty pre-existing dir".
- [x] **(done 2026-07-03, branch `fix/origin-url-validation`) (high) `RemoteHost`
      validates `URLs[0]` but go-git pushes to the LAST URL of a
      multi-URL origin.** `git.go:302` returns `RequireHTTPS(urls[0])`; go-git v5.19.1
      uses `URLs[0]` for fetch but `URLs[len-1]` for push (verified against module
      source). An origin with URLs `["https://gitlab.com/...", "http://attacker/..."]`
      (one `git remote set-url --add` away, or a crafted repo reached via M1) passes the
      gate on URL 0, then `git_commit_push` sends the PAT to URL 1 in cleartext. Pull is
      unaffected. **Fix:** validate *every* configured origin URL and require all hosts
      to match, or reject origins with more than one URL outright (profile repos never
      need multi-URL remotes). Add a regression test.

### Secret-scan gate coverage (NEW)

- [ ] **(medium) `InitAndPush` skips the secret-scan gate entirely.** `git.go:243-273`
      stages `All:true`, commits, and pushes with no `secretscan.ScanFiles` call - only
      `CommitAndPush` (`git.go:98-108`) has the gate. Contradicts the secretscan package
      doc (`secretscan.go:2-4`). First-run setup (the op that publishes a whole directory
      sight-unseen) pushes an accidental `.env`/pasted token unscanned. **Fix:** run the
      scan over the staged paths in `InitAndPush` before `wt.Commit`.
- [ ] **(medium) A blocked commit leaves the secret staged - the error's own remediation
      is a dead-end.** `CommitAndPush` runs `AddWithOptions{All:true}` (`git.go:62`)
      before the scan (`git.go:102-107`); go-git writes the index + the secret's blob to
      `.git/objects` immediately. Verified: after a block the file stays `staging=A`, so
      adding it to `.gitignore` and retrying (what `BlockedError` advises,
      `secretscan.go:96-97`) still re-blocks forever, and no unstage/reset tool exists.
      **Fix:** scan *before* staging (derive paths from a pre-add `wt.Status()`), or
      unstage flagged paths on block; reword the error.
- [ ] **(low) The commit message is never secret-scanned.** The gate covers file contents
      only; a token pasted into `message` (`main.go:189`) reaches the remote in the commit
      header. **Fix:** run the ruleset over the message in `CommitAndPush`/`InitAndPush`.
- [ ] *(info)* After a blocked commit the secret's blob persists in `.git/objects` even if
      the file is later edited - at-rest residue in the local profile repo nothing cleans up.

### Tool-surface hardening (NEW)

- [ ] **(medium) No schema/required-arg validation before handlers run - the `stringArg`
      comment is false.** `main.go:136-141` claims "Required arguments are enforced by the
      tool schema"; mcp-go v0.18.0 `handleToolCall` calls the handler with no arg checking
      (verified). So `git_commit_push` with a missing `message` commits + pushes an
      empty-message commit; missing paths flow as `""` into go-git; `set_credentials` with
      no token stores an empty token. **Fix:** validate non-empty required args per handler
      (or `req.RequireString`), and fix the comment.
- [ ] **(low) `keyringStore.Set` is a non-atomic two-entry write.** `keyring_store.go:17-25`
      - if the username write succeeds and the token write fails, a later `Get` returns a
      backend error (not `ErrNotFound`), so `get_auth_status` reports a scary failure and
      sync is blocked until a manual `delete_credentials`. **Fix:** best-effort delete the
      username on token-write failure, or store both as one JSON value under a single key.
- [ ] **(low) `Status` output is nondeterministically ordered** (map iteration,
      `git.go:43-45`) - noisy for diffing tool output across calls. **Fix:** sort paths.
- [ ] *(cleanup)* `internal/hostapi/` is an **empty directory** (no files) - dead artefact;
      remove it or document what it holds a place for.
- [ ] *(fold into L4/store-key host-normalisation)* `CORTEX_GIT_HOST` with a port can never
      match - lookup hosts come from `url.Hostname()` (port stripped), so
      `CORTEX_GIT_HOST=git.example.com:8443` is silently dead. Strip the port in the canon
      or document "hostname without port".

### Test-coverage gaps (floor 75%, actual ~79.8% - this is where the missing ~20% is)

- [ ] `git_commit_push` happy path has **no in-process test** (acknowledged
      `handlers_test.go:105-107`; only the tag-gated e2e covers it - CI must actually run e2e
      for it to count). Also untested: `resolveCreds` "could not read credentials" arm
      (`main.go:159-160`) and `getAuthStatusHandler` error arm (`main.go:270`) - both
      drivable via `keyring.MockInitWithError`; `Pull` detached-HEAD guard
      (`git.go:150-152`); `envcreds` warn-once emission; `fileStorePath()` fallback chain;
      `keyringStore` partial-failure paths. No regression test yet for any NEW finding above.

### Release pipeline (NEW - highest-leverage before v0.3)

- [ ] **(high) The version gate runs AFTER the release is already public.** `.goreleaser.yaml:75`
      has `draft: false`, so goreleaser publishes a live release, then `release.yml:50-55`
      checks `mcpb/manifest.json` vs the tag. A mismatch or any `.mcpb`-step failure ->
      public partial release (binaries + checksums live, no `.mcpb`, tag burned). **Fix:**
      run the version check as the first release-job step, and/or `draft: true` with an
      explicit publish at the end.
- [ ] **(medium) Release is gated only on e2e, not on CI.** `release.yml:25` is `needs: e2e`
      only; nothing blocks the release on lint/unit-tests/coverage/gosec/govulncheck/gitleaks,
      and `codecov.yml:5-7` notes there are no required status checks - so a tag on a red
      commit ships. **Fix:** gate the release on CI too (required checks or `needs:`).
- [ ] **(medium) Only 1 of 3 version files is checked at release.** `release.yml:50-55`
      validates `mcpb/manifest.json`; `bin/VERSION` + `.claude-plugin/plugin.json` are a
      manual checklist (`RELEASING.md:30-33`). A stale `bin/VERSION` ships a plugin whose
      launcher fetches the previous release's binary - green pipeline, wrong artifact.
      **Fix:** cheap CI check that all three equal the tag.
- [ ] **(low) The lefthook gofmt hook is a no-op.** `lefthook.yml:6` runs `gofmt -l` which
      exits 0 even when it lists unformatted files (verified). **Fix:** use the
      `test -z "$(gofmt -l ...)"` pattern `ci.yml:29-31` already uses.
- [ ] **(low) Release job's workflow-level `contents: write` leaks into the e2e job**
      (`release.yml:8-9`), which only needs read and runs third-party containers with the
      write-scoped token in `.git/config` (no `persist-credentials: false` anywhere).
      **Fix:** move `permissions:` down to the `release` job; give e2e `contents: read`.
- [ ] **(low) No recovery path for a partial release.** `RELEASING.md` documents the happy
      path only; re-running re-runs `goreleaser release --clean` against an existing
      release/tag (untested). Given the gate-ordering item makes this reachable, add a
      "recovering a failed release" section or make the job idempotent.
- [ ] **(low) e2e never runs between releases** (`e2e.yml:6-7` is `workflow_dispatch` only);
      rot (Gitea image drift, Docker Hub availability) surfaces exactly when it blocks a
      release. **Fix:** run on PRs touching `e2e/` or `mcp/git-server/`, or a weekly schedule.
- [ ] **(low) `make validate` is much weaker than CI but `RELEASING.md:34` treats it as the
      pre-tag gate.** `mcp/git-server/Makefile:24-27` omits the gofmt check, the 75%
      coverage floor, gosec, govulncheck, gitleaks, `make test-launcher`, mcpb checks. **Fix:**
      enrich `validate` or amend the checklist.
- [ ] *(chore)* Go version hardcoded in 6 places (ci.yml env + release.yml x4 + e2e.yml +
      codeql.yml, all `1.26.4`); Dependabot can't bump them or the go-installed tools. **Fix:**
      a single `go-version-file` source.
- [ ] *(low)* Launcher first-run race on the fixed temp name `"$bin.tmp"`
      (`bin/cortex-git-launch.sh:99-101`): the SessionStart prefetch hook and the MCP launch
      both fire at session start, so two concurrent first-runs collide (spurious `set -e`
      failure; integrity unaffected). Superseded versioned binaries are also never pruned
      (unbounded cache growth). **Fix:** `mktemp` in `$bin_dir` / per-PID suffix; prune old.
- [ ] *(low)* goreleaser `before: go mod tidy` (`.goreleaser.yaml:9`) can silently build
      drifted deps at release time. **Fix:** `git diff --exit-code` after tidy in the job.
- [ ] *(minor)* lefthook: trailing-whitespace matches only spaces not tabs; several checks
      (em-dash, eof, line-endings, json/yaml) have no CI backstop so `--no-verify` bypasses
      them permanently; `make hooks-install` installs only lefthook (gitleaks/cz/PyYAML/perl
      assumed present -> confusing fresh-clone failures); `.gitleaks.toml:3` comment says CI
      runs `gitleaks detect` but ci.yml runs `gitleaks git`; ci.yml double-runs same-repo PRs.

### Skills - data-loss traps (NEW)

- [x] **(done 2026-07-03, branch `fix/path-confinement-and-pull-gate`) (high) `sync-profile`'s conflict recovery is a guaranteed data-loss recipe.**
      `skills/sync-profile/SKILL.md` Error handling says on push rejection "run `git_pull`
      first, then retry `git_commit_push`". But `CommitPush` commits locally first, and
      `Pull` is fetch + `HardReset` to origin - so the pull discards the just-made commit and
      the retry finds a clean tree; the delta is lost with certainty. (The destructive pull
      is M2; the skill *recommending this sequence* is the distinct new hole.) **Fix:** change
      the recovery to a non-destructive path, or gate on M2's safe-pull work.
- [ ] **(high) Codex reconcile folds back to the wrong file.** restore-profile Tier 1 step b
      reconciles a differing `AGENTS.md`, but the reconcile procedure's step 5 hardcodes
      writing the merged `CLAUDE.md` to `[local_path]/CLAUDE.md` - for Codex the merged result
      should land in `adapters/codex.md`. As written it clobbers the repo's `CLAUDE.md` with
      AGENTS-shaped content or the fold-back never happens. **Fix:** branch the write target by
      platform; extend `docs/profile-merge-sketch.md` to the AGENTS.md cell.
- [ ] **(medium) `install-codex.sh` re-run clobbers a newer non-managed `AGENTS.md`.**
      `backup_existing` (lines 152-157) returns early when `$dest.cortex-bak` exists, then
      `cp "$src" "$dest"` (line 167) overwrites a user-edited/reconciled AGENTS.md with no fresh
      backup. `--uninstall` (108-116) similarly restores over a marker-managed-but-user-edited
      file without saving it. **Fix:** detect content drift before overwrite; save-before-restore.

### Skills - flow holes (NEW unless noted)

- [ ] **(tracked, still open) Memory path resolution on Claude Code CLI** - the relative
      `memory/` reference when `CLAUDE.md` lives at `~/.claude/CLAUDE.md`. Confirmed nothing in
      the skills addresses it; ironically the Codex adapter already solved it with a `## Memory`
      pointer via the config block - port that to the Claude branch.
- [ ] **(new) `promote-lessons` / `sync-profile` contradiction.** promote-lessons says it runs
      "as part of sync-profile on session handoff", but sync-profile's steps never invoke it.
      promote-lessons also never says how to locate the top-level `memory/lessons.md` (same
      resolution gap as above). **Fix:** either wire the call into sync-profile or correct the
      prose; add the repo-path lookup.
- [ ] **(new) Empty-remote clone unhandled in two flows.** go-git can't clone an empty remote
      (`ErrEmptyRemoteRepository`). (a) restore-profile step 4 gives no guidance when the remote
      exists but is empty; (b) setup Section 0's adopt branch routes an empty-remote "I already
      have a repo" answer to `git_clone` (fails) instead of the `git_init` path. **Fix:** detect
      empty-remote and route to init.
- [ ] **(new) Platform-detection asymmetry.** setup Section 0 checks
      `~/OneDrive/Documents/CLAUDE.md` on Windows Cowork; restore/sync mention only
      `~/Documents/CLAUDE.md` - on an OneDrive-redirected box restore places/reconciles at the
      wrong path. setup Section 0 also never checks `$CODEX_HOME/AGENTS.md` for an existing
      Codex-first profile. **Fix:** align the path lists across all three skills.
- [ ] **(new) Tier 2 sandbox instructions drift.** `install-codex.sh` (255-268) teaches the
      current `[permissions.<name>]` profile form; restore-profile Tier 2 (SKILL.md 87-94) still
      shows only the legacy `[sandbox_workspace_write] network_access = true`. **Fix:** update the
      skill to match the script.
- [ ] *(minor)* `install-codex.sh` (189-193) drops a nonstandard port from the recorded Remote
      (`host="${host%%:*}"` reused to rebuild the URL) though the comment says only userinfo is
      stripped; safety-gate pattern drift (`*.pfx`/`*.p12` present in some replicated gate lists,
      absent in others - point at the sync-profile gate instead of re-enumerating); reconcile
      step 5 self-contradiction (writes the merged file to the tree before asking about push, so
      "declining leaves the repo as it was" is false - next sync commits it); setup adopt-path
      `git_pull` lacks restore's last-write-wins warning; `profile-template/adapters/` is an empty
      untracked dir so a fresh clone lacks it (skills generate adapters from prose - works, but
      setup step 4's "use `profile-template/.gitignore` as the source" has no resolvable path).

### Docs refresh (NEW - stale since the v0.2.0 cut)

- [ ] **(this file - partly done)** Top status block corrected 2026-07-03; still stale:
      Cowork sub-task (iv) (lines ~341-343) says the work "awaits Lucas's hands-on test before
      tagging v0.2.0" (merged + tagged without it); the Codex live-validation addendum note
      "`bin/VERSION` must point at a real release" is now satisfied - mark resolved; the
      Publishing Authenticode item still lists 4 options with SignPath as "front-runner" whereas
      `docs/CODE_SIGNING.md` records it as the decision (point to the doc).
- [ ] **`docs/RELEASING.md:66-83`** "Current state" says the Cowork test is a gate before tagging
      - v0.2.0 shipped 2026-07-01 without it. Update to "released; Cowork `.mcpb` test still open"
      or delete (the checklists above are the durable content).
- [ ] **`CONTRIBUTING.md`** line 79/98-100 "not published to a marketplace yet" (false since
      v0.1.0, contradicts README) and line 55 "70% coverage floor" (CI is 75%). Reword both.
- [ ] **`SECURITY.md`** threat-model table line ~164 still says the content scan "fails closed"
      - PR #20 dialled this back to best-effort; reword to "(best-effort content scan)". Lines
      101-102/163 reference a tracked "non-transcript entry path" TODO item that does not exist -
      add it or drop the "is tracked" claim.
- [ ] **`docs/notes/2026-06-29-install-and-restore-findings.md`** - both findings are resolved
      (release fixed the clean-install blocker; reconcile implemented per profile-merge-sketch)
      but the note reads as "current release is broken". Add a two-line "Resolved 2026-07-01"
      addendum at the top.
- [ ] *(minor)* No "newly shipped, report issues" caveat on the Cowork `.mcpb` path in
      README/usage.md (it shipped untested); no CHANGELOG / releases-page pointer in the docs set.

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
- [ ] **(found 2026-06-24, adversarial review) Credential store key is byte-exact while
      resolution canonicalises via `url.Hostname()`.** `set_credentials` stores under the
      raw `host` arg (`cmd/server/main.go` `setCredentialsHandler` -> `keychain` ->
      `file_store.go` / `keyring_store.go`, exact-string keyed), but lookups key on
      `RequireHTTPS` -> `u.Hostname()` (`internal/git/git.go`), which strips the port and
      does not lowercase. So a `host:port` remote (or a case / trailing-dot mismatch)
      stores and resolves under different keys -> `ErrNotFound`, breaking the "command-only
      Codex block just works" guarantee for clone/pull/push/init. Pre-existing (not
      Codex-only); bounded because github/gitlab use no port. **Fix:** canonicalise the
      host at the store boundary the same way lookup does (lowercase + trim + strip
      trailing dot + strip port), or have `set_credentials` accept the remote URL and run
      it through `RequireHTTPS` so the stored key == the resolve key. Add a regression
      test. Relates to L4 (env path) - do both as one host-normalisation pass.
- [ ] *(info, optional)* enforce `0700` on the credentials dir when it pre-exists
      (the `os.MkdirAll(dir, 0o700)` in `fileStore.save`,
      `internal/keychain/file_store.go:183-184`); add a regression test asserting no
      credential-handler output ever contains the token (PAT-in-logs verified clean
      today - keep it that way).

### Git operations / tool surface

- [x] **(done 2026-07-03, branch `fix/path-confinement-and-pull-gate`) M1 (medium) - all path-taking git tools accept arbitrary, unvalidated paths.**
      `cmd/server/main.go:168-258`. Model-supplied `repo_path`/`local_path` go straight
      to go-git with no `Clean`/`Abs`, no allowlist, no confinement. A prompt-injection
      can point ops outside scope (`git_status` freely reachable for path disclosure).
      **Fix:** confine all path args to a configurable root (e.g. `CORTEX_REPO_ROOT`),
      reject relative paths and anything resolving outside it after `Abs` +
      `EvalSymlinks` - mirror the `os.Root` confinement already used in `secretscan`.
      `git_init` also reuses an existing repo and stages `All:true` - refuse a
      non-empty pre-existing dir unless it is the expected profile repo.
- [x] **(done 2026-07-03, branch `fix/path-confinement-and-pull-gate`) M2 (medium) - `git_pull` does a destructive `HardReset` with no gate.**
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
- [x] **(done 2026-06-25, branch `fix/supply-chain-pinning`) M10 (medium) - all GitHub
      Actions pinned by mutable tag, not commit SHA.** Every `uses:` across
      `.github/workflows/{ci,release,codeql,e2e}.yml` is now pinned to a full 40-char
      commit SHA with the resolved version in a trailing comment: `actions/checkout`
      `df4cb1c` (v6.0.3), `actions/setup-go` `924ae3a` (v6.5.0),
      `golangci/golangci-lint-action` `82606bf` (v9.2.1), `codecov/codecov-action`
      `fb8b358` (v7.0.0), `github/codeql-action/{init,analyze,upload-sarif}` `8aad20d`
      (v4.36.2), `goreleaser/goreleaser-action` `5daf1e9` (v7.2.2). Dependabot already
      bumps `uses:` so the comments stay current. SHAs resolved via the GitHub API.
- [x] **(done 2026-06-25, branch `fix/supply-chain-pinning`) M11 (medium) - gitleaks
      binary downloaded with no integrity verification.** `.github/workflows/ci.yml` -
      added a `GITLEAKS_SHA256` env (the upstream
      `gitleaks_8.30.1_checksums.txt` value for `linux_x64.tar.gz`) and a
      `sha256sum -c -` check between the `curl` and the `tar`/`install`, so a tampered
      or corrupt tarball fails the job before it runs as the secret-scanning gate. Bump
      in lockstep with `GITLEAKS_VERSION`.
- [x] **(done 2026-06-25, branch `fix/supply-chain-pinning`) L1 (low) - lefthook
      installed via `go install ...@latest`.** Root `Makefile` - added
      `LEFTHOOK_VERSION := v2.1.9` and pinned the `hooks-install` `go install` to it,
      consistent with the already-pinned golangci-lint/gosec/govulncheck.
- [x] **(done 2026-06-25, branch `fix/supply-chain-pinning`) L5 (low) - e2e Gitea image
      on a mutable Docker Hub tag.** `e2e/Dockerfile` - `FROM gitea/gitea:1.26` now pins
      the manifest digest `@sha256:8e25c717...c1f39` (tag kept for readability), so a
      re-pushed `1.26` tag can no longer change release-blocking e2e behaviour.
- [x] **(done 2026-06-25, branch `fix/supply-chain-pinning`) L6 (low) - e2e TLS private
      key world-readable.** The gitignore half is already satisfied (`e2e/certs/` is in
      `.gitignore`). The `chmod 644` in `e2e/gen-certs.sh:36` is **deliberately kept** -
      the in-place comment documents that the unprivileged Gitea container user must read
      the key through the local bind mount regardless of host UID, and `640`+shared-group
      is not reliably portable across CI/host UIDs. The key is a disposable localhost-only
      self-signed cert, so the residual exposure (other local users mid-run) is accepted
      rather than risk the e2e run.

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

**Distribution.** Today: the `scripts/install-codex.sh` bootstrap + the restore/setup
skill branch. **Update (2026-06-26): Codex now HAS a plugin + marketplace system**
(`codex plugin`, `codex plugin marketplace`) - this supersedes the earlier "no
plugin/marketplace" note. A plugin bundles **skills, MCP servers, and hooks**; a
marketplace is a local dir or **Git repo** with `.agents/plugins/marketplace.json`.
Proven hands-on that installing a plugin registers a **bundled local stdio MCP server**
with no manual `config.toml` edit - so Cortex can ship Tier 2 (MCP + skills) as a
first-class plugin: add `.agents/plugins/marketplace.json` + `plugins/cortex/` to this
repo, then `codex plugin marketplace add cortex-sync/Cortex` + `codex plugin add cortex`.
Tier 1 (AGENTS.md) stays host/script-managed (plugins carry no instructions field). See
the **live-validation addendum** at the end of this section for the proven manifest
schema and open packaging questions.

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
"never PATs in files". The `config.toml` block is **command-only** (no `env`); a full
`CORTEX_GIT_HOST`/`_USERNAME`/`_TOKEN` env triple is documented as a fallback **only**
when the store is unavailable, with a plaintext-on-disk warning. Both skills instruct
**merge, don't clobber** existing `config.toml`, and to restart Codex after editing it;
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

**Post-review follow-ups (adversarial multi-agent review, 2026-06-24 - 6 dimensions, 40
findings survived verification, 0 critical/high).** Fixes applied on branch
`feat/codex-support`: reworded the Tier 1 "sandbox-independent" **overclaim** (it is
*network*-independent, but memory reads depend on the *filesystem* sandbox); added an
explicit Tier 1 `git clone` step (a Codex-only box had no documented way to obtain the
repo); **demoted `danger-full-access`** to a last-resort with a whole-sandbox warning (was
offered co-equal and as the MCP-bug workaround); marked the MCP-cancel bug version-specific
(`0.125.0-alpha.3`, maybe fixed); `install-codex.sh` no longer writes through a symlinked
`AGENTS.md` (backs up / replaces the link, refuses a dir dest, guards unset `HOME`);
`setup` now honours `CODEX_HOME`; documented the plaintext-token exposure (`/proc/PID/environ`,
not `0600`). **Top pre-ship check** = the Codex memory-read question (the ⛔ blocking item
below). **Deferred:** the credential store-key canonicalisation bug (see Credential handling
above) - pre-existing, own fix + test.

**Second review (2026-06-24, PR #23 code-review workflow - 5 lenses, 33 findings verified,
0 critical/high; prior fixes confirmed landed). Fixes applied (commit `fix(codex)` #2):**
`install-codex.sh` now (a) backs up a genuine user `AGENTS.md` **once** so a changed-adapter
re-run can't clobber the original backup, (b) appends the `## Cortex configuration` block
(repo path + remote/host) so `sync-profile` and the memory pointer resolve the repo - closing
the gap where the script placed an `AGENTS.md` with no block, (c) adds `--uninstall` (removes
the skill symlinks + MCP entry, restores `AGENTS.md` from backup), (d) `grep -qw` for the
MCP-registered check; the **Tier 1 clone is now token-safe** (no PAT in URL, prompt entry,
warn re `store` helper, MCP `git_clone` as the sanctioned alternative); Tier 2 step-lettering
collision fixed; the **secret-scan-skipped-on-Tier-1** caveat is documented (by design - Tier 1
is host-side git). Remaining low/by-design: native Windows launcher (already a future item
above); the secret-scan caveat is documented, not closed.

**Open Codex checks (hands-on in a real install):**
- [x] **⛔ BLOCKING (pre-ship) - Codex memory-read under the sandbox. RESOLVED 2026-06-26
      (Codex 0.142.1): reads WORK, no mitigation needed.** Under a `:workspace` permissions
      profile Codex reads files outside the workspace (full-disk read; writes confined),
      and `:read-only` denies writes (sandbox genuinely enforces on WSL2/kernel 6.18).
      Tier 1 "just works"; the documented mitigations are optional fallbacks, not required.
- [~] **Tier 2 end-to-end - PARTIAL (2026-06-26).** Codex discovers all cortex-git tools
      (namespaced `mcp__cortex_git.<tool>`) and the server is fully functional standalone,
      BUT invoking any tool via `codex exec` hits the **MCP-cancel bug** ("user cancelled
      MCP tool call") across every sandbox + approval mode - still live in 0.142.1. The
      **interactive TUI** path is the daily-use mode and remains untested (needs a real
      TTY). Recheck there before declaring Tier 2 daily-usable.
- [x] **Exact `codex mcp add` flags - RESOLVED 2026-06-26.** `codex mcp add <NAME> (--url
      <URL> | -- <COMMAND>...)` with `--env KEY=VALUE` for stdio servers; the
      `install-codex.sh --with-mcp` wiring (`codex mcp add cortex-git -- <launcher>`) is correct.

**Live-validation addendum (2026-06-26, Codex CLI 0.142.1).** Hands-on against a real,
logged-in install (model `gpt-5.5`); prior notes were written vs `0.125.0-alpha.3`.
- **Tier 1 PROVEN end-to-end:** `install-codex.sh --profile-dir` placed `AGENTS.md` (+
  token-free Cortex config block); `codex exec` woke as Bree and named the memory path
  from the block. With the sandbox read result above, the migration guarantee holds.
- **Permissions model refactored:** flat `sandbox_mode` -> `[permissions.<name>]` profiles
  (`filesystem`/`network`/`extends`; parents `:workspace`, `:read-only`; select via
  `default_permissions`). Legacy `sandbox_mode` + `[sandbox_workspace_write]` deprecated but
  still works. `codex sandbox` now requires `-P <profile>` + a `[permissions]` table. The
  Tier 2 network hint in `install-codex.sh` was updated to the `permissions.<p>.network`
  form (legacy noted as fallback).
- **Plugin-as-distribution PROVEN viable.** Manifest `.codex-plugin/plugin.json`:
  `"skills": "./skills/"`, `"mcpServers": "./.mcp.json"` (wrapped `{"mcpServers":{...}}`),
  `"apps"`, `"hooks"`, `interface{}`. `marketplace.json` at `<root>/.agents/plugins/`;
  plugin entry `policy.authentication` must be `ON_INSTALL` | `ON_USE` (NOT `NONE`).
  `codex plugin marketplace add <dir|owner/repo|git-url>` + `codex plugin add <n>@<mp>`.
  Installing a test plugin registered its bundled **local stdio MCP** (visible in
  `codex mcp list`) with no `[mcp_servers]` edit. **Open packaging Qs:** what root/env-var
  Codex gives a plugin's `mcpServers.command` (Claude uses `${CLAUDE_PLUGIN_ROOT}`); and
  `bin/VERSION` must point at a real release before the launcher's download path works.
- **Native memory complements, not competes:** `~/.codex/memories_1.sqlite` /
  `goals_1.sqlite` are auto-derived, usage-ranked, local-only session memory/goals - the
  opposite layer to Cortex's authored, git-synced profile. No collision (both currently empty).

**Refs:** Codex docs (config-reference, mcp, guides/agents-md, skills,
concepts/sandboxing, agent-approvals-security, hooks, windows, plugins, plugins/build);
openai/codex#20603 (SessionEnd request). Plan: `~/.claude/plans/jolly-tumbling-cherny.md`.

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
