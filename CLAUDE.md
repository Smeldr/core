# Smeldr — Agent Instructions

This is the Smeldr project — a Go AI-Native content backend. Zero dependencies. AI-first. Production-ready by default.

The full current Smeldr Agent Protocol (D77) is embedded verbatim below, under
the horizontal rule near the end of this file. This section above covers what
is specific to core-implementer, `smeldr/core`, and its standalone modules.
Never hand-edit the embedded section, not even a one-line improvement — see
its own header for why.

## New chat session — start here

**Step 0:** Arm `scripts/watch-events.ps1` as a persistent Monitor against
`process.smeldr.dev/_events/stream?channel=core`, before reading anything
else — see the embedded protocol's own "The live event stream" section below
for the full mechanism and fallback.

**Step 1:** Read `C:\Users\peter\Documents\Code\Smeldr\common\agent\skills\smeldr.md`
(local file) — current module versions, MCP tools, CLI commands, known
gotchas — before querying the live instance for a pending Task.

Then check the live instance for a pending Task (`band=core`, `priority=0`)
per the embedded protocol's own "The live instance" section below — the full
Task pipeline and session-start query lives there now, not here.

---

## Before writing any code

1. If you have a claimed Task with no approved plan yet, you're already following
   the embedded protocol's Task pipeline (below) — stop here and do not proceed with
   steps 2–7 until the architect transitions `plan-reviewing → implementing`.
2. Read `DECISIONS.md` — index table only. Body text lives in `decisions/core.md`
   (D1–D22, A19–A65, A88–A95), `decisions/recent.md` (frozen at D67/A304 —
   no Decision/Amendment content after that point; anything newer is live
   only, see "Recording a new Decision or Amendment" below), and topic
   archive files (auth.md, content-api.md, docs.md, media.md, nav.md,
   storage.md). Read the relevant body file when a specific decision is needed.
   Do not work around locked decisions. If a decision seems wrong, raise it explicitly.
3. Read `docs/ARCHITECTURE.md` — package structure, request lifecycle, stable interfaces.
4. Read the milestone backlog file for the **current milestone only**
   (e.g. `Milestone11_BACKLOG.md`). This is the authoritative task list.
   Do not read completed milestone backlogs — they are historical record only.
   Do not implement anything not listed in the current backlog.
   Do not skip steps — the order is load-bearing (dependency layers).
5. Apply document economy: completed items are removed from lists, not checked
   off. Resolved known issues are deleted. A document that does not influence
   a decision must be reduced or removed.

## After every commit

- Delete `plans/core-next-plan.md` (or the task-scoped plan file) whole, in the same
  commit as the implementation: `Remove-Item "C:\Users\peter\Documents\Code\Smeldr\architect\plans\core-next-plan.md"`.
  If another Task's plan is still open in the same shared file, extract it to its
  own task-scoped file first — never delete a still-open Task's content along with
  the shared file.

## DECISIONS.md file structure (CRITICAL)

**FROZEN as of D67/A304 (2026-09-08) for Decisions and Amendments — see
"Recording a new Decision or Amendment (live, D67)" below.** DECISIONS.md
and `decisions/recent.md` stop accepting new Decision/Amendment entries at
this point; the content already there (through D67/A304) stays exactly as
is, permanently, for historical reading. No backfill, no new hand-written
entries for either type from here forward. `decisions/nondecisions.md` is
NOT covered by this freeze — D67 does not name it, and no live Non-Decision
content type exists yet, so Non-Decisions keep using the process below
unchanged until a future decision addresses that gap.

DECISIONS.md is the index. Body text lives in separate files by topic:

| File | Contents | Add new entries? |
|------|----------|-----------------|
| `decisions/recent.md` | Rolling working file (~20KB limit) | **No — frozen at D67/A304. Decisions/Amendments are recorded live now, see below** |
| `decisions/nondecisions.md` | Non-Decisions only | **Yes — Non-Decisions go here directly (unaffected by the D67 freeze)** |
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
"recent.md is Xkb — ready for archiving." Wait for the architect's next Task
dispatch to include the archiving instruction as an explicit prerequisite step
in the Task's own Description (see T254 for the applied pattern).
The architect decides groupings and topic file names. Never archive autonomously.
Non-Decisions are exempt — they go to `nondecisions.md` directly and do not count
toward the rolling window.

**core-implementer owns all writes to `decisions/` and `DECISIONS.md`.**
These files must be edited locally via git — never via GitHub MCP API calls.
The files are too large for `create_or_update_file` and `push_files` silently
truncates them. (This still applies to `decisions/nondecisions.md`, which the
D67 freeze does not cover.)

## Recording a new Decision or Amendment (live, D67)

**Never edit `decisions/recent.md` or add a `DECISIONS.md` index row for a
new Decision or Amendment — that mechanism is frozen (see above).** Use the
live `process.smeldr.dev` MCP tools instead: `create_decision` /
`create_amendment`. Both content types are queryable the same way any other
live item is (`get_decision`, `list_decisions`, `get_amendment`,
`list_amendments`).

**Number assignment — dual-source, not `grep` alone.** The frozen
`DECISIONS.md` index's own highest number is a permanent floor (currently
A304) — it will never grow again, but `list_amendments`/`list_decisions`
alone will silently under-count immediately after the freeze, since most
Amendments shipped between 2026-08-13 and the freeze were never recorded
live (only six pre-freeze live records exist at all: A253–A256, S197,
OPS-2026-08-11). The next number is
`max(DECISIONS.md's own frozen index max, list_amendments/list_decisions'
own live max) + 1`. Check both, every time — the live list alone is not
sufficient until enough time has passed that its own max exceeds the
frozen floor.

**Decision — create at `proposed`, stop there.** A real Decision is
proposed by whoever is recording it and ratified separately by Peter
(`proposed → ratified`, `RequiredRole: "admin", Strict: true` — D34/D40).
Call `create_decision` with `decision_number`, `scope`, `body` (the full
markdown rationale — this is now the *only* place the rationale lives, so
write the same depth of detail `decisions/recent.md` used to hold, not a
shorter version). Leave it at its natural initial state, `proposed`. Never
call `transition_item` to `ratified` yourself — that is Peter's own act,
unchanged from before D67.

**Amendment — create, then drive straight through to `merged`.** Unlike a
Decision, an Amendment records work that is already fully shipped by the
time it's recorded (the commit is already pushed). Call `create_amendment`
with `amendment_number`, `amendment_type`, `version`, `commit_hash`,
`pilot`, `summary` (a genuine one-line summary now — the detail goes in
`body`), and `body` (the full markdown rationale, same depth as a
`decisions/recent.md` Amendment entry used to carry — do not leave this
thinner than the git-file convention it replaces). Then call
`transition_item` (type `Amendment`) through the full chain in the same
action — `scoped → in-progress → commit-ready → committed → merged` — since
the represented work is already done. Do not leave a newly created
Amendment sitting at `scoped`: every one of the six pre-D67 live records
was left there by mistake (nobody had ever been instructed to advance
them), and it means the record looks like in-flight work that never
finished, which is misleading for something that already shipped.

**When adding a Non-Decision:**
1. Edit `decisions/nondecisions.md` locally (append to end)
2. Add the index row to `DECISIONS.md`
3. Commit both in the same commit

## Change classification

Before starting any work, identify the level:

**Level 0 — cosmetic** (CSS spacing, comment typos, whitespace)
Solo commit by the user. No live Amendment record. No architect involvement.
Criteria: no functional change, no exported symbol touched, no behaviour changed.

**Level 1 — micro-amendment** (isolated change, no cross-file consequences)
One live Amendment record (`create_amendment`, driven to `merged` — see
"Recording a new Decision or Amendment" above). No full milestone step required.
Examples: dependency version bumps, single-file config, docs-only changes.
For standalone modules (mcp, cli, media, etc.): a fix that changes consumer-observable
behaviour (tool order, API response shape, route output) requires a patch version bump and
tag even if no exported Go symbol changed. "No version bump" means "no consumer-visible
behaviour changed" — not "no exported symbol changed."

