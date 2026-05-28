package smeldr

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// AuthFunc authenticates an incoming HTTP request and returns the identified
// User and whether authentication succeeded. Use [BearerHMAC], [CookieSession],
// [BasicAuth], or [AnyAuth] to obtain an AuthFunc. Implement this interface to
// provide a custom authentication scheme.
//
// The unexported authenticate method is intentional: it prevents accidental
// direct calls and allows future additions to the interface without breaking
// existing implementations (consistent with [Option] and [Signal]).
type AuthFunc interface {
	authenticate(*http.Request) (User, bool)
}

// productionWarner is an optional capability interface implemented by AuthFunc
// values that should emit a warning when used outside of development.
// Step 11 (forge.go) type-asserts each registered AuthFunc to this interface.
type productionWarner interface {
	warnIfProduction(w io.Writer)
}

// csrfAware is an optional capability interface implemented by AuthFunc values
// that manage CSRF validation. Step 9 (middleware.go) type-asserts each registered
// AuthFunc to decide whether to validate CSRF tokens on non-safe HTTP methods.
type csrfAware interface {
	csrfEnabled() bool
}

// CSRFCookieName is the name of the CSRF cookie set by [CookieSession].
// Client-side AJAX code should read this cookie and send its value as the
// X-CSRF-Token request header on all non-safe methods (POST, PUT, PATCH, DELETE).
const CSRFCookieName = "smeldr_csrf"

// WithoutCSRF is an [Option] passed to [CookieSession] to disable automatic
// CSRF protection. This is strongly discouraged for production use.
var WithoutCSRF Option = withoutCSRFOption{}

// withoutCSRFOption is the unexported implementation of the WithoutCSRF option.
type withoutCSRFOption struct{}

func (withoutCSRFOption) isOption() {}

// HasRole reports whether the user holds at least the given role level.
// This is hierarchical: an Admin satisfies HasRole(smeldr.Editor).
// Delegates to the free function [HasRole] in roles.go.
func (u User) HasRole(role Role) bool {
	return HasRole(u.Roles, role)
}

// Is reports whether the user holds exactly the given role (exact match only).
// An Admin does not satisfy Is(smeldr.Editor).
// Delegates to the free function [IsRole] in roles.go.
func (u User) Is(role Role) bool {
	return IsRole(u.Roles, role)
}

// tokenPayload is the JSON structure embedded in signed tokens and session cookies.
type tokenPayload struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Roles []string `json:"roles"`
	Exp   int64    `json:"exp,omitempty"` // Unix seconds; 0 means no expiry
}

// encodeToken JSON-marshals user, computes HMAC-SHA256 over the base64url payload,
// and returns "payload.signature" (both base64url-encoded, no padding).
// When ttl > 0 an expiry timestamp is embedded in the payload.
func encodeToken(user User, secret string, ttl time.Duration) (string, error) {
	roles := make([]string, len(user.Roles))
	for i, r := range user.Roles {
		roles[i] = string(r)
	}

	var exp int64
	if ttl > 0 {
		exp = time.Now().Add(ttl).Unix()
	}

	raw, err := json.Marshal(tokenPayload{ID: user.ID, Name: user.Name, Roles: roles, Exp: exp})
	if err != nil {
		// json.Marshal on tokenPayload (string/[]string/int64 fields) is
		// unreachable in practice; return a smeldr.Error per Decision 16.
		return "", ErrInternal
	}

	payload := base64.RawURLEncoding.EncodeToString(raw)
	sig := tokenHMAC(payload, secret)
	return payload + "." + sig, nil
}

