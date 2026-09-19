// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Finding is a thin, detector-owned record of a structural or
// governance condition (D51). Written only by detectors — never by
// create_*/update_* MCP tools or a human-driven state flow: a write
// surface would invite exactly the acknowledged/dismissed/accepted-as-is
// resolution claims D46 forbids. A finding resolves when its own
// detector stops firing for the same subject, not by human action —
// the same "authoritative state lives outside Node.Status" shape [Run]
// (D38) already established.
//
// A Finding's identity is (Detector, SubjectType, SubjectID) — dedup
// across repeated sweep runs is load-bearing (D51's own "why 2" table):
// a detector that fires again for a subject it already flagged updates
// LastSeenAt on the existing row rather than creating a duplicate.
type Finding struct {
	// ID is the finding's own unique identifier.
	ID string `json:"id" db:"id"`
	// Detector names the detector that produced this finding (e.g. "structural").
	Detector string `json:"detector" db:"detector"`
	// SubjectType is the qualified subject's type (e.g. "RelationEdge"),
	// matching D51's own condition-identity convention — the same
	// pattern smeldr_relations' source_type/source_id and
	// recordAssertProvenance's SubjectType already use.
	SubjectType string `json:"subject_type" db:"subject_type"`
	// SubjectID is the qualified subject's own ID.
	SubjectID string `json:"subject_id" db:"subject_id"`
	// Provenance names which of D51's three conditions this finding is:
	// "detected", "asserted", or "scheduled".
	Provenance string `json:"provenance" db:"provenance"`
	// Message is a human-readable description, set by the detector at
	// Record time (e.g. "relation target no longer alive").
	Message string `json:"message" db:"message"`
	// FirstSeenAt is when this subject was first flagged by this
	// detector. Unchanged by later Record calls for the same subject.
	FirstSeenAt time.Time `json:"first_seen_at" db:"first_seen_at"`
	// LastSeenAt is when this subject was most recently flagged by this
	// detector. Advances on every Record call for the same subject —
	// this is the "was this present last week" history D51 names.
	LastSeenAt time.Time `json:"last_seen_at" db:"last_seen_at"`
}

// FindingStore persists [Finding] records. Obtain one with [NewFindingStore].
type FindingStore interface {
	// Record upserts f, deduplicated on (Detector, SubjectType, SubjectID).
	// A repeat finding for a subject already on file updates LastSeenAt
	// and Message only; a new subject inserts with FirstSeenAt ==
	// LastSeenAt. f.ID and f.FirstSeenAt/LastSeenAt are set by Record —
	// the caller supplies Detector, SubjectType, SubjectID, Provenance,
	// and Message only.
	Record(ctx context.Context, f Finding) error
	// List returns findings for detector, newest-first by LastSeenAt.
	// An empty detector returns findings from every detector.
	List(ctx context.Context, detector string) ([]Finding, error)
}

// CreateFindingTable creates smeldr_findings if it does not already
// exist. Call once at application startup, before [NewFindingStore] is
// used.
func CreateFindingTable(db DB) error {
	_, err := db.ExecContext(context.Background(), `
		CREATE TABLE IF NOT EXISTS smeldr_findings (
			id            TEXT NOT NULL PRIMARY KEY,
			detector      TEXT NOT NULL,
			subject_type  TEXT NOT NULL,
			subject_id    TEXT NOT NULL,
			provenance    TEXT NOT NULL,
			message       TEXT NOT NULL DEFAULT '',
			first_seen_at TIMESTAMPTZ NOT NULL,
			last_seen_at  TIMESTAMPTZ NOT NULL,
			UNIQUE(detector, subject_type, subject_id)
		)`)
	return err
}

type findingStore struct {
	db DB
}

// NewFindingStore returns a [FindingStore] backed by db. [CreateFindingTable]
// must be called first.
func NewFindingStore(db DB) FindingStore {
	return &findingStore{db: db}
}

func (s *findingStore) Record(ctx context.Context, f Finding) error {
	var existingID string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM smeldr_findings WHERE detector = $1 AND subject_type = $2 AND subject_id = $3`,
		f.Detector, f.SubjectType, f.SubjectID,
	).Scan(&existingID)
	now := time.Now().UTC()
	switch {
	case err == nil:
		_, err = s.db.ExecContext(ctx,
			`UPDATE smeldr_findings SET message = $1, last_seen_at = $2 WHERE id = $3`,
			f.Message, now, existingID,
		)
		return err
	case errors.Is(err, sql.ErrNoRows):
		id := f.ID
		if id == "" {
			id = NewID()
		}
		_, err = s.db.ExecContext(ctx,
			`INSERT INTO smeldr_findings
				(id, detector, subject_type, subject_id, provenance, message, first_seen_at, last_seen_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			id, f.Detector, f.SubjectType, f.SubjectID, f.Provenance, f.Message, now, now,
		)
		return err
	default:
		return err
	}
}

// Findings wires store as the [FindingStore] detectors record into.
// [App.SweepStructural] records a Finding for each newly-flagged stale
// edge when a store is configured; no store means SweepStructural's own
// behaviour is unchanged from before this feature existed.
func (a *App) Findings(store FindingStore) *App {
	a.findingStore = store
	return a
}

// FindingStore returns the store configured via [App.Findings], or nil
// if none was configured.
func (a *App) FindingStore() FindingStore {
	return a.findingStore
}

func (s *findingStore) List(ctx context.Context, detector string) ([]Finding, error) {
	query := `SELECT id, detector, subject_type, subject_id, provenance, message, first_seen_at, last_seen_at
		FROM smeldr_findings`
	var args []any
	if detector != "" {
		query += ` WHERE detector = $1`
		args = append(args, detector)
	}
	query += ` ORDER BY last_seen_at DESC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Finding
	for rows.Next() {
		var f Finding
		if err := rows.Scan(
			&f.ID, &f.Detector, &f.SubjectType, &f.SubjectID, &f.Provenance,
			&f.Message, scanDest(&f.FirstSeenAt), scanDest(&f.LastSeenAt),
		); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
