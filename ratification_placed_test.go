package smeldr

import (
	"context"
	"testing"
	"time"
)

// T1289: ratification-required is placed with its Decision, so the place's
// approver reads the request it is asked to answer.

// ratificationOf returns the ID of the open ratification-required Signal
// about decision.
func (f approvalFixture) ratificationOf(t *testing.T, decision string) string {
	t.Helper()
	var id string
	if err := f.db.QueryRow(`SELECT id FROM smeldr_signals WHERE signal_type = 'ratification-required' AND subject_id = $1 AND status = 'open'`, decision).Scan(&id); err != nil {
		t.Fatalf("no open request for %s: %v", decision, err)
	}
	return id
}

// placedIn returns the targets of the Signal's live place edges.
func (f approvalFixture) placedIn(t *testing.T, signalID string) []string {
	t.Helper()
	edges, err := signalPlaceEdges(context.Background(), f.db, signalID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range edges {
		out = append(out, e.TargetID)
	}
	return out
}

func (f approvalFixture) readsSignal(t *testing.T, token, id string) bool {
	t.Helper()
	scope, err := LoadReadScope(context.Background(), f.rs, User{ID: token})
	if err != nil {
		t.Fatal(err)
	}
	return scope.Readable("Signal", id)
}

func TestRatificationRequest_Placed(t *testing.T) {
	f := newApprovalReadFixture(t)
	ctx := context.Background()
	for _, g := range []RoleGrant{
		{TokenID: "dom-appr", RoleName: RoleDomainApprover, ScopeAnchorID: "dom-1"},
		{TokenID: "are-appr", RoleName: RoleAreaApprover, ScopeAnchorID: "are-1"},
		{TokenID: "other-appr", RoleName: RoleDomainApprover, ScopeAnchorID: "dom-2"},
	} {
		if _, err := f.rs.Grant(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	f.app.emitRatificationRequired(ctx, "d-a")
	id := f.ratificationOf(t, "d-a")
	if got := f.placedIn(t, id); len(got) != 1 || got[0] != "are-1" {
		t.Errorf("request placed in %v; want are-1", got)
	}
	for token, want := range map[string]bool{"dom-appr": true, "are-appr": true, "other-appr": false} {
		if got := f.readsSignal(t, token, id); got != want {
			t.Errorf("%s reads the request = %v, want %v", token, got, want)
		}
	}
}

// A Decision with no place: the request stays unplaced, is placed when the
// Decision is, and follows it when it moves.
func TestRatificationRequest_FollowsItsDecision(t *testing.T) {
	f := newApprovalReadFixture(t)
	ctx := context.Background()
	f.app.emitRatificationRequired(ctx, "d-u")
	id := f.ratificationOf(t, "d-u")
	if got := f.placedIn(t, id); len(got) != 0 {
		t.Fatalf("an unplaced Decision's request is placed in %v", got)
	}
	r := f.app.signalResolver()
	edge, _, err := writeEdgeRow(ctx, f.db, RelationEdge{SourceType: "Decision", SourceID: "d-u", TargetType: "Area", TargetID: "are-1",
		RelationKind: "belongs_to_area", EdgeClass: "asserted", Attributes: []byte("{}")})
	if err != nil {
		t.Fatal(err)
	}
	r.premiseEdgeChanged(ctx, edge)
	if got := f.placedIn(t, id); len(got) != 1 || got[0] != "are-1" {
		t.Errorf("after placing the Decision: %v; want are-1", got)
	}
	if _, err := f.db.Exec(`UPDATE smeldr_relations SET invalid_at = $1 WHERE id = $2`, time.Now().Add(-time.Second).UTC(), edge.ID); err != nil {
		t.Fatal(err)
	}
	moved, _, err := writeEdgeRow(ctx, f.db, RelationEdge{SourceType: "Decision", SourceID: "d-u", TargetType: "Area", TargetID: "are-2",
		RelationKind: "belongs_to_area", EdgeClass: "asserted", Attributes: []byte("{}")})
	if err != nil {
		t.Fatal(err)
	}
	r.premiseEdgeChanged(ctx, moved)
	if got := f.placedIn(t, id); len(got) != 1 || got[0] != "are-2" {
		t.Errorf("after moving the Decision: %v; want are-2", got)
	}
}

// A request written before this change, with no place edge, is placed by
// the sweep; a second sweep changes nothing.
func TestRatificationRequest_SweepPlacesOldOnes(t *testing.T) {
	f := newApprovalReadFixture(t)
	ctx := context.Background()
	if err := f.app.signalResolver().emit(ctx, ratificationSignal("D-d-d", "d-d")); err != nil {
		t.Fatal(err)
	}
	id := f.ratificationOf(t, "d-d")
	if got := f.placedIn(t, id); len(got) != 0 {
		t.Fatalf("placed before the sweep: %v", got)
	}
	for i := 0; i < 2; i++ {
		if _, err := f.app.SweepContradictionConditions(ctx); err != nil {
			t.Fatal(err)
		}
		if got := f.placedIn(t, id); len(got) != 1 || got[0] != "dom-1" {
			t.Errorf("sweep %d: placed in %v; want dom-1", i+1, got)
		}
	}
}
