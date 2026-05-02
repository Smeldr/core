package forgemedia

import (
	"database/sql"
	"encoding/base64"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	forge "forge-cms.dev/forge"
)

// authorCtx returns a test Context with Author role.
func authorCtx(t *testing.T) forge.Context {
	t.Helper()
	return forge.NewTestContext(forge.User{
		ID:    "a1",
		Name:  "Author",
		Roles: []forge.Role{forge.Author},
	})
}

// editorCtx returns a test Context with Editor role.
func editorCtx(t *testing.T) forge.Context {
	t.Helper()
	return forge.NewTestContext(forge.User{
		ID:    "e1",
		Name:  "Editor",
		Roles: []forge.Role{forge.Editor},
	})
}

// newMCPServer returns a *Server wired to an in-memory DB and a temp-dir store.
func newMCPServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := CreateMediaTable(db); err != nil {
		t.Fatalf("CreateMediaTable: %v", err)
	}
	app := forge.New(forge.MustConfig(forge.Config{
		BaseURL: "https://example.com",
		Secret:  []byte("test-secret-must-be-32-bytes!!!!!"),
		DB:      db,
	}))
	store := &LocalMediaStore{dir: dir, baseURL: "https://example.com"}
	return &Server{app: app, store: store, db: db, maxSize: 5 << 20, dir: dir}
}

// ─── MCPMeta ──────────────────────────────────────────────────────────────────

func TestMCPMeta(t *testing.T) {
	s := newMCPServer(t)
	meta := s.MCPMeta()
	if meta.TypeName != "File" {
		t.Errorf("TypeName: want File, got %q", meta.TypeName)
	}
	if meta.Prefix != "/media" {
		t.Errorf("Prefix: want /media, got %q", meta.Prefix)
	}
	if len(meta.Operations) != 2 {
		t.Errorf("Operations: want 2, got %d", len(meta.Operations))
	}
}

// ─── MCPCreate ────────────────────────────────────────────────────────────────

func TestMCPCreate_success(t *testing.T) {
	s := newMCPServer(t)

	data := base64.StdEncoding.EncodeToString(jpegMagic)
	fields := map[string]any{
		"filename":    "photo.jpg",
		"data":        data,
		"description": "a test image",
	}

	result, err := s.MCPCreate(authorCtx(t), fields)
	if err != nil {
		t.Fatalf("MCPCreate: %v", err)
	}
	rec, ok := result.(MediaRecord)
	if !ok {
		t.Fatalf("result is %T, want MediaRecord", result)
	}
	if rec.ID == "" {
		t.Error("ID should be non-empty")
	}
	if rec.MediaType != MediaTypeImage {
		t.Errorf("MediaType: want image, got %q", rec.MediaType)
	}
	if rec.URL == "" {
		t.Error("URL should be non-empty")
	}
}

func TestMCPCreate_missingFilename(t *testing.T) {
	s := newMCPServer(t)
	_, err := s.MCPCreate(authorCtx(t), map[string]any{
		"data":        base64.StdEncoding.EncodeToString(jpegMagic),
		"description": "desc",
	})
	if err == nil {
		t.Fatal("expected error for missing filename")
	}
}

func TestMCPCreate_invalidBase64(t *testing.T) {
	s := newMCPServer(t)
	_, err := s.MCPCreate(authorCtx(t), map[string]any{
		"filename":    "photo.jpg",
		"data":        "!!!not-base64!!!",
		"description": "desc",
	})
	if err == nil {
		t.Fatal("expected error for invalid base64")
	}
}

func TestMCPCreate_missingDescriptionForImage(t *testing.T) {
	s := newMCPServer(t)
	data := base64.StdEncoding.EncodeToString(jpegMagic)
	_, err := s.MCPCreate(authorCtx(t), map[string]any{
		"filename": "photo.jpg",
		"data":     data,
		// no description
	})
	if err == nil {
		t.Fatal("expected error for missing image description")
	}
}

// ─── MCPList ──────────────────────────────────────────────────────────────────

