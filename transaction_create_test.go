package smeldr

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// txItem is a compiled content type stored in tx_items, for the create
// transaction tests.
type txItem struct {
	Node
	Title string `smeldr:"required" db:"title"`
}

func createTxItemsTable(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, q := range []string{
		`CREATE TABLE tx_items (id TEXT PRIMARY KEY, slug TEXT NOT NULL, status TEXT NOT NULL,
			published_at DATETIME, scheduled_at DATETIME, created_at DATETIME, updated_at DATETIME,
			rev INTEGER NOT NULL DEFAULT 0, last_actor TEXT NOT NULL DEFAULT '', title TEXT NOT NULL)`,
		`CREATE TABLE tx_side (item_id TEXT NOT NULL)`,
		// A title of "fail" makes the INSERT itself fail.
		`CREATE TRIGGER tx_items_refuse BEFORE INSERT ON tx_items WHEN NEW.title = 'fail'
			BEGIN SELECT RAISE(ABORT, 'refused by trigger'); END`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

// txHarness is an App with one SQL-backed compiled module whose own pre-save
// check writes a row to tx_side (and fails for a title of "hookfail"), with
// provenance, the event stream and an AfterCreate counter wired.
type txHarness struct {
	app      *App
	db       *sql.DB
	mod      *Module[*txItem]
	created  atomic.Int32
	stream   chan []byte
	provRepo ProvenanceStore
}

func newTxHarness(t *testing.T) *txHarness {
	t.Helper()
	db := newSQLiteDB(t)
	createTxItemsTable(t, db)
	if err := CreateProvenanceTable(db); err != nil {
		t.Fatal(err)
	}
	h := &txHarness{db: db}
	h.app = New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	h.provRepo = NewProvenanceStore(db)
	h.app.Provenance(h.provRepo)
	h.app.EventStream()
	writeSide := saveHooksOption{before: func(ctx Context, exec DB, _, item any) error {
		it := item.(*txItem)
		if _, err := exec.ExecContext(ctx, `INSERT INTO tx_side (item_id) VALUES ($1)`, it.ID); err != nil {
			return err
		}
		if it.Title == "hookfail" {
			return Err("title", "refused by the pre-save check after it wrote")
		}
		return nil
	}}
	h.mod = NewModule((*txItem)(nil),
		Repo(NewSQLRepo[*txItem](db, Table("tx_items"))),
		At("/tx"),
		writeSide,
		On(AfterCreate, func(Context, *txItem) error { h.created.Add(1); return nil }),
	)
	h.app.Content(h.mod)
	_ = h.app.Handler()
	ch, err := h.app.eventBroadcaster.subscribe("watcher", eventStreamChannelAll)
	if err != nil {
		t.Fatal(err)
	}
	h.stream = ch
	return h
}

func (h *txHarness) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := h.db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

// mcpCreate creates through MCPCreate as an author.
func (h *txHarness) mcpCreate(title string) (string, error) {
	ctx := NewTestContext(User{ID: "u-author", Roles: []Role{Author}})
	out, err := h.mod.MCPCreate(ctx, map[string]any{"Title": title})
	if err != nil {
		return "", err
	}
	return out.(*txItem).ID, nil
}

// httpCreate creates through POST /tx as an author and returns the status.
func (h *txHarness) httpCreate(t *testing.T, title string) int {
	t.Helper()
	tok, err := SignToken(User{ID: "u-author", Roles: []Role{Author}}, "supersecretkey16", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"Title": title})
	r := httptest.NewRequest(http.MethodPost, "/tx", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.app.Handler().ServeHTTP(w, r)
	return w.Code
}

// assertNothingFor checks that a failed create left nothing: no item, no
// side-table row, no AfterCreate call, and, after a later successful create's
// own event and provenance have arrived, neither of those for the failed one.
func (h *txHarness) assertNothingAfterFailure(t *testing.T, create func(title string) bool) {
	t.Helper()
	if n := h.count(t, `SELECT COUNT(*) FROM tx_items`); n != 0 {
		t.Fatalf("tx_items rows after a failed create = %d, want 0", n)
	}
	if n := h.count(t, `SELECT COUNT(*) FROM tx_side`); n != 0 {
		t.Errorf("tx_side rows after a failed create = %d, want 0 (the pre-save write was not rolled back)", n)
	}
	// A successful create is the positive control: its event and provenance
	// arrive, and the failed one's never do.
	if !create("ok") {
		t.Fatal("control create failed")
	}
	deadline := time.Now().Add(3 * time.Second)
	var events int
	for h.count(t, `SELECT COUNT(*) FROM smeldr_provenance WHERE subject_type = 'txItem' OR subject_type = 'TxItem'`) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	for drained := false; !drained; {
		select {
		case <-h.stream:
			events++
		case <-time.After(200 * time.Millisecond):
			drained = true
		}
	}
	if events != 1 {
		t.Errorf("event-stream lines = %d, want 1 (the control create only)", events)
	}
	if n := h.count(t, `SELECT COUNT(*) FROM smeldr_provenance`); n != 1 {
		t.Errorf("provenance records = %d, want 1 (the control create only)", n)
	}
	time.Sleep(50 * time.Millisecond) // AfterCreate handlers are asynchronous
	if got := h.created.Load(); got != 1 {
		t.Errorf("AfterCreate calls = %d, want 1 (the control create only)", got)
	}
	if n := h.count(t, `SELECT COUNT(*) FROM tx_side`); n != 1 {
		t.Errorf("tx_side rows = %d, want 1 (the control create only)", n)
	}
}

// A failing INSERT, and a pre-save check that fails after writing, each leave
// no row, no pre-save write, no event and no provenance, through MCP and HTTP.
func TestCreateInTx_RollbackEveryCompiledPath(t *testing.T) {
	for _, title := range []string{"fail", "hookfail"} {
		t.Run("mcp/"+title, func(t *testing.T) {
			h := newTxHarness(t)
			if _, err := h.mcpCreate(title); err == nil {
				t.Fatal("MCPCreate succeeded, want an error")
			}
			h.assertNothingAfterFailure(t, func(title string) bool { _, err := h.mcpCreate(title); return err == nil })
		})
		t.Run("http/"+title, func(t *testing.T) {
			h := newTxHarness(t)
			if code := h.httpCreate(t, title); code < 400 {
				t.Fatalf("POST /tx = %d, want an error status", code)
			}
			h.assertNothingAfterFailure(t, func(title string) bool { return h.httpCreate(t, title) == http.StatusCreated })
		})
	}
}

// With one SQLite connection a read on the plain db inside the create's
// transaction would wait forever; a create must finish, not hang.
func TestCreateInTx_NoDeadlockOnOneConnection(t *testing.T) {
	h := newTxHarness(t)
	done := make(chan error, 2)
	go func() { _, err := h.mcpCreate("one"); done <- err }()
	go func() {
		if code := h.httpCreate(t, "two"); code != http.StatusCreated {
			done <- errors.New("http create failed")
			return
		}
		done <- nil
	}()
	for range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a create did not finish within 5s on one connection (deadlock)")
		}
	}
}

