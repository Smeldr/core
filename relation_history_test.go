// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"
)

// newHistoryStore is a relation store over SQLite with the real provenance
// store wired, and a registered "tagged" kind (article -> tag).
func newHistoryStore(t *testing.T) (*RelationStore, *sql.DB) {
	t.Helper()
	store, _ := setupLayer1Store(t)
	db := store.db.(*sql.DB)
	if err := CreateProvenanceTable(db); err != nil {
		t.Fatalf("CreateProvenanceTable: %v", err)
	}
	store.setProvenanceStore(NewProvenanceStore(db))
	upsertTestKind(t, store, "tagged", "article", "tag")
	return store, db
}

func histCtx(id string) Context { return NewTestContext(User{ID: id, Roles: []Role{Editor}}) }

func tagEdge() RelationEdge {
	return RelationEdge{SourceType: "article", SourceID: "art-1", TargetType: "tag", TargetID: "tag-1",
		RelationKind: "tagged", EdgeClass: "asserted"}
}

// edgeRecords returns the provenance records of one relation row in life
// order: a row's life is one assert (possibly repeated) and at most one
// invalidate, and the store's timestamps are second resolution, so the order is
// by verb, then time.
func edgeRecords(t *testing.T, db *sql.DB, id string) []ProvenanceRecord {
	t.Helper()
	recs, err := NewProvenanceStore(db).List(context.Background(), ProvenanceFilter{SubjectType: "RelationEdge", SubjectID: id})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	sort.SliceStable(recs, func(i, j int) bool {
		if (recs[i].Verb == "assert") != (recs[j].Verb == "assert") {
			return recs[i].Verb == "assert"
		}
		return recs[i].Timestamp.Before(recs[j].Timestamp)
	})
	return recs
}

// rowOfKind returns the one row of kind among rows.
func rowOfKind(t *testing.T, rows []RelationEdge, kind string) RelationEdge {
	t.Helper()
	for _, r := range rows {
		if r.RelationKind == kind {
			return r
		}
	}
	t.Fatalf("no %s row in %+v", kind, rows)
	return RelationEdge{}
}

func verbs(recs []ProvenanceRecord) string {
	var out []string
	for _, r := range recs {
		out = append(out, r.Verb+":"+r.ToState)
	}
	return strings.Join(out, ",")
}

// One row is one life: assert, withdraw, assert again leaves two rows with
// different IDs in creation order, the first ended and the second live, and two
// provenance pairs.
func TestRelationHistory_EndThenReaddTwoRows(t *testing.T) {
	store, db := newHistoryStore(t)
	ctx := histCtx("alice")
	if err := store.Assert(ctx, tagEdge()); err != nil {
		t.Fatalf("assert: %v", err)
	}
	first, _ := store.GetLiveBySource(ctx, "article", "art-1", "")
	if err := store.Withdraw(ctx, first[0].ID, "no longer tagged"); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := store.Assert(histCtx("bob"), tagEdge()); err != nil {
		t.Fatalf("re-assert: %v", err)
	}

	rows, err := store.GetBySource(ctx, "article", "art-1", "")
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows = %+v, %v; want two lives", rows, err)
	}
	if rows[0].ID != first[0].ID || rows[0].InvalidAt == nil || rows[1].InvalidAt != nil || rows[1].ID == rows[0].ID {
		t.Errorf("rows = %+v; want the first life ended and a new live row after it", rows)
	}
	if got := verbs(edgeRecords(t, db, rows[0].ID)); got != "assert:,invalidate:withdrawn" {
		t.Errorf("first life records = %s", got)
	}
	if got := verbs(edgeRecords(t, db, rows[1].ID)); got != "assert:" {
		t.Errorf("second life records = %s", got)
	}
	end := edgeRecords(t, db, rows[0].ID)[1]
	if end.ActorID != "alice" || end.Reason != "no longer tagged" || end.FromState != "live" {
		t.Errorf("invalidate = %+v; want alice's withdrawal with her reason", end)
	}
}

