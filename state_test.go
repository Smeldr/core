package smeldr

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"strings"
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

// — validateTransition tests ——————————————————————————————————————————————————

func TestValidateTransition_nilDB(t *testing.T) {
	ctx := context.Background()
	if err := validateTransition(ctx, nil, "Post", "draft", "published"); err != nil {
		t.Errorf("nil db: want nil, got %v", err)
	}
}

func TestValidateTransition_nonSQLite(t *testing.T) {
	// failOnNthExecDB.QueryRowContext returns guardRowConn{noRow:true} → scan fails
	// → sqlite_master probe returns error → validateTransition returns nil.
	ctx := context.Background()
	db := &failOnNthExecDB{failAt: 999} // exec never fails; query always returns no-row
	if err := validateTransition(ctx, db, "Post", "draft", "published"); err != nil {
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
	if err := validateTransition(ctx, db, "Post", "published", "published"); err != nil {
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
	if err := validateTransition(ctx, db, "AgentJob", "draft", "published"); err != nil {
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
	err := validateTransition(ctx, db, "AgentJob", "draft", "archived")
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
	if err := validateTransition(ctx, db, "GenericPost", "draft", "published"); err != nil {
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
	err := validateTransition(ctx, db, "GenericPost", "archived", "draft")
	if !errors.Is(err, ErrConflict) {
		t.Errorf("invalid default-flow transition: want ErrConflict, got %v", err)
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
	if err := validateTransition(ctx, db, "GenericPost", "draft", "published"); err != nil {
		t.Errorf("no flow: want nil, got %v", err)
	}
}

func TestValidateTransition_countQueryError(t *testing.T) {
	// transitFailDB returns a valid SQLite-probe result (count=0), a valid flowID (42)
	// for the flow lookup, but fails the COUNT(*) FROM smeldr_transitions query.
	// validateTransition must fail open (return nil) when the count query errors.
	ctx := context.Background()
	db := &transitFailDB{}
	if err := validateTransition(ctx, db, "Post", "draft", "published"); err != nil {
		t.Errorf("count query error: want nil (fail open), got %v", err)
	}
}

// transitFailDB simulates a DB that passes the sqlite_master probe and flow lookup
// but fails the smeldr_transitions COUNT query. It tracks query order by SQL prefix.
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
	// All other queries (smeldr_transitions COUNT) — return no row → scan error.
	conn := &guardRowConn{noRow: true}
	return sql.OpenDB(conn).QueryRowContext(ctx, "SELECT v")
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
	err := m.MCPPublish(ctx, p.Slug)
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
	err := m.MCPArchive(ctx, p.Slug)
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
	err := m.MCPSchedule(ctx, p.Slug, time.Now().Add(time.Hour))
	if !errors.Is(err, ErrConflict) {
		t.Errorf("MCPSchedule on invalid transition: want ErrConflict, got %v", err)
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
	var flowID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM smeldr_state_flows WHERE name = 'default' AND type_name IS NULL`).Scan(&flowID); err != nil {
		t.Fatalf("get default flow id: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_states (flow_id, name, is_initial, is_terminal, suppresses_signals) VALUES (?, 'quarantined', 0, 0, 1)`,
		flowID,
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
	m.setAfterHook(func(_ Context, _ Signal, _ afterHookMeta, _ any) {
		hookCalled <- struct{}{}
	})

	p := &testPost{Node: Node{ID: NewID(), Slug: "s", Status: Published}}
	m.notifyAfter(NewTestContext(User{}), AfterPublish, "draft", p)

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
	m.setAfterHook(func(_ Context, _ Signal, _ afterHookMeta, _ any) {
		hookCalled <- struct{}{}
	})

	// Item is in "draft" state — suppresses_signals=false for draft → hook must fire.
	p := &testPost{Node: Node{ID: NewID(), Slug: "s", Status: Draft}}
	m.notifyAfter(NewTestContext(User{}), AfterUpdate, "", p)

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
func setupTriggerFlow(t *testing.T, db DB, triggerClass, triggerType string) int64 {
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
	var transID int64
	if err := db.QueryRowContext(ctx, `
		SELECT t.id FROM smeldr_transitions t
		JOIN smeldr_state_flows f ON t.flow_id = f.id
		WHERE f.type_name = 'testPost' AND t.from_state = 'draft' AND t.to_state = 'published'
	`).Scan(&transID); err != nil {
		t.Fatalf("get transition id: %v", err)
	}
	res, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_transition_triggers (transition_id, trigger_class, trigger_type, config) VALUES (?, ?, ?, ?)`,
		transID, triggerClass, triggerType, `{}`,
	)
	if err != nil {
		t.Fatalf("insert trigger: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func TestFireAsyncTriggers_nilDB(t *testing.T) {
	// Must not panic and return immediately.
	fireAsyncTriggers(context.Background(), nil, "testPost", "draft", "published")
}

func TestFireAsyncTriggers_nonSQLite(t *testing.T) {
	// failOnNthExecDB returns no-row on QueryRowContext → sqlite_master probe fails → return.
	db := &failOnNthExecDB{failAt: 999}
	fireAsyncTriggers(context.Background(), db, "testPost", "draft", "published")
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
	fireAsyncTriggers(ctx, db, "testPost", "draft", "published")
}

func TestFireAsyncTriggers_syncTrigger_skipped(t *testing.T) {
	db := newSQLiteDB(t)
	setupTriggerFlow(t, db, "sync", "create-signal")

	prev := slog.Default()
	t.Cleanup(func() { restoreDefaultLogging(prev) })
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))

	fireAsyncTriggers(context.Background(), db, "testPost", "draft", "published")
	time.Sleep(30 * time.Millisecond)

	if strings.Contains(buf.String(), "fireAsyncTriggers dispatch") {
		t.Error("sync trigger should not be dispatched, but slog shows a dispatch message")
	}
}

func TestFireAsyncTriggers_asyncTrigger_dispatched(t *testing.T) {
	db := newSQLiteDB(t)
	setupTriggerFlow(t, db, "async", "create-signal")

	prev := slog.Default()
	t.Cleanup(func() { restoreDefaultLogging(prev) })
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))

	fireAsyncTriggers(context.Background(), db, "testPost", "draft", "published")
	time.Sleep(50 * time.Millisecond)

	if !strings.Contains(buf.String(), "fireAsyncTriggers dispatch") {
		t.Error("async trigger: want slog.Info dispatch message, got none")
	}
	if !strings.Contains(buf.String(), "create-signal") {
		t.Error("async trigger: want trigger_type=create-signal in log, missing")
	}
}

func TestFireAsyncTriggers_queryError(t *testing.T) {
	// triggerQueryFailDB: probe succeeds, QueryContext returns an error → fail-open.
	db := &triggerQueryFailDB{}
	fireAsyncTriggers(context.Background(), db, "testPost", "draft", "published")
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
	fireAsyncTriggers(context.Background(), db, "testPost", "draft", "published")
}

func TestFireAsyncTriggers_rowsError(t *testing.T) {
	// triggerRowsErrDB: probe succeeds; driver.Rows.Next returns non-EOF error — rows.Err() path.
	db := &triggerRowsErrDB{}
	fireAsyncTriggers(context.Background(), db, "testPost", "draft", "published")
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
	setupTriggerFlow(t, db, "async", "create-signal")

	repo := &DynamicTypeRepo{db: db, typeName: "testPost"}
	node, err := repo.CreateDraft(ctx, map[string]any{"title": "Trigger Test"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}

	prev := slog.Default()
	t.Cleanup(func() { restoreDefaultLogging(prev) })
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))

	if err := repo.SetStatus(ctx, node.ID, Published); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	if !strings.Contains(buf.String(), "fireAsyncTriggers dispatch") {
		t.Errorf("SetStatus: want async trigger dispatch log, got:\n%s", buf.String())
	}
}
