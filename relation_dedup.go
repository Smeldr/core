package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// One live row per relation (T1019). The assert path looks a relation up
// before it inserts, so two first asserts at the same moment could both
// insert; the partial unique index below refuses the second.

// createLiveRelationIndexSQL is the unique index on a relation's live tuple:
// the dedup key of [writeEdgeRowAt] (A297), over rows not ended. A row with a
// future invalid_at is live to the app but outside the index; no path writes
// one on a first assert.
const createLiveRelationIndexSQL = `CREATE UNIQUE INDEX IF NOT EXISTS idx_relations_live_tuple
    ON smeldr_relations (source_type, source_id, target_type, target_id, relation_kind, edge_class)
    WHERE invalid_at IS NULL`

// liveDuplicatesSQL finds live tuples with more than one row.
const liveDuplicatesSQL = `SELECT source_type, source_id, target_type, target_id, relation_kind, edge_class
    FROM smeldr_relations WHERE invalid_at IS NULL
    GROUP BY source_type, source_id, target_type, target_id, relation_kind, edge_class
    HAVING COUNT(*) > 1`

// ensureLiveRelationIndex makes the live-tuple index when there are no live
// duplicates. With duplicates it logs which command makes it and returns nil:
// old data a migration can fix does not stop boot.
func ensureLiveRelationIndex(ctx context.Context, db DB) error {
	var one string
	err := db.QueryRowContext(ctx, liveDuplicatesSQL+" LIMIT 1").Scan(&one, &one, &one, &one, &one, &one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return fmt.Errorf("smeldr: live relation duplicates: %w", err)
	default:
		slog.WarnContext(ctx, "smeldr: smeldr_relations has live duplicate relations, so the live-tuple index is not made; run smeldr-server migrate-relation-dedup (App.MigrateRelationDuplicates)")
		return nil
	}
	if _, err := db.ExecContext(ctx, createLiveRelationIndexSQL); err != nil {
		return fmt.Errorf("smeldr: live relation index: %w", err)
	}
	return nil
}

// liveRelationExists is the answer to a first assert that another writer beat
// to the same relation: it already exists, as the live row ID. It is an
// [ErrConflict] for a caller asserting the relation, and nothing at all for an
// internal writer ([writeEdgeRow]).
type liveRelationExists struct {
	ID string
}

func (e *liveRelationExists) Error() string {
	return fmt.Sprintf("%s: relation already exists as %s (recorded at the same moment by another writer): read it, and re-assert it with its rev if you meant to change it", ErrConflict.Error(), e.ID)
}

func (e *liveRelationExists) Is(target error) bool { return target == ErrConflict }

// liveRelationLost finds the live row that won the race for edge's tuple.
func liveRelationLost(ctx context.Context, db DB, edge RelationEdge, now time.Time) error {
	var id string
	err := db.QueryRowContext(ctx,
		`SELECT id FROM smeldr_relations WHERE source_type=$1 AND source_id=$2 AND target_type=$3 AND target_id=$4
		   AND relation_kind=$5 AND edge_class=$6 AND (invalid_at IS NULL OR invalid_at > $7)`,
		edge.SourceType, edge.SourceID, edge.TargetType, edge.TargetID, edge.RelationKind, edge.EdgeClass, now).Scan(&id)
	if err != nil {
		return fmt.Errorf("%w: a relation insert wrote nothing and no live row explains it: %v", ErrInternal, err)
	}
	return &liveRelationExists{ID: id}
}

// RelationDedupOptions configure [App.MigrateRelationDuplicates].
type RelationDedupOptions struct {
	// DryRun reports what the migration would do and writes nothing.
	DryRun bool
}

// RelationDuplicate is one relation that had more than one live row.
type RelationDuplicate struct {
	// Kind, Source and Target name the relation: Source and Target are the
	// records' addresses where their type has one, else "type:id".
	Kind   string `json:"kind"`
	Source string `json:"source"`
	Target string `json:"target"`
	// EdgeClass is the rows' class (asserted, inferred, observed).
	EdgeClass string `json:"edge_class"`
	// Kept is the oldest row's id, the one that stays live.
	Kept string `json:"kept"`
	// Ended are the other rows' ids, ended as deduplicated.
	Ended []string `json:"ended"`
}

// RelationDedupReport is what [App.MigrateRelationDuplicates] did, or would do.
type RelationDedupReport struct {
	// DryRun is true when nothing was written.
	DryRun bool `json:"dry_run"`
	// Duplicates are the relations with more than one live row.
	Duplicates []RelationDuplicate `json:"duplicates"`
	// RowsEnded is how many rows were (or would be) ended.
	RowsEnded int `json:"rows_ended"`
	// IndexMade is true when the live-tuple index exists afterwards.
	IndexMade bool `json:"index_made"`
}

