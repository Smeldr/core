# Forge — Full API Reference

Complete API reference for the Forge framework.
For the short overview and quick-start examples see [README.md](README.md).
Web version: [smeldr.dev/docs](https://smeldr.dev/docs).

---

## Getting started

Five minutes from `go get` to a running content API.

```bash
go get smeldr.dev/core
```

**1. Define a content type**

```go
type Post struct {
    smeldr.Node
    Title string `forge:"required" json:"title"`
    Body  string `forge:"required,min=50" json:"body"`
}

func (p *Post) Head() smeldr.Head {
    return smeldr.Head{
        Title:       p.Title,
        Description: smeldr.Excerpt(p.Body, 160),
        Canonical:   smeldr.URL("/posts/", p.Slug),
    }
}
```

**2. Wire it up**

```go
app := smeldr.New(smeldr.Config{
    BaseURL: "https://mysite.com",
    Secret:  []byte(os.Getenv("SECRET")),
})

app.Content(smeldr.NewModule((*Post)(nil),
    smeldr.At("/posts"),
    smeldr.Auth(
        smeldr.Read(smeldr.Guest),
        smeldr.Write(smeldr.Author),
        smeldr.Delete(smeldr.Editor),
    ),
))

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

Embed `smeldr.Node`. Implement `Validate()` and `Head()`. That's the contract.

```go
type BlogPost struct {
    smeldr.Node                                          // ID, Slug, Status, timestamps

    Title  string      `forge:"required"      json:"title"`
    Body   string      `forge:"required,min=50" json:"body"`
    Author string      `forge:"required"      json:"author"`
    Tags   []string    `                      json:"tags,omitempty"`
    Cover  smeldr.Image `                      json:"cover,omitempty"`
}

// Validate runs after struct-tag validation.
// Use it for rules that tags cannot express.
func (p *BlogPost) Validate() error {
    if p.Status == smeldr.Published && len(p.Tags) == 0 {
        return smeldr.Err("tags", "required when publishing")
    }
    return nil
}

// Head returns all metadata for this content's page.
// Forge uses this for SEO, social sharing, and AI indexing.
func (p *BlogPost) Head() smeldr.Head {
    return smeldr.Head{
        Title:       p.Title,
        Description: smeldr.Excerpt(p.Body, 160),
        Author:      p.Author,
        Tags:        p.Tags,
        Image:       p.Cover,
        Type:        smeldr.Article,
        Canonical:   smeldr.URL("/posts/", p.Slug),
        Breadcrumbs: smeldr.Crumbs(
            smeldr.Crumb("Home",  "/"),
            smeldr.Crumb("Posts", "/posts"),
            smeldr.Crumb(p.Title, "/posts/"+p.Slug),
        ),
    }
}

// Markdown enables AI-friendly content negotiation.
// Accept: text/markdown → returns this. Accept: text/plain → stripped version.
func (p *BlogPost) Markdown() string { return p.Body }
```

### smeldr.Node — what you always get

```go
type Node struct {
    ID          string        // UUID — internal primary key
    Slug        string        // URL-safe identifier, auto-generated from title
    Status      smeldr.Status  // Draft | Published | Scheduled | Archived
    PublishedAt time.Time     // zero if not Published
    ScheduledAt *time.Time    // non-nil if Scheduled
    CreatedAt   time.Time
    UpdatedAt   time.Time
}
```

Slug is auto-generated from the first `forge:"required"` string field
unless you set it explicitly. Renaming a slug is safe — the UUID
keeps all internal relations intact.

### smeldr.Image — SEO-aware image type

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
    smeldr.Node
    Title string `forge:"required,min=3" db:"title" json:"title"`
    Body  string `forge:"required" db:"body" json:"body" smeldr_format:"markdown" smeldr_description:"Write in Markdown. Supports headings, lists, and code blocks."`
    Embed string `db:"embed" json:"embed,omitempty" smeldr_format:"html" smeldr_description:"Raw HTML only. Use for iframes and third-party embeds. Must be trusted content."`
}
```

| Tag | Purpose |
|-----|---------|
| `forge:"required,min=N"` | Validation — enforced identically across HTTP, API, and MCP calls |
| `db:"column_name"` | SQLRepo column mapping — omit to use lowercased field name |
| `smeldr_format:"markdown"` | Machine-readable format hint; currently `"markdown"` and `"html"` are supported |
| `smeldr_description:"..."` | Free-text authoring guidance — appears as the field's `description` in the MCP JSON Schema tool definition |

Validation via `forge` tags cannot be bypassed — an AI agent calling a write tool
faces the same `required` and `min` rules as a direct HTTP POST.

---

## Lifecycle

Every content type has a lifecycle. Always. It cannot be opted out of —
this is what guarantees draft content never leaks to the public,
sitemaps, feeds, or AI crawlers.

```go
smeldr.Draft      // visible to Author+ (own) and Editor+
smeldr.Published  // publicly visible
smeldr.Scheduled  // publishes automatically at ScheduledAt
smeldr.Archived   // hidden from public, preserved in storage
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

## Routing variants

✅ **Available**

Forge modules expose a list endpoint and a per-item show endpoint by default.
Two options change this for common patterns.

### SingleInstance — singleton page modules

Use `smeldr.SingleInstance()` when a module holds exactly one canonical item
(About, Contact, Terms, Privacy). The item is served directly at the module
prefix with no slug in the URL.

```go
smeldr.NewModule((*AboutPage)(nil),
    smeldr.At("/about"),
    smeldr.Repo(repo),
    smeldr.SingleInstance(),
)
// GET /about → serves the first Published item as JSON (or HTML if Templates is set)
// GET /about/{slug} → 404 (route not registered)
```

Rules:
- `GET /{prefix}` serves `items[0]` from the repository filtered to `Published`.
  Author and Editor roles see items at any status.
- `GET /{prefix}/{slug}` is not registered. Requests to it return 404.
- Preview tokens work the same as the standard show handler.
  `smeldr.dev/mcp` generates the preview URL as `/{prefix}?preview={token}` — no slug
  in the path (requires smeldr.dev/mcp ≥ v1.10.2).
- `MCPMeta().SingleInstance` returns `true`. The `smeldr.dev/mcp` server suppresses
  the `list_{type}s` admin tool for SingleInstance modules.

#### Pattern: SingleInstance with custom public handler

Use this pattern when the public URL for the content differs from the module
prefix — for example, a homepage served at `/` while the module is registered
at `/homepage`.

```go
// Register the content type as SingleInstance.
// GET /homepage → serves the published record (admin + MCP access).
// list_home_pages MCP tool suppressed.
// create_preview_url generates /homepage?preview=<token> (no slug).
app.Content(smeldr.NewModule((*HomePage)(nil),
    smeldr.Repo(homePageRepo),
    smeldr.At("/homepage"),
    smeldr.SingleInstance(),
    smeldr.MCP(smeldr.MCPRead, smeldr.MCPWrite),
))

// Serve the public route with a custom handler.
// Reads the published record; falls back to defaults when none exists.
app.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    hps, _ := homePageRepo.FindAll(r.Context(), smeldr.ListOptions{
        Status: []smeldr.Status{smeldr.Published},
    })
    hp := homePageDefaults()
    for _, p := range hps {
        if p.Slug == "home" {
            hp = p
            break
        }
    }
    // render template with hp ...
}))
```

The module prefix (`/homepage`) is the admin and MCP surface. The public URL
(`/`) is fully independent — the custom handler owns it entirely.

#### When to use SingleInstance

- Exactly one record of this type will ever be published
- No list page is needed
- Content is managed via MCP or CLI, not browsed by users
- The public URL may differ from the module prefix

#### When not to use SingleInstance

- Multiple records of the same type — use a regular module
- The module prefix **is** the public URL and templates handle rendering — use
  a regular module with `smeldr.Templates(...)`
- Items need per-item slug URLs — use a regular module or `Standalone()`

### Standalone — first-class top-level URLs

Use `smeldr.Standalone()` when items should appear at `/{slug}` rather than
`/{prefix}/{slug}`. This is useful for landing pages, blog posts as root-level
pages, or wiki entries.

```go
smeldr.NewModule((*Post)(nil),
    smeldr.At("/posts"),
    smeldr.Repo(repo),
    smeldr.Standalone(),
)
// GET /my-post → serves the Published Post with slug "my-post"
// GET /posts   → list of all Published posts (unchanged)
// GET /posts/my-post → 404 (slug URL not registered under prefix)
```

Rules:
- `GET /{prefix}` still serves the list of all Published items.
- `GET /{slug}` is registered at the App level, dispatched to whichever
  Standalone module owns the item with that slug.
- Multiple Standalone modules coexist. Each slug is tried in registration
  order; the first module that can serve it wins.
- If no module has the slug, the request falls through to the redirect
  handler (which 404s if no redirect rule matches).
- Draft items are not served to guests via the top-level `/{slug}` route.
- If the module also uses `smeldr.AIIndex(smeldr.AIDoc)`, AI doc is served
  at `GET /{slug}/aidoc` rather than `GET /{prefix}/{slug}/aidoc`.

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
`smeldr.Write(smeldr.Author)` means Author, Editor, and Admin.

### Custom roles

```go
// Create custom roles inline with the hierarchy builder
moderator := smeldr.NewRole("moderator", smeldr.RoleBelow(smeldr.Editor), smeldr.RoleAbove(smeldr.Author))
subscriber := smeldr.NewRole("subscriber", smeldr.RoleBelow(smeldr.Author), smeldr.RoleAbove(smeldr.Guest))

