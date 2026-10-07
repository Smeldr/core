// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"
)

// afterRecorder counts which After events a module's handlers received, and
// the user each one saw.
type afterRecorder struct {
	mu    sync.Mutex
	fired map[LifecycleEvent]int
	users []string
}

func newAfterRecorder() *afterRecorder {
	return &afterRecorder{fired: map[LifecycleEvent]int{}}
}

// options registers a recording handler for every event a transition can fire.
func (r *afterRecorder) options() []Option {
	var opts []Option
	for _, sig := range []LifecycleEvent{AfterUpdate, AfterPublish, AfterUnpublish, AfterArchive, AfterSchedule} {
		s := sig
		opts = append(opts, On[*testPost](s, func(ctx Context, _ *testPost) error {
			r.mu.Lock()
			r.fired[s]++
			r.users = append(r.users, ctx.User().ID)
			r.mu.Unlock()
			return nil
		}))
	}
	return opts
}

// wait returns the events fired once want of them have arrived (After handlers
// are asynchronous) or a deadline passed, after a short settle for extras.
func (r *afterRecorder) wait(want int) map[LifecycleEvent]int {
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		n := 0
		for _, c := range r.fired {
			n += c
		}
		r.mu.Unlock()
		if n >= want {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[LifecycleEvent]int{}
	for k, v := range r.fired {
		out[k] = v
	}
	return out
}

func wantFired(sigs ...LifecycleEvent) map[LifecycleEvent]int {
	out := map[LifecycleEvent]int{}
	for _, s := range sigs {
		out[s]++
	}
	return out
}

// newTransitionHookApp builds an App over SQLite with a testPost module
// registered through Content, so App.TransitionItem reaches its table and its
// handlers. repoWrap, when set, wraps the module's repository.
func newTransitionHookApp(t *testing.T, repoWrap func(Repository[*testPost]) Repository[*testPost], opts ...Option) (*App, *sql.DB, *SQLRepo[*testPost]) {
	t.Helper()
	db := newSQLiteDB(t)
	if _, err := db.ExecContext(context.Background(), `
		CREATE TABLE test_posts (
			id           TEXT NOT NULL PRIMARY KEY,
			slug         TEXT NOT NULL DEFAULT '',
			status       TEXT NOT NULL DEFAULT 'draft',
			created_at   DATETIME NOT NULL DEFAULT '',
			updated_at   DATETIME NOT NULL DEFAULT '',
			scheduled_at DATETIME,
			published_at DATETIME,
			title        TEXT NOT NULL DEFAULT '',
			body         TEXT NOT NULL DEFAULT '',
			rev          INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	sqlRepo := NewSQLRepo[*testPost](db)
	var repo Repository[*testPost] = sqlRepo
	if repoWrap != nil {
		repo = repoWrap(sqlRepo)
	}
	app := New(MustConfig(Config{BaseURL: "https://example.com", Secret: []byte(transitionItemTestSecret), DB: db}))
	app.Content(newTestModule(repo, opts...))
	return app, db, sqlRepo
}

// TestStatusSignals is the one table every transition path shares.
func TestStatusSignals(t *testing.T) {
	cases := []struct {
		from, to Status
		want     []LifecycleEvent
	}{
		{Draft, Published, []LifecycleEvent{AfterPublish}},
		{Scheduled, Published, []LifecycleEvent{AfterPublish}},
		{Published, Draft, []LifecycleEvent{AfterUnpublish}},
		{Published, Archived, []LifecycleEvent{AfterUnpublish, AfterArchive}},
		{Draft, Archived, []LifecycleEvent{AfterArchive}},
		{Draft, Scheduled, []LifecycleEvent{AfterSchedule}},
		{Published, Scheduled, []LifecycleEvent{AfterUnpublish, AfterSchedule}},
		{Draft, Draft, nil},
		{Published, Published, nil},
		{"proposed", "ratified", nil},
	}
	for _, c := range cases {
		t.Run(string(c.from)+"->"+string(c.to), func(t *testing.T) {
			if got := statusSignals(c.from, c.to); !reflect.DeepEqual(got, c.want) {
				t.Errorf("statusSignals(%s, %s) = %v; want %v", c.from, c.to, got, c.want)
			}
		})
	}
}

// TestAfterHooks_StatusTransitionsMatchPUT proves every transition path fires
// the module's own After handlers a PUT making the same change fires:
// AfterUpdate plus statusSignals(from, to), each exactly once.
func TestAfterHooks_StatusTransitionsMatchPUT(t *testing.T) {
	type runFn func(t *testing.T, rec *afterRecorder, from, to Status)
	memPath := func(call func(m *Module[*testPost], seed *testPost, to Status) error) runFn {
		return func(t *testing.T, rec *afterRecorder, from, to Status) {
			repo := NewMemoryRepo[*testPost]()
			seed := seedPost(t, repo, "Item", from)
			m := newTestModule(repo, rec.options()...)
			if err := call(m, seed, to); err != nil {
				t.Fatalf("transition: %v", err)
			}
		}
	}
	put := memPath(func(m *Module[*testPost], seed *testPost, to Status) error {
		return serveWrite(m.updateHandler, http.MethodPut, "/testposts/"+seed.Slug, seed.Slug,
			map[string]any{"Title": seed.Title, "Status": string(to)})
	})
	publish := memPath(func(m *Module[*testPost], seed *testPost, _ Status) error {
		return m.MCPPublish(hookCtx(), seed.Slug, "")
	})
	schedule := memPath(func(m *Module[*testPost], seed *testPost, _ Status) error {
		return m.MCPSchedule(hookCtx(), seed.Slug, time.Now().Add(time.Hour), "")
	})
	archive := memPath(func(m *Module[*testPost], seed *testPost, _ Status) error {
		return m.MCPArchive(hookCtx(), seed.Slug, "")
	})
	scheduler := memPath(func(m *Module[*testPost], seed *testPost, _ Status) error {
		if got := m.publishDue(NewBackgroundContext("example.com"), seed, time.Now()); got != duePublished {
			return errors.New("publishDue did not publish")
		}
		return nil
	})
	transitionItem := func(t *testing.T, rec *afterRecorder, from, to Status) {
		app, _, repo := newTransitionHookApp(t, nil, rec.options()...)
		seed := seedPost(t, repo, "Item", from)
		if _, err := app.TransitionItem(hookCtx(), "testPost", seed.Slug, string(to)); err != nil {
			t.Fatalf("TransitionItem: %v", err)
		}
	}

	rows := []struct {
		name     string
		from, to Status
		run      runFn
	}{
		{"PUT", Draft, Published, put},
		{"PUT", Published, Archived, put},
		{"MCP publish", Draft, Published, publish},
		{"MCP schedule", Published, Scheduled, schedule},
		{"MCP archive", Published, Archived, archive},
		{"MCP archive", Draft, Archived, archive},
		{"scheduler", Scheduled, Published, scheduler},
		{"transition_item", Draft, Published, transitionItem},
		{"transition_item", Published, Archived, transitionItem},
		{"transition_item", Draft, Scheduled, transitionItem},
		{"transition_item", Published, Draft, transitionItem},
	}
	for _, r := range rows {
		t.Run(r.name+" "+string(r.from)+"->"+string(r.to), func(t *testing.T) {
			rec := newAfterRecorder()
			r.run(t, rec, r.from, r.to)
			want := wantFired(append([]LifecycleEvent{AfterUpdate}, statusSignals(r.from, r.to)...)...)
			if got := rec.wait(len(want) + 1); !reflect.DeepEqual(got, want) {
				t.Errorf("fired %v; want %v", got, want)
			}
		})
	}
}

// TestAfterHooks_TransitionItemHandlersSeeTheCaller proves transition_item's
// handlers get the caller's own Context (its user), and a caller with no
// Context gets the guest background context.
func TestAfterHooks_TransitionItemHandlersSeeTheCaller(t *testing.T) {
	cases := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{"caller Context", NewTestContext(User{ID: "user-7", Roles: []Role{Editor}}), "user-7"},
		{"plain context", context.Background(), GuestUser.ID},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := newAfterRecorder()
			app, _, repo := newTransitionHookApp(t, nil, rec.options()...)
			seed := seedPost(t, repo, "Item", Draft)
			if _, err := app.TransitionItem(c.ctx, "testPost", seed.Slug, string(Published)); err != nil {
				t.Fatalf("TransitionItem: %v", err)
			}
			rec.wait(2)
			rec.mu.Lock()
			defer rec.mu.Unlock()
			if len(rec.users) != 2 {
				t.Fatalf("handlers ran %d times; want 2", len(rec.users))
			}
			for _, u := range rec.users {
				if u != c.want {
					t.Errorf("handler saw user %q; want %q", u, c.want)
				}
			}
		})
	}
}

// TestAfterHooks_TransitionItemDoesNotDoubleRecord proves transition_item still
// writes one provenance record for one transition: the module's handlers run,
// and the bus side (provenance subscriber, stream) is not fed a second time.
func TestAfterHooks_TransitionItemDoesNotDoubleRecord(t *testing.T) {
	rec := newAfterRecorder()
	app, db, repo := newTransitionHookApp(t, nil, rec.options()...)
	if err := CreateProvenanceTable(db); err != nil {
		t.Fatalf("CreateProvenanceTable: %v", err)
	}
	app.Provenance(NewProvenanceStore(db))
	app.Handler() // wires the signal bus, as a running App has it
	seed := seedPost(t, repo, "Item", Draft)
	if _, err := app.TransitionItem(hookCtx(), "testPost", seed.Slug, string(Published)); err != nil {
		t.Fatalf("TransitionItem: %v", err)
	}
	if got := rec.wait(2); !reflect.DeepEqual(got, wantFired(AfterUpdate, AfterPublish)) {
		t.Fatalf("module handlers fired %v; want AfterUpdate and AfterPublish", got)
	}
	time.Sleep(200 * time.Millisecond) // a bus dispatch, if any, is asynchronous
	var n int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM smeldr_provenance WHERE subject_id = ?`, seed.ID).Scan(&n); err != nil {
		t.Fatalf("count provenance: %v", err)
	}
	if n != 1 {
		t.Errorf("provenance records for one transition = %d; want 1", n)
	}
}

// findfailRepo fails FindByID with an error other than ErrNotFound and counts
// the calls.
type findfailRepo struct {
	Repository[*testPost]
	calls *int
}

func (r findfailRepo) FindByID(context.Context, string) (*testPost, error) {
	if r.calls != nil {
		*r.calls++
	}
	return nil, errRepoError
}

// TestAfterHooks_TransitionItemLoadFailureDoesNotFail proves a module that
// cannot load the item skips its handlers and the committed transition still
// succeeds.
func TestAfterHooks_TransitionItemLoadFailureDoesNotFail(t *testing.T) {
	rec := newAfterRecorder()
	app, db, repo := newTransitionHookApp(t, func(r Repository[*testPost]) Repository[*testPost] {
		return findfailRepo{Repository: r}
	}, rec.options()...)
	seed := seedPost(t, repo, "Item", Draft)
	if _, err := app.TransitionItem(hookCtx(), "testPost", seed.Slug, string(Published)); err != nil {
		t.Fatalf("TransitionItem: %v", err)
	}
	var status string
	if err := db.QueryRowContext(context.Background(), `SELECT status FROM test_posts WHERE id = ?`, seed.ID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != string(Published) {
		t.Errorf("status = %q; want published", status)
	}
	if got := rec.wait(1); len(got) != 0 {
		t.Errorf("handlers fired %v; want none", got)
	}
}

// TestAfterHooks_NoModuleForType proves a transition of a type with no
// registered module fires nothing and does not fail: another type's module is
// not reached, and an App with no modules at all is a no-op.
func TestAfterHooks_NoModuleForType(t *testing.T) {
	rec := newAfterRecorder()
	app, _, repo := newTransitionHookApp(t, nil, rec.options()...)
	seed := seedPost(t, repo, "Item", Draft)
	app.fireModuleAfterTransition(hookCtx(), "SomeOtherType", seed.ID, string(Draft), string(Published))
	(&App{}).fireModuleAfterTransition(hookCtx(), "testPost", seed.ID, string(Draft), string(Published))
	if got := rec.wait(1); len(got) != 0 {
		t.Errorf("handlers fired %v; want none", got)
	}
}

// TestAfterHooks_ModuleWithoutHandlersSkipsLoad proves a module with no After
// handler does not load the item at all on a transition.
func TestAfterHooks_ModuleWithoutHandlersSkipsLoad(t *testing.T) {
	loads := 0
	app, _, repo := newTransitionHookApp(t, func(r Repository[*testPost]) Repository[*testPost] {
		return findfailRepo{r, &loads}
	})
	seed := seedPost(t, repo, "Item", Draft)
	if _, err := app.TransitionItem(hookCtx(), "testPost", seed.Slug, string(Published)); err != nil {
		t.Fatalf("TransitionItem: %v", err)
	}
	if loads != 0 {
		t.Errorf("item loaded %d times; want 0 for a module with no After handler", loads)
	}
}
