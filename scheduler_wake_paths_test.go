package smeldr

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// Every path that puts an item in Scheduled wakes the scheduler (T1003,
// T1008): with the timer armed for the 60 s cap, an item due in a moment is
// published within wakeBound, which only a wake can meet.
const wakeBound = 3 * time.Second

// startAppScheduler runs a Scheduler over app's entries with app's wake, as
// App.serve does, until the test ends.
func startAppScheduler(t *testing.T, app *App) {
	t.Helper()
	s := newScheduler(app.schedulerModules, NewBackgroundContext("example.com"))
	s.wake = app.schedulerWake
	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	t.Cleanup(func() { cancel(); s.Wait() })
	time.Sleep(50 * time.Millisecond) // the startup tick arms the timer for the cap
}

// waitStatus polls get until it reports want or d passes.
func waitStatus(get func() Status, want Status, d time.Duration) bool {
	for deadline := time.Now().Add(d); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if get() == want {
			return true
		}
	}
	return get() == want
}

// storyShape is a module shaped like smeldr.dev's Story; postShape like its
// Post (T1008: site measured the Story on the cap and the Post woken).
type storyShape struct {
	Node
	Title    string `smeldr:"required" db:"title"`
	Excerpt  string `smeldr:"required" db:"excerpt"`
	Body     string `smeldr:"required" smeldr_format:"markdown" db:"body"`
	Featured bool   `db:"featured"`
}

func (s *storyShape) Head() Head { return Head{Title: s.Title} }

type postShape struct {
	Node
	Title string `smeldr:"required" db:"title"`
}

func (p *postShape) Head() Head { return Head{Title: p.Title} }

// siteShapedApp is an App over SQLite with a Post-shaped and a Story-shaped
// module, registered in smeldr.dev's order and with its options. wrap, when
// set, wraps the Story module's repository.
func siteShapedApp(t *testing.T, wrap func(Repository[*storyShape]) Repository[*storyShape]) (*App, *Module[*postShape], *Module[*storyShape], *SQLRepo[*storyShape]) {
	t.Helper()
	db := newSQLiteDB(t)
	mustExecDB(t, db, `CREATE TABLE posts (id TEXT PRIMARY KEY, slug TEXT NOT NULL UNIQUE, status TEXT NOT NULL DEFAULT 'draft',
		published_at DATETIME, scheduled_at DATETIME, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, title TEXT NOT NULL, rev INTEGER NOT NULL DEFAULT 0)`)
	mustExecDB(t, db, `CREATE TABLE stories (id TEXT PRIMARY KEY, slug TEXT NOT NULL UNIQUE, status TEXT NOT NULL DEFAULT 'draft',
		published_at DATETIME, scheduled_at DATETIME, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, title TEXT NOT NULL,
		excerpt TEXT NOT NULL DEFAULT '', body TEXT NOT NULL, featured INTEGER NOT NULL DEFAULT 0, rev INTEGER NOT NULL DEFAULT 0)`)
	app := New(MustConfig(Config{BaseURL: "https://example.com", Secret: []byte(transitionItemTestSecret), DB: db}))
	stories := NewSQLRepo[*storyShape](db, Table("stories"))
	var srepo Repository[*storyShape] = stories
	if wrap != nil {
		srepo = wrap(stories)
	}
	pm := NewModule((*postShape)(nil), Repo(NewSQLRepo[*postShape](db, Table("posts"))), At("/devlog"), MCP(MCPRead, MCPWrite),
		SitemapConfig{}, Feed(FeedConfig{Title: "Devlog"}), AIIndex(LLMsTxt), BlockHost(), Cache(5*time.Minute))
	sm := NewModule((*storyShape)(nil), Repo(srepo), At("/solved"), MCP(MCPRead, MCPWrite), SitemapConfig{}, BlockHost())
	app.Content(pm)
	app.Content(sm)
	return app, pm, sm, stories
}

// storyStatus reads a story's status, "" when it cannot be read.
func storyStatus(repo *SQLRepo[*storyShape], slug string) func() Status {
	return func() Status {
		s, err := repo.FindBySlug(context.Background(), slug)
		if err != nil {
			return ""
		}
		return s.Status
	}
}

// adminCtx is an admin caller for the MCP methods.
func adminCtx() Context { return NewTestContext(User{ID: "admin", Roles: []Role{Admin}}) }

// newStory creates a draft story over MCP, as site does.
func newStory(t *testing.T, sm *Module[*storyShape], slug string) {
	t.Helper()
	if _, err := sm.MCPCreate(adminCtx(), map[string]any{"slug": slug, "title": "Story " + slug, "excerpt": "An excerpt.", "body": "A body."}); err != nil {
		t.Fatalf("create %s: %v", slug, err)
	}
}

