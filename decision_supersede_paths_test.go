package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// T1187: the error paths of supersede, repair and the decision-replacement
// sweep that only a broken database or a missing wiring reaches.

// faultDB passes everything through to DB except the statements containing
// match: a query fails, a single-row query fails on Scan, an exec fails. It
// has no BeginTx, so a transaction runs on it statement by statement.
type faultDB struct {
	DB
	match string
}

var errFault = errors.New("fault injected by test")

func (d faultDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if strings.Contains(q, d.match) {
		return nil, errFault
	}
	return d.DB.QueryContext(ctx, q, args...)
}

func (d faultDB) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	if strings.Contains(q, d.match) {
		return d.DB.QueryRowContext(ctx, `SELECT fault_injected_by_test()`)
	}
	return d.DB.QueryRowContext(ctx, q, args...)
}

func (d faultDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	if strings.Contains(q, d.match) {
		return nil, errFault
	}
	return d.DB.ExecContext(ctx, q, args...)
}

// TestSupersede_Wiring: the surface variant, and the refusals of an App
// without governance or relations.
func TestSupersede_Wiring(t *testing.T) {
	f := newSupersedeFixture(t)
	f.placedDecision(t, "old", "ratified")
	f.placedDecision(t, "new", "ratified")
	f.placedDecision(t, "lost", "superseded")
	human := as("human-1", Human)

	gov := f.app.governance
	f.app.governance = nil
	if _, err := f.app.SupersedeDecision(human, SupersedeRequest{Decision: "old", Replacement: "new", Reason: "r"}); ProblemTypeOf(err) != problemTransitionGateUnwired {
		t.Errorf("supersede without governance: %v", err)
	}
	if _, err := f.app.RepairSuperseded(human, RepairRequest{Decision: "lost", Replacement: "new", Reason: "r"}); ProblemTypeOf(err) != problemTransitionGateUnwired {
		t.Errorf("repair without governance: %v", err)
	}
	f.app.governance = gov

	rels := f.app.relationStore
	f.app.relationStore = nil
	if _, err := f.app.SupersedeDecision(human, SupersedeRequest{Decision: "old", Replacement: "new", Reason: "r"}); !errors.Is(err, ErrInternal) {
		t.Errorf("supersede without relations: %v", err)
	}
	if _, err := f.app.RepairSuperseded(human, RepairRequest{Decision: "lost", Replacement: "new", Reason: "r"}); !errors.Is(err, ErrInternal) {
		t.Errorf("repair without relations: %v", err)
	}
	f.app.relationStore = rels

	if m := (&App{}).decisionModule(); m != nil {
		t.Errorf("decisionModule of a bare App = %v", m)
	}

	res, err := f.app.SupersedeDecisionVia(human, "mcp", SupersedeRequest{Decision: "old", Replacement: "new", Reason: "r"})
	if err != nil || res.Replacement == "" || f.status(t, "old") != "superseded" {
		t.Errorf("via mcp: %+v %v", res, err)
	}
}

