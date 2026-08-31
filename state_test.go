package smeldr

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// agentJobFlow is a reusable test fixture mirroring the AgentJob custom flow
// from custom-state-flows.md section 6.1.
var agentJobFlow = StateFlow{
	Name:     "agent-job",
	TypeName: "AgentJob",
	States: []State{
		{Name: "draft", IsInitial: true},
		{Name: "published"},
		{Name: "paused", SuppressesSignals: true},
		{Name: "archived", IsTerminal: true},
	},
	Transitions: []Transition{
		{From: "draft", To: "published"},
		{From: "published", To: "paused"},
		{From: "paused", To: "published"},
		{From: "published", To: "archived"},
		{From: "paused", To: "archived"},
	},
}

func TestRegisterFlow_happyPath(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}

	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(agentJobFlow); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}

	// Flow row exists with correct type_name.
	var flowID string
	var typeName string
	if err := db.QueryRowContext(ctx,
		`SELECT id, type_name FROM smeldr_state_flows WHERE name = 'agent-job'`,
	).Scan(&flowID, &typeName); err != nil {
		t.Fatalf("flow row not found: %v", err)
	}
	if typeName != "AgentJob" {
		t.Errorf("type_name = %q, want %q", typeName, "AgentJob")
	}

	// Correct number of states.
	var stateCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_states WHERE flow_id = ?`, flowID,
	).Scan(&stateCount); err != nil {
		t.Fatalf("count states: %v", err)
	}
	if stateCount != 4 {
		t.Errorf("state count = %d, want 4", stateCount)
	}

	// Correct number of transitions.
	var transCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_transitions WHERE flow_id = ?`, flowID,
	).Scan(&transCount); err != nil {
		t.Fatalf("count transitions: %v", err)
	}
	if transCount != 5 {
		t.Errorf("transition count = %d, want 5", transCount)
	}

	// paused state has suppresses_signals = true.
	var suppresses bool
	if err := db.QueryRowContext(ctx,
		`SELECT suppresses_signals FROM smeldr_states WHERE flow_id = ? AND name = 'paused'`, flowID,
	).Scan(&suppresses); err != nil {
		t.Fatalf("paused state not found: %v", err)
	}
	if !suppresses {
		t.Error("paused state: suppresses_signals should be true")
	}
}

func TestRegisterFlow_idempotent(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}

	app := &App{cfg: Config{DB: db}}
	for range 2 {
		if err := app.RegisterFlow(agentJobFlow); err != nil {
			t.Fatalf("RegisterFlow: %v", err)
		}
	}

	var flowID string
	if err := db.QueryRowContext(ctx,
		`SELECT id FROM smeldr_state_flows WHERE name = 'agent-job'`,
	).Scan(&flowID); err != nil {
		t.Fatalf("flow row not found: %v", err)
	}

	var stateCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_states WHERE flow_id = ?`, flowID,
	).Scan(&stateCount); err != nil {
		t.Fatalf("count states: %v", err)
	}
	if stateCount != 4 {
		t.Errorf("state count after 2 registrations = %d, want 4 (no duplicates)", stateCount)
	}

	var transCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_transitions WHERE flow_id = ?`, flowID,
	).Scan(&transCount); err != nil {
		t.Fatalf("count transitions: %v", err)
	}
	if transCount != 5 {
		t.Errorf("transition count after 2 registrations = %d, want 5 (no duplicates)", transCount)
	}
}

// TestRegisterFlow_rename_updatesInPlace is the direct regression pin for
// T268: a rename (same TypeName, new Name) must update the existing row in
// place, not orphan it. Before the fix, this exact shape — RegisterFlow
// called twice with the same TypeName but a different Name — produced two
// rows for the same type, and resolveFlowID's own unordered query could
// pick either one; that's precisely what happened live (architect-task ->
// agent-task, T231).
func TestRegisterFlow_rename_updatesInPlace(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}
	app := &App{cfg: Config{DB: db}}

	original := StateFlow{
		Name:        "old-name",
		TypeName:    "RenameTestType",
		Description: "original description",
		States:      []State{{Name: "draft", IsInitial: true}, {Name: "done", IsTerminal: true}},
		Transitions: []Transition{{From: "draft", To: "done"}},
	}
	if err := app.RegisterFlow(original); err != nil {
		t.Fatalf("RegisterFlow (original): %v", err)
	}

	renamed := StateFlow{
		Name:        "new-name",
		TypeName:    "RenameTestType", // same type, different name — the rename shape
		Description: "updated description",
		States: []State{
			{Name: "draft", IsInitial: true},
			{Name: "done", IsTerminal: true},
			{Name: "resolved", IsTerminal: true},
		},
		Transitions: []Transition{
			{From: "draft", To: "done"},
			{From: "draft", To: "resolved"},
		},
	}
	if err := app.RegisterFlow(renamed); err != nil {
		t.Fatalf("RegisterFlow (renamed): %v", err)
	}

	var flowCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_state_flows WHERE type_name = 'RenameTestType'`,
	).Scan(&flowCount); err != nil {
		t.Fatalf("count flows: %v", err)
	}
	if flowCount != 1 {
		t.Fatalf("flow count for type after rename = %d, want 1 (no orphaned row)", flowCount)
	}

	flowID, found, err := resolveFlowID(ctx, db, "RenameTestType")
	if err != nil || !found {
		t.Fatalf("resolveFlowID: found=%v err=%v", found, err)
	}
	var name, description string
	if err := db.QueryRowContext(ctx,
		`SELECT name, description FROM smeldr_state_flows WHERE id = $1`, flowID,
	).Scan(&name, &description); err != nil {
		t.Fatalf("select name/description: %v", err)
	}
	if name != "new-name" {
		t.Errorf("resolved flow's name = %q, want %q", name, "new-name")
	}
	if description != "updated description" {
		t.Errorf("description = %q, want %q (must update on re-registration too)", description, "updated description")
	}

	// The old row's own states must be gone too — not silently orphaned
	// rows still occupying the table under a different, unreachable id.
	var stateCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_states WHERE flow_id = $1`, flowID,
	).Scan(&stateCount); err != nil {
		t.Fatalf("count states: %v", err)
	}
	if stateCount != 3 {
		t.Errorf("state count after rename = %d, want 3 (draft/done/resolved)", stateCount)
	}
}

func TestRegisterFlow_unknownStateError(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}

	// Create the table that RegisterFlow will validate against.
	// TypeName "TestItem" → table "test_items".
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE test_items (id TEXT NOT NULL PRIMARY KEY, status TEXT NOT NULL)`,
	); err != nil {
		t.Fatalf("create test_items: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO test_items VALUES ('1', 'unknown-state')`,
	); err != nil {
		t.Fatalf("insert item: %v", err)
	}

	flow := StateFlow{
		Name:     "test-flow",
		TypeName: "TestItem",
		States: []State{
			{Name: "draft", IsInitial: true},
			{Name: "archived", IsTerminal: true},
		},
		Transitions: []Transition{
			{From: "draft", To: "archived"},
		},
	}

	app := &App{cfg: Config{DB: db}}
	err := app.RegisterFlow(flow)
	if err == nil {
		t.Fatal("RegisterFlow should return error for item in unknown state")
	}
	if !strings.Contains(err.Error(), "unknown-state") {
		t.Errorf("error %q does not mention the unknown state", err.Error())
	}
}

func TestRegisterFlow_nilDB(t *testing.T) {
	app := &App{cfg: Config{}}
	err := app.RegisterFlow(agentJobFlow)
	if err == nil {
		t.Fatal("RegisterFlow should return error when DB is nil")
	}
}

func TestRegisterFlow_emptyName(t *testing.T) {
	db := newSQLiteDB(t)
	app := &App{cfg: Config{DB: db}}
	err := app.RegisterFlow(StateFlow{TypeName: "AgentJob"})
	if err == nil {
		t.Fatal("RegisterFlow should return error when Name is empty")
	}
}

func TestRegisterFlow_emptyTypeName(t *testing.T) {
	db := newSQLiteDB(t)
	app := &App{cfg: Config{DB: db}}
	err := app.RegisterFlow(StateFlow{Name: "agent-job"})
	if err == nil {
		t.Fatal("RegisterFlow should return error when TypeName is empty")
	}
}

func TestRegisterFlow_withRequiredRole(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}

	flow := StateFlow{
		Name:     "governed-flow",
		TypeName: "GovernedItem",
		States: []State{
			{Name: "draft", IsInitial: true},
			{Name: "published"},
		},
		Transitions: []Transition{
			{From: "draft", To: "published", RequiredRole: "Editor"},
		},
	}

	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(flow); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}

	// Verify required_role was stored as non-NULL.
	var flowID string
	if err := db.QueryRowContext(ctx,
		`SELECT id FROM smeldr_state_flows WHERE name = 'governed-flow'`,
	).Scan(&flowID); err != nil {
		t.Fatalf("flow not found: %v", err)
	}
	var role *string
	if err := db.QueryRowContext(ctx,
		`SELECT required_role FROM smeldr_transitions WHERE flow_id = ? AND from_state = 'draft'`, flowID,
	).Scan(&role); err != nil {
		t.Fatalf("transition not found: %v", err)
	}
	if role == nil || *role != "Editor" {
		t.Errorf("required_role = %v, want %q", role, "Editor")
	}
}

func TestRegisterFlow_validateItemsInValidStates(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}

	// TypeName "ValidItem" → table "valid_items"; all items are in known states.
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE valid_items (id TEXT NOT NULL PRIMARY KEY, status TEXT NOT NULL)`,
	); err != nil {
		t.Fatalf("create valid_items: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO valid_items VALUES ('1', 'draft')`,
	); err != nil {
		t.Fatalf("insert item: %v", err)
	}

	flow := StateFlow{
		Name:     "valid-flow",
		TypeName: "ValidItem",
		States: []State{
			{Name: "draft", IsInitial: true},
			{Name: "archived", IsTerminal: true},
		},
		Transitions: []Transition{
			{From: "draft", To: "archived"},
		},
	}

	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(flow); err != nil {
		t.Fatalf("RegisterFlow: all items in valid states, should not error: %v", err)
	}
}

func TestRegisterFlow_insertFlowError(t *testing.T) {
	// failOnNthExecDB{failAt: 1} makes the first ExecContext call (INSERT OR IGNORE
	// into smeldr_state_flows) fail immediately.
	app := &App{cfg: Config{DB: &failOnNthExecDB{failAt: 1}}}
	err := app.RegisterFlow(agentJobFlow)
	if err == nil {
		t.Fatal("RegisterFlow should return error when flow INSERT fails")
	}
}

func TestRegisterFlow_readFlowIDError(t *testing.T) {
	// queryFailDB: ExecContext returns (nil, nil) so INSERT OR IGNORE succeeds,
	// but QueryRowContext always fails → flow ID SELECT returns scan error → line 149-151.
	app := &App{cfg: Config{DB: &queryFailDB{}}}
	err := app.RegisterFlow(StateFlow{
		Name:     "test-flow",
		TypeName: "TestType",
		States:   []State{{Name: "draft", IsInitial: true}},
	})
	if err == nil {
		t.Fatal("expected error on flow ID read failure")
	}
}

func TestRegisterFlow_updateConflictPolicyError(t *testing.T) {
	// failAt=2: INSERT flow (call 1) succeeds, UPDATE conflict_policy (call 2) fails.
	app := &App{cfg: Config{DB: &flowIDDB{failAt: 2}}}
	err := app.RegisterFlow(agentJobFlow)
	if err == nil {
		t.Fatal("RegisterFlow should return error when conflict policy UPDATE fails")
	}
}

func TestRegisterFlow_insertStateError(t *testing.T) {
	// flowIDDB returns flowID=42 for QueryRowContext and fails ExecContext on call N.
	// failAt=3: INSERT flow (1), UPDATE conflict_policy (2) succeed, INSERT first state (3) fails.
	app := &App{cfg: Config{DB: &flowIDDB{failAt: 3}}}
	err := app.RegisterFlow(agentJobFlow)
	if err == nil {
		t.Fatal("RegisterFlow should return error when state INSERT fails")
	}
}

func TestRegisterFlow_insertTransitionError(t *testing.T) {
	// failAt=7: INSERT flow (1), UPDATE (2), 4 state INSERTs (3-6), first transition INSERT (7) fails.
	app := &App{cfg: Config{DB: &flowIDDB{failAt: 7}}}
	err := app.RegisterFlow(agentJobFlow)
	if err == nil {
		t.Fatal("RegisterFlow should return error when transition INSERT fails")
	}
}

