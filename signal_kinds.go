package smeldr

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// SignalKind says what a [Signal] is (D115): a condition the system keeps
// while it holds, or a request one actor makes of another.
type SignalKind string

const (
	// SignalCondition is a state of the world the system emits and resolves
	// itself: it is open while the condition holds and resolved when it ends.
	// No actor answers or withdraws one.
	SignalCondition SignalKind = "condition"
	// SignalRequest is an ask from one actor to another: it is open until it is
	// answered or withdrawn, and never resolved by the system.
	SignalRequest SignalKind = "request"
)

// SignalTypeInfo describes one signal_type in the catalog (D115): its kind and
// the rule that ends it.
type SignalTypeInfo struct {
	// Type is the signal_type value.
	Type string `json:"type"`
	// Kind is [SignalCondition] or [SignalRequest].
	Kind SignalKind `json:"kind"`
	// Resolution says what ends a Signal of this type.
	Resolution string `json:"resolution"`
}

// signalTypeTable is the catalog, in a fixed order. Read-only after init.
var signalTypeTable = []SignalTypeInfo{
	{"ratification-required", SignalCondition, "resolved when the Decision leaves proposed"},
	{"authorization-required", SignalCondition, "resolved when the subject leaves from_state: the blocked transition happened or was abandoned"},
	{"conflict-detected", SignalCondition, "resolved when the contradicts edge ends or a side leaves ratified; for a taken URL, when the item is published or its slug changes"},
	{"downstream-blocked", SignalCondition, "resolved when the blocking item is done; no emitter yet"},
	{"reference-stale", SignalCondition, "resolved when the reference is refreshed or removed; no emitter yet"},
	{"premise-flagged", SignalCondition, "resolved when the premise is in force again, the grounded_in edge ends, the resting Decision leaves force, or a ratify holder confirms it with confirm_premise (D149, D153, T919)"},
	{"coverage-matches-nothing", SignalCondition, "resolved when the coverage rule is changed, archived or matches again (CheckCoverage)"},
	{"merge-unapproved", SignalCondition, "resolved when an approver answers for the Area with answer_change_check: approved after the fact, or not approved with a reason (T918)"},
	{"review-requested", SignalRequest, "answered by the review, or withdrawn by the asker"},
	{"question", SignalRequest, "answered by the answer, or withdrawn by the asker"},
}

// signalTypeIndex is signalTypeTable keyed by type.
var signalTypeIndex = func() map[string]SignalTypeInfo {
	m := make(map[string]SignalTypeInfo, len(signalTypeTable))
	for _, t := range signalTypeTable {
		m[t.Type] = t
	}
	return m
}()

// SignalTypes returns the signal_type catalog (D115) in a fixed order. The
// slice is a copy. A type not in it is history: it is still accepted with a
// deprecated_signal_type warning until [Config.SignalEventCutover].
func SignalTypes() []SignalTypeInfo {
	out := make([]SignalTypeInfo, len(signalTypeTable))
	copy(out, signalTypeTable)
	return out
}

// SignalTypeOf returns the catalog entry for t, and false when t is not in
// the catalog (a history type).
func SignalTypeOf(t string) (SignalTypeInfo, bool) {
	info, ok := signalTypeIndex[t]
	return info, ok
}

// SignalMessageMaxLength is the most characters a Signal's message may hold
// (D114). Longer is a message_too_long warning until
// [Config.SignalMessageCutover], and refused after it.
const SignalMessageMaxLength = 500

// SignalCutovers are the two dates at which a Signal warning turns into a
// refusal. A zero date means warnings only.
type SignalCutovers struct {
	// Message is [Config.SignalMessageCutover].
	Message time.Time
	// Event is [Config.SignalEventCutover].
	Event time.Time
}

// SignalCutovers returns the App's [Config.SignalMessageCutover] and
// [Config.SignalEventCutover], for [InsertSignalWithWarnings]. Safe on a nil
// *App (zero: warnings only).
func (a *App) SignalCutovers() SignalCutovers {
	if a == nil {
		return SignalCutovers{}
	}
	return SignalCutovers{Message: a.cfg.SignalMessageCutover, Event: a.cfg.SignalEventCutover}
}

