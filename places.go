// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Places (D142): Organization, Domain, Area and Set are core record types, present on
// every instance with core governance. Organization is one record per instance, the
// organization; Domains belong to Organization, Areas belong to a Domain (D72), and
// any record may be placed in a Domain or an Area. A Set groups records and
// carries no authority (D71).

// OrganizationRecord is the organization: one record per instance (D142),
// created by [App.EnsureOrganization] with the organization's name. Domains
// belong to it by belongs_to_organization, and a Decision that spans Domains
// relates to it explicitly (D83). Its type name is Organization everywhere a
// user, an agent or a tool sees it ([TypeName]); only the Go struct carries
// the Record suffix, because [Organization] is the schema.org constant this
// package already exports (a D141 exception, T895).
type OrganizationRecord struct {
	Node
	// RecordAddress is the Organization's address (D110, prefix ORG).
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
// belongs_to_domain, and records are placed in it by belongs_to_area. Its
// coverage rules say which repository paths it covers ([CoverageRule]).
type Area struct {
	Node
	// RecordAddress is the Area's address (D110, prefix ARE).
	RecordAddress
	// Name is the Area's name.
	Name string `json:"name" db:"name" smeldr:"required" smeldr_search:"text"`
	// Description is free text in Markdown.
	Description string `json:"description" db:"description" smeldr_format:"markdown" smeldr_search:"text"`
	// ApprovalMode is what a change touching the Area needs (D142 point 8):
	// [ApprovalModeCommentOnly] (the default) or [ApprovalModeRequiresApproval].
	// Stored for the pull request check, which reads it.
	ApprovalMode string `json:"approval_mode" db:"approval_mode" smeldr_enum:"comment-only|requires-approval" smeldr_description:"What a change touching the Area needs: comment-only (default) or requires-approval."`
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
var compiledPlaceTypes = []string{"Organization", "Domain", "Area", "Set"}

// legacyPlaceTypes maps each place type to the lowercase runtime type it
// replaces: instances defined domain, area and set themselves before D142,
// and [App.MigratePlaces] moves their records to the compiled types.
var legacyPlaceTypes = map[string]string{"Domain": "domain", "Area": "area", "Set": "set"}

// placeTableStmts are the tables of the place types. A unique index on a
// constant keeps smeldr_organizations to one row: an instance can never hold a second
// Organization.
var placeTableStmts = []string{
	placeTableStmt("smeldr_organizations", ""),
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_smeldr_organizations_one ON smeldr_organizations ((1))`,
	placeTableStmt("smeldr_domains", ""),
	placeTableStmt("smeldr_areas", "approval_mode TEXT NOT NULL DEFAULT 'comment-only',"),
	placeTableStmt("smeldr_sets", ""),
}

// placeTableStmt is the table of a place type.
func placeTableStmt(table, extra string) string {
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
		` + extra + `
		last_actor   TEXT NOT NULL DEFAULT ''
	)`
}

// placeFlow is the state flow of a place type: active, and archived for
// every type but Organization (an archived place is no longer a live target, as an
// archived reference type before D142).
func placeFlow(typeName string) StateFlow {
	if typeName == "Organization" {
		return StateFlow{
			Name:     "place-organization",
			TypeName: "Organization",
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
	app.Content(NewModule[*OrganizationRecord]((*OrganizationRecord)(nil),
		TypeName("Organization"), Repo(NewSQLRepo[*OrganizationRecord](db, Table("smeldr_organizations"))), MCP(MCPRead, MCPWrite), withAddress(),
	))
	app.Content(NewModule[*Domain]((*Domain)(nil),
		At("/domains"), Repo(NewSQLRepo[*Domain](db, Table("smeldr_domains"))), MCP(MCPRead, MCPWrite), withAddress(),
	))
	app.Content(NewModule[*Area]((*Area)(nil),
		At("/areas"), Repo(NewSQLRepo[*Area](db, Table("smeldr_areas"))), MCP(MCPRead, MCPWrite), withAddress(),
		saveHooksOption{prepare: func(_ Context, _, item any) error { return normalizeApprovalMode(item.(*Area)) }},
	))
	app.Content(NewModule[*Set]((*Set)(nil),
		At("/sets"), Repo(NewSQLRepo[*Set](db, Table("smeldr_sets"))), MCP(MCPRead, MCPWrite), withAddress(),
	))
}

// placeKindTargets maps each place relation kind to the place type it targets,
// and placeKindOf the other way: the kind that places a record in a type.
var (
	placeKindTargets = map[string]string{"belongs_to_organization": "Organization", "belongs_to_domain": "Domain", "belongs_to_area": "Area"}
	placeKindOf      = map[string]string{"Organization": "belongs_to_organization", "Domain": "belongs_to_domain", "Area": "belongs_to_area"}
	placeRank        = map[string]int{"Organization": 0, "Domain": 1, "Area": 2}
)

// placedItem is one record of a place hierarchy walk.
type placedItem struct{ typeName, id string }

// placeMembers walks the place hierarchy Organization -> Domain -> Area (D72, D142)
// down from the place anchorID of type anchorType: the place itself, every
// record placed in it, and for a place below it (a Domain in Organization, an Area in
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

// placeTables maps each compiled place type to its table, for the labels in
// the place refusals.
var placeTables = map[string]string{"Organization": "smeldr_organizations", "Domain": "smeldr_domains", "Area": "smeldr_areas"}

// recordPlace is a record's place: the kind and target of its live asserted
// place edge.
type recordPlace struct{ kind, targetType, targetID string }

// decisionPlace returns the place of the record id: its oldest live asserted
// edge of a [placeRelationKinds] kind (T883). It reads the kind, not the
// target type, so an edge to a legacy domain or area counts as a place
// before [App.MigratePlaces] has run, as [App.placedSources] does.
// found=false with a nil error means the record has no place.
func decisionPlace(ctx context.Context, db DB, id string) (recordPlace, bool, error) {
	var p recordPlace
	err := db.QueryRowContext(ctx,
		`SELECT relation_kind, target_type, target_id FROM smeldr_relations
		  WHERE source_id = $1 AND edge_class = 'asserted'
		    AND relation_kind IN ($2, $3, $4)
		    AND (invalid_at IS NULL OR invalid_at > $5)
		  ORDER BY created_at, id LIMIT 1`,
		id, placeRelationKinds[0], placeRelationKinds[1], placeRelationKinds[2], time.Now().UTC(),
	).Scan(&p.kind, &p.targetType, &p.targetID)
	if errors.Is(err, sql.ErrNoRows) {
		return recordPlace{}, false, nil
	}
	if err != nil {
		return recordPlace{}, false, fmt.Errorf("%w: place of %s: %s", ErrInternal, id, err)
	}
	return p, true, nil
}

// requireDecisionPlace refuses to ratify a Decision without a place (D142
// point 4, T883): the place decides who may approve it (D83), so a Decision
// with none is refused fail-closed, for every caller, on every door into
// ratified.
func requireDecisionPlace(ctx context.Context, db DB, id string) error {
	_, found, err := decisionPlace(ctx, db, id)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: Decision %s has no place: place it before ratifying, with assert_relation belongs_to_domain (a Domain), belongs_to_area (an Area) or belongs_to_organization (Organization, for a Decision that spans Domains)",
			ErrConflict, recordLabel(ctx, db, "smeldr_decisions", id))
	}
	return nil
}

// requireOnePlace refuses a second place for a Decision (T883): a Decision
// has exactly one place, Organization, one Domain or one Area (an Area implies its
// Domain). The same edge again is not a second place. Other records are not
// limited.
func requireOnePlace(ctx context.Context, db DB, edge RelationEdge) error {
	if edge.SourceType != "Decision" || placeKindTargets[edge.RelationKind] == "" {
		return nil
	}
	p, found, err := decisionPlace(ctx, db, edge.SourceID)
	if err != nil || !found {
		return err
	}
	if p.kind == edge.RelationKind && p.targetID == edge.TargetID {
		return nil
	}
	return fmt.Errorf("%w: Decision %s already has a place (%s %s): a Decision has exactly one place; withdraw that relation first",
		ErrConflict, recordLabel(ctx, db, "smeldr_decisions", edge.SourceID), p.kind,
		recordLabel(ctx, db, placeTables[p.targetType], p.targetID))
}

// recordLabel is the address of the record id in table, or id when it has
// none or cannot be read: a label for a message, never a failure of its own.
func recordLabel(ctx context.Context, db DB, table, id string) string {
	if table == "" {
		return id
	}
	var address string
	if err := db.QueryRowContext(ctx, `SELECT address FROM `+quoteIdent(table)+` WHERE id = $1`, id).Scan(&address); err != nil || address == "" {
		return id
	}
	return address
}

// normalizeApprovalMode defaults an Area's approval mode to comment-only and
// refuses any value but the two modes (T886).
func normalizeApprovalMode(a *Area) error {
	a.ApprovalMode = strings.TrimSpace(a.ApprovalMode)
	switch a.ApprovalMode {
	case "":
		a.ApprovalMode = ApprovalModeCommentOnly
	case ApprovalModeCommentOnly, ApprovalModeRequiresApproval:
	default:
		return Err("approval_mode", fmt.Sprintf("must be %q or %q", ApprovalModeCommentOnly, ApprovalModeRequiresApproval))
	}
	return nil
}
