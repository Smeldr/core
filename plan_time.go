package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// planType is the runtime-defined type of a Task's plan (D81).
const planType = "task_plan"

// planTimeJob names the system writer of a plan's plan-time edges (G30 phase
// 1, T1015): the plan's author never writes them.
const planTimeJob = "plan-time"

// EnsureTaskPlanFields adds the fields a plan declares its work with to the
// task_plan type, when the App has one (G30 phase 1, T1015): repository
// (owner/name), paths (path patterns in the coverage-rule language) and
// no_commit (the work ends in no commit, D88). It also creates the table that
// records when a plan's plan-time record was made. Idempotent; a no-op on an
// App with no task_plan type. Called on the server's boot path.
func EnsureTaskPlanFields(ctx context.Context, app *App) error {
	if app == nil || app.cfg.DB == nil {
		return nil
	}
	if _, err := app.cfg.DB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS smeldr_plan_time_records (
		plan_id     TEXT NOT NULL PRIMARY KEY,
		recorded_at TIMESTAMPTZ NOT NULL,
		transition  TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		return fmt.Errorf("smeldr: EnsureTaskPlanFields: %w", err)
	}
	desc := app.typeRegistry.Lookup(planType)
	if desc == nil || desc.Schema == nil {
		return nil
	}
	var fields []SchemaField
	if err := json.Unmarshal(desc.Schema.Fields, &fields); err != nil {
		return fmt.Errorf("smeldr: EnsureTaskPlanFields: read the task_plan schema: %w", err)
	}
	have := map[string]bool{}
	for _, f := range fields {
		have[f.Name] = true
	}
	added := false
	for _, f := range []SchemaField{
		{Name: "repository", Type: "string", Description: "The repository the plan's work changes, owner/name. Required to submit a plan whose work ends in a commit."},
		{Name: "paths", Type: "array", Description: "The paths the plan's work touches, as patterns in the coverage-rule language (* within a segment, ? one character, ** any number of segments). Required to submit a plan whose work ends in a commit."},
		{Name: "no_commit", Type: "boolean", Description: "True when the plan's work ends in no commit (a review, an investigation, D88): then repository and paths are not needed and no plan-time record is made."},
	} {
		if !have[f.Name] {
			fields = append(fields, f)
			added = true
		}
	}
	if !added {
		return nil
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return fmt.Errorf("smeldr: EnsureTaskPlanFields: %w", err)
	}
	schema := *desc.Schema
	schema.Fields = raw
	if _, err := app.RedefineContentType(ctx, &schema, "G30 phase 1 (T1015): plans declare repository, paths and no_commit"); err != nil {
		return fmt.Errorf("smeldr: EnsureTaskPlanFields: %w", err)
	}
	return nil
}

// planDeclaration is what a plan declares about its work.
type planDeclaration struct {
	Repository string   `json:"repository"`
	Paths      []string `json:"paths"`
	NoCommit   bool     `json:"no_commit"`
}

// planDeclarationOf reads a plan's declaration from its fields.
func planDeclarationOf(node *DynamicNode) planDeclaration {
	var d planDeclaration
	_ = json.Unmarshal(node.Fields, &d)
	return d
}

// planPathsRule applies the submit rule to a plan moving to submitted or
// approved (T1015): unless no_commit, repository and paths are required and
// each path must be a valid pattern. A missing declaration is a
// plan_paths_missing warning until cutover, a [ValidationError] after; an
// invalid one is always refused.
func planPathsRule(node *DynamicNode, cutover time.Time) (*CreateWarning, error) {
	d := planDeclarationOf(node)
	if d.NoCommit {
		return nil, nil
	}
	if len(d.Paths) > DecisionsForPathsMaxPaths {
		return nil, Err("paths", fmt.Sprintf("at most %d patterns", DecisionsForPathsMaxPaths))
	}
	for _, p := range d.Paths {
		if err := checkCoveragePattern(p); err != nil {
			var ve *ValidationError
			if errors.As(err, &ve) {
				return nil, Err("paths", fmt.Sprintf("%q is not a valid pattern: %s", p, err.Error()))
			}
			return nil, err
		}
	}
	if strings.TrimSpace(d.Repository) != "" {
		if _, err := normalizeRepository(d.Repository); err != nil {
			return nil, err
		}
	}
	field := ""
	switch {
	case strings.TrimSpace(d.Repository) == "":
		field = "repository"
	case len(d.Paths) == 0:
		field = "paths"
	default:
		return nil, nil
	}
	msg := "repository and paths are required to submit a plan whose work ends in a commit: the Areas and Decisions it rests on are recorded from them; set no_commit for work that ends in none"
	if !cutover.IsZero() && !time.Now().Before(cutover) {
		return nil, Err(field, msg)
	}
	return &CreateWarning{Code: "plan_paths_missing", Field: field, Message: msg}, nil
}

