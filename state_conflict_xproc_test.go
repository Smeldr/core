// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingLocker is a handle that provides the cross-process lock and records how
// it is used. inProcessHeld reports, at each call, whether the in-process lock of
// the type was held: it must be when the cross-process lock is taken and when it
// is given up.
type recordingLocker struct {
	DB
	typeName string
	mu       sync.Mutex
	events   []string
	err      error
	wait     bool // block until ctx ends, then return its error
}

func (l *recordingLocker) inProcessHeld() bool {
	v, ok := conflictLocks.Load(l.typeName)
	return ok && len(v.(chan struct{})) == 1
}

func (l *recordingLocker) record(e string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
}

func (l *recordingLocker) AcquireLock(ctx context.Context, name string) (func(), error) {
	l.record("acquire " + name + " inproc=" + boolStr(l.inProcessHeld()))
	if l.wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if l.err != nil {
		return nil, l.err
	}
	return func() { l.record("release inproc=" + boolStr(l.inProcessHeld())) }, nil
}

func (l *recordingLocker) got() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

func boolStr(b bool) string {
	if b {
		return "held"
	}
	return "free"
}

func xprocSetup(t *testing.T, policy ConflictPolicy) *recordingLocker {
	t.Helper()
	db := newMigratedDB(t)
	registerConflictFlow(t, db, policy)
	insertConflictItem(t, db, "a", "published")
	insertConflictItem(t, db, "b", "draft")
	return &recordingLocker{DB: db, typeName: "ConflictType"}
}

// TestConflictLock_CrossProcessLockIsTakenAfterTheInProcessOne: the handle is asked
// once per plan, with the stable name, while the in-process lock is held, and the
// lock is given up before the in-process one, once, however often the plan is
// released.
func TestConflictLock_CrossProcessLockIsTakenAfterTheInProcessOne(t *testing.T) {
	l := xprocSetup(t, ConflictSupersede)
	plan, err := planConflict(context.Background(), l, "ConflictType", "published", "b")
	if err != nil || plan == nil {
		t.Fatalf("planConflict = %v, %v", plan, err)
	}
	if got := l.got(); len(got) != 1 || got[0] != "acquire smeldr:conflict:ConflictType inproc=held" {
		t.Fatalf("events after the plan = %v, want one acquire with the in-process lock held", got)
	}
	plan.release()
	plan.release()
	want := []string{"acquire smeldr:conflict:ConflictType inproc=held", "release inproc=held"}
	if got := l.got(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("events = %v, want %v (released once, before the in-process lock)", got, want)
	}
	if l.inProcessHeld() {
		t.Error("the in-process lock is still held after release")
	}
}

func TestConflictLock_CrossProcessLockNotAskedWithoutAPolicyOrWhenReentrant(t *testing.T) {
	l := xprocSetup(t, ConflictSupersede)
	ctx := context.Background()
	// A type with no policy to apply never asks.
	if plan, err := planConflict(ctx, l, "ConflictType", "draft", "b"); plan != nil || err != nil {
		t.Fatalf("a transition that is not into the active state = %v, %v, want nil, nil", plan, err)
	}
	if got := l.got(); len(got) != 0 {
		t.Errorf("asked %v without a policy to apply", got)
	}
	// A nested transition of the same type under a held plan does not ask again.
	plan, err := planConflict(ctx, l, "ConflictType", "published", "b")
	if err != nil || plan == nil {
		t.Fatalf("planConflict = %v, %v", plan, err)
	}
	defer plan.release()
	release, held, err := acquireConflictLock(plan.holding(ctx), l, "ConflictType")
	if err != nil || held {
		t.Fatalf("nested acquire: held=%v err=%v, want not held and no error", held, err)
	}
	release()
	if got := l.got(); len(got) != 1 {
		t.Errorf("events = %v, want a single acquire for the whole call chain", got)
	}
}

func TestConflictLock_CrossProcessLockFailsOpen(t *testing.T) {
	prev := conflictLockWait
	conflictLockWait = 60 * time.Millisecond
	t.Cleanup(func() { conflictLockWait = prev })

	for name, l := range map[string]*recordingLocker{
		"the handle cannot give it": {err: errors.New("connection reset")},
		"the wait is bounded":       {wait: true},
	} {
		t.Run(name, func(t *testing.T) {
			base := xprocSetup(t, ConflictSupersede)
			l.DB, l.typeName = base.DB, "ConflictType"
			var plan *conflictPlan
			var err error
			out := capturedLog(func() { plan, err = planConflict(context.Background(), l, "ConflictType", "published", "b") })
			if err != nil || plan == nil || !plan.locked {
				t.Fatalf("plan = %v, locked = %v, err = %v, want the transition to go ahead holding the in-process lock", plan, plan != nil && plan.locked, err)
			}
			for _, want := range []string{"level=ERROR", "cross-process lock not taken", "type=ConflictType", "lock=smeldr:conflict:ConflictType", "waited="} {
				if !strings.Contains(out, want) {
					t.Errorf("the Error line must say %q:\n%s", want, out)
				}
			}
			plan.release()
			if l.inProcessHeld() {
				t.Error("the in-process lock was not released")
			}
		})
	}
}

func TestConflictLock_CrossProcessWaitEndedByTheCaller(t *testing.T) {
	l := xprocSetup(t, ConflictSupersede)
	l.wait = true
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(40 * time.Millisecond); cancel() }()
	plan, err := planConflict(ctx, l, "ConflictType", "published", "b")
	if !errors.Is(err, ErrInternal) || plan != nil {
		t.Fatalf("plan = %v, err = %v, want nil and ErrInternal (nothing may be written for a caller that gave up)", plan, err)
	}
	if l.inProcessHeld() {
		t.Error("the in-process lock was not released after the caller gave up")
	}
}

func TestConflictLock_HandleWithoutTheCapabilityIsUnchanged(t *testing.T) {
	db := newMigratedDB(t)
	registerConflictFlow(t, db, ConflictSupersede)
	plan, err := planConflict(context.Background(), db, "ConflictType", "published", "b")
	if err != nil || plan == nil || !plan.locked {
		t.Fatalf("plan = %v, err = %v, want a locked plan", plan, err)
	}
	plan.release()
}
