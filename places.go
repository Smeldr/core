// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"fmt"
	"time"
)

// Places (D142): Root, Domain, Area and Set are core record types, present on
// every instance with core governance. Root is one record per instance, the
// organization; Domains belong to Root, Areas belong to a Domain (D72), and
// any record may be placed in a Domain or an Area. A Set groups records and
// carries no authority (D71).

// Root is the organization: one record per instance (D142), created by
// [App.EnsureRoot] with the organization's name. Domains belong to it by
// belongs_to_root, and a Decision that spans Domains relates to it explicitly
// (D83).
type Root struct {
	Node
	// RecordAddress is the Root's address (D110, prefix ROOT).
	RecordAddress
	// Name is the organization's name.
	Name string `json:"name" db:"name" smeldr:"required" smeldr_search:"text"`
	// Description is free text in Markdown.
	Description string `json:"description" db:"description" smeldr_format:"markdown" smeldr_search:"text"`
}

// Domain is a part of the organization with its own authority (D72): records
// placed in it by belongs_to_domain, Areas that belong to it, and the
// domain-approver and domain-reader roles scoped to it.
type Domain struct {
	Node
	// RecordAddress is the Domain's address (D110, prefix DOM).
	RecordAddress
	// Name is the Domain's name.
	Name string `json:"name" db:"name" smeldr:"required" smeldr_search:"text"`
	// Description is free text in Markdown.
	Description string `json:"description" db:"description" smeldr_format:"markdown" smeldr_search:"text"`
}

// Area is a part of one Domain (D72): it belongs to its Domain by
// belongs_to_domain, and records are placed in it by belongs_to_area.
type Area struct {
	Node
	// RecordAddress is the Area's address (D110, prefix ARE).
	RecordAddress
	// Name is the Area's name.
	Name string `json:"name" db:"name" smeldr:"required" smeldr_search:"text"`
	// Description is free text in Markdown.
	Description string `json:"description" db:"description" smeldr_format:"markdown" smeldr_search:"text"`
}

// Set groups records by in_set. It carries no authority (D71): a Set is never
// a place that decides who may approve or read.
type Set struct {
	Node
	// RecordAddress is the Set's address (D110, prefix SET).
	RecordAddress
	// Name is the Set's name.
	Name string `json:"name" db:"name" smeldr:"required" smeldr_search:"text"`
	// Description is free text in Markdown.
	Description string `json:"description" db:"description" smeldr_format:"markdown" smeldr_search:"text"`
}

// compiledPlaceTypes are the compiled place types, in registration order.
var compiledPlaceTypes = []string{"Root", "Domain", "Area", "Set"}

// legacyPlaceTypes maps each place type to the lowercase runtime type it
// replaces: instances defined domain, area and set themselves before D142,
// and [App.MigratePlaces] moves their records to the compiled types.
var legacyPlaceTypes = map[string]string{"Domain": "domain", "Area": "area", "Set": "set"}

