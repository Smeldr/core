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
Archived 2026-06-09: A131–A135 → phase6-archive.md
Archived 2026-06-10: A136–A138 → phase7-archive.md
Archived 2026-06-15: A139–A150 → phase8-archive.md
Archived 2026-06-23: A151–A157 → phase9-archive.md
Archived 2026-07-01: A158–A169 → phase10-archive.md
Archived 2026-07-02: A170, A171, A173–A183 → phase11-archive.md
Archived 2026-07-04: A184–A190 → phase12-archive.md
Archived 2026-07-05: A191–A193 → phase13-archive.md
Archived 2026-07-05: A194–A200 → phase14-archive.md
Archived 2026-07-10: A201–A209 → phase15-archive.md
Archived 2026-07-24: A210–A214 → phase16-archive.md
Archived 2026-07-28: A215–A219 → phase17-archive.md
Archived 2026-07-29: A220-A221 → phase18-archive.md

---

## A222 — Webhook/outbound TIMESTAMPTZ scan bug

### What

`webhook.go`'s `List` and `EndpointsForEvent`, and `outbound.go`'s `fetchDueJobs`,
`ListJobsForEndpoint`, `ListDeliveryLogs`, and `DeliveryStats` all scanned columns
documented and created as `TIMESTAMPTZ` (`created_at`, `next_retry_at`,
`expires_at`, `attempted_at`) directly into `time.Time`/`*time.Time` destinations
via plain `rows.Scan`/`row.Scan` calls. `modernc.org/sqlite`'s driver only
auto-converts the exact column-type names `DATE`, `DATETIME`, and `TIMESTAMP` to
`time.Time` on scan — `TIMESTAMPTZ` is not on that list, and the scan fails hard:

```
sql: Scan error on column index N, name "...": unsupported Scan, storing
driver.Value type string into type *time.Time
```

`fetchDueJobs` is the core polling function the worker pool's background loop
calls every cycle to find due webhook jobs. Against the actually-documented,
required production DDL, outbound webhook delivery was completely non-functional
— not a partial degradation.

### Why undetected

Every test table across `webhook_test.go` (5 occurrences), `outbound_test.go` (1
shared helper), and `integration_full_test.go`'s G26–G29 cross-milestone groups
(the most production-realistic tests in the suite — G26 literally calls
`WebhookPool().ListJobsForEndpoint` after a real `MCPPublish`) declared these
columns as `DATETIME` instead of the documented `TIMESTAMPTZ`. Confirmed:
`modernc.org/sqlite`'s driver keys off the column's declared type name
(decltype), not the stored value's actual string format — so this was a pure
test/production DDL divergence masking a real, severe bug through 96%+ package
coverage the whole time.

### Fix

1. **Non-nullable fields** (`List`, `EndpointsForEvent`, `fetchDueJobs`,
   `ListJobsForEndpoint`, `ListDeliveryLogs`): wrapped each `*time.Time`
   scan destination in `scanDest()` — `storage.go`'s existing `timeScanner`
   (A200), already built for exactly this class of problem and already tested
   (`storage_sqlite_test.go`, 7 cases). Reused, not duplicated.
