package smeldr

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// T906 (D145): placing or moving a Decision that is no longer proposed, or an
// Area that already had a Domain, needs ratify over the old and new place.

// placeAuthorityFixture extends the approval fixture with a second Domain,
// grants, and Decisions in each state.
type placeAuthorityFixture struct {
	approvalFixture
	rels *RelationStore
	dom2 string
}

func newPlaceAuthorityFixture(t *testing.T) placeAuthorityFixture {
	t.Helper()
	f := placeAuthorityFixture{approvalFixture: newApprovalFixture(t), dom2: "dom-2"}
	f.rels = f.app.relationStore
	if _, err := f.db.Exec(`INSERT INTO smeldr_domains (id, slug, created_at, updated_at, name, address) VALUES ('dom-2', 'dom-2', datetime('now'), datetime('now'), 'Two', 'DOM2')`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE smeldr_areas SET address = 'ARE1' WHERE id = 'are-1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE smeldr_domains SET address = 'DOM1' WHERE id = 'dom-1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO smeldr_organizations (id, slug, created_at, updated_at, name, address) VALUES ('org-1', 'organization', datetime('now'), datetime('now'), 'Acme', 'ORG1')`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, g := range []RoleGrant{
		{TokenID: "human-1", RoleName: RoleApprover},
		{TokenID: "human-2", RoleName: RoleDomainApprover, ScopeAnchorID: f.domain},
		// Every actor reads everything, so the read rules do not hide an edge.
		{TokenID: "human-1", RoleName: "editor"},
		{TokenID: "human-2", RoleName: "editor"},
		{TokenID: "plain-1", RoleName: "editor"},
	} {
		if _, err := f.rs.Grant(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	// An agent token with an approver grant from before the agent rule.
	var roleID string
	if err := f.db.QueryRow(`SELECT id FROM smeldr_roles WHERE name = 'approver'`).Scan(&roleID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO smeldr_role_grants (id, token_id, role_id, scope_static, created_at) VALUES ('g-agent', 'agent-1', $1, '[]', datetime('now'))`, roleID); err != nil {
		t.Fatal(err)
	}
	return f
}

// decision inserts a Decision in state status.
func (f placeAuthorityFixture) decision(t *testing.T, id, status string) {
	t.Helper()
	if _, err := f.db.Exec(`INSERT INTO smeldr_decisions (id, slug, status, created_at, updated_at, address) VALUES ($1, $1, $2, datetime('now'), datetime('now'), $3)`, id, status, "D-"+id); err != nil {
		t.Fatal(err)
	}
}

// place asserts a place edge as ctx.
func (f placeAuthorityFixture) place(ctx context.Context, sourceType, id, targetType, target, class string) error {
	kind := placeKindOf[targetType]
	_, err := f.rels.insertEdge(ctx, RelationEdge{SourceType: sourceType, SourceID: id, TargetType: targetType, TargetID: target,
		RelationKind: kind, EdgeClass: class, Attributes: json.RawMessage("{}")})
	return err
}

// liveEdge returns the ID of the live place edge from id to target.
func (f placeAuthorityFixture) liveEdge(t *testing.T, id, target string) string {
	t.Helper()
	var edge string
	if err := f.db.QueryRow(`SELECT id FROM smeldr_relations WHERE source_id = $1 AND target_id = $2 AND invalid_at IS NULL`, id, target).Scan(&edge); err != nil {
		t.Fatalf("no live edge %s -> %s: %v", id, target, err)
	}
	return edge
}

func wantPlaceRefusal(t *testing.T, err error, msg string) {
	t.Helper()
	if !errors.Is(err, ErrForbidden) || !strings.Contains(err.Error(), msg) {
		t.Errorf("err = %v; want forbidden with %q", err, msg)
	}
}

