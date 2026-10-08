// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// signalStatusOf reads a Signal's current status.
func signalStatusOf(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	var s string
	if err := db.QueryRowContext(context.Background(), `SELECT status FROM smeldr_signals WHERE id = ?`, id).Scan(&s); err != nil {
		t.Fatalf("status of %s: %v", id, err)
	}
	return s
}

func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// orderApp is an App whose Signal flow supersedes (active state "read"), with a
// relation store and a provenance store wired, and two losers plus a winner
// seeded.
func orderApp(t *testing.T) (*App, *sql.DB, *RelationStore, *fakeProvenanceStore) {
	t.Helper()
	app, db, store := edgeTestApp(t, "read", signalSupersedeStates, signalSupersedeTransitions)
	upsertTestKind(t, store, "supersedes", "Signal", "Signal")
	prov := &fakeProvenanceStore{}
	app.Provenance(prov)
	insertSignal(t, db, "loser-1", "loser-1-slug", "read")
	insertSignal(t, db, "loser-2", "loser-2-slug", "read")
	insertSignal(t, db, "winner", "winner-slug", "pending")
	return app, db, store, prov
}

func mustExec(t *testing.T, db *sql.DB, q string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), q); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

// TestTransitionItemVia_Supersede_WinnerWriteFails_LosersUntouched is the bug:
// the losers used to be superseded before the winner's own write, so a failed
// winner left the type with no active item. Now the winner's write comes first,
// inside one transaction with the losers, and a failure leaves everything as it
// was with nothing recorded.
func TestTransitionItemVia_Supersede_WinnerWriteFails_LosersUntouched(t *testing.T) {
	app, db, store, prov := orderApp(t)
	mustExec(t, db, `CREATE TRIGGER fail_winner BEFORE UPDATE ON smeldr_signals
		WHEN NEW.id = 'winner' BEGIN SELECT RAISE(ABORT, 'winner write refused'); END`)

	_, err := app.TransitionItemVia(NewTestContext(User{ID: "u1", Roles: []Role{Editor}}), "mcp", "Signal", "winner-slug", "read", "")
	if err == nil {
		t.Fatal("want the winner's write failure returned")
	}
	for _, id := range []string{"loser-1", "loser-2"} {
		if s := signalStatusOf(t, db, id); s != "read" {
			t.Errorf("%s status = %q after a failed winner, want it still active (read)", id, s)
		}
	}
	if s := signalStatusOf(t, db, "winner"); s != "pending" {
		t.Errorf("winner status = %q, want unchanged", s)
	}
	if got := prov.Appended(); len(got) != 0 {
		t.Errorf("provenance recorded for a change that did not happen: %+v", got)
	}
	if n := countRows(t, db, "smeldr_relations"); n != 0 {
		t.Errorf("%d supersedes edges written for a change that did not happen", n)
	}
	_ = store
}

// TestTransitionItemVia_Supersede_CommitFails_AllRolledBack proves the
// atomicity: the winner's write and the losers' writes all succeed as
// statements, the COMMIT itself fails (a deferred foreign key, raised by a
// trigger on the winner row), and neither the winner nor any loser changed.
func TestTransitionItemVia_Supersede_CommitFails_AllRolledBack(t *testing.T) {
	app, db, _, prov := orderApp(t)
	mustExec(t, db, `PRAGMA foreign_keys = ON`)
	mustExec(t, db, `CREATE TABLE fk_child (id TEXT PRIMARY KEY, sig TEXT REFERENCES smeldr_signals(id) DEFERRABLE INITIALLY DEFERRED)`)
	mustExec(t, db, `CREATE TRIGGER fail_commit AFTER UPDATE ON smeldr_signals
		WHEN NEW.id = 'winner' BEGIN INSERT INTO fk_child (id, sig) VALUES ('c', 'no-such-signal'); END`)

	_, err := app.TransitionItemVia(NewTestContext(User{ID: "u1", Roles: []Role{Editor}}), "mcp", "Signal", "winner-slug", "read", "")
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("err = %v, want ErrInternal from the failed commit", err)
	}
	for id, want := range map[string]string{"loser-1": "read", "loser-2": "read", "winner": "pending"} {
		if s := signalStatusOf(t, db, id); s != want {
			t.Errorf("%s status = %q after a failed commit, want %q (all rolled back together)", id, s, want)
		}
	}
	if got := prov.Appended(); len(got) != 0 {
		t.Errorf("provenance recorded after a failed commit: %+v", got)
	}
}

