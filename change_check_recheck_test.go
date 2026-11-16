package smeldr

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// D196, T1294: a change in the Decisions in force for an Area reopens the open
// Checks of that Area.

// liveCheck is the newest Check of the change that is not superseded.
func (f changeCheckFixture) liveCheck(t *testing.T, number int) *ChangeCheck {
	t.Helper()
	c, err := f.app.liveChangeCheck(context.Background(), "acme/app", number)
	if err != nil || c == nil {
		t.Fatalf("live Check of #%d: %v %v", number, c, err)
	}
	return c
}

// against is what a Check is checked against, by Decision ID.
func (f changeCheckFixture) against(t *testing.T, id string) map[string]bool {
	t.Helper()
	ids, err := queryStrings(context.Background(), f.db, `SELECT target_id FROM smeldr_relations WHERE source_id = $1 AND relation_kind = 'checked_against' AND invalid_at IS NULL`, id)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, s := range ids {
		out[s] = true
	}
	return out
}

// approveBoth approves the approval Check for both Areas.
func approveBoth(t *testing.T, f changeCheckFixture, c *ChangeCheck) {
	t.Helper()
	if err := f.app.AnswerChangeCheck(human("u-author"), c.Address, f.a1.Address, true, ""); err != nil {
		t.Fatal(err)
	}
	if err := f.app.AnswerChangeCheck(human("dapp"), c.Address, f.a2.Address, true, ""); err != nil {
		t.Fatal(err)
	}
	if f.status(t, c.ID) != ChangeCheckApproved {
		t.Fatalf("not approved: %s", f.status(t, c.ID))
	}
}

// reason is the reason of the provenance record of id moving to to.
func (f changeCheckFixture) reason(t *testing.T, id, to string) string {
	t.Helper()
	var r string
	if err := f.db.QueryRow(`SELECT reason FROM smeldr_provenance WHERE subject_id = $1 AND to_state = $2 ORDER BY timestamp DESC LIMIT 1`, id, to).Scan(&r); err != nil {
		t.Fatalf("provenance of %s to %s: %v", id, to, err)
	}
	return r
}

// The rule: an approved Check whose Area's Decision leaves force waits for
// approval again as a new Check, and the old approval stays readable.
func TestReopen_DecisionLeavesForce(t *testing.T) {
	f, c := newApprovalCheck(t, both)
	approveBoth(t, f, c)
	if !f.against(t, c.ID)[f.dArea] {
		t.Fatal("the Check is not checked against the Area's Decision")
	}
	if _, err := f.app.RetireDecision(human("u-author"), f.dArea, "no longer wanted", nil); err != nil {
		t.Fatal(err)
	}
	live := f.liveCheck(t, 30)
	if live.ID == c.ID {
		t.Fatal("no new Check after the Decision left force")
	}
	if string(live.Status) != ChangeCheckAwaitingApproval || live.HeadCommit != c.HeadCommit {
		t.Errorf("new Check: %s at %s", live.Status, live.HeadCommit)
	}
	reqs := f.requests(t, live.ID)
	if reqs[f.a1.Address][1] != signalOpen || reqs[f.a2.Address][1] != signalOpen {
		t.Errorf("new requests: %v", reqs)
	}
	if f.against(t, live.ID)[f.dArea] || !f.against(t, live.ID)[f.dOrg] {
		t.Errorf("new Check checked against %v", f.against(t, live.ID))
	}
	// The old one: superseded with the reason, its answers and what it was
	// checked against kept.
	if f.status(t, c.ID) != ChangeCheckSuperseded {
		t.Errorf("old Check: %s", f.status(t, c.ID))
	}
	reason := f.reason(t, c.ID, ChangeCheckSuperseded)
	if reason != "checked again as "+live.Address+": the Decisions in force for a touched Area changed" {
		t.Errorf("reason %q", reason)
	}
	for _, name := range []string{f.a1.Address, f.a2.Address, "D9", f.dArea} {
		if strings.Contains(reason, name) {
			t.Errorf("the reason names %s: %q", name, reason)
		}
	}
	var answeredBy string
	if err := f.db.QueryRow(`SELECT answered_by FROM smeldr_change_check_areas WHERE check_id = $1 AND area_id = $2`, c.ID, f.a1.ID).Scan(&answeredBy); err != nil || answeredBy != "u-author" {
		t.Errorf("old answer: %q %v", answeredBy, err)
	}
	if !f.against(t, c.ID)[f.dArea] {
		t.Error("the old Check no longer says what it was checked against")
	}
	if got := f.reason(t, live.ID, ChangeCheckAwaitingApproval); got == "" {
		t.Error("no transition of the new Check")
	}
	// A redelivered event of the same commit answers the new Check and makes
	// nothing.
	before := f.count(t, "smeldr_change_checks")
	res, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 30, HeadCommit: headA, Paths: approvalPaths})
	if err != nil || res.Check.ID != live.ID || f.count(t, "smeldr_change_checks") != before {
		t.Errorf("redelivery: %v %v, %d Checks", res.Check, err, f.count(t, "smeldr_change_checks"))
	}
}

