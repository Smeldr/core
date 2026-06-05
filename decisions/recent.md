# Smeldr — Recent Decisions

Rolling working file. All new decisions are added here first.
When this file approaches ~20KB, report it at session start — the architect
will issue archiving instructions via NEXT.md.

Non-Decisions go directly to `nondecisions.md` — not here.

Archived 2026-05-25: D32 → docs.md · A87, A97–A101 → core.md
Archived 2026-05-30: A102–A115 → phase3-archive.md
Archived 2026-06-04: A116–A120 → phase3-archive.md

---

## A121 — T85: core-repo brand sweep ("Forge" → "Smeldr" in doc prose + headers)

**Date:** 2026-06-01  
**Status:** Agreed  
**Branch:** `docs/T85-brand-sweep`

### What

Pure doc-only brand sweep across 17 files in the core repo. Every instance of
"Forge" as the framework brand name in living doc prose and headers is renamed to
"Smeldr". No code changes, no wire-level identifiers, no version bump.

### Scope

Files touched: `README.md`, `.github/copilot-instructions.md`, `CHANGELOG.md`
(header + `decisions/recent.md` header), `DECISIONS.md` (header + intro),
`docs/ARCHITECTURE.md`, `docs/REFERENCE.md`, `docs/FEATURELIST.md`,
`docs/VISION.md`, `docs/SECURITY.md`, `skills/smeldr.md`, `BENCHMARKS.md`,
`CLA.md`, `Milestone_BACKLOG_TEMPLATE.md`, `NOTES.md`, `ERROR_HANDLING.md`,
`example/README.md`, `example/api/README.md`.

Additional: `docs/VISION.md` — `forge-admin` → `smeldr-admin` and
`Forge Cloud` → `Smeldr Cloud` throughout.

`skills/smeldr.md` — version line resynced to current versions alongside the
header brand fix (file was stale at v1.25.1 from before the housekeeping sprint).

Two stale filesystem paths in `copilot-instructions.md` corrected:
`common/agent/skills/forge.md` → `smeldr.md` (file was renamed in the
housekeeping sprint but the instructions were not updated).

### Preserve (not touched)

`X-Forge-*` webhook headers, `forge://` MCP resource URI scheme, `FORGE_*`
env vars, `forge-cli` binary name, historical CHANGELOG entry bodies,
`decisions/*.md` archive files, DECISIONS.md dated index rows, `migrate.go`
`forge_*` rename sources, `BenchmarkForgeMarkdown` Go identifier,
`FORGE_SECRET` env var references, test-local identifiers.

### Why

Every prior renaming task (T59/T62/T64/T65/T66/A106) renamed the code, module
paths, config files, and error prefixes — but never the framework *brand name*
in doc prose. This sweep makes the docs consistent with the published brand.
The direct trigger was the "Forge v1.31.0" tag name during the T32 release dance.

---

## A122 — T88+T89: fix stale `forge:` struct tag examples + core/skills sync

**Date:** 2026-06-02  
**Status:** Agreed  
**Level:** 1 (docs-only, no version bump)

### What

Two classes of correctness bugs closed in one commit:

**T88 — stale `forge:"required"` in live code examples.**
`A111` (v1.30.0) renamed the struct tag key from `forge:"required"` to
`smeldr:"required"`. Any developer or AI assistant copying the README minimal
example would produce a non-functional content type (validation and auto-slug
silently do nothing with the old key). One file, two lines:
- `README.md` lines 101–102: `forge:"required"` → `smeldr:"required"`

**T89 — `core/skills/` public mirror synced from `common/agent/skills/`.**
`core/skills/` is the deliberate public distribution copy of the canonical
pilot skills in `common/agent/skills/` (private repo). It had drifted to
`forge v1.25.1` (missing SiteConfig, RawHead, block MCP catalog, oauth
section, and stale struct tags). Root cause: the doc-gate reminder was
passive ("copy updated...") — easy to forget. This amendment:

1. Fixed the canonical (`common/agent/skills/smeldr.md`): stale footer path
   `forge-common/agent/skills/forge.md` → `Smeldr/common/agent/skills/smeldr.md`.
2. Fixed `common/agent/skills/smeldr-design.md`: stale Destination footer.
3. Synced `core/skills/smeldr.md` and `core/skills/smeldr-design.md` from
   common via `Copy-Item *.md -Force`. Sync brings correct struct tags,
   current sections (SiteConfig, RawHead, block MCP catalog, oauth), and correct
   footer paths.
