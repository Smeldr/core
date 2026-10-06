//go:build integration

package pgx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	smeldr "smeldr.dev/core"
)

// This file is the Postgres proof for D103: the state machine (transition rules
// and role gates, RequiredReason, async triggers, ConflictPolicy, the initial
// state, Locked, EnsureColumn on an older schema) is enforced on Postgres exactly
// as it is on SQLite. Core cannot import a driver, so it cannot run these in its
// own package; they use exported API only and need DATABASE_URL, like every other
// integration test here.

const pgTestSecret = "integration-test-secret-32bytes!"

// isolatedDB gives the test a database of its own: a fresh schema, selected on
// every connection of the pool through search_path, dropped on cleanup. Tests run
// in one database, so without it they would see each other's tables and flows.
func isolatedDB(t *testing.T) (smeldr.DB, string) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping Postgres integration test")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	schema := "t_" + strings.ReplaceAll(strings.ToLower(smeldr.NewID()), "-", "")
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}
	pool, err := poolInSchema(ctx, dsn, schema)
	if err != nil {
		t.Fatalf("pool in schema: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		if os.Getenv("PG_KEEP_SCHEMA") == "" {
			_, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		}
		admin.Close()
	})
	return Wrap(pool), schema
}

func poolInSchema(ctx context.Context, dsn, schema string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	return pgxpool.NewWithConfig(ctx, cfg)
}

func newPGApp(t *testing.T, db smeldr.DB) (*smeldr.App, *smeldr.RoleStore) {
	t.Helper()
	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte(pgTestSecret), DB: db})
	store := smeldr.NewRoleStore(db)
	if err := app.Governance(store); err != nil {
		t.Fatalf("Governance: %v", err)
	}
	return app, store
}

// createItemsTable creates the table a compiled type named typeName (CamelCase,
// letters only) is found in: <snake>s, which resolveItemTable probes for.
func createItemsTable(t *testing.T, db smeldr.DB, table string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), `CREATE TABLE `+table+` (
		id           TEXT PRIMARY KEY,
		slug         TEXT NOT NULL,
		status       TEXT NOT NULL,
		next_eval_at TIMESTAMPTZ,
		last_actor   TEXT NOT NULL DEFAULT '',
		updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		t.Fatalf("create %s: %v", table, err)
	}
}

func insertItem(t *testing.T, db smeldr.DB, table, id, status string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO `+table+` (id, slug, status) VALUES ($1, $1, $2)`, id, status); err != nil {
		t.Fatalf("insert %s %s: %v", table, id, err)
	}
}

