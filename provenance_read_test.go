// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func appWithStore(db DB, store ProvenanceStore) *App {
	return &App{cfg: Config{DB: db}, provenanceStore: store}
}

// The members view carries the whole record on every entry; the gated view only
// on a Strict RequiredOperation transition; Gated says which was which.
func TestItemProvenance_AudiencesAndActor(t *testing.T) {
	recs := []ProvenanceRecord{
		{SubjectType: "GovPostStrict", SubjectID: "p1", Verb: "create", ToState: "draft", ActorKind: "agent", ActorID: "a1", Surface: "mcp", Reason: "r-create"},
		{SubjectType: "GovPostStrict", SubjectID: "p1", Verb: "update", FromState: "draft", ToState: "draft", ActorKind: "human", ActorID: "a2", Surface: "http", Reason: "r-update"},
		{SubjectType: "GovPostStrict", SubjectID: "p1", Verb: "transition", FromState: "draft", ToState: "published", ActorKind: "job", ActorID: "a3", Surface: "trigger", Reason: "r-gated"},
	}
	db, _ := setupGovStateStrictDB(t)
	app := appWithStore(db, &fakeProvenanceStore{listed: recs})
	ctx := context.Background()

	members, err := app.ItemProvenance(ctx, "GovPostStrict", "p1", ProvenanceMembers, 0, 0)
	if err != nil || members.Total != 3 || len(members.Entries) != 3 {
		t.Fatalf("members = %+v, %v", members, err)
	}
	for i, e := range members.Entries {
		if e.ActorID != recs[i].ActorID || e.ActorKind != recs[i].ActorKind || e.Surface != recs[i].Surface || e.Reason != recs[i].Reason {
			t.Errorf("members entry %d lost its actor: %+v", i, e)
		}
	}
	if members.Entries[0].Gated || members.Entries[1].Gated || !members.Entries[2].Gated {
		t.Errorf("Gated flags = %v %v %v, want false false true", members.Entries[0].Gated, members.Entries[1].Gated, members.Entries[2].Gated)
	}

	gated, err := app.ItemProvenance(ctx, "GovPostStrict", "p1", ProvenanceGated, 0, 0)
	if err != nil || len(gated.Entries) != 3 {
		t.Fatalf("gated = %+v, %v", gated, err)
	}
	for i := 0; i < 2; i++ {
		if e := gated.Entries[i]; e.ActorID != "" || e.ActorKind != "" || e.Surface != "" || e.Reason != "" {
			t.Errorf("gated entry %d (an ungated act) leaked its actor: %+v", i, e)
		}
	}
	if e := gated.Entries[2]; e.ActorID != "a3" || e.Reason != "r-gated" {
		t.Errorf("gated entry of a gated transition lost its actor: %+v", e)
	}
}

// A transition that is not Strict is not gated, so the gated view withholds it
// while the members view does not.
func TestItemProvenance_NonStrictTransitionIsWithheldFromGatedOnly(t *testing.T) {
	db, _ := setupGovStateDB(t)
	rec := ProvenanceRecord{SubjectType: "GovPost", SubjectID: "p1", Verb: "transition", FromState: "draft", ToState: "published", ActorKind: "human", ActorID: "a1", Reason: "x"}
	app := appWithStore(db, &fakeProvenanceStore{listed: []ProvenanceRecord{rec}})
	g, _ := app.ItemProvenance(context.Background(), "GovPost", "p1", ProvenanceGated, 0, 0)
	m, _ := app.ItemProvenance(context.Background(), "GovPost", "p1", ProvenanceMembers, 0, 0)
	if g.Entries[0].ActorID != "" || m.Entries[0].ActorID != "a1" || g.Entries[0].Gated {
		t.Errorf("gated %+v members %+v", g.Entries[0], m.Entries[0])
	}
}