// relationDedupJob is the job actor of the ends the migration records.
const relationDedupJob = "relation-dedup"

// MigrateRelationDuplicates ends every second live row of a relation (T1019):
// for each relation with more than one live row it keeps the oldest
// (created_at, then id) and ends the others with the cause
// [EdgeEndDeduplicated], recorded in provenance with the kept id in the
// reason. Nothing is merged into the kept row: the ended rows stay in history
// as they were. Then it makes the live-tuple index, so it cannot happen again.
//
//	report, err := app.MigrateRelationDuplicates(ctx, smeldr.RelationDedupOptions{DryRun: true})
//
// Idempotent: a second run finds nothing. It needs relations ([App.Relations]).
func (a *App) MigrateRelationDuplicates(ctx context.Context, opts RelationDedupOptions) (*RelationDedupReport, error) {
	s := a.relationStore
	if s == nil {
		return nil, fmt.Errorf("%w: MigrateRelationDuplicates needs relations (App.Relations)", ErrBadRequest)
	}
	rep := &RelationDedupReport{DryRun: opts.DryRun, Duplicates: []RelationDuplicate{}}
	rows, err := s.db.QueryContext(ctx, liveDuplicatesSQL)
	if err != nil {
		return nil, fmt.Errorf("%w: find live duplicates: %s", ErrInternal, err)
	}
	type tuple struct{ sourceType, sourceID, targetType, targetID, kind, class string }
	var tuples []tuple
	for rows.Next() {
		var t tuple
		if err := rows.Scan(&t.sourceType, &t.sourceID, &t.targetType, &t.targetID, &t.kind, &t.class); err != nil {
			rows.Close()
			return nil, fmt.Errorf("%w: find live duplicates: %s", ErrInternal, err)
		}
		tuples = append(tuples, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: find live duplicates: %s", ErrInternal, err)
	}
	sys := TrustedRead(ctx)
	for _, t := range tuples {
		ids, err := liveRowsOf(ctx, s.db, t.sourceType, t.sourceID, t.targetType, t.targetID, t.kind, t.class)
		if err != nil {
			return nil, err
		}
		if len(ids) < 2 {
			continue // ended meanwhile
		}
		d := RelationDuplicate{Kind: t.kind, EdgeClass: t.class, Kept: ids[0], Ended: ids[1:],
			Source: a.recordLabel(sys, t.sourceType, t.sourceID), Target: a.recordLabel(sys, t.targetType, t.targetID)}
		rep.Duplicates = append(rep.Duplicates, d)
		rep.RowsEnded += len(d.Ended)
		if opts.DryRun {
			continue
		}
		ending := edgeEnding{cause: EdgeEndDeduplicated, reason: "a second live row of the relation; kept " + d.Kept,
			actorKind: "job", actorID: relationDedupJob}
		for _, id := range d.Ended {
			if _, err := s.endEdge(ctx, id, ending); err != nil {
				return nil, fmt.Errorf("%w: end duplicate %s: %s", ErrInternal, id, err)
			}
		}
	}
	if opts.DryRun {
		return rep, nil
	}
	if err := ensureLiveRelationIndex(ctx, s.db); err != nil {
		return nil, err
	}
	var name string
	rep.IndexMade = s.db.QueryRowContext(ctx, liveDuplicatesSQL+" LIMIT 1").Scan(&name, &name, &name, &name, &name, &name) == sql.ErrNoRows
	return rep, nil
}

// liveRowsOf returns the ids of a relation's live rows, oldest first.
func liveRowsOf(ctx context.Context, db DB, sourceType, sourceID, targetType, targetID, kind, class string) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id FROM smeldr_relations WHERE source_type=$1 AND source_id=$2 AND target_type=$3 AND target_id=$4
		   AND relation_kind=$5 AND edge_class=$6 AND invalid_at IS NULL ORDER BY created_at, id`,
		sourceType, sourceID, targetType, targetID, kind, class)
	if err != nil {
		return nil, fmt.Errorf("%w: read a relation's rows: %s", ErrInternal, err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("%w: read a relation's rows: %s", ErrInternal, err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// recordLabel names a record for a report: its address when it has one, else
// "type:id".
func (a *App) recordLabel(ctx context.Context, typeName, id string) string {
	if addr := a.transitionAddress(ctx, statusTransition{typeName: typeName, id: id}); addr != "" {
		return addr
	}
	return typeName + ":" + id
}
