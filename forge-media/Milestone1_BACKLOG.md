# forge-media — Milestone 1 Backlog (v1.0.0)

Media upload, storage, MIME validation, HTTP endpoints, MCP integration,
and forge-cli commands. Zero third-party dependencies.

---

## Progress

| Step | File(s) | Status | Completed |
|------|---------|--------|-----------|
| 1 | forge core: config.go, forge.go | 🔲 Not started | — |
| 2 | forge-media/media.go | 🔲 Not started | — |
| 3 | forge-media/server.go | 🔲 Not started | — |
| 4 | forge-media/mcp.go | 🔲 Not started | — |
| 5 | forge-cli/media.go | 🔲 Not started | — |
| 6 | Docs: REFERENCE, README, ARCHITECTURE, CHANGELOG | 🔲 Not started | — |

---

## Layer 1 — forge core (prerequisite for all forge-media steps)

### Step 1 — config.go + forge.go

**Depends on:** nothing
**Decisions:** A73
**Files:** `config.go`, `config_test.go`, `forge.go`, `REFERENCE.md`

#### 1.1 — Config struct additions

- [ ] Add `MediaPath string` to `Config` struct in `forge.go` (godoc: upload directory path; default `./media` applied at handler time)
- [ ] Add `MediaMaxSize int64` to `Config` struct in `forge.go` (godoc: max upload size in bytes; default 5 MB applied at handler time)

#### 1.2 — loadConfigFile parser additions

- [ ] Add `case "media_path"` to switch in `loadConfigFile` — set `cfg.MediaPath = value`
- [ ] Add `case "media_max_size"` to switch — parse with `strconv.ParseInt(value, 10, 64)`; return error on invalid int

#### 1.3 — mergeFileConfig additions

- [ ] Add merge guard for `MediaPath` (copy from fileCfg when goCfg is zero)
- [ ] Add merge guard for `MediaMaxSize` (copy from fileCfg when goCfg is zero)

#### 1.4 — App.Config() accessor (Amendment A73)

- [ ] Add `func (a *App) Config() Config { return a.cfg }` to `forge.go`
- [ ] Add godoc comment

#### 1.5 — Tests

- [ ] Add test case in `config_test.go` for `media_path = ./uploads` — parsed correctly
- [ ] Add test case for `media_max_size = 10485760` — parsed as 10485760 int64
- [ ] Add test case for `media_max_size = notanumber` — returns error
- [ ] Add test case for merge: Go-code MediaPath wins over file value
- [ ] Add test case for merge: file MediaMaxSize applied when Go-code is zero

#### 1.6 — REFERENCE.md

- [ ] Add `media_path` entry under Config keys section
- [ ] Add `media_max_size` entry under Config keys section

#### Verification

- [ ] `go build ./...` — no errors
- [ ] `go vet ./...` — clean
- [ ] `gofmt -l .` — returns nothing
- [ ] `go test -v -run TestConfig ./...` — all green
- [ ] Review `ARCHITECTURE.md` and `DECISIONS.md` — A73 drafted and agreed upon

---

## Layer 2 — forge-media core types (no Step 1 dep at runtime, uses forge.DB interface)

### Step 2 — forge-media/media.go

**Depends on:** Step 1 (App.Config accessor)
**Decisions:** none new
**Files:** `forge-media/media.go`, `forge-media/media_test.go`, `forge-media/go.mod`, `go.work`

#### 2.1 — Module scaffolding

- [ ] Create `forge-media/go.mod` (module `github.com/forge-cms/forge/forge-media`, go 1.22)
- [ ] Add `use ./forge-media` to `go.work`

#### 2.2 — MediaType and MediaRecord

- [ ] Define `MediaType string` type with constants: `MediaTypeImage`, `MediaTypeDocument`, `MediaTypeVideo`, `MediaTypeOther`
- [ ] Define `MediaRecord` struct: `ID`, `Filename`, `OriginalFilename`, `MediaType`, `MIMEType`, `Description`, `SizeBytes`, `UploadedAt`

#### 2.3 — MediaStore interface

- [ ] Define `MediaStore` interface: `Store(filename string, data []byte) (url string, err error)`, `Delete(filename string) error`, `URL(filename string) string`

#### 2.4 — LocalMediaStore

- [ ] Implement `LocalMediaStore` struct with `dir string`, `baseURL string`
- [ ] Implement `NewLocalMediaStore(app *forge.App) *LocalMediaStore` — reads `MediaPath` and `BaseURL` from `app.Config()`; applies default `"./media"` if MediaPath is zero
- [ ] Implement `Store` — writes to `dir/filename`, returns URL
- [ ] Implement `Delete` — removes file (non-fatal if file missing: return nil)
- [ ] Implement `URL` — returns `baseURL + "/media/" + filename`

