// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// ProvenanceRecord is one immutable record of who did what to a governed subject,
// how, and optionally why. Written by [App.Provenance] for every lifecycle
// transition on a Node-subject, and by the relation graph for every asserted edge.
//
// Unlike [AuditRecord] (kept, unchanged, for backward compatibility — see
// [App.Audit]), ProvenanceRecord is keyed on SubjectType+SubjectID rather than
// ContentType+Slug: every Node has an ID, and so does every [RelationEdge], but
// RelationEdge has no Slug. ProvenanceRecord is additive — it does not replace
// AuditRecord's table, wiring, or behaviour.
type ProvenanceRecord struct {
	ID          string    `json:"id"`           // UUID v7 primary key
	Timestamp   time.Time `json:"timestamp"`    // wall-clock time the event was recorded (UTC)
	SubjectType string    `json:"subject_type"` // Go type name, e.g. "Decision", "RelationEdge"
	SubjectID   string    `json:"subject_id"`   // Node.ID or RelationEdge.ID
	Verb        string    `json:"verb"`         // "create" | "update" | "transition" | "assert" | "invalidate"
	FromState   string    `json:"from_state"`   // empty for create/assert
	ToState     string    `json:"to_state"`     // empty for invalidate
	ActorKind   string    `json:"actor_kind"`   // "human" | "job" | "agent"; empty only if truly unattributable
	ActorID     string    `json:"actor_id"`     // user UUID, job identifier, or agent identifier
	Surface     string    `json:"surface"`      // "http" | "mcp" | "cli" | "trigger"; empty when not derivable
	Reason      string    `json:"reason"`       // optional free text, empty unless supplied
}

// ProvenanceFilter narrows a [ProvenanceStore.List] query.
// Zero values are treated as "no filter" for that dimension.
type ProvenanceFilter struct {
	From        time.Time // zero = no lower bound
	To          time.Time // zero = no upper bound
	SubjectType string    // empty = all types
	SubjectID   string    // empty = all subjects
	ActorID     string    // empty = all actors
}

// ProvenanceStore is the persistence interface for [ProvenanceRecord]s.
// Implement it for a custom storage backend; use [NewProvenanceStore] for the
// default SQLite/Postgres-compatible implementation.
type ProvenanceStore interface {
	Append(ctx context.Context, r ProvenanceRecord) error
	List(ctx context.Context, f ProvenanceFilter) ([]ProvenanceRecord, error)
}

// sqlProvenanceStore is the default SQL-backed [ProvenanceStore].
type sqlProvenanceStore struct {
	db DB
}

// NewProvenanceStore returns a [ProvenanceStore] backed by db.
//
// The smeldr_provenance table must exist before [App.Provenance] is called.
// Create it with [CreateProvenanceTable], or run the following DDL directly:
//
//	CREATE TABLE IF NOT EXISTS smeldr_provenance (
//	    id           TEXT PRIMARY KEY,
//	    timestamp    TIMESTAMPTZ NOT NULL,
//	    subject_type TEXT NOT NULL,
//	    subject_id   TEXT NOT NULL,
//	    verb         TEXT NOT NULL,
//	    from_state   TEXT NOT NULL,
//	    to_state     TEXT NOT NULL,
//	    actor_kind   TEXT NOT NULL,
//	    actor_id     TEXT NOT NULL,
//	    surface      TEXT NOT NULL,
//	    reason       TEXT NOT NULL
//	);
func NewProvenanceStore(db DB) ProvenanceStore {
	return &sqlProvenanceStore{db: db}
}

// CreateProvenanceTable creates the smeldr_provenance table if it does not exist.
// Call once at application startup before [NewProvenanceStore].
func CreateProvenanceTable(db DB) error {
	_, err := db.ExecContext(context.Background(), `
		CREATE TABLE IF NOT EXISTS smeldr_provenance (
			id           TEXT PRIMARY KEY,
			timestamp    TIMESTAMPTZ NOT NULL,
			subject_type TEXT NOT NULL,
			subject_id   TEXT NOT NULL,
			verb         TEXT NOT NULL,
			from_state   TEXT NOT NULL,
			to_state     TEXT NOT NULL,
			actor_kind   TEXT NOT NULL,
			actor_id     TEXT NOT NULL,
			surface      TEXT NOT NULL,
			reason       TEXT NOT NULL
		)`)
	return err
}

// Append persists r to the smeldr_provenance table.
// Timestamp is stored as an RFC3339 string for SQLite compatibility.
func (s *sqlProvenanceStore) Append(ctx context.Context, r ProvenanceRecord) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO smeldr_provenance
		 (id, timestamp, subject_type, subject_id, verb, from_state, to_state, actor_kind, actor_id, surface, reason)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		r.ID, r.Timestamp.UTC().Format(time.RFC3339), r.SubjectType, r.SubjectID,
		r.Verb, r.FromState, r.ToState, r.ActorKind, r.ActorID, r.Surface, r.Reason,
	)
	return err
}

