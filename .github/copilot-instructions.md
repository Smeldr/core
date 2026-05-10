# Forge — Copilot Instructions

This is the Forge CMS project — a Go web framework designed for how you
actually think. Zero dependencies. AI-first. Production-ready by default.

## New chat session — start here

Every new chat has no memory of previous sessions. Follow these steps
at the start of every new chat, before doing anything else.

**Step 1 — Read session context:**
Read `C:\Users\peter\Documents\Code\forge-architect\context\corepilot.md` (local file).
This is your state from the previous session: current versions, latest
amendment, active milestone and step, anything deferred.

**Step 2 — Check for a pending task:**
Check whether `NEXT.md` exists in the forge repo root (local workspace).
If it exists:
1. Read it and form a full implementation plan (including any questions).
2. Write the plan file locally to:
   `C:\Users\peter\Documents\Code\forge-architect\plans\core-next-plan.md`
   Include the complete plan and any open questions in that file.
3. Notify the user in chat that the plan is ready for review at
   `forge-architect/plans/core-next-plan.md`. Do not write any code yet.
4. Wait for explicit approval before implementing anything.
5. At commit time: delete both `NEXT.md` and `plans/core-next-plan.md`
   in the same commit as the implementation.
6. After the commit: write `C:\Users\peter\Documents\Code\forge-architect\context\corepilot.md`
   locally with the amendment number, then commit and push from that repo
   (see "After every commit" for the exact command sequence).

**Step 3 — If no NEXT.md:**
Report what you found in `context/corepilot.md` and ask the user what
to work on. Do not proceed autonomously.

**Why this matters:**
An implementer that starts a new chat without reading context will use
wrong versions, repeat completed work, or miss deferred items. The context
file is the bridge between sessions. Always read it first.

---

## Before writing any code

1. Read `NEXT.md` in the workspace root. If it exists:
   - Read the task description.
   - **Write the full implementation plan (including any questions) to the local file:
     `C:\Users\peter\Documents\Code\forge-architect\plans\core-next-plan.md`**
   - Notify the user in chat that the plan is ready for review at
     `forge-architect/plans/core-next-plan.md`. Do not write any code yet.
   - Wait for explicit approval. Do not implement anything until the user confirms the plan.
   - Stop here — do not proceed with steps 2–7.
2. Read session context from `C:\Users\peter\Documents\Code\forge-architect\context\corepilot.md`
   (local file). This is your state from the previous session.
3. Read `DECISIONS.md` — index table only. Body text lives in `decisions/core.md`
   (Decisions 1–22 + amendments) and `decisions/phase2.md` (Decision 25 onwards).
   Read the relevant body file when a specific decision is needed.
   Do not work around locked decisions. If a decision seems wrong, raise it explicitly.
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

- If `NEXT.md` exists, delete it with `Remove-Item NEXT.md -ErrorAction SilentlyContinue`.
  NEXT.md is written locally by the architect and is never committed to git — it is
  always untracked. Do not use `git rm`; it will fail on an untracked file.
- If `plans/core-next-plan.md` exists locally, delete it:
  `Remove-Item "C:\Users\peter\Documents\Code\forge-architect\plans\core-next-plan.md"`
- Update session context: write `C:\Users\peter\Documents\Code\forge-architect\context\corepilot.md`
  locally. Record: current versions, latest amendment shipped,
  current milestone and step, what was deferred or blocked.
  Then commit and push from the forge-architect repo:
  `cd C:\Users\peter\Documents\Code\forge-architect ; git add context/corepilot.md ; git commit -m "chore(context): update corepilot after [sprint name]" ; git push`
  Do NOT use GitHub MCP to update this file.

## DECISIONS.md file structure (CRITICAL)

DECISIONS.md is now split into three files:

- `DECISIONS.md` — index table only. Always small.
- `decisions/core.md` — Decisions 1–22 + all amendments (A19–A65). ~173KB.
- `decisions/phase2.md` — Decision 25 onwards.

**Corepilot owns all writes to `decisions/` and `DECISIONS.md`.**
These files must be edited locally via git — never via GitHub MCP API calls.
The files are too large for `create_or_update_file` and `push_files` silently
truncates them.

