package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// dynSchedApp is an App serving runtime-defined content only, with one web
// content type "memo" (URL prefix /memos), its provenance recorded in store.
func dynSchedApp(t *testing.T) (*App, *sql.DB, *DynamicTypeRepo, *fakeProvenanceStore) {
	t.Helper()
	db := newSQLiteDB(t)
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte(transitionItemTestSecret),
		DB:      db,
	}))
	store := &fakeProvenanceStore{}
	app.Provenance(store)
	app.ServeDynamicContent()
	repo := defineDynSchedType(t, app, "memo", "/memos")
	return app, db, repo, store
}

// defineDynSchedType defines a runtime content type and returns its repository.
func defineDynSchedType(t *testing.T, app *App, typeName, prefix string) *DynamicTypeRepo {
	t.Helper()
	fields, err := json.Marshal([]SchemaField{{Name: "Title", Type: "string", Required: true, Role: "title"}})
	if err != nil {
		t.Fatal(err)
	}
	desc, err := app.DefineContentType(context.Background(), &ContentTypeSchema{TypeName: typeName, URLPrefix: prefix, Fields: fields})
	if err != nil {
		t.Fatalf("DefineContentType: %v", err)
	}
	repo, err := app.DynamicContentRepo(desc.Name)
	if err != nil {
		t.Fatalf("DynamicContentRepo: %v", err)
	}
	return repo
}

// publishedSignals returns a channel that receives the id of every item the
// App announces with AfterPublish.
func publishedSignals(app *App) <-chan string {
	ch := make(chan string, 16)
	app.AddSignalListener(func(sig LifecycleEvent, _ string, item any) {
		if sig != AfterPublish {
			return
		}
		select {
		case ch <- nodeIDOf(item):
		default:
		}
	})
	return ch
}

// waitPublished waits up to d for id on ch.
func waitPublished(t *testing.T, ch <-chan string, id string, d time.Duration) bool {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case got := <-ch:
			if got == id {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// runApp serves app on a local port until the test ends.
func runApp(t *testing.T, app *App) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- app.serve(context.Background(), ln, time.Second) }()
	t.Cleanup(func() {
		ln.Close()
		<-done
	})
}

func newDraft(t *testing.T, repo *DynamicTypeRepo, title string) *DynamicNode {
	t.Helper()
	n, err := repo.CreateDraft(context.Background(), map[string]any{"Title": title})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	return n
}

func dynStatus(t *testing.T, repo *DynamicTypeRepo, id string) *DynamicNode {
	t.Helper()
	n, err := repo.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	return n
}

// The probe of P290 as a test: an App with only runtime-defined types runs a
// scheduler, and a scheduled item is published at its time.
func TestDynamicScheduler_PublishesAtTime(t *testing.T) {
	app, _, repo, store := dynSchedApp(t)
	if len(app.schedulerModules) != 1 {
		t.Fatalf("scheduler entries = %d, want the one dynamic entry", len(app.schedulerModules))
	}
	app.ServeDynamicContent() // a second call adds no second entry
	if len(app.schedulerModules) != 1 {
		t.Fatalf("after a second ServeDynamicContent: %d entries, want 1", len(app.schedulerModules))
	}
	published := publishedSignals(app)
	node := newDraft(t, repo, "Due soon")
	runApp(t, app)
	if err := repo.ScheduleContent(context.Background(), node.ID, time.Now().UTC().Add(50*time.Millisecond)); err != nil {
		t.Fatalf("ScheduleContent: %v", err)
	}
	if !waitPublished(t, published, node.ID, 5*time.Second) {
		t.Fatalf("not published: status %q", dynStatus(t, repo, node.ID).Status)
	}
	got := dynStatus(t, repo, node.ID)
	if got.Status != Published || got.PublishedAt.IsZero() || got.ScheduledAt != nil {
		t.Errorf("after publish: status %q, published_at %v, scheduled_at %v; want published, set, nil",
			got.Status, got.PublishedAt, got.ScheduledAt)
	}
	var viaTrigger bool
	for _, rec := range store.Appended() {
		if rec.SubjectID == node.ID && rec.FromState == "scheduled" && rec.ToState == "published" && rec.Surface == surfaceTrigger {
			viaTrigger = true
		}
	}
	if !viaTrigger {
		t.Errorf("no scheduled -> published record with surface trigger: %+v", store.Appended())
	}
	// The fragment is rebuilt at the end of the tick, after the announcement.
	var frag []byte
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if frag, _ = app.sitemapStore.Get("/memos/sitemap.xml"); strings.Contains(string(frag), "/memos/"+node.Slug) {
			break
		}
	}
	if !strings.Contains(string(frag), "/memos/"+node.Slug) {
		t.Errorf("sitemap fragment does not list the published item:\n%s", frag)
	}
}

