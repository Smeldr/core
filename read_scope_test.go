package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

// queryHookDB records every QueryContext statement and can replace or fail
// the first one matching a substring.
type queryHookDB struct {
	DB
	queries []string
	match   string
	fail    bool   // return an error for the match
	replace string // run this statement instead of the match
}

func (d *queryHookDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	d.queries = append(d.queries, q)
	if d.match != "" && strings.Contains(q, d.match) {
		if d.fail {
			return nil, errors.New("injected failure")
		}
		if d.replace != "" {
			return d.DB.QueryContext(ctx, d.replace)
		}
	}
	return d.DB.QueryContext(ctx, q, args...)
}

// readScopeFixture is a governance DB with relations: anchor A has items a1
// (incoming belongs_to_domain), a2 (outgoing contains), a3 (an invalidated
// edge) and a4 (an inferred edge).
type readScopeFixture struct {
	db     *sql.DB
	rs     *RoleStore
	anchor string
}

func newReadScopeFixture(t *testing.T) readScopeFixture {
	t.Helper()
	db := setupGovernanceDB(t)
	setupRelationsTable(t, db)
	ctx := context.Background()
	f := readScopeFixture{db: db, rs: NewRoleStore(db), anchor: "A"}
	edge := func(srcType, src, tgtType, tgt, kind, class string, invalid any) {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO smeldr_relations (id, source_type, source_id, target_type, target_id, relation_kind, edge_class, invalid_at, attributes, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, '{}', datetime('now'), datetime('now'))`,
			NewID(), srcType, src, tgtType, tgt, kind, class, invalid); err != nil {
			t.Fatalf("insert edge: %v", err)
		}
	}
	edge("Decision", "a1", "Domain", "A", "belongs_to_domain", "asserted", nil)
	edge("Domain", "A", "Task", "a2", "contains", "asserted", nil)
	edge("Decision", "a3", "Domain", "A", "belongs_to_domain", "asserted", time.Now().UTC().Add(-time.Hour))
	edge("Decision", "a4", "Domain", "A", "belongs_to_domain", "inferred", nil)
	return f
}

func (f readScopeFixture) role(t *testing.T, def RoleDefinition) {
	t.Helper()
	if err := f.rs.DefineRole(context.Background(), def); err != nil {
		t.Fatalf("DefineRole %s: %v", def.Name, err)
	}
}

func (f readScopeFixture) grant(t *testing.T, g RoleGrant) {
	t.Helper()
	if _, err := f.rs.Grant(context.Background(), g); err != nil {
		t.Fatalf("Grant %s: %v", g.RoleName, err)
	}
}

// TestReadScope_EquivalentToAuthorized: for every grant shape, Readable equals
// Authorized(read) on every candidate item.
func TestReadScope_EquivalentToAuthorized(t *testing.T) {
	past := time.Now().UTC().Add(-time.Hour)
	cases := []struct {
		name  string
		roles []RoleDefinition
		grant []RoleGrant
	}{
		{"global", []RoleDefinition{{Name: "g", Operations: []string{"read"}, ScopeMode: ScopeGlobal}},
			[]RoleGrant{{RoleName: "g"}}},
		{"static exact", []RoleDefinition{{Name: "s", Operations: []string{"read"}, ScopeMode: ScopeStatic}},
			[]RoleGrant{{RoleName: "s", ScopeStatic: []string{"Decision:d1"}}}},
		{"static wildcard", []RoleDefinition{{Name: "s", Operations: []string{"read"}, ScopeMode: ScopeStatic}},
			[]RoleGrant{{RoleName: "s", ScopeStatic: []string{"Task:*"}}}},
		{"dynamic incoming", []RoleDefinition{{Name: "d", Operations: []string{"read"}, ScopeMode: ScopeDynamic, ScopeRelationKind: "belongs_to_domain", ScopeDirection: "incoming"}},
			[]RoleGrant{{RoleName: "d", ScopeAnchorID: "A"}}},
		{"dynamic outgoing", []RoleDefinition{{Name: "d", Operations: []string{"read"}, ScopeMode: ScopeDynamic, ScopeRelationKind: "contains", ScopeDirection: "outgoing"}},
			[]RoleGrant{{RoleName: "d", ScopeAnchorID: "A"}}},
		{"dynamic both", []RoleDefinition{{Name: "d", Operations: []string{"read"}, ScopeMode: ScopeDynamic, ScopeRelationKind: "belongs_to_domain", ScopeDirection: "both"}},
			[]RoleGrant{{RoleName: "d", ScopeAnchorID: "A"}}},
		{"expired", []RoleDefinition{{Name: "g", Operations: []string{"read"}, ScopeMode: ScopeGlobal}},
			[]RoleGrant{{RoleName: "g", ExpiresAt: &past}}},
		{"no read op", []RoleDefinition{{Name: "w", Operations: []string{"update"}, ScopeMode: ScopeGlobal}},
			[]RoleGrant{{RoleName: "w"}}},
		{"dynamic without anchor", []RoleDefinition{{Name: "d", Operations: []string{"read"}, ScopeMode: ScopeDynamic, ScopeRelationKind: "belongs_to_domain", ScopeDirection: "incoming"}},
			[]RoleGrant{{RoleName: "d"}}},
	}
	candidates := [][2]string{
		{"Decision", "d1"}, {"Decision", "d2"}, {"Task", "t1"}, {"Task", "a2"},
		{"Decision", "a1"}, {"Decision", "a3"}, {"Decision", "a4"}, {"Domain", "A"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newReadScopeFixture(t)
			for _, r := range c.roles {
				f.role(t, r)
			}
			for _, g := range c.grant {
				g.TokenID = "tok"
				f.grant(t, g)
			}
			s, err := LoadReadScope(context.Background(), f.rs, User{ID: "tok"})
			if err != nil {
				t.Fatalf("LoadReadScope: %v", err)
			}
			for _, cand := range candidates {
				want, err := f.rs.Authorized(context.Background(), "tok", "read", AuthTarget{TypeName: cand[0], ID: cand[1]})
				if err != nil {
					t.Fatalf("Authorized: %v", err)
				}
				if got := s.Readable(cand[0], cand[1]); got != want {
					t.Errorf("Readable(%s, %s) = %v; Authorized says %v", cand[0], cand[1], got, want)
				}
			}
		})
	}
}

func TestReadScope_MalformedJSONSkipped(t *testing.T) {
	f := newReadScopeFixture(t)
	f.role(t, RoleDefinition{Name: "s", Operations: []string{"read"}, ScopeMode: ScopeStatic})
	f.grant(t, RoleGrant{TokenID: "tok", RoleName: "s", ScopeStatic: []string{"Decision:d1"}})
	ctx := context.Background()
	if _, err := f.db.ExecContext(ctx, `UPDATE smeldr_role_grants SET scope_static = 'not json'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `INSERT INTO smeldr_roles (id, name, operations, scope_mode, created_at, updated_at) VALUES ('rbad', 'bad', 'not json', 'global', datetime('now'), datetime('now'))`); err != nil {
		t.Skipf("roles schema differs: %v", err)
	}
	s, err := LoadReadScope(ctx, f.rs, User{ID: "tok"})
	if err != nil || s.Readable("Decision", "d1") || s.All() {
		t.Errorf("malformed JSON: scope %+v err %v; want an empty scope", s, err)
	}
}

