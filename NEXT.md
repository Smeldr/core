# Next — Decision 27: Field format semantics in MCPSchema

## What and why

`MCPSchema` exposes field types (`string`, `number`, `array`) but carries no
semantic or authoring information. A content type with multiple string fields
of fundamentally different kinds — markdown body, trusted HTML embed — gives
an AI agent no signal to distinguish them or author them correctly.

The gap was identified with `DocPage`, which has both `Body` (markdown) and
`Embed` (trusted HTML). An agent seeing two `string` fields cannot infer the
correct authoring convention for either.

Two new optional struct tags close this gap:

- `forge_format` — machine-readable format hint (`markdown` or `html`)
- `forge_description` — free text authoring guidance shown in tool descriptions

Both are hints only. Forge performs no validation based on either tag.

## Struct tag usage

```go
Body  string `forge:"required" forge_format:"markdown" forge_description:"Write content in Markdown. Supports headings, lists, and code blocks."`
Embed string `forge_format:"html" forge_description:"Raw HTML only. Use for iframes and third-party embeds. Must be trusted content."`
```

## Supported forge_format values

| Value      | Meaning |
|------------|---------|
| `markdown` | CommonMark/GFM markdown — also covers plain text |
| `html`     | Trusted raw HTML — caller is responsible for sanitisation |

Fields without a `forge_format` tag have `Format = ""` — no hint emitted.

## MCPField changes

Add `Format string` and `Description string` to `MCPField`:

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

`MCPSchema()` in `module.go` reads both struct tags and populates the new fields.

## forge-mcp tool description output

When building tool input schemas, apply this priority:

- Both `forge_description` and `forge_format` present:
  use `forge_description` as the description and append format as a
  parenthetical — e.g. `"Write content in Markdown. Supports headings,
  lists, and code blocks. (markdown)"`
- Only `forge_format` present:
  emit a short derived hint — e.g. `"(markdown)"` or `"(html)"`
- Neither present:
  field description unchanged from current behaviour

## What does not change

- No validation — format and description are hints only
- No breaking change — new `MCPField` fields are additive; fields without
  the tags are unaffected
- No impact on HTML rendering, template helpers, or non-MCP paths

## Version

- forge core: `v1.9.0` (new fields on exported struct `MCPField`)
- forge-mcp: `v1.3.0` (tool description output changes)

## Decision record

Write Decision 27 to `decisions/phase2.md` and add the index row to
`DECISIONS.md`. Use the format established by Decision 25 and 26.

Decision 27 body:

---

## Decision 27 — Field format semantics: `forge_format` and `forge_description`

**Status:** Locked
**Date:** 2026-04-06

**Decision:** Forge introduces two optional struct tags — `forge_format` and
`forge_description` — that declare the expected content format and authoring
guidance for string fields. Both are surfaced in `MCPField` and in forge-mcp
tool descriptions to give AI agents explicit, actionable context when authoring
content. Neither tag triggers validation — they are semantic hints only.

### Struct tags

```go
Body  string `forge:"required" forge_format:"markdown" forge_description:"Write content in Markdown. Supports headings, lists, and code blocks."`
Embed string `forge_format:"html" forge_description:"Raw HTML only. Use for iframes and third-party embeds. Must be trusted content."`
```

### Supported `forge_format` values

| Value      | Meaning |
|------------|---------|
| `markdown` | CommonMark/GFM markdown — also covers plain text |
| `html`     | Trusted raw HTML — caller is responsible for sanitisation |

Fields without a `forge_format` tag have `Format = ""` — no hint emitted.

### `forge_description`

Free text written by the developer. Shown in forge-mcp tool descriptions when
present. No fixed vocabulary — the developer writes what the AI agent needs to
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

- No validation — format and description are hints only
- No breaking change — all new fields on `MCPField` are additive
- No impact on HTML rendering, template helpers, or non-MCP paths

**Rationale:**
`MCPSchema` exposes field types (`string`, `number`, `array`) but carries no
semantic or authoring information. A content type with multiple string fields
of fundamentally different kinds — markdown body, trusted HTML embed — gives
an AI agent no signal to distinguish them or author them correctly. The gap
was identified with `DocPage.Body` (markdown) and `DocPage.Embed` (trusted
HTML). The two tags close the gap at different levels: `forge_format` provides
machine-readable semantics; `forge_description` provides human- and AI-readable
authoring guidance.

**Rejected alternatives:**
- Convention-based field naming (`BodyMarkdown`, `EmbedHTML`): Fragile, not
  machine-readable, constrains naming.
- Validation based on format: Out of scope — semantics alone are sufficient
  for agent guidance.
- Additional format values from the start (`url`, `slug`, `plaintext`): Kept
  minimal — extended when concrete need arises.
- `forge_description` as a separate decision: Both tags solve the same AI-DX
  problem and belong together.

**Consequences:**
- `mcp.go`: `MCPField` gains `Format string` and `Description string`
- `module.go`: `MCPSchema()` reads `forge_format` and `forge_description`
  struct tags and populates the new fields
- `forge-mcp/tool.go`: field descriptions in tool schemas include format
  and description when present
- forge core bumps to `v1.9.0` (new fields on exported struct `MCPField`)
- forge-mcp bumps to `v1.3.0` (tool description output changes)

---

## After implementation

- Delete this file
- Update `context/corepilot.md` in forge-cms/forge-architect with version,
  amendment number, and files changed
