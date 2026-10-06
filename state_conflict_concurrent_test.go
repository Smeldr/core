// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"
)

// beginBarrierDB holds every transaction at BeginTx until n callers are there,
// or until timeout, so a test can make two winners both finish their conflict
// reads before either writes. It is the race window itself: planConflict's reads
// are done by the time BeginTx is called and the winner's write comes after.
type beginBarrierDB struct {
	DB
	sqlDB   *sql.DB
	n       int
	timeout time.Duration

	mu      sync.Mutex
	waiting int
	release chan struct{}
}

func newBeginBarrierDB(db *sql.DB, n int, timeout time.Duration) *beginBarrierDB {
	return &beginBarrierDB{DB: db, sqlDB: db, n: n, timeout: timeout, release: make(chan struct{})}
}

func (b *beginBarrierDB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	b.mu.Lock()
	b.waiting++
	if b.waiting == b.n {
		close(b.release)
	}
	b.mu.Unlock()
	select {
	case <-b.release:
	case <-time.After(b.timeout):
	}
	return b.sqlDB.BeginTx(ctx, opts)
}

func countStatus(t *testing.T, db *sql.DB, status string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM smeldr_signals WHERE status = $1`, status).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", status, err)
	}
	return n
}

// raceTwoWinners transitions winner-a and winner-b into the active state at the
// same moment and returns both errors.
func raceTwoWinners(t *testing.T, app *App, db *sql.DB) (errA, errB error) {
	t.Helper()
	start := time.Now()
	defer func() { assertRaceFast(t, start) }()
	app.cfg.DB = newBeginBarrierDB(db, 2, 400*time.Millisecond)
	var wg sync.WaitGroup
	wg.Add(2)
	run := func(slug string, out *error) {
		defer wg.Done()
		_, *out = app.TransitionItemVia(NewTestContext(User{ID: "u-" + slug, Roles: []Role{Editor}}), "mcp", "Signal", slug, "read", "")
	}
	go run("winner-a-slug", &errA)
	go run("winner-b-slug", &errB)
	wg.Wait()
	return errA, errB
}

// TestConflictSupersede_ConcurrentWinners: two items entering the active state at
// once. Both used to read the same loser list before either wrote, so both became
// active. Exactly one item may be active afterwards, and it is the one that
// committed last (each winner supersedes whoever was active when it took its turn).
func TestConflictSupersede_ConcurrentWinners(t *testing.T) {
	app, db, _ := edgeTestApp(t, "read", signalSupersedeStates, signalSupersedeTransitions)
	insertSignal(t, db, "old", "old-slug", "read")
	insertSignal(t, db, "winner-a", "winner-a-slug", "pending")
	insertSignal(t, db, "winner-b", "winner-b-slug", "pending")

	errA, errB := raceTwoWinners(t, app, db)
	if errA != nil || errB != nil {
		t.Fatalf("both winners should succeed under supersede: %v, %v", errA, errB)
	}
	if n := countStatus(t, db, "read"); n != 1 {
		t.Errorf("%d items in the active state, want exactly 1", n)
	}
	if s := signalStatusOf(t, db, "old"); s != "superseded" {
		t.Errorf("old = %q, want superseded", s)
	}
}

// TestConflictReject_ConcurrentWinners: under reject the second of two concurrent
// winners must get ErrConflict, not also become active.
func TestConflictReject_ConcurrentWinners(t *testing.T) {
	app, db, _ := edgeTestApp(t, "read", signalSupersedeStates, signalSupersedeTransitions)
	if err := app.RegisterFlow(StateFlow{
		Name: "signal-protocol", TypeName: "Signal",
		ActiveState: "read", ConflictPolicy: ConflictReject,
		States: signalSupersedeStates, Transitions: signalSupersedeTransitions,
	}); err != nil {
		t.Fatalf("RegisterFlow reject: %v", err)
	}
	insertSignal(t, db, "winner-a", "winner-a-slug", "pending")
	insertSignal(t, db, "winner-b", "winner-b-slug", "pending")

	errA, errB := raceTwoWinners(t, app, db)
	rejected := 0
	for _, err := range []error{errA, errB} {
		if errors.Is(err, ErrConflict) {
			rejected++
		} else if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if n := countStatus(t, db, "read"); n != 1 {
		t.Errorf("%d items in the active state, want exactly 1", n)
	}
	if rejected != 1 {
		t.Errorf("%d winners rejected, want exactly 1 (errors: %v, %v)", rejected, errA, errB)
	}
}

// assertRaceFast fails a race test that ran into the lock's wait bound. A winner
// that never released its lock makes the next one wait the full conflictLockWait
// (10 s), which would still end with one active item, so the clock is what
// catches a missing release at a call site.
func assertRaceFast(t *testing.T, start time.Time) {
	t.Helper()
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("the race took %v: a winner did not release the conflict lock", d)
	}
}
