# Amendment A65 — ContextFunc: per-request extra data for module templates

## Context

Module show templates currently receive only the single content item being
rendered. There is no framework-supported way to pass additional data — for
example, a full list of DocPages for a sidebar — into a show template without
writing a custom handler that bypasses the module entirely.

`ContextFunc` closes this gap. It is a new module option that lets the developer
supply a function called at render time. The return value is stored in a new
`Extra any` field on `TemplateData[T]` and is available in the template as
`.Extra`. The template casts it to the concrete type it expects via a template
helper function.

This is the cleanest solution for the sidebar problem and for any future
use case where a template needs data beyond its own content item: related
posts, navigation trees, featured items, user preferences. It follows the
established Forge API patterns: declared once in `main.go`, zero impact on
sites that do not use it.

## What to implement

### 1. `TemplateData[T]` — add `Extra any` field (`templatedata.go`)

```go
type TemplateData[T any] struct {
    PageHead
    Content  T
    User     User
    Request  *http.Request
    SiteName string
    Extra    any   // populated by ContextFunc option; nil when not set
}
```

`NewTemplateData` signature unchanged — `Extra` is set by the render path,
not the constructor.

### 2. New option type — `ContextFunc` (`module.go` or new `options.go`)

```go
// ContextFunc returns an Option that registers a function called at render
// time for every list and show request. The return value is stored in
// TemplateData.Extra and is available in templates as .Extra.
//
// Use ContextFunc to supply sidebar data, navigation trees, related items,
// or any per-request data that the content item itself does not carry:
//
//  forge.ContextFunc(func(ctx forge.Context, item any) (any, error) {
//      all, _ := docRepo.FindAll(ctx, forge.ListOptions{})
//      return all, nil
//  })
//
// The item argument is the content item being rendered (T for show,
// []T for list). Cast it inside the function if needed.
// Errors from ContextFunc are logged and Extra is set to nil — they do
// not abort the render.
func ContextFunc(fn func(ctx Context, item any) (any, error)) Option
```

Internal option type:

```go
type contextFuncOption struct {
    fn func(Context, any) (any, error)
}
func (contextFuncOption) isOption() {}
```

### 3. `Module[T]` — store and call `ContextFunc` (`module.go`)

Add field:

```go
contextFunc func(Context, any) (any, error) // nil when not set
```

Wire in `NewModule` option parsing:

```go
case contextFuncOption:
    m.contextFunc = v.fn
```

Call in render path — add a helper:

```go
func (m *Module[T]) resolveExtra(ctx Context, item any) any {
    if m.contextFunc == nil {
        return nil
    }
    extra, err := m.contextFunc(ctx, item)
    if err != nil {
        // log and return nil — never abort the render
        return nil
    }
    return extra
}
```

Call `resolveExtra` in `renderListHTML` and `renderShowHTML` in `templates.go`:

```go
data.Extra = m.resolveExtra(ctx, items)  // list
data.Extra = m.resolveExtra(ctx, item)   // show
```

### 4. Template helper — `forge_extra` (`templatehelpers.go`)

Add a template function that casts `.Extra` to a concrete type for use in
templates. Because Go templates cannot type-assert, provide a helper that
returns the value as-is (templates use it via `{{.Extra}}` directly).

No new template function is strictly required — `.Extra` is accessible
directly in templates. If corepilot identifies a clean helper pattern for
typed access, propose it in the plan.

### 5. Developer call site (forge-site example)

```go
app.Content(forge.NewModule((*DocPage)(nil),
    forge.Repo(docRepo),
    forge.At("/docs"),
    forge.Templates("templates/docs"),
    forge.ContextFunc(func(ctx forge.Context, _ any) (any, error) {
        return docRepo.FindAll(ctx, forge.ListOptions{
            Status: []forge.Status{forge.Published},
        })
    }),
))
```

In `templates/docs/show.html`:

```html
{{- $nav := .Extra}}
{{template "sidebar" $nav}}
```

## Test plan

1. `TestContextFunc_list` — ContextFunc called for list render; Extra set correctly
2. `TestContextFunc_show` — ContextFunc called for show render; Extra set correctly
3. `TestContextFunc_nil` — no ContextFunc option; Extra is nil in TemplateData
4. `TestContextFunc_error` — ContextFunc returns error; render completes, Extra is nil

## Constraints

- Zero breaking changes. Sites without `ContextFunc` are unaffected.
- `ContextFunc` errors must never abort the render — log and set Extra to nil.
- `NewTemplateData` signature unchanged.
- No changes to `forgeHeadTmpl`.
- Update `DECISIONS.md` (append A65), `ARCHITECTURE.md`, `CHANGELOG.md`
  (`[1.5.0]` section), `README.md`.
- Version: `v1.5.0` (new exported symbol: `ContextFunc`; new field: `TemplateData.Extra`).
- Present the implementation plan for Peter's approval before writing any code.
