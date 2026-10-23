package smeldr

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// T918 part 2: approval per Area (D144) and the one-item role.

func human(id string) Context { return NewTestContext(User{ID: id, Roles: []Role{Editor, Human}}) }
func agent(id string) Context { return NewTestContext(User{ID: id, Roles: []Role{Editor, Agent}}) }

var approvalPaths = []string{"cmd/x/main.go", "internal/billing/a.go"}

// newApprovalCheck is a Check of both fixture Areas, those in requires set to
// requires-approval, with the approval roles granted:
//   - u-author: approver (from the coverage fixture);
//   - dapp: domain-approver of the General Domain;
//   - aa2: area-approver of the second Area;
//   - bot: an agent with area-approver on the second Area.
func newApprovalCheck(t *testing.T, requires func(f changeCheckFixture) []*Area) (changeCheckFixture, *ChangeCheck) {
	t.Helper()
	f := newChangeCheckFixture(t)
	ctx := context.Background()
	for _, reg := range []func(context.Context, *RoleStore) error{RegisterDomainApproverRole, RegisterAreaApproverRole, RegisterItemChangeApproverRole} {
		if err := reg(ctx, f.rs); err != nil {
			t.Fatal(err)
		}
	}
	for _, g := range []RoleGrant{
		{TokenID: "dapp", RoleName: RoleDomainApprover, ScopeAnchorID: f.domain},
		{TokenID: "aa2", RoleName: RoleAreaApprover, ScopeAnchorID: f.a2.ID},
		{TokenID: "bot", RoleName: RoleAreaApprover, ScopeAnchorID: f.a2.ID},
		{TokenID: "dapp", RoleName: "editor"},
		{TokenID: "aa2", RoleName: "editor"},
		{TokenID: "bot", RoleName: "editor"},
		{TokenID: "u-author", RoleName: "editor"},
	} {
		if _, err := f.rs.Grant(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range requires(f) {
		if _, err := f.db.Exec(`UPDATE smeldr_areas SET approval_mode = 'requires-approval' WHERE id = $1`, a.ID); err != nil {
			t.Fatal(err)
		}
	}
	res, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 30, HeadCommit: headA, Paths: approvalPaths})
	if err != nil {
		t.Fatal(err)
	}
	return f, res.Check
}

func both(f changeCheckFixture) []*Area   { return []*Area{f.a1, f.a2} }
func second(f changeCheckFixture) []*Area { return []*Area{f.a2} }

