# Smeldr — Full API Reference

Complete API reference for the Smeldr framework.
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
    Title string `smeldr:"required" json:"title"`
    Body  string `smeldr:"required,min=50" json:"body"`
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

Smeldr has six concepts. Learn them once, apply them everywhere.

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

    Title  string      `smeldr:"required"      json:"title"`
    Body   string      `smeldr:"required,min=50" json:"body"`
    Author string      `smeldr:"required"      json:"author"`
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
// Smeldr uses this for SEO, social sharing, and AI indexing.
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

Slug is auto-generated from the first `smeldr:"required"` string field
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
    Title string `smeldr:"required,min=3" db:"title" json:"title"`
    Body  string `smeldr:"required" db:"body" json:"body" smeldr_format:"markdown" smeldr_description:"Write in Markdown. Supports headings, lists, and code blocks."`
    Embed string `db:"embed" json:"embed,omitempty" smeldr_format:"html" smeldr_description:"Raw HTML only. Use for iframes and third-party embeds. Must be trusted content."`
}
```

| Tag | Purpose |
|-----|---------|
| `smeldr:"required,min=N"` | Validation — enforced identically across HTTP, API, and MCP calls |
| `db:"column_name"` | SQLRepo column mapping — omit to use lowercased field name |
| `smeldr_format:"markdown"` | Machine-readable format hint; currently `"markdown"` and `"html"` are supported |
| `smeldr_description:"..."` | Free-text authoring guidance — appears as the field's `description` in the MCP JSON Schema tool definition |

Validation via `smeldr` tags cannot be bypassed — an AI agent calling a write tool
faces the same `required` and `min` rules as a direct HTTP POST.

> **Breaking change in v1.30.0:** the tag key was renamed from `forge:"required"`
> to `smeldr:"required"`. Any content type still using `forge:"required"` will no
> longer have those fields validated until the tag is updated.

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

### What Smeldr enforces automatically

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

Smeldr runs an internal ticker. No external cron needed.

```go
// Schedule via the API
PUT /posts/my-draft
{
  "status":       "scheduled",
  "scheduled_at": "2025-09-01T09:00:00Z"
}
```

At `scheduled_at`, Smeldr automatically transitions to `Published`,
sets `PublishedAt`, fires `AfterPublish` signals,
regenerates the sitemap, and adds the item to the RSS feed.

---

## Routing variants

✅ **Available**

Smeldr modules expose a list endpoint and a per-item show endpoint by default.
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

When multiple auth methods are configured, Smeldr tries them in order and uses the first that succeeds. A request with a valid Bearer token and no cookie is authenticated as a bearer user. A request with neither is treated as `smeldr.Guest`.

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

On first startup with an empty `smeldr_tokens` table, Smeldr auto-creates a
bootstrap admin token and emits it via `slog.Warn`:

```
WARN smeldr: bootstrap admin token created token=<plaintext>
```

Copy this token immediately. Use it with `smeldr-cli init` or with the
`create_token` MCP tool to issue long-lived named tokens, then discard it.

**Critical:** a token produced by `smeldr.SignToken` in `main()` is rejected
when `TokenStore` is configured — `VerifyBearerToken` only accepts tokens
that exist in the store. Use `TokenStore.Create` or `smeldr.dev/cli` instead.

`smeldr.VerifyTokenString(token string, secret []byte, store *TokenStore) (User, bool)` —
verifies a raw bearer token string directly, without an `*http.Request`. Identical
verification logic to `VerifyBearerToken`. Use this when calling from a downstream
library (e.g. the `VerifyBearer` callback in `oauth.Config`). Requires v1.25.0+.

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

Define metadata once on your content type. Smeldr renders it correctly
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
Smeldr merges all fragments into `/sitemap.xml` automatically.
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

Smeldr is the first Go framework to treat AI indexing as a first-class feature.

### llms.txt

Smeldr generates `/llms.txt` automatically from all registered modules.
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

No configuration. Smeldr handles negotiation automatically.

---

## Social sharing

> ✅ **Available** — Open Graph and Twitter Card meta tags are rendered automatically when `smeldr.Social` is added to a module.

```go
app.Content(&BlogPost{},
    smeldr.At("/posts"),
    smeldr.Social(smeldr.OpenGraph, smeldr.TwitterCard),
)
```

Smeldr reads your content type's `Head()` method and renders the correct
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

Smeldr renders in `<head>`:

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

Smeldr treats cookies as typed, declared, compliance-aware values.
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

Smeldr serves a live manifest at `GET /.well-known/cookies.json`.
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

Smeldr provides a first-class navigation tree with two modes: database-backed
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
| `Module` | string | Smeldr module table name, e.g. `posts` |
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

Smeldr accepts any database connection that satisfies the `smeldr.DB` interface —
which `*sql.DB` and any pgx adapter already implement.
You write SQL. Smeldr handles scanning and mapping.

Smeldr core has zero dependencies. The driver is always your choice.
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
Swap the driver without changing any other Smeldr code.

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

Smeldr maps columns to struct fields by `db` tag first, then by field name.
No ORM. No query builder. SQL is the query language — and AI assistants write it extremely well.

```go
type BlogPost struct {
    smeldr.Node
    Title  string `smeldr:"required" db:"title"  json:"title"`
    Body   string `smeldr:"required" db:"body"   json:"body"`
    Author string `smeldr:"required" db:"author" json:"author"`
}
// db tag controls column mapping — omit it and Smeldr uses the field name lowercased
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

