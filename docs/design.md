# Cortex - Plugin Design Document

**Status:** Draft - open questions resolved\
**Author:** Lucas Symons\
**Date:** 2026-06-03\
**Version:** 0.2

---

## 1. Vision

Most AI sessions start from scratch. Each conversation forgets context, persona, team rules, and hard-won lessons. The Cortex plugin solves this by treating your AI configuration - persona, memory files, instructions, and accumulated knowledge - as a Git-managed artefact that travels with you.

The result: every new session on any device, in any supported AI tool, starts with the full context of everything you've ever taught your AI. Sub-agents inherit the same context. Knowledge built in one project enriches all future projects.

**Long-term goal:** Cross-AI-platform portability. The same profile works in Claude, Gemini, ChatGPT, or any future agent - with per-platform adapters translating the unified format into each tool's native instruction syntax.

---

## 2. Goals

- **Portable AI identity** - Your persona, rules, and memory follow you across devices and reinstalls
- **Shared knowledge** - Lessons learned in one project feed back into the top-level memory, available to all future sessions
- **Secure by default** - Private repo, no PII in memory files, credentials in OS keychain
- **Cross-AI ready** - Unified profile format with per-platform adapters
- **Zero friction** - Auto-syncs on session handoff; manual sync available any time
- **Good onboarding** - Guided questionnaire produces a well-structured, secure base profile from day one

---

## 3. How it works (user journey)

### First install

1. User installs the plugin via `/plugin install` or from the community marketplace
2. Plugin runs the **setup skill** - a guided questionnaire covering:
   - Name, role, organisation
   - Tech stack and preferred languages
   - AI persona preferences (tone, name, communication style)
   - Security rules (PII handling, credential policy, stop-and-ask thresholds)
   - Git host selection (GitHub / GitLab / Azure DevOps / Gitea)
   - Repo name and visibility (private strongly recommended)
3. Plugin generates a base `CLAUDE.md` (and platform adapters) from questionnaire answers
4. Plugin initialises the memory file structure (`memory/active.md`, `systems.md`, `people.md`, `lessons.md`)
5. Plugin creates the Git repo (or connects to an existing one), commits, and pushes
6. Profile is live

### Day to day

- On **session handoff**, the sync skill automatically commits and pushes any changed memory files
- **Manual sync** available via `/sync-profile` at any time
- On a **new device**, install the plugin and run `/restore-profile` - clones the repo and places files in the right locations for the current platform

### Knowledge promotion

- When a lesson is saved to a project-level `lessons.md`, the sync skill identifies entries worth promoting to the top-level memory
- Promoted entries are tagged with their source project so context is preserved
- This happens automatically on handoff, or can be triggered manually

---

## 4. Architecture

```
Plugin
├── Setup skill          → questionnaire → generates CLAUDE.md + memory structure + repo
├── Sync skill           → commits and pushes changed files on handoff or manual trigger
├── Restore skill        → clones repo on new device, places files correctly
├── Promote skill        → lifts project-level lessons to top-level memory
├── Git MCP server       → all git operations (clone, commit, push, pull, status, init, credentials)
└── Platform adapters    → translates unified profile to Claude, Gemini, ChatGPT formats
```

### Git MCP server

A lightweight server that wraps git operations. Exposes tools:

| Tool | Description |
|---|---|
| `git_status` | List changed files in the profile repo |
| `git_commit_push` | Commit all changes with a message and push |
| `git_pull` | Pull latest from remote |
| `git_clone` | Clone an existing repo to a target path (restore flow) |
| `git_init` | Initialise a new repo locally, add the remote, commit and push (first-run setup) |
| `get_auth_status` | Check if credentials are configured; reports the active backend |
| `set_credentials` | Store a PAT in the credential store (keychain or encrypted-file fallback) |
| `delete_credentials` | Remove the stored PAT for a host (token rotation); idempotent |