func TestPlaceAuthority_Assert(t *testing.T) {
	f := newPlaceAuthorityFixture(t)
	approver, domApprover, author := as("human-1", Human), as("human-2", Human), as("plain-1")
	f.decision(t, "prop", "proposed")
	if err := f.place(author, "Decision", "prop", "Domain", f.dom2, "asserted"); err != nil {
		t.Errorf("an Author places a proposed Decision: %v", err)
	}
	for _, tc := range []struct {
		name, id, targetType, target string
		ctx                          context.Context
		refusal                      string // "" passes
	}{
		{"approver into a Domain", "r1", "Domain", "dom-2", approver, ""},
		{"approver onto the Organization", "r2", "Organization", "org-1", approver, ""},
		{"domain-approver into its Domain", "r3", "Domain", "dom-1", domApprover, ""},
		{"domain-approver into an Area of its Domain", "r4", "Area", "are-1", domApprover, ""},
		{"domain-approver into another Domain", "r5", "Domain", "dom-2", domApprover, "placing D-r5 in DOM2 needs ratify over DOM2"},
		{"domain-approver onto the Organization", "r6", "Organization", "org-1", domApprover, "needs ratify over ORG1"},
		{"an Author", "r7", "Domain", "dom-1", author, "needs ratify over DOM1"},
		{"an agent with an approver grant", "r8", "Domain", "dom-1", as("agent-1", Agent), "an agent token never ratifies"},
		{"no actor", "r9", "Domain", "dom-1", context.Background(), "no actor is known"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.decision(t, tc.id, "ratified")
			err := f.place(tc.ctx, "Decision", tc.id, tc.targetType, tc.target, "asserted")
			if tc.refusal == "" && err != nil {
				t.Errorf("err = %v", err)
			}
			if tc.refusal != "" {
				wantPlaceRefusal(t, err, tc.refusal)
			}
		})
	}
	// An inferred place edge is no place, so it is not gated.
	f.decision(t, "inf", "ratified")
	if err := f.place(author, "Decision", "inf", "Domain", "dom-1", "inferred"); err != nil {
		t.Errorf("inferred place edge: %v", err)
	}
	// Superseded and pending re-evaluation are gated too.
	for _, status := range []string{"superseded", "pending-re-evaluation", "archived"} {
		f.decision(t, "s-"+status, status)
		wantPlaceRefusal(t, f.place(author, "Decision", "s-"+status, "Domain", "dom-1", "asserted"), "needs ratify over DOM1")
	}
}

func TestPlaceAuthority_Withdraw(t *testing.T) {
	f := newPlaceAuthorityFixture(t)
	approver, domApprover, author := as("human-1", Human), as("human-2", Human), as("plain-1")
	f.decision(t, "w1", "ratified")
	if err := f.place(approver, "Decision", "w1", "Domain", "dom-2", "asserted"); err != nil {
		t.Fatal(err)
	}
	edge := f.liveEdge(t, "w1", "dom-2")
	wantPlaceRefusal(t, f.rels.Withdraw(domApprover, edge, "move it"), "moving D-w1 out of DOM2 needs ratify over DOM2")
	wantPlaceRefusal(t, f.rels.Withdraw(author, edge, "move it"), "needs ratify over DOM2")
	if err := f.rels.Withdraw(approver, edge, "move it"); err != nil {
		t.Errorf("approver withdraws: %v", err)
	}
	// An ended edge is left to Withdraw's own answer.
	if err := f.rels.Withdraw(author, edge, "again"); !errors.Is(err, ErrConflict) {
		t.Errorf("ended edge: %v; want conflict", err)
	}
	// A proposed Decision's place is withdrawn at the Author tier.
	f.decision(t, "w2", "proposed")
	if err := f.place(author, "Decision", "w2", "Domain", "dom-2", "asserted"); err != nil {
		t.Fatal(err)
	}
	if err := f.rels.Withdraw(author, f.liveEdge(t, "w2", "dom-2"), "inbox"); err != nil {
		t.Errorf("proposed: %v", err)
	}
}

// A domain-approver moves a ratified Decision from an Area of its Domain to
// the Domain itself, but not up to the Organization (architect's case).
func TestPlaceAuthority_DomainApproverMoves(t *testing.T) {
	f := newPlaceAuthorityFixture(t)
	approver, domApprover := as("human-1", Human), as("human-2", Human)
	f.decision(t, "m1", "ratified")
	if err := f.place(approver, "Decision", "m1", "Area", "are-1", "asserted"); err != nil {
		t.Fatal(err)
	}
	if err := f.rels.Withdraw(domApprover, f.liveEdge(t, "m1", "are-1"), "up to the Domain"); err != nil {
		t.Fatalf("out of the Area: %v", err)
	}
	if err := f.place(domApprover, "Decision", "m1", "Domain", "dom-1", "asserted"); err != nil {
		t.Fatalf("into its Domain: %v", err)
	}
	if err := f.rels.Withdraw(domApprover, f.liveEdge(t, "m1", "dom-1"), "up to the Organization"); err != nil {
		t.Fatalf("out of its Domain: %v", err)
	}
	wantPlaceRefusal(t, f.place(domApprover, "Decision", "m1", "Organization", "org-1", "asserted"), "needs ratify over ORG1")
}

