//go:build integration

package pgx

import (
	"context"
	"encoding/json"
	"testing"

	smeldr "smeldr.dev/core"
)

// A runtime-defined type is redefined on Postgres: the new optional field is
// stored and read back, the creation time kept, and a removal is refused.
func TestPG_RedefineContentType(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	for _, create := range []func(smeldr.DB) error{smeldr.CreateBlockTables, smeldr.CreateSchemaTable} {
		if err := create(db); err != nil {
			t.Fatal(err)
		}
	}
	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte(pgTestSecret), DB: db})
	base := []smeldr.SchemaField{{Name: "Title", Type: "string", Required: true, Role: "title"}, {Name: "band", Type: "string"}}
	fields, _ := json.Marshal(base)
	if _, err := app.DefineContentType(ctx, &smeldr.ContentTypeSchema{TypeName: "pg_memo", Kind: "content", Fields: fields}); err != nil {
		t.Fatalf("DefineContentType: %v", err)
	}
	before, err := smeldr.NewSchemaStore(db).FindByTypeName(ctx, "pg_memo")
	if err != nil {
		t.Fatal(err)
	}
	more, _ := json.Marshal(append(base, smeldr.SchemaField{Name: "note", Type: "string"}))
	if _, err := app.RedefineContentType(ctx, &smeldr.ContentTypeSchema{TypeName: "pg_memo", Fields: more}, "add note"); err != nil {
		t.Fatalf("RedefineContentType: %v", err)
	}
	after, err := smeldr.NewSchemaStore(db).FindByTypeName(ctx, "pg_memo")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := after.ParseFields()
	if len(parsed) != 3 || parsed[2].Name != "note" || !after.CreatedAt.Equal(before.CreatedAt) {
		t.Errorf("stored = %+v created %v; want three fields and the original creation time %v", parsed, after.CreatedAt, before.CreatedAt)
	}
	less, _ := json.Marshal(base[:1])
	if _, err := app.RedefineContentType(ctx, &smeldr.ContentTypeSchema{TypeName: "pg_memo", Fields: less}, ""); err == nil {
		t.Error("a removal was accepted")
	}
}
