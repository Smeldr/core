package smeldr

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// T1061 phase 2, group C: each refusal type of the change checks, premise
// confirmations, Signal kinds and the rev rule, driven through a real site.

// wantTypedC asserts err's refusal type and exactly its values.
func wantTypedC(t *testing.T, err error, typ ProblemType, values map[string]string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want a %s refusal, got nil", typ)
	}
	if got := ProblemTypeOf(err); got != typ {
		t.Errorf("type = %q, want %q (err %v)", got, typ, err)
	}
	if got := ProblemValuesOf(err); !reflect.DeepEqual(got, values) {
		t.Errorf("values = %#v, want %#v", got, values)
	}
}

// wantSentinelC asserts err is sentinel and reads exactly msg.
func wantSentinelC(t *testing.T, err, sentinel error, msg string) {
	t.Helper()
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want %v", err, sentinel)
	}
	if err != nil && err.Error() != msg {
		t.Errorf("message = %q, want %q", err.Error(), msg)
	}
}

// wantFieldC asserts err is a ValidationError on field with message msg.
func wantFieldC(t *testing.T, err error, field, msg string) {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want a ValidationError", err)
	}
	fs := ve.Fields()
	if len(fs) != 1 || fs[0].Field != field || fs[0].Message != msg {
		t.Errorf("fields = %+v, want %s: %s", fs, field, msg)
	}
	if want := "validation failed: " + field + ": " + msg; err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}

func TestPhaseTwoC_CheckChangeInput(t *testing.T) {
	f := newChangeCheckFixture(t)
	many := make([]string, DecisionsForPathsMaxPaths+1)
	for i := range many {
		many[i] = "a.go"
	}
	_, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 1, HeadCommit: headA, Paths: many})
	wantTypedC(t, err, problemLimitPathsPerCall, map[string]string{"limit": "3000"})
	wantSentinelC(t, err, ErrBadRequest, ErrBadRequest.Error()+": at most 3000 paths per check (the most the host lists); give the first 3000 and files_total")

	_, err = f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 1, HeadCommit: headA, Paths: []string{"a", "b"}, FilesTotal: 1})
	wantTypedC(t, err, problemFieldOutOfRange, map[string]string{"value": "1", "min": "2"})
	wantFieldC(t, err, "files_total", "is 1, fewer than the 2 paths given")

	_, err = f.app.CheckChange(context.Background(), ChangeInput{Repository: "acme/app", Number: 1, HeadCommit: headA, Paths: []string{"internal/billing/a.go"}})
	wantTypedC(t, err, problemChangeCheckNoCaller, nil)
	wantSentinelC(t, err, ErrForbidden, ErrForbidden.Error()+": check_change needs a caller")

	_, err = f.app.ListChangeChecks(f.ops, ChangeCheckQuery{Number: 70})
	wantTypedC(t, err, problemChangeCheckFilterRequired, nil)
	wantSentinelC(t, err, ErrBadRequest, ErrBadRequest.Error()+": give a repository, an Area or a Decision: Checks are read by filter, never as one list")
}

func TestPhaseTwoC_ChangeCheckWrites(t *testing.T) {
	f := newChangeCheckFixture(t)
	res, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 11, HeadCommit: headA, Paths: []string{"internal/billing/a.go"}})
	if err != nil {
		t.Fatal(err)
	}
	m := findModule[*ChangeCheck](t, f.app)
	_, err = m.MCPCreate(f.ops, map[string]any{"repository": "x/y"})
	wantTypedC(t, err, problemChangeCheckCoreOnly, nil)
	wantSentinelC(t, err, ErrForbidden, ErrForbidden.Error()+": a ChangeCheck is made only by check_change (App.CheckChange) and moved only by core")

	err = m.MCPDelete(f.ops, res.Check.Slug)
	wantTypedC(t, err, problemChangeCheckNotDeletable, nil)
	wantSentinelC(t, err, ErrForbidden, ErrForbidden.Error()+": a ChangeCheck is not deleted; how long Checks are kept is not decided yet")
}

func TestPhaseTwoC_SupersedeRace(t *testing.T) {
	f := newChangeCheckFixture(t)
	res, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 12, HeadCommit: headA, Paths: []string{"internal/billing/a.go"}})
	if err != nil {
		t.Fatal(err)
	}
	// Every move to superseded is skipped, as if another call kept moving it.
	if _, err := f.db.Exec(`CREATE TRIGGER t BEFORE UPDATE ON smeldr_change_checks WHEN NEW.status = 'superseded' BEGIN SELECT RAISE(IGNORE); END`); err != nil {
		t.Fatal(err)
	}
	c := res.Check
	_, err = f.app.claimSupersede(f.ops, c.ID, c.Address, "x", "CHK99")
	wantTypedC(t, err, problemChangeCheckSupersedeRace, map[string]string{"check": c.Address})
	wantSentinelC(t, err, ErrConflict, ErrConflict.Error()+": "+c.Address+" kept changing while it was superseded")
}

