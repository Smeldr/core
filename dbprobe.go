// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// sqlStater is implemented by a database driver's error that carries a SQLSTATE
// code (pgx's *pgconn.PgError does). Core imports no driver, so it asks for the
// method, not the type.
type sqlStater interface{ SQLState() string }

// sqlState returns the SQLSTATE of err, or "" when no error in its chain has one.
func sqlState(err error) string {
	var s sqlStater
	if errors.As(err, &s) {
		return s.SQLState()
	}
	return ""
}

// isNoSuchTable reports whether err says a table does not exist, on SQLite
// ("no such table") or on Postgres (SQLSTATE 42P01, or the message
// `relation "x" does not exist` from a driver that hides the code).
func isNoSuchTable(err error) bool {
	if err == nil {
		return false
	}
	if sqlState(err) == "42P01" {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "no such table") ||
		(strings.Contains(msg, `relation "`) && strings.Contains(msg, "does not exist"))
}

// isNoSuchColumn reports whether err says the given column does not exist, used
// to fail open on a table that predates a column (a third-party module table
// this framework does not control), the same graceful-degradation shape
// [isNoSuchTable] provides for a missing table.
//
// SQLite (via modernc.org/sqlite) reports a missing column with two message
// shapes depending on the statement kind: an UPDATE or SELECT produces
// "no such column: <column>", an INSERT naming it produces "table <table> has no
// column named <column>". Postgres (SQLSTATE 42703) produces
// `column "<column>" does not exist` for a SELECT or UPDATE and
// `column "<column>" of relation "<table>" does not exist` for an INSERT. The
// column name is always matched, so an error about another column is not taken
// for this one.
func isNoSuchColumn(err error, column string) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if sqliteMissingColumn(msg, column) || strings.Contains(msg, "no column named "+column) {
		return true
	}
	return (sqlState(err) == "42703" || strings.Contains(msg, "does not exist")) && pgNamesColumn(msg, column)
}

// pgNamesColumn reports whether a Postgres error message names column: the
// quoted form `column "x" ...`, and the qualified form `column t.x does not exist`
// a SELECT that names its table produces (Postgres leaves the qualified name
// unquoted).
func pgNamesColumn(msg, column string) bool {
	return strings.Contains(msg, `column "`+column+`"`) ||
		strings.Contains(msg, "."+column+" does not exist") ||
		strings.Contains(msg, "column "+column+" does not exist")
}

// sqliteMissingColumn matches SQLite's "no such column: x" and its qualified form
// "no such column: table.x", which a probe that names the table produces.
func sqliteMissingColumn(msg, column string) bool {
	_, rest, found := strings.Cut(msg, "no such column: ")
	if !found {
		return false
	}
	name, _, _ := strings.Cut(rest, " ")
	return name == column || strings.HasSuffix(name, "."+column)
}

// isDuplicateColumn reports whether err says the column being added already
// exists: SQLite "duplicate column name: x", Postgres SQLSTATE 42701 or
// `column "x" of relation "y" already exists`. Two processes booting together can
// both see a column absent and both add it; the loser has nothing left to do.
func isDuplicateColumn(err error, column string) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if strings.Contains(msg, "duplicate column name: "+column) {
		return true
	}
	return (sqlState(err) == "42701" || strings.Contains(msg, "already exists")) && pgNamesColumn(msg, column)
}

// refuseInTx turns a probe handed a transaction into an error instead of a poisoned
// transaction. On Postgres a failed statement aborts the whole transaction, and a
// probe fails by design when the table or column is absent, so a probe must run on
// the base handle. Only a bare *sql.Tx is recognised, which is what [conflictTx]
// hands out; a wrapper that hides one is the caller's to keep off the probes.
func refuseInTx(db DB, what string) error {
	if _, ok := db.(*sql.Tx); ok {
		return fmt.Errorf("%w: %s must run on the base handle, not inside a transaction", ErrInternal, what)
	}
	return nil
}

// probeAbandoned wraps the ending of ctx as [ErrInternal], so a cancelled or
// timed-out call is never read as "nothing to enforce" or "no such table".
func probeAbandoned(ctx context.Context, what string) error {
	return fmt.Errorf("%w: %s abandoned, the context ended: %s", ErrInternal, what, ctx.Err())
}

