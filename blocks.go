package smeldr

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
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

	// LastActor is the actor ID of whoever wrote this item most recently:
	// its creation, a content update ([DynamicTypeRepo.UpdateFieldsVia],
	// since v1.130.0) or a state transition (D78). Empty when no caller
	// identity was available (a system-initiated write) or the item has not
	// been written since this column was added. Compiled types follow the same
	// rule (v1.133.0).
	LastActor string `db:"last_actor" json:"last_actor,omitempty"`
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
// path and the unique (type_name, slug) index for non-empty slugs (skipped,
// with a warning per duplicate, while duplicates exist). Call once at application startup before using [NewDynamicContentRepo]
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
	created_at   TIMESTAMPTZ NOT NULL,
	updated_at   TIMESTAMPTZ NOT NULL,
	scheduled_at TIMESTAMPTZ,
	published_at TIMESTAMPTZ,
	rev          INTEGER NOT NULL DEFAULT 0,
	last_actor   TEXT NOT NULL DEFAULT ''
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

	return ensureDynamicSlugIndex(ctx, db)
}

// ensureDynamicSlugIndex creates the partial unique index on
// smeldr_dynamic_content (type_name, slug) for non-empty slugs, so one type
// can never hold two items under one address. Blocks may carry an empty
// slug, which the index leaves alone.
//
// Duplicates that already exist are never rewritten: when there are any, each
// is logged (type, slug and the ids holding it) and the index is skipped, so
// boot goes on. Resolve them (give all but one a new slug) and the next boot
// creates the index. A failure of the CREATE INDEX statement itself is
// handled the same way: logged, index skipped, boot goes on. Only a failure
// to run the duplicate scan is returned.
func ensureDynamicSlugIndex(ctx context.Context, db DB) error {
	rows, err := db.QueryContext(ctx, `
SELECT type_name, slug, id FROM smeldr_dynamic_content
WHERE slug <> '' AND (type_name, slug) IN (
	SELECT type_name, slug FROM smeldr_dynamic_content
	WHERE slug <> '' GROUP BY type_name, slug HAVING COUNT(*) > 1)
ORDER BY type_name, slug, id`)
	if err != nil {
		return err
	}
	type key struct{ typeName, slug string }
	var order []key
	ids := map[key][]string{}
	for rows.Next() {
		var k key
		var id string
		if err := rows.Scan(&k.typeName, &k.slug, &id); err != nil {
			rows.Close()
			return err
		}
		if _, seen := ids[k]; !seen {
			order = append(order, k)
		}
		ids[k] = append(ids[k], id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(order) > 0 {
		for _, k := range order {
			slog.WarnContext(ctx, "smeldr: dynamic content slug held by more than one item; unique slug index not created until resolved",
				"type", k.typeName, "slug", k.slug, "ids", strings.Join(ids[k], ","))
		}
		return nil
	}
	if _, err := db.ExecContext(ctx, `
CREATE UNIQUE INDEX IF NOT EXISTS idx_dynamic_content_type_slug
	ON smeldr_dynamic_content (type_name, slug) WHERE slug <> ''`); err != nil {
		// A duplicate written between the scan and this statement, or any
		// other refusal, must not fail boot either: the index is skipped the
		// same way, and the next boot tries again.
		slog.WarnContext(ctx, "smeldr: dynamic content unique slug index not created; boot continues without it",
			"error", err)
	}
	return nil
}
