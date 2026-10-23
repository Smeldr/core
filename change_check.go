// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The pull request check (D142 point 8, D143, D144, T918): a proposed change
// to a repository is checked against the Decisions in force for the paths it
// touches, through the Areas that cover them. Core is host-neutral and never
// calls the host: an adapter (smeldr.dev/github) turns the host's events into
// [App.CheckChange] calls and posts the verdict.

// ChangeCheck is one checked commit of a proposed change (a pull request on
// GitHub, a merge request elsewhere): the Areas it touches, the Decisions in
// force for them, and its verdict as the record's state. Only the newest
// checked commit of a change is live; an older one is superseded (D143). It is
// made by [App.CheckChange] only, never through a create or update tool, and
// its fields hold only what every reader of it may see: counts, the
// repository, the number and the commit, never an Area's or a Decision's name
// (Turn 20). It is placed in every Area it touches and related to its
// Decisions by checked_against, so each reader sees the parts it may read.
type ChangeCheck struct {
	Node
	// RecordAddress is the Check's address (D110, prefix CHK).
	RecordAddress
	// Repository is owner/name, in lower case.
	Repository string `json:"repository" db:"repository" smeldr:"readonly"`
	// Number is the change's number on the host (the pull request number).
	Number int `json:"number" db:"number" smeldr:"readonly"`
	// HeadCommit is the checked commit: a full 40-character SHA.
	HeadCommit string `json:"head_commit" db:"head_commit" smeldr:"readonly"`
	// FilesTotal is how many files the change touches, as the host counts
	// them.
	FilesTotal int `json:"files_total" db:"files_total" smeldr:"readonly"`
	// FilesChecked is how many paths core resolved: at most
	// [DecisionsForPathsMaxPaths], the most the host lists.
	FilesChecked int `json:"files_checked" db:"files_checked" smeldr:"readonly"`
	// CheckedPartly is true when the change touches more files than were
	// checked.
	CheckedPartly bool `json:"checked_partly" db:"checked_partly" smeldr:"readonly"`
	// Setting is the strictest approval mode among the Areas it touches:
	// what kind of check it is, without naming the Areas.
	Setting string `json:"setting" db:"setting" smeldr:"readonly"`
	// LastActor is the actor of the latest write (D78).
	LastActor string `json:"last_actor,omitempty" db:"last_actor"`
}

// The states of a [ChangeCheck] (the change-check flow).
const (
	// ChangeCheckChecking: made, its verdict not set yet.
	ChangeCheckChecking = "checking"
	// ChangeCheckCommented: no touched Area needs approval; the host gets a
	// comment.
	ChangeCheckCommented = "commented"
	// ChangeCheckAwaitingApproval: some touched Area needs approval.
	ChangeCheckAwaitingApproval = "awaiting-approval"
	// ChangeCheckApproved: every Area that needs approval approved it.
	ChangeCheckApproved = "approved"
	// ChangeCheckNotApproved: an Area that needs approval did not approve it.
	ChangeCheckNotApproved = "not-approved"
	// ChangeCheckSuperseded: a newer commit of the change was checked.
	ChangeCheckSuperseded = "superseded"
)

// The verdict of one Area of a [ChangeCheck].
const (
	// AreaVerdictCommented: the Area only comments.
	AreaVerdictCommented = "commented"
	// AreaVerdictAwaiting: the Area needs approval and has not answered.
	AreaVerdictAwaiting = "awaiting-approval"
)

