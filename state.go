package smeldr

import (
	"context"
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
}

// State is a node in a [StateFlow].
type State struct {
	// Name is the state's unique identifier within the flow (e.g. "paused").
	Name string

	// IsInitial marks this state as the entry point for newly created items.
	// Exactly one State in a flow should have IsInitial set to true.
	IsInitial bool

	// IsTerminal marks this state as a sink: no outbound transitions are
	// permitted from a terminal state.
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

	// Upsert the flow row — INSERT OR IGNORE, then SELECT id for idempotency
	// (last_insert_rowid() returns 0 after an ignored insert).
	if _, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO smeldr_state_flows(name, type_name, description) VALUES (?, ?, ?)`,
		flow.Name, flow.TypeName, flow.Description,
	); err != nil {
		return fmt.Errorf("smeldr: RegisterFlow %q: upsert flow: %w", flow.Name, err)
	}
	var flowID int64
	if err := db.QueryRowContext(ctx,
		`SELECT id FROM smeldr_state_flows WHERE name = ?`, flow.Name,
	).Scan(&flowID); err != nil {
		return fmt.Errorf("smeldr: RegisterFlow %q: read flow id: %w", flow.Name, err)
	}

	// Store ActiveState and ConflictPolicy — runs after INSERT OR IGNORE so it
	// also updates an existing flow when the policy changes.
	if _, err := db.ExecContext(ctx,
		`UPDATE smeldr_state_flows SET active_state = ?, conflict_policy = ? WHERE name = ?`,
		flow.ActiveState, string(flow.ConflictPolicy), flow.Name,
	); err != nil {
		return fmt.Errorf("smeldr: RegisterFlow %q: update conflict policy: %w", flow.Name, err)
	}

	// Upsert states.
	for _, s := range flow.States {
		if _, err := db.ExecContext(ctx,
			`INSERT OR IGNORE INTO smeldr_states(flow_id, name, is_initial, is_terminal, suppresses_signals) VALUES (?, ?, ?, ?, ?)`,
			flowID, s.Name, s.IsInitial, s.IsTerminal, s.SuppressesSignals,
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
			`INSERT OR IGNORE INTO smeldr_transitions(flow_id, from_state, to_state, required_role) VALUES (?, ?, ?, ?)`,
			flowID, t.From, t.To, roleArg,
		); err != nil {
			return fmt.Errorf("smeldr: RegisterFlow %q: upsert transition %s→%s: %w", flow.Name, t.From, t.To, err)
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
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table,
	).Scan(&tableCount); err != nil || tableCount == 0 {
		return nil // table not yet created — no items to validate
	}

	// Build NOT IN clause from the registered state names.
	placeholders := make([]string, len(flow.States))
	args := make([]any, len(flow.States))
	for i, s := range flow.States {
		placeholders[i] = "?"
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
// Returns nil when:
//   - db is nil (no DB configured)
//   - the database is not SQLite (non-SQLite databases skip flow validation)
//   - fromStatus == toStatus (identity transition — always allowed for idempotency)
//   - no flow is registered for typeName and no default flow exists
func validateTransition(ctx context.Context, db DB, typeName, fromStatus, toStatus string) error {
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
	// Look up custom flow for this type.
	var flowID int64
	err := db.QueryRowContext(ctx,
		`SELECT id FROM smeldr_state_flows WHERE type_name = ? LIMIT 1`, typeName,
	).Scan(&flowID)
	if err != nil {
		// Fall back to the default flow (type_name IS NULL, name = 'default').
		err = db.QueryRowContext(ctx,
			`SELECT id FROM smeldr_state_flows WHERE type_name IS NULL AND name = 'default' LIMIT 1`,
		).Scan(&flowID)
		if err != nil {
			return nil // no flow registered — no validation
		}
	}
	// Check whether the transition exists in smeldr_transitions.
	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_transitions WHERE flow_id = ? AND from_state = ? AND to_state = ?`,
		flowID, fromStatus, toStatus,
	).Scan(&count); err != nil {
		return nil // query failed — fail open rather than blocking all transitions
	}
	if count == 0 {
		return fmt.Errorf("%w: transition %s→%s is not permitted for type %q", ErrConflict, fromStatus, toStatus, typeName)
	}
	return nil
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
	var flowID int64
	err := db.QueryRowContext(ctx,
		`SELECT id FROM smeldr_state_flows WHERE type_name = ? LIMIT 1`, typeName,
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
		`SELECT suppresses_signals FROM smeldr_states WHERE flow_id = ? AND name = ?`,
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
		 FROM smeldr_state_flows WHERE type_name = ? LIMIT 1`,
		typeName,
	).Scan(&activeState, &conflictPolicy); err != nil {
		return nil // no flow registered — no enforcement
	}
	if activeState == "" || conflictPolicy == "" || toState != activeState {
		return nil
	}

	// Detect whether items live in a typed table or in smeldr_dynamic_content.
	staticTable := camelToSnake(typeName) + "s"
	isDynamic := false
	var tableExists int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, staticTable,
	).Scan(&tableExists); err != nil || tableExists == 0 {
		staticTable = "smeldr_dynamic_content"
		isDynamic = true
	}

	switch ConflictPolicy(conflictPolicy) {
	case ConflictReject:
		return conflictRejectCheck(ctx, db, typeName, activeState, staticTable, isDynamic)

	case ConflictSupersede:
		// Check whether activeState → superseded transition exists.
		var flowID int64
		if err := db.QueryRowContext(ctx,
			`SELECT id FROM smeldr_state_flows WHERE type_name = ? LIMIT 1`, typeName,
		).Scan(&flowID); err != nil {
			return nil // fail-open
		}
		var transCount int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM smeldr_transitions WHERE flow_id = ? AND from_state = ? AND to_state = 'superseded'`,
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
			`SELECT COUNT(*) FROM smeldr_dynamic_content WHERE type_name = ? AND status = ?`,
			typeName, activeState,
		).Scan(&count)
	} else {
		err = db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM `+quoteIdent(table)+` WHERE status = ?`,
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
				`UPDATE smeldr_dynamic_content SET status = 'superseded', updated_at = ? WHERE id = ? AND type_name = ?`,
				now, oldID, typeName)
		} else {
			_, updateErr = db.ExecContext(ctx,
				`UPDATE `+quoteIdent(table)+` SET status = 'superseded', updated_at = ? WHERE id = ?`,
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

// conflictIDs returns the IDs of all items of typeName in activeState.
func conflictIDs(ctx context.Context, db DB, typeName, activeState, table string, isDynamic bool) ([]string, error) {
	var query string
	var args []any
	if isDynamic {
		query = `SELECT id FROM smeldr_dynamic_content WHERE type_name = ? AND status = ?`
		args = []any{typeName, activeState}
	} else {
		query = `SELECT id FROM ` + quoteIdent(table) + ` WHERE status = ?`
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
func fireAsyncTriggers(ctx context.Context, db DB, typeName, fromState, toState string) {
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
		  AND t.from_state = ?
		  AND t.to_state   = ?
		  AND (f.type_name = ? OR (f.type_name IS NULL AND f.name = 'default'))
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
			// Concrete handlers added in Steps 10+ (create-signal, relation-cascade, schedule-eval).
			default:
				slog.WarnContext(ctx, "smeldr: fireAsyncTriggers unknown trigger_type",
					"trigger_type", tr.triggerType,
				)
			}
		}()
	}
}