Standard `http.Handler` wrapping — no Smeldr-specific types required:

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

Smeldr selects the response format from the `Accept` header.
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
    // Smeldr looks for:
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

Security model: Smeldr is self-hosted; content authors are trusted (same role system
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
    RawHead: template.HTML(`<link rel="preload" href="/fonts/inter.woff2" as="font" crossorigin>`),
})
```

Assets are emitted in order: preconnect → stylesheets → links → scripts → `RawHead`.
Inline script bodies use `template.JS` to opt in to verbatim emission:

```go
smeldr.ScriptTag{Body: template.JS("console.log('Smeldr')")} // never pass user input here
```

`RawHead` accepts any raw HTML as `template.HTML` — use it for analytics snippets,
preload hints, or anything that does not fit the structured fields. The caller is
responsible for safety. Zero value is a no-op.

---

## Error handling

Smeldr uses a typed error hierarchy. Every error knows its HTTP status,
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

Smeldr includes a built-in per-IP token bucket rate limiter.

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

> ✅ **Available** — manual redirects (`app.Redirect`), prefix rewrites (`Redirects(From(...))`), 410 Gone, chain collapse, `/.well-known/redirects.json`, and runtime MCP/CLI management are implemented.

Smeldr automatically tracks every URL a piece of content has ever had.
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

### Manual redirects (code-only)

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

### DB-backed redirect management

Call `App.Redirects(db)` once at startup to activate database-backed
management. It creates the `smeldr_redirects` table if it does not exist,
loads any saved entries into the in-memory store, and enables the MCP
and CLI tools:

```go
if err := app.Redirects(db); err != nil {
    log.Fatal(err)
}
```

`CreateRedirectsTable(db DB) error` is also exported for migration tools and
tests that need the table without calling `App.Redirects`.

**`RedirectStore.Delete(from string)`** removes an entry from the in-memory
store. Pair with `RedirectStore.Remove(ctx, db, from)` to also delete from
the database (this is what `delete_redirect` does internally).

### MCP redirect tools (Editor role)

Available when `App.Redirects(db)` has been called (see smeldr.dev/mcp):

| Tool | Description |
|------|-------------|
| `create_redirect` | Create or upsert a redirect rule. `from` (required, must start with `/`), `to`, `code` (301/302/410, default 301), `is_prefix` (bool). |
| `list_redirects` | List all registered redirect rules (code-registered and DB-saved). |
| `delete_redirect` | Delete a redirect rule by `from` path. |

Changes take effect immediately without a server restart.

### CLI redirect commands (Editor role)

```
smeldr-cli redirect list                                  # aligned table output
smeldr-cli redirect list --json                          # raw JSON
smeldr-cli redirect create --from /old --to /new         # 301
smeldr-cli redirect create --from /gone --code 410       # 410 Gone
smeldr-cli redirect create --from /posts --to /articles --prefix  # prefix rewrite
smeldr-cli redirect delete /old-path
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
import mcp "smeldr.dev/mcp"

func main() {
    app := smeldr.New(smeldr.MustConfig(smeldr.Config{
        BaseURL: "https://mysite.com",
        Secret:  []byte(os.Getenv("SECRET")),
    }))
    app.Content(
        smeldr.NewModule((*Post)(nil), smeldr.At("/posts"), smeldr.MCP(smeldr.MCPWrite)),
    )
    mcp.New(app).ServeStdio(context.Background(), os.Stdin, os.Stdout)
}
```

For Claude Desktop, Cursor, and SSE remote transport configuration see
the [smeldr.dev/mcp README](https://github.com/smeldr/mcp).

### Server.Register — mount all MCP+OAuth routes in one call (v1.13.0+)

`Register(app *smeldr.App)` replaces the pattern of calling `app.Handle` for
each MCP/OAuth endpoint individually:

```go
mcpSrv := mcp.New(app, mcp.WithOAuth(oauthSrv))
mcpSrv.Register(app) // mounts GET/POST /mcp, POST /mcp/message,
                     // /.well-known/oauth-protected-resource,
                     // and all OAuth routes when WithOAuth is configured