func (f changeCheckFixture) status(t *testing.T, id string) string {
	t.Helper()
	var s string
	if err := f.db.QueryRow(`SELECT status FROM smeldr_change_checks WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// requests are a Check's approval requests by Area address: id, status,
// message.
func (f changeCheckFixture) requests(t *testing.T, checkID string) map[string][3]string {
	t.Helper()
	rows, err := f.db.Query(`SELECT a.address, s.id, s.status, s.message FROM smeldr_signals s
		JOIN smeldr_relations r ON r.source_id = s.id AND r.relation_kind = 'belongs_to_area'
		JOIN smeldr_areas a ON a.id = r.target_id
		WHERE s.subject_type = 'ChangeCheck' AND s.subject_id = $1 AND s.receiver = 'approve-change'`, checkID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string][3]string{}
	for rows.Next() {
		var area string
		var v [3]string
		if err := rows.Scan(&area, &v[0], &v[1], &v[2]); err != nil {
			t.Fatal(err)
		}
		out[area] = v
	}
	return out
}

func TestChangeCheck_Requests(t *testing.T) {
	f, c := newApprovalCheck(t, both)
	reqs := f.requests(t, c.ID)
	if len(reqs) != 2 {
		t.Fatalf("requests = %v", reqs)
	}
	for area, r := range reqs {
		other := f.a1.Address
		if area == f.a1.Address {
			other = f.a2.Address
		}
		if r[1] != signalOpen || !strings.Contains(r[2], area) || !strings.Contains(r[2], c.Address) || strings.Contains(r[2], other) {
			t.Errorf("request for %s: %v", area, r)
		}
	}
	// A redelivery opens nothing more.
	if _, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 30, HeadCommit: headA, Paths: approvalPaths}); err != nil {
		t.Fatal(err)
	}
	if len(f.requests(t, c.ID)) != 2 {
		t.Error("a redelivery opened another request")
	}
	// The generic answer and withdraw are refused.
	id := reqs[f.a1.Address][0]
	if err := f.app.AnswerSignal(human("u-author"), id, "approve", ""); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "answer_change_check") {
		t.Errorf("generic answer: %v", err)
	}
	if err := f.app.WithdrawSignal(human("u-author"), id, "no"); !errors.Is(err, ErrConflict) {
		t.Errorf("generic withdraw: %v", err)
	}
}

func TestAnswerChangeCheck_Approve(t *testing.T) {
	f, c := newApprovalCheck(t, both)
	// The approver answers for the first Area: one left.
	if err := f.app.AnswerChangeCheck(human("u-author"), c.Address, f.a1.Address, true, ""); err != nil {
		t.Fatal(err)
	}
	if f.status(t, c.ID) != ChangeCheckAwaitingApproval {
		t.Errorf("after one of two: %s", f.status(t, c.ID))
	}
	reqs := f.requests(t, c.ID)
	if reqs[f.a1.Address][1] != signalAnswered || reqs[f.a2.Address][1] != signalOpen {
		t.Errorf("requests = %v", reqs)
	}
	// The domain-approver answers for the second (the away case).
	if err := f.app.AnswerChangeCheck(human("dapp"), c.Slug, f.a2.Slug, true, ""); err != nil {
		t.Fatal(err)
	}
	if f.status(t, c.ID) != ChangeCheckApproved {
		t.Errorf("after both: %s", f.status(t, c.ID))
	}
	view, err := f.app.ChangeCheckOf(f.ops, c.Address)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range view.Areas {
		if a.Verdict != AreaVerdictApproved {
			t.Errorf("%s verdict %s", a.Address, a.Verdict)
		}
	}
	// No answer counts after the verdict.
	if err := f.app.AnswerChangeCheck(human("u-author"), c.Address, f.a1.Address, true, ""); !errors.Is(err, ErrConflict) {
		t.Errorf("answer after approved: %v", err)
	}
}

func TestAnswerChangeCheck_NotApproved(t *testing.T) {
	f, c := newApprovalCheck(t, second)
	if err := f.app.AnswerChangeCheck(human("aa2"), c.Address, f.a2.Address, false, " "); err == nil || !strings.Contains(err.Error(), "reason") {
		t.Errorf("no reason: %v", err)
	}
	if err := f.app.AnswerChangeCheck(human("aa2"), c.Address, f.a2.Address, false, "breaks the CLI contract"); err != nil {
		t.Fatal(err)
	}
	if f.status(t, c.ID) != ChangeCheckNotApproved {
		t.Errorf("status = %s", f.status(t, c.ID))
	}
	var answer string
	if err := f.db.QueryRow(`SELECT answer FROM smeldr_signals WHERE id = $1`, f.requests(t, c.ID)[f.a2.Address][0]).Scan(&answer); err != nil || answer != "not approved: breaks the CLI contract" {
		t.Errorf("answer = %q, %v", answer, err)
	}
}

func TestAnswerChangeCheck_Refusals(t *testing.T) {
	f, c := newApprovalCheck(t, second)
	for _, tc := range []struct {
		name        string
		ctx         Context
		check, area string
		want        error
		msg         string
	}{
		{"no approve-change", human("ops-1"), c.Address, f.a2.Address, ErrForbidden, "for Area " + f.a2.Address + " needs approve-change"},
		{"agent without an Area grant", agent("u-author"), c.Address, f.a2.Address, ErrForbidden, "approve-change"},
		{"comment-only Area", human("u-author"), c.Address, f.a1.Address, ErrConflict, "only comments"},
		{"Check not found", human("u-author"), "CHK999", f.a2.Address, ErrNotFound, ""},
		{"Area not found", human("u-author"), c.Address, "ARE999", ErrNotFound, "no Area"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := f.app.AnswerChangeCheck(tc.ctx, tc.check, tc.area, true, "")
			if !errors.Is(err, tc.want) || !strings.Contains(err.Error(), tc.msg) {
				t.Errorf("err = %v, want %v with %q", err, tc.want, tc.msg)
			}
		})
	}
	// An Area the Check does not touch.
	other, err := f.areas.MCPCreate(TrustedRead(authorCtx()).(Context), map[string]any{"name": "Elsewhere"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.app.AnswerChangeCheck(human("u-author"), c.Address, asItem[*Area](other).Address, true, ""); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "does not touch") {
		t.Errorf("untouched Area: %v", err)
	}
	// The agent area-approver of that Area counts.
	if err := f.app.AnswerChangeCheck(agent("bot"), c.Address, f.a2.Address, true, ""); err != nil {
		t.Errorf("agent area-approver: %v", err)
	}
	if err := f.app.AnswerChangeCheck(human("u-author"), c.Address, f.a2.Address, true, ""); !errors.Is(err, ErrConflict) {
		t.Errorf("answered twice: %v", err)
	}
}

func TestChangeCheck_SupersedeWithdrawsRequests(t *testing.T) {
	f, c := newApprovalCheck(t, both)
	res, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 30, HeadCommit: headB, Paths: approvalPaths})
	if err != nil {
		t.Fatal(err)
	}
	for area, r := range f.requests(t, c.ID) {
		if r[1] != signalWithdrawn {
			t.Errorf("old request for %s: %s", area, r[1])
		}
	}
	if len(f.requests(t, res.Check.ID)) != 2 {
		t.Error("the new Check has no requests of its own")
	}
	if err := f.app.AnswerChangeCheck(human("u-author"), c.Address, f.a1.Address, true, ""); !errors.Is(err, ErrConflict) {
		t.Errorf("answer on a superseded Check: %v", err)
	}
}

func TestItemChangeApprover(t *testing.T) {
	f, c := newApprovalCheck(t, both)
	grant := func(grantor Context, grantee, scope string) error {
		_, err := f.rs.Grant(grantor, RoleGrant{TokenID: grantee, RoleName: RoleItemChangeApprover, ScopeStatic: []string{scope}})
		return err
	}
	scope := "ChangeCheck:" + c.ID
	for _, tc := range []struct {
		name    string
		grantor Context
		scope   string
		msg     string
	}{
		{"delegated by an agent", agent("bot"), scope, "human"},
		{"by a human without approve-change", human("ops-1"), scope, "no approve-change over Area"},
		{"not a Check", human("u-author"), "Decision:" + f.dArea, "exactly one ChangeCheck"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := grant(tc.grantor, "bot2", tc.scope); !errors.Is(err, ErrForbidden) || !strings.Contains(err.Error(), tc.msg) {
				t.Errorf("err = %v, want forbidden with %q", err, tc.msg)
			}
		})
	}
	if err := grant(human("u-author"), "bot2", "ChangeCheck:nope"); err == nil {
		t.Error("no such Check: granted")
	}
	if _, err := f.rs.Grant(human("u-author"), RoleGrant{TokenID: "bot2", RoleName: RoleItemChangeApprover, ScopeStatic: []string{scope, "Area:" + f.a1.ID}}); !errors.Is(err, ErrForbidden) {
		t.Errorf("two scopes: %v", err)
	}
	if _, err := f.rs.Grant(context.Background(), RoleGrant{TokenID: "bot2", RoleName: RoleItemChangeApprover, ScopeStatic: []string{scope}}); !errors.Is(err, ErrForbidden) {
		t.Errorf("no grantor: %v", err)
	}
	// A human approver delegates it to an agent, which then answers for every
	// Area of that Check.
	if err := grant(human("u-author"), "bot2", scope); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rs.Grant(context.Background(), RoleGrant{TokenID: "bot2", RoleName: "editor"}); err != nil {
		t.Fatal(err)
	}
	for _, a := range []*Area{f.a1, f.a2} {
		if err := f.app.AnswerChangeCheck(agent("bot2"), c.Address, a.Address, true, ""); err != nil {
			t.Errorf("delegate answers %s: %v", a.Address, err)
		}
	}
	if f.status(t, c.ID) != ChangeCheckApproved {
		t.Errorf("status = %s", f.status(t, c.ID))
	}
	// Not on another Check.
	next, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 31, HeadCommit: headB, Paths: approvalPaths})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.app.AnswerChangeCheck(agent("bot2"), next.Check.Address, f.a1.Address, true, ""); !errors.Is(err, ErrForbidden) {
		t.Errorf("another Check: %v", err)
	}
}

func TestChangeCheck_SupersedesEdgeHeals(t *testing.T) {
	// The edge is written with the supersede: refused, the supersede rolls
	// back, and the next call does both.
	f, c := newApprovalCheck(t, second)
	if _, err := f.db.Exec(`CREATE TRIGGER no_edge BEFORE INSERT ON smeldr_relations WHEN NEW.relation_kind = 'supersedes' BEGIN SELECT RAISE(ABORT, 'no'); END`); err != nil {
		t.Fatal(err)
	}
	in := ChangeInput{Repository: "acme/app", Number: 30, HeadCommit: headB, Paths: approvalPaths}
	if _, err := f.app.CheckChange(f.ops, in); err == nil {
		t.Fatal("edge refused: no error")
	}
	if f.status(t, c.ID) == ChangeCheckSuperseded || f.edgesTo(t, c.ID) != 0 {
		t.Fatalf("a refused edge left %s with %d edges", f.status(t, c.ID), f.edgesTo(t, c.ID))
	}
	if _, err := f.db.Exec(`DROP TRIGGER no_edge`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.CheckChange(f.ops, in); err != nil {
		t.Fatal(err)
	}
	if f.status(t, c.ID) != ChangeCheckSuperseded || f.edgesTo(t, c.ID) != 1 {
		t.Errorf("after the next call: %s, %d edges", f.status(t, c.ID), f.edgesTo(t, c.ID))
	}
	// An older row superseded without its edge gets one from the newest.
	if _, err := f.db.Exec(`DELETE FROM smeldr_relations WHERE target_id = $1 AND relation_kind = 'supersedes'`, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.CheckChange(f.ops, in); err != nil {
		t.Fatal(err)
	}
	if f.edgesTo(t, c.ID) != 1 {
		t.Errorf("healed edges: %d", f.edgesTo(t, c.ID))
	}
	// Its write refused: reported.
	if _, err := f.db.Exec(`DELETE FROM smeldr_relations WHERE target_id = $1 AND relation_kind = 'supersedes'`, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`CREATE TRIGGER no_edge BEFORE INSERT ON smeldr_relations WHEN NEW.relation_kind = 'supersedes' BEGIN SELECT RAISE(ABORT, 'no'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.CheckChange(f.ops, in); !errors.Is(err, ErrInternal) {
		t.Errorf("heal refused: %v", err)
	}
}

func (f changeCheckFixture) edgesTo(t *testing.T, id string) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_relations WHERE target_id = $1 AND relation_kind = 'supersedes'`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The paths that need a broken store, a missing actor or no governance.
func TestChangeCheckApproval_Failures(t *testing.T) {
	trigger := func(f changeCheckFixture, sql string) {
		t.Helper()
		if _, err := f.db.Exec(sql); err != nil {
			t.Fatal(err)
		}
	}
	in := ChangeInput{Repository: "acme/app", Number: 30, HeadCommit: headA, Paths: approvalPaths}
	// Opening a request fails at each of its writes; the Check stays awaiting
	// and a redelivery opens it.
	for _, tc := range []struct{ name, sql string }{
		{"signal", `CREATE TRIGGER t BEFORE INSERT ON smeldr_signals BEGIN SELECT RAISE(ABORT, 'no'); END`},
		{"area row", `CREATE TRIGGER t BEFORE UPDATE ON smeldr_change_check_areas BEGIN SELECT RAISE(ABORT, 'no'); END`},
		{"place", `CREATE TRIGGER t BEFORE INSERT ON smeldr_relations WHEN NEW.source_type = 'Signal' BEGIN SELECT RAISE(ABORT, 'no'); END`},
	} {
		t.Run("request "+tc.name, func(t *testing.T) {
			f, _ := newApprovalCheck(t, func(changeCheckFixture) []*Area { return nil })
			if _, err := f.db.Exec(`UPDATE smeldr_areas SET approval_mode = 'requires-approval' WHERE id = $1`, f.a2.ID); err != nil {
				t.Fatal(err)
			}
			trigger(f, tc.sql)
			c := in
			c.Number = 40
			if _, err := f.app.CheckChange(f.ops, c); !errors.Is(err, ErrInternal) {
				t.Errorf("%v, want internal", err)
			}
		})
	}
	f, c := newApprovalCheck(t, both)
	// Answered once, the Area refuses a second answer while the Check waits.
	if err := f.app.AnswerChangeCheck(human("u-author"), c.Address, f.a1.Address, true, ""); err != nil {
		t.Fatal(err)
	}
	if err := f.app.AnswerChangeCheck(human("u-author"), c.Address, f.a1.Address, true, ""); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "already answered") {
		t.Errorf("second answer: %v", err)
	}
	// No actor.
	if err := f.app.authorizeChangeCheckAnswer(context.Background(), c.ID, f.a2.ID, f.a2.Address); !errors.Is(err, ErrForbidden) {
		t.Errorf("no actor: %v", err)
	}
	// Closing the request fails: the answer is reported.
	trigger(f, `CREATE TRIGGER t BEFORE UPDATE ON smeldr_signals BEGIN SELECT RAISE(ABORT, 'no'); END`)
	if err := f.app.AnswerChangeCheck(human("u-author"), c.Address, f.a2.Address, true, ""); !errors.Is(err, ErrInternal) {
		t.Errorf("request update refused: %v", err)
	}
	if err := f.app.withdrawChangeCheckRequests(TrustedRead(f.ops), c.ID, "x"); !errors.Is(err, ErrInternal) {
		t.Errorf("withdraw refused: %v", err)
	}
	trigger(f, `DROP TRIGGER t`)
	// The answer row cannot be written.
	trigger(f, `CREATE TRIGGER u BEFORE UPDATE ON smeldr_change_check_areas BEGIN SELECT RAISE(ABORT, 'no'); END`)
	g, d := newApprovalCheck(t, both)
	trigger(g, `CREATE TRIGGER u BEFORE UPDATE ON smeldr_change_check_areas WHEN NEW.verdict <> 'awaiting-approval' BEGIN SELECT RAISE(ABORT, 'no'); END`)
	if err := g.app.AnswerChangeCheck(human("u-author"), d.Address, g.a1.Address, true, ""); !errors.Is(err, ErrInternal) {
		t.Errorf("answer row refused: %v", err)
	}
	// Reads that fail.
	for _, tc := range []struct{ name, sql string }{
		{"requests", `ALTER TABLE smeldr_change_check_areas RENAME TO gone`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, e := newApprovalCheck(t, both)
			trigger(h, tc.sql)
			if err := h.app.withdrawChangeCheckRequests(TrustedRead(h.ops), e.ID, "x"); !errors.Is(err, ErrInternal) {
				t.Errorf("withdraw: %v", err)
			}
			if err := h.app.requestChangeCheckApprovals(TrustedRead(h.ops), e); !errors.Is(err, ErrInternal) {
				t.Errorf("request: %v", err)
			}
			if err := h.app.AnswerChangeCheck(human("u-author"), e.Address, h.a1.Address, true, ""); !errors.Is(err, ErrInternal) {
				t.Errorf("answer: %v", err)
			}
		})
	}
	h, e := newApprovalCheck(t, both)
	trigger(h, `ALTER TABLE smeldr_signals RENAME TO gone`)
	if err := refuseChangeCheckRequest(context.Background(), h.db, "x"); !errors.Is(err, ErrInternal) {
		t.Errorf("refuse check: %v", err)
	}
	_ = e
	// No database; no governance.
	bare := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")})
	if err := bare.AnswerChangeCheck(context.Background(), "x", "y", true, ""); !errors.Is(err, ErrInternal) {
		t.Errorf("no database: %v", err)
	}
	if err := bare.authorizeChangeCheckAnswer(context.Background(), "c", "a", "A"); err != nil {
		t.Errorf("no governance: %v", err)
	}
}

// The supersede's own failures.
func TestChangeCheck_SupersedeFailures(t *testing.T) {
	// Withdrawing the old requests fails: reported, and the next call heals.
	f, c := newApprovalCheck(t, both)
	if _, err := f.db.Exec(`CREATE TRIGGER t BEFORE UPDATE ON smeldr_signals BEGIN SELECT RAISE(ABORT, 'no'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 30, HeadCommit: headB, Paths: approvalPaths}); !errors.Is(err, ErrInternal) {
		t.Errorf("withdraw refused: %v", err)
	}
	if f.status(t, c.ID) != ChangeCheckSuperseded {
		t.Errorf("old Check: %s", f.status(t, c.ID))
	}
	sys := TrustedRead(f.ops)
	if _, err := f.db.Exec(`DROP TRIGGER t`); err != nil {
		t.Fatal(err)
	}
	// Reads that fail.
	if _, err := f.db.Exec(`ALTER TABLE smeldr_relations RENAME TO relations_gone`); err != nil {
		t.Fatal(err)
	}
	if err := f.app.supersedeOlderChecks(sys, "acme/app", 30); !errors.Is(err, ErrInternal) {
		t.Errorf("edge count unreadable: %v", err)
	}
	if _, err := f.db.Exec(`ALTER TABLE smeldr_change_checks RENAME TO checks_gone`); err != nil {
		t.Fatal(err)
	}
	if err := f.app.supersedeOlderChecks(sys, "acme/app", 30); !errors.Is(err, ErrInternal) {
		t.Errorf("Checks unreadable: %v", err)
	}
	if _, err := f.app.claimSupersede(sys, c.ID, c.Address, "x", "CHK2"); !errors.Is(err, ErrInternal) {
		t.Errorf("claim, Check unreadable: %v", err)
	}
	// An older table gains the request column; a failing one reports it.
	db := newSQLiteDB(t)
	if _, err := db.Exec(`CREATE TABLE smeldr_change_check_areas (check_id TEXT, area_id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := CreateGovernanceTables(db); err != nil {
		t.Fatal(err)
	}
	if err := EnsureChangeCheckTables(context.Background(), &execFailDB{DB: db, failOn: "ADD COLUMN"}); err == nil {
		t.Error("column add refused: no error")
	}
	if err := EnsureChangeCheckTables(context.Background(), db); err != nil {
		t.Errorf("column add: %v", err)
	}
}

// S3154 fix 1: the grantor of the one-item role must hold approve-change over
// every Area of the Check that requires approval.
func TestItemChangeApprover_GrantorCoversEveryArea(t *testing.T) {
	f, c := newApprovalCheck(t, both)
	if _, err := f.rs.Grant(context.Background(), RoleGrant{TokenID: "aa1", RoleName: RoleAreaApprover, ScopeAnchorID: f.a1.ID}); err != nil {
		t.Fatal(err)
	}
	scope := []string{"ChangeCheck:" + c.ID}
	// An area-approver of the first Area only: refused, naming the second.
	_, err := f.rs.Grant(human("aa1"), RoleGrant{TokenID: "bot3", RoleName: RoleItemChangeApprover, ScopeStatic: scope})
	if !errors.Is(err, ErrForbidden) || !strings.Contains(err.Error(), f.a2.Address) {
		t.Errorf("one-Area grantor: %v", err)
	}
	// The domain-approver above both, and an approver, may.
	for _, grantor := range []string{"dapp", "u-author"} {
		if _, err := f.rs.Grant(human(grantor), RoleGrant{TokenID: "bot-" + grantor, RoleName: RoleItemChangeApprover, ScopeStatic: scope}); err != nil {
			t.Errorf("%s: %v", grantor, err)
		}
	}
	// A Check whose other Area only comments needs no authority over it.
	g, d := newApprovalCheck(t, second)
	if _, err := g.rs.Grant(human("aa2"), RoleGrant{TokenID: "bot4", RoleName: RoleItemChangeApprover, ScopeStatic: []string{"ChangeCheck:" + d.ID}}); err != nil {
		t.Errorf("other Area comment-only: %v", err)
	}
	// A Check with no Area that requires approval has nothing to delegate.
	h := newChangeCheckFixture(t)
	if err := RegisterItemChangeApproverRole(context.Background(), h.rs); err != nil {
		t.Fatal(err)
	}
	res, err := h.app.CheckChange(h.ops, ChangeInput{Repository: "acme/app", Number: 50, HeadCommit: headA, Paths: approvalPaths})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.rs.Grant(human("u-author"), RoleGrant{TokenID: "bot5", RoleName: RoleItemChangeApprover, ScopeStatic: []string{"ChangeCheck:" + res.Check.ID}}); !errors.Is(err, ErrForbidden) {
		t.Errorf("nothing to approve: %v", err)
	}
}

// S3154 fix 2: a request is written placed in one step and announced after.
func TestChangeCheck_RequestPlacedThenAnnounced(t *testing.T) {
	f := newChangeCheckFixture(t)
	f.app.EventStream()
	ctx := context.Background()
	for _, reg := range []func(context.Context, *RoleStore) error{RegisterAreaReaderRole, RegisterAreaApproverRole} {
		if err := reg(ctx, f.rs); err != nil {
			t.Fatal(err)
		}
	}
	for _, g := range []RoleGrant{{TokenID: "aa2", RoleName: RoleAreaApprover, ScopeAnchorID: f.a2.ID}, {TokenID: "aa2", RoleName: "area-reader", ScopeAnchorID: f.a2.ID}} {
		if _, err := f.rs.Grant(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.db.Exec(`UPDATE smeldr_areas SET approval_mode = 'requires-approval' WHERE id = $1`, f.a2.ID); err != nil {
		t.Fatal(err)
	}
	ch, err := f.app.eventBroadcaster.subscribeScoped("aa2", eventStreamChannelAll, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.app.eventBroadcaster.unsubscribe(ch)
	// The first attempt cannot place its request: nothing is left open.
	if _, err := f.db.Exec(`CREATE TRIGGER t BEFORE INSERT ON smeldr_relations WHEN NEW.source_type = 'Signal' BEGIN SELECT RAISE(ABORT, 'no'); END`); err != nil {
		t.Fatal(err)
	}
	in := ChangeInput{Repository: "acme/app", Number: 51, HeadCommit: headA, Paths: approvalPaths}
	if _, err := f.app.CheckChange(f.ops, in); !errors.Is(err, ErrInternal) {
		t.Fatalf("place refused: %v", err)
	}
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_signals WHERE subject_type = 'ChangeCheck'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("a refused place left %d requests (%v)", n, err)
	}
	if _, err := f.db.Exec(`DROP TRIGGER t`); err != nil {
		t.Fatal(err)
	}
	res, err := f.app.CheckChange(f.ops, in)
	if err != nil {
		t.Fatal(err)
	}
	if reqs := f.requests(t, res.Check.ID); len(reqs) != 1 || reqs[f.a2.Address][1] != signalOpen {
		t.Errorf("after redelivery: %v", reqs)
	}
	// When signal.created goes out, the request is already placed in its
	// Area (a reader scoped to that Area can see it), and a scope loaded then
	// reads it.
	timeout := time.After(2 * time.Second)
	for {
		select {
		case b := <-ch:
			if !strings.Contains(string(b), `"signal.created"`) {
				continue
			}
			var placed int
			if err := f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_relations r JOIN smeldr_signals s ON s.id = r.source_id
				WHERE s.subject_id = $1 AND r.relation_kind = 'belongs_to_area' AND r.target_id = $2`, res.Check.ID, f.a2.ID).Scan(&placed); err != nil || placed != 1 {
				t.Errorf("at signal.created the request is placed %d times (%v)", placed, err)
			}
			fresh, err := LoadReadScope(ctx, f.rs, User{ID: "aa2"})
			if err != nil {
				t.Fatal(err)
			}
			if !fresh.Readable("Signal", f.requests(t, res.Check.ID)[f.a2.Address][0]) {
				t.Error("the Area's reader cannot read its request")
			}
			return
		case <-timeout:
			t.Fatal("no signal.created")
		}
	}
}

