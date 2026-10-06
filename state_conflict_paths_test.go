// AGPL-3.0-or-later

package smeldr

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// The conflict policy on the status writers that did not apply it before
// (task_plan item-75): HTTP PUT, the scheduler, the eval-queue drain and
// DynamicTypeRepo.ScheduleContent.

func signalPolicyApp(t *testing.T, policy ConflictPolicy) (*sql.DB, *RelationStore, *fakeProvenanceStore) {
	t.Helper()
	app, db, store := edgeTestApp(t, "read", signalSupersedeStates, signalSupersedeTransitions)
	upsertTestKind(t, store, "supersedes", "Signal", "Signal")
	if policy != ConflictSupersede {
		if err := app.RegisterFlow(StateFlow{
			Name: "signal-protocol", TypeName: "Signal", ActiveState: "read", ConflictPolicy: policy,
			States: signalSupersedeStates, Transitions: signalSupersedeTransitions,
		}); err != nil {
			t.Fatalf("RegisterFlow: %v", err)
		}
	}
	return db, store, &fakeProvenanceStore{}
}

var signalPublishStates = []State{
	{Name: "pending", IsInitial: true}, {Name: "scheduled"}, {Name: "published"}, {Name: "superseded"},
}

var signalPublishTransitions = []Transition{
	{From: "pending", To: "scheduled"}, {From: "scheduled", To: "published"}, {From: "published", To: "superseded"},
}

// signalSchedApp is signalPolicyApp for a flow whose ActiveState is "published",
// the state the scheduler publishes into.
func signalSchedApp(t *testing.T, policy ConflictPolicy) (*sql.DB, *RelationStore, *fakeProvenanceStore) {
	t.Helper()
	app, db, store := edgeTestApp(t, "published", signalPublishStates, signalPublishTransitions)
	upsertTestKind(t, store, "supersedes", "Signal", "Signal")
	if policy != ConflictSupersede {
		if err := app.RegisterFlow(StateFlow{
			Name: "signal-protocol", TypeName: "Signal", ActiveState: "published", ConflictPolicy: policy,
			States: signalPublishStates, Transitions: signalPublishTransitions,
		}); err != nil {
			t.Fatalf("RegisterFlow: %v", err)
		}
	}
	return db, store, &fakeProvenanceStore{}
}

func signalModule(db *sql.DB, store *RelationStore, prov ProvenanceStore, repo Repository[*Signal]) *Module[*Signal] {
	if repo == nil {
		repo = NewSQLRepo[*Signal](db, Table("smeldr_signals"))
	}
	m := NewModule[*Signal]((*Signal)(nil), At("/signals"), MCP(MCPRead, MCPWrite), Repo(repo))
	m.setDB(db)
	m.setRelationStore(store)
	m.setProvenanceStore(prov)
	return m
}

func putSignal(m *Module[*Signal], slug, status string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]any{
		"Status": status, "sender": "s", "receiver": "r", "signal_type": "t", "message": "m",
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/signals/"+slug, bytes.NewReader(body))
	r.SetPathValue("slug", slug)
	r = withUser(r, editorUser())
	m.updateHandler(w, r)
	return w
}

func TestHTTPPut_Reject_ConflictIs409AndNothingIsSaved(t *testing.T) {
	db, store, prov := signalPolicyApp(t, ConflictReject)
	insertSignal(t, db, "old", "old-slug", "read")
	insertSignal(t, db, "new", "new-slug", "pending")
	m := signalModule(db, store, prov, nil)

	w := putSignal(m, "new-slug", "read")
	if w.Code != http.StatusConflict {
		t.Fatalf("PUT = %d, want 409\n%s", w.Code, w.Body.String())
	}
	if s := signalStatusOf(t, db, "new"); s != "pending" {
		t.Errorf("rejected item = %q, want it still pending", s)
	}
	if s := signalStatusOf(t, db, "old"); s != "read" {
		t.Errorf("old = %q, want untouched", s)
	}
}

func TestHTTPPut_Supersede_SupersedesTheActiveItem(t *testing.T) {
	db, store, prov := signalPolicyApp(t, ConflictSupersede)
	insertSignal(t, db, "old", "old-slug", "read")
	insertSignal(t, db, "new", "new-slug", "pending")
	m := signalModule(db, store, prov, nil)

	w := putSignal(m, "new-slug", "read")
	if w.Code != http.StatusOK {
		t.Fatalf("PUT = %d, want 200\n%s", w.Code, w.Body.String())
	}
	if s := signalStatusOf(t, db, "new"); s != "read" {
		t.Errorf("winner = %q, want read", s)
	}
	if s := signalStatusOf(t, db, "old"); s != "superseded" {
		t.Errorf("old = %q, want superseded", s)
	}
	if n := len(edgesFrom(t, store, "Signal", "new")); n != 1 {
		t.Errorf("%d supersedes edges, want 1", n)
	}
	var recorded bool
	for _, rec := range prov.Appended() {
		if rec.ToState == "superseded" && rec.Surface == surfaceHTTP {
			recorded = true
		}
	}
	if !recorded {
		t.Error("no provenance record for the superseded item with surface http")
	}
}

