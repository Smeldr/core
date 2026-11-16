# smeldr-pattern: Smeldr Devlog

## CMS note

Built with Smeldr (smeldr.dev) — a Go content framework.
Pages are server-side rendered HTML templates.
Deliver clean, semantic HTML with plain CSS custom properties.
No JavaScript. No Tailwind. No frontend framework.

---

## Content type: Post

The keys are the blog's JSON keys: `Post` has no json tags, so its fields
(and those of the embedded `smeldr.Node`) are written under their Go names.

| Field       | Type     | Required | Example                               |
|-------------|----------|----------|---------------------------------------|
| Title       | string   | yes      | "Why the Smeldr Package Imports Only the Standard Library" |
| Body        | markdown | yes      | Full article (paragraphs, headings, code blocks) |
| PublishedAt | datetime | yes      | "2026-02-28T10:00:00Z"                |
| Slug        | string   | yes      | "why-the-smeldr-package-imports-only-the-standard-library" |

### content.json

```json
[
  {
    "Slug": "why-the-smeldr-package-imports-only-the-standard-library",
    "Title": "Why the Smeldr Package Imports Only the Standard Library",
    "PublishedAt": "2026-02-28T10:00:00Z",
    "Body": "When we started building Smeldr, the first architectural decision we locked was simple: the smeldr package imports only the Go standard library.\n\nGo's standard library is remarkably complete. net/http gives you a production-ready HTTP server. html/template handles safe rendering. database/sql abstracts every relational database.\n\nThe module's go.mod does list one third-party module, modernc.org/sqlite, with the modules it brings, because the package's own tests use SQLite. A program that imports smeldr.dev/core does not compile it in."
  },
  {
    "Slug": "scheduled-publishing-without-a-job-queue",
    "Title": "How We Handle Scheduled Publishing Without a Job Queue",
    "PublishedAt": "2026-03-14T10:00:00Z",
    "Body": "Most CMS platforms solve scheduled publishing with a cron job or a dedicated job queue. These work, but they introduce infrastructure you have to operate and keep in sync with your application state.\n\nSmeldr uses an adaptive in-process ticker. When the scheduler starts it looks at the nearest ScheduledAt timestamp and sets its tick interval to half the remaining time, down to a minimum of one second."
  },
  {
    "Slug": "content-lifecycle-in-smeldr",
    "Title": "Content Lifecycle in Smeldr: Draft, Scheduled, Published, Archived",
    "PublishedAt": "2026-04-10T10:00:00Z",
    "Body": "Every piece of content in Smeldr moves through a defined lifecycle: Draft to Scheduled to Published to Archived. Smeldr enforces this at the framework level, not the application level.\n\nThis matters because lifecycle enforcement is the kind of logic that is trivial to implement once, catastrophic to forget."
  }
]
```

---

## Pages to design

**Post list** — `/posts`
All published posts, newest first.
Each entry: title as link, date, first 2-3 lines of body as plain-text excerpt.

**Post show** — `/posts/{slug}`
Single post: title, date, full body rendered from markdown
(paragraphs, h2/h3 headings, inline code, fenced code blocks).

---

## Design intent

### Mood
Technical devlog for a Go framework. Clean, precise, developer-focused.
Minimal decoration — typography does the work.
Should feel like a confident engineering blog, not a marketing site.

### Color
- Background: white (#ffffff)
- Body text: near-black (#111827)
- Accent / links: accessible blue (#2563eb)
- Muted (dates, meta): medium grey (#6b7280)
- Inline code background: light grey (#f3f4f6)
- Code block background: dark (#1e293b), text: light (#e2e8f0)

### Typography
- Font: IBM Plex Sans (body), IBM Plex Mono (code) — loaded via Google Fonts
- All sans-serif — no serif fonts anywhere, not even for display headings
- Body: 1.65 line-height, max reading width 680px
- Dates: smaller, muted

### Scope — what must NOT be included
- No navigation bar
- No footer
- No sidebar
- No hero image or banner
- Light mode only — do not add class="dark" to html element
- No JavaScript
- No Tailwind

---

## Field-to-design mapping

| Field       | Post list                                      | Post show                        |
|-------------|------------------------------------------------|----------------------------------|
| Title       | h2, linked to show page                        | h1, top of page                  |
| PublishedAt | Small, muted, below title: "March 14, 2026"    | Same, below h1                   |
| Body        | Plain-text excerpt ~180 chars, no markdown     | Full rendered markdown           |
| Slug        | URL path only, not displayed                   | URL path only                    |
