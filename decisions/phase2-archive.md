# ARCHIVED — Forge Decisions: Phase 2

This file is archived. It contains D25–A87 and A96–A97, covering the period
2026-04-05 through 2026-05-17. It is preserved for historical reference.
New decisions are written to `decisions/recent.md` (or `decisions/nondecisions.md`
for Non-Decisions).

---
# Forge � Decisions: Phase 2

Decision 25 onwards.

## Decision 25 � Token management

**Status:** Locked
**Date:** 2026-04-05

**Decision:** Forge provides a `TokenStore` that issues, lists, and revokes
named bearer tokens backed by a SQLite table (`forge_tokens`). Tokens are
stateless HMAC values � the store adds a server-side record that enables
revocation and auditing without changing the token format itself.

### Token table schema

| Field | Type | Notes |
|-------|------|-------|
| `id` | TEXT | UUID v7, primary key |
| `name` | TEXT | Free-label set by Admin (e.g. "Desiree - Author") |
| `role` | TEXT | Forge role string (e.g. "author", "editor") |
| `token_hash` | TEXT | SHA-256 of the issued token � plaintext never stored |
| `expires_at` | TEXT | ISO 8601 � mirrors the token TTL |
| `revoked_at` | TEXT | NULL until revoked |
| `created_at` | TEXT | ISO 8601 |

### Token lifecycle

1. Admin calls `create_token(name, role, ttl)` via MCP
2. Forge calls `SignToken` to produce a signed HMAC token
3. SHA-256 of the token is stored in `forge_tokens`
4. The plaintext token is returned once � never again retrievable
5. On every request, `VerifyBearerToken` checks the hash against the store
   and rejects tokens that are revoked or expired
6. Admin calls `revoke_token(id)` to set `revoked_at` � effective immediately

### MCP tools (forge-mcp, Admin role required)

| Tool | Description |
|------|-------------|
| `create_token` | Issues a new named token with a given role and TTL |
| `list_tokens` | Lists all tokens with name, role, expiry, revoked status |
| `revoke_token` | Revokes a token by ID � effective on next request |

### What this is not

- No user accounts � Forge has no user table, only tokens with roles
- No contact field � no personally identifiable data stored (GDPR)
- No update_token � revoke and re-issue is the only model
- No UI � token management is via MCP tools only

### Module boundary

- `forge/auth.go` � TokenStore, CreateToken, ListTokens, RevokeToken
- `forge-mcp/` � three new admin MCP tools wrapping the above
- `VerifyBearerToken` in `forge/auth.go` gains a TokenStore parameter;
  when nil (no store configured), behaviour is unchanged (stateless HMAC only)

**Rationale:**
Stateless HMAC tokens cannot be revoked � a stolen token is valid until
expiry. A server-side store adds revocation at the cost of one database
lookup per request, which is acceptable given Forge's target workloads.
The SHA-256 hash pattern ensures that a database breach does not expose
usable tokens. Keeping the store optional (nil = stateless mode) preserves
backward compatibility for deployments that do not need revocation.

**Rejected alternatives:**
- Session table with user accounts: Overkill for a token-first auth model.
  Forge has no login flow � tokens are issued by Admin via MCP.
- JWT with blacklist: JWT parsing is more complex than HMAC verification.
  Forge already uses HMAC tokens � no reason to change the format.
- Contact field on tokens: Would store PII. Deliberately omitted.
  Admin uses the name label as a free-text identifier.

**Consequences:**
- `forge.Config` gains optional `TokenStore` field
- `App.TokenStore()` accessor for forge-mcp
- `forge_tokens` table must exist in the database for token management to work;
  Forge logs a warning at startup if TokenStore is configured but the table
  is absent
- Stateless HMAC (current behaviour) remains the default � no breaking change

---

## Amendment A66 � TokenStore: implementation

**Status:** Agreed
**Date:** 2026-04-05

**Implements:** Decision 25

**What changed:**

- `auth.go`: Added `TokenRecord` struct, `TokenStore` struct and
  `NewTokenStore(db, secret)` constructor, `probeTable`, `Create`,
  `List`, `Revoke` methods. `VerifyBearerToken` signature extended from
  2-arg to 3-arg `(r, secret, store *TokenStore)` � when store is nil,
  behaviour is unchanged (stateless HMAC only).
- `forge.go`: `Config.TokenStore *TokenStore` field; `App.tokenStore`
  private field; `App.TokenStore() *TokenStore` accessor; startup probe
  in `Handler()` that logs a warning if the table is absent.
- `forge-mcp/mcp.go`: `Server.tokenStore *forge.TokenStore` field; wired
  from `app.TokenStore()` in `New()`.
- `forge-mcp/transport.go`: sole `VerifyBearerToken` call updated to pass
  `s.tokenStore`.
- `forge-mcp/tool.go`: `authoriseAdmin()` helper; `tokenToolDefs()` (3
  tool definitions with JSON Schema); `handleTokenTool()` dispatcher;
  `handleToolsList()` and `handleToolsCall()` updated to expose and
  dispatch token tools when `s.tokenStore != nil`.

**Consequences:**
- MCP `tools/list` returns three additional tool entries when a TokenStore
  is configured; token tools require Admin role.
- Token tool names (`create_token`, `list_tokens`, `revoke_token`) are
  pre-dispatched before module-level auth to avoid name collisions.
- `forge-mcp` version bumps to `v1.1.0`; root package bumps to `v1.6.0`.

---

## Amendment A67 � `forge_html`: trusted raw HTML passthrough

**Status:** Agreed
**Date:** 2026-04-05

**Context:**
Go's `html/template` escapes all string output by default. There is no way for a
module template to render pre-rendered HTML (e.g. a video embed iframe, a
third-party widget) without a trusted passthrough function. The gap was identified
during planning of the forge-cms.dev demo page, which needs to embed an iframe
alongside Markdown content (`forge_markdown` handles the Markdown; `forge_html`
handles the iframe).

**What changed:**
- `templatehelpers.go`: `forgeHTML(s string) template.HTML` � one-line function
  returning `template.HTML(s)`; registered as `"forge_html"` in `TemplateFuncMap`;
  godoc warns that the caller is responsible for trust.
- `templatehelpers_test.go`: `TestForgeHTML` (3 sub-tests: passthrough, empty,
  not_escaped); `TestTemplateFuncMap_keys` expected count updated from 8 to 9.

**Consequences:**
- `TemplateFuncMap` grows from 8 to 9 entries.
- No exported Go symbol is added � `forgeHTML` is package-internal; only the map
  key `"forge_html"` is visible to templates.
- No interface, file, or behaviour change beyond the new function.
- Root package bumps to `v1.7.0`.

---

## Decision 26 � Last-admin guard on token revocation

**Status:** Locked
**Date:** 2026-04-06

**Decision:** `TokenStore.Revoke` refuses to revoke a token if it is the last
active (non-revoked, non-expired) token with the `admin` role. The check is a
two-step SQL lookup executed inside `Revoke` before the UPDATE. First the role
of the target token is fetched; if it is not `admin` the guard is skipped. If it
is `admin`, a COUNT of other active admin tokens is performed. If that count is 0,
`Revoke` returns the new sentinel error `ErrLastAdmin` without modifying any row.

### Guard logic

```go
// 1. Fetch role of target token � skip guard for non-admin:
SELECT role FROM forge_tokens WHERE id = $1

// 2. Only if role = "admin": count other active admins:
SELECT COUNT(*) FROM forge_tokens
WHERE role = 'admin'
  AND revoked_at IS NULL
  AND expires_at > $1
  AND id != $2
```

If COUNT = 0, `Revoke` returns `ErrLastAdmin`.

### New exported symbol

`ErrLastAdmin` � sentinel `forge.Error`, HTTP status 409 Conflict,
code `"last_admin"`, public message `"Cannot revoke the last active admin token"`.
Consistent with `ErrConflict` and other package sentinels.

### Scope

- `auth.go`: `Revoke` gains the two-step pre-check
- `errors.go`: `ErrLastAdmin` exported sentinel
- `forge-mcp/tool.go`: `handleTokenTool` returns a specific, actionable message
  for `ErrLastAdmin` on `revoke_token`
- forge core bumps to `v1.8.0` (new exported symbol `ErrLastAdmin`)
- forge-mcp bumps to `v1.2.0` (behavioural change in error surface)

### What this does not cover

- Natural token expiry � not an operator action; not guarded
- `Create` and `List` � unchanged
- MCP tool signatures � unchanged
- `forge_tokens` schema � unchanged

**Rationale:**
A single `revoke_token` call can permanently lock out all MCP-based administrative
access. Recovery requires direct database access � bypassing all Forge abstractions.
The guard makes this impossible without first creating a replacement admin token.
The check is in core, not in the MCP layer, so it protects against any caller
regardless of interface.

The guard is intentionally narrow: only the `admin` role is protected, only active
(non-revoked, non-expired) tokens are counted, and natural expiry is excluded
because it is not a discrete operator action. The two-query implementation is
preferred over a single-query approach so that non-admin tokens are never blocked
when no admin tokens exist � a correctness guarantee that the spec's single-query
wording did not provide.

**Rejected alternatives:**
- Guard in forge-mcp only: Does not protect against future non-MCP callers. The
  invariant belongs in the store, not the transport.
- Warn instead of refuse: A warning can be ignored by any caller. A hard refusal cannot.
- Guard all roles: Only admin tokens gate administrative access. Over-broad.
- Single-query guard (COUNT of other admins regardless of target role): Would
  incorrectly block revoking non-admin tokens when no admin tokens exist.

**Consequences:**
- `Revoke` is no longer unconditional � callers must handle `ErrLastAdmin`
- forge-mcp surfaces a clear, actionable error message for this case
- No schema changes, no breaking changes to existing call sites that do not hit the guard


## Decision 27 � Field format semantics: `forge_format` and `forge_description`

