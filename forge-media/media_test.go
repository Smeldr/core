package forgemedia

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	forge "forge-cms.dev/forge"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// openTestDB returns an in-memory SQLite database for tests.
func openTestDB(t *testing.T) forge.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// newTestApp returns a minimal *forge.App for testing store construction.
func newTestApp(t *testing.T, mediaPath, baseURL string) *forge.App {
	t.Helper()
	return forge.New(forge.MustConfig(forge.Config{
		BaseURL:   baseURL,
		Secret:    []byte("test-secret-32-bytes-minimum!!"),
		MediaPath: mediaPath,
	}))
}

// ─── generateFilename ────────────────────────────────────────────────────────

func TestGenerateFilename_format(t *testing.T) {
	name, err := generateFilename("hello world.jpg")
	if err != nil {
		t.Fatal(err)
	}
	// Must contain at least two underscores separating the three parts.
	parts := strings.SplitN(name, "_", 3)
	if len(parts) < 3 {
		t.Fatalf("expected at least 3 underscore-separated parts, got %q", name)
	}
	// Timestamp must be non-empty digits.
	for _, c := range parts[0] {
		if c < '0' || c > '9' {
			t.Errorf("timestamp part %q contains non-digit character %q", parts[0], c)
		}
	}
	// Random part must be 12 hex chars (6 bytes).
	if len(parts[1]) != 12 {
		t.Errorf("random part length: want 12, got %d (%q)", len(parts[1]), parts[1])
	}
}

func TestGenerateFilename_unique(t *testing.T) {
	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		name, err := generateFilename("photo.jpg")
		if err != nil {
			t.Fatal(err)
		}
		if seen[name] {
			t.Fatalf("duplicate filename generated: %q", name)
		}
		seen[name] = true
	}
}

func TestGenerateFilename_sanitize(t *testing.T) {
	cases := []struct {
		input string
		// The sanitized name should contain the want substring.
		want string
	}{
		{"My Photo.jpg", "my_photo.jpg"},
		{"..hidden", "hidden"},
		{"Résumé.pdf", "r_sum_.pdf"},
		{"file name (1).png", "file_name__1_.png"},
	}
	for _, tc := range cases {
		name, err := generateFilename(tc.input)
		if err != nil {
			t.Fatalf("generateFilename(%q): %v", tc.input, err)
		}
		if !strings.HasSuffix(name, tc.want) {
			t.Errorf("generateFilename(%q): suffix want %q, got %q", tc.input, tc.want, name)
		}
	}
}

// ─── detectMIME ──────────────────────────────────────────────────────────────

func TestDetectMIME_jpeg(t *testing.T) {
	magic := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00}
	mime, err := detectMIME(magic, ".jpg")
	if err != nil {
		t.Fatal(err)
	}
	if mime != "image/jpeg" {
		t.Errorf("want image/jpeg, got %q", mime)
	}
}

func TestDetectMIME_png(t *testing.T) {
	magic := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	mime, err := detectMIME(magic, ".png")
	if err != nil {
		t.Fatal(err)
	}
	if mime != "image/png" {
		t.Errorf("want image/png, got %q", mime)
	}
}

func TestDetectMIME_gif(t *testing.T) {
	magic := []byte{'G', 'I', 'F', '8', '9', 'a'}
	mime, err := detectMIME(magic, ".gif")
	if err != nil {
		t.Fatal(err)
	}
	if mime != "image/gif" {
		t.Errorf("want image/gif, got %q", mime)
	}
}

func TestDetectMIME_webp(t *testing.T) {
	magic := []byte{'R', 'I', 'F', 'F', 0, 0, 0, 0, 'W', 'E', 'B', 'P'}
	mime, err := detectMIME(magic, ".webp")
	if err != nil {
		t.Fatal(err)
	}
	if mime != "image/webp" {
		t.Errorf("want image/webp, got %q", mime)
	}
}

func TestDetectMIME_pdf(t *testing.T) {
	magic := []byte{'%', 'P', 'D', 'F', '-', '1', '.', '4'}
	mime, err := detectMIME(magic, ".pdf")
	if err != nil {
		t.Fatal(err)
	}
	if mime != "application/pdf" {
		t.Errorf("want application/pdf, got %q", mime)
	}
}

