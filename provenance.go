// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// ProvenanceRecord is one immutable record of who did what to a governed subject,
// how, and optionally why. Written by [App.Provenance] for every lifecycle
// transition on a Node-subject, and by the relation graph for every asserted edge.
//
// Unlike [AuditRecord] (kept, unchanged, for backward compatibility — see
// [App.Audit]), ProvenanceRecord is keyed on SubjectType+SubjectID rather than
// ContentType+Slug: every Node has an ID, and so does every [RelationEdge], but
// RelationEdge has no Slug. ProvenanceRecord is additive — it does not replace
// AuditRecord's table, wiring, or behaviour.
type ProvenanceRecord struct {
	ID          string    `json:"id"`           // UUID v7 primary key
	Timestamp   time.Time `json:"timestamp"`    // wall-clock time the event was recorded (UTC)
	SubjectType string    `json:"subject_type"` // Go type name, e.g. "Decision", "RelationEdge"
	SubjectID   string    `json:"subject_id"`   // Node.ID or RelationEdge.ID
	Verb        string    `json:"verb"`         // "create" | "update" | "transition" | "assert" | "invalidate"
	FromState   string    `json:"from_state"`   // empty for create/assert
	ToState     string    `json:"to_state"`     // empty for invalidate
	ActorKind   string    `json:"actor_kind"`   // "human" | "job" | "agent"; empty only if truly unattributable
	ActorID     string    `json:"actor_id"`     // user UUID, job identifier, or agent identifier
	Surface     string    `json:"surface"`      // "http" | "mcp" | "cli" | "trigger"; empty when not derivable
	Reason      string    `json:"reason"`       // optional free text, empty unless supplied
}

// ProvenanceFilter narrows a [ProvenanceStore.List] query.
// Zero values are treated as "no filter" for that dimension.
type ProvenanceFilter struct {
	From        time.Time // zero = no lower bound
	To          time.Time // zero = no upper bound
	SubjectType string    // empty = all types
	SubjectID   string    // empty = all subjects
	ActorID     string    // empty = all actors
}

// ProvenanceStore is the persistence interface for [ProvenanceRecord]s.
// Implement it for a custom storage backend; use [NewProvenanceStore] for the
// default SQLite/Postgres-compatible implementation.
type ProvenanceStore interface {
	Append(ctx context.Context, r ProvenanceRecord) error
	List(ctx context.Context, f ProvenanceFilter) ([]ProvenanceRecord, error)
}

// sqlProvenanceStore is the default SQL-backed [ProvenanceStore].
type sqlProvenanceStore struct {
	db DB
}

// NewProvenanceStore returns a [ProvenanceStore] backed by db.
//
// The smeldr_provenance table must exist before [App.Provenance] is called.
// Create it with [CreateProvenanceTable], or run the following DDL directly:
//
//	CREATE TABLE IF NOT EXISTS smeldr_provenance (
//	    id           TEXT PRIMARY KEY,
//	    timestamp    TIMESTAMPTZ NOT NULL,
//	    subject_type TEXT NOT NULL,
//	    subject_id   TEXT NOT NULL,
//	    verb         TEXT NOT NULL,
//	    from_state   TEXT NOT NULL,
//	    to_state     TEXT NOT NULL,
//	    actor_kind   TEXT NOT NULL,
//	    actor_id     TEXT NOT NULL,
//	    surface      TEXT NOT NULL,
//	    reason       TEXT NOT NULL
//	);
func NewProvenanceStore(db DB) ProvenanceStore {
	return &sqlProvenanceStore{db: db}
}

