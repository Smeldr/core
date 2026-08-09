// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// Goal is an orchestration content type representing a work goal — a unit of
// intentional work that can be linked to Decisions and Tasks via the relation
// graph. It models the TODO-table rows in ARCHITECT_TODO.md as live Smeldr items.
type Goal struct {
	Node
	// GoalID is the canonical identifier (e.g. "T114").
	GoalID string `json:"goal_id" db:"goal_id"`
	// Priority is the scheduling priority. Lower values are higher priority.
	Priority int `json:"priority"`
	// Band groups goals by work band (e.g. "P0", "P1", "P2", "P3").
	Band string `json:"band"`
	// Size is the effort estimate (e.g. "S", "M", "L", "XL").
	Size string `json:"size"`
	// Description is the full goal specification in Markdown.
	Description string `json:"description" smeldr_format:"markdown"`
}

// GoalContext is the assembled context for a single [Goal] — the goal itself
// plus all items linked to it via the relation graph (Decisions, Tasks,
// other Goals). Assembled by [QueryGoalContext] and used by the
// get_goal_context MCP tool.
type GoalContext struct {
	Goal            *Goal
	LinkedDecisions []Decision
	LinkedTasks     []Task
	LinkedGoals     []Goal
}

// QueryGoalContext assembles context for the goal whose GoalID field matches
// goalID (e.g. "T114"). It queries both source and target sides of the
// relation graph and appends each linked Decision, Task, or Goal.
// Returns [ErrNotFound] when no goal with the given GoalID exists.
// When rs is nil, returns the goal with empty relation slices (fail-open).
func QueryGoalContext(ctx context.Context, db DB, rs *RelationStore, goalID string) (*GoalContext, error) {
	if goalID == "" {
		return nil, ErrBadRequest
	}
	if db == nil {
		return nil, ErrInternal
	}

	goal, err := QueryOne[*Goal](ctx, db,
		`SELECT * FROM smeldr_goals WHERE goal_id = $1`, goalID)
	if err != nil {
		return nil, err
	}

	gc := &GoalContext{
		Goal:            goal,
		LinkedDecisions: []Decision{},
		LinkedTasks:     []Task{},
		LinkedGoals:     []Goal{},
	}
	if rs == nil {
		return gc, nil
	}

	// Collect edges on both sides of the relation graph and deduplicate by ID.
	type relRef struct {
		typeName string
		id       string
	}
	seen := map[string]bool{}
	var refs []relRef

	srcEdges, err := rs.GetBySource(ctx, "Goal", goal.ID, "")
	if err != nil {
		return nil, err
	}
	for _, e := range srcEdges {
		if seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		refs = append(refs, relRef{typeName: e.TargetType, id: e.TargetID})
	}

	tgtEdges, err := rs.GetByTarget(ctx, "Goal", goal.ID, "")
	if err != nil {
		return nil, err
	}
	for _, e := range tgtEdges {
		if seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		refs = append(refs, relRef{typeName: e.SourceType, id: e.SourceID})
	}

	for _, ref := range refs {
		switch ref.typeName {
		case "Decision":
			d, err := QueryOne[*Decision](ctx, db,
				`SELECT * FROM smeldr_decisions WHERE id = $1`, ref.id)
			if err != nil {
				slog.WarnContext(ctx, "smeldr: QueryGoalContext: skipping Decision",
					"id", ref.id, "error", err)
				continue
			}
			gc.LinkedDecisions = append(gc.LinkedDecisions, *d)
		case "Task":
			t, err := QueryOne[*Task](ctx, db,
				`SELECT * FROM smeldr_tasks WHERE id = $1`, ref.id)
			if err != nil {
				slog.WarnContext(ctx, "smeldr: QueryGoalContext: skipping Task",
					"id", ref.id, "error", err)
				continue
			}
			gc.LinkedTasks = append(gc.LinkedTasks, *t)
		case "Goal":
			if ref.id == goal.ID {
				continue // skip self-links
			}
			g, err := QueryOne[*Goal](ctx, db,
				`SELECT * FROM smeldr_goals WHERE id = $1`, ref.id)
			if err != nil {
				slog.WarnContext(ctx, "smeldr: QueryGoalContext: skipping Goal",
					"id", ref.id, "error", err)
				continue
			}
			gc.LinkedGoals = append(gc.LinkedGoals, *g)
		}
	}

	return gc, nil
}