// changeCheckFlow is the flow of [ChangeCheck]. Every transition is core's,
// made with a reason; the verdict states are locked.
func changeCheckFlow() StateFlow {
	t := func(from, to string) Transition { return Transition{From: from, To: to} }
	return StateFlow{
		Name:     "change-check",
		TypeName: "ChangeCheck",
		States: []State{
			{Name: ChangeCheckChecking, IsInitial: true},
			{Name: ChangeCheckCommented, Locked: true},
			{Name: ChangeCheckAwaitingApproval, Locked: true, AwaitsAction: true},
			{Name: ChangeCheckApproved, Locked: true},
			{Name: ChangeCheckNotApproved, Locked: true},
			{Name: ChangeCheckSuperseded, Locked: true, IsTerminal: true},
		},
		Transitions: []Transition{
			t(ChangeCheckChecking, ChangeCheckCommented),
			t(ChangeCheckChecking, ChangeCheckAwaitingApproval),
			t(ChangeCheckAwaitingApproval, ChangeCheckApproved),
			t(ChangeCheckAwaitingApproval, ChangeCheckNotApproved),
			t(ChangeCheckChecking, ChangeCheckSuperseded),
			t(ChangeCheckCommented, ChangeCheckSuperseded),
			t(ChangeCheckAwaitingApproval, ChangeCheckSuperseded),
			t(ChangeCheckApproved, ChangeCheckSuperseded),
			t(ChangeCheckNotApproved, ChangeCheckSuperseded),
		},
	}
}

// changeCheckTableStmts create the Checks, their Areas, and the indexes: the
// unique (repository, number, head_commit) makes a redelivered check one
// Check (T918 condition 1).
var changeCheckTableStmts = []string{
	`CREATE TABLE IF NOT EXISTS smeldr_change_checks (
		id             TEXT PRIMARY KEY,
		slug           TEXT NOT NULL UNIQUE,
		status         TEXT NOT NULL DEFAULT 'checking',
		published_at   TIMESTAMPTZ,
		scheduled_at   TIMESTAMPTZ,
		created_at     TIMESTAMPTZ NOT NULL,
		updated_at     TIMESTAMPTZ NOT NULL,
		rev            INTEGER NOT NULL DEFAULT 0,
		repository     TEXT NOT NULL DEFAULT '',
		number         INTEGER NOT NULL DEFAULT 0,
		head_commit    TEXT NOT NULL DEFAULT '',
		files_total    INTEGER NOT NULL DEFAULT 0,
		files_checked  INTEGER NOT NULL DEFAULT 0,
		checked_partly BOOLEAN NOT NULL DEFAULT FALSE,
		setting        TEXT NOT NULL DEFAULT '',
		last_actor     TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_smeldr_change_checks_commit ON smeldr_change_checks (repository, number, head_commit)`,
	`CREATE INDEX IF NOT EXISTS idx_smeldr_change_checks_change ON smeldr_change_checks (repository, number, status)`,
	`CREATE TABLE IF NOT EXISTS smeldr_change_check_areas (
		check_id      TEXT NOT NULL,
		area_id       TEXT NOT NULL,
		approval_mode TEXT NOT NULL DEFAULT '',
		verdict       TEXT NOT NULL DEFAULT '',
		answered_by   TEXT NOT NULL DEFAULT '',
		answered_at   TIMESTAMPTZ,
		reason        TEXT NOT NULL DEFAULT '',
		signal_id     TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (check_id, area_id)
	)`,
}

// EnsureChangeCheckTables creates the tables of the pull request check
// ([ChangeCheck], its Areas) on a database made before them, with their
// address storage (CHK). Fresh installs get them from
// [CreateGovernanceTables]. Idempotent, safe to call on every boot.
func EnsureChangeCheckTables(ctx context.Context, db DB) error {
	for _, stmt := range changeCheckTableStmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("smeldr: EnsureChangeCheckTables: %w", err)
		}
	}
	if err := registerCoreAddresses(ctx, db, "ChangeCheck"); err != nil {
		return fmt.Errorf("smeldr: EnsureChangeCheckTables: %w", err)
	}
	// The request of each Area (T918 part 2), on a table made before it.
	if err := EnsureColumn(ctx, db, "smeldr_change_check_areas", "signal_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("smeldr: EnsureChangeCheckTables: %w", err)
	}
	return nil
}

