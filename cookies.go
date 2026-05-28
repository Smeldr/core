package smeldr

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// — CookieCategory —————————————————————————————————————————————————————————

// CookieCategory classifies a cookie by its GDPR consent requirement.
// The category determines which set API is legal (Decision 5).
type CookieCategory string

const (
	// Necessary cookies are required for the site to function.
	// They do not require user consent and must be set with [SetCookie].
	Necessary CookieCategory = "necessary"

	// Preferences cookies remember user settings (e.g. language, theme).
	// They require consent and must be set with [SetCookieIfConsented].
	Preferences CookieCategory = "preferences"

	// Analytics cookies collect anonymous usage statistics.
	// They require consent and must be set with [SetCookieIfConsented].
	Analytics CookieCategory = "analytics"

	// Marketing cookies track users across sites for advertising purposes.
	// They require consent and must be set with [SetCookieIfConsented].
	Marketing CookieCategory = "marketing"
)

// — Cookie struct ——————————————————————————————————————————————————————————

// Cookie declares a typed cookie with its category, attributes, and purpose.
//
// Category determines which set API is legal (Decision 5):
//   - [Necessary]: use [SetCookie] — no consent required
//   - [Preferences], [Analytics], [Marketing]: use [SetCookieIfConsented]
//
// Purpose is a human-readable description included in the compliance manifest
// at /.well-known/cookies.json.
type Cookie struct {
	// Name is the cookie name as set on the wire.
	Name string

	// Category classifies the cookie for consent enforcement.
	Category CookieCategory

	// Path scopes the cookie to a URL prefix. Defaults to "/" if empty.
	Path string

	// Domain optionally scopes the cookie to a domain.
	Domain string

	// Secure restricts the cookie to HTTPS connections.
	Secure bool

	// HttpOnly prevents JavaScript from accessing the cookie.
	HttpOnly bool

	// SameSite controls cross-site request behaviour.
	// Defaults to http.SameSiteStrictMode when zero.
	SameSite http.SameSite

	// MaxAge is the cookie lifetime in seconds.
	// 0 = session cookie; negative = delete immediately.
	MaxAge int

	// Purpose is a human-readable description for the compliance manifest.
	Purpose string
}

// httpCookie converts a [Cookie] declaration into an [http.Cookie] with the
// given value, applying sensible defaults for missing fields.
func (c Cookie) httpCookie(value string) *http.Cookie {
	path := c.Path
	if path == "" {
		path = "/"
	}
	sameSite := c.SameSite
	if sameSite == 0 {
		sameSite = http.SameSiteStrictMode
	}
	return &http.Cookie{
		Name:     c.Name,
		Value:    value,
		Path:     path,
		Domain:   c.Domain,
		Secure:   c.Secure,
		HttpOnly: c.HttpOnly,
		SameSite: sameSite,
		MaxAge:   c.MaxAge,
	}
}

// — SetCookie ——————————————————————————————————————————————————————————————

// SetCookie writes a [Necessary] cookie to w.
//
// SetCookie panics if c.Category is not [Necessary]. This enforces
// Decision 5 at the point of misuse — before any response is sent in
// production. For non-Necessary categories use [SetCookieIfConsented].
func SetCookie(w http.ResponseWriter, c Cookie, value string) {
	if c.Category != Necessary {
		panic(fmt.Sprintf(
			"smeldr.SetCookie: cookie %q has category %q — only Necessary cookies "+
				"may use SetCookie; use SetCookieIfConsented for %q",
			c.Name, c.Category, c.Category,
		))
	}
	http.SetCookie(w, c.httpCookie(value))
}

// — SetCookieIfConsented ——————————————————————————————————————————————————

// SetCookieIfConsented writes a non-Necessary cookie to w only when the
// request carries consent for c.Category. Returns true when the cookie was
// set, false when skipped due to missing consent.
//
// SetCookieIfConsented panics if c.Category is [Necessary]. Necessary
// cookies do not require consent and must use [SetCookie] instead.
func SetCookieIfConsented(w http.ResponseWriter, r *http.Request, c Cookie, value string) bool {
	if c.Category == Necessary {
		panic(fmt.Sprintf(
			"smeldr.SetCookieIfConsented: cookie %q has category Necessary — "+
				"use SetCookie for Necessary cookies",
			c.Name,
		))
	}
	if !ConsentFor(r, c.Category) {
		return false
	}
	http.SetCookie(w, c.httpCookie(value))
	return true
}

// — ReadCookie / ClearCookie ——————————————————————————————————————————————

// ReadCookie returns the value of the named cookie from r, and whether it
// was present. Returns ("", false) when the cookie is absent.
func ReadCookie(r *http.Request, name string) (string, bool) {
	c, err := r.Cookie(name)
	if err != nil {
		return "", false
	}
	return c.Value, true
}

// ClearCookie expires c immediately by setting MaxAge to -1 and an Expires
// time in the past.
func ClearCookie(w http.ResponseWriter, c Cookie) {
	path := c.Path
	if path == "" {
		path = "/"
	}
	sameSite := c.SameSite
	if sameSite == 0 {
		sameSite = http.SameSiteStrictMode
	}
	http.SetCookie(w, &http.Cookie{
		Name:     c.Name,
		Value:    "",
		Path:     path,
		Domain:   c.Domain,
		Secure:   c.Secure,
		HttpOnly: c.HttpOnly,
		SameSite: sameSite,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
}

// — Consent storage ————————————————————————————————————————————————————————

// consentCookieName is the Necessary cookie that stores the user's consent choices.
const consentCookieName = "smeldr_consent"

// consentCookie is the declaration used to write and clear the consent state.
var consentCookie = Cookie{
	Name:     consentCookieName,
	Category: Necessary,
	HttpOnly: true,
	Secure:   true,
	SameSite: http.SameSiteStrictMode,
	MaxAge:   365 * 24 * 60 * 60, // 1 year
	Purpose:  "Stores the user's cookie consent choices.",
}

// ConsentFor reports whether the request carries consent for the given
// category. [Necessary] always returns true regardless of the consent cookie.
func ConsentFor(r *http.Request, cat CookieCategory) bool {
	if cat == Necessary {
		return true
	}
	value, ok := ReadCookie(r, consentCookieName)
	if !ok || value == "" {
		return false
	}
	for _, part := range strings.Split(value, ",") {
		if CookieCategory(strings.TrimSpace(part)) == cat {
			return true
		}
	}
	return false
}

// GrantConsent writes the smeldr_consent cookie to w with the given categories.
// [Necessary] is always implicitly consented and is not stored in the cookie
// value. Subsequent calls overwrite the previous consent state.
func GrantConsent(w http.ResponseWriter, cats ...CookieCategory) {
	parts := make([]string, 0, len(cats))
	for _, cat := range cats {
		if cat == Necessary {
			continue // Necessary is always true; never stored
		}
		parts = append(parts, string(cat))
	}
	http.SetCookie(w, consentCookie.httpCookie(strings.Join(parts, ",")))
}

// RevokeConsent clears the smeldr_consent cookie, withdrawing all non-Necessary
// consent. Subsequent calls to [ConsentFor] for non-Necessary categories
// return false until [GrantConsent] is called again.
func RevokeConsent(w http.ResponseWriter) {
	ClearCookie(w, consentCookie)
}
