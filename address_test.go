package smeldr

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func addressDB(t *testing.T) *sql.DB {
	t.Helper()
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	return db
}

// decisionModule is a Module[*Decision] on db that allocates addresses, as
// RegisterOrchestrationTypes wires it.
func decisionModule(t *testing.T, db DB) (*App, *Module[*Decision]) {
	t.Helper()
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	m := NewModule((*Decision)(nil), Repo(NewSQLRepo[*Decision](db, Table("smeldr_decisions"))), At("/decisions"),
		MCP(MCPRead, MCPWrite), withAddress())
	app.Content(m)
	_ = app.Handler()
	return app, m
}

func authorCtx() Context {
	return NewTestContext(User{ID: "u-author", Roles: []Role{Author, Editor}})
}

func mustCreateDecision(t *testing.T, m *Module[*Decision], fields map[string]any) *Decision {
	t.Helper()
	out, err := m.MCPCreate(authorCtx(), fields)
	if err != nil {
		t.Fatalf("MCPCreate: %v", err)
	}
	return asItem[*Decision](out)
}

func TestCheckAddressPrefix(t *testing.T) {
	cases := []struct {
		typeName, prefix string
		ok               bool
	}{
		{"invoice", "INV", true},
		{"invoice", "IN", true},
		{"invoice", "INVO", true},
		{"invoice", "INVOI", false}, // too long
		{"invoice", "inv", false},   // lower case
		{"invoice", "I1", false},    // digit
		{"invoice", "Q", false},     // single letter reserved for core
		{"invoice", "D", false},     // Decision's letter
		{"Decision", "D", true},
		{"Decision", "DEC", false}, // a core type keeps its reserved prefix
		{"task_plan", "P", true},
		{"task_plan", "TP", false},
	}
	for _, c := range cases {
		err := checkAddressPrefix(c.typeName, c.prefix)
		if (err == nil) != c.ok {
			t.Errorf("checkAddressPrefix(%q, %q) = %v, want ok=%v", c.typeName, c.prefix, err, c.ok)
		}
	}
}

func TestRegisterAddressPrefix_Rules(t *testing.T) {
	ctx := context.Background()
	db := addressDB(t)
	if err := registerAddressPrefix(ctx, db, "invoice", "INV", nil); err != nil {
		t.Fatal(err)
	}
	if err := registerAddressPrefix(ctx, db, "invoice", "INV", nil); err != nil {
		t.Errorf("same pair again: %v, want nil (idempotent)", err)
	}
	if err := registerAddressPrefix(ctx, db, "ticket", "INV", nil); err == nil || !strings.Contains(err.Error(), "already the address prefix of invoice") {
		t.Errorf("a taken prefix: %v, want refused naming invoice", err)
	}
	if err := registerAddressPrefix(ctx, db, "invoice", "IVC", nil); err != nil {
		t.Errorf("a change before the first item: %v, want nil", err)
	}
	if _, _, err := allocateAddress(ctx, db, "invoice"); err != nil {
		t.Fatal(err)
	}
	if err := registerAddressPrefix(ctx, db, "invoice", "INV", nil); err == nil || !strings.Contains(err.Error(), "fixed") {
		t.Errorf("a change after the first item: %v, want refused as fixed", err)
	}
	seedErr := errors.New("seed failed")
	if err := registerAddressPrefix(ctx, db, "memo", "MEM", func() (int64, error) { return 0, seedErr }); !errors.Is(err, ErrInternal) {
		t.Errorf("a seed failure: %v, want ErrInternal", err)
	}
}