// registerChangeCheck registers [ChangeCheck] with its flow, read-only to
// every write surface: core makes and moves it. Called by
// [RegisterGovernanceTypes].
func registerChangeCheck(app *App, db DB) {
	registerFlows(app, changeCheckFlow())
	refuse := func(Context, any, any) error {
		return fmt.Errorf("%w: a ChangeCheck is made only by check_change (App.CheckChange) and moved only by core", ErrForbidden)
	}
	app.Content(NewModule[*ChangeCheck]((*ChangeCheck)(nil),
		At("/change-checks"), Repo(NewSQLRepo[*ChangeCheck](db, Table("smeldr_change_checks"))), MCP(MCPRead), withAddress(),
		saveHooksOption{prepare: refuse},
		On[*ChangeCheck](BeforeDelete, func(Context, *ChangeCheck) error {
			return fmt.Errorf("%w: a ChangeCheck is not deleted; how long Checks are kept is decided later (D143)", ErrForbidden)
		}),
	))
}

// ChangeInput is one checked commit of a proposed change, as the host's
// adapter reports it.
type ChangeInput struct {
	// Repository is owner/name.
	Repository string
	// Number is the change's number on the host.
	Number int
	// HeadCommit is the commit checked: a full 40-character SHA.
	HeadCommit string
	// Paths are the changed files, at most [DecisionsForPathsMaxPaths].
	Paths []string
	// FilesTotal is how many files the change touches; 0 means len(Paths).
	FilesTotal int
}

// ChangeCheckArea is one Area of a Check, as [App.CheckChange] and
// [App.ChangeCheckOf] show it.
type ChangeCheckArea struct {
	ID           string   `json:"id"`
	Address      string   `json:"address"`
	Name         string   `json:"name"`
	ApprovalMode string   `json:"approval_mode"`
	Verdict      string   `json:"verdict"`
	Paths        []string `json:"paths,omitempty"` // only in the result of CheckChange
}

// ChangeCheckResult is a Check with its Areas and Decisions.
type ChangeCheckResult struct {
	// Covered is false when no Area covers the change: then no Check is made
	// (D143) and the rest is empty.
	Covered bool `json:"covered"`
	// Check is the Check; its Status is the verdict.
	Check *ChangeCheck `json:"check,omitempty"`
	// Areas are the touched Areas the caller may read.
	Areas []ChangeCheckArea `json:"areas"`
	// Decisions are the Decisions in force the caller may read.
	Decisions []AppliedDecision `json:"decisions"`
	// Uncovered are the paths no Area covers (CheckChange only).
	Uncovered []string `json:"uncovered,omitempty"`
}

// changeCheckCommitRe is the shape of a head commit.
var changeCheckCommitRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// approvalModeRank orders the approval modes by strictness, for a Check's
// setting.
var approvalModeRank = map[string]int{ApprovalModeCommentOnly: 0, ApprovalModeRequiresApproval: 2}

// normalizeChangeInput checks in and returns it in its stored form.
func normalizeChangeInput(in ChangeInput) (ChangeInput, error) {
	repo, err := normalizeRepository(in.Repository)
	if err != nil {
		return in, err
	}
	in.Repository = repo
	if in.Number <= 0 {
		return in, Err("number", "must be the change's number on the host, above 0")
	}
	in.HeadCommit = strings.ToLower(strings.TrimSpace(in.HeadCommit))
	if !changeCheckCommitRe.MatchString(in.HeadCommit) {
		return in, Err("head_commit", "must be a full 40-character commit SHA")
	}
	if len(in.Paths) > DecisionsForPathsMaxPaths {
		return in, fmt.Errorf("%w: at most %d paths per check (the most the host lists); give the first %d and files_total", ErrBadRequest, DecisionsForPathsMaxPaths, DecisionsForPathsMaxPaths)
	}
	if in.FilesTotal == 0 {
		in.FilesTotal = len(in.Paths)
	}
	if in.FilesTotal < len(in.Paths) {
		return in, Err("files_total", fmt.Sprintf("is %d, fewer than the %d paths given", in.FilesTotal, len(in.Paths)))
	}
	return in, nil
}

