package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// searchFixture is an App with the orchestration types, governance, a
// relations table and two runtime types: domain (a place) and note.
type searchFixture struct {
	app            *App
	db             *sql.DB
	rs             *RoleStore
	tasks          *Module[*Task]
	decisions      *Module[*Decision]
	signals        *Module[*Signal]
	global, reader Context
	seen, hidden   *Task
	placed, loose  *Decision
	domain         *DynamicNode
}

func newSearchFixture(t *testing.T) searchFixture {
	t.Helper()
	ctx := context.Background()
	db := addressDB(t)
	setupTokensTable(t, db)
	setupRelationsTable(t, db)
	if err := CreateBlockTables(db); err != nil {
		t.Fatal(err)
	}
	if err := CreateSchemaTable(db); err != nil {
		t.Fatal(err)
	}
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	RegisterOrchestrationTypes(app, db)
	app.ServeDynamicContent()
	rs := NewRoleStore(db)
	if err := app.Governance(rs); err != nil {
		t.Fatal(err)
	}
	_ = app.Handler()
	if err := RegisterScopedReaderRole(ctx, rs); err != nil {
		t.Fatal(err)
	}
	for _, s := range []*ContentTypeSchema{
		{TypeName: "domain", Fields: json.RawMessage(`[{"Name":"Title","Type":"string","Role":"title"}]`)},
		{TypeName: "note", Fields: json.RawMessage(`[{"Name":"Title","Type":"string","Role":"title"},{"Name":"Text","Type":"string","Format":"markdown"},{"Name":"Secret","Type":"string"}]`)},
	} {
		if _, err := app.DefineContentType(ctx, s); err != nil {
			t.Fatalf("define %s: %v", s.TypeName, err)
		}
	}
	f := searchFixture{app: app, db: db, rs: rs,
		tasks: findModule[*Task](t, app), decisions: findModule[*Decision](t, app), signals: findModule[*Signal](t, app)}
	seed := TrustedRead(authorCtx()).(Context)
	task := func(title, desc string) *Task {
		out, err := f.tasks.MCPCreate(seed, map[string]any{"Title": title, "Description": desc, "Band": "core"})
		if err != nil {
			t.Fatal(err)
		}
		return asItem[*Task](out)
	}
	f.seen = task("Apple pie recipe", "Bake the pie at 180 degrees")
	f.hidden = task("Apple tart", "Hidden from the reader")
	decision := func(title, body string) *Decision {
		out, err := f.decisions.MCPCreate(seed, map[string]any{"Title": title, "Body": body, "Scope": "core"})
		if err != nil {
			t.Fatal(err)
		}
		return asItem[*Decision](out)
	}
	f.placed = decision("Placed decision", "Use apple storage")
	f.loose = decision("Loose decision", "An imported ADR about pears")
	domains, err := app.DynamicContentRepo("domain")
	if err != nil {
		t.Fatal(err)
	}
	if f.domain, err = domains.CreateDraft(ctx, map[string]any{"Title": "Kitchen"}); err != nil {
		t.Fatal(err)
	}
	f.edge(t, "Decision", f.placed.ID, "domain", f.domain.ID, "belongs_to_domain", "asserted", nil)
	if _, err := rs.Grant(ctx, RoleGrant{TokenID: "reader", RoleName: "scoped-reader", ScopeStatic: []string{"Task:" + f.seen.ID, "domain:" + f.domain.ID}}); err != nil {
		t.Fatal(err)
	}
	govGrant(t, rs, "global", "admin")
	f.global = NewTestContext(User{ID: "global"})
	f.reader = NewTestContext(User{ID: "reader"})
	return f
}

func (f searchFixture) edge(t *testing.T, srcType, src, tgtType, tgt, kind, class string, invalid any) {
	t.Helper()
	if _, err := f.db.ExecContext(context.Background(),
		`INSERT INTO smeldr_relations (id, source_type, source_id, target_type, target_id, relation_kind, edge_class, invalid_at, attributes, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, '{}', datetime('now'), datetime('now'))`,
		NewID(), srcType, src, tgtType, tgt, kind, class, invalid); err != nil {
		t.Fatalf("insert edge: %v", err)
	}
}

