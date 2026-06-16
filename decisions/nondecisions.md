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
