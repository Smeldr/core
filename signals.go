package smeldr

import (
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Signal identifies a lifecycle event fired by a content module.
// Handlers are registered with [On] and receive the content value as their
// concrete type T — no type assertion required.
type Signal string

// Lifecycle signals fired by content modules.
const (
	// BeforeCreate fires before a new content item is persisted.
	// Return an error to abort the operation.
	BeforeCreate Signal = "before_create"

	// AfterCreate fires after a new content item has been persisted.
	// Runs asynchronously — errors and panics are logged, never returned.
	AfterCreate Signal = "after_create"

	// BeforeUpdate fires before an existing content item is updated.
	// Return an error to abort the operation.
	BeforeUpdate Signal = "before_update"

	// AfterUpdate fires after a content item has been updated.
	// Runs asynchronously — errors and panics are logged, never returned.
	AfterUpdate Signal = "after_update"

	// BeforeDelete fires before a content item is deleted.
	// Return an error to abort the operation.
	BeforeDelete Signal = "before_delete"

	// AfterDelete fires after a content item has been deleted.
	// Runs asynchronously — errors and panics are logged, never returned.
	AfterDelete Signal = "after_delete"

	// AfterPublish fires after a content item transitions to Published.
	// Runs asynchronously — triggers sitemap and feed regeneration.
	AfterPublish Signal = "after_publish"

	// AfterUnpublish fires after a content item is moved out of Published status.
	// Runs asynchronously — triggers sitemap and feed regeneration.
	AfterUnpublish Signal = "after_unpublish"

	// AfterArchive fires after a content item transitions to Archived.
	// Runs asynchronously — triggers sitemap and feed regeneration.
	AfterArchive Signal = "after_archive"

	// AfterSchedule fires after a content item transitions to Scheduled status.
	// It fires in addition to AfterUpdate — not instead of it. Runs
	// asynchronously — errors and panics are logged, never returned.
	AfterSchedule Signal = "after_schedule"

	// SitemapRegenerate is fired internally after AfterPublish, AfterUnpublish,
	// AfterArchive, and AfterDelete. It is debounced to coalesce burst changes
	// into a single sitemap and feed rebuild.
	SitemapRegenerate Signal = "sitemap_regenerate"
)

// signalHandler is the internal, type-erased handler signature used by
// dispatchBefore and dispatchAfter. It is never exposed to callers —
// use [On] to register typed handlers.
type signalHandler func(Context, any) error

// signalOption is the [Option] value returned by [On]. It carries a single
// signal name and one type-erased handler. Module wiring (module.go)
// accumulates these options into per-signal handler slices.
type signalOption struct {
	signal  Signal
	handler signalHandler
}

// isOption marks signalOption as a valid [Option] value.
func (signalOption) isOption() {}

// On registers a typed signal handler as a module [Option]. The handler
// receives the content value as its concrete type T — no type assertion
// required at the call site.
//
// Example:
//
//	smeldr.On(smeldr.BeforeCreate, func(ctx smeldr.Context, p *Post) error {
//	    p.Author = ctx.User().Name
//	    return nil
//	})
func On[T any](signal Signal, h func(Context, T) error) Option {
	return signalOption{
		signal: signal,
		handler: func(ctx Context, payload any) error {
			return h(ctx, payload.(T))
		},
	}
}

// errSignalPanic is returned by dispatchBefore when a signal handler panics.
var errSignalPanic = newSentinel(500, "signal_panic", "Internal server error")

// dispatchBefore runs handlers synchronously in registration order.
// The first non-nil error aborts iteration and is returned to the caller.
// A panicking handler is recovered, logged, and causes a 500-class
// [smeldr.Error] to be returned — it does not crash the process.
func dispatchBefore(ctx Context, handlers []signalHandler, payload any) error {
	for _, h := range handlers {
		if err := safeCall(ctx, h, payload); err != nil {
			return err
		}
	}
	return nil
}

// dispatchAfter runs all handlers in a single goroutine, asynchronously.
// Errors are logged. Panics are recovered and logged. Nothing is returned
// to the caller — the request has already completed.
func dispatchAfter(ctx Context, handlers []signalHandler, payload any) {
	go func() {
		for _, h := range handlers {
			if err := safeCall(ctx, h, payload); err != nil {
				slog.ErrorContext(ctx, "signal handler error",
					"error", err,
				)
			}
		}
	}()
}

// safeCall invokes a single signalHandler, recovering from any panic.
// On panic it logs the recovered value and returns errSignalPanic.
func safeCall(ctx Context, h signalHandler, payload any) (err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.ErrorContext(ctx, "signal handler panic", "panic", r)
			err = errSignalPanic
		}
	}()
	return h(ctx, payload)
}

