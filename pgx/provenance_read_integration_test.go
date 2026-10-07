//go:build integration

package pgx

import (
	"context"
	"fmt"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// App.ItemProvenance on Postgres: the newest-first order with the id tie-break,
// paging, the TIMESTAMPTZ round trip, and the members and gated views over a real
// flow with a Strict RequiredOperation transition.
func TestPG_ItemProvenance(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateProvenanceTable(db); err != nil {
		t.Fatalf("CreateProvenanceTable: %v", err)
	}
	app, _ := newPGApp(t, db)
	store := smeldr.NewProvenanceStore(db)
	app.Provenance(store)
	registerType(app, "PgItem")
	if err := app.RegisterFlow(pgFlow("PgItem")); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}

	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	add := func(id string, at time.Time, verb, from, to, reason string) {
		t.Helper()
		if err := store.Append(ctx, smeldr.ProvenanceRecord{ID: id, Timestamp: at, SubjectType: "PgItem", SubjectID: "i1", Verb: verb,
			FromState: from, ToState: to, ActorKind: "agent", ActorID: "actor-" + id, Surface: "mcp", Reason: reason}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	add("r1", t0, "create", "", "draft", "created")
	add("r2", t0.Add(time.Minute), "transition", "draft", "review", "to review")
	add("r3", t0.Add(2*time.Minute), "transition", "review", "published", "approved")
	add("r4", t0.Add(2*time.Minute), "update", "published", "published", "same-second edit")
	if err := store.Append(ctx, smeldr.ProvenanceRecord{ID: "x", Timestamp: t0, SubjectType: "PgItem", SubjectID: "other", Verb: "create"}); err != nil {
		t.Fatalf("Append other: %v", err)
	}

	m, err := app.ItemProvenance(ctx, "PgItem", "i1", smeldr.ProvenanceMembers, 0, 0)
	if err != nil || m.Total != 4 || len(m.Entries) != 4 {
		t.Fatalf("members = %+v, %v", m, err)
	}
	for i, want := range []string{"same-second edit", "approved", "to review", "created"} {
		if m.Entries[i].Reason != want {
			t.Errorf("entry %d = %q, want %q (newest first, same-second tie by id desc)", i, m.Entries[i].Reason, want)
		}
		if m.Entries[i].ActorID == "" {
			t.Errorf("members entry %d has no actor", i)
		}
	}
	if !m.Entries[0].Timestamp.Equal(t0.Add(2 * time.Minute)) {
		t.Errorf("timestamp round trip = %v, want %v", m.Entries[0].Timestamp, t0.Add(2*time.Minute))
	}
	if !m.Entries[1].Gated || m.Entries[2].Gated {
		t.Errorf("Gated = %v for review to published, %v for draft to review, want true and false", m.Entries[1].Gated, m.Entries[2].Gated)
	}

	g, err := app.ItemProvenance(ctx, "PgItem", "i1", smeldr.ProvenanceGated, 0, 0)
	if err != nil {
		t.Fatalf("gated: %v", err)
	}
	for i, e := range g.Entries {
		if i == 1 && e.ActorID == "" {
			t.Errorf("the gated transition lost its actor: %+v", e)
		}
		if i != 1 && (e.ActorID != "" || e.Reason != "") {
			t.Errorf("gated entry %d leaked its actor: %+v", i, e)
		}
	}

	p, err := app.ItemProvenance(ctx, "PgItem", "i1", smeldr.ProvenanceMembers, 2, 1)
	if err != nil || p.Total != 4 || len(p.Entries) != 2 || p.Entries[0].Reason != "approved" {
		t.Errorf("page = %+v, %v", p, err)
	}

	// A longer history pages in a stable order.
	for i := 0; i < 30; i++ {
		add(fmt.Sprintf("m%03d", i), t0.Add(time.Hour), "update", "published", "published", fmt.Sprintf("bulk %02d", i))
	}
	first, _ := app.ItemProvenance(ctx, "PgItem", "i1", smeldr.ProvenanceMembers, 10, 0)
	again, _ := app.ItemProvenance(ctx, "PgItem", "i1", smeldr.ProvenanceMembers, 10, 0)
	next, _ := app.ItemProvenance(ctx, "PgItem", "i1", smeldr.ProvenanceMembers, 10, 10)
	for i := range first.Entries {
		if first.Entries[i].Reason != again.Entries[i].Reason {
			t.Errorf("page changed between two identical calls at %d", i)
		}
		if first.Entries[i].Reason == next.Entries[i].Reason {
			t.Errorf("pages overlap at %d", i)
		}
	}
	if first.Total != 34 || first.Entries[0].Reason != "bulk 29" {
		t.Errorf("first = total %d, newest %q, want 34 and bulk 29", first.Total, first.Entries[0].Reason)
	}
}
