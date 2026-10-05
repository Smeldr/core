// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
)

// appendFailStore is a ProvenanceStore whose Append always fails, for proving
// provenance recording is fail-open on the transition path.
type appendFailStore struct{ calls int }

func (s *appendFailStore) Append(_ context.Context, _ ProvenanceRecord) error {
	s.calls++
	return errors.New("append failed")
}

func (s *appendFailStore) List(_ context.Context, _ ProvenanceFilter) ([]ProvenanceRecord, error) {
	return nil, nil
}

// setupProvenanceTransitionApp is setupTransitionItemApp with a fake
// provenance store wired through App.Provenance.
func setupProvenanceTransitionApp(t *testing.T) (*App, *sql.DB, *RoleStore, *fakeProvenanceStore) {
	t.Helper()
	app, db, rs := setupTransitionItemApp(t)
	store := &fakeProvenanceStore{}
	app.Provenance(store)
	return app, db, rs, store
}

// defineProvenanceDynamicType registers a runtime-defined content type with
// the default flow and returns its name plus a draft item's slug.
func defineProvenanceDynamicType(t *testing.T, app *App, db *sql.DB, typeName string) (string, string) {
	t.Helper()
	if err := CreateBlockTables(db); err != nil {
		t.Fatalf("CreateBlockTables: %v", err)
	}
	if err := CreateSchemaTable(db); err != nil {
		t.Fatalf("CreateSchemaTable: %v", err)
	}
	fields, err := json.Marshal([]SchemaField{{Name: "Title", Type: "string", Required: true, Role: "title"}})
	if err != nil {
		t.Fatalf("marshal fields: %v", err)
	}
	desc, err := app.DefineContentType(context.Background(), &ContentTypeSchema{TypeName: typeName, Fields: fields})
	if err != nil {
		t.Fatalf("DefineContentType: %v", err)
	}
	repo, err := app.DynamicContentRepo(desc.Name)
	if err != nil {
		t.Fatalf("DynamicContentRepo: %v", err)
	}
	node, err := repo.CreateDraft(context.Background(), map[string]any{"Title": "Provenance " + typeName})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	return desc.Name, node.Slug
}

// TestTransitionItemVia_Compiled_RecordsProvenance covers the compiled-type
// branch: one record per successful transition carrying type, id, states,
// actor, actor kind, surface and reason, for each kind of caller.
func TestTransitionItemVia_Compiled_RecordsProvenance(t *testing.T) {
	tests := []struct {
		name      string
		ctx       context.Context
		surface   string
		wantActor string
		wantKind  string
	}{
		{"human via mcp", NewTestContext(User{ID: "u-human", Roles: []Role{Editor}}), "mcp", "u-human", "human"},
		{"job via trigger", NewTestContext(User{ID: "u-job", Roles: []Role{Editor, Job}}), "trigger", "u-job", "job"},
		{"agent via http", NewTestContext(User{ID: "u-agent", Roles: []Role{Editor, Agent}}), "http", "u-agent", "agent"},
		{"plain context, unattributable", context.Background(), "", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app, db, _, store := setupProvenanceTransitionApp(t)
			insertSignal(t, db, "sig-p-1", "sig-p-1-slug", "pending")

			if _, err := app.TransitionItemVia(tc.ctx, tc.surface, "Signal", "sig-p-1-slug", "read", "seen it"); err != nil {
				t.Fatalf("TransitionItemVia: %v", err)
			}
			got := store.Appended()
			if len(got) != 1 {
				t.Fatalf("got %d records, want 1: %+v", len(got), got)
			}
			r := got[0]
			if r.SubjectType != "Signal" || r.SubjectID != "sig-p-1" {
				t.Errorf("subject = %s/%s, want Signal/sig-p-1", r.SubjectType, r.SubjectID)
			}
			if r.Verb != "transition" || r.FromState != "pending" || r.ToState != "read" {
				t.Errorf("verb/from/to = %s/%s/%s, want transition/pending/read", r.Verb, r.FromState, r.ToState)
			}
			if r.ActorID != tc.wantActor || r.ActorKind != tc.wantKind {
				t.Errorf("actor = %q (%q), want %q (%q)", r.ActorID, r.ActorKind, tc.wantActor, tc.wantKind)
			}
			if r.Surface != tc.surface || r.Reason != "seen it" {
				t.Errorf("surface/reason = %q/%q, want %q/%q", r.Surface, r.Reason, tc.surface, "seen it")
			}
		})
	}
}