// Rescheduling to an earlier time wakes the scheduler: the item is published
// at its new time, not at the old one or the 60 s cap.
func TestDynamicScheduler_RescheduleEarlierPublishesOnTime(t *testing.T) {
	app, _, repo, _ := dynSchedApp(t)
	published := publishedSignals(app)
	node := newDraft(t, repo, "Moved up")
	ctx := context.Background()
	if err := repo.ScheduleContent(ctx, node.ID, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("ScheduleContent: %v", err)
	}
	runApp(t, app)
	time.Sleep(50 * time.Millisecond) // the startup tick arms the timer an hour out (capped at 60 s)
	if err := repo.ScheduleContent(ctx, node.ID, time.Now().UTC().Add(50*time.Millisecond)); err != nil {
		t.Fatalf("reschedule: %v", err)
	}
	if !waitPublished(t, published, node.ID, 5*time.Second) {
		t.Fatalf("not published after the reschedule: status %q", dynStatus(t, repo, node.ID).Status)
	}
}

// An item already overdue when the App starts is published by the startup tick.
func TestDynamicScheduler_OverdueAtStartup(t *testing.T) {
	app, _, repo, _ := dynSchedApp(t)
	published := publishedSignals(app)
	node := newDraft(t, repo, "Overdue")
	if err := repo.ScheduleContent(context.Background(), node.ID, time.Now().UTC().Add(-time.Hour)); err != nil {
		t.Fatalf("ScheduleContent: %v", err)
	}
	runApp(t, app)
	if !waitPublished(t, published, node.ID, 5*time.Second) {
		t.Fatalf("overdue item not published at startup: status %q", dynStatus(t, repo, node.ID).Status)
	}
}

// A mixed App: one tick publishes a due compiled item and a due runtime item.
func TestDynamicScheduler_MixedApp(t *testing.T) {
	app, db, _ := setupTransitionItemApp(t)
	app.ServeDynamicContent()
	repo := defineDynSchedType(t, app, "memo", "")
	insertSignal(t, db, "due-signal", "due-signal", "scheduled")
	mustExecDB(t, db, "UPDATE smeldr_signals SET scheduled_at = '"+time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)+"' WHERE id = 'due-signal'")
	node := newDraft(t, repo, "Mixed")
	if err := repo.ScheduleContent(context.Background(), node.ID, time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatalf("ScheduleContent: %v", err)
	}
	if len(app.schedulerModules) < 2 {
		t.Fatalf("scheduler entries = %d, want the modules and the dynamic entry", len(app.schedulerModules))
	}
	newScheduler(app.schedulerModules, NewBackgroundContext("example.com")).tick()
	if s := dynStatus(t, repo, node.ID).Status; s != Published {
		t.Errorf("runtime item = %q, want published", s)
	}
	if s := signalStatusOf(t, db, "due-signal"); s == "scheduled" {
		t.Errorf("compiled item still scheduled after the same tick")
	}
}

// rejectMemoFlow is a flow with one active item at a time under ConflictReject.
func rejectMemoFlow(typeName string) StateFlow {
	return StateFlow{
		Name: typeName + "-reject", TypeName: typeName, ActiveState: "published", ConflictPolicy: ConflictReject,
		States: []State{{Name: "draft", IsInitial: true}, {Name: "scheduled"}, {Name: "published"}, {Name: "archived"}},
		Transitions: []Transition{
			{From: "draft", To: "scheduled"}, {From: "draft", To: "published"},
			{From: "scheduled", To: "published"}, {From: "published", To: "archived"},
		},
	}
}

