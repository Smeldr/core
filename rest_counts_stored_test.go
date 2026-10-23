package smeldr

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

// place puts an RcDec item (or an Area, typ "Area") in a place.
func (f restFixture) place(t *testing.T, typ, id, kind, target string) {
	t.Helper()
	if _, _, err := writeEdgeRow(context.Background(), f.db, RelationEdge{SourceType: typ, SourceID: id, TargetType: placeKindTargets[kind], TargetID: target, RelationKind: kind, EdgeClass: "asserted"}); err != nil {
		t.Fatal(err)
	}
}

// placeReader is a context whose scope reads the anchor item and holds read
// grants on the given places ("Domain:D1", "Area:A1").
func placeReader(anchor string, places ...string) context.Context {
	s := &ReadScope{items: map[string]map[string]bool{"RcDec": {anchor: true}}, places: map[string]bool{}}
	for _, p := range places {
		s.places[p] = true
	}
	return WithReadScope(context.Background(), s)
}

// newStoredRestFixture builds Domains D1 and D2, Areas A1 and A2 in D1, B1 in
// D2, and an anchor "a" in A1 that rests on:
//
//	a -> pa1 (A1) -> y (A1)        y in bucket A1
//	a -> b1item (B1) -> z (A1)     b1item in B1, z in the Organization
//	a -> ra2 (A2) -> w (A1)        ra2 in A2, w in D1
//	a -> u (unplaced)              u in the Organization
//	b1item -> v (A1), pa1 -> v     v first reached through B1, then deeper: A1
func newStoredRestFixture(t *testing.T) restFixture {
	t.Helper()
	f := newRestFixture(t)
	if err := EnsureRestCountTable(context.Background(), f.db); err != nil {
		t.Fatal(err)
	}
	f.place(t, "Area", "A1", "belongs_to_domain", "D1")
	f.place(t, "Area", "A2", "belongs_to_domain", "D1")
	f.place(t, "Area", "B1", "belongs_to_domain", "D2")
	for id, area := range map[string]string{"a": "A1", "pa1": "A1", "y": "A1", "b1item": "B1", "z": "A1", "ra2": "A2", "w": "A1", "v": "A1", "u": ""} {
		status := "ratified"
		if id == "y" || id == "z" {
			status = "proposed"
		}
		f.item(t, id, status)
		if area != "" {
			f.place(t, "RcDec", id, "belongs_to_area", area)
		}
	}
	for _, e := range [][2]string{{"a", "pa1"}, {"pa1", "y"}, {"a", "b1item"}, {"b1item", "z"}, {"a", "ra2"}, {"ra2", "w"}, {"a", "u"}, {"b1item", "v"}, {"pa1", "v"}} {
		f.edge(t, e[0], "grounded_in", e[1], false)
	}
	return f
}

func storedOf(t *testing.T, f restFixture, ctx context.Context, id string) StoredRestCounts {
	t.Helper()
	got, err := f.app.StoredRestCounts(ctx, []ItemRef{{"RcDec", id}})
	if err != nil || len(got) != 1 {
		t.Fatalf("StoredRestCounts(%s) = %+v %v", id, got, err)
	}
	return got[0]
}

// TestStoredRestCounts_HiddenPath: a reader gets only the items whose every
// path node they read; never more than their own live walk.
func TestStoredRestCounts_HiddenPath(t *testing.T) {
	f := newStoredRestFixture(t)
	run, err := f.app.SweepRestCounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want RestCount
	}{
		// All eight.
		{"reads everything", allReader(), RestCount{Open: 2, Closed: 6}},
		// Domain D1: pa1, y, v (A1), ra2 (A2), w (D1). Not b1item (D2), not z
		// (reached only through D2: the Organization), not u.
		{"domain-reader of D1", placeReader("a", "Domain:D1"), RestCount{Open: 1, Closed: 4}},
		// Area A1: pa1, y, v. Not w: reached only through ra2 in A2.
		{"area-reader of A1", placeReader("a", "Area:A1"), RestCount{Open: 1, Closed: 2}},
		{"area-reader of B1", placeReader("a", "Area:B1"), RestCount{Closed: 1}},
		// Item grants only: no bucket.
		{"item grant only", placeReader("a"), RestCount{}},
	} {
		got := storedOf(t, f, tc.ctx, "a")
		if got.ThisRestsOn != tc.want || !got.CountedAt.Equal(run.EndedAt) {
			t.Errorf("%s: %+v at %v, want %+v at %v", tc.name, got.ThisRestsOn, got.CountedAt, tc.want, run.EndedAt)
		}
	}
	// What rests on z: b1item (bucket B1) and a through b1item (A1 and B1
	// are in different Domains: the Organization).
	if got := storedOf(t, f, placeReader("z", "Area:B1"), "z"); got.RestsOnThis != (RestCount{Closed: 1}) {
		t.Errorf("z for a reader of B1: %+v", got.RestsOnThis)
	}
	if got := storedOf(t, f, placeReader("z", "Domain:D1", "Domain:D2"), "z"); got.RestsOnThis != (RestCount{Closed: 1}) {
		t.Errorf("z for a reader of D1 and D2: %+v (a is reached only through D2's b1item)", got.RestsOnThis)
	}
	if run.Items != 9 || run.Capped != 0 || run.Buckets == 0 || run.EndedAt.Before(run.StartedAt) {
		t.Errorf("run = %+v", run)
	}
}