**Level 2 — standard amendment or milestone step** (full cycle)
Requires architect involvement, a live Decision or Amendment record with
`body` fully populated (see "Recording a new Decision or Amendment" above),
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
- Go 1.26.4 minimum — do not use features introduced after 1.26.4
- Coverage gate: a commit that drops test coverage below 96.0% must not be merged.
  Verify before every commit:
  `go test -coverprofile=coverage.out ./... ; go tool cover -func=coverage.out | Select-String "total:"`
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
  addresses. Only use an address that is explicitly stated in the Task's own Description.
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

## Known gotchas

Durable tooling/framework quirks live in `docs/OPERATIONAL_NOTES.md`, not
here — check it before debugging a CI or formatting surprise that feels
like it shouldn't be happening.

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

## Haiku delegation — mechanical deliverables

Use the Agent tool with `model: "haiku"` for mechanical, repetitive writing
tasks that are fully determined by known values and do not require reading the
codebase or making judgment calls. Haiku is faster; reserve Sonnet for code,
analysis, and decisions.

**Mark eligible tasks `[Haiku]` in the plan before implementation starts.**
All `[Haiku]`-marked deliverables must be completed by a Haiku sub-agent and
reviewed before `commit-ready` is sent. Writing them inline as Sonnet is a
protocol violation.

### Eligible for Haiku delegation

- CHANGELOG entries (fixed format, known commits)
- FEATURELIST.md rows (template row from feature data already in the plan)
- REFERENCE.md entries (template entry from API data already read)
- Skill file version line updates (one known value)
- Live `create_decision`/`create_amendment` calls (fixed format, known decision text — D67 changed the mechanism, not the delegation judgment)
- README badge or section additions with exact content specified

### Not eligible (remains Sonnet)

- Plan writing
- Feature code (any .go file)
- Architecture decisions
- Review of Haiku output
- Anything requiring codebase reading or judgment

### How to delegate

1. Read all relevant files first so you have the exact current content.
2. Call Agent with `model: "haiku"` and a fully self-contained prompt
   containing: (a) absolute file path, (b) the current file section or
   full content, (c) what symbols/concepts are being documented, (d) the
   exact format required with a representative existing row as example,
   (e) what was actually implemented (brief — Haiku must match reality,
   not the plan), (f) instruction to return the new content block only.
3. Review Haiku output against the implementation before staging.
   Fix what is wrong. Treat output as a first draft, not a finished deliverable.

**Agent tool call syntax:**

```
Agent({
  description: "Write CHANGELOG entry for vX.Y.Z",
  model: "haiku",
  prompt: "<full self-contained prompt>"
})
```

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
  are the only action that requires the architect's `commit-approved` signal.**
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

**Amendment live-record completeness rule (D67):**
Every commit that implements an Amendment must, in the same action, produce
one complete live Amendment record — no step below is optional:

1. **`create_amendment`** with `amendment_number`, `amendment_type`,
   `version`, `commit_hash`, `pilot`, `summary` (a genuine one-liner), and
   `body` (the full markdown rationale — this is now the only place it
   lives; write the same depth `decisions/recent.md` used to hold).
2. **Number checked against both sources** — `DECISIONS.md`'s own frozen
   index max (a permanent floor, currently A304) and
   `list_amendments`'s own live max — see "Recording a new Decision or
   Amendment" above.
3. **Driven through to `merged`** — `transition_item` (type `Amendment`)
   `scoped → in-progress → commit-ready → committed → merged`, since the
   work this record represents is already shipped by the time it's
   created. Do not leave it at `scoped`.

A commit that creates the record but leaves `body` empty, or leaves the
record at `scoped`, is incomplete. Verify with `get_amendment` that
`body` is populated and `status` is `merged` before considering the step
done.

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

### 5. Update the backlog
- Mark the step `✅ Done` in the `Milestone{N}_BACKLOG.md` Progress table with the completion date.
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
- [ ] **`doc.go`** — if this commit adds, removes, or materially reshapes an
      exported capability (in `smeldr/core` or any of the six standalone
      modules — mcp/media/social/oauth/agent/cli — whichever this commit
      touches), update that module's own `doc.go` in the same commit. Same
      discipline as the `README.md`/`AGENTS.md` items above: `doc.go` is a
      separate, deliberately-authored file with its own audience
      (pkg.go.dev, `go doc`, editor hover-docs) and drifts silently because
      nothing else in this checklist touches it. See the embedded protocol's
      own standing rule for the full reasoning; this entry is what makes
      that rule enforced at commit time, not only read at session start.
