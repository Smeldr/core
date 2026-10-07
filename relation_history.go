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

// The causes an ended relation can carry: the closed set a relation's
// "invalidate" [ProvenanceRecord] names in its ToState, and [EdgeEnd.Cause] on
// a read. One row of smeldr_relations is one life of a relation; these say how
// a life ended.
const (
	// EdgeEndWithdrawn: ended on purpose through [RelationStore.Withdraw]
	// (the withdraw_relation tool), by the caller, with a reason.
	EdgeEndWithdrawn = "withdrawn"
	// EdgeEndSwept: ended by [RelationStore.SweepStructural] because its
	// source or target is no longer alive (job "sweep-structural").
	EdgeEndSwept = "swept"
	// EdgeEndRecomputed: ended because the content field that derived it
	// changed ([RelationStore.RecomputeAsserted], [RelationStore.BulkRecompute]).
	EdgeEndRecomputed = "recomputed"
	// EdgeEndAmendmentRejected: an Amendment's "amends" edge, ended because
	// the Amendment was rejected (D102).
	EdgeEndAmendmentRejected = "amendment-rejected"
	// EdgeEndPurged: removed by the administrative purge [RelationStore.Delete].
	EdgeEndPurged = "purged"
	// EdgeEndNotRecorded: the row has ended but no record says how. Every end
	// before relation history (core v1.128.0) reads this way; nothing is
	// backfilled or invented.
	EdgeEndNotRecorded = "not-recorded"
)

// sweepStructuralJob is the job actor of the structural sweep's ends, the same
// id a [SweepRunRecord] of that sweep carries (A289 naming).
const sweepStructuralJob = "sweep-structural"

// relationRecomputeJob is the job actor of a recompute that runs with no
// caller in its context (a bulk import through [RelationStore.BulkRecompute]).
const relationRecomputeJob = "relation-recompute"

// EdgeEnd says how and when an ended relation ended, read from its
// "invalidate" [ProvenanceRecord]. Cause is one of the EdgeEnd* constants;
// [EdgeEndNotRecorded] when no record exists, and then only At is set.
type EdgeEnd struct {
	Cause     string
	At        time.Time
	Reason    string
	ActorKind string
	ActorID   string
}

// edgeEnding is the record an end writes: cause, reason and actor.
type edgeEnding struct {
	cause, reason, actorKind, actorID string
}

// endingFromContext names ctx's caller as the actor of an end.
func endingFromContext(ctx context.Context, cause, reason string) edgeEnding {
	id, kind := actorFromContext(ctx)
	return edgeEnding{cause: cause, reason: reason, actorKind: kind, actorID: id}
}

// recomputeEnding names the content write's caller as the actor of a
// recompute, or the recompute job when ctx carries no caller.
func recomputeEnding(ctx context.Context) edgeEnding {
	e := endingFromContext(ctx, EdgeEndRecomputed, "the content field that derived it changed")
	if e.actorID == "" {
		e.actorKind, e.actorID = "job", relationRecomputeJob
	}
	return e
}

// edgeRowQuerier is what [endEdgeRow] runs on: the store's DB or a
// transaction.
type edgeRowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// endEdgeRow ends the live row id at now: it sets invalid_at only when the row
// is still live, and reports whether it did, returning the row's identity
// (kind, class, both ends) in the same round trip for the end's record and
// event. It writes no record; see [RelationStore.recordEdgeEnd], which the
// caller runs once the end is committed.
func endEdgeRow(ctx context.Context, q edgeRowQuerier, id string, now time.Time) (RelationEdge, bool, error) {
	var e RelationEdge
	err := q.QueryRowContext(ctx,
		"UPDATE smeldr_relations SET invalid_at=$1, updated_at=$2 WHERE id=$3 AND (invalid_at IS NULL OR invalid_at > $4) "+
			"RETURNING id, relation_kind, edge_class, source_type, source_id, target_type, target_id",
		now, now, id, now).Scan(&e.ID, &e.RelationKind, &e.EdgeClass, &e.SourceType, &e.SourceID, &e.TargetType, &e.TargetID)
	if errors.Is(err, sql.ErrNoRows) {
		return RelationEdge{}, false, nil
	}
	if err != nil {
		return RelationEdge{}, false, err
	}
	return e, true, nil
}

