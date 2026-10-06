//go:build integration

package pgx

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	smeldr "smeldr.dev/core"
)

// These tests prove what BeginTx on the adapter changes: core's multi-statement
// writes are atomic on Postgres. Before it (adapter v0.2.x) the handle had no
// BeginTx, every one of them ran statement by statement, and the "kill the
// transaction midway: nothing half-done" test the state machine suite planned could
// not have passed.

// supersedeApp builds an app with a ConflictSupersede flow on pg_items and returns
// an actor context allowed to approve.
func supersedeApp(t *testing.T, db smeldr.DB) (*smeldr.App, context.Context) {
	t.Helper()
	app, store := newPGApp(t, db)
	createItemsTable(t, db, "pg_items")
	registerType(app, "PgItem")
	if err := app.RegisterFlow(raceFlow("PgItem", smeldr.ConflictSupersede)); err != nil {
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
	return app, smeldr.NewTestContext(smeldr.User{ID: tokenID})
}

// failUpdatesOf makes every UPDATE of the row with this id fail, on the server.
func failUpdatesOf(t *testing.T, db smeldr.DB, table, id string) {
	t.Helper()
	ctx := context.Background()
	for _, ddl := range []string{
		`CREATE FUNCTION fail_update() RETURNS trigger AS $$ BEGIN IF OLD.id = TG_ARGV[0] THEN RAISE EXCEPTION 'boom: update of % refused', OLD.id; END IF; RETURN NEW; END $$ LANGUAGE plpgsql`,
		`CREATE TRIGGER fail_update BEFORE UPDATE ON ` + table + ` FOR EACH ROW EXECUTE FUNCTION fail_update('` + id + `')`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatalf("%s: %v", ddl, err)
		}
	}
}

// TestPG_Supersede_AFailedLoserLeavesNothingHalfDone is the atomicity test the
// state machine suite named (A409, item 8) and could not run. Two items hold the
// active state; the winner's write succeeds, one loser's write is refused by the
// server. Documented since A398: on Postgres the transaction is aborted, so the
// whole transition fails and nothing is left half done.
func TestPG_Supersede_AFailedLoserLeavesNothingHalfDone(t *testing.T) {
	db, _ := isolatedDB(t)
	app, actor := supersedeApp(t, db)
	insertItem(t, db, "pg_items", "a", "published")
	insertItem(t, db, "pg_items", "c", "published")
	insertItem(t, db, "pg_items", "b", "review")
	failUpdatesOf(t, db, "pg_items", "c")

	out := captureLog(func() {
		_, err := app.TransitionItem(actor, "PgItem", "b", "published")
		if !errors.Is(err, smeldr.ErrInternal) {
			t.Errorf("a refused loser write: err = %v, want ErrInternal (the transaction could not commit)", err)
		}
	})
	for id, want := range map[string]string{"b": "review", "a": "published", "c": "published"} {
		if got := statusOf(t, db, "pg_items", id); got != want {
			t.Errorf("%s = %q, want %q: the change is half done", id, got, want)
		}
	}
	if !strings.Contains(out, "supersede UPDATE failed") {
		t.Errorf("the refused loser write must be logged:\n%s", out)
	}
}

