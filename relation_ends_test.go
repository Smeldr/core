package smeldr

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// Relations require two real ends (D113 point 3, design/record-shape-v1.md
// P5): assert, propose and observe resolve both ends under the caller's read
// rules and store them by ID.

type relationEndsFixture struct {
	app    *App
	rels   *RelationStore
	rs     *RoleStore
	a, b   *DynamicNode // invoices: the reader reads a only
	pub    *DynamicNode // a published article, readable by no grant of the reader
	reader Context
	global Context
}

func newRelationEndsFixture(t *testing.T) relationEndsFixture {
	t.Helper()
	ctx := context.Background()
	app, db := dynamicAddressApp(t)
	setupTokensTable(t, db)
	if err := migrateGovernance(ctx, db); err != nil {
		t.Fatal(err)
	}
	rs := NewRoleStore(db)
	if err := app.Governance(rs); err != nil {
		t.Fatal(err)
	}
	if err := CreateRelationTables(db); err != nil {
		t.Fatal(err)
	}
	rels, err := NewRelationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := rels.UpsertKind(ctx, RelationKindDef{TypeName: "mentions", Label: "Mentions", Mode: "asserted", Directional: true}); err != nil {
		t.Fatal(err)
	}
	if err := rels.UpsertKind(ctx, RelationKindDef{TypeName: "invoice_link", Label: "Invoice link", Mode: "asserted", Directional: true,
		TypePairs: json.RawMessage(`[{"source_type":"invoice","target_type":"invoice"}]`)}); err != nil {
		t.Fatal(err)
	}
	app.Relations(rels)
	for _, s := range []*ContentTypeSchema{schemaFor("invoice", "", ""), schemaFor("article", "/articles", "")} {
		if _, err := app.DefineContentType(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	invoices, _ := app.DynamicContentRepo("invoice")
	articles, _ := app.DynamicContentRepo("article")
	seed := TrustedRead(ctx)
	f := relationEndsFixture{app: app, rels: rels, rs: rs}
	if f.a, err = invoices.CreateDraft(seed, map[string]any{"Title": "a"}); err != nil {
		t.Fatal(err)
	}
	if f.b, err = invoices.CreateDraft(seed, map[string]any{"Title": "b"}); err != nil {
		t.Fatal(err)
	}
	if f.pub, err = articles.CreateDraft(seed, map[string]any{"Title": "public"}); err != nil {
		t.Fatal(err)
	}
	if err := articles.SetStatus(seed, f.pub.ID, Published); err != nil {
		t.Fatal(err)
	}
	if err := RegisterScopedReaderRole(ctx, rs); err != nil {
		t.Fatal(err)
	}
	if err := rs.DefineRole(ctx, RoleDefinition{Name: "relater", Operations: []string{"create"}, ScopeMode: ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.Grant(ctx, RoleGrant{TokenID: "reader", RoleName: "scoped-reader", ScopeStatic: []string{"invoice:" + f.a.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.Grant(ctx, RoleGrant{TokenID: "reader", RoleName: "relater"}); err != nil {
		t.Fatal(err)
	}
	govGrant(t, rs, "global", "admin")
	f.reader = NewTestContext(User{ID: "reader"})
	f.global = NewTestContext(User{ID: "global"})
	return f
}

// relationWrite is one of the three relation writes.
type relationWrite func(rs *RelationStore, ctx context.Context, st, s, tt, tg, kind string) (RelationEdge, error)

var relationWrites = map[string]relationWrite{
	"assert": func(rs *RelationStore, ctx context.Context, st, s, tt, tg, kind string) (RelationEdge, error) {
		return rs.MCPAssertRelation(ctx, st, s, tt, tg, kind, nil, nil, nil, nil)
	},
	"propose": func(rs *RelationStore, ctx context.Context, st, s, tt, tg, kind string) (RelationEdge, error) {
		return rs.MCPProposeRelation(ctx, st, s, tt, tg, kind, nil, nil, nil, nil)
	},
	"observe": func(rs *RelationStore, ctx context.Context, st, s, tt, tg, kind string) (RelationEdge, error) {
		return rs.MCPObserveRelation(ctx, st, s, tt, tg, kind, nil, nil, nil, nil)
	},
}

func (f relationEndsFixture) edgeCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.app.cfg.DB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM smeldr_relations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRelationEnds_Missing(t *testing.T) {
	f := newRelationEndsFixture(t)
	for name, write := range relationWrites {
		for _, c := range []struct{ end, s, tg string }{
			{"source", "nope", f.a.ID},
			{"target", f.a.ID, "nope"},
		} {
			_, err := write(f.rels, f.global, "invoice", c.s, "invoice", c.tg, "mentions")
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("%s with a missing %s: %v; want ErrNotFound", name, c.end, err)
			}
		}
	}
	if n := f.edgeCount(t); n != 0 {
		t.Errorf("%d edges stored; want none", n)
	}
}

func TestRelationEnds_UnregisteredType(t *testing.T) {
	f := newRelationEndsFixture(t)
	if _, err := f.rels.MCPAssertRelation(f.global, "invoice", f.a.ID, "Phantom", "x", "mentions", nil, nil, nil, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("an end of an unregistered type: %v; want ErrNotFound", err)
	}
}

func TestRelationEnds_HiddenEqualsMissing(t *testing.T) {
	f := newRelationEndsFixture(t)
	_, hidden := f.rels.MCPAssertRelation(f.reader, "invoice", f.a.ID, "invoice", f.b.ID, "mentions", nil, nil, nil, nil)
	_, missing := f.rels.MCPAssertRelation(f.reader, "invoice", f.a.ID, "invoice", "no-such-id", "mentions", nil, nil, nil, nil)
	if !errors.Is(hidden, ErrNotFound) || !errors.Is(missing, ErrNotFound) || hidden.Error() != missing.Error() {
		t.Errorf("hidden %v, missing %v; want the same ErrNotFound", hidden, missing)
	}
	if _, err := f.rels.MCPAssertRelation(f.reader, "invoice", f.a.ID, "invoice", f.a.ID, "mentions", nil, nil, nil, nil); err != nil {
		t.Errorf("both ends readable: %v", err)
	}
}

// TestRelationEnds_PublishedWebContentNeedsAGrant: the ends resolve with the
// caller's read rule, which has no public web-content clause, so a scoped
// caller cannot relate to published content it holds no grant for.
func TestRelationEnds_PublishedWebContentNeedsAGrant(t *testing.T) {
	f := newRelationEndsFixture(t)
	if _, err := f.rels.MCPAssertRelation(f.reader, "invoice", f.a.ID, "article", f.pub.ID, "mentions", nil, nil, nil, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("scoped reader to published web content: %v; want ErrNotFound", err)
	}
	if _, err := f.rels.MCPAssertRelation(f.global, "invoice", f.a.ID, "article", f.pub.ID, "mentions", nil, nil, nil, nil); err != nil {
		t.Errorf("reader of everything: %v", err)
	}
}

func TestRelationEnds_ResolvedToIDs(t *testing.T) {
	f := newRelationEndsFixture(t)
	if f.b.Address == "" {
		t.Fatal("invoice b has no address")
	}
	edge, err := f.rels.MCPAssertRelation(f.global, "invoice", f.a.Slug, "invoice", f.b.Address, "mentions", nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if edge.SourceID != f.a.ID || edge.TargetID != f.b.ID {
		t.Errorf("edge %s -> %s; want IDs %s -> %s", edge.SourceID, edge.TargetID, f.a.ID, f.b.ID)
	}
	stored, err := f.rels.GetBySource(context.Background(), "invoice", f.a.ID, "mentions")
	if err != nil || len(stored) != 1 || stored[0].TargetID != f.b.ID {
		t.Errorf("stored %+v %v; want one edge to %s", stored, err, f.b.ID)
	}
}

func TestRelationEnds_ScopeFailure(t *testing.T) {
	f := newRelationEndsFixture(t)
	if _, err := f.app.cfg.DB.ExecContext(context.Background(), `DROP TABLE smeldr_role_grants`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rels.MCPAssertRelation(f.reader, "invoice", f.a.ID, "invoice", f.a.ID, "mentions", nil, nil, nil, nil); !errors.Is(err, ErrInternal) {
		t.Errorf("scope load failure: %v; want ErrInternal", err)
	}
	if n := f.edgeCount(t); n != 0 {
		t.Errorf("%d edges stored; want none", n)
	}
}

func TestRelationEnds_NoAppUnchanged(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateRelationTables(db); err != nil {
		t.Fatal(err)
	}
	rels, err := NewRelationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := rels.UpsertKind(context.Background(), RelationKindDef{TypeName: "mentions", Label: "Mentions", Mode: "asserted", Directional: true}); err != nil {
		t.Fatal(err)
	}
	edge, err := rels.MCPAssertRelation(context.Background(), "Anything", "a-slug", "Other", "x", "mentions", nil, nil, nil, nil)
	if err != nil || edge.SourceID != "a-slug" || edge.TargetID != "x" {
		t.Errorf("store without an App: %+v %v; want the ends stored as given", edge, err)
	}
}

func TestRelationEnds_KindFirst(t *testing.T) {
	f := newRelationEndsFixture(t)
	called := false
	f.rels.resolveEnd = func(context.Context, string, string) (string, error) {
		called = true
		return "", nil
	}
	for name, write := range relationWrites {
		if _, err := write(f.rels, f.global, "invoice", "nope", "invoice", "nope", "no_such_kind"); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s with an unknown kind: %v; want ErrNotFound", name, err)
		}
	}
	if called {
		t.Error("an end was resolved for an unknown kind")
	}
}

func TestRelationEnds_TypePairsStillChecked(t *testing.T) {
	f := newRelationEndsFixture(t)
	_, err := f.rels.MCPAssertRelation(f.global, "article", f.pub.ID, "invoice", f.a.ID, "invoice_link", nil, nil, nil, nil)
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("type pair not allowed for two real ends: %v; want the type_pairs validation error", err)
	}
	if _, err := f.rels.MCPAssertRelation(f.global, "invoice", f.a.ID, "invoice", f.b.ID, "invoice_link", nil, nil, nil, nil); err != nil {
		t.Errorf("allowed pair: %v", err)
	}
}
