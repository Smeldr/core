// Package forgemedia provides media upload, storage, and serving for Forge
// applications. It is an optional submodule with zero additional dependencies.
//
// # Quick start
//
//	import forgemedia "forge-cms.dev/forge-media"
//
//	store := forgemedia.NewLocalMediaStore(app)
//	srv   := forgemedia.New(app, store)
//	srv.Register(app)
//
// Uploaded files are validated by magic-byte MIME detection, stored in the
// directory configured by [forge.Config.MediaPath] (default ./media), and
// served at GET /media/{filename}. All write operations require at least the
// Author role.
package forgemedia

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	forge "forge-cms.dev/forge"
)

// ─── Media types ─────────────────────────────────────────────────────────────

// MediaType classifies an uploaded file by broad category.
type MediaType string

const (
	// MediaTypeImage covers JPEG, PNG, GIF, WebP, and SVG files.
	MediaTypeImage MediaType = "image"
	// MediaTypeVideo covers MP4 and WebM files.
	MediaTypeVideo MediaType = "video"
	// MediaTypeAudio covers MP3, WAV, and OGG files.
	MediaTypeAudio MediaType = "audio"
	// MediaTypeDocument covers PDF files.
	MediaTypeDocument MediaType = "document"
	// MediaTypeOther covers any file type not in the above categories.
	MediaTypeOther MediaType = "other"
)

// ─── MediaRecord ─────────────────────────────────────────────────────────────

// MediaRecord is a row in the forge_media table. The URL field is computed
// from the application BaseURL at read time and is never stored in the database.
type MediaRecord struct {
	// ID is the unique identifier for this media record (UUID v4 hex string).
	ID string
	// Filename is the generated storage filename (timestamp_random_sanitized).
	Filename string
	// OriginalFilename is the filename as supplied by the uploader.
	OriginalFilename string
	// MediaType classifies the file (image, video, audio, document, other).
	MediaType MediaType
	// MIMEType is the detected MIME type (e.g. "image/jpeg").
	MIMEType string
	// Description is the alt text or caption supplied by the uploader.
	// Required for image uploads (WCAG 1.1.1).
	Description string
	// SizeBytes is the file size in bytes.
	SizeBytes int64
	// UploadedAt is the UTC time the file was stored.
	UploadedAt time.Time
	// URL is computed at read time from the application BaseURL.
	// It is not stored in the database.
	URL string
}

// ─── MediaStore interface ─────────────────────────────────────────────────────

// MediaStore persists and serves media files. Implement this interface to
// use a custom storage backend (e.g. S3, GCS) in place of [LocalMediaStore].
type MediaStore interface {
	// Store writes data to the underlying storage and returns the public URL.
	Store(filename string, data []byte) (url string, err error)
	// Delete removes a file from storage. Returns nil if the file is already absent.
	Delete(filename string) error
	// URL returns the public URL for a stored filename without performing I/O.
	URL(filename string) string
}

// ─── LocalMediaStore ─────────────────────────────────────────────────────────

// LocalMediaStore stores uploaded files in a local directory and serves them
// via the Forge HTTP handler. It implements [MediaStore].
type LocalMediaStore struct {
	dir     string
	baseURL string
}

// NewLocalMediaStore returns a LocalMediaStore configured from the application.
// It reads MediaPath from app.Config() (default "./media") and BaseURL for URL
// construction. The upload directory is not created here; it is created lazily
// on first upload.
func NewLocalMediaStore(app *forge.App) *LocalMediaStore {
	cfg := app.Config()
	dir := cfg.MediaPath
	if dir == "" {
		dir = "./media"
	}
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	return &LocalMediaStore{dir: dir, baseURL: baseURL}
}

