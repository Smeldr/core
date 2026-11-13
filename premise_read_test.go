package smeldr

import (
	"context"
	"errors"
	"testing"
)

// T1183: the premise read with its confirmation (D149).

// TestPremiseFlags: the pairs of a resting Decision, the open condition, the
// premise only to a reader who may read it, and the confirmation named by
// the holder's newest live token (the fixture's fp-human-2).
func TestPremiseFlags(t *testing.T) {
	f := newPremiseFixture(t)
	ctx := context.Background()
	f.placedDecision(t, "d", "ratified")
	f.placedDecision(t, "p", "proposed") // placed, readable to the reader
	f.decision(t, "q", "proposed")       // unplaced: the domain-reader cannot read it
	f.rest(t, "d", "grounded_in", "p")
	f.rest(t, "d", "grounded_in", "q")
	if err := RegisterDomainReaderRole(ctx, f.rs); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rs.Grant(ctx, RoleGrant{TokenID: "reader-9", RoleName: "domain-reader", ScopeAnchorID: f.domain}); err != nil {
		t.Fatal(err)
	}
	if err := f.app.ConfirmPremise(as("human-2", Human), "d", "p", "the draft is what we follow"); err != nil {
		t.Fatal(err)
	}

	// An admin reads both premises.
	got, err := f.app.PremiseFlags(as("human-1", Human, Admin), "d")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("pairs = %+v", got)
	}
	p, q := got[0], got[1]
	if p.Premise == nil || p.Premise.Address != "D-p" || p.Premise.State != "proposed" || !p.Premise.Placed {
		t.Errorf("p premise = %+v", p.Premise)
	}
	if p.Signal != "" || p.ConditionOpen {
		t.Errorf("p is confirmed, its condition resolved, but Signal = %q open %v", p.Signal, p.ConditionOpen)
	}
	if c := p.Confirmation; c == nil || c.By != "fp-human-2" || c.Class != "human" || c.Reason != "the draft is what we follow" || c.PremiseState != "proposed" || c.At.IsZero() {
		t.Errorf("p confirmation = %+v", c)
	}
	if q.Signal == "" || !q.ConditionOpen || q.Confirmation != nil || q.Premise == nil || q.Premise.Placed {
		t.Errorf("q = %+v", q)
	}

	// The domain-reader reads d and p, not q: q's pair is listed without its
	// premise. It reads q's open condition, which is placed where d is
	// (premise_flags.go), so the condition is named.
	got, err = f.app.PremiseFlags(as("reader-9"), "d")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Premise == nil || got[1].Premise != nil || !got[1].ConditionOpen || got[1].Signal == "" {
		t.Errorf("reader = %+v", got)
	}

	// A reader granted d alone does not read the condition: it is reported
	// open, without its address (D181 point 3). Granted the Signal as well,
	// the same reader gets the address.
	if err := RegisterScopedReaderRole(ctx, f.rs); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rs.Grant(ctx, RoleGrant{TokenID: "only-d", RoleName: "scoped-reader", ScopeStatic: []string{"Decision:d"}}); err != nil {
		t.Fatal(err)
	}
	got, err = f.app.PremiseFlags(as("only-d"), "d")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Premise != nil || got[1].Premise != nil || !got[1].ConditionOpen || got[1].Signal != "" {
		t.Errorf("reader of d alone = %+v", got)
	}
	open, err := f.app.signalResolver().selectOpen(ctx, "signal_type = $1", "premise-flagged")
	if err != nil || len(open) != 1 {
		t.Fatalf("open premise conditions = %+v %v", open, err)
	}
	if _, err := f.rs.Grant(ctx, RoleGrant{TokenID: "only-d", RoleName: "scoped-reader", ScopeStatic: []string{"Signal:" + open[0].id}}); err != nil {
		t.Fatal(err)
	}
	got, err = f.app.PremiseFlags(as("only-d"), "d")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Signal != open[0].address || !got[1].ConditionOpen {
		t.Errorf("reader of d and the Signal = %+v", got)
	}
}

// TestPremiseFlags_Refusals: an empty name, an unknown or unreadable
// Decision, no database.
func TestPremiseFlags_Refusals(t *testing.T) {
	f := newPremiseFixture(t)
	f.decision(t, "hidden", "ratified") // unplaced: the reader cannot read it
	if err := RegisterDomainReaderRole(context.Background(), f.rs); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rs.Grant(context.Background(), RoleGrant{TokenID: "reader-9", RoleName: "domain-reader", ScopeAnchorID: f.domain}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.PremiseFlags(as("reader-9"), "  "); !errors.Is(err, ErrBadRequest) || ProblemTypeOf(err) != problemPremiseDecisionRequired {
		t.Errorf("empty: %v", err)
	}
	for _, ref := range []string{"hidden", "nope"} {
		_, err := f.app.PremiseFlags(as("reader-9"), ref)
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v, want not found", ref, err)
		}
	}
	if got, err := f.app.PremiseFlags(as("human-1", Human, Admin), "hidden"); err != nil || len(got) != 0 {
		t.Errorf("a Decision with no pairs: %+v %v", got, err)
	}
	if _, err := (&App{}).PremiseFlags(context.Background(), "d"); !errors.Is(err, ErrInternal) {
		t.Errorf("no database: %v", err)
	}
}

// TestPremiseFlags_ReadFailures: an unreadable confirmation time and a
// failed read of the confirmers' tokens are ErrInternal.
func TestPremiseFlags_ReadFailures(t *testing.T) {
	f := newPremiseFixture(t)
	f.placedDecision(t, "d", "ratified")
	f.placedDecision(t, "p", "proposed")
	f.rest(t, "d", "grounded_in", "p")
	if err := f.app.ConfirmPremise(as("human-2", Human), "d", "p", "holds"); err != nil {
		t.Fatal(err)
	}
	admin := as("human-1", Human, Admin)
	if _, err := f.app.cfg.DB.ExecContext(context.Background(), `UPDATE smeldr_premise_flags SET confirmed_at = 'not a time'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.PremiseFlags(admin, "d"); !errors.Is(err, ErrInternal) {
		t.Errorf("unreadable time: %v, want ErrInternal", err)
	}
	if _, err := f.app.cfg.DB.ExecContext(context.Background(), `DROP TABLE smeldr_tokens`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.PremiseFlags(admin, "d"); !errors.Is(err, ErrInternal) {
		t.Errorf("no tokens table: %v, want ErrInternal", err)
	}
}
