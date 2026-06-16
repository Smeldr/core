package smeldr_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	_ "modernc.org/sqlite"
	smeldr "smeldr.dev/core"
)

// — helpers ——————————————————————————————————————————————————————————————————

func openDynDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := smeldr.CreateBlockTables(db); err != nil {
		t.Fatalf("CreateBlockTables: %v", err)
	}
	if err := smeldr.CreateSchemaTable(db); err != nil {
		t.Fatalf("CreateSchemaTable: %v", err)
	}
	return db
}

func recipeSchema() *smeldr.ContentTypeSchema {
	fields, _ := json.Marshal([]smeldr.SchemaField{
		{Name: "Title", Type: "string", Required: true, Role: "title"},
		{Name: "Body", Type: "string", Role: "body"},
	})
	return &smeldr.ContentTypeSchema{
		TypeName:  "recipe",
		Label:     "Recipe",
		Kind:      "content",
		URLPrefix: "/recipes",
		Fields:    json.RawMessage(fields),
	}
}

// — DynamicTypeRepo ——————————————————————————————————————————————————————————

func TestDynamicTypeRepo_CreateDraft(t *testing.T) {
	db := openDynDB(t)
	schema := recipeSchema()
	repo := smeldr.NewDynamicTypeRepo(db, "recipe", schema)

	node, err := repo.CreateDraft(context.Background(), map[string]any{"Title": "Pasta", "Body": "Boil water"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if node.ID == "" {
		t.Error("expected non-empty ID")
	}
	if node.Slug != "pasta" {
		t.Errorf("slug = %q, want \"pasta\"", node.Slug)
	}
	if node.Status != smeldr.Draft {
		t.Errorf("status = %q, want Draft", node.Status)
	}
	if node.TypeName != "recipe" {
		t.Errorf("TypeName = %q, want \"recipe\"", node.TypeName)
	}
}

func TestDynamicTypeRepo_CreateDraft_SlugCollision(t *testing.T) {
	db := openDynDB(t)
	schema := recipeSchema()
	repo := smeldr.NewDynamicTypeRepo(db, "recipe", schema)

	_, err := repo.CreateDraft(context.Background(), map[string]any{"Title": "Pasta"})
	if err != nil {
		t.Fatalf("first CreateDraft: %v", err)
	}
	node2, err := repo.CreateDraft(context.Background(), map[string]any{"Title": "Pasta"})
	if err != nil {
		t.Fatalf("second CreateDraft: %v", err)
	}
	if node2.Slug != "pasta-2" {
		t.Errorf("collision slug = %q, want \"pasta-2\"", node2.Slug)
	}
}

func TestDynamicTypeRepo_CreateDraft_NoTitleField(t *testing.T) {
	db := openDynDB(t)
	schema := &smeldr.ContentTypeSchema{TypeName: "tag", Kind: "content", Fields: json.RawMessage(`[]`)}
	repo := smeldr.NewDynamicTypeRepo(db, "tag", schema)

	node, err := repo.CreateDraft(context.Background(), map[string]any{"name": "go"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if node.Slug != "item" {
		t.Errorf("slug fallback = %q, want \"item\"", node.Slug)
	}
}

func TestDynamicTypeRepo_CreateDraft_NilSchema(t *testing.T) {
	db := openDynDB(t)
	repo := smeldr.NewDynamicTypeRepo(db, "foo", nil)

	node, err := repo.CreateDraft(context.Background(), map[string]any{"x": "y"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if node.Slug != "item" {
		t.Errorf("nil-schema slug = %q, want \"item\"", node.Slug)
	}
}

func TestDynamicTypeRepo_GetBySlug(t *testing.T) {
	db := openDynDB(t)
	repo := smeldr.NewDynamicTypeRepo(db, "recipe", recipeSchema())

	created, _ := repo.CreateDraft(context.Background(), map[string]any{"Title": "Soup"})
	got, err := repo.GetBySlug(context.Background(), "soup")
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("ID mismatch: got %q, want %q", got.ID, created.ID)
	}
}

func TestDynamicTypeRepo_GetBySlug_NotFound(t *testing.T) {
	db := openDynDB(t)
	repo := smeldr.NewDynamicTypeRepo(db, "recipe", recipeSchema())

	_, err := repo.GetBySlug(context.Background(), "nonexistent")
	if err != smeldr.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDynamicTypeRepo_GetByID(t *testing.T) {
	db := openDynDB(t)
	repo := smeldr.NewDynamicTypeRepo(db, "recipe", recipeSchema())

	created, _ := repo.CreateDraft(context.Background(), map[string]any{"Title": "Stew"})
	got, err := repo.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Slug != "stew" {
		t.Errorf("slug = %q, want \"stew\"", got.Slug)
	}
}

func TestDynamicTypeRepo_GetByID_NotFound(t *testing.T) {
	db := openDynDB(t)
	repo := smeldr.NewDynamicTypeRepo(db, "recipe", recipeSchema())

	_, err := repo.GetByID(context.Background(), "nonexistent-id")
	if err != smeldr.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDynamicTypeRepo_List_Empty(t *testing.T) {
	db := openDynDB(t)
	repo := smeldr.NewDynamicTypeRepo(db, "recipe", recipeSchema())

	items, err := repo.List(context.Background(), smeldr.ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected empty list, got %d items", len(items))
	}
}

func TestDynamicTypeRepo_List_Published(t *testing.T) {
	db := openDynDB(t)
	repo := smeldr.NewDynamicTypeRepo(db, "recipe", recipeSchema())

	n1, _ := repo.CreateDraft(context.Background(), map[string]any{"Title": "A"})
	_, _ = repo.CreateDraft(context.Background(), map[string]any{"Title": "B"})

	_ = repo.SetStatus(context.Background(), n1.ID, smeldr.Published)

	items, err := repo.List(context.Background(), smeldr.ListOptions{Status: []smeldr.Status{smeldr.Published}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("expected 1 published item, got %d", len(items))
	}
}

func TestDynamicTypeRepo_List_Pagination(t *testing.T) {
	db := openDynDB(t)
	repo := smeldr.NewDynamicTypeRepo(db, "recipe", recipeSchema())

	for i := range 5 {
		repo.CreateDraft(context.Background(), map[string]any{"Title": string(rune('A' + i))})
	}

	page1, _ := repo.List(context.Background(), smeldr.ListOptions{Page: 1, PerPage: 2})
	page2, _ := repo.List(context.Background(), smeldr.ListOptions{Page: 2, PerPage: 2})

	if len(page1) != 2 {
		t.Errorf("page1 len = %d, want 2", len(page1))
	}
	if len(page2) != 2 {
		t.Errorf("page2 len = %d, want 2", len(page2))
	}
}

func TestDynamicTypeRepo_List_OrderByCreatedAt(t *testing.T) {
	db := openDynDB(t)
	repo := smeldr.NewDynamicTypeRepo(db, "recipe", recipeSchema())

	repo.CreateDraft(context.Background(), map[string]any{"Title": "First"})
	repo.CreateDraft(context.Background(), map[string]any{"Title": "Second"})

	items, err := repo.List(context.Background(), smeldr.ListOptions{OrderBy: "created_at", Desc: false})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) < 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
}

func TestDynamicTypeRepo_List_OrderByCreatedAtDesc(t *testing.T) {
	db := openDynDB(t)
	repo := smeldr.NewDynamicTypeRepo(db, "recipe", recipeSchema())

	repo.CreateDraft(context.Background(), map[string]any{"Title": "First"})
	repo.CreateDraft(context.Background(), map[string]any{"Title": "Second"})

	items, err := repo.List(context.Background(), smeldr.ListOptions{OrderBy: "CreatedAt", Desc: true})
	if err != nil {
		t.Fatalf("List desc: %v", err)
	}
	if len(items) < 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
}

func TestDynamicTypeRepo_List_DefaultOrder(t *testing.T) {
	db := openDynDB(t)
	repo := smeldr.NewDynamicTypeRepo(db, "recipe", recipeSchema())

	repo.CreateDraft(context.Background(), map[string]any{"Title": "A"})
	repo.CreateDraft(context.Background(), map[string]any{"Title": "B"})

	items, err := repo.List(context.Background(), smeldr.ListOptions{Desc: true})
	if err != nil {
		t.Fatalf("List default order: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
}

func TestDynamicTypeRepo_List_IsolatedByTypeName(t *testing.T) {
	db := openDynDB(t)
	r1 := smeldr.NewDynamicTypeRepo(db, "recipe", recipeSchema())
	r2 := smeldr.NewDynamicTypeRepo(db, "story", nil)

	r1.CreateDraft(context.Background(), map[string]any{"Title": "Pasta"})
	r2.CreateDraft(context.Background(), map[string]any{})

	items, _ := r1.List(context.Background(), smeldr.ListOptions{})
	if len(items) != 1 {
		t.Errorf("expected 1 recipe, got %d", len(items))
	}
}

func TestDynamicTypeRepo_List_NodeMetadata(t *testing.T) {
	db := openDynDB(t)
	repo := smeldr.NewDynamicTypeRepo(db, "recipe", recipeSchema())

	repo.CreateDraft(context.Background(), map[string]any{"Title": "Pasta"})
	items, _ := repo.List(context.Background(), smeldr.ListOptions{})

	if len(items) != 1 {
		t.Fatalf("expected 1 item")
	}
	m := items[0]
	if m["ID"] == nil || m["ID"] == "" {
		t.Error("ID should be set in list map")
	}
	if m["Slug"] != "pasta" {
		t.Errorf("Slug = %v", m["Slug"])
	}
	if m["TypeName"] != "recipe" {
		t.Errorf("TypeName = %v", m["TypeName"])
	}
}

func TestDynamicTypeRepo_UpdateFields(t *testing.T) {
	db := openDynDB(t)
	repo := smeldr.NewDynamicTypeRepo(db, "recipe", recipeSchema())

	node, _ := repo.CreateDraft(context.Background(), map[string]any{"Title": "Old", "Body": "old body"})
	err := repo.UpdateFields(context.Background(), node.ID, map[string]any{"Body": "new body"})
	if err != nil {
		t.Fatalf("UpdateFields: %v", err)
	}
	got, _ := repo.GetByID(context.Background(), node.ID)
	var fields map[string]any
	json.Unmarshal(got.Fields, &fields)
	if fields["Body"] != "new body" {
		t.Errorf("Body = %q, want \"new body\"", fields["Body"])
	}
	if fields["Title"] != "Old" {
		t.Errorf("Title should be preserved, got %q", fields["Title"])
	}
}

func TestDynamicTypeRepo_UpdateFields_NotFound(t *testing.T) {
	db := openDynDB(t)
	repo := smeldr.NewDynamicTypeRepo(db, "recipe", recipeSchema())

	err := repo.UpdateFields(context.Background(), "nonexistent", map[string]any{"Body": "x"})
	if err != smeldr.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDynamicTypeRepo_SetStatus_DraftToPublished(t *testing.T) {
	db := openDynDB(t)
	repo := smeldr.NewDynamicTypeRepo(db, "recipe", recipeSchema())

	node, _ := repo.CreateDraft(context.Background(), map[string]any{"Title": "Toast"})
	if !node.PublishedAt.IsZero() {
		t.Error("PublishedAt should be zero for draft")
	}

	err := repo.SetStatus(context.Background(), node.ID, smeldr.Published)
	if err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	got, _ := repo.GetByID(context.Background(), node.ID)
	if got.Status != smeldr.Published {
		t.Errorf("status = %q, want Published", got.Status)
	}
	if got.PublishedAt.IsZero() {
		t.Error("PublishedAt should be set after publish")
	}
}

func TestDynamicTypeRepo_SetStatus_PreservesPublishedAt(t *testing.T) {
	db := openDynDB(t)
	repo := smeldr.NewDynamicTypeRepo(db, "recipe", recipeSchema())

	node, _ := repo.CreateDraft(context.Background(), map[string]any{"Title": "x"})
	repo.SetStatus(context.Background(), node.ID, smeldr.Published)
	published, _ := repo.GetByID(context.Background(), node.ID)

	err := repo.SetStatus(context.Background(), node.ID, smeldr.Archived)
	if err != nil {
		t.Fatalf("SetStatus archived: %v", err)
	}
	got, _ := repo.GetByID(context.Background(), node.ID)
	if got.Status != smeldr.Archived {
		t.Errorf("status = %q, want Archived", got.Status)
	}
	if !got.PublishedAt.Equal(published.PublishedAt) {
		t.Error("PublishedAt should be preserved when archiving")
	}
}

func TestDynamicTypeRepo_SetStatus_NotFound(t *testing.T) {
	db := openDynDB(t)
	repo := smeldr.NewDynamicTypeRepo(db, "recipe", recipeSchema())

	err := repo.SetStatus(context.Background(), "nonexistent", smeldr.Published)
	if err != smeldr.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// — titleSlug via CreateDraft ————————————————————————————————————————————————

func TestTitleSlug_Slugify(t *testing.T) {
	db := openDynDB(t)
	repo := smeldr.NewDynamicTypeRepo(db, "recipe", recipeSchema())

	node, _ := repo.CreateDraft(context.Background(), map[string]any{"Title": "  Hello World!  "})
	if node.Slug != "hello-world" {
		t.Errorf("slug = %q, want \"hello-world\"", node.Slug)
	}
}

func TestTitleSlug_EmptyTitle_FallsBackToItem(t *testing.T) {
	db := openDynDB(t)
	repo := smeldr.NewDynamicTypeRepo(db, "recipe", recipeSchema())

	node, _ := repo.CreateDraft(context.Background(), map[string]any{"Title": ""})
	if node.Slug != "item" {
		t.Errorf("empty-title slug = %q, want \"item\"", node.Slug)
	}
}

// — PluralSnake ——————————————————————————————————————————————————————————————

func TestPluralSnake(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"recipe", "recipes"},
		{"story", "stories"},
		{"category", "categories"},
		{"key", "keys"},
		{"boy", "boys"},
		{"post", "posts"},
		{"tag", "tags"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got := smeldr.PluralSnake(tc.in)
			if got != tc.want {
				t.Errorf("PluralSnake(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// — ValidateSchemaDef ————————————————————————————————————————————————————————

func TestValidateSchemaDef_Valid(t *testing.T) {
	if err := smeldr.ValidateSchemaDef(recipeSchema()); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateSchemaDef_MissingTypeName(t *testing.T) {
	if err := smeldr.ValidateSchemaDef(&smeldr.ContentTypeSchema{Fields: json.RawMessage("[]")}); err == nil {
		t.Error("expected error for empty TypeName")
	}
}

func TestValidateSchemaDef_EmptyFields(t *testing.T) {
	schema := &smeldr.ContentTypeSchema{TypeName: "x", Fields: json.RawMessage("[]")}
	if err := smeldr.ValidateSchemaDef(schema); err != nil {
		t.Errorf("empty fields should pass: %v", err)
	}
}

func TestValidateSchemaDef_NilFields(t *testing.T) {
	schema := &smeldr.ContentTypeSchema{TypeName: "x"}
	if err := smeldr.ValidateSchemaDef(schema); err != nil {
		t.Errorf("nil fields should pass: %v", err)
	}
}

func TestValidateSchemaDef_UnknownType(t *testing.T) {
	fields, _ := json.Marshal([]smeldr.SchemaField{{Name: "X", Type: "uuid"}})
	schema := &smeldr.ContentTypeSchema{TypeName: "x", Fields: json.RawMessage(fields)}
	if err := smeldr.ValidateSchemaDef(schema); err == nil {
		t.Error("expected error for unknown type")
	}
}

func TestValidateSchemaDef_UnknownRole(t *testing.T) {
	fields, _ := json.Marshal([]smeldr.SchemaField{{Name: "X", Type: "string", Role: "headline"}})
	schema := &smeldr.ContentTypeSchema{TypeName: "x", Fields: json.RawMessage(fields)}
	if err := smeldr.ValidateSchemaDef(schema); err == nil {
		t.Error("expected error for unknown role")
	}
}

func TestValidateSchemaDef_MissingFieldName(t *testing.T) {
	fields, _ := json.Marshal([]smeldr.SchemaField{{Name: "", Type: "string"}})
	schema := &smeldr.ContentTypeSchema{TypeName: "x", Fields: json.RawMessage(fields)}
	if err := smeldr.ValidateSchemaDef(schema); err == nil {
		t.Error("expected error for empty field name")
	}
}

func TestValidateSchemaDef_InvalidFieldsJSON(t *testing.T) {
	schema := &smeldr.ContentTypeSchema{TypeName: "x", Fields: json.RawMessage(`{not json`)}
	if err := smeldr.ValidateSchemaDef(schema); err == nil {
		t.Error("expected error for invalid JSON in fields")
	}
}

func TestValidateSchemaDef_AllKnownTypes(t *testing.T) {
	for _, typ := range []string{"string", "integer", "boolean", "array", "object", "number"} {
		fields, _ := json.Marshal([]smeldr.SchemaField{{Name: "F", Type: typ}})
		schema := &smeldr.ContentTypeSchema{TypeName: "x", Fields: json.RawMessage(fields)}
		if err := smeldr.ValidateSchemaDef(schema); err != nil {
			t.Errorf("type %q should be valid, got: %v", typ, err)
		}
	}
}

func TestValidateSchemaDef_URLPrefix_BadFormat(t *testing.T) {
	schema := &smeldr.ContentTypeSchema{TypeName: "post", URLPrefix: "no-leading-slash"}
	if err := smeldr.ValidateSchemaDef(schema); err == nil {
		t.Fatal("expected error for URLPrefix without leading slash")
	}
}

func TestValidateSchemaDef_URLPrefix_Valid(t *testing.T) {
	schema := &smeldr.ContentTypeSchema{TypeName: "post", URLPrefix: "/posts"}
	if err := smeldr.ValidateSchemaDef(schema); err != nil {
		t.Fatalf("ValidateSchemaDef with valid URLPrefix: %v", err)
	}
}

// — SchemaStore.AllByKind + Save + MigrateSchemaKindColumn ——————————————————

func TestSchemaStore_SaveAndAllByKind(t *testing.T) {
	db := openDynDB(t)
	store := smeldr.NewSchemaStore(db)
	ctx := context.Background()

	schema := recipeSchema()
	if err := store.Save(ctx, schema); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if schema.ID == "" {
		t.Error("Save should set ID")
	}

	schemas, err := store.AllByKind(ctx, "content")
	if err != nil {
		t.Fatalf("AllByKind: %v", err)
	}
	if len(schemas) != 1 {
		t.Fatalf("expected 1 content schema, got %d", len(schemas))
	}
	if schemas[0].TypeName != "recipe" {
		t.Errorf("TypeName = %q, want \"recipe\"", schemas[0].TypeName)
	}
}

func TestSchemaStore_AllByKind_Block(t *testing.T) {
	db := openDynDB(t)
	store := smeldr.NewSchemaStore(db)
	ctx := context.Background()

	store.Save(ctx, recipeSchema())

	blocks, _ := store.AllByKind(ctx, "block")
	if len(blocks) != 0 {
		t.Errorf("expected 0 block schemas before seed, got %d", len(blocks))
	}
}

func TestSchemaStore_Save_Upsert(t *testing.T) {
	db := openDynDB(t)
	store := smeldr.NewSchemaStore(db)
	ctx := context.Background()

	schema := recipeSchema()
	store.Save(ctx, schema)

	schema.Label = "Updated Recipe"
	store.Save(ctx, schema)

	schemas, _ := store.AllByKind(ctx, "content")
	if len(schemas) != 1 {
		t.Fatalf("expected 1 row after upsert, got %d", len(schemas))
	}
	if schemas[0].Label != "Updated Recipe" {
		t.Errorf("Label = %q, want \"Updated Recipe\"", schemas[0].Label)
	}
}

func TestMigrateSchemaKindColumn_Idempotent(t *testing.T) {
	db := openDynDB(t)
	if err := smeldr.MigrateSchemaKindColumn(db); err != nil {
		t.Errorf("MigrateSchemaKindColumn second call: %v", err)
	}
	if err := smeldr.MigrateSchemaKindColumn(db); err != nil {
		t.Errorf("MigrateSchemaKindColumn third call: %v", err)
	}
}
