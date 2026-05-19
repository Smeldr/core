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