// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// governanceOnlyApp is an instance with core governance and without the
// Orchestration add-on (T881).
func governanceOnlyApp(t *testing.T) (*App, *sql.DB, *RelationStore) {
	t.Helper()
	db := newSQLiteDB(t)
	if err := CreateGovernanceTables(db); err != nil {
		t.Fatal(err)
	}
	if err := CreateRelationTables(db); err != nil {
		t.Fatal(err)
	}
	app := New(MustConfig(Config{BaseURL: "https://example.com", Secret: []byte(transitionItemTestSecret), DB: db}))
	RegisterGovernanceTypes(app, db)
	rs, err := NewRelationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterGovernanceRelationKinds(context.Background(), rs); err != nil {
		t.Fatal(err)
	}
	app.Relations(rs)
	return app, db, rs
}

// A governance-only instance has Signal, Decision and Amendment, their kinds
// and addresses, and nothing of the add-on; the combined entry points still
// register both halves.
func TestGovernanceOnly_Registration(t *testing.T) {
	app, db, rs := governanceOnlyApp(t)
	for _, name := range []string{"Signal", "Decision", "Amendment"} {
		if app.typeRegistry.Lookup(name) == nil {
			t.Errorf("%s not registered", name)
		}
	}
	for _, name := range []string{"Task", "Goal", "Run"} {
		if app.typeRegistry.Lookup(name) != nil {
			t.Errorf("%s registered without the add-on", name)
		}
	}
	for _, table := range []string{"smeldr_tasks", "smeldr_goals", "smeldr_runs"} {
		if ok, _ := tableExists(context.Background(), db, table); ok {
			t.Errorf("%s created without the add-on", table)
		}
	}
	for _, k := range []string{"amends", "supersedes", "contradicts", "about", "refers_to", "belongs_to_domain"} {
		if _, ok := rs.GetKind(k); !ok {
			t.Errorf("governance kind %s missing", k)
		}
	}
	for _, k := range []string{"derives_from", "depends_on", "ships_as", "contains", "investigates", "implements"} {
		if _, ok := rs.GetKind(k); ok {
			t.Errorf("add-on kind %s registered without the add-on", k)
		}
	}
	// The combined calls register both halves.
	_, _, fullRS := governanceOnlyApp(t)
	if err := RegisterOrchestrationAddOnRelationKinds(context.Background(), fullRS); err != nil {
		t.Fatal(err)
	}
	if got := len(fullRS.ListKinds()); got != 18 {
		t.Errorf("governance + add-on kinds = %d, want 18", got)
	}
	both, bothDB := signalTestApp(t)
	for _, name := range []string{"Signal", "Decision", "Amendment", "Task", "Goal", "Run"} {
		if both.typeRegistry.Lookup(name) == nil {
			t.Errorf("combined registration lacks %s", name)
		}
	}
	if ok, _ := tableExists(context.Background(), bothDB, "smeldr_tasks"); !ok {
		t.Error("CreateOrchestrationTables did not create smeldr_tasks")
	}
}

// A task_ref names a Task, and on an instance without the add-on it is
// refused with a message that says so, on the module path and on the raw
// insert path (mcp create_signal) alike; an empty task_ref is fine.
func TestGovernanceOnly_TaskRefRefused(t *testing.T) {
	app, db, _ := governanceOnlyApp(t)
	_, err := findModule[*Amendment](t, app).MCPCreate(authorCtx(), map[string]any{"summary": "s", "amendment_type": "fix", "task_ref": "T1"})
	wantAddOnRefusal(t, err, "task_ref")
	if _, err := findModule[*Amendment](t, app).MCPCreate(authorCtx(), map[string]any{"summary": "s", "amendment_type": "fix"}); err != nil {
		t.Errorf("an Amendment without task_ref: %v", err)
	}
	_, err = findModule[*Signal](t, app).MCPCreate(authorCtx(), map[string]any{"sender": "core", "receiver": "architect", "signal_type": "notice", "message": "m", "task_ref": "T1"})
	wantAddOnRefusal(t, err, "task_ref")
	err = InsertSignal(context.Background(), db, &Signal{Sender: "core", Receiver: "architect", SignalType: "notice", Message: "m", TaskRef: "T1"})
	wantAddOnRefusal(t, err, "task_ref")
	if err := InsertSignal(context.Background(), db, &Signal{Sender: "core", Receiver: "architect", SignalType: "notice", Message: "m"}); err != nil {
		t.Errorf("a Signal without task_ref: %v", err)
	}
	if app.addOnRefusal(nil) != "" || app.addOnRefusal([]string{"Decision"}) != "" {
		t.Error("a non-add-on reference was refused")
	}
}

func wantAddOnRefusal(t *testing.T, err error, field string) {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) || !strings.Contains(ve.Error(), field) || !strings.Contains(ve.Error(), "Orchestration add-on") {
		t.Errorf("err = %v; want the %s add-on refusal", err, field)
	}
}

// Goal context and context packets for add-on types are a bad request on an
// instance without the add-on, not an internal failure.
func TestGovernanceOnly_ReadsOfAddOnTypes(t *testing.T) {
	_, db, rs := governanceOnlyApp(t)
	ctx := context.Background()
	if _, err := QueryGoalContext(ctx, db, rs, "G1"); !errors.Is(err, ErrBadRequest) || !strings.Contains(err.Error(), "Orchestration add-on") {
		t.Errorf("goal context = %v, want the add-on bad request", err)
	}
	for _, anchor := range []string{"task", "goal", "run"} {
		if _, err := BuildContextPacket(ctx, db, rs, "https://x", "src", anchor, "x", 1); !errors.Is(err, ErrBadRequest) {
			t.Errorf("packet anchor %s = %v, want ErrBadRequest", anchor, err)
		}
	}
	if err := requireRecordTable(ctx, db, "smeldr_nope", "Nope"); !errors.Is(err, ErrBadRequest) || !strings.Contains(err.Error(), "no Nope records") {
		t.Errorf("unknown table = %v", err)
	}
	if err := requireRecordTable(ctx, db, "smeldr_decisions", "Decision"); err != nil {
		t.Errorf("existing table = %v", err)
	}
}