// TestTransitionItem_LegacyMethods_RecordEmptySurface pins that the two older
// methods keep their signatures and record no surface: they cannot tell which
// entry point called them.
func TestTransitionItem_LegacyMethods_RecordEmptySurface(t *testing.T) {
	app, db, _, store := setupProvenanceTransitionApp(t)
	insertSignal(t, db, "sig-p-2", "sig-p-2-slug", "pending")
	insertSignal(t, db, "sig-p-3", "sig-p-3-slug", "pending")

	if _, err := app.TransitionItem(context.Background(), "Signal", "sig-p-2-slug", "read"); err != nil {
		t.Fatalf("TransitionItem: %v", err)
	}
	if _, err := app.TransitionItemWithReason(context.Background(), "Signal", "sig-p-3-slug", "read", "why"); err != nil {
		t.Fatalf("TransitionItemWithReason: %v", err)
	}
	got := store.Appended()
	if len(got) != 2 {
		t.Fatalf("got %d records, want 2", len(got))
	}
	for _, r := range got {
		if r.Surface != "" {
			t.Errorf("record %s: Surface = %q, want empty", r.SubjectID, r.Surface)
		}
	}
	if got[0].Reason != "" || got[1].Reason != "why" {
		t.Errorf("reasons = %q, %q; want \"\", \"why\"", got[0].Reason, got[1].Reason)
	}
}

// TestTransitionItemVia_Compiled_TwoColumnFallbackRecords proves recording
// does not depend on which UPDATE form ran: a table that predates last_actor
// (the fallback branch) still produces a record.
func TestTransitionItemVia_Compiled_TwoColumnFallbackRecords(t *testing.T) {
	app, db, _, store := setupProvenanceTransitionApp(t)
	registerReasonGatedFlow(t, app, db)

	if _, err := app.TransitionItemVia(context.Background(), "mcp", "ReasonGatedType", "rg-1-slug", "published", "plan said so"); err != nil {
		t.Fatalf("TransitionItemVia: %v", err)
	}
	got := store.Appended()
	if len(got) != 1 || got[0].SubjectType != "ReasonGatedType" || got[0].SubjectID != "rg-1" ||
		got[0].FromState != "draft" || got[0].ToState != "published" || got[0].Reason != "plan said so" {
		t.Fatalf("unexpected records: %+v", got)
	}
}

// TestTransitionItemVia_FailedTransitionsRecordNothing is the error-path
// table: provenance is written only after the status UPDATE succeeded, so a
// rejected or failed transition leaves the store empty.
func TestTransitionItemVia_FailedTransitionsRecordNothing(t *testing.T) {
	app, db, _, store := setupProvenanceTransitionApp(t)
	insertSignal(t, db, "sig-p-4", "sig-p-4-slug", "pending")
	insertDecision(t, db, "dec-p-1", "dec-p-1-slug", "proposed")
	registerReasonGatedFlow(t, app, db)

	tests := []struct {
		name     string
		ctx      context.Context
		typeName string
		slug     string
		to       string
		reason   string
		wantErr  error
	}{
		{"type not registered", context.Background(), "NoSuchType", "x", "read", "", ErrBadRequest},
		{"slug not found", context.Background(), "Signal", "no-such-slug", "read", "", ErrNotFound},
		{"illegal transition", context.Background(), "Signal", "sig-p-4-slug", "acknowledged", "", ErrConflict},
		{"required reason missing", context.Background(), "ReasonGatedType", "rg-1-slug", "published", "", ErrBadRequest},
		{"role gate rejects", NewTestContext(User{ID: "author-1"}), "Decision", "dec-p-1-slug", "ratified", "", ErrForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := app.TransitionItemVia(tc.ctx, "mcp", tc.typeName, tc.slug, tc.to, tc.reason)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if got := store.Appended(); len(got) != 0 {
				t.Errorf("records after failed transition: %+v, want none", got)
			}
		})
	}
}

// TestTransitionItemVia_AppendFailure_StillSucceeds proves recording is
// fail-open: a store that cannot Append never fails a committed transition.
func TestTransitionItemVia_AppendFailure_StillSucceeds(t *testing.T) {
	app, db, _ := setupTransitionItemApp(t)
	store := &appendFailStore{}
	app.Provenance(store)
	insertSignal(t, db, "sig-p-5", "sig-p-5-slug", "pending")

	result, err := app.TransitionItemVia(context.Background(), "mcp", "Signal", "sig-p-5-slug", "read", "")
	if err != nil {
		t.Fatalf("TransitionItemVia with failing store: %v", err)
	}
	if result["status"] != "read" {
		t.Errorf("status = %v, want read", result["status"])
	}
	if store.calls != 1 {
		t.Errorf("Append calls = %d, want 1", store.calls)
	}
}

