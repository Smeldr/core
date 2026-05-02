package forgemedia

import (
	"encoding/base64"
	"path/filepath"
	"time"

	forge "forge-cms.dev/forge"
)

// ─── MCPModule interface implementation ───────────────────────────────────────

// MCPMeta returns the MCP registration metadata for the media Server.
// TypeName is "File"; Prefix is "/media". Both MCPRead and MCPWrite are
// declared so that forge-mcp exposes media as resources and generates
// tools for create and delete.
func (s *Server) MCPMeta() forge.MCPMeta {
	return forge.MCPMeta{
		Prefix:     "/media",
		TypeName:   "File",
		Operations: []forge.MCPOperation{forge.MCPRead, forge.MCPWrite},
	}
}

// MCPSchema returns the field schema for the File content type. Three fields
// are exposed:
//
//   - filename   (string, required) — original filename including extension
//   - data       (string, required) — base64-encoded file bytes
//   - description (string)          — human-readable description; required by the
//     server for image files (WCAG guidance)
func (s *Server) MCPSchema() []forge.MCPField {
	return []forge.MCPField{
		{
			Name:        "Filename",
			JSONName:    "filename",
			Type:        "string",
			Required:    true,
			Description: "Original filename including extension, e.g. photo.jpg",
		},
		{
			Name:        "Data",
			JSONName:    "data",
			Type:        "string",
			Format:      "",
			Required:    true,
			Description: "Base64-encoded file bytes (standard or raw URL encoding).",
		},
		{
			Name:        "Description",
			JSONName:    "description",
			Type:        "string",
			Format:      "markdown",
			Required:    false,
			Description: "Alt text describing the image content for screen readers. Required for images (WCAG 1.1.1).",
		},
	}
}

// MCPList returns all media records as []any. The status filter is ignored —
// media files have no lifecycle and are always returned regardless of status.
func (s *Server) MCPList(_ forge.Context, _ ...forge.Status) ([]any, error) {
	records, err := listMedia(s.db, "")
	if err != nil {
		return nil, err
	}
	out := make([]any, len(records))
	for i, r := range records {
		r.URL = s.store.URL(r.Filename)
		out[i] = r
	}
	return out, nil
}

// MCPGet returns the MediaRecord for the given slug (= ID).
// Returns [forge.ErrNotFound] when no record exists with that ID.
func (s *Server) MCPGet(_ forge.Context, slug string) (any, error) {
	r, err := getMediaByID(s.db, slug)
	if err != nil {
		return nil, err
	}
	r.URL = s.store.URL(r.Filename)
	return r, nil
}

// MCPCreate uploads a new file from the fields map. Required fields:
//   - "filename" (string) — original filename including extension
//   - "data"     (string) — base64-encoded file bytes
//
// Optional:
//   - "description" (string) — required when the file is an image type
func (s *Server) MCPCreate(_ forge.Context, fields map[string]any) (any, error) {
	// Extract filename.
	origFilename, _ := fields["filename"].(string)
	if origFilename == "" {
		return nil, forge.Err("filename", "required")
	}

	// Extract and decode base64 data.
	dataB64, _ := fields["data"].(string)
	if dataB64 == "" {
		return nil, forge.Err("data", "required")
	}
	data, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		// Try raw URL encoding (no padding).
		data, err = base64.RawURLEncoding.DecodeString(dataB64)
		if err != nil {
			return nil, forge.Err("data", "must be base64-encoded file bytes")
		}
	}

	description, _ := fields["description"].(string)

	ext := filepath.Ext(origFilename)
	mime, err := detectMIME(data, ext)
	if err != nil {
		return nil, err
	}

	mt := detectMediaType(mime)
	if mt == MediaTypeImage && description == "" {
		return nil, forge.Err("description", "required for image uploads")
	}

	filename, err := generateFilename(origFilename)
	if err != nil {
		return nil, err
	}

	url, err := s.store.Store(filename, data)
	if err != nil {
		return nil, err
	}

	id := newID()
	rec := MediaRecord{
		ID:               id,
		Filename:         filename,
		OriginalFilename: origFilename,
		MediaType:        mt,
		MIMEType:         mime,
		Description:      description,
		SizeBytes:        int64(len(data)),
		UploadedAt:       time.Now().UTC(),
		URL:              url,
	}
	if err := insertMedia(s.db, rec); err != nil {
		// Best-effort cleanup: remove stored file.
		_ = s.store.Delete(filename)
		return nil, err
	}
	return rec, nil
}

// MCPUpdate always returns an error — media files cannot be updated in place.
// The caller should delete the existing record and re-upload.
func (s *Server) MCPUpdate(_ forge.Context, _ string, _ map[string]any) (any, error) {
	return nil, forge.ErrBadRequest
}

// MCPPublish always returns an error — media files have no lifecycle.
func (s *Server) MCPPublish(_ forge.Context, _ string) error {
	return forge.ErrBadRequest
}

// MCPSchedule always returns an error — media files have no lifecycle.
func (s *Server) MCPSchedule(_ forge.Context, _ string, _ time.Time) error {
	return forge.ErrBadRequest
}

// MCPArchive always returns an error — media files have no lifecycle.
func (s *Server) MCPArchive(_ forge.Context, _ string) error {
	return forge.ErrBadRequest
}

// MCPDelete permanently deletes the media record and its stored file.
// Returns [forge.ErrNotFound] when no record exists with the given slug (= ID).
func (s *Server) MCPDelete(_ forge.Context, slug string) error {
	r, err := getMediaByID(s.db, slug)
	if err != nil {
		return err
	}
	if err := deleteMediaRecord(s.db, slug); err != nil {
		return err
	}
	// Best-effort: file removal failure is not surfaced to the caller.
	_ = s.store.Delete(r.Filename)
	return nil
}

// GetSlug returns the ID of the MediaRecord, satisfying the slugger interface
// used by forge-mcp's allResources to build resource URIs.
func (r MediaRecord) GetSlug() string { return r.ID }
