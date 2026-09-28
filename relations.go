package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

// RelationKindDef describes a named category of typed edge in the relation graph.
// It governs whether edges are asserted (operator) or derived/inferable (agent/rule),
// whether they are directional, and which source→target type pairs are valid.
type RelationKindDef struct {
	ID       string `db:"id"`
	TypeName string `db:"type_name"`
	Label    string `db:"label"`
	// ReverseLabel names the same relation from the target's point of view
	// (e.g. Label "supersedes", ReverseLabel "superseded by") — optional and
	// unvalidated, matching Label's own treatment; "" means no reverse
	// phrasing has been established yet, not an error (T160).
	ReverseLabel string `db:"reverse_label"`
	Mode         string `db:"mode"` // "derived" | "asserted" | "inferable"
	Directional  bool   `db:"directional"`
	// Weighted is confirmed dead (D61, item 4: written and read back but
	// consulted by no production logic anywhere in this package) but is NOT
	// removed in this commit — smeldr.dev/mcp's own relation_tools.go
	// (upsert_relation_kind, list_relation_kinds) actively reads and writes
	// this field, confirmed by building example/server against it via its
	// local `replace smeldr.dev/core => ../..` directive, which fails to
	// compile without this field. D61's own text required confirming "no
	// external importer exists" before removal — that check found the
	// opposite. Removal deferred to its own follow-up, sequenced after (or
	// alongside) an smeldr.dev/mcp-band fix, not shipped here.
	Weighted   bool            `db:"weighted"`
	TypePairs  json.RawMessage `db:"type_pairs"` // JSON: [{source_type, target_type}]
	Attributes json.RawMessage `db:"attributes"`
	CreatedAt  time.Time       `db:"created_at"`
	UpdatedAt  time.Time       `db:"updated_at"`
}

// RelationEdge is a single typed adjacency between two content items.
// It does not embed Node — relations are graph edges, not content items.
type RelationEdge struct {
	ID           string     `db:"id"`
	SourceType   string     `db:"source_type"`
	SourceID     string     `db:"source_id"`
	TargetType   string     `db:"target_type"`
	TargetID     string     `db:"target_id"`
	RelationKind string     `db:"relation_kind"`
	EdgeClass    string     `db:"edge_class"` // "asserted" | "inferred"
	Confidence   *float64   `db:"confidence"`
	ValidAt      *time.Time `db:"valid_at"`
	InvalidAt    *time.Time `db:"invalid_at"`
	CreatedByJob *string    `db:"created_by_job"`

	// CreatedBy is the actor ID of whoever most recently asserted, proposed,
	// or observed this edge via [RelationStore.MCPAssertRelation],
	// [RelationStore.MCPProposeRelation], or [RelationStore.MCPObserveRelation]
	// (01a0e3bc-8) — the human/agent counterpart to CreatedByJob's own
	// system/job attribution. Nil when no caller identity was available (a
	// system-initiated call using a plain context.Context) or the edge
	// predates this column. Overwritten on every re-assert of the same
	// (source, target, relation_kind, edge_class) tuple, same as
	// CreatedByJob's own existing conflict behavior on this table — the most
	// recent asserter is who a credential surface should point at, not
	// necessarily whoever asserted it first.
	CreatedBy *string `db:"created_by"`

	Attributes json.RawMessage `db:"attributes"`
	CreatedAt  time.Time       `db:"created_at"`
	UpdatedAt  time.Time       `db:"updated_at"`

	// LastConfirmedAt is the most recent time [RelationStore.SweepStructural]
	// walked this edge and found both its source and target alive. It is nil
	// until the edge's first successful sweep confirmation, and is never set
	// by Assert/Propose/Observe — only a scheduled structural sweep advances
	// it, matching the "confirmed by the system, on a schedule" provenance a
	// witness certificate needs (distinct from any [ProvenanceRecord] verb,
	// all of which are deliberate actions on an item).
	LastConfirmedAt *time.Time `db:"last_confirmed_at"`
}

// RelationKindRegistry is an in-memory thread-safe store of relation kind definitions,
// hydrated from the database at startup and kept in sync by [RelationStore.UpsertKind].
type RelationKindRegistry struct {
	mu    sync.RWMutex
	kinds map[string]RelationKindDef
}

// RelationStore wraps a DB and an in-memory [RelationKindRegistry].
// Create with [NewRelationStore]. Wire into App with [App.Relations].
type RelationStore struct {
	db       DB
	registry *RelationKindRegistry

	// provenanceStore is non-nil when both App.Relations and App.Provenance are
	// wired on the same App — injected at App.Handler() time regardless of call
	// order (T149). Nil is a normal, fully-supported state: relation assertion
	// works identically either way, simply without a provenance record.
	provenanceStore ProvenanceStore

	// webhookStore, webhookPool, and eventBroadcaster back
	// emitConflictDetectedSignal's own dispatchTransitionWebhook call
	// (01a0dd64-2) — injected at App.Handler() time, mirroring
	// provenanceStore's own wiring. All three are individually nil-safe
	// (dispatchTransitionWebhook's own contract), unlike provenanceStore,
	// so setSignalDeps is wired unconditionally rather than gated.
	webhookStore     *WebhookStore
	webhookPool      *workerPool
	eventBroadcaster *eventBroadcaster
}

// setProvenanceStore wires store for provenance recording on future edge
// assertions. Unexported — called from App.Handler(), not part of the public API.
func (s *RelationStore) setProvenanceStore(store ProvenanceStore) {
	s.provenanceStore = store
}

// setSignalDeps wires the dependencies emitConflictDetectedSignal needs to
// dispatch a webhook/event-stream notification for each Signal it writes.
// Unexported — called from App.Handler(), not part of the public API.
func (s *RelationStore) setSignalDeps(store *WebhookStore, pool *workerPool, broadcaster *eventBroadcaster) {
	s.webhookStore = store
	s.webhookPool = pool
	s.eventBroadcaster = broadcaster
}

// Column order constants — scan order must match SELECT order exactly.
const relationKindColumns = `id, type_name, label, reverse_label, mode, directional, weighted, type_pairs, attributes, created_at, updated_at`
const relationColumns = `id, source_type, source_id, target_type, target_id, relation_kind, edge_class, confidence, valid_at, invalid_at, created_by_job, created_by, attributes, created_at, updated_at, last_confirmed_at`

