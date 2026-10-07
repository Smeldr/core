// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
)

// saveHooksOption is an internal [Option]: a type's own check before a write is
// saved and its own step after. Module[T] runs them in every content write path
// (HTTP POST, PUT and PATCH, MCP create and update): the check through
// [Module.beforeSave], right after the public [BeforeCreate]/[BeforeUpdate]
// hooks. existing is nil on a create.
type saveHooksOption struct {
	before func(ctx Context, db DB, existing, item any) error
	after  func(ctx Context, db DB, rs *RelationStore, existing, item any)
}

// isOption marks saveHooksOption as a valid [Option] value.
func (saveHooksOption) isOption() {}

// runSaveAfter runs the type's post-save step, if it has one. It is fail-open:
// the write is already saved.
func (m *Module[T]) runSaveAfter(ctx Context, existing, item any) {
	if m.saveAfter == nil {
		return
	}
	m.saveAfter(ctx, m.db, m.relationStore, existing, item)
}

// amendsSaveHooks are [Amendment]'s hooks: a non-empty Amends must name one
// Decision (and is written in its canonical "D104" form), it is write-once, and
// once saved it becomes an "amends" edge from the Amendment to that Decision.
func amendsSaveHooks() Option {
	return saveHooksOption{before: amendsBeforeSave, after: amendsAfterSave}
}

// amendmentOf reads an Amendment out of a module's item or existing value.
func amendmentOf(v any) *Amendment {
	if a, ok := v.(*Amendment); ok {
		return a
	}
	return nil
}

// amendsBeforeSave refuses a write whose Amends does not name exactly one
// Decision, or that would change an Amends that is already set. An empty Amends
// is always fine. On success it rewrites Amends to the canonical "D<n>" form.
func amendsBeforeSave(ctx Context, db DB, existing, item any) error {
	a := amendmentOf(item)
	if a == nil {
		return nil
	}
	want := strings.TrimSpace(a.Amends)
	if old := amendmentOf(existing); old != nil && old.Amends != "" {
		// Write-once: the stored value stays. An empty value never clears it (a
		// full-replace PUT that does not know the field must not detach the
		// edge), and a different one is refused.
		if want != "" {
			canon, err := canonicalDecisionNumber(want)
			if err != nil {
				return err
			}
			if canon != old.Amends {
				return Err("amends", fmt.Sprintf("is write-once: this Amendment already amends %s; the link is changed through the relation once withdraw_relation exists", old.Amends))
			}
		}
		a.Amends = old.Amends
		return nil
	}
	if want == "" {
		a.Amends = ""
		return nil
	}
	canon, err := canonicalDecisionNumber(want)
	if err != nil {
		return err
	}
	if db == nil {
		return Err("amends", "cannot be checked: no database is configured")
	}
	if _, err := resolveDecisionID(ctx, db, canon); err != nil {
		return err
	}
	a.Amends = canon
	return nil
}

// amendsAfterSave asserts the edge when Amends has just become set (on create,
// or on an update from empty). Fail-open and idempotent.
func amendsAfterSave(ctx Context, db DB, rs *RelationStore, existing, item any) {
	a := amendmentOf(item)
	if a == nil || a.Amends == "" {
		return
	}
	if old := amendmentOf(existing); old != nil && old.Amends != "" {
		return
	}
	if rs == nil {
		slog.WarnContext(ctx, "smeldr: Amendment amends: no relation store is wired, the amends edge was not asserted",
			"amendment", a.ID, "amends", a.Amends)
		return
	}
	if _, err := assertAmendsEdge(ctx, db, rs, a.ID, a.Amends); err != nil {
		slog.WarnContext(ctx, "smeldr: Amendment amends: the amends edge was not asserted",
			"amendment", a.ID, "amends", a.Amends, "error", err)
	}
}

var decisionNumberRe = regexp.MustCompile(`^[Dd]?(\d+)$`)

// canonicalDecisionNumber returns the "D<n>" form of a Decision number given as
// "D104", "d104" or "104".
func canonicalDecisionNumber(s string) (string, error) {
	m := decisionNumberRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", Err("amends", fmt.Sprintf("%q is not a Decision number (use the form D104)", s))
	}
	return "D" + m[1], nil
}

