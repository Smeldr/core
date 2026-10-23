package smeldr

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// premiseFixture is the place-authority fixture with Decisions placed in
// are-1 (Domain dom-1), where human-2 is a domain-approver and human-1 an
// approver.
type premiseFixture struct{ placeAuthorityFixture }

func newPremiseFixture(t *testing.T) premiseFixture {
	t.Helper()
	return premiseFixture{newPlaceAuthorityFixture(t)}
}

// placedDecision inserts a Decision in status, placed in are-1.
func (f premiseFixture) placedDecision(t *testing.T, id, status string) {
	t.Helper()
	f.decision(t, id, status)
	if _, _, err := writeEdgeRow(context.Background(), f.db, RelationEdge{SourceType: "Decision", SourceID: id, TargetType: "Area", TargetID: f.area,
		RelationKind: "belongs_to_area", EdgeClass: "asserted", Attributes: json.RawMessage("{}")}); err != nil {
		t.Fatal(err)
	}
}

// rest asserts d -kind-> p through the store, as a human approver would.
func (f premiseFixture) rest(t *testing.T, d, kind, p string) RelationEdge {
	t.Helper()
	edge, err := f.rels.insertEdge(as("human-1", Human), RelationEdge{SourceType: "Decision", SourceID: d, TargetType: "Decision", TargetID: p,
		RelationKind: kind, EdgeClass: "asserted", Attributes: json.RawMessage("{}")})
	if err != nil {
		t.Fatal(err)
	}
	return edge
}

// setState moves a Decision by SQL and runs the resolver as a committed
// transition would.
func (f premiseFixture) setState(t *testing.T, id, from, to string) {
	t.Helper()
	if _, err := f.db.Exec(`UPDATE smeldr_decisions SET status = $1 WHERE id = $2`, to, id); err != nil {
		t.Fatal(err)
	}
	f.app.resolveSignalsFor(context.Background(), statusTransition{typeName: "Decision", id: id, from: from, to: to})
}

// premiseSignals returns the premise-flagged Signals about d, by status.
func (f premiseFixture) premiseSignals(t *testing.T, d string) map[string][]string {
	t.Helper()
	rows, err := f.db.Query(`SELECT id, status, message, resolution FROM smeldr_signals WHERE signal_type = 'premise-flagged' AND subject_id = $1 ORDER BY created_at`, d)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var id, status, message, resolution string
		if err := rows.Scan(&id, &status, &message, &resolution); err != nil {
			t.Fatal(err)
		}
		out[status] = append(out[status], id+"|"+message+"|"+resolution)
	}
	return out
}

