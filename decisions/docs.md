# Forge Decisions — Documentation

Archived from decisions/recent.md on 2026-05-17.
Read on demand. See DECISIONS.md for the full index.

---

## Decision 28 - forge-cli: operator CLI submodule

**Date:** 2026-04-07
**Status:** Agreed
**Files:** `forge-cli/` (new submodule)

**Context:**
Operators need a scriptable way to manage content and tokens on a running Forge
instance from a terminal or CI/CD pipeline. The REST API and MCP endpoints are
already stable. A thin CLI wrapping those endpoints is the minimal solution.

**Decision:**
Add a new Go submodule `forge-cms.dev/forge-cli` (package main).

### Design constraints

- Zero third-party dependencies (stdlib only: net/http, encoding/json, flag, bufio, os)
- No import of forge core -- the CLI is a pure HTTP client
- Config via FORGE_URL, FORGE_TOKEN, FORGE_MCP_URL env vars or .forge-cli.env fallback
- FORGE_MCP_URL defaults to {FORGE_URL}/mcp/message

### Files

| File | Purpose |
|------|---------|
| client.go | Config, loadConfig, loadEnvFile, request, getItem, mergeFields, printJSON, fatal |
| frontmatter.go | parseFrontmatter, parseFrontmatterFile -- YAML-subset parser |
| content.go | Content subcommands: create, update, publish, unpublish, archive, delete, list, get |
| token.go | Token subcommands via MCP JSON-RPC 2.0: create, list, revoke |
| status.go | status subcommand -- GET /_health |
| main.go | Entry point + top-level subcommand router |
| cli_test.go | Unit tests: frontmatter (9), mergeFields (2), loadEnvFile (3) |
| go.mod | Module forge-cms.dev/forge-cli, Go 1.22, no require block |
| CHANGELOG.md | Submodule changelog |
| README.md | Installation, configuration, all commands |

### Lifecycle operations (GET-then-PUT)

All lifecycle and update commands follow this pattern:

1. GET /{prefix}/{slug} -- retrieve current item as map[string]any
2. Modify the map (set Status, overlay frontmatter fields, etc.)
3. PUT /{prefix}/{slug} -- write back the entire map

PublishedAt is set server-side on the draft-to-published transition --
the client-supplied value is irrelevant. []string arrays survive the
[]interface{}-JSON-[]string round-trip correctly (confirmed by G23).

### Token commands

Token management posts MCP JSON-RPC 2.0 (tools/call) to FORGE_MCP_URL.
Tools: create_token, list_tokens, revoke_token. Admin role required.

**Consequences:**

- New forge-cli/ submodule added to go.work
- No changes to forge core or forge-mcp
- Tagged forge-cli/v0.1.0
- Integration test group G23 (TestFull_G23_CLIRoundTrip) validates the
  GET-then-PUT round-trip contract in integration_full_test.go

---

## Amendment A69 � README restructure: short README + REFERENCE.md

**Status:** Accepted  
**Date:** 2026-04-14  
**Files affected:** `README.md` (rewritten), `REFERENCE.md` (new file)

### Problem

`README.md` had grown to 1 074 lines � a full API reference that was
useful as a reference but counterproductive as an introduction. Developers
opening the repo saw a wall of text before encountering a runnable example.
AI assistants loading the README for context exhausted token budgets before
reaching the code examples.

### Decision

Split the single `README.md` into two files:

- **`README.md`** � =150 lines. Title, badge, comparison table, one
  complete minimal example (runnable `package main`), one feature-showcase
  snippet, a bullet summary of what you get, and a `## Reference` link
  section. Nothing else.
- **`REFERENCE.md`** � verbatim extraction of all detailed sections removed
  from `README.md`: Getting started, Core concepts, Content types, Lifecycle,
  Roles & auth, SEO & structured data, AI indexing, Social sharing, Cookies &
  compliance, Storage, Middleware, Templates & rendering, Error handling,
  Redirects & content mobility, MCP integration, The AI-first design
  philosophy, Minimal complete example, Known issues.

### New README examples

Two new code examples replace the old "Getting started" walkthrough:

**Minimal example** � a complete `package main` that compiles and runs.
The `Post` type includes `Head()` and `Markdown()` so the showcase snippet
can safely use `SitemapConfig` and `AIIndex(LLMsTxtFull)` without a startup
panic (Decision A36 capability checks).

**Feature showcase** � a `NewModule(...)` call with one option per line.
Each line is commented with the endpoint or tag it enables. Developers can
delete lines to reduce scope without reading docs.