func TestPhaseTwoC_ItemApproverGrant(t *testing.T) {
	f, c := newApprovalCheck(t, both)
	_, err := f.rs.Grant(human("u-author"), RoleGrant{TokenID: "bot2", RoleName: RoleItemChangeApprover, ScopeStatic: []string{"Decision:" + f.dArea}})
	wantTypedC(t, err, problemGrantItemApproverScope, map[string]string{"role": RoleItemChangeApprover})
	wantSentinelC(t, err, ErrForbidden, ErrForbidden.Error()+": Grant: item-change-approver names exactly one ChangeCheck (ChangeCheck:<id>)")

	_, err = f.rs.Grant(agent("bot"), RoleGrant{TokenID: "bot2", RoleName: RoleItemChangeApprover, ScopeStatic: []string{"ChangeCheck:" + c.ID}})
	wantTypedC(t, err, problemGrantItemApproverNeedsHumanGrantor, map[string]string{"role": RoleItemChangeApprover, "grantor_class": "agent"})
	wantSentinelC(t, err, ErrForbidden, ErrForbidden.Error()+": Grant: item-change-approver must come from a human token; the grantor is agent")

	h := newChangeCheckFixture(t)
	if err := RegisterItemChangeApproverRole(context.Background(), h.rs); err != nil {
		t.Fatal(err)
	}
	res, err := h.app.CheckChange(h.ops, ChangeInput{Repository: "acme/app", Number: 50, HeadCommit: headA, Paths: approvalPaths})
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.rs.Grant(human("u-author"), RoleGrant{TokenID: "bot5", RoleName: RoleItemChangeApprover, ScopeStatic: []string{"ChangeCheck:" + res.Check.ID}})
	wantTypedC(t, err, problemGrantCheckHasNoApprovalArea, nil)
	wantSentinelC(t, err, ErrForbidden, ErrForbidden.Error()+": Grant: the Check has no Area that requires approval; there is nothing to delegate")
}

func TestPhaseTwoC_AnswerChangeCheck(t *testing.T) {
	f, c := newApprovalCheck(t, both)

	err := f.app.AnswerChangeCheck(human("ops-1"), c.Address, f.a2.Address, true, "")
	wantTypedC(t, err, problemChangeCheckAnswerAuthority, map[string]string{"area": f.a2.Address, "role": RoleItemChangeApprover})
	wantSentinelC(t, err, ErrForbidden, ErrForbidden.Error()+": answering for Area "+f.a2.Address+
		" needs approve-change over it (its area-approver, its domain-approver or an approver) or item-change-approver on the Check")

	other, err := f.areas.MCPCreate(TrustedRead(authorCtx()).(Context), map[string]any{"name": "Elsewhere"})
	if err != nil {
		t.Fatal(err)
	}
	otherAddress := asItem[*Area](other).Address
	err = f.app.AnswerChangeCheck(human("u-author"), c.Address, otherAddress, true, "")
	wantTypedC(t, err, problemChangeCheckAreaNotTouched, map[string]string{"check": c.Address, "area": otherAddress})
	wantSentinelC(t, err, ErrConflict, ErrConflict.Error()+": "+c.Address+" does not touch Area "+otherAddress)

	// The generic answer is refused on a Check's approval request.
	req := f.requests(t, c.ID)[f.a1.Address]
	err = f.app.AnswerSignal(human("u-author"), req[0], "yes", "")
	wantTypedC(t, err, problemChangeCheckUseAnswerChangeCheck, nil)
	wantSentinelC(t, err, ErrConflict, ErrConflict.Error()+": this request is a Check's approval for one Area: answer it with answer_change_check (App.AnswerChangeCheck), which checks the answerer's authority over that Area")

	if err := f.app.AnswerChangeCheck(human("u-author"), c.Address, f.a1.Address, true, ""); err != nil {
		t.Fatal(err)
	}
	err = f.app.AnswerChangeCheck(human("u-author"), c.Address, f.a1.Address, true, "")
	wantTypedC(t, err, problemChangeCheckAlreadyAnswered, map[string]string{"area": f.a1.Address, "check": c.Address, "verdict": AreaVerdictApproved})
	wantSentinelC(t, err, ErrConflict, ErrConflict.Error()+": Area "+f.a1.Address+" already answered "+c.Address+" (approved)")

	// Superseded by a newer commit: no longer awaiting approval.
	if _, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 30, HeadCommit: headB, Paths: approvalPaths}); err != nil {
		t.Fatal(err)
	}
	err = f.app.AnswerChangeCheck(human("u-author"), c.Address, f.a2.Address, true, "")
	wantTypedC(t, err, problemChangeCheckNotAwaiting, map[string]string{"check": c.Address, "state": ChangeCheckSuperseded})
	wantSentinelC(t, err, ErrConflict, ErrConflict.Error()+": "+c.Address+" is superseded, not awaiting approval")

	g, d := newApprovalCheck(t, second)
	err = g.app.AnswerChangeCheck(human("u-author"), d.Address, g.a1.Address, true, "")
	wantTypedC(t, err, problemChangeCheckAreaCommentOnly, map[string]string{"area": g.a1.Address, "check": d.Address, "approval_mode": ApprovalModeCommentOnly})
	wantSentinelC(t, err, ErrConflict, ErrConflict.Error()+": Area "+g.a1.Address+" only comments on "+d.Address+" (comment-only); there is nothing to approve")
}