// authorizeCheckChange requires the check_change capability of the caller
// under governance: the adapter's token or an operator.
func (a *App) authorizeCheckChange(ctx context.Context) error {
	if a.governance == nil {
		return nil
	}
	sc, ok := ctx.(Context)
	if !ok {
		return fmt.Errorf("%w: check_change needs a caller", ErrForbidden)
	}
	return a.AuthorizeCapability(sc, ResourceItem, "check_change", AuthTarget{})
}

// CheckChange checks one commit of a proposed change (D142 point 8, D143,
// T918). It resolves the paths with [App.DecisionsForPaths] under a system
// read scope, so a Check is the same for every caller. When no Area covers
// them, no Check is made and the result says Covered false. Otherwise it
// makes the Check in one transaction (its Areas with their approval modes,
// belongs_to_area to each, checked_against to each Decision in force,
// supersedes from the change's previous live Check), supersedes that previous
// Check, and moves the new one to commented, or to awaiting-approval when a
// touched Area requires approval.
//
// The same repository, number and commit again returns the existing Check
// (a redelivered event), also when two arrive at once. The caller needs the
// check_change capability under governance and gets the full result.
func (a *App) CheckChange(ctx context.Context, in ChangeInput) (ChangeCheckResult, error) {
	in, err := normalizeChangeInput(in)
	if err != nil {
		return ChangeCheckResult{}, err
	}
	if err := a.authorizeCheckChange(ctx); err != nil {
		return ChangeCheckResult{}, err
	}
	if a.cfg.DB == nil {
		return ChangeCheckResult{}, fmt.Errorf("%w: CheckChange needs a database", ErrInternal)
	}
	sys := WithReadScope(ctx, AllReadScope())
	res, err := a.DecisionsForPaths(sys, DecisionsForPathsQuery{Repository: in.Repository, Paths: in.Paths})
	if err != nil {
		return ChangeCheckResult{}, err
	}
	if len(res.Areas) == 0 {
		return ChangeCheckResult{Covered: false, Areas: []ChangeCheckArea{}, Decisions: []AppliedDecision{}, Uncovered: res.Uncovered}, nil
	}
	check, err := a.findChangeCheck(sys, in.Repository, in.Number, in.HeadCommit)
	if err != nil {
		return ChangeCheckResult{}, err
	}
	if check == nil {
		if _, err := a.insertChangeCheck(sys, in, res); err != nil {
			return ChangeCheckResult{}, err
		}
	}
	// One live Check per change (D143), on every call: a supersede that
	// failed before, or a newer commit checked at the same moment, is
	// repaired here.
	if err := a.supersedeOlderChecks(sys, in.Repository, in.Number); err != nil {
		return ChangeCheckResult{}, err
	}
	if check, err = a.findChangeCheck(sys, in.Repository, in.Number, in.HeadCommit); err != nil {
		return ChangeCheckResult{}, err
	}
	if err := a.settleChangeCheck(sys, check); err != nil {
		return ChangeCheckResult{}, err
	}
	out, err := a.changeCheckView(sys, check.ID)
	if err != nil {
		return ChangeCheckResult{}, err
	}
	paths := map[string][]string{}
	for _, area := range res.Areas {
		paths[area.ID] = area.Paths
	}
	for i := range out.Areas {
		out.Areas[i].Paths = paths[out.Areas[i].ID]
	}
	out.Decisions = res.Decisions
	out.Uncovered = res.Uncovered
	return out, nil
}

