package smeldr

// Error-path coverage for relations.go. Uses mock DB types defined in
// coverage_test.go and auth_test.go (same package).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// errBeginTxDB implements DB + txBeginner but always fails BeginTx.
type errBeginTxDB struct{}

func (d *errBeginTxDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return nil, nil
}
func (d *errBeginTxDB) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, nil
}
func (d *errBeginTxDB) QueryRowContext(ctx context.Context, _ string, _ ...any) *sql.Row {
	conn := &guardRowConn{noRow: true}
	return sql.OpenDB(conn).QueryRowContext(ctx, "SELECT v")
}
func (d *errBeginTxDB) BeginTx(_ context.Context, _ *sql.TxOptions) (*sql.Tx, error) {
	return nil, errors.New("begin tx error")
}

// mockRelationStore builds a *RelationStore with the given DB, bypassing
// NewRelationStore (which calls QueryContext for loadRegistry).
func mockRelationStore(db DB) *RelationStore {
	return &RelationStore{
		db:       db,
		registry: &RelationKindRegistry{kinds: make(map[string]RelationKindDef)},
	}
}

// — CreateRelationTables error paths ———————————————————————————————————————

func TestCreateRelationTables_ExecError1(t *testing.T) {
	err := CreateRelationTables(&failOnNthExecDB{failAt: 1})
	if err == nil {
		t.Error("want error on first ExecContext, got nil")
	}
}

func TestCreateRelationTables_ExecError2(t *testing.T) {
	err := CreateRelationTables(&failOnNthExecDB{failAt: 2})
	if err == nil {
		t.Error("want error on second ExecContext, got nil")
	}
}

func TestCreateRelationTables_ExecError3(t *testing.T) {
	err := CreateRelationTables(&failOnNthExecDB{failAt: 3})
	if err == nil {
		t.Error("want error on third ExecContext, got nil")
	}
}

func TestCreateRelationTables_ExecError4(t *testing.T) {
	err := CreateRelationTables(&failOnNthExecDB{failAt: 4})
	if err == nil {
		t.Error("want error on fourth ExecContext, got nil")
	}
}

func TestCreateRelationTables_ExecError5(t *testing.T) {
	err := CreateRelationTables(&failOnNthExecDB{failAt: 5})
	if err == nil {
		t.Error("want error on fifth ExecContext, got nil")
	}
}

// — NewRelationStore / loadRegistry error paths ————————————————————————————

func TestNewRelationStore_LoadRegistryQueryError(t *testing.T) {
	_, err := NewRelationStore(&errQueryDB{})
	if err == nil {
		t.Error("want error when loadRegistry QueryContext fails, got nil")
	}
}

// — ValidateRelationKindDef ————————————————————————————————————————————————

func TestValidateRelationKindDef_InvalidTypePairs(t *testing.T) {
	def := RelationKindDef{
		TypeName:  "test",
		Mode:      "asserted",
		TypePairs: json.RawMessage(`not valid json`),
	}
	err := ValidateRelationKindDef(def)
	if err == nil {
		t.Error("want error for invalid type_pairs JSON, got nil")
	}
}

// — UpsertKind error path ——————————————————————————————————————————————————

func TestUpsertKind_ExecError(t *testing.T) {
	s := mockRelationStore(&errExecDB{})
	err := s.UpsertKind(context.Background(), RelationKindDef{TypeName: "t", Mode: "asserted"})
	if err == nil {
		t.Error("want DB exec error from UpsertKind, got nil")
	}
}

// — insertEdge error path ——————————————————————————————————————————————————

func TestInsertEdge_ExecError(t *testing.T) {
	s := mockRelationStore(&errExecDB{})
	_, err := s.insertEdge(context.Background(), RelationEdge{
		SourceType: "a", SourceID: "1", TargetType: "b", TargetID: "2",
		RelationKind: "rel", EdgeClass: "asserted",
	})
	if err == nil {
		t.Error("want DB exec error from insertEdge, got nil")
	}
}

// — MCPProposeRelation unknown kind ————————————————————————————————————————

func TestMCPProposeRelation_UnknownKind(t *testing.T) {
	store := setupMCPRelations(t)
	_, err := store.MCPProposeRelation(context.Background(), "a", "1", "b", "2", "nonexistent", nil, nil, nil, nil)
	if err != ErrNotFound {
		t.Errorf("want ErrNotFound for unknown kind, got %v", err)
	}
}

// — MCPGetRelations "both" error paths ————————————————————————————————————

func TestMCPGetRelations_Both_SourceError(t *testing.T) {
	s := mockRelationStore(&errQueryDB{})
	_, err := s.MCPGetRelations(context.Background(), "article", "art-1", "both", "")
	if err == nil {
		t.Error("want error when GetBySource fails in both direction, got nil")
	}
}

// — GetBySource / GetByTarget query error paths ————————————————————————————

func TestGetBySource_QueryError(t *testing.T) {
	s := mockRelationStore(&errQueryDB{})
	_, err := s.GetBySource(context.Background(), "a", "1", "")
	if err == nil {
		t.Error("want DB query error from GetBySource, got nil")
	}
}

func TestGetByTarget_QueryError(t *testing.T) {
	s := mockRelationStore(&errQueryDB{})
	_, err := s.GetByTarget(context.Background(), "a", "1", "")
	if err == nil {
		t.Error("want DB query error from GetByTarget, got nil")
	}
}

// — scanEdge: InvalidAt field path —————————————————————————————————————————