// TestHTTPPut_SameStatus_NeverPlans: a content-only PUT takes no lock and runs no
// policy, even while another transition of the type holds the lock.
func TestHTTPPut_SameStatus_NeverPlans(t *testing.T) {
	db, store, prov := signalPolicyApp(t, ConflictReject)
	insertSignal(t, db, "old", "old-slug", "read")
	m := signalModule(db, store, prov, nil)
	prev := conflictLockWait
	conflictLockWait = 3 * time.Second
	t.Cleanup(func() { conflictLockWait = prev })

	// Another transition of the type holds the lock for the whole test.
	holder := acquireForTest(t, "Signal")
	defer holder()

	start := time.Now()
	w := putSignal(m, "old-slug", "read")
	if w.Code != http.StatusOK {
		t.Fatalf("a PUT that keeps the status = %d, want 200\n%s", w.Code, w.Body.String())
	}
	if time.Since(start) > time.Second {
		t.Errorf("a same-status PUT waited %v for the conflict lock", time.Since(start))
	}
}

func acquireForTest(t *testing.T, typeName string) func() {
	t.Helper()
	release, held, err := acquireConflictLock(context.Background(), typeName)
	if err != nil || !held {
		t.Fatalf("acquire %s: held=%v err=%v", typeName, held, err)
	}
	return release
}

// TestHTTPPut_ConcurrentWinners: two simultaneous PUTs into the active state, one
// active item afterwards.
func TestHTTPPut_ConcurrentWinners(t *testing.T) {
	db, store, prov := signalPolicyApp(t, ConflictSupersede)
	insertSignal(t, db, "old", "old-slug", "read")
	insertSignal(t, db, "new-a", "new-a-slug", "pending")
	insertSignal(t, db, "new-b", "new-b-slug", "pending")
	m := signalModule(db, store, prov,
		barrierSaveRepo[*Signal]{Repository: NewSQLRepo[*Signal](db, Table("smeldr_signals")), b: newSaveBarrier(2, 400*time.Millisecond)})

	start := time.Now()
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i, slug := range []string{"new-a-slug", "new-b-slug"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = putSignal(m, slug, "read").Code
		}()
	}
	wg.Wait()
	assertRaceFast(t, start)
	if codes[0] != http.StatusOK || codes[1] != http.StatusOK {
		t.Fatalf("both PUTs should succeed under supersede: %v", codes)
	}
	if n := countStatus(t, db, "read"); n != 1 {
		t.Errorf("%d items in the active state, want exactly 1", n)
	}
}

func scheduleSignal(t *testing.T, db *sql.DB, id, slug string) {
	t.Helper()
	insertSignal(t, db, id, slug, "scheduled")
	if _, err := db.ExecContext(context.Background(), `UPDATE smeldr_signals SET scheduled_at = $1 WHERE id = $2`, time.Now().UTC().Add(-time.Minute), id); err != nil {
		t.Fatalf("set scheduled_at: %v", err)
	}
}

func TestScheduler_Reject_ItemStaysScheduledAndIsLoggedOnce(t *testing.T) {
	db, store, prov := signalSchedApp(t, ConflictReject)
	insertSignal(t, db, "old", "old-slug", "published")
	scheduleSignal(t, db, "due", "due-slug")
	m := signalModule(db, store, prov, nil)
	ctx := NewBackgroundContext("example.com")

	var out string
	var next *time.Time
	out = capturedLog(func() {
		for i := 0; i < 3; i++ {
			published, n, err := m.processScheduled(ctx, time.Now().UTC())
			if err != nil || published != 0 {
				t.Fatalf("tick %d: published=%d err=%v, want 0 and nil", i, published, err)
			}
			next = n
		}
	})
	if s := signalStatusOf(t, db, "due"); s != "scheduled" {
		t.Errorf("blocked item = %q, want it still scheduled", s)
	}
	if n := strings.Count(out, "blocked by the conflict policy"); n != 1 {
		t.Errorf("the Warn was written %d times over 3 ticks, want once:\n%s", n, out)
	}
	if !strings.Contains(out, "retried every tick") {
		t.Errorf("the line must say what happens next:\n%s", out)
	}
	if next == nil || time.Until(*next) < 30*time.Second || time.Until(*next) > 90*time.Second {
		t.Errorf("next wake = %v, want about one retry interval from now, never a past time", next)
	}

	// The active item leaves the state: the next tick publishes it.
	mustExecDB(t, db, `UPDATE smeldr_signals SET status = 'superseded' WHERE id = 'old'`)
	published, _, err := m.processScheduled(ctx, time.Now().UTC())
	if err != nil || published != 1 {
		t.Fatalf("after the active item left: published=%d err=%v, want 1", published, err)
	}
	if s := signalStatusOf(t, db, "due"); s != "published" {
		t.Errorf("item = %q after its turn, want read", s)
	}
	if _, still := scheduledBlockedLogged.Load("Signal/due"); still {
		t.Error("the once-per-item memory was not cleared when the item published")
	}
}

