package smeldr

import (
	"context"
	"encoding/json"
)

// DynamicNode is the generic content type backing every block in the Smeldr
// block system. One Go type serves all block types — the concrete kind is
// carried in [DynamicNode.TypeName] (e.g. "content_block", "faq_item", "hero")
// and the type-specific fields are stored as JSON in [DynamicNode.Fields].
//
// DynamicNode embeds [Node] and therefore carries the standard content
// lifecycle (Draft / Scheduled / Published / Archived) and identity. It is
// persisted in the smeldr_dynamic_content table, created by [CreateBlockTables].
//
// Blocks are addressed by ID, not by slug — the Slug field is permitted to be
// empty (see [CreateBlockTables]). Composition (which parent contains which
// child, in what order) is recorded separately in smeldr_content_edges via
// [ContentEdgeStore], never inside Fields.
//
// Schema validation of Fields against a registered content-type schema is a
// later concern (it is performed in the MCP/handler layer); DynamicNode itself
// imposes no constraint on the shape of Fields.
type DynamicNode struct {
	Node

	// TypeName is the block-type discriminator, e.g. "content_block" or
	// "hero". It selects the field schema and the render template.
	TypeName string `db:"type_name" json:"type_name"`

	// Fields holds the type-specific data as raw JSON. Its shape is governed
	// by the block type's schema, not by this struct.
	Fields json.RawMessage `db:"fields" json:"fields"`
}

// Head implements [Content] for the future MCP and admin surface. DynamicNode
// is a storage-level type — its Head is intentionally minimal.
func (d *DynamicNode) Head() Head {
	return Head{Title: "Block"}
}

// NewDynamicContentRepo returns a [SQLRepo] bound to the smeldr_dynamic_content
// table for [DynamicNode]. Use it to create, read, update, and delete blocks.
//
// The explicit table name is required because the name derived from the type
// (DynamicNode → "dynamic_nodes") does not match the shared block table.
//
//	repo := smeldr.NewDynamicContentRepo(db)
//	node := &smeldr.DynamicNode{
//	    Node:     smeldr.Node{ID: smeldr.NewID(), Status: smeldr.Draft},
//	    TypeName: "content_block",
//	    Fields:   json.RawMessage(`{"title":"Hello","body":"World"}`),
//	}
//	err := repo.Save(ctx, node)
func NewDynamicContentRepo(db DB) *SQLRepo[*DynamicNode] {
	return NewSQLRepo[*DynamicNode](db, Table("smeldr_dynamic_content"))
}

// CreateBlockTables creates the block-system tables if they do not already
// exist: smeldr_dynamic_content (block storage) and smeldr_content_edges
// (composition edges), plus the index that serves the ordered child-list read
// path. Call once at application startup before using [NewDynamicContentRepo]
// or [NewContentEdgeStore].
//
// The function is idempotent (CREATE TABLE IF NOT EXISTS) and safe to call on
// every boot. It is the single grouped creation function for the block-system
// schema — there is no per-table creator and no versioned migration runner.
//
// The smeldr_content_type_schemas table (user-editable schemas) is a separate,
// later component and is deliberately not created here.
func CreateBlockTables(db DB) error {
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS smeldr_dynamic_content (
	id           TEXT NOT NULL PRIMARY KEY,
	slug         TEXT NOT NULL DEFAULT '',
	type_name    TEXT NOT NULL,
	status       TEXT NOT NULL DEFAULT 'draft',
	fields       TEXT NOT NULL DEFAULT '{}',
	created_at   DATETIME NOT NULL,
	updated_at   DATETIME NOT NULL,
	scheduled_at DATETIME,
	published_at DATETIME
)`); err != nil {
		return err
	}

	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS smeldr_content_edges (
	id          TEXT NOT NULL PRIMARY KEY,
	parent_id   TEXT NOT NULL,
	parent_type TEXT NOT NULL,
	child_id    TEXT NOT NULL,
	child_type  TEXT NOT NULL,
	sort_order  INTEGER NOT NULL,
	is_shared   INTEGER NOT NULL DEFAULT 0,
	edge_role   TEXT NOT NULL DEFAULT 'section'
)`); err != nil {
		return err
	}

	if _, err := db.ExecContext(ctx, `
CREATE INDEX IF NOT EXISTS idx_content_edges_parent
	ON smeldr_content_edges (parent_id, sort_order)`); err != nil {
		return err
	}

	return nil
}