**When adding a new Decision or Amendment:**
1. Edit the relevant file locally (`decisions/core.md` for amendments,
   `decisions/phase2.md` for new decisions)
2. Add the index row to `DECISIONS.md`
3. Commit both in the same commit
4. Never use GitHub MCP `create_or_update_file` or `push_files` for these files

**When appending to `decisions/phase2.md`:** Use enough surrounding context to
uniquely identify the insertion point. Closing lines (e.g. `---`) repeat
throughout the file — a match failure on the first attempt means the context
was too short. Re-read the tail of the file and use a longer unique anchor.

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
edits — neither is optional:

1. **Index table row** — a new row added to the Amendment index table in
   `DECISIONS.md` (columns: ID, description, status, date).
2. **Body section** — the full Amendment text appended to `decisions/core.md`
   (for amendments) or `decisions/phase2.md` (for new decisions).

Both edits must be made locally via git — never via GitHub MCP.
A commit that adds a body without an index row (or vice versa) is incomplete.
Treat these as a single atomic unit: write both, verify with `Select-String`
that both exist, then stage.

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
- Write `C:\Users\peter\Documents\Code\forge-architect\context\corepilot.md` locally,
  then commit and push from that repo (see "After every commit" for the command sequence).
- Never batch updates — update immediately after the step is verified.

### 6. Propose a commit message
- Write a conventional commit message (format below).
- Present it to the user for approval. Do not commit without explicit user approval.
- Commits are the **only** action that requires explicit user approval. Build, vet,
  format, and test commands are executed autonomously.
- **A "yes" answer to a review question is not commit approval.** If you asked a
  blocking question before proposing a commit, and the user answered it, you must
  still present the commit message and wait for a separate explicit approval before
  committing. The confirmation of a technical fact and the approval of a commit are
  two distinct acts. Never collapse them into one.

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

## Docs and content workflow

Use this workflow for any task that involves updating repo docs (REFERENCE.md,
README.md, FEATURELIST.md) or creating content for forge-cms.dev/docs.

This workflow is separate from the standard step workflow. It applies to
docs-only tasks and content tasks — not to code implementation.

### On every session start — doc freshness check

Before any other work, check these three files for staleness against the
current codebase and recent amendments:

1. `REFERENCE.md` — does it reflect all current exported symbols and behaviour?
2. `README.md` — is the version line (`**vX.Y.Z — stable.**`) current?
3. `FEATURELIST.md` — does it list all shipped features?

Present any staleness findings to the user before proceeding. Do not silently
skip this check.

### Docs and content task workflow

Every docs or content task follows this sequence:

**1. Propose commit scope**
Before any work: propose what the commit will cover in one sentence.
Wait for approval to proceed. Do not write anything yet.

**2. Repo doc review**
Read REFERENCE.md, README.md, and FEATURELIST.md.
Present what needs updating — specific, concrete findings only.
Wait for feedback before making any changes.

**3. Apply repo doc updates**
Apply agreed changes to REFERENCE.md, README.md, and/or FEATURELIST.md.
Also update `.claude/skills/forge.md` when any of the following changed:
- MCP tools or CLI commands (update both sections; verify CLI/MCP parity)
- Config keys (update forge.config section)
- New failure modes confirmed in this release (update gotchas)
- Any of the above → bump the version line at the top of the skill file
- Copy updated skill file to `Forge-site-working/.claude/skills/forge.md`
Do not commit yet.

**4. Content brief**
After repo doc updates are applied, write a content brief covering:
- What shipped: plain-language summary (one paragraph)
- Amendment ID (e.g. A93)
- REFERENCE.md section: relevant header
- Devlog: yes/no + suggested angle (or "covered by Axx")
- Solved: which story this feature supports, if any
- Docs: which forge-cms.dev/docs pages need updating

Release type guidance:

| Release type | Devlog | Solved | Docs |
|---|---|---|---|
| New milestone (M-number) | yes | possibly | yes |
| New MCP/CLI feature | yes | possibly | yes |
| Bugfix / patch | no | no | only if API changed |
| Doc/infra fix | no | no | no |

The content brief is handed to the architect, who converts it into a sitepilot NEXT.md.
Wait for feedback before proceeding.