// CreateProvenanceTable creates the smeldr_provenance table and its
// (subject_type, subject_id) index if they do not exist. Call once at
// application startup before [NewProvenanceStore].
//
// The index serves [SubjectProvenance] and [ProvenanceStore.List], which both
// filter on exactly those two columns. Every state change made through
// [App.TransitionItemVia] is now recorded, so the table grows much faster than
// when only module lifecycle events wrote to it, and an unindexed read would
// scan all of it. Both statements are idempotent, so calling this on boot
// against a table that predates the index adds the index without touching any
// row. A caller that creates the table from its own DDL (see
// [NewProvenanceStore]) should add the same index.
func CreateProvenanceTable(db DB) error {
	if _, err := db.ExecContext(context.Background(), `
		CREATE TABLE IF NOT EXISTS smeldr_provenance (
			id           TEXT PRIMARY KEY,
			timestamp    TIMESTAMPTZ NOT NULL,
			subject_type TEXT NOT NULL,
			subject_id   TEXT NOT NULL,
			verb         TEXT NOT NULL,
			from_state   TEXT NOT NULL,
			to_state     TEXT NOT NULL,
			actor_kind   TEXT NOT NULL,
			actor_id     TEXT NOT NULL,
			surface      TEXT NOT NULL,
			reason       TEXT NOT NULL
		)`); err != nil {
		return err
	}
	_, err := db.ExecContext(context.Background(),
		`CREATE INDEX IF NOT EXISTS idx_smeldr_provenance_subject ON smeldr_provenance (subject_type, subject_id)`)
	return err
}

// Append persists r to the smeldr_provenance table.
// Timestamp is stored as an RFC3339 string for SQLite compatibility.
func (s *sqlProvenanceStore) Append(ctx context.Context, r ProvenanceRecord) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO smeldr_provenance
		 (id, timestamp, subject_type, subject_id, verb, from_state, to_state, actor_kind, actor_id, surface, reason)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		r.ID, r.Timestamp.UTC().Format(time.RFC3339), r.SubjectType, r.SubjectID,
		r.Verb, r.FromState, r.ToState, r.ActorKind, r.ActorID, r.Surface, r.Reason,
	)
	return err
}

// List returns provenance records matching f, ordered by timestamp descending.
func (s *sqlProvenanceStore) List(ctx context.Context, f ProvenanceFilter) ([]ProvenanceRecord, error) {
	query := `SELECT id, timestamp, subject_type, subject_id, verb, from_state, to_state, actor_kind, actor_id, surface, reason
	          FROM smeldr_provenance WHERE 1=1`
	args := []any{}
	n := 1
	if !f.From.IsZero() {
		query += fmt.Sprintf(" AND timestamp >= $%d", n)
		args = append(args, f.From.UTC().Format(time.RFC3339))
		n++
	}
	if !f.To.IsZero() {
		query += fmt.Sprintf(" AND timestamp <= $%d", n)
		args = append(args, f.To.UTC().Format(time.RFC3339))
		n++
	}
	if f.SubjectType != "" {
		query += fmt.Sprintf(" AND subject_type = $%d", n)
		args = append(args, f.SubjectType)
		n++
	}
	if f.SubjectID != "" {
		query += fmt.Sprintf(" AND subject_id = $%d", n)
		args = append(args, f.SubjectID)
		n++
	}
	if f.ActorID != "" {
		query += fmt.Sprintf(" AND actor_id = $%d", n)
		args = append(args, f.ActorID)
	}
	query += " ORDER BY timestamp DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProvenanceRecord
	for rows.Next() {
		var r ProvenanceRecord
		var tsStr string
		if err := rows.Scan(&r.ID, &tsStr, &r.SubjectType, &r.SubjectID,
			&r.Verb, &r.FromState, &r.ToState, &r.ActorKind, &r.ActorID, &r.Surface, &r.Reason); err != nil {
			return nil, err
		}
		r.Timestamp, _ = time.Parse(time.RFC3339, tsStr)
		out = append(out, r)
	}
	return out, rows.Err()
}

// recordProvenance persists rec via store, logging and swallowing any error.
// Fail-open: a provenance-recording failure must never fail the write it is
// recording (design doc §7) — the same fail-open discipline validateTransition's
// structural-error zone and applyConflictPolicy already apply in this codebase.
func recordProvenance(ctx context.Context, store ProvenanceStore, rec ProvenanceRecord) {
	if store == nil {
		return
	}
	if rec.ID == "" {
		rec.ID = NewID()
	}
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now().UTC()
	}
	if err := store.Append(ctx, rec); err != nil {
		slog.WarnContext(ctx, "smeldr: recordProvenance: append failed",
			"subject_type", rec.SubjectType, "subject_id", rec.SubjectID, "verb", rec.Verb, "error", err)
	}
}