// Store writes data to dir/filename and returns the public URL.
// Uses os.Root to sandbox all writes inside s.dir, preventing path traversal.
func (s *LocalMediaStore) Store(filename string, data []byte) (string, error) {
	if err := ensureDir(s.dir); err != nil {
		return "", fmt.Errorf("forgemedia: create upload directory: %w", err)
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return "", fmt.Errorf("forgemedia: open root: %w", err)
	}
	defer root.Close()
	f, err := root.Create(filename)
	if err != nil {
		return "", fmt.Errorf("forgemedia: write file: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return "", fmt.Errorf("forgemedia: write file: %w", err)
	}
	return s.URL(filename), nil
}

// Delete removes dir/filename. Returns nil if the file does not exist.
// Uses os.Root to sandbox the removal inside s.dir, preventing path traversal.
func (s *LocalMediaStore) Delete(filename string) error {
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer root.Close()
	if err := root.Remove(filename); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return nil
}

// URL returns the canonical public URL for filename without performing I/O.
func (s *LocalMediaStore) URL(filename string) string {
	return s.baseURL + "/media/" + filename
}

// ─── Filename generation ──────────────────────────────────────────────────────

// generateFilename produces a storage filename from the original upload name.
// Format: <unix-nanoseconds>_<12-hex-random>_<sanitized-original>
// The original name is lower-cased and non-alphanumeric characters (except
// dots and hyphens) are replaced with underscores. Leading dots are stripped.
func generateFilename(original string) (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("forgemedia: generate random suffix: %w", err)
	}
	rnd := hex.EncodeToString(b)
	ts := fmt.Sprintf("%d", time.Now().UnixNano())
	sanitized := sanitizeFilename(original)
	return ts + "_" + rnd + "_" + sanitized, nil
}

// sanitizeFilename lower-cases name, replaces disallowed characters with
// underscores, and strips leading dots.
func sanitizeFilename(name string) string {
	name = strings.ToLower(name)
	var b strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	result := strings.TrimLeft(b.String(), ".")
	if result == "" {
		result = "file"
	}
	return result
}

// ─── MIME detection ───────────────────────────────────────────────────────────

// extToMIME maps supported file extensions to their expected MIME type.
var extToMIME = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
	".svg":  "image/svg+xml",
	".mp4":  "video/mp4",
	".webm": "video/webm",
	".mp3":  "audio/mpeg",
	".wav":  "audio/wav",
	".ogg":  "audio/ogg",
	".pdf":  "application/pdf",
}

// extToLabel maps extensions to human-readable type names for error messages.
var extToLabel = map[string]string{
	".jpg":  "JPEG",
	".jpeg": "JPEG",
	".png":  "PNG",
	".gif":  "GIF",
	".webp": "WebP",
	".svg":  "SVG",
	".mp4":  "MP4",
	".webm": "WebM",
	".mp3":  "MP3",
	".wav":  "WAV",
	".ogg":  "OGG",
	".pdf":  "PDF",
}

// mimeToLabel maps MIME types to human-readable names for error messages.
var mimeToLabel = map[string]string{
	"image/jpeg":      "JPEG",
	"image/png":       "PNG",
	"image/gif":       "GIF",
	"image/webp":      "WebP",
	"image/svg+xml":   "SVG",
	"video/mp4":       "MP4",
	"video/webm":      "WebM",
	"audio/mpeg":      "MP3",
	"audio/wav":       "WAV",
	"audio/ogg":       "OGG",
	"application/pdf": "PDF",
}

// detectMIME detects the actual MIME type from the first bytes of data and
// validates it against the expected MIME type for the file extension.
// Returns an agent-actionable error on mismatch, e.g.:
//
//	"expected JPEG (from .jpg extension), got PNG content"
//
// The extension must be a known type (one of the keys in extToMIME).
func detectMIME(data []byte, ext string) (string, error) {
	ext = strings.ToLower(ext)
	expected, ok := extToMIME[ext]
	if !ok {
		return "", fmt.Errorf("forgemedia: unsupported file extension %q", ext)
	}

	actual := sniffMIME(data)
	if actual == "" {
		// Cannot detect from magic bytes — trust the extension.
		return expected, nil
	}

	if actual != expected {
		extLabel := extToLabel[ext]
		actualLabel, ok2 := mimeToLabel[actual]
		if !ok2 {
			actualLabel = actual
		}
		return "", forge.Err("file",
			fmt.Sprintf("expected %s (from %s extension), got %s content", extLabel, ext, actualLabel))
	}

	return expected, nil
}