func TestPlaceAuthority_AreaMoves(t *testing.T) {
	f := newPlaceAuthorityFixture(t)
	domApprover, author := as("human-2", Human), as("plain-1")
	// A new Area's first Domain stays at today's tier.
	if _, err := f.db.Exec(`INSERT INTO smeldr_areas (id, slug, created_at, updated_at, name, address) VALUES ('are-2', 'are-2', datetime('now'), datetime('now'), 'New', 'ARE2')`); err != nil {
		t.Fatal(err)
	}
	if err := f.place(author, "Area", "are-2", "Domain", "dom-2", "asserted"); err != nil {
		t.Errorf("first Domain: %v", err)
	}
	// Asserting the same live edge again moves nothing.
	if err := f.place(author, "Area", "are-2", "Domain", "dom-2", "asserted"); err != nil {
		t.Errorf("same Domain again: %v", err)
	}
	// Moving are-1 out of dom-1: its domain-approver may, an Author may not.
	edge := f.liveEdge(t, "are-1", "dom-1")
	wantPlaceRefusal(t, f.rels.Withdraw(author, edge, "move"), "moving ARE1 out of DOM1 needs ratify over DOM1")
	if err := f.rels.Withdraw(domApprover, edge, "move"); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	// It had a Domain, so placing it again is a move: not into dom-2, which
	// the domain-approver does not hold, and not by an Author.
	wantPlaceRefusal(t, f.place(domApprover, "Area", "are-1", "Domain", "dom-2", "asserted"), "moving ARE1 into DOM2 needs ratify over DOM2")
	wantPlaceRefusal(t, f.place(author, "Area", "are-1", "Domain", "dom-1", "asserted"), "needs ratify over DOM1")
	if err := f.place(domApprover, "Area", "are-1", "Domain", "dom-1", "asserted"); err != nil {
		t.Errorf("back into its Domain: %v", err)
	}
}

func TestPlaceAuthority_Errors(t *testing.T) {
	f := newPlaceAuthorityFixture(t)
	approver := as("human-1", Human)
	// A source not found by ID is refused, fail closed: an address or slug
	// given to the Go API must not skip the check.
	f.decision(t, "byaddr", "ratified")
	for _, tc := range []struct{ sourceType, id string }{{"Decision", "D-byaddr"}, {"Decision", "nope"}, {"Area", "ARE1"}} {
		if err := f.place(as("plain-1"), tc.sourceType, tc.id, "Domain", "dom-2", "asserted"); !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "by ID") {
			t.Errorf("%s %s: %v; want not found", tc.sourceType, tc.id, err)
		}
	}
	// An Area's other place kinds are not gated, and a failed Area read is internal.
	if err := f.place(as("plain-1"), "Area", "ARE1", "Organization", "org-1", "asserted"); err != nil {
		t.Errorf("Area belongs_to_organization: %v", err)
	}
	// A failed grant read is internal, not a pass.
	f.decision(t, "e1", "ratified")
	if _, err := f.db.Exec(`ALTER TABLE smeldr_role_grants RENAME TO grants_gone`); err != nil {
		t.Fatal(err)
	}
	if err := f.place(approver, "Decision", "e1", "Domain", "dom-1", "asserted"); !errors.Is(err, ErrInternal) {
		t.Errorf("grant read fails: %v; want internal", err)
	}
	if _, err := f.db.Exec(`ALTER TABLE grants_gone RENAME TO smeldr_role_grants`); err != nil {
		t.Fatal(err)
	}
	// A failed Area read is internal.
	if _, err := f.db.Exec(`ALTER TABLE smeldr_areas RENAME TO areas_gone`); err != nil {
		t.Fatal(err)
	}
	if err := f.place(approver, "Area", "are-1", "Domain", "dom-2", "asserted"); !errors.Is(err, ErrInternal) {
		t.Errorf("Area read fails: %v; want internal", err)
	}
	if _, err := f.db.Exec(`ALTER TABLE areas_gone RENAME TO smeldr_areas`); err != nil {
		t.Fatal(err)
	}
	// A failed state read is internal.
	if _, err := f.db.Exec(`ALTER TABLE smeldr_decisions RENAME TO decisions_gone`); err != nil {
		t.Fatal(err)
	}
	if err := f.place(approver, "Decision", "e1", "Domain", "dom-1", "asserted"); !errors.Is(err, ErrInternal) {
		t.Errorf("state read fails: %v; want internal", err)
	}
	if _, err := f.db.Exec(`ALTER TABLE decisions_gone RENAME TO smeldr_decisions`); err != nil {
		t.Fatal(err)
	}
	// A withdrawal of an unknown edge is left to Withdraw.
	if err := f.rels.Withdraw(approver, "no-such-edge", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown edge: %v", err)
	}
}

// Without governance on the database nobody can ratify, so nothing is gated.
func TestPlaceAuthority_NoGovernance(t *testing.T) {
	_, db, rels := placesApp(t)
	if _, err := db.Exec(`INSERT INTO smeldr_decisions (id, slug, status, created_at, updated_at) VALUES ('g1', 'g1', 'ratified', datetime('now'), datetime('now'))`); err != nil {
		t.Fatal(err)
	}
	if err := rels.Assert(context.Background(), RelationEdge{SourceType: "Decision", SourceID: "g1", TargetType: "Organization", TargetID: "org-x",
		RelationKind: "belongs_to_organization", EdgeClass: "asserted", Attributes: json.RawMessage("{}")}); err != nil {
		t.Errorf("no governance: %v", err)
	}
}
