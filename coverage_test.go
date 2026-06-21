package smeldr

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"html/template"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDynamicNode_Head(t *testing.T) {
	h := (&DynamicNode{}).Head()
	if h.Title != "Block" {
		t.Errorf("Head.Title = %q, want \"Block\"", h.Title)
	}
}

func TestSiteConfig_Head(t *testing.T) {
	h := (&SiteConfig{}).Head()
	if h.Title != "Site Configuration" {
		t.Errorf("Head.Title = %q, want \"Site Configuration\"", h.Title)
	}
}

func TestNewContextWithUser_fields(t *testing.T) {
	ctx := NewContextWithUser(GuestUser)
	if ctx.User().ID != GuestUser.ID {
		t.Errorf("User().ID = %q, want %q", ctx.User().ID, GuestUser.ID)
	}
	if ctx.Locale() != "en" {
		t.Errorf("Locale() = %q, want \"en\"", ctx.Locale())
	}
	if ctx.Request() == nil {
		t.Error("Request() should not be nil")
	}
	if ctx.Response() == nil {
		t.Error("Response() should not be nil")
	}
}

func TestApp_Secret(t *testing.T) {
	secret := []byte("test-secret-key!1234567890abcde")
	app := New(Config{BaseURL: "https://example.com", Secret: secret})
	got := app.Secret()
	if string(got) != string(secret) {
		t.Errorf("Secret() = %q, want %q", got, secret)
	}
}

func TestApp_BaseURL_trimsTrailingSlash(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com/", Secret: []byte("test-secret-key!1234567890abcde")})
	got := app.BaseURL()
	if got != "https://example.com" {
		t.Errorf("BaseURL() = %q, want \"https://example.com\"", got)
	}
}

func TestApp_TokenStore_nilWhenNotConfigured(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("test-secret-key!1234567890abcde")})
	if app.TokenStore() != nil {
		t.Error("TokenStore() should be nil when not configured")
	}
}

func TestApp_WebhookStore_nilWhenNotConfigured(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("test-secret-key!1234567890abcde")})
	if app.WebhookStore() != nil {
		t.Error("WebhookStore() should be nil when not configured")
	}
}

func TestApp_AddSignalListener(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("test-secret-key!1234567890abcde")})
	before := len(app.signalListeners)
	app.AddSignalListener(func(Signal, string, any) {})
	if got := len(app.signalListeners); got != before+1 {
		t.Errorf("signalListeners len: got %d, want %d", got, before+1)
	}
}

func TestApp_Nav_populatesCodeItems(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("test-secret-key!1234567890abcde")})
	app.Nav(NavItem{ID: "home", Label: "Home"}, NavItem{ID: "about", Label: "About"})
	if len(app.navCodeItems) != 2 {
		t.Errorf("navCodeItems len: got %d, want 2", len(app.navCodeItems))
	}
}

func TestApp_NavTree_nilBeforeHandler(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("test-secret-key!1234567890abcde")})
	if app.NavTree() != nil {
		t.Error("NavTree() should be nil before Handler() is called")
	}
}

func TestApp_UploadToken_roundtrip(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("test-secret-key!1234567890abcde")})
	tok := app.GenerateUploadToken()
	if tok == "" {
		t.Fatal("GenerateUploadToken returned empty string")
	}
	if err := app.ValidateUploadToken(tok); err != nil {
		t.Errorf("ValidateUploadToken valid token: %v", err)
	}
}

func TestApp_UploadToken_tampered(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("test-secret-key!1234567890abcde")})
	if err := app.ValidateUploadToken("tampered.token.value"); err == nil {
		t.Error("ValidateUploadToken with tampered token should return non-nil error")
	}
}

func TestMiddleware_returnsOption(t *testing.T) {
	opt := Middleware(func(h http.Handler) http.Handler { return h })
	if opt == nil {
		t.Error("Middleware() should return a non-nil Option")
	}
}

func TestRealClock_After(t *testing.T) {
	ch := realClock{}.After(time.Millisecond)
	if ch == nil {
		t.Error("realClock.After should return a non-nil channel")
	}
}

func TestApp_SEO_HeadAssets(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("test-secret-key!1234567890abcde")})
	app.SEO(&HeadAssets{Preconnect: []string{"https://fonts.example.com"}})
	if app.seo.headAssets == nil {
		t.Error("seo.headAssets should be set after SEO(&HeadAssets{...})")
	}
}

func TestApp_SEO_AppSchema(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("test-secret-key!1234567890abcde")})
	app.SEO(&AppSchema{Type: "Organization", Name: "Acme"})
	if app.seo.appSchema == nil {
		t.Error("seo.appSchema should be set after SEO(&AppSchema{...})")
	}
}

func TestApp_SEO_OGDefaults(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("test-secret-key!1234567890abcde")})
	app.SEO(&OGDefaults{TwitterCreator: "@smeldr"})
	if app.seo.ogDefaults == nil {
		t.Error("seo.ogDefaults should be set after SEO(&OGDefaults{...})")
	}
}

// — DB mock helpers ——————————————————————————————————————————————————————————

// failOnNthExecDB passes ExecContext for calls 1..(failAt-1), then fails on call failAt.
type failOnNthExecDB struct {
	failAt int
	count  int
}

func (d *failOnNthExecDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	d.count++
	if d.count == d.failAt {
		return nil, errors.New("exec error on call " + strconv.Itoa(d.count))
	}
	return nil, nil
}
func (d *failOnNthExecDB) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, nil
}
func (d *failOnNthExecDB) QueryRowContext(ctx context.Context, _ string, _ ...any) *sql.Row {
	conn := &guardRowConn{noRow: true}
	return sql.OpenDB(conn).QueryRowContext(ctx, "SELECT v")
}

// errRowsAffectedResult is a sql.Result whose RowsAffected always returns an error.
type errRowsAffectedResult struct{}

func (errRowsAffectedResult) LastInsertId() (int64, error) { return 0, nil }
func (errRowsAffectedResult) RowsAffected() (int64, error) {
	return 0, errors.New("rows affected error")
}

// errRowsAffectedDB returns a result that errors on RowsAffected.
type errRowsAffectedDB struct{}

func (d *errRowsAffectedDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return errRowsAffectedResult{}, nil
}
func (d *errRowsAffectedDB) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, nil
}
func (d *errRowsAffectedDB) QueryRowContext(ctx context.Context, _ string, _ ...any) *sql.Row {
	conn := &guardRowConn{noRow: true}
	return sql.OpenDB(conn).QueryRowContext(ctx, "SELECT v")
}

// zeroRowsResult is a sql.Result returning 0 RowsAffected without error.
type zeroRowsResult struct{}

func (zeroRowsResult) LastInsertId() (int64, error) { return 0, nil }
func (zeroRowsResult) RowsAffected() (int64, error) { return 0, nil }

// zeroRowsExecDB returns 0 RowsAffected from ExecContext.
type zeroRowsExecDB struct{}

func (d *zeroRowsExecDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return zeroRowsResult{}, nil
}
func (d *zeroRowsExecDB) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, nil
}
func (d *zeroRowsExecDB) QueryRowContext(ctx context.Context, _ string, _ ...any) *sql.Row {
	conn := &guardRowConn{noRow: true}
	return sql.OpenDB(conn).QueryRowContext(ctx, "SELECT v")
}