> **First-run repo creation.** go-git's `PlainClone` cannot clone an *empty* remote (it returns `transport.ErrEmptyRemoteRepository` - see [go-git #118](https://github.com/go-git/go-git/issues/118)); the idiomatic pattern for a brand-new repo is to initialise locally and push, which `git_init` does (`PlainInitWithOptions` with `main` as the default branch → `CreateRemote` → commit → push with an explicit refspec). The user creates the empty private repo in their host's web UI first; the setup skill walks them through it.
>
> **Deferred - option 2 (`git_init_remote`).** Fully automated remote creation via each host's REST API (GitHub/GitLab/Azure DevOps/Gitea/...) was considered and deferred: it would pull in a client library per host, bloating the dependency tree and the binary, for a step the LLM can already guide the user through in seconds. Revisit only if the manual web-UI step proves a real friction point.

The server is written in **Go** (compiles to a single native binary per platform - no runtime, no install step, no AV false-positive risk). Git operations use `go-git` (pure Go, no system git dependency). Auth tokens are stored in the OS keychain via `zalando/go-keyring` when a Secret Service is available, with an AES-256-GCM machine-bound encrypted file as the headless/WSL fallback (see §7) - never plaintext on disk.

### Profile repo structure

```
cortex/
├── CLAUDE.md              # primary instruction file (Claude / Claude Code)
├── adapters/
│   ├── gemini.md          # Gemini system prompt format
│   ├── chatgpt.md         # ChatGPT system prompt format
│   └── generic.md         # plain text for any other tool
├── memory/
│   ├── README.md          # memory system conventions
│   ├── active.md          # current sprint / active context
│   ├── systems.md         # systems knowledge
│   ├── people.md          # contacts and stakeholders
│   ├── lessons.md         # learned patterns and decisions
│   ├── bree.md            # (or equivalent persona memory file)
│   └── team.md            # team context
├── .gitignore             # excludes secrets, local-only files
└── sync-log.md            # append-only log of sync events
```

---

## 5. Plugin structure

Following the confirmed Claude Code plugin spec:

```
cortex/
├── .claude-plugin/
│   └── plugin.json            # manifest
├── skills/
│   ├── setup/
│   │   └── SKILL.md           # guided first-run questionnaire
│   ├── sync-profile/
│   │   └── SKILL.md           # manual sync trigger
│   ├── restore-profile/
│   │   └── SKILL.md           # new device restore
│   └── promote-lessons/
│       └── SKILL.md           # promote project lessons to top-level memory
├── .mcp.json                  # registers the Git MCP server
├── mcp/git-server/            # the Go MCP server
│   ├── cmd/server/main.go     #   entry point + tool registration
│   ├── internal/git/          #   go-git wrapper
│   ├── internal/keychain/     #   credential storage (keychain + encrypted-file fallback)
│   └── Makefile               #   build targets
├── profile-template/          # ships a .gitignore + memory/README.md; the rest is generated into the user's repo at setup
└── README.md
```

**`plugin.json`:**
```json
{
  "name": "cortex",
  "description": "Sync your AI profile, persona, and memory across devices and AI platforms via Git",
  "version": "1.0.0",
  "author": { "name": "Lucas Symons" },
  "repository": "https://github.com/<org>/cortex"
}
```

---

## 6. Global CLAUDE.md injection

The plugin docs confirm there is **no direct mechanism to inject into CLAUDE.md via plugin API**. The clean workaround:

The **setup skill** and **restore skill** write directly to the correct CLAUDE.md path for the detected platform:

| Platform | CLAUDE.md location |
|---|---|
| Cowork (Documents connected) | `~/Documents/CLAUDE.md` |
| Claude Code CLI | `~/.claude/CLAUDE.md` |
| Project-level (optional) | `<project-root>/CLAUDE.md` |

On restore, the skill detects which environment it's running in and writes to the right path. This is functionally equivalent to injection - the file is in place before the next session starts.

The profile repo is the source of truth. CLAUDE.md on disk is a synced working copy.

---

## 7. Security design

