package smeldr

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Per-item read rules for dynamic types, Withdraw and Signal subjects
// (core-read-scoped-dynamic-and-signal-subject).

type scopedFixture struct {
	app    *App
	raw    DB
	db     *stmtDB
	rs     *RoleStore
	tasks  *Module[*Task]
	reader Context
	global Context
}

// newScopedFixture is an App on a statement-recording handle with governance,
// a dynamic record type "invoice", and a reader that reads only what each test
// grants it.
func newScopedFixture(t *testing.T) scopedFixture {
	t.Helper()
	ctx := context.Background()
	raw := addressDB(t)
	setupTokensTable(t, raw)
	if err := CreateBlockTables(raw); err != nil {
		t.Fatal(err)
	}
	if err := CreateSchemaTable(raw); err != nil {
		t.Fatal(err)
	}
	db := &stmtDB{DB: raw}
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	app.ServeDynamicContent()
	RegisterOrchestrationTypes(app, db)
	rs := NewRoleStore(db)
	if err := app.Governance(rs); err != nil {
		t.Fatal(err)
	}
	_ = app.Handler()
	if _, err := app.DefineContentType(ctx, schemaFor("invoice", "", "")); err != nil {
		t.Fatal(err)
	}
	if err := RegisterScopedReaderRole(ctx, rs); err != nil {
		t.Fatal(err)
	}
	govGrant(t, rs, "global", "admin")
	return scopedFixture{
		app: app, raw: raw, db: db, rs: rs, tasks: findModule[*Task](t, app),
		reader: NewTestContext(User{ID: "reader"}), global: NewTestContext(User{ID: "global"}),
	}
}

func (f scopedFixture) grant(t *testing.T, patterns ...string) {
	t.Helper()
	if _, err := f.rs.Grant(context.Background(), RoleGrant{TokenID: "reader", RoleName: "scoped-reader", ScopeStatic: patterns}); err != nil {
		t.Fatal(err)
	}
}

// sameStatements runs hidden and missing and requires the same error and the
// same statement sequence.
func (f scopedFixture) sameStatements(t *testing.T, name string, hidden, missing func() error) {
	t.Helper()
	run := func(call func() error) (string, error) {
		f.db.stmts = nil
		err := call()
		return strings.Join(f.db.stmts, "\n"), err
	}
	h, herr := run(hidden)
	m, merr := run(missing)
	if !errors.Is(herr, ErrNotFound) || !errors.Is(merr, ErrNotFound) || herr.Error() != merr.Error() {
		t.Errorf("%s: hidden %v, missing %v; want the same not-found", name, herr, merr)
	}
	if h != m {
		t.Errorf("%s: hidden ran\n  %s\nmissing ran\n  %s", name, strings.ReplaceAll(h, "\n", "\n  "), strings.ReplaceAll(m, "\n", "\n  "))
	}
}

func TestDynamicResolve_HiddenEqualsMissing(t *testing.T) {
	f := newScopedFixture(t)
	repo, _ := f.app.DynamicContentRepo("invoice")
	trusted := TrustedRead(context.Background())
	seen, _ := repo.CreateDraft(trusted, map[string]any{"Title": "seen"})
	hidden, _ := repo.CreateDraft(trusted, map[string]any{"Title": "hidden"})
	f.grant(t, "invoice:"+seen.ID)
	if _, err := f.app.ReadScope(f.reader); err != nil {
		t.Fatal(err)
	}
	resolve := func(ident string) func() error {
		return func() error { _, err := repo.Resolve(f.reader, ident); return err }
	}
	for _, s := range []struct{ name, hidden, missing string }{
		{"address", hidden.Address, "INV999"},
		{"slug", hidden.Slug, "no-such-invoice"},
		{"ID", hidden.ID, NewID()},
	} {
		f.sameStatements(t, "Resolve by "+s.name, resolve(s.hidden), resolve(s.missing))
	}
	if n, err := repo.Resolve(f.reader, seen.Address); err != nil || n.ID != seen.ID {
		t.Errorf("readable invoice = %v %v", n, err)
	}
	if n, err := repo.Resolve(f.global, hidden.Slug); err != nil || n.ID != hidden.ID {
		t.Errorf("global reader = %v %v", n, err)
	}
}