// TestStoredRestCounts_Deterministic: two sweeps of an unchanged graph store
// the same rows (condition 1), and an item placed in two Areas of one Domain
// counts in that Domain.
func TestStoredRestCounts_Deterministic(t *testing.T) {
	f := newStoredRestFixture(t)
	f.item(t, "m", "ratified")
	f.place(t, "RcDec", "m", "belongs_to_area", "A1")
	f.place(t, "RcDec", "m", "belongs_to_area", "A2")
	f.edge(t, "a", "addresses", "m", false)
	rowsOf := func() []string {
		if _, err := f.app.SweepRestCounts(context.Background()); err != nil {
			t.Fatal(err)
		}
		rows, err := f.db.Query(`SELECT item_id, direction, bucket_type, bucket_id, bucket_domain, open_count, closed_count, capped FROM smeldr_rest_counts ORDER BY 1, 2, 3, 4`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var id, dir, bt, bid, bd string
			var o, c int
			var capped bool
			if err := rows.Scan(&id, &dir, &bt, &bid, &bd, &o, &c, &capped); err != nil {
				t.Fatal(err)
			}
			out = append(out, fmt.Sprint(id, dir, bt, bid, bd, o, c, capped))
		}
		return out
	}
	first, second := rowsOf(), rowsOf()
	if !reflect.DeepEqual(first, second) {
		t.Errorf("two sweeps differ:\n%v\n%v", first, second)
	}
	if got := storedOf(t, f, placeReader("a", "Area:A1"), "a"); got.ThisRestsOn != (RestCount{Open: 1, Closed: 2}) {
		t.Errorf("m (A1 and A2) counted for a reader of A1 only: %+v", got.ThisRestsOn)
	}
	if got := storedOf(t, f, placeReader("a", "Domain:D1"), "a"); got.ThisRestsOn != (RestCount{Open: 1, Closed: 5}) {
		t.Errorf("m not counted for a reader of D1: %+v", got.ThisRestsOn)
	}
}

// TestStoredRestCounts covers the read: limits, unreadable items, never swept.
func TestStoredRestCounts(t *testing.T) {
	f := newStoredRestFixture(t)
	// Never swept: zero counts, no CountedAt.
	got := storedOf(t, f, allReader(), "a")
	if got.ThisRestsOn != (RestCount{}) || !got.CountedAt.IsZero() {
		t.Errorf("never swept = %+v", got)
	}
	if _, err := f.app.SweepRestCounts(context.Background()); err != nil {
		t.Fatal(err)
	}
	many := make([]ItemRef, MaxStoredRestCountItems+1)
	if _, err := f.app.StoredRestCounts(allReader(), many); !errors.Is(err, ErrBadRequest) {
		t.Errorf("too many: %v", err)
	}
	list, err := f.app.StoredRestCounts(placeReader("a", "Domain:D1"), []ItemRef{{"RcDec", "y"}, {"RcDec", "a"}, {"RcDec", "a"}, {"Signal", "s"}, {"", "x"}})
	if err != nil || len(list) != 1 || list[0].ID != "a" {
		t.Errorf("readable only, once each = %+v %v", list, err)
	}
	if list, err := f.app.StoredRestCounts(placeReader("a"), []ItemRef{{"RcDec", "y"}}); err != nil || len(list) != 0 {
		t.Errorf("nothing readable = %+v %v", list, err)
	}
	if got := storedOf(t, f, allReader(), "y"); got.RestsOnThis != (RestCount{Closed: 2}) {
		t.Errorf("y rests_on_this = %+v (pa1, a)", got.RestsOnThis)
	}
	// Without the tables: never swept.
	bare := newRestFixture(t)
	bare.item(t, "a", "ratified")
	if got := storedOf(t, bare, allReader(), "a"); !got.CountedAt.IsZero() {
		t.Errorf("no tables = %+v", got)
	}
	if _, err := (&App{}).StoredRestCounts(allReader(), nil); !errors.Is(err, ErrInternal) {
		t.Errorf("no database: %v", err)
	}
	for _, failOn := range []string{"FROM smeldr_rest_count_runs", "FROM smeldr_rest_counts"} {
		app := &App{cfg: Config{DB: &govQueryFailDB{DB: f.db, failOn: failOn}}}
		if _, err := app.StoredRestCounts(allReader(), []ItemRef{{"RcDec", "a"}}); !errors.Is(err, ErrInternal) {
			t.Errorf("%s fails: %v", failOn, err)
		}
	}
}

