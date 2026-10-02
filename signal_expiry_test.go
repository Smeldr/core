package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// insertExpiryTestSignal inserts a smeldr_signals row with a CreatedAt
// backdated by age, for [App.ExpireSignals] test fixtures. Returns the new
// row's id (also used as its slug).
func insertExpiryTestSignal(t *testing.T, db *sql.DB, signalType, status, receiver string, age time.Duration) string {
	t.Helper()
	id := NewID()
	createdAt := time.Now().UTC().Add(-age)
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO smeldr_signals (id, slug, status, created_at, updated_at, sender, receiver, signal_type, message, task_ref, sequence)
		 VALUES (?, ?, ?, ?, ?, 'system', ?, ?, 'test message', '', 0)`,
		id, id, status, createdAt, createdAt, receiver, signalType,
	); err != nil {
		t.Fatalf("insertExpiryTestSignal: %v", err)
	}
	return id
}

func signalStatus(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	var status string
	if err := db.QueryRowContext(context.Background(),
		`SELECT status FROM smeldr_signals WHERE id = ?`, id,
	).Scan(&status); err != nil {
		t.Fatalf("signalStatus: %v", err)
	}
	return status
}

func TestExpireSignals_NilDB(t *testing.T) {
	app := &App{cfg: Config{DB: nil}}
	walked, expired, skipped, err := app.ExpireSignals(context.Background(), SignalExpiryConfig{})
	if err != nil {
		t.Fatalf("nil DB: expected nil error, got %v", err)
	}
	if walked != 0 || expired != 0 || skipped != 0 {
		t.Errorf("nil DB: expected (0,0,0), got (%d,%d,%d)", walked, expired, skipped)
	}
}

func TestExpireSignals_TableMissing(t *testing.T) {
	db := newSQLiteDB(t)
	app := &App{cfg: Config{DB: db}}
	walked, expired, skipped, err := app.ExpireSignals(context.Background(), SignalExpiryConfig{})
	if err != nil {
		t.Fatalf("table missing: expected nil (fail-open), got %v", err)
	}
	if walked != 0 || expired != 0 || skipped != 0 {
		t.Errorf("table missing: expected (0,0,0), got (%d,%d,%d)", walked, expired, skipped)
	}
}

type signalExpiryQueryFailDB struct {
	DB
	n int
}

func (d *signalExpiryQueryFailDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	d.n++
	if d.n == 1 {
		return nil, errors.New("simulated select error")
	}
	return d.DB.QueryContext(ctx, q, args...)
}

func TestExpireSignals_QueryError(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	app := &App{cfg: Config{DB: &signalExpiryQueryFailDB{DB: db}}}
	if _, _, _, err := app.ExpireSignals(context.Background(), SignalExpiryConfig{}); err == nil {
		t.Error("expected error from failed query, got nil")
	}
}

type signalExpiryScanFailDB struct {
	DB
	n int
}

func (d *signalExpiryScanFailDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	d.n++
	if d.n == 1 {
		// One column only; ExpireSignals scans five → scan error.
		return sql.OpenDB(&scanErrConnector{}).QueryContext(ctx, "SELECT v")
	}
	return d.DB.QueryContext(ctx, q, args...)
}

func TestExpireSignals_ScanError(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	app := &App{cfg: Config{DB: &signalExpiryScanFailDB{DB: db}}}
	walked, expired, skipped, err := app.ExpireSignals(context.Background(), SignalExpiryConfig{})
	if err != nil {
		t.Fatalf("scanError: unexpected error: %v", err)
	}
	if walked != 1 || expired != 0 || skipped != 1 {
		t.Errorf("scanError: expected (1,0,1), got (%d,%d,%d)", walked, expired, skipped)
	}
}

type signalExpiryRowsErrDB struct {
	DB
	n int
}

func (d *signalExpiryRowsErrDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	d.n++
	if d.n == 1 {
		return sql.OpenDB(&rowsErrConnector{}).QueryContext(ctx, "SELECT v1, v2")
	}
	return d.DB.QueryContext(ctx, q, args...)
}

func TestExpireSignals_RowsIterationError(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	app := &App{cfg: Config{DB: &signalExpiryRowsErrDB{DB: db}}}
	if _, _, _, err := app.ExpireSignals(context.Background(), SignalExpiryConfig{}); err == nil {
		t.Error("expected error from rows.Err(), got nil")
	}
}

func TestExpireSignals_ExcludesAuthorizationRequired(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	id := insertExpiryTestSignal(t, db, "authorization-required", "pending", "reviewer", 30*24*time.Hour)
	app := &App{cfg: Config{DB: db}}
	walked, expired, skipped, err := app.ExpireSignals(context.Background(), SignalExpiryConfig{})
	if err != nil {
		t.Fatalf("ExpireSignals: %v", err)
	}
	if walked != 1 || expired != 0 || skipped != 1 {
		t.Errorf("expected (1,0,1), got (%d,%d,%d)", walked, expired, skipped)
	}
	if got := signalStatus(t, db, id); got != "pending" {
		t.Errorf("status = %q, want unchanged \"pending\"", got)
	}
}

func TestExpireSignals_ExcludesReviewRequested(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	id := insertExpiryTestSignal(t, db, "review-requested", "read", "brand", 30*24*time.Hour)
	app := &App{cfg: Config{DB: db}}
	_, expired, skipped, err := app.ExpireSignals(context.Background(), SignalExpiryConfig{})
	if err != nil {
		t.Fatalf("ExpireSignals: %v", err)
	}
	if expired != 0 || skipped != 1 {
		t.Errorf("expected (expired=0,skipped=1), got (%d,%d)", expired, skipped)
	}
	if got := signalStatus(t, db, id); got != "read" {
		t.Errorf("status = %q, want unchanged \"read\"", got)
	}
}

func TestExpireSignals_ExcludesConflictDetected(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	id := insertExpiryTestSignal(t, db, "conflict-detected", "pending", decisionRatifyOperation, 30*24*time.Hour)
	app := &App{cfg: Config{DB: db}}
	_, expired, skipped, err := app.ExpireSignals(context.Background(), SignalExpiryConfig{})
	if err != nil {
		t.Fatalf("ExpireSignals: %v", err)
	}
	if expired != 0 || skipped != 1 {
		t.Errorf("expected (expired=0,skipped=1), got (%d,%d)", expired, skipped)
	}
	if got := signalStatus(t, db, id); got != "pending" {
		t.Errorf("status = %q, want unchanged \"pending\"", got)
	}
}

func TestExpireSignals_CustomExcludeType(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	id := insertExpiryTestSignal(t, db, "backlog-nudge", "pending", "core", 30*24*time.Hour)
	app := &App{cfg: Config{DB: db}}
	_, expired, skipped, err := app.ExpireSignals(context.Background(), SignalExpiryConfig{ExcludeTypes: []string{"backlog-nudge"}})
	if err != nil {
		t.Fatalf("ExpireSignals: %v", err)
	}
	if expired != 0 || skipped != 1 {
		t.Errorf("expected (expired=0,skipped=1), got (%d,%d)", expired, skipped)
	}
	if got := signalStatus(t, db, id); got != "pending" {
		t.Errorf("status = %q, want unchanged \"pending\"", got)
	}
}

func TestExpireSignals_MandatoryExclusionsSurviveCustomConfig(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	ids := map[string]string{
		"authorization-required": insertExpiryTestSignal(t, db, "authorization-required", "pending", "r", 30*24*time.Hour),
		"review-requested":       insertExpiryTestSignal(t, db, "review-requested", "pending", "r", 30*24*time.Hour),
		"conflict-detected":      insertExpiryTestSignal(t, db, "conflict-detected", "pending", "r", 30*24*time.Hour),
	}
	app := &App{cfg: Config{DB: db}}
	// A caller's own ExcludeTypes names something unrelated; the mandatory
	// three must still be excluded regardless.
	_, expired, skipped, err := app.ExpireSignals(context.Background(), SignalExpiryConfig{ExcludeTypes: []string{"backlog-nudge"}})
	if err != nil {
		t.Fatalf("ExpireSignals: %v", err)
	}
	if expired != 0 || skipped != 3 {
		t.Errorf("expected (expired=0,skipped=3), got (%d,%d)", expired, skipped)
	}
	for signalType, id := range ids {
		if got := signalStatus(t, db, id); got != "pending" {
			t.Errorf("%s: status = %q, want unchanged \"pending\"", signalType, got)
		}
	}
}

func TestExpireSignals_BatchCapEnforced(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	oldest := insertExpiryTestSignal(t, db, "notice", "pending", "core", 30*24*time.Hour)
	middle := insertExpiryTestSignal(t, db, "notice", "pending", "core", 20*24*time.Hour)
	newest := insertExpiryTestSignal(t, db, "notice", "pending", "core", 15*24*time.Hour)

	app := &App{cfg: Config{DB: db}}
	walked, expired, skipped, err := app.ExpireSignals(context.Background(), SignalExpiryConfig{BatchCap: 2})
	if err != nil {
		t.Fatalf("ExpireSignals: %v", err)
	}
	if walked != 3 || expired != 2 || skipped != 1 {
		t.Errorf("expected (3,2,1), got (%d,%d,%d)", walked, expired, skipped)
	}
	if got := signalStatus(t, db, oldest); got != "expired" {
		t.Errorf("oldest: status = %q, want \"expired\"", got)
	}
	if got := signalStatus(t, db, middle); got != "expired" {
		t.Errorf("middle: status = %q, want \"expired\"", got)
	}
	if got := signalStatus(t, db, newest); got != "pending" {
		t.Errorf("newest: status = %q, want unchanged \"pending\" (batch cap reached)", got)
	}
}

func TestExpireSignals_BatchCapUnlimited(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	var ids []string
	for i := 0; i < 3; i++ {
		ids = append(ids, insertExpiryTestSignal(t, db, "notice", "pending", "core", 30*24*time.Hour))
	}
	app := &App{cfg: Config{DB: db}}
	_, expired, skipped, err := app.ExpireSignals(context.Background(), SignalExpiryConfig{BatchCap: -1})
	if err != nil {
		t.Fatalf("ExpireSignals: %v", err)
	}
	if expired != 3 || skipped != 0 {
		t.Errorf("expected (expired=3,skipped=0), got (%d,%d)", expired, skipped)
	}
	for _, id := range ids {
		if got := signalStatus(t, db, id); got != "expired" {
			t.Errorf("id %s: status = %q, want \"expired\"", id, got)
		}
	}
}

func TestExpireSignals_DefaultBatchCap(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	// Well under DefaultSignalExpiryBatchCap (200) — a zero cfg.BatchCap
	// must not zero out expiry entirely.
	id := insertExpiryTestSignal(t, db, "notice", "pending", "core", 30*24*time.Hour)
	app := &App{cfg: Config{DB: db}}
	_, expired, _, err := app.ExpireSignals(context.Background(), SignalExpiryConfig{})
	if err != nil {
		t.Fatalf("ExpireSignals: %v", err)
	}
	if expired != 1 {
		t.Errorf("expected expired=1 under the default batch cap, got %d", expired)
	}
	if got := signalStatus(t, db, id); got != "expired" {
		t.Errorf("status = %q, want \"expired\"", got)
	}
}

func TestExpireSignals_DefaultMaxAge(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	tooYoung := insertExpiryTestSignal(t, db, "notice", "pending", "core", 13*24*time.Hour)
	oldEnough := insertExpiryTestSignal(t, db, "notice", "pending", "core", 15*24*time.Hour)
	app := &App{cfg: Config{DB: db}}
	walked, expired, _, err := app.ExpireSignals(context.Background(), SignalExpiryConfig{})
	if err != nil {
		t.Fatalf("ExpireSignals: %v", err)
	}
	if walked != 1 || expired != 1 {
		t.Errorf("expected (walked=1,expired=1), got (%d,%d)", walked, expired)
	}
	if got := signalStatus(t, db, tooYoung); got != "pending" {
		t.Errorf("tooYoung: status = %q, want unchanged \"pending\"", got)
	}
	if got := signalStatus(t, db, oldEnough); got != "expired" {
		t.Errorf("oldEnough: status = %q, want \"expired\"", got)
	}
}

func TestExpireSignals_CustomMaxAge(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	tooYoung := insertExpiryTestSignal(t, db, "notice", "pending", "core", 90*time.Minute)
	oldEnough := insertExpiryTestSignal(t, db, "notice", "pending", "core", 3*time.Hour)
	app := &App{cfg: Config{DB: db}}
	_, expired, _, err := app.ExpireSignals(context.Background(), SignalExpiryConfig{MaxAge: 2 * time.Hour})
	if err != nil {
		t.Fatalf("ExpireSignals: %v", err)
	}
	if expired != 1 {
		t.Errorf("expected expired=1, got %d", expired)
	}
	if got := signalStatus(t, db, tooYoung); got != "pending" {
		t.Errorf("tooYoung: status = %q, want unchanged \"pending\"", got)
	}
	if got := signalStatus(t, db, oldEnough); got != "expired" {
		t.Errorf("oldEnough: status = %q, want \"expired\"", got)
	}
}

func TestExpireSignals_ReadEligible(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	id := insertExpiryTestSignal(t, db, "notice", "read", "core", 30*24*time.Hour)
	app := &App{cfg: Config{DB: db}}
	_, expired, _, err := app.ExpireSignals(context.Background(), SignalExpiryConfig{})
	if err != nil {
		t.Fatalf("ExpireSignals: %v", err)
	}
	if expired != 1 {
		t.Errorf("expected expired=1, got %d", expired)
	}
	if got := signalStatus(t, db, id); got != "expired" {
		t.Errorf("status = %q, want \"expired\"", got)
	}
}

func TestExpireSignals_TerminalStatusesNotWalked(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	insertExpiryTestSignal(t, db, "notice", "acknowledged", "core", 30*24*time.Hour)
	insertExpiryTestSignal(t, db, "notice", "expired", "core", 30*24*time.Hour)
	app := &App{cfg: Config{DB: db}}
	walked, expired, skipped, err := app.ExpireSignals(context.Background(), SignalExpiryConfig{})
	if err != nil {
		t.Fatalf("ExpireSignals: %v", err)
	}
	if walked != 0 || expired != 0 || skipped != 0 {
		t.Errorf("expected (0,0,0) — terminal statuses never selected, got (%d,%d,%d)", walked, expired, skipped)
	}
}

func TestExpireSignals_TooYoungNotWalked(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	insertExpiryTestSignal(t, db, "notice", "pending", "core", 1*time.Hour)
	app := &App{cfg: Config{DB: db}}
	walked, expired, skipped, err := app.ExpireSignals(context.Background(), SignalExpiryConfig{})
	if err != nil {
		t.Fatalf("ExpireSignals: %v", err)
	}
	if walked != 0 || expired != 0 || skipped != 0 {
		t.Errorf("expected (0,0,0) — too young to be selected, got (%d,%d,%d)", walked, expired, skipped)
	}
}

func TestExpireSignals_UpdateError(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	failing := insertExpiryTestSignal(t, db, "notice", "pending", "core", 30*24*time.Hour)
	ok := insertExpiryTestSignal(t, db, "notice", "pending", "core", 25*24*time.Hour)
	// nthExecFailDB (shared with DrainEvalQueue's own tests) fails the Nth
	// ExecContext call; ExpireSignals issues one UPDATE per eligible row in
	// created_at ASC order, so failing the first UPDATE fails "failing"
	// (the oldest) and lets "ok" (the next) succeed.
	app := &App{cfg: Config{DB: &nthExecFailDB{DB: db, fail: 1}}}
	walked, expired, skipped, err := app.ExpireSignals(context.Background(), SignalExpiryConfig{})
	if err != nil {
		t.Fatalf("ExpireSignals: %v", err)
	}
	if walked != 2 || expired != 1 || skipped != 1 {
		t.Errorf("expected (2,1,1), got (%d,%d,%d)", walked, expired, skipped)
	}
	if got := signalStatus(t, db, failing); got != "pending" {
		t.Errorf("failing: status = %q, want unchanged \"pending\"", got)
	}
	if got := signalStatus(t, db, ok); got != "expired" {
		t.Errorf("ok: status = %q, want \"expired\"", got)
	}
}

// signalExpiryConcurrentChangeDB simulates another actor answering the
// target Signal (read -> acknowledged, terminal) in the gap between
// ExpireSignals' own SELECT and its per-row UPDATE — the exact race the
// "AND status = <selected status>" guard exists to catch.
type signalExpiryConcurrentChangeDB struct {
	DB
	signalID  string
	triggered bool
}

func (d *signalExpiryConcurrentChangeDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	if !d.triggered {
		d.triggered = true
		if _, err := d.DB.ExecContext(ctx,
			"UPDATE smeldr_signals SET status = 'acknowledged' WHERE id = ?", d.signalID,
		); err != nil {
			return nil, err
		}
	}
	return d.DB.ExecContext(ctx, q, args...)
}

func TestExpireSignals_RowChangedBetweenSelectAndUpdate_SkippedNoEvent(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	id := insertExpiryTestSignal(t, db, "notice", "read", "architect", 20*24*time.Hour)

	b := newEventBroadcaster()
	streamCh, subErr := b.subscribe("u1", "architect")
	if subErr != nil {
		t.Fatalf("subscribe: %v", subErr)
	}
	defer b.unsubscribe(streamCh)

	app := &App{
		cfg:              Config{DB: &signalExpiryConcurrentChangeDB{DB: db, signalID: id}},
		eventBroadcaster: b,
	}
	walked, expired, skipped, err := app.ExpireSignals(context.Background(), SignalExpiryConfig{})
	if err != nil {
		t.Fatalf("ExpireSignals: %v", err)
	}
	if walked != 1 || expired != 0 || skipped != 1 {
		t.Errorf("expected (1,0,1), got (%d,%d,%d)", walked, expired, skipped)
	}
	// The row must keep the real answer (acknowledged), never be
	// overwritten with expired.
	if got := signalStatus(t, db, id); got != "acknowledged" {
		t.Errorf("status = %q, want the concurrent answer \"acknowledged\" preserved", got)
	}
	select {
	case got := <-streamCh:
		t.Fatalf("expected no signal.transitioned event for a lost-race row, got %q", got)
	default:
	}
}

func TestExpireSignals_LastActorColumnMissing_FailsOpen(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	// Hand-built smeldr_signals without last_actor, simulating a pre-A296
	// (or otherwise unmigrated) install.
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE smeldr_signals (
			id TEXT PRIMARY KEY, slug TEXT NOT NULL UNIQUE, status TEXT NOT NULL DEFAULT 'draft',
			published_at TIMESTAMPTZ, scheduled_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL, rev INTEGER NOT NULL DEFAULT 0,
			sender TEXT NOT NULL DEFAULT '', receiver TEXT NOT NULL DEFAULT '',
			signal_type TEXT NOT NULL DEFAULT '', message TEXT NOT NULL DEFAULT '',
			task_ref TEXT NOT NULL DEFAULT '', sequence INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		t.Fatalf("create smeldr_signals: %v", err)
	}
	id := insertExpiryTestSignal(t, db, "notice", "pending", "core", 30*24*time.Hour)
	app := &App{cfg: Config{DB: db}}
	_, expired, skipped, err := app.ExpireSignals(ctx, SignalExpiryConfig{})
	if err != nil {
		t.Fatalf("ExpireSignals: %v", err)
	}
	if expired != 1 || skipped != 0 {
		t.Errorf("expected (expired=1,skipped=0) via the two-column fallback, got (%d,%d)", expired, skipped)
	}
	if got := signalStatus(t, db, id); got != "expired" {
		t.Errorf("status = %q, want \"expired\"", got)
	}
}

func TestExpireSignals_LastActorColumnMissing_FallbackAlsoFails(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE smeldr_signals (
			id TEXT PRIMARY KEY, slug TEXT NOT NULL UNIQUE, status TEXT NOT NULL DEFAULT 'draft',
			published_at TIMESTAMPTZ, scheduled_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL, rev INTEGER NOT NULL DEFAULT 0,
			sender TEXT NOT NULL DEFAULT '', receiver TEXT NOT NULL DEFAULT '',
			signal_type TEXT NOT NULL DEFAULT '', message TEXT NOT NULL DEFAULT '',
			task_ref TEXT NOT NULL DEFAULT '', sequence INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		t.Fatalf("create smeldr_signals: %v", err)
	}
	id := insertExpiryTestSignal(t, db, "notice", "pending", "core", 30*24*time.Hour)
	// The 1st ExecContext call is the primary (3-column) UPDATE, which
	// fails naturally (real isNoSuchColumn error, last_actor absent) and
	// triggers the two-column fallback as its own, 2nd ExecContext call —
	// which this wrapper also fails, simulating both UPDATE attempts
	// failing for one row.
	app := &App{cfg: Config{DB: &nthExecFailDB{DB: db, fail: 2}}}
	walked, expired, skipped, err := app.ExpireSignals(ctx, SignalExpiryConfig{})
	if err != nil {
		t.Fatalf("ExpireSignals: %v", err)
	}
	if walked != 1 || expired != 0 || skipped != 1 {
		t.Errorf("expected (1,0,1), got (%d,%d,%d)", walked, expired, skipped)
	}
	if got := signalStatus(t, db, id); got != "pending" {
		t.Errorf("status = %q, want unchanged \"pending\"", got)
	}
}

func TestExpireSignals_StampsActorAndReason(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	id := insertExpiryTestSignal(t, db, "notice", "pending", "core", 20*24*time.Hour)
	app := &App{cfg: Config{DB: db}}
	if _, _, _, err := app.ExpireSignals(context.Background(), SignalExpiryConfig{}); err != nil {
		t.Fatalf("ExpireSignals: %v", err)
	}
	var lastActor string
	if err := db.QueryRowContext(context.Background(),
		`SELECT last_actor FROM smeldr_signals WHERE id = ?`, id,
	).Scan(&lastActor); err != nil {
		t.Fatalf("SELECT last_actor: %v", err)
	}
	if lastActor != "signal-expiry-sweep" {
		t.Errorf("last_actor = %q, want %q", lastActor, "signal-expiry-sweep")
	}
}

func TestExpireSignals_NotStreamed_WebhookStillEnqueued(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	insertExpiryTestSignal(t, db, "notice", "pending", "architect", 20*24*time.Hour)

	b := newEventBroadcaster()
	streamCh, subErr := b.subscribe("u1", "architect")
	if subErr != nil {
		t.Fatalf("subscribe: %v", subErr)
	}
	defer b.unsubscribe(streamCh)
	otherCh, subErr := b.subscribe("u2", "core")
	if subErr != nil {
		t.Fatalf("subscribe core: %v", subErr)
	}
	defer b.unsubscribe(otherCh)
	allCh, subErr := b.subscribe("u3", eventStreamChannelAll)
	if subErr != nil {
		t.Fatalf("subscribe all: %v", subErr)
	}
	defer b.unsubscribe(allCh)

	pool, store := outboundTestDB(t)
	createWebhookEndpointsTable(t, store.db)
	if _, _, err := store.Create(context.Background(), "https://8.8.8.8/hook", []string{"signal.transitioned"}); err != nil {
		t.Fatalf("Create webhook endpoint: %v", err)
	}

	app := &App{cfg: Config{DB: db}, eventBroadcaster: b, webhookStore: store, webhookPool: pool}
	if _, expired, _, err := app.ExpireSignals(context.Background(), SignalExpiryConfig{}); err != nil || expired != 1 {
		t.Fatalf("ExpireSignals: expired=%d err=%v, want 1 expired", expired, err)
	}

	// A signal.transitioned event is no longer published to the live stream,
	// on the row's own receiver channel or anywhere else: only the receiver
	// ever moves a Signal, so it could only echo back to its own session.
	for name, ch := range map[string]chan []byte{"architect": streamCh, "core": otherCh, "all": allCh} {
		select {
		case got := <-ch:
			t.Errorf("%s subscriber: expected no stream delivery for an expiry, got %q", name, got)
		default:
		}
	}
	// The webhook sink is independent and still fires.
	endpoints, err := store.EndpointsForEvent(context.Background(), "signal.transitioned")
	if err != nil || len(endpoints) != 1 {
		t.Fatalf("EndpointsForEvent: %v, endpoints=%d", err, len(endpoints))
	}
	jobs, err := pool.ListJobsForEndpoint(context.Background(), endpoints[0].ID)
	if err != nil {
		t.Fatalf("ListJobsForEndpoint: %v", err)
	}
	if len(jobs) != 1 || jobs[0].Event != "signal.transitioned" {
		t.Fatalf("jobs = %+v, want exactly one signal.transitioned webhook job", jobs)
	}
	var payload WebhookEventPayload
	if err := json.Unmarshal(jobs[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal webhook payload: %v", err)
	}
	var data transitionWebhookData
	if err := json.Unmarshal(payload.Data, &data); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	if data.ToState != "expired" {
		t.Errorf("webhook ToState = %q, want %q", data.ToState, "expired")
	}
}