// decodeToken reverses encodeToken. Returns [ErrUnauth] if the token is missing,
// malformed, or the HMAC signature does not match.
func decodeToken(token, secret string) (User, error) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return GuestUser, ErrUnauth
	}
	payload, sig := parts[0], parts[1]

	expected := tokenHMAC(payload, secret)
	if subtle.ConstantTimeCompare([]byte(sig), []byte(expected)) != 1 {
		return GuestUser, ErrUnauth
	}

	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return GuestUser, ErrUnauth
	}

	var p tokenPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return GuestUser, ErrUnauth
	}

	// Reject expired tokens.
	if p.Exp != 0 && time.Now().Unix() > p.Exp {
		return GuestUser, ErrUnauth
	}

	roles := make([]Role, len(p.Roles))
	for i, r := range p.Roles {
		roles[i] = Role(r)
	}

	return User{ID: p.ID, Name: p.Name, Roles: roles}, nil
}

// tokenHMAC computes a base64url-encoded (no padding) HMAC-SHA256 of payload
// using secret as the key.
func tokenHMAC(payload, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// encodePreviewToken returns a signed preview token binding prefix and slug,
// valid for ttl. The payload encodes prefix, slug, and expiry as a
// colon-separated string before signing so that a token for one module
// prefix cannot be replayed on a different module.
//
// Token format: base64url(prefix+":"+slug+":"+expUnix) + "." + tokenHMAC(payload, secret)
func encodePreviewToken(prefix, slug string, secret []byte, ttl time.Duration) string {
	exp := strconv.FormatInt(time.Now().Add(ttl).Unix(), 10)
	payload := base64.RawURLEncoding.EncodeToString([]byte(prefix + ":" + slug + ":" + exp))
	return payload + "." + tokenHMAC(payload, string(secret))
}

// decodePreviewToken validates the HMAC signature and expiry of a preview token
// produced by [encodePreviewToken]. Returns the prefix and slug bound to the
// token, or [ErrNotFound] if the token is missing, malformed, expired, or the
// signature does not match. Comparison is constant-time to prevent timing attacks.
func decodePreviewToken(token string, secret []byte) (prefix, slug string, err error) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return "", "", ErrNotFound
	}
	payload, sig := parts[0], parts[1]

	expected := tokenHMAC(payload, string(secret))
	if subtle.ConstantTimeCompare([]byte(sig), []byte(expected)) != 1 {
		return "", "", ErrNotFound
	}

	raw, decErr := base64.RawURLEncoding.DecodeString(payload)
	if decErr != nil {
		return "", "", ErrNotFound
	}
	// payload is "prefix:slug:expUnix" — split on ":" with a max of 3 parts
	// so slugs or prefixes that happen to contain no colons are handled safely.
	fields := strings.SplitN(string(raw), ":", 3)
	if len(fields) != 3 {
		return "", "", ErrNotFound
	}
	expUnix, convErr := strconv.ParseInt(fields[2], 10, 64)
	if convErr != nil || time.Now().Unix() > expUnix {
		return "", "", ErrNotFound
	}
	return fields[0], fields[1], nil
}

// encodeUploadToken returns a short-lived HMAC-SHA256-signed upload token.
// Unlike preview tokens the token carries no slug or prefix — it authorises
// any upload to POST /media within the TTL.
//
// Token format: base64url(expUnix) + "." + base64url(hmac-sha256(secret, payload))
func encodeUploadToken(secret []byte, ttl time.Duration) string {
	exp := strconv.FormatInt(time.Now().Add(ttl).Unix(), 10)
	payload := base64.RawURLEncoding.EncodeToString([]byte(exp))
	return payload + "." + tokenHMAC(payload, string(secret))
}

// decodeUploadToken validates the HMAC signature and expiry of an upload token
// produced by [encodeUploadToken]. Returns [ErrUnauth] if the token is
// malformed, expired, or the signature does not match. Comparison is
// constant-time to prevent timing attacks.
func decodeUploadToken(token string, secret []byte) error {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return ErrUnauth
	}
	payload, sig := parts[0], parts[1]

	expected := tokenHMAC(payload, string(secret))
	if subtle.ConstantTimeCompare([]byte(sig), []byte(expected)) != 1 {
		return ErrUnauth
	}

	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return ErrUnauth
	}
	expUnix, convErr := strconv.ParseInt(string(raw), 10, 64)
	if convErr != nil || time.Now().Unix() > expUnix {
		return ErrUnauth
	}
	return nil
}

