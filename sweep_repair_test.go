package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

// ownFlowFixture is a runtime type "plan" with its own state flow (as
// task_plan), one item in it, and a Decision with an edge to it.
type ownFlowFixture struct {
	db     *sql.DB
	rs     *RelationStore
	app    *App
	itemID string
}

func newOwnFlowFixture(t *testing.T, status Status) ownFlowFixture {
	t.Helper()
	db, rs, app, itemID := setupDynamicTargetDB(t, "plan", status)
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := app.RegisterFlow(StateFlow{Name: "plan", TypeName: "plan", States: []State{
		{Name: "draft", IsInitial: true}, {Name: "approved"}, {Name: "abandoned", IsTerminal: true}, {Name: "archived", IsTerminal: true},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := CreateProvenanceTable(db); err != nil {
		t.Fatal(err)
	}
	store := NewProvenanceStore(db)
	app.Provenance(store)
	rs.setProvenanceStore(store)
	seedDecision(t, db, "decision-1", "proposed")
	if _, err := rs.MCPAssertRelation(ctx, "Decision", "decision-1", "plan", itemID, "belongs_to_plan", nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	return ownFlowFixture{db: db, rs: rs, app: app, itemID: itemID}
}

func (f ownFlowFixture) setStatus(t *testing.T, status string) {
	t.Helper()
	if _, err := f.db.Exec(`UPDATE smeldr_dynamic_content SET status = $1 WHERE id = $2`, status, f.itemID); err != nil {
		t.Fatal(err)
	}
}

// sweepAsBefore ends every live edge the way the sweep did before T932
// (a runtime type with its own flow counted as dead unless published).
func (f ownFlowFixture) sweepAsBefore(t *testing.T) {
	t.Helper()
	if _, _, _, err := f.rs.SweepStructural(context.Background(), func(context.Context, string, string) (bool, error) { return false, nil }, func(context.Context, RelationEdge) {}); err != nil {
		t.Fatal(err)
	}
}

// ageEnded moves the end of the rows ended in the last minute to hours ago,
// so each step's ends are ordered in time (a test's ends can share a clock
// tick).
func (f ownFlowFixture) ageEnded(t *testing.T, hours int) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := f.db.Exec(`UPDATE smeldr_relations SET invalid_at = $1 WHERE invalid_at > $2`,
		now.Add(-time.Duration(hours)*time.Hour), now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
}

func (f ownFlowFixture) liveEdges(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_relations WHERE source_id = 'decision-1' AND invalid_at IS NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestDefaultTargetChecker_OwnFlow: a runtime type with its own flow is alive
// in every state but archived; a missing row is dead; a failed flow lookup is
// an error, so the sweep skips the edge.
func TestDefaultTargetChecker_OwnFlow(t *testing.T) {
	f := newOwnFlowFixture(t, "approved")
	ctx := context.Background()
	for _, status := range []string{"approved", "draft", "abandoned"} {
		f.setStatus(t, status)
		if w, fl, sk, err := f.app.SweepStructural(ctx); err != nil || w != 1 || fl != 0 || sk != 0 {
			t.Errorf("%s: sweep (%d,%d,%d) %v; want the edge kept", status, w, fl, sk, err)
		}
	}
	check := defaultTargetChecker(&awaitsFailDB{DB: f.db, match: "FROM smeldr_state_flows WHERE type_name", err: errors.New("disk")})
	if _, err := check(ctx, "plan", f.itemID); err == nil {
		t.Error("a failed flow lookup must be an error")
	}
	f.setStatus(t, "archived")
	if _, fl, _, err := f.app.SweepStructural(ctx); err != nil || fl != 1 || f.liveEdges(t) != 0 {
		t.Errorf("archived: flagged %d, live %d, %v; want the edge ended", fl, f.liveEdges(t), err)
	}
	if alive, err := defaultTargetChecker(f.db)(ctx, "plan", "no-such-item"); err != nil || alive {
		t.Errorf("a missing row: alive %v, %v", alive, err)
	}
	if own, err := typeHasOwnFlow(ctx, newSQLiteDB(t), "plan"); err != nil || own {
		t.Errorf("no flow tables: %v %v", own, err)
	}
}

// TestRepairSweptEdges: a swept edge whose end is alive is restored once, as
// a new row with its attributes and an assert provenance by sweep-repair;
// the dry run writes nothing; a second run restores nothing; an older
// duplicate, an edge whose end is still dead, and one with a later row ended
// by a withdraw are skipped.
func TestRepairSweptEdges(t *testing.T) {
	f := newOwnFlowFixture(t, "approved")
	ctx := context.Background()
	f.sweepAsBefore(t)
	f.ageEnded(t, 10)
	if f.liveEdges(t) != 0 {
		t.Fatal("the old sweep did not end the edge")
	}
	dry, err := f.app.RepairSweptEdges(ctx, false)
	if err != nil || dry.Applied || dry.Candidates != 1 || dry.Restored != 1 || f.liveEdges(t) != 0 {
		t.Fatalf("dry run = %+v %v, live %d", dry, err, f.liveEdges(t))
	}
	run, err := f.app.RepairSweptEdges(ctx, true)
	if err != nil || run.Restored != 1 || run.ByKind["belongs_to_plan"] != 1 || run.ByType["plan"] != 1 || run.ByType["Decision"] != 1 || f.liveEdges(t) != 1 {
		t.Fatalf("apply = %+v %v, live %d", run, err, f.liveEdges(t))
	}
	var reason string
	if err := f.db.QueryRow(`SELECT p.reason FROM smeldr_provenance p JOIN smeldr_relations r ON r.id = p.subject_id
		WHERE r.source_id = 'decision-1' AND r.invalid_at IS NULL AND p.verb = 'assert' AND p.actor_id = 'sweep-repair'`).Scan(&reason); err != nil || reason == "" {
		t.Errorf("restore provenance = %q %v", reason, err)
	}
	if again, err := f.app.RepairSweptEdges(ctx, true); err != nil || again.Restored != 0 || again.SkippedLive != 1 {
		t.Errorf("second apply = %+v %v", again, err)
	}
	// Two ended rows of one relation: only the newest is considered.
	f.sweepAsBefore(t)
	f.ageEnded(t, 9)
	if run, err := f.app.RepairSweptEdges(ctx, true); err != nil || run.Restored != 1 || run.SkippedOlder != 1 {
		t.Errorf("with an older duplicate = %+v %v", run, err)
	}
	// An end that is still dead stays ended.
	f.sweepAsBefore(t)
	f.ageEnded(t, 8)
	f.setStatus(t, "archived")
	if run, err := f.app.RepairSweptEdges(ctx, true); err != nil || run.Restored != 0 || run.SkippedDead != 1 || f.liveEdges(t) != 0 {
		t.Errorf("dead end = %+v %v", run, err)
	}
	// A later row of the relation, ended by a withdraw: the relation was
	// ended on purpose, so the swept row is not restored.
	f.setStatus(t, "approved")
	again, err := f.rs.MCPAssertRelation(ctx, "Decision", "decision-1", "plan", f.itemID, "belongs_to_plan", nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.rs.Withdraw(ctx, again.ID, "no longer true"); err != nil {
		t.Fatal(err)
	}
	if run, err := f.app.RepairSweptEdges(ctx, true); err != nil || run.Restored != 0 || run.SkippedSuperseded != 1 || f.liveEdges(t) != 0 {
		t.Errorf("superseded = %+v %v, live %d", run, err, f.liveEdges(t))
	}
}

// TestRepairSweptEdges_Failures: a failed candidate read is ErrInternal; a
// restore that cannot be checked or written is counted, and the run goes on.
func TestRepairSweptEdges_Failures(t *testing.T) {
	f := newOwnFlowFixture(t, "approved")
	ctx := context.Background()
	f.sweepAsBefore(t)
	if _, err := (&App{}).RepairSweptEdges(ctx, false); !errors.Is(err, ErrInternal) {
		t.Errorf("no database: %v", err)
	}
	failing := func(match string) *App {
		return &App{cfg: Config{DB: &awaitsFailDB{DB: f.db, match: match, err: errors.New("disk")}}}
	}
	if _, err := failing("FROM smeldr_relations r").RepairSweptEdges(ctx, false); !errors.Is(err, ErrInternal) {
		t.Errorf("candidate read fails: %v", err)
	}
	for _, match := range []string{"FROM smeldr_state_flows WHERE type_name", "FROM smeldr_relations WHERE source_type"} {
		if run, err := failing(match).RepairSweptEdges(ctx, false); err != nil || run.Failed != 1 {
			t.Errorf("%s fails: %+v %v", match, run, err)
		}
	}
	mustExecDB(t, f.db, `CREATE TRIGGER sr_write BEFORE INSERT ON smeldr_relations BEGIN SELECT RAISE(ABORT, 'write'); END`)
	if run, err := f.app.RepairSweptEdges(ctx, true); err != nil || run.Failed != 1 || run.Restored != 0 {
		t.Errorf("write fails: %+v %v", run, err)
	}
}
