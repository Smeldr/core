package smeldr

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Per-item read rules on the graph surfaces (core-read-surfaces 3b): an item
// the caller may not read is absent from every graph read, and a hidden item
// answers exactly like a missing one.

type graphFixture struct {
	readSurfaceFixture
	rels *RelationStore
	far  *Task // readable, reached from seen only through hidden
}

// newGraphFixture adds a third Task "far" the reader may read, and the edges
// seen -> hidden -> far and far -> seen (all depends_on).
func newGraphFixture(t *testing.T) graphFixture {
	t.Helper()
	f := graphFixture{readSurfaceFixture: newReadSurfaceFixture(t)}
	ctx := context.Background()
	if err := CreateRelationTables(f.db); err != nil {
		t.Fatal(err)
	}
	rels, err := NewRelationStore(f.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterOrchestrationRelationKinds(ctx, rels); err != nil {
		t.Fatal(err)
	}
	f.rels = rels
	out, err := f.tasks.MCPCreate(TrustedRead(authorCtx()).(Context), map[string]any{"Description": "far", "Band": "core"})
	if err != nil {
		t.Fatal(err)
	}
	f.far = asItem[*Task](out)
	if _, err := f.rs.Grant(ctx, RoleGrant{TokenID: "reader", RoleName: "scoped-reader", ScopeStatic: []string{"Task:" + f.far.ID}}); err != nil {
		t.Fatal(err)
	}
	assertTaskEdge(t, rels, "depends_on", "Task", f.seen.ID, "Task", f.hidden.ID)
	assertTaskEdge(t, rels, "depends_on", "Task", f.hidden.ID, "Task", f.far.ID)
	assertTaskEdge(t, rels, "depends_on", "Task", f.far.ID, "Task", f.seen.ID)
	return f
}

func edgePairs(edges []RelationEdge) string {
	var b strings.Builder
	for _, e := range edges {
		b.WriteString(e.SourceID + ">" + e.TargetID + " ")
	}
	return b.String()
}

func TestReadSurfacesGraph_RelationsKeepBothEndsReadable(t *testing.T) {
	f := newGraphFixture(t)
	want := f.far.ID + ">" + f.seen.ID + " "
	got, err := f.rels.MCPGetRelations(f.reader, "Task", f.seen.ID, "both", "")
	if err != nil || edgePairs(got) != want {
		t.Errorf("reader relations of seen = %q %v; want only %q", edgePairs(got), err, want)
	}
	all, err := f.rels.MCPGetRelations(f.global, "Task", f.seen.ID, "both", "")
	if err != nil || len(all) != 2 {
		t.Errorf("global relations of seen = %d %v; want 2", len(all), err)
	}
	hidden, err := f.rels.MCPGetRelations(f.reader, "Task", f.hidden.ID, "both", "")
	if err != nil || len(hidden) != 0 {
		t.Errorf("reader relations of hidden = %v %v; want none, as for a missing item", hidden, err)
	}
	impact, err := f.rels.MCPPreviewImpact(f.reader, "Task", f.seen.ID)
	if err != nil || edgePairs(impact) != want {
		t.Errorf("reader impact of seen = %q %v; want %q", edgePairs(impact), err, want)
	}
	if impact, _ := f.rels.MCPPreviewImpact(f.reader, "Task", f.far.ID); len(impact) != 0 {
		t.Errorf("reader impact of far shows the hidden dependant: %q", edgePairs(impact))
	}
}

func TestReadSurfacesGraph_ListRelationsCountsReadableOnly(t *testing.T) {
	f := newGraphFixture(t)
	page, err := f.rels.ListRelations(f.reader, RelationListOptions{})
	if err != nil || page.Total != 1 || len(page.Edges) != 1 || page.Edges[0].SourceID != f.far.ID {
		t.Errorf("reader page = %q total %d %v; want the one readable edge", edgePairs(page.Edges), page.Total, err)
	}
	past, err := f.rels.ListRelations(f.reader, RelationListOptions{Offset: 1, Limit: 1})
	if err != nil || past.Total != 1 || len(past.Edges) != 0 {
		t.Errorf("reader page past the end = %d edges, total %d %v; want 0 and 1", len(past.Edges), past.Total, err)
	}
	all, err := f.rels.ListRelations(f.global, RelationListOptions{Limit: 2})
	if err != nil || all.Total != 3 || len(all.Edges) != 2 {
		t.Errorf("global page = %d edges, total %d %v; want 2 and 3", len(all.Edges), all.Total, err)
	}
	ended, err := f.rels.ListRelations(f.reader, RelationListOptions{IncludeEnded: true})
	if err != nil || ended.Total != 1 {
		t.Errorf("reader page with ended = total %d %v; want 1", ended.Total, err)
	}
}

func TestReadSurfacesGraph_ReachabilityDoesNotWalkThroughHidden(t *testing.T) {
	f := newGraphFixture(t)
	got, err := f.rels.ReachabilityBounded(f.reader, "Task", f.seen.ID, "", "outgoing", 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, ring := range got.Rings {
		if len(ring.Items) != 0 {
			t.Errorf("ring %d = %+v; far is reachable only through hidden", ring.Depth, ring.Items)
		}
	}
	all, err := f.rels.Reachability(f.global, "Task", f.seen.ID, "", "outgoing", 2)
	if err != nil || len(all.Rings[0].Items) != 1 || all.Rings[1].Items[0].ID != f.far.ID {
		t.Errorf("global walk = %+v %v; want hidden then far", all, err)
	}
	// A hidden anchor reads as one with no relations.
	hidden, err := f.rels.Reachability(f.reader, "Task", f.hidden.ID, "", "both", 1)
	if err != nil || len(hidden.Rings[0].Items) != 0 {
		t.Errorf("hidden anchor = %+v %v; want an empty ring", hidden, err)
	}
	// The cap counts readable nodes only: far is the one readable neighbour
	// of seen over both directions, so a cap of 1 is no cut.
	capped, err := f.rels.ReachabilityBounded(f.reader, "Task", f.seen.ID, "", "both", 1, 1)
	if err != nil || capped.Cut != nil || len(capped.Rings[0].Items) != 1 {
		t.Errorf("capped walk = %+v %v; want far, no cut", capped, err)
	}
}

// Requirement 1 (timing) on the graph reads: a hidden and a missing item run
// the same statements.
func TestReadSurfacesGraph_HiddenAndMissingRunTheSameStatements(t *testing.T) {
	f := newGraphFixture(t)
	db := &stmtDB{DB: f.db}
	rels, err := NewRelationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := f.app.ReadScope(f.reader)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithReadScope(f.reader, scope)
	missing := NewID()
	calls := map[string]func(id string) (int, error){
		"relations": func(id string) (int, error) {
			e, err := rels.MCPGetRelations(ctx, "Task", id, "both", "")
			return len(e), err
		},
		"impact": func(id string) (int, error) {
			e, err := rels.MCPPreviewImpact(ctx, "Task", id)
			return len(e), err
		},
		"reachability": func(id string) (int, error) {
			r, err := rels.Reachability(ctx, "Task", id, "", "both", 3)
			n := 0
			for _, ring := range r.Rings {
				n += len(ring.Items)
			}
			return n, err
		},
	}
	for name, call := range calls {
		db.stmts = nil
		hn, herr := call(f.hidden.ID)
		h := strings.Join(db.stmts, "\n")
		db.stmts = nil
		mn, merr := call(missing)
		m := strings.Join(db.stmts, "\n")
		if herr != nil || merr != nil || hn != 0 || mn != 0 {
			t.Errorf("%s: hidden %d %v, missing %d %v; want nothing for both", name, hn, herr, mn, merr)
		}
		if h != m {
			t.Errorf("%s: hidden ran\n  %s\nmissing ran\n  %s", name, h, m)
		}
	}
}

// Without a user, under governance, the package functions read nothing: the
// caller's own scope is empty, never everything.
func TestReadSurfacesGraph_NoCallerReadsNothing(t *testing.T) {
	f := newGraphFixture(t)
	bare := context.Background()
	if e, err := f.rels.MCPGetRelations(bare, "Task", f.seen.ID, "both", ""); err != nil || len(e) != 0 {
		t.Errorf("MCPGetRelations = %d %v; want none", len(e), err)
	}
	if e, err := f.rels.MCPPreviewImpact(bare, "Task", f.seen.ID); err != nil || len(e) != 0 {
		t.Errorf("MCPPreviewImpact = %d %v; want none", len(e), err)
	}
	if p, err := f.rels.ListRelations(bare, RelationListOptions{}); err != nil || p.Total != 0 {
		t.Errorf("ListRelations total = %d %v; want 0", p.Total, err)
	}
	if r, err := f.rels.Reachability(bare, "Task", f.far.ID, "", "both", 1); err != nil || len(r.Rings[0].Items) != 0 {
		t.Errorf("Reachability = %+v %v; want an empty ring", r, err)
	}
	if _, err := BuildContextPacket(bare, f.db, f.rels, "https://example.com", "test", "task", f.seen.Slug, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("BuildContextPacket: %v; want ErrNotFound", err)
	}
	// TrustedRead is the deliberate way past the rules.
	if e, err := f.rels.MCPGetRelations(TrustedRead(bare), "Task", f.seen.ID, "both", ""); err != nil || len(e) != 2 {
		t.Errorf("TrustedRead MCPGetRelations = %d %v; want 2", len(e), err)
	}
}

func TestReadSurfacesGraph_ScopeLoadFailureIsInternal(t *testing.T) {
	f := newGraphFixture(t)
	if _, err := f.db.Exec(`DROP TABLE smeldr_role_grants`); err != nil {
		t.Fatal(err)
	}
	checks := map[string]error{}
	_, checks["relations"] = f.rels.MCPGetRelations(f.reader, "Task", f.seen.ID, "both", "")
	_, checks["impact"] = f.rels.MCPPreviewImpact(f.reader, "Task", f.seen.ID)
	_, checks["list"] = f.rels.ListRelations(f.reader, RelationListOptions{})
	_, checks["reachability"] = f.rels.Reachability(f.reader, "Task", f.seen.ID, "", "both", 1)
	_, checks["packet"] = BuildContextPacket(f.reader, f.db, f.rels, "https://example.com", "test", "task", f.seen.Slug, 1)
	_, checks["goal context"] = QueryGoalContext(f.reader, f.db, f.rels, "G1")
	_, checks["provenance"] = f.app.ItemProvenance(f.reader, "Task", f.seen.ID, ProvenanceMembers, 0, 0)
	for name, err := range checks {
		if !errors.Is(err, ErrInternal) && !(name == "provenance" && errors.Is(err, ErrNotFound)) {
			t.Errorf("%s: %v; want ErrInternal", name, err)
		}
	}
}

func TestReadSurfacesGraph_PacketLeavesHiddenOut(t *testing.T) {
	f := newGraphFixture(t)
	pkt, err := BuildContextPacket(f.reader, f.db, f.rels, "https://example.com", "test", "task", f.seen.Slug, 2)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(pkt)
	if strings.Contains(string(raw), f.hidden.ID) || strings.Contains(string(raw), f.hidden.Slug) {
		t.Errorf("the packet names the hidden Task: %s", raw)
	}
	if !strings.Contains(string(raw), f.far.Slug) && !strings.Contains(string(raw), f.far.ID) {
		t.Errorf("the packet lost the readable far Task: %s", raw)
	}
	_, hiddenErr := BuildContextPacket(f.reader, f.db, f.rels, "https://example.com", "test", "task", f.hidden.Slug, 1)
	_, missingErr := BuildContextPacket(f.reader, f.db, f.rels, "https://example.com", "test", "task", "no-such-task", 1)
	if !errors.Is(hiddenErr, ErrNotFound) || hiddenErr.Error() != missingErr.Error() {
		t.Errorf("hidden anchor %v, missing %v; want the same not-found", hiddenErr, missingErr)
	}
	full, err := BuildContextPacket(f.global, f.db, f.rels, "https://example.com", "test", "task", f.seen.Slug, 1)
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := json.Marshal(full); !strings.Contains(string(raw), f.hidden.Slug) {
		t.Errorf("a global reader lost the hidden Task: %s", raw)
	}
}

func TestReadSurfacesGraph_GoalContextHiddenEqualsMissing(t *testing.T) {
	f := newGraphFixture(t)
	goals := findModule[*Goal](t, f.app)
	out, err := goals.MCPCreate(TrustedRead(authorCtx()).(Context), map[string]any{"Title": "hidden goal"})
	if err != nil {
		t.Fatal(err)
	}
	g := asItem[*Goal](out)
	_, hiddenErr := QueryGoalContext(f.reader, f.db, f.rels, g.Slug)
	_, missingErr := QueryGoalContext(f.reader, f.db, f.rels, "no-such-goal")
	if !errors.Is(hiddenErr, ErrNotFound) || hiddenErr.Error() != missingErr.Error() {
		t.Errorf("hidden goal %v, missing %v; want the same not-found", hiddenErr, missingErr)
	}
	if _, err := QueryGoalContext(f.global, f.db, f.rels, g.Slug); err != nil {
		t.Errorf("global reader: %v", err)
	}
}

func TestReadSurfacesGraph_ProvenanceOfHiddenIsEmpty(t *testing.T) {
	f := newGraphFixture(t)
	createProvenanceTableHelper(t, f.db)
	f.app.Provenance(NewProvenanceStore(f.db))
	if _, err := f.app.TransitionItemVia(TrustedRead(f.global), "mcp", "Task", f.hidden.Slug, "active", ""); err != nil {
		t.Fatal(err)
	}
	all, err := f.app.ItemProvenance(f.global, "Task", f.hidden.ID, ProvenanceMembers, 0, 0)
	if err != nil || all.Total == 0 {
		t.Fatalf("global history = %d %v; want entries", all.Total, err)
	}
	hidden, herr := f.app.ItemProvenance(f.reader, "Task", f.hidden.ID, ProvenanceMembers, 0, 0)
	missing, merr := f.app.ItemProvenance(f.reader, "Task", NewID(), ProvenanceMembers, 0, 0)
	if herr != nil || merr != nil || hidden.Total != 0 || len(hidden.Entries) != 0 || missing.Total != 0 {
		t.Errorf("hidden %+v %v, missing %+v %v; want the same empty history", hidden, herr, missing, merr)
	}
}

func TestReadSurfacesGraph_AuditFiltered(t *testing.T) {
	f := newGraphFixture(t)
	now := time.Now().UTC()
	records := func() []AuditRecord {
		return []AuditRecord{
			{ID: "1", Timestamp: now, ContentType: "Task", Slug: f.seen.Slug},
			{ID: "2", Timestamp: now, ContentType: "Task", Slug: f.hidden.Slug},
			{ID: "3", Timestamp: now, ContentType: "Task", Slug: "deleted-since"},
			{ID: "4", Timestamp: now, ContentType: "Unregistered", Slug: "x"},
			{ID: "5", Timestamp: now, ContentType: "Task", Slug: f.seen.Slug},
		}
	}
	got, err := f.app.filterAuditRecords(context.Background(), User{ID: "reader"}, records())
	if err != nil || len(got) != 2 || got[0].ID != "1" || got[1].ID != "5" {
		t.Errorf("reader entries = %+v %v; want 1 and 5", got, err)
	}
	if all, err := f.app.filterAuditRecords(context.Background(), User{ID: "global"}, records()); err != nil || len(all) != 5 {
		t.Errorf("global entries = %d %v; want 5", len(all), err)
	}

	// Through the handler: an Editor-role token that reads one Task sees one.
	store := NewAuditStore(f.db)
	if err := CreateAuditTable(f.db); err != nil {
		t.Fatal(err)
	}
	for _, r := range records()[:3] {
		if err := store.Append(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	h := newAuditHandler(authFuncFor(User{ID: "reader", Roles: []Role{Editor}}), store, f.app.filterAuditRecords)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/_audit", nil))
	var shown []AuditRecord
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &shown) != nil || len(shown) != 1 || shown[0].Slug != f.seen.Slug {
		t.Errorf("GET /_audit = %d %s; want the one readable entry", w.Code, w.Body)
	}
	// A scope load failure is a 500, never a shorter list.
	if _, err := f.db.Exec(`DROP TABLE smeldr_role_grants`); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/_audit", nil))
	if w.Code != http.StatusInternalServerError {
		t.Errorf("scope failure: %d; want 500", w.Code)
	}
}

func TestReadSurfacesGraph_AuditWithoutGovernanceUnchanged(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")})
	in := []AuditRecord{{ID: "1", ContentType: "Task", Slug: "a"}}
	if got, err := app.filterAuditRecords(context.Background(), User{}, in); err != nil || len(got) != 1 {
		t.Errorf("legacy = %v %v; want unchanged", got, err)
	}
}

func TestReadSurfacesGraph_HandlersGateUnderGovernance(t *testing.T) {
	f := newGraphFixture(t)
	f.app.ReachabilityHandler(f.rels)
	f.app.ContextPacketHandler(f.rels, "test")
	token := func(id string) string {
		tok, err := SignToken(User{ID: id, Roles: []Role{Author}}, "supersecretkey16", 0)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	get := func(path, id string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token(id))
		w := httptest.NewRecorder()
		f.app.mux.ServeHTTP(w, req)
		return w
	}
	// The reader reads Tasks: reachability answers, without the hidden one.
	w := get("/reachability/Task/"+f.seen.ID+"?direction=outgoing&depth=2", "reader")
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), f.hidden.ID) {
		t.Errorf("reader reachability = %d %s", w.Code, w.Body)
	}
	if w := get("/reachability/Goal/"+NewID(), "reader"); w.Code != http.StatusForbidden {
		t.Errorf("reachability on an unreadable type = %d; want 403", w.Code)
	}
	// The packet: an Author-role reader of the type gets it, without hidden.
	w = get("/packet/task/"+f.seen.Slug, "reader")
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), f.hidden.Slug) {
		t.Errorf("reader packet = %d %s", w.Code, w.Body)
	}
	if w := get("/packet/task/"+f.hidden.Slug, "reader"); w.Code != http.StatusNotFound {
		t.Errorf("packet of a hidden Task = %d; want 404", w.Code)
	}
	if w := get("/packet/goal/x", "reader"); w.Code != http.StatusForbidden {
		t.Errorf("packet on an unreadable type = %d; want 403", w.Code)
	}
	if w := get("/packet/task/"+f.hidden.Slug, "global"); w.Code != http.StatusOK {
		t.Errorf("global packet = %d %s", w.Code, w.Body)
	}
	// A scope load failure is a 500 on both.
	if _, err := f.db.Exec(`DROP TABLE smeldr_role_grants`); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/reachability/Task/" + f.seen.ID, "/packet/task/" + f.seen.Slug} {
		if w := get(p, "reader"); w.Code != http.StatusInternalServerError {
			t.Errorf("%s on a scope failure = %d; want 500", p, w.Code)
		}
	}
}