// The order change in createHandler is pinned: an HTTP create that fails both
// the slug-collision check and the type's own pre-save check now returns the
// collision error, because the collision check runs first, before the
// transaction.
func TestCreateHandler_CollisionCheckedBeforePreSave(t *testing.T) {
	h := newTxHarness(t)
	h.mod.slugCheckers = append(h.mod.slugCheckers, func(context.Context, string) error {
		return Err("slug", "taken by a sibling module")
	})
	tok, _ := SignToken(User{ID: "u-author", Roles: []Role{Author}}, "supersecretkey16", time.Hour)
	body, _ := json.Marshal(map[string]any{"Title": "hookfail", "Status": "published"})
	r := httptest.NewRequest(http.MethodPost, "/tx", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	h.app.Handler().ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "taken by a sibling module") {
		t.Errorf("response %d %s, want the collision error", w.Code, w.Body.String())
	}
	if n := h.count(t, `SELECT COUNT(*) FROM tx_side`); n != 0 {
		t.Errorf("tx_side rows = %d, want 0: the pre-save check must not have run", n)
	}
}

// fakeTxDB is a DB whose BeginTx fails, for runInTx's begin path.
type fakeTxDB struct{ DB }

func (fakeTxDB) BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error) {
	return nil, errors.New("no connection")
}

