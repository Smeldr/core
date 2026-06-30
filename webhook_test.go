package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"net"
	"strings"
	"testing"
)

// TestWebhookStore_CreateAndList covers secret handling and List not leaking secrets.
func TestWebhookStore_CreateAndList(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
		CREATE TABLE smeldr_webhook_endpoints (
			id         TEXT    PRIMARY KEY,
			events     TEXT    NOT NULL,
			target_url TEXT    NOT NULL,
			secret_enc TEXT    NOT NULL,
			active     BOOLEAN NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL
		)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	store := NewWebhookStore(db, []byte("test-secret"))

	// Use a mock DNS-bypassing URL resolver by patching with a local HTTPS
	// server — but since we cannot start a TLS server in a unit test easily,
	// we test Create with a well-known public domain that validates correctly.
	// SSRF tests are separate (TestValidateWebhookURL).

	// Test encryption roundtrip directly.
	plain := []byte("my-signing-secret")
	enc, err := store.encryptSecret(plain)
	if err != nil {
		t.Fatalf("encryptSecret: %v", err)
	}
	if enc == "" {
		t.Fatal("encryptSecret returned empty string")
	}
	dec, err := store.decryptSecret(enc)
	if err != nil {
		t.Fatalf("decryptSecret: %v", err)
	}
	if string(dec) != string(plain) {
		t.Fatalf("roundtrip mismatch: got %q want %q", dec, plain)
	}

	// Different appKey should fail to decrypt.
	store2 := NewWebhookStore(db, []byte("different-secret"))
	if _, err := store2.decryptSecret(enc); err == nil {
		t.Fatal("decryptSecret with wrong key should fail")
	}
}

// TestWebhookStore_ListNeverLeaksSecret verifies secretEnc is never populated
// in List results.
func TestWebhookStore_ListNeverLeaksSecret(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
		CREATE TABLE smeldr_webhook_endpoints (
			id         TEXT    PRIMARY KEY,
			events     TEXT    NOT NULL,
			target_url TEXT    NOT NULL,
			secret_enc TEXT    NOT NULL,
			active     BOOLEAN NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL
		)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	store := NewWebhookStore(db, []byte("app-secret"))

	// Insert directly (bypass URL validation by inserting raw).
	ep := WebhookEndpoint{
		ID:     "ep-1",
		Events: []string{"post.published"},
	}
	evJSON, _ := json.Marshal(ep.Events)
	enc, _ := store.encryptSecret([]byte("plain"))
	_, err = db.ExecContext(ctx,
		`INSERT INTO smeldr_webhook_endpoints (id, events, target_url, secret_enc, active, created_at) VALUES ($1,$2,$3,$4,$5,datetime('now'))`,
		ep.ID, string(evJSON), "https://example.com/hook", enc, 1,
	)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List: want 1 item, got %d", len(list))
	}
	if list[0].secretEnc != "" {
		t.Error("List must not populate secretEnc")
	}
}

// TestWebhookStore_EndpointsForEvent verifies event filtering and that
// secretEnc IS populated (needed by the worker pool).
func TestWebhookStore_EndpointsForEvent(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
		CREATE TABLE smeldr_webhook_endpoints (
			id         TEXT    PRIMARY KEY,
			events     TEXT    NOT NULL,
			target_url TEXT    NOT NULL,
			secret_enc TEXT    NOT NULL,
			active     BOOLEAN NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL
		)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	store := NewWebhookStore(db, []byte("app-secret"))

	insertEP := func(id, eventsJSON, enc string, active int) {
		t.Helper()
		_, err := db.ExecContext(ctx,
			`INSERT INTO smeldr_webhook_endpoints (id, events, target_url, secret_enc, active, created_at) VALUES ($1,$2,$3,$4,$5,datetime('now'))`,
			id, eventsJSON, "https://example.com/"+id, enc, active,
		)
		if err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}

	enc, _ := store.encryptSecret([]byte("secret"))

	// ep-1 subscribes to post.published
	insertEP("ep-1", `["post.published"]`, enc, 1)
	// ep-2 subscribes to post.created and post.published
	insertEP("ep-2", `["post.created","post.published"]`, enc, 1)
	// ep-3 inactive — should not appear
	insertEP("ep-3", `["post.published"]`, enc, 0)

	eps, err := store.EndpointsForEvent(ctx, "post.published")
	if err != nil {
		t.Fatalf("EndpointsForEvent: %v", err)
	}
	if len(eps) != 2 {
		t.Fatalf("want 2 endpoints for post.published, got %d", len(eps))
	}
	for _, ep := range eps {
		if ep.secretEnc == "" {
			t.Errorf("endpoint %s: secretEnc must be populated", ep.ID)
		}
	}

	// Event with no subscribers.
	eps, err = store.EndpointsForEvent(ctx, "post.archived")
	if err != nil {
		t.Fatalf("EndpointsForEvent: %v", err)
	}
	if len(eps) != 0 {
		t.Fatalf("want 0 endpoints for post.archived, got %d", len(eps))
	}
}

