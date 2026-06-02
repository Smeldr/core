# Smeldr — Copilot Instructions

This is the Smeldr project — a Go web framework designed for how you
actually think. Zero dependencies. AI-first. Production-ready by default.

## New chat session — start here

Every new chat has no memory of previous sessions. Follow these steps
at the start of every new chat, before doing anything else.

**Step 1 — Read session context:**
Read `C:\Users\peter\Documents\Code\Smeldr\architect\context\corepilot.md` (local file).
This is your state from the previous session: current versions, latest
amendment, active milestone and step, anything deferred.

**Step 2 — Check for a pending task:**
Check whether `NEXT.md` exists in the core repo root (local workspace).
If it exists:
1. Read it and form a full implementation plan (including any questions).
2. Write the plan file locally to:
   `C:\Users\peter\Documents\Code\Smeldr\architect\plans\core-next-plan.md`
   Include the complete plan and any open questions in that file.
3. Notify the user in chat that the plan is ready for review at
   `smeldr/architect/plans/core-next-plan.md`. Do not write any code yet.
4. Wait for explicit approval before implementing anything.
5. At commit time: delete both `NEXT.md` and `plans/core-next-plan.md`
   in the same commit as the implementation.
6. After the commit: write `C:\Users\peter\Documents\Code\Smeldr\architect\context\corepilot.md`
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
     `C:\Users\peter\Documents\Code\Smeldr\architect\plans\core-next-plan.md`**
   - Notify the user in chat that the plan is ready for review at
     `smeldr/architect/plans/core-next-plan.md`. Do not write any code yet.
   - Wait for explicit approval. Do not implement anything until the user confirms the plan.
   - Stop here — do not proceed with steps 2–7.
2. Read session context from `C:\Users\peter\Documents\Code\Smeldr\architect\context\corepilot.md`
   (local file). This is your state from the previous session.
3. Read `DECISIONS.md` — index table only. Body text lives in `decisions/core.md`
   (D1–D22, A19–A65, A88–A95), `decisions/recent.md` (current rolling window),
   and topic archive files (auth.md, content-api.md, docs.md, media.md, nav.md,
   storage.md). Read the relevant body file when a specific decision is needed.
   Do not work around locked decisions. If a decision seems wrong, raise it explicitly.
4. Read `docs/ARCHITECTURE.md` — package structure, request lifecycle, stable interfaces.
5. Read the milestone backlog file for the **current milestone only**
   (e.g. `Milestone11_BACKLOG.md`). This is the authoritative task list.
   Do not read completed milestone backlogs — they are historical record only.
   Do not implement anything not listed in the current backlog.
   Do not skip steps — the order is load-bearing (dependency layers).
6. Apply document economy: completed items are removed from lists, not checked
   off. Resolved known issues are deleted. A document that does not influence
   a decision must be reduced or removed.

## After every commit

- If `NEXT.md` exists, delete it with `Remove-Item NEXT.md -ErrorAction SilentlyContinue`.
  NEXT.md is written locally by the architect and is never committed to git — it is
  always untracked. Do not use `git rm`; it will fail on an untracked file.
- If `plans/core-next-plan.md` exists locally, delete it:
  `Remove-Item "C:\Users\peter\Documents\Code\Smeldr\architect\plans\core-next-plan.md"`
- Update session context: write `C:\Users\peter\Documents\Code\Smeldr\architect\context\corepilot.md`
  locally. Record: current versions, latest amendment shipped,
  current milestone and step, what was deferred or blocked.
  Then commit and push from the smeldr/architect repo:
  `cd C:\Users\peter\Documents\Code\Smeldr\architect ; git add context/corepilot.md ; git commit -m "chore(context): update corepilot after [sprint name]" ; git push`
  Do NOT use GitHub MCP to update this file.

## DECISIONS.md file structure (CRITICAL)

DECISIONS.md is the index. Body text lives in separate files by topic:

| File | Contents | Add new entries? |
|------|----------|-----------------|
| `decisions/recent.md` | Rolling working file (~20KB limit) | **Yes — new decisions go here** |
| `decisions/nondecisions.md` | Non-Decisions only | **Yes — Non-Decisions go here directly** |
| `decisions/core.md` | Archive: D1–D22, A19–A65, A88–A95 | No — archive only |
| `decisions/phase2-archive.md` | Superseded archive (was phase2.md; content now in topic files) | No — archive only |
| `decisions/auth.md` | Archive: D25, A66, D26, A83 | No — archive only |
| `decisions/content-api.md` | Archive: D27, A67, A74, A75, A77 | No — archive only |
| `decisions/docs.md` | Archive: D28, A69–A72, A76, A84–A86 | No — archive only |
| `decisions/media.md` | Archive: A73, D31, A79 | No — archive only |
| `decisions/nav.md` | Archive: D29, D30, A82 | No — archive only |
| `decisions/storage.md` | Archive: A68, A78, A80, A81 | No — archive only |
| `decisions/[topic].md` | Topic files on architect instruction | Only when instructed |

**Archiving rule:** When `recent.md` reaches ~20KB, report at session start:
"recent.md is Xkb — ready for archiving." Wait for NEXT.md with archiving instructions.
The architect decides groupings and topic file names. Never archive autonomously.
Non-Decisions are exempt — they go to `nondecisions.md` directly and do not count
toward the rolling window.

**Corepilot owns all writes to `decisions/` and `DECISIONS.md`.**
These files must be edited locally via git — never via GitHub MCP API calls.
The files are too large for `create_or_update_file` and `push_files` silently
truncates them.

**When adding a new Decision or Amendment:**
1. Edit `decisions/recent.md` locally (append to end)
2. Add the index row to `DECISIONS.md`
3. Commit both in the same commit
4. Never use GitHub MCP `create_or_update_file` or `push_files` for these files

**When adding a Non-Decision:**
1. Edit `decisions/nondecisions.md` locally (append to end)
2. Add the index row to `DECISIONS.md`
3. Commit both in the same commit

**When appending to `decisions/recent.md`:** Append to the end of the file.
Closing lines (e.g. `---`) repeat throughout the file — if editing mid-file,
use enough surrounding context to uniquely identify the insertion point.
Re-read the tail of the file and use a longer unique anchor on match failure.

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
docs/ARCHITECTURE.md check, and explicit user approval before commit.
Criteria: touches an exported Go symbol, interface, or function signature;
affects a route or middleware behaviour; has consequences in more than one file.

When in doubt: Level 2.

## Non-negotiable rules

- Zero third-party dependencies in the `smeldr` core package
- All errors implement `smeldr.Error` — never raw `errors.New`
- **Read `ERROR_HANDLING.md` before writing any code that handles or returns errors,**
  **calls `WriteError`, adds a sentinel, uses `errors.As`/`errors.Is`, or writes**
  **an HTTP response in an error path. The single pipeline rule is non-negotiable.**
- `smeldr.Context` is an interface, not a struct (Decision 21)
- `smeldr.DB` is an interface, not `*sql.DB` (Decision 22)
- Go 1.22 minimum — do not use features introduced after 1.22
- `gofmt` always — no exceptions
- godoc comments on every exported symbol
- A fix or improvement that changes a file **other than** the current step's file
  is an **Amendment**, not a fix. Stop, draft the Amendment, get approval, then implement.
- Every Amendment commit — not just milestone steps — must include an explicit check
  of `docs/ARCHITECTURE.md`. If the amendment adds, removes, or changes any exported symbol,
  interface, file, or behaviour, `docs/ARCHITECTURE.md` must be updated in the same commit.
  Never update `docs/ARCHITECTURE.md` from a plan or backlog description — only from
  verified, running code.