// — scriptBlock ———————————————————————————————————————————————————————————

func TestScriptBlock_marshalError(t *testing.T) {
	result := scriptBlock(make(chan int))
	if result != "" {
		t.Errorf("scriptBlock with unmarshalable value: got %q, want empty string", result)
	}
}

// — parseOneTemplate ——————————————————————————————————————————————————————

func TestParseOneTemplate_parseFilesError(t *testing.T) {
	f, err := os.CreateTemp("", "*.html")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString(`{{invalid template`)
	f.Close()
	_, err = parseOneTemplate(f.Name(), true, nil)
	if err == nil {
		t.Error("expected parse error for invalid template syntax")
	}
}

func TestParseOneTemplate_invalidPartial(t *testing.T) {
	f, err := os.CreateTemp("", "*.html")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString(`<p>valid</p>`)
	f.Close()
	_, err = parseOneTemplate(f.Name(), true, []string{`{{invalid`})
	if err == nil {
		t.Error("expected error for invalid partial source")
	}
}

// — parseBlockTemplates ———————————————————————————————————————————————————

func TestParseBlockTemplates_dirNotFound(t *testing.T) {
	_, err := parseBlockTemplates("non-existent-dir-xyz-block")
	if err == nil {
		t.Error("expected error for non-existent dir")
	}
}

func TestParseBlockTemplates_parseError(t *testing.T) {
	dir, err := os.MkdirTemp("", "blocks-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	f, _ := os.Create(filepath.Join(dir, "myblock.html"))
	f.WriteString("{{invalid")
	f.Close()
	_, err = parseBlockTemplates(dir)
	if err == nil {
		t.Error("expected parse error for invalid block template")
	}
}

// — CreateBlockTables error paths ————————————————————————————————————————

func TestCreateBlockTables_secondExecFails(t *testing.T) {
	err := CreateBlockTables(&failOnNthExecDB{failAt: 2})
	if err == nil {
		t.Error("expected error when 2nd ExecContext fails")
	}
}

func TestCreateBlockTables_thirdExecFails(t *testing.T) {
	err := CreateBlockTables(&failOnNthExecDB{failAt: 3})
	if err == nil {
		t.Error("expected error when 3rd ExecContext fails")
	}
}

// — SchemaStore error paths ———————————————————————————————————————————————

func TestSchemaStore_FindByTypeName_queryError(t *testing.T) {
	store := NewSchemaStore(&errQueryDB{})
	_, err := store.FindByTypeName(context.Background(), "anything")
	if err == nil {
		t.Error("expected error from query failure")
	}
}

func TestSchemaStore_All_queryError(t *testing.T) {
	store := NewSchemaStore(&errQueryDB{})
	_, err := store.All(context.Background())
	if err == nil {
		t.Error("expected error from query failure")
	}
}

// — ContentEdgeStore error paths ——————————————————————————————————————————

func TestContentEdgeStore_Children_queryError(t *testing.T) {
	store := NewContentEdgeStore(&errQueryDB{})
	_, err := store.Children(context.Background(), "parent-id")
	if err == nil {
		t.Error("expected error from query failure")
	}
}

func TestContentEdgeStore_RemoveChild_rowsAffectedError(t *testing.T) {
	store := NewContentEdgeStore(&errRowsAffectedDB{})
	err := store.RemoveChild(context.Background(), "parent-id", "child-id")
	if err == nil {
		t.Error("expected error from RowsAffected failure")
	}
}

// — SQLRepo.Delete RowsAffected error path ————————————————————————————————

func TestSQLRepo_Delete_rowsAffectedError(t *testing.T) {
	repo := &SQLRepo[*testPost]{db: &errRowsAffectedDB{}, table: "testposts"}
	err := repo.Delete(context.Background(), "any-id")
	if err == nil {
		t.Error("expected error from RowsAffected failure in Delete")
	}
}

// — SQLRepo.countByStatus error path ——————————————————————————————————————

func TestSQLRepo_countByStatus_queryError(t *testing.T) {
	repo := &SQLRepo[*testPost]{db: &errQueryDB{}, table: "testposts"}
	_, err := repo.countByStatus(context.Background())
	if err == nil {
		t.Error("expected error from query failure in countByStatus")
	}
}

// — ListOptions.Offset negative ————————————————————————————————————————————

func TestListOptions_Offset_negativeResult(t *testing.T) {
	o := ListOptions{Page: 2, PerPage: -5}
	off := o.Offset()
	if off != 0 {
		t.Errorf("Offset() = %d, want 0 for negative result", off)
	}
}

// — workerPool error paths ——————————————————————————————————————————————————

func TestWorkerPool_ListDeliveryLogs_queryError(t *testing.T) {
	store := NewWebhookStore(nil, []byte("test-key-32bytes-xxxxxxxxxxxx!!!"))
	pool := newWorkerPool(&errQueryDB{}, store, realClock{}, 1)
	_, err := pool.ListDeliveryLogs(context.Background(), "job-1")
	if err == nil {
		t.Error("expected error from query failure in ListDeliveryLogs")
	}
}

func TestWorkerPool_ListJobsForEndpoint_queryError(t *testing.T) {
	store := NewWebhookStore(nil, []byte("test-key-32bytes-xxxxxxxxxxxx!!!"))
	pool := newWorkerPool(&errQueryDB{}, store, realClock{}, 1)
	_, err := pool.ListJobsForEndpoint(context.Background(), "ep-1")
	if err == nil {
		t.Error("expected error from query failure in ListJobsForEndpoint")
	}
}

func TestWorkerPool_DeliveryStats_scanError(t *testing.T) {
	store := NewWebhookStore(nil, []byte("test-key-32bytes-xxxxxxxxxxxx!!!"))
	pool := newWorkerPool(&errQueryDB{}, store, realClock{}, 1)
	_, _, _, _, err := pool.DeliveryStats(context.Background(), "ep-1")
	if err == nil {
		t.Error("expected error from scan failure in DeliveryStats")
	}
}

// — NavTree error paths ————————————————————————————————————————————————————

func TestNavTree_migrate_execError(t *testing.T) {
	tree := &NavTree{}
	err := tree.migrate(context.Background(), &errExecDB{})
	if err == nil {
		t.Error("expected error from ExecContext failure in migrate")
	}
}

func TestNavTree_load_queryError(t *testing.T) {
	tree := &NavTree{}
	err := tree.load(context.Background(), &errQueryDB{})
	if err == nil {
		t.Error("expected error from QueryContext failure in load")
	}
}

func TestNavTree_List_empty(t *testing.T) {
	tree := &NavTree{}
	result := tree.List()
	if result != nil {
		t.Errorf("List() on empty NavTree: got %v, want nil", result)
	}
}

func TestNavTree_Create_execError(t *testing.T) {
	tree := &NavTree{db: &errExecDB{}}
	_, err := tree.Create(context.Background(), NavItem{Label: "Test"})
	if err == nil {
		t.Error("expected error from ExecContext failure in Create")
	}
}

func TestNavTree_Update_execError(t *testing.T) {
	tree := &NavTree{db: &errExecDB{}}
	_, err := tree.Update(context.Background(), NavItem{ID: "x", Label: "Test"})
	if err == nil {
		t.Error("expected error from ExecContext failure in Update")
	}
}

func TestNavTree_Update_zeroAffected(t *testing.T) {
	tree := &NavTree{db: &zeroRowsExecDB{}}
	_, err := tree.Update(context.Background(), NavItem{ID: "x", Label: "Test"})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for 0 rows affected, got %v", err)
	}
}