// A runtime item the conflict policy blocks stays scheduled, is logged once,
// is retried a retry interval later, and publishes once the active item leaves.
func TestDynamicScheduler_BlockedRetried(t *testing.T) {
	app, db, repo, _ := dynSchedApp(t)
	if err := CreateStateFlowTables(db); err != nil {
		t.Fatal(err)
	}
	if err := app.RegisterFlow(rejectMemoFlow(repo.typeName)); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	ctx := context.Background()
	active := newDraft(t, repo, "Active")
	if err := repo.SetStatus(ctx, active.ID, Published); err != nil {
		t.Fatalf("publish active: %v", err)
	}
	due := newDraft(t, repo, "Due")
	if err := repo.ScheduleContent(ctx, due.ID, time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatalf("ScheduleContent: %v", err)
	}
	d := dynamicScheduler{app}
	bg := NewBackgroundContext("example.com")
	var next *time.Time
	out := capturedLog(func() {
		for i := 0; i < 3; i++ {
			n, nx, err := d.processScheduled(bg, time.Now().UTC())
			if err != nil || n != 0 {
				t.Fatalf("tick %d: published=%d err=%v, want 0 and nil", i, n, err)
			}
			next = nx
		}
	})
	if s := dynStatus(t, repo, due.ID).Status; s != Scheduled {
		t.Errorf("blocked item = %q, want scheduled", s)
	}
	if n := strings.Count(out, "blocked by the conflict policy"); n != 1 {
		t.Errorf("the Warn was written %d times over 3 ticks, want once:\n%s", n, out)
	}
	if next == nil || time.Until(*next) < 30*time.Second || time.Until(*next) > 90*time.Second {
		t.Errorf("next = %v, want about one retry interval from now", next)
	}
	if err := repo.SetStatus(ctx, active.ID, "archived"); err != nil {
		t.Fatalf("archive active: %v", err)
	}
	if n, _, err := d.processScheduled(bg, time.Now().UTC()); err != nil || n != 1 {
		t.Fatalf("after the active item left: published=%d err=%v, want 1", n, err)
	}
	if _, still := scheduledBlockedLogged.Load(repo.typeName + "/" + due.ID); still {
		t.Error("the once-per-item memory was not cleared when the item published")
	}
}

// A scheduled item whose URL another item has published stays scheduled and
// is logged once; it publishes once the URL is free.
func TestDynamicScheduler_SlugTakenRetried(t *testing.T) {
	app, db, repo, _ := dynSchedApp(t)
	ctx := context.Background()
	due := newDraft(t, repo, "Same title")
	if err := repo.ScheduleContent(ctx, due.ID, time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatalf("ScheduleContent: %v", err)
	}
	other := newDraft(t, repo, "Other")
	mustExecDB(t, db, "UPDATE smeldr_dynamic_content SET status = 'published', slug = '"+due.Slug+"' WHERE id = '"+other.ID+"'")
	d := dynamicScheduler{app}
	bg := NewBackgroundContext("example.com")
	out := capturedLog(func() {
		for i := 0; i < 2; i++ {
			if n, next, err := d.processScheduled(bg, time.Now().UTC()); err != nil || n != 0 || next == nil {
				t.Fatalf("tick %d: published=%d next=%v err=%v", i, n, next, err)
			}
		}
	})
	if n := strings.Count(out, "its URL is taken"); n != 1 {
		t.Errorf("the taken-URL Warn was written %d times, want once:\n%s", n, out)
	}
	mustExecDB(t, db, "UPDATE smeldr_dynamic_content SET status = 'archived' WHERE id = '"+other.ID+"'")
	if n, _, err := d.processScheduled(bg, time.Now().UTC()); err != nil || n != 1 {
		t.Fatalf("after the URL was freed: published=%d err=%v, want 1", n, err)
	}
	if _, still := scheduledBlockedLogged.Load("slug/" + repo.typeName + "/" + due.ID); still {
		t.Error("the taken-URL memory was not cleared")
	}
}