// resolveDecisionID finds the one Decision whose number is canon ("D104"),
// accepting the older bare "104" spelling a Decision row can carry. No match and
// several matches are both errors naming the number.
func resolveDecisionID(ctx context.Context, db DB, canon string) (string, error) {
	bare := strings.TrimPrefix(canon, "D")
	rows, err := db.QueryContext(ctx,
		`SELECT id FROM smeldr_decisions WHERE decision_number = $1 OR decision_number = $2`, canon, bare)
	if err != nil {
		return "", fmt.Errorf("%w: resolve Decision %s: %s", ErrInternal, canon, err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", fmt.Errorf("%w: resolve Decision %s: %s", ErrInternal, canon, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("%w: resolve Decision %s: %s", ErrInternal, canon, err)
	}
	switch len(ids) {
	case 0:
		return "", Err("amends", fmt.Sprintf("no Decision %s exists", canon))
	case 1:
		return ids[0], nil
	}
	return "", Err("amends", fmt.Sprintf("%s matches %d Decisions: it must name one", canon, len(ids)))
}

// assertAmendsEdge asserts the amends edge from the Amendment amendmentID to the
// Decision numbered canon, unless a live edge for that pair already exists
// (Assert inserts a row per call). It reports whether it asserted one.
func assertAmendsEdge(ctx context.Context, db DB, rs *RelationStore, amendmentID, canon string) (bool, error) {
	decisionID, err := resolveDecisionID(ctx, db, canon)
	if err != nil {
		return false, err
	}
	live, err := rs.GetLiveBySource(ctx, "Amendment", amendmentID, "amends")
	if err != nil {
		return false, err
	}
	for _, e := range live {
		if e.TargetType == "Decision" && e.TargetID == decisionID {
			return false, nil
		}
	}
	if err := rs.Assert(ctx, RelationEdge{
		SourceType: "Amendment", SourceID: amendmentID,
		TargetType: "Decision", TargetID: decisionID,
		RelationKind: "amends", EdgeClass: "asserted",
	}); err != nil {
		return false, err
	}
	return true, nil
}

// EnsureAmendmentAmendsColumn adds [Amendment]'s amends column to
// smeldr_amendments on a database that predates it. Fresh installs have it from
// [CreateOrchestrationTables]. Idempotent, safe on every boot; call it before the
// first Amendment is read (an old table without the column makes every Amendment
// read fail). Same one-column [EnsureColumn] pattern as
// [EnsureAmendmentBodyColumn].
func EnsureAmendmentAmendsColumn(ctx context.Context, db DB) error {
	if err := EnsureColumn(ctx, db, "smeldr_amendments", "amends", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("smeldr: EnsureAmendmentAmendsColumn: %w", err)
	}
	return nil
}

// AmendsLink is one reviewed mapping of an Amendment to the Decision it amends,
// both by their numbers ("A412", "D104").
type AmendsLink struct {
	AmendmentNumber string
	DecisionNumber  string
}

// AmendsOutcome is what [App.BackfillAmendsEdges] did with one [AmendsLink]:
// "asserted", "already present", "unresolved" (a number matches nothing),
// "ambiguous" (a number matches several) or, in a dry run, "would assert".
type AmendsOutcome struct {
	AmendsLink
	Result string
	Detail string
}

// AmendsBackfillReport counts and lists what [App.BackfillAmendsEdges] did.
type AmendsBackfillReport struct {
	DryRun         bool
	Asserted       int
	AlreadyPresent int
	Unresolved     int
	Ambiguous      int
	Outcomes       []AmendsOutcome
}

// BackfillAmendsEdges gives existing Amendments their amends edge from a mapping
// the caller has reviewed. Amendments written before the amends field carry no
// structured link, and a body that mentions D104 does not mean it amends D104, so
// nothing is derived: every pair comes from links. A pair that already has a live
// edge is skipped; the rest are asserted with the caller's own actor in ctx (so
// every edge has an assert record saying who ran this) and the Amendment's amends
// field is filled in when it is empty. With dryRun nothing is written and the
// report says what would be. A link naming an Amendment or Decision that does not
// resolve to exactly one is reported, not an error. The relation store must be
// wired ([App.Relations]).
func (a *App) BackfillAmendsEdges(ctx context.Context, links []AmendsLink, dryRun bool) (*AmendsBackfillReport, error) {
	if a.cfg.DB == nil || a.relationStore == nil {
		return nil, fmt.Errorf("%w: BackfillAmendsEdges needs a database and the relation store (App.Relations)", ErrBadRequest)
	}
	db := a.cfg.DB
	rep := &AmendsBackfillReport{DryRun: dryRun}
	for _, l := range links {
		out := AmendsOutcome{AmendsLink: l}
		canon, derr := canonicalDecisionNumber(l.DecisionNumber)
		amendID, aerr := resolveAmendmentID(ctx, db, l.AmendmentNumber)
		var decisionID string
		if derr == nil {
			decisionID, derr = resolveDecisionID(ctx, db, canon)
		}
		switch {
		case aerr != nil || derr != nil:
			err := aerr
			if err == nil {
				err = derr
			}
			out.Detail = err.Error()
			if strings.Contains(out.Detail, "matches") {
				out.Result = "ambiguous"
				rep.Ambiguous++
			} else {
				out.Result = "unresolved"
				rep.Unresolved++
			}
		default:
			live, err := a.relationStore.GetLiveBySource(ctx, "Amendment", amendID, "amends")
			if err != nil {
				return nil, fmt.Errorf("%w: BackfillAmendsEdges: %s", ErrInternal, err)
			}
			present := false
			for _, e := range live {
				if e.TargetType == "Decision" && e.TargetID == decisionID {
					present = true
				}
			}
			switch {
			case present:
				out.Result = "already present"
				rep.AlreadyPresent++
			case dryRun:
				out.Result = "would assert"
				rep.Asserted++
			default:
				if _, err := assertAmendsEdge(ctx, db, a.relationStore, amendID, canon); err != nil {
					return nil, fmt.Errorf("%w: BackfillAmendsEdges: %s", ErrInternal, err)
				}
				if _, err := db.ExecContext(ctx,
					`UPDATE smeldr_amendments SET amends = $1 WHERE id = $2 AND amends = ''`, canon, amendID); err != nil {
					return nil, fmt.Errorf("%w: BackfillAmendsEdges: %s", ErrInternal, err)
				}
				out.Result = "asserted"
				rep.Asserted++
			}
		}
		rep.Outcomes = append(rep.Outcomes, out)
	}
	return rep, nil
}

// resolveAmendmentID finds the one Amendment numbered number ("A412").
func resolveAmendmentID(ctx context.Context, db DB, number string) (string, error) {
	var id string
	err := db.QueryRowContext(ctx, `SELECT id FROM smeldr_amendments WHERE amendment_number = $1`, strings.TrimSpace(number)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", Err("amendment", fmt.Sprintf("no Amendment %s exists", number))
	}
	if err != nil {
		// More than one row cannot reach here (QueryRow takes the first), so a
		// duplicate number is detected separately.
		return "", fmt.Errorf("%w: resolve Amendment %s: %s", ErrInternal, number, err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_amendments WHERE amendment_number = $1`, strings.TrimSpace(number)).Scan(&n); err == nil && n > 1 {
		return "", Err("amendment", fmt.Sprintf("%s matches %d Amendments: it must name one", number, n))
	}
	return id, nil
}

// AmendsCandidate lists, for one Amendment, the Decisions its summary and body
// mention. A mention is not a link: this exists so a person can read it and
// decide the mapping for [App.BackfillAmendsEdges].
type AmendsCandidate struct {
	AmendmentNumber string
	Mentions        []string
}

var decisionMentionRe = regexp.MustCompile(`\bD(\d+)\b`)

// AmendsBackfillCandidates is read-only. For every Amendment whose amends field is
// empty it lists the Decision numbers that its summary and body mention and that
// exist as Decisions, sorted. It never asserts or stores anything and invents no
// link: an Amendment that mentions no existing Decision is not listed.
func (a *App) AmendsBackfillCandidates(ctx context.Context) ([]AmendsCandidate, error) {
	db := a.cfg.DB
	if db == nil {
		return nil, fmt.Errorf("%w: AmendsBackfillCandidates needs a database", ErrBadRequest)
	}
	decisions := map[string]bool{}
	drows, err := db.QueryContext(ctx, `SELECT decision_number FROM smeldr_decisions`)
	if err != nil {
		return nil, fmt.Errorf("%w: AmendsBackfillCandidates: %s", ErrInternal, err)
	}
	for drows.Next() {
		var n string
		if err := drows.Scan(&n); err != nil {
			drows.Close()
			return nil, fmt.Errorf("%w: AmendsBackfillCandidates: %s", ErrInternal, err)
		}
		if c, err := canonicalDecisionNumber(n); err == nil {
			decisions[c] = true
		}
	}
	drows.Close()
	rows, err := db.QueryContext(ctx, `SELECT amendment_number, summary, body FROM smeldr_amendments WHERE amends = '' ORDER BY amendment_number`)
	if err != nil {
		return nil, fmt.Errorf("%w: AmendsBackfillCandidates: %s", ErrInternal, err)
	}
	defer rows.Close()
	var out []AmendsCandidate
	for rows.Next() {
		var number, summary, body string
		if err := rows.Scan(&number, &summary, &body); err != nil {
			return nil, fmt.Errorf("%w: AmendsBackfillCandidates: %s", ErrInternal, err)
		}
		seen := map[string]bool{}
		for _, m := range decisionMentionRe.FindAllStringSubmatch(summary+"\n"+body, -1) {
			if c := "D" + m[1]; decisions[c] {
				seen[c] = true
			}
		}
		if len(seen) == 0 {
			continue
		}
		c := AmendsCandidate{AmendmentNumber: number}
		for k := range seen {
			c.Mentions = append(c.Mentions, k)
		}
		sort.Strings(c.Mentions)
		out = append(out, c)
	}
	return out, rows.Err()
}
