//go:build integration

package pgx

import (
	"context"
	"fmt"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// App.ItemsStateSince on Postgres: the batch query (IN list, ORDER BY timestamp,
// id) and the TIMESTAMPTZ round trip of the provenance timestamp.
func TestPG_ItemsStateSince(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateProvenanceTable(db); err != nil {
		t.Fatalf("CreateProvenanceTable: %v", err)
	}
	store := smeldr.NewProvenanceStore(db)
	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte(pgTestSecret), DB: db})
	app.Provenance(store)

	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	add := func(id string, ts time.Time, typ, subj, verb, to, reason string) {
		t.Helper()
		if err := store.Append(ctx, smeldr.ProvenanceRecord{ID: id, Timestamp: ts, SubjectType: typ, SubjectID: subj, Verb: verb, ToState: to, Reason: reason}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	add("r1", t0, "Task", "a", "transition", "active", "claimed")
	add("r2", t0.Add(time.Hour), "Task", "a", "transition", "blocked", "waiting on X")
	add("r3", t0.Add(2*time.Hour), "Task", "a", "update", "blocked", "an edit")
	add("r4", t0, "Task", "b", "transition", "x", "first")
	add("r5", t0, "Task", "b", "transition", "x", "second")
	add("r6", t0.Add(time.Hour), "Goal", "c", "transition", "active", "other type")

	got, err := app.ItemsStateSince(ctx, "Task", map[string]string{"a": "blocked", "b": "x", "c": "active", "d": "active"})
	if err != nil {
		t.Fatalf("ItemsStateSince: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %+v, want a and b only", got)
	}
	if a := got["a"]; !a.Since.Equal(t0.Add(time.Hour)) || a.Reason != "waiting on X" {
		t.Errorf("a = %+v", a)
	}
	if got["b"].Reason != "second" {
		t.Errorf("b = %+v, want the greater id to win the same-second tie", got["b"])
	}
	// A current state that is not the latest transition's ToState is unknown.
	if m, _ := app.ItemsStateSince(ctx, "Task", map[string]string{"a": "done"}); len(m) != 0 {
		t.Errorf("mismatch = %+v, want empty", m)
	}

	// More than one chunk of ids.
	big := map[string]string{"a": "blocked"}
	for i := 0; i < 900; i++ {
		big[fmt.Sprintf("id%04d", i)] = "x"
	}
	if m, err := app.ItemsStateSince(ctx, "Task", big); err != nil || len(m) != 1 {
		t.Errorf("chunked = %d entries, %v, want 1", len(m), err)
	}
}
