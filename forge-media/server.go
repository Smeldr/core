package forgemedia

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	forge "forge-cms.dev/forge"
)

// ─── Server ───────────────────────────────────────────────────────────────────

// Server handles media upload, serving, listing, and deletion for a Forge
// application. Create it with [New] and register its routes with [Register].
type Server struct {
	app     *forge.App
	store   MediaStore
	db      forge.DB
	maxSize int64
	dir     string
}

// New returns a Server backed by store. It panics if the application has no
// DB configured (DB is required to persist media records) or if the media
// table cannot be created.
func New(app *forge.App, store MediaStore) *Server {
	cfg := app.Config()
	if cfg.DB == nil {
		panic("forgemedia.New: app has no DB configured — set Config.DB before calling New")
	}
	if err := CreateMediaTable(cfg.DB); err != nil {
		panic(fmt.Sprintf("forgemedia.New: create media table: %v", err))
	}
	maxSize := cfg.MediaMaxSize
	if maxSize == 0 {
		maxSize = 5 << 20 // 5 MB default
	}
	dir := cfg.MediaPath
	if dir == "" {
		dir = "./media"
	}
	return &Server{
		app:     app,
		store:   store,
		db:      cfg.DB,
		maxSize: maxSize,
		dir:     dir,
	}
}

// Register mounts the media routes on app and returns s for chaining.
// Routes registered:
//
//	POST   /media           — upload
//	GET    /media/{filename} — serve (public)
//	GET    /media           — list (Editor+)
//	DELETE /media/{id}      — delete (Editor+)
func Register(app *forge.App, store MediaStore) *Server {
	s := New(app, store)
	mux := s.HTTPHandler()
	app.Handle("POST /media", mux)
	app.Handle("GET /media/{filename}", mux)
	app.Handle("GET /media", mux)
	app.Handle("DELETE /media/{id}", mux)
	return s
}

// HTTPHandler returns an http.Handler that serves all four media routes.
// Mount it with [Register] or attach it manually via [forge.App.Handle].
func (s *Server) HTTPHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /media", s.handleUpload)
	mux.HandleFunc("GET /media/{filename}", s.handleServe)
	mux.HandleFunc("GET /media", s.handleList)
	mux.HandleFunc("DELETE /media/{id}", s.handleDelete)
	return mux
}

// ─── handlers ─────────────────────────────────────────────────────────────────

// handleUpload processes a multipart/form-data POST. Fields:
//
//   - file        — the file to upload (required)
//   - description — alt text / caption (required for images; WCAG 1.1.1)
//
// Returns 201 JSON on success.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	cfg := s.app.Config()
	user, ok := forge.VerifyBearerToken(r, cfg.Secret, cfg.TokenStore)
	if !ok || !user.HasRole(forge.Author) {
		forge.WriteError(w, r, forge.ErrUnauth)
		return
	}

	// Enforce size limit before reading the body.
	r.Body = http.MaxBytesReader(w, r.Body, s.maxSize)
	if err := r.ParseMultipartForm(s.maxSize); err != nil {
		if strings.Contains(err.Error(), "request body too large") || strings.Contains(err.Error(), "http: request body too large") {
			forge.WriteError(w, r, forge.ErrRequestTooLarge)
			return
		}
		forge.WriteError(w, r, forge.ErrBadRequest)
		return
	}

	// Read file field.
	fh, header, err := r.FormFile("file")
	if err != nil {
		forge.WriteError(w, r, forge.Err("file", "required"))
		return
	}
	defer fh.Close()

	data, err := io.ReadAll(fh)
	if err != nil {
		forge.WriteError(w, r, forge.ErrBadRequest)
		return
	}

	description := strings.TrimSpace(r.FormValue("description"))

	// Detect and validate MIME type.
	ext := strings.ToLower(filepath.Ext(header.Filename))
	mimeType, err := detectMIME(data, ext)
	if err != nil {
		forge.WriteError(w, r, err)
		return
	}

	mt := detectMediaType(mimeType)

	// WCAG 1.1.1 — alt text required for images.
	if mt == MediaTypeImage && description == "" {
		forge.WriteError(w, r, forge.Err("description", "required for image uploads"))
		return
	}

	// Generate a unique storage filename.
	filename, err := generateFilename(header.Filename)
	if err != nil {
		forge.WriteError(w, r, forge.ErrBadRequest)
		return
	}

	// Store the file.
	url, err := s.store.Store(filename, data)
	if err != nil {
		forge.WriteError(w, r, forge.ErrBadRequest)
		return
	}

	// Persist the record.
	id := newID()
	rec := MediaRecord{
		ID:               id,
		Filename:         filename,
		OriginalFilename: header.Filename,
		MediaType:        mt,
		MIMEType:         mimeType,
		Description:      description,
		SizeBytes:        int64(len(data)),
		UploadedAt:       time.Now().UTC(),
	}
	if err := insertMedia(s.db, rec); err != nil {
		// Best-effort cleanup — ignore delete error.
		_ = s.store.Delete(filename)
		forge.WriteError(w, r, forge.ErrBadRequest)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":         id,
		"url":        url,
		"media_type": string(mt),
		"mime_type":  mimeType,
	})
}

// handleServe serves a media file by its stored filename. No authentication
// required — uploaded files are publicly accessible by URL.
func (s *Server) handleServe(w http.ResponseWriter, r *http.Request) {
	filename := r.PathValue("filename")
	// Prevent path traversal.
	if strings.Contains(filename, "/") || strings.Contains(filename, "..") {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.dir, filename))
}

// handleList returns a JSON array of all media records. Supports optional
// ?type=image|video|audio|document|other filtering. Requires Editor role.
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	cfg := s.app.Config()
	user, ok := forge.VerifyBearerToken(r, cfg.Secret, cfg.TokenStore)
	if !ok || !user.HasRole(forge.Editor) {
		forge.WriteError(w, r, forge.ErrUnauth)
		return
	}

	filter := MediaType(r.URL.Query().Get("type"))
	records, err := listMedia(s.db, filter)
	if err != nil {
		forge.WriteError(w, r, forge.ErrBadRequest)
		return
	}

	// Attach computed URLs before serialising.
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	for i := range records {
		records[i].URL = baseURL + "/media/" + records[i].Filename
	}

	if records == nil {
		records = []MediaRecord{}
	}
	writeJSON(w, http.StatusOK, records)
}

// handleDelete removes a media record by ID and its underlying file.
// Requires Editor role. Returns 204 No Content on success.
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	cfg := s.app.Config()
	user, ok := forge.VerifyBearerToken(r, cfg.Secret, cfg.TokenStore)
	if !ok || !user.HasRole(forge.Editor) {
		forge.WriteError(w, r, forge.ErrUnauth)
		return
	}

	id := r.PathValue("id")

	// Fetch the record first so we know the filename to delete.
	rec, err := getMediaByID(s.db, id)
	if err != nil {
		forge.WriteError(w, r, err)
		return
	}

	if err := deleteMediaRecord(s.db, id); err != nil {
		forge.WriteError(w, r, err)
		return
	}

	// Best-effort: remove the file (non-fatal if already gone).
	_ = s.store.Delete(rec.Filename)

	w.WriteHeader(http.StatusNoContent)
}

// ─── ID generation ────────────────────────────────────────────────────────────

// newID generates a 16-byte (32 hex char) cryptographically random ID.
func newID() string {
	b := make([]byte, 16)
	if _, err := randRead(b); err != nil {
		panic(fmt.Sprintf("forgemedia: newID: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