// findChangeCheck returns the Check of repository, number and commit, or nil.
func (a *App) findChangeCheck(ctx context.Context, repo string, number int, commit string) (*ChangeCheck, error) {
	c, err := QueryOne[*ChangeCheck](ctx, a.cfg.DB,
		`SELECT * FROM smeldr_change_checks WHERE repository = $1 AND number = $2 AND head_commit = $3`, repo, number, commit)
	switch {
	case errors.Is(err, ErrNotFound):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("%w: read the Check of %s#%d at %s: %v", ErrInternal, repo, number, commit, err)
	}
	return c, nil
}

// insertChangeCheck makes the Check of in in one transaction, or returns the
// one another call made at the same moment.
func (a *App) insertChangeCheck(ctx context.Context, in ChangeInput, res DecisionsForPathsResult) (*ChangeCheck, error) {
	db := a.cfg.DB
	setting := ApprovalModeCommentOnly
	for _, area := range res.Areas {
		if approvalModeRank[area.ApprovalMode] > approvalModeRank[setting] {
			setting = area.ApprovalMode
		}
	}
	actor, _ := actorFromContext(ctx)
	now := time.Now().UTC()
	c := &ChangeCheck{
		Node:       Node{ID: NewID(), Status: ChangeCheckChecking, CreatedAt: now, UpdatedAt: now},
		Repository: in.Repository, Number: in.Number, HeadCommit: in.HeadCommit,
		FilesTotal: in.FilesTotal, FilesChecked: len(in.Paths), CheckedPartly: in.FilesTotal > len(in.Paths),
		Setting: setting, LastActor: actor,
	}
	c.Slug = "chk-" + strings.ToLower(c.ID)
	var edges []RelationEdge
	inserted := false
	err := runInTx(ctx, db, func(exec DB) error {
		address, no, err := allocateAddress(ctx, exec, "ChangeCheck")
		if err != nil {
			return err
		}
		c.assignAddress(address, no)
		r, err := exec.ExecContext(ctx,
			`INSERT INTO smeldr_change_checks (id, slug, status, created_at, updated_at, rev, repository, number, head_commit,
				files_total, files_checked, checked_partly, setting, last_actor, address, address_no, external_ref)
			 SELECT $1, $2, $3, $4, $5, 0, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, ''
			 WHERE NOT EXISTS (SELECT 1 FROM smeldr_change_checks WHERE repository = $6 AND number = $7 AND head_commit = $8)`,
			c.ID, c.Slug, c.Status, now, now, c.Repository, c.Number, c.HeadCommit,
			c.FilesTotal, c.FilesChecked, c.CheckedPartly, c.Setting, c.LastActor, address, no)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return nil // another call made it: read it after the transaction
		}
		inserted = true
		job := "change-check"
		edge := func(targetType, targetID, kind string) error {
			e, wrote, err := writeEdgeRow(ctx, exec, RelationEdge{SourceType: "ChangeCheck", SourceID: c.ID, TargetType: targetType, TargetID: targetID,
				RelationKind: kind, EdgeClass: "asserted", CreatedByJob: &job, Attributes: json.RawMessage("{}")})
			if err == nil && wrote {
				edges = append(edges, e)
			}
			return err
		}
		for _, area := range res.Areas {
			verdict := AreaVerdictCommented
			if area.ApprovalMode == ApprovalModeRequiresApproval {
				verdict = AreaVerdictAwaiting
			}
			if _, err := exec.ExecContext(ctx,
				`INSERT INTO smeldr_change_check_areas (check_id, area_id, approval_mode, verdict) VALUES ($1, $2, $3, $4)`,
				c.ID, area.ID, area.ApprovalMode, verdict); err != nil {
				return err
			}
			if err := edge("Area", area.ID, "belongs_to_area"); err != nil {
				return err
			}
		}
		for _, d := range res.Decisions {
			if err := edge("Decision", d.ID, "checked_against"); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		// A concurrent call may have taken the unique (repository, number,
		// head_commit) between the check and the insert: its Check wins.
		if existing, ferr := a.findChangeCheck(ctx, in.Repository, in.Number, in.HeadCommit); ferr == nil && existing != nil {
			return existing, nil
		}
		return nil, fmt.Errorf("%w: make the Check of %s#%d: %v", ErrInternal, in.Repository, in.Number, err)
	}
	if !inserted {
		existing, err := a.findChangeCheck(ctx, in.Repository, in.Number, in.HeadCommit)
		if err != nil {
			return nil, err
		}
		if existing == nil {
			return nil, fmt.Errorf("%w: the Check of %s#%d at %s was neither made nor found", ErrInternal, in.Repository, in.Number, in.HeadCommit)
		}
		return existing, nil
	}
	actorID, actorKind := actorFromContext(ctx)
	recordProvenance(ctx, a.provenanceStore, ProvenanceRecord{
		SubjectType: "ChangeCheck", SubjectID: c.ID, Verb: "create", ActorID: actorID, ActorKind: actorKind,
		Reason: fmt.Sprintf("check of %s#%d at %s", c.Repository, c.Number, c.HeadCommit[:12]),
	})
	if a.relationStore != nil {
		for _, e := range edges {
			a.relationStore.announceEdge(ctx, e, true)
		}
	}
	return c, nil
}

// claimSupersede moves the Check old to superseded when it is not already,
// with one conditional write and the supersedes edge from newest in the same
// transaction: of several calls at the same moment exactly one moves it
// (and reports true), and a superseded Check is never seen without its edge.
// After the commit it records the change, announces it and the edge, and
// withdraws the Check's open requests (part 2).
func (a *App) claimSupersede(ctx context.Context, oldID, oldAddress, newestID, newestAddress string) (bool, error) {
	db := a.cfg.DB
	actor, kind := actorFromContext(ctx)
	reason := "superseded by " + newestAddress
	job := "change-check"
	for range 3 {
		var from string
		if err := db.QueryRowContext(ctx, `SELECT status FROM smeldr_change_checks WHERE id = $1`, oldID).Scan(&from); err != nil {
			return false, fmt.Errorf("%w: read %s: %v", ErrInternal, oldAddress, err)
		}
		if from == ChangeCheckSuperseded {
			return false, nil
		}
		var edge RelationEdge
		moved := false
		err := runInTx(ctx, db, func(exec DB) error {
			res, err := exec.ExecContext(ctx,
				`UPDATE smeldr_change_checks SET status = $1, updated_at = $2, last_actor = $3, rev = rev + 1 WHERE id = $4 AND status = $5`,
				ChangeCheckSuperseded, time.Now().UTC(), actor, oldID, from)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return nil // its state moved meanwhile
			}
			moved = true
			edge, _, err = writeEdgeRow(ctx, exec, RelationEdge{SourceType: "ChangeCheck", SourceID: newestID, TargetType: "ChangeCheck", TargetID: oldID,
				RelationKind: "supersedes", EdgeClass: "asserted", CreatedByJob: &job, Attributes: json.RawMessage("{}")})
			return err
		})
		if err != nil {
			return false, fmt.Errorf("%w: %s supersedes %s: %v", ErrInternal, newestAddress, oldAddress, err)
		}
		if !moved {
			continue // read it again
		}
		applyStateChange(ctx, db, a.provenanceStore, stateChange{
			typeName: "ChangeCheck", id: oldID, from: from, to: ChangeCheckSuperseded, reason: reason, actorKind: kind, actorID: actor,
		})
		dispatchTransitionWebhook(ctx, a.webhookStore, a.webhookPool, a.eventBroadcaster, "", "change_check.transitioned",
			transitionWebhookData{Type: "change_check", ID: oldID, Address: oldAddress, FromState: from, ToState: ChangeCheckSuperseded, Reason: reason, ActorID: actor, ActorKind: kind})
		if a.relationStore != nil {
			a.relationStore.announceEdge(ctx, edge, true)
		}
		// Its answers would count for nothing now (T918 part 2).
		if err := a.withdrawChangeCheckRequests(ctx, oldID, reason); err != nil {
			return true, err
		}
		return true, nil
	}
	return false, fmt.Errorf("%w: %s kept changing while it was superseded", ErrConflict, oldAddress)
}

