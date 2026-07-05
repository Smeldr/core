package smeldr_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

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
	// Schema declares a "name" field with no title role — slug should fall back to "item".
	schema := &smeldr.ContentTypeSchema{
		TypeName: "tag",
		Kind:     "content",
		Fields:   json.RawMessage(`[{"name":"name","type":"string"}]`),
	}
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

// — ValidateFields integration (via DynamicTypeRepo) ——————————————————————————

func TestCreateDraft_ValidateFields_UnknownField(t *testing.T) {
	db := openDynDB(t)
	schema := recipeSchema()
	repo := smeldr.NewDynamicTypeRepo(db, schema.TypeName, schema)
	_, err := repo.CreateDraft(context.Background(), map[string]any{
		"Title":   "Pasta",
		"unknown": "oops",
	})
	if err == nil {
		t.Fatal("expected validation error for unknown field")
	}
}

func TestCreateDraft_ValidateFields_MissingRequired(t *testing.T) {
	db := openDynDB(t)
	schema := recipeSchema()
	repo := smeldr.NewDynamicTypeRepo(db, schema.TypeName, schema)
	// recipeSchema has required "Title" field.
	_, err := repo.CreateDraft(context.Background(), map[string]any{"Body": "content"})
	if err == nil {
		t.Fatal("expected validation error for missing required field")
	}
}

func TestCreateDraft_ValidateFields_WrongType(t *testing.T) {
	db := openDynDB(t)
	schema := recipeSchema()
	repo := smeldr.NewDynamicTypeRepo(db, schema.TypeName, schema)
	// "Title" is string; pass an integer.
	_, err := repo.CreateDraft(context.Background(), map[string]any{"Title": float64(42)})
	if err == nil {
		t.Fatal("expected validation error for wrong type on Title field")
	}
}

