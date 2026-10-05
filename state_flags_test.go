// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// flagFlow registers (or re-registers) a two-state flow whose second state
// carries the given flags, on a database that may already hold the flow.
func flagFlow(t *testing.T, db DB, typeName string, live State) {
	t.Helper()
	live.Name = "live"
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{
		Name: "flag-flow-" + typeName, TypeName: typeName,
		States:      []State{{Name: "draft", IsInitial: true}, live},
		Transitions: []Transition{{From: "draft", To: "live"}},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
}

type flagsRow struct {
	initial, terminal, suppresses, locked bool
	standing                              string
}

func readFlags(t *testing.T, db DB, typeName, state string) flagsRow {
	t.Helper()
	var r flagsRow
	if err := db.QueryRowContext(context.Background(),
		`SELECT s.is_initial, s.is_terminal, s.suppresses_signals, s.locked, s.standing
		 FROM smeldr_states s JOIN smeldr_state_flows f ON f.id = s.flow_id
		 WHERE f.type_name = $1 AND s.name = $2`, typeName, state,
	).Scan(&r.initial, &r.terminal, &r.suppresses, &r.locked, &r.standing); err != nil {
		t.Fatalf("read flags of %s/%s: %v", typeName, state, err)
	}
	return r
}

// TestRegisterFlow_FlagsUpdateAnExistingRow is the trap: a flag changed in code
// used to be ignored for any state whose row already existed (the upsert was DO
// NOTHING), so a database created before the flag was set never got it.
func TestRegisterFlow_FlagsUpdateAnExistingRow(t *testing.T) {
	tests := []struct {
		name     string
		before   State
		after    State
		wantFlag func(flagsRow) bool
		wantWord string
	}{
		{"locked on", State{}, State{Locked: true}, func(r flagsRow) bool { return r.locked }, "locked"},
		{"locked off", State{Locked: true}, State{}, func(r flagsRow) bool { return !r.locked }, "locked"},
		{"suppresses_signals on", State{}, State{SuppressesSignals: true}, func(r flagsRow) bool { return r.suppresses }, "suppresses_signals"},
		{"suppresses_signals off", State{SuppressesSignals: true}, State{}, func(r flagsRow) bool { return !r.suppresses }, "suppresses_signals"},
		{"is_terminal on", State{}, State{IsTerminal: true}, func(r flagsRow) bool { return r.terminal }, "is_terminal"},
		{"is_initial on", State{}, State{IsInitial: true}, func(r flagsRow) bool { return r.initial }, "is_initial"},
		{"standing on", State{}, State{Standing: StandingHolds}, func(r flagsRow) bool { return r.standing == "holds" }, "standing"},
		{"standing off", State{Standing: StandingHolds}, State{}, func(r flagsRow) bool { return r.standing == "" }, "standing"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := newMigratedDB(t)
			flagFlow(t, db, "FlagT", tc.before)
			out := capturedLog(func() { flagFlow(t, db, "FlagT", tc.after) })
			if !tc.wantFlag(readFlags(t, db, "FlagT", "live")) {
				t.Errorf("the row did not pick up the change: %+v", readFlags(t, db, "FlagT", "live"))
			}
			if n := strings.Count(out, "state flag changed on an existing row"); n != 1 {
				t.Errorf("logged %d change lines, want exactly 1:\n%s", n, out)
			}
			if !strings.Contains(out, "flag="+tc.wantWord) {
				t.Errorf("the line must name the flag %q:\n%s", tc.wantWord, out)
			}
			wantLevel := "level=INFO"
			if tc.wantWord == "locked" || tc.wantWord == "suppresses_signals" {
				wantLevel = "level=WARN"
			}
			if !strings.Contains(out, wantLevel) {
				t.Errorf("want %s for a change to %s:\n%s", wantLevel, tc.wantWord, out)
			}
		})
	}
}

