# Forge — Copilot Instructions

This is the Forge CMS project — a Go web framework designed for how you
actually think. Zero dependencies. AI-first. Production-ready by default.

## Before writing any code

1. Read `NEXT.md` in the workspace root. If it exists:
   - Read the task description.
   - **Plan your approach and present it to the user for approval before writing any code.**
   - Wait for explicit approval. Do not implement anything until the user confirms the plan.
   - Stop here — do not proceed with steps 2–7.
2. Read session context from `forge-cms/forge-architect/context/corepilot.md`
   via GitHub MCP. This is your state from the previous session.
3. Read `DECISIONS.md` — all architectural decisions are locked here.
   Do not work around them. If a decision seems wrong, raise it explicitly.
4. Read `ARCHITECTURE.md` — package structure, request lifecycle, stable interfaces.
5. Read `ROADMAP.md` — current milestone and implementation order.
6. Read the milestone backlog file for the **current milestone only**
   (e.g. `Milestone11_BACKLOG.md`). This is the authoritative task list.
   Do not read completed milestone backlogs — they are historical record only.
   Do not implement anything not listed in the current backlog.
   Do not skip steps — the order is load-bearing (dependency layers).
7. Apply document economy: completed items are removed from lists, not checked
   off. Resolved known issues are deleted. A document that does not influence
   a decision must be reduced or removed.

## After every commit

- If `NEXT.md` exists, stage it for deletion with `git rm NEXT.md` and include
  the deletion in the implementation commit — not as a separate commit.
  This avoids push conflicts since `NEXT.md` already exists on the remote.
- Update session context at `forge-cms/forge-architect/context/corepilot.md`
  via GitHub MCP. Record: current versions, latest amendment shipped,
  current milestone and step, what was deferred or blocked.

## Change classification

Before starting any work, identify the level:

**Level 0 — cosmetic** (CSS spacing, comment typos, whitespace)
Solo commit by the user. No DECISIONS.md entry. No architect involvement.
Criteria: no functional change, no exported symbol touched, no behaviour changed.

**Level 1 — micro-amendment** (isolated change, no cross-file consequences)
One A-entry in DECISIONS.md. No full milestone step required.
Examples: dependency version bumps, single-file config, docs-only changes.

**Level 2 — standard amendment or milestone step** (full cycle)
Requires architect involvement, DECISIONS.md entry with both index row and body,
ARCHITECTURE.md check, and explicit user approval before commit.
Criteria: touches an exported Go symbol, interface, or function signature;
affects a route or middleware behaviour; has consequences in more than one file.

When in doubt: Level 2.

## Non-negotiable rules

- Zero third-party dependencies in the `forge` core package
- All errors implement `forge.Error` — never raw `errors.New`
- **Read `ERROR_HANDLING.md` before writing any code that handles or returns errors,**
  **calls `WriteError`, adds a sentinel, uses `errors.As`/`errors.Is`, or writes**
  **an HTTP response in an error path. The single pipeline rule is non-negotiable.**
- `forge.Context` is an interface, not a struct (Decision 21)
- `forge.DB` is an interface, not `*sql.DB` (Decision 22)
- Go 1.22 minimum — do not use features introduced after 1.22
- `gofmt` always — no exceptions
- godoc comments on every exported symbol
- A fix or improvement that changes a file **other than** the current step's file
  is an **Amendment**, not a fix. Stop, draft the Amendment, get approval, then implement.
- Every Amendment commit — not just milestone steps — must include an explicit check
  of `ARCHITECTURE.md`. If the amendment adds, removes, or changes any exported symbol,
  interface, file, or behaviour, `ARCHITECTURE.md` must be updated in the same commit.
  Never update `ARCHITECTURE.md` from a plan or backlog description — only from
  verified, running code.
- A step that is deferred or descoped must be documented in `Milestone{N}_BACKLOG.md`
  immediately with the reason and the target milestone. Never silently skip.

## Before planning or writing anything

