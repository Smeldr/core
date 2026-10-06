// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// pgError is the shape of a Postgres driver error: a message and a SQLSTATE. It
// stands in for pgx's *pgconn.PgError, which core cannot import.
type pgError struct{ code, msg string }

func (e *pgError) Error() string    { return e.msg }
func (e *pgError) SQLState() string { return e.code }

// failingDB is a real SQLite handle that fails the queries containing match with
// err, and passes everything else through. It lets a test hand the code under test
// the error a Postgres driver would return, which SQLite never produces.
type failingDB struct {
	DB
	match string
	err   error
}

func (d failingDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if strings.Contains(q, d.match) {
		return nil, d.err
	}
	return d.DB.QueryContext(ctx, q, args...)
}

func cancelled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestIsNoSuchTable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"sqlite text", errors.New("SQL logic error: no such table: smeldr_x (1)"), true},
		{"postgres text", errors.New(`ERROR: relation "smeldr_x" does not exist`), true},
		{"postgres sqlstate only", &pgError{"42P01", "something the driver says"}, true},
		{"postgres sqlstate wrapped", fmt.Errorf("query: %w", &pgError{"42P01", "x"}), true},
		{"other sqlstate", &pgError{"42703", "column gone"}, false},
		{"a missing column is not a missing table", errors.New(`column "x" does not exist`), false},
		{"unrelated", errors.New("connection reset"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNoSuchTable(tt.err); got != tt.want {
				t.Errorf("isNoSuchTable(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsNoSuchColumn(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"sqlite select", errors.New("no such column: last_actor"), true},
		{"sqlite select, qualified by table", errors.New("SQL logic error: no such column: t.last_actor (1)"), true},
		{"sqlite, a longer column name", errors.New("no such column: last_actor_at"), false},
		{"sqlite insert", errors.New("table t has no column named last_actor"), true},
		{"postgres select", errors.New(`ERROR: column "last_actor" does not exist (SQLSTATE 42703)`), true},
		{"postgres insert", errors.New(`ERROR: column "last_actor" of relation "t" does not exist`), true},
		{"postgres select, qualified by table (unquoted)", errors.New(`ERROR: column t.last_actor does not exist (SQLSTATE 42703)`), true},
		{"postgres select, qualified, another column", errors.New(`ERROR: column t.other does not exist (SQLSTATE 42703)`), false},
		{"postgres select, a longer column", errors.New(`ERROR: column t.last_actor_at does not exist (SQLSTATE 42703)`), false},
		{"postgres sqlstate with the column in the text", &pgError{"42703", `column "last_actor" does not exist`}, true},
		{"postgres wrapped", fmt.Errorf("update: %w", &pgError{"42703", `column "last_actor" does not exist`}), true},
		{"another column", errors.New(`column "other" does not exist`), false},
		{"another column on sqlite", errors.New("no such column: other"), false},
		{"a missing table is not a missing column", errors.New(`relation "t" does not exist`), false},
		{"unrelated", errors.New("connection reset"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNoSuchColumn(tt.err, "last_actor"); got != tt.want {
				t.Errorf("isNoSuchColumn(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsDuplicateColumn(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"sqlite", errors.New("SQL logic error: duplicate column name: strict (1)"), true},
		{"postgres text", errors.New(`ERROR: column "strict" of relation "t" already exists`), true},
		{"postgres sqlstate", &pgError{"42701", `column "strict" of relation "t" already exists`}, true},
		{"another column", errors.New("duplicate column name: other"), false},
		{"another column on postgres", &pgError{"42701", `column "other" of relation "t" already exists`}, false},
		{"unrelated", errors.New("connection reset"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDuplicateColumn(tt.err, "strict"); got != tt.want {
				t.Errorf("isDuplicateColumn(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestTableExists(t *testing.T) {
	db := newMigratedDB(t)
	ctx := context.Background()
	if ok, err := tableExists(ctx, db, "smeldr_state_flows"); !ok || err != nil {
		t.Errorf("a table that is there = %v, %v, want true, nil", ok, err)
	}
	if ok, err := tableExists(ctx, db, "no_such_table"); ok || err != nil {
		t.Errorf("a table that is not there = %v, %v, want false, nil", ok, err)
	}
	pg := failingDB{db, "no_such_table", &pgError{"42P01", `relation "no_such_table" does not exist`}}
	if ok, err := tableExists(ctx, pg, "no_such_table"); ok || err != nil {
		t.Errorf("a Postgres missing-table error = %v, %v, want false, nil", ok, err)
	}
	broken := failingDB{db, "smeldr_state_flows", errors.New("permission denied")}
	if ok, err := tableExists(ctx, broken, "smeldr_state_flows"); ok || err == nil {
		t.Errorf("another error = %v, %v, want it returned, not read as absent", ok, err)
	}
	if ok, err := tableExists(cancelled(), db, "smeldr_state_flows"); ok || !errors.Is(err, ErrInternal) {
		t.Errorf("a dead context = %v, %v, want false and ErrInternal", ok, err)
	}
}

func TestColumnExists(t *testing.T) {
	db := newMigratedDB(t)
	ctx := context.Background()
	if ok, err := columnExists(ctx, db, "smeldr_transitions", "strict"); !ok || err != nil {
		t.Errorf("a column that is there = %v, %v, want true, nil", ok, err)
	}
	if ok, err := columnExists(ctx, db, "smeldr_transitions", "no_such_column"); ok || err != nil {
		t.Errorf("a column that is not there = %v, %v, want false, nil", ok, err)
	}
	if ok, err := columnExists(ctx, db, "no_such_table", "x"); ok || err != nil {
		t.Errorf("a missing table = %v, %v, want false, nil (the ALTER then says so)", ok, err)
	}
	pg := failingDB{db, "no_such_column", &pgError{"42703", `column "no_such_column" does not exist`}}
	if ok, err := columnExists(ctx, pg, "smeldr_transitions", "no_such_column"); ok || err != nil {
		t.Errorf("a Postgres missing-column error = %v, %v, want false, nil", ok, err)
	}
	broken := failingDB{db, "strict", errors.New("permission denied")}
	if ok, err := columnExists(ctx, broken, "smeldr_transitions", "strict"); ok || err == nil {
		t.Errorf("another error = %v, %v, want it returned", ok, err)
	}
	if ok, err := columnExists(cancelled(), db, "smeldr_transitions", "strict"); ok || !errors.Is(err, ErrInternal) {
		t.Errorf("a dead context = %v, %v, want false and ErrInternal", ok, err)
	}
}

// TestProbes_RefuseATransaction: on Postgres a failed statement aborts the whole
// transaction, and a probe fails by design, so one handed a transaction is an
// error, never a poisoned transaction.
func TestProbes_RefuseATransaction(t *testing.T) {
	db := newMigratedDB(t)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback() //nolint:errcheck
	ctx := context.Background()
	if _, err := tableExists(ctx, tx, "smeldr_state_flows"); !errors.Is(err, ErrInternal) {
		t.Errorf("tableExists in a transaction: err = %v, want ErrInternal", err)
	}
	if _, err := columnExists(ctx, tx, "smeldr_transitions", "strict"); !errors.Is(err, ErrInternal) {
		t.Errorf("columnExists in a transaction: err = %v, want ErrInternal", err)
	}
	if _, err := flowTablesPresent(ctx, tx); !errors.Is(err, ErrInternal) {
		t.Errorf("flowTablesPresent in a transaction: err = %v, want ErrInternal", err)
	}
}

func TestFlowTablesPresent(t *testing.T) {
	ctx := context.Background()
	db := newMigratedDB(t)
	if ok, err := flowTablesPresent(ctx, db); !ok || err != nil {
		t.Errorf("flow tables there, empty = %v, %v, want true, nil", ok, err)
	}
	if err := (&App{cfg: Config{DB: db}}).RegisterFlow(StateFlow{
		Name: "f", TypeName: "T", States: []State{{Name: "draft", IsInitial: true}},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	if ok, err := flowTablesPresent(ctx, db); !ok || err != nil {
		t.Errorf("flow tables there, with rows = %v, %v, want true, nil", ok, err)
	}

	for name, missing := range map[string]error{
		"sqlite text":    errors.New("SQL logic error: no such table: smeldr_state_flows (1)"),
		"postgres text":  errors.New(`ERROR: relation "smeldr_state_flows" does not exist`),
		"postgres state": &pgError{"42P01", "driver text"},
	} {
		t.Run("missing table, "+name, func(t *testing.T) {
			ok, err := flowTablesPresent(ctx, failingDB{db, "smeldr_state_flows", missing})
			if ok || err != nil {
				t.Errorf("= %v, %v, want false, nil (no flow tables, nothing to enforce)", ok, err)
			}
		})
	}

	t.Run("another error is an error, not a skip", func(t *testing.T) {
		ok, err := flowTablesPresent(ctx, failingDB{db, "smeldr_state_flows", errors.New("connection reset")})
		if ok || !errors.Is(err, ErrInternal) {
			t.Errorf("= %v, %v, want false and ErrInternal", ok, err)
		}
	})
	t.Run("a dead context is an error, not a skip", func(t *testing.T) {
		ok, err := flowTablesPresent(cancelled(), db)
		if ok || !errors.Is(err, ErrInternal) {
			t.Errorf("= %v, %v, want false and ErrInternal", ok, err)
		}
		ok, err = flowTablesPresent(cancelled(), failingDB{db, "smeldr_state_flows", &pgError{"42P01", "x"}})
		if ok || !errors.Is(err, ErrInternal) {
			t.Errorf("dead context on a missing table = %v, %v, want ErrInternal, not a skip", ok, err)
		}
	})
}

// TestDeadContext_IsAnErrorNotASkip: before, a context that had already ended made
// the probe fail, and that was read as 'nothing to enforce': the gate, the lock or
// the policy was silently skipped for that call.
func TestDeadContext_IsAnErrorNotASkip(t *testing.T) {
	db := newMigratedDB(t)
	createConflictItemTable(t, db, true, ConflictReject)
	insertConflictItem(t, db, "a", "published")
	insertConflictItem(t, db, "b", "draft")
	dead := cancelled()

	t.Run("validateTransition", func(t *testing.T) {
		err := validateTransition(dead, db, nil, nil, "u", "b", "ConflictType", "draft", "published", "")
		if !errors.Is(err, ErrInternal) {
			t.Errorf("err = %v, want ErrInternal", err)
		}
	})
	t.Run("validateInitialState", func(t *testing.T) {
		if err := validateInitialState(dead, db, "ConflictType", "draft"); !errors.Is(err, ErrInternal) {
			t.Errorf("err = %v, want ErrInternal", err)
		}
	})
	t.Run("validateFlowItems", func(t *testing.T) {
		if err := validateFlowItems(dead, db, StateFlow{Name: "f", TypeName: "T"}); !errors.Is(err, ErrInternal) {
			t.Errorf("err = %v, want ErrInternal", err)
		}
	})
	t.Run("planConflict", func(t *testing.T) {
		plan, err := planConflict(dead, db, "ConflictType", "published", "b")
		if !errors.Is(err, ErrInternal) || plan != nil {
			t.Errorf("plan = %+v, err = %v, want nil and ErrInternal (never a plan that lets b in unchecked)", plan, err)
		}
	})
}

// TestBrokenDatabase_IsAnErrorNotASkip: a probe that fails for a reason other than a
// missing table (a dropped connection, a permission error) is an error to the four
// callers that can return one, and a Warn to the four that cannot.
func TestBrokenDatabase_IsAnErrorNotASkip(t *testing.T) {
	inner := newMigratedDB(t)
	createConflictItemTable(t, inner, true, ConflictReject)
	insertConflictItem(t, inner, "a", "published")
	insertConflictItem(t, inner, "b", "draft")
	db := failingDB{inner, "smeldr_state_flows LIMIT 1", errors.New("connection reset")}
	ctx := context.Background()

	if err := validateTransition(ctx, db, nil, nil, "u", "b", "ConflictType", "draft", "nonsense", ""); !errors.Is(err, ErrInternal) {
		t.Errorf("validateTransition err = %v, want ErrInternal", err)
	}
	if err := validateInitialState(ctx, db, "ConflictType", "draft"); !errors.Is(err, ErrInternal) {
		t.Errorf("validateInitialState err = %v, want ErrInternal", err)
	}
	if err := validateFlowItems(ctx, db, StateFlow{Name: "f", TypeName: "T"}); !errors.Is(err, ErrInternal) {
		t.Errorf("validateFlowItems err = %v, want ErrInternal", err)
	}
	if plan, err := planConflict(ctx, db, "ConflictType", "published", "b"); !errors.Is(err, ErrInternal) || plan != nil {
		t.Errorf("planConflict = %+v, %v, want nil and ErrInternal", plan, err)
	}

	out := capturedLog(func() {
		if isStateLocked(ctx, db, "ConflictType", "published") {
			t.Error("isStateLocked must fail open (false)")
		}
		if suppressesSignals(ctx, db, "ConflictType", "published") {
			t.Error("suppressesSignals must fail open (false)")
		}
		if got := defaultInitialState(ctx, db, "ConflictType"); got != "" {
			t.Errorf("defaultInitialState = %q, want empty", got)
		}
		fireAsyncTriggers(ctx, db, "ConflictType", "draft", "published", "x")
	})
	for _, caller := range []string{"isStateLocked", "suppressesSignals", "defaultInitialState", "fireAsyncTriggers"} {
		if !strings.Contains(out, "caller="+caller) {
			t.Errorf("no Warn for %s:\n%s", caller, out)
		}
	}
	if !strings.Contains(out, "level=WARN") {
		t.Errorf("the skip must be a Warn:\n%s", out)
	}
}

// TestNoFlowTables_NothingToEnforce: with the flow tables absent (the error a
// Postgres driver returns for a missing relation) there is no flow, so nothing is
// enforced and nothing fails.
func TestNoFlowTables_NothingToEnforce(t *testing.T) {
	inner := newMigratedDB(t)
	createConflictItemTable(t, inner, true, ConflictReject)
	insertConflictItem(t, inner, "a", "published")
	db := failingDB{inner, "smeldr_state_flows LIMIT 1", &pgError{"42P01", `relation "smeldr_state_flows" does not exist`}}
	ctx := context.Background()

	if err := validateTransition(ctx, db, nil, nil, "u", "b", "ConflictType", "draft", "nonsense", ""); err != nil {
		t.Errorf("validateTransition = %v, want nil", err)
	}
	if err := validateInitialState(ctx, db, "ConflictType", "nonsense"); err != nil {
		t.Errorf("validateInitialState = %v, want nil", err)
	}
	if err := validateFlowItems(ctx, db, StateFlow{Name: "f", TypeName: "ConflictType", States: []State{{Name: "draft"}}}); err != nil {
		t.Errorf("validateFlowItems = %v, want nil", err)
	}
	if plan, err := planConflict(ctx, db, "ConflictType", "published", "b"); plan != nil || err != nil {
		t.Errorf("planConflict = %+v, %v, want nil, nil", plan, err)
	}
	if isStateLocked(ctx, db, "ConflictType", "published") || suppressesSignals(ctx, db, "ConflictType", "published") {
		t.Error("the fail-open callers must report nothing to enforce")
	}
	if got := defaultInitialState(ctx, db, "ConflictType"); got != "" {
		t.Errorf("defaultInitialState = %q, want empty", got)
	}
}

// TestEnforced_FlowTablesThere: with the flow tables there, an undefined state is
// refused, behind a handle that only differs in the Postgres error it could raise
// elsewhere.
func TestEnforced_FlowTablesThere(t *testing.T) {
	db := newMigratedDB(t)
	createConflictItemTable(t, db, true, ConflictReject)
	registerConflictFlow(t, db, ConflictReject)
	insertConflictItem(t, db, "a", "published")
	insertConflictItem(t, db, "b", "draft")
	other := failingDB{db, "no_such_probe_target", &pgError{"42P01", "x"}}
	if err := validateTransition(context.Background(), other, nil, nil, "u", "b", "ConflictType", "draft", "nonsense", ""); err == nil {
		t.Error("an undefined state must be refused when the flow tables are there")
	}
}

func TestEnsureColumn(t *testing.T) {
	ctx := context.Background()
	db := newSQLiteDB(t)
	if _, err := db.ExecContext(ctx, `CREATE TABLE ec_t (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}

	if err := EnsureColumn(ctx, db, "ec_t", "extra", "TEXT NOT NULL DEFAULT ''"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if ok, _ := columnExists(ctx, db, "ec_t", "extra"); !ok {
		t.Error("the column was not added")
	}
	if err := EnsureColumn(ctx, db, "ec_t", "extra", "TEXT NOT NULL DEFAULT ''"); err != nil {
		t.Errorf("second call (no-op): %v", err)
	}

	t.Run("a table that is not there is the ALTER's own wrapped error", func(t *testing.T) {
		err := EnsureColumn(ctx, db, "ec_missing", "x", "TEXT")
		if err == nil || !strings.Contains(err.Error(), "EnsureColumn: ec_missing.x") {
			t.Errorf("err = %v, want the wrapped ALTER error", err)
		}
	})
	t.Run("another process added it between the lookup and the ALTER", func(t *testing.T) {
		// The lookup sees the column absent, the ALTER finds it present: the
		// two-processes-booting race. SQLite says "duplicate column name".
		racing := lateColumnDB{DB: db, table: "ec_t", column: "raced"}
		if err := EnsureColumn(ctx, racing, "ec_t", "raced", "TEXT NOT NULL DEFAULT ''"); err != nil {
			t.Errorf("a duplicate column on ADD is success, got %v", err)
		}
	})
	t.Run("a Postgres duplicate column on ADD is success too", func(t *testing.T) {
		pg := alterFailDB{DB: db, err: &pgError{"42701", `column "z" of relation "ec_t" already exists`}}
		if err := EnsureColumn(ctx, pg, "ec_t", "z", "TEXT"); err != nil {
			t.Errorf("err = %v, want nil", err)
		}
	})
	t.Run("any other ALTER failure is returned", func(t *testing.T) {
		bad := alterFailDB{DB: db, err: errors.New("permission denied")}
		if err := EnsureColumn(ctx, bad, "ec_t", "y", "TEXT"); err == nil {
			t.Error("want the ALTER failure returned")
		}
	})
	t.Run("a probe failure is returned", func(t *testing.T) {
		broken := failingDB{db, "SELECT", errors.New("connection reset")}
		if err := EnsureColumn(ctx, broken, "ec_t", "w", "TEXT"); err == nil {
			t.Error("want the probe failure returned, not a skipped migration")
		}
	})
}

// lateColumnDB adds column to table the moment the code under test tries to ADD it:
// the column is absent for the lookup and present for the ALTER, as when two
// processes boot together.
type lateColumnDB struct {
	DB
	table, column string
}

func (d lateColumnDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	if strings.HasPrefix(q, "ALTER TABLE") {
		if _, err := d.DB.ExecContext(ctx, q, args...); err != nil {
			return nil, err
		}
	}
	return d.DB.ExecContext(ctx, q, args...)
}

// alterFailDB fails every ALTER with err.
type alterFailDB struct {
	DB
	err error
}

func (d alterFailDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	if strings.HasPrefix(q, "ALTER TABLE") {
		return nil, d.err
	}
	return d.DB.ExecContext(ctx, q, args...)
}

func TestEnsureLastActorColumns_SkipsAbsentTablesAndFailsOnBrokenProbe(t *testing.T) {
	ctx := context.Background()
	db := newSQLiteDB(t)
	if err := EnsureLastActorColumns(ctx, db); err != nil {
		t.Errorf("no orchestration tables at all: %v, want nil (each is skipped)", err)
	}
	broken := failingDB{db, "smeldr_signals", errors.New("connection reset")}
	if err := EnsureLastActorColumns(ctx, broken); err == nil {
		t.Error("a probe failure is an error, not a skipped migration")
	}
}

func TestResolveItemTable(t *testing.T) {
	ctx := context.Background()
	inner := newMigratedDB(t)
	createConflictItemTable(t, inner, true, ConflictReject)
	if got := resolveItemTable(ctx, inner, "ConflictType"); got != "conflict_types" {
		t.Errorf("a typed table = %q, want conflict_types", got)
	}
	if got := resolveItemTable(ctx, inner, "NoSuchType"); got != "smeldr_dynamic_content" {
		t.Errorf("no table = %q, want smeldr_dynamic_content", got)
	}
	pg := failingDB{inner, `"conflict_types"`, &pgError{"42P01", `relation "conflict_types" does not exist`}}
	if got := resolveItemTable(ctx, pg, "ConflictType"); got != "smeldr_dynamic_content" {
		t.Errorf("a Postgres missing-table error = %q, want smeldr_dynamic_content", got)
	}
	var got string
	broken := failingDB{inner, `"conflict_types"`, errors.New("connection reset")}
	out := capturedLog(func() { got = resolveItemTable(ctx, broken, "ConflictType") })
	if got != "smeldr_dynamic_content" || !strings.Contains(out, "level=WARN") || !strings.Contains(out, "table check failed") {
		t.Errorf("a failing table check = %q, log:\n%s\nwant the dynamic table and a Warn", got, out)
	}
}

// TestMigrateStateFlows_UpgradesAnOlderSchema: a database whose state flow tables
// predate the columns the seed and the enforcement queries read is upgraded by
// migrateStateFlows itself, before it seeds (D103). Before, the seed named the
// locked column first and failed on such a database.
func TestMigrateStateFlows_UpgradesAnOlderSchema(t *testing.T) {
	ctx := context.Background()
	db := newSQLiteDB(t)
	for _, ddl := range []string{
		`CREATE TABLE smeldr_state_flows (id TEXT NOT NULL PRIMARY KEY, name TEXT NOT NULL UNIQUE, type_name TEXT, description TEXT, created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE smeldr_states (id TEXT NOT NULL PRIMARY KEY, flow_id TEXT NOT NULL REFERENCES smeldr_state_flows(id), name TEXT NOT NULL, is_initial BOOLEAN NOT NULL DEFAULT FALSE, is_terminal BOOLEAN NOT NULL DEFAULT FALSE, suppresses_signals BOOLEAN NOT NULL DEFAULT FALSE, UNIQUE(flow_id, name))`,
		`CREATE TABLE smeldr_transitions (id TEXT NOT NULL PRIMARY KEY, flow_id TEXT NOT NULL REFERENCES smeldr_state_flows(id), from_state TEXT NOT NULL, to_state TEXT NOT NULL, required_role TEXT, UNIQUE(flow_id, from_state, to_state))`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatalf("old schema: %v", err)
		}
	}
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows on an older schema: %v", err)
	}
	for _, c := range [][2]string{
		{"smeldr_states", "locked"}, {"smeldr_states", "standing"},
		{"smeldr_transitions", "required_reason"}, {"smeldr_transitions", "strict"},
		{"smeldr_state_flows", "active_state"}, {"smeldr_state_flows", "conflict_policy"},
	} {
		if ok, err := columnExists(ctx, db, c[0], c[1]); !ok || err != nil {
			t.Errorf("%s.%s was not added: %v, %v", c[0], c[1], ok, err)
		}
	}
}
