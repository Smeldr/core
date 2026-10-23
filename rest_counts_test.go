package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"
)

// restFixture is a small rest graph of RcDec items (dynamic content) with a
// flow whose "proposed" awaits action (open) and "ratified" does not (closed).
type restFixture struct {
	db  *sql.DB
	app *App
}

func newRestFixture(t *testing.T) restFixture {
	t.Helper()
	db := newMigratedDB(t)
	if err := CreateRelationTables(db); err != nil {
		t.Fatal(err)
	}
	mustExecDB(t, db, `CREATE TABLE smeldr_dynamic_content (id TEXT PRIMARY KEY, type_name TEXT NOT NULL, status TEXT NOT NULL)`)
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{Name: "rc", TypeName: "RcDec", States: []State{
		{Name: "proposed", IsInitial: true, AwaitsAction: true}, {Name: "ratified"}, {Name: "superseded", IsTerminal: true},
	}}); err != nil {
		t.Fatal(err)
	}
	return restFixture{db: db, app: app}
}

// item adds an RcDec item.
func (f restFixture) item(t *testing.T, id, status string) {
	t.Helper()
	if _, err := f.db.Exec(`INSERT INTO smeldr_dynamic_content (id, type_name, status) VALUES ($1, 'RcDec', $2)`, id, status); err != nil {
		t.Fatal(err)
	}
}

// edge asserts src -kind-> tgt between RcDec items; ended makes it a past edge.
func (f restFixture) edge(t *testing.T, src, kind, tgt string, ended bool) {
	t.Helper()
	e := RelationEdge{SourceType: "RcDec", SourceID: src, TargetType: "RcDec", TargetID: tgt, RelationKind: kind, EdgeClass: "asserted"}
	if ended {
		past := time.Now().UTC().Add(-time.Hour)
		e.InvalidAt = &past
	}
	if _, _, err := writeEdgeRow(context.Background(), f.db, e); err != nil {
		t.Fatal(err)
	}
}

// reader is a context whose scope reads only ids (of RcDec).
func reader(ids ...string) context.Context {
	s := &ReadScope{items: map[string]map[string]bool{"RcDec": {}}}
	for _, id := range ids {
		s.items["RcDec"][id] = true
	}
	return WithReadScope(context.Background(), s)
}

func allReader() context.Context { return WithReadScope(context.Background(), AllReadScope()) }

// TestRestCounts_Both counts both directions, open and closed, transitively,
// over every rest kind and only live edges.
func TestRestCounts_Both(t *testing.T) {
	f := newRestFixture(t)
	for id, st := range map[string]string{"a": "ratified", "b": "proposed", "c": "ratified", "d": "superseded", "e": "proposed", "x": "proposed", "y": "ratified"} {
		f.item(t, id, st)
	}
	// a rests on b (grounded_in), b on c (addresses), c on d (amends).
	f.edge(t, "a", "grounded_in", "b", false)
	f.edge(t, "b", "addresses", "c", false)
	f.edge(t, "c", "amends", "d", false)
	// e rests on a; x rested on a once (ended); y depends_on a (not a rest kind).
	f.edge(t, "e", "grounded_in", "a", false)
	f.edge(t, "x", "grounded_in", "a", true)
	f.edge(t, "y", "depends_on", "a", false)

	got, err := f.app.RestCounts(allReader(), "RcDec", "a")
	if err != nil {
		t.Fatal(err)
	}
	want := RestCounts{ThisRestsOn: RestCount{Open: 1, Closed: 2}, RestsOnThis: RestCount{Open: 1}}
	if got != want {
		t.Errorf("RestCounts(a) = %+v, want %+v", got, want)
	}
	// d rests on nothing; three items rest on it.
	if got, _ := f.app.RestCounts(allReader(), "RcDec", "d"); got != (RestCounts{RestsOnThis: RestCount{Open: 2, Closed: 2}}) {
		t.Errorf("RestCounts(d) = %+v", got)
	}
}

