# Forge — Recent Decisions

Rolling working file. All new decisions are added here first.
When this file approaches ~20KB, report it at session start — the architect
will issue archiving instructions via NEXT.md.

Non-Decisions go directly to `nondecisions.md` — not here.

---
## Decision D32 — decisions/ file system restructure

**Date:** 2026-05-17
**Status:** Active

**Context:**
decisions/phase2.md was the catch-all for all decisions from D25 onwards.
The name was misleading and the structure did not scale. One growing file
is unwieldy for agents reading it as context at session start.

**Decision:**
Restructure to a flat, role-separated system with a rolling working file.

**Design decisions reached in planning (2026-05-17):**
- Flat structure — no subdirectories. Topic files are always leaves.
  If a topic grows, split into flat siblings, never nested directories.
  Rationale: nested directories create inconsistent navigation depth for
  agents (2 hops vs. 3 hops). Flat keeps every lookup at exactly 2 hops:
  DECISIONS.md -> topic file.
- Rolling window is size-based (~20KB), not count-based.
  Rationale: agents pay in tokens, not entry count. A count-based limit is
  arbitrary — file size is the relevant constraint.
- Non-Decisions go directly to nondecisions.md, not through recent.md.
  They are a separate category and should not consume the working window.
- Structure description lives in DECISIONS.md header — single authoritative
  source. copilot-instructions.md references it.
  Rationale: the rules for a file system belong at the top of its index.
- Archiving is architect-directed, not autonomous.
  Corepilot reports when recent.md reaches ~20KB. Architect decides
  groupings and topic file names. Corepilot never groups autonomously.
  Rationale: deciding that A88+A92+A96 constitute "auth" requires
  cross-cutting architectural judgment the corepilot does not have.
- phase2.md renamed to phase2-archive.md (not deleted).
  Contains D25–A87, A96–A97, including foundational decisions D25–D31 that
  are still referenced. DECISIONS.md index makes content discoverable.

**Files:**
- decisions/recent.md — new decisions, rolling ~20KB window
- decisions/nondecisions.md — all Non-Decisions
- decisions/phase2-archive.md — archived (was phase2.md)
- decisions/core.md — unchanged
- decisions/[topic].md — created on architect instruction

---

## Amendment A87 � signals.go: AfterSchedule Signal constant

**Date:** 2026-05-06
**Status:** Agreed
**Milestone:** 11 / Layer 1

### Problem

The Scheduled status transition fires AfterUpdate but no dedicated signal. Webhook
consumers and MCP subscription listeners cannot distinguish a scheduling event from
a plain content edit without inspecting the payload. This makes it impossible to
subscribe to post.scheduled webhook events or react to scheduling via signal handlers.

### Change

**signals.go** � new constant added to the Signal const block:

`go
// AfterSchedule fires after a content item transitions to Scheduled status.
// It fires in addition to AfterUpdate � not instead of it. Runs
// asynchronously � errors and panics are logged, never returned.
AfterSchedule Signal = "after_schedule"
`

AfterSchedule is dispatched in module.go alongside AfterUpdate whenever

ewStatus == Scheduled && prevStatus != Scheduled. It fires from both HTTP
updateHandler and MCPSchedule.

### Consequences

- New exported Signal constant: additive only; no existing handlers affected.
- All code that ranges over m.signals is range-based; the new key is ignored
  unless a handler is registered with orge.On[T](AfterSchedule, fn).
- ARCHITECTURE.md updated: AfterSchedule added to the signal constants table.
- No breaking change; no required application updates.

---

## Amendment A97 — Built-in opt-in audit trail (T21)

**Date:** 2026-05-16
**Status:** Agreed
**Scope:** `audit.go` (new file), `forge.go` — `App.Audit`, `App.Handler` (Level 3 — new exported API)

**Problem:**
Applications built on Forge have no standard way to record who changed what and
when. Each team rolls bespoke audit tables. The signal bus (A94) already captures
every lifecycle transition with actor identity, content type, slug, and previous
state — but only for webhook delivery. There is no built-in persistence path.

**Decision:**
Add `App.Audit(store AuditStore) *App` — a single opt-in call that:
1. Subscribes to `AfterPublish`, `AfterSchedule`, `AfterArchive`, and `AfterDelete`
   via the existing signal bus.
2. On each event, appends an `AuditRecord` to the provided `AuditStore`.
3. Mounts `GET /_audit` (Editor-or-higher) that returns the stored records as a
   JSON array, filterable by `from`, `to` (RFC3339), `type`, and `actor`.

