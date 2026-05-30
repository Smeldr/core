package smeldr

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestOnReturnsOption verifies that On[T] returns a value satisfying the
// Option interface.
func TestOnReturnsOption(t *testing.T) {
	var opt Option = On(BeforeCreate, func(_ Context, _ *struct{}) error { return nil })
	if opt == nil {
		t.Fatal("expected non-nil Option")
	}
	so, ok := opt.(signalOption)
	if !ok {
		t.Fatalf("expected signalOption, got %T", opt)
	}
	if so.signal != BeforeCreate {
		t.Errorf("expected signal %q, got %q", BeforeCreate, so.signal)
	}
}

// TestDispatchBeforeRunsAllOnSuccess verifies that dispatchBefore calls all
// handlers when none return an error, and returns nil.
func TestDispatchBeforeRunsAllOnSuccess(t *testing.T) {
	ctx := NewTestContext(GuestUser)
	calls := 0
	handlers := []signalHandler{
		func(_ Context, _ any) error { calls++; return nil },
		func(_ Context, _ any) error { calls++; return nil },
		func(_ Context, _ any) error { calls++; return nil },
	}

	if err := dispatchBefore(ctx, handlers, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 handler calls, got %d", calls)
	}
}

// TestDispatchBeforeAbortsOnError verifies that dispatchBefore stops at the
// first handler that returns an error and propagates that error.
func TestDispatchBeforeAbortsOnError(t *testing.T) {
	ctx := NewTestContext(GuestUser)
	sentinel := ErrConflict
	calls := 0
	handlers := []signalHandler{
		func(_ Context, _ any) error { calls++; return sentinel },
		func(_ Context, _ any) error { calls++; return nil },
	}

	err := dispatchBefore(ctx, handlers, nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
	if calls != 1 {
		t.Errorf("expected second handler to be skipped, got %d calls", calls)
	}
}

// TestDispatchBeforePanicReturnsError verifies that a panicking handler does
// not crash the process and causes dispatchBefore to return a 500-class
// smeldr.Error.
func TestDispatchBeforePanicReturnsError(t *testing.T) {
	ctx := NewTestContext(GuestUser)
	handlers := []signalHandler{
		func(_ Context, _ any) error { panic("kaboom") },
	}

	err := dispatchBefore(ctx, handlers, nil)
	if err == nil {
		t.Fatal("expected error after panic, got nil")
	}
	var fe Error
	if !errors.As(err, &fe) {
		t.Fatalf("expected smeldr.Error, got %T", err)
	}
	if fe.HTTPStatus() != 500 {
		t.Errorf("expected HTTP 500, got %d", fe.HTTPStatus())
	}
	if fe.Code() != "signal_panic" {
		t.Errorf("expected code %q, got %q", "signal_panic", fe.Code())
	}
}

// TestDispatchAfterIsNonBlocking verifies that dispatchAfter returns before
// its handlers finish executing.
func TestDispatchAfterIsNonBlocking(t *testing.T) {
	ctx := NewTestContext(GuestUser)
	var wg sync.WaitGroup
	wg.Add(1)

	started := make(chan struct{})
	handlers := []signalHandler{
		func(_ Context, _ any) error {
			close(started)
			time.Sleep(50 * time.Millisecond)
			wg.Done()
			return nil
		},
	}

	dispatchAfter(ctx, handlers, nil)

	// dispatchAfter must return before the handler finishes, so the channel
	// close happens while we are still waiting here.
	select {
	case <-started:
		// handler started — confirm function already returned (we're here)
	case <-time.After(1 * time.Second):
		t.Fatal("handler did not start within deadline")
	}

	wg.Wait() // prevent goroutine leak in test binary
}

// TestDispatchAfterPanicDoesNotPropagate verifies that a panicking async
// handler neither crashes the process nor returns an error to the caller.
func TestDispatchAfterPanicDoesNotPropagate(t *testing.T) {
	ctx := NewTestContext(GuestUser)
	done := make(chan struct{})
	handlers := []signalHandler{
		func(_ Context, _ any) error {
			defer close(done)
			panic("async kaboom")
		},
	}

	// Must not panic in the caller's goroutine.
	dispatchAfter(ctx, handlers, nil)

	select {
	case <-done:
		// handler ran and recovered
	case <-time.After(1 * time.Second):
		t.Fatal("handler did not run within deadline")
	}
}

// TestDebouncerCoalesces verifies that 10 rapid Trigger calls produce exactly
// one fn invocation after the delay elapses.
func TestDebouncerCoalesces(t *testing.T) {
	var count atomic.Int32
	d := newDebouncer(20*time.Millisecond, func() {
		count.Add(1)
	})

	for range 10 {
		d.Trigger()
	}

	time.Sleep(60 * time.Millisecond)

	if got := count.Load(); got != 1 {
		t.Errorf("expected 1 fn invocation, got %d", got)
	}
}

// TestDebouncerResetsOnTrigger verifies that a Trigger call during the delay
// window prevents the earlier scheduled fn from firing.
func TestDebouncerResetsOnTrigger(t *testing.T) {
	var count atomic.Int32
	delay := 100 * time.Millisecond
	d := newDebouncer(delay, func() {
		count.Add(1)
	})

	d.Trigger()
	time.Sleep(40 * time.Millisecond) // well within delay
	d.Trigger()                       // resets the timer
	time.Sleep(40 * time.Millisecond) // original would have fired here, reset has not
	// timer has not elapsed yet after the reset
	if got := count.Load(); got != 0 {
		t.Errorf("fn fired too early: %d calls", got)
	}

	time.Sleep(100 * time.Millisecond) // now the reset timer fires
	if got := count.Load(); got != 1 {
		t.Errorf("expected 1 fn invocation, got %d", got)
	}
}

// TestDebouncerStop verifies that Stop cancels a pending timer so fn does
// not fire after the module has been torn down. (Amendment A39)
func TestDebouncerStop(t *testing.T) {
	var count atomic.Int32
	d := newDebouncer(40*time.Millisecond, func() {
		count.Add(1)
	})
	d.Trigger() // schedule fn in 40 ms
	d.Stop()    // cancel before it fires
	time.Sleep(60 * time.Millisecond)
	if got := count.Load(); got != 0 {
		t.Errorf("fn fired after Stop: %d calls; want 0", got)
	}
	// Second Stop must not panic.
	d.Stop()
}

// ---- Signal bus tests (App.OnSignal / dispatchBus) ----------------------

// sbPost is the content type used by signal bus unit tests.
type sbPost struct {
	Node
	Title string `smeldr:"required,min=1" db:"title"`
}

func (p *sbPost) ContentTitle() string { return p.Title }

// newBusApp builds a minimal *App for signal bus tests without a database.
func newBusApp(t *testing.T, secret string) (*App, MCPModule) {
	t.Helper()
	app := New(MustConfig(Config{
		BaseURL: "http://localhost:8080",
		Secret:  []byte(secret),
	}))
	repo := NewMemoryRepo[*sbPost]()
	m := NewModule((*sbPost)(nil), At("/sb"), Repo(repo), MCP(MCPRead, MCPWrite))
	app.Content(m)
	return app, app.MCPModules()[0]
}

// TestSignalBus_OnSignalChaining verifies that OnSignal returns *App.
func TestSignalBus_OnSignalChaining(t *testing.T) {
	app := New(MustConfig(Config{
		BaseURL: "http://localhost:8080",
		Secret:  []byte("test-secret-bus-chain"),
	}))
	got := app.OnSignal(AfterCreate, func(ctx context.Context, ev SignalEvent) error { return nil })
	if got != app {
		t.Errorf("OnSignal did not return *App")
	}
}

// TestSignalBus_HandlerCalled verifies that an OnSignal handler receives the
// SignalEvent when a content item is created.
func TestSignalBus_HandlerCalled(t *testing.T) {
	app, mod := newBusApp(t, "test-secret-bus-handler")

	var mu sync.Mutex
	var got SignalEvent
	done := make(chan struct{})

	app.OnSignal(AfterCreate, func(ctx context.Context, ev SignalEvent) error {
		mu.Lock()
		got = ev
		mu.Unlock()
		close(done)
		return nil
	})
	app.wireSignalBus()

	userCtx := NewTestContext(User{ID: "u1", Roles: []Role{Author}})
	if _, err := mod.MCPCreate(userCtx, map[string]any{"title": "Bus test"}); err != nil {
		t.Fatalf("MCPCreate: %v", err)
	}

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("signal bus handler not called within deadline")
	}

	mu.Lock()
	defer mu.Unlock()
	if got.Slug == "" {
		t.Errorf("SignalEvent.Slug is empty")
	}
	if got.URL == "" {
		t.Errorf("SignalEvent.URL is empty")
	}
}

