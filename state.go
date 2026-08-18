package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// ConflictPolicy controls how the framework handles the uniqueness invariant
// when a content type declares a [StateFlow.ActiveState].
// The zero value means no enforcement — most content types leave this unset.
type ConflictPolicy string

const (
	// ConflictReject rejects any transition that would create a second item of
	// the same type in [StateFlow.ActiveState]. The caller must archive or
	// supersede the existing item first.
	ConflictReject ConflictPolicy = "reject"

	// ConflictSupersede automatically transitions all existing items of the same
	// type in [StateFlow.ActiveState] to "superseded" before proceeding with the
	// new transition. A "supersedes" relation is created via [RelationStore] when
	// one is available; if not, the supersede still proceeds without a relation.
	ConflictSupersede ConflictPolicy = "supersede"
)

// StateFlow defines a named state machine for a content type.
// Pass it to [App.RegisterFlow] at startup after [New].
//
// Each content type may have at most one registered flow. Types without a
// custom flow inherit the built-in default flow (draft → scheduled →
// published → archived), which is seeded automatically at startup.
//
// Example — registering a custom flow for an AgentJob type:
//
//	err := app.RegisterFlow(smeldr.StateFlow{
//	    Name:     "agent-job",
//	    TypeName: "AgentJob",
//	    States: []smeldr.State{
//	        {Name: "draft",     IsInitial: true},
//	        {Name: "published"},
//	        {Name: "paused",    SuppressesSignals: true},
//	        {Name: "archived",  IsTerminal: true},
//	    },
//	    Transitions: []smeldr.Transition{
//	        {From: "draft",     To: "published"},
//	        {From: "published", To: "paused"},
//	        {From: "paused",    To: "published"},
//	        {From: "published", To: "archived"},
//	        {From: "paused",    To: "archived"},
//	    },
//	})
type StateFlow struct {
	// Name is the unique identifier for this flow (e.g. "agent-job"). Required.
	Name string

	// TypeName is the Go type name of the content type this flow governs
	// (e.g. "AgentJob"). Required. Items of this type inherit the flow.
	TypeName string

	// Description is an optional human-readable description of the flow.
	Description string

	// States lists every state in the flow. Exactly one State should have
	// IsInitial set to true.
	States []State

	// Transitions lists all legal directed edges between states.
	Transitions []Transition

	// ActiveState is the state where the uniqueness invariant applies.
	// When non-empty, at most one item of [StateFlow.TypeName] may be in
	// this state at any time (enforced by [ConflictPolicy]). Leave empty
	// when there is no uniqueness constraint.
	ActiveState string

	// ConflictPolicy controls what happens when a transition would create a
	// second item in [StateFlow.ActiveState]. The zero value disables enforcement.
	ConflictPolicy ConflictPolicy

	// Triggers declares async/sync trigger handlers on individual transitions.
	// Persisted to smeldr_transition_triggers by [App.RegisterFlow].
	Triggers []TransitionTrigger
}

// State is a node in a [StateFlow].
type State struct {
	// Name is the state's unique identifier within the flow (e.g. "paused").
	Name string

	// IsInitial marks this state as the entry point for newly created items.
	// Exactly one State in a flow should have IsInitial set to true.
	IsInitial bool

	// IsTerminal marks this state as closed: no outbound transition to a
	// non-terminal state is permitted from a terminal state — reopening live
	// work from a closed item is what this forbids. A transition to another
	// terminal state is allowed (e.g. Decision's superseded→archived, further
	// bookkeeping on an already-settled item, never a reopening) — nothing
	// currently validates this at registration time (D58).
	IsTerminal bool

	// SuppressesSignals prevents After* event hooks from firing for items
	// that are in this state.
	SuppressesSignals bool
}

// Transition is a directed edge in a [StateFlow].
type Transition struct {
	// From is the source state name.
	From string

	// To is the target state name.
	To string

	// RequiredRole is the minimum role that may perform this transition.
	// An empty string means any authenticated role may perform it.
	RequiredRole string

	// RequiredReason, when true, requires the caller to supply a non-empty
	// reason for this specific transition (T149) — enforced at the same layer
	// and in the same fail-closed manner as RequiredRole. False (the zero
	// value) means no reason is required, matching every existing flow's
	// behaviour unchanged. Per-Transition, not global: a Decision→superseded
	// transition might require one; a Task todo→doing transition typically
	// would not.
	RequiredReason bool

	// Strict, when true, closes two of validateTransition's fail-open
	// branches for this specific transition (D34): a nil RoleStore (governance
	// not wired) and an empty actorID (no authenticated caller) are rejected
	// with ErrForbidden instead of silently allowed through. False (the zero
	// value, matching every existing flow's behaviour unchanged) keeps
	// today's lenient posture — RequiredRole is checked only when a RoleStore
	// and actor are actually present. Only meaningful alongside a non-empty
	// RequiredRole: Strict has no effect on a transition with no role gate.
	Strict bool
}

// TransitionTrigger registers an async or sync handler on a state transition.
// Declared in [StateFlow.Triggers] and persisted by [App.RegisterFlow].
type TransitionTrigger struct {
	// FromState is the source state the trigger activates on (e.g. "proposed").
	FromState string

	// ToState is the target state the trigger activates on (e.g. "ratified").
	ToState string

	// TriggerClass is "sync" or "async".
	TriggerClass string

	// TriggerType identifies the handler (e.g. "schedule-eval").
	TriggerType string

	// Config is a JSON string consumed by the trigger handler.
	Config string
}

