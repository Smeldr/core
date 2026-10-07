//go:build integration

package pgx

import (
	"context"
	"sync"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// HookPost is a compiled type registered through App.Content, so
// App.TransitionItem reaches both its table and its module's handlers.
type HookPost struct {
	smeldr.Node
	Title string
}

// A transition_item publish on Postgres fires the module's own After handlers
// (AfterUpdate and AfterPublish), with the caller's user, and the transition is
// committed.
func TestPG_TransitionItemFiresModuleAfterHooks(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE hook_posts (
		id TEXT PRIMARY KEY, slug TEXT NOT NULL UNIQUE, status TEXT NOT NULL DEFAULT 'draft',
		published_at TIMESTAMPTZ, scheduled_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL, rev INTEGER NOT NULL DEFAULT 0,
		title TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	var mu sync.Mutex
	fired := map[smeldr.LifecycleEvent]string{}
	record := func(sig smeldr.LifecycleEvent) smeldr.Option {
		return smeldr.On(sig, func(c smeldr.Context, _ *HookPost) error {
			mu.Lock()
			fired[sig] = c.User().ID
			mu.Unlock()
			return nil
		})
	}
	repo := smeldr.NewSQLRepo[*HookPost](db, smeldr.Table("hook_posts"))
	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte(pgTestSecret), DB: db})
	app.Content(smeldr.NewModule((*HookPost)(nil), smeldr.Repo(repo),
		record(smeldr.AfterUpdate), record(smeldr.AfterPublish), record(smeldr.AfterArchive)))

	if err := repo.Save(ctx, &HookPost{Node: smeldr.Node{ID: "hp-1", Slug: "hp-1", Status: smeldr.Draft}, Title: "Hook"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	caller := smeldr.NewContextWithUser(smeldr.User{ID: "pg-user", Roles: []smeldr.Role{smeldr.Editor}})
	if _, err := app.TransitionItem(caller, "HookPost", "hp-1", string(smeldr.Published)); err != nil {
		t.Fatalf("TransitionItem: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(fired)
		mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	for _, sig := range []smeldr.LifecycleEvent{smeldr.AfterUpdate, smeldr.AfterPublish} {
		if got, ok := fired[sig]; !ok || got != "pg-user" {
			t.Errorf("%s: fired=%v user=%q; want fired with pg-user", sig, ok, got)
		}
	}
	if _, ok := fired[smeldr.AfterArchive]; ok {
		t.Error("AfterArchive fired on a publish")
	}
	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM hook_posts WHERE id = $1`, "hp-1").Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != string(smeldr.Published) {
		t.Errorf("status = %q; want published", status)
	}
}
