# Forge Decisions — Navigation

Archived from decisions/recent.md on 2026-05-17.
Read on demand. See DECISIONS.md for the full index.

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