// Re-asserting a live relation still updates its one row.
func TestRelationHistory_ReassertLiveKeepsOneRow(t *testing.T) {
	store, _ := newHistoryStore(t)
	ctx := histCtx("alice")
	for i := 0; i < 2; i++ {
		if err := store.Assert(ctx, tagEdge()); err != nil {
			t.Fatalf("assert %d: %v", i, err)
		}
	}
	if n := countEdges(t, store, "article", "art-1"); n != 1 {
		t.Errorf("rows = %d; want 1", n)
	}
}

func TestRelationStore_Withdraw(t *testing.T) {
	store, db := newHistoryStore(t)
	ctx := histCtx("alice")
	if err := store.Assert(ctx, tagEdge()); err != nil {
		t.Fatal(err)
	}
	live, _ := store.GetLiveBySource(ctx, "article", "art-1", "")
	id := live[0].ID

	if err := store.Withdraw(ctx, id, "  "); !errors.As(err, new(*ValidationError)) {
		t.Errorf("empty reason = %v; want a ValidationError", err)
	}
	if l, _ := store.GetLiveBySource(ctx, "article", "art-1", ""); len(l) != 1 {
		t.Error("an empty reason ended the relation")
	}
	if err := store.Withdraw(ctx, "no-such-edge", "r"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id = %v; want ErrNotFound", err)
	}
	if err := store.Withdraw(ctx, id, "done"); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	err := store.Withdraw(ctx, id, "again")
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "already ended at") {
		t.Errorf("withdrawing an ended row = %v; want ErrConflict naming when it ended", err)
	}
	if got := verbs(edgeRecords(t, db, id)); got != "assert:,invalidate:withdrawn" {
		t.Errorf("records = %s; the refused calls must record nothing", got)
	}
}

// failingProvenance refuses every Append.
type failingProvenance struct{ fakeProvenanceStore }

func (*failingProvenance) Append(context.Context, ProvenanceRecord) error {
	return errors.New("provenance down")
}

func TestRelationStore_Withdraw_DBErrors(t *testing.T) {
	t.Run("the update fails", func(t *testing.T) {
		store, db := newHistoryStore(t)
		if _, err := db.Exec(`DROP TABLE smeldr_relations`); err != nil {
			t.Fatal(err)
		}
		if err := store.Withdraw(histCtx("alice"), "e1", "r"); !errors.Is(err, ErrInternal) {
			t.Errorf("err = %v; want ErrInternal", err)
		}
	})
	t.Run("the provenance write fails: the end is kept", func(t *testing.T) {
		store, _ := newHistoryStore(t)
		ctx := histCtx("alice")
		if err := store.Assert(ctx, tagEdge()); err != nil {
			t.Fatal(err)
		}
		live, _ := store.GetLiveBySource(ctx, "article", "art-1", "")
		store.setProvenanceStore(&failingProvenance{})
		if err := store.Withdraw(ctx, live[0].ID, "r"); err != nil {
			t.Fatalf("withdraw with provenance down = %v; want success", err)
		}
		if l, _ := store.GetLiveBySource(ctx, "article", "art-1", ""); len(l) != 0 {
			t.Error("the end was not kept")
		}
	})
}

func TestSweepStructural_RecordsInvalidate(t *testing.T) {
	store, db := newHistoryStore(t)
	if err := store.Assert(histCtx("alice"), tagEdge()); err != nil {
		t.Fatal(err)
	}
	dead := func(_ context.Context, typ, _ string) (bool, error) { return typ != "tag", nil }
	_, flagged, _, err := store.SweepStructural(context.Background(), dead, func(context.Context, RelationEdge) {})
	if err != nil || flagged != 1 {
		t.Fatalf("sweep = %d, %v; want one flagged", flagged, err)
	}
	rows, _ := store.GetBySource(context.Background(), "article", "art-1", "")
	recs := edgeRecords(t, db, rows[0].ID)
	if got := verbs(recs); got != "assert:,invalidate:swept" {
		t.Fatalf("records = %s", got)
	}
	if recs[1].ActorKind != "job" || recs[1].ActorID != sweepStructuralJob || recs[1].Reason == "" {
		t.Errorf("sweep end = %+v; want job sweep-structural with a reason", recs[1])
	}
	// An already ended row is not flagged again.
	_, flagged, _, _ = store.SweepStructural(context.Background(), dead, func(context.Context, RelationEdge) {})
	if flagged != 0 {
		t.Errorf("second sweep flagged %d; want 0", flagged)
	}
}