#### 2.5 — Filename generation

- [ ] Implement `generateFilename(original string) (string, error)`:
  - Format: `<unix-timestamp>_<6-byte-hex-random>_<sanitized-original>`
  - Sanitize: lowercase, replace non-`[a-z0-9._-]` with `_`, strip leading dots
  - Random: `crypto/rand` 6 bytes → hex string

#### 2.6 — MIME detection

- [ ] Implement `detectMIME(data []byte, ext string) (mimeType string, err error)`:
  - jpg/jpeg: magic `\xFF\xD8\xFF` → `image/jpeg`
  - png: magic `\x89PNG` → `image/png`
  - gif: magic `GIF8` → `image/gif`
  - webp: bytes 0–3 `RIFF` + bytes 8–11 `WEBP` → `image/webp`
  - pdf: magic `%PDF` → `application/pdf`
  - svg: UTF-8 text, search first 512 bytes for `<svg` → `image/svg+xml`
  - Mismatch: agent-actionable error: `"expected JPEG (from .jpg extension), got PNG content"`
  - Unknown extension: return error
- [ ] Implement `detectMediaType(mime string) MediaType` — prefix-based classification

#### 2.7 — DB operations

- [ ] Implement `CreateMediaTable(db forge.DB) error` — `CREATE TABLE IF NOT EXISTS forge_media (...)`
- [ ] Implement unexported `insertMedia(db forge.DB, r MediaRecord) error`
- [ ] Implement unexported `listMedia(db forge.DB, filter MediaType) ([]MediaRecord, error)` — filter="" returns all
- [ ] Implement unexported `getMediaByID(db forge.DB, id string) (MediaRecord, error)` — returns `forge.ErrNotFound` when missing
- [ ] Implement unexported `deleteMediaRecord(db forge.DB, id string) error` — returns `forge.ErrNotFound` when missing

#### 2.8 — Tests

- [ ] TestGenerateFilename — correct format, unique across 100 calls, sanitization of special chars
- [ ] TestDetectMIME — all 6 types detected correctly; mismatch returns agent-actionable error
- [ ] TestLocalMediaStore — Store writes file, URL returns correct URL, Delete removes file
- [ ] TestCreateMediaTable — table created without error on sqlite3 in-memory DB
- [ ] TestListMedia — insert 3 records, list all, list filtered by type

#### Verification

- [ ] `go build ./...` — no errors
- [ ] `go vet ./...` — clean
- [ ] `gofmt -l .` — returns nothing
- [ ] `go test -v ./forge-media/...` — all green
- [ ] Review `ARCHITECTURE.md` and `DECISIONS.md` — no new decisions required

---

## Layer 3 — forge-media HTTP server

### Step 3 — forge-media/server.go

**Depends on:** Step 2
**Decisions:** none new
**Files:** `forge-media/server.go`, `forge-media/server_test.go`

#### 3.1 — Server struct and constructor

- [ ] Define `Server` struct: `app *forge.App`, `store MediaStore`, `db forge.DB`
- [ ] Implement `New(app *forge.App, store MediaStore) *Server`:
  - Panics with `"forgemedia.New: app has no DB configured"` if `app.Config().DB` is nil
  - Calls `CreateMediaTable(db)` at startup; panics on error
  - Stores `app.Config().DB` as `db` field

#### 3.2 — HTTP handlers

- [ ] Implement `handleUpload(w, r)`:
  - Auth: Author+ via `forge.VerifyBearerToken`; call `forge.WriteError(w, r, forge.ErrUnauth)` if missing
  - Enforce `MediaMaxSize` — use `http.MaxBytesReader` before parsing multipart
  - Parse multipart: `file` (file data), `description` (string), `media_type` (optional hint)
  - `detectMIME` against extension; return 422 field error on mismatch
  - WCAG: if image and description empty → `forge.Err("description", "required for image uploads")`
  - `generateFilename`, `store.Store`, `insertMedia`
  - Return JSON `{"id","url","media_type","mime_type"}` with 201
- [ ] Implement `handleServe(w, r)`:
  - No auth — files are public once uploaded
  - `http.ServeFile` from `app.Config().MediaPath`
- [ ] Implement `handleList(w, r)`:
  - Auth: Editor+ via `forge.VerifyBearerToken`
  - `?type=image|document|video|other` filter
  - Return JSON array of MediaRecord
