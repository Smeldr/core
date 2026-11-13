package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// T1183: the narrow search's query building and its edge paths.

// TestNarrowWhere_Cursors: each phase and cursor position builds the
// condition that continues after the cursor, in both database forms.
func TestNarrowWhere_Cursors(t *testing.T) {
	base := func(pg bool, after narrowKey) *narrowPlan {
		return &narrowPlan{upper: "D1", letters: "D", digits: "1", byAddr: true, ref: "d1", pg: pg, after: after,
			counter: map[string]addressCounter{"Decision": {"D", 120}, "Domain": {"DOM", 5}, "Task": {"T", 9}}}
	}
	for _, tc := range []struct {
		name     string
		n        *narrowPlan
		phase    byte
		typ      string
		dynamic  bool
		ok       bool
		contains []string
	}{
		{"address, no cursor", base(false, narrowKey{}), 'a', "Decision", false, true, []string{"address_no BETWEEN", "ORDER BY address_no, id", "LIMIT 201"}},
		{"address on a dynamic type", base(false, narrowKey{}), 'a', "Decision", true, true, []string{"type_name = $1"}},
		{"address, cursor in this type", base(false, narrowKey{Phase: 'a', Type: "Decision", No: 10, ID: "x"}), 'a', "Decision", false, true, []string{"address_no > $", "id > $"}},
		{"address, cursor past this type", base(false, narrowKey{Phase: 'a', Type: "Task", No: 1, ID: "x"}), 'a', "Decision", false, false, nil},
		{"address, cursor in the reference phase", base(false, narrowKey{Phase: 'r', Ref: "a", Type: "Task", ID: "x"}), 'a', "Decision", false, false, nil},
		{"address, prefix not the letters", base(false, narrowKey{}), 'a', "Domain", false, false, nil},
		{"not an addressed type", base(false, narrowKey{}), 'a', "Nope", false, false, nil},
		{"reference on SQLite", base(false, narrowKey{}), 'r', "Task", false, true, []string{"lower(external_ref) >= $1", "lower(external_ref) < $2", "ORDER BY lower(external_ref), id"}},
		{"reference on PostgreSQL", base(true, narrowKey{}), 'r', "Task", false, true, []string{`lower(external_ref COLLATE "C") >= $1`, `ORDER BY lower(external_ref COLLATE "C"), (id COLLATE "C")`}},
		{"reference, cursor before this type", base(false, narrowKey{Phase: 'r', Ref: "d1x", Type: "Task", ID: "x"}), 'r', "Decision", false, true, []string{"lower(external_ref) > $"}},
		{"reference, cursor in this type", base(false, narrowKey{Phase: 'r', Ref: "d1x", Type: "Task", ID: "x"}), 'r', "Task", false, true, []string{"(lower(external_ref), id) > ($3, $4)"}},
		{"reference, cursor after this type", base(false, narrowKey{Phase: 'r', Ref: "d1x", Type: "Decision", ID: "x"}), 'r', "Task", false, true, []string{"lower(external_ref) >= $3"}},
	} {
		clause, args, ok := tc.n.narrowWhere(tc.phase, tc.typ, tc.dynamic, nil, []Status{"proposed"})
		if ok != tc.ok {
			t.Errorf("%s: ok = %v", tc.name, ok)
			continue
		}
		for _, want := range tc.contains {
			if !strings.Contains(clause, want) {
				t.Errorf("%s: %q lacks %q", tc.name, clause, want)
			}
		}
		if ok && (!strings.Contains(clause, "status IN") || len(args) == 0) {
			t.Errorf("%s: no state filter: %q %v", tc.name, clause, args)
		}
	}
}

// TestNarrowKey_Order: the reference phase orders by reference first.
func TestNarrowKey_Order(t *testing.T) {
	a := narrowKey{Phase: 'r', Ref: "a", Type: "Z", ID: "9"}
	b := narrowKey{Phase: 'r', Ref: "b", Type: "A", ID: "1"}
	if !a.less(b) || b.less(a) {
		t.Error("reference order")
	}
	if !(narrowKey{Phase: 'r', Ref: "a", Type: "A", ID: "1"}).less(a) {
		t.Error("type within one reference")
	}
	k, err := decodeNarrowKey(b.encode())
	if err != nil || k != b {
		t.Errorf("round trip = %+v %v", k, err)
	}
}

// TestPlanNarrow_NoRegistry: without a database or a counter registry the
// narrow search has no addressed type and finds nothing.
func TestPlanNarrow_NoRegistry(t *testing.T) {
	ctx := context.Background()
	n, err := (&App{}).planNarrow(ctx, "D1", "")
	if err != nil || len(n.counter) != 0 || !n.byAddr {
		t.Errorf("no database: %+v %v", n, err)
	}
	db := newSQLiteDB(t)
	app := &App{cfg: Config{DB: db}}
	if n, err := app.planNarrow(ctx, "x", ""); err != nil || len(n.counter) != 0 || n.pg {
		t.Errorf("no registry: %+v %v", n, err)
	}
	if _, err := app.planNarrow(ctx, "x", "bad"); !errors.Is(err, ErrBadRequest) {
		t.Errorf("bad cursor: %v", err)
	}
	mustExecDB(t, db, `CREATE TABLE smeldr_address_counters (type_name TEXT, prefix TEXT, last_no BIGINT)`)
	failing := &App{cfg: Config{DB: &sweepRunQueryFailDB{DB: db}}}
	if _, err := failing.planNarrow(ctx, "x", ""); !errors.Is(err, ErrInternal) {
		t.Errorf("failed registry read: %v", err)
	}
	if prefixUpperBound("é") != "ê" {
		t.Error("upper bound of a multi-byte rune")
	}
}

