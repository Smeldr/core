package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// newSignal inserts a Signal of signalType as the system would (a condition)
// or a caller (anything else) and returns its ID.
func newSignal(t *testing.T, db *sql.DB, signalType string) string {
	t.Helper()
	s := &Signal{Sender: "core", Receiver: "architect", SignalType: signalType, Message: "m"}
	if err := InsertSignal(withSignalSystem(context.Background()), db, s); err != nil {
		t.Fatalf("InsertSignal(%s): %v", signalType, err)
	}
	return s.ID
}

// Each kind takes only its own edges, on transition_item's path.
func TestSignalFlow_KindRules(t *testing.T) {
	app, db := signalTestApp(t)
	ctx := context.Background()
	q := newSignal(t, db, "question")
	c := newSignal(t, db, "conflict-detected")

	if _, err := app.TransitionItemVia(ctx, "mcp", "Signal", q, "answered", ""); !errors.Is(err, ErrBadRequest) {
		t.Errorf("answer without a reason: %v, want ErrBadRequest", err)
	}
	if _, err := app.TransitionItemVia(ctx, "mcp", "Signal", q, "resolved", ""); !errors.Is(err, ErrConflict) {
		t.Errorf("request resolved: %v, want ErrConflict", err)
	}
	if _, err := app.TransitionItemVia(ctx, "mcp", "Signal", c, "answered", "done"); !errors.Is(err, ErrConflict) {
		t.Errorf("condition answered: %v, want ErrConflict", err)
	}
	if _, err := app.TransitionItemVia(ctx, "mcp", "Signal", c, "withdrawn", "done"); !errors.Is(err, ErrConflict) {
		t.Errorf("condition withdrawn: %v, want ErrConflict", err)
	}
	if _, err := app.TransitionItemVia(ctx, "mcp", "Signal", c, "resolved", ""); !errors.Is(err, ErrForbidden) {
		t.Errorf("condition resolved by a caller: %v, want ErrForbidden", err)
	}
	if _, err := app.TransitionItemVia(withSignalSystem(ctx), "", "Signal", c, "resolved", ""); err != nil {
		t.Errorf("condition resolved by the system: %v", err)
	}
	if _, err := app.TransitionItemVia(ctx, "mcp", "Signal", q, "answered", "because"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if status, _, _, answer, _ := signalRow(t, db, q); status != "answered" || answer != "because" {
		t.Errorf("after transition_item: %s %q, want answered with the reason as answer", status, answer)
	}
	if _, err := app.TransitionItemVia(ctx, "mcp", "Signal", q, "open", ""); !errors.Is(err, ErrConflict) {
		t.Errorf("out of answered: %v, want ErrConflict", err)
	}
}

// History types keep pending to read to acknowledged, and nothing leaves a
// terminal history state.
func TestSignalFlow_History(t *testing.T) {
	app, db := signalTestApp(t)
	ctx := context.Background()
	h := newSignal(t, db, "plan-ready")
	for _, to := range []string{"read", "acknowledged"} {
		if _, err := app.TransitionItemVia(ctx, "mcp", "Signal", h, to, ""); err != nil {
			t.Fatalf("history to %s: %v", to, err)
		}
	}
	if _, err := app.TransitionItemVia(ctx, "mcp", "Signal", h, "expired", ""); !errors.Is(err, ErrConflict) {
		t.Errorf("acknowledged to expired: %v, want ErrConflict", err)
	}
	p := newSignal(t, db, "notice")
	if _, err := app.TransitionItemVia(ctx, "mcp", "Signal", p, "expired", ""); !errors.Is(err, ErrConflict) {
		t.Errorf("pending to expired: %v, want ErrConflict (nothing expires)", err)
	}
}

// validateSignalTransition answers a missing row and a store failure.
func TestValidateSignalTransition_Errors(t *testing.T) {
	_, db := signalTestApp(t)
	ctx := context.Background()
	if err := validateSignalTransition(ctx, db, "missing", "open", "answered"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing: %v, want ErrNotFound", err)
	}
	failing := &awaitsFailDB{DB: db, match: "SELECT signal_type FROM smeldr_signals", err: errors.New("disk")}
	if err := validateSignalTransition(ctx, failing, "x", "open", "answered"); !errors.Is(err, ErrInternal) {
		t.Errorf("store failure: %v, want ErrInternal", err)
	}
	if err := validateSignalTransition(ctx, failing, "x", "pending", "read"); err != nil {
		t.Errorf("history move: %v, want nil (the flow decides)", err)
	}
}

func TestAnswerSignal(t *testing.T) {
	app, db := signalTestApp(t)
	ctx := context.Background()
	prov := &fakeProvenanceStore{}
	app.Provenance(prov)
	q := newSignal(t, db, "question")
	if err := app.AnswerSignal(ctx, q, "yes, ship it", "Task:01abc"); err != nil {
		t.Fatalf("AnswerSignal: %v", err)
	}
	status, _, _, answer, ref := signalRow(t, db, q)
	if status != "answered" || answer != "yes, ship it" || ref != "Task:01abc" {
		t.Errorf("row = %s %q %q", status, answer, ref)
	}
	var found bool
	for _, r := range prov.Appended() {
		if r.SubjectID == q && r.ToState == "answered" && r.Reason == "yes, ship it" {
			found = true
		}
	}
	if !found {
		t.Errorf("no provenance record carries the answer: %+v", prov.Appended())
	}

	c := newSignal(t, db, "authorization-required")
	cases := []struct {
		name, ident, text, ref string
		want                   error
		field                  string
	}{
		{"empty answer", q, " ", "", nil, "answer"},
		{"bad ref", q, "a", "no-colon", nil, "answer_ref"},
		{"bad ref with space", q, "a", "Task: x", nil, "answer_ref"},
		{"missing", "nope", "a", "", ErrNotFound, ""},
		{"already answered", q, "a", "", ErrConflict, ""},
		{"a condition", c, "a", "", ErrConflict, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := app.AnswerSignal(ctx, tc.ident, tc.text, tc.ref)
			if tc.field != "" {
				wantValidation(t, err, tc.field)
				return
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestWithdrawSignal(t *testing.T) {
	app, db := signalTestApp(t)
	r := newSignal(t, db, "review-requested")
	if err := app.WithdrawSignal(editorCtx(), r, "no longer needed"); err != nil {
		t.Fatalf("WithdrawSignal: %v", err)
	}
	if status, _, _, answer, _ := signalRow(t, db, r); status != "withdrawn" || answer != "no longer needed" {
		t.Errorf("row = %s %q", status, answer)
	}
	wantValidation(t, app.WithdrawSignal(editorCtx(), r, ""), "reason")
	// A caller below the capability's floor (update needs Author) may not.
	guest := NewTestContext(User{ID: "g"})
	q := newSignal(t, db, "question")
	if err := app.AnswerSignal(guest, q, "a", ""); !errors.Is(err, ErrForbidden) {
		t.Errorf("guest answer: %v, want ErrForbidden", err)
	}
	if err := (&App{}).AnswerSignal(context.Background(), q, "a", ""); !errors.Is(err, ErrInternal) {
		t.Errorf("no DB: %v, want ErrInternal", err)
	}
}

// Under governance a caller with no [Context] has no identity to check the
// capability against.
func TestAnswerSignal_GovernanceNeedsACaller(t *testing.T) {
	app, db, _ := setupTransitionItemApp(t)
	q := newSignal(t, db, "question")
	err := app.AnswerSignal(TrustedRead(context.Background()), q, "a", "")
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden", err)
	}
	if signalStatusOf(t, db, q) != "open" {
		t.Error("the request moved")
	}
}

// A failed answer write after the committed transition is logged, not
// returned; a failed index is an error of the column ensure.
func TestSignalStoreFailures(t *testing.T) {
	app, db := signalTestApp(t)
	ctx := context.Background()
	q := newSignal(t, db, "question")
	app.cfg.DB = failAddressDB{db, "SET answer = $1, answer_ref"}
	if err := app.AnswerSignal(ctx, q, "a", ""); err != nil {
		t.Errorf("AnswerSignal: %v, want nil (the transition committed)", err)
	}
	if signalStatusOf(t, db, q) != "answered" {
		t.Error("the transition did not commit")
	}
	if err := EnsureOrchestrationSignalColumns(ctx, failAddressDB{db, "idx_smeldr_signals_open_subject"}); err == nil {
		t.Error("a failed index was not reported")
	}
	if err := EnsureOrchestrationSignalColumns(ctx, db); err != nil {
		t.Errorf("EnsureOrchestrationSignalColumns: %v", err)
	}
}
