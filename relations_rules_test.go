package smeldr

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// T1054: two refusals at the relation door. No relation runs from a record to
// itself, and a supersedes relation between Decisions needs a ratified
// replacement and a replaced Decision that is ratified or pending
// re-evaluation.

// wantProblem fails unless err carries type t and exactly the values want.
func wantProblem(t *testing.T, err error, typ ProblemType, want map[string]string) {
	t.Helper()
	if got := ProblemTypeOf(err); got != typ {
		t.Fatalf("type %q (%v); want %q", got, err, typ)
	}
	got := ProblemValuesOf(err)
	if len(got) != len(want) {
		t.Errorf("values %v; want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("value %s = %q; want %q (all: %v)", k, got[k], v, got)
		}
	}
}

// relationRowCount counts relation rows of kind.
func relationRowCount(t *testing.T, f premiseFixture, kind string) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_relations WHERE relation_kind = $1`, kind).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRelationToItself_Refused(t *testing.T) {
	f := newPremiseFixture(t)
	f.placedDecision(t, "d", "ratified")
	f.placedDecision(t, "e", "ratified")
	ctx := as("human-1", Human)
	before := relationRowCount(t, f, "grounded_in")
	for _, class := range []string{"asserted", "inferred", "observed"} {
		_, err := f.rels.insertEdge(ctx, RelationEdge{SourceType: "Decision", SourceID: "d", TargetType: "Decision", TargetID: "d",
			RelationKind: "grounded_in", EdgeClass: class, Attributes: json.RawMessage("{}")})
		wantProblem(t, err, problemRelationToItself, map[string]string{"kind": "grounded_in", "record": "D-d"})
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Fields()[0].Field != "target_id" {
			t.Errorf("%s: %v; want a ValidationError on target_id", class, err)
		}
	}
	// Through the MCP doors, named by ID.
	if _, err := f.rels.MCPAssertRelation(ctx, "Decision", "d", "Decision", "d", "grounded_in", nil, nil, nil, nil); ProblemTypeOf(err) != problemRelationToItself {
		t.Errorf("assert_relation to itself: %v", err)
	}
	if _, err := f.rels.MCPProposeRelation(ctx, "Decision", "d", "Decision", "d", "grounded_in", nil, nil, nil, nil); ProblemTypeOf(err) != problemRelationToItself {
		t.Errorf("propose_relation to itself: %v", err)
	}
	if _, err := f.rels.MCPObserveRelation(ctx, "Decision", "d", "Decision", "d", "grounded_in", nil, nil, nil, nil); ProblemTypeOf(err) != problemRelationToItself {
		t.Errorf("observe_relation to itself: %v", err)
	}
	// Named once by address and once by ID: both ends resolve to one record,
	// and the refusal names it by its address.
	rf := newRefFixture(t)
	dec := rf.decision(t, "One")
	_, err := rf.rels.MCPAssertRelation(rf.global, "Decision", dec.Address, "Decision", dec.ID, "grounded_in", nil, nil, nil, nil)
	wantProblem(t, err, problemRelationToItself, map[string]string{"kind": "grounded_in", "record": dec.Address})
	if n := relationRowCount(t, f, "grounded_in"); n != before {
		t.Errorf("%d grounded_in rows after the refusals; want %d", n, before)
	}
	// Two records of the same type pass.
	if _, err := f.rels.insertEdge(ctx, RelationEdge{SourceType: "Decision", SourceID: "d", TargetType: "Decision", TargetID: "e",
		RelationKind: "grounded_in", EdgeClass: "asserted", Attributes: json.RawMessage("{}")}); err != nil {
		t.Errorf("d to e: %v", err)
	}
	// A record with no address is named by its ID.
	if got := relationEndLabel(context.Background(), f.db, "invoice", "x1"); got != "x1" {
		t.Errorf("label of a type with no address = %q", got)
	}
}

// replace asserts src supersedes tgt as a human approver.
func replace(f premiseFixture, src, tgt string) error {
	_, err := f.rels.insertEdge(as("human-1", Human), RelationEdge{SourceType: "Decision", SourceID: src, TargetType: "Decision", TargetID: tgt,
		RelationKind: supersedesKind, EdgeClass: "asserted", Attributes: json.RawMessage("{}")})
	return err
}

func TestReplacement_ReplacementMustBeRatified(t *testing.T) {
	for _, state := range []string{"proposed", "pending-re-evaluation", "superseded", "archived"} {
		t.Run(state, func(t *testing.T) {
			f := newPremiseFixture(t)
			f.placedDecision(t, "new", state)
			f.placedDecision(t, "old", "ratified")
			err := replace(f, "new", "old")
			wantProblem(t, err, problemDecisionReplacementState, map[string]string{"decision": "D-old", "replacement": "D-new", "state": state})
			if !errors.Is(err, ErrConflict) {
				t.Errorf("%v; want ErrConflict", err)
			}
			if n := relationRowCount(t, f, supersedesKind); n != 0 {
				t.Errorf("%d supersedes rows; want none", n)
			}
		})
	}
}

func TestReplacement_ReplacedState(t *testing.T) {
	for _, c := range []struct {
		state string
		ok    bool
	}{
		{"ratified", true}, {"pending-re-evaluation", true},
		{"proposed", false}, {"superseded", false}, {"archived", false},
	} {
		t.Run(c.state, func(t *testing.T) {
			f := newPremiseFixture(t)
			f.placedDecision(t, "new", "ratified")
			f.placedDecision(t, "old", c.state)
			err := replace(f, "new", "old")
			if c.ok {
				if err != nil {
					t.Fatalf("replacing a %s Decision: %v", c.state, err)
				}
				return
			}
			wantProblem(t, err, problemDecisionReplacementState, map[string]string{"decision": "D-old", "replacement": "D-new", "state": c.state})
		})
	}
}

// Peter's order on 2 Nov: the replacement ratified, the edge asserted, then
// the old Decision moved to superseded. After the move, no new replacement of
// it is accepted: the edge comes first.
func TestReplacement_EdgeThenMove(t *testing.T) {
	f := newPremiseFixture(t)
	f.placedDecision(t, "d170", "ratified")
	f.placedDecision(t, "d171", "proposed")
	f.setState(t, "d171", "proposed", "ratified")
	if err := replace(f, "d171", "d170"); err != nil {
		t.Fatalf("the edge before the move: %v", err)
	}
	f.setState(t, "d170", "ratified", "superseded")
	f.placedDecision(t, "d172", "ratified")
	wantProblem(t, replace(f, "d172", "d170"), problemDecisionReplacementState,
		map[string]string{"decision": "D-d170", "replacement": "D-d172", "state": "superseded"})
}

// A live edge is re-asserted with its rev after its replacement was itself
// replaced (D163 to D162 on process.smeldr.dev): not checked again.
func TestReplacement_ReassertNotChecked(t *testing.T) {
	f := newPremiseFixture(t)
	f.placedDecision(t, "d163", "ratified")
	f.placedDecision(t, "d162", "ratified")
	ctx := as("human-1", Human)
	first, _, err := f.rels.MCPAssertRelationAt(ctx, "Decision", "d163", "Decision", "d162", supersedesKind, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.setState(t, "d162", "ratified", "superseded")
	f.setState(t, "d163", "ratified", "superseded")
	rev := first.Rev
	if _, _, err := f.rels.MCPAssertRelationAt(ctx, "Decision", "d163", "Decision", "d162", supersedesKind, nil, nil, nil, json.RawMessage(`{"note":"x"}`), &rev); err != nil {
		t.Errorf("a re-assert of the live edge: %v", err)
	}
}

// What the rule leaves alone: an inferred supersedes, an end that is no stored
// Decision, and a store with no Decision table.
func TestReplacement_OutsideTheRule(t *testing.T) {
	f := newPremiseFixture(t)
	f.placedDecision(t, "new", "proposed")
	f.placedDecision(t, "old", "archived")
	if _, err := f.rels.insertEdge(as("human-1", Human), RelationEdge{SourceType: "Decision", SourceID: "new", TargetType: "Decision", TargetID: "old",
		RelationKind: supersedesKind, EdgeClass: "inferred", Attributes: json.RawMessage("{}")}); err != nil {
		t.Errorf("an inferred supersedes: %v", err)
	}
	if err := f.rels.requireReplacementState(context.Background(), RelationEdge{SourceType: "Decision", SourceID: "ghost", TargetType: "Decision", TargetID: "old",
		RelationKind: supersedesKind, EdgeClass: "asserted"}); err != nil {
		t.Errorf("an end that is no stored Decision: %v", err)
	}
	if err := f.rels.requireReplacementState(context.Background(), RelationEdge{SourceType: "Decision", SourceID: "new", TargetType: "Decision", TargetID: "old",
		RelationKind: supersedesKind, EdgeClass: "asserted", ID: "an-edge"}); err != nil {
		t.Errorf("an update of an edge by its ID: %v", err)
	}
	store := setupRelationStore(t)
	if err := store.requireReplacementState(context.Background(), RelationEdge{SourceType: "Decision", SourceID: "a", TargetType: "Decision", TargetID: "b",
		RelationKind: supersedesKind, EdgeClass: "asserted"}); err != nil {
		t.Errorf("a store with no Decision table: %v", err)
	}
}

// A failed read of the Decisions refuses the write (fail closed).
func TestReplacement_FailedReadRefuses(t *testing.T) {
	rs := &RelationStore{db: &queryFailDB{}}
	err := rs.requireReplacementState(context.Background(), RelationEdge{SourceType: "Decision", SourceID: "a", TargetType: "Decision", TargetID: "b",
		RelationKind: supersedesKind, EdgeClass: "asserted"})
	if !errors.Is(err, ErrInternal) {
		t.Errorf("a failed read: %v; want ErrInternal", err)
	}
}

// Over HTTP both refusals carry their id and values.
func TestRelationRules_HTTP(t *testing.T) {
	f := newPremiseFixture(t)
	f.placedDecision(t, "new", "proposed")
	f.placedDecision(t, "old", "ratified")
	w := writeErr(replace(f, "new", "old"), "")
	var body struct {
		Error struct {
			Code, ID, Class string
			Values          map[string]string
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != 409 || body.Error.Code != "conflict" || body.Error.ID != "decision.replacement_state" ||
		body.Error.Class != problemClassRefusal || body.Error.Values["state"] != "proposed" {
		t.Errorf("replacement refusal = %d %s", w.Code, w.Body.String())
	}
	_, err := f.rels.insertEdge(as("human-1", Human), RelationEdge{SourceType: "Decision", SourceID: "old", TargetType: "Decision", TargetID: "old",
		RelationKind: "grounded_in", EdgeClass: "asserted", Attributes: json.RawMessage("{}")})
	w = writeErr(err, "")
	body.Error.Values = nil
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != 422 || body.Error.ID != "relation.to_itself" || body.Error.Values["record"] != "D-old" {
		t.Errorf("self refusal = %d %s", w.Code, w.Body.String())
	}
}

// A runtime type's reference field cannot name its own item; another item of
// the same type passes.
func TestReferences_ToItself(t *testing.T) {
	f := newRefFixture(t)
	ctx := context.Background()
	node := &ContentTypeSchema{TypeName: "node3", Fields: []byte(`[{"name":"title","type":"string","role":"title"},{"name":"parent","type":"string","references":"node3"}]`)}
	if _, err := f.app.DefineContentType(ctx, node); err != nil {
		t.Fatal(err)
	}
	repo, err := f.app.DynamicContentRepo("node3")
	if err != nil {
		t.Fatal(err)
	}
	a, err := repo.CreateDraft(f.global, map[string]any{"title": "a"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := repo.CreateDraft(f.global, map[string]any{"title": "b"})
	if err != nil {
		t.Fatal(err)
	}
	err = repo.UpdateFields(f.global, a.ID, map[string]any{"parent": a.ID})
	if ProblemTypeOf(err) != problemRelationToItself {
		t.Fatalf("a reference to itself: %v; want relation.to_itself", err)
	}
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Fields()[0].Field != "parent" || ProblemValuesOf(err)["kind"] != "refers_to" {
		t.Errorf("refusal %v; want it on parent with kind refers_to", err)
	}
	var stored string
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_relations WHERE source_id = $1 AND relation_kind = 'refers_to'`, a.ID).Scan(&stored); err != nil || stored != "0" {
		t.Errorf("refers_to rows from a after the refusal: %s %v", stored, err)
	}
	if err := repo.UpdateFields(f.global, b.ID, map[string]any{"parent": a.ID}); err != nil {
		t.Errorf("b names a: %v", err)
	}
}
