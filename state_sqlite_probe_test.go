// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// notSQLiteDB is a real SQLite handle that answers the sqlite_master probe the way
// another database would: with an error. Everything else passes through, so a
// flow can be registered on it, which is what a Postgres handle allows.
type notSQLiteDB struct{ DB }

func (d notSQLiteDB) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	if strings.Contains(q, "sqlite_master") {
		return d.DB.QueryRowContext(ctx, `SELECT * FROM relation_that_does_not_exist`)
	}
	return d.DB.QueryRowContext(ctx, q, args...)
}

func cancelled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestIsSQLite(t *testing.T) {
	db := newMigratedDB(t)
	if ok, err := isSQLite(context.Background(), db); !ok || err != nil {
		t.Errorf("a SQLite handle = %v, %v, want true, nil", ok, err)
	}
	if ok, err := isSQLite(context.Background(), notSQLiteDB{db}); ok || err != nil {
		t.Errorf("another database = %v, %v, want false, nil (not SQLite is not an error)", ok, err)
	}
	ok, err := isSQLite(cancelled(), db)
	if ok || !errors.Is(err, ErrInternal) {
		t.Errorf("a dead context = %v, %v, want false and ErrInternal", ok, err)
	}
	if ok, err := isSQLite(cancelled(), notSQLiteDB{db}); ok || !errors.Is(err, ErrInternal) {
		t.Errorf("a dead context on another database = %v, %v, want ErrInternal, not a skip", ok, err)
	}
}

// TestDeadContext_IsAnErrorNotASkip: before, a context that had already ended made
// the sqlite_master probe fail, and that took the 'not SQLite' branch: the gate,
// the lock or the policy was silently skipped for that call.
func TestDeadContext_IsAnErrorNotASkip(t *testing.T) {
	db := newMigratedDB(t)
	createConflictItemTable(t, db, true, ConflictReject)
	insertConflictItem(t, db, "a", "published")
	insertConflictItem(t, db, "b", "draft")
	dead := cancelled()

	t.Run("validateTransition", func(t *testing.T) {
		err := validateTransition(dead, db, nil, nil, "u", "b", "ConflictType", "draft", "published", "")
		if !errors.Is(err, ErrInternal) {
			t.Errorf("err = %v, want ErrInternal", err)
		}
	})
	t.Run("validateInitialState", func(t *testing.T) {
		if err := validateInitialState(dead, db, "ConflictType", "draft"); !errors.Is(err, ErrInternal) {
			t.Errorf("err = %v, want ErrInternal", err)
		}
	})
	t.Run("validateFlowItems", func(t *testing.T) {
		if err := validateFlowItems(dead, db, StateFlow{Name: "f", TypeName: "T"}); !errors.Is(err, ErrInternal) {
			t.Errorf("err = %v, want ErrInternal", err)
		}
	})
	t.Run("planConflict", func(t *testing.T) {
		plan, err := planConflict(dead, db, "ConflictType", "published", "b")
		if !errors.Is(err, ErrInternal) || plan != nil {
			t.Errorf("plan = %+v, err = %v, want nil and ErrInternal (never a plan that lets b in unchecked)", plan, err)
		}
	})
}

// TestDeadContext_FailOpenCallersLogIt: the callers that cannot return an error
// keep failing open, but a dead context is a visible Warn.
func TestDeadContext_FailOpenCallersLogIt(t *testing.T) {
	db := newMigratedDB(t)
	dead := cancelled()
	out := capturedLog(func() {
		if isStateLocked(dead, db, "ConflictType", "published") {
			t.Error("isStateLocked on a dead context must fail open (false)")
		}
		if suppressesSignals(dead, db, "ConflictType", "published") {
			t.Error("suppressesSignals on a dead context must fail open (false)")
		}
		if got := defaultInitialState(dead, db, "ConflictType"); got != "" {
			t.Errorf("defaultInitialState on a dead context = %q, want empty", got)
		}
		fireAsyncTriggers(dead, db, "ConflictType", "draft", "published", "x")
	})
	for _, caller := range []string{"isStateLocked", "suppressesSignals", "defaultInitialState", "fireAsyncTriggers"} {
		if !strings.Contains(out, "caller="+caller) {
			t.Errorf("no Warn for %s:\n%s", caller, out)
		}
	}
	if !strings.Contains(out, "level=WARN") {
		t.Errorf("the skip must be a Warn:\n%s", out)
	}
}

// TestLiveContext_NonSQLiteStaysFailOpen: the behaviour this Task states and does
// not change.
func TestLiveContext_NonSQLiteStaysFailOpen(t *testing.T) {
	db := newMigratedDB(t)
	createConflictItemTable(t, db, true, ConflictReject)
	insertConflictItem(t, db, "a", "published")
	other := notSQLiteDB{db}
	if err := validateTransition(context.Background(), other, nil, nil, "u", "b", "ConflictType", "draft", "nonsense", ""); err != nil {
		t.Errorf("validateTransition on another database = %v, want nil (not enforced)", err)
	}
	plan, err := planConflict(context.Background(), other, "ConflictType", "published", "b")
	if plan != nil || err != nil {
		t.Errorf("planConflict on another database = %+v, %v, want nil, nil", plan, err)
	}
}

func TestRegisterFlow_WarnsOnceWhereItIsNotEnforced(t *testing.T) {
	db := newMigratedDB(t)
	other := notSQLiteDB{db}
	nonSQLiteWarned.Delete(fmt.Sprintf("%T", other))
	app := &App{cfg: Config{DB: other}}
	reg := func(name string) error {
		return app.RegisterFlow(StateFlow{Name: name, TypeName: "T" + name, States: []State{{Name: "draft", IsInitial: true}}})
	}

	var firstErr, secondErr error
	out := capturedLog(func() {
		firstErr = reg("one")
		secondErr = reg("two")
	})
	if firstErr != nil || secondErr != nil {
		t.Fatalf("registration must still succeed: %v, %v", firstErr, secondErr)
	}
	if n := strings.Count(out, "state flows are enforced on SQLite only"); n != 1 {
		t.Errorf("the Warn was logged %d times for two flows, want once per process:\n%s", n, out)
	}
	for _, want := range []string{"level=WARN", "RequiredOperation gate", "NOT checked", "authorization gap", "State.Locked locks nothing", "ConflictPolicy is not applied"} {
		if !strings.Contains(out, want) {
			t.Errorf("the Warn must say %q:\n%s", want, out)
		}
	}
}

func TestRegisterFlow_NoWarnOnSQLite(t *testing.T) {
	db := newMigratedDB(t)
	nonSQLiteWarned.Delete(fmt.Sprintf("%T", db))
	app := &App{cfg: Config{DB: db}}
	out := capturedLog(func() {
		if err := app.RegisterFlow(StateFlow{Name: "q", TypeName: "QuietT", States: []State{{Name: "draft", IsInitial: true}}}); err != nil {
			t.Fatalf("RegisterFlow: %v", err)
		}
	})
	if strings.Contains(out, "enforced on SQLite only") {
		t.Errorf("SQLite must not warn:\n%s", out)
	}
}