// RegisterFlow upserts a custom state flow into the database at startup.
// It is idempotent: calling it twice with the same flow definition is safe.
//
// After upserting the flow, RegisterFlow validates that all existing items
// whose type matches [StateFlow.TypeName] are in states defined by the flow.
// If any item is in an unknown state, RegisterFlow returns an error — treat
// this like a failed migration and refuse to start the application.
//
// RegisterFlow requires [Config.DB] to be set. Call it after [New] returns
// and before the application starts serving.
func (a *App) RegisterFlow(flow StateFlow) error {
	if flow.Name == "" {
		return fmt.Errorf("smeldr: RegisterFlow: StateFlow.Name is required")
	}
	if flow.TypeName == "" {
		return fmt.Errorf("smeldr: RegisterFlow: StateFlow.TypeName is required")
	}
	db := a.cfg.DB
	if db == nil {
		return fmt.Errorf("smeldr: RegisterFlow %q: Config.DB is required", flow.Name)
	}
	ctx := context.Background()

	// Upsert the flow row keyed on TypeName — the real identity resolveFlowID
	// already assumes (its own query is "one row per type_name", enforced
	// here by idx_state_flows_type_name). A rename (changed Name, same
	// TypeName) updates the existing row in place instead of orphaning it
	// (T268) — matching UpsertKind's own established ON CONFLICT(type_name)
	// shape for smeldr_relation_kinds.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_state_flows(id, name, type_name, description) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (type_name) DO UPDATE SET name = EXCLUDED.name, description = EXCLUDED.description`,
		NewID(), flow.Name, flow.TypeName, flow.Description,
	); err != nil {
		return fmt.Errorf("smeldr: RegisterFlow %q: upsert flow: %w", flow.Name, err)
	}
	var flowID string
	if err := db.QueryRowContext(ctx,
		`SELECT id FROM smeldr_state_flows WHERE type_name = $1`, flow.TypeName,
	).Scan(&flowID); err != nil {
		return fmt.Errorf("smeldr: RegisterFlow %q: read flow id: %w", flow.Name, err)
	}

	// Store ActiveState and ConflictPolicy — runs after the INSERT so it
	// also updates an existing flow when the policy changes.
	if _, err := db.ExecContext(ctx,
		`UPDATE smeldr_state_flows SET active_state = $1, conflict_policy = $2 WHERE id = $3`,
		flow.ActiveState, string(flow.ConflictPolicy), flowID,
	); err != nil {
		return fmt.Errorf("smeldr: RegisterFlow %q: update conflict policy: %w", flow.Name, err)
	}

	// Upsert states.
	for _, s := range flow.States {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO smeldr_states(id, flow_id, name, is_initial, is_terminal, suppresses_signals) VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (flow_id, name) DO NOTHING`,
			NewID(), flowID, s.Name, s.IsInitial, s.IsTerminal, s.SuppressesSignals,
		); err != nil {
			return fmt.Errorf("smeldr: RegisterFlow %q: upsert state %q: %w", flow.Name, s.Name, err)
		}
	}

	// Upsert transitions.
	for _, t := range flow.Transitions {
		var roleArg any
		if t.RequiredRole != "" {
			roleArg = t.RequiredRole
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO smeldr_transitions(id, flow_id, from_state, to_state, required_role, required_reason, strict) VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT (flow_id, from_state, to_state) DO NOTHING`,
			NewID(), flowID, t.From, t.To, roleArg, t.RequiredReason, t.Strict,
		); err != nil {
			return fmt.Errorf("smeldr: RegisterFlow %q: upsert transition %s→%s: %w", flow.Name, t.From, t.To, err)
		}
	}

	// Persist transition triggers.
	for _, tr := range flow.Triggers {
		var transitionID string
		if err := db.QueryRowContext(ctx,
			`SELECT id FROM smeldr_transitions WHERE flow_id = $1 AND from_state = $2 AND to_state = $3`,
			flowID, tr.FromState, tr.ToState,
		).Scan(&transitionID); err != nil {
			return fmt.Errorf("smeldr: RegisterFlow %q: trigger %s→%s: transition not found: %w",
				flow.Name, tr.FromState, tr.ToState, err)
		}
		// Idempotency: skip INSERT when a trigger of this type already exists
		// for the transition (smeldr_transition_triggers has no UNIQUE constraint).
		var existingCount int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM smeldr_transition_triggers WHERE transition_id = $1 AND trigger_type = $2`,
			transitionID, tr.TriggerType,
		).Scan(&existingCount); err != nil {
			return fmt.Errorf("smeldr: RegisterFlow %q: check trigger %s→%s: %w",
				flow.Name, tr.FromState, tr.ToState, err)
		}
		if existingCount > 0 {
			continue
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO smeldr_transition_triggers(id, transition_id, trigger_class, trigger_type, config) VALUES ($1, $2, $3, $4, $5)`,
			NewID(), transitionID, tr.TriggerClass, tr.TriggerType, tr.Config,
		); err != nil {
			return fmt.Errorf("smeldr: RegisterFlow %q: insert trigger %s→%s %s: %w",
				flow.Name, tr.FromState, tr.ToState, tr.TriggerType, err)
		}
	}

	return validateFlowItems(ctx, db, flow)
}

// validateFlowItems checks that all existing items of flow.TypeName are in a
// state defined by flow.States. Returns an error listing unknown states if any
// are found.
//
// The check is SQLite-only (same as migrateLegacyTableNames): if the database
// is not SQLite, the function returns nil. If the type's table does not yet
// exist, the function returns nil (no items = nothing to validate).
func validateFlowItems(ctx context.Context, db DB, flow StateFlow) error {
	// Probe SQLite — returns silently for non-SQLite databases.
	var dummy int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master`).Scan(&dummy); err != nil {
		return nil
	}

	table := camelToSnake(flow.TypeName) + "s"

	// Check whether the table exists yet.
	var tableCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=$1`, table,
	).Scan(&tableCount); err != nil || tableCount == 0 {
		return nil // table not yet created — no items to validate
	}

	// Build NOT IN clause from the registered state names.
	placeholders := make([]string, len(flow.States))
	args := make([]any, len(flow.States))
	for i, s := range flow.States {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = s.Name
	}

	rows, err := db.QueryContext(ctx,
		`SELECT DISTINCT status FROM `+quoteIdent(table)+` WHERE status NOT IN (`+strings.Join(placeholders, ", ")+`)`,
		args...,
	)
	if err != nil {
		return fmt.Errorf("smeldr: RegisterFlow %q: validate items in %q: %w", flow.Name, table, err)
	}
	defer rows.Close()

	var unknown []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return fmt.Errorf("smeldr: RegisterFlow %q: validate items: scan: %w", flow.Name, err)
		}
		unknown = append(unknown, s)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("smeldr: RegisterFlow %q: validate items: %w", flow.Name, err)
	}

	if len(unknown) > 0 {
		return fmt.Errorf("smeldr: RegisterFlow %q: items in %q are in unknown states [%s] — migrate the data or add the states to the flow definition",
			flow.Name, table, strings.Join(unknown, ", "))
	}
	return nil
}

