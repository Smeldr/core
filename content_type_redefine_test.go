// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// redefineEnv is an App with one runtime-defined type "memo" (Title required,
// band, count), provenance and the event stream.
type redefineEnv struct {
	app  *App
	db   *sql.DB
	prov *fakeProvenanceStore
}

func memoFields(extra ...SchemaField) json.RawMessage {
	fields := append([]SchemaField{
		{Name: "Title", Type: "string", Required: true, Role: "title"},
		{Name: "band", Type: "string"},
		{Name: "count", Type: "integer"},
	}, extra...)
	b, _ := json.Marshal(fields)
	return b
}

func newRedefineEnv(t *testing.T) *redefineEnv {
	t.Helper()
	db := newSQLiteDB(t)
	for _, create := range []func(DB) error{CreateBlockTables, CreateSchemaTable} {
		if err := create(db); err != nil {
			t.Fatal(err)
		}
	}
	app := New(MustConfig(Config{BaseURL: "https://example.com", Secret: []byte(transitionItemTestSecret), DB: db}))
	prov := &fakeProvenanceStore{}
	app.Provenance(prov)
	app.EventStream()
	if _, err := app.DefineContentType(context.Background(), &ContentTypeSchema{TypeName: "memo", Label: "Memo", Fields: memoFields()}); err != nil {
		t.Fatalf("DefineContentType: %v", err)
	}
	return &redefineEnv{app: app, db: db, prov: prov}
}

func (e *redefineEnv) storedFields(t *testing.T) string {
	t.Helper()
	var label, f string
	if err := e.db.QueryRow(`SELECT label, fields FROM smeldr_content_type_schemas WHERE type_name = 'memo'`).Scan(&label, &f); err != nil {
		t.Fatal(err)
	}
	return label + "|" + f
}

func fieldsOf(fs ...SchemaField) json.RawMessage {
	b, _ := json.Marshal(fs)
	return b
}

// TestRedefineContentType_Compatibility: a redefinition may loosen and add,
// never take away or tighten; a refused one changes neither the registry nor
// the store.
func TestRedefineContentType_Compatibility(t *testing.T) {
	title := SchemaField{Name: "Title", Type: "string", Required: true, Role: "title"}
	band := SchemaField{Name: "band", Type: "string"}
	count := SchemaField{Name: "count", Type: "integer"}
	cases := []struct {
		name   string
		schema ContentTypeSchema
		want   string // "" = accepted; else a substring of the error
	}{
		{"label", ContentTypeSchema{Label: "Notes", Fields: memoFields()}, ""},
		{"role", ContentTypeSchema{Fields: fieldsOf(title, SchemaField{Name: "band", Type: "string", Role: "channel"}, count)}, ""},
		{"format and description", ContentTypeSchema{Fields: fieldsOf(title, SchemaField{Name: "band", Type: "string", Format: "slug", Description: "the band"}, count)}, ""},
		{"required to optional", ContentTypeSchema{Fields: fieldsOf(SchemaField{Name: "Title", Type: "string", Role: "title"}, band, count)}, ""},
		{"new optional field", ContentTypeSchema{Fields: memoFields(SchemaField{Name: "note", Type: "string"})}, ""},
		{"removal", ContentTypeSchema{Fields: fieldsOf(title, band)}, "count: cannot be removed"},
		{"retype", ContentTypeSchema{Fields: fieldsOf(title, band, SchemaField{Name: "count", Type: "string"})}, "count: type cannot change"},
		{"optional to required", ContentTypeSchema{Fields: fieldsOf(title, SchemaField{Name: "band", Type: "string", Required: true}, count)}, "band: cannot become required"},
		{"new required field", ContentTypeSchema{Fields: memoFields(SchemaField{Name: "note", Type: "string", Required: true})}, "note: a new field must be optional"},
		{"prefix set", ContentTypeSchema{URLPrefix: "/memos", Fields: memoFields()}, "url_prefix"},
		{"invalid schema", ContentTypeSchema{Fields: fieldsOf(title, band, count, SchemaField{Name: "x", Type: "nope"})}, "unknown type"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newRedefineEnv(t)
			before := e.app.typeRegistry.Lookup("memo")
			stored := e.storedFields(t)
			c.schema.TypeName = "memo"
			d, err := e.app.RedefineContentType(hookCtx(), &c.schema, "")
			if c.want != "" {
				if err == nil || !strings.Contains(err.Error(), c.want) {
					t.Fatalf("err = %v; want %q", err, c.want)
				}
				if e.app.typeRegistry.Lookup("memo") != before || e.storedFields(t) != stored {
					t.Error("a refused redefinition changed the registry or the store")
				}
				return
			}
			if err != nil {
				t.Fatalf("RedefineContentType: %v", err)
			}
			if got := e.app.typeRegistry.Lookup("memo"); got != d || got.Schema.ID != before.Schema.ID || !got.Schema.CreatedAt.Equal(before.Schema.CreatedAt) {
				t.Errorf("registry = %+v; want the new descriptor with the stored ID and creation time", got)
			}
			if e.storedFields(t) == stored {
				t.Error("store not updated")
			}
		})
	}
}

