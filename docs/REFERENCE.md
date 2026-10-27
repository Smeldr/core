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
- `PUT /posts/{slug}` — update, full replace (Author+)
- `PATCH /posts/{slug}` — update, partial — a field omitted from the body keeps its existing value (Author+, same tier as `PUT`) (T242). A `status`, `slug` or `id` in the body that differs from the stored value is refused with 422 and the field named (held, version pending): a status changes through `PUT`, `transition_item` or the lifecycle tools, and an update never changes the slug or the ID. Before, PATCH and MCP `update_{type}` dropped those keys and reported success. The same value as stored is accepted.
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
LifecycleEvent  →  a hook that fires when something changes
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
| `smeldr_search:"text"` | Marks a string field that `App.Search` matches (case-insensitive substring), see [Search](#search-t885) |

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

### RoleStore vs. an organization-membership model (Non-Decision, T151)

`RoleStore`/the built-in hierarchy above answers one question only: what
can this actor do to *this instance's own content*. It is deliberately
not a model of organization membership, billing authority, or
account-level access — `smeldr.dev/cloud`'s own `pilotorg.Role`
(`Owner`/`Admin`/`Member`) answers that different question, for its own
multi-tenant layer sitting above however many Smeldr instances an
organization owns. The two are not reconciled into one shared vocabulary,
and should not be: checking "can this actor read/write this instance's
own content" always means `RoleStore`; checking "can this actor act on an
organization or its instances" is a `pilotorg.Role`-shaped question that
does not belong in core at all. Do not translate one into the other —
check whichever question the call site is actually asking.

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
    id         TEXT PRIMARY KEY,  -- SHA-256 hex fingerprint of the raw token
    name       TEXT NOT NULL,
    role       TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    revoked_at TEXT,
    created_at TEXT NOT NULL,
    user_id    TEXT               -- optional; see "user_id" below
);
```

#### `user_id`

`user_id` (optional, added v1.104.0) records the JWT's own `User.ID` minted
for this token — the same actor identity `last_actor`, `RoleGrant.Grantor`,
and `RelationEdge.CreatedBy` already record elsewhere, letting an actor ID
seen in any of those be resolved back to the token's own `Name` via
`TokenStore.NamesForUserIDs`. `user_id` is unique per token by construction
(`createToken` mints a fresh ID for every token) — a given value never
resolves to more than one name.

`smeldr_tokens` is **not** a core-owned table — every application defines
its own DDL (this one is a template, not enforced). An application on an
existing deployment adds the column with `smeldr.EnsureTokenUserIDColumn`,
called once at boot before `TokenStore.Create`/`CreateWithID` is first
called; `TokenStore.Create`/`CreateWithID`/`List`/`NamesForUserIDs` all fall
back to the column-less form automatically until then, so upgrading core
alone never breaks an application that hasn't added the column yet.

This fallback detects a missing table or column by matching **SQLite's own
error text** — the same convention every other such fallback in this
codebase uses. An application on another database engine (Postgres, MySQL)
must run its own equivalent of `EnsureTokenUserIDColumn`'s migration before
upgrading; the text-matching fallback does not recognize that engine's own
error format and will not add the column for you.

Every token created before this column existed keeps `user_id = NULL`
permanently — it is never backfilled or guessed. This includes any
already-issued long-lived token (a bootstrap admin token, an agent's own
token minted before the upgrade): it stays absent from
`NamesForUserIDs`'s results until it is revoked and reissued.

#### Actor classification — `CreateClassified` (A418, D105, v1.121.0)

A token can carry a classification tag beside its permission role, so
provenance records what kind of actor used it. There are three tags, all
outside the role hierarchy (`HasRole` ignores them, so a tag never grants or
changes a permission): `smeldr.Agent`, `smeldr.Job` and `smeldr.Human`.

```go
raw, userID, err := tokens.CreateClassified(ctx, "core-implementer", "editor", smeldr.Agent, 90*24*time.Hour)
```

- `class` is `Agent`, `Job` or `Human`; `""` is an unclassified token (the same as `CreateWithID`); anything else is a `ValidationError`. `role` must be `author`, `editor` or `admin`. The minting Admin **attests** the class: .
- The tag is the second entry of the signed token's roles (`[editor, agent]`); the permission role stays first, because the signal bus reads the first role as the actor's role. Provenance follows the signed token.
- The class is also stored in the nullable column `smeldr_tokens.actor_class`, added with `smeldr.EnsureTokenActorClassColumn` (call it at boot; the example server does), and `TokenStore.List` returns it as `TokenRecord.ActorClass` (`""` for an unclassified token, a token created before the column, and every token from `Create`/`CreateWithID`). A **classified mint on a table without the column is refused** with an error naming `EnsureTokenActorClassColumn`; an unclassified mint keeps working.
- An issued token cannot be classified afterwards: the class is inside its signature. Issue a new token and revoke the old one. `list_tokens` shows what was stored, provenance follows the signature; they differ only if the table is edited by hand.
- `Create` and `CreateWithID` keep accepting any role string as before (a custom registered role works through them); only `CreateClassified` validates the role. This inconsistency is deliberate and unchanged.

**What `actor_kind` records (D105).** `job`, then `agent`, then `human` is the order when several tags are present, so an automated actor never reads as a person (a hand-signed `[editor, human, agent]` records `agent`). An actor with **none** of the three tags records **`unclassified`**: it makes no claim to be a person. Every actor that is not a minted bearer token is unclassified too: a session or magic-link user, a Basic-auth user, a token from `Create`/`CreateWithID`.

**Cutover: core v1.121.0 (2026-10-07).** Before v1.121.0 an untagged actor recorded `human`, and no token could carry a tag, so every agent action was recorded as `human`. **Provenance rows written before the cutover keep the value they were written with and are not rewritten: an older `human` row means unclassified, never a verified person.** From v1.121.0 `human` means a person attested by the Admin who minted the token. A reader that compared `actor_kind == "human"` must now also expect `unclassified`.

#### Reasons on minting, revoking and granting (v1.135.0)

Each access-management act can carry a reason, stored on its provenance record (D96), the same place a transition's reason lives:

```go
raw, userID, err := tokens.CreateClassifiedWithReason(ctx, "importer", "editor", smeldr.Job, 30*24*time.Hour, "nightly import")
err = tokens.RevokeWithReason(ctx, fingerprintID, "import moved to the new service")
grantID, err := roles.Grant(ctx, smeldr.RoleGrant{TokenID: userID, RoleName: "steward", Reason: "owns the docs area"})
err = roles.RevokeWithReason(ctx, grantID, "rotation over")
```

- The reason is optional. It is trimmed; longer than 1000 characters is a `*ValidationError` and nothing is minted, revoked or granted. `Create`, `CreateWithID`, `CreateClassified`, `Revoke` and `RoleStore.Revoke` are unchanged and record no reason.
- **The reason is free text that people read: never put a token value or other secret in it.**
- **Minting now writes a record.** Every mint writes a `Token`/`assert` provenance record whose subject is the token's fingerprint id, never the raw token. Before this release only revocation was recorded. The actor is the caller; with no caller identity it is the mechanism as a `job`: `token-bootstrap` for the boot-time admin token, `token-store` for a program calling the store outside a request. A revocation with no caller is now recorded as `job` `token-store` too, where it used to record an empty actor.
- **Read back:** `TokenStore.List` fills `TokenRecord.Reason` (the mint reason) and `RevokeReason`; `RoleStore.ListGrants` fills `RoleGrant.Reason`. They are read from provenance in one query per verb with the default store, and are empty when none was given, provenance is not wired, or the act predates recorded reasons (nothing is backfilled). A failed read leaves them empty and is logged; the list is still returned.
- A revoked grant's row is deleted, so its revoke reason is in its provenance record only. smeldr.dev/mcp's `get_item_provenance` reads it (mcp v1.53.0): `type_name` `Token` with the fingerprint id, or `RoleGrant` with the grant id, gated like `list_tokens` / `list_grants`. Those two names are reserved for these histories: a content type named `Token` or `RoleGrant` cannot be read through `get_item_provenance`.
- smeldr.dev/mcp: `create_token`, `revoke_token`, `grant_role` and `revoke_grant` take an optional `reason`; `list_tokens` and `list_grants` return it. smeldr.dev/cli: `token create ... --reason`, `token revoke <id> --reason`.

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

// Resolve a batch of actor IDs (from last_actor, RoleGrant.Grantor,
// RelationEdge.CreatedBy, ...) back to the token Name each was minted for.
// One query for the whole batch; an ID with no match is simply absent.
names, err := app.TokenStore().NamesForUserIDs(ctx, []string{userID1, userID2})
```

`ErrLastAdmin` (HTTP 409) is returned if you attempt to revoke the last
active admin token. Create a replacement first.

### MCP tools (Admin role required)

| Tool | Description |
|------|-------------|
| `create_token` | Issues a new named token with a given role and TTL; optional `actor_class` (`agent`, `job` or `human`, mcp v1.48.0) classifies its actor |
| `list_tokens` | Lists all tokens with name, role, expiry, revoked status and `ActorClass` |
| `revoke_token` | Revokes a token by ID — effective immediately |

`create_token` returns the plaintext token once. Copy it immediately —
it cannot be retrieved again. See [AGENTS.md](AGENTS.md) for full details.

### Rule-type stewardship (D63/D64, decision-governance-model.md §5)

Authority stewardship — "who owns a rule-type domain" (e.g. `design-system`,
`security`) — is modeled as a real, scoped role grant, not a dedicated table.
Requires no new mechanism: `RoleGrant.ScopeStatic` already accepts any
`"type:id"` pattern with no validation that `type` names a real registered
content type, so a synthetic `"RuleType:<name>"` pattern grants standing
authority over that domain using the same `define_role`/`grant_role` tools
every other scoped grant uses.

```go
// Define a domain-specific steward role and grant it — no core code beyond
// the existing governance mechanism.
app.RoleStore().DefineRole(ctx, smeldr.RoleDefinition{
    Name: "design-system-steward", Operations: []string{"steward"}, ScopeMode: smeldr.ScopeStatic,
})
app.RoleStore().Grant(ctx, smeldr.RoleGrant{
    TokenID: tokenID, RoleName: "design-system-steward",
    ScopeStatic: []string{"RuleType:design-system"},
})
```