func TestReadSurfacesGraph_GoalContextLeavesHiddenLinksOut(t *testing.T) {
	f := newGraphFixture(t)
	goals := findModule[*Goal](t, f.app)
	out, err := goals.MCPCreate(TrustedRead(authorCtx()).(Context), map[string]any{"Title": "shared goal"})
	if err != nil {
		t.Fatal(err)
	}
	g := asItem[*Goal](out)
	if _, err := f.rs.Grant(context.Background(), RoleGrant{TokenID: "reader", RoleName: "scoped-reader", ScopeStatic: []string{"Goal:" + g.ID}}); err != nil {
		t.Fatal(err)
	}
	assertTaskEdge(t, f.rels, "derives_from", "Task", f.seen.ID, "Goal", g.ID)
	assertTaskEdge(t, f.rels, "derives_from", "Task", f.hidden.ID, "Goal", g.ID)
	gc, err := QueryGoalContext(f.reader, f.db, f.rels, g.Slug)
	if err != nil || len(gc.LinkedTasks) != 1 || gc.LinkedTasks[0].ID != f.seen.ID {
		t.Errorf("reader goal context = %+v %v; want only the readable Task", gc, err)
	}
	all, err := QueryGoalContext(f.global, f.db, f.rels, g.Slug)
	if err != nil || len(all.LinkedTasks) != 2 {
		t.Errorf("global goal context = %+v %v; want both Tasks", all, err)
	}
}