// TestWebhookStore_Delete verifies removal.
func TestWebhookStore_Delete(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `
		CREATE TABLE smeldr_webhook_endpoints (
			id TEXT PRIMARY KEY, events TEXT NOT NULL, target_url TEXT NOT NULL,
			secret_enc TEXT NOT NULL, active BOOLEAN NOT NULL DEFAULT 1, created_at DATETIME NOT NULL
		)`)
	store := NewWebhookStore(db, []byte("k"))
	enc, _ := store.encryptSecret([]byte("s"))
	_, _ = db.ExecContext(ctx,
		`INSERT INTO smeldr_webhook_endpoints VALUES ('del-1','[]','https://x.com','`+enc+`',1,datetime('now'))`)
	if err := store.Delete(ctx, "del-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	list, _ := store.List(ctx)
	if len(list) != 0 {
		t.Fatalf("after Delete: want 0, got %d", len(list))
	}
}

// TestValidateWebhookURL covers the SSRF guard logic.
func TestValidateWebhookURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
		errMsg  string
	}{
		// Use a public IP to bypass DNS lookup in offline test environments.
		{"valid HTTPS IP", "https://8.8.8.8/deliver", false, ""},
		{"HTTP rejected", "http://8.8.8.8/deliver", true, "must use HTTPS"},
		{"FTP rejected", "ftp://example.com/hook", true, "must use HTTPS"},
		{"localhost rejected", "https://localhost/hook", true, "localhost"},
		{"localhost port rejected", "https://localhost:9000/hook", true, "localhost"},
		{"dotlocal rejected", "https://myserver.local/hook", true, ".local"},
		{"no scheme", "hooks.example.com/hook", true, "invalid URL"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateWebhookURL(tc.url)
			if tc.wantErr && err == nil {
				t.Fatalf("want error containing %q, got nil", tc.errMsg)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("want no error, got %v", err)
			}
			if tc.wantErr && !strings.Contains(err.Error(), tc.errMsg) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.errMsg)
			}
		})
	}
}

// TestIsPrivateIP verifies the private IP detection table.
func TestIsPrivateIP(t *testing.T) {
	private := []string{
		"127.0.0.1",
		"10.0.0.1",
		"10.255.255.255",
		"172.16.0.1",
		"172.31.255.255",
		"192.168.1.1",
		"::1",
		"fc00::1",
		"fe80::1",
	}
	public := []string{
		"8.8.8.8",
		"1.1.1.1",
		"2001:4860:4860::8888",
	}
	for _, ip := range private {
		if !isPrivateIP(net.ParseIP(ip)) {
			t.Errorf("expected %s to be private", ip)
		}
	}
	for _, ip := range public {
		if isPrivateIP(net.ParseIP(ip)) {
			t.Errorf("expected %s to be public", ip)
		}
	}
}

// TestBuildEventName covers the LifecycleEvent-to-event-name mapping.
func TestBuildEventName(t *testing.T) {
	tests := []struct {
		typeName string
		sig      LifecycleEvent
		want     string
		ok       bool
	}{
		{"Post", AfterCreate, "post.created", true},
		{"Post", AfterUpdate, "post.updated", true},
		{"Post", AfterPublish, "post.published", true},
		{"Post", AfterUnpublish, "post.unpublished", true},
		{"Post", AfterArchive, "post.archived", true},
		{"Post", AfterDelete, "post.deleted", true},
		{"Post", AfterSchedule, "post.scheduled", true},
		{"Post", BeforeCreate, "", false},
		{"Post", SitemapRegenerate, "", false},
		{"DocPage", AfterPublish, "docpage.published", true},
	}
	for _, tc := range tests {
		got, ok := buildEventName(tc.typeName, tc.sig)
		if ok != tc.ok {
			t.Errorf("buildEventName(%q, %q): ok=%v want %v", tc.typeName, tc.sig, ok, tc.ok)
		}
		if got != tc.want {
			t.Errorf("buildEventName(%q, %q): got %q want %q", tc.typeName, tc.sig, got, tc.want)
		}
	}
}

