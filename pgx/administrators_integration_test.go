//go:build integration

package pgx

import (
	"context"
	"reflect"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// T944 (D154) on Postgres: a token with no grant learns who administers the
// instance, by name and class; an expired admin grant, a static administer
// grant and a legacy admin token role without a grant are left out.
func TestPG_Administrators(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	app, store := newPGApp(t, db)
	if err := smeldr.CreateGovernanceTables(db); err != nil {
		t.Fatal(err)
	}
	_ = app.Handler()
	if _, err := db.ExecContext(ctx, `CREATE TABLE smeldr_tokens (id TEXT PRIMARY KEY, name TEXT NOT NULL, role TEXT NOT NULL,
		expires_at TEXT NOT NULL, revoked_at TEXT, created_at TEXT NOT NULL, user_id TEXT, actor_class TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, u := range []struct{ user, role, class string }{{"boss", "editor", "human"}, {"bot", "editor", "job"}, {"old", "editor", "human"}, {"scoped", "editor", "human"}, {"legacy", "admin", "human"}} {
		if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_tokens (id, name, role, expires_at, created_at, user_id, actor_class) VALUES ($1, $2, $3, '2099-01-01T00:00:00Z', '2026-01-01T00:00:00Z', $2, $4)`,
			"fp-"+u.user, u.user, u.role, u.class); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.DefineRole(ctx, smeldr.RoleDefinition{Name: "type-admin", Operations: []string{"administer"}, ScopeMode: smeldr.ScopeStatic}); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	for _, g := range []smeldr.RoleGrant{
		{TokenID: "boss", RoleName: "admin"},
		{TokenID: "bot", RoleName: "admin"},
		{TokenID: "old", RoleName: "admin", ExpiresAt: &past},
		{TokenID: "scoped", RoleName: "type-admin", ScopeStatic: []string{"Decision:*"}},
	} {
		if _, err := store.Grant(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	nobody := smeldr.NewTestContext(smeldr.User{ID: "nobody"})
	got, err := app.Administrators(nobody)
	want := []smeldr.InstanceAdministrator{{Name: "boss", Class: "human"}, {Name: "bot", Class: "job"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("= %+v %v, want %+v", got, err, want)
	}
	if err := app.AuthorizeCapability(nobody, smeldr.ResourceItem, "administrators", smeldr.AuthTarget{}); err != nil {
		t.Errorf("capability for a token with no grant: %v", err)
	}
}