// With Signals on, a taken URL opens one conflict-detected Signal instead of
// the Warn.
func TestDynamicScheduler_SlugTakenSignal(t *testing.T) {
	app, db, _ := setupTransitionItemApp(t)
	app.ServeDynamicContent()
	app.signalConditions = true
	repo := defineDynSchedType(t, app, "memo", "/memos")
	ctx := context.Background()
	due := newDraft(t, repo, "Taken")
	if err := repo.ScheduleContent(ctx, due.ID, time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatalf("ScheduleContent: %v", err)
	}
	other := newDraft(t, repo, "Holder")
	mustExecDB(t, db, "UPDATE smeldr_dynamic_content SET status = 'published', slug = '"+due.Slug+"' WHERE id = '"+other.ID+"'")
	d := dynamicScheduler{app}
	bg := NewBackgroundContext("example.com")
	out := capturedLog(func() {
		for i := 0; i < 2; i++ {
			if _, _, err := d.processScheduled(bg, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
		}
	})
	if strings.Contains(out, "its URL is taken") {
		t.Errorf("with Signals on the block is a Signal, not a Warn:\n%s", out)
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM smeldr_signals WHERE signal_type = 'conflict-detected' AND subject_id = $1", due.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("conflict-detected Signals = %d, want 1", n)
	}
}

// failingQueryDB fails every query whose text contains match.
type failingQueryDB struct {
	DB
	match string
}

func (f failingQueryDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if strings.Contains(q, f.match) {
		return nil, errors.New("query refused")
	}
	return f.DB.QueryContext(ctx, q, args...)
}

func (f failingQueryDB) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	if strings.Contains(q, f.match) {
		return f.DB.QueryRowContext(ctx, "SELECT no_such_column FROM smeldr_dynamic_content")
	}
	return f.DB.QueryRowContext(ctx, q, args...)
}

// The error paths of the tick and of publishDue.
func TestDynamicScheduler_ErrorPaths(t *testing.T) {
	bg := NewBackgroundContext("example.com")
	now := func() time.Time { return time.Now().UTC() }

	t.Run("query error", func(t *testing.T) {
		app, db, _, _ := dynSchedApp(t)
		app.cfg.DB = failingQueryDB{db, "WHERE status = $1"}
		if _, _, err := (dynamicScheduler{app}).processScheduled(bg, now()); err == nil {
			t.Error("want the query error")
		}
	})

	t.Run("missing schema skips the type", func(t *testing.T) {
		app, db, repo, _ := dynSchedApp(t)
		n := newDraft(t, repo, "Orphan")
		if err := repo.ScheduleContent(context.Background(), n.ID, now().Add(-time.Minute)); err != nil {
			t.Fatal(err)
		}
		mustExecDB(t, db, "UPDATE smeldr_dynamic_content SET type_name = 'gone' WHERE id = '"+n.ID+"'")
		var published int
		out := capturedLog(func() {
			var err error
			published, _, err = (dynamicScheduler{app}).processScheduled(bg, now())
			if err != nil {
				t.Fatal(err)
			}
		})
		if published != 0 || !strings.Contains(out, "runtime type not found") {
			t.Errorf("published=%d, log:\n%s", published, out)
		}
	})

	t.Run("null scheduled_at is skipped", func(t *testing.T) {
		app, db, repo, _ := dynSchedApp(t)
		n := newDraft(t, repo, "No time")
		mustExecDB(t, db, "UPDATE smeldr_dynamic_content SET status = 'scheduled', scheduled_at = NULL WHERE id = '"+n.ID+"'")
		if p, next, err := (dynamicScheduler{app}).processScheduled(bg, now()); err != nil || p != 0 || next != nil {
			t.Errorf("published=%d next=%v err=%v, want 0, nil, nil", p, next, err)
		}
	})

	t.Run("row gone", func(t *testing.T) {
		_, _, repo, _ := dynSchedApp(t)
		out := capturedLog(func() {
			if got := repo.publishDue(bg, "no-such-id", now()); got != dueSkipped {
				t.Errorf("outcome = %v, want dueSkipped", got)
			}
		})
		if !strings.Contains(out, "could not be read") {
			t.Errorf("log:\n%s", out)
		}
	})

	t.Run("moved since the query", func(t *testing.T) {
		_, _, repo, _ := dynSchedApp(t)
		n := newDraft(t, repo, "Still draft")
		if got := repo.publishDue(bg, n.ID, now()); got != dueSkipped {
			t.Errorf("outcome = %v, want dueSkipped", got)
		}
		if s := dynStatus(t, repo, n.ID).Status; s != Draft {
			t.Errorf("status = %q, want draft untouched", s)
		}
	})

	t.Run("slug check error", func(t *testing.T) {
		_, db, repo, _ := dynSchedApp(t)
		n := newDraft(t, repo, "Slug check")
		if err := repo.ScheduleContent(context.Background(), n.ID, now().Add(-time.Minute)); err != nil {
			t.Fatal(err)
		}
		broken := *repo
		broken.db = failingQueryDB{db, "SELECT COUNT(*) FROM smeldr_dynamic_content WHERE type_name = $1 AND slug = $2"}
		out := capturedLog(func() {
			if got := broken.publishDue(bg, n.ID, now()); got != dueBlocked {
				t.Errorf("outcome = %v, want dueBlocked", got)
			}
		})
		if !strings.Contains(out, "slug check failed") {
			t.Errorf("log:\n%s", out)
		}
	})

	t.Run("conflict check error", func(t *testing.T) {
		app, db, repo, _ := dynSchedApp(t)
		if err := CreateStateFlowTables(db); err != nil {
			t.Fatal(err)
		}
		if err := app.RegisterFlow(rejectMemoFlow(repo.typeName)); err != nil {
			t.Fatal(err)
		}
		n := newDraft(t, repo, "Cancelled")
		if err := repo.ScheduleContent(context.Background(), n.ID, now().Add(-time.Minute)); err != nil {
			t.Fatal(err)
		}
		// Hold the type's lock so planConflict waits, with a context that is
		// already cancelled.
		release := acquireForTest(t, repo.typeName)
		defer release()
		cctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
		defer cancel()
		out := capturedLog(func() {
			if got := repo.publishDue(cancelledBackground{bg, cctx}, n.ID, now()); got != dueBlocked {
				t.Errorf("outcome = %v, want dueBlocked", got)
			}
		})
		if !strings.Contains(out, "conflict policy check failed") {
			t.Errorf("log:\n%s", out)
		}
	})

	t.Run("write error", func(t *testing.T) {
		_, db, repo, _ := dynSchedApp(t)
		n := newDraft(t, repo, "Write fails")
		if err := repo.ScheduleContent(context.Background(), n.ID, now().Add(-time.Minute)); err != nil {
			t.Fatal(err)
		}
		broken := *repo
		broken.db = failUpdateDB{db}
		out := capturedLog(func() {
			if got := broken.publishDue(bg, n.ID, now()); got != dueSkipped {
				t.Errorf("outcome = %v, want dueSkipped", got)
			}
		})
		if !strings.Contains(out, "failed to publish") {
			t.Errorf("log:\n%s", out)
		}
	})
}

