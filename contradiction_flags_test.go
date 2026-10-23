package smeldr

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// contradict asserts a contradicts edge between two Decisions as an approver
// and returns it (its ends in canonical order).
func (f premiseFixture) contradict(t *testing.T, a, b string) RelationEdge {
	t.Helper()
	edge, err := f.rels.insertEdge(as("human-1", Human), RelationEdge{SourceType: "Decision", SourceID: a, TargetType: "Decision", TargetID: b,
		RelationKind: "contradicts", EdgeClass: "asserted", Attributes: json.RawMessage("{}")})
	if err != nil {
		t.Fatal(err)
	}
	return edge
}

// conflicts returns the conflict-detected Signals about d: id, status,
// message and resolution.
func (f premiseFixture) conflicts(t *testing.T, d string) [][4]string {
	t.Helper()
	rows, err := f.db.Query(`SELECT id, status, message, COALESCE(resolution, '') FROM smeldr_signals WHERE signal_type = 'conflict-detected' AND subject_id = $1 ORDER BY created_at`, d)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out [][4]string
	for rows.Next() {
		var r [4]string
		if err := rows.Scan(&r[0], &r[1], &r[2], &r[3]); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// placeInDomain inserts a ratified Decision placed in Domain dom-2.
func (f premiseFixture) placeInDomain2(t *testing.T, id string) {
	t.Helper()
	f.decision(t, id, "ratified")
	if _, _, err := writeEdgeRow(context.Background(), f.db, RelationEdge{SourceType: "Decision", SourceID: id, TargetType: "Domain", TargetID: f.dom2,
		RelationKind: "belongs_to_domain", EdgeClass: "asserted", Attributes: json.RawMessage("{}")}); err != nil {
		t.Fatal(err)
	}
}

// noOther fails when a text names the other Decision, by ID or address.
func noOther(t *testing.T, what, text, other string) {
	t.Helper()
	if strings.Contains(text, other) {
		t.Errorf("%s names the other Decision %s: %q", what, other, text)
	}
}

// TestConflictDetected_Placed: each side's condition is placed with its own
// Decision and names only it; a second assert opens nothing.
func TestConflictDetected_Placed(t *testing.T) {
	f := newPremiseFixture(t)
	f.placedDecision(t, "alpha", "ratified")
	f.placeInDomain2(t, "beta")
	f.contradict(t, "alpha", "beta")
	f.contradict(t, "alpha", "beta")
	for d, want := range map[string]struct{ place, other string }{"alpha": {f.area, "beta"}, "beta": {f.dom2, "alpha"}} {
		got := f.conflicts(t, d)
		if len(got) != 1 || got[0][1] != "open" {
			t.Fatalf("%s: %v", d, got)
		}
		if got[0][2] != "D-"+d+" contradicts a Decision" {
			t.Errorf("%s message = %q", d, got[0][2])
		}
		noOther(t, d+" message", got[0][2], want.other)
		if places := f.livePlaces(t, got[0][0]); len(places) != 1 || places[0] != want.place {
			t.Errorf("%s placed at %v; want %s", d, places, want.place)
		}
	}
	var rows int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_contradiction_flags`).Scan(&rows); err != nil || rows != 2 {
		t.Errorf("pair rows = %d %v", rows, err)
	}
}

// TestConflictDetected_Resolves: a side leaving ratified and an ended edge
// resolve both conditions, each text naming only its own Decision or none.
func TestConflictDetected_Resolves(t *testing.T) {
	f := newPremiseFixture(t)
	for _, d := range []string{"a1", "b1", "a2"} {
		f.placedDecision(t, d, "ratified")
	}
	f.placeInDomain2(t, "b2")
	f.contradict(t, "a1", "b1")
	edge := f.contradict(t, "a2", "b2")
	f.setState(t, "b1", "ratified", "superseded")
	if err := f.rels.Withdraw(as("human-1", Human), edge.ID, "not a contradiction"); err != nil {
		t.Fatal(err)
	}
	for d, want := range map[string]struct{ resolution, other string }{
		"b1": {"D-b1 is no longer ratified (superseded)", "a1"},
		"a1": {"the contradicting Decision is no longer ratified", "b1"},
		"a2": {"the contradicts relation ended (withdrawn)", "b2"},
		"b2": {"the contradicts relation ended (withdrawn)", "a2"},
	} {
		got := f.conflicts(t, d)
		if len(got) != 1 || got[0][1] != "resolved" || got[0][3] != want.resolution {
			t.Errorf("%s = %v; want resolved %q", d, got, want.resolution)
			continue
		}
		noOther(t, d+" resolution", got[0][3], want.other)
		noOther(t, d+" resolution", got[0][3], edge.ID)
	}
}

// TestConflictDetected_Moves: a moved Decision takes its condition along;
// the other side's stays.
func TestConflictDetected_Moves(t *testing.T) {
	f := newPremiseFixture(t)
	_ = f.app.Handler()
	f.placedDecision(t, "alpha", "ratified")
	f.placeInDomain2(t, "beta")
	f.contradict(t, "alpha", "beta")
	if err := f.rels.Withdraw(as("human-1", Human), f.liveEdge(t, "alpha", f.area), "moving it"); err != nil {
		t.Fatal(err)
	}
	if err := f.place(as("human-1", Human), "Decision", "alpha", "Domain", f.dom2, "asserted"); err != nil {
		t.Fatal(err)
	}
	if places := f.livePlaces(t, f.conflicts(t, "alpha")[0][0]); len(places) != 1 || places[0] != f.dom2 {
		t.Errorf("alpha's condition at %v; want %s", places, f.dom2)
	}
	if places := f.livePlaces(t, f.conflicts(t, "beta")[0][0]); len(places) != 1 || places[0] != f.dom2 {
		t.Errorf("beta's condition at %v", places)
	}
}

// TestConflictDetected_ReadRules: a reader of one side's Domain reads that
// side's condition and nothing of the other's.
func TestConflictDetected_ReadRules(t *testing.T) {
	f := newPremiseFixture(t)
	ctx := context.Background()
	f.placedDecision(t, "alpha", "ratified")
	f.placeInDomain2(t, "beta")
	f.contradict(t, "alpha", "beta")
	if _, err := f.db.Exec(`INSERT INTO smeldr_tokens (id, name, role, expires_at, created_at, user_id, actor_class) VALUES ('fp-dr-1', 'dr-1', 'editor', '2099-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 'dr-1', 'human')`); err != nil {
		t.Fatal(err)
	}
	if err := RegisterDomainReaderRole(ctx, f.rs); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rs.Grant(ctx, RoleGrant{TokenID: "dr-1", RoleName: "domain-reader", ScopeAnchorID: f.domain}); err != nil {
		t.Fatal(err)
	}
	scope, err := LoadReadScope(ctx, f.rs, User{ID: "dr-1"})
	if err != nil {
		t.Fatal(err)
	}
	ends := newEndReader(ctx, f.db, scope)
	for d, want := range map[string]bool{"alpha": true, "beta": false} {
		got, err := ends.readable("Signal", f.conflicts(t, d)[0][0])
		if err != nil || got != want {
			t.Errorf("reader of dom-1 reads %s's condition: %v %v; want %v", d, got, err, want)
		}
	}
}

// TestSweepContradictions_Legacy: rows written before T928 are reworded,
// their resolutions too, backfilled and placed; a stale open one is resolved;
// an unreadable one is left. A second run changes nothing.
func TestSweepContradictions_Legacy(t *testing.T) {
	f := newPremiseFixture(t)
	ctx := context.Background()
	_ = f.app.Handler()
	for _, d := range []string{"a1", "a2", "a3", "a4"} {
		f.placedDecision(t, d, "ratified")
	}
	f.placeInDomain2(t, "b1")
	edge := f.contradict(t, "a1", "b1")
	// The new emitter opened two rows for that edge; remove them and their
	// pair rows, and write old ones instead.
	mustExecDB(t, f.db, `DELETE FROM smeldr_signals WHERE signal_type = 'conflict-detected'`)
	mustExecDB(t, f.db, `DELETE FROM smeldr_contradiction_flags`)
	old := func(subject, source, target, status, resolution string) string {
		t.Helper()
		s := &Signal{Sender: "system", Receiver: "ratify", SignalType: "conflict-detected", Message: "Decision " + source + " contradicts Decision " + target,
			SubjectType: "Decision", SubjectID: subject}
		if err := InsertSignal(withSignalSystem(ctx), f.db, s); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.Exec(`UPDATE smeldr_signals SET status = $1, resolution = $2 WHERE id = $3`, status, resolution, s.ID); err != nil {
			t.Fatal(err)
		}
		return s.ID
	}
	live := old(edge.SourceID, edge.SourceID, edge.TargetID, "open", "")
	movedOther := old("a2", "a2", "a3", "resolved", "Decision a3 moved ratified to superseded")
	movedOwn := old("a3", "a2", "a3", "resolved", "Decision a3 moved ratified to superseded")
	ended := old("a4", "a4", "a2", "resolved", "the contradicts relation e9 ended: withdrawn")
	stale := old("a4", "a4", "a3", "open", "")
	unparsed := old("a1", "x1", "y1", "open", "")

	sweep, err := f.app.SweepContradictionConditions(ctx)
	if err != nil || sweep.Reworded != 5 || sweep.Resolved != 1 || sweep.Unparsed != 1 {
		t.Fatalf("sweep = %+v %v", sweep, err)
	}
	for id, want := range map[string][3]string{
		live:       {"open", "D-" + edge.SourceID + " contradicts a Decision", ""},
		movedOther: {"resolved", "D-a2 contradicts a Decision", "the contradicting Decision is no longer ratified"},
		movedOwn:   {"resolved", "D-a3 contradicts a Decision", "D-a3 is no longer ratified (superseded)"},
		ended:      {"resolved", "D-a4 contradicts a Decision", "the contradicts relation ended (withdrawn)"},
		stale:      {"resolved", "D-a4 contradicts a Decision", "the contradicts relation ended"},
	} {
		var status, message, resolution string
		if err := f.db.QueryRow(`SELECT status, message, COALESCE(resolution, '') FROM smeldr_signals WHERE id = $1`, id).Scan(&status, &message, &resolution); err != nil {
			t.Fatal(err)
		}
		if [3]string{status, message, resolution} != want {
			t.Errorf("%s = %q %q %q; want %q", id, status, message, resolution, want)
		}
	}
	// The live row is backfilled and placed with its Decision.
	var pair int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_contradiction_flags WHERE signal_id = $1`, live).Scan(&pair); err != nil || pair != 1 {
		t.Errorf("live pair row = %d %v", pair, err)
	}
	if places := f.livePlaces(t, live); len(places) != 1 {
		t.Errorf("live row placed at %v", places)
	}
	var reason string
	if err := f.db.QueryRow(`SELECT reason FROM smeldr_provenance WHERE subject_id = $1 AND verb = 'update'`, live).Scan(&reason); err != nil || !strings.Contains(reason, "T928") {
		t.Errorf("provenance = %q %v", reason, err)
	}
	var message string
	if err := f.db.QueryRow(`SELECT message FROM smeldr_signals WHERE id = $1`, unparsed).Scan(&message); err != nil || message != "Decision x1 contradicts Decision y1" {
		t.Errorf("unparsed row changed: %q %v", message, err)
	}
	// The resolver now finds the reworded pair: the edge ends, it resolves.
	if err := f.rels.Withdraw(as("human-1", Human), edge.ID, "no longer"); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := f.db.QueryRow(`SELECT status FROM smeldr_signals WHERE id = $1`, live).Scan(&status); err != nil || status != "resolved" {
		t.Errorf("reworded row after the edge ended: %s %v", status, err)
	}
	if again, err := f.app.SweepContradictionConditions(ctx); err != nil || again.Reworded != 0 || again.Unparsed != 1 {
		t.Errorf("second sweep = %+v %v", again, err)
	}
}

// TestConflictDetected_Failures: a failed write rolls the condition back; a
// failed read is logged; the sweep's read fails as ErrInternal.
func TestConflictDetected_Failures(t *testing.T) {
	f := newPremiseFixture(t)
	ctx := context.Background()
	f.placedDecision(t, "alpha", "ratified")
	f.placeInDomain2(t, "beta")
	mustExecDB(t, f.db, `CREATE TRIGGER cd_place BEFORE INSERT ON smeldr_relations WHEN NEW.source_type = 'Signal' BEGIN SELECT RAISE(ABORT, 'place'); END`)
	f.contradict(t, "alpha", "beta")
	var n int
	if err := f.db.QueryRow(`SELECT (SELECT COUNT(*) FROM smeldr_signals WHERE signal_type = 'conflict-detected') + (SELECT COUNT(*) FROM smeldr_contradiction_flags)`).Scan(&n); err != nil || n != 0 {
		t.Errorf("a failed opening left %d rows %v", n, err)
	}
	mustExecDB(t, f.db, `DROP TRIGGER cd_place`)
	fail := signalResolver{db: &awaitsFailDB{DB: f.db, match: "smeldr_contradiction_flags", err: errors.New("disk")}}
	fail.emitContradiction(ctx, RelationEdge{SourceID: "alpha", TargetID: "beta"})
	fail.resolveContradiction(ctx, "alpha", "beta", "withdrawn")
	fail.resolveContradictionsOf(ctx, "alpha", "superseded")
	if _, err := fail.openContradiction(ctx, "alpha", "beta", "alpha"); err == nil {
		t.Error("a failed pair read must be returned")
	}
	for _, match := range []string{"message LIKE 'Decision %", "SELECT DISTINCT subject_id"} {
		app := &App{cfg: Config{DB: &awaitsFailDB{DB: f.db, match: match, err: errors.New("disk")}}}
		_, err := app.SweepContradictionConditions(ctx)
		if match == "SELECT DISTINCT subject_id" {
			if err != nil {
				t.Errorf("a failed sync read is logged, not returned: %v", err)
			}
		} else if !errors.Is(err, ErrInternal) {
			t.Errorf("%s fails: %v", match, err)
		}
	}
	if _, err := (&App{}).SweepContradictionConditions(ctx); !errors.Is(err, ErrInternal) {
		t.Errorf("no database: %v", err)
	}
	if err := EnsureContradictionFlagTable(ctx, &execFailDB{DB: f.db, failOn: "smeldr_contradiction_flags"}); err == nil {
		t.Error("EnsureContradictionFlagTable: a failed create must be returned")
	}
	if err := EnsureContradictionFlagTable(ctx, f.db); err != nil {
		t.Errorf("EnsureContradictionFlagTable: %v", err)
	}
	if got := rewordContradictionResolution("something else", "a", "D-a"); got != "resolved" {
		t.Errorf("unknown resolution reworded to %q", got)
	}
	if got := rewordContradictionResolution("the contradicts relation e1 vanished", "a", "D-a"); got != "the contradicts relation ended" {
		t.Errorf("odd relation text reworded to %q", got)
	}
}

// TestSweepContradictions_Paths: an old open row whose other side left
// ratified, failed rewrites, and failed reads on the emit path.
func TestSweepContradictions_Paths(t *testing.T) {
	f := newPremiseFixture(t)
	ctx := context.Background()
	f.placedDecision(t, "a1", "ratified")
	f.placedDecision(t, "b1", "superseded")
	f.contradict(t, "a1", "b1")
	mustExecDB(t, f.db, `DELETE FROM smeldr_signals WHERE signal_type = 'conflict-detected'`)
	mustExecDB(t, f.db, `DELETE FROM smeldr_contradiction_flags`)
	s := &Signal{Sender: "system", Receiver: "ratify", SignalType: "conflict-detected", Message: "Decision a1 contradicts Decision b1", SubjectType: "Decision", SubjectID: "a1"}
	if err := InsertSignal(withSignalSystem(ctx), f.db, s); err != nil {
		t.Fatal(err)
	}
	// A rewrite that fails is logged and the row is left for the next run.
	for _, failOn := range []string{"UPDATE smeldr_signals SET message", "INSERT INTO smeldr_contradiction_flags"} {
		app := &App{cfg: Config{DB: &execFailDB{DB: f.db, failOn: failOn}}}
		if res, err := app.SweepContradictionConditions(ctx); err != nil || res.Reworded != 0 {
			t.Errorf("%s fails: %+v %v", failOn, res, err)
		}
	}
	app := &App{cfg: Config{DB: &awaitsFailDB{DB: f.db, match: "relation_kind = 'contradicts'", err: errors.New("disk")}}}
	if res, err := app.SweepContradictionConditions(ctx); err != nil || res.Resolved != 0 {
		t.Errorf("edge read fails: %+v %v", res, err)
	}
	mustExecDB(t, f.db, `UPDATE smeldr_signals SET message = 'Decision a1 contradicts Decision b1' WHERE id = '`+s.ID+`'`)
	mustExecDB(t, f.db, `DELETE FROM smeldr_contradiction_flags`)
	res, err := f.app.SweepContradictionConditions(ctx)
	if err != nil || res.Resolved != 1 {
		t.Fatalf("sweep = %+v %v", res, err)
	}
	var resolution string
	if err := f.db.QueryRow(`SELECT resolution FROM smeldr_signals WHERE id = $1`, s.ID).Scan(&resolution); err != nil || resolution != "the contradicting Decision is no longer ratified" {
		t.Errorf("resolution = %q %v", resolution, err)
	}
	// A failed Decision read labels by ID; a failed place read fails the opening.
	if got := decisionLabel(ctx, &awaitsFailDB{DB: f.db, match: "FROM smeldr_decisions", err: errors.New("disk")}, "a1"); got != "Decision a1" {
		t.Errorf("label on a failed read = %q", got)
	}
	r := signalResolver{db: &awaitsFailDB{DB: f.db, match: "relation_kind IN ($2, $3, $4)", err: errors.New("disk")}}
	if _, err := r.openContradiction(ctx, "a1", "b1", "a1"); err == nil {
		t.Error("a failed place read must be returned")
	}
	mustExecDB(t, f.db, `CREATE TRIGGER cd_sig BEFORE INSERT ON smeldr_signals BEGIN SELECT RAISE(ABORT, 'sig'); END`)
	if _, err := f.app.signalResolver().openContradiction(ctx, "x", "y", "x"); err == nil {
		t.Error("a failed Signal insert must be returned")
	}
}
