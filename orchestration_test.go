package smeldr

import (
	"context"
	"testing"
)

// TestOrchestrationTypes_embedNode verifies at compile time that all four
// orchestration types embed [Node] and are pointer-receiverable by the
// generic content module infrastructure.
func TestOrchestrationTypes_embedNode(t *testing.T) {
	t.Run("Signal", func(t *testing.T) {
		var s Signal
		_ = s.Node
		_ = s.Slug
	})
	t.Run("Task", func(t *testing.T) {
		var tk Task
		_ = tk.Node
		_ = tk.Slug
	})
	t.Run("Decision", func(t *testing.T) {
		var d Decision
		_ = d.Node
		_ = d.Slug
	})
	t.Run("Amendment", func(t *testing.T) {
		var a Amendment
		_ = a.Node
		_ = a.Slug
	})
}

// TestSignalFlow_definition verifies the signal-protocol flow has the expected
// states and transitions without requiring a database.
func TestSignalFlow_definition(t *testing.T) {
	f := orchSignalFlow()
	if f.Name != "signal-protocol" {
		t.Errorf("Name = %q, want %q", f.Name, "signal-protocol")
	}
	if f.TypeName != "Signal" {
		t.Errorf("TypeName = %q, want %q", f.TypeName, "Signal")
	}
	wantStates := []string{"pending", "read", "acknowledged", "expired"}
	if got := stateNames(f); got != join(wantStates) {
		t.Errorf("states = %s, want %s", got, join(wantStates))
	}
	wantInitial := "pending"
	if got := initialState(f); got != wantInitial {
		t.Errorf("initial = %q, want %q", got, wantInitial)
	}
	wantTerminals := []string{"acknowledged", "expired"}
	if got := terminalStates(f); got != join(wantTerminals) {
		t.Errorf("terminals = %s, want %s", got, join(wantTerminals))
	}
	if len(f.Transitions) != 4 {
		t.Errorf("transitions count = %d, want 4", len(f.Transitions))
	}
}

// TestTaskFlow_definition verifies the architect-task flow definition.
func TestTaskFlow_definition(t *testing.T) {
	f := orchTaskFlow()
	if f.Name != "architect-task" {
		t.Errorf("Name = %q, want %q", f.Name, "architect-task")
	}
	if f.TypeName != "Task" {
		t.Errorf("TypeName = %q, want %q", f.TypeName, "Task")
	}
	if len(f.States) != 9 {
		t.Errorf("state count = %d, want 9", len(f.States))
	}
	if len(f.Transitions) != 9 {
		t.Errorf("transition count = %d, want 9", len(f.Transitions))
	}
	if got := initialState(f); got != "backlog" {
		t.Errorf("initial = %q, want %q", got, "backlog")
	}
}

// TestDecisionFlow_definition verifies the governance-decision flow definition.
func TestDecisionFlow_definition(t *testing.T) {
	f := orchDecisionFlow()
	if f.Name != "governance-decision" {
		t.Errorf("Name = %q, want %q", f.Name, "governance-decision")
	}
	if f.TypeName != "Decision" {
		t.Errorf("TypeName = %q, want %q", f.TypeName, "Decision")
	}
	if len(f.States) != 5 {
		t.Errorf("state count = %d, want 5", len(f.States))
	}
	if len(f.Transitions) != 7 {
		t.Errorf("transition count = %d, want 7", len(f.Transitions))
	}
	if got := initialState(f); got != "proposed" {
		t.Errorf("initial = %q, want %q", got, "proposed")
	}
}

// TestAmendmentFlow_definition verifies the amendment-lifecycle flow definition.
func TestAmendmentFlow_definition(t *testing.T) {
	f := orchAmendmentFlow()
	if f.Name != "amendment-lifecycle" {
		t.Errorf("Name = %q, want %q", f.Name, "amendment-lifecycle")
	}
	if f.TypeName != "Amendment" {
		t.Errorf("TypeName = %q, want %q", f.TypeName, "Amendment")
	}
	if len(f.States) != 6 {
		t.Errorf("state count = %d, want 6", len(f.States))
	}
	if len(f.Transitions) != 6 {
		t.Errorf("transition count = %d, want 6", len(f.Transitions))
	}
	if got := initialState(f); got != "scoped" {
		t.Errorf("initial = %q, want %q", got, "scoped")
	}
}

// TestCreateOrchestrationTables verifies that CreateOrchestrationTables creates
// all four tables without error and that they are queryable.
func TestCreateOrchestrationTables(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	ctx := context.Background()
	for _, table := range []string{
		"smeldr_signals", "smeldr_tasks", "smeldr_decisions", "smeldr_amendments",
	} {
		row := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table)
		var n int
		if err := row.Scan(&n); err != nil {
			t.Errorf("table %s not queryable: %v", table, err)
		}
	}
}

// TestRegisterOrchestrationTypes_nilDB verifies that RegisterOrchestrationTypes
// tolerates an App with nil DB by logging and continuing (fail-open).
func TestRegisterOrchestrationTypes_nilDB(t *testing.T) {
	app := New(Config{BaseURL: "http://localhost", Secret: []byte("test-secret-key!!")})
	// Must not panic; errors are logged internally.
	RegisterOrchestrationTypes(app, nil)
}

// TestRegisterOrchestrationTypes_flows verifies that with a real SQLite DB all
// four flows are persisted via RegisterFlow without error.
func TestRegisterOrchestrationTypes_flows(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	app := New(Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-key!!"),
		DB:      db,
	})
	// Must not panic; should register all 4 flows without logging errors.
	RegisterOrchestrationTypes(app, db)

	// Verify the 4 orchestration flows were inserted (exclude the default seed flow).
	row := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM smeldr_state_flows WHERE type_name IS NOT NULL")
	var n int
	if err := row.Scan(&n); err != nil {
		t.Fatalf("count state flows: %v", err)
	}
	if n != 4 {
		t.Errorf("registered flow count = %d, want 4", n)
	}
}

// stateNames returns a space-joined sorted list of state names in f.
func stateNames(f StateFlow) string {
	names := make([]string, len(f.States))
	for i, s := range f.States {
		names[i] = s.Name
	}
	return join(names)
}

// initialState returns the name of the initial state in f, or "" if none.
func initialState(f StateFlow) string {
	for _, s := range f.States {
		if s.IsInitial {
			return s.Name
		}
	}
	return ""
}

// terminalStates returns a space-joined list of terminal state names in f.
func terminalStates(f StateFlow) string {
	var names []string
	for _, s := range f.States {
		if s.IsTerminal {
			names = append(names, s.Name)
		}
	}
	return join(names)
}

// join concatenates ss with a single space separator. Returns "" for nil input.
func join(ss []string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += " "
		}
		result += s
	}
	return result
}
