# forge-pattern: Forge Devlog

## CMS note

Built with Forge (forge-cms.dev) — a Go content framework.
Pages are server-side rendered HTML templates.
Deliver clean, semantic HTML with plain CSS custom properties.
No JavaScript. No Tailwind. No frontend framework.

---

## Content type: Post

| Field        | Type     | Required | Example                               |
|--------------|----------|----------|---------------------------------------|
| title        | string   | yes      | "Why Forge Has Zero Dependencies"     |
| body         | markdown | yes      | Full article (paragraphs, headings, code blocks) |
| published_at | datetime | yes      | "2026-02-28T10:00:00Z"                |
| slug         | string   | yes      | "why-forge-has-zero-dependencies"     |

### content.json

```json
[
  {
    "slug": "why-forge-has-zero-dependencies",
    "title": "Why Forge Has Zero Dependencies",
    "published_at": "2026-02-28T10:00:00Z",
    "body": "When we started building Forge, the first architectural decision we locked was simple: zero third-party dependencies in the core package.\n\nGo's standard library is remarkably complete. net/http gives you a production-ready HTTP server. html/template handles safe rendering. database/sql abstracts every relational database.\n\nThe constraint paid dividends immediately. Forge builds in under two seconds."
  },
  {
    "slug": "scheduled-publishing-without-a-job-queue",
    "title": "How We Handle Scheduled Publishing Without a Job Queue",
    "published_at": "2026-03-14T10:00:00Z",
    "body": "Most CMS platforms solve scheduled publishing with a cron job or a dedicated job queue. These work, but they introduce infrastructure you have to operate and keep in sync with your application state.\n\nForge uses an adaptive in-process ticker. When the scheduler starts it looks at the nearest ScheduledAt timestamp and sets its tick interval to half the remaining time, down to a minimum of one second."
  },
  {
    "slug": "content-lifecycle-in-forge",
    "title": "Content Lifecycle in Forge: Draft, Scheduled, Published, Archived",
    "published_at": "2026-04-10T10:00:00Z",
    "body": "Every piece of content in Forge moves through a defined lifecycle: Draft to Scheduled to Published to Archived. Forge enforces this at the framework level, not the application level.\n\nThis matters because lifecycle enforcement is the kind of logic that is trivial to implement once, catastrophic to forget."
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

| Field        | Post list                                      | Post show                        |
|--------------|------------------------------------------------|----------------------------------|
| title        | h2, linked to show page                        | h1, top of page                  |
| published_at | Small, muted, below title — "March 14, 2026"  | Same, below h1                   |
| body         | Plain-text excerpt ~180 chars, no markdown     | Full rendered markdown           |
| slug         | URL path only — not displayed                  | URL path only                    |