Two read functions on `RoleStore` answer the pull-discoverability half of §5
("anyone holding a stewardship role can query what currently touches
authority they steward"):

```go
func (s *RoleStore) StewardedRuleTypes(ctx context.Context, tokenID string) ([]string, error)
func (s *RoleStore) StewardshipInbox(ctx context.Context, tokenID string) (*StewardshipInbox, error)

type StewardshipInbox struct {
    RuleTypes []string
    Decisions []Decision
    Rules     []Rule
    Stubs     []AuthorityStub
}
```

`StewardedRuleTypes` returns the RuleType domains `tokenID` holds standing
authority over — every `"RuleType:<name>"` static-scope pattern on a grant
whose role holds the `"steward"` operation. `StewardshipInbox` calls it, then
returns every `Decision`/`Rule`/`AuthorityStub` whose own `RuleType` field
matches one of those domains — scoped to these three types (every
RuleType-bearing type that exists today); extend the type when a future type
gains its own `RuleType` field. Both return an empty (non-nil) result, not an
error, when the token holds no stewardship grants.

Classification — "which domain a given new item falls under" — needs no new
mechanism either: `Decision.RuleType`/`Rule.RuleType`/`AuthorityStub.RuleType`
(A303/A304) are ordinary, ungated string fields, settable by any Author+ role
via the existing `update_decision`/`update_rule`/`update_authority_stub` MCP
tools, matching §5's own framing ("frequent, low-stakes... an agent may
propose and apply this by default").

**Not built here**: push notification to a steward when a new item lands in
their domain — §5's own text names this as separate, later work.
`StewardshipInbox` is exposed as an MCP tool, `get_stewardship_inbox`
(Author role, no params — always the caller's own token), by
`smeldr.dev/mcp` — see that module's own docs; this tool requires a
`smeldr_tool_policies` seed row (`{"get_stewardship_inbox", "read"}`,
added alongside `get_goal_context`/`get_sweep_run`'s own rows) or every
governance-enabled caller is silently forbidden, the same gap A298 already
closed once for `get_sweep_run`.

### Approval roles (D142, T884)

Two operations decide who approves: `smeldr.OperationRatify` (`"ratify"`: ratify, supersede or amend a Decision) and `smeldr.OperationApproveChange` (`"approve-change"`: approve a change, such as a pull request, against the Decisions in force). The four Decision doors (`proposed→ratified`, `pending-re-evaluation→ratified`, `pending-re-evaluation→superseded`, `ratified→superseded`) require `ratify`, strictly. No core gate requires `approve-change` yet; the pull request check will. `"approve"` is retired.

| Role (constant) | Operations | Scope | Register with |
|---|---|---|---|
| `approver` (`RoleApprover`, was `decision-steward`) | ratify, approve-change, review | global | `RegisterApproverRole` |
| `domain-approver` (`RoleDomainApprover`, was `decision-domain-admin`) | ratify, approve-change | dynamic, `belongs_to_domain`, incoming, anchored on a Domain | `RegisterDomainApproverRole` |
| `area-approver` (`RoleAreaApprover`) | approve-change | dynamic, `belongs_to_area`, incoming, anchored on an Area | `RegisterAreaApproverRole` |
| `item-approver` | ratify, approve-change, read | static, one item | `RegisterItemApproverRole` |

A dynamic place grant follows the place hierarchy (D142, T882): a domain-approver anchored on a Domain covers the Decisions placed in it, the Areas in it and what is placed in them. `admin` holds no approval operation (01a0e3f9-2); a token that ratifies holds one of these roles explicitly. A Decision is ratified only with a place (T883), so its place decides which domain-approver may act.

```go
_ = smeldr.RegisterDomainApproverRole(ctx, rs)
app.RoleStore().Grant(ctx, smeldr.RoleGrant{
    TokenID: tokenID, RoleName: smeldr.RoleDomainApprover,
    ScopeAnchorID: domainID,
})
```

`RegisterDecisionStewardRole` and `RegisterDecisionDomainAdminRole` are deprecated wrappers that define `approver` and `domain-approver`; they go in the first MINOR after Smeldr Cloud has moved.

**Migration.** `App.Governance` runs it at every boot, idempotently: `decision-steward` is renamed `approver` and `decision-domain-admin` `domain-approver` (their grants follow, keeping ID, scope, expiry and audit trail; when the new role already exists the grants move to it and a duplicate is dropped). Every stored role that holds `approve` also gets `ratify` and `approve-change`; `approve` stays on custom roles so a custom flow that requires it keeps working, and the core roles drop it when they are registered again. Open Signals addressed to `approve` (ratification-required, conflict-detected) are addressed to `ratify`. Each rename gets a governance audit record by actor `system`.

**The agent rule (D142 point 5, D105), fail closed.**
- `RoleStore.Grant` reads the grantee's class (`smeldr_tokens.actor_class` by `user_id`; the class signed into the token when the grantee is the caller). For a grantee of class `agent` or `job`: a role that holds `ratify` is refused; a role that holds `approve-change` is refused unless it is scoped over `belongs_to_area` and the grant is anchored on an existing Area; and that Area grant needs a grantor (the caller, or the store's audit actor) of class `human` who holds `approve-change` over the Area. A class that cannot be read refuses. A grantee without a token row, or an instance without the class columns, is unclassified and not refused by this rule.
- `validateTransition` refuses a transition that requires `ratify` when the actor in ctx is an agent or job token, whatever its grants say.
- Unclassified tokens are not refused by the rule; classifying or retiring them is a separate step.

**Founder.** `App.GrantFounder(ctx, userID)` grants `admin` and `approver` to the person who created the organization, with the reason "founder of the organization (D142)" in each grant's provenance; idempotent. example/server calls it at boot for `ORGANIZATION_FOUNDER`.

`decisionScopeRoles` (`orchestration.go`, a flat `Scope`-string to role-name map) is deliberately left unpopulated; see its doc comment.

### Time-boxed grants (`ExpiresAt`, grants-and-delegate-v1 §3.1/3.2)

`RoleGrant.ExpiresAt *time.Time` makes a grant time-boxed instead of standing. It may be given in any time zone: `Grant` stores it in UTC (T916), so it expires at its moment on SQLite too, where the column is text; at every `App.Governance` boot, a stored expiry in another form (another zone, RFC3339) is rewritten to UTC, and one that cannot be read is set to the Unix epoch (expired, fail closed) with a warning naming the grant.
`nil` (the default, unchanged for every existing caller) is a standing grant
with no expiry. A non-nil value is when the grant stops matching — same
"absent, not present-but-inert" shape `RelationEdge.InvalidAt` already uses
for relations:

```go
expiry := time.Now().Add(14 * 24 * time.Hour) // 14-day time-boxed grant
app.RoleStore().Grant(ctx, smeldr.RoleGrant{
    TokenID: tokenID, RoleName: smeldr.RoleApprover, ExpiresAt: &expiry,
})
```

Enforcement is automatic and requires no separate check: `RoleStore.Authorized`,
`RoleStore.RoleGranted`, and `RoleStore.StewardedRuleTypes` (and so
`StewardshipInbox`, which calls it) each exclude an expired grant from their
underlying query — an expired grant simply stops authorizing anything, with
no revocation call needed. Fail-closed is unchanged: a DB error still denies
before the expiry check is ever evaluated.

Over MCP and the CLI (mcp v1.53.0, cli v0.23.0): `grant_role` takes an optional
`expires_in_days` (a positive number of days, fractions allowed, no upper cap;
the same name and unit as `create_token` and `delegate_item`) and returns the
resulting `expires_at`; `smeldr-cli grant ... --expires-in-days N` sends it.

`RoleStore.ListGrants` deliberately does **not** filter by expiry — it is the
"explainable authority" audit surface (`list_grants`), so a viewer can see
that a grant existed and when it expired, not have it silently disappear.

No sweep or cleanup job exists for v1, by design — an expired row is simply
never matched again, the same reasoning `smeldr_relations`' `invalid_at`
column never needed one either. `RoleStore.Revoke` still works on a
time-boxed grant exactly as on a standing one, for explicit early revocation.

This is deliberately just the storage/enforcement primitive — a default
14-day expiry, a server-enforced maximum, and the delegator-caps-delegate
authority check belong to the higher-level Delegate mechanism
(`delegate_item`, grants-and-delegate-v1 §3.3), a separate, later piece.

`RoleStore.GetRole(ctx, name) (RoleDefinition, error)` is the read-only
counterpart to `DefineRole`, added for exactly this Delegate mechanism: a
caller granting a role on someone else's behalf must check the delegator
against *every* operation the named role actually holds, not just a single
caller-supplied word — checking only one word is a real privilege-escalation
path (a delegator holding nothing but `read` could otherwise name
`operation: "read"` alongside `role: "admin"` and pass a naive check while
handing out full admin). Returns an error wrapping `ErrNotFound` when no role
with that name exists. `delegate_item` itself — the MCP tool that puts
`GetRole`, the per-operation `Authorized` loop, and `Grant` together with a
default/maximum expiry — ships in `smeldr.dev/mcp`, not here; see that
module's own docs.

`RegisterItemApproverRole`/`RegisterItemReviewerRole` (workspace-delegate-region-v1
§0 P1) define two built-in `ScopeStatic`-mode roles — `item-approver`
(`["approve"]`) and `item-reviewer` (`["review"]`) — the first roles
`delegate_item` can actually hand out safely. `RoleStore.Authorized`/
`RoleGranted` branch on the *role's* own `ScopeMode`, not the grant's:
delegating a `ScopeGlobal` role like `decision-steward` "for one Decision"
still authorizes the recipient on every Decision, because a global-scope
role's grant never consults its own `ScopeStatic` list at all. `delegate_item`
refuses to hand out any role whose `ScopeMode` isn't `ScopeStatic`, for
exactly this reason.

### Read scope (per-item read rules, A494)

```go
func (a *App) ReadScope(ctx Context) (*ReadScope, error)
func LoadReadScope(ctx context.Context, rs *RoleStore, user User) (*ReadScope, error)
func (s *ReadScope) All() bool
func (s *ReadScope) Readable(typeName, id string) bool
func (s *ReadScope) TypeReadable(typeName string) bool
```

`ReadScope` is what one caller may read, loaded once per request and then checked in memory. Its answers equal `RoleStore.Authorized(token, "read", AuthTarget{TypeName, ID})` for every grant shape:
- a global grant reads everything;
- a static grant reads its `type:id` and `type:*` patterns;
- a dynamic grant reads the items one live, asserted hop from its anchor. It does not read the anchor itself.

`LoadReadScope` is the loader behind `App.ReadScope`, for a caller that holds a `RoleStore`.
- **Without governance:** `All()` is `user.HasRole(Author)`.
- **No user ID:** an empty scope.
- `TypeReadable` is the floor a surface checks before it filters per item. A nil scope reads nothing.

**Cost.**
- A global reader costs one query.
- A static grant costs nothing beyond that query.
- A dynamic grant loads its anchor's whole one-hop item set on every load: one query per direction. The cost grows with the anchor's size. Measured on SQLite, a load with one static and one dynamic grant plus 10,000 `Readable` calls takes about 2.5 ms and 0.2 MB with 1,000 items under the anchor, and about 55 ms and 9 MB with 50,000. Pushing the filter into SQL is the named later optimisation.

| `ReadScope` / `LoadReadScope` returns | When |
|---|---|
| an `ErrInternal` error, no scope | the grants query, a scan, or an anchor query fails. The failure is logged, and the caller answers an internal error, never not-found. There is no partial scope |
| a scope, nil | otherwise, including an empty scope |

**Reader roles.**
- `RegisterScopedReaderRole` defines `scoped-reader` (static).
- `RegisterDomainReaderRole` defines `domain-reader` (dynamic, `belongs_to_domain`, incoming).
- `RegisterAreaReaderRole` defines `area-reader` (dynamic, `belongs_to_area`, incoming; T882).
- All three have ops exactly `["read"]`, are idempotent, and are registered by example/server when governance is enabled.
- **Place readers read through the place hierarchy (D142, T882).** A grant on a place kind (`belongs_to_organization`, `belongs_to_domain`, `belongs_to_area`) reads the place itself, every record placed in it, and for a place below it (a Domain in Organization, an Area in a Domain) that place and what is placed in it. No other relation kind is followed. So a domain-reader reads its Domain, its Areas and everything placed in either; an area-reader its Area and what is placed in it. Before T882 the anchor was not readable (a scoped member's Navigator landed empty, T843). The per-item check (`RoleStore.Authorized`) uses the same walk; the place record itself counts only for `read`, so a `decision-domain-admin` approves the Decisions in its Domain and its Areas, never the Domain record.

**Static patterns are stored as IDs.** `RoleStore.Grant` resolves each `type:item` pattern through the App's resolver (address, slug, ID, then the transitional legacy number) and stores `type:<ID>`, the only form `Authorized` and `ReadScope` match. A wildcard `type:*` is kept, and duplicates collapse. A pattern that is not `type:item`, names an unregistered type, or names no record of its type is refused with a validation error naming it. A RoleStore used without an App stores the patterns as given. **A synthetic type is kept as given** (since v1.148.1, A497): its pattern names a domain, not a record. The only one is `RuleType`, so a stewardship pattern `RuleType:<name>` is stored unchanged next to resolved record patterns, and `Authorized`, `ReadScope` and `StewardedRuleTypes` match it literally.

v1.146.0 changed no read surface. Since v1.147.0 the item surfaces apply the scope, and since v1.148.0 the graph surfaces do too (both below). The event stream and webhooks are a later step.

### Per-item read rules on the item surfaces (A495)

**The rule.**
- A caller sees an item when its read scope reads it, or when the item is published web content (a type without an address, such as a post). Published web content stays public. **A record type (an addressed type) is never public**, not even in a Published status: a guest reads none of its items (D110: web content is the public kind; before v1.147.0 a guest saw a Published record over HTTP).
- An item the caller may not read is answered **exactly like a missing one**, on every item surface:
  - the same HTTP 404 and body;
  - the same `ErrNotFound`;
  - writes included.
- A forbidden answer only ever reaches a caller who can read the item.

**The order on every surface:**
1. the read scope is loaded;
2. the item is resolved;
3. visibility is checked;
4. only then any enrichment.

So nothing follows the check. For a caller that cannot read every item, a hidden hit counts as a miss and the resolver walks its remaining steps (address, slug, ID, then the legacy number) exactly as for an ident that matches nothing. A hidden and a missing item of the same shape therefore run the same statements, with no timing difference between them.

**The surfaces.**
- **MCP methods on a module:** `MCPGet`, `MCPUpdate`, `MCPPublish`, `MCPSchedule`, `MCPArchive` and `MCPDelete` answer a hidden item as not found. `MCPList` returns only what the caller may see, so a page and a total count readable items only.
- **The module's HTTP routes:**
  - list, show, single instance, AI doc, and the aggregate dispatch (`findAndServe`, `findAndServeAIDoc`): a scoped reader sees published items plus the drafts it may read;
  - PUT and DELETE answer a hidden item with 404;
  - a preview token still opens its one draft (bound to its ID with `App.GeneratePreviewTokenForID`, since drafts may share a slug);
  - sitemaps, feeds and the AI index carry published items only, as before.
- **`/_content/{type}[/{id}]`:** under governance, gated by read access to at least one item of the type (403 otherwise), filtered per item and paged after filtering. A hidden item is 404. Without governance it is unchanged (Editor).
- **`App.TransitionItem*`:** an unreadable item is `ErrNotFound`, checked before the transition gate.

**Without governance**, the Go and MCP surfaces have no per-item rules, as before, and the HTTP routes keep the Author draft rule.

**Loading the scope once per request.**
- `WithReadScope(ctx, s)` attaches a scope, and `ReadScopeFrom(ctx)` returns it. A `Context` passed in stays a `Context`.
- Without an attached scope, a surface loads the caller's own scope from the user in the context. Under governance, no user reads nothing.
- **Trusted in-process code** (a job, a migration, a test) reads past the rules only explicitly, with `TrustedRead(ctx)`, which attaches `AllReadScope()`. Request surfaces never use it.

**Failures.** A failure to load the scope is an internal error (500 / `ErrInternal`) on every surface, never a not-found.

**Slugs.**
- **Records:** for a record type and a caller who cannot read every record, a taken slug never reveals what holds it. A supplied slug that is taken becomes the slug plus the tail of the new ID, and an MCP create returns a `slug_adjusted` warning. A taken automatic slug gets the same tail instead of a counter, because a counter would count records the caller may not read. A reader of everything keeps today's counter.
- **Web content (core v1.154.0, A507): a draft reserves no URL.**
  - Drafts may share a slug with each other and with a published item. Creating or updating a draft never fails, and its slug never changes, because of a collision.
  - An automatic slug's counter counts published items only. They are public, so the counter reveals nothing hidden.
  - Uniqueness is checked when an item is published or scheduled (MCP publish and schedule, HTTP create or update into published, `transition_item`, the scheduler), against published items of the type with another ID. A clash answers 409 `ErrConflict`: `the URL /<prefix>/<slug> is already published; give this item another slug, for example /<prefix>/<slug>-2`. It names only public URLs. The scheduler leaves a clashing item Scheduled and logs it once.
  - A web-content item that is not published may change its slug: a compiled module through an update with `slug`, a dynamic type through `DynamicTypeRepo.SetSlugVia(ctx, surface, id, slug)`. A published item keeps its URL.
  - A slug that names more than one item the caller can read answers 409 `the slug "<slug>" names more than one item you can read; use its ID`. Only readable items are counted, so a hidden draft is never revealed. The ID always resolves, on MCP and on the module's HTTP write routes (`PUT`, `PATCH` and `DELETE /<prefix>/<id>`).
  - `GET /<prefix>/<slug>` serves the published item when one holds the slug, so a draft never shadows a page.
  - Storage: dynamic types use the index `idx_dynamic_content_type_slug_published` (below). A compiled module's own table should declare `slug TEXT NOT NULL` (no `UNIQUE`) and call `EnsurePublishedSlugIndex` (below). A table that keeps a column-level `UNIQUE` refuses a second draft with the same slug until its owner drops it.
  - A user-written `Repository[T]` resolves a slug through `FindBySlug` (one holder), so ambiguity is not detected there. `MemoryRepo` and `SQLRepo` list every holder.
- **The legacy lookup** by an old number (until the D110 cut) treats a hidden record as no match, and its "matches N records" error counts only records the caller may read.

**Stated property: list timing.** A list loads its candidates and then filters, so its duration grows with all rows, hidden ones included. That reveals volume, which addresses already reveal, and it is accepted.

### Per-item read rules on the graph surfaces (A496)

The graph surfaces apply the same rule as the item surfaces: an item the caller may not read is **absent**, and a hidden item answers exactly like a missing one, with the same statements run.

**Which scope a package function applies.** `QueryGoalContext`, `BuildContextPacket`, the `RelationStore` reads and `Reachability` take a database handle, not an App. They apply:
1. the scope attached to the context (`WithReadScope`, `TrustedRead`);
2. otherwise the caller's own scope, loaded from the user in the context, under the governance that `App.Governance` wired on that handle. Under governance, no user reads nothing;
3. otherwise everything, as before, when the handle is one an App uses without governance, or when no App in the process wires governance (legacy mode);
4. otherwise an `ErrInternal` error saying no read scope was attached. This is a handle that no App registered (a wrapper, a transaction, an adapter, or one that cannot be a map key) in a process where governance is wired. It never reads everything.

An App registers its `Config.DB` when it is created, and the handle of the `RelationStore` given to `App.Relations` with the App's governance, in either wiring order. A governed handle stays governed when another App without governance opens it. For any other handle, attach the caller's scope with `WithReadScope`, or use `TrustedRead` for trusted in-process code.

**The surfaces.**
- **`BuildContextPacket` and `GET /packet/{type}/{slug}`:** a hidden anchor is not found. An edge whose far end is hidden is left out before anything is fetched, so the item is not in the packet, not counted in `Omitted`, and not walked through at depth 2.
- **`QueryGoalContext`:** a hidden Goal is not found. Linked Decisions, Tasks and Goals the caller may not read are left out.
- **`RelationStore.MCPGetRelations` and `MCPPreviewImpact`:** an edge is returned only when the caller may read both ends. A hidden item has no edges, the same as a missing one.
- **`RelationStore.ListRelations`:** the page and `Total` cover only edges with both ends readable. For a caller that reads everything, the SQL paging is unchanged. For a scoped caller, every matching row is loaded, filtered, then paged and counted in memory, so its cost grows with the matching rows, not the page.
- **`Reachability`, `ReachabilityBounded` and `GET /reachability/{type}/{id}`:** a hidden node is not placed in a ring, not counted toward the cap or a cut, and not walked through, so no path through it is revealed. A hidden anchor reads as one with no relations.
- **`App.ItemProvenance`:** a hidden item has the empty history of a missing one (`Total` 0).
- **`GET /_audit`:** under governance an entry is shown only when a record of its type that the caller may read still holds the entry's slug. An entry names its subject by type and slug at the time of the event, not by ID, so an entry whose subject was deleted or got a new slug is not shown to a scoped caller. The public clause does not apply, because an entry shows the actor. A reader of everything, or an app without governance, sees every entry as before.

**HTTP gates.** Without governance, `/packet` keeps the Editor role, `/reachability` the Author role, and `/_audit` the Editor role. Under governance, `/packet` and `/reachability` instead require read access to at least one item of the anchor's type (403 otherwise), and the result is built under the caller's own scope. `/_audit` keeps the Editor role and filters.

**Trusted primitives.** `GetBySource`, `GetByTarget`, `GetLiveBySource` and `GetLiveByTarget` stay unfiltered: they are what core's own internals use (the relation cascade, the amends checks, and the walks above, which filter on top of them). A request surface never returns their result directly.

**Failures.** A failure to load the scope is `ErrInternal` (500) on every graph surface, never an empty result.

### Per-item read rules on dynamic types, Withdraw and Signals (A498)

```go
func (r *DynamicTypeRepo) ListPage(ctx context.Context, opts ListOptions) ([]map[string]any, int, error)
func (a *App) ResolveReadableID(ctx context.Context, typeName, ident string) (string, error)
```

**Dynamic types.**
- `DynamicTypeRepo.Resolve`, `List` and the new `ListPage` apply the caller's read scope, the same rule as the package functions above: the attached scope, else the caller's own, else everything without governance, else `ErrInternal` on a handle no App registered.
- A caller sees an item it may read, and published web content (a type with no address).
- `Resolve` answers a hidden item exactly like a missing one, after the same statements.
- `ListPage` returns the page and the total of items matching the status filter. For a caller who cannot read everything, every matching row is loaded and filtered, and the page is cut in memory, so neither a short page nor the total reveals a hidden item. A reader of everything keeps the SQL paging, plus one COUNT query for the total. `List` is `ListPage` without the total.
- `GetBySlug` and `GetByID` stay unfiltered trusted primitives. A request resolves through `Resolve`.
- Trusted Go code (a job, a test) reads past the rules with `TrustedRead`. Under governance, a Go call with no user reads nothing.

**`RelationStore.Withdraw`.** For a caller who cannot read everything, a relation with an end the caller may not read is `ErrNotFound`, exactly as an unknown id, whether it is live or has ended. The check is one statement, run before the "already ended" `ErrConflict`, so that conflict only reaches a caller who can read both ends. A reader of everything runs as before.

**Signals.**
- A Signal with a subject (`subject_type`, `subject_id`) is visible only when the caller may read its subject too.
- This holds on the module surfaces (MCP get and list, writes, HTTP), `App.TransitionItem*`, the context packet (anchor and linked Signals, with `Omitted` counting readable Signals only) and `/_audit`.
- A Signal whose subject is hidden answers exactly like a missing one, after the same statements.
- Since v1.161.0 (D113) every write stores a Signal's subject as its ID, so the rule sees it. A subject stored before that as anything other than its ID (a slug, an address), or of a type that is not an item (such as `RelationEdge`), matches nothing readable and is hidden from a scoped reader (fail-closed) until `MigrateReferences` rewrites it.
- The relation walks (`get_relations`, `list_relations`, `preview_impact`) and `get_reachability` show an edge with a Signal end only when the caller can read the Signal and its subject (D113), the same rule as the context packet.
- The relation tools that name an item (`assert_relation`, `propose_relation`, `observe_relation`, `get_relations`, `get_reachability`, `preview_impact`) take its address, slug or ID (smeldr.dev/mcp v1.64.1, T875), resolved with `App.ResolveReadableID`. One that resolves to nothing the caller may read is not found, never an empty answer, so a hidden item answers like a missing one. Only types the App registers resolve: for an unregistered type (an open relation end) the ident is used as given.

**`App.ResolveReadableID`.**
- It returns the ID of the item that ident names (address, slug, ID, or the legacy number) under the caller's read scope.
- A hidden item, or a Signal whose subject is hidden, is `ErrNotFound` after the same statements as a missing one. An unregistered type is `ErrBadRequest`, and a scope load failure is `ErrInternal`.
- It is the resolver a surface uses to turn a caller's name for an item into the ID it stores or authorizes against. smeldr.dev/mcp's `create_signal`, `delegate_item` and `create_preview_url` use it.

**Over MCP (smeldr.dev/mcp v1.61.0, A500).** Every tool applies these rules. Two stated properties:
- `list_relations` without a `source_type` or `target_type` filter keeps the global gate. A scoped reader passes a type to be gated on that type.
- `transition_item` needs read on the type, then, on the one item, `manage` or the operation the requested transition itself requires (since smeldr.dev/mcp v1.61.1, A502). A delegated approver or reviewer, whose role reads its item since core v1.151.0, moves it through the transition it was delegated for; the flow's own check still decides.

**Delegation roles (core v1.151.0, A501).** `item-approver` holds `["approve", "read"]` and `item-reviewer` `["review", "read"]`, both static-scope: a delegate reads the one item it was delegated, and nothing else widens. Stored grants pick this up when the roles are registered at boot, with no migration.

`RoleStore.Grant`'s idempotency key includes `ScopeStatic` (workspace-delegate-region-v1
§0 F2): granting the same `(token, role, anchor)` pair with a *different*
`ScopeStatic` list creates a separate grant row scoped to that list, rather
than silently resolving to an earlier grant's row and leaving the new scope
unauthorized. A repeat call with the *identical* `ScopeStatic` list still
dedupes to one row, unchanged. Callers whose `ScopeStatic` never varies
(every global- or dynamic-scope grant) see no behavior change.

`RoleStore.GetGrant(ctx, grantID) (RoleGrant, error)` is `ListGrants`'
single-grant, fail-closed counterpart: any lookup error (including a
`RoleStore` with no `smeldr_governance_audit` table wired at all) is
returned as an error, never silently absorbed. `RoleGrant.Grantor` — the
token ID of the grant's own original creator, read from the earliest
matching `smeldr_governance_audit` row — is populated by `GetGrant`
(fail-closed) and, best-effort, by `ListGrants` (fail-open: left empty
rather than failing the whole call when the enrichment can't run).
`RoleStore.ListRoles(ctx) ([]RoleDefinition, error)` is `GetRole`'s bulk
counterpart, returning every role's `Name`/`Operations`/scope shape. Both
`GetGrant` and `ListRoles` back `smeldr.dev/mcp`'s `withdraw_delegation` and
`list_roles` tools respectively — see that module's own docs.

### Authority Check (decision-governance-model.md §4)

Check is an enforced *precondition* on a Decision's `proposed → ratified`
transition: before ratification is allowed to proceed, Smeldr looks for an
existing Rule or AuthorityStub sharing the Decision's own `RuleType`, and
records what it found. Check is advisory and recording, not a gate — it
never blocks ratification, even when it finds a conflict; a separate,
not-yet-built "declared tension" mechanism is what will make "ratified
anyway" itself a recorded act.

Enable it once, on `App`:

```go
app.Check(smeldr.NewCheckStore(db))
```

`nil` (the default) leaves Check disabled — no behaviour change for an
app that never calls `App.Check`.

```go
type CheckRecord struct {
    ID, SubjectType, SubjectID, RuleType string
    RanAt                                time.Time
    Found                                bool
    MatchType, MatchID, MatchName        string
    Sentence                             string
}

type CheckStore interface {
    Append(ctx context.Context, r CheckRecord) error
    Last(ctx context.Context, subjectType, subjectID string) (CheckRecord, bool, error)
    List(ctx context.Context, subjectType, subjectID string, limit int) ([]CheckRecord, error)
}

func NewCheckStore(db smeldr.DB) CheckStore
func CreateCheckTable(db smeldr.DB) error
```

`CreateCheckTable` creates the `smeldr_check_records` table (call it from
your boot path alongside your other `Create*Table` calls — see
`example/server/main.go`'s `ENABLE_CHECK` block). `Last` returns
`found = false, err = nil` when no record exists yet for a subject — not an
error.

Candidate selection, when both a Rule and an AuthorityStub (or several of
either) share the RuleType: the most-recently-published one wins
(`PublishedAt`), the same "most recent" convention `SweepRunStore.Last`
already uses. The recorded sentence names existence, not rank —
`"this touches %q, an existing %s rule"` when found, `"no conflicting
authority found"` otherwise — since a same-RuleType match shares the
matched item's own authority rank; claiming one "outranks" the other is a
separate, larger cross-rule-type question, not built here.

Check runs on both paths a Decision can be ratified from: the HTTP
`PUT` path (`Module.updateHandler`) and `App.TransitionItem`/
`TransitionItemWithReason` — deliberately wired into both, the same
"cover every mutation path" lesson D34's `authorizeDecisionScope` left half
done (`updateHandler` only) and A307 later confirmed as its own gap. Both
sites are fail-open: a `CheckStore` outage is logged at `Warn` and never
surfaces to the caller or blocks the transition.

`authorizeDecisionScope` itself is now wired into both paths too (closed
by `authorizeDecisionScopeByID`, a `TransitionItem`-specific variant that
fetches `Scope` by id since `TransitionItem` has no typed `*Decision`
struct in hand) — unlike Check, this is a blocking authorization gate, so
its `TransitionItem` call runs before the row's `UPDATE`, not after.
Only the `Scope` *lookup* is fail-open (a read error skips the check,
logged at `Warn`); once `Scope` resolves, an ungranted role still fails
closed exactly as `updateHandler`'s own call does.

A recorded Check result is readable via `smeldr.dev/mcp`'s `get_check_status`
tool (Author role, `subject_type`/`subject_id` params) — see that module's
own docs; this tool requires a `smeldr_tool_policies` seed row
(`{"get_check_status", "read"}`, added alongside `get_stewardship_inbox`'s
own row) or every governance-enabled caller is silently forbidden, the same
gap A298/A311 already closed for `get_sweep_run`/`get_stewardship_inbox`.

### Severity and rule type ranks (decision-governance-model §3, §6)

```go
type Severity string // "low", "medium", "high", "critical"

const (
    SeverityLow      Severity = "low"
    SeverityMedium   Severity = "medium"
    SeverityHigh     Severity = "high"
    SeverityCritical Severity = "critical"
)

func CreateRuleTypeRankTable(db DB) error
func SetRuleTypeOrder(ctx context.Context, db DB, names []string) error
func RuleTypeRank(ctx context.Context, db DB, name string) (rank int, ok bool, err error)
func SeverityOf(ctx context.Context, db DB, rs *RelationStore, anchorType, anchorID, ruleType, kind, direction string, maxDepth int) (Severity, error)
```

`Severity` is a closed ordinal scale, compared by rank, not a numeric score; an unknown value ranks lowest.

**`CreateRuleTypeRankTable`** creates `smeldr_rule_type_ranks (name, rank)` if it does not exist. Call it at startup alongside `CreateAuthorityTables`; the example server does under `ENABLE_AUTHORITY` (T949).

**`SetRuleTypeOrder`** declares the whole ordering of rule types, weakest first: the rank is the index in `names`. It is a full replace, not a merge: a name left out loses its rank. It does not run in a transaction, so a failure part way leaves a partial ordering and returns the error.

**`RuleTypeRank`** returns a name's rank; `ok` is false when the name is not registered, never a silent 0.

**`SeverityOf`** returns the higher of two values:

1. **Fan-out:** `rs.Reachability(ctx, anchorType, anchorID, kind, direction, maxDepth)` from the anchor (direction `incoming`, `outgoing` or `both`), bucketed by the total number of items reached: 0 is low, under 5 medium, under 20 high, otherwise critical.
2. **Rule type floor:** the position of `ruleType` in the registered ordering. An empty or unregistered rule type gives low; a single registered rule type gives critical; otherwise the position rank / (count - 1) gives critical at 0.75 or more, high at 0.5, medium at 0.25, else low.

The floor only raises the fan-out, never replaces it: explicit edges are sparse near the top of the rule type hierarchy, so fan-out alone undercounts exposure where the stakes are highest. Pass `""` as `ruleType` when the anchor has none; `SeverityOf` does not look it up itself. A reachability error is returned. With a non-empty `ruleType` and no `smeldr_rule_type_ranks` table, the rank lookup fails and `SeverityOf` returns that error, so create the table first. No MCP tool, CLI command or HTTP route calls these today; they are for Go embedders.

### Capabilities (D94, A499)

A capability is one act on one resource. It is the unit that the MCP, REST and CLI surfaces each project (D94). Types are an argument of the `item` resource, so adding a content type never adds a capability.

```go
type ReasonRule string

const (
    ReasonNotTaken ReasonRule = "none"     // the act takes no reason
    ReasonOptional ReasonRule = "optional" // a reason may be given, and it is recorded
    ReasonRequired ReasonRule = "required" // the act is refused without one
    ReasonPerFlow  ReasonRule = "flow"     // the type's state flow decides per transition (D96)
)

type Capability struct {
    Resource   string     // one of the Resource* constants
    Verb       string     // a standard verb (Verb* constants) or a custom one
    Operation  string     // the governance operation word that gates it (D43)
    ReasonRule ReasonRule
    Custom     bool       // true when Verb is not a standard verb
}

const (
    ResourceItem     = "item"
    ResourceType     = "type"
    ResourceRelation = "relation"
    ResourceSignal   = "signal"
    ResourceAccess   = "access"
    ResourceOps      = "ops"
    ResourceSite     = "site"
    ResourcePageMeta = "page_meta"
    ResourceWebhook  = "webhook"
)

const (
    VerbGet        = "get"
    VerbList       = "list"
    VerbCreate     = "create"
    VerbUpdate     = "update"
    VerbDelete     = "delete"
    VerbTransition = "transition"
)

func (a *App) Capabilities() []Capability                             // a copy, fixed order; safe on a nil *App
func (a *App) CapabilityFor(resource, verb string) (Capability, bool) // false for an unknown pair; one map lookup, no allocation; safe on a nil *App
```

- One governance operation word applies per (resource, verb). The words are the existing ones; no new operation word.
- The registry is read-only. Nothing reads it yet, and no tool, route or schema changes. It is the base for the capability authorization step (C2) and the opt-in consolidated MCP profile (C3), which come later.
- `access delegate` and `access withdraw_delegation` are floors. The per-item check runs inside the act, against the actual target.
- A coverage test checks three things:
  - every built-in MCP tool in core's seeded tool policies maps to a capability;
  - every capability names a known operation word;
  - a tool's seeded operation agrees with its capability's, except two named divergences that the authorization step (C2) resolves: `create_content` and `update_content` are gated on `manage` today, while item `create` and `update` are gated on `create` and `update`; and the per-type `publish_`, `schedule_` and `archive_` tools are gated on `publish` and `archive`, while the one `transition` verb is gated on `manage` (D96). The typed tools keep their seeded rows.

| Resource | Verb | Operation | Reason | Custom |
|---|---|---|---|---|
| item | get | read | none | no |
| item | list | read | none | no |
| item | search | read | none | yes |
| item | create | create | none | no |
| item | update | update | optional | no |
| item | delete | delete | none | no |
| item | transition | manage | flow | no |
| item | compose | manage | none | yes |
| type | get | read | none | no |
| type | list | read | none | no |
| type | define | define-type | none | yes |
| type | define_flow | define-flow | none | yes |
| relation | get | read | none | no |
| relation | list | read | none | no |
| relation | assert | create | none | yes |
| relation | propose | create | none | yes |
| relation | observe | create | none | yes |
| relation | withdraw | archive | required | yes |
| relation | define | define-relation-kind | none | yes |
| relation | preview_impact | manage | none | yes |
| signal | get | read | none | no |
| signal | list | read | none | no |
| signal | create | create | none | no |
| signal | answer | update | required | yes |
| signal | withdraw | archive | required | yes |
| access | list | administer | none | no |
| access | list_roles | read | none | yes |
| access | lookup_names | read | none | yes |
| access | create_token | administer | optional | yes |
| access | revoke_token | administer | optional | yes |
| access | grant | administer | optional | yes |
| access | revoke | administer | optional | yes |
| access | delegate | read | none | yes |
| access | withdraw_delegation | read | none | yes |
| ops | get | read | none | no |
| ops | list | read | none | no |
| ops | migrate_addresses | administer | none | yes |
| site | list | read | none | no |
| site | create | manage | none | no |
| site | update | manage | none | no |
| site | delete | manage | none | no |
| site | preview | publish | none | yes |
| site | upload | create | none | yes |
| page_meta | get | administer | none | no |
| page_meta | list | administer | none | no |
| page_meta | update | administer | none | no |
| page_meta | delete | administer | none | no |
| webhook | list | administer | none | no |
| webhook | create | administer | none | no |
| webhook | delete | administer | none | no |
| webhook | retry | administer | none | yes |

**Planned change (no date promised):** D116 makes a webhook a delivery target of a Subscription. A later Task moves the webhook rows under a `subscription` resource and keeps `webhook` only as long as compatibility needs it (D97). Later Tasks also add `signal answer` and `signal withdraw` (D115) and the `subscription` resource (D116, D117).

#### Authorization by capability (A503)

Two methods on `App` check a caller against a capability and list what the caller may use. Nothing in core or mcp calls them yet. The opt-in consolidated MCP profile (C3) will be the first caller.

```go
func (a *App) AuthorizeCapability(ctx Context, resource, verb string, target AuthTarget) error
func (a *App) VisibleCapabilities(ctx Context) ([]Capability, error)
```

`AuthorizeCapability` checks these rules, in this order:

1. An unknown (resource, verb) pair is `ErrBadRequest`.
2. Without governance (`App.Governance` not called), the caller's legacy role must meet the floor of the capability's operation, else `ErrForbidden`:
   - `read`, `create`, `update`, `publish` and `archive` need Author;
   - `delete` and `manage` need Editor;
   - `administer`, `define-type`, `define-flow` and `define-relation-kind` need Admin.
3. Under governance, a caller with no token ID is `ErrForbidden`.
4. A `read` capability goes through the caller's read scope: the one attached to ctx (`WithReadScope`, `TrustedRead`), or else the caller's own, loaded from its grants. It never asks `RoleStore.Authorized` for "read" with an empty target.
   - With a type and no ID, the type must be readable (`ReadScope.TypeReadable`), else `ErrForbidden`.
   - With a type and an ID, a type the caller cannot read is `ErrForbidden`. An item it cannot read inside a readable type is `ErrNotFound`, the same answer as a missing item.
   - With no type, the caller needs a scope that reads everything (`ReadScope.All`). The exceptions are `signal list` and `relation list`, whose surfaces filter per item: those pass when the caller reads at least one item. This fails toward less access.
   - Only `item get`, `item list` and `relation get` use the target's type. Any other read capability ignores a type it is given and applies the rule for a read with no type, so passing a type cannot open a read whose surface does not filter by it (`ops get` returns aggregates over every item).
5. Any other operation: with a type and an ID, an item the caller cannot read is `ErrNotFound` before its grant is checked, so a write never reveals what a read hides. Then `RoleStore.Authorized` decides for the operation and the target; false is `ErrForbidden`.

A failure to load the scope or the grants is `ErrInternal`: never a pass, and never not-found. The state flow's own per-transition gate and reason rule stay inside `App.TransitionItemVia` (D96); `item transition` here is only the tool-level `manage` gate.

The capability's operation governs the capability path, and the typed MCP tools keep their seeded tool policies. So on a capability path a dynamic item create is gated on `create` (Author without governance), where the typed `create_content` tool stays on `manage` (Editor).

`VisibleCapabilities` returns the capabilities the caller may use on at least one target, in registry order, so a tool list built from it is deterministic:

- Without governance: every capability whose operation's legacy floor the caller's role meets.
- Under governance, with no token ID: none.
- Under governance, a `read` capability is visible when the scope reads everything, or when it reads at least one item and the capability is called with an item type (`item get`, `item list`, `relation get`) or filters per item (`signal list`, `relation list`). Any other capability is visible when one of the caller's unexpired grants holds its operation, in any scope mode.
- A failure to load the scope or the grants is `ErrInternal` and no list: never a full or partial one.

Cost: a read with an attached scope runs no query and does not allocate. Otherwise one grant query, plus one per dynamic anchor (as `LoadReadScope`). `VisibleCapabilities` runs one grant query plus the scope. Nothing is cached.

| `AuthorizeCapability` returns | When |
|---|---|
| `nil` | allowed |
| `ErrBadRequest` | unknown resource or verb |
| `ErrForbidden` | below the legacy floor; no token ID under governance; a read of a type the caller cannot read; no grant for the operation; a type-less read without a scope that reads everything (except `signal list` and `relation list`) |
| `ErrNotFound` | a read of an item the caller cannot read in a readable type; any other operation on an item the caller cannot read |
| `ErrInternal` | the scope or the grants could not be loaded |

Over smeldr.dev/mcp these map to -32001 for `ErrForbidden`, -32000 for `ErrNotFound` (since mcp v1.61.0), -32602 for `ErrBadRequest`, and -32603 for `ErrInternal`.

---

### Search (T885)

`App.Search` finds records and content items by text across every record type the caller may read. It is a plain case-insensitive substring match over each type's searchable fields. There is no semantic search and no full-text index, and the results are the same on SQLite and Postgres. Read rules apply before anything is returned or counted: a record the caller cannot read is not a hit and does not change `Total`, and a Signal whose subject is hidden is hidden too. Hits are sorted newest first (`UpdatedAt`) and paged after the read filter.

```go
res, err := app.Search(ctx, smeldr.SearchQuery{
    Text:  "release plan",
    Types: []string{"Decision"},
    Place: smeldr.SearchPlaceNone,
})
if err != nil {
    return err
}
for _, hit := range res.Hits {
    fmt.Println(hit.Type, hit.Address, hit.Title, hit.Snippet)
}
```

```go
type SearchQuery struct {
    Text   string   // substring to match; optional when a filter is set
    Types  []string // record type names; empty means every type
    States []string // state names
    Place  string   // "" any place; SearchPlaceNone for the unplaced inbox; or a Domain/Area by address, slug or ID
    Limit  int      // default SearchDefaultLimit (50), max SearchMaxLimit (500)
    Offset int
}

type SearchHit struct {
    Type, ID, Slug, Address, Title, State, Field, Snippet string
    UpdatedAt time.Time
}

type SearchResult struct {
    Hits  []SearchHit
    Total int
}
```

Constants: `SearchTextMinLength = 2`, `SearchTextMaxLength = 200`, `SearchDefaultLimit = 50`, `SearchMaxLimit = 500`, `SearchPlaceNone = "none"`.

- `Place: ""` matches any place. `Place: SearchPlaceNone` matches only records with no live asserted `belongs_to_domain` or `belongs_to_area` edge, the inbox of unplaced records (for example imported Decisions).
- A place the caller cannot read answers `ErrNotFound`, the same as a missing one.

**Searchable fields.** A type decides which of its fields are searched:

| Where | How to mark it | Default when unmarked |
|---|---|---|
| Compiled type (Go struct) | `smeldr_search:"text"` on a string field | Only tagged fields are searched. Core tags Task and Goal title and description, Decision title, scope and body, Amendment summary and body, and Signal message, answer and resolution. |
| Runtime-defined type | `"search": true` on a schema field (`SchemaField.Search`) | If no field sets it, the string fields with role `title`, `description`, `body` or `summary`, or format `markdown`, are searched. So a `task_plan` body and summary are searchable with no change. |

**Errors:**

| Error | When |
|---|---|
| `ErrBadRequest` | no text and no filter; text under 2 or over 200 runes; negative `Limit` or `Offset`; a type that is not a registered record type |
| `ErrNotFound` | `Place` is missing or unreadable |
| `ErrInternal` | the read scope or the query failed; never a partial result |

**Capability:** `item search` (operation `read`). A caller who reads at least one item of any type may use it, and each hit is filtered by the read rules.

**MCP tool `search`** (smeldr.dev/mcp v1.65.0, author role). Arguments: `query`, `types` (array), `states` (array), `place`, `limit`, `offset`. The result has `hits`, `count` and `total`. Each hit carries `awaits_action`; `standing` is set for types with standing; `state_since` and `state_reason` are set for Task and Goal.

**CLI** (smeldr.dev/cli v0.28.0):

```
smeldr-cli search [<text>] [--type t]... [--state s]... [--place p|none] [--limit n] [--offset n] [--json]
```

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

### Page meta overrides (operator-managed)

`PageMetaStore` provides a database-backed SEO override layer for URL paths. It sits between the content item's own `Head()` and the global `SiteConfig`/`OGDefaults` fallback.

**Create the table once at startup:**
```go
smeldr.CreatePageMetaTable(db)
store := smeldr.NewPageMetaStore(db)
app.PageMeta(store)
```

**In list modules:** when no `ListHeadFunc` is configured, `renderListHTML` automatically looks up the request path in the store and applies any stored override.

**In custom handlers:** call `App.GetPageMeta` to retrieve a `Head` value for any path:
```go
head := app.GetPageMeta(ctx, r.URL.Path)
```
Returns a zero `Head` when no override is stored for the path.

**Databases (v1.119.3).** `PageMetaStore` works on SQLite and on Postgres, including through `smeldr.dev/core/pgx`: `Set` replaces the stored row with the values given (all three), and the statements use numbered placeholders and `ON CONFLICT (path) DO UPDATE`. Before v1.119.3 `Set`, `Get` and `Delete` failed on Postgres (`?` placeholders and `INSERT OR REPLACE`, SQLSTATE 42601), so `set_page_meta`, `get_page_meta` and `delete_page_meta` did not work there. `List` orders by the database's own collation, so two paths may sort differently on SQLite and on Postgres when they differ only in punctuation.

**Manage overrides via MCP** (Admin role — requires `mcp.WithPageMeta(db)`):
- `set_page_meta` — upsert title/description/og:image for a path
- `get_page_meta` — retrieve current overrides
- `delete_page_meta` — remove overrides (falls back to content type Head)
- `list_page_meta` — list all stored overrides

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

**Creates run in one transaction (D110).** Every create (the compiled MCP create and HTTP POST, dynamic content creates, and Signals) runs its pre-save check and INSERT in one transaction when the database handle supports BeginTx. Events, standing, provenance and the after-save step run only after commit, so nothing is announced for a row that rolled back. Dynamic content creates open one transaction per slug attempt, because Postgres aborts a transaction after a failed INSERT. A database handle without BeginTx runs the same statements without a transaction, as before. A Repository that is not an SQLRepo (MemoryRepo, or your own) is used as is.

`ParseTimestamp(v any) (time.Time, error)` reads any stored time value into a UTC `time.Time` (A505). It accepts a `time.Time`, an `int64` of Unix seconds, or a `string` or `[]byte` in RFC3339Nano, RFC3339 or Go's `time.Time.String` form, which is what SQLite drivers write for a `TIMESTAMPTZ` column. `nil` returns the zero time. It is the one parser for every stored form, so a surface that reads a time column as text can return it as RFC3339, like every other record (D114 point 4). `smeldr.dev/mcp`'s `list_signals` uses it from its next release.

### Schema migration (T246)

`Create*Table` functions (`CreateSiteConfigTable`, `CreateBlockTables`, etc.) use `CREATE TABLE IF NOT EXISTS` — correct for a fresh install, but a no-op against a table that already exists in an older shape. `EnsureColumn` adds one column to an existing table when it is absent:

```go
func EnsureColumn(ctx context.Context, db DB, table, column, columnDDL string) error
```

```go
if err := smeldr.EnsureColumn(ctx, db, "smeldr_site_configs", "custom_field", "TEXT NOT NULL DEFAULT ''"); err != nil {
    log.Fatal(err)
}
```

Idempotent — safe to call on every application startup. Works on SQLite and Postgres (v1.118.0, D103): the column is looked up by selecting it from the table, and added with a plain `ALTER TABLE ... ADD COLUMN`; a column another process adds in between is not an error, and a probe that fails for any other reason is returned, not skipped. Additive only — never drops a column, so a downgrade after `EnsureColumn` has run simply leaves one unused column present, never lost data.

**Ownership:** call `EnsureColumn` for a column your own type declares, at your own application's startup, the same way `Create*Table` is already called — there is no central registry of "columns the framework knows about." An application extending a framework-provided table (for example, adding custom fields to `SiteConfig`) calls `EnsureColumn` itself for its own added columns.

**Renaming a module's legacy tables (v1.136.0).** `RenameLegacyTables` renames each table of a list of pairs that still exists, the way core moved its own `forge_*` tables to `smeldr_*`:

```go
err := smeldr.RenameLegacyTables(ctx, db, [][2]string{{"forge_media", "smeldr_media"}})
```

It works on SQLite and Postgres (each table is looked up by selecting no rows from it), skips a pair whose source is absent, skips with a warning a pair whose source and destination both exist, and runs the renames in one transaction when `db` supports `BeginTx`. It is safe on every boot. A name that is not a plain lower-case identifier is a `*ValidationError`. Call it with the database, not inside a transaction: on Postgres a failed probe aborts the transaction it runs in. smeldr.dev/media and smeldr.dev/social use it.

**Which modules run on Postgres.** Core, smeldr.dev/mcp, smeldr.dev/media and smeldr.dev/social use the application's database and run on SQLite and Postgres (media from v1.7.0, social from v0.11.0). smeldr.dev/oauth keeps its state in its own SQLite file, never the application's database, by Decision D106: an application on Postgres can use it as it is, and a deployment that needs OAuth state elsewhere implements `oauth.Store` (and `oauth.RegistrationStore` for Dynamic Client Registration) and passes it to `oauth.New(cfg, store)`.

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

**Only anonymous responses are cached (A493).**
- **`InMemoryCache`** cannot see the user, so it bypasses any request that carries an `Authorization` header, any cookie, or a `preview` query parameter (`X-Cache: BYPASS`). It never stores a response that sets a cookie or whose `Cache-Control` says `private` or `no-store`.
- **The module cache, `Cache(ttl)`**, serves and stores only the guest's view: a caller with an identity or a role, and a preview response, get `X-Cache: BYPASS`.
- Before core v1.145.1, an authenticated or draft response could be stored and served to the next anonymous caller on the same URL.

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
| `smeldr_excerpt` | string, int | string | Truncated plain-text summary on one line: markdown stripped (`StripMarkdown`), escaped by html/template in its context (v1.143.0, A466) |
| `smeldr_meta` | string | `template.HTML` | Escaped `<meta>` attribute value |
| `smeldr_csrf_token` | — | `template.HTML` | Hidden CSRF input field |
| `smeldr_llms_entries` | `.` (template data) | `template.HTML` | Rendered llms.txt entry list |

#### smeldr_markdown

`smeldr_markdown` (and its alias `markdown`) converts a Markdown string to HTML for
**trusted site authors**. Inline text is escaped, but a line that starts with `<` is
emitted verbatim (see below), so never use it for text from an untrusted author: use
[`RenderMarkdown`](#rendering-markdown-bodies-rendermarkdown-and-stripmarkdown), which
escapes everything. The subset is the one `RenderMarkdown` describes, with two template
differences: headings keep their level (`<h1>` to `<h6>`), and `>` block quotes stay
literal text.

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

### Rendering markdown bodies: `RenderMarkdown` and `StripMarkdown`

**v1.143.0, A465.** One renderer and one stripper for markdown bodies such as a Decision's text, for a caller that shows them outside site templates (Cloud's read layer, for example).

```go
html := smeldr.RenderMarkdown(decision.Body) // template.HTML, safe for any author
preview := smeldr.StripMarkdown(decision.Body) // plain text, never longer than the input
```

**`RenderMarkdown` never passes raw HTML.** Every character of the input is escaped, including a line that starts with `<`. The `markdown` and `smeldr_markdown` template funcs do pass such a line through verbatim, for trusted site authors. So never use a template func for untrusted text.

**The subset:**
- **Blocks:**
  - ATX headings: in a body every level renders one step, `<h3 class="md-h">`, so a body does not take over the page's outline;
  - fenced code with a language class;
  - `---` rules;
  - tables with a separator row: an escaped `\|` and a pipe inside a code span stay in the cell, and a ragged row is padded or cut to the header's width;
  - paragraphs;
  - `- ` bullets and `1. ` or `1) ` numbered lists (`<ol start="n">` when the first number is not 1), with one nested level (a marker indented by two or more spaces; deeper levels flatten into it), and an indented line continues the item above it. A numbered line interrupts a paragraph only when it is `1.`;
  - `> ` block quotes, one level (`>>` flattens).
- **Inline:**
  - `` `code` `` is taken first, so nothing inside it is interpreted;
  - `**strong**`, `__strong__`, `*em*` and `_em_`: emphasis pairs only at word boundaries, so `snake_case` and `2*3*4` stay as written;
  - `\` before punctuation escapes it;
  - links `[label](target)`.
- **Stays literal text:** images, setext headings, reference links and bare URLs.

**Links:** a target becomes an `href` only when it is `http` or `https` with a host, or relative with neither scheme nor host (`/docs`, `docs/x`, `../a`, `#sec`, `?q=1`). Anything else is refused and the whole `[label](target)` stays literal text: `javascript:` in any case, `data:`, `vbscript:`, `mailto:`, `//host`, a backslash, whitespace, a quote, `<`, `>` or a backtick, a control character or its `%` escape, or an HTML character reference. There are no `target` or `rel` attributes; the caller decides.

**Bounds:** the parser uses no regular expressions. Its look-ahead work is capped at 4 steps per input byte plus a constant (a normal body uses about 0.2), so it is linear. It allows at most 4096 emphasis delimiters per inline run and link labels nested three deep, and input over 1 MiB is not parsed. When any bound is hit, or the parser fails in any way, the whole text renders as escaped preformatted text, `<pre class="md-raw">…</pre>`. Nothing is cut, dropped or interpreted: a Decision body is the record. `StripMarkdown` returns its input unchanged in that case.

**`StripMarkdown`:**
- heading, list, quote and fence markers go;
- a table row becomes its cells separated by spaces, and the separator row goes;
- a link or image becomes its label;
- a code span keeps its content;
- emphasis markers go only where they pair.
- Each block stays on its own line, and the result is never longer than the input.
- It is also what a `text/plain` response of a [`Markdownable`](#content-negotiation) item uses.

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

A slug is set once, when the item is created, and never changes: an update
that sends a different `slug` is refused. Smeldr does not record old URLs or
add redirects on its own; a redirect is always an explicit entry, in code
(`app.Redirect`, `Redirects(From(...))`) or at runtime (`create_redirect`,
`smeldr-cli redirect create`).

### What a public URL answers

| Item state | Response at its URL |
|-------|-----------------------|
| Published | `200` |
| Draft / scheduled | `404` (does not leak existence) |
| Archived | `404` |
| Deleted | `404` |

**Where a redirect entry applies.** An entry answers any path that would
otherwise answer `404`: a path outside every module (for example the old
prefix after a module moves, or a retired page), and an item's own URL
under a live module prefix when that item is archived, deleted, not yet
published or never existed. That holds for compiled modules (including
`SingleInstance`) and for dynamic content types. A prefix entry such as
`Redirects(From("/posts"), "/articles")` therefore also covers the missing
and archived items of a module still mounted at `/posts`.

A live item always wins: a Published item, or a draft opened with a valid
preview token, is served even when an entry exists for its path, so a stray
entry never hides content. The answer for a hidden item depends only on the
entry, never on the item: a draft and a slug that never existed answer the
same `301`, `410` or `404`, so the response does not leak that the draft
exists. A found item never touches the redirect table (D24); only the `404`
path pays one lookup.

### 410 versus 404

`410 Gone` tells search engines the content was *intentionally* removed.
Google de-indexes `410` pages significantly faster than `404` pages. When
you archive or delete a page that was public, add a `Gone` entry for its
URL (`app.Redirect("/posts/old", "", smeldr.Gone)` or `create_redirect`
with `code` 410).

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

### Discovery — `list_type_tools` (A207)

With 150+ tools on a real deployment, AI clients that do semantic tool-search
find tools by what they search for — not by what exists. An agent that discovers
`update_essay` has no guaranteed path to `list_essays` or `get_essay`.

`list_type_tools` closes this gap. Given any tool name for a compiled content
type, an agent can look up all sibling tools in one call:

```json
// Request: list_type_tools({type_name: "essay"})
// Response:
{
  "type_name": "essay",
  "tools": [
    "create_essay", "update_essay", "publish_essay",
    "schedule_essay", "archive_essay",
    "list_essays", "get_essay", "delete_essay"
  ]
}
```

| Tool | Role | Description |
|------|------|-------------|
| `list_type_tools` | Author | Returns all MCP tool names registered for a compiled content type. Input: `type_name` (snake_case, e.g. `"essay"`). For `SingleInstance` modules, `list_X` is absent. Returns not-found for unknown types. |

Always present in `tools/list`. Response is derived live from the registered
tools — cannot drift from the actual tool set.

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

`MustConfig` merges the file, then requires an absolute `BaseURL` and a
`Secret` of at least 16 bytes, and panics otherwise. `ValidateConfig` applies
the same rules and returns the error instead, so a caller can refuse before it
writes anything (T969):

```go
func ValidateConfig(cfg Config) (Config, error) // merged Config, or a *ValidationError on smeldr_config, base_url or secret
```

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
{"status":"ok","core":"x.y.z"}
```

When companion modules such as `smeldr.dev/mcp` are linked into the binary, their
versions appear alongside:

```json
{"status":"ok","core":"x.y.z","mcp":"x.y.z"}
```

The same version data is written to stderr at startup:

```
smeldr: core x.y.z, mcp x.y.z
```

Versions are read from the binary's embedded build info
(`runtime/debug.ReadBuildInfo`). For a binary built under a `replace`
directive (e.g. `example/server`'s own `replace smeldr.dev/core =>
../..`), the reported version reflects what was actually linked, not the
nominal `go.mod` require line — an unversioned local-path replace reports
Go's own `"(devel)"` marker rather than a stale version number (T219).

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
func (a *App) OnSignal(sig LifecycleEvent, h func(context.Context, SignalEvent) error) *App
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

### `CreateWebhookTables` (T947)

```go
func CreateWebhookTables(db DB) error
```

Creates the tables outbound webhooks need, if they do not exist: `smeldr_webhook_endpoints`, `smeldr_outbound_jobs` (with `event_id`, D117) and `smeldr_delivery_logs`, plus the indexes the delivery worker reads by (`idx_smeldr_outbound_jobs_due` on `status, next_retry_at`, `idx_smeldr_delivery_logs_job` on `job_id`), then adds `event_id` to a job table from before D117 (`EnsureOutboundJobEventIDColumn`). Call it before `App.Webhooks`: without the job table no event is delivered, and `Enqueue` logs "no such table". Idempotent and safe on every boot; existing rows are kept. SQLite and Postgres. A failing statement is returned with its table named. The example server calls it under `ENABLE_WEBHOOKS`.

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
loopback IPs) with `ValidateWebhookURL`, which is exported so a caller can
refuse a bad URL before it writes anything (T955):

```go
func ValidateWebhookURL(rawURL string) error // a *ValidationError on field "url"; resolves the host
```

Secrets are encrypted with AES-256-GCM using a key derived from
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
const AfterSchedule LifecycleEvent = "after_schedule"
```

Fires after a node transitions to `Scheduled` status via `MCPSchedule`.
Subscribe with `m.On(smeldr.AfterSchedule, ...)` or use it as a webhook
event name suffix (`"mytype.scheduled"`).

### Orchestration transition events (T231)

Every status change of an item fires one `"{type}.transitioned"` event, e.g.
`task.transitioned`, on every path (v1.137.0; D107): HTTP `PUT`,
the MCP `publish_*`, `schedule_*` and `archive_*` tools, the scheduler,
`App.TransitionItem`/`TransitionItemWithReason` (the MCP `transition_item`
tool), a runtime-defined type's status change (`set_content_status`,
`DynamicTypeRepo.SetStatus`/`ScheduleContent`), an item a conflict supersedes,
and `DrainEvalQueue`. Before, only `App.TransitionItem` fired it, and only on
a compiled type.
The payload's `data` object carries `type`, `id`, `slug`, `from_state`,
`to_state`, and `reason` (when supplied). Since v1.122.0 (A427) it also carries `actor_id` and `actor_kind`: who caused the transition, the same id and kind the provenance record of that transition holds (`actor_kind` is `job`, `agent`, `human` or `unclassified`, D105; see Actor classification under Tokens). Both keys are omitted when no actor is known, and **webhook deliveries carry them exactly like the event stream does**, so an admin who forwards deliveries to an external system decides what to forward. The id is opaque and is the same one `last_actor` already holds; resolve it to a name with `lookup_token_names`. `signal.transitioned` from core's Signal resolver carries `job` and `signal-resolver` (D115). This is a separate delivery path
from the `App.OnSignal` bus above — the bus's `LifecycleEvent` vocabulary
(created/updated/published/…) doesn't fit an arbitrary per-flow state name,
so subscribe to `"{type}.transitioned"` specifically, not the created/
updated/… events, to observe orchestration state changes.

On the live event stream (`/_events/stream`, below) `signal.transitioned` and
`amendment.transitioned` go to topic channels, not a role channel (v1.129.0):
see "Topic channels".

A `Signal` created by `DrainEvalQueue` when an automated transition hits a
role-gated boundary (D42) fires the same `"signal.created"` event a
human-created Signal already produces via the normal create path — a
webhook subscriber cannot distinguish the two by event name.

### Webhook delivery

- **Signing:** HMAC-SHA256 of `"<unix_ts>.<body>"`. Header:
  `X-Smeldr-Signature: sha256=<hex>`.
- **Headers:** `X-Smeldr-Event`, `X-Smeldr-Delivery` (UUIDv4), `X-Smeldr-Timestamp`.
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

## Event stream

### GET /_events/stream

Live event stream delivering content-lifecycle and state-flow events as
NDJSON over a held-open chunked HTTP response — not the `text/event-stream`
SSE protocol, and explicitly not a WebSocket (100% standard library,
matching core's zero-third-party-dependency principle). Auth is enforced;
requires the **Author** role.

**Request:**

```
GET /_events/stream?channel=<name> HTTP/1.1
Authorization: Bearer <token>
```

**`channel` query parameter (v1.81.0+, A302):** selects which channel this
connection subscribes to.

- `?channel=core` (or any other non-empty value) — receive only events
  routed to that channel, plus every true broadcast (see below). Channel
  names are free-form strings, not a fixed enum — matched verbatim.
- `?channel=all`, or the parameter omitted/empty entirely — receive every
  event regardless of channel (the unfiltered firehose every connection
  received unconditionally before v1.81.0). This is the default specifically
  so every caller that connected before channels existed keeps working
  unmodified.
- No new authorization is added for `?channel=all`: it is the same access
  every Author-role token already had, not a new grant.

**`include_own` query parameter (v1.107.0):** by default a connection does
not receive an event its own token caused, see "Self-echo" below. **A
consumer that holds the stream with a service token (for example an
organisation's bootstrap token) therefore never sees that token's own
writes. Silence on the stream is not proof that nothing changed.**
`?include_own=true` opts the connection back in to its own events; any
other value, or the parameter omitted, keeps the default. It combines with
`channel`, e.g. `?channel=core&include_own=true`.

**Response on success (200 OK):**

```
Content-Type: application/x-ndjson
Cache-Control: no-cache
Connection: keep-alive
X-Accel-Buffering: no
```

Headers are flushed immediately. The connection is held open and each event
is delivered as one line of JSON (NDJSON format), flushed immediately after
each write. A periodic `{"type":"ping"}` heartbeat is sent every 25 seconds
while idle to keep the connection alive through reverse proxies.

The connection ends when the client disconnects, if a write to the client
fails (e.g. broken pipe), or when `App.Run` begins a graceful shutdown: every
stream is released at once, so open streams no longer hold the shutdown for
its full 5 seconds. Other long-lived handlers that return on
`r.Context().Done()` (such as MCP SSE) end 1 second into the shutdown, when
`Run` cancels every request context; registered modules are stopped even if
the shutdown times out.

**Response on auth failure:**

401 Unauthorized (missing or invalid token) or 403 Forbidden (insufficient role).

**Response on connection-cap exceeded (v1.73.1+, T271):**

429 Too Many Requests — returned when the calling token already holds 4
concurrent `/_events/stream` connections (`eventStreamMaxSubscribersPerToken`,
not configurable in this version). Returned before any header is written, so
the response is a normal error body, not a truncated stream. Bounds a runaway
reconnect loop or a compromised token; a well-behaved listener holding its
one normal connection never approaches this limit.

### EventStream app wiring

```go
app.EventStream()  // mounts GET /_events/stream; returns *App (chainable)
```

`App.EventStream` is optional and independent of `App.Webhooks()`. Calling
it installs an in-memory broadcaster and mounts the stream route. Without
calling it, the route does not exist (404). Both can be configured on the
same `App` instance to deliver the same events to both webhooks (outbound POST)
and stream consumers (persistent connection).

### Events delivered

Every content-lifecycle signal and state-flow-transition event already mapped
to a webhook event name is delivered to the stream:

- Content lifecycle: `AfterCreate`, `AfterUpdate`, `AfterPublish`,
  `AfterUnpublish`, `AfterArchive`, `AfterDelete`, `AfterSchedule`
  → `"{type}.created"`, `"{type}.updated"`, `"{type}.published"`,
  `"{type}.unpublished"`, `"{type}.archived"`, `"{type}.deleted"`,
  `"{type}.scheduled"`
- State transitions: `"{type}.transitioned"` (item moves between states), once
  per status change on every path (v1.137.0; D107). From the same
  version `"{type}.updated"` means a content edit only: a status-only `PUT` no
  longer sends it.
- Signals: `"signal.created"` (new explicit signal). Every `signal.created` and
  `signal.transitioned` carries the Signal's `address` (`S3011`), as Task events do,
  whichever path created or moved it: mcp's `create_signal` (through
  `NotifySignalCreated`), a condition core emits or resolves, a transition (T872). The `"signal.expiry_swept"`
  summary (v1.129.0 to v1.158.0) is gone with the expiry sweep (D115).
- Relations (v1.129.0): `"relation.asserted"` and `"relation.ended"`, also to
  webhook endpoints subscribed to them. `data` is `{type: "relation", id,
  relation_kind, edge_class, source_type, source_id, source_address, target_type,
  target_id, target_address, actor_id, actor_kind}`, enough to re-read both ends.
  `source_address` and `target_address` (T872) are each omitted when that end has no
  address (a compiled application type). The read check on delivery is unchanged: a
  subscriber gets the event only when it may read both ends, so an address is never
  shown to someone who cannot read its record. `relation.asserted`
  adds `new_row` (true when the assert began a new life of the relation,
  false when it touched a live one again). `relation.ended` adds `cause`
  (`withdrawn`, `swept`, `recomputed`, `amendment-rejected`, `purged`, `moved`) and
  `reason`. Every end passes one place, so an ended relation always gets both
  its provenance record and this event.

Events are re-read hints, never a source of truth: on any event, re-read the
item.

The JSON payload shape is identical to webhook event payloads — a
`WebhookEventPayload` envelope with `id`, `event`, `timestamp`, and `data` fields.

### Delivery semantics

- **At-most-once:** If a client is disconnected when an event fires, the event
  is not delivered to that client. Reconnecting starts fresh with no catch-up
  or backfill.
- **Channel-scoped delivery (v1.81.0+, A302; extended v1.89.2+, 01a0a683):**
  a `Task`/`Goal` event is routed by the item's own `Band`; a `Decision`
  event by its own `Scope` (including the literal value `"cross-cutting"`,
  used verbatim as a channel name — not auto-broadcast); a `Signal` by its
  own `Receiver`. This applies to every event those four types produce,
  `"{type}.transitioned"` (A302; except `signal.transitioned`, which goes to
  the `signals` topic since v1.129.0) and `"{type}.created"`/
  `"{type}.updated"`/etc. (01a0a683 — before this, those events reached
  every subscriber regardless of channel, an unclosed gap in A302's own
  scoping). Every generic content-module lifecycle event (a blog post's
  own publish/update/etc.) has no such field and is always delivered as a
  true broadcast, reaching every connected subscriber regardless of the
  channel it requested. (`Amendment` events go to the `amendments` topic, see
  below.) A subscriber
  connected with `?channel=all` (or no parameter — see above) also receives
  everything, channel-routed or broadcast alike. A listener that only cares
  about certain event *types* (as opposed to channels) must still filter
  those client-side — channel scoping and event-type filtering are
  different dimensions.
- **Self-echo (v1.107.0):** an event is not delivered to a connection whose
  own token caused it. The comparison is on the token's `User.ID`, the same
  value the stream subscribes a connection under and a request context
  carries as `User().ID`. It covers transitions, creates and updates, and a
  Signal created through `NotifySignalCreated`. Events caused by anyone else
  still arrive, and so do system-originated ones, which have no actor: the
  condition Signals (`ratification-required`, `authorization-required`,
  `conflict-detected`) and their resolution by core (D115). Suppression is per token, not per connection, so two sessions that
  share a token do not see each other's events; `?include_own=true` restores
  that. A Signal sent to yourself is suppressed like any other self-caused
  event.
- **Topic channels (v1.129.0):** events with no owning role are published on
  a named topic, never broadcast and never on a role channel. They reach that
  topic's subscribers and every `?channel=all` subscriber, so a role session
  on `?channel=core` is not woken, while a consumer of everything (Cloud's live
  updates, orch) gets them. Subscribe to one topic with `?channel=<topic>`.

  | Topic | Events |
  |---|---|
  | `relations` | `relation.asserted`, `relation.ended` |
  | `amendments` | every compiled `Amendment` event (`amendment.created`, `.updated`, `.transitioned`) |
  | `signals` | `signal.transitioned` (a caller moving a Signal) |
  | `types` | `content_type.redefined` (a runtime-defined type's schema changed; core v1.140.0) |

  `signal.created` still routes on the Signal's receiver: it is the wake-up
  for new work. Before v1.129.0 `signal.transitioned` (v1.106.0) and every
  `amendment.*` event (v1.107.0) were not streamed at all, and relations had
  no events. A runtime-defined dynamic type literally named `amendment` is an
  ordinary dynamic type and is broadcast like any other.
- **Routed runtime-defined types (held, version pending):** a dynamic type's
  events (its `"{type}.transitioned"` and status events; dynamic creates and
  edits put nothing on the stream) are a broadcast unless its schema gives a
  string field the role `channel`. Then each event goes to the channel that
  field's value names (for `task_plan`, its `band`) and to the type's topic
  `type:<type name>` (e.g. `type:task_plan`), never to other bands. An item
  whose field is empty goes to the type's topic only. A reviewer of a type
  subscribes to its topic; the `type:` prefix keeps it apart from role and band
  channels. A content edit of a runtime-defined item (`update_content`, the
  content API's PATCH) sends `"{type}.updated"` the same way (held, next version
  after the routing one); before, a dynamic edit sent no event.
- **Several channels on one connection (held, version pending):** `?channel=`
  takes a comma-separated list, e.g. `?channel=architect,type:task_plan`. The
  connection receives an event published to any of them, once, even when it
  was published to two. Spaces and empty entries are ignored; `all` anywhere in
  the list means everything. An older core reads the whole list as one channel
  name and delivers nothing to it, so switch a watcher to a list only after the
  server runs a version with this.
- **Client buffer:** each connection buffers 256 events. A connection whose
  buffer fills is **closed** (v1.160.0, D117), logged at Warn with its token,
  and never blocks the others. Before v1.160.0 the buffer held 32 and events
  past it were dropped silently. A closed client reconnects; with the event log
  it resumes with `?after=` and loses nothing the log kept.
- **Read rules at delivery (v1.160.0):** under governance, a connection loads
  its token's read scope when it connects, and every event, live or replayed,
  is checked against it at delivery, never at publish:
  - an event about an item the scope cannot read is not delivered;
  - a relation event needs both ends readable;
  - a Signal event needs the Signal and its subject readable;
  - an event about no item (`content_type.redefined`) is delivered.

  Reloading the scope during a long connection comes with subscriptions (D116).

### Durable event log and cursor (D117, v1.160.0)

`App.EventLog(smeldr.EventLogOptions{Retention: 0})` keeps every streamed event in `smeldr_events` for the retention (`DefaultEventLogRetention`, 30 days, when zero). It needs `Config.DB` and `App.EventStream`. `App.Handler` creates the table and starts the writer; `CreateEventLogTable(db)` creates it yourself. It is opt-in: without it, the stream stays in memory and live only. example/server turns it on with `ENABLE_EVENT_LOG` (which needs `ENABLE_EVENT_STREAM`), and `EVENT_LOG_RETENTION_DAYS` sets the retention.

- **One writer, one order.** One goroutine assigns each event a `seq`, inserts a batch in one transaction, and only then delivers it. A client resuming with a cursor therefore never has seen an event the log lacks. A second process writing the same database is not supported.
- **One envelope.** Every event has one envelope ID in the log, on the stream and in the webhook outbox. Before v1.160.0 a content-lifecycle event had two different IDs, one for webhooks and one for the stream. `OutboundJob.EventID` (column `event_id`, added by `EnsureOutboundJobEventIDColumn`) names the job's log row. A table without the column still takes jobs.
- **Resume.** `GET /_events/stream?after=<event id>` works like this:
  1. It writes every logged event after that envelope ID, in order, with the connection's `channel`, `include_own` and read rules applied.
  2. It writes one `{"type":"caught_up","cursor":"<last id>"}` line.
  3. It then delivers live events. No event published during the replay is lost.

  A cursor the log does not hold, because it is older than the retention or unknown, starts at the oldest kept event after a `{"type":"skipped","after":"<id>","resumed_from":"<oldest id>"}` line. Listeners already ignore `ping`, and should ignore these lines the same way unless they use them.
  - `?after=` without the log is 400.
  - A failure reading the first page is 500.
  - A later failure closes the connection.
- **At least once.** With a cursor, delivery is at least once: an event can come twice around a reconnect or the replay boundary, so deduplicate by `id`, as for webhooks.
  - **The promise has one gap.** An event whose insert failed, or that arrived while the writer's queue (1024) was full, is still delivered live but is not logged, so it cannot be replayed.
  - `App.UnloggedEvents()` counts those events since the process started, and a Warn names the reason at most once a minute. A non-zero count is a gap in the at-least-once promise.
- **From Go.** `App.EventsAfter(ctx, afterID, limit) (EventPage, error)` reads the log after a cursor, in order. `limit` defaults to 500 and is capped at 1000. `EventPage` has `Events []LoggedEvent`, `Skipped`, `Oldest`, `Next` and `More`. `afterID` is an envelope ID or a page's `Next`, and an empty `afterID` starts at the oldest kept event.
  - Under governance it returns only events the caller in `ctx` may read, by the same rule the stream applies.
  - The read goes on past hidden events until it has `limit` readable ones, the log ends, or it has read 10 x `limit` rows.
  - **Paging:** pass `Next` back as `afterID` until `More` is false.
  - `Next` is the ID of the last event returned, or, when the read ended on hidden events, an opaque continuation (it starts with `~`, is sealed with the instance secret, and names no event and no count). So a page can be empty with `More` true when the scan bound was reached first; `More` false means the log ended.
  - A continuation this instance did not issue is `ErrBadRequest`. A continuation is bound to the instance secret: rotating `SECRET` turns open continuations into `ErrBadRequest`, and their holders resume from an envelope ID or from the start.

| `EventsAfter` returns | When |
|---|---|
| a page | events after the cursor, in order |
| a page with `Skipped` | the cursor is not in the log |
| `ErrBadRequest` | the log is not wired |
| `ErrInternal` | a query failure |

- **Retention.** The writer deletes events older than the retention once an hour.

### Subscriptions (D116, v1.162.0)

A `Subscription` is an owner's standing request for events. It has:
- a filter;
- a delivery: the event stream, or a webhook endpoint;
- a cursor: the last envelope delivered.

It is a core record with a D110 address (prefix `N`), in `smeldr_subscriptions`. `CreateSubscriptionTables(db)` creates the table and its address storage. `App.Subscriptions()` turns subscriptions on for the stream; it needs `Config.DB` and `App.EventStream`. example/server turns them on with `ENABLE_SUBSCRIPTIONS`.

**The owner rule.** A subscription belongs to one token, its `Owner` (a User.ID). The owner reads and changes it, and so does a holder of `administer`. To anyone else it is `ErrNotFound`, the same as a missing one. A subscription has no generic `create_content` or REST surface; it goes through these:

```go
s, err := app.CreateSubscription(ctx, smeldr.SubscriptionSpec{
    Name:   "core tasks",
    Filter: smeldr.SubscriptionFilter{Types: []string{"Task"}, Events: []string{"transitioned"}, Fields: map[string][]string{"band": {"core"}}},
})
app.GetSubscription(ctx, "N3")
app.ListSubscriptions(ctx, "")                         // the caller's own, defaults included
app.UpdateSubscription(ctx, "N3", smeldr.SubscriptionUpdate{Paused: &yes})
app.DeleteSubscription(ctx, "N3")
```

- `SubscriptionSpec.Owner` set to another token needs `administer`, as does a `webhook:<endpoint id>` delivery.
- An owner holds at most `MaxSubscriptionsPerOwner` (100) subscriptions.
- An item list holds at most `MaxSubscriptionItems` (1,000) entries.

**The filter** (`SubscriptionFilter`) is a conjunction: every dimension narrows, and an empty one means any.

| Dimension | Matches |
|---|---|
| `Types` | the event's item type (a relation event matches when either end's type is listed) |
| `Events` | created, updated, transitioned, published, unpublished, scheduled, archived, deleted, relation.asserted, relation.ended |
| `EventNames` | full event names, such as `task.transitioned` (a webhook endpoint's subscription uses these) |
| `FromStates`, `ToStates` | a transition's states (`ToStates` also the state a created item starts in) |
| `Items` | `"Type:ident"` pairs, saved as `"Type:ID"` |
| `RelatedTo` | the items one live, asserted hop from an anchor (`{Anchor: "Type:ident", Kind, Direction}`), by the same query a dynamic grant uses |
| `Fields` | routing values: `band` (Task, Goal), `scope` (Decision), `receiver` and `sender` (Signal), `priority` (Task, as `"0"`), a runtime-defined type's channel field |
| `SignalKinds` | `condition` or `request` (a history Signal type matches neither) |
| `AddressedToMe` | a Signal whose receiver is one of the owner's routing names |
| `SentByMe` | a Signal whose sender is one of the owner's routing names |

- **Routing names.** These are the owner's User.ID, every operation it holds, and the `receiver` values its subscriptions name; a role subscription names its band. Receiver and sender are routing names, not identities: tokens carry no band.
- **Items and anchors** are resolved under the owner's read scope. One the owner cannot read is `ErrNotFound`.
- **Matching runs at delivery**, on the event's logged metadata (`from_state`, `to_state`, `signal_type` and `fields` are columns of `smeldr_events`). A replay therefore filters exactly as live delivery does.

**Defaults.**
- **Universal.** Every token has three, created on first use (a list, or a stream connection). Creating them twice is impossible: a unique index on (owner, default_kind) refuses the second row.
  - `requests_to_me`: requests addressed to me;
  - `conditions_i_can_resolve`: conditions addressed to me, or to an operation I hold;
  - `outcomes_of_my_requests`: my requests answered or withdrawn.

  They cannot be deleted (`ErrConflict`) and can be paused; their filter is fixed.
- **Role.** `App.SeedRoleSubscriptions(ctx, owner, RoleDefaults{Band, Architect})` needs `administer` and is idempotent by name: a subscription the owner already has under the same name is left as it is, even with an older filter (change it with `App.UpdateSubscription` or MCP `update_subscription`). A subscription whose type the instance does not define (such as `task_plan`) is left out. `App.SeedRoleSubscriptionsReport` (and `SeedRoleSubscriptionsReportTrusted`) returns a `RoleSeedReport` with `Created` and `LeftOut` (`RoleSubscriptionSkip{Name, Type}`); a subscription the owner already has is in neither. It creates:
  - for a band: Tasks in the band (created, updated, transitioned; updated carries a priority set later), task_plans in the band moved to approved or changes_requested, and Signals to the band;
  - for architect: task_plans submitted, Tasks to plan-reviewing or commit-reviewing, Tasks to done, blocked or resolved, Decisions proposed or ratified, Tasks created at priority 0, and Signals to architect.

  The operator runs `smeldr-server seed-role-subscriptions --owner=<token user id> --band=core` (or `--architect`). It prints `{"created": [...], "left_out": [{"name": ..., "type": ...}]}` and one stderr line per left-out subscription; a rerun prints two empty lists. Since v1.163.0 every `smeldr-server` subcommand loads the runtime-defined types first, as the running server does; before, `task_plan` was missing and its subscriptions were left out.

**On the stream.**
- `?subscription=<address>` delivers that subscription. It must be the caller's, or the caller must hold `administer`; otherwise it is 404. It resumes from its cursor when no `?after=` is given. The cursor is saved every 10 s and when the connection ends.
- A connection with neither `?subscription=` nor `?channel=` delivers every active subscription of its token, the defaults included. Before v1.162.0 such a connection received everything.
- `?channel=` routes as before (D97), under the read ceiling.
- `?subscription=` with `?channel=` is 400, and so is `?subscription=` without subscriptions on.
- A change to the owner's subscriptions recompiles its open connections.
- `include_own` is per subscription.

**The read scope reloads.** Every connection and every webhook delivery reads its owner's scope from one cache in the App. A cached scope is dropped, and the owner's open connections load it again:
- when a grant of the owner's is made or revoked (`RoleStore.Grant`, `RevokeWithReason`, and delegation, which goes through them);
- when a relation begins or ends at an anchor of one of the owner's dynamic grants;
- after 60 seconds, which also covers a grant or edge that expired without an event.

A connection whose scope or subscriptions cannot be loaded again is closed (fail closed); the client reconnects.

**A revoked token's connections close.** A revocation through the App's `TokenStore` closes them at once. A connection also checks its token every 60 seconds with `TokenStore.IsRevoked(ctx, fingerprint)`, so a revocation made another way closes it then. A token with no store row is checked by its signature alone.

**`App.CanRead(ctx, user, typeName, id) (bool, error)`** answers whether user may read the item, by the same read rules as every read surface, through the same cache. Without governance an Author or higher reads everything. It returns `ErrInternal` when the scope cannot be loaded.

**Webhooks are deliveries of a subscription.** With `App.Webhooks` and `App.Subscriptions` both on:
- A new endpoint gets one subscription, delivery `webhook:<endpoint id>`, owned by `legacy-admin` (which reads everything), with `EventNames` set to the endpoint's events. Deleting the endpoint deletes it.
- An event is delivered to the endpoint only when that subscription is not paused, its filter matches, and its owner may read what the event reveals. A failure to read the subscription or the owner's scope skips the delivery and is logged (fail closed); the change itself never fails.
- An endpoint without a subscription is delivered every event it names, as before.
- `MigrateWebhookSubscriptions(ctx, db, WebhookMigrationOptions{Apply})` gives every endpoint without a subscription its own, in one transaction. It is idempotent and a dry run unless `Apply`. It returns a `WebhookMigrationReport` (`applied`, `duration_ms`, `created`: endpoint ID, target URL, events and the new subscription's address). Without `smeldr_webhook_endpoints` there is nothing to migrate. The operator runs `smeldr-server migrate-webhook-subscriptions [--apply] [--report=<file>]`.

**Capabilities:**
- `subscription get` and `subscription list` (operation `read`), and `create`, `update` and `delete` (`create` / `update` / `delete`). These are floors for one's own; another owner's needs `administer`, checked in the act.
- `seed_role_defaults` and `migrate_webhooks` (`administer`).
- `webhook_create`, `webhook_list`, `webhook_delete` and `webhook_retry` (`administer`). The `webhook` resource rows stay while mcp's webhook tools map to them.

### Example

Client connecting with curl, subscribed to the full, unfiltered stream:

```bash
curl -N -H "Authorization: Bearer <token>" https://example.com/_events/stream
```

Equivalent, explicit form:

```bash
curl -N -H "Authorization: Bearer <token>" "https://example.com/_events/stream?channel=all"
```

Subscribed to one channel only:

```bash
curl -N -H "Authorization: Bearer <token>" "https://example.com/_events/stream?channel=core"
```

Output (NDJSON):

```
{"id":"e1","event":"task.created","timestamp":"2026-08-17T12:00:00Z","data":{"type":"task","id":"t1","slug":"t001-example"}}
{"type":"ping"}
{"id":"e2","event":"task.transitioned","timestamp":"2026-08-17T12:00:05Z","data":{"type":"task","id":"t1","slug":"t001-example","from_state":"active","to_state":"waiting-plan","actor_id":"u1","actor_kind":"agent"}}
{"id":"e3","event":"signal.created","timestamp":"2026-08-17T12:00:10Z","data":{"type":"signal","id":"s1","slug":"plan-ready"}}
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

| LifecycleEvent | Recorded |
|----------------|----------|
| `AfterPublish` | ✅ |
| `AfterSchedule` | ✅ |
| `AfterArchive` | ✅ |
| `AfterDelete` | ✅ |
| `AfterCreate` | ❌ — drafts fire on every auto-save; excluded to prevent unbounded noise |
| `AfterUpdate` | ❌ — same reason |

---

## Provenance trail

`App.Provenance(store ProvenanceStore)` subscribes to `AfterCreate`,
`AfterUpdate`, `AfterPublish`, `AfterUnpublish`, `AfterSchedule`,
`AfterArchive`, and `AfterDelete` via the signal bus and writes one
`ProvenanceRecord` per event — additive and independent of `App.Audit`
(both may be wired at once; the four events they share each produce their
own record in their own store).

**One record per status change (v1.137.0; D107).** A status
change is recorded once, by the transition itself, on every path (verb
`transition`, from, to, actor, surface, reason); the subscriber records
creations, content edits (verb `update`) and deletions, and skips the status
events a transition dispatches. The MCP `publish_*`, `schedule_*` and
`archive_*` tools now store their `reason` on that record. Records written
before this version may repeat one transition two or three times (a
status-changing `PUT` wrote one per event it fired): same subject, verb
`transition`, from, to, actor and surface, within the same second. Nothing is
rewritten; a reader that counts transitions collapses those.

### Setup

```go
// 1. Create the table once at startup. Also creates the
//    (subject_type, subject_id) index SubjectProvenance reads by; idempotent,
//    so calling it on boot against an existing table adds the index.
smeldr.CreateProvenanceTable(db)

// 2. Wire the provenance store
app.Provenance(smeldr.NewProvenanceStore(db))
```

### `ProvenanceRecord` fields

| Field | JSON | Description |
|-------|------|-------------|
| `ID` | `id` | UUID v7 primary key |
| `Timestamp` | `timestamp` | UTC time the event was recorded |
| `SubjectType` | `subject_type` | Go type name, e.g. `"Decision"`, `"RelationEdge"` |
| `SubjectID` | `subject_id` | `Node.ID` or `RelationEdge.ID` |
| `Verb` | `verb` | `"create"` \| `"update"` \| `"transition"` \| `"assert"` \| `"invalidate"` |
| `FromState` | `from_state` | Empty for create/assert |
| `ToState` | `to_state` | Empty for invalidate |
| `ActorKind` | `actor_kind` | `"job"`, `"agent"`, `"human"` (the attested tags, D105) or `"unclassified"` (no tag); empty only if truly unattributable. Rows written before v1.121.0 say `"human"` for an untagged actor: that means unclassified |
| `ActorID` | `actor_id` | User UUID, job identifier, or agent identifier |
| `Surface` | `surface` | `"http"` \| `"mcp"` \| `"cli"` \| `"trigger"`; `"cli"` has no current producer — nothing distinguishes a `smeldr-cli` HTTP request from any other |
| `Reason` | `reason` | Free text, empty unless the caller supplied one — most acts carry none |

### Admin objects also write provenance

Minting a token writes one too (`Token`/`assert`, v1.135.0; see "Reasons on minting, revoking and granting"). As of Amendment A281 (Task T203), `RoleStore.Grant`, `RoleStore.Revoke`, and `TokenStore.Revoke` each write a `ProvenanceRecord` in addition to their existing `GovernanceAuditStore` write. Two new `SubjectType` values exist as a result: `"RoleGrant"` (for grant/revoke actions) and `"Token"` (for revoked tokens): the first non-`Node`, non-`RelationEdge` subjects the provenance system has carried.

The verb values are `"assert"` when a role is granted and `"invalidate"` when a grant or token is revoked — reusing the same pair `RelationEdge` already employs for its own create/remove shape.

These entries are always fully attributed (carrying `ActorKind`, `ActorID`, `Surface`, and `Reason`) when read back through `SubjectProvenance`, because neither `RoleGrant` nor `Token` has a registered `StateFlow` — this mirrors `GovernanceAuditStore`'s already-transparent posture for admin actions.

Provenance writes only activate when both stores are configured on the same `App` instance — otherwise all `recordProvenance` call sites are silent no-ops.

### State changes through `TransitionItem` also write provenance (A392, v1.109.0+)

With `App.Provenance` wired, every successful state change made through
`App.TransitionItemVia` (and `App.TransitionItem`/`App.TransitionItemWithReason`, which delegate
to it), `DynamicTypeRepo.SetStatus`/`SetStatusWithReason`, the
`POST /_content/{type}/{id}/status` endpoint, and `DynamicTypeRepo.ScheduleContent` writes one
`ProvenanceRecord`: verb `"transition"`, from and to state, actor and actor kind (taken from the
`smeldr.Context` in `ctx`, empty for a plain context), surface, and the reason when one was
supplied. `SubjectType` is the registered type name (`"Task"`, `"Decision"`, a dynamic type's own
name). Before this these paths recorded only `last_actor`.

- Written after the status UPDATE succeeded, never before: a rejected or failed transition records
  nothing.
- Synchronous and fail-open: a failed write is logged and never fails the transition.
- A runtime-defined content type is recorded once, by `DynamicTypeRepo`, not a second time by
  `TransitionItemVia`.
- Module HTTP, MCP lifecycle and scheduler transitions are unchanged: they already recorded through
  the signal bus.
- **No backfill.** Transitions made before the instance upgraded have no record and none can be
  reconstructed.
- `POST /_content/{type}/{id}/status` records surface `"http"` and the authenticated caller as
  actor (v1.109.1; v1.109.0 recorded no actor there because the handler passed the plain request
  context).
- Since v1.110.0 two more state changes outside the transition code are recorded, by the same
  internal writer:
  - **The `ConflictSupersede` side effect.** An item that a winning transition moves to
    `superseded` gets `last_actor` stamped with the triggering actor and one record: verb
    `"transition"`, the active state to `superseded`, the triggering caller as actor (empty for a
    plain context), the winning transition's surface, and the reason `superseded by <Type> <id>`
    naming the winner. **No event of any kind fires for the superseded item** (no webhook, stream
    event, `After*` signal or async trigger), so a live surface that relies on the event stream does
    not see it change until it re-reads. No built-in flow uses `ConflictSupersede`, so this affects
    customer-defined types only.
  - **Core's Signal resolver** (D115). Each condition it resolves gets one record: actor kind
    `"job"`, actor `"signal-resolver"`, surface `"trigger"`, reason the resolution (for example
    `Task 01a... moved backlog to active`). Nothing is written for a row that had already left
    `open`. (The retired expiry sweep wrote `"signal-expiry-sweep"` records until v1.158.0.)
- `ENABLE_PROVENANCE` (the example server's switch for `App.Provenance`) must be set, otherwise
  nothing is recorded at all.

What a reader sees is unchanged and decided at read time: `SubjectProvenance` shows the actor only
for a transition that required an operation under `Strict` enforcement, so a Task state change
reads as verb, states and date, and a Decision ratification also shows who did it.

### Reading an item's history — `App.ItemProvenance` and `get_item_provenance` (A428, D101, v1.123.0)

One item's history, newest first, with the actor shown by audience (D101). The capability, in the terms of D94: resource **item provenance**, verb **read**.

```go
page, err := app.ItemProvenance(ctx, "Decision", decisionID, smeldr.ProvenanceMembers, 50, 0)
// page.Entries newest first, page.Total the item's whole history
```

- **Audience.** `ProvenanceMembers`: every entry carries `ActorKind`, `ActorID`, `Surface` and `Reason` (the whole record); `Gated` still says whether the act had to pass an authority check. `ProvenanceGated`: the rule of `SubjectProvenance` below, the actor only on a transition that required `RequiredOperation` under `Strict`. An unknown audience is an error, never the wider view.
- **Who is a member.** A core instance is one organisation. Every credential that reaches the MCP tool was minted on the instance and the tool requires Editor and the `read` operation, so every caller that passes the tool's own authorization is a member, and the tool defaults to `members`. A caller can only **narrow** its view: the optional `view` argument takes `members` (default) or `gated`, so a surface for a wider audience asks for `gated` and the actor never leaves core. The gated view is also what an in-process consumer serving a public audience passes.
- **Paging.** `limit` 0 means 50 and is capped at 500, `offset` must not be negative, an offset past the end is an empty page with the true `Total`. Entries of the same second (the record stores second resolution) are ordered by id, newest first, so a page never reshuffles.
- **Honest errors.** With no provenance store wired (`App.Provenance`, behind `ENABLE_PROVENANCE` in the example server) the read is an `ErrNotFound` saying provenance is not enabled, never an empty page: an empty history and "not recorded here" are different statements. A real database error while deciding whether a transition was gated is `ErrInternal`: visibility is never decided by a swallowed error. A type with no flow, or a transition its flow does not declare, is not an error, it is not gated. (`SubjectProvenance` below keeps its fail-closed behaviour.)
- **What an entry is.** Time, verb (`create`, `update`, `transition`, and the standing events `standing-began` and `standing-ended`, from which held and ceased intervals are read), from and to state, and by audience the actor. `actor_kind` is `job`, `agent`, `human` or `unclassified` (D105). **Rows written before core v1.121.0 say `human` for an actor with no classification and mean unclassified, never a verified person.** History exists only for changes made after provenance was switched on.
- **Relation events are not in this read.** An edge asserted or ended is recorded against the `RelationEdge`, not the item, so an item's history here is not complete for relations; they join with `core-relation-history-one-row-per-life`.
- **MCP.** `get_item_provenance(type_name, slug, limit?, offset?, view?)`, Editor, policy row `read` (since smeldr.dev/mcp v1.49.0). **CLI.** `smeldr-cli history <type_name> <slug> [--limit n] [--offset n] [--view members|gated]` (smeldr.dev/cli v0.20.0).

### The gated-only read — `SubjectProvenance`

There is **no HTTP endpoint** for reading provenance, and the MCP tool above (`get_item_provenance`, since mcp v1.49.0) is the remote read; `SubjectProvenance` is the older, gated-only plain Go function, meant to be composed
directly by a caller holding its own `DB` handle (the same pattern
`smeldr.dev/cloud`'s own read layer already uses for other core data), not
reached over a network round trip:

```go
entries, err := smeldr.SubjectProvenance(ctx, db, store, "Decision", decisionID)
```

Each returned `ProvenanceEntry` carries the gating decision already applied:
a transition that required a `RequiredOperation` with `Strict: true` (see
[State flows](#state-flows)) exposes `ActorKind`/`ActorID`/
`Surface`/`Reason`; any other event carries only `Verb`/`FromState`/
`ToState`/`Timestamp` — actor identity is never returned for an act nobody
had to be authorized to perform. `SubjectProvenance` never accepts an actor
as a query parameter — only `subjectType`/`subjectID` — an item-scoped
query only, never an actor-scoped one.

| `ProvenanceEntry` field | Populated |
|---|---|
| `Timestamp`, `Verb`, `FromState`, `ToState`, `Gated` | Always |
| `ActorKind`, `ActorID`, `Surface`, `Reason` | Only when `Gated == true` |

### Since when is an item in its state — `App.ItemsStateSince` (A415, v1.120.0)

```go
since, err := app.ItemsStateSince(ctx, "Task", map[string]string{id: status})
// since[id] is a smeldr.StateSince{Since time.Time; Reason string}
```

For a page of items of one type (item id to its current state) it returns when each entered the state it is in now and the reason that transition carried: the latest provenance record of verb `transition`, kept only when its `ToState` equals the item's current state. An id is **absent** when that does not hold: provenance is not wired (`App.Provenance`), the item moved before provenance was switched on, or its status changed by a path that wrote no record. Absence means unknown, never "has not moved".

- `Since` has **second resolution** (the record stores it that way); two transitions in the same second are ordered by record id.
- The **actor is deliberately not returned**. This is not a `SubjectProvenance` read and does not apply its gating: the `RequiredReason` transitions exist so the reason is written down, and no Task transition is `RequiredOperation` with `Strict`, so that gating would hide every Task reason. Who may see an actor is a separate rule (D101) delivered by its own read.
- One query per 400 ids on the default store. A custom `ProvenanceStore` is read through `List`, one id at a time (the interface is unchanged), with one Warn per process. A failed read is `ErrInternal`.
- The mcp tools `get_task`/`list_tasks`/`get_goal`/`list_goals` and `list_items_by_state` (Task, Goal) carry it as `state_since` (RFC3339 UTC) and `state_reason` (omitted when the transition carried none), since smeldr.dev/mcp v1.47.0. Works on SQLite and Postgres.

### Custom store

Implement `ProvenanceStore` to use a different backend:

```go
type ProvenanceStore interface {
    Append(ctx context.Context, r ProvenanceRecord) error
    List(ctx context.Context, f ProvenanceFilter) ([]ProvenanceRecord, error)
}
```

---

## Scheduled sweep run records

`App.SweepStructural(ctx) (walked, flagged, skipped int, err error)`, `App.DrainEvalQueue(ctx) (walked, triggered, skipped int, err error)` run detectors at scheduled intervals to maintain data consistency — structural validation, eval-queue draining, relation invalidation. (`App.ExpireSignals` is retired since v1.159.0, D115: it moves nothing.) Previously, a successful clean run logged one Debug line and persisted nothing, making it indistinguishable from "the sweep never ran". `SweepRunStore` records every scheduled sweep, so staleness can be derived by Go code (e.g. a future alerting task). `SweepRunStore` itself has no HTTP or MCP surface in core (A279) — `Last` and `List` are plain Go methods. `smeldr.dev/mcp` exposes a remote-callable read on top of them: the `get_sweep_run` tool (Author role) wraps `SweepRunStore.Last`, added for exactly the "public read surfaces deferred via future task if needed" case A279 anticipated.

`SweepStructural` also stamps a **per-edge** confirmation, distinct from `SweepRunStore`'s per-run aggregate: `RelationEdge.LastConfirmedAt` (A299) is set to the sweep's own `now` for every edge whose source and target were both checked and found alive that run — nil until an edge's first successful sweep, and never touched by `Assert`/`Propose`/`Observe`. It answers "was this specific edge part of the most recent successful walk," which `SweepRunRecord.Walked`'s aggregate count cannot — the field a witness certificate needs for "confirmed by the system, on a schedule," as opposed to any deliberate `ProvenanceRecord` action. Exposed via `smeldr.dev/mcp`'s `get_relations` (`last_confirmed_at`, omitted when nil).

### Setup

```go
// 1. Create the table once at startup
smeldr.CreateSweepRunTable(db)

// 2. Wire the sweep run store (optional)
runStore := smeldr.NewSweepRunStore(db)

// 3. Wrap a detector to record runs
sweepFn := func(ctx context.Context) (int, int, int, error) {
    walked, flagged, skipped, err := app.SweepStructural(ctx)
    errStr := ""
    if err != nil {
        errStr = err.Error()
    }
    _ = runStore.Append(ctx, smeldr.SweepRunRecord{
        ID: smeldr.NewID(), Detector: "structural", RanAt: time.Now().UTC(),
        Interval: "0 * * * *", Walked: walked, Flagged: flagged, Skipped: skipped, Err: errStr,
        ActorKind: "job", ActorID: "sweep-structural",
    })
    return walked, flagged, skipped, err
}
```

DDL (also created by `CreateSweepRunTable`; a pre-existing table missing
`actor_kind`/`actor_id` is upgraded in place via `EnsureColumn`):

```sql
CREATE TABLE IF NOT EXISTS smeldr_sweep_runs (
    id         TEXT PRIMARY KEY,
    detector   TEXT NOT NULL,
    ran_at     TIMESTAMPTZ NOT NULL,
    interval   TEXT NOT NULL,
    walked     INTEGER NOT NULL,
    flagged    INTEGER NOT NULL,
    skipped    INTEGER NOT NULL,
    err        TEXT NOT NULL,
    actor_kind TEXT NOT NULL DEFAULT '',
    actor_id   TEXT NOT NULL DEFAULT ''
);
```

### SweepRunRecord fields

| Field | JSON | Description |
|-------|------|-------------|
| `ID` | `id` | Unique record ID (UUIDv7) |
| `Detector` | `detector` | Stable name, e.g. `"structural"`, `"eval-queue"` |
| `RanAt` | `ran_at` | UTC time the sweep ran |
| `Interval` | `interval` | The detector's own declared cron schedule, e.g. `"0 * * * *"` |
| `Walked` | `walked` | Total items examined this run |
| `Flagged` | `flagged` | Items with issues (e.g. relations invalidated, items transitioned) |
| `Skipped` | `skipped` | Items the detector could not fully check this run (e.g. a target- or source-checker error in `SweepStructural` — D61/T233 extended the sweep to check source existence as well as target, not target-only — or a role-gated/erroring item in `DrainEvalQueue`) — logged, not fatal to the run |
| `Err` | `err` | Non-empty when the run itself returned an error; empty string for success |
| `ActorKind` | `actor_kind` | `"job"`, `"agent"`, `"human"` or `"unclassified"`; empty only if truly unattributable, matching `ProvenanceRecord`'s own vocabulary (A289) |
| `ActorID` | `actor_id` | Fixed mechanism identifier for a scheduled detector, e.g. `"sweep-structural"`, `"drain-eval-queue"` |

### Custom store

Implement `SweepRunStore` to use a different backend:

```go
type SweepRunStore interface {
    Append(ctx context.Context, r SweepRunRecord) error
    Last(ctx context.Context, detector string) (r SweepRunRecord, found bool, err error)
    List(ctx context.Context, detector string, limit int) ([]SweepRunRecord, error)
}
```

### Signal kinds and states (D115, v1.159.0)

A Signal is one of two kinds, and its kind decides how it ends. Nothing expires.

- A **condition** is a state of the world that core emits and resolves itself. It is `open` while the condition holds and `resolved` when it ends, with `resolution` saying what ended it. No actor answers or withdraws one, and only core creates one: `InsertSignal` refuses a condition type from any other caller with `ErrForbidden`.
- A **request** is an ask from one actor to another. It is `open` until it is `answered` (with an answer, and optionally the change it names) or `withdrawn` (with a reason). Core never resolves one.

**The catalog.** `smeldr.SignalTypes() []SignalTypeInfo` returns it in a fixed order, and `smeldr.SignalTypeOf(t) (SignalTypeInfo, bool)` looks up one type. `SignalTypeInfo` is `{Type, Kind, Resolution}`, with `Kind` either `smeldr.SignalCondition` or `smeldr.SignalRequest`.

| Type | Kind | Ends when |
|---|---|---|
| `ratification-required` | condition | the Decision leaves `proposed` |
| `authorization-required` | condition | the subject leaves `from_state` (the blocked transition happened or was abandoned) |
| `conflict-detected` | condition | the `contradicts` edge ends or a side leaves `ratified` (one row per side, placed with it, T928); for a taken URL, the item is published or its slug changes |
| `coverage-matches-nothing` | condition | the coverage rule is changed or archived, or matches again at the next `CheckCoverage` (T886) |
| `premise-flagged` | condition | the premise is in force again, the `grounded_in` edge ends, the resting Decision leaves force, or a ratify holder confirms it (`ConfirmPremise`, D149) |
| `downstream-blocked`, `reference-stale` | condition | registered with their rule; no emitter yet, so none can be created |
| `review-requested` | request | answered or withdrawn |
| `question` | request | answered or withdrawn |

**History types.** Every other `signal_type` (plan-ready, commit-ready, task-closed, notice, implementing, implementation-question and the rest) is history. A history Signal is still accepted, with a `deprecated_signal_type` warning, until `Config.SignalEventCutover`, and refused with a validation error after it. `implementation-question`'s warning names `question` as its replacement. A history Signal starts in `pending` and keeps `pending → read → acknowledged`. Since D115 neither `pending` nor `read` awaits action.

**The flow** (`signal-protocol`, one flow for both kinds):

| From | To | Rule |
|---|---|---|
| `open` (initial, awaits action) | `resolved` | conditions only, by core only: any other caller is `ErrForbidden` |
| `open` | `answered` | requests only, `RequiredReason` (the answer) |
| `open` | `withdrawn` | requests only, `RequiredReason` |
| `pending` | `read` | history |
| `read` | `acknowledged` | history |

`resolved`, `answered`, `withdrawn`, `acknowledged` and `expired` are terminal. A condition answered or withdrawn, or a request resolved, is `ErrConflict` on every transition path (`transition_item`, the module paths, `App.TransitionItemVia`).

**Answering.**

```go
err := app.AnswerSignal(ctx, "S2851", "yes, approved in the plan body", "Task:01a1...")
err = app.WithdrawSignal(ctx, "S2852", "asked in the wrong place")
```

`App.AnswerSignal(ctx, ident, answer, ref string) error` and `App.WithdrawSignal(ctx, ident, reason string) error` take an ID, slug or address. The answer, or the withdrawal reason, is stored in `answer` and is the transition's reason in provenance: answering is one act. `ref` is optional and has the form `Type:ID`.

| Returns | When |
|---|---|
| nil | an open request, answered or withdrawn with a non-empty text |
| `ValidationError` (`answer`, `reason` or `answer_ref`) | the text is empty, or the ref is not `Type:ID` |
| `ErrNotFound` | the Signal is missing, or hidden by the caller's read scope (the same statements run for both) |
| `ErrConflict` | the Signal is not open, or it is a condition |
| `ErrForbidden` | the caller lacks `signal answer` (operation `update`) or `signal withdraw` (operation `archive`); under governance, a caller with no `Context` |

A Signal's `receiver` and `sender` are **routing names, not identities**. They are band or role names, such as `core`, `architect` or `approve`, not token IDs, so core cannot check that the caller is the receiver. Any holder of `signal answer` who can read a request may answer it, until tokens carry a band. A `transition_item` call to `answered` also stores its reason as the answer.

**What core emits and resolves.**
- `authorization-required` is opened by `App.DrainEvalQueue` when a scheduled transition is operation-gated. There is one open condition per blocked transition.
- `ratification-required` is opened when a Decision is created in `proposed` or enters it. There is one open condition per Decision, with receiver `approve`.
- `conflict-detected` is opened in two cases:
  - on a `contradicts` edge, as two rows, one about each side (T928). Each row's message names only its own Decision (`D12 contradicts a Decision`), never the other, and each is placed where its Decision is placed, in the same transaction as the insert; it follows the Decision when it moves (as `premise-flagged` does). The pair is kept in `smeldr_contradiction_flags` (created by `CreateGovernanceTables`, or `EnsureContradictionFlagTable` on an older database), and the resolver finds the rows there, not by their message. The resolutions name only the row's own Decision or none: `D12 is no longer ratified (superseded)` on the row about the Decision that moved, `the contradicting Decision is no longer ratified` on the other, `the contradicts relation ended (withdrawn)` when the edge ends. Asserting the edge again while the condition is open adds no rows. `App.SweepContradictionConditions` (`ContradictionSweep{Reworded, Resolved, Unparsed, Decisions}`) rewrites rows written before T928 (`Decision <id> contradicts Decision <id>`, in any state): the message and the resolution name only the row's own Decision, the pair row is filled in, an open row whose contradiction no longer holds is resolved, and every open contradiction or premise condition is placed with its Decision. Each rewrite records an `update` in provenance. It is idempotent; the example server runs it at boot and on the structural sweep's schedule (detector `contradictions`). Measured on Postgres: 1,000 old rows in 21 s the first time, 3 s on later runs. Provenance records written before T928 may still hold an internal Decision ID in their reason; the sweep does not rewrite history;
  - when the scheduler cannot publish a scheduled item because its URL is taken. There is one condition per item, with the item as subject, receiver `manage`, and `from_state` `scheduled`. On an instance without Signals, the scheduler logs this once instead.
- **Resolution:** every committed status change runs one indexed lookup of the open Signals about its subject (`idx_smeldr_signals_open_subject`, on `(subject_type, subject_id) WHERE status = 'open'`) and resolves what the change ended. An edge that ends (withdrawn, invalidated or superseded) resolves its contradiction. A slug change resolves the taken-URL condition.
  - Each resolution stamps `last_actor` `signal-resolver` and writes a provenance record with actor kind `job` and surface `trigger`. It also sends `signal.transitioned` to webhooks.
  - The resolver fails open: an error is logged and never fails the change.

**Message limit (D114).** A message longer than `SignalMessageMaxLength` (500 characters) gets a `message_too_long` warning until `Config.SignalMessageCutover`, and is refused after it. Put the detail in the item the Signal names.

**Checking without writing.** `smeldr.CheckSignal(ctx, s, cutovers) ([]CreateWarning, error)` applies all of the above:

| Returns | When |
|---|---|
| warnings | a history type before `cutovers.Event`, or a message longer than 500 before `cutovers.Message` |
| `ValidationError` (`signal_type`) | an empty type, or a history type after the cutover |
| `ValidationError` (`message`) | a message longer than 500 after the cutover |
| `ErrForbidden` | a condition type created by anyone but core |

`App.SignalCutovers()` returns the instance's two dates as a `SignalCutovers{Message, Event}`.

**Surfacing old Signals.** `App.OpenSignalsOlderThan(ctx, age time.Duration, limit int) ([]Signal, error)` returns the open Signals created at least `age` ago, oldest first, under the caller's read scope:
- a Signal the caller may not read, or whose subject it may not read, is left out;
- `age` of 0 or less means `DefaultOpenSignalAge` (14 days);
- `limit` of 0 or less means 50, and more than 500 is 500.

**Columns.** `resolution`, `answer` and `answer_ref` are added by `EnsureOrchestrationSignalColumns`, which also creates the open-subject index.

**Migration.** `smeldr.MigrateSignalKinds(ctx, db, SignalMigrationOptions{Apply bool}) (*SignalMigrationReport, error)` (also `App.MigrateSignalKinds`) runs in one transaction and is idempotent. Without `Apply` it is a dry run that rolls back.
- **Conditions:** `authorization-required` and `conflict-detected` in `pending`, `read` or `expired` become `open`. An expired condition is noted.
- **Requests:** `review-requested` and `question`:
  - in `pending` or `read` become `open`;
  - in `acknowledged` become `answered`, with answer "migrated: acknowledged before answers were recorded";
  - in `expired` become `withdrawn`, with "migrated: expired before answers were recorded".
- **`implementation-question`** becomes `question` by the same rules.
- **History types** stay as they are and are counted in `history`, by type and status.
- **Proposed Decisions:** each Decision in `proposed` without an open `ratification-required` gets one, listed in `opened`.

Each change is reported as `{id, address, type, old, new, subject, note}`, with `old` and `new` written as `type/status`.

**`smeldr-server migrate-signal-kinds [--apply] [--report=path]`** (example/server) is a dry run unless `--apply` is given. On process.smeldr.dev, devops applies it in deploy 2, on Peter's yes.

### Signal expiry sweep (retired, D115)

`App.ExpireSignals` stays exported (D97) but moves nothing: it logs once per process that the sweep is retired and returns zeros. `SignalExpiryConfig`, `DefaultSignalExpiryMaxAge`, `DefaultSignalExpiryBatchCap` and `DefaultSignalExpiryExcludedTypes` are kept for the API and read by nothing. The `signal.expiry_swept` stream event is gone. `example/server` no longer schedules the sweep: `ENABLE_SIGNAL_EXPIRY_SWEEP` only logs at boot that it does nothing, and `SIGNAL_EXPIRY_SWEEP_SCHEDULE`, `SIGNAL_EXPIRY_MAX_AGE_DAYS` and `SIGNAL_EXPIRY_BATCH_CAP` are not read. Use `App.OpenSignalsOlderThan` to find old open Signals.

---

## Findings (D51)

`Finding` is a thin, detector-owned record of a structural or governance
condition — a different granularity from `SweepRunStore` (one row per
sweep *run*, with aggregate counts): `Finding` is one row per individual
*detected issue*, deduplicated across repeated sweeps.

```go
smeldr.CreateFindingTable(db) // once at startup

app := smeldr.New(smeldr.MustConfig(cfg))
app.Findings(smeldr.NewFindingStore(db))
// App.SweepStructural now records a Finding for each newly-flagged
// stale relation edge, in addition to its existing AfterRelationCascade
// signal. No FindingStore configured means no behaviour change.

findings, err := app.FindingStore().List(ctx, "structural") // detector filter; "" lists all
```

**No `create_*`/`update_*` MCP tools and no human-driven state flow, by
design (D51/D46)** — a write surface would invite exactly the
`acknowledged`/`dismissed`/`accepted-as-is` human-resolution claims D46
forbids. A finding resolves when its own detector stops firing for the
same subject, not by human action — the same "authoritative state lives
outside `Node.Status`" shape `Run` (D38) already established. The read
surface is `App.ListFindings` (below), served by `smeldr.dev/mcp`'s
`list_findings` tool and `smeldr-cli findings` (T953).

Identity is `(Detector, SubjectType, SubjectID)` — `Record` upserts:
a repeat finding for the same subject updates `LastSeenAt`/`Message`
only; a new subject inserts with `FirstSeenAt == LastSeenAt`.

Four detectors record when a store is wired: `structural`
(`SweepStructural`, subject a `RelationEdge`, provenance `detected`),
`eval-queue` (`DrainEvalQueue`, the item that transitioned, `scheduled`),
`standing-drift` (`CheckStandingDrift`, `detected`) and declared-tension
aggregation on a Decision's ratification. The example server wires the
store when governance records or the structural sweep are on (T948).

```go
type FindingStore interface {
    Record(ctx context.Context, f Finding) error
    List(ctx context.Context, detector string) ([]Finding, error)
}
```

**`App.ListFindings(ctx, FindingQuery{Detector, SubjectType, Limit})`
(T953)** returns `[]FindingView{Detector, SubjectType, SubjectID, Address,
Provenance, Message, FirstSeenAt, LastSeenAt}`, newest first by
`last_seen_at`; `Limit` 0 means 100, at most 500 (else `ErrBadRequest`).
The finding's own ID is not shown; `Address` is the subject's address when
it has one. **A finding is shown only when the caller may read its
subject:** an item by the read rules; a `RelationEdge` only when both its
ends are readable; a finding whose subject no longer exists only to a
scope that reads everything. So a reader never sees a finding, or its
message, about an item it cannot read. Filtered rows do not count toward
the limit. Without a store wired it is `ErrNotFound` ("findings are not
recorded on this instance"), not an empty list; a failed read is
`ErrInternal`. Capability `ops list` (`read`), in the reads-any set: a
caller who reads at least one item may call it, and **every tool on `ops
list` must filter its result per subject the same way** (a test in
smeldr.dev/mcp pins `list_findings` as the only one). MCP `list_findings`
{detector?, subject_type?, limit?}, CLI `smeldr-cli findings`.

---

## Context packets

`App.ContextPacketHandler(rs *RelationStore, sourceName string)` mounts
`GET /packet/{type}/{slug}` — a bounded, relation-graph-traversed snapshot
of one of the five orchestration anchor types (`goal`, `decision`,
`amendment`, `task`, `signal`) plus its directly linked items, built by
`BuildContextPacket`. Requires **Editor role** via bearer auth — same
contract as `GET /_audit`/`GET /_logs`/`GET /_events/stream`.

`{slug}` accepts either the item's own real slug, or — for `decision`,
`task`, `goal`, and `amendment` — its human-facing identifier (a
Decision's own `DecisionNumber`, a Task's own `TaskID`, etc.), the same
fallback `Module.resolveItem` already applies on the write side (A300).
`signal` has no such fallback: it carries no canonical identifier beyond
its own slug.

### Setup

```go
app.ContextPacketHandler(relationStore, "my-instance-name")
```

`example/server` gates this behind its own `ENABLE_CONTEXT_PACKET` flag, in
addition to (not instead of) `ENABLE_RELATIONS`/`ENABLE_ORCHESTRATION` — all
three must be set for the route to mount. `ENABLE_CONTEXT_PACKET` is not
implied by the other two: an operator who only wants the relation/
orchestration MCP tools does not silently also get this HTTP route.

### Request

`GET /packet/{type}/{slug}?depth={1|2}` — `type` is one of
`goal`/`decision`/`amendment`/`task`/`signal`; `depth` defaults to `1`
(direct links only) and accepts `2` (one further hop). Any other value
returns `400`.

### Status codes

| Status | Meaning |
|---|---|
| `200` | Packet returned |
| `400` | Unknown `type`, or `depth` outside `1`–`2` |
| `401` | No valid bearer token |
| `403` | Valid token, role below `Editor` |
| `404` | No item of `type` with that `slug` |

There is no status-based visibility gate beyond the role check above — a
`Task` anchor in any state (`backlog`, `active`, `done`, ...) resolves
identically; `Status` never carries lifecycle vocabulary for these five
types (see [State flows](#state-flows)), only their own governed-flow state
names.

Both `PacketAnchor` and `PacketItem` carry `created_at`/`updated_at`,
read through from the underlying content's own `Node.CreatedAt`/
`Node.UpdatedAt` (A290). `PacketRelation` carries `created_at` (from
the edge's own `RelationEdge.CreatedAt`) plus `label`/`reverse_label`,
resolved from the edge's `RelationKind` via the registered
`RelationKindDef` — both empty strings when the kind isn't registered,
not an error (A291).

---

## Reachability

`App.ReachabilityHandler(rs *RelationStore)` mounts `GET /reachability/
{type}/{id}` — a bounded, breadth-first traversal of the relation graph
from any anchor, not limited to the five orchestration types `ContextPacket`
covers. A real remote-exposure path for `RelationStore.Reachability`
(A293): `ContextPacket`'s own depth cap (2) and per-type cap (25) would
silently undercount a real, densely-connected domain — this route has no
depth cap tighter than `MaxReachabilityDepth` (10). Requires **Author
role** via bearer auth — lower than `ContextPacket`'s `Editor` bar, since
Reachability exposes graph structure only (type/id/edge_class/confidence),
never item content.

**Live edges only (v1.117.0).** Reachability walks only relations that are live now: `invalid_at` is NULL or in the future, the same predicate `SweepStructural` and governance use. A relation that has ended is neither traversed nor counted, so Pulse's held-open count and Navigator's neighbourhood no longer include it (before v1.117.0 they did). The relation store has two pairs of getters: `GetBySource`/`GetByTarget` return **every** edge, ended ones included (the history view, each edge carries `InvalidAt`), and `GetLiveBySource`/`GetLiveByTarget` (new in v1.117.0, same signatures) return only live edges. Inside core the live pair is used by reachability, the relation cascade and `MCPPreviewImpact` (the preview must match the cascade), `get_goal_context` and the context packet; `MCPGetRelations` keeps the all-edges pair, so an operator can still see what ended. A caller outside core that shows what an item is related to now should use the live pair.

### Setup

```go
app.ReachabilityHandler(relationStore)
```

`example/server` gates this behind its own `ENABLE_REACHABILITY` flag, in
addition to `ENABLE_RELATIONS` — unlike `ContextPacketHandler`, it does
**not** require `ENABLE_ORCHESTRATION`, since Reachability operates on the
generic relation graph, not the five orchestration anchor types.

### Request

`GET /reachability/{type}/{id}?kind=&direction=&depth=` — `kind` is
optional (empty = all relation kinds); `direction` is `incoming`,
`outgoing`, or `both` (default `both`); `depth` defaults to `1` and must be
between `1` and `MaxReachabilityDepth` (10).

### Status codes

| Status | Meaning |
|---|---|
| `200` | Reachability result returned (JSON-encoded `Reachability`) |
| `400` | Invalid `direction`, `depth` out of range, or `depth` not a number |
| `401` | No valid bearer token |
| `403` | Valid token, role below `Author` |

There is no existence check on `{type}/{id}` — an anchor with no edges
simply returns a result whose rings all have zero items, matching
`Reachability`'s own "absence is a genuine result, not an error" design
(see [reachability.go]'s own doc comment).

`RelationStore.MCPReachability` is a thin passthrough to `Reachability`.

**The HTTP route is unbounded, and what that costs.** `GET /reachability/{type}/{id}` and `Reachability` return the whole walk. The walk expands each visited node once, one indexed query per node per direction, so its cost and its response size are the size of the walked component (up to depth 10), not depth times fan-out. On a densely connected graph that is large in both queries and bytes. Use it where the whole walk is needed (Tension, in-process); use the bounded variant below for anything sent to a caller.

### The bounded walk — `ReachabilityBounded` and `get_reachability` (A430, v1.124.0)

```go
res, err := store.ReachabilityBounded(ctx, "post", id, "", "both", 3, 500)
if res.Cut != nil { /* the cap stopped the walk: res.Cut.Depth, res.Cut.Dropped */ }
```

- `maxItems` 0 means `DefaultReachabilityItems` (500), a larger value is capped at `MaxReachabilityItems` (2000), a negative one is `ErrBadRequest`. Below the cap the result is exactly `Reachability`'s.
- **A cut is a count, never silent.** When the cap lands inside ring D, that ring holds the first items of the ring (sorted by type and id, so the choice is the same on every call), `Reachability.Cut` is `{Depth: D, Dropped: n}` with n the exact number of nodes found in ring D that were not returned, and **no deeper ring is returned**. Deeper rings are absent, not empty: an empty ring means "nothing at that distance", and after a cut that is not known. **The ring at `Cut.Depth` is itself partial, even when it is empty** (the cap landed exactly at a ring boundary and the next ring had more): the rule that an empty ring is a genuine absence does not apply to it. A cap equal to the size of the walked component is not a cut.
- **Cost bound.** The previous ring is expanded in full before cutting and holds at most `maxItems` nodes, so a walk costs at most `maxItems` expansions, one query per direction each (at most 4000 queries, at most 2000 items). Batching a ring's lookups is not done; it waits for a measurement.
- **MCP.** `get_reachability(type_name, id, kind?, direction?, depth?, max_items?, limit?, offset?)`, Author, policy row `read` (smeldr.dev/mcp v1.50.0). The result is `{items:[{depth, type, id, edge_class, confidence?}], ring_sizes, total, count, cut?}`: the rings flattened in ring order, the count per returned ring, the number of items the walk returned, the page, and the cut. `direction` is `incoming`, `outgoing` or `both` (default both), the walk's own vocabulary and **not** `get_relations`' `source`/`target`. **CLI.** `smeldr-cli reachability <type_name> <id> [flags]` (smeldr.dev/cli v0.21.0). In the capability terms of D94: resource reachability, verb read; the difference in direction words is recorded in the inventory.

---

## MCP resource subscriptions

Available in `smeldr.dev/mcp` when `App.AddSignalListener` is wired (set up
automatically by `New(app)` in `smeldr.dev/mcp`).

### Subscribe to a resource

Send `resources/subscribe` with `{"uri": "smeldr://posts/my-slug"}` to receive
`notifications/resources/updated` events over the SSE connection when that
resource changes. Post it to the message URL the `GET /mcp` stream announced
(`/mcp/message?session_id=<id>`): the subscription belongs to that session.
Before smeldr.dev/mcp v1.61.2 (A504) a subscribe over HTTP registered
nothing while answering `{}`. Notifications are not yet filtered by the
subscriber's read rules (core Task `core-subscriptions-and-stream-read-rules`).
A status change notifies on every path, `transition_item`
included (v1.137.0; core's `AfterTransition`, D107): one
notification for a move between custom states, and one per status event as
well for a move into or out of `published`, `archived` or `scheduled`.
Runtime-defined (dynamic) types have no resource URI, so they are not
notified.

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

Drafts of web content may share a slug (core v1.154.0): a slug-only token opens a draft only while the slug names exactly one draft or scheduled item, and otherwise falls through to the normal visibility check.

### `App.GeneratePreviewTokenForID`

```go
func (a *App) GeneratePreviewTokenForID(prefix, slug, id string) string
```

The same token bound to one item as well, so it opens that draft or scheduled item even when other drafts share its slug. The URL is unchanged (core v1.154.0, A507):

```go
token := app.GeneratePreviewTokenForID("/posts", "my-draft", item.ID)
previewURL := app.BaseURL() + "/posts/my-draft?preview=" + token
```

### `EnsurePublishedSlugIndex`

```go
func EnsurePublishedSlugIndex(ctx context.Context, db DB, table string) error
```

Creates `idx_<table>_slug_published`, a unique index on the slug of published rows of a compiled module's own table, so drafts may share a slug and published items may not (core v1.154.0, A507). It is idempotent and works on SQLite and Postgres. It scans for published duplicates first and refuses with `ErrConflict` naming them. It does not drop a column-level `UNIQUE` on `slug`: declare the column `slug TEXT NOT NULL`, or rebuild the table (SQLite) or drop the constraint (Postgres) first.

```go
smeldr.EnsurePublishedSlugIndex(ctx, db, "posts")
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
- `slug` (string, required) — the item: its slug, ID or address, e.g. `"my-draft-post"`. When drafts share a slug, give the ID.

Returns the full preview URL as a string, with the item's own slug. Since smeldr.dev/mcp v1.62.0 (A511) the item is resolved under the caller's read scope (a missing or hidden item answers not found) and the token binds its ID (`App.GeneratePreviewTokenForID`), so it opens exactly that draft. A prefix with no compiled module keeps a slug-only token.

**Changing a draft's slug over MCP** (smeldr.dev/mcp v1.62.0): `update_<type>` of compiled web content and `update_content` accept `new_slug` while the item is not published; `slug`/`id` keep naming the item. `update_content` needs `fields`, `new_slug` or both. This is how to take the free slug a publish conflict names.

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
    TypeName  string          `db:"type_name" json:"type_name"`
    Fields    json.RawMessage `db:"fields"     json:"fields"`
    LastActor string          `db:"last_actor" json:"last_actor,omitempty"`
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

`LastActor` (D78, Amendment A347) is the actor ID of the item's most recent
writer: set by `CreateDraftVia`, `UpdateFieldsVia` (v1.130.0) and by
`SetStatus`/`SetStatusWithReason`/`ScheduleContent`, empty when no caller
identity was available.

`Save` writes the post-save `Rev`, `UpdatedAt`, and `CreatedAt` back onto `node`
itself once the write succeeds (v1.64.1+) — no re-read needed to see the current
`Rev` after a call like the one above, as long as `node` is a pointer (as it is here).

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
    Role     string   // "title" | "description" | "og_image" | "body" | "summary" | "channel"
    Relation string   // non-empty = future T06 edge-backed relation placeholder
}
```

`Role` is a semantic seam read by T72 (head/SEO) and the ContentList renderer
(summary cards). At most one field per schema may carry each role. `channel`
(held, version pending) must be a `string` field: it routes the type's stream
events on the field's value plus the topic `type:<type name>` (see "Routed
runtime-defined types" under the event stream). A type opts in by redefining
its schema with the role; `ValidateSchemaDef` refuses it on a non-string field
or on a second field.

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

Validates `fields` (a `map[string]any`) against `schema`. Returns a
`*ValidationError` when:
- An unknown field is present (not declared in the schema).
- A required field is absent.
- A field's value type does not match the schema (`"string"`, `"integer"`,
  `"boolean"`, `"number"`).

Returns nil when `schema` is nil (no validation possible — backwards-compatible).
Use on the create path where all required fields must be present. (A202)

### `ValidatePartialFields` — partial field validation for updates

```go
err := smeldr.ValidatePartialFields(schema, patch)
```

Validates a `patch` map against `schema`. Unknown fields and type mismatches are
rejected; absent required fields are **not** checked — the patch is partial and
required fields may already be stored. Returns nil when `schema` is nil.
Use on the update path (PATCH semantics). (A202)

`ValidateBlockFields` is retained as an alias for the older `json.RawMessage`
variant.

### `ContentTypeRegistry` and `TypeDescriptor`

`ContentTypeRegistry` is a concurrency-safe name/prefix registry owned by `App`.
It is the linchpin shared by the ContentList block resolver (T96) and the future
relation endpoint system (T06).

```go
type TypeDescriptor struct {
    Name   string
    Prefix string             // URL prefix (e.g. "/posts")
    Schema *ContentTypeSchema // nil for compiled modules in this increment
    Kind   string             // "block" | "content" | "compiled"
    Fetch  func(ctx context.Context, opts ListOptions) ([]map[string]any, error)
}
```

`Fetch` is set automatically at `App.Content()` time for any `Module[T]` that
implements `ContentLister` (i.e. every compiled module). It returns Published items
as type-erased `map[string]any` for the ContentList block resolver.

`Kind` distinguishes three registration paths: `"content"` for runtime-defined
types created via `DefineContentType`, `"compiled"` for every `Module[T]`
registered through `App.Content()` (all six orchestration types plus any
custom compiled module), and `"block"` reserved for the block system's own
kind. `DynamicContentRepo` rejects any `Kind != "content"` — a compiled
module's data lives behind its own module, not `smeldr_dynamic_content`.

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

**Databases (v1.119.2).** Runtime-defined content types work on SQLite and on Postgres, including through `smeldr.dev/core/pgx`: reads, `UpdateFields`, `SetStatus`, `ScheduleContent`, `App.TransitionItem`, the `Locked` check and the conflict policy (the winner and loser writes share a transaction from pgx v0.3.0). Before v1.119.2 every read of a dynamic item failed on Postgres, because the driver returns a TEXT column as a string and `database/sql` cannot store a string in a `json.RawMessage`. `Query` and `SQLRepo` now scan a `json.RawMessage` field from a string or bytes, and a NULL gives a nil message (before, a NULL was a scan error on every database), so the same holds for any struct of yours with a `json.RawMessage` field. `SeedBlockTypeSchemas` uses `ON CONFLICT DO NOTHING`. Not covered by a Postgres test: the `/_content/{type}` admin routes, which call the same repository.

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

**Redefine a type (core v1.140.0)**

```go
desc, err := app.RedefineContentType(ctx, &smeldr.ContentTypeSchema{
    TypeName: "recipe",
    Fields:   fieldsJSON, // the full list, as for DefineContentType
}, "reason, optional")
```

`RedefineContentType` (and `RedefineContentTypeVia` with a surface) replaces a
registered runtime-defined type's schema, in the store and in the running App,
without a restart. A redefinition may loosen and add, never take away or tighten:
- **allowed:** the label (an empty one keeps the stored label), a field's role,
  format, description and relation, a required field becoming optional, new
  optional fields;
- **refused**, as a validation error on the field with nothing saved: removing a
  field, changing its type, making it required, adding a required field, and any
  change of `URLPrefix`. Setting, moving or removing a public prefix decides what
  is published, which a schema edit must not do.

It writes one provenance record (subject `ContentType` / the type name, verb
`update`, actor, surface, reason) and sends `content_type.redefined` on the `types`
topic and to webhooks. The change applies to writes after it: existing items are
not revalidated. It is in this process only: another process on the same database
sees the new schema after a restart. Redefinitions in one App run one at a time,
so two concurrent ones that each add a field cannot both pass: the second is
checked against the first's schema and refused for removing its field. The
public `GET {prefix}` and `GET {prefix}/{slug}` routes look the type up per
request, so they serve the new schema at once. MCP: `redefine_content_type` (gated by
`define-type`, like `define_content_type`); CLI: `smeldr-cli content-type redefine`.

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
| `POST` | `/_content/{type}/{id}/status` | Editor+ | Set status; enforces the transition's `RequiredOperation` as the authenticated caller (403 without it) and returns 400 when the transition requires a reason (v1.109.1) |

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
err = repo.ScheduleContent(ctx, id, time.Now().Add(48*time.Hour))
```

**Who wrote it (v1.130.0).** A create and a content update record their actor,
like a transition does. The caller comes from `ctx` when it is a
`smeldr.Context` (every MCP tool and the `/_content` REST routes pass one); a
plain `context.Context` is a system write with no actor.

- `last_actor` is set to the caller on create and on every update, so for a
  dynamic item it means "the most recent writer". Compiled types follow the
  same rule (v1.133.0; see "`last_actor` means the most recent
  writer" below).
- With provenance wired (`App.Provenance`), a create writes a `create` entry
  and an update an `update` entry (from and to the item's current status),
  each with `actor_kind`, `actor_id` and the surface. `get_item_provenance`
  and `App.ItemProvenance` show them. A refused write (validation, not found,
  a locked state) records nothing; a failed provenance write never fails the
  write it records.
- `CreateDraftVia(ctx, surface, fields)` and `UpdateFieldsVia(ctx, surface,
  id, patch)` name the entry point (`"http"`, `"mcp"`, `"cli"`) for the
  record; `CreateDraft` and `UpdateFields` are the same with no surface. The
  REST routes pass `"http"`; smeldr.dev/mcp passes `"mcp"` from v1.52.0.
- Nothing is backfilled: earlier creates and edits of dynamic items have no
  entry.

**Slugs (v1.134.0).** A new dynamic item's slug comes from its
schema's title field (`item` when there is none). The first free one of
`base`, `base-2` .. `base-100` is taken, read in one query. When all are taken
the slug is `base-` and the last 8 hex characters of the item's own id (its
random tail), or `base-` and the whole id if that is taken too. For web content
(a type with a URL prefix) only published items count as taken (core v1.154.0,
A507): a draft reserves no URL. One type never publishes two items under
one non-empty slug, and a record type never holds two items under one slug:
`CreateBlockTables` creates the unique index
`idx_dynamic_content_type_slug_published` on `(type_name, slug) WHERE slug <> ''
AND (status = 'published' OR address <> '')` (a record's item carries an
address, a web-content item does not; despite its name the index covers
every item of a record type), then drops the earlier all-status index
`idx_dynamic_content_type_slug`. The new index is created first, so the old one
is dropped only once the new one exists. Boot logs the outcome once:
`smeldr: published-only slug index in place`, or `smeldr: published-only slug
index skipped` with the count and the slugs when published duplicates already
exist (each is also logged with its type, slug and ids). No row is changed;
give all but one of each a new slug and the next boot creates the index. A
failure of the `CREATE INDEX` statement itself is logged the same way and boot
goes on; only a failure to run the duplicate scan is returned.

**Database migration**

When upgrading an existing database that pre-dates A154, call once at startup:

```go
smeldr.MigrateURLPrefixColumn(db) // idempotent; SQLite and Postgres
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

---

### Titles and field limits on runtime-defined types (A512, D112, D114)

A runtime-defined record type (no URL prefix) declares one string field with role `title`. Web content types (with a URL prefix) are not held to the title rule.

- `SchemaField.MaxLength` (`max_length`) caps a string field in characters; zero means no limit. It is refused on a non-string field, on a negative value, and on a `title`-role field above 120.
- A create with no title warns `title_missing`; a title over 120 characters, or over its own `max_length`, warns `title_too_long`.
- Any field a write makes longer than its `max_length` warns `field_too_long`, on any type. A create checks every field; an update checks only the fields it sets.
- A record type with no string field of role `title` warns `title_field_missing` (`TitleFieldWarnings`).
- `Config.TitleCutover` is the one cut date for all of these. Zero means warnings only; from the cut date on, each condition is refused with a validation error naming the field, and nothing is written.

| Write | Before the cutover | After the cutover |
|---|---|---|
| create a record with no title | written, `title_missing` | refused |
| a title over 120 characters (or its `max_length`) | written, `title_too_long` | refused |
| a field longer than its `max_length` (create, or an update that sets it) | written, `field_too_long` | refused |
| define a record type with no title field | defined, `title_field_missing` logged | refused by `App.DefineContentType` |
| redefine a record type with no title field | redefined | redefined (never refused for it) |

```go
func (r *DynamicTypeRepo) CreateDraftWithWarnings(ctx context.Context, surface string, fields map[string]any) (*DynamicNode, []CreateWarning, error)
func (r *DynamicTypeRepo) UpdateFieldsWithWarnings(ctx context.Context, surface, id string, patch map[string]any) ([]CreateWarning, error)
func TitleFieldWarnings(schema *ContentTypeSchema) []CreateWarning
```

Each warning names the field and the item's address. `CreateDraftVia` and `UpdateFieldsVia` apply the same refusal and log the warnings once per actor, type, code and day. Over MCP, smeldr.dev/mcp's `create_content` and `update_content` return the warnings (D110 shape), and `define_content_type`/`redefine_content_type` add `title_field_missing`. The admin routes `POST /_content/{type}` and `PATCH /_content/{type}/{id}` answer a validation error as 422.

#### Adding task_plan's summary

task_plan gets a `summary` (at most 300 characters, D114) with no core code: add it on an instance with `redefine_content_type`, passing the full existing field list with the new, optional field appended, so existing plans stay valid:

```json
{"type_name": "task_plan", "fields": [...the existing fields..., {"name": "summary", "type": "string", "role": "summary", "max_length": 300}], "reason": "D114: task_plan summary"}
```

On process.smeldr.dev, architect makes this call after the deploy that carries this release, on Peter's yes.

## State flows

Custom state machines for content types. Defined in `state.go`.

**Databases: enforced on SQLite and Postgres alike (v1.118.0, D103).** The state machine is enforced on every supported database. Every function below that consults the registered flow first asks one portable question, whether `smeldr_state_flows` is there (`SELECT 1 FROM smeldr_state_flows LIMIT 1`): when the table is absent there is no flow and nothing is enforced; when the question fails for any other reason the call returns `ErrInternal` (or, for the functions that cannot return an error, logs a Warn and fails open), never a silent skip. This holds for a cancelled context too.

| Function | What it enforces |
|---|---|
| `validateTransition` | the flow's edges, `RequiredOperation`, `RequiredReason`, `Strict`, the governance gate |
| `isStateLocked` | `State.Locked` (A306) |
| `validateInitialState`, `defaultInitialState`, `validateFlowItems` | the initial state, the state list |
| `suppressesSignals`, `fireAsyncTriggers` | `State.SuppressesSignals`, async transition triggers |
| `planConflict`, `resolveItemTable` | `ConflictPolicy`, the typed-versus-dynamic table choice |
| `MigrateStanding`, `migrateLegacyTableNames`, `EnsureLastActorColumns`, `MigrateRedirectsToRoutes`, `EnsureColumn` | boot migrations |

**Upgrading a Postgres deployment (breaking, no opt-out).** Before v1.118.0 the state machine was not enforced on Postgres through `smeldr.dev/core/pgx`: a flow was stored and never consulted. From v1.118.0 it is, so on upgrade a transition that passed unchecked can be refused (403, 409 or 400), content in a `Locked` state becomes read-only, async triggers fire and `ConflictPolicy` rejects or supersedes. This is a security fix (a transition's `RequiredOperation` gate was not checked on Postgres), shipped under D97 with no transition period. `process.smeldr.dev` and the Cloud product run on SQLite and are not affected.

**Tables and columns are looked up by selecting from them**, not by `sqlite_master`, `PRAGMA` or `information_schema`, so `EnsureColumn` and the boot migrations now work on Postgres: a Postgres database created by an older core gets the `strict`, `required_reason`, `locked`, `standing`, `active_state`, `conflict_policy` and `last_actor` columns at boot, and the legacy `forge_*` tables are renamed. A column that another process adds between the lookup and the `ALTER TABLE` is not an error. `isNoSuchTable`, `isNoSuchColumn` and the duplicate-column check recognise the Postgres errors (SQLSTATE 42P01, 42703, 42701) as well as SQLite's text. These probes must run on the base handle, never inside a transaction: on Postgres a failed statement aborts the whole transaction.

**`ConflictReject` and `ConflictSupersede` across processes (v1.119.0).** The per-type lock (v1.115.1) serialises transitions into the active state inside one process. On Postgres the reject check and the winner's write are separate statements under READ COMMITTED, so before v1.119.0 two application processes that moved two items into the active state at the same moment could both pass the check: measured against postgres:16 with two processes over 25 simultaneous rounds, every round ended with two holders. From v1.119.0 the policy also takes a lock shared by all processes on the database, held from the policy's reads until the winner's write is committed, so a second process waits and then sees the first winner: the same measurement ends every round with exactly one holder, under `ConflictReject` and under `ConflictSupersede`.

The cross-process lock is an optional capability of the database handle: a method `AcquireLock(ctx context.Context, name string) (release func(), err error)` that core asks for after the in-process lock, with the name `"smeldr:conflict:"` plus the type name. The method signature and the name prefix are a stable contract (D22) for every database adapter that implements it. `smeldr.dev/core/pgx` implements it (from v0.3.0) with a Postgres advisory lock on a dedicated pooled connection; a handle without the method, including SQLite and any wrapper, keeps the in-process lock only. A wrapper that embeds `smeldr.DB` hides the method: forward `AcquireLock` to keep the lock. Like the in-process lock it is fail-open: after 10 seconds without the lock the transition goes ahead and an Error is logged naming the type, the lock and the time waited. The pgx adapter refuses the lock whenever fewer than two pool connections are free at that moment (a holder that took the last one could not run its own statements), with the same Error line, rather than hanging. That can happen under ordinary load on a small or busy pool, not only on a pool of one: a recurring Error line about the cross-process lock means raise `MaxConns`. If the lock's connection is lost while it is held, Postgres drops the lock with the session and exclusion is gone for that transition; this is logged at Error when the lock is given up. SQLite with several processes on one file is not covered (measured: two processes moving two items at the same moment ended every round with two holders). Closed by `core-conflict-policy-cross-process-exclusion`.

### `StateFlow`

```go
type StateFlow struct {
    Name          string
    TypeName      string
    Description   string
    States        []State
    Transitions   []Transition
    ActiveState   string       // optional: state where uniqueness is enforced (A186)
    ConflictPolicy ConflictPolicy // optional: how to handle conflicts (A186)
}
```

### `State`

```go
type State struct {
    Name              string
    IsInitial         bool
    IsTerminal        bool
    SuppressesSignals bool
}
```

### `Transition`

```go
type Transition struct {
    From              string
    To                string
    RequiredOperation string // empty = any authenticated role; matched via RoleStore.Authorized (D63/D64, A309)
    RequiredReason    bool   // A220: caller must supply a non-empty reason
}
```

`RequiredReason` gates on the `reason` argument threaded by `App.TransitionItemWithReason`, `Module[T]`'s `MCPPublish`/`MCPSchedule`/`MCPArchive`, and `updateHandler`'s `Smeldr-Reason` request header (T237) — an empty reason against a `RequiredReason` transition returns `ErrBadRequest`.

### `App.RegisterFlow`

```go
func (a *App) RegisterFlow(flow StateFlow) error
```

Registers a state flow for a content type. Idempotent — safe to call on every startup, upserted by `TypeName` (not `Name`), so renaming a flow updates the existing row in place rather than orphaning it (T268). The flow is stored in `smeldr_state_flows`; states in `smeldr_states`; transitions in `smeldr_transitions`. `ActiveState`, `ConflictPolicy`, and `Description` are updated on every call. **State flags (v1.113.0).** Every flag of a state that already has a row (`IsInitial`, `IsTerminal`, `SuppressesSignals`, `Locked`, `Standing`) is updated to the registered value, so a flag changed in code reaches a database created before the change (before, the upsert was `DO NOTHING` and a changed `Locked` was ignored for any existing row). A row whose flags are unchanged is not rewritten. Each change to an existing row is logged with the flow, state, flag, old and new value: at Warn for `locked` and `suppresses_signals`, at Info for the rest; a first registration logs nothing. When the registered states name an initial state, `is_initial` is cleared on rows the list no longer mentions (logged at Info), so moving the initial state never leaves two initial rows. A state dropped from a flow otherwise keeps its row. Because every flag updates, registering a state without `Locked` or `Standing` on a type that had them resets both.

### `App.TransitionItem` (D49)

```go
func (a *App) TransitionItem(ctx context.Context, typeName, slug, toState string) (map[string]any, error)
```

Moves the item identified by `typeName` and `slug` to `toState` — resolving whichever table stores it: a compiled `Module`'s own table, or `smeldr_dynamic_content` for a runtime-defined type. Validates the transition exactly as the HTTP path and `DynamicTypeRepo.SetStatus` already do (`validateTransition`, including any `RequiredOperation`/`Strict` gate). Backs `smeldr.dev/mcp`'s `transition_item` tool.

```go
result, err := app.TransitionItem(ctx, "Signal", "some-signal-slug", "read")
// result: map[string]any{"id": ..., "slug": ..., "status": "read"}
```

- Runtime-defined type (`TypeDescriptor.Kind == "content"`) — delegates unchanged to `DynamicContentRepo` + `SetStatus`. Zero behavioural difference from calling those directly.
- Compiled type (`Kind == "compiled"`) — resolves the table via `resolveItemTable`, then performs a raw `UPDATE … SET status = …` after `validateTransition` and `applyConflictPolicy` pass. **Does not advance `Node.Rev`** — the same choice `SetStatus`/`applyConflictPolicy`'s own supersede path already make for their raw status updates. A concurrent `Save` holding the item's pre-transition rev still satisfies `Save`'s CAS check and silently overwrites the status change with whatever status its own in-memory item carries.
- Fires `fireAsyncTriggers` after a successful compiled-type update (A240's `TransitionTrigger` pipeline) — the same, and only, hook `SetStatus` fires for the dynamic path. Does **not** fire `module.go`'s `notifyAfter`/`AfterPublish`-class signal-bus hooks for either path; that mechanism is orthogonal to state-flow transitions in this codebase, not something this method newly declines.
- An unregistered `typeName` returns an error wrapping `ErrBadRequest`. A registered-but-missing item returns `ErrNotFound`. A compiled type whose backing table cannot be found (partial migration, an unregistered module) returns a distinct error rather than silently falling into the dynamic-content branch — matching D47's own guard against exactly that ambiguity.
- `slug` also accepts a compiled type's own human-facing identifier as a fallback, when it has one — `Task.TaskID` ("T203"), `Goal.GoalID`, `Decision.DecisionNumber`, `Amendment.AmendmentNumber` — tried only after a real slug lookup misses (A262, T253). The result's own `"slug"` is always the item's real slug, never the identifier you passed in.

### `App.TransitionItemWithReason` (A256, v1.65.0+)

```go
func (a *App) TransitionItemWithReason(ctx context.Context, typeName, slug, toState, reason string) (map[string]any, error)
```

`App.TransitionItem` with a caller-supplied `reason`, needed to satisfy a
`Transition.RequiredReason` gate (A220) — `App.TransitionItem` itself always
passes an empty reason and can never satisfy one. Mirrors
`DynamicTypeRepo.SetStatus`/`SetStatusWithReason`'s own shape: a second
method rather than a signature change, so `App.TransitionItem`'s existing
callers are unaffected. Backs `smeldr.dev/mcp`'s `transition_item` tool's
optional `reason` argument.

```go
result, err := app.TransitionItemWithReason(ctx, "Decision", "some-decision-slug", "superseded", "no longer accurate")
```

**`last_actor` (D78, Amendment A347):** the returned map's `"last_actor"` key
echoes the caller's own actor ID (empty for a caller with no `smeldr.Context`
identity). The same value is persisted onto the transitioned row's own
`last_actor` column, exposed automatically via `get_task`/`list_tasks`/
`get_decision`/etc. — on the six orchestration types (`Signal`/`Task`/
`Decision`/`Amendment`/`Goal`/`Run`) and `DynamicNode`. A compiled type's own
table that predates this column (any table outside these seven — a
third-party module) keeps working unchanged: the write fails open to the
original two-column UPDATE rather than erroring.

**`last_actor` is also set on create, not only on transition (01a0e3bc-8).**
`Module[T].MCPCreate` stamps `last_actor` from the caller's own
`ctx.User().ID` for any type with a settable `LastActor` field — covers the
same six orchestration types (create tools: `create_task`, `create_decision`,
etc.). Before this, a never-transitioned item (every fresh `proposed`
Decision, for example) had no `last_actor` at all — no proposer/asserter
recorded. Always overwrites whatever the create payload itself may have set
for `last_actor`; actor identity is never client-suppliable.

**`last_actor` means the most recent writer (v1.133.0; Option A,
Peter, 2026-10-08).** On a compiled type, every create and content update
stamps `last_actor` from the caller, as transitions already did and as dynamic
items do since v1.130.0: HTTP `POST`, `PUT` and `PATCH`, and the MCP
`create_{type}` and `update_{type}` tools. A value in the request body is
ignored on every one of them, and a full-replace `PUT` that omits the field no
longer erases it. A caller with no user ID (a system write) keeps the stored
value on an update and leaves it empty on a create. So an edit moves
`last_actor`: a reader that wants the item's proposer reads the `create` entry
of `get_item_provenance`, not `last_actor`.

### `App.TransitionItemVia` (A392, v1.109.0+)

```go
func (a *App) TransitionItemVia(ctx context.Context, surface, typeName, slug, toState, reason string) (map[string]any, error)
```

`App.TransitionItemWithReason` with the entry point named. `surface` takes the same values as
`ProvenanceRecord.Surface` (`"http"`, `"mcp"`, `"cli"`, `"trigger"`) and may be empty when the
caller cannot tell; it is written onto the `ProvenanceRecord` the transition produces (see
[Provenance trail](#provenance-trail)). `App.TransitionItem` and `App.TransitionItemWithReason`
keep their signatures and call it with an empty surface, so their callers are unaffected.
Validation, authorization, `last_actor` and the returned map are identical to
`TransitionItemWithReason`.

```go
result, err := app.TransitionItemVia(ctx, "mcp", "Task", "some-task-slug", "active", "")
```

### `ConflictPolicy` (A186)

```go
type ConflictPolicy string

const (
    ConflictReject    ConflictPolicy = "reject"
    ConflictSupersede ConflictPolicy = "supersede"
)
```

Optional field on `StateFlow`. When `ActiveState` is set and an item transitions to that state:

- `ConflictReject` — returns `ErrConflict` if any item of the same type is already in `ActiveState`.
- `ConflictSupersede` — transitions the other items in `ActiveState` to `"superseded"` when an item enters it (since v1.112.1 the entering item's own write comes first, see below).
  Since v1.111.0 each superseded item also gets a `supersedes` relation edge from the winner (by item ID, with the triggering actor as `created_by`), **but only when** the instance wired `App.Relations` **and** a `supersedes` relation kind that permits `Type -> Type` is registered. `RegisterOrchestrationRelationKinds` registers that kind for `Decision -> Decision` only, so a customer-defined type needs its own: `UpsertKind` a `supersedes` kind (`Mode: "asserted"`, `Directional: true`) whose `TypePairs` is empty (every pair) or lists your type, registered after `RegisterOrchestrationRelationKinds` and keeping its `Decision -> Decision` pair, since a kind has one definition. Otherwise items are still superseded and recorded, without an edge, and one Info line per type per process says so. `RelationStore.TraceLineage` from a superseded item then reaches its replacement.
  **Order and atomicity (v1.112.1).** The other items are looked up first, with reads only (and `ConflictReject`'s `ErrConflict` is returned then, before anything is written); then the entering item's own write; then the items it supersedes; then their provenance, standing and `supersedes` edge, recorded only for the items that really moved. A failed write of the entering item therefore leaves every other item untouched and nothing recorded. For `App.TransitionItemVia` and a runtime-defined type the entering item's write and the losers' writes run in **one transaction** when the database handle supports `BeginTx` (a `*sql.DB` does), so they succeed or fail together; on a handle without `BeginTx` the same writes run in the same order without atomicity. `smeldr.dev/core/pgx` provides `BeginTx` from adapter v0.3.0; through v0.2.x it did not, so on Postgres through that adapter these writes ran statement by statement (this was read from the code, not measured). The `Module` lifecycle methods (`MCPPublish`, `MCPSchedule`, `MCPArchive`) write the entering item through the module's own repository, which cannot join that transaction, so they are not atomic: if the second write fails (or the process dies between the two) the type is left with **more than one item in its active state**, never none, and a failed loser write is logged at Error. A failed write of one superseded item is skipped and not recorded (fail-open, as always); on Postgres a failed statement aborts the surrounding transaction, so there the whole transition fails and nothing is left half done (proved on postgres:16 in the pgx suite, with a loser write refused by a server trigger), on SQLite it is skipped. Both leave the data consistent. The one statement core expects to fail sometimes inside this transaction, the `last_actor` fail-open of a table that predates the column, runs under a `SAVEPOINT` so it cannot abort a Postgres transaction (v1.119.1; before it, such a table gave an internal error "current transaction is aborted" on a transition on Postgres through a handle with `BeginTx`, since v1.118.0). An item already in the active state is never selected as its own loser. **Enforced paths (v1.116.0).** Every status writer in core either applies the policy or cannot reach the active state. It applies on: `App.TransitionItemVia` (and so the `transition_item` tool), `DynamicTypeRepo.SetStatus` (and so `POST /_content/{type}/{id}/status`) and `DynamicTypeRepo.ScheduleContent`, `Module.MCPPublish`, `MCPSchedule` and `MCPArchive`, **`Module`'s HTTP PUT when it changes status** (a rejected PUT is **HTTP 409**, nothing saved; under supersede the losers follow the save), **the scheduler's publish** (a rejected scheduled item stays `scheduled` and is retried every tick, so it can sit past its scheduled time with no end while another item holds the active state: that is the intended meaning, not a stuck scheduler; one Warn is logged per item, saying so) and **`App.DrainEvalQueue`** (a rejected drained transition is skipped, logged, and its queue row deleted as for any other blocked transition). It cannot reach the active state on: the create paths (`createHandler`, `MCPCreate`), because `validateInitialState` only admits the flow's initial state; and `MCPUpdate`/`updateFields`, which keep the existing status. **Behaviour change in v1.116.0:** a PUT or a scheduled publication that used to succeed for a type with a policy can now be a 409 or a wait.

  **Concurrent winners (v1.115.1).** Transitions of one type into its `ActiveState` are serialised within the process: the policy's reads, the entering item's write and the losers' writes happen under one per-type lock, so two simultaneous winners cannot both pass the reads before either writes (before, both became active under `ConflictSupersede` and both passed the check under `ConflictReject`). The second winner sees the first one committed: it is rejected under `ConflictReject`, and supersedes it under `ConflictSupersede`. The lock covers `App.TransitionItemVia`, `DynamicTypeRepo.SetStatus` and the three `Module` lifecycle methods. A nested transition of the same type made under the context the call hands on (a hook, say) is not blocked by its own caller. If the lock cannot be had within 10 seconds the transition goes ahead unlocked and an Error is logged, in keeping with both policies being fail-open; a cancelled context while waiting is an `ErrInternal`. **Limits:** this lock is in-process; the cross-process lock a Postgres handle adds on top of it (v1.119.0) is described under State flows. Before v1.118.0 the policy was enforced on SQLite only.

Zero value (empty string) means no enforcement. Both policies fail-open: DB errors return nil so the transition is never blocked by a hiccup in the conflict check.

```go
app.RegisterFlow(smeldr.StateFlow{
    Name:           "article-flow",
    TypeName:       "Article",
    ActiveState:    "published",
    ConflictPolicy: smeldr.ConflictReject,
    States: []smeldr.State{
        {Name: "draft", IsInitial: true},
        {Name: "published"},
        {Name: "archived", IsTerminal: true},
    },
    Transitions: []smeldr.Transition{
        {From: "draft", To: "published"},
        {From: "published", To: "archived"},
    },
})
```

### Standing (D100)

Standing says whether an item's claim is in force. It is separate from the governed state name (D56): the state name says where the item is in its lifecycle, standing says whether it holds.

```go
type Standing string

const (
    StandingHolds       Standing = "holds"        // in force (also the one legal State.Standing tag)
    StandingCeased      Standing = "ceased"       // held, no longer does
    StandingNone        Standing = "none"         // never held
    StandingNotRecorded Standing = "not recorded" // history would decide it, and it was not recorded
)
```

**Declaring it.** A flow state may carry `Standing: smeldr.StandingHolds`, which says an item in that state is in force. Any other non-empty value is rejected by `App.RegisterFlow` with `ErrBadRequest`. A customer-defined type declares its own the same way. Smeldr's own flows tag Decision `ratified` and `pending-re-evaluation`, and Amendment `merged`; Signal, Task and Goal have no standing.

```go
smeldr.State{Name: "ratified", Locked: true, Standing: smeldr.StandingHolds}
```

`App.RegisterFlow` updates the tag of a state row that already exists (the other state flags, `locked`, `suppresses_signals`, `is_initial`, `is_terminal`, still do not update an existing row).

**What each item stores.** Each item of a type whose flow tags a state stores its own standing in `smeldr_standing(subject_type, subject_id, standing, updated_at)`. The code that changes the item's state is the only writer:

- entering a state that holds (from an untagged state, or on creation) stores `holds`;
- leaving a state that holds for an untagged state stores `ceased`;
- any other move changes nothing, so an item that never held stays `none` (a Decision archived straight from `proposed` is `none`, never `ceased`) and a ceased item stays `ceased`.

An item with no row is `none`. Every path that changes state writes it: `App.TransitionItemVia`, `DynamicTypeRepo` status changes and creation, the Module lifecycle paths (HTTP and MCP, through `notifyAfter`), the conflict-supersede side effect (an automatically superseded Decision becomes `ceased`), core's Signal resolver (D115) and `DrainEvalQueue`. The write is synchronous and fail-open: a failure is logged and never fails the state change that was already made, and `App.CheckStandingDrift` is the backstop. It is written whether or not provenance is wired.

**Events.** Every change of standing is also recorded as a provenance event, verb `"standing-began"` or `"standing-ended"`, carrying the transition's own actor, surface, reason and from/to states, when `App.Provenance` is wired. `SubjectProvenance` returns them like any other entry and gates them exactly like the transition (the actor is shown only for a transition that required an operation under `Strict` enforcement), so ratifying a Decision now reads as two entries.

**Reading it.**

```go
func ItemStanding(ctx context.Context, db DB, typeName, id string) (Standing, bool, error)
func CountStanding(ctx context.Context, db DB, typeName string) (map[Standing]int, error)
func TypeHasStanding(ctx context.Context, db DB, typeName string) bool
func ItemStandings(ctx context.Context, db DB, typeName string, ids []string) (map[string]Standing, error)
```

`TypeHasStanding` and `ItemStandings` (since v1.114.0) are for a caller that shows a page of items: ask `TypeHasStanding` once, then `ItemStandings` once, instead of `ItemStanding` per item. `ItemStandings` costs one tagged-state lookup plus one query per 400 ids, and returns an entry for every id asked: an item of a type with standing but no stored row is `StandingNone`, never absent. For a type with no standing (or empty `ids`) the map is empty and no standing query is issued, so an absent id means the whole type has no standing, never that one item is unknown. A failed read is `ErrInternal`; `TypeHasStanding` is fail-open (false). `ItemStanding` is `ItemStandings` for one id.

`ItemStanding` returns `(_, false, nil)` for a type whose flow tags no state, and `StandingNone` for an item of a type that has standing but no stored row. `CountStanding` counts stored rows by value (an item without a row is `none` and is not counted). Both are plain functions over a `DB` handle, like `SubjectProvenance`. `BuildContextPacket` also fills an additive `standing` field on the anchor and on each item (omitted for a type without standing).

**Items that existed before.** `MigrateStanding(ctx, db)` gives them a standing, once per type (marker table `smeldr_standing_migrations`), from the flow graph **as registered when the migration ran**, and writes nothing it cannot be certain of: `holds` for an item in a state that holds (start unknown); `ceased` for a state only reachable from states that hold (Decision `superseded`); `none`, left as no row, for a state no path from a state that holds reaches (Decision `proposed`, Amendment `scoped` to `committed` and `rejected`); `not recorded` for anything else (Decision `archived`, reachable from `proposed` and from `superseded`). Run it at boot after the flows are registered; it is idempotent, and an item created afterwards without a row is `none` and is never relabelled. Retagging a flow later never recomputes stored standings: only later transitions use the new tags.

**Drift check.** `App.CheckStandingDrift(ctx) (checked, drifted int, err error)` compares each item's stored standing with its state's tag (an item in a state that holds must hold; one in an untagged state must not) and records a `Finding` (detector `standing-drift`) per mismatch when `App.Findings` is wired. It only reports; it never repairs. The example server schedules it behind `ENABLE_STANDING_DRIFT_CHECK` (default off, `STANDING_DRIFT_CHECK_SCHEDULE`, default `30 3 * * *`), and wires `App.Findings` (with `CreateFindingTable`) whenever governance records or the structural sweep are on (T948), so the structural sweep, the eval queue drain, this check and declared-tension aggregation record their findings. No MCP, CLI or HTTP surface reads findings yet: read them with `App.FindingStore().List` or from `smeldr_findings`.

**Upgrading.** `smeldr_states.standing` is added by `New` to an older database (and by `EnsureStateStandingColumn`); `smeldr_standing` and `smeldr_standing_migrations` are created with the flow tables. The example server also calls `EnsureStateStandingColumn` and `MigrateStanding` at boot.

### Awaits action: open and closed (D109, v1.141.0)

An item is **open** when it waits for someone to act, and **closed** otherwise (D109). The flow says which, per state: `State.AwaitsAction bool` is the developer's name for the operator's word "open" (the field is not called `Open` because Goal has a state named `open`, and D98's "held open" is a count). It is independent of `IsTerminal` and of standing: a ratified Decision is closed and holds standing; a Decision in `pending-re-evaluation` is open and holds standing.

```go
smeldr.State{Name: "blocked", AwaitsAction: true}
```

**Smeldr's own flows** declare D109's table:

| Type | Open | Closed |
|---|---|---|
| Decision | proposed, pending-re-evaluation | ratified, superseded, archived |
| Task | backlog, active, waiting-plan, plan-reviewing, implementing, commit-reviewing, blocked | done, deferred, resolved |
| Amendment | scoped, in-progress, commit-ready, committed | merged, rejected |
| Goal | open, in-progress, parked | done, resolved |
| Signal | open | resolved, answered, withdrawn; history: pending, read, acknowledged, expired (D115) |

Every other state is closed, including Rule and AuthorityStub and the default flow, which declare none: **declared closed by omission**. A state without the declaration, and a status the flow does not name, count as closed, so nothing unknown is shown as waiting.

**Reading it.** Nobody keeps a copy of the table; readers ask the flow.
- `StatesAwaitingAction(ctx, db, typeName) (map[string]bool, found bool, error)` returns the open state names of the type's flow (its own, else the default flow). `found` false means no flow resolved: show unknown, never closed.
- `TransitionOption.ToAwaitsAction` (from `App.ValidTransitions`) is the target state's answer, so false marks a transition that closes the item.
- The context packet carries `awaits_action` on the anchor and on each item (true or false; omitted only when it cannot be read).
- MCP item reads carry it next to `standing` (see the mcp section).

**Stored with the flow.** `smeldr_states.awaits_action` (BOOLEAN, default false) is written by `App.RegisterFlow` in the same upsert as the other flags, and a change on an existing row is logged at Info. `New` adds the column to an older database (also `EnsureStateAwaitsActionColumn`, which the example server calls at boot). The next registration of each flow then writes its declared values, so an existing instance gains them on its first boot of this version, with no SQL step. A flow defined earlier through `define_state_flow` stays closed until it is defined again with `awaits_action`. Setting it is part of defining a flow (`define-flow`); there is no new operation.

### `ErrConflict`

```go
var ErrConflict = smeldr.NewError(409, "conflict", "conflict with existing active item")
```

Returned by `MCPPublish`, `MCPSchedule`, and `DynamicTypeRepo.SetStatus` when `ConflictReject` is active and a conflicting item exists.

### `TransitionTrigger` (A187)

```go
type TransitionTrigger struct {
    FromState    string
    ToState      string
    TriggerClass string // "async"
    TriggerType  string // "schedule-eval"
    Config       string // JSON config string
}
```

Declared in `StateFlow.Triggers []TransitionTrigger`. Persisted by `App.RegisterFlow` to `smeldr_transition_triggers` (idempotent — skipped if a trigger of the same type already exists for the transition).

The only supported `TriggerType` in v1.47.0 is `"schedule-eval"`. Config JSON: `{"eval_field":"<column>","to_state":"<target>"}`.

### `App.DrainEvalQueue` (A187)

```go
func (a *App) DrainEvalQueue(ctx context.Context) (triggered, skipped int, err error)
```

Drains due rows from `smeldr_eval_queue` (where `eval_at <= now`). For each row:

1. Resolves the item's table via `resolveItemTable` (probes `sqlite_master` for `smeldr_<snake>s`, then `<snake>s`, then falls back to `smeldr_dynamic_content`).
2. Applies a direct `UPDATE … SET status = ?, updated_at = ?` on the item.
3. Deletes the queue row regardless of UPDATE success.

Returns `triggered` (successful updates) and `skipped` (UPDATE failed or table not found). Fail-open: nil DB and missing table return `(0, 0, nil)`.

Wire with `agent.NewEvalQueueScheduler("", "UTC", app)` for automatic drain on a cron schedule.

### `smeldr_eval_queue` table

Created by `migrateStateFlows()` — no manual call needed.

```sql
CREATE TABLE IF NOT EXISTS smeldr_eval_queue (
    id         TEXT     PRIMARY KEY,
    type_name  TEXT     NOT NULL,
    item_id    TEXT     NOT NULL,
    to_state   TEXT     NOT NULL,
    eval_at    DATETIME NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(type_name, item_id, to_state)
)
```

The `UNIQUE` constraint prevents duplicate queue entries for the same item+transition.

---

## Orchestration types

`orchestration.go` — five content types for the architect/pilot protocol plus context-packet query.

### `CreateOrchestrationTables`

```go
func CreateOrchestrationTables(db DB) error
```

Creates `smeldr_signals`, `smeldr_tasks`, `smeldr_decisions`, `smeldr_amendments`, `smeldr_goals` and `smeldr_runs` tables
if they do not already exist. Call once at application startup before `RegisterOrchestrationTypes`.

**Core governance and the Orchestration add-on (T881).** The six types are two products: core governance (`Signal`, `Decision`, `Amendment`) and the Orchestration add-on (`Task`, `Goal`, `Run`; `task_plan` is runtime-defined on the instance). Each half has its own calls, and the combined ones call both:

| | Core governance | Orchestration add-on | Both (unchanged) |
|---|---|---|---|
| Tables, addresses | `CreateGovernanceTables` (S, D, A) | `CreateOrchestrationAddOnTables` (T, G, R) | `CreateOrchestrationTables` |
| Types, flows | `RegisterGovernanceTypes` | `RegisterOrchestrationAddOn` | `RegisterOrchestrationTypes` |
| Relation kinds | `RegisterGovernanceRelationKinds`: amends, supersedes, contradicts, addresses, grounded_in, belongs_to_domain, belongs_to_area, in_set, about, refers_to | `RegisterOrchestrationAddOnRelationKinds`: derives_from, depends_on, ships_as, contains, investigates, implements | `RegisterOrchestrationRelationKinds` |

Call the governance half first. Every core record type is added to `RegisterGovernanceTypes`. On an instance without the add-on:
- a `task_ref` (Signal or Amendment) is refused as a validation error that names the add-on, on the module path and on `InsertSignal` alike; an empty `task_ref` is fine;
- `QueryGoalContext` and a context packet anchored on a task, goal or run are a bad request, not an internal error; mcp lists `get_goal_context` only when Goal is registered;
- role subscriptions on Task and task_plan are left out of the seed and reported (`RoleSeedReport.LeftOut`);
- a subscription filter naming Task is refused as an unregistered type.

The example server wires core governance with `ENABLE_GOVERNANCE_RECORDS` and both with `ENABLE_ORCHESTRATION` (which implies it). `ENABLE_GOVERNANCE` is roles and grants, not records.

### Item addresses (D110)

Every record carries a system-assigned address: its type's prefix plus a running number (`D110`, `T412`, `INV27`). The address is unique on the instance and never changes.

- **Fields.** `RecordAddress` is embedded in `Signal`, `Task`, `Decision`, `Amendment`, `Goal`, `Run`, `Rule`, `AuthorityStub` (core v1.156.0, A510) and `DynamicNode`: `Address` (`address`), `AddressNo` (`address_no`) and `ExternalRef` (`external_ref`, free text for your own numbering, writable). `Address` and `AddressNo` carry `smeldr:"readonly"`: they are never part of create or update input.
- **Assignment.** The number is taken in the create's own transaction on every create path (MCP and HTTP create of the six types, a dynamic record create, `InsertSignal`). A failed create takes no number; concurrent creates never share one; a number is never reused, even after a delete.
- **Prefixes.** Core reserves the single letters: Decision `D`, Amendment `A`, Task `T`, Goal `G`, Signal `S`, Run `R`, task_plan `P`, Rule `U`, AuthorityStub `B`; every other single letter is reserved too. A runtime-defined type without a `URLPrefix` is a record type and gets 2 to 4 capital letters: `ContentTypeSchema.AddressPrefix`, or, when empty, a proposal from the name (`invoice` gives `INV`, then `INVO`). A type with a `URLPrefix` is web content and has no address. A prefix is fixed once the type has an item; a redefinition without one keeps it.
- **Storage.** `CreateOrchestrationTables`, `CreateAuthorityTables` and `CreateBlockTables` add the columns, a partial unique index (`address <> ''`, per type on `smeldr_dynamic_content`) and the `smeldr_address_counters` table (one row per type: prefix and last number). On a database with older rows the counter starts above every existing legacy number of the type, so new addresses never collide with ones the migration keeps.
- **Immutability.** MCP update and PATCH refuse a changed `address` or `address_no`; PUT keeps the stored address.
- **Events and packets.** Webhook and event-stream payloads carry `address`, transition events included. A context packet's anchor and item IDs are the address when the record has one; `run` is an anchor type too.
- **Naming a record.** `ParseAddress(s) (prefix string, no int64, ok bool)` parses an address: 1 to 4 letters and a number with no leading zero, the prefix upper-cased (`d110` is `D`, 110). Every tool and function that takes a record's identifier (MCP get, update and transition, `App.TransitionItemVia`, `DynamicTypeRepo.Resolve`, the packet anchor, `QueryGoalContext`, `amends`) accepts its **address, slug or ID**, in that order. An address is only tried when its prefix is the type's own. Until the D110 cut, the last step finds a record by an old number: its legacy human ID, or its `external_ref` (where a value a caller sent in the legacy field is kept), exact match only. Two records holding that value are an error naming the count, and every such lookup is counted in the deprecation log.
- **Transition period (until the cut).** The legacy fields `decision_number`, `task_id`, `goal_id` and `amendment_number` are still accepted on create, marked deprecated (`MCPField.Deprecated`, and a description that starts "Deprecated (D110):"). A value sent there is never used as the number: when it differs from the assigned address it is kept in `external_ref` (unless `external_ref` was sent too), and the legacy field holds the address. An MCP create that received one returns its item with a `warnings` array: `{"code": "deprecated_field", "field", "message", "address"}`. The server logs each deprecated input once per token, per field, per day. A bare number in `amends` (`"104"`) is accepted and logged the same way. `RetiredFieldGuidance(field)` returns the error message and data a create gets after the cut.

### `MigrateAddresses` (D110)

```go
func MigrateAddresses(ctx context.Context, db DB, opts AddressMigrationOptions) (*AddressMigrationReport, error)
func (a *App) MigrateAddresses(ctx context.Context, opts AddressMigrationOptions) (*AddressMigrationReport, error)
```

Gives every record that has no address yet its address, per type in `created_at` order, in one transaction. It is idempotent: a record with an address is skipped, so a second run changes nothing. With `DryRun` the same code runs and is rolled back, so the report is exactly what an apply would do and nothing is written.

- **Decision, Amendment, Task, Goal** keep a legacy number that parses with the type's prefix and no other record holds. Every other record (a duplicate, another prefix, free text, empty) gets a number through the counter, above every number already taken, so a record created after the deploy never collides. Its old value goes to `external_ref`, and the legacy field holds the address.
- **Signal, Run, Rule, AuthorityStub and dynamic record types** number their records from 1 within the range reserved for them when the counter was first seeded (`reserved_no`), skipping any number already in use, then through the counter.
- **References** to a renumbered record are rewritten only where they name exactly one: `Amendment.amends` (through its live `amends` edge), `Signal.task_ref`, `Signal.subject_id`, `Task.note_ref`. Anything ambiguous, or an `amends` with no edge, is listed in `LeftAlone` and not changed.
- **The report** (`AddressMigrationReport`) lists per type the prefix, counts kept, renumbered and newly numbered, counts with and without an address before and after, and every renumbered record `{id, old, new}`; then every rewrite and every reference left alone; and `duration_ms`, how long the transaction held the database's writer. Names and addresses only, never body text.

**`smeldr-server migrate-addresses [--apply] [--report=path]`** (example/server) opens the configured database, sets the schema up as boot does, and runs the migration: a dry run unless `--apply` is given. It prints the report as JSON, and with `--report` also writes it to that file. On SQLite it holds the one writer for the whole transaction; see `duration_ms`.

**Over MCP** (an instance reached only through its URL and token, such as a Cloud org), smeldr.dev/mcp's `migrate_addresses` tool runs the same function. Its `smeldr_tool_policies` seed row is `{"migrate_addresses", "administer"}` (A490), so only Admin may call it. Without the row the tool is denied for every caller, Admin included, whenever governance is wired. The row is inserted at the next boot of an existing instance. From a terminal, smeldr.dev/cli v0.26.0 calls the tool: `smeldr-cli migrate-addresses [--apply] [--report <path>]` (A492).

### Titles and closed vocabularies (A508, D112)

Decision, Task, Goal and Amendment carry a title: one line, at most `TitleMaxLength` (120) characters, counted in characters, a short statement of what the record is. An Amendment's `summary` is its title. Signal has no title. `TitleGuidance` is the exported guidance text: "a title is required: one line of at most 120 characters, a short statement of what the record is (D112)".

Warnings come first. A write without a title, or with one over the limit, succeeds with a warning until the cutover. The codes are `title_missing` and `title_too_long`, in the `warnings` array next to `slug_adjusted` and `deprecated_field`; each names the field (`title`, or `summary` for an Amendment) and the record's address.

The cutover is `Config.TitleCutover`. Its zero value means warnings only, and the error phase starts only when an operator sets the date: the date belongs to whoever runs the instance, and process.smeldr.dev's is set by Peter. After it, the same write is refused with a validation error on the title field whose message includes `TitleGuidance`. example/server reads it from `TITLE_CUTOVER` (RFC3339); a malformed value stops the server from starting.

| Write | Before the cutover | After the cutover |
|---|---|---|
| MCP create without a title, or with one over 120 characters | the item, with `title_missing` or `title_too_long` in `warnings` | refused: validation error on the title field |
| MCP update that touches the title, leaving it missing or too long | the item, with the warning | refused |
| MCP update that does not touch the title | passes, no warning | passes |
| HTTP create, or a PUT or PATCH that changes the title, without a valid one | passes; no warnings in the body, logged once per actor, type, code and day | refused |
| HTTP PUT or PATCH that leaves the title as it is | passes | passes |

The MCP input schema does not mark `title` required and sets no `maxLength`, so a strict client does not refuse the call before the server can warn.

- **Slugs.** New Task and Goal slugs come from the title, as Decision slugs already do. An explicit slug still wins, and existing slugs never change.
- **`Titled`.** Decision, Task, Goal and Amendment implement `ContentTitle()`, so webhook bodies and signal events carry the title; the context packet's Task and Goal fields include `title`.
- **`App.CapabilityWarnings(resource, verb) []string`** returns the warning codes an act can return with a successful result. Item create: `slug_adjusted`, `deprecated_field`, `title_missing`, `title_too_long`. Item update: `deprecated_field`, `title_missing`, `title_too_long`. A copy; nil when there are none.
- **`AmendmentTypes`**: feature, fix, docs, chore, test, ci, refactor, release. A create must use one of them (empty is refused); an update that changes the type must use one; an update that does not change it, or sends it empty, keeps the stored value. An unknown value is a validation error that names the list. The MCP schema advertises the list as `enum` through the `smeldr_enum` struct tag, which is schema only: the type checks the list itself.
- **`Amendment.Pilot`** is deprecated and not stored; the column stays, the field is not emitted, the MCP schema marks it "Deprecated (D112): ...", and an MCP create or update that sends it gets a `deprecated_field` warning.
- **`done` and `resolved`.** The Task and Goal flows' `StateFlow.Description` says: done, the work was carried out; resolved, it closed without being carried out (superseded, declined, or found to be already built), and a reason is required. Every transition into `resolved` requires a reason.
- **Not in this release** (Task `core-dynamic-titles-and-field-limits`): a title field for customer record types at `define_content_type`, dynamic field length limits, task_plan's `summary`, and warnings on `create_content` and `update_content`.

#### `MigrateRecordShape`

```go
func MigrateRecordShape(ctx context.Context, db DB, opts RecordShapeMigrationOptions) (*RecordShapeMigrationReport, error)
func (a *App) MigrateRecordShape(ctx context.Context, opts RecordShapeMigrationOptions) (*RecordShapeMigrationReport, error)
```

One transaction, idempotent; a dry run (`DryRun: true`) runs the same code and rolls back.

- Untitled Tasks and Goals get the first line of their description, untitled Decisions the first line of their body: heading, list and quote marks and emphasis removed, cut at a word boundary at 120 characters, no ellipsis. A record with no usable text is reported in `untitled` and left.
- Amendment summaries over 120 characters are cut the same way; the full summary is put first in the body unless the body already contains it (`body_kept`), so no text is lost. A merged Amendment is otherwise locked: this migration is the only path that edits it, and the report lists every Amendment it changed, by address.
- `amendment_type`: `feat` to `feature`, `bugfix` to `fix`, `deploy` to `release`. An entry in `AmendmentTypeMap` (see below) takes precedence over these rules. Any other value outside the list is reported in `unmapped_amendment_types` for a person to map, never guessed ("Level 1" and "Level 2" are a size, not a type).
- `AmendmentTypeMap map[string]string` on `RecordShapeMigrationOptions`: maps an Amendment to a type on the `AmendmentTypes` list (feature, fix, docs, chore, test, ci, refactor, release). A key is an Amendment address (`"A493"`) or number (`"493"`). Where the table has an address column, the key matches by address; otherwise it matches by amendment_number. The map applies only to an Amendment whose stored `amendment_type` is not on the list, and it wins over the built-in rules. Every entry is checked against the Amendments read, before anything is written. One bad entry refuses the whole run with an `ErrBadRequest` that lists every problem, sorted by key:
  - a value not on the list;
  - a key that is not `A<digits>` or `<digits>`;
  - a key that names no Amendment;
  - a key that matches more than one Amendment;
  - two keys that name the same Amendment;
  - an Amendment that already has a valid type;
  - a map given when there is no `smeldr_amendments` table.

  After an apply, running again with the same map is refused, because each Amendment it names now has a valid type. Run again without the map; that run changes nothing.
- The report, `RecordShapeMigrationReport{dry_run, duration_ms, titles, amendment_types, untitled, unmapped_amendment_types}`, names each change by type, ID, address, field, old and new value. An `amendment_type` change also carries `source`: `"rule"` or `"type_map"`.
- It needs the Task and Goal title column; without it the error names `EnsureTaskGoalTitleColumns`.

**`smeldr-server migrate-record-shape [--apply] [--report=path] [--type-map=path.json]`** (example/server): a dry run unless `--apply` is given. `--type-map` takes a JSON object such as `{"A493": "fix", "A490": "feature"}`. A file that cannot be read or is not valid JSON exits 1 with the reason; core checks the entries the same way as the Go API. On process.smeldr.dev, applying it is devops' job on Peter's yes, after architect has mapped the unmapped types from the dry-run report, with that mapping passed with `--type-map`.

#### `MigrateReferences`

```go
func MigrateReferences(ctx context.Context, db DB, opts ReferenceMigrationOptions) (*ReferenceMigrationReport, error)
func (a *App) MigrateReferences(ctx context.Context, opts ReferenceMigrationOptions) (*ReferenceMigrationReport, error)
```

One transaction, idempotent (an edge already there is reported as `present`); a dry run (`DryRun: true`) runs the same code and rolls back. It turns the references existing records carry as text into the edges D113 asserts on write, and reports what it cannot resolve instead of guessing.

- **Signal.** The subject becomes an `about` edge, and a `subject_id` stored as a slug or address is rewritten to the subject's ID (`rewritten`). A `task_ref` that names a Task becomes an `about` edge.
- **Task `note_ref`.** A value that as a whole names a Decision becomes `implements`, a Goal `derives_from`. One naming a Task is reported only (a Task in a note is not a dependency). Free text is reported and never parsed.
- **Amendment.** An `amends` without a live edge becomes an `amends` edge, and a `task_ref` becomes `ships_as` from the Task.
- **Runtime-defined fields** declared with `references`, and those named in `RefFields` (`{"task_plan.task_ref": "Task"}`, for a field its schema does not declare yet).
- Resolution is by address, slug, ID and legacy number, without a read scope: it is an operator's migration.
- The report, `ReferenceMigrationReport{dry_run, duration_ms, created, present, rewritten, unresolved}`: each edge names its source, field, kind and target; each unresolved value names the record, the field, the value and the reason.
- It needs `smeldr_relations` (`ErrBadRequest` otherwise). A malformed `RefFields` entry is `ErrBadRequest` before anything runs.

**`smeldr-server migrate-references [--apply] [--report=path] [--ref-field=type.field=Type]`** (example/server): a dry run unless `--apply` is given; `--ref-field` may be repeated. On process.smeldr.dev it runs after `migrate-signal-kinds`, and applying it is devops' job on Peter's yes.

### `InsertSignal`

```go
func InsertSignal(ctx context.Context, db DB, s *Signal) error
```

Inserts one Signal into `smeldr_signals` in its own transaction. It fills an empty ID (a new ID), Slug (the ID) and CreatedAt/UpdatedAt (now, UTC), sets Status to the type's initial state (`open` for a catalog type, `pending` for a history type, D115), and writes them back onto `s`. It first runs `CheckSignal` with no cutovers: a condition type from a caller other than core is refused with `ErrForbidden`, and an empty `signal_type` with a validation error. `InsertSignalWithWarnings(ctx, db, s, app.SignalCutovers()) ([]CreateWarning, error)` is the same with the instance's cutovers, and returns the warnings (`deprecated_signal_type`, `message_too_long`). It announces nothing: dispatch the `signal.created` event yourself after it returns nil, through `App.NotifySignalCreated` when calling from outside core. On failure it returns a wrapped error (`smeldr: InsertSignal: ...`) and leaves no row. Since v1.161.0 it checks the subject and `task_ref` and writes their `about` edges in its transaction (see "References between records").

```go
s := &smeldr.Signal{
    Sender:     "core",
    Receiver:   "architect",
    SignalType: "plan-ready",
    Message:    "plan submitted",
}
if err := smeldr.InsertSignal(ctx, db, s); err != nil {
    return err
}
app.NotifySignalCreated(ctx, s.ID, s.Slug)
```

### `RegisterOrchestrationTypes`

```go
func RegisterOrchestrationTypes(app *App, db DB)
```

Registers the six record types of core governance and the Orchestration add-on with
their custom state flows and MCP read+write tools: `RegisterGovernanceTypes` (Signal,
Decision, Amendment), then `RegisterOrchestrationAddOn` (Task, Goal, Run). Fail-open: if
`db` is nil, flow registration errors are logged and startup continues. Call after
`CreateOrchestrationTables` and before `App.Run`. An instance without the add-on calls
`CreateGovernanceTables` and `RegisterGovernanceTypes` instead.

```go
smeldr.CreateOrchestrationTables(db)
smeldr.RegisterOrchestrationTypes(app, db)
```

### Content types

| Type | Table | State flow | Key fields |
|------|-------|------------|------------|
| `Signal` | `smeldr_signals` | signal-protocol (8 states, D115) | sender, receiver, signal_type, message, task_ref, sequence, subject_type, subject_id, from_state, to_state, required_role, resolution, answer, answer_ref |
| `Task` | `smeldr_tasks` | agent-task (10 states) | task_id, priority, band, size, description, note_ref, implements |
| `Decision` | `smeldr_decisions` | governance-decision (5 states) | decision_number, title, scope, body, next_eval_at, eval_note |
| `Amendment` | `smeldr_amendments` | amendment-lifecycle (6 states) | amendment_number, amendment_type, version, commit_hash, pilot, summary, body, amends, task_ref |
| `Goal` | `smeldr_goals` | goal-lifecycle (4 states) | goal_id, priority, band, size, description |

All five types embed `Node` and receive auto-generated MCP tools (`create_*`, `get_*`,
`list_*`, `update_*`, `publish_*`, `archive_*`, `delete_*`) via `MCP(MCPRead, MCPWrite)`.

Every one of those tools' `id`/`slug` argument resolves against, in order: the item's real
`slug`; then its real `Node.ID`; then, for `Task`/`Goal`/`Decision`/`Amendment` (which have
one), its own human-facing identifier (`Task.TaskID`, `Goal.GoalID`,
`Decision.DecisionNumber`, `Amendment.AmendmentNumber`) — `Signal` has none and stays
slug/ID-only. `get_task("T203")` and `get_task("<real slug>")` and `get_task("<real
Node.ID>")` all work identically (A262/T253, A266/T214). The response always reports the
item's own real slug, never the identifier you passed in.

The schemas declare the identifier as `slug`; `id` is accepted as its alias on every one of
these tools, `update_*` included (smeldr.dev/mcp v1.55.2). Both given with different values is
refused with -32602 rather than acting on either.

`Task` and `Goal` list results (`list_task`/`list_goal` via MCP, `GET /tasks`/`GET /goals`
via HTTP) are sorted by `Priority` ascending by default (lower number is higher priority) —
`smeldr.DefaultListOrder("Priority", false)`, set once at registration (A267/T262). `Signal`,
`Decision`, `Amendment`, `Run` have no `Priority` field and keep the repository's own natural
order, unchanged.

`create_task`/`create_goal` (MCP only — not the HTTP `POST` create route) default an
omitted `priority` field to `5`, never `0` — `0` is reserved for a deliberate escalation
(`Priority=0` on a `Task` is the only value that authorizes an implementer to start work
without asking, per `AGENT_PROTOCOL.md`'s own gate rule). Explicitly passing `priority: 0`
is still honoured as a real escalation.

### `DefaultListOrder` (A267, T262)

```go
func DefaultListOrder(field string, desc bool) Option
```

A `Module[T]` option that sorts that module's list results — both the MCP `list_*` tool and
the HTTP `GET {prefix}` route — by `field`, ascending unless `desc` is `true`. `field` must
name an exported string or integer field on `T`; any other kind sorts as equal for every
item (a no-op, not an error). A module with no `DefaultListOrder` keeps the repository's own
natural order, exactly as before this option existed.

```go
smeldr.NewModule((*Task)(nil),
    smeldr.At("/tasks"),
    smeldr.Repo(repo),
    smeldr.DefaultListOrder("Priority", false), // lower Priority first
)
```

### `GoalContext` and `QueryGoalContext`

```go
type GoalContext struct {
    Goal            *Goal
    LinkedDecisions []Decision
    LinkedTasks     []Task
    LinkedGoals     []Goal
}

func QueryGoalContext(ctx context.Context, db DB, rs *RelationStore, goalID string) (*GoalContext, error)
```

`QueryGoalContext` assembles a full context packet for the goal whose `GoalID` field matches `goalID` (e.g. `"T114"`). It queries both the source and target sides of the relation graph and collects linked Decisions, Tasks, and Goals. Deduplicates by edge ID. Skips Goal→Goal self-links. Warns and continues on missing linked items (fail-open per item).

Returns `ErrNotFound` when no matching goal exists. When `rs` is nil, returns the goal with empty slices (fail-open).

The `get_goal_context` MCP tool in `smeldr.dev/mcp` exposes this function as an agent-callable operation (see smeldr.dev/mcp documentation).

### `LifecycleEvent` (renamed from `Signal`)

```go
type LifecycleEvent string
```

Identifies a content lifecycle event. Previously named `Signal`; renamed in A183 to free
the name `Signal` for orchestration use. All constants retain their names:

```go
const (
    BeforeCreate   LifecycleEvent = "before_create"
    AfterCreate    LifecycleEvent = "after_create"
    BeforeUpdate   LifecycleEvent = "before_update"
    AfterUpdate    LifecycleEvent = "after_update"
    BeforeDelete   LifecycleEvent = "before_delete"
    AfterDelete    LifecycleEvent = "after_delete"
    AfterPublish   LifecycleEvent = "after_publish"
    AfterUnpublish LifecycleEvent = "after_unpublish"
    AfterArchive   LifecycleEvent = "after_archive"
    AfterSchedule  LifecycleEvent = "after_schedule"
    SitemapRegenerate     LifecycleEvent = "sitemap_regenerate"
    AfterRelationCascade  LifecycleEvent = "relation.cascade"
)
```

**Which writes run the Before hooks (v1.126.0).** A `BeforeCreate`, `BeforeUpdate` or
`BeforeDelete` hook registered with `On` runs on every content write, whichever surface
it comes through. If it returns an error, the write is aborted before anything is saved
or deleted. A hook that changes the item has that change saved.

| Write | HTTP | MCP | Hook |
|---|---|---|---|
| create | `POST` | `create_*` | `BeforeCreate` |
| content update | `PUT`, `PATCH` | `update_*` | `BeforeUpdate` |
| delete | `DELETE` | `delete_*` | `BeforeDelete` |

Status-only transitions do not run `BeforeUpdate`: the MCP `publish_*`, `schedule_*` and
`archive_*` tools, `transition_item`, and the scheduler. The type's state flow gates them
on every surface instead. A `PUT` that also changes the status is a content write and does
run `BeforeUpdate`. Before v1.126.0, `BeforeCreate`, `BeforeUpdate` and `BeforeDelete` ran
on HTTP only (not on MCP create, update or delete, and not on `PATCH`).

**Which transitions fire the After handlers (v1.127.0).** A module's own `On` After
handlers see the same events for a status change, whichever path makes it: `AfterUpdate`,
then the status event the change calls for.

| Change | Module `On` handlers get |
|---|---|
| into `published` | `AfterUpdate`, `AfterPublish` |
| out of `published` | `AfterUpdate`, `AfterUnpublish` (and `AfterArchive` / `AfterSchedule` when it goes there) |
| into `archived` | `AfterUpdate`, `AfterArchive` |
| into `scheduled` | `AfterUpdate`, `AfterSchedule` |
| any other state change (a custom flow) | `AfterUpdate` |

The paths are HTTP `PUT`, the MCP `publish_*`, `schedule_*` and `archive_*` tools,
`transition_item` and the scheduler. On `transition_item` the handlers get the caller's own
`Context`; a caller with no `Context` (a system path) gets a background context whose user
is the guest. If the item cannot be loaded after the change has committed, the handlers do
not run and the transition still succeeds (the failure is logged). Before v1.127.0,
`transition_item` fired none of a compiled type's module After handlers, and the MCP
lifecycle tools and the scheduler did not fire the module's `AfterUpdate`.

**What the App side gets for a status change (v1.137.0; D107).** Every
status change, on every path (the ones above, plus a runtime-defined type's status change,
an item a conflict supersedes and `DrainEvalQueue`), gives the App-level consumers the same:

| Consumer | Gets |
|---|---|
| provenance (`App.Provenance`) | one record (verb `transition`) and one standing write |
| event stream and webhooks | one `"{type}.transitioned"`, plus `"{type}.published"`, `.unpublished`, `.archived`, `.scheduled` once each as the change calls for |
| `App.OnSignal` (Audit, the relations cascade, agent jobs, social routes, webhooks) | the status events above once each; `AfterUpdate` only when a `PUT` also changed content |
| `App.AddSignalListener` (mcp resource subscriptions) | the status events, and one `AfterTransition` |

A `PUT` counts as a content edit when something other than `Status`, `PublishedAt`,
`ScheduledAt`, `UpdatedAt`, `CreatedAt`, `Rev` or the last writer changed; it then also
gives `AfterUpdate`/`"{type}.updated"` and an `update` record. A target state with
`SuppressesSignals` gets no status events, and `DrainEvalQueue` none either (T211/D51: no
human-publish subscriber is activated by background automation); both still get the record,
`"{type}.transitioned"` and `AfterTransition`. Before this version a status-changing `PUT`
wrote two or three records and sent `"{type}.updated"` with every status change,
`transition_item` reached no `App.OnSignal` subscriber, and a runtime-defined type's status
change sent no event at all. Bus handlers, module handlers and listeners run after the
transition released its conflict lock, and their context no longer says it is held.

### `RegisterOrchestrationRelationKinds` (A296)

```go
func RegisterOrchestrationRelationKinds(ctx context.Context, store *RelationStore) error
```

Registers the relation kinds that connect the orchestration types. Idempotent —
safe to call on every boot; `UpsertKind` updates in place if a kind is already registered.

| Kind | Direction | Type pairs | Mode | Label / reverse label |
|------|-----------|------------|------|-----------------------|
| `derives_from` | directional | Task→Goal | asserted | Derives From / Source Of |
| `depends_on` | directional | Task→Task | asserted | Depends On / (none) |
| `ships_as` | directional | Task→Amendment | asserted | Ships As / Shipped By |
| `amends` | directional | Amendment→Decision | asserted | Amends / Amended By |
| `supersedes` | directional | Decision→Decision | asserted | Supersedes / Superseded By |
| `contains` | directional | Goal→{Goal,Task,Decision,Amendment,Signal} | asserted | Contains / Part Of |
| `contradicts` | symmetric (`Directional: false`) | Decision↔Decision | asserted | Contradicts / (none) |
| `investigates` | directional | Task→Decision | asserted | Investigates / Investigated By |
| `addresses` | directional | Decision→Decision | asserted | Addresses / Addressed By |
| `grounded_in` | directional | Decision→Decision | asserted | Grounded In / Grounds |
| `belongs_to_organization` | directional | any→Organization | asserted | Belongs To Organization / Organization Of |
| `belongs_to_domain` | directional | any→Domain | asserted | Belongs To Domain / Domain Of |
| `belongs_to_area` | directional | any→Area | asserted | Belongs To Area / Area Of |
| `in_set` | directional | (open — no `TypePairs`) | asserted | In Set / (none) |
| `implements` | directional | Task→Decision | asserted | Implements / Implemented By |
| `about` | directional | (open, no `TypePairs`; the source is a Signal) | asserted | About / Subject Of |
| `refers_to` | directional | (open, no `TypePairs`) | asserted | Refers To / Referred To By |

`contradicts` is the vehicle for a Decision-to-Decision Asserted-provenance condition — it
has no `ReverseLabel`, since "contradicts" reads the same statement from either endpoint,
unlike `contains`/"Part Of". `investigates` models a Task delegated to look into a Decision.
`addresses` (A301) models one Decision responding to another — e.g. a decline-Decision
asserting back to the original it declines; `TypePairs` is scoped to Decision→Decision only
for this one real need, not left open for a hypothetical non-Decision use case. `grounded_in`
(A308) models a Decision's own evidentiary basis — the Re-ground intention
(`decision-supersession-model.md` §2 item 4): the Decision stays `ratified`, only the graph
showing what grounds it changes. `TypePairs` is scoped to Decision→Decision only, same
narrow-by-default precedent as `addresses`. Who asserts either edge, and when, is left to
the same `assert_relation` convention already used for every other kind here — no new core
machinery.

The reverse label names the edge from the target's end (a Goal is "Source Of" its Tasks, an
Amendment is "Shipped By" its Task). Registering on boot upserts the labels, so an instance
that registered a kind before its reverse label existed gets it on its next boot (T870).

`implements`, `about` and `refers_to` (D113, A521) are asserted by the system from reference
fields, never by hand: a Task's `implements` naming a Decision, a Signal's subject, `task_ref`
and `answer_ref`, and a runtime-defined reference field. See "References between records" below.

### Naming a module's type: `TypeName` (T895)

A module's content type name is its Go struct's name unless `smeldr.TypeName(name)` says otherwise. The type registry, the MCP tools (`create_<snake>`, `list_<snake>s`, ...), state flows, addresses, relations, events and the default URL prefix (`/<lower>s`) use that name; an `At` option still sets the prefix. The name must be a PascalCase identifier, or `NewModule` panics at startup. Use it when the struct cannot carry the name, as core's own `OrganizationRecord` does:

```go
app.Content(smeldr.NewModule((*smeldr.OrganizationRecord)(nil),
    smeldr.TypeName("Organization"), // tools list_organizations, REST /organizations
    smeldr.Repo(repo),
))
```

### Places: Organization, Domain, Area and Set (D142, T882)

Organization, Domain, Area and Set are compiled core record types, registered by `RegisterGovernanceTypes` (tables from `CreateGovernanceTables`), each with `name` and `description` and its own MCP tools (`list_organizations`, `list_domains`, `get_domain`, `create_domain`, ...).

The Go struct of the Organization is `OrganizationRecord`, registered with `TypeName("Organization")`: `smeldr.Organization` is the schema.org constant this package already exports, and removing it would break callers (a D141 exception, T895). Every name a user, an agent or a tool sees is Organization.

| Type | Address | Table | Flow |
|---|---|---|---|
| `Organization` (Go struct `OrganizationRecord`) | `ORG1` | `smeldr_organizations` (one row: a unique index on a constant) | `active` |
| `Domain` | `DOM<n>` | `smeldr_domains` | `active`, `archived` |
| `Area` | `ARE<n>` | `smeldr_areas` | `active`, `archived` |
| `Set` | `SET<n>` | `smeldr_sets` | `active`, `archived` |

- **Organization is the organization**, one per instance. `App.EnsureOrganization(ctx, name)` creates it with a default Domain (`General`, slug `general`) and the edge `Domain belongs_to_organization Organization`, with provenance (job `setup`), and returns the existing Organization on every later call. An empty name is a validation error; an instance that still keeps the runtime `domain` type answers `ErrConflict` (run `MigratePlaces`, which creates Organization). example/server calls it at boot with `ORGANIZATION_NAME` (default `INSTANCE_NAME`).
- **Places.** `belongs_to_organization`, `belongs_to_domain` and `belongs_to_area` accept any record type as the source (`"*"` in `TypePairs`). A Domain belongs to Organization, an Area to its Domain (D72), and a Decision that spans Domains relates to Organization explicitly (D83). A non-Decision record without a place belongs to the whole organization: that is not an incomplete state. A Set (`in_set`) carries no authority (D71).
- **Cardinality** is not enforced for non-Decision records. For a Decision, exactly one place (Organization, one Domain, or one Area, where an Area implies its Domain) is the rule T883 enforces: a second place edge on a Decision is refused, and `proposed -> ratified` is refused without one (D142 point 4).
- **Who may place or move (D145, T906).** A Decision that is no longer `proposed` (ratified, pending re-evaluation, superseded, archived) is placed or moved only by someone who holds `ratify` over the place: asserting a place edge needs it over the new place, withdrawing one over the place it leaves, so a move (withdraw, then assert) needs both. An Area's `belongs_to_domain` is gated the same way once the Area has had a Domain: withdrawing needs `ratify` over that Domain, and asserting another needs it over the new one; an Area's first Domain stays at the Author tier. A proposed Decision is placed and moved at the Author tier (the inbox). "Ratify over a place": an `approver` covers every place; a `domain-approver` covers its Domain itself and the Areas in it, not the Organization or another Domain. Agent and job tokens are refused. The check sits in `RelationStore` (assert, propose and observe write asserted edges through `insertEdge`; `Withdraw`), so `assert_relation`, `withdraw_relation`, the CLI and the Go API all apply it; an inferred or observed edge is no place and is not gated. System writes (`MigratePlaces`, `EnsureOrganization`, reference fields such as a CoverageRule's `area`) go through their own path and are not gated. Refusals are `ErrForbidden` naming the act and the place ("moving D110 out of DOM4 needs ratify over DOM4 (D145)"); a database without governance skips the check.
- **Reading** follows the place hierarchy; see "Reader roles" above.

**`MigratePlaces(ctx, PlaceMigrationOptions{DryRun, OrganizationName})`** moves an instance from the runtime `domain`, `area` and `set` types it defined before D142, once (`smeldr-server migrate-places`, dry run unless `--apply`, `--report=<file>`):
1. Every runtime record is copied to the compiled table with the same ID, slug, timestamps, address and number; `name` and `description` come from its fields; an archived record stays archived, any other becomes `active`.
2. Relation ends, provenance subjects, event-log subjects (`subject_type`, `other_type`), static grant patterns (`domain:<id>` becomes `Domain:<id>`) and the address counter change from the runtime name to the compiled one. IDs do not change, so dynamic grants anchored on a place keep working, and the next Domain continues the numbering.
3. The runtime schemas are set to `kind = 'retired'`: kept, no longer loaded. The runtime rows stay in `smeldr_dynamic_content`.
4. Organization and the default Domain are created (`EnsureOrganization` with `OrganizationName`), and every Domain without one gets `belongs_to_organization`.

Steps 1 to 3 run in one transaction; a dry run runs them in a transaction it rolls back, so its report is exact. A second run moves nothing. A runtime record whose address a compiled row already holds is a validation error. Until the migration has run, boot leaves the place prefixes with the runtime types (a warning names `migrate-places`) and `EnsureOrganization` waits.

**Rollback.** First choice: restore the database backup taken before the run. By hand, in one transaction: delete the copied rows from the compiled tables (their IDs are in the report), rewrite the type names back (`Domain` to `domain` in `smeldr_relations`, `smeldr_provenance`, `smeldr_events`, the grant patterns and the counter), set the schemas back to `kind = 'content'`, and delete Organization, the default Domain and the `belongs_to_organization` edges. Then run the previous binary.

**What a client sees change** (an MCP client that read the runtime types): `list_content`, `get_content` and `create_content` with `type_name: "domain"` stop working (the type is not loaded); use `list_domains`, `get_domain`, `create_domain`, `update_domain` (and the same for areas and sets). Items have the compiled shape (`ID`, `Slug`, `Status`, `address`, `name`, `description`). Relation ends and `get_relations` use `Domain`, `Area`, `Set`. Event names stay `domain.*`; addresses (`DOM1`) do not change.

### The pull request check (D142 point 8, D143, D144, T918)

A proposed change to a repository (a pull request on GitHub) is checked against the Decisions in force for the paths it touches, through the Areas that cover them. Core is host-neutral and never calls the host: an adapter turns the host's events into core calls and posts the verdict.

**`ChangeCheck`** (address `CHK<n>`, table `smeldr_change_checks`, MCP `get_change_check`, `list_change_checks`) is one checked commit of a change. Fields: `repository` (owner/name, lower case), `number`, `head_commit` (full 40-character SHA), `files_total`, `files_checked` (at most 3,000), `checked_partly` (the change touches more files than were checked), `setting` (the strictest approval mode among its Areas). No title or body: its fields hold only what every reader of it may see, never an Area's or a Decision's name. It is placed (`belongs_to_area`) in every Area it touches and related (`checked_against`) to every Decision in force for them, so each reader sees the parts its read rules allow. It is made only by `CheckChange` and moved only by core: a create, update, or delete through any surface is `ErrForbidden`.

States (flow `change-check`): `checking`, then `commented` (no touched Area requires approval) or `awaiting-approval`; `approved` or `not-approved` (the per-Area answers, next part); `superseded` when a newer commit of the change is checked (with a `supersedes` edge from the newer one). Only the newest checked commit is live (D143); history is read by filter (repository and number, Area, Decision), never as one list. **Retention:** no Check is deleted; how long Checks are kept is decided later from measured volume (D143).

**`App.CheckChange(ctx, ChangeInput{Repository, Number, HeadCommit, Paths, FilesTotal})`** resolves the paths with `DecisionsForPaths` under a system read scope (a Check is the same for every caller). No covered Area: no Check, `covered: false` (D143). Otherwise it makes the Check in one transaction (its Areas with their approval modes, its places and `checked_against` edges) and sets the verdict. On every call, the newest Check of the change (by address number) is left as its only live one: every older Check not yet superseded moves to superseded, with a `supersedes` edge from the newest, so a supersede that failed before is repaired by a redelivery, and two commits checked at once leave one live Check. The same repository, number and commit again returns the existing Check, also when two arrive at once (a unique index on the three). The caller needs the capability `item check_change` (operation `manage`: the adapter's token or an operator) and gets the full result: the Check, its Areas with their paths, the applied Decisions and the uncovered paths. Refusals: a repository not owner/name, a number below 1, a head commit not 40 hex, `files_total` below the paths given (validation errors); more than 3,000 paths (`ErrBadRequest`).

**Approval per Area (D144).** Each touched Area in `requires-approval` gets its own `review-requested` request: receiver `approve-change`, subject the Check, placed in that Area only, its message naming the Check and that one Area. **`App.AnswerChangeCheck(ctx, check, area, approve, reason)`** records the answer for one Area (a reason is required when not approved): the answerer must hold `approve-change` over that Area (its `area-approver`, its Domain's `domain-approver`, an `approver`: the away case) or the one-item role on that Check; an agent or job token counts only through an `area-approver` grant on that Area or the one-item role. It answers the Area's request (`approve` or `not approved: <reason>`, answer_ref the Check) and moves the Check: `not-approved` on the first not approved, `approved` when every Area that requires approval approved. The Area must be one the caller may read, and the authority is checked before anything about the Area's mode or verdict is said. Refusals: Check or Area not found, or an Area the caller may not read (`ErrNotFound`); an Area the Check does not touch, one that only comments, one already answered, or a Check not awaiting approval (`ErrConflict`); no authority (`ErrForbidden`). The generic `answer_signal` and `withdraw_signal` on such a request are refused (`ErrConflict`), so every answer counts for its Area. A request is written with its place in one transaction and announced after the commit. Superseding a Check withdraws its open requests (repaired on the next call if that failed); an answer is for one commit (carry-over is T907).

**`item-change-approver`** (`RoleItemChangeApprover`, `RegisterItemChangeApproverRole`): a static role with `approve-change` (and read) on the one ChangeCheck its grant names, counting for every Area of that Check. Only a human who holds `approve-change` over every Area of that Check that requires approval grants it (with `delegate_item`), to an agent too: an approver of one Area cannot hand out another's. A grant from an agent, from a human without that authority, for a Check with no Area that requires approval, naming anything but exactly one Check, or with no known grantor is `ErrForbidden`. Used on another Check it authorizes nothing.

**Audit after merge (D143).** An Area in `audit-after-merge` (`ApprovalModeAuditAfterMerge`) never blocks a change: at check time it only comments, and the Check's `setting` ranks it between `comment-only` and `requires-approval`. **`App.RecordMerge(ctx, MergeInput{Repository, Number, HeadCommit, MergeCommit})`** records that the change reached the default branch (capability `item check_change`, the same validation as `CheckChange` plus a 40-hex merge commit). For the change's live Check, each touched audit Area without an approval opens a `merge-unapproved` condition: receiver `approve-change`, subject the Check, placed in that Area, its message naming that Area alone. An Area already approved (an answer before the merge) or with its condition open opens nothing, so a second report opens nothing more. A change with no live Check (nothing it touched was covered) is a no-op. When the live Check checked another head commit than the one merged, the conditions still open and their message names both commits (`head_mismatch` in the result): the merged code was not the checked code. The approver resolves a condition with `answer_change_check` on that Area: approved after the fact, or not approved with a reason; the resolution text says which and who. The Check's state is not changed by an audit answer. An Area already answered, or a superseded Check, is `ErrConflict`.

**`App.ListChangeChecks(ctx, ChangeCheckQuery{Repository, Number, Area, Decision, Status, Limit})`** lists Checks newest first, by filter only (D143): a query without a repository, an Area or a Decision is `ErrBadRequest`; an Area or Decision filter matches the Checks placed in it or checked against it (by address, slug or ID) and must name one the caller may read: an unknown or unreadable one matches no Checks, so a filter reveals nothing about a place the caller cannot see. Only Checks the caller may read are returned, and the limit (50 by default, at most 500) counts those. A supersede is announced as `changecheck.transitioned`, like every other Check transition.

**Tools and capabilities.** MCP `check_change` and `record_merge` take the capability `item check_change` (operation `manage`); `answer_change_check` takes `item answer_change_check` (read: core checks the Area's read and approve-change inside); `get_change_check` and `list_change_checks` take `item change_checks` (read, filtered per Check). CLI `smeldr-cli check change|merge|answer|show|list`.

**`App.ChangeCheckOf(ctx, ident)`** returns a Check with the Areas and Decisions the caller may read: a reader of one Area sees that Area's entry and the Decisions it may read. A Check the caller may not read is `ErrNotFound`. `EnsureChangeCheckTables` creates the tables on an older database (example/server calls it at boot).

**Self-hosted, with no Go (T940).** The GitHub adapter is the module `smeldr.dev/github`. The example server mounts it when `ENABLE_GITHUB` is set, on `POST /github/webhook` (the GitHub App's webhook URL) and `POST /github/core-events`, configured by `GITHUB_APP_ID`, `GITHUB_APP_PRIVATE_KEY` (or `GITHUB_APP_PRIVATE_KEY_FILE`), `GITHUB_WEBHOOK_SECRET` and `GITHUB_ADAPTER_TOKEN_ID`; optional `GITHUB_NAMES_SHOWN`, `GITHUB_CHECK_URL`, `GITHUB_ITEM_URL`. The adapter acts in process as the token `GITHUB_ADAPTER_TOKEN_ID` names (the `token_id` `create_token` returns): it must be live, of class `job` and hold `check_change` (grant `editor`), and its plaintext can be discarded. The server registers its own core webhook for `changecheck.transitioned` at `BASE_URL/github/core-events` on the first boot and reuses it after (BASE_URL must be a public https URL); `GITHUB_CORE_WEBHOOK_SECRET` set instead means you registered it yourself. It needs `ENABLE_TOKENS`, `ENABLE_GOVERNANCE`, `ENABLE_RELATIONS`, `ENABLE_WEBHOOKS` and governance records. Every wrong or missing value refuses to start with an error naming the variable, never its value, and a refused start writes nothing (the core webhook is created only after every check has passed); no secret is logged. GitHub enforces the block: make `smeldr/check` a required status check in branch protection.

### Pinned sources on a Decision (D148, T914)

A Decision that rests on an external source (a readme, a token file, a specification, code, an ADR) carries the claim in its own text and names the source by a pin. **`Decision.Sources`** (`sources`, type `DecisionSources`, a list of `DecisionSource`, stored as a JSON array in `smeldr_decisions.sources`; `[]`, never null) holds at most `DecisionSourcesMax` (20). Each source:

| Field | Rule |
|---|---|
| `repository` | `owner/name`, stored in lower case (the coverage rules' check) |
| `commit` | a full 40-character SHA, stored in lower case; a branch, tag or short SHA is refused |
| `path` | one file, relative to the repository root: no leading `/`, no backslash, no empty or `..` segment, no `*` or `?`, at most 512 characters |
| `anchor` | optional: a heading or line range, one line, at most 200 characters |
| `role` | `source`, `evidence` or `implementation` (`DecisionSourceRoleSource`, `...Evidence`, `...Implementation`) |
| `hash` | optional: `sha256:` and 64 hex, the SHA-256 of the file at the commit, stored in lower case |

The same repository, commit, path and anchor twice is refused. Refusals are validation errors on `sources[i].field`. The check runs in the Decision's save hook, so `create_decision`, `update_decision`, HTTP and the Go API all apply it. Core stores the pins and never fetches them (it has no repository access): the reader fetches the file at the pinned commit and may compare the hash.

**Set them while the Decision is proposed.** A Decision that is ratified, pending re-evaluation, superseded or archived is locked: its content, sources included, is not edited. To re-pin a ratified Decision to a new commit, supersede it with a new Decision: what was ratified is the claim together with its pins. As a further check, a write that does reach a non-proposed Decision's sources (a full PUT that also changes its state) needs `ratify` over the Decision's place, through the same check as D145 and D146 (agent and job tokens refused, no place refused, no governance skipped). `EnsureDecisionSourcesColumn` adds the column to an older database (example/server calls it at boot). The MCP schema advertises `sources` as an array of objects (`MCPField.Items`).

### Coverage rules: which Decisions apply to these paths (D142, T886)

An Area covers repository paths through its coverage rules, and one resolution answers which Decisions apply to a list of paths: the pull request check and the MCP tool share it. Core never reads a repository: **the caller supplies the paths.**

**`CoverageRule`** is a governance record type (address `COV<n>`, table `smeldr_coverage_rules`, flow `active`/`archived`, MCP tools `create_coverage_rule`, `list_coverage_rules`, `get_coverage_rule`, `update_coverage_rule`, `archive_coverage_rule`, ...). Fields: `area` (an Area by address, slug or ID, stored as its address; write-once, so moving a rule is archive plus create; the system relates the rule to the Area by `belongs_to_area`, so it is read under the Area's read rules), `kind` (`path`, the only kind so far), `repository` (`owner/name`, stored in lower case), `pattern`, and the read-only `match_count`, `checked_at`. An update that changes the pattern, repository or kind clears both (the rule is not checked until the next `CheckCoverage`, T901); a repository differing only in case is no change. A pattern: `*` any run within one segment, `?` one character, `**` any number of segments (`internal/billing/**`, `cmd/*/main.go`, `**/*_test.go`); without wildcards it covers one file. Refused: empty, a leading `/`, `..` or empty segments, a backslash, `**` inside a segment, more than 512 characters. Only Areas have rules; one Area may cover paths in several repositories. No line rules (D142).

**`Area.approval_mode`**: `comment-only` (default), `requires-approval` or `audit-after-merge`, what a change touching the Area needs. Stored for the pull request check, which reads it.

**Who may change an Area's gate (D146, T910).** The approval mode, an Area being active, and its coverage rules are the Area's approval gate, so loosening or moving it needs `ratify` over the Area (an `approver`, or the `domain-approver` of the Area's Domain): changing `approval_mode` (either way; unchanged fields pass at the Author tier), archiving or reactivating the Area, deleting it, and for a coverage rule: changing its pattern, repository or kind, archiving or reactivating it, deleting it. Adding a rule needs `approve-change` over its Area (an `area-approver` on the Area, an agent one included, its `domain-approver`, an `approver`). Agent and job tokens never hold `ratify`, so they are refused every other act here. Refusals are `ErrForbidden` naming the act and the Area ("changing ARE1's approval mode needs ratify over ARE1 (D146)"); a database without governance skips the check, and a system transition is not gated.

**`App.ApproversOf(ctx, place)` (D147, T911)** returns who holds approval authority over a place (an Organization, Domain or Area by address, slug or ID), as `[]PlaceApprover` with `name` (the holder's token name), `role` (`approver`, `domain-approver` or `area-approver`), `class` (`human`, `agent`, `job` or `unclassified`) and `anchor_address` (where the grant sits: the Organization for an approver, the Domain, the Area). Listed: every `approver`; the `domain-approver` holders anchored on the place's Domain (the Domain itself, or an Area's Domain); for an Area, the `area-approver` holders anchored on it. Sorted by role, then name. Nothing else about the holder or the grant: no grant IDs, end dates, grantors or token details. It answers only when the caller may read the place; an unknown or unreadable place is `ErrNotFound` alike ("no place X you can read"), so nothing is revealed; `anchor_address` is empty when the caller may not read the anchor place (a domain-reader sees approvers without the Organization's address). It does not need `administer`. Left out: expired grants (as `Authorized`; a revoked grant no longer exists) and holders with no live token, who cannot answer; each holder is named by its newest live token. Only the three role names, not a custom role that holds ratify or approve-change. Without governance the list is empty. Capability `item approvers_of` (read, reads any). MCP `approvers_of`, CLI `smeldr-cli approvers <place>`.

**`App.Administrators(ctx)` (D154, T944)** returns who administers the instance, as `[]InstanceAdministrator` with `name` (the holder's token name) and `class` (`human`, `agent`, `job` or `unclassified`), sorted by name. Nothing else: no grant IDs, end dates, grantors, reasons, roles or token details. A holder is anyone with an unexpired grant whose role holds `administer` at **global** scope, which is what `Authorized` answers `administer` by (the built-in `admin` role, or a custom global role with `administer`). Left out: a static or dynamic grant with `administer` (it does not administer the instance), an expired grant, a revoked one (it no longer exists), a holder with no live token (it cannot be asked), and a token whose legacy role is `admin` but holds no such grant (under governance the legacy role is not asked). Any authenticated caller may call it, **also one that reads nothing yet**: a member with no grant is the one who most needs to know whom to ask. A caller with no user ID is `ErrForbidden`. Without governance the list is empty. Capability `item administrators` (read, authenticated: in `authenticatedReads`, which every authenticated caller passes and sees). MCP `administrators`, CLI `smeldr-cli administrators`.

**`App.RestCounts(ctx, typeName, id)` (D150, T919)** counts, live, what rests on the item and what it rests on, as `RestCounts{RestsOnThis, ThisRestsOn}` (JSON `rests_on_this`, `this_rests_on`), each a `RestCount{Open, Closed, AtLeast}`. *This rests on* follows live edges of `RestRelationKinds` (`grounded_in`, `addresses`, `amends`) from source to target, transitively; *rests on this* follows them backwards. Every edge class counts; an ended edge does not. Each item is counted once, the item itself never, and cycles end. Open and closed are D109's: an item is open when its state awaits action (`StatesAwaitingAction`), closed otherwise; an item of a type with no flow is closed, and an edge end with no row is not counted. The caller's read rules apply as in `Reachability`: an item the caller may not read is neither counted nor walked through, so an item reached only through a hidden one is not counted. An unknown or unreadable anchor is `ErrNotFound` alike; a failed read is `ErrInternal`. Limits: each direction walks at most `RestCountLimit` (1,000) items, nearest first (a stable choice by type and ID inside the last ring), and sets `at_least` when it stopped there; there is no depth limit below that. Cost: one edge query and one state query per type for each ring (per 400 items). Capability `item rest_counts` (read, reads any; the read rules apply per item inside). MCP `get_rest_counts`, CLI `smeldr-cli rests <type> <item>`.

**`App.SweepRestCounts(ctx)` and `App.StoredRestCounts(ctx, items)` (D150, T919)** are the stored rest counts, for lists. The sweep walks every item that has a live rest edge, both ways, in memory (one read of the rest edges, the place edges and the states), with the same `RestCountLimit` per walk, and stores each count per *bucket* in `smeldr_rest_counts`, replacing the previous run in one transaction. A bucket is a place: the Organization, a Domain or an Area. An item reached by a path is stored in the bucket of the lowest place that every item on the path after the walked one (the reached one included) lies in, and in the deepest such bucket any path gave it; an unplaced item, an item in the Organization, and a path through two Domains give the Organization; an item placed in several places counts at their lowest common place. Because a Domain's readers read its Areas, every reader of a bucket reads the whole path, so no hidden item, and no item reached only through a hidden one, is counted for them. Signals are neither counted nor walked through. Each run is recorded in `smeldr_rest_count_runs` (`RestCountRun`: id, start, end, items walked, rows stored, capped walks); a failed read or write is `ErrInternal` and keeps the previous counts. `EnsureRestCountTable(ctx, db)` creates both tables; the example server runs the sweep after `SweepStructural` on the structural sweep's schedule and appends a `SweepRunRecord` with detector `rest-counts`. `StoredRestCounts` takes at most `MaxStoredRestCountItems` (200) `ItemRef`s (more is `ErrBadRequest`) and returns, in the order asked, one `StoredRestCounts` (the `ItemRef`, the `RestCounts`, `counted_at`) for each item the caller may read; others, duplicates and Signals are left out. Each count is the sum over the buckets the caller covers: a scope that reads everything covers all; a read grant on a Domain (`belongs_to_domain`) covers that Domain's bucket and those of its Areas; one on an Area covers that Area's. No other way of reading covers a bucket (an item or static grant, a grant on the Organization), so a stored count may be lower than the live one, never higher (D150 point 4). `at_least` is set when a summed bucket came from a capped walk. `counted_at` is the end of the last run; before any run the counts are zero and `counted_at` is absent. Measured on Postgres: 100,000 items with 200,000 rest edges in 34 s (20 s to read and walk), linear in the items. Capability `item rest_counts` (read, reads any). MCP `list_rest_counts`, CLI `smeldr-cli rests --stored <type>:<item>...`.

**`premise-flagged`, `App.ConfirmPremise(ctx, decision, premise, reason)` and `App.SweepPremiseFlags(ctx)` (D149, D153, T919)** flag a Decision in force that rests on a Decision that is not. The rule, per pair: a Decision D whose standing holds (ratified, or pending re-evaluation) with a live `grounded_in` edge to a Decision P whose standing does not hold (proposed, superseded, archived) opens one `premise-flagged` condition: sender `system`, receiver and required operation `ratify`, subject D, placed where D is placed (its place edge written in the same transaction as the Signal, both announced after the commit). Its message is `D12 rests on a Decision that is not in force`: it names D only, never P, so a reader of the Signal who may not read P does not learn it; P is found through the relations the reader may read. One step only (D rests directly on P). Only `grounded_in` is a premise (D153): `addresses` says D is about P (declines it, follows it up, clarifies it), not that D rests on it, and `amends` is not covered either. A condition opened from an `addresses` edge before D153 resolves at its next evaluation with the reason `addresses is not a premise relation (D153)`. The pair is evaluated when either Decision's state changes, when such an edge is asserted or ends, and by `SweepPremiseFlags` (the structural sweep's schedule, as a backstop over every pair: `PremiseSweep{Pairs, Opened, Resolved, Failed}`). It is resolved when the rule stops holding, with a reason that says which: the premise is in force again, the relation ended, D is no longer in force, or a Decision no longer exists. It never blocks anything. `ConfirmPremise` records that D still holds anyway: it needs `ratify` over D's place (so an agent or job token never confirms), a reason (else a validation error), and an open condition for the pair (else `ErrConflict`); both Decisions are named by address, slug or ID and must be readable (else `ErrNotFound`). It resolves the condition with `confirmed by <actor>: <reason>` (in provenance) and stores P's state; the pair stays closed while P's state is the one it was confirmed at, and opens again when P's state changes and the rule still holds. The pairs are kept in `smeldr_premise_flags` (created by `CreateGovernanceTables`, or `EnsurePremiseFlagTable` on an older database). Capability `item confirm_premise` (read, reads any: `ConfirmPremise` itself checks the read and ratify over the place). MCP `confirm_premise`, CLI `smeldr-cli premise confirm <decision> <premise> --reason "why"`.

**`App.ConfirmPremiseCondition(ctx, condition, reason)` and moving conditions (T925)**: the same confirmation by the condition instead of the premise, for a ratify holder who may not read the premise. `condition` names the open `premise-flagged` Signal by address, slug or ID; it must be readable (a Signal is, with its Decision, D113), else `ErrNotFound`, as is a Signal that is not such a condition; a resolved one is `ErrConflict`; ratify over the resting Decision's place and a reason are required, as for `ConfirmPremise`. Nothing it returns or records names the premise. An open condition follows its Decision's place: when a place edge of the Decision is asserted or ends (a move is an end and an assert, D145), the condition's place edges that are not the Decision's current place end (cause `moved`) and the current one is written, in one transaction per condition; an unplaced Decision leaves its conditions unplaced. `SweepPremiseFlags` does the same for every open condition, as the backstop.

**`App.DecisionsForPaths(ctx, DecisionsForPathsQuery{Repository, Paths, ExcludeOrganization})`** returns the covered Areas (address, name, Domain, `approval_mode`, the paths and rules that put each there), the Decisions in force (standing `holds`: ratified or pending re-evaluation) placed in those Areas, in their Domains and, unless excluded, on the Organization (`place` is `area`, `domain` or `organization`, with `place_address` and the covered Areas it applies through), and the `uncovered` paths. Under the caller's read rules: an Area, rule or Decision the caller may not read is left out, and a path only a hidden Area covers reads as uncovered. At most 3,000 paths per call (GitHub's cap on a pull request's file list); more is `ErrBadRequest`, and the results of calls over parts of a longer list add up. Cost: one query for the repository's rules, compiled once (literal patterns by map lookup, `dir/**` by prefix, the rest segment by segment without regexp), one query each for the Domains, the Decisions and their standing. `BenchmarkDecisionsForPaths` (3,000 paths, 300 rules, SQLite): about 40 ms.

**`App.CheckCoverage(ctx, CheckCoverageQuery{Repository, Paths})`** takes the repository's full file list (at most 100,000 paths, where GitHub's tree API truncates) and, for every active rule of the repository, stores `match_count` and `checked_at`, opens `coverage-matches-nothing` for a rule that matches nothing (one per rule) and resolves it for a rule that matches again. Updating or archiving a rule resolves its condition too. The condition is addressed to the operation `approve-change`; routing by operation is not scoped to the Area yet, so every holder of `approve-change` sees it (a known limit).

MCP: `decisions_for_paths` (Author, capability `item decisions_for_paths`, filtered by the read rules) and `check_coverage` (Editor, capability `item check_coverage`, operation `manage`). CLI: `smeldr-cli decisions-for-paths --repo owner/name [--stdin] [--no-organization]` and `smeldr-cli coverage check --repo owner/name --stdin`.

### References between records (D113, A521)

A reference field names another record. Core checks the name when the record is written and writes the relation edge for it in the same transaction, so the trace is complete by construction.

| Record | Field | Names | Edge |
|---|---|---|---|
| Task | `implements` | a Decision or a Goal | `implements` to a Decision, `derives_from` to a Goal |
| Amendment | `task_ref` | a Task | `ships_as` from the Task to the Amendment |
| Signal | `subject_type` + `subject_id` | a record of that type | `about`, attributes `{"field":"subject_id"}` |
| Signal | `task_ref` | a Task | `about`, attributes `{"field":"task_ref"}` |
| Signal | `answer_ref` (`App.AnswerSignal`) | a record, as `Type:name` | `about`, attributes `{"field":"answer_ref"}` |
| runtime-defined type | a string field with `references` | the type it names | `refers_to`, attributes `{"field":"<name>"}` |

- **Resolution.** Before the write's transaction, each reference is resolved by address, slug, ID or legacy number, under the writer's read scope, on every write path: HTTP POST, PUT and PATCH, MCP create and update, `InsertSignal`, and the create and update of a runtime-defined type.
- **One refusal for missing and hidden.** A record the writer cannot read gets the same answer as a missing one: a validation error on the field that says how to name the record, for example `implements: no Decision or Goal "D999" you can read; name the one this Task implements by address (D113, G12), slug or ID`. A Signal's `task_ref` says what to send instead: `no Task "A185" you can read; send a Task's address (T273), slug or ID in task_ref, and name any other record in subject_type and subject_id`. An unregistered `subject_type` is refused on `subject_type`, and half a subject (type without ID, or the reverse) is refused too.
- **Stored form.** The target's address, or its ID when it has none. A Signal's `subject_id` is stored as the subject's ID, so the read rule sees it, and `answer_ref` as `Type:ID`.
- **Write-once.** Sending the same target again, by any of its names, changes nothing. A different target, or an empty value, is refused ("is write-once"). Correct a wrong reference with the relation tools and, if needed, a new record.
- **Same transaction.** The edge row is written in the record's own transaction: if it cannot be written, the record is not written either. Provenance and the relation event (`relation.asserted`, `new_row` true) follow after the commit, on every path since v1.165.0 (T875): the Task, Amendment and Signal modules and runtime types announce from their save hooks, and a Signal inserted raw (mcp's `create_signal`, through `NotifySignalCreated`) or emitted by core as a condition announces its `about` edges after its insert. Before v1.165.0 a raw Signal insert announced none, and on SQLite the other paths failed to read the edge back and announced none either. `MigrateSignalKinds` stays silent. Each edge carries `created_by_job: "references"`, with the writer in `created_by`. An `answer_ref` edge is written with the stored answer, after the answer's transition.
- **Without a relation store** (`App.Relations` not called), references are checked and stored, and no edge is written. A Module or a dynamic repository built without an App checks nothing and writes no edge, as before.
- **`InsertSignal`.** A caller with an identity (a `smeldr.Context`, or a scope attached with `WithReadScope`) is held to its read scope; a plain internal context is not, but its names must resolve. Core's own emitters keep their subject as given (an ID, possibly of a type with no table) and still get the edge. Inside a transaction the caller holds, core's own Signals are kept without an edge (`MigrateReferences` adds it) and anyone else's references are refused (`ErrBadRequest`).
- **`SchemaField.References`** (`references`) names the type a string field refers to. It is refused on a field that is not a string, and when it names a type that is neither registered nor the type being defined. A redefinition may add it to an existing field, which is how task_plan's `task_ref` becomes a reference.
- **Database.** `smeldr_tasks.implements` and `smeldr_amendments.task_ref` come with `CreateOrchestrationTables`; an existing database needs `EnsureReferenceColumns(ctx, db)` before the first Task or Amendment write (the example server calls it at boot).
- **Existing records** keep their text references until `MigrateReferences` turns them into edges.


### The `amends` relation and `Amendment.Amends` (A431, D102, v1.125.0)

An Amendment says which Decision it changes with its optional field `amends` (a Decision number, `"D104"`), and core turns that into an `amends` edge from the Amendment to the Decision (kind `amends`, label "Amends", reverse "Amended By", the pair Amendment to Decision and no other), so Trace's relation lanes and Navigator read one graph.

- **Declared at creation, refused when wrong.** A non-empty `amends` must name exactly one Decision. `D104`, `d104` and the older bare `104` are all read, and the value is stored in the canonical `D104` form. A number that names no Decision, names several, or is not a Decision number refuses the write (a validation error naming it), in every write path: the HTTP create and PUT and the MCP create and update. An empty `amends` creates no edge: most Amendments amend no Decision.
- **Write-once.** `amends` is set at creation, or later only while it is empty. A different value is refused ("is write-once"), and an empty value never clears it (a full-replace PUT that does not know the field keeps it), so the field and the edge cannot disagree. The edge is asserted once, when the field becomes set, with the caller's own actor (an `assert` provenance record names who recorded the Amendment); no relation store wired means no edge and a Warn, never a failed write. An edge is not asserted twice for the same pair.
- **Reading it.** `get_amendment` returns the field; `get_relations` (type_name `Amendment`, kind `amends`) returns the live edge. The edge is what queries use.
- **A rejected Amendment's edge ends (v1.128.0).** When an Amendment moves to `rejected`, on any path (transition_item, a PUT), its live `amends` edge ends with cause `amendment-rejected` and the rejecting caller as the actor (see "Relation history" below). A stored `amends` is never moved.
- **Existing Amendments have no link.** Amendments written before this field name their Decision only in free text, and a body that mentions D104 does not mean the Amendment amends D104, so nothing is derived. `App.AmendsBackfillCandidates(ctx)` is read-only: per Amendment with an empty `amends`, the existing Decisions its summary and body mention, for a person to read. `App.BackfillAmendsEdges(ctx, links, dryRun)` takes a mapping the caller has reviewed (`[]AmendsLink{{"A412", "D104"}}`), asserts the missing edges with the caller's own actor in ctx, fills `amends` where it is empty, skips pairs that already have a live edge, reports `asserted`, `already present`, `unresolved` and `ambiguous` per link, and with `dryRun` writes nothing. Exported Go, not an MCP tool.
- **Database.** The column `smeldr_amendments.amends` comes with `CreateOrchestrationTables`; an existing database needs `EnsureAmendmentAmendsColumn` before the first Amendment read (the example server calls it at boot). Without the column every Amendment read fails.
`belongs_to_domain`/`belongs_to_area` (A338, D71/D72) connect a Decision to its Domain/Area —
per-instance dynamic content types, not compiled into core, hence the lowercase target type
name (`domain`/`area`) unlike every other kind above's PascalCase compiled-type targets. No
Domain/Area content types ship with core itself; an instance defines them via
`define_content_type` and asserts these edges against its own items. `in_set` (D71) is
deliberately unrestricted — Set's whole point is membership spanning types core cannot
enumerate ahead of time, so it carries no `TypePairs` at all, unlike every other kind here.

`RegisterOrchestrationRelationKinds` also calls
[`RelationStore.RegisterReferenceType`](#registerreferencetype) for `"domain"` and
`"area"` — see that section for what a reference-type designation changes about how the
structural sweep treats a Domain or Area item that has never been published.

### Relation history: one row per life (A434, v1.128.0)

A row of `smeldr_relations` is **one life** of a relation: `created_at` is when it began, `invalid_at` when it ended (NULL while live). A relation that ends and is added again gets a **new row with a new ID**; the ended row stays as history. Nothing is hard-deleted in normal operation.

- **Asserting.** Dedup looks at live rows only (`invalid_at` NULL or in the future). Re-asserting a live relation updates its row and records `assert` again; re-asserting one whose rows have all ended inserts a new row. An explicit `ID` naming an ended row is refused with `ErrConflict`, never revived.
- **Every end is recorded.** An end writes a `ProvenanceRecord` with subject `RelationEdge`, verb `invalidate`, `FromState` `"live"` and the **cause** in `ToState`. The causes are a closed set, exported as constants:

| Cause | Constant | Ended by | Actor |
|---|---|---|---|
| `withdrawn` | `EdgeEndWithdrawn` | `RelationStore.Withdraw` / `withdraw_relation` | the caller, with a required reason |
| `swept` | `EdgeEndSwept` | `RelationStore.SweepStructural` (source or target no longer alive) | job `sweep-structural` |
| `recomputed` | `EdgeEndRecomputed` | `RecomputeAsserted` / `BulkRecompute` (the content field no longer names it) | the content write's caller, or job `relation-recompute` |
| `amendment-rejected` | `EdgeEndAmendmentRejected` | an Amendment's `amends` edge when the Amendment is rejected (D102) | the rejecting caller |
| `purged` | `EdgeEndPurged` | `RelationStore.Delete`, the administrative purge | the caller |
| `moved` | `EdgeEndMoved` | an open condition's place edge, when the record it is about moved (a `premise-flagged` condition follows its Decision, T925) | job `premise-flagged` |
| `not-recorded` | `EdgeEndNotRecorded` | (read only) an end with no record: every end before v1.128.0; nothing is backfilled | none |

- **`Withdraw(ctx, id, reason)`** ends a live relation on purpose. The reason is required (a `ValidationError` otherwise); an unknown id is `ErrNotFound`; a relation that has already ended is `ErrConflict` naming when, never a silent success. MCP `withdraw_relation(id, reason)` (smeldr.dev/mcp v1.51.0) is gated on the **`archive`** operation, the same Author tier as asserting (ending a relation keeps it, an archived-class move). CLI: `smeldr-cli relation withdraw <id> --reason <text>`.
- **What a save recomputes (v1.131.0).** The save hook `App.Relations` wires for a schema-based dynamic type recomputes only the relation kinds the schema's edge fields own (the fields with `Relation: "edge"`). A relation of any other kind on the item, asserted with `assert_relation`, is never touched by a save. Within an owned kind the field decides: a second edge of that kind from the same item is ended (`recomputed`) at the next save. The exported `RecomputeAsserted` and `BulkRecompute` keep their whole-set contract: the caller's `incoming` is every asserted relation of the source, and every other live asserted row is ended.
- **Recompute ends, it does not delete.** A derived edge the content field no longer names is ended (`recomputed`); new ones are inserted with an `assert` record and `CreatedBy`. The current set is the live rows only, so an edge that ended but is still in the field gets a new life on the next save (and, if its target is still dead, is swept again, with a record each time).
- **`Delete` is the administrative purge**: it removes the row, history and all, for an edge that should never have existed. It records `invalidate` (`purged`) first. No tool exposes it; end relations with `Withdraw`.
- **Reading it.** Current-state readers read live rows (reachability, `GetLive*`, `preview_impact`, the cascade, goal context, context packets, governance). History readers read every row: `GetBySource`/`GetByTarget` and `get_relations` return rows **in creation order**, and on `get_relations` (`RelationStore.MCPGetRelations`) every ended row carries `RelationEdge.Ended` (`EdgeEnd{Cause, At, Reason, ActorKind, ActorID}`; MCP `ended: {cause, at, reason, actor_kind, actor_id}`), read in one batched query. The actor is shown to every caller who passes the tool (D101 members audience). A remote reader tells a withdrawn relation from a swept one by `cause`, never from timing.
- **No schema change.** The cause lives in the provenance record. Without a provenance store wired, ends are still kept but every one reads `not-recorded`.

### The whole graph, paged: `RelationStore.ListRelations` (A464, v1.142.0)

One paged read of every relation on the instance, for a client that holds the graph in memory (an org-level snapshot) instead of reading per item.

```go
page, err := rs.ListRelations(ctx, smeldr.RelationListOptions{
    Kind: "depends_on", // optional; also SourceType, TargetType (exact match)
    Limit: 500,         // 0 means 50; above 500 is clamped; negative is refused
    Offset: 0,
    AsOf: prev.AsOf,    // zero on the first page; then pass back the page's AsOf
})
// page.Edges, page.Total, page.AsOf
```

- **Order:** `created_at`, then `id`, so pages never repeat or skip. An index `idx_relations_created (created_at, id)` is created with the relation tables.
- **One moment:** every page and its `Total` are cut at `AsOf`. An edge created after it is not in the set, and an edge that ended after it still counts as live and is returned with its `invalid_at`. The pages therefore describe the graph exactly as it was at `AsOf`. Then follow the event stream's `relations` topic from `AsOf` on: add an edge on `relation.asserted`, and drop it from the live set on `relation.ended`. Only an administrative purge (`Delete`, no tool) can shift a later page.
- **Live by default,** as `GetLive*` define it: `invalid_at` is NULL or later than `AsOf`. The structural sweep additionally requires `valid_at <= now`; the read side keeps the `GetLive*` meaning. `IncludeEnded` adds ended rows, with `Ended` filled as on `get_relations`.
- **IDs:** `SourceID` and `TargetID` are returned as stored. Every edge core writes uses the item's `Node.ID`, the `ID` the item list tools return, so an edge joins to an item on (type, `ID`) with no mapping. A context packet's canonical id is a different form. Type names are the registered names (`Decision`, `task_plan`), never folded. An edge asserted with a slug or a wrong type string is returned as stored until the structural sweep ends it.
- **Errors:** a negative `Limit` or `Offset` is a `ValidationError`. A missing relations table gives an empty page. Any other failure is `ErrInternal`.
- MCP: `list_relations` (Author, operation `read`). CLI: `smeldr-cli relation list [--all]`.

### Relations need two real ends (A505, D113)

A relation is stored only between two real, readable ends. On a store wired with `App.Relations`, `MCPAssertRelation`, `MCPProposeRelation` and `MCPObserveRelation` check both ends under the caller's read rules before they write. A store used without an App, and core's own Go `RelationStore.Assert`, are unchanged.

An end may be given by address, slug or ID. It is stored by its ID, so an edge given by slug never becomes a dead edge that the structural sweep later invalidates.

The checks run in this order:

1. The relation kind. An unknown kind returns `ErrNotFound` before any end is looked up.
2. The source end.
3. The target end.
4. The kind's type pairs. A type-pair violation between two real ends is still the `type_pairs` validation error.

If the caller's read scope cannot be loaded, the answer is `ErrInternal` and nothing is written.

Ends resolve with `App.ResolveReadableID`: address, slug, ID, then the transitional legacy number. A hidden item and a missing item run the same statements, so the two cannot be told apart. The read rule has no public web-content clause, so a scoped caller cannot relate to published web content it holds no grant for. The rule fails toward less access.

| Answer | When |
|---|---|
| the edge, stored by IDs | both ends exist and are readable |
| `ErrNotFound` | the kind is unknown, or an end is missing, unreadable, or of an unregistered type |
| validation error `type_pairs` | both ends are real, but the kind does not allow that pair |
| `ErrInternal` | the caller's read scope could not be loaded |

Over MCP, `assert_relation`, `propose_relation` and `observe_relation` to a missing end answer not found.

### Fields per type (A505, D114)

Seven types do not emit `PublishedAt` and `ScheduledAt` in JSON: `Goal`, `Signal`, `Task`, `Decision`, `Amendment`, `Rule` and `AuthorityStub`. Their state flows have no published or scheduled state, so the fields could never be set. This covers MCP get and list, HTTP JSON, webhook bodies and create results.

`Run` keeps both fields, because it has no flow and they can be set. Every web content type keeps them too. Decoding is unchanged: JSON that still carries the two keys decodes. The Go fields stay on `Node`.

Each of the seven types has a `MarshalJSON` that shadows the two fields. A test fails when a compiled type registered by `RegisterOrchestrationTypes` or `RegisterAuthorityTypes` is not classified, and checks that the fields are dropped exactly when the type's flow has neither a published nor a scheduled state.

### `RegisterReferenceType`

```go
func (s *RelationStore) RegisterReferenceType(ctx context.Context, typeName string) error
```

Designates a dynamic content type as **reference/lookup data** rather than editorial
content, for the default structural-sweep target checker's own liveness rule
(`App.SweepStructural`'s built-in `TargetChecker`, see "Scheduled sweep run records"
above). Idempotent — safe to call on every boot, same convention as `UpsertKind`.

By default, the structural sweep treats a dynamic content type's row as alive only when
its `Status` is `"published"` — correct for editorial content, where an unpublished
draft is not yet real. `domain`/`area` (D71/D72) are typically created via
`create_content` and never explicitly published, since they are reference data a
Decision points at, not content with its own publication lifecycle — under the default
rule, every never-published Domain or Area item's `belongs_to_domain`/`belongs_to_area`
edges were silently invalidated by the very first structural sweep. A type registered
here instead counts as alive whenever its row exists and its `Status` is not
`"archived"` — draft, published, and scheduled all count, mirroring the rule every
compiled orchestration type already gets unconditionally.

**A runtime type with its own state flow (T932)** follows the same rule as a reference type: alive unless its row is gone or its `Status` is `"archived"`. A flow of its own (a row in `smeldr_state_flows` for that type name, not the default flow) means the type has its own lifecycle, which may never include `published`: a `task_plan` moves draft, submitted, changes_requested, approved and abandoned. Before this, every relation to or from such an item (a plan's `refers_to` its Task, a Signal's `about` its plan) was ended at the next hourly sweep. A failed flow lookup is an error, so the sweep skips the edge rather than ending it. A type on the default flow keeps the editorial rule.

**`App.RepairSweptEdges(ctx, apply)` and `smeldr-server repair-swept-edges [--apply] [--report path]` (T932)** restore what the structural sweep ended while both ends were alive. A candidate is an ended row whose end provenance is cause `swept` by `sweep-structural`; it is restored, as a new row (the ended row stays as history), when both ends are alive under today's rule, no live row of the same (source, target, kind) exists, and it is the newest row of that relation whatever ended the others: a later row ended by a withdraw or a rewrite means the relation was ended on purpose, so it stays ended. The new row keeps kind, class, confidence, attributes and job, and gets an `assert` provenance by job `sweep-repair`. A restore sends no relation event and runs no resolver, so it opens no condition and does not flood the stream. `SweepRepair{Applied, Candidates, Restored, SkippedDead, SkippedLive, SkippedOlder, SkippedSuperseded, Failed, ByType, ByKind}` reports it; without `apply` (the command's default) nothing is written. A second apply restores nothing. Run it only on a release that has the rule above, or the next sweep ends the restored edges again. On a production instance, `--apply` waits for Peter's go after a Signal to architect and Peter; the order is in `docs/OPERATIONAL_NOTES.md`.

A reference-type designation is independent of whether the type is actually a
registered dynamic content type: `RegisterOrchestrationRelationKinds` designates
`"domain"`/`"area"` at the same point it registers the D71/D72 relation kinds, not at
the point an instance defines the type's own schema via `define_content_type` — the
target checker still requires `targetType` to resolve as a real registered dynamic
content type before consulting the reference-type table at all, so a designation for a
type that is never defined (or later removed) is simply inert.

### `Signal` structured fields (A296)

`Signal` (`orchestration.go`) carries five additional fields, populated when the Signal was
generated by the system rather than written by a human:

```go
SubjectType       string // e.g. "Decision" — mirrors ProvenanceRecord's own vocabulary
SubjectID         string // the item's ID
FromState         string // the blocked transition's origin state
ToState           string // the blocked transition's target state
RequiredOperation string // the operation that would have been required (D63/D64, A309 — an operation word, e.g. "approve", not a role name)
```

All five are empty on an ordinary human-authored Signal. `recordAuthorizationRequiredSignal`
(the mechanism behind `DrainEvalQueue`'s loud-failure path, see "Scheduled sweep run records"
above) populates all five — previously this information was only readable by parsing the
Signal's own free-text `Message` sentence.

```go
func EnsureOrchestrationSignalColumns(ctx context.Context, db DB) error
```

Adds the five columns to `smeldr_signals` on a pre-A296 database. Fresh installs already have
them via `CreateOrchestrationTables`'s own `CREATE TABLE`. Idempotent — safe to call on every
boot, same shape as every other `EnsureColumn`-based migration (T246).

### `ValidTransitions` (A296)

```go
type TransitionOption struct {
    ToState           string
    RequiredOperation string // "" if the transition has no operation gate
    RequiredReason    bool
    Strict            bool
}

func (a *App) ValidTransitions(ctx context.Context, typeName, fromState string) ([]TransitionOption, error)
```

Returns every legal transition out of `fromState` for `typeName`'s registered flow (or the
default flow, if `typeName` has none), including each transition's `RequiredOperation` — not
previously exposed by any documented API. Useful for a caller that needs to know, for any
item in any state, whether an operation gate applies before attempting the transition.

```go
options, err := app.ValidTransitions(ctx, "Decision", "proposed")
// [{ToState: "ratified", RequiredOperation: "approve", Strict: true}, {ToState: "archived"}]
```

Returns `nil, nil` (not an error) when `Config.DB` is nil or no flow is registered for
`typeName` (and no default flow exists either).