4. Replaced the passive doc-gate reminder with an **unconditional Copy-Item
   command** in copilot-instructions M-number pre-commit gate.
   `smeldr-design-assistant.md` and `smeldr-operator.md` are core-only
   (Claude.ai project instructions; no common canonical) and are not
   overwritten by the Copy-Item *.md command.

### Preserve

Historical `forge:"required"` references in `CHANGELOG.md:47` (migration
note), `DECISIONS.md:198` (archive row), `docs/REFERENCE.md:186–187`
(breaking-change migration guidance), and `decisions/*.md` archives.

---

## A123 — T86: wire-level dual-compat sweep (forge → smeldr, non-breaking)

**Date:** 2026-06-03  
**Status:** Agreed  
**Versions:** core v1.32.0, mcp v1.15.0, cli v0.11.0

### Design rule

New identifier is generated and preferred. Legacy identifier is still accepted
(on parse) or still emitted (on output) alongside the new one. Nothing breaks.
T87 (removal of legacy side) is deferred — after a deprecation window.

### Three surfaces

**1. mcp — resource URI scheme (`forge://` → `smeldr://`)**

`resources/list`, `resources/templates/list`, and subscription notifications
now emit `smeldr://` URIs. `resources/read` and `resources/subscribe` accept
both `smeldr://` (new, preferred) and `forge://` (legacy). If a caller sends
a `forge://` URI, the response echoes it back unchanged — the round-trip is
preserved. `serverInfo.name` in the `initialize` response updated to
`"smeldr-mcp"` (informational metadata, no client keys on the exact string).

**2. core — dual-emit `X-Smeldr-*` + `X-Forge-*` webhook headers**

`httpDeliver` now sets both `X-Smeldr-Signature`, `X-Smeldr-Timestamp`,
`X-Smeldr-Event`, `X-Smeldr-Delivery` (preferred) and the legacy
`X-Forge-*` equivalents on every delivery. Values are identical. Existing
receivers verifying `X-Forge-Signature` continue to work unchanged.

**3. cli — `SMELDR_*` env vars preferred, `FORGE_*` fallback (closes T78)**

`loadConfig` now reads `SMELDR_URL`, `SMELDR_TOKEN`, `SMELDR_MCP_URL` first,
falling back to the `FORGE_*` equivalents when unset. Both `.smeldr-cli.env`
and `.forge-cli.env` are loaded (`.smeldr-cli.env` first). `forge-cli init`
writes `.smeldr-cli.env` with `SMELDR_*` variable names. The `forge-cli`
binary name is deliberately unchanged.

### Deferred (T87)

- Remove `forge://` accept path from `parseResourceURI`
- Remove `X-Forge-*` header emission from `httpDeliver`
- Remove `.forge-cli.env` read from `loadConfig`

T87 is a breaking change requiring a deprecation notice. It is not scheduled.

---

## A124 — T53: NewRateLimiter / NewInMemoryCache stoppable ticker constructors

**Date:** 2026-06-03  
**Status:** Agreed  
**Version:** core v1.33.0 (minor — new exported symbols)

### Problem

`RateLimit` and `InMemoryCache` each start a background sweep goroutine that runs
`for range ticker.C` indefinitely. Neither can be stopped. In tests this leaks one
goroutine per middleware construction, masking real leaks and tripping goroutine-leak
detectors.

### Solution

Add two new exported constructors that return the middleware alongside a stop function:

- `NewRateLimiter(n int, d time.Duration, opts ...Option) (Middleware, func())`
- `NewInMemoryCache(ttl time.Duration, opts ...Option) (func(http.Handler) http.Handler, func())`

The goroutines now use `select` on a `stop` channel alongside `ticker.C`. The stop
function closes `stop` and blocks on a `done` channel until the goroutine confirms exit.
`sync.OnceFunc` makes the stop function idempotent (safe to call multiple times).

The existing `RateLimit` and `InMemoryCache` delegate to the new constructors and discard
the stop function — no API breakage, no change to call sites.

Tests use `t.Cleanup(stop)` or call `stop()` directly to confirm goroutine exit. The
stop function is deterministic: it returns only after the goroutine has exited.

---

## A125 — T30: `CreateRedirectsTable`, `App.Redirects(db)`, `App.RedirectDB()`, `RedirectStore.Delete` + MCP redirect tools + CLI redirect commands

