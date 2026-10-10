// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// d109Open is D109's table of open states, the only states of the five
// orchestration flows that declare AwaitsAction. Every other state is closed.
var d109Open = map[string][]string{
	"Decision":  {"proposed", "pending-re-evaluation"},
	"Task":      {"backlog", "active", "waiting-plan", "plan-reviewing", "implementing", "commit-reviewing", "blocked"},
	"Amendment": {"scoped", "in-progress", "commit-ready", "committed"},
	"Goal":      {"open", "in-progress", "parked"},
	"Signal":    {"pending", "read"},
}

// d109Closed names every closed state, so a state added to a flow later fails
// the table test until it is classified.
var d109Closed = map[string][]string{
	"Decision":  {"ratified", "superseded", "archived"},
	"Task":      {"done", "deferred", "resolved"},
	"Amendment": {"merged", "rejected"},
	"Goal":      {"done", "resolved"},
	"Signal":    {"acknowledged", "expired"},
}

// TestOrchestrationFlows_AwaitsActionPerD109 pins D109's table state by state,
// in code (the StateFlow definitions) and in the stored rows.
func TestOrchestrationFlows_AwaitsActionPerD109(t *testing.T) {
	_, db, _ := setupTransitionItemApp(t)
	ctx := context.Background()
	for typeName, open := range d109Open {
		got, found, err := StatesAwaitingAction(ctx, db, typeName)
		if err != nil || !found {
			t.Fatalf("%s: StatesAwaitingAction = %v, %v, %v", typeName, got, found, err)
		}
		if len(got) != len(open) {
			t.Errorf("%s: open states %v, want exactly %v", typeName, got, open)
		}
		for _, s := range open {
			if !got[s] {
				t.Errorf("%s %q: closed, want open (D109)", typeName, s)
			}
		}
		for _, s := range d109Closed[typeName] {
			if got[s] {
				t.Errorf("%s %q: open, want closed (D109)", typeName, s)
			}
		}
		var states int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_states s JOIN smeldr_state_flows f ON f.id = s.flow_id WHERE f.type_name = $1`, typeName).Scan(&states); err != nil {
			t.Fatal(err)
		}
		if want := len(open) + len(d109Closed[typeName]); states != want {
			t.Errorf("%s has %d states, the D109 table classifies %d: classify the new state", typeName, states, want)
		}
	}
	for _, typeName := range []string{"Rule", "AuthorityStub"} {
		if got, _, err := StatesAwaitingAction(ctx, db, typeName); err != nil || len(got) != 0 {
			t.Errorf("%s: %v, %v; want closed by omission", typeName, got, err)
		}
	}
}

// TestEnsureStateAwaitsActionColumn_ExistingInstance: a smeldr_states table from
// before D109 gains the column, and the next boot's registration writes the
// declared values into the existing rows.
func TestEnsureStateAwaitsActionColumn_ExistingInstance(t *testing.T) {
	_, db, _ := setupTransitionItemApp(t)
	ctx := context.Background()
	mustExecDB(t, db, `ALTER TABLE smeldr_states DROP COLUMN awaits_action`)
	if got, found, err := StatesAwaitingAction(ctx, db, "Task"); err != nil || !found || len(got) != 0 {
		t.Fatalf("before the column: %v, %v, %v; want found and all closed", got, found, err)
	}
	if err := EnsureStateAwaitsActionColumn(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := EnsureStateAwaitsActionColumn(ctx, db); err != nil {
		t.Fatalf("not idempotent: %v", err)
	}
	if got, _, _ := StatesAwaitingAction(ctx, db, "Task"); len(got) != 0 {
		t.Fatalf("the new column must default to closed, got %v", got)
	}
	next := New(MustConfig(Config{BaseURL: "https://example.com", Secret: []byte(transitionItemTestSecret), DB: db}))
	out := capturedLog(func() { RegisterOrchestrationTypes(next, db) })
	got, _, err := StatesAwaitingAction(ctx, db, "Task")
	if err != nil || len(got) != len(d109Open["Task"]) || !got["blocked"] {
		t.Errorf("after the next boot: %v, %v; want D109's Task states", got, err)
	}
	if !strings.Contains(out, "flag=awaits_action") || !strings.Contains(out, "level=INFO") {
		t.Errorf("the change must be logged at Info:\n%s", out)
	}
}

type awaitsFailDB struct {
	DB
	match string
	err   error
}

func (d *awaitsFailDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if strings.Contains(q, d.match) {
		return nil, d.err
	}
	return d.DB.QueryContext(ctx, q, args...)
}

func (d *awaitsFailDB) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	if strings.Contains(q, d.match) {
		return d.DB.QueryRowContext(ctx, "SELECT no_such_function_awaits()")
	}
	return d.DB.QueryRowContext(ctx, q, args...)
}

// TestStatesAwaitingAction_Paths covers each answer: a declared flow, the
// default flow, no flow tables, no DB, and the errors that are returned.
func TestStatesAwaitingAction_Paths(t *testing.T) {
	ctx := context.Background()
	db := newMigratedDB(t)
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{Name: "aw", TypeName: "AwT",
		States: []State{{Name: "todo", IsInitial: true, AwaitsAction: true}, {Name: "done", IsTerminal: true}}}); err != nil {
		t.Fatal(err)
	}
	if got, found, err := StatesAwaitingAction(ctx, db, "AwT"); err != nil || !found || !got["todo"] || got["done"] || got["unknown"] {
		t.Errorf("declared flow = %v, %v, %v", got, found, err)
	}
	if got, found, err := StatesAwaitingAction(ctx, db, "NoFlowT"); err != nil || !found || got == nil || len(got) != 0 {
		t.Errorf("default flow = %v, %v, %v; want found, all closed, non-nil", got, found, err)
	}
	if got, found, err := StatesAwaitingAction(ctx, nil, "AwT"); got != nil || found || err != nil {
		t.Errorf("no DB = %v, %v, %v", got, found, err)
	}
	if _, found, err := StatesAwaitingAction(ctx, newSQLiteDB(t), "AwT"); found || err != nil {
		t.Errorf("no flow tables = %v, %v; want not found, no error", found, err)
	}
	empty := newSQLiteDB(t)
	mustExecDB(t, empty, `CREATE TABLE smeldr_state_flows (id TEXT, name TEXT, type_name TEXT)`)
	if _, found, err := StatesAwaitingAction(ctx, empty, "AwT"); found || err != nil {
		t.Errorf("no flow at all = %v, %v; want not found", found, err)
	}
	if _, _, err := StatesAwaitingAction(ctx, &awaitsFailDB{DB: db, match: "FROM smeldr_state_flows WHERE type_name"}, "AwT"); err == nil {
		t.Error("a failed flow lookup must be returned")
	}
	if _, _, err := StatesAwaitingAction(ctx, &awaitsFailDB{DB: db, match: "awaits_action = TRUE", err: errors.New("disk")}, "AwT"); err == nil {
		t.Error("a failed state query must be returned")
	}
}

// TestValidTransitions_ToAwaitsAction: each option says whether its target
// state is open, so a reader sees which transitions close an item.
func TestValidTransitions_ToAwaitsAction(t *testing.T) {
	app, _, _ := setupTransitionItemApp(t)
	opts, err := app.ValidTransitions(context.Background(), "Task", "commit-reviewing")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, o := range opts {
		got[o.ToState] = o.ToAwaitsAction
	}
	if v, ok := got["done"]; !ok || v {
		t.Errorf("commit-reviewing to done: %v (present %v); want closing", v, ok)
	}
	if v, ok := got["implementing"]; !ok || !v {
		t.Errorf("commit-reviewing to implementing: %v (present %v); want open", v, ok)
	}
}

// TestBuildContextPacket_AwaitsAction: the packet carries the fact next to
// standing, true or false, and leaves it out only when the flow cannot be read.
func TestBuildContextPacket_AwaitsAction(t *testing.T) {
	app, db, rs := setupTransitionItemApp(t)
	ratifyDecision(t, app, db, rs, "dec-aw", "dec-aw-slug")
	pkt, err := BuildContextPacket(context.Background(), db, nil, "https://example.com", "test", "decision", "dec-aw-slug", 1)
	if err != nil {
		t.Fatal(err)
	}
	if pkt.Anchor.AwaitsAction == nil || *pkt.Anchor.AwaitsAction {
		t.Errorf("a ratified Decision: awaits_action = %v, want false (closed)", pkt.Anchor.AwaitsAction)
	}
	insertSignal(t, db, "sg-aw", "sg-aw-slug", "pending")
	spkt, err := BuildContextPacket(context.Background(), db, nil, "https://example.com", "test", "signal", "sg-aw-slug", 1)
	if err != nil {
		t.Fatal(err)
	}
	if spkt.Anchor.AwaitsAction == nil || !*spkt.Anchor.AwaitsAction {
		t.Errorf("a pending Signal: awaits_action = %v, want true (open)", spkt.Anchor.AwaitsAction)
	}

	p := packetAwaits{}
	failing := &awaitsFailDB{DB: db, match: "awaits_action = TRUE", err: errors.New("disk")}
	if got := p.of(context.Background(), failing, "Signal", "pending"); got != nil {
		t.Errorf("an unreadable flow = %v, want nil (unknown)", *got)
	}
	if got := p.of(context.Background(), db, "Signal", "pending"); got != nil {
		t.Error("the answer per type is cached for the packet")
	}
}
