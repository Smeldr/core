package smeldr

import (
	"context"
	"time"
)

// schedulableModule is the unexported interface satisfied by every [Module[T]].
// The [Scheduler] calls processScheduled on each registered module once per tick.
type schedulableModule interface {
	processScheduled(ctx Context, now time.Time) (published int, next *time.Time, err error)
}

// Scheduler drives the Scheduled→Published transition for all content modules
// registered with [App.Content]. A single background goroutine runs with an
// adaptive timer: after each tick the timer is reset to fire at the soonest
// remaining ScheduledAt across all modules, falling back to 60 seconds when no
// scheduled items exist.
//
// The Scheduler is created and started by [App.Run] and stopped as part of
// graceful shutdown. Applications do not create Schedulers directly.
type Scheduler struct {
	modules []schedulableModule
	bgCtx   Context
	done    chan struct{}
}

// newScheduler returns a Scheduler that will process scheduled items for each
// of the provided modules, using bgCtx for storage calls and signal dispatch.
func newScheduler(modules []schedulableModule, bgCtx Context) *Scheduler {
	return &Scheduler{
		modules: modules,
		bgCtx:   bgCtx,
		done:    make(chan struct{}),
	}
}

// Start spawns the scheduler goroutine. The goroutine exits when ctx is
// cancelled. Call [Scheduler.Wait] after cancellation to block until the
// goroutine has fully exited.
func (s *Scheduler) Start(ctx context.Context) {
	go s.run(ctx)
}

// Wait blocks until the goroutine started by [Scheduler.Start] has exited.
// It should be called after cancelling the context passed to Start to ensure
// clean shutdown.
func (s *Scheduler) Wait() {
	<-s.done
}

// tick processes all overdue Scheduled items across every registered module
// and returns the soonest remaining ScheduledAt across all modules (nil if no
// scheduled items remain after the pass).
func (s *Scheduler) tick() *time.Time {
	now := time.Now().UTC()
	var next *time.Time
	for _, m := range s.modules {
		_, n, _ := m.processScheduled(s.bgCtx, now)
		// Transient storage errors are logged by processScheduled's caller
		// (the module); the scheduler continues running.
		if n != nil && (next == nil || n.Before(*next)) {
			next = n
		}
	}
	return next
}

// run is the body of the scheduler goroutine.
func (s *Scheduler) run(ctx context.Context) {
	defer close(s.done)

	// Process any items that became due while the server was offline.
	next := s.tick()

	timer := time.NewTimer(nextDur(next))
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			next = s.tick()
			timer.Reset(nextDur(next))
		}
	}
}

// nextDur returns the duration to wait before the next scheduler tick.
// When next is nil (no scheduled items remain) it returns 60 seconds.
// The returned duration is never less than 1 millisecond.
func nextDur(next *time.Time) time.Duration {
	if next == nil {
		return 60 * time.Second
	}
	d := time.Until(*next)
	if d < time.Millisecond {
		return time.Millisecond
	}
	return d
}