// — coerceSliceFields empty parts ——————————————————————————————————————————

func TestCoerceSliceFields_emptyParts(t *testing.T) {
	type ts struct {
		Tags []string `json:"tags"`
	}
	fields := map[string]any{"tags": "a,,b"}
	coerceSliceFields(fields, reflect.TypeOf(ts{}))
	got, ok := fields["tags"].([]any)
	if !ok {
		t.Fatal("expected []any after coercion")
	}
	if len(got) != 2 {
		t.Errorf("got %d parts, want 2 (empty parts filtered)", len(got))
	}
}

// — stringField edge cases ——————————————————————————————————————————————————

func TestStringField_nonStruct(t *testing.T) {
	result := stringField[string]("hello", "any")
	if result != "" {
		t.Errorf("stringField(string, ...) = %q, want empty", result)
	}
}

func TestStringField_fieldNotFound(t *testing.T) {
	type simple struct{ Name string }
	result := stringField[simple](simple{Name: "test"}, "NonExistentField")
	if result != "" {
		t.Errorf("stringField with missing field = %q, want empty", result)
	}
}

func TestStringField_nonStringField(t *testing.T) {
	type withInt struct{ Count int }
	result := stringField[withInt](withInt{Count: 5}, "count")
	if result != "" {
		t.Errorf("stringField with int field = %q, want empty", result)
	}
}

// — Markdown helpers ——————————————————————————————————————————————————————————

func TestMdApplyLinks_unsafeScheme(t *testing.T) {
	result := mdApplyLinks("[click](javascript:alert(1))")
	if strings.Contains(result, "<a") {
		t.Errorf("unsafe URL scheme should not produce <a> tag, got: %q", result)
	}
	if !strings.Contains(result, "[click]") {
		t.Errorf("unsafe URL should pass through unchanged, got: %q", result)
	}
}

func TestMdApplyBold_unclosed(t *testing.T) {
	result := mdApplyBold("unclosed **bold")
	if strings.Contains(result, "<strong>") {
		t.Errorf("unclosed bold should not produce <strong>, got: %q", result)
	}
}

func TestMdApplyCode_unclosed(t *testing.T) {
	result := mdApplyCode("unclosed `code")
	if strings.Contains(result, "<code>") {
		t.Errorf("unclosed code should not produce <code>, got: %q", result)
	}
}

// — extractNode no Node field ——————————————————————————————————————————————

func TestExtractNode_noNodeField(t *testing.T) {
	type noNode struct{ Title string }
	n := extractNode(&noNode{Title: "test"})
	if n.ID != "" {
		t.Errorf("extractNode without Node field: got ID=%q, want empty Node", n.ID)
	}
}

// — HasRole / roleBuilder.Below ————————————————————————————————————————————

func TestHasRole_unknownRequired(t *testing.T) {
	result := HasRole([]Role{Admin}, Role("unknown-role-xyz-not-registered"))
	if result {
		t.Error("HasRole with unknown required role should return false")
	}
}

func TestRoleBuilder_Below_clampToMinimum(t *testing.T) {
	rb := NewRole("test-clamped-below").Below(Guest)
	if rb.level < 1 {
		t.Errorf("roleBuilder.Below(Guest).level = %d, want >= 1", rb.level)
	}
}

// — capitalisePrefixTitle empty prefix ————————————————————————————————————

func TestCapitalisePrefixTitle_slashOnly(t *testing.T) {
	result := capitalisePrefixTitle("/")
	if result != "/" {
		t.Errorf("capitalisePrefixTitle(\"/\") = %q, want \"/\"", result)
	}
}

// — decryptSecret error paths ————————————————————————————————————————————

func TestWebhookStore_decryptSecret_badBase64(t *testing.T) {
	store := NewWebhookStore(nil, []byte("test-key-32bytes-xxxxxxxxxxxx!!!"))
	_, err := store.decryptSecret("!!!not-valid-base64!!!")
	if err == nil {
		t.Error("expected error for invalid base64 in decryptSecret")
	}
}

func TestWebhookStore_decryptSecret_shortCiphertext(t *testing.T) {
	store := NewWebhookStore(nil, []byte("test-key-32bytes-xxxxxxxxxxxx!!!"))
	// "short" base64-encoded is 5 bytes, less than the GCM nonce size of 12.
	shortEnc := "c2hvcnQ="
	_, err := store.decryptSecret(shortEnc)
	if err == nil {
		t.Error("expected error for too-short ciphertext")
	}
}

// — MCPSchema with anonymous non-Node embed ————————————————————————————————

func TestMCPSchema_anonymousNonNodeEmbed(t *testing.T) {
	type embedded struct{ Extra string }
	type post struct {
		Node
		embedded
		Title string
	}
	m := NewModule((*post)(nil), Repo(NewMemoryRepo[*post]()))
	fields := m.MCPSchema()
	for _, f := range fields {
		if f.Name == "Extra" {
			t.Error("MCPSchema should skip anonymous non-Node embedded fields")
		}
	}
}

// — CacheStore eviction (covers set.evict and unlink tail) ———————————————

func TestCacheStore_eviction(t *testing.T) {
	cache := NewCacheStore(time.Minute, 2)
	exp := time.Now().Add(time.Minute)
	cache.mu.Lock()
	cache.set(&cacheEntry{key: "a", body: []byte("a"), status: 200, expires: exp})
	cache.set(&cacheEntry{key: "b", body: []byte("b"), status: 200, expires: exp})
	cache.set(&cacheEntry{key: "c", body: []byte("c"), status: 200, expires: exp})
	count := cache.count
	cache.mu.Unlock()
	if count != 2 {
		t.Errorf("count = %d after adding 3 to max-2 cache, want 2", count)
	}
}

// — App.Handler NavModeDB path ————————————————————————————————————————————

func TestApp_Handler_navModeDB_success(t *testing.T) {
	db := newSQLiteDB(t)
	mem := NewMemoryRepo[*testPost]()
	m := newTestModule(mem)

	app := New(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("testsecret16chars"),
		NavMode: NavModeDB,
		DB:      db,
	})
	app.Content(m)
	_ = app.Handler()
	if app.navTree == nil {
		t.Error("navTree should be non-nil after Handler() with NavModeDB")
	}
}

func TestApp_Handler_navModeDB_migrateError(t *testing.T) {
	app := New(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("testsecret16chars"),
		NavMode: NavModeDB,
		DB:      &errExecDB{},
	})
	_ = app.Handler()
	if app.navTree != nil {
		t.Error("navTree should be nil after migrate error")
	}
}

// — mock: ExecContext succeeds, QueryContext fails ——————————————————————————

type oneRowResult struct{}

