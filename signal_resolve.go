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

// signalResolverActor names core's resolver as the actor of the state changes
// it makes: stamped in last_actor and written as the ActorID of the
// [ProvenanceRecord] (ActorKind "job").
const signalResolverActor = "signal-resolver"

// DefaultOpenSignalAge is the age [App.OpenSignalsOlderThan] uses when it is
// given none: 14 days, the age the retired expiry sweep used.
const DefaultOpenSignalAge = 14 * 24 * time.Hour

// signalSink is what a module tells the App about its items for the condition
// Signals (D115). *App implements it; a module used without an App has none.
type signalSink interface {
	SignalCutovers() SignalCutovers
	signalItemCreated(ctx context.Context, typeName, id, status string)
	signalSlugBlocked(ctx context.Context, typeName, id, slug string) bool
	signalSlugChanged(ctx context.Context, typeName, id string)
}

// setSignalSink stores the App as this module's [signalSink] (App.Content).
func (m *Module[T]) setSignalSink(s signalSink) { m.signalSink = s }

// openSignal is one open Signal row, as the resolver reads it.
type openSignal struct {
	id, slug, signalType, receiver, fromState, message string
}

// signalResolver resolves and emits condition Signals on one handle. It is
// built from an App or a [RelationStore], whichever saw the change. Every
// method fails open: an error is logged, never returned to the change that
// caused it.
type signalResolver struct {
	db          DB
	provenance  ProvenanceStore
	webhooks    *WebhookStore
	pool        *workerPool
	broadcaster *eventBroadcaster
}

// signalResolver returns the App's resolver.
func (a *App) signalResolver() signalResolver {
	return signalResolver{db: a.cfg.DB, provenance: a.provenanceStore, webhooks: a.webhookStore, pool: a.webhookPool, broadcaster: a.eventBroadcaster}
}