```

Routes registered:

| Route | Always | WithOAuth only |
|-------|--------|---------------|
| `GET /mcp` | ✓ | |
| `POST /mcp` | ✓ | |
| `POST /mcp/message` | ✓ | |
| `GET /.well-known/oauth-protected-resource` | ✓ | |
| `GET /.well-known/oauth-authorization-server` | | ✓ |
| `GET /oauth/authorize` | | ✓ |
| `POST /oauth/authorize` | | ✓ |
| `POST /oauth/token` | | ✓ |
| `POST /oauth/revoke` | | ✓ |

The existing `Handler()` method is unchanged for non-Smeldr embeddings.

### Block system tools — `WithBlocks` (T32)

`mcp.New(app, mcp.WithBlocks())` exposes the block-system MCP tools.
They operate on the `smeldr_dynamic_content` and `smeldr_content_edges` tables
(create them with `smeldr.CreateBlockTables(db)`). `WithBlocks` reads the App's
`Config.DB`; with no DB the tools are not exposed. Blocks are addressed by **ID**
and are not browsable resources — the read surface is `get_node` / `list_nodes`.

```go
smeldr.CreateBlockTables(db)
mcpSrv := mcp.New(app, mcp.WithBlocks())
```

Generic node lifecycle (Author role):

| Tool | Description |
|------|-------------|
| `create_node` | Create a Draft block. Args: `type_name` (req), `fields` (object). Returns `{id, type_name, status, slug}`. |
| `update_node` | Merge `fields` onto a block by `id` (absent keys preserved; `type_name` immutable). |
| `get_node` | Fetch a block by `id` at any status. |
| `list_nodes` | List blocks; optional `type_name` and `status` filters. |
| `publish_node` | Publish a block by `id` (idempotent). |
| `archive_node` | Archive a block by `id`. |

Composition (Editor role) — assemble blocks into pages and collections:

| Tool | Description |
|------|-------------|
| `add_section` | Append `child_id` as the last section of `parent_id`. |
| `reorder_sections` | Set a page's section order to `ordered_child_ids`. |
| `remove_section` | Remove a section edge (`parent_id`, `child_id`). |
| `add_item` | Append `child_id` as the last item of a collection `parent_id`. |
| `reorder_items` | Set a collection's item order to `ordered_child_ids`. |
| `remove_item` | Remove an item edge (`parent_id`, `child_id`). |

`add_section` / `add_item` derive `parent_type` / `child_type` from the stored
blocks — pass only the IDs.

---

## The AI-first design philosophy

Smeldr is the first Go framework explicitly designed to be maintained by AI assistants.

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
    Title  string      `smeldr:"required"         json:"title"`
    Body   string      `smeldr:"required,min=100"  json:"body"`
    Author string      `smeldr:"required"         json:"author"`
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
import media "smeldr.dev/media"

store := media.NewLocalMediaStore(app)
mediaSrv := media.Register(app, store)

// Wire into the MCP server so AI agents can upload files.
mcpSrv := mcp.New(app, mcp.WithModule(mediaSrv))
```

`Register` mounts all four HTTP routes and returns a `*Server` that implements
`smeldr.MCPModule`. Pass it to `mcp.WithModule` to expose the MCP tools.

A database is required. Call `media.CreateMediaTable(db)` once to create
the `forge_media` table, or run the migration manually.

### HTTP endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/media` | Author+ | Upload a file. Multipart body with `file` and `description` fields. Returns `{"id","url","media_type","mime_type"}` (201). Image uploads require a non-empty `description` (WCAG 1.1.1). |
| `GET` | `/media/{filename}` | Public | Serve a stored file. No authentication required. |
| `GET` | `/media` | Editor+ | List all records. Optional `?type=image\|document\|video\|other` filter. |
| `DELETE` | `/media/{id}` | Editor+ | Delete a record and its stored file. Returns 204 No Content. |

### MCP tools

Exposed when `mcp.WithModule(mediaSrv)` is wired. TypeName is `"File"`.

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

`smeldr.dev/cli` is a standalone operator tool for managing a running Smeldr instance
from the command line. No MCP client required. Install it with:

```bash
go install smeldr.dev/cli/cmd/smeldr-cli@latest
```