`AfterCreate` and `AfterUpdate` are intentionally excluded: they fire on every
save (including auto-saves / draft cycles) and would produce unbounded noise in
the audit log. The audit trail records only transitions that change publication
state.

**Implementation:**
- `AuditRecord` — immutable struct: `ID`, `Timestamp`, `Signal`, `ContentType`,
  `Slug`, `ActorID`, `ActorRole`, `PreviousState`.
- `AuditFilter` — query narrowing: `From`, `To time.Time`, `ContentType`, `ActorID string`.
- `AuditStore` interface — `Append(ctx, AuditRecord) error` + `List(ctx, AuditFilter) ([]AuditRecord, error)`.
- `NewAuditStore(db DB) AuditStore` — default SQL implementation; timestamps
  stored as RFC3339 strings for SQLite compatibility (same pattern as `auth.go`).
- `CreateAuditTable(db DB) error` — DDL helper; creates `forge_audit_log` table.
- `GET /_audit` mounted lazily in `App.Handler()` when `auditStore != nil`.
  Auth resolved with same nil-check pattern as `New()`.

**Why signal bus and not a module option:**
`App.Audit` is a third independent `OnSignal` subscriber, parallel to `App.Webhooks`
and forge-agent's subscriber. No shared helper is needed — the signal bus IS the
abstraction. Adding a module option would couple every content type to the audit
path unconditionally.

**Rejected alternatives:**
- *Module option `WithAudit()`*: couples audit to module registration; modules
  added after `App.Audit()` would require re-wiring.
- *Middleware/interceptor*: would require access to the HTTP handler layer, not
  the lifecycle layer. Actor identity is cleanly available on `SignalEvent`.
- *`AfterCreate`/`AfterUpdate` included*: rejected — drafts fire these on every
  auto-save; audit log would grow unboundedly with low-value entries.

**New exported symbols:**
- `audit.go`: `AuditRecord`, `AuditFilter`, `AuditStore`, `NewAuditStore`, `CreateAuditTable`
- `forge.go`: `App.Audit`

**New HTTP surface:**
- `GET /_audit` — requires Editor role; returns `[]AuditRecord` JSON.

**Forge core → v1.22.0.**

---

## Amendment A98 — Fix data race in notifyAfter (module.go)

**Date:** 2026-05-19
**Status:** Agreed

**Context:**
The `-race` gate added in the consolidation sprint CI immediately caught a
genuine production data race. The race detector reported concurrent read/write
at the same memory address:

- Write: `forge-cms.dev/forge.setNodeStatus()` — `module.go:1064`
- Read:  `forge-cms.dev/forge.extractNode()` — `ai.go:285`

Root cause: `notifyAfter` passes the original `item` pointer to two goroutines
without any synchronisation:

1. `dispatchAfter` → goroutine → `buildSignalEvent(item)` → `extractNode(item)`
   reads `Node` fields via reflection.
2. `afterHook` goroutine → reads `item` in the `fn` callback.

A subsequent MCP call on the same slug (e.g. MCPPublish after MCPSchedule)
calls `setNodeStatus`/`setNodeTime` on the same pointer before those goroutines
finish reading. This is a real production race — not a test-only artefact.

**Decision:**
Before passing `item` to any goroutine in `notifyAfter`, take a shallow copy
of the struct the pointer points to. Both goroutines receive the snapshot; the
caller retains the original pointer for subsequent operations.

A shallow struct copy is sufficient: the raced fields (`Status`, `PublishedAt`,
`ScheduledAt`) are value types or pointers to values fully written before
`notifyAfter` is called. No deep copy is needed.

**Implementation:**
New unexported function `snapshotItem(item any) any` in `module.go`:

```go
func snapshotItem(item any) any {
    rv := reflect.ValueOf(item)
    if rv.Kind() != reflect.Ptr || rv.IsNil() {
        return item
    }
    cp := reflect.New(rv.Elem().Type())
    cp.Elem().Set(rv.Elem())
    return cp.Interface()
}
```

`notifyAfter` calls `snapshotItem` once and passes `snap` to both goroutines.

**Alternatives rejected:**
- *Mutex on Node fields*: heavyweight; every read path would need locking.
- *Copy at call sites*: 17 call sites; error-prone.
- *Channel rendezvous*: adds latency to every post-lifecycle transition.

**No exported symbols changed. No interface changes.**

**Forge core → v1.22.1.**

---