func TestPhaseTwoC_AnswerRace(t *testing.T) {
	f, c := newApprovalCheck(t, both)
	if _, err := f.db.Exec(`CREATE TRIGGER t BEFORE UPDATE ON smeldr_change_check_areas WHEN NEW.verdict = 'approved' BEGIN SELECT RAISE(IGNORE); END`); err != nil {
		t.Fatal(err)
	}
	err := f.app.AnswerChangeCheck(human("u-author"), c.Address, f.a1.Address, true, "")
	wantTypedC(t, err, problemChangeCheckAnswerRace, map[string]string{"area": f.a1.Address, "check": c.Address})
	wantSentinelC(t, err, ErrConflict, ErrConflict.Error()+": Area "+f.a1.Address+" of "+c.Address+" was answered at the same moment")
}

func TestPhaseTwoC_AuditSuperseded(t *testing.T) {
	f, c := newAuditCheck(t, "")
	if _, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 30, HeadCommit: headB, Paths: approvalPaths}); err != nil {
		t.Fatal(err)
	}
	err := f.app.AnswerChangeCheck(human("u-author"), c.Address, f.a1.Address, true, "")
	wantTypedC(t, err, problemChangeCheckSuperseded, map[string]string{"check": c.Address})
	wantSentinelC(t, err, ErrConflict, ErrConflict.Error()+": "+c.Address+" is superseded; answer for the change's live Check")
}

// The address rule: an Area the caller may not read is named back as the
// caller sent it, never as its resolved address.
func TestPhaseTwoC_AreaNotReadable(t *testing.T) {
	f, c := newApprovalCheck(t, both)
	reader := NewTestContext(User{ID: "reader-a", Roles: []Role{Author, Human}})
	for _, sent := range []string{f.a2.ID, f.a2.Slug, "ARE999"} {
		err := f.app.AnswerChangeCheck(reader, c.Address, sent, true, "")
		wantTypedC(t, err, problemPlaceNotFound, map[string]string{"area": sent})
		wantSentinelC(t, err, ErrNotFound, ErrNotFound.Error()+": no Area \""+sent+"\" you can read")
		if v := ProblemValuesOf(err)["area"]; sent != f.a2.Address && v == f.a2.Address {
			t.Errorf("value names the hidden Area's address %q", v)
		}
	}
}