func TestRegisterFlow_validateQueryError(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}

	// Create a table for TypeName "NoStatusItem" → "no_status_items"
	// but without a status column — the SELECT DISTINCT status query will fail.
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE no_status_items (id TEXT NOT NULL PRIMARY KEY)`,
	); err != nil {
		t.Fatalf("create no_status_items: %v", err)
	}
	// Insert a row so the table is non-empty (triggers the query path).
	if _, err := db.ExecContext(ctx,
		`INSERT INTO no_status_items VALUES ('1')`,
	); err != nil {
		t.Fatalf("insert item: %v", err)
	}

	flow := StateFlow{
		Name:     "no-status-flow",
		TypeName: "NoStatusItem",
		States:   []State{{Name: "draft", IsInitial: true}},
		Transitions: []Transition{
			{From: "draft", To: "draft"},
		},
	}

	app := &App{cfg: Config{DB: db}}
	err := app.RegisterFlow(flow)
	if err == nil {
		t.Fatal("RegisterFlow should return error when status column is missing")
	}
}

// flowIDDB succeeds ExecContext calls 1..(failAt-1), fails on call failAt,
// and returns flowID=42 for every QueryRowContext call.
// Used to test RegisterFlow error paths past the initial flow INSERT.
type flowIDDB struct {
	failAt int
	count  int
}

func (d *flowIDDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	d.count++
	if d.count == d.failAt {
		return nil, errors.New("exec error on call " + strconv.Itoa(d.count))
	}
	return nil, nil
}

func (d *flowIDDB) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, nil
}

func (d *flowIDDB) QueryRowContext(ctx context.Context, _ string, _ ...any) *sql.Row {
	conn := &guardRowConn{val: int64(42)}
	return sql.OpenDB(conn).QueryRowContext(ctx, "SELECT v")
}

// — validateTransition tests ——————————————————————————————————————————————————

func TestValidateTransition_nilDB(t *testing.T) {
	ctx := context.Background()
	if err := validateTransition(ctx, nil, nil, "", "Post", "draft", "published", ""); err != nil {
		t.Errorf("nil db: want nil, got %v", err)
	}
}

func TestValidateTransition_nonSQLite(t *testing.T) {
	// failOnNthExecDB.QueryRowContext returns guardRowConn{noRow:true} → scan fails
	// → sqlite_master probe returns error → validateTransition returns nil.
	ctx := context.Background()
	db := &failOnNthExecDB{failAt: 999} // exec never fails; query always returns no-row
	if err := validateTransition(ctx, db, nil, "", "Post", "draft", "published", ""); err != nil {
		t.Errorf("non-SQLite: want nil, got %v", err)
	}
}

func TestValidateTransition_identity(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}
	// Same from/to status is always allowed regardless of any registered flow.
	if err := validateTransition(ctx, db, nil, "", "Post", "published", "published", ""); err != nil {
		t.Errorf("identity transition: want nil, got %v", err)
	}
}

func TestValidateTransition_customFlow_valid(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(agentJobFlow); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	// draft→published is in agentJobFlow.
	if err := validateTransition(ctx, db, nil, "", "AgentJob", "draft", "published", ""); err != nil {
		t.Errorf("valid custom-flow transition: want nil, got %v", err)
	}
}

func TestValidateTransition_customFlow_invalid(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(agentJobFlow); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	// draft→archived is NOT in agentJobFlow.
	err := validateTransition(ctx, db, nil, "", "AgentJob", "draft", "archived", "")
	if !errors.Is(err, ErrConflict) {
		t.Errorf("invalid custom-flow transition: want ErrConflict, got %v", err)
	}
}

func TestValidateTransition_defaultFlow_valid(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}
	// No custom flow registered for "GenericPost" → falls back to default flow.
	// Default flow includes draft→published.
	if err := validateTransition(ctx, db, nil, "", "GenericPost", "draft", "published", ""); err != nil {
		t.Errorf("valid default-flow transition: want nil, got %v", err)
	}
}

func TestValidateTransition_defaultFlow_invalid(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}
	// No custom flow → falls back to default. archived→draft is not in default flow.
	err := validateTransition(ctx, db, nil, "", "GenericPost", "archived", "draft", "")
	if !errors.Is(err, ErrConflict) {
		t.Errorf("invalid default-flow transition: want ErrConflict, got %v", err)
	}
}

// — RequiredReason tests (T149) ————————————————————————————————————————————

func setupReasonFlowDB(t *testing.T) *sql.DB {
	t.Helper()
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{
		Name:     "decision-flow",
		TypeName: "Decision",
		States: []State{
			{Name: "ratified", IsInitial: true},
			{Name: "superseded"},
		},
		Transitions: []Transition{
			{From: "ratified", To: "superseded", RequiredReason: true},
		},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	return db
}

func TestValidateTransition_RequiredReasonMissing(t *testing.T) {
	db := setupReasonFlowDB(t)
	err := validateTransition(context.Background(), db, nil, "", "Decision", "ratified", "superseded", "")
	if !errors.Is(err, ErrBadRequest) {
		t.Errorf("missing reason: want ErrBadRequest, got %v", err)
	}
}

func TestValidateTransition_RequiredReasonSatisfied(t *testing.T) {
	db := setupReasonFlowDB(t)
	err := validateTransition(context.Background(), db, nil, "", "Decision", "ratified", "superseded", "no longer accurate")
	if err != nil {
		t.Errorf("reason supplied: want nil, got %v", err)
	}
}

func TestValidateTransition_RequiredReasonZeroValue_NoOp(t *testing.T) {
	// agentJobFlow's transitions have RequiredReason unset (zero value = false) —
	// existing flows are completely unaffected, no reason needed.
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(agentJobFlow); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	if err := validateTransition(ctx, db, nil, "", "AgentJob", "draft", "published", ""); err != nil {
		t.Errorf("RequiredReason zero value: want nil (no reason needed), got %v", err)
	}
}

func TestValidateTransition_noFlow(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}
	// Delete the default flow so no flow exists at all — validateTransition returns nil.
	if _, err := db.ExecContext(ctx, `DELETE FROM smeldr_state_flows`); err != nil {
		t.Fatalf("delete flows: %v", err)
	}
	if err := validateTransition(ctx, db, nil, "", "GenericPost", "draft", "published", ""); err != nil {
		t.Errorf("no flow: want nil, got %v", err)
	}
}

func TestValidateTransition_transitionQueryError(t *testing.T) {
	// transitFailDB returns a valid SQLite-probe result (count=0) and a valid flowID,
	// but fails the SELECT required_role FROM smeldr_transitions query with a real error
	// (not sql.ErrNoRows — that would mean "transition not found" → ErrConflict).
	// D34: validateTransition must fail CLOSED (ErrInternal) when the transitions
	// query errors — this branch fires before the strict check is ever reached,
	// for every transition, strict or not.
	ctx := context.Background()
	db := &transitFailDB{}
	err := validateTransition(ctx, db, nil, "", "Post", "draft", "published", "")
	if !errors.Is(err, ErrInternal) {
		t.Errorf("transitions query error: want ErrInternal (fail closed), got %v", err)
	}
}

func TestValidateTransition_unknownTargetState(t *testing.T) {
	// "done" is not a state in agentJobFlow (draft/published/paused/archived).
	// The new target-state pre-check must return ErrConflict with a message that
	// identifies the target state as invalid, not merely that the transition edge
	// is absent.
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(agentJobFlow); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	err := validateTransition(ctx, db, nil, "", "AgentJob", "draft", "done", "")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("unknown target state: want ErrConflict, got %v", err)
	}
	if !strings.Contains(err.Error(), "not a valid target state") {
		t.Errorf("unknown target state: want 'not a valid target state' in message, got %q", err.Error())
	}
}

// transitFailDB simulates a DB that passes the sqlite_master probe and flow lookup
// but fails the smeldr_transitions SELECT required_role query with a real non-ErrNoRows
// error, exercising the fail-open path in validateTransition.
type transitFailDB struct{}

func (d *transitFailDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return nil, nil
}
func (d *transitFailDB) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, nil
}
func (d *transitFailDB) QueryRowContext(ctx context.Context, query string, _ ...any) *sql.Row {
	if strings.HasPrefix(query, "SELECT COUNT(*) FROM sqlite_master") {
		// sqlite_master probe — return count=0 to signal SQLite.
		conn := &guardRowConn{val: int64(0)}
		return sql.OpenDB(conn).QueryRowContext(ctx, "SELECT v")
	}
	if strings.HasPrefix(query, "SELECT id FROM smeldr_state_flows") {
		// Flow lookup — return a valid flowID so validation proceeds.
		conn := &guardRowConn{val: int64(1)}
		return sql.OpenDB(conn).QueryRowContext(ctx, "SELECT v")
	}
	// SELECT required_role FROM smeldr_transitions — return a real driver error
	// (not ErrNoRows, which would mean "transition not found" → ErrConflict).
	return sql.OpenDB(&queryErrConnector{err: errors.New("simulated transitions query error")}).QueryRowContext(ctx, "SELECT v")
}

// queryErrConnector is a sql.Connector whose Prepare-time query always returns an
// error, causing sql.Row.Scan to return that error (not sql.ErrNoRows).
type queryErrConnector struct{ err error }

func (c *queryErrConnector) Connect(_ context.Context) (driver.Conn, error) {
	return &queryErrDriverConn{err: c.err}, nil
}
func (c *queryErrConnector) Driver() driver.Driver { return dummyDriver{} }

type queryErrDriverConn struct{ err error }

func (c *queryErrDriverConn) Prepare(_ string) (driver.Stmt, error) { return c, nil }
func (c *queryErrDriverConn) Close() error                          { return nil }
func (c *queryErrDriverConn) Begin() (driver.Tx, error)             { return nil, nil }
func (c *queryErrDriverConn) NumInput() int                         { return -1 }
func (c *queryErrDriverConn) Exec(_ []driver.Value) (driver.Result, error) {
	return nil, nil
}
func (c *queryErrDriverConn) Query(_ []driver.Value) (driver.Rows, error) {
	return nil, c.err
}

// flowResolveFailDB simulates a real DB error while resolving a state
// flow's smeldr_state_flows lookup, for T249's fail-open regression
// coverage. failTypeQuery makes the type-specific lookup return a real
// error; failDefaultQuery makes the default-flow fallback return a real
// error (only reached when the type-specific lookup legitimately misses).
// typeQueried/defaultQueried record whether each query actually ran, so a
// test can assert resolveFlowID did not fall through to the default query
// after a real (non-ErrNoRows) error on the type-specific one.
type flowResolveFailDB struct {
	failTypeQuery, failDefaultQuery bool
	typeQueried, defaultQueried     bool
}

func (d *flowResolveFailDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return nil, nil
}
func (d *flowResolveFailDB) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, nil
}
func (d *flowResolveFailDB) QueryRowContext(ctx context.Context, query string, _ ...any) *sql.Row {
	if strings.HasPrefix(query, "SELECT COUNT(*) FROM sqlite_master") {
		conn := &guardRowConn{val: int64(0)}
		return sql.OpenDB(conn).QueryRowContext(ctx, "SELECT v")
	}
	if strings.Contains(query, "type_name = $1") {
		d.typeQueried = true
		if d.failTypeQuery {
			return sql.OpenDB(&queryErrConnector{err: errors.New("simulated flow lookup error")}).QueryRowContext(ctx, "SELECT v")
		}
		return sql.OpenDB(&guardRowConn{noRow: true}).QueryRowContext(ctx, "SELECT v")
	}
	// default-flow fallback query (type_name IS NULL AND name = 'default')
	d.defaultQueried = true
	if d.failDefaultQuery {
		return sql.OpenDB(&queryErrConnector{err: errors.New("simulated default flow lookup error")}).QueryRowContext(ctx, "SELECT v")
	}
	return sql.OpenDB(&guardRowConn{noRow: true}).QueryRowContext(ctx, "SELECT v")
}

// TestResolveFlowID_QueryError pins T249: a real error on the type-specific
// smeldr_state_flows query must be propagated, not swallowed into
// found=false — and must not fall through to the default-flow query, since
// a DB error is not "keep trying," unlike a legitimate miss.
func TestResolveFlowID_QueryError(t *testing.T) {
	db := &flowResolveFailDB{failTypeQuery: true}
	_, found, err := resolveFlowID(context.Background(), db, "Post")
	if err == nil {
		t.Fatal("type-specific query error: want non-nil err, got nil")
	}
	if found {
		t.Error("type-specific query error: want found=false")
	}
	if db.defaultQueried {
		t.Error("type-specific query error: must not fall through to the default-flow query")
	}
}

// TestResolveFlowID_DefaultQueryError pins T249: the type-specific query
// legitimately misses (sql.ErrNoRows), then a real error on the
// default-flow fallback query must also be propagated, not swallowed.
func TestResolveFlowID_DefaultQueryError(t *testing.T) {
	db := &flowResolveFailDB{failDefaultQuery: true}
	_, found, err := resolveFlowID(context.Background(), db, "Post")
	if err == nil {
		t.Fatal("default-flow query error: want non-nil err, got nil")
	}
	if found {
		t.Error("default-flow query error: want found=false")
	}
	if !db.typeQueried {
		t.Error("default-flow query error: type-specific query should have run first")
	}
}

// TestValidateTransition_flowResolutionError is the direct regression pin
// for T249: validateTransition must fail CLOSED (ErrInternal) when
// resolveFlowID itself errors, not silently permit the transition as it
// did before this fix (the error was discarded and treated as "no flow
// registered — no validation").
func TestValidateTransition_flowResolutionError(t *testing.T) {
	db := &flowResolveFailDB{failTypeQuery: true}
	err := validateTransition(context.Background(), db, nil, "", "Post", "draft", "published", "")
	if !errors.Is(err, ErrInternal) {
		t.Errorf("flow resolution error: want ErrInternal (fail closed), got %v", err)
	}
}

// — validateInitialState tests ——————————————————————————————————————————————————

func TestValidateInitialState_nilDB(t *testing.T) {
	ctx := context.Background()
	if err := validateInitialState(ctx, nil, "Post", "draft"); err != nil {
		t.Errorf("nil db: want nil, got %v", err)
	}
}

func TestValidateInitialState_emptyStatus(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := validateInitialState(ctx, db, "Post", ""); err != nil {
		t.Errorf("empty status: want nil, got %v", err)
	}
}

func TestValidateInitialState_nonSQLite(t *testing.T) {
	// failOnNthExecDB.QueryRowContext returns guardRowConn{noRow:true} → scan fails
	// → sqlite_master probe returns error → validateInitialState returns nil.
	ctx := context.Background()
	db := &failOnNthExecDB{failAt: 999}
	if err := validateInitialState(ctx, db, "Post", "draft"); err != nil {
		t.Errorf("non-SQLite: want nil, got %v", err)
	}
}

func TestValidateInitialState_noFlow(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}
	// Delete the default flow so no flow exists — validateInitialState returns nil.
	if _, err := db.ExecContext(ctx, `DELETE FROM smeldr_state_flows`); err != nil {
		t.Fatalf("delete flows: %v", err)
	}
	if err := validateInitialState(ctx, db, "AgentJob", "draft"); err != nil {
		t.Errorf("no flow: want nil, got %v", err)
	}
}

func TestValidateInitialState_validState(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(agentJobFlow); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	// "draft" is a state in agentJobFlow.
	if err := validateInitialState(ctx, db, "AgentJob", "draft"); err != nil {
		t.Errorf("valid state: want nil, got %v", err)
	}
}

func TestValidateInitialState_invalidState(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(agentJobFlow); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	// "done" is NOT a state in agentJobFlow (draft/published/paused/archived).
	err := validateInitialState(ctx, db, "AgentJob", "done")
	if !errors.Is(err, ErrConflict) {
		t.Errorf("invalid state: want ErrConflict, got %v", err)
	}
}

func TestValidateInitialState_stateQueryError(t *testing.T) {
	// stateCountFailDB passes the sqlite_master probe and flow lookup but
	// fails the smeldr_states COUNT query — validateInitialState must fail open.
	ctx := context.Background()
	db := &stateCountFailDB{}
	if err := validateInitialState(ctx, db, "Post", "draft"); err != nil {
		t.Errorf("state count query error: want nil (fail open), got %v", err)
	}
}

// stateCountFailDB simulates a DB that passes the sqlite_master probe and flow
// lookup but fails the SELECT COUNT(*) FROM smeldr_states query with a real error,
// exercising the fail-open path in validateInitialState.
type stateCountFailDB struct{}

func (d *stateCountFailDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return nil, nil
}
func (d *stateCountFailDB) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, nil
}
func (d *stateCountFailDB) QueryRowContext(ctx context.Context, query string, _ ...any) *sql.Row {
	if strings.Contains(query, "sqlite_master") {
		// sqlite_master probe — return count=0 to signal SQLite.
		return sql.OpenDB(&guardRowConn{val: int64(0)}).QueryRowContext(ctx, "SELECT v")
	}
	if strings.HasPrefix(query, "SELECT id FROM smeldr_state_flows") {
		// Flow lookup — return a valid flowID so validation proceeds.
		return sql.OpenDB(&guardRowConn{val: int64(1)}).QueryRowContext(ctx, "SELECT v")
	}
	// SELECT COUNT(*) FROM smeldr_states — return a real driver error.
	return sql.OpenDB(&queryErrConnector{err: errors.New("simulated state count error")}).QueryRowContext(ctx, "SELECT v")
}

// — defaultInitialState ——————————————————————————————————————————————————————

func TestDefaultInitialState_nilDB(t *testing.T) {
	if got := defaultInitialState(context.Background(), nil, "Goal"); got != "" {
		t.Errorf("nil DB: got %q, want empty", got)
	}
}

func TestDefaultInitialState_nonSQLite(t *testing.T) {
	// failOnNthExecDB.QueryRowContext returns guardRowConn{noRow:true} → scan
	// fails → sqlite_master probe returns error → fail open.
	db := &failOnNthExecDB{failAt: 999}
	if got := defaultInitialState(context.Background(), db, "Goal"); got != "" {
		t.Errorf("non-SQLite: got %q, want empty", got)
	}
}

func TestDefaultInitialState_noFlow(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}
	// No flow registered for "Goal" — only the seeded default flow exists,
	// which is keyed on type_name IS NULL, not "Goal" itself.
	if got := defaultInitialState(ctx, db, "Goal"); got != "" {
		t.Errorf("no flow: got %q, want empty", got)
	}
}

func TestDefaultInitialState_noInitialState(t *testing.T) {
	// restrictedFlow's states (published/archived) have neither marked
	// IsInitial — the flow exists but no row satisfies is_initial=true.
	db := newSQLiteDB(t)
	restrictedFlow(t, db)
	if got := defaultInitialState(context.Background(), db, "testPost"); got != "" {
		t.Errorf("flow with no initial state: got %q, want empty", got)
	}
}

func TestDefaultInitialState_customInitialState(t *testing.T) {
	// orchGoalFlow is the real, production Goal flow (orchestration.go) —
	// IsInitial state is "open", never "draft". This is T180's own
	// motivating scenario, not a synthetic fixture.
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(orchGoalFlow()); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	if got := defaultInitialState(ctx, db, "Goal"); got != "open" {
		t.Errorf("custom initial state: got %q, want %q", got, "open")
	}
}

func TestDefaultInitialState_stateQueryError(t *testing.T) {
	// stateCountFailDB passes the sqlite_master probe and flow lookup but
	// fails the smeldr_states query — must fail open.
	if got := defaultInitialState(context.Background(), &stateCountFailDB{}, "Goal"); got != "" {
		t.Errorf("state query error: got %q, want empty", got)
	}
}

// — Module[T] integration tests for validateTransition ————————————————————————

// restrictedFlow registers a flow for "testPost" that only permits published→archived.
// This causes MCPPublish (draft→published) and MCPSchedule (draft→scheduled) to fail.
func restrictedFlow(t *testing.T, db DB) {
	t.Helper()
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}
	app := &App{cfg: Config{DB: db}}
	flow := StateFlow{
		Name:     "restricted",
		TypeName: "testPost",
		States:   []State{{Name: "published"}, {Name: "archived", IsTerminal: true}},
		Transitions: []Transition{
			{From: "published", To: "archived"},
		},
	}
	if err := app.RegisterFlow(flow); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
}

func TestMCPPublish_invalidTransition(t *testing.T) {
	sqlDB := newSQLiteDB(t)
	restrictedFlow(t, sqlDB)

	mem := NewMemoryRepo[*testPost]()
	p := seedPost(t, mem, "Test", Draft)

	m := newTestModule(mem)
	m.setDB(sqlDB)

	ctx := NewTestContext(editorUser())
	err := m.MCPPublish(ctx, p.Slug, "")
	if !errors.Is(err, ErrConflict) {
		t.Errorf("MCPPublish on invalid transition: want ErrConflict, got %v", err)
	}
}

func TestMCPArchive_invalidTransition(t *testing.T) {
	sqlDB := newSQLiteDB(t)
	restrictedFlow(t, sqlDB)

	mem := NewMemoryRepo[*testPost]()
	p := seedPost(t, mem, "Test", Draft)

	m := newTestModule(mem)
	m.setDB(sqlDB)

	ctx := NewTestContext(editorUser())
	err := m.MCPArchive(ctx, p.Slug, "")
	if !errors.Is(err, ErrConflict) {
		t.Errorf("MCPArchive on invalid transition: want ErrConflict, got %v", err)
	}
}

func TestMCPSchedule_invalidTransition(t *testing.T) {
	sqlDB := newSQLiteDB(t)
	restrictedFlow(t, sqlDB)

	mem := NewMemoryRepo[*testPost]()
	p := seedPost(t, mem, "Test", Draft)

	m := newTestModule(mem)
	m.setDB(sqlDB)

	ctx := NewTestContext(editorUser())
	err := m.MCPSchedule(ctx, p.Slug, time.Now().Add(time.Hour), "")
	if !errors.Is(err, ErrConflict) {
		t.Errorf("MCPSchedule on invalid transition: want ErrConflict, got %v", err)
	}
}

// — reason threading (T237) ————————————————————————————————————————————————

// requiredReasonPostFlow registers a flow for "testPost" whose three
// draft-originating transitions all require a reason — enough to prove
// MCPPublish/MCPSchedule/MCPArchive's own new reason parameter actually
// reaches validateTransition's RequiredReason gate, not just that the
// parameter compiles.
func requiredReasonPostFlow(t *testing.T, db DB) {
	t.Helper()
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}
	app := &App{cfg: Config{DB: db}}
	flow := StateFlow{
		Name:     "reason-required-post",
		TypeName: "testPost",
		States: []State{
			{Name: "draft", IsInitial: true},
			{Name: "published"},
			{Name: "scheduled"},
			{Name: "archived", IsTerminal: true},
		},
		Transitions: []Transition{
			{From: "draft", To: "published", RequiredReason: true},
			{From: "draft", To: "scheduled", RequiredReason: true},
			{From: "draft", To: "archived", RequiredReason: true},
		},
	}
	if err := app.RegisterFlow(flow); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
}

func TestModule_MCPPublish_ThreadsReason(t *testing.T) {
	sqlDB := newSQLiteDB(t)
	requiredReasonPostFlow(t, sqlDB)

	mem := NewMemoryRepo[*testPost]()
	p := seedPost(t, mem, "Test", Draft)

	m := newTestModule(mem)
	m.setDB(sqlDB)

	ctx := NewTestContext(editorUser())
	// No reason: RequiredReason gate rejects — proves the gate is actually
	// wired for this flow before testing that the real argument satisfies it.
	if err := m.MCPPublish(ctx, p.Slug, ""); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("MCPPublish with no reason: want ErrBadRequest, got %v", err)
	}
	// A real reason: the gate is satisfied — this only happens if MCPPublish's
	// own reason parameter actually reaches validateTransition's last argument.
	if err := m.MCPPublish(ctx, p.Slug, "scheduled release window closed early"); err != nil {
		t.Errorf("MCPPublish with reason: want nil, got %v", err)
	}
}

func TestModule_MCPSchedule_ThreadsReason(t *testing.T) {
	sqlDB := newSQLiteDB(t)
	requiredReasonPostFlow(t, sqlDB)

	mem := NewMemoryRepo[*testPost]()
	p := seedPost(t, mem, "Test", Draft)

	m := newTestModule(mem)
	m.setDB(sqlDB)

	ctx := NewTestContext(editorUser())
	if err := m.MCPSchedule(ctx, p.Slug, time.Now().Add(time.Hour), ""); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("MCPSchedule with no reason: want ErrBadRequest, got %v", err)
	}
	if err := m.MCPSchedule(ctx, p.Slug, time.Now().Add(time.Hour), "coordinated with marketing"); err != nil {
		t.Errorf("MCPSchedule with reason: want nil, got %v", err)
	}
}

func TestModule_MCPArchive_ThreadsReason(t *testing.T) {
	sqlDB := newSQLiteDB(t)
	requiredReasonPostFlow(t, sqlDB)

	mem := NewMemoryRepo[*testPost]()
	p := seedPost(t, mem, "Test", Draft)

	m := newTestModule(mem)
	m.setDB(sqlDB)

	ctx := NewTestContext(editorUser())
	if err := m.MCPArchive(ctx, p.Slug, ""); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("MCPArchive with no reason: want ErrBadRequest, got %v", err)
	}
	if err := m.MCPArchive(ctx, p.Slug, "superseded by rewrite"); err != nil {
		t.Errorf("MCPArchive with reason: want nil, got %v", err)
	}
}

func TestModule_updateHandler_ReasonHeader_ThreadsToValidateTransition(t *testing.T) {
	sqlDB := newSQLiteDB(t)
	requiredReasonPostFlow(t, sqlDB)

	mem := NewMemoryRepo[*testPost]()
	p := seedPost(t, mem, "Test", Draft)

	m := newTestModule(mem)
	app := New(MustConfig(Config{DB: sqlDB, BaseURL: "https://example.com", Secret: []byte("update-handler-reason-test-secret1")}))
	app.Content(m)

	body, _ := json.Marshal(map[string]any{
		"Title":  p.Title,
		"Body":   p.Body,
		"Status": "published",
	})
	req := httptest.NewRequest(http.MethodPut, m.prefix+"/"+p.Slug, bytes.NewReader(body))
	req.Header.Set("Smeldr-Reason", "editorial sign-off received")
	tok, _ := SignToken(editorUser(), "update-handler-reason-test-secret1", 0)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
}

func TestModule_updateHandler_RequiredReason_MissingHeader_StillRejected(t *testing.T) {
	sqlDB := newSQLiteDB(t)
	requiredReasonPostFlow(t, sqlDB)

	mem := NewMemoryRepo[*testPost]()
	p := seedPost(t, mem, "Test", Draft)

	m := newTestModule(mem)
	app := New(MustConfig(Config{DB: sqlDB, BaseURL: "https://example.com", Secret: []byte("update-handler-reason-test-secret2")}))
	app.Content(m)

	body, _ := json.Marshal(map[string]any{
		"Title":  p.Title,
		"Body":   p.Body,
		"Status": "published",
	})
	req := httptest.NewRequest(http.MethodPut, m.prefix+"/"+p.Slug, bytes.NewReader(body))
	// Smeldr-Reason deliberately not set.
	tok, _ := SignToken(editorUser(), "update-handler-reason-test-secret2", 0)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", w.Code, w.Body.String())
	}
}

func TestModule_updateHandler_NoReasonHeader_SameStatusUnaffected(t *testing.T) {
	// A plain-content update with no status change never reaches the
	// RequiredReason gate at all — regression pin that reading an absent
	// Smeldr-Reason header (empty string) does not disturb the common,
	// same-status edit path most PUT calls actually are.
	sqlDB := newSQLiteDB(t)
	if err := migrateStateFlows(context.Background(), sqlDB); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}

	mem := NewMemoryRepo[*testPost]()
	p := seedPost(t, mem, "Test", Draft)

	m := newTestModule(mem)
	app := New(MustConfig(Config{DB: sqlDB, BaseURL: "https://example.com", Secret: []byte("update-handler-reason-test-secret3")}))
	app.Content(m)

	body, _ := json.Marshal(map[string]any{
		"Title":  "Updated Title",
		"Body":   p.Body,
		"Status": "draft",
	})
	req := httptest.NewRequest(http.MethodPut, m.prefix+"/"+p.Slug, bytes.NewReader(body))
	tok, _ := SignToken(editorUser(), "update-handler-reason-test-secret3", 0)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
}

// — Module[T] integration tests for validateInitialState ————————————————————————

func TestMCPCreate_invalidInitialState(t *testing.T) {
	// restrictedFlow registers only "published" and "archived" states for "testPost".
	// MCPCreate with an explicit status="done" must be rejected with ErrConflict.
	sqlDB := newSQLiteDB(t)
	restrictedFlow(t, sqlDB)

	mem := NewMemoryRepo[*testPost]()
	m := newTestModule(mem)
	m.setDB(sqlDB)

	ctx := NewTestContext(editorUser())
	_, err := m.MCPCreate(ctx, map[string]any{
		"Title":  "Test",
		"Status": "done",
	})
	if !errors.Is(err, ErrConflict) {
		t.Errorf("MCPCreate with invalid initial state: want ErrConflict, got %v", err)
	}
	// Nothing should have been saved.
	items, _ := mem.FindAll(context.Background(), ListOptions{})
	if len(items) != 0 {
		t.Errorf("repo count = %d; want 0 (aborted on invalid initial state)", len(items))
	}
}

func TestCreateHandler_invalidInitialState(t *testing.T) {
	// restrictedFlow registers only "published" and "archived" states for "testPost".
	// POST with Status="done" must be rejected with 409 Conflict.
	sqlDB := newSQLiteDB(t)
	restrictedFlow(t, sqlDB)

	mem := NewMemoryRepo[*testPost]()
	m := newTestModule(mem)
	m.setDB(sqlDB)

	body, _ := json.Marshal(map[string]string{"Title": "Test", "Status": "done"})
	w := httptest.NewRecorder()
	r := withUser(
		httptest.NewRequest(http.MethodPost, "/testposts", bytes.NewReader(body)),
		editorUser(),
	)
	r.Header.Set("Content-Type", "application/json")
	m.createHandler(w, r)

	if w.Code != http.StatusConflict {
		t.Errorf("createHandler with invalid initial state: status = %d, want 409", w.Code)
	}
	// Nothing should have been saved.
	items, _ := mem.FindAll(context.Background(), ListOptions{})
	if len(items) != 0 {
		t.Errorf("repo count = %d; want 0 (aborted on invalid initial state)", len(items))
	}
}

// — Module[T] integration tests for applyDefaultStatus (T180, A225) —————————————

// customInitialFlow registers a flow for "testPost" whose IsInitial state is
// "backlog", never "draft" — the same shape as the real orchestration types
// (orchestration.go) that motivated this fix, applied to the lightweight
// testPost type so these tests don't need full orchestration wiring.
func customInitialFlow(t *testing.T, db DB) {
	t.Helper()
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}
	app := &App{cfg: Config{DB: db}}
	flow := StateFlow{
		Name:     "custom-initial",
		TypeName: "testPost",
		States:   []State{{Name: "backlog", IsInitial: true}, {Name: "archived", IsTerminal: true}},
		Transitions: []Transition{
			{From: "backlog", To: "archived"},
		},
	}
	if err := app.RegisterFlow(flow); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
}

func TestMCPCreate_omittedStatus_customInitialState(t *testing.T) {
	sqlDB := newSQLiteDB(t)
	customInitialFlow(t, sqlDB)

	mem := NewMemoryRepo[*testPost]()
	m := newTestModule(mem)
	m.setDB(sqlDB)

	ctx := NewTestContext(editorUser())
	created, err := m.MCPCreate(ctx, map[string]any{"Title": "Test"})
	if err != nil {
		t.Fatalf("MCPCreate with omitted status: %v", err)
	}
	item := created.(*testPost)
	if item.Status != "backlog" {
		t.Errorf("Status = %q, want %q", item.Status, "backlog")
	}
}

func TestMCPCreate_omittedStatus_defaultsToDraft(t *testing.T) {
	// No custom flow registered for "testPost" — regression guard: the
	// overwhelmingly common case (no custom StateFlow) must still default
	// to the literal Draft constant, exactly as before this fix.
	sqlDB := newSQLiteDB(t)
	if err := migrateStateFlows(context.Background(), sqlDB); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}

	mem := NewMemoryRepo[*testPost]()
	m := newTestModule(mem)
	m.setDB(sqlDB)

	ctx := NewTestContext(editorUser())
	created, err := m.MCPCreate(ctx, map[string]any{"Title": "Test"})
	if err != nil {
		t.Fatalf("MCPCreate with omitted status: %v", err)
	}
	item := created.(*testPost)
	if item.Status != Draft {
		t.Errorf("Status = %q, want %q", item.Status, Draft)
	}
}

func TestCreateHandler_omittedStatus_customInitialState(t *testing.T) {
	// Before this fix, an omitted status via HTTP POST silently persisted
	// the literal empty string "" — never "draft", and never the type's own
	// initial state. This proves the fix, not just the absence of an error.
	sqlDB := newSQLiteDB(t)
	customInitialFlow(t, sqlDB)

	mem := NewMemoryRepo[*testPost]()
	m := newTestModule(mem)
	m.setDB(sqlDB)

	body, _ := json.Marshal(map[string]string{"Title": "Test"})
	w := httptest.NewRecorder()
	r := withUser(
		httptest.NewRequest(http.MethodPost, "/testposts", bytes.NewReader(body)),
		editorUser(),
	)
	r.Header.Set("Content-Type", "application/json")
	m.createHandler(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201\nbody: %s", w.Code, w.Body.String())
	}
	var created testPost
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if created.Status != "backlog" {
		t.Errorf("Status = %q, want %q", created.Status, "backlog")
	}
}

func TestCreateHandler_omittedStatus_defaultsToDraft(t *testing.T) {
	// No custom flow registered — regression guard, same as the MCPCreate
	// counterpart above.
	sqlDB := newSQLiteDB(t)
	if err := migrateStateFlows(context.Background(), sqlDB); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}

	mem := NewMemoryRepo[*testPost]()
	m := newTestModule(mem)
	m.setDB(sqlDB)

	body, _ := json.Marshal(map[string]string{"Title": "Test"})
	w := httptest.NewRecorder()
	r := withUser(
		httptest.NewRequest(http.MethodPost, "/testposts", bytes.NewReader(body)),
		editorUser(),
	)
	r.Header.Set("Content-Type", "application/json")
	m.createHandler(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201\nbody: %s", w.Code, w.Body.String())
	}
	var created testPost
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if created.Status != Draft {
		t.Errorf("Status = %q, want %q", created.Status, Draft)
	}
}

// — suppressesSignals unit tests ——————————————————————————————————————————————

func TestSuppressesSignals_nilDB(t *testing.T) {
	ctx := context.Background()
	if suppressesSignals(ctx, nil, "Post", "published") {
		t.Error("nil db: want false, got true")
	}
}

func TestSuppressesSignals_nonSQLite(t *testing.T) {
	// failOnNthExecDB with failAt=999 leaves exec intact but QueryRowContext
	// always returns a no-row result → sqlite_master probe scan fails → false.
	ctx := context.Background()
	db := &failOnNthExecDB{failAt: 999}
	if suppressesSignals(ctx, db, "Post", "published") {
		t.Error("non-SQLite: want false, got true")
	}
}

func TestSuppressesSignals_noFlow(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}
	// No flow registered — suppressesSignals must return false.
	if suppressesSignals(ctx, db, "UnknownType", "published") {
		t.Error("no flow: want false, got true")
	}
}

func TestSuppressesSignals_falseWhenNotSet(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{
		Name:        "no-suppress",
		TypeName:    "testPost",
		States:      []State{{Name: "draft", IsInitial: true}, {Name: "published"}},
		Transitions: []Transition{{From: "draft", To: "published"}},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	if suppressesSignals(ctx, db, "testPost", "published") {
		t.Error("suppresses_signals=false: want false, got true")
	}
}

func TestSuppressesSignals_trueWhenSet(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{
		Name:     "suppress-test",
		TypeName: "testPost",
		States: []State{
			{Name: "draft", IsInitial: true},
			{Name: "published", SuppressesSignals: true},
		},
		Transitions: []Transition{{From: "draft", To: "published"}},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	if !suppressesSignals(ctx, db, "testPost", "published") {
		t.Error("suppresses_signals=true: want true, got false")
	}
}

func TestSuppressesSignals_defaultFlowFallback(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}
	// migrateStateFlows already seeds a default flow (name='default', type_name IS NULL).
	// Get its ID and insert a state with suppresses_signals=true.
	var flowID string
	if err := db.QueryRowContext(ctx, `SELECT id FROM smeldr_state_flows WHERE name = 'default' AND type_name IS NULL`).Scan(&flowID); err != nil {
		t.Fatalf("get default flow id: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_states (id, flow_id, name, is_initial, is_terminal, suppresses_signals) VALUES (?, ?, 'quarantined', FALSE, FALSE, TRUE)`,
		NewID(), flowID,
	); err != nil {
		t.Fatalf("insert state: %v", err)
	}
	// "UnknownType" has no custom flow → falls back to the default flow → quarantined suppresses.
	if !suppressesSignals(ctx, db, "UnknownType", "quarantined") {
		t.Error("default flow fallback: want true for quarantined with suppresses_signals=1, got false")
	}
}

