# Next task for corepilot

## Enhancement: inline link support in mdInline

### What

`mdInline` in `markdown.go` does not support `[text](url)` syntax.
Because HTML-escaping runs before inline pattern matching, link syntax
is passed through as plain escaped text. Add inline link support to `mdInline`.

### Why

Markdown body content stored via MCP uses `[text](url)` links. These render
as literal text on the page, not as anchors. Discovered when publishing
Story content with footer links via MCP.

### Where to fix

`markdown.go` — add `mdApplyLinks` and call it from `mdInline`.

Call order in `mdInline` must be:
1. `template.HTMLEscapeString` (existing — must stay first)
2. `mdApplyLinks` (new — must run before bold/code)
3. `mdApplyBold` (existing)
4. `mdApplyCode` (existing)

### mdApplyLinks specification

- Pattern: `[text](url)` → `<a href="url">text</a>`
- URL allow-list: only `http://`, `https://`, and `/` (relative paths).
  Any other scheme — reject silently and emit the original `[text](url)` text unchanged.
- The `text` part is already HTML-escaped at this point — do not double-escape.
- The `url` part must be HTML-attribute-escaped before insertion into `href`.
- Match the first `](` and next `)` only — nested brackets out of scope.

### Security note

URL validation is non-negotiable. Use an allow-list (`http://`, `https://`, `/`),
not a denylist. `javascript:`, `data:`, and all other schemes must never
reach the `href` attribute.

### Scope

- `markdown.go` — add `mdApplyLinks`, update `mdInline`
- `markdown_test.go` — add tests covering:
  - Basic `[text](https://example.com)` → `<a href="...">text</a>`
  - Relative `[docs](/docs)` → `<a href="/docs">docs</a>`
  - Rejected scheme `[x](javascript:alert(1))` → literal text unchanged
  - Link next to other inline patterns (bold, code) — reasonable behaviour documented
- `CHANGELOG.md` — new entry
- Version bump: patch (forge core only, forge-mcp not affected)

### Expected outcome

`[Read the docs](/docs) | [See it in action](/docs/demo)` renders as two
clickable anchor tags in Story and Post body content.
