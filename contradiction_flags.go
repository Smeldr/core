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

// contradictionFlagTableStmts create the table of the contradiction pairs
// (T928): for each contradicts edge, stored canonically as (source, target),
// one row per side with the conflict-detected Signal about that side. The
// resolver finds the Signals of a pair here, never by their message.
var contradictionFlagTableStmts = []string{
	`CREATE TABLE IF NOT EXISTS smeldr_contradiction_flags (
		source_id  TEXT NOT NULL,
		target_id  TEXT NOT NULL,
		subject_id TEXT NOT NULL,
		signal_id  TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (source_id, target_id, subject_id)
	)`,
}

// EnsureContradictionFlagTable creates the table of the contradiction pairs
// (T928) on a database made before it. Fresh installs get it from
// [CreateGovernanceTables]. Idempotent, safe to call on every boot.
func EnsureContradictionFlagTable(ctx context.Context, db DB) error {
	for _, stmt := range contradictionFlagTableStmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("smeldr: EnsureContradictionFlagTable: %w", err)
		}
	}
	return nil
}

// contradictsMessage is the message of the conflict-detected condition about
// one side of a contradiction. It names that side only (label is its
// address), never the other: a reader of this side may not read the other.
func contradictsMessage(label string) string {
	return label + " contradicts a Decision"
}

// decisionLabel is how a condition names a Decision: its address, or its ID
// when it has none or cannot be read.
func decisionLabel(ctx context.Context, db DB, id string) string {
	d, err := readPremiseDecision(ctx, db, id)
	if err != nil {
		return "Decision " + id
	}
	return d.label(id)
}

// contradictionEndedText resolves both conditions of a pair whose contradicts
// edge ended. It names neither Decision nor the relation.
func contradictionEndedText(cause string) string {
	return "the contradicts relation ended (" + cause + ")"
}

// contradictionLeftText resolves one condition of a pair whose Decision
// moved out of ratified: the row about the moved Decision names it, the
// other row names nothing.
func contradictionLeftText(label string, own bool, to string) string {
	if own {
		return label + " is no longer ratified (" + to + ")"
	}
	return "the contradicting Decision is no longer ratified"
}