func TestScheduler_Supersede_SupersedesTheActiveItem(t *testing.T) {
	db, store, prov := signalSchedApp(t, ConflictSupersede)
	insertSignal(t, db, "old", "old-slug", "published")
	scheduleSignal(t, db, "due", "due-slug")
	m := signalModule(db, store, prov, nil)

	published, _, err := m.processScheduled(NewBackgroundContext("example.com"), time.Now().UTC())
	if err != nil || published != 1 {
		t.Fatalf("published=%d err=%v, want 1", published, err)
	}
	if s := signalStatusOf(t, db, "old"); s != "superseded" {
		t.Errorf("old = %q, want superseded", s)
	}
	if n := len(edgesFrom(t, store, "Signal", "due")); n != 1 {
		t.Errorf("%d supersedes edges, want 1", n)
	}
	var viaTrigger bool
	for _, rec := range prov.Appended() {
		if rec.ToState == "superseded" && rec.Surface == surfaceTrigger {
			viaTrigger = true
		}
	}
	if !viaTrigger {
		t.Error("the superseded item's record must carry surface trigger")
	}
}

// TestScheduler_PolicyCheckFailsStaysScheduled: a failure that is not a conflict
// (a context cancelled while waiting for the lock) leaves the item scheduled and
// is logged, never published unguarded.
func TestScheduler_PolicyCheckFailsStaysScheduled(t *testing.T) {
	db, store, prov := signalSchedApp(t, ConflictReject)
	scheduleSignal(t, db, "due", "due-slug")
	m := signalModule(db, store, prov, nil)
	release := acquireForTest(t, "Signal")
	defer release()

	// The deadline falls while the plan waits for the lock: the probes before it
	// must still succeed (a context that is dead on arrival makes the very first
	// SQLite probe fail, which is the existing fail-open "not SQLite" path).
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	bg := cancelledBackground{NewBackgroundContext("example.com"), ctx}
	var published int
	out := capturedLog(func() {
		published, _, _ = m.processScheduled(bg, time.Now().UTC())
	})
	if published != 0 {
		t.Errorf("published = %d, want 0", published)
	}
	if s := signalStatusOf(t, db, "due"); s != "scheduled" {
		t.Errorf("item = %q, want it still scheduled", s)
	}
	if !strings.Contains(out, "conflict policy check failed") {
		t.Errorf("the failure must be logged:\n%s", out)
	}
}

// cancelledBackground is a background Context whose Done/Err report cancelled.
type cancelledBackground struct {
	Context
	c context.Context
}

func (b cancelledBackground) Done() <-chan struct{} { return b.c.Done() }
func (b cancelledBackground) Err() error            { return b.c.Err() }

func queueDrain(t *testing.T, db *sql.DB, itemID string) {
	t.Helper()
	mustExecDB(t, db, `INSERT INTO smeldr_eval_queue (id, type_name, item_id, to_state, eval_at)
		VALUES ('`+NewID()+`', 'ConflictType', '`+itemID+`', 'published', datetime('now', '-1 second'))`)
}

func TestDrainEvalQueue_Reject_SkipsAndDeletesTheRow(t *testing.T) {
	db := newMigratedDB(t)
	createConflictItemTable(t, db, true, ConflictReject)
	insertConflictItem(t, db, "a", "published")
	insertConflictItem(t, db, "b", "draft")
	queueDrain(t, db, "b")

	app := &App{cfg: Config{DB: db}}
	var walked, triggered, skipped int
	var err error
	out := capturedLog(func() { walked, triggered, skipped, err = app.DrainEvalQueue(context.Background()) })
	if err != nil || walked != 1 || triggered != 0 || skipped != 1 {
		t.Fatalf("drain = (%d, %d, %d, %v), want (1, 0, 1, nil)", walked, triggered, skipped, err)
	}
	if s := conflictItemStatus(t, db, "b"); s != "draft" {
		t.Errorf("rejected item = %q, want draft", s)
	}
	if !strings.Contains(out, "skipped by the conflict policy, not re-queued") {
		t.Errorf("the skip must be logged with what happens next:\n%s", out)
	}
	if n := countRows(t, db, "smeldr_eval_queue"); n != 0 {
		t.Errorf("%d queue rows left, want the row deleted as for any blocked transition", n)
	}
}