// TestDBIsPostgres_ProbeFailureNotCached: a probe that fails for another
// reason than a missing function (here a cancelled context) is an error and
// is not remembered, so the next call probes again and settles it (T1183
// review, change 2).
func TestDBIsPostgres_ProbeFailureNotCached(t *testing.T) {
	db := newSQLiteDB(t)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if pg, err := dbIsPostgres(cancelled, db); err == nil || pg {
		t.Fatalf("cancelled probe = %v, %v, want an error", pg, err)
	}
	if _, cached := postgresCache.Load(db); cached {
		t.Fatal("a failed probe was cached")
	}
	if pg, err := dbIsPostgres(context.Background(), db); err != nil || pg {
		t.Fatalf("probe = %v, %v, want SQLite", pg, err)
	}
	if v, cached := postgresCache.Load(db); !cached || v.(bool) {
		t.Fatalf("settled probe cache = %v, %v", v, cached)
	}
	app := &App{cfg: Config{DB: newSQLiteDB(t)}}
	if _, err := app.planNarrow(cancelled, "D1", ""); !errors.Is(err, ErrInternal) {
		t.Errorf("planNarrow on a failed probe: %v", err)
	}
	if err := ensureNarrowSearchIndexes(cancelled, newSQLiteDB(t), "t", false); err == nil {
		t.Error("index on a failed probe: no error")
	}
}

// TestCallerIdentity: a plain context has no caller.
func TestCallerIdentity(t *testing.T) {
	if got := callerIdentity(context.Background()); got != "" {
		t.Errorf("callerIdentity = %q", got)
	}
	if got := callerIdentity(NewTestContext(User{ID: "i-1"})); got != "i-1" {
		t.Errorf("callerIdentity = %q", got)
	}
}

// premiseFailDB fails the queries that read the premise pairs or a premise.
type premiseFailDB struct {
	DB
	match string
}

func (d *premiseFailDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if strings.Contains(q, d.match) {
		return nil, errRepoError
	}
	return d.DB.QueryContext(ctx, q, args...)
}

func (d *premiseFailDB) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	if strings.Contains(q, d.match) {
		return d.DB.QueryRowContext(ctx, "SELECT no_such_column FROM smeldr_premise_flags")
	}
	return d.DB.QueryRowContext(ctx, q, args...)
}

// TestPremiseFlags_Failures: a failed read of the pairs, of the open
// condition or of the premise is ErrInternal; no pair table is no pairs; a
// premise that no longer exists is not named.
func TestPremiseFlags_Failures(t *testing.T) {
	f := newPremiseFixture(t)
	f.placedDecision(t, "d", "ratified")
	f.placedDecision(t, "p", "proposed")
	f.rest(t, "d", "grounded_in", "p")
	admin := as("human-1", Human, Admin)
	db := f.app.cfg.DB
	for _, match := range []string{"FROM smeldr_premise_flags WHERE decision_id", "FROM smeldr_signals", "FROM smeldr_decisions WHERE id", "FROM smeldr_relations"} {
		f.app.cfg.DB = &premiseFailDB{DB: db, match: match}
		_, err := f.app.PremiseFlags(admin, "d")
		f.app.cfg.DB = db
		if match == "FROM smeldr_decisions WHERE id" {
			// The resting Decision's own lookup does not use this query; the
			// premise's does.
			if err != nil && !errors.Is(err, ErrInternal) {
				t.Errorf("%s: %v", match, err)
			}
			continue
		}
		if !errors.Is(err, ErrInternal) {
			t.Errorf("%s: %v, want ErrInternal", match, err)
		}
	}
	// The premise is gone: the pair is listed without it.
	mustExecDB(t, f.db, `DELETE FROM smeldr_decisions WHERE id = 'p'`)
	got, err := f.app.PremiseFlags(admin, "d")
	if err != nil || len(got) != 1 || got[0].Premise != nil {
		t.Errorf("premise gone: %+v %v", got, err)
	}
	mustExecDB(t, f.db, `DROP TABLE smeldr_premise_flags`)
	if got, err := f.app.PremiseFlags(admin, "d"); err != nil || len(got) != 0 {
		t.Errorf("no pair table: %+v %v", got, err)
	}
}

