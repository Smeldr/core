//go:build integration

package pgx

import (
	"context"
	"errors"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// Classified tokens on Postgres: the actor_class column added to a table that
// predates it, a refused classified mint before that, then mint and list.
func TestPG_ClassifiedTokens(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE smeldr_tokens (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, role TEXT NOT NULL,
		expires_at TEXT NOT NULL, revoked_at TEXT, created_at TEXT NOT NULL)`); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	ts := smeldr.NewTokenStore(db, pgTestSecret)

	// Before the columns exist: an unclassified mint works, a classified one is refused.
	if _, err := ts.Create(ctx, "old", "editor", time.Hour); err != nil {
		t.Fatalf("unclassified mint on the legacy table: %v", err)
	}
	var ve *smeldr.ValidationError
	if _, _, err := ts.CreateClassified(ctx, "bot", "editor", smeldr.Agent, time.Hour); !errors.As(err, &ve) {
		t.Fatalf("classified mint on a table without actor_class = %v, want a ValidationError", err)
	}

	if err := smeldr.EnsureTokenUserIDColumn(ctx, db); err != nil {
		t.Fatalf("EnsureTokenUserIDColumn: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := smeldr.EnsureTokenActorClassColumn(ctx, db); err != nil {
			t.Fatalf("EnsureTokenActorClassColumn call %d: %v", i+1, err)
		}
	}

	for _, c := range []struct {
		name  string
		class smeldr.Role
	}{{"bot", smeldr.Agent}, {"cron", smeldr.Job}, {"peter", smeldr.Human}} {
		raw, _, err := ts.CreateClassified(ctx, c.name, "editor", c.class, time.Hour)
		if err != nil {
			t.Fatalf("CreateClassified(%s): %v", c.name, err)
		}
		u, ok := smeldr.VerifyTokenString(raw, []byte(pgTestSecret), ts)
		if !ok || len(u.Roles) != 2 || u.Roles[0] != smeldr.Editor || u.Roles[1] != c.class {
			t.Errorf("%s verified as %v, %v, want [editor %s]", c.name, u.Roles, ok, c.class)
		}
	}
	recs, err := ts.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := map[string]string{}
	for _, r := range recs {
		got[r.Name] = r.ActorClass
	}
	want := map[string]string{"old": "", "bot": "agent", "cron": "job", "peter": "human"}
	for n, w := range want {
		if g, ok := got[n]; !ok || g != w {
			t.Errorf("ActorClass of %s = %q (present %v), want %q", n, g, ok, w)
		}
	}
}
