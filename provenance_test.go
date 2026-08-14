package smeldr

import (
	"context"
	"errors"
	"testing"
	"time"
)

// --- fakeProvenanceStore ---

type fakeProvenanceStore struct {
	appended []ProvenanceRecord
	listed   []ProvenanceRecord
}

func (f *fakeProvenanceStore) Append(_ context.Context, r ProvenanceRecord) error {
	f.appended = append(f.appended, r)
	return nil
}

func (f *fakeProvenanceStore) List(_ context.Context, _ ProvenanceFilter) ([]ProvenanceRecord, error) {
	return f.listed, nil
}

// --- transitionIsGated tests (T243) ---

func TestTransitionIsGated_StrictAndRole_True(t *testing.T) {
	db, _ := setupGovStateStrictDB(t)
	if !transitionIsGated(context.Background(), db, "GovPostStrict", "draft", "published") {
		t.Error("RequiredRole+Strict transition: want gated=true")
	}
}

func TestTransitionIsGated_RoleWithoutStrict_False(t *testing.T) {
	db, _ := setupGovStateDB(t)
	// GovPost's draft->published has RequiredRole="editor" but Strict is the
	// zero value (false) - gating requires both, per §4.3's own tier.
	if transitionIsGated(context.Background(), db, "GovPost", "draft", "published") {
		t.Error("RequiredRole without Strict: want gated=false")
	}
}

func TestTransitionIsGated_NilDB_False(t *testing.T) {
	if transitionIsGated(context.Background(), nil, "GovPostStrict", "draft", "published") {
		t.Error("nil db: want gated=false")
	}
}

func TestTransitionIsGated_SameState_False(t *testing.T) {
	db, _ := setupGovStateStrictDB(t)
	// An edit (fromState == toState) is never a transition - provenanceVerbFor's
	// own "update" classification - so never gated, and must not query the DB.
	if transitionIsGated(context.Background(), db, "GovPostStrict", "draft", "draft") {
		t.Error("fromState==toState: want gated=false")
	}
}

func TestTransitionIsGated_NoFlowRegistered_False(t *testing.T) {
	db, _ := setupGovStateDB(t)
	if transitionIsGated(context.Background(), db, "NoSuchType", "draft", "published") {
		t.Error("no flow registered for type: want gated=false")
	}
}

func TestTransitionIsGated_EdgeNotDeclared_False(t *testing.T) {
	db, _ := setupGovStateStrictDB(t)
	// GovPostStrict has no published->draft edge declared.
	if transitionIsGated(context.Background(), db, "GovPostStrict", "published", "draft") {
		t.Error("undeclared edge: want gated=false")
	}
}

// TestTransitionIsGated_UnresolvableGate_FailsClosedNoActor pins the
// fail-closed direction stated in transitionIsGated's own doc comment: a
// genuine DB error resolving the gate must return false (withhold the
// actor), the opposite direction from validateTransition's own fail-closed
// rule (which rejects the transition on the identical class of error).
func TestTransitionIsGated_UnresolvableGate_FailsClosedNoActor(t *testing.T) {
	db, _ := setupGovStateStrictDB(t)
	wrapped := &govQueryRowFailDB{DB: db, failOn: "FROM smeldr_transitions"}
	if transitionIsGated(context.Background(), wrapped, "GovPostStrict", "draft", "published") {
		t.Error("unresolvable gate (DB error): want gated=false, not true")
	}
}

// --- SubjectProvenance tests (T243) ---