// recordPlanTime records the Areas a plan's declared paths fall in and the
// Decisions in force for them, as belongs_to_area and checked_against edges
// written by the system (T1015). The record made before is ended first, so the
// history keeps both. A plan with no_commit, or declaring nothing, gets no
// record. Read under an all-read system scope, as [App.CheckChange] does.
func (a *App) recordPlanTime(ctx context.Context, planID, transition string) error {
	repo, err := a.DynamicContentRepo(planType)
	if err != nil {
		return err
	}
	node, err := repo.GetByID(ctx, planID)
	if err != nil {
		return err
	}
	d := planDeclarationOf(node)
	if d.NoCommit || strings.TrimSpace(d.Repository) == "" || len(d.Paths) == 0 {
		return nil
	}
	sys := WithReadScope(ctx, AllReadScope())
	res, err := a.DecisionsForPaths(sys, DecisionsForPathsQuery{Repository: d.Repository, Paths: d.Paths, Patterns: true})
	if err != nil {
		return err
	}
	db := a.cfg.DB
	now := time.Now().UTC()
	exec, commit, rollback, err := conflictTx(ctx, db)
	if err != nil {
		return err
	}
	defer rollback()
	rows, err := exec.QueryContext(ctx,
		`SELECT id FROM smeldr_relations WHERE source_type = $1 AND source_id = $2 AND created_by_job = $3
		   AND relation_kind IN ('belongs_to_area', 'checked_against') AND (invalid_at IS NULL OR invalid_at > $4)`,
		planType, planID, planTimeJob, now)
	if err != nil {
		return err
	}
	var old []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		old = append(old, id)
	}
	rows.Close()
	for _, id := range old {
		if _, _, err := endEdgeRow(ctx, exec, id, now); err != nil {
			return err
		}
	}
	job := planTimeJob
	edge := func(targetType, targetID, kind string) error {
		_, _, err := writeEdgeRow(ctx, exec, RelationEdge{SourceType: planType, SourceID: planID, TargetType: targetType, TargetID: targetID,
			RelationKind: kind, EdgeClass: "asserted", CreatedByJob: &job, Attributes: json.RawMessage("{}")})
		return err
	}
	for _, area := range res.Areas {
		if err := edge("Area", area.ID, "belongs_to_area"); err != nil {
			return err
		}
	}
	for _, dec := range res.Decisions {
		if err := edge("Decision", dec.ID, "checked_against"); err != nil {
			return err
		}
	}
	if _, err := exec.ExecContext(ctx,
		`INSERT INTO smeldr_plan_time_records (plan_id, recorded_at, transition) VALUES ($1, $2, $3)
		 ON CONFLICT (plan_id) DO UPDATE SET recorded_at = EXCLUDED.recorded_at, transition = EXCLUDED.transition`,
		planID, now, transition); err != nil {
		return err
	}
	return commit()
}

// afterPlanTransition makes a plan's plan-time record on its move to
// submitted or approved (T1015). The move has committed, so a failure is
// logged and the plan's read says that no record exists.
func (a *App) afterPlanTransition(ctx context.Context, t statusTransition) {
	if t.typeName != planType || (t.to != "submitted" && t.to != "approved") || a.cfg.DB == nil {
		return
	}
	if err := a.recordPlanTime(ctx, t.id, t.to); err != nil {
		slog.ErrorContext(ctx, "smeldr: plan-time record not made; the plan's read shows that none exists",
			"plan", t.id, "transition", t.to, "err", err)
	}
}

// PlanTimeArea is an Area a plan's declared paths fall in.
type PlanTimeArea struct {
	ID      string `json:"id"`
	Address string `json:"address"`
	Name    string `json:"name"`
}

// PlanTimeDecision is a Decision in force for a plan's declared paths.
type PlanTimeDecision struct {
	ID      string `json:"id"`
	Address string `json:"address"`
	Title   string `json:"title"`
	State   string `json:"state"`
}

