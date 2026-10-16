package smeldr

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
)

// set is a string set.
type set map[string]bool

// newSet returns the set of values.
func newSet(values []string) set {
	if len(values) == 0 {
		return nil
	}
	s := make(set, len(values))
	for _, v := range values {
		s[v] = true
	}
	return s
}

// compiledSubscription is one subscription prepared for matching at delivery.
type compiledSubscription struct {
	id         string
	includeOwn bool
	types      set
	events     set
	names      set
	from, to   set
	items      set // "Type:ID"
	related    set // "Type:ID" one hop from the anchor
	hasRelated bool
	fields     map[string]set
	kinds      map[SignalKind]bool
	toMe       bool
	byMe       bool
}

// compileSubscription prepares s; related is the anchor's one-hop items when
// s has a related_to dimension.
func compileSubscription(s Subscription, related set) compiledSubscription {
	c := compiledSubscription{
		id: s.ID, includeOwn: s.IncludeOwn,
		types: newSet(s.Filter.Types), events: newSet(s.Filter.Events), names: newSet(s.Filter.EventNames),
		from: newSet(s.Filter.FromStates), to: newSet(s.Filter.ToStates),
		items: newSet(s.Filter.Items), related: related, hasRelated: s.Filter.RelatedTo != nil,
		toMe: s.Filter.AddressedToMe, byMe: s.Filter.SentByMe,
	}
	if len(s.Filter.Fields) > 0 {
		c.fields = make(map[string]set, len(s.Filter.Fields))
		for k, v := range s.Filter.Fields {
			c.fields[k] = newSet(v)
		}
	}
	if len(s.Filter.SignalKinds) > 0 {
		c.kinds = map[SignalKind]bool{}
		for _, k := range s.Filter.SignalKinds {
			c.kinds[k] = true
		}
	}
	return c
}

// eventKind is the kind of an event name: "relation.asserted" and
// "relation.ended" whole, otherwise the part after the last dot.
func eventKind(event string) string {
	if strings.HasPrefix(event, "relation.") {
		return event
	}
	if i := strings.LastIndexByte(event, '.'); i >= 0 {
		return event[i+1:]
	}
	return event
}

// matches reports whether meta passes every dimension of c; names are the
// owner's routing names, for addressed_to_me and sent_by_me.
func (c compiledSubscription) matches(meta eventMeta, names set) bool {
	relation := strings.HasPrefix(meta.event, "relation.")
	if c.types != nil && !c.types[meta.subjectType] && !(relation && c.types[meta.otherType]) {
		return false
	}
	if c.events != nil && !c.events[eventKind(meta.event)] {
		return false
	}
	if c.names != nil && !c.names[meta.event] {
		return false
	}
	if c.from != nil && !c.from[meta.fromState] {
		return false
	}
	if c.to != nil && !c.to[meta.toState] {
		return false
	}
	subject, other := meta.subjectType+":"+meta.subjectID, meta.otherType+":"+meta.otherID
	if c.items != nil && !c.items[subject] && !(meta.otherType != "" && c.items[other]) {
		return false
	}
	if c.hasRelated && !c.related[subject] && !(meta.otherType != "" && c.related[other]) {
		return false
	}
	for k, values := range c.fields {
		if !values[meta.fields[k]] {
			return false
		}
	}
	if c.kinds != nil {
		info, ok := SignalTypeOf(meta.signalType)
		if meta.subjectType != "Signal" || !ok || !c.kinds[info.Kind] {
			return false
		}
	}
	if c.toMe && !names[meta.fields["receiver"]] {
		return false
	}
	if c.byMe && !names[meta.fields["sender"]] {
		return false
	}
	return true
}

// connSubscriptions is what one stream connection delivers: its owner's
// compiled subscriptions and routing names.
type connSubscriptions struct {
	owner string
	subs  []compiledSubscription
	names set
}

// match reports whether any subscription delivers meta, applying each
// subscription's own include_own.
func (cs *connSubscriptions) match(meta eventMeta) bool {
	own := meta.actorID != "" && meta.actorID == cs.owner
	for _, c := range cs.subs {
		if own && !c.includeOwn {
			continue
		}
		if c.matches(meta, cs.names) {
			return true
		}
	}
	return false
}

// routingNames are the names a Signal addressed to owner may carry: the
// owner's User.ID, the operations it holds, and the receiver values its
// subscriptions name (a role subscription names its band).
func (a *App) routingNames(ctx context.Context, owner string, subs []Subscription) (set, error) {
	names := set{owner: true}
	if a.governance != nil {
		ops, err := a.governance.grantedOperations(ctx, owner)
		if err != nil {
			return nil, fmt.Errorf("%w: routing names: %s", ErrInternal, err)
		}
		for op := range ops {
			names[op] = true
		}
	}
	for _, s := range subs {
		for _, r := range s.Filter.Fields["receiver"] {
			names[r] = true
		}
	}
	return names, nil
}