// Point 8: a Decision entering force reopens too; a not-approved Check waits
// again and its old request is withdrawn.
func TestReopen_DecisionEntersForce(t *testing.T) {
	f, c := newApprovalCheck(t, second)
	if err := f.app.AnswerChangeCheck(human("dapp"), c.Address, f.a2.Address, false, "too wide"); err != nil {
		t.Fatal(err)
	}
	if f.status(t, c.ID) != ChangeCheckNotApproved {
		t.Fatalf("not not-approved: %s", f.status(t, c.ID))
	}
	if _, err := f.db.Exec(`UPDATE smeldr_decisions SET status = 'ratified' WHERE id = 'dec-proposed'`); err != nil {
		t.Fatal(err)
	}
	f.app.afterStatusChange(human("u-author"), statusTransition{typeName: "Decision", id: "dec-proposed", from: "proposed", to: "ratified"})
	live := f.liveCheck(t, 30)
	if live.ID == c.ID || string(live.Status) != ChangeCheckAwaitingApproval || !f.against(t, live.ID)["dec-proposed"] {
		t.Fatalf("after ratify: %s %s", live.Address, live.Status)
	}
	if reqs := f.requests(t, live.ID); reqs[f.a2.Address][1] != signalOpen {
		t.Errorf("new request: %v", reqs)
	}
}

// Point 3 for awaiting and point 4: an awaiting Check's open request is
// withdrawn and a new one opens; a comment-only Check reopens to commented
// and asks nobody.
func TestReopen_AwaitingAndCommentOnly(t *testing.T) {
	f, c := newApprovalCheck(t, second)
	old := f.requests(t, c.ID)[f.a2.Address]
	comment, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 31, HeadCommit: headA, Paths: []string{"internal/billing/x.go"}})
	if err != nil || string(comment.Check.Status) != ChangeCheckCommented {
		t.Fatal(comment.Check, err)
	}
	if _, err := f.app.RetireDecision(human("u-author"), f.dOrg, "gone", nil); err != nil {
		t.Fatal(err)
	}
	live := f.liveCheck(t, 30)
	if live.ID == c.ID || string(live.Status) != ChangeCheckAwaitingApproval {
		t.Fatalf("awaiting reopen: %s", live.Status)
	}
	if got := f.requests(t, c.ID)[f.a2.Address]; got[0] != old[0] || got[1] != signalWithdrawn {
		t.Errorf("old request %v", got)
	}
	lc := f.liveCheck(t, 31)
	if lc.ID == comment.Check.ID || string(lc.Status) != ChangeCheckCommented || len(f.requests(t, lc.ID)) != 0 {
		t.Errorf("comment-only reopen: %s %v", lc.Status, f.requests(t, lc.ID))
	}
}

// Point 1: a merged change is never reopened; a closed one neither, until it
// is opened again.
func TestReopen_MergedAndClosed(t *testing.T) {
	f, c := newApprovalCheck(t, both)
	approveBoth(t, f, c)
	if _, err := f.app.RecordMerge(f.ops, MergeInput{Repository: "acme/app", Number: 30, HeadCommit: headA, MergeCommit: headB}); err != nil {
		t.Fatal(err)
	}
	closed, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 32, HeadCommit: headA, Paths: approvalPaths})
	if err != nil {
		t.Fatal(err)
	}
	res, err := f.app.RecordClose(f.ops, CloseInput{Repository: "acme/app", Number: 32, HeadCommit: headA})
	if err != nil || !res.Covered || res.Check.ID != closed.Check.ID {
		t.Fatalf("close: %+v %v", res, err)
	}
	for area, r := range f.requests(t, closed.Check.ID) {
		if r[1] != signalWithdrawn {
			t.Errorf("request of %s after the close: %v", area, r)
		}
	}
	if string(f.liveCheck(t, 32).Status) != ChangeCheckAwaitingApproval {
		t.Error("the close moved the Check's state")
	}
	if _, err := f.app.RetireDecision(human("u-author"), f.dArea, "gone", nil); err != nil {
		t.Fatal(err)
	}
	if f.liveCheck(t, 30).ID != c.ID || f.liveCheck(t, 32).ID != closed.Check.ID {
		t.Fatal("a merged or closed change was reopened")
	}
	if r, err := f.app.RecheckChangeChecks(context.Background()); err != nil || r.Reopened != 0 {
		t.Errorf("sweep: %+v %v", r, err)
	}
	// Opened again: its Decisions changed meanwhile, so it is checked again.
	again, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 32, HeadCommit: headA, Paths: approvalPaths})
	if err != nil || again.Check.ID == closed.Check.ID || string(again.Check.Status) != ChangeCheckAwaitingApproval {
		t.Fatalf("reopened change: %v %v", again.Check, err)
	}
	// A merge is final: a merged change stays ended.
	if _, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 30, HeadCommit: headA, Paths: approvalPaths}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_change_ends WHERE number = 30 AND how = 'merged'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("merged row: %d %v", n, err)
	}
}