// pairSignals returns the open conflict-detected Signals of the pair rows
// matching where (over smeldr_contradiction_flags, placeholders from $1), each
// with the row's subject.
func (r signalResolver) pairSignals(ctx context.Context, where string, args ...any) (map[string]string, []openSignal, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT subject_id, signal_id FROM smeldr_contradiction_flags WHERE `+where, args...)
	if err != nil {
		return nil, nil, err
	}
	subjects := map[string]string{}
	var ids []string
	for rows.Next() {
		var subject, id string
		if err := rows.Scan(&subject, &id); err != nil {
			rows.Close()
			return nil, nil, err
		}
		if id != "" {
			subjects[id] = subject
			ids = append(ids, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(ids) == 0 {
		return subjects, nil, err
	}
	ph := make([]string, len(ids))
	sigArgs := make([]any, len(ids))
	for i, id := range ids {
		ph[i] = fmt.Sprintf("$%d", i+1)
		sigArgs[i] = id
	}
	open, err := r.selectOpen(ctx, "id IN ("+strings.Join(ph, ", ")+")", sigArgs...)
	return subjects, open, err
}

// emitContradiction opens the two conflict-detected conditions of a
// contradicts edge (D85), one about each side, each placed where its side is
// placed (T928). One open condition per contradiction (D115): a pair with an
// open condition opens nothing. Fail-open.
func (r signalResolver) emitContradiction(ctx context.Context, edge RelationEdge) {
	_, open, err := r.pairSignals(ctx, "source_id = $1 AND target_id = $2", edge.SourceID, edge.TargetID)
	if err != nil {
		slog.WarnContext(ctx, "smeldr: conflict-detected: lookup failed", "edge_id", edge.ID, "error", err)
		return
	}
	if len(open) > 0 {
		return
	}
	for _, subject := range []string{edge.TargetID, edge.SourceID} {
		if _, err := r.openContradiction(ctx, edge.SourceID, edge.TargetID, subject); err != nil {
			slog.WarnContext(ctx, "smeldr: conflict-detected: not opened", "edge_id", edge.ID, "subject_id", subject, "error", err)
		}
	}
}

// openContradiction opens the condition about subject, one side of the pair
// (source, target): the Signal, its pair row and its place edge in one
// transaction, announced after the commit.
func (r signalResolver) openContradiction(ctx context.Context, sourceID, targetID, subject string) (string, error) {
	var stale string
	err := r.db.QueryRowContext(ctx, `SELECT signal_id FROM smeldr_contradiction_flags WHERE source_id = $1 AND target_id = $2 AND subject_id = $3`,
		sourceID, targetID, subject).Scan(&stale)
	found := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	s := &Signal{
		Sender: "system", Receiver: decisionRatifyOperation, SignalType: "conflict-detected",
		Message:     contradictsMessage(decisionLabel(ctx, r.db, subject)),
		SubjectType: "Decision", SubjectID: subject,
	}
	return r.openDecisionCondition(ctx, s, subject, "conflict-detected", func(exec DB, signalID string) (sql.Result, error) {
		if found {
			return exec.ExecContext(ctx, `UPDATE smeldr_contradiction_flags SET signal_id = $1 WHERE source_id = $2 AND target_id = $3 AND subject_id = $4 AND signal_id = $5`,
				signalID, sourceID, targetID, subject, stale)
		}
		return exec.ExecContext(ctx, `INSERT INTO smeldr_contradiction_flags (source_id, target_id, subject_id, signal_id) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`,
			sourceID, targetID, subject, signalID)
	})
}

// openDecisionCondition opens a condition about a Decision: inserts s,
// claims its row with claim (a claim that changes no row means another
// evaluation opened it first, and everything rolls back), and places s where
// the Decision is placed, in one transaction; announced after the commit.
// It reports "opened", or "" when the row was taken.
func (r signalResolver) openDecisionCondition(ctx context.Context, s *Signal, decisionID, job string, claim func(exec DB, signalID string) (sql.Result, error)) (string, error) {
	place, placed, err := decisionPlace(ctx, r.db, decisionID)
	if err != nil {
		if !isNoSuchTable(err) {
			return "", err
		}
		placed = false // no relations on this database: nothing to place in
	}
	var edge RelationEdge
	err = runInTx(ctx, r.db, func(exec DB) error {
		if err := InsertSignal(withSignalSystem(ctx), exec, s); err != nil {
			return err
		}
		res, err := claim(exec, s.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errRequestTaken
		}
		if !placed {
			return nil
		}
		edge, _, err = writeEdgeRow(ctx, exec, RelationEdge{SourceType: "Signal", SourceID: s.ID, TargetType: place.targetType, TargetID: place.targetID,
			RelationKind: place.kind, EdgeClass: "asserted", CreatedByJob: &job, Attributes: []byte("{}")})
		return err
	})
	if errors.Is(err, errRequestTaken) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if placed && r.relations != nil {
		r.relations.announceEdge(ctx, edge, true)
	}
	r.announceCreated(ctx, s)
	return "opened", nil
}

// resolveContradiction resolves the open conditions of the pair (source,
// target) whose contradicts edge ended.
func (r signalResolver) resolveContradiction(ctx context.Context, sourceID, targetID, cause string) {
	_, open, err := r.pairSignals(ctx, "source_id = $1 AND target_id = $2", sourceID, targetID)
	if err != nil {
		if !isNoSuchTable(err) {
			slog.WarnContext(ctx, "smeldr: signal resolver: contradiction lookup failed", "source", sourceID, "target", targetID, "error", err)
		}
		return
	}
	r.resolve(ctx, open, contradictionEndedText(cause))
}

// resolveContradictionsOf resolves every open contradiction a Decision is a
// side of, after it left ratified for to: each row's text names only its
// own Decision.
func (r signalResolver) resolveContradictionsOf(ctx context.Context, decisionID, to string) {
	subjects, open, err := r.pairSignals(ctx, "source_id = $1 OR target_id = $1", decisionID)
	if err != nil {
		if !isNoSuchTable(err) {
			slog.WarnContext(ctx, "smeldr: signal resolver: contradiction lookup failed", "decision", decisionID, "error", err)
		}
		return
	}
	label := decisionLabel(ctx, r.db, decisionID)
	for _, s := range open {
		own := subjects[s.id] == decisionID
		r.resolve(ctx, []openSignal{s}, contradictionLeftText(label, own, to))
	}
}

// ContradictionSweep is what one [App.SweepContradictionConditions] run did.
type ContradictionSweep struct {
	// Reworded is how many old rows got the new message (and resolution).
	Reworded int `json:"reworded"`
	// Resolved is how many old open rows were resolved because their
	// contradiction no longer holds.
	Resolved int `json:"resolved"`
	// Unparsed is how many old rows could not be read and were left alone.
	Unparsed int `json:"unparsed"`
	// Decisions is how many Decisions with open conditions had their
	// conditions' places synced.
	Decisions int `json:"decisions"`
}

// oldContradictsPrefix and oldContradictsMiddle make up the message of a
// conflict-detected row written before T928: "Decision <id> contradicts
// Decision <id>".
const (
	oldContradictsPrefix = "Decision "
	oldContradictsMiddle = " contradicts Decision "
)

// SweepContradictionConditions brings the contradiction conditions in line
// with T928. Every conflict-detected row about a Decision written before it
// (message "Decision <id> contradicts Decision <id>", in any state) gets the
// new message naming only its own Decision, a resolution that names no other
// Decision, and its pair row; an open one whose contradiction no longer holds
// (the edge ended, or a side left ratified) is resolved. Then every open
// contradiction or premise condition is placed where its Decision is placed.
// It is idempotent; a row it cannot read is counted and left as it is. Run it
// at boot and on the structural sweep's schedule. Reading the rows fails as
// [ErrInternal]; a row that fails is logged and skipped.
func (a *App) SweepContradictionConditions(ctx context.Context) (ContradictionSweep, error) {
	if a.cfg.DB == nil {
		return ContradictionSweep{}, fmt.Errorf("%w: SweepContradictionConditions needs a database", ErrInternal)
	}
	db := a.cfg.DB
	r := a.signalResolver()
	type oldRow struct{ id, subject, status, message, resolution string }
	rows, err := db.QueryContext(ctx,
		`SELECT id, subject_id, status, message, COALESCE(resolution, '') FROM smeldr_signals
		  WHERE signal_type = 'conflict-detected' AND subject_type = 'Decision' AND message LIKE 'Decision % contradicts Decision %'`)
	if err != nil {
		return ContradictionSweep{}, fmt.Errorf("%w: SweepContradictionConditions: %v", ErrInternal, err)
	}
	var old []oldRow
	for rows.Next() {
		var o oldRow
		if err := rows.Scan(&o.id, &o.subject, &o.status, &o.message, &o.resolution); err != nil {
			rows.Close()
			return ContradictionSweep{}, fmt.Errorf("%w: SweepContradictionConditions: %v", ErrInternal, err)
		}
		old = append(old, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return ContradictionSweep{}, fmt.Errorf("%w: SweepContradictionConditions: %v", ErrInternal, err)
	}
	var out ContradictionSweep
	for _, o := range old {
		pair, ok := strings.CutPrefix(o.message, oldContradictsPrefix)
		source, target, ok2 := strings.Cut(pair, oldContradictsMiddle)
		if !ok || !ok2 || source == "" || target == "" || (o.subject != source && o.subject != target) {
			out.Unparsed++
			continue
		}
		if resolved, err := a.rewordContradiction(ctx, o.id, o.subject, o.status, o.resolution, source, target); err != nil {
			slog.WarnContext(ctx, "smeldr: SweepContradictionConditions: row not reworded", "signal", o.id, "error", err)
		} else {
			out.Reworded++
			if resolved {
				out.Resolved++
			}
		}
	}
	subjects, err := premisePairs(ctx, db,
		`SELECT DISTINCT subject_id, '' FROM smeldr_signals
		  WHERE signal_type IN ('conflict-detected', 'premise-flagged') AND subject_type = 'Decision' AND status = 'open'`)
	if err != nil {
		slog.WarnContext(ctx, "smeldr: SweepContradictionConditions: open conditions not read", "error", err)
		return out, nil
	}
	for _, s := range subjects {
		r.syncDecisionConditionPlaces(ctx, s[0])
	}
	out.Decisions = len(subjects)
	return out, nil
}

// rewordContradiction rewrites one old row: the message names only its own
// Decision, the resolution names no other Decision or relation, the pair row
// is filled in, and an open row whose contradiction no longer holds is
// resolved. It reports whether it resolved the row.
func (a *App) rewordContradiction(ctx context.Context, id, subject, status, resolution, source, target string) (bool, error) {
	db := a.cfg.DB
	label := decisionLabel(ctx, db, subject)
	// Every old resolution is rewritten: they name Decisions and relations by
	// internal ID.
	newResolution := resolution
	if resolution != "" {
		newResolution = rewordContradictionResolution(resolution, subject, label)
	}
	if _, err := db.ExecContext(ctx, `UPDATE smeldr_signals SET message = $1, resolution = $2 WHERE id = $3`,
		contradictsMessage(label), newResolution, id); err != nil {
		return false, err
	}
	recordProvenance(ctx, a.provenanceStore, ProvenanceRecord{
		SubjectType: "Signal", SubjectID: id, Verb: "update", FromState: status, ToState: status,
		ActorKind: "job", ActorID: "contradictions", Reason: "reworded: names only its own Decision (T928)",
	})
	if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_contradiction_flags (source_id, target_id, subject_id, signal_id) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`,
		source, target, subject, id); err != nil {
		return false, err
	}
	if status != signalOpen {
		return false, nil
	}
	ended, err := a.contradictionEnded(ctx, source, target, subject, label)
	if err != nil || ended == "" {
		return false, err
	}
	open, err := a.signalResolver().selectOpen(ctx, "id = $1", id)
	if err != nil {
		return false, err
	}
	a.signalResolver().resolve(ctx, open, ended)
	return len(open) > 0, nil
}

