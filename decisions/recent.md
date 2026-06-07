# Smeldr — Recent Decisions

Rolling working file. All new decisions are added here first.
When this file approaches ~20KB, report it at session start — the architect
will issue archiving instructions via NEXT.md.

Non-Decisions go directly to `nondecisions.md` — not here.

Archived 2026-05-25: D32 → docs.md · A87, A97–A101 → core.md
Archived 2026-05-30: A102–A115 → phase3-archive.md
Archived 2026-06-04: A116–A120 → phase3-archive.md
Archived 2026-06-05: A121–A125 → phase4-archive.md

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

## A129 - T100 Step 1: oauth package rename (smeldr.dev/oauth v0.2.0)

**Date:** 2026-06-06
**Status:** Implemented

Renamed the Go package declaration in `smeldr.dev/oauth` from `forgeoauth` to
`oauth`, and the external test package from `forgeoauth_test` to `oauth_test`.
This is T100 Step 1 of the coordinated standalone-module forge-naming cleanup
(the package declarations were never renamed when the framework became Smeldr).

Scope of the rename (this module's own forge-named residue only):
- package declaration in all 9 production files + both `_test` files
- godoc package comment and selector examples (`forgeoauth.X` -> `oauth.X`)
- error-string prefixes in sentinels, panics, and `fmt.Errorf` (`forgeoauth:` -> `oauth:`)
- slog log-message prefixes in authorize.go (7) and token.go (4) (`forge-oauth:` -> `oauth:`)
- README v0.2.0 badge + migration note + stale `forge-cms.dev` -> `smeldr.dev` paths
- CHANGELOG header + v0.2.0 breaking-minor section

Preserved (out of T100 scope): `forge_oauth_*` SQLite table names (DB-migration
scope, would break existing oauth.db), `forgemcp`/`forge-mcp` references (the mcp
module, renamed in Step 2), and `valid-forge-token`/`forge-test.invalid` test
fixtures.

No exported-symbol change, no behaviour change (`errors.Is` matches by value, not
string). Released as **v0.2.0** (breaking-minor per project precedent - the
package qualifier change breaks importers' selectors, but ~0 external importers,
so a `/v2` module path is overkill). This tag gates T100 Step 2 (mcp v1.17.0),
which imports `smeldr.dev/oauth`.

---

## A130 - T100 Step 2: mcp package rename + oauth v0.2.0 adoption (smeldr.dev/mcp v1.17.0)

**Date:** 2026-06-06
**Status:** Implemented

Renamed the Go package declaration in `smeldr.dev/mcp` from `forgemcp` to `mcp`
across all 16 production + internal test files, and adopted the renamed oauth
package. T100 Step 2 (gated on oauth v0.2.0 from A129, now satisfied).

Scope of the rename (this module's own forge-named residue + oauth adoption):
- package declaration in all 16 `.go` files (test files are internal
  `package forgemcp` → `package mcp`, not `_test`)
- godoc selector examples `forgemcp.X` → `mcp.X`; package-doc "Forge" → "Smeldr"
- oauth adoption: dropped the `forgeoauth` import alias, `forgeoauth.X` → `oauth.X`
  selectors, bumped `smeldr.dev/oauth` dep v0.1.5 → v0.2.0 + `go mod tidy`. Values
  unchanged - `errors.Is(err, oauth.ErrTokenNotFound)` still matches.
- `WithOAuth` parameter renamed `oauth` → `srv` to avoid shadowing the now-bare
  `oauth` package name (parameter names are not part of the call signature)
- stale godoc fixed: `forge_format`/`forge_description` → `smeldr_format`/
  `smeldr_description` (the struct-tag keys were renamed to `smeldr_*` in T62/A107;
  the comments described non-existent tags and were misleading)
- `forge-media` prose → `media`; `forge-operator` → `operator`
- README (drop alias, `mcp.X` selectors, migration note) + CHANGELOG header + v1.17.0

Preserved (out of T100 scope): `WithForgeFallback` API + `forgeFallback` field
(T86/T87 legacy forge-bearer compat); `forge://` resource-URI parse-compat
(A123/T86); `forge-cli` (current binary name until Step 5) and `forgemedia.Register`
godoc (media still `package forgemedia` until Step 3); standalone "Forge"/"forge"
brand words in comments (`forge App`, `forge core`, `forgeCtx` var, etc. - tracked
as a separate brand-prose pass by the architect).

Exported mcp API unchanged (`New`, `WithBlocks`, `WithModule`, `WithOAuth`,
`WithForgeFallback`, `WithSecret`). No behaviour change. Breaking-MINOR **v1.17.0**.
mcp adds no further gate - Steps 3/4/5 (media, social, cli) are independent.

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