## Amendment A99 — Go toolchain upgrade policy

**Context:** govulncheck CI failure on Go 1.26.2 (GO-2026-4982, GO-2026-4980,
GO-2026-4971, GO-2026-4918 — all fixed in Go 1.26.3) prompted formalising the
toolchain upgrade cadence.

**Decision:** Forge adopts the following Go toolchain upgrade policy:

- **Patch releases (1.26.x):** Follow promptly — within one sprint of release.
  Patch releases contain only bugfixes and security fixes, no breaking changes.
  govulncheck in CI acts as the practical trigger.

- **Minor releases (1.27, 1.28, …):** Upgrade within 1–2 months of release,
  or no later than when Go drops support for the previous minor. Go officially
  supports only the two most recent minor versions; running an unsupported minor
  means no security patches from the Go team.

- **go.mod `go` directive:** Always tracks the latest patch of the current minor
  (e.g. `go 1.26.3`, not `go 1.26.2`, once 1.26.3 is out).

- **`toolchain` directive:** Use when a patch bump is needed for govulncheck but
  the minimum language version for users should stay stable. Prefer bumping both
  `go` and `toolchain` together on patch upgrades to keep go.mod unambiguous.

**Policy recorded:** 2026-05-19. Next action: bump `go.mod` to `go 1.26.3`
once Go 1.26.3 is available in the local toolchain.

---

## Amendment A100 — Go 1.26.3 toolchain bump (v1.22.2)

**Context:** govulncheck CI failure on four stdlib CVEs (GO-2026-4982,
GO-2026-4980, GO-2026-4971, GO-2026-4918 — all fixed in Go 1.26.3).
Go 1.26.3 became available on 2026-05-19. Per A99 policy, patch upgrades
are followed within one sprint.

**Change:** `go.mod` `go` directive bumped from `go 1.26.2` to `go 1.26.3`.
CI uses `go-version-file: go.mod` so it auto-picks Go 1.26.3. No other
changes to go.mod or go.sum.

**Forge core → v1.22.2.**

---

## Amendment A101 — `SingleInstance()` and `Standalone()` module routing options (v1.23.0)

**Date:** 2026-05-23
**Status:** Agreed

**Context:**
Some content modules hold exactly one item (About, Contact, Terms) where the
canonical URL is the module prefix (`/about`), not a slug URL (`/about/some-slug`).
Other modules benefit from first-class top-level URLs where items live at
`/{slug}` rather than `/{prefix}/{slug}` (e.g. blog posts as landing pages).
Neither use-case is expressible with the existing Option set.

**Decision:**

### `SingleInstance() Option`
- Marks a module as single-instance.
- `Register()` mounts `singleInstanceHandler` at `GET /{prefix}` only.
- `GET /{prefix}/{slug}` is not registered — requests to it 404 via the
  redirect fallback.
- `singleInstanceHandler`: uses `repo.FindAll` with `Published` filter for
  guests; Author+ see all statuses. Serves `items[0]` via `renderShowHTML` or
  `writeContentCached`; 404 when the list is empty.
- Preview token support: loads all items, reads slug from the first item,
  validates the preview token (same HMAC model as `showHandler`). One extra
  repo read on preview path only.
- `MCPMeta().SingleInstance` returns `true`. forge-mcp suppresses the
  `list_{type}s` admin tool for SingleInstance modules (a single `get_{type}`
  is sufficient).
- aidoc (`/{prefix}/aidoc`) not registered for SingleInstance modules — the
  single-instance pattern implies no slug, so aidoc has no natural URL.

### `Standalone() Option`
- Marks a module as standalone-routed.
- `Register()` mounts the list handler at `GET /{prefix}` only. It does NOT
  mount `GET /{prefix}/{slug}` or `GET /{prefix}/{slug}/aidoc`.
- `App.Content()` detects Standalone modules via the `standaloneDispatcher`
  interface and appends them to `App.standaloneModules`.
- `App.Handler()` registers `GET /{slug}` and `GET /{slug}/aidoc` dispatch
  handlers when at least one Standalone module is present. These are
  registered before the `"/"` redirect fallback.
- `GET /{slug}` dispatch: iterates `standaloneModules`, calls
  `findAndServe(w, r, slug)` on each. First match wins. If none match, falls
  through to `redirectStore.handler()` (preserving redirect rules for
  single-segment paths).
- `GET /{slug}/aidoc` dispatch: same iteration, calls `findAndServeAIDoc`.
  Returns 404 if no module serves it.