// validateTransition checks whether the transition from fromStatus to toStatus
// is permitted for the given content type by its registered flow. Returns
// ErrConflict when the transition is not allowed.
//
// validateTransition checks that the transition fromStatus→toStatus is permitted
// for typeName by the registered state flow. When governance is wired and the
// transition row carries a required_role, the actor must hold a grant to that role.
//
// Two distinct failure zones:
//
//   - FAIL-OPEN (structural): DB unavailable, no flow registered, or any error
//     on the structural queries → returns nil (allow). This preserves backwards
//     compatibility for deployments without a registered flow.
//
//   - FAIL-CLOSED (authorization): the transition exists and carries a
//     required_role, governance is wired, and the actor's grants are evaluated.
//     Any DB error during the grant check returns [ErrForbidden] (deny).
//
// The actorID parameter carries the token ID of the actor initiating the transition.
// When actorID is empty the required_role check is skipped — this applies to any
// caller that does not carry a user in context: system-initiated paths (e.g. batch
// migrations, test code with plain context.Context, or background jobs that do not
// pass a smeldr.Context). Only callers that explicitly provide an actorID are
// subject to governance enforcement.
//
// The reason parameter carries the caller-supplied rationale for this transition,
// if any (T149). When the transition row carries required_reason=true and reason
// is empty, the transition is rejected with [ErrBadRequest] — this check is
// fail-closed and unconditional, unlike required_role's checks (it does not
// depend on governance being wired or an actorID being present, since supplying
// a reason is a caller-side fact, not an identity check).
//
// Returns nil when:
//   - db is nil (no DB configured)
//   - the database is not SQLite (non-SQLite databases skip flow validation)
//   - fromStatus == toStatus (identity transition — always allowed for idempotency)
//   - no flow is registered for typeName and no default flow exists
func validateTransition(ctx context.Context, db DB, rs *RoleStore, actorID, typeName, fromStatus, toStatus, reason string) error {
	if db == nil {
		return nil
	}
	// Probe SQLite — same guard as validateFlowItems.
	var dummy int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master`).Scan(&dummy); err != nil {
		return nil
	}
	if fromStatus == toStatus {
		return nil
	}

	flowID, flowFound, flowErr := resolveFlowID(ctx, db, typeName)
	if flowErr != nil {
		return fmt.Errorf("%w: could not resolve state flow for type %q: %s",
			ErrInternal, typeName, flowErr)
	}
	if !flowFound {
		return nil // no flow registered — no validation
	}

	// Verify the target state exists in this flow before checking the transition
	// edge. Produces a more specific error than "transition not permitted" when the
	// caller supplies a state name that belongs to a different flow entirely.
	var targetExists int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_states WHERE flow_id = $1 AND name = $2`,
		flowID, toStatus,
	).Scan(&targetExists); err == nil && targetExists == 0 {
		return fmt.Errorf("%w: %q is not a valid target state for type %q",
			ErrConflict, toStatus, typeName)
	}

	// ── FAIL-OPEN structural boundary ────────────────────────────────────────
	requiredRole, requiredReason, strict, edgeFound, gateErr := lookupTransitionGate(ctx, db, flowID, fromStatus, toStatus)
	if !edgeFound && gateErr == nil {
		return fmt.Errorf("%w: transition %s→%s is not permitted for type %q", ErrConflict, fromStatus, toStatus, typeName)
	}
	if gateErr != nil {
		// D34: fail closed globally — this branch fires before the strict
		// check below is ever reached, for every transition, strict or not.
		// A transient error (SQLITE_BUSY, cancelled context) must not silently
		// bypass a role gate; the caller can retry.
		return fmt.Errorf("%w: transition %s→%s: could not verify authorization: %s",
			ErrInternal, fromStatus, toStatus, gateErr)
	}

	// ── FAIL-CLOSED: required_reason ─────────────────────────────────────────
	// Unconditional — does not depend on governance or actorID, since "did the
	// caller supply a reason" is a fact about the call itself, not an identity.
	if requiredReason && reason == "" {
		return fmt.Errorf("%w: transition %s→%s requires a reason", ErrBadRequest, fromStatus, toStatus)
	}

	// ── FAIL-CLOSED authorization boundary ───────────────────────────────────
	if requiredRole == "" {
		return nil // no role gate on this transition
	}
	if rs == nil {
		if strict {
			return fmt.Errorf("%w: transition %s→%s requires role %q but governance is not wired",
				ErrForbidden, fromStatus, toStatus, requiredRole)
		}
		return nil // governance not wired — skip required_role check (non-strict, unchanged)
	}
	if actorID == "" {
		// Any caller without an actor in context (system paths, plain
		// context.Context, background jobs) is treated as pre-authorized —
		// unless this transition is strict (D34), in which case an absent
		// actor cannot satisfy a role gate and is rejected instead.
		if strict {
			return fmt.Errorf("%w: transition %s→%s requires role %q but no actor is present",
				ErrForbidden, fromStatus, toStatus, requiredRole)
		}
		return nil
	}
	ok, err := rs.RoleGranted(ctx, actorID, requiredRole, AuthTarget{})
	if err != nil {
		return fmt.Errorf("%w: transition %s→%s requires role %q: %s",
			ErrForbidden, fromStatus, toStatus, requiredRole, err)
	}
	if !ok {
		return fmt.Errorf("%w: transition %s→%s requires role %q",
			ErrForbidden, fromStatus, toStatus, requiredRole)
	}
	return nil
}

// resolveFlowID resolves typeName's registered flow — type-specific first,
// falling back to the default flow (type_name IS NULL, name = 'default') —
// returning found=false when neither exists (no flow registered at all).
// Extracted from validateTransition (T243) so [transitionIsGated] can share
// the identical resolution rather than reimplementing it.
func resolveFlowID(ctx context.Context, db DB, typeName string) (flowID string, found bool, err error) {
	e := db.QueryRowContext(ctx,
		`SELECT id FROM smeldr_state_flows WHERE type_name = $1 LIMIT 1`, typeName,
	).Scan(&flowID)
	if e == nil {
		return flowID, true, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return "", false, e
	}
	e = db.QueryRowContext(ctx,
		`SELECT id FROM smeldr_state_flows WHERE type_name IS NULL AND name = 'default' LIMIT 1`,
	).Scan(&flowID)
	if e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, e
	}
	return flowID, true, nil
}