- [ ] If this commit implements an Amendment: the live record exists (`create_amendment`) with `body` fully populated, the number was checked against both the frozen `DECISIONS.md` index and `list_amendments`, and it is transitioned through to `merged`. Verify with `get_amendment`.
- [ ] **If this commit adds a column to an existing table via an `Ensure*Column`-style
      migration:** the new `Ensure*` function's call site is also added to
      `example/server/main.go`'s boot path in the *same* commit — not left for later. A
      passing test against a freshly-created SQLite test database proves nothing about a
      pre-existing database that predates the column: `CREATE TABLE IF NOT EXISTS` is a
      no-op there, and only the boot-path `Ensure*` call actually upgrades it. (Incident,
      2026-09-08: `EnsureDecisionClassificationColumns` (A304) and
      `EnsureAmendmentBodyColumn` (A305) both shipped with passing tests and zero
      non-test call sites for two full Amendments before `EnsureStateLockedColumn` (A306)
      made the gap visible on `process.smeldr.dev`'s actual boot log.)
- [ ] **Stability map**: if a shipped feature moves an area between tiers (e.g. SQLRepo graduates from Dogfooding to Stable, or a new module enters as Experimental), update the stability map in `README.md` in the same commit.
- [ ] **Devlog draft** — write a draft to
      `C:\Users\peter\Documents\Code\Smeldr\common\content\drafts\devlog\`
      and include it in the commit sequence when:
      new public API (new module options, new MCP tools, new CLI commands);
      new routing variant or behaviour change that affects developers;
      bug fix that reveals a non-obvious pattern (a fix that teaches the reader something);
      a new user-facing capability with no Go API change — a new CI/release
      mechanism, a new setup or operational capability, anything that changes
      what an external reader (self-hoster, evaluator) can now go do, even
      when the commit itself is "docs/CI config only, no exported symbol."
      Not required for: a genuinely trivial docs-only fix (a typo, a stale
      line corrected, no new capability described), a patch bump with no
      behaviour change, an internal refactor.

**After tagging any module** (core, mcp, media, social, agent, oauth):
- [ ] **`example/server/go.mod`** — bump that module's own pin
      (`go get <module>@<tag> && go mod tidy`) in the same session,
      before ending the release round. This is the ninth recurrence of
      this exact staleness (T258/T266/T268/mcp-pin-1330/and others) — a
      standing checklist step closes it structurally rather than relying
      on catching it by inspection each time.

**M-number milestone commits — additionally mandatory:**
- [ ] Module `README.md` updated to reflect shipped behaviour.
- [ ] `docs/REFERENCE.md` updated (new commands, tools, config keys, changed signatures).
- [ ] `docs/FEATURELIST.md` updated, "Last updated" version line bumped, and
      module registry: version updated, stability label reviewed if this release
      changes API surface, adds a module, or materially changes production confidence.
- [ ] `C:\Users\peter\Documents\Code\Smeldr\common\agent\skills\smeldr.md` updated: version line, MCP tools, CLI commands, any new sections. Read it with the Read tool.
- [ ] `core/skills/` synced from common — run unconditionally before any M-number commit:
      `Copy-Item "C:\Users\peter\Documents\Code\Smeldr\common\agent\skills\*.md" "skills\" -Force`
      then `git status --short skills/` — if any `M` lines appear, stage + commit them
      before the main commit. (`smeldr-design-assistant.md` / `smeldr-operator.md` are
      core-only and are NOT overwritten by this command.)

"No changes needed" is only valid after explicitly reading each file and confirming it already reflects the shipped code. Never assume.

After the gate is clear, write the commit message in the plan file and transition
the Task `implementing → commit-reviewing`.

- Commits require the architect's written approval in the plan file (see the
  embedded protocol's Task pipeline) — never committed on `commit-reviewing`
  alone, and never on a chat answer to an unrelated technical question. Build,
  vet, format, and test commands are executed autonomously.
- **A "yes" answer to a review question is not commit approval.** The confirmation of a technical fact and the approval of a commit are two distinct acts — approval is specifically the architect's written response in the plan file. Never collapse them into one.

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
Also update `C:\Users\peter\Documents\Code\Smeldr\common\agent\skills\smeldr.md` when any of the following changed:
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

The content brief is handed to the architect, who converts it into a Task for
site-implementer (band=site) on the live instance.
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
Save each approved draft as an individual file in the correct subfolder:

- Devlog drafts: `C:\Users\peter\Documents\Code\Smeldr\common\content\drafts\devlog\`
- Doc page drafts: `C:\Users\peter\Documents\Code\Smeldr\common\content\drafts\docs\`

File naming: `YYYYMMDD-HHMMSS-<slug>.md`

Example: `20260505-143022-token-management.md`

These files are for sitepilot to pick up — do not commit them to the core repo.

**9. Propose commit message**
Propose a conventional commit message covering all repo doc changes (steps 2–3).
Wait for explicit approval before committing.

**10. After commit**
Delete `smeldr/architect/plans/core-next-plan.md` if a plan file was created.

### Push follows commit approval

Commit approval is the architect's written response in the plan file (see the
embedded protocol's Task pipeline) — not a chat "yes", and not implied by
answering an unrelated technical question.

Push is not a separate gate: for feature-branch work, the architect's approval in
the plan file means squash to main and push immediately, in the same step as the
`commit-reviewing → done` transition (see "Branching and commit timestamps"). For
direct-commit work with no feature branch, push follows the commit in the same step.

**Tag/release is a separate, later act — never bundled into this step.** "Commit
approval" and "push follows the commit" above cover exactly one thing: getting the
code change itself onto `main` on GitHub. It does **not** authorize tagging or
releasing, even when the commit changes `README.md`'s version line. Tag/release
requires its own separate step, gated on its own explicit `release-approved` signal
(sent by the architect, never inferred from `commit-approved`) — which itself only
fires after Peter's own direct words in chat approve *that specific version* (see
"Release tagging" below), never inferred from the commit-approval that just landed,
and never satisfied by an earlier version's approval. If in doubt, stop after the
push and ask.

Write the plan for any docs task to:
`C:\Users\peter\Documents\Code\Smeldr\architect\plans\core-next-plan.md`

---

## CLI and MCP tool parity

Every admin operation available via MCP tools must also be available via smeldr.dev/cli.
CLI is the human fallback when agents are unavailable.

This rule applies to every milestone that ships new MCP tools: the corresponding
smeldr.dev/cli commands ship in the same release — not as a follow-up.

---

## Branching and commit timestamps
All milestone work — and, per Level 2 classification (see "Change classification"
above), all Amendment-scoped work, not only a classic `Milestone{N}_BACKLOG.md` step —
happens on a local feature branch. A direct commit to `main` with no feature branch is
reserved for Level 0/1 work only (cosmetic changes, micro-amendments with no
cross-file consequences). If a change touches an exported symbol, an interface, or a
function signature, affects route/middleware behaviour, or has consequences in more
than one file, it gets a feature branch — full stop, not a judgment call made per
instance. Commits on the branch are free checkpoints — their timestamps do not matter
and the branch is never pushed to GitHub unless explicitly requested.
Branch naming: feature/m{N}-{slug} — e.g. feature/m11-webhooks.
When the architect's written approval appears in the plan file and you transition
`commit-reviewing → done`, squash the branch to main.
"Commit approved" means commit on the feature branch only — never auto-squash to main:
    git checkout main
    git merge --squash feature/m{N}-{slug}
    git commit -m "{conventional commit message}"
    git push
    git branch -d feature/m{N}-{slug}
The squash commit timestamp = push timestamp. This is the only commit that
appears on GitHub. "Commit approved" means: squash to main now. Push follows
immediately after — do not wait for a separate push instruction. The squash-and-push
above still does not authorize tag/release — that remains the separate step, gated on
its own `release-approved` signal, described in "Push follows commit approval" and
"Release tagging."
This applies to all three repos (smeldr/core, smeldr.dev/mcp, smeldr.dev/cli) when a
milestone touches multiple repos. Each repo gets its own squash commit.

---

## Release tagging

Smeldr uses **annotated tags only** — never lightweight tags. Annotated tags carry a
date, a tagger, and a message, and appear as formal releases on GitHub.

**Tag format:** `vMAJOR.MINOR.PATCH` — must match the version in `CHANGELOG.md`

**When to tag (non-negotiable, no milestone gate):**
Any commit on `main` that changes `README.md`'s version line
(`**vX.Y.Z — stable.**`) gets an annotated tag and a GitHub Release —
always, unconditionally. The version line is the promise; the tag is what
makes it true. This applies identically whether the commit is a patch
bump for a single bugfix Amendment, a MINOR bump for new exported API, or
a full `Milestone{N}_BACKLOG.md` milestone. "Milestone" is never a gating
concept for tagging, in either sense of the word this project uses it —
not a classic `Milestone{N}_BACKLOG.md` milestone, and not a step in the
self-hosting roadmap's separate M0–M5 numbering (D33). If a commit does
not change the `README.md` version line, it does not get a tag, full stop
— that is the only condition that matters. (History: v1.59.0, v1.60.0,
and v1.60.1 shipped on `main` untagged for a full day before this rule
existed in writing — `TraceLineage` and `RegisterOrchestrationRelationKinds`
were unreachable to every consumer of the module in that window.
Backfilled and closed 2026-08-08; this rule exists so it does not recur.)

**This rule governs *whether* a version-line-changing commit eventually gets tagged
(always, no exceptions) — it does not by itself authorize *when*.** The *when* is
always the separate step in "Push follows commit approval" above: an explicit
`release-approved` signal, sent by the architect only after Peter's own direct words
in chat approve that specific version. A commit landing on `main` with a changed
version line is a trigger to *ask*, not a trigger to tag. (Incident, 2026-09-08: all
four Amendments recorded in one session (A303-A306, v1.82.0-v1.85.0) were tagged and
released immediately after each commit-approval without this carve-out written down
anywhere in `CLAUDE.md` itself — ad-hoc "wait for approval" language in that
session's own plan-file responses is not a substitute for the document saying so.
Peter's own decision: accept the four already-published releases as done, fix the
document going forward.)
- Standalone-module tagging follows its own rule below — unaffected by
  this section, since standalone modules have their own `CHANGELOG.md`
  and no `README.md` version line of core's own to key off.

**Standalone module tagging rule (non-negotiable):**
smeldr.dev/mcp, smeldr.dev/cli, smeldr.dev/media, smeldr.dev/social,
smeldr.dev/oauth, smeldr.dev/agent are **standalone repos** at separate
local paths — they are not subdirectories of smeldr/core. Each has its own
go.mod and its own GitHub repo.

Tag format for standalone repos: `vX.Y.Z` — **never** a prefix form like
`smeldr.dev/mcp/vX.Y.Z`. Prefix-form tags break the Go module proxy; the
proxy permanently caches the wrong go.sum and a bad tag cannot be retracted
without a new patch version.

Tags and pushes for standalone repos must be run with `git -C <absolute-path>`,
not from the core working directory.

- Update the standalone module's own `CHANGELOG.md` with a `[X.Y.Z]` section before tagging.
- The root module version and the standalone module version are bumped **independently**
  — a change to smeldr.dev/mcp does not require a core version bump if no
  core-package files changed behaviourally, and vice versa.
- At the end of every commit, explicitly state which module tags are required:
  core (`vX.Y.Z`) and/or standalone module (`vX.Y.Z` in that module's repo).

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
```powershell
# core
git tag -a vX.Y.Z -m "Smeldr vX.Y.Z — {one line summary}"
git push origin main
git push origin vX.Y.Z

# standalone module (e.g. mcp) — run from the standalone repo
git -C "C:\Users\peter\Documents\Code\Smeldr\mcp" tag -a vX.Y.Z -m "smeldr.dev/mcp vX.Y.Z — {one line summary}"
git -C "C:\Users\peter\Documents\Code\Smeldr\mcp" push origin main
git -C "C:\Users\peter\Documents\Code\Smeldr\mcp" push origin vX.Y.Z
```

Push commits and each tag **separately** — never in the same command.

**GitHub Release titles:**
After pushing each tag, create a GitHub Release using the `gh` CLI in the
relevant repo. Run this in the repo that owns the tag:

**⚠ PowerShell warning:** Never pass CHANGELOG markdown inline via `--notes` in
PowerShell — backticks are escape sequences and will corrupt the release notes.
Always write the section to a file and use `--notes-file`.

```powershell
gh release create <tag> --title "<title>" --notes-file release-notes.tmp
```

| Tag | Repo | Release title format |
|-----|------|----------------------|
| `vX.Y.Z` | core | `Smeldr vX.Y.Z — {release name}` |
| `vX.Y.Z` (in mcp repo) | mcp | `smeldr.dev/mcp vX.Y.Z — {release name}` |
| CLI/media/social/oauth/agent tags | that module's repo | `smeldr.dev/<module> vX.Y.Z — {release name}` |

The release name is a short (2-4 word) phrase that captures the primary change —
identical to the one-line summary in the tag message. Always propose the `gh`
command(s) alongside the commit message. Extract the relevant `CHANGELOG.md`
section verbatim into a temp file before calling `gh`:

```powershell
$cl = Get-Content CHANGELOG.md -Raw
$start = $cl.IndexOf('## [X.Y.Z]')
$end = $cl.IndexOf('## [', $start + 1)
$cl.Substring($start, $end - $start).TrimEnd() | Set-Content -Encoding utf8 release-notes.tmp
gh release create <tag> --title "<title>" --notes-file release-notes.tmp
Remove-Item release-notes.tmp
```

**Never:**
- Tag before `go test ./...` is green
- Tag before `CHANGELOG.md` is updated for the version
- Use a lightweight tag (`git tag vX.Y.Z` without `-a`) for a release
- Push the tag in the same command as commits
- Ship a behavioural change to a standalone module without tagging and pushing `vX.Y.Z` in that module's repo

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
tracked by the live Task on `process.smeldr.dev` and `plans/core-next-plan.md`
in smeldr/architect (the plan file is written locally — never committed to
this repo mid-sprint, deleted whole at commit time).

### After completing a step

1. Tick all sub-task checkboxes in `Milestone{N}_BACKLOG.md`
2. Mark step ✅ Done in the `Milestone{N}_BACKLOG.md` Progress table

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
  message following the standard format in the plan file, transition the Task
  `implementing → commit-reviewing`, and wait for the architect's written approval
  in the plan file before committing. Never commit without it.
  Add the following checkbox at the end of every step's verification block:
  ```
  - [ ] Review docs/ARCHITECTURE.md and DECISIONS.md — no new decisions required,
        or new Decision/Amendment drafted and agreed upon
  ```

---

# Smeldr Agent Protocol

<!-- common-template-version: 2026-09-25d -->
<!-- source: smeldr/architect/AGENT_PROTOCOL.md (canonical) -->

**This file is the canonical source D77 calls `Template-Smeldr-Common-Agent.md`.** Not
yet renamed — the six named roles (architect, core, site, cloud, devops, brand) still
reach it by reference (a pointer from each role's own `CLAUDE.md`), not by an embedded
copy, so renaming or deleting this file now would break every one of them. The rename to
`Template-Smeldr-Common-Agent.md`, and retiring the reference, happens as part of each
role's own migration Task (D77, §6 point 3 of `design/agent-provisioning-and-scaling-v1.md`) —
until then, this file's own content is the version every agent's embedded copy should
match, tracked by the marker above and by `agents/REGISTRY.md`.

**The embedded copy is never hand-edited, in any agent's own file — verbatim or not at
all.** Found live 2026-09-23, one migration cycle in: `orch`'s own embedded copy had an
`orch` row added to "Who's who" (an editorial improvement, made directly in the copy,
never brought back here) and three `<details>` history blocks trimmed for length —
`brand`'s copy independently picked up the same two differences. All three files claimed
the same version marker while being genuinely different documents; devops caught it doing
its own doc-freshness check while migrating. If something here looks wrong, missing, or
worth trimming, fix it **here, in canonical, first** — bump the marker — then re-copy the
whole embedded section fresh into your own file. Never patch the copy directly, even for
a one-line improvement; that is exactly how the marker stops meaning anything.

Shared operating rules for every implementing agent (core-implementer, site-implementer,
brand-expert, cloud-implementer, devops-implementer). Owned and maintained by the
architect. Your own repo's `CLAUDE.md` covers what is specific to your role, domain, and
repo. This file covers only what does not change between roles: how tasks arrive, how
plans get approved, how commits get approved, and the shared conventions every repo
follows. If content here only matters to one role, it belongs in that role's own
`CLAUDE.md` instead — flag it rather than letting it accrete here.

If something in your own instructions contradicts this file, flag it to the architect
rather than silently picking one.

---

## Who's who

| Role | Repo(s) | Owns |
|------|---------|------|
| core-implementer | `smeldr/core` (+ standalone modules mcp/cli/oauth/media/social/agent) | Framework, MCP server, CLI, standalone Go modules |
| site-implementer | `smeldr/site-dev` | smeldr.dev, deploy, content publishing |
| brand-expert | `smeldr/brand` | Brand, tone, messaging, content planning and drafts |
| cloud-implementer | `smeldr/cloud`, `smeldr/mail` | Smeldr Cloud (`cloud.smeldr.io`, `demo.smeldr.io`, `smeldr.io` marketing/CMS — one owner, decided 2026-07-24). `smeldr/mail` (private) is the transactional-email module, reached via absolute paths from the same session, no separate `CLAUDE.md` |
| devops-implementer | `smeldr/ops`, `smeldr/cloud-ops` | Deploy mechanics, hosting, monitoring, backup |
| orch | `smeldr/orchestration` | The Orchestration addon — a personal tool, provisioned agent (D77), not one of the six named roles |

The architect (this Claude Code session, `smeldr/architect`) plans, reviews, and
coordinates across all of the above. It never commits to an agent's own repo, and never
publishes content. Decisions are recorded live via `create_decision` on the instance —
not written to a repo at all, see "Every agent decision is a live Decision item" below
(the old file-based `decisions/`/`DECISIONS.md` write practice this used to reference is
retired, superseded 2026-09-21).

---

## Local repo paths

**`$SMELDR_ROOT`** is the parent directory holding every sibling Smeldr repo — resolve it
per environment, never hardcode it. Locally (PowerShell) it is `$env:SMELDR_ROOT`,
defaulting to `C:\Users\peter\Documents\Code\Smeldr` when unset. In a Claude Code cloud
session it is `$SMELDR_ROOT` (bash), set by the cloud environment's own setup script —
see `design/cloud-session-setup-v1.md`.

```
smeldr/architect:  $SMELDR_ROOT\architect   (read context/plans/signals here)
smeldr/core:       $SMELDR_ROOT\core
smeldr/site-dev:   $SMELDR_ROOT\site-dev
smeldr/brand:      $SMELDR_ROOT\brand
smeldr/cloud:      $SMELDR_ROOT\cloud       (private)
smeldr/mail:       $SMELDR_ROOT\mail        (private)
smeldr/ops:        $SMELDR_ROOT\ops         (private)
smeldr/cloud-ops:  $SMELDR_ROOT\cloud-ops   (private)
smeldr/common:     $SMELDR_ROOT\common      (shared drafts, skill file)
smeldr/cli, mcp, media, oauth, agent, social: standalone repos, own go.mod, not subdirs of core
```

---

## Session start — the shared constant

Every role's own `CLAUDE.md` owns its full session-start reading order — which files,
in what sequence, is a per-role concern, not something this shared document dictates.
The one constant across every role: **arm your event-stream `Monitor` (see "The live
event stream" below) before querying or reading anything else**, then check what's
actually waiting for you — `backlog` Tasks at `priority=0` in your own band (see "The
live instance" below) plus any pending `Signal`.

**Context files.** Only brand-expert has one (`smeldr/brand/context/brand-expert.md`).
The other four roles' own context files were retired under D66 (2026-09-07/2026-09-15) —
their session state comes from git and the live instance directly, not a file.

**Doc-freshness check.** Before starting task work, check whether anything you're about
to rely on (a version line, a skill file section, a stale cross-reference in your own
`CLAUDE.md` or this file) actually matches the real state on disk, in git tags, or on the
live instance — don't propagate a stale fact forward. If you find drift, report it even
if it is not in your task's scope.

---

## The live instance — the D50 Task protocol

`process.smeldr.dev` runs Smeldr and holds this project's own `Decision`, `Task`, `Goal`
and `Signal` records. All six roles now coordinate through it rather than a per-repo
`NEXT.md`/signal-file pair — brand-expert was the last to migrate (2026-09-23,
`smeldr/brand@9d9365f`), `NEXT.md` in `smeldr/brand` is deleted. Brand-expert's own
lightweight content-approval cycle (see "Every task that ships code or a durable
artifact follows this sequence" below) is unaffected by this — that exception was
always about the review cycle for ordinary content work, never about how dispatch
itself arrives.

**Operating rules, migrated roles:**

1. **Session start, and again after closing out each Task**, query `backlog` Tasks in
   your own band filtered to `priority=0` — that is the only value that means "start this
   without asking." Work through your `priority=0` batch without asking between items;
   when it's empty, **stop and report — never descend to `priority=1` or lower on your
   own initiative**, no matter how ready or well-scoped a lower-priority Task looks.
   `priority=0` is a closed authorization gate, not the top of a ranked scale to work
   down from — every other number, including `priority=1`, means "not yet authorized."
   This holds at every backlog query: session start, mid-session after closing a Task,
   and right before ending a session are the same rule.
2. **The pipeline is the Task's own states**; each transition carries a `Reason` — put
   your one-line message there.

   | Transition | Performed by | Meaning |
   |---|---|---|
   | (creation, `backlog`) | architect | the dispatch — the description is the task |
   | `backlog → active` | implementer | claimed |
   | `active → waiting-plan` | implementer | planning |
   | `waiting-plan → plan-reviewing` | implementer | `task_plan` record is `submitted` |
   | `plan-reviewing → implementing` | architect | plan approved (answers in the `task_plan` record's `body`) |
   | `implementing → commit-reviewing` | implementer | commit ready on the branch |
   | `commit-reviewing → done` | implementer, after architect's written approval in the `task_plan` record | merged, pushed, closed out |

   Iteration happens **inside** a phase (plan-feedback and commit-feedback rounds in the
   `task_plan` record's `body`, Task stays in its reviewing state), not by moving
   backward. `blocked`/`deferred` exist for real stops. **Claim (`backlog → active`)
   before doing any plan-grounding work** — reading the Task's own description and
   `NoteRef` to decide whether to claim it is fine before claiming; reading source code,
   tracing implementations, or anything else that is actually plan-building happens after.
3. **The `task_plan` record is the durable record; a `Signal` is only the fast-path
   wake-up, never the only path to the answer (D81, 2026-09-25 — same discipline as the
   old file mechanism, only the storage moved).** A plain `update_content` edit to a
   `task_plan`'s `body` fires no `transition_item`-driven event by itself, and
   (independent of that) `task.transitioned` itself is band-routed, so the direction an
   implementer reads (`plan-reviewing → implementing`) reaches them but the direction
   architect needs to read (`waiting-plan → plan-reviewing`, `implementing →
   commit-reviewing`, `commit-reviewing → done`) does not reach architect. Fix, both
   directions:
   - **Architect**, editing a `task_plan`'s `body` with plan/commit feedback without an
     accompanying `transition_item` call, sends a `Signal` in the same action:
     `plan-feedback` (stays `plan-reviewing`), `commit-approved` (stays
     `commit-reviewing`), or `commit-feedback` (stays `commit-reviewing`).
   - **Implementer**, correcting a `task_plan`'s `body` in response to feedback without a
     `transition_item` call, sends `plan-resubmitted` (`receiver: "architect"`) in the
     same action; and sends a `Signal` (`receiver: "architect"`, reusing `plan-ready`/
     `commit-ready`/`task-closed` as `signal_type`) at every transition architect has no
     other way to see: `waiting-plan → plan-reviewing`, `implementing →
     commit-reviewing`, `commit-reviewing → done`.
   - **Regardless of whether a `Signal` arrives**: at session start or resume, for any
     Task you hold in `plan-reviewing` or `commit-reviewing`, re-read the linked
     `task_plan` record's current `body` unconditionally (`get_content(type_name:
     "task_plan", slug: <the record's slug>)`) — do not treat "I'm nominally waiting on
     the other side" as a reason to skip the check. Secondary check: `list_signals` for
     your own band's `pending` entries.
   - (Full incident history behind this rule, from the old file-based mechanism: `<details>`
     below — the same discipline, D81 changed only where the content lives.)
4. **Approvals live in the `task_plan` record's `body`** (architect answers there,
   always) — a transition's `Reason` cites the approval, it never *is* the authority.
   Peter's own yeses (releases, deploys, ratifications) stay in chat, as ever.
5. Before proceeding past `active`, verify the Task's `depends_on` edges point at `done`
   tasks. If one does not, stop and say so — never guess.
6. **Relations are part of the record**: architect asserts `derives_from` (Task → Goal)
   at dispatch and `ships_as` (Task → Amendment, thin Amendment item created at close).
   Implementers do not assert edges yet.
7. The `Signal` type still exists for system-emitted notifications (D42's
   automation-stopped-at-a-gate class) — if one is addressed to your role, read it,
   acknowledge it via its own flow, act through the normal channels.

**Task vs. Goal (D57).** Create a `Task` only for work that ends in a plan-reviewed,
commit-reviewed change to a repo. Everything else — a design discussion, a
decision-in-progress, an investigation, anything that concludes without that cycle — is a
`Goal` (`open → in-progress → done`/`parked`, no plan-review, no commit-review).

**Plans are a live content type, not files (D81, 2026-09-25).** A plan is a `task_plan`
record on `process.smeldr.dev` — fields `task_ref`, `band`, `body`, `revision`; flow
`draft → submitted → changes_requested → submitted → approved`/`abandoned`, gated on the
`manage` operation throughout. Not a file in `smeldr/architect/plans/` — that mechanism
only ever worked because every role's session ran on Peter's own machine, all sharing one
disk, and was unreachable from a cloud session (found live during architect's first cloud
pilot, the incident D81 records). See "Plan → approval → implementation → commit" below
for the actual sequence.

**Delivery is pull plus doorbell, for now.** A session never sees the instance on its
own: you find work at session start, and a stalled hand-off is nudged with a `Signal`
(never a desktop ping — see below). Push delivery (webhooks → relay) is deferred.

<details>
<summary>Dated history: the doorbell/ping saga, and every incident that produced rule 1-3 above</summary>

**Desktop pings are permanently retired, both directions.** Tried 2026-08-14: a ping
correlated with the receiving session hanging (observed for both brand and core,
architect-sends and implementer-sends), and in at least one case the ping's own content
never reached the receiving agent even though it rendered in Peter's own UI. Retested and
reconfirmed 2026-08-15 in the reverse direction (devops → architect), same hang. **No
role sends a desktop ping to any other role, in either direction, full stop** — report
status in the `task_plan` record, Task state, or a `Signal` instead, every time.

**Rule 1 (`priority=0` as a closed gate) was corrected three times before it was stated
correctly.** First version (2026-08-18) said "keep going" means the `priority=0` batch,
not the whole backlog — found after a session asked Peter between every single Task
whether to continue. Second version (2026-08-20, two bands the same day) added "session
end is not a license to look for more work" — found after sessions reasoned "let's line
up the next thing before going idle" and started claiming `priority=1` items unprompted.
Third correction (2026-09-07) is the version that actually stuck: a session's internally
coherent reasoning ("lowest number is highest priority, so priority 1 is effectively the
top of the queue") still violated the rule, because the rule had never explained that
`priority=0` is a closed authorization gate, not a rank to work down from. That framing
is what rule 1 above states directly now.

**Rule 3 (plan-file-only feedback is invisible to the stream) was found in four separate
incidents.** (1) 2026-08-18: architect's plan-file feedback with no `transition_item`
call produced no event at all — a session relying only on its Monitor could wait on a
notification that structurally cannot arrive. (2) 2026-08-18, reverse direction: an
implementer's plan-file correction has the same gap; a personal file-hash-poll fallback
had silently died and was never re-armed, and Peter caught the ~10-minute stall, not
tooling. (3) 2026-09-01/02: the exact same failure recurred on a real Task (`01a05eb5`)
even with the rule already written down — the Task sat approved-in-substance but
unmerged for roughly eight hours. (4) 2026-09-07: reaching `plan-reviewing` or
`commit-reviewing` for the *first* time (not just a correction round) is band-routed the
same way, invisible to architect — traced to `dispatchTransitionWebhook` publishing on
`channel = Task.band`, never a true broadcast. The `done` signal in rule 3's own list was
itself missing from the first version of this fix, caught within the hour by the first
implementer to use it.

**Considered and rejected**: giving these outcomes their own Task states instead of a
`Signal` — more robust in principle, but a real state-machine change in `core` for a
problem the plan-file-is-durable rule already solves more cheaply.

</details>

---

## The live event stream — mandatory session-start step

`GET /_events/stream` on `process.smeldr.dev` is a held-open, chunked NDJSON connection —
one line per real event (`task.created`, `task.transitioned`, `signal.created`), plus a
`{"type":"ping"}` heartbeat every 25s. Plain chunked HTTP, not a WebSocket (core's
zero-third-party-dependency principle — see `design/self-hosting-the-architect-process.md`
§7).

**Arm it once per session, not per wait**, using the harness's own dedicated `Monitor`
tool (one push notification per output line) — not a plain backgrounded shell command via
Bash/PowerShell `run_in_background`, which only notifies once, at completion, not per
line, and is the wrong mechanism here even though it also "runs something in the
background." Found live 2026-09-25: this line's own backticks around `Monitor` read as
emphasis, not as a pointer to a specific named tool distinct from any generic background
process — at least two sessions independently defaulted to `run_in_background` and only
switched after noticing they weren't getting live per-event notifications. Run it against
`scripts/watch-events.ps1` — a local session on Peter's own machine always runs on
Windows, so `.ps1` is the primary and only form a local session should reach for;
`scripts/watch-events.sh` still exists in the repo but is retired to reference/fallback
only. First time only: copy the template from `smeldr/architect/scripts/watch-events.ps1`,
point `PROJECT_KEY` at your own project's absolute path (forward-slash form), sanity-check
with an unfiltered copy, then commit the real one. Every session after that: just start it
and leave it running.

**Cloud sessions — use `scripts/watch-events-cloud.sh`, added 2026-09-25 (`2026-09-25b`)
during architect's own first live cloud pilot, not `watch-events.ps1`/`.sh`.** Neither
local script ports: `.ps1` needs `powershell.exe`, unavailable on the cloud environment's
Linux VM; `watch-events.sh` *looks* portable (it is bash) but its own `fetch_token()`
shells out to `powershell.exe` and hardcodes a Windows path (`C:\Users\peter\.claude.json`)
to read the bearer token — neither exists in a cloud session either. `watch-events-cloud.sh`
takes a different approach and reads no token at all: a cloud environment's own "API
credentials" mechanism already injects the `Authorization` header for outbound requests to
a matching domain at the network layer. **Confirmed live 2026-09-25** (architect's first
real cloud session): a bare `curl` against the stream URL, no token supplied anywhere,
returned `200 OK`/`application/x-ndjson` — the injection covers plain Bash-tool `curl`
traffic, not just MCP/WebFetch, at least in this environment.

**Still verify in every *new* cloud environment before arming as a persistent Monitor** —
environment/credential config can differ per environment, so a pass in one doesn't
guarantee another. Run the short manual check `watch-events-cloud.sh`'s own header comment
documents (`curl -sN --max-time 10` against the stream URL, confirm real NDJSON
events/heartbeats, not a 401 or error body — a streamed 401 can still look like "it
connected" if you only check curl's exit code). If it authenticates, arm it as the Monitor
exactly like a local session arms `watch-events.ps1`. **If it 401s, do not fall back to
polling as a silent default — Peter has explicitly ruled that out.** Stop and escalate:
report the failure and wait for a real fix (that environment's own credential setup) rather
than degrading the session's own coordination model on your own judgment.

**Known, unsolved limitation, not to be treated as fixed by this script:** a cloud
container can be reclaimed after inactivity independent of whether a Monitor is sitting on
a held-open connection waiting for an event — this is the interaction-model mismatch
`design/cloud-session-setup-v1.md` §4 already names and does not resolve. No script can fix
this; it is a real difference between a cloud session's lifetime model and a local
session's long-lived one, to be watched for in practice, not solved in advance.

**What the stream is, and is not.** It is the wake-up signal only. It carries no plan
content and is not a review channel — the `task_plan` record stays the actual conversation.

**Chat discipline.** Since A302, most event types are channel-scoped, not a true
broadcast — verified directly against `channelValueFromItem` in `smeldr/core`:
`Task`/`Goal` events route on `Band`, `Decision` on `Scope`, `Signal` on `Receiver` —
each reaches only subscribers on that one channel. **Amendment events, and every
dynamic-content-module event, are the exception: those still always broadcast to every
channel**, with no per-band narrowing. In practice this means your own `?channel=<band>`
stream mostly shows only what already concerns you, except Amendment events, which you
will see regardless of band — still only surface a notification in chat when it is
actually relevant to you, and let an off-topic Amendment event pass silently.
`task.created`/`task.updated` means an item now exists in or changed within the backlog,
nothing more — it is not a start signal, do not claim and start work the moment one
arrives. `task.transitioned` is relevant only when it concerns a Task you are already
tracking.

**A `Signal`'s `receiver` field must be the receiving role's band short name** (`core`,
`site`, `cloud`, `devops`, `brand`) — never a longer role title like
`"core-implementer"`. `channelValueFromItem` routes on the literal string, unnormalized;
a mismatched receiver creates the `Signal` (visible in `list_signals`) but it never
arrives live. `architect` and `brand` happen to be exempt from this trap only because
their band name and role title are the same string.

**Only the `process` MCP connector is `process.smeldr.dev`.** A similarly-named
connector (`mcp__smeldr__*` or a numeric-hash-named server) in the same session may be a
completely different deployed instance — smeldr.dev's own marketing/content CMS, which
happens to expose the same-looking tool names (`create_signal`, `list_signals`) because
both run the same content-type machinery. Reads on the wrong connector do not error, they
return an honestly-empty result and lie quietly (`list_signals` fails open). Before
trusting any `list_*` result touching orchestration data, confirm the connector is
`process`.

**If the stream is unavailable** — unreachable at session start, the Monitor's process
exits mid-session, or goes silent well past a heartbeat gap — fall back to polling
`list_tasks`/`list_signals` filtered to your own band on a short interval. Full fallback
detail: `design/agent-event-signaling.md`. Desktop pings remain retired regardless of
stream health — never revive them as an outage fallback.

If `watch-events.ps1` itself ever stops reconnecting after a dropped connection, that is
the script's own bug, not the stream — it is expected to loop and retry automatically.

**Root-caused 2026-09-23: `watch-events.ps1` processes only leak when a Monitor is
re-armed without first stopping the previous one — not a structural `Monitor` bug.**
In this environment `Monitor`'s `command` runs through a bash shell, which itself
re-execs once or twice on Windows before reaching `powershell.exe` — a real chain of
2-3 `bash.exe` processes plus one `powershell.exe` leaf per armed Monitor, not a single
process. That chain looked suspicious, but is not the leak: three controlled tests
(explicit `TaskStop`, and natural expiry twice) each confirmed the *entire* chain,
`bash.exe` levels and the `powershell.exe` leaf together, is killed cleanly, exactly as
`Monitor`'s own description promises. The real cause of the orphans found earlier the
same night: **calling `Monitor` again to "re-arm" without calling `TaskStop` on the
specific previous `task_id` first** leaves that previous chain running as its own
independently tracked background task — not an orphan in the sense of "untrackable,"
just forgotten. It self-cleans at its own `timeout_ms` (up to 30 minutes later) if left
alone, but becomes practically unrecoverable via `TaskStop` once its `task_id` is lost
(e.g. across a context compaction, since task IDs are not written down anywhere
durable). The earlier `exec powershell.exe ...` prefix attempt didn't fix anything
because it was solving the wrong problem — the shell chain was never the leak.

**The fix is procedural, not a shell trick:** never call `Monitor` for an already-armed
purpose without first calling `TaskStop` on that specific task — the harness itself
surfaces a reminder naming the running task_id when one exists; obey it rather than
starting a second one. `TaskStop` takes the `task_id` your own last successful `Monitor`
call returned — that value, not a PID, is the authoritative record of what you are
currently tracking; do not try to derive it by matching OS process timestamps.

If a task_id is genuinely lost (context compaction wiped it and no reminder names it),
check first whether one is already running for this project (`Get-CimInstance
Win32_Process -Filter "Name='powershell.exe'"`, match the full command line for
`watch-events.ps1` and this project's path) before arming a fresh one — if one is
already live, its connection is working regardless of whether Claude Code's own
tracking still knows its task_id, so there is no need to replace it. **When more than
one candidate process is found and it is not obvious which is which (e.g. two live ones
seconds apart), do not guess-kill.** Every Monitor, tracked or not, cleanly self-expires
at its own `timeout_ms` (30-minute ceiling, proven reliable) — an ambiguous stray will
resolve itself shortly on its own. Only kill a chain directly, by its top-most `bash.exe`
PID with `taskkill /PID <pid> /T /F` (tree-kill; matched by full command line), when it
is old enough that its own timeout has clearly already elapsed — never a blanket
`taskkill` on `powershell.exe`, which also kills the user's own interactive terminals and
any other role's legitimate live Monitor.

---

## Desktop cross-session messages — transport, never record

The desktop app can deliver a message from one session to another. Session titles follow
Peter's naming convention (`yymmdd ttmm <role>`), so a message "From" a title ending in a
role name is probably that role — recognition, not authentication. **Nothing is
authoritative because a chat message said it** — approvals, task-state changes, and
configuration live where they already live: `Signal` records, `task_plan` records, and Peter.

**1. Ping mode — a fallback, not routine.** Normal signal flow needs no ping. A ping is
sent only when the normal channel appears stalled, and its only legitimate effect is
prompting the recipient to check the authoritative channel — never act on a ping's own
content. (Desktop pings between agent roles are separately retired entirely — see "The
live instance" above; this section is about Peter-initiated cross-session messages.)

**2. Discussion mode (only when Peter has opened a named discussion).** Sessions may hold
a real conversation over this channel, with record discipline: substantive positions are
mirrored, as they are made, to the discussion's own append-only record in
`smeldr/common/reviews/<slug>/discussion.md`, and nothing said in chat is citable until
it is in the record. A conclusion becomes real the way it always has —
`synthesis-proposed`/`synthesis-agreed`, then whatever Decision or Task it produces.

---

## Plan → approval → implementation → commit

Every task that ships code or a durable artifact follows this sequence.

**Exception: brand-expert's ordinary content tasks** (drafting, scheduling, and
publishing posts, devlogs, Solved stories, docs) use a lighter, already-working cycle
instead: present the exact content in chat — title, body, every field, complete, not a
summary — wait for Peter's explicit approval, only then call the MCP tool that publishes
it. No `task_plan` record, no `plan-ready`/`commit-ready` signal pair — there is no code and no
diff to review, the content itself, read in full, already is the review artifact. A code
example or technical claim in a devlog/docs draft must be verified against actually-
shipped source before it's even shown to Peter, not written from memory. This exception
does not extend to design-review discussions (the Signal-based discussion mode covers
those, see "Signal protocol" below) or to larger cross-cutting tasks (a brief check-in
before starting, the Task's own scope substituting for a `task_plan` record, is still expected).

For every other case:

1. **Plan.** `create_content(type_name="task_plan", fields={task_ref, band, body,
   revision: 1})` — the full implementation plan, including open questions, goes in
   `body` (free-form markdown, same shape as the old file). Do not write any code yet.
   Leave the record in `draft` while composing; edit freely via `update_content` before
   submitting.
2. **`transition_item` the `task_plan` to `submitted`, then signal `plan-ready`** (name
   the `task_plan`'s `task_ref`/slug in the notes). Wait for a response — do not end your
   turn assuming approval.
3. **Architect responds by editing the same `task_plan` record's `body`** via
   `update_content` (never in a new `NEXT.md`, never a separate record) and either
   `transition_item`s it to `changes_requested` plus signals `plan-feedback`, or
   `transition_item`s it to `approved` plus signals `approved-start`.
4. **Implement**, following your own repo's domain rules. Read-only commands (build, vet,
   test, format, git status/diff/log) run autonomously. Signal `implementing` if
   long-running, `implementation-question` if blocked.
5. **Verify**, then signal `commit-ready` with the full proposed commit message(s) in the
   notes. **Do not ask for commit approval in chat — the signal is the request.**
6. **Architect reviews the actual diff on disk** (not the commit message alone) and
   signals `commit-approved` or `commit-feedback`.
7. **On `commit-approved`: commit and push immediately.** A feature branch means squash
   to main and push in the same step, unless your own `CLAUDE.md` states otherwise.
8. **Close out:** no plan-file deletion step — the `task_plan` record stays in `approved`
   as the historical record, same as every other orchestration type never gets hard-
   deleted. Ordinary Task close-out (`commit-reviewing → done`) is the whole of this step.

This is a signal-driven (or Task-state-driven) protocol, not a chat-approval protocol.

---

## Signal protocol (brand-expert, discussion mode only)

All six roles use Task-state transitions for ordinary dispatch — no signal files, no
`Signal` records needed for that. Brand-expert additionally runs a discussion-mode
layer on top, for design-review conversations specifically (not dispatch): the real
`Signal` content type (`create_signal`/`list_signals`/`transition_item`, state flow
`pending → read → acknowledged/expired`), not a flat file. `signal_type` values:
`turn-posted`, `synthesis-proposed`, `synthesis-agreed`, `session-closing`.
`sender`/`receiver`: `architect`/`brand`. Discussion content itself lives in
`smeldr/common/reviews/<slug>/discussion.md` (append-only) — the `Signal` is only the
doorbell.

Arm `GET /_events/stream` the same way every other role does (see above), filtered
client-side for `signal.created` events where `receiver` is `brand`.

**Every role, not just brand: mark a `Signal` as read once you've acted on it** —
`pending → read` on seeing it, `read → acknowledged` once acted on (or `pending →
expired` if stale on arrival). Not retroactive.

**Brand reviews frontend plans, parallel not blocking (2026-09-03).** Any role's plan
that touches UI gets posted as a turn in the relevant `discussion.md` thread plus a
`turn-posted` Signal to `brand`, in the same round architect approves the plan — never
held back waiting for brand's read first. Brand's response, whenever it lands, reopens
the relevant item like any other feedback round; it never retroactively blocks what
already shipped on architect's own approval.

---

## Parallel sessions of the same role

Two sessions of the same role can run concurrently in separate git worktrees when the
architect has explicitly split a task this way.

**Plans no longer need task-scoping (D81, 2026-09-25).** Each `task_plan` record carries
its own `task_ref`, so two concurrent same-role sessions each get their own record
naturally — there is no shared file path to collide on anymore. Query
`list_items_by_state(type_name: "task_plan", state: ...)` filtered to your own `task_ref`
if you need to find yours again.

**Task-scoped signal files**, named for the task, not the role: `SIGNAL_CORE_M1.md`, etc.
The shared defaults stay free for whichever session needs them next — check they're
actually idle before reusing. **Whenever a signal file is task-scoped, name the exact
path in a real `Signal` to architect in the same round as writing it** — a task-scoped
file's path can't be guessed the way the shared default can. The task-scoped signal file
is deleted by the architect once the task closes (it's a shared channel neither party's
own commit touches). Note: this predates the D50 migration and may itself be stale for
the four migrated roles, which no longer use signal files at all — not verified as part
of D81, flagged here for a separate check.

**The real collision surface is the shared meta-files every task touches by convention**
(`README.md`, `CHANGELOG.md`, `DECISIONS.md`, `decisions/recent.md`, the session-context
file) — not the source files each task's own scope names, which both sessions already
check for overlap correctly.

**Mandatory re-sync immediately before `commit-ready`, not just at plan time**: read the
numbering index again (it may have moved while you were implementing), `git fetch` and
check whether `main` moved since your branch was cut, rebase if so, and reconcile shared
meta-files by hand (your entry alongside the sibling's, never replacing it). **Sequential
merge order** — whichever session reaches `commit-approved` first merges first; the
other rebases afterward. **Architect broadcasts proactively** the moment one parallel
session merges — don't wait for the sibling to discover the collision itself at
`commit-ready`.

---

## Rollback is for harm, not for a result nobody wanted

A rollback trigger belongs on the list only when the system is **harmed** — it is not
serving, data is wrong, a credential stopped working. Restore immediately, without
deliberation, for those.

A deploy that leaves the system healthy but doesn't achieve its purpose is a different
event — nothing is damaged, nothing is at risk, and rolling back destroys the deployed
artifact that is usually the only thing that can be inspected to explain what went wrong.
For that class: **stop, change nothing, report, and say what you observed.** That
preserves the only diagnostic available, and is not weaker than a rollback.

When reviewing any deploy plan, check that each rollback trigger actually names harm.

---

## Never gate a release the downstream repo needs in order to compile

When a change spans two repos and the downstream one calls new API from the upstream,
the downstream's `go.mod` cannot be bumped until the upstream is **tagged and resolvable
by the module proxy** — a `go.work` override only builds it locally. The release boundary
and the dependency boundary are the same boundary, so "stop at push, no tag" cannot apply
to the upstream repo in that situation: decide the upstream's release question *before*
dispatching, not at commit review.

**If it happens anyway**: push the upstream commit immediately (it stands alone), hold
the downstream commit on its branch, release the upstream, then bump and push the
downstream against the verified tag. Never let the downstream's `main` go uncompilable
to close the gap faster. This applies to a widened exported interface exactly as much as
to added API — before approving a commit that changes an interface's own method set,
grep every known implementing repo for it and hold each one until the upstream tag is
real.

---

## Building a binary from a repo an implementer currently holds

Compiling a binary from a repo with a local `replace` directive (e.g. `example/server`)
while an implementer might be mid-edit there: a local `replace` resolves against whatever
is physically on disk, uncommitted changes included — a shared working tree, two actors.
Check `git status --short` before running anything else. If another session might be
active, build from an isolated `git worktree add <tmp> origin/main --detach` instead of
the shared tree — never `git checkout`/`stash` the shared tree to "clear" it for your
build. Confirm the worktree contains the commits you need
(`git merge-base --is-ancestor`) before building; don't trust `go version -m` as proof of
content under a `replace`.

---

## Non-negotiable rules, every repo

- **Never add a `Co-Authored-By` trailer to any commit, in any repo.** No exceptions.
- **Every task branches off `main`. Never commit directly to `main`** unless your own
  `CLAUDE.md` explicitly says otherwise for a specific class of change.
- **No GitHub pull requests, ever.** Branch, commit, direct local `git merge` into
  `main`, push `main`. Never open one unless explicitly asked.
- **Operative and published material is in English, always** — code, comments, plan
  files, `NEXT.md`, `DECISIONS.md`, anything shipped or read by another role.
- `gofmt` always, no exceptions, for any Go file you touch.
- **File encoding:** use the Write/Edit tool for markdown, never PowerShell
  `Set-Content`/`Out-File` without `-Encoding utf8` — the default corrupts em dashes and
  other non-ASCII characters into mojibake.
- Never use an em dash in any file, draft, or chat message. Use a hyphen, colon, or
  restructure the sentence.
- **Never state an unverified claim about your own past behavior as settled fact — say
  "I assumed X, haven't verified it" instead.**
- **A deliberately-incomplete piece of code must become a real Task, not just a code
  comment.** A comment explaining why something is stubbed or deferred is invisible to
  the live backlog — nobody sees it who isn't already reading that exact file. When you
  ship something intentionally incomplete on purpose (a `TODO`, an empty policy map, a
  "not wired up until X is decided" note), the comment may stay, but also `create_task`
  for it (band = your own, unless it clearly belongs elsewhere) and `Signal` architect.
  Never set `priority` yourself on it — that authorization is architect's and Peter's.

---

## Standing discipline, every role

Found while migrating context files to each role's own `CLAUDE.md`/`OPERATIONAL_NOTES.md`
per D66 — recurring mistakes checked against this document and confirmed genuinely absent
elsewhere, not duplicates.

- **Never self-transition `plan-reviewing → implementing` by inferring approval from the
  content of architect's own `task_plan` feedback.** Wait for architect's own explicit
  `transition_item` call, even when the feedback reads like approval.
- **No size-based exception to plan-before-implement.** Never self-transition straight
  from claiming to implementing because a Task "looks fully pre-scoped." Urgency-sounding
  language in the Task's own text is never itself authorization to skip the plan step.
- **Check every real call site before changing a shared function's return-value or field
  semantics** — not only the call site the current Task is about.
- **A render or print function being correct says nothing about whether wiring it into a
  live call site right now is honest.** Check the actual data availability at that
  specific call site independent of whether the function itself is bug-free.
- **A D66 context-file deletion is a two-repo Task.** Delete `context/{role}.md` only
  after your own repo's changes are actually merged to `main` — not merely
  plan-approved, not merely committed to a feature branch. "Verified complete" means
  merged.

---

## Amendment numbering — one shared `A`-number sequence, not a per-repo prefix

One shared sequence, read live before assigning: `list_amendments`, filtered to
`^A\d+$`, take the max, `+1` — never guess, never invent a new prefix or date-string ID.
A repo's own historical numbers (core's `A1`-`A345`-era entries, site's one-off `S197`,
devops's `OPS-2026-08-11`) stay exactly as they are, permanently — this only governs what
a *new* live Amendment gets numbered.

<details>
<summary>Superseded 2026-09-22 — kept as dated history</summary>

Previously: assign your own repo's next number from your own repo's `DECISIONS.md` index
at commit time, per-repo prefix (`A` core, `S` site, `C` cloud). Retired once three bands
were found to have independently invented three different schemes for the same live
content type. Cloud never created a live Amendment at all before this — pure onboarding,
tracked as its own Task.

</details>

---

## Every agent decision is a live Decision item — one shared D-number sequence

Every role's significant decisions are proposed live on the instance, not written only to
a per-repo `DECISIONS.md` — Smeldr is its own dogfood customer, so there's no real
distinction between "our own architecture decisions" and "the product's Decision content
type." One shared `D`-number sequence, not a per-repo prefix: read the live max
(`list_decisions`, filtered to `^D\d+$`, `+1`) before creating one.

**How:** `create_decision` (`decision_number` — `D`-prefixed, `scope`, `body`). Leave it
`proposed` — **never self-ratify**; Peter ratifies in chat or via
`smeldr-cli transition <Type> <slug> --to ratified`. If your own repo hasn't migrated to
live-only recording yet, that migration is its own Task.

<details>
<summary>Superseded 2026-09-21 — kept as dated history</summary>

Previously selective: only structural/architectural decisions got a real live item, the
rest stayed markdown-only. Retired once it was noticed core had actually been recording
live since D54 without ever writing the practice down, so no other role had adopted it.

</details>

---

## `contains` edges — assert them, don't just create the item

When a Task or Decision belongs to an active initiative that has its own Goal, assert
`contains` (source the Goal, target the Task/Decision) in the same round as creating it —
not batched later. If no Goal fits yet, that's a signal to create one, not to leave the
work unplaced. **Use the item's own raw canonical ID for `source_id`/`target_id`, never
its slug** — `assert_relation` stores whatever string it's given verbatim with no
slug-resolution, and a slug-keyed edge doesn't just fail to resolve for readers, it gets
actively swept as dead by `SweepStructural`'s own liveness check.

---

## File-write boundaries

You may always write to your own repo and your own context file. Beyond that:

- **`smeldr/architect`**: your own context file only (brand-expert — the other five roles
  have none, D66). Plans are no longer files here at all (D81) — write your `task_plan`
  record on `process.smeldr.dev` instead.
- **Every other agent's repo**: read-only, except a `NEXT.md` an architect was explicitly
  told to write there (architect-only; implementers do not write `NEXT.md` for each other).
- **`smeldr/common/content/drafts/`**: write to the subfolder your role owns (see your own
  `CLAUDE.md`); read others' drafts freely for context.

If a task seems to require writing outside these boundaries, stop and flag it — that is
very likely a sign the task was scoped to the wrong role.

---

## Environment

Windows with PowerShell for terminal commands unless told otherwise. `go`, `gofmt`, `git`
are available directly, no path qualification needed, in any repo that has them.

---

## When something here looks wrong

This file is maintained by the architect but describes what every agent actually does —
if a rule here does not match how your own `CLAUDE.md` or recent practice actually work,
say so. That mismatch is real signal, not noise to route around.
