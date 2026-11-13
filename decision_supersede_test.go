package smeldr

import (
	"context"
	"errors"
	"testing"
	"time"
)

// T1187, D189: supersede as one write, the retire act, the bare move to
// superseded, the repair, and the decision-replacement detector.

// supersedeFixture is the place-authority fixture (Decisions placed in are-1
// under dom-1; human-1 an approver, human-2 a domain approver of dom-1,
// agent-1 an agent with an old approver grant, plain-1 without ratify).
type supersedeFixture struct{ premiseFixture }

func newSupersedeFixture(t *testing.T) supersedeFixture {
	t.Helper()
	f := supersedeFixture{newPremiseFixture(t)}
	// The agent reads everything too, so its refusal is the agent rule's and
	// not the read rules'.
	if _, err := f.rs.Grant(context.Background(), RoleGrant{TokenID: "agent-1", RoleName: "editor"}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f supersedeFixture) status(t *testing.T, id string) string {
	t.Helper()
	var s string
	if err := f.db.QueryRow(`SELECT status FROM smeldr_decisions WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func (f supersedeFixture) liveSupersedes(t *testing.T, source, target string) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_relations WHERE relation_kind = 'supersedes' AND source_id = $1 AND target_id = $2 AND invalid_at IS NULL`, source, target).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (f supersedeFixture) rev(t *testing.T, id string) int {
	t.Helper()
	var r int
	if err := f.db.QueryRow(`SELECT rev FROM smeldr_decisions WHERE id = $1`, id).Scan(&r); err != nil {
		t.Fatal(err)
	}
	return r
}

func intp(n int) *int { return &n }

// TestSupersede_ExistingReplacement: form 1 asserts the relation and moves
// the replaced Decision in one write; standing follows; the reason is on the
// move and on the relation.
func TestSupersede_ExistingReplacement(t *testing.T) {
	f := newSupersedeFixture(t)
	f.placedDecision(t, "old", "ratified")
	f.placedDecision(t, "new", "ratified")
	ctx := context.Background()
	res, err := f.app.SupersedeDecision(as("human-1", Human), SupersedeRequest{Decision: "old", Replacement: "new", Reason: "new wording", Rev: intp(f.rev(t, "old"))})
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision != "D-old" || res.Replacement != "D-new" || res.Created || res.Relation == "" {
		t.Errorf("result = %+v", res)
	}
	if s := f.status(t, "old"); s != "superseded" {
		t.Errorf("old is %s", s)
	}
	if n := f.liveSupersedes(t, "new", "old"); n != 1 {
		t.Errorf("%d live supersedes relations, want 1", n)
	}
	if st, _, err := ItemStanding(ctx, f.db, "Decision", "old"); err != nil || st != StandingCeased {
		t.Errorf("old standing = %s, %v", st, err)
	}
	var reasons int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_provenance WHERE reason = 'new wording' AND (subject_id = 'old' OR subject_id = $1)`, res.Relation).Scan(&reasons); err != nil || reasons < 2 {
		t.Errorf("reason recorded %d times (%v), want on the move and on the relation", reasons, err)
	}
}

// TestSupersede_NewReplacement: form 2 creates the replacement, places it
// where the replaced one is, ratifies it (recorded as this act's move from
// proposed), and moves the replaced one, in one write. Sources are not copied.
func TestSupersede_NewReplacement(t *testing.T) {
	f := newSupersedeFixture(t)
	f.placedDecision(t, "old", "ratified")
	if _, err := f.db.Exec(`UPDATE smeldr_decisions SET scope = 'core', sources = '[{"repository":"o/r","commit":"0123456789012345678901234567890123456789","path":"a.md","role":"source"}]' WHERE id = 'old'`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	res, err := f.app.SupersedeDecision(as("human-1", Human), SupersedeRequest{Decision: "old", Reason: "replaced by a clearer rule",
		New: &NewReplacement{Title: "The clearer rule", Body: "Every write names its place."}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.Replacement == "" || res.Decision != "D-old" {
		t.Fatalf("result = %+v", res)
	}
	var id, status, scope, title, body, sources string
	if err := f.db.QueryRow(`SELECT id, status, scope, title, body, COALESCE(sources, '') FROM smeldr_decisions WHERE address = $1`, res.Replacement).
		Scan(&id, &status, &scope, &title, &body, &sources); err != nil {
		t.Fatalf("the new Decision %s: %v", res.Replacement, err)
	}
	if status != "ratified" || scope != "core" || title != "The clearer rule" || body != "Every write names its place." {
		t.Errorf("new = %s %s %q %q", status, scope, title, body)
	}
	if sources != "" && sources != "[]" && sources != "null" {
		t.Errorf("sources copied: %s", sources)
	}
	if p, ok, err := decisionPlace(ctx, f.db, id); err != nil || !ok || p.targetID != f.area {
		t.Errorf("new place = %+v %v %v, want are-1", p, ok, err)
	}
	if n := f.liveSupersedes(t, id, "old"); n != 1 || f.status(t, "old") != "superseded" {
		t.Errorf("relation %d, old %s", n, f.status(t, "old"))
	}
	if st, _, _ := ItemStanding(ctx, f.db, "Decision", id); st != StandingHolds {
		t.Errorf("new standing = %s", st)
	}
	// Point 1 of the review: the history shows the ratification by this act.
	var ratifiedBy string
	if err := f.db.QueryRow(`SELECT actor_id FROM smeldr_provenance WHERE subject_id = $1 AND verb = 'transition' AND to_state = 'ratified'`, id).Scan(&ratifiedBy); err != nil || ratifiedBy != "human-1" {
		t.Errorf("ratification recorded by %q (%v), want human-1", ratifiedBy, err)
	}
}

// TestSupersede_RetryByRev: a second send of the same request meets a rev
// conflict and creates nothing (D181's rule for creates, shown).
func TestSupersede_RetryByRev(t *testing.T) {
	f := newSupersedeFixture(t)
	f.placedDecision(t, "old", "ratified")
	rev := f.rev(t, "old")
	req := SupersedeRequest{Decision: "old", Reason: "r", Rev: intp(rev), New: &NewReplacement{Title: "Retry", Body: "b"}}
	if _, err := f.app.SupersedeDecision(as("human-1", Human), req); err != nil {
		t.Fatal(err)
	}
	var before int
	f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_decisions`).Scan(&before)
	_, err := f.app.SupersedeDecision(as("human-1", Human), req)
	var rc *RevConflictError
	if !errors.As(err, &rc) {
		// The old Decision is superseded now, which is refused before the
		// rev; either refusal writes nothing.
		if ProblemTypeOf(err) != problemDecisionReplacementState {
			t.Fatalf("retry: %v, want a rev conflict or replacement_state", err)
		}
	}
	var after int
	f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_decisions`).Scan(&after)
	if after != before {
		t.Errorf("the retry created %d Decisions", after-before)
	}
	// A stale rev on a Decision still in force: a rev conflict, nothing written.
	f.placedDecision(t, "other", "ratified")
	_, err = f.app.SupersedeDecision(as("human-1", Human), SupersedeRequest{Decision: "other", Reason: "r", Rev: intp(f.rev(t, "other") + 1), New: &NewReplacement{Title: "Stale", Body: "b"}})
	if !errors.As(err, &rc) {
		t.Fatalf("stale rev: %v, want a rev conflict", err)
	}
	f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_decisions`).Scan(&after)
	if after != before+1 || f.status(t, "other") != "ratified" {
		t.Errorf("a stale rev wrote: %d Decisions, other %s", after-before, f.status(t, "other"))
	}
}