**5. Content suggestions (forge-cms.dev/docs)**
Based on updated REFERENCE.md, suggest doc page title(s) that should be
created or updated on forge-cms.dev.
Wait for feedback.

**6. Content outline**
For each approved title, suggest content as a short bullet list.
Wait for feedback before writing full drafts.

**7. Full drafts**
Write full draft(s) based on approved outlines.
Wait for feedback and approval.

**8. Save approved drafts**
Save each approved draft as an individual file in:
`C:\Users\peter\Documents\Code\Forge-site-working\content\`

File naming: `YYYYMMDD-HHMMSS-<slug>.md`

Example: `20260505-143022-token-management.md`

These files are for sitepilot to pick up — do not commit them to the forge repo.

**9. Propose commit message**
Propose a conventional commit message covering all repo doc changes (steps 2–3).
Wait for explicit approval before committing.

**10. After commit**
Update `forge-architect/plans/core-next-plan.md` if a plan file was created.
Update `forge-architect/context/corepilot.md` and push from that repo.

### Never push without explicit permission

Commits require explicit approval (a separate "yes" after the commit message is
proposed — not implied by answering a technical question).

Pushes require a **separate** explicit instruction after the commit is made.
"Commit approved" is not push permission. Always wait for "push it" or equivalent.

Write the plan for any docs task to:
`C:\Users\peter\Documents\Code\forge-architect\plans\core-next-plan.md`

---

## CLI and MCP tool parity

Every admin operation available via MCP tools must also be available via forge-cli.
CLI is the human fallback when agents are unavailable.

This rule applies to every milestone that ships new MCP tools: the corresponding
forge-cli commands ship in the same release — not as a follow-up.

**Current known gap:** forge-cli v0.3.0 has no nav commands despite four MCP nav
tools existing (`list_nav_items`, `create_nav_item`, `update_nav_item`,
`delete_nav_item`). This gap is tracked and will be closed in the nav CLI milestone.

---

## Branching and commit timestamps
All milestone work happens on a local feature branch. Commits on the branch are
free checkpoints — their timestamps do not matter and the branch is never pushed
to GitHub unless explicitly requested.
Branch naming: feature/m{N}-{slug} — e.g. feature/m11-webhooks.
When the architect approves push, squash the branch to main:
    git checkout main
    git merge --squash feature/m{N}-{slug}
    git commit -m "{conventional commit message}"
    git push
    git branch -d feature/m{N}-{slug}
The squash commit timestamp = push timestamp. This is the only commit that
appears on GitHub. "Commit approved" means: squash to main now. Push follows
immediately after — do not wait for a separate push instruction.
This applies to all three repos (forge core, forge-mcp, forge-cli) when a
milestone touches multiple repos. Each repo gets its own squash commit.

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

**CHANGELOG ownership — non-negotiable:**
Each module owns its own CHANGELOG. Never add submodule release notes as subsections
in the root `CHANGELOG.md`. The separation is strict:
- `CHANGELOG.md` — forge core only
- `forge-mcp/CHANGELOG.md` — forge-mcp only
- `forge-media/CHANGELOG.md` — forge-media only
A brief reference line in the root is acceptable: `Submodules: forge-media v1.0.0 released.`
The detail belongs in the submodule's own file.

**Pre-tag checklist — all must be green before tagging:**
1. `git status --short` returns nothing (working tree clean)
2. `go test ./...` is green (root); `go test ./...` inside each changed submodule is green
3. `CHANGELOG.md` (root) and each changed submodule's `CHANGELOG.md` has an entry for the
   version being tagged
4. For standalone modules that depend on forge core (forge-mcp, forge-media, forge-cli),
   run this full checklist before tagging — every time, without exception:
   - `head -1 go.mod` → must be `module forge-cms.dev/<module-name>` (not any github.com path)
   - `grep forge-cms.dev/forge go.mod` → correct forge version in require block
   - `go mod tidy` → no diff
   - `go build ./...` → green
   - `go test ./...` → green
   - `git status --short` → clean tree
   The module proxy caches go.mod permanently on first fetch — a bad tag requires a new patch
   version. Running this checklist before tagging is the only way to avoid that.

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

The release name is a short (2-4 word) phrase that captures the primary change —
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