// SignToken produces a signed token encoding the given User. Pass the token to
// the client (e.g. as a JSON response body); validate it later with [BearerHMAC]
// or [CookieSession].
//
// When ttl > 0 the token contains an expiry timestamp; [decodeToken] rejects
// tokens whose expiry has passed. Use ttl = 0 for tokens with no expiry.
//
// The token format is: base64url(json(User)) + "." + base64url(hmac-sha256(secret, payload)).
// Roles are stored as strings for forward compatibility (Decision 15).
func SignToken(user User, secret string, ttl time.Duration) (string, error) {
	return encodeToken(user, secret, ttl)
}

// — BearerHMAC —————————————————————————————————————————————————————————————

// bearerAuthFn implements [AuthFunc] for HMAC-signed bearer tokens.
type bearerAuthFn struct {
	secret string
}

// BearerHMAC returns an [AuthFunc] that validates HMAC-signed bearer tokens
// from the Authorization header (format: "Bearer <token>"). Generate tokens
// with [SignToken].
func BearerHMAC(secret string) AuthFunc {
	return &bearerAuthFn{secret: secret}
}

func (b *bearerAuthFn) authenticate(r *http.Request) (User, bool) {
	hdr := r.Header.Get("Authorization")
	if !strings.HasPrefix(hdr, "Bearer ") {
		return GuestUser, false
	}
	token := strings.TrimPrefix(hdr, "Bearer ")
	user, err := decodeToken(token, b.secret)
	if err != nil {
		return GuestUser, false
	}
	return user, true
}

// VerifyBearerToken extracts and verifies the HMAC-signed bearer token from r's
// Authorization header. It returns the authenticated [User] and true on success,
// or [GuestUser] and false if the header is absent, malformed, or the signature
// is invalid. secret must be the same value used to sign the token with [SignToken].
//
// When store is non-nil, VerifyBearerToken additionally checks the forge_tokens
// table: the token's SHA-256 fingerprint must be present and not revoked.
// Pass nil to skip database verification and use HMAC-only validation.
//
// This is the public counterpart to the unexported authenticate method on
// [BearerHMAC] and is intended for use outside the forge package (e.g. forge-mcp
// SSE transport) where [AuthFunc] is not directly callable.
func VerifyBearerToken(r *http.Request, secret []byte, store *TokenStore) (User, bool) {
	hdr := r.Header.Get("Authorization")
	if !strings.HasPrefix(hdr, "Bearer ") {
		return GuestUser, false
	}
	token := strings.TrimPrefix(hdr, "Bearer ")
	user, err := decodeToken(token, string(secret))
	if err != nil {
		return GuestUser, false
	}
	if store != nil {
		h := sha256.Sum256([]byte(token))
		id := hex.EncodeToString(h[:])
		row := store.db.QueryRowContext(r.Context(),
			`SELECT revoked_at FROM forge_tokens WHERE id = $1`, id,
		)
		var revokedAt *string
		if err := row.Scan(&revokedAt); err != nil {
			// Token fingerprint not in DB — not a managed token.
			return GuestUser, false
		}
		if revokedAt != nil {
			return GuestUser, false
		}
	}
	return user, true
}