func (oneRowResult) LastInsertId() (int64, error) { return 0, nil }
func (oneRowResult) RowsAffected() (int64, error) { return 1, nil }

type oneRowExecQueryErrDB struct{}

func (oneRowExecQueryErrDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return oneRowResult{}, nil
}
func (oneRowExecQueryErrDB) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, errors.New("query error")
}
func (oneRowExecQueryErrDB) QueryRowContext(ctx context.Context, _ string, _ ...any) *sql.Row {
	conn := &guardRowConn{noRow: true}
	return sql.OpenDB(conn).QueryRowContext(ctx, "SELECT v")
}

// — mock: schedulable module that always errors ————————————————————————————

type errSchedulableModule struct{}

func (m *errSchedulableModule) processScheduled(_ Context, _ time.Time) (int, *time.Time, error) {
	return 0, nil, errors.New("scheduled error")
}

// — isOption marker methods — 28 empty bodies —————————————————————————————

func TestIsOption_allTypes_coverage(t *testing.T) {
	aiIndexOption{}.isOption()
	withoutIDOption{}.isOption()
	withoutCSRFOption{}.isOption()
	manifestAuthOption{}.isOption()
	feedOption{}.isOption()
	feedDisabledOption{}.isOption()
	headFuncOption[*testPost]{}.isOption()
	listHeadFuncOption[*testPost]{}.isOption()
	mcpOption{}.isOption()
	trustedProxyOption{}.isOption()
	cacheMaxEntriesOption{}.isOption()
	atOption{}.isOption()
	moduleCacheOption{}.isOption()
	middlewareModuleOption{}.isOption()
	authOption{}.isOption()
	repoOption[*testPost]{}.isOption()
	contextFuncOption{}.isOption()
	singleInstanceOption{}.isOption()
	standaloneOption{}.isOption()
	blockHostOption{}.isOption()
	apiOnlyOption{}.isOption()
	redirectsOption{}.isOption()
	roleOption{}.isOption()
	signalOption{}.isOption()
	SitemapConfig{}.isOption()
	socialOption{}.isOption()
	tableOption{}.isSQLRepoOption()
	templatesOption{}.isOption()
}

// — CacheStore: reinsert HEAD entry (covers c.head = e.next in unlink) ————

func TestCacheStore_reinsertHead(t *testing.T) {
	cache := NewCacheStore(time.Minute, 3)
	exp := time.Now().Add(time.Minute)
	cache.mu.Lock()
	cache.set(&cacheEntry{key: "a", body: []byte("a"), status: 200, expires: exp})
	cache.set(&cacheEntry{key: "b", body: []byte("b"), status: 200, expires: exp})
	// "b" is now the HEAD. Re-inserting "b" calls unlink(b) where b.prev == nil.
	cache.set(&cacheEntry{key: "b", body: []byte("b2"), status: 200, expires: exp})
	cache.mu.Unlock()
}

// — App.ServeBlocks nil DB and CreateBlockTables error ————————————————————

func TestApp_ServeBlocks_nilDB(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("testsecret16chars")})
	_, err := app.ServeBlocks(t.TempDir())
	if err == nil {
		t.Error("expected error when Config.DB is nil")
	}
}

func TestApp_ServeBlocks_createTablesError(t *testing.T) {
	app := New(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("testsecret16chars"),
		DB:      &failOnNthExecDB{failAt: 1},
	})
	_, err := app.ServeBlocks(t.TempDir())
	if err == nil {
		t.Error("expected error when CreateBlockTables fails")
	}
}

// — newStatsHandler: non-admin user gets 403 ——————————————————————————————

func TestNewStatsHandler_forbidden(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("testsecret16chars")})
	auth := BearerHMAC(string(app.cfg.Secret))
	h := newStatsHandler(auth, app)

	tok, err := SignToken(User{ID: "u1", Name: "alice", Roles: []Role{Author}}, string(app.cfg.Secret), time.Hour)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/_stats", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	h.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for non-admin user", w.Code)
	}
}

// — renderListHTML: listHeadFunc branch ————————————————————————————————————