// Signal is an orchestration content type representing a protocol message
// between pilots and the architect. It models the file-based SIGNAL_CORE.md
// / SIGNAL_SITE.md hand-off protocol as a persisted, state-managed record.
type Signal struct {
	Node
	// Sender is the originating agent identifier (e.g. "core", "site").
	Sender string `json:"sender"`
	// Receiver is the destination agent identifier (e.g. "architect").
	Receiver string `json:"receiver"`
	// SignalType is the protocol signal name (e.g. "plan-ready", "commit-ready").
	SignalType string `json:"signal_type" db:"signal_type"`
	// Message is the free-text body of the signal.
	Message string `json:"message"`
	// TaskRef is the optional task or amendment identifier this signal relates to.
	TaskRef string `json:"task_ref" db:"task_ref"`
	// Sequence is the per-task monotonic counter that orders signals in a conversation.
	Sequence int `json:"sequence"`
}

// Task is an orchestration content type representing a work item moving
// through the architect/pilot protocol state machine.
type Task struct {
	Node
	// TaskID is the canonical task identifier (e.g. "T23").
	TaskID string `json:"task_id" db:"task_id"`
	// Priority is the scheduling priority. Lower values are higher priority.
	Priority int `json:"priority"`
	// Band groups tasks into work bands (e.g. "M", "T", "R").
	Band string `json:"band"`
	// Size is the effort estimate (e.g. "S", "M", "L", "XL").
	Size string `json:"size"`
	// Description is the full task specification in Markdown.
	Description string `json:"description" smeldr_format:"markdown"`
	// NoteRef is an optional cross-reference to a design note or decision.
	NoteRef string `json:"note_ref" db:"note_ref"`
}

// Decision is an orchestration content type representing a ratified
// architectural decision with a structured freshness evaluation cycle.
type Decision struct {
	Node
	// DecisionNumber is the canonical identifier (e.g. "D22" or "A183").
	DecisionNumber string `json:"decision_number" db:"decision_number"`
	// Scope categorises the decision (e.g. "core", "agent", "cross-cutting").
	Scope string `json:"scope"`
	// Body is the full decision text in Markdown, including rationale.
	Body string `json:"body" smeldr_format:"markdown"`
	// NextEvalAt is the scheduled re-evaluation date. Zero means no scheduled review.
	NextEvalAt time.Time `json:"next_eval_at" db:"next_eval_at"`
	// EvalNote records the outcome of the most recent evaluation pass.
	EvalNote string `json:"eval_note" db:"eval_note"`
}

// decisionScopeRoles maps a Decision's Scope field to the role name required
// to transition it — ratify (proposed→ratified) or supersede
// (ratified→superseded) — per D34. Deliberately starts empty: no
// scope-to-role policy has been decided yet. An unmapped Scope value is not
// an error — it simply means authorizeDecisionScope has nothing additional
// to check for that item, and the generic RequiredRole gate already set on
// orchDecisionFlow's transitions is what enforces authority today.
var decisionScopeRoles = map[string]string{}

// authorizeDecisionScope checks whether actorID holds the role D34 maps from
// item's Scope field, via scopeRoles. It is layered alongside — not instead
// of — validateTransition's generic RequiredRole gate: RequiredRole is fixed
// per (from, to) transition row and shared by every Decision regardless of
// its own Scope, while Scope is a per-instance field a shared row can't
// express.
//
// Returns nil (no additional check) when item is not *Decision, rs is nil,
// actorID is empty, or item's Scope has no entry in scopeRoles — the same
// fail-open posture validateTransition itself uses for a non-strict
// transition, so this wrapper is not a stricter island with its own rules.
func authorizeDecisionScope(ctx context.Context, rs *RoleStore, actorID string, item any, scopeRoles map[string]string) error {
	d, ok := item.(*Decision)
	if !ok || rs == nil || actorID == "" {
		return nil
	}
	role, ok := scopeRoles[d.Scope]
	if !ok {
		return nil
	}
	granted, err := rs.RoleGranted(ctx, actorID, role, AuthTarget{})
	if err != nil {
		return fmt.Errorf("%w: scope %q requires role %q: %s", ErrForbidden, d.Scope, role, err)
	}
	if !granted {
		return fmt.Errorf("%w: scope %q requires role %q", ErrForbidden, d.Scope, role)
	}
	return nil
}

