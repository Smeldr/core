// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"fmt"
	"time"
)

// SignalMigrationOptions configures [MigrateSignalKinds].
type SignalMigrationOptions struct {
	// Apply commits the migration. Without it the migration is a dry run: it
	// runs in full and rolls back, so the report shows exactly what an apply
	// would do and nothing is written.
	Apply bool
}

// SignalMigrationReport is what [MigrateSignalKinds] did, or in a dry run
// what it would do.
type SignalMigrationReport struct {
	Applied bool `json:"applied"`
	// DurationMS is how long the migration's one transaction was open.
	DurationMS int64 `json:"duration_ms"`
	// Changes lists every Signal whose type or state the migration changed.
	Changes []SignalMigrationChange `json:"changes"`
	// Opened lists every condition the migration created: one
	// ratification-required for each Decision in proposed without one.
	Opened []SignalMigrationChange `json:"opened"`
	// History counts the history-type Signals left as they are, by type and
	// status.
	History map[string]map[string]int `json:"history"`
}

// SignalMigrationChange is one Signal the migration changed or created. Old
// and New are "type/status".
type SignalMigrationChange struct {
	ID      string `json:"id"`
	Address string `json:"address"`
	Type    string `json:"type"`
	Old     string `json:"old"`
	New     string `json:"new"`
	// Subject names the item the Signal is about, as "Type:ID", when it has one.
	Subject string `json:"subject,omitempty"`
	// Note says why a change was made in a way the rules mark, such as a
	// condition that had expired.
	Note string `json:"note,omitempty"`
}

// signalMigrationActor is stamped in last_actor on every row the migration
// writes.
const signalMigrationActor = "signal-kinds-migration"

// The answers the migration writes on requests closed before answers were
// recorded (D115).
const (
	signalMigratedAcknowledged = "migrated: acknowledged before answers were recorded"
	signalMigratedExpired      = "migrated: expired before answers were recorded"
)

// signalMigrationTarget returns the type and state a Signal of type t in
// status moves to, the answer to store, and false when it stays as it is
// (history, or already in a D115 state).
func signalMigrationTarget(t, status string) (newType, newStatus, answer string, move bool) {
	newType = t
	if t == "implementation-question" {
		// A request in practice (binding 1 of the approved plan).
		newType = "question"
	}
	info, known := SignalTypeOf(newType)
	if !known {
		return "", "", "", false
	}
	switch {
	case info.Kind == SignalCondition && (status == signalPending || status == "read" || status == "expired"):
		return newType, signalOpen, "", true
	case info.Kind == SignalRequest && (status == signalPending || status == "read"):
		return newType, signalOpen, "", true
	case info.Kind == SignalRequest && status == "acknowledged":
		return newType, signalAnswered, signalMigratedAcknowledged, true
	case info.Kind == SignalRequest && status == "expired":
		return newType, signalWithdrawn, signalMigratedExpired, true
	case newType != t:
		// An implementation-question already in a D115 state keeps it.
		return newType, status, "", true
	}
	return "", "", "", false
}

// MigrateSignalKinds brings existing Signals to D115, in one transaction:
//   - a condition (authorization-required, conflict-detected) in pending, read
//     or expired becomes open; an expired one is noted, since conditions were
//     never meant to expire;
//   - a request (review-requested, question) in pending or read becomes open,
//     an acknowledged one answered with "migrated: acknowledged before answers
//     were recorded", an expired one withdrawn with "migrated: expired before
//     answers were recorded";
//   - an implementation-question becomes a question, by the same rules;
//   - every other type is history and stays as it is, counted by type and
//     status;
//   - each Decision in proposed with no open ratification-required gets one.
//
// It is idempotent: a second run changes nothing. Without opts.Apply it is a
// dry run that rolls back. It needs smeldr_signals with the D115 columns
// ([EnsureOrchestrationSignalColumns]); without the table there is nothing to
// migrate.
func MigrateSignalKinds(ctx context.Context, db DB, opts SignalMigrationOptions) (*SignalMigrationReport, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: MigrateSignalKinds needs a database", ErrBadRequest)
	}
	rep := &SignalMigrationReport{Applied: opts.Apply, Changes: []SignalMigrationChange{}, Opened: []SignalMigrationChange{}, History: map[string]map[string]int{}}
	if ok, err := tableExists(ctx, db, "smeldr_signals"); err != nil {
		return nil, fmt.Errorf("%w: MigrateSignalKinds: %s", ErrInternal, err)
	} else if !ok {
		return rep, nil
	}
	hasDecisions, err := tableExists(ctx, db, "smeldr_decisions")
	if err != nil {
		return nil, fmt.Errorf("%w: MigrateSignalKinds: %s", ErrInternal, err)
	}
	start := time.Now()
	exec, commit, rollback, err := conflictTx(ctx, db)
	if err != nil {
		return nil, err
	}
	defer rollback()
	if err := migrateSignalRows(ctx, exec, rep); err != nil {
		return nil, err
	}
	if hasDecisions {
		if err := openRatificationConditions(ctx, exec, rep); err != nil {
			return nil, err
		}
	}
	if !opts.Apply {
		rollback()
	} else if err := commit(); err != nil {
		return nil, fmt.Errorf("%w: MigrateSignalKinds commit: %s", ErrInternal, err)
	}
	rep.DurationMS = time.Since(start).Milliseconds()
	return rep, nil
}