func TestDynamicListPage_ReadableOnly(t *testing.T) {
	f := newScopedFixture(t)
	repo, _ := f.app.DynamicContentRepo("invoice")
	trusted := TrustedRead(context.Background())
	var ids []string
	for _, title := range []string{"a", "b", "c", "d", "e"} {
		n, err := repo.CreateDraft(trusted, map[string]any{"Title": title})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, n.ID)
	}
	// Readable rows interleaved with hidden ones: the first, third and fifth.
	f.grant(t, "invoice:"+ids[0], "invoice:"+ids[2], "invoice:"+ids[4])

	seen := map[string]bool{}
	for page := 1; page <= 3; page++ {
		items, total, err := repo.ListPage(f.reader, ListOptions{Page: page, PerPage: 1, OrderBy: "created_at"})
		if err != nil || total != 3 || len(items) != 1 {
			t.Fatalf("page %d = %d items, total %d %v; want 1 of 3", page, len(items), total, err)
		}
		seen[items[0]["ID"].(string)] = true
	}
	if !seen[ids[0]] || !seen[ids[2]] || !seen[ids[4]] {
		t.Errorf("pages returned %v; want the three readable invoices", seen)
	}
	if items, total, err := repo.ListPage(f.reader, ListOptions{Page: 4, PerPage: 1}); err != nil || total != 3 || len(items) != 0 {
		t.Errorf("past the end = %d items, total %d %v", len(items), total, err)
	}
	if items, err := repo.List(f.reader, ListOptions{}); err != nil || len(items) != 3 {
		t.Errorf("List = %d %v; want 3", len(items), err)
	}
	all, total, err := repo.ListPage(f.global, ListOptions{Page: 2, PerPage: 2})
	if err != nil || total != 5 || len(all) != 2 {
		t.Errorf("global page = %d items, total %d %v; want 2 of 5", len(all), total, err)
	}
	// No scope attached on a handle no App registered: fail closed.
	stray := NewDynamicTypeRepo(&stmtDB{DB: f.raw}, "invoice", nil)
	if _, err := stray.List(context.Background(), ListOptions{}); !errors.Is(err, ErrInternal) {
		t.Errorf("unregistered handle List: %v; want ErrInternal", err)
	}
	if _, err := stray.Resolve(context.Background(), "x"); !errors.Is(err, ErrInternal) {
		t.Errorf("unregistered handle Resolve: %v; want ErrInternal", err)
	}
	if _, _, err := stray.ListPage(TrustedRead(context.Background()), ListOptions{}); err != nil {
		t.Errorf("TrustedRead ListPage: %v", err)
	}
}

func TestDynamicListPage_PublishedWebContentIsPublic(t *testing.T) {
	f := newScopedFixture(t)
	ctx := TrustedRead(context.Background())
	if _, err := f.app.DefineContentType(context.Background(), schemaFor("article", "/articles", "")); err != nil {
		t.Fatal(err)
	}
	repo, _ := f.app.DynamicContentRepo("article")
	published, _ := repo.CreateDraft(ctx, map[string]any{"Title": "out"})
	if err := repo.SetStatus(ctx, published.ID, Published); err != nil {
		t.Fatal(err)
	}
	draft, _ := repo.CreateDraft(ctx, map[string]any{"Title": "draft"})
	items, total, err := repo.ListPage(f.reader, ListOptions{})
	if err != nil || total != 1 || items[0]["ID"] != published.ID {
		t.Errorf("reader sees %v total %d %v; want only the published article", items, total, err)
	}
	if _, err := repo.Resolve(f.reader, draft.Slug); !errors.Is(err, ErrNotFound) {
		t.Errorf("a draft article: %v; want not found", err)
	}
}

