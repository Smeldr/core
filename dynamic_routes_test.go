package smeldr

import (
	"context"
	"testing"
)

// TestDynamicRouteRegistration_WritesSmeldrRoutes verifies that DefineContentType
// inserts list and item rows into smeldr_routes when url_prefix is non-empty.
func TestDynamicRouteRegistration_WritesSmeldrRoutes(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateRoutesTable(db); err != nil {
		t.Fatalf("CreateRoutesTable: %v", err)
	}
	if err := CreateSchemaTable(db); err != nil {
		t.Fatalf("CreateSchemaTable: %v", err)
	}

	app := New(Config{BaseURL: "http://localhost", Secret: []byte("test-secret-key!!"), DB: db})
	ctx := context.Background()

	_, err := app.DefineContentType(ctx, &ContentTypeSchema{
		TypeName:  "widget",
		URLPrefix: "/widgets",
	})
	if err != nil {
		t.Fatalf("DefineContentType: %v", err)
	}

	rows, err := Query[struct {
		Pattern string `db:"path_pattern"`
		View    string `db:"view"`
	}](ctx, db, "SELECT path_pattern, view FROM smeldr_routes WHERE route_type='content' ORDER BY view")
	if err != nil {
		t.Fatalf("query smeldr_routes: %v", err)
	}

	if len(rows) != 2 {
		t.Fatalf("want 2 rows in smeldr_routes, got %d", len(rows))
	}

	want := map[string]string{
		"item": "/widgets/{slug}",
		"list": "/widgets",
	}
	for _, r := range rows {
		if want[r.View] != r.Pattern {
			t.Errorf("view %q: pattern = %q; want %q", r.View, r.Pattern, want[r.View])
		}
	}
}

// TestLoadDynamicTypes_BackfillsRoutes verifies that loadDynamicTypes upserts
// smeldr_routes rows for types that were defined before this amendment.
func TestLoadDynamicTypes_BackfillsRoutes(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateRoutesTable(db); err != nil {
		t.Fatalf("CreateRoutesTable: %v", err)
	}
	if err := CreateSchemaTable(db); err != nil {
		t.Fatalf("CreateSchemaTable: %v", err)
	}

	// Write a schema directly — simulates a type defined before A169.
	ctx := context.Background()
	store := NewSchemaStore(db)
	schema := &ContentTypeSchema{
		TypeName:  "gadget",
		URLPrefix: "/gadgets",
		Kind:      "content",
	}
	if err := store.Save(ctx, schema); err != nil {
		t.Fatalf("store.Save: %v", err)
	}

	// App without WithDynamicContent — call loadDynamicTypes manually.
	app := New(Config{BaseURL: "http://localhost", Secret: []byte("test-secret-key!!"), DB: db})
	app.loadDynamicTypes(ctx)

	rows, err := Query[struct {
		Pattern string `db:"path_pattern"`
		View    string `db:"view"`
	}](ctx, db, "SELECT path_pattern, view FROM smeldr_routes WHERE route_type='content' ORDER BY view")
	if err != nil {
		t.Fatalf("query smeldr_routes: %v", err)
	}

	if len(rows) != 2 {
		t.Fatalf("loadDynamicTypes: want 2 rows in smeldr_routes, got %d", len(rows))
	}

	want := map[string]string{
		"item": "/gadgets/{slug}",
		"list": "/gadgets",
	}
	for _, r := range rows {
		if want[r.View] != r.Pattern {
			t.Errorf("view %q: pattern = %q; want %q", r.View, r.Pattern, want[r.View])
		}
	}
}

// TestDynamicRouteRegistration_Idempotent verifies that calling DefineContentType
// followed by loadDynamicTypes does not duplicate rows in smeldr_routes.
func TestDynamicRouteRegistration_Idempotent(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateRoutesTable(db); err != nil {
		t.Fatalf("CreateRoutesTable: %v", err)
	}
	if err := CreateSchemaTable(db); err != nil {
		t.Fatalf("CreateSchemaTable: %v", err)
	}

	ctx := context.Background()
	app := New(Config{BaseURL: "http://localhost", Secret: []byte("test-secret-key!!"), DB: db})

	_, err := app.DefineContentType(ctx, &ContentTypeSchema{
		TypeName:  "thing",
		URLPrefix: "/things",
	})
	if err != nil {
		t.Fatalf("DefineContentType: %v", err)
	}

	// Simulate restart: new app, loadDynamicTypes backfill.
	app2 := New(Config{BaseURL: "http://localhost", Secret: []byte("test-secret-key!!"), DB: db})
	app2.loadDynamicTypes(ctx)

	rows, err := Query[struct {
		Count int `db:"cnt"`
	}](ctx, db, "SELECT COUNT(*) as cnt FROM smeldr_routes WHERE route_type='content'")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows[0].Count != 2 {
		t.Errorf("want 2 rows (no duplicates), got %d", rows[0].Count)
	}
}
