package smeldr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// --- fakeAuditStore ---

type fakeAuditStore struct {
	appended []AuditRecord
	listed   []AuditRecord
}

func (f *fakeAuditStore) Append(_ context.Context, r AuditRecord) error {
	f.appended = append(f.appended, r)
	return nil
}

func (f *fakeAuditStore) List(_ context.Context, _ AuditFilter) ([]AuditRecord, error) {
	return f.listed, nil
}

// --- sqlAuditStore tests ---

func createAuditTableHelper(t *testing.T, db DB) {
	t.Helper()
	if err := CreateAuditTable(db); err != nil {
		t.Fatalf("CreateAuditTable: %v", err)
	}
}

func TestAuditStore_AppendAndList(t *testing.T) {
	db := newSQLiteDB(t)
	createAuditTableHelper(t, db)
	store := NewAuditStore(db)
	ctx := context.Background()

	r := AuditRecord{
		ID:            "test-id-1",
		Timestamp:     time.Now().UTC().Truncate(time.Second),
		Signal:        AfterPublish,
		ContentType:   "Post",
		Slug:          "hello",
		ActorID:       "actor-1",
		ActorRole:     "editor",
		PreviousState: "draft",
	}
	if err := store.Append(ctx, r); err != nil {
		t.Fatalf("Append: %v", err)
	}

	records, err := store.List(ctx, AuditFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("List: got %d records, want 1", len(records))
	}
	got := records[0]
	if got.ID != r.ID {
		t.Errorf("ID: got %q, want %q", got.ID, r.ID)
	}
	if got.Signal != AfterPublish {
		t.Errorf("Signal: got %q, want %q", got.Signal, AfterPublish)
	}
	if got.ContentType != "Post" {
		t.Errorf("ContentType: got %q, want %q", got.ContentType, "Post")
	}
	if got.Slug != "hello" {
		t.Errorf("Slug: got %q, want %q", got.Slug, "hello")
	}
	if got.PreviousState != "draft" {
		t.Errorf("PreviousState: got %q, want %q", got.PreviousState, "draft")
	}
}

func TestAuditStore_ListFilterByContentType(t *testing.T) {
	db := newSQLiteDB(t)
	createAuditTableHelper(t, db)
	store := NewAuditStore(db)
	ctx := context.Background()

	for _, rec := range []AuditRecord{
		{ID: "a", Timestamp: time.Now().UTC(), Signal: AfterPublish, ContentType: "Post", Slug: "p1", ActorID: "u1", ActorRole: "editor"},
		{ID: "b", Timestamp: time.Now().UTC(), Signal: AfterPublish, ContentType: "Page", Slug: "p2", ActorID: "u1", ActorRole: "editor"},
	} {
		if err := store.Append(ctx, rec); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	records, err := store.List(ctx, AuditFilter{ContentType: "Post"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 1 || records[0].ContentType != "Post" {
		t.Errorf("ContentType filter: got %d records", len(records))
	}
}

func TestAuditStore_ListFilterByActorID(t *testing.T) {
	db := newSQLiteDB(t)
	createAuditTableHelper(t, db)
	store := NewAuditStore(db)
	ctx := context.Background()

	for _, rec := range []AuditRecord{
		{ID: "a", Timestamp: time.Now().UTC(), Signal: AfterPublish, ContentType: "Post", Slug: "p1", ActorID: "actor-1", ActorRole: "editor"},
		{ID: "b", Timestamp: time.Now().UTC(), Signal: AfterPublish, ContentType: "Post", Slug: "p2", ActorID: "actor-2", ActorRole: "author"},
	} {
		if err := store.Append(ctx, rec); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	records, err := store.List(ctx, AuditFilter{ActorID: "actor-1"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 1 || records[0].ActorID != "actor-1" {
		t.Errorf("ActorID filter: got %d records", len(records))
	}
}

func TestAuditStore_ListFilterByTimeRange(t *testing.T) {
	db := newSQLiteDB(t)
	createAuditTableHelper(t, db)
	store := NewAuditStore(db)
	ctx := context.Background()

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	ids := []string{"aaaaaaaa", "bbbbbbbb", "cccccccc"}
	for i, ts := range []time.Time{base.Add(-time.Hour), base, base.Add(time.Hour)} {
		rec := AuditRecord{
			ID: ids[i], Timestamp: ts,
			Signal: AfterPublish, ContentType: "Post", Slug: "p", ActorID: "u", ActorRole: "editor",
		}
		if err := store.Append(ctx, rec); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	records, err := store.List(ctx, AuditFilter{From: base, To: base})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 1 {
		t.Errorf("time range filter: got %d records, want 1", len(records))
	}
}

func TestAuditStore_EmptyResult(t *testing.T) {
	db := newSQLiteDB(t)
	createAuditTableHelper(t, db)
	store := NewAuditStore(db)

	records, err := store.List(context.Background(), AuditFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected empty, got %d", len(records))
	}
}

// --- App.Audit() LifecycleEvent wiring tests ---

func TestAppAudit_SubscribedSignalsFire(t *testing.T) {
	store := &fakeAuditStore{}
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("audit-test-secret-12345678901234"),
	}))
	app.Audit(store)

	ctx := NewTestContext(User{ID: "u1", Roles: []Role{Editor}})

	// dispatchBus is synchronous; call it directly with each subscribed LifecycleEvent.
	for _, sig := range []LifecycleEvent{AfterPublish, AfterSchedule, AfterArchive, AfterDelete} {
		ev := SignalEvent{
			Type:          "Post",
			Slug:          "hello",
			Timestamp:     time.Now(),
			ActorID:       "u1",
			ActorRole:     "editor",
			PreviousState: "draft",
		}
		app.dispatchBus(ctx, ev, sig)
	}

	if len(store.appended) != 4 {
		t.Errorf("appended %d records, want 4 (one per subscribed LifecycleEvent)", len(store.appended))
	}
	for _, r := range store.appended {
		if r.ID == "" {
			t.Error("AuditRecord.ID is empty")
		}
		if r.ActorID != "u1" {
			t.Errorf("ActorID = %q, want u1", r.ActorID)
		}
		if r.PreviousState != "draft" {
			t.Errorf("PreviousState = %q, want draft", r.PreviousState)
		}
	}
}

func TestAppAudit_UnsubscribedSignalsNotRecorded(t *testing.T) {
	store := &fakeAuditStore{}
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("audit-test-secret-12345678901234"),
	}))
	app.Audit(store)

	ctx := NewTestContext(User{})
	for _, sig := range []LifecycleEvent{AfterCreate, AfterUpdate} {
		ev := SignalEvent{Type: "Post", Slug: "s", Timestamp: time.Now()}
		app.dispatchBus(ctx, ev, sig)
	}

	if len(store.appended) != 0 {
		t.Errorf("expected 0 appended for unsubscribed signals, got %d", len(store.appended))
	}
}

// --- GET /_audit HTTP endpoint tests ---

func TestAuditHandler_Unauthorized(t *testing.T) {
	store := &fakeAuditStore{}
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("audit-test-secret-12345678901234"),
	}))
	app.Audit(store)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/_audit", nil)
	app.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuditHandler_ForbiddenForAuthor(t *testing.T) {
	store := &fakeAuditStore{}
	secret := []byte("audit-test-secret-12345678901234")
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  secret,
	}))
	app.Audit(store)

	tok, _ := SignToken(User{ID: "u1", Roles: []Role{Author}}, string(secret), 0)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/_audit", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	app.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestAuditHandler_OKForEditor(t *testing.T) {
	store := &fakeAuditStore{listed: []AuditRecord{
		{ID: "r1", Signal: AfterPublish, ContentType: "Post", Slug: "hello"},
	}}
	secret := []byte("audit-test-secret-12345678901234")
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  secret,
	}))
	app.Audit(store)

	tok, _ := SignToken(User{ID: "u2", Roles: []Role{Editor}}, string(secret), 0)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/_audit", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	app.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var records []AuditRecord
	if err := json.NewDecoder(w.Body).Decode(&records); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(records) != 1 || records[0].ID != "r1" {
		t.Errorf("records = %v, want [{ID:r1}]", records)
	}
}

