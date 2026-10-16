//go:build integration

package pgx

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	smeldr "smeldr.dev/core"
)

// D112/D114 on Postgres: a field's max_length round-trips through the schema
// store, and a dynamic create returns its title and length warnings.
func TestPG_DynamicFieldLimits(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateBlockTables(db); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.CreateSchemaTable(db); err != nil {
		t.Fatal(err)
	}
	app := smeldr.New(smeldr.Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	app.ServeDynamicContent()
	if _, err := app.DefineContentType(ctx, &smeldr.ContentTypeSchema{TypeName: "ticket",
		Fields: json.RawMessage(`[{"name":"title","type":"string","role":"title"},{"name":"summary","type":"string","role":"summary","max_length":10}]`)}); err != nil {
		t.Fatal(err)
	}
	stored, err := smeldr.NewSchemaStore(db).FindByTypeName(ctx, "ticket")
	if err != nil {
		t.Fatal(err)
	}
	fields, err := stored.ParseFields()
	if err != nil || len(fields) != 2 || fields[1].MaxLength != 10 {
		t.Fatalf("stored fields = %+v %v; want max_length 10 kept", fields, err)
	}
	repo, err := app.DynamicContentRepo("ticket")
	if err != nil {
		t.Fatal(err)
	}
	node, ws, err := repo.CreateDraftWithWarnings(ctx, "test", map[string]any{"summary": strings.Repeat("s", 11)})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, w := range ws {
		got[w.Code] = w.Address == node.Address && node.Address != ""
	}
	if !got["title_missing"] || !got["field_too_long"] {
		t.Errorf("warnings = %+v; want title_missing and field_too_long with the address", ws)
	}
	ws, err = repo.UpdateFieldsWithWarnings(ctx, "test", node.ID, map[string]any{"title": "named"})
	if err != nil || len(ws) != 0 {
		t.Errorf("clean update: %+v %v", ws, err)
	}
}