func statusOf(t *testing.T, db smeldr.DB, table, id string) string {
	t.Helper()
	var s string
	if err := db.QueryRowContext(context.Background(), `SELECT status FROM `+table+` WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatalf("status of %s: %v", id, err)
	}
	return s
}

func registerType(app *smeldr.App, name string) {
	app.TypeRegistry().Register(&smeldr.TypeDescriptor{Name: name, Kind: "compiled"})
}

// pgFlow is the flow most tests share: draft -> review -> published, with a
// reason-gated door to archived and a strict operation-gated door to published.
func pgFlow(typeName string) smeldr.StateFlow {
	return smeldr.StateFlow{
		Name:           "pg-flow-" + strings.ToLower(typeName),
		TypeName:       typeName,
		ActiveState:    "published",
		ConflictPolicy: smeldr.ConflictReject,
		States: []smeldr.State{
			{Name: "draft", IsInitial: true},
			{Name: "review"},
			{Name: "published"},
			{Name: "archived", IsTerminal: true},
		},
		Transitions: []smeldr.Transition{
			{From: "draft", To: "review"},
			{From: "review", To: "published", RequiredOperation: "approve", Strict: true},
			{From: "draft", To: "archived", RequiredReason: true},
			{From: "review", To: "archived"},
		},
		Triggers: []smeldr.TransitionTrigger{
			{FromState: "draft", ToState: "review", TriggerClass: "async", TriggerType: "schedule-eval",
				Config: `{"eval_field":"next_eval_at","to_state":"archived"}`},
		},
	}
}

func TestPG_StateMachine_RejectsATransitionNotInTheFlow(t *testing.T) {
	db, _ := isolatedDB(t)
	app, _ := newPGApp(t, db)
	createItemsTable(t, db, "pg_items")
	registerType(app, "PgItem")
	if err := app.RegisterFlow(pgFlow("PgItem")); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	insertItem(t, db, "pg_items", "a", "draft")
	ctx := context.Background()

	if _, err := app.TransitionItem(ctx, "PgItem", "a", "published"); !errors.Is(err, smeldr.ErrConflict) {
		t.Errorf("draft -> published is not in the flow: err = %v, want ErrConflict", err)
	}
	if got := statusOf(t, db, "pg_items", "a"); got != "draft" {
		t.Errorf("a refused transition changed the status to %q", got)
	}
	if _, err := app.TransitionItem(ctx, "PgItem", "a", "nonsense"); err == nil {
		t.Error("a state the flow does not define must be refused")
	}
	if _, err := app.TransitionItem(ctx, "PgItem", "a", "review"); err != nil {
		t.Fatalf("draft -> review is in the flow: %v", err)
	}
	if got := statusOf(t, db, "pg_items", "a"); got != "review" {
		t.Errorf("status = %q, want review", got)
	}
}

func TestPG_StateMachine_RoleGate(t *testing.T) {
	db, _ := isolatedDB(t)
	app, store := newPGApp(t, db)
	createItemsTable(t, db, "pg_items")
	registerType(app, "PgItem")
	if err := app.RegisterFlow(pgFlow("PgItem")); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	ctx := context.Background()
	insertItem(t, db, "pg_items", "a", "review")

	if err := store.DefineRole(ctx, smeldr.RoleDefinition{
		Name: "pg-approver", Operations: []string{"approve"}, ScopeMode: smeldr.ScopeGlobal,
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	tokenID := "pg-token-" + smeldr.NewID()
	if _, err := store.Grant(ctx, smeldr.RoleGrant{TokenID: tokenID, RoleName: "pg-approver"}); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	// The gate is the security side of D103: before, on Postgres, this passed
	// for anyone, because the flow was stored and never consulted.
	if _, err := app.TransitionItem(smeldr.NewTestContext(smeldr.User{ID: "nobody"}), "PgItem", "a", "published"); !errors.Is(err, smeldr.ErrForbidden) {
		t.Errorf("an actor without the operation: err = %v, want ErrForbidden", err)
	}
	if got := statusOf(t, db, "pg_items", "a"); got != "review" {
		t.Errorf("a forbidden transition changed the status to %q", got)
	}
	if _, err := app.TransitionItem(smeldr.NewTestContext(smeldr.User{ID: tokenID}), "PgItem", "a", "published"); err != nil {
		t.Fatalf("an actor with the operation: %v", err)
	}
	if got := statusOf(t, db, "pg_items", "a"); got != "published" {
		t.Errorf("status = %q, want published", got)
	}
}

func TestPG_StateMachine_RequiredReason(t *testing.T) {
	db, _ := isolatedDB(t)
	app, _ := newPGApp(t, db)
	createItemsTable(t, db, "pg_items")
	registerType(app, "PgItem")
	if err := app.RegisterFlow(pgFlow("PgItem")); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	insertItem(t, db, "pg_items", "a", "draft")
	ctx := context.Background()

	if _, err := app.TransitionItem(ctx, "PgItem", "a", "archived"); !errors.Is(err, smeldr.ErrBadRequest) {
		t.Errorf("no reason: err = %v, want ErrBadRequest", err)
	}
	if _, err := app.TransitionItemWithReason(ctx, "PgItem", "a", "archived", "not needed any more"); err != nil {
		t.Fatalf("with a reason: %v", err)
	}
}

func TestPG_StateMachine_AsyncTriggerQueuesAnEvaluation(t *testing.T) {
	db, _ := isolatedDB(t)
	app, _ := newPGApp(t, db)
	createItemsTable(t, db, "pg_items")
	registerType(app, "PgItem")
	if err := app.RegisterFlow(pgFlow("PgItem")); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	insertItem(t, db, "pg_items", "a", "draft")
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `UPDATE pg_items SET next_eval_at = $1 WHERE id = 'a'`, time.Now().Add(time.Hour).UTC()); err != nil {
		t.Fatalf("set next_eval_at: %v", err)
	}

	if _, err := app.TransitionItem(ctx, "PgItem", "a", "review"); err != nil {
		t.Fatalf("TransitionItem: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_eval_queue WHERE item_id = 'a'`).Scan(&n); err != nil {
			t.Fatalf("read eval queue: %v", err)
		}
		if n == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the async trigger queued %d evaluations, want 1", n)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestPG_StateMachine_ConflictPolicyReject(t *testing.T) {
	db, _ := isolatedDB(t)
	app, store := newPGApp(t, db)
	createItemsTable(t, db, "pg_items")
	registerType(app, "PgItem")
	if err := app.RegisterFlow(pgFlow("PgItem")); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	ctx := context.Background()
	if err := store.DefineRole(ctx, smeldr.RoleDefinition{Name: "pg-approver", Operations: []string{"approve"}, ScopeMode: smeldr.ScopeGlobal}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	tokenID := "pg-token-" + smeldr.NewID()
	if _, err := store.Grant(ctx, smeldr.RoleGrant{TokenID: tokenID, RoleName: "pg-approver"}); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	actor := smeldr.NewTestContext(smeldr.User{ID: tokenID})
	insertItem(t, db, "pg_items", "a", "published")
	insertItem(t, db, "pg_items", "b", "review")

	if _, err := app.TransitionItem(actor, "PgItem", "b", "published"); !errors.Is(err, smeldr.ErrConflict) {
		t.Errorf("a second item into the active state: err = %v, want ErrConflict", err)
	}
	if got := statusOf(t, db, "pg_items", "b"); got != "review" {
		t.Errorf("a refused item changed to %q", got)
	}
	if got := statusOf(t, db, "pg_items", "a"); got != "published" {
		t.Errorf("the existing item changed to %q", got)
	}
}

func TestPG_StateMachine_ConflictPolicySupersede(t *testing.T) {
	db, _ := isolatedDB(t)
	app, store := newPGApp(t, db)
	createItemsTable(t, db, "pg_items")
	registerType(app, "PgItem")
	flow := pgFlow("PgItem")
	flow.ConflictPolicy = smeldr.ConflictSupersede
	flow.States = append(flow.States, smeldr.State{Name: "superseded"})
	flow.Transitions = append(flow.Transitions, smeldr.Transition{From: "published", To: "superseded"})
	if err := app.RegisterFlow(flow); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	ctx := context.Background()
	if err := store.DefineRole(ctx, smeldr.RoleDefinition{Name: "pg-approver", Operations: []string{"approve"}, ScopeMode: smeldr.ScopeGlobal}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	tokenID := "pg-token-" + smeldr.NewID()
	if _, err := store.Grant(ctx, smeldr.RoleGrant{TokenID: tokenID, RoleName: "pg-approver"}); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	insertItem(t, db, "pg_items", "a", "published")
	insertItem(t, db, "pg_items", "b", "review")

	if _, err := app.TransitionItem(smeldr.NewTestContext(smeldr.User{ID: tokenID}), "PgItem", "b", "published"); err != nil {
		t.Fatalf("TransitionItem: %v", err)
	}
	if got := statusOf(t, db, "pg_items", "b"); got != "published" {
		t.Errorf("the winner = %q, want published", got)
	}
	if got := statusOf(t, db, "pg_items", "a"); got != "superseded" {
		t.Errorf("the previous holder = %q, want superseded", got)
	}
}

// TestPG_EnsureColumn_UpgradesAnOlderSchema: a Postgres database created by an older
// core has no strict/required_reason/locked columns, and EnsureColumn used to do
// nothing there. With enforcement on, the state machine would then fail on every
// transition, so the upgrade has to work.
func TestPG_EnsureColumn_UpgradesAnOlderSchema(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE old_things (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create old_things: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO old_things (id) VALUES ('x')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	for i := 0; i < 2; i++ { // the second call is the idempotent no-op
		if err := smeldr.EnsureColumn(ctx, db, "old_things", "note", "TEXT NOT NULL DEFAULT ''"); err != nil {
			t.Fatalf("EnsureColumn (call %d): %v", i+1, err)
		}
	}
	var note string
	if err := db.QueryRowContext(ctx, `SELECT note FROM old_things WHERE id = 'x'`).Scan(&note); err != nil {
		t.Fatalf("read the added column: %v", err)
	}
	if err := smeldr.EnsureColumn(ctx, db, "no_such_table", "x", "TEXT"); err == nil {
		t.Error("a table that is not there must be an error, as on SQLite")
	}
}

// TestPG_EnsureColumn_ConcurrentBoot: two processes booting together can both find
// the column absent and both add it; the loser's duplicate-column error is success.
func TestPG_EnsureColumn_ConcurrentBoot(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE raced (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- smeldr.EnsureColumn(ctx, db, "raced", "extra", "TEXT NOT NULL DEFAULT ''")
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("a concurrent EnsureColumn failed: %v", err)
		}
	}
}

// TestPG_Boot_OlderStateFlowSchema boots an App on a database whose state flow
// tables predate the columns D103's enforcement reads, and checks the columns are
// added and a flow then works.
func TestPG_Boot_OlderStateFlowSchema(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	for _, ddl := range []string{
		`CREATE TABLE smeldr_state_flows (id TEXT NOT NULL PRIMARY KEY, name TEXT NOT NULL UNIQUE, type_name TEXT, description TEXT, created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE smeldr_states (id TEXT NOT NULL PRIMARY KEY, flow_id TEXT NOT NULL REFERENCES smeldr_state_flows(id), name TEXT NOT NULL, is_initial BOOLEAN NOT NULL DEFAULT FALSE, is_terminal BOOLEAN NOT NULL DEFAULT FALSE, suppresses_signals BOOLEAN NOT NULL DEFAULT FALSE, UNIQUE(flow_id, name))`,
		`CREATE TABLE smeldr_transitions (id TEXT NOT NULL PRIMARY KEY, flow_id TEXT NOT NULL REFERENCES smeldr_state_flows(id), from_state TEXT NOT NULL, to_state TEXT NOT NULL, required_role TEXT, UNIQUE(flow_id, from_state, to_state))`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatalf("old schema: %v", err)
		}
	}
	app, _ := newPGApp(t, db)
	createItemsTable(t, db, "pg_items")
	registerType(app, "PgItem")
	if err := app.RegisterFlow(pgFlow("PgItem")); err != nil {
		t.Fatalf("RegisterFlow on an upgraded schema: %v", err)
	}
	for table, cols := range map[string][]string{
		"smeldr_transitions": {"required_reason", "strict"},
		"smeldr_states":      {"locked", "standing"},
		"smeldr_state_flows": {"active_state", "conflict_policy"},
	} {
		for _, col := range cols {
			if _, err := db.ExecContext(ctx, `SELECT `+table+`.`+col+` FROM `+table+` WHERE 1=0`); err != nil {
				t.Errorf("%s.%s was not added: %v", table, col, err)
			}
		}
	}
	insertItem(t, db, "pg_items", "a", "draft")
	if _, err := app.TransitionItem(ctx, "PgItem", "a", "published"); !errors.Is(err, smeldr.ErrConflict) {
		t.Errorf("the flow must be enforced after the upgrade: err = %v, want ErrConflict", err)
	}
}

// ———— The cross-process ConflictReject race ————————————————————————————————————

// raceTypes are the types one race run uses, one per round, so that every round
// starts with no item in the active state. prefix is one capital and lowercase
// letters, then two lowercase letters for the round: the table name is the snake
// case of the type name, plus an s.
func raceTypes(prefix string, rounds int) []string {
	out := make([]string, rounds)
	for i := range out {
		out[i] = prefix + string(rune('a'+i/26)) + string(rune('a'+i%26))
	}
	return out
}

func raceTable(typeName string) string { return strings.ToLower(typeName) + "s" }

// raceResult counts, per round, how many items ended in the active state.
type raceResult struct{ one, both, none int }

// runRace moves item "a" and item "b" of each round's type into the active state
// from two separate OS processes, "b" offsetB after "a" (zero is the same moment).
// Each process is the test binary re-run as TestPG_RaceChild.
// raceFlow is pgFlow with the policy under test; supersede needs a state to move the
// previous holder to.
func raceFlow(typeName string, policy smeldr.ConflictPolicy) smeldr.StateFlow {
	f := pgFlow(typeName)
	f.ConflictPolicy = policy
	if policy == smeldr.ConflictSupersede {
		f.States = append(f.States, smeldr.State{Name: "superseded"})
		f.Transitions = append(f.Transitions, smeldr.Transition{From: "published", To: "superseded"})
	}
	return f
}

func runRace(t *testing.T, db smeldr.DB, app *smeldr.App, schema, tokenID, prefix string, policy smeldr.ConflictPolicy, rounds int, offsetB time.Duration) raceResult {
	t.Helper()
	ctx := context.Background()
	types := raceTypes(prefix, rounds)
	for _, ty := range types {
		createItemsTable(t, db, raceTable(ty))
		registerType(app, ty)
		if err := app.RegisterFlow(raceFlow(ty, policy)); err != nil {
			t.Fatalf("RegisterFlow %s: %v", ty, err)
		}
		insertItem(t, db, raceTable(ty), "a", "review")
		insertItem(t, db, raceTable(ty), "b", "review")
	}

	start := time.Now().Add(3 * time.Second)
	var wg sync.WaitGroup
	outs := make([]string, 2)
	for i, item := range []string{"a", "b"} {
		offset := time.Duration(0)
		if item == "b" {
			offset = offsetB
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=^TestPG_RaceChild$", "-test.v")
			cmd.Env = append(os.Environ(),
				"PG_RACE_CHILD=1", "PG_RACE_SCHEMA="+schema, "PG_RACE_ITEM="+item, "PG_RACE_TOKEN="+tokenID,
				"PG_RACE_PREFIX="+prefix, "PG_RACE_START="+strconv.FormatInt(start.Add(offset).UnixNano(), 10),
				"PG_RACE_ROUNDS="+strconv.Itoa(rounds))
			out, err := cmd.CombinedOutput()
			outs[i] = string(out)
			if err != nil {
				t.Errorf("child %s: %v\n%s", item, err, out)
			}
		}()
	}
	wg.Wait()
	for _, o := range outs {
		for _, line := range strings.Split(o, "\n") {
			if strings.HasPrefix(line, "child ") {
				t.Log(line)
			}
		}
	}

	var res raceResult
	for _, ty := range types {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+raceTable(ty)+` WHERE status = 'published'`).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", ty, err)
		}
		switch n {
		case 0:
			res.none++
		case 1:
			res.one++
		default:
			res.both++
		}
	}
	return res
}

