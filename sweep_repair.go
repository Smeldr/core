// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// sweepRepairJob is the job actor of the edges [App.RepairSweptEdges]
// restores.
const sweepRepairJob = "sweep-repair"

// SweepRepair is what one [App.RepairSweptEdges] run found, and did when it
// applied.
type SweepRepair struct {
	// Applied is whether the run wrote anything (false for a dry run).
	Applied bool `json:"applied"`
	// Candidates is how many ended rows the structural sweep ended.
	Candidates int `json:"candidates"`
	// Restored is how many relations were (or, in a dry run, would be)
	// restored as new rows.
	Restored int `json:"restored"`
	// SkippedDead is how many candidates have an end that is still not alive.
	SkippedDead int `json:"skipped_dead"`
	// SkippedLive is how many candidates already have a live row of the
	// same relation.
	SkippedLive int `json:"skipped_live"`
	// SkippedOlder is how many candidates are older rows of a relation
	// whose newest ended row is the one considered.
	SkippedOlder int `json:"skipped_older"`
	// SkippedSuperseded is how many candidates have a later row of the same
	// relation, ended for any reason (a person withdrew it, a rewrite ended
	// it): the relation was ended on purpose after the sweep, so it stays
	// ended.
	SkippedSuperseded int `json:"skipped_superseded"`
	// Failed is how many restores could not be checked or written; each is
	// logged.
	Failed int `json:"failed"`
	// ByType counts the restored relations per end type (both ends).
	ByType map[string]int `json:"by_type"`
	// ByKind counts the restored relations per relation kind.
	ByKind map[string]int `json:"by_kind"`
}

// RepairSweptEdges restores the relations the structural sweep ended while
// both their ends were alive (T932: until the fix a runtime-defined
// type with its own state flow, such as task_plan, counted as alive only when
// published, so every edge to or from one was ended each hour).
//
// A candidate is an ended row whose end was recorded as cause "swept" by
// "sweep-structural". It is restored when both ends are alive under today's
// rule, no live row of the same (source, target, kind) exists, and it is the
// newest row of that relation, whatever ended the others: a later row ended
// by a withdraw or a rewrite means the relation was ended on purpose, and it
// stays ended. A restore writes a new row with the old
// row's kind, class, confidence, attributes and job (the ended row stays as
// history) and records an "assert" provenance with actor job "sweep-repair".
// A restore sends no relation event and runs no resolver: it puts back a
// relation that was already true, so it never opens a condition and does
// not flood the event stream. With apply false (a dry run) nothing is
// written and the counts say what an apply would do. A second apply
// restores nothing. Reading the candidates fails as [ErrInternal]; a
// restore that fails is logged and counted, and the run goes on.
func (a *App) RepairSweptEdges(ctx context.Context, apply bool) (SweepRepair, error) {
	out := SweepRepair{Applied: apply, ByType: map[string]int{}, ByKind: map[string]int{}}
	if a.cfg.DB == nil {
		return out, fmt.Errorf("%w: RepairSweptEdges needs a database", ErrInternal)
	}
	db := a.cfg.DB
	now := time.Now().UTC()
	rows, err := db.QueryContext(ctx,
		`SELECT `+relationColumns+` FROM smeldr_relations r
		  WHERE r.invalid_at IS NOT NULL AND r.invalid_at <= $1
		    AND EXISTS (SELECT 1 FROM smeldr_provenance p
		                 WHERE p.subject_type = 'RelationEdge' AND p.subject_id = r.id
		                   AND p.verb = 'invalidate' AND p.to_state = $2 AND p.actor_id = $3)
		  ORDER BY r.source_type, r.source_id, r.target_type, r.target_id, r.relation_kind, r.invalid_at DESC, r.id DESC`,
		now, EdgeEndSwept, sweepStructuralJob)
	if err != nil {
		return out, fmt.Errorf("%w: RepairSweptEdges: %v", ErrInternal, err)
	}
	candidates, err := collectEdges(rows)
	rows.Close()
	if err != nil {
		return out, fmt.Errorf("%w: RepairSweptEdges: %v", ErrInternal, err)
	}
	out.Candidates = len(candidates)
	check := defaultTargetChecker(db)
	alive := map[[2]string]bool{}
	isAlive := func(typ, id string) (bool, error) {
		k := [2]string{typ, id}
		if v, ok := alive[k]; ok {
			return v, nil
		}
		v, err := check(ctx, typ, id)
		if err != nil {
			return false, err
		}
		alive[k] = v
		return v, nil
	}
	seen := map[[5]string]bool{}
	for _, e := range candidates {
		key := [5]string{e.SourceType, e.SourceID, e.TargetType, e.TargetID, e.RelationKind}
		if seen[key] {
			out.SkippedOlder++
			continue
		}
		seen[key] = true
		did, err := a.repairEdge(ctx, db, e, now, apply, isAlive)
		switch {
		case err != nil:
			out.Failed++
			slog.WarnContext(ctx, "smeldr: RepairSweptEdges: relation not restored", "edge_id", e.ID, "error", err)
		case did == "dead":
			out.SkippedDead++
		case did == "live":
			out.SkippedLive++
		case did == "superseded":
			out.SkippedSuperseded++
		default:
			out.Restored++
			out.ByKind[e.RelationKind]++
			out.ByType[e.SourceType]++
			out.ByType[e.TargetType]++
		}
	}
	return out, nil
}

