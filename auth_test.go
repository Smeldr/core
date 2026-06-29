package smeldr

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// — User methods ——————————————————————————————————————————————————————————

func TestUserHasRole(t *testing.T) {
	t.Run("exact match", func(t *testing.T) {
		u := User{Roles: []Role{Editor}}
		if !u.HasRole(Editor) {
			t.Fatal("expected HasRole(Editor) to return true for Editor")
		}
	})
	t.Run("hierarchical — Admin satisfies Editor", func(t *testing.T) {
		u := User{Roles: []Role{Admin}}
		if !u.HasRole(Editor) {
			t.Fatal("expected HasRole(Editor) to return true for Admin")
		}
	})
	t.Run("insufficient — Author does not satisfy Editor", func(t *testing.T) {
		u := User{Roles: []Role{Author}}
		if u.HasRole(Editor) {
			t.Fatal("expected HasRole(Editor) to return false for Author")
		}
	})
	t.Run("guest user — no roles", func(t *testing.T) {
		if GuestUser.HasRole(Author) {
			t.Fatal("expected HasRole(Author) to return false for GuestUser")
		}
	})
}

func TestUserIs(t *testing.T) {
	t.Run("exact match returns true", func(t *testing.T) {
		u := User{Roles: []Role{Author}}
		if !u.Is(Author) {
			t.Fatal("expected Is(Author) to return true for Author")
		}
	})
	t.Run("higher role does not satisfy exact match", func(t *testing.T) {
		u := User{Roles: []Role{Admin}}
		if u.Is(Editor) {
			t.Fatal("expected Is(Editor) to return false for Admin")
		}
	})
	t.Run("guest user — no roles", func(t *testing.T) {
		if GuestUser.Is(Guest) {
			t.Fatal("expected Is(Guest) to return false for GuestUser (no roles set)")
		}
	})
}

// — SignToken / decodeToken —————————————————————————————————————————————

// compile-time: signToken's error return must satisfy smeldr.Error
var _ Error = ErrInternal

func TestSignTokenRoundTrip(t *testing.T) {
	secret := "test-secret-that-is-long-enough-32x"
	original := User{ID: "u1", Name: "Alice", Roles: []Role{Editor}}

	token, err := SignToken(original, secret, 0)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}

	got, err := decodeToken(token, secret)
	if err != nil {
		t.Fatalf("decodeToken: %v", err)
	}

	if got.ID != original.ID {
		t.Errorf("ID: got %q want %q", got.ID, original.ID)
	}
	if got.Name != original.Name {
		t.Errorf("Name: got %q want %q", got.Name, original.Name)
	}
	if len(got.Roles) != 1 || got.Roles[0] != Editor {
		t.Errorf("Roles: got %v want [editor]", got.Roles)
	}
}

func TestSignTokenTampered(t *testing.T) {
	secret := "test-secret-that-is-long-enough-32x"
	token, _ := SignToken(User{ID: "u1", Name: "Alice", Roles: []Role{Editor}}, secret, 0)

	// Tamper: replace first character of payload
	parts := strings.SplitN(token, ".", 2)
	if len(parts[0]) == 0 {
		t.Fatal("payload is empty")
	}
	// Flip the first byte of the payload
	payload := []byte(parts[0])
	payload[0] ^= 0x01
	tampered := string(payload) + "." + parts[1]

	_, err := decodeToken(tampered, secret)
	if err == nil {
		t.Fatal("expected error for tampered token, got nil")
	}
}

func TestSignTokenWrongSecret(t *testing.T) {
	token, _ := SignToken(User{ID: "u1"}, "secret-a-long-enough-32-chars-00", 0)
	_, err := decodeToken(token, "secret-b-long-enough-32-chars-00")
	if err == nil {
		t.Fatal("expected error for wrong secret, got nil")
	}
}

func TestSignTokenWithExpiry(t *testing.T) {
	secret := "test-secret-that-is-long-enough-32x"
	user := User{ID: "u1", Roles: []Role{Author}}

	// A token with a future TTL must decode successfully.
	tok, err := SignToken(user, secret, 24*time.Hour)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	got, err := decodeToken(tok, secret)
	if err != nil {
		t.Fatalf("decodeToken on fresh token: %v", err)
	}
	if got.ID != user.ID {
		t.Fatalf("ID: got %q want %q", got.ID, user.ID)
	}
}

func TestSignTokenExpiredRejects(t *testing.T) {
	secret := "test-secret-that-is-long-enough-32x"
	// Craft a token whose exp is 1 hour in the past.
	raw, _ := json.Marshal(tokenPayload{
		ID:    "u99",
		Name:  "old",
		Roles: []string{"guest"},
		Exp:   time.Now().Add(-time.Hour).Unix(),
	})
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	sig := tokenHMAC(encoded, secret)
	tok := encoded + "." + sig

	_, err := decodeToken(tok, secret)
	if err == nil {
		t.Fatal("expected ErrUnauth for expired token, got nil")
	}
}

// — BearerHMAC ————————————————————————————————————————————————————————————

const testSecret = "test-hmac-secret-that-is-32chars"

func signedToken(t *testing.T, user User) string {
	t.Helper()
	tok, err := SignToken(user, testSecret, 0)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	return tok
}

func TestBearerHMACValid(t *testing.T) {
	user := User{ID: "u42", Name: "Bob", Roles: []Role{Author}}
	tok := signedToken(t, user)

	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	fn := BearerHMAC(testSecret)
	got, ok := fn.authenticate(req)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if got.ID != user.ID {
		t.Errorf("ID: got %q want %q", got.ID, user.ID)
	}
}

func TestBearerHMACInvalid(t *testing.T) {
	tok := signedToken(t, User{ID: "u1"})

	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	fn := BearerHMAC("different-secret-32chars-padding!")
	_, ok := fn.authenticate(req)
	if ok {
		t.Fatal("expected ok=false for wrong secret")
	}
}

