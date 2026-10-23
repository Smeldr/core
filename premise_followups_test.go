package smeldr

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// openPremiseSignal returns the open premise-flagged Signal about d: its ID
// and address.
func (f premiseFixture) openPremiseSignal(t *testing.T, d string) (id, address string) {
	t.Helper()
	if err := f.db.QueryRow(`SELECT id, address FROM smeldr_signals WHERE signal_type = 'premise-flagged' AND subject_id = $1 AND status = 'open'`, d).Scan(&id, &address); err != nil {
		t.Fatalf("no open condition about %s: %v", d, err)
	}
	return id, address
}

// livePlaces returns the targets of a record's live place edges.
func (f premiseFixture) livePlaces(t *testing.T, sourceID string) []string {
	t.Helper()
	rows, err := f.db.Query(`SELECT target_id FROM smeldr_relations WHERE source_id = $1 AND relation_kind LIKE 'belongs_to_%' AND invalid_at IS NULL ORDER BY created_at`, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var target string
		if err := rows.Scan(&target); err != nil {
			t.Fatal(err)
		}
		out = append(out, target)
	}
	return out
}

// TestConfirmPremiseCondition: confirming by the condition, and its refusals.
func TestConfirmPremiseCondition(t *testing.T) {
	f := newPremiseFixture(t)
	f.placedDecision(t, "d", "ratified")
	f.placedDecision(t, "p", "proposed")
	f.rest(t, "d", "grounded_in", "p")
	if _, err := f.rs.Grant(context.Background(), RoleGrant{TokenID: "agent-1", RoleName: "editor"}); err != nil {
		t.Fatal(err)
	}
	sigID, address := f.openPremiseSignal(t, "d")
	// A readable Signal that is not a premise-flagged condition.
	other := &Signal{Sender: "core", Receiver: "architect", SignalType: "question", Message: "a question", SubjectType: "Decision", SubjectID: "d"}
	if err := InsertSignal(withSignalSystem(context.Background()), f.db, other); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		ctx       context.Context
		condition string
		want      error
	}{
		"unknown":       {as("human-2", Human), "S99999", ErrNotFound},
		"not premise":   {as("human-2", Human), other.ID, ErrNotFound},
		"agent":         {as("agent-1", Agent), address, ErrForbidden},
		"no ratify":     {as("plain-1"), address, ErrForbidden},
		"reads nothing": {context.Background(), address, ErrNotFound},
	} {
		if err := f.app.ConfirmPremiseCondition(tc.ctx, tc.condition, "why"); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v; want %v", name, err, tc.want)
		}
	}
	var ve *ValidationError
	if err := f.app.ConfirmPremiseCondition(as("human-2", Human), address, " "); !errors.As(err, &ve) {
		t.Errorf("no reason: %v", err)
	}
	if err := f.app.ConfirmPremiseCondition(as("human-2", Human), address, "still right"); err != nil {
		t.Fatal(err)
	}
	var status, resolution string
	if err := f.db.QueryRow(`SELECT status, resolution FROM smeldr_signals WHERE id = $1`, sigID).Scan(&status, &resolution); err != nil || status != "resolved" || resolution != "confirmed by human-2: still right" {
		t.Errorf("signal = %s %q %v", status, resolution, err)
	}
	var state string
	if err := f.db.QueryRow(`SELECT confirmed_premise_state FROM smeldr_premise_flags WHERE signal_id = $1`, sigID).Scan(&state); err != nil || state != "proposed" {
		t.Errorf("stored premise state = %q %v", state, err)
	}
	if err := f.app.ConfirmPremiseCondition(as("human-2", Human), sigID, "again"); !errors.Is(err, ErrConflict) {
		t.Errorf("resolved condition: %v", err)
	}
	if err := (&App{}).ConfirmPremiseCondition(context.Background(), "x", "why"); !errors.Is(err, ErrInternal) {
		t.Errorf("no database: %v", err)
	}
}