// relationInsertColumns excludes last_confirmed_at — it is only ever set by
// [RelationStore.SweepStructural]'s own confirm-write, never on insert or
// re-assert (an INSERT ... ON CONFLICT re-assert also leaves an existing
// row's last_confirmed_at untouched, since it is absent from both the
// column list and the ON CONFLICT SET clause below).
const relationInsertColumns = `id, source_type, source_id, target_type, target_id, relation_kind, edge_class, confidence, valid_at, invalid_at, created_by_job, created_by, attributes, created_at, updated_at`

// CreateRelationTables creates the smeldr_relation_kinds and smeldr_relations tables and
// their indexes if they do not already exist. Idempotent — safe to call on every boot.
func CreateRelationTables(db DB) error {
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS smeldr_relation_kinds (
    id             TEXT NOT NULL PRIMARY KEY,
    type_name      TEXT NOT NULL UNIQUE,
    label          TEXT NOT NULL DEFAULT '',
    reverse_label  TEXT NOT NULL DEFAULT '',
    mode           TEXT NOT NULL,
    directional    INTEGER NOT NULL DEFAULT 1,
    weighted       INTEGER NOT NULL DEFAULT 0,
    type_pairs     TEXT NOT NULL DEFAULT '[]',
    attributes     TEXT NOT NULL DEFAULT '{}',
    created_at     TIMESTAMPTZ NOT NULL,
    updated_at     TIMESTAMPTZ NOT NULL
)`); err != nil {
		return err
	}
	if err := EnsureColumn(ctx, db, "smeldr_relation_kinds", "reverse_label", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}

	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS smeldr_relations (
    id              TEXT NOT NULL PRIMARY KEY,
    source_type     TEXT NOT NULL,
    source_id       TEXT NOT NULL,
    target_type     TEXT NOT NULL,
    target_id       TEXT NOT NULL,
    relation_kind   TEXT NOT NULL,
    edge_class      TEXT NOT NULL,
    confidence      REAL,
    valid_at        TIMESTAMPTZ,
    invalid_at      TIMESTAMPTZ,
    created_by_job  TEXT,
    created_by      TEXT,
    attributes      TEXT NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL,
    last_confirmed_at TIMESTAMPTZ
)`); err != nil {
		return err
	}
	if err := EnsureColumn(ctx, db, "smeldr_relations", "last_confirmed_at", "TIMESTAMPTZ"); err != nil {
		return err
	}
	if err := EnsureRelationCreatedByColumn(ctx, db); err != nil {
		return err
	}

	if _, err := db.ExecContext(ctx, `
CREATE INDEX IF NOT EXISTS idx_relations_source
    ON smeldr_relations (source_type, source_id, relation_kind)`); err != nil {
		return err
	}

	if _, err := db.ExecContext(ctx, `
CREATE INDEX IF NOT EXISTS idx_relations_target
    ON smeldr_relations (target_type, target_id, relation_kind)`); err != nil {
		return err
	}

	if _, err := db.ExecContext(ctx, `
CREATE INDEX IF NOT EXISTS idx_relations_governance_temporal
    ON smeldr_relations (relation_kind, valid_at, invalid_at)
    WHERE valid_at IS NOT NULL`); err != nil {
		return err
	}

	// smeldr_reference_types is a brand-new table name on every install, so a
	// plain CREATE TABLE IF NOT EXISTS is self-healing on next boot with no
	// separate EnsureColumn-style migration — the A307 class of gap (a new
	// column silently missing from a pre-existing table) doesn't apply to a
	// whole new table. See RegisterReferenceType's own doc comment for why
	// this is a table, not a field on ContentTypeSchema.
	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS smeldr_reference_types (
    type_name  TEXT NOT NULL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL
)`); err != nil {
		return err
	}

	return nil
}

// EnsureRelationCreatedByColumn adds smeldr_relations' created_by column
// (01a0e3bc-8) on pre-existing SQLite databases that predate it. Fresh
// installs already have the column via [CreateRelationTables]'s own CREATE
// TABLE statement (which calls this too, so it is idempotent either way);
// this only upgrades a database created before this column existed. Same
// one-column [EnsureColumn] pattern as [EnsureDecisionTitleColumn]/
// [EnsureAmendmentBodyColumn].
func EnsureRelationCreatedByColumn(ctx context.Context, db DB) error {
	if err := EnsureColumn(ctx, db, "smeldr_relations", "created_by", "TEXT"); err != nil {
		return fmt.Errorf("smeldr: EnsureRelationCreatedByColumn: %w", err)
	}
	return nil
}

// NewRelationStore creates a RelationStore backed by db and hydrates the in-memory
// RelationKindRegistry from all rows currently in smeldr_relation_kinds.
func NewRelationStore(db DB) (*RelationStore, error) {
	s := &RelationStore{
		db:       db,
		registry: &RelationKindRegistry{kinds: make(map[string]RelationKindDef)},
	}
	if err := s.loadRegistry(context.Background()); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *RelationStore) loadRegistry(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+relationKindColumns+" FROM smeldr_relation_kinds")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		d, err := scanRelationKind(rows)
		if err != nil {
			return err
		}
		s.registry.kinds[d.TypeName] = d
	}
	return rows.Err()
}

// ValidateRelationKindDef checks that def has a non-empty type_name, a recognised mode,
// and valid JSON in type_pairs (if set).
func ValidateRelationKindDef(def RelationKindDef) error {
	if def.TypeName == "" {
		return Err("type_name", "required")
	}
	switch def.Mode {
	case "derived", "asserted", "inferable":
	default:
		return Err("mode", fmt.Sprintf("must be derived, asserted or inferable; got %q", def.Mode))
	}
	if len(def.TypePairs) > 0 {
		var pairs []any
		if err := json.Unmarshal(def.TypePairs, &pairs); err != nil {
			return Err("type_pairs", "must be a valid JSON array")
		}
	}
	return nil
}

// GetKind returns the relation kind definition for typeName from the in-memory registry.
// No database round-trip.
func (s *RelationStore) GetKind(typeName string) (RelationKindDef, bool) {
	s.registry.mu.RLock()
	defer s.registry.mu.RUnlock()
	d, ok := s.registry.kinds[typeName]
	return d, ok
}

// ListKinds returns all registered relation kinds sorted by type_name.
func (s *RelationStore) ListKinds() []RelationKindDef {
	s.registry.mu.RLock()
	out := make([]RelationKindDef, 0, len(s.registry.kinds))
	for _, d := range s.registry.kinds {
		out = append(out, d)
	}
	s.registry.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].TypeName < out[j].TypeName })
	return out
}

// UpsertKind validates def, writes it to the database, and updates the in-memory registry.
// If a kind with the same type_name already exists, all mutable fields are updated.
func (s *RelationStore) UpsertKind(ctx context.Context, def RelationKindDef) error {
	if err := ValidateRelationKindDef(def); err != nil {
		return err
	}
	now := time.Now().UTC()

	if def.TypePairs == nil {
		def.TypePairs = json.RawMessage("[]")
	}
	if def.Attributes == nil {
		def.Attributes = json.RawMessage("{}")
	}

	// Preserve id and created_at from registry for existing kinds.
	s.registry.mu.RLock()
	existing, exists := s.registry.kinds[def.TypeName]
	s.registry.mu.RUnlock()
	if exists {
		if def.ID == "" {
			def.ID = existing.ID
		}
		if def.CreatedAt.IsZero() {
			def.CreatedAt = existing.CreatedAt
		}
	}
	if def.ID == "" {
		def.ID = NewID()
	}
	if def.CreatedAt.IsZero() {
		def.CreatedAt = now
	}
	def.UpdatedAt = now

	_, err := s.db.ExecContext(ctx, `