// S3154 fix 3: nothing about an Area is said before the caller's read and
// authority are checked.
func TestAnswerChangeCheck_RevealsNothing(t *testing.T) {
	f, c := newApprovalCheck(t, both)
	if _, err := f.rs.Grant(context.Background(), RoleGrant{TokenID: "reader-a", RoleName: RoleAreaApprover, ScopeAnchorID: f.a1.ID}); err != nil {
		t.Fatal(err)
	}
	err := f.app.AnswerChangeCheck(NewTestContext(User{ID: "reader-a", Roles: []Role{Author, Human}}), c.Address, f.a2.ID, true, "")
	if !errors.Is(err, ErrNotFound) || strings.Contains(err.Error(), f.a2.Address) || strings.Contains(err.Error(), "requires") {
		t.Errorf("unreadable Area: %v", err)
	}
}

// S3154 minor: a superseded Check whose request withdrawal failed has it
// withdrawn by the next call.
func TestChangeCheck_WithdrawHeals(t *testing.T) {
	f, c := newApprovalCheck(t, both)
	if _, err := f.db.Exec(`CREATE TRIGGER t BEFORE UPDATE ON smeldr_signals BEGIN SELECT RAISE(ABORT, 'no'); END`); err != nil {
		t.Fatal(err)
	}
	in := ChangeInput{Repository: "acme/app", Number: 30, HeadCommit: headB, Paths: approvalPaths}
	if _, err := f.app.CheckChange(f.ops, in); err == nil {
		t.Fatal("withdraw refused: no error")
	}
	// Still refused: the next call reports the withdrawal it could not do.
	if _, err := f.app.CheckChange(f.ops, in); !errors.Is(err, ErrInternal) {
		t.Errorf("withdraw still refused: %v", err)
	}
	if _, err := f.db.Exec(`DROP TRIGGER t`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.CheckChange(f.ops, in); err != nil {
		t.Fatal(err)
	}
	for area, r := range f.requests(t, c.ID) {
		if r[1] != signalWithdrawn {
			t.Errorf("old request for %s: %s", area, r[1])
		}
	}
}

// A request another call opened at the same moment is not opened twice; the
// stores the answer and the grant read can fail.
func TestChangeCheckApproval_Races(t *testing.T) {
	f := newChangeCheckFixture(t)
	if _, err := f.db.Exec(`UPDATE smeldr_areas SET approval_mode = 'requires-approval' WHERE id = $1`, f.a2.ID); err != nil {
		t.Fatal(err)
	}
	// Each Signal insert marks the Area row taken, as a concurrent call would.
	if _, err := f.db.Exec(`CREATE TRIGGER taken AFTER INSERT ON smeldr_signals BEGIN
		UPDATE smeldr_change_check_areas SET signal_id = 'other' WHERE check_id = NEW.subject_id; END`); err != nil {
		t.Fatal(err)
	}
	res, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 60, HeadCommit: headA, Paths: approvalPaths})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_signals WHERE subject_id = $1`, res.Check.ID).Scan(&n); err != nil || n != 0 {
		t.Errorf("a taken request was opened again: %d (%v)", n, err)
	}

	g, c := newApprovalCheck(t, both)
	if _, err := g.db.Exec(`ALTER TABLE smeldr_areas RENAME TO areas_gone`); err != nil {
		t.Fatal(err)
	}
	if err := g.app.AnswerChangeCheck(human("u-author"), c.Address, "ARE1", true, ""); !errors.Is(err, ErrInternal) {
		t.Errorf("Areas unreadable: %v", err)
	}
	h, d := newApprovalCheck(t, both)
	if _, err := h.db.Exec(`ALTER TABLE smeldr_change_check_areas RENAME TO gone`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.rs.Grant(human("u-author"), RoleGrant{TokenID: "bot6", RoleName: RoleItemChangeApprover, ScopeStatic: []string{"ChangeCheck:" + d.ID}}); !errors.Is(err, ErrForbidden) {
		t.Errorf("Check Areas unreadable at grant: %v", err)
	}
}
