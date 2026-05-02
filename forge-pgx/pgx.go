// Package forgepgx provides a [forge.DB] adapter for pgx/v5 native connection
// pools. It bridges [pgxpool.Pool] to the [forge.DB] interface so Forge modules
// can use pgx's maximum-throughput connection pool without any changes to core
// Forge code.
//
// # Usage
//
//	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
//	if err != nil {
//	    log.Fatal(err)
//	}
//	app := forge.New(forge.Config{
//	    BaseURL: "https://example.com",
//	    Secret:  []byte(os.Getenv("SECRET")),
//	    DB:      forgepgx.Wrap(pool),
//	})
//
// See [Decision 22] in DECISIONS.md for performance rationale and driver
// comparison.
package forgepgx

import (
	"context"
	"database/sql"

	forge "forge-cms.dev/forge"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

// Compile-time assertion: poolAdapter must satisfy forge.DB.
var _ forge.DB = (*poolAdapter)(nil)

// poolAdapter wraps a pgx native pool and exposes the [forge.DB] interface.
// The underlying *sql.DB is opened once at construction time via
// [stdlib.OpenDBFromPool]; it wraps the pool without establishing any
// additional connections.
type poolAdapter struct {
	db *sql.DB
}

// Wrap returns a [forge.DB] backed by the given pgx connection pool.
//
// The pool must not be nil. Wrap calls [stdlib.OpenDBFromPool] once to create
// a *sql.DB wrapper; no network connections are established at this point.
//
//	pool, _ := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
//	db := forgepgx.Wrap(pool)
//	app := forge.New(forge.Config{DB: db, ...})
func Wrap(p *pgxpool.Pool) forge.DB {
	return &poolAdapter{db: stdlib.OpenDBFromPool(p)}
}

// QueryContext executes a query that returns rows, typically a SELECT.
// It satisfies [forge.DB] and delegates directly to the underlying *sql.DB.
func (a *poolAdapter) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return a.db.QueryContext(ctx, query, args...)
}

// ExecContext executes a query that does not return rows, typically INSERT,
// UPDATE, or DELETE. It satisfies [forge.DB] and delegates to the underlying
// *sql.DB.
func (a *poolAdapter) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return a.db.ExecContext(ctx, query, args...)
}

// QueryRowContext executes a query that is expected to return at most one row.
// It satisfies [forge.DB] and delegates to the underlying *sql.DB.
func (a *poolAdapter) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return a.db.QueryRowContext(ctx, query, args...)
}
