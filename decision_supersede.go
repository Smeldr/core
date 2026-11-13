// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// D189: a superseded Decision always has its replacement. Supersede is one
// write in one of two forms, retiring is its own act and state, the bare move
// to superseded without a replacement is deprecated, and a Decision left
// superseded with no replacement is a finding that one recorded act repairs.

// decisionRetired is the state of a Decision taken out of force without a
// replacement (D189 point 4).
const decisionRetired = "retired"

// decisionSupersedeCutover is the date from which the bare move of a Decision
// to superseded, with no live supersedes relation pointing at it, is refused
// (D189 point 3). Before it the move works with a warning that names the
// date. Three months from the release that first carries the warning (D97).
var decisionSupersedeCutover = time.Date(2027, 2, 15, 0, 0, 0, 0, time.UTC)

// warningSupersedeWithoutReplacement is the warning code of the bare move
// before [decisionSupersedeCutover].
const warningSupersedeWithoutReplacement = "decision.supersede_without_replacement"

// decisionInForce reports whether a Decision in status is in force: ratified,
// or pending re-evaluation.
func decisionInForce(status string) bool {
	return status == decisionRatified || status == "pending-re-evaluation"
}

// decisionWasInForce reports whether a Decision in status is, or was, in
// force: a replacement named by the repair may itself have been replaced
// since (D189 point 1).
func decisionWasInForce(status string) bool {
	return decisionInForce(status) || status == "superseded" || status == decisionRetired
}

// SupersedeRequest is one supersede write ([App.SupersedeDecision], D189
// point 2). Exactly one of Replacement and New is set.
type SupersedeRequest struct {
	// Decision is the Decision being replaced, by address, slug or ID. It
	// must be in force (ratified or pending re-evaluation).
	Decision string
	// Rev is the rev of the replaced Decision the caller read (T1010). A
	// second send of the same request meets a rev conflict and writes
	// nothing. Nil writes with a rev_missing warning.
	Rev *int
	// Reason says why. It is required, and kept on the move and the relation.
	Reason string
	// Replacement is an existing, ratified Decision, by address, slug or ID.
	Replacement string
	// New is a replacement created by this write: core creates it, places it
	// where the replaced Decision is placed and ratifies it, in the same
	// transaction.
	New *NewReplacement
}

// NewReplacement is the Decision a supersede write creates ([SupersedeRequest]).
type NewReplacement struct {
	// Title is the new Decision's title (at most 120 characters, D112).
	Title string
	// Body is its basis: the claim that binds (D148 point 1).
	Body string
	// Scope is its scope; empty takes the replaced Decision's.
	Scope string
	// Sources are its pinned sources (D148). The replaced Decision's sources
	// are never copied: a source supports the claim it was attached to, and
	// the new claim is new text.
	Sources DecisionSources
}

// SupersedeResult is what a supersede write did.
type SupersedeResult struct {
	// Decision and Replacement are the addresses (IDs when a record has none).
	Decision    string `json:"decision"`
	Replacement string `json:"replacement"`
	// Relation is the ID of the live supersedes relation.
	Relation string `json:"relation"`
	// Created is true when the replacement was created by this write.
	Created bool `json:"created"`
	// Warnings are the write's warnings (rev_missing and the like).
	Warnings []CreateWarning `json:"warnings,omitempty"`
}

// SupersedeDecision replaces a Decision in one write (D189 point 2, D181
// point 7): the replacement is ratified (an existing one) or created, placed
// where the replaced one is and ratified (a new one); the supersedes relation
// is asserted; the replaced Decision moves to superseded. The Decisions, the
// place and the relation commit in one transaction, or nothing does; the
// move is written only while the replaced Decision is still in the state the
// checks read, so two overlapping supersedes cannot both commit, with or
// without a rev. Standing is recorded after the commit, as on any
// transition.
//
// The caller needs ratify over the replaced Decision's place, and in the
// first form over the replacement's place too. An agent or a job is refused
// (D142). A reason is required. Every check is made before any write: an
// unknown or unreadable Decision is [ErrNotFound]; a replaced Decision not in
// force, or a replacement not ratified, is a conflict
// (decision.replacement_state); a Decision with no place is
// decision.place_missing; neither or both forms is decision.supersede_form; a
// stale Rev is a [*RevConflictError].
func (a *App) SupersedeDecision(ctx context.Context, req SupersedeRequest) (SupersedeResult, error) {
	return a.supersedeDecision(ctx, "", req)
}