### Configuration

`smeldr.dev/cli` reads connection details from `.smeldr-cli.env` in the
current directory (legacy: `.forge-cli.env` is still read if present):

```
SMELDR_URL=https://mysite.com
SMELDR_TOKEN=<bearer-token>
```

Legacy `FORGE_URL` / `FORGE_TOKEN` / `FORGE_MCP_URL` are still accepted as fallbacks.

Use `smeldr-cli init` to bootstrap a new instance in one step:

```bash
smeldr-cli init --url https://mysite.com --bootstrap-token <token-from-startup-log>
```

`init` calls `/_health`, issues a long-lived admin token via MCP, and writes
`.smeldr-cli.env`. Use `--force` to overwrite an existing env file.

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
| `block node <verb>` | Manage blocks: `create`/`update`/`get`/`list`/`publish`/`archive` (Author). `list` prints a table; `--json` for raw. Full CLI/MCP parity with the block tools (v0.10.0+, T32). |
| `block section <verb>` / `block item <verb>` | Compose pages/collections: `add`/`reorder`/`remove` (Editor). |

Block `Fields` keys are case-sensitive PascalCase — `block node create/update` take
`--field K=V` (case preserved) and/or `--fields '<json>'`.

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

Smeldr reads a `smeldr.config` file from the working directory (or from the path
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

### Stats endpoint

`GET /_stats` returns HTTP 200 with a JSON body containing per-type item counts
across all registered content modules. **Admin role required** (bearer token).

```go
app.StatsHandler()
// GET /_stats (Authorization: Bearer <admin-token>) → 200 application/json
```

**Response shape:**

```json
{
  "content": [
    {
      "type": "Post",
      "prefix": "/posts",
      "counts": { "draft": 3, "published": 12, "scheduled": 1, "archived": 5 }
    }
  ],
  "generated_at": "2026-06-04T12:00:00Z"
}
```

When external stats providers are registered (see below), an `external` key is
added:

```json
{
  "content": [...],
  "external": {
    "media": { "total_files": 42, "total_size_bytes": 8388608 }
  },
  "generated_at": "..."
}
```

**`App.Stats(ctx context.Context) (SiteStats, error)`** — the underlying
aggregation method. Iterates `statsCollectors` (all registered modules) and all
external providers. Provider errors are logged at Warn and never cause the whole
call to fail.

**`App.RegisterStatsProvider(p StatsExtProvider)`** — registers an external
stats contributor. The `StatsExtProvider` interface:

```go
type StatsExtProvider interface {
    StatsKey() string                                        // key in SiteStats.External
    ProvideStats(ctx context.Context) (map[string]any, error)
}
```

Use this to contribute media statistics from `smeldr.dev/media` (or any external
module) without creating an import cycle:

```go
// In main.go — after registering the media module:
app.RegisterStatsProvider(mediaSrv.StatsProvider())
```

`smeldr.dev/media` will implement `StatsProvider()` in a forthcoming minor release.

### Log capture endpoint

`App.CaptureLogs` installs an opt-in, in-memory ring buffer that captures recent
log records, and mounts `GET /_logs` (**Admin role required**, bearer token) to
read them. It is a **live-debugging facility for a running instance** — designed to
work over plain HTTP even when MCP is unavailable. It is **not** log storage:
entries are in-memory only and lost on restart; stderr remains the durable path.

```go
app.CaptureLogs()                              // ring of 500, WARN and above
app.CaptureLogs(
    smeldr.WithLogCapacity(1000),              // retain the most recent 1000 records
    smeldr.WithLogLevel(slog.LevelInfo),       // capture INFO and above
)
// GET /_logs (Authorization: Bearer <admin-token>) → 200 application/json
```

`CaptureLogs` tees: every record still reaches the existing handler (typically
stderr) **and** records at or above the capture level are stored in the ring. It
never narrows what stderr receives.

**Ordering constraint:** `CaptureLogs` wraps `slog.Default().Handler()` at the
moment it is called and then calls `slog.SetDefault`. Call it **after** any
application-side `slog.SetDefault` of your own.

**Zero-config behaviour:** when no custom handler has been configured, `CaptureLogs`
forwards to a text handler on `os.Stderr` rather than wrapping slog's built-in
handler. Wrapping the built-in handler would route the standard `log` package back
into itself (an infinite re-entrant loop), because `slog.SetDefault` also repoints
the `log` package. Apps that configure their own handler are wrapped unchanged.

**Options:**
- `WithLogCapacity(n int)` — max entries retained (default **500**; `n <= 0` keeps the default).
- `WithLogLevel(level slog.Level)` — minimum level captured (default **`slog.LevelWarn`**).

**Response shape** (entries newest-first; `entries` is always an array, never null):

```json
{
  "capacity": 500,
  "count": 2,
  "dropped": 0,
  "entries": [
    {"time": "2026-06-05T12:00:01Z", "level": "ERROR", "msg": "db query failed", "attrs": {"code": 500}, "seq": 42},
    {"time": "2026-06-05T12:00:00Z", "level": "WARN",  "msg": "slow request",    "seq": 41}
  ]
}
```

`capacity` is the ring size; `dropped` counts entries evicted by overwrite since
start (non-zero means older entries are gone); `count` is the number returned.

**Query parameters:**
- `level` — minimum level to return (`debug`|`info`|`warn`|`error`), inclusive.
- `limit` — return at most the N most recent matching entries.
- `since` — RFC3339 timestamp; return only entries strictly after it.

A malformed query parameter returns **400**; a missing/invalid token **401**; an
authenticated non-Admin **403**. When `CaptureLogs` has not been called, the route
is not registered and returns **404**.

`smeldr-cli logs` calls `GET /_logs` directly over HTTP (not via MCP) so it works
when MCP is down; it ships in `smeldr.dev/cli`.

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

- **Signing:** HMAC-SHA256 of `"<unix_ts>.<body>"`. Preferred header:
  `X-Smeldr-Signature: sha256=<hex>` (legacy `X-Forge-Signature` also emitted — T87 removes it).
- **Headers (preferred):** `X-Smeldr-Event`, `X-Smeldr-Delivery` (UUIDv4), `X-Smeldr-Timestamp`.
  Legacy `X-Forge-*` equivalents are emitted alongside during the deprecation window.
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

Smeldr does not provide built-in sitemap ping. Google deprecated their ping
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
smeldr-cli audit list [--from RFC3339] [--to RFC3339] [--type TYPE] [--actor ACTOR]
```

Prints a tab-aligned table to stdout. Requires `SMELDR_TOKEN` (or legacy `FORGE_TOKEN`) with Editor role.

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

Send `resources/subscribe` with `{"uri": "smeldr://posts/my-slug"}` to receive
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

Smeldr enforces Published-only visibility by default — drafts return 404 to guests.
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
smeldr-cli preview <prefix> <slug>
```

Calls `create_preview_url` via MCP and prints the full preview URL to stdout.
Requires Admin role.

```bash
smeldr-cli preview /posts my-draft-post
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
smeldr-cli media upload <file> [--description <text>]
smeldr-cli media list [--type image|document|video|audio|other]
smeldr-cli media delete <id>
```

`upload` POSTs to `/media` with the configured bearer token. `--description` is
required for image files (WCAG 1.1.1).

```bash
smeldr-cli media upload hero.jpg --description "Hero image"
# → https://example.com/media/abc123-hero.jpg

smeldr-cli media list --type image
smeldr-cli media delete <id>
```

## Block data foundation

The block system stores all block types as rows in one generic table and records
composition (which parent contains which child, in what order) in one edge table.
This is the data layer only — MCP tools, rendering, and schema seeding build on
top of it.

### `CreateBlockTables`

```go
smeldr.CreateBlockTables(db)
```

Creates `smeldr_dynamic_content` and `smeldr_content_edges` (plus the
`(parent_id, sort_order)` index) if they do not exist. Idempotent — safe to call
on every boot. This is the single grouped creation function for the block schema;
there is no per-table creator and no versioned migration runner.

```sql
CREATE TABLE IF NOT EXISTS smeldr_dynamic_content (
    id           TEXT NOT NULL PRIMARY KEY,
    slug         TEXT NOT NULL DEFAULT '',
    type_name    TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'draft',
    fields       TEXT NOT NULL DEFAULT '{}',
    created_at   DATETIME NOT NULL,
    updated_at   DATETIME NOT NULL,
    scheduled_at DATETIME,
    published_at DATETIME
);

CREATE TABLE IF NOT EXISTS smeldr_content_edges (
    id          TEXT NOT NULL PRIMARY KEY,
    parent_id   TEXT NOT NULL,
    parent_type TEXT NOT NULL,
    child_id    TEXT NOT NULL,
    child_type  TEXT NOT NULL,
    sort_order  INTEGER NOT NULL,
    is_shared   INTEGER NOT NULL DEFAULT 0,
    edge_role   TEXT NOT NULL DEFAULT 'section'
);
```

### `DynamicNode`

One Go type serves every block type. `TypeName` is the discriminator
(`"content_block"`, `"hero"`, …); `Fields` holds the type-specific data as raw
JSON. `DynamicNode` embeds `Node`, so it carries the standard lifecycle. Blocks
are addressed by ID — `Slug` may be empty.

```go
type DynamicNode struct {
    smeldr.Node
    TypeName string          `db:"type_name" json:"type_name"`
    Fields   json.RawMessage `db:"fields"     json:"fields"`
}

repo := smeldr.NewDynamicContentRepo(db) // *SQLRepo[*DynamicNode]
node := &smeldr.DynamicNode{
    Node:     smeldr.Node{ID: smeldr.NewID(), Status: smeldr.Draft},
    TypeName: "content_block",
    Fields:   json.RawMessage(`{"title":"Hello","body":"World"}`),
}
repo.Save(ctx, node)
```

`NewDynamicContentRepo(db)` returns a `SQLRepo` bound to `smeldr_dynamic_content`
(the table name cannot be derived from the type), with the full
`FindByID` / `FindBySlug` / `FindAll` / `Save` / `Delete` / `Seq` surface.

### `ContentEdge` and `ContentEdgeStore`

A `ContentEdge` records that one parent contains one child at one position. The
same table and store serve both page→block (`edge_role` `"section"`) and
collection→item (`"item"`) composition.

```go
type ContentEdge struct {
    ID         string
    ParentID   string
    ParentType string
    ChildID    string
    ChildType  string
    SortOrder  int
    IsShared   bool
    EdgeRole   string // defaults to "section"
}

edges := smeldr.NewContentEdgeStore(db)
```

| Method | Effect |
|--------|--------|
| `AddChild(ctx, ContentEdge) (ContentEdge, error)` | Appends the child as the parent's last entry. Assigns `ID` and `SortOrder`; defaults `EdgeRole` to `"section"`. |
| `Children(ctx, parentID) ([]ContentEdge, error)` | The parent's child list, ordered by `SortOrder`, in one query. Empty parent → empty slice. |
| `ChildrenOf(ctx, parentIDs) ([]ContentEdge, error)` | Children of many parents in one batched `IN()` query — the render engine's level-load path (no N+1). |
| `RemoveChild(ctx, parentID, childID) error` | Deletes the edge. `ErrNotFound` if absent. |
| `Reorder(ctx, parentID, orderedChildIDs) error` | Sets `SortOrder` to match the given order in one atomic statement. `ErrBadRequest` if empty. |

### `App.ServeBlocks` — block rendering engine

```go
r, err := app.ServeBlocks("templates/blocks")
html, err := r.Render(ctx, "page", pageID)
```

`ServeBlocks(dir)` ensures the block tables, parses one
`templates/blocks/<type_name>.html` per block type, and returns a `*BlockRenderer`.
`Render(ctx, pageType, pageID)` assembles the page's ordered, **Published** section
blocks (and each collection's items) into HTML.

- **Batched, no N+1** — one `IN()` query for each depth level's blocks plus one
  `ChildrenOf` for their item edges; the tree is assembled in memory.
- **Cycle-safe** — a visited-set on the render path plus a `maxDepth` of 16.
- **Graceful** — an unpublished, missing, dangling, or malformed block, a missing
  `<type_name>.html`, or a template execution error is skipped (and logged), never
  failing the whole page. `Render` returns an error only on a database fault.

Each block template receives a `map[string]any`: `.ID` / `.Slug` / `.Status`, the
block's `Fields` promoted to top level (**PascalCase keys** — `.Title`, `.Body`,
`.Headline`), `.AnchorID`, and for collections `.Layout` + `.Items` (each item
pre-rendered). Markdown fields arrive already rendered to safe HTML; output them
directly (`{{ .Body }}`) — do not call a markdown helper in the template.

Block `Fields` **must use PascalCase keys** (matching the block-system type tables)
or the template accessors will not bind.

**Reference fields.** A field named `{Name}ID` is resolved to a `.{Name}`
sub-object holding the referenced block's full template data. The built-in mapping
is `ImageID` → `.Image` on `content_block`, `contact_card`, and `hero`. `.Image`
carries `.MediaURL`, `.AltText`, `.Title`, `.Caption` (Caption markdown-rendered):

```html
{{ with .Image }}<img src="{{ .MediaURL }}" alt="{{ .AltText }}">{{ end }}
```

Resolution is **Published-only** and **`{{ with }}`-guarded**: an absent,
unpublished, or dangling reference produces no `.Image` key, so the guard renders
nothing — no error. Referenced blocks are batch-loaded in one query (no N+1).

### `ContentTypeSchema` and `SchemaField`

`ContentTypeSchema` is the schema descriptor for a block or content type. It is
stored in `smeldr_content_type_schemas` and read by `SchemaStore`. `SchemaField`
describes one field within a schema.

```go
type ContentTypeSchema struct {
    ID        string
    TypeName  string
    Kind      string // "block" | "content" — discriminates block types from content types
    Fields    []SchemaField
    URLPrefix string // operator-set public URL prefix; empty = admin-only, no public routes; must start with "/"
}

type SchemaField struct {
    Name     string
    Type     string   // "string" | "integer" | "boolean" | "array" | "object"
    Required bool
    Format   string   // "url" etc. — format hint for validation
    Role     string   // "title" | "description" | "og_image" | "body" | "summary"
    Relation string   // non-empty = future T06 edge-backed relation placeholder
}
```

`Role` is a semantic seam read by T72 (head/SEO) and the ContentList renderer
(summary cards). At most one field per schema may carry each role.

### `CreateSchemaTable` and `MigrateSchemaKindColumn`

```go
smeldr.CreateSchemaTable(db)      // creates smeldr_content_type_schemas (idempotent)
smeldr.MigrateSchemaKindColumn(db) // adds kind column to existing databases (idempotent)
```

`CreateSchemaTable` is called internally; call it explicitly only when bootstrapping
a database without going through `App`. `MigrateSchemaKindColumn` is safe to call
on every boot — it probes `PRAGMA table_info` and is a no-op when the column already
exists.

### `ValidateFields` — schema-driven field validation

```go
err := smeldr.ValidateFields(schema, fields)
```

Validates `fields` (a `map[string]any`) against `schema`. Returns an error when:
- A required field is missing or `null`.
- A field's value type does not match the schema (`"string"`, `"integer"`, etc.).
- A string field with `Format: "url"` contains a non-URL value (relative `/path`
  strings are accepted as internal links).
- Two schema fields share the same non-empty `Role`.

Unschematised types (schema is nil) are not validated — backwards-compatible.
`ValidateBlockFields` is retained as an alias.

### `ContentTypeRegistry` and `TypeDescriptor`

`ContentTypeRegistry` is a concurrency-safe name/prefix registry owned by `App`.
It is the linchpin shared by the ContentList block resolver (T96) and the future
relation endpoint system (T06).

```go
type TypeDescriptor struct {
    Name   string
    Prefix string             // URL prefix (e.g. "/posts")
    Schema *ContentTypeSchema // nil for compiled modules in this increment
    Kind   string             // "block" | "content"
    Fetch  func(ctx context.Context, opts ListOptions) ([]map[string]any, error)
}
```

`Fetch` is set automatically at `App.Content()` time for any `Module[T]` that
implements `ContentLister` (i.e. every compiled module). It returns Published items
as type-erased `map[string]any` for the ContentList block resolver.

| Method | Effect |
|--------|--------|
| `Register(d *TypeDescriptor)` | Adds descriptor. Panics on duplicate name or prefix. |
| `RegisterPrefix(prefix, name string)` | Adds prefix alias for existing type. Idempotent for same name; panics if prefix claimed by different type. |
| `Lookup(name string) *TypeDescriptor` | Returns descriptor or nil. |
| `LookupByPrefix(prefix string) *TypeDescriptor` | Strips leading `/` before lookup. |
| `All() []*TypeDescriptor` | Snapshot of all registered descriptors. |

Compiled modules are auto-registered at `App.Content()` time with a soft-dup
guard: the first `Content()` call for a type name wins; subsequent calls for the
same `TypeName` call `RegisterPrefix` instead.

### `App.TypeRegistry`

```go
reg := app.TypeRegistry() // *ContentTypeRegistry
```

Returns the app's type registry. Use it to register runtime-defined types (T104
increment 2+) or to look up a type by name or prefix from outside the framework.