func TestRecomputeAsserted_EndsInsteadOfDeletes(t *testing.T) {
	store, db := newHistoryStore(t)
	ctx := histCtx("writer-1")
	two := []RelationEdge{
		{TargetType: "tag", TargetID: "tag-1", RelationKind: "tagged"},
		{TargetType: "tag", TargetID: "tag-2", RelationKind: "tagged"},
	}
	if err := store.RecomputeAsserted(ctx, "article", "art-1", two); err != nil {
		t.Fatal(err)
	}
	if err := store.RecomputeAsserted(ctx, "article", "art-1", two[:1]); err != nil {
		t.Fatal(err)
	}
	rows, _ := store.GetBySource(ctx, "article", "art-1", "")
	var tag2 RelationEdge
	for _, r := range rows {
		if r.TargetID == "tag-2" {
			tag2 = r
		}
	}
	if tag2.ID == "" || tag2.InvalidAt == nil {
		t.Fatalf("tag-2 row = %+v; want it kept, ended", tag2)
	}
	recs := edgeRecords(t, db, tag2.ID)
	if got := verbs(recs); got != "assert:,invalidate:recomputed" {
		t.Fatalf("records = %s", got)
	}
	if recs[0].ActorID != "writer-1" || recs[1].ActorID != "writer-1" {
		t.Errorf("records = %+v; want the content write's actor on both", recs)
	}
	if tag2.CreatedBy == nil || *tag2.CreatedBy != "writer-1" {
		t.Errorf("CreatedBy = %v; want writer-1", tag2.CreatedBy)
	}

	// The field names tag-2 again: a new life, not the old row revived.
	if err := store.RecomputeAsserted(ctx, "article", "art-1", two); err != nil {
		t.Fatal(err)
	}
	live, _ := store.GetLiveBySource(ctx, "article", "art-1", "")
	if len(live) != 2 || countEdges(t, store, "article", "art-1") != 3 {
		t.Errorf("live %d, rows %d; want 2 live and 3 rows", len(live), countEdges(t, store, "article", "art-1"))
	}
}

func TestBulkRecompute_NoCallerRecordsTheJob(t *testing.T) {
	store, db := newHistoryStore(t)
	ctx := context.Background()
	src := func(in ...RelationEdge) []RelationSource {
		return []RelationSource{{SourceType: "article", SourceID: "art-1", Incoming: in}}
	}
	if err := store.BulkRecompute(ctx, src(RelationEdge{TargetType: "tag", TargetID: "tag-1", RelationKind: "tagged"})); err != nil {
		t.Fatal(err)
	}
	if err := store.BulkRecompute(ctx, src()); err != nil {
		t.Fatal(err)
	}
	rows, _ := store.GetBySource(ctx, "article", "art-1", "")
	for _, r := range edgeRecords(t, db, rows[0].ID) {
		if r.ActorKind != "job" || r.ActorID != relationRecomputeJob {
			t.Errorf("record %+v; want job relation-recompute", r)
		}
	}
	if rows[0].CreatedBy != nil {
		t.Errorf("CreatedBy = %q; want none for a job", *rows[0].CreatedBy)
	}
}

func TestRelationStore_DeletePurgeRecordsFirst(t *testing.T) {
	store, db := newHistoryStore(t)
	ctx := histCtx("admin-1")
	if err := store.Assert(ctx, tagEdge()); err != nil {
		t.Fatal(err)
	}
	rows, _ := store.GetBySource(ctx, "article", "art-1", "")
	if err := store.Delete(ctx, rows[0].ID); err != nil {
		t.Fatal(err)
	}
	if n := countEdges(t, store, "article", "art-1"); n != 0 {
		t.Errorf("rows = %d; want the purge to remove the row", n)
	}
	recs := edgeRecords(t, db, rows[0].ID)
	if got := verbs(recs); got != "assert:,invalidate:purged" || recs[1].ActorID != "admin-1" {
		t.Errorf("records = %s %+v", got, recs)
	}
	before := len(edgeRecords(t, db, "missing"))
	if err := store.Delete(ctx, "missing"); err != nil || len(edgeRecords(t, db, "missing")) != before {
		t.Errorf("purging an unknown id = %v; want a no-op with no record", err)
	}
}