// PlanTimeRecord is what Smeldr recorded about a plan at submit and approve
// (G30 phase 1, T1015): the Areas its declared paths fall in and the Decisions
// in force for them, within the reader's read rights. Recorded is false when
// no record exists for a plan whose work ends in a commit, so a missing basis
// never reads as "no Decisions apply".
type PlanTimeRecord struct {
	NoCommit   bool               `json:"no_commit"`
	Recorded   bool               `json:"recorded"`
	RecordedAt *time.Time         `json:"recorded_at,omitempty"`
	Transition string             `json:"transition,omitempty"`
	Areas      []PlanTimeArea     `json:"areas"`
	Decisions  []PlanTimeDecision `json:"decisions"`
}

// PlanTimeRecord reads the plan-time record of the plan named by address,
// slug or ID. The plan must be readable ([ErrNotFound] otherwise); each Area
// and Decision is listed only when the caller may read it, and never counted
// otherwise.
func (a *App) PlanTimeRecord(ctx context.Context, plan string) (PlanTimeRecord, error) {
	out := PlanTimeRecord{Areas: []PlanTimeArea{}, Decisions: []PlanTimeDecision{}}
	repo, err := a.DynamicContentRepo(planType)
	if err != nil {
		return out, err
	}
	scope, err := scopeFor(ctx, a.governance)
	if err != nil {
		return out, err
	}
	node, err := repo.resolveFor(ctx, plan, func(n *DynamicNode) bool { return scope.Readable(planType, n.ID) })
	if err != nil {
		return out, err
	}
	out.NoCommit = planDeclarationOf(node).NoCommit
	db := a.cfg.DB
	var at time.Time
	var transition string
	err = db.QueryRowContext(ctx, `SELECT recorded_at, transition FROM smeldr_plan_time_records WHERE plan_id = $1`, node.ID).
		Scan(scanDest(&at), &transition)
	switch {
	case err == nil:
		out.Recorded, out.RecordedAt, out.Transition = true, &at, transition
	case isNoSuchTable(err) || errors.Is(err, sql.ErrNoRows):
	default:
		return out, fmt.Errorf("%w: the plan-time record of %s: %v", ErrInternal, plan, err)
	}
	now := time.Now().UTC()
	rows, err := db.QueryContext(ctx,
		`SELECT r.relation_kind, r.target_id FROM smeldr_relations r
		  WHERE r.source_type = $1 AND r.source_id = $2 AND r.created_by_job = $3
		    AND r.relation_kind IN ('belongs_to_area', 'checked_against') AND (r.invalid_at IS NULL OR r.invalid_at > $4)
		  ORDER BY r.relation_kind, r.target_id`, planType, node.ID, planTimeJob, now)
	if isNoSuchTable(err) {
		return out, nil // no relations on this instance: nothing was recorded
	}
	if err != nil {
		return out, fmt.Errorf("%w: the plan-time record of %s: %v", ErrInternal, plan, err)
	}
	type target struct{ kind, id string }
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.kind, &t.id); err != nil {
			rows.Close()
			return out, err
		}
		targets = append(targets, t)
	}
	rows.Close()
	for _, t := range targets {
		switch t.kind {
		case "belongs_to_area":
			if !scope.Readable("Area", t.id) {
				continue
			}
			ar := PlanTimeArea{ID: t.id}
			if err := db.QueryRowContext(ctx, `SELECT address, name FROM smeldr_areas WHERE id = $1`, t.id).Scan(&ar.Address, &ar.Name); err != nil {
				continue
			}
			out.Areas = append(out.Areas, ar)
		case "checked_against":
			if !scope.Readable("Decision", t.id) {
				continue
			}
			dec := PlanTimeDecision{ID: t.id}
			if err := db.QueryRowContext(ctx, `SELECT address, title, status FROM smeldr_decisions WHERE id = $1`, t.id).Scan(&dec.Address, &dec.Title, &dec.State); err != nil {
				continue
			}
			out.Decisions = append(out.Decisions, dec)
		}
	}
	return out, nil
}

// planTimeEdge reports whether an edge of kind from sourceType is one the
// plan-time record owns, which only the system writes (T1015).
func planTimeEdge(sourceType, kind string) bool {
	return sourceType == planType && (kind == "belongs_to_area" || kind == "checked_against")
}

// errPlanTimeEdge refuses an author's write of a plan-time edge.
func errPlanTimeEdge() error {
	return Refused("a plan's belongs_to_area and checked_against relations are recorded by Smeldr at submit and approve, from the plan's repository and paths; change those instead")
}

// planPathsCutover is the App's [Config.PlanPathsCutover], zero without one.
func (r *DynamicTypeRepo) planPathsCutover() time.Time {
	if r.refApp == nil {
		return time.Time{}
	}
	return r.refApp.cfg.PlanPathsCutover
}
