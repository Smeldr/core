# forge-design: Technical Skill (Claude Code)

## Purpose

This skill enables Claude Code to generate `forge-pattern.md` files from Forge content
types, and to guide the AI-assisted design workflow from Go struct to rendered HTML.

Use this skill when a Forge developer wants to design the public HTML surface for a
content type — without writing HTML or CSS by hand.

---

## Workflow

```
Go struct -> forge-pattern.md -> design agent -> HTML + CSS -> html/template
```

1. Read the Go struct for the content type
2. Generate a forge-pattern.md (format below)
3. Give forge-pattern.md to a design agent (Claude Design, Stitch, etc.)
4. Review the output against the design intent
5. Translate static HTML to Go html/template syntax

---

## Rules

### R1 - Data sourcing
All field values in `content.json` come from the struct's exported fields and their
`json` tags. Do not hardcode values (author names, categories, etc.) unless explicitly
stated. The CMS note may contain non-field values (e.g. site name or wordmark text).

### R2 - Scope constraints are mandatory
Always include a "Scope - what must NOT be included" section. Design agents fill every
unspecified decision with their own choice. Name everything that should be absent:
navigation bar, footer, sidebar, hero image, JavaScript, CSS frameworks, dark mode, etc.

### R3 - Font field is required
Typography must include an explicit `Font:` line. Options: system font stack, Google
Fonts, self-hosted woff2. The choice is the developer's. If unspecified, the design
agent will choose and may add external dependencies the developer did not intend.

### R4 - Reference images sharpen output
Include 1-2 reference images (existing sites or blogs with a similar aesthetic) when
available. Typography, spacing, and visual weight are described far more precisely by
an image than by words. If no images are available, describe the aesthetic target more
precisely in the Mood section.

### R5 - Output is static HTML
Design agents produce static HTML files and a CSS file, not Go templates. Translation
to html/template syntax ({{ .Field }}, {{ range .Items }}) is a separate step done
after the design is approved. Do not ask the design agent to write Go templates.

### R6 - Request CSS custom properties
In the CMS note, specify: "Deliver CSS with plain CSS custom properties." This makes
the implementation step cleaner and the stylesheet maintainable by the developer.

### R7 - Field-to-design mapping is mandatory
Always include a field-to-design mapping table. It removes ambiguity about which
struct field appears where and in what visual role. Cover every page type in the table
(list page, show page, single-instance page, etc.).

### R8 - Include both list and show pages
For standard content types (list + show routes), always describe both pages under
"Pages to design". The mapping table must cover both page types.

---

## forge-pattern.md format

```markdown
# forge-pattern: [Site / Project name]

## CMS note

Built with Forge (smeldr.dev) - a Go content framework.
Pages are server-side rendered HTML templates.
Deliver clean, semantic HTML with plain CSS custom properties.
No JavaScript. No Tailwind. No frontend framework.

---

## Content type: [TypeName]

| Field | Type | Required | Example |
|-------|------|----------|---------|
| [field] | [string/markdown/datetime/etc] | [yes/no] | [example value] |

### content.json

​```json
[
  {
    "slug": "example-slug",
    "field": "value"
  }
]
​```

---

## Pages to design

**[Page name]** - `/route`
Description of what appears on this page and what data it shows.

---

## Design intent

### Mood
[1-2 sentences describing the target aesthetic and who the audience is.]

### Color
- Background: [name] ([hex])
- Body text: [name] ([hex])
- Accent / links: [name] ([hex])
- Muted (dates, meta): [name] ([hex])
- Inline code background: [name] ([hex])
- Code block background: [name] ([hex]), text: [name] ([hex])

### Typography
- Font: [system stack / Google Fonts family / self-hosted - specify explicitly]
- [Other typographic rules - all sans-serif, display size, etc.]
- Body: [line-height], max reading width [width]

### Scope - what must NOT be included
- No [element 1]
- No [element 2]
- [Add every absent element explicitly]

---

## Field-to-design mapping

| Field | [Page type 1] | [Page type 2] |
|-------|---------------|---------------|
| [field] | [visual role on page 1] | [visual role on page 2] |
```

---

## How to read a Forge Go struct

Given a struct like:

```go
type Post struct {
    smeldr.Node
    Title string `json:"title"   forge:"required,min=3,max=120"`
    Body  string `json:"body"    forge:"required,min=10"`
    Tags  []string `json:"tags" db:"-"`
}
```

- `smeldr.Node` provides: `id`, `slug`, `status`, `published_at`, `scheduled_at`,
  `created_at`, `updated_at` - all available in JSON output
- `json:"title"` -> field name in content.json is `title`
- `forge:"required"` -> mark Required: yes in the fields table
- `db:"-"` -> excluded from database, still available in JSON if present

Do not infer field types from the Go type alone - check the forge tag for `markdown`
to determine if a string field renders as markdown.
