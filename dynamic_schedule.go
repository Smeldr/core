package smeldr

import (
	"errors"
	"log/slog"
	"time"
)

// dynamicScheduler publishes the due Scheduled items of every runtime-defined
// content type (T1004): one [schedulableModule] for all of
// smeldr_dynamic_content, added by [App.ServeDynamicContent], so the
// [Scheduler] needs no knowledge of each type. It mirrors
// [Module.processScheduled]: every due item in one tick, under the slug check
// and the conflict policy, with the same retry for a blocked item.
type dynamicScheduler struct{ app *App }

// processScheduled publishes every due Scheduled item across the runtime
// types and returns the soonest ScheduledAt still ahead (nil when none). One
// query reads the candidates; an item is loaded only when it is due.
func (d dynamicScheduler) processScheduled(ctx Context, now time.Time) (int, *time.Time, error) {
	rows, err := Query[*DynamicNode](ctx, d.app.cfg.DB,
		"SELECT id, type_name, scheduled_at FROM smeldr_dynamic_content WHERE status = $1",
		string(Scheduled))
	if err != nil {
		return 0, nil, err
	}
	published := 0
	var next *time.Time
	repos := map[string]*DynamicTypeRepo{}
	var rebuild []string
	noTime := map[string]int{}
	defer d.app.dynamicNoTimeOnce.Do(func() {
		for typeName, n := range noTime {
			warnScheduledWithoutTime(typeName, n)
		}
	})
	for _, row := range rows {
		if row.ScheduledAt == nil {
			noTime[row.TypeName]++
			continue
		}
		if row.ScheduledAt.After(now) {
			if next == nil || row.ScheduledAt.Before(*next) {
				t := *row.ScheduledAt
				next = &t
			}
			continue
		}
		repo, seen := repos[row.TypeName]
		if !seen {
			repo, err = d.app.DynamicContentRepo(row.TypeName)
			if err != nil {
				slog.Warn("smeldr: scheduler: runtime type not found; its scheduled items are skipped",
					"type", row.TypeName, "err", err)
			}
			repos[row.TypeName] = repo
		}
		if repo == nil {
			continue
		}
		switch repo.publishDue(ctx, row.ID, now) {
		case duePublished:
			rebuild = appendOnce(rebuild, row.TypeName)
			published++
		case dueBlocked:
			// Stays Scheduled and is tried again: the timer must not be set
			// from its own past ScheduledAt (see Module.processScheduled).
			if retry := now.Add(scheduledRetryInterval); next == nil || retry.Before(*next) {
				next = &retry
			}
		case dueSkipped:
			if retry := skipRetryAt(row.TypeName+"/"+row.ID, now); retry != nil && (next == nil || retry.Before(*next)) {
				next = retry
			}
		}
	}
	for _, typeName := range rebuild {
		if desc := d.app.typeRegistry.Lookup(typeName); desc != nil {
			d.app.rebuildDynamicSitemap(ctx, desc)
			d.app.rebuildDynamicAIIndex(ctx, desc)
		}
	}
	return published, next, nil
}

// appendOnce appends s to list unless list already holds it.
func appendOnce(list []string, s string) []string {
	for _, v := range list {
		if v == s {
			return list
		}
	}
	return append(list, s)
}

// publishDue moves the due item id from Scheduled to Published for the
// scheduler (T1004), as [Module.publishDue] does for a compiled type: the slug
// check, the conflict policy, the write with its superseded items, then the
// status-change record and announcement and the async triggers. The write
// applies only while the item is still Scheduled, so a move made since the
// scheduler read it wins. No flow check: the schedule already passed it.
func (r *DynamicTypeRepo) publishDue(ctx Context, id string, now time.Time) dueOutcome {
	node, err := r.GetByID(ctx, id)
	if err != nil {
		slog.Warn("smeldr: scheduler: scheduled item could not be read; it is retried on the next tick",
			"type", r.typeName, "id", id, "err", err)
		return dueSkipped
	}
	if node.Status != Scheduled {
		return dueSkipped
	}
	slugKey := "slug/" + r.typeName + "/" + id
	if err := r.publishedSlugConflict(ctx, node); err != nil {
		if errors.Is(err, ErrInternal) {
			slog.Warn("smeldr: scheduler: slug check failed; item stays scheduled and is retried",
				"type", r.typeName, "id", id, "err", err)
			return dueBlocked
		}
		if r.refApp != nil && r.refApp.signalSlugBlocked(ctx, r.typeName, id, node.Slug) {
			return dueBlocked
		}
		if _, seen := scheduledBlockedLogged.LoadOrStore(slugKey, struct{}{}); !seen {
			slog.Warn("smeldr: scheduler: scheduled item not published: its URL is taken; it stays scheduled until its slug changes or the URL is free",
				"type", r.typeName, "id", id, "slug", node.Slug, "err", err)
		}
		return dueBlocked
	}
	scheduledBlockedLogged.Delete(slugKey)
	key := r.typeName + "/" + id
	plan, err := planConflict(ctx, r.db, r.typeName, string(Published), id)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			if _, seen := scheduledBlockedLogged.LoadOrStore(key, struct{}{}); !seen {
				slog.Warn("smeldr: scheduler: scheduled item blocked by the conflict policy: it stays scheduled, past its time, and is retried every tick until the active item leaves the state",
					"type", r.typeName, "id", id, "err", err)
			}
			return dueBlocked
		}
		slog.Warn("smeldr: scheduler: conflict policy check failed; item stays scheduled and is retried on the next tick",
			"type", r.typeName, "id", id, "err", err)
		return dueBlocked
	}
	defer plan.release()
	hctx := plan.holding(ctx)
	actorID, _ := actorFromContext(ctx)
	written, err := r.writeStatus(hctx, plan, node, Published, now, now, actorID, surfaceTrigger, true)
	if err != nil {
		slog.Warn("smeldr: scheduler failed to publish item; skipping",
			"type", r.typeName, "id", id, "err", err)
		return dueSkipped
	}
	if !written {
		return dueSkipped
	}
	scheduledBlockedLogged.Delete(key)
	clearSkipRetry(key)
	changed := *node
	changed.Status = Published
	changed.PublishedAt = now
	changed.ScheduledAt = nil
	changed.UpdatedAt = now
	changed.LastActor = actorID
	r.statusChanged(hctx, &changed, Scheduled, "", surfaceTrigger)
	fireAsyncTriggers(ctx, r.db, r.typeName, string(Scheduled), string(Published), id)
	return duePublished
}
