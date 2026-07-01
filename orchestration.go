// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"log/slog"
	"time"
)

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

// CreateOrchestrationTables creates the four orchestration content tables
// (smeldr_signals, smeldr_tasks, smeldr_decisions, smeldr_amendments) if
// they do not already exist. Call once at application startup before
// [RegisterOrchestrationTypes].
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
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(context.Background(), stmt); err != nil {
			return err
		}
	}
	return nil
}

// RegisterOrchestrationTypes registers the four orchestration content types
// ([Signal], [Task], [Decision], [Amendment]) with the application and their
// custom state flows. Call after [CreateOrchestrationTables] and before
// [App.Run]. Flow registration errors are logged and do not block startup
// (fail-open).
func RegisterOrchestrationTypes(app *App, db DB) {
	flows := []StateFlow{
		orchSignalFlow(),
		orchTaskFlow(),
		orchDecisionFlow(),
		orchAmendmentFlow(),
	}
	for _, f := range flows {
		if err := app.RegisterFlow(f); err != nil {
			slog.Error("smeldr: RegisterOrchestrationTypes: RegisterFlow failed",
				"flow", f.Name, "error", err)
		}
	}
	app.Content(NewModule[*Signal]((*Signal)(nil),
		At("/signals"), Repo(NewSQLRepo[*Signal](db)), MCP(MCPRead, MCPWrite),
	))
	app.Content(NewModule[*Task]((*Task)(nil),
		At("/tasks"), Repo(NewSQLRepo[*Task](db)), MCP(MCPRead, MCPWrite),
	))
	app.Content(NewModule[*Decision]((*Decision)(nil),
		At("/decisions"), Repo(NewSQLRepo[*Decision](db)), MCP(MCPRead, MCPWrite),
	))
	app.Content(NewModule[*Amendment]((*Amendment)(nil),
		At("/amendments"), Repo(NewSQLRepo[*Amendment](db)), MCP(MCPRead, MCPWrite),
	))
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
			{From: "proposed", To: "ratified"},
			{From: "proposed", To: "archived"},
			{From: "ratified", To: "pending-re-evaluation"},
			{From: "pending-re-evaluation", To: "ratified"},
			{From: "pending-re-evaluation", To: "superseded"},
			{From: "ratified", To: "superseded"},
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
