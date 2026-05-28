package smeldr

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// — NewBackgroundContext ———————————————————————————————————————————————————

// TestNewBackgroundContext verifies that NewBackgroundContext returns a fully
// initialised Context backed by context.Background().
func TestNewBackgroundContext(t *testing.T) {
	ctx := NewBackgroundContext("example.com")
	if ctx == nil {
		t.Fatal("NewBackgroundContext() returned nil")
	}
	if ctx.RequestID() == "" {
		t.Error("RequestID() should not be empty")
	}
	if ctx.Locale() != "en" {
		t.Errorf("Locale() = %q, want \"en\"", ctx.Locale())
	}
	if ctx.User().ID != "" {
		t.Errorf("User should be GuestUser, got %+v", ctx.User())
	}
	if ctx.Request() == nil {
		t.Error("Request() should not be nil")
	}
	if ctx.Response() == nil {
		t.Error("Response() should not be nil")
	}
	if ctx.SiteName() != "example.com" {
		t.Errorf("SiteName() = %q, want \"example.com\"", ctx.SiteName())
	}
	// Must satisfy context.Context — Done channel exists.
	select {
	case <-ctx.Done():
		t.Error("background context should not be cancelled")
	default:
	}
}

// — nextDur ———————————————————————————————————————————————————————————————

// TestNextDur covers the three branches: nil input, past time, future time.
func TestNextDur(t *testing.T) {
	t.Run("nil returns 60s", func(t *testing.T) {
		if got := nextDur(nil); got != 60*time.Second {
			t.Errorf("nextDur(nil) = %v, want 60s", got)
		}
	})

	t.Run("past time returns minimum 1ms", func(t *testing.T) {
		past := time.Now().UTC().Add(-5 * time.Second)
		if got := nextDur(&past); got < time.Millisecond {
			t.Errorf("nextDur(past) = %v, want >= 1ms", got)
		}
	})

	t.Run("future time returns positive duration", func(t *testing.T) {
		future := time.Now().UTC().Add(10 * time.Second)
		got := nextDur(&future)
		if got <= 0 {
			t.Errorf("nextDur(future) = %v, want > 0", got)
		}
		if got > 11*time.Second {
			t.Errorf("nextDur(future) = %v, want <= ~10s", got)
		}
	})
}

// — processScheduled ——————————————————————————————————————————————————————

// schedModule returns a Module[*testPost] with an in-memory repo and
// the provided extra options.
func schedModule(opts ...Option) (*Module[*testPost], *MemoryRepo[*testPost]) {
	repo := NewMemoryRepo[*testPost]()
	base := []Option{Repo(repo), At("/posts")}
	m := NewModule((*testPost)(nil), append(base, opts...)...)
	return m, repo
}