// A closed change opened again with nothing changed keeps its answers and is
// asked again only where its requests were withdrawn.
func TestReopen_ClosedThenOpenedNothingChanged(t *testing.T) {
	f, c := newApprovalCheck(t, both)
	if err := f.app.AnswerChangeCheck(human("u-author"), c.Address, f.a1.Address, true, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.RecordClose(f.ops, CloseInput{Repository: "acme/app", Number: 30, HeadCommit: headA}); err != nil {
		t.Fatal(err)
	}
	res, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 30, HeadCommit: headA, Paths: approvalPaths})
	if err != nil || res.Check.ID != c.ID {
		t.Fatalf("opened again: %v %v", res.Check, err)
	}
	reqs := f.requests(t, c.ID)
	if reqs[f.a1.Address][1] != signalAnswered || reqs[f.a2.Address][1] != signalOpen {
		t.Errorf("requests after reopen: %v", reqs)
	}
	if f.status(t, c.ID) != ChangeCheckAwaitingApproval {
		t.Errorf("status %s", f.status(t, c.ID))
	}
}

// RecordClose refuses what RecordMerge refuses, and a change with no Check is
// still recorded as closed.
func TestRecordClose_Refusals(t *testing.T) {
	f := newChangeCheckFixture(t)
	if _, err := f.app.RecordClose(f.ops, CloseInput{Repository: "acme/app", Number: 0, HeadCommit: headA}); err == nil || !strings.Contains(err.Error(), "number") {
		t.Errorf("number 0: %v", err)
	}
	if _, err := f.app.RecordClose(f.reader, CloseInput{Repository: "acme/app", Number: 5, HeadCommit: headA}); !errors.Is(err, ErrForbidden) {
		t.Errorf("no capability: %v", err)
	}
	res, err := f.app.RecordClose(f.ops, CloseInput{Repository: "acme/app", Number: 5, HeadCommit: headA})
	if err != nil || res.Covered {
		t.Errorf("no Check: %+v %v", res, err)
	}
	if f.count(t, "smeldr_change_ends") != 1 {
		t.Error("the close was not recorded")
	}
	if _, err := (&App{}).RecordClose(f.ops, CloseInput{Repository: "acme/app", Number: 5, HeadCommit: headA}); !errors.Is(err, ErrInternal) {
		t.Errorf("no database: %v", err)
	}
}