// TestSweepRestCounts_Cap: a walk past RestCountLimit is capped and counted
// once in the run.
func TestSweepRestCounts_Cap(t *testing.T) {
	f := newRestFixture(t)
	if err := EnsureRestCountTable(context.Background(), f.db); err != nil {
		t.Fatal(err)
	}
	f.item(t, "hub", "ratified")
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i := range RestCountLimit + 3 {
		id := fmt.Sprintf("n%04d", i)
		if _, err := tx.Exec(`INSERT INTO smeldr_dynamic_content (id, type_name, status) VALUES ($1, 'RcDec', 'ratified')`, id); err != nil {
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
	run, err := f.app.SweepRestCounts(context.Background())
	if err != nil || run.Capped != 1 || run.Items != RestCountLimit+4 {
		t.Fatalf("run = %+v %v", run, err)
	}
	if got := storedOf(t, f, allReader(), "hub"); got.RestsOnThis != (RestCount{Closed: RestCountLimit, AtLeast: true}) {
		t.Errorf("hub = %+v", got.RestsOnThis)
	}
}

// TestSweepRestCounts_Failures: a failed read or store is ErrInternal and
// keeps the previous counts.
func TestSweepRestCounts_Failures(t *testing.T) {
	f := newStoredRestFixture(t)
	if _, err := f.app.SweepRestCounts(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := storedOf(t, f, allReader(), "a")
	// A store that fails half way rolls back.
	f.item(t, "poison", "ratified")
	f.edge(t, "poison", "grounded_in", "a", false)
	mustExecDB(t, f.db, `CREATE TRIGGER rc_poison BEFORE INSERT ON smeldr_rest_counts WHEN NEW.item_id = 'poison' BEGIN SELECT RAISE(ABORT, 'poison'); END`)
	if _, err := f.app.SweepRestCounts(context.Background()); !errors.Is(err, ErrInternal) {
		t.Errorf("store fails: %v", err)
	}
	if after := storedOf(t, f, allReader(), "a"); !reflect.DeepEqual(after, before) {
		t.Errorf("previous counts lost: %+v, was %+v", after, before)
	}
	for _, failOn := range []string{"relation_kind IN ($2", "belongs_to_organization", "SELECT id, status FROM", "awaits_action = TRUE"} {
		app := &App{cfg: Config{DB: &govQueryFailDB{DB: f.db, failOn: failOn}}}
		if _, err := app.SweepRestCounts(context.Background()); !errors.Is(err, ErrInternal) {
			t.Errorf("%s fails: %v", failOn, err)
		}
	}
	if _, err := (&App{}).SweepRestCounts(context.Background()); !errors.Is(err, ErrInternal) {
		t.Errorf("no database: %v", err)
	}
	if err := EnsureRestCountTable(context.Background(), &execFailDB{DB: f.db, failOn: "smeldr_rest_counts"}); err == nil {
		t.Error("EnsureRestCountTable: a failed create must be returned")
	}
}

// TestReadScope_PlaceGrants: a read grant on a place records the place, so
// the stored counts know which buckets the reader covers.
func TestReadScope_PlaceGrants(t *testing.T) {
	s := &ReadScope{items: map[string]map[string]bool{}, places: map[string]bool{"Domain:D1": true, "Area:B1": true}}
	for _, tc := range []struct {
		typ, id, domain string
		want            bool
	}{
		{"Organization", "", "", false},
		{"Domain", "D1", "", true},
		{"Domain", "D2", "", false},
		{"Area", "A1", "D1", true},
		{"Area", "B1", "D2", true},
		{"Area", "C1", "D2", false},
		{"Area", "X", "", false},
		{"Other", "x", "", false},
	} {
		if got := s.coversRestBucket(tc.typ, tc.id, tc.domain); got != tc.want {
			t.Errorf("%s %s (%s) = %v", tc.typ, tc.id, tc.domain, got)
		}
	}
	if !AllReadScope().coversRestBucket("Organization", "", "") || (*ReadScope)(nil).coversRestBucket("Domain", "D1", "") {
		t.Error("all or nil scope")
	}
}

// TestSweepRestCounts_PlaceKinds: an item placed in a Domain counts in that
// Domain's bucket; one placed in the Organization in the Organization's.
func TestSweepRestCounts_PlaceKinds(t *testing.T) {
	f := newRestFixture(t)
	if err := EnsureRestCountTable(context.Background(), f.db); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "d", "o"} {
		f.item(t, id, "ratified")
	}
	f.place(t, "RcDec", "a", "belongs_to_domain", "D1")
	f.place(t, "RcDec", "d", "belongs_to_domain", "D1")
	f.place(t, "RcDec", "o", "belongs_to_organization", "ORG")
	f.edge(t, "a", "grounded_in", "d", false)
	f.edge(t, "a", "grounded_in", "o", false)
	if _, err := f.app.SweepRestCounts(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := storedOf(t, f, placeReader("a", "Domain:D1"), "a"); got.ThisRestsOn != (RestCount{Closed: 1}) {
		t.Errorf("domain-reader of D1 = %+v; want d only", got.ThisRestsOn)
	}
	if got := storedOf(t, f, allReader(), "a"); got.ThisRestsOn != (RestCount{Closed: 2}) {
		t.Errorf("reads everything = %+v", got.ThisRestsOn)
	}
	if (restPlace{domain: "D1"}).depth() != 1 || (restPlace{}).depth() != 0 || (restPlace{area: "A"}).depth() != 2 {
		t.Error("depth")
	}
}
