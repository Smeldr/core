package smeldr

import (
	"context"
	"errors"
	"testing"
	"time"
)

// T1287, D195: an act through a client records the token's identity as the actor, the
// token behind it as the means, and the client as via; another actor's
// record in the same request takes neither.
func TestProvenance_Via(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateProvenanceTable(db); err != nil {
		t.Fatal(err)
	}
	ps := NewProvenanceStore(db)
	ctx := NewTestContext(User{ID: "person-1", Roles: []Role{Author, Human}, TokenID: "fp-1", Via: "https://client.example/app"})
	for _, r := range []ProvenanceRecord{
		{ID: "p1", Timestamp: time.Now(), SubjectType: "Task", SubjectID: "t1", Verb: "create", ActorKind: "human", ActorID: "person-1"},
		{ID: "p2", Timestamp: time.Now(), SubjectType: "Task", SubjectID: "t1", Verb: "update", ActorKind: "job", ActorID: "sweeper"},
	} {
		if err := ps.Append(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	recs, err := ps.List(context.Background(), ProvenanceFilter{SubjectID: "t1"})
	if err != nil || len(recs) != 2 {
		t.Fatalf("records = %+v %v", recs, err)
	}
	for _, r := range recs {
		want := ""
		if r.ActorID == "person-1" {
			want = "https://client.example/app"
			if r.Means != "fp-1" {
				t.Errorf("means = %q", r.Means)
			}
		}
		if r.Via != want {
			t.Errorf("%s: via = %q, want %q", r.ActorID, r.Via, want)
		}
	}
}

// A table made before the via column: the record is written and read with
// its means, and without via.
func TestProvenance_NoViaColumn(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateProvenanceTable(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE smeldr_provenance DROP COLUMN via`); err != nil {
		t.Fatal(err)
	}
	ps := NewProvenanceStore(db)
	ctx := NewTestContext(User{ID: "person-1", TokenID: "fp-1", Via: "https://client.example/app"})
	if err := ps.Append(ctx, ProvenanceRecord{ID: "p1", Timestamp: time.Now(), SubjectType: "Task", SubjectID: "t1", Verb: "create", ActorKind: "human", ActorID: "person-1"}); err != nil {
		t.Fatal(err)
	}
	recs, err := ps.List(context.Background(), ProvenanceFilter{SubjectID: "t1"})
	if err != nil || len(recs) != 1 || recs[0].Means != "fp-1" || recs[0].Via != "" {
		t.Errorf("records = %+v %v", recs, err)
	}
	// A fresh store finds the column missing on its first read too.
	if recs, err := NewProvenanceStore(db).List(context.Background(), ProvenanceFilter{SubjectID: "t1"}); err != nil || len(recs) != 1 {
		t.Errorf("fresh store list = %+v %v", recs, err)
	}
	// And a table with neither means nor via is read from its first List.
	if _, err := db.Exec(`ALTER TABLE smeldr_provenance DROP COLUMN means`); err != nil {
		t.Fatal(err)
	}
	if recs, err := NewProvenanceStore(db).List(context.Background(), ProvenanceFilter{SubjectID: "t1"}); err != nil || len(recs) != 1 || recs[0].Means != "" {
		t.Errorf("no means, no via: %+v %v", recs, err)
	}
}

// A list read that fails for another reason than a missing column is an
// error, not a fall back to fewer columns.
func TestProvenance_ListFails(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateProvenanceTable(db); err != nil {
		t.Fatal(err)
	}
	ps := NewProvenanceStore(faultDB{DB: db, match: "FROM smeldr_provenance"})
	if _, err := ps.List(context.Background(), ProvenanceFilter{SubjectID: "t1"}); !errors.Is(err, errFault) {
		t.Errorf("err = %v, want the fault", err)
	}
	if err := ps.Append(NewTestContext(User{ID: "u"}), ProvenanceRecord{ID: "p1", Timestamp: time.Now(), ActorID: "u"}); err != nil {
		t.Errorf("append = %v", err)
	}
}
