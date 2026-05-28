package smeldr

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// — TestCookieCategory_constants ——————————————————————————————————————————

func TestCookieCategory_constants(t *testing.T) {
	cases := []struct {
		cat  CookieCategory
		want string
	}{
		{Necessary, "necessary"},
		{Preferences, "preferences"},
		{Analytics, "analytics"},
		{Marketing, "marketing"},
	}
	for _, tc := range cases {
		t.Run(string(tc.cat), func(t *testing.T) {
			if string(tc.cat) != tc.want {
				t.Errorf("CookieCategory = %q; want %q", tc.cat, tc.want)
			}
		})
	}
}

// — TestSetCookie —————————————————————————————————————————————————————————

func TestSetCookie_necessary(t *testing.T) {
	c := Cookie{Name: "session", Category: Necessary, HttpOnly: true, Secure: true}
	w := httptest.NewRecorder()
	SetCookie(w, c, "abc123")

	setCookie := w.Header().Get("Set-Cookie")
	if setCookie == "" {
		t.Fatal("expected Set-Cookie header, got none")
	}
	if !strings.Contains(setCookie, "session=abc123") {
		t.Errorf("Set-Cookie missing name=value: %s", setCookie)
	}
	if !strings.Contains(setCookie, "HttpOnly") {
		t.Errorf("Set-Cookie missing HttpOnly: %s", setCookie)
	}
}

func TestSetCookie_defaultPath(t *testing.T) {
	c := Cookie{Name: "sess", Category: Necessary} // no Path set
	w := httptest.NewRecorder()
	SetCookie(w, c, "v")
	setCookie := w.Header().Get("Set-Cookie")
	if !strings.Contains(setCookie, "Path=/") {
		t.Errorf("Set-Cookie missing default Path=/: %s", setCookie)
	}
}

func TestSetCookie_panicsOnNonNecessary(t *testing.T) {
	cases := []CookieCategory{Preferences, Analytics, Marketing}
	for _, cat := range cases {
		t.Run(string(cat), func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("SetCookie with category %q should panic", cat)
				}
			}()
			w := httptest.NewRecorder()
			SetCookie(w, Cookie{Name: "x", Category: cat}, "v")
		})
	}
}

// — TestSetCookieIfConsented ——————————————————————————————————————————————

func TestSetCookieIfConsented_panicsOnNecessary(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("SetCookieIfConsented with Necessary should panic")
		}
	}()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	SetCookieIfConsented(w, r, Cookie{Name: "x", Category: Necessary}, "v")
}

func TestSetCookieIfConsented_noConsent(t *testing.T) {
	cats := []CookieCategory{Preferences, Analytics, Marketing}
	for _, cat := range cats {
		t.Run(string(cat), func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			// No consent cookie on request.
			got := SetCookieIfConsented(w, r, Cookie{Name: "x", Category: cat}, "v")
			if got {
				t.Errorf("SetCookieIfConsented returned true without consent for %q", cat)
			}
			if w.Header().Get("Set-Cookie") != "" {
				t.Errorf("expected no Set-Cookie header without consent, got: %s", w.Header().Get("Set-Cookie"))
			}
		})
	}
}

func TestSetCookieIfConsented_withConsent(t *testing.T) {
	cats := []CookieCategory{Preferences, Analytics, Marketing}
	for _, cat := range cats {
		t.Run(string(cat), func(t *testing.T) {
			// Build a request with the consent cookie pre-set.
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.AddCookie(&http.Cookie{Name: consentCookieName, Value: string(cat)})

			w := httptest.NewRecorder()
			got := SetCookieIfConsented(w, r, Cookie{Name: "pref", Category: cat}, "yes")
			if !got {
				t.Errorf("SetCookieIfConsented returned false with consent for %q", cat)
			}
			if w.Header().Get("Set-Cookie") == "" {
				t.Errorf("expected Set-Cookie header with consent for %q", cat)
			}
		})
	}
}

// — TestReadCookie ————————————————————————————————————————————————————————

func TestReadCookie_hit(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "lang", Value: "en"})

	val, ok := ReadCookie(r, "lang")
	if !ok {
		t.Fatal("ReadCookie returned ok=false for present cookie")
	}
	if val != "en" {
		t.Errorf("ReadCookie value = %q; want %q", val, "en")
	}
}

func TestReadCookie_miss(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	val, ok := ReadCookie(r, "missing")
	if ok {
		t.Error("ReadCookie returned ok=true for absent cookie")
	}
	if val != "" {
		t.Errorf("ReadCookie value = %q; want empty string", val)
	}
}

// — TestClearCookie ———————————————————————————————————————————————————————

func TestClearCookie(t *testing.T) {
	c := Cookie{Name: "session", Category: Necessary, Secure: true, HttpOnly: true}
	w := httptest.NewRecorder()
	ClearCookie(w, c)

	setCookie := w.Header().Get("Set-Cookie")
	if setCookie == "" {
		t.Fatal("expected Set-Cookie header from ClearCookie, got none")
	}
	if !strings.Contains(setCookie, "session=") {
		t.Errorf("Set-Cookie missing cookie name: %s", setCookie)
	}
	// MaxAge=-1 serialises as "Max-Age=0" in http.Cookie (Go clamps to 0 when < 0)
	// and also adds an Expires in the past. Either confirms clearing.
	hasClear := strings.Contains(setCookie, "Max-Age=0") || strings.Contains(setCookie, "expires=")
	if !hasClear {
		t.Errorf("Set-Cookie does not appear to clear cookie: %s", setCookie)
	}
}

