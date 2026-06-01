package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
)

// tableExists reports whether a table or index of the given name exists in the
// SQLite schema.
func tableExists(t *testing.T, db DB, name string) bool {
	t.Helper()
	var found string
	err := db.QueryRowContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE name = $1`, name).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		t.Fatalf("tableExists(%q): %v", name, err)
	}
	return found == name
}

func TestCreateBlockTables(t *testing.T) {
	db := newSQLiteDB(t)

	if err := CreateBlockTables(db); err != nil {
		t.Fatalf("CreateBlockTables: %v", err)
	}

	for _, name := range []string{
		"smeldr_dynamic_content",
		"smeldr_content_edges",
		"idx_content_edges_parent",
	} {
		if !tableExists(t, db, name) {
			t.Errorf("expected %q to exist after CreateBlockTables", name)
		}
	}

	// content_type_schemas is a later component — it must NOT be created here.
	if tableExists(t, db, "smeldr_content_type_schemas") {
		t.Error("smeldr_content_type_schemas should not be created by CreateBlockTables")
	}

	// Idempotent: a second call must not error.
	if err := CreateBlockTables(db); err != nil {
		t.Fatalf("CreateBlockTables (second call): %v", err)
	}
}

func TestDynamicNode_RoundTrip(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateBlockTables(db); err != nil {
		t.Fatalf("CreateBlockTables: %v", err)
	}
	repo := NewDynamicContentRepo(db)
	ctx := context.Background()

	id := NewID()
	want := &DynamicNode{
		Node:     Node{ID: id, Slug: "", Status: Draft},
		TypeName: "content_block",
		Fields:   json.RawMessage(`{"title":"Hello","body":"World"}`),
	}
	if err := repo.Save(ctx, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := repo.FindByID(ctx, id)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.TypeName != "content_block" {
		t.Errorf("TypeName = %q, want content_block", got.TypeName)
	}
	if got.Status != Draft {
		t.Errorf("Status = %q, want draft", got.Status)
	}

	// Fields must survive the JSON round trip intact.
	var fields map[string]string
	if err := json.Unmarshal(got.Fields, &fields); err != nil {
		t.Fatalf("unmarshal Fields: %v (raw %q)", err, got.Fields)
	}
	if fields["title"] != "Hello" || fields["body"] != "World" {
		t.Errorf("Fields = %v, want title=Hello body=World", fields)
	}
}

func TestDynamicNode_Update(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateBlockTables(db); err != nil {
		t.Fatalf("CreateBlockTables: %v", err)
	}
	repo := NewDynamicContentRepo(db)
	ctx := context.Background()

	id := NewID()
	node := &DynamicNode{
		Node:     Node{ID: id, Status: Draft},
		TypeName: "hero",
		Fields:   json.RawMessage(`{"headline":"v1"}`),
	}
	if err := repo.Save(ctx, node); err != nil {
		t.Fatalf("Save: %v", err)
	}

	node.Status = Published
	node.Fields = json.RawMessage(`{"headline":"v2"}`)
	if err := repo.Save(ctx, node); err != nil {
		t.Fatalf("Save (update): %v", err)
	}

	got, err := repo.FindByID(ctx, id)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.Status != Published {
		t.Errorf("Status = %q, want published", got.Status)
	}
	var fields map[string]string
	if err := json.Unmarshal(got.Fields, &fields); err != nil {
		t.Fatalf("unmarshal Fields: %v", err)
	}
	if fields["headline"] != "v2" {
		t.Errorf("headline = %q, want v2", fields["headline"])
	}
}

func TestDynamicNode_Delete(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateBlockTables(db); err != nil {
		t.Fatalf("CreateBlockTables: %v", err)
	}
	repo := NewDynamicContentRepo(db)
	ctx := context.Background()

	id := NewID()
	node := &DynamicNode{Node: Node{ID: id, Status: Draft}, TypeName: "faq_item", Fields: json.RawMessage(`{}`)}
	if err := repo.Save(ctx, node); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := repo.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.FindByID(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("FindByID after delete: err = %v, want ErrNotFound", err)
	}
}
