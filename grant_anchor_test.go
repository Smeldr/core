package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// T1213: a dynamic grant's anchor, given as an ID, an address or a slug, is
// resolved on write and stored as the item's ID; one that names nothing is
// refused (grant.anchor_not_found).

// newAnchorFixture is the approval fixture with the Area at address ARE1 and
// slug the-area, the Domain at DOM1 and slug the-domain, the area-reader role,
// and two test kinds: test_watches (Area to Domain) and test_open (no pairs).
func newAnchorFixture(t *testing.T) approvalFixture {
	t.Helper()
	f := newApprovalFixture(t)
	ctx := context.Background()
	for _, q := range []string{
		`UPDATE smeldr_areas SET address = 'ARE1', slug = 'the-area' WHERE id = 'are-1'`,
		`UPDATE smeldr_domains SET address = 'DOM1', slug = 'the-domain' WHERE id = 'dom-1'`,
		`INSERT INTO smeldr_relation_kinds (id, type_name, mode, type_pairs, created_at, updated_at) VALUES ('k1', 'test_watches', 'asserted', '[{"source_type":"Area","target_type":"Domain"}]', datetime('now'), datetime('now'))`,
		`INSERT INTO smeldr_relation_kinds (id, type_name, mode, type_pairs, created_at, updated_at) VALUES ('k2', 'test_open', 'asserted', '[]', datetime('now'), datetime('now'))`,
		`INSERT INTO smeldr_relation_kinds (id, type_name, mode, type_pairs, created_at, updated_at) VALUES ('k3', 'test_either', 'asserted', '[{"source_type":"Task","target_type":"Area"},{"source_type":"Task","target_type":"Domain"}]', datetime('now'), datetime('now'))`,
	} {
		if _, err := f.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := RegisterAreaReaderRole(ctx, f.rs); err != nil {
		t.Fatal(err)
	}
	for _, r := range []RoleDefinition{
		{Name: "domain-watcher", Operations: []string{"read"}, ScopeMode: ScopeDynamic, ScopeRelationKind: "test_watches", ScopeDirection: "incoming"},
		{Name: "open-reader", Operations: []string{"read"}, ScopeMode: ScopeDynamic, ScopeRelationKind: "test_open", ScopeDirection: "incoming"},
		{Name: "either-reader", Operations: []string{"read"}, ScopeMode: ScopeDynamic, ScopeRelationKind: "test_either", ScopeDirection: "incoming"},
	} {
		if err := f.rs.DefineRole(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// grantedAnchor grants role on anchor to token and returns the stored anchor.
func (f approvalFixture) grantedAnchor(t *testing.T, ctx context.Context, token, role, anchor string) (string, error) {
	t.Helper()
	if _, err := f.rs.Grant(ctx, RoleGrant{TokenID: token, RoleName: role, ScopeAnchorID: anchor}); err != nil {
		return "", err
	}
	gs, err := f.rs.ListGrants(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range gs {
		if g.RoleName == role {
			return g.ScopeAnchorID, nil
		}
	}
	t.Fatalf("no %s grant for %s", role, token)
	return "", nil
}

func TestGrantAnchor_Forms(t *testing.T) {
	for _, anchor := range []string{"ARE1", "the-area", "are-1"} {
		t.Run(anchor, func(t *testing.T) {
			f := newAnchorFixture(t)
			got, err := f.grantedAnchor(t, context.Background(), "human-2", "area-reader", anchor)
			if err != nil || got != "are-1" {
				t.Fatalf("stored %q, %v; want are-1", got, err)
			}
			ok, err := f.rs.Authorized(context.Background(), "human-2", "read", AuthTarget{TypeName: "Area", ID: "are-1"})
			if err != nil || !ok {
				t.Errorf("the holder reads the Area: %v %v", ok, err)
			}
		})
	}
	t.Run("a kind's type pairs", func(t *testing.T) {
		f := newAnchorFixture(t)
		if got, err := f.grantedAnchor(t, context.Background(), "human-2", "domain-watcher", "DOM1"); err != nil || got != "dom-1" {
			t.Errorf("stored %q, %v; want dom-1", got, err)
		}
	})
	t.Run("an open kind", func(t *testing.T) {
		f := newAnchorFixture(t)
		if got, err := f.grantedAnchor(t, context.Background(), "human-2", "open-reader", "ARE1"); err != nil || got != "are-1" {
			t.Errorf("an address on an open kind: stored %q, %v; want are-1", got, err)
		}
		if got, err := f.grantedAnchor(t, context.Background(), "plain-1", "open-reader", "item-of-another-type"); err != nil || got != "item-of-another-type" {
			t.Errorf("a non-address on an open kind: stored %q, %v; want it kept as given", got, err)
		}
	})
	t.Run("a role that is not dynamic", func(t *testing.T) {
		f := newAnchorFixture(t)
		if got, err := f.grantedAnchor(t, context.Background(), "human-2", "editor", "ARE1"); err != nil || got != "ARE1" {
			t.Errorf("stored %q, %v; want ARE1 unchanged", got, err)
		}
	})
}

func TestGrantAnchor_Refusals(t *testing.T) {
	f := newAnchorFixture(t)
	if _, err := f.db.Exec(`UPDATE smeldr_domains SET slug = 'shared' WHERE id = 'dom-1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE smeldr_areas SET slug = 'shared' WHERE id = 'are-1'`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, role, anchor string
		typ                ProblemType
	}{
		{"unknown address", "area-reader", "ARE999", problemGrantAnchorNotFound},
		{"unknown slug", "area-reader", "no-such-area", problemGrantAnchorNotFound},
		{"an address of another type", "area-reader", "DOM1", problemGrantAnchorNotFound},
		{"unknown address on an open kind", "open-reader", "ARE999", problemGrantAnchorNotFound},
		{"a slug in two candidate types", "either-reader", "shared", problemIdentAmbiguous},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.rs.Grant(context.Background(), RoleGrant{TokenID: "human-2", RoleName: tc.role, ScopeAnchorID: tc.anchor})
			if ProblemTypeOf(err) != tc.typ {
				t.Fatalf("err = %v (%s), want %s", err, ProblemTypeOf(err), tc.typ)
			}
			gs, _ := f.rs.ListGrants(context.Background(), "human-2")
			for _, g := range gs {
				if g.RoleName == tc.role {
					t.Errorf("a refused grant was stored: %+v", g)
				}
			}
		})
	}
	err := func() error {
		_, err := f.rs.Grant(context.Background(), RoleGrant{TokenID: "human-2", RoleName: "area-reader", ScopeAnchorID: "ARE999"})
		return err
	}()
	if v := ProblemValuesOf(err); v["anchor"] != "ARE999" || v["role"] != "area-reader" || v["types"] != "Area" {
		t.Errorf("values = %v", v)
	}
}

// TestGrantAnchor_AgentAreaGrant: the agent rule sees the resolved Area, so a
// human domain-approver delegates the Area named by its address; an unknown
// Area is refused with the same text as one the grantor holds no authority
// over (T971).
func TestGrantAnchor_AgentAreaGrant(t *testing.T) {
	f := newAnchorFixture(t)
	if _, err := f.rs.Grant(context.Background(), RoleGrant{TokenID: "human-1", RoleName: RoleDomainApprover, ScopeAnchorID: "DOM1"}); err != nil {
		t.Fatal(err)
	}
	human := as("human-1", Human)
	if got, err := f.grantedAnchor(t, human, "agent-1", RoleAreaApprover, "ARE1"); err != nil || got != "are-1" {
		t.Fatalf("stored %q, %v", got, err)
	}
	_, err := f.rs.Grant(human, RoleGrant{TokenID: "job-1", RoleName: RoleAreaApprover, ScopeAnchorID: "ARE999"})
	var re *RefusalError
	if !errors.As(err, &re) || ProblemTypeOf(err) != problemGrantGrantorLacksAreaAuthority {
		t.Errorf("an unknown Area for an agent grant: %v (%s), want the authority refusal", err, ProblemTypeOf(err))
	}
	// The article (T1211 missed these): "a job token", never "an job token".
	_, err = f.rs.Grant(as("plain-1"), RoleGrant{TokenID: "job-1", RoleName: RoleAreaApprover, ScopeAnchorID: "ARE1"})
	if err == nil || !strings.Contains(err.Error(), "a job token's area-approver grant must come from a human token") || strings.Contains(err.Error(), "an job") {
		t.Errorf("job grantee message: %v", err)
	}
	_, err = f.rs.Grant(as("human-2", Human), RoleGrant{TokenID: "job-1", RoleName: RoleAreaApprover, ScopeAnchorID: "ARE1"})
	if err == nil || !strings.Contains(err.Error(), "cannot delegate it to a job token") {
		t.Errorf("job grantee, grantor without authority: %v", err)
	}
}

// TestGrantAnchor_LookupFailures: a read the resolution needs that fails is
// an internal error, never taken as "not found".
func TestGrantAnchor_LookupFailures(t *testing.T) {
	for _, match := range []string{
		"SELECT type_pairs FROM smeldr_relation_kinds",
		"SELECT type_name FROM smeldr_address_counters",
	} {
		t.Run(match, func(t *testing.T) {
			f := newAnchorFixture(t)
			rs := &RoleStore{db: faultDB{DB: f.db, match: match}}
			role, anchor := "domain-watcher", "DOM1"
			if strings.Contains(match, "address_counters") {
				role, anchor = "open-reader", "ARE1"
			}
			_, err := rs.Grant(context.Background(), RoleGrant{TokenID: "human-2", RoleName: role, ScopeAnchorID: anchor})
			if !errors.Is(err, ErrInternal) {
				t.Errorf("err = %v, want ErrInternal", err)
			}
		})
	}
	t.Run("bad type_pairs", func(t *testing.T) {
		f := newAnchorFixture(t)
		if _, err := f.db.Exec(`UPDATE smeldr_relation_kinds SET type_pairs = 'not json' WHERE type_name = 'test_watches'`); err != nil {
			t.Fatal(err)
		}
		_, err := f.rs.Grant(context.Background(), RoleGrant{TokenID: "human-2", RoleName: "domain-watcher", ScopeAnchorID: "DOM1"})
		if !errors.Is(err, ErrInternal) {
			t.Errorf("err = %v, want ErrInternal", err)
		}
	})
}

// TestGrantAnchor_EdgeForms: an address of a prefix nobody registered, on an
// open kind, is kept as given; a kind whose types have no table here keeps
// the anchor as given.
func TestGrantAnchor_EdgeForms(t *testing.T) {
	f := newAnchorFixture(t)
	if got, err := f.grantedAnchor(t, context.Background(), "plain-1", "open-reader", "XYZ12"); err != nil || got != "XYZ12" {
		t.Errorf("an unregistered prefix on an open kind: stored %q, %v; want it kept as given", got, err)
	}
	if _, err := f.db.Exec(`DROP TABLE smeldr_domains`); err != nil {
		t.Fatal(err)
	}
	if got, err := f.grantedAnchor(t, context.Background(), "human-2", "domain-watcher", "DOM1"); err != nil || got != "DOM1" {
		t.Errorf("no Domain table: stored %q, %v; want it kept as given", got, err)
	}
}

// nthFaultDB fails the nth query containing match (counting from 1) and
// passes every other one through.
type nthFaultDB struct {
	DB
	match string
	n     int
	seen  *int
}

func (d nthFaultDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if strings.Contains(q, d.match) {
		*d.seen++
		if *d.seen == d.n {
			return nil, errFault
		}
	}
	return d.DB.QueryContext(ctx, q, args...)
}

// TestGrantAnchor_TableReadsFail: the candidate table's probe and the lookup
// itself failing are internal errors, never "not found" or "kept as given".
func TestGrantAnchor_TableReadsFail(t *testing.T) {
	for _, tc := range []struct {
		name, match string
		n           int
	}{
		{"the table probe", `FROM "smeldr_domains" WHERE 1=0`, 2},
		{"the lookup", `FROM "smeldr_domains" WHERE "`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAnchorFixture(t)
			seen := 0
			rs := &RoleStore{db: nthFaultDB{DB: f.db, match: tc.match, n: tc.n, seen: &seen}}
			_, err := rs.Grant(context.Background(), RoleGrant{TokenID: "human-2", RoleName: "domain-watcher", ScopeAnchorID: "DOM1"})
			if !errors.Is(err, ErrInternal) {
				t.Errorf("err = %v, want ErrInternal (%d matching queries seen)", err, seen)
			}
		})
	}
}