func TestModule_renderListHTML_listHeadFunc(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := newTestModule(repo)

	tpl, err := template.New("list").Parse(`<ul>{{range .Content}}<li>ok</li>{{end}}</ul>`)
	if err != nil {
		t.Fatalf("template parse: %v", err)
	}
	m.tplList = tpl
	m.neg.html = true
	m.listHeadFunc = func(ctx Context, items []*testPost) Head {
		return Head{Title: "List Head"}
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/posts", nil)
	r.Header.Set("Accept", "text/html")
	ctx := ContextFrom(w, r)
	m.renderListHTML(w, r, ctx, []*testPost{})

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// — NavTree.Create load error after successful INSERT ——————————————————————

func TestNavTree_Create_loadError(t *testing.T) {
	n := &NavTree{db: oneRowExecQueryErrDB{}}
	ctx := context.Background()
	_, err := n.Create(ctx, NavItem{Label: "test"})
	if err == nil {
		t.Error("expected error from load after successful INSERT")
	}
}

// — NavTree.Update load error after successful UPDATE —————————————————————

func TestNavTree_Update_loadError(t *testing.T) {
	n := &NavTree{db: oneRowExecQueryErrDB{}}
	ctx := context.Background()
	_, err := n.Update(ctx, NavItem{ID: "some-id", Label: "test"})
	if err == nil {
		t.Error("expected error from load after successful UPDATE")
	}
}

// — autoSlug returns "" when no suitable field ————————————————————————————

func TestAutoSlug_noSuitableField(t *testing.T) {
	type noFields struct {
		Node
		Count int
	}
	rv := reflect.ValueOf(noFields{})
	got := autoSlug(rv)
	if got != "" {
		t.Errorf("autoSlug = %q, want \"\" for struct with no suitable string field", got)
	}
}

// — Scheduler.tick logs warn when module returns error ————————————————————

func TestScheduler_tick_moduleError(t *testing.T) {
	bgCtx := NewContextWithUser(GuestUser)
	s := newScheduler([]schedulableModule{&errSchedulableModule{}}, bgCtx)
	s.tick()
}

// — mcpGoTypeStr: reflect.Bool → "boolean" ————————————————————————————————

func TestMCPGoTypeStr_boolType(t *testing.T) {
	got := mcpGoTypeStr(reflect.TypeOf(false))
	if got != "boolean" {
		t.Errorf("mcpGoTypeStr(bool) = %q, want \"boolean\"", got)
	}
}

// — newWorkerPool: workers ≤ 0 defaults to 10 —————————————————————————————

func TestNewWorkerPool_defaultWorkers(t *testing.T) {
	p := newWorkerPool(nil, nil, realClock{}, 0)
	if p.workers != 10 {
		t.Errorf("workers = %d, want 10 when passed 0", p.workers)
	}
}

// — findAndServe preview bypass: HTML Accept header ————————————————————————

func TestModule_findAndServe_previewBypass_html(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	draft := seedPost(t, repo, "Draft Item", Draft)

	const prefix = "/posts"
	m := newTestModule(repo)
	m.secret = []byte(testSecret)
	m.prefix = prefix
	m.neg.html = true

	token := encodePreviewToken(prefix, draft.Slug, []byte(testSecret), time.Hour)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/"+draft.Slug+"?preview="+token, nil)
	r.Header.Set("Accept", "text/html")
	served := m.findAndServe(w, r, draft.Slug)

	if !served {
		t.Error("findAndServe should return true for valid HTML preview token")
	}
	// No tplShow template → 406 Not Acceptable.
	if w.Code != http.StatusNotAcceptable {
		t.Errorf("status = %d, want 406 (no show template)", w.Code)
	}
}

// — roleBuilder.Below clamps level to minimum 1 ———————————————————————————

func TestRoleBuilder_Below_clampsToMin(t *testing.T) {
	rb := NewRole("test-clamp-role").Below(Role("totally-unregistered-xyz"))
	if rb.level != 1 {
		t.Errorf("level = %d, want 1 after clamping below unregistered role", rb.level)
	}
}

// — ContentEdgeStore.AddChild with IsShared=true ————————————————————————————

func TestContentEdgeStore_AddChild_isShared(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateBlockTables(db); err != nil {
		t.Fatalf("CreateBlockTables: %v", err)
	}
	store := NewContentEdgeStore(db)
	ctx := context.Background()
	e, err := store.AddChild(ctx, ContentEdge{
		ParentID:   "parent-1",
		ParentType: "post",
		ChildID:    "child-1",
		ChildType:  "block",
		IsShared:   true,
	})
	if err != nil {
		t.Fatalf("AddChild: %v", err)
	}
	if !e.IsShared {
		t.Error("expected IsShared=true in returned edge")
	}
}

// — backoffDelay: attempt ≤ 0 returns 1s immediately ——————————————————————

func TestBackoffDelay_zeroAttempt(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	got := backoffDelay(0, rng)
	if got != time.Second {
		t.Errorf("backoffDelay(0) = %v, want 1s", got)
	}
}

// — mock: ExecContext fails, QueryRowContext returns int64(0) —————————————

type execErrQueryOKDB struct{}

func (d execErrQueryOKDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return nil, errors.New("exec error")
}
func (d execErrQueryOKDB) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, nil
}
func (d execErrQueryOKDB) QueryRowContext(ctx context.Context, _ string, _ ...any) *sql.Row {
	conn := &guardRowConn{val: int64(0)}
	return sql.OpenDB(conn).QueryRowContext(ctx, "SELECT v")
}

// — renderListHTML: nil template → 406 ——————————————————————————————————

func TestModule_renderListHTML_nilTemplate(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := newTestModule(repo)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/testposts", nil)
	ctx := ContextFrom(w, r)
	m.renderListHTML(w, r, ctx, []*testPost{})

	if w.Code != http.StatusNotAcceptable {
		t.Errorf("status = %d, want 406 for nil tplList", w.Code)
	}
}

// — renderListHTML: navTree non-nil → data.Nav set ——————————————————————

func TestModule_renderListHTML_withNavTree(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := newTestModule(repo)

	tpl, err := template.New("list").Parse(`<ul>{{range .Content}}<li>ok</li>{{end}}</ul>`)
	if err != nil {
		t.Fatalf("template.Parse: %v", err)
	}
	m.tplList = tpl
	m.navTree = &NavTree{}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/testposts", nil)
	ctx := ContextFrom(w, r)
	m.renderListHTML(w, r, ctx, []*testPost{})

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// — renderListHTML: template Execute error → 500 —————————————————————————

func TestModule_renderListHTML_templateError(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := newTestModule(repo)

	tpl, err := template.New("list").Funcs(template.FuncMap{
		"fail": func() (string, error) { return "", errors.New("intentional fail") },
	}).Parse(`{{fail}}`)
	if err != nil {
		t.Fatalf("template.Parse: %v", err)
	}
	m.tplList = tpl

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/testposts", nil)
	ctx := ContextFrom(w, r)
	m.renderListHTML(w, r, ctx, []*testPost{})

	if w.Code == http.StatusOK {
		t.Error("expected non-200 response when template execution fails")
	}
}

// — renderShowHTML: navTree non-nil → data.Nav set ——————————————————————

func TestModule_renderShowHTML_withNavTree(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := newTestModule(repo)

	tpl, err := template.New("show").Parse(`<div>ok</div>`)
	if err != nil {
		t.Fatalf("template.Parse: %v", err)
	}
	m.tplShow = tpl
	m.navTree = &NavTree{}

	item := &testPost{Node: Node{ID: "1", Slug: "test", Status: Published}, Title: "Test"}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/testposts/test", nil)
	ctx := ContextFrom(w, r)
	m.renderShowHTML(w, r, ctx, item)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// — renderShowHTML: template Execute error → 500 —————————————————————————

func TestModule_renderShowHTML_templateError(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := newTestModule(repo)

	tpl, err := template.New("show").Funcs(template.FuncMap{
		"fail": func() (string, error) { return "", errors.New("intentional fail") },
	}).Parse(`{{fail}}`)
	if err != nil {
		t.Fatalf("template.Parse: %v", err)
	}
	m.tplShow = tpl

	item := &testPost{Node: Node{ID: "1", Slug: "test", Status: Published}, Title: "Test"}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/testposts/test", nil)
	ctx := ContextFrom(w, r)
	m.renderShowHTML(w, r, ctx, item)

	if w.Code == http.StatusOK {
		t.Error("expected non-200 when show template execution fails")
	}
}

// — errorTemplate: invalid syntax → nil —————————————————————————————————

func TestModule_errorTemplate_invalidSyntax(t *testing.T) {
	dir := t.TempDir()
	errorsDir := filepath.Join(dir, "errors")
	if err := os.MkdirAll(errorsDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(errorsDir, "404.html"), []byte("{{invalid"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	m := newTestModule(NewMemoryRepo[*testPost]())
	m.templateDir = dir
	if result := m.errorTemplate(404); result != nil {
		t.Error("expected nil for invalid template syntax in errorTemplate")
	}
}

// — showHandler: preview bypass with Accept: text/html → renderShowHTML ——

func TestModule_showHandler_previewBypass_html(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	draft := seedPost(t, repo, "Draft Post", Draft)

	const prefix = "/posts"
	m := newTestModule(repo)
	m.secret = []byte(testSecret)
	m.prefix = prefix
	m.neg.html = true

	token := encodePreviewToken(prefix, draft.Slug, []byte(testSecret), time.Hour)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, prefix+"/"+draft.Slug+"?preview="+token, nil)
	r.SetPathValue("slug", draft.Slug)
	r.Header.Set("Accept", "text/html")
	m.showHandler(w, r)

	// tplShow is nil → 406 (renderShowHTML was reached)
	if w.Code != http.StatusNotAcceptable {
		t.Errorf("status = %d, want 406 (no show template)", w.Code)
	}
}

// — singleInstanceHandler: preview bypass with Accept: text/html → renderShowHTML

func TestModule_singleInstanceHandler_previewBypass_html(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	draft := seedPost(t, repo, "Draft Single", Draft)

	const prefix = "/pages"
	m := newTestModule(repo)
	m.secret = []byte(testSecret)
	m.prefix = prefix
	m.neg.html = true

	token := encodePreviewToken(prefix, draft.Slug, []byte(testSecret), time.Hour)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, prefix+"?preview="+token, nil)
	r.Header.Set("Accept", "text/html")
	m.singleInstanceHandler(w, r)

	// tplShow is nil → 406 (renderShowHTML was reached)
	if w.Code != http.StatusNotAcceptable {
		t.Errorf("status = %d, want 406 (no show template)", w.Code)
	}
}

// — App.Handler: NavModeDB load error → navTree nil ————————————————————

func TestApp_Handler_navModeDB_loadError(t *testing.T) {
	app := New(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("testsecret16chars"),
		NavMode: NavModeDB,
		DB:      oneRowExecQueryErrDB{},
	})
	_ = app.Handler()
	if app.navTree != nil {
		t.Error("navTree should be nil after NavTree.load error")
	}
}

// — createHandler: MaxBytesReader → ErrRequestTooLarge ————————————————

func TestModule_createHandler_maxBytesError(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := newTestModule(repo)

	body := bytes.NewReader([]byte(`{"title":"a long enough title to exceed the limit"}`))
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/testposts", body)
	r.Body = http.MaxBytesReader(w, r.Body, 1)
	r = withUser(r, editorUser())
	m.createHandler(w, r)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413 for MaxBytesError in createHandler", w.Code)
	}
}

// — updateHandler: MaxBytesReader → ErrRequestTooLarge ————————————————

func TestModule_updateHandler_maxBytesError(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	existing := seedPost(t, repo, "Existing Post", Published)

	m := newTestModule(repo)

	body := bytes.NewReader([]byte(`{"title":"a long enough title to exceed the limit"}`))
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/testposts/"+existing.Slug, body)
	r.SetPathValue("slug", existing.Slug)
	r.Body = http.MaxBytesReader(w, r.Body, 1)
	r = withUser(r, editorUser())
	m.updateHandler(w, r)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413 for MaxBytesError in updateHandler", w.Code)
	}
}