func TestAssert_WithInvalidAt_RoundTrip(t *testing.T) {
	store := setupMCPRelations(t)
	upsertTestKind(t, store, "tagged", "article", "tag")

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	invalidAt := now.Add(24 * time.Hour)
	if err := store.Assert(ctx, RelationEdge{
		SourceType: "article", SourceID: "art-1",
		TargetType: "tag", TargetID: "tag-1",
		RelationKind: "tagged", EdgeClass: "asserted",
		InvalidAt: &invalidAt,
	}); err != nil {
		t.Fatalf("Assert: %v", err)
	}
	edges, err := store.GetBySource(ctx, "article", "art-1", "tagged")
	if err != nil {
		t.Fatalf("GetBySource: %v", err)
	}
	if len(edges) != 1 || edges[0].InvalidAt == nil || !edges[0].InvalidAt.Equal(invalidAt) {
		t.Errorf("InvalidAt not round-tripped: %+v", edges)
	}
}

// — RecomputeAsserted / BulkRecompute query error paths ————————————————————

func TestRecomputeAsserted_QueryError(t *testing.T) {
	s := mockRelationStore(&errQueryDB{})
	err := s.RecomputeAsserted(context.Background(), "a", "1", nil)
	if err == nil {
		t.Error("want DB query error from RecomputeAsserted, got nil")
	}
}

func TestBulkRecompute_QueryError(t *testing.T) {
	s := mockRelationStore(&errQueryDB{})
	err := s.BulkRecompute(context.Background(), []RelationSource{
		{SourceType: "a", SourceID: "1", Incoming: nil},
	})
	if err == nil {
		t.Error("want DB query error from BulkRecompute, got nil")
	}
}

// — applyRelationDiff error paths ——————————————————————————————————————————

// BeginTx error: s.db implements txBeginner but fails.
func TestApplyRelationDiff_BeginTxError(t *testing.T) {
	s := mockRelationStore(&errBeginTxDB{})
	err := s.applyRelationDiff(context.Background(), &errExecDB{}, []string{"id-1"}, nil, "a", "1")
	if err == nil {
		t.Error("want BeginTx error from applyRelationDiff, got nil")
	}
}

// DELETE error: s.db does NOT implement txBeginner so exec = the db param;
// db param returns error on ExecContext.
func TestApplyRelationDiff_DeleteError(t *testing.T) {
	s := mockRelationStore(&errQueryDB{}) // errQueryDB has no BeginTx
	err := s.applyRelationDiff(context.Background(), &errExecDB{}, []string{"id-1"}, nil, "a", "1")
	if err == nil {
		t.Error("want DELETE exec error from applyRelationDiff, got nil")
	}
}

// INSERT error: s.db does NOT implement txBeginner; toDelete empty, toInsert non-empty.
func TestApplyRelationDiff_InsertError(t *testing.T) {
	s := mockRelationStore(&errQueryDB{}) // no BeginTx
	toInsert := []RelationEdge{{
		SourceType: "a", SourceID: "1", TargetType: "b", TargetID: "2",
		RelationKind: "rel", EdgeClass: "asserted",
	}}
	err := s.applyRelationDiff(context.Background(), &errExecDB{}, nil, toInsert, "a", "1")
	if err == nil {
		t.Error("want INSERT exec error from applyRelationDiff, got nil")
	}
}

// — buildCascadeHandler GetByTarget error path ———————————————————————————

func TestBuildCascadeHandler_GetByTargetError(t *testing.T) {
	s := mockRelationStore(&errQueryDB{})
	app := &App{}
	handler := buildCascadeHandler(s, app)
	err := handler(context.Background(), SignalEvent{Type: "article", NodeID: "art-1"})
	if err == nil {
		t.Error("want GetByTarget error from cascade handler, got nil")
	}
}

// — applyRelationDiff: commit no-op path (non-txBeginner, empty diff) ————

func TestApplyRelationDiff_CommitNoOp(t *testing.T) {
	// errQueryDB has no BeginTx → commit stays as the default no-op closure.
	// Empty toDelete + toInsert means no loops run; only commit() is called.
	s := mockRelationStore(&errQueryDB{})
	err := s.applyRelationDiff(context.Background(), &errExecDB{}, nil, nil, "a", "1")
	if err != nil {
		t.Errorf("want nil for empty diff with no-op commit, got %v", err)
	}
}

// — buildCascadeHandler: visited deduplication ——————————————————————————

func TestBuildCascadeHandler_VisitedDedup(t *testing.T) {
	store := setupMCPRelations(t)
	ctx := context.Background()

	// Register two kinds that allow article→tag edges.
	for _, kind := range []string{"tagged", "categorized"} {
		upsertTestKind(t, store, kind, "article", "tag")
	}

	// Assert two edges from the SAME source to the SAME target but different kinds.
	// GetByTarget will return both; the second hits the visited-dedup continue.
	for _, kind := range []string{"tagged", "categorized"} {
		if err := store.Assert(ctx, RelationEdge{
			SourceType: "article", SourceID: "art-1",
			TargetType: "tag", TargetID: "tag-1",
			RelationKind: kind, EdgeClass: "asserted",
		}); err != nil {
			t.Fatalf("Assert %s: %v", kind, err)
		}
	}

	app := &App{}
	handler := buildCascadeHandler(store, app)
	if err := handler(ctx, SignalEvent{Type: "tag", NodeID: "tag-1"}); err != nil {
		t.Errorf("cascade handler: %v", err)
	}
	// Let the 500ms debouncer fire and complete before test cleanup.
	time.Sleep(600 * time.Millisecond)
}
