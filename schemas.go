package smeldr

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

// SchemaField describes one field within a [ContentTypeSchema]. The Type uses
// JSON Schema type names ("string", "integer", "boolean", "array", "object").
// Format is an optional hint for human operators and tool generators
// ("markdown", "url", "html"). Required marks fields that must be present on
// every create call.
type SchemaField struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Format      string `json:"format,omitempty"`
	Description string `json:"description,omitempty"`
	// Role identifies the semantic role of this field: "title", "description",
	// "og_image", "body", or "summary". At most one field per role per schema.
	// Drives auto-slug generation (title), ContentList cards, and T72 head derivation.
	Role string `json:"role,omitempty"`
	// Relation is a placeholder for T06 edge-backed semantic links between types.
	// Set to "edge" when this field points to another content type reactively.
	Relation string `json:"relation,omitempty"`
}

// ContentTypeSchema holds the declared field definitions for one content type.
// Schemas for the 16 canonical block types are seeded at startup via
// [SeedBlockTypeSchemas]. Runtime-defined schemas (Kind "content") are written
// via [SchemaStore.Save] when [App.DefineContentType] is called.
//
// URLPrefix is the operator-chosen public URL prefix (e.g. "/recipes"). When
// empty, the type has no public URL and is accessible only via admin routes.
type ContentTypeSchema struct {
	ID        string          `db:"id"`
	TypeName  string          `db:"type_name"`
	Label     string          `db:"label"`
	Kind      string          `db:"kind"`       // "block" | "content"; default "block"
	URLPrefix string          `db:"url_prefix"` // public URL prefix; empty = admin-only
	Fields    json.RawMessage `db:"fields"`
	CreatedAt time.Time       `db:"created_at"`
	UpdatedAt time.Time       `db:"updated_at"`
}

// ParseFields decodes the stored JSON fields into a slice of [SchemaField].
func (s *ContentTypeSchema) ParseFields() ([]SchemaField, error) {
	var fields []SchemaField
	if err := json.Unmarshal(s.Fields, &fields); err != nil {
		return nil, err
	}
	return fields, nil
}

// SchemaStore reads block-type schemas from [smeldr_content_type_schemas].
// Create one with [NewSchemaStore] after calling [CreateSchemaTable].
type SchemaStore struct {
	db DB
}

// NewSchemaStore returns a SchemaStore backed by db.
func NewSchemaStore(db DB) *SchemaStore { return &SchemaStore{db: db} }

// FindByTypeName returns the schema for typeName, or [ErrNotFound] when no
// schema is registered for that type.
func (s *SchemaStore) FindByTypeName(ctx context.Context, typeName string) (*ContentTypeSchema, error) {
	rows, err := Query[*ContentTypeSchema](ctx, s.db,
		"SELECT * FROM smeldr_content_type_schemas WHERE type_name = $1", typeName)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	return rows[0], nil
}

// All returns all registered schemas ordered by type_name.
func (s *SchemaStore) All(ctx context.Context) ([]*ContentTypeSchema, error) {
	rows, err := Query[*ContentTypeSchema](ctx, s.db,
		"SELECT * FROM smeldr_content_type_schemas ORDER BY type_name")
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []*ContentTypeSchema{}
	}
	return rows, nil
}

// AllByKind returns all schemas with the given kind ("block" | "content"),
// ordered by type_name.
func (s *SchemaStore) AllByKind(ctx context.Context, kind string) ([]*ContentTypeSchema, error) {
	rows, err := Query[*ContentTypeSchema](ctx, s.db,
		"SELECT * FROM smeldr_content_type_schemas WHERE kind = $1 ORDER BY type_name", kind)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []*ContentTypeSchema{}
	}
	return rows, nil
}