func TestAuditHandler_EmptyReturnsArray(t *testing.T) {
	store := &fakeAuditStore{}
	secret := []byte("audit-test-secret-12345678901234")
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  secret,
	}))
	app.Audit(store)

	tok, _ := SignToken(User{ID: "u3", Roles: []Role{Editor}}, string(secret), 0)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/_audit", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	app.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	body := strings.TrimSpace(w.Body.String())
	if !strings.HasPrefix(body, "[") {
		t.Errorf("expected JSON array, got: %q", body)
	}
}

func TestAuditHandler_BadFromParam(t *testing.T) {
	store := &fakeAuditStore{}
	secret := []byte("audit-test-secret-12345678901234")
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  secret,
	}))
	app.Audit(store)

	tok, _ := SignToken(User{ID: "u4", Roles: []Role{Editor}}, string(secret), 0)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/_audit?from=not-a-date", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	app.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestAuditHandler_NotMountedWithoutAudit(t *testing.T) {
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("audit-test-secret-12345678901234"),
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/_audit", nil)
	app.Handler().ServeHTTP(w, r)

	// Without App.Audit(), /_audit falls through to the redirect fallback → 404.
	if w.Code == http.StatusOK {
		t.Error("/_audit should not be mounted when App.Audit() was not called")
	}
}

func TestAuditHandler_ValidFromParam(t *testing.T) {
	store := &fakeAuditStore{}
	secret := []byte("audit-test-secret-12345678901234")
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  secret,
	}))
	app.Audit(store)

	tok, _ := SignToken(User{ID: "u5", Roles: []Role{Editor}}, string(secret), 0)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/_audit?from=2026-01-01T00:00:00Z", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	app.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestAuditHandler_ValidToParam(t *testing.T) {
	store := &fakeAuditStore{}
	secret := []byte("audit-test-secret-12345678901234")
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  secret,
	}))
	app.Audit(store)

	tok, _ := SignToken(User{ID: "u6", Roles: []Role{Editor}}, string(secret), 0)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/_audit?to=2026-12-31T23:59:59Z", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	app.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestAuditHandler_BadToParam(t *testing.T) {
	store := &fakeAuditStore{}
	secret := []byte("audit-test-secret-12345678901234")
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  secret,
	}))
	app.Audit(store)

	tok, _ := SignToken(User{ID: "u7", Roles: []Role{Editor}}, string(secret), 0)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/_audit?to=not-a-date", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	app.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

// errAuditStore returns an error from List.
type errAuditStore struct{}

func (s *errAuditStore) Append(_ context.Context, _ AuditRecord) error { return nil }
func (s *errAuditStore) List(_ context.Context, _ AuditFilter) ([]AuditRecord, error) {
	return nil, errRepoError
}

func TestAuditHandler_ListError(t *testing.T) {
	secret := []byte("audit-test-secret-12345678901234")
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  secret,
	}))
	app.Audit(&errAuditStore{})

	tok, _ := SignToken(User{ID: "u8", Roles: []Role{Editor}}, string(secret), 0)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/_audit", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	app.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}