// List returns provenance records matching f, ordered by timestamp descending.
func (s *sqlProvenanceStore) List(ctx context.Context, f ProvenanceFilter) ([]ProvenanceRecord, error) {
	query := `SELECT id, timestamp, subject_type, subject_id, verb, from_state, to_state, actor_kind, actor_id, surface, reason
	          FROM smeldr_provenance WHERE 1=1`
	args := []any{}
	n := 1
	if !f.From.IsZero() {
		query += fmt.Sprintf(" AND timestamp >= $%d", n)
		args = append(args, f.From.UTC().Format(time.RFC3339))
		n++
	}
	if !f.To.IsZero() {
		query += fmt.Sprintf(" AND timestamp <= $%d", n)
		args = append(args, f.To.UTC().Format(time.RFC3339))
		n++
	}
	if f.SubjectType != "" {
		query += fmt.Sprintf(" AND subject_type = $%d", n)
		args = append(args, f.SubjectType)
		n++
	}
	if f.SubjectID != "" {
		query += fmt.Sprintf(" AND subject_id = $%d", n)
		args = append(args, f.SubjectID)
		n++
	}
	if f.ActorID != "" {
		query += fmt.Sprintf(" AND actor_id = $%d", n)
		args = append(args, f.ActorID)
		n++
	}
	query += " ORDER BY timestamp DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProvenanceRecord
	for rows.Next() {
		var r ProvenanceRecord
		var tsStr string
		if err := rows.Scan(&r.ID, &tsStr, &r.SubjectType, &r.SubjectID,
			&r.Verb, &r.FromState, &r.ToState, &r.ActorKind, &r.ActorID, &r.Surface, &r.Reason); err != nil {
			return nil, err
		}
		r.Timestamp, _ = time.Parse(time.RFC3339, tsStr)
		out = append(out, r)
	}
	return out, rows.Err()
}

// recordProvenance persists rec via store, logging and swallowing any error.
// Fail-open: a provenance-recording failure must never fail the write it is
// recording (design doc §7) — the same fail-open discipline validateTransition's
// structural-error zone and applyConflictPolicy already apply in this codebase.
func recordProvenance(ctx context.Context, store ProvenanceStore, rec ProvenanceRecord) {
	if store == nil {
		return
	}
	if rec.ID == "" {
		rec.ID = NewID()
	}
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now().UTC()
	}
	if err := store.Append(ctx, rec); err != nil {
		slog.WarnContext(ctx, "smeldr: recordProvenance: append failed",
			"subject_type", rec.SubjectType, "subject_id", rec.SubjectID, "verb", rec.Verb, "error", err)
	}
}

// provenanceLifecycleEvents is every [LifecycleEvent] that represents a completed
// state change to a Node-subject — the "After*" events, excluding Before* (not yet
// committed), SitemapRegenerate (unrelated), and AfterRelationCascade (a
// notification to a different, downstream item — that item's own transition, if
// any, already gets its own After* event on its own subject).
var provenanceLifecycleEvents = []LifecycleEvent{
	AfterCreate, AfterUpdate, AfterPublish, AfterUnpublish, AfterSchedule, AfterArchive, AfterDelete,
}

// provenanceVerbFor maps a LifecycleEvent + from/to state comparison to a
// ProvenanceRecord.Verb. AfterUpdate fires both for genuine custom-flow
// transitions and for plain content edits that do not change status (module.go's
// updateHandler/MCPUpdate call it unconditionally on every save) — distinguished
// here by comparing FromState/ToState, since the design doc's original verb set
// (create/transition/assert/invalidate) did not anticipate the no-op-status case.
func provenanceVerbFor(sig LifecycleEvent, fromState, toState string) string {
	switch sig {
	case AfterCreate:
		return "create"
	case AfterDelete:
		return "invalidate"
	default:
		if fromState == toState {
			return "update"
		}
		return "transition"
	}
}

// currentStatusOf safely extracts the current lifecycle status from item via
// [nodeStatusOf]. item is [SignalEvent]'s unexported raw field — always
// populated when built by [buildSignalEvent] (the real dispatch path), but
// test code constructing a [SignalEvent] literal directly may leave it nil.
// Returns "" rather than panicking in that case — matches recordProvenance's
// own fail-open discipline (§7): a provenance concern must never crash the
// signal dispatch it is observing.
func currentStatusOf(item any) (status string) {
	if item == nil {
		return ""
	}
	defer func() {
		if recover() != nil {
			status = ""
		}
	}()
	return string(nodeStatusOf(item))
}

// Provenance wires store to record a [ProvenanceRecord] for every completed
// lifecycle transition on every Node-subject (see [provenanceLifecycleEvents]).
// Additive and independent of [App.Audit] — both may be wired at once; the four
// events they both cover (AfterPublish/AfterSchedule/AfterArchive/AfterDelete)
// produce one record in each store, not a shared or migrated one. This is
// deliberate: AuditRecord and ProvenanceRecord are two views written from the
// same call site with the same data, never able to disagree, not two competing
// sources of truth.
//
// Errors from [ProvenanceStore.Append] are logged at Warn level and never
// propagated — a provenance-recording bug must never fail the content write it
// is recording.
//
// The smeldr_provenance table must exist before Provenance is called. See
// [NewProvenanceStore] for the required DDL.
func (a *App) Provenance(store ProvenanceStore) *App {
	a.provenanceStore = store
	for _, sig := range provenanceLifecycleEvents {
		s := sig
		a.OnSignal(s, func(ctx context.Context, ev SignalEvent) error {
			toState := currentStatusOf(ev.raw)
			recordProvenance(ctx, a.provenanceStore, ProvenanceRecord{
				SubjectType: ev.Type,
				SubjectID:   ev.NodeID,
				Verb:        provenanceVerbFor(s, ev.PreviousState, toState),
				FromState:   ev.PreviousState,
				ToState:     toState,
				ActorKind:   actorKindFor(ev.ActorID),
				ActorID:     ev.ActorID,
			})
			return nil
		})
	}
	return a
}

// actorKindFor returns "human" when actorID is non-empty (an authenticated user
// or MCP-client identity — smeldr.Context.User() does not currently distinguish
// a human operator from an AI agent driving the same client) and empty otherwise,
// matching ProvenanceRecord's own "empty only if truly unattributable" principle.
// "agent" is reserved for a future, more specific agent-identity concept that
// does not exist in the codebase yet (design doc §9 open question 3, confirmed).
func actorKindFor(actorID string) string {
	if actorID == "" {
		return ""
	}
	return "human"
}
