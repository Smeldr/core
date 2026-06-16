package smeldr_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"

	_ "modernc.org/sqlite"
	smeldr "smeldr.dev/core"
)

// errDB is a minimal smeldr.DB implementation that errors on every call.
// Used to test the non-SQLite early-return path in MigrateURLPrefixColumn.
type errDB struct{}

func (errDB) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, fmt.Errorf("not supported")
}
func (errDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return nil, fmt.Errorf("not supported")
}
func (errDB) QueryRowContext(_ context.Context, _ string, _ ...any) *sql.Row {
	return nil
}

func newSchemaDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := smeldr.CreateSchemaTable(db); err != nil {
		t.Fatalf("CreateSchemaTable: %v", err)
	}
	return db
}

func TestCreateSchemaTable_Idempotent(t *testing.T) {
	db := newSchemaDB(t)
	if err := smeldr.CreateSchemaTable(db); err != nil {
		t.Fatalf("second call: %v", err)
	}
}

func TestSeedBlockTypeSchemas_Idempotent(t *testing.T) {
	db := newSchemaDB(t)
	if err := smeldr.SeedBlockTypeSchemas(db); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	if err := smeldr.SeedBlockTypeSchemas(db); err != nil {
		t.Fatalf("second seed: %v", err)
	}
}

func TestSeedBlockTypeSchemas_AllTypesPresent(t *testing.T) {
	db := newSchemaDB(t)
	if err := smeldr.SeedBlockTypeSchemas(db); err != nil {
		t.Fatalf("seed: %v", err)
	}

	store := smeldr.NewSchemaStore(db)
	schemas, err := store.All(context.Background())
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(schemas) != 16 {
		t.Fatalf("want 16 schemas, got %d", len(schemas))
	}

	want := []string{
		"content_block", "content_grid", "content_list",
		"contact_card", "faq", "faq_item",
		"footer", "gallery", "hero",
		"html_block", "html_grid", "image",
		"link_collection", "link_item", "quote", "team",
	}
	got := make(map[string]bool, len(schemas))
	for _, s := range schemas {
		got[s.TypeName] = true
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("missing type_name %q", name)
		}
	}
}

func TestSchemaStore_FindByTypeName_Found(t *testing.T) {
	db := newSchemaDB(t)
	if err := smeldr.SeedBlockTypeSchemas(db); err != nil {
		t.Fatalf("seed: %v", err)
	}

	store := smeldr.NewSchemaStore(db)
	schema, err := store.FindByTypeName(context.Background(), "content_block")
	if err != nil {
		t.Fatalf("FindByTypeName: %v", err)
	}
	if schema.TypeName != "content_block" {
		t.Errorf("TypeName = %q, want content_block", schema.TypeName)
	}
	if schema.Label != "Content Block" {
		t.Errorf("Label = %q, want Content Block", schema.Label)
	}
	fields, err := schema.ParseFields()
	if err != nil {
		t.Fatalf("ParseFields: %v", err)
	}
	if len(fields) == 0 {
		t.Fatal("want fields, got empty")
	}
}

