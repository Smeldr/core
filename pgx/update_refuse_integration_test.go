//go:build integration

package pgx

import (
	"context"
	"errors"
	"testing"

	smeldr "smeldr.dev/core"
)

// An update that asks to change a compiled item's status is refused on
// Postgres before any statement, and the stored item is unchanged.
func TestPG_UpdateRefusesStatus(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE refuse_posts (
		id TEXT PRIMARY KEY, slug TEXT NOT NULL UNIQUE, status TEXT NOT NULL DEFAULT 'draft',
		published_at TIMESTAMPTZ, scheduled_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL, rev INTEGER NOT NULL DEFAULT 0,
		title TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	repo := smeldr.NewSQLRepo[*EventPost](db, smeldr.Table("refuse_posts"))
	m := smeldr.NewModule((*EventPost)(nil), smeldr.Repo(repo))
	if err := repo.Save(ctx, &EventPost{Node: smeldr.Node{ID: "rp-1", Slug: "rp-1", Status: smeldr.Draft}, Title: "Before"}); err != nil {
		t.Fatal(err)
	}
	caller := smeldr.NewContextWithUser(smeldr.User{ID: "pg-user", Roles: []smeldr.Role{smeldr.Editor}})
	_, err := m.MCPUpdate(caller, "rp-1", map[string]any{"status": "published", "Title": "After"})
	var ve *smeldr.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v; want a ValidationError", err)
	}
	var status, title string
	if err := db.QueryRowContext(ctx, `SELECT status, title FROM refuse_posts WHERE id = 'rp-1'`).Scan(&status, &title); err != nil {
		t.Fatal(err)
	}
	if status != "draft" || title != "Before" {
		t.Errorf("stored = %q, %q; want draft, Before", status, title)
	}
}
