// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// CheckRecord is one immutable record of an Authority-graph Check run against
// a ratification precondition (decision-governance-model.md §4) — persisted
// so "checked, found nothing" is distinguishable from "never checked," the
// same discipline [SweepRunRecord] already applies to detector runs.
type CheckRecord struct {
	ID          string    `json:"id"`           // UUID v7, see [NewID]
	SubjectType string    `json:"subject_type"` // e.g. "Decision"
	SubjectID   string    `json:"subject_id"`
	RuleType    string    `json:"rule_type"` // the subject's own RuleType at check time
	RanAt       time.Time `json:"ran_at"`
	Found       bool      `json:"found"`
	MatchType   string    `json:"match_type"` // "Rule" | "AuthorityStub" | "" when !Found
	MatchID     string    `json:"match_id"`
	MatchName   string    `json:"match_name"` // a human-facing label — Rule.Slug or AuthorityStub.SourceRef
	// Sentence is the pre-computed, human-facing comparison sentence
	// decision-governance-model.md §4 requires: never a bare rule-type label
	// for the reader to place in a mental ordering themselves.
	Sentence string `json:"sentence"`
}

// CheckStore is the persistence interface for Check run records. Implement
// it to use a custom storage backend; use [NewCheckStore] for the default
// SQLite/Postgres-compatible implementation.
type CheckStore interface {
	// Append persists r.
	Append(ctx context.Context, r CheckRecord) error
	// Last returns the most recent record for (subjectType, subjectID).
	// found is false and err is nil when no record exists yet.
	Last(ctx context.Context, subjectType, subjectID string) (r CheckRecord, found bool, err error)
	// List returns up to limit records for (subjectType, subjectID), newest
	// first. limit <= 0 means no limit.
	List(ctx context.Context, subjectType, subjectID string, limit int) ([]CheckRecord, error)
}

// sqlCheckStore is the default SQL-backed [CheckStore].
type sqlCheckStore struct {
	db DB
}

// NewCheckStore returns a [CheckStore] backed by db.
//
// The smeldr_check_records table must exist before use. Create it with
// [CreateCheckTable].
func NewCheckStore(db DB) CheckStore {
	return &sqlCheckStore{db: db}
}

// CreateCheckTable creates the smeldr_check_records table if it does not
// exist. Call once at application startup before [NewCheckStore].
func CreateCheckTable(db DB) error {
	_, err := db.ExecContext(context.Background(), `
		CREATE TABLE IF NOT EXISTS smeldr_check_records (
			id           TEXT PRIMARY KEY,
			subject_type TEXT NOT NULL,
			subject_id   TEXT NOT NULL,
			rule_type    TEXT NOT NULL,
			ran_at       TIMESTAMPTZ NOT NULL,
			found        BOOLEAN NOT NULL,
			match_type   TEXT NOT NULL DEFAULT '',
			match_id     TEXT NOT NULL DEFAULT '',
			match_name   TEXT NOT NULL DEFAULT '',
			sentence     TEXT NOT NULL DEFAULT ''
		)`)
	return err
}

// Check wires store into a, enabling the Check precondition on Decision
// ratification (decision-governance-model.md §4) at both call sites that
// transition a Decision — [Module.updateHandler] (module.go) and
// [App.TransitionItem] (state.go). Pass nil to disable (the default state).
//
// The smeldr_check_records table must exist before Check is called. See
// [CreateCheckTable] for the required DDL.
//
// Errors from [RunAuthorityCheck] are logged at Warn level and never block
// ratification — Check is advisory and recording, not an authorization gate
// (decision-governance-model.md §4's own worked example has the ratifier
// proceed past a found conflict; the declared-tension mechanism, a separate,
// not-yet-built task, is what makes "proceeded anyway" itself a recorded
// act). A CheckStore outage must never become a ratification outage.
func (a *App) Check(store CheckStore) *App {
	a.checkStore = store
	return a
}

// Append persists r to the smeldr_check_records table. RanAt is stored as an
// RFC3339 string for SQLite compatibility.
func (s *sqlCheckStore) Append(ctx context.Context, r CheckRecord) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO smeldr_check_records
		 (id, subject_type, subject_id, rule_type, ran_at, found, match_type, match_id, match_name, sentence)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		r.ID, r.SubjectType, r.SubjectID, r.RuleType, r.RanAt.UTC().Format(time.RFC3339),
		r.Found, r.MatchType, r.MatchID, r.MatchName, r.Sentence,
	)
	if err != nil {
		return fmt.Errorf("smeldr: CheckStore.Append: %w", err)
	}
	return nil
}