// lookupTransitionGate fetches fromState→toState's own transition row within
// flowID, if declared. found=false with a nil error means the flow exists but
// this exact edge is not declared in it — distinct from [resolveFlowID]
// returning found=false, which means no flow exists at all; callers
// (validateTransition, transitionIsGated) treat the two differently, so both
// must stay visible rather than collapsed into one "not found" case.
// Extracted from validateTransition (T243) — same query, same two outcomes,
// now shared rather than duplicated for the provenance gating check.
func lookupTransitionGate(ctx context.Context, db DB, flowID, fromState, toState string) (requiredRole string, requiredReason, strict, found bool, err error) {
	var nullRole sql.NullString
	e := db.QueryRowContext(ctx,
		`SELECT required_role, required_reason, strict FROM smeldr_transitions WHERE flow_id = $1 AND from_state = $2 AND to_state = $3`,
		flowID, fromState, toState,
	).Scan(&nullRole, &requiredReason, &strict)
	if errors.Is(e, sql.ErrNoRows) {
		return "", false, false, false, nil
	}
	if e != nil {
		return "", false, false, false, e
	}
	return nullRole.String, requiredReason, strict, true, nil
}

// validateInitialState checks whether statusName is a known state in the
// registered flow for typeName. It is called at create time, before the item
// is persisted, to prevent callers from storing arbitrary status strings.
//
// Fail-open cases (returns nil):
//   - db is nil (no DB configured)
//   - statusName is empty (caller omitted status — module defaults apply)
//   - the database is not SQLite (sqlite_master probe fails)
//   - no flow is registered for typeName and no default flow exists
//   - the state membership query fails (structural DB error)
func validateInitialState(ctx context.Context, db DB, typeName, statusName string) error {
	if db == nil || statusName == "" {
		return nil
	}
	var dummy int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master`).Scan(&dummy); err != nil {
		return nil
	}
	var flowID string
	err := db.QueryRowContext(ctx,
		`SELECT id FROM smeldr_state_flows WHERE type_name = $1 LIMIT 1`, typeName,
	).Scan(&flowID)
	if err != nil {
		err = db.QueryRowContext(ctx,
			`SELECT id FROM smeldr_state_flows WHERE type_name IS NULL AND name = 'default' LIMIT 1`,
		).Scan(&flowID)
		if err != nil {
			return nil
		}
	}
	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_states WHERE flow_id = $1 AND name = $2`,
		flowID, statusName,
	).Scan(&count); err != nil {
		return nil // structural error — fail open
	}
	if count == 0 {
		return fmt.Errorf("%w: %q is not a valid state in the registered flow for type %q",
			ErrConflict, statusName, typeName)
	}
	return nil
}

// defaultInitialState returns the IsInitial state registered for typeName's
// own custom StateFlow, or "" when none is registered — or on any
// structural/DB issue, fail-open, matching validateInitialState's own
// fail-open cases (nil DB, non-SQLite, missing flow, query error). Callers
// fall back to the literal Draft constant when this returns "": the
// built-in default flow's own initial state is "draft" (migrateStateFlows),
// so a second query against it here would return the same answer for no
// benefit — unlike validateInitialState/suppressesSignals, this function
// deliberately does not fall back to the default flow.
func defaultInitialState(ctx context.Context, db DB, typeName string) string {
	if db == nil {
		return ""
	}
	var dummy int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master`).Scan(&dummy); err != nil {
		return ""
	}
	var flowID string
	if err := db.QueryRowContext(ctx,
		`SELECT id FROM smeldr_state_flows WHERE type_name = $1 LIMIT 1`, typeName,
	).Scan(&flowID); err != nil {
		return ""
	}
	var name string
	if err := db.QueryRowContext(ctx,
		`SELECT name FROM smeldr_states WHERE flow_id = $1 AND is_initial = $2 LIMIT 1`,
		flowID, true,
	).Scan(&name); err != nil {
		return ""
	}
	return name
}

// suppressesSignals reports whether the given state in the type's registered
// flow has suppresses_signals=true. Returns false on any error (fail-open).
// Called by notifyAfter to gate After* event dispatch.
//
// Fail-open cases (returns false):
//   - db is nil (no DB configured)
//   - the database is not SQLite (sqlite_master probe fails)
//   - no flow is registered for typeName and no default flow exists
//   - the state is not found in the flow or any query fails
func suppressesSignals(ctx context.Context, db DB, typeName, statusName string) bool {
	if db == nil {
		return false
	}
	var dummy int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master`).Scan(&dummy); err != nil {
		return false
	}
	var flowID string
	err := db.QueryRowContext(ctx,
		`SELECT id FROM smeldr_state_flows WHERE type_name = $1 LIMIT 1`, typeName,
	).Scan(&flowID)
	if err != nil {
		// Fall back to the default flow (type_name IS NULL, name = 'default').
		err = db.QueryRowContext(ctx,
			`SELECT id FROM smeldr_state_flows WHERE type_name IS NULL AND name = 'default' LIMIT 1`,
		).Scan(&flowID)
		if err != nil {
			return false // no flow registered — signals fire normally
		}
	}
	var suppresses bool
	if err := db.QueryRowContext(ctx,
		`SELECT suppresses_signals FROM smeldr_states WHERE flow_id = $1 AND name = $2`,
		flowID, statusName,
	).Scan(&suppresses); err != nil {
		return false // state not found or query failed — fail open
	}
	return suppresses
}