// recordTransitionProvenance records one completed state change of typeName's
// item id from fromState to toState, for the generic transition paths that do
// not go through a [Module]'s signal bus ([App.TransitionItemVia],
// [DynamicTypeRepo.SetStatus], [DynamicTypeRepo.ScheduleContent]). The actor
// and its kind come from ctx when it carries a [Context] (the same extraction
// those paths already use for last_actor) and are empty otherwise.
//
// Call it only after the status UPDATE succeeded: a rejected or failed
// transition must leave no record. It also stores the item's standing (D100) when
// the type's flow tags a state, through [applyStateChange]. Fail-open, and the
// record is a no-op for a nil store, by way of [recordProvenance]. SubjectType is the registered type name, which is
// what [transitionIsGated] looks the flow up by when the record is read back
// through [SubjectProvenance].
func recordTransitionProvenance(ctx context.Context, db DB, store ProvenanceStore, typeName, id, fromState, toState, reason, surface string) {
	actorID, actorKind := actorFromContext(ctx)
	applyStateChange(ctx, db, store, stateChange{
		typeName:  typeName,
		id:        id,
		from:      fromState,
		to:        toState,
		reason:    reason,
		surface:   surface,
		actorKind: actorKind,
		actorID:   actorID,
	})
}

// stateChange describes one completed state change of an item, whoever caused
// it: a caller's transition, a side effect of another item's transition, or a
// scheduled job. It is the input of [recordStateChange], the single place a
// state change becomes a [ProvenanceRecord].
type stateChange struct {
	typeName, id, from, to, reason, surface, actorKind, actorID string
	// verb is the [ProvenanceRecord.Verb]; empty means the verb
	// [provenanceVerbFor] gives an update from from to to.
	verb string
}

// recordStateChange is the one internal writer of state-change provenance:
// [recordTransitionProvenance], the conflict-supersede side effect,
// [App.ExpireSignals], [App.DrainEvalQueue] and the [App.Provenance] signal
// subscriber all build their record here, so anything that must happen
// whenever an item's state changes (a later standing writer, D100) has exactly
// one place to hook. Fail-open and a no-op for a nil store, by way of
// [recordProvenance]. Call it only after the status UPDATE succeeded.
func recordStateChange(ctx context.Context, store ProvenanceStore, c stateChange) {
	if store == nil {
		return
	}
	verb := c.verb
	if verb == "" {
		verb = provenanceVerbFor(AfterUpdate, c.from, c.to)
	}
	recordProvenance(ctx, store, ProvenanceRecord{
		SubjectType: c.typeName,
		SubjectID:   c.id,
		Verb:        verb,
		FromState:   c.from,
		ToState:     c.to,
		ActorKind:   c.actorKind,
		ActorID:     c.actorID,
		Surface:     c.surface,
		Reason:      c.reason,
	})
}

// actorFromContext returns the actor identity and its kind that ctx carries
// when it is a [Context] (the same extraction every generic transition path
// already uses for last_actor), and empty strings otherwise.
func actorFromContext(ctx context.Context) (id, kind string) {
	sc, ok := ctx.(interface{ User() User })
	if !ok {
		return "", ""
	}
	u := sc.User()
	return u.ID, actorKindFor(u.ID, u.Roles)
}

// provenanceLifecycleEvents is every [LifecycleEvent] that represents a completed
// state change to a Node-subject — the "After*" events, excluding Before* (not yet
// committed), SitemapRegenerate (unrelated), and AfterRelationCascade (a
// notification to a different, downstream item — that item's own transition, if
// any, already gets its own After* event on its own subject).
var provenanceLifecycleEvents = []LifecycleEvent{
	AfterCreate, AfterUpdate, AfterPublish, AfterUnpublish, AfterSchedule, AfterArchive, AfterDelete,
}

