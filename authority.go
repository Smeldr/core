// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
)

// Rule is an orchestration content type representing a fully modeled,
// governing rule — the first subtype of the conceptual Authority
// supertype (Decision/Rule/Principle/Standard/Precedent, decision-governance
// design §4). Unlike Decision, a Rule is not necessarily proposed/ratified
// through the same ceremony — see [ruleFlow]'s own doc comment.
type Rule struct {
	Node
	// Surface names the functional unit this Rule's own execution touches
	// (e.g. "core", "cloud", "site", "ops" — decision-governance §3's own
	// vocabulary). Free-text for now: the organization-configurable
	// rank/vocabulary mechanism is a sibling task's own scope, not built
	// here.
	Surface string `json:"surface"`
	// RuleType names this Rule's own place in the authority-rank ordering
	// (e.g. "design-system"). Stored as a name, never a raw integer — the
	// same instanceRoleRank-style pattern decision-governance §3 specifies
	// for Decision's own future RuleType field; comparison/ranking is a
	// sibling task's own scope, not built here.
	RuleType string `json:"rule_type" db:"rule_type"`
	// Body is the full rule text in Markdown.
	Body string `json:"body" smeldr_format:"markdown"`
	// SourceRef points at the document this Rule was modeled from (a repo
	// path, a design doc anchor, a URL) — never fabricated; empty means no
	// single source document exists (a rule stated directly, not derived).
	SourceRef string `json:"source_ref" db:"source_ref"`
}

// AuthorityStub is a lightweight, cheap-to-create pointer into a source
// document that has not yet been converted into a fully modeled Rule (or
// future Principle/Standard/Precedent) node. decision-governance §4's own
// anti-big-bang instruction: existing documents are not restructured up
// front — a stub is created per reference, converted opportunistically
// only when a real check actually needs the full node.
type AuthorityStub struct {
	Node
	// SourceRef points at the source document this stub references (a repo
	// path, a design doc anchor, a URL). Required — a stub with no source
	// is not a pointer to anything.
	SourceRef string `json:"source_ref" db:"source_ref"`
	// RuleType is this stub's own rule-type tag (see [Rule.RuleType]).
	RuleType string `json:"rule_type" db:"rule_type"`
	// Surface is this stub's own surface tag (see [Rule.Surface]).
	Surface string `json:"surface"`
	// SourceHash is a hash of the referenced source content at the moment
	// the stub was created — lets a future staleness check detect the
	// source document changing out from under an un-converted stub.
	// Algorithm choice (sha256, hex-encoded) is an implementation detail,
	// not asserted as a contract beyond "stable and comparable."
	SourceHash string `json:"source_hash" db:"source_hash"`
}

