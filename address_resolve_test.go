package smeldr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseAddress(t *testing.T) {
	ok := map[string]struct {
		prefix string
		no     int64
	}{
		"D110": {"D", 110}, "d110": {"D", 110}, " INV27 ": {"INV", 27}, "abcd1234": {"ABCD", 1234},
	}
	for in, want := range ok {
		p, n, good := ParseAddress(in)
		if !good || p != want.prefix || n != want.no {
			t.Errorf("ParseAddress(%q) = %q %d %v, want %q %d", in, p, n, good, want.prefix, want.no)
		}
	}
	for _, in := range []string{"", "D", "110", "D0", "D007", "ABCDE1", "D-1", "D1x", "1D"} {
		if _, _, good := ParseAddress(in); good {
			t.Errorf("ParseAddress(%q) parsed, want not an address", in)
		}
	}
	if _, ok := addressIdent("abcd1234", "T"); ok {
		t.Error("a hex-like slug was taken as a T address")
	}
	if a, ok := addressIdent("t12", "T"); !ok || a != "T12" {
		t.Errorf("addressIdent(t12, T) = %q %v", a, ok)
	}
}

// resolveItem: address (this type's prefix, any case), slug, ID, then the
// transitional legacy column; an address wins a tie with a slug.
func TestResolveItem_D110Order(t *testing.T) {
	db := addressDB(t)
	_, m := decisionModule(t, db)
	ctx := authorCtx()
	d1 := mustCreateDecision(t, m, map[string]any{"Title": "first"})
	// A slug that is literally another record's address: "d1" names d2 by slug.
	d2 := mustCreateDecision(t, m, map[string]any{"Title": "second", "Slug": "d1"})
	for _, ident := range []string{"D1", "d1"} {
		got, err := m.resolveItem(ctx, ident)
		if err != nil || got.ID != d1.ID {
			t.Errorf("resolveItem(%q) = %v %v, want D1 (the address wins the tie)", ident, got, err)
		}
	}
	if got, err := m.resolveItem(ctx, d2.ID); err != nil || got.ID != d2.ID {
		t.Errorf("by ID: %v %v", got, err)
	}
	if got, err := m.resolveItem(ctx, d1.Slug); err != nil || got.ID != d1.ID {
		t.Errorf("by slug: %v %v", got, err)
	}
	if _, err := m.resolveItem(ctx, "T1"); err == nil {
		t.Error("another type's prefix resolved a Decision")
	}
	// A record from before addresses is found by its legacy number.
	if _, err := db.Exec(`INSERT INTO smeldr_decisions (id, slug, status, created_at, updated_at, decision_number)
		VALUES ('old', 'old-slug', 'proposed', $1, $1, 'D74-DECLINE')`, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if got, err := m.resolveItem(ctx, "D74-DECLINE"); err != nil || got.ID != "old" {
		t.Errorf("legacy fallback: %v %v", got, err)
	}
}

// transition_item resolves an address, an ID and, for a dynamic record type, an
// address too (it took only a slug before).
func TestTransitionItem_ResolvesAddressAndID(t *testing.T) {
	db := addressDB(t)
	app, m := decisionModule(t, db)
	registerTypeForTest(app, "Decision")
	mustCreateDecision(t, m, map[string]any{"Title": "t"})
	d := mustCreateDecision(t, m, map[string]any{"Title": "u"})
	if _, err := app.TransitionItemVia(TrustedRead(authorCtx()), "mcp", "Decision", "d1", "archived", ""); err != nil {
		t.Errorf("transition by address: %v", err)
	}
	if _, err := app.TransitionItemVia(TrustedRead(authorCtx()), "mcp", "Decision", d.ID, "archived", ""); err != nil {
		t.Errorf("transition by ID: %v", err)
	}

	dyn, _ := dynamicAddressApp(t)
	ctx := context.Background()
	if _, err := dyn.DefineContentType(ctx, schemaFor("invoice", "", "")); err != nil {
		t.Fatal(err)
	}
	repo, _ := dyn.DynamicContentRepo("invoice")
	node, err := repo.CreateDraft(ctx, map[string]any{"Title": "x"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ident := range []string{"inv1", node.Slug, node.ID} {
		got, err := repo.Resolve(ctx, ident)
		if err != nil || got.ID != node.ID {
			t.Errorf("Resolve(%q) = %v %v", ident, got, err)
		}
	}
	if _, err := repo.Resolve(ctx, "INV99"); err == nil {
		t.Error("Resolve of a missing address: want ErrNotFound")
	}
	out, err := dyn.TransitionItemVia(TrustedRead(ctx), "mcp", "invoice", "INV1", "published", "")
	if err != nil || out["slug"] != node.Slug {
		t.Errorf("dynamic transition by address = %v %v, want the real slug", out, err)
	}
}

func registerTypeForTest(app *App, name string) {
	if app.typeRegistry.Lookup(name) == nil {
		app.typeRegistry.Register(&TypeDescriptor{Name: name, Kind: "compiled"})
	}
}

// The packet anchor and the goal context resolve an address, a slug and an ID;
// a Run is an anchor too.
func TestPacketAndGoalContext_ResolveAddress(t *testing.T) {
	ctx := context.Background()
	db := addressDB(t)
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	RegisterOrchestrationTypes(app, db)
	_ = app.Handler()
	goalMod := findModule[*Goal](t, app)
	created, err := goalMod.MCPCreate(authorCtx(), map[string]any{"Description": "a goal"})
	if err != nil {
		t.Fatal(err)
	}
	g := created.(*Goal)
	for _, ident := range []string{"G1", "g1", g.Slug, g.ID} {
		pkt, err := BuildContextPacket(ctx, db, nil, "https://example.com", "test", "goal", ident, 1)
		if err != nil || pkt.Anchor.ID != "G1" {
			t.Errorf("packet anchor %q = %v %v, want G1", ident, pkt, err)
		}
		gc, err := QueryGoalContext(ctx, db, nil, ident)
		if err != nil || gc.Goal.ID != g.ID {
			t.Errorf("goal context %q = %v %v", ident, gc, err)
		}
	}
	runMod := findModule[*Run](t, app)
	r, err := runMod.MCPCreate(authorCtx(), map[string]any{"TaskID": "T1", "Repo": "smeldr/core"})
	if err != nil {
		t.Fatal(err)
	}
	pkt, err := BuildContextPacket(ctx, db, nil, "https://example.com", "test", "run", "R1", 1)
	if err != nil || pkt.Anchor.ID != "R1" || pkt.Anchor.Fields["repo"] != "smeldr/core" {
		t.Errorf("run anchor = %v %v (run %v)", pkt, err, r)
	}
}

// findModule returns the App's registered Module[T].
func findModule[T any](t *testing.T, app *App) *Module[T] {
	t.Helper()
	for _, mm := range app.mcpModules {
		if m, ok := mm.(*Module[T]); ok {
			return m
		}
	}
	t.Fatalf("no module for %T", *new(T))
	return nil
}

// amends: an address, a lower-case one and, transitionally, a bare number,
// resolved by address first; the bare form is logged as deprecated.
func TestAmends_ResolveByAddress(t *testing.T) {
	ctx := context.Background()
	db := addressDB(t)
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	RegisterOrchestrationTypes(app, db)
	_ = app.Handler()
	dec := findModule[*Decision](t, app)
	d, err := dec.MCPCreate(authorCtx(), map[string]any{"Title": "decided"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := resolveDecisionID(ctx, db, "D1")
	if err != nil || id != d.(*Decision).ID {
		t.Errorf("resolveDecisionID(D1) = %q %v", id, err)
	}
	if _, err := resolveDecisionID(ctx, db, "D99"); err == nil {
		t.Error("a missing Decision: want an error")
	}
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)
	am := findModule[*Amendment](t, app)
	a, err := am.MCPCreate(NewTestContext(User{ID: "u-bare", Roles: []Role{Author, Editor}}), map[string]any{"Summary": "s", "Amends": "1"})
	if err != nil {
		t.Fatalf("amends a bare number: %v", err)
	}
	if got := a.(*Amendment).Amends; got != "D1" {
		t.Errorf("amends = %q, want D1", got)
	}
	if !strings.Contains(buf.String(), "bare number") || !strings.Contains(buf.String(), "u-bare") {
		t.Errorf("log = %q, want the bare number noted with its token", buf.String())
	}
	if got, err := resolveAmendmentID(ctx, db, "a1"); err != nil || got != a.(*Amendment).ID {
		t.Errorf("resolveAmendmentID(a1) = %q %v", got, err)
	}
}

// The transition period on MCP create: a legacy value equal to the address is
// dropped, a different one moves to external_ref, an explicit external_ref
// wins; the legacy column holds the address; the result carries a warning
// only when a legacy field was sent; the log is written once per day.
func TestTransitionPeriod_LegacyFieldsOnCreate(t *testing.T) {
	db := addressDB(t)
	_, m := decisionModule(t, db)
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)
	ctx := NewTestContext(User{ID: "u-legacy", Roles: []Role{Author, Editor}})

	plain, err := m.MCPCreate(ctx, map[string]any{"Title": "plain"})
	if _, isMap := plain.(map[string]any); err != nil || isMap {
		t.Fatalf("no legacy field: %T %v, want the item unchanged", plain, err)
	}
	if got := plain.(*Decision).DecisionNumber; got != "D1" {
		t.Errorf("legacy column = %q, want the address D1 mirrored", got)
	}

	cases := []struct {
		fields      map[string]any
		address     string
		externalRef string
		message     string
	}{
		{map[string]any{"Title": "same", "decision_number": "D2"}, "D2", "", "equals the assigned address"},
		{map[string]any{"Title": "own", "decision_number": "OPS-9"}, "D3", "OPS-9", "stored in external_ref"},
		{map[string]any{"Title": "both", "decision_number": "OPS-10", "external_ref": "MINE"}, "D4", "MINE", "not stored"},
	}
	for _, c := range cases {
		out, err := m.MCPCreate(ctx, c.fields)
		if err != nil {
			t.Fatal(err)
		}
		res, ok := out.(map[string]any)
		if !ok {
			t.Fatalf("%v: result %T, want a map with warnings", c.fields, out)
		}
		ws, _ := res["warnings"].([]CreateWarning)
		if len(ws) != 1 || ws[0].Code != "deprecated_field" || ws[0].Field != "decision_number" || ws[0].Address != c.address ||
			!strings.Contains(ws[0].Message, c.message) {
			t.Errorf("%v: warnings = %+v", c.fields, ws)
		}
		if res["address"] != c.address || res["decision_number"] != c.address || res["external_ref"] != c.externalRef {
			t.Errorf("%v: address %v legacy %v external_ref %v, want %s %s %q",
				c.fields, res["address"], res["decision_number"], res["external_ref"], c.address, c.address, c.externalRef)
		}
	}
	if n := strings.Count(buf.String(), "field=decision_number"); n != 1 {
		t.Errorf("deprecation log lines = %d, want 1 per token, field and day:\n%s", n, buf.String())
	}
}

// The legacy fields are marked deprecated in the input schema; the guiding
// error for the cut is prepared.
func TestTransitionPeriod_SchemaAndGuidance(t *testing.T) {
	_, m := decisionModule(t, addressDB(t))
	var found bool
	for _, f := range m.MCPSchema() {
		if f.JSONName == "decision_number" {
			found = true
			if !f.Deprecated || !strings.HasPrefix(f.Description, "Deprecated (D110):") {
				t.Errorf("decision_number field = %+v, want Deprecated and the D110 description", f)
			}
		}
		if f.JSONName == "title" && f.Deprecated {
			t.Error("title is marked deprecated")
		}
	}
	if !found {
		t.Fatal("decision_number missing from the schema")
	}
	for _, field := range []string{"decision_number", "task_id", "goal_id", "amendment_number"} {
		msg, data, ok := RetiredFieldGuidance(field)
		if !ok || !strings.Contains(msg, field+" is no longer accepted") || data["replacement"] != "external_ref" || data["decision"] != "D110" || data["field"] != field {
			t.Errorf("RetiredFieldGuidance(%q) = %q %v %v", field, msg, data, ok)
		}
	}
	if _, _, ok := RetiredFieldGuidance("title"); ok {
		t.Error("RetiredFieldGuidance(title) = ok, want false")
	}
}

// A transition event of an item that was not loaded still carries the address;
// a failed read omits it and logs at debug level.
func TestTransitionAddress_LookupByID(t *testing.T) {
	ctx := context.Background()
	db := addressDB(t)
	app, m := decisionModule(t, db)
	d := mustCreateDecision(t, m, map[string]any{"Title": "event"})
	if got := app.transitionAddress(ctx, statusTransition{typeName: "Decision", id: d.ID}); got != "D1" {
		t.Errorf("unloaded transition address = %q, want D1", got)
	}
	if got := app.transitionAddress(ctx, statusTransition{typeName: "Decision", id: d.ID, item: d}); got != "D1" {
		t.Errorf("loaded transition address = %q, want D1", got)
	}
	if got := app.transitionAddress(ctx, statusTransition{typeName: "Post", id: "x"}); got != "" {
		t.Errorf("a type with no address = %q", got)
	}
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)
	if got := lookupAddressByID(ctx, failAddressDB{db, "SELECT address FROM"}, "Decision", d.ID, false); got != "" {
		t.Errorf("failed read = %q, want empty", got)
	}
	if !strings.Contains(buf.String(), "could not be read") {
		t.Errorf("log = %q, want a debug line", buf.String())
	}
}

// An HTTP POST with a legacy field is logged as deprecated use too (once per
// token, field and day), and its response is the item without a warnings key.
func TestTransitionPeriod_HTTPPostLogsDeprecatedUse(t *testing.T) {
	db := addressDB(t)
	app, _ := decisionModule(t, db)
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)
	tok, _ := SignToken(User{ID: "u-http-legacy", Roles: []Role{Author, Editor}}, "supersecretkey16", time.Hour)
	for i := range 2 {
		body := strings.NewReader(fmt.Sprintf(`{"Title":"http %d","decision_number":"OPS-%d"}`, i, i))
		r := httptest.NewRequest(http.MethodPost, "/decisions", body)
		r.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		app.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusCreated || strings.Contains(w.Body.String(), "warnings") {
			t.Fatalf("POST = %d %s, want 201 without warnings", w.Code, w.Body.String())
		}
	}
	if n := strings.Count(buf.String(), "token=u-http-legacy"); n != 1 {
		t.Errorf("deprecation log lines for the HTTP caller = %d, want 1:\n%s", n, buf.String())
	}
}

// During the transition a caller that created a record with its own legacy
// value finds it again by that value, which now sits in external_ref: through
// resolveItem, transition_item, the packet anchor and the goal context. Two
// records holding the value are an error naming the count; a match is logged.
func TestLegacyFallback_MatchesExternalRef(t *testing.T) {
	ctx := context.Background()
	db := addressDB(t)
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	RegisterOrchestrationTypes(app, db)
	_ = app.Handler()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	goals := findModule[*Goal](t, app)
	created, err := goals.MCPCreate(authorCtx(), map[string]any{"goal_id": "T-own", "Description": "own number"})
	if err != nil {
		t.Fatal(err)
	}
	g := created.(map[string]any)
	if g["external_ref"] != "T-own" || g["goal_id"] != g["address"] {
		t.Fatalf("created = %v, want T-own in external_ref and goal_id = address", g)
	}
	if got, err := goals.resolveItem(authorCtx(), "T-own"); err != nil || got.ID != g["ID"] {
		t.Errorf("resolveItem(T-own) = %v %v", got, err)
	}
	if gc, err := QueryGoalContext(ctx, db, nil, "T-own"); err != nil || gc.Goal.ID != g["ID"] {
		t.Errorf("QueryGoalContext(T-own) = %v %v", gc, err)
	}
	if pkt, err := BuildContextPacket(ctx, db, nil, "https://example.com", "test", "goal", "T-own", 1); err != nil || pkt.Anchor.ID != g["address"] {
		t.Errorf("packet anchor(T-own) = %v %v", pkt, err)
	}
	if _, err := app.TransitionItemVia(TrustedRead(authorCtx()), "mcp", "Goal", "T-own", "active", ""); err != nil && errors.Is(err, ErrNotFound) {
		t.Errorf("transition_item(T-own) not found: %v", err)
	}
	if !strings.Contains(buf.String(), "a lookup by a legacy value") {
		t.Errorf("log = %q, want the legacy lookup noted", buf.String())
	}

	// A second record with the same external_ref makes the value ambiguous.
	if _, err := goals.MCPCreate(authorCtx(), map[string]any{"Description": "same ref", "external_ref": "T-own"}); err != nil {
		t.Fatal(err)
	}
	if _, err := goals.resolveItem(authorCtx(), "T-own"); err == nil || !strings.Contains(err.Error(), "matches 2") {
		t.Errorf("ambiguous lookup: %v, want an error naming 2", err)
	}

	// amends: an Amendment sent with its own number is found by it for a backfill.
	ams := findModule[*Amendment](t, app)
	if _, err := ams.MCPCreate(authorCtx(), map[string]any{"amendment_number": "A900", "Summary": "s"}); err != nil {
		t.Fatal(err)
	}
	if id, err := resolveAmendmentID(ctx, db, "A900"); err != nil || id == "" {
		t.Errorf("resolveAmendmentID(A900) = %q %v, want found through external_ref", id, err)
	}
	if _, err := legacyLookupID(ctx, db, "Signal", "smeldr_signals", "x", nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("a type with no legacy field: %v, want ErrNotFound", err)
	}
	if _, err := legacyLookupID(ctx, failAddressDB{db, "LIMIT 2"}, "Goal", "smeldr_goals", "T-own", nil); err == nil {
		t.Error("a failing read: want an error")
	}
}
