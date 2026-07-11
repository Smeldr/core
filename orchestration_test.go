package smeldr

import (
	"context"
	"testing"
)

// TestOrchestrationTypes_embedNode verifies at compile time that all five
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
// all five tables without error and that they are queryable.
func TestCreateOrchestrationTables(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	ctx := context.Background()
	for _, table := range []string{
		"smeldr_signals", "smeldr_tasks", "smeldr_decisions", "smeldr_amendments", "smeldr_goals",
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

	// Verify the 5 orchestration flows were inserted (exclude the default seed flow).
	row := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM smeldr_state_flows WHERE type_name IS NOT NULL")
	var n int
	if err := row.Scan(&n); err != nil {
		t.Fatalf("count state flows: %v", err)
	}
	if n != 5 {
		t.Errorf("registered flow count = %d, want 5", n)
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
	wantStates := []string{"open", "in-progress", "done", "parked"}
	if got := stateNames(f); got != join(wantStates) {
		t.Errorf("states = %s, want %s", got, join(wantStates))
	}
	if got := initialState(f); got != "open" {
		t.Errorf("initial = %q, want %q", got, "open")
	}
	wantTerminals := []string{"done", "parked"}
	if got := terminalStates(f); got != join(wantTerminals) {
		t.Errorf("terminals = %s, want %s", got, join(wantTerminals))
	}
	if len(f.Transitions) != 5 {
		t.Errorf("transition count = %d, want 5", len(f.Transitions))
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
