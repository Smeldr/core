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
		// sqlite_master probe — return count=0 to LifecycleEvent SQLite.
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
	m.setAfterHook(func(_ Context, _ LifecycleEvent, _ afterHookMeta, _ any) {
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
	m.setAfterHook(func(_ Context, _ LifecycleEvent, _ afterHookMeta, _ any) {
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
		`INSERT INTO smeldr_state_flows (name, type_name, active_state, conflict_policy)
		 VALUES ('unknown-policy-flow', 'UnknownPolicyType', 'published', 'custom-policy')`,
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
	triggered, skipped, err := app.DrainEvalQueue(context.Background())
	if err != nil {
		t.Fatalf("nil DB: expected nil error, got %v", err)
	}
	if triggered != 0 || skipped != 0 {
		t.Errorf("nil DB: expected (0,0), got (%d,%d)", triggered, skipped)
	}
}

func TestDrainEvalQueue_noTable(t *testing.T) {
	// Fresh SQLite with no tables → "no such table" → fail-open.
	db := newSQLiteDB(t)
	app := &App{cfg: Config{DB: db}}
	triggered, skipped, err := app.DrainEvalQueue(context.Background())
	if err != nil {
		t.Fatalf("no table: expected nil (fail-open), got %v", err)
	}
	if triggered != 0 || skipped != 0 {
		t.Errorf("no table: expected (0,0), got (%d,%d)", triggered, skipped)
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
	triggered, skipped, err := app.DrainEvalQueue(ctx)
	if err != nil {
		t.Fatalf("DrainEvalQueue: %v", err)
	}
	if triggered != 1 || skipped != 0 {
		t.Errorf("happy: expected (1,0), got (%d,%d)", triggered, skipped)
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
	triggered, skipped, err := app.DrainEvalQueue(ctx)
	if err != nil {
		t.Fatalf("DrainEvalQueue: %v", err)
	}
	if triggered != 0 || skipped != 0 {
		t.Errorf("not due: expected (0,0), got (%d,%d)", triggered, skipped)
	}
}

func TestDrainEvalQueue_transitionFail(t *testing.T) {
	// Queue row due now but target table missing → UPDATE fails → skipped++, row deleted.
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
	triggered, skipped, err := app.DrainEvalQueue(ctx)
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
	_, _, err := app.DrainEvalQueue(ctx)
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
	_, _, err := app.DrainEvalQueue(ctx)
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
	triggered, skipped, err := app.DrainEvalQueue(ctx)
	if err != nil {
		t.Fatalf("scanFail: unexpected error: %v", err)
	}
	// scan failed → skipped++ (row still deleted)
	if triggered != 0 || skipped != 1 {
		t.Errorf("scanFail: expected (0,1), got (%d,%d)", triggered, skipped)
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
