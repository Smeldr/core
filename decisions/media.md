# Forge Decisions — Media

Archived from decisions/recent.md on 2026-05-17.
Read on demand. See DECISIONS.md for the full index.

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