// TestSupersede_MoreRefusals: the replacement's own checks, in both acts.
func TestSupersede_MoreRefusals(t *testing.T) {
	f := newSupersedeFixture(t)
	f.placedDecision(t, "old", "ratified")
	f.placedDecision(t, "lost", "superseded")
	f.placedDecision(t, "repl", "ratified")
	f.decision(t, "loose", "ratified") // no place
	human := as("human-1", Human)
	for _, tc := range []struct {
		name string
		req  SupersedeRequest
		want error
		typ  ProblemType
	}{
		{"unknown replacement", SupersedeRequest{Decision: "old", Replacement: "nope", Reason: "r"}, ErrNotFound, ""},
		{"replacement with no place", SupersedeRequest{Decision: "old", Replacement: "loose", Reason: "r"}, ErrConflict, problemDecisionPlaceMissing},
		{"stale rev", SupersedeRequest{Decision: "old", Replacement: "repl", Reason: "r", Rev: intp(f.rev(t, "old") + 5)}, nil, ""},
	} {
		_, err := f.app.SupersedeDecision(human, tc.req)
		if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) || (tc.typ != "" && ProblemTypeOf(err) != tc.typ) {
			t.Errorf("supersede, %s: %v (%s)", tc.name, err, ProblemTypeOf(err))
		}
	}
	for _, tc := range []struct {
		name string
		req  RepairRequest
		want error
		typ  ProblemType
	}{
		{"unknown", RepairRequest{Decision: "nope", Replacement: "repl", Reason: "r"}, ErrNotFound, ""},
		{"unknown replacement", RepairRequest{Decision: "lost", Replacement: "nope", Reason: "r"}, ErrNotFound, ""},
		{"replacement with no place", RepairRequest{Decision: "lost", Replacement: "loose", Reason: "r"}, ErrConflict, problemDecisionPlaceMissing},
		{"stale rev", RepairRequest{Decision: "lost", Replacement: "repl", Reason: "r", Rev: intp(f.rev(t, "lost") + 5)}, nil, ""},
	} {
		_, err := f.app.RepairSuperseded(human, tc.req)
		if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) || (tc.typ != "" && ProblemTypeOf(err) != tc.typ) {
			t.Errorf("repair, %s: %v (%s)", tc.name, err, ProblemTypeOf(err))
		}
	}
	var rc *RevConflictError
	if _, err := f.app.SupersedeDecision(human, SupersedeRequest{Decision: "old", Replacement: "repl", Reason: "r", Rev: intp(99)}); !errors.As(err, &rc) {
		t.Errorf("stale rev is not a RevConflictError: %v", err)
	}
	if f.status(t, "old") != "ratified" || f.liveSupersedes(t, "repl", "lost") != 0 {
		t.Error("a refused act wrote")
	}
}

// TestSupersede_WriteFailures: a statement refused inside the write leaves
// nothing behind, at each step of both acts.
func TestSupersede_WriteFailures(t *testing.T) {
	for _, tc := range []struct {
		name, trigger string
		repair        bool
		newForm       bool
	}{
		{"the new Decision", `BEFORE INSERT ON smeldr_decisions`, false, true},
		{"its place", `BEFORE INSERT ON smeldr_relations WHEN NEW.relation_kind = 'belongs_to_area'`, false, true},
		{"the rev", `BEFORE UPDATE OF rev ON smeldr_decisions`, false, false},
		{"the move", `BEFORE UPDATE OF status ON smeldr_decisions`, false, false},
		{"the repair's relation", `BEFORE INSERT ON smeldr_relations WHEN NEW.relation_kind = 'supersedes'`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSupersedeFixture(t)
			f.placedDecision(t, "old", "ratified")
			f.placedDecision(t, "lost", "superseded")
			f.placedDecision(t, "repl", "ratified")
			if _, err := f.db.Exec(`CREATE TRIGGER fault ` + tc.trigger + ` BEGIN SELECT RAISE(ABORT, 'refused by test'); END`); err != nil {
				t.Fatal(err)
			}
			human := as("human-1", Human)
			var err error
			switch {
			case tc.repair:
				_, err = f.app.RepairSuperseded(human, RepairRequest{Decision: "lost", Replacement: "repl", Reason: "r"})
			case tc.newForm:
				_, err = f.app.SupersedeDecision(human, SupersedeRequest{Decision: "old", Reason: "r", New: &NewReplacement{Title: "Next", Body: "b"}})
			default:
				_, err = f.app.SupersedeDecision(human, SupersedeRequest{Decision: "old", Replacement: "repl", Reason: "r"})
			}
			if err == nil {
				t.Fatal("the write succeeded past the trigger")
			}
			if f.status(t, "old") != "ratified" || f.liveSupersedes(t, "repl", "lost") != 0 || f.liveSupersedes(t, "repl", "old") != 0 {
				t.Errorf("a failed write left something behind: old %s", f.status(t, "old"))
			}
		})
	}
}

