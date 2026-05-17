# Forge Decisions — Content API

Archived from decisions/recent.md on 2026-05-17.
Read on demand. See DECISIONS.md for the full index.

---

## Decision 27 � Field format semantics: `forge_format` and `forge_description`

**Status:** Locked
**Date:** 2026-04-07

**Decision:** Forge introduces two optional struct tags � `forge_format` and
`forge_description` � that declare the expected content format and authoring
guidance for string fields. Both are surfaced in `MCPField` and in forge-mcp
tool descriptions to give AI agents explicit, actionable context when authoring
content. Neither tag triggers validation � they are semantic hints only.

### Struct tags

```go
Body  string `forge:"required" forge_format:"markdown" forge_description:"Write content in Markdown. Supports headings, lists, and code blocks."`
Embed string `forge_format:"html" forge_description:"Raw HTML only. Use for iframes and third-party embeds. Must be trusted content."`
```

### Supported `forge_format` values

| Value      | Meaning |
|------------|---------|
| `markdown` | CommonMark/GFM markdown � also covers plain text |
| `html`     | Trusted raw HTML � caller is responsible for sanitisation |

Fields without a `forge_format` tag have `Format = ""` � no hint emitted.

### `forge_description`

Free text written by the developer. Shown in forge-mcp tool descriptions when
present. No fixed vocabulary � the developer writes what the AI agent needs to
know to author the field correctly.

When both tags are present, forge-mcp uses `forge_description` as the primary
description and appends the format as a parenthetical:
`"Write content in Markdown. Supports headings, lists, and code blocks. (markdown)"`.

When only `forge_format` is present, forge-mcp emits a short derived hint:
`"(markdown)"` or `"(html)"`.

When neither is present, the field description is unchanged from current behaviour.

### MCPField

```go
type MCPField struct {
    Name        string
    JSONName    string
    Type        string
    Format      string // "" when no forge_format tag present
    Description string // "" when no forge_description tag present
    Required    bool
    MinLength   int
    MaxLength   int
    Enum        []string
}
```

### What this is not

- No validation � format and description are hints only
- No breaking change � all new fields on `MCPField` are additive
- No impact on HTML rendering, template helpers, or non-MCP paths

**Rationale:**
`MCPSchema` exposes field types (`string`, `number`, `array`) but carries no
semantic or authoring information. A content type with multiple string fields
of fundamentally different kinds � markdown body, trusted HTML embed � gives
an AI agent no signal to distinguish them or author them correctly. The gap
was identified with `DocPage.Body` (markdown) and `DocPage.Embed` (trusted
HTML). The two tags close the gap at different levels: `forge_format` provides
machine-readable semantics; `forge_description` provides human- and AI-readable
authoring guidance.

**Rejected alternatives:**
- Convention-based field naming (`BodyMarkdown`, `EmbedHTML`): Fragile, not
  machine-readable, constrains naming.
- Validation based on format: Out of scope � semantics alone are sufficient
  for agent guidance.
- Additional format values from the start (`url`, `slug`, `plaintext`): Kept
  minimal � extended when concrete need arises.
- `forge_description` as a separate decision: Both tags solve the same AI-DX
  problem and belong together.

**Consequences:**
- `mcp.go`: `MCPField` gains `Format string` and `Description string`
- `module.go`: `MCPSchema()` reads `forge_format` and `forge_description`
  struct tags and populates the new fields
- `forge-mcp/mcp.go`: `fieldDescription` helper; `inputSchema` and
  `inputSchemaUpdate` emit `"description"` key with priority Logic
- forge core bumps to `v1.9.0` (new fields on exported struct `MCPField`)
- forge-mcp bumps to `v1.3.0` (tool description output changes)


---

## Amendment A67 � `forge_html`: trusted raw HTML passthrough

**Status:** Agreed
**Date:** 2026-04-05

**Context:**
Go's `html/template` escapes all string output by default. There is no way for a
module template to render pre-rendered HTML (e.g. a video embed iframe, a
third-party widget) without a trusted passthrough function. The gap was identified
during planning of the forge-cms.dev demo page, which needs to embed an iframe
alongside Markdown content (`forge_markdown` handles the Markdown; `forge_html`
handles the iframe).

**What changed:**
- `templatehelpers.go`: `forgeHTML(s string) template.HTML` � one-line function
  returning `template.HTML(s)`; registered as `"forge_html"` in `TemplateFuncMap`;
  godoc warns that the caller is responsible for trust.
- `templatehelpers_test.go`: `TestForgeHTML` (3 sub-tests: passthrough, empty,
  not_escaped); `TestTemplateFuncMap_keys` expected count updated from 8 to 9.

**Consequences:**
- `TemplateFuncMap` grows from 8 to 9 entries.
- No exported Go symbol is added � `forgeHTML` is package-internal; only the map
  key `"forge_html"` is visible to templates.
- No interface, file, or behaviour change beyond the new function.
- Root package bumps to `v1.7.0`.

---

## Amendment A74 � Rename FaviconLink ? HeadLink, HeadAssets.Favicons ? HeadAssets.Links

**Status:** Agreed
**Date:** 2026-04-18
**Files:** `head.go`, `templates.go`, `head_test.go`, `example_test.go`, `REFERENCE.md`

### Problem