func TestDetectMIME_svg(t *testing.T) {
	data := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>`)
	mime, err := detectMIME(data, ".svg")
	if err != nil {
		t.Fatal(err)
	}
	if mime != "image/svg+xml" {
		t.Errorf("want image/svg+xml, got %q", mime)
	}
}

func TestDetectMIME_mismatch(t *testing.T) {
	// PNG magic bytes but claimed as .jpg
	magic := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	_, err := detectMIME(magic, ".jpg")
	if err == nil {
		t.Fatal("expected mismatch error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "JPEG") || !strings.Contains(msg, "PNG") {
		t.Errorf("error should name both types; got: %v", err)
	}
}

func TestDetectMIME_unknownExtension(t *testing.T) {
	_, err := detectMIME([]byte("data"), ".xyz")
	if err == nil {
		t.Fatal("expected error for unknown extension, got nil")
	}
}

// ─── LocalMediaStore ─────────────────────────────────────────────────────────

func TestLocalMediaStore_storeAndURL(t *testing.T) {
	dir := t.TempDir()
	app := newTestApp(t, dir, "https://example.com")
	store := NewLocalMediaStore(app)

	url, err := store.Store("test.jpg", []byte("fake-data"))
	if err != nil {
		t.Fatal(err)
	}

	want := "https://example.com/media/test.jpg"
	if url != want {
		t.Errorf("URL: want %q, got %q", want, url)
	}

	// File must exist on disk.
	if _, err := os.Stat(filepath.Join(dir, "test.jpg")); err != nil {
		t.Errorf("file not found after Store: %v", err)
	}
}

func TestLocalMediaStore_delete(t *testing.T) {
	dir := t.TempDir()
	app := newTestApp(t, dir, "https://example.com")
	store := NewLocalMediaStore(app)

	if _, err := store.Store("del.jpg", []byte("data")); err != nil {
		t.Fatal(err)
	}

	if err := store.Delete("del.jpg"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "del.jpg")); !os.IsNotExist(err) {
		t.Error("file should not exist after Delete")
	}

	// Delete of missing file must return nil.
	if err := store.Delete("del.jpg"); err != nil {
		t.Errorf("second Delete should be nil, got: %v", err)
	}
}

func TestLocalMediaStore_defaultMediaPath(t *testing.T) {
	// When MediaPath is empty the store should default to "./media".
	app := newTestApp(t, "", "https://example.com")
	store := NewLocalMediaStore(app)
	if store.dir != "./media" {
		t.Errorf("default dir: want ./media, got %q", store.dir)
	}
}

// ─── DB operations ───────────────────────────────────────────────────────────

func TestCreateMediaTable(t *testing.T) {
	db := openTestDB(t)
	if err := CreateMediaTable(db); err != nil {
		t.Fatalf("CreateMediaTable: %v", err)
	}
	// Idempotent — second call must not error.
	if err := CreateMediaTable(db); err != nil {
		t.Fatalf("second CreateMediaTable: %v", err)
	}
}

func TestListMedia_and_insert(t *testing.T) {
	db := openTestDB(t)
	if err := CreateMediaTable(db); err != nil {
		t.Fatal(err)
	}

	records := []MediaRecord{
		{ID: "id1", Filename: "a.jpg", OriginalFilename: "a.jpg", MediaType: MediaTypeImage, MIMEType: "image/jpeg", Description: "first", SizeBytes: 100, UploadedAt: time.Now().UTC()},
		{ID: "id2", Filename: "b.png", OriginalFilename: "b.png", MediaType: MediaTypeImage, MIMEType: "image/png", Description: "second", SizeBytes: 200, UploadedAt: time.Now().UTC()},
		{ID: "id3", Filename: "c.pdf", OriginalFilename: "c.pdf", MediaType: MediaTypeDocument, MIMEType: "application/pdf", Description: "doc", SizeBytes: 300, UploadedAt: time.Now().UTC()},
	}
	for _, r := range records {
		if err := insertMedia(db, r); err != nil {
			t.Fatalf("insertMedia %q: %v", r.ID, err)
		}
	}

	all, err := listMedia(db, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Errorf("list all: want 3, got %d", len(all))
	}

	images, err := listMedia(db, MediaTypeImage)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 2 {
		t.Errorf("list images: want 2, got %d", len(images))
	}

	docs, err := listMedia(db, MediaTypeDocument)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Errorf("list docs: want 1, got %d", len(docs))
	}
}

func TestGetMediaByID(t *testing.T) {
	db := openTestDB(t)
	if err := CreateMediaTable(db); err != nil {
		t.Fatal(err)
	}

	r := MediaRecord{
		ID: "abc", Filename: "x.jpg", OriginalFilename: "x.jpg",
		MediaType: MediaTypeImage, MIMEType: "image/jpeg",
		Description: "test", SizeBytes: 42, UploadedAt: time.Now().UTC(),
	}
	if err := insertMedia(db, r); err != nil {
		t.Fatal(err)
	}

	got, err := getMediaByID(db, "abc")
	if err != nil {
		t.Fatal(err)
	}
	if got.Filename != "x.jpg" {
		t.Errorf("Filename: want x.jpg, got %q", got.Filename)
	}

	_, err = getMediaByID(db, "missing")
	if err != forge.ErrNotFound {
		t.Errorf("missing ID: want ErrNotFound, got %v", err)
	}
}

func TestDeleteMediaRecord(t *testing.T) {
	db := openTestDB(t)
	if err := CreateMediaTable(db); err != nil {
		t.Fatal(err)
	}

	r := MediaRecord{
		ID: "del1", Filename: "d.jpg", OriginalFilename: "d.jpg",
		MediaType: MediaTypeImage, MIMEType: "image/jpeg",
		SizeBytes: 1, UploadedAt: time.Now().UTC(),
	}
	if err := insertMedia(db, r); err != nil {
		t.Fatal(err)
	}

	if err := deleteMediaRecord(db, "del1"); err != nil {
		t.Fatalf("deleteMediaRecord: %v", err)
	}

	if err := deleteMediaRecord(db, "del1"); err != forge.ErrNotFound {
		t.Errorf("second delete: want ErrNotFound, got %v", err)
	}
}

// ─── compile-time: confirm forge.DB is satisfied by *sql.DB ─────────────────

var _ forge.DB = (*sql.DB)(nil)

// ─── queryContext nil-context guard ──────────────────────────────────────────
// Verify that context.Background() is safe in this context (no-op test).
func TestContextBackground_nonNil(t *testing.T) {
	ctx := context.Background()
	if ctx == nil {
		t.Error("context.Background() returned nil")
	}
}