// TestTransitionItemVia_Supersede_Success_WinnerAndLosersTogether pins the
// unchanged success path through the transaction.
func TestTransitionItemVia_Supersede_Success_WinnerAndLosersTogether(t *testing.T) {
	app, db, store, prov := orderApp(t)
	if _, err := app.TransitionItemVia(NewTestContext(User{ID: "u1", Roles: []Role{Editor}}), "mcp", "Signal", "winner-slug", "read", ""); err != nil {
		t.Fatalf("TransitionItemVia: %v", err)
	}
	if s := signalStatusOf(t, db, "winner"); s != "read" {
		t.Errorf("winner = %q, want read", s)
	}
	for _, id := range []string{"loser-1", "loser-2"} {
		if s := signalStatusOf(t, db, id); s != "superseded" {
			t.Errorf("%s = %q, want superseded", id, s)
		}
	}
	if n := len(edgesFrom(t, store, "Signal", "winner")); n != 2 {
		t.Errorf("%d edges, want 2", n)
	}
	superseded := 0
	for _, r := range prov.Appended() {
		if r.ToState == "superseded" {
			superseded++
		}
	}
	if superseded != 2 {
		t.Errorf("%d superseded records, want 2", superseded)
	}
}

// TestTransitionItemVia_Supersede_OneLoserFails: a loser whose own UPDATE fails
// is skipped and not recorded (the documented fail-open), the winner and the
// other loser proceed, and the leftover active item is logged at Error.
func TestTransitionItemVia_Supersede_OneLoserFails(t *testing.T) {
	app, db, _, prov := orderApp(t)
	mustExec(t, db, `CREATE TRIGGER fail_loser BEFORE UPDATE ON smeldr_signals
		WHEN NEW.id = 'loser-2' AND NEW.status = 'superseded' BEGIN SELECT RAISE(ABORT, 'loser refused'); END`)

	out := capturedLog(func() {
		if _, err := app.TransitionItemVia(NewTestContext(User{ID: "u1", Roles: []Role{Editor}}), "mcp", "Signal", "winner-slug", "read", ""); err != nil {
			t.Fatalf("TransitionItemVia: %v", err)
		}
	})
	if s := signalStatusOf(t, db, "winner"); s != "read" {
		t.Errorf("winner = %q, want read", s)
	}
	if s := signalStatusOf(t, db, "loser-1"); s != "superseded" {
		t.Errorf("loser-1 = %q, want superseded", s)
	}
	if s := signalStatusOf(t, db, "loser-2"); s != "read" {
		t.Errorf("loser-2 = %q, want it left as it was", s)
	}
	for _, r := range prov.Appended() {
		if r.SubjectID == "loser-2" {
			t.Errorf("a record was written for the loser that was not superseded: %+v", r)
		}
	}
	if !strings.Contains(out, "level=ERROR") || !strings.Contains(out, "more than one item in its active state") {
		t.Errorf("the leftover active item must be logged at Error:\n%s", out)
	}
}

// beginFailDB makes BeginTx fail, to prove nothing is written when no
// transaction can be started.
type beginFailDB struct{ DB }

func (beginFailDB) BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error) {
	return nil, errors.New("simulated begin failure")
}

// plainDB hides BeginTx: a handle with no transaction support.
type plainDB struct{ DB }

func TestTransitionItemVia_Supersede_BeginFails_NothingWritten(t *testing.T) {
	app, db, _, prov := orderApp(t)
	app.cfg.DB = beginFailDB{db}
	_, err := app.TransitionItemVia(NewTestContext(User{ID: "u1", Roles: []Role{Editor}}), "mcp", "Signal", "winner-slug", "read", "")
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("err = %v, want ErrInternal", err)
	}
	for id, want := range map[string]string{"loser-1": "read", "loser-2": "read", "winner": "pending"} {
		if s := signalStatusOf(t, db, id); s != want {
			t.Errorf("%s = %q, want %q", id, s, want)
		}
	}
	if len(prov.Appended()) != 0 {
		t.Error("provenance written although the transition did not start")
	}
}