### ContentList block resolver — `ContentLister` interface

The `content_list` block type supports dynamic item injection via the
`ContentTypeRegistry`. When `BlockRenderer.Render` encounters a `content_list`
block, it:

1. Reads `ContentType` from the block's `Fields` — this is the **type_name**
   (e.g. `"recipe"`, `"post"`), not the URL prefix.
2. Calls `registry.Lookup(ContentType)` to find the `TypeDescriptor` by type_name.
3. If `desc.Fetch != nil`, calls `desc.Fetch(ctx, opts)` where `opts` is derived
   from the block's `Limit` and `Page` fields.
4. Sets `.Items` to the returned `[]map[string]any` for the template.

Graceful skips (no items, no error): unknown `ContentType`, nil `Fetch`, empty
`ContentType`, or a `Fetch` that returns an error (logged via `slog`).

**Breaking change (A154):** `ContentType` block field must hold the **type_name**
(lowercase snake_case, e.g. `"recipe"`) — not the URL prefix (e.g. `"/recipes"`).
Existing `content_list` blocks using a URL prefix value must be updated.

```go
// ContentLister is implemented by Module[T]. Fetch is wired at App.Content() time.
type ContentLister interface {
    listPublished(ctx context.Context, opts ListOptions) ([]map[string]any, error)
}
```