**Date:** 2026-06-04  
**Status:** Agreed  
**Versions:** core v1.34.0, smeldr.dev/mcp v1.16.0, smeldr.dev/cli v0.12.0

### Decision

Close the gap between the existing `RedirectStore` persistence layer and runtime
management. Today operators hand-create the `smeldr_redirects` table and hardcode
redirects in Go. T30 makes the full redirect lifecycle operator/agent-manageable
at runtime — no DDL, no restart.

### New exported surface (core — `redirects.go`, `forge.go`)

- **`CreateRedirectsTable(db DB) error`** — idempotent `CREATE TABLE IF NOT EXISTS`
  for `smeldr_redirects` (`from_path`, `to_path`, `code`, `is_prefix`). Follows
  the `CreateSiteConfigTable`/`CreateAuditTable`/`CreateBlockTables` pattern.
  Called automatically by `App.Redirects`; also exported for migration tools and
  tests.

- **`App.Redirects(db DB) error`** — new wiring method. Three responsibilities:
  1. `CreateRedirectsTable(db)` (idempotent — no DDL required from operator)
  2. `a.redirectStore.Load(ctx, db)` — loads saved entries into in-memory store
  3. Stores `db` in new `App.redirectDB` field so MCP tools can persist changes
  The existing `App.RedirectStore().Load(ctx, db)` pattern continues to work
  unchanged — `App.Redirects(db)` is additive.

- **`App.RedirectDB() DB`** — read accessor; returns the stored DB or nil.
  Used by `smeldr.dev/mcp` to gate and service redirect tools.

- **`RedirectStore.Delete(from string)`** — in-memory removal. Removes from both
  `exact` map and `prefix` slice. Parallel to `Add()`. MCP tools call
  `Remove(ctx, db, from)` (DB) + `Delete(from)` (in-memory) for immediate effect.

**Godoc updates:** `Load()`, `Save()`, `Remove()` "table must exist" notes removed.

### New MCP tools (`smeldr.dev/mcp` — `redirect_tools.go`)

Three tools, auto-registered when `App.RedirectDB() != nil` (i.e. `App.Redirects(db)`
was called). Dispatched before module-scoped tools (same pattern as block/webhook).

| Tool | Role | Description |
|------|------|-------------|
| `create_redirect` | Editor+ | Create/upsert a redirect. `from` (required, must start with `/`), `to`, `code` (301/302/410), `is_prefix`. Calls `Save`+`Add`. |
| `list_redirects` | Editor+ | Returns all entries sorted by `from` (exact + prefix). |
| `delete_redirect` | Editor+ | Delete by `from` path. Calls `Remove`+`Delete`. |

**Role: Editor** — redirects are a content-management operation (managing moved/renamed
pages), directly analogous to nav items (also Editor). Not security-sensitive
infrastructure (unlike tokens/webhooks, which are Admin).

### New CLI commands (`smeldr.dev/cli` — `redirect.go`)

`redirect` command group via `mcpCall` (pure HTTP client, no core/mcp import):

```
forge-cli redirect list [--json]
forge-cli redirect create --from <path> --to <path> [--code 301|302|410] [--prefix]
forge-cli redirect delete <from-path>
```

`redirect list` prints an aligned table (FROM, TO, CODE, PREFIX); `--json` for raw.

### Option C — auto-redirect on slug change

Assessed and split as T30b (fast-follow). T30 ships the foundation
(`App.Redirects`, `RedirectStore.Delete`, MCP create/delete primitives) that T30b
depends on.

### Consequences

- Operators calling `app.RedirectStore().Load(ctx, db)` at startup continue to work
  unchanged — the old pattern is not deprecated. `App.Redirects(db)` is the
  preferred new pattern.
- `App.RedirectDB()` is the gate for MCP redirect tools. Without `App.Redirects(db)`,
  no redirect tools are exposed.
- `RedirectStore.Delete(from)` is in-memory-only; always pair with
  `RedirectStore.Remove(ctx, db, from)` for full persistence.
- 9 new core tests (`redirects_test.go`), 9 new MCP tests (`redirect_tools_test.go`),
  8 new CLI tests (`redirect_test.go`).

---

## A126 — T04: `/_stats` endpoint (content + media statistics)

**Date:** 2026-06-04  
**Status:** Agreed  
**Version:** core v1.35.0

### Decision