// TestPG_Supersede_TableWithoutLastActor: a table that predates last_actor still
// transitions inside the transaction. The first UPDATE (with last_actor) fails by
// design and would abort the Postgres transaction; it runs under a savepoint.
func TestPG_Supersede_TableWithoutLastActor(t *testing.T) {
	db, _ := isolatedDB(t)
	app, store := newPGApp(t, db)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE pg_olds (id TEXT PRIMARY KEY, slug TEXT NOT NULL, status TEXT NOT NULL, updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		t.Fatalf("create: %v", err)
	}
	registerType(app, "PgOld")
	flow := raceFlow("PgOld", smeldr.ConflictSupersede)
	flow.Name = "pg-old-flow"
	flow.TypeName = "PgOld"
	if err := app.RegisterFlow(flow); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	if err := store.DefineRole(ctx, smeldr.RoleDefinition{Name: "pg-approver", Operations: []string{"approve"}, ScopeMode: smeldr.ScopeGlobal}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	tokenID := "pg-token-" + smeldr.NewID()
	if _, err := store.Grant(ctx, smeldr.RoleGrant{TokenID: tokenID, RoleName: "pg-approver"}); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	insertItem(t, db, "pg_olds", "a", "published")
	insertItem(t, db, "pg_olds", "b", "review")

	if _, err := app.TransitionItem(smeldr.NewTestContext(smeldr.User{ID: tokenID}), "PgOld", "b", "published"); err != nil {
		t.Fatalf("a table without last_actor inside the conflict transaction: %v", err)
	}
	for id, want := range map[string]string{"b": "published", "a": "superseded"} {
		if got := statusOf(t, db, "pg_olds", id); got != want {
			t.Errorf("%s = %q, want %q", id, got, want)
		}
	}
}

// TestPG_Governance_AnAuditFailureRollsTheMutationBack: with the bundled audit
// store, the mutation and its audit record share one transaction, so an audit
// failure undoes the mutation (A233). Before BeginTx on the adapter they did not.
func TestPG_Governance_AnAuditFailureRollsTheMutationBack(t *testing.T) {
	db, _ := isolatedDB(t)
	_, store := newPGApp(t, db)
	ctx := context.Background()
	if err := smeldr.CreateGovernanceAuditTable(db); err != nil {
		t.Fatalf("CreateGovernanceAuditTable: %v", err)
	}
	audited := store.WithAudit("pg-actor", smeldr.NewGovernanceAuditStore(db))
	if err := store.DefineRole(ctx, smeldr.RoleDefinition{Name: "pg-existing", Operations: []string{"read"}, ScopeMode: smeldr.ScopeGlobal}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	grantID, err := audited.Grant(ctx, smeldr.RoleGrant{TokenID: "pg-tok-keep", RoleName: "pg-existing"})
	if err != nil {
		t.Fatalf("Grant (audit works): %v", err)
	}

	// Now every audit insert is refused by the server.
	for _, ddl := range []string{
		`CREATE FUNCTION refuse_audit() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'audit refused'; END $$ LANGUAGE plpgsql`,
		`CREATE TRIGGER refuse_audit BEFORE INSERT ON smeldr_governance_audit FOR EACH ROW EXECUTE FUNCTION refuse_audit()`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatalf("%s: %v", ddl, err)
		}
	}
	count := func(q string, args ...any) int {
		var n int
		if err := db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}

	if _, err := audited.Grant(ctx, smeldr.RoleGrant{TokenID: "pg-tok-new", RoleName: "pg-existing"}); err == nil {
		t.Error("a Grant whose audit record is refused must fail")
	}
	if n := count(`SELECT COUNT(*) FROM smeldr_role_grants WHERE token_id = 'pg-tok-new'`); n != 0 {
		t.Errorf("the refused Grant left %d grant rows behind", n)
	}
	if err := audited.DefineRole(ctx, smeldr.RoleDefinition{Name: "pg-new-role", Operations: []string{"read"}, ScopeMode: smeldr.ScopeGlobal}); err == nil {
		t.Error("a DefineRole whose audit record is refused must fail")
	}
	if n := count(`SELECT COUNT(*) FROM smeldr_roles WHERE name = 'pg-new-role'`); n != 0 {
		t.Errorf("the refused DefineRole left %d role rows behind", n)
	}
	if err := audited.Revoke(ctx, grantID); err == nil {
		t.Error("a Revoke whose audit record is refused must fail")
	}
	if n := count(`SELECT COUNT(*) FROM smeldr_role_grants WHERE id = $1`, grantID); n != 1 {
		t.Errorf("the refused Revoke removed the grant (%d rows left, want 1)", n)
	}
}

// TestPG_RecomputeAsserted_ARollbackKeepsTheOldEdges: the diff deletes stale edges and
// inserts new ones in one transaction; an insert that fails midway undoes the deletes.
func TestPG_RecomputeAsserted_ARollbackKeepsTheOldEdges(t *testing.T) {
	db, _ := isolatedDB(t)
	newPGApp(t, db)
	ctx := context.Background()
	if err := smeldr.CreateRelationTables(db); err != nil {
		t.Fatalf("CreateRelationTables: %v", err)
	}
	rs, err := smeldr.NewRelationStore(db)
	if err != nil {
		t.Fatalf("NewRelationStore: %v", err)
	}
	edge := func(id, target string) smeldr.RelationEdge {
		return smeldr.RelationEdge{ID: id, TargetType: "Doc", TargetID: target, RelationKind: "references"}
	}
	if err := rs.RecomputeAsserted(ctx, "Doc", "src", []smeldr.RelationEdge{edge("e1", "t1"), edge("e2", "t2")}); err != nil {
		t.Fatalf("first RecomputeAsserted: %v", err)
	}
	// The second call drops t1 and t2 and adds two edges with the same id: the
	// second insert violates the primary key.
	err = rs.RecomputeAsserted(ctx, "Doc", "src", []smeldr.RelationEdge{edge("dup", "t3"), edge("dup", "t4")})
	if err == nil {
		t.Fatal("two inserts of one id must fail")
	}
	var ids []string
	rows, err := db.QueryContext(ctx, `SELECT id FROM smeldr_relations WHERE source_id = 'src' ORDER BY id`)
	if err != nil {
		t.Fatalf("read edges: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if strings.Join(ids, ",") != "e1,e2" {
		t.Errorf("edges after the failed diff = %v, want [e1 e2]: the deletes must be rolled back with the failed insert", ids)
	}
}

// TestPG_Transactions_NeedOneConnectionAtMostTwoPerTransition: a transition under a
// policy holds the advisory lock on one connection and its transaction on another.
// MaxConns 2 is enough; MaxConns 1 runs the transaction without the lock.
func TestPG_Transactions_SmallPools(t *testing.T) {
	for _, tc := range []struct {
		name     string
		maxConns int32
		wantLock bool
	}{
		{"MaxConns 2: the lock and the transaction", 2, true},
		{"MaxConns 1: the transaction without the lock", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			admin := openPool(t, nil)
			schema := "t_" + strings.ReplaceAll(strings.ToLower(smeldr.NewID()), "-", "")
			if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
				t.Fatalf("schema: %v", err)
			}
			t.Cleanup(func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") })
			pool := openPool(t, func(cfg *pgxpool.Config) {
				cfg.ConnConfig.RuntimeParams["search_path"] = schema
				cfg.MaxConns = tc.maxConns
			})
			db := Wrap(pool)
			app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte(pgTestSecret), DB: db})
			createItemsTable(t, db, "pg_items")
			registerType(app, "PgItem")
			flow := raceFlow("PgItem", smeldr.ConflictSupersede)
			flow.Transitions = []smeldr.Transition{{From: "review", To: "published"}, {From: "published", To: "superseded"}}
			flow.Triggers = nil
			if err := app.RegisterFlow(flow); err != nil {
				t.Fatalf("RegisterFlow: %v", err)
			}
			insertItem(t, db, "pg_items", "a", "published")
			insertItem(t, db, "pg_items", "b", "review")

			done := make(chan struct{})
			var terr error
			var out string
			go func() {
				defer close(done)
				out = captureLog(func() { _, terr = app.TransitionItem(ctx, "PgItem", "b", "published") })
			}()
			select {
			case <-done:
			case <-time.After(20 * time.Second):
				t.Fatal("the transition hung")
			}
			if terr != nil {
				t.Fatalf("the transition must complete: %v", terr)
			}
			for id, want := range map[string]string{"b": "published", "a": "superseded"} {
				if got := statusOf(t, db, "pg_items", id); got != want {
					t.Errorf("%s = %q, want %q", id, got, want)
				}
			}
			if got := strings.Contains(out, "cross-process lock not taken"); got == tc.wantLock {
				t.Errorf("lock refused line present = %v, want %v:\n%s", got, !tc.wantLock, out)
			}
		})
	}
}
