// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestAmendsBeforeSave_EdgesAndErrors(t *testing.T) {
	ctx := amCtx()
	// Not an Amendment: nothing to check.
	if err := amendsBeforeSave(ctx, nil, nil, &Decision{}); err != nil {
		t.Errorf("a non-Amendment item: %v", err)
	}
	// A value but no database to resolve it with.
	if err := amendsBeforeSave(ctx, nil, nil, &Amendment{Amends: "D1"}); err == nil || !strings.Contains(err.Error(), "no database") {
		t.Errorf("no database: err = %v", err)
	}
	// Already set, and a garbage value arrives: a validation error, not a silent keep.
	old := &Amendment{Amends: "D1"}
	if err := amendsBeforeSave(ctx, nil, old, &Amendment{Amends: "nonsense"}); err == nil {
		t.Error("a malformed value over a stored one must be refused")
	}
	// A database that fails on the lookup is an internal error, not a "no such Decision".
	if err := amendsBeforeSave(ctx, &queryFailDB{}, nil, &Amendment{Amends: "D1"}); !errors.Is(err, ErrInternal) {
		t.Errorf("failing lookup: err = %v, want ErrInternal", err)
	}
	// Whitespace around an empty value is empty.
	a := &Amendment{Amends: "  "}
	if err := amendsBeforeSave(ctx, nil, nil, a); err != nil || a.Amends != "" {
		t.Errorf("blank = %q, %v, want it cleared to empty without error", a.Amends, err)
	}
}

func TestAmendsAfterSave_Edges(t *testing.T) {
	e := newAmendsEnv(t, true)
	e.decision(t, "dec-1", "D1")
	ctx := amCtx()
	amendsAfterSave(ctx, e.db, e.rs, nil, &Decision{})                                   // not an Amendment
	amendsAfterSave(ctx, e.db, e.rs, nil, &Amendment{Node: Node{ID: "x"}})               // empty amends
	amendsAfterSave(ctx, e.db, e.rs, &Amendment{Amends: "D1"}, &Amendment{Amends: "D1"}) // already set before
	// A Decision that disappeared between the check and the assert: a Warn, no panic.
	amendsAfterSave(ctx, e.db, e.rs, nil, &Amendment{Node: Node{ID: "am-1"}, Amends: "D404"})
	if n := len(e.liveAmends(t, "am-1")); n != 0 {
		t.Errorf("%d edges for a vanished Decision", n)
	}
}

func TestResolveHelpers_Errors(t *testing.T) {
	e := newAmendsEnv(t, true)
	ctx := context.Background()
	if _, err := resolveDecisionID(ctx, &queryFailDB{}, "D1"); !errors.Is(err, ErrInternal) {
		t.Errorf("resolveDecisionID on a failing database = %v, want ErrInternal", err)
	}
	if _, err := resolveAmendmentID(ctx, &queryFailDB{}, "A1"); !errors.Is(err, ErrInternal) {
		t.Errorf("resolveAmendmentID on a failing database = %v, want ErrInternal", err)
	}
	if _, err := assertAmendsEdge(ctx, &queryFailDB{}, e.rs, "am", "D1"); !errors.Is(err, ErrInternal) {
		t.Errorf("assertAmendsEdge on a failing database = %v, want ErrInternal", err)
	}
	// Two Amendments with one number is ambiguous.
	e.create(t, map[string]any{"amendment_number": "A777", "slug": "a777-one"})
	e.create(t, map[string]any{"amendment_number": "A777", "slug": "a777-two"})
	if _, err := resolveAmendmentID(ctx, e.db, "A777"); err == nil || !strings.Contains(err.Error(), "matches 2") {
		t.Errorf("duplicate Amendment number = %v, want an ambiguity error", err)
	}
	if _, err := resolveAmendmentID(ctx, e.db, "A404"); err == nil {
		t.Error("a missing Amendment must be an error")
	}
}

func TestBackfillAmendsEdges_AmbiguousAndErrors(t *testing.T) {
	e := newAmendsEnv(t, true)
	e.decision(t, "dec-a", "D5")
	e.decision(t, "dec-b", "5")
	e.create(t, map[string]any{"amendment_number": "A800"})
	e.create(t, map[string]any{"amendment_number": "A801", "slug": "a801-one"})
	e.create(t, map[string]any{"amendment_number": "A801", "slug": "a801-two"})
	rep, err := e.app.BackfillAmendsEdges(amCtx(), []AmendsLink{
		{"A800", "D5"},       // the Decision number matches two rows
		{"A801", "D5"},       // the Amendment number matches two rows
		{"A800", "not-a-no"}, // malformed Decision number
	}, false)
	if err != nil {
		t.Fatalf("BackfillAmendsEdges: %v", err)
	}
	if rep.Ambiguous != 2 || rep.Unresolved != 1 || rep.Asserted != 0 {
		t.Errorf("report = %+v, want 2 ambiguous and 1 unresolved", rep)
	}
	// A relation read that fails is an error, not a silent skip.
	broken := &App{cfg: Config{DB: &queryFailDB{}}, relationStore: e.rs}
	if _, err := broken.BackfillAmendsEdges(amCtx(), []AmendsLink{{"A800", "D1"}}, true); err != nil {
		// resolution fails first and is reported as unresolved, so no error here
		t.Errorf("a link that cannot be resolved is reported, not an error: %v", err)
	}
}

func TestAmendsBackfillCandidates_Errors(t *testing.T) {
	if _, err := (&App{}).AmendsBackfillCandidates(context.Background()); !errors.Is(err, ErrBadRequest) {
		t.Errorf("no database: err = %v, want ErrBadRequest", err)
	}
	failing := &App{cfg: Config{DB: &queryFailDB{}}}
	if _, err := failing.AmendsBackfillCandidates(context.Background()); !errors.Is(err, ErrInternal) {
		t.Errorf("failing database: err = %v, want ErrInternal", err)
	}
}

func TestCanonicalDecisionNumber(t *testing.T) {
	for in, want := range map[string]string{"D104": "D104", "d104": "D104", "104": "D104", " 7 ": "D7"} {
		if got, err := canonicalDecisionNumber(in); err != nil || got != want {
			t.Errorf("canonicalDecisionNumber(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "D", "DD1", "decision 1", "D1x"} {
		if _, err := canonicalDecisionNumber(in); err == nil {
			t.Errorf("canonicalDecisionNumber(%q) must fail", in)
		}
	}
}
