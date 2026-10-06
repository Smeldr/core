//go:build integration

package pgx

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// Runtime-defined (dynamic) content types on Postgres. Before core v1.119.2 every
// read failed ("unsupported Scan, storing driver.Value type string into type
// *json.RawMessage"), so none of this worked, and the Locked check on a dynamic
// type (A306) and the state machine on one (D103) were never exercised there.

func dynamicApp(t *testing.T, db smeldr.DB, flow *smeldr.StateFlow) (*smeldr.App, *smeldr.DynamicTypeRepo) {
	t.Helper()
	app, _ := newPGApp(t, db)
	ctx := context.Background()
	if err := smeldr.CreateBlockTables(db); err != nil {
		t.Fatalf("CreateBlockTables: %v", err)
	}
	if err := smeldr.CreateSchemaTable(db); err != nil {
		t.Fatalf("CreateSchemaTable: %v", err)
	}
	fields, err := json.Marshal([]smeldr.SchemaField{{Name: "Title", Type: "string", Required: true, Role: "title"}})
	if err != nil {
		t.Fatal(err)
	}
	desc, err := app.DefineContentType(ctx, &smeldr.ContentTypeSchema{TypeName: "memo", Kind: "content", Fields: fields})
	if err != nil {
		t.Fatalf("DefineContentType: %v", err)
	}
	if flow != nil {
		flow.TypeName = desc.Name
		if err := app.RegisterFlow(*flow); err != nil {
			t.Fatalf("RegisterFlow: %v", err)
		}
	}
	repo, err := app.DynamicContentRepo(desc.Name)
	if err != nil {
		t.Fatalf("DynamicContentRepo: %v", err)
	}
	return app, repo
}

func titleOf(t *testing.T, n *smeldr.DynamicNode) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(n.Fields, &m); err != nil {
		t.Fatalf("fields %q: %v", n.Fields, err)
	}
	s, _ := m["Title"].(string)
	return s
}