// TestPremiseFlagged_Message: the condition opens on the edge, names the
// resting Decision only, and is placed where it is placed, in one write.
func TestPremiseFlagged_Message(t *testing.T) {
	f := newPremiseFixture(t)
	f.placedDecision(t, "d", "ratified")
	f.placedDecision(t, "p", "proposed")
	f.rest(t, "d", "grounded_in", "p")
	open := f.premiseSignals(t, "d")["open"]
	if len(open) != 1 {
		t.Fatalf("open = %v", open)
	}
	parts := strings.Split(open[0], "|")
	if parts[1] != "D-d rests on a Decision that is not in force" {
		t.Errorf("message = %q", parts[1])
	}
	var receiver, operation, subjectType string
	if err := f.db.QueryRow(`SELECT receiver, required_role, subject_type FROM smeldr_signals WHERE id = $1`, parts[0]).Scan(&receiver, &operation, &subjectType); err != nil {
		t.Fatal(err)
	}
	if receiver != "ratify" || operation != "ratify" || subjectType != "Decision" {
		t.Errorf("receiver %s, operation %s, subject %s", receiver, operation, subjectType)
	}
	// Placed in the Decision's Area.
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_relations WHERE source_type = 'Signal' AND source_id = $1 AND relation_kind = 'belongs_to_area' AND target_id = $2`, parts[0], f.area).Scan(&n); err != nil || n != 1 {
		t.Errorf("place edges = %d %v", n, err)
	}
	// No address but D's, and not the premise's ID either.
	for _, other := range []string{"D-p", "p rests"} {
		if strings.Contains(parts[1], other) {
			t.Errorf("message names the premise: %q", parts[1])
		}
	}
	// addresses opens it as well; an unplaced Decision gets no place edge.
	f.decision(t, "u", "ratified")
	f.rest(t, "u", "addresses", "p")
	if got := f.premiseSignals(t, "u")["open"]; len(got) != 1 {
		t.Errorf("addresses: %v", got)
	}
}

// TestPremiseFlagged_Once: evaluating an open pair again opens nothing more.
func TestPremiseFlagged_Once(t *testing.T) {
	f := newPremiseFixture(t)
	f.placedDecision(t, "d", "ratified")
	f.placedDecision(t, "p", "proposed")
	f.rest(t, "d", "grounded_in", "p")
	f.rest(t, "d", "addresses", "p")
	f.setState(t, "d", "pending-re-evaluation", "ratified")
	sweep, err := f.app.SweepPremiseFlags(context.Background())
	if err != nil || sweep.Opened != 0 || sweep.Pairs != 1 {
		t.Errorf("sweep = %+v %v", sweep, err)
	}
	if got := f.premiseSignals(t, "d"); len(got["open"]) != 1 || len(got) != 1 {
		t.Errorf("signals = %v", got)
	}
	// Nothing opens while the resting Decision is not in force, nor when both are.
	f.placedDecision(t, "q", "proposed")
	f.rest(t, "q", "grounded_in", "p")
	f.placedDecision(t, "r", "ratified")
	f.placedDecision(t, "s", "ratified")
	f.rest(t, "r", "grounded_in", "s")
	if len(f.premiseSignals(t, "q")) != 0 || len(f.premiseSignals(t, "r")) != 0 {
		t.Error("opened without the rule")
	}
}

// TestPremiseFlagged_Resolves: each way the rule stops holding resolves the
// condition, with the reason saying which.
func TestPremiseFlagged_Resolves(t *testing.T) {
	f := newPremiseFixture(t)
	for _, id := range []string{"d1", "d2", "d3"} {
		f.placedDecision(t, id, "ratified")
	}
	f.placedDecision(t, "p", "proposed")
	f.placedDecision(t, "p2", "superseded")
	f.rest(t, "d1", "grounded_in", "p")
	edge := f.rest(t, "d2", "grounded_in", "p2")
	f.rest(t, "d3", "addresses", "p2")
	for _, d := range []string{"d1", "d2", "d3"} {
		if len(f.premiseSignals(t, d)["open"]) != 1 {
			t.Fatalf("%s not open", d)
		}
	}
	// The premise is ratified: in force again.
	f.setState(t, "p", "proposed", "ratified")
	// The relation ends.
	if err := f.rels.Withdraw(as("human-1", Human), edge.ID, "no longer rests on it"); err != nil {
		t.Fatal(err)
	}
	// The resting Decision is superseded.
	f.setState(t, "d3", "ratified", "superseded")
	for d, want := range map[string]string{
		"d1": "the premise of D-d1 is in force again",
		"d2": "the relation from D-d2 to its premise ended",
		"d3": "D-d3 is no longer in force (superseded)",
	} {
		got := f.premiseSignals(t, d)
		if len(got["open"]) != 0 || len(got["resolved"]) != 1 || !strings.HasSuffix(got["resolved"][0], "|"+want) {
			t.Errorf("%s = %v; want resolved %q", d, got, want)
		}
	}
	// A Decision that is gone: the sweep resolves its pair.
	f.placedDecision(t, "d4", "ratified")
	f.rest(t, "d4", "grounded_in", "p2")
	if _, err := f.db.Exec(`DELETE FROM smeldr_decisions WHERE id = 'p2'`); err != nil {
		t.Fatal(err)
	}
	sweep, err := f.app.SweepPremiseFlags(context.Background())
	if err != nil || sweep.Resolved != 1 {
		t.Errorf("sweep = %+v %v", sweep, err)
	}
	if got := f.premiseSignals(t, "d4")["resolved"]; len(got) != 1 || !strings.HasSuffix(got[0], "|the premise of D-d4 no longer exists") {
		t.Errorf("premise gone = %v", got)
	}
	if _, err := f.db.Exec(`DELETE FROM smeldr_decisions WHERE id = 'd1'`); err != nil {
		t.Fatal(err)
	}
	f.setState(t, "p", "ratified", "superseded") // d1 is gone: nothing opens
	if got := f.premiseSignals(t, "d1"); len(got["open"]) != 0 {
		t.Errorf("gone Decision opened: %v", got)
	}
}

// TestConfirmPremise: who may confirm, and the pair stays closed until the
// premise's state changes, then opens again (condition 4).
func TestConfirmPremise(t *testing.T) {
	f := newPremiseFixture(t)
	f.placedDecision(t, "d", "ratified")
	f.placedDecision(t, "p", "proposed")
	f.placedDecision(t, "other", "proposed")
	f.rest(t, "d", "grounded_in", "p")
	// The agent reads everything, so the refusal is its class, not the read.
	if _, err := f.rs.Grant(context.Background(), RoleGrant{TokenID: "agent-1", RoleName: "editor"}); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		ctx              context.Context
		decision, reason string
		premise          string
		want             error
	}{
		"no reason":              {as("human-2", Human), "d", "  ", "p", nil},
		"agent":                  {as("agent-1", Agent), "d", "still right", "p", ErrForbidden},
		"no ratify":              {as("plain-1"), "d", "still right", "p", ErrForbidden},
		"unknown":                {as("human-2", Human), "nope", "still right", "p", ErrNotFound},
		"unknown premise":        {as("human-2", Human), "d", "still right", "nope", ErrNotFound},
		"no open condition":      {as("human-2", Human), "d", "still right", "other", ErrConflict},
		"no actor reads nothing": {context.Background(), "d", "still right", "p", ErrNotFound},
	} {
		err := f.app.ConfirmPremise(tc.ctx, tc.decision, tc.premise, tc.reason)
		if tc.want == nil {
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Errorf("%s: %v; want a validation error", name, err)
			}
			continue
		}
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: %v; want %v", name, err, tc.want)
		}
	}
	if err := f.app.ConfirmPremise(as("human-2", Human), "d", "p", "the premise's draft is what we follow"); err != nil {
		t.Fatal(err)
	}
	got := f.premiseSignals(t, "d")
	if len(got["open"]) != 0 || len(got["resolved"]) != 1 || !strings.HasSuffix(got["resolved"][0], "|confirmed by human-2: the premise's draft is what we follow") {
		t.Fatalf("confirmed = %v", got)
	}
	var by, reason, state string
	if err := f.db.QueryRow(`SELECT confirmed_by, confirmed_reason, confirmed_premise_state FROM smeldr_premise_flags WHERE decision_id = 'd' AND premise_id = 'p'`).Scan(&by, &reason, &state); err != nil || by != "human-2" || state != "proposed" {
		t.Errorf("row = %s %s %s %v", by, reason, state, err)
	}
	// The premise unchanged: re-evaluation keeps it closed.
	f.setState(t, "d", "pending-re-evaluation", "ratified")
	if sweep, _ := f.app.SweepPremiseFlags(context.Background()); sweep.Opened != 0 || len(f.premiseSignals(t, "d")["open"]) != 0 {
		t.Errorf("reopened with the premise unchanged: %+v", sweep)
	}
	// The premise's state changes (and the rule still holds): it opens again,
	// and the confirmation is cleared.
	f.setState(t, "p", "proposed", "archived")
	if got := f.premiseSignals(t, "d"); len(got["open"]) != 1 {
		t.Errorf("not reopened on a new premise state: %v", got)
	}
	if err := f.db.QueryRow(`SELECT confirmed_by, confirmed_premise_state FROM smeldr_premise_flags WHERE decision_id = 'd' AND premise_id = 'p'`).Scan(&by, &state); err != nil || by != "" || state != "" {
		t.Errorf("confirmation not cleared: %s %s %v", by, state, err)
	}
	if err := (&App{}).ConfirmPremise(context.Background(), "a", "b", "why"); !errors.Is(err, ErrInternal) {
		t.Errorf("no database: %v", err)
	}
}

// TestPremiseFlagged_Ratify: a real transition into ratified opens the
// condition of a Decision whose premise is not in force.
func TestPremiseFlagged_Ratify(t *testing.T) {
	f := newPremiseFixture(t)
	f.placedDecision(t, "d", "proposed")
	f.placedDecision(t, "p", "proposed")
	f.rest(t, "d", "grounded_in", "p")
	if len(f.premiseSignals(t, "d")) != 0 {
		t.Fatal("opened while proposed")
	}
	if _, err := f.app.TransitionItem(as("human-1", Human), "Decision", "d", "ratified"); err != nil {
		t.Fatal(err)
	}
	if got := f.premiseSignals(t, "d")["open"]; len(got) != 1 {
		t.Errorf("after ratify: %v", got)
	}
}

// TestSweepPremiseFlags_Failures: reading the pairs fails as ErrInternal; a
// pair that fails is counted and the sweep goes on.
func TestSweepPremiseFlags_Failures(t *testing.T) {
	f := newPremiseFixture(t)
	f.placedDecision(t, "d", "ratified")
	f.placedDecision(t, "p", "proposed")
	if _, _, err := writeEdgeRow(context.Background(), f.db, RelationEdge{SourceType: "Decision", SourceID: "d", TargetType: "Decision", TargetID: "p",
		RelationKind: "grounded_in", EdgeClass: "asserted", Attributes: json.RawMessage("{}")}); err != nil {
		t.Fatal(err)
	}
	if _, err := (&App{cfg: Config{DB: &govQueryFailDB{DB: f.db, failOn: "UNION"}}}).SweepPremiseFlags(context.Background()); !errors.Is(err, ErrInternal) {
		t.Errorf("pairs fail: %v", err)
	}
	for _, failOn := range []string{"SELECT address, status FROM smeldr_decisions", "SELECT COUNT(*) FROM smeldr_relations", "FROM smeldr_premise_flags WHERE decision_id", "WHERE status = 'open'"} {
		app := &App{cfg: Config{DB: &awaitsFailDB{DB: f.db, match: failOn, err: errors.New("disk")}}}
		if failOn == "WHERE status = 'open'" {
			// An open Signal must exist for the lookup to run.
			if _, err := f.app.SweepPremiseFlags(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		sweep, err := app.SweepPremiseFlags(context.Background())
		if err != nil || sweep.Failed != 1 {
			t.Errorf("%s: %+v %v", failOn, sweep, err)
		}
	}
	if _, err := (&App{}).SweepPremiseFlags(context.Background()); !errors.Is(err, ErrInternal) {
		t.Errorf("no database: %v", err)
	}
	if err := EnsurePremiseFlagTable(context.Background(), &execFailDB{DB: f.db, failOn: "smeldr_premise_flags"}); err == nil {
		t.Error("EnsurePremiseFlagTable: a failed create must be returned")
	}
	if err := EnsurePremiseFlagTable(context.Background(), f.db); err != nil {
		t.Errorf("EnsurePremiseFlagTable: %v", err)
	}
}

// TestPremiseFlagged_OpenRaces: a pair another evaluation claimed first opens
// nothing, and a failed write rolls the whole opening back.
func TestPremiseFlagged_OpenRaces(t *testing.T) {
	f := newPremiseFixture(t)
	f.placedDecision(t, "d", "ratified")
	f.placedDecision(t, "p", "proposed")
	f.rest(t, "d", "grounded_in", "p")
	r := f.app.signalResolver()
	ctx := context.Background()
	count := func() int {
		var n int
		if err := f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_signals WHERE signal_type = 'premise-flagged'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := count()
	// The row moved on (stale signal), or exists already (a new pair): taken.
	for _, row := range []premiseRow{{found: true, signalID: "stale"}, {}} {
		if did, err := r.openPremise(ctx, "d", "p", "D-d", row); err != nil || did != "" {
			t.Errorf("taken %+v: %q %v", row, did, err)
		}
	}
	if count() != before {
		t.Error("a taken pair left a Signal")
	}
	// The place edge fails: nothing stays.
	mustExecDB(t, f.db, `CREATE TRIGGER pf_edge BEFORE INSERT ON smeldr_relations WHEN NEW.source_type = 'Signal' BEGIN SELECT RAISE(ABORT, 'edge'); END`)
	mustExecDB(t, f.db, `DELETE FROM smeldr_premise_flags`)
	if _, err := r.openPremise(ctx, "d", "p", "D-d", premiseRow{}); err == nil {
		t.Error("a failed place edge must be returned")
	}
	if count() != before {
		t.Error("a failed opening left a Signal")
	}
	mustExecDB(t, f.db, `DROP TRIGGER pf_edge`)
	mustExecDB(t, f.db, `CREATE TRIGGER pf_sig BEFORE INSERT ON smeldr_signals BEGIN SELECT RAISE(ABORT, 'sig'); END`)
	if _, err := r.openPremise(ctx, "d", "p", "D-d", premiseRow{}); err == nil {
		t.Error("a failed Signal insert must be returned")
	}
	// The fail-open entry points log and return.
	fail := signalResolver{db: &awaitsFailDB{DB: f.db, match: "smeldr_", err: errors.New("disk")}}
	fail.evaluatePremise(ctx, "d", "p")
	fail.evaluatePremisesOf(ctx, "d")
	bare := signalResolver{db: newSQLiteDB(t)}
	bare.evaluatePremise(ctx, "d", "p")
	bare.evaluatePremisesOf(ctx, "d")
}