func TestReadSurfacesGraph_MissingRelationTable(t *testing.T) {
	f := newGraphFixture(t)
	if _, err := f.db.Exec(`DROP TABLE smeldr_relations`); err != nil {
		t.Fatal(err)
	}
	if p, err := f.rels.ListRelations(f.reader, RelationListOptions{}); err != nil || p.Total != 0 {
		t.Errorf("reader list on a missing table = %+v %v; want an empty page", p, err)
	}
	if _, err := f.rels.MCPPreviewImpact(f.reader, "Task", f.seen.ID); err == nil {
		t.Error("MCPPreviewImpact on a missing table: no error")
	}
	if _, err := f.rels.MCPGetRelations(f.reader, "Task", f.seen.ID, "both", ""); err == nil {
		t.Error("MCPGetRelations on a missing table: no error")
	}
}

// uncomparableDB cannot be a map key: its package functions get no governance
// of their own and need an attached scope.
type uncomparableDB struct {
	DB
	_ []int
}

func TestGovernanceRegistry_UncomparableAndNil(t *testing.T) {
	db := uncomparableDB{DB: newSQLiteDB(t)}
	registerGovernance(db, NewRoleStore(db))
	if rs, known := governanceFor(db); rs != nil || known {
		t.Error("an uncomparable handle is registered")
	}
	// Governance is wired in the process, so an unrecorded handle fails closed.
	if _, err := packageScope(context.Background(), db); !errors.Is(err, ErrInternal) {
		t.Errorf("uncomparable handle: %v; want ErrInternal", err)
	}
	registerGovernance(nil, nil)
	if _, known := governanceFor(nil); known {
		t.Error("a nil handle is registered")
	}
}