**Status:** Locked
**Date:** 2026-04-07

**Decision:** Forge introduces two optional struct tags � `forge_format` and
`forge_description` � that declare the expected content format and authoring
guidance for string fields. Both are surfaced in `MCPField` and in forge-mcp
tool descriptions to give AI agents explicit, actionable context when authoring
content. Neither tag triggers validation � they are semantic hints only.

### Struct tags

```go
Body  string `forge:"required" forge_format:"markdown" forge_description:"Write content in Markdown. Supports headings, lists, and code blocks."`
Embed string `forge_format:"html" forge_description:"Raw HTML only. Use for iframes and third-party embeds. Must be trusted content."`
```

### Supported `forge_format` values

| Value      | Meaning |
|------------|---------|
| `markdown` | CommonMark/GFM markdown � also covers plain text |
| `html`     | Trusted raw HTML � caller is responsible for sanitisation |

Fields without a `forge_format` tag have `Format = ""` � no hint emitted.

### `forge_description`

Free text written by the developer. Shown in forge-mcp tool descriptions when
present. No fixed vocabulary � the developer writes what the AI agent needs to
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

- No validation � format and description are hints only
- No breaking change � all new fields on `MCPField` are additive
- No impact on HTML rendering, template helpers, or non-MCP paths

**Rationale:**
`MCPSchema` exposes field types (`string`, `number`, `array`) but carries no
semantic or authoring information. A content type with multiple string fields
of fundamentally different kinds � markdown body, trusted HTML embed � gives
an AI agent no signal to distinguish them or author them correctly. The gap
was identified with `DocPage.Body` (markdown) and `DocPage.Embed` (trusted
HTML). The two tags close the gap at different levels: `forge_format` provides
machine-readable semantics; `forge_description` provides human- and AI-readable
authoring guidance.

**Rejected alternatives:**
- Convention-based field naming (`BodyMarkdown`, `EmbedHTML`): Fragile, not
  machine-readable, constrains naming.
- Validation based on format: Out of scope � semantics alone are sufficient
  for agent guidance.
- Additional format values from the start (`url`, `slug`, `plaintext`): Kept
  minimal � extended when concrete need arises.
- `forge_description` as a separate decision: Both tags solve the same AI-DX
  problem and belong together.

**Consequences:**
- `mcp.go`: `MCPField` gains `Format string` and `Description string`
- `module.go`: `MCPSchema()` reads `forge_format` and `forge_description`
  struct tags and populates the new fields
- `forge-mcp/mcp.go`: `fieldDescription` helper; `inputSchema` and
  `inputSchemaUpdate` emit `"description"` key with priority Logic
- forge core bumps to `v1.9.0` (new fields on exported struct `MCPField`)
- forge-mcp bumps to `v1.3.0` (tool description output changes)


---

## Decision 28 - forge-cli: operator CLI submodule

**Date:** 2026-04-07
**Status:** Agreed
**Files:** `forge-cli/` (new submodule)

**Context:**
Operators need a scriptable way to manage content and tokens on a running Forge
instance from a terminal or CI/CD pipeline. The REST API and MCP endpoints are
already stable. A thin CLI wrapping those endpoints is the minimal solution.

**Decision:**
Add a new Go submodule `forge-cms.dev/forge-cli` (package main).

### Design constraints

- Zero third-party dependencies (stdlib only: net/http, encoding/json, flag, bufio, os)
- No import of forge core -- the CLI is a pure HTTP client
- Config via FORGE_URL, FORGE_TOKEN, FORGE_MCP_URL env vars or .forge-cli.env fallback
- FORGE_MCP_URL defaults to {FORGE_URL}/mcp/message

### Files

| File | Purpose |
|------|---------|
| client.go | Config, loadConfig, loadEnvFile, request, getItem, mergeFields, printJSON, fatal |
| frontmatter.go | parseFrontmatter, parseFrontmatterFile -- YAML-subset parser |
| content.go | Content subcommands: create, update, publish, unpublish, archive, delete, list, get |
| token.go | Token subcommands via MCP JSON-RPC 2.0: create, list, revoke |
| status.go | status subcommand -- GET /_health |
| main.go | Entry point + top-level subcommand router |
| cli_test.go | Unit tests: frontmatter (9), mergeFields (2), loadEnvFile (3) |
| go.mod | Module forge-cms.dev/forge-cli, Go 1.22, no require block |
| CHANGELOG.md | Submodule changelog |
| README.md | Installation, configuration, all commands |

### Lifecycle operations (GET-then-PUT)

All lifecycle and update commands follow this pattern:

1. GET /{prefix}/{slug} -- retrieve current item as map[string]any
2. Modify the map (set Status, overlay frontmatter fields, etc.)
3. PUT /{prefix}/{slug} -- write back the entire map

PublishedAt is set server-side on the draft-to-published transition --
the client-supplied value is irrelevant. []string arrays survive the
[]interface{}-JSON-[]string round-trip correctly (confirmed by G23).

### Token commands

Token management posts MCP JSON-RPC 2.0 (tools/call) to FORGE_MCP_URL.
Tools: create_token, list_tokens, revoke_token. Admin role required.

**Consequences:**

- New forge-cli/ submodule added to go.work
- No changes to forge core or forge-mcp
- Tagged forge-cli/v0.1.0
- Integration test group G23 (TestFull_G23_CLIRoundTrip) validates the
  GET-then-PUT round-trip contract in integration_full_test.go

---

## Amendment A68 � storage.go/module.go: irregular pluralisation doc comments

**Date:** 2026-04-09
**Status:** Agreed
**Level:** 1 (micro-amendment � doc-only, no exported symbol change)

### Problem

Story ? "storys" by default. An implementer agent hit an internal server
error because neither Table nor At mentioned this class of problem. The
correction (orge.Table("stories")) was trivially available, but neither
doc comment surfaced it. This is a documentation gap, not a code bug.

### Changes

**storage.go � Table function doc comment:**

Extended to name the problem class explicitly and add a *Story example:

`go
// Table returns a [SQLRepoOption] that overrides the automatically derived
// table name for a [SQLRepo]. Use it when the default snake_case plural
// derivation does not produce the correct name � for example, types whose
// plural is not formed by appending "s" (Story ? "storys", not "stories").
//
//repo := forge.NewSQLRepo[*Story](db, forge.Table("stories"))
//repo := forge.NewSQLRepo[*BlogPost](db, forge.Table("posts"))
`

**module.go � NewModule doc comment, At option line:**

Extended to name the pitfall inline:

`go
//   - [At]: override URL prefix (default: "/"+lowercase(TypeName)+"s").
//     Use when the default pluralisation is wrong: Story ? "/storys".
//     Example: forge.At("/solved") or forge.At("/stories").
`

### Consequences

- No logic changes
- No new tests required
- No exported symbols added, removed, or renamed
- ARCHITECTURE.md unchanged (existing entries are historical record)
- NEXT.md deleted in the same commit

---

## Decision 29 � NavTree: first-class navigation abstraction

**Date:** 2026-04-11
**Status:** Agreed
**Level:** 2 (standard � new exported types, interface, DB migration, template injection, MCP tools)

### Problem

Forge applications need structured navigation that can be rendered in
templates and authored by AI consuming agents via MCP. The existing
approach (hard-coding nav in templates or ad hoc Extra data) is
inconsistent across modules and invisible to agents. A first-class
abstraction eliminates this duplication and surfaces nav to both
template authors and MCP clients.

### Decision

Add a `NavTree` type with two backing modes � DB-persisted (`NavModeDB`)
and code-supplied (`NavModeCode`). Nav is inactive by default (zero
`NavMode` value) so existing apps are unaffected.

### NavMode values

| Value | Constant | Meaning |
|-------|----------|---------|
| 0 | (zero) | Nav inactive � no tree, no migration |
| 1 | `NavModeDB` | Tree persisted in `forge_nav` table; CRUD via MCP |
| 2 | `NavModeCode` | Tree supplied once via `App.Nav()` at startup; read-only |

`NavModeDB` panics at `Handler()` startup if `Config.DB` is nil.

### NavItem fields

| Field | Type | Persisted | Notes |
|-------|------|-----------|-------|
| `ID` | string | ? | Caller-supplied; primary key |
| `Label` | string | ? | Display text |
| `Path` | string | ? | Href (absolute or relative) |
| `ParentID` | string | ? | Empty string = root item |
| `Module` | string | ? | Informational; not enforced |
| `Hidden` | bool | ? | Exclude from nav; show in breadcrumb |
| `Ghost` | bool | ? | Show in nav; breadcrumb only � not clickable |
| `SortOrder` | int | ? | Ascending; ties broken by Label |
| `Children` | []*NavItem | ? | In-memory only; populated by buildTree |

### Hidden / Ghost flag matrix

| Hidden | Ghost | In nav | In breadcrumb | Clickable |
|--------|-------|--------|---------------|-----------|
| false | false | ? | ? | ? |
| true | false | ? | ? | ? |
| false | true | ? | ? | ? |
| true | true | ? | ? | ? |

### forge_nav table schema

```sql
CREATE TABLE IF NOT EXISTS forge_nav (
    id        TEXT PRIMARY KEY,
    label     TEXT,
    path      TEXT,
    parent_id TEXT,
    module    TEXT,
    hidden    INTEGER,
    ghost     INTEGER,
    sort_order INTEGER
)
```

### Deferred wiring pattern

`Content()` runs before `Handler()`. At `Content()` time, `NavTree` is
not yet initialised. The fix: `Content()` detects modules implementing
`interface{ setNavTree(*NavTree) }` and appends them to
`App.navTreeModules`. In `Handler()`, after `NavTree` is initialised,
`setNavTree` is called on every collected module.

### Template injection

