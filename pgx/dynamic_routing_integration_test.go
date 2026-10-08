//go:build integration

package pgx

import (
	"context"
	"encoding/json"
	"testing"

	smeldr "smeldr.dev/core"
)

// A runtime-defined type's "channel" role is stored with its fields on
// Postgres and read back, so its events route there as on SQLite.
func TestPG_DynamicChannelRole(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	for _, create := range []func(smeldr.DB) error{smeldr.CreateBlockTables, smeldr.CreateSchemaTable} {
		if err := create(db); err != nil {
			t.Fatal(err)
		}
	}
	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte(pgTestSecret), DB: db})
	fields, _ := json.Marshal([]smeldr.SchemaField{
		{Name: "Title", Type: "string", Required: true, Role: "title"},
		{Name: "band", Type: "string", Role: "channel"},
	})
	desc, err := app.DefineContentType(ctx, &smeldr.ContentTypeSchema{TypeName: "routed_plan", Kind: "content", Fields: fields})
	if err != nil {
		t.Fatalf("DefineContentType: %v", err)
	}
	stored, err := smeldr.NewSchemaStore(db).FindByTypeName(ctx, desc.Name)
	if err != nil {
		t.Fatalf("FindByTypeName: %v", err)
	}
	parsed, err := stored.ParseFields()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range parsed {
		if f.Name == "band" && f.Role == "channel" {
			found = true
		}
	}
	if !found {
		t.Errorf("stored fields %+v; want band with role channel", parsed)
	}
}