INSERT INTO smeldr_relation_kinds (`+relationKindColumns+`)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (type_name) DO UPDATE SET
    label         = EXCLUDED.label,
    reverse_label = EXCLUDED.reverse_label,
    mode          = EXCLUDED.mode,
    directional   = EXCLUDED.directional,
    weighted      = EXCLUDED.weighted,
    type_pairs    = EXCLUDED.type_pairs,
    attributes    = EXCLUDED.attributes,
    updated_at    = EXCLUDED.updated_at`,
		def.ID, def.TypeName, def.Label, def.ReverseLabel, def.Mode,
		intOf(def.Directional), intOf(def.Weighted),
		string(def.TypePairs), string(def.Attributes),
		def.CreatedAt, def.UpdatedAt,
	)
	if err != nil {
		return err
	}

	s.registry.mu.Lock()
	s.registry.kinds[def.TypeName] = def
	s.registry.mu.Unlock()
	return nil
}

// RegisterReferenceType designates typeName — a runtime-defined dynamic
// content type — as a reference type for [defaultTargetChecker]'s own
// liveness rule (core-sweep-invalidates-domain-edges, 2026-09-28): a
// reference type's row counts as alive whenever it exists and is not
// archived (draft, published, and scheduled all count), the same "no
// status is terminal except actually gone" reasoning defaultTargetChecker
// already applies unconditionally to every compiled type — generalized
// here to the dynamic types an application designates as reference/lookup
// data (Domain, Area) rather than editorial content, for which "must be
// published to count as real" remains the correct, unchanged rule.
//
// A table, not a field on [ContentTypeSchema]: a type can be designated a
// reference type before, or independently of, its own schema row being
// defined — [RegisterOrchestrationRelationKinds] designates "domain" and
// "area" at the same point it registers D71/D72's relation kinds, not at
// schema-definition time, and a type only ever gains schema validation
// rules under [ENABLE_DYNAMIC_CONTENT]'s own separate mechanism regardless.
//
// Idempotent — safe to call on every boot, matching [UpsertKind]'s own
// idempotency convention. Returns an error when typeName is empty or the
// DB operation fails. Does not itself validate that typeName is a
// registered dynamic content type — [defaultTargetChecker] checks that
// separately and unconditionally, so a reference-type designation for a
// type that never gets registered (or is later removed) is simply inert,
// never consulted.
func (s *RelationStore) RegisterReferenceType(ctx context.Context, typeName string) error {
	if typeName == "" {
		return fmt.Errorf("smeldr: RegisterReferenceType: typeName must not be empty")
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO smeldr_reference_types (type_name, created_at) VALUES ($1, $2)
		 ON CONFLICT (type_name) DO NOTHING`,
		typeName, time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return fmt.Errorf("smeldr: RegisterReferenceType: %q: %w", typeName, err)
	}
	return nil
}

// Assert inserts or updates an asserted edge in smeldr_relations.
// The relation_kind must be registered and edge_class must be "asserted".
func (s *RelationStore) Assert(ctx context.Context, edge RelationEdge) error {
	if _, ok := s.GetKind(edge.RelationKind); !ok {
		return Err("relation_kind", fmt.Sprintf("unknown relation kind %q", edge.RelationKind))
	}
	if edge.EdgeClass != "asserted" {
		return Err("edge_class", "Assert only accepts edge_class=asserted")
	}
	_, err := s.insertEdge(ctx, edge)
	return err
}

// validateTypePairs checks edge's (SourceType, TargetType) against kind's own
// registered TypePairs, when TypePairs is non-empty. An empty (or malformed —
// already validated at registration time by ValidateRelationKindDef, so this
// should not happen in practice) TypePairs means the kind is unconstrained,
// matching extractRelationEdges's (smeldr.go) own existing treatment of
// "no TypePairs declared" as permissive rather than an error (D61).
func validateTypePairs(kind RelationKindDef, edge RelationEdge) error {
	var pairs []struct {
		SourceType string `json:"source_type"`
		TargetType string `json:"target_type"`
	}
	if err := json.Unmarshal(kind.TypePairs, &pairs); err != nil || len(pairs) == 0 {
		return nil
	}
	for _, p := range pairs {
		if p.SourceType == edge.SourceType && p.TargetType == edge.TargetType {
			return nil
		}
	}
	return Err("type_pairs", fmt.Sprintf("relation kind %q does not permit %s→%s",
		edge.RelationKind, edge.SourceType, edge.TargetType))
}

// canonicalizeNonDirectional reorders edge's (source, target) when kind is
// registered Directional: false, so the same symmetric fact asserted from
// either side produces one canonical row instead of two — (type, id) ordered
// lexicographically, the smaller pair always stored as source (D61). A
// directional kind's edge is returned unchanged.
func canonicalizeNonDirectional(kind RelationKindDef, edge RelationEdge) RelationEdge {
	if kind.Directional {
		return edge
	}
	srcKey := edge.SourceType + "\x00" + edge.SourceID
	tgtKey := edge.TargetType + "\x00" + edge.TargetID
	if srcKey > tgtKey {
		edge.SourceType, edge.TargetType = edge.TargetType, edge.SourceType
		edge.SourceID, edge.TargetID = edge.TargetID, edge.SourceID
	}
	return edge
}