// Amendment is an orchestration content type representing a committed
// changeset that links a Task to its implementation in code.
type Amendment struct {
	Node
	// AmendmentNumber is the canonical identifier (e.g. "A183").
	AmendmentNumber string `json:"amendment_number" db:"amendment_number"`
	// AmendmentType classifies the change (e.g. "feat", "fix", "refactor").
	AmendmentType string `json:"amendment_type" db:"amendment_type"`
	// Version is the smeldr.dev/core semver shipped with this amendment.
	Version string `json:"version"`
	// CommitHash is the git SHA of the squash commit.
	CommitHash string `json:"commit_hash" db:"commit_hash"`
	// Pilot identifies the implementing agent (e.g. "corepilot", "sitepilot").
	Pilot string `json:"pilot"`
	// Summary is a one-line description of what the amendment changes.
	Summary string `json:"summary"`
}

// RunOutcome is the terminal state of a completed or abandoned [Run] (D38
// §3). Empty means the Run is still in-flight — every named value below is
// terminal, including RunNeedsResync, whose continuation is always a new
// Run branching from this one, never a resumption of this row.
type RunOutcome string

const (
	// RunMerged means the Run's branch was squash-merged successfully.
	RunMerged RunOutcome = "merged"
	// RunNeedsResync means the land-time freshness gate found main had
	// moved; a human resolves it, and continuation is always a new Run.
	RunNeedsResync RunOutcome = "needs-resync"
	// RunStuck means a step failed or timed out; the worktree is preserved
	// as crash-scene evidence for inspection.
	RunStuck RunOutcome = "stuck"
	// RunFailed means the Run was abandoned; the worktree is preserved.
	RunFailed RunOutcome = "failed"
	// RunOrphaned means an admin declared the lease expired and reclaimed
	// it; a new Run opens for the same Task.
	RunOrphaned RunOutcome = "orphaned"
)

// RunCleanupState is whether a terminal [Run]'s worktree/branch cleanup has
// completed (D38 §6/§7). A failed cleanup degrades to a disk-space problem,
// never a correctness one — names are never reused, so nothing else is ever
// blocked by a cleanup that hasn't finished yet.
type RunCleanupState string

const (
	// RunCleanupPending means cleanup has not yet completed (or has not
	// yet been attempted). The janitor pass retries it.
	RunCleanupPending RunCleanupState = "pending"
	// RunCleanupDone means the worktree and branch have been removed.
	RunCleanupDone RunCleanupState = "done"
)

