package smeldr

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// grantFixture is the identity fixture with governance wired and one agent
// identity to grant to.
func grantFixture(t *testing.T) (identityFixture, *RoleStore, *Identity) {
	t.Helper()
	f := newIdentityFixture(t)
	rs := NewRoleStore(f.db)
	if err := f.app.Governance(rs); err != nil {
		t.Fatal(err)
	}
	_, id, err := f.store.CreateClassified(as("h-1", Admin, Human), "bot", "editor", Agent, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return f, rs, f.identity(t, id)
}

func grantsOf(t *testing.T, f identityFixture, holder string) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_role_grants WHERE token_id = $1`, holder).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A grant names the rev of the grantee's identity (T1010 part c): matching
// writes and advances it, stale is a conflict that writes nothing, none warns
// and is refused after the cutover.
func TestRevRequired_GrantNamesIdentityRev(t *testing.T) {
	f, rs, ident := grantFixture(t)
	ctx := context.Background()
	rev := int(ident.Rev)
	if _, warnings, err := rs.GrantAt(ctx, RoleGrant{TokenID: ident.ID, RoleName: "editor"}, &rev); err != nil || len(warnings) != 0 {
		t.Fatalf("a matching rev: %v, %v", warnings, err)
	}
	if got := f.identity(t, ident.ID).Rev; got != ident.Rev+1 {
		t.Errorf("the identity's rev = %d; want %d", got, ident.Rev+1)
	}
	var conflict *RevConflictError
	if _, _, err := rs.GrantAt(ctx, RoleGrant{TokenID: ident.ID, RoleName: "author"}, &rev); !errors.As(err, &conflict) || !errors.Is(err, ErrRevConflict) {
		t.Fatalf("a stale rev: %v; want a RevConflictError", err)
	}
	if n := grantsOf(t, f, ident.ID); n != 1 {
		t.Errorf("a stale grant wrote: %d grants", n)
	}
	_, warnings, err := rs.GrantAt(ctx, RoleGrant{TokenID: ident.ID, RoleName: "author"}, nil)
	if err != nil || len(warnings) != 1 || warnings[0].Code != "rev_missing" || !strings.Contains(warnings[0].Message, ident.Address) {
		t.Errorf("no rev: %v, %v; want a rev_missing warning naming the identity", warnings, err)
	}
	rs.revCutover = time.Now().Add(-time.Hour)
	var ve *ValidationError
	if _, _, err := rs.GrantAt(ctx, RoleGrant{TokenID: ident.ID, RoleName: "admin"}, nil); !errors.As(err, &ve) {
		t.Errorf("no rev after the cutover: %v; want a ValidationError", err)
	}
	rs.revCutover = time.Now().Add(time.Hour)
	if _, warnings, _ := rs.GrantAt(ctx, RoleGrant{TokenID: ident.ID, RoleName: "admin"}, nil); len(warnings) != 1 || !strings.Contains(warnings[0].Message, "refused without one from") {
		t.Errorf("before a set cutover: %v", warnings)
	}
}

// A grantee that is not an identity names no rev.
func TestRevRequired_GrantWithoutIdentity(t *testing.T) {
	f, rs, _ := grantFixture(t)
	ctx := context.Background()
	_, plain, err := f.store.CreateWithID(ctx, "plain", "editor", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, warnings, err := rs.GrantAt(ctx, RoleGrant{TokenID: plain, RoleName: "editor"}, nil); err != nil || len(warnings) != 0 {
		t.Errorf("no identity, no rev: %v, %v; want no warning", warnings, err)
	}
	rev := 0
	var ve *ValidationError
	if _, _, err := rs.GrantAt(ctx, RoleGrant{TokenID: plain, RoleName: "author"}, &rev); !errors.As(err, &ve) {
		t.Errorf("a rev for no identity: %v; want a ValidationError", err)
	}
	if _, err := rs.RevokeWithReasonAt(ctx, "no-such-grant", "", nil); err != nil {
		t.Errorf("revoke of an unknown grant: %v", err)
	}
	if _, _, err := (&RoleStore{db: identityFaultDB{f.db, "WHERE id IN ("}}).granteeRevRule(ctx, plain, "grant_role", nil); err == nil {
		t.Error("a failed identity read: want an error")
	}
	// The fixture has one connection: the transaction comes last.
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if ruled, _, err := NewRoleStore(tx).granteeRevRule(ctx, plain, "grant_role", nil); ruled || err != nil {
		t.Errorf("in a transaction: %v, %v; want nothing ruled", ruled, err)
	}
}

// A revoke names the rev of the identity holding the grant.
func TestRevRequired_RevokeNamesIdentityRev(t *testing.T) {
	f, rs, ident := grantFixture(t)
	ctx := context.Background()
	rev := int(ident.Rev)
	grantID, _, err := rs.GrantAt(ctx, RoleGrant{TokenID: ident.ID, RoleName: "editor"}, &rev)
	if err != nil {
		t.Fatal(err)
	}
	var conflict *RevConflictError
	if _, err := rs.RevokeWithReasonAt(ctx, grantID, "", &rev); !errors.As(err, &conflict) {
		t.Fatalf("a stale revoke: %v; want a RevConflictError", err)
	}
	if n := grantsOf(t, f, ident.ID); n != 1 {
		t.Errorf("a stale revoke deleted the grant")
	}
	current := int(f.identity(t, ident.ID).Rev)
	if warnings, err := rs.RevokeWithReasonAt(ctx, grantID, "done", &current); err != nil || len(warnings) != 0 {
		t.Fatalf("a matching revoke: %v, %v", warnings, err)
	}
	if n := grantsOf(t, f, ident.ID); n != 0 {
		t.Errorf("the grant was not revoked")
	}
	if got := int(f.identity(t, ident.ID).Rev); got != current+1 {
		t.Errorf("the identity's rev after revoke = %d; want %d", got, current+1)
	}
	second, err := rs.Grant(ctx, RoleGrant{TokenID: ident.ID, RoleName: "author"})
	if err != nil {
		t.Fatal(err)
	}
	if warnings, err := rs.RevokeWithReasonAt(ctx, second, "", nil); err != nil || len(warnings) != 1 {
		t.Errorf("a revoke without a rev: %v, %v; want a warning", warnings, err)
	}
	if err := rs.Revoke(ctx, "no-such-grant"); err != nil {
		t.Errorf("Revoke of an unknown grant: %v", err)
	}
	if _, err := (&RoleStore{db: identityFaultDB{f.db, "SELECT token_id FROM smeldr_role_grants WHERE id"}}).RevokeWithReasonAt(ctx, "g", "", nil); err == nil {
		t.Error("a failed grant read: want an error")
	}
}
