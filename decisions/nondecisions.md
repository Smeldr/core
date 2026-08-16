# Forge — Non-Decisions

All entries here are formal records of what Forge explicitly decided NOT to do,
and why. These do not count toward the `recent.md` rolling window.

New Non-Decisions go here directly — not through `recent.md`.

---
## Non-Decision A96 — Sitemap ping (T39)

**Date:** 2026-05-16
**Status:** Agreed
**Level:** 1 (docs-only — no code change)

### What was considered

An opt-in `SitemapPingURL string` field on `Config` that fires an HTTP GET
after every `AfterPublish` signal to notify search engines of new content.

### Decision

Forge will not provide sitemap ping.

### Rationale

Google deprecated their ping endpoint in June 2023. The only remaining
protocol with real adoption is IndexNow (Bing, Yandex), which requires an
API key and a verification file hosted on the site — this is
application-level setup, not framework responsibility. Forge must not own
deployment-specific integrations.

### Developer pattern

Register an `AfterPublish` signal handler in `main.go` that calls your
preferred indexing API. `SignalEvent` carries the slug and URL:

```go
app.OnSignal(forge.AfterPublish, func(ctx context.Context, ev forge.SignalEvent) error {
    // ev.URL is the canonical URL of the published content item.
    // Call your indexing API here (IndexNow, etc.)
    return nil
})
```

See REFERENCE.md — "Search engine indexing" for a full example.

### Consequences

No exported Go symbols added, removed, or renamed.
No build, vet, or test changes required.

---

## Non-Decision: Dynamic content slug immutability (T104)

**Decision not taken:** Slug auto-update when a content item's title field is edited.

### What was considered

When `UpdateFields` is called with a new title value, should `DynamicTypeRepo` regenerate
the slug to reflect the new title? Auto-updating slugs would keep URLs "tidy" for items
that are still in draft. Published items would need to stay stable (broken links).

### Why not

URL stability is a first-class SEO and operational requirement. Any slug-update logic
requires tracking "was ever published", redirect creation, and canonical URL management —
all of which belong in operator code or a future T-series task, not the core data layer.
The core's job is to be a reliable data store. Slug mutation without an explicit operator
action violates the principle of least surprise.

### Current behaviour

Slugs are set once at `CreateDraft` time from the title-role field (or `"item"` fallback).
`UpdateFields` never touches the slug. Operators who need to change a slug must do so
directly via `UpdateFields({Slug: "new-slug"})` — the slug field is not special-cased
and is writable like any other field.

### Consequences

No exported Go symbols added, removed, or renamed.
No migration or test changes required.

---

## Non-Decision A156a — HTML rendering for dynamic content types

**Date:** 2026-06-17
**Status:** Agreed
**Level:** 1 (docs-only — no code change)

### What was considered

Building HTML surface into core for runtime-defined dynamic content types — generic
schema-aware renderer, DB-stored templates (text/template), universal list/show template.

### Decision

Smeldr will not provide HTML rendering for dynamic content types. Dynamic types serve
JSON (headless) by default.

### Rationale

Core is a data and lifecycle layer. Rendering is a presentation concern. Cloud presentation
layer handles templates, styling, and operator-uploaded views. Embedding rendering in core
couples the data layer to presentation decisions that vary per cloud operator.

### Developer pattern

Use the JSON API (`GET /{url_prefix}/{slug}`). For HTML in a standalone site, implement
templates in your own application.

### Consequences

No exported Go symbols added, removed, or renamed.
No build, vet, or test changes required.

---

## Non-Decision A156b — Block rendering as cloud concern

**Date:** 2026-06-17
**Status:** Agreed
**Level:** 1 (docs-only — no code change)

### What was considered

Shipping block rendering templates (hero, gallery, content grid, etc.) as part of core
for cloud operators.

### Decision

Core retains block data model and `app.ServeBlocks()` for developer sites. Templates that
render blocks to HTML in a cloud context belong to the cloud presentation layer.

### Rationale

Same core/cloud separation. Which template renders a hero block is a product decision,
not a framework decision.

### Developer pattern

For standalone sites, use `app.ServeBlocks()` (existing).

### Consequences

No exported Go symbols added, removed, or renamed.
No build, vet, or test changes required.

---

## Non-Decision A172 — BSL and SSPL declined

**Date:** 2026-06-28
**Status:** Declined

### What was considered

Relicensing Smeldr Core from AGPL to Business Source License (BSL, as used by
MariaDB/Hashicorp) or Server Side Public License (SSPL, as used by MongoDB and Redis).
Both restrict the right to run the software in production without a commercial license,
which proponents argue gives stronger commercial protection than AGPL.

### Decision

Declined. Smeldr Core stays AGPL permanently.

### Rationale

Article XII of the Smeldr Constitution states that an organization owns its operational
model. Ownership is the starting condition, not a grant from the platform. BSL and SSPL
make running the software in production conditional on commercial terms — this directly
contradicts Article XII.

Other projects (Elastic, MongoDB, Redis) started permissive and relicensed under commercial
pressure, at significant cost to community trust. Smeldr started protective: AGPL from day one.
The relicense escape is not needed and would contradict the constitutional commitment.

AGPL is what makes a fully open, fully capable core safe to give away: no one can offer
Smeldr as a service without open-sourcing their stack or holding a commercial license.
That protection is sufficient and constitutionally sound.

See also: `constitution/cloud-strategy.md` — "The commercial boundary".

### Consequences

No exported Go symbols added, removed, or renamed.
No build, vet, or test changes required.

---

## Non-Decision — Unified RoleStore/pilotorg.Role model (T151)

**Date:** 2026-08-16
**Status:** Declined — deliberately separate axes, no reconciliation needed
**Level:** 1 (docs-only — no code change)

### What was considered

Whether core's `RoleStore` (`Guest < Author < Editor < Admin`, a
per-instance content-permission hierarchy) and Cloud's `pilotorg.Role`
(`Owner > Admin > Member`, a per-org membership/billing hierarchy) should
be reconciled into one shared model. Open since 2026-07-18, deliberately
left unreconciled at the time; cited as actively blocking T226 (cloud
read-route auth) a month later.

### Decision

Smeldr will not unify `RoleStore` and `pilotorg.Role` into a single model.

### Rationale

The two answer genuinely different questions. `RoleStore` governs what an
actor can do to a single Smeldr instance's own content — read a draft,
publish, configure the app. `pilotorg.Role` governs what an actor can do
to the *organization*, across however many instances it owns — invite a
member, manage billing, take an account-level irreversible action.
Neither question is a special case of the other; forcing them into one
shared vocabulary would lose the distinction that makes either one
meaningful. Same shape as D56's `Standing`/`Status` separation: two axes
answering two different questions stay two axes.

T226's own blocker — auth on Cloud's `/cloud/read/trace/*` and
`/cloud/read/pulse/*` routes — never actually needed a unified model.
Both routes read a single instance's own `db`/`rs` handles directly (no
`orgID` parameter), so the real question is "is this caller a member of
the org that owns this instance," which `pilotorg.Role` already answers
and already checks elsewhere in the same repo
(`internal/authapi/routes.go:186,200`). T226 is unblocked, retargeted to
its own real fix (wire that existing check onto the two routes directly)
— no core change, no reconciliation required.

### Developer pattern

Checking "can this actor read/write this instance's own content" →
`RoleStore`. Checking "can this actor act on this organization or its
instances" → `pilotorg.Role` (Cloud only). Do not translate one into the
other — check whichever question the call site is actually asking.

### Consequences

No exported Go symbols added, removed, or renamed.
No build, vet, or test changes required.

---