func TestMCPList_returnsAll(t *testing.T) {
	s := newMCPServer(t)

	for _, rec := range []MediaRecord{
		{ID: "m1", Filename: "a.jpg", OriginalFilename: "a.jpg", MediaType: MediaTypeImage, MIMEType: "image/jpeg", SizeBytes: 1, UploadedAt: time.Now().UTC()},
		{ID: "m2", Filename: "b.pdf", OriginalFilename: "b.pdf", MediaType: MediaTypeDocument, MIMEType: "application/pdf", SizeBytes: 2, UploadedAt: time.Now().UTC()},
	} {
		if err := insertMedia(s.db, rec); err != nil {
			t.Fatal(err)
		}
	}

	items, err := s.MCPList(editorCtx(t))
	if err != nil {
		t.Fatalf("MCPList: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("want 2 items, got %d", len(items))
	}
}

func TestMCPList_statusIgnored(t *testing.T) {
	s := newMCPServer(t)
	if err := insertMedia(s.db, MediaRecord{
		ID: "x1", Filename: "x.jpg", OriginalFilename: "x.jpg",
		MediaType: MediaTypeImage, MIMEType: "image/jpeg",
		SizeBytes: 1, UploadedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	// Status filter is ignored for media — all records returned regardless.
	items, err := s.MCPList(editorCtx(t), forge.Draft)
	if err != nil {
		t.Fatalf("MCPList: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("want 1 item, got %d", len(items))
	}
}

// ─── MCPGet ───────────────────────────────────────────────────────────────────

func TestMCPGet_found(t *testing.T) {
	s := newMCPServer(t)
	if err := insertMedia(s.db, MediaRecord{
		ID: "g1", Filename: "g.jpg", OriginalFilename: "g.jpg",
		MediaType: MediaTypeImage, MIMEType: "image/jpeg",
		SizeBytes: 1, UploadedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	item, err := s.MCPGet(editorCtx(t), "g1")
	if err != nil {
		t.Fatalf("MCPGet: %v", err)
	}
	rec := item.(MediaRecord)
	if rec.ID != "g1" {
		t.Errorf("ID: want g1, got %q", rec.ID)
	}
}

func TestMCPGet_notFound(t *testing.T) {
	s := newMCPServer(t)
	_, err := s.MCPGet(editorCtx(t), "missing")
	if err != forge.ErrNotFound {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

// ─── MCPDelete ────────────────────────────────────────────────────────────────

func TestMCPDelete_success(t *testing.T) {
	s := newMCPServer(t)
	if err := insertMedia(s.db, MediaRecord{
		ID: "d1", Filename: "d.jpg", OriginalFilename: "d.jpg",
		MediaType: MediaTypeImage, MIMEType: "image/jpeg",
		SizeBytes: 1, UploadedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.MCPDelete(editorCtx(t), "d1"); err != nil {
		t.Fatalf("MCPDelete: %v", err)
	}

	if _, err := getMediaByID(s.db, "d1"); err != forge.ErrNotFound {
		t.Errorf("record should be gone, got: %v", err)
	}
}

func TestMCPDelete_notFound(t *testing.T) {
	s := newMCPServer(t)
	if err := s.MCPDelete(editorCtx(t), "missing"); err != forge.ErrNotFound {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

// ─── MCPUpdate / not-supported ops ───────────────────────────────────────────

func TestMCPUpdate_notSupported(t *testing.T) {
	s := newMCPServer(t)
	_, err := s.MCPUpdate(editorCtx(t), "any", map[string]any{"filename": "x.jpg"})
	if err == nil {
		t.Fatal("MCPUpdate should return an error")
	}
}

func TestMCPPublish_notSupported(t *testing.T) {
	s := newMCPServer(t)
	if err := s.MCPPublish(editorCtx(t), "any"); err == nil {
		t.Fatal("MCPPublish should return an error")
	}
}

func TestMCPSchedule_notSupported(t *testing.T) {
	s := newMCPServer(t)
	if err := s.MCPSchedule(editorCtx(t), "any", time.Now()); err == nil {
		t.Fatal("MCPSchedule should return an error")
	}
}

func TestMCPArchive_notSupported(t *testing.T) {
	s := newMCPServer(t)
	if err := s.MCPArchive(editorCtx(t), "any"); err == nil {
		t.Fatal("MCPArchive should return an error")
	}
}

// ─── GetSlug ──────────────────────────────────────────────────────────────────

func TestGetSlug(t *testing.T) {
	r := MediaRecord{ID: "slug-id"}
	if r.GetSlug() != "slug-id" {
		t.Errorf("GetSlug: want slug-id, got %q", r.GetSlug())
	}
}

// ─── compile-time interface check ─────────────────────────────────────────────

var _ forge.MCPModule = (*Server)(nil)
var _ = (*sql.DB)(nil)