func TestDrainEvalQueue_Supersede_SupersedesTheActiveItem(t *testing.T) {
	db := newMigratedDB(t)
	createConflictItemTable(t, db, true, ConflictSupersede)
	insertConflictItem(t, db, "a", "published")
	insertConflictItem(t, db, "b", "draft")
	queueDrain(t, db, "b")

	prov := &fakeProvenanceStore{}
	app := &App{cfg: Config{DB: db}, provenanceStore: prov}
	walked, triggered, skipped, err := app.DrainEvalQueue(context.Background())
	if err != nil || walked != 1 || triggered != 1 || skipped != 0 {
		t.Fatalf("drain = (%d, %d, %d, %v), want (1, 1, 0, nil)", walked, triggered, skipped, err)
	}
	if s := conflictItemStatus(t, db, "b"); s != "published" {
		t.Errorf("winner = %q, want published", s)
	}
	if s := conflictItemStatus(t, db, "a"); s != "superseded" {
		t.Errorf("old = %q, want superseded", s)
	}
	// The superseded item's record and last_actor name the drain, like the winner's own.
	if got := conflictItemLastActor(t, db, "a"); got != "drain-eval-queue" {
		t.Errorf("superseded item last_actor = %q, want drain-eval-queue", got)
	}
	var named bool
	for _, rec := range prov.Appended() {
		if rec.ToState == "superseded" {
			named = rec.ActorID == "drain-eval-queue" && rec.ActorKind == "job"
		}
	}
	if !named {
		t.Error("the superseded item's record must name actor drain-eval-queue, kind job")
	}
}

// TestScheduleContent_AppliesThePolicy: ScheduleContent into a flow whose
// ActiveState is "scheduled" is rejected for the second item and supersedes under
// that policy.
func TestScheduleContent_AppliesThePolicy(t *testing.T) {
	for _, tc := range []struct {
		policy ConflictPolicy
		wantOK bool
	}{{ConflictReject, false}, {ConflictSupersede, true}} {
		t.Run(string(tc.policy), func(t *testing.T) {
			app, db, _, _ := setupProvenanceTransitionApp(t)
			typeName, slugA := defineProvenanceDynamicType(t, app, db, "schedpol"+string(tc.policy))
			repo, err := app.DynamicContentRepo(typeName)
			if err != nil {
				t.Fatalf("DynamicContentRepo: %v", err)
			}
			a, err := repo.GetBySlug(context.Background(), slugA)
			if err != nil {
				t.Fatalf("GetBySlug: %v", err)
			}
			b, err := repo.CreateDraft(context.Background(), map[string]any{"Title": "second"})
			if err != nil {
				t.Fatalf("CreateDraft: %v", err)
			}
			if err := app.RegisterFlow(StateFlow{
				Name: typeName + "-flow", TypeName: typeName, ActiveState: "scheduled", ConflictPolicy: tc.policy,
				States:      []State{{Name: "draft", IsInitial: true}, {Name: "scheduled"}, {Name: "superseded"}},
				Transitions: []Transition{{From: "draft", To: "scheduled"}, {From: "scheduled", To: "superseded"}},
			}); err != nil {
				t.Fatalf("RegisterFlow: %v", err)
			}
			ctx := NewTestContext(User{ID: "u-s", Roles: []Role{Editor}})
			when := time.Now().UTC().Add(time.Hour)
			if err := repo.ScheduleContent(ctx, a.ID, when); err != nil {
				t.Fatalf("first ScheduleContent: %v", err)
			}
			err = repo.ScheduleContent(ctx, b.ID, when)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("second ScheduleContent under supersede: %v", err)
				}
				var status string
				_ = db.QueryRowContext(context.Background(), `SELECT status FROM smeldr_dynamic_content WHERE id = ?`, a.ID).Scan(&status)
				if status != "superseded" {
					t.Errorf("first item = %q, want superseded", status)
				}
				return
			}
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("second ScheduleContent under reject = %v, want ErrConflict", err)
			}
			var status string
			_ = db.QueryRowContext(context.Background(), `SELECT status FROM smeldr_dynamic_content WHERE id = ?`, b.ID).Scan(&status)
			if status != "draft" {
				t.Errorf("rejected item = %q, want it still draft", status)
			}
		})
	}
}
