package smeldr

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestOrchestrationTypes_embedNode verifies at compile time that all six
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
	t.Run("Goal", func(t *testing.T) {
		var g Goal
		_ = g.Node
		_ = g.Slug
		_ = g.GoalID
	})
	t.Run("Run", func(t *testing.T) {
		var r Run
		_ = r.Node
		_ = r.Slug
		_ = r.TaskID
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

// TestTaskFlow_definition verifies the agent-task flow definition.
func TestTaskFlow_definition(t *testing.T) {
	f := orchTaskFlow()
	if f.Name != "agent-task" {
		t.Errorf("Name = %q, want %q", f.Name, "agent-task")
	}
	if f.TypeName != "Task" {
		t.Errorf("TypeName = %q, want %q", f.TypeName, "Task")
	}
	if len(f.States) != 10 {
		t.Errorf("state count = %d, want 10", len(f.States))
	}
	if len(f.Transitions) != 12 {
		t.Errorf("transition count = %d, want 12", len(f.Transitions))
	}
	if got := initialState(f); got != "backlog" {
		t.Errorf("initial = %q, want %q", got, "backlog")
	}
}

// TestTaskFlow_ResolvedReachableFromThreeStates pins exactly which states may
// close a Task as "resolved" (D58): active, waiting-plan, plan-reviewing —
// every state that precedes a real build starting — and confirms
// implementing/commit-reviewing/blocked deliberately cannot.
func TestTaskFlow_ResolvedReachableFromThreeStates(t *testing.T) {
	f := orchTaskFlow()
	want := map[string]bool{
		"active":         true,
		"waiting-plan":   true,
		"plan-reviewing": true,
	}
	notWant := []string{"implementing", "commit-reviewing", "blocked", "backlog", "done", "deferred"}

	got := map[string]bool{}
	for _, tr := range f.Transitions {
		if tr.To == "resolved" {
			got[tr.From] = true
			if !tr.RequiredReason {
				t.Errorf("transition %s→resolved: RequiredReason = false, want true", tr.From)
			}
		}
	}
	for from := range want {
		if !got[from] {
			t.Errorf("missing transition %s→resolved", from)
		}
	}
	for _, from := range notWant {
		if got[from] {
			t.Errorf("unexpected transition %s→resolved", from)
		}
	}
}

// TestTaskFlow_ResolvedIsTerminal confirms "resolved" is a genuine sink: no
// transition anywhere in the flow has it as a source.
func TestTaskFlow_ResolvedIsTerminal(t *testing.T) {
	f := orchTaskFlow()
	var resolved *State
	for i := range f.States {
		if f.States[i].Name == "resolved" {
			resolved = &f.States[i]
		}
	}
	if resolved == nil {
		t.Fatal(`"resolved" state not found`)
	}
	if !resolved.IsTerminal {
		t.Error(`"resolved".IsTerminal = false, want true`)
	}
	for _, tr := range f.Transitions {
		if tr.From == "resolved" {
			t.Errorf("unexpected outbound transition resolved→%s", tr.To)
		}
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

	// D34/D40: ratify and supersede require the "admin" role, fail-closed
	// — both from "proposed"/"ratified" directly (D34) and from
	// "pending-re-evaluation" (D40): re-evaluation is the same
	// authority-bearing act through a different door.
	var ratify, supersede, reEvalRatify, reEvalSupersede *Transition
	for i := range f.Transitions {
		tr := &f.Transitions[i]
		switch {
		case tr.From == "proposed" && tr.To == "ratified":
			ratify = tr
		case tr.From == "ratified" && tr.To == "superseded":
			supersede = tr
		case tr.From == "pending-re-evaluation" && tr.To == "ratified":
			reEvalRatify = tr
		case tr.From == "pending-re-evaluation" && tr.To == "superseded":
			reEvalSupersede = tr
		}
	}
	if ratify == nil {
		t.Fatal("proposed→ratified transition not found")
	}
	if ratify.RequiredRole != "admin" || !ratify.Strict {
		t.Errorf("proposed→ratified: RequiredRole=%q Strict=%v, want %q true", ratify.RequiredRole, ratify.Strict, "admin")
	}
	if supersede == nil {
		t.Fatal("ratified→superseded transition not found")
	}
	if supersede.RequiredRole != "admin" || !supersede.Strict {
		t.Errorf("ratified→superseded: RequiredRole=%q Strict=%v, want %q true", supersede.RequiredRole, supersede.Strict, "admin")
	}
	if reEvalRatify == nil {
		t.Fatal("pending-re-evaluation→ratified transition not found")
	}
	if reEvalRatify.RequiredRole != "admin" || !reEvalRatify.Strict {
		t.Errorf("pending-re-evaluation→ratified: RequiredRole=%q Strict=%v, want %q true", reEvalRatify.RequiredRole, reEvalRatify.Strict, "admin")
	}
	if reEvalSupersede == nil {
		t.Fatal("pending-re-evaluation→superseded transition not found")
	}
	if reEvalSupersede.RequiredRole != "admin" || !reEvalSupersede.Strict {
		t.Errorf("pending-re-evaluation→superseded: RequiredRole=%q Strict=%v, want %q true", reEvalSupersede.RequiredRole, reEvalSupersede.Strict, "admin")
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
// all six tables without error and that they are queryable.
func TestCreateOrchestrationTables(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	ctx := context.Background()
	for _, table := range []string{
		"smeldr_signals", "smeldr_tasks", "smeldr_decisions", "smeldr_amendments", "smeldr_goals", "smeldr_runs",
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

// TestRegisterOrchestrationTypes_flows verifies that with a real SQLite DB
// exactly the five flow-bearing types are persisted via RegisterFlow, and
// that Run — deliberately flow-less, D38 — gets none.
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
	// Must not panic; should register all 5 flows (not 6 — Run gets none)
	// without logging errors.
	RegisterOrchestrationTypes(app, db)

	// Verify the 5 orchestration flows were inserted (exclude the default seed flow).
	row := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM smeldr_state_flows WHERE type_name IS NOT NULL")
	var n int
	if err := row.Scan(&n); err != nil {
		t.Fatalf("count state flows: %v", err)
	}
	if n != 5 {
		t.Errorf("registered flow count = %d, want 5 (Run must not add a sixth)", n)
	}

	// Load-bearing assertion, not just an unchanged count: a type
	// deliberately registered *without* a flow is a first for this
	// codebase. An unchanged count of 5 alone could also mean "I forgot
	// to register Run's module at all" — this proves specifically that no
	// row exists for type_name='Run', not merely that the total is right.
	var runFlowCount int
	row = db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM smeldr_state_flows WHERE type_name = 'Run'")
	if err := row.Scan(&runFlowCount); err != nil {
		t.Fatalf("count Run state flows: %v", err)
	}
	if runFlowCount != 0 {
		t.Errorf("Run state flow count = %d, want 0 (Run registers no StateFlow, D38)", runFlowCount)
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

// TestCreateOrchestrationTables_DBError verifies that CreateOrchestrationTables
// returns an error when the database rejects a DDL statement.
func TestCreateOrchestrationTables_DBError(t *testing.T) {
	db := newSQLiteDB(t)
	failing := &execFailDB{DB: db, failOn: "smeldr_signals"}
	if err := CreateOrchestrationTables(failing); err == nil {
		t.Error("expected error from failing DB, got nil")
	}
}

// TestGoalFlow_definition verifies the goal-lifecycle flow definition.
func TestGoalFlow_definition(t *testing.T) {
	f := orchGoalFlow()
	if f.Name != "goal-lifecycle" {
		t.Errorf("Name = %q, want %q", f.Name, "goal-lifecycle")
	}
	if f.TypeName != "Goal" {
		t.Errorf("TypeName = %q, want %q", f.TypeName, "Goal")
	}
	wantStates := []string{"open", "in-progress", "done", "parked", "resolved"}
	if got := stateNames(f); got != join(wantStates) {
		t.Errorf("states = %s, want %s", got, join(wantStates))
	}
	if got := initialState(f); got != "open" {
		t.Errorf("initial = %q, want %q", got, "open")
	}
	wantTerminals := []string{"done", "resolved"}
	if got := terminalStates(f); got != join(wantTerminals) {
		t.Errorf("terminals = %s, want %s", got, join(wantTerminals))
	}
	if len(f.Transitions) != 8 {
		t.Errorf("transition count = %d, want 8", len(f.Transitions))
	}
}

// TestGoalFlow_ParkedNoLongerTerminal is an explicit regression pin, not just
// an updated count assertion: "parked" has an outbound edge to "open" (a
// resumable pause), so labeling it IsTerminal was dishonest (D58, T146) —
// this fails loudly if that label ever comes back.
func TestGoalFlow_ParkedNoLongerTerminal(t *testing.T) {
	f := orchGoalFlow()
	for _, s := range f.States {
		if s.Name == "parked" && s.IsTerminal {
			t.Error(`"parked".IsTerminal = true, want false — parked→open exists, it is not a sink`)
		}
	}
}

// TestGoalFlow_ResolvedReachableFromThreeStates mirrors the Task flow's own
// equivalent test: open, in-progress, parked can all close a Goal as
// "resolved" (D58), each requiring a reason.
func TestGoalFlow_ResolvedReachableFromThreeStates(t *testing.T) {
	f := orchGoalFlow()
	want := map[string]bool{
		"open":        true,
		"in-progress": true,
		"parked":      true,
	}
	got := map[string]bool{}
	for _, tr := range f.Transitions {
		if tr.To == "resolved" {
			got[tr.From] = true
			if !tr.RequiredReason {
				t.Errorf("transition %s→resolved: RequiredReason = false, want true", tr.From)
			}
		}
	}
	for from := range want {
		if !got[from] {
			t.Errorf("missing transition %s→resolved", from)
		}
	}
	if len(got) != len(want) {
		t.Errorf("unexpected extra →resolved transitions: got %v, want %v", got, want)
	}
}

// TestGoalFlow_ResolvedIsTerminal mirrors the Task flow's own equivalent test.
func TestGoalFlow_ResolvedIsTerminal(t *testing.T) {
	f := orchGoalFlow()
	var resolved *State
	for i := range f.States {
		if f.States[i].Name == "resolved" {
			resolved = &f.States[i]
		}
	}
	if resolved == nil {
		t.Fatal(`"resolved" state not found`)
	}
	if !resolved.IsTerminal {
		t.Error(`"resolved".IsTerminal = false, want true`)
	}
	for _, tr := range f.Transitions {
		if tr.From == "resolved" {
			t.Errorf("unexpected outbound transition resolved→%s", tr.To)
		}
	}
}

// TestQueryGoalContext covers the five error and happy-path cases for
// [QueryGoalContext].
func TestQueryGoalContext(t *testing.T) {
	ctx := context.Background()

	t.Run("empty_goalID", func(t *testing.T) {
		db := newSQLiteDB(t)
		if err := CreateOrchestrationTables(db); err != nil {
			t.Fatalf("CreateOrchestrationTables: %v", err)
		}
		_, err := QueryGoalContext(ctx, db, nil, "")
		if err != ErrBadRequest {
			t.Errorf("err = %v, want ErrBadRequest", err)
		}
	})

	t.Run("nil_db", func(t *testing.T) {
		_, err := QueryGoalContext(ctx, nil, nil, "T999")
		if err != ErrInternal {
			t.Errorf("err = %v, want ErrInternal", err)
		}
	})

	t.Run("goal_not_found", func(t *testing.T) {
		db := newSQLiteDB(t)
		if err := CreateOrchestrationTables(db); err != nil {
			t.Fatalf("CreateOrchestrationTables: %v", err)
		}
		_, err := QueryGoalContext(ctx, db, nil, "T999")
		if err != ErrNotFound {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("nil_rs_returns_goal_only", func(t *testing.T) {
		db := newSQLiteDB(t)
		if err := CreateOrchestrationTables(db); err != nil {
			t.Fatalf("CreateOrchestrationTables: %v", err)
		}
		goalNodeID := insertTestGoal(t, db, "T114", "P0", "M")
		_ = goalNodeID

		gc, err := QueryGoalContext(ctx, db, nil, "T114")
		if err != nil {
			t.Fatalf("QueryGoalContext: %v", err)
		}
		if gc.Goal == nil || gc.Goal.GoalID != "T114" {
			t.Errorf("Goal.GoalID = %v, want T114", gc.Goal)
		}
		if len(gc.LinkedDecisions) != 0 {
			t.Errorf("LinkedDecisions = %d, want 0", len(gc.LinkedDecisions))
		}
		if len(gc.LinkedTasks) != 0 {
			t.Errorf("LinkedTasks = %d, want 0", len(gc.LinkedTasks))
		}
		if len(gc.LinkedGoals) != 0 {
			t.Errorf("LinkedGoals = %d, want 0", len(gc.LinkedGoals))
		}
	})

	t.Run("with_relations_returns_linked_items", func(t *testing.T) {
		db := newSQLiteDB(t)
		if err := CreateOrchestrationTables(db); err != nil {
			t.Fatalf("CreateOrchestrationTables: %v", err)
		}
		if err := CreateRelationTables(db); err != nil {
			t.Fatalf("CreateRelationTables: %v", err)
		}
		rs, err := NewRelationStore(db)
		if err != nil {
			t.Fatalf("NewRelationStore: %v", err)
		}

		goalNodeID := insertTestGoal(t, db, "T114", "P0", "M")
		decisionNodeID := insertTestDecision(t, db, "A198")

		if err := rs.UpsertKind(ctx, RelationKindDef{TypeName: "implements", Mode: "asserted"}); err != nil {
			t.Fatalf("UpsertKind: %v", err)
		}
		if err := rs.Assert(ctx, RelationEdge{
			ID:           NewID(),
			SourceType:   "Goal",
			SourceID:     goalNodeID,
			TargetType:   "Decision",
			TargetID:     decisionNodeID,
			RelationKind: "implements",
			EdgeClass:    "asserted",
		}); err != nil {
			t.Fatalf("Assert: %v", err)
		}

		gc, err := QueryGoalContext(ctx, db, rs, "T114")
		if err != nil {
			t.Fatalf("QueryGoalContext: %v", err)
		}
		if gc.Goal == nil || gc.Goal.GoalID != "T114" {
			t.Errorf("Goal.GoalID = %v, want T114", gc.Goal)
		}
		if len(gc.LinkedDecisions) != 1 {
			t.Errorf("LinkedDecisions = %d, want 1", len(gc.LinkedDecisions))
		} else if gc.LinkedDecisions[0].DecisionNumber != "A198" {
			t.Errorf("LinkedDecisions[0].DecisionNumber = %q, want A198", gc.LinkedDecisions[0].DecisionNumber)
		}
		if len(gc.LinkedTasks) != 0 {
			t.Errorf("LinkedTasks = %d, want 0", len(gc.LinkedTasks))
		}
	})

	t.Run("with_linked_task_and_goal", func(t *testing.T) {
		db := newSQLiteDB(t)
		if err := CreateOrchestrationTables(db); err != nil {
			t.Fatalf("CreateOrchestrationTables: %v", err)
		}
		if err := CreateRelationTables(db); err != nil {
			t.Fatalf("CreateRelationTables: %v", err)
		}
		rs, err := NewRelationStore(db)
		if err != nil {
			t.Fatalf("NewRelationStore: %v", err)
		}
		if err := rs.UpsertKind(ctx, RelationKindDef{TypeName: "blocks", Mode: "asserted"}); err != nil {
			t.Fatalf("UpsertKind: %v", err)
		}

		goalNodeID := insertTestGoal(t, db, "T200", "P1", "S")
		taskNodeID := insertTestTask(t, db, "write-tests")
		linkedGoalID := insertTestGoal(t, db, "T201", "P1", "S")

		// Task linked via target→Goal (reverse direction)
		if err := rs.Assert(ctx, RelationEdge{
			ID:           NewID(),
			SourceType:   "Task",
			SourceID:     taskNodeID,
			TargetType:   "Goal",
			TargetID:     goalNodeID,
			RelationKind: "blocks",
			EdgeClass:    "asserted",
		}); err != nil {
			t.Fatalf("Assert task edge: %v", err)
		}
		// Goal linked via source→Goal
		if err := rs.Assert(ctx, RelationEdge{
			ID:           NewID(),
			SourceType:   "Goal",
			SourceID:     goalNodeID,
			TargetType:   "Goal",
			TargetID:     linkedGoalID,
			RelationKind: "blocks",
			EdgeClass:    "asserted",
		}); err != nil {
			t.Fatalf("Assert goal edge: %v", err)
		}

		gc, err := QueryGoalContext(ctx, db, rs, "T200")
		if err != nil {
			t.Fatalf("QueryGoalContext: %v", err)
		}
		if len(gc.LinkedTasks) != 1 {
			t.Errorf("LinkedTasks = %d, want 1", len(gc.LinkedTasks))
		}
		if len(gc.LinkedGoals) != 1 {
			t.Errorf("LinkedGoals = %d, want 1", len(gc.LinkedGoals))
		} else if gc.LinkedGoals[0].GoalID != "T201" {
			t.Errorf("LinkedGoals[0].GoalID = %q, want T201", gc.LinkedGoals[0].GoalID)
		}
	})

	t.Run("getbysource_error_propagated", func(t *testing.T) {
		db := newSQLiteDB(t)
		if err := CreateOrchestrationTables(db); err != nil {
			t.Fatalf("CreateOrchestrationTables: %v", err)
		}
		if err := CreateRelationTables(db); err != nil {
			t.Fatalf("CreateRelationTables: %v", err)
		}
		_ = insertTestGoal(t, db, "T401", "P0", "S")

		// RelationStore backed by a DB that fails on GetBySource queries.
		failRS, err := NewRelationStore(&govQueryFailDB{DB: db, failOn: "source_type"})
		if err != nil {
			t.Fatalf("NewRelationStore: %v", err)
		}
		_, err = QueryGoalContext(ctx, db, failRS, "T401")
		if err == nil {
			t.Error("QueryGoalContext: expected error from GetBySource failure, got nil")
		}
	})

	t.Run("getbytarget_error_propagated", func(t *testing.T) {
		db := newSQLiteDB(t)
		if err := CreateOrchestrationTables(db); err != nil {
			t.Fatalf("CreateOrchestrationTables: %v", err)
		}
		if err := CreateRelationTables(db); err != nil {
			t.Fatalf("CreateRelationTables: %v", err)
		}
		_ = insertTestGoal(t, db, "T402", "P0", "S")

		// RelationStore backed by a DB that fails on GetByTarget queries only.
		failRS, err := NewRelationStore(&govQueryFailDB{DB: db, failOn: "target_type"})
		if err != nil {
			t.Fatalf("NewRelationStore: %v", err)
		}
		_, err = QueryGoalContext(ctx, db, failRS, "T402")
		if err == nil {
			t.Error("QueryGoalContext: expected error from GetByTarget failure, got nil")
		}
	})

	t.Run("deduplication_and_self_link_skipped", func(t *testing.T) {
		db := newSQLiteDB(t)
		if err := CreateOrchestrationTables(db); err != nil {
			t.Fatalf("CreateOrchestrationTables: %v", err)
		}
		if err := CreateRelationTables(db); err != nil {
			t.Fatalf("CreateRelationTables: %v", err)
		}
		rs, err := NewRelationStore(db)
		if err != nil {
			t.Fatalf("NewRelationStore: %v", err)
		}
		if err := rs.UpsertKind(ctx, RelationKindDef{TypeName: "relates-to", Mode: "asserted"}); err != nil {
			t.Fatalf("UpsertKind: %v", err)
		}

		goalNodeID := insertTestGoal(t, db, "T300", "P2", "M")
		decisionNodeID := insertTestDecision(t, db, "A199")

		// Assert one edge Goal→Decision; it appears once in GetBySource.
		// GetByTarget for "Goal" finds nothing for the goal itself.
		edgeID := NewID()
		if err := rs.Assert(ctx, RelationEdge{
			ID:           edgeID,
			SourceType:   "Goal",
			SourceID:     goalNodeID,
			TargetType:   "Decision",
			TargetID:     decisionNodeID,
			RelationKind: "relates-to",
			EdgeClass:    "asserted",
		}); err != nil {
			t.Fatalf("Assert: %v", err)
		}

		gc, err := QueryGoalContext(ctx, db, rs, "T300")
		if err != nil {
			t.Fatalf("QueryGoalContext: %v", err)
		}
		// Edge appears in GetBySource only; deduplication must not double-count.
		if len(gc.LinkedDecisions) != 1 {
			t.Errorf("LinkedDecisions = %d, want 1 (no duplicates)", len(gc.LinkedDecisions))
		}
		// No self-links.
		if len(gc.LinkedGoals) != 0 {
			t.Errorf("LinkedGoals = %d, want 0", len(gc.LinkedGoals))
		}
	})

	t.Run("self_link_covers_dedup_and_skip", func(t *testing.T) {
		// A Goal→Goal self-link appears in both GetBySource and GetByTarget.
		// It must be deduplicated by the seen-edge map, and then the self-link
		// skip (ref.id == goal.ID) must prevent it appearing in LinkedGoals.
		db := newSQLiteDB(t)
		if err := CreateOrchestrationTables(db); err != nil {
			t.Fatalf("CreateOrchestrationTables: %v", err)
		}
		if err := CreateRelationTables(db); err != nil {
			t.Fatalf("CreateRelationTables: %v", err)
		}
		rs, err := NewRelationStore(db)
		if err != nil {
			t.Fatalf("NewRelationStore: %v", err)
		}
		if err := rs.UpsertKind(ctx, RelationKindDef{TypeName: "self", Mode: "asserted"}); err != nil {
			t.Fatalf("UpsertKind: %v", err)
		}
		goalNodeID := insertTestGoal(t, db, "T500", "P3", "S")

		if err := rs.Assert(ctx, RelationEdge{
			ID:           NewID(),
			SourceType:   "Goal",
			SourceID:     goalNodeID,
			TargetType:   "Goal",
			TargetID:     goalNodeID,
			RelationKind: "self",
			EdgeClass:    "asserted",
		}); err != nil {
			t.Fatalf("Assert self-link: %v", err)
		}

		gc, err := QueryGoalContext(ctx, db, rs, "T500")
		if err != nil {
			t.Fatalf("QueryGoalContext: %v", err)
		}
		if len(gc.LinkedGoals) != 0 {
			t.Errorf("LinkedGoals = %d, want 0 (self-link must be skipped)", len(gc.LinkedGoals))
		}
	})

	t.Run("missing_linked_items_skipped_with_warn", func(t *testing.T) {
		// Relations pointing to non-existent Task and Goal IDs cause warn+continue;
		// the context is returned without those items.
		db := newSQLiteDB(t)
		if err := CreateOrchestrationTables(db); err != nil {
			t.Fatalf("CreateOrchestrationTables: %v", err)
		}
		if err := CreateRelationTables(db); err != nil {
			t.Fatalf("CreateRelationTables: %v", err)
		}
		rs, err := NewRelationStore(db)
		if err != nil {
			t.Fatalf("NewRelationStore: %v", err)
		}
		if err := rs.UpsertKind(ctx, RelationKindDef{TypeName: "links", Mode: "asserted"}); err != nil {
			t.Fatalf("UpsertKind: %v", err)
		}
		goalNodeID := insertTestGoal(t, db, "T600", "P1", "M")

		// Edge to a non-existent Task ID.
		if err := rs.Assert(ctx, RelationEdge{
			ID: NewID(), SourceType: "Goal", SourceID: goalNodeID,
			TargetType: "Task", TargetID: "ghost-task-id",
			RelationKind: "links", EdgeClass: "asserted",
		}); err != nil {
			t.Fatalf("Assert task edge: %v", err)
		}
		// Edge to a non-existent Goal ID.
		if err := rs.Assert(ctx, RelationEdge{
			ID: NewID(), SourceType: "Goal", SourceID: goalNodeID,
			TargetType: "Goal", TargetID: "ghost-goal-id",
			RelationKind: "links", EdgeClass: "asserted",
		}); err != nil {
			t.Fatalf("Assert goal edge: %v", err)
		}

		gc, err := QueryGoalContext(ctx, db, rs, "T600")
		if err != nil {
			t.Fatalf("QueryGoalContext: %v", err)
		}
		// Missing items are skipped; slices remain empty.
		if len(gc.LinkedTasks) != 0 {
			t.Errorf("LinkedTasks = %d, want 0", len(gc.LinkedTasks))
		}
		if len(gc.LinkedGoals) != 0 {
			t.Errorf("LinkedGoals = %d, want 0", len(gc.LinkedGoals))
		}
	})
}

// insertTestGoal inserts a minimal Goal row into smeldr_goals and returns its
// node ID.
func insertTestGoal(t *testing.T, db DB, goalID, band, size string) string {
	t.Helper()
	g := &Goal{
		Node:     Node{ID: NewID(), Slug: GenerateSlug("goal-" + goalID), Status: Published},
		GoalID:   goalID,
		Priority: 1,
		Band:     band,
		Size:     size,
	}
	repo := NewSQLRepo[*Goal](db, Table("smeldr_goals"))
	if err := repo.Save(context.Background(), g); err != nil {
		t.Fatalf("insertTestGoal Save: %v", err)
	}
	return g.ID
}

// insertTestDecision inserts a minimal Decision row into smeldr_decisions and
// returns its node ID.
func insertTestDecision(t *testing.T, db DB, decisionNumber string) string {
	t.Helper()
	d := &Decision{
		Node:           Node{ID: NewID(), Slug: GenerateSlug("decision-" + decisionNumber), Status: Published},
		DecisionNumber: decisionNumber,
		Scope:          "core",
	}
	repo := NewSQLRepo[*Decision](db, Table("smeldr_decisions"))
	if err := repo.Save(context.Background(), d); err != nil {
		t.Fatalf("insertTestDecision Save: %v", err)
	}
	return d.ID
}

// insertTestTask inserts a minimal Task row into smeldr_tasks and returns its
// node ID.
func insertTestTask(t *testing.T, db DB, taskID string) string {
	t.Helper()
	tk := &Task{
		Node:   Node{ID: NewID(), Slug: GenerateSlug("task-" + taskID), Status: Published},
		TaskID: taskID,
	}
	repo := NewSQLRepo[*Task](db, Table("smeldr_tasks"))
	if err := repo.Save(context.Background(), tk); err != nil {
		t.Fatalf("insertTestTask Save: %v", err)
	}
	return tk.ID
}

// — authorizeDecisionScope (D34) —————————————————————————————————————————————

func TestAuthorizeDecisionScope_NotDecision(t *testing.T) {
	db := setupGovernanceDB(t)
	rs := NewRoleStore(db)
	tk := &Task{Node: Node{ID: NewID()}, TaskID: "T1"}
	err := authorizeDecisionScope(context.Background(), rs, "tok", tk, map[string]string{"core": "core-ratifier"})
	if err != nil {
		t.Errorf("non-Decision item: want nil, got %v", err)
	}
}

func TestAuthorizeDecisionScope_NilRS(t *testing.T) {
	d := &Decision{Node: Node{ID: NewID()}, Scope: "core"}
	err := authorizeDecisionScope(context.Background(), nil, "tok", d, map[string]string{"core": "core-ratifier"})
	if err != nil {
		t.Errorf("nil RoleStore: want nil, got %v", err)
	}
}

func TestAuthorizeDecisionScope_EmptyActor(t *testing.T) {
	db := setupGovernanceDB(t)
	rs := NewRoleStore(db)
	d := &Decision{Node: Node{ID: NewID()}, Scope: "core"}
	err := authorizeDecisionScope(context.Background(), rs, "", d, map[string]string{"core": "core-ratifier"})
	if err != nil {
		t.Errorf("empty actorID: want nil, got %v", err)
	}
}

func TestAuthorizeDecisionScope_UnmappedScope(t *testing.T) {
	db := setupGovernanceDB(t)
	rs := NewRoleStore(db)
	d := &Decision{Node: Node{ID: NewID()}, Scope: "unmapped-scope"}
	err := authorizeDecisionScope(context.Background(), rs, "tok", d, map[string]string{"core": "core-ratifier"})
	if err != nil {
		t.Errorf("unmapped scope: want nil, got %v", err)
	}
}

func TestAuthorizeDecisionScope_Granted(t *testing.T) {
	db := setupGovernanceDB(t)
	rs := NewRoleStore(db)
	const uid = "tok-core-ratifier"
	if err := rs.DefineRole(context.Background(), RoleDefinition{Name: "core-ratifier", Operations: []string{"approve"}}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	if _, err := rs.Grant(context.Background(), RoleGrant{TokenID: uid, RoleName: "core-ratifier"}); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	d := &Decision{Node: Node{ID: NewID()}, Scope: "core"}
	err := authorizeDecisionScope(context.Background(), rs, uid, d, map[string]string{"core": "core-ratifier"})
	if err != nil {
		t.Errorf("granted role: want nil, got %v", err)
	}
}

func TestAuthorizeDecisionScope_NotGranted(t *testing.T) {
	db := setupGovernanceDB(t)
	rs := NewRoleStore(db)
	const uid = "tok-no-grant"
	if err := rs.DefineRole(context.Background(), RoleDefinition{Name: "core-ratifier", Operations: []string{"approve"}}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	d := &Decision{Node: Node{ID: NewID()}, Scope: "core"}
	err := authorizeDecisionScope(context.Background(), rs, uid, d, map[string]string{"core": "core-ratifier"})
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("ungranted role: want ErrForbidden, got %v", err)
	}
}

func TestAuthorizeDecisionScope_GrantCheckError(t *testing.T) {
	db := setupGovernanceDB(t)
	wrapped := &govQueryFailDB{DB: db, failOn: "FROM smeldr_role_grants g"}
	rs := NewRoleStore(wrapped)
	d := &Decision{Node: Node{ID: NewID()}, Scope: "core"}
	err := authorizeDecisionScope(context.Background(), rs, "tok", d, map[string]string{"core": "core-ratifier"})
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("grant check error: want ErrForbidden (fail-closed), got %v", err)
	}
}

// — RegisterOrchestrationRelationKinds ———————————————————————————————————————

func TestRegisterOrchestrationRelationKinds_RoundTrip(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()

	if err := RegisterOrchestrationRelationKinds(ctx, store); err != nil {
		t.Fatalf("RegisterOrchestrationRelationKinds: %v", err)
	}

	want := map[string]struct {
		label        string
		reverseLabel string
		typePairs    string
	}{
		"derives_from": {"Derives From", "", `[{"source_type":"Task","target_type":"Goal"}]`},
		"depends_on":   {"Depends On", "", `[{"source_type":"Task","target_type":"Task"}]`},
		"ships_as":     {"Ships As", "", `[{"source_type":"Task","target_type":"Amendment"}]`},
		"supersedes":   {"Supersedes", "Superseded By", `[{"source_type":"Decision","target_type":"Decision"}]`},
		"contains": {"Contains", "Part Of", `[{"source_type":"Goal","target_type":"Goal"},` +
			`{"source_type":"Goal","target_type":"Task"},` +
			`{"source_type":"Goal","target_type":"Decision"},` +
			`{"source_type":"Goal","target_type":"Amendment"},` +
			`{"source_type":"Goal","target_type":"Signal"}]`},
	}

	kinds := store.ListKinds()
	if len(kinds) != len(want) {
		t.Fatalf("ListKinds: got %d kinds, want %d: %+v", len(kinds), len(want), kinds)
	}
	for _, k := range kinds {
		w, ok := want[k.TypeName]
		if !ok {
			t.Errorf("unexpected kind registered: %q", k.TypeName)
			continue
		}
		if k.Label != w.label {
			t.Errorf("%s: Label = %q, want %q", k.TypeName, k.Label, w.label)
		}
		if k.ReverseLabel != w.reverseLabel {
			t.Errorf("%s: ReverseLabel = %q, want %q", k.TypeName, k.ReverseLabel, w.reverseLabel)
		}
		if k.Mode != "asserted" {
			t.Errorf("%s: Mode = %q, want %q", k.TypeName, k.Mode, "asserted")
		}
		if !k.Directional {
			t.Errorf("%s: Directional = false, want true", k.TypeName)
		}
		if k.Weighted {
			t.Errorf("%s: Weighted = true, want false", k.TypeName)
		}
		if string(k.TypePairs) != w.typePairs {
			t.Errorf("%s: TypePairs = %s, want %s", k.TypeName, k.TypePairs, w.typePairs)
		}
	}

	// Idempotent — a second call must not error or duplicate kinds.
	if err := RegisterOrchestrationRelationKinds(ctx, store); err != nil {
		t.Fatalf("second RegisterOrchestrationRelationKinds call: %v", err)
	}
	if got := len(store.ListKinds()); got != len(want) {
		t.Errorf("after second call: got %d kinds, want %d", got, len(want))
	}
}

func TestRegisterOrchestrationRelationKinds_UpsertError(t *testing.T) {
	store := mockRelationStore(&errExecDB{})
	err := RegisterOrchestrationRelationKinds(context.Background(), store)
	if err == nil {
		t.Fatal("want error when UpsertKind fails, got nil")
	}
	if !strings.Contains(err.Error(), `"derives_from"`) {
		t.Errorf("error = %q, want it to name the failing kind %q", err.Error(), "derives_from")
	}
}

// TestRun_SaveRevConflict proves Run is genuinely wired onto SQLRepo's real
// rev-CAS (D38 §1/§3) — not a test of the future listener's own MCP-update-
// payload rev-echo discipline (D38 §3), which stays a separate, still-open
// concern this task does not touch (see Run's own doc comment). The
// SQLRepo-level echo this test's own stale/fresh-copy split now exercises
// is a different, lower layer (A253): whether Save tells its immediate Go
// caller the truth at all. Mirrors TestSQLRepo_Save_RevConflict's pattern.
func TestRun_SaveRevConflict(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	repo := NewSQLRepo[*Run](db, Table("smeldr_runs"))
	ctx := context.Background()

	item := &Run{Node: Node{ID: "run-1", Slug: "run-1"}, TaskID: "T145"}
	// First insert: stored rev = 0.
	if err := repo.Save(ctx, item); err != nil {
		t.Fatalf("first save: %v", err)
	}
	// A second holder reads the item at rev 0 and never advances its own
	// copy — this manufactures a genuinely stale write, independent of
	// what item.Rev becomes after the next Save.
	stale := &Run{Node: Node{ID: "run-1", Slug: "run-1", Rev: item.Rev}, TaskID: "T145"}

	// Second save with item.Rev = 0: WHERE rev=0 matches stored rev=0 → update, stored rev→1.
	if err := repo.Save(ctx, item); err != nil {
		t.Fatalf("second save: %v", err)
	}
	if item.Rev != 1 {
		t.Fatalf("item.Rev after second save = %d, want 1 (write-back)", item.Rev)
	}

	// stale.Rev is still 0: WHERE rev=0 fails (stored rev=1) → ErrRevConflict.
	if err := repo.Save(ctx, stale); !errors.Is(err, ErrRevConflict) {
		t.Errorf("stale save: got %v, want ErrRevConflict", err)
	}
}
