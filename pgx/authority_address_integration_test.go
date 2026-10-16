//go:build integration

package pgx

import (
	"context"
	"testing"

	smeldr "smeldr.dev/core"
)

// Rule and AuthorityStub addresses on Postgres: CreateAuthorityTables adds the
// address storage idempotently, and creates are numbered U1, U2 and B1
// (core-rule-and-stub-slugs-without-reservation).
func TestPG_AuthorityAddresses(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	for range 2 {
		if err := smeldr.CreateAuthorityTables(db); err != nil {
			t.Fatalf("CreateAuthorityTables: %v", err)
		}
	}
	app := smeldr.New(smeldr.Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	smeldr.RegisterAuthorityTypes(app, db)
	_ = app.Handler()
	author := smeldr.TrustedRead(smeldr.NewTestContext(smeldr.User{ID: "u-author", Roles: []smeldr.Role{smeldr.Author}})).(smeldr.Context)

	create := func(typeName string, fields map[string]any) string {
		t.Helper()
		for _, m := range app.MCPModules() {
			if m.MCPMeta().TypeName != typeName {
				continue
			}
			out, err := m.MCPCreate(author, fields)
			if err != nil {
				t.Fatalf("create %s: %v", typeName, err)
			}
			switch v := out.(type) {
			case *smeldr.Rule:
				return v.Address
			case *smeldr.AuthorityStub:
				return v.Address
			}
			t.Fatalf("create %s returned %T", typeName, out)
		}
		t.Fatalf("no module for %s", typeName)
		return ""
	}
	for _, want := range []string{"U1", "U2"} {
		if got := create("Rule", map[string]any{"rule_type": "design-system"}); got != want {
			t.Errorf("Rule address = %q; want %s", got, want)
		}
	}
	if got := create("AuthorityStub", map[string]any{"source_ref": "design/x.md"}); got != "B1" {
		t.Errorf("AuthorityStub address = %q; want B1", got)
	}
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pg_indexes WHERE indexname IN ('idx_smeldr_rules_address', 'idx_smeldr_authority_stubs_address')`).Scan(&n); err != nil || n != 2 {
		t.Errorf("address indexes = %d (%v); want 2", n, err)
	}
}