func TestPhaseTwoC_Premise(t *testing.T) {
	f := newPremiseFixture(t)
	f.placedDecision(t, "d", "ratified")
	f.placedDecision(t, "p", "proposed")
	f.placedDecision(t, "other", "proposed")
	f.rest(t, "d", "grounded_in", "p")
	sigID, _ := f.openPremiseSignal(t, "d")

	err := f.app.ConfirmPremise(as("human-2", Human), "d", "other", "still right")
	wantTypedC(t, err, problemPremiseNoOpenFlag, map[string]string{"decision": "D-d"})
	wantSentinelC(t, err, ErrConflict, ErrConflict.Error()+": D-d has no open premise-flagged condition for that premise")

	err = f.app.ConfirmPremiseCondition(as("human-2", Human), "S99999", "why")
	wantTypedC(t, err, problemPremiseConditionNotFound, map[string]string{"signal": "S99999"})
	wantSentinelC(t, err, ErrNotFound, ErrNotFound.Error()+`: no premise-flagged condition "S99999" you can read`)

	if err := f.app.ConfirmPremise(as("human-2", Human), "d", "p", "still right"); err != nil {
		t.Fatal(err)
	}
	err = f.app.ConfirmPremiseCondition(as("human-2", Human), sigID, "again")
	wantTypedC(t, err, problemPremiseConditionNotOpen, map[string]string{"signal": sigID, "state": "resolved"})
	wantSentinelC(t, err, ErrConflict, ErrConflict.Error()+`: the condition "`+sigID+`" is resolved, not open`)

	// The condition open again while the pair stays confirmed for the same
	// premise state: the second confirmation does not win.
	if _, err := f.db.Exec(`UPDATE smeldr_signals SET status = 'open' WHERE id = $1`, sigID); err != nil {
		t.Fatal(err)
	}
	err = f.app.ConfirmPremise(as("human-2", Human), "d", "p", "still right")
	wantTypedC(t, err, problemPremiseAlreadyConfirmed, map[string]string{"decision": "D-d"})
	wantSentinelC(t, err, ErrConflict, ErrConflict.Error()+": the premise of D-d was already confirmed for its current state")

	f.app.cfg.RevCutover = time.Now().Add(-time.Hour)
	_, err = f.app.ConfirmPremiseAt(as("human-2", Human), "d", "p", "still holds", nil)
	wantTypedC(t, err, problemRevMissing, map[string]string{"record": "D-d", "write": "confirm_premise"})
	wantFieldC(t, err, "rev", "confirm_premise D-d: send the rev you read, so a change made since is not overwritten unseen; it is required")
}

func TestPhaseTwoC_SignalKinds(t *testing.T) {
	_, err := CheckSignal(context.Background(), &Signal{SignalType: "ratification-required", Message: "m"}, SignalCutovers{})
	wantTypedC(t, err, problemSignalConditionSystemOnly, map[string]string{"signal_type": "ratification-required"})
	wantSentinelC(t, err, ErrForbidden, ErrForbidden.Error()+`: signal_type "ratification-required" is a condition: only the system creates it`)

	app, db := signalTestApp(t)
	ctx := context.Background()
	q := newSignal(t, db, "question")
	c := newSignal(t, db, "conflict-detected")

	_, err = app.TransitionItemVia(ctx, "mcp", "Signal", q, "resolved", "")
	wantTypedC(t, err, problemSignalRequestNotResolvable, map[string]string{"signal_type": "question"})
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), ErrConflict.Error()+`: a "question" Signal is a request: it is answered or withdrawn, never resolved`) {
		t.Errorf("err = %v", err)
	}

	_, err = app.TransitionItemVia(ctx, "mcp", "Signal", c, "answered", "done")
	wantTypedC(t, err, problemSignalConditionNotAnswerable, map[string]string{"signal_type": "conflict-detected"})
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), ErrConflict.Error()+`: a "conflict-detected" Signal is a condition: it is resolved by the system when it ends, never answered or withdrawn`) {
		t.Errorf("err = %v", err)
	}

	if err := app.AnswerSignal(ctx, q, "yes", ""); err != nil {
		t.Fatal(err)
	}
	err = app.AnswerSignal(ctx, q, "again", "")
	wantTypedC(t, err, problemSignalNotOpen, map[string]string{"state": "answered"})
	wantSentinelC(t, err, ErrConflict, ErrConflict.Error()+": the Signal is answered, not open")

	gov, gdb, _ := setupTransitionItemApp(t)
	gq := newSignal(t, gdb, "question")
	err = gov.AnswerSignal(TrustedRead(context.Background()), gq, "a", "")
	wantTypedC(t, err, problemSignalAnswerNotPermitted, nil)
	wantSentinelC(t, err, ErrForbidden, ErrForbidden.Error())
}

func TestPhaseTwoC_SignalConditionResolvedByCaller(t *testing.T) {
	app, db := signalTestApp(t)
	c := newSignal(t, db, "conflict-detected")
	_, err := app.TransitionItemVia(context.Background(), "mcp", "Signal", c, "resolved", "")
	wantTypedC(t, err, problemSignalConditionSystemOnly, nil)
	if !errors.Is(err, ErrForbidden) || !strings.Contains(err.Error(), ErrForbidden.Error()+": a condition is resolved by the system when it ends") {
		t.Errorf("err = %v", err)
	}
}
