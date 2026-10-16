package smeldr

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

// Titles and field limits on runtime-defined types (D112, D114).

func dynamicTitleApp(t *testing.T, cutover time.Time) *App {
	t.Helper()
	db := newSQLiteDB(t)
	if err := CreateBlockTables(db); err != nil {
		t.Fatal(err)
	}
	if err := CreateSchemaTable(db); err != nil {
		t.Fatal(err)
	}
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db, TitleCutover: cutover})
	app.ServeDynamicContent()
	return app
}

func defineTicket(t *testing.T, app *App) *DynamicTypeRepo {
	t.Helper()
	if _, err := app.DefineContentType(context.Background(), &ContentTypeSchema{TypeName: "ticket",
		Fields: json.RawMessage(`[{"name":"Title","type":"string","role":"title"},{"name":"Summary","type":"string","role":"summary","max_length":10},{"name":"Body","type":"string"}]`)}); err != nil {
		t.Fatal(err)
	}
	repo, err := app.DynamicContentRepo("ticket")
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func codes(ws []CreateWarning) map[string]string {
	out := map[string]string{}
	for _, w := range ws {
		out[w.Code] = w.Field
	}
	return out
}

func TestSchemaMaxLength_Validation(t *testing.T) {
	for name, fields := range map[string]string{
		"non-string":      `[{"name":"N","type":"integer","max_length":3}]`,
		"negative":        `[{"name":"S","type":"string","max_length":-1}]`,
		"title above 120": `[{"name":"T","type":"string","role":"title","max_length":121}]`,
	} {
		err := ValidateSchemaDef(&ContentTypeSchema{TypeName: "x", Kind: "content", Fields: json.RawMessage(fields)})
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("%s: %v; want a validation error", name, err)
		}
	}
	if err := ValidateSchemaDef(&ContentTypeSchema{TypeName: "x", Kind: "content",
		Fields: json.RawMessage(`[{"name":"T","type":"string","role":"title","max_length":80},{"name":"S","type":"string","max_length":300}]`)}); err != nil {
		t.Errorf("valid limits: %v", err)
	}
}

func TestDefine_TitleFieldDeclared(t *testing.T) {
	untitled := func() *ContentTypeSchema {
		return &ContentTypeSchema{TypeName: "plain_record", Fields: json.RawMessage(`[{"name":"Body","type":"string"}]`)}
	}
	before := dynamicTitleApp(t, time.Time{})
	s := untitled()
	if _, err := before.DefineContentType(context.Background(), s); err != nil {
		t.Fatalf("before the cutover: %v", err)
	}
	if got := codes(TitleFieldWarnings(s)); got["title_field_missing"] != "fields" {
		t.Errorf("TitleFieldWarnings = %v", got)
	}
	after := dynamicTitleApp(t, time.Now().Add(-time.Hour))
	var ve *ValidationError
	if _, err := after.DefineContentType(context.Background(), untitled()); !errors.As(err, &ve) || !strings.Contains(err.Error(), "title") {
		t.Errorf("after the cutover: %v; want a validation error", err)
	}
	// A title field that is not a string does not count.
	if ws := TitleFieldWarnings(&ContentTypeSchema{TypeName: "r", Kind: "content", Fields: json.RawMessage(`[{"name":"T","type":"integer","role":"title"}]`)}); len(ws) != 1 {
		t.Errorf("integer title: %v", ws)
	}
	if ws := TitleFieldWarnings(nil); ws != nil {
		t.Errorf("nil schema: %v", ws)
	}
	if ws := TitleFieldWarnings(&ContentTypeSchema{TypeName: "r", Kind: "content", Fields: json.RawMessage(`not json`)}); ws != nil {
		t.Errorf("unreadable fields: %v", ws)
	}
}

func TestDefine_WebContentUnaffected(t *testing.T) {
	app := dynamicTitleApp(t, time.Now().Add(-time.Hour))
	s := &ContentTypeSchema{TypeName: "page", URLPrefix: "/pages", Fields: json.RawMessage(`[{"name":"Body","type":"string"}]`)}
	if _, err := app.DefineContentType(context.Background(), s); err != nil {
		t.Errorf("web content without a title field after the cutover: %v", err)
	}
	if ws := TitleFieldWarnings(s); ws != nil {
		t.Errorf("web content warned: %v", ws)
	}
	repo, err := app.DynamicContentRepo("page")
	if err != nil {
		t.Fatal(err)
	}
	if _, ws, err := repo.CreateDraftWithWarnings(context.Background(), "", map[string]any{"Body": "x"}); err != nil || ws != nil {
		t.Errorf("web content create: %v %v", ws, err)
	}
}