// compileForConnection prepares a connection's subscriptions: the one named
// by ident (the caller's, or any with administer), or every active one of the
// owner, with the universal defaults created first. A paused subscription
// delivers nothing.
func (a *App) compileForConnection(ctx context.Context, owner, ident string) (*connSubscriptions, *Subscription, error) {
	var subs []Subscription
	var named *Subscription
	if ident != "" {
		s, err := a.GetSubscription(ctx, ident)
		if err != nil {
			return nil, nil, err
		}
		if s.Delivery != DeliveryStream {
			return nil, nil, fmt.Errorf("%w: subscription %s delivers to a webhook, not the stream", ErrBadRequest, s.Address)
		}
		named, owner = s, s.Owner
		subs = []Subscription{*s}
	} else {
		if err := a.ensureDefaultSubscriptions(ctx, owner); err != nil {
			return nil, nil, err
		}
		all, err := listSubscriptions(ctx, a.cfg.DB, owner)
		if err != nil {
			return nil, nil, err
		}
		subs = all
	}
	everything, err := listSubscriptions(ctx, a.cfg.DB, owner)
	if err != nil {
		return nil, nil, err
	}
	names, err := a.routingNames(ctx, owner, everything)
	if err != nil {
		return nil, nil, err
	}
	cs := &connSubscriptions{owner: owner, names: names}
	for _, s := range subs {
		if s.Paused || s.Delivery != DeliveryStream {
			continue
		}
		related, err := a.relatedSet(ctx, s.Filter.RelatedTo)
		if err != nil {
			return nil, nil, err
		}
		cs.subs = append(cs.subs, compileSubscription(s, related))
	}
	return cs, named, nil
}

// relatedSet is the items one live, asserted hop from an anchor, as
// "Type:ID", by the same query a dynamic grant uses.
func (a *App) relatedSet(ctx context.Context, anchor *SubscriptionAnchor) (set, error) {
	if anchor == nil {
		return nil, nil
	}
	dir := anchor.Direction
	if dir == "" {
		dir = "both"
	}
	scope := &ReadScope{items: map[string]map[string]bool{}}
	if err := scope.loadAnchor(ctx, a.cfg.DB, anchor.Kind, dir, anchor.Anchor); err != nil && !isNoSuchTable(err) {
		return nil, fmt.Errorf("%w: related_to: %s", ErrInternal, err)
	}
	out := set{}
	for typ, ids := range scope.items {
		for id := range ids {
			out[typ+":"+id] = true
		}
	}
	return out, nil
}

// subscriptionsChanged recompiles the stream connections of owner after a
// subscription of theirs changed.
func (a *App) subscriptionsChanged(owner string) {
	a.webhookSubs.reset()
	if a.eventBroadcaster == nil {
		return
	}
	a.eventBroadcaster.recompileOwner(owner)
}

// streamSubscriptions is the App side of a stream connection: what it
// delivers and where its cursor is saved. It is what
// [newEventStreamHandlerScoped] calls.
type streamSubscriptions interface {
	compile(ctx context.Context, user User, ident string) (*connSubscriptions, *Subscription, error)
	saveCursor(ctx context.Context, id, cursor string) error
}

// appStreamSubscriptions adapts the App to [streamSubscriptions]; nil when the
// App keeps no subscriptions (no table), so connections deliver as before.
type appStreamSubscriptions struct{ a *App }

func (s appStreamSubscriptions) compile(ctx context.Context, user User, ident string) (*connSubscriptions, *Subscription, error) {
	return s.a.compileForConnection(NewContextWithUser(user), user.ID, ident)
}

func (s appStreamSubscriptions) saveCursor(ctx context.Context, id, cursor string) error {
	return saveSubscriptionCursor(ctx, s.a.cfg.DB, id, cursor)
}

// eventFields are the routing values of item, for subscription filters: band
// and priority (Task), band (Goal), scope (Decision), receiver, sender and
// signal type (Signal), and a runtime-defined type's channel field.
func (a *App) eventFields(typeName string, item any) map[string]string {
	if f := compiledEventFields(item); f != nil {
		return f
	}
	switch v := item.(type) {
	case *DynamicNode:
		if a.typeRegistry == nil {
			return nil
		}
		desc := a.typeRegistry.Lookup(typeName)
		if desc == nil {
			return nil
		}
		field := routeField(desc.Schema)
		if field == "" {
			return nil
		}
		var values map[string]any
		_ = json.Unmarshal(v.Fields, &values)
		if s, ok := values[field].(string); ok {
			out := map[string]string{field: s}
			if band, ok := values["band"].(string); ok && field != "band" {
				out["band"] = band
			}
			return out
		}
	}
	return nil
}

// Subscriptions turns on subscription records (D116): [App.Handler] creates
// smeldr_subscriptions, and the event stream delivers a connection's
// subscriptions (?subscription=, or all of its owner's when no ?channel= is
// given). Needs [Config.DB] and [App.EventStream]. Without it the stream
// routes by ?channel= only, as before.
func (a *App) Subscriptions() *App {
	a.subscriptionsOn = true
	return a
}

// subscriptionSource is the stream's subscription source when subscriptions
// are on and their table could be created; nil otherwise.
func (a *App) subscriptionSource() streamSubscriptions {
	if !a.subscriptionsOn || a.cfg.DB == nil {
		return nil
	}
	if err := CreateSubscriptionTables(a.cfg.DB); err != nil {
		slog.Error("smeldr: subscriptions not started: the stream routes by ?channel= only", "error", err)
		return nil
	}
	return appStreamSubscriptions{a}
}

// compiledEventFields are the routing values of a compiled orchestration item:
// band and priority (Task), band (Goal), scope (Decision), receiver, sender
// and signal type (Signal); nil for anything else.
func compiledEventFields(item any) map[string]string {
	switch v := item.(type) {
	case *Task:
		return map[string]string{"band": v.Band, "priority": strconv.Itoa(v.Priority)}
	case *Goal:
		return map[string]string{"band": v.Band}
	case *Decision:
		return map[string]string{"scope": v.Scope}
	case *Signal:
		return map[string]string{"receiver": v.Receiver, "sender": v.Sender, "signal_type": v.SignalType}
	}
	return nil
}
