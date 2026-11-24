//go:build integration

package pgx

import (
	"context"
	"errors"
	"testing"

	smeldr "smeldr.dev/core"
)

// LifecyclePost is a compiled type for the lifecycle conflict test.
type LifecyclePost struct {
	smeldr.Node
	Title string
}

// racingLifecycleRepo saves a fresh copy changed by change just before the
// tool's own Save, once: another call's write landing in between (T1331).
type racingLifecycleRepo struct {
	smeldr.Repository[*LifecyclePost]
	change func(*LifecyclePost)
	done   bool
}

func (r *racingLifecycleRepo) Save(ctx context.Context, p *LifecyclePost) error {
	if !r.done {
		r.done = true
		stored, err := r.Repository.FindByID(ctx, p.ID)
		if err != nil {
			return err
		}
		c := *stored
		r.change(&c)
		if err := r.Repository.Save(ctx, &c); err != nil {
			return err
		}
	}
	return r.Repository.Save(ctx, p)
}

// T1331 on Postgres: a publish whose item another call changed in between is
// refused and the other call's write is kept: a field change answers the rev
// conflict, a status change transition.state_moved.
func TestPG_ModuleLifecycle_ConcurrentChange(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE lifecycle_posts (
		id TEXT PRIMARY KEY, slug TEXT NOT NULL UNIQUE, status TEXT NOT NULL DEFAULT 'draft',
		published_at TIMESTAMPTZ, scheduled_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL, rev INTEGER NOT NULL DEFAULT 0,
		title TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	repo := smeldr.NewSQLRepo[*LifecyclePost](db, smeldr.Table("lifecycle_posts"))
	editor := smeldr.NewContextWithUser(smeldr.User{ID: "ed", Roles: []smeldr.Role{smeldr.Editor}})
	for _, tc := range []struct {
		name   string
		change func(*LifecyclePost)
		check  func(error) bool
		title  string
		status smeldr.Status
	}{
		{"field", func(p *LifecyclePost) { p.Title = "changed meanwhile" },
			func(err error) bool {
				return errors.Is(err, smeldr.ErrRevConflict) && smeldr.ProblemTypeOf(err) != "transition.state_moved"
			}, "changed meanwhile", smeldr.Draft},
		{"status", func(p *LifecyclePost) { p.Status = smeldr.Archived },
			func(err error) bool { return smeldr.ProblemTypeOf(err) == "transition.state_moved" }, "Original status", smeldr.Archived},
	} {
		t.Run(tc.name, func(t *testing.T) {
			created, err := smeldr.NewModule((*LifecyclePost)(nil), smeldr.Repo(repo)).MCPCreate(editor, map[string]any{"title": "Original " + tc.name})
			if err != nil {
				t.Fatal(err)
			}
			item := created.(*LifecyclePost)
			m := smeldr.NewModule((*LifecyclePost)(nil), smeldr.Repo(smeldr.Repository[*LifecyclePost](&racingLifecycleRepo{Repository: repo, change: tc.change})))
			err = m.MCPPublish(editor, item.Slug, "")
			if !tc.check(err) {
				t.Fatalf("publish after a concurrent %s change: %v (%q)", tc.name, err, smeldr.ProblemTypeOf(err))
			}
			got, err := repo.FindByID(ctx, item.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Title != tc.title || got.Status != tc.status {
				t.Errorf("stored %q %s, want %q %s", got.Title, got.Status, tc.title, tc.status)
			}
		})
	}
}