// VerifyTokenString verifies a raw bearer token string without requiring an
// [http.Request]. It is otherwise identical to [VerifyBearerToken]: it decodes
// the HMAC signature, optionally checks the token fingerprint against the
// forge_tokens table, and returns the authenticated [User] and true on success.
//
// Use VerifyTokenString when the caller already holds the raw token value —
// for example, when a downstream server (forge-oauth) needs to validate a
// Forge token without constructing a synthetic HTTP request. For regular HTTP
// middleware, prefer [VerifyBearerToken] which extracts the token from the
// Authorization header itself.
//
// When store is non-nil, the lookup uses [context.Background].
func VerifyTokenString(token string, secret []byte, store *TokenStore) (User, bool) {
	user, err := decodeToken(token, string(secret))
	if err != nil {
		return GuestUser, false
	}
	if store != nil {
		h := sha256.Sum256([]byte(token))
		id := hex.EncodeToString(h[:])
		row := store.db.QueryRowContext(context.Background(),
			`SELECT revoked_at FROM forge_tokens WHERE id = $1`, id,
		)
		var revokedAt *string
		if err := row.Scan(&revokedAt); err != nil {
			return GuestUser, false
		}
		if revokedAt != nil {
			return GuestUser, false
		}
	}
	return user, true
}

// — TokenStore —————————————————————————————————————————————————————————————

// TokenRecord is a named bearer token entry stored in the forge_tokens table.
// Retrieve records with [TokenStore.List]; revoke with [TokenStore.Revoke].
type TokenRecord struct {
	// ID is the SHA-256 hex fingerprint of the raw token. Tokens are never
	// stored in plaintext; only this fingerprint is persisted.
	ID string

	// Name is the human-readable label provided when the token was created.
	Name string

	// Role is the role string assigned to this token (e.g. "author", "editor").
	Role string

	// ExpiresAt is the UTC time after which the token is no longer valid.
	ExpiresAt time.Time

	// RevokedAt is the UTC time at which this token was revoked. A zero value
	// means the token has not been revoked.
	RevokedAt time.Time

	// CreatedAt is the UTC time at which the token was created.
	CreatedAt time.Time
}

// TokenStore manages named, revocable bearer tokens stored in a forge_tokens
// database table. Use [NewTokenStore] to create one; wire it into
// [Config.TokenStore] to activate database-backed token verification.
//
// The forge_tokens table must exist before the application starts. Forge does
// not create or migrate it automatically. Required DDL:
//
//	CREATE TABLE forge_tokens (
//	    id         TEXT PRIMARY KEY,  -- SHA-256 hex fingerprint of the raw token
//	    name       TEXT NOT NULL,
//	    role       TEXT NOT NULL,
//	    expires_at TEXT NOT NULL,     -- RFC3339 UTC
//	    revoked_at TEXT,              -- NULL when not revoked; RFC3339 UTC when revoked
//	    created_at TEXT NOT NULL      -- RFC3339 UTC
//	);
type TokenStore struct {
	db     DB
	secret string
}

// NewTokenStore creates a [TokenStore] backed by db using secret as the HMAC
// signing key. The secret must match [Config.Secret] so that tokens created
// here are verifiable by [VerifyBearerToken].
func NewTokenStore(db DB, secret string) *TokenStore {
	return &TokenStore{db: db, secret: secret}
}

// probeTable verifies the forge_tokens table is accessible. Called at startup
// by [App.Handler] when a TokenStore is configured.
func (ts *TokenStore) probeTable(ctx context.Context) error {
	row := ts.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM forge_tokens`)
	var n int
	return row.Scan(&n)
}

// ensureBootstrap creates a bootstrap admin token when forge_tokens is empty.
// Called at startup by [App.Handler] after a successful [TokenStore.probeTable].
// When the table already contains at least one row (any token, revoked or not)
// this is a no-op. The raw token is emitted via [log/slog] at Warn level and
// is never persisted — copy it immediately.
func (ts *TokenStore) ensureBootstrap(ctx context.Context) {
	row := ts.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM forge_tokens`)
	var n int
	if err := row.Scan(&n); err != nil || n > 0 {
		return
	}
	raw, err := ts.Create(ctx, "bootstrap-admin", "admin", 10*365*24*time.Hour)
	if err != nil {
		slog.Warn("forge: failed to create bootstrap admin token", "err", err)
		return
	}
	slog.Warn("forge: forge_tokens is empty — bootstrap admin token created (copy now, shown once):\n\t" + raw)
}