// testTitledPost is a test content type that implements Titled.
type testTitledPost struct {
	Node
	Title string
}

func (p *testTitledPost) ContentTitle() string { return p.Title }

// TestBuildWebhookPayload verifies payload structure and Titled interface.
func TestBuildWebhookPayload(t *testing.T) {
	item := &testTitledPost{
		Node:  Node{ID: "node-1", Slug: "hello-world"},
		Title: "Hello World",
	}

	data, err := buildWebhookPayload("Post", item, AfterPublish)
	if err != nil {
		t.Fatalf("buildWebhookPayload: %v", err)
	}

	var payload WebhookEventPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Event != "post.published" {
		t.Errorf("Event: got %q want %q", payload.Event, "post.published")
	}
	if payload.ID == "" {
		t.Error("payload ID must not be empty")
	}
	if payload.Timestamp.IsZero() {
		t.Error("payload Timestamp must not be zero")
	}

	var ed webhookEventData
	if err := json.Unmarshal(payload.Data, &ed); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	if ed.Type != "post" {
		t.Errorf("Type: got %q want %q", ed.Type, "post")
	}
	if ed.ID != "node-1" {
		t.Errorf("ID: got %q want %q", ed.ID, "node-1")
	}
	if ed.Slug != "hello-world" {
		t.Errorf("Slug: got %q want %q", ed.Slug, "hello-world")
	}
	if ed.Title != "Hello World" {
		t.Errorf("Title: got %q want %q", ed.Title, "Hello World")
	}

	// Without Titled interface — title should be omitted.
	type plainPost struct{ Node }
	plain := &plainPost{Node: Node{ID: "node-2", Slug: "plain"}}
	data2, err := buildWebhookPayload("Post", plain, AfterCreate)
	if err != nil {
		t.Fatalf("buildWebhookPayload (plain): %v", err)
	}
	var payload2 WebhookEventPayload
	json.Unmarshal(data2, &payload2)
	var ed2 webhookEventData
	json.Unmarshal(payload2.Data, &ed2)
	if ed2.Title != "" {
		t.Errorf("Title for non-Titled type should be empty, got %q", ed2.Title)
	}

	// Non-delivery LifecycleEvent should return error.
	_, err = buildWebhookPayload("Post", item, BeforeCreate)
	if err == nil {
		t.Fatal("buildWebhookPayload with BeforeCreate should return error")
	}
}

