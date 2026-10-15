//go:build integration

package pgx

import (
	"context"
	"fmt"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// TestPG_ListRelations: paging, a kind filter and the as_of cut on Postgres.
func TestPG_ListRelations(t *testing.T) {
	db, _ := isolatedDB(t)
	// A bare store on a handle no App registered: under governance elsewhere in
	// the process it fails closed, so the test reads as trusted.
	ctx := smeldr.TrustedRead(context.Background())
	if err := smeldr.CreateRelationTables(db); err != nil {
		t.Fatalf("CreateRelationTables: %v", err)
	}
	rs, err := smeldr.NewRelationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := smeldr.RegisterOrchestrationRelationKinds(ctx, rs); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := 0; i < 5; i++ {
		e, err := rs.MCPAssertRelation(ctx, "Task", fmt.Sprintf("pg-t%d", i), "Task", "pg-root", "depends_on", nil, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, e.ID)
	}
	if _, err := rs.MCPAssertRelation(ctx, "Task", "pg-t0", "Amendment", "pg-am", "ships_as", nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	first, err := rs.ListRelations(ctx, smeldr.RelationListOptions{Kind: "depends_on", Limit: 2})
	if err != nil || first.Total != 5 || len(first.Edges) != 2 {
		t.Fatalf("first page = %d edges, total %d, %v", len(first.Edges), first.Total, err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := rs.MCPAssertRelation(ctx, "Task", "pg-late", "Task", "pg-root", "depends_on", nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := rs.Withdraw(ctx, ids[4], "between pages"); err != nil {
		t.Fatal(err)
	}
	seen := len(first.Edges)
	for off := 2; off < first.Total; off += 2 {
		p, err := rs.ListRelations(ctx, smeldr.RelationListOptions{Kind: "depends_on", Limit: 2, Offset: off, AsOf: first.AsOf})
		if err != nil || p.Total != 5 {
			t.Fatalf("offset %d: total %d, %v", off, p.Total, err)
		}
		seen += len(p.Edges)
	}
	if seen != 5 {
		t.Errorf("pages at as_of returned %d edges, want 5", seen)
	}
	if fresh, _ := rs.ListRelations(ctx, smeldr.RelationListOptions{Kind: "depends_on", Limit: 50}); fresh.Total != 5 {
		t.Errorf("fresh total = %d, want 5 (one added, one ended)", fresh.Total)
	}
	if all, _ := rs.ListRelations(ctx, smeldr.RelationListOptions{IncludeEnded: true, Limit: 50}); all.Total != 7 {
		t.Errorf("with ended = %d, want 7", all.Total)
	}
}
