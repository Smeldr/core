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
