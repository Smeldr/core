//go:build integration

package pgx

import (
	"context"
	"testing"

	smeldr "smeldr.dev/core"
)

// ActorPost is a compiled type with a LastActor column.
type ActorPost struct {
	smeldr.Node
	Title     string
	LastActor string `json:"last_actor,omitempty" db:"last_actor"`
}

// On Postgres a compiled type's content update records its writer as
// last_actor, whatever the update names.
func TestPG_CompiledUpdateRecordsWriter(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE actor_posts (
		id TEXT PRIMARY KEY, slug TEXT NOT NULL UNIQUE, status TEXT NOT NULL DEFAULT 'draft',
		published_at TIMESTAMPTZ, scheduled_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL, rev INTEGER NOT NULL DEFAULT 0,
		title TEXT NOT NULL DEFAULT '', last_actor TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	repo := smeldr.NewSQLRepo[*ActorPost](db, smeldr.Table("actor_posts"))
	m := smeldr.NewModule((*ActorPost)(nil), smeldr.Repo(repo))

	alice := smeldr.NewContextWithUser(smeldr.User{ID: "alice", Roles: []smeldr.Role{smeldr.Editor}})
	bob := smeldr.NewContextWithUser(smeldr.User{ID: "bob", Roles: []smeldr.Role{smeldr.Editor}})
	item, err := m.MCPCreate(alice, map[string]any{"title": "First", "last_actor": "carol"})
	if err != nil {
		t.Fatalf("MCPCreate: %v", err)
	}
	slug := item.(*ActorPost).Slug
	if _, err := m.MCPUpdate(bob, slug, map[string]any{"title": "Edited", "last_actor": "carol"}); err != nil {
		t.Fatalf("MCPUpdate: %v", err)
	}
	var got string
	if err := db.QueryRowContext(ctx, `SELECT last_actor FROM actor_posts WHERE slug = $1`, slug).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "bob" {
		t.Errorf("last_actor = %q; want bob, the writer", got)
	}
}