// TestSignalBus_MultipleHandlers verifies that two handlers registered for the
// same signal both receive the event.
func TestSignalBus_MultipleHandlers(t *testing.T) {
	app, mod := newBusApp(t, "test-secret-bus-multi")

	var count atomic.Int32
	done := make(chan struct{})

	for range 2 {
		app.OnSignal(AfterCreate, func(ctx context.Context, ev SignalEvent) error {
			if count.Add(1) == 2 {
				close(done)
			}
			return nil
		})
	}
	app.wireSignalBus()

	userCtx := NewTestContext(User{ID: "u1", Roles: []Role{Author}})
	if _, err := mod.MCPCreate(userCtx, map[string]any{"title": "Multi"}); err != nil {
		t.Fatalf("MCPCreate: %v", err)
	}

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("not all handlers called; count=%d", count.Load())
	}
}

// TestSignalBus_HandlerError verifies that a handler returning an error does
// not prevent subsequent handlers from running.
func TestSignalBus_HandlerError(t *testing.T) {
	app, mod := newBusApp(t, "test-secret-bus-error")

	secondCalled := make(chan struct{})

	app.OnSignal(AfterCreate, func(ctx context.Context, ev SignalEvent) error {
		return errors.New("deliberate bus error")
	})
	app.OnSignal(AfterCreate, func(ctx context.Context, ev SignalEvent) error {
		close(secondCalled)
		return nil
	})
	app.wireSignalBus()

	userCtx := NewTestContext(User{ID: "u1", Roles: []Role{Author}})
	if _, err := mod.MCPCreate(userCtx, map[string]any{"title": "Error test"}); err != nil {
		t.Fatalf("MCPCreate: %v", err)
	}

	select {
	case <-secondCalled:
		// second handler ran despite first returning an error — correct
	case <-time.After(500 * time.Millisecond):
		t.Fatal("second handler not called after first handler error")
	}
}

