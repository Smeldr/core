# Amendment A64 — PageHead: embeddable head struct for custom handlers

## Context

`forge:head` currently works in module templates (list.html, show.html) because
they receive `TemplateData[T]` which carries `.Head`, `.OGDefaults`, `.AppSchema`,
and `.HeadAssets`. Custom handlers — like a home page — define their own data
structs and cannot use `{{template "forge:head" .}}` because those fields are
absent.

The idiomatic Go solution is embedding. Forge exports a `PageHead` struct
containing exactly the fields `forge:head` reads. Any custom handler struct
embeds `forge.PageHead` and gets `forge:head` support immediately, with no
constraints on what other fields the struct carries.

This is the correct architectural answer to the scalability question: a home
page with posts, products, and testimonials simply embeds `forge.PageHead`
alongside its own fields — the framework owns nothing beyond `PageHead`.

## What to implement

Add `PageHead` to `templatedata.go` (or `head.go` — corepilot chooses the
cleanest fit):

```go
// PageHead holds the framework-owned fields that forge:head reads.
// Embed PageHead in any custom handler data struct to enable
// {{template "forge:head" .}} without using TemplateData[T].
//
// Example:
//
//  type homeData struct {
//      forge.PageHead
//      Posts []*Post
//  }
type PageHead struct {
    Head       Head
    OGDefaults *OGDefaults
    AppSchema  template.HTML
    HeadAssets *HeadAssets
}
```

Update `TemplateData[T]` to embed `PageHead` instead of declaring the four
fields individually. The exported field names must remain identical so existing
templates are unaffected — embedding satisfies this automatically.

Before (current):
```go
type TemplateData[T any] struct {
    Head       Head
    OGDefaults *OGDefaults
    AppSchema  template.HTML
    HeadAssets *HeadAssets
    Content    T
    Request    *http.Request
    SiteName   string
}
```

After:
```go
type TemplateData[T any] struct {
    PageHead             // Head, OGDefaults, AppSchema, HeadAssets promoted
    Content  T
    Request  *http.Request
    SiteName string
}
```

The render path in `templates.go` must set `data.Head`, `data.OGDefaults`,
`data.AppSchema`, and `data.HeadAssets` via the promoted fields — no call-site
change is needed if promotion works correctly, but corepilot must verify.

## Constraints

- Zero breaking changes. Existing templates call `.Head`, `.OGDefaults` etc.
  directly — promotion preserves these names exactly.
- `NewTemplateData` constructor signature unchanged if possible.
- No changes to `forgeHeadTmpl` — it already reads the correct field names.
- Tests: at least one test confirming that a struct embedding `PageHead` renders
  `forge:head` correctly (field promotion works as expected in the template engine).
- Update `DECISIONS.md` (append A64), `ARCHITECTURE.md`, `CHANGELOG.md`
  (`[1.4.0]` section), `README.md`.
- Version: `v1.4.0` (new exported symbol: `PageHead`).
- Present the implementation plan for Peter's approval before writing any code.