Add `GET /_stats` — an Admin-only JSON endpoint that returns per-type item counts
across all registered content modules, plus an extensible external-provider slot
for media and other companion modules. Paired with Go 1.26.4 toolchain bump.

### New exported surface (`stats.go`)

- **`ContentTypeStats`** — `{type, prefix, counts: {status: int}}` per module.
- **`SiteStats`** — `{content: [], external?: {}, generated_at: RFC3339}`.
- **`StatsExtProvider`** interface — `StatsKey() string` + `ProvideStats(ctx) (map[string]any, error)`.
  External modules implement and register with `App.RegisterStatsProvider(p)`.
  A non-nil error is logged at Warn and omits that provider — never fails the whole call.
- **`App.Stats(ctx context.Context) (SiteStats, error)`** — aggregates across all
  `statsCollectors` (collected via type assertion in `App.Content()`) and external providers.
- **`App.StatsHandler()`** — mounts `GET /_stats`. Auth: Admin bearer token.
  Reuses `BearerHMAC` default auth; same pattern as `/_audit`.
- **`App.RegisterStatsProvider(p StatsExtProvider)`** — additive registration.

### Private additions (`storage.go`, `module.go`)

- **`statusCounter`** (private) — `countByStatus(ctx) (map[Status]int, error)`.
  Implemented by `MemoryRepo[T]` (in-memory count) and `SQLRepo[T]` (single `GROUP BY`).
  Custom repos that don't implement it degrade gracefully (empty counts).
- **`statsCollector`** (private) — `collectStats(ctx) ContentTypeStats`.
  Implemented by `Module[T]`; collected via type assertion in `App.Content()`.

### Access gate: Admin

`/_health` is public (reverse-proxy). `/_stats` exposes internal metadata
(content volumes; potentially media disk usage via external providers). Editor
would be too broad. Admin matches the tier of token/webhook management.

### Go 1.26.4 toolchain bump

`go.mod` `go` directive bumped across core, mcp, and cli.
Closes GO-2026-5039 (net/textproto) and GO-2026-5037 (crypto/x509).
CI uses `go-version-file: go.mod` — bump takes effect immediately on push.

### Tests

11 new tests in `stats_test.go`: JSON serialisation, `MemoryRepo.countByStatus`,
`SQLRepo.countByStatus`, `App.Stats` empty/module/external/error-degrades,
`GET /_stats` Admin 200 / Editor 403 / no-token 401.

---

## A127 — smeldr.dev/cli: nav command group (T18)

**Status:** Agreed — 2026-06-04
**File:** `cli/nav.go` (new) + `cli/main.go`
**Principle:** N10 — CLI/MCP parity

### Decision

Add a `nav` command group to smeldr-cli (`nav.go`) that reaches full parity with
the four nav MCP tools that ship in smeldr.dev/mcp:

- `nav list [--json]` — calls `list_nav_items`, renders table (ID, LABEL, PATH, PARENT, HIDDEN, GHOST, SORT) or raw JSON.
- `nav create --label <label> [--path] [--parent-id] [--module] [--hidden] [--ghost] [--sort-order]` — calls `create_nav_item`.
- `nav update <id> [same optional flags]` — calls `update_nav_item`; absent fields preserved.
- `nav delete <id>` — calls `delete_nav_item`; cascades to descendants on the server.

All four verbs require Editor role (enforced by the MCP server). `list` works in
any nav mode; create/update/delete require DB nav mode — the server surfaces a clear
error when attempted against a non-DB instance.

### Consequences

- CLI/MCP parity gap (tracked since nav shipped in mcp v1.4.0) is closed.
- "Current known gap" block removed from `.github/copilot-instructions.md`.
- smeldr.dev/cli bumped to v0.13.0 (new commands = minor version).
- No core or mcp changes.

---

## A128 — T79: in-memory log capture + `GET /_logs` (live error debugging)

**Status:** Agreed — 2026-06-05 · core v1.36.0

### Context

`smeldr-cli` had no way to surface a running instance's error logs. The driving
use case is debugging "when AI is unavailable" — so the path must NOT depend on
MCP (MCP may be the thing that is down). Core used the default `slog` handler to
stderr: no capture, no buffer, no queryable endpoint. This needs core
infrastructure plus an endpoint, wired the same opt-in way as `/_stats`
(`RegisterStatsProvider`/`StatsHandler`) and `/_audit` (`App.Audit`).