func TestRunInTx_Paths(t *testing.T) {
	ctx := context.Background()
	db := newSQLiteDB(t)
	if _, err := db.Exec(`CREATE TABLE t (v TEXT)`); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		var n int
		_ = db.QueryRow(`SELECT COUNT(*) FROM t`).Scan(&n)
		return n
	}

	if err := runInTx(ctx, db, func(exec DB) error {
		_, err := exec.ExecContext(ctx, `INSERT INTO t VALUES ('a')`)
		return err
	}); err != nil || count() != 1 {
		t.Fatalf("commit path: err %v, rows %d, want nil and 1", err, count())
	}

	boom := errors.New("boom")
	if err := runInTx(ctx, db, func(exec DB) error {
		_, _ = exec.ExecContext(ctx, `INSERT INTO t VALUES ('b')`)
		return boom
	}); !errors.Is(err, boom) || count() != 1 {
		t.Errorf("fn error: err %v, rows %d, want boom unchanged and 1 (rolled back)", err, count())
	}

	ran := false
	if err := runInTx(ctx, fakeTxDB{db}, func(DB) error { ran = true; return nil }); !errors.Is(err, ErrInternal) || ran {
		t.Errorf("begin failure: err %v, fn ran %v, want ErrInternal and not run", err, ran)
	}

	// A handle without BeginTx (here the transaction itself) runs fn directly.
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := runInTx(ctx, tx, func(exec DB) error {
		if exec != DB(tx) {
			t.Error("fn did not get the handle itself")
		}
		return nil
	}); err != nil {
		t.Errorf("no-BeginTx path: %v", err)
	}
	_ = tx.Rollback()

	// A commit failure: the transaction's connection is gone before commit.
	if err := runInTx(ctx, db, func(exec DB) error {
		return exec.(*sql.Tx).Rollback()
	}); !errors.Is(err, ErrInternal) {
		t.Errorf("commit failure: err %v, want ErrInternal", err)
	}
}

