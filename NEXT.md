# Amendment A63 — HeadAssets: app-level asset injection via forge:head

## Context

The `forge:head` template currently covers all semantic head content: title,
description, canonical, Open Graph, Twitter Card, JSON-LD, and robots. It does
not cover structural assets: favicons, stylesheets, fonts, and scripts.

Today a Forge site must declare these manually in every custom template (e.g.
the home page). This blocks the Forge Cloud provisioning vision: a provisioned
site should be describable entirely in `main.go` with no template editing.

`HeadAssets` is the mechanism that closes this gap. It follows the established
pattern of `OGDefaults` and `AppSchema`: declared once in `main.go`, injected
automatically into every template via `forge:head`.

## What to implement

Add a `HeadAssets` struct to `head.go` (or a new `assets.go`):

```go
type HeadAssets struct {
    Preconnect  []string     // href values — emitted as <link rel="preconnect">
    Stylesheets []string     // href values — emitted as <link rel="stylesheet">
    Favicons    []FaviconLink
    Scripts     []ScriptTag
}

type FaviconLink struct {
    Rel   string // e.g. "icon", "apple-touch-icon"
    Type  string // e.g. "image/png", "image/x-icon" — omitted if empty
    Sizes string // e.g. "32x32", "180x180" — omitted if empty
    Href  string
}

type ScriptTag struct {
    Src   string // external script — omitted if empty
    Body  string // inline script body — used when Src is empty
    Async bool
    Defer bool
}
```

Wire `HeadAssets` into the existing SEO configuration path:

- Add `HeadAssets *HeadAssets` to `OGDefaults` (it belongs to the same
  app-level declaration family), or introduce it as a standalone field on
  `seoState` — corepilot should choose the cleanest fit given the existing
  structure.
- Propagate it through `setSEODefaults` → `Module[T]` → `TemplateData`
  (add a `HeadAssets *HeadAssets` field to `TemplateData`).
- Extend `forgeHeadTmpl` in `templates.go` to emit the assets in correct
  order: preconnect → stylesheets → favicons → scripts.
- Add `App.HeadAssets(a *HeadAssets) *App` (or wire it through `App.SEO`)
  so the developer declares it once in `main.go`.

## Rendering order in forge:head

```
<title>...</title>
<!-- existing: description, canonical, OG, Twitter, JSON-LD, robots -->
<!-- new: -->
<link rel="preconnect" ...>   (for each Preconnect)
<link rel="stylesheet" ...>   (for each Stylesheet)
<link rel="icon" ...>         (for each Favicon)
<script ...>                  (for each Script)
```

## Constraints

- Zero breaking changes. Sites that do not call the new API are unaffected.
- `HeadAssets` nil check must be safe — `forge:head` emits nothing for a nil value.
- No new third-party dependencies.
- Tests: at least one test covering the template output for a populated
  `HeadAssets` value.
- Update `ARCHITECTURE.md`, `DECISIONS.md` (append Amendment A63), and
  `CHANGELOG.md`.
- Present the implementation plan for Peter's approval before writing any code.