// TestTransitionItemVia_Supersede_NoTransactionSupport: a handle without
// BeginTx runs the same writes in the same order, winner first, not atomically.
func TestTransitionItemVia_Supersede_NoTransactionSupport(t *testing.T) {
	app, db, _, _ := orderApp(t)
	app.cfg.DB = plainDB{db}
	if _, err := app.TransitionItemVia(NewTestContext(User{ID: "u1", Roles: []Role{Editor}}), "mcp", "Signal", "winner-slug", "read", ""); err != nil {
		t.Fatalf("TransitionItemVia: %v", err)
	}
	if s := signalStatusOf(t, db, "winner"); s != "read" {
		t.Errorf("winner = %q", s)
	}
	if s := signalStatusOf(t, db, "loser-1"); s != "superseded" {
		t.Errorf("loser-1 = %q", s)
	}

	// Winner failure on the non-transactional path: winner first means the
	// losers are still untouched.
	app2, db2, _, _ := orderApp(t)
	app2.cfg.DB = plainDB{db2}
	mustExec(t, db2, `CREATE TRIGGER fail_winner BEFORE UPDATE ON smeldr_signals
		WHEN NEW.id = 'winner' BEGIN SELECT RAISE(ABORT, 'winner write refused'); END`)
	if _, err := app2.TransitionItemVia(NewTestContext(User{ID: "u1", Roles: []Role{Editor}}), "mcp", "Signal", "winner-slug", "read", ""); err == nil {
		t.Fatal("want the winner failure returned")
	}
	if s := signalStatusOf(t, db2, "loser-1"); s != "read" {
		t.Errorf("loser-1 = %q on a handle without transactions, want untouched (winner first)", s)
	}
}