func TestSuppressesSignals_scanError(t *testing.T) {
	// suppressFailDB: probe succeeds, flow lookup succeeds, smeldr_states query returns no-row.
	ctx := context.Background()
	db := &suppressFailDB{}
	if suppressesSignals(ctx, db, "Post", "published") {
		t.Error("states scan error: want false (fail open), got true")
	}
}

// suppressFailDB simulates a DB that passes the sqlite_master probe and flow
// lookup but fails the smeldr_states query (no-row → scan error).
type suppressFailDB struct{}

func (d *suppressFailDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return nil, nil
}
func (d *suppressFailDB) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, nil
}
func (d *suppressFailDB) QueryRowContext(ctx context.Context, query string, _ ...any) *sql.Row {
	if strings.HasPrefix(query, "SELECT COUNT(*) FROM sqlite_master") {
		conn := &guardRowConn{val: int64(0)}
		return sql.OpenDB(conn).QueryRowContext(ctx, "SELECT v")
	}
	if strings.HasPrefix(query, "SELECT id FROM smeldr_state_flows") {
		conn := &guardRowConn{val: int64(1)}
		return sql.OpenDB(conn).QueryRowContext(ctx, "SELECT v")
	}
	// smeldr_states query — return no-row to trigger scan error.
	conn := &guardRowConn{noRow: true}
	return sql.OpenDB(conn).QueryRowContext(ctx, "SELECT v")
}

func TestSuppressesSignals_bothFlowsFail(t *testing.T) {
	// suppressBothFlowsFailDB: probe succeeds, but BOTH flow lookups return no-row
	// → default-flow fallback also fails → false (line 286 path in suppressesSignals).
	ctx := context.Background()
	db := &suppressBothFlowsFailDB{}
	if suppressesSignals(ctx, db, "Post", "published") {
		t.Error("both flows fail: want false (fail open), got true")
	}
}

// suppressBothFlowsFailDB: sqlite_master probe succeeds; all smeldr_state_flows
// queries return no-row, so both the custom and default flow lookups fail.
type suppressBothFlowsFailDB struct{}

func (d *suppressBothFlowsFailDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return nil, nil
}
func (d *suppressBothFlowsFailDB) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, nil
}
func (d *suppressBothFlowsFailDB) QueryRowContext(ctx context.Context, query string, _ ...any) *sql.Row {
	if strings.HasPrefix(query, "SELECT COUNT(*) FROM sqlite_master") {
		return sql.OpenDB(&guardRowConn{val: int64(0)}).QueryRowContext(ctx, "SELECT v")
	}
	return sql.OpenDB(&guardRowConn{noRow: true}).QueryRowContext(ctx, "SELECT v")
}

// — notifyAfter suppression integration tests ——————————————————————————————————

// suppressedFlow registers a flow for "testPost" where "published" has
// suppresses_signals=true. "draft" does not suppress signals.
func suppressedFlow(t *testing.T, db DB) {
	t.Helper()
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{
		Name:     "suppressed",
		TypeName: "testPost",
		States: []State{
			{Name: "draft", IsInitial: true},
			{Name: "published", SuppressesSignals: true},
		},
		Transitions: []Transition{{From: "draft", To: "published"}},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
}

func TestNotifyAfter_suppressedState_hooksSkipped(t *testing.T) {
	sqlDB := newSQLiteDB(t)
	suppressedFlow(t, sqlDB)

	mem := NewMemoryRepo[*testPost]()
	m := newTestModule(mem)
	m.setDB(sqlDB)

	hookCalled := make(chan struct{}, 1)
	m.setAfterHook(func(_ Context, _ LifecycleEvent, _ afterHookMeta, _ any) {
		hookCalled <- struct{}{}
	})

	p := &testPost{Node: Node{ID: NewID(), Slug: "s", Status: Published}}
	m.notifyAfter(NewTestContext(User{}), AfterPublish, "draft", surfaceHTTP, "", p)

	select {
	case <-hookCalled:
		t.Error("afterHook was called; want suppressed because published has suppresses_signals=true")
	case <-time.After(30 * time.Millisecond):
		// ok — hook was not called
	}
}

func TestNotifyAfter_unsuppressedState_hooksFire(t *testing.T) {
	sqlDB := newSQLiteDB(t)
	suppressedFlow(t, sqlDB)

	mem := NewMemoryRepo[*testPost]()
	m := newTestModule(mem)
	m.setDB(sqlDB)

	hookCalled := make(chan struct{}, 1)
	m.setAfterHook(func(_ Context, _ LifecycleEvent, _ afterHookMeta, _ any) {
		hookCalled <- struct{}{}
	})

	// Item is in "draft" state — suppresses_signals=false for draft → hook must fire.
	p := &testPost{Node: Node{ID: NewID(), Slug: "s", Status: Draft}}
	m.notifyAfter(NewTestContext(User{}), AfterUpdate, "", surfaceHTTP, "", p)

	select {
	case <-hookCalled:
		// ok — hook fired as expected
	case <-time.After(2 * time.Second):
		t.Error("afterHook was not called; want fired because draft does not suppress signals")
	}
}

// — fireAsyncTriggers unit tests ——————————————————————————————————————————————

// setupTriggerFlow registers a flow for "testPost" (draft→published) and inserts
// a trigger row for that transition. Returns the trigger row ID.
func setupTriggerFlow(t *testing.T, db DB, triggerClass, triggerType string) {
	t.Helper()
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{
		Name:     "trigger-test",
		TypeName: "testPost",
		States: []State{
			{Name: "draft", IsInitial: true},
			{Name: "published"},
		},
		Transitions: []Transition{{From: "draft", To: "published"}},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	var transID string
	if err := db.QueryRowContext(ctx, `
		SELECT t.id FROM smeldr_transitions t
		JOIN smeldr_state_flows f ON t.flow_id = f.id
		WHERE f.type_name = 'testPost' AND t.from_state = 'draft' AND t.to_state = 'published'
	`).Scan(&transID); err != nil {
		t.Fatalf("get transition id: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_transition_triggers (id, transition_id, trigger_class, trigger_type, config) VALUES (?, ?, ?, ?, ?)`,
		NewID(), transID, triggerClass, triggerType, `{}`,
	); err != nil {
		t.Fatalf("insert trigger: %v", err)
	}
}

func TestFireAsyncTriggers_nilDB(t *testing.T) {
	// Must not panic and return immediately.
	fireAsyncTriggers(context.Background(), nil, "testPost", "draft", "published", "")
}

func TestFireAsyncTriggers_nonSQLite(t *testing.T) {
	// failOnNthExecDB returns no-row on QueryRowContext → sqlite_master probe fails → return.
	db := &failOnNthExecDB{failAt: 999}
	fireAsyncTriggers(context.Background(), db, "testPost", "draft", "published", "")
}

func TestFireAsyncTriggers_noTriggers(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}
	// Flow registered but no trigger rows — must return without launching goroutines.
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{
		Name:        "no-trigger",
		TypeName:    "testPost",
		States:      []State{{Name: "draft", IsInitial: true}, {Name: "published"}},
		Transitions: []Transition{{From: "draft", To: "published"}},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	fireAsyncTriggers(ctx, db, "testPost", "draft", "published", "")
}

func TestFireAsyncTriggers_syncTrigger_skipped(t *testing.T) {
	db := newSQLiteDB(t)
	setupTriggerFlow(t, db, "sync", "create-LifecycleEvent")

	prev := slog.Default()
	t.Cleanup(func() { restoreDefaultLogging(prev) })
	var buf safeBuf
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))

	fireAsyncTriggers(context.Background(), db, "testPost", "draft", "published", "")
	time.Sleep(30 * time.Millisecond)

	if strings.Contains(buf.String(), "fireAsyncTriggers dispatch") {
		t.Error("sync trigger should not be dispatched, but slog shows a dispatch message")
	}
}

func TestFireAsyncTriggers_asyncTrigger_dispatched(t *testing.T) {
	db := newSQLiteDB(t)
	setupTriggerFlow(t, db, "async", "create-LifecycleEvent")

	prev := slog.Default()
	t.Cleanup(func() { restoreDefaultLogging(prev) })
	var buf safeBuf
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))

	fireAsyncTriggers(context.Background(), db, "testPost", "draft", "published", "")
	time.Sleep(50 * time.Millisecond)

	if !strings.Contains(buf.String(), "fireAsyncTriggers dispatch") {
		t.Error("async trigger: want slog.Info dispatch message, got none")
	}
	if !strings.Contains(buf.String(), "create-LifecycleEvent") {
		t.Error("async trigger: want trigger_type=create-LifecycleEvent in log, missing")
	}
}

func TestFireAsyncTriggers_queryError(t *testing.T) {
	// triggerQueryFailDB: probe succeeds, QueryContext returns an error → fail-open.
	db := &triggerQueryFailDB{}
	fireAsyncTriggers(context.Background(), db, "testPost", "draft", "published", "")
}

// triggerQueryFailDB: sqlite_master probe succeeds; QueryContext returns an error.
type triggerQueryFailDB struct{}

func (d *triggerQueryFailDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return nil, nil
}
func (d *triggerQueryFailDB) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, errors.New("query error")
}
func (d *triggerQueryFailDB) QueryRowContext(ctx context.Context, query string, _ ...any) *sql.Row {
	if strings.HasPrefix(query, "SELECT COUNT(*) FROM sqlite_master") {
		conn := &guardRowConn{val: int64(0)}
		return sql.OpenDB(conn).QueryRowContext(ctx, "SELECT v")
	}
	conn := &guardRowConn{noRow: true}
	return sql.OpenDB(conn).QueryRowContext(ctx, "SELECT v")
}

func TestFireAsyncTriggers_scanError(t *testing.T) {
	// triggerScanErrDB: probe succeeds; rows return 1 column but Scan expects 2 — scan error path.
	db := &triggerScanErrDB{}
	fireAsyncTriggers(context.Background(), db, "testPost", "draft", "published", "")
}

func TestFireAsyncTriggers_rowsError(t *testing.T) {
	// triggerRowsErrDB: probe succeeds; driver.Rows.Next returns non-EOF error — rows.Err() path.
	db := &triggerRowsErrDB{}
	fireAsyncTriggers(context.Background(), db, "testPost", "draft", "published", "")
}

// triggerScanErrDB: probe succeeds; QueryContext returns rows with 1 column while
// fireAsyncTriggers scans into 2 destinations — triggers the rows.Scan error path.
type triggerScanErrDB struct{}

func (d *triggerScanErrDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return nil, nil
}
func (d *triggerScanErrDB) QueryContext(ctx context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return sql.OpenDB(&scanErrConnector{}).QueryContext(ctx, "SELECT v")
}
func (d *triggerScanErrDB) QueryRowContext(ctx context.Context, query string, _ ...any) *sql.Row {
	if strings.HasPrefix(query, "SELECT COUNT(*) FROM sqlite_master") {
		return sql.OpenDB(&guardRowConn{val: int64(0)}).QueryRowContext(ctx, "SELECT v")
	}
	return sql.OpenDB(&guardRowConn{noRow: true}).QueryRowContext(ctx, "SELECT v")
}

// scanErrConnector returns a driver connection whose rows have one column.
type scanErrConnector struct{}

func (c *scanErrConnector) Connect(_ context.Context) (driver.Conn, error) {
	return &scanErrConn{}, nil
}
func (c *scanErrConnector) Driver() driver.Driver { return dummyDriver{} }

type scanErrConn struct{}

func (c *scanErrConn) Prepare(_ string) (driver.Stmt, error) { return &scanErrStmt{}, nil }
func (c *scanErrConn) Close() error                          { return nil }
func (c *scanErrConn) Begin() (driver.Tx, error)             { return nil, nil }

type scanErrStmt struct{}

func (s *scanErrStmt) Close() error                                 { return nil }
func (s *scanErrStmt) NumInput() int                                { return -1 }
func (s *scanErrStmt) Exec(_ []driver.Value) (driver.Result, error) { return nil, nil }
func (s *scanErrStmt) Query(_ []driver.Value) (driver.Rows, error)  { return &scanErrRows{}, nil }

// scanErrRows returns one row but Columns() advertises only 1 column;
// Scan(&triggerType, &config) with 2 destinations fails.
type scanErrRows struct{ done bool }

func (r *scanErrRows) Columns() []string { return []string{"v"} }
func (r *scanErrRows) Close() error      { return nil }
func (r *scanErrRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = "trigger-type"
	return nil
}

// triggerRowsErrDB: probe succeeds; QueryContext returns rows whose Next() returns a
// non-EOF error, so rows.Next() returns false and rows.Err() returns the error.
type triggerRowsErrDB struct{}

func (d *triggerRowsErrDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return nil, nil
}
func (d *triggerRowsErrDB) QueryContext(ctx context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return sql.OpenDB(&rowsErrConnector{}).QueryContext(ctx, "SELECT v1, v2")
}
func (d *triggerRowsErrDB) QueryRowContext(ctx context.Context, query string, _ ...any) *sql.Row {
	if strings.HasPrefix(query, "SELECT COUNT(*) FROM sqlite_master") {
		return sql.OpenDB(&guardRowConn{val: int64(0)}).QueryRowContext(ctx, "SELECT v")
	}
	return sql.OpenDB(&guardRowConn{noRow: true}).QueryRowContext(ctx, "SELECT v")
}