func TestReadScope_TypeReadable(t *testing.T) {
	f := newReadScopeFixture(t)
	f.role(t, RoleDefinition{Name: "s", Operations: []string{"read"}, ScopeMode: ScopeStatic})
	f.role(t, RoleDefinition{Name: "d", Operations: []string{"read"}, ScopeMode: ScopeDynamic, ScopeRelationKind: "belongs_to_domain", ScopeDirection: "incoming"})
	f.grant(t, RoleGrant{TokenID: "tok", RoleName: "s", ScopeStatic: []string{"Goal:g1", "Run:*"}})
	f.grant(t, RoleGrant{TokenID: "tok", RoleName: "d", ScopeAnchorID: "A"})
	s, err := LoadReadScope(context.Background(), f.rs, User{ID: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	for typeName, want := range map[string]bool{"Goal": true, "Run": true, "Decision": true, "Task": false, "Domain": false} {
		if got := s.TypeReadable(typeName); got != want {
			t.Errorf("TypeReadable(%s) = %v; want %v", typeName, got, want)
		}
	}
	if s.All() {
		t.Error("a scoped reader reads All")
	}
}

func TestReadScope_LegacyGuestAndNil(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		user User
		all  bool
	}{
		{User{ID: "a", Roles: []Role{Author}}, true},
		{User{ID: "e", Roles: []Role{Editor}}, true},
		{GuestUser, false},
	} {
		s, err := LoadReadScope(ctx, nil, c.user)
		if err != nil || s.All() != c.all || s.Readable("Task", "x") != c.all {
			t.Errorf("legacy %v: All %v err %v; want %v", c.user.Roles, s.All(), err, c.all)
		}
	}
	f := newReadScopeFixture(t)
	if s, err := LoadReadScope(ctx, f.rs, GuestUser); err != nil || s.TypeReadable("Task") {
		t.Errorf("no user ID: %v %v; want an empty scope", s, err)
	}
	var none *ReadScope
	if none.All() || none.Readable("Task", "x") || none.TypeReadable("Task") {
		t.Error("a nil scope reads something")
	}
}

func TestReadScope_GlobalIsOneQuery(t *testing.T) {
	f := newReadScopeFixture(t)
	f.role(t, RoleDefinition{Name: "g", Operations: []string{"read"}, ScopeMode: ScopeGlobal})
	f.role(t, RoleDefinition{Name: "d", Operations: []string{"read"}, ScopeMode: ScopeDynamic, ScopeRelationKind: "belongs_to_domain", ScopeDirection: "incoming"})
	f.grant(t, RoleGrant{TokenID: "tok", RoleName: "g"})
	f.grant(t, RoleGrant{TokenID: "tok", RoleName: "d", ScopeAnchorID: "A"})
	hook := &queryHookDB{DB: f.db}
	s, err := LoadReadScope(context.Background(), &RoleStore{db: hook}, User{ID: "tok"})
	if err != nil || !s.All() {
		t.Fatalf("global: %v %v", s, err)
	}
	if len(hook.queries) != 1 {
		t.Errorf("a global reader issued %d queries; want 1: %v", len(hook.queries), hook.queries)
	}
}

func TestReadScope_LoadFailuresAreInternal(t *testing.T) {
	cases := []struct {
		name string
		hook queryHookDB
	}{
		{"grants query", queryHookDB{match: "smeldr_role_grants", fail: true}},
		{"grant scan", queryHookDB{match: "smeldr_role_grants", replace: "SELECT 1"}},
		{"anchor query", queryHookDB{match: "FROM smeldr_relations", fail: true}},
		{"anchor scan", queryHookDB{match: "FROM smeldr_relations", replace: "SELECT 1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newReadScopeFixture(t)
			f.role(t, RoleDefinition{Name: "d", Operations: []string{"read"}, ScopeMode: ScopeDynamic, ScopeRelationKind: "belongs_to_domain", ScopeDirection: "both"})
			f.grant(t, RoleGrant{TokenID: "tok", RoleName: "d", ScopeAnchorID: "A"})
			hook := c.hook
			hook.DB = f.db
			s, err := LoadReadScope(context.Background(), &RoleStore{db: &hook}, User{ID: "tok"})
			if !errors.Is(err, ErrInternal) || s != nil {
				t.Errorf("scope %v err %v; want ErrInternal and no scope", s, err)
			}
		})
	}
}

func TestReaderRoles(t *testing.T) {
	f := newReadScopeFixture(t)
	ctx := context.Background()
	for range 2 {
		if err := RegisterScopedReaderRole(ctx, f.rs); err != nil {
			t.Fatalf("RegisterScopedReaderRole: %v", err)
		}
		if err := RegisterDomainReaderRole(ctx, f.rs); err != nil {
			t.Fatalf("RegisterDomainReaderRole: %v", err)
		}
	}
	f.grant(t, RoleGrant{TokenID: "tok", RoleName: "scoped-reader", ScopeStatic: []string{"Goal:g1"}})
	f.grant(t, RoleGrant{TokenID: "tok", RoleName: "domain-reader", ScopeAnchorID: "A"})
	s, err := LoadReadScope(ctx, f.rs, User{ID: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	if !s.Readable("Goal", "g1") || !s.Readable("Decision", "a1") || s.Readable("Domain", "A") || s.Readable("Task", "a2") {
		t.Errorf("reader roles read the wrong items: %+v", s)
	}
	for _, op := range []string{"update", "approve", "manage"} {
		if ok, _ := f.rs.Authorized(ctx, "tok", op, AuthTarget{TypeName: "Goal", ID: "g1"}); ok {
			t.Errorf("scoped-reader grants %q", op)
		}
	}
}

func BenchmarkReadScope(b *testing.B) {
	for _, n := range []int{1000, 50000} {
		b.Run(itoa(n)+"items", func(b *testing.B) {
			db := newSQLiteDB(b)
			setupTokensTable(b, db)
			if err := migrateGovernance(context.Background(), db); err != nil {
				b.Fatal(err)
			}
			setupRelationsTable(b, db)
			ctx := context.Background()
			tx, _ := db.BeginTx(ctx, nil)
			for i := range n {
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO smeldr_relations (id, source_type, source_id, target_type, target_id, relation_kind, edge_class, attributes, created_at, updated_at)
					 VALUES (?, 'Decision', ?, 'Domain', 'A', 'belongs_to_domain', 'asserted', '{}', datetime('now'), datetime('now'))`,
					NewID(), "d"+itoa(i)); err != nil {
					b.Fatal(err)
				}
			}
			if err := tx.Commit(); err != nil {
				b.Fatal(err)
			}
			rs := NewRoleStore(db)
			_ = RegisterScopedReaderRole(ctx, rs)
			_ = RegisterDomainReaderRole(ctx, rs)
			_, _ = rs.Grant(ctx, RoleGrant{TokenID: "tok", RoleName: "scoped-reader", ScopeStatic: []string{"Goal:g1"}})
			_, _ = rs.Grant(ctx, RoleGrant{TokenID: "tok", RoleName: "domain-reader", ScopeAnchorID: "A"})
			ids := make([]string, 10000)
			for i := range ids {
				ids[i] = "d" + itoa(i%n)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				s, err := LoadReadScope(ctx, rs, User{ID: "tok"})
				if err != nil {
					b.Fatal(err)
				}
				for _, id := range ids {
					_ = s.Readable("Decision", id)
				}
			}
		})
	}
}

func itoa(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{digits[i%10]}, b...)
	}
	return string(b)
}