// provenanceVerbFor maps a LifecycleEvent + from/to state comparison to a
// ProvenanceRecord.Verb. AfterUpdate fires both for genuine custom-flow
// transitions and for plain content edits that do not change status (module.go's
// updateHandler/MCPUpdate call it unconditionally on every save) — distinguished
// here by comparing FromState/ToState, since the design doc's original verb set
// (create/transition/assert/invalidate) did not anticipate the no-op-status case.
func provenanceVerbFor(sig LifecycleEvent, fromState, toState string) string {
	switch sig {
	case AfterCreate:
		return "create"
	case AfterDelete:
		return "invalidate"
	default:
		if fromState == toState {
			return "update"
		}
		return "transition"
	}
}

// currentStatusOf safely extracts the current lifecycle status from item via
// [nodeStatusOf]. item is [SignalEvent]'s unexported raw field — always
// populated when built by [buildSignalEvent] (the real dispatch path), but
// test code constructing a [SignalEvent] literal directly may leave it nil.
// Returns "" rather than panicking in that case — matches recordProvenance's
// own fail-open discipline (§7): a provenance concern must never crash the
// signal dispatch it is observing.
func currentStatusOf(item any) (status string) {
	if item == nil {
		return ""
	}
	defer func() {
		if recover() != nil {
			status = ""
		}
	}()
	return string(nodeStatusOf(item))
}

// Provenance wires store to record a [ProvenanceRecord] for every completed
// lifecycle transition on every Node-subject (see [provenanceLifecycleEvents]).
// Additive and independent of [App.Audit] — both may be wired at once; the four
// events they both cover (AfterPublish/AfterSchedule/AfterArchive/AfterDelete)
// produce one record in each store, not a shared or migrated one. This is
// deliberate: AuditRecord and ProvenanceRecord are two views written from the
// same call site with the same data, never able to disagree, not two competing
// sources of truth.
//
// Errors from [ProvenanceStore.Append] are logged at Warn level and never
// propagated — a provenance-recording bug must never fail the content write it
// is recording.
//
// The smeldr_provenance table must exist before Provenance is called. See
// [NewProvenanceStore] for the required DDL.
func (a *App) Provenance(store ProvenanceStore) *App {
	a.provenanceStore = store
	for _, sig := range provenanceLifecycleEvents {
		s := sig
		a.OnSignal(s, func(ctx context.Context, ev SignalEvent) error {
			toState := currentStatusOf(ev.raw)
			recordStateChange(ctx, a.provenanceStore, stateChange{
				typeName:  ev.Type,
				id:        ev.NodeID,
				verb:      provenanceVerbFor(s, ev.PreviousState, toState),
				from:      ev.PreviousState,
				to:        toState,
				actorKind: actorKindFor(ev.ActorID, ev.ActorRoles),
				actorID:   ev.ActorID,
				surface:   ev.Surface,
				reason:    ev.Reason,
			})
			return nil
		})
	}
	return a
}

// ProvenanceEntry is one item-history event with the gating decision of
// provenance-visibility-brief.md §4.3 already applied: actor identity
// (ActorKind, ActorID, Surface, Reason) is populated only when Gated — the
// transition that produced this record required RequiredOperation with Strict
// enforcement. An ungated entry carries only the non-identifying facts
// (Verb, FromState, ToState, Timestamp), matching the brief's own framing:
// "an act that did not [require authority] gets a word and a date, with
// nothing to open."
type ProvenanceEntry struct {
	Timestamp time.Time
	Verb      string
	FromState string
	ToState   string
	Gated     bool
	ActorKind string // "" unless Gated
	ActorID   string // "" unless Gated
	Surface   string // "" unless Gated
	Reason    string // "" unless Gated
}

