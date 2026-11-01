//go:build integration

package pgx

import (
	"context"
	"errors"
	"testing"

	smeldr "smeldr.dev/core"
)

// TestPG_RelationRev: on Postgres a re-assert of a governance relation with a
// stale rev is a RevConflictError (the guarded upsert), and a withdraw with the
// rev it read ends the edge and advances its rev (T1010 part b).
func TestPG_RelationRev(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateOrchestrationTables(db); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.CreateRelationTables(db); err != nil {
		t.Fatal(err)
	}
	rs, err := smeldr.NewRelationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := smeldr.RegisterOrchestrationRelationKinds(ctx, rs); err != nil {
		t.Fatal(err)
	}
	first, _, err := rs.MCPAssertRelationAt(smeldr.TrustedRead(ctx), "Decision", "d-new", "Decision", "d-old", "supersedes", nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("first assert: %v", err)
	}
	stale := first.Rev + 7
	_, _, err = rs.MCPAssertRelationAt(smeldr.TrustedRead(ctx), "Decision", "d-new", "Decision", "d-old", "supersedes", nil, nil, nil, nil, &stale)
	var rc *smeldr.RevConflictError
	if !errors.As(err, &rc) || rc.Current != first.Rev {
		t.Fatalf("a stale re-assert: %v; want a RevConflictError at rev %d", err, first.Rev)
	}
	current := first.Rev
	again, _, err := rs.MCPAssertRelationAt(smeldr.TrustedRead(ctx), "Decision", "d-new", "Decision", "d-old", "supersedes", nil, nil, nil, nil, &current)
	if err != nil || again.Rev != current+1 {
		t.Fatalf("a re-assert with the rev it read: %v, rev %d; want %d", err, again.Rev, current+1)
	}
	if _, err := rs.WithdrawAt(smeldr.TrustedRead(ctx), again.ID, "replaced", &again.Rev); err != nil {
		t.Fatalf("withdraw with the rev it read: %v", err)
	}
}
