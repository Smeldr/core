// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// d107Counter counts named occurrences from concurrent goroutines.
type d107Counter struct {
	mu sync.Mutex
	n  map[string]int
}

func (c *d107Counter) add(k string) {
	c.mu.Lock()
	if c.n == nil {
		c.n = map[string]int{}
	}
	c.n[k]++
	c.mu.Unlock()
}

func (c *d107Counter) get(k string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n[k]
}

// d107Env is an App with a testPost module over SQLite, a flow for it whose
// published state holds standing, provenance, the event stream and counters
// for every App-level consumer of a status change: the bus ([App.OnSignal]),
// the listeners ([App.AddSignalListener]) and the stream. Every key is
// "<event>:<item id>".
type d107Env struct {
	app    *App
	db     *sql.DB
	m      *Module[*testPost]
	repo   *SQLRepo[*testPost]
	prov   *fakeProvenanceStore
	bus    *d107Counter
	listen *d107Counter
	stream *d107Counter
	ch     chan []byte
}

func newD107Env(t *testing.T, opts ...Option) *d107Env {
	t.Helper()
	return newD107EnvWrapped(t, nil, opts...)
}

// newD107EnvWrapped is newD107Env with the module's repository wrapped by wrap
// (the env's repo field stays the plain SQLRepo, for seeding).
func newD107EnvWrapped(t *testing.T, wrap func(Repository[*testPost]) Repository[*testPost], opts ...Option) *d107Env {
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
	if err := CreateStateFlowTables(db); err != nil {
		t.Fatalf("CreateStateFlowTables: %v", err)
	}
	env := &d107Env{db: db, prov: &fakeProvenanceStore{}, bus: &d107Counter{}, listen: &d107Counter{}, stream: &d107Counter{}}
	env.repo = NewSQLRepo[*testPost](db)
	env.app = New(MustConfig(Config{BaseURL: "https://example.com", Secret: []byte(transitionItemTestSecret), DB: db}))
	if err := env.app.RegisterFlow(StateFlow{
		Name: "d107-posts", TypeName: "testPost",
		States: []State{
			{Name: "draft", IsInitial: true}, {Name: "scheduled"},
			{Name: "published", Standing: StandingHolds}, {Name: "archived"},
			{Name: "quiet", SuppressesSignals: true},
		},
		Transitions: []Transition{
			{From: "draft", To: "published"}, {From: "draft", To: "scheduled"}, {From: "scheduled", To: "published"},
			{From: "published", To: "archived"}, {From: "published", To: "draft"}, {From: "draft", To: "archived"},
			{From: "draft", To: "quiet"},
		},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	env.app.Provenance(env.prov)
	env.app.EventStream()
	for _, sig := range []LifecycleEvent{AfterUpdate, AfterPublish, AfterUnpublish, AfterArchive, AfterSchedule} {
		s := sig
		env.app.OnSignal(s, func(_ context.Context, ev SignalEvent) error {
			env.bus.add(string(s) + ":" + ev.NodeID)
			return nil
		})
	}
	env.app.AddSignalListener(func(sig LifecycleEvent, _ string, item any) {
		env.listen.add(string(sig) + ":" + nodeIDOf(item))
	})
	var repo Repository[*testPost] = env.repo
	if wrap != nil {
		repo = wrap(env.repo)
	}
	env.m = newTestModule(repo, opts...)
	env.app.Content(env.m)
	env.app.Handler()
	ch, err := env.app.eventBroadcaster.subscribe("watcher", eventStreamChannelAll)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	t.Cleanup(func() { env.app.eventBroadcaster.unsubscribe(ch) })
	env.ch = ch
	return env
}

// pump moves every stream line waiting on the channel into the stream counter.
func (e *d107Env) pump(t *testing.T) {
	t.Helper()
	for {
		select {
		case raw := <-e.ch:
			var p WebhookEventPayload
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Fatalf("unmarshal %q: %v", raw, err)
			}
			var d struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(p.Data, &d)
			e.stream.add(p.Event + ":" + d.ID)
		default:
			return
		}
	}
}

// settle waits until the asynchronous consumers reached want (keys of the
// form "bus|listen|stream <event>:<id>") or a deadline passed, then a short
// while longer so an extra delivery would show.
func (e *d107Env) settle(t *testing.T, want map[string]int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		e.pump(t)
		done := true
		for k, n := range want {
			if e.count(k) < n {
				done = false
				break
			}
		}
		if done {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(80 * time.Millisecond)
	e.pump(t)
}

func (e *d107Env) count(key string) int {
	where, k, _ := strings.Cut(key, " ")
	switch where {
	case "bus":
		return e.bus.get(k)
	case "listen":
		return e.listen.get(k)
	default:
		return e.stream.get(k)
	}
}

// verbs counts the provenance records of id by verb.
func (e *d107Env) verbs(id string) map[string]int {
	out := map[string]int{}
	for _, r := range e.prov.Appended() {
		if r.SubjectID == id {
			out[r.Verb]++
		}
	}
	return out
}

// expectation is what one status change of item id from one state to another
// must give every App-level consumer (D107).
func expectation(typeName, id string, from, to Status, milestones, edited bool) map[string]int {
	lower := strings.ToLower(typeName)
	want := map[string]int{
		"stream " + lower + ".transitioned:" + id:      1,
		"listen " + string(AfterTransition) + ":" + id: 1,
		"bus " + string(AfterUpdate) + ":" + id:        0,
		"stream " + lower + ".updated:" + id:           0,
	}
	for _, sig := range []LifecycleEvent{AfterPublish, AfterUnpublish, AfterArchive, AfterSchedule} {
		want["bus "+string(sig)+":"+id] = 0
		suffix, _ := signalToEventSuffix(sig)
		want["stream "+lower+"."+suffix+":"+id] = 0
	}
	if milestones {
		for _, sig := range statusSignals(from, to) {
			want["bus "+string(sig)+":"+id] = 1
			suffix, _ := signalToEventSuffix(sig)
			want["stream "+lower+"."+suffix+":"+id] = 1
		}
	}
	if edited {
		want["bus "+string(AfterUpdate)+":"+id] = 1
		want["stream "+lower+".updated:"+id] = 1
	}
	return want
}

func (e *d107Env) check(t *testing.T, want map[string]int) {
	t.Helper()
	positive := map[string]int{}
	for k, n := range want {
		if n > 0 {
			positive[k] = n
		}
	}
	e.settle(t, positive)
	for k, n := range want {
		if got := e.count(k); got != n {
			t.Errorf("%s = %d; want %d", k, got, n)
		}
	}
}

// checkRecord asserts one transition record (actor, surface and reason as
// given) and the standing events the move calls for.
func (e *d107Env) checkRecord(t *testing.T, id string, from, to Status, actor, surface, reason string, standing string) {
	t.Helper()
	var transitions []ProvenanceRecord
	for _, r := range e.prov.Appended() {
		if r.SubjectID == id && r.Verb == "transition" {
			transitions = append(transitions, r)
		}
	}
	if len(transitions) != 1 {
		t.Fatalf("transition records = %d (%+v); want 1", len(transitions), transitions)
	}
	r := transitions[0]
	if r.FromState != string(from) || r.ToState != string(to) || r.ActorID != actor || r.Surface != surface || r.Reason != reason {
		t.Errorf("record = %+v; want %s->%s by %q via %q, reason %q", r, from, to, actor, surface, reason)
	}
	v := e.verbs(id)
	began, ended := 0, 0
	switch standing {
	case "began":
		began = 1
	case "ended":
		ended = 1
	}
	if v[verbStandingBegan] != began || v[verbStandingEnded] != ended {
		t.Errorf("standing events = %v; want began %d, ended %d", v, began, ended)
	}
}

// seed stores a testPost in status.
func (e *d107Env) seed(t *testing.T, title string, status Status) *testPost {
	t.Helper()
	p := seedPost(t, e.repo, title, status)
	if status == Published || status == Archived {
		// A seeded item never went through a transition: no standing row
		// unless the test moves it.
		if _, err := e.db.Exec(`DELETE FROM smeldr_standing WHERE subject_id = ?`, p.ID); err != nil {
			t.Fatalf("clear standing: %v", err)
		}
	}
	return p
}

// TestStatusChange_OneRecordOneEventPerPath is D107's per-path table: every
// status change, through every door, gives exactly one provenance record and
// standing write, one "<type>.transitioned" event, the milestones once each,
// one AfterTransition, and no "updated" unless a PUT also changed content.
func TestStatusChange_OneRecordOneEventPerPath(t *testing.T) {
	type path func(t *testing.T, e *d107Env, seed *testPost, to Status) (actor, surface, reason string)
	put := func(title string) path {
		return func(t *testing.T, e *d107Env, seed *testPost, to Status) (string, string, string) {
			if title == "" {
				title = seed.Title
			}
			if err := serveWrite(e.m.updateHandler, http.MethodPut, "/testposts/"+seed.Slug, seed.Slug,
				map[string]any{"Title": title, "Status": string(to)}); err != nil {
				t.Fatalf("PUT: %v", err)
			}
			return "editor-1", surfaceHTTP, ""
		}
	}
	publish := func(t *testing.T, e *d107Env, seed *testPost, _ Status) (string, string, string) {
		if err := e.m.MCPPublish(hookCtx(), seed.Slug, "go live"); err != nil {
			t.Fatalf("MCPPublish: %v", err)
		}
		return "editor-1", surfaceMCP, "go live"
	}
	schedule := func(t *testing.T, e *d107Env, seed *testPost, _ Status) (string, string, string) {
		if err := e.m.MCPSchedule(hookCtx(), seed.Slug, time.Now().Add(time.Hour), "later"); err != nil {
			t.Fatalf("MCPSchedule: %v", err)
		}
		return "editor-1", surfaceMCP, "later"
	}
	archive := func(t *testing.T, e *d107Env, seed *testPost, _ Status) (string, string, string) {
		if err := e.m.MCPArchive(hookCtx(), seed.Slug, "done"); err != nil {
			t.Fatalf("MCPArchive: %v", err)
		}
		return "editor-1", surfaceMCP, "done"
	}
	scheduler := func(t *testing.T, e *d107Env, seed *testPost, _ Status) (string, string, string) {
		if got := e.m.publishDue(NewBackgroundContext("example.com"), seed, time.Now()); got != duePublished {
			t.Fatalf("publishDue = %v", got)
		}
		return "", surfaceTrigger, ""
	}
	transitionItem := func(t *testing.T, e *d107Env, seed *testPost, to Status) (string, string, string) {
		if _, err := e.app.TransitionItemVia(hookCtx(), "mcp", "testPost", seed.Slug, string(to), "moved"); err != nil {
			t.Fatalf("TransitionItemVia: %v", err)
		}
		return "editor-1", "mcp", "moved"
	}

	rows := []struct {
		name     string
		from, to Status
		run      path
		standing string
		edited   bool
	}{
		{"PUT", Draft, Published, put(""), "began", false},
		{"PUT", Published, Archived, put(""), "ended", false},
		{"PUT with an edit", Draft, Published, put("A new title"), "began", true},
		{"MCP publish", Draft, Published, publish, "began", false},
		{"MCP schedule", Draft, Scheduled, schedule, "", false},
		{"MCP archive", Published, Archived, archive, "ended", false},
		{"scheduler", Scheduled, Published, scheduler, "began", false},
		{"transition_item", Draft, Published, transitionItem, "began", false},
		{"transition_item", Published, Archived, transitionItem, "ended", false},
	}
	for _, r := range rows {
		t.Run(r.name+" "+string(r.from)+"->"+string(r.to), func(t *testing.T) {
			e := newD107Env(t)
			seed := e.seed(t, "Item", r.from)
			actor, surface, reason := r.run(t, e, seed, r.to)
			e.check(t, expectation("testPost", seed.ID, r.from, r.to, true, r.edited))
			e.checkRecord(t, seed.ID, r.from, r.to, actor, surface, reason, r.standing)
			wantUpdate := 0
			if r.edited {
				wantUpdate = 1
			}
			if got := e.verbs(seed.ID)["update"]; got != wantUpdate {
				t.Errorf("update records = %d; want %d", got, wantUpdate)
			}
		})
	}
}

// TestStatusChange_PutScheduledAtIsNotAnEdit: a PUT into scheduled changes
// ScheduledAt with the status, which is bookkeeping, not content.
func TestStatusChange_PutScheduledAtIsNotAnEdit(t *testing.T) {
	e := newD107Env(t)
	seed := e.seed(t, "Item", Draft)
	if err := serveWrite(e.m.updateHandler, http.MethodPut, "/testposts/"+seed.Slug, seed.Slug,
		map[string]any{"Title": seed.Title, "Status": string(Scheduled), "ScheduledAt": time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("PUT: %v", err)
	}
	e.check(t, expectation("testPost", seed.ID, Draft, Scheduled, true, false))
}

// TestStatusChange_DynamicPaths covers a runtime-defined type: transition_item,
// SetStatus and ScheduleContent each give one record, one event, the
// milestones on the bus (it had none before) and one AfterTransition.
func TestStatusChange_DynamicPaths(t *testing.T) {
	type run func(t *testing.T, app *App, typeName string, node *DynamicNode)
	rows := []struct {
		name string
		to   Status
		run  run
	}{
		{"transition_item", Published, func(t *testing.T, app *App, typeName string, node *DynamicNode) {
			if _, err := app.TransitionItemVia(hookCtx(), "mcp", typeName, node.Slug, string(Published), "r"); err != nil {
				t.Fatalf("TransitionItemVia: %v", err)
			}
		}},
		{"set_content_status", Published, func(t *testing.T, app *App, typeName string, node *DynamicNode) {
			repo, err := app.DynamicContentRepo(typeName)
			if err != nil {
				t.Fatal(err)
			}
			if err := repo.SetStatusWithReason(hookCtx(), node.ID, Published, "r"); err != nil {
				t.Fatalf("SetStatusWithReason: %v", err)
			}
		}},
		{"schedule_content", Scheduled, func(t *testing.T, app *App, typeName string, node *DynamicNode) {
			repo, err := app.DynamicContentRepo(typeName)
			if err != nil {
				t.Fatal(err)
			}
			if err := repo.ScheduleContent(hookCtx(), node.ID, time.Now().Add(time.Hour)); err != nil {
				t.Fatalf("ScheduleContent: %v", err)
			}
		}},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			e := newD107Env(t)
			typeName, slug := defineProvenanceDynamicType(t, e.app, e.db, "memo")
			repo, err := e.app.DynamicContentRepo(typeName)
			if err != nil {
				t.Fatal(err)
			}
			node, err := repo.GetBySlug(context.Background(), slug)
			if err != nil {
				t.Fatal(err)
			}
			r.run(t, e.app, typeName, node)
			e.check(t, expectation(typeName, node.ID, Draft, r.to, true, false))
			if got := e.verbs(node.ID)["transition"]; got != 1 {
				t.Errorf("transition records = %d; want 1", got)
			}
		})
	}
}

// TestStatusChange_SupersededItemIsAnnounced: an item a conflict supersedes
// gets its own record, its own "<type>.transitioned" event and its milestones
// (here AfterUnpublish, leaving published), with the reason naming the winner.
func TestStatusChange_SupersededItemIsAnnounced(t *testing.T) {
	e := newD107Env(t)
	if err := e.app.RegisterFlow(StateFlow{
		Name: "d107-posts", TypeName: "testPost", ActiveState: "published", ConflictPolicy: ConflictSupersede,
		States: []State{
			{Name: "draft", IsInitial: true}, {Name: "published"}, {Name: "superseded"},
		},
		Transitions: []Transition{{From: "draft", To: "published"}, {From: "published", To: "superseded"}},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	old := e.seed(t, "Old", Published)
	winner := e.seed(t, "New", Draft)
	if _, err := e.app.TransitionItemVia(hookCtx(), "mcp", "testPost", winner.Slug, string(Published), ""); err != nil {
		t.Fatalf("TransitionItemVia: %v", err)
	}
	want := expectation("testPost", old.ID, Published, "superseded", true, false)
	for k, v := range expectation("testPost", winner.ID, Draft, Published, true, false) {
		want[k] = v
	}
	e.check(t, want)
	var rec ProvenanceRecord
	for _, r := range e.prov.Appended() {
		if r.SubjectID == old.ID && r.Verb == "transition" {
			rec = r
		}
	}
	if rec.ToState != "superseded" || !strings.Contains(rec.Reason, winner.ID) {
		t.Errorf("superseded record = %+v; want to superseded with the winner named", rec)
	}
}

// TestStatusChange_DrainHasNoMilestones: a move made by DrainEvalQueue gets
// its record, the canonical event and AfterTransition, but no milestone
// (T211/D51 keeps human-publish subscribers out of background automation).
func TestStatusChange_DrainHasNoMilestones(t *testing.T) {
	e := newD107Env(t)
	seed := e.seed(t, "Item", Draft)
	if _, err := e.db.Exec(
		`INSERT INTO smeldr_eval_queue (id, type_name, item_id, to_state, eval_at) VALUES (?, 'testPost', ?, 'published', datetime('now', '-1 second'))`,
		NewID(), seed.ID); err != nil {
		t.Fatalf("queue: %v", err)
	}
	if _, triggered, _, err := e.app.DrainEvalQueue(context.Background()); err != nil || triggered != 1 {
		t.Fatalf("DrainEvalQueue = %d, %v; want 1 triggered", triggered, err)
	}
	e.check(t, expectation("testPost", seed.ID, Draft, Published, false, false))
	e.checkRecord(t, seed.ID, Draft, Published, "drain-eval-queue", "trigger", "", "began")
}

// TestStatusChange_SuppressedStateHasNoMilestones: a target state that
// suppresses signals gets no milestone, and still the record, the canonical
// event and AfterTransition.
func TestStatusChange_SuppressedStateHasNoMilestones(t *testing.T) {
	e := newD107Env(t)
	if err := e.app.RegisterFlow(StateFlow{
		Name: "d107-posts", TypeName: "testPost",
		States:      []State{{Name: "draft", IsInitial: true}, {Name: "published", SuppressesSignals: true}},
		Transitions: []Transition{{From: "draft", To: "published"}},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	seed := e.seed(t, "Item", Draft)
	if _, err := e.app.TransitionItemVia(hookCtx(), "mcp", "testPost", seed.Slug, string(Published), ""); err != nil {
		t.Fatalf("TransitionItemVia: %v", err)
	}
	e.check(t, expectation("testPost", seed.ID, Draft, Published, false, false))
}

// TestStatusChange_FailedRecordStillAnnounced: a provenance Append failure is
// logged, and the event and the milestones still go out.
func TestStatusChange_FailedRecordStillAnnounced(t *testing.T) {
	e := newD107Env(t)
	e.app.provenanceStore = &failingProvenanceStore{}
	seed := e.seed(t, "Item", Draft)
	if _, err := e.app.TransitionItemVia(hookCtx(), "mcp", "testPost", seed.Slug, string(Published), ""); err != nil {
		t.Fatalf("TransitionItemVia: %v", err)
	}
	e.check(t, expectation("testPost", seed.ID, Draft, Published, true, false))
}

// TestStatusChange_UnloadableItem: when the item cannot be loaded for its bus
// events, the record and the canonical event still go out and the bus gets
// nothing.
func TestStatusChange_UnloadableItem(t *testing.T) {
	e := newD107EnvWrapped(t, func(r Repository[*testPost]) Repository[*testPost] { return findfailRepo{Repository: r} })
	seed := e.seed(t, "Item", Draft)
	if _, err := e.app.TransitionItemVia(hookCtx(), "mcp", "testPost", seed.Slug, string(Published), ""); err != nil {
		t.Fatalf("TransitionItemVia: %v", err)
	}
	want := expectation("testPost", seed.ID, Draft, Published, false, false)
	want["listen "+string(AfterTransition)+":"+seed.ID] = 0
	e.check(t, want)
	if got := e.verbs(seed.ID)["transition"]; got != 1 {
		t.Errorf("transition records = %d; want 1", got)
	}
}

// TestStatusChange_ListenerPanicIsContained: a panicking listener is recovered
// and the next one still runs.
func TestStatusChange_ListenerPanicIsContained(t *testing.T) {
	e := newD107Env(t)
	e.app.signalListeners = append([]func(LifecycleEvent, string, any){func(LifecycleEvent, string, any) { panic("listener") }}, e.app.signalListeners...)
	seed := e.seed(t, "Item", Draft)
	if _, err := e.app.TransitionItemVia(hookCtx(), "mcp", "testPost", seed.Slug, string(Published), ""); err != nil {
		t.Fatalf("TransitionItemVia: %v", err)
	}
	e.check(t, expectation("testPost", seed.ID, Draft, Published, true, false))
}

// lockedAuditStore is an [AuditStore] safe for the bus goroutine.
type lockedAuditStore struct {
	mu   sync.Mutex
	recs []AuditRecord
}

func (s *lockedAuditStore) Append(_ context.Context, r AuditRecord) error {
	s.mu.Lock()
	s.recs = append(s.recs, r)
	s.mu.Unlock()
	return nil
}

func (s *lockedAuditStore) List(context.Context, AuditFilter) ([]AuditRecord, error) { return nil, nil }

func (s *lockedAuditStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.recs)
}

// TestStatusChange_AuditSeesTransitionItem: Audit, an OnSignal subscriber,
// gets one record for a transition_item publish (it got none before D107).
func TestStatusChange_AuditSeesTransitionItem(t *testing.T) {
	e := newD107Env(t)
	audit := &lockedAuditStore{}
	e.app.Audit(audit)
	seed := e.seed(t, "Item", Draft)
	if _, err := e.app.TransitionItemVia(hookCtx(), "mcp", "testPost", seed.Slug, string(Published), ""); err != nil {
		t.Fatalf("TransitionItemVia: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for audit.count() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(80 * time.Millisecond)
	if got := audit.count(); got != 1 {
		t.Errorf("audit records = %d; want 1", got)
	}
}

// TestStatusChange_HandlersRunWithoutTheConflictLock: the bus handlers and a
// module's own handlers run after the transition released its conflict lock,
// so their context must not say it is still held; a nested transition of the
// same type then takes the lock instead of skipping it.
func TestStatusChange_HandlersRunWithoutTheConflictLock(t *testing.T) {
	held := &d107Counter{}
	e := newD107Env(t, On[*testPost](AfterPublish, func(ctx Context, _ *testPost) error {
		if conflictHeld(ctx, "testPost") {
			held.add("module")
		}
		held.add("module-ran")
		return nil
	}))
	if err := e.app.RegisterFlow(StateFlow{
		Name: "d107-posts", TypeName: "testPost", ActiveState: "published", ConflictPolicy: ConflictSupersede,
		States:      []State{{Name: "draft", IsInitial: true}, {Name: "published"}, {Name: "superseded"}},
		Transitions: []Transition{{From: "draft", To: "published"}, {From: "published", To: "superseded"}},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	e.app.OnSignal(AfterPublish, func(ctx context.Context, _ SignalEvent) error {
		if conflictHeld(ctx, "testPost") {
			held.add("bus")
		}
		held.add("bus-ran")
		return nil
	})
	e.app.Handler()
	for _, run := range []func(seed *testPost) error{
		func(seed *testPost) error { return e.m.MCPPublish(hookCtx(), seed.Slug, "") },
		func(seed *testPost) error {
			_, err := e.app.TransitionItemVia(hookCtx(), "mcp", "testPost", seed.Slug, string(Published), "")
			return err
		},
		func(seed *testPost) error {
			return serveWrite(e.m.updateHandler, http.MethodPut, "/testposts/"+seed.Slug, seed.Slug,
				map[string]any{"Title": seed.Title, "Status": string(Published)})
		},
	} {
		seed := e.seed(t, "Item "+NewID(), Draft)
		if err := run(seed); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for (held.get("bus-ran") < 3 || held.get("module-ran") < 3) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if held.get("bus-ran") < 3 || held.get("module-ran") < 3 {
		t.Fatalf("handlers ran bus %d, module %d; want 3 each", held.get("bus-ran"), held.get("module-ran"))
	}
	if held.get("bus") != 0 || held.get("module") != 0 {
		t.Errorf("handlers saw the lock as held: bus %d, module %d; want 0", held.get("bus"), held.get("module"))
	}
}

// TestWithoutConflictHold covers both context kinds and the no-op case.
func TestWithoutConflictHold(t *testing.T) {
	plain := context.WithValue(context.Background(), conflictHeldKey{}, []string{"T"})
	if conflictHeld(withoutConflictHold(plain), "T") {
		t.Error("plain context still held")
	}
	smeldrCtx := &heldContext{Context: hookCtx(), held: []string{"T"}}
	cleared := withoutConflictHoldContext(smeldrCtx)
	if conflictHeld(cleared, "T") || cleared.User().ID != "editor-1" {
		t.Errorf("Context: held %v, user %q", conflictHeld(cleared, "T"), cleared.User().ID)
	}
	free := context.Background()
	if withoutConflictHold(free) != free {
		t.Error("an unheld context was wrapped")
	}
}

// TestContentChanged pins what a PUT counts as a content edit.
func TestContentChanged(t *testing.T) {
	type raw struct {
		Node
		Title string
		Data  json.RawMessage
		Tags  map[string]int
	}
	now := time.Now()
	later := now.Add(time.Hour)
	base := raw{Node: Node{ID: "1", Slug: "s", Status: Draft}, Title: "T", Data: json.RawMessage(`{"a":1}`), Tags: map[string]int{"x": 1, "y": 2}}
	cases := []struct {
		name  string
		after func(r raw) any
		want  bool
	}{
		{"lifecycle fields only", func(r raw) any {
			r.Status, r.PublishedAt, r.ScheduledAt, r.UpdatedAt, r.CreatedAt, r.Rev = Published, now, &later, now, now, 9
			return r
		}, false},
		{"a content field", func(r raw) any { r.Title = "U"; return r }, true},
		{"raw JSON with other whitespace", func(r raw) any { r.Data = json.RawMessage(`{ "a" : 1 }`); return r }, false},
		{"a map built in another order", func(r raw) any { r.Tags = map[string]int{"y": 2, "x": 1}; return r }, false},
		{"the slug", func(r raw) any { r.Slug = "t"; return r }, true},
		{"an unencodable value", func(raw) any { return map[string]any{"f": func() {}} }, true},
		{"not an object", func(raw) any { return 42 }, true},
		{"a field only one side has", func(raw) any { return map[string]any{"Other": 1} }, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := contentChanged(base, c.after(base)); got != c.want {
				t.Errorf("contentChanged = %v; want %v", got, c.want)
			}
		})
	}
	type actor struct {
		Title     string
		LastActor string `json:"last_actor"`
		Plain     string
	}
	if contentChanged(actor{Title: "T", LastActor: "a"}, actor{Title: "T", LastActor: "b"}) {
		t.Error("last_actor counted as content")
	}
	type untagged struct{ LastActor string }
	if contentChanged(untagged{LastActor: "a"}, untagged{LastActor: "b"}) {
		t.Error("LastActor counted as content")
	}
}

// TestLoadForAnnounce covers the item lookups: a runtime-defined type, a
// compiled one, an unknown id and a failing repository.
func TestLoadForAnnounce(t *testing.T) {
	e := newD107Env(t)
	typeName, slug := defineProvenanceDynamicType(t, e.app, e.db, "memo")
	repo, _ := e.app.DynamicContentRepo(typeName)
	node, _ := repo.GetBySlug(context.Background(), slug)
	if item, _ := e.app.loadForAnnounce(context.Background(), typeName, node.ID); item == nil {
		t.Error("dynamic item not loaded")
	}
	if item, _ := e.app.loadForAnnounce(context.Background(), typeName, "missing"); item != nil {
		t.Error("missing dynamic item loaded")
	}
	seed := e.seed(t, "Item", Draft)
	if item, _ := e.app.loadForAnnounce(context.Background(), "testPost", seed.ID); item == nil {
		t.Error("compiled item not loaded")
	}
	if item, _ := e.app.loadForAnnounce(context.Background(), "testPost", "missing"); item != nil {
		t.Error("missing compiled item loaded")
	}
	failing := newD107EnvWrapped(t, func(r Repository[*testPost]) Repository[*testPost] { return findfailRepo{Repository: r} })
	if item, _ := failing.app.loadForAnnounce(context.Background(), "testPost", seed.ID); item != nil {
		t.Error("item loaded through a failing repository")
	}
	if item, _ := (&App{}).loadForAnnounce(context.Background(), "testPost", seed.ID); item != nil {
		t.Error("item loaded with no registry or module")
	}
	if _, err := e.db.Exec(`DROP TABLE smeldr_dynamic_content`); err != nil {
		t.Fatal(err)
	}
	if item, _ := e.app.loadForAnnounce(context.Background(), typeName, node.ID); item != nil {
		t.Error("dynamic item loaded with its table gone")
	}
}

// TestTransitionChannel covers the channel of the canonical event.
func TestTransitionChannel(t *testing.T) {
	if got := transitionChannel("Signal", &Signal{Receiver: "core"}); got != eventStreamChannelSignals {
		t.Errorf("Signal = %q; want its topic", got)
	}
	if got := transitionChannel("Task", &Task{Band: "core"}); got != "core" {
		t.Errorf("Task = %q; want its band", got)
	}
	if got := transitionChannel("Task", nil); got != "" {
		t.Errorf("Task with no item = %q; want a broadcast", got)
	}
}

// TestStatusChange_NoBusWork: with nothing listening, the record and standing
// are still written and no item is loaded.
func TestStatusChange_NoBusWork(t *testing.T) {
	db := newSQLiteDB(t)
	if _, err := db.Exec(`CREATE TABLE test_posts (id TEXT PRIMARY KEY, slug TEXT, status TEXT, created_at DATETIME, updated_at DATETIME, scheduled_at DATETIME, published_at DATETIME, title TEXT, body TEXT, rev INTEGER NOT NULL DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}
	repo := NewSQLRepo[*testPost](db)
	calls := 0
	app := New(MustConfig(Config{BaseURL: "https://example.com", Secret: []byte(transitionItemTestSecret), DB: db}))
	app.Content(newTestModule(findfailRepo{Repository: repo, calls: &calls}))
	app.provenanceStore = &fakeProvenanceStore{}
	seed := seedPost(t, repo, "Item", Draft)
	app.afterStatusChange(hookCtx(), statusTransition{typeName: "testPost", id: seed.ID, from: "draft", to: "published", milestones: true})
	if calls != 0 {
		t.Errorf("item loads = %d; want 0 with no bus work", calls)
	}
	if got := app.provenanceStore.(*fakeProvenanceStore).Appended(); len(got) != 1 {
		t.Errorf("records = %d; want 1", len(got))
	}
	app.afterStatusChange(hookCtx(), statusTransition{typeName: "testPost", id: seed.ID, from: "draft", to: "draft"})
	if got := app.provenanceStore.(*fakeProvenanceStore).Appended(); len(got) != 1 {
		t.Errorf("records after a no-move = %d; want still 1", len(got))
	}
}

// TestStatusChange_ModuleWithoutApp: a module used without an App records its
// own transition and standing, and an MCP publish of a published item stays a
// plain AfterPublish.
func TestStatusChange_ModuleWithoutApp(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	rec := newAfterRecorder()
	m := newTestModule(repo, rec.options()...)
	prov := &fakeProvenanceStore{}
	m.setProvenanceStore(prov)
	seed := seedPost(t, repo, "Item", Draft)
	if err := m.MCPPublish(hookCtx(), seed.Slug, ""); err != nil {
		t.Fatalf("MCPPublish: %v", err)
	}
	if got := verbsOf(prov.Appended()); strings.Join(got, ",") != "transition" {
		t.Errorf("records = %v; want one transition", got)
	}
	if got := rec.wait(2); got[AfterUpdate] != 1 || got[AfterPublish] != 1 {
		t.Errorf("handlers fired %v; want AfterUpdate and AfterPublish once", got)
	}
	if err := m.MCPPublish(hookCtx(), seed.Slug, ""); err != nil {
		t.Fatalf("second MCPPublish: %v", err)
	}
	if got := rec.wait(4); got[AfterPublish] != 2 {
		t.Errorf("AfterPublish fired %d times after a republish; want 2", got[AfterPublish])
	}
	if got := verbsOf(prov.Appended()); len(got) != 1 {
		t.Errorf("records after a republish = %v; want still one (no App, no subscriber)", got)
	}
}

// TestSignalContext keeps a plain context's values and gives it a guest user.
func TestSignalContext(t *testing.T) {
	type key struct{}
	app := New(MustConfig(Config{BaseURL: "https://example.com", Secret: []byte(transitionItemTestSecret)}))
	sc := app.signalContext(context.WithValue(context.Background(), key{}, "v"))
	if sc.Value(key{}) != "v" || sc.User().ID != GuestUser.ID {
		t.Errorf("value %v, user %q", sc.Value(key{}), sc.User().ID)
	}
	c := hookCtx()
	if app.signalContext(c) != c {
		t.Error("a Context was wrapped")
	}
}