// SupersedeDecisionVia is [App.SupersedeDecision] naming the surface the
// write came through ("mcp", "http", "cli"), for provenance.
func (a *App) SupersedeDecisionVia(ctx context.Context, surface string, req SupersedeRequest) (SupersedeResult, error) {
	return a.supersedeDecision(ctx, surface, req)
}

// decisionRef is a Decision as a supersede, retire or repair resolved it.
type decisionRef struct {
	id, slug, status, label string
	place                   recordPlace
	placed                  bool
}

// resolveDecision resolves ident to a Decision the caller may read, with its
// place. Unknown and unreadable are [ErrNotFound] alike.
func (a *App) resolveDecision(ctx context.Context, scope *ReadScope, ident string) (decisionRef, error) {
	_, id, slug, status, err := a.lookupCompiledRecord(ctx, "Decision", ident, scope)
	if err != nil {
		return decisionRef{}, err
	}
	if !scope.Readable("Decision", id) {
		return decisionRef{}, problem(problemItemNotFound, ErrNotFound, nil)
	}
	d := decisionRef{id: id, slug: slug, status: status, label: recordLabel(ctx, a.cfg.DB, "smeldr_decisions", id)}
	if d.place, d.placed, err = decisionPlace(ctx, a.cfg.DB, id); err != nil {
		return decisionRef{}, err
	}
	return d, nil
}

// requireRatifyOver is the authority check of a supersede, retire or repair:
// ratify over d's place (D189). A Decision with no place is
// decision.place_missing: nobody holds authority over it until it is placed.
// requireActOver refuses an agent or a job first (D142).
func (a *App) requireRatifyOver(ctx context.Context, d decisionRef, act, code string) error {
	if !d.placed {
		return problem(problemDecisionPlaceMissing,
			fmt.Errorf("%w: Decision %s has no place: place it first (assert_relation belongs_to_domain, belongs_to_area or belongs_to_organization), so it is known who may %s it", ErrConflict, d.label, code),
			problemValues{"decision": d.label})
	}
	over := recordLabel(ctx, a.cfg.DB, placeTables[d.place.targetType], d.place.targetID)
	return requireActOver(ctx, a.governance, OperationRatify, AuthTarget{TypeName: d.place.targetType, ID: d.place.targetID},
		placeAct{act: act, code: code, over: over, place: readableRecord(d.place.targetType, d.place.targetID), actRead: true, decision: "D189"})
}

// requireDecisionGovernance refuses an act gated on ratify when no governance
// is wired, as a strict transition is (nobody could hold the operation).
func (a *App) requireDecisionGovernance(act string) error {
	if a.governance != nil {
		return nil
	}
	return problem(problemTransitionGateUnwired,
		fmt.Errorf("%w: %s needs the operation %q, and governance is not wired", ErrForbidden, act, OperationRatify),
		problemValues{"from": "", "to": "", "operation": OperationRatify})
}

// decisionRev reads the rev of the Decision id and checks it against
// expected: a stale one is a [*RevConflictError]; nil yields the rev_missing
// warning while the instance allows it.
func (a *App) decisionRev(ctx context.Context, id, ident, write string, expected *int) (*CreateWarning, error) {
	var rev int
	if err := a.cfg.DB.QueryRowContext(ctx, `SELECT rev FROM smeldr_decisions WHERE id = $1`, id).Scan(&rev); err != nil {
		return nil, fmt.Errorf("%w: the rev of Decision %s: %s", ErrInternal, ident, err)
	}
	if expected == nil {
		return revMissing("Decision", ident, write, a.cfg.RevCutover)
	}
	if *expected != rev {
		return nil, &RevConflictError{Type: "Decision", ID: ident, Sent: *expected, Current: rev}
	}
	return nil, nil
}

