package smeldr

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// AuditRecord is one immutable audit log entry written after every lifecycle
// transition subscribed to by [App.Audit].
type AuditRecord struct {
	ID            string    `json:"id"`             // UUID v7 primary key
	Timestamp     time.Time `json:"timestamp"`      // wall-clock time the lifecycle signal was dispatched (UTC)
	Signal        Signal    `json:"signal"`         // lifecycle signal that triggered this record (e.g. AfterPublish)
	ContentType   string    `json:"content_type"`   // unqualified Go type name of the content item (e.g. "Post")
	Slug          string    `json:"slug"`           // URL slug of the content item at the time of the event
	ActorID       string    `json:"actor_id"`       // stable UUID of the authenticated user; empty for unauthenticated actions
	ActorRole     string    `json:"actor_role"`     // role string of the actor ("guest", "author", "editor", "admin")
	PreviousState string    `json:"previous_state"` // lifecycle state before the transition; empty for AfterCreate
}

// AuditFilter narrows an [AuditStore.List] query.
// Zero values are treated as "no filter" for that dimension.
type AuditFilter struct {
	From        time.Time // zero = no lower bound
	To          time.Time // zero = no upper bound
	ContentType string    // empty = all types
	ActorID     string    // empty = all actors
}

// AuditStore is the persistence interface for the built-in audit trail.
// Implement it to use a custom storage backend; use [NewAuditStore] for the
// default SQLite/Postgres-compatible implementation.
type AuditStore interface {
	Append(ctx context.Context, r AuditRecord) error
	List(ctx context.Context, f AuditFilter) ([]AuditRecord, error)
}

// sqlAuditStore is the default SQL-backed [AuditStore].
type sqlAuditStore struct {
	db DB
}

// NewAuditStore returns an [AuditStore] backed by db.
//
// The smeldr_audit_log table must exist before [App.Audit] is called.
// Create it with [CreateAuditTable], or run the following DDL directly:
//
//	CREATE TABLE IF NOT EXISTS smeldr_audit_log (
//	    id           TEXT PRIMARY KEY,
//	    timestamp    TIMESTAMPTZ NOT NULL,
//	    signal       TEXT NOT NULL,
//	    content_type TEXT NOT NULL,
//	    slug         TEXT NOT NULL,
//	    actor_id     TEXT NOT NULL,
//	    actor_role   TEXT NOT NULL,
//	    prev_state   TEXT NOT NULL
//	);
func NewAuditStore(db DB) AuditStore {
	return &sqlAuditStore{db: db}
}

// CreateAuditTable creates the smeldr_audit_log table if it does not exist.
// Call once at application startup before [NewAuditStore].
func CreateAuditTable(db DB) error {
	_, err := db.ExecContext(context.Background(), `
		CREATE TABLE IF NOT EXISTS smeldr_audit_log (
			id           TEXT PRIMARY KEY,
			timestamp    TIMESTAMPTZ NOT NULL,
			signal       TEXT NOT NULL,
			content_type TEXT NOT NULL,
			slug         TEXT NOT NULL,
			actor_id     TEXT NOT NULL,
			actor_role   TEXT NOT NULL,
			prev_state   TEXT NOT NULL
		)`)
	return err
}

// Append persists r to the smeldr_audit_log table.
// Timestamp is stored as an RFC3339 string for SQLite compatibility.
func (s *sqlAuditStore) Append(ctx context.Context, r AuditRecord) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO smeldr_audit_log
		 (id, timestamp, signal, content_type, slug, actor_id, actor_role, prev_state)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		r.ID, r.Timestamp.UTC().Format(time.RFC3339), string(r.Signal), r.ContentType, r.Slug,
		r.ActorID, r.ActorRole, r.PreviousState,
	)
	return err
}

// List returns audit records matching f, ordered by timestamp descending.
func (s *sqlAuditStore) List(ctx context.Context, f AuditFilter) ([]AuditRecord, error) {
	query := `SELECT id, timestamp, signal, content_type, slug, actor_id, actor_role, prev_state
	          FROM smeldr_audit_log WHERE 1=1`
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
	if f.ContentType != "" {
		query += fmt.Sprintf(" AND content_type = $%d", n)
		args = append(args, f.ContentType)
		n++
	}
	if f.ActorID != "" {
		query += fmt.Sprintf(" AND actor_id = $%d", n)
		args = append(args, f.ActorID)
		n++
	}
	_ = n
	query += " ORDER BY timestamp DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditRecord
	for rows.Next() {
		var r AuditRecord
		var sig, tsStr string
		if err := rows.Scan(&r.ID, &tsStr, &sig, &r.ContentType,
			&r.Slug, &r.ActorID, &r.ActorRole, &r.PreviousState); err != nil {
			return nil, err
		}
		r.Signal = Signal(sig)
		r.Timestamp, _ = time.Parse(time.RFC3339, tsStr)
		out = append(out, r)
	}
	return out, rows.Err()
}

// newAuditHandler returns the http.Handler mounted at GET /_audit by [App.Audit].
// Requires Editor or higher role. Accepts optional query params:
// from, to (RFC3339), type, actor.
func newAuditHandler(auth AuthFunc, store AuditStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.authenticate(r)
		if !ok {
			WriteError(w, r, ErrUnauth)
			return
		}
		if !user.HasRole(Editor) {
			WriteError(w, r, ErrForbidden)
			return
		}

		var f AuditFilter
		q := r.URL.Query()
		if s := q.Get("from"); s != "" {
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				WriteError(w, r, ErrBadRequest)
				return
			}
			f.From = t
		}
		if s := q.Get("to"); s != "" {
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				WriteError(w, r, ErrBadRequest)
				return
			}
			f.To = t
		}
		f.ContentType = q.Get("type")
		f.ActorID = q.Get("actor")

		records, err := store.List(r.Context(), f)
		if err != nil {
			slog.ErrorContext(r.Context(), "smeldr: audit list failed", "error", err)
			WriteError(w, r, ErrInternal)
			return
		}
		if records == nil {
			records = []AuditRecord{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(records)
	})
}
