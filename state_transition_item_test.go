package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

const transitionItemTestSecret = "test-secret-16x!"

// setupTransitionItemApp builds a real App with governance and all six
// compiled orchestration types wired against an in-memory SQLite DB —
// the exact combination T224/T225's own investigation exercised.
func setupTransitionItemApp(t *testing.T) (*App, *sql.DB, *RoleStore) {
	t.Helper()
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte(transitionItemTestSecret),
		DB:      db,
	}))
	rs := NewRoleStore(db)
	if err := app.Governance(rs); err != nil {
		t.Fatalf("Governance: %v", err)
	}
	RegisterOrchestrationTypes(app, db)
	return app, db, rs
}

// insertSignal inserts a minimal smeldr_signals row directly, bypassing
// MCPCreate — TransitionItem's own contract only needs id/slug/status to
// exist, not a fully-populated Signal.
func insertSignal(t *testing.T, db *sql.DB, id, slug, status string) {
	t.Helper()
	repo := NewSQLRepo[*Signal](db, Table("smeldr_signals"))
	sig := &Signal{Node: Node{ID: id, Slug: slug, Status: Status(status)}}
	if err := repo.Save(context.Background(), sig); err != nil {
		t.Fatalf("insertSignal: %v", err)
	}
}

// insertTaskWithTaskID inserts a minimal smeldr_tasks row directly, with a
// realistic flow-driven status (not the CMS "published" constant) and its
// own human-facing TaskID, for T253's human-ID resolution tests.
func insertTaskWithTaskID(t *testing.T, db *sql.DB, id, slug, taskID, status string) {
	t.Helper()
	repo := NewSQLRepo[*Task](db, Table("smeldr_tasks"))
	tk := &Task{Node: Node{ID: id, Slug: slug, Status: Status(status)}, TaskID: taskID}
	if err := repo.Save(context.Background(), tk); err != nil {
		t.Fatalf("insertTaskWithTaskID: %v", err)
	}
}

// — happy paths ——————————————————————————————————————————————————————————

func TestApp_TransitionItem_Compiled_HappyPath(t *testing.T) {
	app, db, _ := setupTransitionItemApp(t)
	insertSignal(t, db, "sig-1", "sig-1-slug", "pending")

	result, err := app.TransitionItem(context.Background(), "Signal", "sig-1-slug", "read")
	if err != nil {
		t.Fatalf("TransitionItem: %v", err)
	}
	if result["status"] != "read" {
		t.Errorf("result status = %v, want \"read\"", result["status"])
	}

	var status string
	if err := db.QueryRowContext(context.Background(),
		"SELECT status FROM smeldr_signals WHERE id = ?", "sig-1",
	).Scan(&status); err != nil {
		t.Fatalf("verify status: %v", err)
	}
	if status != "read" {
		t.Errorf("stored status = %q, want \"read\"", status)
	}
}

// TestApp_TransitionItem_Compiled_ByHumanID exercises the reported bug
// directly: a caller resolves a Task by its own human-facing TaskID
// ("T203"), not its slug — this is the exact "transition_item" symptom
// T253 reports. Also confirms the response's own "slug" field is the real
// slug, not the human-ID ident echoed back (the same class of bug caught
// and fixed in Module.MCPUpdate for the MCP dispatch path).
func TestApp_TransitionItem_Compiled_ByHumanID(t *testing.T) {
	app, db, _ := setupTransitionItemApp(t)
	insertTaskWithTaskID(t, db, "task-1", "some-task-slug", "T203", "backlog")

	result, err := app.TransitionItem(context.Background(), "Task", "T203", "active")
	if err != nil {
		t.Fatalf("TransitionItem by human ID: %v", err)
	}
	if result["status"] != "active" {
		t.Errorf("result status = %v, want \"active\"", result["status"])
	}
	if result["slug"] != "some-task-slug" {
		t.Errorf("result slug = %v, want %q (must not echo back the human-ID ident)", result["slug"], "some-task-slug")
	}

	var status string
	if err := db.QueryRowContext(context.Background(),
		"SELECT status FROM smeldr_tasks WHERE id = ?", "task-1",
	).Scan(&status); err != nil {
		t.Fatalf("verify status: %v", err)
	}
	if status != "active" {
		t.Errorf("stored status = %q, want \"active\"", status)
	}
}