// TestTransitionItemVia_Dynamic_RecordsOnce proves the dynamic branch is
// recorded exactly once, by DynamicTypeRepo.setStatusVia, not a second time by
// TransitionItemVia itself.
func TestTransitionItemVia_Dynamic_RecordsOnce(t *testing.T) {
	app, db, _, store := setupProvenanceTransitionApp(t)
	typeName, slug := defineProvenanceDynamicType(t, app, db, "provdyn")

	ctx := NewTestContext(User{ID: "u-dyn", Roles: []Role{Editor}})
	if _, err := app.TransitionItemVia(ctx, "mcp", typeName, slug, "published", "go live"); err != nil {
		t.Fatalf("TransitionItemVia: %v", err)
	}
	got := store.Appended()
	if len(got) != 1 {
		t.Fatalf("got %d records, want exactly 1: %+v", len(got), got)
	}
	r := got[0]
	if r.SubjectType != typeName || r.Verb != "transition" || r.FromState != "draft" || r.ToState != "published" ||
		r.ActorID != "u-dyn" || r.ActorKind != "human" || r.Surface != "mcp" || r.Reason != "go live" {
		t.Errorf("unexpected record: %+v", r)
	}
}

// TestDynamicTypeRepo_FailedSetStatus_RecordsNothing covers the dynamic
// error path: a transition the flow rejects leaves no record.
func TestDynamicTypeRepo_FailedSetStatus_RecordsNothing(t *testing.T) {
	app, db, _, store := setupProvenanceTransitionApp(t)
	typeName, slug := defineProvenanceDynamicType(t, app, db, "provdynfail")

	if _, err := app.TransitionItemVia(context.Background(), "mcp", typeName, slug, "no-such-state", ""); err == nil {
		t.Fatal("transition to an unknown state: want error, got nil")
	}
	if got := store.Appended(); len(got) != 0 {
		t.Errorf("records after rejected transition: %+v, want none", got)
	}
}

// TestDynamicTypeRepo_ScheduleContent_RecordsProvenance covers the third
// dynamic door, which has no surface of its own.
func TestDynamicTypeRepo_ScheduleContent_RecordsProvenance(t *testing.T) {
	app, db, _, store := setupProvenanceTransitionApp(t)
	typeName, slug := defineProvenanceDynamicType(t, app, db, "provsched")
	repo, err := app.DynamicContentRepo(typeName)
	if err != nil {
		t.Fatalf("DynamicContentRepo: %v", err)
	}
	node, err := repo.GetBySlug(context.Background(), slug)
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}

	ctx := NewTestContext(User{ID: "u-sched", Roles: []Role{Editor}})
	if err := repo.ScheduleContent(ctx, node.ID, node.CreatedAt.AddDate(0, 0, 1)); err != nil {
		t.Fatalf("ScheduleContent: %v", err)
	}
	got := store.Appended()
	if len(got) != 1 || got[0].ToState != "scheduled" || got[0].FromState != "draft" ||
		got[0].SubjectID != node.ID || got[0].ActorID != "u-sched" || got[0].Surface != "" {
		t.Fatalf("unexpected records: %+v", got)
	}
}

// TestDynamicTypeRepo_WithProvenance_NilRecordsNothing pins the documented
// default: a repo with no store, or one reset to nil, records nothing.
func TestDynamicTypeRepo_WithProvenance_NilRecordsNothing(t *testing.T) {
	app, db, _, store := setupProvenanceTransitionApp(t)
	typeName, slug := defineProvenanceDynamicType(t, app, db, "provnil")
	repo, err := app.DynamicContentRepo(typeName)
	if err != nil {
		t.Fatalf("DynamicContentRepo: %v", err)
	}
	node, err := repo.GetBySlug(context.Background(), slug)
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if err := repo.WithProvenance(nil).SetStatus(context.Background(), node.ID, Published); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if got := store.Appended(); len(got) != 0 {
		t.Errorf("records from a repo with nil store: %+v, want none", got)
	}
}