2. **Nullable field** (`DeliveryStats`'s `MAX(dl.attempted_at) *time.Time`
   return — `scanDest`'s type assertion matches `*time.Time`, not `**time.Time`,
   so it doesn't apply directly to a nullable-pointer return): scanned into a
   `*string` first, matching `TokenStore.List`'s existing nullable-field shape
   for its `RevokedAt`. **Deviation from the plan, with concrete evidence, not
   silently decided:** the plan (and the architect's approval) called for
   mirroring `TokenStore`'s exact parse — a bare `time.Parse(time.RFC3339, ...)`.
   Implemented as specified, then verified with a new real round-trip test
   (`TestWorkerPool_DeliveryStats_realAttempt`) rather than assumed — the test
   failed. A throwaway debug test revealed why: `TokenStore.Create` explicitly
   pre-formats its own writes with `.Format(time.RFC3339)` before insert, making
   its own reader/writer contract self-consistent by construction.
   `outbound.go`'s writes are never pre-formatted — they insert a raw Go
   `time.Time` value, and the driver's own stringification produced
   `"2026-07-28 17:58:07.7222132 +0000 UTC"` (Go's default `time.Time.String()`
   shape), which a bare RFC3339 parse cannot read (no `T` separator) but which
   `timeScanner`'s existing broader layout list already handles. Final fix:
   `timeScanner{dst: &t}.Scan(*maxAtStr)`, calling the same already-tested
   unexported method directly on the scanned string — not a new, generalized
   `nullableTimeScanner` type (the architect's specific "don't build one for a
   single use site" guidance is still honored; this reuses the one helper that
   already exists rather than adding a second).
3. **Root cause of the malformed string, fixed at the source, not papered
   over on the read side:** `realClock.Now()` (`outbound.go`, the production
   `Clock` implementation) and `webhookDispatch`'s local `now := time.Now()`
   (`webhook.go`) both returned local, monotonic-clock-bearing `time.Time`
   values — every other timestamp-generation site in this codebase already
   calls `.UTC()` (`WebhookStore.Create`, `provenance.go`'s `recordProvenance`,
   `TokenStore.Create`). `realClock.Now()` now returns `time.Now().UTC()` —
   one source-of-truth fix, rather than patching every individual call site
   that reads `p.clock.Now()`.
4. **Test DDL and fixture corrections**, required, not optional (otherwise this
   exact bug class regresses invisibly again): all `DATETIME` column
   declarations in `webhook_test.go`, `outbound_test.go`, and
   `integration_full_test.go`'s G26–G29 table helpers corrected to
   `TIMESTAMPTZ`. Eight `datetime('now')` SQL-literal inserts (across
   `webhook_test.go` and `integration_full_test.go`) replaced with a real
   parameterized `time.Now().UTC()` value, matching how production code
   actually writes these columns (it never uses SQLite's `datetime()` SQL
   function). Six `newFakeClock(time.Now())` construction sites (across
   `outbound_test.go` and `integration_full_test.go`'s G27–G29) corrected to
   `.UTC()` for the same monotonic-clock reason as `realClock.Now()`.
5. **New regression test**: `TestWorkerPool_DeliveryStats_realAttempt` exercises
   the actual `Enqueue` → `fetchDueJobs` → `processJob` → `DeliveryStats` round
   trip end-to-end — the only way to have caught the RFC3339-parse gap before
   shipping, and the guard against this exact bug recurring in `DeliveryStats`
   specifically.

### Consequences

- No exported Go symbols changed. `Clock` interface signature unchanged (only
  `realClock`'s returned value changed, from local+monotonic to UTC).
- Fixes a previously-undetected, severe production bug: outbound webhook
  delivery was completely non-functional against any SQLite database following
  the documented `TIMESTAMPTZ` DDL.
- Test suite now genuinely exercises the documented production schema across
  all three affected test files, closing the exact gap that let this ship
  undetected through 96%+ coverage.
- New test: `TestWorkerPool_DeliveryStats_realAttempt`. Coverage: 96.1%.
- Patch release: v1.57.1 → v1.57.2. No API change.
- Level 2 amendment (cross-file, production-critical behaviour fix).

---

## A223 — Finish the Forge→Smeldr rename (T176)

### What

`core/pgx` submodule: Go package declaration renamed `forgepgx` → `pgx` across
`pgx.go` and its 3 test files, matching the module's own import path
(`smeldr.dev/core/pgx`) and every other renamed companion package's convention
(package name equals its import path's last segment). `forgepgx.Wrap` →
`pgx.Wrap` in godoc examples and `storage.go`'s cross-reference comment.
`TestWrap_compilesAsForgeDB` → `TestWrap_compilesAsSmeldrDB`;
`runForgePgxRepoParity` → `runPgxRepoParity`; the integration test's
`CREATE TEMP TABLE forgepgx_test` → `smeldr_pgx_test` (a temp table scoped to
one test's connection lifetime — not a persisted-migration compatibility
concern, unlike the literal `forge_*` table names `migrate.go` preserves).
Tagged `pgx/v0.2.0` (path-prefixed — a same-repo submodule, not a standalone
repo like mcp/cli/media); `pgx/CHANGELOG.md` added (didn't exist before).

Full prose/log-prefix sweep across the rest of `core/`: `doc.go`, `storage.go`,
`benchmarks_test.go`, `example/docs/main.go` (named in NEXT.md), plus
`smeldr.go` (27 occurrences, heaviest single file), `module.go`, `mcp.go`,
`context.go`, `errors.go`, `head.go`, `nav.go`, `node.go`, `outbound.go`
(prose only), `relations.go`, `sitemap.go`, `social.go`, `templatedata.go`,
`templatehelpers.go`, `schema.go`, `markdown.go`, `example/blog/main.go`,
`example/api/main.go`, and roughly 15 test files (mostly `TestForgeXxx` →
`TestSmeldrXxx` identifier renames — zero external impact, all
unexported/test-only). Unexported Go identifiers renamed for consistency:
`forgeVersions` → `smeldrVersions`, `mcpParseForgeTag` → `mcpParseSmeldrTag`.
Stale `"forge:head"` template-name documentation corrected to `"smeldr:head"`
(the real reserved partial name, per `templates.go:70` — confirmed no runtime
code path treats the literal `"forge:head"` string as reserved, so this was a
stale doc, not a hidden compat surface).

### Why

Found during an awesome-go submission readiness review: a repo that still
visibly says "Forge" in its primary pkg.go.dev-facing documentation and public
API (`core/pgx`'s own package name) reads as an incomplete or abandoned
rebrand, undermining the credibility the submission is trying to establish.

The initial grep sweep, run before any code was touched, found the scope was
far larger than NEXT.md's own text implied — roughly 100 occurrences across 34
files, not just the 4 files named. Full scope was proposed and confirmed with
the architect before implementation started, matching the same thoroughness
the original A129–A132 Forge→Smeldr rename campaign applied to each
standalone module.

### Real bugs found during the sweep, fixed in the same commit

The sweep surfaced six defects entangled with the stale naming — not pure
cosmetics:

1. **`doc.go`'s broken `[Signal]` cross-reference.** Amendment A183 renamed
   `Signal` to `LifecycleEvent`, then reused the bare name `Signal` for a new,
   unrelated orchestration content type (`orchestration.go`). The godoc link
   in `doc.go`'s package comment silently resolved to the wrong type ever
   since. Fixed to `[LifecycleEvent]`.
2. **`ai.go`'s dead-function godoc example.** `LLMsTemplateData`'s example
   showed `{{forge_llms_entries .}}`; the real template function (per
   `templatehelpers.go`) is `smeldr_llms_entries`. Copy-pasting the example
   produced a template parse error.
3. **`ai.go`'s actual `/llms-full.txt` output string.** The generated footer
   line read `"> Generated by Forge on %s | ..."` — real, externally-visible
   product output served to AI crawlers, not a comment. Changed to "Generated
   by Smeldr on...", with `ai_test.go`'s matching assertion updated in
   lockstep.
4. **`example/docs/main.go`'s dead struct-tag code sample.** The embedded
   doc-page sample showed `Title string \`forge:"required,min=3"\`` — the
   real, current tag key is `smeldr` (A107). Confirmed against
   `module.go`'s actual tag-key read (`sf.Tag.Get("smeldr")`): copy-pasting
   the old sample produces zero validation, silently, with no error raised.
5. **`smeldr_test.go`'s stale comment.** `TestApp_health_ok`'s doc comment
   claimed the `/_health` response has "a 'forge' version key" — the actual
   key, asserted by the sibling test two functions down, is `"core"`.
   Comment corrected; that sibling test (`TestApp_health_forgeVersion`)
   renamed to `TestApp_health_coreVersion` to match what it actually checks.
6. **Log-prefix inconsistency.** `auth.go`'s `basicAuthWarn` constant and
   three `WARN` messages in `smeldr.go` (TokenStore probe failure, NavTree
   migration/load failure) all used a `forge:` prefix, while the `panic`
   two lines away in the same function already correctly said `smeldr:` —
   a real, operator-visible stderr inconsistency within the same function.
   All four corrected to `smeldr:`.

### Preserve — explicit, not touched

- `migrate.go`'s `migrateLegacyTableNames` and its 7 literal `forge_*` →
  `smeldr_*` table-rename pairs, plus `migrate_test.go`'s tests of it, plus
  `smeldr.go`'s operator-facing "rename forge_* tables manually" guidance —
  this code's entire purpose is detecting/renaming OLD `forge_*` artifacts in
  existing production databases; the literal old names must stay as literal
  strings for the migration logic to work.
- `X-Forge-Signature`/`X-Forge-Timestamp`/`X-Forge-Event`/`X-Forge-Delivery`
  HTTP headers in `outbound.go`/`outbound_test.go` — explicitly documented in
  the code itself as intentional during the T86/T87 deprecation window.
- `example/server/main.go`'s `mcp.WithForgeFallback()` call — an external
  `smeldr.dev/mcp` module API this example merely calls; not core's to
  rename (mcp's own A130 already documents this as intentionally preserved).
- `example/server/seed_goals/main.go`'s `Description` seed-data strings —
  verbatim historical/planning narrative (past incidents, the T86
  deprecation plan naming `forge://`/`X-Forge-*`/`FORGE_*` as things to be
  removed *later*), not prose to rewrite.

### Incidental finding, flagged separately, not fixed here

While verifying `example/blog`'s tests after the rename, `TestBlogSignal` and
`TestBlogFullServer/audit/recordedOnPublish` were found failing
deterministically with a 409 `rev_conflict` on a PUT-publish request.
Confirmed via `git stash` that this failure exists against the unmodified
pre-rename code too — unrelated to this amendment, a genuine pre-existing bug
or test-timing issue in the optimistic-concurrency path. Flagged as a
separate tracked task rather than fixed here (out of scope for a rename).

### Consequences

- No exported Go symbols changed anywhere in `core`. `pgx`'s package rename is
  its own module's breaking-minor change (v0.1.2 → v0.2.0), independent of
  core's own version.
- Two changes are consumer-observable despite no API change: the
  `/llms-full.txt` generated footer text, and the four log-prefix fixes
  (operator-visible stderr). Patch release: v1.57.2 → v1.57.3.
- `pgx/CHANGELOG.md` created (new file, this module had none before).
- Coverage: 96.1% (unchanged — comment/string/unexported-identifier changes
  only, plus the `ai_test.go` assertion update to match the footer-string
  change).
- Level 2 amendment (cross-file, package rename with version bump, includes
  genuine behavioural/documentation-correctness fixes, not pure cosmetics).

Level 2 amendment.

---