`FaviconLink` and `HeadAssets.Favicons` implied the field only accepted favicon
and touch-icon elements. In practice, developers and AI agents legitimately place
any `<link>` element there � `rel="me"` (profile verification), `rel="manifest"`,
`rel="alternate"`, `rel="canonical"` � and the name gave no indication that these
were valid uses. A developer looking for where to add a `rel="me"` link would not
find it by scanning the type name or field name `Favicons`.

### Decision

Rename:
- `FaviconLink` ? `HeadLink`
- `HeadAssets.Favicons []FaviconLink` ? `HeadAssets.Links []HeadLink`

The four struct fields (`Rel`, `Href`, `Type`, `Sizes`) and the template rendering
path are unchanged. The renaming is purely semantic � the generated HTML is identical.

### Rationale

`HeadLink` is the correct name: it represents any HTML `<link>` element. The struct
already had no favicon-specific logic � it was a generic `<link>` builder from day one.
`Links` at the call site is immediately readable:

```go
app.SEO(&forge.HeadAssets{
    Links: []forge.HeadLink{
        {Rel: "icon", Type: "image/png", Sizes: "32x32", Href: "/favicon-32.png"},
        {Rel: "me", Href: "https://mastodon.social/@you"},
        {Rel: "manifest", Href: "/site.webmanifest"},
    },
})
```

An AI agent or developer scanning the struct immediately understands the field's scope.

### Consequences

1. **Breaking change** � all callers that reference `FaviconLink` or `.Favicons` must
   update. The struct's fields and rendering behaviour are unchanged.
2. **Version bump** � ships as `v1.13.0`.
3. `REFERENCE.md` updated: field name in the `HeadAssets` example, comment in the
   `TemplateData` table.
4. `ARCHITECTURE.md` updated: A63 row and `head.go` exports list updated to `HeadLink`.
5. `example_test.go` updated: `ExampleHeadAssets` uses `Links: []HeadLink{�}`.

---

## Amendment A75 � `markdown.go`: HTML passthrough in `renderMarkdown`

**Status:** Agreed � 2026-04-22
**Shipped in:** v1.13.1

### Problem

Content authors on self-hosted Forge sites need to mix Markdown prose with raw
HTML blocks (e.g. `<div class="pull-quote">`, iframes, custom components). The
existing `renderMarkdown` HTML-escaped every line, making such blocks impossible
to use inside body content fields.

### Decision

Lines whose trimmed form starts with `<` are emitted verbatim � without
HTML-escaping � by `renderMarkdown`. All other lines continue through the
existing pipeline (HTML escape ? inline markdown ? tag wrap).

### Rationale

Forge is self-hosted. Content authors have the same trust level as the role
system that governs MCP write operations � they are the site owner or explicitly
granted `Author`/`Editor`/`Admin` role. No anonymous or untrusted input reaches
`renderMarkdown` directly. Treating these users the same as anonymous web users
would prevent legitimate authoring workflows (embedded videos, styled callouts,
third-party widgets) with no security benefit.

The `<` prefix heuristic is intentionally simple: it catches both opening tags
(`<div>`, `<iframe>`) and closing tags (`</div>`). Inline `<code>` escaping is
unaffected because inline code is wrapped in backticks and processed after the
HTML-escape step.

### Consequences

1. HTML blocks in trusted body content now render correctly.
2. No change to the public API � `renderMarkdown` signature is unchanged.
3. Version bump: `v1.13.1` (patch � no API change, behaviour fix for trusted content).
4. `CHANGELOG.md` entry added under `[1.13.1]`.

---

## Amendment A77

**Date:** 2026-05-02
**Status:** Agreed
**Files:** `head.go`, `module.go`, `templates.go`

### Problem

`renderListHTML` in `templates.go` always passes `Head{}` (zero) to
`NewTemplateData`. It never calls `resolveHead` or any list-specific head
function. Every module list page renders `<title></title>` with no meta
description, regardless of what the developer configures.

Root cause: `renderListHTML` simply had no mechanism to accept a list-level
head override. `HeadFunc[T]` only applies to the show page (single item);
there was no parallel option for the list page.

Confirmed via `curl http://localhost:8080/devlog | grep title` returning
`<title></title>`.

### Decision

Add `ListHeadFunc[T any](fn func(Context, []T) Head) Option` as a new exported
option in `head.go`. It follows the same pattern as `HeadFunc`:

- `listHeadFuncOption[T any]` — unexported generic type carrying the function.
- `listHeadFunc any` field on `Module[T]` — stores the option value at module
  construction time.
- `NewModule` options loop — type-asserts `listHeadFuncOption[T]` directly
  (same approach as `headFuncOption[T]`).
- `renderListHTML` — after building `data`, resolves the list head:

```go
if m.listHeadFunc != nil {
    if fn, ok := m.listHeadFunc.(func(Context, []T) Head); ok {
        head := fn(ctx, items)
        head = mergeOGDefaults(head, m.ogDefaults)
        data.Head = head
    }
}
```

`mergeOGDefaults` is used so that `Config.OGDefaults` (site-level fallbacks)
are still applied to the list head, consistent with show-page behaviour.

### Consequences

1. No breaking change — existing modules without `ListHeadFunc` behave
   identically (zero `Head{}`, empty `<title>`).
2. `ListHeadFunc` and `HeadFunc` are independent; both can be set on the same
   module without interference.
3. Version bump: `v1.14.1` (patch — bug fix + new exported symbol, no breaking
   change).

---