// Under governance in the process, a package function on a handle that no App
// registered (a wrapper, a transaction, an adapter) with no attached scope
// fails closed; it never reads everything.
func TestPackageScope_UnregisteredHandleFailsClosed(t *testing.T) {
	f := newGraphFixture(t)
	wrapped := &stmtDB{DB: f.db}
	rels, err := NewRelationStore(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string]error{}
	_, checks["relations"] = rels.MCPGetRelations(f.reader, "Task", f.seen.ID, "both", "")
	_, checks["impact"] = rels.MCPPreviewImpact(f.reader, "Task", f.seen.ID)
	_, checks["list"] = rels.ListRelations(f.reader, RelationListOptions{})
	_, checks["reachability"] = rels.Reachability(f.reader, "Task", f.seen.ID, "", "both", 1)
	_, checks["packet"] = BuildContextPacket(f.reader, wrapped, rels, "https://example.com", "test", "task", f.seen.Slug, 1)
	_, checks["goal context"] = QueryGoalContext(f.reader, wrapped, rels, "G1")
	for name, err := range checks {
		if !errors.Is(err, ErrInternal) || !strings.Contains(err.Error(), "no read scope attached") {
			t.Errorf("%s on an unregistered handle: %v; want ErrInternal naming the missing scope", name, err)
		}
	}
	// An attached scope, or TrustedRead, is the way through.
	scope, err := f.app.ReadScope(f.reader)
	if err != nil {
		t.Fatal(err)
	}
	if e, err := rels.MCPGetRelations(WithReadScope(f.reader, scope), "Task", f.seen.ID, "both", ""); err != nil || len(e) != 1 {
		t.Errorf("attached scope = %d %v; want the one readable edge", len(e), err)
	}
	if e, err := rels.MCPGetRelations(TrustedRead(f.reader), "Task", f.seen.ID, "both", ""); err != nil || len(e) != 2 {
		t.Errorf("TrustedRead = %d %v; want 2", len(e), err)
	}
}

