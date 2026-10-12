//go:build integration

package pgx

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// PgTxItem is a compiled type for the create-in-transaction tests on Postgres.
type PgTxItem struct {
	smeldr.Node
	Title string `smeldr:"required" db:"title"`
}

// refuseInsertsOf makes every INSERT into table whose column equals value fail
// on the server, the way a constraint or a broken write would.
func refuseInsertsOf(t *testing.T, db smeldr.DB, table, column, value string) {
	t.Helper()
	ctx := context.Background()
	fn := "refuse_" + table
	for _, ddl := range []string{
		`CREATE FUNCTION ` + fn + `() RETURNS trigger AS $$ BEGIN IF NEW.` + column + ` = TG_ARGV[0] THEN RAISE EXCEPTION 'boom: insert refused'; END IF; RETURN NEW; END $$ LANGUAGE plpgsql`,
		`CREATE TRIGGER ` + fn + ` BEFORE INSERT ON ` + table + ` FOR EACH ROW EXECUTE FUNCTION ` + fn + `('` + value + `')`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatalf("%s: %v", ddl, err)
		}
	}
}

func pgCount(t *testing.T, db smeldr.DB, q string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), q).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

// A compiled create whose INSERT the server refuses leaves no row and no
// provenance record on Postgres; a later create commits and is recorded.
func TestPG_CreateInTx_CompiledRollback(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE pg_tx_items (id TEXT PRIMARY KEY, slug TEXT NOT NULL, status TEXT NOT NULL,
		published_at TIMESTAMPTZ, scheduled_at TIMESTAMPTZ, created_at TIMESTAMPTZ, updated_at TIMESTAMPTZ,
		rev INTEGER NOT NULL DEFAULT 0, last_actor TEXT NOT NULL DEFAULT '', title TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.CreateProvenanceTable(db); err != nil {
		t.Fatal(err)
	}
	refuseInsertsOf(t, db, "pg_tx_items", "title", "fail")
	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte(pgTestSecret), DB: db})
	app.Provenance(smeldr.NewProvenanceStore(db))
	mod := smeldr.NewModule((*PgTxItem)(nil), smeldr.Repo(smeldr.NewSQLRepo[*PgTxItem](db, smeldr.Table("pg_tx_items"))), smeldr.At("/pgtx"))
	app.Content(mod)
	_ = app.Handler()
	actx := smeldr.NewTestContext(smeldr.User{ID: "pg-author", Roles: []smeldr.Role{smeldr.Author}})

	if _, err := mod.MCPCreate(actx, map[string]any{"Title": "fail"}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("MCPCreate = %v, want the server's refusal", err)
	}
	if _, err := mod.MCPCreate(actx, map[string]any{"Title": "fine"}); err != nil {
		t.Fatalf("control create: %v", err)
	}
	if n := pgCount(t, db, `SELECT COUNT(*) FROM pg_tx_items`); n != 1 {
		t.Errorf("rows = %d, want 1 (the control create only)", n)
	}
	deadline := time.Now().Add(3 * time.Second)
	for pgCount(t, db, `SELECT COUNT(*) FROM smeldr_provenance`) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if n := pgCount(t, db, `SELECT COUNT(*) FROM smeldr_provenance`); n != 1 {
		t.Errorf("provenance records = %d, want 1 (the control create only)", n)
	}
}

// InsertSignal on Postgres: a refused INSERT leaves no row; a normal one commits.
func TestPG_InsertSignal_Rollback(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateOrchestrationTables(db); err != nil {
		t.Fatal(err)
	}
	refuseInsertsOf(t, db, "smeldr_signals", "signal_type", "refused")
	if err := smeldr.InsertSignal(ctx, db, &smeldr.Signal{Sender: "core", SignalType: "refused"}); err == nil {
		t.Fatal("want an error")
	}
	if err := smeldr.InsertSignal(ctx, db, &smeldr.Signal{Sender: "core", SignalType: "plan-ready"}); err != nil {
		t.Fatal(err)
	}
	if n := pgCount(t, db, `SELECT COUNT(*) FROM smeldr_signals`); n != 1 {
		t.Errorf("rows = %d, want 1", n)
	}
}

// Concurrent dynamic creates with one title on Postgres: a create that loses the
// slug race retries in a new transaction (the failed INSERT aborted the first),
// so every create succeeds with its own slug.
func TestPG_DynamicCreate_RetryInNewTransaction(t *testing.T) {
	db, _ := isolatedDB(t)
	_, repo := dynamicApp(t, db, nil)
	ctx := context.Background()
	const n = 8
	var wg sync.WaitGroup
	slugs := make(chan string, n)
	errs := make(chan error, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			node, err := repo.CreateDraft(ctx, map[string]any{"Title": "race"})
			if err != nil {
				errs <- err
				return
			}
			slugs <- node.Slug
		}()
	}
	wg.Wait()
	close(slugs)
	close(errs)
	for err := range errs {
		t.Errorf("CreateDraft: %v", err)
	}
	seen := map[string]bool{}
	for s := range slugs {
		if seen[s] {
			t.Errorf("slug %q given twice", s)
		}
		seen[s] = true
	}
	if len(seen) != n {
		t.Errorf("distinct slugs = %d, want %d", len(seen), n)
	}
}

// A dynamic create whose INSERT the server refuses leaves no row on Postgres.
func TestPG_DynamicCreate_Rollback(t *testing.T) {
	db, _ := isolatedDB(t)
	_, repo := dynamicApp(t, db, nil)
	ctx := context.Background()
	refuseInsertsOf(t, db, "smeldr_dynamic_content", "slug", "fail")
	if _, err := repo.CreateDraft(ctx, map[string]any{"Title": "fail"}); err == nil {
		t.Fatal("want an error")
	}
	if n := pgCount(t, db, `SELECT COUNT(*) FROM smeldr_dynamic_content WHERE slug = 'fail'`); n != 0 {
		t.Errorf("rows = %d, want 0", n)
	}
}
