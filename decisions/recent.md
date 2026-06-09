# Smeldr — Recent Decisions

Rolling working file. All new decisions are added here first.
When this file approaches ~20KB, report it at session start — the architect
will issue archiving instructions via NEXT.md.

Non-Decisions go directly to `nondecisions.md` — not here.

Archived 2026-05-25: D32 → docs.md · A87, A97–A101 → core.md
Archived 2026-05-30: A102–A115 → phase3-archive.md
Archived 2026-06-04: A116–A120 → phase3-archive.md
Archived 2026-06-05: A121–A125 → phase4-archive.md
Archived 2026-06-07: A126–A130 → phase5-archive.md

---

## A131 - T100 Step 3: media package rename (smeldr.dev/media v1.4.0)

**Date:** 2026-06-06
**Status:** Implemented

Renamed the Go package declaration in `smeldr.dev/media` from `forgemedia` to
`media` across all 8 production + test files. T100 Step 3 (independent - no module
imports media at the go.mod level; the site passes it to mcp via `WithModule`).

Scope:
- package declaration in 7 internal files; external test package
  `forgemedia_test` → `media_test` (example_test.go)
- error-/panic-string prefixes `forgemedia:` / `forgemedia.New:` → `media:` /
  `media.New:` (16 occurrences across media.go + server.go)
- godoc import example: dropped `forgemedia` alias, `forgemedia.X` → `media.X`
- package-doc framework word "Forge" → "Smeldr" (Q1 precedent)
- stale cross-module refs `forge-mcp` / `forgemcp.X` → `mcp` (mcp was renamed in
  Step 2 / A130 / mcp v1.17.0)
- canary path-traversal test fixture `canary-forge-media-test.txt` →
  `canary-media-test.txt` (arbitrary filename, not a semantic-forge fixture -
  renamed so the T100 grep gate is literally zero)
- CHANGELOG header `forge-media` → `smeldr.dev/media` + v1.4.0 section

Version: media was at v1.3.0 (T95 StatsProvider NOT shipped), so this rename takes
**v1.4.0**; T95 later becomes v1.5.0.

Preserved (out of T100 scope): `forge_media` SQLite table name (10 refs - DB-migration
scope, underscore form does not match the hyphenated grep gate, would break the
production smeldr.dev DB); standalone "Forge"/"forge" brand words in comments
(`media.go` "Forge HTTP handler", `server.go` "for a Forge" - T101 brand-prose pass).

No exported-symbol change, no behaviour change. Breaking-MINOR. media is independent
- adds and consumes no gate. Final grep gate (`forgemedia|forge-media|forgemcp|
forge-mcp` in `*.go`) = literally zero.

---

## A132 - T100 Step 4: social package rename (smeldr.dev/social v0.8.0)

**Date:** 2026-06-07
**Status:** Implemented

Renamed the Go package declaration in `smeldr.dev/social` from `forgesocial` to
`social` across all 25 production + test files. T100 Step 4 (independent of media;
social imports mcp via go.mod but mcp v1.17.0 already shipped in A130).