// InsertSignal fills the defaults, keeps a given slug, and leaves no row when
// the INSERT fails.
func TestInsertSignal(t *testing.T) {
	ctx := context.Background()
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatal(err)
	}
	s := &Signal{Sender: "core", Receiver: "architect", SignalType: "plan-ready", Message: "m"}
	if err := InsertSignal(ctx, db, s); err != nil {
		t.Fatal(err)
	}
	if s.ID == "" || s.Slug != s.ID || s.Status != "pending" || s.CreatedAt.IsZero() {
		t.Errorf("defaults = id %q slug %q status %q created %v", s.ID, s.Slug, s.Status, s.CreatedAt)
	}
	kept := &Signal{Node: Node{Slug: "core-plan-ready-x"}, Sender: "core", SignalType: "plan-ready"}
	if err := InsertSignal(ctx, db, kept); err != nil || kept.Slug != "core-plan-ready-x" {
		t.Errorf("given slug: err %v slug %q", err, kept.Slug)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM smeldr_signals`).Scan(&n)
	if n != 2 {
		t.Fatalf("rows = %d, want 2", n)
	}
	if _, err := db.Exec(`CREATE TRIGGER sig_refuse BEFORE INSERT ON smeldr_signals BEGIN SELECT RAISE(ABORT, 'no'); END`); err != nil {
		t.Fatal(err)
	}
	if err := InsertSignal(ctx, db, &Signal{Sender: "core"}); err == nil || !strings.Contains(err.Error(), "InsertSignal") {
		t.Errorf("refused insert: err %v, want an InsertSignal error", err)
	}
	_ = db.QueryRow(`SELECT COUNT(*) FROM smeldr_signals`).Scan(&n)
	if n != 2 {
		t.Errorf("rows after a refused insert = %d, want 2", n)
	}
}

// recordAuthorizationRequiredSignal returns its error with no row and no
// signal.created when the INSERT fails, and still writes the same row as before
// when it succeeds.
func TestAuthorizationRequiredSignal_Rollback(t *testing.T) {
	ctx := context.Background()
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatal(err)
	}
	b := newEventBroadcaster()
	ch, _ := b.subscribe("w", eventStreamChannelAll)
	if err := recordAuthorizationRequiredSignal(ctx, db, nil, nil, b, "Decision", "d1", "proposed", "ratified", "ratify"); err != nil {
		t.Fatal(err)
	}
	var sender, receiver, typ, role, from, to string
	if err := db.QueryRow(`SELECT sender, receiver, signal_type, required_role, from_state, to_state FROM smeldr_signals`).
		Scan(&sender, &receiver, &typ, &role, &from, &to); err != nil {
		t.Fatal(err)
	}
	if sender != "system" || receiver != "ratify" || typ != "authorization-required" || role != "ratify" || from != "proposed" || to != "ratified" {
		t.Errorf("row = %s %s %s %s %s %s", sender, receiver, typ, role, from, to)
	}
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("no signal.created for the committed row")
	}
	if _, err := db.Exec(`CREATE TRIGGER sig_refuse BEFORE INSERT ON smeldr_signals BEGIN SELECT RAISE(ABORT, 'no'); END`); err != nil {
		t.Fatal(err)
	}
	if err := recordAuthorizationRequiredSignal(ctx, db, nil, nil, b, "Decision", "d2", "proposed", "ratified", "ratify"); err == nil {
		t.Fatal("want an error from a refused insert")
	}
	select {
	case <-ch:
		t.Error("signal.created sent for a row that was not committed")
	case <-time.After(200 * time.Millisecond):
	}
}

// A dynamic create whose INSERT fails leaves no row, no standing and no
// provenance record.
func TestDynamicCreate_Rollback(t *testing.T) {
	ctx := context.Background()
	db := newSQLiteDB(t)
	for _, f := range []func(DB) error{CreateBlockTables, CreateSchemaTable, CreateProvenanceTable} {
		if err := f(db); err != nil {
			t.Fatal(err)
		}
	}
	prov := NewProvenanceStore(db)
	repo := NewDynamicTypeRepo(db, "note", &ContentTypeSchema{TypeName: "note", Fields: json.RawMessage(`[{"Name":"Title","Type":"string","Role":"title"}]`)}).WithProvenance(prov)
	if _, err := db.Exec(`CREATE TRIGGER dyn_refuse BEFORE INSERT ON smeldr_dynamic_content
		WHEN NEW.slug LIKE 'fail%' BEGIN SELECT RAISE(ABORT, 'no'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateDraftVia(ctx, "mcp", map[string]any{"Title": "fail"}); err == nil {
		t.Fatal("want an error")
	}
	var rows, records int
	_ = db.QueryRow(`SELECT COUNT(*) FROM smeldr_dynamic_content`).Scan(&rows)
	_ = db.QueryRow(`SELECT COUNT(*) FROM smeldr_provenance`).Scan(&records)
	if rows != 0 || records != 0 {
		t.Errorf("after a failed dynamic create: rows %d, provenance %d, want 0 and 0", rows, records)
	}
	if _, err := repo.CreateDraftVia(ctx, "mcp", map[string]any{"Title": "fine"}); err != nil {
		t.Fatal(err)
	}
	_ = db.QueryRow(`SELECT COUNT(*) FROM smeldr_provenance`).Scan(&records)
	if records != 1 {
		t.Errorf("provenance after a successful create = %d, want 1", records)
	}
}

// A conflict-detected Signal whose INSERT fails is logged, leaves no row and
// dispatches no signal.created, and the relation assertion still succeeds.
func TestConflictDetectedSignal_Rollback(t *testing.T) {
	store := setupRelationStoreWithSignals(t)
	b := newEventBroadcaster()
	ch, _ := b.subscribe("w", eventStreamChannelAll)
	store.setSignalDeps(nil, nil, b)
	if _, err := store.db.ExecContext(context.Background(),
		`CREATE TRIGGER sig_refuse BEFORE INSERT ON smeldr_signals BEGIN SELECT RAISE(ABORT, 'no'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MCPAssertRelation(context.Background(), "Decision", "d1", "Decision", "d2", "contradicts", nil, nil, nil, nil); err != nil {
		t.Fatalf("MCPAssertRelation: %v, want the assertion to succeed", err)
	}
	if rows := queryConflictSignals(t, store); len(rows) != 0 {
		t.Errorf("signal rows = %d, want 0", len(rows))
	}
	for drained := false; !drained; {
		select {
		case line := <-ch:
			if strings.Contains(string(line), "signal.created") {
				t.Errorf("signal.created dispatched for a row that was not committed: %s", line)
			}
		case <-time.After(200 * time.Millisecond):
			drained = true
		}
	}
}
