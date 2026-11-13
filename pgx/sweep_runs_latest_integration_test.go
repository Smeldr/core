//go:build integration

package pgx

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// latestSweepRunIDsQuery mirrors the first read in core's sweep_run.go, so
// its plan can be shown on PostgreSQL. A change to one is a change to the
// other. The second read is the rows by primary key.
const latestSweepRunIDsQuery = `WITH RECURSIVE d(detector) AS (
		     SELECT MIN(detector) FROM smeldr_sweep_runs
		     UNION ALL
		     SELECT (SELECT MIN(detector) FROM smeldr_sweep_runs WHERE detector > d.detector)
		       FROM d WHERE d.detector IS NOT NULL)
		 SELECT (SELECT x.id FROM smeldr_sweep_runs x WHERE x.detector = d.detector
		          ORDER BY x.ran_at DESC, x.id DESC LIMIT 1)
		   FROM d WHERE d.detector IS NOT NULL`

// TestPG_LatestSweepRuns: with 10,000 runs of six detectors, LatestSweepRuns
// returns each detector's newest run, and its plan reads the runs only
// through the (detector, ran_at, id) index: a handful of rows per detector,
// never the table (T1183 review, change 3).
func TestPG_LatestSweepRuns(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateSweepRunTable(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO smeldr_sweep_runs (id, detector, ran_at, interval, walked, flagged, skipped, err)
		SELECT 'run-' || lpad(g::text, 5, '0'), 'det-' || (g % 6), timestamptz '2026-01-01' + g * interval '1 minute', '1h', g, 0, 0, ''
		  FROM generate_series(0, 9999) g`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `ANALYZE smeldr_sweep_runs`); err != nil {
		t.Fatal(err)
	}
	got, err := smeldr.LatestSweepRuns(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 6 {
		t.Fatalf("got %d detectors", len(got))
	}
	for i, r := range got {
		if r.Detector != fmt.Sprintf("det-%d", i) || r.Walked < 9994 || !r.RanAt.After(time.Date(2026, 1, 7, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("%s: newest = %+v", r.Detector, r)
		}
	}
	rows, err := db.QueryContext(ctx, `EXPLAIN (ANALYZE, COSTS OFF, TIMING OFF, SUMMARY OFF) `+latestSweepRunIDsQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, line)
	}
	text := strings.Join(plan, "\n")
	t.Logf("plan:\n%s", text)
	if strings.Contains(text, "Seq Scan on smeldr_sweep_runs") {
		t.Errorf("a full scan of the runs:\n%s", text)
	}
}
