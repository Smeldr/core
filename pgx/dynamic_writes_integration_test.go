//go:build integration

package pgx

import (
	"context"
	"testing"

	smeldr "smeldr.dev/core"
)

// A dynamic item's create and update record their actor on Postgres:
// last_actor follows the writer, and a create and an update entry name the
// caller and the surface.
func TestPG_DynamicWritesRecordActor(t *testing.T) {
	db, _ := isolatedDB(t)
	app, _ := dynamicApp(t, db, nil)
	if err := smeldr.CreateProvenanceTable(db); err != nil {
		t.Fatalf("CreateProvenanceTable: %v", err)
	}
	prov := smeldr.NewProvenanceStore(db)
	app.Provenance(prov)
	repo, err := app.DynamicContentRepo("memo")
	if err != nil {
		t.Fatalf("DynamicContentRepo: %v", err)
	}
	alice := smeldr.NewContextWithUser(smeldr.User{ID: "alice", Roles: []smeldr.Role{smeldr.Editor}})
	bob := smeldr.NewContextWithUser(smeldr.User{ID: "bob", Roles: []smeldr.Role{smeldr.Editor}})

	node, err := repo.CreateDraftVia(alice, "mcp", map[string]any{"Title": "first"})
	if err != nil {
		t.Fatalf("CreateDraftVia: %v", err)
	}
	if err := repo.UpdateFieldsVia(bob, "http", node.ID, map[string]any{"Title": "edited"}); err != nil {
		t.Fatalf("UpdateFieldsVia: %v", err)
	}
	got, err := repo.GetByID(context.Background(), node.ID)
	if err != nil || got.LastActor != "bob" {
		t.Fatalf("last_actor = %q (%v); want bob", got.LastActor, err)
	}
	recs, err := prov.List(context.Background(), smeldr.ProvenanceFilter{SubjectType: "memo", SubjectID: node.ID})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]smeldr.ProvenanceRecord{}
	for _, r := range recs {
		seen[r.Verb] = r
	}
	if c := seen["create"]; c.ActorID != "alice" || c.Surface != "mcp" {
		t.Errorf("create = %+v; want alice via mcp", c)
	}
	if u := seen["update"]; u.ActorID != "bob" || u.Surface != "http" || u.FromState != "draft" || u.ToState != "draft" {
		t.Errorf("update = %+v; want bob via http, draft -> draft", u)
	}
}