// Newest first, ties by id, Total is the whole count, limit and offset slice it.
func TestItemProvenance_OrderAndPaging(t *testing.T) {
	app, _, store := sinceApp(t)
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 7; i++ {
		// Records 0..6; 5 and 6 share a second, so the id decides.
		ts := t0.Add(time.Duration(i) * time.Minute)
		if i == 6 {
			ts = t0.Add(5 * time.Minute)
		}
		appendRec(t, store, ProvenanceRecord{ID: fmt.Sprintf("r%d", i), Timestamp: ts, SubjectType: "Task", SubjectID: "t1", Verb: "update", FromState: "x", ToState: "x", Reason: fmt.Sprintf("n%d", i)})
	}
	appendRec(t, store, ProvenanceRecord{ID: "other", Timestamp: t0, SubjectType: "Task", SubjectID: "t2", Verb: "update", Reason: "not mine"})
	ctx := context.Background()

	all, err := app.ItemProvenance(ctx, "Task", "t1", ProvenanceMembers, 0, 0)
	if err != nil || all.Total != 7 || len(all.Entries) != 7 {
		t.Fatalf("all = %d/%d, %v", len(all.Entries), all.Total, err)
	}
	want := []string{"n6", "n5", "n4", "n3", "n2", "n1", "n0"}
	for i, w := range want {
		if all.Entries[i].Reason != w {
			t.Errorf("entry %d = %s, want %s (newest first, same-second tie by id desc)", i, all.Entries[i].Reason, w)
		}
	}
	p, _ := app.ItemProvenance(ctx, "Task", "t1", ProvenanceMembers, 3, 2)
	if p.Total != 7 || len(p.Entries) != 3 || p.Entries[0].Reason != "n4" || p.Entries[2].Reason != "n2" {
		t.Errorf("page = %+v", p)
	}
	end, _ := app.ItemProvenance(ctx, "Task", "t1", ProvenanceMembers, 3, 6)
	if len(end.Entries) != 1 || end.Total != 7 {
		t.Errorf("last page = %d entries total %d", len(end.Entries), end.Total)
	}
	past, err := app.ItemProvenance(ctx, "Task", "t1", ProvenanceMembers, 3, 50)
	if err != nil || past.Total != 7 || past.Entries == nil || len(past.Entries) != 0 {
		t.Errorf("past the end = %+v, %v, want an empty non-nil page with Total 7", past, err)
	}
}

func TestItemProvenance_LimitDefaultAndCap(t *testing.T) {
	app, _, store := sinceApp(t)
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 520; i++ {
		appendRec(t, store, ProvenanceRecord{ID: fmt.Sprintf("r%04d", i), Timestamp: t0.Add(time.Duration(i) * time.Second), SubjectType: "Task", SubjectID: "t1", Verb: "update"})
	}
	ctx := context.Background()
	def, _ := app.ItemProvenance(ctx, "Task", "t1", ProvenanceMembers, 0, 0)
	big, _ := app.ItemProvenance(ctx, "Task", "t1", ProvenanceMembers, 9999, 0)
	if len(def.Entries) != 50 || len(big.Entries) != 500 || big.Total != 520 {
		t.Errorf("default %d, capped %d, total %d, want 50, 500, 520", len(def.Entries), len(big.Entries), big.Total)
	}
}

// Visibility is never decided by a swallowed error: a gate lookup that fails is
// ErrInternal for both audiences, while SubjectProvenance keeps failing closed.
func TestItemProvenance_GateLookupErrorIsNotSwallowed(t *testing.T) {
	db, _ := setupGovStateStrictDB(t)
	rec := ProvenanceRecord{SubjectType: "GovPostStrict", SubjectID: "p1", Verb: "transition", FromState: "draft", ToState: "published", ActorID: "a1"}
	failing := &govQueryRowFailDB{DB: db, failOn: "FROM smeldr_transitions"}
	app := appWithStore(failing, &fakeProvenanceStore{listed: []ProvenanceRecord{rec}})
	for _, aud := range []ProvenanceAudience{ProvenanceMembers, ProvenanceGated} {
		if _, err := app.ItemProvenance(context.Background(), "GovPostStrict", "p1", aud, 0, 0); !errors.Is(err, ErrInternal) {
			t.Errorf("%s: err = %v, want ErrInternal", aud, err)
		}
	}
	// The older read still fails closed: no error, actor withheld.
	entries, err := SubjectProvenance(context.Background(), failing, &fakeProvenanceStore{listed: []ProvenanceRecord{rec}}, "GovPostStrict", "p1")
	if err != nil || len(entries) != 1 || entries[0].ActorID != "" {
		t.Errorf("SubjectProvenance = %+v, %v, want the fail-closed entry without an actor", entries, err)
	}
}

