// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// saveBarrier holds Save calls until n are waiting, or until timeout: the same race
// window as beginBarrierDB, for the Module paths whose winner is saved through the
// module's own repository.
type saveBarrier struct {
	n       int
	timeout time.Duration
	mu      sync.Mutex
	waiting int
	release chan struct{}
}

func newSaveBarrier(n int, timeout time.Duration) *saveBarrier {
	return &saveBarrier{n: n, timeout: timeout, release: make(chan struct{})}
}

func (b *saveBarrier) wait() {
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
}

type barrierSaveRepo[T any] struct {
	Repository[T]
	b *saveBarrier
}

func (r barrierSaveRepo[T]) Save(ctx context.Context, item T) error {
	r.b.wait()
	return r.Repository.Save(ctx, item)
}

// TestModuleMCPPublish_Supersede_ConcurrentWinners: the Module path cannot use a
// transaction, so the lock is all that stands between two simultaneous winners and
// two active items.
func TestModuleMCPPublish_Supersede_ConcurrentWinners(t *testing.T) {
	_, db, store := edgeTestApp(t, "published",
		[]State{{Name: "pending", IsInitial: true}, {Name: "published"}, {Name: "superseded"}},
		[]Transition{{From: "pending", To: "published"}, {From: "published", To: "superseded"}})
	upsertTestKind(t, store, "supersedes", "Signal", "Signal")
	insertSignal(t, db, "old", "old-slug", "published")
	insertSignal(t, db, "new-a", "new-a-slug", "pending")
	insertSignal(t, db, "new-b", "new-b-slug", "pending")
	m := NewModule[*Signal]((*Signal)(nil), At("/signals"), MCP(MCPRead, MCPWrite),
		Repo(barrierSaveRepo[*Signal]{Repository: NewSQLRepo[*Signal](db, Table("smeldr_signals")), b: newSaveBarrier(2, 400*time.Millisecond)}))
	m.setDB(db)
	m.setRelationStore(store)

	start := time.Now()
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, slug := range []string{"new-a-slug", "new-b-slug"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = m.MCPPublish(NewTestContext(User{ID: "u-" + slug, Roles: []Role{Editor}}), slug, "")
		}()
	}
	wg.Wait()
	assertRaceFast(t, start)
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("both winners should succeed under supersede: %v, %v", errs[0], errs[1])
	}
	if n := countStatus(t, db, "published"); n != 1 {
		t.Errorf("%d items in the active state, want exactly 1", n)
	}
}