// sniffMIME identifies common types from magic bytes. Returns "" if unknown.
func sniffMIME(data []byte) string {
	n := len(data)
	if n == 0 {
		return ""
	}

	// JPEG: FF D8 FF
	if n >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg"
	}
	// PNG: 89 50 4E 47 0D 0A 1A 0A
	if n >= 8 && data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G' {
		return "image/png"
	}
	// GIF: "GIF8"
	if n >= 4 && data[0] == 'G' && data[1] == 'I' && data[2] == 'F' && data[3] == '8' {
		return "image/gif"
	}
	// WebP: "RIFF" at 0–3 and "WEBP" at 8–11
	if n >= 12 &&
		data[0] == 'R' && data[1] == 'I' && data[2] == 'F' && data[3] == 'F' &&
		data[8] == 'W' && data[9] == 'E' && data[10] == 'B' && data[11] == 'P' {
		return "image/webp"
	}
	// PDF: "%PDF"
	if n >= 4 && data[0] == '%' && data[1] == 'P' && data[2] == 'D' && data[3] == 'F' {
		return "application/pdf"
	}
	// MP4: "ftyp" at bytes 4–7 (ISO Base Media File Format)
	if n >= 8 && data[4] == 'f' && data[5] == 't' && data[6] == 'y' && data[7] == 'p' {
		return "video/mp4"
	}
	// WebM: EBML magic 0x1A 0x45 0xDF 0xA3
	if n >= 4 && data[0] == 0x1A && data[1] == 0x45 && data[2] == 0xDF && data[3] == 0xA3 {
		return "video/webm"
	}
	// MP3: ID3 tag or sync word FF FB / FF F3 / FF E3
	if n >= 3 && data[0] == 'I' && data[1] == 'D' && data[2] == '3' {
		return "audio/mpeg"
	}
	if n >= 2 && data[0] == 0xFF && (data[1] == 0xFB || data[1] == 0xF3 || data[1] == 0xE3) {
		return "audio/mpeg"
	}
	// WAV: "RIFF" at 0–3 and "WAVE" at 8–11
	if n >= 12 &&
		data[0] == 'R' && data[1] == 'I' && data[2] == 'F' && data[3] == 'F' &&
		data[8] == 'W' && data[9] == 'A' && data[10] == 'V' && data[11] == 'E' {
		return "audio/wav"
	}
	// OGG: "OggS"
	if n >= 4 && data[0] == 'O' && data[1] == 'g' && data[2] == 'g' && data[3] == 'S' {
		return "audio/ogg"
	}
	// SVG: text containing "<svg" in first 512 bytes
	limit := n
	if limit > 512 {
		limit = 512
	}
	if strings.Contains(strings.ToLower(string(data[:limit])), "<svg") {
		return "image/svg+xml"
	}

	return ""
}

// detectMediaType classifies a MIME type into a MediaType constant.
func detectMediaType(mime string) MediaType {
	switch {
	case strings.HasPrefix(mime, "image/"):
		return MediaTypeImage
	case strings.HasPrefix(mime, "video/"):
		return MediaTypeVideo
	case strings.HasPrefix(mime, "audio/"):
		return MediaTypeAudio
	case mime == "application/pdf":
		return MediaTypeDocument
	default:
		return MediaTypeOther
	}
}

// ─── DB operations ────────────────────────────────────────────────────────────

