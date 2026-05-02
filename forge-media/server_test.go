package forgemedia

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	forge "forge-cms.dev/forge"
)

// ─── test helpers ─────────────────────────────────────────────────────────────

// openServerTestDB returns an in-memory SQLite DB and creates the media table.
func openServerTestDB(t *testing.T) forge.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := CreateMediaTable(db); err != nil {
		t.Fatalf("CreateMediaTable: %v", err)
	}
	return db
}

var testSecret = []byte("test-secret-must-be-32-bytes!!!!!")

// newTestServer returns a Server wired to an in-memory DB and a temp-dir store.
func newTestServer(t *testing.T) (*Server, *forge.App) {
	t.Helper()
	dir := t.TempDir()
	db := openServerTestDB(t)

	app := forge.New(forge.MustConfig(forge.Config{
		BaseURL: "https://example.com",
		Secret:  testSecret,
		DB:      db,
	}))

	store := &LocalMediaStore{dir: dir, baseURL: "https://example.com"}
	s := &Server{
		app:     app,
		store:   store,
		db:      db,
		maxSize: 5 << 20,
		dir:     dir,
	}
	return s, app
}

// authorToken returns a signed bearer token with Author role.
func authorToken(t *testing.T) string {
	t.Helper()
	tok, err := forge.SignToken(forge.User{ID: "u1", Name: "Author", Roles: []forge.Role{forge.Author}}, string(testSecret), 0)
	if err != nil {
		t.Fatal(err)
	}
	return "Bearer " + tok
}

// editorToken returns a signed bearer token with Editor role.
func editorToken(t *testing.T) string {
	t.Helper()
	tok, err := forge.SignToken(forge.User{ID: "u2", Name: "Editor", Roles: []forge.Role{forge.Editor}}, string(testSecret), 0)
	if err != nil {
		t.Fatal(err)
	}
	return "Bearer " + tok
}

// buildUpload constructs a multipart/form-data request body.
func buildUpload(t *testing.T, filename string, data []byte, description string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatal(err)
	}
	if description != "" {
		if err := mw.WriteField("description", description); err != nil {
			t.Fatal(err)
		}
	}
	mw.Close()
	return &buf, mw.FormDataContentType()
}

// jpegMagic is a minimal JPEG header.
var jpegMagic = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F'}

// pngMagic is a minimal PNG header.
var pngMagic = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}

// ─── handleUpload tests ───────────────────────────────────────────────────────

