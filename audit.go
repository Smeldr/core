package smeldr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// AuditRecord is one immutable audit log entry written after every lifecycle
// transition subscribed to by [App.Audit].
type AuditRecord struct {
	ID            string         `json:"id"`             // UUID v7 primary key
	Timestamp     time.Time      `json:"timestamp"`      // wall-clock time the lifecycle signal was dispatched (UTC)
	Signal        LifecycleEvent `json:"signal"`         // lifecycle signal that triggered this record (e.g. AfterPublish)
	ContentType   string         `json:"content_type"`   // unqualified Go type name of the content item (e.g. "Post")
	Slug          string         `json:"slug"`           // URL slug of the content item at the time of the event
	ActorID       string         `json:"actor_id"`       // stable UUID of the authenticated user; empty for unauthenticated actions
	ActorRole     string         `json:"actor_role"`     // role string of the actor ("guest", "author", "editor", "admin")
	PreviousState string         `json:"previous_state"` // lifecycle state before the transition; empty for AfterCreate
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
		r.Signal = LifecycleEvent(sig)
		r.Timestamp, _ = time.Parse(time.RFC3339, tsStr)
		out = append(out, r)
	}
	return out, rows.Err()
}

// auditFilter narrows the records GET /_audit returns to what the caller may
// read. Nil returns them all.
type auditFilter func(ctx context.Context, user User, records []AuditRecord) ([]AuditRecord, error)

// filterAuditRecords is the per-item read rule of GET /_audit under
// governance: an entry is shown only when a record of its type that the caller
// may read still holds the entry's slug. An entry names its subject by type and
// slug at the time of the event, not by ID, so an entry whose subject was
// deleted or has a new slug is not shown to a scoped caller (fail-closed). The
// public clause of the item surfaces does not apply: an entry carries the
// actor, which a public page does not show. Without governance, or for a
// caller that reads everything, the records are returned unchanged.
func (a *App) filterAuditRecords(ctx context.Context, user User, records []AuditRecord) ([]AuditRecord, error) {
	if a.governance == nil {
		return records, nil
	}
	scope, err := LoadReadScope(ctx, a.governance, user)
	if err != nil {
		return nil, err
	}
	if scope.All() {
		return records, nil
	}
	seen := map[[2]string]bool{}
	kept := records[:0]
	for _, r := range records {
		key := [2]string{r.ContentType, r.Slug}
		readable, ok := seen[key]
		if !ok {
			if readable, err = a.auditSubjectReadable(ctx, scope, r.ContentType, r.Slug); err != nil {
				return nil, err
			}
			seen[key] = readable
		}
		if readable {
			kept = append(kept, r)
		}
	}
	return kept, nil
}

// auditSubjectReadable reports whether scope reads the record of typeName that
// slug names now, through the same resolvers (and the same miss-continuation)
// as the item surfaces. An unregistered type, or no readable record, is false;
// a lookup failure is an error, never a hidden entry.
func (a *App) auditSubjectReadable(ctx context.Context, scope *ReadScope, typeName, slug string) (bool, error) {
	desc := a.typeRegistry.Lookup(typeName)
	if desc == nil {
		return false, nil
	}
	visible := func(id string) bool { return scope.Readable(typeName, id) }
	var err error
	if desc.Kind == "content" {
		repo, rerr := a.DynamicContentRepo(typeName)
		if rerr != nil {
			return false, fmt.Errorf("%w: audit: %s", ErrInternal, rerr)
		}
		_, err = repo.resolveFor(ctx, slug, func(n *DynamicNode) bool { return visible(n.ID) })
	} else {
		_, _, _, _, err = a.lookupCompiledRecord(ctx, typeName, slug, visible)
	}
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrNotFound):
		return false, nil
	}
	return false, fmt.Errorf("%w: audit: %s", ErrInternal, err)
}

// newAuditHandler returns the http.Handler mounted at GET /_audit by [App.Audit].
// Requires Editor or higher role. Accepts optional query params:
// from, to (RFC3339), type, actor. filter, when not nil, narrows the records
// to what the caller may read.
func newAuditHandler(auth AuthFunc, store AuditStore, filter auditFilter) http.Handler {
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
		if filter != nil {
			if records, err = filter(r.Context(), user, records); err != nil {
				WriteError(w, r, err)
				return
			}
		}
		if len(records) == 0 {
			records = []AuditRecord{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(records)
	})
}