// applyConflictPolicy enforces the uniqueness invariant declared by
// [StateFlow.ActiveState] and [StateFlow.ConflictPolicy] at transition time.
// It must be called after [validateTransition] succeeds, before the status UPDATE.
//
// Returns nil (fail-open) when:
//   - db is nil
//   - the database is not SQLite
//   - no flow is registered for typeName
//   - ActiveState is empty or ConflictPolicy is empty
//   - toState does not equal ActiveState
//
// newItemID is the ID of the item being transitioned into ActiveState, used
// to create the optional "supersedes" relation in [ConflictSupersede] mode.
// rs may be nil — relation creation is always fail-open.
func applyConflictPolicy(ctx context.Context, db DB, rs *RelationStore, typeName, toState, newItemID string) error {
	if db == nil {
		return nil
	}
	var dummy int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master`).Scan(&dummy); err != nil {
		return nil // not SQLite — skip
	}

	var activeState, conflictPolicy string
	if err := db.QueryRowContext(ctx,
		`SELECT COALESCE(active_state, ''), COALESCE(conflict_policy, '')
		 FROM smeldr_state_flows WHERE type_name = $1 LIMIT 1`,
		typeName,
	).Scan(&activeState, &conflictPolicy); err != nil {
		return nil // no flow registered — no enforcement
	}
	if activeState == "" || conflictPolicy == "" || toState != activeState {
		return nil
	}

	// Detect whether items live in a typed table or in smeldr_dynamic_content.
	// Reuses resolveItemTable rather than re-probing sqlite_master directly —
	// its own probe order (smeldr_<snake>s, then <snake>s, then
	// smeldr_dynamic_content) is what every other item-resolution path in
	// this package already relies on (T229).
	staticTable := resolveItemTable(ctx, db, typeName)
	isDynamic := staticTable == "smeldr_dynamic_content"

	switch ConflictPolicy(conflictPolicy) {
	case ConflictReject:
		return conflictRejectCheck(ctx, db, typeName, activeState, staticTable, isDynamic)

	case ConflictSupersede:
		// Check whether activeState → superseded transition exists.
		var flowID string
		if err := db.QueryRowContext(ctx,
			`SELECT id FROM smeldr_state_flows WHERE type_name = $1 LIMIT 1`, typeName,
		).Scan(&flowID); err != nil {
			return nil // fail-open
		}
		var transCount int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM smeldr_transitions WHERE flow_id = $1 AND from_state = $2 AND to_state = 'superseded'`,
			flowID, activeState,
		).Scan(&transCount); err != nil || transCount == 0 {
			// No superseded transition — fall back to reject behaviour.
			return conflictRejectCheck(ctx, db, typeName, activeState, staticTable, isDynamic)
		}
		return conflictSupersede(ctx, db, rs, typeName, activeState, newItemID, staticTable, isDynamic)
	}
	return nil
}

// conflictRejectCheck returns ErrConflict when any item of typeName is already
// in activeState. Returns nil on DB error (fail-open).
func conflictRejectCheck(ctx context.Context, db DB, typeName, activeState, table string, isDynamic bool) error {
	var count int
	var err error
	if isDynamic {
		err = db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM smeldr_dynamic_content WHERE type_name = $1 AND status = $2`,
			typeName, activeState,
		).Scan(&count)
	} else {
		err = db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM `+quoteIdent(table)+` WHERE status = $1`,
			activeState,
		).Scan(&count)
	}
	if err != nil {
		return nil // fail-open
	}
	if count > 0 {
		return fmt.Errorf("%w: type %q already has an item in state %q", ErrConflict, typeName, activeState)
	}
	return nil
}

// conflictSupersede transitions all existing items of typeName in activeState to
// "superseded" and optionally creates a "supersedes" relation for each via rs.
// Individual UPDATE and relation failures are logged but do not block the caller.
func conflictSupersede(ctx context.Context, db DB, rs *RelationStore, typeName, activeState, newItemID, table string, isDynamic bool) error {
	ids, err := conflictIDs(ctx, db, typeName, activeState, table, isDynamic)
	if err != nil {
		return nil // fail-open
	}
	now := time.Now().UTC()
	for _, oldID := range ids {
		var updateErr error
		if isDynamic {
			_, updateErr = db.ExecContext(ctx,
				`UPDATE smeldr_dynamic_content SET status = 'superseded', updated_at = $1 WHERE id = $2 AND type_name = $3`,
				now, oldID, typeName)
		} else {
			_, updateErr = db.ExecContext(ctx,
				`UPDATE `+quoteIdent(table)+` SET status = 'superseded', updated_at = $1 WHERE id = $2`,
				now, oldID)
		}
		if updateErr != nil {
			slog.WarnContext(ctx, "smeldr: applyConflictPolicy: supersede UPDATE failed",
				"type", typeName, "id", oldID, "error", updateErr)
			continue
		}
		if rs != nil && newItemID != "" {
			if relErr := rs.Assert(ctx, RelationEdge{
				SourceType:   typeName,
				SourceID:     newItemID,
				TargetType:   typeName,
				TargetID:     oldID,
				RelationKind: "supersedes",
				EdgeClass:    "asserted",
			}); relErr != nil {
				slog.WarnContext(ctx, "smeldr: applyConflictPolicy: supersedes relation failed",
					"type", typeName, "new_id", newItemID, "old_id", oldID, "error", relErr)
			}
		}
	}
	return nil
}

// resolveItemTable returns the DB table name that stores items of typeName.
// It probes sqlite_master in order: smeldr_<snake>s (orchestration types),
// <snake>s (static module types), then falls back to smeldr_dynamic_content.
func resolveItemTable(ctx context.Context, db DB, typeName string) string {
	snake := camelToSnake(typeName) + "s"
	for _, candidate := range []string{"smeldr_" + snake, snake} {
		var n int
		if db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=$1", candidate,
		).Scan(&n) == nil && n > 0 {
			return candidate
		}
	}
	return "smeldr_dynamic_content"
}

// TransitionItem moves the item identified by typeName and slug to toState.
// Equivalent to [App.TransitionItemWithReason] with an empty reason —
// preserved unchanged, including for any [Transition.RequiredReason] gate,
// which this form can never satisfy. Existing callers are unaffected by
// [App.TransitionItemWithReason]'s addition.
func (a *App) TransitionItem(ctx context.Context, typeName, slug, toState string) (map[string]any, error) {
	return a.TransitionItemWithReason(ctx, typeName, slug, toState, "")
}

