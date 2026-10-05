// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Standing is whether an item's claim is in force (D100): a state of a
// [StateFlow] may declare [StandingHolds] (see [State.Standing]), and each item
// of a type with such a state stores its own standing, set only by the code
// that changes the item's state. The same type is the flow's tag
// ([StandingHolds] or empty) and the stored value. It is separate from the
// governed state name (D56): the state name says where the item is in its
// lifecycle, standing says whether it is in force.
type Standing string

const (
	// StandingHolds: the item is in force. As a [State.Standing] tag, the only
	// legal one; as a stored value, the item is currently in a state tagged
	// with it.
	StandingHolds Standing = "holds"

	// StandingCeased: the item held and no longer does.
	StandingCeased Standing = "ceased"

	// StandingNone: the item never held. It is also what [ItemStanding]
	// reports for an item that has no stored row.
	StandingNone Standing = "none"

	// StandingNotRecorded: the item's history would decide whether it ever
	// held, and it was not recorded (an item that existed before standing was
	// recorded, see [MigrateStanding]). Nothing is guessed.
	StandingNotRecorded Standing = "not recorded"
)

// Standing event verbs written as [ProvenanceRecord]s next to the transition's
// own record, with the same actor, surface, reason and from/to states (so
// [SubjectProvenance] gates them exactly like the transition).
const (
	verbStandingBegan = "standing-began"
	verbStandingEnded = "standing-ended"
)

// holdsStates returns the names of the type's flow states tagged
// [StandingHolds]. A type with no registered flow, no tagged state, or a
// failing lookup yields an empty set: standing is simply not tracked for it
// (fail-open, a lookup failure never fails the change being recorded).
func holdsStates(ctx context.Context, db DB, typeName string) map[string]bool {
	rows, err := db.QueryContext(ctx,
		`SELECT s.name FROM smeldr_states s JOIN smeldr_state_flows f ON f.id = s.flow_id
		 WHERE f.type_name = $1 AND s.standing = $2`, typeName, string(StandingHolds))
	if err != nil {
		if !isNoSuchTable(err) && !isNoSuchColumn(err, "standing") {
			slog.WarnContext(ctx, "smeldr: standing: tagged-state lookup failed", "type", typeName, "error", err)
		}
		return nil
	}
	defer rows.Close()
	var out map[string]bool
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			continue
		}
		if out == nil {
			out = map[string]bool{}
		}
		out[name] = true
	}
	return out
}

// applyStateChange is what every state-change path calls after the item's
// status UPDATE succeeded: the transition's own [ProvenanceRecord]
// ([recordStateChange]) and the item's standing ([writeStanding]).
func applyStateChange(ctx context.Context, db DB, prov ProvenanceStore, c stateChange) {
	recordStateChange(ctx, prov, c)
	writeStanding(ctx, db, prov, c)
}