**Template contract for `content_list`:** `.Items` is `[]map[string]any` (unlike
collection blocks where `.Items` is `[]template.HTML`). Access fields with
`{{index . "Title"}}` or dot notation:

```html
{{range .Items}}
  <article>
    <h2>{{index . "Title"}}</h2>
    <p>{{index . "Excerpt"}}</p>
  </article>
{{end}}
```

`Limit` maps to `ListOptions.PerPage`; `Page` maps to `ListOptions.Page`. Both
fields are optional — omit for no pagination.

---

## Dynamic content

Runtime-defined content types are created with `App.DefineContentType` and served
with `App.ServeDynamicContent`. Public URL routing is operator-controlled via
`ContentTypeSchema.URLPrefix` (A154).

**Define a content type with a public URL**

```go
fields, _ := json.Marshal([]smeldr.SchemaField{
    {Name: "Title", Type: "string", Required: true, Role: "title"},
    {Name: "Body",  Type: "string", Role: "body"},
})
desc, err := app.DefineContentType(&smeldr.ContentTypeSchema{
    TypeName:  "recipe",
    Kind:      "content",
    Fields:    json.RawMessage(fields),
    URLPrefix: "/recipes",  // operator-set; empty = admin-only, no public routes
})
```

`URLPrefix` must start with `"/"`. When non-empty, `DefineContentType` registers
`GET {URLPrefix}/{slug}` as the public read route. Empty `URLPrefix` means the type
is accessible via the admin API only.