// Save upserts schema into smeldr_content_type_schemas keyed on type_name.
// On insert, ID and CreatedAt are set when zero. On conflict the label, kind,
// url_prefix, fields, and updated_at are updated while id and created_at are
// preserved.
func (s *SchemaStore) Save(ctx context.Context, schema *ContentTypeSchema) error {
	if schema.ID == "" {
		schema.ID = NewID()
	}
	now := time.Now().UTC()
	schema.UpdatedAt = now
	if schema.CreatedAt.IsZero() {
		schema.CreatedAt = now
	}
	kind := schema.Kind
	if kind == "" {
		kind = "block"
	}
	fields := schema.Fields
	if len(fields) == 0 {
		fields = json.RawMessage("[]")
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO smeldr_content_type_schemas (id, type_name, label, kind, url_prefix, fields, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT(type_name) DO UPDATE SET
    label      = excluded.label,
    kind       = excluded.kind,
    url_prefix = excluded.url_prefix,
    fields     = excluded.fields,
    updated_at = excluded.updated_at`,
		schema.ID, schema.TypeName, schema.Label, kind, schema.URLPrefix,
		fields, schema.CreatedAt, schema.UpdatedAt)
	return err
}

// CreateSchemaTable creates smeldr_content_type_schemas if it does not exist.
// Call once at startup alongside [CreateBlockTables]. Idempotent.
func CreateSchemaTable(db DB) error {
	_, err := db.ExecContext(context.Background(), `
CREATE TABLE IF NOT EXISTS smeldr_content_type_schemas (
	id         TEXT NOT NULL PRIMARY KEY,
	type_name  TEXT NOT NULL UNIQUE,
	label      TEXT NOT NULL DEFAULT '',
	kind       TEXT NOT NULL DEFAULT 'block',
	url_prefix TEXT NOT NULL DEFAULT '',
	fields     TEXT NOT NULL DEFAULT '[]',
	created_at DATETIME NOT NULL,
	updated_at DATETIME NOT NULL
)`)
	return err
}

// MigrateURLPrefixColumn adds the url_prefix column to
// smeldr_content_type_schemas when it is absent. Idempotent; safe to call on
// every boot. A no-op on non-SQLite databases that do not support PRAGMA.
func MigrateURLPrefixColumn(db DB) error {
	ctx := context.Background()
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(smeldr_content_type_schemas)")
	if err != nil {
		return nil // non-SQLite; assume schema is current
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var dflt *string
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			continue
		}
		if name == "url_prefix" {
			return nil // already present
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx,
		`ALTER TABLE smeldr_content_type_schemas ADD COLUMN url_prefix TEXT NOT NULL DEFAULT ''`)
	return err
}

// MigrateSchemaKindColumn adds the kind column to smeldr_content_type_schemas
// when it is missing. Safe to call on every boot; no-op when the column exists
// or when the database does not support PRAGMA table_info (non-SQLite).
func MigrateSchemaKindColumn(db DB) error {
	ctx := context.Background()
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(smeldr_content_type_schemas)")
	if err != nil {
		return nil // non-SQLite; assume schema is current
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var dflt *string
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			continue
		}
		if name == "kind" {
			return nil // already present
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx,
		`ALTER TABLE smeldr_content_type_schemas ADD COLUMN kind TEXT NOT NULL DEFAULT 'block'`)
	return err
}

// SeedBlockTypeSchemas inserts the 16 canonical block type schemas using
// INSERT OR IGNORE so it is safe to call on every boot. Each row is skipped
// when a row with that type_name already exists, preserving any customisation.
func SeedBlockTypeSchemas(db DB) error {
	type seed struct {
		typeName string
		label    string
		fields   []SchemaField
	}

	anchorID := SchemaField{Name: "AnchorID", Type: "string"}
	layout := func(desc string) SchemaField {
		return SchemaField{Name: "Layout", Type: "string", Description: desc}
	}
	collectionBase := func(layoutDesc string) []SchemaField {
		f := []SchemaField{
			{Name: "Title", Type: "string", Required: true},
			{Name: "Subtitle", Type: "string", Format: "markdown"},
		}
		if layoutDesc != "" {
			f = append(f, layout(layoutDesc))
		}
		f = append(f, anchorID)
		return f
	}

	seeds := []seed{
		// --- Leaf types ---
		{
			typeName: "content_block",
			label:    "Content Block",
			fields: []SchemaField{
				{Name: "Title", Type: "string", Required: true},
				{Name: "Body", Type: "string", Format: "markdown"},
				{Name: "ImageID", Type: "string", Description: "ID of an Image block"},
				{Name: "Link", Type: "object", Description: "Link sub-object"},
				anchorID,
			},
		},
		{
			typeName: "image",
			label:    "Image",
			fields: []SchemaField{
				{Name: "MediaURL", Type: "string", Required: true, Format: "url"},
				{Name: "AltText", Type: "string", Required: true},
				{Name: "Title", Type: "string"},
				{Name: "Caption", Type: "string", Format: "markdown"},
				{Name: "Link", Type: "object", Description: "Link sub-object"},
				anchorID,
			},
		},
		{
			typeName: "link_item",
			label:    "Link Item",
			fields: []SchemaField{
				{Name: "Title", Type: "string", Required: true},
				{Name: "URL", Type: "string", Required: true, Format: "url"},
				{Name: "Target", Type: "string", Description: "_self or _blank"},
				{Name: "Body", Type: "string", Format: "markdown"},
				{Name: "IsCTA", Type: "boolean", Description: "Render as button vs. text link"},
				anchorID,
			},
		},
		{
			typeName: "html_block",
			label:    "HTML Block",
			fields: []SchemaField{
				{Name: "AdminLabel", Type: "string", Required: true},
				{Name: "HTML", Type: "string", Required: true, Format: "html"},
				anchorID,
			},
		},
		{
			typeName: "quote",
			label:    "Quote",
			fields: []SchemaField{
				{Name: "QuoteText", Type: "string", Required: true, Format: "markdown"},
				{Name: "Attribution", Type: "string"},
				{Name: "Context", Type: "string", Format: "markdown"},
				{Name: "Link", Type: "object", Description: "Link sub-object"},
				anchorID,
			},
		},
		{
			typeName: "contact_card",
			label:    "Contact Card",
			fields: []SchemaField{
				{Name: "Name", Type: "string", Required: true},
				{Name: "JobTitle", Type: "string"},
				{Name: "Body", Type: "string", Format: "markdown"},
				{Name: "Email", Type: "string"},
				{Name: "Phone", Type: "string"},
				{Name: "ImageID", Type: "string", Description: "ID of an Image block"},
				anchorID,
			},
		},
		{
			typeName: "faq_item",
			label:    "FAQ Item",
			fields: []SchemaField{
				{Name: "Question", Type: "string", Required: true},
				{Name: "Answer", Type: "string", Required: true, Format: "markdown"},
				anchorID,
			},
		},
		// --- Collection types ---
		{typeName: "content_grid", label: "Content Grid", fields: collectionBase("grid-2, grid-3, or list")},
		{typeName: "gallery", label: "Gallery", fields: collectionBase("gallery, carousel, or masonry")},
		{typeName: "link_collection", label: "Link Collection", fields: collectionBase("inline, list, or grid")},
		{typeName: "html_grid", label: "HTML Grid", fields: collectionBase("columns (integer)")},
		{typeName: "faq", label: "FAQ", fields: collectionBase("")},
		{typeName: "team", label: "Team", fields: collectionBase("grid-2 or grid-3")},
		// --- Structural types ---
		{
			typeName: "hero",
			label:    "Hero",
			fields: []SchemaField{
				{Name: "Headline", Type: "string", Required: true},
				{Name: "Subtext", Type: "string", Format: "markdown"},
				{Name: "ImageID", Type: "string", Description: "ID of an Image block"},
				{Name: "PrimaryLink", Type: "object", Description: "Primary CTA link sub-object"},
				{Name: "SecondaryLink", Type: "object", Description: "Secondary link sub-object"},
				anchorID,
			},
		},
		{
			typeName: "footer",
			label:    "Footer",
			fields: []SchemaField{
				{Name: "Body", Type: "string", Format: "markdown"},
				{Name: "CopyrightText", Type: "string"},
				{Name: "LinkCollectionID", Type: "string", Description: "ID of a Link Collection block"},
				anchorID,
			},
		},
		// --- Dynamic list block ---
		{
			typeName: "content_list",
			label:    "Content List",
			fields: []SchemaField{
				{Name: "Title", Type: "string"},
				{Name: "ContentType", Type: "string", Required: true, Description: "posts, stories, etc."},
				{Name: "FilterTags", Type: "array", Description: "Array of tag strings to filter by"},
				{Name: "SortField", Type: "string", Description: "published_at, created_at, or title"},
				{Name: "SortDir", Type: "string", Description: "asc or desc"},
				{Name: "Limit", Type: "integer", Description: "Maximum number of items to display"},
				{Name: "SeeMoreLink", Type: "object", Description: "Link sub-object"},
				anchorID,
			},
		},
	}

	now := time.Now().UTC()
	ctx := context.Background()
	for _, s := range seeds {
		raw, err := json.Marshal(s.fields)
		if err != nil {
			return fmt.Errorf("SeedBlockTypeSchemas: marshal %s: %w", s.typeName, err)
		}
		if _, err := db.ExecContext(ctx,
			`INSERT OR IGNORE INTO smeldr_content_type_schemas (id, type_name, label, fields, created_at, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			NewID(), s.typeName, s.label, json.RawMessage(raw), now, now,
		); err != nil {
			return fmt.Errorf("SeedBlockTypeSchemas: insert %s: %w", s.typeName, err)
		}
	}
	return nil
}

// ValidateSchemaDef checks that a ContentTypeSchema is well-formed before
// writing it to the database. Returns an error on empty TypeName, a URLPrefix
// that does not start with "/", unknown field types, or unrecognised Role values.
func ValidateSchemaDef(schema *ContentTypeSchema) error {
	if schema.TypeName == "" {
		return fmt.Errorf("smeldr: schema TypeName is required")
	}
	if schema.URLPrefix != "" && !strings.HasPrefix(schema.URLPrefix, "/") {
		return fmt.Errorf("smeldr: URLPrefix must start with \"/\", got %q", schema.URLPrefix)
	}
	if len(schema.Fields) == 0 {
		return nil
	}
	fields, err := schema.ParseFields()
	if err != nil {
		return fmt.Errorf("smeldr: schema Fields JSON invalid: %w", err)
	}
	knownTypes := map[string]bool{
		"string": true, "integer": true, "boolean": true,
		"array": true, "object": true, "number": true,
	}
	knownRoles := map[string]bool{
		"": true, "title": true, "description": true,
		"og_image": true, "body": true, "summary": true,
	}
	for _, f := range fields {
		if f.Name == "" {
			return fmt.Errorf("smeldr: schema field Name is required")
		}
		if !knownTypes[f.Type] {
			return fmt.Errorf("smeldr: schema field %q has unknown type %q", f.Name, f.Type)
		}
		if !knownRoles[f.Role] {
			return fmt.Errorf("smeldr: schema field %q has unknown role %q (valid: title, description, og_image, body, summary)", f.Name, f.Role)
		}
	}
	return nil
}

// ValidateFields validates a complete field map against a ContentTypeSchema.
// Unknown fields, missing required fields, and type mismatches are all
// rejected. Returns nil when schema is nil (no validation is possible).
// Use for the create path where all required fields must be present.
func ValidateFields(schema *ContentTypeSchema, fields map[string]any) *ValidationError {
	if schema == nil {
		return nil
	}
	schemaFields, err := schema.ParseFields()
	if err != nil {
		return Err("schema", "invalid schema: "+err.Error())
	}
	allowed := make(map[string]SchemaField, len(schemaFields))
	for _, f := range schemaFields {
		allowed[f.Name] = f
	}
	for k := range fields {
		if _, ok := allowed[k]; !ok {
			return Err(k, "unknown field")
		}
	}
	for _, f := range schemaFields {
		v, present := fields[f.Name]
		if f.Required && !present {
			return Err(f.Name, "required")
		}
		if present {
			if ve := checkFieldType(f.Name, f.Type, v); ve != nil {
				return ve
			}
		}
	}
	return nil
}

// ValidatePartialFields validates a patch map against a ContentTypeSchema.
// Unknown fields and type mismatches are rejected; absent required fields
// are not checked (the patch is partial — required fields may already be stored).
// Returns nil when schema is nil (no validation is possible).
// Use for the update path where only provided fields need to be validated.
func ValidatePartialFields(schema *ContentTypeSchema, patch map[string]any) *ValidationError {
	if schema == nil {
		return nil
	}
	schemaFields, err := schema.ParseFields()
	if err != nil {
		return Err("schema", "invalid schema: "+err.Error())
	}
	allowed := make(map[string]SchemaField, len(schemaFields))
	for _, f := range schemaFields {
		allowed[f.Name] = f
	}
	for k, v := range patch {
		sf, ok := allowed[k]
		if !ok {
			return Err(k, "unknown field")
		}
		if ve := checkFieldType(sf.Name, sf.Type, v); ve != nil {
			return ve
		}
	}
	return nil
}

// checkFieldType verifies that v matches the declared JSON Schema type.
// JSON numbers always unmarshal as float64 in Go's any; integer fields
// additionally require that the float64 value has no fractional part.
func checkFieldType(name, typ string, v any) *ValidationError {
	switch typ {
	case "string":
		if _, ok := v.(string); !ok {
			return Err(name, "expected string")
		}
	case "integer":
		switch n := v.(type) {
		case float64:
			if math.Trunc(n) != n {
				return Err(name, "expected integer (got non-integer number)")
			}
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			// all integer Go types are acceptable
		default:
			return Err(name, "expected integer")
		}
	case "number":
		switch v.(type) {
		case float64, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			// any numeric type is acceptable
		default:
			return Err(name, "expected number")
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return Err(name, "expected boolean")
		}
	case "array":
		if _, ok := v.([]any); !ok {
			return Err(name, "expected array")
		}
	case "object":
		if _, ok := v.(map[string]any); !ok {
			return Err(name, "expected object")
		}
	}
	return nil
}

// ValidateBlockFields validates a fields JSON object against a schema.
// An unknown field (not declared in the schema) returns an error.
// A missing required field also returns an error. An absent or empty fields
// value is treated as {}.
//
// Callers that do not have a schema for the given type (schema lookup returned
// [ErrNotFound]) should skip validation entirely — unschematised types pass
// through unchanged (backwards compatibility).
func ValidateBlockFields(schema *ContentTypeSchema, fields json.RawMessage) error {
	schemaFields, err := schema.ParseFields()
	if err != nil {
		return fmt.Errorf("invalid schema for %q: %w", schema.TypeName, err)
	}

	var provided map[string]any
	if len(fields) == 0 {
		provided = map[string]any{}
	} else {
		if err := json.Unmarshal(fields, &provided); err != nil {
			return fmt.Errorf("invalid fields JSON: %w", err)
		}
	}

	// Build set of allowed field names from the schema.
	allowed := make(map[string]SchemaField, len(schemaFields))
	for _, f := range schemaFields {
		allowed[f.Name] = f
	}

	// Reject unknown fields.
	for k := range provided {
		if _, ok := allowed[k]; !ok {
			return fmt.Errorf("unknown field %q for type %q", k, schema.TypeName)
		}
	}

	// Reject missing required fields.
	for _, f := range schemaFields {
		if f.Required {
			if _, ok := provided[f.Name]; !ok {
				return fmt.Errorf("required field %q missing for type %q", f.Name, schema.TypeName)
			}
		}
	}

	return nil
}
