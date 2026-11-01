package smeldr

import (
	"context"
	"errors"
	"testing"
	"time"
)

// hasRevMissingIn reports whether warnings carry rev_missing on rev.
func hasRevMissingIn(ws []CreateWarning) bool {
	for _, w := range ws {
		if w.Code == "rev_missing" && w.Field == "rev" {
			return true
		}
	}
	return false
}

// Re-asserting and withdrawing a governance relation name the edge's rev
// (T1010 part b); a first assert needs none; every write advances the rev.
func TestRevRequired_Relations(t *testing.T) {
	f := newPremiseFixture(t)
	f.placedDecision(t, "d", "ratified")
	f.placedDecision(t, "p", "proposed")
	ctx := as("human-1", Human)

	first, warnings, err := f.rels.MCPAssertRelationAt(ctx, "Decision", "d", "Decision", "p", "grounded_in", nil, nil, nil, nil, nil)
	if err != nil || hasRevMissingIn(warnings) {
		t.Fatalf("a first assert: %v, %v; want no warning", err, warnings)
	}
	_, warnings, err = f.rels.MCPAssertRelationAt(ctx, "Decision", "d", "Decision", "p", "grounded_in", nil, nil, nil, nil, nil)
	if err != nil || !hasRevMissingIn(warnings) {
		t.Fatalf("a re-assert without a rev: %v, %v; want rev_missing", err, warnings)
	}
	stale := first.Rev
	_, _, err = f.rels.MCPAssertRelationAt(ctx, "Decision", "d", "Decision", "p", "grounded_in", nil, nil, nil, nil, &stale)
	wantRevConflict(t, err, stale, first.Rev+1)
	current := first.Rev + 1
	again, _, err := f.rels.MCPAssertRelationAt(ctx, "Decision", "d", "Decision", "p", "grounded_in", nil, nil, nil, nil, &current)
	if err != nil || again.Rev != current+1 {
		t.Fatalf("a re-assert with the rev it read: %v, rev %d; want rev %d", err, again.Rev, current+1)
	}

	wrong := again.Rev - 1
	if _, err := f.rels.WithdrawAt(ctx, again.ID, "gone", &wrong); err == nil {
		t.Fatal("a withdraw with a stale rev must be refused")
	} else {
		wantRevConflict(t, err, wrong, again.Rev)
	}
	warnings, err = f.rels.WithdrawAt(ctx, again.ID, "gone", nil)
	if err != nil || !hasRevMissingIn(warnings) {
		t.Fatalf("a withdraw without a rev: %v, %v; want it done with rev_missing", err, warnings)
	}

	// After the cutover, a re-assert without a rev is refused.
	f.placedDecision(t, "q", "proposed")
	if _, _, err := f.rels.MCPAssertRelationAt(ctx, "Decision", "d", "Decision", "q", "grounded_in", nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	f.rels.revCutover = time.Now().Add(-time.Hour)
	var ve *ValidationError
	if _, _, err := f.rels.MCPAssertRelationAt(ctx, "Decision", "d", "Decision", "q", "grounded_in", nil, nil, nil, nil, nil); !errors.As(err, &ve) {
		t.Errorf("a re-assert without a rev after the cutover: %v; want a ValidationError", err)
	}

	if revRequiredKind("depends_on") || !revRequiredKind("belongs_to_area") || !revRequiredKind("supersedes") {
		t.Error("revRequiredKind: the closed list is belongs_to_*, grounded_in, contradicts, supersedes")
	}
}

// A change check answer names the Check's rev; a stale one records nothing.
func TestRevRequired_ChangeCheck(t *testing.T) {
	f, c := newApprovalCheck(t, both)
	var rev int
	if err := f.db.QueryRow(`SELECT rev FROM smeldr_change_checks WHERE id = $1`, c.ID).Scan(&rev); err != nil {
		t.Fatal(err)
	}
	stale := rev - 1
	_, err := f.app.AnswerChangeCheckAt(human("u-author"), c.Address, f.a1.Address, true, "", &stale)
	wantRevConflict(t, err, stale, rev)
	var verdict string
	if err := f.db.QueryRow(`SELECT verdict FROM smeldr_change_check_areas WHERE check_id = $1 AND area_id = $2`, c.ID, f.a1.ID).Scan(&verdict); err != nil || verdict != AreaVerdictAwaiting {
		t.Fatalf("verdict = %q, %v; the stale answer must record nothing", verdict, err)
	}
	warnings, err := f.app.AnswerChangeCheckAt(human("u-author"), c.Address, f.a1.Address, true, "", nil)
	if err != nil || !hasRevMissingIn(warnings) {
		t.Fatalf("an answer without a rev: %v, %v; want it recorded with rev_missing", err, warnings)
	}
	if err := f.db.QueryRow(`SELECT rev FROM smeldr_change_checks WHERE id = $1`, c.ID).Scan(&rev); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.AnswerChangeCheckAt(human("dapp"), c.Slug, f.a2.Slug, true, "", &rev); err != nil {
		t.Fatalf("an answer with the rev it read: %v", err)
	}
}

// A premise confirmation names the Decision's rev; two confirmations of one
// premise state cannot both win.
func TestRevRequired_Premise(t *testing.T) {
	f := newPremiseFixture(t)
	f.placedDecision(t, "d", "ratified")
	f.placedDecision(t, "p", "proposed")
	f.rest(t, "d", "grounded_in", "p")
	var rev int
	if err := f.db.QueryRow(`SELECT rev FROM smeldr_decisions WHERE id = 'd'`).Scan(&rev); err != nil {
		t.Fatal(err)
	}
	stale := rev + 3
	_, err := f.app.ConfirmPremiseAt(as("human-2", Human), "d", "p", "still holds", &stale)
	wantRevConflict(t, err, stale, rev)
	if open := f.premiseSignals(t, "d")["open"]; len(open) != 1 {
		t.Fatalf("the stale confirmation must leave the condition open: %v", f.premiseSignals(t, "d"))
	}
	warnings, err := f.app.ConfirmPremiseAt(as("human-2", Human), "d", "p", "still holds", nil)
	if err != nil || !hasRevMissingIn(warnings) {
		t.Fatalf("a confirmation without a rev: %v, %v; want it done with rev_missing", err, warnings)
	}

	// The state guard: the same premise state confirmed twice.
	err = f.app.confirmPremisePair(context.Background(), "d", "p", "proposed", "D-d", nil, "again")
	if !errors.Is(err, ErrConflict) {
		t.Errorf("a second confirmation of the same state: %v; want ErrConflict", err)
	}
}

// ConfirmPremiseConditionAt applies the same rule by the condition.
func TestRevRequired_PremiseCondition(t *testing.T) {
	f := newPremiseFixture(t)
	f.placedDecision(t, "d", "ratified")
	f.placedDecision(t, "p", "proposed")
	f.rest(t, "d", "grounded_in", "p")
	open := f.premiseSignals(t, "d")["open"]
	if len(open) != 1 {
		t.Fatalf("want one open condition, got %v", open)
	}
	id := open[0]
	if i := len(id); i > 0 {
		for j, ch := range id {
			if ch == '|' {
				id = id[:j]
				break
			}
		}
	}
	stale := 99
	_, err := f.app.ConfirmPremiseConditionAt(as("human-2", Human), id, "still holds", &stale)
	var rc *RevConflictError
	if !errors.As(err, &rc) {
		t.Fatalf("a stale rev by the condition: %v; want a RevConflictError", err)
	}
}

// A rev that is not a number is a validation error on an update.
func TestRevRequired_NonNumeric(t *testing.T) {
	m, store, d := decisionModuleAtStatus(t, "proposed")
	_, err := m.MCPUpdate(adminDecisionCtx(t, store), d.Slug, map[string]any{"Body": "x", "rev": "four"})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("a non-numeric rev: %v; want a ValidationError on rev", err)
	}
}

