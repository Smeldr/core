// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Rule and AuthorityStub are core records with a D110 address: Rule U,
// AuthorityStub B (core-rule-and-stub-slugs-without-reservation).

func authorityApp(t *testing.T, db *sql.DB) *App {
	t.Helper()
	if err := CreateAuthorityTables(db); err != nil {
		t.Fatalf("CreateAuthorityTables: %v", err)
	}
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	RegisterAuthorityTypes(app, db)
	_ = app.Handler()
	return app
}

func TestAuthorityAddresses_Create(t *testing.T) {
	db := newSQLiteDB(t)
	app := authorityApp(t, db)
	rules := findModule[*Rule](t, app)
	stubs := findModule[*AuthorityStub](t, app)

	var made []*Rule
	for i, want := range []string{"U1", "U2"} {
		out, err := rules.MCPCreate(authorCtx(), map[string]any{"slug": "rule-" + string(rune('a'+i)), "rule_type": "design-system"})
		if err != nil {
			t.Fatal(err)
		}
		r := out.(*Rule)
		if r.Address != want || r.AddressNo != int64(i+1) {
			t.Errorf("Rule address = %q %d; want %s", r.Address, r.AddressNo, want)
		}
		made = append(made, r)
	}
	out, err := stubs.MCPCreate(authorCtx(), map[string]any{"source_ref": "design/x.md"})
	if err != nil {
		t.Fatal(err)
	}
	if s := out.(*AuthorityStub); s.Address != "B1" {
		t.Errorf("AuthorityStub address = %q; want B1", s.Address)
	}
	for _, ident := range []string{"U1", "u1", made[0].Slug, made[0].ID} {
		got, err := rules.MCPGet(authorCtx(), ident)
		if err != nil || got.(*Rule).ID != made[0].ID {
			t.Errorf("MCPGet(%q) = %v, %v; want the first Rule", ident, got, err)
		}
	}
	b, _ := json.Marshal(made[0])
	if !strings.Contains(string(b), `"address":"U1"`) {
		t.Errorf("JSON = %s; want the address", b)
	}
	if !rules.addressed || rules.draftSlugsShared() {
		t.Error("Rule is not an addressed record")
	}
	if got := lookupAddressByID(context.Background(), db, "Rule", made[1].ID, false); got != "U2" {
		t.Errorf("lookupAddressByID = %q; want U2", got)
	}
}

