package smeldr

import (
	"context"
	"database/sql"
)

// PageMeta holds operator-configured SEO overrides for a specific URL path.
// The zero value represents "no override configured" — a missing row in the
// database is not an error, it simply means no operator override exists.
type PageMeta struct {
	// Path is the URL path this entry applies to (e.g. "/posts" or "/about").
	Path string

	// MetaTitle overrides the <title> and og:title for this path.
	MetaTitle string

	// Description overrides the meta description and og:description for this path.
	Description string

	// OGImage overrides the og:image URL for this path.
	OGImage string
}

// PageMetaStore persists per-path SEO overrides in the smeldr_page_meta table.
//
// Create a store with [NewPageMetaStore] and call [CreatePageMetaTable] to
// initialise the table. Wire the store into the application with [App.PageMeta]:
//
//	store := smeldr.NewPageMetaStore(db)
//	smeldr.CreatePageMetaTable(db)  // call once at startup
//	app.PageMeta(store)
type PageMetaStore struct {
	db DB
}

// NewPageMetaStore returns a [PageMetaStore] backed by the provided database.
func NewPageMetaStore(db DB) *PageMetaStore {
	return &PageMetaStore{db: db}
}

// CreatePageMetaTable creates the smeldr_page_meta table if it does not exist.
// It is idempotent — safe to call on every application boot.
func CreatePageMetaTable(db DB) error {
	_, err := db.ExecContext(context.Background(), `
CREATE TABLE IF NOT EXISTS smeldr_page_meta (
    path             TEXT PRIMARY KEY NOT NULL,
    meta_title       TEXT NOT NULL DEFAULT '',
    meta_description TEXT NOT NULL DEFAULT '',
    og_image         TEXT NOT NULL DEFAULT ''
)`)
	return err
}

// Set upserts the SEO overrides for path. Any combination of title, description,
// and ogImage may be empty strings — empty values are stored as-is and will not
// override the global fallback.
func (s *PageMetaStore) Set(ctx context.Context, path, title, description, ogImage string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO smeldr_page_meta
		 (path, meta_title, meta_description, og_image)
		 VALUES (?, ?, ?, ?)`,
		path, title, description, ogImage)
	return err
}

// Get returns the stored overrides for path. If no row exists for path,
// Get returns a zero [PageMeta] and a nil error — a missing row is not an error.
// The caller may check meta.Path != "" to distinguish present from absent.
func (s *PageMetaStore) Get(ctx context.Context, path string) (PageMeta, error) {
	var m PageMeta
	err := s.db.QueryRowContext(ctx,
		`SELECT path, meta_title, meta_description, og_image
		 FROM smeldr_page_meta WHERE path = ?`,
		path).Scan(&m.Path, &m.MetaTitle, &m.Description, &m.OGImage)
	if err == sql.ErrNoRows {
		return PageMeta{}, nil
	}
	return m, err
}

// Delete removes the overrides for path. It is a no-op if path has no stored
// entry.
func (s *PageMetaStore) Delete(ctx context.Context, path string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM smeldr_page_meta WHERE path = ?`, path)
	return err
}

// List returns all stored path overrides, ordered by path.
func (s *PageMetaStore) List(ctx context.Context) ([]PageMeta, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT path, meta_title, meta_description, og_image
		 FROM smeldr_page_meta ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var metas []PageMeta
	for rows.Next() {
		var m PageMeta
		if err := rows.Scan(&m.Path, &m.MetaTitle, &m.Description, &m.OGImage); err != nil {
			return nil, err
		}
		metas = append(metas, m)
	}
	return metas, rows.Err()
}