// TestApp_TransitionItem_Compiled_ByHumanID_StillResolvesBySlug confirms
// the ordinary slug path is unaffected — the fallback only ever fires on a
// slug miss.
func TestApp_TransitionItem_Compiled_ByHumanID_StillResolvesBySlug(t *testing.T) {
	app, db, _ := setupTransitionItemApp(t)
	insertTaskWithTaskID(t, db, "task-2", "some-task-slug", "T204", "backlog")

	result, err := app.TransitionItem(context.Background(), "Task", "some-task-slug", "active")
	if err != nil {
		t.Fatalf("TransitionItem by slug: %v", err)
	}
	if result["slug"] != "some-task-slug" {
		t.Errorf("result slug = %v, want %q", result["slug"], "some-task-slug")
	}
}

func TestApp_TransitionItem_Dynamic_HappyPath(t *testing.T) {
	app, db, _ := setupTransitionItemApp(t)
	if err := CreateBlockTables(db); err != nil {
		t.Fatalf("CreateBlockTables: %v", err)
	}
	if err := CreateSchemaTable(db); err != nil {
		t.Fatalf("CreateSchemaTable: %v", err)
	}
	fields, err := json.Marshal([]SchemaField{
		{Name: "Title", Type: "string", Required: true, Role: "title"},
	})
	if err != nil {
		t.Fatalf("marshal fields: %v", err)
	}
	schema := &ContentTypeSchema{TypeName: "recipe", Fields: fields}
	desc, err := app.DefineContentType(context.Background(), schema)
	if err != nil {
		t.Fatalf("DefineContentType: %v", err)
	}
	repo, err := app.DynamicContentRepo(desc.Name)
	if err != nil {
		t.Fatalf("DynamicContentRepo: %v", err)
	}
	node, err := repo.CreateDraft(context.Background(), map[string]any{"Title": "Pasta"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}

	result, err := app.TransitionItem(context.Background(), desc.Name, node.Slug, "published")
	if err != nil {
		t.Fatalf("TransitionItem: %v", err)
	}
	if result["status"] != "published" {
		t.Errorf("result status = %v, want \"published\"", result["status"])
	}
	got, err := repo.GetBySlug(context.Background(), node.Slug)
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if got.Status != Published {
		t.Errorf("stored status = %q, want Published", got.Status)
	}
}

// — authority parity ————————————————————————————————————————————————————

func TestApp_TransitionItem_Compiled_RoleGated_Forbidden(t *testing.T) {
	app, db, _ := setupTransitionItemApp(t)
	insertDecision(t, db, "dec-1", "dec-1-slug", "proposed")

	_, err := app.TransitionItem(NewTestContext(User{ID: "author-1"}), "Decision", "dec-1-slug", "ratified")
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("expected ErrForbidden for unauthorized actor, got %v", err)
	}
}

func TestApp_TransitionItem_Compiled_RoleGated_Granted(t *testing.T) {
	app, db, rs := setupTransitionItemApp(t)
	insertDecision(t, db, "dec-2", "dec-2-slug", "proposed")
	tokenID := setupTokenWithRole(t, db, rs, "admin")

	result, err := app.TransitionItem(NewTestContext(User{ID: tokenID}), "Decision", "dec-2-slug", "ratified")
	if err != nil {
		t.Fatalf("TransitionItem: %v", err)
	}
	if result["status"] != "ratified" {
		t.Errorf("result status = %v, want \"ratified\"", result["status"])
	}
}

func insertDecision(t *testing.T, db *sql.DB, id, slug, status string) {
	t.Helper()
	repo := NewSQLRepo[*Decision](db, Table("smeldr_decisions"))
	dec := &Decision{Node: Node{ID: id, Slug: slug, Status: Status(status)}}
	if err := repo.Save(context.Background(), dec); err != nil {
		t.Fatalf("insertDecision: %v", err)
	}
}

// — error paths ——————————————————————————————————————————————————————————

func TestApp_TransitionItem_UnregisteredType(t *testing.T) {
	app, _, _ := setupTransitionItemApp(t)
	_, err := app.TransitionItem(context.Background(), "NoSuchType", "slug", "read")
	if err == nil {
		t.Fatal("expected error for unregistered type")
	}
}

