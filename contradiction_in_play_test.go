package smeldr

import (
	"context"
	"errors"
	"testing"
)

// T1242, D192: a recorded contradiction holds an open condition while its
// relation is live and both Decisions can still hold (proposed, ratified or
// pending re-evaluation).

// openConflicts counts the open conflict-detected Signals about d.
func (f premiseFixture) openConflicts(t *testing.T, d string) int {
	t.Helper()
	n := 0
	for _, r := range f.conflicts(t, d) {
		if r[1] == signalOpen {
			n++
		}
	}
	return n
}

func TestDecisionInPlay(t *testing.T) {
	for state, want := range map[string]bool{
		"proposed": true, "ratified": true, "pending-re-evaluation": true,
		"superseded": false, "retired": false, "archived": false,
	} {
		if got := decisionInPlay(state); got != want {
			t.Errorf("decisionInPlay(%q) = %v, want %v", state, got, want)
		}
	}
}

// Case (a): a proposed side archived without being ratified resolves both.
func TestContradictionInPlay_ProposedSideArchived(t *testing.T) {
	f := newPremiseFixture(t)
	f.placedDecision(t, "d4", "ratified")
	f.placedDecision(t, "d5", "proposed")
	f.contradict(t, "d4", "d5")
	if f.openConflicts(t, "d4") != 1 || f.openConflicts(t, "d5") != 1 {
		t.Fatalf("open: d4 %v, d5 %v; want one each", f.conflicts(t, "d4"), f.conflicts(t, "d5"))
	}
	f.setState(t, "d5", "proposed", "archived")
	own, other := f.conflicts(t, "d5"), f.conflicts(t, "d4")
	if len(own) != 1 || own[0][1] != "resolved" || own[0][3] != "D-d5 can no longer hold (archived)" {
		t.Errorf("d5 = %v", own)
	}
	if len(other) != 1 || other[0][1] != "resolved" || other[0][3] != "the contradicting Decision can no longer hold" {
		t.Errorf("d4 = %v", other)
	}
	noOther(t, "d4 resolution", other[0][3], "d5")
}

// Case (b): a side already out of force opens nothing, on a first assert and
// on a re-assert after the side was superseded.
func TestContradictionInPlay_SideOutOfForce(t *testing.T) {
	f := newPremiseFixture(t)
	f.placedDecision(t, "d3", "superseded")
	f.placedDecision(t, "d1", "ratified")
	f.contradict(t, "d3", "d1")
	if got := append(f.conflicts(t, "d3"), f.conflicts(t, "d1")...); len(got) != 0 {
		t.Errorf("a superseded side opened %v", got)
	}

	f.placedDecision(t, "e3", "ratified")
	f.placedDecision(t, "e4", "ratified")
	edge := f.contradict(t, "e3", "e4")
	f.setState(t, "e3", "ratified", "superseded")
	if f.openConflicts(t, "e3")+f.openConflicts(t, "e4") != 0 {
		t.Fatalf("not resolved when e3 was superseded")
	}
	if err := f.rels.Withdraw(as("human-1", Human), edge.ID, "asserted again below"); err != nil {
		t.Fatal(err)
	}
	f.contradict(t, "e3", "e4")
	if n := f.openConflicts(t, "e3") + f.openConflicts(t, "e4"); n != 0 {
		t.Errorf("a re-assert with a superseded side opened %d", n)
	}
}

// Two proposed Decisions: in play, so open; ratifying one keeps them open;
// re-evaluation keeps them open; superseded resolves.
func TestContradictionInPlay_Lifecycle(t *testing.T) {
	f := newPremiseFixture(t)
	f.placedDecision(t, "p1", "proposed")
	f.placedDecision(t, "p2", "proposed")
	f.contradict(t, "p1", "p2")
	for _, step := range []struct {
		id, from, to string
		open         int
	}{
		{"p1", "proposed", "ratified", 2},
		{"p1", "ratified", "pending-re-evaluation", 2},
		{"p1", "pending-re-evaluation", "superseded", 0},
	} {
		f.setState(t, step.id, step.from, step.to)
		if n := f.openConflicts(t, "p1") + f.openConflicts(t, "p2"); n != step.open {
			t.Errorf("after %s -> %s: %d open, want %d", step.from, step.to, n, step.open)
		}
	}
}