func TestSubjectProvenance_GatedTransition_ActorExposed(t *testing.T) {
	db, _ := setupGovStateStrictDB(t)
	store := &fakeProvenanceStore{listed: []ProvenanceRecord{
		{
			SubjectType: "GovPostStrict", SubjectID: "p1", Verb: "transition",
			FromState: "draft", ToState: "published",
			ActorKind: "human", ActorID: "actor-1", Surface: "mcp", Reason: "quarterly review",
		},
	}}
	entries, err := SubjectProvenance(context.Background(), db, store, "GovPostStrict", "p1")
	if err != nil {
		t.Fatalf("SubjectProvenance: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	e := entries[0]
	if !e.Gated {
		t.Fatal("gated transition: want Gated=true")
	}
	if e.ActorKind != "human" || e.ActorID != "actor-1" || e.Surface != "mcp" || e.Reason != "quarterly review" {
		t.Errorf("gated entry: actor fields not populated, got %+v", e)
	}
}

func TestSubjectProvenance_UngatedTransition_ActorWithheld(t *testing.T) {
	db, _ := setupGovStateDB(t)
	store := &fakeProvenanceStore{listed: []ProvenanceRecord{
		{
			SubjectType: "GovPost", SubjectID: "p1", Verb: "transition",
			FromState: "draft", ToState: "published",
			ActorKind: "human", ActorID: "actor-1", Surface: "mcp", Reason: "should not leak",
		},
	}}
	entries, err := SubjectProvenance(context.Background(), db, store, "GovPost", "p1")
	if err != nil {
		t.Fatalf("SubjectProvenance: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e.Gated {
		t.Fatal("ungated transition (RequiredRole without Strict): want Gated=false")
	}
	if e.ActorKind != "" || e.ActorID != "" || e.Surface != "" || e.Reason != "" {
		t.Errorf("ungated entry: actor fields must stay empty, got %+v", e)
	}
	if e.Verb != "transition" || e.FromState != "draft" || e.ToState != "published" {
		t.Errorf("ungated entry: non-identifying facts must still be present, got %+v", e)
	}
}

func TestSubjectProvenance_CreateAndUpdate_NeverGated(t *testing.T) {
	db, _ := setupGovStateStrictDB(t)
	store := &fakeProvenanceStore{listed: []ProvenanceRecord{
		{SubjectType: "GovPostStrict", SubjectID: "p1", Verb: "create", FromState: "", ToState: "draft", ActorKind: "human", ActorID: "a1"},
		{SubjectType: "GovPostStrict", SubjectID: "p1", Verb: "update", FromState: "draft", ToState: "draft", ActorKind: "human", ActorID: "a1"},
	}}
	entries, err := SubjectProvenance(context.Background(), db, store, "GovPostStrict", "p1")
	if err != nil {
		t.Fatalf("SubjectProvenance: %v", err)
	}
	for _, e := range entries {
		if e.Gated {
			t.Errorf("verb %q: want never gated, got Gated=true", e.Verb)
		}
	}
}

// TestSubjectProvenance_NoActorInFilter pins §4.1 structurally: the
// ProvenanceFilter SubjectProvenance builds must never set ActorID, checked
// by inspecting the filter the store actually received rather than trusting
// caller discipline.
func TestSubjectProvenance_NoActorInFilter(t *testing.T) {
	spy := &filterSpyStore{}
	db, _ := setupGovStateDB(t)
	if _, err := SubjectProvenance(context.Background(), db, spy, "GovPost", "p1"); err != nil {
		t.Fatalf("SubjectProvenance: %v", err)
	}
	if spy.gotFilter.ActorID != "" {
		t.Errorf("ProvenanceFilter.ActorID: want empty (never a query key), got %q", spy.gotFilter.ActorID)
	}
	if spy.gotFilter.SubjectType != "GovPost" || spy.gotFilter.SubjectID != "p1" {
		t.Errorf("filter: want item-scoped GovPost/p1, got %+v", spy.gotFilter)
	}
}

func TestSubjectProvenance_StoreListError_Propagates(t *testing.T) {
	db, _ := setupGovStateDB(t)
	store := &errProvenanceStore{}
	if _, err := SubjectProvenance(context.Background(), db, store, "GovPost", "p1"); err == nil {
		t.Error("store.List error: want non-nil error propagated")
	}
}

type filterSpyStore struct {
	gotFilter ProvenanceFilter
}

func (s *filterSpyStore) Append(_ context.Context, _ ProvenanceRecord) error { return nil }
func (s *filterSpyStore) List(_ context.Context, f ProvenanceFilter) ([]ProvenanceRecord, error) {
	s.gotFilter = f
	return nil, nil
}

type errProvenanceStore struct{}

func (s *errProvenanceStore) Append(_ context.Context, _ ProvenanceRecord) error { return nil }
func (s *errProvenanceStore) List(_ context.Context, _ ProvenanceFilter) ([]ProvenanceRecord, error) {
	return nil, errors.New("simulated list failure")
}

// --- sqlProvenanceStore tests ---

func createProvenanceTableHelper(t *testing.T, db DB) {
	t.Helper()
	if err := CreateProvenanceTable(db); err != nil {
		t.Fatalf("CreateProvenanceTable: %v", err)
	}
}

func TestProvenanceStore_AppendAndList(t *testing.T) {
	db := newSQLiteDB(t)
	createProvenanceTableHelper(t, db)
	store := NewProvenanceStore(db)
	ctx := context.Background()

	r := ProvenanceRecord{
		ID:          "test-id-1",
		Timestamp:   time.Now().UTC().Truncate(time.Second),
		SubjectType: "Decision",
		SubjectID:   "dec-1",
		Verb:        "transition",
		FromState:   "proposed",
		ToState:     "ratified",
		ActorKind:   "human",
		ActorID:     "actor-1",
		Surface:     "mcp",
		Reason:      "quarterly review",
	}
	if err := store.Append(ctx, r); err != nil {
		t.Fatalf("Append: %v", err)
	}

	records, err := store.List(ctx, ProvenanceFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("List: got %d records, want 1", len(records))
	}
	got := records[0]
	if got.ID != r.ID {
		t.Errorf("ID: got %q, want %q", got.ID, r.ID)
	}
	if got.SubjectType != "Decision" || got.SubjectID != "dec-1" {
		t.Errorf("subject: got %s/%s, want Decision/dec-1", got.SubjectType, got.SubjectID)
	}
	if got.Reason != "quarterly review" {
		t.Errorf("Reason: got %q, want %q", got.Reason, "quarterly review")
	}
	if !got.Timestamp.Equal(r.Timestamp) {
		t.Errorf("Timestamp: got %v, want %v", got.Timestamp, r.Timestamp)
	}
}

func TestProvenanceStore_ListFilterBySubjectType(t *testing.T) {
	db := newSQLiteDB(t)
	createProvenanceTableHelper(t, db)
	store := NewProvenanceStore(db)
	ctx := context.Background()

	for _, st := range []string{"Decision", "Task"} {
		if err := store.Append(ctx, ProvenanceRecord{ID: NewID(), SubjectType: st, SubjectID: "x", Verb: "create"}); err != nil {
			t.Fatalf("Append %s: %v", st, err)
		}
	}
	records, err := store.List(ctx, ProvenanceFilter{SubjectType: "Decision"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 1 || records[0].SubjectType != "Decision" {
		t.Errorf("List filtered by SubjectType: got %+v, want 1 Decision record", records)
	}
}

func TestProvenanceStore_ListFilterBySubjectID(t *testing.T) {
	db := newSQLiteDB(t)
	createProvenanceTableHelper(t, db)
	store := NewProvenanceStore(db)
	ctx := context.Background()

	for _, id := range []string{"a", "b"} {
		if err := store.Append(ctx, ProvenanceRecord{ID: NewID(), SubjectType: "Decision", SubjectID: id, Verb: "create"}); err != nil {
			t.Fatalf("Append %s: %v", id, err)
		}
	}
	records, err := store.List(ctx, ProvenanceFilter{SubjectID: "a"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 1 || records[0].SubjectID != "a" {
		t.Errorf("List filtered by SubjectID: got %+v, want 1 record with SubjectID=a", records)
	}
}

func TestProvenanceStore_ListFilterByActorID(t *testing.T) {
	db := newSQLiteDB(t)
	createProvenanceTableHelper(t, db)
	store := NewProvenanceStore(db)
	ctx := context.Background()

	for _, actor := range []string{"u1", "u2"} {
		if err := store.Append(ctx, ProvenanceRecord{ID: NewID(), SubjectType: "Decision", SubjectID: "x", Verb: "create", ActorID: actor}); err != nil {
			t.Fatalf("Append actor %s: %v", actor, err)
		}
	}
	records, err := store.List(ctx, ProvenanceFilter{ActorID: "u1"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 1 || records[0].ActorID != "u1" {
		t.Errorf("List filtered by ActorID: got %+v, want 1 record for u1", records)
	}
}

func TestProvenanceStore_ListFilterByTimeRange(t *testing.T) {
	db := newSQLiteDB(t)
	createProvenanceTableHelper(t, db)
	store := NewProvenanceStore(db)
	ctx := context.Background()

	old := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	recent := time.Now().UTC().Truncate(time.Second)
	if err := store.Append(ctx, ProvenanceRecord{ID: NewID(), Timestamp: old, SubjectType: "Decision", SubjectID: "x", Verb: "create"}); err != nil {
		t.Fatalf("Append old: %v", err)
	}
	if err := store.Append(ctx, ProvenanceRecord{ID: NewID(), Timestamp: recent, SubjectType: "Decision", SubjectID: "x", Verb: "create"}); err != nil {
		t.Fatalf("Append recent: %v", err)
	}

	records, err := store.List(ctx, ProvenanceFilter{From: recent.Add(-1 * time.Hour)})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("List with From filter: got %d records, want 1", len(records))
	}

	records, err = store.List(ctx, ProvenanceFilter{To: old.Add(1 * time.Hour)})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("List with To filter: got %d records, want 1", len(records))
	}
}

func TestProvenanceStore_EmptyResult(t *testing.T) {
	db := newSQLiteDB(t)
	createProvenanceTableHelper(t, db)
	store := NewProvenanceStore(db)
	records, err := store.List(context.Background(), ProvenanceFilter{SubjectType: "NothingMatches"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("List: got %d records, want 0", len(records))
	}
}

func TestCreateProvenanceTable_ExecError(t *testing.T) {
	if err := CreateProvenanceTable(&errExecDB{}); err == nil {
		t.Error("want error from CreateProvenanceTable, got nil")
	}
}

func TestProvenanceStore_List_QueryError(t *testing.T) {
	store := NewProvenanceStore(&errQueryDB{})
	if _, err := store.List(context.Background(), ProvenanceFilter{}); err == nil {
		t.Error("want error from List, got nil")
	}
}

func TestProvenanceStore_Append_ExecError(t *testing.T) {
	store := NewProvenanceStore(&errExecDB{})
	if err := store.Append(context.Background(), ProvenanceRecord{ID: "x"}); err == nil {
		t.Error("want error from Append, got nil")
	}
}

// --- recordProvenance (fail-open) ---

func TestRecordProvenance_NilStore_NoOp(t *testing.T) {
	// Must not panic when store is nil — this is the normal state before
	// App.Provenance is ever called.
	recordProvenance(context.Background(), nil, ProvenanceRecord{SubjectType: "Decision"})
}

func TestRecordProvenance_ExecError_FailsOpen(t *testing.T) {
	store := NewProvenanceStore(&errExecDB{})
	// Must not panic and must not return an error to the caller — recordProvenance
	// has no return value by design (§7: a provenance bug must never fail the
	// write it's recording). This test just proves it doesn't panic.
	recordProvenance(context.Background(), store, ProvenanceRecord{SubjectType: "Decision", SubjectID: "x"})
}

func TestRecordProvenance_GeneratesIDAndTimestamp(t *testing.T) {
	store := &fakeProvenanceStore{}
	recordProvenance(context.Background(), store, ProvenanceRecord{SubjectType: "Decision", SubjectID: "x"})
	if len(store.appended) != 1 {
		t.Fatalf("got %d appended, want 1", len(store.appended))
	}
	if store.appended[0].ID == "" {
		t.Error("ID was not generated")
	}
	if store.appended[0].Timestamp.IsZero() {
		t.Error("Timestamp was not generated")
	}
}

// --- provenanceVerbFor ---

func TestProvenanceVerbFor(t *testing.T) {
	tests := []struct {
		name string
		sig  LifecycleEvent
		from string
		to   string
		want string
	}{
		{"create", AfterCreate, "", "draft", "create"},
		{"delete", AfterDelete, "published", "published", "invalidate"},
		{"transition", AfterUpdate, "proposed", "ratified", "transition"},
		{"update-no-status-change", AfterUpdate, "draft", "draft", "update"},
		{"publish-transition", AfterPublish, "draft", "published", "transition"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := provenanceVerbFor(tt.sig, tt.from, tt.to); got != tt.want {
				t.Errorf("provenanceVerbFor(%s, %q, %q) = %q, want %q", tt.sig, tt.from, tt.to, got, tt.want)
			}
		})
	}
}

// --- currentStatusOf ---

func TestCurrentStatusOf_Nil(t *testing.T) {
	if got := currentStatusOf(nil); got != "" {
		t.Errorf("currentStatusOf(nil) = %q, want empty", got)
	}
}

func TestCurrentStatusOf_NotANode(t *testing.T) {
	// Must not panic on a value with no Node-shaped fields — fail-open, per
	// recordProvenance's own discipline.
	if got := currentStatusOf("not a node"); got != "" {
		t.Errorf("currentStatusOf(non-node) = %q, want empty", got)
	}
}

func TestCurrentStatusOf_RealNode(t *testing.T) {
	item := &testPost{Node: Node{Status: Published}}
	if got := currentStatusOf(item); got != string(Published) {
		t.Errorf("currentStatusOf(item) = %q, want %q", got, Published)
	}
}

// --- actorKindFor ---

func TestActorKindFor(t *testing.T) {
	if got := actorKindFor("", nil); got != "" {
		t.Errorf("actorKindFor(\"\", nil) = %q, want empty", got)
	}
	if got := actorKindFor("u1", []Role{Editor}); got != "human" {
		t.Errorf("actorKindFor(u1, [Editor]) = %q, want human", got)
	}
	if got := actorKindFor("job-1", []Role{Editor, Job}); got != "job" {
		t.Errorf("actorKindFor(job-1, [Editor,Job]) = %q, want job", got)
	}
	if got := actorKindFor("agent-1", []Role{Agent}); got != "agent" {
		t.Errorf("actorKindFor(agent-1, [Agent]) = %q, want agent", got)
	}
}

// TestAppProvenance_JobDrivenTransition_ActorKindJob exercises the real
// app.dispatchBus path (not a direct call to Provenance()'s closure).
//
// An earlier design recovered roles inside the OnSignal closure via a
// ctx.(Context) type assertion — empirically confirmed broken: dispatchBus
// wraps ctx via context.WithoutCancel + context.WithTimeout before invoking
// handlers, and those stdlib wrapper types do not preserve smeldr.Context's
// richer method set, so the type assertion silently saw ok=false on every
// real dispatch (unlike relations.go's recordAssertProvenance, which is
// called synchronously and never passes through dispatchBus's rewrap — the
// two call sites looked identical but weren't). SignalEvent.ActorRoles,
// captured synchronously in buildSignalEvent before dispatch, is the fix;
// this test asserts against that field directly, the same way
// buildSignalEvent's real callers exercise it.
func TestAppProvenance_JobDrivenTransition_ActorKindJob(t *testing.T) {
	store := &fakeProvenanceStore{}
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("provenance-job-test-secret-12345"),
	}))
	app.Provenance(store)

	ctx := NewTestContext(User{ID: "job-42", Roles: []Role{Editor, Job}})
	ev := SignalEvent{
		Type: "Post", Slug: "s", NodeID: "n1",
		ActorID: "job-42", ActorRoles: []Role{Editor, Job},
		PreviousState: "draft",
	}
	app.dispatchBus(ctx, ev, AfterPublish)

	if len(store.appended) != 1 {
		t.Fatalf("got %d appended, want 1", len(store.appended))
	}
	if store.appended[0].ActorKind != "job" {
		t.Errorf("ActorKind = %q, want %q (ev.ActorRoles: [Editor, Job])",
			store.appended[0].ActorKind, "job")
	}
}

// TestAppProvenance_SurfaceAndReason_Populated (T243) pins that
// Provenance()'s subscription actually copies ev.Surface/ev.Reason onto the
// ProvenanceRecord it appends — the two fields the architect named as
// currently-unset (added scope, 2026-08-14). Same dispatchBus-level pattern
// as the ActorKind tests above: a hand-built SignalEvent through the real
// subscription closure, not a direct struct-literal assertion.
func TestAppProvenance_SurfaceAndReason_Populated(t *testing.T) {
	store := &fakeProvenanceStore{}
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("provenance-surface-test-secret-123"),
	}))
	app.Provenance(store)

	ctx := NewTestContext(User{ID: "actor-1", Roles: []Role{Editor}})
	ev := SignalEvent{
		Type: "Post", Slug: "s", NodeID: "n1",
		ActorID: "actor-1", ActorRoles: []Role{Editor},
		PreviousState: "draft",
		Surface:       "mcp",
		Reason:        "quarterly review",
	}
	app.dispatchBus(ctx, ev, AfterPublish)

	if len(store.appended) != 1 {
		t.Fatalf("got %d appended, want 1", len(store.appended))
	}
	if store.appended[0].Surface != "mcp" {
		t.Errorf("Surface = %q, want %q", store.appended[0].Surface, "mcp")
	}
	if store.appended[0].Reason != "quarterly review" {
		t.Errorf("Reason = %q, want %q", store.appended[0].Reason, "quarterly review")
	}
}

// TestNotifyAfter_SurfacePropagatesToSignalEvent (T243) proves the surface
// argument reaches SignalEvent.Surface through the real dispatch path —
// notifyAfter -> afterHookMeta -> buildSignalEvent -> dispatchBus — not a
// hand-built SignalEvent literal, mirroring signals_test.go's own
// established "through the real path" precedent for other SignalEvent
// fields.
func TestNotifyAfter_SurfacePropagatesToSignalEvent(t *testing.T) {
	mem := NewMemoryRepo[*testPost]()
	m := newTestModule(mem)
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("notifyafter-surface-test-secret-12"),
	}))
	store := &fakeProvenanceStore{}
	app.Provenance(store)
	app.hookableModules = append(app.hookableModules, m)
	app.wireSignalBus()

	ctx := NewTestContext(User{ID: "actor-1", Roles: []Role{Editor}})
	p := &testPost{Node: Node{ID: "n1", Slug: "s", Status: Published}}
	m.notifyAfter(ctx, AfterPublish, "draft", surfaceMCP, "", p)

	// afterHook runs in its own goroutine (module.go's notifyAfter) - wait for it.
	deadline := time.Now().Add(2 * time.Second)
	for len(store.appended) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(store.appended) != 1 {
		t.Fatalf("got %d appended, want 1", len(store.appended))
	}
	if store.appended[0].Surface != "mcp" {
		t.Errorf("Surface = %q, want %q (through notifyAfter -> afterHookMeta -> buildSignalEvent)",
			store.appended[0].Surface, "mcp")
	}
}