// Point 2: a Decision moved from one Area to the other reopens the open
// Checks of both; one placed in a Domain reaches its Areas.
func TestReopen_MovedAndPlaced(t *testing.T) {
	f := newChangeCheckFixture(t)
	ctx := context.Background()
	one, _ := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 40, HeadCommit: headA, Paths: []string{"internal/billing/x.go"}})
	two, _ := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 41, HeadCommit: headA, Paths: []string{"cmd/x/main.go"}})
	if _, err := f.db.Exec(`UPDATE smeldr_relations SET invalid_at = $1 WHERE source_id = $2 AND relation_kind = 'belongs_to_area'`, time.Now().UTC().Add(-time.Second), f.dArea); err != nil {
		t.Fatal(err)
	}
	placeEdge(t, f.db, f.dArea, "Area", f.a2.ID, "belongs_to_area", "asserted", nil)
	r, err := f.app.RecheckChangeChecks(ctx)
	if err != nil || r.Reopened != 2 || r.Failed != 0 {
		t.Fatalf("move: %+v %v", r, err)
	}
	if f.against(t, f.liveCheck(t, 40).ID)[f.dArea] || !f.against(t, f.liveCheck(t, 41).ID)[f.dArea] {
		t.Error("the Checks do not follow the move")
	}
	_ = one
	_ = two
	// Inline: a ratified Decision placed in the Domain through the relation
	// store reaches both Areas at once.
	if _, err := f.db.Exec(`INSERT INTO smeldr_decisions (id, slug, status, title, created_at, updated_at, address, address_no)
		VALUES ('dec-new', 'dec-new', 'ratified', 'New', datetime('now'), datetime('now'), 'D990', 990)`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO smeldr_standing (subject_type, subject_id, standing) VALUES ('Decision', 'dec-new', 'holds')`); err != nil {
		t.Fatal(err)
	}
	if err := f.app.relationStore.Assert(TrustedRead(authorCtx()), RelationEdge{SourceType: "Decision", SourceID: "dec-new", TargetType: "Domain", TargetID: f.domain,
		RelationKind: "belongs_to_domain", EdgeClass: "asserted", Attributes: []byte("{}")}); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{40, 41} {
		if !f.against(t, f.liveCheck(t, n).ID)["dec-new"] {
			t.Errorf("#%d not reopened by the Domain placement", n)
		}
	}
}

// Point 3 of revision 2: the comparison is quiet when nothing changed.
func TestRecheck_QuietAfterCheckChange(t *testing.T) {
	f := newChangeCheckFixture(t)
	// The Billing Area carries a Decision in the Area, the Domain and the
	// Organization at once.
	c, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 50, HeadCommit: headA, Paths: []string{"internal/billing/x.go"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{f.dArea, f.dDomain, f.dOrg} {
		if !f.against(t, c.Check.ID)[d] {
			t.Fatalf("not checked against %s", d)
		}
	}
	for _, place := range []struct{ name, paths string }{{"area", "internal/billing/y.go"}, {"domain only", "cmd/y/main.go"}} {
		if _, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 51, HeadCommit: headA, Paths: []string{place.paths}}); err != nil {
			t.Fatal(err)
		}
	}
	// Another caller asking without the Organization changes nothing.
	if _, err := f.app.DecisionsForPaths(f.ops, DecisionsForPathsQuery{Repository: "acme/app", Paths: []string{"internal/billing/x.go"}, ExcludeOrganization: true}); err != nil {
		t.Fatal(err)
	}
	tables := []string{"smeldr_change_checks", "smeldr_relations", "smeldr_signals", "smeldr_provenance"}
	before := map[string]int{}
	for _, tbl := range tables {
		before[tbl] = f.count(t, tbl)
	}
	for i := 0; i < 2; i++ {
		r, err := f.app.RecheckChangeChecks(context.Background())
		if err != nil || r.Reopened != 0 || r.Failed != 0 || r.Checked != 2 {
			t.Fatalf("sweep %d: %+v %v", i, r, err)
		}
	}
	// A sweep run as a caller who reads one Area gives the same.
	if r, err := f.app.RecheckChangeChecks(f.reader); err != nil || r.Reopened != 0 {
		t.Errorf("as a reader: %+v %v", r, err)
	}
	for _, tbl := range tables {
		if got := f.count(t, tbl); got != before[tbl] {
			t.Errorf("%s: %d rows, was %d", tbl, got, before[tbl])
		}
	}
}

// A database with Checks from before the reopen rule gets the columns and
// the round index; its old Checks are never reopened.
func TestRecheck_MigrationNoReopen(t *testing.T) {
	f := newChangeCheckFixture(t)
	ctx := context.Background()
	c, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 60, HeadCommit: headA, Paths: []string{"internal/billing/x.go"}})
	if err != nil {
		t.Fatal(err)
	}
	// Make it a Check from before: the table as it was, the old index.
	for _, stmt := range []string{
		`UPDATE smeldr_change_checks SET reopenable = FALSE`,
		`DROP INDEX idx_smeldr_change_checks_round`,
		`CREATE UNIQUE INDEX idx_smeldr_change_checks_commit ON smeldr_change_checks (repository, number, head_commit)`,
	} {
		if _, err := f.db.Exec(stmt); err != nil {
			t.Fatal(stmt, err)
		}
	}
	if err := EnsureChangeCheckTables(ctx, f.db); err != nil {
		t.Fatal(err)
	}
	var idx int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_smeldr_change_checks_commit'`).Scan(&idx); err != nil || idx != 0 {
		t.Errorf("old index: %d %v", idx, err)
	}
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_smeldr_change_checks_round'`).Scan(&idx); err != nil || idx != 1 {
		t.Errorf("round index: %d %v", idx, err)
	}
	if r, err := f.app.RecheckChangeChecks(ctx); err != nil || r.Checked != 0 {
		t.Errorf("first sweep: %+v %v", r, err)
	}
	if _, err := f.app.RetireDecision(human("u-author"), f.dArea, "gone", nil); err != nil {
		t.Fatal(err)
	}
	if f.liveCheck(t, 60).ID != c.Check.ID {
		t.Error("a Check from before the rule was reopened")
	}
	// A Check made after it is.
	n, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 61, HeadCommit: headA, Paths: []string{"internal/billing/x.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE smeldr_standing SET standing = 'ceased' WHERE subject_id = $1`, f.dOrg); err != nil {
		t.Fatal(err)
	}
	if r, _ := f.app.RecheckChangeChecks(ctx); r.Reopened != 1 || f.liveCheck(t, 61).ID == n.Check.ID || f.liveCheck(t, 60).ID != c.Check.ID {
		t.Errorf("only the new Check is reopened: %+v", r)
	}
}

