// Package pgx provides a [smeldr.DB] adapter for pgx/v5 native connection
// pools. It bridges [pgxpool.Pool] to the [smeldr.DB] interface so Smeldr
// modules can use pgx's maximum-throughput connection pool without any
// changes to core Smeldr code.
//
// # Usage
//
//	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
//	if err != nil {
//	    log.Fatal(err)
//	}
//	app := smeldr.New(smeldr.Config{
//	    BaseURL: "https://example.com",
//	    Secret:  []byte(os.Getenv("SECRET")),
//	    DB:      pgx.Wrap(pool),
//	})
//
// # Transactions
//
// The handle Wrap returns also provides BeginTx, so core's multi-statement writes
// (the conflict policy's winner and loser writes, an audited governance mutation, a
// relation diff) are atomic on Postgres from adapter v0.3.0. Before it they ran
// statement by statement.
//
// # Cross-process locking
//
// The handle Wrap returns also provides AcquireLock, which core's conflict policy
// uses to exclude other application processes on the same database, not only other
// goroutines of this one: a Postgres advisory lock on a dedicated pooled
// connection. A wrapper that embeds smeldr.DB hides the method; forward it to keep
// the lock. The lock is refused (logged at Error by core, the transition goes ahead
// without it) whenever fewer than two pool connections are free at that moment, so
// it can trip under ordinary load on a small or busy pool: a recurring Error line
// means raise MaxConns.
//
// See [Decision 22] in DECISIONS.md for performance rationale and driver
// comparison.
package pgx

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"hash/fnv"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"smeldr.dev/core"
)

// Compile-time assertion: poolAdapter must satisfy smeldr.DB.
var _ smeldr.DB = (*poolAdapter)(nil)

// poolAdapter wraps a pgx native pool and exposes the [smeldr.DB] interface.
// The underlying *sql.DB is opened once at construction time via
// [stdlib.OpenDBFromPool]; it wraps the pool without establishing any
// additional connections.
type poolAdapter struct {
	db   *sql.DB
	pool *pgxpool.Pool
}

// Wrap returns a [smeldr.DB] backed by the given pgx connection pool.
//
// The pool must not be nil. Wrap calls [stdlib.OpenDBFromPool] once to create
// a *sql.DB wrapper; no network connections are established at this point.
//
//	pool, _ := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
//	db := pgx.Wrap(pool)
//	app := smeldr.New(smeldr.Config{DB: db, ...})
func Wrap(p *pgxpool.Pool) smeldr.DB {
	return &poolAdapter{db: stdlib.OpenDBFromPool(p), pool: p}
}

// QueryContext executes a query that returns rows, typically a SELECT.
// It satisfies [smeldr.DB] and delegates directly to the underlying *sql.DB.
func (a *poolAdapter) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return a.db.QueryContext(ctx, query, args...)
}

// ExecContext executes a query that does not return rows, typically INSERT,
// UPDATE, or DELETE. It satisfies [smeldr.DB] and delegates to the underlying
// *sql.DB.
func (a *poolAdapter) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return a.db.ExecContext(ctx, query, args...)
}

// QueryRowContext executes a query that is expected to return at most one row.
// It satisfies [smeldr.DB] and delegates to the underlying *sql.DB.
func (a *poolAdapter) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return a.db.QueryRowContext(ctx, query, args...)
}

// lockKey is the 64-bit key of the Postgres advisory lock for a lock name: FNV-1a
// 64 over the name, as a signed integer. It is computed here, in Go, so every
// process derives the same key without a server function (hashtextextended is an
// internal function, not stable API, and needs Postgres 11). The key of a given
// name must never change: processes of different versions share the lock.
func lockKey(name string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	return int64(h.Sum64())
}

// BeginTx starts a transaction on one pooled connection, under the default READ
// COMMITTED. Core opens a transaction only when its handle has exactly this method:
// the conflict policy's winner and loser writes, a governance mutation with its
// audit record, a relation diff and the legacy table rename. Without it (adapter
// v0.2.x and earlier) each of them ran statement by statement, with no atomicity.
// On Postgres a failed statement aborts the whole transaction; core's one statement
// that is expected to fail sometimes (the last_actor fail-open) runs under a
// savepoint for that reason.
func (a *poolAdapter) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return a.db.BeginTx(ctx, opts)
}

// AcquireLock takes the Postgres advisory lock for name and returns the function
// that gives it up. It is the cross-process half of the conflict policy: core asks
// a handle that provides it (smeldr.dev/core's unexported crossProcessLocker, which
// this method satisfies structurally) for the lock of a type before it reads who
// holds the active state, and gives it up after the winner's write is committed,
// so a second process sees the first winner.
//
// The method signature and the "smeldr:conflict:" name prefix are a stable contract
// for every adapter that implements it. The lock is a session lock held on one
// dedicated connection taken from the pool for as long as the lock is held, because
// the statements the policy and the write issue run on other pooled connections.
// The wait ends when ctx ends; core bounds it. The lock is refused at once (an
// error, which core logs at Error before it goes ahead without the cross-process
// lock) whenever fewer than two pool connections are free at that moment, because
// a holder that took the last one could not run its own statements. That can happen
// under ordinary load on a small or busy pool, not only on a pool of one: a
// recurring Error line from core about the cross-process lock means raise MaxConns. If the lock cannot be given up cleanly the
// connection is discarded, so a lock never rides back into the pool.
func (a *poolAdapter) AcquireLock(ctx context.Context, name string) (func(), error) {
	if a.pool != nil {
		if st := a.pool.Stat(); st.MaxConns()-st.AcquiredConns() <= 1 {
			return nil, fmt.Errorf("pgx: not taking the lock %q: the pool (max %d, in use %d) has no connection to spare for the statements of the lock's holder", name, st.MaxConns(), st.AcquiredConns())
		}
	}
	conn, err := a.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("pgx: lock %q: take a connection: %w", name, err)
	}
	key := lockKey(name)
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1::bigint)`, key); err != nil {
		_ = conn.Raw(func(any) error { return driver.ErrBadConn }) // a cancelled wait leaves the session in an unknown state
		_ = conn.Close()
		return nil, fmt.Errorf("pgx: lock %q: %w", name, err)
	}
	return func() {
		uctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var held bool
		err := conn.QueryRowContext(uctx, `SELECT pg_advisory_unlock($1::bigint)`, key).Scan(&held)
		switch {
		case err != nil:
			slog.Error("smeldr: pgx: could not give up the advisory lock, discarding its connection", "lock", name, "error", err)
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		case !held:
			slog.Error("smeldr: pgx: the advisory lock was not held when it was given up: its connection was lost while the lock was in use, so another process may have taken the lock meanwhile",
				"lock", name)
		}
		_ = conn.Close()
	}, nil
}