// Create generates a signed named bearer token with the given role and ttl,
// stores its SHA-256 fingerprint in forge_tokens, and returns the raw token
// string. The raw token is never persisted; it cannot be retrieved after this
// call — pass it to the client through a secure channel.
//
// role must be a valid [Role] string ("author", "editor", "admin").
// ttl must be positive.
func (ts *TokenStore) Create(ctx context.Context, name, role string, ttl time.Duration) (string, error) {
	user := User{ID: NewID(), Name: name, Roles: []Role{Role(role)}}
	raw, err := SignToken(user, ts.secret, ttl)
	if err != nil {
		return "", ErrInternal
	}
	h := sha256.Sum256([]byte(raw))
	id := hex.EncodeToString(h[:])
	now := time.Now().UTC()
	expiresAt := now.Add(ttl)
	_, err = ts.db.ExecContext(ctx,
		`INSERT INTO forge_tokens (id, name, role, expires_at, created_at) VALUES ($1, $2, $3, $4, $5)`,
		id, name, role, expiresAt.Format(time.RFC3339), now.Format(time.RFC3339),
	)
	if err != nil {
		return "", ErrInternal
	}
	return raw, nil
}

// List returns all token records from forge_tokens ordered by created_at
// descending (newest first). Revoked and expired tokens are included; inspect
// [TokenRecord.RevokedAt] to filter client-side.
func (ts *TokenStore) List(ctx context.Context) ([]TokenRecord, error) {
	rows, err := ts.db.QueryContext(ctx,
		`SELECT id, name, role, expires_at, revoked_at, created_at FROM forge_tokens ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, ErrInternal
	}
	defer rows.Close()
	var out []TokenRecord
	for rows.Next() {
		var rec TokenRecord
		var expiresAtStr, createdAtStr string
		var revokedAtStr *string
		if err := rows.Scan(&rec.ID, &rec.Name, &rec.Role, &expiresAtStr, &revokedAtStr, &createdAtStr); err != nil {
			return nil, ErrInternal
		}
		rec.ExpiresAt, _ = time.Parse(time.RFC3339, expiresAtStr)
		rec.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
		if revokedAtStr != nil {
			rec.RevokedAt, _ = time.Parse(time.RFC3339, *revokedAtStr)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrInternal
	}
	return out, nil
}

// Revoke marks the token with the given fingerprint ID as revoked in
// forge_tokens. Returns [ErrLastAdmin] if the token being revoked is the last
// active (non-revoked, non-expired) admin token — create a replacement admin
// token before revoking this one. Subsequent [VerifyBearerToken] calls with a
// non-nil [TokenStore] reject revoked tokens immediately. Use [TokenStore.List]
// to obtain token IDs.
func (ts *TokenStore) Revoke(ctx context.Context, id string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	// Look up the role of the token being revoked to determine whether the
	// last-admin guard applies.
	var role string
	if err := ts.db.QueryRowContext(ctx,
		`SELECT role FROM forge_tokens WHERE id = $1`, id,
	).Scan(&role); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ErrInternal
	}

	// Guard: refuse to revoke the last active admin token.
	if role == "admin" {
		var otherAdmins int
		if err := ts.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM forge_tokens WHERE role = 'admin' AND revoked_at IS NULL AND expires_at > $1 AND id != $2`,
			now, id,
		).Scan(&otherAdmins); err != nil {
			return ErrInternal
		}
		if otherAdmins == 0 {
			return ErrLastAdmin
		}
	}

	_, err := ts.db.ExecContext(ctx,
		`UPDATE forge_tokens SET revoked_at = $1 WHERE id = $2`,
		now, id,
	)
	if err != nil {
		return ErrInternal
	}
	return nil
}

// — CookieSession ——————————————————————————————————————————————————————————

// cookieAuthFn implements [AuthFunc] and [csrfAware] for cookie-based sessions.
type cookieAuthFn struct {
	name   string
	secret string
	csrf   bool
}