func TestBearerHMACMissingHeader(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	fn := BearerHMAC(testSecret)
	_, ok := fn.authenticate(req)
	if ok {
		t.Fatal("expected ok=false for missing Authorization header")
	}
}

func TestBearerHMACMalformedHeader(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Basic abc123")
	fn := BearerHMAC(testSecret)
	_, ok := fn.authenticate(req)
	if ok {
		t.Fatal("expected ok=false for non-Bearer Authorization")
	}
}

// — CookieSession —————————————————————————————————————————————————————————

func TestCookieSessionValid(t *testing.T) {
	user := User{ID: "u99", Name: "Carol", Roles: []Role{Editor}}
	tok := signedToken(t, user)

	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "forge_session", Value: tok})

	fn := CookieSession("forge_session", testSecret)
	got, ok := fn.authenticate(req)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if got.ID != user.ID {
		t.Errorf("ID: got %q want %q", got.ID, user.ID)
	}
}

func TestCookieSessionInvalid(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "forge_session", Value: "not.avalid.token"})

	fn := CookieSession("forge_session", testSecret)
	_, ok := fn.authenticate(req)
	if ok {
		t.Fatal("expected ok=false for invalid cookie value")
	}
}

func TestCookieSessionNoCookie(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	fn := CookieSession("forge_session", testSecret)
	_, ok := fn.authenticate(req)
	if ok {
		t.Fatal("expected ok=false for missing cookie")
	}
}

func TestCookieSessionCSRFEnabled(t *testing.T) {
	fn := CookieSession("forge_session", testSecret)
	ca, ok := fn.(csrfAware)
	if !ok {
		t.Fatal("CookieSession must implement csrfAware")
	}
	if !ca.csrfEnabled() {
		t.Fatal("expected csrfEnabled()=true by default")
	}
}

func TestCookieSessionWithoutCSRF(t *testing.T) {
	fn := CookieSession("forge_session", testSecret, WithoutCSRF)
	ca, ok := fn.(csrfAware)
	if !ok {
		t.Fatal("CookieSession must implement csrfAware")
	}
	if ca.csrfEnabled() {
		t.Fatal("expected csrfEnabled()=false when WithoutCSRF passed")
	}
}

// — BasicAuth ————————————————————————————————————————————————————————————

func TestBasicAuthValid(t *testing.T) {
	fn := BasicAuth("alice", "s3cr3t")
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("alice", "s3cr3t")

	got, ok := fn.authenticate(req)
	if !ok {
		t.Fatal("expected ok=true for correct credentials")
	}
	if got.ID != "alice" {
		t.Errorf("ID: got %q want %q", got.ID, "alice")
	}
	if got.Name != "alice" {
		t.Errorf("Name: got %q want %q", got.Name, "alice")
	}
	if len(got.Roles) != 1 || got.Roles[0] != Guest {
		t.Errorf("Roles: got %v want [guest]", got.Roles)
	}
}

func TestBasicAuthInvalid(t *testing.T) {
	fn := BasicAuth("alice", "s3cr3t")
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("alice", "wrong")

	_, ok := fn.authenticate(req)
	if ok {
		t.Fatal("expected ok=false for wrong password")
	}
}

func TestBasicAuthMissingHeader(t *testing.T) {
	fn := BasicAuth("alice", "s3cr3t")
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	_, ok := fn.authenticate(req)
	if ok {
		t.Fatal("expected ok=false for missing Authorization header")
	}
}

func TestBasicAuthProductionWarn(t *testing.T) {
	fn := BasicAuth("alice", "s3cr3t")
	pw, ok := fn.(productionWarner)
	if !ok {
		t.Fatal("BasicAuth must implement productionWarner")
	}

	var buf bytes.Buffer
	pw.warnIfProduction(&buf)

	if !strings.Contains(buf.String(), "BasicAuth") {
		t.Errorf("warning should mention BasicAuth; got: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "non-development") {
		t.Errorf("warning should mention non-development; got: %q", buf.String())
	}
}

// — AnyAuth ———————————————————————————————————————————————————————————————

func TestAnyAuthFirstWins(t *testing.T) {
	// First AuthFunc returns a user; second should not be consulted.
	user := User{ID: "first", Name: "First", Roles: []Role{Author}}
	tok := signedToken(t, user)

	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	// bearerHMAC succeeds; cookieSession would fail (no cookie).
	fn := AnyAuth(BearerHMAC(testSecret), CookieSession("forge_session", testSecret))
	got, ok := fn.authenticate(req)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if got.ID != user.ID {
		t.Errorf("ID: got %q want %q", got.ID, user.ID)
	}
}

func TestAnyAuthNoneMatch(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "/", nil) // no headers, no cookies
	fn := AnyAuth(BearerHMAC(testSecret), CookieSession("forge_session", testSecret))
	_, ok := fn.authenticate(req)
	if ok {
		t.Fatal("expected ok=false when no AuthFunc matches")
	}
}

func TestAnyAuthForwardsWarn(t *testing.T) {
	fn := AnyAuth(BearerHMAC(testSecret), BasicAuth("admin", "pass"))
	pw, ok := fn.(productionWarner)
	if !ok {
		t.Fatal("AnyAuth must implement productionWarner")
	}

	var buf bytes.Buffer
	pw.warnIfProduction(&buf)

	// BasicAuth's warning should have been forwarded.
	if !strings.Contains(buf.String(), "BasicAuth") {
		t.Errorf("expected forwarded BasicAuth warning; got: %q", buf.String())
	}
}

