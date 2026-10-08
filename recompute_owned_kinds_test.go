// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
)

// newOwnedKindsEnv is an App with relations and provenance wired, an
// "article" schema whose edge field "references" (kind references, article
// to source) owns that kind, and a second kind "tagged" no field owns.
func newOwnedKindsEnv(t *testing.T) (*App, *RelationStore, *sql.DB) {
	t.Helper()
	store, db := newHistoryStore(t) // registers tagged (article -> tag)
	if err := CreateSchemaTable(db); err != nil {
		t.Fatalf("CreateSchemaTable: %v", err)
	}
	app := New(Config{BaseURL: "http://localhost", Secret: []byte("test-secret-key!!"), DB: db})
	app.Relations(store)
	upsertTestKind(t, store, "references", "article", "source")
	fields, _ := json.Marshal([]SchemaField{{Name: "references", Type: "string", Relation: "edge"}})
	if err := NewSchemaStore(db).Save(context.Background(), &ContentTypeSchema{
		TypeName: "article", Kind: "content", Fields: json.RawMessage(fields),
	}); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return app, store, db
}

// saveArticle runs the save hook for article art-1 whose references field names
// target ("" for none).
func saveArticle(t *testing.T, app *App, target string) {
	t.Helper()
	m := map[string]any{}
	if target != "" {
		m["references"] = target
	}
	raw, _ := json.Marshal(m)
	dn := &DynamicNode{Node: Node{ID: "art-1"}, TypeName: "article", Fields: json.RawMessage(raw)}
	if err := app.syncSaveHook(histCtx("editor-1"), "article", "art-1", dn); err != nil {
		t.Fatalf("save hook: %v", err)
	}
}

func liveOf(t *testing.T, store *RelationStore, kind string) []RelationEdge {
	t.Helper()
	live, err := store.GetLiveBySource(context.Background(), "article", "art-1", kind)
	if err != nil {
		t.Fatal(err)
	}
	return live
}

// A relation of a kind no field owns, asserted by hand, survives every save.
func TestSaveHook_KeepsHandAssertedOtherKind(t *testing.T) {
	app, store, db := newOwnedKindsEnv(t)
	if err := store.Assert(histCtx("alice"), tagEdge()); err != nil {
		t.Fatal(err)
	}
	saveArticle(t, app, "src-1")
	saveArticle(t, app, "src-2")

	tagged := liveOf(t, store, "tagged")
	if len(tagged) != 1 {
		t.Fatalf("live tagged = %+v; want the hand-asserted edge kept", tagged)
	}
	if got := verbs(edgeRecords(t, db, tagged[0].ID)); got != "assert:" {
		t.Errorf("hand-asserted edge records = %s; want no invalidate", got)
	}
	if refs := liveOf(t, store, "references"); len(refs) != 1 || refs[0].TargetID != "src-2" {
		t.Errorf("live references = %+v; want the field's src-2", refs)
	}
}

// Within the kind a field owns, the field decides: a hand-assert of that kind
// to another target is ended at the next save.
func TestSaveHook_FieldOwnsItsKind(t *testing.T) {
	app, store, db := newOwnedKindsEnv(t)
	hand := RelationEdge{SourceType: "article", SourceID: "art-1", TargetType: "source", TargetID: "src-hand", RelationKind: "references", EdgeClass: "asserted"}
	if err := store.Assert(histCtx("alice"), hand); err != nil {
		t.Fatal(err)
	}
	saveArticle(t, app, "src-1")

	refs := liveOf(t, store, "references")
	if len(refs) != 1 || refs[0].TargetID != "src-1" {
		t.Fatalf("live references = %+v; want only the field's src-1", refs)
	}
	all, _ := store.GetBySource(context.Background(), "article", "art-1", "references")
	for _, e := range all {
		if e.TargetID == "src-hand" {
			if got := verbs(edgeRecords(t, db, e.ID)); got != "assert:,invalidate:recomputed" {
				t.Errorf("hand-assert records = %s; want it ended as recomputed", got)
			}
		}
	}
}

// Clearing the field ends the derived edge and leaves the other kind alone.
func TestSaveHook_FieldClearedEndsDerived(t *testing.T) {
	app, store, _ := newOwnedKindsEnv(t)
	if err := store.Assert(histCtx("alice"), tagEdge()); err != nil {
		t.Fatal(err)
	}
	saveArticle(t, app, "src-1")
	saveArticle(t, app, "")
	if refs := liveOf(t, store, "references"); len(refs) != 0 {
		t.Errorf("live references = %+v; want none after the field was cleared", refs)
	}
	if tagged := liveOf(t, store, "tagged"); len(tagged) != 1 {
		t.Errorf("live tagged = %+v; want the hand-asserted edge kept", tagged)
	}
}

// failQueryDB fails every query, to prove an empty kind set runs none.
type failQueryDB struct{ DB }

func (failQueryDB) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("no query expected")
}

func TestRecomputeAssertedKinds_EmptyKindsNoQuery(t *testing.T) {
	store, _ := newHistoryStore(t)
	store.db = failQueryDB{store.db}
	if err := store.recomputeAssertedKinds(context.Background(), "article", "art-1", nil, nil); err != nil {
		t.Errorf("empty kinds = %v; want nil and no query", err)
	}
	if err := store.recomputeAssertedKinds(context.Background(), "article", "art-1", []string{"references"}, nil); err == nil {
		t.Error("a query error with kinds = nil; want it returned")
	}
}

// The exported RecomputeAsserted keeps its whole-set contract: every other
// live asserted row of the source is ended, whatever its kind.
func TestRecomputeAsserted_StillWholeSet(t *testing.T) {
	store, _ := newHistoryStore(t)
	if err := store.Assert(histCtx("alice"), tagEdge()); err != nil {
		t.Fatal(err)
	}
	if err := store.RecomputeAsserted(histCtx("alice"), "article", "art-1", nil); err != nil {
		t.Fatal(err)
	}
	if live, _ := store.GetLiveBySource(context.Background(), "article", "art-1", ""); len(live) != 0 {
		t.Errorf("live = %+v; want the whole set ended", live)
	}
}