// MigrateSignalKinds runs [MigrateSignalKinds] on the App's database.
func (a *App) MigrateSignalKinds(ctx context.Context, opts SignalMigrationOptions) (*SignalMigrationReport, error) {
	return MigrateSignalKinds(ctx, a.cfg.DB, opts)
}

// migrateSignalRows moves every Signal to its D115 type and state.
func migrateSignalRows(ctx context.Context, exec DB, rep *SignalMigrationReport) error {
	type row struct{ id, address, signalType, status, subjectType, subjectID string }
	rows, err := exec.QueryContext(ctx,
		`SELECT id, COALESCE(address, ''), signal_type, status, subject_type, subject_id FROM smeldr_signals ORDER BY created_at, id`)
	if err != nil {
		return fmt.Errorf("%w: MigrateSignalKinds: %s", ErrInternal, err)
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.address, &r.signalType, &r.status, &r.subjectType, &r.subjectID); err != nil {
			rows.Close()
			return fmt.Errorf("%w: MigrateSignalKinds: %s", ErrInternal, err)
		}
		all = append(all, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return fmt.Errorf("%w: MigrateSignalKinds: %s", ErrInternal, err)
	}
	now := time.Now().UTC()
	for _, r := range all {
		newType, newStatus, answer, move := signalMigrationTarget(r.signalType, r.status)
		if !move {
			if _, known := SignalTypeOf(r.signalType); !known {
				if rep.History[r.signalType] == nil {
					rep.History[r.signalType] = map[string]int{}
				}
				rep.History[r.signalType][r.status]++
			}
			continue
		}
		if _, err := exec.ExecContext(ctx,
			`UPDATE smeldr_signals SET signal_type = $1, status = $2, answer = CASE WHEN answer = '' THEN $3 ELSE answer END,
			 updated_at = $4, last_actor = $5 WHERE id = $6`,
			newType, newStatus, answer, now, signalMigrationActor, r.id); err != nil {
			return fmt.Errorf("%w: MigrateSignalKinds: %s", ErrInternal, err)
		}
		c := SignalMigrationChange{ID: r.id, Address: r.address, Type: "Signal",
			Old: r.signalType + "/" + r.status, New: newType + "/" + newStatus}
		if r.subjectType != "" {
			c.Subject = r.subjectType + ":" + r.subjectID
		}
		if info, _ := SignalTypeOf(newType); info.Kind == SignalCondition && r.status == "expired" {
			c.Note = "a condition had expired; it is open again"
		}
		rep.Changes = append(rep.Changes, c)
	}
	return nil
}

// openRatificationConditions creates one open ratification-required for each
// Decision in proposed that has none (binding 2 of the approved plan).
func openRatificationConditions(ctx context.Context, exec DB, rep *SignalMigrationReport) error {
	rows, err := exec.QueryContext(ctx,
		`SELECT d.id FROM smeldr_decisions d WHERE d.status = $1 AND NOT EXISTS (
			SELECT 1 FROM smeldr_signals s WHERE s.status = 'open' AND s.signal_type = 'ratification-required'
			AND s.subject_type = 'Decision' AND s.subject_id = d.id) ORDER BY d.created_at, d.id`, decisionProposed)
	if err != nil {
		return fmt.Errorf("%w: MigrateSignalKinds: %s", ErrInternal, err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("%w: MigrateSignalKinds: %s", ErrInternal, err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return fmt.Errorf("%w: MigrateSignalKinds: %s", ErrInternal, err)
	}
	for _, id := range ids {
		s := ratificationSignal(id)
		// A one-off migration: its Signals and their about edges are not
		// announced (no signal.created, no relation.asserted; T875).
		if _, err := InsertSignalWithWarnings(withSignalSystem(ctx), exec, s, SignalCutovers{}); err != nil {
			return fmt.Errorf("%w: MigrateSignalKinds: %s", ErrInternal, err)
		}
		rep.Opened = append(rep.Opened, SignalMigrationChange{ID: s.ID, Address: s.Address, Type: "Signal",
			New: s.SignalType + "/" + string(s.Status), Subject: "Decision:" + id})
	}
	return nil
}