func TestInsertEdge_ExplicitIDOfEndedRowRefused(t *testing.T) {
	store, _ := newHistoryStore(t)
	ctx := histCtx("alice")
	if err := store.Assert(ctx, tagEdge()); err != nil {
		t.Fatal(err)
	}
	rows, _ := store.GetBySource(ctx, "article", "art-1", "")
	if err := store.Withdraw(ctx, rows[0].ID, "r"); err != nil {
		t.Fatal(err)
	}
	e := tagEdge()
	e.ID = rows[0].ID
	if err := store.Assert(ctx, e); !errors.Is(err, ErrConflict) {
		t.Errorf("asserting with the ended row's id = %v; want ErrConflict", err)
	}
	if l, _ := store.GetLiveBySource(ctx, "article", "art-1", ""); len(l) != 0 {
		t.Error("the ended row was revived")
	}
	// An explicit id of a live row still updates it.
	if err := store.Assert(ctx, tagEdge()); err != nil {
		t.Fatal(err)
	}
	live, _ := store.GetLiveBySource(ctx, "article", "art-1", "")
	e.ID = live[0].ID
	if err := store.Assert(ctx, e); err != nil {
		t.Errorf("asserting with a live row's id = %v", err)
	}
}

// get_relations' data: every row in creation order, each ended one with how it
// ended; an end with no record reads not-recorded.
func TestMCPGetRelations_EndedCause(t *testing.T) {
	store, db := newHistoryStore(t)
	upsertTestKind(t, store, "cites", "article", "tag")
	ctx := histCtx("alice")
	if err := store.Assert(ctx, tagEdge()); err != nil {
		t.Fatal(err)
	}
	e2 := tagEdge()
	e2.RelationKind = "cites"
	if err := store.Assert(ctx, e2); err != nil {
		t.Fatal(err)
	}
	rows, _ := store.GetBySource(ctx, "article", "art-1", "")
	tagged, cites := rowOfKind(t, rows, "tagged"), rowOfKind(t, rows, "cites")
	if err := store.Withdraw(ctx, tagged.ID, "wrong tag"); err != nil {
		t.Fatal(err)
	}
	// An end written before relation history: no record.
	if _, err := db.Exec(`UPDATE smeldr_relations SET invalid_at=$1 WHERE id=$2`, time.Now().UTC().Add(-time.Minute), cites.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Assert(ctx, tagEdge()); err != nil {
		t.Fatal(err)
	}

	got, err := store.MCPGetRelations(ctx, "article", "art-1", "both", "")
	if err != nil || len(got) != 3 {
		t.Fatalf("got %d edges, %v; want 3", len(got), err)
	}
	byID := map[string]RelationEdge{}
	for i, g := range got {
		byID[g.ID] = g
		if i > 0 && g.CreatedAt.Before(got[i-1].CreatedAt) {
			t.Errorf("rows not in creation order: %v before %v", got[i-1].CreatedAt, g.CreatedAt)
		}
	}
	if e := byID[tagged.ID].Ended; e == nil || e.Cause != EdgeEndWithdrawn || e.ActorID != "alice" || e.Reason != "wrong tag" {
		t.Errorf("withdrawn row = %+v", e)
	}
	if e := byID[cites.ID].Ended; e == nil || e.Cause != EdgeEndNotRecorded || e.ActorID != "" {
		t.Errorf("unrecorded end = %+v; want not-recorded", e)
	}
	live := 0
	for _, g := range got {
		if g.InvalidAt == nil {
			live++
			if g.Ended != nil || g.ID == tagged.ID {
				t.Errorf("live row %+v; want a new row with no Ended", g)
			}
		}
	}
	if live != 1 {
		t.Errorf("%d live rows; want the one new life", live)
	}

	// A provenance store without the batched read is read one subject at a time.
	fake := &fakeProvenanceStore{listed: []ProvenanceRecord{{SubjectType: "RelationEdge", SubjectID: cites.ID, Verb: "invalidate", ToState: EdgeEndSwept}}}
	store.setProvenanceStore(fake)
	got, err = store.MCPGetRelations(ctx, "article", "art-1", "source", "cites")
	if err != nil || len(got) != 1 || got[0].Ended == nil || got[0].Ended.Cause != EdgeEndSwept {
		t.Errorf("fallback read = %+v, %v", got, err)
	}

	// No provenance store at all: every end reads not-recorded.
	store.setProvenanceStore(nil)
	got, _ = store.MCPGetRelations(ctx, "article", "art-1", "source", "cites")
	if got[0].Ended == nil || got[0].Ended.Cause != EdgeEndNotRecorded {
		t.Errorf("without a store = %+v", got[0].Ended)
	}

	if _, err := store.MCPGetRelations(ctx, "article", "art-1", "sideways", ""); err == nil {
		t.Error("a bad direction was accepted")
	}
}

// listErrProvenance fails every List, to reach the read-error path.
type listErrProvenance struct{ fakeProvenanceStore }

func (*listErrProvenance) List(context.Context, ProvenanceFilter) ([]ProvenanceRecord, error) {
	return nil, errors.New("provenance down")
}

func TestMCPGetRelations_EndReadFailsIsInternal(t *testing.T) {
	store, _ := newHistoryStore(t)
	ctx := histCtx("alice")
	if err := store.Assert(ctx, tagEdge()); err != nil {
		t.Fatal(err)
	}
	rows, _ := store.GetBySource(ctx, "article", "art-1", "")
	if err := store.Withdraw(ctx, rows[0].ID, "r"); err != nil {
		t.Fatal(err)
	}
	store.setProvenanceStore(&listErrProvenance{})
	if _, err := store.MCPGetRelations(ctx, "article", "art-1", "target", ""); err != nil {
		t.Errorf("no ended row on this side must not read provenance: %v", err)
	}
	if _, err := store.MCPGetRelations(ctx, "article", "art-1", "source", ""); !errors.Is(err, ErrInternal) {
		t.Errorf("err = %v; want ErrInternal", err)
	}
}

func TestRelationHistory_ReadErrorPaths(t *testing.T) {
	t.Run("the batched provenance read fails", func(t *testing.T) {
		store, db := newHistoryStore(t)
		ctx := histCtx("alice")
		if err := store.Assert(ctx, tagEdge()); err != nil {
			t.Fatal(err)
		}
		rows, _ := store.GetBySource(ctx, "article", "art-1", "")
		if err := store.Withdraw(ctx, rows[0].ID, "r"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`DROP TABLE smeldr_provenance`); err != nil {
			t.Fatal(err)
		}
		if _, err := store.MCPGetRelations(ctx, "article", "art-1", "source", ""); !errors.Is(err, ErrInternal) {
			t.Errorf("err = %v; want ErrInternal", err)
		}
	})
}

// The Amendment's status-change step is fail-open on every path that cannot
// end the edge, and does nothing for another state or another type.
func TestAmendsStatusChanged_FailOpen(t *testing.T) {
	ctx := histCtx("alice")
	a := &Amendment{Node: Node{ID: "am-1"}}
	amendsStatusChanged(ctx, nil, nil, a, "in-progress", "rejected") // no relation store: logged
	amendsStatusChanged(ctx, nil, nil, &Task{}, "a", "rejected")     // not an Amendment
	store, db := newHistoryStore(t)
	amendsStatusChanged(ctx, nil, store, a, "scoped", "in-progress") // not a rejection
	if _, err := db.Exec(`DROP TABLE smeldr_relations`); err != nil {
		t.Fatal(err)
	}
	amendsStatusChanged(ctx, nil, store, a, "in-progress", "rejected") // the read fails: logged
}