// TestDynamicSetStatus_Supersede_ConcurrentWinners is the same race on the
// runtime-defined type path.
func TestDynamicSetStatus_Supersede_ConcurrentWinners(t *testing.T) {
	app, db, _, _ := setupProvenanceTransitionApp(t)
	typeName, slugA := defineProvenanceDynamicType(t, app, db, "racedyn")
	repo, err := app.DynamicContentRepo(typeName)
	if err != nil {
		t.Fatalf("DynamicContentRepo: %v", err)
	}
	a, err := repo.GetBySlug(context.Background(), slugA)
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	b, err := repo.CreateDraft(context.Background(), map[string]any{"Title": "second"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if err := app.RegisterFlow(StateFlow{
		Name: "racedyn-flow", TypeName: typeName, ActiveState: "published", ConflictPolicy: ConflictSupersede,
		States:      []State{{Name: "draft", IsInitial: true}, {Name: "published"}, {Name: "superseded"}},
		Transitions: []Transition{{From: "draft", To: "published"}, {From: "published", To: "superseded"}},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	repo.db = newBeginBarrierDB(db, 2, 400*time.Millisecond)

	start := time.Now()
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, id := range []string{a.ID, b.ID} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = repo.setStatusVia(NewTestContext(User{ID: "u-" + id, Roles: []Role{Editor}}), id, Published, "", "mcp")
		}()
	}
	wg.Wait()
	assertRaceFast(t, start)
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("both winners should succeed under supersede: %v, %v", errs[0], errs[1])
	}
	var n int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM smeldr_dynamic_content WHERE type_name = $1 AND status = 'published'`, typeName).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("%d items in the active state, want exactly 1", n)
	}
}

func conflictLockTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db := newMigratedDB(t)
	createConflictItemTable(t, db, true, ConflictSupersede)
	insertConflictItem(t, db, "w", "draft")
	return db
}

// TestConflictLock_NotTakenWithoutAPolicy: a type with no policy, or a transition
// that is not into the active state, never touches the lock registry.
func TestConflictLock_NotTakenWithoutAPolicy(t *testing.T) {
	db := conflictLockTestDB(t)
	plan, err := planConflict(context.Background(), db, "ConflictType", "archived", "w")
	if err != nil || plan != nil {
		t.Errorf("a transition outside the active state = %+v, %v, want nil, nil", plan, err)
	}
	plan, err = planConflict(context.Background(), db, "NoPolicyAtAllType", "published", "w")
	if err != nil || plan != nil {
		t.Errorf("a type with no flow = %+v, %v, want nil, nil", plan, err)
	}
	if _, held := conflictLocks.Load("NoPolicyAtAllType"); held {
		t.Error("a lock was created for a type with no policy")
	}
}

// TestConflictLock_WaitsThenGoesAheadUnlocked: the bound makes the lock fail-open.
func TestConflictLock_WaitsThenGoesAheadUnlocked(t *testing.T) {
	db := conflictLockTestDB(t)
	prev := conflictLockWait
	conflictLockWait = 60 * time.Millisecond
	t.Cleanup(func() { conflictLockWait = prev })

	first, err := planConflict(context.Background(), db, "ConflictType", "published", "w")
	if err != nil || first == nil || !first.locked {
		t.Fatalf("first plan = %+v, %v, want one holding the lock", first, err)
	}
	var second *conflictPlan
	start := time.Now()
	out := capturedLog(func() {
		second, err = planConflict(context.Background(), db, "ConflictType", "published", "w")
	})
	if err != nil || second == nil {
		t.Fatalf("second plan = %+v, %v, want a plan that went ahead", second, err)
	}
	if time.Since(start) < 50*time.Millisecond {
		t.Errorf("the second plan did not wait for the lock (%v)", time.Since(start))
	}
	if second.locked {
		t.Error("the second plan claims to hold a lock it never got")
	}
	if !strings.Contains(out, "lock wait timed out") || !strings.Contains(out, "level=ERROR") {
		t.Errorf("the timeout must be logged at Error:\n%s", out)
	}
	// Releasing the unlocked plan must not free the first plan's lock: a third
	// waiter still has to wait out its own bound.
	second.release()
	waited := time.Now()
	third, err := planConflict(context.Background(), db, "ConflictType", "published", "w")
	if err != nil || third == nil || third.locked || time.Since(waited) < 50*time.Millisecond {
		t.Errorf("the lock was freed by an unlocked plan's release: %+v, %v, after %v", third, err, time.Since(waited))
	}
	third.release()
	first.release()
}

// TestConflictLock_CancelledWhileWaiting: a cancelled context is an error, and
// nothing is held.
func TestConflictLock_CancelledWhileWaiting(t *testing.T) {
	db := conflictLockTestDB(t)
	first, err := planConflict(context.Background(), db, "ConflictType", "published", "w")
	if err != nil || first == nil {
		t.Fatalf("first plan = %+v, %v", first, err)
	}
	defer first.release()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	plan, err := planConflict(ctx, db, "ConflictType", "published", "w")
	if !errors.Is(err, ErrInternal) || plan != nil {
		t.Errorf("a cancelled wait = %+v, %v, want nil and ErrInternal", plan, err)
	}
}

// TestConflictLock_ReleaseIsSafe: nil plan, plan without a lock, and releasing twice.
func TestConflictLock_ReleaseIsSafe(t *testing.T) {
	var nilPlan *conflictPlan
	nilPlan.release()
	(&conflictPlan{}).release()
	if got := nilPlan.holding(context.Background()); got == nil {
		t.Error("holding on a nil plan must return the context it was given")
	}

	db := conflictLockTestDB(t)
	plan, err := planConflict(context.Background(), db, "ConflictType", "published", "w")
	if err != nil || plan == nil {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	plan.release()
	plan.release()
	again, err := planConflict(context.Background(), db, "ConflictType", "published", "w")
	if err != nil || again == nil || !again.locked {
		t.Errorf("after a double release the lock must be free: %+v, %v", again, err)
	}
	again.release()
}

// TestConflictLock_ReentrantUnderTheSameContext: a nested transition of the same
// type under the context the plan handed on does not wait on its own caller, and
// releasing it does not free the caller's lock. The context keeps every Context
// method.
func TestConflictLock_ReentrantUnderTheSameContext(t *testing.T) {
	db := conflictLockTestDB(t)
	prev := conflictLockWait
	conflictLockWait = 3 * time.Second
	t.Cleanup(func() { conflictLockWait = prev })

	base := NewTestContext(User{ID: "u-reentrant", Roles: []Role{Editor}})
	outer, err := planConflict(base, db, "ConflictType", "published", "w")
	if err != nil || outer == nil || !outer.locked {
		t.Fatalf("outer plan = %+v, %v", outer, err)
	}
	held := outer.holdingContext(base)
	if held.User().ID != "u-reentrant" {
		t.Errorf("the held context lost its user: %q", held.User().ID)
	}
	if !conflictHeld(held, "ConflictType") || conflictHeld(held, "SomeOtherType") {
		t.Error("the held context must name exactly the type whose lock it holds")
	}

	start := time.Now()
	inner, err := planConflict(held, db, "ConflictType", "published", "w")
	if err != nil || inner == nil {
		t.Fatalf("inner plan = %+v, %v", inner, err)
	}
	if time.Since(start) > time.Second {
		t.Errorf("the nested plan waited %v on its own caller's lock", time.Since(start))
	}
	if inner.locked {
		t.Error("the nested plan must not claim to take the lock a second time")
	}
	inner.release()

	// The outer lock is still held: a fresh context must wait for it.
	conflictLockWait = 80 * time.Millisecond
	waited := time.Now()
	stranger, err := planConflict(context.Background(), db, "ConflictType", "published", "w")
	if err != nil || stranger == nil {
		t.Fatalf("stranger = %+v, %v", stranger, err)
	}
	if time.Since(waited) < 60*time.Millisecond || stranger.locked {
		t.Errorf("releasing the nested plan freed the outer lock (waited %v, locked %v)", time.Since(waited), stranger.locked)
	}
	stranger.release()
	outer.release()

	// A plain context.Context takes the WithValue branch.
	plain, err := planConflict(context.Background(), db, "ConflictType", "published", "w")
	if err != nil || plain == nil {
		t.Fatalf("plain plan = %+v, %v", plain, err)
	}
	if !conflictHeld(plain.holding(context.Background()), "ConflictType") {
		t.Error("a plain context must carry the held marker too")
	}
	plain.release()
}
