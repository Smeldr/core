# Task: README polish — value up, fixes throughout

## Why

The restructured README is close, but two things hold it back from being
effective for community engagement (HN, r/golang, pkg.go.dev):

1. The value proposition is buried at the bottom as an anonymous bullet list.
   Visitors should see what Forge gives them within the first screen — not after
   reading two code examples.

2. Several small but visible issues undermine credibility: a duplicate table row,
   a weak tagline, an unexplained Go pattern, a noop signal callback, and no
   pointer to the runnable examples.

## What

### 1. Tagline — make it specific

The current tagline "The Go web framework designed for how you actually think"
is generic. It could describe any framework. Replace it with something that
names Forge's actual differentiators: content lifecycle, zero dependencies,
AI-native. The tagline should tell a Go developer in one line why Forge is
different from Echo, Gin, or Chi.

### 2. Value section — move up and present properly

Move the "What you get" content to directly after the tagline and badge line,
before the comparison table, Installation, and code examples.

Do not present it as a flat anonymous bullet list. Present it as named features
with a one-line description each. Every item from the current list must be
preserved — none dropped. Current items:

- Full CRUD
- Role-based auth
- Draft-safe lifecycle
- Structured data (JSON-LD)
- Event-driven sitemap
- Content negotiation
- Open Graph
- Twitter Cards
- AI indexing (llms.txt + AIDoc)
- RSS feed
- Security headers
- Graceful shutdown
- Cookie compliance manifest
- Scheduled publishing
- MCP integration

Group them logically if that helps readability. Each feature should have a
description that says what it means in practice — not just the feature name.

### 3. Comparison table — remove duplicate row

The table currently has two rows that describe the same thing:
- "AI indexing (llms.txt + AIDoc)"
- "AI-native endpoints (llms.txt, AIDoc)"

Remove one. Keep the row that is most descriptive.

### 4. Minimal example — explain (*Post)(nil)

The pattern `forge.NewModule((*Post)(nil), ...)` passes a nil pointer for
type inference. This is valid Go but unfamiliar to many developers. Add a
short comment on that line explaining why it is written this way — something
like "nil pointer used for generic type inference — no allocation".

### 5. Showcase example — give AfterPublish a real body

The current AfterPublish callback returns nil with only a comment. Replace
the noop with a log.Printf that prints the slug, so the reader can see what
the signal actually does when triggered. Import "log" if not already imported.

### 6. Add pointer to runnable examples

After the showcase example, add a short paragraph or note pointing to the
example/ directory:

  Three complete runnable examples are in example/:
  - example/blog — a devlog with seeded posts, RSS, AI indexing, scheduled publishing
  - example/api  — a headless JSON API with role-based auth and redirect manifest
  - example/docs — a documentation site with sidebar navigation

Each runs with: cd example/blog && go run .

### 7. Amendment number

Read DECISIONS.md before assigning an amendment number.
The correct next number is the last row in the index table + 1.
Do not pre-assign — look it up.

## Definition of done

- Tagline is specific to Forge's differentiators
- Value section appears before the comparison table, named features with descriptions
- All 15 features from the current list are present — none dropped
- Comparison table has no duplicate rows
- (*Post)(nil) has an explanatory comment
- AfterPublish callback logs the slug
- Pointer to example/ is present after the showcase
- go build ./... passes clean (no Go changes other than the log.Printf addition)
- README stays under 180 lines
