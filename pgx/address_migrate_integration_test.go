//go:build integration

package pgx

import (
	"context"
	"fmt"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// The migration on Postgres: a database with rows from before addresses, then
// records created after the counters were seeded. Kept and renumbered rows,
// the reserve for Signals, a dry run that writes nothing, and a second run
// that changes nothing.
func TestPG_MigrateAddresses(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateOrchestrationTables(db); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`DELETE FROM smeldr_address_counters`)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, n := range []string{"D2", "D2", "D9", "X-1"} {
		exec(`INSERT INTO smeldr_decisions (id, slug, created_at, updated_at, decision_number) VALUES ($1, $1, $2, $2, $3)`,
			fmt.Sprintf("d%d", i), base.Add(time.Duration(i)*time.Minute), n)
	}
	for i := range 2 {
		exec(`INSERT INTO smeldr_signals (id, slug, created_at, updated_at) VALUES ($1, $1, $2, $2)`,
			fmt.Sprintf("s%d", i), base.Add(time.Duration(10+i)*time.Minute))
	}
	if err := smeldr.CreateOrchestrationTables(db); err != nil {
		t.Fatal(err)
	}
	s := &smeldr.Signal{Sender: "core", SignalType: "notice"}
	if err := smeldr.InsertSignal(ctx, db, s); err != nil || s.Address != "S3" {
		t.Fatalf("post-deploy Signal = %q %v, want S3", s.Address, err)
	}

	count := func() int {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_decisions WHERE address <> ''`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if _, err := smeldr.MigrateAddresses(ctx, db, smeldr.AddressMigrationOptions{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 0 {
		t.Fatalf("the dry run wrote %d addresses", n)
	}
	rep, err := smeldr.MigrateAddresses(ctx, db, smeldr.AddressMigrationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"d0": "D10", "d1": "D11", "d2": "D9", "d3": "D12"}
	for id, addr := range want {
		var got string
		if err := db.QueryRowContext(ctx, `SELECT address FROM smeldr_decisions WHERE id = $1`, id).Scan(&got); err != nil || got != addr {
			t.Errorf("%s = %q %v, want %s", id, got, err, addr)
		}
	}
	for id, addr := range map[string]string{"s0": "S1", "s1": "S2"} {
		var got string
		if err := db.QueryRowContext(ctx, `SELECT address FROM smeldr_signals WHERE id = $1`, id).Scan(&got); err != nil || got != addr {
			t.Errorf("%s = %q %v, want %s", id, got, err, addr)
		}
	}
	if rep.DurationMS < 0 {
		t.Errorf("duration = %d", rep.DurationMS)
	}
	again, err := smeldr.MigrateAddresses(ctx, db, smeldr.AddressMigrationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range again.Types {
		if tr.Kept+tr.Renumbered+tr.NewlyNumbered != 0 {
			t.Errorf("second run changed %+v", tr)
		}
	}
}