// A handle an App uses without governance reads everything, even while
// governance is wired elsewhere in the process; a governed handle stays
// governed when another App without governance opens it; and the relation
// store's own handle follows the App's governance in either wiring order.
func TestPackageScope_RegisteredHandles(t *testing.T) {
	f := newGraphFixture(t)
	ctx := context.Background()

	legacyDB := newSQLiteDB(t)
	_ = New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: legacyDB})
	if s, err := packageScope(ctx, legacyDB); err != nil || !s.All() {
		t.Errorf("legacy handle = %v %v; want everything", s, err)
	}

	_ = New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: f.db})
	if rs, _ := governanceFor(f.db); rs != f.rs {
		t.Error("a second App without governance undid the handle's governance")
	}

	for _, governFirst := range []bool{false, true} {
		app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: f.db})
		wrapped := &stmtDB{DB: f.db}
		store, err := NewRelationStore(wrapped)
		if err != nil {
			t.Fatal(err)
		}
		if governFirst {
			if err := app.Governance(f.rs); err != nil {
				t.Fatal(err)
			}
			app.Relations(store)
		} else {
			app.Relations(store)
			if err := app.Governance(f.rs); err != nil {
				t.Fatal(err)
			}
		}
		if rs, _ := governanceFor(wrapped); rs != f.rs {
			t.Errorf("governance first %v: the store's handle is not governed", governFirst)
		}
	}
}