// Fail closed: a reopen that fails at the insert leaves the old Check as it
// was and the next sweep reopens it; one that fails at the supersede leaves
// two live Checks, the newest waiting, repaired by the next CheckChange.
func TestReopen_FailClosed(t *testing.T) {
	f, c := newApprovalCheck(t, both)
	approveBoth(t, f, c)
	if _, err := f.db.Exec(`CREATE TRIGGER fail_areas BEFORE INSERT ON smeldr_change_check_areas BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.RetireDecision(human("u-author"), f.dArea, "gone", nil); err != nil {
		t.Fatal(err)
	}
	if f.liveCheck(t, 30).ID != c.ID || f.status(t, c.ID) != ChangeCheckApproved {
		t.Fatal("the failed reopen changed the Check")
	}
	if r, err := f.app.RecheckChangeChecks(context.Background()); err != nil || r.Failed != 1 {
		t.Errorf("sweep with the failure: %+v %v", r, err)
	}
	if _, err := f.db.Exec(`DROP TRIGGER fail_areas`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`CREATE TRIGGER fail_supersede BEFORE UPDATE ON smeldr_change_checks WHEN NEW.status = 'superseded' BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	// The new Check is made and waiting; the supersede fails, so the run
	// counts it as failed.
	if r, _ := f.app.RecheckChangeChecks(context.Background()); r.Failed != 1 {
		t.Errorf("sweep with the supersede failing: %+v", r)
	}
	if f.status(t, c.ID) != ChangeCheckApproved || string(f.liveCheck(t, 30).Status) != ChangeCheckAwaitingApproval {
		t.Error("the newest live Check is not the waiting one")
	}
	if _, err := f.db.Exec(`DROP TRIGGER fail_supersede`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 30, HeadCommit: headA, Paths: approvalPaths}); err != nil {
		t.Fatal(err)
	}
	if f.status(t, c.ID) != ChangeCheckSuperseded || f.liveChecks(t, 30) != 1 {
		t.Errorf("not repaired: %s, %d live", f.status(t, c.ID), f.liveChecks(t, 30))
	}
}

// A second reopen of the same Check makes nothing: one new Check per change
// in force.
func TestReopen_OnceOnly(t *testing.T) {
	f, c := newApprovalCheck(t, both)
	areas := []string{f.a1.ID, f.a2.ID}
	if ok, err := f.app.reopenChangeCheck(context.Background(), c, areas, nil, false); err != nil || !ok {
		t.Fatalf("first: %v %v", ok, err)
	}
	if ok, err := f.app.reopenChangeCheck(context.Background(), c, areas, nil, false); err != nil || ok {
		t.Errorf("second: %v %v", ok, err)
	}
	if f.count(t, "smeldr_change_checks") != 2 {
		t.Errorf("%d Checks", f.count(t, "smeldr_change_checks"))
	}
}