// recordEdgeEnd writes the "invalidate" provenance record of an ended relation
// row (subject RelationEdge, from "live" to the cause) and sends
// relation.ended. Every end passes here, so the record and the event cannot
// disagree. Fail-open (the end is already written): no provenance store means
// no record, no sink means no event.
func (s *RelationStore) recordEdgeEnd(ctx context.Context, edge RelationEdge, e edgeEnding) {
	s.emitRelationEvent(ctx, eventRelationEnded, edge, nil, e)
	recordProvenance(ctx, s.provenanceStore, ProvenanceRecord{
		SubjectType: "RelationEdge",
		SubjectID:   edge.ID,
		Verb:        "invalidate",
		FromState:   "live",
		ToState:     e.cause,
		ActorKind:   e.actorKind,
		ActorID:     e.actorID,
		Reason:      e.reason,
	})
}

// endEdge ends the live row id now and records how. It reports whether a live
// row was ended; an unknown or already ended row ends nothing and records
// nothing.
func (s *RelationStore) endEdge(ctx context.Context, id string, e edgeEnding) (bool, error) {
	edge, ended, err := endEdgeRow(ctx, s.db, id, time.Now().UTC())
	if err != nil || !ended {
		return false, err
	}
	s.recordEdgeEnd(ctx, edge, e)
	return true, nil
}

// emitRelationEvent sends a relation event (asserted, or ended with e's cause)
// to the event stream's [eventStreamChannelRelations] topic and to webhook
// endpoints subscribed to it, skipping the causing actor's own stream
// connection. newRow is set on relation.asserted only.
func (s *RelationStore) emitRelationEvent(ctx context.Context, eventName string, edge RelationEdge, newRow *bool, e edgeEnding) {
	if s.eventBroadcaster == nil && (s.webhookStore == nil || s.webhookPool == nil) {
		return
	}
	data := relationEventData{
		Type: "relation", ID: edge.ID, RelationKind: edge.RelationKind, EdgeClass: edge.EdgeClass,
		SourceType: edge.SourceType, SourceID: edge.SourceID, TargetType: edge.TargetType, TargetID: edge.TargetID,
		NewRow: newRow, Cause: e.cause, Reason: e.reason, ActorID: e.actorID, ActorKind: e.actorKind,
	}
	// The stream skips the causing token's own connection; a job actor is not
	// a token, so it skips nobody.
	origin := e.actorID
	if e.actorKind == "job" {
		origin = ""
	}
	dispatchEventFrom(ctx, s.webhookStore, s.webhookPool, s.eventBroadcaster, origin, eventStreamChannelRelations, eventName, data)
}

// assertEnding names ctx's caller (or the edge's job) as the actor of an
// assert event.
func assertEnding(ctx context.Context, edge RelationEdge) edgeEnding {
	if edge.CreatedByJob != nil && *edge.CreatedByJob != "" {
		return edgeEnding{actorKind: "job", actorID: *edge.CreatedByJob}
	}
	return endingFromContext(ctx, "", "")
}

