package smeldr

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

// checkExecFailDB fails every ExecContext call; QueryContext/QueryRowContext
// pass through to the wrapped DB unchanged.
type checkExecFailDB struct {
	DB
}

func (d *checkExecFailDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return nil, errRepoError
}

// checkQueryFailDB fails every QueryContext call; ExecContext passes through
// to the wrapped DB unchanged.
type checkQueryFailDB struct {
	DB
}

func (d *checkQueryFailDB) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, errRepoError
}

// checkQueryFailOnDB fails QueryContext only when the query contains failOn —
// used to fail one of queryAuthorityCandidates' two queries independently.
type checkQueryFailOnDB struct {
	DB
	failOn string
}

func (d *checkQueryFailOnDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if strings.Contains(q, d.failOn) {
		return nil, errRepoError
	}
	return d.DB.QueryContext(ctx, q, args...)
}

func createCheckTableHelper(t *testing.T, db DB) {
	t.Helper()
	if err := CreateCheckTable(db); err != nil {
		t.Fatalf("CreateCheckTable: %v", err)
	}
}

// — CheckStore ———————————————————————————————————————————————————————————————

func TestCreateCheckTable_Idempotent(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateCheckTable(db); err != nil {
		t.Fatalf("first CreateCheckTable: %v", err)
	}
	if err := CreateCheckTable(db); err != nil {
		t.Fatalf("second CreateCheckTable (should be idempotent): %v", err)
	}
}

func TestCheckStore_AppendAndLast(t *testing.T) {
	db := newSQLiteDB(t)
	createCheckTableHelper(t, db)
	store := NewCheckStore(db)
	ctx := context.Background()

	r := CheckRecord{
		ID: "check-1", SubjectType: "Decision", SubjectID: "d1", RuleType: "design-system",
		RanAt: time.Now().UTC().Truncate(time.Second), Found: true,
		MatchType: "Rule", MatchID: "r1", MatchName: "no-scroll-lock",
		Sentence: `this touches "no-scroll-lock", an existing design-system rule`,
	}
	if err := store.Append(ctx, r); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, found, err := store.Last(ctx, "Decision", "d1")
	if err != nil {
		t.Fatalf("Last: %v", err)
	}
	if !found {
		t.Fatal("Last: found = false, want true")
	}
	if got.ID != r.ID || got.RuleType != r.RuleType || got.Found != r.Found ||
		got.MatchType != r.MatchType || got.MatchID != r.MatchID || got.MatchName != r.MatchName ||
		got.Sentence != r.Sentence {
		t.Errorf("Last: got %+v, want %+v", got, r)
	}
	if !got.RanAt.Equal(r.RanAt) {
		t.Errorf("Last: RanAt = %v, want %v", got.RanAt, r.RanAt)
	}
}

func TestCheckStore_AppendError(t *testing.T) {
	db := newSQLiteDB(t)
	createCheckTableHelper(t, db)
	store := NewCheckStore(&checkExecFailDB{DB: db})

	err := store.Append(context.Background(), CheckRecord{ID: "c1", SubjectType: "Decision", SubjectID: "d1", RanAt: time.Now()})
	if err == nil {
		t.Fatal("Append: want error, got nil")
	}
}

func TestCheckStore_LastNotFound(t *testing.T) {
	db := newSQLiteDB(t)
	createCheckTableHelper(t, db)
	store := NewCheckStore(db)

	_, found, err := store.Last(context.Background(), "Decision", "no-such-id")
	if err != nil {
		t.Fatalf("Last: want nil error for no rows, got %v", err)
	}
	if found {
		t.Error("Last: found = true, want false for a subject with no check records yet")
	}
}

func TestCheckStore_LastError(t *testing.T) {
	db := newSQLiteDB(t)
	createCheckTableHelper(t, db)
	store := NewCheckStore(&checkQueryFailDB{DB: db})

	_, _, err := store.Last(context.Background(), "Decision", "d1")
	if err == nil {
		t.Fatal("Last: want error, got nil")
	}
}