func TestItemProvenance_NoFlowAndUndeclaredEdgeAreNotErrors(t *testing.T) {
	db, _ := setupGovStateStrictDB(t)
	recs := []ProvenanceRecord{
		{SubjectType: "NoSuchFlowType", SubjectID: "x", Verb: "transition", FromState: "a", ToState: "b", ActorID: "a1"},
	}
	app := appWithStore(db, &fakeProvenanceStore{listed: recs})
	if p, err := app.ItemProvenance(context.Background(), "NoSuchFlowType", "x", ProvenanceGated, 0, 0); err != nil || len(p.Entries) != 1 || p.Entries[0].Gated {
		t.Errorf("no flow = %+v, %v", p, err)
	}
	recs[0].SubjectType, recs[0].FromState, recs[0].ToState = "GovPostStrict", "draft", "nonexistent-state"
	if p, err := app.ItemProvenance(context.Background(), "GovPostStrict", "x", ProvenanceGated, 0, 0); err != nil || p.Entries[0].Gated {
		t.Errorf("undeclared edge = %+v, %v", p, err)
	}
}

func TestItemProvenance_ArgumentAndWiringErrors(t *testing.T) {
	app, _, _ := sinceApp(t)
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"unknown audience": func() error { _, e := app.ItemProvenance(ctx, "Task", "t", "everyone", 0, 0); return e },
		"empty audience":   func() error { _, e := app.ItemProvenance(ctx, "Task", "t", "", 0, 0); return e },
		"negative offset":  func() error { _, e := app.ItemProvenance(ctx, "Task", "t", ProvenanceMembers, 0, -1); return e },
		"negative limit":   func() error { _, e := app.ItemProvenance(ctx, "Task", "t", ProvenanceMembers, -1, 0); return e },
	} {
		var ve *ValidationError
		if err := call(); !errors.As(err, &ve) {
			t.Errorf("%s: err = %v, want a ValidationError", name, err)
		}
	}
	if _, err := (&App{}).ItemProvenance(ctx, "Task", "t", ProvenanceMembers, 0, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("no store: err = %v, want ErrNotFound (not enabled)", err)
	}
	failing := appWithStore(nil, &listOnlyStore{err: errors.New("boom")})
	if _, err := failing.ItemProvenance(ctx, "Task", "t", ProvenanceMembers, 0, 0); !errors.Is(err, ErrInternal) {
		t.Errorf("store error: err = %v, want ErrInternal", err)
	}
}

func TestItemProvenance_StandingVerbsComeThrough(t *testing.T) {
	app, _, store := sinceApp(t)
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	appendRec(t, store, ProvenanceRecord{ID: "a", Timestamp: t0, SubjectType: "Decision", SubjectID: "d1", Verb: "standing-began", ToState: "holds"})
	appendRec(t, store, ProvenanceRecord{ID: "b", Timestamp: t0.Add(time.Hour), SubjectType: "Decision", SubjectID: "d1", Verb: "standing-ended"})
	p, err := app.ItemProvenance(context.Background(), "Decision", "d1", ProvenanceMembers, 0, 0)
	if err != nil || len(p.Entries) != 2 || p.Entries[0].Verb != "standing-ended" || p.Entries[1].Verb != "standing-began" {
		t.Errorf("standing events = %+v, %v", p, err)
	}
}