func (f searchFixture) note(t *testing.T, fields map[string]any) *DynamicNode {
	t.Helper()
	repo, err := f.app.DynamicContentRepo("note")
	if err != nil {
		t.Fatal(err)
	}
	n, err := repo.CreateDraft(context.Background(), fields)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func hitIDs(res SearchResult) []string {
	ids := make([]string, len(res.Hits))
	for i, h := range res.Hits {
		ids[i] = h.ID
	}
	return ids
}

func hasHit(res SearchResult, id string) bool {
	for _, h := range res.Hits {
		if h.ID == id {
			return true
		}
	}
	return false
}

func TestSearch_TextAcrossTypes(t *testing.T) {
	f := newSearchFixture(t)
	n := f.note(t, map[string]any{"Title": "Shopping", "Text": "Buy an APPLE today"})
	res, err := f.app.Search(f.global, SearchQuery{Text: "apple"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{f.seen.ID, f.hidden.ID, f.placed.ID, n.ID} {
		if !hasHit(res, id) {
			t.Errorf("missing hit %s in %v", id, hitIDs(res))
		}
	}
	if hasHit(res, f.loose.ID) || res.Total != 4 {
		t.Errorf("total %d, hits %v; want the four apple records", res.Total, hitIDs(res))
	}
	for _, h := range res.Hits {
		switch h.ID {
		case f.seen.ID:
			if h.Type != "Task" || h.Field != "title" || h.Title != "Apple pie recipe" || h.Address == "" || h.State == "" {
				t.Errorf("task hit = %+v", h)
			}
		case n.ID:
			if h.Type != "note" || h.Field != "Text" || !strings.Contains(h.Snippet, "APPLE") || h.Title != "Shopping" {
				t.Errorf("note hit = %+v", h)
			}
		case f.placed.ID:
			if h.Field != "body" || h.Title != "Placed decision" {
				t.Errorf("decision hit = %+v", h)
			}
		}
	}
}

func TestSearch_HiddenNotCounted(t *testing.T) {
	f := newSearchFixture(t)
	res, err := f.app.Search(f.reader, SearchQuery{Text: "apple"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 1 || !hasHit(res, f.seen.ID) {
		t.Errorf("reader: total %d hits %v; want only the readable Task", res.Total, hitIDs(res))
	}
}

func TestSearch_SignalSubjectHidden(t *testing.T) {
	f := newSearchFixture(t)
	ctx := TrustedRead(authorCtx()).(Context)
	mk := func(subject string) *Signal {
		out, err := f.signals.MCPCreate(ctx, map[string]any{"sender": "architect", "receiver": "core", "signal_type": "question",
			"message": "about apples", "subject_type": "Task", "subject_id": subject})
		if err != nil {
			t.Fatal(err)
		}
		return asItem[*Signal](out)
	}
	visible, hidden := mk(f.seen.ID), mk(f.hidden.ID)
	if _, err := f.rs.Grant(context.Background(), RoleGrant{TokenID: "reader", RoleName: "scoped-reader",
		ScopeStatic: []string{"Signal:" + visible.ID, "Signal:" + hidden.ID}}); err != nil {
		t.Fatal(err)
	}
	res, err := f.app.Search(f.reader, SearchQuery{Text: "about apples", Types: []string{"Signal"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 1 || !hasHit(res, visible.ID) {
		t.Errorf("hits %v; want only the Signal whose subject is readable", hitIDs(res))
	}
	if h := res.Hits[0]; h.Title != "about apples" || h.Field != "message" {
		t.Errorf("signal hit = %+v; want the message as title", h)
	}
}

func TestSearch_Filters(t *testing.T) {
	f := newSearchFixture(t)
	cases := []struct {
		name string
		q    SearchQuery
		want []string
	}{
		{"type", SearchQuery{Text: "apple", Types: []string{"Decision"}}, []string{f.placed.ID}},
		{"place none", SearchQuery{Types: []string{"Decision"}, Place: SearchPlaceNone}, []string{f.loose.ID}},
		{"place by ID", SearchQuery{Place: f.domain.ID}, []string{f.placed.ID}},
		{"place by address", SearchQuery{Place: f.domain.Address}, []string{f.placed.ID}},
		{"state", SearchQuery{Text: "apple", Types: []string{"Task"}, States: []string{"no-such-state"}}, nil},
		{"duplicate types", SearchQuery{Text: "pears", Types: []string{"Decision", "Decision", " "}}, []string{f.loose.ID}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := f.app.Search(f.global, c.q)
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(hitIDs(res)) != fmt.Sprint(append([]string{}, c.want...)) || res.Total != len(c.want) {
				t.Errorf("hits %v total %d; want %v", hitIDs(res), res.Total, c.want)
			}
		})
	}
}

func TestSearch_StateFilterMatches(t *testing.T) {
	f := newSearchFixture(t)
	res, err := f.app.Search(f.global, SearchQuery{Text: "apple", Types: []string{"Task", "note"}, States: []string{string(f.seen.Status)}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 2 {
		t.Errorf("hits %v; want both apple Tasks", hitIDs(res))
	}
}

func TestSearch_PlaceEdgesThatDoNotCount(t *testing.T) {
	f := newSearchFixture(t)
	f.edge(t, "Decision", f.loose.ID, "domain", f.domain.ID, "belongs_to_domain", "inferred", nil)
	f.edge(t, "Decision", f.loose.ID, "domain", f.domain.ID, "belongs_to_domain", "asserted", time.Now().UTC().Add(-time.Hour))
	f.edge(t, "Decision", f.loose.ID, "domain", f.domain.ID, "relates_to", "asserted", nil)
	res, err := f.app.Search(f.global, SearchQuery{Types: []string{"Decision"}, Place: SearchPlaceNone})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 1 || !hasHit(res, f.loose.ID) {
		t.Errorf("hits %v; an inferred, ended or other-kind edge is no place", hitIDs(res))
	}
	f.edge(t, "Decision", f.loose.ID, "area", "some-area", "belongs_to_area", "asserted", nil)
	if res, _ := f.app.Search(f.global, SearchQuery{Types: []string{"Decision"}, Place: SearchPlaceNone}); res.Total != 0 {
		t.Errorf("an Area place: hits %v; want none unplaced", hitIDs(res))
	}
}

func TestSearch_PlaceHidden(t *testing.T) {
	f := newSearchFixture(t)
	repo, _ := f.app.DynamicContentRepo("domain")
	other, err := repo.CreateDraft(context.Background(), map[string]any{"Title": "Garage"})
	if err != nil {
		t.Fatal(err)
	}
	_, hiddenErr := f.app.Search(f.reader, SearchQuery{Place: other.ID})
	_, missingErr := f.app.Search(f.reader, SearchQuery{Place: "no-such-place"})
	if !errors.Is(hiddenErr, ErrNotFound) || !errors.Is(missingErr, ErrNotFound) {
		t.Errorf("hidden %v, missing %v; want not found for both", hiddenErr, missingErr)
	}
	if _, err := f.app.Search(f.reader, SearchQuery{Place: f.domain.ID}); err != nil {
		t.Errorf("readable place: %v", err)
	}
}

func TestSearch_BadQueries(t *testing.T) {
	f := newSearchFixture(t)
	for name, q := range map[string]SearchQuery{
		"empty":          {},
		"blank text":     {Text: "   "},
		"one rune":       {Text: "a"},
		"too long":       {Text: strings.Repeat("x", SearchTextMaxLength+1)},
		"negative limit": {Text: "apple", Limit: -1},
		"negative skip":  {Text: "apple", Offset: -1},
		"unknown type":   {Text: "apple", Types: []string{"Nope"}},
	} {
		if _, err := f.app.Search(f.global, q); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%s: %v; want bad request", name, err)
		}
	}
}

func TestSearch_Paging(t *testing.T) {
	f := newSearchFixture(t)
	all, err := f.app.Search(f.global, SearchQuery{Text: "apple"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := f.app.Search(f.global, SearchQuery{Text: "apple", Limit: 2, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != all.Total || len(page.Hits) != 2 || page.Hits[0].ID != all.Hits[1].ID {
		t.Errorf("page %v total %d; want hits 2-3 of %v", hitIDs(page), page.Total, hitIDs(all))
	}
	past, _ := f.app.Search(f.global, SearchQuery{Text: "apple", Offset: 99})
	if len(past.Hits) != 0 || past.Hits == nil || past.Total != all.Total {
		t.Errorf("past the end: %+v", past)
	}
	big, _ := f.app.Search(f.global, SearchQuery{Text: "apple", Limit: SearchMaxLimit + 1})
	if big.Total != all.Total {
		t.Errorf("capped limit: %+v", big)
	}
	for i := 1; i < len(all.Hits); i++ {
		if all.Hits[i].UpdatedAt.After(all.Hits[i-1].UpdatedAt) {
			t.Errorf("hits not newest first: %v", hitIDs(all))
		}
	}
}

func TestSearch_Wildcards(t *testing.T) {
	f := newSearchFixture(t)
	literal := f.note(t, map[string]any{"Title": "Rates", "Text": `100% done_now \ ok`})
	f.note(t, map[string]any{"Title": "Other", "Text": "1000 doneXnow"})
	for _, text := range []string{"0% d", "e_n", `\ ok`} {
		res, err := f.app.Search(f.global, SearchQuery{Text: text, Types: []string{"note"}})
		if err != nil {
			t.Fatal(err)
		}
		if res.Total != 1 || !hasHit(res, literal.ID) {
			t.Errorf("%q: hits %v; want only the literal match", text, hitIDs(res))
		}
	}
}

func TestSearch_UnicodeFoldAndEscapes(t *testing.T) {
	f := newSearchFixture(t)
	n := f.note(t, map[string]any{"Title": "Æble <b>&\"quoted\"", "Text": "ÆBLEGRØD"})
	for _, text := range []string{"æble", "ÆBLE", "blegrød", "<b>&", `"quoted"`, "æ"} {
		res, err := f.app.Search(f.global, SearchQuery{Text: text, Types: []string{"note"}})
		if text == "æ" {
			if !errors.Is(err, ErrBadRequest) {
				t.Errorf("one rune: %v", err)
			}
			continue
		}
		if err != nil || !hasHit(res, n.ID) {
			t.Errorf("%q: %v %v; want the note", text, hitIDs(res), err)
		}
	}
}

func TestSearch_SearchableFields(t *testing.T) {
	f := newSearchFixture(t)
	f.note(t, map[string]any{"Title": "Plain", "Secret": "zebra"})
	if res, _ := f.app.Search(f.global, SearchQuery{Text: "zebra"}); res.Total != 0 {
		t.Errorf("a field with no role, format or search flag matched: %v", hitIDs(res))
	}
	ctx := context.Background()
	if _, err := f.app.DefineContentType(ctx, &ContentTypeSchema{TypeName: "memo",
		Fields: json.RawMessage(`[{"Name":"Title","Type":"string","Role":"title"},{"Name":"Code","Type":"string","Search":true}]`)}); err != nil {
		t.Fatal(err)
	}
	repo, _ := f.app.DynamicContentRepo("memo")
	m, err := repo.CreateDraft(ctx, map[string]any{"Title": "zebra title", "Code": "zebra-7"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := f.app.Search(f.global, SearchQuery{Text: "zebra", Types: []string{"memo"}})
	if err != nil || res.Total != 1 || res.Hits[0].ID != m.ID || res.Hits[0].Field != "Code" {
		t.Errorf("flagged field: %+v %v; want only Code searched", res, err)
	}
}

func TestSearch_BadFieldsJSON(t *testing.T) {
	f := newSearchFixture(t)
	n := f.note(t, map[string]any{"Title": "Broken apple"})
	if _, err := f.db.Exec(`UPDATE smeldr_dynamic_content SET fields = '{"Title":"apple' WHERE id = ?`, n.ID); err != nil {
		t.Fatal(err)
	}
	res, err := f.app.Search(f.global, SearchQuery{Text: "apple", Types: []string{"note"}})
	if err != nil || res.Total != 0 {
		t.Errorf("bad JSON row: %v %v; want skipped", hitIDs(res), err)
	}
}

func TestSearch_QueryError(t *testing.T) {
	f := newSearchFixture(t)
	if _, err := f.db.Exec(`DROP TABLE smeldr_tasks`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.Search(f.global, SearchQuery{Text: "apple"}); !errors.Is(err, ErrInternal) {
		t.Errorf("query error: %v; want internal", err)
	}
	if _, err := f.db.Exec(`DROP TABLE smeldr_dynamic_content`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.Search(f.global, SearchQuery{Text: "apple", Types: []string{"note"}}); !errors.Is(err, ErrInternal) {
		t.Errorf("dynamic query error: %v; want internal", err)
	}
}

func TestSearch_ScopeLoadFails(t *testing.T) {
	f := newSearchFixture(t)
	if _, err := f.db.Exec(`DROP TABLE smeldr_role_grants`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.Search(f.reader, SearchQuery{Text: "apple"}); !errors.Is(err, ErrInternal) {
		t.Errorf("scope load: %v; want internal", err)
	}
}

func TestSearch_NoRelationsTable(t *testing.T) {
	f := newSearchFixture(t)
	if _, err := f.db.Exec(`DROP TABLE smeldr_relations`); err != nil {
		t.Fatal(err)
	}
	res, err := f.app.Search(f.global, SearchQuery{Types: []string{"Decision"}, Place: SearchPlaceNone})
	if err != nil || res.Total != 2 {
		t.Errorf("no relations: %v %v; want every Decision unplaced", hitIDs(res), err)
	}
}

func TestSearch_PlaceQueryError(t *testing.T) {
	f := newSearchFixture(t)
	if _, err := f.db.Exec(`ALTER TABLE smeldr_relations RENAME COLUMN edge_class TO gone`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.Search(f.global, SearchQuery{Place: SearchPlaceNone}); !errors.Is(err, ErrInternal) {
		t.Errorf("place query error: %v; want internal", err)
	}
}

func TestSearch_NoPlaceTypes(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")})
	if _, err := app.Search(authorCtx(), SearchQuery{Place: "DOM1"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("no place types: %v; want not found", err)
	}
	res, err := app.Search(authorCtx(), SearchQuery{Place: SearchPlaceNone})
	if err != nil || res.Total != 0 {
		t.Errorf("no database: %+v %v", res, err)
	}
}

// A module on a repository that is not SQL is read whole and matched in Go.
func TestSearch_MemoryRepo(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")})
	m := NewModule((*Task)(nil), Repo(NewMemoryRepo[*Task]()), At("/tasks"), MCP(MCPRead, MCPWrite))
	app.Content(m)
	ctx := authorCtx()
	if _, err := m.MCPCreate(ctx, map[string]any{"Title": "Memory apple", "Band": "core"}); err != nil {
		t.Fatal(err)
	}
	res, err := app.Search(ctx, SearchQuery{Text: "apple"})
	if err != nil || res.Total != 1 || res.Hits[0].Title != "Memory apple" {
		t.Errorf("memory repo: %+v %v", res, err)
	}
	if app.addSearchModule(m); len(app.searchModules) != 1 {
		t.Errorf("a second module of a type was added")
	}
}

func TestSearch_HitTitleFallback(t *testing.T) {
	cases := []struct {
		title string
		hit   SearchHit
		want  string
	}{
		{"  first\nsecond", SearchHit{}, "first"},
		{"", SearchHit{Address: "T1", Slug: "s"}, "T1"},
		{"", SearchHit{Slug: "s"}, "s"},
		{strings.Repeat("y", TitleMaxLength+5), SearchHit{}, strings.Repeat("y", TitleMaxLength)},
	}
	for _, c := range cases {
		if got := hitTitle(c.title, c.hit); got != c.want {
			t.Errorf("hitTitle(%q) = %q; want %q", c.title, got, c.want)
		}
	}
}

func TestSearch_SnippetCut(t *testing.T) {
	text := []rune(strings.Repeat("a ", 100) + "needle" + strings.Repeat(" b", 100))
	i := runeIndex(text, []rune("needle"))
	s := snippetAround(text, i, 6)
	if !strings.HasPrefix(s, "...") || !strings.HasSuffix(s, "...") || !strings.Contains(s, "needle") {
		t.Errorf("snippet = %q", s)
	}
	if runeIndex([]rune("abc"), nil) != 0 || runeIndex([]rune("ab"), []rune("abc")) != -1 {
		t.Error("runeIndex edge cases")
	}
	if got := longestASCIIRun([]rune("æab€cde")); got != "cde" {
		t.Errorf("longestASCIIRun = %q", got)
	}
	if dynamicFields, _ := dynamicSearchFields(nil); dynamicFields != nil {
		t.Error("nil schema has fields")
	}
	if dynamicFields, _ := dynamicSearchFields(&ContentTypeSchema{Fields: json.RawMessage(`not json`)}); dynamicFields != nil {
		t.Error("bad schema has fields")
	}
}

func BenchmarkSearch_Text(b *testing.B) {
	db := newSQLiteDB(b)
	if err := CreateOrchestrationTables(db); err != nil {
		b.Fatal(err)
	}
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	RegisterOrchestrationTypes(app, db)
	_ = app.Handler()
	m := findModuleB[*Task](b, app)
	ctx := TrustedRead(authorCtx()).(Context)
	for i := range 5000 {
		if _, err := m.MCPCreate(ctx, map[string]any{"Title": fmt.Sprintf("Task %d", i), "Description": "some body text", "Band": "core"}); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for b.Loop() {
		if _, err := app.Search(ctx, SearchQuery{Text: "task 49", Types: []string{"Task"}}); err != nil {
			b.Fatal(err)
		}
	}
}

func findModuleB[T any](b *testing.B, app *App) *Module[T] {
	b.Helper()
	for _, mm := range app.mcpModules {
		if m, ok := mm.(*Module[T]); ok {
			return m
		}
	}
	b.Fatalf("no module for %T", *new(T))
	return nil
}