func TestAuditSubjectReadable_DynamicAndFailure(t *testing.T) {
	ctx := context.Background()
	db := addressDB(t)
	setupTokensTable(t, db)
	if err := CreateBlockTables(db); err != nil {
		t.Fatal(err)
	}
	if err := CreateSchemaTable(db); err != nil {
		t.Fatal(err)
	}
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
	repo, _ := app.DynamicContentRepo("invoice")
	seen, err := repo.CreateDraft(TrustedRead(ctx), map[string]any{"Title": "seen"})
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := repo.CreateDraft(TrustedRead(ctx), map[string]any{"Title": "hidden"})
	if err != nil {
		t.Fatal(err)
	}
	_ = RegisterScopedReaderRole(ctx, rs)
	if _, err := rs.Grant(ctx, RoleGrant{TokenID: "reader", RoleName: "scoped-reader", ScopeStatic: []string{"invoice:" + seen.ID}}); err != nil {
		t.Fatal(err)
	}
	scope, err := LoadReadScope(ctx, rs, User{ID: "reader"})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := app.auditSubjectReadable(ctx, scope, "invoice", seen.Slug); err != nil || !ok {
		t.Errorf("readable invoice = %v %v", ok, err)
	}
	if ok, err := app.auditSubjectReadable(ctx, scope, "invoice", hidden.Slug); err != nil || ok {
		t.Errorf("hidden invoice = %v %v; want false", ok, err)
	}
	// A lookup failure is an error, never a hidden entry.
	if _, err := db.Exec(`DROP TABLE smeldr_tasks`); err != nil {
		t.Fatal(err)
	}
	if _, err := app.auditSubjectReadable(ctx, scope, "Task", "x"); !errors.Is(err, ErrInternal) {
		t.Errorf("Task lookup on a dropped table: %v; want ErrInternal", err)
	}
	if _, err := app.filterAuditRecords(ctx, User{ID: "reader"}, []AuditRecord{{ContentType: "Task", Slug: "x"}}); !errors.Is(err, ErrInternal) {
		t.Errorf("filterAuditRecords on a lookup failure: %v; want ErrInternal", err)
	}
}

