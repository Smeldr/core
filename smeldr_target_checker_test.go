package smeldr

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// setupTargetCheckerDB creates a DB with relation tables, orchestration
// tables, and a "depends_on" kind registered for Task→Decision — enough to
// assert a real edge from a compiled Task to a compiled Decision and drive
// it through App.SweepStructural's real default checker.
func setupTargetCheckerDB(t *testing.T) (*sql.DB, *RelationStore, *App) {
	t.Helper()
	db := newSQLiteDB(t)
	if err := CreateRelationTables(db); err != nil {
		t.Fatalf("CreateRelationTables: %v", err)
	}
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	rs, err := NewRelationStore(db)
	if err != nil {
		t.Fatalf("NewRelationStore: %v", err)
	}
	upsertTestKind(t, rs, "depends_on", "Task", "Decision")

	app := &App{cfg: Config{DB: db}}
	app.Relations(rs)
	return db, rs, app
}

// seedDecision inserts a Decision row directly at the given status.
func seedDecision(t *testing.T, db DB, id, status string) {
	t.Helper()
	repo := NewSQLRepo[*Decision](db, Table("smeldr_decisions"))
	d := &Decision{
		Node:           Node{ID: id, Slug: id, Status: Status(status)},
		DecisionNumber: "D-checker-test",
		Scope:          "core",
	}
	if err := repo.Save(context.Background(), d); err != nil {
		t.Fatalf("seed Decision %s: %v", id, err)
	}
}

// seedTask inserts a Task row and returns its assertable edge source.
func seedTask(t *testing.T, db DB, id string) {
	t.Helper()
	repo := NewSQLRepo[*Task](db, Table("smeldr_tasks"))
	task := &Task{Node: Node{ID: id, Slug: id, Status: "backlog"}}
	if err := repo.Save(context.Background(), task); err != nil {
		t.Fatalf("seed Task %s: %v", id, err)
	}
}

func assertTaskDependsOnDecision(t *testing.T, rs *RelationStore, taskID, decisionID string) RelationEdge {
	t.Helper()
	edge, err := rs.MCPAssertRelation(context.Background(),
		"Task", taskID, "Decision", decisionID, "depends_on", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("MCPAssertRelation Task/%s -> Decision/%s: %v", taskID, decisionID, err)
	}
	return edge
}

// edgeSurvives asserts the edge from taskID is still present and not
// invalidated after a sweep.
func edgeSurvives(t *testing.T, rs *RelationStore, taskID string) {
	t.Helper()
	// "" matches any relation kind — this helper is reused across tests
	// that register different kinds ("depends_on", "linked").
	edges, err := rs.GetBySource(context.Background(), "Task", taskID, "")
	if err != nil {
		t.Fatalf("GetBySource: %v", err)
	}
	if len(edges) != 1 || edges[0].InvalidAt != nil {
		t.Errorf("expected the edge from Task/%s to survive, got %+v", taskID, edges)
	}
}

// — defaultTargetChecker via App.SweepStructural — never called directly,
// per the architect's own check criterion: a checker correct in isolation
// but never reached by the real entry point is the same defect class as
// the one this fixes. ————————————————————————————————————————————————————

func TestAppSweepStructural_CompiledTarget_Alive(t *testing.T) {
	_, rs, app := setupTargetCheckerDB(t)
	seedTask(t, app.cfg.DB, "task-1")
	seedDecision(t, app.cfg.DB, "decision-1", "proposed")
	assertTaskDependsOnDecision(t, rs, "task-1", "decision-1")

	f, sk, err := app.SweepStructural(context.Background())
	if err != nil {
		t.Fatalf("SweepStructural: %v", err)
	}
	if f != 0 || sk != 0 {
		t.Errorf("want (0,0) for a live compiled target, got (%d,%d)", f, sk)
	}
	edgeSurvives(t, rs, "task-1")
}