// TestAppProvenance_AgentDrivenTransition_ActorKindAgent mirrors the job
// case above for the Agent classification role.
func TestAppProvenance_AgentDrivenTransition_ActorKindAgent(t *testing.T) {
	store := &fakeProvenanceStore{}
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("provenance-agent-test-secret-1234"),
	}))
	app.Provenance(store)

	ctx := NewTestContext(User{ID: "agent-7", Roles: []Role{Agent}})
	ev := SignalEvent{
		Type: "Post", Slug: "s", NodeID: "n1",
		ActorID: "agent-7", ActorRoles: []Role{Agent},
		PreviousState: "draft",
	}
	app.dispatchBus(ctx, ev, AfterPublish)

	if len(store.appended) != 1 {
		t.Fatalf("got %d appended, want 1", len(store.appended))
	}
	if store.appended[0].ActorKind != "agent" {
		t.Errorf("ActorKind = %q, want %q (ev.ActorRoles: [Agent])",
			store.appended[0].ActorKind, "agent")
	}
}

// --- App.Provenance integration ---

func TestAppProvenance_SubscribedSignalsFire(t *testing.T) {
	store := &fakeProvenanceStore{}
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("provenance-test-secret-1234567890"),
	}))
	app.Provenance(store)

	ctx := NewTestContext(User{ID: "u1", Roles: []Role{Editor}})
	item := &testPost{Node: Node{ID: "n1", Slug: "hello", Status: Published}}

	for _, sig := range provenanceLifecycleEvents {
		ev := SignalEvent{
			Type:          "Post",
			Slug:          "hello",
			NodeID:        "n1",
			Timestamp:     time.Now(),
			ActorID:       "u1",
			ActorRole:     "editor",
			PreviousState: "draft",
			raw:           item,
		}
		app.dispatchBus(ctx, ev, sig)
	}

	if len(store.appended) != len(provenanceLifecycleEvents) {
		t.Fatalf("appended %d records, want %d (one per subscribed LifecycleEvent)",
			len(store.appended), len(provenanceLifecycleEvents))
	}
	for _, r := range store.appended {
		if r.ID == "" {
			t.Error("ProvenanceRecord.ID is empty")
		}
		if r.SubjectType != "Post" || r.SubjectID != "n1" {
			t.Errorf("subject: got %s/%s, want Post/n1", r.SubjectType, r.SubjectID)
		}
		if r.ActorID != "u1" || r.ActorKind != "human" {
			t.Errorf("actor: got %s/%s, want u1/human", r.ActorID, r.ActorKind)
		}
	}
}

