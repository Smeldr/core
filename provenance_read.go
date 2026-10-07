// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"fmt"
)

// ProvenanceAudience is who an item's history is read for (D101).
type ProvenanceAudience string

const (
	// ProvenanceMembers is the organisation's own members: every entry carries
	// its actor (kind and id), surface and reason. On a core instance, which is
	// one organisation, that is every caller that passes the read tool's own
	// authorization.
	ProvenanceMembers ProvenanceAudience = "members"

	// ProvenanceGated is anyone else: an entry carries its actor only when the
	// transition required RequiredOperation with Strict enforcement, the rule of
	// [SubjectProvenance]. An ungated entry is a word and a date.
	ProvenanceGated ProvenanceAudience = "gated"
)

// Paging bounds of [App.ItemProvenance]: a limit of 0 means the default, and a
// larger one is capped, like list_signals.
const (
	defaultProvenanceLimit = 50
	maxProvenanceLimit     = 500
)

// ProvenancePage is one page of an item's history, newest first. Total is the
// number of entries the item has, not the number on the page.
type ProvenancePage struct {
	Entries []ProvenanceEntry
	Total   int
}

// ItemProvenance reads one item's history, newest first, for audience:
// time, verb, from and to state, and (by the audience's rule) actor, surface and
// reason. Standing changes ("standing-began", "standing-ended") are entries like
// any other. typeName is the registered type name ("Decision") and itemID the
// item's ID, as [SubjectProvenance] takes them.
//
// limit 0 means 50 and a larger limit is capped at 500; offset must not be
// negative; an offset past the end is an empty page with the true Total. An
// unknown audience is an error, never the wider view.
//
// Errors are explicit: with no provenance store wired ([App.Provenance]) the
// error is [ErrNotFound] saying provenance is not enabled, never an empty page,
// so "no history" and "not recorded here" stay different statements. A genuine
// database error while deciding whether a transition was gated is [ErrInternal]:
// visibility is never decided by a swallowed error (contrast the fail-closed
// [SubjectProvenance]). A type with no flow, or a transition its flow does not
// declare, is not an error: not gated.
//
// Relation events (assert, invalidate) are recorded against the RelationEdge,
// not the item, and are not part of this read.
func (a *App) ItemProvenance(ctx context.Context, typeName, itemID string, audience ProvenanceAudience, limit, offset int) (ProvenancePage, error) {
	if audience != ProvenanceMembers && audience != ProvenanceGated {
		return ProvenancePage{}, Err("audience", `must be "members" or "gated"`)
	}
	if offset < 0 {
		return ProvenancePage{}, Err("offset", "must not be negative")
	}
	if limit < 0 {
		return ProvenancePage{}, Err("limit", "must not be negative")
	}
	if a.provenanceStore == nil {
		return ProvenancePage{}, fmt.Errorf("%w: provenance is not enabled on this instance", ErrNotFound)
	}
	if limit == 0 {
		limit = defaultProvenanceLimit
	}
	limit = min(limit, maxProvenanceLimit)

	records, err := a.provenanceStore.List(ctx, ProvenanceFilter{SubjectType: typeName, SubjectID: itemID})
	if err != nil {
		return ProvenancePage{}, fmt.Errorf("%w: ItemProvenance: %s", ErrInternal, err)
	}
	db := a.cfg.DB
	page := ProvenancePage{Total: len(records), Entries: []ProvenanceEntry{}}
	if offset >= len(records) {
		return page, nil
	}
	end := min(offset+limit, len(records))
	// Many entries share one edge (every edit is from == to): look each up once.
	gates := map[[2]string]bool{}
	for _, r := range records[offset:end] {
		key := [2]string{r.FromState, r.ToState}
		gated, seen := gates[key]
		if !seen {
			g, err := transitionGate(ctx, db, typeName, r.FromState, r.ToState)
			if err != nil {
				return ProvenancePage{}, fmt.Errorf("%w: ItemProvenance: gate lookup: %s", ErrInternal, err)
			}
			gated = g
			gates[key] = g
		}
		e := ProvenanceEntry{
			Timestamp: r.Timestamp,
			Verb:      r.Verb,
			FromState: r.FromState,
			ToState:   r.ToState,
			Gated:     gated,
		}
		if audience == ProvenanceMembers || gated {
			e.ActorKind, e.ActorID, e.Surface, e.Reason = r.ActorKind, r.ActorID, r.Surface, r.Reason
		}
		page.Entries = append(page.Entries, e)
	}
	return page, nil
}