// insertEdge persists edge to smeldr_relations, generating an ID and timestamps
// as needed. Returns the populated edge with all fields set. Called by Assert,
// MCPAssertRelation, MCPProposeRelation, and MCPObserveRelation.
//
// When edge's own relation kind is registered, edge is checked against the
// kind's own TypePairs and canonicalized if the kind is non-directional
// (D61, items 1 and 3) before anything is written. An unregistered kind is
// left unvalidated here — every real caller already checks GetKind itself
// and returns its own error first; this is defense in depth, not the
// primary gate.
//
// A fresh edge (edge.ID == "") reuses an existing row's ID when one already
// matches (source, target, kind, edge_class) exactly, rather than creating a
// duplicate (D61, item 3's dedup half) — an explicit edge.ID from the caller
// is never overridden, preserving the existing update-by-id contract.
func (s *RelationStore) insertEdge(ctx context.Context, edge RelationEdge) (RelationEdge, error) {
	now := time.Now().UTC()

	// 01a0e3bc-8: record the human/agent caller, same smeldrCtxAccessor
	// type-assertion dynamic.go/state.go already use for this exact
	// situation — ctx is a plain context.Context here, but every real
	// MCP-tool call site passes a smeldr.Context through unchanged.
	// System-initiated calls (a plain context.Context) leave CreatedBy nil.
	type smeldrCtxAccessor interface {
		User() User
	}
	if sc, ok := ctx.(smeldrCtxAccessor); ok {
		if actorID := sc.User().ID; actorID != "" {
			edge.CreatedBy = &actorID
		}
	}

	if kind, ok := s.GetKind(edge.RelationKind); ok {
		if err := validateTypePairs(kind, edge); err != nil {
			return RelationEdge{}, err
		}
		edge = canonicalizeNonDirectional(kind, edge)
	}

	if edge.ID == "" {
		// Dedup key: (source, target, relation_kind, edge_class). D61's own
		// literal text omitted edge_class, but that would let an "observed"
		// edge and a later "asserted" edge for the identical tuple collapse
		// onto the same row — whichever is written last silently wins
		// edge_class, which could downgrade a previously human-asserted edge
		// to "observed" the next time a webhook reports the same fact. Found
		// during implementation, flagged, and confirmed by architect review
		// as a real trust-integrity concern worth including now rather than
		// deferring — a refinement within D61's own intent (dedup the same
		// fact asserted twice), not a reversal of it (A297).
		var existingID string
		lookupErr := s.db.QueryRowContext(ctx,
			`SELECT id FROM smeldr_relations WHERE source_type=$1 AND source_id=$2 `+
				`AND target_type=$3 AND target_id=$4 AND relation_kind=$5 AND edge_class=$6`,
			edge.SourceType, edge.SourceID, edge.TargetType, edge.TargetID,
			edge.RelationKind, edge.EdgeClass,
		).Scan(&existingID)
		switch {
		case lookupErr == nil:
			edge.ID = existingID
		case errors.Is(lookupErr, sql.ErrNoRows):
			edge.ID = NewID()
		default:
			return RelationEdge{}, lookupErr
		}
	}
	if edge.CreatedAt.IsZero() {
		edge.CreatedAt = now
	}
	edge.UpdatedAt = now
	if edge.Attributes == nil {
		edge.Attributes = json.RawMessage("{}")
	}

	_, err := s.db.ExecContext(ctx, `
INSERT INTO smeldr_relations (`+relationInsertColumns+`)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
ON CONFLICT (id) DO UPDATE SET
    source_type    = EXCLUDED.source_type,
    source_id      = EXCLUDED.source_id,
    target_type    = EXCLUDED.target_type,
    target_id      = EXCLUDED.target_id,
    relation_kind  = EXCLUDED.relation_kind,
    edge_class     = EXCLUDED.edge_class,
    confidence     = EXCLUDED.confidence,
    valid_at       = EXCLUDED.valid_at,
    invalid_at     = EXCLUDED.invalid_at,
    created_by_job = EXCLUDED.created_by_job,
    created_by     = EXCLUDED.created_by,
    attributes     = EXCLUDED.attributes,
    updated_at     = EXCLUDED.updated_at`,
		edge.ID, edge.SourceType, edge.SourceID,
		edge.TargetType, edge.TargetID,
		edge.RelationKind, edge.EdgeClass,
		edge.Confidence, edge.ValidAt, edge.InvalidAt,
		edge.CreatedByJob, edge.CreatedBy, string(edge.Attributes),
		edge.CreatedAt, edge.UpdatedAt,
	)
	if err != nil {
		return RelationEdge{}, err
	}

	s.recordAssertProvenance(ctx, edge)
	if edge.RelationKind == "contradicts" {
		s.emitConflictDetectedSignal(ctx, edge)
	}
	return edge, nil
}

