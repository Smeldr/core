package pgx

import (
	"context"
	"database/sql"
	"testing"
)

// TestWrap_compilesAsSmeldrDB documents the compile-time guarantee that
// poolAdapter satisfies smeldr.DB. The actual enforcement is the package-level
// declaration in pgx.go:
//
//	var _ smeldr.DB = (*poolAdapter)(nil)
//
// This test exists to keep that guarantee visible in test output and ensure the
// file is included in all test builds. No database is required.
func TestWrap_compilesAsSmeldrDB(t *testing.T) {
	// Compilation of this package is the test.
	// No runtime check needed — the guarantee is enforced at compile time.
}

// poolAdapter provides the cross-process lock core's conflict policy asks for. The
// method signature is a stable contract (D22) for every adapter that implements it:
// core asserts the handle against exactly this shape, so a signature change would
// silently turn the lock off.
var _ interface {
	AcquireLock(ctx context.Context, name string) (release func(), err error)
} = (*poolAdapter)(nil)

// TestLockKey pins the advisory lock key of one known name. Processes of different
// versions share the lock, so the hash must never change.
func TestLockKey(t *testing.T) {
	const name = "smeldr:conflict:Task"
	const want = int64(-4667081937754432639)
	if got := lockKey(name); got != want {
		t.Errorf("lockKey(%q) = %d, want %d: a changed hash splits the lock between versions", name, got, want)
	}
	if lockKey("smeldr:conflict:Task") == lockKey("smeldr:conflict:Decision") {
		t.Error("two type names share a lock key")
	}
}

// poolAdapter provides BeginTx with exactly the signature core asserts against
// (core's txBeginner); a different shape would silently turn every transaction
// path off on Postgres.
var _ interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
} = (*poolAdapter)(nil)