// rowsErrConnector returns a driver whose rows.Next() yields a non-EOF error.
type rowsErrConnector struct{}

func (c *rowsErrConnector) Connect(_ context.Context) (driver.Conn, error) {
	return &rowsErrDriverConn{}, nil
}
func (c *rowsErrConnector) Driver() driver.Driver { return dummyDriver{} }

type rowsErrDriverConn struct{}

func (c *rowsErrDriverConn) Prepare(_ string) (driver.Stmt, error) { return &rowsErrStmt{}, nil }
func (c *rowsErrDriverConn) Close() error                          { return nil }
func (c *rowsErrDriverConn) Begin() (driver.Tx, error)             { return nil, nil }

type rowsErrStmt struct{}

func (s *rowsErrStmt) Close() error                                 { return nil }
func (s *rowsErrStmt) NumInput() int                                { return -1 }
func (s *rowsErrStmt) Exec(_ []driver.Value) (driver.Result, error) { return nil, nil }
func (s *rowsErrStmt) Query(_ []driver.Value) (driver.Rows, error)  { return &rowsErrDriverRows{}, nil }

type rowsErrDriverRows struct{}

func (r *rowsErrDriverRows) Columns() []string { return []string{"v1", "v2"} }
func (r *rowsErrDriverRows) Close() error      { return nil }
func (r *rowsErrDriverRows) Next(_ []driver.Value) error {
	return errors.New("rows iteration error")
}

// — SetStatus integration test for fireAsyncTriggers ———————————————————————

func TestSetStatus_firesAsyncTrigger(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := CreateBlockTables(db); err != nil {
		t.Fatalf("CreateBlockTables: %v", err)
	}
	setupTriggerFlow(t, db, "async", "create-LifecycleEvent")

	repo := &DynamicTypeRepo{db: db, typeName: "testPost"}
	node, err := repo.CreateDraft(ctx, map[string]any{"title": "Trigger Test"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}

	prev := slog.Default()
	t.Cleanup(func() { restoreDefaultLogging(prev) })
	var buf safeBuf
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))

	if err := repo.SetStatus(ctx, node.ID, Published); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	if !strings.Contains(buf.String(), "fireAsyncTriggers dispatch") {
		t.Errorf("SetStatus: want async trigger dispatch log, got:\n%s", buf.String())
	}
}

// — A240: async triggers for typed orchestration modules ————————————————————
//
// Every test below drives a real typed-module transition path
// (updateHandler over real HTTP, or a real MCP*/processScheduled method
// call) — never fireAsyncTriggers directly. That distinction is the whole
// point: before A240, fireAsyncTriggers had exactly two call sites, both
// in dynamic.go, and no typed module (Signal/Task/Decision/Amendment/
// Goal/Run) ever reached it through any of its own five real transition
// sites (updateHandler, MCPPublish, MCPSchedule, MCPArchive,
// processScheduled).

// setupTriggerFlowTransition registers a minimal StateFlow for "testPost"
// with a single (from, to) transition carrying one async trigger — like
// setupTriggerFlow but for an arbitrary transition pair. Needed because
// MCPSchedule/MCPArchive/processScheduled don't share updateHandler/
// MCPPublish's draft->published shape, and the self-transition test below
// needs a from==to pair setupTriggerFlow can't express.
func setupTriggerFlowTransition(t *testing.T, db DB, triggerClass, triggerType, from, to string) {
	t.Helper()
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}
	states := []State{{Name: from, IsInitial: true}}
	if to != from {
		states = append(states, State{Name: to})
	}
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{
		Name:        "trigger-test-" + from + "-" + to,
		TypeName:    "testPost",
		States:      states,
		Transitions: []Transition{{From: from, To: to}},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	var transID string
	if err := db.QueryRowContext(ctx, `
		SELECT t.id FROM smeldr_transitions t
		JOIN smeldr_state_flows f ON t.flow_id = f.id
		WHERE f.type_name = 'testPost' AND t.from_state = $1 AND t.to_state = $2
	`, from, to).Scan(&transID); err != nil {
		t.Fatalf("get transition id: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_transition_triggers (id, transition_id, trigger_class, trigger_type, config) VALUES (?, ?, ?, ?, ?)`,
		NewID(), transID, triggerClass, triggerType, `{}`,
	); err != nil {
		t.Fatalf("insert trigger: %v", err)
	}
}

// captureAsyncTriggerLog swaps slog's default handler for the duration of
// the test and returns the buffer it writes to. Matches
// TestSetStatus_firesAsyncTrigger's own precedent exactly.
func captureAsyncTriggerLog(t *testing.T) *safeBuf {
	t.Helper()
	prev := slog.Default()
	t.Cleanup(func() { restoreDefaultLogging(prev) })
	var buf safeBuf
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	return &buf
}

func TestModule_updateHandler_firesAsyncTrigger(t *testing.T) {
	sqlDB := newSQLiteDB(t)
	setupTriggerFlow(t, sqlDB, "async", "create-LifecycleEvent")

	mem := NewMemoryRepo[*testPost]()
	p := seedPost(t, mem, "Test Post", Draft)

	m := newTestModule(mem)
	m.setDB(sqlDB)

	buf := captureAsyncTriggerLog(t)

	update := map[string]any{"Title": p.Title, "Status": string(Published)}
	body, _ := json.Marshal(update)
	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodPut, "/testposts/"+p.Slug, bytes.NewReader(body)), editorUser())
	r.SetPathValue("slug", p.Slug)
	m.updateHandler(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody: %s", w.Code, w.Body.String())
	}
	time.Sleep(50 * time.Millisecond)
	if !strings.Contains(buf.String(), "fireAsyncTriggers dispatch") {
		t.Errorf("updateHandler draft->published: want async trigger dispatch log, got:\n%s", buf.String())
	}
}

// TestModule_updateHandler_noTriggerOnSameStatus pins the asymmetry
// flagged during review: updateHandler's fireAsyncTriggers call is
// guarded by prevStatus != newStatus (unlike the three MCP lifecycle
// methods, proven unconditional in
// TestModule_MCPPublish_firesAsyncTriggerEvenOnSameStatus below). The
// trigger is deliberately registered on draft->draft itself (not a
// different transition) — a trigger registered elsewhere would make this
// test pass whether or not the guard exists, since fireAsyncTriggers'
// own query would find no matching row either way. Only a same-status
// trigger that actually exists can prove the guard, not just the
// mechanism's own natural "nothing matches," is what's stopping it.
func TestModule_updateHandler_noTriggerOnSameStatus(t *testing.T) {
	sqlDB := newSQLiteDB(t)
	setupTriggerFlowTransition(t, sqlDB, "async", "create-LifecycleEvent", "draft", "draft")

	mem := NewMemoryRepo[*testPost]()
	p := seedPost(t, mem, "Test Post", Draft)

	m := newTestModule(mem)
	m.setDB(sqlDB)

	buf := captureAsyncTriggerLog(t)

	// Same status (draft -> draft): only the title changes.
	update := map[string]any{"Title": "Updated Title", "Status": string(Draft)}
	body, _ := json.Marshal(update)
	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodPut, "/testposts/"+p.Slug, bytes.NewReader(body)), editorUser())
	r.SetPathValue("slug", p.Slug)
	m.updateHandler(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody: %s", w.Code, w.Body.String())
	}
	time.Sleep(50 * time.Millisecond)
	if strings.Contains(buf.String(), "fireAsyncTriggers dispatch") {
		t.Errorf("updateHandler with unchanged status: want NO async trigger dispatch, got:\n%s", buf.String())
	}
}