// The sweep resolves a row written since T928 whose side left play behind
// the resolver's back, and leaves a held one; a second run changes nothing.
func TestSweepContradictions_Stale(t *testing.T) {
	f := newPremiseFixture(t)
	for _, d := range []string{"s1", "s2", "h1", "h2"} {
		f.placedDecision(t, d, "ratified")
	}
	f.contradict(t, "s1", "s2")
	f.contradict(t, "h1", "h2")
	if _, err := f.db.Exec(`UPDATE smeldr_decisions SET status = 'archived' WHERE id = 's2'`); err != nil {
		t.Fatal(err)
	}
	sweep, err := f.app.SweepContradictionConditions(context.Background())
	if err != nil || sweep.Stale != 2 {
		t.Fatalf("sweep = %+v, %v; want Stale 2", sweep, err)
	}
	if got := f.conflicts(t, "s2"); len(got) != 1 || got[0][3] != "D-s2 can no longer hold (archived)" {
		t.Errorf("s2 = %v", got)
	}
	if got := f.conflicts(t, "s1"); len(got) != 1 || got[0][3] != "the contradicting Decision can no longer hold" {
		t.Errorf("s1 = %v", got)
	}
	if f.openConflicts(t, "h1") != 1 || f.openConflicts(t, "h2") != 1 {
		t.Errorf("a held pair was resolved")
	}
	if again, err := f.app.SweepContradictionConditions(context.Background()); err != nil || again.Stale != 0 {
		t.Errorf("second sweep = %+v, %v; want Stale 0", again, err)
	}
}

// A failed read of the pairs resolves nothing and does not fail the sweep.
func TestSweepContradictions_StaleReadFails(t *testing.T) {
	f := newPremiseFixture(t)
	f.app.cfg.DB = faultDB{DB: f.db, match: "FROM smeldr_contradiction_flags f"}
	if n := f.app.resolveStaleContradictions(context.Background()); n != 0 {
		t.Errorf("resolved %d on a failed read", n)
	}
	if _, err := contradictionPairsOpen(context.Background(), faultDB{DB: f.db, match: "smeldr_contradiction_flags"}); !errors.Is(err, errFault) {
		t.Errorf("err = %v, want the fault", err)
	}

	// A pair whose check or whose open row cannot be read is skipped and
	// left open.
	f.app.cfg.DB = f.db
	f.placedDecision(t, "x1", "ratified")
	f.placedDecision(t, "x2", "ratified")
	f.contradict(t, "x1", "x2")
	if _, err := f.db.Exec(`UPDATE smeldr_decisions SET status = 'archived' WHERE id = 'x2'`); err != nil {
		t.Fatal(err)
	}
	for _, match := range []string{
		"relation_kind = 'contradicts'",
		"SELECT id, slug, address, signal_type, receiver, from_state, message FROM smeldr_signals",
	} {
		f.app.cfg.DB = faultDB{DB: f.db, match: match}
		if n := f.app.resolveStaleContradictions(context.Background()); n != 0 {
			t.Errorf("%s failing: resolved %d", match, n)
		}
	}
	f.app.cfg.DB = f.db
	if f.openConflicts(t, "x1") != 1 || f.openConflicts(t, "x2") != 1 {
		t.Errorf("a pair that could not be checked was resolved")
	}
}

// A side that cannot be read counts as in play: the pair still opens.
func TestContradictionInPlay_SideUnreadable(t *testing.T) {
	f := newPremiseFixture(t)
	f.placedDecision(t, "u1", "ratified")
	f.placedDecision(t, "u2", "ratified")
	r := f.app.signalResolver()
	r.db = faultDB{DB: f.db, match: "SELECT address, status FROM smeldr_decisions"}
	r.emitContradiction(context.Background(), RelationEdge{ID: "e", SourceType: "Decision", SourceID: "u1", TargetType: "Decision", TargetID: "u2", RelationKind: "contradicts"})
	if n := f.openConflicts(t, "u1") + f.openConflicts(t, "u2"); n != 2 {
		t.Errorf("open = %d, want 2", n)
	}
}

// ratification-required names the Decision by its address.
func TestRatificationRequired_NamesAddress(t *testing.T) {
	f := newPremiseFixture(t)
	f.placedDecision(t, "r1", "proposed")
	f.app.emitRatificationRequired(context.Background(), "r1")
	var msg string
	if err := f.db.QueryRow(`SELECT message FROM smeldr_signals WHERE signal_type = 'ratification-required' AND subject_id = 'r1'`).Scan(&msg); err != nil {
		t.Fatal(err)
	}
	if msg != "D-r1 is proposed and waits for ratification" {
		t.Errorf("message = %q", msg)
	}
}

// Re-asserting a live contradicts edge from its other end is the same edge:
// with no rev it warns rev_missing, with a stale rev it is refused.
func TestContradicts_ReversedReassertRev(t *testing.T) {
	f := newPremiseFixture(t)
	f.placedDecision(t, "a1", "ratified")
	f.placedDecision(t, "b1", "ratified")
	f.contradict(t, "a1", "b1")
	ctx := as("human-1", Human)
	_, warnings, err := f.rels.MCPAssertRelationAt(ctx, "Decision", "b1", "Decision", "a1", "contradicts", nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range warnings {
		found = found || w.Code == "rev_missing"
	}
	if !found {
		t.Errorf("reversed re-assert without rev: warnings %+v, want rev_missing", warnings)
	}
	stale := 99
	_, _, err = f.rels.MCPAssertRelationAt(ctx, "Decision", "b1", "Decision", "a1", "contradicts", nil, nil, nil, nil, &stale)
	var rc *RevConflictError
	if !errors.As(err, &rc) {
		t.Errorf("reversed re-assert with a stale rev: %v, want RevConflictError", err)
	}
}