func TestCheckStore_ListOrderAndLimit(t *testing.T) {
	db := newSQLiteDB(t)
	createCheckTableHelper(t, db)
	store := NewCheckStore(db)
	ctx := context.Background()

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for i, id := range []string{"c1", "c2", "c3"} {
		r := CheckRecord{ID: id, SubjectType: "Decision", SubjectID: "d1", RanAt: base.Add(time.Duration(i) * time.Hour)}
		if err := store.Append(ctx, r); err != nil {
			t.Fatalf("Append %s: %v", id, err)
		}
	}
	// A different subject must not appear in "d1"'s list.
	if err := store.Append(ctx, CheckRecord{ID: "other", SubjectType: "Decision", SubjectID: "d2", RanAt: base}); err != nil {
		t.Fatalf("Append other: %v", err)
	}

	all, err := store.List(ctx, "Decision", "d1", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("List: got %d records, want 3", len(all))
	}
	wantOrder := []string{"c3", "c2", "c1"}
	for i, want := range wantOrder {
		if all[i].ID != want {
			t.Errorf("List[%d].ID = %q, want %q", i, all[i].ID, want)
		}
	}

	limited, err := store.List(ctx, "Decision", "d1", 2)
	if err != nil {
		t.Fatalf("List with limit: %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("List with limit=2: got %d records, want 2", len(limited))
	}
	if limited[0].ID != "c3" || limited[1].ID != "c2" {
		t.Errorf("List with limit=2: got %+v", limited)
	}
}

func TestCheckStore_ListEmpty(t *testing.T) {
	db := newSQLiteDB(t)
	createCheckTableHelper(t, db)
	store := NewCheckStore(db)

	records, err := store.List(context.Background(), "Decision", "no-such-id", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("List: got %d records, want 0", len(records))
	}
}

func TestCheckStore_ListError(t *testing.T) {
	db := newSQLiteDB(t)
	createCheckTableHelper(t, db)
	store := NewCheckStore(&checkQueryFailDB{DB: db})

	_, err := store.List(context.Background(), "Decision", "d1", 0)
	if err == nil {
		t.Fatal("List: want error, got nil")
	}
}

// checkScanFailDB swaps any SELECT against smeldr_check_records for a query
// with the wrong column count, so the caller's Scan fails.
type checkScanFailDB struct {
	DB
}

func (d *checkScanFailDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if strings.Contains(q, "smeldr_check_records") {
		return d.DB.QueryContext(ctx, "SELECT 1")
	}
	return d.DB.QueryContext(ctx, q, args...)
}

func TestCheckStore_LastScanError(t *testing.T) {
	db := newSQLiteDB(t)
	createCheckTableHelper(t, db)
	store := NewCheckStore(db)
	if err := store.Append(context.Background(), CheckRecord{ID: "c1", SubjectType: "Decision", SubjectID: "d1", RanAt: time.Now()}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	badStore := NewCheckStore(&checkScanFailDB{DB: db})
	_, _, err := badStore.Last(context.Background(), "Decision", "d1")
	if err == nil {
		t.Fatal("Last: want scan error, got nil")
	}
}

func TestCheckStore_ListScanError(t *testing.T) {
	db := newSQLiteDB(t)
	createCheckTableHelper(t, db)
	store := NewCheckStore(db)
	if err := store.Append(context.Background(), CheckRecord{ID: "c1", SubjectType: "Decision", SubjectID: "d1", RanAt: time.Now()}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	badStore := NewCheckStore(&checkScanFailDB{DB: db})
	_, err := badStore.List(context.Background(), "Decision", "d1", 0)
	if err == nil {
		t.Fatal("List: want scan error, got nil")
	}
}

// — queryAuthorityCandidates —————————————————————————————————————————————————

func setupAuthorityDB(t *testing.T) DB {
	t.Helper()
	db := newSQLiteDB(t)
	if err := CreateAuthorityTables(db); err != nil {
		t.Fatalf("CreateAuthorityTables: %v", err)
	}
	return db
}

func seedRule(t *testing.T, db DB, id, slug, ruleType, status string, publishedAt time.Time) {
	t.Helper()
	if err := NewSQLRepo[*Rule](db, Table("smeldr_rules")).Save(context.Background(), &Rule{
		Node:     Node{ID: id, Slug: slug, Status: Status(status), PublishedAt: publishedAt},
		RuleType: ruleType, Surface: "core",
	}); err != nil {
		t.Fatalf("seed Rule %s: %v", slug, err)
	}
}

func seedStub(t *testing.T, db DB, id, slug, ruleType, status string, publishedAt time.Time) {
	t.Helper()
	if err := NewSQLRepo[*AuthorityStub](db, Table("smeldr_authority_stubs")).Save(context.Background(), &AuthorityStub{
		Node:      Node{ID: id, Slug: slug, Status: Status(status), PublishedAt: publishedAt},
		RuleType:  ruleType,
		SourceRef: "docs/" + slug + ".md",
	}); err != nil {
		t.Fatalf("seed AuthorityStub %s: %v", slug, err)
	}
}

func TestQueryAuthorityCandidates_NoMatches(t *testing.T) {
	db := setupAuthorityDB(t)
	rules, stubs, err := queryAuthorityCandidates(context.Background(), db, "design-system")
	if err != nil {
		t.Fatalf("queryAuthorityCandidates: %v", err)
	}
	if len(rules) != 0 || len(stubs) != 0 {
		t.Errorf("got rules=%v stubs=%v, want both empty", rules, stubs)
	}
}

func TestQueryAuthorityCandidates_RuleMatch(t *testing.T) {
	db := setupAuthorityDB(t)
	seedRule(t, db, "r1", "no-scroll-lock", "design-system", "active", time.Now().UTC())

	rules, stubs, err := queryAuthorityCandidates(context.Background(), db, "design-system")
	if err != nil {
		t.Fatalf("queryAuthorityCandidates: %v", err)
	}
	if len(rules) != 1 || rules[0].Slug != "no-scroll-lock" {
		t.Errorf("rules = %+v, want one matching no-scroll-lock", rules)
	}
	if len(stubs) != 0 {
		t.Errorf("stubs = %+v, want empty", stubs)
	}
}

func TestQueryAuthorityCandidates_StubMatch(t *testing.T) {
	db := setupAuthorityDB(t)
	seedStub(t, db, "s1", "turn-67-law", "design-system", "stub", time.Now().UTC())

	rules, stubs, err := queryAuthorityCandidates(context.Background(), db, "design-system")
	if err != nil {
		t.Fatalf("queryAuthorityCandidates: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("rules = %+v, want empty", rules)
	}
	if len(stubs) != 1 || stubs[0].Slug != "turn-67-law" {
		t.Errorf("stubs = %+v, want one matching turn-67-law", stubs)
	}
}

func TestQueryAuthorityCandidates_RetiredRuleExcluded(t *testing.T) {
	db := setupAuthorityDB(t)
	seedRule(t, db, "r1", "old-rule", "design-system", "retired", time.Now().UTC())

	rules, _, err := queryAuthorityCandidates(context.Background(), db, "design-system")
	if err != nil {
		t.Fatalf("queryAuthorityCandidates: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("rules = %+v, want a retired Rule excluded", rules)
	}
}

func TestQueryAuthorityCandidates_ConvertedStubExcluded(t *testing.T) {
	db := setupAuthorityDB(t)
	seedStub(t, db, "s1", "now-a-rule", "design-system", "converted", time.Now().UTC())

	_, stubs, err := queryAuthorityCandidates(context.Background(), db, "design-system")
	if err != nil {
		t.Fatalf("queryAuthorityCandidates: %v", err)
	}
	if len(stubs) != 0 {
		t.Errorf("stubs = %+v, want a converted stub excluded", stubs)
	}
}

func TestQueryAuthorityCandidates_RulesQueryError(t *testing.T) {
	db := setupAuthorityDB(t)
	wrapped := &checkQueryFailOnDB{DB: db, failOn: "FROM smeldr_rules"}
	_, _, err := queryAuthorityCandidates(context.Background(), wrapped, "design-system")
	if err == nil {
		t.Fatal("expected error when the Rules query fails")
	}
}

func TestQueryAuthorityCandidates_StubsQueryError(t *testing.T) {
	db := setupAuthorityDB(t)
	wrapped := &checkQueryFailOnDB{DB: db, failOn: "FROM smeldr_authority_stubs"}
	_, _, err := queryAuthorityCandidates(context.Background(), wrapped, "design-system")
	if err == nil {
		t.Fatal("expected error when the AuthorityStubs query fails")
	}
}

// — RunAuthorityCheck ————————————————————————————————————————————————————————

func TestRunAuthorityCheck_NotFound(t *testing.T) {
	db := setupAuthorityDB(t)
	createCheckTableHelper(t, db)
	store := NewCheckStore(db)

	r, err := RunAuthorityCheck(context.Background(), db, store, "Decision", "d1", "design-system")
	if err != nil {
		t.Fatalf("RunAuthorityCheck: %v", err)
	}
	if r.Found {
		t.Errorf("Found = true, want false")
	}
	if r.Sentence != "no conflicting authority found" {
		t.Errorf("Sentence = %q, want %q", r.Sentence, "no conflicting authority found")
	}

	// Recorded, not silently assumed.
	last, found, err := store.Last(context.Background(), "Decision", "d1")
	if err != nil || !found {
		t.Fatalf("Last after RunAuthorityCheck: found=%v err=%v", found, err)
	}
	if last.Found {
		t.Errorf("recorded Found = true, want false")
	}
}

func TestRunAuthorityCheck_FoundRule(t *testing.T) {
	db := setupAuthorityDB(t)
	createCheckTableHelper(t, db)
	store := NewCheckStore(db)
	seedRule(t, db, "r1", "no-scroll-lock", "design-system", "active", time.Now().UTC())

	r, err := RunAuthorityCheck(context.Background(), db, store, "Decision", "d1", "design-system")
	if err != nil {
		t.Fatalf("RunAuthorityCheck: %v", err)
	}
	if !r.Found || r.MatchType != "Rule" || r.MatchID != "r1" || r.MatchName != "no-scroll-lock" {
		t.Errorf("got %+v, want a found Rule match on r1/no-scroll-lock", r)
	}
	if r.Sentence != `this touches "no-scroll-lock", an existing design-system rule` {
		t.Errorf("Sentence = %q", r.Sentence)
	}
}

func TestRunAuthorityCheck_FoundStub(t *testing.T) {
	db := setupAuthorityDB(t)
	createCheckTableHelper(t, db)
	store := NewCheckStore(db)
	seedStub(t, db, "s1", "turn-67-law", "design-system", "stub", time.Now().UTC())

	r, err := RunAuthorityCheck(context.Background(), db, store, "Decision", "d1", "design-system")
	if err != nil {
		t.Fatalf("RunAuthorityCheck: %v", err)
	}
	if !r.Found || r.MatchType != "AuthorityStub" || r.MatchID != "s1" {
		t.Errorf("got %+v, want a found AuthorityStub match on s1", r)
	}
}

func TestRunAuthorityCheck_MultipleCandidates_MostRecentWins(t *testing.T) {
	db := setupAuthorityDB(t)
	createCheckTableHelper(t, db)
	store := NewCheckStore(db)
	older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	seedRule(t, db, "r1", "older-rule", "design-system", "active", older)
	seedRule(t, db, "r2", "newer-rule", "design-system", "active", newer)

	r, err := RunAuthorityCheck(context.Background(), db, store, "Decision", "d1", "design-system")
	if err != nil {
		t.Fatalf("RunAuthorityCheck: %v", err)
	}
	if r.MatchID != "r2" {
		t.Errorf("MatchID = %q, want r2 (the most recently published)", r.MatchID)
	}
}

func TestRunAuthorityCheck_QueryError(t *testing.T) {
	db := setupAuthorityDB(t)
	createCheckTableHelper(t, db)
	store := NewCheckStore(db)
	wrapped := &checkQueryFailDB{DB: db}

	r, err := RunAuthorityCheck(context.Background(), wrapped, store, "Decision", "d1", "design-system")
	if err == nil {
		t.Fatal("expected error when queryAuthorityCandidates fails")
	}
	if r != nil {
		t.Errorf("expected nil record on query error, got %+v", r)
	}
}

func TestRunAuthorityCheck_AppendError(t *testing.T) {
	db := setupAuthorityDB(t)
	createCheckTableHelper(t, db)
	store := NewCheckStore(&checkExecFailDB{DB: db})

	r, err := RunAuthorityCheck(context.Background(), db, store, "Decision", "d1", "design-system")
	if err == nil {
		t.Fatal("expected error when store.Append fails")
	}
	if r == nil {
		t.Fatal("expected the computed record to still be returned on a persist failure")
	}
	if r.Sentence != "no conflicting authority found" {
		t.Errorf("returned record Sentence = %q, want the computed finding despite the persist failure", r.Sentence)
	}
}

// — runDecisionAuthorityCheck / runDecisionAuthorityCheckByID (wiring) ————————

func TestRunDecisionAuthorityCheck_NilStore_NoOp(t *testing.T) {
	db := setupAuthorityDB(t)
	// No CheckStore configured — must not panic, must not query.
	runDecisionAuthorityCheck(context.Background(), db, nil, &Decision{Node: Node{ID: "d1"}, RuleType: "design-system"}, "proposed", "ratified")
}

func TestRunDecisionAuthorityCheck_WrongTransition_NoOp(t *testing.T) {
	db := setupAuthorityDB(t)
	createCheckTableHelper(t, db)
	store := NewCheckStore(db)
	runDecisionAuthorityCheck(context.Background(), db, store, &Decision{Node: Node{ID: "d1"}, RuleType: "design-system"}, "proposed", "archived")
	_, found, err := store.Last(context.Background(), "Decision", "d1")
	if err != nil {
		t.Fatalf("Last: %v", err)
	}
	if found {
		t.Error("expected no Check record for a non-ratification transition")
	}
}

func TestRunDecisionAuthorityCheck_NonDecisionItem_NoOp(t *testing.T) {
	db := setupAuthorityDB(t)
	createCheckTableHelper(t, db)
	store := NewCheckStore(db)
	// Any non-*Decision item — must be a silent no-op, matching
	// authorizeDecisionScope's own established type-assertion pattern.
	runDecisionAuthorityCheck(context.Background(), db, store, &Rule{Node: Node{ID: "r1"}}, "proposed", "ratified")
	_, found, err := store.Last(context.Background(), "Rule", "r1")
	if err != nil {
		t.Fatalf("Last: %v", err)
	}
	if found {
		t.Error("expected no Check record for a non-Decision item")
	}
}

func TestRunDecisionAuthorityCheck_Ratifies(t *testing.T) {
	db := setupAuthorityDB(t)
	createCheckTableHelper(t, db)
	store := NewCheckStore(db)
	runDecisionAuthorityCheck(context.Background(), db, store, &Decision{Node: Node{ID: "d1"}, RuleType: "design-system"}, "proposed", "ratified")
	last, found, err := store.Last(context.Background(), "Decision", "d1")
	if err != nil || !found {
		t.Fatalf("Last: found=%v err=%v", found, err)
	}
	if last.RuleType != "design-system" {
		t.Errorf("RuleType = %q, want design-system", last.RuleType)
	}
}

func TestRunDecisionAuthorityCheckByID_NilStore_NoOp(t *testing.T) {
	db := setupAuthorityDB(t)
	runDecisionAuthorityCheckByID(context.Background(), db, nil, "Decision", "d1", "proposed", "ratified")
}

func TestRunDecisionAuthorityCheckByID_WrongType_NoOp(t *testing.T) {
	db := setupAuthorityDB(t)
	createCheckTableHelper(t, db)
	store := NewCheckStore(db)
	runDecisionAuthorityCheckByID(context.Background(), db, store, "Task", "t1", "proposed", "ratified")
	_, found, err := store.Last(context.Background(), "Task", "t1")
	if err != nil {
		t.Fatalf("Last: %v", err)
	}
	if found {
		t.Error("expected no Check record for a non-Decision type")
	}
}

func TestRunDecisionAuthorityCheckByID_WrongTransition_NoOp(t *testing.T) {
	db := setupAuthorityDB(t)
	createCheckTableHelper(t, db)
	store := NewCheckStore(db)
	runDecisionAuthorityCheckByID(context.Background(), db, store, "Decision", "d1", "ratified", "superseded")
	_, found, err := store.Last(context.Background(), "Decision", "d1")
	if err != nil {
		t.Fatalf("Last: %v", err)
	}
	if found {
		t.Error("expected no Check record for a non-ratification transition")
	}
}

func TestRunDecisionAuthorityCheckByID_LookupError(t *testing.T) {
	db := setupAuthorityDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	createCheckTableHelper(t, db)
	store := NewCheckStore(db)
	// itemID does not exist in smeldr_decisions — QueryRowContext.Scan fails
	// with sql.ErrNoRows, exercised as a real lookup failure, logged and
	// swallowed (fail-open), not panicking.
	runDecisionAuthorityCheckByID(context.Background(), db, store, "Decision", "no-such-id", "proposed", "ratified")
	_, found, err := store.Last(context.Background(), "Decision", "no-such-id")
	if err != nil {
		t.Fatalf("Last: %v", err)
	}
	if found {
		t.Error("expected no Check record when the RuleType lookup itself fails")
	}
}

func TestRunDecisionAuthorityCheckByID_Ratifies(t *testing.T) {
	db := setupAuthorityDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	createCheckTableHelper(t, db)
	store := NewCheckStore(db)
	if err := NewSQLRepo[*Decision](db, Table("smeldr_decisions")).Save(context.Background(), &Decision{
		Node: Node{ID: "d1", Slug: "d1"}, DecisionNumber: "D1", Scope: "core", RuleType: "design-system",
	}); err != nil {
		t.Fatalf("seed Decision: %v", err)
	}

	runDecisionAuthorityCheckByID(context.Background(), db, store, "Decision", "d1", "proposed", "ratified")
	last, found, err := store.Last(context.Background(), "Decision", "d1")
	if err != nil || !found {
		t.Fatalf("Last: found=%v err=%v", found, err)
	}
	if last.RuleType != "design-system" {
		t.Errorf("RuleType = %q, want design-system", last.RuleType)
	}
}

// runDecisionAuthorityCheck/ByID are fail-open (see their own godoc): a
// RunAuthorityCheck failure — not just store.Append's own error, exercised
// directly against RunAuthorityCheck above — must be logged and swallowed
// through the wiring functions themselves, never panicking or propagating.
func TestRunDecisionAuthorityCheck_RunAuthorityCheckError_LoggedNotPanicked(t *testing.T) {
	db := setupAuthorityDB(t)
	createCheckTableHelper(t, db)
	store := NewCheckStore(&checkExecFailDB{DB: db})
	runDecisionAuthorityCheck(context.Background(), db, store,
		&Decision{Node: Node{ID: "d1"}, RuleType: "design-system"}, "proposed", "ratified")
}

func TestRunDecisionAuthorityCheckByID_RunAuthorityCheckError_LoggedNotPanicked(t *testing.T) {
	db := setupAuthorityDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	createCheckTableHelper(t, db)
	store := NewCheckStore(&checkExecFailDB{DB: db})
	if err := NewSQLRepo[*Decision](db, Table("smeldr_decisions")).Save(context.Background(), &Decision{
		Node: Node{ID: "d1", Slug: "d1"}, DecisionNumber: "D1", Scope: "core", RuleType: "design-system",
	}); err != nil {
		t.Fatalf("seed Decision: %v", err)
	}
	runDecisionAuthorityCheckByID(context.Background(), db, store, "Decision", "d1", "proposed", "ratified")
}

// — App.Check wiring ————————————————————————————————————————————————————————

// TestApp_Check_WiresIntoTransitionItem confirms App.Check's own contract
// end to end: the *App return value chains, and a configured CheckStore
// produces a real record via App.TransitionItem's own
// runDecisionAuthorityCheckByID call site (state.go), not just via the
// unit-level wiring tests above.
func TestApp_Check_WiresIntoTransitionItem(t *testing.T) {
	app, db, rs := setupTransitionItemApp(t)
	if err := CreateAuthorityTables(db); err != nil {
		t.Fatalf("CreateAuthorityTables: %v", err)
	}
	createCheckTableHelper(t, db)
	store := NewCheckStore(db)
	if got := app.Check(store); got != app {
		t.Error("App.Check should return the same *App for chaining")
	}
	insertDecision(t, db, "dec-check-1", "dec-check-1-slug", "proposed")
	tokenID := setupTokenWithRole(t, db, rs, "admin")

	if _, err := app.TransitionItem(NewTestContext(User{ID: tokenID}), "Decision", "dec-check-1-slug", "ratified"); err != nil {
		t.Fatalf("TransitionItem: %v", err)
	}
	_, found, err := store.Last(context.Background(), "Decision", "dec-check-1")
	if err != nil {
		t.Fatalf("Last: %v", err)
	}
	if !found {
		t.Error("expected App.Check's wiring to have produced a Check record via TransitionItem")
	}
}