// Run is an orchestration content type representing one mechanical episode
// of headless automated work (D38, M3): from the moment a listener claims
// it to the moment it merges or is abandoned. Unlike the other five
// orchestration types, Run registers no [StateFlow] — its authoritative
// state lives in LeaseHolder and Outcome, guarded by [SQLRepo.Save]'s
// rev-CAS, never in the embedded [Node].Status field via
// [validateTransition]. Node.Status is present (inherited, required by the
// generic content-module machinery) but carries no meaning for Run: no
// code path ever publishes a Run, so every row stays Draft for its entire
// life. This does not hide Run rows from a listener's own reads — MCPList
// applies no draft-visibility filtering of its own (it returns everything
// [Repo.FindAll] returns unless the caller explicitly requests a status
// subset).
//
// Every lease-touching write (claim, renewal, the land-time holder check,
// reclaim) MUST echo the Rev value it last read for this row — this is a
// contract the M3 listener (not built by this task) must uphold, not
// something Run or SQLRepo enforces on its own: a write whose payload
// omits rev has MCPUpdate silently seed the row's current value instead,
// satisfying the CAS by construction and degrading claims/renewals to
// last-write-wins, indistinguishably from correct behaviour under any
// single-threaded test (D38 §3).
type Run struct {
	Node
	// TaskID is the Task this Run performs mechanical work for (e.g. "T145").
	TaskID string `json:"task_id" db:"task_id"`
	// Repo is the repository this Run operates on (e.g. "smeldr/core").
	Repo string `json:"repo"`
	// Machine identifies which machine this Run's worktree/branch live on.
	Machine string `json:"machine"`
	// Branch is the git branch name — derived from this Run's own ID,
	// never a timestamp or a next-available scan, and never reused (D38 §6).
	Branch string `json:"branch"`
	// WorktreePath is the local filesystem path of this Run's git worktree.
	WorktreePath string `json:"worktree_path" db:"worktree_path"`
	// BaseSHA is the commit this Run's branch started from.
	BaseSHA string `json:"base_sha" db:"base_sha"`
	// LeaseHolder is the listener process ID that currently holds this
	// Run's lease. Written only by the lease holder itself or by a reclaim
	// action that terminals the Run (D38 §8) — never by anything else,
	// including the claude -p child process the listener spawns.
	LeaseHolder string `json:"lease_holder" db:"lease_holder"`
	// Outcome is empty while the Run is in-flight. No lease_expires_at
	// field exists — expiry is computed as Node.UpdatedAt plus a TTL
	// constant by whoever asks (D38 §8), not stored.
	Outcome RunOutcome `json:"outcome"`
	// Cleanup tracks this Run's worktree/branch cleanup progress.
	Cleanup RunCleanupState `json:"cleanup"`
	// AcknowledgedAt gates deletion of a preserved stuck/failed/orphaned
	// Run's worktree (D38 §7). The zero value means not yet acknowledged —
	// matching [Decision].NextEvalAt's own established nullable-timestamp
	// convention, not a *time.Time: the generic SQLRepo scan path
	// (scanDest, storage.go) only special-cases a scan destination whose
	// address is naturally *time.Time — which is what taking the address
	// of a plain time.Time field gives you. A *time.Time field's own
	// address is **time.Time, unhandled by that special case, and falls
	// through to database/sql's generic pointer-to-pointer path, which
	// cannot parse SQLite's string-formatted timestamps into the inner
	// *time.Time it allocates. Verified directly, not assumed: reproduced
	// this exact failure empirically against a real SQLRepo before
	// choosing this field's type. [Node].ScheduledAt is *time.Time and
	// is NOT a working counterexample — every existing test that round-
	// trips a non-nil ScheduledAt uses NewMemoryRepo, never NewSQLRepo;
	// a nil ScheduledAt round-trips fine (database/sql's nil-source case
	// needs no string parsing), but a non-nil one hits this exact failure
	// against a real SQLite-backed repo. This looks like a real,
	// previously-untested latent bug in Node.ScheduledAt, flagged to the
	// architect as its own follow-up — not fixed here, out of this
	// task's scope.
	AcknowledgedAt time.Time `json:"acknowledged_at" db:"acknowledged_at"`
}

