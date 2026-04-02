# A61: OGDefaults and AppSchema

## Background
Forge sites currently have no way to set app-level Open Graph fallbacks or
app-level structured data. Each content type must handle this individually,
leading to repetition across all Forge sites.

## Why
- `OGDefaults` provides a single place to declare fallback values for
  `og:image`, `twitter:site`, and `twitter:creator` at the app level.
  These are used by `forge:head` when a content item does not supply its own.
- `AppSchema` provides app-level JSON-LD structured data (e.g. Organization)
  registered once via `app.SEO()` and emitted in every page's `<head>`.

Both reduce boilerplate in every Forge site and align with the existing
`app.SEO()` extension point already present in `forge.go`.

## Constraints
- Both are `SEOOption` implementations — they apply via `app.SEO()`.
- `OGDefaults` affects `forge:head` template output — the partial in
  `templates.go` must read fallback values when `Head.Image` is empty.
- `AppSchema` emits a `<script type="application/ld+json">` block — decide
  whether this belongs in `forge:head` or as a separate partial.
- Zero new exported types beyond `OGDefaults` and `AppSchema` structs unless
  genuinely required.
- All changes must be reflected in `ARCHITECTURE.md` and `DECISIONS.md` (A61).
- `example_test.go` and `README.md` must be updated if any documented example
  is affected.

## Your task
1. Analyse the existing `seoState`, `SEOOption`, `forge:head` partial, and
   `social.go` before proposing anything.
2. Plan your approach — types, signatures, template changes — and present it
   for review before writing any code.
3. Wait for explicit approval before implementing.