// repairEdge restores one candidate, or says why not: "dead" (an end is not
// alive), "live" (a live row of the relation exists), "superseded" (a later
// row of the relation was ended, for any reason), or "restored".
func (a *App) repairEdge(ctx context.Context, db DB, e RelationEdge, now time.Time, apply bool, isAlive func(typ, id string) (bool, error)) (string, error) {
	for _, end := range [][2]string{{e.SourceType, e.SourceID}, {e.TargetType, e.TargetID}} {
		ok, err := isAlive(end[0], end[1])
		if err != nil {
			return "", err
		}
		if !ok {
			return "dead", nil
		}
	}
	// A later row is one ended after this one; a tie on the end time goes to
	// the greater id, as the candidate order does.
	var live, later int
	if err := db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(CASE WHEN invalid_at IS NULL OR invalid_at > $6 THEN 1 ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN invalid_at IS NOT NULL AND invalid_at <= $6 AND (invalid_at > $7 OR (invalid_at = $7 AND id > $8)) THEN 1 ELSE 0 END), 0)
		   FROM smeldr_relations WHERE source_type = $1 AND source_id = $2 AND target_type = $3 AND target_id = $4
		    AND relation_kind = $5 AND id <> $8`,
		e.SourceType, e.SourceID, e.TargetType, e.TargetID, e.RelationKind, now, e.InvalidAt.UTC(), e.ID).Scan(&live, &later); err != nil {
		return "", err
	}
	if live > 0 {
		return "live", nil
	}
	if later > 0 {
		return "superseded", nil
	}
	if !apply {
		return "restored", nil
	}
	restored := RelationEdge{
		SourceType: e.SourceType, SourceID: e.SourceID, TargetType: e.TargetType, TargetID: e.TargetID,
		RelationKind: e.RelationKind, EdgeClass: e.EdgeClass, Confidence: e.Confidence,
		CreatedByJob: e.CreatedByJob, Attributes: e.Attributes,
	}
	if len(restored.Attributes) == 0 {
		restored.Attributes = []byte("{}")
	}
	edge, _, err := writeEdgeRow(ctx, db, restored)
	if err != nil {
		return "", err
	}
	recordProvenance(ctx, a.provenanceStore, ProvenanceRecord{
		SubjectType: "RelationEdge", SubjectID: edge.ID, Verb: "assert",
		ActorKind: "job", ActorID: sweepRepairJob,
		Reason: "restored: ended by the structural sweep while its end was alive (T932)",
	})
	return "restored", nil
}