// TestRedefineContentType_TakesEffectWithoutRestart: a new role routes, a new
// field validates, the label is kept when omitted, and one provenance record
// and one types-topic event are written.
func TestRedefineContentType_TakesEffectWithoutRestart(t *testing.T) {
	e := newRedefineEnv(t)
	types, _ := e.app.eventBroadcaster.subscribe("w-types", eventStreamChannelTypes)
	core, _ := e.app.eventBroadcaster.subscribe("w-core", "core")
	t.Cleanup(func() { e.app.eventBroadcaster.unsubscribe(types); e.app.eventBroadcaster.unsubscribe(core) })

	if got := e.app.eventChannels("memo", &DynamicNode{Fields: json.RawMessage(`{"band":"core"}`)}); got != nil {
		t.Fatalf("before: channels = %v; want a broadcast", got)
	}
	fields := fieldsOf(
		SchemaField{Name: "Title", Type: "string", Required: true, Role: "title"},
		SchemaField{Name: "band", Type: "string", Role: "channel"},
		SchemaField{Name: "count", Type: "integer"},
		SchemaField{Name: "note", Type: "string"},
	)
	d, err := e.app.RedefineContentTypeVia(hookCtx(), "mcp", &ContentTypeSchema{TypeName: "memo", Fields: fields}, "  route on band  ")
	if err != nil {
		t.Fatalf("RedefineContentTypeVia: %v", err)
	}
	if d.Schema.Label != "Memo" {
		t.Errorf("label = %q; want the stored one", d.Schema.Label)
	}
	if got := e.app.eventChannels("memo", &DynamicNode{Fields: json.RawMessage(`{"band":"core"}`)}); len(got) != 2 || got[0] != "core" {
		t.Errorf("after: channels = %v; want core and the type topic", got)
	}
	repo, err := e.app.DynamicContentRepo("memo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateDraft(context.Background(), map[string]any{"Title": "x", "note": "new field"}); err != nil {
		t.Errorf("new field not accepted: %v", err)
	}
	var recs []ProvenanceRecord
	for _, r := range e.prov.Appended() {
		if r.SubjectType == "ContentType" {
			recs = append(recs, r)
		}
	}
	if len(recs) != 1 || recs[0].SubjectID != "memo" || recs[0].Verb != "update" || recs[0].Reason != "route on band" || recs[0].Surface != "mcp" || recs[0].ActorID != "editor-1" {
		t.Errorf("records = %+v", recs)
	}
	ty, co := &routeSub{ch: types, got: map[string]int{}}, &routeSub{ch: core, got: map[string]int{}}
	ty.drain(t)
	co.drain(t)
	if ty.got[eventContentTypeRedefined+":"] != 1 || len(co.got) != 0 {
		t.Errorf("types %v, core %v; want one redefined event on the topic only", ty.got, co.got)
	}
}