// placeTableStmts are the tables of the place types. A unique index on a
// constant keeps smeldr_roots to one row: an instance can never hold a second
// Root.
var placeTableStmts = []string{
	placeTableStmt("smeldr_roots"),
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_smeldr_roots_one ON smeldr_roots ((1))`,
	placeTableStmt("smeldr_domains"),
	placeTableStmt("smeldr_areas"),
	placeTableStmt("smeldr_sets"),
}

// placeTableStmt is the table of a place type.
func placeTableStmt(table string) string {
	return `CREATE TABLE IF NOT EXISTS ` + table + ` (
		id           TEXT PRIMARY KEY,
		slug         TEXT NOT NULL UNIQUE,
		status       TEXT NOT NULL DEFAULT 'active',
		published_at TIMESTAMPTZ,
		scheduled_at TIMESTAMPTZ,
		created_at   TIMESTAMPTZ NOT NULL,
		updated_at   TIMESTAMPTZ NOT NULL,
		rev          INTEGER NOT NULL DEFAULT 0,
		name         TEXT NOT NULL DEFAULT '',
		description  TEXT NOT NULL DEFAULT '',
		last_actor   TEXT NOT NULL DEFAULT ''
	)`
}

// placeFlow is the state flow of a place type: active, and archived for
// every type but Root (an archived place is no longer a live target, as an
// archived reference type before D142).
func placeFlow(typeName string) StateFlow {
	if typeName == "Root" {
		return StateFlow{
			Name:     "place-root",
			TypeName: "Root",
			States:   []State{{Name: "active", IsInitial: true}},
		}
	}
	return StateFlow{
		Name:     "place-" + camelToSnake(typeName),
		TypeName: typeName,
		States: []State{
			{Name: "active", IsInitial: true},
			{Name: "archived"},
		},
		Transitions: []Transition{
			{From: "active", To: "archived"},
			{From: "archived", To: "active"},
		},
	}
}

// registerPlaceTypes registers the four place types with their flows.
// Called by [RegisterGovernanceTypes].
func registerPlaceTypes(app *App, db DB) {
	for _, t := range compiledPlaceTypes {
		registerFlows(app, placeFlow(t))
	}
	app.Content(NewModule[*Root]((*Root)(nil),
		At("/roots"), Repo(NewSQLRepo[*Root](db, Table("smeldr_roots"))), MCP(MCPRead, MCPWrite), withAddress(),
	))
	app.Content(NewModule[*Domain]((*Domain)(nil),
		At("/domains"), Repo(NewSQLRepo[*Domain](db, Table("smeldr_domains"))), MCP(MCPRead, MCPWrite), withAddress(),
	))
	app.Content(NewModule[*Area]((*Area)(nil),
		At("/areas"), Repo(NewSQLRepo[*Area](db, Table("smeldr_areas"))), MCP(MCPRead, MCPWrite), withAddress(),
	))
	app.Content(NewModule[*Set]((*Set)(nil),
		At("/sets"), Repo(NewSQLRepo[*Set](db, Table("smeldr_sets"))), MCP(MCPRead, MCPWrite), withAddress(),
	))
}

// placeKindTargets maps each place relation kind to the place type it targets,
// and placeKindOf the other way: the kind that places a record in a type.
var (
	placeKindTargets = map[string]string{"belongs_to_root": "Root", "belongs_to_domain": "Domain", "belongs_to_area": "Area"}
	placeKindOf      = map[string]string{"Root": "belongs_to_root", "Domain": "belongs_to_domain", "Area": "belongs_to_area"}
	placeRank        = map[string]int{"Root": 0, "Domain": 1, "Area": 2}
)

// placedItem is one record of a place hierarchy walk.
type placedItem struct{ typeName, id string }

// placeMembers walks the place hierarchy Root -> Domain -> Area (D72, D142)
// down from the place anchorID of type anchorType: the place itself, every
// record placed in it, and for a place below it (a Domain in Root, an Area in
// a Domain) that place and what is placed in it. No other relation kind is
// followed. Read scopes and the per-item check both use it, and so will the
// approve scopes (T884).
func placeMembers(ctx context.Context, db DB, anchorType, anchorID string) ([]placedItem, error) {
	now := time.Now().UTC()
	out := []placedItem{{anchorType, anchorID}}
	seen := map[string]bool{anchorID: true}
	frontier := []placedItem{{anchorType, anchorID}}
	for len(frontier) > 0 {
		p := frontier[0]
		frontier = frontier[1:]
		rows, err := db.QueryContext(ctx,
			`SELECT source_type, source_id FROM smeldr_relations
			  WHERE target_id = $1 AND relation_kind = $2 AND edge_class = 'asserted'
			    AND (invalid_at IS NULL OR invalid_at > $3)`,
			p.id, placeKindOf[p.typeName], now)
		if err != nil {
			return nil, fmt.Errorf("place members of %s %s: %w", p.typeName, p.id, err)
		}
		var children []placedItem
		for rows.Next() {
			var c placedItem
			if err := rows.Scan(&c.typeName, &c.id); err != nil {
				rows.Close()
				return nil, fmt.Errorf("place members of %s %s: %w", p.typeName, p.id, err)
			}
			children = append(children, c)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, fmt.Errorf("place members of %s %s: %w", p.typeName, p.id, err)
		}
		for _, c := range children {
			if seen[c.id] {
				continue
			}
			seen[c.id] = true
			out = append(out, c)
			if rank, ok := placeRank[c.typeName]; ok && rank > placeRank[p.typeName] {
				frontier = append(frontier, c)
			}
		}
	}
	return out, nil
}