// The error paths of the re-check.
func TestRecheck_Failures(t *testing.T) {
	f, c := newApprovalCheck(t, both)
	ctx := context.Background()
	if _, err := (&App{}).RecheckChangeChecks(ctx); !errors.Is(err, ErrInternal) {
		t.Errorf("no database: %v", err)
	}
	(&App{}).recheckForDecision(ctx, "x") // no database: nothing
	if _, err := f.db.Exec(`ALTER TABLE smeldr_change_check_areas RENAME TO cca`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.recheckChangeCheck(ctx, c, nil); !errors.Is(err, ErrInternal) {
		t.Errorf("Areas unreadable: %v", err)
	}
	if _, err := f.app.reopenChangeCheck(ctx, c, []string{f.a1.ID}, nil, false); err == nil {
		t.Error("reopen with no Area table")
	}
	if _, err := f.db.Exec(`ALTER TABLE cca RENAME TO smeldr_change_check_areas`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`ALTER TABLE smeldr_change_ends RENAME TO ends`); err != nil {
		t.Fatal(err)
	}
	if r, err := f.app.RecheckChangeChecks(ctx); err != nil || r.Checked != 0 {
		t.Errorf("no ends table: %+v %v", r, err)
	}
	if _, err := f.app.RecordClose(f.ops, CloseInput{Repository: "acme/app", Number: 30, HeadCommit: headA}); !errors.Is(err, ErrInternal) {
		t.Errorf("close with no table: %v", err)
	}
	if _, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 30, HeadCommit: headA, Paths: approvalPaths}); !errors.Is(err, ErrInternal) {
		t.Errorf("check with no ends table: %v", err)
	}
	if _, err := f.app.RecordMerge(f.ops, MergeInput{Repository: "acme/app", Number: 30, HeadCommit: headA, MergeCommit: headB}); !errors.Is(err, ErrInternal) {
		t.Errorf("merge with no ends table: %v", err)
	}
	f.app.recheckForDecision(ctx, f.dArea) // logged, nothing else
	if _, err := f.db.Exec(`ALTER TABLE ends RENAME TO smeldr_change_ends`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`ALTER TABLE smeldr_relations RENAME TO rels`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.recheckChangeCheck(ctx, c, nil); !errors.Is(err, ErrInternal) {
		t.Errorf("relations unreadable: %v", err)
	}
	f.app.recheckForDecision(ctx, f.dArea) // place unreadable: logged
	if _, err := f.db.Exec(`ALTER TABLE rels RENAME TO smeldr_relations`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`ALTER TABLE smeldr_areas RENAME TO areas`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.reopenChangeCheck(ctx, c, []string{f.a1.ID}, nil, false); !errors.Is(err, ErrInternal) {
		t.Errorf("Area modes unreadable: %v", err)
	}
	if _, err := f.db.Exec(`ALTER TABLE areas RENAME TO smeldr_areas`); err != nil {
		t.Fatal(err)
	}
}

// The new column is not part of the record's shape.
func TestChangeCheck_RoundNotInShape(t *testing.T) {
	f, c := newApprovalCheck(t, both)
	b, _ := json.Marshal(c)
	for _, k := range []string{"round", "reopenable"} {
		if strings.Contains(string(b), `"`+k+`"`) {
			t.Errorf("%s in the Check's JSON: %s", k, b)
		}
	}
	view, err := f.app.ChangeCheckOf(f.ops, c.Address)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(view)
	if strings.Contains(string(b), `"round"`) {
		t.Errorf("round in the view: %s", b)
	}
}

// The failure paths a re-check and a close meet, injected where they would
// happen.
func TestRecheck_InjectedFailures(t *testing.T) {
	ctx := context.Background()
	trigger := func(t *testing.T, f changeCheckFixture, sql string) func() {
		t.Helper()
		if _, err := f.db.Exec(`CREATE TRIGGER inject ` + sql + ` BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
			t.Fatal(err)
		}
		return func() {
			if _, err := f.db.Exec(`DROP TRIGGER inject`); err != nil {
				t.Fatal(err)
			}
		}
	}
	reopen := func(t *testing.T, f changeCheckFixture, c *ChangeCheck) error {
		t.Helper()
		_, err := f.app.reopenChangeCheck(ctx, c, []string{f.a1.ID, f.a2.ID}, []AppliedDecision{{ID: f.dOrg}}, false)
		return err
	}
	for _, tc := range []struct {
		name, sql string
	}{
		{"insert of the Check", `BEFORE INSERT ON smeldr_change_checks`},
		{"Area edge", `BEFORE INSERT ON smeldr_relations WHEN NEW.relation_kind = 'belongs_to_area'`},
		{"checked_against edge", `BEFORE INSERT ON smeldr_relations WHEN NEW.relation_kind = 'checked_against'`},
		{"settle", `BEFORE UPDATE ON smeldr_change_checks WHEN NEW.status = 'awaiting-approval'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, c := newApprovalCheck(t, both)
			undo := trigger(t, f, tc.sql)
			defer undo()
			if err := reopen(t, f, c); err == nil {
				t.Error("no error")
			}
		})
	}
	t.Run("round unreadable", func(t *testing.T) {
		f, c := newApprovalCheck(t, both)
		ghost := *c
		ghost.ID = "no-such-check"
		if err := reopen(t, f, &ghost); !errors.Is(err, ErrInternal) {
			t.Errorf("%v", err)
		}
	})
	t.Run("in force unreadable", func(t *testing.T) {
		f, c := newApprovalCheck(t, both)
		if _, err := f.db.Exec(`ALTER TABLE smeldr_domains RENAME TO doms`); err != nil {
			t.Fatal(err)
		}
		if _, err := f.app.recheckChangeCheck(ctx, c, map[string][]AppliedDecision{}); err == nil {
			t.Error("no error")
		}
	})
	t.Run("a Check with no Area", func(t *testing.T) {
		f, c := newApprovalCheck(t, both)
		if _, err := f.db.Exec(`DELETE FROM smeldr_change_check_areas WHERE check_id = $1`, c.ID); err != nil {
			t.Fatal(err)
		}
		if ok, err := f.app.recheckChangeCheck(ctx, c, nil); ok || err != nil {
			t.Errorf("%v %v", ok, err)
		}
	})
	t.Run("open Checks unreadable", func(t *testing.T) {
		f, _ := newApprovalCheck(t, both)
		if _, err := f.db.Exec(`ALTER TABLE smeldr_change_checks DROP COLUMN reopenable`); err != nil {
			t.Fatal(err)
		}
		if _, err := f.app.RecheckChangeChecks(ctx); !errors.Is(err, ErrInternal) {
			t.Errorf("sweep: %v", err)
		}
		f.app.recheckForDecision(ctx, f.dArea) // logged
	})
	t.Run("close: withdraw and live Check fail", func(t *testing.T) {
		f, _ := newApprovalCheck(t, both)
		undo := trigger(t, f, `BEFORE UPDATE ON smeldr_signals`)
		if _, err := f.app.RecordClose(f.ops, CloseInput{Repository: "acme/app", Number: 30, HeadCommit: headA}); err == nil {
			t.Error("withdraw failure: no error")
		}
		undo()
		if _, err := f.db.Exec(`ALTER TABLE smeldr_change_checks RENAME TO ccs`); err != nil {
			t.Fatal(err)
		}
		if _, err := f.app.RecordClose(f.ops, CloseInput{Repository: "acme/app", Number: 31, HeadCommit: headA}); !errors.Is(err, ErrInternal) {
			t.Errorf("live Check unreadable: %v", err)
		}
	})
	t.Run("reopened change: ask again fails", func(t *testing.T) {
		f, _ := newApprovalCheck(t, both)
		if _, err := f.app.RecordClose(f.ops, CloseInput{Repository: "acme/app", Number: 30, HeadCommit: headA}); err != nil {
			t.Fatal(err)
		}
		undo := trigger(t, f, `BEFORE UPDATE ON smeldr_change_check_areas`)
		defer undo()
		if _, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 30, HeadCommit: headA, Paths: approvalPaths}); !errors.Is(err, ErrInternal) {
			t.Errorf("%v", err)
		}
	})
	t.Run("reopened change: re-check fails", func(t *testing.T) {
		f, c := newApprovalCheck(t, both)
		if _, err := f.app.RecordClose(f.ops, CloseInput{Repository: "acme/app", Number: 30, HeadCommit: headA}); err != nil {
			t.Fatal(err)
		}
		sys := WithReadScope(ctx, AllReadScope())
		if _, err := f.app.afterChangeReopened(sys, &ChangeCheck{Node: Node{ID: c.ID, Status: ChangeCheckSuperseded}}); err != nil {
			t.Errorf("superseded: %v", err)
		}
		if _, err := f.db.Exec(`ALTER TABLE smeldr_relations RENAME TO rels`); err != nil {
			t.Fatal(err)
		}
		if _, err := f.app.afterChangeReopened(sys, c); err == nil {
			t.Error("no error")
		}
	})
}