### Consequences

- No Go code changed. No exported symbols added, removed, or renamed.
- `go build ./...`, `go vet ./...`, `go test ./...` are green by
  construction.
- `example_test.go` unchanged � no Example functions compile-test README
  prose, only API signatures.
- `example/blog/main.go` package comment: was already at v1.11.0.
- `REFERENCE.md` is verbatim � no content was altered, only relocated.
- No version bump. Stays at v1.11.0 (documentation-only change).

---

## Amendment A70 � README: tagline, named value section, showcase fixes (2026-04-14)

**Status:** Agreed  
**Scope:** Documentation only. No exported symbols changed. No version bump.

### Problem

The restructured README (A69) still had several issues undermining its effectiveness for community engagement (HN, r/golang, pkg.go.dev):

1. **Tagline** was generic � could describe any web framework.
2. **Value proposition** was buried at the bottom as a flat anonymous bullet list, visible only after two full code examples.
3. **Duplicate table row** � "AI indexing (llms.txt + AIDoc)" and "AI-native endpoints (llms.txt, AIDoc)" said the same thing.
4. **`(*Post)(nil)` unexplained** � valid Go but unfamiliar to many developers.
5. **AfterPublish noop** � the signal callback returned `nil` with only a comment; the reader could not tell what it actually does.
6. **No pointer to runnable examples** in `example/`.
7. **"What you get" flat bullets** � anonymous, unordered, no descriptions.

### Changes

- **Tagline replaced:**
  ```
  **Go get Forge. From idea to production in one step.**
  Zero dependencies. Built-in content lifecycle. AI-native by default.
  ```
  First line is the hook; second delivers the three concrete differentiators.

- **New "What Forge gives you" section** inserted after the badge/version line, before the comparison table. All 15 features named and described, grouped into five categories: Content, Auth & security, Discovery, AI-native, Infrastructure.

- **Duplicate table row removed:** "AI-native endpoints (llms.txt, AIDoc)" deleted; "AI indexing (llms.txt + AIDoc)" kept.

- **`(*Post)(nil)` comment added:**
  ```go
  m := forge.NewModule((*Post)(nil), // nil pointer � type parameter inferred, no allocation
  ```

- **AfterPublish real body:**
  ```go
  forge.On(forge.AfterPublish, func(_ forge.Context, p *Post) error {
      log.Printf("published: %s", p.Slug) // fires on publish and scheduled?Published
      return nil
  }),
  ```

- **Examples pointer** added after the showcase code block, before the Reference section:
  ```markdown
  Three runnable examples are in [example/](example/):
  - example/blog � devlog with seeded posts, RSS, AI indexing, and scheduled publishing
  - example/api  � headless JSON API with role-based auth and a redirect manifest
  - example/docs � documentation site with AI indexing, /llms.txt, and AIDoc endpoints
  ```

- **Flat "What you get" bullet list removed** � all 15 features moved to the new named value section.

### Consequences

- README more effective for first-time visitors and community links.
- No call-site syntax changed. No AI generation accuracy affected.
- `example_test.go` unaffected � uses its own `examplePost` type, not the README showcase.
- No version bump. Stays at v1.11.0 (documentation-only change).
- NEXT.md deleted in the same commit.

---

## Amendment A71 � README: framework subtitle + 30-second start (2026-04-15)

**Status:** Agreed  
**Scope:** Documentation only. No exported symbols changed. No version bump.

### Problem

Two remaining first-impression gaps identified after A70:

1. **No plain-language description** � the tagline ("Go get Forge. From idea to
   production in one step.") is a pun, not a description. A first-time visitor
   landing from GitHub search or a link cannot tell what Forge is before scrolling.

2. **No immediate runnable path** � the quickest way to see Forge in action
   (`cd example/blog && go run .`) was buried after the feature list and comparison
   table. A developer who can run the project in 30 seconds is more likely to read on.

### Changes

- **Tagline replaced:**
  ```
  **Go get Forge. From idea to production in one step.**
  Zero dependencies. Built-in content lifecycle. AI-native by default.
  ```
  Replaced with a single plain sentence:
  ```
  A Go framework for content-driven applications. Zero dependencies. AI-native by default.
  ```

- **New `## 30-second start` section** inserted immediately after the badges/version
  line, before `## What Forge gives you`:
  ```bash
  git clone https://forge-cms.dev/forge
  cd example/blog
  go run .
  # open http://localhost:8080
  ```
  No prose � four commands only. The `open` line is a comment for cross-platform safety.

### Consequences

- README opens with a factual description instead of marketing copy.
- Clone-and-run path is the first content after the version badge.
- No call-site syntax changed. No AI generation accuracy affected.
- `example_test.go` unaffected.
- No version bump. Stays at v1.11.0 (documentation-only change).
- NEXT.md deleted in the same commit.

---

## Amendment A72 � VISION.md: strategic repositioning (2026-04-18)

**Status:** Agreed  
**Scope:** Documentation only. No exported symbols changed. No version bump.

### Problem

VISION.md last updated 2026-03-18 and no longer reflected the strategic
positioning decided on 2026-04-17:

1. No articulation of Forge as a typed state layer for AI agents (beyond content).
2. No documentation of the two-layer commercial model (Core AGPL / Cloud commercial).
3. Roadmap still described future plans for Phases 1�2, which shipped in v1.11.0
   and forge-mcp v1.4.0.

### Changes

**Inserted `## What Forge is`** after "The core thesis", before "The vision in one sentence":
- Forge as the typed, persistent state layer AI agents operate on
- Contrast with Temporal/LangChain (orchestration vs state substrate)
- MCP as the protocol for agent-driven state transitions
- Content as canonical use case; any typed stateful data is valid

**Inserted `## The two-layer model`** after the MCP section, before Roadmap:
- Forge Core: open source (AGPL), zero dependencies, self-hostable
- Forge Cloud: commercial, process-per-tenant, SQLite per customer, forge-admin closed source
- forge-media: LocalMediaStore with swappable interface for S3 in Phase 4

**Replaced `## Roadmap`** in full:
- Phase 1 ? DONE: forge-mcp v1.4.0
- Phase 2 ? DONE: forge v1.11.0 (full production foundation)
- Phase 3: Forge Cloud private beta (current focus)
- Phase 4: Forge Cloud GA (multi-site, bureau workflow, commercial licenses)

### Consequences

- VISION.md now accurately reflects shipped state and strategic direction.
- No call-site syntax changed. No AI generation accuracy affected.
- `example_test.go` unaffected.
- No version bump. Stays at v1.11.0 (documentation-only change).
- NEXT.md deleted in the same commit.

---

## Amendment A76 � Go 1.26.2 + vanity module rename to `forge-cms.dev`

**Status:** Agreed � 2026-04-30
**Shipped in:** v1.14.0

### Problem

Two community issues requested via GitHub:

1. **Issue #1** � The `go` directive in all modules was `go 1.22`. Go 1.26.2 is
   the current supported release. Staying on 1.22 prevents use of language and
   stdlib improvements available in later releases.

2. **Issue #2** � All module paths use `github.com/forge-cms/...`. The project
   now has a dedicated domain (`forge-cms.dev`) and a vanity URL should be the
   canonical import path. This improves brand consistency and decouples the
   module path from the GitHub repository URL.

### Decision

**Go version:** Bump the `go` directive in all modules and `go.work` from their
current values (`1.22`, `1.24`, `1.25`) to `go 1.26.2`.

**Module rename:**

| Old path | New path |
|----------|----------|
| `github.com/forge-cms/forge` | `forge-cms.dev/forge` |
| `github.com/forge-cms/forge-mcp` | `forge-cms.dev/forge-mcp` |
| `github.com/forge-cms/forge-media` | `forge-cms.dev/forge-media` |
| `github.com/forge-cms/forge-cli` | `forge-cms.dev/forge-cli` |
| `github.com/forge-cms/forge-pgx` | `forge-cms.dev/forge-pgx` |

`forge-pgx` is included even though not listed in the original issue � it shares
the workspace and its `replace` directive would break immediately if the root
module path changed without updating `forge-pgx/go.mod`.

Historical references in `decisions/core.md` and `decisions/phase2.md` are left
as-is; they are permanent records of past decisions, not forward-facing API docs.

### forgeVersions() logic change

The old `forgeVersions()` used `strings.HasPrefix(path, "github.com/forge-cms/forge")`
because sub-modules shared the root path as a prefix. After the rename the modules
are independent paths (`forge-cms.dev/forge-mcp` is not a sub-path of
`forge-cms.dev/forge`). The matching logic is updated to:

```go
const base = "forge-cms.dev/"
// match any module under forge-cms.dev
if !strings.HasPrefix(path, base) { return }
key := strings.ReplaceAll(strings.TrimPrefix(path, base), "-", "_")
result[key] = v
```

Output keys are identical to before (`"forge"`, `"forge_mcp"`, etc.).

### Consequences

1. **Breaking import change** for all external users � they must update their
   `go.mod` and import paths. A minor version bump (`v1.14.0`) signals this.
2. All sub-modules ship coordinated version bumps:
   - forge-mcp: `v1.6.0`
   - forge-media: `v1.1.0`
   - forge-cli: `v0.3.0`
   - forge-pgx: not tagged (no behaviour change, workspace-local only)
3. `go get` resolution requires Caddy vanity URL config on `forge-cms.dev` to be
   deployed before external users can use the new paths. This is a deploy-day
   task noted in `NEXT.md` and handled separately.
4. `pkg.go.dev` badge in `README.md` updated to `forge-cms.dev/forge`.
5. `ARCHITECTURE.md`, `AGENTS.md`, `README.md`, `REFERENCE.md`, `CHANGELOG.md`
   all updated with the new import paths.
6. `decisions/` historical files left unchanged.
7. `forgeVersions()` godoc updated.
8. `CHANGELOG.md` version policy section updated.
9. Both `NEXT.md` (untracked) deleted after commit.

---

## Amendment A84 — REFERENCE.md: accuracy fixes and gap-fill for v1.16.0

**Date:** 2026-05-05
**Status:** Agreed
**Level:** 1 (docs-only — no exported symbol changed, no code change)
**Files:** `REFERENCE.md`, `DECISIONS.md`, `decisions/phase2.md`

### Problem

A full audit of `REFERENCE.md` against v1.16.0 found 5 accuracy errors and
6 missing sections covering features shipped in Amendments A66–A83.

### Accuracy corrections

1. **Version examples** — health endpoint example showed `"version":"1.13.1"`
   and `"forge_mcp":"1.5.0"`; corrected to `"1.16.0"` and `"1.6.1"`.

2. **forge-mcp README link** — pointed to `forge-mcp/README.md` (removed subdir);
   corrected to `https://github.com/forge-cms/forge-mcp`.

3. **forge-media README link** — pointed to dead `https://forge-cms.dev/forge/tree/main/forge-media`;
   corrected to `https://github.com/forge-cms/forge-media`.

4. **Rate Limiting section** — falsely stated "Forge does not include a built-in
   rate-limiting middleware". `forge.RateLimit` has existed since a prior amendment.
   Section rewritten to document `forge.RateLimit(100, time.Minute)` and
   `forge.TrustedProxy`.

5. **`app.Content` fallback path in examples** — both "Getting Started" and
   "Minimal Complete Example" used `app.Content(&Post{}, forge.At("/posts"), ...)`
   which silently skips AI/sitemap/feed wiring when a raw struct pointer is passed
   instead of a `Registrator`. Corrected to `forge.NewModule((*Post)(nil), ...)`.

### Missing sections added

6. **Token management** — new section (between "Roles & auth" and "SEO") covering
   `NewTokenStore`, `Config.TokenStore`, `forge_tokens` DDL, bootstrap flow with
   `slog.Warn`, `TokenStore.Create/List/Revoke`, `ErrLastAdmin`, MCP tools table,
   and a critical footgun warning about using `SignToken` alongside `TokenStore`.

7. **Navigation** — new section (between "Cookies & compliance" and "Storage")
   covering `NavModeDB`/`NavModeCode`, `Config.NavMode`, `App.Nav()`, `NavItem`
   fields table, template `.Nav` usage, and the MCP nav tools note.

8. **AppSchema** — new subsection in "SEO & structured data" after "Rich result
   types" documenting `app.SEO(&forge.AppSchema{Type, Name, URL, Logo})`.

9. **OGDefaults** — new subsection at end of "Social sharing" documenting
   `app.SEO(&forge.OGDefaults{Image, TwitterSite, TwitterCreator})`.

10. **AbsURL** — new note in "Head" section (after Breadcrumbs, before HeadFunc)
    documenting `forge.AbsURL(base, path)` helper.

11. **SeqRepository** — new subsection in "Storage" (between MemoryRepo and
    Production SQL repository) covering type-assert pattern and `iter.Seq2` loop.

12. **forge-cli** — new section (between forge-media and Static files) covering
    install, `init` subcommand, all commands table, and frontmatter file format.

### Additional fix

13. **`ErrLastAdmin`** added to the sentinel errors list in "Error handling".

### Consequences

- No exported Go symbols added, removed, or renamed.
- No build, vet, or test changes required.
- `REFERENCE.md` now accurately reflects v1.16.0 across all shipped amendments.

---

## Amendment A85 — copilot-instructions.md: docs/content workflow; FEATURELIST.md

**Date:** 2026-05-05
**Status:** Agreed
**Level:** 1 (docs-only — no exported symbol changed, no code change)
**Files:** `.github/copilot-instructions.md`, `FEATURELIST.md` (new), `DECISIONS.md`, `decisions/phase2.md`

### Problem

Two gaps in the repo's operational documentation:

1. **No docs workflow** — Copilot instructions had a detailed standard step workflow
   and release tagging workflow, but no parallel workflow for docs-only tasks
   (updating REFERENCE.md, README.md, FEATURELIST.md) or for creating content
   for forge-cms.dev/docs. Without a formal workflow, docs tasks were handled
   ad hoc with no consistent structure for review, approval, or content drafts.

2. **No feature list** — No single file enumerated what Forge generates and
   includes automatically. New users, AI agents, and the architect had no
   authoritative reference for the complete feature surface.

### Changes

**`.github/copilot-instructions.md`** — new `## Docs and content workflow` section
inserted between `## Standard step workflow` and `## Release tagging`. The section
defines:
- A doc freshness check on every session start (REFERENCE.md, README.md, FEATURELIST.md)
- A 9-step docs and content task workflow: propose scope → repo doc review → apply
  changes → content suggestions → outline → full drafts → save drafts → propose
  commit → update context
- Push permission rules (explicit instruction required, separate from commit approval)
- Draft file naming and save location (`Forge-site-working/content/YYYYMMDD-HHMMSS-<slug>.md`)

**`FEATURELIST.md`** — new file in repo root. Complete feature list for v1.16.0 (A84),
grouped into 9 categories: Routes and feeds, Storage, Rendering, Lifecycle, Access
control, Navigation, MCP tools, Template infrastructure, SEO, Operations, forge-media,
forge-cli, Developer and AI-agent experience.

### Consequences

- No exported Go symbols added, removed, or renamed.
- No build, vet, or test changes required.
- Future doc sessions have a consistent, auditable workflow.
- FEATURELIST.md must be updated whenever an amendment adds or changes a feature.

---

## Amendment A86 — copilot-instructions.md: CLI and MCP tool parity rule

**Date:** 2026-05-05
**Status:** Agreed
**Level:** 1 (docs-only — no exported symbol changed, no code change)
**Files:** `.github/copilot-instructions.md`, `DECISIONS.md`, `decisions/phase2.md`

### Problem

Forge ships MCP tools for every admin operation, but there was no formal rule
requiring that forge-cli expose equivalent commands in the same release. This
allowed CLI gaps to accumulate silently — the nav tools gap (four MCP nav tools
with no CLI counterpart in forge-cli v0.3.0) was the first concrete instance.
Without a written rule, future milestones could repeat the pattern.

### Change

**`.github/copilot-instructions.md`** — new `## CLI and MCP tool parity` section
inserted between `## Docs and content workflow` and `## Release tagging`.

The section states:
- Every admin operation available via MCP tools must also be available via forge-cli
- CLI is the human fallback when agents are unavailable
- The rule applies per milestone: CLI commands ship in the same release as MCP tools
- Current known gap documented: forge-cli v0.3.0 has no nav commands despite
  `list_nav_items`, `create_nav_item`, `update_nav_item`, `delete_nav_item` existing
- Gap is tracked and will be closed in the nav CLI milestone

### Consequences

- No exported Go symbols added, removed, or renamed.
- No build, vet, or test changes required.
- Future milestone planning must include CLI commands whenever MCP tools are added.
- Nav CLI gap is formally acknowledged and tracked.

---

## Decision D32 — decisions/ file system restructure

**Date:** 2026-05-17
**Status:** Active

**Context:**
decisions/phase2.md was the catch-all for all decisions from D25 onwards.
The name was misleading and the structure did not scale.

**Decision:**
Restructure to a flat, role-separated system with a rolling working file.

- Flat structure — no subdirectories. Topic files are always leaves.
- Rolling window is size-based (~20KB), not count-based.
- Non-Decisions go directly to nondecisions.md, not through recent.md.
- Structure description lives in DECISIONS.md header — single authoritative source.
- Archiving is architect-directed, not autonomous.
- phase2.md renamed to phase2-archive.md (not deleted).

**Files:**
- decisions/recent.md — new decisions, rolling ~20KB window
- decisions/nondecisions.md — all Non-Decisions
- decisions/phase2-archive.md — archived (was phase2.md)
- decisions/core.md — unchanged
- decisions/[topic].md — created on architect instruction

---
