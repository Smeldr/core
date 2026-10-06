//go:build integration

package pgx

import (
	"context"
	"errors"
	"testing"

	smeldr "smeldr.dev/core"
)

// TestPG_OrchestrationFlows: the production flows (Task, Decision) on Postgres, the
// way process.smeldr.dev runs them: a transition the flow does not list is refused,
// a RequiredReason door needs its reason, and a strict, role-gated door refuses an
// actor without the role. Also runs MigrateStanding on Postgres, twice.
func TestPG_OrchestrationFlows(t *testing.T) {
	db, _ := isolatedDB(t)
	app, store := newPGApp(t, db)
	ctx := context.Background()
	if err := smeldr.CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	smeldr.RegisterOrchestrationTypes(app, db)

	tasks := smeldr.NewSQLRepo[*smeldr.Task](db, smeldr.Table("smeldr_tasks"))
	if err := tasks.Save(ctx, &smeldr.Task{Node: smeldr.Node{ID: "t1", Slug: "t1", Status: "backlog"}}); err != nil {
		t.Fatalf("save task: %v", err)
	}
	if _, err := app.TransitionItem(ctx, "Task", "t1", "implementing"); !errors.Is(err, smeldr.ErrConflict) {
		t.Errorf("backlog -> implementing is not a door: err = %v, want ErrConflict", err)
	}
	if _, err := app.TransitionItem(ctx, "Task", "t1", "active"); err != nil {
		t.Fatalf("backlog -> active: %v", err)
	}
	if _, err := app.TransitionItem(ctx, "Task", "t1", "done"); !errors.Is(err, smeldr.ErrBadRequest) {
		t.Errorf("active -> done without a reason: err = %v, want ErrBadRequest", err)
	}
	if _, err := app.TransitionItemWithReason(ctx, "Task", "t1", "done", "concluded without a commit"); err != nil {
		t.Fatalf("active -> done with a reason: %v", err)
	}

	decisions := smeldr.NewSQLRepo[*smeldr.Decision](db, smeldr.Table("smeldr_decisions"))
	if err := decisions.Save(ctx, &smeldr.Decision{Node: smeldr.Node{ID: "d1", Slug: "d1", Status: "proposed"}}); err != nil {
		t.Fatalf("save decision: %v", err)
	}
	if _, err := app.TransitionItem(smeldr.NewTestContext(smeldr.User{ID: "nobody"}), "Decision", "d1", "ratified"); !errors.Is(err, smeldr.ErrForbidden) {
		t.Errorf("ratifying without the operation: err = %v, want ErrForbidden", err)
	}
	if got := statusOf(t, db, "smeldr_decisions", "d1"); got != "proposed" {
		t.Errorf("a forbidden ratification changed the status to %q", got)
	}
	tokenID := "pg-token-" + smeldr.NewID()
	if err := smeldr.RegisterDecisionStewardRole(ctx, store); err != nil {
		t.Fatalf("RegisterDecisionStewardRole: %v", err)
	}
	if err := store.DefineRole(ctx, smeldr.RoleDefinition{Name: "pg-admin", Operations: []string{"approve"}, ScopeMode: smeldr.ScopeGlobal}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	for _, role := range []string{"pg-admin", "decision-steward"} {
		if _, err := store.Grant(ctx, smeldr.RoleGrant{TokenID: tokenID, RoleName: role}); err != nil {
			t.Fatalf("Grant %s: %v", role, err)
		}
	}
	if _, err := app.TransitionItem(smeldr.NewTestContext(smeldr.User{ID: tokenID}), "Decision", "d1", "ratified"); err != nil {
		t.Fatalf("ratifying with the operation: %v", err)
	}

	// A ratified Decision is Locked: its content may not change. Before D103 this
	// was accepted on Postgres, because Locked locked nothing there.
	m := smeldr.NewModule(&smeldr.Decision{},
		smeldr.Repo(decisions), smeldr.At("/decisions"),
		smeldr.Auth(smeldr.Read(smeldr.Guest), smeldr.Write(smeldr.Guest), smeldr.Delete(smeldr.Guest)))
	// A second App on the same database owns the module: RegisterOrchestrationTypes
	// already mounted its own Decision routes on the first.
	smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte(pgTestSecret), DB: db}).Content(m)
	if _, err := m.MCPUpdate(smeldr.NewTestContext(smeldr.User{ID: tokenID}), "d1", map[string]any{"title": "tampered"}); !errors.Is(err, smeldr.ErrConflict) {
		t.Errorf("editing a ratified (Locked) Decision: err = %v, want ErrConflict", err)
	}

	for i := 0; i < 2; i++ {
		if err := smeldr.MigrateStanding(ctx, db); err != nil {
			t.Fatalf("MigrateStanding (call %d): %v", i+1, err)
		}
	}
	var marked int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_standing_migrations`).Scan(&marked); err != nil {
		t.Fatalf("read markers: %v", err)
	}
	if marked == 0 {
		t.Error("MigrateStanding marked no type on Postgres")
	}
}

// TestPG_LastActorColumnMissing_FailsOpen: a compiled type's table that predates the
// last_actor column still transitions; the missing-column error is the Postgres
// spelling, which isNoSuchColumn has to recognise.
func TestPG_LastActorColumnMissing_FailsOpen(t *testing.T) {
	db, _ := isolatedDB(t)
	app, _ := newPGApp(t, db)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE pg_olds (id TEXT PRIMARY KEY, slug TEXT NOT NULL, status TEXT NOT NULL, updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		t.Fatalf("create: %v", err)
	}
	registerType(app, "PgOld")
	if err := app.RegisterFlow(smeldr.StateFlow{
		Name: "pg-old", TypeName: "PgOld",
		States:      []smeldr.State{{Name: "draft", IsInitial: true}, {Name: "review"}},
		Transitions: []smeldr.Transition{{From: "draft", To: "review"}},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	insertItem(t, db, "pg_olds", "a", "draft")
	if _, err := app.TransitionItem(ctx, "PgOld", "a", "review"); err != nil {
		t.Fatalf("a table without last_actor must still transition: %v", err)
	}
	if got := statusOf(t, db, "pg_olds", "a"); got != "review" {
		t.Errorf("status = %q, want review", got)
	}
}

// TestPG_Boot_RenamesLegacyTables: the forge_* tables of an older install are renamed
// at boot on Postgres, as they always were on SQLite.
func TestPG_Boot_RenamesLegacyTables(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	for _, name := range []string{"forge_audit_log", "forge_nav"} {
		if _, err := db.ExecContext(ctx, `CREATE TABLE `+name+` (id TEXT PRIMARY KEY)`); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	newPGApp(t, db)
	for _, name := range []string{"smeldr_audit_log", "smeldr_nav"} {
		if _, err := db.ExecContext(ctx, `SELECT 1 FROM `+name+` WHERE 1=0`); err != nil {
			t.Errorf("%s was not created by the rename: %v", name, err)
		}
	}
	for _, name := range []string{"forge_audit_log", "forge_nav"} {
		if _, err := db.ExecContext(ctx, `SELECT 1 FROM `+name+` WHERE 1=0`); err == nil {
			t.Errorf("%s still exists after the rename", name)
		}
	}
}