func TestModule_MCPPublish_firesAsyncTrigger(t *testing.T) {
	sqlDB := newSQLiteDB(t)
	setupTriggerFlow(t, sqlDB, "async", "create-LifecycleEvent")

	mem := NewMemoryRepo[*testPost]()
	p := seedPost(t, mem, "Test Post", Draft)

	m := newTestModule(mem)
	m.setDB(sqlDB)

	buf := captureAsyncTriggerLog(t)

	ctx := NewTestContext(editorUser())
	if err := m.MCPPublish(ctx, p.Slug, ""); err != nil {
		t.Fatalf("MCPPublish: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if !strings.Contains(buf.String(), "fireAsyncTriggers dispatch") {
		t.Errorf("MCPPublish draft->published: want async trigger dispatch log, got:\n%s", buf.String())
	}
}

// TestModule_MCPPublish_firesAsyncTriggerEvenOnSameStatus is the other
// half of the asymmetry pin: MCPPublish's fireAsyncTriggers call is
// unconditional, unlike updateHandler's guard above. Republishing an
// already-Published item is a real, allowed self-transition
// (validateTransition's own fromStatus==toStatus early-return) — with a
// trigger registered specifically on published->published, it must still
// fire.
func TestModule_MCPPublish_firesAsyncTriggerEvenOnSameStatus(t *testing.T) {
	sqlDB := newSQLiteDB(t)
	setupTriggerFlowTransition(t, sqlDB, "async", "create-LifecycleEvent", "published", "published")

	mem := NewMemoryRepo[*testPost]()
	p := seedPost(t, mem, "Test Post", Published)

	m := newTestModule(mem)
	m.setDB(sqlDB)

	buf := captureAsyncTriggerLog(t)

	ctx := NewTestContext(editorUser())
	if err := m.MCPPublish(ctx, p.Slug, ""); err != nil {
		t.Fatalf("MCPPublish (republish): %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if !strings.Contains(buf.String(), "fireAsyncTriggers dispatch") {
		t.Errorf("MCPPublish on an already-Published item: want async trigger dispatch (MCP sites call fireAsyncTriggers unconditionally, unlike updateHandler's guard), got:\n%s", buf.String())
	}
}

func TestModule_MCPSchedule_firesAsyncTrigger(t *testing.T) {
	sqlDB := newSQLiteDB(t)
	setupTriggerFlowTransition(t, sqlDB, "async", "create-LifecycleEvent", "draft", "scheduled")

	mem := NewMemoryRepo[*testPost]()
	p := seedPost(t, mem, "Test Post", Draft)

	m := newTestModule(mem)
	m.setDB(sqlDB)

	buf := captureAsyncTriggerLog(t)

	ctx := NewTestContext(editorUser())
	if err := m.MCPSchedule(ctx, p.Slug, time.Now().Add(time.Hour), ""); err != nil {
		t.Fatalf("MCPSchedule: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if !strings.Contains(buf.String(), "fireAsyncTriggers dispatch") {
		t.Errorf("MCPSchedule draft->scheduled: want async trigger dispatch log, got:\n%s", buf.String())
	}
}

func TestModule_MCPArchive_firesAsyncTrigger(t *testing.T) {
	sqlDB := newSQLiteDB(t)
	setupTriggerFlowTransition(t, sqlDB, "async", "create-LifecycleEvent", "draft", "archived")

	mem := NewMemoryRepo[*testPost]()
	p := seedPost(t, mem, "Test Post", Draft)

	m := newTestModule(mem)
	m.setDB(sqlDB)

	buf := captureAsyncTriggerLog(t)

	ctx := NewTestContext(editorUser())
	if err := m.MCPArchive(ctx, p.Slug, ""); err != nil {
		t.Fatalf("MCPArchive: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if !strings.Contains(buf.String(), "fireAsyncTriggers dispatch") {
		t.Errorf("MCPArchive draft->archived: want async trigger dispatch log, got:\n%s", buf.String())
	}
}

func TestModule_processScheduled_firesAsyncTrigger(t *testing.T) {
	sqlDB := newSQLiteDB(t)
	setupTriggerFlowTransition(t, sqlDB, "async", "create-LifecycleEvent", "scheduled", "published")

	mem := NewMemoryRepo[*testPost]()
	past := time.Now().UTC().Add(-1 * time.Minute)
	p := &testPost{Node: Node{ID: NewID(), Slug: "overdue", Status: Scheduled, ScheduledAt: &past}, Title: "Overdue"}
	if err := mem.Save(context.Background(), p); err != nil {
		t.Fatalf("Save: %v", err)
	}

	m := newTestModule(mem)
	m.setDB(sqlDB)

	buf := captureAsyncTriggerLog(t)

	ctx := NewBackgroundContext("example.com")
	published, _, err := m.processScheduled(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("processScheduled: %v", err)
	}
	if published != 1 {
		t.Fatalf("published = %d, want 1", published)
	}
	time.Sleep(50 * time.Millisecond)
	if !strings.Contains(buf.String(), "fireAsyncTriggers dispatch") {
		t.Errorf("processScheduled scheduled->published: want async trigger dispatch log, got:\n%s", buf.String())
	}
}

// safeBuf is a goroutine-safe bytes.Buffer for slog handlers in tests that spawn goroutines.
// bytes.Buffer is not safe for concurrent use; the race detector flags slog writes from
// async goroutines against reads in the test goroutine.
type safeBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuf) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// ——— helpers for ConflictPolicy tests ————————————————————————————————————————

// newMigratedDB creates a SQLite DB with migrateStateFlows applied.
func newMigratedDB(t *testing.T) *sql.DB {
	t.Helper()
	db := newSQLiteDB(t)
	if err := migrateStateFlows(context.Background(), db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}
	return db
}

// registerConflictFlow registers a flow with the given ConflictPolicy and
// ActiveState. Creates the typed table (type_items) if it does not exist.
func registerConflictFlow(t *testing.T, db *sql.DB, policy ConflictPolicy) {
	t.Helper()
	ctx := context.Background()
	// camelToSnake("ConflictType")+"s" = "conflict_types"
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS conflict_types (id TEXT PRIMARY KEY, status TEXT NOT NULL, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
	); err != nil {
		t.Fatalf("create conflict_types: %v", err)
	}
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{
		Name:           "conflict-flow",
		TypeName:       "ConflictType",
		ActiveState:    "published",
		ConflictPolicy: policy,
		States: []State{
			{Name: "draft", IsInitial: true},
			{Name: "published"},
			{Name: "superseded"},
			{Name: "archived", IsTerminal: true},
		},
		Transitions: []Transition{
			{From: "draft", To: "published"},
			{From: "published", To: "superseded"},
			{From: "published", To: "archived"},
		},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
}

// insertConflictItem inserts a row into conflict_types with the given status.
func insertConflictItem(t *testing.T, db *sql.DB, id, status string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO conflict_types (id, status) VALUES (?, ?)`, id, status,
	); err != nil {
		t.Fatalf("insertConflictItem: %v", err)
	}
}

// ——— migrateStateFlowConflictColumns ————————————————————————————————————————

// — migrateTransitionReasonColumn tests (T149) ———————————————————————————————

func TestMigrateTransitionReasonColumn_addsColumn(t *testing.T) {
	// Simulate a pre-T149 DB: create smeldr_transitions WITHOUT required_reason,
	// then call migrateTransitionReasonColumn directly.
	db := newSQLiteDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE smeldr_transitions (
			id INTEGER PRIMARY KEY,
			flow_id TEXT NOT NULL,
			from_state TEXT NOT NULL,
			to_state TEXT NOT NULL,
			required_role TEXT
		)`,
	); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if err := migrateTransitionReasonColumn(ctx, db); err != nil {
		t.Fatalf("migrateTransitionReasonColumn: %v", err)
	}
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(smeldr_transitions)")
	if err != nil {
		t.Fatalf("PRAGMA: %v", err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var dflt *string
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			continue
		}
		if name == "required_reason" {
			found = true
		}
	}
	if !found {
		t.Error("required_reason column missing after migration")
	}
}

func TestMigrateTransitionReasonColumn_idempotent(t *testing.T) {
	db := newMigratedDB(t) // migrateStateFlows already adds the column
	ctx := context.Background()
	if err := migrateTransitionReasonColumn(ctx, db); err != nil {
		t.Fatalf("second call: %v", err)
	}
}

func TestMigrateTransitionReasonColumn_nonSQLite(t *testing.T) {
	db := &queryFailDB{}
	if err := migrateTransitionReasonColumn(context.Background(), db); err != nil {
		t.Fatalf("non-SQLite: expected nil, got %v", err)
	}
}

func TestMigrateTransitionReasonColumn_alterFail(t *testing.T) {
	// Empty SQLite DB with no smeldr_transitions table: PRAGMA returns empty
	// rows (no error), column absent, then ALTER TABLE fails (no such table).
	db := newSQLiteDB(t)
	if err := migrateTransitionReasonColumn(context.Background(), db); err == nil {
		t.Error("expected error when ALTER TABLE has no target table, got nil")
	}
}

// — migrateTransitionStrictColumn tests (D34) ————————————————————————————————

func TestMigrateTransitionStrictColumn_addsColumn(t *testing.T) {
	// Simulate a pre-D34 DB: create smeldr_transitions WITHOUT strict,
	// then call migrateTransitionStrictColumn directly.
	db := newSQLiteDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE smeldr_transitions (
			id INTEGER PRIMARY KEY,
			flow_id TEXT NOT NULL,
			from_state TEXT NOT NULL,
			to_state TEXT NOT NULL,
			required_role TEXT,
			required_reason BOOLEAN NOT NULL DEFAULT FALSE
		)`,
	); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if err := migrateTransitionStrictColumn(ctx, db); err != nil {
		t.Fatalf("migrateTransitionStrictColumn: %v", err)
	}
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(smeldr_transitions)")
	if err != nil {
		t.Fatalf("PRAGMA: %v", err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var dflt *string
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			continue
		}
		if name == "strict" {
			found = true
		}
	}
	if !found {
		t.Error("strict column missing after migration")
	}
}

func TestMigrateTransitionStrictColumn_idempotent(t *testing.T) {
	db := newMigratedDB(t) // migrateStateFlows already adds the column
	ctx := context.Background()
	if err := migrateTransitionStrictColumn(ctx, db); err != nil {
		t.Fatalf("second call: %v", err)
	}
}

func TestMigrateTransitionStrictColumn_nonSQLite(t *testing.T) {
	db := &queryFailDB{}
	if err := migrateTransitionStrictColumn(context.Background(), db); err != nil {
		t.Fatalf("non-SQLite: expected nil, got %v", err)
	}
}

func TestMigrateTransitionStrictColumn_alterFail(t *testing.T) {
	// Empty SQLite DB with no smeldr_transitions table: PRAGMA returns empty
	// rows (no error), column absent, then ALTER TABLE fails (no such table).
	db := newSQLiteDB(t)
	if err := migrateTransitionStrictColumn(context.Background(), db); err == nil {
		t.Error("expected error when ALTER TABLE has no target table, got nil")
	}
}

func TestMigrateStateFlowConflictColumns_addsColumns(t *testing.T) {
	// Simulate a pre-v1.46.0 DB: create smeldr_state_flows WITHOUT the new columns,
	// then call migrateStateFlowConflictColumns directly.
	db := newSQLiteDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE smeldr_state_flows (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			type_name TEXT,
			description TEXT,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
	); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if err := migrateStateFlowConflictColumns(ctx, db); err != nil {
		t.Fatalf("migrateStateFlowConflictColumns: %v", err)
	}
	// Verify both columns present.
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(smeldr_state_flows)")
	if err != nil {
		t.Fatalf("PRAGMA: %v", err)
	}
	defer rows.Close()
	found := map[string]bool{}
	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var dflt *string
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			continue
		}
		found[name] = true
	}
	if !found["active_state"] {
		t.Error("active_state column missing after migration")
	}
	if !found["conflict_policy"] {
		t.Error("conflict_policy column missing after migration")
	}
}

func TestMigrateStateFlowConflictColumns_idempotent(t *testing.T) {
	db := newMigratedDB(t) // migrateStateFlows already adds the columns
	ctx := context.Background()
	// Second call should be a no-op.
	if err := migrateStateFlowConflictColumns(ctx, db); err != nil {
		t.Fatalf("second call: %v", err)
	}
}

func TestMigrateStateFlowConflictColumns_nonSQLite(t *testing.T) {
	// A DB whose QueryContext always fails simulates a non-SQLite driver.
	db := &queryFailDB{}
	if err := migrateStateFlowConflictColumns(context.Background(), db); err != nil {
		t.Fatalf("non-SQLite: expected nil, got %v", err)
	}
}

func TestMigrateStateFlowConflictColumns_alterFail(t *testing.T) {
	// Empty SQLite DB with no tables: PRAGMA returns empty rows (no error),
	// both columns absent, then ALTER TABLE fails (no such table) → returns error.
	db := newSQLiteDB(t)
	err := migrateStateFlowConflictColumns(context.Background(), db)
	if err == nil {
		t.Error("expected error when ALTER TABLE has no target table, got nil")
	}
}

func TestMigrateStateFlowConflictColumns_alterConflictPolicyFail(t *testing.T) {
	// Table with active_state already present but without conflict_policy.
	// conflictExecFailDB makes ExecContext fail → ALTER for conflict_policy fails → line 139-141.
	db := newSQLiteDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE smeldr_state_flows (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			type_name TEXT,
			description TEXT,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			active_state TEXT NOT NULL DEFAULT ''
		)`,
	); err != nil {
		t.Fatalf("create table: %v", err)
	}
	// Wrap: PRAGMA (QueryContext) goes through real DB; ALTER (ExecContext) always fails.
	wrapped := &conflictExecFailDB{DB: db}
	err := migrateStateFlowConflictColumns(ctx, wrapped)
	if err == nil {
		t.Error("expected error when ALTER TABLE conflict_policy fails, got nil")
	}
}

// queryFailDB fails every QueryContext (simulates non-SQLite for PRAGMA tests).
type queryFailDB struct{}

func (d *queryFailDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return nil, nil
}
func (d *queryFailDB) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, errors.New("not SQLite")
}
func (d *queryFailDB) QueryRowContext(ctx context.Context, _ string, _ ...any) *sql.Row {
	row := (*sql.Row)(nil)
	_ = row
	// Return a *sql.Row that always returns an error by querying a non-existent table.
	db, _ := sql.Open("sqlite", ":memory:")
	if db != nil {
		r := db.QueryRowContext(ctx, "SELECT 1 FROM nonexistent_table_xyzzy")
		return r
	}
	return nil
}

// ——— RegisterFlow — ConflictPolicy persistence ——————————————————————————————

func TestRegisterFlow_conflictPolicyStored(t *testing.T) {
	db := newMigratedDB(t)
	app := &App{cfg: Config{DB: db}}
	flow := StateFlow{
		Name:           "cp-flow",
		TypeName:       "CPItem",
		ActiveState:    "published",
		ConflictPolicy: ConflictReject,
		States:         []State{{Name: "draft", IsInitial: true}, {Name: "published"}},
		Transitions:    []Transition{{From: "draft", To: "published"}},
	}
	if err := app.RegisterFlow(flow); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	var activeState, conflictPolicy string
	if err := db.QueryRowContext(context.Background(),
		`SELECT active_state, conflict_policy FROM smeldr_state_flows WHERE name = ?`, "cp-flow",
	).Scan(&activeState, &conflictPolicy); err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if activeState != "published" {
		t.Errorf("active_state: got %q, want %q", activeState, "published")
	}
	if conflictPolicy != string(ConflictReject) {
		t.Errorf("conflict_policy: got %q, want %q", conflictPolicy, ConflictReject)
	}
}

// ——— applyConflictPolicy — guard paths ————————————————————————————————————

func TestApplyConflictPolicy_nilDB(t *testing.T) {
	if err := applyConflictPolicy(context.Background(), nil, nil, "T", "published", "id1"); err != nil {
		t.Errorf("nil DB: expected nil, got %v", err)
	}
}

func TestApplyConflictPolicy_noFlow(t *testing.T) {
	db := newMigratedDB(t)
	// No flow registered for "UnknownType" — should return nil.
	if err := applyConflictPolicy(context.Background(), db, nil, "UnknownType", "published", "id1"); err != nil {
		t.Errorf("no flow: expected nil, got %v", err)
	}
}

func TestApplyConflictPolicy_emptyActiveState(t *testing.T) {
	db := newMigratedDB(t)
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{
		Name:     "no-active-flow",
		TypeName: "NoActiveType",
		States:   []State{{Name: "draft", IsInitial: true}},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	// ActiveState="" → no enforcement.
	if err := applyConflictPolicy(context.Background(), db, nil, "NoActiveType", "draft", "id1"); err != nil {
		t.Errorf("empty active_state: expected nil, got %v", err)
	}
}

func TestApplyConflictPolicy_toStateNotActiveState(t *testing.T) {
	db := newMigratedDB(t)
	registerConflictFlow(t, db, ConflictReject)
	// Transitioning to "archived", not to "published" (the active state).
	if err := applyConflictPolicy(context.Background(), db, nil, "ConflictType", "archived", "id1"); err != nil {
		t.Errorf("toState != activeState: expected nil, got %v", err)
	}
}

// ——— ConflictReject ————————————————————————————————————————————————————————

func TestApplyConflictPolicy_reject_noConflict(t *testing.T) {
	db := newMigratedDB(t)
	registerConflictFlow(t, db, ConflictReject)
	// No items in "published" state → no conflict.
	if err := applyConflictPolicy(context.Background(), db, nil, "ConflictType", "published", "new-id"); err != nil {
		t.Errorf("no conflict: expected nil, got %v", err)
	}
}

func TestApplyConflictPolicy_reject_conflict(t *testing.T) {
	db := newMigratedDB(t)
	registerConflictFlow(t, db, ConflictReject)
	insertConflictItem(t, db, "existing", "published")
	err := applyConflictPolicy(context.Background(), db, nil, "ConflictType", "published", "new-id")
	if !errors.Is(err, ErrConflict) {
		t.Errorf("conflict: expected ErrConflict, got %v", err)
	}
}

// TestApplyConflictPolicy_smeldrPrefixedTable pins T229: applyConflictPolicy
// previously probed only <snake>s (e.g. "tasks"), never smeldr_<snake>s (e.g.
// "smeldr_tasks") — the table every orchestration type actually uses. A
// missed probe silently fell through to isDynamic=true, checking conflicts
// against smeldr_dynamic_content instead of the type's own real table —
// harmless while no orchestration type used a conflict policy, but silently
// wrong the day one does. Uses "Task"/"smeldr_tasks" directly (not the
// registerConflictFlow fixture, whose own "conflict_types" bare-form table
// happened to already match the pre-fix probe and would not have caught
// this regression).
func TestApplyConflictPolicy_smeldrPrefixedTable(t *testing.T) {
	db := newMigratedDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS smeldr_tasks (id TEXT PRIMARY KEY, status TEXT NOT NULL, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
	); err != nil {
		t.Fatalf("create smeldr_tasks: %v", err)
	}
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{
		Name:           "task-conflict-flow",
		TypeName:       "Task",
		ActiveState:    "published",
		ConflictPolicy: ConflictReject,
		States: []State{
			{Name: "draft", IsInitial: true},
			{Name: "published"},
		},
		Transitions: []Transition{
			{From: "draft", To: "published"},
		},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_tasks (id, status) VALUES (?, ?)`, "existing-task", "published",
	); err != nil {
		t.Fatalf("insert existing task: %v", err)
	}
	err := applyConflictPolicy(ctx, db, nil, "Task", "published", "new-task")
	if !errors.Is(err, ErrConflict) {
		t.Errorf("smeldr_-prefixed table: want ErrConflict (real conflict against smeldr_tasks), got %v", err)
	}
}

func TestApplyConflictPolicy_reject_dbError(t *testing.T) {
	// Mock DB whose QueryContext fails — simulates DB error on COUNT query.
	// The PRAGMA probe must succeed first (SQLite identity check).
	db := newMigratedDB(t)
	registerConflictFlow(t, db, ConflictReject)
	// Use a wrapped DB that fails QueryContext after the sqlite_master probe.
	wrapped := &countFailAfterProbeDB{DB: db}
	if err := applyConflictPolicy(context.Background(), wrapped, nil, "ConflictType", "published", "id1"); err != nil {
		t.Errorf("db error: expected nil (fail-open), got %v", err)
	}
}

// countFailAfterProbeDB wraps a real DB and makes QueryContext fail on calls
// after the sqlite_master probe (i.e. on actual content queries).
type countFailAfterProbeDB struct {
	DB     DB
	probed bool
}

func (d *countFailAfterProbeDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return d.DB.ExecContext(ctx, q, args...)
}
func (d *countFailAfterProbeDB) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	return d.DB.QueryRowContext(ctx, q, args...)
}
func (d *countFailAfterProbeDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if !d.probed {
		d.probed = true
		return d.DB.QueryContext(ctx, q, args...)
	}
	return nil, errors.New("simulated count query error")
}

// ——— ConflictSupersede ——————————————————————————————————————————————————————

func TestApplyConflictPolicy_supersede_noConflict(t *testing.T) {
	db := newMigratedDB(t)
	registerConflictFlow(t, db, ConflictSupersede)
	// No items in "published" → supersede is a no-op.
	if err := applyConflictPolicy(context.Background(), db, nil, "ConflictType", "published", "new-id"); err != nil {
		t.Errorf("no conflict: expected nil, got %v", err)
	}
}

func TestApplyConflictPolicy_supersede_happyPath(t *testing.T) {
	db := newMigratedDB(t)
	registerConflictFlow(t, db, ConflictSupersede)
	insertConflictItem(t, db, "old-item", "published")

	if err := applyConflictPolicy(context.Background(), db, nil, "ConflictType", "published", "new-id"); err != nil {
		t.Fatalf("supersede: unexpected error: %v", err)
	}

	var status string
	if err := db.QueryRowContext(context.Background(),
		`SELECT status FROM conflict_types WHERE id = ?`, "old-item",
	).Scan(&status); err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if status != "superseded" {
		t.Errorf("supersede: old item status = %q, want %q", status, "superseded")
	}
}

func TestApplyConflictPolicy_supersede_noSupersededTransition(t *testing.T) {
	// Register a flow with ConflictSupersede but WITHOUT a published→superseded transition.
	db := newMigratedDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS no_super_types (id TEXT PRIMARY KEY, status TEXT NOT NULL, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
	); err != nil {
		t.Fatalf("create table: %v", err)
	}
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{
		Name:           "no-super-flow",
		TypeName:       "NoSuperType",
		ActiveState:    "published",
		ConflictPolicy: ConflictSupersede,
		States:         []State{{Name: "draft", IsInitial: true}, {Name: "published"}},
		Transitions:    []Transition{{From: "draft", To: "published"}},
		// Note: no published→superseded transition.
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	// No items in published → fallback to reject, but count=0 → nil.
	if err := applyConflictPolicy(ctx, db, nil, "NoSuperType", "published", "id1"); err != nil {
		t.Errorf("no super transition, no conflict: expected nil, got %v", err)
	}
}

func TestApplyConflictPolicy_supersede_noSupersededTransition_conflict(t *testing.T) {
	db := newMigratedDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS no_super_types (id TEXT PRIMARY KEY, status TEXT NOT NULL, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
	); err != nil {
		t.Fatalf("create table: %v", err)
	}
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{
		Name:           "no-super-flow",
		TypeName:       "NoSuperType",
		ActiveState:    "published",
		ConflictPolicy: ConflictSupersede,
		States:         []State{{Name: "draft", IsInitial: true}, {Name: "published"}},
		Transitions:    []Transition{{From: "draft", To: "published"}},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	// Insert a conflicting item.
	if _, err := db.ExecContext(ctx, `INSERT INTO no_super_types (id, status) VALUES ('old', 'published')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// No superseded transition → falls back to reject → ErrConflict.
	err := applyConflictPolicy(ctx, db, nil, "NoSuperType", "published", "id1")
	if !errors.Is(err, ErrConflict) {
		t.Errorf("fallback to reject: expected ErrConflict, got %v", err)
	}
}

func TestApplyConflictPolicy_supersede_nilRelationStore(t *testing.T) {
	db := newMigratedDB(t)
	registerConflictFlow(t, db, ConflictSupersede)
	insertConflictItem(t, db, "old", "published")
	// nil RelationStore → no panic, item still superseded.
	if err := applyConflictPolicy(context.Background(), db, nil, "ConflictType", "published", "new"); err != nil {
		t.Fatalf("nil rs: unexpected error: %v", err)
	}
	var status string
	if err := db.QueryRowContext(context.Background(),
		`SELECT status FROM conflict_types WHERE id = ?`, "old",
	).Scan(&status); err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if status != "superseded" {
		t.Errorf("nil rs: old item status = %q, want %q", status, "superseded")
	}
}

// ——— Dynamic content path —————————————————————————————————————————————————

func TestApplyConflictPolicy_dynamic_reject(t *testing.T) {
	db := newMigratedDB(t)
	ctx := context.Background()
	// Create dynamic content table.
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS smeldr_dynamic_content (id TEXT PRIMARY KEY, type_name TEXT NOT NULL, status TEXT NOT NULL, published_at DATETIME, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, slug TEXT)`,
	); err != nil {
		t.Fatalf("create smeldr_dynamic_content: %v", err)
	}
	app := &App{cfg: Config{DB: db}}
	// Register flow for a type whose typed table does NOT exist → falls back to smeldr_dynamic_content.
	if err := app.RegisterFlow(StateFlow{
		Name:           "dyn-reject-flow",
		TypeName:       "DynRejectType",
		ActiveState:    "published",
		ConflictPolicy: ConflictReject,
		States:         []State{{Name: "draft", IsInitial: true}, {Name: "published"}},
		Transitions:    []Transition{{From: "draft", To: "published"}},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	// Insert a conflicting dynamic item.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_dynamic_content (id, type_name, status, updated_at, slug) VALUES ('dyn1', 'DynRejectType', 'published', CURRENT_TIMESTAMP, 'dyn1')`,
	); err != nil {
		t.Fatalf("insert dynamic: %v", err)
	}
	err := applyConflictPolicy(ctx, db, nil, "DynRejectType", "published", "new-dyn")
	if !errors.Is(err, ErrConflict) {
		t.Errorf("dynamic reject: expected ErrConflict, got %v", err)
	}
}

func TestApplyConflictPolicy_dynamic_supersede(t *testing.T) {
	db := newMigratedDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS smeldr_dynamic_content (id TEXT PRIMARY KEY, type_name TEXT NOT NULL, status TEXT NOT NULL, published_at DATETIME, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, slug TEXT)`,
	); err != nil {
		t.Fatalf("create smeldr_dynamic_content: %v", err)
	}
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{
		Name:           "dyn-super-flow",
		TypeName:       "DynSuperType",
		ActiveState:    "published",
		ConflictPolicy: ConflictSupersede,
		States:         []State{{Name: "draft", IsInitial: true}, {Name: "published"}, {Name: "superseded"}},
		Transitions: []Transition{
			{From: "draft", To: "published"},
			{From: "published", To: "superseded"},
		},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_dynamic_content (id, type_name, status, updated_at, slug) VALUES ('dyn-old', 'DynSuperType', 'published', CURRENT_TIMESTAMP, 'dyn-old')`,
	); err != nil {
		t.Fatalf("insert dynamic: %v", err)
	}
	if err := applyConflictPolicy(ctx, db, nil, "DynSuperType", "published", "dyn-new"); err != nil {
		t.Fatalf("dynamic supersede: unexpected error: %v", err)
	}
	var status string
	if err := db.QueryRowContext(ctx,
		`SELECT status FROM smeldr_dynamic_content WHERE id = ?`, "dyn-old",
	).Scan(&status); err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if status != "superseded" {
		t.Errorf("dynamic supersede: old item status = %q, want %q", status, "superseded")
	}
}

// ——— applyConflictPolicy — non-SQLite path ————————————————————————————————

func TestApplyConflictPolicy_nonSQLite(t *testing.T) {
	// queryFailDB: QueryRowContext queries a nonexistent table → scan returns error
	// → sqlite_master probe fails → return nil (not SQLite).
	if err := applyConflictPolicy(context.Background(), &queryFailDB{}, nil, "T", "published", "id1"); err != nil {
		t.Errorf("non-SQLite: expected nil, got %v", err)
	}
}

func TestApplyConflictPolicy_unknownPolicy(t *testing.T) {
	// Insert a flow with a non-standard conflict_policy string ("custom-policy").
	// The switch in applyConflictPolicy has no matching case → falls to default return nil (line 406).
	db := newMigratedDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_state_flows (id, name, type_name, active_state, conflict_policy)
		 VALUES (?, 'unknown-policy-flow', 'UnknownPolicyType', 'published', 'custom-policy')`,
		NewID(),
	); err != nil {
		t.Fatalf("insert flow: %v", err)
	}
	if err := applyConflictPolicy(ctx, db, nil, "UnknownPolicyType", "published", "id1"); err != nil {
		t.Errorf("unknown policy: expected nil, got %v", err)
	}
}

// ——— applyConflictPolicy — ConflictSupersede flowID fail path ———————————

func TestApplyConflictPolicy_flowIDFail(t *testing.T) {
	// The 4th QueryRowContext call inside applyConflictPolicy (ConflictSupersede branch)
	// is the flowID lookup. Fail it to exercise the fail-open path at line 393-394.
	db := newMigratedDB(t)
	registerConflictFlow(t, db, ConflictSupersede)
	insertConflictItem(t, db, "existing", "published")
	wrapped := &nthQueryRowFailDB{DB: db, fail: 4}
	err := applyConflictPolicy(context.Background(), wrapped, nil, "ConflictType", "published", "new-id")
	if err != nil {
		t.Errorf("flowID fail: expected nil (fail-open), got %v", err)
	}
}

// nthQueryRowFailDB wraps a real DB and makes the nth QueryRowContext call return
// a scan error (by querying a nonexistent table on a fresh in-memory SQLite).
type nthQueryRowFailDB struct {
	DB   DB
	n    int
	fail int // 1-indexed
}

func (d *nthQueryRowFailDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return d.DB.ExecContext(ctx, q, args...)
}
func (d *nthQueryRowFailDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return d.DB.QueryContext(ctx, q, args...)
}
func (d *nthQueryRowFailDB) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	d.n++
	if d.n == d.fail {
		sdb, _ := sql.Open("sqlite", ":memory:")
		return sdb.QueryRowContext(ctx, "SELECT 1 FROM no_table_nthfail_xyz")
	}
	return d.DB.QueryRowContext(ctx, q, args...)
}

// ——— conflictRejectCheck — error path ————————————————————————————————————

func TestConflictRejectCheck_queryFail(t *testing.T) {
	// queryFailDB: QueryRowContext fails → err != nil → return nil (fail-open).
	err := conflictRejectCheck(context.Background(), &queryFailDB{}, "T", "published", "ts", false)
	if err != nil {
		t.Errorf("expected nil (fail-open on COUNT query error), got %v", err)
	}
}

// ——— conflictSupersede — error paths ——————————————————————————————————————

func TestConflictSupersede_conflictIDsFail(t *testing.T) {
	// queryFailDB: QueryContext fails → conflictIDs returns error → return nil (fail-open).
	err := conflictSupersede(context.Background(), &queryFailDB{}, nil, "T", "published", "new1", "ts", false)
	if err != nil {
		t.Errorf("expected nil (fail-open on conflictIDs error), got %v", err)
	}
}

func TestConflictSupersede_updateFail(t *testing.T) {
	// conflictExecFailDB: ExecContext always fails → UPDATE warns + continues → return nil.
	db := newMigratedDB(t)
	registerConflictFlow(t, db, ConflictSupersede)
	insertConflictItem(t, db, "old-upd", "published")
	wrapped := &conflictExecFailDB{DB: db}
	err := conflictSupersede(context.Background(), wrapped, nil, "ConflictType", "published", "new-upd", "conflict_types", false)
	if err != nil {
		t.Errorf("expected nil (fail-open on UPDATE error), got %v", err)
	}
}

// conflictExecFailDB wraps a real DB and makes every ExecContext call fail.
type conflictExecFailDB struct{ DB }

func (d *conflictExecFailDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return nil, errors.New("exec fail")
}

func TestConflictSupersede_rsNonNilAssertFail(t *testing.T) {
	// RelationStore with no kinds registered → Assert returns "unknown relation kind" →
	// warn + continue (fail-open). Item is still superseded by the UPDATE.
	db := newMigratedDB(t)
	registerConflictFlow(t, db, ConflictSupersede)
	insertConflictItem(t, db, "old-rs", "published")
	if err := CreateRelationTables(db); err != nil {
		t.Fatalf("CreateRelationTables: %v", err)
	}
	rs, err := NewRelationStore(db)
	if err != nil {
		t.Fatalf("NewRelationStore: %v", err)
	}
	if err := conflictSupersede(context.Background(), db, rs, "ConflictType", "published", "new-rs", "conflict_types", false); err != nil {
		t.Errorf("expected nil (fail-open on Assert error), got %v", err)
	}
	var status string
	if err := db.QueryRowContext(context.Background(),
		`SELECT status FROM conflict_types WHERE id = ?`, "old-rs",
	).Scan(&status); err != nil {
		t.Fatalf("SELECT status: %v", err)
	}
	if status != "superseded" {
		t.Errorf("rs Assert fail: old item status = %q, want superseded", status)
	}
}

// ——— validateFlowItems — non-SQLite and error paths ——————————————————————

func TestValidateFlowItems_nonSQLite(t *testing.T) {
	// queryFailDB: QueryRowContext always errors → sqlite_master probe fails → return nil.
	flow := StateFlow{Name: "test", TypeName: "TestType"}
	if err := validateFlowItems(context.Background(), &queryFailDB{}, flow); err != nil {
		t.Errorf("non-SQLite: expected nil, got %v", err)
	}
}

func TestValidateFlowItems_queryContextFail(t *testing.T) {
	// Set up a DB with the conflict_types table (so tableCount=1 and we reach QueryContext).
	// Then wrap it to make QueryContext always fail → returns error (line 226).
	db := newMigratedDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE conflict_types (id TEXT PRIMARY KEY, status TEXT NOT NULL)`,
	); err != nil {
		t.Fatalf("create table: %v", err)
	}
	wrapped := &alwaysFailQueryContextDB{DB: db}
	flow := StateFlow{
		Name:     "test-flow",
		TypeName: "ConflictType",
		States:   []State{{Name: "draft", IsInitial: true}, {Name: "published"}},
	}
	err := validateFlowItems(ctx, wrapped, flow)
	if err == nil {
		t.Error("expected error from QueryContext fail in validateFlowItems, got nil")
	}
}