// The schedule tool's path on a Story-shaped module next to a Post-shaped one:
// a far item first, then a near one, which is published at its own time.
func TestWakePaths_StoryShapedModuleMCPSchedule(t *testing.T) {
	app, _, sm, stories := siteShapedApp(t, nil)
	newStory(t, sm, "far")
	newStory(t, sm, "near")
	if err := sm.MCPSchedule(adminCtx(), "far", time.Now().Add(time.Hour), ""); err != nil {
		t.Fatal(err)
	}
	startAppScheduler(t, app)
	if err := sm.MCPSchedule(adminCtx(), "near", time.Now().Add(100*time.Millisecond), ""); err != nil {
		t.Fatal(err)
	}
	if !waitStatus(storyStatus(stories, "near"), Published, wakeBound) {
		t.Fatalf("near Story = %q after %v: not woken", storyStatus(stories, "near")(), wakeBound)
	}
}

// failSaveOnce fails the first publishing Save of the item with slug, as
// SQLite's busy lock does while another write commits (T1008).
type failSaveOnce struct {
	Repository[*storyShape]
	slug   string
	mu     sync.Mutex
	failed bool
}

func (r *failSaveOnce) Save(ctx context.Context, item *storyShape) error {
	r.mu.Lock()
	fail := !r.failed && item.Slug == r.slug && item.Status == Published
	if fail {
		r.failed = true
	}
	r.mu.Unlock()
	if fail {
		return errors.New("database is locked (5) (SQLITE_BUSY)")
	}
	return r.Repository.Save(ctx, item)
}

func (r *failSaveOnce) didFail() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.failed
}

