# Smeldr — Decisions Archive (Phase 16)

Archived from `decisions/recent.md` on 2026-07-24. Entries A210–A214.

---

## A210 — T133: mcp v1.29.1 patch tag + CLAUDE.md Level 1 amendment classification fix

**Status:** Done  
**Date:** 2026-07-06  
**Repos:** smeldr.dev/mcp, smeldr/core

### What was decided

1. **mcp v1.29.1 patch tag** — Tagged and released smeldr.dev/mcp v1.29.1 covering T130 (staticcheck fixes, no consumer impact) and T132 (discoverToolDef moved to position 0, consumer-observable). Although no exported Go symbol changed between v1.29.0 and v1.29.1, the tool ordering change is consumer-observable: MCP clients with tool-list caps now reliably receive `list_type_tools` (A207). Patch bump required.

2. **CLAUDE.md classification clarification** — Two edits: (a) Level 1 examples now state that for standalone modules, a fix that changes consumer-observable behaviour requires a patch tag even if no exported symbol changed; "no version bump" means "no consumer-visible behaviour changed." (b) The "Amendments alone do not get a tag" when-to-tag rule gains an explicit exception for standalone-module amendments that change consumer-observable behaviour.

### Why

T132 was classified "no version bump" because no exported Go symbol changed. But the tool ordering change is directly observable to every MCP client — any client with a tool-list cap sees a different tool set on v1.29.0 vs v1.29.1. A consumer pinning v1.29.0 in go.mod cannot receive the fix via `go get` without a tag. The CLAUDE.md rule now uses "consumer-visible behaviour" as the gate, not "exported symbol changed."

### Consequences

- Consumers can `go get smeldr.dev/mcp@v1.29.1` to receive the tool-ordering fix.
- CLAUDE.md process rule is unambiguous for future standalone-module amendments.
- No core version bump. No core tag. Level 1 amendment.

---

## A211 — T137: Social REST API (smeldr.dev/social v0.10.0) and CLI REST switch (smeldr.dev/cli v0.15.2)

**Status:** Done  
**Date:** 2026-07-09  
**Repos:** smeldr.dev/social, smeldr.dev/cli

### What was decided

1. **Social REST API (smeldr.dev/social v0.10.0)** — Implemented 5 REST endpoints in `Social.Register()` (new file: `post_http.go`):
   - `POST   /social/posts` — create a ScheduledPost
   - `GET    /social/posts` — list ScheduledPosts (optional `?status=` filter)
   - `GET    /social/posts/{id}` — get one ScheduledPost
   - `PUT    /social/posts/{id}` — patch-merge update (only JSON keys present in body are applied; avoids requiring clients to re-send scheduler-managed fields)
   - `DELETE /social/posts/{id}` — delete (204 No Content)

   All endpoints require Bearer token validation via `smeldr.VerifyBearerToken(r, s.cfg.Secret, s.tokens)`. `Social.tokens *smeldr.TokenStore` internal field added and initialised in `New()` with `smeldr.NewTokenStore(db, string(cfg.Secret))`. Comprehensive test coverage: `post_http_test.go` (24 tests covering all endpoints and error paths). `export_test.go` updated with `PostHandlerForTest()` for white-box test access.

2. **CLI REST switch (smeldr.dev/cli v0.15.2)** — Switched 7 `runSocialPost*` functions from `mcpCall()` to `request()` against `SMELDR_URL` REST endpoints. `SMELDR_MCP_URL` no longer required for post commands (still required for credential/schedule/platform commands). Help text updated to document the split. `cliVersion` bumped to `"0.15.2"`.

### Why

`social/README.md` has always documented these 5 REST endpoints, but they were never implemented — operators and CLI could only manage posts via MCP, which requires the MCP server running. REST enables direct Bearer-token access and lets `smeldr-cli social post` commands work against the Smeldr HTTP API directly without MCP infrastructure.

### Consequences

- New HTTP surface on smeldr.dev/social: 5 REST routes with Bearer auth. `Social.tokens` remains internal — no change to the public `Social` struct interface.
- `Social.Register()` now registers 5 additional routes (behaviour change).
- CLI post commands now require `SMELDR_URL` + `SMELDR_TOKEN`; `SMELDR_MCP_URL` no longer needed for post operations.
- social v0.10.0 (minor bump — new HTTP surface). cli v0.15.2 (patch bump — behaviour change, no new commands).

---

## A212 — AutoMigrate NodeRevColumn in Module.setDB (T136)

**Status:** Done
**Date:** 2026-07-10
**Repo:** smeldr/core

### What was decided

1. `Module[T].setDB` now calls `MigrateNodeRevColumn(db, tn.tableName())` automatically when `m.repo` implements an unexported `tableName() string` interface. Only `*SQLRepo[T]` implements this interface.
2. `SQLRepo[T]` gains an unexported method `tableName() string { return r.table }`.
3. nil-DB guard: if `db == nil`, the migration is skipped (no panic).
4. Error strategy: `slog.Warn` and continue (same precedent as `MigrateSchemaKindColumn` in `smeldr.go`).
5. Two new tests: `TestSetDB_AutoMigratesRevColumn` (verifies migration runs and Save succeeds after), `TestSetDB_SkipsMigrationForNonSQLRepo` (verifies MemoryRepo does not implement `tableName()`).
6. Version: core v1.54.2 (patch).

