//go:build integration

package pgx

import (
	"context"
	"fmt"
	"testing"

	smeldr "smeldr.dev/core"
)

// TestPG_ListRelationsScoped: under governance a scoped reader's pages and
// Total cover only the edges whose both ends it may read, paged in memory on
// Postgres the same way as on SQLite.
func TestPG_ListRelationsScoped(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateRelationTables(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS smeldr_tokens (id TEXT NOT NULL PRIMARY KEY, role TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	app := smeldr.New(smeldr.Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	roles := smeldr.NewRoleStore(db)
	if err := app.Governance(roles); err != nil {
		t.Fatal(err)
	}
	rs, err := smeldr.NewRelationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := smeldr.RegisterOrchestrationRelationKinds(ctx, rs); err != nil {
		t.Fatal(err)
	}
	trusted := smeldr.TrustedRead(ctx)
	// Interleaved: a readable Task -> Task edge, then a hidden Task -> Amendment one.
	for i := 0; i < 5; i++ {
		if _, err := rs.MCPAssertRelation(trusted, "Task", fmt.Sprintf("pg-t%d", i), "Task", "pg-root", "depends_on", nil, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
		if i < 3 {
			if _, err := rs.MCPAssertRelation(trusted, "Task", fmt.Sprintf("pg-t%d", i), "Amendment", fmt.Sprintf("pg-am%d", i), "ships_as", nil, nil, nil, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := smeldr.RegisterScopedReaderRole(ctx, roles); err != nil {
		t.Fatal(err)
	}
	// No App resolver for Task here (the type is not registered), so the
	// wildcard form is used.
	if _, err := roles.Grant(ctx, smeldr.RoleGrant{TokenID: "tok", RoleName: "scoped-reader", ScopeStatic: []string{"Task:*"}}); err != nil {
		t.Fatal(err)
	}
	reader := smeldr.NewTestContext(smeldr.User{ID: "tok"})

	first, err := rs.ListRelations(reader, smeldr.RelationListOptions{Limit: 2})
	if err != nil || first.Total != 5 || len(first.Edges) != 2 {
		t.Fatalf("first page = %d edges, total %d, %v; want 2 of 5", len(first.Edges), first.Total, err)
	}
	seen := map[string]bool{}
	for off := 0; off < first.Total; off += 2 {
		p, err := rs.ListRelations(reader, smeldr.RelationListOptions{Limit: 2, Offset: off, AsOf: first.AsOf})
		if err != nil || p.Total != 5 {
			t.Fatalf("offset %d: total %d, %v", off, p.Total, err)
		}
		for _, e := range p.Edges {
			if e.TargetType != "Task" || seen[e.ID] {
				t.Errorf("offset %d: edge %s -> %s %s (repeat %v)", off, e.SourceID, e.TargetType, e.TargetID, seen[e.ID])
			}
			seen[e.ID] = true
		}
	}
	if len(seen) != 5 {
		t.Errorf("pages returned %d readable edges, want 5", len(seen))
	}
	if all, err := rs.ListRelations(trusted, smeldr.RelationListOptions{Limit: 50}); err != nil || all.Total != 8 {
		t.Errorf("trusted total = %d %v, want 8", all.Total, err)
	}
}