// — NewModule panics: no Repo ————————————————————————————————————————————

func TestNewModule_panic_noRepo(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when NewModule has no Repo option")
		}
	}()
	_ = NewModule((*testPost)(nil))
}

// — NewModule panics: APIOnly + SingleInstance —————————————————————————

func TestNewModule_panic_apiOnlySingleInstance(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for APIOnly+SingleInstance combination")
		}
	}()
	_ = NewModule((*testPost)(nil), Repo(NewMemoryRepo[*testPost]()), APIOnly(), SingleInstance())
}

// — getNodeFields panics: type has no ID/Slug/Status ——————————————————

func TestGetNodeFields_panic_noNodeEmbed(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when type is missing ID/Slug/Status fields")
		}
	}()
	type noNodeStruct struct{ Title string }
	_ = getNodeFields(reflect.TypeOf(noNodeStruct{}))
}

// — workerPool.processJob: decrypt error → dead-letter immediately ————

func TestWorkerPool_processJob_decryptError(t *testing.T) {
	store := NewWebhookStore(nil, []byte("test-key-32bytes-xxxxxxxxxxxx!!!"))
	pool := newWorkerPool(&errExecDB{}, store, realClock{}, 1)
	job := OutboundJob{
		ID:         "job-1",
		EndpointID: "ep-1",
		SecretEnc:  "!!!invalid-base64!!!",
	}
	rng := rand.New(rand.NewSource(1))
	err := pool.processJob(context.Background(), job, rng)
	if err == nil {
		t.Error("expected error from decryptSecret failure in processJob")
	}
}

// — ContentEdgeStore.AddChild: QueryRowContext scan error ——————————————

func TestContentEdgeStore_AddChild_queryRowError(t *testing.T) {
	store := NewContentEdgeStore(oneRowExecQueryErrDB{})
	_, err := store.AddChild(context.Background(), ContentEdge{
		ParentID: "parent-1", ParentType: "post",
		ChildID: "child-1", ChildType: "block",
	})
	if err == nil {
		t.Error("expected error when QueryRowContext scan fails in AddChild")
	}
}

// — ContentEdgeStore.AddChild: ExecContext fails after successful scan ——

func TestContentEdgeStore_AddChild_execError(t *testing.T) {
	store := NewContentEdgeStore(execErrQueryOKDB{})
	_, err := store.AddChild(context.Background(), ContentEdge{
		ParentID: "parent-1", ParentType: "post",
		ChildID: "child-1", ChildType: "block",
	})
	if err == nil {
		t.Error("expected error when ExecContext fails in AddChild")
	}
}

// — ContentEdgeStore.ChildrenOf: QueryContext error ————————————————————

func TestContentEdgeStore_ChildrenOf_queryError(t *testing.T) {
	store := NewContentEdgeStore(&errQueryDB{})
	_, err := store.ChildrenOf(context.Background(), []string{"parent-1"})
	if err == nil {
		t.Error("expected error from QueryContext failure in ChildrenOf")
	}
}

// — ContentEdgeStore.RemoveChild: ExecContext error ————————————————————

func TestContentEdgeStore_RemoveChild_execError(t *testing.T) {
	store := NewContentEdgeStore(&errExecDB{})
	err := store.RemoveChild(context.Background(), "parent-1", "child-1")
	if err == nil {
		t.Error("expected error from ExecContext failure in RemoveChild")
	}
}

// — coerceSliceFields: pointer input → outer ptr loop fires ———————————

func TestCoerceSliceFields_ptrInput(t *testing.T) {
	type sWithTags struct {
		Tags []string `json:"tags"`
	}
	coerceSliceFields(map[string]any{}, reflect.TypeOf((*sWithTags)(nil)))
}

// — coerceSliceFields: non-struct input → early return ————————————————

func TestCoerceSliceFields_nonStruct(t *testing.T) {
	fields := map[string]any{"tags": "a,b"}
	coerceSliceFields(fields, reflect.TypeOf(""))
	if _, isStr := fields["tags"].(string); !isStr {
		t.Error("map should be unchanged for non-struct type")
	}
}

// — coerceSliceFields: *[]string field → inner ptr loop fires —————————

func TestCoerceSliceFields_ptrSliceField(t *testing.T) {
	type sWithPtrTags struct {
		Tags *[]string `json:"tags"`
	}
	coerceSliceFields(map[string]any{}, reflect.TypeOf(sWithPtrTags{}))
}

// — coerceSliceFields: key not in map → !ok continue ——————————————————

func TestCoerceSliceFields_keyNotInFields(t *testing.T) {
	type sWithSlice struct {
		Tags []string `json:"tags"`
	}
	fields := map[string]any{"other": "value"}
	coerceSliceFields(fields, reflect.TypeOf(sWithSlice{}))
	if _, ok := fields["tags"]; ok {
		t.Error("unexpected key 'tags' in map")
	}
}

// — coerceSliceFields: value not string → !isStr continue —————————————

func TestCoerceSliceFields_valueNotString(t *testing.T) {
	type sWithSlice struct {
		Tags []string `json:"tags"`
	}
	fields := map[string]any{"tags": 42}
	coerceSliceFields(fields, reflect.TypeOf(sWithSlice{}))
	if _, isInt := fields["tags"].(int); !isInt {
		t.Error("map should be unchanged when value is not a string")
	}
}

// — MCPSchema: *string field → inner ptr loop fires ————————————————————