### Credential handling
- Git PATs stored in the OS keychain (`zalando/go-keyring`) whenever a usable Secret Service is present (macOS Keychain, Windows Credential Manager, desktop Linux with gnome-keyring/KWallet)
- **Fallback** for headless platforms (WSL2, containers, CI) where no Secret Service is available: an AES-256-GCM encrypted file at `${XDG_CONFIG_HOME:-~/.config}/cortex/credentials.enc`, mode `0600`. The key is derived from a machine-bound identifier (an OS machine-id file - `/etc/machine-id` or `/var/lib/dbus/machine-id` - or `CORTEX_MACHINE_ID`) + the current user, so the file is non-portable and never plaintext. The backend is selected automatically at runtime; `get_auth_status` / `set_credentials` report which one is active.
  - **Limitation (accepted):** the file is auto-decryptable by any process running as the same user on the same machine - it is obfuscation-at-rest, comparable to (and stronger than) plaintext stores like `git-credential-store` / the `gh` CLI, not a hardware-backed keychain. It exists so WSL - a *primary* target, since Windows users run Claude Code there - works without per-machine keyring setup.
- PAT scopes: repo read/write only (minimum required)
- `set_credentials` MCP tool receives the PAT as a tool argument (passed by the skill), stores it, never echoes it back

### Repository
- Private repo **strongly recommended** (enforced by default in setup, requires explicit override to use public)
- `.gitignore` excludes: `*.env`, `*.pem`, `*.key`, `*secret*`, `*credential*`, `*.tfstate`, local cache files
- No PII in memory files - the setup skill and sync skill both screen for PII patterns before committing (email addresses, phone numbers, account numbers)

### Memory content rules (baked into generated CLAUDE.md)
- Never commit authentication material (passwords, API keys, tokens)
- Never commit customer or staff data
- Memory files contain system knowledge and lessons, not personal data
- Externally-sourced content reviewed before saving (prompt injection protection)

### Prompt injection protection
- The setup questionnaire generates explicit instruction-authority rules in CLAUDE.md
- Memory files are treated as trusted reference material, not commands
- Any loaded content that appears to contain AI-directed instructions is flagged

---

## 8. Cross-AI platform adapters

The unified profile is stored in `CLAUDE.md` format (extended markdown). Per-platform adapters translate it:

| Platform | Adapter format | Location in repo |
|---|---|---|
| Claude / Claude Code | Native CLAUDE.md | `/CLAUDE.md` |
| Gemini | System prompt markdown | `/adapters/gemini.md` |
| ChatGPT | System prompt markdown | `/adapters/chatgpt.md` |
| Generic | Plain text | `/adapters/generic.md` |

The **sync skill** regenerates adapters on each sync using a conversion skill. Initially the adapters are manually curated; future versions can use an LLM to auto-translate.

For sub-agent use: the restore skill can be invoked by a sub-agent to pull the latest profile context before starting work on a task.

---

## 9. Setup questionnaire outline

The setup skill walks through these sections:

### Identity
- Name, role, organisation
- Timezone
- Primary email

### Tech stack
- Languages (multi-select)
- Cloud platforms
- IaC tools
- Source control / CI/CD
- Collaboration tools

### AI persona
- Does the user want a named persona? (y/n)
- If yes: persona name, personality traits, communication style, backstory depth
- Tone preference (formal / balanced / casual)
- Sarcasm / humour level

### Working style
- Preferred explanation depth (concise / detailed)
- Code style defaults (production-ready always / ask first)
- Language/naming conventions (British English etc.)
- Em-dash preference (yes/no - important!)

### Security rules
- PII sensitivity level
- Stop-and-ask thresholds (terraform destroy, git force push etc.)
- Credential leak procedure
- Escalation contacts

### Memory system
- Enable memory system? (y/n)
- Memory file names (defaults provided)
- Proactive save prompts? (y/n)

### Git configuration
- Git host (GitHub / GitLab / Azure DevOps / Gitea / other)
- Repo name (default: `cortex`)
- Visibility (default: private)
- PAT entry (stored to keychain)

---

## 10. Implementation phases

### Phase 1 - Core sync (MVP)
- Git MCP server (clone, commit, push, pull, status)
- Setup skill (questionnaire → CLAUDE.md + memory structure + repo)
- Sync skill (manual trigger)
- Restore skill (new device)
- Basic `.gitignore` and security rules in generated CLAUDE.md