// TestProcessScheduled_publishesOverdue verifies that a testPost with
// ScheduledAt in the past is transitioned to Published and its ScheduledAt
// is cleared.
func TestProcessScheduled_publishesOverdue(t *testing.T) {
	m, repo := schedModule()
	ctx := NewBackgroundContext("example.com")

	past := time.Now().UTC().Add(-1 * time.Minute)
	p := &testPost{Node: Node{ID: NewID(), Slug: "overdue", Status: Scheduled, ScheduledAt: &past}}
	if err := repo.Save(context.Background(), p); err != nil {
		t.Fatalf("Save: %v", err)
	}

	now := time.Now().UTC()
	published, next, err := m.processScheduled(ctx, now)
	if err != nil {
		t.Fatalf("processScheduled error: %v", err)
	}
	if published != 1 {
		t.Errorf("published = %d, want 1", published)
	}
	if next != nil {
		t.Errorf("next = %v, want nil (no remaining scheduled items)", next)
	}

	// Verify persisted state via repo.
	got, err := repo.FindByID(context.Background(), p.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.Status != Published {
		t.Errorf("Status = %v, want Published", got.Status)
	}
	if got.ScheduledAt != nil {
		t.Errorf("ScheduledAt = %v, want nil", got.ScheduledAt)
	}
	if got.PublishedAt.IsZero() {
		t.Error("PublishedAt should be set after publishing")
	}
}

// TestProcessScheduled_skipsNotYetDue verifies that a testPost with ScheduledAt
// in the future remains Scheduled and is returned as the next candidate.
func TestProcessScheduled_skipsNotYetDue(t *testing.T) {
	m, repo := schedModule()
	ctx := NewBackgroundContext("example.com")

	future := time.Now().UTC().Add(10 * time.Minute)
	p := &testPost{Node: Node{ID: NewID(), Slug: "future", Status: Scheduled, ScheduledAt: &future}}
	if err := repo.Save(context.Background(), p); err != nil {
		t.Fatalf("Save: %v", err)
	}

	published, next, err := m.processScheduled(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("processScheduled error: %v", err)
	}
	if published != 0 {
		t.Errorf("published = %d, want 0", published)
	}
	if next == nil {
		t.Fatal("next should not be nil for a future-scheduled item")
	}
	if !next.Equal(future) {
		t.Errorf("next = %v, want %v", *next, future)
	}

	// Item should still be Scheduled in the repo.
	got, _ := repo.FindByID(context.Background(), p.ID)
	if got.Status != Scheduled {
		t.Errorf("Status = %v, want Scheduled", got.Status)
	}
}

// TestProcessScheduled_firesAfterPublish verifies that the AfterPublish signal
// fires for each item transitioned to Published.
func TestProcessScheduled_firesAfterPublish(t *testing.T) {
	var fired atomic.Int32

	handler := func(_ Context, _ *testPost) error {
		fired.Add(1)
		return nil
	}
	m, repo := schedModule(On(AfterPublish, handler))
	ctx := NewBackgroundContext("example.com")

	past := time.Now().UTC().Add(-30 * time.Second)
	for i := range 3 {
		p := &testPost{Node: Node{
			ID:          NewID(),
			Slug:        "item-" + string(rune('a'+i)),
			Status:      Scheduled,
			ScheduledAt: &past,
		}}
		if err := repo.Save(context.Background(), p); err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}

	published, _, err := m.processScheduled(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("processScheduled error: %v", err)
	}
	if published != 3 {
		t.Errorf("published = %d, want 3", published)
	}

	// dispatchAfter is asynchronous — wait up to 500 ms.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if fired.Load() == 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := fired.Load(); got != 3 {
		t.Errorf("AfterPublish fired %d times, want 3", got)
	}
}

// TestProcessScheduled_mixedItems verifies that among overdue and future items
// only the overdue ones are published and the soonest future time is returned.
func TestProcessScheduled_mixedItems(t *testing.T) {
	m, repo := schedModule()
	ctx := NewBackgroundContext("example.com")

	past := time.Now().UTC().Add(-1 * time.Minute)
	sooner := time.Now().UTC().Add(5 * time.Minute)
	later := time.Now().UTC().Add(10 * time.Minute)

	items := []*testPost{
		{Node: Node{ID: NewID(), Slug: "past", Status: Scheduled, ScheduledAt: &past}},
		{Node: Node{ID: NewID(), Slug: "sooner", Status: Scheduled, ScheduledAt: &sooner}},
		{Node: Node{ID: NewID(), Slug: "later", Status: Scheduled, ScheduledAt: &later}},
	}
	for _, item := range items {
		if err := repo.Save(context.Background(), item); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	published, next, err := m.processScheduled(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("processScheduled error: %v", err)
	}
	if published != 1 {
		t.Errorf("published = %d, want 1", published)
	}
	if next == nil {
		t.Fatal("next should not be nil")
	}
	if !next.Equal(sooner) {
		t.Errorf("next = %v, want soonest future = %v", *next, sooner)
	}
}

// — Scheduler lifecycle ———————————————————————————————————————————————————

// TestScheduler_startStop verifies that a Scheduler goroutine exits cleanly
// when its context is cancelled, and Wait() returns promptly.
func TestScheduler_startStop(t *testing.T) {
	// No modules — tick is a no-op but the goroutine must still exit cleanly.
	bgCtx := NewBackgroundContext("example.com")
	sched := newScheduler(nil, bgCtx)

	schedCtx, cancel := context.WithCancel(context.Background())
	sched.Start(schedCtx)

	cancel()

	done := make(chan struct{})
	go func() {
		sched.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Scheduler.Wait() did not return within 2s after cancel")
	}
}