// CreateAuthorityTables creates the two Authority-mechanism content tables
// (smeldr_rules, smeldr_authority_stubs) if they do not already exist. Call
// once at application startup, alongside [CreateOrchestrationTables].
func CreateAuthorityTables(db DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS smeldr_rules (
			id           TEXT PRIMARY KEY,
			slug         TEXT NOT NULL UNIQUE,
			status       TEXT NOT NULL DEFAULT 'draft',
			published_at TIMESTAMPTZ,
			scheduled_at TIMESTAMPTZ,
			created_at   TIMESTAMPTZ NOT NULL,
			updated_at   TIMESTAMPTZ NOT NULL,
			rev          INTEGER NOT NULL DEFAULT 0,
			surface      TEXT NOT NULL DEFAULT '',
			rule_type    TEXT NOT NULL DEFAULT '',
			body         TEXT NOT NULL DEFAULT '',
			source_ref   TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS smeldr_authority_stubs (
			id           TEXT PRIMARY KEY,
			slug         TEXT NOT NULL UNIQUE,
			status       TEXT NOT NULL DEFAULT 'draft',
			published_at TIMESTAMPTZ,
			scheduled_at TIMESTAMPTZ,
			created_at   TIMESTAMPTZ NOT NULL,
			updated_at   TIMESTAMPTZ NOT NULL,
			rev          INTEGER NOT NULL DEFAULT 0,
			source_ref   TEXT NOT NULL DEFAULT '',
			rule_type    TEXT NOT NULL DEFAULT '',
			surface      TEXT NOT NULL DEFAULT '',
			source_hash  TEXT NOT NULL DEFAULT ''
		)`,
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(context.Background(), stmt); err != nil {
			return err
		}
	}
	return nil
}

// RegisterAuthorityTypes registers the two Authority-mechanism content
// types ([Rule], [AuthorityStub]) with the application, including their own
// state flows. Call after [CreateAuthorityTables] and before [App.Run].
// Flow registration errors are logged and do not block startup (fail-open),
// matching [RegisterOrchestrationTypes]'s own precedent exactly.
func RegisterAuthorityTypes(app *App, db DB) {
	flows := []StateFlow{
		ruleFlow(),
		authorityStubFlow(),
	}
	for _, f := range flows {
		if err := app.RegisterFlow(f); err != nil {
			slog.Error("smeldr: RegisterAuthorityTypes: RegisterFlow failed",
				"flow", f.Name, "error", err)
		}
	}
	app.Content(NewModule[*Rule]((*Rule)(nil),
		At("/rules"), Repo(NewSQLRepo[*Rule](db, Table("smeldr_rules"))), MCP(MCPRead, MCPWrite),
	))
	app.Content(NewModule[*AuthorityStub]((*AuthorityStub)(nil),
		At("/authority-stubs"), Repo(NewSQLRepo[*AuthorityStub](db, Table("smeldr_authority_stubs"))), MCP(MCPRead, MCPWrite),
	))
}

// RegisterAuthorityRelationKinds registers the relation kind that connects
// the Authority-mechanism types: materializes (AuthorityStub→Rule),
// asserted when a stub is converted to a fully modeled Rule. Idempotent —
// safe to call on every boot; UpsertKind updates in place if a kind with
// the same type_name is already registered. Separate from
// [RegisterOrchestrationRelationKinds] — this is a new type family, not an
// extension of the existing one.
func RegisterAuthorityRelationKinds(ctx context.Context, store *RelationStore) error {
	kinds := []RelationKindDef{
		{
			// Scoped to AuthorityStub->Rule only, for the one real need
			// named here — extend TypePairs later (Principle/Standard/
			// Precedent) only when one of those subtypes actually gets
			// built.
			TypeName:     "materializes",
			Label:        "Materializes",
			ReverseLabel: "Materialized From",
			Mode:         "asserted",
			Directional:  true,
			TypePairs:    json.RawMessage(`[{"source_type":"AuthorityStub","target_type":"Rule"}]`),
		},
	}
	for _, k := range kinds {
		if err := store.UpsertKind(ctx, k); err != nil {
			return fmt.Errorf("register relation kind %q: %w", k.TypeName, err)
		}
	}
	return nil
}

// ruleFlow returns the state flow for [Rule] records. A rule starts as
// draft while being modeled, becomes active once it genuinely governs, and
// retires when it no longer applies. Deliberately not proposed/ratified
// like Decision (decision-governance §4): most Rules migrate an already-
// governing convention into the graph, not a fresh proposal seeking
// approval — matching the concrete first case (Turn 67 §4's already-
// in-force "no seen/acknowledged" law). RequiredRole/RequiredOperation
// gating on active/retired is explicitly deferred: D63/D64's own
// role-gating redesign is proposed but not yet ratified, and adding a new
// gate ahead of that ratification would need its own later Amendment
// regardless.
func ruleFlow() StateFlow {
	return StateFlow{
		Name:     "authority-rule",
		TypeName: "Rule",
		States: []State{
			{Name: "draft", IsInitial: true},
			{Name: "active"},
			{Name: "retired", IsTerminal: true},
		},
		Transitions: []Transition{
			{From: "draft", To: "active"},
			{From: "active", To: "retired"},
		},
	}
}

// authorityStubFlow returns the state flow for [AuthorityStub] records. A
// stub starts cheap and unconverted, and either becomes converted (a full
// Rule/Principle/Standard/Precedent node now exists for it — asserted via
// the "materializes" relation kind, not tracked as a field on the stub
// itself) or retired (the reference turned out not to be worth modeling,
// or its source document is gone).
func authorityStubFlow() StateFlow {
	return StateFlow{
		Name:     "authority-stub",
		TypeName: "AuthorityStub",
		States: []State{
			{Name: "stub", IsInitial: true},
			{Name: "converted", IsTerminal: true},
			{Name: "retired", IsTerminal: true},
		},
		Transitions: []Transition{
			{From: "stub", To: "converted"},
			{From: "stub", To: "retired"},
		},
	}
}