- A step that is deferred or descoped must be documented in `Milestone{N}_BACKLOG.md`
  immediately with the reason and the target milestone. Never silently skip.
- **Email addresses in public documents:** Never infer, guess, or construct email
  addresses. Only use an address that is explicitly stated in the NEXT.md task prompt.
  If a document requires a contact address and none is provided, use the placeholder
  `[contact@example.com]` and flag it in the plan for Peter to fill in.

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

Prefer `smeldr.Verb(Noun)` or `smeldr.Noun` — no abbreviations, no
clever names. A longer but unambiguous name is always better than a
short opaque one.

**Analyse consequences for developer and AI experience before any amendment:**
Before proposing a Decision, Amendment, or architectural change, explicitly
evaluate its impact on:
1. **Call-site syntax** — how does it look when a developer writes it?
2. **README and documentation** — does any documented example break or
   become misleading?
3. **AI generation accuracy** — will AI assistants be able to produce
   correct Smeldr code without consulting docs?
4. **Consistency** — does this pattern align with all existing exported
   symbols, or does it introduce a special case?

Document this analysis in the Amendment before it is agreed upon.
If an amendment breaks a README example, fix the README in the same step.

## Code style

- Single package: `smeldr` — no sub-packages
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

**File encoding:** Always use the VS Code edit tool (`replace_string_in_file` /
`create_file`) to write markdown files. Never use PowerShell `Set-Content` or
`Out-File` without `-Encoding utf8` — PowerShell's default encoding corrupts
em dashes, bullets, and other non-ASCII characters (mojibake).

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
  `go build ./...`, `go vet ./...`, `golangci-lint run ./...`, `gofmt -l .`, `go test ./...`.
  Fix any failures before proceeding. If `golangci-lint` is not installed, skip it
  with a warning — it must not block a commit when the tool is absent.
  Never ask the user whether to run these.
- **NEVER ask, announce, or request approval before running any of the following:
  `go build`, `go vet`, `go test`, `gofmt`, `golangci-lint`, or any read-only PowerShell file
  command (`Get-Content`, `Select-String`, `Get-ChildItem`, `git diff`, `git log`,
  `git status`). Just run them. Do not narrate the process. Only surface results
  when they are unexpected (build failure, test failure, format diff). Commits
  are the ONLY action that requires explicit user approval.**
- Read any file in the workspace automatically — no permission needed.
  Use PowerShell (`Get-Content`, `Select-String`, etc.) or the read_file tool
  to read `DECISIONS.md`, `docs/ARCHITECTURE.md`, milestone backlog
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


**README compile test rule:**
Smeldr maintains `example_test.go` in the root package. Every Example function
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
2. **Body section** — the full Amendment text appended to `decisions/recent.md`.

Both edits must be made locally via git — never via GitHub MCP.
A commit that adds a body without an index row (or vice versa) is incomplete.
Treat these as a single atomic unit: write both, verify with `Select-String`
that both exist, then stage.

### 4. Architecture and decision review
- After verification passes, review `docs/ARCHITECTURE.md` and `DECISIONS.md`.
- Ask: does this implementation reveal a gap, ambiguity, or conflict?
- If yes: draft a new Decision or Amendment and present it to the user before proceeding.
- Check this step's implementation against all previously implemented files: does it
  duplicate logic, diverge from an established pattern, or require a change to another
  file? Any change that crosses a file boundary requires an Amendment — not a fix.
- After each step, consider whether `docs/ARCHITECTURE.md` needs updating: new exported
  symbols, corrected interface locations, changed behaviour, new middleware, or
  planned files that are now implemented. Update it before proposing the commit.
- The step is not complete until the review checkbox is ticked.

### 5. Update the backlog and session context
- Mark the step `✅ Done` in the `Milestone{N}_BACKLOG.md` Progress table with the completion date.
- Write `C:\Users\peter\Documents\Code\Smeldr\architect\context\corepilot.md` locally,
  then commit and push from that repo (see "After every commit" for the command sequence).