func TestRedefine_TitleFieldNotEnforced(t *testing.T) {
	app := dynamicTitleApp(t, time.Time{})
	if _, err := app.DefineContentType(context.Background(), &ContentTypeSchema{TypeName: "legacy_rec", Fields: json.RawMessage(`[{"name":"Body","type":"string"}]`)}); err != nil {
		t.Fatal(err)
	}
	app.cfg.TitleCutover = time.Now().Add(-time.Hour)
	desc, err := app.RedefineContentTypeVia(context.Background(), "mcp", &ContentTypeSchema{TypeName: "legacy_rec",
		Fields: json.RawMessage(`[{"name":"Body","type":"string"},{"name":"Summary","type":"string","role":"summary","max_length":300}]`)}, "add summary")
	if err != nil {
		t.Fatalf("redefine after the cutover without a title field: %v", err)
	}
	if ws := TitleFieldWarnings(desc.Schema); len(ws) != 1 {
		t.Errorf("TitleFieldWarnings after redefine = %v", ws)
	}
}

func TestDynamicTitles_Create(t *testing.T) {
	repo := defineTicket(t, dynamicTitleApp(t, time.Time{}))
	ctx := context.Background()
	cases := []struct {
		fields map[string]any
		want   map[string]string
	}{
		{map[string]any{"Body": "x"}, map[string]string{"title_missing": "Title"}},
		{map[string]any{"Title": "  "}, map[string]string{"title_missing": "Title"}},
		{map[string]any{"Title": strings.Repeat("a", 121)}, map[string]string{"title_too_long": "Title"}},
		{map[string]any{"Title": strings.Repeat("é", 120)}, map[string]string{}},
		{map[string]any{"Title": "ok", "Summary": "eleven char"}, map[string]string{"field_too_long": "Summary"}},
	}
	for i, c := range cases {
		node, ws, err := repo.CreateDraftWithWarnings(ctx, "mcp", c.fields)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		got := codes(ws)
		if len(got) != len(c.want) {
			t.Errorf("case %d: warnings %v; want %v", i, got, c.want)
		}
		for code, field := range c.want {
			if got[code] != field {
				t.Errorf("case %d: warnings %v; want %s on %s", i, got, code, field)
			}
		}
		for _, w := range ws {
			if w.Address == "" || w.Address != node.Address {
				t.Errorf("case %d: warning address %q, item %q", i, w.Address, node.Address)
			}
		}
	}
}

func TestDynamicTitles_Update(t *testing.T) {
	repo := defineTicket(t, dynamicTitleApp(t, time.Time{}))
	ctx := context.Background()
	node, _, err := repo.CreateDraftWithWarnings(ctx, "", map[string]any{"Body": "untitled"})
	if err != nil {
		t.Fatal(err)
	}
	if ws, err := repo.UpdateFieldsWithWarnings(ctx, "", node.ID, map[string]any{"Body": "edited"}); err != nil || ws != nil {
		t.Errorf("update not touching the title: %v %v", ws, err)
	}
	ws, err := repo.UpdateFieldsWithWarnings(ctx, "", node.ID, map[string]any{"Title": strings.Repeat("t", 130), "Summary": strings.Repeat("s", 11)})
	if err != nil {
		t.Fatal(err)
	}
	if got := codes(ws); got["title_too_long"] != "Title" || got["field_too_long"] != "Summary" {
		t.Errorf("update warnings = %v", got)
	}
	stored, err := repo.GetByID(ctx, node.ID)
	if err != nil || !strings.Contains(string(stored.Fields), strings.Repeat("t", 130)) {
		t.Errorf("a warned update was not saved: %s %v", stored.Fields, err)
	}
}

func TestDynamicTitles_AfterCutover(t *testing.T) {
	app := dynamicTitleApp(t, time.Time{})
	repo := defineTicket(t, app)
	ctx := context.Background()
	node, _, err := repo.CreateDraftWithWarnings(ctx, "", map[string]any{"Title": "named"})
	if err != nil {
		t.Fatal(err)
	}
	app.cfg.TitleCutover = time.Now().Add(-time.Hour)
	repo, err = app.DynamicContentRepo("ticket")
	if err != nil {
		t.Fatal(err)
	}
	var ve *ValidationError
	if _, _, err := repo.CreateDraftWithWarnings(ctx, "", map[string]any{"Body": "x"}); !errors.As(err, &ve) || !strings.Contains(err.Error(), TitleGuidance) {
		t.Errorf("create without a title: %v", err)
	}
	if _, err := repo.UpdateFieldsWithWarnings(ctx, "", node.ID, map[string]any{"Summary": strings.Repeat("s", 11)}); !errors.As(err, &ve) {
		t.Errorf("update with a long field: %v", err)
	}
	stored, err := repo.GetByID(ctx, node.ID)
	if err != nil || strings.Contains(string(stored.Fields), "sssssssssss") {
		t.Errorf("a refused update was written: %s %v", stored.Fields, err)
	}
	// The old API refuses the same way.
	if _, err := repo.CreateDraftVia(ctx, "", map[string]any{"Body": "x"}); !errors.As(err, &ve) {
		t.Errorf("CreateDraftVia after the cutover: %v", err)
	}
	if err := repo.UpdateFieldsVia(ctx, "", node.ID, map[string]any{"Title": ""}); !errors.As(err, &ve) {
		t.Errorf("UpdateFieldsVia after the cutover: %v", err)
	}
}