// TransitionItemWithReason moves the item identified by typeName and slug to
// toState, resolving whichever table stores it — a compiled [Module]'s own
// table or a runtime-defined dynamic type — and validating the transition
// exactly as the HTTP path ([Module.updateHandler]) and
// [DynamicTypeRepo.SetStatus] already do (D49). actorID for the D34/D40 role
// gate is extracted from ctx when it carries a [Context]; a plain
// context.Context yields an empty actorID, matching SetStatus's own existing
// behaviour. reason satisfies a [Transition.RequiredReason] gate (A220);
// added as a new method rather than changing [App.TransitionItem]'s own
// signature, mirroring [DynamicTypeRepo.SetStatus]/[DynamicTypeRepo.
// SetStatusWithReason]'s established shape — preserving the API stability
// promise for [App.TransitionItem]'s existing callers (T235). Returns
// [ErrNotFound] when typeName is registered but no item has the given slug,
// or a descriptive error when typeName is not registered at all.
//
// Rev note (D49): the compiled-type branch performs a raw status UPDATE and
// does not read or advance [Node.Rev] — the same choice
// [DynamicTypeRepo.SetStatus] and applyConflictPolicy's own supersede path
// already make for their raw status updates. A concurrent [SQLRepo.Save]
// holding the item's pre-transition rev still satisfies Save's CAS check
// (the stored rev is unchanged) and will silently overwrite this status
// change with whatever status its own in-memory item carries — the same
// exposure the existing dynamic-content and conflict-supersede paths
// already carry, now extended to compiled types rather than newly
// introduced.
func (a *App) TransitionItemWithReason(ctx context.Context, typeName, slug, toState, reason string) (map[string]any, error) {
	desc := a.typeRegistry.Lookup(typeName)
	if desc == nil {
		return nil, fmt.Errorf("%w: content type %q not registered", ErrBadRequest, typeName)
	}
	if desc.Kind == "content" {
		repo, err := a.DynamicContentRepo(typeName)
		if err != nil {
			return nil, err
		}
		node, err := repo.GetBySlug(ctx, slug)
		if err != nil {
			return nil, err
		}
		if err := repo.SetStatusWithReason(ctx, node.ID, Status(toState), reason); err != nil {
			return nil, err
		}
		return map[string]any{"id": node.ID, "slug": slug, "status": toState}, nil
	}

	if a.cfg.DB == nil {
		return nil, fmt.Errorf("smeldr: TransitionItem requires Config.DB")
	}
	db := a.cfg.DB
	table := resolveItemTable(ctx, db, typeName)
	if table == "smeldr_dynamic_content" {
		// desc.Kind == "compiled" (the only other branch reachable here) but
		// no dedicated table was found — a partial migration or an
		// unregistered module's table simply absent. D47's own guard against
		// this exact ambiguity: do not silently fall into the dynamic-content
		// branch for a type the registry says is compiled.
		return nil, fmt.Errorf("smeldr: %q is registered as a compiled type but its table could not be found", typeName)
	}

	// realSlug is fetched from the row, never assumed to equal the caller's
	// own ident — ident can be a human ID (e.g. "T203") rather than a real
	// slug when the fallback below resolves it (T253); echoing ident back
	// as "slug" in the response would be wrong in that case.
	var id, realSlug, currentStatus string
	err := db.QueryRowContext(ctx,
		"SELECT id, slug, status FROM "+quoteIdent(table)+" WHERE slug = $1", slug,
	).Scan(&id, &realSlug, &currentStatus)
	if errors.Is(err, sql.ErrNoRows) {
		if col, ok := humanIDColumns[typeName]; ok {
			err = db.QueryRowContext(ctx,
				"SELECT id, slug, status FROM "+quoteIdent(table)+" WHERE "+quoteIdent(col)+" = $1", slug,
			).Scan(&id, &realSlug, &currentStatus)
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("%w: TransitionItem: %s", ErrInternal, err)
	}

	type smeldrCtxAccessor interface{ User() User }
	actorID := ""
	if sc, ok := ctx.(smeldrCtxAccessor); ok {
		actorID = sc.User().ID
	}
	if err := validateTransition(ctx, db, a.governance, actorID, typeName, currentStatus, toState, reason); err != nil {
		return nil, err
	}
	if err := applyConflictPolicy(ctx, db, nil, typeName, toState, id); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	if _, err := db.ExecContext(ctx,
		"UPDATE "+quoteIdent(table)+" SET status = $1, updated_at = $2 WHERE id = $3",
		toState, now, id,
	); err != nil {
		return nil, fmt.Errorf("%w: TransitionItem: %s", ErrInternal, err)
	}
	fireAsyncTriggers(ctx, db, typeName, currentStatus, toState, id)
	dispatchTransitionWebhook(ctx, a.webhookStore, a.webhookPool, a.eventBroadcaster,
		strings.ToLower(typeName)+".transitioned",
		transitionWebhookData{
			Type:      strings.ToLower(typeName),
			ID:        id,
			Slug:      realSlug,
			FromState: currentStatus,
			ToState:   toState,
			Reason:    reason,
		})
	return map[string]any{"id": id, "slug": realSlug, "status": toState}, nil
}

// isNoSuchTable reports whether err is a SQLite "no such table" error.
func isNoSuchTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table")
}

// drainAuthorizationGate reports whether typeName's fromState→toState
// transition requires a role — automation may never cross such a boundary
// itself (D42, [DrainEvalQueue]'s authority half of T211).
// requiredRole is empty when the transition is not gated; a non-empty
// value is the exact role name the flow declares, independent of whether
// that transition is [Transition.Strict] and independent of whether
// governance is wired on this instance — declaring a RequiredRole is the
// operator's stated intent about that transition, and the absence of a
// wired [RoleStore] does not withdraw it for an unattended caller.
//
// Returns the item's current status as fromState (needed by the caller
// either way, and unavailable from smeldr_eval_queue itself, which stores
// only toState). A query error is reported as err, not folded into a
// "gated" verdict — the caller falls back to its own existing skip+log
// behaviour for a plain failure rather than misreporting it as an
// authorization block.
func drainAuthorizationGate(ctx context.Context, db DB, table, typeName, itemID, toState string) (fromState, requiredRole string, err error) {
	if err := db.QueryRowContext(ctx,
		"SELECT status FROM "+quoteIdent(table)+" WHERE id = $1", itemID,
	).Scan(&fromState); err != nil {
		return "", "", fmt.Errorf("smeldr: drainAuthorizationGate: read status: %w", err)
	}
	if fromState == toState {
		return fromState, "", nil
	}

	var flowID string
	lookupErr := db.QueryRowContext(ctx,
		`SELECT id FROM smeldr_state_flows WHERE type_name = $1 LIMIT 1`, typeName,
	).Scan(&flowID)
	if lookupErr != nil {
		lookupErr = db.QueryRowContext(ctx,
			`SELECT id FROM smeldr_state_flows WHERE type_name IS NULL AND name = 'default' LIMIT 1`,
		).Scan(&flowID)
		if lookupErr != nil {
			return fromState, "", nil // no flow registered — nothing to gate
		}
	}

	var role sql.NullString
	rowErr := db.QueryRowContext(ctx,
		`SELECT required_role FROM smeldr_transitions WHERE flow_id = $1 AND from_state = $2 AND to_state = $3`,
		flowID, fromState, toState,
	).Scan(&role)
	if errors.Is(rowErr, sql.ErrNoRows) {
		return fromState, "", nil // transition not declared — nothing to gate
	}
	if rowErr != nil {
		return "", "", fmt.Errorf("smeldr: drainAuthorizationGate: read transition: %w", rowErr)
	}
	if !role.Valid {
		return fromState, "", nil
	}
	return fromState, role.String, nil
}

