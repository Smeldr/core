package smeldr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// wantNeedsTime asserts err is T1009's refusal, a ValidationError on
// ScheduledAt, and names the call that sets the time.
func wantNeedsTime(t *testing.T, err error, tool string) {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) || !strings.Contains(err.Error(), "ScheduledAt") || !strings.Contains(err.Error(), "without a scheduled_at") || !strings.Contains(err.Error(), tool) {
		t.Fatalf("err = %v; want a ValidationError on ScheduledAt naming %s", err, tool)
	}
}

// transition_item to scheduled on a compiled type: refused with no time,
// nothing written; allowed with one.
func TestScheduledNeedsTime_TransitionItemCompiled(t *testing.T) {
	app, db, repo := newTransitionHookApp(t, nil)
	ctx := TrustedRead(hookCtx())
	seed := seedPost(t, repo, "Item", Draft)
	_, err := app.TransitionItem(ctx, "testPost", seed.Slug, string(Scheduled))
	wantNeedsTime(t, err, "schedule_test_post")
	if got, _ := repo.FindBySlug(context.Background(), seed.Slug); got.Status != Draft {
		t.Errorf("status = %q after the refusal, want draft", got.Status)
	}
	mustExecDB(t, db, "UPDATE test_posts SET scheduled_at = '"+time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)+"' WHERE id = '"+seed.ID+"'")
	if _, err := app.TransitionItem(ctx, "testPost", seed.Slug, string(Scheduled)); err != nil {
		t.Fatalf("with a time: %v", err)
	}
}

// The table check: no column refuses, a lookup failure is allowed and logged,
// a vanished row is allowed (the UPDATE then changes nothing).
func TestScheduledNeedsTime_TableCheck(t *testing.T) {
	db := newSQLiteDB(t)
	mustExecDB(t, db, `CREATE TABLE no_times (id TEXT PRIMARY KEY, status TEXT NOT NULL)`)
	mustExecDB(t, db, `INSERT INTO no_times (id, status) VALUES ('a', 'draft')`)
	ctx := context.Background()

	wantNeedsTime(t, requireTableScheduledTime(ctx, db, "no_times", "NoTime", "a"), "schedule_no_time")

	var err error
	out := capturedLog(func() {
		err = requireTableScheduledTime(ctx, failingQueryDB{db, "scheduled_at IS NULL"}, "no_times", "NoTime", "a")
	})
	if err != nil || !strings.Contains(out, "scheduled_at check failed") {
		t.Errorf("a failed lookup: err = %v, log:\n%s; want allowed and logged", err, out)
	}

	mustExecDB(t, db, `CREATE TABLE some_times (id TEXT PRIMARY KEY, status TEXT NOT NULL, scheduled_at DATETIME)`)
	if err := requireTableScheduledTime(ctx, db, "some_times", "SomeTime", "gone"); err != nil {
		t.Errorf("a vanished row: %v, want allowed", err)
	}
}

// transition_item to scheduled on a runtime-defined type with no time.
func TestScheduledNeedsTime_Dynamic(t *testing.T) {
	app, _, repo, _ := dynSchedApp(t)
	n := newDraft(t, repo, "No time")
	_, err := app.TransitionItem(TrustedRead(context.Background()), repo.typeName, n.Slug, string(Scheduled))
	wantNeedsTime(t, err, "schedule_content")
	if s := dynStatus(t, repo, n.ID).Status; s != Draft {
		t.Errorf("status = %q after the refusal, want draft", s)
	}
	if err := repo.ScheduleContent(context.Background(), n.ID, time.Now().Add(time.Hour)); err != nil {
		t.Errorf("ScheduleContent, which sets the time, is unchanged: %v", err)
	}
}

// A PUT to scheduled with no time is a 422 naming the tool; with one a 200.
func TestScheduledNeedsTime_PUT(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	existing := seedPost(t, repo, "My Post", Draft)
	m := newTestModule(repo)
	put := func(fields map[string]any) *httptest.ResponseRecorder {
		body, _ := json.Marshal(fields)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPut, "/testposts/"+existing.Slug, bytes.NewReader(body))
		r.SetPathValue("slug", existing.Slug)
		m.updateHandler(w, withUser(r, editorUser()))
		return w
	}
	if w := put(map[string]any{"title": "My Post", "status": string(Scheduled)}); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "schedule_test_post") {
		t.Errorf("no time: %d %s; want 422 naming schedule_test_post", w.Code, w.Body.String())
	}
	if w := put(map[string]any{"title": "My Post", "status": string(Scheduled), "ScheduledAt": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}); w.Code != http.StatusOK {
		t.Errorf("with a time: %d %s; want 200", w.Code, w.Body.String())
	}
}

