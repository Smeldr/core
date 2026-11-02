//go:build integration

package pgx

import (
	"context"
	"errors"
	"testing"

	smeldr "smeldr.dev/core"
)

// TestPG_RelationRefusalTypes: on Postgres the relation store's refusals carry
// their types and values through the pgx error path, with the old sentinel and
// text (T1061 phase 2).
func TestPG_RelationRefusalTypes(t *testing.T) {
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

	_, err = rs.MCPAssertRelation(smeldr.TrustedRead(ctx), "Decision", "a", "Decision", "b", "no_such_kind", nil, nil, nil, nil)
	if smeldr.ProblemTypeOf(err) != "relation.kind_unknown" || !errors.Is(err, smeldr.ErrNotFound) || err.Error() != smeldr.ErrNotFound.Error() {
		t.Errorf("an unknown kind: %v (%q); want relation.kind_unknown, ErrNotFound, its text", err, smeldr.ProblemTypeOf(err))
	}

	err = rs.Withdraw(smeldr.TrustedRead(ctx), "no-such-relation", "gone")
	if smeldr.ProblemTypeOf(err) != "relation.not_found" || !errors.Is(err, smeldr.ErrNotFound) ||
		smeldr.ProblemValuesOf(err)["relation"] != "no-such-relation" {
		t.Errorf("withdrawing a missing relation: %v; want relation.not_found naming it", err)
	}

	edge, err := rs.MCPAssertRelation(smeldr.TrustedRead(ctx), "Decision", "a", "Decision", "b", "grounded_in", nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := rs.Withdraw(smeldr.TrustedRead(ctx), edge.ID, "replaced"); err != nil {
		t.Fatal(err)
	}
	err = rs.Withdraw(smeldr.TrustedRead(ctx), edge.ID, "again")
	if smeldr.ProblemTypeOf(err) != "relation.already_withdrawn" || !errors.Is(err, smeldr.ErrConflict) ||
		smeldr.ProblemValuesOf(err)["relation"] != edge.ID || smeldr.ProblemValuesOf(err)["ended_at"] == "" {
		t.Errorf("withdrawing an ended relation: %v %v; want relation.already_withdrawn with its end", err, smeldr.ProblemValuesOf(err))
	}
}

// TestPG_ContentRefusalTypes: on Postgres a dynamic type's refusals and the
// schema store's miss carry their types through the pgx path (T1061 phase 3).
func TestPG_ContentRefusalTypes(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	_, repo := dynamicApp(t, db, nil)

	_, err := repo.GetByID(ctx, "no-such-memo")
	if smeldr.ProblemTypeOf(err) != "item.not_found" || !errors.Is(err, smeldr.ErrNotFound) || err.Error() != smeldr.ErrNotFound.Error() {
		t.Errorf("a missing memo: %v (%q); want item.not_found, ErrNotFound, its text", err, smeldr.ProblemTypeOf(err))
	}

	_, err = repo.CreateDraft(smeldr.TrustedRead(ctx), map[string]any{"Title": 7})
	if smeldr.ProblemTypeOf(err) != "field.type_mismatch" || smeldr.ProblemValuesOf(err)["expected"] != "string" {
		t.Errorf("a number as the title: %v; want field.type_mismatch expecting a string", err)
	}

	_, err = smeldr.NewSchemaStore(db).FindByTypeName(ctx, "no_such_type")
	if smeldr.ProblemTypeOf(err) != "content_type.not_found" || !errors.Is(err, smeldr.ErrNotFound) {
		t.Errorf("a missing schema: %v; want content_type.not_found", err)
	}
}