// TestLatestSweepRuns_ScanFails: a row that cannot be read is ErrInternal.
func TestLatestSweepRuns_ScanFails(t *testing.T) {
	ctx := context.Background()
	db := newSQLiteDB(t)
	mustExecDB(t, db, `CREATE TABLE smeldr_sweep_runs (id TEXT, detector TEXT, ran_at TEXT, interval TEXT, walked TEXT, flagged INTEGER, skipped INTEGER, err TEXT, actor_kind TEXT, actor_id TEXT)`)
	mustExecDB(t, db, `INSERT INTO smeldr_sweep_runs VALUES ('r', 'd', '2026-11-11T10:00:00Z', '', 'not a number', 0, 0, '', '', '')`)
	if _, err := LatestSweepRuns(ctx, db); !errors.Is(err, ErrInternal) {
		t.Errorf("bad row: %v", err)
	}
}

// TestRestCountsWith_Failures: no database, and a failed address read.
func TestRestCountsWith_Failures(t *testing.T) {
	ctx := allReader()
	if _, err := (&App{}).RestCountsWith(ctx, "RcDec", "a", RestCountOptions{}); !errors.Is(err, ErrInternal) {
		t.Errorf("no database: %v", err)
	}
	f := newRestFixture(t)
	f.item(t, "a", "ratified")
	f.item(t, "b", "proposed")
	f.edge(t, "a", "grounded_in", "b", false)
	mustExecDB(t, f.db, `ALTER TABLE smeldr_dynamic_content ADD COLUMN address TEXT NOT NULL DEFAULT ''`)
	db := f.app.cfg.DB
	for _, dir := range []string{"out", "in"} {
		f.app.cfg.DB = &premiseFailDB{DB: db, match: "SELECT id, address FROM"}
		anchor := "a"
		if dir == "in" {
			anchor = "b"
		}
		_, err := f.app.RestCountsWith(ctx, "RcDec", anchor, RestCountOptions{Addresses: true})
		f.app.cfg.DB = db
		if !errors.Is(err, ErrInternal) {
			t.Errorf("%s: failed address read: %v", dir, err)
		}
	}
}

// TestSearchNarrow_QueryFails: a failed candidate read is ErrInternal, for a
// compiled type and for a runtime-defined one.
func TestSearchNarrow_QueryFails(t *testing.T) {
	for _, match := range []string{"SELECT * FROM smeldr_decisions", "SELECT * FROM smeldr_dynamic_content"} {
		f := newSearchFixture(t)
		db := f.app.cfg.DB
		f.app.cfg.DB = &premiseFailDB{DB: db, match: match}
		swapRepoDB(f, &premiseFailDB{DB: db, match: match})
		_, err := f.app.Search(f.global, SearchQuery{Text: "d", Narrow: true})
		f.app.cfg.DB = db
		if !errors.Is(err, ErrInternal) {
			t.Errorf("%s: %v, want ErrInternal", match, err)
		}
	}
}

// swapRepoDB points the fixture's Decision repository at db.
func swapRepoDB(f searchFixture, db DB) {
	if sr, ok := f.decisions.repo.(*SQLRepo[*Decision]); ok {
		sr.db = db
	}
}

// TestNarrowSearch_EdgePaths: a failed index write, an unreadable counter
// row, and a digit prefix too long to be a number.
func TestNarrowSearch_EdgePaths(t *testing.T) {
	ctx := context.Background()
	db := newSQLiteDB(t)
	if err := ensureNarrowSearchIndexes(ctx, &sweepRunExecFailDB{DB: db}, "smeldr_signals", false); err == nil {
		t.Error("a failed index write was not reported")
	}
	mustExecDB(t, db, `CREATE TABLE smeldr_address_counters (type_name TEXT, prefix TEXT, last_no TEXT)`)
	mustExecDB(t, db, `INSERT INTO smeldr_address_counters VALUES ('Decision', 'D', 'many')`)
	if _, err := (&App{cfg: Config{DB: db}}).planNarrow(ctx, "D1", ""); !errors.Is(err, ErrInternal) {
		t.Errorf("unreadable counter: %v", err)
	}
	n := &narrowPlan{byAddr: true, letters: "D", digits: "99999999999999999999", counter: map[string]addressCounter{"Decision": {"D", 10}}}
	if got := n.addressRanges("Decision"); got != nil {
		t.Errorf("overflowing digits = %v", got)
	}
}

// pgProbeDB answers the PostgreSQL probe as PostgreSQL does, over SQLite.
type pgProbeDB struct{ DB }

func (d pgProbeDB) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	if strings.Contains(q, "current_setting('server_version_num')") {
		return d.DB.QueryRowContext(ctx, `SELECT '160015'`)
	}
	return d.DB.QueryRowContext(ctx, q, args...)
}

// TestDBIsPostgres_SettledTrue: a probe that succeeds is PostgreSQL, and the
// answer is cached for the handle.
func TestDBIsPostgres_SettledTrue(t *testing.T) {
	db := pgProbeDB{DB: newSQLiteDB(t)}
	if pg, err := dbIsPostgres(context.Background(), db); err != nil || !pg {
		t.Fatalf("probe = %v, %v, want PostgreSQL", pg, err)
	}
	if v, cached := postgresCache.Load(db); !cached || !v.(bool) {
		t.Fatalf("cache = %v, %v, want true", v, cached)
	}
}