// CreateOrchestrationTables creates the six orchestration content tables
// (smeldr_signals, smeldr_tasks, smeldr_decisions, smeldr_amendments,
// smeldr_goals, smeldr_runs) if they do not already exist. Call once at
// application startup before [RegisterOrchestrationTypes].
func CreateOrchestrationTables(db DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS smeldr_signals (
			id          TEXT PRIMARY KEY,
			slug        TEXT NOT NULL UNIQUE,
			status      TEXT NOT NULL DEFAULT 'draft',
			published_at TIMESTAMPTZ,
			scheduled_at TIMESTAMPTZ,
			created_at  TIMESTAMPTZ NOT NULL,
			updated_at  TIMESTAMPTZ NOT NULL,
			rev         INTEGER NOT NULL DEFAULT 0,
			sender      TEXT NOT NULL DEFAULT '',
			receiver    TEXT NOT NULL DEFAULT '',
			signal_type TEXT NOT NULL DEFAULT '',
			message     TEXT NOT NULL DEFAULT '',
			task_ref    TEXT NOT NULL DEFAULT '',
			sequence    INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS smeldr_tasks (
			id          TEXT PRIMARY KEY,
			slug        TEXT NOT NULL UNIQUE,
			status      TEXT NOT NULL DEFAULT 'draft',
			published_at TIMESTAMPTZ,
			scheduled_at TIMESTAMPTZ,
			created_at  TIMESTAMPTZ NOT NULL,
			updated_at  TIMESTAMPTZ NOT NULL,
			rev         INTEGER NOT NULL DEFAULT 0,
			task_id     TEXT NOT NULL DEFAULT '',
			priority    INTEGER NOT NULL DEFAULT 0,
			band        TEXT NOT NULL DEFAULT '',
			size        TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			note_ref    TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS smeldr_decisions (
			id              TEXT PRIMARY KEY,
			slug            TEXT NOT NULL UNIQUE,
			status          TEXT NOT NULL DEFAULT 'draft',
			published_at    TIMESTAMPTZ,
			scheduled_at    TIMESTAMPTZ,
			created_at      TIMESTAMPTZ NOT NULL,
			updated_at      TIMESTAMPTZ NOT NULL,
			rev             INTEGER NOT NULL DEFAULT 0,
			decision_number TEXT NOT NULL DEFAULT '',
			scope           TEXT NOT NULL DEFAULT '',
			body            TEXT NOT NULL DEFAULT '',
			next_eval_at    TIMESTAMPTZ,
			eval_note       TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS smeldr_amendments (
			id               TEXT PRIMARY KEY,
			slug             TEXT NOT NULL UNIQUE,
			status           TEXT NOT NULL DEFAULT 'draft',
			published_at     TIMESTAMPTZ,
			scheduled_at     TIMESTAMPTZ,
			created_at       TIMESTAMPTZ NOT NULL,
			updated_at       TIMESTAMPTZ NOT NULL,
			rev              INTEGER NOT NULL DEFAULT 0,
			amendment_number TEXT NOT NULL DEFAULT '',
			amendment_type   TEXT NOT NULL DEFAULT '',
			version          TEXT NOT NULL DEFAULT '',
			commit_hash      TEXT NOT NULL DEFAULT '',
			pilot            TEXT NOT NULL DEFAULT '',
			summary          TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS smeldr_goals (
			id           TEXT PRIMARY KEY,
			slug         TEXT NOT NULL UNIQUE,
			status       TEXT NOT NULL DEFAULT 'draft',
			published_at TIMESTAMPTZ,
			scheduled_at TIMESTAMPTZ,
			created_at   TIMESTAMPTZ NOT NULL,
			updated_at   TIMESTAMPTZ NOT NULL,
			rev          INTEGER NOT NULL DEFAULT 0,
			goal_id      TEXT NOT NULL DEFAULT '',
			priority     INTEGER NOT NULL DEFAULT 0,
			band         TEXT NOT NULL DEFAULT '',
			size         TEXT NOT NULL DEFAULT '',
			description  TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS smeldr_runs (
			id              TEXT PRIMARY KEY,
			slug            TEXT NOT NULL UNIQUE,
			status          TEXT NOT NULL DEFAULT 'draft',
			published_at    TIMESTAMPTZ,
			scheduled_at    TIMESTAMPTZ,
			created_at      TIMESTAMPTZ NOT NULL,
			updated_at      TIMESTAMPTZ NOT NULL,
			rev             INTEGER NOT NULL DEFAULT 0,
			task_id         TEXT NOT NULL DEFAULT '',
			repo            TEXT NOT NULL DEFAULT '',
			machine         TEXT NOT NULL DEFAULT '',
			branch          TEXT NOT NULL DEFAULT '',
			worktree_path   TEXT NOT NULL DEFAULT '',
			base_sha        TEXT NOT NULL DEFAULT '',
			lease_holder    TEXT NOT NULL DEFAULT '',
			outcome         TEXT NOT NULL DEFAULT '',
			cleanup         TEXT NOT NULL DEFAULT '',
			acknowledged_at TIMESTAMPTZ
		)`,
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(context.Background(), stmt); err != nil {
			return err
		}
	}
	return nil
}

// RegisterOrchestrationTypes registers the six orchestration content types
// ([Signal], [Task], [Decision], [Amendment], [Goal], [Run]) with the
// application. The first five also get their custom state flows; [Run]
// deliberately does not (D38 — see its own doc comment). Call after
// [CreateOrchestrationTables] and before [App.Run]. Flow registration
// errors are logged and do not block startup (fail-open).
func RegisterOrchestrationTypes(app *App, db DB) {
	flows := []StateFlow{
		orchSignalFlow(),
		orchTaskFlow(),
		orchDecisionFlow(),
		orchAmendmentFlow(),
		orchGoalFlow(),
	}
	for _, f := range flows {
		if err := app.RegisterFlow(f); err != nil {
			slog.Error("smeldr: RegisterOrchestrationTypes: RegisterFlow failed",
				"flow", f.Name, "error", err)
		}
	}
	app.Content(NewModule[*Signal]((*Signal)(nil),
		At("/signals"), Repo(NewSQLRepo[*Signal](db, Table("smeldr_signals"))), MCP(MCPRead, MCPWrite),
	))
	app.Content(NewModule[*Task]((*Task)(nil),
		At("/tasks"), Repo(NewSQLRepo[*Task](db, Table("smeldr_tasks"))), MCP(MCPRead, MCPWrite),
	))
	app.Content(NewModule[*Decision]((*Decision)(nil),
		At("/decisions"), Repo(NewSQLRepo[*Decision](db, Table("smeldr_decisions"))), MCP(MCPRead, MCPWrite),
	))
	app.Content(NewModule[*Amendment]((*Amendment)(nil),
		At("/amendments"), Repo(NewSQLRepo[*Amendment](db, Table("smeldr_amendments"))), MCP(MCPRead, MCPWrite),
	))
	app.Content(NewModule[*Goal]((*Goal)(nil),
		At("/goals"), Repo(NewSQLRepo[*Goal](db, Table("smeldr_goals"))), MCP(MCPRead, MCPWrite),
	))
	// Run gets no entry in the flows slice above — D38 requires no
	// StateFlow (see Run's own doc comment). MCP(MCPRead, MCPWrite) is not
	// just consistency with its five siblings: D38's claim/renewal/
	// land-time-holder-check writes all travel the MCP update path
	// (mcp/tool.go's handleToolsCall → MCPUpdate), so MCPWrite is a real
	// requirement for the future listener, not a style preference.
	app.Content(NewModule[*Run]((*Run)(nil),
		At("/runs"), Repo(NewSQLRepo[*Run](db, Table("smeldr_runs"))), MCP(MCPRead, MCPWrite),
	))
}

// RegisterOrchestrationRelationKinds registers the relation kinds that
// connect the orchestration types (Task, Goal, Decision, Amendment):
// derives_from (Task→Goal), depends_on (Task→Task), ships_as
// (Task→Amendment), and supersedes (Decision→Decision). Idempotent — safe
// to call on every boot; UpsertKind updates in place if a kind with the
// same type_name is already registered.
func RegisterOrchestrationRelationKinds(ctx context.Context, store *RelationStore) error {
	kinds := []RelationKindDef{
		{
			TypeName:    "derives_from",
			Label:       "Derives From",
			Mode:        "asserted",
			Directional: true,
			TypePairs:   json.RawMessage(`[{"source_type":"Task","target_type":"Goal"}]`),
		},
		{
			TypeName:    "depends_on",
			Label:       "Depends On",
			Mode:        "asserted",
			Directional: true,
			TypePairs:   json.RawMessage(`[{"source_type":"Task","target_type":"Task"}]`),
		},
		{
			TypeName:    "ships_as",
			Label:       "Ships As",
			Mode:        "asserted",
			Directional: true,
			TypePairs:   json.RawMessage(`[{"source_type":"Task","target_type":"Amendment"}]`),
		},
		{
			TypeName:    "supersedes",
			Label:       "Supersedes",
			Mode:        "asserted",
			Directional: true,
			TypePairs:   json.RawMessage(`[{"source_type":"Decision","target_type":"Decision"}]`),
		},
	}
	for _, k := range kinds {
		if err := store.UpsertKind(ctx, k); err != nil {
			return fmt.Errorf("register relation kind %q: %w", k.TypeName, err)
		}
	}
	return nil
}

// orchSignalFlow returns the state flow for [Signal] records.
// A signal starts as pending, is acknowledged or expires from any non-terminal state.
func orchSignalFlow() StateFlow {
	return StateFlow{
		Name:     "signal-protocol",
		TypeName: "Signal",
		States: []State{
			{Name: "pending", IsInitial: true},
			{Name: "read"},
			{Name: "acknowledged", IsTerminal: true},
			{Name: "expired", IsTerminal: true},
		},
		Transitions: []Transition{
			{From: "pending", To: "read"},
			{From: "read", To: "acknowledged"},
			{From: "pending", To: "expired"},
			{From: "read", To: "expired"},
		},
	}
}

// orchTaskFlow returns the state flow for [Task] records.
// Tasks progress from backlog through active work stages to done or deferred.
func orchTaskFlow() StateFlow {
	return StateFlow{
		Name:     "architect-task",
		TypeName: "Task",
		States: []State{
			{Name: "backlog", IsInitial: true},
			{Name: "active"},
			{Name: "waiting-plan"},
			{Name: "plan-reviewing"},
			{Name: "implementing"},
			{Name: "commit-reviewing"},
			{Name: "done", IsTerminal: true},
			{Name: "blocked"},
			{Name: "deferred", IsTerminal: true},
		},
		Transitions: []Transition{
			{From: "backlog", To: "active"},
			{From: "active", To: "waiting-plan"},
			{From: "waiting-plan", To: "plan-reviewing"},
			{From: "plan-reviewing", To: "implementing"},
			{From: "implementing", To: "commit-reviewing"},
			{From: "commit-reviewing", To: "done"},
			{From: "active", To: "blocked"},
			{From: "blocked", To: "active"},
			{From: "active", To: "deferred"},
		},
	}
}

// orchDecisionFlow returns the state flow for [Decision] records.
// Decisions are proposed, ratified, and periodically re-evaluated.
func orchDecisionFlow() StateFlow {
	return StateFlow{
		Name:     "governance-decision",
		TypeName: "Decision",
		States: []State{
			{Name: "proposed", IsInitial: true},
			{Name: "ratified"},
			{Name: "pending-re-evaluation"},
			{Name: "superseded", IsTerminal: true},
			{Name: "archived", IsTerminal: true},
		},
		Transitions: []Transition{
			// D34/D40: ratify and supersede require the "admin" role,
			// fail-closed (Strict) so a nil RoleStore or missing actor
			// rejects rather than silently allows — the highest of the
			// three built-in seeded roles, requiring no new provisioning
			// to start enforcing. D40 extends the same gate to the
			// re-evaluation door into the same two states: ratifying or
			// superseding is an authority-bearing act regardless of which
			// state it is entered from, and having been re-evaluated first
			// does not supply the authority the direct path requires.
			{From: "proposed", To: "ratified", RequiredRole: "admin", Strict: true},
			{From: "proposed", To: "archived"},
			{From: "ratified", To: "pending-re-evaluation"},
			{From: "pending-re-evaluation", To: "ratified", RequiredRole: "admin", Strict: true},
			{From: "pending-re-evaluation", To: "superseded", RequiredRole: "admin", Strict: true},
			{From: "ratified", To: "superseded", RequiredRole: "admin", Strict: true},
			{From: "superseded", To: "archived"},
		},
		Triggers: []TransitionTrigger{
			{
				FromState:    "proposed",
				ToState:      "ratified",
				TriggerClass: "async",
				TriggerType:  "schedule-eval",
				Config:       `{"eval_field":"next_eval_at","to_state":"pending-re-evaluation"}`,
			},
			{
				FromState:    "pending-re-evaluation",
				ToState:      "ratified",
				TriggerClass: "async",
				TriggerType:  "schedule-eval",
				Config:       `{"eval_field":"next_eval_at","to_state":"pending-re-evaluation"}`,
			},
		},
	}
}

// orchAmendmentFlow returns the state flow for [Amendment] records.
// Amendments move from scoped through implementation to merged or rejected.
func orchAmendmentFlow() StateFlow {
	return StateFlow{
		Name:     "amendment-lifecycle",
		TypeName: "Amendment",
		States: []State{
			{Name: "scoped", IsInitial: true},
			{Name: "in-progress"},
			{Name: "commit-ready"},
			{Name: "committed"},
			{Name: "merged", IsTerminal: true},
			{Name: "rejected", IsTerminal: true},
		},
		Transitions: []Transition{
			{From: "scoped", To: "in-progress"},
			{From: "in-progress", To: "commit-ready"},
			{From: "commit-ready", To: "committed"},
			{From: "committed", To: "merged"},
			{From: "in-progress", To: "rejected"},
			{From: "commit-ready", To: "rejected"},
		},
	}
}

// orchGoalFlow returns the state flow for [Goal] records.
// Goals move from open through active work to done or parked; parked goals
// can return to open when a concrete need materialises.
func orchGoalFlow() StateFlow {
	return StateFlow{
		Name:     "goal-lifecycle",
		TypeName: "Goal",
		States: []State{
			{Name: "open", IsInitial: true},
			{Name: "in-progress"},
			{Name: "done", IsTerminal: true},
			{Name: "parked", IsTerminal: true},
		},
		Transitions: []Transition{
			{From: "open", To: "in-progress"},
			{From: "in-progress", To: "done"},
			{From: "open", To: "parked"},
			{From: "in-progress", To: "parked"},
			{From: "parked", To: "open"},
		},
	}
}