func TestUpdateFields_ValidatePartialFields_UnknownField(t *testing.T) {
	db := openDynDB(t)
	schema := recipeSchema()
	repo := smeldr.NewDynamicTypeRepo(db, schema.TypeName, schema)
	node, err := repo.CreateDraft(context.Background(), map[string]any{"Title": "Pasta"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	err = repo.UpdateFields(context.Background(), node.ID, map[string]any{"unknown": "x"})
	if err == nil {
		t.Fatal("expected validation error for unknown field in patch")
	}
}

func TestUpdateFields_ValidatePartialFields_MissingRequired_OK(t *testing.T) {
	db := openDynDB(t)
	schema := recipeSchema()
	repo := smeldr.NewDynamicTypeRepo(db, schema.TypeName, schema)
	node, err := repo.CreateDraft(context.Background(), map[string]any{"Title": "Pasta"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	// Partial update omitting required "Title" is OK — it's already stored.
	if err := repo.UpdateFields(context.Background(), node.ID, map[string]any{"Body": "updated body"}); err != nil {
		t.Fatalf("partial update missing required should succeed: %v", err)
	}
}

// — DynamicTypeRepo.ScheduleContent ——————————————————————————————————————————

func TestDynamicTypeRepo_ScheduleContent_HappyPath(t *testing.T) {
	db := openDynDB(t)
	schema := recipeSchema()
	repo := smeldr.NewDynamicTypeRepo(db, schema.TypeName, schema)
	node, err := repo.CreateDraft(context.Background(), map[string]any{"Title": "Pasta"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	scheduledAt := time.Now().UTC().Add(24 * time.Hour)
	if err := repo.ScheduleContent(context.Background(), node.ID, scheduledAt); err != nil {
		t.Fatalf("ScheduleContent: %v", err)
	}
	updated, err := repo.GetByID(context.Background(), node.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if updated.Status != smeldr.Scheduled {
		t.Errorf("status = %q, want %q", updated.Status, smeldr.Scheduled)
	}
}

// — App.DynamicContentRepo WithGovernance wire ————————————————————————————————

func TestDynamicContentRepo_WithGovernance_NilGovernance(t *testing.T) {
	// When the app has no governance, DynamicContentRepo returns an unwired repo.
	// Verified indirectly: CreateDraft succeeds without a RoleStore.
	cfg := smeldr.Config{
		BaseURL: "https://example.com",
		Secret:  []byte("test-secret-minimum16bytes"),
		DB:      openDynDB(t),
	}
	app := smeldr.New(cfg)
	app.ServeDynamicContent()
	ctx := context.Background()
	schema := recipeSchema()
	if _, err := app.DefineContentType(ctx, schema); err != nil {
		t.Fatalf("DefineContentType: %v", err)
	}
	repo, err := app.DynamicContentRepo(schema.TypeName)
	if err != nil {
		t.Fatalf("DynamicContentRepo: %v", err)
	}
	if _, err := repo.CreateDraft(ctx, map[string]any{"Title": "Test"}); err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
}

// — DynamicTypeRepo.ScheduleContent error paths ——————————————————————————————

func TestDynamicTypeRepo_ScheduleContent_NotFound(t *testing.T) {
	db := openDynDB(t)
	schema := recipeSchema()
	repo := smeldr.NewDynamicTypeRepo(db, schema.TypeName, schema)
	err := repo.ScheduleContent(context.Background(), "no-such-id", time.Now().Add(time.Hour))
	if err == nil {
		t.Error("ScheduleContent with nonexistent ID: expected error, got nil")
	}
}

// TestDynamicTypeRepo_ScheduleContent_WithSmeldrContext covers the actorID branch
// inside ScheduleContent when the context implements smeldr.Context (User().ID is set).
func TestDynamicTypeRepo_ScheduleContent_WithSmeldrContext(t *testing.T) {
	db := openDynDB(t)
	schema := recipeSchema()
	repo := smeldr.NewDynamicTypeRepo(db, schema.TypeName, schema)
	node, err := repo.CreateDraft(context.Background(), map[string]any{"Title": "Focaccia"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	ctx := smeldr.NewTestContext(smeldr.User{ID: "editor-1", Roles: []smeldr.Role{smeldr.Editor}})
	if err := repo.ScheduleContent(ctx, node.ID, time.Now().UTC().Add(48*time.Hour)); err != nil {
		t.Fatalf("ScheduleContent with TestContext: %v", err)
	}
	updated, err := repo.GetByID(context.Background(), node.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if updated.Status != smeldr.Scheduled {
		t.Errorf("status = %q, want scheduled", updated.Status)
	}
}

// TestDynamicTypeRepo_SetStatus_WithSmeldrContext covers the actorID branch
// inside SetStatus when ctx implements smeldr.Context.
func TestDynamicTypeRepo_SetStatus_WithSmeldrContext(t *testing.T) {
	db := openDynDB(t)
	schema := recipeSchema()
	repo := smeldr.NewDynamicTypeRepo(db, schema.TypeName, schema)
	node, err := repo.CreateDraft(context.Background(), map[string]any{"Title": "Ciabatta"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	ctx := smeldr.NewTestContext(smeldr.User{ID: "editor-2", Roles: []smeldr.Role{smeldr.Editor}})
	if err := repo.SetStatus(ctx, node.ID, smeldr.Published); err != nil {
		t.Fatalf("SetStatus with TestContext: %v", err)
	}
	updated, err := repo.GetByID(context.Background(), node.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if updated.Status != smeldr.Published {
		t.Errorf("status = %q, want published", updated.Status)
	}
}

// TestDynamicContentRepo_WithGovernance_Wired covers the governance wire branch
// in DynamicContentRepo: when App.governance != nil, repo.WithGovernance is called.
func TestDynamicContentRepo_WithGovernance_Wired(t *testing.T) {
	db := openDynDB(t)
	cfg := smeldr.Config{
		BaseURL: "https://example.com",
		Secret:  []byte("test-secret-minimum16bytes"),
		DB:      db,
	}
	app := smeldr.New(cfg)
	store := smeldr.NewRoleStore(db)
	if err := app.Governance(store); err != nil {
		t.Fatalf("Governance: %v", err)
	}
	app.ServeDynamicContent()

	ctx := context.Background()
	schema := recipeSchema()
	if _, err := app.DefineContentType(ctx, schema); err != nil {
		t.Fatalf("DefineContentType: %v", err)
	}
	repo, err := app.DynamicContentRepo(schema.TypeName)
	if err != nil {
		t.Fatalf("DynamicContentRepo with governance wired: %v", err)
	}
	// Verify the governed repo is functional.
	if _, err := repo.CreateDraft(ctx, map[string]any{"Title": "Sourdough"}); err != nil {
		t.Fatalf("CreateDraft via governed repo: %v", err)
	}
}

// — App.RefreshContentIndex ———————————————————————————————————————————————————

func TestRefreshContentIndex_UnknownType_NoOp(t *testing.T) {
	cfg := smeldr.Config{
		BaseURL: "https://example.com",
		Secret:  []byte("test-secret-minimum16bytes"),
		DB:      openDynDB(t),
	}
	app := smeldr.New(cfg)
	// Should not panic for unknown type.
	app.RefreshContentIndex(context.Background(), "nonexistent")
}

// TestRefreshContentIndex_NoPrefix_NoOp covers the early-return branch when the
// registered type has an empty URLPrefix (admin-only type).
func TestRefreshContentIndex_NoPrefix_NoOp(t *testing.T) {
	db := openDynDB(t)
	cfg := smeldr.Config{
		BaseURL: "https://example.com",
		Secret:  []byte("test-secret-minimum16bytes"),
		DB:      db,
	}
	app := smeldr.New(cfg)
	app.ServeDynamicContent()
	ctx := context.Background()

	// Admin-only type: no URLPrefix.
	noPrefix := &smeldr.ContentTypeSchema{
		TypeName: "internal_note",
		Fields:   json.RawMessage(`[{"name":"Title","type":"string","required":true}]`),
	}
	if _, err := app.DefineContentType(ctx, noPrefix); err != nil {
		t.Fatalf("DefineContentType: %v", err)
	}
	// Must not panic; early-returns because Prefix is empty.
	app.RefreshContentIndex(ctx, "internal_note")
}

// dynAIContent is a minimal content type used to trigger llmsStore wiring
// on an App via app.Content(smeldr.NewModule(..., smeldr.AIIndex(smeldr.LLMsTxt))).
type dynAIContent struct {
	smeldr.Node
	Title string `smeldr:"required"`
}

// TestRefreshContentIndex_WithLLMsStore covers the full path of RefreshContentIndex
// including rebuildDynamicAIIndex when llmsStore is wired. Uses a module with
// AIIndex to trigger llmsStore initialisation on the App, then defines a
// dynamic content type with a URL prefix and publishes an item so the rebuild
// has a non-empty result to process.
func TestRefreshContentIndex_WithLLMsStore(t *testing.T) {
	db := openDynDB(t)
	cfg := smeldr.Config{
		BaseURL: "https://example.com",
		Secret:  []byte("test-secret-minimum16bytes"),
		DB:      db,
	}
	app := smeldr.New(cfg)

	// Register a module with AIIndex so that App.llmsStore is initialised.
	aiRepo := smeldr.NewMemoryRepo[*dynAIContent]()
	m := smeldr.NewModule((*dynAIContent)(nil), smeldr.Repo(aiRepo), smeldr.AIIndex(smeldr.LLMsTxt))
	app.Content(m)
	app.ServeDynamicContent()

	ctx := context.Background()
	// Define a content type with URLPrefix and both title + description roles.
	eventFields, _ := json.Marshal([]smeldr.SchemaField{
		{Name: "Title", Type: "string", Required: true, Role: "title"},
		{Name: "Summary", Type: "string", Role: "description"},
	})
	schema := &smeldr.ContentTypeSchema{
		TypeName:  "tevent",
		Label:     "Test Event",
		URLPrefix: "/tevents",
		Fields:    json.RawMessage(eventFields),
	}
	if _, err := app.DefineContentType(ctx, schema); err != nil {
		t.Fatalf("DefineContentType: %v", err)
	}

	// Create and publish an item so the rebuild has real content to process.
	repo, err := app.DynamicContentRepo("tevent")
	if err != nil {
		t.Fatalf("DynamicContentRepo: %v", err)
	}
	node, err := repo.CreateDraft(ctx, map[string]any{"Title": "Annual Gala", "Summary": "The big event"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if err := repo.SetStatus(ctx, node.ID, smeldr.Published); err != nil {
		t.Fatalf("SetStatus Published: %v", err)
	}

	// RefreshContentIndex must not panic and must cover the full rebuild path.
	app.RefreshContentIndex(ctx, "tevent")

	// Create a second item with empty Title so the title-fallback-to-slug branch
	// in rebuildDynamicAIIndex is covered (e.Title == "" → e.Title = slug).
	node2, err := repo.CreateDraft(ctx, map[string]any{"Title": "", "Summary": ""})
	if err == nil {
		if err2 := repo.SetStatus(ctx, node2.ID, smeldr.Published); err2 == nil {
			app.RefreshContentIndex(ctx, "tevent")
		}
	}
}

// — ScheduleContent transition blocked ———————————————————————————————————————

// TestDynamicTypeRepo_ScheduleContent_TransitionBlocked registers a custom flow
// for "recipe" that only allows draft→published, blocking draft→scheduled.
// ScheduleContent must return ErrConflict — covers the validateTransition error
// return path in ScheduleContent (dynamic.go line 261).
func TestDynamicTypeRepo_ScheduleContent_TransitionBlocked(t *testing.T) {
	db := openDynDB(t)
	cfg := smeldr.Config{
		BaseURL: "https://example.com",
		Secret:  []byte("test-secret-minimum16bytes"),
		DB:      db,
	}
	app := smeldr.New(cfg)

	if err := app.RegisterFlow(smeldr.StateFlow{
		Name:     "recipe-no-sched",
		TypeName: "recipe",
		States: []smeldr.State{
			{Name: "draft", IsInitial: true},
			{Name: "published", IsTerminal: true},
		},
		Transitions: []smeldr.Transition{
			{From: "draft", To: "published"},
		},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}

	schema := recipeSchema()
	repo := smeldr.NewDynamicTypeRepo(db, schema.TypeName, schema)
	node, err := repo.CreateDraft(context.Background(), map[string]any{"Title": "Pasta"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}

	err = repo.ScheduleContent(context.Background(), node.ID, time.Now().Add(time.Hour))
	if err == nil {
		t.Fatal("expected ErrConflict for blocked transition, got nil")
	}
	if !errors.Is(err, smeldr.ErrConflict) {
		t.Errorf("expected ErrConflict, got %v", err)
	}
}

// — loadDynamicTypes llmsStore branch ————————————————————————————————————————

// TestLoadDynamicTypes_WithLLMsStore verifies that loadDynamicTypes (called from
// Handler()) hits the llmsStore branch when a schema with URLPrefix is pre-saved
// to DB before the App is created and a module with AIIndex has wired llmsStore.
// Covers dynamic.go lines 461-464.
func TestLoadDynamicTypes_WithLLMsStore(t *testing.T) {
	db := openDynDB(t)
	ctx := context.Background()

	// Pre-save a schema with URLPrefix directly via SchemaStore — bypasses
	// DefineContentType so the type is not in typeRegistry when loadDynamicTypes runs.
	fields, _ := json.Marshal([]smeldr.SchemaField{
		{Name: "Title", Type: "string", Required: true, Role: "title"},
	})
	schema := &smeldr.ContentTypeSchema{
		TypeName:  "prearticle",
		Label:     "Pre-Article",
		URLPrefix: "/prearticles",
		Kind:      "content",
		Fields:    json.RawMessage(fields),
	}
	if err := smeldr.NewSchemaStore(db).Save(ctx, schema); err != nil {
		t.Fatalf("Save schema: %v", err)
	}

	cfg := smeldr.Config{
		BaseURL: "https://example.com",
		Secret:  []byte("test-secret-minimum16bytes"),
		DB:      db,
	}
	app := smeldr.New(cfg)

	// Register a module with AIIndex — this sets a.llmsStore on the App.
	aiRepo := smeldr.NewMemoryRepo[*dynAIContent]()
	m := smeldr.NewModule((*dynAIContent)(nil), smeldr.Repo(aiRepo), smeldr.AIIndex(smeldr.LLMsTxt))
	app.Content(m)
	app.ServeDynamicContent()

	// Handler() calls loadDynamicTypes which finds the pre-saved schema,
	// registers it, and hits the llmsStore branch (a.llmsStore != nil).
	_ = app.Handler()
}

// — rebuildDynamicSitemap and rebuildDynamicAIIndex empty-slug paths ——————————

// TestRebuildIndex_EmptySlugItems inserts a published item with empty slug into
// the DB and calls RefreshContentIndex. Both rebuildDynamicSitemap and
// rebuildDynamicAIIndex encounter the item and execute "if slug == "" { continue }",
// covering dynamic.go lines 821-822 and 861-862.
func TestRebuildIndex_EmptySlugItems(t *testing.T) {
	db := openDynDB(t)
	ctx := context.Background()

	cfg := smeldr.Config{
		BaseURL: "https://example.com",
		Secret:  []byte("test-secret-minimum16bytes"),
		DB:      db,
	}
	app := smeldr.New(cfg)

	// Wire llmsStore (needed by rebuildDynamicAIIndex).
	aiRepo := smeldr.NewMemoryRepo[*dynAIContent]()
	m := smeldr.NewModule((*dynAIContent)(nil), smeldr.Repo(aiRepo), smeldr.AIIndex(smeldr.LLMsTxt))
	app.Content(m)

	// ServeDynamicContent sets sitemapStore (needed by rebuildDynamicSitemap).
	app.ServeDynamicContent()

	schema := recipeSchema()
	if _, err := app.DefineContentType(ctx, schema); err != nil {
		t.Fatalf("DefineContentType: %v", err)
	}

	// Insert a published item with empty slug directly — CreateDraft always
	// generates a slug, so raw SQL is required to exercise the slug=="" guard.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_dynamic_content
		 (id, type_name, slug, status, fields, created_at, updated_at, published_at, scheduled_at, rev)
		 VALUES ($1, 'recipe', '', 'published', '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, NULL, 0)`,
		smeldr.NewID(),
	); err != nil {
		t.Fatalf("insert empty-slug item: %v", err)
	}

	// RefreshContentIndex calls rebuildDynamicSitemap + rebuildDynamicAIIndex
	// synchronously; both skip the empty-slug item via "if slug == "" { continue }".
	app.RefreshContentIndex(ctx, "recipe")
}
