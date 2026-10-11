package smeldr

import (
	"sync/atomic"
	"testing"
	"time"
)

// A pending call runs once, synchronously, inside flush, and not again when
// its delay would have elapsed.
func TestDebouncer_FlushRunsPendingOnce(t *testing.T) {
	var count atomic.Int32
	d := newDebouncer(30*time.Millisecond, func() { count.Add(1) })
	d.Trigger()
	d.flush()
	if got := count.Load(); got != 1 {
		t.Fatalf("after flush: %d calls, want 1", got)
	}
	time.Sleep(60 * time.Millisecond)
	if got := count.Load(); got != 1 {
		t.Errorf("after the delay: %d calls, want still 1", got)
	}
	d.flush() // a second flush finds nothing and returns
	if got := count.Load(); got != 1 {
		t.Errorf("after a second flush: %d calls, want 1", got)
	}
}

// With nothing pending, flush runs nothing.
func TestDebouncer_FlushNothingPending(t *testing.T) {
	var count atomic.Int32
	d := newDebouncer(time.Hour, func() { count.Add(1) })
	d.flush()
	if got := count.Load(); got != 0 {
		t.Errorf("%d calls, want 0", got)
	}
}

// A call that already fired is waited for: flush returns only after it ends.
func TestDebouncer_FlushWaitsForFiring(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var finished atomic.Bool
	d := newDebouncer(time.Millisecond, func() {
		close(started)
		<-release
		finished.Store(true)
	})
	d.Trigger()
	<-started

	flushed := make(chan struct{})
	go func() {
		d.flush()
		close(flushed)
	}()
	select {
	case <-flushed:
		t.Fatal("flush returned while fn was still running")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	select {
	case <-flushed:
	case <-time.After(2 * time.Second):
		t.Fatal("flush did not return after fn ended")
	}
	if !finished.Load() {
		t.Error("flush returned before fn finished")
	}
}

// After flush, Trigger schedules nothing.
func TestDebouncer_TriggerAfterFlushIsNoop(t *testing.T) {
	var count atomic.Int32
	d := newDebouncer(5*time.Millisecond, func() { count.Add(1) })
	d.flush()
	d.Trigger()
	time.Sleep(30 * time.Millisecond)
	if got := count.Load(); got != 0 {
		t.Errorf("%d calls after Trigger on a flushed debouncer, want 0", got)
	}
}

// fn calling Trigger on its own debouncer during flush is a no-op, not a
// deadlock (flush runs fn with the lock released).
func TestDebouncer_TriggerFromFnDuringFlush(t *testing.T) {
	var count atomic.Int32
	var d *debouncer
	d = newDebouncer(time.Hour, func() {
		count.Add(1)
		d.Trigger()
	})
	d.Trigger()
	done := make(chan struct{})
	go func() {
		d.flush()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("flush deadlocked when fn called Trigger")
	}
	if got := count.Load(); got != 1 {
		t.Errorf("%d calls, want 1", got)
	}
}

// Stop still cancels without running, and keeps the pending count exact, so
// a later flush neither runs the cancelled call nor blocks.
func TestDebouncer_StopThenFlush(t *testing.T) {
	var count atomic.Int32
	d := newDebouncer(time.Hour, func() { count.Add(1) })
	d.Trigger()
	d.Trigger() // replaces the first timer
	d.Stop()
	done := make(chan struct{})
	go func() {
		d.flush()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("flush blocked after Stop")
	}
	if got := count.Load(); got != 0 {
		t.Errorf("%d calls, want 0 (Stop cancels)", got)
	}
}

// Module.Stop flushes the module's rebuild debounce: a rebuild triggered just
// before Stop has run by the time Stop returns, exactly once.
func TestModule_StopFlushesPendingRebuild(t *testing.T) {
	m := newTestModule(NewMemoryRepo[*testPost]())
	var count atomic.Int32
	m.debounce = newDebouncer(time.Hour, func() { count.Add(1) })
	m.triggerRebuild()
	m.Stop()
	if got := count.Load(); got != 1 {
		t.Fatalf("after Stop: %d rebuilds, want 1", got)
	}
	m.Stop()
	if got := count.Load(); got != 1 {
		t.Errorf("after a second Stop: %d rebuilds, want 1", got)
	}
}