func TestAnyAuthCSRFAware(t *testing.T) {
	t.Run("CSRF enabled when CookieSession present", func(t *testing.T) {
		fn := AnyAuth(BearerHMAC(testSecret), CookieSession("forge_session", testSecret))
		ca, ok := fn.(csrfAware)
		if !ok {
			t.Fatal("AnyAuth must implement csrfAware")
		}
		if !ca.csrfEnabled() {
			t.Fatal("expected csrfEnabled()=true when CookieSession is in the chain")
		}
	})
	t.Run("CSRF disabled when only BearerHMAC", func(t *testing.T) {
		fn := AnyAuth(BearerHMAC(testSecret))
		ca, ok := fn.(csrfAware)
		if !ok {
			t.Fatal("AnyAuth must implement csrfAware")
		}
		if ca.csrfEnabled() {
			t.Fatal("expected csrfEnabled()=false with only BearerHMAC in chain")
		}
	})
	t.Run("CSRF disabled via WithoutCSRF", func(t *testing.T) {
		fn := AnyAuth(CookieSession("forge_session", testSecret, WithoutCSRF))
		ca := fn.(csrfAware)
		if ca.csrfEnabled() {
			t.Fatal("expected csrfEnabled()=false with WithoutCSRF")
		}
	})
}

func TestCSRFCookieName(t *testing.T) {
	if CSRFCookieName != "smeldr_csrf" {
		t.Errorf("CSRFCookieName: got %q want %q", CSRFCookieName, "smeldr_csrf")
	}
}

func TestWithoutCSRFImplementsOption(t *testing.T) {
	// Compile-time: var WithoutCSRF Option — this test documents the runtime check.
	var _ Option = WithoutCSRF
}

// — VerifyBearerToken ———————————————————————————————————————————————————————

func TestVerifyBearerToken(t *testing.T) {
	secret := []byte("test-secret-32-bytes-xxxxxxxxxxxx")
	u := User{ID: "u1", Name: "Alice", Roles: []Role{Editor}}

	t.Run("valid token returns user", func(t *testing.T) {
		tok, err := SignToken(u, string(secret), 0)
		if err != nil {
			t.Fatalf("SignToken: %v", err)
		}
		r, _ := http.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Authorization", "Bearer "+tok)
		got, ok := VerifyBearerToken(r, secret, nil)
		if !ok {
			t.Fatal("expected ok=true for valid token")
		}
		if got.ID != u.ID {
			t.Errorf("user ID: got %q want %q", got.ID, u.ID)
		}
		if len(got.Roles) == 0 || got.Roles[0] != Editor {
			t.Errorf("user roles: got %v want [Editor]", got.Roles)
		}
	})

	t.Run("missing Authorization header returns GuestUser", func(t *testing.T) {
		r, _ := http.NewRequest(http.MethodGet, "/", nil)
		got, ok := VerifyBearerToken(r, secret, nil)
		if ok {
			t.Fatal("expected ok=false for missing header")
		}
		if got.ID != GuestUser.ID {
			t.Errorf("expected GuestUser, got %+v", got)
		}
	})

	t.Run("wrong secret returns GuestUser", func(t *testing.T) {
		tok, err := SignToken(u, string(secret), 0)
		if err != nil {
			t.Fatalf("SignToken: %v", err)
		}
		r, _ := http.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Authorization", "Bearer "+tok)
		got, ok := VerifyBearerToken(r, []byte("wrong-secret-32-bytes-xxxxxxxxxxxx"), nil)
		if ok {
			t.Fatal("expected ok=false for wrong secret")
		}
		if got.ID != GuestUser.ID {
			t.Errorf("expected GuestUser, got %+v", got)
		}
	})
}

// — TokenStore ——————————————————————————————————————————————————————————————

// stubDB implements smeldr.DB using an in-memory slice for smeldr_tokens rows.
// Only ExecContext (INSERT + UPDATE) is needed by TokenStore.Create/Revoke.
type stubDB struct {
	rows []stubTokenRow
}

type stubTokenRow struct {
	id, name, role, expiresAt string
	revokedAt                 *string
	createdAt                 string
}

func (s *stubDB) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	if strings.Contains(query, "INSERT INTO smeldr_tokens") {
		s.rows = append(s.rows, stubTokenRow{
			id:        args[0].(string),
			name:      args[1].(string),
			role:      args[2].(string),
			expiresAt: args[3].(string),
			createdAt: args[4].(string),
		})
		return nil, nil
	}
	if strings.Contains(query, "UPDATE smeldr_tokens SET revoked_at") {
		ts := args[0].(string)
		id := args[1].(string)
		for i := range s.rows {
			if s.rows[i].id == id {
				s.rows[i].revokedAt = &ts
				return nil, nil
			}
		}
		return nil, nil
	}
	return nil, errors.New("stubDB: unhandled ExecContext query")
}

func (s *stubDB) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, errors.New("stubDB: QueryContext not used in these tests")
}

func (s *stubDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if strings.Contains(query, "SELECT role FROM smeldr_tokens") {
		id := args[0].(string)
		for _, r := range s.rows {
			if r.id == id {
				conn := &guardRowConn{val: r.role}
				return sql.OpenDB(conn).QueryRowContext(ctx, "SELECT v")
			}
		}
		conn := &guardRowConn{noRow: true}
		return sql.OpenDB(conn).QueryRowContext(ctx, "SELECT v")
	}
	if strings.Contains(query, "COUNT(*)") {
		now := args[0].(string)
		excludeID := args[1].(string)
		count := int64(0)
		for _, r := range s.rows {
			if r.id == excludeID || r.role != "admin" || r.revokedAt != nil {
				continue
			}
			if r.expiresAt > now {
				count++
			}
		}
		conn := &guardRowConn{val: count}
		return sql.OpenDB(conn).QueryRowContext(ctx, "SELECT v")
	}
	return nil
}