// TestSupersede_ReadFailures: a read that fails is an error, never taken as
// "nothing there".
func TestSupersede_ReadFailures(t *testing.T) {
	human := as("human-1", Human)
	for _, tc := range []struct {
		name, match string
		run         func(f supersedeFixture) error
	}{
		{"the rev", `SELECT rev FROM smeldr_decisions`, func(f supersedeFixture) error {
			_, err := f.app.SupersedeDecision(human, SupersedeRequest{Decision: "old", Replacement: "repl", Reason: "r"})
			return err
		}},
		{"the replaced scope", `SELECT scope FROM smeldr_decisions`, func(f supersedeFixture) error {
			_, err := f.app.SupersedeDecision(human, SupersedeRequest{Decision: "old", Reason: "r", New: &NewReplacement{Title: "Next", Body: "b"}})
			return err
		}},
		{"the live relation, supersede", `relation_kind = 'supersedes' AND edge_class`, func(f supersedeFixture) error {
			_, err := f.app.SupersedeDecision(human, SupersedeRequest{Decision: "old", Replacement: "repl", Reason: "r"})
			return err
		}},
		{"the live relation, repair", `relation_kind = 'supersedes' AND edge_class`, func(f supersedeFixture) error {
			_, err := f.app.RepairSuperseded(human, RepairRequest{Decision: "lost", Replacement: "repl", Reason: "r"})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSupersedeFixture(t)
			f.placedDecision(t, "old", "ratified")
			f.placedDecision(t, "lost", "superseded")
			f.placedDecision(t, "repl", "ratified")
			f.app.cfg.DB = faultDB{DB: f.app.cfg.DB, match: tc.match}
			if err := tc.run(f); !errors.Is(err, ErrInternal) {
				t.Errorf("err = %v, want ErrInternal", err)
			}
			if f.status(t, "old") != "ratified" || f.status(t, "lost") != "superseded" {
				t.Error("a failed read moved a Decision")
			}
		})
	}
}

// TestSweep_DecisionReplacement_Failures: no tables is nothing to do; a
// failing read or delete is an error.
func TestSweep_DecisionReplacement_Failures(t *testing.T) {
	f := newSupersedeFixture(t)
	f.app.Findings(NewFindingStore(f.db))
	if n, err := f.app.sweepDecisionReplacement(context.Background()); n != 0 || err != nil {
		t.Errorf("without the findings table: %d %v", n, err)
	}
	if err := CreateFindingTable(f.db); err != nil {
		t.Fatal(err)
	}
	f.placedDecision(t, "lost", "superseded")
	ctx := context.Background()
	if _, err := f.app.sweepDecisionReplacement(ctx); err != nil {
		t.Fatal(err)
	}
	// "lost" repaired by hand, so the next run deletes its finding.
	f.placedDecision(t, "repl", "ratified")
	if _, _, err := writeEdgeRow(ctx, f.db, RelationEdge{SourceType: "Decision", SourceID: "repl", TargetType: "Decision", TargetID: "lost",
		RelationKind: "supersedes", EdgeClass: "asserted", Attributes: []byte("{}")}); err != nil {
		t.Fatal(err)
	}
	db := f.app.cfg.DB
	for _, match := range []string{
		`SELECT d.id, d.status FROM smeldr_decisions`,
		`SELECT subject_id FROM smeldr_findings WHERE detector`,
		`DELETE FROM smeldr_findings WHERE detector`,
	} {
		f.app.cfg.DB = faultDB{DB: db, match: match}
		if _, err := f.app.sweepDecisionReplacement(ctx); !errors.Is(err, ErrInternal) {
			t.Errorf("%s: err = %v, want ErrInternal", match, err)
		}
	}
	f.app.cfg.DB = db
	if n, err := f.app.sweepDecisionReplacement(ctx); n != 0 || err != nil {
		t.Errorf("after the repair: %d %v", n, err)
	}
}

// TestLiveSupersedes_Error: a failing read is an internal error.
func TestLiveSupersedes_Error(t *testing.T) {
	f := newSupersedeFixture(t)
	if _, err := liveSupersedes(context.Background(), faultDB{DB: f.db, match: "smeldr_relations"}, "", "x"); !errors.Is(err, ErrInternal) {
		t.Errorf("err = %v", err)
	}
}