- [ ] Implement `handleDelete(w, r)`:
  - Auth: Editor+ via `forge.VerifyBearerToken`
  - Extract `{id}` from path
  - `deleteMediaRecord`, then `store.Delete(filename)` (non-fatal if file missing)
  - 204 No Content on success
- [ ] All error paths call `forge.WriteError` only — no raw `http.Error` or `w.WriteHeader`

#### 3.3 — HTTPHandler and Register

- [ ] Implement `Server.HTTPHandler() http.Handler` — returns internal mux with all 4 routes
- [ ] Implement `Server.Register(app *forge.App)`:
  - Calls `app.Handle("POST /media", s.HTTPHandler())`
  - Calls `app.Handle("GET /media/{filename}", s.HTTPHandler())`
  - Calls `app.Handle("GET /media", s.HTTPHandler())`
  - Calls `app.Handle("DELETE /media/{id}", s.HTTPHandler())`

#### 3.4 — Tests

- [ ] TestHandleUpload — valid JPEG upload returns 201 + JSON; missing description for image returns 422; size limit exceeded returns 413; MIME mismatch returns 422 with agent-actionable message
- [ ] TestHandleServe — serves file by filename; 404 for missing file
- [ ] TestHandleList — returns JSON array; `?type=image` filter applied
- [ ] TestHandleDelete — 204 on success; 404 for unknown id; 403 for insufficient role

#### Verification

- [ ] `go build ./...` — no errors
- [ ] `go vet ./...` — clean
- [ ] `gofmt -l .` — returns nothing
- [ ] `go test -v ./forge-media/...` — all green
- [ ] Review `ARCHITECTURE.md` and `DECISIONS.md` — no new decisions required

---

## Layer 4 — forge-media MCP integration

### Step 4 — forge-media/mcp.go

**Depends on:** Step 3, forge-mcp WithModule ServerOption
**Decisions:** forge-mcp Level 1 amendment (WithModule)
**Files:** `forge-media/mcp.go`, `forge-media/mcp_test.go`, `forge-mcp/mcp.go`

#### 4.1 — MCPModule interface analysis

- [ ] Read `forge.MCPModule` interface — note all 9 required methods
- [ ] Note: lifecycle methods (MCPPublish, MCPSchedule, MCPArchive, MCPUpdate) are not applicable to media; they will return a structured "not supported" forge.Error
- [ ] TypeName = "File" → forge-mcp generates: `create_file` (upload), `list_files` (list), `delete_file` (delete), `get_file` (get by ID), plus stub lifecycle tools

#### 4.2 — forge-mcp WithModule option

- [ ] Add `WithModule(m forge.MCPModule) ServerOption` to `forge-mcp/mcp.go`
- [ ] `WithModule` appends the module to `s.modules` — picked up by all tool and resource generation

#### 4.3 — forge-media MCPModule implementation

- [ ] Implement `Server.MCPMeta() forge.MCPMeta` — TypeName "File", Prefix "/media", Operations [MCPWrite]
- [ ] Implement `Server.MCPSchema() []forge.MCPField` — filename (required), data (required, base64 hint), description (with WCAG description text), media_type (optional)
- [ ] Implement `Server.MCPList(ctx, status...)` — returns all media records as `[]any` (status ignored for media)
- [ ] Implement `Server.MCPGet(ctx, slug)` — slug = ID; returns record or `forge.ErrNotFound`
- [ ] Implement `Server.MCPCreate(ctx, fields)` — upload: decode base64 data, detect MIME, validate, store, insert; returns MediaRecord
- [ ] Implement `Server.MCPUpdate(ctx, slug, fields)` — returns `forge.ErrBadRequest` with message "media files cannot be updated; delete and re-upload"
- [ ] Implement `Server.MCPPublish(ctx, slug)` — returns same "not supported" error
- [ ] Implement `Server.MCPSchedule(ctx, slug, at)` — returns same error
- [ ] Implement `Server.MCPArchive(ctx, slug)` — returns same error
- [ ] Implement `Server.MCPDelete(ctx, slug)` — deletes record + file

#### 4.4 — Tests

- [ ] TestMCPCreate — valid image, missing description, base64 decode error, auth check
- [ ] TestMCPList — returns records, filter param works
- [ ] TestMCPDelete — removes record, unknown id → error
- [ ] TestMCPUpdate — returns not-supported error with clear message

#### Verification

- [ ] `go build ./...` — no errors
- [ ] `go vet ./...` — clean
- [ ] `gofmt -l .` — returns nothing
- [ ] `go test -v ./forge-media/... ./forge-mcp/...` — all green
- [ ] Review `ARCHITECTURE.md` and `DECISIONS.md` — forge-mcp WithModule documented