// supersedeOlderChecks leaves the newest Check of a change (the highest
// address number: numbers are taken in creation order) as its only live one:
// every other Check of the change not yet superseded moves to superseded,
// and the call that moved it writes the supersedes edge from the newest. It
// is safe to run on every call and at the same moment as another: a Check
// another call superseded first is left as it is.
func (a *App) supersedeOlderChecks(ctx context.Context, repo string, number int) error {
	db := a.cfg.DB
	rows, err := db.QueryContext(ctx,
		`SELECT id, address, status FROM smeldr_change_checks WHERE repository = $1 AND number = $2
		 ORDER BY address_no DESC`, repo, number)
	if err != nil {
		return fmt.Errorf("%w: the Checks of %s#%d: %v", ErrInternal, repo, number, err)
	}
	type row struct{ id, address, status string }
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.address, &r.status); err != nil {
			rows.Close()
			return fmt.Errorf("%w: the Checks of %s#%d: %v", ErrInternal, repo, number, err)
		}
		all = append(all, r)
	}
	rows.Close()
	if len(all) < 2 {
		return nil
	}
	newest := all[0]
	job := "change-check"
	for _, old := range all[1:] {
		if old.status != ChangeCheckSuperseded {
			if _, err := a.claimSupersede(ctx, old.id, old.address, newest.id, newest.address); err != nil {
				return err
			}
			continue
		}
		// A superseded Check keeps no open request: one whose withdrawal
		// failed after the claim is withdrawn now (S3154).
		if err := a.withdrawChangeCheckRequests(ctx, old.id, "superseded by "+newest.address); err != nil {
			return err
		}
		// A Check superseded before the edge was written with it (an older
		// row) gets the edge from the newest.
		var n int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM smeldr_relations WHERE target_id = $1 AND relation_kind = 'supersedes' AND invalid_at IS NULL`, old.id).Scan(&n); err != nil {
			return fmt.Errorf("%w: the supersedes edges of %s: %v", ErrInternal, old.address, err)
		}
		if n > 0 {
			continue
		}
		edge, wrote, err := writeEdgeRow(ctx, db, RelationEdge{SourceType: "ChangeCheck", SourceID: newest.id, TargetType: "ChangeCheck", TargetID: old.id,
			RelationKind: "supersedes", EdgeClass: "asserted", CreatedByJob: &job, Attributes: json.RawMessage("{}")})
		if err != nil {
			return fmt.Errorf("%w: %s supersedes %s: %v", ErrInternal, newest.address, old.address, err)
		}
		if wrote && a.relationStore != nil {
			a.relationStore.announceEdge(ctx, edge, true)
		}
	}
	return nil
}

// settleChangeCheck moves a Check still in checking to its verdict:
// awaiting-approval when a touched Area requires approval, else commented. A
// Check past checking is left as it is (a redelivered event).
func (a *App) settleChangeCheck(ctx context.Context, c *ChangeCheck) error {
	if string(c.Status) == ChangeCheckAwaitingApproval {
		// A redelivery opens a request that failed to open before.
		return a.requestChangeCheckApprovals(ctx, c)
	}
	if string(c.Status) != ChangeCheckChecking {
		return nil
	}
	to, reason := ChangeCheckCommented, "no touched Area requires approval"
	if c.Setting == ApprovalModeRequiresApproval {
		to, reason = ChangeCheckAwaitingApproval, "a touched Area requires approval"
	}
	if _, err := a.TransitionItemVia(ctx, "", "ChangeCheck", c.ID, to, reason); err != nil {
		return err
	}
	c.Status = Status(to)
	if to == ChangeCheckAwaitingApproval {
		return a.requestChangeCheckApprovals(ctx, c)
	}
	return nil
}

// ChangeCheckOf returns the Check ident names (address, slug or ID) with the
// touched Areas and the Decisions the caller may read: each reader sees only
// the parts of a Check that its read rules allow (Turn 20). A Check the
// caller may not read is [ErrNotFound].
func (a *App) ChangeCheckOf(ctx context.Context, ident string) (ChangeCheckResult, error) {
	if a.cfg.DB == nil {
		return ChangeCheckResult{}, fmt.Errorf("%w: ChangeCheckOf needs a database", ErrInternal)
	}
	scope, err := scopeFor(ctx, a.governance)
	if err != nil {
		return ChangeCheckResult{}, err
	}
	// The lookup applies the read rules: a Check the caller may not read is
	// not found.
	_, id, _, _, err := a.lookupCompiledRecord(ctx, "ChangeCheck", ident, scope)
	if err != nil {
		return ChangeCheckResult{}, err
	}
	return a.changeCheckView(WithReadScope(ctx, scope), id)
}

// changeCheckView reads the Check id with its Areas and checked_against
// Decisions, under the read scope in ctx.
func (a *App) changeCheckView(ctx context.Context, id string) (ChangeCheckResult, error) {
	db := a.cfg.DB
	scope, err := scopeFor(ctx, a.governance)
	if err != nil {
		return ChangeCheckResult{}, err
	}
	c, err := QueryOne[*ChangeCheck](ctx, db, `SELECT * FROM smeldr_change_checks WHERE id = $1`, id)
	if err != nil {
		return ChangeCheckResult{}, fmt.Errorf("%w: read the Check %s: %v", ErrInternal, id, err)
	}
	out := ChangeCheckResult{Covered: true, Check: c, Areas: []ChangeCheckArea{}, Decisions: []AppliedDecision{}}
	rows, err := db.QueryContext(ctx,
		`SELECT a.id, a.address, a.name, c.approval_mode, c.verdict FROM smeldr_change_check_areas c
		   JOIN smeldr_areas a ON a.id = c.area_id WHERE c.check_id = $1`, id)
	if err != nil {
		return ChangeCheckResult{}, fmt.Errorf("%w: read the Areas of the Check %s: %v", ErrInternal, c.Address, err)
	}
	for rows.Next() {
		var area ChangeCheckArea
		if err := rows.Scan(&area.ID, &area.Address, &area.Name, &area.ApprovalMode, &area.Verdict); err != nil {
			rows.Close()
			return ChangeCheckResult{}, fmt.Errorf("%w: read the Areas of the Check %s: %v", ErrInternal, c.Address, err)
		}
		if scope.Readable("Area", area.ID) {
			out.Areas = append(out.Areas, area)
		}
	}
	rows.Close()
	sort.Slice(out.Areas, func(i, j int) bool { return out.Areas[i].Address < out.Areas[j].Address })
	rows, err = db.QueryContext(ctx,
		`SELECT d.id, d.address, d.title, d.status FROM smeldr_relations r JOIN smeldr_decisions d ON d.id = r.target_id
		  WHERE r.source_id = $1 AND r.relation_kind = 'checked_against' AND r.invalid_at IS NULL`, id)
	if err != nil {
		return ChangeCheckResult{}, fmt.Errorf("%w: read the Decisions of the Check %s: %v", ErrInternal, c.Address, err)
	}
	defer rows.Close()
	for rows.Next() {
		var d AppliedDecision
		if err := rows.Scan(&d.ID, &d.Address, &d.Title, &d.State); err != nil {
			return ChangeCheckResult{}, fmt.Errorf("%w: read the Decisions of the Check %s: %v", ErrInternal, c.Address, err)
		}
		if scope.Readable("Decision", d.ID) {
			out.Decisions = append(out.Decisions, d)
		}
	}
	sort.Slice(out.Decisions, func(i, j int) bool { return out.Decisions[i].Address < out.Decisions[j].Address })
	return out, rows.Err()
}
