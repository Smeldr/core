package smeldr

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestAutomatedRefusal_KeepsItsReason (T1236): an agent or a job that tries
// a supersede (either form) or a repair is refused with ratify.automated_class
// as a RefusalError, so MCP and the cli show its reason and values, as they do
// for retire. The reason names no place; errors.Is(err, ErrForbidden) holds.
func TestAutomatedRefusal_KeepsItsReason(t *testing.T) {
	f := newSupersedeFixture(t)
	if _, err := f.rs.Grant(context.Background(), RoleGrant{TokenID: "job-1", RoleName: "editor"}); err != nil {
		t.Fatal(err)
	}
	f.placedDecision(t, "old", "ratified")
	f.placedDecision(t, "new", "ratified")
	f.placedDecision(t, "lost", "superseded")
	for _, actor := range []struct {
		ctx   context.Context
		class string
		token string
	}{
		{as("agent-1", Agent), "agent", "an agent token"},
		{as("job-1", Job), "job", "a job token"},
	} {
		for _, c := range []struct {
			name string
			code string
			call func(ctx context.Context) error
		}{
			{"supersede by replacement", "supersede", func(ctx context.Context) error {
				_, err := f.app.SupersedeDecision(ctx, SupersedeRequest{Decision: "old", Replacement: "new", Reason: "r"})
				return err
			}},
			{"supersede by a new Decision", "supersede", func(ctx context.Context) error {
				_, err := f.app.SupersedeDecision(ctx, SupersedeRequest{Decision: "old", Reason: "r", New: &NewReplacement{Title: "t", Body: "b"}})
				return err
			}},
			{"repair", "repair", func(ctx context.Context) error {
				_, err := f.app.RepairSuperseded(ctx, RepairRequest{Decision: "lost", Replacement: "new", Reason: "r"})
				return err
			}},
		} {
			t.Run(actor.class+" "+c.name, func(t *testing.T) {
				err := c.call(actor.ctx)
				var re *RefusalError
				if !errors.As(err, &re) || !errors.Is(err, ErrForbidden) || ProblemTypeOf(err) != problemRatifyAutomatedClass {
					t.Fatalf("err = %v (%s); want a ratify.automated_class RefusalError", err, ProblemTypeOf(err))
				}
				want := actor.token + " never ratifies, so it cannot " + c.code + "; a person with ratify over the place does"
				if re.Reason != want {
					t.Errorf("reason = %q, want %q", re.Reason, want)
				}
				v := ProblemValuesOf(err)
				if v["token_class"] != actor.class || v["act"] != c.code || v["place"] != "" {
					t.Errorf("values = %v", v)
				}
				for _, place := range []string{"are-1", "dom-1", "ARE", "DOM"} {
					if strings.Contains(re.Reason, place) {
						t.Errorf("the reason names a place (%q): %q", place, re.Reason)
					}
				}
			})
		}
	}
	if f.status(t, "old") != "ratified" || f.status(t, "lost") != "superseded" {
		t.Error("a refused act moved a Decision")
	}
}
