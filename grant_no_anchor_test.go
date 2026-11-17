package smeldr

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// T1248 question 1: what a dynamic grant stored with no anchor reaches. The
// rows are written directly, as an older Grant stored them, so the test holds
// whatever Grant does on write.

// insertAnchorless stores a grant of role to token with scope_anchor_id NULL
// (empty false) or an empty string (empty true).
func (f approvalFixture) insertAnchorless(t *testing.T, id, token, role string, empty bool) {
	t.Helper()
	var roleID string
	if err := f.db.QueryRow(`SELECT id FROM smeldr_roles WHERE name = $1`, role).Scan(&roleID); err != nil {
		t.Fatal(err)
	}
	var anchor any
	if empty {
		anchor = ""
	}
	if _, err := f.db.Exec(`INSERT INTO smeldr_role_grants (id, token_id, role_id, scope_static, scope_anchor_id, created_at) VALUES ($1, $2, $3, '[]', $4, datetime('now'))`,
		id, token, roleID, anchor); err != nil {
		t.Fatal(err)
	}
}

func TestGrantNoAnchor_ReachesNothing(t *testing.T) {
	f := newAnchorFixture(t)
	ctx := context.Background()
	// human-2: area-reader with NULL, domain-approver with '', area-approver with NULL.
	f.insertAnchorless(t, "g-read", "human-2", "area-reader", false)
	f.insertAnchorless(t, "g-dom", "human-2", RoleDomainApprover, true)
	f.insertAnchorless(t, "g-area", "human-2", RoleAreaApprover, false)

	area := AuthTarget{TypeName: "Area", ID: f.area}
	domain := AuthTarget{TypeName: "Domain", ID: f.domain}
	for _, tc := range []struct {
		op     string
		target AuthTarget
		anchor bool
	}{
		{"read", area, true}, {"read", domain, true},
		{OperationRatify, domain, true}, {OperationRatify, area, false},
		{OperationApproveChange, area, false}, {OperationApproveChange, domain, true},
	} {
		ok, err := f.rs.authorizedOver(ctx, "human-2", tc.op, tc.target, tc.anchor)
		if err != nil || ok {
			t.Errorf("%s over %s (anchor counts %v) = %v, %v; want false", tc.op, tc.target.TypeName, tc.anchor, ok, err)
		}
	}
	for _, role := range []string{"area-reader", RoleDomainApprover, RoleAreaApprover} {
		for _, target := range []AuthTarget{area, domain} {
			if ok, err := f.rs.RoleGranted(ctx, "human-2", role, target); err != nil || ok {
				t.Errorf("RoleGranted %s over %s = %v, %v; want false", role, target.TypeName, ok, err)
			}
		}
	}
	scope, err := LoadReadScope(ctx, f.rs, User{ID: "human-2"})
	if err != nil || scope.All() || scope.readsAny() {
		t.Errorf("read scope: all %v, reads any %v, %v; want nothing", scope.All(), scope.readsAny(), err)
	}
	// human-1 holds domain-reader on DOM1, so it reads the place and its Area.
	if err := RegisterDomainReaderRole(ctx, f.rs); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rs.Grant(ctx, RoleGrant{TokenID: "human-1", RoleName: "domain-reader", ScopeAnchorID: "DOM1"}); err != nil {
		t.Fatal(err)
	}
	for _, place := range []string{"DOM1", "ARE1"} {
		got, err := f.app.ApproversOf(as("human-1", Human), place)
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range got {
			if a.Name == "fp-human-2" {
				t.Errorf("ApproversOf(%s) lists the anchorless holder: %+v", place, a)
			}
		}
	}

	// What the operations alone still do: a capability is visible when any
	// grant holds its operation, and a Signal addressed to an operation's name
	// is routed to the holder. Both are the same for a grant anchored on a
	// place with nothing in it; delivery of an event still needs its subject
	// to be readable (eventReadable).
	ops, err := f.rs.grantedOperations(ctx, "human-2")
	if err != nil || !ops["read"] || !ops[OperationRatify] || !ops[OperationApproveChange] {
		t.Errorf("granted operations = %v, %v", ops, err)
	}
	names, err := f.app.routingNames(ctx, "human-2", nil)
	if err != nil || !names[OperationRatify] || !names[OperationApproveChange] {
		t.Errorf("routing names = %v, %v", names, err)
	}
}