---

## Layer 5 — forge-cli

### Step 5 — forge-cli/media.go

**Depends on:** Step 3 (HTTP endpoints)
**Decisions:** none new
**Files:** `forge-cli/media.go`

#### 5.1 — Command routing

- [ ] Add `case "media"` to `main()` switch in `forge-cli/main.go`
- [ ] Add media to `printUsage` output

#### 5.2 — upload subcommand

- [ ] `forge-cli media upload <file> [--description <text>] [--type image|document|video|other]`
- [ ] Read file from disk; detect image by extension (`.jpg .jpeg .png .gif .webp .svg`)
- [ ] Require `--description` flag for images; exit 1 with message if missing
- [ ] Multipart `POST /media` to `FORGE_URL/media`
- [ ] Print returned URL on success

#### 5.3 — list subcommand

- [ ] `forge-cli media list [--type image|document|video|other]`
- [ ] `GET /media?type=<filter>` (omit query param if no filter)
- [ ] Print table: ID | Original Filename | Type | Uploaded At | URL

#### 5.4 — delete subcommand

- [ ] `forge-cli media delete <id>`
- [ ] `DELETE /media/<id>`
- [ ] Print confirmation on 204; print error on non-204

#### 5.5 — Auth

- [ ] Use `FORGE_TOKEN` env var / `.forge-cli.env` (existing pattern from `client.go`)

#### Verification

- [ ] `go build ./...` — no errors
- [ ] `go vet ./...` — clean
- [ ] `gofmt -l .` — returns nothing
- [ ] `go test -v ./forge-cli/...` — all green
- [ ] Review `ARCHITECTURE.md` and `DECISIONS.md` — no new decisions required

---

## Layer 6 — Documentation and integration

### Step 6 — Docs

**Depends on:** Steps 1–5
**Files:** `README.md`, `AGENTS.md`, `ARCHITECTURE.md`, `CHANGELOG.md`, `REFERENCE.md` (verify Step 1.6), `example_test.go` or `forge-media/doc.go`

#### 6.1 — REFERENCE.md

- [ ] Verify `media_path` and `media_max_size` added in Step 1.6 — no duplicate edit needed

#### 6.2 — AGENTS.md

- [ ] Add forge-media wiring to "Adding MCP support" section with one-call example using `Register(app)` and `forgemcp.WithModule(mediaSrv)`

#### 6.3 — ARCHITECTURE.md

- [ ] Add `forge-media/` submodule entry
- [ ] Document `App.Config()` accessor
- [ ] Document `Register(app)` helper

#### 6.4 — CHANGELOG.md

- [ ] Add forge core v1.12.0 section (config keys, App.Config accessor)
- [ ] Add forge-media v1.0.0 section (initial release)
- [ ] Add forge-mcp entry (WithModule option)

#### 6.5 — Compile-verified wiring example

- [ ] Add `ExampleMedia` function to `forge-media/example_test.go` showing:
  ```go
  store := forgemedia.NewLocalMediaStore(app)
  srv := forgemedia.New(app, store)
  srv.Register(app)
  mcpSrv := forgemcp.New(app, forgemcp.WithModule(srv))
  ```

#### Verification

- [ ] `go build ./...` — no errors
- [ ] `go vet ./...` — clean
- [ ] `gofmt -l .` — returns nothing
- [ ] `go test ./...` — all green (including Example functions)
- [ ] Review `ARCHITECTURE.md` and `DECISIONS.md` — complete and accurate

---

## Completion criteria for Milestone 1 (forge-media)

- [ ] `forge-media/go.mod` exists with zero third-party dependencies
- [ ] `go.work` includes `use ./forge-media`
- [ ] All HTTP endpoints tested: POST /media, GET /media/{filename}, GET /media, DELETE /media/{id}
- [ ] MIME magic-byte validation rejects mismatched files with agent-actionable errors
- [ ] WCAG 1.1.1 enforced: description required for image uploads at both HTTP and MCP layers
- [ ] forge-mcp `WithModule` allows forge-media to participate in one MCP session
- [ ] forge-cli `media upload|list|delete` commands work against a running Forge instance
- [ ] `go test ./...` green across forge core, forge-media, forge-mcp, forge-cli
- [ ] `README.md`, `AGENTS.md`, `ARCHITECTURE.md`, `CHANGELOG.md` updated
- [ ] forge core tagged `v1.12.0`, forge-media tagged `forge-media/v1.0.0`, forge-mcp tagged `forge-mcp/v1.5.0`