// rewordContradictionResolution rewrites an old resolution text so it names
// no Decision but the row's own and no relation.
func rewordContradictionResolution(resolution, subject, label string) string {
	if rest, ok := strings.CutPrefix(resolution, "the contradicts relation "); ok {
		if _, cause, ok := strings.Cut(rest, " ended: "); ok {
			return contradictionEndedText(cause)
		}
		return "the contradicts relation ended"
	}
	// "Decision <id> moved <from> to <to>", from resolveSignalsFor.
	if rest, ok := strings.CutPrefix(resolution, "Decision "); ok {
		if moved, change, ok := strings.Cut(rest, " moved "); ok {
			if _, to, ok := strings.Cut(change, " to "); ok {
				return contradictionLeftText(label, moved == subject, to)
			}
		}
	}
	return "resolved"
}

// contradictionEnded says why an old open contradiction no longer holds (the
// edge ended, or a side is no longer ratified), or "" when it still holds.
func (a *App) contradictionEnded(ctx context.Context, source, target, subject, label string) (string, error) {
	db := a.cfg.DB
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_relations WHERE source_id = $1 AND target_id = $2 AND relation_kind = 'contradicts'
		    AND (invalid_at IS NULL OR invalid_at > $3)`, source, target, time.Now().UTC()).Scan(&n); err != nil {
		return "", err
	}
	if n == 0 {
		return "the contradicts relation ended", nil
	}
	for _, side := range []string{subject, source, target} {
		d, err := readPremiseDecision(ctx, db, side)
		if err != nil {
			return "", err
		}
		if d.found && d.state != decisionRatified {
			return contradictionLeftText(label, side == subject, d.state), nil
		}
	}
	return "", nil
}
