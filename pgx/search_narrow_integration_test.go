//go:build integration

package pgx

import (
	"context"
	"fmt"
	"strings"
	"testing"

	smeldr "smeldr.dev/core"
)

// T1183 on Postgres: the narrow search reads the address number by range and
// the reference by an index in byte order (the C collation), so a prefix
// matches under any database collation; the ceiling and its cursor work the
// same.
func TestPG_SearchNarrow(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateOrchestrationTables(db); err != nil {
		t.Fatal(err)
	}
	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte(pgTestSecret), DB: db})
	smeldr.RegisterOrchestrationTypes(app, db)
	_ = app.Handler()

	// The reference index is in byte order under the C collation, whatever
	// the database's own collation.
	var def string
	if err := db.QueryRowContext(ctx, `SELECT indexdef FROM pg_indexes WHERE indexname = 'idx_smeldr_signals_narrow_ref'`).Scan(&def); err != nil {
		t.Fatalf("reference index: %v", err)
	}
	if want := `(lower((external_ref COLLATE "C")), id COLLATE "C")`; !strings.Contains(def, want) {
		t.Errorf("reference index = %s, want %s", def, want)
	}

	var signals []*smeldr.Signal
	for range smeldr.SearchNarrowCountCeiling + 5 {
		s := &smeldr.Signal{Sender: "core", SignalType: "plan-ready"}
		if err := smeldr.InsertSignal(ctx, db, s); err != nil {
			t.Fatal(err)
		}
		signals = append(signals, s)
	}
	for ref, s := range map[string]*smeldr.Signal{"C26 imported": signals[3], "c2-other": signals[4], "C_3 literal": signals[5]} {
		if _, err := db.ExecContext(ctx, `UPDATE smeldr_signals SET external_ref = $1 WHERE id = $2`, ref, s.ID); err != nil {
			t.Fatal(err)
		}
	}
	all := smeldr.NewTestContext(smeldr.User{ID: "x", Roles: []smeldr.Role{smeldr.Admin}})
	search := func(q smeldr.SearchQuery) smeldr.SearchResult {
		t.Helper()
		q.Narrow = true
		res, err := app.Search(all, q)
		if err != nil {
			t.Fatalf("Search(%+v): %v", q, err)
		}
		return res
	}

	// A whole address (the last, so no longer number starts with it) and its start.
	last := signals[len(signals)-1]
	if res := search(smeldr.SearchQuery{Text: last.Address}); len(res.Hits) != 1 || res.Hits[0].MatchReason != smeldr.SearchMatchAddress {
		t.Errorf("%s: %+v", last.Address, res.Hits)
	}
	if res := search(smeldr.SearchQuery{Text: "S1", Limit: 500}); res.Total != 1+10+100 {
		t.Errorf("S1: total %d, want S1, S10-S19 and S100-S199", res.Total)
	}
	// A reference prefix, case-insensitively, with LIKE's wildcards literal.
	if res := search(smeldr.SearchQuery{Text: "c2"}); len(res.Hits) != 2 || res.Hits[0].MatchReason != smeldr.SearchMatchReference {
		t.Errorf("c2: %+v", res.Hits)
	}
	if res := search(smeldr.SearchQuery{Text: "c_"}); len(res.Hits) != 1 || res.Hits[0].ExternalRef != "C_3 literal" {
		t.Errorf("c_: %+v", res.Hits)
	}
	// One letter over more than the ceiling: at_least, then the rest.
	first := search(smeldr.SearchQuery{Text: "s", Limit: 500})
	if !first.AtLeast || first.Next == "" || first.Total != smeldr.SearchNarrowCountCeiling {
		t.Fatalf("s: total %d at_least %v", first.Total, first.AtLeast)
	}
	rest := search(smeldr.SearchQuery{Text: "s", Limit: 500, After: first.Next})
	if rest.AtLeast || rest.Next != "" || first.Total+rest.Total != len(signals) {
		t.Errorf("s, after: total %d, want %d", rest.Total, len(signals)-first.Total)
	}
}