**Serve public and admin routes**

```go
app.ServeDynamicContent()
```

Call this once at startup. It:
1. Runs `MigrateURLPrefixColumn` on `smeldr_dynamic_content`.
2. Initialises the in-memory sitemap store.
3. Loads all persisted dynamic types from the DB at boot (`loadDynamicTypes`).
4. Registers 5 admin endpoints keyed by **type_name** (not URL prefix):

| Method | Path | Role | Description |
|--------|------|------|-------------|
| `POST` | `/_content/types` | Admin | Define a new content type |
| `POST` | `/_content/{type}` | Editor+ | Create a draft item |
| `GET` | `/_content/{type}` | Editor+ | List all items (all statuses) |
| `GET` | `/_content/{type}/{id}` | Editor+ | Get item by ID |
| `PATCH` | `/_content/{type}/{id}` | Editor+ | Update fields (PATCH semantics) |
| `POST` | `/_content/{type}/{id}/status` | Editor+ | Set status |

`{type}` is the `type_name` (e.g. `recipe`), not the URL prefix.

**Sitemap auto-rebuild**

`SetStatus` fires a background goroutine that regenerates `{URLPrefix}/sitemap.xml`
in the in-memory sitemap store after each status change. Types without a `URLPrefix`
are not included in the sitemap.