func TestWithdraw_HiddenEqualsMissing(t *testing.T) {
	g := newGraphFixture(t)
	db := &stmtDB{DB: g.db}
	rels, err := NewRelationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := g.app.ReadScope(g.reader)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithReadScope(g.reader, scope)
	page, err := g.rels.ListRelations(TrustedRead(context.Background()), RelationListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var hiddenEdge, readableEdge string
	for _, e := range page.Edges {
		switch {
		case e.TargetID == g.hidden.ID:
			hiddenEdge = e.ID
		case e.SourceID == g.far.ID:
			readableEdge = e.ID
		}
	}
	run := func(id string) (string, error) {
		db.stmts = nil
		err := rels.Withdraw(ctx, id, "test")
		return strings.Join(db.stmts, "\n"), err
	}
	h, herr := run(hiddenEdge)
	m, merr := run(NewID())
	if !errors.Is(herr, ErrNotFound) || !errors.Is(merr, ErrNotFound) || h != m {
		t.Errorf("hidden %v (%s), missing %v (%s); want the same not-found and statements", herr, h, merr, m)
	}
	// End the hidden edge as a trusted caller: an ended hidden edge is still
	// not-found for the reader, never "already ended".
	if err := g.rels.Withdraw(TrustedRead(context.Background()), hiddenEdge, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(hiddenEdge); !errors.Is(err, ErrNotFound) {
		t.Errorf("ended hidden edge: %v; want not found", err)
	}
	// The readable edge: withdrawn once, then a conflict.
	if err := rels.Withdraw(ctx, readableEdge, "test"); err != nil {
		t.Fatalf("withdraw readable: %v", err)
	}
	if err := rels.Withdraw(ctx, readableEdge, "test"); !errors.Is(err, ErrConflict) {
		t.Errorf("withdraw again: %v; want conflict", err)
	}
	// A scope load failure is internal, never not-found.
	if _, err := g.db.Exec(`DROP TABLE smeldr_role_grants`); err != nil {
		t.Fatal(err)
	}
	if err := g.rels.Withdraw(g.reader, readableEdge, "test"); !errors.Is(err, ErrInternal) {
		t.Errorf("scope failure: %v; want ErrInternal", err)
	}
}

// insertSubjectSignal stores a Signal about (subjectType, subjectID).
func insertSubjectSignal(t *testing.T, db DB, subjectType, subjectID string) *Signal {
	t.Helper()
	s := &Signal{Sender: "core", Receiver: "architect", SignalType: "note", SubjectType: subjectType, SubjectID: subjectID}
	if err := InsertSignal(context.Background(), db, s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSignalSubject_HiddenSubjectHidesTheSignal(t *testing.T) {
	f := newScopedFixture(t)
	trusted := TrustedRead(authorCtx()).(Context)
	seenTask, _ := f.tasks.MCPCreate(trusted, map[string]any{"Description": "seen"})
	hiddenTask, _ := f.tasks.MCPCreate(trusted, map[string]any{"Description": "hidden"})
	seen, hidden := asItem[*Task](seenTask), asItem[*Task](hiddenTask)
	f.grant(t, "Task:"+seen.ID, "Signal:*")
	if _, err := f.app.ReadScope(f.reader); err != nil {
		t.Fatal(err)
	}
	aboutSeen := insertSubjectSignal(t, f.raw, "Task", seen.ID)
	aboutHidden := insertSubjectSignal(t, f.raw, "Task", hidden.ID)
	bySlug := insertSubjectSignal(t, f.raw, "Task", seen.Slug)
	plain := insertSubjectSignal(t, f.raw, "", "")
	signals := findModule[*Signal](t, f.app)

	// MCPGet and TransitionItemVia: a Signal whose subject is hidden is a miss.
	get := func(ident string) func() error {
		return func() error { _, err := signals.MCPGet(f.reader, ident); return err }
	}
	move := func(ident string) func() error {
		return func() error {
			_, err := f.app.TransitionItemVia(f.reader, "mcp", "Signal", ident, "read", "")
			return err
		}
	}
	for _, s := range []struct{ name, hidden, missing string }{
		{"address", aboutHidden.Address, "S99999"},
		{"slug", aboutHidden.Slug, "no-such-signal"},
		{"ID", aboutHidden.ID, NewID()},
	} {
		f.sameStatements(t, "MCPGet by "+s.name, get(s.hidden), get(s.missing))
		f.sameStatements(t, "TransitionItemVia by "+s.name, move(s.hidden), move(s.missing))
	}
	if _, err := signals.MCPGet(f.reader, aboutSeen.Slug); err != nil {
		t.Errorf("a Signal about a readable Task: %v", err)
	}
	if _, err := signals.MCPGet(f.reader, bySlug.Slug); !errors.Is(err, ErrNotFound) {
		t.Errorf("a subject held as a slug: %v; want not found (fail-closed)", err)
	}

	// MCPList: only the Signals whose subject is readable, or that have none.
	items, err := signals.MCPList(f.reader)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, it := range items {
		got[it.(*Signal).ID] = true
	}
	if len(got) != 2 || !got[aboutSeen.ID] || !got[plain.ID] {
		t.Errorf("reader list = %v; want the Signal about the readable Task and the plain one", got)
	}
	if all, err := signals.MCPList(f.global); err != nil || len(all) != 4 {
		t.Errorf("global list = %d %v; want 4", len(all), err)
	}
}

func TestSignalSubject_Packet(t *testing.T) {
	g := newGraphFixture(t)
	if _, err := g.rs.Grant(context.Background(), RoleGrant{TokenID: "reader", RoleName: "scoped-reader", ScopeStatic: []string{"Signal:*"}}); err != nil {
		t.Fatal(err)
	}
	aboutHidden := insertSubjectSignal(t, g.db, "Task", g.hidden.ID)
	aboutSeen := insertSubjectSignal(t, g.db, "Task", g.seen.ID)
	_, hiddenErr := BuildContextPacket(g.reader, g.db, g.rels, "https://example.com", "test", "signal", aboutHidden.Slug, 1)
	_, missingErr := BuildContextPacket(g.reader, g.db, g.rels, "https://example.com", "test", "signal", "no-such-signal", 1)
	if !errors.Is(hiddenErr, ErrNotFound) || hiddenErr.Error() != missingErr.Error() {
		t.Errorf("packet anchor hidden by subject %v, missing %v", hiddenErr, missingErr)
	}
	if _, err := BuildContextPacket(g.reader, g.db, g.rels, "https://example.com", "test", "signal", aboutSeen.Slug, 1); err != nil {
		t.Errorf("packet anchor with a readable subject: %v", err)
	}
	// Linked to a readable Task, a Signal whose subject is hidden is left out.
	if err := g.rels.UpsertKind(context.Background(), RelationKindDef{TypeName: "about", Label: "About", Mode: "asserted", Directional: true,
		TypePairs: json.RawMessage(`[{"source_type":"Signal","target_type":"Task"}]`)}); err != nil {
		t.Fatal(err)
	}
	assertTaskEdge(t, g.rels, "about", "Signal", aboutHidden.ID, "Task", g.seen.ID)
	assertTaskEdge(t, g.rels, "about", "Signal", aboutSeen.ID, "Task", g.seen.ID)
	pkt, err := BuildContextPacket(g.reader, g.db, g.rels, "https://example.com", "test", "task", g.seen.Slug, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range pkt.Items {
		if it.Slug == aboutHidden.Slug {
			t.Errorf("the packet holds a Signal whose subject is hidden")
		}
	}
	full, err := BuildContextPacket(g.global, g.db, g.rels, "https://example.com", "test", "task", g.seen.Slug, 1)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range full.Items {
		found = found || it.Slug == aboutHidden.Slug
	}
	if !found {
		t.Error("a global reader lost the Signal")
	}
}

func TestResolveReadableID(t *testing.T) {
	f := newScopedFixture(t)
	trusted := TrustedRead(authorCtx()).(Context)
	seenTask, _ := f.tasks.MCPCreate(trusted, map[string]any{"Description": "seen"})
	hiddenTask, _ := f.tasks.MCPCreate(trusted, map[string]any{"Description": "hidden"})
	seen, hidden := asItem[*Task](seenTask), asItem[*Task](hiddenTask)
	repo, _ := f.app.DynamicContentRepo("invoice")
	inv, _ := repo.CreateDraft(TrustedRead(context.Background()), map[string]any{"Title": "seen"})
	f.grant(t, "Task:"+seen.ID, "invoice:"+inv.ID)
	if _, err := f.app.ReadScope(f.reader); err != nil {
		t.Fatal(err)
	}
	resolve := func(typeName, ident string) func() error {
		return func() error { _, err := f.app.ResolveReadableID(f.reader, typeName, ident); return err }
	}
	for _, s := range []struct{ name, hidden, missing string }{
		{"address", hidden.Address, "T999"},
		{"slug", hidden.Slug, "no-such-task"},
		{"ID", hidden.ID, NewID()},
	} {
		f.sameStatements(t, "ResolveReadableID by "+s.name, resolve("Task", s.hidden), resolve("Task", s.missing))
	}
	for _, ident := range []string{seen.Address, seen.Slug, seen.ID} {
		if id, err := f.app.ResolveReadableID(f.reader, "Task", ident); err != nil || id != seen.ID {
			t.Errorf("readable Task by %q = %q %v", ident, id, err)
		}
	}
	if id, err := f.app.ResolveReadableID(f.reader, "invoice", inv.Address); err != nil || id != inv.ID {
		t.Errorf("readable invoice = %q %v", id, err)
	}
	if id, err := f.app.ResolveReadableID(f.global, "Task", hidden.Slug); err != nil || id != hidden.ID {
		t.Errorf("global reader = %q %v", id, err)
	}
	if _, err := f.app.ResolveReadableID(f.reader, "NoSuchType", "x"); !errors.Is(err, ErrBadRequest) {
		t.Errorf("unregistered type: %v; want ErrBadRequest", err)
	}
	if _, err := f.raw.ExecContext(context.Background(), `DROP TABLE smeldr_role_grants`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.ResolveReadableID(f.reader, "Task", seen.ID); !errors.Is(err, ErrInternal) {
		t.Errorf("scope failure: %v; want ErrInternal", err)
	}
}

func TestReadScopedDynamicSignal_ErrorPaths(t *testing.T) {
	f := newScopedFixture(t)
	ctx := context.Background()
	trusted := TrustedRead(authorCtx()).(Context)
	created, _ := f.tasks.MCPCreate(trusted, map[string]any{"Description": "seen"})
	seen := asItem[*Task](created)
	f.grant(t, "Task:"+seen.ID, "Signal:*")
	about := insertSubjectSignal(t, f.raw, "Task", seen.ID)

	// A Signal with a readable subject resolves through the compiled lookup.
	if _, err := f.app.TransitionItemVia(f.reader, "mcp", "Signal", about.Slug, "read", ""); errors.Is(err, ErrNotFound) {
		t.Errorf("a Signal with a readable subject: %v; want past the lookup", err)
	}
	if id, err := f.app.ResolveReadableID(f.reader, "Signal", about.Address); err != nil || id != about.ID {
		t.Errorf("ResolveReadableID Signal = %q %v", id, err)
	}
	if _, err := f.app.ResolveReadableID(f.reader, "invoice", "INV404"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a missing invoice: %v; want not found", err)
	}

	// The packet's Signal subject read: empty, a query failure, a scan failure.
	ok := func(string, string) bool { return true }
	if got, err := signalsWithReadableSubject(ctx, f.raw, nil, ok); err != nil || len(got) != 0 {
		t.Errorf("no ids = %v %v", got, err)
	}
	if _, err := signalsWithReadableSubject(ctx, &govQueryFailDB{DB: f.raw, failOn: "smeldr_signals"}, []string{about.ID}, ok); !errors.Is(err, ErrInternal) {
		t.Errorf("query failure: %v; want ErrInternal", err)
	}
	if _, err := signalsWithReadableSubject(ctx, &govQueryNullRowsDB{DB: f.raw, nullOn: "smeldr_signals"}, []string{about.ID}, ok); !errors.Is(err, ErrInternal) {
		t.Errorf("scan failure: %v; want ErrInternal", err)
	}

	// ListPage's COUNT failure for a reader of everything.
	repo := NewDynamicTypeRepo(&govQueryRowFailDB{DB: f.raw, failOn: "COUNT(*)"}, "invoice", nil)
	if _, _, err := repo.ListPage(TrustedRead(ctx), ListOptions{}); err == nil {
		t.Error("ListPage on a COUNT failure: no error")
	}

	// Withdraw's read of the edge's ends fails: internal, never not-found.
	if err := CreateRelationTables(f.raw); err != nil {
		t.Fatal(err)
	}
	rels, err := NewRelationStore(&govQueryRowFailDB{DB: f.raw, failOn: "SELECT source_type, source_id, target_type"})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := f.app.ReadScope(f.reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := rels.Withdraw(WithReadScope(f.reader, scope), NewID(), "test"); !errors.Is(err, ErrInternal) {
		t.Errorf("Withdraw on a read failure: %v; want ErrInternal", err)
	}
}