// emitConflictDetectedSignal fires D85's conflict-detected structural Signal
// whenever a contradicts edge is written — via MCPAssertRelation,
// MCPProposeRelation, or MCPObserveRelation, all of which funnel through
// insertEdge above; a system-witnessed contradiction is just as real as a
// human- or agent-asserted one, so all three edge classes fire this,
// deliberately not narrowed to asserted/inferred alone (01a0dd64-2,
// architect-approved).
//
// Two rows are written, one per side (D86 point 3: two ordinary
// single-receiver rows rather than a second subject-column pair), each
// naming the *other* Decision as subject_id — the item this Signal's own
// receiver did not just author the contradiction on. receiver is
// decisionRatifyOperation for both rows: contradicts is always Decision↔
// Decision (enforced by its own TypePairs), so both sides need the same
// ratification authority. This is a known, deliberately deferred
// simplification — it broadcasts to every holder of that operation
// globally, not narrowed to either Decision's own Domain, since no
// per-Domain Signal channel convention exists yet (signal-patterns-v1.md
// §5 names this as its own future design pass, not this Task's scope).
//
// Best-effort and fail-open on the dispatch half only, matching
// recordAuthorizationRequiredSignal's own contract: a DB error here is
// logged, never returned, since a failed structural-Signal emission must
// never fail the relation assertion itself.
func (s *RelationStore) emitConflictDetectedSignal(ctx context.Context, edge RelationEdge) {
	pairs := [2]struct{ subjectID string }{{edge.TargetID}, {edge.SourceID}}
	for _, pair := range pairs {
		id := NewID()
		now := time.Now().UTC()
		message := fmt.Sprintf("Decision %s contradicts Decision %s", edge.SourceID, edge.TargetID)
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO smeldr_signals
				(id, slug, status, created_at, updated_at, sender, receiver, signal_type, message, task_ref, sequence,
				 subject_type, subject_id)
			VALUES
				($1, $2, 'pending', $3, $4, 'system', $5, 'conflict-detected', $6, '', 0,
				 'Decision', $7)`,
			id, id, now, now, decisionRatifyOperation, message, pair.subjectID,
		)
		if err != nil {
			slog.WarnContext(ctx, "smeldr: emitConflictDetectedSignal: insert failed",
				"edge_id", edge.ID, "subject_id", pair.subjectID, "error", err)
			continue
		}
		dispatchTransitionWebhook(ctx, s.webhookStore, s.webhookPool, s.eventBroadcaster, decisionRatifyOperation, "signal.created", transitionWebhookData{
			Type: "signal", ID: id, Slug: id, ToState: "pending",
		})
	}
}

// recordAssertProvenance records a ProvenanceRecord for a successfully asserted
// edge (T149). Fail-open and best-effort in two independent ways: does nothing
// if s.provenanceStore is nil (App.Provenance was never wired), and recovers the
// actor only when ctx is a concrete smeldr.Context — Assert/MCPAssertRelation/
// MCPProposeRelation are declared with plain context.Context (no .User()
// accessor), but every real caller today (smeldr.dev/mcp's tool handlers, and
// the one internal caller in applyConflictPolicy's supersede path) passes the
// original smeldr.Context through unwrapped, so the type assertion recovers it
// in practice without requiring any change to these methods' public signatures.
// See design/transition-provenance.md §5.2 for the full reasoning.
func (s *RelationStore) recordAssertProvenance(ctx context.Context, edge RelationEdge) {
	if s.provenanceStore == nil {
		return
	}
	var actorID string
	var roles []Role
	if sc, ok := ctx.(Context); ok {
		actorID = sc.User().ID
		roles = sc.User().Roles
	}
	actorKind := actorKindFor(actorID, roles)
	if edge.CreatedByJob != nil && *edge.CreatedByJob != "" {
		actorKind = "job"
		actorID = *edge.CreatedByJob
	}
	recordProvenance(ctx, s.provenanceStore, ProvenanceRecord{
		SubjectType: "RelationEdge",
		SubjectID:   edge.ID,
		Verb:        "assert",
		ActorKind:   actorKind,
		ActorID:     actorID,
	})
}

// MCPAssertRelation manually asserts a typed edge between two content items.
// Returns ErrNotFound when relationKind is not registered. The edge is stored
// with edge_class="asserted" and the populated RelationEdge is returned.
func (s *RelationStore) MCPAssertRelation(ctx context.Context,
	sourceType, sourceID, targetType, targetID, relationKind string,
	confidence *float64, validAt, invalidAt *time.Time,
	attributes json.RawMessage,
) (RelationEdge, error) {
	if _, ok := s.GetKind(relationKind); !ok {
		return RelationEdge{}, ErrNotFound
	}
	return s.insertEdge(ctx, RelationEdge{
		SourceType:   sourceType,
		SourceID:     sourceID,
		TargetType:   targetType,
		TargetID:     targetID,
		RelationKind: relationKind,
		EdgeClass:    "asserted",
		Confidence:   confidence,
		ValidAt:      validAt,
		InvalidAt:    invalidAt,
		Attributes:   attributes,
	})
}

// MCPProposeRelation records an inferred edge for human or agent review.
// Identical to MCPAssertRelation but stores edge_class="inferred". The edge is
// NOT automatically asserted — it remains pending review.
func (s *RelationStore) MCPProposeRelation(ctx context.Context,
	sourceType, sourceID, targetType, targetID, relationKind string,
	confidence *float64, validAt, invalidAt *time.Time,
	attributes json.RawMessage,
) (RelationEdge, error) {
	if _, ok := s.GetKind(relationKind); !ok {
		return RelationEdge{}, ErrNotFound
	}
	return s.insertEdge(ctx, RelationEdge{
		SourceType:   sourceType,
		SourceID:     sourceID,
		TargetType:   targetType,
		TargetID:     targetID,
		RelationKind: relationKind,
		EdgeClass:    "inferred",
		Confidence:   confidence,
		ValidAt:      validAt,
		InvalidAt:    invalidAt,
		Attributes:   attributes,
	})
}

// MCPObserveRelation records an edge a system directly witnessed (e.g. via an
// inbound integration), for example a webhook-reported fact rather than a human's
// direct claim or an agent's inference. Identical to MCPAssertRelation and
// MCPProposeRelation but stores edge_class="observed". Excluded from
// governance.go's asserted-only dynamic-scope check and from
// RecomputeAsserted/BulkRecompute's asserted-only diff scope, by design
// (design/edge-class-observed-spike.md) — a system-witnessed fact is not
// automatically the same trust tier as a deliberate human grant, and is not
// subject to Layer 1's reference-field-driven reconciliation.
func (s *RelationStore) MCPObserveRelation(ctx context.Context,
	sourceType, sourceID, targetType, targetID, relationKind string,
	confidence *float64, validAt, invalidAt *time.Time,
	attributes json.RawMessage,
) (RelationEdge, error) {
	if _, ok := s.GetKind(relationKind); !ok {
		return RelationEdge{}, ErrNotFound
	}
	return s.insertEdge(ctx, RelationEdge{
		SourceType:   sourceType,
		SourceID:     sourceID,
		TargetType:   targetType,
		TargetID:     targetID,
		RelationKind: relationKind,
		EdgeClass:    "observed",
		Confidence:   confidence,
		ValidAt:      validAt,
		InvalidAt:    invalidAt,
		Attributes:   attributes,
	})
}

// MCPGetRelations queries the relation graph for a given item.
// direction must be "source", "target", or "both".
// kind filters by relation kind; empty string returns all kinds.
func (s *RelationStore) MCPGetRelations(ctx context.Context, typeName, id, direction, kind string) ([]RelationEdge, error) {
	switch direction {
	case "source":
		return s.GetBySource(ctx, typeName, id, kind)
	case "target":
		return s.GetByTarget(ctx, typeName, id, kind)
	case "both":
		src, err := s.GetBySource(ctx, typeName, id, kind)
		if err != nil {
			return nil, err
		}
		tgt, err := s.GetByTarget(ctx, typeName, id, kind)
		if err != nil {
			return nil, err
		}
		return append(src, tgt...), nil
	default:
		return nil, fmt.Errorf("smeldr: MCPGetRelations: direction must be source, target, or both; got %q", direction)
	}
}

// MCPPreviewImpact returns which items would receive an AfterRelationCascade
// signal if the given item changed status — without firing any signals.
// Returns the source-side dependents found via GetByTarget.
func (s *RelationStore) MCPPreviewImpact(ctx context.Context, typeName, id string) ([]RelationEdge, error) {
	return s.GetByTarget(ctx, typeName, id, "")
}

// MCPUpsertRelationKind registers or updates a relation kind via MCP.
// Delegates to UpsertKind (which validates and persists), then returns
// the stored RelationKindDef from the in-memory registry.
func (s *RelationStore) MCPUpsertRelationKind(ctx context.Context, def RelationKindDef) (RelationKindDef, error) {
	if err := s.UpsertKind(ctx, def); err != nil {
		return RelationKindDef{}, err
	}
	stored, _ := s.GetKind(def.TypeName)
	return stored, nil
}

// MCPListRelationKinds returns all registered relation kinds sorted by type_name.
// Thin wrapper over ListKinds so mcp has a uniform MCPXxx naming convention.
func (s *RelationStore) MCPListRelationKinds() []RelationKindDef {
	return s.ListKinds()
}

// GetBySource returns all edges where source_type and source_id match.
// If kind is non-empty, only edges with that relation_kind are returned.
func (s *RelationStore) GetBySource(ctx context.Context, sourceType, sourceID, kind string) ([]RelationEdge, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if kind == "" {
		rows, err = s.db.QueryContext(ctx,
			"SELECT "+relationColumns+" FROM smeldr_relations WHERE source_type=$1 AND source_id=$2",
			sourceType, sourceID)
	} else {
		rows, err = s.db.QueryContext(ctx,
			"SELECT "+relationColumns+" FROM smeldr_relations WHERE source_type=$1 AND source_id=$2 AND relation_kind=$3",
			sourceType, sourceID, kind)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectEdges(rows)
}

// GetByTarget returns all edges where target_type and target_id match.
// If kind is non-empty, only edges with that relation_kind are returned.
func (s *RelationStore) GetByTarget(ctx context.Context, targetType, targetID, kind string) ([]RelationEdge, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if kind == "" {
		rows, err = s.db.QueryContext(ctx,
			"SELECT "+relationColumns+" FROM smeldr_relations WHERE target_type=$1 AND target_id=$2",
			targetType, targetID)
	} else {
		rows, err = s.db.QueryContext(ctx,
			"SELECT "+relationColumns+" FROM smeldr_relations WHERE target_type=$1 AND target_id=$2 AND relation_kind=$3",
			targetType, targetID, kind)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectEdges(rows)
}

// Delete removes a relation edge by ID. No-op if the ID does not exist.
func (s *RelationStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM smeldr_relations WHERE id=$1", id)
	return err
}

func collectEdges(rows *sql.Rows) ([]RelationEdge, error) {
	var out []RelationEdge
	for rows.Next() {
		e, err := scanEdge(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func scanRelationKind(rows *sql.Rows) (RelationKindDef, error) {
	var d RelationKindDef
	var directional, weighted int
	var typePairs, attributes string
	err := rows.Scan(
		&d.ID, &d.TypeName, &d.Label, &d.ReverseLabel, &d.Mode,
		&directional, &weighted,
		&typePairs, &attributes,
		scanDest(&d.CreatedAt), scanDest(&d.UpdatedAt),
	)
	if err != nil {
		return RelationKindDef{}, err
	}
	d.Directional = directional != 0
	d.Weighted = weighted != 0
	d.TypePairs = json.RawMessage(typePairs)
	d.Attributes = json.RawMessage(attributes)
	return d, nil
}

func scanEdge(rows *sql.Rows) (RelationEdge, error) {
	var e RelationEdge
	var confidence sql.NullFloat64
	var createdByJob sql.NullString
	var createdBy sql.NullString
	var attributes string
	err := rows.Scan(
		&e.ID, &e.SourceType, &e.SourceID,
		&e.TargetType, &e.TargetID,
		&e.RelationKind, &e.EdgeClass,
		&confidence, nullTimeScanner{dst: &e.ValidAt}, nullTimeScanner{dst: &e.InvalidAt},
		&createdByJob,
		&createdBy,
		&attributes,
		scanDest(&e.CreatedAt), scanDest(&e.UpdatedAt),
		nullTimeScanner{dst: &e.LastConfirmedAt},
	)
	if err != nil {
		return RelationEdge{}, err
	}
	if confidence.Valid {
		e.Confidence = &confidence.Float64
	}
	if createdByJob.Valid {
		e.CreatedByJob = &createdByJob.String
	}
	if createdBy.Valid {
		e.CreatedBy = &createdBy.String
	}
	e.Attributes = json.RawMessage(attributes)
	return e, nil
}

func intOf(b bool) int {
	if b {
		return 1
	}
	return 0
}

// RelationSource carries the source identity and current set of incoming
// asserted edges for one content item. Used by [RelationStore.BulkRecompute].
type RelationSource struct {
	SourceType string
	SourceID   string
	Incoming   []RelationEdge
}

// RecomputeAsserted performs a differential update of the asserted edges for
// one content item. It selects the current asserted rows, diffs against
// incoming, and applies only the delta (delete stale, insert new).
//
// Key: (target_type, target_id, relation_kind). Returns nil immediately when
// the diff is empty — the common case costs exactly one SELECT and zero writes.
// Runs inside a transaction when the DB supports BeginTx; falls back to
// sequential writes otherwise.
func (s *RelationStore) RecomputeAsserted(ctx context.Context, sourceType, sourceID string, incoming []RelationEdge) error {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+relationColumns+
			" FROM smeldr_relations WHERE source_type=$1 AND source_id=$2 AND edge_class='asserted'",
		sourceType, sourceID)
	if err != nil {
		return err
	}
	currentEdges, err := collectEdges(rows)
	rows.Close()
	if err != nil {
		return err
	}

	toDelete, toInsert := computeRelationDiff(currentEdges, incoming)
	if len(toDelete) == 0 && len(toInsert) == 0 {
		return nil
	}
	return s.applyRelationDiff(ctx, s.db, toDelete, toInsert, sourceType, sourceID)
}

// BulkRecompute applies [RelationStore.RecomputeAsserted] to a batch of items.
// All SELECTs are performed first, then all writes are applied — efficient for
// post-import scenarios where per-save overhead would accumulate.
// Call it explicitly after bulk import; it is not called from the save path.
func (s *RelationStore) BulkRecompute(ctx context.Context, items []RelationSource) error {
	type diff struct {
		sourceType string
		sourceID   string
		toDelete   []string
		toInsert   []RelationEdge
	}

	diffs := make([]diff, 0, len(items))
	for _, src := range items {
		rows, err := s.db.QueryContext(ctx,
			"SELECT "+relationColumns+
				" FROM smeldr_relations WHERE source_type=$1 AND source_id=$2 AND edge_class='asserted'",
			src.SourceType, src.SourceID)
		if err != nil {
			return err
		}
		current, err := collectEdges(rows)
		rows.Close()
		if err != nil {
			return err
		}
		td, ti := computeRelationDiff(current, src.Incoming)
		if len(td) > 0 || len(ti) > 0 {
			diffs = append(diffs, diff{src.SourceType, src.SourceID, td, ti})
		}
	}

	for _, d := range diffs {
		if err := s.applyRelationDiff(ctx, s.db, d.toDelete, d.toInsert, d.sourceType, d.sourceID); err != nil {
			return err
		}
	}
	return nil
}

// TargetChecker reports whether a relation target is still live (published and not
// hard-deleted). Error means the check could not be performed — SweepStructural counts
// that relation as skipped, not flagged.
type TargetChecker func(ctx context.Context, targetType, targetID string) (alive bool, err error)

// SweepStructural iterates all active relations (invalid_at IS NULL OR invalid_at > now,
// AND valid_at IS NULL OR valid_at <= now) and calls check for each unique target AND
// each unique source (D61, item 2 — the sweep previously checked target existence only;
// an edge whose source was deleted was never revisited). When either side is not alive,
// the sweep sets invalid_at = now on the edge and calls onStale(ctx, edge) — once per
// edge even if both its source and target are dead, not twice.
// Returns (walked, flagged, skipped, error): walked = total relation rows examined
// (T223 — the count that makes flagged/skipped meaningful: without it, "flagged=0,
// skipped=0" cannot be told apart from "nothing to check" versus "checked everything,
// found nothing"); flagged = relations whose source or target was not alive; skipped =
// relations where check returned an error for their target and/or source (logged, not
// fatal — an edge whose target check fails but whose source check flags it is still
// counted flagged, not skipped); error = only on fatal DB errors that abort the sweep.
func (s *RelationStore) SweepStructural(
	ctx context.Context,
	check TargetChecker,
	onStale func(ctx context.Context, edge RelationEdge),
) (walked int, flagged int, skipped int, err error) {
	now := time.Now().UTC()
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+relationColumns+" FROM smeldr_relations "+
			"WHERE (invalid_at IS NULL OR invalid_at > $1) "+
			"AND (valid_at IS NULL OR valid_at <= $2)",
		now, now,
	)
	if err != nil {
		return 0, 0, 0, err
	}
	defer rows.Close()

	type sideKey struct{ typ, id string }
	var allEdges []RelationEdge
	byTarget := map[sideKey][]RelationEdge{}
	bySource := map[sideKey][]RelationEdge{}
	for rows.Next() {
		e, scanErr := scanEdge(rows)
		if scanErr != nil {
			return 0, 0, 0, scanErr
		}
		walked++
		allEdges = append(allEdges, e)
		byTarget[sideKey{e.TargetType, e.TargetID}] = append(byTarget[sideKey{e.TargetType, e.TargetID}], e)
		bySource[sideKey{e.SourceType, e.SourceID}] = append(bySource[sideKey{e.SourceType, e.SourceID}], e)
	}
	if err := rows.Err(); err != nil {
		return walked, 0, 0, err
	}

	// skippedKeys records every side (as target or as source) whose liveness
	// check errored — a checked-and-alive side and a could-not-check side must
	// never collapse into the same "confirmed" state below.
	skippedKeys := map[sideKey]bool{}

	deadTargets := map[sideKey]bool{}
	for k := range byTarget {
		alive, checkErr := check(ctx, k.typ, k.id)
		if checkErr != nil {
			slog.WarnContext(ctx, "SweepStructural: target check error",
				"target_type", k.typ, "target_id", k.id, "err", checkErr)
			skipped++
			skippedKeys[k] = true
			continue
		}
		if !alive {
			deadTargets[k] = true
		}
	}
	deadSources := map[sideKey]bool{}
	for k := range bySource {
		alive, checkErr := check(ctx, k.typ, k.id)
		if checkErr != nil {
			slog.WarnContext(ctx, "SweepStructural: source check error",
				"source_type", k.typ, "source_id", k.id, "err", checkErr)
			skipped++
			skippedKeys[k] = true
			continue
		}
		if !alive {
			deadSources[k] = true
		}
	}

	staled := map[string]bool{} // edge ID — flagged once even if both sides are dead
	flagEdge := func(e RelationEdge) error {
		if staled[e.ID] {
			return nil
		}
		if _, updateErr := s.db.ExecContext(ctx,
			"UPDATE smeldr_relations SET invalid_at=$1 WHERE id=$2",
			now, e.ID,
		); updateErr != nil {
			return updateErr
		}
		e.InvalidAt = &now
		onStale(ctx, e)
		staled[e.ID] = true
		flagged++
		return nil
	}

	for k := range deadTargets {
		for _, e := range byTarget[k] {
			if err := flagEdge(e); err != nil {
				return walked, flagged, skipped, err
			}
		}
	}
	for k := range deadSources {
		for _, e := range bySource[k] {
			if err := flagEdge(e); err != nil {
				return walked, flagged, skipped, err
			}
		}
	}

	// Every edge that was neither flagged nor left unresolved by a skipped
	// check — i.e. both its source and target were actually checked and
	// found alive — gets last_confirmed_at advanced to now. One batched
	// UPDATE per sweep run, not one per edge: this write now touches the
	// dominant case each run (everything still alive), unlike flagEdge's
	// per-row UPDATE above, which only ever touches the typically-small
	// flagged set.
	confirmedIDs := make([]string, 0, len(allEdges))
	for _, e := range allEdges {
		if staled[e.ID] {
			continue
		}
		if skippedKeys[sideKey{e.TargetType, e.TargetID}] || skippedKeys[sideKey{e.SourceType, e.SourceID}] {
			continue
		}
		confirmedIDs = append(confirmedIDs, e.ID)
	}
	if len(confirmedIDs) > 0 {
		if err := s.confirmEdges(ctx, confirmedIDs, now); err != nil {
			return walked, flagged, skipped, err
		}
	}

	return walked, flagged, skipped, nil
}

// confirmEdges sets last_confirmed_at = at on every edge in ids, in one
// batched UPDATE. Called only by SweepStructural for edges whose source and
// target were both checked and found alive this run.
func (s *RelationStore) confirmEdges(ctx context.Context, ids []string, at time.Time) error {
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+1)
	args = append(args, at)
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+2)
		args = append(args, id)
	}
	query := "UPDATE smeldr_relations SET last_confirmed_at=$1 WHERE id IN (" +
		strings.Join(placeholders, ",") + ")"
	_, err := s.db.ExecContext(ctx, query, args...)
	return err
}

// computeRelationDiff returns the IDs to delete and edges to insert given the
// current set and the desired incoming set. Key: (target_type, target_id, relation_kind).
func computeRelationDiff(current, incoming []RelationEdge) (toDelete []string, toInsert []RelationEdge) {
	type key struct{ tt, tid, kind string }

	cur := make(map[key]string, len(current))
	for _, e := range current {
		cur[key{e.TargetType, e.TargetID, e.RelationKind}] = e.ID
	}
	inc := make(map[key]RelationEdge, len(incoming))
	for _, e := range incoming {
		inc[key{e.TargetType, e.TargetID, e.RelationKind}] = e
	}

	for k, id := range cur {
		if _, exists := inc[k]; !exists {
			toDelete = append(toDelete, id)
		}
	}
	for k, e := range inc {
		if _, exists := cur[k]; !exists {
			toInsert = append(toInsert, e)
		}
	}
	return
}

type txBeginner interface {
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

type edgeExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// applyRelationDiff deletes toDelete IDs and inserts toInsert edges for the
// given source, stamping SourceType/SourceID/EdgeClass/timestamps on inserts.
// Wraps in a transaction when exec implements BeginTx.
func (s *RelationStore) applyRelationDiff(ctx context.Context, db edgeExecer, toDelete []string, toInsert []RelationEdge, sourceType, sourceID string) error {
	var exec edgeExecer = db
	commit := func() error { return nil }

	if txdb, ok := s.db.(txBeginner); ok {
		tx, err := txdb.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback() //nolint:errcheck
		exec = tx
		commit = tx.Commit
	}

	for _, id := range toDelete {
		if _, err := exec.ExecContext(ctx, "DELETE FROM smeldr_relations WHERE id=$1", id); err != nil {
			return err
		}
	}

	now := time.Now().UTC()
	for _, e := range toInsert {
		if e.ID == "" {
			e.ID = NewID()
		}
		e.SourceType = sourceType
		e.SourceID = sourceID
		e.EdgeClass = "asserted"
		if e.CreatedAt.IsZero() {
			e.CreatedAt = now
		}
		e.UpdatedAt = now
		if e.Attributes == nil {
			e.Attributes = json.RawMessage("{}")
		}
		_, err := exec.ExecContext(ctx,
			"INSERT INTO smeldr_relations ("+relationInsertColumns+") VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)",
			e.ID, e.SourceType, e.SourceID,
			e.TargetType, e.TargetID,
			e.RelationKind, e.EdgeClass,
			e.Confidence, e.ValidAt, e.InvalidAt,
			e.CreatedByJob, e.CreatedBy, string(e.Attributes),
			e.CreatedAt, e.UpdatedAt,
		)
		if err != nil {
			return err
		}
	}

	return commit()
}

// buildCascadeHandler returns a func suitable for [App.OnSignal] that implements
// Layer 2 one-hop reactive cascade. When a target item changes status (published,
// archived, deleted, unpublished), the handler looks up all source-side dependents
// via GetByTarget and fires [AfterRelationCascade] for each unique source item.
//
// Guards applied per call:
//   - visited-set: each (sourceType, sourceID) pair is notified at most once.
//   - idempotency-set: each edge is processed at most once per run.
//   - depth = 1: only direct dependents are notified; transitive cascades are
//     not triggered because buildCascadeHandler subscribes to status-change
//     signals only, not to [AfterRelationCascade] itself.
//
// Debouncing: cascade events are debounced 500 ms per source item via a
// sync.Map of per-source debouncers. Multiple rapid-fire edges pointing from
// the same source to different targets yield a single [AfterRelationCascade].
func buildCascadeHandler(store *RelationStore, app *App) func(context.Context, SignalEvent) error {
	var debouncers sync.Map // "sourceType:sourceID" → *debouncer
	return func(ctx context.Context, ev SignalEvent) error {
		edges, err := store.GetByTarget(ctx, ev.Type, ev.NodeID, "")
		if err != nil {
			return err
		}
		if len(edges) == 0 {
			return nil
		}
		triggerSig := string(ev.PreviousState)
		baseCtx := context.WithoutCancel(ctx)
		visited := make(map[string]struct{})
		seen := make(map[string]struct{})
		for _, edge := range edges {
			visitKey := edge.SourceType + ":" + edge.SourceID
			idempKey := edge.ID + ":" + triggerSig
			if _, ok := visited[visitKey]; ok {
				continue
			}
			if _, ok := seen[idempKey]; ok {
				continue
			}
			visited[visitKey] = struct{}{}
			seen[idempKey] = struct{}{}

			cascadeEv := SignalEvent{
				Type:          edge.SourceType,
				NodeID:        edge.SourceID,
				PreviousState: triggerSig,
				ActorID:       ev.NodeID,
				Timestamp:     time.Now(),
			}
			key := visitKey
			d, _ := debouncers.LoadOrStore(key, newDebouncer(500*time.Millisecond, func() {
				debouncers.Delete(key)
				app.emitSignal(baseCtx, AfterRelationCascade, cascadeEv)
			}))
			d.(*debouncer).Trigger()
		}
		return nil
	}
}