// — TestConsentFor ————————————————————————————————————————————————————————

func TestConsentFor_necessary_alwaysTrue(t *testing.T) {
	// No consent cookie on request — Necessary must still return true.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if !ConsentFor(r, Necessary) {
		t.Error("ConsentFor(Necessary) should always return true")
	}
}

func TestConsentFor_absent(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	cats := []CookieCategory{Preferences, Analytics, Marketing}
	for _, cat := range cats {
		t.Run(string(cat), func(t *testing.T) {
			if ConsentFor(r, cat) {
				t.Errorf("ConsentFor(%q) should return false without consent cookie", cat)
			}
		})
	}
}

func TestConsentFor_granted(t *testing.T) {
	cats := []CookieCategory{Preferences, Analytics, Marketing}
	for _, cat := range cats {
		t.Run(string(cat), func(t *testing.T) {
			// Simulate GrantConsent by adding the consent cookie directly.
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.AddCookie(&http.Cookie{
				Name:  consentCookieName,
				Value: string(cat),
			})
			if !ConsentFor(r, cat) {
				t.Errorf("ConsentFor(%q) should return true after grant", cat)
			}
		})
	}
}

func TestConsentFor_multipleCategories(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{
		Name:  consentCookieName,
		Value: "preferences,analytics",
	})

	if !ConsentFor(r, Preferences) {
		t.Error("ConsentFor(Preferences) should be true")
	}
	if !ConsentFor(r, Analytics) {
		t.Error("ConsentFor(Analytics) should be true")
	}
	if ConsentFor(r, Marketing) {
		t.Error("ConsentFor(Marketing) should be false — not in consent list")
	}
}

// — TestGrantConsent ——————————————————————————————————————————————————————

func TestGrantConsent_setsConsentCookie(t *testing.T) {
	w := httptest.NewRecorder()
	GrantConsent(w, Preferences, Analytics)

	setCookie := w.Header().Get("Set-Cookie")
	if setCookie == "" {
		t.Fatal("expected Set-Cookie from GrantConsent, got none")
	}
	if !strings.Contains(setCookie, consentCookieName) {
		t.Errorf("Set-Cookie missing %q: %s", consentCookieName, setCookie)
	}
	// Necessary should not appear in the stored value.
	if strings.Contains(setCookie, string(Necessary)) {
		t.Errorf("consent cookie value should not contain 'necessary': %s", setCookie)
	}
}

func TestGrantConsent_necessaryFilteredOut(t *testing.T) {
	w := httptest.NewRecorder()
	// Pass Necessary alongside others — it should be silently dropped from
	// the stored value (it is always implicitly true).
	GrantConsent(w, Necessary, Analytics)

	setCookie := w.Header().Get("Set-Cookie")
	if strings.Contains(setCookie, string(Necessary)) {
		t.Errorf("consent cookie value must not contain 'necessary': %s", setCookie)
	}
}

// — TestRevokeConsent —————————————————————————————————————————————————————

func TestRevokeConsent(t *testing.T) {
	w := httptest.NewRecorder()
	RevokeConsent(w)

	setCookie := w.Header().Get("Set-Cookie")
	if setCookie == "" {
		t.Fatal("expected Set-Cookie from RevokeConsent, got none")
	}
	if !strings.Contains(setCookie, consentCookieName) {
		t.Errorf("Set-Cookie missing %q: %s", consentCookieName, setCookie)
	}
	hasClear := strings.Contains(setCookie, "Max-Age=0") || strings.Contains(setCookie, "expires=")
	if !hasClear {
		t.Errorf("RevokeConsent Set-Cookie should clear cookie: %s", setCookie)
	}
}

// TestRevokeConsent_consentForReturnsFalse verifies the full round-trip:
// after revoke, ConsentFor returns false for all non-Necessary categories.
func TestRevokeConsent_consentForReturnsFalse(t *testing.T) {
	// Build a request that already has consent granted.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{
		Name:  consentCookieName,
		Value: "preferences,analytics,marketing",
	})

	// Simulate revoke by stripping the consent cookie from the request.
	// (In real usage the cleared cookie is sent back in the next request.)
	rRevoked := httptest.NewRequest(http.MethodGet, "/", nil)
	// No consent cookie added.

	cats := []CookieCategory{Preferences, Analytics, Marketing}
	for _, cat := range cats {
		t.Run(string(cat), func(t *testing.T) {
			if ConsentFor(r, cat) == false {
				t.Errorf("pre-condition: ConsentFor(%q) should be true on granted request", cat)
			}
			if ConsentFor(rRevoked, cat) {
				t.Errorf("ConsentFor(%q) should be false after revoke", cat)
			}
		})
	}
}
