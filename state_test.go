package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"testing"
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
	var flowID int64
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

	var flowID int64
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
	var flowID int64
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

func TestRegisterFlow_insertStateError(t *testing.T) {
	// flowIDDB returns flowID=42 for QueryRowContext and fails ExecContext on call N.
	// failAt=2: INSERT flow (call 1) succeeds, INSERT first state (call 2) fails.
	app := &App{cfg: Config{DB: &flowIDDB{failAt: 2}}}
	err := app.RegisterFlow(agentJobFlow)
	if err == nil {
		t.Fatal("RegisterFlow should return error when state INSERT fails")
	}
}

func TestRegisterFlow_insertTransitionError(t *testing.T) {
	// failAt=6: INSERT flow (1) + 4 state INSERTs (2-5) succeed, first transition INSERT (6) fails.
	app := &App{cfg: Config{DB: &flowIDDB{failAt: 6}}}
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