`TemplateData[T]` gains a `Nav []NavItem` field. Both `renderListHTML`
and `renderShowHTML` in `templates.go` call `m.navTree.Tree()` and
assign the result to `data.Nav` when `m.navTree != nil`.

Templates access navigation via `{{range .Nav}}` and recurse into
`{{range .Children}}`.

### delete_nav_item: recursive cascade

Deleting a nav item deletes all its descendants. `collectDescendantIDs`
walks the in-memory tree under a read lock to gather all descendant IDs,
then a single SQL `DELETE � WHERE id IN (�)` removes all of them. The
in-memory cache is rebuilt via `load()` after the deletion.

### MCP nav tools

All nav tools require the **Editor** role or higher.

| Tool | Condition | Description |
|------|-----------|-------------|
| `list_nav_items` | always (when NavTree ? nil) | Returns flat list of all NavItems |
| `create_nav_item` | NavModeDB only | Creates a new item |
| `update_nav_item` | NavModeDB only | Partial-overlay update |
| `delete_nav_item` | NavModeDB only | Recursive delete |

`update_nav_item` implements partial-overlay semantics: it fetches the
existing item via `Get()`, applies only the fields present in the MCP
args (non-empty string / explicit bool), then calls `Update()`. Absent
fields are preserved.

### New exported symbols

In package `forge`:
- `type NavMode int`
- `const NavModeDB NavMode`, `const NavModeCode NavMode`
- `type NavItem struct { � }`
- `type NavTree struct { � }` (opaque � fields unexported)
- `(*NavTree).HasDB() bool`
- `(*NavTree).Tree() []NavItem`
- `(*NavTree).List() []NavItem`
- `(*NavTree).Get(id string) (NavItem, bool)`
- `(*NavTree).Create(ctx context.Context, item NavItem) (NavItem, error)`
- `(*NavTree).Update(ctx context.Context, item NavItem) (NavItem, error)`
- `(*NavTree).Delete(ctx context.Context, id string) error`
- `Config.NavMode NavMode` field
- `App.Nav(items ...NavItem)` method
- `App.NavTree() *NavTree` method
- `TemplateData[T].Nav []NavItem` field

### Consequences

- forge core: v1.9.1 ? v1.10.0
- forge-mcp: v1.3.1 ? v1.4.0
- Zero behaviour change for apps that do not set `Config.NavMode`
- No new third-party dependencies
- `example_test.go` unchanged (no new examples required for this decision)
- NEXT.md deleted in the same commit

---

## Decision 30 � forge.config: file-based configuration

**Date:** 2026-04-11
**Status:** Agreed
**Level:** 2 (new exported Config fields, changed MustConfig behaviour)

### Problem

Forge Cloud agents need to provision a Forge instance by writing a file � without
compiling Go code. No existing mechanism supports this. The format must be simple
enough for an AI agent to generate without consulting docs.

### Decision

Add a minimal `key = value` file parser in `config.go`. `MustConfig` loads
`forge.config` from the working directory (or the path in `FORGE_CONFIG`) and
merges file values into the Go `Config`. Go-code fields always take precedence �
no breaking change for existing applications.

### File format

```
# forge.config � plain key = value pairs
base_url = https://example.com
https = true
nav_mode = db
org_name = Acme Corp
org_type = Organization
twitter_site = @acme
og_image = /static/og.png
```

Rules:
- Lines beginning with `#` are comments � skipped
- Blank/whitespace lines � skipped
- Split on the first `=` only � values may contain `=`
- Trim whitespace from key and value
- Unknown keys � silently ignored (forward compatibility)
- `secret` as a key � panics immediately with a descriptive message

### Key-to-field mapping (explicit table, no reflection)

| Key | Maps to | Valid values |
|-----|---------|--------------|
| `base_url` | `Config.BaseURL` | Full URL including scheme |
| `https` | `Config.HTTPS` | `true` or `false` |
| `nav_mode` | `Config.NavMode` | `db` or `code` |
| `org_name` | `Config.AppSchema.Name` | Free text |
| `org_type` | `Config.AppSchema.Type` | schema.org type e.g. `Organization` |
| `twitter_site` | `Config.OGDefaults.TwitterSite` | `@handle` |
| `og_image` | `Config.OGDefaults.Image.URL` | Relative or absolute path |

`url` in AppSchema is always derived from `BaseURL` � never a separate key.
`secret` in the file panics immediately.

### og_image path resolution

`og_image` is stored as-is by the parser. In `Handler()`, at auto-apply time,
if the value starts with `/` and `BaseURL` is non-empty, it is resolved to an
absolute URL by prefixing `BaseURL` (trailing slash stripped). This ensures
`og:image` is always an absolute URL as required by scrapers.

Example: `og_image = /static/og.png` + `base_url = https://example.com` ?
`OGDefaults.Image.URL = "https://example.com/static/og.png"`.

### Load order in MustConfig

1. Check `FORGE_CONFIG` env var � if set, use its value as the file path
2. Otherwise, try `forge.config` in the working directory
3. Merge file values into Go `Config` (Go-code non-zero values win)
4. Validate (`BaseURL` is required; `Secret` must be = 16 bytes � cannot come from file)

### Config.AppSchema and Config.OGDefaults � new fields

`AppSchema` and `OGDefaults` today only reach the `App` via `app.SEO()`. To
support file-based provisioning, both are added as fields on `Config`. In
`Handler()`, before the `setSEODefaults` loop, these fields are auto-applied
to `seoState` when `app.SEO()` has not already set those values (Go-code wins).

```go
// Option A: directly in Go config
app := forge.New(forge.MustConfig(forge.Config{
    BaseURL:    "https://example.com",
    Secret:     []byte(os.Getenv("SECRET")),
    AppSchema:  &forge.AppSchema{Name: "Acme", Type: "Organization"},
    OGDefaults: &forge.OGDefaults{TwitterSite: "@acme"},
}))

// Option B: via forge.config file (no Go code change needed for provisioning)
// forge.config:
//   org_name = Acme
//   org_type = Organization
//   twitter_site = @acme
```

### New exported symbols

- `Config.AppSchema *AppSchema` field
- `Config.OGDefaults *OGDefaults` field

New unexported functions (internal):
- `loadConfigFile(path string) (Config, error)`
- `mergeFileConfig(goCfg, fileCfg Config) Config`

### Error messages

Parse errors include line number, the invalid value, and what is expected:

```
forge.config line 4: invalid value "yes" for key "https" � expected "true" or "false"
forge.config line 7: invalid value "auto" for key "nav_mode" � expected "db" or "code"
```

### Consequences

- forge core: v1.10.0 ? **v1.11.0**
- forge-mcp: no changes (no version bump)
- forge-cli: no changes
- Zero behaviour change for apps that do not have a `forge.config` file
- No new third-party dependencies
- `example_test.go` unchanged
- NEXT.md deleted in the same commit
- `plans/core-next-plan.md` deleted in the same commit

---

## Amendment A69 � README restructure: short README + REFERENCE.md

**Status:** Accepted  
**Date:** 2026-04-14  
**Files affected:** `README.md` (rewritten), `REFERENCE.md` (new file)

### Problem

`README.md` had grown to 1 074 lines � a full API reference that was
useful as a reference but counterproductive as an introduction. Developers
opening the repo saw a wall of text before encountering a runnable example.
AI assistants loading the README for context exhausted token budgets before
reaching the code examples.

### Decision

Split the single `README.md` into two files:

- **`README.md`** � =150 lines. Title, badge, comparison table, one
  complete minimal example (runnable `package main`), one feature-showcase
  snippet, a bullet summary of what you get, and a `## Reference` link
  section. Nothing else.
- **`REFERENCE.md`** � verbatim extraction of all detailed sections removed
  from `README.md`: Getting started, Core concepts, Content types, Lifecycle,
  Roles & auth, SEO & structured data, AI indexing, Social sharing, Cookies &
  compliance, Storage, Middleware, Templates & rendering, Error handling,
  Redirects & content mobility, MCP integration, The AI-first design
  philosophy, Minimal complete example, Known issues.

### New README examples

Two new code examples replace the old "Getting started" walkthrough:

**Minimal example** � a complete `package main` that compiles and runs.
The `Post` type includes `Head()` and `Markdown()` so the showcase snippet
can safely use `SitemapConfig` and `AIIndex(LLMsTxtFull)` without a startup
panic (Decision A36 capability checks).

**Feature showcase** � a `NewModule(...)` call with one option per line.
Each line is commented with the endpoint or tag it enables. Developers can
delete lines to reduce scope without reading docs.

### Consequences

- No Go code changed. No exported symbols added, removed, or renamed.
- `go build ./...`, `go vet ./...`, `go test ./...` are green by
  construction.
- `example_test.go` unchanged � no Example functions compile-test README
  prose, only API signatures.
- `example/blog/main.go` package comment: was already at v1.11.0.
- `REFERENCE.md` is verbatim � no content was altered, only relocated.
- No version bump. Stays at v1.11.0 (documentation-only change).

---

## Amendment A70 � README: tagline, named value section, showcase fixes (2026-04-14)

**Status:** Agreed  
**Scope:** Documentation only. No exported symbols changed. No version bump.

### Problem

The restructured README (A69) still had several issues undermining its effectiveness for community engagement (HN, r/golang, pkg.go.dev):

1. **Tagline** was generic � could describe any web framework.
2. **Value proposition** was buried at the bottom as a flat anonymous bullet list, visible only after two full code examples.
3. **Duplicate table row** � "AI indexing (llms.txt + AIDoc)" and "AI-native endpoints (llms.txt, AIDoc)" said the same thing.
4. **`(*Post)(nil)` unexplained** � valid Go but unfamiliar to many developers.
5. **AfterPublish noop** � the signal callback returned `nil` with only a comment; the reader could not tell what it actually does.
6. **No pointer to runnable examples** in `example/`.
7. **"What you get" flat bullets** � anonymous, unordered, no descriptions.