// TestSupersede_NewReplacementPaths: sources on the new Decision, an invalid
// one refused on the create's own rules, the D34 scope role for both
// Decisions, a refused address, a failing place read.
func TestSupersede_NewReplacementPaths(t *testing.T) {
	human := as("human-1", Human)
	t.Run("sources", func(t *testing.T) {
		f := newSupersedeFixture(t)
		f.placedDecision(t, "old", "ratified")
		res, err := f.app.SupersedeDecision(human, SupersedeRequest{Decision: "old", Reason: "r", New: &NewReplacement{Title: "Next", Body: "b",
			Sources: DecisionSources{{Repository: "acme/app", Commit: testSHA, Path: "a.md", Role: "source"}}}})
		if err != nil || !res.Created {
			t.Fatalf("%+v %v", res, err)
		}
		_, err = f.app.SupersedeDecision(human, SupersedeRequest{Decision: res.Replacement, Reason: "r", New: &NewReplacement{Title: "Bad", Body: "b",
			Sources: DecisionSources{{Commit: "x"}}}})
		if err == nil {
			t.Error("an invalid source was accepted")
		}
	})
	t.Run("scope role", func(t *testing.T) {
		f := newSupersedeFixture(t)
		f.placedDecision(t, "old", "ratified")
		f.placedDecision(t, "free", "ratified")
		if _, err := f.db.Exec(`UPDATE smeldr_decisions SET scope = 'guarded' WHERE id = 'old'`); err != nil {
			t.Fatal(err)
		}
		decisionScopeRoles["guarded"] = "guardian"
		t.Cleanup(func() { delete(decisionScopeRoles, "guarded") })
		if _, err := f.app.SupersedeDecision(human, SupersedeRequest{Decision: "old", Reason: "r", New: &NewReplacement{Title: "Next", Body: "b", Scope: "open"}}); !errors.Is(err, ErrForbidden) {
			t.Errorf("the replaced Decision's scope role: %v", err)
		}
		if _, err := f.app.SupersedeDecision(human, SupersedeRequest{Decision: "free", Reason: "r", New: &NewReplacement{Title: "Next", Body: "b", Scope: "guarded"}}); !errors.Is(err, ErrForbidden) {
			t.Errorf("the new Decision's scope role: %v", err)
		}
		if f.status(t, "old") != "ratified" || f.status(t, "free") != "ratified" {
			t.Error("a refused supersede moved a Decision")
		}
	})
	t.Run("address refused", func(t *testing.T) {
		f := newSupersedeFixture(t)
		f.placedDecision(t, "old", "ratified")
		for _, ev := range []string{"INSERT", "UPDATE"} {
			if _, err := f.db.Exec(`CREATE TRIGGER fault_` + ev + ` BEFORE ` + ev + ` ON smeldr_address_counters BEGIN SELECT RAISE(ABORT, 'refused by test'); END`); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := f.app.SupersedeDecision(human, SupersedeRequest{Decision: "old", Reason: "r", New: &NewReplacement{Title: "Next", Body: "b"}}); err == nil || f.status(t, "old") != "ratified" {
			t.Errorf("err = %v, old %s", err, f.status(t, "old"))
		}
	})
	t.Run("place read fails", func(t *testing.T) {
		f := newSupersedeFixture(t)
		f.placedDecision(t, "old", "ratified")
		f.app.cfg.DB = faultDB{DB: f.app.cfg.DB, match: "SELECT relation_kind, target_type, target_id FROM smeldr_relations"}
		if _, err := f.app.SupersedeDecision(human, SupersedeRequest{Decision: "old", Reason: "r", New: &NewReplacement{Title: "Next", Body: "b"}}); err == nil {
			t.Error("a failing place read was taken as no place")
		}
	})
}

// recordFailStore is a FindingStore whose Record fails.
type recordFailStore struct{ FindingStore }

func (recordFailStore) Record(context.Context, Finding) error { return errFault }

// TestSweep_DecisionReplacement_RecordFails: a finding that cannot be
// recorded is the sweep's error.
func TestSweep_DecisionReplacement_RecordFails(t *testing.T) {
	f := newSupersedeFixture(t)
	if err := CreateFindingTable(f.db); err != nil {
		t.Fatal(err)
	}
	f.app.Findings(recordFailStore{NewFindingStore(f.db)})
	f.placedDecision(t, "lost", "superseded")
	if _, err := f.app.sweepDecisionReplacement(context.Background()); !errors.Is(err, errFault) {
		t.Errorf("err = %v", err)
	}
}

// TestSupersede_ModulePaths: without the Decision type a new replacement
// cannot be made; the type's pre-save check refuses inside the write; a
// create through MCPCreate that fails in its transaction is returned.
func TestSupersede_ModulePaths(t *testing.T) {
	human := as("human-1", Human)
	t.Run("no Decision type", func(t *testing.T) {
		f := newSupersedeFixture(t)
		f.placedDecision(t, "old", "ratified")
		mods := f.app.mcpModules
		f.app.mcpModules = nil
		defer func() { f.app.mcpModules = mods }()
		if _, err := f.app.SupersedeDecision(human, SupersedeRequest{Decision: "old", Reason: "r", New: &NewReplacement{Title: "Next", Body: "b"}}); !errors.Is(err, ErrInternal) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("pre-save check", func(t *testing.T) {
		f := newSupersedeFixture(t)
		f.placedDecision(t, "old", "ratified")
		m := f.app.decisionModule()
		if m == nil {
			t.Skip("the fixture registers no Decision module")
		}
		saved := m.saveBefore
		m.saveBefore = func(Context, DB, any, any) error { return errFault }
		defer func() { m.saveBefore = saved }()
		if _, err := f.app.SupersedeDecision(human, SupersedeRequest{Decision: "old", Reason: "r", New: &NewReplacement{Title: "Next", Body: "b"}}); !errors.Is(err, errFault) || f.status(t, "old") != "ratified" {
			t.Errorf("err = %v, old %s", err, f.status(t, "old"))
		}
	})
	t.Run("MCPCreate write fails", func(t *testing.T) {
		f := newSupersedeFixture(t)
		m := f.app.decisionModule()
		if m == nil {
			t.Skip("the fixture registers no Decision module")
		}
		if _, err := f.db.Exec(`CREATE TRIGGER fault BEFORE INSERT ON smeldr_decisions BEGIN SELECT RAISE(ABORT, 'refused by test'); END`); err != nil {
			t.Fatal(err)
		}
		if _, err := m.MCPCreate(human.(Context), map[string]any{"title": "T", "body": "b", "scope": "s"}); err == nil {
			t.Error("the create succeeded past the trigger")
		}
	})
	t.Run("MCPCreate save hook fails", func(t *testing.T) {
		f := newSupersedeFixture(t)
		m := f.app.decisionModule()
		if m == nil {
			t.Skip("the fixture registers no Decision module")
		}
		saved := m.syncSaveHook
		m.syncSaveHook = func(context.Context, string, string, any) error { return errFault }
		defer func() { m.syncSaveHook = saved }()
		if _, err := m.MCPCreate(human.(Context), map[string]any{"title": "T", "body": "b", "scope": "s"}); !errors.Is(err, errFault) {
			t.Errorf("err = %v", err)
		}
	})
}

// TestSupersede_MovedDuringTheWrite: the replaced Decision moves after the
// checks read it (an overlapping act), with no rev sent. The write refuses
// inside its transaction and leaves nothing: no new Decision, no address, no
// relation. A trigger moves it at the moment the relation is written.
func TestSupersede_MovedDuringTheWrite(t *testing.T) {
	human := as("human-1", Human)
	for _, tc := range []struct {
		name, subject, to string
		run               func(f supersedeFixture) error
	}{
		{"form 1", "old", "pending-re-evaluation", func(f supersedeFixture) error {
			_, err := f.app.SupersedeDecision(human, SupersedeRequest{Decision: "old", Replacement: "repl", Reason: "r"})
			return err
		}},
		{"form 2", "old", "superseded", func(f supersedeFixture) error {
			_, err := f.app.SupersedeDecision(human, SupersedeRequest{Decision: "old", Reason: "r", New: &NewReplacement{Title: "Twin", Body: "b"}})
			return err
		}},
		{"repair", "lost", "retired", func(f supersedeFixture) error {
			_, err := f.app.RepairSuperseded(human, RepairRequest{Decision: "lost", Replacement: "repl", Reason: "r"})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSupersedeFixture(t)
			f.placedDecision(t, "old", "ratified")
			f.placedDecision(t, "lost", "superseded")
			f.placedDecision(t, "repl", "ratified")
			before := map[string]string{"old": f.status(t, "old"), "lost": f.status(t, "lost")}
			var decisions, counter int
			f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_decisions`).Scan(&decisions)
			f.db.QueryRow(`SELECT COALESCE(MAX(last_no), 0) FROM smeldr_address_counters WHERE type_name = 'Decision'`).Scan(&counter)
			if _, err := f.db.Exec(`CREATE TRIGGER overlap AFTER INSERT ON smeldr_relations WHEN NEW.relation_kind = 'supersedes'
				BEGIN UPDATE smeldr_decisions SET status = '` + tc.to + `' WHERE id = '` + tc.subject + `'; END`); err != nil {
				t.Fatal(err)
			}
			err := tc.run(f)
			if !errors.Is(err, ErrConflict) || ProblemTypeOf(err) != problemDecisionReplacementState {
				t.Fatalf("err = %v (%s), want decision.replacement_state", err, ProblemTypeOf(err))
			}
			var decisionsAfter, counterAfter int
			f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_decisions`).Scan(&decisionsAfter)
			f.db.QueryRow(`SELECT COALESCE(MAX(last_no), 0) FROM smeldr_address_counters WHERE type_name = 'Decision'`).Scan(&counterAfter)
			if decisionsAfter != decisions || counterAfter != counter {
				t.Errorf("Decisions %d to %d, address counter %d to %d", decisions, decisionsAfter, counter, counterAfter)
			}
			for id, st := range before {
				if got := f.status(t, id); got != st {
					t.Errorf("%s is %s, was %s", id, got, st)
				}
			}
			if f.liveSupersedes(t, "repl", "old") != 0 || f.liveSupersedes(t, "repl", "lost") != 0 {
				t.Error("a refused write left a supersedes relation")
			}
		})
	}
}

// TestSupersede_MutualRefused: a replacement that the replaced Decision
// already supersedes is refused before any write, in form 1 and in the
// repair, naming the relation in the way.
func TestSupersede_MutualRefused(t *testing.T) {
	f := newSupersedeFixture(t)
	f.placedDecision(t, "old", "ratified")
	f.placedDecision(t, "lost", "superseded")
	f.placedDecision(t, "repl", "ratified")
	ctx := context.Background()
	for _, src := range []string{"old", "lost"} {
		if _, _, err := writeEdgeRow(ctx, f.db, RelationEdge{SourceType: "Decision", SourceID: src, TargetType: "Decision", TargetID: "repl",
			RelationKind: "supersedes", EdgeClass: "asserted", Attributes: []byte("{}")}); err != nil {
			t.Fatal(err)
		}
	}
	human := as("human-1", Human)
	_, err := f.app.SupersedeDecision(human, SupersedeRequest{Decision: "old", Replacement: "repl", Reason: "r"})
	if ProblemTypeOf(err) != problemDecisionReplacementState || !strings.Contains(err.Error(), "supersede each other") {
		t.Errorf("form 1: %v", err)
	}
	_, err = f.app.RepairSuperseded(human, RepairRequest{Decision: "lost", Replacement: "repl", Reason: "r"})
	if ProblemTypeOf(err) != problemDecisionReplacementState || !strings.Contains(err.Error(), "supersede each other") {
		t.Errorf("repair: %v", err)
	}
	if f.status(t, "old") != "ratified" || f.liveSupersedes(t, "repl", "old") != 0 || f.liveSupersedes(t, "repl", "lost") != 0 {
		t.Error("a refused act wrote")
	}
}
