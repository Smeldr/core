//go:build integration

package pgx

import (
	"context"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// The read scope on Postgres: static and dynamic grants give the same answers
// as Authorized(read), and the reader roles read what they name.
func TestPG_ReadScope(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateRelationTables(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS smeldr_tokens (id TEXT NOT NULL PRIMARY KEY, role TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	app := smeldr.New(smeldr.Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	rs := smeldr.NewRoleStore(db)
	if err := app.Governance(rs); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, e := range []struct {
		srcType, src, tgtType, tgt, class string
		invalid                           any
	}{
		{"Decision", "a1", "Domain", "A", "asserted", nil},
		{"Decision", "a3", "Domain", "A", "asserted", now.Add(-time.Hour)},
		{"Decision", "a4", "Domain", "A", "inferred", nil},
	} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO smeldr_relations (id, source_type, source_id, target_type, target_id, relation_kind, edge_class, invalid_at, attributes, created_at, updated_at)
			 VALUES ($1, $2, $3, $4, $5, 'belongs_to_domain', $6, $7, '{}', $8, $8)`,
			smeldr.NewID(), e.srcType, e.src, e.tgtType, e.tgt, e.class, e.invalid, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := smeldr.RegisterScopedReaderRole(ctx, rs); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.RegisterDomainReaderRole(ctx, rs); err != nil {
		t.Fatal(err)
	}
	// No App resolver for Goal here (the type is not registered), so the
	// wildcard form is used.
	if _, err := rs.Grant(ctx, smeldr.RoleGrant{TokenID: "tok", RoleName: "scoped-reader", ScopeStatic: []string{"Goal:*"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.Grant(ctx, smeldr.RoleGrant{TokenID: "tok", RoleName: "domain-reader", ScopeAnchorID: "A"}); err != nil {
		t.Fatal(err)
	}
	s, err := app.ReadScope(smeldr.NewTestContext(smeldr.User{ID: "tok"}))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range [][2]string{{"Decision", "a1"}, {"Decision", "a3"}, {"Decision", "a4"}, {"Domain", "A"}, {"Goal", "g1"}, {"Task", "t1"}} {
		want, err := rs.Authorized(ctx, "tok", "read", smeldr.AuthTarget{TypeName: c[0], ID: c[1]})
		if err != nil {
			t.Fatal(err)
		}
		if got := s.Readable(c[0], c[1]); got != want {
			t.Errorf("Readable(%s, %s) = %v; Authorized says %v", c[0], c[1], got, want)
		}
	}
	// A place reader reads its place too (D142, T882).
	if !s.Readable("Decision", "a1") || !s.Readable("Domain", "A") || s.All() {
		t.Errorf("unexpected scope: a1 %v, Domain %v, All %v", s.Readable("Decision", "a1"), s.Readable("Domain", "A"), s.All())
	}
}
