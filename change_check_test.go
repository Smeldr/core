package smeldr

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// T918 part 1: the ChangeCheck type and its resolution.

const (
	headA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	headB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// changeCheckFixture is the coverage fixture with an operator who holds
// check_change (an editor grant) and a reader of the first Area only.
type changeCheckFixture struct {
	coverageFixture
	ops, reader Context
}

func newChangeCheckFixture(t *testing.T) changeCheckFixture {
	t.Helper()
	f := changeCheckFixture{coverageFixture: newCoverageFixture(t)}
	ctx := context.Background()
	if err := RegisterAreaReaderRole(ctx, f.rs); err != nil {
		t.Fatal(err)
	}
	for _, g := range []RoleGrant{
		{TokenID: "ops-1", RoleName: "editor"},
		{TokenID: "reader-a", RoleName: "area-reader", ScopeAnchorID: f.a1.ID},
	} {
		if _, err := f.rs.Grant(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	f.ops = NewTestContext(User{ID: "ops-1", Roles: []Role{Editor}})
	f.reader = NewTestContext(User{ID: "reader-a", Roles: []Role{Author}})
	return f
}

func (f changeCheckFixture) count(t *testing.T, table string) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (f changeCheckFixture) edges(t *testing.T, source, kind string) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_relations WHERE source_id = $1 AND relation_kind = $2 AND invalid_at IS NULL`, source, kind).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCheckChange_Invalid(t *testing.T) {
	f := newChangeCheckFixture(t)
	many := make([]string, DecisionsForPathsMaxPaths+1)
	for i := range many {
		many[i] = "a.go"
	}
	for _, tc := range []struct {
		name string
		in   ChangeInput
		want string
	}{
		{"repository", ChangeInput{Repository: "acme", Number: 1, HeadCommit: headA}, "repository"},
		{"number", ChangeInput{Repository: "acme/app", Number: 0, HeadCommit: headA}, "number"},
		{"head commit", ChangeInput{Repository: "acme/app", Number: 1, HeadCommit: "main"}, "head_commit"},
		{"too many paths", ChangeInput{Repository: "acme/app", Number: 1, HeadCommit: headA, Paths: many}, "at most 3000 paths"},
		{"files total", ChangeInput{Repository: "acme/app", Number: 1, HeadCommit: headA, Paths: []string{"a", "b"}, FilesTotal: 1}, "files_total"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.app.CheckChange(f.ops, tc.in); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
	// The caller needs check_change; a bare context is no caller.
	in := ChangeInput{Repository: "acme/app", Number: 1, HeadCommit: headA, Paths: []string{"internal/billing/a.go"}}
	if _, err := f.app.CheckChange(f.reader, in); !errors.Is(err, ErrForbidden) {
		t.Errorf("reader: %v, want forbidden", err)
	}
	if _, err := f.app.CheckChange(context.Background(), in); !errors.Is(err, ErrForbidden) {
		t.Errorf("no caller: %v, want forbidden", err)
	}
	if f.count(t, "smeldr_change_checks") != 0 {
		t.Error("a refused check wrote a Check")
	}
}

func TestCheckChange_Uncovered(t *testing.T) {
	f := newChangeCheckFixture(t)
	res, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "Acme/App", Number: 7, HeadCommit: strings.ToUpper(headA), Paths: []string{"docs/x.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Covered || res.Check != nil || strings.Join(res.Uncovered, ",") != "docs/x.md" {
		t.Errorf("result = %+v", res)
	}
	if f.count(t, "smeldr_change_checks") != 0 {
		t.Error("an uncovered change made a Check (D143)")
	}
}

func TestCheckChange_CommentOnly(t *testing.T) {
	f := newChangeCheckFixture(t)
	res, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 7, HeadCommit: headA, Paths: []string{"internal/billing/a.go", "docs/x.md"}})
	if err != nil {
		t.Fatal(err)
	}
	c := res.Check
	if !res.Covered || c == nil || string(c.Status) != ChangeCheckCommented || c.Setting != ApprovalModeCommentOnly ||
		c.FilesChecked != 2 || c.FilesTotal != 2 || c.CheckedPartly || !strings.HasPrefix(c.Address, "CHK") {
		t.Fatalf("check = %+v", c)
	}
	if len(res.Areas) != 1 || res.Areas[0].ID != f.a1.ID || res.Areas[0].Verdict != AreaVerdictCommented || strings.Join(res.Areas[0].Paths, ",") != "internal/billing/a.go" {
		t.Errorf("areas = %+v", res.Areas)
	}
	if len(res.Decisions) != 3 || strings.Join(res.Uncovered, ",") != "docs/x.md" {
		t.Errorf("decisions = %+v, uncovered %v", res.Decisions, res.Uncovered)
	}
	if f.edges(t, c.ID, "belongs_to_area") != 1 || f.edges(t, c.ID, "checked_against") != 3 {
		t.Errorf("edges: %d places, %d decisions", f.edges(t, c.ID, "belongs_to_area"), f.edges(t, c.ID, "checked_against"))
	}
}

func TestCheckChange_RequiresApproval(t *testing.T) {
	f := newChangeCheckFixture(t)
	if _, err := f.db.Exec(`UPDATE smeldr_areas SET approval_mode = 'requires-approval' WHERE id = $1`, f.a2.ID); err != nil {
		t.Fatal(err)
	}
	res, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 8, HeadCommit: headA,
		Paths: []string{"cmd/x/main.go", "internal/billing/a.go"}, FilesTotal: 5000})
	if err != nil {
		t.Fatal(err)
	}
	c := res.Check
	if string(c.Status) != ChangeCheckAwaitingApproval || c.Setting != ApprovalModeRequiresApproval || !c.CheckedPartly || c.FilesTotal != 5000 {
		t.Errorf("check = %+v", c)
	}
	verdicts := map[string]string{}
	for _, a := range res.Areas {
		verdicts[a.ID] = a.Verdict
	}
	if verdicts[f.a1.ID] != AreaVerdictCommented || verdicts[f.a2.ID] != AreaVerdictAwaiting {
		t.Errorf("verdicts = %v", verdicts)
	}
}

func TestCheckChange_IdempotentAndSuperseded(t *testing.T) {
	f := newChangeCheckFixture(t)
	in := ChangeInput{Repository: "acme/app", Number: 9, HeadCommit: headA, Paths: []string{"internal/billing/a.go"}}
	first, err := f.app.CheckChange(f.ops, in)
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.app.CheckChange(f.ops, in)
	if err != nil || again.Check.ID != first.Check.ID || f.count(t, "smeldr_change_checks") != 1 {
		t.Fatalf("redelivery: %v, %v", again.Check, err)
	}
	// Two at once: one Check.
	var wg sync.WaitGroup
	ids := make([]string, 4)
	same := in
	same.HeadCommit = headB
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if res, err := f.app.CheckChange(f.ops, same); err == nil {
				ids[i] = res.Check.ID
			} else {
				t.Errorf("concurrent: %v", err)
			}
		}(i)
	}
	wg.Wait()
	for _, id := range ids[1:] {
		if id != ids[0] {
			t.Errorf("concurrent checks made %v", ids)
		}
	}
	if f.count(t, "smeldr_change_checks") != 2 {
		t.Errorf("checks = %d, want 2", f.count(t, "smeldr_change_checks"))
	}
	// The newer commit superseded the first.
	old, err := f.app.ChangeCheckOf(f.ops, first.Check.Address)
	if err != nil || string(old.Check.Status) != ChangeCheckSuperseded {
		t.Errorf("first after a newer commit: %+v, %v", old.Check, err)
	}
	if f.edges(t, ids[0], "supersedes") != 1 {
		t.Error("no supersedes edge from the newer Check")
	}
}

func TestChangeCheckOf_ReadRules(t *testing.T) {
	f := newChangeCheckFixture(t)
	if _, err := f.db.Exec(`UPDATE smeldr_areas SET approval_mode = 'requires-approval' WHERE id = $1`, f.a2.ID); err != nil {
		t.Fatal(err)
	}
	res, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 10, HeadCommit: headA, Paths: []string{"cmd/x/main.go", "internal/billing/a.go"}})
	if err != nil {
		t.Fatal(err)
	}
	// The operator sees all of it.
	full, err := f.app.ChangeCheckOf(f.ops, res.Check.Slug)
	if err != nil || len(full.Areas) != 2 || len(full.Decisions) != 3 {
		t.Fatalf("operator view: %+v, %v", full, err)
	}
	// A reader of the first Area sees that Area and the Decision placed in it.
	view, err := f.app.ChangeCheckOf(f.reader, res.Check.Address)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Areas) != 1 || view.Areas[0].ID != f.a1.ID || len(view.Decisions) != 1 || view.Decisions[0].ID != f.dArea {
		t.Errorf("reader view: areas %+v, decisions %+v", view.Areas, view.Decisions)
	}
	// Someone who reads neither Area does not see the Check.
	stranger := NewTestContext(User{ID: "nobody", Roles: []Role{Author}})
	if _, err := f.app.ChangeCheckOf(stranger, res.Check.Address); !errors.Is(err, ErrNotFound) {
		t.Errorf("stranger: %v, want not found", err)
	}
	if _, err := f.app.ChangeCheckOf(f.ops, "CHK9999"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown: %v", err)
	}
}

func TestChangeCheck_WritesRefused(t *testing.T) {
	f := newChangeCheckFixture(t)
	res, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 11, HeadCommit: headA, Paths: []string{"internal/billing/a.go"}})
	if err != nil {
		t.Fatal(err)
	}
	m := findModule[*ChangeCheck](t, f.app)
	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodPost, "/change-checks", bytes.NewReader([]byte(`{"repository":"x/y"}`))), User{ID: "ops-1", Roles: []Role{Editor}})
	m.createHandler(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("POST: %d, want 403", w.Code)
	}
	if err := m.MCPDelete(f.ops, res.Check.Slug); !errors.Is(err, ErrForbidden) {
		t.Errorf("delete: %v, want forbidden", err)
	}
}

func TestCheckChange_Failures(t *testing.T) {
	in := ChangeInput{Repository: "acme/app", Number: 12, HeadCommit: headA, Paths: []string{"internal/billing/a.go"}}
	for _, tc := range []struct{ name, stmt string }{
		{"resolution", `ALTER TABLE smeldr_coverage_rules RENAME TO rules_gone`},
		{"areas row", `ALTER TABLE smeldr_change_check_areas RENAME TO areas_gone`},
		{"existing check", `ALTER TABLE smeldr_change_checks RENAME TO checks_gone`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newChangeCheckFixture(t)
			if _, err := f.db.Exec(tc.stmt); err != nil {
				t.Fatal(err)
			}
			if _, err := f.app.CheckChange(f.ops, in); !errors.Is(err, ErrInternal) {
				t.Errorf("%v, want internal", err)
			}
		})
	}
	// A failed insert leaves nothing behind.
	f := newChangeCheckFixture(t)
	if _, err := f.db.Exec(`ALTER TABLE smeldr_change_check_areas RENAME TO areas_gone`); err != nil {
		t.Fatal(err)
	}
	_, _ = f.app.CheckChange(f.ops, in)
	if f.count(t, "smeldr_change_checks") != 0 {
		t.Error("a failed check left a Check")
	}
	if _, err := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")}).CheckChange(context.Background(), in); !errors.Is(err, ErrInternal) {
		t.Errorf("no database: %v", err)
	}
	if _, err := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")}).ChangeCheckOf(context.Background(), "x"); !errors.Is(err, ErrInternal) {
		t.Errorf("no database, read: %v", err)
	}
}

// The paths that need a broken store.
func TestCheckChange_BrokenStore(t *testing.T) {
	in := ChangeInput{Repository: "acme/app", Number: 13, HeadCommit: headA, Paths: []string{"internal/billing/a.go"}}
	// No address counter: the Check cannot be numbered, nothing is written.
	f := newChangeCheckFixture(t)
	if _, err := f.db.Exec(`DELETE FROM smeldr_address_counters WHERE type_name = 'ChangeCheck'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.CheckChange(f.ops, in); !errors.Is(err, ErrInternal) || f.count(t, "smeldr_change_checks") != 0 {
		t.Errorf("no counter: %v", err)
	}
	// A verdict that cannot be set: the flow lost its transitions.
	f = newChangeCheckFixture(t)
	if _, err := f.db.Exec(`DELETE FROM smeldr_transitions WHERE flow_id IN (SELECT id FROM smeldr_state_flows WHERE type_name = 'ChangeCheck')`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.CheckChange(f.ops, in); err == nil {
		t.Error("settle without transitions: no error")
	}
	// The Check stays in checking and a redelivery finishes it once the flow
	// is back.
	f.app.RegisterFlow(changeCheckFlow())
	res, err := f.app.CheckChange(f.ops, in)
	if err != nil || string(res.Check.Status) != ChangeCheckCommented {
		t.Errorf("redelivery settles: %+v, %v", res.Check, err)
	}
	// The Checks of a change that cannot be listed.
	next := in
	next.HeadCommit = headB
	if _, err := f.db.Exec(`CREATE TRIGGER no_edge BEFORE INSERT ON smeldr_relations WHEN NEW.relation_kind = 'supersedes' BEGIN SELECT RAISE(ABORT, 'no'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.CheckChange(f.ops, next); !errors.Is(err, ErrInternal) {
		t.Errorf("supersedes edge refused: %v", err)
	}
	if _, err := f.db.Exec(`DROP TRIGGER no_edge`); err != nil {
		t.Fatal(err)
	}
	// Reading a Check whose Areas or Decisions cannot be read.
	id := res.Check.ID
	if _, err := f.app.changeCheckView(f.ops, "no-such-check"); !errors.Is(err, ErrInternal) {
		t.Errorf("missing Check: %v", err)
	}
	if _, err := f.db.Exec(`ALTER TABLE smeldr_relations RENAME TO relations_gone`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.changeCheckView(TrustedRead(f.ops), id); !errors.Is(err, ErrInternal) {
		t.Errorf("relations gone: %v", err)
	}
	if _, err := f.db.Exec(`ALTER TABLE smeldr_areas RENAME TO areas_gone`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.changeCheckView(TrustedRead(f.ops), id); !errors.Is(err, ErrInternal) {
		t.Errorf("areas gone: %v", err)
	}
}

// Failures inside the write: each part of the transaction refused by a
// trigger leaves nothing behind.
func TestCheckChange_WriteRefused(t *testing.T) {
	in := ChangeInput{Repository: "acme/app", Number: 14, HeadCommit: headA, Paths: []string{"internal/billing/a.go"}}
	for _, tc := range []struct{ name, trigger string }{
		{"the Check", `CREATE TRIGGER no_check BEFORE INSERT ON smeldr_change_checks BEGIN SELECT RAISE(ABORT, 'no'); END`},
		{"its place", `CREATE TRIGGER no_place BEFORE INSERT ON smeldr_relations WHEN NEW.relation_kind = 'belongs_to_area' AND NEW.source_type = 'ChangeCheck' BEGIN SELECT RAISE(ABORT, 'no'); END`},
		{"its Decisions", `CREATE TRIGGER no_decision BEFORE INSERT ON smeldr_relations WHEN NEW.relation_kind = 'checked_against' BEGIN SELECT RAISE(ABORT, 'no'); END`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newChangeCheckFixture(t)
			if _, err := f.db.Exec(tc.trigger); err != nil {
				t.Fatal(err)
			}
			if _, err := f.app.CheckChange(f.ops, in); !errors.Is(err, ErrInternal) {
				t.Errorf("%v, want internal", err)
			}
			if f.count(t, "smeldr_change_checks") != 0 || f.count(t, "smeldr_change_check_areas") != 0 {
				t.Error("a refused write left rows")
			}
		})
	}
	// Losing the race to another call: the insert finds the row there and
	// returns that Check.
	f := newChangeCheckFixture(t)
	first, err := f.app.CheckChange(f.ops, in)
	if err != nil {
		t.Fatal(err)
	}
	res, err := f.app.DecisionsForPaths(TrustedRead(f.ops), DecisionsForPathsQuery{Repository: in.Repository, Paths: in.Paths})
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.app.insertChangeCheck(TrustedRead(f.ops), in, res)
	if err != nil || got.ID != first.Check.ID {
		t.Errorf("lost race: %v, %v", got, err)
	}
	// Read rules that cannot be loaded.
	if _, err := f.db.Exec(`ALTER TABLE smeldr_role_grants RENAME TO grants_gone`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.ChangeCheckOf(f.reader, first.Check.Address); err == nil {
		t.Error("ChangeCheckOf without grants: no error")
	}
	if _, err := f.app.changeCheckView(f.reader, first.Check.ID); err == nil {
		t.Error("changeCheckView without grants: no error")
	}
}

// liveChecks counts the Checks of a change that are not superseded.
func (f changeCheckFixture) liveChecks(t *testing.T, number int) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_change_checks WHERE number = $1 AND status <> 'superseded'`, number).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// One live Check per change (D143), whatever happened before the call.
func TestCheckChange_OneLiveCheck(t *testing.T) {
	// (a) The supersede fails once; a redelivery repairs it.
	f := newChangeCheckFixture(t)
	in := ChangeInput{Repository: "acme/app", Number: 20, HeadCommit: headA, Paths: []string{"internal/billing/a.go"}}
	if _, err := f.app.CheckChange(f.ops, in); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`CREATE TRIGGER no_supersede BEFORE UPDATE ON smeldr_change_checks WHEN NEW.status = 'superseded' BEGIN SELECT RAISE(ABORT, 'no'); END`); err != nil {
		t.Fatal(err)
	}
	next := in
	next.HeadCommit = headB
	if _, err := f.app.CheckChange(f.ops, next); err == nil {
		t.Fatal("a failed supersede: no error")
	}
	if f.liveChecks(t, 20) != 2 {
		t.Fatalf("setup: live = %d, want 2 after the failed supersede", f.liveChecks(t, 20))
	}
	if _, err := f.db.Exec(`DROP TRIGGER no_supersede`); err != nil {
		t.Fatal(err)
	}
	res, err := f.app.CheckChange(f.ops, next)
	if err != nil {
		t.Fatal(err)
	}
	if f.liveChecks(t, 20) != 1 || string(res.Check.Status) == ChangeCheckSuperseded {
		t.Errorf("after redelivery: live = %d, newest %s", f.liveChecks(t, 20), res.Check.Status)
	}

	// (b) Two head commits at once.
	g := newChangeCheckFixture(t)
	var wg sync.WaitGroup
	for _, head := range []string{headA, headB} {
		wg.Add(1)
		go func(head string) {
			defer wg.Done()
			c := in
			c.Number, c.HeadCommit = 21, head
			if _, err := g.app.CheckChange(g.ops, c); err != nil {
				t.Errorf("%s: %v", head[:4], err)
			}
		}(head)
	}
	wg.Wait()
	if g.liveChecks(t, 21) != 1 {
		t.Errorf("two heads at once: live = %d, want 1", g.liveChecks(t, 21))
	}
	var live, other string
	if err := g.db.QueryRow(`SELECT id FROM smeldr_change_checks WHERE number = 21 AND status <> 'superseded'`).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if err := g.db.QueryRow(`SELECT id FROM smeldr_change_checks WHERE number = 21 AND status = 'superseded'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	var edges int
	if err := g.db.QueryRow(`SELECT COUNT(*) FROM smeldr_relations WHERE source_id = $1 AND target_id = $2 AND relation_kind = 'supersedes'`, live, other).Scan(&edges); err != nil || edges != 1 {
		t.Errorf("supersedes edge from the live Check: %d, %v", edges, err)
	}
}

func TestEnsureChangeCheckTables(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateGovernanceTables(db); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := EnsureChangeCheckTables(context.Background(), db); err != nil {
			t.Fatal(err)
		}
	}
	if err := EnsureChangeCheckTables(context.Background(), &execFailDB{DB: db, failOn: "smeldr_change"}); err == nil {
		t.Error("failing database: no error")
	}
	if err := EnsureChangeCheckTables(context.Background(), &execFailDB{DB: db, failOn: "smeldr_address_counters"}); err == nil {
		t.Error("failing address storage: no error")
	}
}

func TestListChangeChecks(t *testing.T) {
	f := newChangeCheckFixture(t)
	for _, tc := range []struct {
		number int
		head   string
		paths  []string
	}{
		{70, headA, []string{"internal/billing/a.go"}},
		{70, headB, []string{"internal/billing/a.go"}},
		{71, headA, []string{"cmd/x/main.go"}},
	} {
		if _, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: tc.number, HeadCommit: tc.head, Paths: tc.paths}); err != nil {
			t.Fatal(err)
		}
	}
	count := func(ctx Context, q ChangeCheckQuery) int {
		t.Helper()
		got, err := f.app.ListChangeChecks(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		return len(got)
	}
	for _, tc := range []struct {
		name string
		q    ChangeCheckQuery
		want int
	}{
		{"repository", ChangeCheckQuery{Repository: "Acme/App"}, 3},
		{"one change", ChangeCheckQuery{Repository: "acme/app", Number: 70}, 2},
		{"live only", ChangeCheckQuery{Repository: "acme/app", Number: 70, Status: ChangeCheckCommented}, 1},
		{"by Area", ChangeCheckQuery{Area: f.a2.Address}, 1},
		{"by Decision", ChangeCheckQuery{Decision: f.dArea}, 2},
		{"limit", ChangeCheckQuery{Repository: "acme/app", Limit: 1}, 1},
		{"unknown Area", ChangeCheckQuery{Area: "ARE999"}, 0},
	} {
		if got := count(f.ops, tc.q); got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
	}
	// A reader of the first Area sees only its Checks.
	if got := count(f.reader, ChangeCheckQuery{Repository: "acme/app"}); got != 2 {
		t.Errorf("reader: %d, want 2", got)
	}
	// A filter on a place or Decision the reader cannot read says nothing:
	// no Checks, though Checks of its own Area may touch that place.
	if got := count(f.reader, ChangeCheckQuery{Area: f.a2.Address}); got != 0 {
		t.Errorf("reader filtering by an unreadable Area: %d", got)
	}
	if got := count(f.reader, ChangeCheckQuery{Decision: f.dOrg}); got != 0 {
		t.Errorf("reader filtering by an unreadable Decision: %d", got)
	}
	if got := count(f.reader, ChangeCheckQuery{Decision: f.dArea}); got != 2 {
		t.Errorf("reader filtering by its own Area's Decision: %d", got)
	}
	// The limit is filled after the read filter: the newest Check (of the
	// second Area) is unreadable, the next readable one is still returned.
	if got, err := f.app.ListChangeChecks(f.reader, ChangeCheckQuery{Repository: "acme/app", Limit: 1}); err != nil || len(got) != 1 || got[0].Number != 70 {
		t.Errorf("reader, limit 1: %v, %v", got, err)
	}
	if _, err := f.app.ListChangeChecks(f.ops, ChangeCheckQuery{Number: 70}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("no filter: %v", err)
	}
	if _, err := f.app.ListChangeChecks(f.ops, ChangeCheckQuery{Repository: "acme"}); err == nil {
		t.Error("bad repository accepted")
	}
	if got := count(f.ops, ChangeCheckQuery{Repository: "acme/app", Limit: 9999}); got != 3 {
		t.Errorf("limit capped: %d", got)
	}
	if _, err := f.db.Exec(`ALTER TABLE smeldr_change_checks RENAME TO gone`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.ListChangeChecks(f.ops, ChangeCheckQuery{Repository: "acme/app"}); !errors.Is(err, ErrInternal) {
		t.Errorf("table gone: %v", err)
	}
	if _, err := f.db.Exec(`ALTER TABLE smeldr_areas RENAME TO areas_gone`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.ListChangeChecks(f.ops, ChangeCheckQuery{Area: "ARE1"}); !errors.Is(err, ErrInternal) {
		t.Errorf("Areas gone: %v", err)
	}
	if _, err := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")}).ListChangeChecks(context.Background(), ChangeCheckQuery{Repository: "a/b"}); !errors.Is(err, ErrInternal) {
		t.Errorf("no database: %v", err)
	}
}

// A supersede is announced like every other Check transition.
func TestChangeCheck_SupersedeEventName(t *testing.T) {
	f := newChangeCheckFixture(t)
	f.app.EventStream()
	ch, err := f.app.eventBroadcaster.subscribeScoped("ops-1", eventStreamChannelAll, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.app.eventBroadcaster.unsubscribe(ch)
	in := ChangeInput{Repository: "acme/app", Number: 80, HeadCommit: headA, Paths: []string{"internal/billing/a.go"}}
	if _, err := f.app.CheckChange(f.ops, in); err != nil {
		t.Fatal(err)
	}
	in.HeadCommit = headB
	if _, err := f.app.CheckChange(f.ops, in); err != nil {
		t.Fatal(err)
	}
	timeout := time.After(2 * time.Second)
	for {
		select {
		case b := <-ch:
			if strings.Contains(string(b), `"to_state":"superseded"`) {
				if !strings.Contains(string(b), `"event":"changecheck.transitioned"`) {
					t.Errorf("supersede event: %s", b)
				}
				return
			}
		case <-timeout:
			t.Fatal("no supersede event")
		}
	}
}