// TestSupersede_Atomic: a failure inside the transaction leaves nothing: no
// new Decision, no address taken, no relation, the replaced one in force.
func TestSupersede_Atomic(t *testing.T) {
	f := newSupersedeFixture(t)
	f.placedDecision(t, "old", "ratified")
	var counter int
	f.db.QueryRow(`SELECT last_no FROM smeldr_address_counters WHERE type_name = 'Decision'`).Scan(&counter)
	var decisions int
	f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_decisions`).Scan(&decisions)
	if _, err := f.db.Exec(`CREATE TRIGGER no_supersedes BEFORE INSERT ON smeldr_relations WHEN NEW.relation_kind = 'supersedes' BEGIN SELECT RAISE(ABORT, 'refused by test'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := f.app.SupersedeDecision(as("human-1", Human), SupersedeRequest{Decision: "old", Reason: "r", New: &NewReplacement{Title: "Never", Body: "b"}})
	if err == nil {
		t.Fatal("the write succeeded past the trigger")
	}
	var counterAfter, decisionsAfter int
	f.db.QueryRow(`SELECT last_no FROM smeldr_address_counters WHERE type_name = 'Decision'`).Scan(&counterAfter)
	f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_decisions`).Scan(&decisionsAfter)
	if counterAfter != counter || decisionsAfter != decisions || f.status(t, "old") != "ratified" {
		t.Errorf("after a failed write: counter %d to %d, Decisions %d to %d, old %s", counter, counterAfter, decisions, decisionsAfter, f.status(t, "old"))
	}
}

// TestSupersede_RelationAlreadyLive: a supersede left half done (the
// relation asserted, the replaced one still in force) is completed by form 1:
// the live relation is kept, only the move is written.
func TestSupersede_RelationAlreadyLive(t *testing.T) {
	f := newSupersedeFixture(t)
	f.placedDecision(t, "old", "ratified")
	f.placedDecision(t, "new", "ratified")
	edge := f.rest(t, "new", "supersedes", "old")
	res, err := f.app.SupersedeDecision(as("human-1", Human), SupersedeRequest{Decision: "old", Replacement: "new", Reason: "complete it"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Relation != edge.ID || f.liveSupersedes(t, "new", "old") != 1 || f.status(t, "old") != "superseded" {
		t.Errorf("result %+v, relations %d, old %s", res, f.liveSupersedes(t, "new", "old"), f.status(t, "old"))
	}
}

// TestSupersede_Refusals: every check comes before any write.
func TestSupersede_Refusals(t *testing.T) {
	f := newSupersedeFixture(t)
	f.placedDecision(t, "old", "ratified")
	f.placedDecision(t, "new", "ratified")
	f.placedDecision(t, "draft", "proposed")
	f.decision(t, "loose", "ratified") // no place
	human := as("human-1", Human)
	for _, tc := range []struct {
		name string
		ctx  context.Context
		req  SupersedeRequest
		want error
		typ  ProblemType
	}{
		{"neither form", human, SupersedeRequest{Decision: "old", Reason: "r"}, ErrBadRequest, problemDecisionSupersedeForm},
		{"both forms", human, SupersedeRequest{Decision: "old", Reason: "r", Replacement: "new", New: &NewReplacement{Title: "t", Body: "b"}}, ErrBadRequest, problemDecisionSupersedeForm},
		{"no reason", human, SupersedeRequest{Decision: "old", Replacement: "new"}, nil, problemFieldRequired},
		{"unknown", human, SupersedeRequest{Decision: "nope", Replacement: "new", Reason: "r"}, ErrNotFound, ""},
		{"replaced not in force", human, SupersedeRequest{Decision: "draft", Replacement: "new", Reason: "r"}, ErrConflict, problemDecisionReplacementState},
		{"replacement not ratified", human, SupersedeRequest{Decision: "old", Replacement: "draft", Reason: "r"}, ErrConflict, problemDecisionReplacementState},
		{"itself", human, SupersedeRequest{Decision: "old", Replacement: "old", Reason: "r"}, ErrConflict, problemDecisionReplacementState},
		{"agent", as("agent-1", Agent), SupersedeRequest{Decision: "old", Replacement: "new", Reason: "r"}, ErrForbidden, problemRatifyAutomatedClass},
		{"no ratify", as("plain-1"), SupersedeRequest{Decision: "old", Replacement: "new", Reason: "r"}, ErrForbidden, problemPlaceAuthorityRequired},
		{"no place, new form", human, SupersedeRequest{Decision: "loose", Reason: "r", New: &NewReplacement{Title: "t", Body: "b"}}, ErrConflict, problemDecisionPlaceMissing},
		{"no place, existing form", human, SupersedeRequest{Decision: "loose", Replacement: "new", Reason: "r"}, ErrConflict, problemDecisionPlaceMissing},
		{"new without a basis", human, SupersedeRequest{Decision: "old", Reason: "r", New: &NewReplacement{Title: "t"}}, nil, problemFieldRequired},
		{"no actor", context.Background(), SupersedeRequest{Decision: "old", Replacement: "new", Reason: "r"}, ErrForbidden, problemPlaceNoActor},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.app.SupersedeDecision(tc.ctx, tc.req)
			if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) || (tc.typ != "" && ProblemTypeOf(err) != tc.typ) {
				t.Errorf("err = %v (%s), want %v %s", err, ProblemTypeOf(err), tc.want, tc.typ)
			}
			if f.status(t, "old") != "ratified" {
				t.Fatal("a refused supersede moved the Decision")
			}
		})
	}
}

// TestRetire: out of force without a replacement, with a reason, under
// ratify; never for an agent.
func TestRetire(t *testing.T) {
	f := newSupersedeFixture(t)
	f.placedDecision(t, "a", "ratified")
	f.placedDecision(t, "b", "pending-re-evaluation")
	if _, err := f.app.RetireDecision(as("human-1", Human), "a", "", nil); ProblemTypeOf(err) != problemFieldRequired {
		t.Errorf("no reason: %v", err)
	}
	if _, err := f.app.RetireDecision(as("agent-1", Agent), "a", "throwaway", nil); !errors.Is(err, ErrForbidden) {
		t.Errorf("agent: %v", err)
	}
	if _, err := f.app.RetireDecision(as("human-1", Human), "a", "throwaway", intp(f.rev(t, "a"))); err != nil || f.status(t, "a") != decisionRetired {
		t.Errorf("retire a: %v, %s", err, f.status(t, "a"))
	}
	if st, _, _ := ItemStanding(context.Background(), f.db, "Decision", "a"); st != StandingCeased {
		t.Errorf("a standing = %s", st)
	}
	if _, err := f.app.RetireDecision(as("human-1", Human), "b", "throwaway", nil); err != nil || f.status(t, "b") != decisionRetired {
		t.Errorf("retire b from pending re-evaluation: %v", err)
	}
}

// TestBareSupersede_Deprecation: the bare move works with a warning before
// the date and is refused from it; with a replacement recorded, no warning.
func TestBareSupersede_Deprecation(t *testing.T) {
	f := newSupersedeFixture(t)
	f.placedDecision(t, "a", "ratified")
	f.placedDecision(t, "b", "ratified")
	f.placedDecision(t, "c", "ratified")
	f.placedDecision(t, "d", "ratified")
	human := as("human-1", Human)
	out, err := f.app.TransitionItem(human, "Decision", "a", "superseded")
	if err != nil {
		t.Fatal(err)
	}
	ws, _ := out["warnings"].([]CreateWarning)
	found := false
	for _, w := range ws {
		found = found || w.Code == warningSupersedeWithoutReplacement
	}
	if !found {
		t.Errorf("no deprecation warning: %v", out["warnings"])
	}
	f.rest(t, "d", "supersedes", "c")
	out, err = f.app.TransitionItem(human, "Decision", "c", "superseded")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range func() []CreateWarning { w, _ := out["warnings"].([]CreateWarning); return w }() {
		if w.Code == warningSupersedeWithoutReplacement {
			t.Error("warned although the replacement is recorded")
		}
	}
	saved := decisionSupersedeCutover
	decisionSupersedeCutover = time.Now().Add(-time.Hour)
	t.Cleanup(func() { decisionSupersedeCutover = saved })
	if _, err := f.app.TransitionItem(human, "Decision", "b", "superseded"); ProblemTypeOf(err) != problemDecisionReplacementRequired || f.status(t, "b") != "ratified" {
		t.Errorf("after the date: %v, b %s", err, f.status(t, "b"))
	}
}

// TestRepairSuperseded: name the replacement, or retire; refused when there
// is nothing to repair.
func TestRepairSuperseded(t *testing.T) {
	f := newSupersedeFixture(t)
	f.placedDecision(t, "lost", "superseded")
	f.placedDecision(t, "gone", "superseded")
	f.placedDecision(t, "repl", "ratified")
	f.placedDecision(t, "draft", "proposed")
	f.placedDecision(t, "live", "ratified")
	human := as("human-1", Human)
	for _, tc := range []struct {
		name string
		req  RepairRequest
		typ  ProblemType
	}{
		{"neither", RepairRequest{Decision: "lost", Reason: "r"}, problemDecisionSupersedeForm},
		{"both", RepairRequest{Decision: "lost", Reason: "r", Replacement: "repl", Retire: true}, problemDecisionSupersedeForm},
		{"no reason", RepairRequest{Decision: "lost", Replacement: "repl"}, problemFieldRequired},
		{"not superseded", RepairRequest{Decision: "live", Reason: "r", Replacement: "repl"}, problemDecisionReplacementState},
		{"replacement never in force", RepairRequest{Decision: "lost", Reason: "r", Replacement: "draft"}, problemDecisionReplacementState},
	} {
		if _, err := f.app.RepairSuperseded(human, tc.req); ProblemTypeOf(err) != tc.typ {
			t.Errorf("%s: %v, want %s", tc.name, err, tc.typ)
		}
	}
	if _, err := f.app.RepairSuperseded(as("agent-1", Agent), RepairRequest{Decision: "lost", Reason: "r", Replacement: "repl"}); !errors.Is(err, ErrForbidden) {
		t.Errorf("agent: %v", err)
	}
	out, err := f.app.RepairSuperseded(human, RepairRequest{Decision: "lost", Reason: "D188 replaced it", Replacement: "repl", Rev: intp(f.rev(t, "lost"))})
	if err != nil || f.liveSupersedes(t, "repl", "lost") != 1 || out["replacement"] != "D-repl" {
		t.Fatalf("repair: %v %v", out, err)
	}
	if _, err := f.app.RepairSuperseded(human, RepairRequest{Decision: "lost", Reason: "again", Replacement: "repl"}); ProblemTypeOf(err) != problemDecisionRepairNotNeeded {
		t.Errorf("repaired twice: %v", err)
	}
	if _, err := f.app.RetireDecision(human, "lost", "retire it anyway", nil); ProblemTypeOf(err) != problemDecisionRepairNotNeeded {
		t.Errorf("retiring a superseded Decision with its replacement recorded: %v", err)
	}
	if _, err := f.app.RepairSuperseded(human, RepairRequest{Decision: "gone", Reason: "a throwaway", Retire: true}); err != nil || f.status(t, "gone") != decisionRetired {
		t.Errorf("repair by retiring: %v, %s", err, f.status(t, "gone"))
	}
}

// TestSweep_DecisionReplacement: both findings, and each cleared once it no
// longer fires.
func TestSweep_DecisionReplacement(t *testing.T) {
	f := newSupersedeFixture(t)
	if err := CreateFindingTable(f.db); err != nil {
		t.Fatal(err)
	}
	f.app.Findings(NewFindingStore(f.db))
	f.placedDecision(t, "lost", "superseded")
	f.placedDecision(t, "half", "ratified")
	f.placedDecision(t, "repl", "ratified")
	f.rest(t, "repl", "supersedes", "half")
	ctx := context.Background()
	if _, _, _, err := f.app.SweepStructural(ctx); err != nil {
		t.Fatal(err)
	}
	subjects := func() map[string]string {
		got := map[string]string{}
		list, err := f.app.findingStore.List(ctx, decisionReplacementDetector)
		if err != nil {
			t.Fatal(err)
		}
		for _, fd := range list {
			got[fd.SubjectID] = fd.Message
		}
		return got
	}
	got := subjects()
	if len(got) != 2 || got["lost"] == "" || got["half"] == "" {
		t.Fatalf("findings = %v", got)
	}
	if _, err := f.app.RepairSuperseded(as("human-1", Human), RepairRequest{Decision: "lost", Reason: "r", Replacement: "repl"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.SupersedeDecision(as("human-1", Human), SupersedeRequest{Decision: "half", Replacement: "repl", Reason: "complete"}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := f.app.SweepStructural(ctx); err != nil {
		t.Fatal(err)
	}
	if got := subjects(); len(got) != 0 {
		t.Errorf("findings after the repairs = %v", got)
	}
}