// TestRestCounts_Anchor: an unknown and an unreadable anchor answer alike.
func TestRestCounts_Anchor(t *testing.T) {
	f := newRestFixture(t)
	f.item(t, "a", "ratified")
	if _, err := f.app.RestCounts(allReader(), "RcDec", "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown anchor: %v", err)
	}
	if _, err := f.app.RestCounts(reader(), "RcDec", "a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unreadable anchor: %v", err)
	}
	if _, err := f.app.RestCounts(allReader(), "", "a"); !errors.Is(err, ErrBadRequest) {
		t.Errorf("no type: %v", err)
	}
	if _, err := (&App{}).RestCounts(allReader(), "RcDec", "a"); !errors.Is(err, ErrInternal) {
		t.Errorf("no database: %v", err)
	}
}

// TestRestCounts_Hidden: a hidden intermediate item is neither counted nor
// walked through; a hidden item at the end is not counted.
func TestRestCounts_Hidden(t *testing.T) {
	f := newRestFixture(t)
	for _, id := range []string{"a", "h", "far", "end"} {
		f.item(t, id, "ratified")
	}
	f.edge(t, "a", "grounded_in", "h", false)
	f.edge(t, "h", "grounded_in", "far", false)
	f.edge(t, "a", "grounded_in", "end", false)
	got, err := f.app.RestCounts(reader("a", "far"), "RcDec", "a")
	if err != nil || got != (RestCounts{}) {
		t.Errorf("reader of a and far = %+v %v; want nothing (far only through hidden h, end hidden)", got, err)
	}
	if got, _ := f.app.RestCounts(allReader(), "RcDec", "a"); got.ThisRestsOn.Closed != 3 {
		t.Errorf("reader of all = %+v", got)
	}
	// An edge to an item with no row is not an item.
	f.edge(t, "a", "grounded_in", "ghost", false)
	if got, _ := f.app.RestCounts(allReader(), "RcDec", "a"); got.ThisRestsOn.Closed != 3 {
		t.Errorf("dangling edge counted: %+v", got)
	}
}

// TestRestCounts_Once: cycles end and an item reached by two paths counts once.
func TestRestCounts_Once(t *testing.T) {
	f := newRestFixture(t)
	for _, id := range []string{"a", "b", "c", "d"} {
		f.item(t, id, "ratified")
	}
	f.edge(t, "a", "grounded_in", "b", false)
	f.edge(t, "a", "grounded_in", "c", false)
	f.edge(t, "b", "grounded_in", "d", false)
	f.edge(t, "c", "grounded_in", "d", false)
	f.edge(t, "d", "grounded_in", "a", false) // a cycle back to the anchor
	got, err := f.app.RestCounts(allReader(), "RcDec", "a")
	if err != nil || got.ThisRestsOn != (RestCount{Closed: 3}) || got.RestsOnThis != (RestCount{Closed: 3}) {
		t.Errorf("RestCounts(a) = %+v %v; want 3 each way, a itself never", got, err)
	}
}

