package smeldr

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// storeRoutesFixture is an App with a TokenStore and every HTTP route family
// that authenticates a bearer token wired, for T1176.
type storeRoutesFixture struct {
	identityFixture
	h         http.Handler
	goalSlug  string
	goalID    string
	tokenID   string // the stored fingerprint of tok
	tok       string // an admin token minted through the store
	userID    string
	identity  string
	badSigned string // tok's claims signed with another secret
}

func newStoreRoutesFixture(t *testing.T) storeRoutesFixture {
	t.Helper()
	f := storeRoutesFixture{identityFixture: newIdentityFixture(t)}
	for _, create := range []func(DB) error{CreateOrchestrationTables, CreateRelationTables} {
		if err := create(f.db); err != nil {
			t.Fatal(err)
		}
	}
	rs, err := NewRelationStore(f.db)
	if err != nil {
		t.Fatal(err)
	}
	f.goalID = insertTestGoal(t, f.db, "T9176", "P0", "S")
	f.goalSlug = mustSlugForID(t, f.db, "smeldr_goals", f.goalID)

	old := slog.Default()
	t.Cleanup(func() { slog.SetDefault(old) })
	f.app.CaptureLogs()
	f.app.Audit(NewAuditStore(f.db))
	f.app.EventStream()
	f.app.ServeDynamicContent()
	f.app.StatsHandler()
	f.app.ContextPacketHandler(rs, "t1176")
	f.app.ReachabilityHandler(rs)
	f.app.Content(NewModule((*testPost)(nil), Repo(NewMemoryRepo[*testPost]()), At("/posts"),
		Auth(Read(Editor), Write(Editor))))
	f.app.Cookies(Cookie{Name: "session", Category: Necessary, Purpose: "Auth"})
	f.app.CookiesManifestAuth(BearerHMAC(testSecret))
	f.app.Redirect("/old", "/new", Permanent)
	f.app.RedirectManifestAuth(BearerHMAC(testSecret))
	f.h = f.app.Handler()

	f.tok, f.userID, err = f.store.CreateClassified(as("h-1", Admin, Human), "t1176", "admin", Agent, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	f.tokenID = tokenFingerprint(f.tok)
	signed, err := decodeToken(f.tok, testSecret)
	if err != nil {
		t.Fatal(err)
	}
	if f.badSigned, err = SignToken(signed, "another-secret-16b", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT identity_id FROM smeldr_tokens WHERE id = $1`, f.tokenID).Scan(&f.identity); err != nil {
		t.Fatal(err)
	}
	return f
}

// call sends one request with the bearer token tok and returns its status.
// A streaming route is cut after a short while; its status is what it wrote
// first.
func (f storeRoutesFixture) call(method, path, body, tok string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, req)
	return w.Code
}

// storeRoute is one HTTP route family that authenticates a bearer token.
type storeRoute struct {
	name, method, path, body string
}

func (f storeRoutesFixture) routes() []storeRoute {
	return []storeRoute{
		{"packet", "GET", "/packet/goal/" + f.goalSlug, ""},
		{"reachability", "GET", "/reachability/goal/" + f.goalID, ""},
		{"_content", "POST", "/_content/types", `{}`},
		{"record route", "POST", "/posts", `{"Title":"t1176","Slug":"t1176"}`},
		{"_audit", "GET", "/_audit", ""},
		{"_logs", "GET", "/_logs", ""},
		{"_stats", "GET", "/_stats", ""},
		{"_events/stream", "GET", "/_events/stream", ""},
		{"cookies manifest", "GET", "/.well-known/cookies.json", ""},
		{"redirects manifest", "GET", "/.well-known/redirects.json", ""},
	}
}

// TestStoreCheck_EveryRouteFamily: on every route family, a live token works,
// and a revoked token and a token of a suspended identity get the answer a
// bad signature gets (T1176).
func TestStoreCheck_EveryRouteFamily(t *testing.T) {
	for _, refuse := range []struct {
		name string
		do   func(t *testing.T, f storeRoutesFixture)
	}{
		{"revoked", func(t *testing.T, f storeRoutesFixture) {
			if err := f.store.Revoke(context.Background(), f.tokenID); err != nil {
				t.Fatal(err)
			}
		}},
		{"identity suspended", func(t *testing.T, f storeRoutesFixture) {
			if _, err := f.db.Exec(`UPDATE smeldr_identities SET status = $1 WHERE id = $2`, IdentitySuspended, f.identity); err != nil {
				t.Fatal(err)
			}
		}},
		{"identity retired", func(t *testing.T, f storeRoutesFixture) {
			if _, err := f.db.Exec(`UPDATE smeldr_identities SET status = $1 WHERE id = $2`, IdentityRetired, f.identity); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(refuse.name, func(t *testing.T) {
			f := newStoreRoutesFixture(t)
			routes := f.routes()
			live := make([]int, len(routes))
			bad := make([]int, len(routes))
			for i, rt := range routes {
				live[i] = f.call(rt.method, rt.path, rt.body, f.tok)
				bad[i] = f.call(rt.method, rt.path, rt.body, f.badSigned)
				if live[i] == bad[i] {
					t.Errorf("%s: a live token gets %d, the same as a bad signature", rt.name, live[i])
				}
			}
			refuse.do(t, f)
			for i, rt := range routes {
				if got := f.call(rt.method, rt.path, rt.body, f.tok); got != bad[i] {
					t.Errorf("%s: %s token gets %d; a bad signature gets %d", rt.name, refuse.name, got, bad[i])
				}
			}
		})
	}
}

// TestStoreCheck_UnstoredTokenRefused: a token signed with the App's secret
// that the store does not hold is refused, as on MCP (T1176).
func TestStoreCheck_UnstoredTokenRefused(t *testing.T) {
	f := newStoreRoutesFixture(t)
	unstored, err := SignToken(User{ID: "u-x", Roles: []Role{Admin}}, testSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, rt := range f.routes() {
		if got, bad := f.call(rt.method, rt.path, rt.body, unstored), f.call(rt.method, rt.path, rt.body, f.badSigned); got != bad {
			t.Errorf("%s: an unstored token gets %d; a bad signature gets %d", rt.name, got, bad)
		}
	}
}

// TestStoreCheck_NoStoreUnchanged: an App without a TokenStore authenticates
// a signed token as before, revoked in some database or not.
func TestStoreCheck_NoStoreUnchanged(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte(testSecret)})
	if _, ok := app.requestAuth.(*storeCheckedAuth); ok {
		t.Fatal("an App without a store wraps its auth")
	}
	tok, err := SignToken(User{ID: "u-1", Roles: []Role{Editor}}, testSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	if u, ok := app.requestAuth.authenticate(req); !ok || u.ID != "u-1" {
		t.Errorf("authenticate = %+v, %v", u, ok)
	}
}

// TestStoreCheck_ManifestAuthNoStoreUnchanged: on an App without a
// TokenStore, the AuthFunc given to either manifest is used as given (T1176).
func TestStoreCheck_ManifestAuthNoStoreUnchanged(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte(testSecret)})
	auth := BearerHMAC(testSecret)
	if got := app.storeChecked(auth); got != auth {
		t.Fatalf("storeChecked without a store = %T; want the AuthFunc unchanged", got)
	}
	app.Cookies(Cookie{Name: "session", Category: Necessary, Purpose: "Auth"})
	app.CookiesManifestAuth(auth)
	app.Redirect("/old", "/new", Permanent)
	app.RedirectManifestAuth(auth)
	h := app.Handler()
	tok, err := SignToken(User{ID: "u-1", Roles: []Role{Editor}}, testSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/.well-known/cookies.json", "/.well-known/redirects.json"} {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("%s: a signed token gets %d; want 200", path, w.Code)
		}
	}
}

// TestStoreCheck_SameUserAsMCP: one stored token is the same user on an HTTP
// route as through VerifyBearerToken with the store, which MCP uses: same
// ID, class and TokenID (architect's addition 1 to P383).
func TestStoreCheck_SameUserAsMCP(t *testing.T) {
	f := newStoreRoutesFixture(t)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+f.tok)
	httpUser, ok := f.app.requestAuth.authenticate(req)
	if !ok {
		t.Fatal("HTTP refused a live token")
	}
	mcpUser, ok := VerifyBearerToken(req, []byte(testSecret), f.store)
	if !ok {
		t.Fatal("VerifyBearerToken refused a live token")
	}
	if httpUser.ID != mcpUser.ID || httpUser.TokenID != mcpUser.TokenID || httpUser.TokenID != f.tokenID ||
		!sameRoles(httpUser.Roles, mcpUser.Roles) || !hasClass(httpUser.Roles, Agent) {
		t.Errorf("HTTP user %+v; MCP user %+v", httpUser, mcpUser)
	}
	if httpUser.ID != f.userID {
		t.Errorf("HTTP user ID %q; the token was minted for %q (identity %q)", httpUser.ID, f.userID, f.identity)
	}
}

func hasClass(roles []Role, class Role) bool {
	for _, r := range roles {
		if r == class {
			return true
		}
	}
	return false
}

func sameRoles(a, b []Role) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestStoreCheckedAuth_Paths covers storeCheckedAuth.authenticate's error
// paths one by one (P383's table).
func TestStoreCheckedAuth_Paths(t *testing.T) {
	f := newStoreRoutesFixture(t)
	bearer := func(tok string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		if tok != "" {
			r.Header.Set("Authorization", "Bearer "+tok)
		}
		return r
	}
	checked := withStoreCheck(BearerHMAC(testSecret), testSecret, f.store)
	if _, ok := checked.authenticate(bearer("")); ok {
		t.Error("no credential accepted")
	}
	if _, ok := checked.authenticate(bearer(f.badSigned)); ok {
		t.Error("a bad signature accepted")
	}
	if u, ok := checked.authenticate(bearer(f.tok)); !ok || u.TokenID != f.tokenID {
		t.Errorf("a live token: %+v, %v", u, ok)
	}

	custom := withStoreCheck(fixedAuth{u: User{ID: "custom"}}, testSecret, f.store)
	if u, ok := custom.authenticate(bearer("")); !ok || u.ID != "custom" {
		t.Errorf("no bearer header, another AuthFunc: %+v, %v", u, ok)
	}
	if u, ok := custom.authenticate(bearer(f.badSigned)); !ok || u.ID != "custom" {
		t.Errorf("a bearer of another secret, another AuthFunc: %+v, %v", u, ok)
	}

	down := withStoreCheck(BearerHMAC(testSecret), testSecret, NewTokenStore(identityFaultDB{f.db, "FROM smeldr_tokens"}, testSecret))
	if _, ok := down.authenticate(bearer(f.tok)); ok {
		t.Error("a store error accepted the token; want fail closed")
	}
}

// TestStoreCheck_CookieSessionUnchanged: a session cookie still
// authenticates, keeps its CSRF, and passes its production warning through
// the store check (T1176).
func TestStoreCheck_CookieSessionUnchanged(t *testing.T) {
	f := newStoreRoutesFixture(t)
	session := CookieSession("smeldr_session", testSecret)
	auth := withStoreCheck(session, testSecret, f.store)
	cookieTok, err := SignToken(User{ID: "member-1", Roles: []Role{Editor}}, testSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: "smeldr_session", Value: cookieTok})
	if u, ok := auth.authenticate(r); !ok || u.ID != "member-1" {
		t.Errorf("cookie session: %+v, %v", u, ok)
	}
	ca, ok := auth.(csrfAware)
	if !ok || ca.csrfEnabled() != session.(csrfAware).csrfEnabled() {
		t.Error("the store check changed CookieSession's CSRF")
	}
	var warn strings.Builder
	auth.(productionWarner).warnIfProduction(&warn)
	withStoreCheck(BearerHMAC(testSecret), testSecret, f.store).(productionWarner).warnIfProduction(&warn)
	if withStoreCheck(BearerHMAC(testSecret), testSecret, f.store).(csrfAware).csrfEnabled() {
		t.Error("BearerHMAC behind the store check reports CSRF")
	}

	// A cookie together with a revoked bearer token is refused (P383 Q2).
	if err := f.store.Revoke(context.Background(), f.tokenID); err != nil {
		t.Fatal(err)
	}
	both := withStoreCheck(AnyAuth(session, BearerHMAC(testSecret)), testSecret, f.store)
	r.Header.Set("Authorization", "Bearer "+f.tok)
	if _, ok := both.authenticate(r); ok {
		t.Error("a cookie with a revoked bearer token was accepted")
	}
}

// BenchmarkStoreCheckedAuth measures the per-request cost the store check
// adds for a bearer token on SQLite: one SHA-256 and one primary-key query.
func BenchmarkStoreCheckedAuth(b *testing.B) {
	f := newIdentityFixture(b)
	tok, _, err := f.store.CreateClassified(as("h-1", Admin, Human), "bench", "admin", Agent, time.Hour)
	if err != nil {
		b.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	plain := BearerHMAC(testSecret)
	checked := withStoreCheck(plain, testSecret, f.store)
	b.Run("signature only", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			plain.authenticate(r)
		}
	})
	b.Run("with store check", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			checked.authenticate(r)
		}
	})
}

// TestStoreCheck_PreviewAndUploadTokensUnchanged: on an App with a store, a
// ?preview= token still opens a draft and an upload token still passes the
// App's middleware to the upload handler; neither is a bearer token of the
// store (architect's addition 2 to P383).
func TestStoreCheck_PreviewAndUploadTokensUnchanged(t *testing.T) {
	f := newIdentityFixture(t)
	repo := NewMemoryRepo[*testPost]()
	draft := seedPost(t, repo, "Preview draft", Draft)
	f.app.Content(NewModule((*testPost)(nil), Repo(repo), At("/posts")))
	// The shape of smeldr.dev/media's upload check, behind the App's middleware.
	f.app.Handle("POST /media", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr := r.Header.Get("Authorization")
		if !strings.HasPrefix(hdr, "UploadToken ") || f.app.ValidateUploadToken(strings.TrimPrefix(hdr, "UploadToken ")) != nil {
			WriteError(w, r, ErrUnauth)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	h := f.app.Handler()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/posts/"+draft.Slug+"?preview="+f.app.GeneratePreviewToken("/posts", draft.Slug), nil))
	if w.Code != http.StatusOK {
		t.Errorf("preview of a draft: %d, want 200", w.Code)
	}

	req := httptest.NewRequest("POST", "/media", nil)
	req.Header.Set("Authorization", "UploadToken "+f.app.GenerateUploadToken())
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Errorf("upload token: %d, want 201", w.Code)
	}
}
