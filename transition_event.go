package smeldr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
)

// statusTransition is one committed status change of one item, as handed to
// [App.afterStatusChange] (D107). from and to differ; a call with equal states
// is ignored.
type statusTransition struct {
	typeName string // registered type name
	id, slug string
	from, to string
	surface  string // "http", "mcp", "trigger" or "" when the caller cannot tell
	reason   string
	// milestones says whether the named milestone events ([statusSignals])
	// reach the App's bus. Every call site sets it explicitly: true for a
	// member's transition and for the items a conflict supersedes, false for
	// [App.DrainEvalQueue], which keeps the T211/D51 exclusion (no
	// human-publish subscriber is activated by background automation).
	milestones bool
	// item is the saved item when the caller holds it (a snapshot, never the
	// caller's own pointer), with its module's URL prefix. When nil it is loaded
	// by id, and only when something will read it.
	item   any
	prefix string
	// channel is the event-stream channel of the "<type>.transitioned" event
	// when channelSet; otherwise it is derived from the type and the item.
	channel    string
	channelSet bool
	// store is where the record goes when storeSet: a [DynamicTypeRepo]
	// passes its own ([DynamicTypeRepo.WithProvenance], nil records nothing);
	// otherwise the App's [App.Provenance] store.
	store    ProvenanceStore
	storeSet bool
}

// afterStatusChange records and announces one committed status change, the
// same way on every path (D107):
//  1. one provenance record and one standing write ([applyStateChange]);
//  2. one canonical "<type>.transitioned" event, to the event stream and to
//     webhook endpoints subscribed to it;
//  3. the named milestones ([statusSignals]) once each on the App's bus
//     ([App.OnSignal], the stream, webhooks subscribed to them), marked so the
//     [App.Provenance] subscriber does not record them a second time, unless
//     t.milestones is false or the target state suppresses signals;
//  4. one [AfterTransition] to the [App.AddSignalListener] callbacks.
//
// Steps 3 and 4 run asynchronously. Every failure is logged and never
// returned: the change has committed. A module's own [On] handlers are not
// fired here; each path fires them itself, as before.
func (a *App) afterStatusChange(ctx context.Context, t statusTransition) {
	if t.from == t.to {
		return
	}
	actorID, actorKind := actorFromContext(ctx)
	store := a.provenanceStore
	if t.storeSet {
		store = t.store
	}
	applyStateChange(ctx, a.cfg.DB, store, stateChange{
		typeName:  t.typeName,
		id:        t.id,
		from:      t.from,
		to:        t.to,
		reason:    t.reason,
		surface:   t.surface,
		actorKind: actorKind,
		actorID:   actorID,
	})

	busy := a.hasBusWork()
	if busy && t.item == nil {
		t.item, t.prefix = a.loadForAnnounce(ctx, t.typeName, t.id)
	}
	if t.slug == "" && t.item != nil {
		t.slug = nodeSlugOf(t.item)
	}
	if !t.channelSet {
		t.channel = transitionChannel(t.typeName, t.item)
	}
	dispatchTransitionWebhookFrom(ctx, a.webhookStore, a.webhookPool, a.eventBroadcaster, actorID, t.channel,
		strings.ToLower(t.typeName)+".transitioned",
		transitionWebhookData{
			Type:      strings.ToLower(t.typeName),
			ID:        t.id,
			Slug:      t.slug,
			FromState: t.from,
			ToState:   t.to,
			Reason:    t.reason,
			ActorID:   actorID,
			ActorKind: actorKind,
		})

	if !busy || t.item == nil {
		return
	}
	var sigs []LifecycleEvent
	if t.milestones && !suppressesSignals(ctx, a.cfg.DB, t.typeName, t.to) {
		sigs = statusSignals(Status(t.from), Status(t.to))
	}
	meta := afterHookMeta{
		TypeName:       t.typeName,
		Prefix:         t.prefix,
		PrevState:      t.from,
		Surface:        t.surface,
		Reason:         t.reason,
		FromTransition: true,
	}
	sctx := a.signalContext(withoutConflictHold(ctx))
	item := t.item
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.ErrorContext(sctx, "smeldr: transition announcement panic", "panic", r, "type", t.typeName, "id", t.id)
			}
		}()
		for _, sig := range sigs {
			a.signalBus(sctx, sig, meta, item)
		}
		a.notifyListeners(AfterTransition, t.typeName, item)
	}()
}

// hasBusWork reports whether anything listens to the App's bus: an
// [App.OnSignal] handler, an [App.AddSignalListener] callback or the event
// stream. Without one an announcement skips loading the item.
func (a *App) hasBusWork() bool {
	if a.eventBroadcaster != nil || len(a.signalListeners) > 0 {
		return true
	}
	a.busMu.RLock()
	defer a.busMu.RUnlock()
	return len(a.busHandlers) > 0
}

// signalBus builds the [SignalEvent] for sig and hands it to the bus
// ([App.dispatchBus]: the [App.OnSignal] handlers and the event stream), then
// to the [App.AddSignalListener] callbacks: what a module's afterHook does for
// its own events, for a caller that is not a module.
func (a *App) signalBus(ctx Context, sig LifecycleEvent, meta afterHookMeta, item any) {
	ev := buildSignalEvent(ctx, sig, meta, item, a.cfg.BaseURL)
	a.dispatchBus(ctx, ev, sig)
	a.notifyListeners(sig, meta.TypeName, item)
}

