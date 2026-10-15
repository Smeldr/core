package smeldr

import (
	"bytes"
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

// Titles and closed vocabularies (D112).

const titleTestSecret = "title-test-secret-32-bytes-xxxxxxx"

type titleEnv struct {
	app       *App
	db        DB
	h         http.Handler
	tasks     *Module[*Task]
	goals     *Module[*Goal]
	decisions *Module[*Decision]
	amends    *Module[*Amendment]
}

func newTitleEnv(t *testing.T, cutover time.Time) titleEnv {
	t.Helper()
	db := addressDB(t)
	app := New(MustConfig(Config{BaseURL: "https://example.com", Secret: []byte(titleTestSecret), DB: db, TitleCutover: cutover}))
	RegisterOrchestrationTypes(app, db)
	e := titleEnv{app: app, db: db}
	e.h = app.Handler()
	e.tasks = findModule[*Task](t, app)
	e.goals = findModule[*Goal](t, app)
	e.decisions = findModule[*Decision](t, app)
	e.amends = findModule[*Amendment](t, app)
	return e
}

// warningsOf returns the warning codes of a create or update result, nil for a
// typed result that carries none.
func warningsOf(out any) map[string]string {
	m, ok := out.(map[string]any)
	if !ok {
		return nil
	}
	ws, _ := m["warnings"].([]CreateWarning)
	got := map[string]string{}
	for _, w := range ws {
		got[w.Code] = w.Field
	}
	return got
}

func editorCtx() Context { return NewTestContext(User{ID: "u-editor", Roles: []Role{Author, Editor}}) }

func TestRecordTitles_MissingWarns(t *testing.T) {
	e := newTitleEnv(t, time.Time{})
	cases := []struct {
		name   string
		create func() (any, error)
		field  string
	}{
		{"Task", func() (any, error) {
			return e.tasks.MCPCreate(editorCtx(), map[string]any{"Description": "x", "Band": "core"})
		}, "title"},
		{"Goal", func() (any, error) { return e.goals.MCPCreate(editorCtx(), map[string]any{"Description": "x"}) }, "title"},
		{"Decision", func() (any, error) {
			return e.decisions.MCPCreate(editorCtx(), map[string]any{"Body": "x", "Scope": "core"})
		}, "title"},
		{"Amendment", func() (any, error) {
			return e.amends.MCPCreate(editorCtx(), map[string]any{"Body": "x", "amendment_type": "fix"})
		}, "summary"},
	}
	for _, c := range cases {
		out, err := c.create()
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := warningsOf(out); got["title_missing"] != c.field {
			t.Errorf("%s warnings = %v; want title_missing on %s", c.name, got, c.field)
		}
		if m, ok := out.(map[string]any); ok {
			if ws := m["warnings"].([]CreateWarning); ws[len(ws)-1].Address == "" {
				t.Errorf("%s: warning carries no address", c.name)
			}
		}
	}
	// A blank title is missing too.
	out, err := e.tasks.MCPCreate(editorCtx(), map[string]any{"Title": "   ", "Band": "core"})
	if err != nil || warningsOf(out)["title_missing"] != "title" {
		t.Errorf("blank title: %v %v", warningsOf(out), err)
	}
}

func TestRecordTitles_LengthInRunes(t *testing.T) {
	e := newTitleEnv(t, time.Time{})
	long, err := e.tasks.MCPCreate(editorCtx(), map[string]any{"Title": strings.Repeat("a", TitleMaxLength+1), "Band": "core"})
	if err != nil || warningsOf(long)["title_too_long"] != "title" {
		t.Errorf("121 characters: %v %v", warningsOf(long), err)
	}
	// 120 two-byte characters: 240 bytes, but within the limit.
	ok, err := e.tasks.MCPCreate(editorCtx(), map[string]any{"Title": strings.Repeat("é", TitleMaxLength), "Band": "core"})
	if err != nil {
		t.Fatal(err)
	}
	if _, typed := ok.(*Task); !typed {
		t.Errorf("120 multibyte characters: result %T with %v; want a typed Task, no warning", ok, warningsOf(ok))
	}
}

func TestRecordTitles_UpdateUntouchedNoWarning(t *testing.T) {
	e := newTitleEnv(t, time.Time{})
	out, err := e.tasks.MCPCreate(editorCtx(), map[string]any{"Band": "core"})
	if err != nil {
		t.Fatal(err)
	}
	task := asItem[*Task](out)
	upd, err := e.tasks.MCPUpdate(editorCtx(), task.Slug, map[string]any{"band": "site"})
	if err != nil {
		t.Fatal(err)
	}
	if _, typed := upd.(*Task); !typed {
		t.Errorf("an update not touching the title: %T %v; want a typed Task", upd, warningsOf(upd))
	}
}

func TestRecordTitles_UpdateWarns(t *testing.T) {
	e := newTitleEnv(t, time.Time{})
	out, err := e.tasks.MCPCreate(editorCtx(), map[string]any{"Title": "short", "Band": "core"})
	if err != nil {
		t.Fatal(err)
	}
	task := asItem[*Task](out)
	upd, err := e.tasks.MCPUpdate(editorCtx(), task.Slug, map[string]any{"TITLE": strings.Repeat("b", 130)})
	if err != nil {
		t.Fatal(err)
	}
	if warningsOf(upd)["title_too_long"] != "title" {
		t.Errorf("update with a long title: %v", warningsOf(upd))
	}
	if got := asItem[*Task](upd).Title; len(got) != 130 {
		t.Errorf("stored title has %d characters; want 130 (a warning, not a refusal)", len(got))
	}
}

func TestRecordTitles_AfterCutoverRefused(t *testing.T) {
	e := newTitleEnv(t, time.Now().Add(-time.Hour))
	_, err := e.tasks.MCPCreate(editorCtx(), map[string]any{"Band": "core"})
	var ve *ValidationError
	if !errors.As(err, &ve) || !strings.Contains(err.Error(), TitleGuidance) || !strings.Contains(err.Error(), "title") {
		t.Fatalf("create without a title after the cutover: %v; want a validation error with the guidance", err)
	}
	if _, err := e.amends.MCPCreate(editorCtx(), map[string]any{"Summary": strings.Repeat("s", 121), "amendment_type": "fix"}); !errors.As(err, &ve) {
		t.Errorf("long summary after the cutover: %v", err)
	}
	out, err := e.tasks.MCPCreate(editorCtx(), map[string]any{"Title": "a title", "Band": "core"})
	if err != nil {
		t.Fatal(err)
	}
	slug := asItem[*Task](out).Slug
	if _, err := e.tasks.MCPUpdate(editorCtx(), slug, map[string]any{"title": ""}); !errors.As(err, &ve) {
		t.Errorf("update clearing the title after the cutover: %v", err)
	}
	// An older untitled row still takes an edit that leaves the title alone.
	if err := NewSQLRepo[*Task](e.db, Table("smeldr_tasks")).Save(context.Background(), &Task{Node: Node{ID: "old-1", Slug: "old-1", Status: "backlog"}, Band: "core"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.tasks.MCPUpdate(editorCtx(), "old-1", map[string]any{"band": "site"}); err != nil {
		t.Errorf("untouched title on an old row after the cutover: %v", err)
	}
}

func TestRecordTitles_BeforeCutoverWarns(t *testing.T) {
	e := newTitleEnv(t, time.Now().Add(time.Hour))
	out, err := e.goals.MCPCreate(editorCtx(), map[string]any{"Description": "x"})
	if err != nil || warningsOf(out)["title_missing"] != "title" {
		t.Errorf("before a future cutover: %v %v; want a warning", warningsOf(out), err)
	}
}

func TestRecordTitles_HTTP(t *testing.T) {
	tok, err := SignToken(User{ID: "http-1", Roles: []Role{Editor}}, titleTestSecret, 0)
	if err != nil {
		t.Fatal(err)
	}
	do := func(h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	before := newTitleEnv(t, time.Time{})
	w := do(before.h, http.MethodPost, "/tasks", map[string]any{"band": "core"})
	if w.Code != http.StatusCreated || strings.Contains(w.Body.String(), "warnings") {
		t.Errorf("HTTP create without a title = %d %s; want 201 and no warnings key", w.Code, w.Body.String())
	}
	var created Task
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	// A full-replace PUT that keeps the empty title is not held to the rule.
	if w := do(before.h, http.MethodPut, "/tasks/"+created.Slug, map[string]any{"band": "site", "rev": created.Rev, "status": string(created.Status)}); w.Code >= 300 {
		t.Errorf("PUT keeping the empty title = %d %s", w.Code, w.Body.String())
	}

	after := newTitleEnv(t, time.Now().Add(-time.Hour))
	if w := do(after.h, http.MethodPost, "/tasks", map[string]any{"band": "core"}); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "title") {
		t.Errorf("HTTP create without a title after the cutover = %d %s; want 422 on title", w.Code, w.Body.String())
	}
	w = do(after.h, http.MethodPost, "/tasks", map[string]any{"title": "named", "band": "core"})
	if w.Code != http.StatusCreated {
		t.Fatalf("HTTP create with a title = %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if w := do(after.h, http.MethodPut, "/tasks/"+created.Slug, map[string]any{"title": strings.Repeat("x", 121), "band": "core", "rev": created.Rev, "status": string(created.Status)}); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("PUT with a long title after the cutover = %d %s; want 422", w.Code, w.Body.String())
	}
	if w := do(after.h, http.MethodPatch, "/tasks/"+created.Slug, map[string]any{"title": ""}); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("PATCH clearing the title after the cutover = %d %s; want 422", w.Code, w.Body.String())
	}
}

func TestRecordTitles_Surfaces(t *testing.T) {
	e := newTitleEnv(t, time.Time{})
	out, err := e.tasks.MCPCreate(editorCtx(), map[string]any{"Title": "Ship the registry", "Band": "core"})
	if err != nil {
		t.Fatal(err)
	}
	task := asItem[*Task](out)
	payload, err := buildWebhookPayload("Task", task, AfterUpdate)
	if err != nil || !strings.Contains(string(payload), "Ship the registry") {
		t.Errorf("webhook payload %s %v; want the title", payload, err)
	}
	var titled Titled = task
	if titled.ContentTitle() != "Ship the registry" {
		t.Errorf("ContentTitle = %q", titled.ContentTitle())
	}
	for _, v := range []Titled{&Decision{Title: "d"}, &Goal{Title: "g"}, &Amendment{Summary: "a"}} {
		if v.ContentTitle() == "" {
			t.Errorf("%T has no ContentTitle", v)
		}
	}
	if err := CreateRelationTables(e.db); err != nil {
		t.Fatal(err)
	}
	rels, err := NewRelationStore(e.db)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := BuildContextPacket(TrustedRead(context.Background()), e.db, rels, "https://example.com", "test", "task", task.Slug, 1)
	if err != nil {
		t.Fatal(err)
	}
	if packet.Anchor.Fields["title"] != "Ship the registry" {
		t.Errorf("packet anchor fields = %v; want the title", packet.Anchor.Fields)
	}
}

func TestAmendmentType_Closed(t *testing.T) {
	e := newTitleEnv(t, time.Time{})
	var ve *ValidationError
	for _, v := range []string{"", "feat", "Level 2"} {
		_, err := e.amends.MCPCreate(editorCtx(), map[string]any{"Summary": "s", "amendment_type": v})
		if !errors.As(err, &ve) || !strings.Contains(err.Error(), "feature, fix, docs, chore, test, ci, refactor, release") {
			t.Errorf("amendment_type %q: %v; want a validation error naming the list", v, err)
		}
	}
	for _, v := range AmendmentTypes {
		if _, err := e.amends.MCPCreate(editorCtx(), map[string]any{"Summary": "s " + v, "amendment_type": v}); err != nil {
			t.Errorf("amendment_type %q: %v", v, err)
		}
	}
}

func TestAmendmentType_Updates(t *testing.T) {
	e := newTitleEnv(t, time.Time{})
	if err := NewSQLRepo[*Amendment](e.db, Table("smeldr_amendments")).Save(context.Background(),
		&Amendment{Node: Node{ID: "am-old", Slug: "am-old", Status: "scoped"}, AmendmentType: "Level 2", Summary: "old"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.amends.MCPUpdate(editorCtx(), "am-old", map[string]any{"version": "v1"}); err != nil {
		t.Errorf("update leaving a legacy type alone: %v", err)
	}
	var ve *ValidationError
	if _, err := e.amends.MCPUpdate(editorCtx(), "am-old", map[string]any{"amendment_type": "deploy"}); !errors.As(err, &ve) {
		t.Errorf("update changing to an unknown type: %v", err)
	}
	out, err := e.amends.MCPUpdate(editorCtx(), "am-old", map[string]any{"amendment_type": "release"})
	if err != nil || asItem[*Amendment](out).AmendmentType != "release" {
		t.Errorf("update changing to a listed type: %v", err)
	}
}

func TestAmendmentPilot_Retired(t *testing.T) {
	e := newTitleEnv(t, time.Time{})
	out, err := e.amends.MCPCreate(editorCtx(), map[string]any{"Summary": "s", "amendment_type": "fix", "pilot": "core-b"})
	if err != nil {
		t.Fatal(err)
	}
	if warningsOf(out)["deprecated_field"] != "pilot" {
		t.Errorf("create with pilot: %v; want deprecated_field on pilot", warningsOf(out))
	}
	a := asItem[*Amendment](out)
	got, err := e.amends.MCPGet(editorCtx(), a.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(got); strings.Contains(string(b), `"pilot"`) {
		t.Errorf("pilot emitted: %s", b)
	}
	if asItem[*Amendment](got).Pilot != "" || got.(*Amendment).Pilot != "" {
		t.Errorf("pilot stored: %q", got.(*Amendment).Pilot)
	}
	upd, err := e.amends.MCPUpdate(editorCtx(), a.Slug, map[string]any{"PILOT": "x"})
	if err != nil || warningsOf(upd)["deprecated_field"] != "pilot" {
		t.Errorf("update with pilot: %v %v", warningsOf(upd), err)
	}
	// An older row keeps its stored pilot through an update.
	if err := NewSQLRepo[*Amendment](e.db, Table("smeldr_amendments")).Save(context.Background(),
		&Amendment{Node: Node{ID: "am-p", Slug: "am-p", Status: "scoped"}, AmendmentType: "fix", Summary: "s", Pilot: "core-implementer"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.amends.MCPUpdate(editorCtx(), "am-p", map[string]any{"version": "v2"}); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := e.db.QueryRowContext(context.Background(), `SELECT pilot FROM smeldr_amendments WHERE id = 'am-p'`).Scan(&stored); err != nil || stored != "core-implementer" {
		t.Errorf("older row's pilot = %q %v; want kept", stored, err)
	}
}

func TestRecordShape_MCPSchema(t *testing.T) {
	e := newTitleEnv(t, time.Time{})
	fields := map[string]MCPField{}
	for _, f := range e.amends.MCPSchema() {
		fields[f.JSONName] = f
	}
	if !slices.Equal(fields["amendment_type"].Enum, AmendmentTypes) || fields["amendment_type"].Required {
		t.Errorf("amendment_type = %+v; want the enum, not required by tag", fields["amendment_type"])
	}
	if !fields["pilot"].Deprecated || !strings.HasPrefix(fields["pilot"].Description, "Deprecated (D112):") {
		t.Errorf("pilot = %+v; want deprecated", fields["pilot"])
	}
	for _, m := range []interface{ MCPSchema() []MCPField }{e.tasks, e.goals, e.decisions} {
		for _, f := range m.MCPSchema() {
			if f.JSONName == "title" && (f.Required || f.MaxLength != 0) {
				t.Errorf("title = %+v; want neither required nor maxLength (warnings first)", f)
			}
		}
	}
}

func TestResolvedRequiresReason(t *testing.T) {
	for _, flow := range []StateFlow{orchTaskFlow(), orchGoalFlow()} {
		n := 0
		for _, tr := range flow.Transitions {
			if tr.To == "resolved" {
				n++
				if !tr.RequiredReason {
					t.Errorf("%s: %s -> resolved takes no reason", flow.Name, tr.From)
				}
			}
		}
		if n == 0 {
			t.Errorf("%s has no transition into resolved", flow.Name)
		}
		if !strings.Contains(flow.Description, "resolved: it closed without being carried out") || !strings.Contains(flow.Description, "done: the work was carried out") {
			t.Errorf("%s description = %q", flow.Name, flow.Description)
		}
	}
}

func TestFlowDescriptions_Upserted(t *testing.T) {
	db := addressDB(t)
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	old := orchTaskFlow()
	old.Description = ""
	if err := app.RegisterFlow(old); err != nil {
		t.Fatal(err)
	}
	if err := app.RegisterFlow(orchTaskFlow()); err != nil {
		t.Fatal(err)
	}
	var desc string
	if err := db.QueryRowContext(context.Background(), `SELECT description FROM smeldr_state_flows WHERE type_name = 'Task'`).Scan(&desc); err != nil || !strings.Contains(desc, "resolved") {
		t.Errorf("stored description = %q %v", desc, err)
	}
}

func TestCapabilityWarnings_Registered(t *testing.T) {
	var app *App
	create := app.CapabilityWarnings(ResourceItem, VerbCreate)
	update := app.CapabilityWarnings(ResourceItem, VerbUpdate)
	for _, code := range []string{"slug_adjusted", "deprecated_field", "title_missing", "title_too_long"} {
		if !slices.Contains(create, code) {
			t.Errorf("item create does not register %s", code)
		}
	}
	for _, code := range []string{"deprecated_field", "title_missing", "title_too_long"} {
		if !slices.Contains(update, code) {
			t.Errorf("item update does not register %s", code)
		}
	}
	if app.CapabilityWarnings(ResourceItem, VerbGet) != nil || app.CapabilityWarnings("nope", "nope") != nil {
		t.Error("a capability without warnings returns some")
	}
	create[0] = "mutated"
	if app.CapabilityWarnings(ResourceItem, VerbCreate)[0] == "mutated" {
		t.Error("CapabilityWarnings returned the registry's own slice")
	}
	// Every code core produces is registered for item create.
	produced := []string{"slug_adjusted", "deprecated_field"}
	for _, w := range titleWarnings(&Task{}) {
		produced = append(produced, w.Code)
	}
	for _, w := range titleWarnings(&Task{Title: strings.Repeat("x", 121)}) {
		produced = append(produced, w.Code)
	}
	for _, w := range retiredFieldWarnings(&Amendment{}, map[string]any{"pilot": "x"}) {
		produced = append(produced, w.Code)
	}
	for _, code := range produced {
		if !slices.Contains(app.CapabilityWarnings(ResourceItem, VerbCreate), code) {
			t.Errorf("produced code %s is not registered", code)
		}
	}
}

func TestRecordTitles_Helpers(t *testing.T) {
	if titleWarnings(&Signal{}) != nil || titleFieldTouched(&Signal{}, map[string]any{"title": "x"}) || titleChanged(&Signal{}, &Signal{}) {
		t.Error("a type without a title rule got one")
	}
	if retiredFieldWarnings(&Task{}, map[string]any{"pilot": "x"}) != nil || retiredFieldWarnings(&Amendment{}, map[string]any{"version": "x"}) != nil {
		t.Error("a retired-field warning for a field that is not retired")
	}
	if ws, err := titleCheck(&Task{Title: "fine"}, time.Now().Add(-time.Hour)); ws != nil || err != nil {
		t.Errorf("a valid title after the cutover: %v %v", ws, err)
	}
}

// TestRecordTitles_SlugFromTitleExplicitWins: a new Task's slug comes from its
// title, and an explicit slug still wins (architect's answer 3).
func TestRecordTitles_SlugFromTitleExplicitWins(t *testing.T) {
	e := newTitleEnv(t, time.Time{})
	out, err := e.tasks.MCPCreate(editorCtx(), map[string]any{"Title": "Ship the Registry", "Band": "core"})
	if err != nil {
		t.Fatal(err)
	}
	if got := asItem[*Task](out).Slug; got != "ship-the-registry" {
		t.Errorf("slug from title = %q", got)
	}
	out, err = e.tasks.MCPCreate(editorCtx(), map[string]any{"Title": "Something else", "Slug": "core-chosen-slug", "Band": "core"})
	if err != nil {
		t.Fatal(err)
	}
	if got := asItem[*Task](out).Slug; got != "core-chosen-slug" {
		t.Errorf("explicit slug = %q; want it to win over the title", got)
	}
}