// TestPG_ConflictPolicy_TwoProcesses asks the question the in-process lock (A405)
// cannot answer: whether two processes, not two goroutines, can both pass the
// policy's check. Before the cross-process lock (the pgx adapter's AcquireLock) they
// could: the reject check and the winner's write are separate statements under
// READ COMMITTED, and every simultaneous round ended with two holders (A409).
//
// Three runs, 25 rounds each, each round a fresh type: a control with one process
// 250 ms behind the other (always one holder, so the harness and the rejection work
// across processes); ConflictReject with both at the same moment; ConflictSupersede
// with both at the same moment. The last two must end every round with exactly one
// item in the active state.
func TestPG_ConflictPolicy_TwoProcesses(t *testing.T) {
	if os.Getenv("PG_RACE_CHILD") != "" {
		t.Skip("child process")
	}
	const rounds = 25
	db, schema := isolatedDB(t)
	app, store := newPGApp(t, db)
	ctx := context.Background()
	if err := store.DefineRole(ctx, smeldr.RoleDefinition{Name: "pg-approver", Operations: []string{"approve"}, ScopeMode: smeldr.ScopeGlobal}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	tokenID := "pg-token-" + smeldr.NewID()
	if _, err := store.Grant(ctx, smeldr.RoleGrant{TokenID: tokenID, RoleName: "pg-approver"}); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	control := runRace(t, db, app, schema, tokenID, "Ctrl", smeldr.ConflictReject, rounds, 250*time.Millisecond)
	t.Logf("control, reject, one process 250 ms behind the other, %d rounds: %d with one holder, %d with two, %d with none", rounds, control.one, control.both, control.none)
	if control.one != rounds {
		t.Errorf("the control must end every round with exactly one holder: %+v", control)
	}

	reject := runRace(t, db, app, schema, tokenID, "Rej", smeldr.ConflictReject, rounds, 0)
	t.Logf("reject, two processes at the same moment, %d rounds: %d with one holder, %d with TWO holders, %d with none", rounds, reject.one, reject.both, reject.none)
	if reject.one != rounds {
		t.Errorf("ConflictReject must end every simultaneous round with exactly one holder across processes: %+v", reject)
	}

	supersede := runRace(t, db, app, schema, tokenID, "Sup", smeldr.ConflictSupersede, rounds, 0)
	t.Logf("supersede, two processes at the same moment, %d rounds: %d with one active item, %d with two, %d with none", rounds, supersede.one, supersede.both, supersede.none)
	if supersede.one != rounds {
		t.Errorf("ConflictSupersede must end every simultaneous round with exactly one active item: %+v", supersede)
	}
}

// TestPG_RaceChild is the child process of TestPG_ConflictPolicy_TwoProcesses: it
// is not a test in a normal run (it skips without PG_RACE_CHILD). It moves its
// item into the active state of each round's type at that round's slot.
func TestPG_RaceChild(t *testing.T) {
	if os.Getenv("PG_RACE_CHILD") == "" {
		t.Skip("only runs as the child of TestPG_ConflictPolicy_TwoProcesses")
	}
	ctx := context.Background()
	rounds, _ := strconv.Atoi(os.Getenv("PG_RACE_ROUNDS"))
	startNs, _ := strconv.ParseInt(os.Getenv("PG_RACE_START"), 10, 64)
	pool, err := poolInSchema(ctx, os.Getenv("DATABASE_URL"), os.Getenv("PG_RACE_SCHEMA"))
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()
	db := Wrap(pool)
	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte(pgTestSecret), DB: db})
	store := smeldr.NewRoleStore(db)
	if err := app.Governance(store); err != nil {
		t.Fatalf("Governance: %v", err)
	}
	actor := smeldr.NewTestContext(smeldr.User{ID: os.Getenv("PG_RACE_TOKEN")})
	types := raceTypes(os.Getenv("PG_RACE_PREFIX"), rounds)
	for _, ty := range types {
		registerType(app, ty)
	}
	won, lost := 0, 0
	for i, ty := range types {
		slot := time.Unix(0, startNs).Add(time.Duration(i) * 400 * time.Millisecond)
		time.Sleep(time.Until(slot))
		if _, err := app.TransitionItem(actor, ty, os.Getenv("PG_RACE_ITEM"), "published"); err != nil {
			lost++
		} else {
			won++
		}
	}
	fmt.Printf("child %s: won %d, refused %d\n", os.Getenv("PG_RACE_ITEM"), won, lost)
}