**Deliverable:** A working plugin that generates a profile, pushes to Git, and restores on a new device.

### Phase 2 - Auto-sync and knowledge promotion
- PostHandoff hook integration (auto-sync on session end)
- Promote-lessons skill (project lessons → top-level memory)
- PII screening before commit
- Sync log

**Deliverable:** Fully automated sync workflow matching current manual handoff behaviour.

### Phase 3 - Cross-platform adapters
- Gemini adapter generation
- ChatGPT adapter generation
- Generic adapter
- Adapter regeneration on sync

**Deliverable:** Profile usable in non-Claude AI tools.

### Phase 4 - Distribution
- Plugin validation (`claude plugin validate`)
- README and docs
- Submit to community marketplace
- Independent marketplace.json for private/team distribution

---

## 11. Tech stack decisions

| Component | Choice | Rationale |
|---|---|---|
| MCP server language | **Go** | Compiles to a single native binary per platform - no runtime, no install step, no AV false-positive risk. Clean distribution story for a public plugin. |
| Git operations | `go-git` | Pure Go git implementation - no system git dependency, works everywhere. |
| Credential storage | OS keychain, with encrypted-file fallback | `zalando/go-keyring` for OS keychain access (macOS Keychain, Windows Credential Manager, Linux Secret Service) when available; AES-256-GCM machine-bound encrypted file (`~/.config/cortex/credentials.enc`, `0600`) as a fallback on headless/WSL2/container/CI where no Secret Service exists. Backend chosen automatically. See §7 for the security trade-off. |
| Git transport | HTTPS + PAT only | No SSH keys. PAT over HTTPS works on the broadest range of networks and is simpler to store and rotate. |
| Git host API (repo creation) | REST API per host | GitHub API, GitLab API, Azure DevOps API - thin client per host. PAT auth. |
| Adapter generation | Skill-based (LLM) | Claude translates CLAUDE.md → platform format using a conversion prompt. |
| Registry | Public registries | Dependencies come from public registries (the Go module proxy, Docker Hub). No private or internal registry is involved. |

---

## 12. Open questions - resolved

| # | Question | Decision |
|---|---|---|
| 1 | Hook event availability | **Use a custom sync skill triggered explicitly by instructions** (e.g. "on handoff, run sync-profile"). No dependency on a specific hook event. Simpler and more reliable. |
| 2 | Private-registry constraint | **Not applicable** - dependencies come from public registries; there is no private or internal registry requirement. |
| 3 | MCP server install in sandbox | **Spike required early in Phase 1.** If sandboxed `pip install` works, use Python. If not, switch MCP server to **Go** - compiles to a clean native binary with no AV false-positive risk. PyInstaller bundles are routinely flagged; Go binaries are not. |
| 4 | Sub-agent profile inheritance | **Unknown - needs testing.** Design assumption: sub-agents do not automatically inherit parent CLAUDE.md. High-level agent instructions will explicitly reference the profile repo path and tell sub-agents which memory files to read at the start of any task. |
| 5 | PAT scopes per host | **HTTPS + PAT throughout** (no SSH). Minimum scopes to document per host: GitHub (`repo`), GitLab (`write_repository`), Azure DevOps (`Code: Read & Write`). Added to setup questionnaire help text. |
| 6 | Conflict resolution | **Last-write-wins by default** (hard-reset local onto origin on pull; remote wins, local divergence discarded). Conflicts in memory files are unlikely given append-only convention. Better merge logic deferred to a later phase. |

---

## 13. Next steps

1. ✅ Design doc written and reviewed
2. ✅ Set up the plugin repo
3. ✅ Implement the Git MCP server (Go, Phase 1 tools only)
4. ✅ Write the setup skill questionnaire
5. ✅ Write the sync and restore skills
6. ✅ Build lesson promotion skill
7. ✅ Release pipeline (goreleaser) producing tagged binaries + checksums
8. Binary distribution to installed plugins + marketplace publishing (see [TODO.md](TODO.md))
9. Add PostHandoff hook to auto-trigger sync
10. Submit to community marketplace
