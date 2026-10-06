//go:build integration

package pgx

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	smeldr "smeldr.dev/core"
)

type locker interface {
	AcquireLock(ctx context.Context, name string) (func(), error)
}

func lockerOf(t *testing.T, db smeldr.DB) locker {
	t.Helper()
	l, ok := db.(locker)
	if !ok {
		t.Fatal("the handle pgx.Wrap returns does not provide AcquireLock: core would silently keep the in-process lock only")
	}
	return l
}

// testCtx gives a test its own deadline: a lock that a mutation or a defect never
// hands over ends the test with a failure instead of hanging the suite.
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// take acquires the lock and registers its release as a cleanup (run once, however
// often it is called), so a test that fails midway still gives the connection back
// and pool.Close does not wait for it.
func take(t *testing.T, ctx context.Context, l locker, name string) func() {
	t.Helper()
	release, err := l.AcquireLock(ctx, name)
	if err != nil {
		t.Fatalf("take %s: %v", name, err)
	}
	var once sync.Once
	give := func() { once.Do(release) }
	t.Cleanup(give)
	return give
}

func TestPG_AcquireLock_ExcludesAndReleases(t *testing.T) {
	db, _ := isolatedDB(t)
	l := lockerOf(t, db)
	ctx := testCtx(t)

	release := take(t, ctx, l, "smeldr:conflict:Thing")
	// A second taker waits for the first.
	got := make(chan error, 1)
	go func() {
		rel, err := l.AcquireLock(ctx, "smeldr:conflict:Thing")
		if err == nil {
			rel()
		}
		got <- err
	}()
	select {
	case err := <-got:
		t.Fatalf("a second taker got the lock while the first held it (err = %v)", err)
	case <-time.After(300 * time.Millisecond):
	}
	// A different name is a different lock.
	take(t, ctx, l, "smeldr:conflict:Other")()

	release()
	select {
	case err := <-got:
		if err != nil {
			t.Fatalf("second taker after release: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("the second taker never got the lock after the first released it")
	}
}

func TestPG_AcquireLock_WaitEndsWithTheContext(t *testing.T) {
	db, _ := isolatedDB(t)
	l := lockerOf(t, db)
	take(t, testCtx(t), l, "smeldr:conflict:Thing")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := l.AcquireLock(ctx, "smeldr:conflict:Thing"); err == nil {
		t.Fatal("a wait that outlived its context must be an error")
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("the wait took %v after a 300 ms deadline", time.Since(start))
	}
}

// TestPG_AcquireLock_TheNextTakerSeesTheHoldersCommittedWrite is the point of the
// lock. The holder writes, gives the lock up, and the waiting taker's first read
// then sees the write.
func TestPG_AcquireLock_TheNextTakerSeesTheHoldersCommittedWrite(t *testing.T) {
	db, _ := isolatedDB(t)
	l := lockerOf(t, db)
	ctx := testCtx(t)
	if _, err := db.ExecContext(ctx, `CREATE TABLE holders (id TEXT PRIMARY KEY, status TEXT NOT NULL)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	release := take(t, ctx, l, "smeldr:conflict:Thing")
	seen := make(chan int, 1)
	go func() {
		rel, err := l.AcquireLock(ctx, "smeldr:conflict:Thing")
		if err != nil {
			seen <- -1
			return
		}
		defer rel()
		var n int
		_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM holders WHERE status = 'published'`).Scan(&n)
		seen <- n
	}()
	time.Sleep(200 * time.Millisecond) // the second taker is now waiting
	if _, err := db.ExecContext(ctx, `INSERT INTO holders (id, status) VALUES ('a', 'published')`); err != nil {
		t.Fatalf("holder's write: %v", err)
	}
	release()
	select {
	case n := <-seen:
		if n != 1 {
			t.Errorf("the next taker saw %d published items after the lock was given up, want 1 (the holder's write)", n)
		}
	case <-ctx.Done():
		t.Fatal("the next taker never got the lock")
	}
}

func openPool(t *testing.T, mutate func(*pgxpool.Config)) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping Postgres integration test")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if mutate != nil {
		mutate(cfg)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestPG_AcquireLock_ConnectionIsReturnedAndLockFreed: after release nothing stays
// checked out of the pool and a fresh taker gets the lock at once.
func TestPG_AcquireLock_ConnectionIsReturnedAndLockFreed(t *testing.T) {
	ctx := context.Background()
	pool := openPool(t, nil)
	l := lockerOf(t, Wrap(pool))
	for i := 0; i < 3; i++ {
		release, err := l.AcquireLock(ctx, "smeldr:conflict:Reused")
		if err != nil {
			t.Fatalf("taker %d: %v", i, err)
		}
		release()
	}
	deadline := time.Now().Add(3 * time.Second)
	for pool.Stat().AcquiredConns() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d connections still checked out after every lock was released", pool.Stat().AcquiredConns())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestPG_AcquireLock_LostConnectionIsNotFatal: the holder's connection is killed
// while the lock is held. Postgres drops a session lock with its session, so the
// release cannot find it: that is logged and the pool stays usable.
func TestPG_AcquireLock_LostConnectionIsNotFatal(t *testing.T) {
	ctx := context.Background()
	pool := openPool(t, nil)
	db := Wrap(pool)
	l := lockerOf(t, db)
	release, err := l.AcquireLock(ctx, "smeldr:conflict:Killed")
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_locks WHERE locktype = 'advisory' AND granted AND pid <> pg_backend_pid()`); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	out := captureLog(func() { release() })
	if !strings.Contains(out, "level=ERROR") {
		t.Errorf("a lock whose connection was lost must be logged at Error:\n%s", out)
	}
	if _, err := db.ExecContext(ctx, `SELECT 1`); err != nil {
		t.Errorf("the pool must stay usable: %v", err)
	}
	again, err := l.AcquireLock(ctx, "smeldr:conflict:Killed")
	if err != nil {
		t.Fatalf("the lock must be free again: %v", err)
	}
	again()
}

func captureLog(fn func()) string {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)
	fn()
	return buf.String()
}

// TestPG_AcquireLock_SmallPoolIsRefusedNotHung: with a pool of one connection the
// lock would take the only connection and its holder could not run its own
// statements. It is refused at once, and a transition into an active state still
// completes, with the Error line, instead of hanging.
func TestPG_AcquireLock_SmallPoolIsRefusedNotHung(t *testing.T) {
	ctx := context.Background()
	admin := openPool(t, nil)
	schema := "t_" + strings.ReplaceAll(strings.ToLower(smeldr.NewID()), "-", "")
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") })
	pool := openPool(t, func(cfg *pgxpool.Config) {
		cfg.ConnConfig.RuntimeParams["search_path"] = schema
		cfg.MaxConns = 1
	})
	db := Wrap(pool)

	if _, err := lockerOf(t, db).AcquireLock(ctx, "smeldr:conflict:Thing"); err == nil {
		t.Fatal("a pool of one connection must refuse the lock")
	}

	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte(pgTestSecret), DB: db})
	createItemsTable(t, db, "pg_items")
	registerType(app, "PgItem")
	flow := pgFlow("PgItem")
	flow.Transitions = []smeldr.Transition{{From: "draft", To: "published"}} // no gate: this test is about the lock
	flow.Triggers = nil
	if err := app.RegisterFlow(flow); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	insertItem(t, db, "pg_items", "a", "draft")

	var terr error
	var out string
	done := make(chan struct{})
	go func() {
		defer close(done)
		out = captureLog(func() { _, terr = app.TransitionItem(ctx, "PgItem", "a", "published") })
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the transition hung on a one-connection pool")
	}
	if terr != nil {
		t.Errorf("the transition must complete: %v", terr)
	}
	if errors.Is(terr, smeldr.ErrInternal) {
		t.Errorf("not an internal error: %v", terr)
	}
	for _, want := range []string{"level=ERROR", "cross-process lock not taken", "lock=smeldr:conflict:PgItem"} {
		if !strings.Contains(out, want) {
			t.Errorf("the Error line must say %q:\n%s", want, out)
		}
	}
	if got := statusOf(t, db, "pg_items", "a"); got != "published" {
		t.Errorf("status = %q, want published", got)
	}
}