// Use anywhere a Role is accepted
app.Content(&BlogPost{},
    smeldr.At("/posts"),
    smeldr.Auth(smeldr.Read(subscriber), smeldr.Write(moderator)),
)
```

### Auth configuration

```go
// Accept bearer tokens (APIs, mobile clients)
app.Use(smeldr.Authenticate(smeldr.BearerHMAC(secret)))

// Accept cookie sessions (browser apps)
app.Use(smeldr.Authenticate(smeldr.CookieSession("forge_session", secret)))

// Accept both — first match wins
// Use this for apps that serve both a browser UI and an API
app.Use(smeldr.Authenticate(smeldr.AnyAuth(
    smeldr.BearerHMAC(secret),
    smeldr.CookieSession("forge_session", secret),
)))

// Generate a signed token
token := smeldr.SignToken(smeldr.User{
    ID:    "42",
    Name:  "Alice",
    Roles: []smeldr.Role{smeldr.Editor},
}, secret)
```

When multiple auth methods are configured, Forge tries them in order and uses the first that succeeds. A request with a valid Bearer token and no cookie is authenticated as a bearer user. A request with neither is treated as `smeldr.Guest`.

### In hooks and handlers

```go
smeldr.On(smeldr.BeforeCreate, func(ctx smeldr.Context, p *BlogPost) error {
    user := ctx.User()              // smeldr.User{ID, Name, Roles}
    user.HasRole(smeldr.Editor)      // true if Editor or above
    user.Is(smeldr.Author)           // true if exactly Author
    return nil
})
```

---

## Token management

> ✅ **Available**

`TokenStore` adds server-side named bearer tokens with revocation. When
configured, every request validates the token against the database — a revoked
or expired token is rejected immediately, even if the HMAC signature is valid.

### Wiring

```go
app := smeldr.New(smeldr.MustConfig(smeldr.Config{
    BaseURL:    "https://mysite.com",
    Secret:     []byte(os.Getenv("SECRET")),
    DB:         db,
    TokenStore: smeldr.NewTokenStore(db, os.Getenv("SECRET")),
}))
```

Create the `smeldr_tokens` table once before starting:

```sql
CREATE TABLE smeldr_tokens (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    role       TEXT NOT NULL,
    token_hash TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    revoked_at TEXT,
    created_at TEXT NOT NULL
);
```

### Bootstrap

On first startup with an empty `smeldr_tokens` table, Forge auto-creates a
bootstrap admin token and emits it via `slog.Warn`:

```
WARN forge: bootstrap admin token created token=<plaintext>
```

Copy this token immediately. Use it with `forge-cli init` or with the
`create_token` MCP tool to issue long-lived named tokens, then discard it.

**Critical:** a token produced by `smeldr.SignToken` in `main()` is rejected
when `TokenStore` is configured — `VerifyBearerToken` only accepts tokens
that exist in the store. Use `TokenStore.Create` or `smeldr.dev/cli` instead.

`smeldr.VerifyTokenString(token string, secret []byte, store *TokenStore) (User, bool)` —
verifies a raw bearer token string directly, without an `*http.Request`. Identical
verification logic to `VerifyBearerToken`. Use this when calling from a downstream
library (e.g. the `VerifyBearer` callback in `forgeoauth.Config`). Requires v1.25.0+.

### Go API

```go
// Issue a named token — returns plaintext once, never retrievable again
token, err := app.TokenStore().Create(ctx, "alice-author", "author", 365*24*time.Hour)

// List all tokens
records, err := app.TokenStore().List(ctx)

// Revoke by ID
err := app.TokenStore().Revoke(ctx, id)
```

`ErrLastAdmin` (HTTP 409) is returned if you attempt to revoke the last
active admin token. Create a replacement first.

### MCP tools (Admin role required)

| Tool | Description |
|------|-------------|
| `create_token` | Issues a new named token with a given role and TTL |
| `list_tokens` | Lists all tokens with name, role, expiry, revoked status |
| `revoke_token` | Revokes a token by ID — effective immediately |

`create_token` returns the plaintext token once. Copy it immediately —
it cannot be retrieved again. See [AGENTS.md](AGENTS.md) for full details.

---

## SEO & structured data

Define metadata once on your content type. Forge renders it correctly
everywhere — HTML head, JSON-LD, sitemap, RSS, and AI endpoints.

### Head

```go
func (p *BlogPost) Head() smeldr.Head {
    return smeldr.Head{
        Title:       p.Title,
        Description: smeldr.Excerpt(p.Body, 160),
        Author:      p.Author,
        Published:   p.PublishedAt,
        Modified:    p.UpdatedAt,
        Image:       p.Cover,
        Type:        smeldr.Article,
        Canonical:   smeldr.URL("/posts/", p.Slug),
        Breadcrumbs: smeldr.Crumbs(
            smeldr.Crumb("Home",  "/"),
            smeldr.Crumb("Posts", "/posts"),
            smeldr.Crumb(p.Title, "/posts/"+p.Slug),
        ),
    }
}
```

`smeldr.URL(prefix, slug)` builds a root-relative path.
`smeldr.AbsURL(base, path)` joins it with the site base URL to produce
an absolute `https://...` URL — use this when `Head.Canonical` or
`Head.Image.URL` must be absolute:

```go
Canonical: smeldr.AbsURL("https://mysite.com", smeldr.URL("/posts/", p.Slug)),
```

### Advanced: context-aware head with HeadFunc

When `Head()` on your content type is not enough — for example, you need request
context like the site name, a per-request user preference, or a database lookup —
use `HeadFunc`. It receives the full `smeldr.Context` alongside the item.
`HeadFunc` takes priority over `Headable` when both are present.

```go
// HeadFunc wins over the content type's Head() method when set
app.Content(&BlogPost{},
    smeldr.At("/posts"),
    smeldr.HeadFunc(func(ctx smeldr.Context, p *BlogPost) smeldr.Head {
        return smeldr.Head{
            Title: p.Title + " — " + ctx.SiteName(),
        }
    }),
)
```

### List page head with ListHeadFunc

By default, a module's list page (e.g. `/posts`) renders with an empty `<title>`.
Use `ListHeadFunc` to set the title and meta tags for the list page. The function
receives the request context and the full slice of published items.

```go
app.Content(&BlogPost{},
    smeldr.At("/posts"),
    smeldr.ListHeadFunc(func(ctx smeldr.Context, posts []*BlogPost) smeldr.Head {
        return smeldr.Head{
            Title:       "All posts — " + ctx.SiteName(),
            Description: "Browse all articles published on this site.",
        }
    }),
)
```

`ListHeadFunc` is independent of `HeadFunc` — both can be set on the same module.

### Rich result types

