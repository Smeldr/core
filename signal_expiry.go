package smeldr

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// DefaultSignalExpiryMaxAge is the default age (by CreatedAt) after which a
// pending or read Signal becomes eligible for expiry via [App.ExpireSignals].
const DefaultSignalExpiryMaxAge = 14 * 24 * time.Hour

// DefaultSignalExpiryBatchCap caps how many Signals [App.ExpireSignals]
// expires in a single call by default. One signal.transitioned webhook
// fires per expired Signal (same mechanism [App.TransitionItemWithReason]
// uses); an unbounded first run against an established instance's full
// backlog could flood webhook endpoints, and originally event-stream
// subscribers too (the stream no longer carries this event, see
// [eventStreamSuppressed]); found live: architect had 609 stale pending
// Signals as of 2026-09-28, manually marked read pending this mechanism's
// existence.
const DefaultSignalExpiryBatchCap = 200

// signalExpiryActor names the expiry sweep as the actor of its own state
// changes: stamped in last_actor and written as the ActorID of the
// [ProvenanceRecord] (ActorKind "job"), one name for the mechanism.
const signalExpiryActor = "signal-expiry-sweep"

// DefaultSignalExpiryExcludedTypes are signal_type values that represent a
// standing condition — closed only by being answered, never by growing
// old — always applied in addition to any types named in
// [SignalExpiryConfig.ExcludeTypes]; a caller cannot opt a standing-condition
// type back into expiry.
//
//   - "authorization-required" — emitted by core itself
//     (recordAuthorizationRequiredSignal, D42/D64) when a scheduled
//     transition is blocked pending an operation-gated decision.
//   - "review-requested" — not emitted by core code, but a real, live
//     signal_type agents already create via create_signal today (the
//     Plan governance loop's own review-request convention,
//     signal-patterns-v1.md) — excluding it is not a pre-emptive,
//     currently-inert designation, it protects Signals that exist on
//     live instances right now.
//   - "conflict-detected" — emitted by core itself
//     (RelationStore.emitConflictDetectedSignal, relations.go, D85/A354)
//     whenever a contradicts relation edge is asserted between two
//     Decisions; the contradiction stays open until someone resolves the
//     pair (Workspace W6 renders it as a row built from open Signals of
//     this type) — expiring it by age would silently hide a live
//     conflict, exactly the failure this exclusion mechanism exists to
//     prevent.
var DefaultSignalExpiryExcludedTypes = []string{
	"authorization-required",
	"review-requested",
	"conflict-detected",
}

// SignalExpiryConfig configures one call to [App.ExpireSignals]. The zero
// value is a safe, fully-defaulted configuration.
type SignalExpiryConfig struct {
	// MaxAge overrides [DefaultSignalExpiryMaxAge]. <= 0 means the default.
	MaxAge time.Duration
	// ExcludeTypes adds signal_type values to exclude, on top of — never
	// instead of — [DefaultSignalExpiryExcludedTypes].
	ExcludeTypes []string
	// BatchCap overrides [DefaultSignalExpiryBatchCap]. 0 means the
	// default; a negative value means unlimited (an explicit opt-out of
	// the safety cap).
	BatchCap int
}

