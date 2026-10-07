// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"
)

// StateSince is when an item entered the state it is in now, and the reason
// that transition carried. See [App.ItemsStateSince].
type StateSince struct {
	// Since is the time of the transition into the current state, in UTC. The
	// provenance record stores it with second resolution.
	Since time.Time
	// Reason is the free text the transition carried; empty when it carried none.
	Reason string
}

// transitionBatchReader is the optional capability a [ProvenanceStore] may
// have to answer [App.ItemsStateSince] in one query per chunk of ids. The
// default SQL store has it; a custom store without it is read through
// [ProvenanceStore.List], one id at a time.
type transitionBatchReader interface {
	latestTransitions(ctx context.Context, subjectType string, ids []string) (map[string]ProvenanceRecord, error)
}

var warnStateSinceFallback = sync.OnceFunc(func() {
	slog.Warn("smeldr: ItemsStateSince: the ProvenanceStore has no batch read; reading one item at a time through List")
})

// ItemsStateSince reports, for each item of typeName in current (item id to
// the item's current state), when it entered that state and why: the latest
// provenance record of verb "transition" for the item, returned only when its
// ToState equals the item's current state.
//
// An id is absent from the result when that is not so: provenance is not wired
// ([App.Provenance]), the item moved before provenance was switched on, or its
// status changed by a path that wrote no record. Absence therefore means
// unknown, never "has not moved". Two transitions in the same second are
// ordered by record id, which is time ordered.
//
// The actor of the transition is deliberately not part of the result; the
// provenance visibility rules for actors belong to [SubjectProvenance] and its
// readers. One query per 400 ids for the default store. A failed read returns
// [ErrInternal]. A nil store or an empty current yields an empty map.
func (a *App) ItemsStateSince(ctx context.Context, typeName string, current map[string]string) (map[string]StateSince, error) {
	out := map[string]StateSince{}
	if a.provenanceStore == nil || len(current) == 0 {
		return out, nil
	}
	ids := make([]string, 0, len(current))
	for id := range current {
		ids = append(ids, id)
	}
	var latest map[string]ProvenanceRecord
	var err error
	if r, ok := a.provenanceStore.(transitionBatchReader); ok {
		latest, err = r.latestTransitions(ctx, typeName, ids)
	} else {
		warnStateSinceFallback()
		latest, err = latestTransitionsViaList(ctx, a.provenanceStore, typeName, ids)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: ItemsStateSince: %s", ErrInternal, err)
	}
	for id, rec := range latest {
		if cur, ok := current[id]; ok && rec.ToState == cur {
			out[id] = StateSince{Since: rec.Timestamp.UTC(), Reason: rec.Reason}
		}
	}
	return out, nil
}

// newerTransition reports whether r supersedes best: a later timestamp, or the
// same second and a greater id.
func newerTransition(r, best ProvenanceRecord) bool {
	if !r.Timestamp.Equal(best.Timestamp) {
		return r.Timestamp.After(best.Timestamp)
	}
	return r.ID > best.ID
}

// latestTransitionsViaList is the fallback for a store without a batch read.
func latestTransitionsViaList(ctx context.Context, store ProvenanceStore, subjectType string, ids []string) (map[string]ProvenanceRecord, error) {
	out := make(map[string]ProvenanceRecord, len(ids))
	for _, id := range ids {
		recs, err := store.List(ctx, ProvenanceFilter{SubjectType: subjectType, SubjectID: id})
		if err != nil {
			return nil, err
		}
		for _, r := range recs {
			if r.Verb != "transition" {
				continue
			}
			if best, ok := out[id]; !ok || newerTransition(r, best) {
				out[id] = r
			}
		}
	}
	return out, nil
}

// latestTransitions implements [transitionBatchReader]: one query per
// standingChunk ids, the first row per subject of a newest-first ordering wins.
func (s *sqlProvenanceStore) latestTransitions(ctx context.Context, subjectType string, ids []string) (map[string]ProvenanceRecord, error) {
	out := make(map[string]ProvenanceRecord, len(ids))
	decided := make(map[string]struct{}, len(ids))
	for start := 0; start < len(ids); start += standingChunk {
		end := min(start+standingChunk, len(ids))
		chunk := ids[start:end]
		args := make([]any, 0, len(chunk)+1)
		args = append(args, subjectType)
		ph := make([]byte, 0, len(chunk)*5)
		for i, id := range chunk {
			if i > 0 {
				ph = append(ph, ',')
			}
			ph = append(ph, '$')
			ph = strconv.AppendInt(ph, int64(i+2), 10)
			args = append(args, id)
		}
		rows, err := s.db.QueryContext(ctx,
			`SELECT id, subject_id, timestamp, to_state, reason FROM smeldr_provenance
			 WHERE subject_type = $1 AND verb = 'transition' AND subject_id IN (`+string(ph)+`)
			 ORDER BY timestamp DESC, id DESC`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var r ProvenanceRecord
			var ts string
			if err := rows.Scan(&r.ID, &r.SubjectID, &ts, &r.ToState, &r.Reason); err != nil {
				rows.Close()
				return nil, err
			}
			if _, seen := decided[r.SubjectID]; seen {
				continue
			}
			// The newest record decides the subject. If its timestamp does not
			// parse the subject stays unknown: an older record must not stand in
			// for it, and a zero time must not be shown as a date.
			decided[r.SubjectID] = struct{}{}
			parsed, perr := time.Parse(time.RFC3339, ts)
			if perr != nil {
				slog.Warn("smeldr: ItemsStateSince: unparseable provenance timestamp, state left unknown",
					"subject_type", subjectType, "subject_id", r.SubjectID, "timestamp", ts)
				continue
			}
			r.Timestamp = parsed
			out[r.SubjectID] = r
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
