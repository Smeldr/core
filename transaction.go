// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"fmt"
	"time"
)

// runInTx runs fn inside one transaction on db and commits it. On any error it
// rolls back and returns that error; fn's own error comes back unchanged. It is
// the one place a create opens its transaction (D110: a create and everything it
// writes commit together or not at all), so the caller announces nothing (events,
// standing, provenance) until runInTx has returned nil.
//
// A db without BeginTx ([txBeginner]): nil, a test double, or a *sql.Tx the
// caller already holds, runs fn on db itself: the same statements in the same
// order, not atomic, which is the fallback every transactional write in core has
// ([conflictTx]). Inside fn every statement must go through exec, never through
// db or a handle cached elsewhere: on SQLite with one connection a read on db
// while the transaction holds it waits forever.
func runInTx(ctx context.Context, db DB, fn func(exec DB) error) error {
	exec, commit, rollback, err := conflictTx(ctx, db)
	if err != nil {
		return err
	}
	defer rollback()
	if err := fn(exec); err != nil {
		return err
	}
	if err := commit(); err != nil {
		return fmt.Errorf("%w: commit transaction: %s", ErrInternal, err)
	}
	return nil
}

// txRebinder is implemented by a repository that can run the same queries on
// another handle, such as a transaction. [SQLRepo] implements it.
type txRebinder[T any] interface {
	onDB(db DB) Repository[T]
}

// onDB returns a copy of r that runs every query on db. The table and type are
// shared; r itself is unchanged.
func (r *SQLRepo[T]) onDB(db DB) Repository[T] {
	cp := *r
	cp.db = db
	return &cp
}

// repoOn returns the module's repository bound to exec when it can be
// ([txRebinder]), otherwise the module's own repository. A repository that
// cannot be rebound (MemoryRepo, a user's own) writes last in the create, so a
// failure before it still leaves no row.
func (m *Module[T]) repoOn(exec DB) Repository[T] {
	if rb, ok := m.repo.(txRebinder[T]); ok && exec != nil {
		return rb.onDB(exec)
	}
	return m.repo
}

// createInTx saves a new item and the type's own pre-save check in one
// transaction: the internal check ([saveHooksOption] before, run on the
// transaction) and the INSERT commit together or not at all. The public
// [BeforeCreate] hooks have already run; everything announced about the item
// (events, standing, provenance, the after-save step) is the caller's, after
// createInTx returns nil.
func (m *Module[T]) createInTx(ctx Context, item T) error {
	return runInTx(ctx, m.db, func(exec DB) error {
		if m.saveBefore != nil {
			if err := m.saveBefore(ctx, exec, nil, item); err != nil {
				return err
			}
		}
		return m.repoOn(exec).Save(ctx, item)
	})
}

// InsertSignal inserts s into smeldr_signals in its own transaction. It fills
// an empty ID (a new ID), Slug (the ID), Status ("pending") and CreatedAt and
// UpdatedAt (now, UTC), and writes them back onto s.
//
// It announces nothing: the caller dispatches the "signal.created" event itself
// after InsertSignal returns nil, so no event is ever sent for a row that was not
// committed. [App.NotifySignalCreated] is that dispatch for a caller outside core.
func InsertSignal(ctx context.Context, db DB, s *Signal) error {
	if s.ID == "" {
		s.ID = NewID()
	}
	if s.Slug == "" {
		s.Slug = s.ID
	}
	if s.Status == "" {
		s.Status = "pending"
	}
	now := time.Now().UTC()
	if s.CreatedAt.IsZero() {
		s.CreatedAt = now
	}
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = now
	}
	return runInTx(ctx, db, func(exec DB) error {
		_, err := exec.ExecContext(ctx,
			`INSERT INTO smeldr_signals
				(id, slug, status, created_at, updated_at, sender, receiver, signal_type, message, task_ref, sequence,
				 subject_type, subject_id, from_state, to_state, required_role)
			VALUES
				($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
			s.ID, s.Slug, string(s.Status), s.CreatedAt, s.UpdatedAt, s.Sender, s.Receiver, s.SignalType, s.Message,
			s.TaskRef, s.Sequence, s.SubjectType, s.SubjectID, s.FromState, s.ToState, s.RequiredOperation,
		)
		if err != nil {
			return fmt.Errorf("smeldr: InsertSignal: %w", err)
		}
		return nil
	})
}