// alwaysFailQueryContextDB wraps a real DB and makes every QueryContext call fail.
// QueryRowContext is delegated to the real DB so probes succeed.
type alwaysFailQueryContextDB struct{ DB }

func (d *alwaysFailQueryContextDB) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, errors.New("query context fail")
}

// ——— conflictIDs — scan error path ————————————————————————————————————————

func TestConflictIDs_scanError(t *testing.T) {
	// zeroColScanDB: QueryContext returns rows with 0 columns; Scan into &id (1 dest)
	// fails → slog.WarnContext + continue → returns (nil, nil).
	ids, err := conflictIDs(context.Background(), &zeroColQueryDB{}, "T", "published", "ts", false)
	if err != nil {
		t.Errorf("expected nil err (scan error is logged and skipped), got %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected empty ids slice, got %v", ids)
	}
}

// zeroColQueryDB: QueryContext returns rows with 0 columns so rows.Scan(&id) fails.
type zeroColQueryDB struct{}

func (d *zeroColQueryDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return nil, nil
}
func (d *zeroColQueryDB) QueryContext(ctx context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return sql.OpenDB(&zeroColConnector{}).QueryContext(ctx, "SELECT")
}
func (d *zeroColQueryDB) QueryRowContext(ctx context.Context, _ string, _ ...any) *sql.Row {
	return sql.OpenDB(&guardRowConn{noRow: true}).QueryRowContext(ctx, "SELECT v")
}

// zeroColConnector returns driver rows with 0 columns; Scan into any destination fails.
type zeroColConnector struct{}

func (c *zeroColConnector) Connect(_ context.Context) (driver.Conn, error) {
	return &zeroColConn{}, nil
}
func (c *zeroColConnector) Driver() driver.Driver { return dummyDriver{} }

type zeroColConn struct{}

func (c *zeroColConn) Prepare(_ string) (driver.Stmt, error) { return &zeroColStmt{}, nil }
func (c *zeroColConn) Close() error                          { return nil }
func (c *zeroColConn) Begin() (driver.Tx, error)             { return nil, nil }

type zeroColStmt struct{}

func (s *zeroColStmt) Close() error                                 { return nil }
func (s *zeroColStmt) NumInput() int                                { return -1 }
func (s *zeroColStmt) Exec(_ []driver.Value) (driver.Result, error) { return nil, nil }
func (s *zeroColStmt) Query(_ []driver.Value) (driver.Rows, error)  { return &zeroColRows{}, nil }

// zeroColRows returns one row with no columns; rows.Scan(&anything) fails with
// "expected 0 destination arguments in Scan, not N".
type zeroColRows struct{ done bool }

func (r *zeroColRows) Columns() []string { return []string{} }
func (r *zeroColRows) Close() error      { return nil }
func (r *zeroColRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	return nil
}

// ——— RegisterFlow — Triggers (TransitionTrigger) ——————————————————————————

func TestRegisterFlow_withTrigger(t *testing.T) {
	db := newMigratedDB(t)
	app := &App{cfg: Config{DB: db}}
	flow := StateFlow{
		Name:     "trigger-flow",
		TypeName: "TriggerItem",
		States:   []State{{Name: "draft", IsInitial: true}, {Name: "published"}},
		Transitions: []Transition{
			{From: "draft", To: "published"},
		},
		Triggers: []TransitionTrigger{
			{
				FromState:    "draft",
				ToState:      "published",
				TriggerClass: "async",
				TriggerType:  "schedule-eval",
				Config:       `{"eval_field":"next_eval_at","to_state":"pending"}`,
			},
		},
	}
	if err := app.RegisterFlow(flow); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	// Verify the trigger row exists.
	var count int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM smeldr_transition_triggers WHERE trigger_type = 'schedule-eval'`,
	).Scan(&count); err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if count == 0 {
		t.Error("expected trigger row in smeldr_transition_triggers, got 0")
	}
}

func TestRegisterFlow_triggerTransitionNotFound(t *testing.T) {
	db := newMigratedDB(t)
	app := &App{cfg: Config{DB: db}}
	flow := StateFlow{
		Name:     "bad-trigger-flow",
		TypeName: "BadTriggerItem",
		States:   []State{{Name: "draft", IsInitial: true}, {Name: "published"}},
		Transitions: []Transition{
			{From: "draft", To: "published"},
		},
		Triggers: []TransitionTrigger{
			{
				FromState:    "published", // no transition from published→archived
				ToState:      "archived",
				TriggerClass: "async",
				TriggerType:  "schedule-eval",
				Config:       `{}`,
			},
		},
	}
	err := app.RegisterFlow(flow)
	if err == nil {
		t.Error("expected error for trigger referencing non-existent transition, got nil")
	}
}

func TestRegisterFlow_triggerIdempotent(t *testing.T) {
	db := newMigratedDB(t)
	app := &App{cfg: Config{DB: db}}
	flow := StateFlow{
		Name:     "idem-trigger-flow",
		TypeName: "IdemTriggerItem",
		States:   []State{{Name: "draft", IsInitial: true}, {Name: "published"}},
		Transitions: []Transition{
			{From: "draft", To: "published"},
		},
		Triggers: []TransitionTrigger{
			{
				FromState:    "draft",
				ToState:      "published",
				TriggerClass: "async",
				TriggerType:  "schedule-eval",
				Config:       `{}`,
			},
		},
	}
	if err := app.RegisterFlow(flow); err != nil {
		t.Fatalf("first RegisterFlow: %v", err)
	}
	// Second call must be a no-op — INSERT OR IGNORE.
	if err := app.RegisterFlow(flow); err != nil {
		t.Fatalf("second RegisterFlow (idempotent): %v", err)
	}
	var count int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM smeldr_transition_triggers WHERE trigger_type = 'schedule-eval'`,
	).Scan(&count); err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if count != 1 {
		t.Errorf("idempotent: expected 1 trigger row, got %d", count)
	}
}

// ——— fireAsyncTriggers — schedule-eval handler ——————————————————————————

func setupEvalQueueFlow(t *testing.T, db *sql.DB) (typeName, itemID string) {
	t.Helper()
	ctx := context.Background()
	typeName = "EvalItem"
	// camelToSnake("EvalItem")+"s" = "eval_items"
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS eval_items (id TEXT PRIMARY KEY, status TEXT NOT NULL, next_eval_at DATETIME, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
	); err != nil {
		t.Fatalf("create eval_items: %v", err)
	}
	itemID = NewID()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO eval_items (id, status, next_eval_at) VALUES (?, 'ratified', datetime('now', '+1 day'))`, itemID,
	); err != nil {
		t.Fatalf("insert eval_items: %v", err)
	}
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{
		Name:     "eval-flow",
		TypeName: typeName,
		States: []State{
			{Name: "draft", IsInitial: true},
			{Name: "ratified"},
			{Name: "pending-re-evaluation"},
		},
		Transitions: []Transition{
			{From: "draft", To: "ratified"},
			{From: "ratified", To: "pending-re-evaluation"},
		},
		Triggers: []TransitionTrigger{
			{
				FromState:    "draft",
				ToState:      "ratified",
				TriggerClass: "async",
				TriggerType:  "schedule-eval",
				Config:       `{"eval_field":"next_eval_at","to_state":"pending-re-evaluation"}`,
			},
		},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	return typeName, itemID
}

func TestFireAsyncTriggers_scheduleEval_happy(t *testing.T) {
	db := newMigratedDB(t)
	typeName, itemID := setupEvalQueueFlow(t, db)

	fireAsyncTriggers(context.Background(), db, typeName, "draft", "ratified", itemID)
	time.Sleep(50 * time.Millisecond)

	var count int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM smeldr_eval_queue WHERE item_id = ?`, itemID,
	).Scan(&count); err != nil {
		t.Fatalf("SELECT smeldr_eval_queue: %v", err)
	}
	if count == 0 {
		t.Error("schedule-eval: expected row in smeldr_eval_queue, got 0")
	}
}

func TestFireAsyncTriggers_scheduleEval_badConfig(t *testing.T) {
	db := newMigratedDB(t)
	ctx := context.Background()
	// Register a trigger with an invalid JSON config.
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{
		Name:        "bad-cfg-flow",
		TypeName:    "BadCfgItem",
		States:      []State{{Name: "a", IsInitial: true}, {Name: "b"}},
		Transitions: []Transition{{From: "a", To: "b"}},
		Triggers: []TransitionTrigger{
			{FromState: "a", ToState: "b", TriggerClass: "async", TriggerType: "schedule-eval", Config: `not-json`},
		},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	prev := slog.Default()
	t.Cleanup(func() { restoreDefaultLogging(prev) })
	var buf safeBuf
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))

	fireAsyncTriggers(ctx, db, "BadCfgItem", "a", "b", "some-id")
	time.Sleep(50 * time.Millisecond)

	if !strings.Contains(buf.String(), "bad config") {
		t.Errorf("bad config: expected 'bad config' in log, got:\n%s", buf.String())
	}
}

func TestFireAsyncTriggers_scheduleEval_noItemID(t *testing.T) {
	db := newMigratedDB(t)
	typeName, _ := setupEvalQueueFlow(t, db)

	prev := slog.Default()
	t.Cleanup(func() { restoreDefaultLogging(prev) })
	var buf safeBuf
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))

	fireAsyncTriggers(context.Background(), db, typeName, "draft", "ratified", "")
	time.Sleep(50 * time.Millisecond)

	if !strings.Contains(buf.String(), "no itemID") {
		t.Errorf("no itemID: expected 'no itemID' in log, got:\n%s", buf.String())
	}
}

func TestFireAsyncTriggers_scheduleEval_emptyEvalField(t *testing.T) {
	// Item has next_eval_at = NULL → sql.NullTime.Valid = false → warn and return.
	db := newMigratedDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS null_eval_items (id TEXT PRIMARY KEY, status TEXT, next_eval_at DATETIME)`,
	); err != nil {
		t.Fatalf("create table: %v", err)
	}
	itemID := NewID()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO null_eval_items (id, status, next_eval_at) VALUES (?, 'draft', NULL)`, itemID,
	); err != nil {
		t.Fatalf("insert: %v", err)
	}
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{
		Name:        "null-eval-flow",
		TypeName:    "NullEvalItem",
		States:      []State{{Name: "draft", IsInitial: true}, {Name: "ratified"}},
		Transitions: []Transition{{From: "draft", To: "ratified"}},
		Triggers: []TransitionTrigger{
			{FromState: "draft", ToState: "ratified", TriggerClass: "async", TriggerType: "schedule-eval",
				Config: `{"eval_field":"next_eval_at","to_state":"pending"}`},
		},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	prev := slog.Default()
	t.Cleanup(func() { restoreDefaultLogging(prev) })
	var buf safeBuf
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))

	fireAsyncTriggers(ctx, db, "NullEvalItem", "draft", "ratified", itemID)
	time.Sleep(50 * time.Millisecond)

	if !strings.Contains(buf.String(), "eval_field unreadable or empty") {
		t.Errorf("null eval: expected 'eval_field unreadable or empty' in log, got:\n%s", buf.String())
	}
}

// ——— resolveItemTable ——————————————————————————————————————————————————————

func TestResolveItemTable_smeldrPrefixMatch(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	// camelToSnake("Decision")+"s" = "decisions"; smeldr_decisions should match first.
	if _, err := db.ExecContext(ctx, `CREATE TABLE smeldr_decisions (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	got := resolveItemTable(ctx, db, "Decision")
	if got != "smeldr_decisions" {
		t.Errorf("smeldr prefix: got %q, want %q", got, "smeldr_decisions")
	}
}

func TestResolveItemTable_snakeMatch(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	// camelToSnake("BlogPost")+"s" = "blog_posts"; no smeldr_blog_posts → snake match.
	if _, err := db.ExecContext(ctx, `CREATE TABLE blog_posts (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	got := resolveItemTable(ctx, db, "BlogPost")
	if got != "blog_posts" {
		t.Errorf("snake match: got %q, want %q", got, "blog_posts")
	}
}

func TestResolveItemTable_fallback(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	// No matching table → fallback to smeldr_dynamic_content.
	got := resolveItemTable(ctx, db, "UnknownType")
	if got != "smeldr_dynamic_content" {
		t.Errorf("fallback: got %q, want %q", got, "smeldr_dynamic_content")
	}
}

// ——— DrainEvalQueue ———————————————————————————————————————————————————————

func TestDrainEvalQueue_nilDB(t *testing.T) {
	app := &App{cfg: Config{DB: nil}}
	walked, triggered, skipped, err := app.DrainEvalQueue(context.Background())
	if err != nil {
		t.Fatalf("nil DB: expected nil error, got %v", err)
	}
	if walked != 0 || triggered != 0 || skipped != 0 {
		t.Errorf("nil DB: expected (0,0,0), got (%d,%d,%d)", walked, triggered, skipped)
	}
}

func TestDrainEvalQueue_noTable(t *testing.T) {
	// Fresh SQLite with no tables → "no such table" → fail-open.
	db := newSQLiteDB(t)
	app := &App{cfg: Config{DB: db}}
	walked, triggered, skipped, err := app.DrainEvalQueue(context.Background())
	if err != nil {
		t.Fatalf("no table: expected nil (fail-open), got %v", err)
	}
	if walked != 0 || triggered != 0 || skipped != 0 {
		t.Errorf("no table: expected (0,0,0), got (%d,%d,%d)", walked, triggered, skipped)
	}
}

func TestDrainEvalQueue_happy(t *testing.T) {
	db := newMigratedDB(t)
	ctx := context.Background()
	// Create the target table.
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE eval_items (id TEXT PRIMARY KEY, status TEXT NOT NULL, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
	); err != nil {
		t.Fatalf("create eval_items: %v", err)
	}
	itemID := NewID()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO eval_items (id, status) VALUES (?, 'ratified')`, itemID,
	); err != nil {
		t.Fatalf("insert item: %v", err)
	}
	qID := NewID()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_eval_queue (id, type_name, item_id, to_state, eval_at) VALUES (?, 'EvalItem', ?, 'pending-re-evaluation', datetime('now', '-1 second'))`,
		qID, itemID,
	); err != nil {
		t.Fatalf("insert queue: %v", err)
	}

	app := &App{cfg: Config{DB: db}}
	walked, triggered, skipped, err := app.DrainEvalQueue(ctx)
	if err != nil {
		t.Fatalf("DrainEvalQueue: %v", err)
	}
	if walked != 1 || triggered != 1 || skipped != 0 {
		t.Errorf("happy: expected (1,1,0), got (%d,%d,%d)", walked, triggered, skipped)
	}

	// Item must have new status.
	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM eval_items WHERE id = ?`, itemID).Scan(&status); err != nil {
		t.Fatalf("SELECT status: %v", err)
	}
	if status != "pending-re-evaluation" {
		t.Errorf("happy: item status = %q, want %q", status, "pending-re-evaluation")
	}
	// Queue row must be deleted.
	var qCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_eval_queue`).Scan(&qCount); err != nil {
		t.Fatalf("SELECT queue: %v", err)
	}
	if qCount != 0 {
		t.Errorf("happy: queue row not deleted, count=%d", qCount)
	}
}

// failingProvenanceStore.Append always fails — used to prove
// recordProvenance's fail-open discipline reaches DrainEvalQueue too
// (T211): the queue row is still deleted, triggered still increments.
type failingProvenanceStore struct{}

func (s *failingProvenanceStore) Append(_ context.Context, _ ProvenanceRecord) error {
	return errors.New("simulated provenance append failure")
}
func (s *failingProvenanceStore) List(_ context.Context, _ ProvenanceFilter) ([]ProvenanceRecord, error) {
	return nil, nil
}

// TestDrainEvalQueue_RecordsProvenance_OnSuccessfulTransition (T211/D51)
// verifies the one absent write D51 identified: a real drain-driven
// transition, with App.Provenance wired, produces exactly one
// ProvenanceRecord naming the drain as a "job" actor.
func TestDrainEvalQueue_RecordsProvenance_OnSuccessfulTransition(t *testing.T) {
	db := newMigratedDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE eval_items (id TEXT PRIMARY KEY, status TEXT NOT NULL, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
	); err != nil {
		t.Fatalf("create eval_items: %v", err)
	}
	itemID := NewID()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO eval_items (id, status) VALUES (?, 'ratified')`, itemID,
	); err != nil {
		t.Fatalf("insert item: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_eval_queue (id, type_name, item_id, to_state, eval_at) VALUES (?, 'EvalItem', ?, 'pending-re-evaluation', datetime('now', '-1 second'))`,
		NewID(), itemID,
	); err != nil {
		t.Fatalf("insert queue: %v", err)
	}

	store := &fakeProvenanceStore{}
	app := &App{cfg: Config{DB: db}}
	app.Provenance(store)

	_, triggered, skipped, err := app.DrainEvalQueue(ctx)
	if err != nil {
		t.Fatalf("DrainEvalQueue: %v", err)
	}
	if triggered != 1 || skipped != 0 {
		t.Fatalf("expected (1,0), got (%d,%d)", triggered, skipped)
	}

	if len(store.appended) != 1 {
		t.Fatalf("got %d provenance records, want 1", len(store.appended))
	}
	rec := store.appended[0]
	if rec.ActorKind != "job" || rec.ActorID != "drain-eval-queue" {
		t.Errorf("actor: got kind=%q id=%q, want job/drain-eval-queue", rec.ActorKind, rec.ActorID)
	}
	if rec.Surface != "trigger" {
		t.Errorf("Surface: got %q, want %q", rec.Surface, "trigger")
	}
	if rec.SubjectType != "EvalItem" || rec.SubjectID != itemID {
		t.Errorf("subject: got %s/%s, want EvalItem/%s", rec.SubjectType, rec.SubjectID, itemID)
	}
	if rec.FromState != "ratified" || rec.ToState != "pending-re-evaluation" {
		t.Errorf("states: got %s->%s, want ratified->pending-re-evaluation", rec.FromState, rec.ToState)
	}
	if rec.Verb != "transition" {
		t.Errorf("Verb: got %q, want %q", rec.Verb, "transition")
	}
}

// TestDrainEvalQueue_NoProvenanceStore_NoOp confirms the existing fail-open
// contract explicitly: App.Provenance never called (provenanceStore nil) —
// no panic, behaviour identical to before T211.
func TestDrainEvalQueue_NoProvenanceStore_NoOp(t *testing.T) {
	db := newMigratedDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE eval_items (id TEXT PRIMARY KEY, status TEXT NOT NULL, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
	); err != nil {
		t.Fatalf("create eval_items: %v", err)
	}
	itemID := NewID()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO eval_items (id, status) VALUES (?, 'ratified')`, itemID,
	); err != nil {
		t.Fatalf("insert item: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_eval_queue (id, type_name, item_id, to_state, eval_at) VALUES (?, 'EvalItem', ?, 'pending-re-evaluation', datetime('now', '-1 second'))`,
		NewID(), itemID,
	); err != nil {
		t.Fatalf("insert queue: %v", err)
	}

	app := &App{cfg: Config{DB: db}} // provenanceStore never set
	_, triggered, skipped, err := app.DrainEvalQueue(ctx)
	if err != nil {
		t.Fatalf("DrainEvalQueue: %v", err)
	}
	if triggered != 1 || skipped != 0 {
		t.Errorf("expected (1,0), got (%d,%d)", triggered, skipped)
	}
}

// TestDrainEvalQueue_ProvenanceWriteFails_QueueRowStillDeleted (T211/A241)
// pins that a failing provenance write does not weaken the existing
// not-re-queued rule: the queue row is still deleted and triggered still
// increments, matching recordProvenance's own fail-open discipline.
func TestDrainEvalQueue_ProvenanceWriteFails_QueueRowStillDeleted(t *testing.T) {
	db := newMigratedDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE eval_items (id TEXT PRIMARY KEY, status TEXT NOT NULL, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
	); err != nil {
		t.Fatalf("create eval_items: %v", err)
	}
	itemID := NewID()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO eval_items (id, status) VALUES (?, 'ratified')`, itemID,
	); err != nil {
		t.Fatalf("insert item: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_eval_queue (id, type_name, item_id, to_state, eval_at) VALUES (?, 'EvalItem', ?, 'pending-re-evaluation', datetime('now', '-1 second'))`,
		NewID(), itemID,
	); err != nil {
		t.Fatalf("insert queue: %v", err)
	}

	app := &App{cfg: Config{DB: db}}
	app.Provenance(&failingProvenanceStore{})

	_, triggered, skipped, err := app.DrainEvalQueue(ctx)
	if err != nil {
		t.Fatalf("DrainEvalQueue: %v", err)
	}
	if triggered != 1 || skipped != 0 {
		t.Errorf("provenance write failure must not affect triggered/skipped: got (%d,%d), want (1,0)", triggered, skipped)
	}
	var qCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_eval_queue`).Scan(&qCount); err != nil {
		t.Fatalf("SELECT queue: %v", err)
	}
	if qCount != 0 {
		t.Errorf("queue row must still be deleted on provenance write failure, count=%d", qCount)
	}
}

func TestDrainEvalQueue_notDueYet(t *testing.T) {
	db := newMigratedDB(t)
	ctx := context.Background()
	// Queue row with eval_at in the future → not drained.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_eval_queue (id, type_name, item_id, to_state, eval_at) VALUES (?, 'EvalItem', 'item1', 'pending', datetime('now', '+1 hour'))`,
		NewID(),
	); err != nil {
		t.Fatalf("insert: %v", err)
	}
	app := &App{cfg: Config{DB: db}}
	_, triggered, skipped, err := app.DrainEvalQueue(ctx)
	if err != nil {
		t.Fatalf("DrainEvalQueue: %v", err)
	}
	if triggered != 0 || skipped != 0 {
		t.Errorf("not due: expected (0,0), got (%d,%d)", triggered, skipped)
	}
}