// TestTransitionItemVia_SubjectProvenance_RoundTrip writes real transitions
// through the SQL store and reads them back through SubjectProvenance: an
// ungated Task transition shows no actor, a gated Decision ratification does.
func TestTransitionItemVia_SubjectProvenance_RoundTrip(t *testing.T) {
	app, db, rs := setupTransitionItemApp(t)
	if err := CreateProvenanceTable(db); err != nil {
		t.Fatalf("CreateProvenanceTable: %v", err)
	}
	store := NewProvenanceStore(db)
	app.Provenance(store)
	ctx := context.Background()

	insertTaskWithTaskID(t, db, "task-p-1", "task-p-1-slug", "T9001", "backlog")
	if _, err := app.TransitionItemVia(NewTestContext(User{ID: "u-task", Roles: []Role{Editor}}), "mcp", "Task", "task-p-1-slug", "active", "claimed"); err != nil {
		t.Fatalf("Task transition: %v", err)
	}
	entries, err := SubjectProvenance(ctx, db, store, "Task", "task-p-1")
	if err != nil {
		t.Fatalf("SubjectProvenance(Task): %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("Task entries = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.Verb != "transition" || e.FromState != "backlog" || e.ToState != "active" || e.Gated {
		t.Errorf("Task entry = %+v, want ungated transition backlog->active", e)
	}
	if e.ActorID != "" || e.ActorKind != "" || e.Surface != "" || e.Reason != "" {
		t.Errorf("ungated Task entry leaked identity: %+v", e)
	}

	insertDecision(t, db, "dec-p-2", "dec-p-2-slug", "proposed")
	tokenID := setupTokenWithRole(t, db, rs, "admin")
	if err := RegisterDecisionStewardRole(ctx, rs); err != nil {
		t.Fatalf("RegisterDecisionStewardRole: %v", err)
	}
	if _, err := rs.Grant(ctx, RoleGrant{TokenID: tokenID, RoleName: "decision-steward"}); err != nil {
		t.Fatalf("Grant decision-steward: %v", err)
	}
	if _, err := app.TransitionItemVia(NewTestContext(User{ID: tokenID}), "mcp", "Decision", "dec-p-2-slug", "ratified", "ratified by steward"); err != nil {
		t.Fatalf("Decision transition: %v", err)
	}
	entries, err = SubjectProvenance(ctx, db, store, "Decision", "dec-p-2")
	if err != nil {
		t.Fatalf("SubjectProvenance(Decision): %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("Decision entries = %d, want 1", len(entries))
	}
	g := entries[0]
	if !g.Gated || g.ActorID != tokenID || g.Surface != "mcp" || g.Reason != "ratified by steward" {
		t.Errorf("gated Decision entry = %+v, want actor %s, surface mcp, reason shown", g, tokenID)
	}
}

// TestCreateProvenanceTable_AddsSubjectIndex proves the index exists after
// boot, including on a table that predates it, and that calling the function
// again is a no-op.
func TestCreateProvenanceTable_AddsSubjectIndex(t *testing.T) {
	db := newSQLiteDB(t)
	// A pre-existing table created by the old DDL, with no index.
	if _, err := db.ExecContext(context.Background(), `
		CREATE TABLE smeldr_provenance (
			id TEXT PRIMARY KEY, timestamp TIMESTAMPTZ NOT NULL,
			subject_type TEXT NOT NULL, subject_id TEXT NOT NULL, verb TEXT NOT NULL,
			from_state TEXT NOT NULL, to_state TEXT NOT NULL, actor_kind TEXT NOT NULL,
			actor_id TEXT NOT NULL, surface TEXT NOT NULL, reason TEXT NOT NULL
		)`); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	indexCount := func() int {
		var n int
		if err := db.QueryRowContext(context.Background(),
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_smeldr_provenance_subject'`,
		).Scan(&n); err != nil {
			t.Fatalf("query sqlite_master: %v", err)
		}
		return n
	}
	if indexCount() != 0 {
		t.Fatal("index present before CreateProvenanceTable")
	}
	for i := 0; i < 2; i++ {
		if err := CreateProvenanceTable(db); err != nil {
			t.Fatalf("CreateProvenanceTable (call %d): %v", i+1, err)
		}
	}
	if indexCount() != 1 {
		t.Errorf("index count after boot = %d, want 1", indexCount())
	}
}

// TestCreateProvenanceTable_ExecErrors covers the failure path: an error from
// the DDL is surfaced to the caller rather than swallowed.
func TestCreateProvenanceTable_ExecErrors(t *testing.T) {
	if err := CreateProvenanceTable(&errExecDB{}); err == nil {
		t.Error("CreateProvenanceTable on a failing DB: want error, got nil")
	}
}