Shipped across two steps on one feature branch (Step A `logcapture.go` + tests;
Step B wiring + endpoint + integration + docs), squashed to main together.

### Decision

**Capture (Step A) — `logcapture.go`:**
- `App.CaptureLogs(opts ...LogCaptureOption) *App` installs a **teeing**
  `slog.Handler` and calls `slog.SetDefault`: every record still reaches the
  existing handler (stderr) AND records at/above the ring level are captured into
  a bounded in-memory ring. Additive — without the call nothing changes.
- `LogEntry{Time, Level, Msg, Attrs, Seq}` (JSON wire shape).
- `WithLogCapacity(n)` (default **500**) and `WithLogLevel(level)` (default
  **WARN**).
- Tee contract: `Enabled = inner.Enabled || level>=ringMin` — the OR guarantees
  the inner (stderr) threshold is never narrowed. `WithAttrs`/`WithGroup` carry
  attrs and groups to both the inner handler and the captured entry (nested groups
  → nested maps).
- Ring: fixed-capacity circular buffer, `sync.Mutex`-guarded, overwrite-oldest
  eviction, monotonic `seq`, `dropped` counter; `snapshot()` returns newest-first.

**slog/log re-entrancy guard (the load-bearing fix):** `slog.SetDefault` also
repoints the standard `log` package through the new handler. slog's built-in
zero-config handler (`*slog.defaultHandler`) itself writes *via* the log package,
so wrapping it and reinstalling creates an infinite re-entrant loop
(`log → tee → defaultHandler → log → …`) that deadlocks on the log mutex — it
would freeze any zero-config app on its first WARN. `CaptureLogs` therefore
substitutes a direct `os.Stderr` text handler as the forwarding target **only**
when the current default is the built-in handler (detected by the stable type
name `*slog.defaultHandler`). Apps that configure their own handler (the
recommended path) are wrapped unchanged.

**Endpoint (Step B) — `forge.go` + `logcapture.go`:**
- `CaptureLogs` stores the ring on `App`; `GET /_logs` is registered at
  `Handler()`/`Run()` time, mirroring the `/_audit` block. Route absent → **404**
  when `CaptureLogs` was not called.
- `GET /_logs` requires the **Admin** role; auth resolves as `cfg.Auth` else
  `BearerHMAC(secret)` — plain HTTP + bearer, so it works when MCP is down.
  401 (no/invalid token), 403 (authenticated, wrong role).
- Response envelope `{capacity, count, dropped, entries}` (entries newest-first;
  `entries` is always `[]`, never null). Query params: `level` (min level,
  inclusive `>=`), `limit` (most recent N), `since` (RFC3339, strictly after).
  Malformed param → 400.

### Stance

- **Ephemeral live-debugging facility, NOT log storage.** In-memory, bounded, lost
  on restart. stderr stays the durable path (the tee preserves it untouched);
  durability/rotation/aggregation is a non-goal.
- **HTTP + CLI only — no MCP tool.** The feature must not depend on the subsystem
  it helps debug. N10 governs MCP-tool→CLI parity, not endpoint→MCP; `/_stats`,
  `/_audit`, `/_health` set the precedent (plain HTTP admin endpoints, no MCP tool).
- **No redaction in v1.** Admin-only + in-memory; "do not log secrets" is the
  documented stance. A `WithLogRedactor` hook is noted as a possible future option.
- **Ordering constraint:** `CaptureLogs` wraps `slog.Default().Handler()` at call
  time, so it must be called AFTER any app-side `slog.SetDefault`.

### Consequences

- New exported core API: `CaptureLogs`, `LogEntry`, `LogCaptureOption`,
  `WithLogCapacity`, `WithLogLevel`; new endpoint `GET /_logs`. Minor bump
  **core v1.36.0**.
- Zero-config apps that opt in see stderr lines reformatted to text-handler format
  (still stderr, still tee'd, INFO+ preserved) — an accepted, documented trade-off
  of the re-entrancy guard.
- Internal-type detection (`*slog.defaultHandler`) is covered by a test that fails
  cleanly (prints the new type name) if a future Go release renames it, rather than
  regressing into the deadlock.
- `smeldr-cli logs` (calls `GET /_logs` directly, not MCP) ships as a separate
  follow-up step in the cli repo; core ships first so the endpoint exists.
- Integration group **G37** exercises `/_logs` with M1 auth/roles.

---