func TestPG_DynamicContent_ReadsAndWrites(t *testing.T) {
	db, _ := isolatedDB(t)
	_, repo := dynamicApp(t, db, nil)
	ctx := context.Background()
	const title = "Smørrebrød og kaffe ☕ — «første»"

	node, err := repo.CreateDraft(ctx, map[string]any{"Title": title})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	byID, err := repo.GetByID(ctx, node.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got := titleOf(t, byID); got != title {
		t.Errorf("GetByID title = %q, want %q", got, title)
	}
	bySlug, err := repo.GetBySlug(ctx, node.Slug)
	if err != nil || bySlug.ID != node.ID {
		t.Fatalf("GetBySlug = %v, %v", bySlug, err)
	}

	listed, err := repo.List(ctx, smeldr.ListOptions{})
	if err != nil || len(listed) != 1 {
		t.Fatalf("List = %v, %v, want one item", listed, err)
	}
	// What a reader gets is a decoded object, never a quoted string or base64.
	if got, ok := listed[0]["Title"].(string); !ok || got != title {
		t.Errorf("List item Title = %#v, want the string %q", listed[0]["Title"], title)
	}

	if err := repo.UpdateFields(ctx, node.ID, map[string]any{"Title": title + " (rettet)"}); err != nil {
		t.Fatalf("UpdateFields: %v", err)
	}
	// The stored column is JSON text, not a bytea hex string.
	var typ, stored string
	if err := db.QueryRowContext(ctx, `SELECT pg_typeof(fields)::text, fields FROM smeldr_dynamic_content WHERE id = $1`, node.ID).Scan(&typ, &stored); err != nil {
		t.Fatalf("read the column: %v", err)
	}
	var back map[string]any
	if typ != "text" || json.Unmarshal([]byte(stored), &back) != nil || back["Title"] != title+" (rettet)" {
		t.Errorf("stored column is %s %q, want text holding the merged JSON", typ, stored)
	}
	after, err := repo.GetByID(ctx, node.ID)
	if err != nil || titleOf(t, after) != title+" (rettet)" {
		t.Errorf("GetByID after the update = %v, %v", after, err)
	}
}

func TestPG_DynamicContent_StateMachine(t *testing.T) {
	db, _ := isolatedDB(t)
	app, repo := dynamicApp(t, db, &smeldr.StateFlow{
		Name: "memo-flow",
		States: []smeldr.State{
			{Name: "draft", IsInitial: true},
			{Name: "sealed", Locked: true},
			{Name: "gone", IsTerminal: true},
		},
		Transitions: []smeldr.Transition{{From: "draft", To: "sealed"}, {From: "sealed", To: "gone"}},
	})
	ctx := context.Background()
	node, err := repo.CreateDraft(ctx, map[string]any{"Title": "first"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}

	if err := repo.UpdateFields(ctx, node.ID, map[string]any{"Title": "edited"}); err != nil {
		t.Fatalf("an item in a state that is not locked must be editable: %v", err)
	}
	if err := repo.SetStatus(ctx, node.ID, "gone"); !errors.Is(err, smeldr.ErrConflict) {
		t.Errorf("draft -> gone is not in the flow: err = %v, want ErrConflict", err)
	}
	if err := repo.SetStatus(ctx, node.ID, "sealed"); err != nil {
		t.Fatalf("draft -> sealed: %v", err)
	}
	// The A306 Locked check on a dynamic type, on Postgres.
	if err := repo.UpdateFields(ctx, node.ID, map[string]any{"Title": "tampered"}); !errors.Is(err, smeldr.ErrConflict) {
		t.Errorf("an item in a locked state: err = %v, want ErrConflict", err)
	}
	got, err := repo.GetByID(ctx, node.ID)
	if err != nil || titleOf(t, got) != "edited" || string(got.Status) != "sealed" {
		t.Errorf("after the refused edit: %v, %v, want title edited, status sealed", got, err)
	}
	if _, err := app.TransitionItem(ctx, "memo", node.Slug, "gone"); err != nil {
		t.Errorf("App.TransitionItem on a dynamic type: %v", err)
	}

	other, err := repo.CreateDraft(ctx, map[string]any{"Title": "second"})
	if err != nil {
		t.Fatalf("CreateDraft 2: %v", err)
	}
	if err := repo.ScheduleContent(ctx, other.ID, time.Now().Add(time.Hour)); err == nil {
		t.Error("draft -> scheduled is not in this flow: want an error")
	}
}

func supersedeMemoFlow() *smeldr.StateFlow {
	return &smeldr.StateFlow{
		Name:           "memo-supersede",
		ActiveState:    "published",
		ConflictPolicy: smeldr.ConflictSupersede,
		States:         []smeldr.State{{Name: "draft", IsInitial: true}, {Name: "published"}, {Name: "superseded"}},
		Transitions:    []smeldr.Transition{{From: "draft", To: "published"}, {From: "published", To: "superseded"}},
	}
}

// TestPG_DynamicContent_Supersede: the dynamic repo's winner and loser writes share a
// transaction (A411), so the loser becomes superseded when the winner publishes, and a
// loser write the server refuses leaves nothing half done.
func TestPG_DynamicContent_Supersede(t *testing.T) {
	db, _ := isolatedDB(t)
	_, repo := dynamicApp(t, db, supersedeMemoFlow())
	ctx := context.Background()
	a, _ := repo.CreateDraft(ctx, map[string]any{"Title": "a"})
	b, _ := repo.CreateDraft(ctx, map[string]any{"Title": "b"})
	if err := repo.SetStatus(ctx, a.ID, "published"); err != nil {
		t.Fatalf("a -> published: %v", err)
	}
	if err := repo.SetStatus(ctx, b.ID, "published"); err != nil {
		t.Fatalf("b -> published: %v", err)
	}
	status := func(n *smeldr.DynamicNode) string {
		got, err := repo.GetByID(ctx, n.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		return string(got.Status)
	}
	if status(a) != "superseded" || status(b) != "published" {
		t.Errorf("a = %q, b = %q, want superseded and published", status(a), status(b))
	}
}

func TestPG_DynamicContent_SupersedeARefusedLoserLeavesNothingHalfDone(t *testing.T) {
	db, _ := isolatedDB(t)
	_, repo := dynamicApp(t, db, supersedeMemoFlow())
	ctx := context.Background()
	a, _ := repo.CreateDraft(ctx, map[string]any{"Title": "a"})
	b, _ := repo.CreateDraft(ctx, map[string]any{"Title": "b"})
	if err := repo.SetStatus(ctx, a.ID, "published"); err != nil {
		t.Fatalf("a -> published: %v", err)
	}
	failUpdatesOf(t, db, "smeldr_dynamic_content", a.ID)

	var err error
	out := captureLog(func() { err = repo.SetStatus(ctx, b.ID, "published") })
	if !errors.Is(err, smeldr.ErrInternal) {
		t.Errorf("a refused loser write: err = %v, want ErrInternal", err)
	}
	for id, want := range map[string]string{a.ID: "published", b.ID: "draft"} {
		got, gerr := repo.GetByID(ctx, id)
		if gerr != nil || string(got.Status) != want {
			t.Errorf("%s = %v, %v, want status %q: the change is half done", id, got, gerr, want)
		}
	}
	if !strings.Contains(out, "supersede UPDATE failed") {
		t.Errorf("the refused loser write must be logged:\n%s", out)
	}
}

func TestPG_SeedBlockTypeSchemas(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateSchemaTable(db); err != nil {
		t.Fatalf("CreateSchemaTable: %v", err)
	}
	for i := 0; i < 2; i++ { // the second call must change nothing and not fail
		if err := smeldr.SeedBlockTypeSchemas(db); err != nil {
			t.Fatalf("SeedBlockTypeSchemas (call %d): %v", i+1, err)
		}
	}
	if _, err := db.ExecContext(ctx, `UPDATE smeldr_content_type_schemas SET label = 'Customised' WHERE type_name = 'content_block'`); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.SeedBlockTypeSchemas(db); err != nil {
		t.Fatalf("a third seed: %v", err)
	}
	all, err := smeldr.NewSchemaStore(db).All(ctx)
	if err != nil {
		t.Fatalf("SchemaStore.All: %v", err)
	}
	if len(all) != 16 {
		t.Fatalf("%d schemas, want 16", len(all))
	}
	for _, s := range all {
		var fields []map[string]any
		if json.Unmarshal(s.Fields, &fields) != nil {
			t.Errorf("%s: fields %q are not a JSON array", s.TypeName, s.Fields)
		}
		if s.TypeName == "content_block" && s.Label != "Customised" {
			t.Errorf("content_block label = %q: the seed overwrote a customised row", s.Label)
		}
	}
}