Scope of the rename (this module's own forge-named residue):
- package declaration in 21 internal files; 4 external test packages
  `forgesocial_test` → `social_test` (social_test.go, router_test.go,
  route_test.go, route_worker_test.go)
- ~120 error/panic/log-string prefixes `forgesocial:` → `social:` across
  social.go, twitter.go, mastodon.go, linkedin.go, route.go, router.go,
  credential.go, oauth.go, schedule.go, schema.go, scheduler.go,
  platform_config.go, route_worker.go
- import alias dropped; `forgesocial.X` → `social.X` across all test files
- package-doc "forge" → "smeldr" in social.go
- stale cross-module refs `forge-mcp` / `forgemcp.X` → `mcp` (mcp was renamed
  in Step 2 / A130 / mcp v1.17.0)
- `social_test.go`: local var `social` → `svc` (package-name collision fix —
  `social.ScheduledPost` type-ref failed vet when local var shadowed package name)
- `router_test.go`: local var `social` → `svc` in two test functions (consistency —
  no type-ref failure there, but matches social_test.go decision and mcp `srv` precedent)
- README: `smeldr.dev` install/import paths, `social.X` selectors, v0.8.0 badge,
  "Migrating from v0.7.x" section
- CHANGELOG: header `forge-social` → `smeldr.dev/social` + [0.8.0] section prepended;
  historical entries preserved verbatim (forgemcp/forgesocial refs are historical narrative)

Version: social was at v0.7.x; this rename takes **v0.8.0** (breaking-MINOR).

Preserved (out of T100 scope): `forge_social_*` DB table names (65 refs, 8 tables:
`forge_social_posts`, `forge_social_credentials`, `forge_social_oauth_states`,
`forge_social_routes`, `forge_social_route_jobs`, `forge_social_route_log`,
`forge_social_publication_schedules`, `forge_social_platform_config` — DB-migration
scope tracked as T102); `X-Forge-Signature` header name (T86/T87 cross-agent
signature contract — any rename requires coordinated update of all agent verifiers);
standalone "Forge"/"forge" brand words in comments and prose (T101 brand-prose pass).

No exported-symbol change, no behaviour change. Breaking-MINOR. social imports
mcp (already v1.17.0) but adds no further gate for remaining Steps 5+.
Final grep gate (`forgesocial|forge-social|forgemcp|forge-mcp` in `*.go`) = ZERO.

---

## A133 — T100 Step 5: cli binary rename + `logs` command (smeldr.dev/cli v0.14.0)

**Date:** 2026-06-07
**Status:** Implemented

### Binary rename: `forge-cli` → `smeldr-cli`

All user-facing `"forge-cli"` strings renamed in 21 source files. All `.go` files
moved from the module root to `cmd/smeldr-cli/` so that `go install` produces a
binary named `smeldr-cli` instead of `cli`.

New install path: `go install smeldr.dev/cli/cmd/smeldr-cli@latest`

**Preserved (T86/T87 — gate = 6 hits, all intentional):**
- `loadEnvFile(".forge-cli.env")` in `client.go` (legacy env file fallback)
- Comment `.smeldr-cli.env first, then .forge-cli.env (legacy).` in `client.go`
- Package-doc comment mentioning `(legacy: .forge-cli.env)` in `main.go`
- `(legacy: .forge-cli.env is still read if present)` in `printUsage`
- `"forge-cli-env-*"` temp-file names in `cli_test.go` (2×, arbitrary test label)

### New: `smeldr-cli logs` (T79 CLI half)

`GET /_logs` called directly over HTTP (not MCP) so it works when MCP is the
failing component. Requires Admin role. Server must call `app.CaptureLogs()`
(core v1.36.0+, A128).

Response decoding uses:

```go
type logsEnvelope struct {
    Capacity int        `json:"capacity"`
    Count    int        `json:"count"`
    Dropped  uint64     `json:"dropped"`
    Entries  []logEntry `json:"entries"`
}
type logEntry struct {
    Time  time.Time      `json:"time"`
    Level string         `json:"level"`
    Msg   string         `json:"msg"`
    Attrs map[string]any `json:"attrs"`
    Seq   uint64         `json:"seq"`
}
```

Flags: `--level LEVEL` (forwarded as query param), `--limit N`, `--since RFC3339`
(validated before sending), `--json` (raw envelope).

Default output: tabwriter table with columns TIMESTAMP / LEVEL (uppercased) /
SEQ / MESSAGE, entries newest-first. Footer `(N entries dropped — ring buffer
overflowed)` when `Dropped > 0`. Error messages: 401 → "Admin token required",
403 → "forbidden — Admin role required", 404 → "/_logs not available — call
app.CaptureLogs() on the server (core v1.36.0+)".

Five tests in `logs_test.go`: table output (uppercased levels), `--json`,
empty entries, dropped footer, query-param forwarding.

### Docs

`README.md` fully rewritten: title, install path, migration-from-v0.13.x section
(breaking rename note + preserved fallbacks), `logs` command section, `SMELDR_*`
config, social reference → `smeldr.dev/social`.

`CHANGELOG.md` header → `smeldr-cli`; `[0.14.0]` section prepended with Breaking
+ Added entries.

### Closes

- **T79 CLI half** — `smeldr-cli logs` implements the CLI side of the `/_logs`
  operator workflow (server side: A128, core v1.36.0).
- **N57** — `forge-cli` binary name reversal; binary is now `smeldr-cli`.

---

## A134 — T100 Step 7: core + common docs selector sweep

**Date:** 2026-06-07
**Status:** Agreed
**Level:** 1 (docs-only, no version bump)

### Decision

Sweep stale `forge*.` selectors and `forge-cli` binary name from the common skill
and core docs. All module renames (Steps 1–5) and site-dev integration (Step 6)
were done and tagged before this step ran. Closes T100.

### Files changed

- **`common/agent/skills/smeldr.md`** — version line bumped (mcp v1.17.0, oauth v0.2.0,
  media v1.4.0, cli v0.14.0, social v0.8.0); `forgeoauth.`→`oauth.`,
  `forgemcp.`→`mcp.`, `forgesocial.`→`social.`, `forgeagent`→`agentflow` alias
  (consistent with site-dev S176); `forge-cli`→`smeldr-cli` throughout CLI section;
  `socialSrv` collision fix (local var `social` would shadow package `social`);
  config note updated to `SMELDR_*` primary with `FORGE_*` fallback note.
- **`core/docs/REFERENCE.md`** — same selector + `smeldr-cli` sweep; install path
  updated to `go install smeldr.dev/cli/cmd/smeldr-cli@latest`.
- **`core/docs/FEATURELIST.md`** — `mcp.`/`oauth.`/`social.` selectors; `smeldr-cli`.
- **`core/AGENTS.md`** — `mcp.`/`media.` selectors in code blocks.
- **`core/example/blog/main.go` + `main_test.go`** — `forgemcp` alias → `mcp`;
  `go build ./...` green.
- **`core/skills/smeldr.md`** — synced from `common/agent/skills/smeldr.md`.
- **`common` commit:** `f914d82` · **`core` squash commit:** `79e3bbb`

### Preserved

- `.forge-cli.env` legacy note in REFERENCE.md (T86/T87)
- `FORGE_*` env var fallback mentions in skill (T86/T87)
- `## forge-oauth` / `## forge-social` / `## forge-agent` section headers in skill (T101)
- `mcp.WithForgeFallback()` — exported function name unchanged

### Gate

`forge(mcp|oauth|media|social|agent).|forge-cli` across all 7 files: **ZERO hits**.

---

## A135 — T101: standalone-module brand-prose sweep

**Date:** 2026-06-07
**Status:** Agreed
**Level:** 1 (prose/identifier cleanup, no exported-symbol or behaviour change)

### Decision

Sweep remaining "Forge"/"forge" brand words in standalone module repos at the prose
level: godoc/comments, READMEs, user-visible help strings, and unexported internal
identifiers that were intentionally left out of T100 (which renamed only the
compiler-visible package declarations). T101 completes the brand rename so that a
developer reading the mcp source does not encounter "forge App" or `forgeCtx`.
oauth unchanged — all remaining hits are test fixtures.

### Scope per repo

**mcp v1.17.0 → v1.17.1** (`feat/t101-mcp-prose`, squash `51d5dba`):
- `transport.go`: `forgeCtx` local variable → `smeldrCtx` (10 occurrences, replace_all);
  `forgeFallback` struct field → `fallback` (2 occurrences)
- `mcp.go`: struct field declaration `forgeFallback` → `fallback`; 4 comments
  "forge App"/"forge core"/"Forge type identifier" → "Smeldr"
- `transport.go`: 4 comments "forge [smeldr.App]"/"forge routes"/"Forge bearer
  token mode"/"Forge role" → "Smeldr"
- `mcp_test.go`: 1 comment "Forge type identifier" → "Smeldr"
- `tool.go`: 1 comment "forge error" → "smeldr error"; user-visible MCP tool
  description "Forge module table name" → "Smeldr module table name"

**media v1.4.0 → v1.4.1** (`feat/t101-media-prose`, squash `7af6bee`):
- `media.go:92`: "Forge HTTP handler" → "Smeldr HTTP handler"
- `server.go:17`: "for a Forge" → "for a Smeldr"

**social v0.8.0 → v0.8.1** (`feat/t101-social-prose`, squash `50b0711`):
- `README.md`: 4 lines "Forge application"/"Forge lifecycle signals"/"auto-generated
  by Forge"/"Forge signals" → "Smeldr"; `verifyForgeSignature` example function →
  `verifySignature`
- `route.go`: 2 comments "Forge fires"/"Forge lifecycle signal" → "Smeldr"
- `router.go`: 2 comments "forge App's signal bus goroutine"/"Forge App's signal
  bus" → "Smeldr App's"
- `social.go`: 1 comment "Forge application" → "Smeldr application"

**cli v0.14.0 → v0.14.1** (`feat/t101-cli-prose`, squash `6d631a5`):
- `init.go:20`: flag description "Base URL of the Forge instance" → "Smeldr instance"
- `media.go:264`: help text "Forge media library" → "Smeldr media library"
- `status.go:12`: comment "configured Forge instance" → "Smeldr instance"
- `cli_test.go`: test env-var identifiers `TEST_FORGE_CLI_X/Y/Z` →
  `TEST_SMELDR_CLI_X/Y/Z`; `__nonexistent_forge_cli_env__` →
  `__nonexistent_smeldr_cli_env__`

### Preserved

- `WithForgeFallback` exported function name (T86/T87 surface — no rename)
- `forge://` URI parsing in resource.go + test strings (T86/T87)
- `forge bearer token` in T86/T87 fallback description (mcp.go:43)
- `TestForgeFallback_*` test names (document the `WithForgeFallback` API)
- `X-Forge-Signature` header name (social, T86/T87)
- `ForgeURL` exported struct field (cli client.go — exported identifier, outside scope)
- `FORGE_*` env var fallback documentation (cli client.go, T86/T87)
- `.forge-cli.env` legacy file references (cli, T86/T87)
- README migration note "forge-cli → smeldr-cli in v0.14.0" (historical)
- `"forge-cli-env-*"` temp-file prefix in cli_test.go (arbitrary test label)
- `"go", "forge", "cms"` fixture values in cli_test.go (test content, not identifiers)
- oauth repo entirely (all remaining hits are test fixtures)

### Closes T101.

---

## A136 — `list_storys` → `list_stories`: consonant-y pluralization in MCP list tool names

**Date:** 2026-06-08
**Status:** Agreed
**Level:** 2 (patch; no exported-symbol change, no behaviour change for existing tool names)

### Decision

MCP list tool names for content types whose snake_case name ends in consonant+y
(e.g. `Story` → `story`) were generated as `list_storys`, which is grammatically
wrong. Fix: new `pluralSnake()` helper applies the standard English consonant-y →
ies rule when forming the list tool name.

### Changes

**`mcp/mcp.go`** — line 343:
`"list_" + typeSnake + "s"` → `"list_" + pluralSnake(typeSnake)`

New helpers added after `snakeCase`:

```go
func pluralSnake(s string) string {
    if len(s) >= 2 && s[len(s)-1] == 'y' && !isVowel(s[len(s)-2]) {
        return s[:len(s)-1] + "ies"
    }
    return s + "s"
}
func isVowel(b byte) bool {
    return b == 'a' || b == 'e' || b == 'i' || b == 'o' || b == 'u'
}
```

**`mcp/tool.go`** — `moduleForAdminList()` reverse lookup updated to resolve
the "ies" suffix back to the base type (stories→story) in addition to the
existing plain-s suffix stripping (posts→post).

**`mcp/mcp_test.go`** — three new tests:
- `TestPluralSnake`: story→stories, category→categories, post→posts, key→keys,
  essay→essays, day→days
- `TestMCPConsonantYPlural_toolName`: registers `testStory` module, asserts
  `defs[0].Name == "list_test_stories"`
- `TestMCPConsonantYPlural_dispatch`: asserts `list_test_stories` dispatches
  correctly (returns `items` field)

`go test ./...` → ok `smeldr.dev/mcp` 0.195s.

**`mcp/CHANGELOG.md`** — [1.17.2] section prepended.

**`common/agent/skills/smeldr.md`** and **`core/skills/smeldr.md`** — version
line updated: `mcp v1.17.1 → v1.17.2`.

### Version

mcp v1.17.2 (patch; `list_stories` now generated where previously `list_storys`
was generated — operators using consonant-y types must update their MCP client
tool references).

---

## A137 — Scheduler save-error resilience: continue publishing remaining items

**Date:** 2026-06-08
**Status:** Agreed
**Level:** 2 (bug fix; behaviour change in error path of `processScheduled`)

### Decision

When `processScheduled` called `repo.Save` on a scheduled item and the save
failed, the function returned immediately with the error. This stopped all
remaining scheduled items in the same tick from being published. The fix
replaces the `return` with `slog.Warn + continue` so the failing item is
skipped and the loop proceeds to the next item.

Also fixed: `scheduler.tick()` was discarding the error return from
`processScheduled` (comment said "logged by caller" — untrue). The error is
now captured and logged.

### Files changed

- **`module.go`**: `processScheduled` — `return published, next, err` on
  `repo.Save` failure replaced with:
  ```go
  slog.Warn("smeldr: scheduler failed to publish item; skipping",
      "id", nodeIDOf(item), "err", err)
  continue
  ```
- **`scheduler.go`**: `tick()` — `_, n, _ := m.processScheduled(...)` →
  captures `err` and calls `slog.Warn("smeldr: scheduler tick error", "err", err)`
  when non-nil. Import `"log/slog"` added.
- **`scheduler_test.go`**: `TestProcessScheduled_continuesAfterSaveError` — seeds
  3 scheduled items, wraps repo with `failOnSaveRepo` that injects an error for
  item #2, asserts `published == 2` and no error returned.
  New helper: `failOnSaveRepo[T any]` wraps `Repository[T]` and returns
  `errors.New("injected save failure")` when `Save` is called for a specific ID.

### Version

core v1.36.1 (patch; no exported-symbol or interface change).

Branch: `claude/scheduled-posts-conflict-m6s3kn` merged to main via `--no-ff`.

---