func TestApp_TransitionItem_NotFound(t *testing.T) {
	app, _, _ := setupTransitionItemApp(t)
	_, err := app.TransitionItem(context.Background(), "Signal", "no-such-slug", "read")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestApp_TransitionItem_Dynamic_NotFound(t *testing.T) {
	app, db, _ := setupTransitionItemApp(t)
	if err := CreateBlockTables(db); err != nil {
		t.Fatalf("CreateBlockTables: %v", err)
	}
	if err := CreateSchemaTable(db); err != nil {
		t.Fatalf("CreateSchemaTable: %v", err)
	}
	fields, err := json.Marshal([]SchemaField{
		{Name: "Title", Type: "string", Required: true, Role: "title"},
	})
	if err != nil {
		t.Fatalf("marshal fields: %v", err)
	}
	schema := &ContentTypeSchema{TypeName: "recipe", Fields: fields}
	desc, err := app.DefineContentType(context.Background(), schema)
	if err != nil {
		t.Fatalf("DefineContentType: %v", err)
	}
	_, err = app.TransitionItem(context.Background(), desc.Name, "no-such-slug", "published")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for dynamic-content branch, got %v", err)
	}
}

func TestApp_TransitionItem_SelectQueryFails(t *testing.T) {
	app, db, _ := setupTransitionItemApp(t)
	insertSignal(t, db, "sig-select-fail", "sig-select-fail-slug", "pending")
	app.cfg.DB = &govQueryRowFailDB{DB: db, failOn: "id, slug, status FROM"}
	_, err := app.TransitionItem(context.Background(), "Signal", "sig-select-fail-slug", "read")
	if !errors.Is(err, ErrInternal) {
		t.Errorf("expected ErrInternal on the item lookup query failing, got %v", err)
	}
}

func TestApp_TransitionItem_InvalidTargetState(t *testing.T) {
	app, db, _ := setupTransitionItemApp(t)
	insertSignal(t, db, "sig-3", "sig-3-slug", "pending")
	_, err := app.TransitionItem(context.Background(), "Signal", "sig-3-slug", "not-a-real-state")
	if !errors.Is(err, ErrConflict) {
		t.Errorf("expected ErrConflict for invalid target state, got %v", err)
	}
}

func TestApp_TransitionItem_TransitionNotPermitted(t *testing.T) {
	app, db, _ := setupTransitionItemApp(t)
	insertSignal(t, db, "sig-4", "sig-4-slug", "acknowledged") // terminal state
	_, err := app.TransitionItem(context.Background(), "Signal", "sig-4-slug", "read")
	if !errors.Is(err, ErrConflict) {
		t.Errorf("expected ErrConflict for a transition not in the flow, got %v", err)
	}
}

func TestApp_TransitionItem_DBNil(t *testing.T) {
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte(transitionItemTestSecret),
	}))
	app.typeRegistry.Register(&TypeDescriptor{Name: "Ghost", Kind: "compiled"})
	_, err := app.TransitionItem(context.Background(), "Ghost", "slug", "state")
	if err == nil {
		t.Fatal("expected error when Config.DB is nil")
	}
}

// TestApp_TransitionItem_CompiledTypeTableMissing proves D47's own guard —
// a type the registry says is compiled must never silently fall into the
// dynamic-content branch just because resolveItemTable finds no dedicated
// table for it (partial migration, an unregistered module).
func TestApp_TransitionItem_CompiledTypeTableMissing(t *testing.T) {
	app, _, _ := setupTransitionItemApp(t)
	app.typeRegistry.Register(&TypeDescriptor{Name: "Ghost", Kind: "compiled"})
	_, err := app.TransitionItem(context.Background(), "Ghost", "slug", "state")
	if err == nil {
		t.Fatal("expected error for a compiled type with no backing table")
	}
}

func TestApp_TransitionItem_TransitionRowQueryFails(t *testing.T) {
	app, db, _ := setupTransitionItemApp(t)
	insertSignal(t, db, "sig-5", "sig-5-slug", "pending")
	app.cfg.DB = &govQueryRowFailDB{DB: db, failOn: "FROM smeldr_transitions"}
	_, err := app.TransitionItem(context.Background(), "Signal", "sig-5-slug", "read")
	if !errors.Is(err, ErrInternal) {
		t.Errorf("expected ErrInternal (D34 fail-closed), got %v", err)
	}
}