```go
smeldr.Article       // blog posts, news articles
smeldr.Product       // e-commerce products
smeldr.FAQPage       // FAQ pages
smeldr.HowTo         // step-by-step guides
smeldr.Event         // events with dates and locations
smeldr.Recipe        // recipes with ingredients
smeldr.Review        // reviews with ratings
smeldr.Organization  // company / about pages
```

### App-level JSON-LD (AppSchema)

Emit site-wide Organisation or WebSite structured data on every page via
`app.SEO`:

```go
app.SEO(&smeldr.AppSchema{
    Type: "Organization",
    Name: "Acme Corp",
    URL:  "https://acme.com",
    Logo: "https://acme.com/logo.png",
})
```

The `<script type="application/ld+json">` block is injected by `smeldr:head`
automatically on every page. Use it for WebSite, Organization, or any other
app-level schema type that does not vary per content item.

### Sitemap

```go
app.SEO(smeldr.SitemapConfig{
    ChangeFreq: smeldr.Weekly,
    Priority:   0.8,
})
```

Each module owns its fragment (e.g. `/posts/sitemap.xml`).
Forge merges all fragments into `/sitemap.xml` automatically.
Sitemaps regenerate on every publish/unpublish — never stale, never on-demand.

### Robots

```go
app.SEO(smeldr.RobotsConfig{
    Disallow:  []string{"/admin"},
    Sitemaps:  true,
    AIScraper: smeldr.AskFirst,  // respectful AI crawler policy
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
    smeldr.At("/posts"),
    smeldr.AIIndex(smeldr.LLMsTxt),
)
```

Enable all three AI endpoints in one call:

```go
app.Content(&BlogPost{},
    smeldr.At("/posts"),
    smeldr.AIIndex(smeldr.LLMsTxt, smeldr.LLMsTxtFull, smeldr.AIDoc),
)
```

Override with a custom template by creating `templates/llms.txt`:

```
# {{.SiteName}}

> {{.Description}}

## Posts
{{smeldr_llms_entries .}}
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

> ✅ **Available** — Open Graph and Twitter Card meta tags are rendered automatically when `smeldr.Social` is added to a module.

```go
app.Content(&BlogPost{},
    smeldr.At("/posts"),
    smeldr.Social(smeldr.OpenGraph, smeldr.TwitterCard),
)
```

Forge reads your content type's `Head()` method and renders the correct
meta tags in `<head>`. No additional configuration.

### What your content type needs

```go
func (p *BlogPost) Head() smeldr.Head {
    return smeldr.Head{
        Title:       p.Title,
        Description: smeldr.Excerpt(p.Body, 160),
        Image:       p.Cover,  // used for og:image and twitter:image

        // Per-platform overrides (optional)
        Social: smeldr.SocialOverrides{
            Twitter: smeldr.TwitterMeta{
                Card:    smeldr.SummaryLargeImage,
                Creator: "@alice",
            },
        },
    }
}
```

```go
app.Content(&BlogPost{},
    smeldr.At("/posts"),
    smeldr.Social(smeldr.OpenGraph, smeldr.TwitterCard),
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

### Site-wide OG and Twitter fallbacks (OGDefaults)

Set a fallback OG image and Twitter handles for pages where the content
type's `Head()` does not provide them:

```go
app.SEO(&smeldr.OGDefaults{
    Image: smeldr.Image{
        URL:    "https://mysite.com/og-default.png",
        Width:  1200,
        Height: 630,
    },
    TwitterSite:    "@mysite",
    TwitterCreator: "@defaultauthor",
})
```

`OGDefaults.Image` is used when `Head().Image.URL` is empty.
`TwitterSite` is emitted on every page. `TwitterCreator` is the fallback
when the content item's own creator is not set.

---

## Cookies & compliance

> ✅ **Available** — typed cookie declarations, consent enforcement, and `/.well-known/cookies.json` are implemented as of Milestone 6.

Forge treats cookies as typed, declared, compliance-aware values.
The category determines which API you can use — enforced at compile time.
It is architecturally impossible to set a non-necessary cookie without consent handling.

### Declaring cookies

```go
var (
    // Necessary — use smeldr.SetCookie, no consent needed
    SessionCookie = smeldr.Cookie{
        Name:     "forge_session",
        Category: smeldr.Necessary,
        Duration: 24 * time.Hour,
        HTTPOnly: true,
        Secure:   true,
        SameSite: http.SameSiteLaxMode,
        Purpose:  "Authenticates the current user session.",
    }

    // Non-necessary — must use smeldr.SetCookieIfConsented
    PreferenceCookie = smeldr.Cookie{
        Name:     "forge_prefs",
        Category: smeldr.Preferences,
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
smeldr.SetCookie(w, r, SessionCookie, sessionID)
value, ok := smeldr.ReadCookie(r, SessionCookie)
smeldr.ClearCookie(w, SessionCookie)

// Non-necessary — silently skipped if user has not consented
set := smeldr.SetCookieIfConsented(w, r, PreferenceCookie, "dark-mode")
```

### Cookie categories

```go
smeldr.Necessary    // session auth, CSRF — never requires consent
smeldr.Preferences  // theme, language — requires consent
smeldr.Analytics    // page views, funnels — requires consent
smeldr.Marketing    // ad targeting — requires consent
```

### Compliance manifest

```go
// Default — public (compliance transparency by design)
app.Cookies(SessionCookie, PreferenceCookie)

// Restricted — require Editor+ to read the manifest
app.Cookies(SessionCookie, PreferenceCookie,
    smeldr.ManifestAuth(smeldr.Editor),
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

## Navigation

> ✅ **Available**

Forge provides a first-class navigation tree with two modes: database-backed
(`NavModeDB`) and code-defined (`NavModeCode`).

### Wiring

```go
// Code-defined — no database required
app := smeldr.New(smeldr.MustConfig(smeldr.Config{
    NavMode: smeldr.NavModeCode,
    // ...
}))

app.Nav(
    smeldr.NavItem{Label: "Home",  Path: "/"},
    smeldr.NavItem{Label: "Blog",  Path: "/posts"},
    smeldr.NavItem{Label: "Docs",  Path: "/docs"},
)
```

```go
// Database-backed — items persisted in smeldr_nav table (auto-created)
app := smeldr.New(smeldr.MustConfig(smeldr.Config{
    NavMode: smeldr.NavModeDB,
    DB:      db,
    // ...
}))
```

### NavItem fields

| Field | Type | Description |
|-------|------|-------------|
| `Label` | string | Display text in nav and breadcrumbs |
| `Path` | string | URL prefix, e.g. `/posts`. Empty path = ghost item |
| `ParentID` | string | ID of parent item; empty for top-level |
| `Module` | string | Forge module table name, e.g. `posts` |
| `Hidden` | bool | Excluded from rendered nav; still accessible in breadcrumbs |
| `Ghost` | bool | Non-clickable; appears in nav unless also Hidden |
| `SortOrder` | int | Display order within parent; lower = first |
| `Children` | []*NavItem | Populated in memory; never persisted |

### In templates

`.Nav` on every module template holds the top-level items with `Children` populated:

```html
<nav>
  {{range .Nav}}
    {{if not .Ghost}}
      <a href="{{.Path}}">{{.Label}}</a>
    {{end}}
  {{end}}
</nav>
```

### MCP nav tools (Editor role, NavModeDB only)

When `NavModeDB` is configured, MCP tools are available for runtime nav
management: create, update, delete, and list nav items.

Obtain the live tree at any time via `app.NavTree()` after `app.Handler()` or
`app.Run()` is called.

---

## Storage

Forge accepts any database connection that satisfies the `smeldr.DB` interface —
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
app := smeldr.New(smeldr.Config{DB: db, ...})
```

**Maximum performance — native pgx connection pool (~2.5× faster)**

For high-throughput production workloads. Uses `pgx`,
a thin adapter that is a separate module from smeldr core.

```go
import (
    forgepgx "smeldr.dev/core/pgx"
    "github.com/jackc/pgx/v5/pgxpool"
)

pool, _ := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
app := smeldr.New(smeldr.Config{DB: forgepgx.Wrap(pool), ...})
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
app := smeldr.New(smeldr.Config{DB: db, ...})
```

Switching between all three approaches requires changing exactly one value
in `smeldr.Config`. Nothing else in your codebase changes.

### Querying

```go
// Single item — returns typed result, maps columns to struct fields
post, err := smeldr.QueryOne[*BlogPost](db,
    "SELECT * FROM posts WHERE slug = $1 AND status = $2",
    slug, smeldr.Published,
)
if errors.Is(err, smeldr.ErrNotFound) {
    // no row — use smeldr.ErrNotFound, not sql.ErrNoRows
}

// List with pagination
opts := smeldr.ListOptions{Page: 1, PerPage: 20, OrderBy: "published_at", Desc: true}

posts, err := smeldr.Query[*BlogPost](db,
    "SELECT * FROM posts WHERE status = $1 ORDER BY published_at DESC LIMIT $2 OFFSET $3",
    smeldr.Published, opts.PerPage, opts.Offset(),
)
```

Forge maps columns to struct fields by `db` tag first, then by field name.
No ORM. No query builder. SQL is the query language — and AI assistants write it extremely well.

```go
type BlogPost struct {
    smeldr.Node
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
    FindAll(ctx context.Context, opts smeldr.ListOptions) ([]T, error)
    Save(ctx context.Context, node T) error
    Delete(ctx context.Context, id string) error
}

// Zero-config in-memory implementation
repo := smeldr.NewMemoryRepo[*BlogPost]()
```

### Streaming with SeqRepository

Both `MemoryRepo[T]` and `SQLRepo[T]` implement the optional
`SeqRepository[T]` interface, which provides a lazy `iter.Seq2` stream
without loading the full result set into memory at once:

```go
if sr, ok := repo.(smeldr.SeqRepository[*BlogPost]); ok {
    for post, err := range sr.Seq(ctx, smeldr.ListOptions{}) {
        if err != nil {
            break
        }
        process(post)
    }
}
```

Use `Seq` for export pipelines, bulk operations, or any case where
loading all items at once would be memory-prohibitive.

### Production SQL repository

> ✅ **Available** — `SQLRepo[T]` is a production-ready `Repository[T]` backed by `smeldr.DB`, implemented as of Milestone 7.

`SQLRepo[T]` derives the table name automatically (`BlogPost` → `blog_posts`) or accepts a `Table()` override:

```go
// Auto-derived table name: blog_posts
repo := smeldr.NewSQLRepo[*BlogPost](db)

// Explicit table name
repo := smeldr.NewSQLRepo[*BlogPost](db, smeldr.Table("posts"))

// T must be a pointer type — NewSQLRepo[*BlogPost] pairs with NewModule((*BlogPost)(nil), ...)
repo := smeldr.NewSQLRepo[*BlogPost](db)

m := smeldr.NewModule((*BlogPost)(nil),
    smeldr.At("/posts"),
    smeldr.Repo(repo),
)

app.Content(m)
```

`SQLRepo` uses `$N` positional placeholders (PostgreSQL / pgx compatible) and upserts via `ON CONFLICT (id) DO UPDATE`.

---

## Middleware

### Global

```go
app.Use(
    smeldr.RequestLogger(),              // structured slog output
    smeldr.Recoverer(),                  // panic → 500, process never crashes
    smeldr.CORS("https://mysite.com"),   // CORS headers
    smeldr.MaxBodySize(1 << 20),         // 1 MB request limit
    smeldr.RateLimit(100, time.Minute),  // 100 req/min per IP
    smeldr.SecurityHeaders(),            // HSTS, CSP, X-Frame-Options, Referrer-Policy
)
```

### Per-module

```go
app.Content(&BlogPost{},
    smeldr.At("/posts"),
    smeldr.Middleware(
        smeldr.InMemoryCache(5*time.Minute),                        // LRU, max 1000 entries
    // smeldr.InMemoryCache(5*time.Minute, smeldr.CacheMaxEntries(500)), // custom limit
        myCustomMiddleware,
    ),
)
```

### Writing middleware

Standard `http.Handler` wrapping — no Forge-specific types required:

```go
func myCustomMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        ctx := smeldr.ContextFrom(w, r)  // access smeldr.Context if needed
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
Accept: text/html         →  HTML template (requires smeldr.Templates)
Accept: text/markdown     →  raw markdown (requires Markdown() method)
Accept: text/plain        →  clean text (always available)
```

### Template convention

```go
app.Content(&BlogPost{},
    smeldr.At("/posts"),
    smeldr.Templates("templates/posts"),        // parsed at startup, fails fast if missing
    // smeldr.TemplatesOptional("templates/posts"), // no startup failure if missing
    // Forge looks for:
    //   templates/posts/list.html  →  GET /posts
    //   templates/posts/show.html  →  GET /posts/{slug}
)
```

### In templates

```html
{{/* show.html */}}
{{template "smeldr:head" .}}

<article>
    <h1>{{.Content.Title}}</h1>
    <p>By {{.Content.Author}} · {{.Content.PublishedAt | smeldr_date}}</p>
    {{.Content.Body | smeldr_markdown}}
</article>

{{/* list.html */}}
{{template "smeldr:head" .}}

{{range .Content}}
<a href="/posts/{{.Slug}}">
    <h2>{{.Title}}</h2>
    <p>{{.Body | smeldr_excerpt 120}}</p>
</a>
{{end}}
```

The `smeldr:head` partial renders everything in `<head>` automatically:
`<title>`, `<meta>`, canonical, Open Graph, Twitter Cards, `twitter:site`,
app-level JSON-LD, JSON-LD, breadcrumbs,
and `<meta name="robots">` based on content Status.

### Template functions

All functions are registered in `TemplateFuncMap` and are available in every module template.

| Function | Input | Output | Use case |
|----------|-------|--------|----------|
| `smeldr_markdown` | Markdown string | `template.HTML` | Body fields authored in Markdown |
| `forge_html` | HTML string | `template.HTML` | Fields that already contain rendered HTML |
| `smeldr_date` | `time.Time` | string | Human-readable date (`2 Jan 2006`) |
| `smeldr_rfc3339` | `time.Time` | string | Machine-readable datetime for `<time datetime>` |
| `smeldr_excerpt` | string, int | string | Truncated plain-text summary |
| `smeldr_meta` | string | `template.HTML` | Escaped `<meta>` attribute value |
| `smeldr_csrf_token` | — | `template.HTML` | Hidden CSRF input field |
| `smeldr_llms_entries` | `.` (template data) | `template.HTML` | Rendered llms.txt entry list |

#### smeldr_markdown

`smeldr_markdown` converts a Markdown string to safe HTML. All content is HTML-escaped
before tag wrapping — no XSS risk from user-authored text.

```html
{{.Content.Body | smeldr_markdown}}
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

| | `smeldr_markdown` | `forge_html` |
|---|---|---|
| Input format | Markdown text | Already-rendered HTML |
| HTML passthrough | Lines starting with `<` only | Entire value verbatim |
| Use case | Body fields authored in Markdown | Pre-rendered content, iframes, migration |

### Template data shape


```go
type TemplateData[T Node] struct {
    Content     T                  // T for show, []T for list
    Head        smeldr.Head         // from Headable.Head() on T, or HeadFunc if provided (HeadFunc takes priority)
    User        smeldr.User         // current user (zero value if Guest)
    Request     *http.Request
    OGDefaults  *smeldr.OGDefaults  // app-level OG/Twitter fallbacks (nil if not configured)
    AppSchema   template.HTML      // pre-rendered app-level JSON-LD block (empty if not configured)
    HeadAssets  *smeldr.HeadAssets  // preconnect/stylesheets/links/scripts (nil if not configured)
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
as `smeldr:head`). Files are loaded in alphabetical order for determinism.

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
// parsed once at startup — includes TemplateFuncMap, smeldr:head, and all partials
homeTpl := app.MustParseTemplate("templates/home.html")

app.Handle("GET /", func(w http.ResponseWriter, r *http.Request) {
    data := smeldr.NewTemplateData[any](ctx, nil, smeldr.Head{Title: "Home"}, "My Site")
    homeTpl.Execute(w, data)
})
```

`MustParseTemplate` panics on error (consistent with `MustConfig`), so
misconfigured templates fail at startup rather than at first request.

### Custom handler with smeldr:head

✅ **Available**

Custom handler data structs can embed `smeldr.PageHead` to gain
`{{template "smeldr:head" .}}` support without using `TemplateData[T]`:

```go
type homeData struct {
    smeldr.PageHead        // promotes Head, OGDefaults, AppSchema, HeadAssets
    Posts      []*Post
    Featured   *Post
}

homeTpl := app.MustParseTemplate("templates/home.html")

app.Handle("GET /", func(w http.ResponseWriter, r *http.Request) {
    data := homeData{
        PageHead: smeldr.PageHead{Head: smeldr.Head{Title: "Home"}},
        Posts:    loadPosts(),
    }
    homeTpl.Execute(w, data)
})
```

In `templates/home.html`:

```html
<head>{{template "smeldr:head" .}}</head>
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
app.Content(smeldr.NewModule((*DocPage)(nil),
    smeldr.At("/docs"),
    smeldr.Repo(docRepo),
    smeldr.Templates("templates/docs"),
    smeldr.ContextFunc(func(ctx smeldr.Context, _ any) (any, error) {
        return docRepo.FindAll(ctx, smeldr.ListOptions{
            Status: []smeldr.Status{smeldr.Published},
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

Inject preconnect hints, stylesheets, favicon links, and scripts into `smeldr:head`
on every page via `app.SEO`:

```go
app.SEO(&smeldr.HeadAssets{
    Preconnect:  []string{"https://fonts.googleapis.com"},
    Stylesheets: []string{
        "https://fonts.googleapis.com/css2?family=Inter&display=swap",
        "/static/app.css",
    },
    Links: []smeldr.HeadLink{
        {Rel: "icon", Type: "image/png", Sizes: "32x32", Href: "/favicon-32.png"},
        {Rel: "apple-touch-icon", Href: "/apple-touch-icon.png"},
    },
    Scripts: []smeldr.ScriptTag{
        {Src: "/static/app.js", Defer: true},
    },
})
```

Assets are emitted in order: preconnect → stylesheets → links → scripts.
Inline script bodies use `template.JS` to opt in to verbatim emission:

```go
smeldr.ScriptTag{Body: template.JS("console.log('Forge')")} // never pass user input here
```

---

## Error handling

Forge uses a typed error hierarchy. Every error knows its HTTP status,
its machine-readable code, and what is safe to show the client.
Internal details are logged — never leaked.

### Sentinel errors

```go
smeldr.ErrNotFound   // 404 — resource does not exist
smeldr.ErrGone       // 410 — resource existed but was intentionally removed
smeldr.ErrForbidden  // 403 — authenticated but insufficient role
smeldr.ErrUnauth     // 401 — not authenticated
smeldr.ErrConflict   // 409 — state conflict (e.g. duplicate slug)
smeldr.ErrLastAdmin  // 409 — attempt to revoke the last active admin token
```

### In hooks and custom handlers

```go
smeldr.On(smeldr.BeforeCreate, func(ctx smeldr.Context, p *BlogPost) error {
    if slugExists(p.Slug) {
        return smeldr.ErrConflict                   // → 409
    }
    if !ctx.User().HasRole(smeldr.Editor) {
        return smeldr.ErrForbidden                  // → 403
    }
    return smeldr.Err("title", "already taken")     // → 422 with field detail
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

## Rate limiting

Forge includes a built-in per-IP token bucket rate limiter.

```go
app.Use(
    smeldr.RateLimit(100, time.Minute),  // 100 requests per IP per minute
)
```

Behind a reverse proxy, supply `TrustedProxy` so the real client IP is read
from the forwarded header rather than the connection address:

```go
app.Use(
    smeldr.RateLimit(100, time.Minute,
        smeldr.TrustedProxy("X-Real-IP"),
    ),
)
```

`ErrTooManyRequests` (HTTP 429) is the typed sentinel returned to the client
when the limit is exceeded. Use it directly in custom middleware when
`smeldr.RateLimit` is insufficient for your logic:

```go
func myRateLimiter(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if limited(r) {
            smeldr.WriteError(w, r, smeldr.ErrTooManyRequests)
            return
        }
        next.ServeHTTP(w, r)
    })
}

app.Use(myRateLimiter)
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
    smeldr.At("/articles"),                              // new prefix
    smeldr.Redirects(smeldr.From("/posts"), "/articles"), // 301 all /posts/* → /articles/*
)

// One-off redirects
app.Redirect("/old-path",  "/new-path", smeldr.Permanent) // 301
app.Redirect("/removed",   "",          smeldr.Gone)       // 410
```

### Optional DB persistence

To persist redirects across restarts, create the `smeldr_redirects` table and
call `Load` at startup:

```sql
CREATE TABLE smeldr_redirects (
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

## MCP integration (smeldr.dev/mcp)

✅ **Available**

`smeldr.dev/mcp` is a separate module that wraps a `smeldr.App` and exposes its
content modules to AI assistants via the [Model Context Protocol](https://modelcontextprotocol.io).
Schema derivation, lifecycle enforcement, and role checks are all automatic —
no configuration beyond `smeldr.MCP(...)` on your existing modules.

```go
import forgemcp "smeldr.dev/mcp"

func main() {
    app := smeldr.New(smeldr.MustConfig(smeldr.Config{
        BaseURL: "https://mysite.com",
        Secret:  []byte(os.Getenv("SECRET")),
    }))
    app.Content(
        smeldr.NewModule((*Post)(nil), smeldr.At("/posts"), smeldr.MCP(smeldr.MCPWrite)),
    )
    forgemcp.New(app).ServeStdio(context.Background(), os.Stdin, os.Stdout)
}
```

For Claude Desktop, Cursor, and SSE remote transport configuration see
the [smeldr.dev/mcp README](https://github.com/smeldr/mcp).

---

## The AI-first design philosophy

Forge is the first Go framework explicitly designed to be maintained by AI assistants.

**Intent over mechanics**  
`smeldr.SEO(smeldr.RichArticle)` — not 40 lines of JSON-LD template code.
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
Every exported symbol: `smeldr.Verb(Noun)` or `smeldr.Noun`.
No abbreviations. No clever names. Predictable, searchable, memorable.

---

## Minimal complete example

```go
package main

import (
    "os"
    "time"

    "smeldr.dev/core"
)

type Article struct {
    smeldr.Node
    Title  string      `forge:"required"         json:"title"`
    Body   string      `forge:"required,min=100"  json:"body"`
    Author string      `forge:"required"         json:"author"`
    Cover  smeldr.Image `                          json:"cover,omitempty"`
}

func (a *Article) Validate() error {
    if a.Status == smeldr.Published && a.Cover.URL == "" {
        return smeldr.Err("cover", "required when publishing")
    }
    return nil
}

func (a *Article) Head() smeldr.Head {
    return smeldr.Head{
        Title:       a.Title,
        Description: smeldr.Excerpt(a.Body, 160),
        Author:      a.Author,
        Image:       a.Cover,
        Type:        smeldr.Article,
        Canonical:   smeldr.URL("/articles/", a.Slug),
    }
}

func (a *Article) Markdown() string { return a.Body }

func main() {
    secret := []byte(os.Getenv("SECRET"))

    app := smeldr.New(smeldr.Config{
        BaseURL: "https://mysite.com",
        Secret:  secret,
    })

    app.Use(
        smeldr.RequestLogger(),
        smeldr.Recoverer(),
        smeldr.SecurityHeaders(),
        smeldr.MaxBodySize(1 << 20),
        smeldr.Authenticate(smeldr.AnyAuth(
            smeldr.BearerHMAC(secret),
            smeldr.CookieSession("session", secret),
        )),
    )

    app.SEO(smeldr.SitemapConfig{ChangeFreq: smeldr.Weekly, Priority: 0.8})
    app.SEO(smeldr.RobotsConfig{AIScraper: smeldr.AskFirst})

    app.Content(smeldr.NewModule((*Article)(nil),
        smeldr.At("/articles"),
        smeldr.Auth(
            smeldr.Read(smeldr.Guest),
            smeldr.Write(smeldr.Author),
            smeldr.Delete(smeldr.Editor),
        ),
        smeldr.Cache(10*time.Minute),
        smeldr.Social(smeldr.OpenGraph, smeldr.TwitterCard),
        smeldr.AIIndex(smeldr.LLMsTxt, smeldr.AIDoc),
        smeldr.Templates("templates/articles"),
        smeldr.On(smeldr.BeforeCreate, func(ctx smeldr.Context, a *Article) error {
            a.Author = ctx.User().Name
            return nil
        }),
    ))

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

## smeldr.dev/media

✅ **Available**

Optional module for file upload, storage, serving, and AI-agent access via MCP.

### Install

```
go get smeldr.dev/media
```

### Wiring

```go
import forgemedia "smeldr.dev/media"

store := forgemedia.NewLocalMediaStore(app)
mediaSrv := forgemedia.Register(app, store)

// Wire into the MCP server so AI agents can upload files.
mcpSrv := forgemcp.New(app, forgemcp.WithModule(mediaSrv))
```

`Register` mounts all four HTTP routes and returns a `*Server` that implements
`smeldr.MCPModule`. Pass it to `forgemcp.WithModule` to expose the MCP tools.

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

The two `smeldr.config` keys below are read automatically by `LocalMediaStore`.
Set them in your `smeldr.config` file or override in Go code via `smeldr.Config`.

| Key | Default | Description |
|-----|---------|-------------|
| `media_path` | `./media` | Directory where uploaded files are stored on disk. |
| `media_max_size` | `5242880` (5 MB) | Maximum upload size in bytes. |

Full reference: [smeldr.dev/media README](https://github.com/smeldr/media)

---

## smeldr.dev/cli

✅ **Available**

`smeldr.dev/cli` is a standalone operator tool for managing a running Forge instance
from the command line. No MCP client required. Install it with:

```bash
go install smeldr.dev/cli@latest
```

### Configuration

`smeldr.dev/cli` reads connection details from a `.forge-cli.env` file in the
current directory:

```
FORGE_URL=https://mysite.com
FORGE_TOKEN=<bearer-token>
```

Use `forge-cli init` to bootstrap a new instance in one step:

```bash
forge-cli init --url https://mysite.com --bootstrap-token <token-from-startup-log>
```

`init` calls `/_health`, issues a long-lived admin token via MCP, and writes
`.forge-cli.env`. Use `--force` to overwrite an existing env file.

### Commands

| Command | Description |
|---------|-------------|
| `init` | Bootstrap a new instance from the startup bootstrap token |
| `status` | Show the site URL and authenticated user |
| `create <type>` | Create a Draft from a YAML-frontmatter file |
| `update <type> <slug>` | Update a content item |
| `publish <type> <slug>` | Publish a Draft |
| `schedule <type> <slug> <datetime>` | Schedule for future publication (RFC3339) |
| `archive <type> <slug>` | Archive a published item |
| `delete <type> <slug>` | Delete an item permanently |
| `token create` | Issue a new named token |
| `token list` | List all tokens |
| `token revoke <id>` | Revoke a token by ID |
| `social <resource> <verb>` | Manage smeldr.dev/social posts, credentials, schedules, and platform config. Requires smeldr.dev/social v0.5.0+. See smeldr.dev/social docs. |

Content files use YAML-subset frontmatter (metadata before `---`, body after):

```
---
title: My Post
author: Alice
---
Body content goes here.
```

Full reference: [smeldr.dev/cli README](https://github.com/smeldr/cli)

---

## Static files

✅ **Available**

`App.Static` serves static assets (CSS, JS, images) from an embedded FS in
production and from disk in development — no boilerplate required.

```go
//go:embed static
var staticFiles embed.FS

func main() {
    app := smeldr.New(smeldr.MustConfig(smeldr.Config{
        BaseURL: "https://mysite.com",
        Secret:  []byte(os.Getenv("SECRET")),
        Dev:     os.Getenv("DEV") == "1",
    }))

    staticFS, _ := fs.Sub(staticFiles, "static")
    app.Static("/static/", staticFS, "static")

    app.Run(":8080")
}
```

### Production mode (`Config.Dev == false`)

Serves from the embedded `prod` FS. Every response gets:

```
Cache-Control: public, max-age=31536000, immutable
```

Assets are cached by browsers and CDNs for one year. Use content-hashed
filenames (e.g. `app.abc123.js`) so cache-busting is automatic on deploy.

### Development mode (`Config.Dev == true`)

Serves from `devDir` on disk. File changes are visible immediately without
rebuilding. A startup log line confirms the active mode:

```
INFO static: serving from disk dir=static
```

`App.Static` panics at startup if `devDir` does not exist when `Dev` is true.

### Enabling dev mode

Two ways to set `Config.Dev`:

**Go code:**
```go
Dev: os.Getenv("DEV") == "1",
```

**smeldr.config file:**
```
dev = true
```

---

## smeldr.config file

Forge reads a `smeldr.config` file from the working directory (or from the path
in the `SMELDR_CONFIG` environment variable) at startup. Fields set in Go code
take precedence over file values — with one deliberate exception: `og_image`
overrides the Go-code `OGDefaults.Image.URL` so operators can update the site
OG image without a rebuild (see below). The file format is one `key = value`
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
| `og_image` | string | — | Site-level OG image URL. **Overrides** `OGDefaults.Image` set in Go code — file value wins. Go-code value is the fallback when key is absent. Root-relative paths (e.g. `/media/og.png`) are resolved against `base_url` at startup. |
| `media_path` | string | `./media` | Upload directory for smeldr.dev/media |
| `media_max_size` | integer | `5242880` | Max upload size in bytes (smeldr.dev/media) |
| `dev` | bool | `false` | Enable development mode (serve static files from disk) |

**Operator flow — updating the OG image without a rebuild:**

```
# 1. Upload the new image via smeldr.dev/media or place it in the media directory.
# 2. Add or update og_image in smeldr.config:
og_image = /media/site-og.png

# 3. Restart the container — no rebuild required.
```

Example:

```
# smeldr.config
base_url = https://mysite.com
https    = true
nav_mode = db
og_image = /media/site-og.png
media_path     = /var/data/media
media_max_size = 10485760
```

### Health endpoint

`GET /_health` returns HTTP 200 with a JSON body containing the framework version
and status. No authentication is required.

```
GET /_health
→ 200 application/json
{"status":"ok","forge":"x.y.z"}
```

When companion modules such as `smeldr.dev/mcp` are linked into the binary, their
versions appear alongside:

```json
{"status":"ok","forge":"x.y.z","forge_mcp":"x.y.z"}
```

The same version data is written to stderr at startup:

```
forge: forge x.y.z, forge_mcp x.y.z
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

## SiteConfig

`SiteConfig` is a built-in singleton content type for site-wide defaults that need
to be configurable via MCP — without code changes or redeployment.

### Fields

| Field | `db` column | Description |
|-------|-------------|-------------|
| `SiteName` | `site_name` | Appended to all page titles. Empty = no suffix. |
| `TitleSeparator` | `title_separator` | Separator between page title and site name, e.g. `" | "`. Default `" | "` at render time if empty. |
| `OGImage` | `og_image` | Relative URL of the global fallback OG image, e.g. `/media/og.png`. |
| `XHandle` | `x_handle` | X (formerly Twitter) site handle, e.g. `@smeldr`. Emitted as `twitter:site` meta tag. |
| `HeadScript` | `head_script` | Raw snippet injected verbatim into `<head>`. For analytics (Google Analytics, GoatCounter, Plausible, etc.) or custom head content. |

### Registration

```go
// 1. Create the table once at startup
if err := smeldr.CreateSiteConfigTable(db); err != nil {
    log.Fatal(err)
}

// 2. Register the module
app.Content(smeldr.NewSiteConfigModule(db))
```

After registration, the MCP tools `create_site_config`, `update_site_config`, and
`get_site_config` are available to Admin-role agents. Configure via MCP:

```json
{
  "site_name":       "Acme Blog",
  "title_separator": " | ",
  "og_image":        "/media/og-default.png",
  "x_handle":        "@acmeblog",
  "head_script":     "<script>/* analytics */</script>"
}
```

### DB schema

```sql
CREATE TABLE IF NOT EXISTS smeldr_site_configs (
    id               TEXT NOT NULL PRIMARY KEY,
    slug             TEXT NOT NULL DEFAULT 'site-config',
    status           TEXT NOT NULL DEFAULT 'draft',
    created_at       DATETIME NOT NULL,
    updated_at       DATETIME NOT NULL,
    published_at     DATETIME,
    site_name        TEXT NOT NULL DEFAULT '',
    title_separator  TEXT NOT NULL DEFAULT '',
    og_image         TEXT NOT NULL DEFAULT '',
    x_handle         TEXT NOT NULL DEFAULT '',
    head_script      TEXT NOT NULL DEFAULT ''
);
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

---

## Signal bus

`App.OnSignal` lets application code subscribe to content lifecycle events.
Multiple handlers per signal are supported; handlers fire in registration order
in a dedicated goroutine, independent of the `afterHook` goroutine.

### `SignalEvent`

```go
type SignalEvent struct {
    Type          string    // Go type name of the content item (e.g. "Post")
    Slug          string
    Title         string    // empty when the type does not implement Titled
    URL           string    // BaseURL + module prefix + "/" + slug
    Timestamp     time.Time
    PreviousState string    // status before the transition; "" on creates
    ActorRole     string    // first role of the acting user, or "guest"
    ActorID       string    // user ID of the acting user, or ""
}
```

### `App.OnSignal`

```go
func (a *App) OnSignal(sig Signal, h func(context.Context, SignalEvent) error) *App
```

Registers `h` to fire whenever `sig` is dispatched. Returns `*App` for chaining.
The handler receives a `context.Context` with a 100 ms timeout, detached from the
originating HTTP request (via `context.WithoutCancel`). Handler errors are logged
at Warn level; subsequent handlers still fire.

```go
app.OnSignal(smeldr.AfterPublish, func(ctx context.Context, ev smeldr.SignalEvent) error {
    log.Printf("published: %s %s", ev.Type, ev.Slug)
    return nil
})
```

`App.OnSignal` must be called before `App.Run`. Handlers registered after `Run`
are silently ignored.

### `OutboundDelivery`

```go
type OutboundDelivery interface {
    Enqueue(ctx context.Context, job OutboundJob) error
}
```

Minimal interface for any engine that needs retry-backed outbound HTTP delivery
without coupling to `WebhookStore`. The unexported `workerPool` (returned by
`App.WebhookPool() WebhookJobQueue`) satisfies this interface.

---

## Outbound webhooks

### `WebhookEndpoint`

```go
type WebhookEndpoint struct {
    ID        string
    Events    []string
    TargetURL string
    Active    bool
    CreatedAt time.Time
}
```

Registered outbound delivery destination. `secretEnc` is unexported — the
plaintext signing secret is returned once by `WebhookStore.Create` and
cannot be retrieved again.

### `WebhookStore`

```go
func NewWebhookStore(db DB, appSecret []byte) *WebhookStore
func (s *WebhookStore) Create(ctx context.Context, targetURL string, events []string) (WebhookEndpoint, string, error)
func (s *WebhookStore) List(ctx context.Context) ([]WebhookEndpoint, error)
func (s *WebhookStore) Delete(ctx context.Context, id string) error
func (s *WebhookStore) EndpointsForEvent(ctx context.Context, event string) ([]WebhookEndpoint, error)
func (s *WebhookStore) DecryptSecret(ep WebhookEndpoint) ([]byte, error)
```

`Create` validates `targetURL` for SSRF safety (HTTPS required, no private or
loopback IPs). Secrets are encrypted with AES-256-GCM using a key derived from
`appSecret`. Rotating `Config.Secret` invalidates all stored secrets.

Required tables — run once at startup:

```sql
CREATE TABLE IF NOT EXISTS smeldr_webhook_endpoints (
    id TEXT PRIMARY KEY,
    events TEXT NOT NULL,
    target_url TEXT NOT NULL,
    secret_enc TEXT NOT NULL,
    active BOOLEAN NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL
);
```

### `WebhookJobQueue`

```go
type WebhookJobQueue interface {
    Enqueue(ctx context.Context, job OutboundJob) error
    ListJobsForEndpoint(ctx context.Context, endpointID string) ([]OutboundJob, error)
    ListDeliveryLogs(ctx context.Context, jobID string) ([]DeliveryLog, error)
}
```

Returned by `App.WebhookPool()`. Use in tests to inspect enqueued jobs.

### `OutboundJob`

```go
type OutboundJob struct {
    ID          string
    EndpointID  string
    TargetURL   string
    SecretEnc   string
    Payload     []byte
    Event       string
    Attempts    int
    NextRetryAt time.Time
    CreatedAt   time.Time
    ExpiresAt   time.Time
    Status      string // "pending" | "dead"
}
```

### `DeliveryLog`

```go
type DeliveryLog struct {
    ID          string
    JobID       string
    AttemptedAt time.Time
    StatusCode  int
    DurationMS  int64
    Error       string
}
```

### App webhook wiring

```go
app.Webhooks(store)           // wire WebhookStore; starts worker pool with App.Run
pool := app.WebhookPool()     // WebhookJobQueue — nil if Webhooks not called
```

`App.Webhooks` must be called before `App.Run`. The worker pool starts and
stops with the server lifecycle. `wireSignalBus` runs at `Run` time so
all `app.Content(m)` and `app.OnSignal(...)` calls are visible before hooks
are wired.

### `Titled` interface

```go
type Titled interface {
    ContentTitle() string
}
```

Implement `ContentTitle() string` on your content type to include the item
title in outbound webhook payloads. Types that do not implement `Titled` emit
an empty title field.

### `AfterSchedule` signal

```go
const AfterSchedule Signal = "after_schedule"
```

Fires after a node transitions to `Scheduled` status via `MCPSchedule`.
Subscribe with `m.On(smeldr.AfterSchedule, ...)` or use it as a webhook
event name suffix (`"mytype.scheduled"`).

### Webhook delivery

- **Signing:** HMAC-SHA256 of `"<unix_ts>.<body>"`. Response header:
  `X-Forge-Signature: sha256=<hex>`.
- **Headers:** `X-Forge-Event`, `X-Forge-Delivery` (UUIDv4), `X-Forge-Timestamp`.
- **Backoff:** `4^attempt` seconds ± 20% jitter, capped at 1 hour.
- **Circuit breaker:** endpoint skipped after 5 consecutive failures for 5 minutes.
- **Dead-letter:** job marked `"dead"` after 7 attempts.

### MCP webhook tools (Admin role)

Available when `App.Webhooks(store)` is configured:

| Tool | Description |
|------|-------------|
| `create_webhook` | HTTPS URL + event list → endpoint + one-time secret |
| `list_webhooks` | All endpoints with delivery stats (no secrets) |
| `delete_webhook` | Remove endpoint by ID |
| `list_webhook_deliveries` | Delivery log for a job ID |
| `retry_webhook` | Re-queue a dead job |

### smeldr.dev/cli webhook commands

```
forge webhook create --url https://example.com/hook --events post.published,post.updated
forge webhook list
forge webhook delete <endpoint-id>
forge webhook deliveries <job-id>
forge webhook retry <job-id>
```

---

## Search engine indexing

Forge does not provide built-in sitemap ping. Google deprecated their ping
endpoint in June 2023, and IndexNow (Bing, Yandex) requires an API key and a
verification file hosted on your site — application-level setup, not framework
responsibility.

Use `App.OnSignal` with `AfterPublish` to call your preferred indexing API.
`SignalEvent.URL` is the fully-qualified canonical URL of the published item.

```go
app.OnSignal(smeldr.AfterPublish, func(ctx context.Context, ev smeldr.SignalEvent) error {
    // IndexNow example
    req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
        "https://api.indexnow.org/indexnow?url="+url.QueryEscape(ev.URL)+
            "&key=YOUR_INDEXNOW_KEY", nil)
    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        return nil // log and move on — never block the signal bus
    }
    defer resp.Body.Close()
    return nil
})
```

`SignalEvent` fields available in the handler:

| Field | Description |
|-------|-------------|
| `URL` | Absolute canonical URL (`Config.BaseURL` + prefix + slug) |
| `Slug` | URL slug of the content item |
| `Type` | Unqualified content type name (e.g. `"Post"`) |
| `Title` | Human-readable title (if type implements `Titled`) |
| `ActorID` | Token UUID of the actor who triggered the publish |

---

## Audit trail

`App.Audit(store AuditStore)` subscribes to `AfterPublish`, `AfterSchedule`,
`AfterArchive`, and `AfterDelete` via the signal bus and persists each transition
to a SQL table. It also mounts `GET /_audit` (Editor role required).

### Setup

```go
// 1. Create the table once at startup
smeldr.CreateAuditTable(db)

// 2. Wire the audit store
app.Audit(smeldr.NewAuditStore(db))
```

DDL (also created by `CreateAuditTable`):

```sql
CREATE TABLE IF NOT EXISTS smeldr_audit_log (
    id           TEXT PRIMARY KEY,
    timestamp    TIMESTAMPTZ NOT NULL,
    signal       TEXT NOT NULL,
    content_type TEXT NOT NULL,
    slug         TEXT NOT NULL,
    actor_id     TEXT NOT NULL,
    actor_role   TEXT NOT NULL,
    prev_state   TEXT NOT NULL
);
```

### Custom store

Implement `AuditStore` to use a different backend:

```go
type AuditStore interface {
    Append(ctx context.Context, r AuditRecord) error
    List(ctx context.Context, f AuditFilter) ([]AuditRecord, error)
}
```

### HTTP endpoint

`GET /_audit` — requires Editor role; returns `[]AuditRecord` as JSON, newest first.

Query parameters:

| Parameter | Format | Description |
|-----------|--------|-------------|
| `from` | RFC3339 | Lower bound (inclusive) on timestamp |
| `to` | RFC3339 | Upper bound (inclusive) on timestamp |
| `type` | string | Filter by content type name (e.g. `"Post"`) |
| `actor` | string | Filter by actor ID |

### AuditRecord fields

| Field | JSON | Description |
|-------|------|-------------|
| `ID` | `id` | Unique record ID (UUIDv7) |
| `Timestamp` | `timestamp` | UTC time of the transition |
| `Signal` | `signal` | One of `after_publish`, `after_schedule`, `after_archive`, `after_delete` |
| `ContentType` | `content_type` | Unqualified Go type name (e.g. `"Post"`) |
| `Slug` | `slug` | URL slug of the content item |
| `ActorID` | `actor_id` | Token UUID of the actor |
| `ActorRole` | `actor_role` | Role at time of action |
| `PreviousState` | `previous_state` | Status before the transition |

### smeldr.dev/cli

```
forge-cli audit list [--from RFC3339] [--to RFC3339] [--type TYPE] [--actor ACTOR]
```

Prints a tab-aligned table to stdout. Requires `FORGE_TOKEN` with Editor role.

### Which signals are recorded

| Signal | Recorded |
|--------|----------|
| `AfterPublish` | ✅ |
| `AfterSchedule` | ✅ |
| `AfterArchive` | ✅ |
| `AfterDelete` | ✅ |
| `AfterCreate` | ❌ — drafts fire on every auto-save; excluded to prevent unbounded noise |
| `AfterUpdate` | ❌ — same reason |

---

## MCP resource subscriptions

Available in `smeldr.dev/mcp` when `App.AddSignalListener` is wired (set up
automatically by `New(app)` in `smeldr.dev/mcp`).

### Subscribe to a resource

Send `resources/subscribe` with `{"uri": "forge:///posts/my-slug"}` to receive
`notifications/resources/updated` events over the SSE connection when that
resource changes.

### Unsubscribe

Send `resources/unsubscribe` with the same URI to stop receiving notifications.

### Server capability

The `initialize` response includes:

```json
{
  "capabilities": {
    "resources": { "subscribe": true, "listChanged": true }
  }
}
```

---

## Draft preview

Forge enforces Published-only visibility by default — drafts return 404 to guests.
Draft preview provides a stateless way to share draft content with reviewers
before publishing, without requiring a login.

### How it works

A preview token is a HMAC-SHA256-signed URL parameter. It encodes the module
prefix, content slug, and expiry timestamp. The server validates the token
inline on each request — no database lookup, no cookie.

The token binds both prefix and slug: a token for `/posts/my-draft` cannot
be replayed on `/docs/my-draft` or on any other slug.

**Archived items are never previewable.** A valid token for an Archived item
falls through to 404, as Archived means explicitly taken offline by an editor.

### `Config.PreviewTokenExpiry`

```go
type Config struct {
    // ...
    PreviewTokenExpiry time.Duration // default 12 h when zero
}
```

### `App.GeneratePreviewToken`

```go
func (a *App) GeneratePreviewToken(prefix, slug string) string
```

Returns a signed preview token valid for `Config.PreviewTokenExpiry` (default 12 h).
Build the full preview URL by combining the token with the app's base URL:

```go
token := app.GeneratePreviewToken("/posts", "my-draft")
previewURL := app.BaseURL() + "/posts/my-draft?preview=" + token
```

### `App.BaseURL`

```go
func (a *App) BaseURL() string
```

Returns `Config.BaseURL` without a trailing slash.

### Module behaviour

When `?preview=<token>` is present on a `GET /{prefix}/{slug}` request:

1. The HMAC signature is verified (constant-time).
2. The expiry timestamp is checked.
3. The token prefix is verified to match this module's prefix.
4. The token slug is verified to match the requested slug.
5. The item's status must be `Draft` or `Scheduled`.
6. If all checks pass: the item is served regardless of Published status.
7. If any check fails: silent fall-through to the normal visibility check (404 for guests).

### smeldr.dev/mcp: `create_preview_url`

Admin-only MCP tool that generates a preview URL.

Parameters:
- `prefix` (string, required) — module prefix including the leading slash, e.g. `"/posts"`
- `slug` (string, required) — content slug, e.g. `"my-draft-post"`

Returns the full preview URL as a string.

### smeldr.dev/cli: `forge preview`

```
forge-cli preview <prefix> <slug>
```

Calls `create_preview_url` via MCP and prints the full preview URL to stdout.
Requires Admin role.

```bash
forge-cli preview /posts my-draft-post
# → https://example.com/posts/my-draft-post?preview=<token>
```

---

## Media upload token

The upload token lets an HTTP client (or AI agent) upload directly to
`POST /media` without a full admin bearer token. The token is short-lived,
stateless, and carries no slug or prefix binding — it authorises any upload
within the TTL.

UploadToken uploads are restricted to image MIME types:
`image/jpeg`, `image/png`, `image/webp`, `image/gif`, `image/avif`.
Bearer-token uploads retain full MIME access.

### `Config.MediaUploadTokenExpiry`

```go
type Config struct {
    // ...
    MediaUploadTokenExpiry time.Duration // default 15 m when zero
}
```

### `App.GenerateUploadToken`

```go
func (a *App) GenerateUploadToken() string
```

Returns a signed upload token valid for `Config.MediaUploadTokenExpiry` (default 15 m).

### `App.ValidateUploadToken`

```go
func (a *App) ValidateUploadToken(token string) error
```

Validates an upload token. Returns `ErrUnauth` on any failure (expired, tampered,
wrong secret). Used by smeldr.dev/media's upload handler.

### Upload flow

```bash
# 1. Generate a token via MCP (Author+ role)
#    Returns: { token, upload_url, expires_in }

# 2. POST directly to /media
curl -X POST https://example.com/media \
  -H "Authorization: UploadToken <token>" \
  -F "file=@hero.jpg" \
  -F "description=Hero image for landing page"
# → 201 { "id": "...", "url": "https://example.com/media/abc123-hero.jpg", ... }
```

### smeldr.dev/mcp: `create_upload_token`

Author+ MCP tool that generates an upload token.

Parameters: none

Returns:
```json
{
  "token": "<signed-token>",
  "upload_url": "https://example.com/media",
  "expires_in": 900
}
```

### smeldr.dev/cli: `forge media`

```
forge-cli media upload <file> [--description <text>]
forge-cli media list [--type image|document|video|audio|other]
forge-cli media delete <id>
```

`upload` POSTs to `/media` with the configured bearer token. `--description` is
required for image files (WCAG 1.1.1).

```bash
forge-cli media upload hero.jpg --description "Hero image"
# → https://example.com/media/abc123-hero.jpg

forge-cli media list --type image
forge-cli media delete <id>
```