- `findAndServe` honours preview tokens (same model as `showHandler`).
  Items not visible to the requester return `false` (dispatch continues).
- Sitemap, feed, and AI index URL generation uses `/{slug}` (not
  `/{prefix}/{slug}`) when `m.standalone` is true.
- aidoc registered via `standaloneDispatcher` only when module has AIDoc
  feature (`findAndServeAIDoc` returns false otherwise).

### URL generation 3-way branch
All three derived-content generators (sitemap, feed, AI index) apply the same
branching logic:
```
singleInstance → baseURL + prefix           (one URL for the whole module)
standalone     → baseURL + "/" + slug       (top-level slug URL)
normal         → baseURL + prefix + "/" + slug
```

### Call-site syntax
```go
// Single About page at /about
forge.NewModule((*About)(nil),
    forge.At("/about"),
    forge.Repo(repo),
    forge.SingleInstance(),
)

// Blog posts at /{slug} instead of /posts/{slug}
forge.NewModule((*Post)(nil),
    forge.At("/posts"),
    forge.Repo(repo),
    forge.Standalone(),
)
```

**Consequences:**
- No breaking changes. Both options are additive.
- `example_test.go` unaffected (no existing Example uses these options).
- `standaloneDispatcher` is unexported — no API surface risk.
- `MCPMeta.SingleInstance` is a new field (additive, zero-value = false for
  existing modules — no behavioural change).

**Files changed:** `mcp.go`, `module.go`, `forge.go`, `forge-mcp/mcp.go`,
`integration_full_test.go` (G34, G35), `CHANGELOG.md`, `docs/ARCHITECTURE.md`.

**Forge core → v1.23.0. forge-mcp → v1.10.0.**

---

### A102 — `module.go`: `APIOnly()` module option

**Date:** 2026-05-22
**Status:** Agreed
**File:** `module.go`

**What:**
`APIOnly() Option` marks a module as REST/MCP/CLI-only with no public HTML surface.
`GET /{prefix}` and `GET /{prefix}/{slug}` with `Accept: text/html` return 404. JSON
routes (`Accept: application/json` or absent `Accept`) are unchanged. MCP tools are
generated in full — the same as a regular module. forge-cli works via the REST JSON
API without any changes.

**Why:**
Forge registers HTML routes for all modules. For content types with no public HTML
representation — such as `HomePage` or platform config — a browser visiting the prefix
receives JSON rather than a 404. This is confusing and incorrect: the prefix should not
be a browsable URL at all. APIOnly() makes the intent explicit and enforceable.

**How:**
- `apiOnlyOption struct{}` + `APIOnly() Option` constructor
- `apiOnly bool` field on `Module[T]`
- `listHandler`, `showHandler`, `singleInstanceHandler`: early return 404 when
  `m.apiOnly` and `Accept` header contains `text/html` (explicit, non-wildcard)
- Startup panic when `APIOnly()` and `SingleInstance()` are combined — they are
  logically incompatible (`SingleInstance` serves HTML at `GET /{prefix}`)
- No change to `Register()`, `contentNegotiator`, `MCPMeta`, or `forge-mcp`

**Status resolution — 404 vs 406:**
404 chosen over 406. 404 signals "this URL has no browsable surface" — search engines
will not index it and browsers will not attempt to render it. Consistent with the
lifecycle enforcement precedent: Draft → 404 intentionally hides existence.

**Call-site syntax:**
```go
forge.NewModule((*HomePage)(nil),
    forge.At("/home-pages"),
    forge.Repo(repo),
    forge.MCP(forge.MCPWrite),
    forge.APIOnly(),
)
```

**Distinction from other routing variants:**
- `SingleInstance()`: one item, HTML at `GET /{prefix}`, `list_{type}s` MCP tool suppressed
- `Standalone()`:     items at `/{slug}`, HTML served
- `APIOnly()`:        no HTML anywhere; REST + MCP + CLI only, all MCP tools present

**Consequences:**
- No breaking changes. Additive option.
- `example_test.go`: `ExampleAPIOnly()` added (compile-verified).
- `integration_full_test.go`: G36 group (5 sub-tests).
- `docs/ARCHITECTURE.md` updated.

**Files changed:** `module.go`, `integration_full_test.go`, `example_test.go`,
`DECISIONS.md`, `decisions/recent.md`, `docs/ARCHITECTURE.md`, `CHANGELOG.md`,
`README.md`, `AGENTS.md`.

**Forge core → v1.24.0.**

---