func TestAppSweepStructural_CompiledTarget_Deleted(t *testing.T) {
	db, rs, app := setupTargetCheckerDB(t)
	seedTask(t, db, "task-1")
	seedDecision(t, db, "decision-1", "proposed")
	assertTaskDependsOnDecision(t, rs, "task-1", "decision-1")

	if _, err := db.ExecContext(context.Background(),
		"DELETE FROM smeldr_decisions WHERE id=$1", "decision-1"); err != nil {
		t.Fatalf("delete Decision: %v", err)
	}

	f, sk, err := app.SweepStructural(context.Background())
	if err != nil {
		t.Fatalf("SweepStructural: %v", err)
	}
	if f != 1 || sk != 0 {
		t.Errorf("want (1,0) for a hard-deleted compiled target, got (%d,%d)", f, sk)
	}
	edges, err := rs.GetBySource(context.Background(), "Task", "task-1", "depends_on")
	if err != nil {
		t.Fatalf("GetBySource: %v", err)
	}
	if len(edges) != 1 || edges[0].InvalidAt == nil {
		t.Errorf("expected the edge to be invalidated, got %+v", edges)
	}
}

// TestAppSweepStructural_SupersededDecisionSurvives is the case the
// architect named explicitly as the one they would want to see fail
// first if the definition of "alive" were wrong: a superseded Decision
// still exists and its lineage must not be invalidated by reaching a
// terminal status.
func TestAppSweepStructural_SupersededDecisionSurvives(t *testing.T) {
	db, rs, app := setupTargetCheckerDB(t)
	seedTask(t, db, "task-1")
	seedDecision(t, db, "decision-1", "superseded")
	assertTaskDependsOnDecision(t, rs, "task-1", "decision-1")

	f, sk, err := app.SweepStructural(context.Background())
	if err != nil {
		t.Fatalf("SweepStructural: %v", err)
	}
	if f != 0 || sk != 0 {
		t.Errorf("want (0,0) for a superseded-but-existing Decision, got (%d,%d)", f, sk)
	}
	edgeSurvives(t, rs, "task-1")
}

// TestAppSweepStructural_ArchivedDecisionSurvives applies the identical
// rule to "archived" — argued in the plan as no different from
// "superseded": archiving is bookkeeping, not retraction.
func TestAppSweepStructural_ArchivedDecisionSurvives(t *testing.T) {
	db, rs, app := setupTargetCheckerDB(t)
	seedTask(t, db, "task-1")
	seedDecision(t, db, "decision-1", "archived")
	assertTaskDependsOnDecision(t, rs, "task-1", "decision-1")

	f, sk, err := app.SweepStructural(context.Background())
	if err != nil {
		t.Fatalf("SweepStructural: %v", err)
	}
	if f != 0 || sk != 0 {
		t.Errorf("want (0,0) for an archived-but-existing Decision, got (%d,%d)", f, sk)
	}
	edgeSurvives(t, rs, "task-1")
}

// targetCheckerQueryFailDB makes the compiled-table existence query fail
// while leaving every other query untouched.
type targetCheckerQueryFailDB struct {
	DB
	failOn string
}

func (d *targetCheckerQueryFailDB) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	if strings.Contains(q, d.failOn) {
		sdb, _ := sql.Open("sqlite", ":memory:")
		return sdb.QueryRowContext(ctx, "SELECT 1 FROM no_table_xyz")
	}
	return d.DB.QueryRowContext(ctx, q, args...)
}

func TestAppSweepStructural_CompiledTarget_QueryError(t *testing.T) {
	db, rs, app := setupTargetCheckerDB(t)
	seedTask(t, db, "task-1")
	seedDecision(t, db, "decision-1", "proposed")
	assertTaskDependsOnDecision(t, rs, "task-1", "decision-1")

	app.cfg.DB = &targetCheckerQueryFailDB{DB: db, failOn: "smeldr_decisions"}

	f, sk, err := app.SweepStructural(context.Background())
	if err != nil {
		t.Fatalf("SweepStructural: %v", err)
	}
	if f != 0 || sk != 1 {
		t.Errorf("want (0,1) when the compiled-table query fails, got (%d,%d)", f, sk)
	}
	edgeSurvives(t, rs, "task-1")
}