// TestConfirmPremiseCondition_HiddenPremise: a ratify holder over the
// Decision who may not read its premise confirms by the condition, and no
// error, resolution, result or provenance record names the premise.
func TestConfirmPremiseCondition_HiddenPremise(t *testing.T) {
	f := newPremiseFixture(t)
	ctx := context.Background()
	f.placedDecision(t, "d", "ratified")
	f.decision(t, "hidden-premise", "proposed")
	if _, _, err := writeEdgeRow(ctx, f.db, RelationEdge{SourceType: "Decision", SourceID: "hidden-premise", TargetType: "Domain", TargetID: f.dom2,
		RelationKind: "belongs_to_domain", EdgeClass: "asserted", Attributes: json.RawMessage("{}")}); err != nil {
		t.Fatal(err)
	}
	f.rest(t, "d", "grounded_in", "hidden-premise")
	// dr-1: domain-approver and domain-reader of dom-1 only.
	if _, err := f.db.Exec(`INSERT INTO smeldr_tokens (id, name, role, expires_at, created_at, user_id, actor_class) VALUES ('fp-dr-1', 'dr-1', 'editor', '2099-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 'dr-1', 'human')`); err != nil {
		t.Fatal(err)
	}
	if err := RegisterDomainReaderRole(ctx, f.rs); err != nil {
		t.Fatal(err)
	}
	for _, g := range []RoleGrant{{TokenID: "dr-1", RoleName: RoleDomainApprover, ScopeAnchorID: f.domain}, {TokenID: "dr-1", RoleName: "domain-reader", ScopeAnchorID: f.domain}} {
		if _, err := f.rs.Grant(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	dr := as("dr-1", Human)
	sigID, address := f.openPremiseSignal(t, "d")
	var texts []string
	// Naming the premise does not work: it cannot be read.
	if err := f.app.ConfirmPremise(dr, "d", "hidden-premise", "still right"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("by the premise: %v", err)
	}
	// The condition's own refusals.
	for _, e := range []error{
		f.app.ConfirmPremiseCondition(dr, address, ""),
		f.app.ConfirmPremiseCondition(as("plain-1"), address, "why"),
	} {
		if e == nil {
			t.Fatal("a refusal was nil")
		}
		texts = append(texts, e.Error())
	}
	if err := f.app.ConfirmPremiseCondition(dr, address, "still right"); err != nil {
		t.Fatalf("by the condition: %v", err)
	}
	if e := f.app.ConfirmPremiseCondition(dr, address, "again"); e != nil {
		texts = append(texts, e.Error())
	}
	var message, resolution string
	if err := f.db.QueryRow(`SELECT message, resolution FROM smeldr_signals WHERE id = $1`, sigID).Scan(&message, &resolution); err != nil {
		t.Fatal(err)
	}
	texts = append(texts, message, resolution)
	rows, err := f.db.Query(`SELECT reason FROM smeldr_provenance WHERE subject_id = $1`, sigID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var reason string
		if err := rows.Scan(&reason); err != nil {
			t.Fatal(err)
		}
		texts = append(texts, reason)
	}
	rows.Close()
	if len(texts) < 6 {
		t.Errorf("too few texts checked: %q", texts)
	}
	for _, text := range texts {
		if strings.Contains(text, "hidden-premise") {
			t.Errorf("a text names the premise: %q", text)
		}
	}
}

// TestPremiseFlagged_Moves: the open condition follows its Decision's place,
// with exactly one live place edge after a move, none while unplaced.
func TestPremiseFlagged_Moves(t *testing.T) {
	f := newPremiseFixture(t)
	_ = f.app.Handler() // wires provenance into the relation store
	f.placedDecision(t, "d", "ratified")
	f.placedDecision(t, "p", "proposed")
	f.rest(t, "d", "grounded_in", "p")
	sigID, _ := f.openPremiseSignal(t, "d")
	if got := f.livePlaces(t, sigID); len(got) != 1 || got[0] != f.area {
		t.Fatalf("opened at %v", got)
	}
	// Withdraw the Decision's place (a ratify holder may), then place it in dom-2.
	if err := f.rels.Withdraw(as("human-1", Human), f.liveEdge(t, "d", f.area), "moving it"); err != nil {
		t.Fatal(err)
	}
	if got := f.livePlaces(t, sigID); len(got) != 0 {
		t.Errorf("unplaced Decision, condition at %v", got)
	}
	if err := f.place(as("human-1", Human), "Decision", "d", "Domain", f.dom2, "asserted"); err != nil {
		t.Fatal(err)
	}
	if got := f.livePlaces(t, sigID); len(got) != 1 || got[0] != f.dom2 {
		t.Errorf("after the move, condition at %v; want exactly %s", got, f.dom2)
	}
	var cause string
	if err := f.db.QueryRow(`SELECT to_state FROM smeldr_provenance WHERE verb = 'invalidate' AND subject_id IN (SELECT id FROM smeldr_relations WHERE source_id = $1)`, sigID).Scan(&cause); err != nil || cause != EdgeEndMoved {
		t.Errorf("end cause = %q %v", cause, err)
	}
	// A stray second place edge is ended by the next sync, keeping one.
	if _, _, err := writeEdgeRow(context.Background(), f.db, RelationEdge{SourceType: "Signal", SourceID: sigID, TargetType: "Area", TargetID: f.area,
		RelationKind: "belongs_to_area", EdgeClass: "asserted", Attributes: json.RawMessage("{}")}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.SweepPremiseFlags(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.livePlaces(t, sigID); len(got) != 1 || got[0] != f.dom2 {
		t.Errorf("after the sweep, condition at %v", got)
	}
}

// TestPremiseFlagged_MoveFails: a failed sync leaves the condition where it
// was; the sweep moves it later.
func TestPremiseFlagged_MoveFails(t *testing.T) {
	f := newPremiseFixture(t)
	f.placedDecision(t, "d", "ratified")
	f.placedDecision(t, "p", "proposed")
	f.rest(t, "d", "grounded_in", "p")
	sigID, _ := f.openPremiseSignal(t, "d")
	if err := f.rels.Withdraw(as("human-1", Human), f.liveEdge(t, "d", f.area), "moving it"); err != nil {
		t.Fatal(err)
	}
	mustExecDB(t, f.db, `CREATE TRIGGER pf_move BEFORE INSERT ON smeldr_relations WHEN NEW.source_type = 'Signal' BEGIN SELECT RAISE(ABORT, 'move'); END`)
	if err := f.place(as("human-1", Human), "Decision", "d", "Domain", f.dom2, "asserted"); err != nil {
		t.Fatal(err)
	}
	if got := f.livePlaces(t, sigID); len(got) != 0 {
		t.Errorf("failed sync placed it at %v", got)
	}
	mustExecDB(t, f.db, `DROP TRIGGER pf_move`)
	if _, err := f.app.SweepPremiseFlags(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.livePlaces(t, sigID); len(got) != 1 || got[0] != f.dom2 {
		t.Errorf("after the sweep, condition at %v", got)
	}
	// Read failures are logged, never returned.
	fail := signalResolver{db: &awaitsFailDB{DB: f.db, match: "smeldr_", err: errors.New("disk")}}
	fail.syncDecisionConditionPlaces(context.Background(), "d")
	failPlaces := signalResolver{db: &awaitsFailDB{DB: f.db, match: "source_type = 'Signal'", err: errors.New("disk")}}
	failPlaces.syncDecisionConditionPlaces(context.Background(), "d")
}

// TestConfirmPremiseCondition_Failures: a failed read or write is ErrInternal.
func TestConfirmPremiseCondition_Failures(t *testing.T) {
	f := newPremiseFixture(t)
	f.placedDecision(t, "d", "ratified")
	f.placedDecision(t, "p", "proposed")
	f.rest(t, "d", "grounded_in", "p")
	sigID, _ := f.openPremiseSignal(t, "d")
	ctx := WithReadScope(context.Background(), AllReadScope())
	for _, match := range []string{"FROM smeldr_premise_flags WHERE signal_id", "SELECT address, status FROM smeldr_decisions", "WHERE status = 'open'"} {
		app := &App{cfg: Config{DB: &awaitsFailDB{DB: f.db, match: match, err: errors.New("disk")}}}
		if err := app.ConfirmPremiseCondition(ctx, sigID, "why"); !errors.Is(err, ErrInternal) {
			t.Errorf("%s fails: %v", match, err)
		}
	}
	app := &App{cfg: Config{DB: &execFailDB{DB: f.db, failOn: "UPDATE smeldr_premise_flags"}}}
	if err := app.ConfirmPremiseCondition(ctx, sigID, "why"); !errors.Is(err, ErrInternal) {
		t.Errorf("the write fails: %v", err)
	}
}

// TestConfirmPremise_Failures: a failed read in ConfirmPremise is ErrInternal,
// and a failed read of the open conditions is logged by the sweep.
func TestConfirmPremise_Failures(t *testing.T) {
	f := newPremiseFixture(t)
	f.placedDecision(t, "d", "ratified")
	f.placedDecision(t, "p", "proposed")
	f.rest(t, "d", "grounded_in", "p")
	ctx := WithReadScope(context.Background(), AllReadScope())
	for _, match := range []string{"SELECT address, status FROM smeldr_decisions", "CAST(confirmed_at AS TEXT)", "WHERE status = 'open'"} {
		app := &App{cfg: Config{DB: &awaitsFailDB{DB: f.db, match: match, err: errors.New("disk")}}}
		if err := app.ConfirmPremise(ctx, "d", "p", "why"); !errors.Is(err, ErrInternal) {
			t.Errorf("%s fails: %v", match, err)
		}
	}
	app := &App{cfg: Config{DB: &awaitsFailDB{DB: f.db, match: "SELECT DISTINCT subject_id, '' FROM smeldr_signals WHERE signal_type = $1", err: errors.New("disk")}}}
	if _, err := app.SweepPremiseFlags(context.Background()); err != nil {
		t.Errorf("a failed read of the open conditions is logged, not returned: %v", err)
	}
}