// Withdraw ends the live relation id on purpose: its row stays, as history,
// with invalid_at set to now, and an "invalidate" provenance record names the
// caller (from ctx), the reason and the cause [EdgeEndWithdrawn]. Asserting
// the same relation again later starts a new row, a new life.
//
// reason is required. An unknown id is [ErrNotFound]; a relation that has
// already ended is [ErrConflict] naming when, never a silent success.
func (s *RelationStore) Withdraw(ctx context.Context, id, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return Err("reason", "is required to withdraw a relation")
	}
	ended, err := s.endEdge(ctx, id, endingFromContext(ctx, EdgeEndWithdrawn, reason))
	if err != nil {
		return fmt.Errorf("%w: withdraw relation %s: %s", ErrInternal, id, err)
	}
	if ended {
		return nil
	}
	var at *time.Time
	err = s.db.QueryRowContext(ctx, "SELECT invalid_at FROM smeldr_relations WHERE id=$1", id).
		Scan(nullTimeScanner{dst: &at})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("%w: relation %s", ErrNotFound, id)
	case err != nil:
		return fmt.Errorf("%w: withdraw relation %s: %s", ErrInternal, id, err)
	case at == nil:
		// Live a moment ago and not ended by this call: lost a race with a
		// concurrent re-assert of an explicit future end. Report it as ended.
		return fmt.Errorf("%w: relation %s could not be withdrawn", ErrConflict, id)
	}
	return fmt.Errorf("%w: relation %s already ended at %s", ErrConflict, id, at.UTC().Format(time.RFC3339))
}

// provenanceBySubjects is an optional [ProvenanceStore] capability: the
// records of several subjects of one type and verb in one query. The default
// SQL store has it; a custom store without it is read one subject at a time.
type provenanceBySubjects interface {
	listBySubjects(ctx context.Context, subjectType, verb string, ids []string) ([]ProvenanceRecord, error)
}

// listBySubjects implements [provenanceBySubjects] for the default store.
func (s *sqlProvenanceStore) listBySubjects(ctx context.Context, subjectType, verb string, ids []string) ([]ProvenanceRecord, error) {
	args := []any{subjectType, verb}
	ph := make([]string, len(ids))
	for i, id := range ids {
		args = append(args, id)
		ph[i] = fmt.Sprintf("$%d", i+3)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, timestamp, subject_type, subject_id, verb, from_state, to_state, actor_kind, actor_id, surface, reason
		 FROM smeldr_provenance WHERE subject_type = $1 AND verb = $2 AND subject_id IN (`+strings.Join(ph, ",")+`)`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProvenanceRows(rows)
}

// fillEdgeEnds sets Ended on every edge in edges that has ended by now, from
// its "invalidate" provenance record ([EdgeEndNotRecorded] when it has none).
// One query for the whole set with the default store. A read failure leaves
// the causes unread (Ended stays nil) and is returned.
func (s *RelationStore) fillEdgeEnds(ctx context.Context, edges []RelationEdge) error {
	now := time.Now().UTC()
	var ids []string
	for _, e := range edges {
		if e.InvalidAt != nil && !e.InvalidAt.After(now) {
			ids = append(ids, e.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	records := map[string]ProvenanceRecord{}
	if s.provenanceStore != nil {
		var recs []ProvenanceRecord
		if bs, ok := s.provenanceStore.(provenanceBySubjects); ok {
			var err error
			if recs, err = bs.listBySubjects(ctx, "RelationEdge", "invalidate", ids); err != nil {
				return err
			}
		} else {
			for _, id := range ids {
				got, err := s.provenanceStore.List(ctx, ProvenanceFilter{SubjectType: "RelationEdge", SubjectID: id})
				if err != nil {
					return err
				}
				recs = append(recs, got...)
			}
		}
		for _, r := range recs {
			if r.Verb != "invalidate" {
				continue
			}
			// A row ends once; should several records exist, the latest wins.
			if prev, ok := records[r.SubjectID]; !ok || r.Timestamp.After(prev.Timestamp) {
				records[r.SubjectID] = r
			}
		}
	}
	for i := range edges {
		e := &edges[i]
		if e.InvalidAt == nil || e.InvalidAt.After(now) {
			continue
		}
		end := &EdgeEnd{Cause: EdgeEndNotRecorded, At: *e.InvalidAt}
		if r, ok := records[e.ID]; ok && r.ToState != "" {
			end.Cause, end.Reason, end.ActorKind, end.ActorID = r.ToState, r.Reason, r.ActorKind, r.ActorID
		}
		e.Ended = end
	}
	return nil
}