// More open Checks than the inline bound: the rest are left to the sweep.
func TestRecheck_InlineLimit(t *testing.T) {
	f := newChangeCheckFixture(t)
	for n := 70; n < 73; n++ {
		if _, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: n, HeadCommit: headA, Paths: []string{"internal/billing/x.go"}}); err != nil {
			t.Fatal(err)
		}
	}
	old := recheckInlineLimit
	recheckInlineLimit = 1
	defer func() { recheckInlineLimit = old }()
	if _, err := f.app.RetireDecision(human("u-author"), f.dArea, "gone", nil); err != nil {
		t.Fatal(err)
	}
	if r, err := f.app.RecheckChangeChecks(context.Background()); err != nil || r.Reopened != 2 {
		t.Errorf("the sweep did the rest: %+v %v", r, err)
	}
}

func TestSameDecisions(t *testing.T) {
	if sameDecisions([]string{"a", "b"}, []AppliedDecision{{ID: "a"}, {ID: "c"}}) {
		t.Error("a,b equal to a,c")
	}
	if !sameDecisions([]string{"b", "a"}, []AppliedDecision{{ID: "a"}, {ID: "b"}}) {
		t.Error("order matters")
	}
}

// The migration's own failures name EnsureChangeCheckTables.
func TestEnsureChangeCheckTables_Migration(t *testing.T) {
	db := newSQLiteDB(t)
	if _, err := db.Exec(`CREATE TABLE smeldr_change_checks (id TEXT PRIMARY KEY, slug TEXT NOT NULL UNIQUE, status TEXT NOT NULL DEFAULT 'checking',
		published_at TIMESTAMPTZ, scheduled_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL, rev INTEGER NOT NULL DEFAULT 0,
		repository TEXT NOT NULL DEFAULT '', number INTEGER NOT NULL DEFAULT 0, head_commit TEXT NOT NULL DEFAULT '', files_total INTEGER NOT NULL DEFAULT 0,
		files_checked INTEGER NOT NULL DEFAULT 0, checked_partly BOOLEAN NOT NULL DEFAULT FALSE, setting TEXT NOT NULL DEFAULT '', last_actor TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	for _, failOn := range []string{"ADD COLUMN", "idx_smeldr_change_checks_round", "DROP INDEX"} {
		if err := EnsureChangeCheckTables(context.Background(), &execFailDB{DB: db, failOn: failOn}); err == nil || !strings.Contains(err.Error(), "EnsureChangeCheckTables") {
			t.Errorf("failing %s: %v", failOn, err)
		}
	}
}

// The hole the review found: a Decision deleted while an approved Check was
// checked against it, with the structural sweep (which ends edges to a
// deleted record) run before the re-check. The Check still reopens.
func TestReopen_DeletedDecisionAfterStructuralSweep(t *testing.T) {
	f, c := newApprovalCheck(t, both)
	approveBoth(t, f, c)
	ctx := context.Background()
	for _, stmt := range []string{
		`DELETE FROM smeldr_decisions WHERE id = 'dec-area'`,
		`DELETE FROM smeldr_standing WHERE subject_id = 'dec-area'`,
	} {
		if _, err := f.db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, _, err := f.app.SweepStructural(ctx); err != nil {
		t.Fatal(err)
	}
	if f.against(t, c.ID)[f.dArea] {
		t.Fatal("the structural sweep did not end the edge to the deleted Decision; the test does not show the case")
	}
	r, err := f.app.RecheckChangeChecks(ctx)
	if err != nil || r.Reopened != 1 {
		t.Fatalf("after the sweep: %+v %v", r, err)
	}
	live := f.liveCheck(t, 30)
	if live.ID == c.ID || string(live.Status) != ChangeCheckAwaitingApproval {
		t.Errorf("not reopened: %s %s", live.Address, live.Status)
	}
	// Quiet afterwards: the new Check has no ended edge.
	if r, err := f.app.RecheckChangeChecks(ctx); err != nil || r.Reopened != 0 {
		t.Errorf("second sweep: %+v %v", r, err)
	}
}

// A touched Area archived (or gone) while a Check touches it: the new Check
// leaves it out and says why, naming no Area.
func TestReopen_AreaArchived(t *testing.T) {
	f, c := newApprovalCheck(t, both)
	approveBoth(t, f, c)
	if _, err := f.db.Exec(`UPDATE smeldr_areas SET status = 'archived' WHERE id = $1`, f.a2.ID); err != nil {
		t.Fatal(err)
	}
	r, err := f.app.RecheckChangeChecks(context.Background())
	if err != nil || r.Reopened != 1 {
		t.Fatalf("sweep: %+v %v", r, err)
	}
	live := f.liveCheck(t, 30)
	areas, err := queryStrings(context.Background(), f.db, `SELECT area_id FROM smeldr_change_check_areas WHERE check_id = $1`, live.ID)
	if err != nil || len(areas) != 1 || areas[0] != f.a1.ID {
		t.Errorf("new Check's Areas: %v %v", areas, err)
	}
	if string(live.Status) != ChangeCheckAwaitingApproval || len(f.requests(t, live.ID)) != 1 {
		t.Errorf("new Check: %s %v", live.Status, f.requests(t, live.ID))
	}
	reason := f.reason(t, c.ID, ChangeCheckSuperseded)
	if reason != "checked again as "+live.Address+": a touched Area is gone or archived" || strings.Contains(reason, f.a2.Address) {
		t.Errorf("reason %q", reason)
	}
	// Every touched Area gone: a Check with none, commented.
	if _, err := f.db.Exec(`UPDATE smeldr_areas SET status = 'archived' WHERE id = $1`, f.a1.ID); err != nil {
		t.Fatal(err)
	}
	if r, err := f.app.RecheckChangeChecks(context.Background()); err != nil || r.Reopened != 1 {
		t.Fatalf("second sweep: %+v %v", r, err)
	}
	if last := f.liveCheck(t, 30); string(last.Status) != ChangeCheckCommented || len(f.requests(t, last.ID)) != 0 {
		t.Errorf("no Area left: %s %v", last.Status, f.requests(t, last.ID))
	}
}