func TestDynamicTitles_OldAPI(t *testing.T) {
	repo := defineTicket(t, dynamicTitleApp(t, time.Time{}))
	ctx := context.Background()
	node, err := repo.CreateDraftVia(ctx, "", map[string]any{"Body": "x"})
	if err != nil || node == nil {
		t.Fatalf("CreateDraftVia: %v", err)
	}
	if err := repo.UpdateFieldsVia(ctx, "", node.ID, map[string]any{"Title": strings.Repeat("x", 200)}); err != nil {
		t.Errorf("UpdateFieldsVia with a warning: %v", err)
	}
	if err := repo.UpdateFieldsVia(ctx, "", "no-such-id", map[string]any{"Title": "x"}); err == nil {
		t.Error("update of a missing item: want an error")
	}
	if err := repo.UpdateFieldsVia(ctx, "", node.ID, map[string]any{"Nope": "x"}); err == nil {
		t.Error("update with an unknown field: want an error")
	}
}

func TestDynamicTitles_HTTP(t *testing.T) {
	call := func(app *App, h http.Handler, method, path, body string, values map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		for k, v := range values {
			r.SetPathValue(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	auth := authFuncFor(User{ID: "admin", Roles: []Role{Admin}})
	app := dynamicTitleApp(t, time.Time{})
	defineTicket(t, app)
	w := call(app, newCreateContentHandler(app, auth), http.MethodPost, "/_content/ticket", `{"Body":"x"}`, map[string]string{"type": "ticket"})
	if w.Code != http.StatusCreated || strings.Contains(w.Body.String(), "warnings") {
		t.Fatalf("HTTP create without a title = %d %s; want 201, no warnings key", w.Code, w.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	id, _ := created["ID"].(string)
	if w := call(app, newCreateContentHandler(app, auth), http.MethodPost, "/_content/ticket", `{"Nope":"x"}`, map[string]string{"type": "ticket"}); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("HTTP create with an unknown field = %d; want 422", w.Code)
	}
	app.cfg.TitleCutover = time.Now().Add(-time.Hour)
	if w := call(app, newCreateContentHandler(app, auth), http.MethodPost, "/_content/ticket", `{"Body":"x"}`, map[string]string{"type": "ticket"}); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("HTTP create without a title after the cutover = %d %s; want 422", w.Code, w.Body.String())
	}
	if w := call(app, newUpdateContentHandler(app, auth), http.MethodPatch, "/_content/ticket/"+id, `{"Summary":"far too long"}`, map[string]string{"type": "ticket", "id": id}); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("HTTP update with a long field after the cutover = %d %s; want 422", w.Code, w.Body.String())
	}
	if w := call(app, newUpdateContentHandler(app, auth), http.MethodPatch, "/_content/ticket/nope", `{"Body":"y"}`, map[string]string{"type": "ticket", "id": "nope"}); w.Code != http.StatusNotFound {
		t.Errorf("HTTP update of a missing item = %d; want 404", w.Code)
	}
}

func TestDynamicFieldWarnings_EdgeCases(t *testing.T) {
	if ws := dynamicFieldWarnings(nil, nil, nil); ws != nil {
		t.Errorf("nil schema: %v", ws)
	}
	bad := &ContentTypeSchema{TypeName: "r", Kind: "content", Fields: json.RawMessage(`not json`)}
	if ws := dynamicFieldWarnings(bad, map[string]any{}, func(string) bool { return true }); ws != nil {
		t.Errorf("unreadable schema: %v", ws)
	}
	// A title role on web content is not held to the record rule.
	web := &ContentTypeSchema{TypeName: "w", Kind: "content", URLPrefix: "/w", Fields: json.RawMessage(`[{"name":"T","type":"string","role":"title"}]`)}
	if ws := dynamicFieldWarnings(web, map[string]any{}, func(string) bool { return true }); ws != nil {
		t.Errorf("web content title: %v", ws)
	}
	// A declared title limit below 120 applies.
	short := &ContentTypeSchema{TypeName: "s", Kind: "content", Fields: json.RawMessage(`[{"name":"T","type":"string","role":"title","max_length":5}]`)}
	if got := codes(dynamicFieldWarnings(short, map[string]any{"T": "longer"}, func(string) bool { return true })); got["title_too_long"] != "T" {
		t.Errorf("declared title limit: %v", got)
	}
}

func TestCapabilityWarnings_DynamicCodes(t *testing.T) {
	var app *App
	if !slices.Contains(app.CapabilityWarnings(ResourceItem, VerbCreate), "field_too_long") || !slices.Contains(app.CapabilityWarnings(ResourceItem, VerbUpdate), "field_too_long") {
		t.Error("field_too_long is not registered for item create and update")
	}
	if !slices.Contains(app.CapabilityWarnings(ResourceType, "define"), "title_field_missing") {
		t.Error("title_field_missing is not registered for type define")
	}
}