**Access a repo from application code**

```go
repo, err := app.DynamicContentRepo("recipe")
if err != nil { ... }

id, err := repo.CreateDraft(ctx, schema, fields)
item, err := repo.GetBySlug(ctx, "chocolate-cake")
items, err := repo.List(ctx, smeldr.ListOptions{Status: "published", PerPage: 10})
err = repo.UpdateFields(ctx, id, newFields)
err = repo.SetStatus(ctx, id, "published")
```

**Database migration**

When upgrading an existing database that pre-dates A154, call once at startup:

```go
smeldr.MigrateURLPrefixColumn(db) // idempotent; no-op on non-SQLite
```

`ServeDynamicContent` calls this automatically. Use the explicit call only when
bootstrapping a database without going through `App`.

**Validation helpers**

`smeldr.ValidateSchemaDef(schema)` validates a schema before writing; checks
`TypeName`, `URLPrefix` format (must start with `"/"`), field types, and roles.
`smeldr.PluralSnake(name)` returns the default plural URL prefix for a type name
(e.g. `"recipe"` → `"recipes"`, `"story"` → `"stories"`).

**HTML rendering**

Dynamic content types serve JSON by default. For HTML rendering in a standalone
site, implement templates in your application using the JSON API as the data
source. Cloud rendering is outside core scope (A156).
