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

// SQLRepo is rebound into the create transaction (A485); without this method
// creates would quietly stop being atomic, so a rename breaks the build.
var _ txRebinder[*Node] = (*SQLRepo[*Node])(nil)

// onDB returns a copy of r that runs every query on db. The table and type are
// shared; r itself is unchanged.
//
//lint:ignore U1000 called through the txRebinder interface (repoOn); staticcheck does not follow generic interface assertions
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
		// D110: the address is taken on this transaction, so a create that
		// rolls back takes no number.
		if err := m.assignNewAddress(ctx, exec, item); err != nil {
			return err
		}
		if m.saveBefore != nil {
			if err := m.saveBefore(ctx, exec, nil, item); err != nil {
				return err
			}
		}
		return m.repoOn(exec).Save(ctx, item)
	})
}

// InsertSignal inserts s into smeldr_signals in its own transaction. It fills
// an empty ID (a new ID), Slug (the ID), Status and CreatedAt and UpdatedAt
// (now, UTC), and writes them back onto s. Status is always the type's
// initial state (D115): open for a catalog type ([SignalTypes]), pending for a
// history type.
//
// s is checked first with [CheckSignal] and no cutovers (warnings only): a
// Signal it refuses is not written. Use [InsertSignalWithWarnings] for the
// warnings and the instance's cutovers.
//
// It announces nothing: the caller dispatches the "signal.created" event itself
// after InsertSignal returns nil, so no event is ever sent for a row that was not
// committed. [App.NotifySignalCreated] is that dispatch for a caller outside core.
func InsertSignal(ctx context.Context, db DB, s *Signal) error {
	_, err := InsertSignalWithWarnings(ctx, db, s, SignalCutovers{})
	return err
}

// InsertSignalWithWarnings is [InsertSignal] with the instance's cutovers
// ([App.SignalCutovers]): it returns the warnings of [CheckSignal] for a
// Signal it wrote, and the refusal for one it did not.
func InsertSignalWithWarnings(ctx context.Context, db DB, s *Signal, c SignalCutovers) ([]CreateWarning, error) {
	warnings, err := CheckSignal(ctx, s, c)
	if err != nil {
		return nil, err
	}
	// D113: the subject and task_ref must name records the caller can read;
	// they are stored canonically and related in the insert's transaction.
	plan, err := prepareSignalReferences(ctx, db, s)
	if err != nil {
		return nil, err
	}
	s.Status = Status(signalInitialState(s.SignalType))
	return warnings, insertSignalRow(ctx, db, s, plan)
}

// insertSignalRow is the write of [InsertSignalWithWarnings].
func insertSignalRow(ctx context.Context, db DB, s *Signal, plan signalReferencePlan) error {
	if s.ID == "" {
		s.ID = NewID()
	}
	if s.Slug == "" {
		s.Slug = s.ID
	}
	now := time.Now().UTC()
	if s.CreatedAt.IsZero() {
		s.CreatedAt = now
	}
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = now
	}
	err := runInTx(ctx, db, func(exec DB) error {
		// D110: the Signal's address is taken on this transaction.
		address, no, err := allocateAddress(ctx, exec, "Signal")
		if err != nil {
			return err
		}
		s.Address, s.AddressNo = address, no
		_, err = exec.ExecContext(ctx,
			`INSERT INTO smeldr_signals
				(id, slug, status, created_at, updated_at, sender, receiver, signal_type, message, task_ref, sequence,
				 subject_type, subject_id, from_state, to_state, required_role, address, address_no, external_ref)
			VALUES
				($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)`,
			s.ID, s.Slug, string(s.Status), s.CreatedAt, s.UpdatedAt, s.Sender, s.Receiver, s.SignalType, s.Message,
			s.TaskRef, s.Sequence, s.SubjectType, s.SubjectID, s.FromState, s.ToState, s.RequiredOperation,
			s.Address, s.AddressNo, s.ExternalRef,
		)
		if err != nil {
			return fmt.Errorf("smeldr: InsertSignal: %w", err)
		}
		return writeSignalReferenceEdges(ctx, exec, plan, s)
	})
	if err != nil {
		// Nothing was committed, so the Signal has no address.
		s.Address, s.AddressNo = "", 0
	}
	return err
}