func TestReadSurfacesGraph_ScopedErrorPaths(t *testing.T) {
	f := newGraphFixture(t)
	scope, err := f.app.ReadScope(f.reader)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithReadScope(f.reader, scope)
	store := func(db DB) *RelationStore {
		t.Helper()
		s, err := NewRelationStore(db)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	// The in-memory paging path: a query failure and a scan failure are
	// ErrInternal, never a shorter page.
	failing := store(&govQueryFailDB{DB: f.db, failOn: "FROM smeldr_relations WHERE created_at"})
	if _, err := failing.ListRelations(ctx, RelationListOptions{}); !errors.Is(err, ErrInternal) {
		t.Errorf("scoped list on a query failure: %v; want ErrInternal", err)
	}
	nulls := store(&govQueryNullRowsDB{DB: f.db, nullOn: "FROM smeldr_relations WHERE created_at"})
	if _, err := nulls.ListRelations(ctx, RelationListOptions{}); !errors.Is(err, ErrInternal) {
		t.Errorf("scoped list on a scan failure: %v; want ErrInternal", err)
	}
	// A failing neighbour read stops the walk and the goal context.
	edges := store(&govQueryFailDB{DB: f.db, failOn: "WHERE source_type"})
	if _, err := edges.Reachability(ctx, "Task", f.seen.ID, "", "outgoing", 1); err == nil {
		t.Error("reachability on a failing edge read: no error")
	}
	goals := findModule[*Goal](t, f.app)
	out, err := goals.MCPCreate(TrustedRead(authorCtx()).(Context), map[string]any{"Title": "goal"})
	if err != nil {
		t.Fatal(err)
	}
	g := asItem[*Goal](out)
	if _, err := QueryGoalContext(TrustedRead(f.reader), f.db, edges, g.Slug); err == nil {
		t.Error("goal context on a failing source read: no error")
	}
	targets := store(&govQueryFailDB{DB: f.db, failOn: "WHERE target_type"})
	if _, err := targets.Reachability(ctx, "Task", f.seen.ID, "", "incoming", 1); err == nil {
		t.Error("reachability on a failing incoming edge read: no error")
	}
	if _, err := targets.MCPPreviewImpact(ctx, "Task", f.seen.ID); err == nil {
		t.Error("impact on a failing target read: no error")
	}
	if _, err := targets.MCPGetRelations(ctx, "Task", f.seen.ID, "target", ""); err == nil {
		t.Error("relations on a failing target read: no error")
	}
	if _, err := QueryGoalContext(TrustedRead(f.reader), f.db, targets, g.Slug); err == nil {
		t.Error("goal context on a failing target read: no error")
	}
	// The reachability route answers a walk error (here an out-of-range depth).
	f.app.ReachabilityHandler(f.rels)
	tok, err := SignToken(User{ID: "reader", Roles: []Role{Author}}, "supersecretkey16", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"?depth=0", "?depth=x"} {
		req := httptest.NewRequest(http.MethodGet, "/reachability/Task/"+f.seen.ID+q, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		f.app.mux.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("reachability %s = %d; want 400", q, w.Code)
		}
	}
}