// liveSupersedes returns the ID of a live supersedes relation from source to
// target, or of any live one pointing at target when source is empty; "" when
// there is none.
func liveSupersedes(ctx context.Context, db DB, source, target string) (string, error) {
	q := `SELECT id FROM smeldr_relations
	       WHERE relation_kind = 'supersedes' AND edge_class = 'asserted' AND target_id = $1
	         AND (invalid_at IS NULL OR invalid_at > $2)`
	args := []any{target, time.Now().UTC()}
	if source != "" {
		q += ` AND source_id = $3`
		args = append(args, source)
	}
	var id string
	err := db.QueryRowContext(ctx, q+` ORDER BY created_at, id LIMIT 1`, args...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) || isNoSuchTable(err) {
		return "", nil // no relation table: no relation
	}
	if err != nil {
		return "", fmt.Errorf("%w: the supersedes relations of %s: %s", ErrInternal, target, err)
	}
	return id, nil
}

// decisionModule is the App's compiled Decision module, nil when the
// orchestration types are not registered.
func (a *App) decisionModule() *Module[*Decision] {
	for _, mm := range a.mcpModules {
		if m, ok := mm.(*Module[*Decision]); ok {
			return m
		}
	}
	return nil
}

// setDecisionStatus moves the Decision id from the state from to status on
// exec, with the actor, after advancing its rev (named by expected when set).
// The move is written only while the Decision is still in from: when it has
// moved since the checks read it (an overlapping write, rev or no rev), the
// act is refused with decision.replacement_state and the transaction rolls
// back. status == from guards without moving.
func setDecisionStatus(ctx context.Context, exec DB, id, label, ident, from, status, actorID string, expected *int) error {
	if _, _, err := bumpRev(ctx, exec, "smeldr_decisions", "Decision", id, expected); err != nil {
		var rc *RevConflictError
		if errors.As(err, &rc) {
			rc.ID = ident
		}
		return err
	}
	res, err := exec.ExecContext(ctx,
		`UPDATE smeldr_decisions SET status = $1, updated_at = $2, last_actor = $3 WHERE id = $4 AND status = $5`,
		status, time.Now().UTC(), actorID, id, from)
	if err != nil {
		return fmt.Errorf("%w: move Decision %s to %s: %s", ErrInternal, ident, status, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%w: move Decision %s to %s: %s", ErrInternal, ident, status, err)
	}
	if n == 0 {
		return problem(problemDecisionReplacementState,
			fmt.Errorf("%w: %s is no longer %s: it moved while this act was being written; read it again", ErrConflict, label, from),
			problemValues{"decision": label, "replacement": "", "state": from})
	}
	return nil
}

// refuseMutualSupersede refuses naming repl as the replacement of old when
// old already supersedes repl: the write would leave each superseding the
// other.
func refuseMutualSupersede(ctx context.Context, db DB, old, repl decisionRef) error {
	back, err := liveSupersedes(ctx, db, old.id, repl.id)
	if err != nil || back == "" {
		return err
	}
	return problem(problemDecisionReplacementState,
		fmt.Errorf("%w: %s already supersedes %s (relation %s), so %s cannot replace it: the two would supersede each other", ErrConflict, old.label, repl.label, back, repl.label),
		problemValues{"decision": old.label, "replacement": repl.label, "state": repl.status})
}

func (a *App) supersedeDecision(ctx context.Context, surface string, req SupersedeRequest) (SupersedeResult, error) {
	reason := strings.TrimSpace(req.Reason)
	hasExisting, hasNew := strings.TrimSpace(req.Replacement) != "", req.New != nil
	switch {
	case reason == "":
		return SupersedeResult{}, fieldErr(problemFieldRequired, "reason", "a reason is required to supersede a Decision", nil)
	case hasExisting == hasNew:
		return SupersedeResult{}, problem(problemDecisionSupersedeForm,
			fmt.Errorf("%w: name the replacement: either an existing, ratified Decision, or a new one by its title and basis, not both", ErrBadRequest),
			problemValues{"act": "supersede"})
	case a.cfg.DB == nil || a.relationStore == nil:
		return SupersedeResult{}, fmt.Errorf("%w: SupersedeDecision needs a database and relations (App.Relations)", ErrInternal)
	}
	if err := a.requireDecisionGovernance("superseding a Decision"); err != nil {
		return SupersedeResult{}, err
	}
	sc, ok := ctx.(Context)
	if !ok {
		return SupersedeResult{}, problem(problemPlaceNoActor, fmt.Errorf("%w: superseding a Decision needs an actor", ErrForbidden),
			problemValues{"act": "supersede", "operation": OperationRatify, "place": ""})
	}
	db := a.cfg.DB
	scope, err := scopeFor(ctx, a.governance)
	if err != nil {
		return SupersedeResult{}, err
	}

	// Every check before any write.
	old, err := a.resolveDecision(ctx, scope, req.Decision)
	if err != nil {
		return SupersedeResult{}, err
	}
	var repl decisionRef
	if hasExisting {
		if repl, err = a.resolveDecision(ctx, scope, req.Replacement); err != nil {
			return SupersedeResult{}, err
		}
	}
	if !decisionInForce(old.status) {
		return SupersedeResult{}, problem(problemDecisionReplacementState,
			fmt.Errorf("%w: %s is %s; only a ratified Decision, or one pending re-evaluation, can be superseded", ErrConflict, old.label, old.status),
			problemValues{"decision": old.label, "replacement": repl.label, "state": old.status})
	}
	if hasExisting {
		switch {
		case repl.id == old.id:
			return SupersedeResult{}, problem(problemDecisionReplacementState,
				fmt.Errorf("%w: a Decision cannot replace itself", ErrConflict),
				problemValues{"decision": old.label, "replacement": repl.label, "state": repl.status})
		case repl.status != decisionRatified:
			return SupersedeResult{}, problem(problemDecisionReplacementState,
				fmt.Errorf("%w: %s can replace %s only once it is ratified; it is %s", ErrConflict, repl.label, old.label, repl.status),
				problemValues{"decision": old.label, "replacement": repl.label, "state": repl.status})
		}
	}
	if err := a.requireRatifyOver(ctx, old, "superseding "+old.label, "supersede"); err != nil {
		return SupersedeResult{}, err
	}
	if hasExisting {
		if err := a.requireRatifyOver(ctx, repl, "naming "+repl.label+" as the replacement of "+old.label, "supersede"); err != nil {
			return SupersedeResult{}, err
		}
	}
	var warnings []CreateWarning
	w, err := a.decisionRev(ctx, old.id, req.Decision, "supersede", req.Rev)
	if err != nil {
		return SupersedeResult{}, err
	}
	if w != nil {
		warnings = append(warnings, *w)
	}

	// The new replacement is prepared on the create's own rules (validation,
	// title, hooks, the type's prepareSave); it is written in the transaction.
	var m *Module[*Decision]
	var item *Decision
	if hasNew {
		if m = a.decisionModule(); m == nil {
			return SupersedeResult{}, fmt.Errorf("%w: SupersedeDecision needs the Decision type (RegisterOrchestrationTypes)", ErrInternal)
		}
		scopeName := strings.TrimSpace(req.New.Scope)
		if scopeName == "" {
			if err := db.QueryRowContext(ctx, `SELECT scope FROM smeldr_decisions WHERE id = $1`, old.id).Scan(&scopeName); err != nil {
				return SupersedeResult{}, fmt.Errorf("%w: the scope of %s: %s", ErrInternal, old.label, err)
			}
		}
		fields := map[string]any{"title": req.New.Title, "body": req.New.Body, "scope": scopeName}
		if len(req.New.Sources) > 0 {
			fields["sources"] = req.New.Sources
		}
		var prep createPrep
		if item, prep, err = m.prepareCreate(sc, fields); err != nil {
			return SupersedeResult{}, err
		}
		if strings.TrimSpace(item.Body) == "" {
			return SupersedeResult{}, fieldErr(problemFieldRequired, "body", "the new Decision's basis is required", nil)
		}
		warnings = append(warnings, prep.titleWarns...)
		repl = decisionRef{id: item.ID, slug: item.Slug, status: decisionRatified, place: old.place, placed: true}
	}
	liveRel := ""
	if hasExisting {
		if err := refuseMutualSupersede(ctx, db, old, repl); err != nil {
			return SupersedeResult{}, err
		}
		if liveRel, err = liveSupersedes(ctx, db, repl.id, old.id); err != nil {
			return SupersedeResult{}, err
		}
	}
	// D34, as on the transition's own path: the scope's role, for each
	// Decision that moves or is ratified here (a no-op while no scope maps to
	// a role).
	actorID, _ := actorFromContext(ctx)
	if err := authorizeDecisionScopeByID(ctx, db, a.governance, actorID, "Decision", old.id, decisionScopeRoles); err != nil {
		return SupersedeResult{}, err
	}
	if hasNew {
		if err := authorizeDecisionScope(ctx, a.governance, actorID, item, decisionScopeRoles); err != nil {
			return SupersedeResult{}, err
		}
	}
	var placeEdge, relEdge RelationEdge
	relNew := false
	err = runInTx(ctx, db, func(exec DB) error {
		if hasNew {
			// Inserted ratified: the ratification is this act's, recorded as
			// the move proposed to ratified after the commit.
			setNodeStatus(item, Status(decisionRatified))
			if err := m.assignNewAddress(sc, exec, item); err != nil {
				return err
			}
			if m.saveBefore != nil {
				if err := m.saveBefore(sc, exec, nil, item); err != nil {
					return err
				}
			}
			if err := m.repoOn(exec).Save(sc, item); err != nil {
				return err
			}
			var err error
			if placeEdge, _, err = writeEdgeRow(ctx, exec, RelationEdge{SourceType: "Decision", SourceID: item.ID,
				TargetType: old.place.targetType, TargetID: old.place.targetID, RelationKind: old.place.kind,
				EdgeClass: "asserted", Attributes: []byte("{}")}); err != nil {
				return err
			}
		}
		if liveRel == "" {
			var err error
			if relEdge, relNew, err = writeEdgeRow(ctx, exec, RelationEdge{SourceType: "Decision", SourceID: repl.id,
				TargetType: "Decision", TargetID: old.id, RelationKind: "supersedes",
				EdgeClass: "asserted", Attributes: []byte("{}")}); err != nil {
				return err
			}
		} else {
			relEdge.ID = liveRel
		}
		return setDecisionStatus(ctx, exec, old.id, old.label, req.Decision, old.status, "superseded", actorID, req.Rev)
	})
	if err != nil {
		return SupersedeResult{}, err
	}

	// Committed: record and announce, as the separate paths would.
	if hasNew {
		repl.label = addressOf(item)
		if repl.label == "" {
			repl.label = item.ID
		}
		if err := m.announceCreate(sc, item, surface, reason); err != nil {
			slog.WarnContext(ctx, "smeldr: supersede: the new Decision's save hook failed after commit", "decision", item.ID, "error", err)
		}
		a.afterStatusChange(ctx, statusTransition{typeName: "Decision", id: item.ID, slug: item.Slug,
			from: decisionProposed, to: decisionRatified, surface: surface, reason: reason, milestones: true})
		a.relationStore.announceEdgeFor(ctx, placeEdge, true, surface, reason)
	}
	if liveRel == "" {
		a.relationStore.announceEdgeFor(ctx, relEdge, relNew, surface, reason)
	}
	a.afterStatusChange(ctx, statusTransition{typeName: "Decision", id: old.id, slug: old.slug,
		from: old.status, to: "superseded", surface: surface, reason: reason, milestones: true})
	if hasNew {
		fireAsyncTriggers(ctx, db, "Decision", decisionProposed, decisionRatified, item.ID)
		runDecisionAuthorityCheckByID(ctx, db, a.checkStore, "Decision", item.ID, decisionProposed, decisionRatified)
		runDeclaredTensionAggregationByID(ctx, db, a.findingStore, "Decision", item.ID, decisionProposed, decisionRatified)
		a.fireModuleAfterTransition(ctx, "Decision", item.ID, decisionProposed, decisionRatified)
	}
	a.fireModuleAfterTransition(ctx, "Decision", old.id, old.status, "superseded")

	if repl.label == "" {
		repl.label = repl.id
	}
	return SupersedeResult{Decision: old.label, Replacement: repl.label, Relation: relEdge.ID, Created: hasNew, Warnings: warnings}, nil
}

// RetireDecision takes a Decision out of force without a replacement (D189
// point 4): it moves to retired, with a required reason, under ratify over
// its place; an agent or a job is refused. From superseded it is the repair
// of a Decision superseded with no replacement recorded, and is refused when
// one is (decision.repair_not_needed). rev is the rev the caller read
// (T1010), nil for none.
func (a *App) RetireDecision(ctx context.Context, decision, reason string, rev *int) (map[string]any, error) {
	return a.RetireDecisionVia(ctx, "", decision, reason, rev)
}

// RetireDecisionVia is [App.RetireDecision] naming the surface.
func (a *App) RetireDecisionVia(ctx context.Context, surface, decision, reason string, rev *int) (map[string]any, error) {
	if strings.TrimSpace(reason) == "" {
		return nil, fieldErr(problemFieldRequired, "reason", "a reason is required to retire a Decision", nil)
	}
	return a.transitionItem(ctx, surface, "Decision", decision, decisionRetired, reason, rev)
}

// RepairRequest is one repair of a Decision superseded with no replacement
// recorded ([App.RepairSuperseded], D189 point 5). Exactly one of
// Replacement and Retire is set.
type RepairRequest struct {
	Decision string // the superseded Decision, by address, slug or ID
	Rev      *int   // its rev the caller read (T1010)
	Reason   string // required, kept in provenance
	// Replacement names the Decision that replaced it: one that is, or was,
	// in force. The supersedes relation is asserted onto the superseded one.
	Replacement string
	// Retire moves it to retired instead.
	Retire bool
}

// RepairSuperseded repairs a Decision that is superseded with no live
// supersedes relation pointing at it (D189 point 5), in one recorded act:
// it names its replacement, or retires it. It needs ratify over the
// Decision's place (and over the replacement's), refuses an agent or a job,
// and is refused for a Decision that already has its replacement recorded
// (decision.repair_not_needed) or is not superseded
// (decision.replacement_state). Nothing is rewritten silently: the relation
// and the move carry the reason in provenance.
func (a *App) RepairSuperseded(ctx context.Context, req RepairRequest) (map[string]any, error) {
	return a.RepairSupersededVia(ctx, "", req)
}

// RepairSupersededVia is [App.RepairSuperseded] naming the surface.
func (a *App) RepairSupersededVia(ctx context.Context, surface string, req RepairRequest) (map[string]any, error) {
	reason := strings.TrimSpace(req.Reason)
	hasRepl := strings.TrimSpace(req.Replacement) != ""
	switch {
	case reason == "":
		return nil, fieldErr(problemFieldRequired, "reason", "a reason is required to repair a superseded Decision", nil)
	case hasRepl == req.Retire:
		return nil, problem(problemDecisionSupersedeForm,
			fmt.Errorf("%w: name the replacement, or retire the Decision, not both and not neither", ErrBadRequest),
			problemValues{"act": "repair"})
	case a.cfg.DB == nil || a.relationStore == nil:
		return nil, fmt.Errorf("%w: RepairSuperseded needs a database and relations (App.Relations)", ErrInternal)
	}
	if err := a.requireDecisionGovernance("repairing a Decision"); err != nil {
		return nil, err
	}
	db := a.cfg.DB
	scope, err := scopeFor(ctx, a.governance)
	if err != nil {
		return nil, err
	}
	old, err := a.resolveDecision(ctx, scope, req.Decision)
	if err != nil {
		return nil, err
	}
	if old.status != "superseded" {
		return nil, problem(problemDecisionReplacementState,
			fmt.Errorf("%w: %s is %s; only a superseded Decision with no replacement recorded is repaired", ErrConflict, old.label, old.status),
			problemValues{"decision": old.label, "replacement": strings.TrimSpace(req.Replacement), "state": old.status})
	}
	if live, err := liveSupersedes(ctx, db, "", old.id); err != nil {
		return nil, err
	} else if live != "" {
		return nil, problem(problemDecisionRepairNotNeeded,
			fmt.Errorf("%w: %s already has its replacement recorded; there is nothing to repair", ErrConflict, old.label),
			problemValues{"decision": old.label})
	}
	if req.Retire {
		return a.transitionItem(ctx, surface, "Decision", req.Decision, decisionRetired, reason, req.Rev)
	}
	repl, err := a.resolveDecision(ctx, scope, req.Replacement)
	if err != nil {
		return nil, err
	}
	if repl.id == old.id || !decisionWasInForce(repl.status) {
		return nil, problem(problemDecisionReplacementState,
			fmt.Errorf("%w: %s is %s; a replacement is a Decision that is, or was, in force", ErrConflict, repl.label, repl.status),
			problemValues{"decision": old.label, "replacement": repl.label, "state": repl.status})
	}
	if err := refuseMutualSupersede(ctx, db, old, repl); err != nil {
		return nil, err
	}
	if err := a.requireRatifyOver(ctx, old, "naming the replacement of "+old.label, "repair"); err != nil {
		return nil, err
	}
	if err := a.requireRatifyOver(ctx, repl, "naming "+repl.label+" as the replacement of "+old.label, "repair"); err != nil {
		return nil, err
	}
	var warnings []CreateWarning
	w, err := a.decisionRev(ctx, old.id, req.Decision, "repair", req.Rev)
	if err != nil {
		return nil, err
	}
	if w != nil {
		warnings = append(warnings, *w)
	}
	actorID, _ := actorFromContext(ctx)
	var edge RelationEdge
	var isNew bool
	err = runInTx(ctx, db, func(exec DB) error {
		var err error
		if edge, isNew, err = writeEdgeRow(ctx, exec, RelationEdge{SourceType: "Decision", SourceID: repl.id,
			TargetType: "Decision", TargetID: old.id, RelationKind: "supersedes",
			EdgeClass: "asserted", Attributes: []byte("{}")}); err != nil {
			return err
		}
		// The rev advances, and the Decision must still be superseded.
		return setDecisionStatus(ctx, exec, old.id, old.label, req.Decision, "superseded", "superseded", actorID, req.Rev)
	})
	if err != nil {
		return nil, err
	}
	a.relationStore.announceEdgeFor(ctx, edge, isNew, surface, reason)
	result := map[string]any{"decision": old.label, "replacement": repl.label, "relation": edge.ID}
	if len(warnings) > 0 {
		result["warnings"] = warnings
	}
	return result, nil
}

// bareSupersedeCheck is D189 point 3 on the move of a Decision into
// superseded: with no live supersedes relation pointing at it, the move is
// refused from [decisionSupersedeCutover], and before it goes through with
// the warning this returns. Any other move returns nil.
func bareSupersedeCheck(ctx context.Context, db DB, typeName, id, from, to string) (*CreateWarning, error) {
	if typeName != "Decision" || to != "superseded" || !decisionInForce(from) {
		return nil, nil
	}
	live, err := liveSupersedes(ctx, db, "", id)
	if err != nil || live != "" {
		return nil, err
	}
	label := recordLabel(ctx, db, "smeldr_decisions", id)
	date := decisionSupersedeCutover.Format("2006-01-02")
	if !time.Now().UTC().Before(decisionSupersedeCutover) {
		return nil, problem(problemDecisionReplacementRequired,
			fmt.Errorf("%w: %s has no replacement recorded: supersede it with its replacement in one write (supersede_decision), or retire it (retire_decision)", ErrConflict, label),
			problemValues{"decision": label, "date": date})
	}
	return &CreateWarning{
		Code:    warningSupersedeWithoutReplacement,
		Field:   "to_state",
		Message: fmt.Sprintf("%s moved to superseded with no replacement recorded; from %s this is refused: supersede it with its replacement in one write (supersede_decision), or retire it (retire_decision)", label, date),
		Address: label,
	}, nil
}

// repairGuard refuses retiring a superseded Decision that has its
// replacement recorded (D189 point 5): only one superseded with none is
// retired.
func repairGuard(ctx context.Context, db DB, typeName, id, from, to string) error {
	if typeName != "Decision" || from != "superseded" || to != decisionRetired {
		return nil
	}
	live, err := liveSupersedes(ctx, db, "", id)
	if err != nil || live == "" {
		return err
	}
	label := recordLabel(ctx, db, "smeldr_decisions", id)
	return problem(problemDecisionRepairNotNeeded,
		fmt.Errorf("%w: %s has its replacement recorded, so it stays superseded", ErrConflict, label),
		problemValues{"decision": label})
}

// The detector of D189 point 5.
const decisionReplacementDetector = "decision-replacement"

// sweepDecisionReplacement records a finding for every Decision superseded
// with no live supersedes relation pointing at it, and for every Decision
// still in force that a live supersedes relation points at (a supersede left
// half done). At the end it deletes its own findings for Decisions it no
// longer flags, so a repaired one leaves the list (D51: a finding resolves
// when its detector stops firing). Other detectors' findings are untouched.
func (a *App) sweepDecisionReplacement(ctx context.Context) (flagged int, err error) {
	if a.findingStore == nil || a.cfg.DB == nil {
		return 0, nil
	}
	db := a.cfg.DB
	for _, t := range []string{"smeldr_decisions", "smeldr_relations", "smeldr_findings"} {
		ok, err := tableExists(ctx, db, t)
		if err != nil || !ok {
			return 0, err
		}
	}
	now := time.Now().UTC()
	rows, err := db.QueryContext(ctx, `
		SELECT d.id, d.status FROM smeldr_decisions d
		 WHERE (d.status = 'superseded' AND NOT EXISTS (
		          SELECT 1 FROM smeldr_relations r
		           WHERE r.relation_kind = 'supersedes' AND r.edge_class = 'asserted' AND r.target_id = d.id
		             AND (r.invalid_at IS NULL OR r.invalid_at > $1)))
		    OR (d.status IN ('ratified', 'pending-re-evaluation') AND EXISTS (
		          SELECT 1 FROM smeldr_relations r
		           WHERE r.relation_kind = 'supersedes' AND r.edge_class = 'asserted' AND r.target_id = d.id
		             AND (r.invalid_at IS NULL OR r.invalid_at > $1)))`, now)
	if err != nil {
		return 0, fmt.Errorf("%w: the decision-replacement sweep: %s", ErrInternal, err)
	}
	type hit struct{ id, status string }
	var hits []hit
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.id, &h.status); err != nil {
			rows.Close()
			return 0, fmt.Errorf("%w: the decision-replacement sweep: %s", ErrInternal, err)
		}
		hits = append(hits, h)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, fmt.Errorf("%w: the decision-replacement sweep: %s", ErrInternal, err)
	}
	keep := map[string]bool{}
	for _, h := range hits {
		msg := "superseded with no replacement recorded"
		if h.status != "superseded" {
			msg = "replacement recorded, but the replaced Decision is still in force"
		}
		if err := a.findingStore.Record(ctx, Finding{Detector: decisionReplacementDetector, SubjectType: "Decision",
			SubjectID: h.id, Provenance: "detected", Message: msg}); err != nil {
			return flagged, err
		}
		keep[h.id] = true
		flagged++
	}
	// Clear what no longer fires: read this detector's subjects, delete the
	// ones not flagged in this run.
	stale, err := db.QueryContext(ctx, `SELECT subject_id FROM smeldr_findings WHERE detector = $1`, decisionReplacementDetector)
	if err != nil {
		return flagged, fmt.Errorf("%w: the decision-replacement findings: %s", ErrInternal, err)
	}
	var drop []string
	for stale.Next() {
		var id string
		if err := stale.Scan(&id); err != nil {
			stale.Close()
			return flagged, fmt.Errorf("%w: the decision-replacement findings: %s", ErrInternal, err)
		}
		if !keep[id] {
			drop = append(drop, id)
		}
	}
	err = stale.Err()
	stale.Close()
	if err != nil {
		return flagged, fmt.Errorf("%w: the decision-replacement findings: %s", ErrInternal, err)
	}
	for _, id := range drop {
		if _, err := db.ExecContext(ctx, `DELETE FROM smeldr_findings WHERE detector = $1 AND subject_id = $2`, decisionReplacementDetector, id); err != nil {
			return flagged, fmt.Errorf("%w: clear a decision-replacement finding: %s", ErrInternal, err)
		}
	}
	return flagged, nil
}
