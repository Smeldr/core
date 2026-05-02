# Changelog — forge-media

All notable changes to forge-media are documented here.

Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versioning: [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [1.1.0] — 2026-04-30

Go 1.26.2 and module path migration to `forge-cms.dev` (Amendment A76).

### Changed

- `go.mod`: module path renamed from `github.com/forge-cms/forge-media` to
  `forge-cms.dev/forge-media`; `go` directive bumped from `1.22` to `1.26.2`.
- All imports updated to `forge-cms.dev/forge` and `forge-cms.dev/forge-media`.

---

## [1.0.0] — 2026-04-18

Initial release — Media Library for Forge (Decision 31).

### Added

- `MediaType` string type with constants `MediaTypeImage`, `MediaTypeDocument`,
  `MediaTypeVideo`, `MediaTypeOther`; derived from MIME type via `detectMediaType`.
- `MediaRecord` struct: `ID`, `Filename`, `OriginalFilename`, `MediaType`,
  `MIMEType`, `Description`, `SizeBytes`, `UploadedAt`.
  `GetSlug() string` returns `ID` — satisfies `forge.MCPModule` slug contract.
- `MediaStore` interface: `Store(filename string, data []byte) (url string, err error)`,
  `Delete(filename string) error`, `URL(filename string) string`.
- `LocalMediaStore`: writes files to `Config.MediaPath` (default `"./media"`);
  returns public URLs rooted at `Config.BaseURL + "/media/"`.
  Created with `NewLocalMediaStore(app *forge.App)`.
- `CreateMediaTable(db forge.DB) error`: idempotent `CREATE TABLE IF NOT EXISTS forge_media`
  with columns id, filename, original_filename, media_type, mime_type, description,
  size_bytes, uploaded_at.
- MIME detection via magic-byte sniffing (JPEG, PNG, GIF, WebP, PDF, SVG) with
  extension/content mismatch errors that are actionable by AI agents.
- Unique filename generation: `<unix-timestamp>_<6-byte-hex-random>_<sanitized-original>`.
- `Server` struct: HTTP server for media upload, serving, listing, and deletion.
  - `New(app *forge.App, store MediaStore) *Server`: panics if no DB configured.
  - `Register(app *forge.App, store MediaStore) *Server`: convenience constructor
    that creates and registers the server in one call.
  - `HTTPHandler() http.Handler`: internal mux with all four routes.
  - Routes:
    - `POST /media` — upload (Author+ role); WCAG 1.1.1 description required
      for image uploads; returns `{"id","url","media_type","mime_type"}` 201.
    - `GET /media/{filename}` — serve file (public; no auth required).
    - `GET /media` — list records (Editor+ role); `?type=` filter supported.
    - `DELETE /media/{id}` — delete record and file (Editor+ role); 204 No Content.
- `Server` implements `forge.MCPModule`:
  - `MCPMeta()`: TypeName `"File"`, Prefix `"/media"`, MCPRead + MCPWrite.
  - `MCPSchema()`: three fields — `filename` (required), `data` (required,
    base64-encoded), `description` (WCAG 1.1.1 hint for images).
  - `MCPCreate`: decodes base64 (StdEncoding, then RawURLEncoding fallback),
    validates MIME, stores file, inserts `MediaRecord`; returns the record.
  - `MCPList`: returns all `MediaRecord` values as `[]any` (status ignored).
  - `MCPGet`: returns single record by ID; `forge.ErrNotFound` when missing.
  - `MCPDelete`: removes DB record and stored file.
  - `MCPUpdate`, `MCPPublish`, `MCPSchedule`, `MCPArchive`: return
    `forge.ErrBadRequest` — media files do not support lifecycle transitions.