// Last returns the most recent record for (subjectType, subjectID), ordered
// by RanAt descending. found is false and err is nil when no record exists
// yet.
func (s *sqlCheckStore) Last(ctx context.Context, subjectType, subjectID string) (CheckRecord, bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, subject_type, subject_id, rule_type, ran_at, found, match_type, match_id, match_name, sentence
		 FROM smeldr_check_records WHERE subject_type = $1 AND subject_id = $2
		 ORDER BY ran_at DESC LIMIT 1`,
		subjectType, subjectID,
	)
	if err != nil {
		return CheckRecord{}, false, fmt.Errorf("smeldr: CheckStore.Last: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return CheckRecord{}, false, rows.Err()
	}
	r, err := scanCheckRecord(rows)
	if err != nil {
		return CheckRecord{}, false, fmt.Errorf("smeldr: CheckStore.Last: %w", err)
	}
	return r, true, nil
}

// List returns up to limit records for (subjectType, subjectID), newest
// first. limit <= 0 means no limit.
func (s *sqlCheckStore) List(ctx context.Context, subjectType, subjectID string, limit int) ([]CheckRecord, error) {
	query := `SELECT id, subject_type, subject_id, rule_type, ran_at, found, match_type, match_id, match_name, sentence
	          FROM smeldr_check_records WHERE subject_type = $1 AND subject_id = $2 ORDER BY ran_at DESC`
	args := []any{subjectType, subjectID}
	if limit > 0 {
		query += " LIMIT $3"
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("smeldr: CheckStore.List: %w", err)
	}
	defer rows.Close()

	var out []CheckRecord
	for rows.Next() {
		r, err := scanCheckRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("smeldr: CheckStore.List: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// scanCheckRecord scans one smeldr_check_records row in the column order
// shared by Last and List.
func scanCheckRecord(row rowScanner) (CheckRecord, error) {
	var r CheckRecord
	var ranAtStr string
	if err := row.Scan(&r.ID, &r.SubjectType, &r.SubjectID, &r.RuleType, &ranAtStr,
		&r.Found, &r.MatchType, &r.MatchID, &r.MatchName, &r.Sentence); err != nil {
		return CheckRecord{}, err
	}
	r.RanAt, _ = time.Parse(time.RFC3339, ranAtStr)
	return r, nil
}

// queryAuthorityCandidates is the cheap-retrieval half of Check
// (decision-governance-model.md §4's own cost-control requirement): two
// plain, indexed SELECTs against active Rules and unconverted
// AuthorityStubs sharing ruleType. A retired Rule or a converted/retired
// AuthorityStub is excluded — a converted stub's own Rule is found via the
// Rules query instead, and "no longer applies" must read as "not found,"
// matching Check's own "no relevant authority found" semantics.
func queryAuthorityCandidates(ctx context.Context, db DB, ruleType string) (rules []Rule, stubs []AuthorityStub, err error) {
	rules, err = Query[Rule](ctx, db,
		`SELECT * FROM smeldr_rules WHERE rule_type = $1 AND status = 'active'`, ruleType)
	if err != nil {
		return nil, nil, fmt.Errorf("smeldr: queryAuthorityCandidates: rules: %w", err)
	}
	stubs, err = Query[AuthorityStub](ctx, db,
		`SELECT * FROM smeldr_authority_stubs WHERE rule_type = $1 AND status = 'stub'`, ruleType)
	if err != nil {
		return nil, nil, fmt.Errorf("smeldr: queryAuthorityCandidates: authority stubs: %w", err)
	}
	return rules, stubs, nil
}

// RunAuthorityCheck is the full Check (decision-governance-model.md §4):
// cheap retrieval via [queryAuthorityCandidates], then — only when at least
// one candidate exists — the expensive half (candidate selection and
// sentence composition), then records the outcome via store.Append.
//
// Candidate selection, when more than one candidate matches: the
// most-recently-published one wins (Rule.PublishedAt / AuthorityStub.
// PublishedAt) — the same "most recent" convention [SweepRunStore.Last]
// already uses, a deliberate, simple default rather than a rank comparison
// (see the sentence note below).
//
// The sentence names existence, not rank: "this touches %q, an existing
// %s rule" — not "outranks." Check matches candidates by exact RuleType
// equality, so a matched candidate and the subject being checked share the
// same authority rank; claiming one "outranks" the other would assert a
// comparison the match itself cannot honestly support. A cross-rule-type
// inference (does a higher-ranked Rule constrain a lower-ranked subject) is
// a real, larger question for a future iteration, not built here.
//
// RunAuthorityCheck is fail-open at the persistence step only: a
// store.Append failure still returns the computed record (so a caller can
// act on the finding even though it could not be persisted), alongside the
// error. A queryAuthorityCandidates failure returns (nil, err) — there is no
// finding to act on in that case. Neither failure is this function's own
// concern to treat as blocking; that decision belongs to the caller (see
// both wiring sites in module.go/state.go, which log and proceed).
func RunAuthorityCheck(ctx context.Context, db DB, store CheckStore, subjectType, subjectID, ruleType string) (*CheckRecord, error) {
	rules, stubs, err := queryAuthorityCandidates(ctx, db, ruleType)
	if err != nil {
		return nil, fmt.Errorf("smeldr: RunAuthorityCheck: %w", err)
	}

	r := CheckRecord{
		ID:          NewID(),
		SubjectType: subjectType,
		SubjectID:   subjectID,
		RuleType:    ruleType,
		RanAt:       time.Now().UTC(),
	}

	// Candidate selection: most-recently-published Rule or AuthorityStub
	// wins, Rules and stubs compared on equal footing (both are real
	// Authority-graph nodes for Check's purposes).
	var bestPublishedAt time.Time
	for _, rule := range rules {
		if !r.Found || rule.PublishedAt.After(bestPublishedAt) {
			r.Found = true
			bestPublishedAt = rule.PublishedAt
			r.MatchType = "Rule"
			r.MatchID = rule.ID
			r.MatchName = rule.Slug
		}
	}
	for _, stub := range stubs {
		if !r.Found || stub.PublishedAt.After(bestPublishedAt) {
			r.Found = true
			bestPublishedAt = stub.PublishedAt
			r.MatchType = "AuthorityStub"
			r.MatchID = stub.ID
			r.MatchName = stub.SourceRef
		}
	}

	if r.Found {
		r.Sentence = fmt.Sprintf("this touches %q, an existing %s rule", r.MatchName, ruleType)
	} else {
		r.Sentence = "no conflicting authority found"
	}

	if err := store.Append(ctx, r); err != nil {
		slog.WarnContext(ctx, "smeldr: RunAuthorityCheck: record persist failed",
			"subject_type", subjectType, "subject_id", subjectID, "error", err)
		return &r, fmt.Errorf("smeldr: RunAuthorityCheck: %w", err)
	}
	return &r, nil
}

// runDecisionAuthorityCheck is [Module.updateHandler]'s own wiring of Check
// (decision-governance-model.md §4): a no-op unless item is a *Decision
// transitioning proposed→ratified and store is configured (App.Check was
// called). Fail-open — logs and returns on any RunAuthorityCheck error,
// never surfaced to the HTTP caller, matching App.Check's own godoc.
func runDecisionAuthorityCheck(ctx context.Context, db DB, store CheckStore, item any, fromStatus, toStatus string) {
	if store == nil || fromStatus != "proposed" || toStatus != "ratified" {
		return
	}
	d, ok := item.(*Decision)
	if !ok {
		return
	}
	if _, err := RunAuthorityCheck(ctx, db, store, "Decision", d.ID, d.RuleType); err != nil {
		slog.WarnContext(ctx, "smeldr: runDecisionAuthorityCheck failed",
			"subject_id", d.ID, "error", err)
	}
}

// runDecisionAuthorityCheckByID is [App.TransitionItem]'s own wiring of
// Check: a no-op unless typeName is "Decision," the transition is
// proposed→ratified, and store is configured. Unlike
// runDecisionAuthorityCheck, TransitionItem never decodes a full Decision
// struct (it operates generically across compiled types via raw SQL), so
// RuleType is fetched directly by id. Fail-open, same as above — a lookup
// error is logged, never returned to the caller.
func runDecisionAuthorityCheckByID(ctx context.Context, db DB, store CheckStore, typeName, itemID, fromStatus, toStatus string) {
	if store == nil || typeName != "Decision" || fromStatus != "proposed" || toStatus != "ratified" {
		return
	}
	var ruleType string
	if err := db.QueryRowContext(ctx,
		`SELECT rule_type FROM smeldr_decisions WHERE id = $1`, itemID,
	).Scan(&ruleType); err != nil {
		slog.WarnContext(ctx, "smeldr: runDecisionAuthorityCheckByID: read rule_type failed",
			"subject_id", itemID, "error", err)
		return
	}
	if _, err := RunAuthorityCheck(ctx, db, store, "Decision", itemID, ruleType); err != nil {
		slog.WarnContext(ctx, "smeldr: runDecisionAuthorityCheckByID failed",
			"subject_id", itemID, "error", err)
	}
}