func TestAppProvenance_UnsubscribedSignalNotRecorded(t *testing.T) {
	store := &fakeProvenanceStore{}
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("provenance-test-secret-1234567890"),
	}))
	app.Provenance(store)

	ctx := NewTestContext(User{})
	ev := SignalEvent{Type: "Post", Slug: "s", Timestamp: time.Now()}
	app.dispatchBus(ctx, ev, SitemapRegenerate)

	if len(store.appended) != 0 {
		t.Errorf("expected 0 appended for unsubscribed signal, got %d", len(store.appended))
	}
}

func TestAppProvenance_NilRawEvent_NoPanic(t *testing.T) {
	// SignalEvent built directly (not via buildSignalEvent) has a nil raw field —
	// must not panic. Regression guard for currentStatusOf's fail-open behaviour.
	store := &fakeProvenanceStore{}
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("provenance-test-secret-1234567890"),
	}))
	app.Provenance(store)

	ctx := NewTestContext(User{ID: "u1"})
	ev := SignalEvent{Type: "Post", Slug: "s", NodeID: "n1", ActorID: "u1"}
	app.dispatchBus(ctx, ev, AfterCreate)

	if len(store.appended) != 1 {
		t.Fatalf("got %d appended, want 1", len(store.appended))
	}
	if store.appended[0].ToState != "" {
		t.Errorf("ToState = %q, want empty (nil raw)", store.appended[0].ToState)
	}
}

func TestAppProvenance_AuditAndProvenanceBothFire_NoConflict(t *testing.T) {
	// Confirms the deliberate redundancy on the 4 events App.Audit already
	// covers: both stores get a record from the same dispatch, independently.
	auditStore := &fakeAuditStore{}
	provStore := &fakeProvenanceStore{}
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("both-test-secret-12345678901234"),
	}))
	app.Audit(auditStore)
	app.Provenance(provStore)

	ctx := NewTestContext(User{ID: "u1", Roles: []Role{Editor}})
	ev := SignalEvent{Type: "Post", Slug: "hello", NodeID: "n1", ActorID: "u1", ActorRole: "editor", PreviousState: "draft"}
	app.dispatchBus(ctx, ev, AfterPublish)

	if len(auditStore.appended) != 1 {
		t.Errorf("AuditStore: got %d appended, want 1", len(auditStore.appended))
	}
	if len(provStore.appended) != 1 {
		t.Errorf("ProvenanceStore: got %d appended, want 1", len(provStore.appended))
	}
}