- Never batch updates — update immediately after the step is verified.

### 6. Pre-commit documentation gate — then propose commit message

**Complete this checklist before writing the commit message.
All items must be resolved. Do not propose a commit until the gate is clear.**

**Every commit — mandatory:**
- [ ] `README.md` version line (`**vX.Y.Z — stable.**`) matches the version being shipped. Update if behind.
- [ ] No `🔲 Coming in Milestone N` badge remains for a milestone that has shipped.
- [ ] `go test ./...` is green (re-run if any file changed since last verification).
- [ ] `golangci-lint run ./...` is clean, or golangci-lint is not installed (skip with warning).
- [ ] **AGENTS.md** — update when any of the following change:
      content type struct rules (tags, validation, field conventions);
      module option API (new options, changed behaviour);
      MCP tool behaviour (new tools, suppressed tools, changed tool names);
      lifecycle rules or role enforcement;
      token, webhook, or media management tools.
      AGENTS.md ships with Smeldr and is the primary reference for external AI coding
      assistants. It must stay in sync with the developer-facing API.
      Specifically: if this commit touches module wiring API (`smeldr.MCP`, `smeldr.Repo`,
      `smeldr.At`, `smeldr.NewModule`), MCP server wiring (`forgemcp.New`, `mcpSrv.Handler()`,
      `forgemcp.WithModule`), smeldr.dev/media registration (`forgemedia.Register`,
      `forgemedia.NewLocalMediaStore`), token API (`smeldr.NewTokenStore`, `smeldr.SignToken`),
      `smeldr.Config` fields, or `smeldr_format`/`smeldr_description` tag values: verify all
      code examples in `AGENTS.md` are still accurate before committing.
