package smeldr

import (
	"context"
	"encoding/json"
	"testing"
)

// switchoverSQLiteA453 is the SQLite statement A453's deployment step gives
// for routing task_plan on band, word for word. json(f) around the aggregate
// input is required: the CASE result comes out of a subquery column, which
// drops SQLite's JSON subtype, and without json() json_group_array stores
// every element as a quoted string. That first form failed live on
// process.smeldr.dev (2026-10-08).
const switchoverSQLiteA453 = `UPDATE smeldr_content_type_schemas SET fields = (SELECT json_group_array(json(f)) FROM (SELECT CASE WHEN json_extract(value, '$.name') = 'band' THEN json_set(value, '$.role', 'channel') ELSE json(value) END AS f FROM json_each(smeldr_content_type_schemas.fields) ORDER BY key)) WHERE type_name = 'task_plan'`

// TestSwitchoverSQL_KeepsFieldObjects runs A453's switch-over statement on a
// task_plan schema row and checks the read-back the way the deployment step
// must: every element of fields is still a JSON object (json_type), band
// carries the channel role, the other fields are unchanged, and a fresh App
// loads the type routed on band.
func TestSwitchoverSQL_KeepsFieldObjects(t *testing.T) {
	db := newSQLiteDB(t)
	for _, create := range []func(DB) error{CreateBlockTables, CreateSchemaTable} {
		if err := create(db); err != nil {
			t.Fatal(err)
		}
	}
	original := []SchemaField{
		{Name: "task_ref", Type: "string", Required: true},
		{Name: "band", Type: "string", Required: true},
		{Name: "body", Type: "string", Role: "body"},
		{Name: "revision", Type: "integer"},
	}
	fields, _ := json.Marshal(original)
	app := New(MustConfig(Config{BaseURL: "https://example.com", Secret: []byte(transitionItemTestSecret), DB: db}))
	if _, err := app.DefineContentType(context.Background(), &ContentTypeSchema{TypeName: "task_plan", Fields: fields}); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(switchoverSQLiteA453); err != nil {
		t.Fatalf("switch-over: %v", err)
	}

	rows, err := db.Query(`SELECT json_type(value), json_extract(value, '$.name') FROM smeldr_content_type_schemas, json_each(fields) WHERE type_name = 'task_plan' ORDER BY key`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var typ, name string
		if err := rows.Scan(&typ, &name); err != nil {
			t.Fatalf("element %d: %v (an element that is not an object has no name)", n, err)
		}
		if typ != "object" {
			t.Errorf("element %d (%s) json_type = %q; want object", n, name, typ)
		}
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if n != len(original) {
		t.Fatalf("elements = %d; want %d", n, len(original))
	}

	s, err := NewSchemaStore(db).FindByTypeName(context.Background(), "task_plan")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ParseFields()
	if err != nil {
		t.Fatalf("stored fields unreadable: %v", err)
	}
	want := append([]SchemaField(nil), original...)
	want[1].Role = "channel"
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("field %d = %+v; want %+v", i, got[i], want[i])
		}
	}

	reloaded := New(MustConfig(Config{BaseURL: "https://example.com", Secret: []byte(transitionItemTestSecret), DB: db}))
	reloaded.ServeDynamicContent()
	reloaded.Handler()
	if d := reloaded.typeRegistry.Lookup("task_plan"); d == nil || routeField(d.Schema) != "band" {
		t.Fatalf("after reload: %+v; want task_plan routed on band", d)
	}
}