func TestSchemaStore_FindByTypeName_NotFound(t *testing.T) {
	db := newSchemaDB(t)
	if err := smeldr.SeedBlockTypeSchemas(db); err != nil {
		t.Fatalf("seed: %v", err)
	}

	store := smeldr.NewSchemaStore(db)
	_, err := store.FindByTypeName(context.Background(), "custom_nonexistent_type")
	if err != smeldr.ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestMigrateURLPrefixColumn_AddsColumn(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	// Old-style table without url_prefix column
	_, err = db.Exec(`CREATE TABLE smeldr_content_type_schemas (
		id        TEXT NOT NULL PRIMARY KEY,
		type_name TEXT NOT NULL UNIQUE,
		label     TEXT NOT NULL DEFAULT '',
		kind      TEXT NOT NULL DEFAULT '',
		fields    TEXT NOT NULL DEFAULT '[]',
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`)
	if err != nil {
		t.Fatalf("create old table: %v", err)
	}

	if err := smeldr.MigrateURLPrefixColumn(db); err != nil {
		t.Fatalf("MigrateURLPrefixColumn: %v", err)
	}

	// Column should now exist
	_, err = db.Exec(`INSERT INTO smeldr_content_type_schemas
		(id, type_name, label, kind, url_prefix, fields, created_at, updated_at)
		VALUES ('1', 'test', 'Test', 'content', '/tests', '[]', '2025-01-01', '2025-01-01')`)
	if err != nil {
		t.Errorf("url_prefix column should exist after migration: %v", err)
	}

	// Idempotent: second call is a no-op
	if err := smeldr.MigrateURLPrefixColumn(db); err != nil {
		t.Fatalf("second MigrateURLPrefixColumn: %v", err)
	}
}

// TestMigrateURLPrefixColumn_NonSQLite verifies that a QueryContext error (e.g. from
// a non-SQLite DB) causes MigrateURLPrefixColumn to return nil (assumed current schema).
func TestMigrateURLPrefixColumn_NonSQLite(t *testing.T) {
	if err := smeldr.MigrateURLPrefixColumn(errDB{}); err != nil {
		t.Fatalf("expected nil for non-SQLite DB, got: %v", err)
	}
}

func TestValidateBlockFields_AcceptsValidFields(t *testing.T) {
	schema := schemaWith(t, "test_type", []smeldr.SchemaField{
		{Name: "Title", Type: "string", Required: true},
		{Name: "Body", Type: "string"},
	})
	fields := json.RawMessage(`{"Title":"Hello","Body":"World"}`)
	if err := smeldr.ValidateBlockFields(schema, fields); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateBlockFields_AcceptsOnlyRequired(t *testing.T) {
	schema := schemaWith(t, "test_type", []smeldr.SchemaField{
		{Name: "Title", Type: "string", Required: true},
		{Name: "Body", Type: "string"},
	})
	fields := json.RawMessage(`{"Title":"Hello"}`)
	if err := smeldr.ValidateBlockFields(schema, fields); err != nil {
		t.Fatalf("optional absent field should not error: %v", err)
	}
}

func TestValidateBlockFields_RejectsUnknownField(t *testing.T) {
	schema := schemaWith(t, "test_type", []smeldr.SchemaField{
		{Name: "Title", Type: "string", Required: true},
	})
	fields := json.RawMessage(`{"Title":"Hello","Unknown":"oops"}`)
	err := smeldr.ValidateBlockFields(schema, fields)
	if err == nil {
		t.Fatal("want error for unknown field")
	}
}

func TestValidateBlockFields_RejectsMissingRequired(t *testing.T) {
	schema := schemaWith(t, "test_type", []smeldr.SchemaField{
		{Name: "Title", Type: "string", Required: true},
		{Name: "Body", Type: "string"},
	})
	fields := json.RawMessage(`{"Body":"World"}`)
	err := smeldr.ValidateBlockFields(schema, fields)
	if err == nil {
		t.Fatal("want error for missing required field")
	}
}

func TestValidateBlockFields_EmptyFieldsTreatedAsEmpty(t *testing.T) {
	schema := schemaWith(t, "test_type", []smeldr.SchemaField{
		{Name: "Title", Type: "string"},
	})
	if err := smeldr.ValidateBlockFields(schema, nil); err != nil {
		t.Fatalf("nil fields should be treated as empty object: %v", err)
	}
	if err := smeldr.ValidateBlockFields(schema, json.RawMessage{}); err != nil {
		t.Fatalf("empty fields should be treated as empty object: %v", err)
	}
}

func TestParseFields_invalidJSON(t *testing.T) {
	schema := &smeldr.ContentTypeSchema{
		ID:       "test-id",
		TypeName: "test_type",
		Label:    "test_type",
		Fields:   json.RawMessage(`{invalid json}`),
	}
	_, err := schema.ParseFields()
	if err == nil {
		t.Error("expected error for invalid JSON in Fields")
	}
}

func TestValidateBlockFields_InvalidSchemaJSON(t *testing.T) {
	schema := &smeldr.ContentTypeSchema{
		ID:       "x",
		TypeName: "bad_schema",
		Fields:   json.RawMessage(`{invalid}`),
	}
	if err := smeldr.ValidateBlockFields(schema, json.RawMessage(`{"Title":"x"}`)); err == nil {
		t.Fatal("expected error for invalid schema JSON")
	}
}

func TestValidateBlockFields_InvalidFieldsJSON(t *testing.T) {
	schema := schemaWith(t, "x", []smeldr.SchemaField{{Name: "Title", Type: "string"}})
	if err := smeldr.ValidateBlockFields(schema, json.RawMessage(`{bad json`)); err == nil {
		t.Fatal("expected error for invalid fields JSON")
	}
}

// schemaWith builds a ContentTypeSchema without touching the database.
func schemaWith(t *testing.T, typeName string, fields []smeldr.SchemaField) *smeldr.ContentTypeSchema {
	t.Helper()
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal fields: %v", err)
	}
	return &smeldr.ContentTypeSchema{
		ID:       "test-id",
		TypeName: typeName,
		Label:    typeName,
		Fields:   raw,
	}
}