- [ ] If this commit implements an Amendment: both the DECISIONS.md index row and the body section in `decisions/recent.md` are present. Verify with `Select-String`.
- [ ] **Stability map**: if a shipped feature moves an area between tiers (e.g. SQLRepo graduates from Dogfooding to Stable, or a new module enters as Experimental), update the stability map in `README.md` in the same commit.
- [ ] **Devlog draft** — write a draft to
      `C:\Users\peter\Documents\Code\Smeldr\common\content\drafts\devlog\`
      and include it in the commit sequence when:
      new public API (new module options, new MCP tools, new CLI commands);
      new routing variant or behaviour change that affects developers;
      bug fix that reveals a non-obvious pattern (a fix that teaches the reader something).
      Not required for: docs-only commits, patch bumps without behaviour change, internal refactors.

**M-number milestone commits — additionally mandatory:**
- [ ] Module `README.md` updated to reflect shipped behaviour.
- [ ] `docs/REFERENCE.md` updated (new commands, tools, config keys, changed signatures).
- [ ] `docs/FEATURELIST.md` updated, "Last updated" version line bumped, and
      module registry: version updated, stability label reviewed if this release
      changes API surface, adds a module, or materially changes production confidence.
- [ ] `C:\Users\peter\Documents\Code\Smeldr\common\agent\skills\smeldr.md` updated: version line, MCP tools, CLI commands, any new sections. Read it with the Read tool.
- [ ] `skills/smeldr.md` in core repo — copy updated `smeldr/common/agent/skills/smeldr.md`

"No changes needed" is only valid after explicitly reading each file and confirming it already reflects the shipped code. Never assume.

After the gate is clear, write the commit message and present it to the user.

- Commits are the **only** action that requires explicit user approval. Build, vet, format, and test commands are executed autonomously.
- **A "yes" answer to a review question is not commit approval.** The confirmation of a technical fact and the approval of a commit are two distinct acts. Never collapse them into one.

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

Use this workflow for any task that involves updating repo docs (docs/REFERENCE.md,
README.md, docs/FEATURELIST.md) or creating content for smeldr.dev/docs.

This workflow is separate from the standard step workflow. It applies to
docs-only tasks and content tasks — not to code implementation.

**When to use this workflow:** For docs-only tasks and content operations (devlog,
solved stories, doc page drafts) that follow a code commit. Repo doc updates
(README, docs/REFERENCE.md, docs/FEATURELIST.md, smeldr/common skill) are gated in the
standard step workflow (step 6) — complete those before proposing any commit.

The content brief (step 4) is always required for any new M-number milestone.

### On every session start — doc freshness check

Before any other work, check these three files for staleness against the
current codebase and recent amendments:

1. `docs/REFERENCE.md` — does it reflect all current exported symbols and behaviour?
2. `README.md` — is the version line (`**vX.Y.Z — stable.**`) current?
3. `docs/FEATURELIST.md` — does it list all shipped features? Check "Last updated" version.
   Module registry versions and stability labels current?
4. `C:\Users\peter\Documents\Code\Smeldr\common\agent\skills\smeldr.md` — does the version line match current versions?
   Are all MCP tools and CLI commands listed? Read it with the Read tool.

Present any staleness findings to the user before proceeding. Do not silently
skip this check.

### Docs and content task workflow

Every docs or content task follows this sequence:

**1. Propose commit scope**
Before any work: propose what the commit will cover in one sentence.
Wait for approval to proceed. Do not write anything yet.

**2. Repo doc review**
Read docs/REFERENCE.md, README.md, and docs/FEATURELIST.md.
Present what needs updating — specific, concrete findings only.
Wait for feedback before making any changes.

**3. Apply repo doc updates**
Apply agreed changes to docs/REFERENCE.md, README.md, and/or docs/FEATURELIST.md.
Also update `.claude/skills/smeldr.md` when any of the following changed:
- MCP tools or CLI commands (update both sections; verify CLI/MCP parity)
- Config keys (update smeldr.config section)
- New failure modes confirmed in this release (update gotchas)
- Any of the above → bump the version line at the top of the skill file
- Update `C:\Users\peter\Documents\Code\Smeldr\common\agent\skills\smeldr.md` only
  (no copies — all pilots read from smeldr/common directly)
Do not commit yet.

**4. Content brief**
After repo doc updates are applied, write a content brief covering:
- What shipped: plain-language summary (one paragraph)
- Amendment ID (e.g. A93)
- docs/REFERENCE.md section: relevant header
- Devlog: yes/no + suggested angle (or "covered by Axx")
- Solved: which story this feature supports, if any
- Docs: which smeldr.dev/docs pages need updating

Release type guidance:

| Release type | Devlog | Solved | Docs |
|---|---|---|---|
| New milestone (M-number) | yes | possibly | yes |
| New MCP/CLI feature | yes | possibly | yes |
| Bugfix / patch | no | no | only if API changed |
| Doc/infra fix | no | no | no |

The content brief is handed to the architect, who converts it into a sitepilot NEXT.md.
Wait for feedback before proceeding.

**5. Content suggestions (smeldr.dev/docs)**
Based on updated docs/REFERENCE.md, suggest doc page title(s) that should be
created or updated on smeldr.dev.
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

These files are for sitepilot to pick up — do not commit them to the core repo.

**9. Propose commit message**
Propose a conventional commit message covering all repo doc changes (steps 2–3).
Wait for explicit approval before committing.

**10. After commit**
Update `smeldr/architect/plans/core-next-plan.md` if a plan file was created.
Update `smeldr/architect/context/corepilot.md` and push from that repo.

### Never push without explicit permission

Commits require explicit approval (a separate "yes" after the commit message is
proposed — not implied by answering a technical question).

Pushes require a **separate** explicit instruction after the commit is made.
"Commit approved" is not push permission. Always wait for "push it" or equivalent.

Write the plan for any docs task to:
`C:\Users\peter\Documents\Code\Smeldr\architect\plans\core-next-plan.md`

---

## CLI and MCP tool parity

Every admin operation available via MCP tools must also be available via smeldr.dev/cli.
CLI is the human fallback when agents are unavailable.

This rule applies to every milestone that ships new MCP tools: the corresponding
smeldr.dev/cli commands ship in the same release — not as a follow-up.

**Current known gap:** smeldr.dev/cli v0.3.0 has no nav commands despite four MCP nav
tools existing (`list_nav_items`, `create_nav_item`, `update_nav_item`,
`delete_nav_item`). This gap is tracked and will be closed in the nav CLI milestone.

---

## Branching and commit timestamps
All milestone work happens on a local feature branch. Commits on the branch are
free checkpoints — their timestamps do not matter and the branch is never pushed
to GitHub unless explicitly requested.
Branch naming: feature/m{N}-{slug} — e.g. feature/m11-webhooks.
When the architect approves push via a separate NEXT.md, squash the branch to main.
"Commit approved" means commit on the feature branch only — never auto-squash to main:
    git checkout main
    git merge --squash feature/m{N}-{slug}
    git commit -m "{conventional commit message}"
    git push
    git branch -d feature/m{N}-{slug}
The squash commit timestamp = push timestamp. This is the only commit that
appears on GitHub. "Commit approved" means: squash to main now. Push follows
immediately after — do not wait for a separate push instruction.
This applies to all three repos (smeldr/core, smeldr.dev/mcp, smeldr.dev/cli) when a
milestone touches multiple repos. Each repo gets its own squash commit.

---

## Release tagging

Smeldr uses **annotated tags only** — never lightweight tags. Annotated tags carry a
date, a tagger, and a message, and appear as formal releases on GitHub.

**Tag format:** `vMAJOR.MINOR.PATCH` — must match the version in `CHANGELOG.md`

**When to tag:**
- Every milestone that ships a version bump (`v0.x.0`) gets a tag
- Patch releases (bug fixes, no API change) get a tag
- Amendments alone do not get a tag unless they ship with a milestone

**Sub-module tagging rule (non-negotiable):**
Any commit that modifies files under `smeldr.dev/mcp/` (or any other sub-module
directory) in a way that affects behaviour **must** also produce a sub-module tag.
The sub-module tag uses the prefix convention: `smeldr.dev/mcp/vX.Y.Z`.
- Update `smeldr.dev/mcp/CHANGELOG.md` with a `[X.Y.Z]` section before tagging.
- The root module version and the sub-module version are bumped **independently**
  — a patch to `smeldr.dev/mcp/mcp.go` does not require a root version bump if no
  root-package files changed behaviourally, and vice versa.
- At the end of every commit, explicitly state which module tags are required:
  root (`vX.Y.Z`) and/or sub-module (`smeldr.dev/mcp/vX.Y.Z`).

**CHANGELOG ownership — non-negotiable:**
Each module owns its own CHANGELOG. Never add submodule release notes as subsections
in the root `CHANGELOG.md`. The separation is strict:
- `CHANGELOG.md` — smeldr/core only
- `smeldr.dev/mcp/CHANGELOG.md` — smeldr.dev/mcp only
- `smeldr.dev/media/CHANGELOG.md` — smeldr.dev/media only
A brief reference line in the root is acceptable: `Submodules: smeldr.dev/media v1.0.0 released.`
The detail belongs in the submodule's own file.

**Pre-tag checklist — all must be green before tagging:**
1. `git status --short` returns nothing (working tree clean)
2. `go test ./...` is green (root); `go test ./...` inside each changed submodule is green
3. `CHANGELOG.md` (root) and each changed submodule's `CHANGELOG.md` has an entry for the
   version being tagged
4. For standalone modules that depend on smeldr.dev/core (smeldr.dev/mcp, smeldr.dev/media, smeldr.dev/cli),
   run this full checklist before tagging — every time, without exception:
   - `head -1 go.mod` → must be `module smeldr.dev/<module-name>` (not any github.com path)
   - `grep smeldr.dev/core go.mod` → correct core version in require block
   - `go mod tidy` → no diff
   - `go build ./...` → green
   - `go test ./...` → green
   - `git status --short` → clean tree
   The module proxy caches go.mod permanently on first fetch — a bad tag requires a new patch
   version. Running this checklist before tagging is the only way to avoid that.

**Tag and push sequence:**
```
git tag -a vX.Y.Z -m "Smeldr vX.Y.Z — {one line summary}"
git push origin main
git push origin vX.Y.Z
# if smeldr.dev/mcp also changed:
git tag -a smeldr.dev/mcp/vX.Y.Z -m "smeldr.dev/mcp vX.Y.Z — {one line summary}"
git push origin smeldr.dev/mcp/vX.Y.Z
```

Push commits and each tag **separately** — never in the same command.

**GitHub Release titles:**
After pushing, create a GitHub Release for each tag from
`github.com/smeldr/core/releases`. Title each release as follows:

| Tag | Release title format |
|-----|----------------------|
| `vX.Y.Z` | `Smeldr vX.Y.Z — {release name}` |
| `smeldr.dev/mcp/vX.Y.Z` | `smeldr.dev/mcp vX.Y.Z — {release name}` |

The release name is a short (2-4 word) phrase that captures the primary change —
identical to the one-line summary in the tag message. Always propose the GitHub
Release title(s) alongside the commit message. Paste the relevant
`CHANGELOG.md` section as release notes.

**Never:**
- Tag before `go test ./...` is green
- Tag before `CHANGELOG.md` is updated for the version
- Use a lightweight tag (`git tag vX.Y.Z` without `-a`) for a release
- Push the tag in the same command as commits
- Ship a behavioural change to `smeldr.dev/mcp/` without a `smeldr.dev/mcp/vX.Y.Z` tag

---

## Milestone planning process

Before implementing any milestone, a dedicated backlog file must be created and
agreed upon. This file is the single source of truth for that milestone's detail.

### Planning documentation

Smeldr uses one tier of planning documentation per active milestone:

**`Milestone{N}_BACKLOG.md` (repo root)**
- Full implementation plan for one milestone only
- Contains numbered sub-sections (N.M), atomic checkboxes, verification blocks,
  and the architecture review checkbox
- The authoritative task list — implementation follows this file exactly
- Updated after every step: tick all checkboxes, mark step ✅ in Progress table

Delivery history lives in `CHANGELOG.md`. Current state and active sprint are
tracked in `context/corepilot.md` and `plans/core-next-plan.md` in smeldr/architect
(written locally — never committed to this repo mid-sprint).

### After completing a step

1. Tick all sub-task checkboxes in `Milestone{N}_BACKLOG.md`
2. Mark step ✅ Done in the `Milestone{N}_BACKLOG.md` Progress table
3. Write `context/corepilot.md` and push from smeldr/architect

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
root clean for developers who clone Smeldr to use it, not to study its
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
  block passes, review `docs/ARCHITECTURE.md` and `DECISIONS.md` and ask:
  - Does the implementation reveal a gap, ambiguity, or conflict in an existing decision?
  - Did any implementation choice introduce a pattern or constraint not yet captured?
  - Does the file's dependency graph still match the rules in `docs/ARCHITECTURE.md`?
  If yes to any of the above, a new Decision or Amendment must be proposed and agreed
  upon before the next step begins. The step is not complete until this review is done.
- **Every step ends with a commit.** After the architecture review, write a commit
  message following the standard format and wait for user approval before committing.
  Never commit without approval.
  Add the following checkbox at the end of every step's verification block:
  ```
  - [ ] Review docs/ARCHITECTURE.md and DECISIONS.md — no new decisions required,
        or new Decision/Amendment drafted and agreed upon
  ```