// After the cutover, each part-b write without a rev is refused, and with the
// right rev a premise confirmation goes through.
func TestRevRequired_PartBCutover(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	var ve *ValidationError

	f, c := newApprovalCheck(t, both)
	f.app.cfg.RevCutover = past
	if _, err := f.app.AnswerChangeCheckAt(human("u-author"), c.Address, f.a1.Address, true, "", nil); !errors.As(err, &ve) {
		t.Errorf("a change check answer without a rev after the cutover: %v; want a ValidationError", err)
	}

	pf := newPremiseFixture(t)
	pf.placedDecision(t, "d", "ratified")
	pf.placedDecision(t, "p", "proposed")
	pf.rest(t, "d", "grounded_in", "p")
	pf.app.cfg.RevCutover = past
	if _, err := pf.app.ConfirmPremiseAt(as("human-2", Human), "d", "p", "still holds", nil); !errors.As(err, &ve) {
		t.Errorf("a premise confirmation without a rev after the cutover: %v; want a ValidationError", err)
	}
	var rev int
	if err := pf.db.QueryRow(`SELECT rev FROM smeldr_decisions WHERE id = 'd'`).Scan(&rev); err != nil {
		t.Fatal(err)
	}
	if _, err := pf.app.ConfirmPremiseAt(as("human-2", Human), "d", "p", "still holds", &rev); err != nil {
		t.Errorf("with the rev it read: %v", err)
	}

	edge := pf.contradict(t, "d", "p")
	pf.rels.revCutover = past
	if _, err := pf.rels.WithdrawAt(as("human-1", Human), edge.ID, "gone", nil); !errors.As(err, &ve) {
		t.Errorf("a withdraw without a rev after the cutover: %v; want a ValidationError", err)
	}
}