// notifyListeners calls every [App.AddSignalListener] callback with sig. A
// panicking callback is recovered and logged; the others still run.
func (a *App) notifyListeners(sig LifecycleEvent, typeName string, item any) {
	for _, l := range a.signalListeners {
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("smeldr: signal listener panic", "panic", r, "signal", sig, "type", typeName)
				}
			}()
			l(sig, typeName, item)
		}()
	}
}

// signalContext returns ctx as a [Context], so [buildSignalEvent] can read the
// caller. A plain context.Context (a system path) becomes a background
// Context whose user is [GuestUser], the same one
// [App.fireModuleAfterTransition] uses, keeping ctx's values.
func (a *App) signalContext(ctx context.Context) Context {
	if c, ok := ctx.(Context); ok {
		return c
	}
	return &valuesContext{Context: a.backgroundContext(), values: ctx}
}

// valuesContext is a [Context] whose values come from another context: a
// system path's context keeps what it carries while gaining the [Context]
// methods.
type valuesContext struct {
	Context
	values context.Context
}

func (v *valuesContext) Value(key any) any { return v.values.Value(key) }

// loadForAnnounce loads a changed item for its bus events, when the caller
// does not hold it: a compiled type through the module registered for it
// (the first one that holds the id), a runtime-defined type through its
// repository. It returns nil when the item cannot be loaded; a failure other
// than not-found is logged.
func (a *App) loadForAnnounce(ctx context.Context, typeName, id string) (item any, prefix string) {
	var desc *TypeDescriptor
	if a.typeRegistry != nil {
		desc = a.typeRegistry.Lookup(typeName)
	}
	if desc != nil && desc.Kind == "content" {
		repo, err := a.DynamicContentRepo(typeName)
		if err == nil {
			var node *DynamicNode
			node, err = repo.GetByID(ctx, id)
			if err == nil {
				if desc.Schema != nil {
					prefix = desc.Schema.URLPrefix
				}
				return node, prefix
			}
		}
		if !errors.Is(err, ErrNotFound) {
			slog.WarnContext(ctx, "smeldr: transition announcement: the item could not be loaded, its bus events are skipped",
				"type", typeName, "id", id, "error", err)
		}
		return nil, ""
	}
	for _, m := range a.transitionModules[typeName] {
		item, prefix, err := m.loadByID(ctx, id)
		if err == nil {
			return item, prefix
		}
		if !errors.Is(err, ErrNotFound) {
			slog.WarnContext(ctx, "smeldr: transition announcement: the item could not be loaded, its bus events are skipped",
				"type", typeName, "id", id, "error", err)
			return nil, ""
		}
	}
	return nil, ""
}

// transitionChannel is the event-stream channel of typeName's
// "<type>.transitioned" event: its topic ([transitionTopicChannels]), else the
// item's own routing field ([channelValueFromItem]), else "" (a broadcast).
func transitionChannel(typeName string, item any) string {
	if ch := transitionTopicChannels[typeName]; ch != "" {
		return ch
	}
	if item == nil {
		return ""
	}
	return channelValueFromItem(typeName, item)
}

// withoutConflictHold returns ctx with no conflict lock marked as held. A
// plan's lock is released when the transition returns, but the async work it
// starts (bus handlers, module [On] handlers, listeners) runs later: carrying
// the mark there would let a handler that transitions an item of the same type
// skip the lock and break the flow's ActiveState uniqueness.
func withoutConflictHold(ctx context.Context) context.Context {
	if held, _ := ctx.Value(conflictHeldKey{}).([]string); len(held) == 0 {
		return ctx
	}
	if c, ok := ctx.(Context); ok {
		return &heldContext{Context: c}
	}
	return context.WithValue(ctx, conflictHeldKey{}, []string(nil))
}

// withoutConflictHoldContext is [withoutConflictHold] for a [Context].
func withoutConflictHoldContext(ctx Context) Context {
	if c, ok := withoutConflictHold(ctx).(Context); ok {
		return c
	}
	return ctx
}

// contentIgnored are the JSON keys a status change sets by itself: the
// lifecycle fields of [Node] (no json tags, so the Go names), the storage
// counters, and the last writer in both spellings (a custom type's untagged
// LastActor, the orchestration types' and [DynamicNode]'s "last_actor").
var contentIgnored = []string{"Status", "PublishedAt", "ScheduledAt", "UpdatedAt", "CreatedAt", "Rev", "LastActor", "last_actor"}

// contentChanged reports whether a PUT changed anything but the lifecycle and
// bookkeeping fields in [contentIgnored] (D107): a status-only PUT is a
// transition, not a content edit. Fields are compared as their JSON encoding,
// so equal JSON compares equal whatever its whitespace or map order. An item
// that cannot be encoded counts as changed: over-reporting an edit is the safe
// side.
func contentChanged(before, after any) bool {
	b, errB := json.Marshal(before)
	a, errA := json.Marshal(after)
	if errB != nil || errA != nil {
		return true
	}
	var mb, ma map[string]json.RawMessage
	if json.Unmarshal(b, &mb) != nil || json.Unmarshal(a, &ma) != nil {
		return true
	}
	for _, k := range contentIgnored {
		delete(mb, k)
		delete(ma, k)
	}
	if len(mb) != len(ma) {
		return true
	}
	for k, v := range mb {
		w, ok := ma[k]
		if !ok || !bytes.Equal(v, w) {
			return true
		}
	}
	return false
}
