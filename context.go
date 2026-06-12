package smeldr

import (
	"context"
	"net/http"
	"net/http/httptest"
)

// User represents an authenticated identity. The zero value is an
// unauthenticated guest — equivalent to [GuestUser]. See Amendment R3.
//
// The User type is declared here (context.go) rather than auth.go because
// [Context.User] returns it and context.go is in a lower dependency layer
// than auth.go. auth.go adds authentication machinery on top of this type.
type User struct {
	// ID is the user's stable UUID. Empty for unauthenticated guests.
	ID string

	// Name is the display name. Empty for unauthenticated guests.
	Name string

	// Roles is the set of roles held by this user. Forge's hierarchical
	// permission checks ([HasRole], [IsRole]) operate on this slice.
	Roles []Role
}

// GuestUser is the zero-value User representing an unauthenticated request.
// Forge sets ctx.User() to GuestUser when no authentication middleware has
// identified the caller.
var GuestUser = User{}

// Context is the request-scoped value passed to every Forge hook and handler.
// It embeds [context.Context] for full compatibility with stdlib and third-party
// libraries, while exposing Forge-specific accessors without key-based lookups.
//
// smeldr.Context is always non-nil — Forge guarantees this before any user code
// is called. The internal implementation is [contextImpl] (unexported).
// Use [ContextFrom] in production and [NewTestContext] in tests.
type Context interface {
	context.Context

	// User returns the authenticated identity for this request.
	// Returns [GuestUser] (zero value) for unauthenticated requests.
	User() User

	// Locale returns the BCP 47 language tag for this request.
	// Always "en" in v1; i18n support is planned for v2 (Decision 11).
	Locale() string

	// SiteName returns the hostname portion of Config.BaseURL
	// (e.g. "example.com"). Set automatically at startup.
	SiteName() string

	// RequestID returns the UUID v7 assigned to this request for
	// end-to-end traceability. Set as X-Request-ID on the response.
	RequestID() string

	// Request returns the underlying *http.Request.
	Request() *http.Request

	// Response returns the http.ResponseWriter for this request.
	Response() http.ResponseWriter
}

// contextKey is the unexported type used to store forge values in a
// context.Context without colliding with other packages' keys.
type contextKey int

const (
	userContextKey contextKey = iota
)

// contextImpl is the unexported concrete implementation of [Context].
// All method implementations are simple field accessors — zero allocations
// on the hot path.
type contextImpl struct {
	context.Context
	user      User
	locale    string
	siteName  string
	requestID string
	req       *http.Request
	w         http.ResponseWriter
}

func (c *contextImpl) User() User                    { return c.user }
func (c *contextImpl) Locale() string                { return c.locale }
func (c *contextImpl) SiteName() string              { return c.siteName }
func (c *contextImpl) RequestID() string             { return c.requestID }
func (c *contextImpl) Request() *http.Request        { return c.req }
func (c *contextImpl) Response() http.ResponseWriter { return c.w }

// ContextFrom builds a [Context] from a live HTTP request. It:
//   - Derives the RequestID from X-Request-ID response header, then request
//     header, generating a fresh UUID v7 if neither is present
//   - Writes the final RequestID to the X-Request-ID response header
//   - Reads the authenticated [User] from the request's context (set by auth
//     middleware); uses [GuestUser] if absent
//   - Sets Locale to "en" (i18n deferred to v2)
//   - Sets SiteName to "" (wired in smeldr.go, Step 11)
func ContextFrom(w http.ResponseWriter, r *http.Request) Context {
	// Determine request ID: response header > request header > generated.
	rid := w.Header().Get("X-Request-ID")
	if rid == "" {
		rid = r.Header.Get("X-Request-ID")
	}
	if rid == "" {
		rid = NewID()
	}
	w.Header().Set("X-Request-ID", rid)

	// Extract user set by auth middleware (if any).
	user := GuestUser
	if u, ok := r.Context().Value(userContextKey).(User); ok {
		user = u
	}

	return &contextImpl{
		Context:   r.Context(),
		user:      user,
		locale:    "en",
		siteName:  "",
		requestID: rid,
		req:       r,
		w:         w,
	}
}

// NewTestContext returns a [Context] suitable for unit tests. It requires no
// running HTTP server:
//   - Request() returns a synthetic GET / request
//   - Response() returns a *httptest.ResponseRecorder
//   - Locale is "en", SiteName is "", RequestID is a generated UUID v7
//
// Pass [GuestUser] (or a zero User) for unauthenticated test scenarios.
func NewTestContext(user User) Context {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	return &contextImpl{
		Context:   req.Context(),
		user:      user,
		locale:    "en",
		siteName:  "",
		requestID: NewID(),
		req:       req,
		w:         rec,
	}
}

// NewContextWithUser returns a [Context] for use in background goroutines or
// non-HTTP transports (e.g. stdio MCP) that require a real User identity.
// Unlike [NewTestContext], this function may appear in production code.
// Unlike [NewBackgroundContext], the User is caller-supplied rather than
// hardcoded to [GuestUser].
//
//   - Request() returns a synthetic GET / request backed by [context.Background]
//   - Response() returns a *httptest.ResponseRecorder (discards output)
//   - Locale is "en"; SiteName is ""; RequestID is a generated UUID v7
func NewContextWithUser(user User) Context {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(context.Background())
	return &contextImpl{
		Context:   context.Background(),
		user:      user,
		locale:    "en",
		siteName:  "",
		requestID: NewID(),
		req:       req,
		w:         rec,
	}
}

// NewBackgroundContext returns a [Context] for use in background goroutines
// such as the scheduled-publishing ticker. It has no HTTP lifecycle and never
// times out:
//   - Request() returns a synthetic GET / request backed by [context.Background]
//   - Response() returns a *httptest.ResponseRecorder (discards output)
//   - User is [GuestUser]; Locale is "en"; RequestID is a generated UUID v7
//
// siteName should be the hostname portion of [Config.BaseURL] (e.g. "example.com").
func NewBackgroundContext(siteName string) Context {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(context.Background())
	return &contextImpl{
		Context:   context.Background(),
		user:      GuestUser,
		locale:    "en",
		siteName:  siteName,
		requestID: NewID(),
		req:       req,
		w:         rec,
	}
}