**Apply DRY (Don't Repeat Yourself):**
Before proposing or implementing anything, check whether the logic,
type, or pattern already exists elsewhere in the codebase.
Reuse and extend — never duplicate.

**Analyse for performance bottlenecks first:**
Before planning or implementing any feature, identify where the
performance-critical paths are. Consider: allocations per request,
reflection usage (use the sync.Map cache pattern), goroutine overhead,
and SQL query efficiency. Propose the performant solution by default —
not the convenient one.

**Optimise for readability and developer/AI experience:**
Every exported symbol is part of the public API that developers write
by hand and AI assistants read and generate. Before finalising any
signature, option name, or syntax pattern, ask:
- Is this the most readable form at the call site?
- Can an AI assistant infer intent from the symbol name alone, without
  reading docs?
- Is the pattern consistent with every other symbol in the package?
- Would a developer scanning unfamiliar code understand it in under
  three seconds?

Prefer `forge.Verb(Noun)` or `forge.Noun` — no abbreviations, no
clever names. A longer but unambiguous name is always better than a
short opaque one.

**Analyse consequences for developer and AI experience before any amendment:**
Before proposing a Decision, Amendment, or architectural change, explicitly
evaluate its impact on:
1. **Call-site syntax** — how does it look when a developer writes it?
2. **README and documentation** — does any documented example break or
   become misleading?
3. **AI generation accuracy** — will AI assistants be able to produce
   correct Forge code without consulting docs?
4. **Consistency** — does this pattern align with all existing exported
   symbols, or does it introduce a special case?

Document this analysis in the Amendment before it is agreed upon.
If an amendment breaks a README example, fix the README in the same step.

## Code style

- Single package: `forge` — no sub-packages
- File names are the organisation — keep logic in the correct file
- Prefer interfaces over concrete types in function signatures
- Table-driven tests with `t.Run`
- Benchmarks for anything on the hot path (request handling, validation, scanning)

## Environment

The development environment is **Windows with PowerShell**. All terminal commands
must use PowerShell syntax. Never use Unix-only tools.

| Instead of | Use |
|-----------|-----|
| `grep pattern file` | `Select-String -Path file -Pattern "pattern"` |
| `grep -r pattern dir` | `Get-ChildItem dir -Recurse \| Select-String "pattern"` |
| `cat file` | `Get-Content file` |
| `ls` | `Get-ChildItem` |
| `rm file` | `Remove-Item file` |
| `mv src dst` | `Move-Item src dst` |
| `cp src dst` | `Copy-Item src dst` |
| `&&` to chain commands | `;` to chain commands |
| `which cmd` | `Get-Command cmd` |

`go`, `gofmt`, `git` are available directly — no path qualification needed.

---

## Standard step workflow

Every step — without exception — follows this exact sequence:

### 1. Plan the step
- Write a detailed plan covering: what types/functions will be defined, their
  signatures, performance considerations, and how they will be tested.
- Present the plan to the user before writing any code.

### 2. Document the plan in the milestone backlog
- Expand the step's section in `Milestone{N}_BACKLOG.md` with numbered
  sub-sections (N.1, N.2, …) and atomic checkboxes.
- Every step ends with a verification block and the architecture review checkbox.
- Save the file. Confirm with the user before starting implementation.

### 3. Implement the step
- One step = one file (implementation + test file). Never mix two files in one step.
- Never plan or implement two steps in the same session without explicit user approval.
- Before writing any code, scan all existing files for patterns, types, or helpers
  that overlap with what you are about to implement. Reuse and extend — never duplicate.
- Tick checkboxes in the backlog as each task is completed.
- Run verification after implementation automatically — no permission needed:
  `go build ./...`, `go vet ./...`, `gofmt -l .`, `go test ./...`.
  Fix any failures before proceeding. Never ask the user whether to run these.
- **NEVER ask, announce, or request approval before running any of the following:
  `go build`, `go vet`, `go test`, `gofmt`, or any read-only PowerShell file
  command (`Get-Content`, `Select-String`, `Get-ChildItem`, `git diff`, `git log`,
  `git status`). Just run them. Do not narrate the process. Only surface results
  when they are unexpected (build failure, test failure, format diff). Commits
  are the ONLY action that requires explicit user approval.**
- Read any file in the workspace automatically — no permission needed.
  Use PowerShell (`Get-Content`, `Select-String`, etc.) or the read_file tool
  to read `DECISIONS.md`, `ARCHITECTURE.md`, `ROADMAP.md`, milestone backlog
  files, or any source file before planning or implementing. Never ask the user
  whether to read a file that already exists in the workspace.

**Cross-milestone integration test rule:**
Every milestone must include a final step that extends `integration_full_test.go`
with new cross-milestone groups (G-numbered sequentially after the previous
milestone's last group). Each new group must exercise the milestone's features
in combination with at least one feature from a prior milestone. New groups are
appended only — never replace or renumber existing groups.

**README status badge rule:**
Every milestone must include a step (or sub-task within the final integration step)
that updates `README.md` section badges. Each README section that documents a
feature has a milestone badge (`🔲 **Coming in Milestone N**` or `✅ **Available**`).
When a milestone ships a feature, update its badge from `🔲 Coming in Milestone N`
to `✅ **Available**` in the same commit. Never leave a badge pointing to a shipped
milestone — it becomes a lie the moment the code merges.

**README version and consistency rule:**
Before proposing any commit, review `README.md` for:
- **Version number** — the `**vX.Y.Z — stable.**` line on line 7 must match the
  latest tag in `CHANGELOG.md`. Update it if behind.
- **Milestone comments** — code examples that say `// — Milestone N` or
  `*(feature — Milestone N)*` must be updated when that milestone ships:
  remove the comment (feature is now always available), or update the badge.
- **Section consistency** — any section that documents a feature shipped in this
  commit must reflect the current behaviour (signatures, option names, endpoint
  paths). A README that misrepresents the API is a documentation bug.
- **No ✅ badge may claim a feature is available if it is not yet implemented.**
  No `🔲 Coming in Milestone N` badge may remain for a milestone that has shipped.

This review is part of every commit preparation, not only milestone commits.
Do not propose a commit message until README has been checked and updated.

**README compile test rule:**
Forge maintains `example_test.go` in the root package. Every Example function
in that file is a compile-verified extract of a README code example.

This rule applies at three points:

*Milestone planning:*
When drafting a `Milestone{N}_BACKLOG.md`, review `example_test.go` and confirm
that no planned change will break an existing Example function. If a planned
change will break an Example, the plan must include an update to
`example_test.go` as an explicit sub-task in the same step.

*Milestone closing:*
Before a milestone is marked ✅ Done, `go test ./...` must be green — which
includes all Example functions. A milestone may not be closed with a failing
Example function.

*Amendment drafting:*
When drafting an Amendment, explicitly state in the Consequences section
whether the Amendment will break any existing Example function.

An Amendment may make README syntax more elegant — if it does, update
`example_test.go` to reflect the improved syntax in the same commit.

An Amendment must never leave `example_test.go` in a failing state.

**Amendment DECISIONS.md completeness rule:**
Every commit that implements an Amendment must contain **both** of the following
edits to `DECISIONS.md` — neither is optional:

1. **Index table row** — a new row added to the Amendment index table at the top
   of `DECISIONS.md` (columns: ID, description, status, date).
2. **Body section** — the full Amendment text appended after the previous
   Amendment's body.

A commit that adds a body without an index row (or an index row without a body)
is incomplete and must not be proposed. Treat these as a single atomic unit:
write both in the same edit pass, verify with `Select-String` or `grep_search`
that both exist, then stage the file.

### 4. Architecture and decision review
- After verification passes, review `ARCHITECTURE.md` and `DECISIONS.md`.
- Ask: does this implementation reveal a gap, ambiguity, or conflict?
- If yes: draft a new Decision or Amendment and present it to the user before proceeding.
- Check this step's implementation against all previously implemented files: does it
  duplicate logic, diverge from an established pattern, or require a change to another
  file? Any change that crosses a file boundary requires an Amendment — not a fix.
- After each step, consider whether `ARCHITECTURE.md` needs updating: new exported
  symbols, corrected interface locations, changed behaviour, new middleware, or
  planned files that are now implemented. Update it before proposing the commit.
- The step is not complete until the review checkbox is ticked.

### 5. Update the roadmap, backlog, and session context
- Mark the step `✅ Done` in the `Milestone{N}_BACKLOG.md` Progress table with the completion date.
- Tick the step's summary checkbox in `ROADMAP.md` and update its row in the step table.
- Update session context at `forge-cms/forge-architect/context/corepilot.md` via GitHub MCP.
- Never batch updates — update immediately after the step is verified.

### 6. Propose a commit message
- Write a conventional commit message (format below).
- Present it to the user for approval. Do not commit without explicit user approval.
- Commits are the **only** action that requires explicit user approval. Build, vet,
  format, and test commands are executed autonomously.

### Commit message format

```
{type}({scope}): {short description} (Milestone {N}, Step {N})

{Body: what was implemented, bullet points if multiple items}

Decisions: {Decision numbers and Amendment IDs referenced}
Milestone: {N} / Step {N} ✅
```

Types: `feat`, `fix`, `refactor`, `test`, `docs`, `chore`  
Scope: the file name without extension (e.g. `errors`, `roles`, `node`)

---

## Release tagging

Forge uses **annotated tags only** — never lightweight tags. Annotated tags carry a
date, a tagger, and a message, and appear as formal releases on GitHub.

**Tag format:** `vMAJOR.MINOR.PATCH` — must match the version in `CHANGELOG.md`

**When to tag:**
- Every milestone that ships a version bump (`v0.x.0`) gets a tag
- Patch releases (bug fixes, no API change) get a tag
- Amendments alone do not get a tag unless they ship with a milestone

**Sub-module tagging rule (non-negotiable):**
Any commit that modifies files under `forge-mcp/` (or any other sub-module
directory) in a way that affects behaviour **must** also produce a sub-module tag.
The sub-module tag uses the prefix convention: `forge-mcp/vX.Y.Z`.
- Update `forge-mcp/CHANGELOG.md` with a `[X.Y.Z]` section before tagging.
- The root module version and the sub-module version are bumped **independently**
  — a patch to `forge-mcp/mcp.go` does not require a root version bump if no
  root-package files changed behaviourally, and vice versa.
- At the end of every commit, explicitly state which module tags are required:
  root (`vX.Y.Z`) and/or sub-module (`forge-mcp/vX.Y.Z`).

**Pre-tag checklist — all three must be green before tagging:**
1. `git status --short` returns nothing (working tree clean)
2. `go test ./...` is green (root); `go test ./...` inside `forge-mcp/` is green
3. `CHANGELOG.md` (root) and/or `forge-mcp/CHANGELOG.md` has an entry for the
   version being tagged

**Tag and push sequence:**
```
git tag -a vX.Y.Z -m "Forge vX.Y.Z — {one line summary}"
git push origin main
git push origin vX.Y.Z
# if forge-mcp also changed:
git tag -a forge-mcp/vX.Y.Z -m "forge-mcp vX.Y.Z — {one line summary}"
git push origin forge-mcp/vX.Y.Z
```

Push commits and each tag **separately** — never in the same command.

**GitHub Release titles:**
After pushing, create a GitHub Release for each tag from
`github.com/forge-cms/forge/releases`. Title each release as follows:

| Tag | Release title format |
|-----|----------------------|
| `vX.Y.Z` | `Forge vX.Y.Z — {release name}` |
| `forge-mcp/vX.Y.Z` | `forge-mcp vX.Y.Z — {release name}` |

The release name is a short (2–4 word) phrase that captures the primary change —
identical to the one-line summary in the tag message. Always propose the GitHub
Release title(s) alongside the commit message. Paste the relevant
`CHANGELOG.md` section as release notes.

**Never:**
- Tag before `go test ./...` is green
- Tag before `CHANGELOG.md` is updated for the version
- Use a lightweight tag (`git tag vX.Y.Z` without `-a`) for a release
- Push the tag in the same command as commits
- Ship a behavioural change to `forge-mcp/` without a `forge-mcp/vX.Y.Z` tag

---

## Milestone planning process

Before implementing any milestone, a dedicated backlog file must be created and
agreed upon. This file is the single source of truth for that milestone's detail.

### Two-tier structure

Forge uses two tiers of planning documentation:

**Tier 1 — `ROADMAP.md` (repo root)**
- High-level roadmap for all milestones
- Progress table at the top tracks milestone-level status
- Each milestone section has a per-step progress table and one-line step
  summary checkboxes — no sub-tasks, no implementation detail
- One-line step format: `- [ ] Step {N} — \`{filename}\`: {one sentence summary}`
- Updated when: a step is completed (tick the step checkbox + update step table)
  or a milestone status changes (update the top Progress table)

**Tier 2 — `Milestone{N}_BACKLOG.md` (repo root)**
- Full implementation plan for one milestone only
- Contains numbered sub-sections (N.M), atomic checkboxes, verification blocks,
  and the architecture review checkbox
- The authoritative task list — implementation follows this file exactly
- Updated after every step: tick all checkboxes, mark step ✅ in Progress table

### Keeping the two tiers in sync

After completing a step:
1. Tick all sub-task checkboxes in `Milestone{N}_BACKLOG.md`
2. Mark step ✅ Done in the `Milestone{N}_BACKLOG.md` Progress table
3. Tick the step checkbox in `ROADMAP.md` under the relevant milestone section
4. Update the step row status in `ROADMAP.md` step table
5. If all steps in a milestone are done, mark the milestone ✅ in the top
   `ROADMAP.md` Progress table

Never update only one file — always keep both in sync.

### Structure of a milestone backlog file

The file follows the structure defined in `Milestone_BACKLOG_TEMPLATE.md`.
Copy that file and fill in the placeholders before implementation starts.

### Milestone close — backlog cleanup

When a milestone is marked ✅ Done, remove its backlog and test strategy
files from the working tree in the final commit of that milestone:

```powershell
git rm Milestone{N}_BACKLOG.md
git rm Milestone{N}_TEST_STRATEGY.md   # if one exists
```

These files are preserved in git history. Removing them keeps the repo
root clean for developers who clone forge to use it, not to study its
internal planning history. `Milestone_BACKLOG_TEMPLATE.md` is never removed.

### Rules for steps

- **One step = one file** (implementation + test file). Never mix two files in one step.
- **Steps are strictly separate** — never plan or implement two steps in the same
  session without explicit user approval.
- **Steps are ordered by dependency layer** — a step may not be started until all
  steps it depends on are marked ✅.
- **Sub-sections (N.M)** break the step into logical implementation chunks: define
  the type, implement the logic, write the tests, verify. Keep sub-sections small
  enough that each can be completed and verified in one sitting.
- **Checkboxes are atomic** — each `- [ ]` item must be a single, unambiguous task.
  Never write "implement X" without specifying what X requires.
- **Every step ends with an architecture and decision review.** After the verification
  block passes, review `ARCHITECTURE.md` and `DECISIONS.md` and ask:
  - Does the implementation reveal a gap, ambiguity, or conflict in an existing decision?
  - Did any implementation choice introduce a pattern or constraint not yet captured?
  - Does the file's dependency graph still match the rules in `ARCHITECTURE.md`?
  If yes to any of the above, a new Decision or Amendment must be proposed and agreed
  upon before the next step begins. The step is not complete until this review is done.
- **Every step ends with a commit.** After the architecture review, write a commit
  message following the standard format and wait for user approval before committing.
  Never commit without approval.
  Add the following checkbox at the end of every step's verification block:
  ```
  - [ ] Review ARCHITECTURE.md and DECISIONS.md — no new decisions required,
        or new Decision/Amendment drafted and agreed upon
  ```