// CreateMediaTable creates the forge_media table if it does not already exist.
// Call this once at application startup.
func CreateMediaTable(db forge.DB) error {
	_, err := db.ExecContext(context.Background(), `
		CREATE TABLE IF NOT EXISTS forge_media (
			id                TEXT PRIMARY KEY,
			filename          TEXT NOT NULL UNIQUE,
			original_filename TEXT NOT NULL,
			media_type        TEXT NOT NULL,
			mime_type         TEXT NOT NULL,
			description       TEXT NOT NULL DEFAULT '',
			size_bytes        INTEGER NOT NULL DEFAULT 0,
			uploaded_at       DATETIME NOT NULL
		)`)
	if err != nil {
		return fmt.Errorf("forgemedia: create table: %w", err)
	}
	return nil
}

// insertMedia inserts a MediaRecord into the forge_media table.
func insertMedia(db forge.DB, r MediaRecord) error {
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO forge_media
			(id, filename, original_filename, media_type, mime_type, description, size_bytes, uploaded_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Filename, r.OriginalFilename, string(r.MediaType),
		r.MIMEType, r.Description, r.SizeBytes, r.UploadedAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("forgemedia: insert media: %w", err)
	}
	return nil
}

// listMedia returns all media records, optionally filtered by MediaType.
// Pass an empty string to return all records.
func listMedia(db forge.DB, filter MediaType) ([]MediaRecord, error) {
	var (
		sqlrows *sql.Rows
		err     error
	)
	if filter == "" {
		sqlrows, err = db.QueryContext(context.Background(), `
			SELECT id, filename, original_filename, media_type, mime_type, description, size_bytes, uploaded_at
			FROM forge_media ORDER BY uploaded_at DESC`)
	} else {
		sqlrows, err = db.QueryContext(context.Background(), `
			SELECT id, filename, original_filename, media_type, mime_type, description, size_bytes, uploaded_at
			FROM forge_media WHERE media_type = ? ORDER BY uploaded_at DESC`, string(filter))
	}
	if err != nil {
		return nil, fmt.Errorf("forgemedia: list media: %w", err)
	}
	defer sqlrows.Close()

	var records []MediaRecord
	for sqlrows.Next() {
		var r MediaRecord
		var mt string
		if err := sqlrows.Scan(&r.ID, &r.Filename, &r.OriginalFilename, &mt,
			&r.MIMEType, &r.Description, &r.SizeBytes, &r.UploadedAt); err != nil {
			return nil, fmt.Errorf("forgemedia: scan media row: %w", err)
		}
		r.MediaType = MediaType(mt)
		records = append(records, r)
	}
	return records, sqlrows.Err()
}

// getMediaByID returns a single MediaRecord by its ID.
// Returns forge.ErrNotFound when no row exists.
func getMediaByID(db forge.DB, id string) (MediaRecord, error) {
	row := db.QueryRowContext(context.Background(), `
		SELECT id, filename, original_filename, media_type, mime_type, description, size_bytes, uploaded_at
		FROM forge_media WHERE id = ?`, id)

	var r MediaRecord
	var mt string
	err := row.Scan(&r.ID, &r.Filename, &r.OriginalFilename, &mt,
		&r.MIMEType, &r.Description, &r.SizeBytes, &r.UploadedAt)
	if err != nil {
		if isNoRows(err) {
			return MediaRecord{}, forge.ErrNotFound
		}
		return MediaRecord{}, fmt.Errorf("forgemedia: get media by id: %w", err)
	}
	r.MediaType = MediaType(mt)
	return r, nil
}

// deleteMediaRecord removes a forge_media row by ID.
// Returns forge.ErrNotFound when no row exists.
func deleteMediaRecord(db forge.DB, id string) error {
	res, err := db.ExecContext(context.Background(),
		`DELETE FROM forge_media WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("forgemedia: delete media record: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("forgemedia: rows affected: %w", err)
	}
	if n == 0 {
		return forge.ErrNotFound
	}
	return nil
}

// ─── HTTP helpers (http.ResponseWriter JSON) ──────────────────────────────────

// writeJSON writes v as JSON to w with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	encodeJSON(w, v)
}
