//go:build integration

package pgx

import (
	"context"
	"testing"

	smeldr "smeldr.dev/core"
)

// TestPG_GrantKeepsRuleTypePattern: on an App with governance, a stewardship
// pattern names a synthetic type and is stored as given on Postgres, and the
// steward's rule types read it back.
func TestPG_GrantKeepsRuleTypePattern(t *testing.T) {
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
	if err := rs.DefineRole(ctx, smeldr.RoleDefinition{Name: "rule-steward", Operations: []string{"steward"}, ScopeMode: smeldr.ScopeStatic}); err != nil {
		t.Fatal(err)
	}
	id, err := rs.Grant(ctx, smeldr.RoleGrant{TokenID: "steward", RoleName: "rule-steward", ScopeStatic: []string{"RuleType:design-system"}})
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	g, err := rs.GetGrant(ctx, id)
	if err != nil || len(g.ScopeStatic) != 1 || g.ScopeStatic[0] != "RuleType:design-system" {
		t.Errorf("stored scope = %v %v; want [RuleType:design-system]", g.ScopeStatic, err)
	}
	if types, err := rs.StewardedRuleTypes(ctx, "steward"); err != nil || len(types) != 1 || types[0] != "design-system" {
		t.Errorf("StewardedRuleTypes = %v %v; want [design-system]", types, err)
	}
}