// The states of the Signal flow (D115).
const (
	signalOpen      = "open"
	signalResolved  = "resolved"
	signalAnswered  = "answered"
	signalWithdrawn = "withdrawn"
	// signalPending is the history initial state, kept for the history types
	// until [Config.SignalEventCutover].
	signalPending = "pending"
)

// systemSignalKey marks a context as core's own: only core's emitters and
// resolver set it, so only they may create a condition or resolve one.
type systemSignalKey struct{}

// withSignalSystem returns ctx marked as core's own Signal work.
func withSignalSystem(ctx context.Context) context.Context {
	return context.WithValue(ctx, systemSignalKey{}, true)
}

// isSignalSystem reports whether ctx carries core's Signal marker.
func isSignalSystem(ctx context.Context) bool {
	v, _ := ctx.Value(systemSignalKey{}).(bool)
	return v
}

// signalReplacement names what replaces a history type, for its warning.
var signalReplacement = map[string]string{
	"implementation-question": `use signal_type "question" instead`,
}

// CheckSignal applies the Signal rules (D114, D115) to s before it is
// written. It returns the warnings of a write that may go ahead, or an error
// for one that may not:
//
//   - an empty signal_type is a [ValidationError] on signal_type;
//   - a condition type created without core's own marker is [ErrForbidden]:
//     only the system emits conditions;
//   - a type not in the catalog ([SignalTypes]) is history: a
//     deprecated_signal_type warning until c.Event, a [ValidationError] on
//     signal_type after it;
//   - a message longer than [SignalMessageMaxLength] characters is a
//     message_too_long warning until c.Message, a [ValidationError] on
//     message after it.
func CheckSignal(ctx context.Context, s *Signal, c SignalCutovers) ([]CreateWarning, error) {
	if strings.TrimSpace(s.SignalType) == "" {
		return nil, Err("signal_type", "signal_type is required")
	}
	var warnings []CreateWarning
	info, known := SignalTypeOf(s.SignalType)
	switch {
	case known && info.Kind == SignalCondition && !isSignalSystem(ctx):
		return nil, fmt.Errorf("%w: signal_type %q is a condition: only the system creates it", ErrForbidden, s.SignalType)
	case !known:
		msg := fmt.Sprintf("signal_type %q is history (D115): it is accepted until the instance's signal event cutover, then refused", s.SignalType)
		if r, ok := signalReplacement[s.SignalType]; ok {
			msg += "; " + r
		}
		ws, err := warningsCheck([]CreateWarning{{Code: "deprecated_signal_type", Field: "signal_type", Message: msg}}, c.Event)
		if err != nil {
			return nil, err
		}
		warnings = append(warnings, ws...)
	}
	if n := utf8.RuneCountInString(s.Message); n > SignalMessageMaxLength {
		msg := fmt.Sprintf("message is %d characters, the limit is %d: put the detail in the item the Signal names", n, SignalMessageMaxLength)
		ws, err := warningsCheck([]CreateWarning{{Code: "message_too_long", Field: "message", Message: msg}}, c.Message)
		if err != nil {
			return nil, err
		}
		warnings = append(warnings, ws...)
	}
	return warnings, nil
}

// signalInitialState is the state a new Signal of type t starts in: open for a
// catalog type, pending for a history type, which keeps the history flow
// until [Config.SignalEventCutover].
func signalInitialState(t string) string {
	if _, ok := SignalTypeOf(t); ok {
		return signalOpen
	}
	return signalPending
}

// signalCreateCheck applies [CheckSignal] to a Signal created through a
// module path (create_content, POST), with the App's cutovers, and sets its
// status to the type's initial state: a Signal's state is the server's. Any
// other type passes untouched.
func (m *Module[T]) signalCreateCheck(ctx context.Context, item T) ([]CreateWarning, error) {
	s, ok := any(item).(*Signal)
	if !ok {
		return nil, nil
	}
	var c SignalCutovers
	if m.signalSink != nil {
		c = m.signalSink.SignalCutovers()
	}
	ws, err := CheckSignal(ctx, s, c)
	if err != nil {
		return nil, err
	}
	s.Status = Status(signalInitialState(s.SignalType))
	return ws, nil
}