// ExpireSignals moves [Signal] rows in "pending" or "read" whose CreatedAt
// is older than the configured max age to "expired" — the "retraction by
// time" state design/trace-lifecycle-semantics.md names but nothing ever
// implemented (found live: Signals accumulate pending/read forever;
// architect manually marked 609 stale ones read on 2026-09-28). Never
// deletes — Signals are Trace history, and orchestration records are never
// deleted.
//
// Each expiry is an ordinary transition: last_actor is stamped
// "signal-expiry-sweep", one [ProvenanceRecord] is written when [App.Provenance]
// is wired (ActorKind "job", the same actor name, surface "trigger", written
// only for a row whose UPDATE took effect), and a signal.transitioned webhook fires, exactly
// as a human-driven transition would, the same [dispatchTransitionWebhook]
// mechanism [App.TransitionItemWithReason] itself uses. Like that one, it
// is not published to the live event stream: see [eventStreamSuppressed]. ExpireSignals does not call
// TransitionItemWithReason directly: that method stamps last_actor from the
// caller's own [Context] (empty for a plain context.Context), whereas a
// scheduled detector should record an identifiable mechanism name — the
// same reasoning [App.DrainEvalQueue] already applies via its own raw
// UPDATE. No transition validation is needed beyond the query's own WHERE
// clause: [Signal]'s state flow allows both "pending" and "read" to reach
// "expired" unconditionally (no RequiredOperation/RequiredReason gate).
//
// Each row's UPDATE is guarded with "AND status = <the status this row had
// at SELECT time>", so a row a human (or anything else) transitions between
// the SELECT and this row's UPDATE — most importantly read -> acknowledged,
// terminal — is left untouched rather than silently overwritten with
// "expired": RowsAffected() == 0 means that race happened, and the row is
// counted as skipped with no signal.transitioned webhook, not as a partial or
// incorrect expiry.
//
// Returns walked (rows examined), expired (rows actually transitioned),
// skipped (an excluded type, the batch cap, a lost update race, or a
// per-row failure — logged, never fatal to the run), and err only for a
// whole-run failure (DB unset, query failure). A missing smeldr_signals
// table (ENABLE_ORCHESTRATION never wired) is not an error — (0, 0, 0,
// nil), matching [App.SweepStructural]/[App.DrainEvalQueue]'s own
// convention for an absent prerequisite table.
func (a *App) ExpireSignals(ctx context.Context, cfg SignalExpiryConfig) (walked, expired, skipped int, err error) {
	db := a.cfg.DB
	if db == nil {
		return 0, 0, 0, nil
	}
	maxAge := cfg.MaxAge
	if maxAge <= 0 {
		maxAge = DefaultSignalExpiryMaxAge
	}
	batchCap := cfg.BatchCap
	if batchCap == 0 {
		batchCap = DefaultSignalExpiryBatchCap
	}
	excluded := make(map[string]bool, len(DefaultSignalExpiryExcludedTypes)+len(cfg.ExcludeTypes))
	for _, t := range DefaultSignalExpiryExcludedTypes {
		excluded[t] = true
	}
	for _, t := range cfg.ExcludeTypes {
		excluded[t] = true
	}
	cutoff := time.Now().UTC().Add(-maxAge)

	rows, queryErr := db.QueryContext(ctx,
		`SELECT id, slug, signal_type, receiver, status FROM smeldr_signals
		 WHERE status IN ('pending', 'read') AND created_at <= $1
		 ORDER BY created_at ASC`,
		cutoff,
	)
	if isNoSuchTable(queryErr) {
		return 0, 0, 0, nil
	}
	if queryErr != nil {
		return 0, 0, 0, fmt.Errorf("smeldr: ExpireSignals: query: %w", queryErr)
	}
	defer rows.Close()

	type sigRow struct{ id, slug, signalType, receiver, status string }
	var pending []sigRow
	for rows.Next() {
		walked++
		var r sigRow
		if err := rows.Scan(&r.id, &r.slug, &r.signalType, &r.receiver, &r.status); err != nil {
			slog.WarnContext(ctx, "smeldr: ExpireSignals: scan", "error", err)
			skipped++
			continue
		}
		pending = append(pending, r)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return walked, 0, skipped, fmt.Errorf("smeldr: ExpireSignals: rows: %w", rowsErr)
	}
	rows.Close()

	now := time.Now().UTC()
	reason := fmt.Sprintf("expired by age: %d days", int(maxAge.Hours()/24))
	for _, r := range pending {
		if excluded[r.signalType] {
			skipped++
			continue
		}
		if batchCap > 0 && expired >= batchCap {
			skipped++
			continue
		}
		// AND status = $<n> (bound to r.status, the value seen at SELECT
		// time) guards against a row transitioning between the SELECT
		// above and this UPDATE — e.g. a human answering it (read ->
		// acknowledged, terminal) in the gap. Without this guard, the
		// blind UPDATE below would silently overwrite a terminal
		// acknowledged with expired: an illegal transition that discards
		// the real answer. RowsAffected == 0 means exactly that race
		// happened; the row is left untouched, counted as skipped, and no
		// signal.transitioned webhook fires for it (found in commit review,
		// architect).
		result, updateErr := db.ExecContext(ctx,
			"UPDATE smeldr_signals SET status = $1, updated_at = $2, last_actor = $3 WHERE id = $4 AND status = $5",
			"expired", now, signalExpiryActor, r.id, r.status,
		)
		if updateErr != nil {
			if !isNoSuchColumn(updateErr, "last_actor") {
				slog.WarnContext(ctx, "smeldr: ExpireSignals: UPDATE failed", "id", r.id, "error", updateErr)
				skipped++
				continue
			}
			result, updateErr = db.ExecContext(ctx,
				"UPDATE smeldr_signals SET status = $1, updated_at = $2 WHERE id = $3 AND status = $4",
				"expired", now, r.id, r.status,
			)
			if updateErr != nil {
				slog.WarnContext(ctx, "smeldr: ExpireSignals: UPDATE failed", "id", r.id, "error", updateErr)
				skipped++
				continue
			}
		}
		if n, raErr := result.RowsAffected(); raErr == nil && n == 0 {
			slog.DebugContext(ctx, "smeldr: ExpireSignals: row changed state before UPDATE, skipping",
				"id", r.id, "expected_status", r.status)
			skipped++
			continue
		}
		expired++
		// One name for the mechanism, the same one stamped in last_actor
		// above. Recorded only now that the UPDATE is known to have taken
		// effect (not the lost-race, skipped case). Bounded per run by
		// BatchCap, so at most that many synchronous INSERTs.
		recordStateChange(ctx, a.provenanceStore, stateChange{
			typeName:  "Signal",
			id:        r.id,
			from:      r.status,
			to:        "expired",
			reason:    reason,
			surface:   surfaceTrigger,
			actorKind: "job",
			actorID:   signalExpiryActor,
		})
		dispatchTransitionWebhook(ctx, a.webhookStore, a.webhookPool, a.eventBroadcaster, r.receiver,
			eventSignalTransitioned,
			transitionWebhookData{
				Type:      "signal",
				ID:        r.id,
				Slug:      r.slug,
				FromState: r.status,
				ToState:   "expired",
				Reason:    reason,
			})
	}
	return walked, expired, skipped, nil
}
