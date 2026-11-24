package smeldr

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// racingRepo lands another call's write just before a lifecycle tool's own
// Save (T1331): once, it saves a fresh copy of the stored item changed by
// change, then passes the tool's item on.
type racingRepo struct {
	Repository[*testPost]
	change func(*testPost)
	done   bool
}

func (r *racingRepo) Save(ctx context.Context, p *testPost) error {
	if !r.done && r.change != nil {
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

// lifecycleSQLRepo is a SQLite-backed testPost repository.
func lifecycleSQLRepo(t *testing.T) Repository[*testPost] {
	t.Helper()
	db := newSQLiteDB(t)
	if _, err := db.Exec(`CREATE TABLE test_posts (id TEXT PRIMARY KEY, slug TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'draft',
		created_at DATETIME NOT NULL DEFAULT '', updated_at DATETIME NOT NULL DEFAULT '', scheduled_at DATETIME, published_at DATETIME,
		title TEXT NOT NULL DEFAULT '', body TEXT NOT NULL DEFAULT '', rev INTEGER NOT NULL DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}
	return NewSQLRepo[*testPost](db)
}

// lifecycleTools runs each of the three tools on slug.
var lifecycleTools = []struct {
	name string
	to   Status
	run  func(m *Module[*testPost], ctx Context, slug string) error
}{
	{"publish", Published, func(m *Module[*testPost], ctx Context, slug string) error { return m.MCPPublish(ctx, slug, "") }},
	{"schedule", Scheduled, func(m *Module[*testPost], ctx Context, slug string) error {
		return m.MCPSchedule(ctx, slug, time.Now().Add(time.Hour), "")
	}},
	{"archive", Archived, func(m *Module[*testPost], ctx Context, slug string) error { return m.MCPArchive(ctx, slug, "") }},
}

// T1331: a field changed by another call between a lifecycle tool's read and
// its Save is kept, and the tool is refused with the rev conflict; nothing is
// announced for it. On SQLRepo this passes before the fix too, on purpose: it
// guards the rev check SQLRepo has had since A158, which is what keeps the
// production path safe. (MemoryRepo has no rev check yet; whether it gets one
// is Peter's decision, P505.)
func TestModuleLifecycle_ConcurrentFieldChange(t *testing.T) {
	for _, tool := range lifecycleTools {
		t.Run(tool.name, func(t *testing.T) {
			inner := lifecycleSQLRepo(t)
			p := seedPost(t, inner, "Original", Draft)
			var announced atomic.Int32
			m := newTestModule(&racingRepo{Repository: inner, change: func(c *testPost) { c.Title = "changed meanwhile" }},
				On[*testPost](AfterPublish, func(Context, *testPost) error { announced.Add(1); return nil }),
				On[*testPost](AfterSchedule, func(Context, *testPost) error { announced.Add(1); return nil }),
				On[*testPost](AfterArchive, func(Context, *testPost) error { announced.Add(1); return nil }))
			err := tool.run(m, NewTestContext(editorUser()), p.Slug)
			if !errors.Is(err, ErrRevConflict) || ProblemTypeOf(err) == problemTransitionStateMoved {
				t.Fatalf("%s after a field change: %v (%q), want the rev conflict", tool.name, err, ProblemTypeOf(err))
			}
			got, _ := inner.FindByID(context.Background(), p.ID)
			if got.Title != "changed meanwhile" || got.Status != Draft {
				t.Errorf("stored %q %s, want the other call's title and draft", got.Title, got.Status)
			}
			time.Sleep(50 * time.Millisecond)
			if n := announced.Load(); n != 0 {
				t.Errorf("%d After hooks ran for the refused move", n)
			}
		})
	}
}

// T1331: when the other call moved the item's status, the tool answers
// transition.state_moved, as transition_item does, with the state it was
// checked from and the target; the other call's state is kept.
func TestModuleLifecycle_ConcurrentStatusChange(t *testing.T) {
	for _, tool := range lifecycleTools {
		t.Run(tool.name, func(t *testing.T) {
			inner := lifecycleSQLRepo(t)
			p := seedPost(t, inner, "Original", Draft)
			other := Archived
			if tool.to == Archived {
				other = Published
			}
			m := newTestModule(&racingRepo{Repository: inner, change: func(c *testPost) { c.Status = other }})
			err := tool.run(m, NewTestContext(editorUser()), p.Slug)
			if ProblemTypeOf(err) != problemTransitionStateMoved || !errors.Is(err, ErrConflict) {
				t.Fatalf("%s after a status change: %v (%q), want transition.state_moved", tool.name, err, ProblemTypeOf(err))
			}
			if v := ProblemValuesOf(err); v["from"] != string(Draft) || v["to"] != string(tool.to) || v["item"] != p.ID {
				t.Errorf("values %v", v)
			}
			got, _ := inner.FindByID(context.Background(), p.ID)
			if got.Status != other {
				t.Errorf("stored %s, want %s (the other call's move)", got.Status, other)
			}
		})
	}
}

// failingFindRepo refuses every Save with a rev conflict and every read
// after it, so saveMove keeps the conflict it got.
type failingFindRepo struct {
	Repository[*testPost]
	saved bool
}

func (r *failingFindRepo) Save(context.Context, *testPost) error {
	r.saved = true
	return ErrRevConflict
}

func (r *failingFindRepo) FindByID(ctx context.Context, id string) (*testPost, error) {
	if r.saved {
		return nil, ErrInternal
	}
	return r.Repository.FindByID(ctx, id)
}

// A rev conflict whose item cannot be read again stays the rev conflict.
func TestModuleLifecycle_ConflictUnreadable(t *testing.T) {
	inner := NewMemoryRepo[*testPost]()
	p := seedPost(t, inner, "Original", Draft)
	m := newTestModule(&failingFindRepo{Repository: inner})
	if err := m.MCPArchive(NewTestContext(editorUser()), p.Slug, ""); !errors.Is(err, ErrRevConflict) || ProblemTypeOf(err) == problemTransitionStateMoved {
		t.Errorf("archive: %v", err)
	}
}
