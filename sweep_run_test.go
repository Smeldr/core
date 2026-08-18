package smeldr

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

// sweepRunExecFailDB fails every ExecContext call; QueryContext/QueryRowContext
// pass through to the wrapped DB unchanged.
type sweepRunExecFailDB struct {
	DB
}

func (d *sweepRunExecFailDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return nil, errRepoError
}

// sweepRunQueryFailDB fails every QueryContext call; ExecContext passes
// through to the wrapped DB unchanged.
type sweepRunQueryFailDB struct {
	DB
}

func (d *sweepRunQueryFailDB) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, errRepoError
}

func createSweepRunTableHelper(t *testing.T, db DB) {
	t.Helper()
	if err := CreateSweepRunTable(db); err != nil {
		t.Fatalf("CreateSweepRunTable: %v", err)
	}
}

func TestCreateSweepRunTable_Idempotent(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateSweepRunTable(db); err != nil {
		t.Fatalf("first CreateSweepRunTable: %v", err)
	}
	if err := CreateSweepRunTable(db); err != nil {
		t.Fatalf("second CreateSweepRunTable (should be idempotent): %v", err)
	}
}

func TestSweepRunStore_AppendAndLast(t *testing.T) {
	db := newSQLiteDB(t)
	createSweepRunTableHelper(t, db)
	store := NewSweepRunStore(db)
	ctx := context.Background()

	r := SweepRunRecord{
		ID:       "run-1",
		Detector: "structural",
		RanAt:    time.Now().UTC().Truncate(time.Second),
		Interval: "0 * * * *",
		Walked:   5,
		Flagged:  1,
		Skipped:  0,
		Err:      "",
	}
	if err := store.Append(ctx, r); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, found, err := store.Last(ctx, "structural")
	if err != nil {
		t.Fatalf("Last: %v", err)
	}
	if !found {
		t.Fatal("Last: found = false, want true")
	}
	if got.ID != r.ID || got.Detector != r.Detector || got.Interval != r.Interval ||
		got.Walked != r.Walked || got.Flagged != r.Flagged || got.Skipped != r.Skipped || got.Err != r.Err {
		t.Errorf("Last: got %+v, want %+v", got, r)
	}
	if !got.RanAt.Equal(r.RanAt) {
		t.Errorf("Last: RanAt = %v, want %v", got.RanAt, r.RanAt)
	}
}

func TestSweepRunStore_AppendError(t *testing.T) {
	db := newSQLiteDB(t)
	createSweepRunTableHelper(t, db)
	store := NewSweepRunStore(&sweepRunExecFailDB{DB: db})

	err := store.Append(context.Background(), SweepRunRecord{ID: "run-1", Detector: "structural", RanAt: time.Now()})
	if err == nil {
		t.Fatal("Append: want error, got nil")
	}
}

func TestSweepRunStore_LastNotFound(t *testing.T) {
	db := newSQLiteDB(t)
	createSweepRunTableHelper(t, db)
	store := NewSweepRunStore(db)

	_, found, err := store.Last(context.Background(), "structural")
	if err != nil {
		t.Fatalf("Last: want nil error for no rows, got %v", err)
	}
	if found {
		t.Error("Last: found = true, want false for a detector with no runs yet")
	}
}

func TestSweepRunStore_LastError(t *testing.T) {
	db := newSQLiteDB(t)
	createSweepRunTableHelper(t, db)
	store := NewSweepRunStore(&sweepRunQueryFailDB{DB: db})

	_, _, err := store.Last(context.Background(), "structural")
	if err == nil {
		t.Fatal("Last: want error, got nil")
	}
}

func TestSweepRunStore_ListOrderAndLimit(t *testing.T) {
	db := newSQLiteDB(t)
	createSweepRunTableHelper(t, db)
	store := NewSweepRunStore(db)
	ctx := context.Background()

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for i, id := range []string{"run-1", "run-2", "run-3"} {
		r := SweepRunRecord{
			ID: id, Detector: "structural",
			RanAt: base.Add(time.Duration(i) * time.Hour),
		}
		if err := store.Append(ctx, r); err != nil {
			t.Fatalf("Append %s: %v", id, err)
		}
	}
	// A different detector must not appear in "structural"'s list.
	if err := store.Append(ctx, SweepRunRecord{ID: "eq-1", Detector: "eval-queue", RanAt: base}); err != nil {
		t.Fatalf("Append eq-1: %v", err)
	}

	all, err := store.List(ctx, "structural", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("List: got %d records, want 3", len(all))
	}
	// Newest first: run-3, run-2, run-1.
	wantOrder := []string{"run-3", "run-2", "run-1"}
	for i, want := range wantOrder {
		if all[i].ID != want {
			t.Errorf("List[%d].ID = %q, want %q", i, all[i].ID, want)
		}
	}

	limited, err := store.List(ctx, "structural", 2)
	if err != nil {
		t.Fatalf("List with limit: %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("List with limit=2: got %d records, want 2", len(limited))
	}
	if limited[0].ID != "run-3" || limited[1].ID != "run-2" {
		t.Errorf("List with limit=2: got %+v", limited)
	}
}

func TestSweepRunStore_ListEmpty(t *testing.T) {
	db := newSQLiteDB(t)
	createSweepRunTableHelper(t, db)
	store := NewSweepRunStore(db)

	records, err := store.List(context.Background(), "structural", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("List: got %d records, want 0", len(records))
	}
}

func TestSweepRunStore_ListError(t *testing.T) {
	db := newSQLiteDB(t)
	createSweepRunTableHelper(t, db)
	store := NewSweepRunStore(&sweepRunQueryFailDB{DB: db})

	_, err := store.List(context.Background(), "structural", 0)
	if err == nil {
		t.Fatal("List: want error, got nil")
	}
}

// sweepRunScanFailDB swaps any SELECT against smeldr_sweep_runs for a query
// with the wrong column count, so the caller's Scan fails — proving a
// row-scan failure is wrapped and returned, not swallowed.
type sweepRunScanFailDB struct {
	DB
}

func (d *sweepRunScanFailDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if strings.Contains(q, "smeldr_sweep_runs") {
		return d.DB.QueryContext(ctx, "SELECT 1")
	}
	return d.DB.QueryContext(ctx, q, args...)
}

func TestSweepRunStore_LastScanError(t *testing.T) {
	db := newSQLiteDB(t)
	createSweepRunTableHelper(t, db)
	store := NewSweepRunStore(db)
	if err := store.Append(context.Background(), SweepRunRecord{ID: "run-1", Detector: "structural", RanAt: time.Now()}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	badStore := NewSweepRunStore(&sweepRunScanFailDB{DB: db})
	_, _, err := badStore.Last(context.Background(), "structural")
	if err == nil {
		t.Fatal("Last: want scan error, got nil")
	}
}

func TestSweepRunStore_ListScanError(t *testing.T) {
	db := newSQLiteDB(t)
	createSweepRunTableHelper(t, db)
	store := NewSweepRunStore(db)
	if err := store.Append(context.Background(), SweepRunRecord{ID: "run-1", Detector: "structural", RanAt: time.Now()}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	badStore := NewSweepRunStore(&sweepRunScanFailDB{DB: db})
	_, err := badStore.List(context.Background(), "structural", 0)
	if err == nil {
		t.Fatal("List: want scan error, got nil")
	}
}
