//go:build integration

package pgx

import (
	"context"
	"errors"
	"testing"

	smeldr "smeldr.dev/core"
)

// T914 (D148) on Postgres: a Decision's sources round-trip through the JSON
// column, and a ratified Decision's sources are frozen.
func TestPG_DecisionSources(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	app, store := newPGApp(t, db)
	for _, create := range []func(smeldr.DB) error{smeldr.CreateGovernanceTables, smeldr.CreateRelationTables} {
		if err := create(db); err != nil {
			t.Fatal(err)
		}
	}
	smeldr.RegisterGovernanceTypes(app, db)
	rels, err := smeldr.NewRelationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := smeldr.RegisterGovernanceRelationKinds(ctx, rels); err != nil {
		t.Fatal(err)
	}
	app.Relations(rels)
	_ = app.Handler()
	if err := smeldr.EnsureDecisionSourcesColumn(ctx, db); err != nil {
		t.Fatalf("EnsureDecisionSourcesColumn on a fresh table: %v", err)
	}
	if _, err := store.Grant(ctx, smeldr.RoleGrant{TokenID: "author", RoleName: "editor"}); err != nil {
		t.Fatal(err)
	}
	author := smeldr.NewTestContext(smeldr.User{ID: "author", Roles: []smeldr.Role{smeldr.Editor}})
	var decisions smeldr.MCPModule
	for _, m := range app.MCPModules() {
		if m.MCPMeta().TypeName == "Decision" {
			decisions = m
		}
	}
	sha := "0123456789abcdef0123456789abcdef01234567"
	out, err := decisions.MCPCreate(author, map[string]any{"title": "Money in cents", "sources": []any{
		map[string]any{"repository": "Acme/App", "commit": sha, "path": "docs/money.md", "role": "source"},
		map[string]any{"repository": "acme/app", "commit": sha, "path": "internal/money.go", "anchor": "L10-L40", "role": "implementation"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	d := out.(*smeldr.Decision)
	got, err := decisions.MCPGet(author, d.Slug)
	if err != nil {
		t.Fatal(err)
	}
	sources := got.(*smeldr.Decision).Sources
	if len(sources) != 2 || sources[0].Repository != "acme/app" || sources[1].Anchor != "L10-L40" {
		t.Errorf("round trip: %+v", sources)
	}
	if _, err := db.ExecContext(ctx, `UPDATE smeldr_decisions SET status = 'ratified' WHERE id = $1`, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := decisions.MCPUpdate(author, d.Slug, map[string]any{"sources": []any{}}); !errors.Is(err, smeldr.ErrConflict) {
		t.Errorf("ratified sources changed: %v, want the lock's conflict", err)
	}
}
