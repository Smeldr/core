package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// oldDSN is how smeldr-server opened its file before T1013.
func oldDSN(path string) string { return path + "?_pragma=journal_mode(WAL)" }

func TestSQLiteDSN(t *testing.T) {
	got := sqliteDSN("data/smeldr.db", 5*time.Second)
	for _, want := range []string{"data/smeldr.db?", "_pragma=journal_mode(WAL)", "_pragma=busy_timeout(5000)", "_txlock=immediate"} {
		if !strings.Contains(got, want) {
			t.Errorf("sqliteDSN = %q; missing %q", got, want)
		}
	}
}

// openPair opens the same new file twice, as two processes would: the first
// with dsnA, the second with dsnB, each with one connection.
func openPair(t *testing.T, dsnA, dsnB func(string) string) (*sql.DB, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "shared.db")
	open := func(dsn string) *sql.DB {
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		t.Cleanup(func() { db.Close() })
		return db
	}
	a := open(dsnA(path))
	if _, err := a.Exec(`CREATE TABLE items (id INTEGER PRIMARY KEY, n INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Exec(`INSERT INTO items (id, n) VALUES (1, 0)`); err != nil {
		t.Fatal(err)
	}
	return a, open(dsnB(path))
}

func newDSN(path string) string { return sqliteDSN(path, 5*time.Second) }

// isBusy reports SQLite's busy failure: SQLITE_BUSY (5), or SQLITE_BUSY_SNAPSHOT
// (517) for a stale WAL snapshot, both "database is locked".
func isBusy(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "database is locked") || strings.Contains(err.Error(), "SQLITE_BUSY"))
}

// While another process holds the write lock, a write with the new DSN waits
// for it and succeeds; with the old one it fails at once.
func TestSQLiteDSN_WaitsForAnotherWriter(t *testing.T) {
	for _, c := range []struct {
		name     string
		dsn      func(string) string
		wantBusy bool
	}{
		{"old DSN", oldDSN, true},
		{"sqliteDSN", newDSN, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, b := openPair(t, newDSN, c.dsn)
			tx, err := a.BeginTx(context.Background(), nil) // BEGIN IMMEDIATE under newDSN
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(`UPDATE items SET n = n + 1 WHERE id = 1`); err != nil {
				t.Fatal(err)
			}
			released := make(chan struct{})
			go func() {
				time.Sleep(300 * time.Millisecond)
				tx.Commit() //nolint:errcheck
				close(released)
			}()
			_, err = b.Exec(`UPDATE items SET n = n + 10 WHERE id = 1`)
			<-released
			if busy := isBusy(err); busy != c.wantBusy {
				t.Errorf("second writer: err = %v; want busy = %v", err, c.wantBusy)
			}
		})
	}
}

// A transaction that reads, then writes after another process committed, fails
// at once on its stale snapshot with a deferred BEGIN; with _txlock=immediate
// its BEGIN holds the lock, the other writer waits, and both succeed.
func TestSQLiteDSN_ImmediateAvoidsStaleSnapshot(t *testing.T) {
	for _, c := range []struct {
		name     string
		dsn      func(string) string
		wantBusy bool
	}{
		{"old DSN", oldDSN, true},
		{"sqliteDSN", newDSN, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, b := openPair(t, c.dsn, c.dsn)
			tx, err := a.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			var n int
			if err := tx.QueryRow(`SELECT n FROM items WHERE id = 1`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			otherDone := make(chan error, 1)
			go func() {
				_, err := b.Exec(`UPDATE items SET n = n + 10 WHERE id = 1`)
				otherDone <- err
			}()
			if !c.wantBusy {
				time.Sleep(200 * time.Millisecond) // the other writer is waiting on A's lock
			} else if err := <-otherDone; err != nil {
				t.Fatalf("the other writer, with nothing holding the lock: %v", err)
			}
			_, werr := tx.Exec(`UPDATE items SET n = ? WHERE id = 1`, n+1)
			if werr == nil {
				werr = tx.Commit()
			} else {
				tx.Rollback() //nolint:errcheck
			}
			if busy := isBusy(werr); busy != c.wantBusy {
				t.Errorf("the reading transaction's write: err = %v; want busy = %v", werr, c.wantBusy)
			}
			if !c.wantBusy {
				if err := <-otherDone; err != nil {
					t.Errorf("the other writer, after waiting: %v", err)
				}
			}
		})
	}
}