func TestMCPSchema_pointerField(t *testing.T) {
	type postWithPtrField struct {
		Node
		Description *string `json:"description"`
	}
	m := NewModule((*postWithPtrField)(nil), Repo(NewMemoryRepo[*postWithPtrField]()))
	fields := m.MCPSchema()
	found := false
	for _, f := range fields {
		if f.Name == "Description" {
			found = true
		}
	}
	if !found {
		t.Error("MCPSchema should include *string field 'Description'")
	}
}

// — MCPSchema: exported anonymous non-Node embed → skip ——————————————

func TestMCPSchema_exportedAnonymousNonNodeEmbed(t *testing.T) {
	type ExtraData struct{ Value string }
	type postWithExtraEmbed struct {
		Node
		ExtraData
		Title string
	}
	m := NewModule((*postWithExtraEmbed)(nil), Repo(NewMemoryRepo[*postWithExtraEmbed]()))
	for _, f := range m.MCPSchema() {
		if f.Name == "Value" {
			t.Error("MCPSchema should skip exported anonymous non-Node embedded fields")
		}
	}
}

// — RunValidation: non-struct panics ——————————————————————————————————

func TestRunValidation_nonStruct(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for non-struct input to RunValidation")
		}
	}()
	_ = RunValidation("not a struct")
}

// — parseConstraints: bad min= tag → panic ————————————————————————————

func TestParseConstraints_badMinTag(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for invalid min= tag value")
		}
	}()
	type badMin struct {
		Node
		Name string `smeldr:"min=abc"`
	}
	_ = RunValidation(&badMin{Node: Node{ID: "1", Slug: "x", Status: Draft}})
}

// — parseConstraints: bad max= tag → panic ————————————————————————————

func TestParseConstraints_badMaxTag(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for invalid max= tag value")
		}
	}()
	type badMax struct {
		Node
		Name string `smeldr:"max=abc"`
	}
	_ = RunValidation(&badMax{Node: Node{ID: "1", Slug: "x", Status: Draft}})
}

// — parseConstraints: empty tag part → continue (no panic) ————————————

func TestParseConstraints_emptyTagPart(t *testing.T) {
	type trailingComma struct {
		Node
		Title string `smeldr:"required,"`
	}
	v := &trailingComma{Node: Node{ID: "1", Slug: "x", Status: Draft}, Title: "Hello"}
	if err := RunValidation(v); err != nil {
		t.Errorf("unexpected validation error for empty tag part: %v", err)
	}
}

// — NavTree.List: different SortOrder → comparator fires ——————————————

func TestNavTree_List_sortOrder(t *testing.T) {
	n := &NavTree{}
	n.mu.Lock()
	n.buildTree([]NavItem{
		{ID: "a", Label: "A", SortOrder: 2},
		{ID: "b", Label: "B", SortOrder: 1},
	})
	n.mu.Unlock()
	items := n.List()
	if len(items) != 2 {
		t.Fatalf("List() returned %d items, want 2", len(items))
	}
	if items[0].ID != "b" {
		t.Errorf("first item = %q, want \"b\" (lower SortOrder)", items[0].ID)
	}
}

// — NavTree.Delete: ExecContext error after flat populated ————————————

func TestNavTree_Delete_execError(t *testing.T) {
	n := &NavTree{db: &errExecDB{}}
	n.mu.Lock()
	n.buildTree([]NavItem{{ID: "x", Label: "X", SortOrder: 1}})
	n.mu.Unlock()
	err := n.Delete(context.Background(), "x")
	if err == nil {
		t.Error("expected error from ExecContext failure in NavTree.Delete")
	}
}

// — App.Handler with TokenStore: probeTable succeeds → ensureBootstrap ——

func TestApp_Handler_withTokenStore(t *testing.T) {
	db := newSQLiteDB(t)
	_, err := db.ExecContext(context.Background(), `
		CREATE TABLE smeldr_tokens (
			id         TEXT NOT NULL PRIMARY KEY,
			name       TEXT NOT NULL,
			role       TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			revoked_at TEXT,
			created_at TEXT NOT NULL
		)`)
	if err != nil {
		t.Fatalf("create smeldr_tokens table: %v", err)
	}
	ts := NewTokenStore(db, testSecret)
	app := New(Config{
		BaseURL:    "https://example.com",
		Secret:     []byte(testSecret),
		TokenStore: ts,
	})
	_ = app.Handler()
}

// — ptrToT: non-pointer T uses pv.Elem() branch ———————————————————————

func TestPtrToT_nonPointer(t *testing.T) {
	type simplePT struct {
		Node
		Name string
	}
	pv := reflect.New(reflect.TypeOf(simplePT{}))
	_ = ptrToT[simplePT](pv, reflect.TypeOf(simplePT{}))
}

// — updateHandler: Published → Scheduled fires AfterSchedule —————————

