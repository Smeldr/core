package smeldr

import (
	"context"
	"encoding/json"
	"fmt"
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
}

// ContentTypeSchema holds the declared field definitions for one block type.
// Schemas for the 16 canonical block types are seeded at startup via
// [SeedBlockTypeSchemas]. The schema table is read-only via MCP; schema
// writability is a future concern (T55/T49).
type ContentTypeSchema struct {
	ID        string          `db:"id"`
	TypeName  string          `db:"type_name"`
	Label     string          `db:"label"`
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

// CreateSchemaTable creates smeldr_content_type_schemas if it does not exist.
// Call once at startup alongside [CreateBlockTables]. Idempotent.
func CreateSchemaTable(db DB) error {
	_, err := db.ExecContext(context.Background(), `
CREATE TABLE IF NOT EXISTS smeldr_content_type_schemas (
	id         TEXT NOT NULL PRIMARY KEY,
	type_name  TEXT NOT NULL UNIQUE,
	label      TEXT NOT NULL DEFAULT '',
	fields     TEXT NOT NULL DEFAULT '[]',
	created_at DATETIME NOT NULL,
	updated_at DATETIME NOT NULL
)`)
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
