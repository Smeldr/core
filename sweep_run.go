package smeldr

import (
	"context"
	"fmt"
	"time"
)

// SweepRunRecord is one immutable record of a completed scheduled detector
// run — persisted so a clean sweep leaves a trace distinguishable from no
// sweep having run at all (T223).
type SweepRunRecord struct {
	ID       string    `json:"id"`       // UUID v7, see [NewID]
	Detector string    `json:"detector"` // stable name, e.g. "structural", "eval-queue"
	RanAt    time.Time `json:"ran_at"`
	Interval string    `json:"interval"` // the detector's own declared cron schedule
	Walked   int       `json:"walked"`   // total items examined this run
	Flagged  int       `json:"flagged"`  // e.g. relations invalidated / items transitioned
	Skipped  int       `json:"skipped"`
	Err      string    `json:"err"` // non-empty when the run itself returned an error
}

// SweepRunStore is the persistence interface for scheduled detector run
// records. Implement it to use a custom storage backend; use
// [NewSweepRunStore] for the default SQLite/Postgres-compatible
// implementation.
//
// SweepRunStore has no HTTP or MCP surface by design — [Last] and [List]
// are for staleness derivation from Go code (e.g. a future alerting task),
// not human browsing. See [App.SweepStructural] and [App.DrainEvalQueue]
// for the detector functions this is intended to record.
//
// # Wiring a scheduled detector to record its own runs
//
// smeldr.dev/agent's NewSweepScheduler deliberately takes no [SweepRunStore]
// parameter — the base smeldr.dev/agent package is dependency-free by
// design, and a store parameter would force a smeldr.dev/core import into
// it. Record runs by wrapping the detector function at the call site
// instead, where both packages are already imported:
//
//	sweepFn := func(ctx context.Context) (int, int, int, error) {
//	    walked, flagged, skipped, err := app.SweepStructural(ctx)
//	    errStr := ""
//	    if err != nil {
//	        errStr = err.Error()
//	    }
//	    _ = runStore.Append(ctx, smeldr.SweepRunRecord{
//	        ID: smeldr.NewID(), Detector: "structural", RanAt: time.Now().UTC(),
//	        Interval: "0 * * * *", Walked: walked, Flagged: flagged, Skipped: skipped, Err: errStr,
//	    })
//	    return walked, flagged, skipped, err
//	}
//	sweep, err := agent.NewSweepScheduler("0 * * * *", "UTC", sweepFn)
type SweepRunStore interface {
	// Append persists r.
	Append(ctx context.Context, r SweepRunRecord) error
	// Last returns the most recent record for detector, ordered by RanAt.
	// found is false and err is nil when no record exists for detector yet.
	Last(ctx context.Context, detector string) (r SweepRunRecord, found bool, err error)
	// List returns up to limit records for detector, newest first. limit <= 0
	// means no limit.
	List(ctx context.Context, detector string, limit int) ([]SweepRunRecord, error)
}

// sqlSweepRunStore is the default SQL-backed [SweepRunStore].
type sqlSweepRunStore struct {
	db DB
}

// NewSweepRunStore returns a [SweepRunStore] backed by db.
//
// The smeldr_sweep_runs table must exist before use. Create it with
// [CreateSweepRunTable], or run the following DDL directly:
//
//	CREATE TABLE IF NOT EXISTS smeldr_sweep_runs (
//	    id       TEXT PRIMARY KEY,
//	    detector TEXT NOT NULL,
//	    ran_at   TIMESTAMPTZ NOT NULL,
//	    interval TEXT NOT NULL,
//	    walked   INTEGER NOT NULL,
//	    flagged  INTEGER NOT NULL,
//	    skipped  INTEGER NOT NULL,
//	    err      TEXT NOT NULL
//	);
func NewSweepRunStore(db DB) SweepRunStore {
	return &sqlSweepRunStore{db: db}
}

// CreateSweepRunTable creates the smeldr_sweep_runs table if it does not
// exist. Call once at application startup before [NewSweepRunStore].
func CreateSweepRunTable(db DB) error {
	_, err := db.ExecContext(context.Background(), `
		CREATE TABLE IF NOT EXISTS smeldr_sweep_runs (
			id       TEXT PRIMARY KEY,
			detector TEXT NOT NULL,
			ran_at   TIMESTAMPTZ NOT NULL,
			interval TEXT NOT NULL,
			walked   INTEGER NOT NULL,
			flagged  INTEGER NOT NULL,
			skipped  INTEGER NOT NULL,
			err      TEXT NOT NULL
		)`)
	return err
}

// Append persists r to the smeldr_sweep_runs table.
// RanAt is stored as an RFC3339 string for SQLite compatibility.
func (s *sqlSweepRunStore) Append(ctx context.Context, r SweepRunRecord) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO smeldr_sweep_runs
		 (id, detector, ran_at, interval, walked, flagged, skipped, err)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		r.ID, r.Detector, r.RanAt.UTC().Format(time.RFC3339), r.Interval,
		r.Walked, r.Flagged, r.Skipped, r.Err,
	)
	if err != nil {
		return fmt.Errorf("smeldr: SweepRunStore.Append: %w", err)
	}
	return nil
}

// Last returns the most recent record for detector, ordered by RanAt
// descending. found is false and err is nil when no record exists yet.
func (s *sqlSweepRunStore) Last(ctx context.Context, detector string) (SweepRunRecord, bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, detector, ran_at, interval, walked, flagged, skipped, err
		 FROM smeldr_sweep_runs WHERE detector = $1
		 ORDER BY ran_at DESC LIMIT 1`,
		detector,
	)
	if err != nil {
		return SweepRunRecord{}, false, fmt.Errorf("smeldr: SweepRunStore.Last: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return SweepRunRecord{}, false, rows.Err()
	}
	r, err := scanSweepRunRecord(rows)
	if err != nil {
		return SweepRunRecord{}, false, fmt.Errorf("smeldr: SweepRunStore.Last: %w", err)
	}
	return r, true, nil
}

// List returns up to limit records for detector, newest first. limit <= 0
// means no limit.
func (s *sqlSweepRunStore) List(ctx context.Context, detector string, limit int) ([]SweepRunRecord, error) {
	query := `SELECT id, detector, ran_at, interval, walked, flagged, skipped, err
	          FROM smeldr_sweep_runs WHERE detector = $1 ORDER BY ran_at DESC`
	args := []any{detector}
	if limit > 0 {
		query += " LIMIT $2"
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("smeldr: SweepRunStore.List: %w", err)
	}
	defer rows.Close()

	var out []SweepRunRecord
	for rows.Next() {
		r, err := scanSweepRunRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("smeldr: SweepRunStore.List: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// rowScanner is satisfied by both *sql.Rows and any equivalent test double,
// matching the single-row-at-a-time Scan contract Last and List both rely on.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanSweepRunRecord scans one smeldr_sweep_runs row in the column order
// shared by Last and List.
func scanSweepRunRecord(row rowScanner) (SweepRunRecord, error) {
	var r SweepRunRecord
	var ranAtStr string
	if err := row.Scan(&r.ID, &r.Detector, &ranAtStr, &r.Interval,
		&r.Walked, &r.Flagged, &r.Skipped, &r.Err); err != nil {
		return SweepRunRecord{}, err
	}
	r.RanAt, _ = time.Parse(time.RFC3339, ranAtStr)
	return r, nil
}