// TestRestCounts_Cap: past RestCountLimit the walk stops at the limit and
// says at least.
func TestRestCounts_Cap(t *testing.T) {
	f := newRestFixture(t)
	f.item(t, "hub", "ratified")
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i := range RestCountLimit + 5 {
		id := fmt.Sprintf("n%04d", i)
		if _, err := tx.Exec(`INSERT INTO smeldr_dynamic_content (id, type_name, status) VALUES ($1, 'RcDec', 'proposed')`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO smeldr_relations (id, source_type, source_id, target_type, target_id, relation_kind, edge_class, created_at, updated_at)
			VALUES ($1, 'RcDec', $2, 'RcDec', 'hub', 'grounded_in', 'asserted', $3, $3)`, "r"+id, id, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	got, err := f.app.RestCounts(allReader(), "RcDec", "hub")
	if err != nil || got.RestsOnThis != (RestCount{Open: RestCountLimit, AtLeast: true}) {
		t.Errorf("capped = %+v %v", got.RestsOnThis, err)
	}
	// Exactly at the limit is not a cut.
	mustExecDB(t, f.db, `DELETE FROM smeldr_relations WHERE source_id IN ('n0000', 'n0001', 'n0002', 'n0003', 'n0004')`)
	if got, _ := f.app.RestCounts(allReader(), "RcDec", "hub"); got.RestsOnThis != (RestCount{Open: RestCountLimit}) {
		t.Errorf("at the limit = %+v", got.RestsOnThis)
	}
}

// TestRestCounts_Failures: a failed edge, state or flow read is ErrInternal.
func TestRestCounts_Failures(t *testing.T) {
	f := newRestFixture(t)
	f.item(t, "a", "ratified")
	f.item(t, "b", "proposed")
	f.edge(t, "a", "grounded_in", "b", false)
	for _, failOn := range []string{"FROM smeldr_relations", "SELECT id, status FROM", "awaits_action = TRUE"} {
		app := &App{cfg: Config{DB: &govQueryFailDB{DB: f.db, failOn: failOn}}}
		if _, err := app.RestCounts(allReader(), "RcDec", "a"); !errors.Is(err, ErrInternal) {
			t.Errorf("%s fails: %v", failOn, err)
		}
	}
	// A Signal end whose subject cannot be read (no Signal table here) fails
	// as the anchor and as an end of the walk.
	sig := &ReadScope{items: map[string]map[string]bool{"RcDec": {"a": true}, "Signal": {"s1": true}}}
	if _, err := f.app.RestCounts(WithReadScope(context.Background(), sig), "Signal", "s1"); !errors.Is(err, ErrInternal) {
		t.Errorf("Signal anchor: %v", err)
	}
	if _, _, err := writeEdgeRow(context.Background(), f.db, RelationEdge{SourceType: "RcDec", SourceID: "a", TargetType: "Signal", TargetID: "s1", RelationKind: "addresses", EdgeClass: "asserted"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.RestCounts(WithReadScope(context.Background(), sig), "RcDec", "a"); !errors.Is(err, ErrInternal) {
		t.Errorf("Signal end: %v", err)
	}
	// A state that cannot be scanned fails.
	mustExecDB(t, f.db, `CREATE TABLE smeldr_rc_nulls (id TEXT PRIMARY KEY, status TEXT)`)
	mustExecDB(t, f.db, `INSERT INTO smeldr_rc_nulls (id, status) VALUES ('n', NULL)`)
	if _, _, err := writeEdgeRow(context.Background(), f.db, RelationEdge{SourceType: "RcDec", SourceID: "b", TargetType: "RcNull", TargetID: "n", RelationKind: "addresses", EdgeClass: "asserted"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.RestCounts(allReader(), "RcDec", "b"); !errors.Is(err, ErrInternal) {
		t.Errorf("NULL state: %v", err)
	}
	// A type with no flow at all counts as closed.
	mustExecDB(t, f.db, `DELETE FROM smeldr_rc_nulls`)
	mustExecDB(t, f.db, `DELETE FROM smeldr_state_flows`)
	if got, err := f.app.RestCounts(allReader(), "RcDec", "a"); err != nil || got.ThisRestsOn != (RestCount{Closed: 1}) {
		t.Errorf("no flow = %+v %v", got, err)
	}
}

func TestSortNodes(t *testing.T) {
	nodes := []reachabilityNode{{"B", "1"}, {"A", "2"}, {"A", "1"}}
	sortNodes(nodes)
	if nodes[0] != (reachabilityNode{"A", "1"}) || nodes[1] != (reachabilityNode{"A", "2"}) || nodes[2] != (reachabilityNode{"B", "1"}) {
		t.Errorf("sortNodes = %v", nodes)
	}
}