// TestPG_SearchNarrowReferences: on a database whose own collation is not C,
// more reference matches than the ceiling over two types, differing in case
// and holding "_", "-", a space and an upper-case non-ASCII letter: following
// Next returns every match exactly once. The reference phase's query for a
// one-character prefix over a few thousand matches is an index scan with no
// sort, also with a cursor (T1183 review, change 1).
func TestPG_SearchNarrowReferences(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateOrchestrationTables(db); err != nil {
		t.Fatal(err)
	}
	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte(pgTestSecret), DB: db})
	smeldr.RegisterOrchestrationTypes(app, db)
	_ = app.Handler()
	var collation string
	if err := db.QueryRowContext(ctx, `SELECT datcollate FROM pg_database WHERE datname = current_database()`).Scan(&collation); err != nil {
		t.Fatal(err)
	}
	t.Logf("database collation: %s", collation)

	variants := []string{"X-%d", "x_%d", "x %d", "XÉ-%d", "xa%d", "Xa%d"}
	want := map[string]bool{}
	tasks := smeldr.NewSQLRepo[*smeldr.Task](db, smeldr.Table("smeldr_tasks"))
	for i := range 130 {
		ref := fmt.Sprintf(variants[i%len(variants)], i/len(variants))
		s := &smeldr.Signal{Sender: "core", SignalType: "plan-ready"}
		if err := smeldr.InsertSignal(ctx, db, s); err != nil {
			t.Fatal(err)
		}
		task := &smeldr.Task{Node: smeldr.Node{ID: smeldr.NewID(), Slug: fmt.Sprintf("ref-task-%d", i), Status: "backlog"}, Title: "ref", Band: "core"}
		if err := tasks.Save(ctx, task); err != nil {
			t.Fatal(err)
		}
		for table, id := range map[string]string{"smeldr_signals": s.ID, "smeldr_tasks": task.ID} {
			if _, err := db.ExecContext(ctx, `UPDATE `+table+` SET external_ref = $1 WHERE id = $2`, ref, id); err != nil {
				t.Fatal(err)
			}
		}
		want[s.ID], want[task.ID] = true, true
	}
	all := smeldr.NewTestContext(smeldr.User{ID: "x", Roles: []smeldr.Role{smeldr.Admin}})
	got := map[string]int{}
	after, calls := "", 0
	for {
		res, err := app.Search(all, smeldr.SearchQuery{Text: "x", Narrow: true, Limit: 500, After: after})
		if err != nil {
			t.Fatal(err)
		}
		calls++
		for _, h := range res.Hits {
			got[h.ID]++
		}
		if res.Next == "" {
			break
		}
		if calls > 5 {
			t.Fatal("next does not end")
		}
		after = res.Next
	}
	if calls < 2 {
		t.Errorf("%d calls: the matches did not pass the ceiling", calls)
	}
	for id := range want {
		if got[id] != 1 {
			t.Errorf("%s found %d times", id, got[id])
		}
	}
	if len(got) != len(want) {
		t.Errorf("found %d, want %d", len(got), len(want))
	}
	if res, err := app.Search(all, smeldr.SearchQuery{Text: "xÉ", Narrow: true, Limit: 500}); err != nil || res.Total != 2*22 {
		t.Errorf("xÉ: total %d, %v", res.Total, err)
	}

	// A few thousand more Signals whose reference starts with x, then the
	// phase's query plan, without and with a cursor.
	if _, err := db.ExecContext(ctx, `
		INSERT INTO smeldr_signals (id, slug, status, sender, signal_type, external_ref, created_at, updated_at)
		SELECT 'bulk-' || g, 'bulk-' || g, 'pending', 'core', 'plan-ready', 'x-bulk-' || g, now(), now()
		  FROM generate_series(1, 4000) g`); err != nil {
		t.Fatalf("bulk insert: %v", err)
	}
	if _, err := db.ExecContext(ctx, `ANALYZE smeldr_signals`); err != nil {
		t.Fatal(err)
	}
	const ref = `lower(external_ref COLLATE "C")`
	const id = `(id COLLATE "C")`
	for name, cond := range map[string]string{
		"first page": "",
		"cursor":     " AND (" + ref + ", " + id + ") > ('x-bulk-2000', 'bulk-2000')",
	} {
		rows, err := db.QueryContext(ctx, `EXPLAIN (ANALYZE, COSTS OFF, TIMING OFF, SUMMARY OFF) SELECT * FROM smeldr_signals WHERE external_ref <> '' AND `+ref+` >= 'x' AND `+ref+` < 'y'`+cond+
			` ORDER BY `+ref+`, `+id+` LIMIT 201`)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, line)
		}
		rows.Close()
		text := strings.Join(plan, "\n")
		t.Logf("%s plan:\n%s", name, text)
		if !strings.Contains(text, "Index Scan using idx_smeldr_signals_narrow_ref") || strings.Contains(text, "Sort") {
			t.Errorf("%s: not an index scan without a sort:\n%s", name, text)
		}
	}
}