// transitionIsGated reports whether typeName's fromState→toState transition
// required RequiredOperation with Strict enforcement — the exact predicate
// [validateTransition] itself evaluates (D34/D40), reused via
// [resolveFlowID]/[lookupTransitionGate] rather than reimplemented, per
// provenance-visibility-brief.md §4.3 ("the tier is whether the act had to
// pass an authority check").
//
// fromState == toState is never gated (an edit, not a transition — matches
// [provenanceVerbFor]'s own "update" classification), checked before any
// query. Any failure to resolve the gate — no flow, no declared edge, or a
// genuine DB error — returns false. This fail-closed direction is the
// opposite of [validateTransition]'s own: that function fails closed toward
// *rejecting the transition* (D34: a DB error must never silently permit an
// unauthorized act); this one fails closed toward *withholding the actor*
// (a gate that cannot be resolved must never be treated as safe to reveal).
// Both are the conservative choice for their own question — stated
// explicitly so a future reader does not assume they point the same way.
//
// The `err != nil` check against [resolveFlowID]'s own return is correct
// defensive code but structurally unreachable today: resolveFlowID
// currently swallows a genuine DB error into found=false, nil error rather
// than propagating it (T249 — a real, separate fail-open finding on
// validateTransition's own authorization path, found by architect
// reviewing this code, not introduced by it). This check stays correct
// and dormant, not dead weight, until T249 fixes resolveFlowID to
// propagate a real error — at which point this branch gets a live path.
func transitionIsGated(ctx context.Context, db DB, typeName, fromState, toState string) bool {
	if db == nil || fromState == toState {
		return false
	}
	flowID, flowFound, err := resolveFlowID(ctx, db, typeName)
	if err != nil || !flowFound {
		return false
	}
	requiredRole, _, strict, edgeFound, err := lookupTransitionGate(ctx, db, flowID, fromState, toState)
	if err != nil || !edgeFound {
		return false
	}
	return requiredRole != "" && strict
}

// SubjectProvenance returns subjectType+subjectID's gating-aware provenance
// history — never keyed on an actor (provenance-visibility-brief.md §4.1:
// "actor is never a query key"; the [ProvenanceFilter] this function builds
// never sets ActorID, satisfying the constraint structurally rather than by
// caller discipline).
//
// This is the read mechanism the brief commits core to building (§4.7: "no
// new surface" — no HTTP route, no MCP tool). Callers compose it directly,
// the same way [smeldr.dev/cloud]'s own read layer already reads core data
// (cloud/internal/read.BuildTraceReading takes a [DB] handle directly, no
// round trip) — cloud's Trace witness certificate is the one surface the
// brief names for showing an entry's actor.
func SubjectProvenance(ctx context.Context, db DB, store ProvenanceStore, subjectType, subjectID string) ([]ProvenanceEntry, error) {
	records, err := store.List(ctx, ProvenanceFilter{SubjectType: subjectType, SubjectID: subjectID})
	if err != nil {
		return nil, err
	}
	entries := make([]ProvenanceEntry, 0, len(records))
	for _, r := range records {
		gated := transitionIsGated(ctx, db, subjectType, r.FromState, r.ToState)
		e := ProvenanceEntry{
			Timestamp: r.Timestamp,
			Verb:      r.Verb,
			FromState: r.FromState,
			ToState:   r.ToState,
			Gated:     gated,
		}
		if gated {
			e.ActorKind, e.ActorID, e.Surface, e.Reason = r.ActorKind, r.ActorID, r.Surface, r.Reason
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// actorKindFor returns "" when actorID is empty (matching ProvenanceRecord's
// own "empty only if truly unattributable" principle), "job" or "agent" when
// roles contains the matching [Job]/[Agent] classification role (checked via
// [IsRole], independent of the permission hierarchy — a token can carry both
// a real permission role and a classification role at once, e.g.
// []Role{Editor, Job}), and "human" otherwise. No caller mints a [Job]- or
// [Agent]-tagged token anywhere in this codebase today; the classification
// mechanism is real and wired regardless, ready for the first caller that does.
func actorKindFor(actorID string, roles []Role) string {
	if actorID == "" {
		return ""
	}
	if IsRole(roles, Job) {
		return "job"
	}
	if IsRole(roles, Agent) {
		return "agent"
	}
	return "human"
}
