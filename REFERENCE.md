# Forge — Full API Reference

Complete API reference for the Forge framework.
For the short overview and quick-start examples see [README.md](README.md).
Web version: [forge-cms.dev/docs](https://forge-cms.dev/docs).

---

## Getting started

Five minutes from `go get` to a running content API.

```bash
go get github.com/forge-cms/forge
```

**1. Define a content type**

```go
type Post struct {
    forge.Node
    Title string `forge:"required" json:"title"`
    Body  string `forge:"required,min=50" json:"body"`
}

func (p *Post) Head() forge.Head {
    return forge.Head{
        Title:       p.Title,
        Description: forge.Excerpt(p.Body, 160),
        Canonical:   forge.URL("/posts/", p.Slug),
    }
}
```

**2. Wire it up**

```go
app := forge.New(forge.Config{
    BaseURL: "https://mysite.com",
    Secret:  []byte(os.Getenv("SECRET")),
})

app.Content(&Post{},
    forge.At("/posts"),
    forge.Auth(
        forge.Read(forge.Guest),
        forge.Write(forge.Author),
        forge.Delete(forge.Editor),
    ),
)

app.Run(":8080")
```

**3. You have:**

- `GET /posts` — list published posts (JSON or HTML)
- `GET /posts/{slug}` — single post
- `POST /posts` — create (Author+)
- `PUT /posts/{slug}` — update (Author+)
- `DELETE /posts/{slug}` — delete (Author+)
- `GET /posts/sitemap.xml` — auto-generated, always fresh
- Draft posts never visible to unauthenticated requests

No boilerplate. No route registration. No sitemap library.

---

## Core concepts

Forge has six concepts. Learn them once, apply them everywhere.

```
Node      →  the base every content type embeds
Module    →  one content type, fully wired
Signal    →  a hook that fires when something changes
Head      →  all metadata for a page (SEO + social + AI)
Cookie    →  a declared, typed, compliance-aware browser cookie
Role      →  a position in the access hierarchy
```

Everything else is just Go.

---

## Content types

Embed `forge.Node`. Implement `Validate()` and `Head()`. That's the contract.

```go
type BlogPost struct {
    forge.Node                                          // ID, Slug, Status, timestamps

    Title  string      `forge:"required"      json:"title"`
    Body   string      `forge:"required,min=50" json:"body"`
    Author string      `forge:"required"      json:"author"`
    Tags   []string    `                      json:"tags,omitempty"`
    Cover  forge.Image `                      json:"cover,omitempty"`
}

// Validate runs after struct-tag validation.
// Use it for rules that tags cannot express.
func (p *BlogPost) Validate() error {
    if p.Status == forge.Published && len(p.Tags) == 0 {
        return forge.Err("tags", "required when publishing")
    }
    return nil
}

// Head returns all metadata for this content's page.
// Forge uses this for SEO, social sharing, and AI indexing.
func (p *BlogPost) Head() forge.Head {
    return forge.Head{
        Title:       p.Title,
        Description: forge.Excerpt(p.Body, 160),
        Author:      p.Author,
        Tags:        p.Tags,
        Image:       p.Cover,
        Type:        forge.Article,
        Canonical:   forge.URL("/posts/", p.Slug),
        Breadcrumbs: forge.Crumbs(
            forge.Crumb("Home",  "/"),
            forge.Crumb("Posts", "/posts"),
            forge.Crumb(p.Title, "/posts/"+p.Slug),
        ),
    }
}

// Markdown enables AI-friendly content negotiation.
// Accept: text/markdown → returns this. Accept: text/plain → stripped version.
func (p *BlogPost) Markdown() string { return p.Body }
```

### forge.Node — what you always get

```go
type Node struct {
    ID          string        // UUID — internal primary key
    Slug        string        // URL-safe identifier, auto-generated from title
    Status      forge.Status  // Draft | Published | Scheduled | Archived
    PublishedAt time.Time     // zero if not Published
    ScheduledAt *time.Time    // non-nil if Scheduled
    CreatedAt   time.Time
    UpdatedAt   time.Time
}
```

Slug is auto-generated from the first `forge:"required"` string field
unless you set it explicitly. Renaming a slug is safe — the UUID
keeps all internal relations intact.

### forge.Image — SEO-aware image type

```go
type Image struct {
    URL    string // absolute or relative
    Alt    string // required for accessibility and SEO
    Width  int    // required for Open Graph
    Height int    // required for Open Graph
}
```

### Struct tags

Four struct tags control validation, storage, and AI/MCP behaviour. Use them together:

```go
type DocPage struct {
    forge.Node
    Title string `forge:"required,min=3" db:"title" json:"title"`
    Body  string `forge:"required" db:"body" json:"body" forge_format:"markdown" forge_description:"Write in Markdown. Supports headings, lists, and code blocks."`
    Embed string `db:"embed" json:"embed,omitempty" forge_format:"html" forge_description:"Raw HTML only. Use for iframes and third-party embeds. Must be trusted content."`
}
```

| Tag | Purpose |
|-----|---------|
| `forge:"required,min=N"` | Validation — enforced identically across HTTP, API, and MCP calls |
| `db:"column_name"` | SQLRepo column mapping — omit to use lowercased field name |
| `forge_format:"markdown"` | Machine-readable format hint; currently `"markdown"` and `"html"` are supported |
| `forge_description:"..."` | Free-text authoring guidance — appears as the field's `description` in the MCP JSON Schema tool definition |

Validation via `forge` tags cannot be bypassed — an AI agent calling a write tool
faces the same `required` and `min` rules as a direct HTTP POST.

---

## Lifecycle

Every content type has a lifecycle. Always. It cannot be opted out of —
this is what guarantees draft content never leaks to the public,
sitemaps, feeds, or AI crawlers.

```go
forge.Draft      // visible to Author+ (own) and Editor+
forge.Published  // publicly visible
forge.Scheduled  // publishes automatically at ScheduledAt
forge.Archived   // hidden from public, preserved in storage
```

### What Forge enforces automatically

| | Draft | Scheduled | Archived | Published |
|---|---|---|---|---|
| Public GET | 404 | 404 | 404 | ✓ |
| Sitemap | ✗ | ✗ | ✗ | ✓ |
| RSS feed | ✗ | ✗ | ✗ | ✓ |
| AIDoc / llms.txt | ✗ | ✗ | ✗ | ✓ |
| `<meta robots>` | noindex | noindex | noindex | index |
| Author (own content) | ✓ | ✓ | ✓ | ✓ |
| Editor+ | ✓ | ✓ | ✓ | ✓ |

### Scheduled publishing

> ✅ **Available** — the adaptive ticker and automatic `Scheduled → Published` transition are implemented as of Milestone 8.

Forge runs an internal ticker. No external cron needed.

```go
// Schedule via the API
PUT /posts/my-draft
{
  "status":       "scheduled",
  "scheduled_at": "2025-09-01T09:00:00Z"
}
```

At `scheduled_at`, Forge automatically transitions to `Published`,
sets `PublishedAt`, fires `AfterPublish` signals,
regenerates the sitemap, and adds the item to the RSS feed.

---

## Roles & auth

### Built-in role hierarchy

```
Admin   →  full access including app configuration
Editor  →  create, update, delete any content — sees all drafts
Author  →  create, update own content — sees own drafts
Guest   →  read Published content only (unauthenticated)
```

Higher roles inherit all permissions below them.
`forge.Write(forge.Author)` means Author, Editor, and Admin.

### Custom roles

```go
// Create custom roles inline with the hierarchy builder
moderator := forge.NewRole("moderator", forge.RoleBelow(forge.Editor), forge.RoleAbove(forge.Author))
subscriber := forge.NewRole("subscriber", forge.RoleBelow(forge.Author), forge.RoleAbove(forge.Guest))

// Use anywhere a Role is accepted
app.Content(&BlogPost{},
    forge.At("/posts"),
    forge.Auth(forge.Read(subscriber), forge.Write(moderator)),
)
```

### Auth configuration

```go
// Accept bearer tokens (APIs, mobile clients)
app.Use(forge.Authenticate(forge.BearerHMAC(secret)))

// Accept cookie sessions (browser apps)
app.Use(forge.Authenticate(forge.CookieSession("forge_session", secret)))

// Accept both — first match wins
// Use this for apps that serve both a browser UI and an API
app.Use(forge.Authenticate(forge.AnyAuth(
    forge.BearerHMAC(secret),
    forge.CookieSession("forge_session", secret),
)))

// Generate a signed token
token := forge.SignToken(forge.User{
    ID:    "42",
    Name:  "Alice",
    Roles: []forge.Role{forge.Editor},
}, secret)
```

When multiple auth methods are configured, Forge tries them in order and uses the first that succeeds. A request with a valid Bearer token and no cookie is authenticated as a bearer user. A request with neither is treated as `forge.Guest`.

### In hooks and handlers

```go
forge.On(forge.BeforeCreate, func(ctx forge.Context, p *BlogPost) error {
    user := ctx.User()              // forge.User{ID, Name, Roles}
    user.HasRole(forge.Editor)      // true if Editor or above
    user.Is(forge.Author)           // true if exactly Author
    return nil
})
```

---

## SEO & structured data

Define metadata once on your content type. Forge renders it correctly
everywhere — HTML head, JSON-LD, sitemap, RSS, and AI endpoints.

### Head

```go
func (p *BlogPost) Head() forge.Head {
    return forge.Head{
        Title:       p.Title,
        Description: forge.Excerpt(p.Body, 160),
        Author:      p.Author,
        Published:   p.PublishedAt,
        Modified:    p.UpdatedAt,
        Image:       p.Cover,
        Type:        forge.Article,
        Canonical:   forge.URL("/posts/", p.Slug),
        Breadcrumbs: forge.Crumbs(
            forge.Crumb("Home",  "/"),
            forge.Crumb("Posts", "/posts"),
            forge.Crumb(p.Title, "/posts/"+p.Slug),
        ),
    }
}
```

### Advanced: context-aware head with HeadFunc

When `Head()` on your content type is not enough — for example, you need request
context like the site name, a per-request user preference, or a database lookup —
use `HeadFunc`. It receives the full `forge.Context` alongside the item.
`HeadFunc` takes priority over `Headable` when both are present.

```go
// HeadFunc wins over the content type's Head() method when set
app.Content(&BlogPost{},
    forge.At("/posts"),
    forge.HeadFunc(func(ctx forge.Context, p *BlogPost) forge.Head {
        return forge.Head{
            Title: p.Title + " — " + ctx.SiteName(),
        }
    }),
)
```

### Rich result types

```go
forge.Article       // blog posts, news articles
forge.Product       // e-commerce products
forge.FAQPage       // FAQ pages
forge.HowTo         // step-by-step guides
forge.Event         // events with dates and locations
forge.Recipe        // recipes with ingredients
forge.Review        // reviews with ratings
forge.Organization  // company / about pages
```

### Sitemap

```go
app.SEO(forge.SitemapConfig{
    ChangeFreq: forge.Weekly,
    Priority:   0.8,
})
```

Each module owns its fragment (e.g. `/posts/sitemap.xml`).
Forge merges all fragments into `/sitemap.xml` automatically.
Sitemaps regenerate on every publish/unpublish — never stale, never on-demand.

### Robots

```go
app.SEO(forge.RobotsConfig{
    Disallow:  []string{"/admin"},
    Sitemaps:  true,
    AIScraper: forge.AskFirst,  // respectful AI crawler policy
})
```

---

## AI indexing

> ✅ **Available** — `/llms.txt`, AIDoc endpoints, and content negotiation for AI agents.

Forge is the first Go framework to treat AI indexing as a first-class feature.

### llms.txt

Forge generates `/llms.txt` automatically from all registered modules.
Only `Published` content appears. Regenerated on every publish.

```go
app.Content(&BlogPost{},
    forge.At("/posts"),
    forge.AIIndex(forge.LLMsTxt),
)
```

Enable all three AI endpoints in one call:

```go
app.Content(&BlogPost{},
    forge.At("/posts"),
    forge.AIIndex(forge.LLMsTxt, forge.LLMsTxtFull, forge.AIDoc),
)
```

Override with a custom template by creating `templates/llms.txt`:

```
# {{.SiteName}}

> {{.Description}}

## Posts
{{forge_llms_entries .}}
```

### AIDoc format

Every Published content item gets a `/{prefix}/{slug}/aidoc` endpoint.
Designed to be token-efficient and unambiguous for LLMs.

```
+++aidoc+v1+++
type:     article
id:       019242ab-1234-7890-abcd-ef0123456789
slug:     hello-world
title:    Hello World
author:   Alice
created:  2025-01-15
modified: 2025-03-01
tags:     [intro, welcome]
summary:  A short introduction to this blog.
+++
Full body content here — clean, stripped of HTML.
```

The format is designed for token efficiency:
- `status` is omitted — AIDoc endpoints only serve Published content
- Dates use `YYYY-MM-DD` — time and timezone are rarely meaningful for AI consumers
- Responses are gzip-compressed at the transport layer — no token cost, significant network saving for bulk crawling
- No binary encoding — LLMs read this directly without preprocessing

`+++aidoc+v1+++` allows future evolution without breaking existing parsers.

### Content negotiation for AI agents

```bash
# JSON (default)
curl /posts/hello-world

# HTML (when templates registered)
curl /posts/hello-world -H "Accept: text/html"

# Clean markdown (requires Markdown() method on content type)
curl /posts/hello-world -H "Accept: text/markdown"

# Clean plain text (always available)
curl /posts/hello-world -H "Accept: text/plain"
```

No configuration. Forge handles negotiation automatically.

---

## Social sharing

> ✅ **Available** — Open Graph and Twitter Card meta tags are rendered automatically when `forge.Social` is added to a module.

```go
app.Content(&BlogPost{},
    forge.At("/posts"),
    forge.Social(forge.OpenGraph, forge.TwitterCard),
)
```

Forge reads your content type's `Head()` method and renders the correct
meta tags in `<head>`. No additional configuration.

### What your content type needs

```go
func (p *BlogPost) Head() forge.Head {
    return forge.Head{
        Title:       p.Title,
        Description: forge.Excerpt(p.Body, 160),
        Image:       p.Cover,  // used for og:image and twitter:image

        // Per-platform overrides (optional)
        Social: forge.SocialOverrides{
            Twitter: forge.TwitterMeta{
                Card:    forge.SummaryLargeImage,
                Creator: "@alice",
            },
        },
    }
}
```

```go
app.Content(&BlogPost{},
    forge.At("/posts"),
    forge.Social(forge.OpenGraph, forge.TwitterCard),
)
```

Forge renders in `<head>`:

```html
<meta property="og:title"               content="Hello World" />
<meta property="og:description"         content="..." />
<meta property="og:image"               content="https://mysite.com/img/cover.jpg" />
<meta property="og:image:width"         content="1200" />
<meta property="og:image:height"        content="630" />
<meta property="og:type"                content="article" />
<meta property="og:url"                 content="https://mysite.com/posts/hello-world" />
<meta property="article:published_time" content="2025-01-15T09:00:00Z" />
<meta property="article:author"         content="Alice" />
<meta property="article:tag"            content="intro" />
<meta name="twitter:card"               content="summary_large_image" />
<meta name="twitter:title"              content="Hello World" />
<meta name="twitter:creator"            content="@alice" />
```

---

## Cookies & compliance

> ✅ **Available** — typed cookie declarations, consent enforcement, and `/.well-known/cookies.json` are implemented as of Milestone 6.

Forge treats cookies as typed, declared, compliance-aware values.
The category determines which API you can use — enforced at compile time.
It is architecturally impossible to set a non-necessary cookie without consent handling.

### Declaring cookies

```go
var (
    // Necessary — use forge.SetCookie, no consent needed
    SessionCookie = forge.Cookie{
        Name:     "forge_session",
        Category: forge.Necessary,
        Duration: 24 * time.Hour,
        HTTPOnly: true,
        Secure:   true,
        SameSite: http.SameSiteLaxMode,
        Purpose:  "Authenticates the current user session.",
    }

    // Non-necessary — must use forge.SetCookieIfConsented
    PreferenceCookie = forge.Cookie{
        Name:     "forge_prefs",
        Category: forge.Preferences,
        Duration: 365 * 24 * time.Hour,
        Secure:   true,
        SameSite: http.SameSiteLaxMode,
        Purpose:  "Remembers theme and language preferences.",
    }
)
```

### Using cookies

```go
// Necessary — always works
forge.SetCookie(w, r, SessionCookie, sessionID)
value, ok := forge.ReadCookie(r, SessionCookie)
forge.ClearCookie(w, SessionCookie)

// Non-necessary — silently skipped if user has not consented
set := forge.SetCookieIfConsented(w, r, PreferenceCookie, "dark-mode")
```

### Cookie categories

```go
forge.Necessary    // session auth, CSRF — never requires consent
forge.Preferences  // theme, language — requires consent
forge.Analytics    // page views, funnels — requires consent
forge.Marketing    // ad targeting — requires consent
```

### Compliance manifest

```go
// Default — public (compliance transparency by design)
app.Cookies(SessionCookie, PreferenceCookie)

// Restricted — require Editor+ to read the manifest
app.Cookies(SessionCookie, PreferenceCookie,
    forge.ManifestAuth(forge.Editor),
)
```

Forge serves a live manifest at `GET /.well-known/cookies.json`.
Any developer or AI agent can audit your cookie compliance with a single request.

```json
{
  "generated": "2025-06-01T00:00:00Z",
  "cookies": [
    {
      "name":     "forge_session",
      "category": "necessary",
      "duration": "24h",
      "purpose":  "Authenticates the current user session.",
      "consent":  false
    },
    {
      "name":     "forge_prefs",
      "category": "preferences",
      "duration": "8760h",
      "purpose":  "Remembers theme and language preferences.",
      "consent":  true
    }
  ]
}
```

---

## Storage

Forge accepts any database connection that satisfies the `forge.DB` interface —
which `*sql.DB` and any pgx adapter already implement.
You write SQL. Forge handles scanning and mapping.

Forge core has zero dependencies. The driver is always your choice.
Performance is the default recommendation — zero-dependency is the alternative.

### Choosing a driver

**Recommended — pgx via stdlib shim (~1.8× faster than lib/pq)**

For most PostgreSQL users. One dependency, near-native pgx speed,
compatible with all standard `*sql.DB` tooling.

```go
import "github.com/jackc/pgx/v5/stdlib"

db := stdlib.OpenDB(connConfig) // *sql.DB backed by pgx
app := forge.New(forge.Config{DB: db, ...})
```

**Maximum performance — native pgx connection pool (~2.5× faster)**

For high-throughput production workloads. Uses `forge-pgx`,
a thin adapter that is a separate module from Forge core.

```go
import (
    forgepgx "github.com/forge-cms/forge-pgx"
    "github.com/jackc/pgx/v5/pgxpool"
)

pool, _ := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
app := forge.New(forge.Config{DB: forgepgx.Wrap(pool), ...})
```

**Zero dependencies — standard database/sql**

For SQLite, MySQL, or teams that cannot add any dependency.
Swap the driver without changing any other Forge code.

```go
import (
    "database/sql"
    _ "github.com/mattn/go-sqlite3"     // SQLite
    // _ "github.com/go-sql-driver/mysql" // MySQL
    // _ "github.com/lib/pq"             // PostgreSQL (slower than pgx)
)

db, _ := sql.Open("sqlite3", "./mysite.db")
app := forge.New(forge.Config{DB: db, ...})
```

Switching between all three approaches requires changing exactly one value
in `forge.Config`. Nothing else in your codebase changes.

### Querying

```go
// Single item — returns typed result, maps columns to struct fields
post, err := forge.QueryOne[*BlogPost](db,
    "SELECT * FROM posts WHERE slug = $1 AND status = $2",
    slug, forge.Published,
)
if errors.Is(err, forge.ErrNotFound) {
    // no row — use forge.ErrNotFound, not sql.ErrNoRows
}

// List with pagination
opts := forge.ListOptions{Page: 1, PerPage: 20, OrderBy: "published_at", Desc: true}

posts, err := forge.Query[*BlogPost](db,
    "SELECT * FROM posts WHERE status = $1 ORDER BY published_at DESC LIMIT $2 OFFSET $3",
    forge.Published, opts.PerPage, opts.Offset(),
)
```

Forge maps columns to struct fields by `db` tag first, then by field name.
No ORM. No query builder. SQL is the query language — and AI assistants write it extremely well.

```go
type BlogPost struct {
    forge.Node
    Title  string `forge:"required" db:"title"  json:"title"`
    Body   string `forge:"required" db:"body"   json:"body"`
    Author string `forge:"required" db:"author" json:"author"`
}
// db tag controls column mapping — omit it and Forge uses the field name lowercased
```

### Repository interface

For testing, prototyping, and custom backends:

```go
type Repository[T any] interface {
    FindByID(ctx context.Context, id string) (T, error)
    FindBySlug(ctx context.Context, slug string) (T, error)
    FindAll(ctx context.Context, opts forge.ListOptions) ([]T, error)
    Save(ctx context.Context, node T) error
    Delete(ctx context.Context, id string) error
}

// Zero-config in-memory implementation
repo := forge.NewMemoryRepo[*BlogPost]()
```

### Production SQL repository

> ✅ **Available** — `SQLRepo[T]` is a production-ready `Repository[T]` backed by `forge.DB`, implemented as of Milestone 7.

`SQLRepo[T]` derives the table name automatically (`BlogPost` → `blog_posts`) or accepts a `Table()` override:

```go
// Auto-derived table name: blog_posts
repo := forge.NewSQLRepo[*BlogPost](db)

// Explicit table name
repo := forge.NewSQLRepo[*BlogPost](db, forge.Table("posts"))

// T must be a pointer type — NewSQLRepo[*BlogPost] pairs with NewModule((*BlogPost)(nil), ...)
repo := forge.NewSQLRepo[*BlogPost](db)

m := forge.NewModule((*BlogPost)(nil),
    forge.At("/posts"),
    forge.Repo(repo),
)

app.Content(m)
```

`SQLRepo` uses `$N` positional placeholders (PostgreSQL / pgx compatible) and upserts via `ON CONFLICT (id) DO UPDATE`.

---

## Middleware

### Global

```go
app.Use(
    forge.RequestLogger(),              // structured slog output
    forge.Recoverer(),                  // panic → 500, process never crashes
    forge.CORS("https://mysite.com"),   // CORS headers
    forge.MaxBodySize(1 << 20),         // 1 MB request limit
    forge.RateLimit(100, time.Minute),  // 100 req/min per IP
    forge.SecurityHeaders(),            // HSTS, CSP, X-Frame-Options, Referrer-Policy
)
```

### Per-module

```go
app.Content(&BlogPost{},
    forge.At("/posts"),
    forge.Middleware(
        forge.InMemoryCache(5*time.Minute),                        // LRU, max 1000 entries
    // forge.InMemoryCache(5*time.Minute, forge.CacheMaxEntries(500)), // custom limit
        myCustomMiddleware,
    ),
)
```

### Writing middleware

Standard `http.Handler` wrapping — no Forge-specific types required:

```go
func myCustomMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        ctx := forge.ContextFrom(w, r)  // access forge.Context if needed
        _ = ctx.User()
        next.ServeHTTP(w, r)
    })
}
```

---

## Templates & rendering

### Content negotiation

Forge selects the response format from the `Accept` header.
Register templates to enable HTML. Everything else works automatically.

```
Accept: application/json  →  JSON (always available)
Accept: text/html         →  HTML template (requires forge.Templates)
Accept: text/markdown     →  raw markdown (requires Markdown() method)
Accept: text/plain        →  clean text (always available)
```

### Template convention

```go
app.Content(&BlogPost{},
    forge.At("/posts"),
    forge.Templates("templates/posts"),        // parsed at startup, fails fast if missing
    // forge.TemplatesOptional("templates/posts"), // no startup failure if missing
    // Forge looks for:
    //   templates/posts/list.html  →  GET /posts
    //   templates/posts/show.html  →  GET /posts/{slug}
)
```

### In templates

```html
{{/* show.html */}}
{{template "forge:head" .}}

<article>
    <h1>{{.Content.Title}}</h1>
    <p>By {{.Content.Author}} · {{.Content.PublishedAt | forge_date}}</p>
    {{.Content.Body | forge_markdown}}
</article>

{{/* list.html */}}
{{template "forge:head" .}}

{{range .Content}}
<a href="/posts/{{.Slug}}">
    <h2>{{.Title}}</h2>
    <p>{{.Body | forge_excerpt 120}}</p>
</a>
{{end}}
```

The `forge:head` partial renders everything in `<head>` automatically:
`<title>`, `<meta>`, canonical, Open Graph, Twitter Cards, `twitter:site`,
app-level JSON-LD, JSON-LD, breadcrumbs,
and `<meta name="robots">` based on content Status.

### Template functions

All functions are registered in `TemplateFuncMap` and are available in every module template.

| Function | Input | Output | Use case |
|----------|-------|--------|----------|
| `forge_markdown` | Markdown string | `template.HTML` | Body fields authored in Markdown |
| `forge_html` | HTML string | `template.HTML` | Fields that already contain rendered HTML |
| `forge_date` | `time.Time` | string | Human-readable date (`2 Jan 2006`) |
| `forge_rfc3339` | `time.Time` | string | Machine-readable datetime for `<time datetime>` |
| `forge_excerpt` | string, int | string | Truncated plain-text summary |
| `forge_meta` | string | `template.HTML` | Escaped `<meta>` attribute value |
| `forge_csrf_token` | — | `template.HTML` | Hidden CSRF input field |
| `forge_llms_entries` | `.` (template data) | `template.HTML` | Rendered llms.txt entry list |

#### forge_markdown

`forge_markdown` converts a Markdown string to safe HTML. All content is HTML-escaped
before tag wrapping — no XSS risk from user-authored text.

```html
{{.Content.Body | forge_markdown}}
```

**HTML passthrough:** lines whose trimmed form starts with `<` are emitted verbatim —
they bypass the Markdown renderer and go straight to output. This allows raw HTML blocks
(feature grids, pull quotes via CSS classes, `<div>` containers) to be embedded inline
in a Markdown field:

```markdown
This is regular **Markdown**.

<div class="pull-quote">A trusted raw HTML block.</div>

More regular Markdown here.
```

Security model: Forge is self-hosted; content authors are trusted (same role system
that governs MCP write operations). No sanitisation is applied to verbatim HTML lines.

#### forge_html

`forge_html` marks a string as trusted HTML, bypassing Go's default HTML escaping.
Use it when a field already contains rendered HTML — for example, pre-rendered video
embeds, third-party iframes, or content migrated from another system.

```html
{{.Content.Embed | forge_html}}
```

Never pass unsanitised user input to `forge_html`. The string is emitted verbatim
with no escaping — the caller is responsible for ensuring the content is safe.

| | `forge_markdown` | `forge_html` |
|---|---|---|
| Input format | Markdown text | Already-rendered HTML |
| HTML passthrough | Lines starting with `<` only | Entire value verbatim |
| Use case | Body fields authored in Markdown | Pre-rendered content, iframes, migration |

### Template data shape


```go
type TemplateData[T Node] struct {
    Content     T                  // T for show, []T for list
    Head        forge.Head         // from Headable.Head() on T, or HeadFunc if provided (HeadFunc takes priority)
    User        forge.User         // current user (zero value if Guest)
    Request     *http.Request
    OGDefaults  *forge.OGDefaults  // app-level OG/Twitter fallbacks (nil if not configured)
    AppSchema   template.HTML      // pre-rendered app-level JSON-LD block (empty if not configured)
    HeadAssets  *forge.HeadAssets  // preconnect/stylesheets/links/scripts (nil if not configured)
}
```

### Shared partials

✅ **Available**

Define nav, footer, or any shared HTML once and inject it into every module
template set — and into custom handler templates — automatically.

```go
app.Partials("templates/partials") // any *.html file in the directory is a partial
```

Each partial file must use `{{define "name"}}...{{end}}` syntax (same convention
as `forge:head`). Files are loaded in alphabetical order for determinism.

```html
{{/* templates/partials/nav.html */}}
{{define "nav"}}
<nav><a href="/">Home</a> | <a href="/posts">Blog</a></nav>
{{end}}
```

```html
{{/* templates/posts/list.html */}}
{{template "nav" .}}
{{range .Content}}<a href="/posts/{{.Slug}}"><h2>{{.Title}}</h2></a>{{end}}
```

For custom `app.Handle()` routes that also need shared partials, use
`App.MustParseTemplate`:

```go
// parsed once at startup — includes TemplateFuncMap, forge:head, and all partials
homeTpl := app.MustParseTemplate("templates/home.html")

app.Handle("GET /", func(w http.ResponseWriter, r *http.Request) {
    data := forge.NewTemplateData[any](ctx, nil, forge.Head{Title: "Home"}, "My Site")
    homeTpl.Execute(w, data)
})
```

`MustParseTemplate` panics on error (consistent with `MustConfig`), so
misconfigured templates fail at startup rather than at first request.

### Custom handler with forge:head

✅ **Available**

Custom handler data structs can embed `forge.PageHead` to gain
`{{template "forge:head" .}}` support without using `TemplateData[T]`:

```go
type homeData struct {
    forge.PageHead        // promotes Head, OGDefaults, AppSchema, HeadAssets
    Posts      []*Post
    Featured   *Post
}

homeTpl := app.MustParseTemplate("templates/home.html")

app.Handle("GET /", func(w http.ResponseWriter, r *http.Request) {
    data := homeData{
        PageHead: forge.PageHead{Head: forge.Head{Title: "Home"}},
        Posts:    loadPosts(),
    }
    homeTpl.Execute(w, data)
})
```

In `templates/home.html`:

```html
<head>{{template "forge:head" .}}</head>
<body>
    {{range .Posts}}<h2>{{.Title}}</h2>{{end}}
</body>
```

`PageHead` is the same struct that `TemplateData[T]` embeds internally.
Any field on `PageHead` set by `app.SEO(...)` (such as `HeadAssets`) must be
populated manually in custom handlers — module templates receive these
automatically, but `app.Handle()` routes are outside the module render path.

### Per-request extra data (ContextFunc)

✅ **Available**

Pass additional data — sidebar items, navigation trees, related posts — into
a module's list or show template without writing a custom handler:

```go
app.Content(forge.NewModule((*DocPage)(nil),
    forge.At("/docs"),
    forge.Repo(docRepo),
    forge.Templates("templates/docs"),
    forge.ContextFunc(func(ctx forge.Context, _ any) (any, error) {
        return docRepo.FindAll(ctx, forge.ListOptions{
            Status: []forge.Status{forge.Published},
        })
    }),
))
```

The return value is available as `.Extra` in the template:

```html
{{- $nav := .Extra}}
<nav>
  {{range $nav}}<a href="/docs/{{.Slug}}">{{.Title}}</a>{{end}}
</nav>
```

The `item` argument is the content being rendered — `T` for show, `[]T` for
list. Cast it inside the function if the concrete type is needed. Errors from
`ContextFunc` log and set `.Extra` to nil; the render is never aborted.

### Site-wide static assets

✅ **Available**

Inject preconnect hints, stylesheets, favicon links, and scripts into `forge:head`
on every page via `app.SEO`:

```go
app.SEO(&forge.HeadAssets{
    Preconnect:  []string{"https://fonts.googleapis.com"},
    Stylesheets: []string{
        "https://fonts.googleapis.com/css2?family=Inter&display=swap",
        "/static/app.css",
    },
    Links: []forge.HeadLink{
        {Rel: "icon", Type: "image/png", Sizes: "32x32", Href: "/favicon-32.png"},
        {Rel: "apple-touch-icon", Href: "/apple-touch-icon.png"},
    },
    Scripts: []forge.ScriptTag{
        {Src: "/static/app.js", Defer: true},
    },
})
```

Assets are emitted in order: preconnect → stylesheets → links → scripts.
Inline script bodies use `template.JS` to opt in to verbatim emission:

```go
forge.ScriptTag{Body: template.JS("console.log('Forge')")} // never pass user input here
```

---

## Error handling

Forge uses a typed error hierarchy. Every error knows its HTTP status,
its machine-readable code, and what is safe to show the client.
Internal details are logged — never leaked.

### Sentinel errors

```go
forge.ErrNotFound   // 404 — resource does not exist
forge.ErrGone       // 410 — resource existed but was intentionally removed
forge.ErrForbidden  // 403 — authenticated but insufficient role
forge.ErrUnauth     // 401 — not authenticated
forge.ErrConflict   // 409 — state conflict (e.g. duplicate slug)
```

### In hooks and custom handlers

```go
forge.On(forge.BeforeCreate, func(ctx forge.Context, p *BlogPost) error {
    if slugExists(p.Slug) {
        return forge.ErrConflict                   // → 409
    }
    if !ctx.User().HasRole(forge.Editor) {
        return forge.ErrForbidden                  // → 403
    }
    return forge.Err("title", "already taken")     // → 422 with field detail
})
```

### Error responses follow the Accept header

```json
// Accept: application/json
{
  "error": {
    "code":       "validation_failed",
    "message":    "Validation failed",
    "request_id": "019242ab-1234-7890-abcd-ef0123456789",
    "fields": [
      { "field": "title", "message": "required" },
      { "field": "body",  "message": "minimum 50 characters" }
    ]
  }
}
```

HTML error pages are rendered from `templates/errors/{status}.html` if present.

### Request tracing

Every request gets a `X-Request-ID` header (UUID v7).
The same ID appears in error responses and every structured log entry —
making it trivial to trace a user-reported error to an exact log line.

```go
// Available in all hooks and handlers
id := ctx.RequestID()
```

---

## Redirects & content mobility

> ✅ **Available** — manual redirects (`app.Redirect`), prefix rewrites (`Redirects(From(...))`), 410 Gone, chain collapse, and `/.well-known/redirects.json` are implemented as of Milestone 7.

Forge automatically tracks every URL a piece of content has ever had.
Rename a slug, change a prefix, archive a post — inbound links and SEO
rankings are preserved without any developer effort.

### What happens automatically

| Event | Previous URL response |
|-------|-----------------------|
| Slug renamed | `301` → new URL |
| Module prefix changed | `301` → new prefix + slug |
| Node archived | `410 Gone` |
| Node deleted | `410 Gone` |
| Node drafted / scheduled | `404` (does not leak existence) |

### Why 410 and not 404 for archived content

`410 Gone` tells search engines the content was *intentionally* removed.
Google de-indexes `410` pages significantly faster than `404` pages.
For a CMS, this is almost always what you want.

### Manual redirects

```go
// Bulk redirect when renaming a module prefix
app.Content(&BlogPost{},
    forge.At("/articles"),                              // new prefix
    forge.Redirects(forge.From("/posts"), "/articles"), // 301 all /posts/* → /articles/*
)

// One-off redirects
app.Redirect("/old-path",  "/new-path", forge.Permanent) // 301
app.Redirect("/removed",   "",          forge.Gone)       // 410
```

### Optional DB persistence

To persist redirects across restarts, create the `forge_redirects` table and
call `Load` at startup:

```sql
CREATE TABLE forge_redirects (
    from_path TEXT PRIMARY KEY,
    to_path   TEXT NOT NULL DEFAULT '',
    code      INTEGER NOT NULL DEFAULT 301,
    is_prefix BOOLEAN NOT NULL DEFAULT FALSE
);
```

```go
if err := app.RedirectStore().Load(ctx, db); err != nil {
    log.Fatal(err)
}
```

### Inspect the redirect table

```
GET /.well-known/redirects.json   (requires Editor+)
```

---

## MCP integration (forge-mcp)

✅ **Available**

`forge-mcp` is a separate module that wraps a `forge.App` and exposes its
content modules to AI assistants via the [Model Context Protocol](https://modelcontextprotocol.io).
Schema derivation, lifecycle enforcement, and role checks are all automatic —
no configuration beyond `forge.MCP(...)` on your existing modules.

```go
import forgemcp "github.com/forge-cms/forge/forge-mcp"

func main() {
    app := forge.New(forge.MustConfig(forge.Config{
        BaseURL: "https://mysite.com",
        Secret:  []byte(os.Getenv("SECRET")),
    }))
    app.Content(
        forge.NewModule((*Post)(nil), forge.At("/posts"), forge.MCP(forge.MCPWrite)),
    )
    forgemcp.New(app).ServeStdio(context.Background(), os.Stdin, os.Stdout)
}
```

For Claude Desktop, Cursor, and SSE remote transport configuration see
[forge-mcp/README.md](forge-mcp/README.md).

---

## The AI-first design philosophy

Forge is the first Go framework explicitly designed to be maintained by AI assistants.

**Intent over mechanics**  
`forge.SEO(forge.RichArticle)` — not 40 lines of JSON-LD template code.
An AI assistant reads, modifies, and explains your intent without touching internals.

**Declarative over imperative**  
Every content module is fully described by its `app.Content(...)` call.
No tracing middleware chains. No hunting through files for route registration.

**Impossible to get wrong by accident**  
Draft content cannot leak. Non-necessary cookies cannot be set without consent handling.
These are architectural guarantees, not conventions.

**Self-describing**  
```
GET /.well-known/cookies.json  →  cookie compliance audit
GET /llms.txt                  →  site structure for AI crawlers
GET /posts/hello-world/aidoc   →  token-efficient content for LLMs
GET /sitemap.xml               →  always fresh, event-driven
```

**One right way**  
One way to declare cookies. One way to handle SEO. One way to register content.
AI assistants never guess which pattern you used.

**Consistent naming**  
Every exported symbol: `forge.Verb(Noun)` or `forge.Noun`.
No abbreviations. No clever names. Predictable, searchable, memorable.

---

## Minimal complete example

```go
package main

import (
    "os"
    "time"

    "github.com/forge-cms/forge"
)

type Article struct {
    forge.Node
    Title  string      `forge:"required"         json:"title"`
    Body   string      `forge:"required,min=100"  json:"body"`
    Author string      `forge:"required"         json:"author"`
    Cover  forge.Image `                          json:"cover,omitempty"`
}

func (a *Article) Validate() error {
    if a.Status == forge.Published && a.Cover.URL == "" {
        return forge.Err("cover", "required when publishing")
    }
    return nil
}

func (a *Article) Head() forge.Head {
    return forge.Head{
        Title:       a.Title,
        Description: forge.Excerpt(a.Body, 160),
        Author:      a.Author,
        Image:       a.Cover,
        Type:        forge.Article,
        Canonical:   forge.URL("/articles/", a.Slug),
    }
}

func (a *Article) Markdown() string { return a.Body }

func main() {
    secret := []byte(os.Getenv("SECRET"))

    app := forge.New(forge.Config{
        BaseURL: "https://mysite.com",
        Secret:  secret,
    })

    app.Use(
        forge.RequestLogger(),
        forge.Recoverer(),
        forge.SecurityHeaders(),
        forge.MaxBodySize(1 << 20),
        forge.Authenticate(forge.AnyAuth(
            forge.BearerHMAC(secret),
            forge.CookieSession("session", secret),
        )),
    )

    app.SEO(forge.SitemapConfig{ChangeFreq: forge.Weekly, Priority: 0.8})
    app.SEO(forge.RobotsConfig{AIScraper: forge.AskFirst})

    app.Content(&Article{},
        forge.At("/articles"),
        forge.Auth(
            forge.Read(forge.Guest),
            forge.Write(forge.Author),
            forge.Delete(forge.Editor),
        ),
        forge.Cache(10*time.Minute),
        forge.Social(forge.OpenGraph, forge.TwitterCard),
        forge.AIIndex(forge.LLMsTxt, forge.AIDoc),
        forge.Templates("templates/articles"),
        forge.On(forge.BeforeCreate, func(ctx forge.Context, a *Article) error {
            a.Author = ctx.User().Name
            return nil
        }),
    )

    app.Run(":8080")
}
```

**~70 lines. What you get:**

Full CRUD · Role-based auth · Draft-safe lifecycle  
Structured data (JSON-LD) · Event-driven sitemap · Content negotiation  
Open Graph · Twitter Cards · AI indexing · RSS feed  
Security headers · Graceful shutdown · Cookie compliance manifest  
Scheduled publishing

---

## forge-media

✅ **Available**

Optional submodule for file upload, storage, serving, and AI-agent access via MCP.

### Install

```
go get github.com/forge-cms/forge-media
```

### Wiring

```go
import forgemedia "github.com/forge-cms/forge-media"

store := forgemedia.NewLocalMediaStore(app)
mediaSrv := forgemedia.Register(app, store)

// Wire into the MCP server so AI agents can upload files.
mcpSrv := forgemcp.New(app, forgemcp.WithModule(mediaSrv))
```

`Register` mounts all four HTTP routes and returns a `*Server` that implements
`forge.MCPModule`. Pass it to `forgemcp.WithModule` to expose the MCP tools.

A database is required. Call `forgemedia.CreateMediaTable(db)` once to create
the `forge_media` table, or run the migration manually.

### HTTP endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/media` | Author+ | Upload a file. Multipart body with `file` and `description` fields. Returns `{"id","url","media_type","mime_type"}` (201). Image uploads require a non-empty `description` (WCAG 1.1.1). |
| `GET` | `/media/{filename}` | Public | Serve a stored file. No authentication required. |
| `GET` | `/media` | Editor+ | List all records. Optional `?type=image\|document\|video\|other` filter. |
| `DELETE` | `/media/{id}` | Editor+ | Delete a record and its stored file. Returns 204 No Content. |

### MCP tools

Exposed when `forgemcp.WithModule(mediaSrv)` is wired. TypeName is `"File"`.

| Tool | Role | Description |
|------|------|-------------|
| `create_file` | Author+ | Upload a file. Required fields: `filename`, `data` (base64-encoded). Optional: `description` (required for images). |
| `list_files` | Editor+ | List all uploaded files. Returns an array of `MediaRecord` objects. |
| `get_file` | Editor+ | Fetch a single file record by ID. |
| `delete_file` | Editor+ | Delete a file record and its stored file by ID. |

`update_file`, `publish_file`, `schedule_file`, and `archive_file` are not
supported — media files have no lifecycle. These tools return `ErrBadRequest`.

### Config keys

The two `forge.config` keys below are read automatically by `LocalMediaStore`.
Set them in your `forge.config` file or override in Go code via `forge.Config`.

| Key | Default | Description |
|-----|---------|-------------|
| `media_path` | `./media` | Directory where uploaded files are stored on disk. |
| `media_max_size` | `5242880` (5 MB) | Maximum upload size in bytes. |

Full reference: [forge-media README](https://github.com/forge-cms/forge/tree/main/forge-media)

---

## forge.config file

Forge reads a `forge.config` file from the working directory (or from the path
in the `FORGE_CONFIG` environment variable) at startup. Fields set in Go code
always take precedence over file values. The file format is one `key = value`
pair per line; lines starting with `#` are comments.

The `secret` key is forbidden in config files — it must be supplied as an
environment variable or directly in Go code.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `base_url` | string | — | Canonical site URL, e.g. `https://example.com` |
| `https` | bool | `false` | Force HTTP → HTTPS redirect |
| `nav_mode` | `db` \| `code` | — | Navigation mode |
| `org_name` | string | — | JSON-LD organisation name |
| `org_type` | string | — | JSON-LD organisation type |
| `twitter_site` | string | — | Twitter/X site handle, e.g. `@mysite` |
| `og_image` | string | — | Default Open Graph image URL |
| `media_path` | string | `./media` | Upload directory for forge-media |
| `media_max_size` | integer | `5242880` | Max upload size in bytes (forge-media) |

Example:

```
# forge.config
base_url = https://mysite.com
https    = true
nav_mode = db
media_path     = /var/data/media
media_max_size = 10485760
```

### Health endpoint

`GET /_health` returns HTTP 200 with a JSON body containing the framework version
and status. No authentication is required.

```
GET /_health
→ 200 application/json
{"status":"ok","forge":"1.13.1"}
```

When companion modules such as `forge-mcp` are linked into the binary, their
versions appear alongside:

```json
{"status":"ok","forge":"1.13.1","forge_mcp":"1.5.0"}
```

The same version data is written to stderr at startup:

```
forge: forge 1.13.1, forge_mcp 1.5.0
```

`/_health` is exempt from the HTTPS redirect middleware (A59) so that
co-located reverse proxies (e.g. Caddy `health_uri`) can probe via plain HTTP
without triggering a `301` redirect.

`App.Health()` must be called once to mount the endpoint — it is not mounted
automatically:

```go
app.Health()
```

---

## Known issues

**Windows: CSS files served as `text/plain`**

Go's MIME type lookup on Windows uses the registry, which may map `.css`
to `text/plain`. If your browser rejects stylesheets during local development,
add this to your `main()` before starting the server:

```go
import "mime"
mime.AddExtensionType(".css", "text/css")
```
