// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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

// — RuleType rank mechanism (decision-governance design §3) ————————————————

// CreateRuleTypeRankTable creates smeldr_rule_type_ranks if it does not
// already exist. Call once at application startup, alongside
// [CreateAuthorityTables].
func CreateRuleTypeRankTable(db DB) error {
	_, err := db.ExecContext(context.Background(), `
		CREATE TABLE IF NOT EXISTS smeldr_rule_type_ranks (
			name TEXT PRIMARY KEY,
			rank INTEGER NOT NULL
		)`)
	return err
}

// SetRuleTypeOrder declares the complete, current authority-rank ordering
// for RuleType values (decision-governance §3), from weakest to strongest
// authority — rank equals index in names (0 = weakest). Vocabulary and
// depth are organization-defined, not hardcoded by this function, unlike
// e.g. smeldr/cloud's own closed instanceRoleRank switch.
//
// This is a full replace, not a merge: a name registered by a previous
// call that is absent from names loses its registered rank entirely
// ([RuleTypeRank] then returns ok=false for it) — matching §3's own
// framing that the organization declares "the" current ordering, not an
// accumulating set. Not wrapped in a transaction (same accepted risk
// profile as [RegisterOrchestrationRelationKinds]'s own per-item loop) —
// a failure partway through leaves a partial ordering, surfaced as an
// error to the caller, never silently.
func SetRuleTypeOrder(ctx context.Context, db DB, names []string) error {
	if _, err := db.ExecContext(ctx, `DELETE FROM smeldr_rule_type_ranks`); err != nil {
		return fmt.Errorf("smeldr: SetRuleTypeOrder: clear existing order: %w", err)
	}
	for rank, name := range names {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO smeldr_rule_type_ranks (name, rank) VALUES ($1, $2)`,
			name, rank,
		); err != nil {
			return fmt.Errorf("smeldr: SetRuleTypeOrder: insert %q: %w", name, err)
		}
	}
	return nil
}

// RuleTypeRank looks up name's current authority rank. ok is false when
// name is not currently registered — never a silent 0, which would
// wrongly rank an unclassified name as the single weakest registered one
// instead of "unknown."
func RuleTypeRank(ctx context.Context, db DB, name string) (rank int, ok bool, err error) {
	row := db.QueryRowContext(ctx,
		`SELECT rank FROM smeldr_rule_type_ranks WHERE name = $1`, name)
	if err := row.Scan(&rank); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("smeldr: RuleTypeRank: %w", err)
	}
	return rank, true, nil
}

// — Reversibility (decision-governance design §3/§7) ————————————————————————

// Reversibility is a [Decision]'s own declared or inferred reversibility —
// not binary. The zero value "" means neither declared nor inferable,
// never treated as [Reversible] by default (§7's own explicit
// fail-closed-on-the-unknown posture).
type Reversibility string

const (
	// Reversible means the actual world can be fully restored.
	Reversible Reversibility = "reversible"
	// ConditionallyReversible means restoration is technically possible
	// but carries a real cost or loss.
	ConditionallyReversible Reversibility = "conditionally-reversible"
	// Irreversible means the actual world cannot be restored.
	Irreversible Reversibility = "irreversible"
	// ReversibilityDisputed marks a declared value that disagrees with an
	// inferred one — never silently overridden by either side (§7). See
	// [ResolveReversibility].
	ReversibilityDisputed Reversibility = "reversibility-disputed"
)

// DestructiveOperationClass is one entry in reversibility's own small,
// closed, hardcoded allowlist (§7) — audited by editing this list
// directly, not organization-configurable like RuleType: §7 names a
// fixed, universal set of destructive act shapes, not an org-specific
// vocabulary.
type DestructiveOperationClass string

const (
	ClassDelete                DestructiveOperationClass = "delete"
	ClassExternalCommunication DestructiveOperationClass = "external-communication"
	ClassFundsTransfer         DestructiveOperationClass = "funds-transfer"
)

// destructiveOperationReversibility is the allowlist itself — each listed
// class is inferred with no friction (§7); anything not present here has
// no default and must fall back to an explicit declaration.
var destructiveOperationReversibility = map[DestructiveOperationClass]Reversibility{
	ClassDelete:                Irreversible,
	ClassExternalCommunication: Irreversible,
	ClassFundsTransfer:         Irreversible,
}

// InferReversibility looks up class's own allowlisted reversibility. ok is
// false for any class not on the allowlist — the caller must fall back to
// requiring an explicit declaration (§7), never assume [Reversible].
//
// §7 names a fourth class, "a transition explicitly flagged irreversible
// in its own flow definition" — deliberately not built here: it requires
// a new field on the core [Transition] struct plus a matching
// smeldr_transitions migration, a cross-file schema change beyond this
// allowlist's own scope, named as its own future follow-up.
func InferReversibility(class DestructiveOperationClass) (value Reversibility, ok bool) {
	value, ok = destructiveOperationReversibility[class]
	return value, ok
}

// ResolveReversibility reconciles an inferred value (from
// [InferReversibility]) against a declared one (e.g. [Decision].
// Reversibility as written by whoever authored it). Pure function — not
// wired into any transition or gate; enforcement (blocking a transition
// on a missing declaration) is decision-governance §4's own Check
// mechanism, a future task's scope, not this function's.
func ResolveReversibility(inferred Reversibility, inferredOK bool, declared Reversibility) Reversibility {
	switch {
	case inferredOK && declared != "" && declared != inferred:
		return ReversibilityDisputed
	case declared != "":
		return declared
	case inferredOK:
		return inferred
	default:
		return ""
	}
}