// TestAppSweepStructural_UnregisteredType_NoTable_Errors is the case the
// architect required: resolveItemTable's own fallback to
// smeldr_dynamic_content is reachable a second, unintended way — a
// compiled type whose own table is simply absent (partial migration, an
// unregistered module) resolves to the same fallback a genuine
// dynamic-content type does. Confirmed here that an unrecognized type
// (no compiled table, not present in smeldr_content_type_schemas) is
// reported as an error and the edge survives — a sweep that cannot tell
// must decline, never delete.
func TestAppSweepStructural_UnregisteredType_NoTable_Errors(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateRelationTables(db); err != nil {
		t.Fatalf("CreateRelationTables: %v", err)
	}
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	if err := CreateSchemaTable(db); err != nil {
		t.Fatalf("CreateSchemaTable: %v", err)
	}
	if err := CreateBlockTables(db); err != nil {
		t.Fatalf("CreateBlockTables: %v", err)
	}
	rs, err := NewRelationStore(db)
	if err != nil {
		t.Fatalf("NewRelationStore: %v", err)
	}
	upsertTestKind(t, rs, "depends_on", "Task", "PhantomType")

	app := &App{cfg: Config{DB: db}}
	app.Relations(rs)

	// A dynamic content row that happens to share the target's ID, but
	// under a real, different, registered type — proves the checker
	// isn't just trusting "a row with this ID exists somewhere".
	if err := NewSchemaStore(db).Save(context.Background(), &ContentTypeSchema{
		TypeName: "article", Kind: "content",
	}); err != nil {
		t.Fatalf("SchemaStore.Save: %v", err)
	}
	repo := &DynamicTypeRepo{db: db, typeName: "article"}
	node, err := repo.CreateDraft(context.Background(), map[string]any{"title": "decoy"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if _, err := db.ExecContext(context.Background(),
		"UPDATE smeldr_dynamic_content SET id=$1 WHERE id=$2", "phantom-1", node.ID); err != nil {
		t.Fatalf("UPDATE id: %v", err)
	}

	// Seed a real Task so the edge's source exists; "PhantomType" itself
	// has no table anywhere and is not registered in
	// smeldr_content_type_schemas.
	taskRepo := NewSQLRepo[*Task](db, Table("smeldr_tasks"))
	if err := taskRepo.Save(context.Background(), &Task{Node: Node{ID: "task-1", Slug: "task-1", Status: "backlog"}}); err != nil {
		t.Fatalf("seed Task: %v", err)
	}
	if _, err := rs.MCPAssertRelation(context.Background(),
		"Task", "task-1", "PhantomType", "phantom-1", "depends_on", nil, nil, nil, nil); err != nil {
		t.Fatalf("MCPAssertRelation: %v", err)
	}

	f, sk, err := app.SweepStructural(context.Background())
	if err != nil {
		t.Fatalf("SweepStructural: %v", err)
	}
	if f != 0 || sk != 1 {
		t.Errorf("want (0,1) for an unrecognized type with no table, got (%d,%d)", f, sk)
	}
	edgeSurvives(t, rs, "task-1")
}

// TestAppSweepStructural_SchemaRegistryTableMissing_Errors covers the
// other way the registry lookup itself can fail: smeldr_content_type_schemas
// doesn't exist at all, not just an absent row in it. App.Relations always
// creates that table as a side effect (CreateSchemaTable), so this wires
// App.relationStore directly — package-internal test, unexported field is
// reachable — to reach a state App.Relations itself would never produce,
// the way a hand-rolled RelationStore.SweepStructural caller integrating
// against this package without calling App.Relations could. Must also
// error, not silently treat "can't check" as "not alive".
func TestAppSweepStructural_SchemaRegistryTableMissing_Errors(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateRelationTables(db); err != nil {
		t.Fatalf("CreateRelationTables: %v", err)
	}
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	rs, err := NewRelationStore(db)
	if err != nil {
		t.Fatalf("NewRelationStore: %v", err)
	}
	upsertTestKind(t, rs, "linked", "Task", "GhostType")

	// App.relationStore set directly, bypassing App.Relations() and the
	// CreateSchemaTable side effect it performs — smeldr_content_type_schemas
	// genuinely does not exist in this DB.
	app := &App{cfg: Config{DB: db}, relationStore: rs}

	seedTask(t, db, "task-1")
	if _, err := rs.MCPAssertRelation(context.Background(),
		"Task", "task-1", "GhostType", "ghost-1", "linked", nil, nil, nil, nil); err != nil {
		t.Fatalf("MCPAssertRelation: %v", err)
	}

	f, sk, err := app.SweepStructural(context.Background())
	if err != nil {
		t.Fatalf("SweepStructural: %v", err)
	}
	if f != 0 || sk != 1 {
		t.Errorf("want (0,1) when the schema registry table itself is missing, got (%d,%d)", f, sk)
	}
	edgeSurvives(t, rs, "task-1")
}