// create_<type> in scheduled with no time, and update_<type> clearing the time
// of a scheduled item, are refused.
func TestScheduledNeedsTime_CreateAndUpdate(t *testing.T) {
	_, _, sm, _ := siteShapedApp(t, nil)
	_, err := sm.MCPCreate(adminCtx(), map[string]any{"slug": "early", "title": "Too early", "excerpt": "x", "body": "y", "Status": string(Scheduled)})
	wantNeedsTime(t, err, "schedule_story_shape")

	newStory(t, sm, "kept")
	if err := sm.MCPSchedule(adminCtx(), "kept", time.Now().Add(time.Hour), ""); err != nil {
		t.Fatal(err)
	}
	_, err = sm.MCPUpdate(adminCtx(), "kept", map[string]any{"ScheduledAt": nil})
	wantNeedsTime(t, err, "schedule_story_shape")
}

// DrainEvalQueue skips a queued move to scheduled whose item has no time, logs
// it, and applies the one whose item has a time.
func TestScheduledNeedsTime_DrainEvalQueue(t *testing.T) {
	db := newMigratedDB(t)
	mustExecDB(t, db, `CREATE TABLE drain_items (id TEXT PRIMARY KEY, status TEXT NOT NULL, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_actor TEXT NOT NULL DEFAULT '', scheduled_at DATETIME)`)
	mustExecDB(t, db, `INSERT INTO drain_items (id, status) VALUES ('none', 'draft')`)
	mustExecDB(t, db, `INSERT INTO drain_items (id, status, scheduled_at) VALUES ('timed', 'draft', '`+time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)+`')`)
	for _, id := range []string{"none", "timed"} {
		mustExecDB(t, db, `INSERT INTO smeldr_eval_queue (id, type_name, item_id, to_state, eval_at)
			VALUES ('`+NewID()+`', 'DrainItem', '`+id+`', 'scheduled', datetime('now', '-1 second'))`)
	}
	app := &App{cfg: Config{DB: db}}
	var walked, triggered, skipped int
	var err error
	out := capturedLog(func() { walked, triggered, skipped, err = app.DrainEvalQueue(context.Background()) })
	if err != nil || walked != 2 || triggered != 1 || skipped != 1 {
		t.Fatalf("drain = (%d, %d, %d, %v), want (2, 1, 1, nil)\n%s", walked, triggered, skipped, err, out)
	}
	status := func(id string) string {
		var s string
		if err := db.QueryRow(`SELECT status FROM drain_items WHERE id = ?`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if status("none") != "draft" || status("timed") != "scheduled" {
		t.Errorf("none = %q, timed = %q; want draft and scheduled", status("none"), status("timed"))
	}
	if !strings.Contains(out, "has no scheduled_at") {
		t.Errorf("the skip must be logged:\n%s", out)
	}
}

// The scheduler's first tick reports Scheduled items with no time, once per
// module and per runtime type; later ticks are quiet.
func TestScheduledNeedsTime_StartupWarn(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	if err := repo.Save(context.Background(), &testPost{Node: Node{ID: NewID(), Slug: "stuck", Status: Scheduled}, Title: "Stuck"}); err != nil {
		t.Fatal(err)
	}
	m := newTestModule(repo)
	bg := NewBackgroundContext("example.com")
	first := capturedLog(func() { m.processScheduled(bg, time.Now().UTC()) })
	second := capturedLog(func() { m.processScheduled(bg, time.Now().UTC()) })
	if !strings.Contains(first, "will never be published") || !strings.Contains(first, "count=1") {
		t.Errorf("first tick must report the stuck item:\n%s", first)
	}
	if strings.Contains(second, "will never be published") {
		t.Errorf("a later tick must be quiet:\n%s", second)
	}

	app, db, drepo, _ := dynSchedApp(t)
	n := newDraft(t, drepo, "Stuck dynamic")
	mustExecDB(t, db, "UPDATE smeldr_dynamic_content SET status = 'scheduled', scheduled_at = NULL WHERE id = '"+n.ID+"'")
	d := dynamicScheduler{app}
	first = capturedLog(func() { d.processScheduled(bg, time.Now().UTC()) })
	second = capturedLog(func() { d.processScheduled(bg, time.Now().UTC()) })
	if !strings.Contains(first, "will never be published") || !strings.Contains(first, "type="+drepo.typeName) {
		t.Errorf("first dynamic tick must report the stuck item:\n%s", first)
	}
	if strings.Contains(second, "will never be published") {
		t.Errorf("a later dynamic tick must be quiet:\n%s", second)
	}
}
