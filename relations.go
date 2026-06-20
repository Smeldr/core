package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"
)

// RelationKindDef describes a named category of typed edge in the relation graph.
// It governs whether edges are asserted (operator) or derived/inferable (agent/rule),
// whether they are directional, and which source→target type pairs are valid.
type RelationKindDef struct {
	ID          string          `db:"id"`
	TypeName    string          `db:"type_name"`
	Label       string          `db:"label"`
	Mode        string          `db:"mode"` // "derived" | "asserted" | "inferable"
	Directional bool            `db:"directional"`
	Weighted    bool            `db:"weighted"`
	TypePairs   json.RawMessage `db:"type_pairs"` // JSON: [{source_type, target_type}]
	Attributes  json.RawMessage `db:"attributes"`
	CreatedAt   time.Time       `db:"created_at"`
	UpdatedAt   time.Time       `db:"updated_at"`
}

// RelationEdge is a single typed adjacency between two content items.
// It does not embed Node — relations are graph edges, not content items.
type RelationEdge struct {
	ID           string          `db:"id"`
	SourceType   string          `db:"source_type"`
	SourceID     string          `db:"source_id"`
	TargetType   string          `db:"target_type"`
	TargetID     string          `db:"target_id"`
	RelationKind string          `db:"relation_kind"`
	EdgeClass    string          `db:"edge_class"` // "asserted" | "inferred"
	Confidence   *float64        `db:"confidence"`
	ValidAt      *time.Time      `db:"valid_at"`
	InvalidAt    *time.Time      `db:"invalid_at"`
	CreatedByJob *string         `db:"created_by_job"`
	Attributes   json.RawMessage `db:"attributes"`
	CreatedAt    time.Time       `db:"created_at"`
	UpdatedAt    time.Time       `db:"updated_at"`
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
}

// Column order constants — scan order must match SELECT order exactly.
const relationKindColumns = `id, type_name, label, mode, directional, weighted, type_pairs, attributes, created_at, updated_at`
const relationColumns = `id, source_type, source_id, target_type, target_id, relation_kind, edge_class, confidence, valid_at, invalid_at, created_by_job, attributes, created_at, updated_at`

// CreateRelationTables creates the smeldr_relation_kinds and smeldr_relations tables and
// their indexes if they do not already exist. Idempotent — safe to call on every boot.
func CreateRelationTables(db DB) error {
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS smeldr_relation_kinds (
    id            TEXT NOT NULL PRIMARY KEY,
    type_name     TEXT NOT NULL UNIQUE,
    label         TEXT NOT NULL DEFAULT '',
    mode          TEXT NOT NULL,
    directional   INTEGER NOT NULL DEFAULT 1,
    weighted      INTEGER NOT NULL DEFAULT 0,
    type_pairs    TEXT NOT NULL DEFAULT '[]',
    attributes    TEXT NOT NULL DEFAULT '{}',
    created_at    DATETIME NOT NULL,
    updated_at    DATETIME NOT NULL
)`); err != nil {
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
    valid_at        DATETIME,
    invalid_at      DATETIME,
    created_by_job  TEXT,
    attributes      TEXT NOT NULL DEFAULT '{}',
    created_at      DATETIME NOT NULL,
    updated_at      DATETIME NOT NULL
)`); err != nil {
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
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (type_name) DO UPDATE SET
    label       = EXCLUDED.label,
    mode        = EXCLUDED.mode,
    directional = EXCLUDED.directional,
    weighted    = EXCLUDED.weighted,
    type_pairs  = EXCLUDED.type_pairs,
    attributes  = EXCLUDED.attributes,
    updated_at  = EXCLUDED.updated_at`,
		def.ID, def.TypeName, def.Label, def.Mode,
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

// Assert inserts or updates an asserted edge in smeldr_relations.
// The relation_kind must be registered and edge_class must be "asserted".
func (s *RelationStore) Assert(ctx context.Context, edge RelationEdge) error {
	if _, ok := s.GetKind(edge.RelationKind); !ok {
		return Err("relation_kind", fmt.Sprintf("unknown relation kind %q", edge.RelationKind))
	}
	if edge.EdgeClass != "asserted" {
		return Err("edge_class", "Assert only accepts edge_class=asserted")
	}

	now := time.Now().UTC()
	if edge.ID == "" {
		edge.ID = NewID()
	}
	if edge.CreatedAt.IsZero() {
		edge.CreatedAt = now
	}
	edge.UpdatedAt = now
	if edge.Attributes == nil {
		edge.Attributes = json.RawMessage("{}")
	}

	_, err := s.db.ExecContext(ctx, `
INSERT INTO smeldr_relations (`+relationColumns+`)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
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
    attributes     = EXCLUDED.attributes,
    updated_at     = EXCLUDED.updated_at`,
		edge.ID, edge.SourceType, edge.SourceID,
		edge.TargetType, edge.TargetID,
		edge.RelationKind, edge.EdgeClass,
		edge.Confidence, edge.ValidAt, edge.InvalidAt,
		edge.CreatedByJob, string(edge.Attributes),
		edge.CreatedAt, edge.UpdatedAt,
	)
	return err
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
		&d.ID, &d.TypeName, &d.Label, &d.Mode,
		&directional, &weighted,
		&typePairs, &attributes,
		&d.CreatedAt, &d.UpdatedAt,
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
	var validAt, invalidAt sql.NullTime
	var createdByJob sql.NullString
	var attributes string
	err := rows.Scan(
		&e.ID, &e.SourceType, &e.SourceID,
		&e.TargetType, &e.TargetID,
		&e.RelationKind, &e.EdgeClass,
		&confidence, &validAt, &invalidAt,
		&createdByJob,
		&attributes,
		&e.CreatedAt, &e.UpdatedAt,
	)
	if err != nil {
		return RelationEdge{}, err
	}
	if confidence.Valid {
		e.Confidence = &confidence.Float64
	}
	if validAt.Valid {
		e.ValidAt = &validAt.Time
	}
	if invalidAt.Valid {
		e.InvalidAt = &invalidAt.Time
	}
	if createdByJob.Valid {
		e.CreatedByJob = &createdByJob.String
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