// site's sequence (S3457): a Story and a Post scheduled over MCP for one whole
// second. The Story's publish meets a busy database once; it is retried within
// a second, not at the 60 s cap.
func TestWakePaths_SaveFailsOnceRetriedSoon(t *testing.T) {
	var flaky *failSaveOnce
	app, pm, sm, stories := siteShapedApp(t, func(r Repository[*storyShape]) Repository[*storyShape] {
		flaky = &failSaveOnce{Repository: r, slug: "round"}
		return flaky
	})
	startAppScheduler(t, app)
	due := time.Now().UTC().Add(2 * time.Second).Truncate(time.Second)
	// Seeded on the SQL repository itself: a create through the wrapper would
	// not run in the repository's transaction (the wrapper hides it).
	now := time.Now().UTC()
	if err := stories.Save(context.Background(), &storyShape{Node: Node{ID: NewID(), Slug: "round", Status: Draft, CreatedAt: now, UpdatedAt: now},
		Title: "Story round", Excerpt: "An excerpt.", Body: "A body."}); err != nil {
		t.Fatal(err)
	}
	if err := sm.MCPSchedule(adminCtx(), "round", due, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := pm.MCPCreate(adminCtx(), map[string]any{"slug": "round", "title": "Post round"}); err != nil {
		t.Fatal(err)
	}
	if err := pm.MCPSchedule(adminCtx(), "round", due, ""); err != nil {
		t.Fatal(err)
	}
	if !waitStatus(storyStatus(stories, "round"), Published, time.Until(due)+wakeBound) {
		t.Fatalf("Story = %q %v after due: a failed save waited for the cap", storyStatus(stories, "round")(), time.Since(due))
	}
	if !flaky.didFail() {
		t.Fatal("the injected failure never ran: the test proves nothing")
	}
}

// Only the first consecutive failure of an item is retried soon; after that
// the cap applies, until the item publishes.
func TestSkipRetryAt(t *testing.T) {
	now := time.Now()
	key := "type/" + NewID()
	t.Cleanup(func() { clearSkipRetry(key) })
	if r := skipRetryAt(key, now); r == nil || !r.Equal(now.Add(scheduledSkipRetry)) {
		t.Fatalf("first failure: retry = %v, want now + %v", r, scheduledSkipRetry)
	}
	if r := skipRetryAt(key, now); r != nil {
		t.Errorf("second failure: retry = %v, want nil (the cap)", r)
	}
	clearSkipRetry(key)
	if r := skipRetryAt(key, now); r == nil {
		t.Error("after a publish: the next failure is retried soon again")
	}
}

// A runtime item whose publish fails is retried within a second the first
// time, at the cap after that, and published once the failure clears.
func TestDynamicScheduler_SkippedRetriedSoon(t *testing.T) {
	app, db, repo, _ := dynSchedApp(t)
	n := newDraft(t, repo, "Flaky")
	if err := repo.ScheduleContent(context.Background(), n.ID, time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	app.cfg.DB = failUpdateDB{db}
	d := dynamicScheduler{app}
	bg := NewBackgroundContext("example.com")
	now := time.Now().UTC()
	capturedLog(func() {
		if p, next, err := d.processScheduled(bg, now); err != nil || p != 0 || next == nil || next.After(now.Add(2*time.Second)) {
			t.Errorf("first failure: published=%d next=%v err=%v, want a retry within a second", p, next, err)
		}
		if _, next, _ := d.processScheduled(bg, now); next != nil {
			t.Errorf("second failure: next = %v, want nil (the cap)", next)
		}
	})
	app.cfg.DB = db
	if p, _, err := d.processScheduled(bg, time.Now().UTC()); err != nil || p != 1 {
		t.Fatalf("after the failure cleared: published=%d err=%v, want 1", p, err)
	}
	if _, still := scheduledSkipped.Load(repo.typeName + "/" + n.ID); still {
		t.Error("the failure memory was not cleared when the item published")
	}
}

// transition_item to scheduled on a compiled type wakes the scheduler.
func TestWakePaths_TransitionItemCompiled(t *testing.T) {
	app, db, repo := newTransitionHookApp(t, nil)
	bg := context.Background()
	seed := &testPost{Node: Node{ID: NewID(), Slug: "near", Status: Draft}, Title: "Near"}
	if err := repo.Save(bg, seed); err != nil {
		t.Fatal(err)
	}
	mustExecDB(t, db, "UPDATE test_posts SET scheduled_at = '"+time.Now().UTC().Add(100*time.Millisecond).Format(time.RFC3339Nano)+"' WHERE id = '"+seed.ID+"'")
	startAppScheduler(t, app)
	if _, err := app.TransitionItem(TrustedRead(bg), "testPost", "near", string(Scheduled)); err != nil {
		t.Fatalf("TransitionItem: %v", err)
	}
	status := func() Status {
		p, err := repo.FindBySlug(bg, "near")
		if err != nil {
			return ""
		}
		return p.Status
	}
	if !waitStatus(status, Published, wakeBound) {
		t.Fatalf("item = %q after %v: transition_item to scheduled did not wake the scheduler", status(), wakeBound)
	}
}

// transition_item to scheduled on a runtime-defined type wakes the scheduler.
func TestWakePaths_TransitionItemDynamic(t *testing.T) {
	app, db, repo, _ := dynSchedApp(t)
	n := newDraft(t, repo, "Near")
	mustExecDB(t, db, "UPDATE smeldr_dynamic_content SET scheduled_at = '"+time.Now().UTC().Add(100*time.Millisecond).Format(time.RFC3339Nano)+"' WHERE id = '"+n.ID+"'")
	startAppScheduler(t, app)
	if _, err := app.TransitionItem(TrustedRead(context.Background()), repo.typeName, n.Slug, string(Scheduled)); err != nil {
		t.Fatalf("TransitionItem: %v", err)
	}
	status := func() Status { return dynStatus(t, repo, n.ID).Status }
	if !waitStatus(status, Published, wakeBound) {
		t.Fatalf("item = %q after %v: transition_item to scheduled did not wake the scheduler", status(), wakeBound)
	}
}

// A scheduled_at edit through update_<type> on an item already Scheduled
// wakes the scheduler (the save keeps it Scheduled).
func TestWakePaths_MCPUpdateRescheduleEarlier(t *testing.T) {
	app, _, sm, stories := siteShapedApp(t, nil)
	newStory(t, sm, "moved")
	if err := sm.MCPSchedule(adminCtx(), "moved", time.Now().Add(time.Hour), ""); err != nil {
		t.Fatal(err)
	}
	startAppScheduler(t, app)
	if _, err := sm.MCPUpdate(adminCtx(), "moved", map[string]any{"ScheduledAt": time.Now().UTC().Add(100 * time.Millisecond).Format(time.RFC3339Nano)}); err != nil {
		t.Fatalf("MCPUpdate: %v", err)
	}
	if !waitStatus(storyStatus(stories, "moved"), Published, wakeBound) {
		t.Fatalf("item = %q after %v: a scheduled_at edit did not wake the scheduler", storyStatus(stories, "moved")(), wakeBound)
	}
}

// afterStatusChange wakes on a change into Scheduled (a reschedule too) and
// on nothing else; with no scheduler it does not block.
func TestAfterStatusChange_WakesOnScheduled(t *testing.T) {
	app := New(MustConfig(Config{BaseURL: "https://example.com", Secret: []byte(transitionItemTestSecret)}))
	bg := context.Background()
	app.afterStatusChange(bg, statusTransition{typeName: "x", id: "1", from: "draft", to: "scheduled"}) // nil channel: no block
	app.schedulerWake = make(chan struct{}, 1)
	woken := func() bool {
		select {
		case <-app.schedulerWake:
			return true
		default:
			return false
		}
	}
	for _, c := range []struct {
		from, to string
		want     bool
	}{
		{"draft", "scheduled", true},
		{"scheduled", "scheduled", true},
		{"scheduled", "published", false},
		{"draft", "published", false},
	} {
		app.afterStatusChange(bg, statusTransition{typeName: "x", id: "1", from: c.from, to: c.to})
		if got := woken(); got != c.want {
			t.Errorf("%s -> %s: woken = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}