// writeStanding is the only writer of an item's stored standing. When the
// type's flow tags a state [StandingHolds]:
//   - entering a tagged state from an untagged one (or from nothing, on
//     creation) stores [StandingHolds] and records a "standing-began" event;
//   - leaving a tagged state for an untagged one stores [StandingCeased] and
//     records a "standing-ended" event;
//   - any other move (untagged to untagged, tagged to tagged, no move) changes
//     nothing, so an item that never held stays [StandingNone] and a ceased item
//     stays ceased.
//
// Events go through [recordStateChange], so they carry the same actor, surface,
// reason and from/to states as the transition and exist only when a provenance
// store is wired; the stored value is written regardless. Fail-open: a failure
// is logged and never fails the change (the drift check,
// [App.CheckStandingDrift], is the backstop). A type whose flow has no tagged
// state is a single lookup and no write. Call it only after the status UPDATE
// succeeded.
func writeStanding(ctx context.Context, db DB, prov ProvenanceStore, c stateChange) {
	if db == nil || c.typeName == "" || c.id == "" || c.from == c.to {
		return
	}
	tagged := holdsStates(ctx, db, c.typeName)
	if len(tagged) == 0 {
		return
	}
	var value Standing
	var verb string
	switch fromT, toT := tagged[c.from], tagged[c.to]; {
	case !fromT && toT:
		value, verb = StandingHolds, verbStandingBegan
	case fromT && !toT:
		value, verb = StandingCeased, verbStandingEnded
	default:
		return
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_standing (subject_type, subject_id, standing, updated_at) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (subject_type, subject_id) DO UPDATE SET standing = EXCLUDED.standing, updated_at = EXCLUDED.updated_at`,
		c.typeName, c.id, string(value), time.Now().UTC(),
	); err != nil {
		slog.WarnContext(ctx, "smeldr: standing: write failed, standing not recorded for this change",
			"type", c.typeName, "id", c.id, "standing", value, "error", err)
		return
	}
	ev := c
	ev.verb = verb
	recordStateChange(ctx, prov, ev)
}

// forgetStanding removes an item's stored standing when the item is deleted.
// Fail-open and a no-op without a database.
func forgetStanding(ctx context.Context, db DB, typeName, id string) {
	if db == nil || typeName == "" || id == "" {
		return
	}
	if _, err := db.ExecContext(ctx,
		`DELETE FROM smeldr_standing WHERE subject_type = $1 AND subject_id = $2`, typeName, id,
	); err != nil && !isNoSuchTable(err) {
		slog.WarnContext(ctx, "smeldr: standing: delete failed", "type", typeName, "id", id, "error", err)
	}
}

// ItemStanding reports the stored standing of the item (typeName, id). The
// second result is false when the type's flow tags no state [StandingHolds]:
// such a type has no standing at all (Signal, Task and Goal among Smeldr's
// own), and the first result is then empty. For a type that has standing, an
// item without a stored row reports [StandingNone]. A plain function over a
// [DB] handle, like [SubjectProvenance], for a caller that already holds one.
func ItemStanding(ctx context.Context, db DB, typeName, id string) (Standing, bool, error) {
	if len(holdsStates(ctx, db, typeName)) == 0 {
		return "", false, nil
	}
	var s string
	err := db.QueryRowContext(ctx,
		`SELECT standing FROM smeldr_standing WHERE subject_type = $1 AND subject_id = $2`, typeName, id).Scan(&s)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return StandingNone, true, nil
	case err != nil:
		return "", true, fmt.Errorf("%w: ItemStanding: %s", ErrInternal, err)
	}
	return Standing(s), true, nil
}

// CountStanding counts the stored standing rows of typeName by value (for
// example how many items hold). An item without a row is [StandingNone] and is
// not counted here. Returns an empty map for a type with no standing.
func CountStanding(ctx context.Context, db DB, typeName string) (map[Standing]int, error) {
	out := map[Standing]int{}
	if len(holdsStates(ctx, db, typeName)) == 0 {
		return out, nil
	}
	rows, err := db.QueryContext(ctx,
		`SELECT standing, COUNT(*) FROM smeldr_standing WHERE subject_type = $1 GROUP BY standing`, typeName)
	if err != nil {
		return nil, fmt.Errorf("%w: CountStanding: %s", ErrInternal, err)
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			return nil, fmt.Errorf("%w: CountStanding: %s", ErrInternal, err)
		}
		out[Standing(s)] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: CountStanding: %s", ErrInternal, err)
	}
	return out, nil
}

// flowGraph is one type's flow as registered: its states with their tags and
// the directed transitions between them.
type flowGraph struct {
	typeName    string
	initial     map[string]bool
	tagged      map[string]bool
	states      []string
	transitions [][2]string
}

// loadFlowGraphs returns the flow of every type that tags at least one state
// [StandingHolds], ordered by type name.
func loadFlowGraphs(ctx context.Context, db DB) ([]flowGraph, error) {
	frows, err := db.QueryContext(ctx,
		`SELECT f.id, f.type_name FROM smeldr_state_flows f
		 WHERE f.type_name IS NOT NULL
		   AND EXISTS (SELECT 1 FROM smeldr_states s WHERE s.flow_id = f.id AND s.standing = $1)
		 ORDER BY f.type_name`, string(StandingHolds))
	if err != nil {
		return nil, err
	}
	type flowRow struct{ id, typeName string }
	var flows []flowRow
	for frows.Next() {
		var r flowRow
		if err := frows.Scan(&r.id, &r.typeName); err != nil {
			frows.Close()
			return nil, err
		}
		flows = append(flows, r)
	}
	frows.Close()
	if err := frows.Err(); err != nil {
		return nil, err
	}
	var out []flowGraph
	for _, f := range flows {
		g := flowGraph{typeName: f.typeName, initial: map[string]bool{}, tagged: map[string]bool{}}
		srows, err := db.QueryContext(ctx,
			`SELECT name, is_initial, standing FROM smeldr_states WHERE flow_id = $1 ORDER BY name`, f.id)
		if err != nil {
			return nil, err
		}
		for srows.Next() {
			var name, standing string
			var initial bool
			if err := srows.Scan(&name, &initial, &standing); err != nil {
				srows.Close()
				return nil, err
			}
			g.states = append(g.states, name)
			g.initial[name] = initial
			g.tagged[name] = Standing(standing) == StandingHolds
		}
		srows.Close()
		if err := srows.Err(); err != nil {
			return nil, err
		}
		trows, err := db.QueryContext(ctx,
			`SELECT from_state, to_state FROM smeldr_transitions WHERE flow_id = $1`, f.id)
		if err != nil {
			return nil, err
		}
		for trows.Next() {
			var from, to string
			if err := trows.Scan(&from, &to); err != nil {
				trows.Close()
				return nil, err
			}
			g.transitions = append(g.transitions, [2]string{from, to})
		}
		trows.Close()
		if err := trows.Err(); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, nil
}

// classify says what an item sitting in each state of the flow, as registered
// now, can be known to be, from the flow graph alone (D100 point 5, no
// guessing):
//   - a tagged state: [StandingHolds];
//   - a state no path from any tagged state reaches: [StandingNone] (such an
//     item cannot have held under this flow);
//   - a non-initial state whose every incoming transition leaves a tagged
//     state: [StandingCeased] (it is only reachable from a holding state);
//   - anything else: [StandingNotRecorded] (its history would decide it).
func (g flowGraph) classify() map[string]Standing {
	reach := map[string]bool{}
	queue := []string{}
	for s := range g.tagged {
		if g.tagged[s] {
			queue = append(queue, s)
		}
	}
	seen := map[string]bool{}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, t := range g.transitions {
			if t[0] != cur {
				continue
			}
			reach[t[1]] = true
			if !seen[t[1]] {
				seen[t[1]] = true
				queue = append(queue, t[1])
			}
		}
	}
	out := map[string]Standing{}
	for _, s := range g.states {
		switch {
		case g.tagged[s]:
			out[s] = StandingHolds
		case !reach[s]:
			out[s] = StandingNone
		default:
			onlyFromTagged, incoming := true, 0
			for _, t := range g.transitions {
				if t[1] != s {
					continue
				}
				incoming++
				if !g.tagged[t[0]] {
					onlyFromTagged = false
				}
			}
			if !g.initial[s] && incoming > 0 && onlyFromTagged {
				out[s] = StandingCeased
			} else {
				out[s] = StandingNotRecorded
			}
		}
	}
	return out
}

// standingItem is one stored item's id and current state.
type standingItem struct{ id, status string }

// standingItems returns every existing item of typeName with its state: from
// its own table, or from the dynamic content table for a runtime-defined type.
// A missing table means no items.
func standingItems(ctx context.Context, db DB, typeName string) ([]standingItem, error) {
	table := resolveItemTable(ctx, db, typeName)
	var rows *sql.Rows
	var err error
	if table == "smeldr_dynamic_content" {
		rows, err = db.QueryContext(ctx, `SELECT id, status FROM smeldr_dynamic_content WHERE type_name = $1`, typeName)
	} else {
		rows, err = db.QueryContext(ctx, `SELECT id, status FROM `+quoteIdent(table))
	}
	if err != nil {
		if isNoSuchTable(err) {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	var out []standingItem
	for rows.Next() {
		var it standingItem
		if err := rows.Scan(&it.id, &it.status); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// MigrateStanding stores a standing for the items that existed before their
// type's flow tagged a state [StandingHolds] (D100 point 5), once per type,
// and records that it did in smeldr_standing_migrations. Idempotent: a type
// already migrated is skipped, so an item created afterwards without a row is
// correctly [StandingNone] and is never relabelled by a later boot.
//
// The classification is made from the flow graph as registered when the
// migration ran, and writes nothing it cannot be certain of: an item in a
// tagged state is [StandingHolds] (start unknown); in a state only reachable
// from tagged states, [StandingCeased] (Decision "superseded"); in a state no
// path from a tagged state reaches, [StandingNone] (certain, so it is left as
// the absence of a row); anything else is [StandingNotRecorded] (Decision
// "archived", reachable from "proposed" and from "superseded"). Call it at
// boot after the flows are registered. Like the other migrations it does
// nothing on a database it cannot probe (not SQLite). A failure for one type
// is returned and leaves that type unmarked, so the next boot retries it.
func MigrateStanding(ctx context.Context, db DB) error {
	if db == nil {
		return nil
	}
	var probe int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master`).Scan(&probe); err != nil {
		return nil
	}
	graphs, err := loadFlowGraphs(ctx, db)
	if err != nil {
		if isNoSuchTable(err) || isNoSuchColumn(err, "standing") {
			return nil
		}
		return fmt.Errorf("smeldr: MigrateStanding: read flows: %w", err)
	}
	for _, g := range graphs {
		var done int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM smeldr_standing_migrations WHERE type_name = $1`, g.typeName).Scan(&done); err != nil {
			return fmt.Errorf("smeldr: MigrateStanding: %s: read marker: %w", g.typeName, err)
		}
		if done > 0 {
			continue
		}
		classes := g.classify()
		items, err := standingItems(ctx, db, g.typeName)
		if err != nil {
			return fmt.Errorf("smeldr: MigrateStanding: %s: read items: %w", g.typeName, err)
		}
		now := time.Now().UTC()
		for _, it := range items {
			class, ok := classes[it.status]
			if !ok {
				class = StandingNotRecorded
			}
			if class == StandingNone {
				continue
			}
			if _, err := db.ExecContext(ctx,
				`INSERT INTO smeldr_standing (subject_type, subject_id, standing, updated_at) VALUES ($1, $2, $3, $4)
				 ON CONFLICT (subject_type, subject_id) DO NOTHING`,
				g.typeName, it.id, string(class), now,
			); err != nil {
				return fmt.Errorf("smeldr: MigrateStanding: %s: write %s: %w", g.typeName, it.id, err)
			}
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO smeldr_standing_migrations (type_name, applied_at) VALUES ($1, $2) ON CONFLICT (type_name) DO NOTHING`,
			g.typeName, now); err != nil {
			return fmt.Errorf("smeldr: MigrateStanding: %s: write marker: %w", g.typeName, err)
		}
	}
	return nil
}