// TestRedefineContentType_Refusals covers the guard paths.
func TestRedefineContentType_Refusals(t *testing.T) {
	e := newRedefineEnv(t)
	if _, err := e.app.RedefineContentType(hookCtx(), &ContentTypeSchema{TypeName: "nope", Fields: memoFields()}, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown type: %v", err)
	}
	if _, err := e.app.RedefineContentType(hookCtx(), nil, ""); err == nil {
		t.Error("nil schema accepted")
	}
	if _, err := e.app.RedefineContentType(hookCtx(), &ContentTypeSchema{TypeName: "memo", Fields: memoFields()}, strings.Repeat("r", 1001)); err == nil {
		t.Error("over-long reason accepted")
	}
	e.app.typeRegistry.Register(&TypeDescriptor{Name: "Compiled", Prefix: "/compiled", Kind: "compiled"})
	if _, err := e.app.RedefineContentType(hookCtx(), &ContentTypeSchema{TypeName: "Compiled", Fields: memoFields()}, ""); !errors.Is(err, ErrBadRequest) {
		t.Errorf("compiled type: %v", err)
	}
	if _, err := (&App{typeRegistry: newContentTypeRegistry()}).RedefineContentType(hookCtx(), &ContentTypeSchema{TypeName: "memo"}, ""); err == nil {
		t.Error("no DB accepted")
	}
	if _, err := e.app.RedefineContentType(hookCtx(), &ContentTypeSchema{TypeName: "memo"}, ""); err == nil || !strings.Contains(err.Error(), "cannot be removed") {
		t.Errorf("empty fields: %v; want every field reported as removed", err)
	}
}

// failSchemaSaveDB fails the schema upsert.
type failSchemaSaveDB struct{ DB }

func (f failSchemaSaveDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	if strings.Contains(q, "smeldr_content_type_schemas") {
		return nil, fmt.Errorf("disk full")
	}
	return f.DB.ExecContext(ctx, q, args...)
}

// TestRedefineContentType_SaveFailureKeepsRegistry: a failed save leaves the
// running App on the old schema.
func TestRedefineContentType_SaveFailureKeepsRegistry(t *testing.T) {
	e := newRedefineEnv(t)
	before := e.app.typeRegistry.Lookup("memo")
	e.app.cfg.DB = failSchemaSaveDB{e.db}
	if _, err := e.app.RedefineContentType(hookCtx(), &ContentTypeSchema{TypeName: "memo", Label: "Notes", Fields: memoFields()}, ""); err == nil {
		t.Fatal("save failure not returned")
	}
	if e.app.typeRegistry.Lookup("memo") != before {
		t.Error("registry changed after a failed save")
	}
}

// TestRedefineContentType_Concurrent: readers of the registry and a
// redefinition race-free (run under -race).
func TestRedefineContentType_Concurrent(t *testing.T) {
	e := newRedefineEnv(t)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if d := e.app.typeRegistry.Lookup("memo"); d != nil {
					_ = ValidateFields(d.Schema, map[string]any{"Title": "x"})
					_ = routeField(d.Schema)
				}
			}
		}()
	}
	for i := 0; i < 20; i++ {
		label := fmt.Sprintf("Memo %d", i)
		if _, err := e.app.RedefineContentType(hookCtx(), &ContentTypeSchema{TypeName: "memo", Label: label, Fields: memoFields()}, ""); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
}

// TestRedefineContentType_ConcurrentKeepsEveryField: concurrent redefinitions
// that each add a different field run one at a time. Exactly one wins; every
// other one is checked against the winner's schema and refused for removing
// its field, so no accepted field is silently dropped. Store and registry
// agree afterwards. (Run under -race.)
func TestRedefineContentType_ConcurrentKeepsEveryField(t *testing.T) {
	const writers = 8
	for round := 0; round < 5; round++ {
		e := newRedefineEnv(t)
		start := make(chan struct{})
		errs := make([]error, writers)
		var wg sync.WaitGroup
		for i := 0; i < writers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				fields := memoFields(SchemaField{Name: fmt.Sprintf("f%d", i), Type: "string"})
				_, errs[i] = e.app.RedefineContentType(hookCtx(), &ContentTypeSchema{TypeName: "memo", Fields: fields}, "")
			}()
		}
		close(start)
		wg.Wait()
		winner := -1
		for i, err := range errs {
			switch {
			case err == nil && winner == -1:
				winner = i
			case err == nil:
				t.Fatalf("round %d: redefinitions f%d and f%d both accepted; one field was dropped", round, winner, i)
			case !strings.Contains(err.Error(), "cannot be removed"):
				t.Fatalf("round %d: f%d refused with %v; want the winner's field reported as removed", round, i, err)
			}
		}
		if winner == -1 {
			t.Fatalf("round %d: no redefinition accepted", round)
		}
		want := string(memoFields(SchemaField{Name: fmt.Sprintf("f%d", winner), Type: "string"}))
		stored, err := NewSchemaStore(e.db).FindByTypeName(context.Background(), "memo")
		if err != nil {
			t.Fatal(err)
		}
		if string(stored.Fields) != want || string(e.app.typeRegistry.Lookup("memo").Schema.Fields) != want {
			t.Errorf("round %d: stored %s, registry %s; want both %s", round, stored.Fields, e.app.typeRegistry.Lookup("memo").Schema.Fields, want)
		}
	}
}