// tableExists reports whether table exists, by selecting no rows from it. That is
// valid SQL on every supported database, unlike sqlite_master, PRAGMA or
// information_schema, and a missing table is an error the driver reports in a
// form [isNoSuchTable] recognises. Any other error is returned.
//
// Never call it inside a transaction: on Postgres a failed statement aborts the
// whole transaction, so the probe would poison the work around it. Callers hold
// the base handle, not a transaction, when they ask.
func tableExists(ctx context.Context, db DB, table string) (bool, error) {
	if err := refuseInTx(db, "table check"); err != nil {
		return false, err
	}
	rows, err := db.QueryContext(ctx, "SELECT 1 FROM "+quoteIdent(table)+" WHERE 1=0")
	if err == nil {
		closeProbe(rows)
		return true, nil
	}
	if ctx.Err() != nil {
		return false, probeAbandoned(ctx, "table check")
	}
	if isNoSuchTable(err) {
		return false, nil
	}
	return false, err
}

// columnExists reports whether table has column. A missing table counts as a
// missing column: the caller that goes on to add the column then gets the
// database's own, wrapped, error for the missing table. Same rules as
// [tableExists], including never inside a transaction.
func columnExists(ctx context.Context, db DB, table, column string) (bool, error) {
	if err := refuseInTx(db, "column check"); err != nil {
		return false, err
	}
	// The column is qualified with its table on purpose: SQLite reads a
	// double-quoted name that matches no column as a string literal, so an
	// unqualified "x" would succeed for a column that is not there. Qualified, it
	// is an error on SQLite and on Postgres alike.
	qt := quoteIdent(table)
	rows, err := db.QueryContext(ctx, "SELECT "+qt+"."+quoteIdent(column)+" FROM "+qt+" WHERE 1=0")
	if err == nil {
		closeProbe(rows)
		return true, nil
	}
	if ctx.Err() != nil {
		return false, probeAbandoned(ctx, "column check")
	}
	if isNoSuchTable(err) || isNoSuchColumn(err, column) {
		return false, nil
	}
	return false, err
}

// flowTablesPresent reports whether the state flow tables exist, which is the one
// question every state machine caller asks before it enforces anything (D103):
// when smeldr_state_flows is not there, no flow is registered and there is
// nothing to enforce. It asks the same way on every supported database, so the
// state machine (transition validation and role gates, Locked, SuppressesSignals,
// the initial state, async triggers, ConflictPolicy) is enforced on SQLite and on
// Postgres alike.
//
// A probe that fails because ctx has ended is returned as [ErrInternal], and so
// is any other failure that is not a missing table: a cancelled call, a dropped
// connection or a permission error can never be read as "no flow to enforce" and
// silently skip a gate, a lock or the conflict policy.
//
// Like [tableExists] it must run on the base handle, before any transaction
// begins.
func flowTablesPresent(ctx context.Context, db DB) (bool, error) {
	if err := refuseInTx(db, "state flow check"); err != nil {
		return false, err
	}
	rows, err := db.QueryContext(ctx, `SELECT 1 FROM smeldr_state_flows LIMIT 1`)
	if err == nil {
		closeProbe(rows)
		return true, nil
	}
	if ctx.Err() != nil {
		return false, probeAbandoned(ctx, "state flow check")
	}
	if isNoSuchTable(err) {
		return false, nil
	}
	return false, fmt.Errorf("%w: state flow check failed: %s", ErrInternal, err)
}

// flowTablesPresentFailOpen is [flowTablesPresent] for the callers that cannot
// return an error (they answer a bool or a string, or nothing): a failed probe is
// logged at Warn and they fail open, as they always have, so a failure is at
// least visible.
func flowTablesPresentFailOpen(ctx context.Context, db DB, caller string) bool {
	ok, err := flowTablesPresent(ctx, db)
	if err != nil {
		slog.WarnContext(ctx, "smeldr: state flow check skipped", "caller", caller, "error", err)
		return false
	}
	return ok
}

// closeProbe closes the rows of a probe that selected nothing. A handle that
// answers a query with neither rows nor an error (a test double, a broken driver)
// is read as a probe that succeeded, not a panic.
func closeProbe(rows *sql.Rows) {
	if rows != nil {
		rows.Close()
	}
}
