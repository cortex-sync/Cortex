# Profile merge - design sketch

**Status:** Implemented - the reconcile flow lives in `restore-profile`
("Reconcile an existing local profile"), and `setup` Section 0 forks to it for the
existing-local-file + populated-remote case. This document is retained as the design
record.\
**Date:** 2026-06-17 (implemented 2026-07-01)

---

## The gap

`setup` and `restore-profile` between them cover every combination of local
profile and remote repo **except one**: an existing local `CLAUDE.md` *and* an
existing remote repo with content. There is no path that reconciles the two.

Two axes - **local profile** (`~/.claude/CLAUDE.md` ± local `memory/`) ×
**remote repo**:

|                   | No remote                     | Empty remote              | Remote with content     |
|-------------------|-------------------------------|---------------------------|-------------------------|
| **No local**      | questionnaire (`setup`)       | `git_init` (`setup`)      | clone (`restore`)       |
| **Local exists**  | import/fresh → `git_init` (`setup` S0) | import → `git_init` (`setup` S0) | **missing: merge**      |

The bottom-right cell is both:

- a **feature gap** - `setup` can only ever `git_init` a fresh empty remote, so
  it cannot reach this cell; and
- a **latent data-loss bug** - `restore-profile` clones and *clobbers* the local
  `CLAUDE.md` with no guard (the "place the profile" step: "Copy `CLAUDE.md` from
  the cloned repo to the detected platform path").

## Where the logic belongs

Do **not** overload `setup` - its mental model is "create new".

1. **`setup` gains an early fork** (before the questionnaire): *"Do you already
   have a Cortex profile repo?"* → Yes routes to the reconcile path; No is the
   current greenfield flow.
2. **The merge itself lives in `restore-profile`**, upgraded from
   *clone-and-clobber* to *clone-and-reconcile*: when a local `CLAUDE.md` exists,
   merge instead of overwrite. One home for the logic, both entry points reach
   it, and it fixes restore's silent-overwrite bug for free.

## Reconcile flow (existing MCP tools only - no Go changes)

```
1. git_clone        remote → local_path          # repo's CLAUDE.md + memory/ land locally
2. reconcile        repo content  ⊕  ~/.claude/CLAUDE.md (+ local memory/)
3. git_commit_push  local_path                   # safety gate + content scan already enforce this
4. copy             merged CLAUDE.md → ~/.claude/CLAUDE.md
```

## Merge strategy, per file type (the real work)

No common ancestor, so this is a **2-way merge with the user as adjudicator**,
not a git 3-way merge:

- **`CLAUDE.md`** - LLM section-level merge. Align headings, surface only the
  *conflicts* (same section, different content) for the user to choose; auto-union
  the non-overlapping sections. Show a draft/diff and confirm before writing.
  This is where design.md open-question #6 ("last-write-wins") is too crude -
  prose needs adjudication.
- **`memory/*.md`** - append-only convention (design §3) makes these a
  **union + dedupe** by entry, near-automatic. Tag provenance where it matters.
- **Persona / identity blocks** - treat as singletons. If both exist and differ,
  always ask; never silently merge two personas.

## Decide before building

1. **Safety** - the local `CLAUDE.md` is *imported content, not instructions*
   (same caution `setup` Section 0 already calls out). Carry it into the
   reconcile path.
2. **Conflict UX** - inline one-section-at-a-time confirm vs. write a merged
   draft and let the user edit. Leaning: inline-confirm for `CLAUDE.md`, silent
   union for `memory/`. Keeps the common case zero-friction.

**Net:** no MCP/Go changes; one new fork in `setup`; `restore-profile` grows a
merge branch.