// A scoped caller whose supplied slug is taken gets the ID tail and the
// slug_adjusted warning, as for the other core records.
func TestAuthorityAddresses_SlugAdjusted(t *testing.T) {
	db := newSQLiteDB(t)
	app := authorityApp(t, db)
	rules := findModule[*Rule](t, app)
	if _, err := rules.MCPCreate(authorCtx(), map[string]any{"slug": "taken"}); err != nil {
		t.Fatal(err)
	}
	scoped := WithReadScope(authorCtx(), &ReadScope{items: map[string]map[string]bool{}}).(Context)
	out, err := rules.MCPCreate(scoped, map[string]any{"slug": "taken"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	if !strings.Contains(string(b), "slug_adjusted") || strings.Contains(string(b), `"slug":"taken"`) {
		t.Errorf("result = %s; want an adjusted slug and the warning", b)
	}
}

// A table from before addresses gets the columns at boot; its rows keep an
// empty address, are reserved, and MigrateAddresses numbers them below the
// first create after the deploy.
func TestAuthorityAddresses_ExistingRows(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	for _, stmt := range []string{
		`CREATE TABLE smeldr_rules (id TEXT PRIMARY KEY, slug TEXT NOT NULL UNIQUE, status TEXT NOT NULL DEFAULT 'draft',
			published_at TIMESTAMPTZ, scheduled_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL,
			rev INTEGER NOT NULL DEFAULT 0, surface TEXT NOT NULL DEFAULT '', rule_type TEXT NOT NULL DEFAULT '',
			body TEXT NOT NULL DEFAULT '', source_ref TEXT NOT NULL DEFAULT '')`,
	} {
		mustExecMig(t, db, stmt)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mustExecMig(t, db, `INSERT INTO smeldr_rules (id, slug, created_at, updated_at) VALUES ('r-1', 'old-1', $1, $1)`, base)
	mustExecMig(t, db, `INSERT INTO smeldr_rules (id, slug, created_at, updated_at) VALUES ('r-2', 'old-2', $1, $1)`, base.Add(time.Minute))

	app := authorityApp(t, db)
	if a, _, _ := addressOfRow(t, db, "smeldr_rules", "r-1"); a != "" {
		t.Errorf("an old row got an address at boot: %q", a)
	}
	rules := findModule[*Rule](t, app)
	out, err := rules.MCPCreate(authorCtx(), map[string]any{"slug": "new"})
	if err != nil || out.(*Rule).Address != "U3" {
		t.Fatalf("post-deploy Rule = %v, %v; want U3 (two old rows reserved)", out, err)
	}
	if err := CreateAuthorityTables(db); err != nil {
		t.Fatalf("second CreateAuthorityTables: %v", err)
	}
	rep, err := MigrateAddresses(ctx, db, AddressMigrationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if tr := typeReport(rep, "Rule"); tr.TypeName == "" {
		t.Errorf("report = %+v; want a Rule entry", rep)
	}
	for id, want := range map[string]string{"r-1": "U1", "r-2": "U2"} {
		if a, _, _ := addressOfRow(t, db, "smeldr_rules", id); a != want {
			t.Errorf("%s address = %q; want %s", id, a, want)
		}
	}
	if out, err := rules.MCPCreate(authorCtx(), map[string]any{"slug": "after"}); err != nil || out.(*Rule).Address != "U4" {
		t.Errorf("after the migration = %v, %v; want U4", out, err)
	}
}

// A duplicate address stops boot, as for the other core records.
func TestAuthorityAddresses_DuplicateStopsBoot(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateAuthorityTables(db); err != nil {
		t.Fatal(err)
	}
	mustExecMig(t, db, `DROP INDEX idx_smeldr_authority_stubs_address`)
	now := time.Now().UTC()
	for _, id := range []string{"s-1", "s-2"} {
		mustExecMig(t, db, `INSERT INTO smeldr_authority_stubs (id, slug, address, created_at, updated_at) VALUES ($1, $1, 'B1', $2, $2)`, id, now)
	}
	if err := CreateAuthorityTables(db); err == nil {
		t.Error("CreateAuthorityTables with a duplicate address = nil; want an error")
	}
}

// U and B belong to Rule and AuthorityStub only.
func TestAuthorityAddresses_Prefixes(t *testing.T) {
	for _, c := range []struct{ typeName, prefix string }{{"Post", "U"}, {"Post", "B"}, {"Rule", "B"}, {"AuthorityStub", "U"}} {
		if err := checkAddressPrefix(c.typeName, c.prefix); err == nil {
			t.Errorf("%s may take %s", c.typeName, c.prefix)
		}
	}
	for _, c := range []struct{ typeName, prefix string }{{"Rule", "U"}, {"AuthorityStub", "B"}} {
		if err := checkAddressPrefix(c.typeName, c.prefix); err != nil {
			t.Errorf("%s %s: %v", c.typeName, c.prefix, err)
		}
	}
}

// Without the authority tables (no ENABLE_AUTHORITY), an orchestration type's
// transition event still carries its address, and a Rule lookup is empty
// without an error (architect binding).
func TestAuthorityAddresses_NoAuthorityTables(t *testing.T) {
	ctx := context.Background()
	db := addressDB(t)
	app, m := decisionModule(t, db)
	d := mustCreateDecision(t, m, map[string]any{"Title": "event"})
	if exists, _ := tableExists(ctx, db, "smeldr_rules"); exists {
		t.Fatal("the fixture has authority tables")
	}
	if got := app.transitionAddress(ctx, statusTransition{typeName: "Decision", id: d.ID}); got != "D1" {
		t.Errorf("Decision transition address = %q; want D1", got)
	}
	if got := lookupAddressByID(ctx, db, "Rule", "r-1", false); got != "" {
		t.Errorf("Rule lookup without its table = %q; want empty", got)
	}
	types, err := migrationTypes(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	for _, mt := range types {
		if mt.typeName == "Rule" || mt.typeName == "AuthorityStub" {
			t.Errorf("migrationTypes lists %s without its table", mt.typeName)
		}
	}
}
