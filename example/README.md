# Smeldr Examples

Three self-contained applications demonstrating different Smeldr feature sets.

| Example | What it demonstrates | Command |
|---------|---------------------|---------|
| `blog` | Full production app: SQLite, auth (Guest/Author), MCP tools, audit log, RSS feed, sitemap, AI index (`llms.txt`), AfterPublish signal, seed data | `cd blog && go run .` |
| `api` | Auth, RBAC, validation hooks, redirects, security headers | `cd api && go run .` |
| `docs` | AI indexing: `llms.txt`, `llms-full.txt`, `aidoc`, `AISummary`, breadcrumbs | `cd docs && go run .` |

Start with `blog` — it is the canonical production pattern.  
`api` and `docs` are focused showcases of specific feature clusters.

## blog

The blog example is a complete, production-ready Smeldr application that you can
use as a starting point for any content-driven site.

**Features:**

- SQLite storage via `modernc.org/sqlite` (pure Go, zero C dependencies)
- Token-based auth: Guest can read, Author can write
- MCP tools for AI-assisted content management
- Audit log on every publish/unpublish action
- RSS feed at `/feed.xml`
- Sitemap at `/sitemap.xml`
- AI index at `/llms.txt` and `/llms-full.txt`
- `AfterPublish` signal hook example
- Scheduler: posts with `Status: "scheduled"` and a past `ScheduledAt` are
  automatically published on the next scheduler tick

**Run:**

```bash
cd blog
go run .
```

The server starts on `:8080`. An admin bootstrap token is printed on first run.

## api

Demonstrates Smeldr's auth and validation features in isolation:
RBAC roles, per-field validation hooks, HTTP redirects, and security headers.

**Run:**

```bash
cd api
go run .
```

## docs

Demonstrates Smeldr's AI indexing features: `llms.txt`, `llms-full.txt`,
`aidoc` content type, `AISummary` helper, and breadcrumb navigation.

**Run:**

```bash
cd docs
go run .
```