// TestDynamicSetStatus_Supersede_WinnerWriteFails_LosersUntouched is the same
// guarantee on the runtime-defined type path.
func TestDynamicSetStatus_Supersede_WinnerWriteFails_LosersUntouched(t *testing.T) {
	app, db, _, prov := setupProvenanceTransitionApp(t)
	typeName, slugA := defineProvenanceDynamicType(t, app, db, "orderdyn")
	repo, err := app.DynamicContentRepo(typeName)
	if err != nil {
		t.Fatalf("DynamicContentRepo: %v", err)
	}
	b, err := repo.CreateDraft(context.Background(), map[string]any{"Title": "second"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	a, err := repo.GetBySlug(context.Background(), slugA)
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if err := app.RegisterFlow(StateFlow{
		Name: "orderdyn-flow", TypeName: typeName, ActiveState: "published", ConflictPolicy: ConflictSupersede,
		States:      []State{{Name: "draft", IsInitial: true}, {Name: "published"}, {Name: "superseded"}},
		Transitions: []Transition{{From: "draft", To: "published"}, {From: "published", To: "superseded"}},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	ctx := NewTestContext(User{ID: "u-d", Roles: []Role{Editor}})
	if err := repo.setStatusVia(ctx, a.ID, Published, "", "mcp"); err != nil {
		t.Fatalf("publish A: %v", err)
	}
	before := len(prov.Appended())
	mustExec(t, db, `CREATE TRIGGER fail_dyn_winner BEFORE UPDATE ON smeldr_dynamic_content
		WHEN NEW.id = '`+b.ID+`' BEGIN SELECT RAISE(ABORT, 'winner write refused'); END`)

	if err := repo.setStatusVia(ctx, b.ID, Published, "", "mcp"); err == nil {
		t.Fatal("want the winner's write failure returned")
	}
	var status string
	_ = db.QueryRowContext(context.Background(), `SELECT status FROM smeldr_dynamic_content WHERE id = ?`, a.ID).Scan(&status)
	if status != "published" {
		t.Errorf("A = %q after a failed winner, want it still published", status)
	}
	if len(prov.Appended()) != before {
		t.Error("provenance recorded for a change that did not happen")
	}
}

// TestModuleMCPPublish_Supersede_WinnerSaveFails_LosersUntouched: the Module
// path writes the winner through its own repository; when that Save fails
// nothing else has happened (the bug), where it used to have superseded the
// losers already.
func TestModuleMCPPublish_Supersede_WinnerSaveFails_LosersUntouched(t *testing.T) {
	app, db, store := edgeTestApp(t, "published",
		[]State{{Name: "pending", IsInitial: true}, {Name: "published"}, {Name: "superseded"}},
		[]Transition{{From: "pending", To: "published"}, {From: "published", To: "superseded"}})
	_ = app
	upsertTestKind(t, store, "supersedes", "Signal", "Signal")
	prov := &fakeProvenanceStore{}
	insertSignal(t, db, "old", "old-slug", "published")
	insertSignal(t, db, "new", "new-slug", "pending")
	m := NewModule[*Signal]((*Signal)(nil),
		At("/signals"), Repo(savefailRepo[*Signal]{inner: NewSQLRepo[*Signal](db, Table("smeldr_signals"))}), MCP(MCPRead, MCPWrite))
	m.setDB(db)
	m.setRelationStore(store)
	m.setProvenanceStore(prov)

	if err := m.MCPPublish(NewTestContext(User{ID: "u-mod", Roles: []Role{Editor}}), "new-slug", ""); err == nil {
		t.Fatal("want the Save failure returned")
	}
	if s := signalStatusOf(t, db, "old"); s != "published" {
		t.Errorf("old = %q after a failed winner Save, want it still published", s)
	}
	if len(prov.Appended()) != 0 || countRows(t, db, "smeldr_relations") != 0 {
		t.Error("something was recorded for a change that did not happen")
	}
}

// TestPlanConflict_WinnerNeverItsOwnLoser: a winner that is already in the
// active state (an idempotent re-publish) is not selected as its own loser.
func TestPlanConflict_WinnerNeverItsOwnLoser(t *testing.T) {
	db := newMigratedDB(t)
	createConflictItemTable(t, db, true, ConflictSupersede)
	insertConflictItem(t, db, "w", "published")
	insertConflictItem(t, db, "other", "published")
	plan, err := planConflict(context.Background(), db, "ConflictType", "published", "w")
	if err != nil {
		t.Fatalf("planConflict: %v", err)
	}
	if plan == nil || len(plan.losers) != 1 || plan.losers[0] != "other" {
		t.Fatalf("plan = %+v, want exactly the other item as the loser", plan)
	}
	plan.release()

	// Only the winner active: nothing to do.
	db2 := newMigratedDB(t)
	createConflictItemTable(t, db2, true, ConflictSupersede)
	insertConflictItem(t, db2, "w", "published")
	plan2, err := planConflict(context.Background(), db2, "ConflictType", "published", "w")
	if err != nil || plan2 == nil || len(plan2.losers) != 0 {
		t.Errorf("plan = %+v, err = %v, want a plan with no losers", plan2, err)
	}
	plan2.release()
}

// TestConflictPlan_NilIsSafe: every method of a nil plan is a no-op.
func TestConflictPlan_NilIsSafe(t *testing.T) {
	var p *conflictPlan
	if got := p.supersede(context.Background(), nil); got != nil {
		t.Errorf("nil plan supersede = %v", got)
	}
	p.afterCommit(context.Background(), nil, nil, nil, nil, true, "", []string{"x"})
	p.run(context.Background(), nil, nil, nil, nil, true, "")
}

// TestConflictTx covers the three outcomes of starting the write transaction.
func TestConflictTx(t *testing.T) {
	db := newSQLiteDB(t)
	exec, commit, rollback, err := conflictTx(context.Background(), db)
	if err != nil {
		t.Fatalf("conflictTx: %v", err)
	}
	if _, ok := exec.(*sql.Tx); !ok {
		t.Errorf("exec = %T, want a transaction for a *sql.DB", exec)
	}
	if err := commit(); err != nil {
		t.Errorf("commit: %v", err)
	}
	rollback() // safe after commit

	exec, commit, rollback, err = conflictTx(context.Background(), plainDB{db})
	if err != nil || exec == nil || commit() != nil {
		t.Errorf("a handle without BeginTx must pass through: exec=%v err=%v", exec, err)
	}
	rollback()

	if _, _, _, err := conflictTx(context.Background(), beginFailDB{db}); !errors.Is(err, ErrInternal) {
		t.Errorf("BeginTx failure = %v, want ErrInternal", err)
	}
}