// recordAuthorizationRequiredSignal inserts a Signal recording that
// typeName/itemID's fromState→toState transition requires a human holding
// requiredRole — the loud-failure half of T211's authority answer
// ([drainAuthorizationGate]). A Signal is a persisted, queryable row
// (unlike a log line), addressed to the exact role the flow declares —
// never a hardcoded role, since RequiredRole is free-form and a Signal
// naming the wrong authority would be a false statement about who may act.
//
// store/pool deliver a "signal.created" webhook event for this Signal (T231);
// broadcaster additionally streams it (T269) — the same event name a
// human-created Signal already produces via the normal [Module.MCPCreate]
// path's AfterCreate signal, so a subscriber cannot distinguish a
// D42-triggered Signal from a human-created one by event name, which is the
// correct behaviour: a Signal is a Signal. [dispatchTransitionWebhook] is
// nil-safe, so a caller with webhooks/streaming unwired passes nil for
// whichever it lacks.
func recordAuthorizationRequiredSignal(ctx context.Context, db DB, store *WebhookStore, pool *workerPool, broadcaster *eventBroadcaster, typeName, itemID, fromState, toState, requiredRole string) error {
	id := NewID()
	now := time.Now().UTC()
	message := fmt.Sprintf("%s %s: %s→%s requires role %q", typeName, itemID, fromState, toState, requiredRole)
	_, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_signals
			(id, slug, status, created_at, updated_at, sender, receiver, signal_type, message, task_ref, sequence)
		VALUES
			($1, $2, 'pending', $3, $4, 'system', $5, 'authorization-required', $6, '', 0)`,
		id, id, now, now, requiredRole, message,
	)
	if err != nil {
		return fmt.Errorf("smeldr: recordAuthorizationRequiredSignal: %w", err)
	}
	dispatchTransitionWebhook(ctx, store, pool, broadcaster, "signal.created", transitionWebhookData{
		Type: "signal", ID: id, Slug: id, ToState: "pending",
	})
	return nil
}

// DrainEvalQueue transitions items whose scheduled evaluation time has arrived.
// It selects all rows from smeldr_eval_queue WHERE eval_at <= now, then for
// each row checks whether the item's current status→to_state transition is
// role-gated ([drainAuthorizationGate]): if not, applies a direct status
// UPDATE; if it is, automation does not cross the boundary itself — a
// [recordAuthorizationRequiredSignal] Signal is recorded instead. Either way
// the queue row is deleted regardless of outcome (failed or blocked
// transitions are not re-queued — they are logged/recorded and counted as
// skipped).
//
// Returns walked (total eligible rows read from smeldr_eval_queue, T223 — the
// count that makes triggered/skipped meaningful on a clean run), the number
// of items transitioned (triggered), and items skipped due to errors or a
// role gate. Returns (0, 0, 0, nil) when Config.DB is nil or the table does
// not yet exist (fail-open).
func (a *App) DrainEvalQueue(ctx context.Context) (walked, triggered, skipped int, err error) {
	db := a.cfg.DB
	if db == nil {
		return 0, 0, 0, nil
	}

	type queueRow struct {
		id       string
		typeName string
		itemID   string
		toState  string
	}

	rows, queryErr := db.QueryContext(ctx,
		`SELECT id, type_name, item_id, to_state FROM smeldr_eval_queue WHERE eval_at <= $1`,
		time.Now().UTC(),
	)
	if isNoSuchTable(queryErr) {
		return 0, 0, 0, nil
	}
	if queryErr != nil {
		return 0, 0, 0, fmt.Errorf("smeldr: DrainEvalQueue: query: %w", queryErr)
	}
	defer rows.Close()

	var pending []queueRow
	for rows.Next() {
		walked++
		var r queueRow
		if err := rows.Scan(&r.id, &r.typeName, &r.itemID, &r.toState); err != nil {
			slog.WarnContext(ctx, "smeldr: DrainEvalQueue: scan", "error", err)
			skipped++
			continue
		}
		pending = append(pending, r)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return walked, 0, skipped, fmt.Errorf("smeldr: DrainEvalQueue: rows: %w", rowsErr)
	}
	rows.Close()

	now := time.Now().UTC()
	for _, r := range pending {
		table := resolveItemTable(ctx, db, r.typeName)

		fromState, requiredRole, gateErr := drainAuthorizationGate(ctx, db, table, r.typeName, r.itemID, r.toState)
		switch {
		case gateErr != nil:
			// A plain failure, not an authorization verdict — falls back
			// to the same skip+log treatment as any other DrainEvalQueue
			// error rather than misreporting it as "authorization
			// required."
			slog.WarnContext(ctx, "smeldr: DrainEvalQueue: authorization gate check failed",
				"type_name", r.typeName, "item_id", r.itemID, "to_state", r.toState, "error", gateErr)
			skipped++
		case requiredRole != "":
			// Automation may never cross a role-gated boundary itself —
			// the authority half of T211. Emit the loud-failure Signal
			// instead of applying the transition.
			if sigErr := recordAuthorizationRequiredSignal(ctx, db, a.webhookStore, a.webhookPool, a.eventBroadcaster, r.typeName, r.itemID, fromState, r.toState, requiredRole); sigErr != nil {
				slog.WarnContext(ctx, "smeldr: DrainEvalQueue: authorization-required signal failed",
					"type_name", r.typeName, "item_id", r.itemID, "to_state", r.toState, "required_role", requiredRole, "error", sigErr)
			}
			skipped++
		default:
			_, updateErr := db.ExecContext(ctx,
				"UPDATE "+quoteIdent(table)+" SET status = $1, updated_at = $2 WHERE id = $3",
				r.toState, now, r.itemID,
			)
			if updateErr != nil {
				slog.WarnContext(ctx, "smeldr: DrainEvalQueue: UPDATE failed",
					"type_name", r.typeName, "item_id", r.itemID, "to_state", r.toState, "error", updateErr)
				skipped++
			} else {
				// T211/D51: record the condition's arrival. This is the one
				// absent write D51 identified — provenance only (signal
				// dispatch, cache invalidation and rebuild triggers are
				// deliberately out of scope, argued in the Amendment: this
				// drain's only target today is D40's Decision re-evaluation
				// flow, and firing AfterPublish-class signals for an
				// automated transition would activate every human-publish
				// subscriber with no operator decision that background
				// automation should trigger them). ActorKind "job" with a
				// fixed ActorID naming the mechanism, not a Run (D38) — a
				// stateless periodic sweep has no claim/lease/worktree
				// lifecycle to attach a Run to; generalises to
				// SweepStructural (T223) as the same pattern. Fail-open:
				// recordProvenance itself logs-and-swallows an Append
				// failure — the queue row is still deleted below regardless
				// (A241's own "not re-queued" rule, unweakened).
				if a.provenanceStore != nil {
					recordProvenance(ctx, a.provenanceStore, ProvenanceRecord{
						SubjectType: r.typeName,
						SubjectID:   r.itemID,
						Verb:        provenanceVerbFor(AfterUpdate, fromState, r.toState),
						FromState:   fromState,
						ToState:     r.toState,
						ActorKind:   "job",
						ActorID:     "drain-eval-queue",
						Surface:     "trigger",
					})
				}
				triggered++
			}
		}

		// Always delete from queue — failed/blocked transitions are not
		// re-queued.
		if _, delErr := db.ExecContext(ctx,
			`DELETE FROM smeldr_eval_queue WHERE id = $1`, r.id,
		); delErr != nil {
			slog.WarnContext(ctx, "smeldr: DrainEvalQueue: DELETE failed",
				"queue_id", r.id, "error", delErr)
		}
	}
	return walked, triggered, skipped, nil
}

// conflictIDs returns the IDs of all items of typeName in activeState.
func conflictIDs(ctx context.Context, db DB, typeName, activeState, table string, isDynamic bool) ([]string, error) {
	var query string
	var args []any
	if isDynamic {
		query = `SELECT id FROM smeldr_dynamic_content WHERE type_name = $1 AND status = $2`
		args = []any{typeName, activeState}
	} else {
		query = `SELECT id FROM ` + quoteIdent(table) + ` WHERE status = $1`
		args = []any{activeState}
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			slog.WarnContext(ctx, "smeldr: applyConflictPolicy: scan id", "error", err)
			continue
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// fireAsyncTriggers queries smeldr_transition_triggers for async trigger rows
// matching the given (typeName, fromState, toState) transition and dispatches
// each in a goroutine. Panics inside goroutines are recovered and logged.
// Fails silently on DB error — the transition itself always succeeds.
// Called by DynamicTypeRepo.SetStatus after a successful status UPDATE.
func fireAsyncTriggers(ctx context.Context, db DB, typeName, fromState, toState, itemID string) {
	if db == nil {
		return
	}
	var dummy int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master`).Scan(&dummy); err != nil {
		return // not SQLite — skip
	}
	rows, err := db.QueryContext(ctx, `
		SELECT tt.trigger_type, tt.config
		FROM smeldr_transition_triggers tt
		JOIN smeldr_transitions t ON tt.transition_id = t.id
		JOIN smeldr_state_flows f ON t.flow_id = f.id
		WHERE tt.trigger_class = 'async'
		  AND t.from_state = $1
		  AND t.to_state   = $2
		  AND (f.type_name = $3 OR (f.type_name IS NULL AND f.name = 'default'))
	`, fromState, toState, typeName)
	if err != nil {
		slog.WarnContext(ctx, "smeldr: fireAsyncTriggers query failed",
			"type_name", typeName, "error", err)
		return
	}
	defer rows.Close()

	type trigRow struct{ triggerType, config string }
	var triggers []trigRow
	for rows.Next() {
		var tr trigRow
		if err := rows.Scan(&tr.triggerType, &tr.config); err != nil {
			slog.WarnContext(ctx, "smeldr: fireAsyncTriggers scan failed", "error", err)
			return
		}
		triggers = append(triggers, tr)
	}
	if err := rows.Err(); err != nil {
		slog.WarnContext(ctx, "smeldr: fireAsyncTriggers rows error", "error", err)
		return
	}

	for _, tr := range triggers {
		tr := tr
		go func() {
			defer func() {
				if r := recover(); r != nil {
					slog.ErrorContext(ctx, "smeldr: fireAsyncTriggers panic",
						"panic", r, "trigger_type", tr.triggerType)
				}
			}()
			slog.InfoContext(ctx, "smeldr: fireAsyncTriggers dispatch",
				"trigger_type", tr.triggerType,
				"type_name", typeName,
				"from_state", fromState,
				"to_state", toState,
				"config", tr.config,
			)
			switch tr.triggerType {
			case "schedule-eval":
				var cfg struct {
					EvalField string `json:"eval_field"`
					ToState   string `json:"to_state"`
				}
				if err := json.Unmarshal([]byte(tr.config), &cfg); err != nil || cfg.EvalField == "" || cfg.ToState == "" {
					slog.WarnContext(ctx, "smeldr: schedule-eval: bad config", "config", tr.config)
					return
				}
				if itemID == "" {
					slog.WarnContext(ctx, "smeldr: schedule-eval: no itemID",
						"type_name", typeName, "from_state", fromState, "to_state", toState)
					return
				}
				table := resolveItemTable(ctx, db, typeName)
				var evalAt sql.NullTime
				if err := db.QueryRowContext(ctx,
					"SELECT "+cfg.EvalField+" FROM "+quoteIdent(table)+" WHERE id = $1", itemID,
				).Scan(&evalAt); err != nil || !evalAt.Valid || evalAt.Time.IsZero() {
					slog.WarnContext(ctx, "smeldr: schedule-eval: eval_field unreadable or empty",
						"item_id", itemID, "eval_field", cfg.EvalField)
					return
				}
				if _, err := db.ExecContext(ctx,
					`INSERT INTO smeldr_eval_queue (id, type_name, item_id, to_state, eval_at)
					 VALUES ($1, $2, $3, $4, $5) ON CONFLICT (type_name, item_id, to_state) DO NOTHING`,
					NewID(), typeName, itemID, cfg.ToState, evalAt.Time.UTC(),
				); err != nil {
					slog.WarnContext(ctx, "smeldr: schedule-eval: INSERT failed",
						"item_id", itemID, "error", err)
				}
			default:
				slog.WarnContext(ctx, "smeldr: fireAsyncTriggers unknown trigger_type",
					"trigger_type", tr.triggerType,
				)
			}
		}()
	}
}