// TestRegisterFlow_UnchangedFlags_NoWriteNoLog: re-registering a flow that did
// not change issues no UPDATE and logs nothing, and a first registration logs
// nothing either (there was no row to compare with).
func TestRegisterFlow_UnchangedFlags_NoWriteNoLog(t *testing.T) {
	db := newMigratedDB(t)
	live := State{Locked: true, SuppressesSignals: true, Standing: StandingHolds}
	first := capturedLog(func() { flagFlow(t, db, "QuietT", live) })
	if strings.Contains(first, "state flag changed") {
		t.Errorf("a first registration logged a change:\n%s", first)
	}
	mustExecDB(t, db, `CREATE TABLE state_updates (n INTEGER)`)
	mustExecDB(t, db, `CREATE TRIGGER count_state_updates AFTER UPDATE ON smeldr_states BEGIN INSERT INTO state_updates VALUES (1); END`)

	out := capturedLog(func() {
		flagFlow(t, db, "QuietT", live)
		flagFlow(t, db, "QuietT", live)
	})
	var n int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM state_updates`).Scan(&n); err != nil {
		t.Fatalf("count updates: %v", err)
	}
	if n != 0 {
		t.Errorf("%d smeldr_states UPDATEs for an unchanged flow, want none", n)
	}
	if strings.Contains(out, "state flag changed") {
		t.Errorf("an unchanged flow logged a change:\n%s", out)
	}
}

func mustExecDB(t *testing.T, db DB, q string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), q); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

// TestRegisterOrchestrationTypes_AppliesLockedToAPreA306Row simulates the
// database A306 never reached: the Decision "ratified" row exists with
// locked = FALSE. The next boot's RegisterFlow now locks it, content edits are
// refused, and the change is a Warn in the boot log.
func TestRegisterOrchestrationTypes_AppliesLockedToAPreA306Row(t *testing.T) {
	_, db, _ := setupTransitionItemApp(t)
	ctx := context.Background()
	if !isStateLocked(ctx, db, "Decision", "ratified") {
		t.Fatal("precondition: ratified is locked after a fresh registration")
	}
	mustExecDB(t, db, `UPDATE smeldr_states SET locked = FALSE
		WHERE name = 'ratified' AND flow_id IN (SELECT id FROM smeldr_state_flows WHERE type_name = 'Decision')`)
	if isStateLocked(ctx, db, "Decision", "ratified") {
		t.Fatal("precondition: the simulated old row is unlocked")
	}

	// A fresh app on the same database is the next boot: the first app already
	// holds the routes, so registering on it again would panic on the mux.
	next := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte(transitionItemTestSecret),
		DB:      db,
	}))
	if err := next.Governance(NewRoleStore(db)); err != nil {
		t.Fatalf("Governance: %v", err)
	}
	out := capturedLog(func() { RegisterOrchestrationTypes(next, db) })

	if !isStateLocked(ctx, db, "Decision", "ratified") {
		t.Error("the next boot did not lock the ratified row")
	}
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "state=ratified") || !strings.Contains(out, "flag=locked") {
		t.Errorf("the locked change must be a Warn naming the state:\n%s", out)
	}
}

// TestRegisterFlow_ClearsStaleInitial: a state dropped from the flow keeps its
// row, so moving the initial state used to leave two rows initial.
func TestRegisterFlow_ClearsStaleInitial(t *testing.T) {
	db := newMigratedDB(t)
	app := &App{cfg: Config{DB: db}}
	reg := func(states ...State) error {
		return app.RegisterFlow(StateFlow{Name: "init-flow", TypeName: "InitT", States: states})
	}
	if err := reg(State{Name: "draft", IsInitial: true}, State{Name: "live"}); err != nil {
		t.Fatalf("first: %v", err)
	}
	out := capturedLog(func() {
		if err := reg(State{Name: "start", IsInitial: true}, State{Name: "live"}); err != nil {
			t.Fatalf("second: %v", err)
		}
	})
	if readFlags(t, db, "InitT", "draft").initial {
		t.Error("a state dropped from the flow is still marked initial")
	}
	if !readFlags(t, db, "InitT", "start").initial {
		t.Error("the new initial state is not marked initial")
	}
	if got := defaultInitialState(context.Background(), db, "InitT"); got != "start" {
		t.Errorf("defaultInitialState = %q, want start", got)
	}
	if !strings.Contains(out, "cleared is_initial on states no longer in the flow") {
		t.Errorf("clearing a stale initial must be logged:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "cleared is_initial") && !strings.Contains(line, "level=INFO") {
			t.Errorf("clearing a stale initial is an Info line:\n%s", line)
		}
	}

	// A flow that names no initial state is left alone.
	if err := reg(State{Name: "live"}); err != nil {
		t.Fatalf("no initial: %v", err)
	}
	if !readFlags(t, db, "InitT", "start").initial {
		t.Error("a list with no initial state must not clear the existing initial row")
	}
}

// TestRegisterFlow_ClearStaleInitialFails: the guard's own failure is returned.
func TestRegisterFlow_ClearStaleInitialFails(t *testing.T) {
	db := newMigratedDB(t)
	app := &App{cfg: Config{DB: &failMatchDB{DB: db, exec: "SET is_initial = FALSE"}}}
	err := app.RegisterFlow(StateFlow{Name: "init-fail", TypeName: "InitFail", States: []State{{Name: "draft", IsInitial: true}}})
	if err == nil {
		t.Fatal("want the guard's failure returned")
	}
}

// TestExistingStateFlags_ToleratesAnUnreadableTable: the pre-read is best
// effort and never fails the registration.
func TestExistingStateFlags_ToleratesAnUnreadableTable(t *testing.T) {
	var _ *sql.DB
	db := newMigratedDB(t)
	if got := existingStateFlags(context.Background(), &failMatchDB{DB: db, query: "FROM smeldr_states WHERE flow_id"}, "x"); got != nil {
		t.Errorf("a failed read = %v, want nil", got)
	}
	if got := existingStateFlags(context.Background(), &flowIDDB{}, "x"); got != nil {
		t.Errorf("a nil result set = %v, want nil", got)
	}
}
