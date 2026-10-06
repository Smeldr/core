// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func fallbackDB(t *testing.T) *sql.DB {
	t.Helper()
	db := newSQLiteDB(t)
	if _, err := db.Exec(`CREATE TABLE fb (id TEXT PRIMARY KEY, status TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if _, err := db.Exec(`INSERT INTO fb (id, status) VALUES (?, 'old')`, id); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func fbStatus(t *testing.T, db DB, id string) string {
	t.Helper()
	var s string
	if err := db.QueryRowContext(context.Background(), `SELECT status FROM fb WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

const (
	fbWith    = `UPDATE fb SET status = $1, last_actor = $2 WHERE id = $3`
	fbWithout = `UPDATE fb SET status = $1 WHERE id = $2`
)

// TestExecWithColumnFallback covers both kinds of handle. The abort a failed
// statement causes on Postgres is Postgres behaviour and cannot be shown on SQLite;
// the pgx suite proves it. Here: the right statement runs, the fallback runs when
// the column is missing, another error is returned as it is, and a transaction
// stays usable and committable afterwards.
func TestExecWithColumnFallback(t *testing.T) {
	ctx := context.Background()
	run := func(exec DB, first string, a []any) error {
		return execWithColumnFallback(ctx, exec, "last_actor", first, a, fbWithout, []any{"new", "a"})
	}
	t.Run("a plain handle: the fallback runs for a missing column", func(t *testing.T) {
		db := fallbackDB(t)
		if err := run(db, fbWith, []any{"new", "u", "a"}); err != nil {
			t.Fatalf("err = %v", err)
		}
		if got := fbStatus(t, db, "a"); got != "new" {
			t.Errorf("status = %q, want new (the fallback form)", got)
		}
	})
	t.Run("a plain handle: another error is returned and nothing falls back", func(t *testing.T) {
		db := fallbackDB(t)
		err := run(db, `UPDATE fb SET nope = 1 WHERE id = $1`, []any{"a"})
		if err == nil || isNoSuchColumn(err, "last_actor") {
			t.Fatalf("err = %v, want the other column's error", err)
		}
		if got := fbStatus(t, db, "a"); got != "old" {
			t.Errorf("status = %q: the fallback ran for an unrelated error", got)
		}
	})
	t.Run("a transaction: the column exists, the first form runs", func(t *testing.T) {
		db := fallbackDB(t)
		if _, err := db.Exec(`ALTER TABLE fb ADD COLUMN last_actor TEXT NOT NULL DEFAULT ''`); err != nil {
			t.Fatal(err)
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback() //nolint:errcheck
		if err := run(tx, fbWith, []any{"new", "u", "a"}); err != nil {
			t.Fatalf("err = %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}
		var actor string
		if err := db.QueryRow(`SELECT last_actor FROM fb WHERE id = 'a'`).Scan(&actor); err != nil || actor != "u" {
			t.Errorf("last_actor = %q, %v, want u", actor, err)
		}
	})
	t.Run("a transaction: a missing column falls back and the transaction stays usable", func(t *testing.T) {
		db := fallbackDB(t)
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()                     //nolint:errcheck
		for _, id := range []string{"a", "b"} { // twice: the savepoint is released, never stacked
			if err := execWithColumnFallback(ctx, tx, "last_actor", fbWith, []any{"new", "u", id}, fbWithout, []any{"new", id}); err != nil {
				t.Fatalf("%s: %v", id, err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}
		for _, id := range []string{"a", "b"} {
			if got := fbStatus(t, db, id); got != "new" {
				t.Errorf("%s = %q, want new", id, got)
			}
		}
	})
	t.Run("a transaction: another error is returned, not rolled back to the savepoint", func(t *testing.T) {
		db := fallbackDB(t)
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback() //nolint:errcheck
		if err := run(tx, `UPDATE fb SET nope = 1 WHERE id = $1`, []any{"a"}); err == nil {
			t.Fatal("want the error")
		}
		// SQLite's transaction survives a failed statement; the savepoint was released.
		if _, err := tx.ExecContext(ctx, `RELEASE SAVEPOINT `+columnFallbackSavepoint); err == nil {
			t.Error("the savepoint was still open after the helper returned")
		}
	})
	t.Run("a transaction that cannot open a savepoint returns the error", func(t *testing.T) {
		db := fallbackDB(t)
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = tx.Rollback()
		if err := run(tx, fbWith, []any{"new", "u", "a"}); !errors.Is(err, sql.ErrTxDone) {
			t.Errorf("err = %v, want sql.ErrTxDone", err)
		}
	})
}

// TestSupersede_TableWithoutLastActor_InATransaction: the D78 fail-open for a table
// that predates last_actor, through the whole conflict path (the winner's write and
// the loser's write both on one transaction).
func TestSupersede_TableWithoutLastActor_InATransaction(t *testing.T) {
	app, db, _ := setupTransitionItemApp(t)
	registerConflictFlow(t, db, ConflictSupersede)
	if _, err := db.Exec(`ALTER TABLE conflict_types ADD COLUMN slug TEXT NOT NULL DEFAULT ''`); err != nil {
		t.Fatal(err)
	}
	insertConflictItem(t, db, "ct-old", "published")
	insertConflictItem(t, db, "ct-new", "draft")
	if _, err := db.Exec(`UPDATE conflict_types SET slug = id`); err != nil {
		t.Fatal(err)
	}
	app.typeRegistry.Register(&TypeDescriptor{Name: "ConflictType", Kind: "compiled"})

	if _, err := app.TransitionItem(context.Background(), "ConflictType", "ct-new", "published"); err != nil {
		t.Fatalf("TransitionItem on a table without last_actor: %v", err)
	}
	for id, want := range map[string]string{"ct-new": "published", "ct-old": "superseded"} {
		var got string
		if err := db.QueryRow(`SELECT status FROM conflict_types WHERE id = ?`, id).Scan(&got); err != nil || got != want {
			t.Errorf("%s = %q, %v, want %q", id, got, err, want)
		}
	}
}