### Why

`MigrateNodeRevColumn` existed and was tested since an earlier amendment but was never called by the framework. Operators were expected to call it manually. Nobody did — including the production smeldr.dev deployment — causing a 2026-07-07 outage that required a direct SSH emergency patch to the live SQLite database.

### Consequences

- `Module[T].setDB` calls `MigrateNodeRevColumn` at module registration time — new DB operation at startup.
- Zero operator action required for new or existing deployments.
- Custom repos (not `*SQLRepo[T]`) are unaffected.
- `docs/ARCHITECTURE.md` unchanged (no new exported symbols).
- No exported symbols added or removed.

---

## A213 — Go 1.26.5 Toolchain Bump (T138)

**Status:** Done
**Date:** 2026-07-10
**Repos:** smeldr/core, smeldr.dev/mcp, smeldr.dev/cli, smeldr.dev/agent, smeldr.dev/social, smeldr.dev/media, smeldr.dev/oauth

### What was decided

1. Bumped the `go` directive in `go.mod` to `1.26.5` in all 7 repos:
   - core: go 1.26.4 → go 1.26.5 (go.mod + go.work)
   - mcp: go 1.26.4 → go 1.26.5 (go.mod + go.work)
   - cli: go 1.26.4 → go 1.26.5 (go.mod only)
   - agent: go 1.26.4 → go 1.26.5 (go.mod only)
   - social: go 1.26.4 → go 1.26.5 (go.mod only)
   - media: go 1.26.3 → go 1.26.5 (go.mod + go.work)
   - oauth: go 1.26.3 → go 1.26.5 (go.mod only)
2. Also bumped go.work files in core, mcp, and media repos (which each have one).
3. No exported symbols changed. No consumer-visible behaviour changes.
4. No version bumps in any module's go.mod. No tags required.

### Why

GO-2026-5856: Encrypted Client Hello (ECH) privacy leak in crypto/tls affects all Go 1.26.x before 1.26.5. CI on core and mcp has been red since A211 because govulncheck flags this vulnerability. Fix: go 1.26.5 (also fixed in go 1.25.12 and go 1.27.0-rc.2). GOTOOLCHAIN=auto fetches 1.26.5 automatically on all platforms once go.mod declares it.

### Consequences

- CI govulncheck will be green on core and mcp after push.
- No exported symbols changed. No consumer-visible behaviour changes.
- No version bumps. No tags. Level 1 amendment — metadata-only change.
- `go build ./...` and `go test ./...` green in all 7 repos locally.

---

## A214 — T145: Context Packet v1 — bounded orchestration context envelope

**Status:** Done
**Date:** 2026-07-11
**Repos:** smeldr/core

### What was decided

New HTTP endpoint and context packet envelope (v1.0):

**Exported types (7):**
- `ContextPacket` — v1 bounded operational context JSON envelope
- `PacketSource` — identifies the Smeldr instance that produced the packet
- `PacketAnchor` — the focal orchestration item (type, id, slug, status, rev, url, fields)
- `PacketBoundary` — how the packet was assembled (method, depth, omitted counts)
- `PacketOmission` — per-type included/omitted item counts when cap exceeded
- `PacketItem` — one linked content node in the packet
- `PacketRelation` — one relation graph edge (both endpoints must be in packet)

**Exported functions/methods (2):**
- `BuildContextPacket(ctx, DB, *RelationStore, baseURL, sourceName, anchorType, anchorSlug string, depth int) (*ContextPacket, error)` — breadth-first traversal over all 5 anchor types (goal, decision, amendment, task, signal), depth 1–2, per-type cap 25 items, seenEdge + seenNode dedup (diamond-safe)
- `App.ContextPacketHandler(rs *RelationStore, sourceName string)` — mounts `GET /packet/{type}/{slug}[?depth=]` unauthenticated HTTP endpoint

**Key design decisions:**
- Anchor always present (with Status, Rev, Fields); NOT duplicated in Items
- Relations only emitted when both endpoints present (as Anchor or in Items)
- Ghost links (nodeID with no DB row) silently skipped with slog.Warn
- `QueryGoalContext` unchanged — backward compat preserved
- No MCP tool (HTTP-only v1); no auth; snapshot only (generated_at timestamp)
- `packet_version: "1.0"`, per-type cap: 25
- Anchor and linked items are filtered to Published status only — matches the existing sitewide "non-Published content is never publicly visible" convention (`Module.isVisible`); a Draft anchor returns `ErrNotFound`, a Draft linked item is silently excluded (not counted in `Boundary.Omitted`)

### Why

M3 public demo (`T111`) requires a "Continue with your own AI" action — a hand-off that carries bounded, portable, provenance-carrying operational context to an isolated instance or external AI agent. Design settled through a Fable 5 research round. This is the first concrete implementation slice: HTTP-only v1, snapshot semantics, no authentication, no MCP tool yet.

### Consequences

- New public HTTP endpoint `GET /packet/{type}/{slug}[?depth=]` (unauthenticated; intended for isolated demo instance with public read access)
- 7 new exported types, 1 new exported function (`BuildContextPacket`), 1 new `App` method (`ContextPacketHandler`)
- v1.55.0 (additive, no breaking changes)
- `QueryGoalContext` unchanged (backward compat)
- Level 2 amendment (new exported symbols + new public HTTP route)