### Changes

- **Tagline replaced:**
  ```
  **Go get Forge. From idea to production in one step.**
  Zero dependencies. Built-in content lifecycle. AI-native by default.
  ```
  First line is the hook; second delivers the three concrete differentiators.

- **New "What Forge gives you" section** inserted after the badge/version line, before the comparison table. All 15 features named and described, grouped into five categories: Content, Auth & security, Discovery, AI-native, Infrastructure.

- **Duplicate table row removed:** "AI-native endpoints (llms.txt, AIDoc)" deleted; "AI indexing (llms.txt + AIDoc)" kept.

- **`(*Post)(nil)` comment added:**
  ```go
  m := forge.NewModule((*Post)(nil), // nil pointer � type parameter inferred, no allocation
  ```

- **AfterPublish real body:**
  ```go
  forge.On(forge.AfterPublish, func(_ forge.Context, p *Post) error {
      log.Printf("published: %s", p.Slug) // fires on publish and scheduled?Published
      return nil
  }),
  ```

- **Examples pointer** added after the showcase code block, before the Reference section:
  ```markdown
  Three runnable examples are in [example/](example/):
  - example/blog � devlog with seeded posts, RSS, AI indexing, and scheduled publishing
  - example/api  � headless JSON API with role-based auth and a redirect manifest
  - example/docs � documentation site with AI indexing, /llms.txt, and AIDoc endpoints
  ```

- **Flat "What you get" bullet list removed** � all 15 features moved to the new named value section.

### Consequences

- README more effective for first-time visitors and community links.
- No call-site syntax changed. No AI generation accuracy affected.
- `example_test.go` unaffected � uses its own `examplePost` type, not the README showcase.
- No version bump. Stays at v1.11.0 (documentation-only change).
- NEXT.md deleted in the same commit.

---

## Amendment A71 � README: framework subtitle + 30-second start (2026-04-15)

**Status:** Agreed  
**Scope:** Documentation only. No exported symbols changed. No version bump.

### Problem

Two remaining first-impression gaps identified after A70:

1. **No plain-language description** � the tagline ("Go get Forge. From idea to
   production in one step.") is a pun, not a description. A first-time visitor
   landing from GitHub search or a link cannot tell what Forge is before scrolling.

2. **No immediate runnable path** � the quickest way to see Forge in action
   (`cd example/blog && go run .`) was buried after the feature list and comparison
   table. A developer who can run the project in 30 seconds is more likely to read on.

### Changes

- **Tagline replaced:**
  ```
  **Go get Forge. From idea to production in one step.**
  Zero dependencies. Built-in content lifecycle. AI-native by default.
  ```
  Replaced with a single plain sentence:
  ```
  A Go framework for content-driven applications. Zero dependencies. AI-native by default.
  ```

- **New `## 30-second start` section** inserted immediately after the badges/version
  line, before `## What Forge gives you`:
  ```bash
  git clone https://forge-cms.dev/forge
  cd example/blog
  go run .
  # open http://localhost:8080
  ```
  No prose � four commands only. The `open` line is a comment for cross-platform safety.

### Consequences

- README opens with a factual description instead of marketing copy.
- Clone-and-run path is the first content after the version badge.
- No call-site syntax changed. No AI generation accuracy affected.
- `example_test.go` unaffected.
- No version bump. Stays at v1.11.0 (documentation-only change).
- NEXT.md deleted in the same commit.

---

## Amendment A72 � VISION.md: strategic repositioning (2026-04-18)

**Status:** Agreed  
**Scope:** Documentation only. No exported symbols changed. No version bump.

### Problem

VISION.md last updated 2026-03-18 and no longer reflected the strategic
positioning decided on 2026-04-17:

1. No articulation of Forge as a typed state layer for AI agents (beyond content).
2. No documentation of the two-layer commercial model (Core AGPL / Cloud commercial).
3. Roadmap still described future plans for Phases 1�2, which shipped in v1.11.0
   and forge-mcp v1.4.0.

### Changes

**Inserted `## What Forge is`** after "The core thesis", before "The vision in one sentence":
- Forge as the typed, persistent state layer AI agents operate on
- Contrast with Temporal/LangChain (orchestration vs state substrate)
- MCP as the protocol for agent-driven state transitions
- Content as canonical use case; any typed stateful data is valid

**Inserted `## The two-layer model`** after the MCP section, before Roadmap:
- Forge Core: open source (AGPL), zero dependencies, self-hostable
- Forge Cloud: commercial, process-per-tenant, SQLite per customer, forge-admin closed source
- forge-media: LocalMediaStore with swappable interface for S3 in Phase 4

**Replaced `## Roadmap`** in full:
- Phase 1 ? DONE: forge-mcp v1.4.0
- Phase 2 ? DONE: forge v1.11.0 (full production foundation)
- Phase 3: Forge Cloud private beta (current focus)
- Phase 4: Forge Cloud GA (multi-site, bureau workflow, commercial licenses)

### Consequences

- VISION.md now accurately reflects shipped state and strategic direction.
- No call-site syntax changed. No AI generation accuracy affected.
- `example_test.go` unaffected.
- No version bump. Stays at v1.11.0 (documentation-only change).
- NEXT.md deleted in the same commit.

---

## Amendment A73 � forge.go/config.go: MediaPath, MediaMaxSize fields; App.Config() accessor (2026-04-25)

### Problem

`forge-media` (new optional submodule) needs to read the canonical upload
directory and maximum upload size from the app configuration, so that the
developer does not repeat these values at the call site.

### Change

**`forge.go` � `Config` struct:**

Added two optional fields after `OGDefaults`:

```go
// MediaPath is the upload directory for forge-media.
// Default ./media is applied at handler time when this is empty.
MediaPath string

// MediaMaxSize is the maximum upload size in bytes for forge-media.
// Default 5 MB (5242880) is applied at handler time when this is zero.
MediaMaxSize int64
```

**`config.go` � `loadConfigFile`:**

Added `media_path` and `media_max_size` cases to the key switch:

```go
case "media_path":
    cfg.MediaPath = value
case "media_max_size":
    n, err := strconv.ParseInt(value, 10, 64)
    if err != nil {
        return Config{}, fmt.Errorf("forge.config line %d: invalid value %q for key \"media_max_size\" � expected an integer number of bytes", lineNum, value)
    }
    cfg.MediaMaxSize = n
```

**`config.go` � `mergeFileConfig`:**

Added merge guards (Go code wins when non-zero):

```go
if goCfg.MediaPath == "" && fileCfg.MediaPath != "" {
    goCfg.MediaPath = fileCfg.MediaPath
}
if goCfg.MediaMaxSize == 0 && fileCfg.MediaMaxSize != 0 {
    goCfg.MediaMaxSize = fileCfg.MediaMaxSize
}
```

**`forge.go` � `App.Config()` accessor:**

```go
// Config returns a copy of the application configuration.
// Intended for use by optional forge submodules (e.g. forge-media).
func (a *App) Config() Config { return a.cfg }
```

### Consequences

- `forge-media` reads `app.Config().MediaPath` and `app.Config().MediaMaxSize` without
  requiring the developer to pass these values explicitly.
- The accessor returns a copy � callers cannot mutate the live config.
- No existing exported symbol changed. No call-site syntax affected.
- `example_test.go` unaffected.
- `REFERENCE.md` updated with `forge.config` key table including `media_path` and `media_max_size`.

---

## Decision 31 � forge-media submodule

**Status:** Agreed
**Date:** 2026-04-18

**Decision:** Introduce `forge-media` as an optional, separately versioned Go submodule
(`forge-cms.dev/forge-media`) that provides file upload, serving, listing,
and deletion for Forge applications, together with a full `forge.MCPModule` implementation
so that AI agents can manage media files through MCP. Add `WithModule` to `forge-mcp` as
the wiring point for externally-defined `MCPModule` implementations.

### Module layout

```
forge-media/
  go.mod          � module forge-cms.dev/forge-media, requires forge v0.0.0
  media.go        � MediaStore interface, LocalMediaStore, MediaRecord, DB helpers
  os_helpers.go   � testable wrappers for OS and crypto operations
  server.go       � Server struct, New(), Register(), four HTTP handlers
  mcp.go          � forge.MCPModule implementation on *Server
```

### MediaStore interface (`media.go`)

```go
type MediaStore interface {
    Store(filename string, data []byte) (url string, err error)
    Delete(filename string) error
    URL(filename string) string
}
```

`LocalMediaStore` implements `MediaStore` by writing files to `cfg.MediaPath`
(default `"./media"`) and computing URLs from `cfg.BaseURL`.

### MediaRecord struct

| Field | DB column | Notes |
|-------|-----------|-------|
| `ID` | `id` | 22-char base64 raw URL (16 random bytes) |
| `Filename` | `filename` | generated; safe for filesystem and URLs |
| `OriginalFilename` | `original_filename` | caller-supplied |
| `MediaType` | `media_type` | `image` / `video` / `audio` / `document` / `other` |
| `MIMEType` | `mime_type` | detected from magic bytes |
| `Description` | `description` | WCAG alt text; required for images |
| `SizeBytes` | `size_bytes` | |
| `UploadedAt` | `uploaded_at` | UTC |
| `URL` | *(computed)* | not persisted; set at query time |

Table: `forge_media`. Created by `CreateMediaTable(db forge.DB)`.

### HTTP endpoints (`server.go`)

| Method | Path | Role | Description |
|--------|------|------|-------------|
| `POST` | `/media` | Author+ | Upload a file (multipart) |
| `GET` | `/media/{filename}` | public | Serve a stored file |
| `GET` | `/media` | Editor+ | List records; `?type=` filter |
| `DELETE` | `/media/{id}` | Editor+ | Delete record + file |

`Register(app *forge.App, store MediaStore) *Server` wires all four routes
onto the forge `App` and returns the `Server` (which also implements `MCPModule`).

`New(app, store)` panics if `cfg.DB == nil` � DB is required for record persistence.

### MCPModule implementation (`mcp.go`)

`*Server` implements `forge.MCPModule`:

| Method | Behaviour |
|--------|-----------|
| `MCPMeta()` | TypeName `"File"`, Prefix `"/media"`, Read+Write ops |
| `MCPSchema()` | `filename` (required), `data` (required, base64), `description` (markdown hint) |
| `MCPList(ctx, statuses...)` | Returns all records; status filter ignored (no lifecycle) |
| `MCPGet(ctx, slug)` | Lookup by ID; `ErrNotFound` when missing |
| `MCPCreate(ctx, fields)` | Decode base64 `data`; detect MIME; require description for images; store + insert |
| `MCPUpdate` | Returns `ErrBadRequest` � delete and re-upload instead |
| `MCPPublish` | Returns `ErrBadRequest` |
| `MCPSchedule` | Returns `ErrBadRequest` |
| `MCPArchive` | Returns `ErrBadRequest` |
| `MCPDelete(ctx, slug)` | Delete DB record + best-effort file removal |

`MediaRecord.GetSlug() string` returns `r.ID`, satisfying the internal `slugger`
interface in `forge-mcp` for resource URI construction.

### WithModule option (`forge-mcp/mcp.go`)

```go
func WithModule(m forge.MCPModule) ServerOption {
    return func(s *Server) { s.modules = append(s.modules, m) }
}
```

Enables modules from external sub-packages (where `forge.App.MCPModules()` cannot
reach) to participate in the same MCP server. Wiring:

```go
mediaSrv := forgemedia.Register(app, store)
mcpSrv := forgemcp.New(app, forgemcp.WithModule(mediaSrv))
```

### MIME detection

`detectMIME(data []byte, ext string)` uses magic bytes and cross-checks extension.
Mismatch produces an agent-actionable `forge.Err("file", "expected JPEG (from .jpg extension), got PNG content")`.
`sniffMIME` covers: JPEG, PNG, GIF, WebP, PDF, MP4, WebM, MP3, WAV, OGG, SVG.

### Rejected alternatives

- **Single package**: Ruled out because forge core has zero third-party dependencies.
  SQLite and OS I/O belong in an optional layer.
- **Separate repository**: Ruled out to keep versioning simple � a single repo with a
  `replace` directive for local development, same as `forge-cli` and `forge-mcp`.
- **Struct tag on Node**: Media files are not content nodes � they have no slug,
  lifecycle, or template. A separate struct type is more honest.

### Consequences

- `forge-media` is independently versioned (`forge-media/v1.0.0`).
- `forge-mcp` bumps to `v1.5.0` for the `WithModule` addition.
- Forge core bumps to `v1.12.0` for `MediaPath`, `MediaMaxSize`, and `App.Config()`.
- No existing exported symbol in `forge` core changed.
- WCAG 1.1.1 is enforced at the handler level for image uploads � description required.
- `LocalMediaStore` never stores absolute URLs in the DB; computes from `baseURL` at read time.

---

## Amendment A74 � Rename FaviconLink ? HeadLink, HeadAssets.Favicons ? HeadAssets.Links

**Status:** Agreed
**Date:** 2026-04-18
**Files:** `head.go`, `templates.go`, `head_test.go`, `example_test.go`, `REFERENCE.md`

### Problem

`FaviconLink` and `HeadAssets.Favicons` implied the field only accepted favicon
and touch-icon elements. In practice, developers and AI agents legitimately place
any `<link>` element there � `rel="me"` (profile verification), `rel="manifest"`,
`rel="alternate"`, `rel="canonical"` � and the name gave no indication that these
were valid uses. A developer looking for where to add a `rel="me"` link would not
find it by scanning the type name or field name `Favicons`.

### Decision

Rename:
- `FaviconLink` ? `HeadLink`
- `HeadAssets.Favicons []FaviconLink` ? `HeadAssets.Links []HeadLink`

The four struct fields (`Rel`, `Href`, `Type`, `Sizes`) and the template rendering
path are unchanged. The renaming is purely semantic � the generated HTML is identical.

### Rationale

`HeadLink` is the correct name: it represents any HTML `<link>` element. The struct
already had no favicon-specific logic � it was a generic `<link>` builder from day one.
`Links` at the call site is immediately readable:

```go
app.SEO(&forge.HeadAssets{
    Links: []forge.HeadLink{
        {Rel: "icon", Type: "image/png", Sizes: "32x32", Href: "/favicon-32.png"},
        {Rel: "me", Href: "https://mastodon.social/@you"},
        {Rel: "manifest", Href: "/site.webmanifest"},
    },
})
```

An AI agent or developer scanning the struct immediately understands the field's scope.

### Consequences

1. **Breaking change** � all callers that reference `FaviconLink` or `.Favicons` must
   update. The struct's fields and rendering behaviour are unchanged.
2. **Version bump** � ships as `v1.13.0`.
3. `REFERENCE.md` updated: field name in the `HeadAssets` example, comment in the
   `TemplateData` table.
4. `ARCHITECTURE.md` updated: A63 row and `head.go` exports list updated to `HeadLink`.
5. `example_test.go` updated: `ExampleHeadAssets` uses `Links: []HeadLink{�}`.

---

## Amendment A75 � `markdown.go`: HTML passthrough in `renderMarkdown`

**Status:** Agreed � 2026-04-22
**Shipped in:** v1.13.1

### Problem

Content authors on self-hosted Forge sites need to mix Markdown prose with raw
HTML blocks (e.g. `<div class="pull-quote">`, iframes, custom components). The
existing `renderMarkdown` HTML-escaped every line, making such blocks impossible
to use inside body content fields.

### Decision

Lines whose trimmed form starts with `<` are emitted verbatim � without
HTML-escaping � by `renderMarkdown`. All other lines continue through the
existing pipeline (HTML escape ? inline markdown ? tag wrap).

### Rationale

Forge is self-hosted. Content authors have the same trust level as the role
system that governs MCP write operations � they are the site owner or explicitly
granted `Author`/`Editor`/`Admin` role. No anonymous or untrusted input reaches
`renderMarkdown` directly. Treating these users the same as anonymous web users
would prevent legitimate authoring workflows (embedded videos, styled callouts,
third-party widgets) with no security benefit.

The `<` prefix heuristic is intentionally simple: it catches both opening tags
(`<div>`, `<iframe>`) and closing tags (`</div>`). Inline `<code>` escaping is
unaffected because inline code is wrapped in backticks and processed after the
HTML-escape step.

### Consequences

1. HTML blocks in trusted body content now render correctly.
2. No change to the public API � `renderMarkdown` signature is unchanged.
3. Version bump: `v1.13.1` (patch � no API change, behaviour fix for trusted content).
4. `CHANGELOG.md` entry added under `[1.13.1]`.

---

## Amendment A76 � Go 1.26.2 + vanity module rename to `forge-cms.dev`

**Status:** Agreed � 2026-04-30
**Shipped in:** v1.14.0

### Problem

Two community issues requested via GitHub:

1. **Issue #1** � The `go` directive in all modules was `go 1.22`. Go 1.26.2 is
   the current supported release. Staying on 1.22 prevents use of language and
   stdlib improvements available in later releases.

2. **Issue #2** � All module paths use `github.com/forge-cms/...`. The project
   now has a dedicated domain (`forge-cms.dev`) and a vanity URL should be the
   canonical import path. This improves brand consistency and decouples the
   module path from the GitHub repository URL.

### Decision

**Go version:** Bump the `go` directive in all modules and `go.work` from their
current values (`1.22`, `1.24`, `1.25`) to `go 1.26.2`.

**Module rename:**

| Old path | New path |
|----------|----------|
| `github.com/forge-cms/forge` | `forge-cms.dev/forge` |
| `github.com/forge-cms/forge-mcp` | `forge-cms.dev/forge-mcp` |
| `github.com/forge-cms/forge-media` | `forge-cms.dev/forge-media` |
| `github.com/forge-cms/forge-cli` | `forge-cms.dev/forge-cli` |
| `github.com/forge-cms/forge-pgx` | `forge-cms.dev/forge-pgx` |

`forge-pgx` is included even though not listed in the original issue � it shares
the workspace and its `replace` directive would break immediately if the root
module path changed without updating `forge-pgx/go.mod`.

Historical references in `decisions/core.md` and `decisions/phase2.md` are left
as-is; they are permanent records of past decisions, not forward-facing API docs.

### forgeVersions() logic change

The old `forgeVersions()` used `strings.HasPrefix(path, "github.com/forge-cms/forge")`
because sub-modules shared the root path as a prefix. After the rename the modules
are independent paths (`forge-cms.dev/forge-mcp` is not a sub-path of
`forge-cms.dev/forge`). The matching logic is updated to:

```go
const base = "forge-cms.dev/"
// match any module under forge-cms.dev
if !strings.HasPrefix(path, base) { return }
key := strings.ReplaceAll(strings.TrimPrefix(path, base), "-", "_")
result[key] = v
```

Output keys are identical to before (`"forge"`, `"forge_mcp"`, etc.).

### Consequences

1. **Breaking import change** for all external users � they must update their
   `go.mod` and import paths. A minor version bump (`v1.14.0`) signals this.
2. All sub-modules ship coordinated version bumps:
   - forge-mcp: `v1.6.0`
   - forge-media: `v1.1.0`
   - forge-cli: `v0.3.0`
   - forge-pgx: not tagged (no behaviour change, workspace-local only)
3. `go get` resolution requires Caddy vanity URL config on `forge-cms.dev` to be
   deployed before external users can use the new paths. This is a deploy-day
   task noted in `NEXT.md` and handled separately.
4. `pkg.go.dev` badge in `README.md` updated to `forge-cms.dev/forge`.
5. `ARCHITECTURE.md`, `AGENTS.md`, `README.md`, `REFERENCE.md`, `CHANGELOG.md`
   all updated with the new import paths.
6. `decisions/` historical files left unchanged.
7. `forgeVersions()` godoc updated.
8. `CHANGELOG.md` version policy section updated.
9. Both `NEXT.md` (untracked) deleted after commit.

---

## Amendment A77

**Date:** 2026-05-02
**Status:** Agreed
**Files:** `head.go`, `module.go`, `templates.go`

### Problem

`renderListHTML` in `templates.go` always passes `Head{}` (zero) to
`NewTemplateData`. It never calls `resolveHead` or any list-specific head
function. Every module list page renders `<title></title>` with no meta
description, regardless of what the developer configures.

Root cause: `renderListHTML` simply had no mechanism to accept a list-level
head override. `HeadFunc[T]` only applies to the show page (single item);
there was no parallel option for the list page.

Confirmed via `curl http://localhost:8080/devlog | grep title` returning
`<title></title>`.

### Decision

Add `ListHeadFunc[T any](fn func(Context, []T) Head) Option` as a new exported
option in `head.go`. It follows the same pattern as `HeadFunc`:

- `listHeadFuncOption[T any]` — unexported generic type carrying the function.
- `listHeadFunc any` field on `Module[T]` — stores the option value at module
  construction time.
- `NewModule` options loop — type-asserts `listHeadFuncOption[T]` directly
  (same approach as `headFuncOption[T]`).
- `renderListHTML` — after building `data`, resolves the list head:

```go
if m.listHeadFunc != nil {
    if fn, ok := m.listHeadFunc.(func(Context, []T) Head); ok {
        head := fn(ctx, items)
        head = mergeOGDefaults(head, m.ogDefaults)
        data.Head = head
    }
}
```

`mergeOGDefaults` is used so that `Config.OGDefaults` (site-level fallbacks)
are still applied to the list head, consistent with show-page behaviour.

### Consequences

1. No breaking change — existing modules without `ListHeadFunc` behave
   identically (zero `Head{}`, empty `<title>`).
2. `ListHeadFunc` and `HeadFunc` are independent; both can be set on the same
   module without interference.
3. Version bump: `v1.14.1` (patch — bug fix + new exported symbol, no breaking
   change).

---

## Amendment A78 — node.go: ValidateStruct unexported; RunValidation is sole public entry point

**Date:** 2026-05-04
**Status:** Agreed
**Files:** `node.go`, `node_test.go`

### Problem

`ValidateStruct` was exported but was never intended to be part of the public
API — it was an implementation detail of `RunValidation`. Having it exported
created two entry points for the same logic, and surfaced internal panic messages
that referred to the internal function name. Test helpers called `validateStruct`
directly rather than going through the documented public function.

### Decision

Unexport `ValidateStruct` to `validateStruct`. `RunValidation` is the only
public entry point for struct-tag-based validation.

Changes:
- `ValidateStruct` → `validateStruct` in `node.go`
- `RunValidation` godoc updated: removed the now-broken `[validateStruct]`
  cross-reference; "Struct-tag constraints" listed instead
- Panic message updated from `"forge: ValidateStruct requires..."` to
  `"forge: RunValidation requires a struct or pointer to struct"` — the public
  function name is now used in the error
- `node_test.go`: section comment and all 9 test functions renamed from
  `TestValidateStruct*` → `TestRunValidation*`; all `validateStruct()` call
  sites replaced with `RunValidation()`

### Consequences

1. **Breaking change** — any caller that imported `forge.ValidateStruct` will
   fail to compile. `RunValidation` was always the documented entry point;
   `ValidateStruct` was never mentioned in README or REFERENCE.md.
2. Single public entry point reduces confusion and prevents callers from
   bypassing the pointer-normalisation in `RunValidation`.
3. No behaviour change — `RunValidation` calls `validateStruct` as before.

---

## Amendment A79 — forge-media/media.go: os.Root replaces filepath.Join (path traversal fix)

**Date:** 2026-05-04
**Status:** Agreed
**Files:** `forge-media/media.go`, `forge-media/media_test.go`

### Problem

`LocalMediaStore.Store()` and `LocalMediaStore.Delete()` previously constructed
file paths with `filepath.Join(s.dir, filename)`. This provides no protection
against path traversal: a crafted filename such as `../../etc/passwd` resolves
to a path outside `s.dir`, allowing an attacker to overwrite or delete arbitrary
files accessible to the process. CWE-22 (Improper Limitation of a Pathname to a
Restricted Directory).

### Decision

Replace `filepath.Join` with `os.Root` (introduced in Go 1.24):

- `os.OpenRoot(s.dir)` returns a sandboxed filesystem handle anchored at `s.dir`.
  Any filename that would escape the root — via `../`, symlink traversal, or
  other means — is rejected by the OS with an error before any I/O occurs.
- `Store()`: `root.Create(filename)` replaces the previous open-and-write sequence.
  `ensureDir(s.dir)` is still called first so the directory is created on demand.
- `Delete()`: `root.Remove(filename)` replaces `os.Remove(filepath.Join(...))`.
  The `os.IsNotExist` guard is retained — a missing file returns `nil`.
- `path/filepath` import removed from `media.go` (no longer needed).

No change to `server.go`: the manual `strings.Contains` check in `handleServe`
is preserved because `http.ServeFile` does not go through `LocalMediaStore`.

### Tests

Two new tests added to `forge-media/media_test.go`:

- `TestLocalMediaStore_store_pathTraversal` — calls `Store("../../etc/secret", ...)`
  and asserts that an error is returned and no file is written outside the root.
- `TestLocalMediaStore_delete_pathTraversal` — creates a canary file outside the
  root, calls `Delete("../canary-forge-media-test.txt")`, and asserts that an
  error is returned and the canary file still exists.

Both tests use `&LocalMediaStore{dir: dir, baseURL: "..."}` directly (same
package) to avoid touching `app` wiring.

### Consequences

1. Security: path traversal in `Store`/`Delete` is prevented at the OS level
   regardless of input. Upgrade is strongly recommended.
2. `forge-media` minimum Go version was already `1.26.2` (via A76); `os.Root`
   (Go 1.24) is available.
3. Version bump: `forge-media/v1.1.2` (patch — security fix, no API change).

---

## Amendment A80 — storage.go: SeqRepository[T] lazy iterator interface

**Date:** 2026-05-04
**Status:** Agreed
**Level:** 1 (additive — new exported interface and methods, no breaking change)
**Files:** `storage.go`, `storage_test.go`

### Problem

`Repository[T].FindAll` loads the entire result set into memory before returning
it. For large collections (feed generation, search indexing, CSV export) this
wastes memory proportional to the table size. There was no way to stream items
one at a time.

### Decision

Add `SeqRepository[T]` as an optional interface extending `Repository[T]`.
Both `MemoryRepo[T]` and `SQLRepo[T]` implement it. Callers type-assert their
`Repository` to `SeqRepository` to use lazy iteration.

```go
type SeqRepository[T any] interface {
    Seq(ctx context.Context, opts ListOptions) iter.Seq2[T, error]
}
```

Callers:

```go
if sr, ok := repo.(forge.SeqRepository[*Post]); ok {
    for item, err := range sr.Seq(ctx, opts) {
        if err != nil { ... }
    }
}
```

### Implementation

**`MemoryRepo[T].Seq`:** acquires an RLock, snapshots IDs and items map,
releases the lock, then yields items in insertion order applying the status
filter. Returns early if `yield` returns false (caller break).

**`SQLRepo[T].Seq`:** issues `SELECT *` with optional `WHERE "status" IN ($1,...)`
(no LIMIT), calls `QueryContext`, scans rows one at a time via cached reflection
(same path as `Query[T]`), yields each row. Propagates `rows.Err()` at end.
Does not apply sorting or pagination — use `FindAll` when those are required.

### Consequences

1. New exported symbol: `SeqRepository[T any]` interface.
2. `MemoryRepo[T]` and `SQLRepo[T]` each gain an unexported-method-equivalent `Seq`
   that satisfies the interface — no change to their existing method sets.
3. `Repository[T]` interface is unchanged — no breaking change for existing
   implementations.
4. Requires Go 1.23 `iter.Seq2` (forge minimum is 1.26.2 via A76).
5. Five new tests: `TestMemoryRepo_Seq_basic`, `TestMemoryRepo_Seq_statusFilter`,
   `TestMemoryRepo_Seq_yieldStop`, `TestSQLRepo_Seq_basic`,
   `TestSQLRepo_Seq_statusFilter_query`.

---

## Amendment A81 — go.mod: modernc.org/sqlite as test-only dependency

**Date:** 2026-05-04
**Status:** Agreed
**Level:** 1 (micro-amendment — test infrastructure only, no exported symbol change)
**Files:** `go.mod`, `go.sum`, `storage_sqlite_test.go` (new)

### Problem

All `SQLRepo[T]` tests used a fake `database/sql` driver that captures SQL
strings but never executes them against a real SQL engine. Behavioural
correctness — that `Save`, `FindByID`, `FindBySlug`, `FindAll`, and `Delete`
actually work against SQL — was untested. A parity suite existed for `MemoryRepo`
but had no counterpart for `SQLRepo`.

### Decision

Add `modernc.org/sqlite` as a **test-only** direct dependency in the core
module's `go.mod`. Import it only in `storage_sqlite_test.go`. The `forge`
package itself remains free of third-party dependencies at build time.

Exception to the zero-dependency rule: test-only imports (`_test.go` files)
may use third-party packages when:
1. The package is pure Go with no CGO (modernc.org/sqlite qualifies).
2. No alternative exists within the stdlib.
3. The import is isolated to a single `_test.go` file.
4. The decision is documented here so future maintainers understand the
   precedent.

### What this enables

`TestRepoParity_SQLRepo` runs the full `runRepoParity` 11-sub-test suite
against an in-memory SQLite database via `NewSQLRepo[parityItem]`. This
verifies that `$N` positional placeholders, `INSERT ... ON CONFLICT ("id") DO
UPDATE SET`, `RowsAffected()` on `DELETE`, and pagination all behave correctly
against a real SQL engine.

### Consequences

1. `go.mod` gains `require modernc.org/sqlite vX.Y.Z` and its transitive deps.
2. `go get forge-cms.dev/forge` users who never run tests are unaffected at
   runtime — the SQLite library is not linked into the produced binary.
3. CI `go test ./...` now requires a network-connected build cache on first run
   (same as any test dependency).
4. No new exported symbols. No changes to production code paths.
5. Sets the precedent: any future test-only CGO-free dependency follows the
   same three criteria and must be documented as an amendment.

---

## Amendment A82 — forge.go / config.go / static.go: Config.Dev + App.Static()

**Date:** 2026-05-04
**Status:** Agreed
**Level:** 2 (new exported symbol + config key + new file)
**Files:** `forge.go`, `config.go`, `static.go` (new), `static_test.go` (new), `REFERENCE.md`

### Problem

Every Forge site that serves static assets (CSS, JS, images) must implement
~10 lines of boilerplate to switch between an embedded FS in production and
disk-based serving in development. The pattern is identical across all sites;
Forge should own it.

### Decision

**`Config.Dev bool`** — new optional field on `forge.Config`. When true, enables
development mode for any feature that distinguishes dev from prod (currently
`App.Static`).

**`App.Static(prefix string, prod fs.FS, devDir string)`** — new method that
mounts a static file handler at `prefix`. Behaviour:

- `Dev == false` (production): serves from the embedded `prod` FS. Every
  response carries `Cache-Control: public, max-age=31536000, immutable`.
- `Dev == true` (development): serves from `devDir` on disk via
  `http.FileServer(http.Dir(devDir))`. A `slog.Info` line is emitted at mount
  time. Panics at startup if `devDir` does not exist.

**forge.config key `dev`** — accepts `"true"` or `"false"`. Sets `Config.Dev`
via the standard `loadConfigFile` / `mergeFileConfig` pipeline. Go-code value
takes precedence (zero-value false does not overwrite a file-set true).

**`withImmutableCache`** — unexported helper that wraps an `http.Handler` to
inject the Cache-Control header. Tested independently.

### Call site

```go
//go:embed static
var staticFiles embed.FS

staticFS, _ := fs.Sub(staticFiles, "static")
app.Static("/static/", staticFS, "static")
```

Replaces the per-site boilerplate pattern.

### Consequences

1. New exported symbol `App.Static` — godoc on method and on `Config.Dev`.
2. New forge.config key `dev` — `REFERENCE.md` "Static Files" section added;
   `dev` row added to the forge.config key table.
3. No breaking change — all existing `App` usage unaffected.
4. `withImmutableCache` is unexported; tests exercise it directly.
5. `App.Static` panics on missing `devDir` in dev mode — programmer error,
   detectable at startup.
6. 8 new tests in `static_test.go`.

**Fix (2026-05-04):** `a.mux.Handle(prefix, h)` changed to
`a.mux.Handle("GET "+prefix, h)`. Go 1.22+ `http.ServeMux` rejects mixing
method-unqualified patterns (e.g. `"/static/"`) with method-qualified patterns
(e.g. `"GET /"`) on the same mux. Static files are read-only; `GET` (and
`HEAD`, handled automatically by ServeMux for `GET` routes) is the correct and
complete set of methods. No test changes required.

---

## Amendment A83 — auth.go / forge.go / forge-cli/init.go: TokenStore bootstrap + forge-cli init

**Date:** 2026-05-04
**Status:** Agreed
**Level:** 1 (additive — new unexported method, new forge-cli subcommand, no breaking change)
**Files:** `auth.go`, `forge.go`, `auth_test.go`, `forge-cli/init.go` (new), `forge-cli/main.go`, `forge-cli/CHANGELOG.md`

### Problem

A new operator deploying Forge with a TokenStore had no recovery path if they
missed the initial bootstrap token. The token is shown only once (correct by
design — only the fingerprint is stored). If the operator missed it, they were
forced to manipulate the database directly. Additionally, site-level code
(`ensureBootstrapToken` in `seed.go`) was the only discovery mechanism for this
pattern; a new developer would not know it existed.

### Decision

**Part 1 — `TokenStore.ensureBootstrap` (forge core):**

New unexported method `ensureBootstrap(ctx context.Context)` on `*TokenStore`.
Called by `App.Handler()` immediately after a successful `probeTable()`.
Behaviour:

- Queries `SELECT COUNT(*) FROM forge_tokens`.
- If count > 0 or scan fails: no-op (table already has tokens, or not
  accessible — probeTable already warned).
- If count == 0: calls `ts.Create(ctx, "bootstrap-admin", "admin", 10yr)` and
  emits the raw token via `slog.Warn` — impossible to miss in any log output:
  ```
  WARN  forge: forge_tokens is empty — bootstrap admin token created (copy now, shown once):
        <raw token>
  ```

**Part 2 — `forge-cli init` (forge-cli v0.3.0):**

New subcommand that completes the setup flow in one command:

```
forge-cli init [--url URL] [--bootstrap-token TOKEN] [--name NAME] [--days N] [--force]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--url` | `http://localhost:8080` | Base URL of the Forge instance |
| `--bootstrap-token` | (required) | Pasted from startup log |
| `--name` | `operator` | Name for the created admin token |
| `--days` | `365` | Token TTL in days |
| `--force` | false | Overwrite existing `.forge-cli.env` |

Flow:
1. `GET /_health` unauthenticated — fail fast if unreachable.
2. `create_token` via MCP with the bootstrap token — admin role, given name, TTL.
3. Extract `"token"` from the JSON result.
4. Fail if `.forge-cli.env` exists and `--force` not set.
5. Write `.forge-cli.env`: `FORGE_URL=...` + `FORGE_TOKEN=...`.
6. `GET /_health` with the new token — warn on failure (env file preserved).
7. Print success lines.

`FORGE_MCP_URL` is not written — `client.go` defaults to `FORGE_URL/mcp/message`.
Role is hardcoded to `admin` — `init` is a bootstrap tool only.

### Consequences

1. `ensureBootstrap` is unexported — no public API change.
2. `forge_tokens` emptiness is checked on every `App.Handler()` call, but the
   query is a single `SELECT COUNT(*)` — negligible overhead, and only when
   `TokenStore` is configured.
3. Sites that call `ensureBootstrapToken` in their own code will see a
   duplicate token on the first run. Site-level code should be removed — this
   is a follow-up task for `forge-site-working`.
4. `forge-cli` bumped to v0.3.0 (new subcommand, no breaking change).
5. `cliVersion` corrected from `"0.1.0"` to `"0.3.0"` (was never updated
   through v0.2.0 / A76 module rename).
6. 2 new tests in `auth_test.go`: `TestTokenStore_ensureBootstrap_empty` and
   `TestTokenStore_ensureBootstrap_nonEmpty`.

---

## Amendment A84 — REFERENCE.md: accuracy fixes and gap-fill for v1.16.0

**Date:** 2026-05-05
**Status:** Agreed
**Level:** 1 (docs-only — no exported symbol changed, no code change)
**Files:** `REFERENCE.md`, `DECISIONS.md`, `decisions/phase2.md`

### Problem

A full audit of `REFERENCE.md` against v1.16.0 found 5 accuracy errors and
6 missing sections covering features shipped in Amendments A66–A83.

### Accuracy corrections

1. **Version examples** — health endpoint example showed `"version":"1.13.1"`
   and `"forge_mcp":"1.5.0"`; corrected to `"1.16.0"` and `"1.6.1"`.

2. **forge-mcp README link** — pointed to `forge-mcp/README.md` (removed subdir);
   corrected to `https://github.com/forge-cms/forge-mcp`.

3. **forge-media README link** — pointed to dead `https://forge-cms.dev/forge/tree/main/forge-media`;
   corrected to `https://github.com/forge-cms/forge-media`.

4. **Rate Limiting section** — falsely stated "Forge does not include a built-in
   rate-limiting middleware". `forge.RateLimit` has existed since a prior amendment.
   Section rewritten to document `forge.RateLimit(100, time.Minute)` and
   `forge.TrustedProxy`.

5. **`app.Content` fallback path in examples** — both "Getting Started" and
   "Minimal Complete Example" used `app.Content(&Post{}, forge.At("/posts"), ...)`
   which silently skips AI/sitemap/feed wiring when a raw struct pointer is passed
   instead of a `Registrator`. Corrected to `forge.NewModule((*Post)(nil), ...)`.

### Missing sections added

6. **Token management** — new section (between "Roles & auth" and "SEO") covering
   `NewTokenStore`, `Config.TokenStore`, `forge_tokens` DDL, bootstrap flow with
   `slog.Warn`, `TokenStore.Create/List/Revoke`, `ErrLastAdmin`, MCP tools table,
   and a critical footgun warning about using `SignToken` alongside `TokenStore`.

7. **Navigation** — new section (between "Cookies & compliance" and "Storage")
   covering `NavModeDB`/`NavModeCode`, `Config.NavMode`, `App.Nav()`, `NavItem`
   fields table, template `.Nav` usage, and the MCP nav tools note.

8. **AppSchema** — new subsection in "SEO & structured data" after "Rich result
   types" documenting `app.SEO(&forge.AppSchema{Type, Name, URL, Logo})`.

9. **OGDefaults** — new subsection at end of "Social sharing" documenting
   `app.SEO(&forge.OGDefaults{Image, TwitterSite, TwitterCreator})`.

10. **AbsURL** — new note in "Head" section (after Breadcrumbs, before HeadFunc)
    documenting `forge.AbsURL(base, path)` helper.

11. **SeqRepository** — new subsection in "Storage" (between MemoryRepo and
    Production SQL repository) covering type-assert pattern and `iter.Seq2` loop.

12. **forge-cli** — new section (between forge-media and Static files) covering
    install, `init` subcommand, all commands table, and frontmatter file format.

### Additional fix

13. **`ErrLastAdmin`** added to the sentinel errors list in "Error handling".

### Consequences

- No exported Go symbols added, removed, or renamed.
- No build, vet, or test changes required.
- `REFERENCE.md` now accurately reflects v1.16.0 across all shipped amendments.

---

## Amendment A85 — copilot-instructions.md: docs/content workflow; FEATURELIST.md

**Date:** 2026-05-05
**Status:** Agreed
**Level:** 1 (docs-only — no exported symbol changed, no code change)
**Files:** `.github/copilot-instructions.md`, `FEATURELIST.md` (new), `DECISIONS.md`, `decisions/phase2.md`

### Problem

Two gaps in the repo's operational documentation:

1. **No docs workflow** — Copilot instructions had a detailed standard step workflow
   and release tagging workflow, but no parallel workflow for docs-only tasks
   (updating REFERENCE.md, README.md, FEATURELIST.md) or for creating content
   for forge-cms.dev/docs. Without a formal workflow, docs tasks were handled
   ad hoc with no consistent structure for review, approval, or content drafts.

2. **No feature list** — No single file enumerated what Forge generates and
   includes automatically. New users, AI agents, and the architect had no
   authoritative reference for the complete feature surface.

### Changes

**`.github/copilot-instructions.md`** — new `## Docs and content workflow` section
inserted between `## Standard step workflow` and `## Release tagging`. The section
defines:
- A doc freshness check on every session start (REFERENCE.md, README.md, FEATURELIST.md)
- A 9-step docs and content task workflow: propose scope → repo doc review → apply
  changes → content suggestions → outline → full drafts → save drafts → propose
  commit → update context
- Push permission rules (explicit instruction required, separate from commit approval)
- Draft file naming and save location (`Forge-site-working/content/YYYYMMDD-HHMMSS-<slug>.md`)

**`FEATURELIST.md`** — new file in repo root. Complete feature list for v1.16.0 (A84),
grouped into 9 categories: Routes and feeds, Storage, Rendering, Lifecycle, Access
control, Navigation, MCP tools, Template infrastructure, SEO, Operations, forge-media,
forge-cli, Developer and AI-agent experience.

### Consequences

- No exported Go symbols added, removed, or renamed.
- No build, vet, or test changes required.
- Future doc sessions have a consistent, auditable workflow.
- FEATURELIST.md must be updated whenever an amendment adds or changes a feature.

---

## Amendment A86 — copilot-instructions.md: CLI and MCP tool parity rule

**Date:** 2026-05-05
**Status:** Agreed
**Level:** 1 (docs-only — no exported symbol changed, no code change)
**Files:** `.github/copilot-instructions.md`, `DECISIONS.md`, `decisions/phase2.md`

### Problem

Forge ships MCP tools for every admin operation, but there was no formal rule
requiring that forge-cli expose equivalent commands in the same release. This
allowed CLI gaps to accumulate silently — the nav tools gap (four MCP nav tools
with no CLI counterpart in forge-cli v0.3.0) was the first concrete instance.
Without a written rule, future milestones could repeat the pattern.

### Change

**`.github/copilot-instructions.md`** — new `## CLI and MCP tool parity` section
inserted between `## Docs and content workflow` and `## Release tagging`.

The section states:
- Every admin operation available via MCP tools must also be available via forge-cli
- CLI is the human fallback when agents are unavailable
- The rule applies per milestone: CLI commands ship in the same release as MCP tools
- Current known gap documented: forge-cli v0.3.0 has no nav commands despite
  `list_nav_items`, `create_nav_item`, `update_nav_item`, `delete_nav_item` existing
- Gap is tracked and will be closed in the nav CLI milestone

### Consequences

- No exported Go symbols added, removed, or renamed.
- No build, vet, or test changes required.
- Future milestone planning must include CLI commands whenever MCP tools are added.
- Nav CLI gap is formally acknowledged and tracked.

---

## Amendment A87 � signals.go: AfterSchedule Signal constant

**Date:** 2026-05-06
**Status:** Agreed
**Milestone:** 11 / Layer 1

### Problem

The Scheduled status transition fires AfterUpdate but no dedicated signal. Webhook
consumers and MCP subscription listeners cannot distinguish a scheduling event from
a plain content edit without inspecting the payload. This makes it impossible to
subscribe to post.scheduled webhook events or react to scheduling via signal handlers.

### Change

**signals.go** � new constant added to the Signal const block:

`go
// AfterSchedule fires after a content item transitions to Scheduled status.
// It fires in addition to AfterUpdate � not instead of it. Runs
// asynchronously � errors and panics are logged, never returned.
AfterSchedule Signal = "after_schedule"
`

AfterSchedule is dispatched in module.go alongside AfterUpdate whenever

ewStatus == Scheduled && prevStatus != Scheduled. It fires from both HTTP
updateHandler and MCPSchedule.

### Consequences

- New exported Signal constant: additive only; no existing handlers affected.
- All code that ranges over m.signals is range-based; the new key is ignored
  unless a handler is registered with orge.On[T](AfterSchedule, fn).
- ARCHITECTURE.md updated: AfterSchedule added to the signal constants table.
- No breaking change; no required application updates.

---

## Non-Decision A96 — Sitemap ping (T39)

**Date:** 2026-05-16
**Status:** Agreed
**Level:** 1 (docs-only — no code change)

### What was considered

An opt-in `SitemapPingURL string` field on `Config` that fires an HTTP GET
after every `AfterPublish` signal to notify search engines of new content.

### Decision

Forge will not provide sitemap ping.

### Rationale

Google deprecated their ping endpoint in June 2023. The only remaining
protocol with real adoption is IndexNow (Bing, Yandex), which requires an
API key and a verification file hosted on the site — this is
application-level setup, not framework responsibility. Forge must not own
deployment-specific integrations.

### Developer pattern

Register an `AfterPublish` signal handler in `main.go` that calls your
preferred indexing API. `SignalEvent` carries the slug and URL:

```go
app.OnSignal(forge.AfterPublish, func(ctx context.Context, ev forge.SignalEvent) error {
    // ev.URL is the canonical URL of the published content item.
    // Call your indexing API here (IndexNow, etc.)
    return nil
})
```

See REFERENCE.md — "Search engine indexing" for a full example.

### Consequences

No exported Go symbols added, removed, or renamed.
No build, vet, or test changes required.

---

## Amendment A97 — Built-in opt-in audit trail (T21)

**Date:** 2026-05-16
**Status:** Agreed
**Scope:** `audit.go` (new file), `forge.go` — `App.Audit`, `App.Handler` (Level 3 — new exported API)

**Problem:**
Applications built on Forge have no standard way to record who changed what and
when. Each team rolls bespoke audit tables. The signal bus (A94) already captures
every lifecycle transition with actor identity, content type, slug, and previous
state — but only for webhook delivery. There is no built-in persistence path.

**Decision:**
Add `App.Audit(store AuditStore) *App` — a single opt-in call that:
1. Subscribes to `AfterPublish`, `AfterSchedule`, `AfterArchive`, and `AfterDelete`
   via the existing signal bus.
2. On each event, appends an `AuditRecord` to the provided `AuditStore`.
3. Mounts `GET /_audit` (Editor-or-higher) that returns the stored records as a
   JSON array, filterable by `from`, `to` (RFC3339), `type`, and `actor`.

`AfterCreate` and `AfterUpdate` are intentionally excluded: they fire on every
save (including auto-saves / draft cycles) and would produce unbounded noise in
the audit log. The audit trail records only transitions that change publication
state.

**Implementation:**
- `AuditRecord` — immutable struct: `ID`, `Timestamp`, `Signal`, `ContentType`,
  `Slug`, `ActorID`, `ActorRole`, `PreviousState`.
- `AuditFilter` — query narrowing: `From`, `To time.Time`, `ContentType`, `ActorID string`.
- `AuditStore` interface — `Append(ctx, AuditRecord) error` + `List(ctx, AuditFilter) ([]AuditRecord, error)`.
- `NewAuditStore(db DB) AuditStore` — default SQL implementation; timestamps
  stored as RFC3339 strings for SQLite compatibility (same pattern as `auth.go`).
- `CreateAuditTable(db DB) error` — DDL helper; creates `forge_audit_log` table.
- `GET /_audit` mounted lazily in `App.Handler()` when `auditStore != nil`.
  Auth resolved with same nil-check pattern as `New()`.

**Why signal bus and not a module option:**
`App.Audit` is a third independent `OnSignal` subscriber, parallel to `App.Webhooks`
and forge-agent's subscriber. No shared helper is needed — the signal bus IS the
abstraction. Adding a module option would couple every content type to the audit
path unconditionally.

**Rejected alternatives:**
- *Module option `WithAudit()`*: couples audit to module registration; modules
  added after `App.Audit()` would require re-wiring.
- *Middleware/interceptor*: would require access to the HTTP handler layer, not
  the lifecycle layer. Actor identity is cleanly available on `SignalEvent`.
- *`AfterCreate`/`AfterUpdate` included*: rejected — drafts fire these on every
  auto-save; audit log would grow unboundedly with low-value entries.

**New exported symbols:**
- `audit.go`: `AuditRecord`, `AuditFilter`, `AuditStore`, `NewAuditStore`, `CreateAuditTable`
- `forge.go`: `App.Audit`

**New HTTP surface:**
- `GET /_audit` — requires Editor role; returns `[]AuditRecord` JSON.

**Forge core → v1.22.0.**