func TestProposeAddressPrefix(t *testing.T) {
	ctx := context.Background()
	db := addressDB(t)
	got, err := proposeAddressPrefix(ctx, db, "invoice")
	if err != nil || got != "INV" {
		t.Fatalf("invoice: %q %v, want INV", got, err)
	}
	if err := registerAddressPrefix(ctx, db, "invoice", "INV", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := proposeAddressPrefix(ctx, db, "invoice_two"); got != "INVO" {
		t.Errorf("INV taken: %q, want INVO", got)
	}
	if got, _ := proposeAddressPrefix(ctx, db, "customer_ticket"); got != "CUS" {
		t.Errorf("customer_ticket: %q, want CUS", got)
	}
	if got, _ := proposeAddressPrefix(ctx, db, "x-9_y"); got != "XY" {
		t.Errorf("non-letters dropped: %q, want XY", got)
	}
	if _, err := proposeAddressPrefix(ctx, db, "q1"); err == nil {
		t.Error("a name with one letter: want an error asking for a prefix")
	}
	for _, p := range []string{"AB"} {
		if err := registerAddressPrefix(ctx, db, "ab_holder", p, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := proposeAddressPrefix(ctx, db, "ab"); err == nil || !strings.Contains(err.Error(), "taken") {
		t.Errorf("every candidate taken: %v, want an error", err)
	}
}

func TestAllocateAddress_UnregisteredType(t *testing.T) {
	_, _, err := allocateAddress(context.Background(), addressDB(t), "nobody")
	if !errors.Is(err, ErrInternal) || !errors.Is(err, errNoAddressPrefix) {
		t.Errorf("err = %v, want ErrInternal and errNoAddressPrefix", err)
	}
}

// Creates are numbered in order, concurrent creates never share a number, and a
// failed create takes none.
func TestCompiledCreate_AddressesInOrderAndNoGap(t *testing.T) {
	db := addressDB(t)
	_, m := decisionModule(t, db)
	first := mustCreateDecision(t, m, map[string]any{"Title": "one"})
	if first.Address != "D1" || first.AddressNo != 1 {
		t.Fatalf("first = %q/%d, want D1/1", first.Address, first.AddressNo)
	}
	if _, err := db.Exec(`CREATE TRIGGER d_refuse BEFORE INSERT ON smeldr_decisions WHEN NEW.title = 'fail'
		BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := m.MCPCreate(authorCtx(), map[string]any{"Title": "fail"}); err == nil {
		t.Fatal("want the refused create to fail")
	}
	if next := mustCreateDecision(t, m, map[string]any{"Title": "two"}); next.Address != "D2" {
		t.Errorf("after a failed create: %q, want D2 (no gap)", next.Address)
	}

	const n = 12
	var wg sync.WaitGroup
	got := make(chan string, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := m.MCPCreate(authorCtx(), map[string]any{"Title": fmt.Sprintf("concurrent %d", i)})
			if err != nil {
				t.Errorf("concurrent create: %v", err)
				return
			}
			got <- asItem[*Decision](out).Address
		}()
	}
	wg.Wait()
	close(got)
	seen := map[string]bool{}
	for a := range got {
		if seen[a] {
			t.Errorf("address %s given twice", a)
		}
		seen[a] = true
	}
	for i := 3; i < 3+n; i++ {
		if !seen[fmt.Sprintf("D%d", i)] {
			t.Errorf("D%d missing: concurrent creates must take D3..D%d", i, 2+n)
		}
	}
}

// An HTTP POST allocates too, and PUT keeps the stored address whatever the
// body says.
func TestCompiledHTTP_AddressAssignedAndKeptByPut(t *testing.T) {
	db := addressDB(t)
	app, _ := decisionModule(t, db)
	tok, _ := SignToken(User{ID: "u-author", Roles: []Role{Author, Editor}}, "supersecretkey16", time.Hour)
	do := func(method, path string, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		app.Handler().ServeHTTP(w, r)
		return w
	}
	w := do(http.MethodPost, "/decisions", map[string]any{"Title": "posted", "Address": "D999"})
	if w.Code != http.StatusCreated {
		t.Fatalf("POST = %d %s", w.Code, w.Body.String())
	}
	var created Decision
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.Address != "D1" {
		t.Fatalf("POST address = %q, want D1 (a sent address is ignored)", created.Address)
	}
	w = do(http.MethodPut, "/decisions/"+created.Slug, map[string]any{"Title": "changed", "Address": "D77", "AddressNo": 77, "Status": "draft"})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", w.Code, w.Body.String())
	}
	var stored string
	_ = db.QueryRow(`SELECT address FROM smeldr_decisions WHERE id = $1`, created.ID).Scan(&stored)
	if stored != "D1" {
		t.Errorf("after PUT the stored address is %q, want D1", stored)
	}
}

// MCP update and PATCH refuse a different address; the same value is accepted.
func TestCompiledUpdate_RefusesAddress(t *testing.T) {
	db := addressDB(t)
	_, m := decisionModule(t, db)
	d := mustCreateDecision(t, m, map[string]any{"Title": "x"})
	ctx := authorCtx()
	if _, err := m.MCPUpdate(ctx, d.Slug, map[string]any{"address": "D50"}); err == nil || !strings.Contains(err.Error(), "address") {
		t.Errorf("update address: %v, want refused", err)
	}
	if _, err := m.MCPUpdate(ctx, d.Slug, map[string]any{"address_no": float64(50)}); err == nil {
		t.Error("update address_no: want refused")
	}
	if _, err := m.MCPUpdate(ctx, d.Slug, map[string]any{"address": "D1", "title": "same address"}); err != nil {
		t.Errorf("update with the stored address: %v, want accepted", err)
	}
	if _, err := m.MCPUpdate(ctx, d.Slug, map[string]any{"external_ref": "OPS-7"}); err != nil {
		t.Errorf("update external_ref: %v, want accepted", err)
	}
	var ref string
	_ = db.QueryRow(`SELECT external_ref FROM smeldr_decisions WHERE id = $1`, d.ID).Scan(&ref)
	if ref != "OPS-7" {
		t.Errorf("external_ref = %q, want OPS-7", ref)
	}
}

// The input schema leaves the readonly address fields out and keeps
// external_ref; a user's own readonly field is not a validation constraint.
func TestMCPSchema_ReadonlyAddressFields(t *testing.T) {
	_, m := decisionModule(t, addressDB(t))
	names := map[string]bool{}
	for _, f := range m.MCPSchema() {
		names[f.JSONName] = true
	}
	if names["address"] || names["address_no"] {
		t.Errorf("schema has address fields: %v", names)
	}
	if !names["external_ref"] {
		t.Errorf("schema lacks external_ref: %v", names)
	}
	type withReadonly struct {
		Node
		Computed string `smeldr:"readonly"`
	}
	if err := RunValidation(&withReadonly{}); err != nil {
		t.Errorf("RunValidation with a readonly tag: %v", err)
	}
}

// A module without a database numbers from its own counter under the core
// prefix.
func TestCompiledCreate_MemoryCounter(t *testing.T) {
	m := NewModule((*Decision)(nil), Repo(NewMemoryRepo[*Decision]()), At("/decisions"), withAddress())
	for i := 1; i <= 2; i++ {
		out, err := m.MCPCreate(authorCtx(), map[string]any{"Title": "m"})
		if err != nil {
			t.Fatal(err)
		}
		if got := asItem[*Decision](out).Address; got != fmt.Sprintf("D%d", i) {
			t.Errorf("create %d: %q", i, got)
		}
	}
}

// The counter starts above every existing legacy number, duplicated or not,
// and a type with no legacy field starts at its count of unaddressed rows.
func TestCounterSeededFromExistingRows(t *testing.T) {
	ctx := context.Background()
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatal(err)
	}
	// Simulate a database from before addresses: drop the counters and add rows.
	if _, err := db.Exec(`DELETE FROM smeldr_address_counters`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i, n := range []string{"D7", "D7", "D3", "D74-DECLINE", ""} {
		if _, err := db.Exec(`INSERT INTO smeldr_decisions (id, slug, created_at, updated_at, decision_number) VALUES ($1, $2, $3, $3, $4)`,
			fmt.Sprintf("d%d", i), fmt.Sprintf("d%d", i), now, n); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 4 {
		if _, err := db.Exec(`INSERT INTO smeldr_signals (id, slug, created_at, updated_at) VALUES ($1, $1, $2, $2)`,
			fmt.Sprintf("s%d", i), now); err != nil {
			t.Fatal(err)
		}
	}
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatal(err)
	}
	if a, _, _ := allocateAddress(ctx, db, "Decision"); a != "D8" {
		t.Errorf("first new Decision = %s, want D8 (above the highest legacy D7)", a)
	}
	if a, _, _ := allocateAddress(ctx, db, "Signal"); a != "S5" {
		t.Errorf("first new Signal = %s, want S5 (4 existing Signals are numbered 1 to 4 by the migration)", a)
	}
}

// InsertSignal numbers its Signal, and a refused insert takes no number.
func TestInsertSignal_Addresses(t *testing.T) {
	ctx := context.Background()
	db := addressDB(t)
	s := &Signal{Sender: "core", SignalType: "plan-ready"}
	if err := InsertSignal(ctx, db, s); err != nil || s.Address != "S1" {
		t.Fatalf("first: %q %v, want S1", s.Address, err)
	}
	if _, err := db.Exec(`CREATE TRIGGER s_refuse BEFORE INSERT ON smeldr_signals WHEN NEW.signal_type = 'no'
		BEGIN SELECT RAISE(ABORT, 'no'); END`); err != nil {
		t.Fatal(err)
	}
	refused := &Signal{Sender: "core", SignalType: "no"}
	if err := InsertSignal(ctx, db, refused); err == nil || refused.Address != "" {
		t.Errorf("refused: err %v address %q, want an error and no address", err, refused.Address)
	}
	next := &Signal{Sender: "core", SignalType: "plan-ready"}
	if err := InsertSignal(ctx, db, next); err != nil || next.Address != "S2" {
		t.Errorf("after a refused insert: %q %v, want S2", next.Address, err)
	}
}

func dynamicAddressApp(t *testing.T) (*App, *sql.DB) {
	t.Helper()
	db := newSQLiteDB(t)
	if err := CreateBlockTables(db); err != nil {
		t.Fatal(err)
	}
	if err := CreateSchemaTable(db); err != nil {
		t.Fatal(err)
	}
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	app.ServeDynamicContent()
	return app, db
}

func schemaFor(name, urlPrefix, addressPrefix string) *ContentTypeSchema {
	return &ContentTypeSchema{TypeName: name, URLPrefix: urlPrefix, AddressPrefix: addressPrefix,
		Fields: json.RawMessage(`[{"Name":"Title","Type":"string","Role":"title"}]`)}
}

// A dynamic record type gets a proposed or given prefix and its items are
// numbered; web content has none; task_plan takes P; wrong prefixes are refused.
func TestDynamicTypes_Prefixes(t *testing.T) {
	ctx := context.Background()
	app, _ := dynamicAddressApp(t)
	desc, err := app.DefineContentType(ctx, schemaFor("invoice", "", ""))
	if err != nil {
		t.Fatal(err)
	}
	if desc.Schema.AddressPrefix != "INV" {
		t.Errorf("proposed prefix = %q, want INV", desc.Schema.AddressPrefix)
	}
	repo, _ := app.DynamicContentRepo("invoice")
	a, _ := repo.CreateDraft(ctx, map[string]any{"Title": "first"})
	b, _ := repo.CreateDraft(ctx, map[string]any{"Title": "second"})
	if a.Address != "INV1" || b.Address != "INV2" {
		t.Errorf("addresses = %q %q, want INV1 INV2", a.Address, b.Address)
	}

	if _, err := app.DefineContentType(ctx, schemaFor("article", "/articles", "")); err != nil {
		t.Fatal(err)
	}
	arepo, _ := app.DynamicContentRepo("article")
	if art, _ := arepo.CreateDraft(ctx, map[string]any{"Title": "web"}); art.Address != "" {
		t.Errorf("web content address = %q, want none", art.Address)
	}
	if _, err := app.DefineContentType(ctx, schemaFor("page", "/pages", "PG")); err == nil {
		t.Error("a prefix on web content: want refused")
	}
	if _, err := app.DefineContentType(ctx, schemaFor("task_plan", "", "TP")); err == nil || !strings.Contains(err.Error(), `"P"`) {
		t.Errorf("task_plan with TP: %v, want refused naming P", err)
	}
	tp, err := app.DefineContentType(ctx, schemaFor("task_plan", "", ""))
	if err != nil || tp.Schema.AddressPrefix != "P" {
		t.Errorf("task_plan: %v prefix %q, want P", err, tp.Schema.AddressPrefix)
	}
	if _, err := app.DefineContentType(ctx, schemaFor("ticket", "", "TK")); err != nil {
		t.Errorf("an explicit 2-letter prefix: %v", err)
	}
	reserved := &ContentTypeSchema{TypeName: "bad", Fields: json.RawMessage(`[{"Name":"address","Type":"string"}]`)}
	if _, err := app.DefineContentType(ctx, reserved); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Errorf("a field named address: %v, want refused", err)
	}
}

// Redefine keeps the prefix when it is absent and refuses a change once the
// type has addresses.
func TestDynamicTypes_RedefineKeepsPrefix(t *testing.T) {
	ctx := context.Background()
	app, _ := dynamicAddressApp(t)
	if _, err := app.DefineContentType(ctx, schemaFor("invoice", "", "")); err != nil {
		t.Fatal(err)
	}
	repo, _ := app.DynamicContentRepo("invoice")
	if _, err := repo.CreateDraft(ctx, map[string]any{"Title": "one"}); err != nil {
		t.Fatal(err)
	}
	next := schemaFor("invoice", "", "")
	next.Fields = json.RawMessage(`[{"Name":"Title","Type":"string","Role":"title"},{"Name":"Amount","Type":"number"}]`)
	nd, err := app.RedefineContentTypeVia(ctx, "mcp", next, "add amount")
	if err != nil {
		t.Fatalf("redefine without prefix: %v", err)
	}
	if nd.Schema.AddressPrefix != "INV" {
		t.Errorf("prefix after redefine = %q, want INV kept", nd.Schema.AddressPrefix)
	}
	changed := schemaFor("invoice", "", "BIL")
	changed.Fields = next.Fields
	if _, err := app.RedefineContentTypeVia(ctx, "mcp", changed, "rename"); err == nil || !strings.Contains(err.Error(), "fixed") {
		t.Errorf("a changed prefix after the first item: %v, want refused", err)
	}
}

// A record type defined before addresses gets its prefix when it is loaded at
// boot, so it can create at once; the proposal is logged.
func TestDynamicTypes_LoadRegistersPrefix(t *testing.T) {
	ctx := context.Background()
	db := newSQLiteDB(t)
	for _, f := range []func(DB) error{CreateBlockTables, CreateSchemaTable} {
		if err := f(db); err != nil {
			t.Fatal(err)
		}
	}
	// An old schema row and two old items, written as before addresses.
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO smeldr_content_type_schemas (id, type_name, label, kind, url_prefix, fields, created_at, updated_at)
		VALUES ('sch1', 'domain', 'Domain', 'content', '', '[{"Name":"Title","Type":"string","Role":"title"}]', $1, $1)`, now); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if _, err := db.Exec(`INSERT INTO smeldr_dynamic_content (id, slug, type_name, created_at, updated_at) VALUES ($1, $1, 'domain', $2, $2)`,
			fmt.Sprintf("old%d", i), now); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	app.ServeDynamicContent()
	_ = app.Handler()
	repo, err := app.DynamicContentRepo("domain")
	if err != nil {
		t.Fatal(err)
	}
	n, err := repo.CreateDraft(ctx, map[string]any{"Title": "new"})
	if err != nil {
		t.Fatalf("create right after boot: %v", err)
	}
	if n.Address != "DOM3" {
		t.Errorf("address = %q, want DOM3 (the 2 old items are numbered 1 and 2 by the migration)", n.Address)
	}
	if !strings.Contains(buf.String(), "prefix=DOM") || !strings.Contains(buf.String(), "type=domain") {
		t.Errorf("log = %q, want the proposed prefix logged", buf.String())
	}
}

// A repo built directly, outside App, registers its prefix on its first create.
func TestDynamicRepoDirect_RegistersPrefixOnFirstCreate(t *testing.T) {
	ctx := context.Background()
	db := newSQLiteDB(t)
	if err := CreateBlockTables(db); err != nil {
		t.Fatal(err)
	}
	repo := NewDynamicTypeRepo(db, "memo", schemaFor("memo", "", ""))
	n, err := repo.CreateDraft(ctx, map[string]any{"Title": "one"})
	if err != nil || n.Address != "MEM1" {
		t.Errorf("first create: %q %v, want MEM1", n.Address, err)
	}
}

// Events and packets carry the address.
func TestAddress_InWebhookPayloadAndPacket(t *testing.T) {
	d := &Decision{Node: Node{ID: "id1", Slug: "d1"}, RecordAddress: RecordAddress{Address: "D9", ExternalRef: "X-1"}, DecisionNumber: "D9-old"}
	payload, err := buildWebhookPayload("Decision", d, AfterCreate)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"address":"D9"`) {
		t.Errorf("webhook payload = %s, want the address", payload)
	}
	id, fields := packetFieldsFromItem(d)
	if id != "D9" || fields["address"] != "D9" || fields["external_ref"] != "X-1" {
		t.Errorf("packet = %q %v, want D9 with address and external_ref", id, fields)
	}
	legacy := &Decision{DecisionNumber: "D104"}
	if id, _ := packetFieldsFromItem(legacy); id != "D104" {
		t.Errorf("unaddressed packet id = %q, want the legacy D104 (transitional fallback)", id)
	}
}

// The address index refuses duplicates and is idempotent.
func TestEnsureAddressStorage(t *testing.T) {
	ctx := context.Background()
	db := addressDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Errorf("second CreateOrchestrationTables: %v, want idempotent", err)
	}
	if _, err := db.Exec(`CREATE TABLE dup_t (id TEXT, type_name TEXT, address TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO dup_t (id, address) VALUES ('a', 'X1'), ('b', 'X1')`); err != nil {
		t.Fatal(err)
	}
	if err := ensureAddressStorage(ctx, db, "dup_t", false); err == nil || !strings.Contains(err.Error(), "X1") {
		t.Errorf("duplicate addresses: %v, want an error naming X1", err)
	}
}

// failAddressDB fails every statement whose text contains match.
type failAddressDB struct {
	DB
	match string
}

func (f failAddressDB) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	if strings.Contains(q, f.match) {
		return f.DB.QueryRowContext(ctx, "SELECT * FROM no_such_table_for_test")
	}
	return f.DB.QueryRowContext(ctx, q, args...)
}

func (f failAddressDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if strings.Contains(q, f.match) {
		return nil, errors.New("query refused")
	}
	return f.DB.QueryContext(ctx, q, args...)
}

func (f failAddressDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	if strings.Contains(q, f.match) {
		return nil, errors.New("exec refused")
	}
	return f.DB.ExecContext(ctx, q, args...)
}

// Database errors on every prefix and allocation path are ErrInternal, and a
// registration that fails leaves the registry as it was.
func TestAddressRegistry_DatabaseErrors(t *testing.T) {
	ctx := context.Background()
	db := addressDB(t)
	cases := []struct {
		name  string
		match string
		run   func(DB) error
	}{
		{"read prefix", "SELECT prefix, last_no", func(d DB) error { return registerAddressPrefix(ctx, d, "invoice", "INV", nil) }},
		{"read holder", "SELECT type_name FROM smeldr_address_counters WHERE prefix", func(d DB) error { return registerAddressPrefix(ctx, d, "invoice", "INV", nil) }},
		{"insert", "INSERT INTO smeldr_address_counters", func(d DB) error { return registerAddressPrefix(ctx, d, "invoice", "INV", nil) }},
		{"propose", "SELECT type_name FROM smeldr_address_counters", func(d DB) error { _, err := proposeAddressPrefix(ctx, d, "invoice"); return err }},
		{"lookup", "SELECT prefix FROM smeldr_address_counters", func(d DB) error { _, err := addressPrefixOf(ctx, d, "Decision"); return err }},
		{"allocate", "UPDATE smeldr_address_counters", func(d DB) error { _, _, err := allocateAddress(ctx, d, "Decision"); return err }},
		{"counter table", "CREATE TABLE IF NOT EXISTS smeldr_address_counters", func(d DB) error { return registerCoreAddresses(ctx, d, orchestrationAddressTypes...) }},
		{"seed", "SELECT task_id", func(d DB) error {
			if _, err := db.Exec(`DELETE FROM smeldr_address_counters WHERE type_name = 'Task'`); err != nil {
				return nil
			}
			return registerCoreAddresses(ctx, d, orchestrationAddressTypes...)
		}},
		{"duplicate scan", "HAVING COUNT", func(d DB) error { return ensureAddressStorage(ctx, d, "smeldr_tasks", false) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.run(failAddressDB{db, c.match}); err == nil {
				t.Errorf("want an error when %q fails", c.match)
			}
		})
	}
	// Changing the prefix of a type with no address yet updates the row; an
	// update failure is ErrInternal.
	if err := registerAddressPrefix(ctx, db, "draft_type", "DRA", nil); err != nil {
		t.Fatal(err)
	}
	if err := registerAddressPrefix(ctx, failAddressDB{db, "UPDATE smeldr_address_counters SET prefix"}, "draft_type", "DRF", nil); !errors.Is(err, ErrInternal) {
		t.Errorf("prefix update failure: %v, want ErrInternal", err)
	}
	addressOption{}.isOption()
}

// A failed address index stops boot for any reason, not only a duplicate:
// D110 promises uniqueness by a database constraint.
func TestEnsureAddressStorage_IndexFailureStopsBoot(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateBlockTables(failAddressDB{db, "_address ON"}); !errors.Is(err, ErrInternal) || !strings.Contains(err.Error(), "unique address index") {
		t.Errorf("CreateBlockTables with the address index refused: %v, want an ErrInternal naming the index", err)
	}
	if err := CreateOrchestrationTables(failAddressDB{newSQLiteDB(t), "_address ON"}); err == nil {
		t.Error("CreateOrchestrationTables with the address index refused: want an error")
	}
}