func TestTokenStoreCreate(t *testing.T) {
	secret := "test-secret-32-bytes-xxxxxxxxxxxx"
	db := &stubDB{}
	store := NewTokenStore(db, secret)

	raw, err := store.Create(context.Background(), "CI Bot", "author", 24*time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if raw == "" {
		t.Fatal("Create returned empty token")
	}
	if len(db.rows) != 1 {
		t.Fatalf("expected 1 row in stubDB, got %d", len(db.rows))
	}
	row := db.rows[0]
	if row.name != "CI Bot" {
		t.Errorf("name: got %q want %q", row.name, "CI Bot")
	}
	if row.role != "author" {
		t.Errorf("role: got %q want %q", row.role, "author")
	}
	if row.id == "" {
		t.Error("fingerprint ID must be non-empty")
	}
	// Stored token must decode to the correct user.
	user, err := decodeToken(raw, secret)
	if err != nil {
		t.Fatalf("decodeToken: %v", err)
	}
	if user.Name != "CI Bot" {
		t.Errorf("user.Name: got %q want %q", user.Name, "CI Bot")
	}
	if len(user.Roles) != 1 || user.Roles[0] != Author {
		t.Errorf("user.Roles: got %v want [author]", user.Roles)
	}
}

func TestTokenStoreRevoke(t *testing.T) {
	secret := "test-secret-32-bytes-xxxxxxxxxxxx"
	db := &stubDB{}
	store := NewTokenStore(db, secret)

	_, err := store.Create(context.Background(), "Bot", "author", 24*time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id := db.rows[0].id

	if err := store.Revoke(context.Background(), id); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if db.rows[0].revokedAt == nil {
		t.Fatal("expected revokedAt to be set after Revoke")
	}
}

// — VerifyBearerToken with store ——————————————————————————————————————————

// rowDB implements smeldr.DB with a QueryRowContext that returns a pre-built
// *sql.Row via sql.OpenDB + a custom driver.Connector. Avoids sql.Register.
type rowDB struct {
	revokedAt *string // nil → active; non-nil pointer → revoked; use errNoRow sentinel
	noRow     bool    // true → simulate "not found" (empty result set)
}

func (r *rowDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return nil, nil
}
func (r *rowDB) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, nil
}
func (r *rowDB) QueryRowContext(ctx context.Context, _ string, _ ...any) *sql.Row {
	conn := &rowDriverConn{revokedAt: r.revokedAt, noRow: r.noRow}
	db := sql.OpenDB(conn)
	return db.QueryRowContext(ctx, "SELECT revoked_at")
}

// rowDriverConn implements driver.Connector, driver.Conn, driver.Stmt, and
// driver.Rows as a single struct to keep the stub self-contained.
type rowDriverConn struct {
	revokedAt *string
	noRow     bool
	done      bool
}

func (c *rowDriverConn) Connect(_ context.Context) (driver.Conn, error) {
	return &rowDriverConn{revokedAt: c.revokedAt, noRow: c.noRow}, nil
}
func (c *rowDriverConn) Driver() driver.Driver                        { return dummyDriver{} }
func (c *rowDriverConn) Prepare(_ string) (driver.Stmt, error)        { return c, nil }
func (c *rowDriverConn) Close() error                                 { return nil }
func (c *rowDriverConn) Begin() (driver.Tx, error)                    { return nil, nil }
func (c *rowDriverConn) NumInput() int                                { return -1 }
func (c *rowDriverConn) Exec(_ []driver.Value) (driver.Result, error) { return nil, nil }
func (c *rowDriverConn) Query(_ []driver.Value) (driver.Rows, error)  { return c, nil }
func (c *rowDriverConn) Columns() []string                            { return []string{"revoked_at"} }
func (c *rowDriverConn) Next(dest []driver.Value) error {
	if c.done || c.noRow {
		return io.EOF // sql.Row.Scan returns sql.ErrNoRows
	}
	c.done = true
	if c.revokedAt == nil {
		dest[0] = nil
	} else {
		dest[0] = *c.revokedAt
	}
	return nil
}

// dummyDriver satisfies driver.Driver; not used since we use sql.OpenDB.
type dummyDriver struct{}

func (dummyDriver) Open(_ string) (driver.Conn, error) { return nil, nil }

// guardRowConn implements sql driver.Connector for a single-value row.
// Used by stubDB.QueryRowContext to satisfy the last-admin guard queries.
type guardRowConn struct {
	val   driver.Value
	noRow bool
	done  bool
}

func (c *guardRowConn) Connect(_ context.Context) (driver.Conn, error) {
	return &guardRowConn{val: c.val, noRow: c.noRow}, nil
}
func (c *guardRowConn) Driver() driver.Driver                        { return dummyDriver{} }
func (c *guardRowConn) Prepare(_ string) (driver.Stmt, error)        { return c, nil }
func (c *guardRowConn) Close() error                                 { return nil }
func (c *guardRowConn) Begin() (driver.Tx, error)                    { return nil, nil }
func (c *guardRowConn) NumInput() int                                { return -1 }
func (c *guardRowConn) Exec(_ []driver.Value) (driver.Result, error) { return nil, nil }
func (c *guardRowConn) Query(_ []driver.Value) (driver.Rows, error)  { return c, nil }
func (c *guardRowConn) Columns() []string                            { return []string{"v"} }
func (c *guardRowConn) Next(dest []driver.Value) error {
	if c.done || c.noRow {
		return io.EOF
	}
	c.done = true
	dest[0] = c.val
	return nil
}

func TestTokenStore_Revoke_lastAdmin(t *testing.T) {
	secret := "test-secret-32-bytes-xxxxxxxxxxxx"

	t.Run("only_admin_blocked", func(t *testing.T) {
		db := &stubDB{}
		store := NewTokenStore(db, secret)
		_, err := store.Create(context.Background(), "Admin", "admin", 24*time.Hour)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		id := db.rows[0].id
		err = store.Revoke(context.Background(), id)
		if !errors.Is(err, ErrLastAdmin) {
			t.Fatalf("expected ErrLastAdmin, got %v", err)
		}
		if db.rows[0].revokedAt != nil {
			t.Fatal("row must not be modified when guard fires")
		}
	})

	t.Run("two_admins_allowed", func(t *testing.T) {
		db := &stubDB{}
		store := NewTokenStore(db, secret)
		_, err := store.Create(context.Background(), "Admin1", "admin", 24*time.Hour)
		if err != nil {
			t.Fatalf("Create admin1: %v", err)
		}
		_, err = store.Create(context.Background(), "Admin2", "admin", 24*time.Hour)
		if err != nil {
			t.Fatalf("Create admin2: %v", err)
		}
		id := db.rows[0].id
		if err := store.Revoke(context.Background(), id); err != nil {
			t.Fatalf("Revoke: %v", err)
		}
		if db.rows[0].revokedAt == nil {
			t.Fatal("expected revokedAt to be set after Revoke")
		}
	})

	t.Run("non_admin_not_guarded", func(t *testing.T) {
		db := &stubDB{}
		store := NewTokenStore(db, secret)
		_, err := store.Create(context.Background(), "Editor", "editor", 24*time.Hour)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		id := db.rows[0].id
		if err := store.Revoke(context.Background(), id); err != nil {
			t.Fatalf("Revoke: %v", err)
		}
		if db.rows[0].revokedAt == nil {
			t.Fatal("expected revokedAt to be set after Revoke")
		}
	})
}

func TestVerifyBearerTokenWithStore(t *testing.T) {
	secret := []byte("test-secret-32-bytes-xxxxxxxxxxxx")

	t.Run("managed token absent from DB is rejected", func(t *testing.T) {
		store := NewTokenStore(&rowDB{noRow: true}, string(secret))
		u := User{ID: "u1", Name: "Alice", Roles: []Role{Editor}}
		tok, _ := SignToken(u, string(secret), 0)
		r, _ := http.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Authorization", "Bearer "+tok)
		_, ok := VerifyBearerToken(r, secret, store)
		if ok {
			t.Fatal("expected rejection when token fingerprint not in DB")
		}
	})

	t.Run("revoked token is rejected", func(t *testing.T) {
		revoked := "2026-01-01T00:00:00Z"
		store := NewTokenStore(&rowDB{revokedAt: &revoked}, string(secret))
		u := User{ID: "u2", Name: "Bob", Roles: []Role{Author}}
		tok, _ := SignToken(u, string(secret), 0)
		r, _ := http.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Authorization", "Bearer "+tok)
		_, ok := VerifyBearerToken(r, secret, store)
		if ok {
			t.Fatal("expected rejection for revoked token")
		}
	})

	t.Run("valid managed token is accepted", func(t *testing.T) {
		store := NewTokenStore(&rowDB{revokedAt: nil}, string(secret))
		u := User{ID: "u3", Name: "Carol", Roles: []Role{Editor}}
		tok, _ := SignToken(u, string(secret), 0)
		r, _ := http.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Authorization", "Bearer "+tok)
		got, ok := VerifyBearerToken(r, secret, store)
		if !ok {
			t.Fatal("expected accepted for valid non-revoked managed token")
		}
		if got.ID != "u3" {
			t.Errorf("user ID: got %q want %q", got.ID, "u3")
		}
	})
}

// — ensureBootstrap ————————————————————————————————————————————————————————

// bootstrapDB is a minimal DB stub for ensureBootstrap tests.
// It returns a configurable COUNT(*) value and tracks INSERT calls.
type bootstrapDB struct {
	count       int // rows in smeldr_tokens (returned by COUNT(*))
	insertCount int // number of INSERT INTO smeldr_tokens calls
}

func (b *bootstrapDB) ExecContext(_ context.Context, query string, _ ...any) (sql.Result, error) {
	if strings.Contains(query, "INSERT INTO smeldr_tokens") {
		b.insertCount++
		return nil, nil
	}
	return nil, errors.New("bootstrapDB: unhandled ExecContext")
}

func (b *bootstrapDB) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, errors.New("bootstrapDB: QueryContext not used")
}