// standingDriftDetector names [App.CheckStandingDrift]'s findings.
const standingDriftDetector = "standing-drift"

// CheckStandingDrift compares every item's stored standing with its current
// state's tag, for each type whose flow tags a state [StandingHolds]: an item
// in a tagged state must hold, and an item in an untagged state must not.
// A mismatch means a state change reached the item without going through the
// standing writer (a write that failed, or a change made behind core's back).
// It only reports: each mismatch is recorded as a [Finding] (Detector
// "standing-drift", Provenance "detected") when [App.Findings] is wired and
// counted; nothing is repaired, since a repair would be a guess. Returns how
// many items it checked and how many drifted. A database it cannot read yields
// (0, 0, nil) like the other detectors; a failing read is returned after
// reporting what was found so far.
func (a *App) CheckStandingDrift(ctx context.Context) (checked, drifted int, err error) {
	db := a.cfg.DB
	if db == nil {
		return 0, 0, nil
	}
	graphs, gerr := loadFlowGraphs(ctx, db)
	if gerr != nil {
		if isNoSuchTable(gerr) || isNoSuchColumn(gerr, "standing") {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("smeldr: CheckStandingDrift: read flows: %w", gerr)
	}
	for _, g := range graphs {
		items, ierr := standingItems(ctx, db, g.typeName)
		if ierr != nil {
			return checked, drifted, fmt.Errorf("smeldr: CheckStandingDrift: %s: read items: %w", g.typeName, ierr)
		}
		stored, serr := storedStandings(ctx, db, g.typeName)
		if serr != nil {
			return checked, drifted, fmt.Errorf("smeldr: CheckStandingDrift: %s: read standing: %w", g.typeName, serr)
		}
		for _, it := range items {
			checked++
			got := stored[it.id]
			var msg string
			switch {
			case g.tagged[it.status] && got != StandingHolds:
				msg = fmt.Sprintf("item is in %q, a state that holds, but its stored standing is %q", it.status, standingLabel(got))
			case !g.tagged[it.status] && got == StandingHolds:
				msg = fmt.Sprintf("item is in %q, a state that does not hold, but its stored standing is %q", it.status, StandingHolds)
			default:
				continue
			}
			drifted++
			if a.findingStore != nil {
				if rerr := a.findingStore.Record(ctx, Finding{
					Detector: standingDriftDetector, SubjectType: g.typeName, SubjectID: it.id,
					Provenance: "detected", Message: msg,
				}); rerr != nil {
					slog.WarnContext(ctx, "smeldr: CheckStandingDrift: Finding record failed",
						"type", g.typeName, "id", it.id, "error", rerr)
				}
			}
		}
	}
	return checked, drifted, nil
}

func standingLabel(s Standing) Standing {
	if s == "" {
		return StandingNone
	}
	return s
}

// storedStandings returns the stored standing of every item of typeName that
// has a row, keyed by item id.
func storedStandings(ctx context.Context, db DB, typeName string) (map[string]Standing, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT subject_id, standing FROM smeldr_standing WHERE subject_type = $1`, typeName)
	if err != nil {
		if isNoSuchTable(err) {
			return map[string]Standing{}, nil
		}
		return nil, err
	}
	defer rows.Close()
	out := map[string]Standing{}
	for rows.Next() {
		var id, s string
		if err := rows.Scan(&id, &s); err != nil {
			return nil, err
		}
		out[id] = Standing(s)
	}
	return out, rows.Err()
}