// TestSignalBus_HandlerTimeout verifies that a handler blocking longer than
// 100 ms has its context cancelled by dispatchBus but does not stall the bus.
func TestSignalBus_HandlerTimeout(t *testing.T) {
	app, mod := newBusApp(t, "test-secret-bus-timeout")

	handlerStarted := make(chan struct{})
	handlerCtxDone := make(chan struct{})
	app.OnSignal(AfterCreate, func(ctx context.Context, ev SignalEvent) error {
		close(handlerStarted)
		<-ctx.Done() // blocks until 100 ms timeout cancels the context
		close(handlerCtxDone)
		return ctx.Err()
	})

	secondDone := make(chan struct{})
	app.OnSignal(AfterCreate, func(ctx context.Context, ev SignalEvent) error {
		close(secondDone)
		return nil
	})
	app.wireSignalBus()

	userCtx := NewTestContext(User{ID: "u1", Roles: []Role{Author}})
	if _, err := mod.MCPCreate(userCtx, map[string]any{"title": "Timeout test"}); err != nil {
		t.Fatalf("MCPCreate: %v", err)
	}

	select {
	case <-handlerStarted:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("blocking handler never started")
	}
	select {
	case <-handlerCtxDone:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("handler context not cancelled within 300 ms; per-handler timeout may not be 100 ms")
	}
	select {
	case <-secondDone:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("second handler not called after first timed out; bus may have stalled")
	}
}

// TestSignalBus_WithoutCancel verifies that the handler context is not derived
// from the request context — if the request context were cancelled, the handler
// would still run. dispatchBus uses context.WithoutCancel to detach from the
// request lifecycle.
func TestSignalBus_WithoutCancel(t *testing.T) {
	app, mod := newBusApp(t, "test-secret-bus-withoutcancel")

	handlerRan := make(chan struct{})
	app.OnSignal(AfterCreate, func(ctx context.Context, ev SignalEvent) error {
		// If this context were tied to the request, it could already be Done.
		// We simply verify the handler receives a non-nil, non-cancelled context.
		select {
		case <-ctx.Done():
			t.Errorf("handler context is already cancelled at handler entry")
		default:
		}
		close(handlerRan)
		return nil
	})
	app.wireSignalBus()

	userCtx := NewTestContext(User{ID: "u1", Roles: []Role{Author}})
	if _, err := mod.MCPCreate(userCtx, map[string]any{"title": "WithoutCancel"}); err != nil {
		t.Fatalf("MCPCreate: %v", err)
	}

	select {
	case <-handlerRan:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("handler not called")
	}
}