func TestModule_updateHandler_scheduleTransition(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	existing := seedPost(t, repo, "My Post", Published)

	m := newTestModule(repo)

	body, _ := json.Marshal(map[string]any{
		"title":  "My Post",
		"status": string(Scheduled),
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/testposts/"+existing.Slug, bytes.NewReader(body))
	r.SetPathValue("slug", existing.Slug)
	r = withUser(r, editorUser())
	m.updateHandler(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for Published→Scheduled transition", w.Code)
	}
}

// — processScheduled: nil ScheduledAt → continue ——————————————————————

func TestModule_processScheduled_nilScheduledAt(t *testing.T) {
	type scheduledPost struct {
		Node
		Title       string     `smeldr:"required"`
		ScheduledAt *time.Time // nil → processScheduled skips via fv.IsNil()
	}
	repo := NewMemoryRepo[*scheduledPost]()
	p := &scheduledPost{
		Node:  Node{ID: NewID(), Slug: "sched-nil", Status: Scheduled},
		Title: "Scheduled Item",
	}
	if err := repo.Save(context.Background(), p); err != nil {
		t.Fatalf("Save: %v", err)
	}
	m := NewModule((*scheduledPost)(nil), Repo(repo))
	ctx := NewContextWithUser(GuestUser)
	count, _, err := m.processScheduled(ctx, time.Now())
	if err != nil {
		t.Errorf("processScheduled: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0 (nil ScheduledAt skipped)", count)
	}
}

// — RedirectStore.Add: two prefix entries → sort comparator fires ————

func TestRedirectStore_Add_prefixSorts(t *testing.T) {
	s := NewRedirectStore()
	s.Add(RedirectEntry{From: "/old", To: "/new", Code: Permanent, IsPrefix: true})
	s.Add(RedirectEntry{From: "/older-path", To: "/new2", Code: Permanent, IsPrefix: true})
	if len(s.prefix) != 2 {
		t.Fatalf("prefix len = %d, want 2", len(s.prefix))
	}
	if s.prefix[0].From != "/older-path" {
		t.Errorf("first prefix = %q, want \"/older-path\" (longer)", s.prefix[0].From)
	}
}

// — RedirectStore.Load: query error ————————————————————————————————————

func TestRedirectStore_Load_queryError(t *testing.T) {
	s := NewRedirectStore()
	if err := s.Load(context.Background(), &errQueryDB{}); err == nil {
		t.Error("expected error from query failure in RedirectStore.Load")
	}
}

// — renderMarkdown: unterminated code fence → flushCode called ————————

func TestRenderMarkdown_unterminatedCodeFence(t *testing.T) {
	result := renderMarkdown("```\nsome code")
	if !strings.Contains(string(result), "some code") {
		t.Errorf("unterminated fence: expected content in output, got %q", result)
	}
}

// — mdApplyLinks: no "](" → b.WriteString(s) + break —————————————————

func TestMdApplyLinks_noMid(t *testing.T) {
	input := "[no close bracket paren"
	if got := mdApplyLinks(input); got != input {
		t.Errorf("noMid: got %q, want %q", got, input)
	}
}

// — mdApplyLinks: no ")" → b.WriteString(s) + break ——————————————————

func TestMdApplyLinks_noClose(t *testing.T) {
	input := "[text](url without paren"
	if got := mdApplyLinks(input); got != input {
		t.Errorf("noClose: got %q, want %q", got, input)
	}
}

// — Module.listPublished ————————————————————————————————————————————————————

func TestModule_listPublished_returnsPublishedItems(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := newTestModule(repo)

	ctx := context.Background()
	pub := &testPost{Node: Node{ID: NewID(), Slug: "p1", Status: Published}, Title: "Pub"}
	drf := &testPost{Node: Node{ID: NewID(), Slug: "p2", Status: Draft}, Title: "Dft"}
	for _, p := range []*testPost{pub, drf} {
		if err := repo.Save(ctx, p); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	items, err := m.listPublished(ctx, ListOptions{})
	if err != nil {
		t.Fatalf("listPublished: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("want 1 published item, got %d", len(items))
	}
	if items[0]["Title"] != "Pub" {
		t.Errorf("want Title=Pub, got %v", items[0]["Title"])
	}
}

// — collectDBFields: unexported field and db:"-" tag ——————————————————————

type testStructWithPrivateField struct {
	ID   string `db:"id"`
	name string // unexported, no db tag
}

type testStructWithDashTag struct {
	ID      string `db:"id"`
	Ignored string `db:"-"`
}

// Covers storage.go:61-62 (unexported field continue) and 76-77 (db:"-" continue).
func TestCollectDBFields_SkipsUnexportedAndDashTag(t *testing.T) {
	db := newSQLiteDB(t)
	_, err := db.ExecContext(context.Background(), "CREATE TABLE tmp (id TEXT)")
	if err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}
	// Reference the unexported field so the linter does not flag it as unused.
	_ = testStructWithPrivateField{name: "x"}
	// Query with a struct that has an unexported field → covers lines 61-62.
	rows1, err := Query[*testStructWithPrivateField](context.Background(), db, "SELECT id FROM tmp")
	if err != nil {
		t.Fatalf("Query unexported: %v", err)
	}
	if len(rows1) != 0 {
		t.Errorf("want 0 rows, got %d", len(rows1))
	}
	// Query with a struct that has db:"-" → covers lines 76-77.
	rows2, err := Query[*testStructWithDashTag](context.Background(), db, "SELECT id FROM tmp")
	if err != nil {
		t.Fatalf("Query dash tag: %v", err)
	}
	if len(rows2) != 0 {
		t.Errorf("want 0 rows, got %d", len(rows2))
	}
}

// — SQLRepo.List with Desc ordering ————————————————————————————————————————

// Covers storage.go:638-640 — the "ORDER BY col DESC" branch in SQLRepo.List.
func TestSQLRepoList_OrderByDesc(t *testing.T) {
	db := newSQLiteDB(t)
	// Use the posts table set up by the standard SQLite post repo.
	if _, err := db.ExecContext(context.Background(), `
		CREATE TABLE IF NOT EXISTS testposts (
			id TEXT NOT NULL PRIMARY KEY, slug TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'draft', title TEXT NOT NULL DEFAULT '',
			body TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT (datetime('now')),
			updated_at DATETIME NOT NULL DEFAULT (datetime('now')),
			scheduled_at DATETIME, published_at DATETIME, rev INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}
	repo := NewSQLRepo[*testPost](db, Table("testposts"))
	ctx := context.Background()
	for _, title := range []string{"Alpha", "Beta", "Gamma"} {
		if err := repo.Save(ctx, &testPost{Node: Node{ID: NewID(), Slug: GenerateSlug(title), Status: Published}, Title: title}); err != nil {
			t.Fatalf("Save %s: %v", title, err)
		}
	}
	items, err := repo.FindAll(ctx, ListOptions{OrderBy: "Title", Desc: true})
	if err != nil {
		t.Fatalf("FindAll: %v", err)
	}
	if len(items) != 3 {
		t.Errorf("want 3 items, got %d", len(items))
	}
	if items[0].Title != "Gamma" {
		t.Errorf("want first item Gamma (desc), got %q", items[0].Title)
	}
}

// — extractRelationEdges edge-case paths ————————————————————————————————

func TestExtractRelationEdges_InvalidFieldsJSON(t *testing.T) {
	store := &RelationStore{
		db:       &errQueryDB{},
		registry: &RelationKindRegistry{kinds: make(map[string]RelationKindDef)},
	}
	dn := &DynamicNode{Fields: json.RawMessage(`not json`)}
	dn.ID = "d1"
	fields := []SchemaField{{Name: "tag", Relation: "edge"}}
	out := extractRelationEdges("article", "d1", fields, dn, store)
	if out != nil {
		t.Errorf("want nil for invalid JSON, got %v", out)
	}
}

func TestExtractRelationEdges_FieldValueNotString(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateRelationTables(db); err != nil {
		t.Fatalf("CreateRelationTables: %v", err)
	}
	store, err := NewRelationStore(db)
	if err != nil {
		t.Fatalf("NewRelationStore: %v", err)
	}
	if err := store.UpsertKind(context.Background(), RelationKindDef{
		TypeName:  "tag",
		Mode:      "asserted",
		TypePairs: json.RawMessage(`[{"source_type":"article","target_type":"tag"}]`),
	}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}
	// Field value is a number, not a string — should be skipped.
	dn := &DynamicNode{Fields: json.RawMessage(`{"tag": 42}`)}
	dn.ID = "d1"
	fields := []SchemaField{{Name: "tag", Relation: "edge"}}
	out := extractRelationEdges("article", "d1", fields, dn, store)
	if len(out) != 0 {
		t.Errorf("want 0 edges for non-string field value, got %d", len(out))
	}
}

func TestExtractRelationEdges_FieldMissingFromJSON(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateRelationTables(db); err != nil {
		t.Fatalf("CreateRelationTables: %v", err)
	}
	store, err := NewRelationStore(db)
	if err != nil {
		t.Fatalf("NewRelationStore: %v", err)
	}
	// Field "tag" is in schema but not in the JSON payload.
	dn := &DynamicNode{Fields: json.RawMessage(`{"other": "value"}`)}
	dn.ID = "d1"
	fields := []SchemaField{{Name: "tag", Relation: "edge"}}
	out := extractRelationEdges("article", "d1", fields, dn, store)
	if len(out) != 0 {
		t.Errorf("want 0 edges for missing field, got %d", len(out))
	}
}
