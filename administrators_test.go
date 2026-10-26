package smeldr

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// T944 (D154): who administers the instance, for any authenticated caller.

// newAdministratorsFixture is the approvers fixture with administer grants:
// admin to human-3 and two-1 (two live tokens, the newer names it), to gone-1
// (its token revoked) and late-1 (its token expired), an expired admin grant
// to old-1, a revoked one to reader-1, a custom global role with administer
// to agent-x, a static role with administer to human-3, a role whose
// operations do not parse to two-1, and a token whose legacy role is admin
// with no grant.
func newAdministratorsFixture(t *testing.T) areaGateFixture {
	t.Helper()
	f := newApproversFixture(t)
	ctx := context.Background()
	for _, r := range []RoleDefinition{
		{Name: "custodian", Operations: []string{"administer"}},
		{Name: "type-admin", Operations: []string{"administer"}, ScopeMode: ScopeStatic},
	} {
		if err := f.rs.DefineRole(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.db.Exec(`INSERT INTO smeldr_roles (id, name, operations, scope_mode, created_at, updated_at) VALUES ('r-bad', 'broken', '["administer"', 'global', $1, $1)`, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO smeldr_tokens (id, name, role, expires_at, created_at, user_id, actor_class) VALUES ('fp-legacy', 'legacy-admin', 'admin', '2099-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 'legacy-1', 'human')`); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	for _, g := range []RoleGrant{
		{TokenID: "human-3", RoleName: "admin"},
		{TokenID: "two-1", RoleName: "admin"},
		{TokenID: "gone-1", RoleName: "admin"},
		{TokenID: "late-1", RoleName: "admin"},
		{TokenID: "old-1", RoleName: "admin", ExpiresAt: &past},
		{TokenID: "agent-x", RoleName: "custodian"},
		{TokenID: "human-3", RoleName: "type-admin", ScopeStatic: []string{"Decision:*"}},
		{TokenID: "reader-1", RoleName: "type-admin", ScopeStatic: []string{"Decision:*"}},
	} {
		if _, err := f.rs.Grant(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.db.Exec(`INSERT INTO smeldr_role_grants (id, token_id, role_id, scope_static, created_at) VALUES ('g-bad', 'two-1', 'r-bad', '[]', $1)`, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	revoked, err := f.rs.Grant(ctx, RoleGrant{TokenID: "reader-1", RoleName: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.rs.Revoke(ctx, revoked); err != nil {
		t.Fatal(err)
	}
	return f
}

// TestAdministrators: every authenticated caller gets the same names and
// classes; only live global administer grants with a live token count.
func TestAdministrators(t *testing.T) {
	f := newAdministratorsFixture(t)
	want := []InstanceAdministrator{
		{Name: "agent-x", Class: "agent"},
		{Name: "fp-human-3", Class: "human"},
		{Name: "two-new", Class: "human"},
	}
	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{
		{"a domain-reader", as("reader-1")},
		{"an administrator", as("human-3", Human)},
		{"a token with no grant at all", as("nobody-1")},
		{"the legacy admin token", as("legacy-1", Admin)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := f.app.Administrators(tc.ctx)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Errorf("= %+v %v, want %+v", got, err, want)
			}
			if err := f.app.AuthorizeCapability(tc.ctx.(Context), ResourceItem, "administrators", AuthTarget{}); err != nil {
				t.Errorf("capability: %v", err)
			}
		})
	}
	visible, err := f.app.VisibleCapabilities(as("nobody-1").(Context))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range visible {
		found = found || (c.Resource == ResourceItem && c.Verb == "administrators")
	}
	if !found {
		t.Error("administrators is not visible to a token with no grant")
	}
}

// TestAdministrators_Refuses: no caller, no answer; without governance the
// list is empty.
func TestAdministrators_Refuses(t *testing.T) {
	f := newAdministratorsFixture(t)
	for _, ctx := range []context.Context{context.Background(), as("")} {
		if _, err := f.app.Administrators(ctx); !errors.Is(err, ErrForbidden) {
			t.Errorf("no caller: %v", err)
		}
	}
	if err := f.app.AuthorizeCapability(as("").(Context), ResourceItem, "administrators", AuthTarget{}); !errors.Is(err, ErrForbidden) {
		t.Errorf("capability with no caller: %v", err)
	}
	if _, err := (&App{}).Administrators(as("human-3")); !errors.Is(err, ErrInternal) {
		t.Errorf("no database: %v", err)
	}
	if got, err := (&App{cfg: Config{DB: f.db}}).Administrators(as("human-3")); err != nil || len(got) != 0 || got == nil {
		t.Errorf("without governance = %#v %v", got, err)
	}
}

// TestAdministrators_Failures: a failed read is ErrInternal.
func TestAdministrators_Failures(t *testing.T) {
	f := newAdministratorsFixture(t)
	for _, match := range []string{"FROM smeldr_role_grants g JOIN smeldr_roles r", "FROM smeldr_tokens WHERE revoked_at"} {
		app := &App{cfg: Config{DB: &awaitsFailDB{DB: f.db, match: match, err: errors.New("disk")}}, governance: f.app.governance}
		if _, err := app.Administrators(as("human-3")); !errors.Is(err, ErrInternal) {
			t.Errorf("%s fails: %v", match, err)
		}
	}
}
