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

// stubDB implements smeldr.DB using an in-memory slice for forge_tokens rows.
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
	if strings.Contains(query, "INSERT INTO forge_tokens") {
		s.rows = append(s.rows, stubTokenRow{
			id:        args[0].(string),
			name:      args[1].(string),
			role:      args[2].(string),
			expiresAt: args[3].(string),
			createdAt: args[4].(string),
		})
		return nil, nil
	}
	if strings.Contains(query, "UPDATE forge_tokens SET revoked_at") {
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
	if strings.Contains(query, "SELECT role FROM forge_tokens") {
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
	count       int // rows in forge_tokens (returned by COUNT(*))
	insertCount int // number of INSERT INTO forge_tokens calls
}

func (b *bootstrapDB) ExecContext(_ context.Context, query string, _ ...any) (sql.Result, error) {
	if strings.Contains(query, "INSERT INTO forge_tokens") {
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