func TestDrainEvalQueue_transitionFail(t *testing.T) {
	// Queue row due now but target table missing → drainAuthorizationGate's
	// own status-read fails first (no such table) → skipped++, row deleted.
	// The failure point moved with the authorization-gate addition; the
	// observable outcome (skipped, not triggered, row still gone) did not.
	db := newMigratedDB(t)
	ctx := context.Background()
	qID := NewID()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_eval_queue (id, type_name, item_id, to_state, eval_at) VALUES (?, 'MissingType', 'item-x', 'active', datetime('now', '-1 second'))`,
		qID,
	); err != nil {
		t.Fatalf("insert queue: %v", err)
	}
	app := &App{cfg: Config{DB: db}}
	_, triggered, skipped, err := app.DrainEvalQueue(ctx)
	if err != nil {
		t.Fatalf("DrainEvalQueue: %v", err)
	}
	if triggered != 0 || skipped != 1 {
		t.Errorf("transition fail: expected (0,1), got (%d,%d)", triggered, skipped)
	}
	// Row must still be deleted.
	var qCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_eval_queue WHERE id = ?`, qID).Scan(&qCount); err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if qCount != 0 {
		t.Errorf("transition fail: queue row not deleted, count=%d", qCount)
	}
}

func TestDrainEvalQueue_selectFail(t *testing.T) {
	// QueryContext returns a non-"no such table" error → returns error (not fail-open).
	db := newMigratedDB(t)
	ctx := context.Background()
	wrapped := &evalQueueQueryFailDB{DB: db}
	app := &App{cfg: Config{DB: wrapped}}
	_, _, _, err := app.DrainEvalQueue(ctx)
	if err == nil {
		t.Error("selectFail: expected error from QueryContext, got nil")
	}
}

// evalQueueQueryFailDB: makes the first QueryContext (after migration) fail with a
// non-"no such table" error to exercise the DrainEvalQueue SELECT error path.
type evalQueueQueryFailDB struct {
	DB
	n int
}

func (d *evalQueueQueryFailDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	d.n++
	if d.n == 1 {
		return nil, errors.New("simulated select error")
	}
	return d.DB.QueryContext(ctx, q, args...)
}

func TestDrainEvalQueue_rowsError(t *testing.T) {
	// QueryContext returns rows whose Next produces a driver error → rows.Err() path.
	db := newMigratedDB(t)
	ctx := context.Background()
	wrapped := &evalQueueRowsErrDB{DB: db}
	app := &App{cfg: Config{DB: wrapped}}
	_, _, _, err := app.DrainEvalQueue(ctx)
	if err == nil {
		t.Error("rowsError: expected error from rows.Err(), got nil")
	}
}

// evalQueueRowsErrDB: first QueryContext returns rows that produce an iteration error.
type evalQueueRowsErrDB struct {
	DB
	n int
}

func (d *evalQueueRowsErrDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	d.n++
	if d.n == 1 {
		return sql.OpenDB(&rowsErrConnector{}).QueryContext(ctx, "SELECT v1, v2, v3, v4")
	}
	return d.DB.QueryContext(ctx, q, args...)
}

func TestDrainEvalQueue_scanFail(t *testing.T) {
	// Rows with wrong column count → scan fails → skipped++.
	db := newMigratedDB(t)
	ctx := context.Background()
	// Insert a queue row so QueryContext returns at least one row before we swap.
	qID := NewID()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_eval_queue (id, type_name, item_id, to_state, eval_at) VALUES (?, 'T', 'i', 's', datetime('now', '-1 second'))`,
		qID,
	); err != nil {
		t.Fatalf("insert: %v", err)
	}
	wrapped := &evalQueueScanFailDB{DB: db}
	app := &App{cfg: Config{DB: wrapped}}
	walked, triggered, skipped, err := app.DrainEvalQueue(ctx)
	if err != nil {
		t.Fatalf("scanFail: unexpected error: %v", err)
	}
	// scan failed → skipped++ (row still deleted); walked still counts the
	// row (T223 — walked is "rows examined," incremented before Scan is
	// attempted, so a scan failure is still an examined row, not a skipped
	// examination).
	if walked != 1 || triggered != 0 || skipped != 1 {
		t.Errorf("scanFail: expected (1,0,1), got (%d,%d,%d)", walked, triggered, skipped)
	}
}

// evalQueueScanFailDB: first QueryContext returns rows with too few columns so Scan fails.
type evalQueueScanFailDB struct {
	DB
	n int
}

func (d *evalQueueScanFailDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	d.n++
	if d.n == 1 {
		// Return 1-column rows; DrainEvalQueue scans 4 columns → scan error.
		return sql.OpenDB(&scanErrConnector{}).QueryContext(ctx, "SELECT v")
	}
	return d.DB.QueryContext(ctx, q, args...)
}

// — drainAuthorizationGate / recordAuthorizationRequiredSignal — direct unit tests —

// gatedItemFixture creates a target table + one row for drainAuthorizationGate's
// own tests, independent of DrainEvalQueue/smeldr_eval_queue.
func gatedItemFixture(t *testing.T, db *sql.DB, table, itemID, status string) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS `+table+` (id TEXT PRIMARY KEY, status TEXT NOT NULL, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
	); err != nil {
		t.Fatalf("create %s: %v", table, err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO `+table+` (id, status) VALUES (?, ?)`, itemID, status,
	); err != nil {
		t.Fatalf("insert %s row: %v", table, err)
	}
}

func TestDrainAuthorizationGate_StatusReadError(t *testing.T) {
	db := newMigratedDB(t)
	gatedItemFixture(t, db, "gate_items", "item-1", "reviewing")
	wrapped := &nthQueryRowFailDB{DB: db, fail: 1} // 1st QueryRowContext = status read
	_, _, err := drainAuthorizationGate(context.Background(), wrapped, "gate_items", "GateItem", "item-1", "approved")
	if err == nil {
		t.Fatal("expected error from status read, got nil")
	}
}

func TestDrainAuthorizationGate_SameState_NotGated(t *testing.T) {
	db := newMigratedDB(t)
	gatedItemFixture(t, db, "gate_items", "item-2", "approved")
	fromState, requiredRole, err := drainAuthorizationGate(context.Background(), db, "gate_items", "GateItem", "item-2", "approved")
	if err != nil {
		t.Fatalf("drainAuthorizationGate: %v", err)
	}
	if fromState != "approved" || requiredRole != "" {
		t.Errorf("fromState=%q requiredRole=%q, want approved/empty", fromState, requiredRole)
	}
}

func TestDrainAuthorizationGate_NoFlow_NotGated(t *testing.T) {
	// Deliberately newSQLiteDB, not newMigratedDB: migrateStateFlows seeds
	// a "default" flow, so a type-specific lookup miss always falls
	// through to a real (if unrelated) default flow in every other test
	// here. To exercise the true both-lookups-fail path, smeldr_state_flows
	// must not exist at all.
	db := newSQLiteDB(t)
	gatedItemFixture(t, db, "gate_items", "item-3", "reviewing")
	fromState, requiredRole, err := drainAuthorizationGate(context.Background(), db, "gate_items", "GateItem", "item-3", "approved")
	if err != nil {
		t.Fatalf("drainAuthorizationGate: %v", err)
	}
	if fromState != "reviewing" || requiredRole != "" {
		t.Errorf("fromState=%q requiredRole=%q, want reviewing/empty", fromState, requiredRole)
	}
}