// TestGrantAnchor_Required: a dynamic role with no anchor is refused on
// write and nothing is stored; the agent rule still answers first for an
// agent; a static or global role needs no anchor.
func TestGrantAnchor_Required(t *testing.T) {
	f := newAnchorFixture(t)
	ctx := context.Background()
	for _, tc := range []struct{ role, anchor string }{
		{"area-reader", ""},
		{"area-reader", "  "},
		{RoleDomainApprover, ""},
		{"open-reader", ""},
	} {
		t.Run(tc.role+"/"+tc.anchor, func(t *testing.T) {
			_, err := f.rs.Grant(ctx, RoleGrant{TokenID: "human-2", RoleName: tc.role, ScopeAnchorID: tc.anchor})
			var ve *ValidationError
			if ProblemTypeOf(err) != problemGrantAnchorRequired || !errors.As(err, &ve) {
				t.Fatalf("err = %v (%s), want %s", err, ProblemTypeOf(err), problemGrantAnchorRequired)
			}
			if v := ProblemValuesOf(err); v["role"] != tc.role {
				t.Errorf("values = %v", v)
			}
			if !strings.Contains(err.Error(), "scope_anchor_id") {
				t.Errorf("the message does not name the field: %v", err)
			}
			gs, _ := f.rs.ListGrants(ctx, "human-2")
			for _, g := range gs {
				if g.RoleName == tc.role {
					t.Errorf("a refused grant was stored: %+v", g)
				}
			}
		})
	}
	_, err := f.rs.Grant(as("human-1", Human), RoleGrant{TokenID: "agent-1", RoleName: RoleAreaApprover})
	if ProblemTypeOf(err) != problemGrantAutomatedApproveChangeNotArea {
		t.Errorf("an agent's area-approver with no anchor: %v (%s), want the agent rule", err, ProblemTypeOf(err))
	}
	if err := f.rs.DefineRole(ctx, RoleDefinition{Name: "one-area", Operations: []string{"read"}, ScopeMode: ScopeStatic}); err != nil {
		t.Fatal(err)
	}
	for _, g := range []RoleGrant{
		{TokenID: "human-2", RoleName: "editor"},
		{TokenID: "human-2", RoleName: "one-area", ScopeStatic: []string{"Area:are-1"}},
	} {
		if _, err := f.rs.Grant(ctx, g); err != nil {
			t.Errorf("%s with no anchor: %v", g.RoleName, err)
		}
	}
}

// TestReportAnchorlessDynamicGrants: the boot check names stored anchorless
// dynamic grants by ID, keeps them, says nothing when there are none, and a
// failed read is a warning only.
func TestReportAnchorlessDynamicGrants(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)
	ctx := context.Background()

	f := newAnchorFixture(t)
	reportAnchorlessDynamicGrants(ctx, f.db)
	if strings.Contains(buf.String(), "no anchor") {
		t.Errorf("no anchorless grant, yet: %s", buf.String())
	}
	f.insertAnchorless(t, "g-x", "human-2", "area-reader", false)
	f.insertAnchorless(t, "g-y", "human-2", RoleDomainApprover, true)
	if _, err := f.rs.Grant(ctx, RoleGrant{TokenID: "human-2", RoleName: "area-reader", ScopeAnchorID: "ARE1"}); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	reportAnchorlessDynamicGrants(ctx, f.db)
	out := buf.String()
	if !strings.Contains(out, "dynamic grants with no anchor authorize nothing") || !strings.Contains(out, "count=2") || !strings.Contains(out, "g-x") || !strings.Contains(out, "g-y") {
		t.Errorf("log = %s", out)
	}
	if strings.Contains(out, "human-2") {
		t.Errorf("the log names a token: %s", out)
	}
	var n int
	if err := f.db.QueryRow(`SELECT count(*) FROM smeldr_role_grants WHERE id IN ('g-x', 'g-y')`).Scan(&n); err != nil || n != 2 {
		t.Errorf("stored anchorless grants left: %d, %v; want 2", n, err)
	}

	buf.Reset()
	reportAnchorlessDynamicGrants(ctx, faultDB{DB: f.db, match: "scope_mode = 'dynamic'"})
	if !strings.Contains(buf.String(), "anchorless dynamic grant check skipped") {
		t.Errorf("a failed read: %s", buf.String())
	}
}
