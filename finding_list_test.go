package smeldr

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// findingsFixture is the approvers fixture with findings wired: Decisions
// dec-in (in are-1, which reader-1 reads) and dec-out (in are-2, which it
// does not), and five findings: on each Decision, on an edge between two
// readable Decisions, on an edge from dec-in to dec-out, and on a Decision
// that no longer exists.
type findingsFixture struct {
	areaGateFixture
	readableEdge, mixedEdge string
}

func newFindingsFixture(t *testing.T) findingsFixture {
	t.Helper()
	f := findingsFixture{areaGateFixture: newApproversFixture(t)}
	ctx := context.Background()
	if err := CreateFindingTable(f.db); err != nil {
		t.Fatal(err)
	}
	store := NewFindingStore(f.db)
	f.app.Findings(store)
	human := as("human-1", Human)
	for _, d := range []struct{ id, area string }{{"dec-in", f.area}, {"dec-in2", f.area}, {"dec-out", "are-2"}} {
		f.decision(t, d.id, "ratified")
		if err := f.place(human, "Decision", d.id, "Area", d.area, "asserted"); err != nil {
			t.Fatal(err)
		}
	}
	edge := func(src, dst string) string {
		e, err := f.rels.insertEdge(human, RelationEdge{SourceType: "Decision", SourceID: src, TargetType: "Decision", TargetID: dst,
			RelationKind: "grounded_in", EdgeClass: "asserted", Attributes: json.RawMessage("{}")})
		if err != nil {
			t.Fatal(err)
		}
		return e.ID
	}
	f.readableEdge, f.mixedEdge = edge("dec-in", "dec-in2"), edge("dec-in", "dec-out")
	for _, fd := range []Finding{
		{Detector: "standing-drift", SubjectType: "Decision", SubjectID: "dec-in", Provenance: "detected", Message: "in drifted"},
		{Detector: "standing-drift", SubjectType: "Decision", SubjectID: "dec-out", Provenance: "detected", Message: "secret-out drifted"},
		{Detector: "standing-drift", SubjectType: "Decision", SubjectID: "gone", Provenance: "detected", Message: "gone drifted"},
		{Detector: "structural", SubjectType: "RelationEdge", SubjectID: f.readableEdge, Provenance: "detected", Message: "edge readable"},
		{Detector: "structural", SubjectType: "RelationEdge", SubjectID: f.mixedEdge, Provenance: "detected", Message: "secret-mixed edge"},
	} {
		if err := store.Record(ctx, fd); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func subjects(list []FindingView) []string {
	var out []string
	for _, f := range list {
		out = append(out, f.SubjectType+":"+f.SubjectID)
	}
	return out
}

// TestListFindings (T953): a reader sees a finding only when it may read its
// subject; a scope that reads everything sees all.
func TestListFindings(t *testing.T) {
	f := newFindingsFixture(t)
	all, err := f.app.ListFindings(as("human-1", Human).(Context), FindingQuery{})
	if err != nil || len(all) != 5 {
		t.Fatalf("a global reader = %v %v, want 5", subjects(all), err)
	}
	got, err := f.app.ListFindings(as("reader-1").(Context), FindingQuery{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"Decision:dec-in": true, "RelationEdge:" + f.readableEdge: true}
	if len(got) != 2 || !want[got[0].SubjectType+":"+got[0].SubjectID] || !want[got[1].SubjectType+":"+got[1].SubjectID] {
		t.Errorf("a domain-reader = %v, want dec-in and the readable edge", subjects(got))
	}
	for _, v := range got {
		if strings.Contains(v.Message, "secret") {
			t.Errorf("a domain-reader sees the message %q of a subject it cannot read", v.Message)
		}
		if v.SubjectID == "dec-in" && v.Address != "D-dec-in" {
			t.Errorf("address = %q, want D-dec-in", v.Address)
		}
		if v.FirstSeenAt.IsZero() || v.LastSeenAt.IsZero() {
			t.Errorf("times not set: %+v", v)
		}
	}
	// Filters, and the limit counts only what is shown.
	if got, err := f.app.ListFindings(as("reader-1").(Context), FindingQuery{Detector: "structural"}); err != nil || len(got) != 1 || got[0].SubjectID != f.readableEdge {
		t.Errorf("detector filter = %v %v", subjects(got), err)
	}
	if got, err := f.app.ListFindings(as("human-1", Human).(Context), FindingQuery{SubjectType: "Decision"}); err != nil || len(got) != 3 {
		t.Errorf("type filter = %v %v", subjects(got), err)
	}
	if got, err := f.app.ListFindings(as("reader-1").(Context), FindingQuery{Limit: 1}); err != nil || len(got) != 1 {
		t.Errorf("limit 1 = %v %v", subjects(got), err)
	}
	if got, err := f.app.ListFindings(as("human-1", Human).(Context), FindingQuery{Limit: 2}); err != nil || len(got) != 2 {
		t.Errorf("limit 2 for a global reader = %v %v", subjects(got), err)
	}
}

// TestListFindings_Errors: bounds, no store, a failing read.
func TestListFindings_Errors(t *testing.T) {
	f := newFindingsFixture(t)
	reader := as("reader-1").(Context)
	for _, limit := range []int{-1, 501} {
		if _, err := f.app.ListFindings(reader, FindingQuery{Limit: limit}); !errors.Is(err, ErrBadRequest) {
			t.Errorf("limit %d: %v", limit, err)
		}
	}
	if _, err := (&App{cfg: Config{DB: f.db}}).ListFindings(reader, FindingQuery{}); !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "not recorded") {
		t.Errorf("no store: %v", err)
	}
	failing := func(match string) *App {
		return &App{cfg: Config{DB: &awaitsFailDB{DB: f.db, match: match, err: errors.New("disk")}}, governance: f.app.governance, findingStore: f.app.findingStore}
	}
	if _, err := failing("FROM smeldr_findings WHERE").ListFindings(as("human-1", Human).(Context), FindingQuery{}); !errors.Is(err, ErrInternal) {
		t.Errorf("query fails: %v", err)
	}
	// An edge that cannot be read is left out for a narrow reader.
	got, err := failing("FROM smeldr_relations WHERE id").ListFindings(reader, FindingQuery{})
	if err != nil || len(got) != 1 || got[0].SubjectID != "dec-in" {
		t.Errorf("edge read fails = %v %v, want dec-in only", subjects(got), err)
	}
}