// CookieSession returns an [AuthFunc] that reads a named cookie containing a
// signed user token (same format as [BearerHMAC]). CSRF protection is enabled
// by default — pass [WithoutCSRF] to opt out (strongly discouraged).
//
// The CSRF cookie is named [CSRFCookieName]. See [Amendment S6].
func CookieSession(name, secret string, opts ...Option) AuthFunc {
	csrf := true
	for _, o := range opts {
		if _, ok := o.(withoutCSRFOption); ok {
			csrf = false
		}
	}
	return &cookieAuthFn{name: name, secret: secret, csrf: csrf}
}

func (c *cookieAuthFn) authenticate(r *http.Request) (User, bool) {
	cookie, err := r.Cookie(c.name)
	if err != nil {
		return GuestUser, false
	}
	user, err := decodeToken(cookie.Value, c.secret)
	if err != nil {
		return GuestUser, false
	}
	return user, true
}

func (c *cookieAuthFn) csrfEnabled() bool {
	return c.csrf
}

// — BasicAuth ——————————————————————————————————————————————————————————————

const basicAuthWarn = `WARN  forge: BasicAuth is enabled in a non-development environment.
      BasicAuth sends credentials on every request and has no session management.
      Consider smeldr.BearerHMAC or smeldr.CookieSession for production use.`

// basicAuthFn implements [AuthFunc] and [productionWarner] for HTTP Basic Auth.
type basicAuthFn struct {
	username string
	password string
}

// BasicAuth returns an [AuthFunc] that validates HTTP Basic Auth credentials.
// On success it returns a synthetic User with ID and Name set to the username
// and Roles set to [Guest].
//
// BasicAuth should not be used in production. Consider [BearerHMAC] or
// [CookieSession] for production use. See Amendment S7.
func BasicAuth(username, password string) AuthFunc {
	return &basicAuthFn{username: username, password: password}
}

func (b *basicAuthFn) authenticate(r *http.Request) (User, bool) {
	u, p, ok := r.BasicAuth()
	if !ok {
		return GuestUser, false
	}
	uMatch := subtle.ConstantTimeCompare([]byte(u), []byte(b.username))
	pMatch := subtle.ConstantTimeCompare([]byte(p), []byte(b.password))
	if uMatch&pMatch != 1 {
		return GuestUser, false
	}
	return User{ID: b.username, Name: b.username, Roles: []Role{Guest}}, true
}

func (b *basicAuthFn) warnIfProduction(w io.Writer) {
	fmt.Fprintln(w, basicAuthWarn)
}

// — AnyAuth ————————————————————————————————————————————————————————————————

// anyAuthFn implements [AuthFunc], [productionWarner], and [csrfAware].
// It wraps a list of AuthFunc values and returns the first successful result.
type anyAuthFn struct {
	fns []AuthFunc
}

// AnyAuth returns an [AuthFunc] that tries each provided AuthFunc in order and
// returns the first successful result. If none match, it returns [GuestUser].
//
// AnyAuth forwards [productionWarner] and [csrfAware] capability calls to any
// child that implements them.
func AnyAuth(fns ...AuthFunc) AuthFunc {
	return &anyAuthFn{fns: fns}
}

func (a *anyAuthFn) authenticate(r *http.Request) (User, bool) {
	for _, fn := range a.fns {
		if user, ok := fn.authenticate(r); ok {
			return user, true
		}
	}
	return GuestUser, false
}

func (a *anyAuthFn) warnIfProduction(w io.Writer) {
	for _, fn := range a.fns {
		if pw, ok := fn.(productionWarner); ok {
			pw.warnIfProduction(w)
		}
	}
}

func (a *anyAuthFn) csrfEnabled() bool {
	for _, fn := range a.fns {
		if ca, ok := fn.(csrfAware); ok {
			if ca.csrfEnabled() {
				return true
			}
		}
	}
	return false
}