func TestHandleUpload_success(t *testing.T) {
	s, _ := newTestServer(t)

	body, ct := buildUpload(t, "photo.jpg", jpegMagic, "a test image")
	req := httptest.NewRequest(http.MethodPost, "/media", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", authorToken(t))
	w := httptest.NewRecorder()

	s.handleUpload(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status: want 201, got %d — body: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	for _, key := range []string{"id", "url", "media_type", "mime_type"} {
		if resp[key] == "" || resp[key] == nil {
			t.Errorf("response missing %q field", key)
		}
	}
	if resp["media_type"] != "image" {
		t.Errorf("media_type: want image, got %v", resp["media_type"])
	}
}

func TestHandleUpload_missingDescriptionForImage(t *testing.T) {
	s, _ := newTestServer(t)

	// JPEG without description.
	body, ct := buildUpload(t, "photo.jpg", jpegMagic, "")
	req := httptest.NewRequest(http.MethodPost, "/media", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", authorToken(t))
	w := httptest.NewRecorder()

	s.handleUpload(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status: want 422, got %d — body: %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("description")) {
		t.Errorf("response should mention 'description', got: %s", w.Body.String())
	}
}

func TestHandleUpload_MIMEmismatch(t *testing.T) {
	s, _ := newTestServer(t)

	// PNG magic bytes but filename claims .jpg.
	body, ct := buildUpload(t, "photo.jpg", pngMagic, "mismatch")
	req := httptest.NewRequest(http.MethodPost, "/media", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", authorToken(t))
	w := httptest.NewRecorder()

	s.handleUpload(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status: want 422, got %d — body: %s", w.Code, w.Body.String())
	}
	body2 := w.Body.String()
	if !bytes.Contains([]byte(body2), []byte("JPEG")) || !bytes.Contains([]byte(body2), []byte("PNG")) {
		t.Errorf("error should name both types; got: %s", body2)
	}
}

func TestHandleUpload_unauthenticated(t *testing.T) {
	s, _ := newTestServer(t)

	body, ct := buildUpload(t, "photo.jpg", jpegMagic, "test")
	req := httptest.NewRequest(http.MethodPost, "/media", body)
	req.Header.Set("Content-Type", ct)
	// No Authorization header.
	w := httptest.NewRecorder()

	s.handleUpload(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status: want 401, got %d", w.Code)
	}
}

func TestHandleUpload_sizeLimitExceeded(t *testing.T) {
	s, _ := newTestServer(t)
	s.maxSize = 10 // 10 bytes — our JPEG magic is larger

	body, ct := buildUpload(t, "photo.jpg", jpegMagic, "test")
	req := httptest.NewRequest(http.MethodPost, "/media", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", authorToken(t))
	w := httptest.NewRecorder()

	s.handleUpload(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status: want 413, got %d — body: %s", w.Code, w.Body.String())
	}
}

// ─── handleServe tests ────────────────────────────────────────────────────────

func TestHandleServe_existingFile(t *testing.T) {
	s, _ := newTestServer(t)

	// Write a file directly to the store dir.
	content := []byte("fake image data")
	if err := os.WriteFile(s.dir+"/testfile.jpg", content, 0o644); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/media/testfile.jpg", nil)
	req.SetPathValue("filename", "testfile.jpg")
	w := httptest.NewRecorder()

	s.handleServe(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", w.Code)
	}
	got, _ := io.ReadAll(w.Body)
	if !bytes.Equal(got, content) {
		t.Errorf("body mismatch: want %q, got %q", content, got)
	}
}

func TestHandleServe_pathTraversal(t *testing.T) {
	s, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/media/../secret", nil)
	req.SetPathValue("filename", "../secret")
	w := httptest.NewRecorder()

	s.handleServe(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("path traversal: want 404, got %d", w.Code)
	}
}

// ─── handleList tests ─────────────────────────────────────────────────────────

func TestHandleList_returnsJSON(t *testing.T) {
	s, _ := newTestServer(t)

	// Insert two records directly.
	for i, rec := range []MediaRecord{
		{ID: "r1", Filename: "a.jpg", OriginalFilename: "a.jpg", MediaType: MediaTypeImage, MIMEType: "image/jpeg", SizeBytes: 1, UploadedAt: time.Now().UTC()},
		{ID: "r2", Filename: "b.pdf", OriginalFilename: "b.pdf", MediaType: MediaTypeDocument, MIMEType: "application/pdf", SizeBytes: 2, UploadedAt: time.Now().UTC()},
	} {
		if err := insertMedia(s.db, rec); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/media", nil)
	req.Header.Set("Authorization", editorToken(t))
	w := httptest.NewRecorder()

	s.handleList(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d — body: %s", w.Code, w.Body.String())
	}
	var records []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &records); err != nil {
		t.Fatalf("parse JSON: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("want 2 records, got %d", len(records))
	}
}

func TestHandleList_typeFilter(t *testing.T) {
	s, _ := newTestServer(t)

	for _, rec := range []MediaRecord{
		{ID: "i1", Filename: "img.jpg", OriginalFilename: "img.jpg", MediaType: MediaTypeImage, MIMEType: "image/jpeg", SizeBytes: 1, UploadedAt: time.Now().UTC()},
		{ID: "d1", Filename: "doc.pdf", OriginalFilename: "doc.pdf", MediaType: MediaTypeDocument, MIMEType: "application/pdf", SizeBytes: 2, UploadedAt: time.Now().UTC()},
	} {
		if err := insertMedia(s.db, rec); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/media?type=image", nil)
	req.Header.Set("Authorization", editorToken(t))
	w := httptest.NewRecorder()

	s.handleList(w, req)

	var records []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &records); err != nil {
		t.Fatalf("parse JSON: %v", err)
	}
	if len(records) != 1 {
		t.Errorf("filtered list: want 1 image, got %d", len(records))
	}
}

func TestHandleList_requiresEditorRole(t *testing.T) {
	s, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/media", nil)
	req.Header.Set("Authorization", authorToken(t)) // Author, not Editor
	w := httptest.NewRecorder()

	s.handleList(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 for Author on list, got %d", w.Code)
	}
}

// ─── handleDelete tests ───────────────────────────────────────────────────────

func TestHandleDelete_success(t *testing.T) {
	s, _ := newTestServer(t)

	// Create a file and DB record.
	rec := MediaRecord{
		ID: "del1", Filename: "todel.jpg", OriginalFilename: "todel.jpg",
		MediaType: MediaTypeImage, MIMEType: "image/jpeg",
		SizeBytes: 1, UploadedAt: time.Now().UTC(),
	}
	if err := insertMedia(s.db, rec); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.dir+"/todel.jpg", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/media/del1", nil)
	req.SetPathValue("id", "del1")
	req.Header.Set("Authorization", editorToken(t))
	w := httptest.NewRecorder()

	s.handleDelete(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status: want 204, got %d — body: %s", w.Code, w.Body.String())
	}

	// Record should be gone.
	if _, err := getMediaByID(s.db, "del1"); err != forge.ErrNotFound {
		t.Errorf("record should be deleted, got: %v", err)
	}
}

func TestHandleDelete_notFound(t *testing.T) {
	s, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodDelete, "/media/missing", nil)
	req.SetPathValue("id", "missing")
	req.Header.Set("Authorization", editorToken(t))
	w := httptest.NewRecorder()

	s.handleDelete(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestHandleDelete_requiresEditorRole(t *testing.T) {
	s, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodDelete, "/media/x", nil)
	req.SetPathValue("id", "x")
	req.Header.Set("Authorization", authorToken(t)) // Author only
	w := httptest.NewRecorder()

	s.handleDelete(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 for Author on delete, got %d", w.Code)
	}
}

// ─── compile-time interface check ─────────────────────────────────────────────

var _ forge.DB = (*sql.DB)(nil)