func (b *bootstrapDB) QueryRowContext(ctx context.Context, query string, _ ...any) *sql.Row {
	if strings.Contains(query, "COUNT(*)") {
		conn := &guardRowConn{val: int64(b.count)}
		return sql.OpenDB(conn).QueryRowContext(ctx, "SELECT v")
	}
	conn := &guardRowConn{noRow: true}
	return sql.OpenDB(conn).QueryRowContext(ctx, "SELECT v")
}

func TestTokenStore_ensureBootstrap_empty(t *testing.T) {
	db := &bootstrapDB{count: 0}
	store := NewTokenStore(db, "test-secret-32-bytes-xxxxxxxxxxxx")
	store.ensureBootstrap(context.Background())
	if db.insertCount != 1 {
		t.Errorf("ensureBootstrap on empty table: expected 1 INSERT (Create), got %d", db.insertCount)
	}
}

func TestTokenStore_ensureBootstrap_nonEmpty(t *testing.T) {
	db := &bootstrapDB{count: 1}
	store := NewTokenStore(db, "test-secret-32-bytes-xxxxxxxxxxxx")
	store.ensureBootstrap(context.Background())
	if db.insertCount != 0 {
		t.Errorf("ensureBootstrap on non-empty table: expected no INSERT (no-op), got %d", db.insertCount)
	}
}

// — Preview token tests ———————————————————————————————————————————————————