func TestApp_TransitionItem_UpdateExecFails(t *testing.T) {
	app, db, _ := setupTransitionItemApp(t)
	insertSignal(t, db, "sig-6", "sig-6-slug", "pending")
	app.cfg.DB = &conflictExecFailDB{DB: db}
	_, err := app.TransitionItem(context.Background(), "Signal", "sig-6-slug", "read")
	if !errors.Is(err, ErrInternal) {
		t.Errorf("expected ErrInternal on UPDATE failure, got %v", err)
	}
}

func TestApp_TransitionItem_ConflictPolicyViolated(t *testing.T) {
	app, db, _ := setupTransitionItemApp(t)
	registerConflictFlow(t, db, ConflictReject)
	if _, err := db.ExecContext(context.Background(),
		"ALTER TABLE conflict_types ADD COLUMN slug TEXT NOT NULL DEFAULT ''",
	); err != nil {
		t.Fatalf("alter conflict_types: %v", err)
	}
	insertConflictItem(t, db, "ct-existing", "published")
	insertConflictItem(t, db, "ct-new", "draft")
	if _, err := db.ExecContext(context.Background(),
		"UPDATE conflict_types SET slug = id",
	); err != nil {
		t.Fatalf("backfill slug: %v", err)
	}
	app.typeRegistry.Register(&TypeDescriptor{Name: "ConflictType", Kind: "compiled"})

	_, err := app.TransitionItem(context.Background(), "ConflictType", "ct-new", "published")
	if !errors.Is(err, ErrConflict) {
		t.Errorf("expected ErrConflict from ConflictReject policy, got %v", err)
	}
}

// — TransitionItemWithReason (T235) ————————————————————————————————————————