func TestDrainAuthorizationGate_NoTransitionRow_NotGated(t *testing.T) {
	db := newMigratedDB(t)
	gatedItemFixture(t, db, "gate_items", "item-4", "reviewing")
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{
		Name:     "gate-flow",
		TypeName: "GateItem",
		States:   []State{{Name: "reviewing", IsInitial: true}, {Name: "archived"}},
		// No "reviewing" -> "approved" transition declared at all.
		Transitions: []Transition{{From: "reviewing", To: "archived"}},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	fromState, requiredRole, err := drainAuthorizationGate(context.Background(), db, "gate_items", "GateItem", "item-4", "approved")
	if err != nil {
		t.Fatalf("drainAuthorizationGate: %v", err)
	}
	if fromState != "reviewing" || requiredRole != "" {
		t.Errorf("fromState=%q requiredRole=%q, want reviewing/empty", fromState, requiredRole)
	}
}

// — ValidTransitions (A296) ——————————————————————————————————————————————————

func TestValidTransitions(t *testing.T) {
	db := newMigratedDB(t)
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{
		Name:     "vt-flow",
		TypeName: "VTItem",
		States: []State{
			{Name: "draft", IsInitial: true},
			{Name: "reviewing"},
			{Name: "approved"},
			{Name: "archived"},
		},
		Transitions: []Transition{
			{From: "draft", To: "reviewing"},
			{From: "reviewing", To: "approved", RequiredRole: "admin", Strict: true},
			{From: "reviewing", To: "archived", RequiredReason: true},
		},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	ctx := context.Background()

	t.Run("multiple transitions, mixed gating", func(t *testing.T) {
		got, err := app.ValidTransitions(ctx, "VTItem", "reviewing")
		if err != nil {
			t.Fatalf("ValidTransitions: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d options, want 2: %+v", len(got), got)
		}
		byState := map[string]TransitionOption{}
		for _, o := range got {
			byState[o.ToState] = o
		}
		approved, ok := byState["approved"]
		if !ok {
			t.Fatal("missing \"approved\" option")
		}
		if approved.RequiredRole != "admin" || !approved.Strict || approved.RequiredReason {
			t.Errorf("approved = %+v, want RequiredRole=admin Strict=true RequiredReason=false", approved)
		}
		archived, ok := byState["archived"]
		if !ok {
			t.Fatal("missing \"archived\" option")
		}
		if archived.RequiredRole != "" || archived.Strict || !archived.RequiredReason {
			t.Errorf("archived = %+v, want RequiredRole=\"\" Strict=false RequiredReason=true", archived)
		}
	})

	t.Run("non-gated transition", func(t *testing.T) {
		got, err := app.ValidTransitions(ctx, "VTItem", "draft")
		if err != nil {
			t.Fatalf("ValidTransitions: %v", err)
		}
		if len(got) != 1 || got[0].ToState != "reviewing" || got[0].RequiredRole != "" {
			t.Errorf("got %+v, want one ungated transition to \"reviewing\"", got)
		}
	})

	t.Run("terminal state, no outgoing transitions", func(t *testing.T) {
		got, err := app.ValidTransitions(ctx, "VTItem", "approved")
		if err != nil {
			t.Fatalf("ValidTransitions: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %d options, want 0: %+v", len(got), got)
		}
	})

	t.Run("unknown fromState", func(t *testing.T) {
		got, err := app.ValidTransitions(ctx, "VTItem", "no-such-state")
		if err != nil {
			t.Fatalf("ValidTransitions: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %d options, want 0: %+v", len(got), got)
		}
	})

	t.Run("unknown typeName, no default flow", func(t *testing.T) {
		// CreateStateFlowTables only, not migrateStateFlows/newMigratedDB —
		// tables exist (so resolveFlowID's lookup gets sql.ErrNoRows, a
		// real "not found", not a "no such table" error) but no default
		// flow is seeded.
		bareDB := newSQLiteDB(t)
		if err := CreateStateFlowTables(bareDB); err != nil {
			t.Fatalf("CreateStateFlowTables: %v", err)
		}
		bareApp := &App{cfg: Config{DB: bareDB}}
		got, err := bareApp.ValidTransitions(ctx, "NoSuchType", "draft")
		if err != nil {
			t.Fatalf("ValidTransitions: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %d options, want 0: %+v", len(got), got)
		}
	})

	t.Run("nil DB", func(t *testing.T) {
		nilDBApp := &App{cfg: Config{}}
		got, err := nilDBApp.ValidTransitions(ctx, "VTItem", "reviewing")
		if err != nil || got != nil {
			t.Errorf("got (%v, %v), want (nil, nil)", got, err)
		}
	})
}

func TestValidTransitions_ResolveFlowIDError(t *testing.T) {
	db := newMigratedDB(t)
	app := &App{cfg: Config{DB: &nthQueryRowFailDB{DB: db, fail: 1}}}
	_, err := app.ValidTransitions(context.Background(), "VTItem", "reviewing")
	if err == nil {
		t.Fatal("expected error from resolveFlowID's underlying query failure, got nil")
	}
}

// validTransitionsQueryFailDB makes the smeldr_transitions listing query
// fail while leaving resolveFlowID's own QueryRowContext lookups alone —
// isolates ValidTransitions's own QueryContext error branch.
type validTransitionsQueryFailDB struct{ DB }

func (d *validTransitionsQueryFailDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if strings.Contains(q, "SELECT to_state, required_role, required_reason, strict") {
		return nil, errors.New("injected QueryContext failure")
	}
	return d.DB.QueryContext(ctx, q, args...)
}

func TestValidTransitions_QueryError(t *testing.T) {
	db := newMigratedDB(t)
	app := &App{cfg: Config{DB: &validTransitionsQueryFailDB{DB: db}}}
	if err := app.RegisterFlow(StateFlow{
		Name: "vt-qfail-flow", TypeName: "VTQFail",
		States:      []State{{Name: "draft", IsInitial: true}, {Name: "done"}},
		Transitions: []Transition{{From: "draft", To: "done"}},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	_, err := app.ValidTransitions(context.Background(), "VTQFail", "draft")
	if err == nil {
		t.Fatal("expected error from injected QueryContext failure, got nil")
	}
}

// validTransitionsScanFailDB swaps the smeldr_transitions listing query for
// one with the wrong column count, so the caller's Scan fails.
type validTransitionsScanFailDB struct{ DB }

func (d *validTransitionsScanFailDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if strings.Contains(q, "SELECT to_state, required_role, required_reason, strict") {
		return d.DB.QueryContext(ctx, "SELECT 1")
	}
	return d.DB.QueryContext(ctx, q, args...)
}

func TestValidTransitions_ScanError(t *testing.T) {
	db := newMigratedDB(t)
	app := &App{cfg: Config{DB: &validTransitionsScanFailDB{DB: db}}}
	if err := app.RegisterFlow(StateFlow{
		Name: "vt-scanfail-flow", TypeName: "VTScanFail",
		States:      []State{{Name: "draft", IsInitial: true}, {Name: "done"}},
		Transitions: []Transition{{From: "draft", To: "done"}},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	_, err := app.ValidTransitions(context.Background(), "VTScanFail", "draft")
	if err == nil {
		t.Fatal("expected scan error from wrong column count, got nil")
	}
}

func TestDrainAuthorizationGate_UngatedTransition(t *testing.T) {
	db := newMigratedDB(t)
	gatedItemFixture(t, db, "gate_items", "item-5", "reviewing")
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{
		Name:        "gate-flow",
		TypeName:    "GateItem",
		States:      []State{{Name: "reviewing", IsInitial: true}, {Name: "approved"}},
		Transitions: []Transition{{From: "reviewing", To: "approved"}}, // no RequiredRole
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	fromState, requiredRole, err := drainAuthorizationGate(context.Background(), db, "gate_items", "GateItem", "item-5", "approved")
	if err != nil {
		t.Fatalf("drainAuthorizationGate: %v", err)
	}
	if fromState != "reviewing" || requiredRole != "" {
		t.Errorf("fromState=%q requiredRole=%q, want reviewing/empty", fromState, requiredRole)
	}
}

func TestDrainAuthorizationGate_GatedTransition(t *testing.T) {
	db := newMigratedDB(t)
	gatedItemFixture(t, db, "gate_items", "item-6", "reviewing")
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{
		Name:     "gate-flow",
		TypeName: "GateItem",
		States:   []State{{Name: "reviewing", IsInitial: true}, {Name: "approved"}},
		Transitions: []Transition{
			{From: "reviewing", To: "approved", RequiredRole: "reviewer"},
		},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	fromState, requiredRole, err := drainAuthorizationGate(context.Background(), db, "gate_items", "GateItem", "item-6", "approved")
	if err != nil {
		t.Fatalf("drainAuthorizationGate: %v", err)
	}
	if fromState != "reviewing" || requiredRole != "reviewer" {
		t.Errorf("fromState=%q requiredRole=%q, want reviewing/reviewer", fromState, requiredRole)
	}
}

func TestDrainAuthorizationGate_TransitionQueryError(t *testing.T) {
	db := newMigratedDB(t)
	gatedItemFixture(t, db, "gate_items", "item-7", "reviewing")
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{
		Name:     "gate-flow",
		TypeName: "GateItem",
		States:   []State{{Name: "reviewing", IsInitial: true}, {Name: "approved"}},
		Transitions: []Transition{
			{From: "reviewing", To: "approved", RequiredRole: "reviewer"},
		},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	// 1st QueryRowContext = status read, 2nd = flow-id lookup, 3rd = transition-row lookup.
	wrapped := &nthQueryRowFailDB{DB: db, fail: 3}
	_, _, err := drainAuthorizationGate(context.Background(), wrapped, "gate_items", "GateItem", "item-7", "approved")
	if err == nil {
		t.Fatal("expected error from transition-row read, got nil")
	}
}

func TestRecordAuthorizationRequiredSignal_Success(t *testing.T) {
	db := newMigratedDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	ctx := context.Background()
	b := newEventBroadcaster()
	streamCh, subErr := b.subscribe("u1")
	if subErr != nil {
		t.Fatalf("subscribe: %v", subErr)
	}
	defer b.unsubscribe(streamCh)
	if err := recordAuthorizationRequiredSignal(ctx, db, nil, nil, b, "GateItem", "item-8", "reviewing", "approved", "reviewer"); err != nil {
		t.Fatalf("recordAuthorizationRequiredSignal: %v", err)
	}
	select {
	case payload := <-streamCh:
		var got WebhookEventPayload
		if err := json.Unmarshal(payload, &got); err != nil {
			t.Fatalf("unmarshal broadcast payload: %v", err)
		}
		if got.Event != "signal.created" {
			t.Errorf("Event = %q, want %q", got.Event, "signal.created")
		}
	default:
		t.Fatal("expected signal.created broadcast, got none")
	}
	var sender, receiver, signalType, status string
	if err := db.QueryRowContext(ctx,
		`SELECT sender, receiver, signal_type, status FROM smeldr_signals`,
	).Scan(&sender, &receiver, &signalType, &status); err != nil {
		t.Fatalf("SELECT smeldr_signals: %v", err)
	}
	if sender != "system" || receiver != "reviewer" || signalType != "authorization-required" || status != "pending" {
		t.Errorf("Signal fields = (%q,%q,%q,%q), want (system,reviewer,authorization-required,pending)",
			sender, receiver, signalType, status)
	}
	var subjectType, subjectID, fromState, toState, requiredRole string
	if err := db.QueryRowContext(ctx,
		`SELECT subject_type, subject_id, from_state, to_state, required_role FROM smeldr_signals`,
	).Scan(&subjectType, &subjectID, &fromState, &toState, &requiredRole); err != nil {
		t.Fatalf("SELECT smeldr_signals structured columns: %v", err)
	}
	if subjectType != "GateItem" || subjectID != "item-8" || fromState != "reviewing" ||
		toState != "approved" || requiredRole != "reviewer" {
		t.Errorf("structured fields = (%q,%q,%q,%q,%q), want (GateItem,item-8,reviewing,approved,reviewer)",
			subjectType, subjectID, fromState, toState, requiredRole)
	}
}

func TestRecordAuthorizationRequiredSignal_InsertError(t *testing.T) {
	db := newMigratedDB(t)
	// smeldr_signals table deliberately not created.
	err := recordAuthorizationRequiredSignal(context.Background(), db, nil, nil, nil, "GateItem", "item-9", "reviewing", "approved", "reviewer")
	if err == nil {
		t.Fatal("expected error from INSERT into missing smeldr_signals, got nil")
	}
}

// — DrainEvalQueue — gated transition wiring, end-to-end ————————————————————

// TestDrainEvalQueue_AuthorizationGate_NoProvenanceWrite (T211) confirms
// the role-gated branch (Signal recorded, no UPDATE applied) writes no
// provenance record — only a real transition produces one, matching
// provenanceVerbFor's own precondition that a record describes something
// that actually happened.
func TestDrainEvalQueue_AuthorizationGate_NoProvenanceWrite(t *testing.T) {
	db := newMigratedDB(t)
	ctx := context.Background()
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	gatedItemFixture(t, db, "gate_items", "item-11", "reviewing")

	store := &fakeProvenanceStore{}
	app := &App{cfg: Config{DB: db}}
	app.Provenance(store)
	if err := app.RegisterFlow(StateFlow{
		Name:     "gate-flow-prov",
		TypeName: "GateItem",
		States:   []State{{Name: "reviewing", IsInitial: true}, {Name: "approved"}},
		Transitions: []Transition{
			{From: "reviewing", To: "approved", RequiredRole: "reviewer"},
		},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_eval_queue (id, type_name, item_id, to_state, eval_at) VALUES (?, 'GateItem', 'item-11', 'approved', datetime('now', '-1 second'))`,
		NewID(),
	); err != nil {
		t.Fatalf("insert queue: %v", err)
	}

	_, triggered, skipped, err := app.DrainEvalQueue(ctx)
	if err != nil {
		t.Fatalf("DrainEvalQueue: %v", err)
	}
	if triggered != 0 || skipped != 1 {
		t.Fatalf("gated: expected (0,1), got (%d,%d)", triggered, skipped)
	}
	if len(store.appended) != 0 {
		t.Errorf("gated transition (never applied): want 0 provenance records, got %d", len(store.appended))
	}
}

func TestDrainEvalQueue_GatedTransition_SignalEmittedNotApplied(t *testing.T) {
	db := newMigratedDB(t)
	ctx := context.Background()
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	gatedItemFixture(t, db, "gate_items", "item-10", "reviewing")

	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{
		Name:     "gate-flow",
		TypeName: "GateItem",
		States:   []State{{Name: "reviewing", IsInitial: true}, {Name: "approved"}},
		Transitions: []Transition{
			{From: "reviewing", To: "approved", RequiredRole: "reviewer"},
		},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}

	qID := NewID()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_eval_queue (id, type_name, item_id, to_state, eval_at) VALUES (?, 'GateItem', 'item-10', 'approved', datetime('now', '-1 second'))`,
		qID,
	); err != nil {
		t.Fatalf("insert queue: %v", err)
	}

	_, triggered, skipped, err := app.DrainEvalQueue(ctx)
	if err != nil {
		t.Fatalf("DrainEvalQueue: %v", err)
	}
	if triggered != 0 || skipped != 1 {
		t.Errorf("gated: expected (0,1), got (%d,%d)", triggered, skipped)
	}

	// Item status must be unchanged — automation never crossed the gate.
	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM gate_items WHERE id = 'item-10'`).Scan(&status); err != nil {
		t.Fatalf("SELECT status: %v", err)
	}
	if status != "reviewing" {
		t.Errorf("item status = %q, want unchanged %q", status, "reviewing")
	}

	// Exactly one Signal recorded, addressed to the declared role.
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_signals WHERE signal_type = 'authorization-required'`).Scan(&count); err != nil {
		t.Fatalf("SELECT signal count: %v", err)
	}
	if count != 1 {
		t.Errorf("authorization-required signal count = %d, want 1", count)
	}
	var receiver string
	if err := db.QueryRowContext(ctx, `SELECT receiver FROM smeldr_signals WHERE signal_type = 'authorization-required'`).Scan(&receiver); err != nil {
		t.Fatalf("SELECT signal receiver: %v", err)
	}
	if receiver != "reviewer" {
		t.Errorf("signal receiver = %q, want %q", receiver, "reviewer")
	}

	// Queue row must still be deleted — blocked transitions are not re-queued.
	var qCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_eval_queue`).Scan(&qCount); err != nil {
		t.Fatalf("SELECT queue: %v", err)
	}
	if qCount != 0 {
		t.Errorf("queue row not deleted, count=%d", qCount)
	}
}

func TestDrainEvalQueue_GatedTransition_SignalRecordFails(t *testing.T) {
	// Same as TestDrainEvalQueue_GatedTransition_SignalEmittedNotApplied,
	// but smeldr_signals is deliberately never created — exercises
	// DrainEvalQueue's own log-and-continue branch when the gate check
	// succeeds (a role is required) but recordAuthorizationRequiredSignal
	// itself fails. The item must still not transition, and the queue row
	// must still be deleted — a Signal failing to record does not change
	// the authorization verdict.
	db := newMigratedDB(t)
	ctx := context.Background()
	gatedItemFixture(t, db, "gate_items", "item-11", "reviewing")

	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{
		Name:     "gate-flow",
		TypeName: "GateItem",
		States:   []State{{Name: "reviewing", IsInitial: true}, {Name: "approved"}},
		Transitions: []Transition{
			{From: "reviewing", To: "approved", RequiredRole: "reviewer"},
		},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}

	qID := NewID()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_eval_queue (id, type_name, item_id, to_state, eval_at) VALUES (?, 'GateItem', 'item-11', 'approved', datetime('now', '-1 second'))`,
		qID,
	); err != nil {
		t.Fatalf("insert queue: %v", err)
	}

	_, triggered, skipped, err := app.DrainEvalQueue(ctx)
	if err != nil {
		t.Fatalf("DrainEvalQueue: %v", err)
	}
	if triggered != 0 || skipped != 1 {
		t.Errorf("gated, signal-record-fails: expected (0,1), got (%d,%d)", triggered, skipped)
	}

	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM gate_items WHERE id = 'item-11'`).Scan(&status); err != nil {
		t.Fatalf("SELECT status: %v", err)
	}
	if status != "reviewing" {
		t.Errorf("item status = %q, want unchanged %q", status, "reviewing")
	}

	var qCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_eval_queue`).Scan(&qCount); err != nil {
		t.Fatalf("SELECT queue: %v", err)
	}
	if qCount != 0 {
		t.Errorf("queue row not deleted, count=%d", qCount)
	}
}

// nthExecFailDB wraps a real DB and makes the nth ExecContext call fail.
type nthExecFailDB struct {
	DB
	n    int
	fail int // 1-indexed
}

func (d *nthExecFailDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	d.n++
	if d.n == d.fail {
		return nil, errors.New("simulated exec error")
	}
	return d.DB.ExecContext(ctx, q, args...)
}

func TestDrainEvalQueue_UngatedTransition_UpdateFails(t *testing.T) {
	// The ungated (default) branch's own UPDATE failing — a pre-existing
	// DrainEvalQueue path, unaffected by the authorization-gate addition.
	// registerFlow runs against the unwrapped db so its own ExecContext
	// calls don't shift the wrapped DB's call count; only DrainEvalQueue's
	// own two ExecContext calls (UPDATE, then DELETE) are ever seen by it.
	db := newMigratedDB(t)
	ctx := context.Background()
	gatedItemFixture(t, db, "gate_items", "item-12", "reviewing")

	regApp := &App{cfg: Config{DB: db}}
	if err := regApp.RegisterFlow(StateFlow{
		Name:        "gate-flow",
		TypeName:    "GateItem",
		States:      []State{{Name: "reviewing", IsInitial: true}, {Name: "approved"}},
		Transitions: []Transition{{From: "reviewing", To: "approved"}}, // no RequiredRole
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}

	qID := NewID()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_eval_queue (id, type_name, item_id, to_state, eval_at) VALUES (?, 'GateItem', 'item-12', 'approved', datetime('now', '-1 second'))`,
		qID,
	); err != nil {
		t.Fatalf("insert queue: %v", err)
	}

	wrapped := &nthExecFailDB{DB: db, fail: 1} // 1st ExecContext inside DrainEvalQueue = the UPDATE
	app := &App{cfg: Config{DB: wrapped}}
	_, triggered, skipped, err := app.DrainEvalQueue(ctx)
	if err != nil {
		t.Fatalf("DrainEvalQueue: %v", err)
	}
	if triggered != 0 || skipped != 1 {
		t.Errorf("ungated, update fails: expected (0,1), got (%d,%d)", triggered, skipped)
	}
}

func TestDrainEvalQueue_DeleteFails(t *testing.T) {
	// The queue-row DELETE failing — logged, does not affect the returned
	// counts (the transition itself already applied or was handled).
	db := newMigratedDB(t)
	ctx := context.Background()
	gatedItemFixture(t, db, "gate_items", "item-13", "reviewing")

	regApp := &App{cfg: Config{DB: db}}
	if err := regApp.RegisterFlow(StateFlow{
		Name:        "gate-flow",
		TypeName:    "GateItem",
		States:      []State{{Name: "reviewing", IsInitial: true}, {Name: "approved"}},
		Transitions: []Transition{{From: "reviewing", To: "approved"}}, // no RequiredRole
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}

	qID := NewID()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_eval_queue (id, type_name, item_id, to_state, eval_at) VALUES (?, 'GateItem', 'item-13', 'approved', datetime('now', '-1 second'))`,
		qID,
	); err != nil {
		t.Fatalf("insert queue: %v", err)
	}

	wrapped := &nthExecFailDB{DB: db, fail: 2} // 1st = UPDATE (succeeds), 2nd = DELETE (fails)
	app := &App{cfg: Config{DB: wrapped}}
	_, triggered, skipped, err := app.DrainEvalQueue(ctx)
	if err != nil {
		t.Fatalf("DrainEvalQueue: %v", err)
	}
	if triggered != 1 || skipped != 0 {
		t.Errorf("delete fails: expected (1,0), got (%d,%d)", triggered, skipped)
	}
}

func TestRegisterFlow_checkTriggerQueryFail(t *testing.T) {
	// The SELECT COUNT(*) for idempotency check fails → returns error.
	db := newMigratedDB(t)
	app := &App{cfg: Config{DB: &triggerCheckFailDB{DB: db}}}
	err := app.RegisterFlow(StateFlow{
		Name:        "check-fail-flow",
		TypeName:    "CheckFailItem",
		States:      []State{{Name: "a", IsInitial: true}, {Name: "b"}},
		Transitions: []Transition{{From: "a", To: "b"}},
		Triggers: []TransitionTrigger{
			{FromState: "a", ToState: "b", TriggerClass: "async", TriggerType: "schedule-eval", Config: `{}`},
		},
	})
	if err == nil {
		t.Error("checkTriggerQueryFail: expected error, got nil")
	}
}

// triggerCheckFailDB: wraps a real DB; makes the QueryRowContext call for
// `SELECT COUNT(*) FROM smeldr_transition_triggers` fail by returning no row.
type triggerCheckFailDB struct {
	DB
}

func (d *triggerCheckFailDB) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	if strings.Contains(q, "smeldr_transition_triggers") {
		// Return a row that errors on Scan.
		sdb, _ := sql.Open("sqlite", ":memory:")
		return sdb.QueryRowContext(ctx, "SELECT 1 FROM no_table_xyz")
	}
	return d.DB.QueryRowContext(ctx, q, args...)
}

func TestFireAsyncTriggers_scheduleEval_insertFail(t *testing.T) {
	// schedule-eval handler: eval_at is set but INSERT into smeldr_eval_queue fails → warn, fail-open.
	db := newMigratedDB(t)
	typeName, itemID := setupEvalQueueFlow(t, db)
	wrapped := &evalQueueInsertFailDB{DB: db}

	prev := slog.Default()
	t.Cleanup(func() { restoreDefaultLogging(prev) })
	var buf safeBuf
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))

	fireAsyncTriggers(context.Background(), wrapped, typeName, "draft", "ratified", itemID)
	time.Sleep(50 * time.Millisecond)

	if !strings.Contains(buf.String(), "INSERT failed") {
		t.Errorf("insertFail: expected 'INSERT failed' in log, got:\n%s", buf.String())
	}
}

// evalQueueInsertFailDB: forwards everything except ExecContext for smeldr_eval_queue inserts.
type evalQueueInsertFailDB struct{ DB }

func (d *evalQueueInsertFailDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	if strings.Contains(q, "smeldr_eval_queue") {
		return nil, errors.New("simulated INSERT fail")
	}
	return d.DB.ExecContext(ctx, q, args...)
}

// — validateTransition required_role tests ————————————————————————————————————

// setupGovStateDB creates a SQLite DB with governance tables, state flow tables,
// and a "GovPost" flow where draft→published requires the "editor" role.
func setupGovStateDB(t *testing.T) (*sql.DB, *RoleStore) {
	t.Helper()
	db := setupGovernanceDB(t) // governance tables + seeded default roles
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{
		Name:     "govpost-flow",
		TypeName: "GovPost",
		States: []State{
			{Name: "draft", IsInitial: true},
			{Name: "published"},
		},
		Transitions: []Transition{
			{From: "draft", To: "published", RequiredRole: "editor"},
		},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	return db, NewRoleStore(db)
}

func TestValidateTransition_RequiredRole_NilRS(t *testing.T) {
	db, _ := setupGovStateDB(t)
	if err := validateTransition(context.Background(), db, nil, "tok", "GovPost", "draft", "published", ""); err != nil {
		t.Errorf("nil RoleStore: want nil, got %v", err)
	}
}

func TestValidateTransition_RequiredRole_EmptyActor(t *testing.T) {
	db, rs := setupGovStateDB(t)
	if err := validateTransition(context.Background(), db, rs, "", "GovPost", "draft", "published", ""); err != nil {
		t.Errorf("empty actorID (system path): want nil, got %v", err)
	}
}

func TestValidateTransition_RequiredRole_Granted(t *testing.T) {
	db, rs := setupGovStateDB(t)
	tokenID := setupTokenWithRole(t, db, rs, "editor")
	if err := validateTransition(context.Background(), db, rs, tokenID, "GovPost", "draft", "published", ""); err != nil {
		t.Errorf("authorized editor: want nil, got %v", err)
	}
}

func TestValidateTransition_RequiredRole_NotGranted(t *testing.T) {
	db, rs := setupGovStateDB(t)
	tokenID := setupTokenWithRole(t, db, rs, "author") // author does not have editor role
	err := validateTransition(context.Background(), db, rs, tokenID, "GovPost", "draft", "published", "")
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("unauthorized actor: want ErrForbidden, got %v", err)
	}
}

func TestValidateTransition_RequiredRole_GrantCheckError(t *testing.T) {
	db, _ := setupGovStateDB(t)
	// Structural queries go through real db; grants query goes through wrapped db (fails).
	wrapped := &govQueryFailDB{DB: db, failOn: "FROM smeldr_role_grants g"}
	rs := NewRoleStore(wrapped)
	err := validateTransition(context.Background(), db, rs, "tok", "GovPost", "draft", "published", "")
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("grant query error: want ErrForbidden (fail-closed), got %v", err)
	}
}

// — D34 strict-transition tests ————————————————————————————————————————————

// setupGovStateStrictDB extends setupGovStateDB with a second flow
// ("GovPostStrict") whose draft→published transition is both RequiredRole
// and Strict — used to exercise the fail-closed [E]/[F] branches without
// disturbing the non-strict "GovPost" flow's own existing test coverage.
func setupGovStateStrictDB(t *testing.T) (*sql.DB, *RoleStore) {
	t.Helper()
	db, rs := setupGovStateDB(t)
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{
		Name:     "govpoststrict-flow",
		TypeName: "GovPostStrict",
		States: []State{
			{Name: "draft", IsInitial: true},
			{Name: "published"},
		},
		Transitions: []Transition{
			{From: "draft", To: "published", RequiredRole: "editor", Strict: true},
		},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	return db, rs
}

func TestValidateTransition_Strict_NilRS_Forbidden(t *testing.T) {
	db, _ := setupGovStateStrictDB(t)
	err := validateTransition(context.Background(), db, nil, "tok", "GovPostStrict", "draft", "published", "")
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("strict, nil RoleStore: want ErrForbidden, got %v", err)
	}
}

func TestValidateTransition_Strict_EmptyActor_Forbidden(t *testing.T) {
	db, rs := setupGovStateStrictDB(t)
	err := validateTransition(context.Background(), db, rs, "", "GovPostStrict", "draft", "published", "")
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("strict, empty actorID: want ErrForbidden, got %v", err)
	}
}

func TestValidateTransition_Strict_Granted(t *testing.T) {
	db, rs := setupGovStateStrictDB(t)
	tokenID := setupTokenWithRole(t, db, rs, "editor")
	err := validateTransition(context.Background(), db, rs, tokenID, "GovPostStrict", "draft", "published", "")
	if err != nil {
		t.Errorf("strict, authorized editor: want nil, got %v", err)
	}
}

func TestValidateTransition_Strict_NotGranted(t *testing.T) {
	db, rs := setupGovStateStrictDB(t)
	tokenID := setupTokenWithRole(t, db, rs, "author") // author does not have editor role
	err := validateTransition(context.Background(), db, rs, tokenID, "GovPostStrict", "draft", "published", "")
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("strict, unauthorized actor: want ErrForbidden, got %v", err)
	}
}

// TestRegisterFlow_StrictColumnPersisted confirms Transition.Strict actually
// reaches the smeldr_transitions row, not just compiles — the non-strict
// "GovPost" flow (setupGovStateDB) and the strict "GovPostStrict" flow
// (setupGovStateStrictDB) are registered side by side in the same DB above;
// this reads both rows back directly to prove they differ.
func TestRegisterFlow_StrictColumnPersisted(t *testing.T) {
	db, _ := setupGovStateStrictDB(t)
	var nonStrict, strict bool
	if err := db.QueryRowContext(context.Background(),
		`SELECT t.strict FROM smeldr_transitions t JOIN smeldr_state_flows f ON t.flow_id = f.id WHERE f.type_name = 'GovPost'`,
	).Scan(&nonStrict); err != nil {
		t.Fatalf("read GovPost strict column: %v", err)
	}
	if nonStrict {
		t.Error("GovPost draft→published: want strict=false, got true")
	}
	if err := db.QueryRowContext(context.Background(),
		`SELECT t.strict FROM smeldr_transitions t JOIN smeldr_state_flows f ON t.flow_id = f.id WHERE f.type_name = 'GovPostStrict'`,
	).Scan(&strict); err != nil {
		t.Fatalf("read GovPostStrict strict column: %v", err)
	}
	if !strict {
		t.Error("GovPostStrict draft→published: want strict=true, got false")
	}
}

// — DynamicTypeRepo.WithGovernance + SetStatus tests ——————————————————————————

// setupGovStateBlockDB extends setupGovStateDB with block tables for DynamicTypeRepo
// and a "govrecipe" flow where draft→published requires the "editor" role.
func setupGovStateBlockDB(t *testing.T) (*sql.DB, *RoleStore) {
	t.Helper()
	db, rs := setupGovStateDB(t)
	if err := CreateBlockTables(db); err != nil {
		t.Fatalf("CreateBlockTables: %v", err)
	}
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{
		Name:     "govrecipe-flow",
		TypeName: "govrecipe",
		States: []State{
			{Name: "draft", IsInitial: true},
			{Name: "published"},
		},
		Transitions: []Transition{
			{From: "draft", To: "published", RequiredRole: "editor"},
		},
	}); err != nil {
		t.Fatalf("RegisterFlow (govrecipe): %v", err)
	}
	return db, rs
}

// TestDynamicTypeRepo_WithGovernance_PlainCtx verifies that a plain context.Context
// (no User) bypasses the required_role check — system-initiated paths are pre-authorized.
func TestDynamicTypeRepo_WithGovernance_PlainCtx(t *testing.T) {
	db, rs := setupGovStateBlockDB(t)
	repo := NewDynamicTypeRepo(db, "govrecipe", nil).WithGovernance(rs)
	node, err := repo.CreateDraft(context.Background(), nil)
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if err := repo.SetStatus(context.Background(), node.ID, Published); err != nil {
		t.Errorf("plain ctx (system path): want nil, got %v", err)
	}
}

// TestDynamicTypeRepo_WithGovernance_Authorized verifies that an actor holding the
// required "editor" role may perform the transition.
func TestDynamicTypeRepo_WithGovernance_Authorized(t *testing.T) {
	db, rs := setupGovStateBlockDB(t)
	tokenID := setupTokenWithRole(t, db, rs, "editor")
	repo := NewDynamicTypeRepo(db, "govrecipe", nil).WithGovernance(rs)
	node, err := repo.CreateDraft(context.Background(), nil)
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	ctx := NewTestContext(User{ID: tokenID})
	if err := repo.SetStatus(ctx, node.ID, Published); err != nil {
		t.Errorf("authorized editor: want nil, got %v", err)
	}
}

// TestDynamicTypeRepo_WithGovernance_Forbidden verifies that an actor lacking the
// required role receives ErrForbidden when performing a gated transition.
func TestDynamicTypeRepo_WithGovernance_Forbidden(t *testing.T) {
	db, rs := setupGovStateBlockDB(t)
	tokenID := setupTokenWithRole(t, db, rs, "author") // author does not satisfy "editor"
	repo := NewDynamicTypeRepo(db, "govrecipe", nil).WithGovernance(rs)
	node, err := repo.CreateDraft(context.Background(), nil)
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	ctx := NewTestContext(User{ID: tokenID})
	if err := repo.SetStatus(ctx, node.ID, Published); !errors.Is(err, ErrForbidden) {
		t.Errorf("unauthorized actor: want ErrForbidden, got %v", err)
	}
}