func TestEncodeDecodePreviewToken(t *testing.T) {
	secret := []byte("test-secret-32-bytes-xxxxxxxxxxxx")
	const prefix = "posts"
	const slug = "my-draft"

	t.Run("valid token returns correct prefix and slug", func(t *testing.T) {
		token := encodePreviewToken(prefix, slug, secret, time.Hour)
		gotPrefix, gotSlug, err := decodePreviewToken(token, secret)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotPrefix != prefix {
			t.Errorf("prefix: got %q, want %q", gotPrefix, prefix)
		}
		if gotSlug != slug {
			t.Errorf("slug: got %q, want %q", gotSlug, slug)
		}
	})

	t.Run("expired token returns error", func(t *testing.T) {
		token := encodePreviewToken(prefix, slug, secret, -time.Second)
		_, _, err := decodePreviewToken(token, secret)
		if err == nil {
			t.Fatal("expected error for expired token, got nil")
		}
	})

	t.Run("wrong secret returns error", func(t *testing.T) {
		token := encodePreviewToken(prefix, slug, secret, time.Hour)
		_, _, err := decodePreviewToken(token, []byte("different-secret-xxxxxxxxxxxxxxxxxxxxx"))
		if err == nil {
			t.Fatal("expected error for wrong secret, got nil")
		}
	})

	t.Run("tampered payload returns error", func(t *testing.T) {
		token := encodePreviewToken(prefix, slug, secret, time.Hour)
		// Flip a character in the payload portion (before the dot)
		bs := []byte(token)
		bs[0] ^= 0x01
		_, _, err := decodePreviewToken(string(bs), secret)
		if err == nil {
			t.Fatal("expected error for tampered token, got nil")
		}
	})

	t.Run("malformed token (no dot) returns error", func(t *testing.T) {
		_, _, err := decodePreviewToken("nodothere", secret)
		if err == nil {
			t.Fatal("expected error for malformed token, got nil")
		}
	})

	t.Run("cross-module: different prefix in token vs check", func(t *testing.T) {
		// Token is for "posts" but caller checks "docs" — caller responsibility
		token := encodePreviewToken("posts", slug, secret, time.Hour)
		gotPrefix, _, err := decodePreviewToken(token, secret)
		if err != nil {
			t.Fatalf("unexpected decode error: %v", err)
		}
		// The decoded prefix must be "posts", not "docs"
		if gotPrefix == "docs" {
			t.Error("cross-module: decoded prefix should not match 'docs'")
		}
	})
}

func TestEncodeDecodeUploadToken(t *testing.T) {
	secret := []byte("test-secret-upload")

	t.Run("valid token round-trips without error", func(t *testing.T) {
		token := encodeUploadToken(secret, time.Hour)
		if err := decodeUploadToken(token, secret); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})

	t.Run("expired token returns ErrUnauth", func(t *testing.T) {
		token := encodeUploadToken(secret, -time.Second)
		if err := decodeUploadToken(token, secret); err == nil {
			t.Fatal("expected error for expired token, got nil")
		}
	})

	t.Run("wrong secret returns ErrUnauth", func(t *testing.T) {
		token := encodeUploadToken(secret, time.Hour)
		if err := decodeUploadToken(token, []byte("wrong-secret")); err == nil {
			t.Fatal("expected error for wrong secret, got nil")
		}
	})

	t.Run("tampered payload returns ErrUnauth", func(t *testing.T) {
		token := encodeUploadToken(secret, time.Hour)
		tampered := "X" + token[1:]
		if err := decodeUploadToken(tampered, secret); err == nil {
			t.Fatal("expected error for tampered token, got nil")
		}
	})
}

// — VerifyTokenString —————————————————————————————————————————————————————

func TestVerifyTokenString_noStore(t *testing.T) {
	secret := []byte(testSecret)
	original := User{ID: "u1", Name: "Alice", Roles: []Role{Editor}}
	tok, err := SignToken(original, testSecret, 0)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	user, ok := VerifyTokenString(tok, secret, nil)
	if !ok {
		t.Fatal("VerifyTokenString: expected ok=true")
	}
	if user.ID != original.ID {
		t.Errorf("User.ID: got %q, want %q", user.ID, original.ID)
	}
}

func TestVerifyTokenString_invalid(t *testing.T) {
	user, ok := VerifyTokenString("not-a-valid-token", []byte(testSecret), nil)
	if ok {
		t.Error("expected ok=false for invalid token")
	}
	if user.ID != GuestUser.ID {
		t.Errorf("expected GuestUser, got %+v", user)
	}
}