// debouncer delays invocation of fn until no further [debouncer.Trigger]
// calls have arrived within the configured delay. Rapid bursts of calls
// collapse into a single fn invocation.
type debouncer struct {
	mu    sync.Mutex
	timer *time.Timer
	delay time.Duration
	fn    func()
}

// newDebouncer returns a debouncer that calls fn after delay elapses
// without any further Trigger calls.
func newDebouncer(delay time.Duration, fn func()) *debouncer {
	return &debouncer{delay: delay, fn: fn}
}

// Trigger resets the debounce timer. fn fires only after delay elapses
// with no subsequent Trigger calls.
func (d *debouncer) Trigger() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timer != nil {
		d.timer.Stop()
	}
	d.timer = time.AfterFunc(d.delay, d.fn)
}

// Stop cancels any pending debounce timer. Safe to call multiple times.
func (d *debouncer) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timer != nil {
		d.timer.Stop()
	}
}

// — Signal bus types ——————————————————————————————————————————————————————

// SignalEvent is the enriched payload passed to every [App.OnSignal] handler.
// It carries structured metadata about a content lifecycle transition,
// allowing downstream subscribers (webhooks, audit trail, social syndication)
// to act on the event without importing content types directly.
type SignalEvent struct {
	// Type is the unqualified content type name (e.g. "Post").
	Type string

	// Slug is the URL slug of the content item.
	Slug string

	// Title is the human-readable content title. Populated when the content
	// type implements [Titled]; empty otherwise.
	Title string

	// URL is the absolute canonical URL of the content item, built from
	// Config.BaseURL + module prefix + "/" + Slug.
	URL string

	// Timestamp is the wall-clock time at which the signal was dispatched.
	Timestamp time.Time

	// PreviousState is the lifecycle state before the transition.
	// Empty for AfterCreate (no previous state).
	// Possible values: "draft", "scheduled", "published", "archived".
	PreviousState string

	// ActorRole is the role of the authenticated user who triggered the signal.
	// "guest" for unauthenticated requests.
	ActorRole string

	// ActorID is the stable ID of the authenticated user. Empty for
	// unauthenticated requests.
	ActorID string

	// raw holds the original content item. Used internally by the webhook
	// delivery handler to build the full payload. Not exposed to external
	// OnSignal subscribers.
	raw any
}

// afterHookMeta carries module-level metadata from notifyAfter to the App's
// wireSignalBus closure. Using a struct avoids a stringly-typed parameter list
// and makes future extension non-breaking.
type afterHookMeta struct {
	// TypeName is the unqualified content type name (e.g. "Post").
	TypeName string

	// Prefix is the module's URL prefix (e.g. "/posts").
	Prefix string

	// PrevState is the lifecycle state before the transition.
	// Empty for AfterCreate.
	PrevState string
}

// buildSignalEvent constructs a [SignalEvent] from the parameters available
// in the wireSignalBus closure. Called once per signal dispatch.
func buildSignalEvent(ctx Context, _ Signal, meta afterHookMeta, item any, baseURL string) SignalEvent {
	n := extractNode(item)
	slug := n.Slug
	title := ""
	if t, ok := item.(Titled); ok {
		title = t.ContentTitle()
	}
	role := "guest"
	actorID := ""
	if u := ctx.User(); len(u.Roles) > 0 {
		role = string(u.Roles[0])
		actorID = u.ID
	}
	url := strings.TrimRight(baseURL, "/") + meta.Prefix + "/" + slug
	return SignalEvent{
		Type:          meta.TypeName,
		Slug:          slug,
		Title:         title,
		URL:           url,
		Timestamp:     time.Now(),
		PreviousState: meta.PrevState,
		ActorRole:     role,
		ActorID:       actorID,
		raw:           item,
	}
}
