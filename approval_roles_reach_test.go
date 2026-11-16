package smeldr

import (
	"context"
	"testing"
)

// T1247, D191: what each approval role reads, both ways.

// newApprovalReadFixture is the approval fixture with two Organizations, a
// Domain and an Area in each, and Decisions placed at each level: d-a in
// are-1, d-d in dom-1, d-x in are-2 (the other Organization), d-u unplaced.
func newApprovalReadFixture(t *testing.T) approvalFixture {
	t.Helper()
	f := newApprovalFixture(t)
	ctx := context.Background()
	for _, q := range []string{
		// The schema holds one Organization (idx_smeldr_organizations_one);
		// the rule is "for each", so the test drops the index to hold two.
		`DROP INDEX idx_smeldr_organizations_one`,
		`INSERT INTO smeldr_organizations (id, slug, created_at, updated_at, name) VALUES ('org-1', 'org-1', datetime('now'), datetime('now'), 'One')`,
		`INSERT INTO smeldr_organizations (id, slug, created_at, updated_at, name) VALUES ('org-2', 'org-2', datetime('now'), datetime('now'), 'Two')`,
		`INSERT INTO smeldr_domains (id, slug, created_at, updated_at, name) VALUES ('dom-2', 'dom-2', datetime('now'), datetime('now'), 'Two')`,
		`INSERT INTO smeldr_areas (id, slug, created_at, updated_at, name) VALUES ('are-2', 'are-2', datetime('now'), datetime('now'), 'Two')`,
	} {
		if _, err := f.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"d-a", "d-d", "d-x", "d-u"} {
		if _, err := f.db.Exec(`INSERT INTO smeldr_decisions (id, slug, status, created_at, updated_at, address) VALUES ($1, $1, 'proposed', datetime('now'), datetime('now'), $2)`, id, "D-"+id); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range [][4]string{
		{"Domain", "dom-1", "Organization", "org-1"}, {"Domain", "dom-2", "Organization", "org-2"},
		{"Area", "are-2", "Domain", "dom-2"},
		{"Decision", "d-a", "Area", "are-1"}, {"Decision", "d-d", "Domain", "dom-1"}, {"Decision", "d-x", "Area", "are-2"},
	} {
		if _, _, err := writeEdgeRow(ctx, f.db, RelationEdge{SourceType: e[0], SourceID: e[1], TargetType: e[2], TargetID: e[3],
			RelationKind: placeKindOf[e[2]], EdgeClass: "asserted", Attributes: []byte("{}")}); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// reads checks what token reads, by the read scope and by Authorized alike,
// and that it holds no write, manage, administer or delete.
func (f approvalFixture) reads(t *testing.T, token string, want map[string]bool) {
	t.Helper()
	ctx := context.Background()
	scope, err := LoadReadScope(ctx, f.rs, User{ID: token})
	if err != nil {
		t.Fatal(err)
	}
	if scope.All() {
		t.Errorf("%s reads everything", token)
	}
	for key, w := range want {
		typ, id, _ := cutPattern(key)
		if got := scope.Readable(typ, id); got != w {
			t.Errorf("%s: scope reads %s = %v, want %v", token, key, got, w)
		}
		if got, err := f.rs.Authorized(ctx, token, "read", AuthTarget{TypeName: typ, ID: id}); err != nil || got != w {
			t.Errorf("%s: Authorized(read, %s) = %v %v, want %v", token, key, got, err, w)
		}
	}
	for _, op := range []string{"update", "manage", "administer", "delete"} {
		if ok, _ := f.rs.Authorized(ctx, token, op, AuthTarget{TypeName: "Decision", ID: "d-a"}); ok {
			t.Errorf("%s gained %s", token, op)
		}
	}
}

func TestApprovalRoleRead_Reach(t *testing.T) {
	f := newApprovalReadFixture(t)
	ctx := context.Background()
	for _, g := range []RoleGrant{
		{TokenID: "dom-appr", RoleName: RoleDomainApprover, ScopeAnchorID: "dom-1"},
		{TokenID: "are-appr", RoleName: RoleAreaApprover, ScopeAnchorID: "are-1"},
		{TokenID: "appr", RoleName: RoleApprover},
		{TokenID: "human-1", RoleName: RoleDomainApprover, ScopeAnchorID: "dom-1"},
	} {
		if _, err := f.rs.Grant(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	// An agent's area-approver, from a human who approves over the Area.
	if _, err := f.rs.Grant(as("human-1", Human), RoleGrant{TokenID: "agent-1", RoleName: RoleAreaApprover, ScopeAnchorID: "are-1"}); err != nil {
		t.Fatal(err)
	}
	if err := f.rs.DefineRole(ctx, RoleDefinition{Name: "chief", Operations: []string{OperationRatify}, ScopeMode: ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rs.Grant(ctx, RoleGrant{TokenID: "chief-1", RoleName: "chief"}); err != nil {
		t.Fatal(err)
	}
	f.reads(t, "dom-appr", map[string]bool{"Domain:dom-1": true, "Area:are-1": true, "Decision:d-a": true, "Decision:d-d": true,
		"Organization:org-1": false, "Domain:dom-2": false, "Decision:d-x": false, "Decision:d-u": false, "Task:t-1": false})
	f.reads(t, "are-appr", map[string]bool{"Area:are-1": true, "Decision:d-a": true,
		"Domain:dom-1": false, "Decision:d-d": false, "Area:are-2": false, "Decision:d-x": false, "Decision:d-u": false})
	f.reads(t, "agent-1", map[string]bool{"Area:are-1": true, "Decision:d-a": true, "Area:are-2": false, "Decision:d-x": false})
	f.reads(t, "appr", map[string]bool{"Organization:org-1": true, "Organization:org-2": true, "Domain:dom-1": true, "Domain:dom-2": true,
		"Area:are-1": true, "Area:are-2": true, "Decision:d-a": true, "Decision:d-d": true, "Decision:d-x": true,
		"Decision:d-u": false, "Task:t-1": false})
	f.reads(t, "chief-1", map[string]bool{"Organization:org-1": false, "Decision:d-a": false})
	// The approval acts are unchanged.
	for _, c := range []struct {
		token, op string
		target    AuthTarget
	}{
		{"dom-appr", OperationRatify, AuthTarget{TypeName: "Decision", ID: "d-d"}},
		{"are-appr", OperationApproveChange, AuthTarget{TypeName: "Decision", ID: "d-a"}},
		{"appr", OperationRatify, AuthTarget{TypeName: "Decision", ID: "d-x"}},
	} {
		if ok, err := f.rs.Authorized(ctx, c.token, c.op, c.target); err != nil || !ok {
			t.Errorf("%s %s over %s = %v %v", c.token, c.op, c.target.ID, ok, err)
		}
	}
}

// A condition placed with its Decision is read by the place's approver.
func TestApprovalRoleRead_ConditionPlaced(t *testing.T) {
	f := newApprovalReadFixture(t)
	ctx := context.Background()
	if _, err := f.rs.Grant(ctx, RoleGrant{TokenID: "dom-appr", RoleName: RoleDomainApprover, ScopeAnchorID: "dom-1"}); err != nil {
		t.Fatal(err)
	}
	f.app.signalResolver().emitContradiction(ctx, RelationEdge{ID: "e", SourceType: "Decision", SourceID: "d-a", TargetType: "Decision", TargetID: "d-d", RelationKind: "contradicts"})
	var id string
	if err := f.db.QueryRow(`SELECT id FROM smeldr_signals WHERE signal_type = 'conflict-detected' AND subject_id = 'd-a'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	scope, err := LoadReadScope(ctx, f.rs, User{ID: "dom-appr"})
	if err != nil || !scope.Readable("Signal", id) {
		t.Errorf("the domain-approver does not read the condition placed in its Domain: %v", err)
	}
}

// The approver's read fails closed: Organizations or a place walk that
// cannot be read give no read, never everything.
func TestApprovalRoleRead_FailsClosed(t *testing.T) {
	f := newApprovalReadFixture(t)
	ctx := context.Background()
	if _, err := f.rs.Grant(ctx, RoleGrant{TokenID: "appr", RoleName: RoleApprover}); err != nil {
		t.Fatal(err)
	}
	for _, match := range []string{"FROM smeldr_organizations", "WHERE target_id = $1 AND relation_kind = $2"} {
		rs := &RoleStore{db: faultDB{DB: f.db, match: match}}
		scope, err := LoadReadScope(ctx, rs, User{ID: "appr"})
		if err != nil || scope.All() || scope.Readable("Decision", "d-a") {
			t.Errorf("%s failing: %v", match, err)
		}
		if ok, err := rs.Authorized(ctx, "appr", "read", AuthTarget{TypeName: "Decision", ID: "d-a"}); ok || err != nil {
			t.Errorf("%s failing: Authorized = %v %v; want false, no error", match, ok, err)
		}
	}
	if ok, _ := f.rs.Authorized(ctx, "appr", "read", AuthTarget{}); ok {
		t.Error("a read with no target authorized")
	}
	if ids, err := organizationIDs(ctx, f.db); err != nil || len(ids) != 2 {
		t.Errorf("organizationIDs = %v %v", ids, err)
	}
}
