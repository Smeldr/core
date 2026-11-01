package smeldr

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"
)

// planFixture is the coverage fixture with a task_plan type, its declaration
// fields (EnsureTaskPlanFields) and a plan flow.
type planFixture struct {
	coverageFixture
	plans *DynamicTypeRepo
}

func newPlanFixture(t *testing.T) planFixture {
	t.Helper()
	f := newCoverageFixture(t)
	ctx := context.Background()
	f.app.ServeDynamicContent()
	fields, _ := json.Marshal([]SchemaField{{Name: "band", Type: "string"}, {Name: "body", Type: "string", Role: "body"}})
	if _, err := f.app.DefineContentType(ctx, &ContentTypeSchema{TypeName: planType, Kind: "content", Fields: fields}); err != nil {
		t.Fatalf("define task_plan: %v", err)
	}
	if err := EnsureTaskPlanFields(ctx, f.app); err != nil {
		t.Fatalf("EnsureTaskPlanFields: %v", err)
	}
	if err := f.app.RegisterFlow(StateFlow{Name: "plan-flow", TypeName: planType,
		States: []State{{Name: "draft", IsInitial: true}, {Name: "submitted"}, {Name: "changes_requested"}, {Name: "approved"}},
		Transitions: []Transition{{From: "draft", To: "submitted"}, {From: "submitted", To: "approved"},
			{From: "submitted", To: "changes_requested"}, {From: "changes_requested", To: "submitted"}},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	repo, err := f.app.DynamicContentRepo(planType)
	if err != nil {
		t.Fatal(err)
	}
	return planFixture{coverageFixture: f, plans: repo}
}

// plan creates a draft plan with fields.
func (f planFixture) plan(t *testing.T, fields map[string]any) *DynamicNode {
	t.Helper()
	all := map[string]any{"band": "core", "body": "the plan"}
	for k, v := range fields {
		all[k] = v
	}
	n, err := f.plans.CreateDraft(context.Background(), all)
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	return n
}

func (f planFixture) move(t *testing.T, n *DynamicNode, to string) (map[string]any, error) {
	t.Helper()
	return f.app.TransitionItem(TrustedRead(context.Background()), planType, n.ID, to)
}

func (f planFixture) record(t *testing.T, ctx context.Context, n *DynamicNode) PlanTimeRecord {
	t.Helper()
	r, err := f.app.PlanTimeRecord(ctx, n.ID)
	if err != nil {
		t.Fatalf("PlanTimeRecord: %v", err)
	}
	return r
}

func areaIDsOf(r PlanTimeRecord) string {
	var ids []string
	for _, a := range r.Areas {
		ids = append(ids, a.ID)
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

func decisionIDsOf(r PlanTimeRecord) string {
	var ids []string
	for _, d := range r.Decisions {
		ids = append(ids, d.ID)
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

// A commit plan's declared paths are resolved at submit and again at
// approval: the Areas and the Decisions in force, written by the system, the
// earlier set ended so the history keeps both.
func TestPlanTime_RecordAndRerecord(t *testing.T) {
	f := newPlanFixture(t)
	n := f.plan(t, map[string]any{"repository": "acme/app", "paths": []any{"internal/billing/**"}})
	if _, err := f.move(t, n, "submitted"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	ctx := TrustedRead(context.Background())
	r := f.record(t, ctx, n)
	if !r.Recorded || r.Transition != "submitted" || areaIDsOf(r) != f.a1.ID {
		t.Fatalf("at submit: %+v", r)
	}
	if got := decisionIDsOf(r); got != "dec-area,dec-domain,dec-org" {
		t.Errorf("decisions at submit = %s", got)
	}
	// The plan now touches the second Area too, and is approved.
	if _, err := f.move(t, n, "changes_requested"); err != nil {
		t.Fatal(err)
	}
	if err := f.plans.UpdateFields(context.Background(), n.ID, map[string]any{"paths": []any{"internal/billing/**", "cmd/x/**"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.move(t, n, "submitted"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.move(t, n, "approved"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	r = f.record(t, ctx, n)
	if r.Transition != "approved" || areaIDsOf(r) != sortedIDs(f.a1.ID, f.a2.ID) {
		t.Fatalf("at approval: %+v", r)
	}
	var live, ended int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_relations WHERE source_id = $1 AND created_by_job = 'plan-time' AND invalid_at IS NULL`, n.ID).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_relations WHERE source_id = $1 AND created_by_job = 'plan-time' AND invalid_at IS NOT NULL`, n.ID).Scan(&ended); err != nil {
		t.Fatal(err)
	}
	if live != 5 || ended < 4 {
		t.Errorf("live %d, ended %d plan-time edges; want the approval's 5 live and the earlier ones ended", live, ended)
	}
}

// A no_commit plan needs no declaration and gets no record; a commit plan with
// none submits with a warning before the cutover and is refused after, and
// its read says no record exists.
func TestPlanTime_Declarations(t *testing.T) {
	f := newPlanFixture(t)
	ctx := TrustedRead(context.Background())

	none := f.plan(t, map[string]any{"no_commit": true})
	res, err := f.move(t, none, "submitted")
	if err != nil || res["warnings"] != nil {
		t.Fatalf("a no_commit plan: %v, %v; want it submitted without a warning", res, err)
	}
	if r := f.record(t, ctx, none); r.Recorded || !r.NoCommit {
		t.Errorf("a no_commit plan's read: %+v", r)
	}

	bare := f.plan(t, nil)
	res, err = f.move(t, bare, "submitted")
	if err != nil {
		t.Fatalf("before the cutover: %v", err)
	}
	ws, _ := res["warnings"].([]CreateWarning)
	if len(ws) != 1 || ws[0].Code != "plan_paths_missing" || ws[0].Field != "repository" {
		t.Errorf("warnings = %+v; want plan_paths_missing on repository", res["warnings"])
	}
	if r := f.record(t, ctx, bare); r.Recorded || r.NoCommit {
		t.Errorf("a commit plan with no declaration must read as not recorded: %+v", r)
	}

	f.app.cfg.PlanPathsCutover = time.Now().Add(-time.Hour)
	other := f.plan(t, map[string]any{"repository": "acme/app"})
	var ve *ValidationError
	if _, err := f.move(t, other, "submitted"); !errors.As(err, &ve) || !strings.Contains(err.Error(), "paths") {
		t.Errorf("after the cutover, no paths: %v; want a ValidationError on paths", err)
	}
	bad := f.plan(t, map[string]any{"repository": "acme/app", "paths": []any{"../etc/**"}})
	if _, err := f.move(t, bad, "submitted"); !errors.As(err, &ve) {
		t.Errorf("an invalid pattern: %v; want a ValidationError", err)
	}
	wrong := f.plan(t, map[string]any{"repository": "not a repo", "paths": []any{"a/**"}})
	if _, err := f.move(t, wrong, "submitted"); !errors.As(err, &ve) {
		t.Errorf("an invalid repository: %v; want a ValidationError", err)
	}
	many := make([]any, DecisionsForPathsMaxPaths+1)
	for i := range many {
		many[i] = "a/**"
	}
	big := f.plan(t, map[string]any{"repository": "acme/app", "paths": many})
	if _, err := f.move(t, big, "submitted"); !errors.As(err, &ve) {
		t.Errorf("too many patterns: %v; want a ValidationError", err)
	}
}

// The author cannot write or withdraw a plan-time edge.
func TestPlanTime_AuthorCannotWrite(t *testing.T) {
	f := newPlanFixture(t)
	n := f.plan(t, map[string]any{"repository": "acme/app", "paths": []any{"internal/billing/**"}})
	if _, err := f.move(t, n, "submitted"); err != nil {
		t.Fatal(err)
	}
	ctx := TrustedRead(authorCtx())
	if _, _, err := f.app.relationStore.MCPAssertRelationAt(ctx, planType, n.ID, "Area", f.a2.ID, "belongs_to_area", nil, nil, nil, nil, nil); !errors.Is(err, ErrForbidden) {
		t.Errorf("an author's assert: %v; want a refusal", err)
	}
	var id string
	if err := f.db.QueryRow(`SELECT id FROM smeldr_relations WHERE source_id = $1 AND created_by_job = 'plan-time' AND invalid_at IS NULL LIMIT 1`, n.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.relationStore.WithdrawAt(ctx, id, "not mine", nil); !errors.Is(err, ErrForbidden) {
		t.Errorf("an author's withdraw: %v; want a refusal", err)
	}
}

// A reader sees only the Areas and Decisions it may read.
func TestPlanTime_ReadRights(t *testing.T) {
	f := newPlanFixture(t)
	n := f.plan(t, map[string]any{"repository": "acme/app", "paths": []any{"internal/billing/**", "cmd/*/main.go"}})
	if _, err := f.move(t, n, "submitted"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := f.rs.Grant(ctx, RoleGrant{TokenID: "reader", RoleName: "scoped-reader",
		ScopeStatic: []string{"Area:" + f.a1.ID, "Decision:dec-area", planType + ":" + n.ID}}); err != nil {
		t.Fatal(err)
	}
	r := f.record(t, NewTestContext(User{ID: "reader"}), n)
	if areaIDsOf(r) != f.a1.ID || decisionIDsOf(r) != "dec-area" {
		t.Errorf("the reader's record = %+v; want only what it may read", r)
	}
	if _, err := f.app.PlanTimeRecord(NewTestContext(User{ID: "nobody"}), n.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("a plan the caller cannot read: %v; want ErrNotFound", err)
	}
}

// A failed record is logged; the transition stands.
func TestPlanTime_RecordFailsLogged(t *testing.T) {
	f := newPlanFixture(t)
	n := f.plan(t, map[string]any{"repository": "acme/app", "paths": []any{"internal/billing/**"}})
	if _, err := f.db.Exec(`DROP TABLE smeldr_plan_time_records`); err != nil {
		t.Fatal(err)
	}
	out := capturedLog(func() {
		if _, err := f.move(t, n, "submitted"); err != nil {
			t.Errorf("the transition must stand: %v", err)
		}
	})
	if !strings.Contains(out, "plan-time record not made") {
		t.Errorf("the failure must be logged:\n%s", out)
	}
}

// EnsureTaskPlanFields is idempotent and a no-op without a task_plan type.
func TestEnsureTaskPlanFields(t *testing.T) {
	f := newPlanFixture(t)
	if err := EnsureTaskPlanFields(context.Background(), f.app); err != nil {
		t.Fatalf("a second call: %v", err)
	}
	var fields []SchemaField
	_ = json.Unmarshal(f.app.typeRegistry.Lookup(planType).Schema.Fields, &fields)
	count := map[string]int{}
	for _, fl := range fields {
		count[fl.Name]++
	}
	if count["repository"] != 1 || count["paths"] != 1 || count["no_commit"] != 1 {
		t.Errorf("fields = %v", count)
	}
	other := newCoverageFixture(t)
	if err := EnsureTaskPlanFields(context.Background(), other.app); err != nil {
		t.Errorf("no task_plan type: %v", err)
	}
	if err := EnsureTaskPlanFields(context.Background(), nil); err != nil {
		t.Errorf("a nil App: %v", err)
	}
}

func TestPatternsOverlap(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"a/**", "a/b/*.go", true},
		{"cmd/*/main.go", "cmd/x/**", true},
		{"cmd/*/main.go", "internal/**", false},
		{"internal/billing/a.go", "internal/billing/**", true},
		{"README.md", "README.md", true},
		{"README.md", "docs/README.md", false},
		{"**", "x/y/z", true},
		{"a/*.go", "a/*.md", false},
		{"a/x?.go", "a/*y.go", true},
		{"a/b", "a/b/c", false},
		{"**/main.go", "cmd/*/main.go", true},
		{"**/a/**/b", "x/**/a/y/**", true},
	} {
		if got := patternsOverlap(c.a, c.b); got != c.want {
			t.Errorf("patternsOverlap(%q, %q) = %v; want %v", c.a, c.b, got, c.want)
		}
		if got := patternsOverlap(c.b, c.a); got != c.want {
			t.Errorf("patternsOverlap(%q, %q) = %v; want %v (symmetric)", c.b, c.a, got, c.want)
		}
	}
}

// An instance with no relations table reads an empty, unrecorded record.
func TestPlanTime_NoRelationsTable(t *testing.T) {
	f := newPlanFixture(t)
	n := f.plan(t, nil)
	if _, err := f.db.Exec(`DROP TABLE smeldr_relations`); err != nil {
		t.Fatal(err)
	}
	r, err := f.app.PlanTimeRecord(TrustedRead(context.Background()), n.ID)
	if err != nil || r.Recorded || len(r.Areas) != 0 {
		t.Errorf("no relations table: %+v, %v; want an empty, unrecorded record", r, err)
	}
}

// The record and its read fail closed on what they cannot read, and skip a
// target that is gone.
func TestPlanTime_ErrorPaths(t *testing.T) {
	ctx := TrustedRead(context.Background())
	other := newCoverageFixture(t)
	if err := other.app.recordPlanTime(ctx, "p", "submitted"); err == nil {
		t.Error("recordPlanTime without a task_plan type: want an error")
	}
	if _, err := other.app.PlanTimeRecord(ctx, "p"); err == nil {
		t.Error("PlanTimeRecord without a task_plan type: want an error")
	}
	if got := (&DynamicTypeRepo{}).planPathsCutover(); !got.IsZero() {
		t.Errorf("planPathsCutover without an App = %v", got)
	}

	f := newPlanFixture(t)
	if err := f.app.recordPlanTime(ctx, "no-such-plan", "submitted"); err == nil {
		t.Error("recordPlanTime of an unknown plan: want an error")
	}
	if _, err := f.app.PlanTimeRecord(ctx, "no-such-plan"); err == nil {
		t.Error("PlanTimeRecord of an unknown plan: want an error")
	}
	bad := f.plan(t, map[string]any{"repository": "not a repository", "paths": []any{"a/**"}})
	if err := f.app.recordPlanTime(ctx, bad.ID, "submitted"); err == nil {
		t.Error("recordPlanTime with an invalid pattern: want an error")
	}

	n := f.plan(t, map[string]any{"repository": "acme/app", "paths": []any{"internal/billing/**"}})
	if _, err := f.move(t, n, "submitted"); err != nil {
		t.Fatal(err)
	}
	// A target that is gone is left out of the read.
	if _, err := f.db.Exec(`DELETE FROM smeldr_decisions WHERE id = 'dec-org'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`DROP TABLE smeldr_areas`); err != nil {
		t.Fatal(err)
	}
	r := f.record(t, ctx, n)
	if len(r.Areas) != 0 || decisionIDsOf(r) != "dec-area,dec-domain" {
		t.Errorf("gone targets: %+v", r)
	}
	// A record row it cannot scan, and a relations table it cannot query, are errors.
	if _, err := f.db.Exec(`UPDATE smeldr_plan_time_records SET recorded_at = 'not a time' WHERE plan_id = $1`, n.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.PlanTimeRecord(ctx, n.ID); !errors.Is(err, ErrInternal) {
		t.Errorf("an unreadable record row: %v; want ErrInternal", err)
	}
	if _, err := f.db.Exec(`DELETE FROM smeldr_plan_time_records`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`ALTER TABLE smeldr_relations DROP COLUMN created_by_job`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.PlanTimeRecord(ctx, n.ID); !errors.Is(err, ErrInternal) {
		t.Errorf("an unqueryable relations table: %v; want ErrInternal", err)
	}
	if err := f.app.recordPlanTime(ctx, n.ID, "approved"); err == nil {
		t.Error("recordPlanTime on an unqueryable relations table: want an error")
	}
}

func sortedIDs(ids ...string) string {
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

// EnsureTaskPlanFields reports a table it cannot make and a schema it cannot
// read; recordPlanTime reports an edge it cannot end or write.
func TestPlanTime_WriteErrors(t *testing.T) {
	f := newPlanFixture(t)
	if err := EnsureTaskPlanFields(cancelled(), f.app); err == nil {
		t.Error("EnsureTaskPlanFields on a cancelled context: want an error")
	}
	desc := f.app.typeRegistry.Lookup(planType)
	saved := desc.Schema.Fields
	desc.Schema.Fields = json.RawMessage("not json")
	if err := EnsureTaskPlanFields(context.Background(), f.app); err == nil {
		t.Error("EnsureTaskPlanFields on an unreadable schema: want an error")
	}
	desc.Schema.Fields = saved
	// A schema missing a field, where the redefinition cannot be saved.
	var fields []SchemaField
	_ = json.Unmarshal(saved, &fields)
	desc.Schema.Fields, _ = json.Marshal(fields[:len(fields)-1])
	if _, err := f.db.Exec(`CREATE TRIGGER no_schema BEFORE UPDATE ON smeldr_content_type_schemas BEGIN SELECT RAISE(ABORT, 'no update'); END`); err != nil {
		t.Fatal(err)
	}
	if err := EnsureTaskPlanFields(context.Background(), f.app); err == nil {
		t.Error("EnsureTaskPlanFields with a failing redefinition: want an error")
	}
	if _, err := f.db.Exec(`DROP TRIGGER no_schema`); err != nil {
		t.Fatal(err)
	}
	desc.Schema.Fields = saved

	ctx := TrustedRead(context.Background())
	n := f.plan(t, map[string]any{"repository": "acme/app", "paths": []any{"internal/billing/**"}})
	if _, err := f.db.Exec(`CREATE TRIGGER no_insert BEFORE INSERT ON smeldr_relations BEGIN SELECT RAISE(ABORT, 'no insert'); END`); err != nil {
		t.Fatal(err)
	}
	if err := f.app.recordPlanTime(ctx, n.ID, "submitted"); err == nil {
		t.Error("recordPlanTime with a failing edge write: want an error")
	}
	if _, err := f.db.Exec(`DROP TRIGGER no_insert`); err != nil {
		t.Fatal(err)
	}
	if err := f.app.recordPlanTime(ctx, n.ID, "submitted"); err != nil {
		t.Fatalf("recordPlanTime: %v", err)
	}
	if _, err := f.db.Exec(`CREATE TRIGGER no_update BEFORE UPDATE ON smeldr_relations BEGIN SELECT RAISE(ABORT, 'no update'); END`); err != nil {
		t.Fatal(err)
	}
	if err := f.app.recordPlanTime(ctx, n.ID, "approved"); err == nil {
		t.Error("recordPlanTime with a failing edge end: want an error")
	}
}