// createWebhookEndpointsTable creates the smeldr_webhook_endpoints table on db.
func createWebhookEndpointsTable(t *testing.T, db interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}) {
	t.Helper()
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
		CREATE TABLE smeldr_webhook_endpoints (
			id         TEXT    PRIMARY KEY,
			events     TEXT    NOT NULL,
			target_url TEXT    NOT NULL,
			secret_enc TEXT    NOT NULL,
			active     BOOLEAN NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL
		)`)
	if err != nil {
		t.Fatalf("create smeldr_webhook_endpoints: %v", err)
	}
}

func TestWebhookStore_Create_valid(t *testing.T) {
	db := newSQLiteDB(t)
	createWebhookEndpointsTable(t, db)
	store := NewWebhookStore(db, []byte("test-key-32bytes-xxxxxxxxxxxx!!!"))
	ctx := context.Background()

	// 8.8.8.8 is a public IP literal — Go resolves it without DNS, always passes SSRF check.
	ep, secret, err := store.Create(ctx, "https://8.8.8.8", []string{"post.create"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ep.ID == "" {
		t.Error("Create should assign a non-empty endpoint ID")
	}
	if secret == "" {
		t.Error("Create should return a non-empty plaintext secret")
	}
}

func TestWebhookStore_Create_httpURL(t *testing.T) {
	db := newSQLiteDB(t)
	store := NewWebhookStore(db, []byte("test-key-32bytes-xxxxxxxxxxxx!!!"))
	ctx := context.Background()

	_, _, err := store.Create(ctx, "http://example.com", []string{"post.create"})
	if err == nil {
		t.Error("Create with http:// URL should return error (SSRF guard requires HTTPS)")
	}
}

func TestWebhookStore_Create_privateIP(t *testing.T) {
	db := newSQLiteDB(t)
	store := NewWebhookStore(db, []byte("test-key-32bytes-xxxxxxxxxxxx!!!"))
	ctx := context.Background()

	_, _, err := store.Create(ctx, "https://192.168.1.1", []string{"post.create"})
	if err == nil {
		t.Error("Create with private IP should return error (SSRF guard)")
	}
}

// — decryptSecret error paths —————————————————————————————————————————————

func TestWebhookStore_decryptSecret_invalidBase64(t *testing.T) {
	store := NewWebhookStore(nil, []byte("test-key-32bytes-xxxxxxxxxxxx!!!"))
	_, err := store.decryptSecret("not-valid-base64!!!")
	if err == nil {
		t.Error("decryptSecret with invalid base64 should return an error")
	}
	if !strings.Contains(err.Error(), "base64") {
		t.Errorf("error should mention base64, got: %v", err)
	}
}

func TestWebhookStore_decryptSecret_tooShort(t *testing.T) {
	store := NewWebhookStore(nil, []byte("test-key-32bytes-xxxxxxxxxxxx!!!"))
	// 8 bytes encoded as base64 — less than AES-GCM nonce size (12).
	shortEnc := "AAAAAAAAAAA=" // 8 bytes
	_, err := store.decryptSecret(shortEnc)
	if err == nil {
		t.Error("decryptSecret with too-short ciphertext should return an error")
	}
}

// — WebhookStore DB error paths ———————————————————————————————————————————

func TestWebhookStore_Create_insertError(t *testing.T) {
	store := NewWebhookStore(&errExecDB{}, []byte("test-key-32bytes-xxxxxxxxxxxx!!!"))
	ctx := context.Background()
	// 8.8.8.8 passes SSRF validation; the ExecContext INSERT will then fail.
	_, _, err := store.Create(ctx, "https://8.8.8.8", []string{"post.created"})
	if err == nil {
		t.Error("expected error when INSERT fails")
	}
}

func TestWebhookStore_List_queryContextError(t *testing.T) {
	store := NewWebhookStore(&errQueryDB{}, []byte("test-key-32bytes-xxxxxxxxxxxx!!!"))
	_, err := store.List(context.Background())
	if err == nil {
		t.Error("expected error when QueryContext fails")
	}
}

func TestWebhookStore_EndpointsForEvent_queryContextError(t *testing.T) {
	store := NewWebhookStore(&errQueryDB{}, []byte("test-key-32bytes-xxxxxxxxxxxx!!!"))
	_, err := store.EndpointsForEvent(context.Background(), "post.created")
	if err == nil {
		t.Error("expected error when QueryContext fails")
	}
}

// — webhookDispatch error paths ———————————————————————————————————————————

func TestWebhookDispatch_signalNotDeliverable(t *testing.T) {
	store := NewWebhookStore(nil, []byte("k"))
	pool := newWorkerPool(nil, store, realClock{}, 1)
	ev := SignalEvent{Type: "Post"}
	// BeforeCreate is not a delivery LifecycleEvent — dispatched returns nil immediately.
	if err := webhookDispatch(context.Background(), ev, BeforeCreate, store, pool); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestWebhookDispatch_endpointsLookupError(t *testing.T) {
	store := NewWebhookStore(&errQueryDB{}, []byte("k"))
	pool := newWorkerPool(&errExecDB{}, store, realClock{}, 1)
	ev := SignalEvent{Type: "Post", raw: &testPost{}}
	// EndpointsForEvent fails — function logs and returns nil.
	if err := webhookDispatch(context.Background(), ev, AfterCreate, store, pool); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestWebhookDispatch_enqueueError(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	createWebhookEndpointsTable(t, db)
	store := NewWebhookStore(db, []byte("test-key-32bytes-xxxxxxxxxxxx!!!"))
	enc, _ := store.encryptSecret([]byte("plain"))
	eventsJSON := `["testpost.created"]`
	_, _ = db.ExecContext(ctx,
		`INSERT INTO smeldr_webhook_endpoints (id, events, target_url, secret_enc, active, created_at) VALUES ($1,$2,$3,$4,$5,datetime('now'))`,
		"ep-1", eventsJSON, "https://8.8.8.8/hook", enc, 1,
	)
	// Pool uses errExecDB → Enqueue always fails (INSERT into smeldr_outbound_jobs errors).
	pool := newWorkerPool(&errExecDB{}, store, realClock{}, 1)
	ev := SignalEvent{Type: "testpost", raw: &testPost{}}
	// Enqueue error is logged, not returned.
	if err := webhookDispatch(ctx, ev, AfterCreate, store, pool); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}