// newTestTokensDB creates an in-memory SQLite DB with the smeldr_tokens table.
func newTestTokensDB(t *testing.T) *sql.DB {
	t.Helper()
	db := newSQLiteDB(t)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
		CREATE TABLE smeldr_tokens (
			id         TEXT PRIMARY KEY,
			name       TEXT NOT NULL,
			role       TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			revoked_at TEXT,
			created_at TEXT NOT NULL
		)`)
	if err != nil {
		t.Fatalf("create smeldr_tokens: %v", err)
	}
	return db
}

func TestVerifyTokenString_withStore_valid(t *testing.T) {
	db := newTestTokensDB(t)
	ts := NewTokenStore(db, testSecret)
	ctx := context.Background()

	raw, err := ts.Create(ctx, "test-token", "editor", time.Hour)
	if err != nil {
		t.Fatalf("ts.Create: %v", err)
	}

	user, ok := VerifyTokenString(raw, []byte(testSecret), ts)
	if !ok {
		t.Fatal("expected ok=true for valid, non-revoked token")
	}
	if !user.HasRole(Editor) {
		t.Error("expected user to have Editor role")
	}
}

func TestVerifyTokenString_withStore_revoked(t *testing.T) {
	db := newTestTokensDB(t)
	ts := NewTokenStore(db, testSecret)
	ctx := context.Background()

	raw, err := ts.Create(ctx, "to-revoke", "editor", time.Hour)
	if err != nil {
		t.Fatalf("ts.Create: %v", err)
	}
	// Determine the fingerprint and revoke it directly.
	records, err := ts.List(ctx)
	if err != nil || len(records) == 0 {
		t.Fatalf("ts.List: %v / %d", err, len(records))
	}
	if err := ts.Revoke(ctx, records[0].ID); err != nil {
		t.Fatalf("ts.Revoke: %v", err)
	}

	user, ok := VerifyTokenString(raw, []byte(testSecret), ts)
	if ok {
		t.Error("expected ok=false for revoked token")
	}
	if user.ID != GuestUser.ID {
		t.Errorf("expected GuestUser for revoked token, got %+v", user)
	}
}

func TestVerifyTokenString_withStore_notFound(t *testing.T) {
	db := newTestTokensDB(t)
	ts := NewTokenStore(db, testSecret)

	// Token is cryptographically valid but fingerprint is absent from the table.
	raw, err := SignToken(User{ID: "x", Roles: []Role{Editor}}, testSecret, time.Hour)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}

	user, ok := VerifyTokenString(raw, []byte(testSecret), ts)
	if ok {
		t.Error("expected ok=false when fingerprint is absent from table")
	}
	if user.ID != GuestUser.ID {
		t.Errorf("expected GuestUser, got %+v", user)
	}
}

// — decodeToken error paths ————————————————————————————————————————————————

func TestDecodeToken_invalidBase64(t *testing.T) {
	badPayload := "!invalid!"
	sig := tokenHMAC(badPayload, testSecret)
	_, err := decodeToken(badPayload+"."+sig, testSecret)
	if !errors.Is(err, ErrUnauth) {
		t.Errorf("expected ErrUnauth, got %v", err)
	}
}

func TestDecodeToken_invalidJSON(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte("not-json"))
	sig := tokenHMAC(payload, testSecret)
	_, err := decodeToken(payload+"."+sig, testSecret)
	if !errors.Is(err, ErrUnauth) {
		t.Errorf("expected ErrUnauth, got %v", err)
	}
}

// — decodePreviewToken error paths ————————————————————————————————————————

func TestDecodePreviewToken_invalidBase64(t *testing.T) {
	badPayload := "!invalid!"
	sig := tokenHMAC(badPayload, testSecret)
	_, _, err := decodePreviewToken(badPayload+"."+sig, []byte(testSecret))
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDecodePreviewToken_fieldsNotThree(t *testing.T) {
	raw := base64.RawURLEncoding.EncodeToString([]byte("onepart"))
	sig := tokenHMAC(raw, testSecret)
	_, _, err := decodePreviewToken(raw+"."+sig, []byte(testSecret))
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// — decodeUploadToken error paths —————————————————————————————————————————

func TestDecodeUploadToken_noDot(t *testing.T) {
	if err := decodeUploadToken("nodots", []byte(testSecret)); !errors.Is(err, ErrUnauth) {
		t.Errorf("expected ErrUnauth, got %v", err)
	}
}

func TestDecodeUploadToken_invalidBase64(t *testing.T) {
	badPayload := "!invalid!"
	sig := tokenHMAC(badPayload, testSecret)
	if err := decodeUploadToken(badPayload+"."+sig, []byte(testSecret)); !errors.Is(err, ErrUnauth) {
		t.Errorf("expected ErrUnauth, got %v", err)
	}
}

// — TokenStore DB error path stubs ————————————————————————————————————————

// errExecDB always fails ExecContext.
type errExecDB struct{}

func (e *errExecDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return nil, errors.New("exec error")
}
func (e *errExecDB) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, nil
}
func (e *errExecDB) QueryRowContext(ctx context.Context, _ string, _ ...any) *sql.Row {
	conn := &guardRowConn{noRow: true}
	return sql.OpenDB(conn).QueryRowContext(ctx, "SELECT v")
}

// errQueryDB always fails QueryContext.
type errQueryDB struct{}

func (e *errQueryDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return nil, nil
}
func (e *errQueryDB) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, errors.New("query error")
}
func (e *errQueryDB) QueryRowContext(ctx context.Context, _ string, _ ...any) *sql.Row {
	conn := &guardRowConn{noRow: true}
	return sql.OpenDB(conn).QueryRowContext(ctx, "SELECT v")
}

// errRowConn is a sql driver.Connector whose Next returns a non-EOF error,
// causing sql.Row.Scan to return an error that is not sql.ErrNoRows.
type errRowConn struct{}

func (c *errRowConn) Connect(_ context.Context) (driver.Conn, error) { return c, nil }
func (c *errRowConn) Driver() driver.Driver                          { return dummyDriver{} }
func (c *errRowConn) Prepare(_ string) (driver.Stmt, error)          { return c, nil }
func (c *errRowConn) Close() error                                   { return nil }
func (c *errRowConn) Begin() (driver.Tx, error)                      { return nil, nil }
func (c *errRowConn) NumInput() int                                  { return -1 }
func (c *errRowConn) Exec(_ []driver.Value) (driver.Result, error)   { return nil, nil }
func (c *errRowConn) Query(_ []driver.Value) (driver.Rows, error)    { return c, nil }
func (c *errRowConn) Columns() []string                              { return []string{"v"} }
func (c *errRowConn) Next(_ []driver.Value) error                    { return errors.New("scan error") }

// revokeRoleErrDB makes every QueryRowContext fail with a scan error.
type revokeRoleErrDB struct{}

func (r *revokeRoleErrDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return nil, nil
}
func (r *revokeRoleErrDB) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, nil
}
func (r *revokeRoleErrDB) QueryRowContext(ctx context.Context, _ string, _ ...any) *sql.Row {
	conn := &errRowConn{}
	return sql.OpenDB(conn).QueryRowContext(ctx, "SELECT v")
}

// revokeAdminCountErrDB returns "admin" for the first QueryRowContext call
// and fails the second, covering the admin-guard COUNT error path.
type revokeAdminCountErrDB struct{ calls int }

func (r *revokeAdminCountErrDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return nil, nil
}
func (r *revokeAdminCountErrDB) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, nil
}
func (r *revokeAdminCountErrDB) QueryRowContext(ctx context.Context, _ string, _ ...any) *sql.Row {
	r.calls++
	if r.calls == 1 {
		conn := &guardRowConn{val: "admin"}
		return sql.OpenDB(conn).QueryRowContext(ctx, "SELECT v")
	}
	conn := &errRowConn{}
	return sql.OpenDB(conn).QueryRowContext(ctx, "SELECT v")
}

// revokeUpdateErrDB returns no-row (role = "", not admin) and fails ExecContext.
type revokeUpdateErrDB struct{}

func (r *revokeUpdateErrDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return nil, errors.New("update error")
}
func (r *revokeUpdateErrDB) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, nil
}
func (r *revokeUpdateErrDB) QueryRowContext(ctx context.Context, _ string, _ ...any) *sql.Row {
	conn := &guardRowConn{noRow: true}
	return sql.OpenDB(conn).QueryRowContext(ctx, "SELECT v")
}

// bootstrapFailInsertDB returns count=0 (empty) but fails ExecContext,
// covering ensureBootstrap's "Create fails" warning path.
type bootstrapFailInsertDB struct{}

func (b *bootstrapFailInsertDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return nil, errors.New("insert failed")
}
func (b *bootstrapFailInsertDB) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, errors.New("not used")
}
func (b *bootstrapFailInsertDB) QueryRowContext(ctx context.Context, _ string, _ ...any) *sql.Row {
	conn := &guardRowConn{val: int64(0)}
	return sql.OpenDB(conn).QueryRowContext(ctx, "SELECT v")
}

// — TokenStore error tests ————————————————————————————————————————————————

func TestTokenStore_Create_execError(t *testing.T) {
	store := NewTokenStore(&errExecDB{}, testSecret)
	_, err := store.Create(context.Background(), "test", "author", time.Hour)
	if !errors.Is(err, ErrInternal) {
		t.Errorf("expected ErrInternal, got %v", err)
	}
}

func TestTokenStore_List_queryError(t *testing.T) {
	store := NewTokenStore(&errQueryDB{}, testSecret)
	_, err := store.List(context.Background())
	if !errors.Is(err, ErrInternal) {
		t.Errorf("expected ErrInternal, got %v", err)
	}
}

func TestTokenStore_List_revokedToken(t *testing.T) {
	db := newTestTokensDB(t)
	ts := NewTokenStore(db, testSecret)
	ctx := context.Background()

	if _, err := ts.Create(ctx, "tok", "editor", time.Hour); err != nil {
		t.Fatalf("Create: %v", err)
	}
	records, err := ts.List(ctx)
	if err != nil {
		t.Fatalf("List before revoke: %v", err)
	}
	if err := ts.Revoke(ctx, records[0].ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	records, err = ts.List(ctx)
	if err != nil {
		t.Fatalf("List after revoke: %v", err)
	}
	if records[0].RevokedAt.IsZero() {
		t.Error("expected RevokedAt to be non-zero after revoke")
	}
}

func TestTokenStore_Revoke_roleQueryError(t *testing.T) {
	store := NewTokenStore(&revokeRoleErrDB{}, testSecret)
	if err := store.Revoke(context.Background(), "some-id"); !errors.Is(err, ErrInternal) {
		t.Errorf("expected ErrInternal, got %v", err)
	}
}

func TestTokenStore_Revoke_adminCountError(t *testing.T) {
	store := NewTokenStore(&revokeAdminCountErrDB{}, testSecret)
	if err := store.Revoke(context.Background(), "some-id"); !errors.Is(err, ErrInternal) {
		t.Errorf("expected ErrInternal, got %v", err)
	}
}

func TestTokenStore_Revoke_updateError(t *testing.T) {
	store := NewTokenStore(&revokeUpdateErrDB{}, testSecret)
	if err := store.Revoke(context.Background(), "some-id"); !errors.Is(err, ErrInternal) {
		t.Errorf("expected ErrInternal, got %v", err)
	}
}

func TestTokenStore_ensureBootstrap_createFails(t *testing.T) {
	store := NewTokenStore(&bootstrapFailInsertDB{}, testSecret)
	store.ensureBootstrap(context.Background())
}

func TestTokenStore_probeTable_success(t *testing.T) {
	db := newTestTokensDB(t)
	ts := NewTokenStore(db, testSecret)
	if err := ts.probeTable(context.Background()); err != nil {
		t.Errorf("probeTable: %v", err)
	}
}

func TestTokenStore_List_empty(t *testing.T) {
	db := newTestTokensDB(t)
	ts := NewTokenStore(db, testSecret)

	records, err := ts.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("List on empty table: got %d records, want 0", len(records))
	}
}

func TestTokenStore_List_withRecords(t *testing.T) {
	db := newTestTokensDB(t)
	ts := NewTokenStore(db, testSecret)
	ctx := context.Background()

	if _, err := ts.Create(ctx, "first", "author", time.Hour); err != nil {
		t.Fatalf("Create first: %v", err)
	}
	if _, err := ts.Create(ctx, "second", "editor", time.Hour); err != nil {
		t.Fatalf("Create second: %v", err)
	}

	records, err := ts.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("List: got %d records, want 2", len(records))
	}
	names := map[string]bool{records[0].Name: true, records[1].Name: true}
	if !names["first"] || !names["second"] {
		t.Errorf("List: unexpected names %q, %q", records[0].Name, records[1].Name)
	}
}