// TestRedefineContentType_PublicRoutesServeNewDescriptor: the public GET
// routes of a type with a prefix look the type up per request. After a
// redefinition the list route serves through the new descriptor and the item
// route through the new schema; an unregistered name is a 404.
func TestRedefineContentType_PublicRoutesServeNewDescriptor(t *testing.T) {
	e := newRedefineEnv(t)
	ctx := context.Background()
	title := SchemaField{Name: "Title", Type: "string", Required: true, Role: "title"}
	if _, err := e.app.DefineContentType(ctx, &ContentTypeSchema{TypeName: "pnote", URLPrefix: "/pnotes", Fields: fieldsOf(title)}); err != nil {
		t.Fatal(err)
	}
	h := e.app.Handler()
	get := func(path string) (int, string) {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w.Code, w.Body.String()
	}
	if _, err := e.app.RedefineContentType(hookCtx(), &ContentTypeSchema{TypeName: "pnote", URLPrefix: "/pnotes", Fields: fieldsOf(title, SchemaField{Name: "note", Type: "string"})}, ""); err != nil {
		t.Fatalf("RedefineContentType: %v", err)
	}

	e.app.typeRegistry.Lookup("pnote").Fetch = func(context.Context, ListOptions) ([]map[string]any, error) {
		return []map[string]any{{"from": "new descriptor"}}, nil
	}
	if code, body := get("/pnotes"); code != http.StatusOK || !strings.Contains(body, "new descriptor") {
		t.Errorf("list after redefine = %d %s; want it served through the new descriptor", code, body)
	}

	repo, err := e.app.DynamicContentRepo("pnote")
	if err != nil {
		t.Fatal(err)
	}
	node, err := repo.CreateDraft(ctx, map[string]any{"Title": "Hello", "note": "added field"})
	if err != nil {
		t.Fatalf("create with the new field: %v", err)
	}
	if err := repo.SetStatus(ctx, node.ID, Published); err != nil {
		t.Fatal(err)
	}
	if code, body := get("/pnotes/" + node.Slug); code != http.StatusOK || !strings.Contains(body, "added field") {
		t.Errorf("item after redefine = %d %s", code, body)
	}

	w := httptest.NewRecorder()
	serveDynamicList(w, httptest.NewRequest("GET", "/gone", nil), e.app, "gone")
	if w.Code != http.StatusNotFound {
		t.Errorf("list of an unregistered type = %d; want 404", w.Code)
	}
}

// TestRegistryReplace_Panics guards the registry's own rules.
func TestRegistryReplace_Panics(t *testing.T) {
	r := newContentTypeRegistry()
	r.Register(&TypeDescriptor{Name: "a", Prefix: "/a"})
	for _, d := range []*TypeDescriptor{{Name: "missing"}, {Name: "a", Prefix: "/b"}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("replace(%+v) did not panic", d)
				}
			}()
			r.replace(d)
		}()
	}
}

// TestSeedToolPolicies_RedefineRowOnExistingInstance: an instance whose policy
// table already exists, without the redefine row, gains it on the next boot's
// migration.
func TestSeedToolPolicies_RedefineRowOnExistingInstance(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := migrateGovernance(ctx, db); err != nil {
		t.Fatalf("first migration: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM smeldr_tool_policies WHERE tool_name = 'redefine_content_type'`); err != nil {
		t.Fatal(err)
	}
	if err := migrateGovernance(ctx, db); err != nil {
		t.Fatalf("second migration: %v", err)
	}
	var op string
	if err := db.QueryRow(`SELECT required_op FROM smeldr_tool_policies WHERE tool_name = 'redefine_content_type'`).Scan(&op); err != nil || op != "define-type" {
		t.Errorf("redefine row = %q, %v; want define-type", op, err)
	}
}