// The guarded UPDATE: a row that left Scheduled between the read and the
// write is not written and not announced.
func TestDynamicTypeRepo_WriteStatusGuard(t *testing.T) {
	_, db, repo, store := dynSchedApp(t)
	n := newDraft(t, repo, "Raced")
	if err := repo.ScheduleContent(context.Background(), n.ID, time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	node := dynStatus(t, repo, n.ID)
	mustExecDB(t, db, "UPDATE smeldr_dynamic_content SET status = 'draft' WHERE id = '"+n.ID+"'")
	plan, err := planConflict(context.Background(), db, repo.typeName, string(Published), n.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.release()
	before := len(store.Appended())
	now := time.Now().UTC()
	written, err := repo.writeStatus(context.Background(), plan, node, Published, now, now, "", surfaceTrigger, true)
	if err != nil || written {
		t.Errorf("written=%v err=%v, want false, nil", written, err)
	}
	if s := dynStatus(t, repo, n.ID).Status; s != Draft {
		t.Errorf("status = %q, want the concurrent draft kept", s)
	}
	if len(store.Appended()) != before {
		t.Error("a write that did not happen was recorded")
	}
}

// ScheduleContent on an App's repository wakes the scheduler; one built
// without an App has no wake and does not fail.
func TestDynamicTypeRepo_ScheduleContentWakes(t *testing.T) {
	app, _, repo, _ := dynSchedApp(t)
	n := newDraft(t, repo, "Wake")
	if err := repo.ScheduleContent(context.Background(), n.ID, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-app.schedulerWake:
	default:
		t.Error("ScheduleContent did not wake the scheduler")
	}
	bare := NewDynamicTypeRepo(repo.db, repo.typeName, repo.schema)
	m := newDraft(t, bare, "No app")
	if err := bare.ScheduleContent(context.Background(), m.ID, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("without an App: %v", err)
	}
}