// selectOpen reads the open Signals matching where (an SQL condition over
// smeldr_signals, its placeholders numbered from $1).
func (r signalResolver) selectOpen(ctx context.Context, where string, args ...any) ([]openSignal, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, slug, signal_type, receiver, from_state, message FROM smeldr_signals
		 WHERE status = 'open' AND `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []openSignal
	for rows.Next() {
		var s openSignal
		if err := rows.Scan(&s.id, &s.slug, &s.signalType, &s.receiver, &s.fromState, &s.message); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// openFor reads the open Signals about one subject: one lookup on
// idx_smeldr_signals_open_subject.
func (r signalResolver) openFor(ctx context.Context, subjectType, subjectID string) ([]openSignal, error) {
	return r.selectOpen(ctx, "subject_type = $1 AND subject_id = $2", subjectType, subjectID)
}

// resolve moves each of sigs from open to resolved with resolution, records
// the change and announces it to webhooks. A row that left open in the
// meantime is left alone.
func (r signalResolver) resolve(ctx context.Context, sigs []openSignal, resolution string) {
	now := time.Now().UTC()
	for _, s := range sigs {
		res, err := r.db.ExecContext(ctx,
			`UPDATE smeldr_signals SET status = $1, resolution = $2, updated_at = $3, last_actor = $4
			 WHERE id = $5 AND status = 'open'`,
			signalResolved, resolution, now, signalResolverActor, s.id)
		if err != nil {
			slog.WarnContext(ctx, "smeldr: signal resolver: update failed", "id", s.id, "error", err)
			continue
		}
		if n, err := res.RowsAffected(); err == nil && n == 0 {
			continue
		}
		applyStateChange(ctx, r.db, r.provenance, stateChange{
			typeName: "Signal", id: s.id, from: signalOpen, to: signalResolved, reason: resolution,
			surface: surfaceTrigger, actorKind: "job", actorID: signalResolverActor,
		})
		dispatchTransitionWebhook(ctx, r.webhooks, r.pool, nil, s.receiver, eventSignalTransitioned,
			transitionWebhookData{
				Type: "signal", ID: s.id, Slug: s.slug, FromState: signalOpen, ToState: signalResolved,
				Reason: resolution, ActorID: signalResolverActor, ActorKind: "job",
			})
	}
}

// emit inserts the condition s as core's own and announces it. The caller has
// checked that no open Signal already stands for the same condition.
func (r signalResolver) emit(ctx context.Context, s *Signal) error {
	if err := InsertSignal(withSignalSystem(ctx), r.db, s); err != nil {
		return err
	}
	dispatchTransitionWebhook(ctx, r.webhooks, r.pool, r.broadcaster, s.Receiver, "signal.created", transitionWebhookData{
		Type: "signal", ID: s.ID, Slug: s.Slug, Address: s.Address, ToState: string(s.Status),
	})
	return nil
}

// contradictsMessage is the message of both conflict-detected rows of one
// contradicts edge, and the rows of an edge are found again by it: the
// emitter ([RelationStore.emitConflictDetectedSignal]) writes it, and the
// matchers ([signalResolver.resolveContradiction] and
// [signalResolver.resolveContradictionsOf]) build their lookups from it. Rows
// written before D115 carry the same text, so the wording must not change:
// TestContradictsMessage_Wording pins it, and TestResolve_ConflictDetected
// proves the emitter and the matchers agree.
func contradictsMessage(sourceID, targetID string) string {
	return fmt.Sprintf("Decision %s contradicts Decision %s", sourceID, targetID)
}

// resolveContradiction resolves the open conflict-detected rows of the
// contradicts edge from sourceID to targetID.
func (r signalResolver) resolveContradiction(ctx context.Context, sourceID, targetID, resolution string) {
	sigs, err := r.selectOpen(ctx, "signal_type = 'conflict-detected' AND subject_type = 'Decision' AND message = $1",
		contradictsMessage(sourceID, targetID))
	if err != nil {
		if !isNoSuchTable(err) {
			slog.WarnContext(ctx, "smeldr: signal resolver: contradiction lookup failed", "source", sourceID, "target", targetID, "error", err)
		}
		return
	}
	r.resolve(ctx, sigs, resolution)
}

// resolveContradictionsOf resolves every open contradiction a Decision is a
// side of: both rows of each of its contradicts edges.
func (r signalResolver) resolveContradictionsOf(ctx context.Context, decisionID, resolution string) {
	mine, err := r.selectOpen(ctx, "signal_type = 'conflict-detected' AND subject_type = 'Decision' AND (subject_id = $1 OR message LIKE $2 OR message LIKE $3)",
		decisionID, contradictsMessage(decisionID, "%"), contradictsMessage("%", decisionID))
	if err != nil {
		slog.WarnContext(ctx, "smeldr: signal resolver: contradiction lookup failed", "decision", decisionID, "error", err)
		return
	}
	r.resolve(ctx, mine, resolution)
}

// resolveSignalsFor is the resolver's part of a committed status change
// ([App.afterStatusChange]): it resolves the conditions the change ends and
// opens the ones it starts. One indexed lookup per change.
func (a *App) resolveSignalsFor(ctx context.Context, t statusTransition) {
	if !a.signalConditions || a.cfg.DB == nil {
		return
	}
	r := a.signalResolver()
	if t.typeName == "Signal" {
		if t.to == signalAnswered || t.to == signalWithdrawn {
			// transition_item carries the answer as its reason: keep it on the
			// row as well, unless [App.AnswerSignal] writes it itself.
			if _, err := r.db.ExecContext(ctx,
				`UPDATE smeldr_signals SET answer = $1 WHERE id = $2 AND answer = ''`, t.reason, t.id); err != nil {
				slog.WarnContext(ctx, "smeldr: signal resolver: answer not stored", "id", t.id, "error", err)
			}
		}
		return
	}
	open, err := r.openFor(ctx, t.typeName, t.id)
	if err != nil {
		slog.WarnContext(ctx, "smeldr: signal resolver: lookup failed", "type", t.typeName, "id", t.id, "error", err)
		return
	}
	resolution := fmt.Sprintf("%s %s moved %s to %s", t.typeName, t.id, t.from, t.to)
	var done []openSignal
	ratificationOpen := false
	for _, s := range open {
		switch s.signalType {
		case "authorization-required":
			if s.fromState == t.from {
				done = append(done, s)
			}
		case "ratification-required":
			if t.typeName == "Decision" && t.from == decisionProposed {
				done = append(done, s)
			} else {
				ratificationOpen = true
			}
		case "conflict-detected":
			if s.fromState == string(Scheduled) && t.to == string(Published) {
				done = append(done, s)
			}
		}
	}
	r.resolve(ctx, done, resolution)
	if t.typeName != "Decision" {
		return
	}
	if t.from == decisionRatified {
		r.resolveContradictionsOf(ctx, t.id, resolution)
	}
	if t.to == decisionProposed && !ratificationOpen {
		a.emitRatificationRequired(ctx, t.id)
	}
}

// The Decision states the resolver acts on.
const (
	decisionProposed = "proposed"
	decisionRatified = "ratified"
)

// ratificationSignal is the ratification-required condition of a Decision.
func ratificationSignal(decisionID string) *Signal {
	return &Signal{
		Sender:            "system",
		Receiver:          decisionRatifyOperation,
		SignalType:        "ratification-required",
		Message:           fmt.Sprintf("Decision %s is proposed and waits for ratification", decisionID),
		SubjectType:       "Decision",
		SubjectID:         decisionID,
		FromState:         decisionProposed,
		ToState:           decisionRatified,
		RequiredOperation: decisionRatifyOperation,
	}
}

// emitRatificationRequired opens the ratification-required condition of a
// Decision now in proposed. The caller has checked none is open.
func (a *App) emitRatificationRequired(ctx context.Context, decisionID string) {
	if err := a.signalResolver().emit(ctx, ratificationSignal(decisionID)); err != nil {
		slog.WarnContext(ctx, "smeldr: ratification-required not emitted", "decision", decisionID, "error", err)
	}
}

// signalItemCreated opens the conditions a new item starts in: a Decision
// created in proposed waits for ratification.
func (a *App) signalItemCreated(ctx context.Context, typeName, id, status string) {
	if !a.signalConditions || a.cfg.DB == nil || typeName != "Decision" || status != decisionProposed {
		return
	}
	open, err := a.signalResolver().openFor(ctx, typeName, id)
	if err != nil {
		slog.WarnContext(ctx, "smeldr: ratification-required lookup failed", "decision", id, "error", err)
		return
	}
	for _, s := range open {
		if s.signalType == "ratification-required" {
			return
		}
	}
	a.emitRatificationRequired(ctx, id)
}

// slugConditionOpen reports whether sigs hold an open taken-URL condition.
func slugConditionOpen(sigs []openSignal) bool {
	for _, s := range sigs {
		if s.signalType == "conflict-detected" && s.fromState == string(Scheduled) {
			return true
		}
	}
	return false
}

// signalSlugBlocked opens the taken-URL condition of a scheduled item the
// scheduler cannot publish, once: an open one is left as it is. It reports
// whether the condition is recorded; false (no Signals here, or a failure)
// leaves the caller to log the block itself.
func (a *App) signalSlugBlocked(ctx context.Context, typeName, id, slug string) bool {
	if !a.signalConditions || a.cfg.DB == nil {
		return false
	}
	r := a.signalResolver()
	open, err := r.openFor(ctx, typeName, id)
	if err != nil {
		slog.WarnContext(ctx, "smeldr: taken-URL condition lookup failed", "type", typeName, "id", id, "error", err)
		return false
	}
	if slugConditionOpen(open) {
		return true
	}
	err = r.emit(ctx, &Signal{
		Sender:            "system",
		Receiver:          "manage",
		SignalType:        "conflict-detected",
		Message:           fmt.Sprintf("%s %s is scheduled, but its URL %q is taken: it stays scheduled until its slug changes or the URL is free", typeName, id, slug),
		SubjectType:       typeName,
		SubjectID:         id,
		FromState:         string(Scheduled),
		ToState:           string(Published),
		RequiredOperation: "manage",
	})
	if err != nil {
		slog.WarnContext(ctx, "smeldr: taken-URL condition not emitted", "type", typeName, "id", id, "error", err)
		return false
	}
	return true
}

// signalSlugChanged resolves the taken-URL condition of an item whose slug
// changed.
func (a *App) signalSlugChanged(ctx context.Context, typeName, id string) {
	if !a.signalConditions || a.cfg.DB == nil {
		return
	}
	r := a.signalResolver()
	open, err := r.openFor(ctx, typeName, id)
	if err != nil {
		slog.WarnContext(ctx, "smeldr: taken-URL condition lookup failed", "type", typeName, "id", id, "error", err)
		return
	}
	var done []openSignal
	for _, s := range open {
		if s.signalType == "conflict-detected" && s.fromState == string(Scheduled) {
			done = append(done, s)
		}
	}
	r.resolve(ctx, done, fmt.Sprintf("%s %s changed its slug", typeName, id))
}

// validateSignalTransition holds the kind rules of the Signal flow (D115), on
// every transition path ([validateTransition]):
//   - only a condition is resolved, and only by core (the system marker);
//     anyone else is [ErrForbidden];
//   - only a request is answered or withdrawn; a condition is [ErrConflict].
//
// Moves out of the history states are left to the flow's own edges.
func validateSignalTransition(ctx context.Context, db DB, itemID, from, to string) error {
	if from != signalOpen {
		return nil
	}
	var signalType string
	err := db.QueryRowContext(ctx, `SELECT signal_type FROM smeldr_signals WHERE id = $1`, itemID).Scan(&signalType)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("%w: signal transition: %s", ErrInternal, err)
	}
	info, known := SignalTypeOf(signalType)
	switch to {
	case signalResolved:
		if !known || info.Kind != SignalCondition {
			return fmt.Errorf("%w: a %q Signal is a request: it is answered or withdrawn, never resolved", ErrConflict, signalType)
		}
		if !isSignalSystem(ctx) {
			return fmt.Errorf("%w: a condition is resolved by the system when it ends", ErrForbidden)
		}
	case signalAnswered, signalWithdrawn:
		if !known || info.Kind != SignalRequest {
			return fmt.Errorf("%w: a %q Signal is a condition: it is resolved by the system when it ends, never answered or withdrawn", ErrConflict, signalType)
		}
	}
	return nil
}

// AnswerSignal answers the open request ident names (ID, slug or address):
// it moves open to answered with answer as the transition's reason, and
// stores answer and ref (the change the answer names, "Type:ID", optional) on
// the Signal (D115).
//
// The Signal is looked up under the caller's read scope: a missing or hidden
// one is [ErrNotFound]. An empty answer, or a ref not shaped "Type:ID", is a
// [ValidationError]; a Signal that is not an open request is [ErrConflict]; a
// caller without the signal answer capability (operation update) is
// [ErrForbidden]. The sender and receiver of a Signal are routing names, not
// identities: any holder of the capability who can read the request may
// answer it.
func (a *App) AnswerSignal(ctx context.Context, ident, answer, ref string) error {
	return a.closeSignalRequest(ctx, ident, signalAnswered, "answer", "answer", answer, ref)
}

// WithdrawSignal withdraws the open request ident names, with reason: the
// same rules as [App.AnswerSignal], under the signal withdraw capability
// (operation archive), with reason in place of the answer.
func (a *App) WithdrawSignal(ctx context.Context, ident, reason string) error {
	return a.closeSignalRequest(ctx, ident, signalWithdrawn, "withdraw", "reason", reason, "")
}

// validAnswerRef reports whether ref is shaped "Type:ID".
func validAnswerRef(ref string) bool {
	i := strings.Index(ref, ":")
	return i > 0 && i < len(ref)-1 && !strings.ContainsAny(ref, " \t\r\n")
}

// closeSignalRequest is [App.AnswerSignal] and [App.WithdrawSignal].
func (a *App) closeSignalRequest(ctx context.Context, ident, to, verb, field, text, ref string) error {
	if strings.TrimSpace(text) == "" {
		return Err(field, field+" is required")
	}
	if ref != "" && !validAnswerRef(ref) {
		return Err("answer_ref", `answer_ref names the change as "Type:ID"`)
	}
	if a.cfg.DB == nil {
		return fmt.Errorf("%w: %s signal requires Config.DB", ErrInternal, verb)
	}
	scope, err := scopeFor(ctx, a.governance)
	if err != nil {
		return err
	}
	_, id, _, status, err := a.lookupCompiledRecord(ctx, "Signal", ident, scope)
	if err != nil {
		return err
	}
	if !scope.Readable("Signal", id) {
		return ErrNotFound
	}
	if sc, ok := ctx.(Context); ok {
		if err := a.AuthorizeCapability(sc, ResourceSignal, verb, AuthTarget{TypeName: "Signal", ID: id}); err != nil {
			return err
		}
	} else if a.governance != nil {
		return ErrForbidden
	}
	if status != signalOpen {
		return fmt.Errorf("%w: the Signal is %s, not open", ErrConflict, status)
	}
	if _, err := a.TransitionItemVia(ctx, "", "Signal", id, to, text); err != nil {
		return err
	}
	if _, err := a.cfg.DB.ExecContext(ctx,
		`UPDATE smeldr_signals SET answer = $1, answer_ref = $2 WHERE id = $3`, text, ref, id); err != nil {
		// The transition has committed, with the text as its reason.
		slog.WarnContext(ctx, "smeldr: signal answer not stored", "id", id, "error", err)
	}
	return nil
}

// OpenSignalsOlderThan returns the open Signals created at least age ago,
// oldest first, at most limit of them, under the caller's read scope: a
// Signal the caller may not read, or whose subject it may not read, is left
// out. age 0 or less means [DefaultOpenSignalAge]; limit 0 or less means 50,
// and more than 500 is 500. Nothing expires (D115): this is how an old open
// Signal is surfaced instead.
func (a *App) OpenSignalsOlderThan(ctx context.Context, age time.Duration, limit int) ([]Signal, error) {
	if a.cfg.DB == nil {
		return nil, nil
	}
	if age <= 0 {
		age = DefaultOpenSignalAge
	}
	switch {
	case limit <= 0:
		limit = 50
	case limit > 500:
		limit = 500
	}
	scope, err := scopeFor(ctx, a.governance)
	if err != nil {
		return nil, err
	}
	rows, err := a.cfg.DB.QueryContext(ctx,
		`SELECT id, subject_type, subject_id FROM smeldr_signals
		 WHERE status = 'open' AND created_at <= $1 ORDER BY created_at ASC, id ASC`,
		time.Now().UTC().Add(-age))
	if isNoSuchTable(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: OpenSignalsOlderThan: %s", ErrInternal, err)
	}
	var ids []string
	for rows.Next() && len(ids) < limit {
		var id, subjectType, subjectID string
		if err := rows.Scan(&id, &subjectType, &subjectID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("%w: OpenSignalsOlderThan: %s", ErrInternal, err)
		}
		if scope.Readable("Signal", id) && signalSubjectVisible(scope, subjectType, subjectID) {
			ids = append(ids, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, fmt.Errorf("%w: OpenSignalsOlderThan: %s", ErrInternal, err)
	}
	repo := NewSQLRepo[*Signal](a.cfg.DB, Table("smeldr_signals"))
	out := make([]Signal, 0, len(ids))
	for _, id := range ids {
		s, err := repo.FindByID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("%w: OpenSignalsOlderThan: %s", ErrInternal, err)
		}
		out = append(out, *s)
	}
	return out, nil
}