// registerReasonGatedFlow creates a fresh compiled type with a single
// RequiredReason-gated transition (draft -> published), mirroring
// registerConflictFlow's own shape.
func registerReasonGatedFlow(t *testing.T, app *App, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS reason_gated_types (id TEXT PRIMARY KEY, slug TEXT NOT NULL, status TEXT NOT NULL, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
	); err != nil {
		t.Fatalf("create reason_gated_types: %v", err)
	}
	if err := app.RegisterFlow(StateFlow{
		Name:     "reason-gated-flow",
		TypeName: "ReasonGatedType",
		States: []State{
			{Name: "draft", IsInitial: true},
			{Name: "published"},
		},
		Transitions: []Transition{
			{From: "draft", To: "published", RequiredReason: true},
		},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	app.typeRegistry.Register(&TypeDescriptor{Name: "ReasonGatedType", Kind: "compiled"})
	if _, err := db.ExecContext(ctx,
		`INSERT INTO reason_gated_types (id, slug, status) VALUES ('rg-1', 'rg-1-slug', 'draft')`,
	); err != nil {
		t.Fatalf("insert reason_gated_types row: %v", err)
	}
}

func TestApp_TransitionItemWithReason_Compiled_RequiredReason(t *testing.T) {
	app, db, _ := setupTransitionItemApp(t)
	registerReasonGatedFlow(t, app, db)

	// TransitionItem (empty reason via the wrapper) must still fail — proves
	// the wrapper delegates with "" rather than silently satisfying the gate.
	if _, err := app.TransitionItem(context.Background(), "ReasonGatedType", "rg-1-slug", "published"); !errors.Is(err, ErrBadRequest) {
		t.Errorf("TransitionItem (no reason): got %v, want ErrBadRequest", err)
	}

	// TransitionItemWithReason with a real reason succeeds.
	result, err := app.TransitionItemWithReason(context.Background(), "ReasonGatedType", "rg-1-slug", "published", "because the plan says so")
	if err != nil {
		t.Fatalf("TransitionItemWithReason: %v", err)
	}
	if result["status"] != "published" {
		t.Errorf("result status = %v, want \"published\"", result["status"])
	}
}

func TestApp_TransitionItemWithReason_Dynamic_RequiredReason(t *testing.T) {
	app, db, _ := setupTransitionItemApp(t)
	if err := CreateBlockTables(db); err != nil {
		t.Fatalf("CreateBlockTables: %v", err)
	}
	if err := CreateSchemaTable(db); err != nil {
		t.Fatalf("CreateSchemaTable: %v", err)
	}
	fields, err := json.Marshal([]SchemaField{
		{Name: "Title", Type: "string", Required: true, Role: "title"},
	})
	if err != nil {
		t.Fatalf("marshal fields: %v", err)
	}
	schema := &ContentTypeSchema{TypeName: "reasongated", Fields: fields}
	desc, err := app.DefineContentType(context.Background(), schema)
	if err != nil {
		t.Fatalf("DefineContentType: %v", err)
	}
	if err := app.RegisterFlow(StateFlow{
		Name:     "dynamic-reason-gated-flow",
		TypeName: "reasongated",
		States: []State{
			{Name: "draft", IsInitial: true},
			{Name: "published"},
		},
		Transitions: []Transition{
			{From: "draft", To: "published", RequiredReason: true},
		},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	repo, err := app.DynamicContentRepo(desc.Name)
	if err != nil {
		t.Fatalf("DynamicContentRepo: %v", err)
	}
	node, err := repo.CreateDraft(context.Background(), map[string]any{"Title": "Reason gated"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}

	if _, err := app.TransitionItem(context.Background(), desc.Name, node.Slug, "published"); !errors.Is(err, ErrBadRequest) {
		t.Errorf("TransitionItem (no reason): got %v, want ErrBadRequest", err)
	}

	result, err := app.TransitionItemWithReason(context.Background(), desc.Name, node.Slug, "published", "because the plan says so")
	if err != nil {
		t.Fatalf("TransitionItemWithReason: %v", err)
	}
	if result["status"] != "published" {
		t.Errorf("result status = %v, want \"published\"", result["status"])
	}
}

// — webhook event coverage for orchestration transitions (T231) ————————————

// wireWebhooksForTest creates the three webhook-delivery tables and calls
// app.Webhooks(store) — TransitionItemWithReason's dispatchTransitionWebhook
// call reads a.webhookStore/a.webhookPool, both nil until this runs.
func wireWebhooksForTest(t *testing.T, app *App, db *sql.DB) *WebhookStore {
	t.Helper()
	ctx := context.Background()
	createWebhookEndpointsTable(t, db)
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE smeldr_outbound_jobs (
			id TEXT PRIMARY KEY, endpoint_id TEXT NOT NULL, target_url TEXT NOT NULL,
			secret_enc TEXT NOT NULL, payload BLOB NOT NULL, event TEXT NOT NULL,
			attempts INTEGER NOT NULL DEFAULT 0, next_retry_at TIMESTAMPTZ NOT NULL,
			created_at TIMESTAMPTZ NOT NULL, expires_at TIMESTAMPTZ NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending'
		)`); err != nil {
		t.Fatalf("create smeldr_outbound_jobs: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE smeldr_delivery_logs (
			id TEXT PRIMARY KEY, job_id TEXT NOT NULL, attempted_at TIMESTAMPTZ NOT NULL,
			status_code INTEGER NOT NULL DEFAULT 0, duration_ms INTEGER NOT NULL DEFAULT 0,
			error TEXT NOT NULL DEFAULT ''
		)`); err != nil {
		t.Fatalf("create smeldr_delivery_logs: %v", err)
	}
	store := NewWebhookStore(db, []byte(transitionItemTestSecret))
	app.Webhooks(store)
	return store
}

// TestApp_TransitionItemWithReason_FiresTransitionWebhook confirms the real
// entry point (App.TransitionItemWithReason, not dispatchTransitionWebhook
// directly) enqueues a "signal.transitioned" job when a subscribed endpoint
// exists — the end-to-end path T231/A263 adds.
func TestApp_TransitionItemWithReason_FiresTransitionWebhook(t *testing.T) {
	app, db, _ := setupTransitionItemApp(t)
	insertSignal(t, db, "sig-2", "sig-2-slug", "pending")
	store := wireWebhooksForTest(t, app, db)
	ctx := context.Background()

	_, _, err := store.Create(ctx, "https://8.8.8.8/hook", []string{"signal.transitioned"})
	if err != nil {
		t.Fatalf("Create webhook endpoint: %v", err)
	}

	result, err := app.TransitionItemWithReason(ctx, "Signal", "sig-2-slug", "read", "because")
	if err != nil {
		t.Fatalf("TransitionItemWithReason: %v", err)
	}
	if result["status"] != "read" {
		t.Fatalf("result status = %v, want \"read\"", result["status"])
	}

	endpoints, err := store.EndpointsForEvent(ctx, "signal.transitioned")
	if err != nil {
		t.Fatalf("EndpointsForEvent: %v", err)
	}
	if len(endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(endpoints))
	}
	jobs, err := app.WebhookPool().ListJobsForEndpoint(ctx, endpoints[0].ID)
	if err != nil {
		t.Fatalf("ListJobsForEndpoint: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 enqueued job, got %d", len(jobs))
	}
	var payload WebhookEventPayload
	if err := json.Unmarshal(jobs[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	var data transitionWebhookData
	if err := json.Unmarshal(payload.Data, &data); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	if data.Type != "signal" || data.ID != "sig-2" || data.Slug != "sig-2-slug" ||
		data.FromState != "pending" || data.ToState != "read" || data.Reason != "because" {
		t.Errorf("data = %+v, unexpected", data)
	}
}

// TestApp_TransitionItemWithReason_NoWebhookStore confirms the transition
// still succeeds, with no panic, when App.Webhooks was never called —
// dispatchTransitionWebhook's nil-safety exercised through the real entry
// point rather than only unit-tested directly.
func TestApp_TransitionItemWithReason_NoWebhookStore(t *testing.T) {
	app, db, _ := setupTransitionItemApp(t)
	insertSignal(t, db, "sig-3", "sig-3-slug", "pending")

	result, err := app.TransitionItemWithReason(context.Background(), "Signal", "sig-3-slug", "read", "")
	if err != nil {
		t.Fatalf("TransitionItemWithReason: %v", err)
	}
	if result["status"] != "read" {
		t.Errorf("result status = %v, want \"read\"", result["status"])
	}
}

// TestDrainEvalQueue_AuthorizationRequiredSignal_FiresWebhook confirms the
// D42-class Signal recorded by recordAuthorizationRequiredSignal (via
// DrainEvalQueue's role-gated branch) fires the same "signal.created" event
// a human-created Signal already produces — T231/A263's second delivery
// path, exercised end-to-end through DrainEvalQueue rather than calling
// recordAuthorizationRequiredSignal directly.
func TestDrainEvalQueue_AuthorizationRequiredSignal_FiresWebhook(t *testing.T) {
	app, db, _ := setupTransitionItemApp(t)
	store := wireWebhooksForTest(t, app, db)
	ctx := context.Background()

	if err := app.RegisterFlow(StateFlow{
		Name:     "gate-item-flow",
		TypeName: "GateItem",
		States:   []State{{Name: "reviewing", IsInitial: true}, {Name: "approved"}},
		Transitions: []Transition{
			{From: "reviewing", To: "approved", RequiredRole: "reviewer"},
		},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE gate_items (
			id TEXT PRIMARY KEY, slug TEXT NOT NULL, status TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL
		)`); err != nil {
		t.Fatalf("create gate_items: %v", err)
	}
	now := time.Now().UTC()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO gate_items (id, slug, status, created_at, updated_at) VALUES ($1,$2,$3,$4,$5)`,
		"gi-1", "gi-1-slug", "reviewing", now, now,
	); err != nil {
		t.Fatalf("insert gate_items row: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_eval_queue (id, type_name, item_id, to_state, eval_at) VALUES ($1,$2,$3,$4,$5)`,
		"eq-1", "GateItem", "gi-1", "approved", now,
	); err != nil {
		t.Fatalf("insert smeldr_eval_queue row: %v", err)
	}

	if _, _, err := store.Create(ctx, "https://8.8.8.8/hook", []string{"signal.created"}); err != nil {
		t.Fatalf("Create webhook endpoint: %v", err)
	}

	triggered, skipped, err := app.DrainEvalQueue(ctx)
	if err != nil {
		t.Fatalf("DrainEvalQueue: %v", err)
	}
	if triggered != 0 || skipped != 1 {
		t.Fatalf("triggered=%d skipped=%d, want 0,1 (role-gated)", triggered, skipped)
	}

	endpoints, err := store.EndpointsForEvent(ctx, "signal.created")
	if err != nil {
		t.Fatalf("EndpointsForEvent: %v", err)
	}
	jobs, err := app.WebhookPool().ListJobsForEndpoint(ctx, endpoints[0].ID)
	if err != nil {
		t.Fatalf("ListJobsForEndpoint: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 enqueued job, got %d", len(jobs))
	}
	if jobs[0].Event != "signal.created" {
		t.Errorf("Event = %q, want %q", jobs[0].Event, "signal.created")
	}
}
