// AGPL-3.0-or-later

package smeldr

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

// wantTypedE checks err's refusal type, its exact values and its message
// (T1061 phase 3, group E).
func wantTypedE(t *testing.T, err error, typ ProblemType, values map[string]string, msg string) {
	t.Helper()
	if err == nil {
		t.Fatalf("err = nil, want %s", typ)
	}
	if got := ProblemTypeOf(err); got != typ {
		t.Errorf("ProblemTypeOf = %q, want %q (err %v)", got, typ, err)
	}
	if got := ProblemValuesOf(err); !reflect.DeepEqual(got, values) {
		t.Errorf("ProblemValuesOf = %#v, want %#v", got, values)
	}
	if err.Error() != msg {
		t.Errorf("Error() = %q, want %q", err.Error(), msg)
	}
}

// wantFieldE checks err is a ValidationError on field alone.
func wantFieldE(t *testing.T, err error, field string) {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) || len(ve.Fields()) != 1 || ve.Fields()[0].Field != field {
		t.Errorf("err = %v, want a ValidationError on %s", err, field)
	}
}

// wantEnvelopeE checks a handler's JSON error envelope: the old status, code
// and message, and the refusal type and values it now carries.
func wantEnvelopeE(t *testing.T, w *httptest.ResponseRecorder, status int, sentinel Error, typ ProblemType, values map[string]string) {
	t.Helper()
	if w.Code != status {
		t.Errorf("status = %d, want %d (body %s)", w.Code, status, w.Body.String())
	}
	var body struct {
		Error struct {
			Code    string            `json:"code"`
			Message string            `json:"message"`
			ID      ProblemType       `json:"id"`
			Class   string            `json:"class"`
			Values  map[string]string `json:"values"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	e := body.Error
	if e.Code != sentinel.Code() || e.Message != sentinel.Public() {
		t.Errorf("code, message = %q, %q, want %q, %q", e.Code, e.Message, sentinel.Code(), sentinel.Public())
	}
	if e.ID != typ {
		t.Errorf("id = %q, want %q", e.ID, typ)
	}
	if e.Class != problemClass(typ) || e.Class == "" {
		t.Errorf("class = %q, want %q", e.Class, problemClass(typ))
	}
	if !reflect.DeepEqual(e.Values, values) {
		t.Errorf("values = %#v, want %#v", e.Values, values)
	}
}

func TestPhaseThreeE_ModuleHandlers(t *testing.T) {
	t.Run("write_not_permitted", func(t *testing.T) {
		m := newTestModule(NewMemoryRepo[*testPost]())
		w := httptest.NewRecorder()
		m.createHandler(w, httptest.NewRequest(http.MethodPost, "/testposts", strings.NewReader(`{"Title":"X"}`)))
		wantEnvelopeE(t, w, http.StatusForbidden, ErrForbidden, problemWriteNotPermitted, nil)
	})
	t.Run("request_malformed_body", func(t *testing.T) {
		m := newTestModule(NewMemoryRepo[*testPost]())
		w := httptest.NewRecorder()
		m.createHandler(w, withUser(httptest.NewRequest(http.MethodPost, "/testposts", strings.NewReader(`{`)), authorUser()))
		wantEnvelopeE(t, w, http.StatusBadRequest, ErrBadRequest, problemRequestMalformedBody, nil)
	})
	t.Run("request_too_large", func(t *testing.T) {
		m := newTestModule(NewMemoryRepo[*testPost]())
		w := httptest.NewRecorder()
		r := withUser(httptest.NewRequest(http.MethodPost, "/testposts", strings.NewReader(`{"Title":"far too long"}`)), authorUser())
		r.Body = http.MaxBytesReader(w, r.Body, 4)
		m.createHandler(w, r)
		wantEnvelopeE(t, w, http.StatusRequestEntityTooLarge, ErrRequestTooLarge, problemRequestTooLarge, nil)
	})
	t.Run("route_not_found", func(t *testing.T) {
		app, _, _ := redirectApp(t)
		w := getPath(app.Handler(), "/posts/never-was")
		wantEnvelopeE(t, w, http.StatusNotFound, ErrNotFound, problemRouteNotFound, nil)
	})
	t.Run("redirect_gone", func(t *testing.T) {
		app, _, _ := redirectApp(t)
		app.Redirect("/posts/removed", "", Gone)
		w := getPath(app.Handler(), "/posts/removed")
		wantEnvelopeE(t, w, http.StatusGone, ErrGone, problemRedirectGone, nil)
	})
	t.Run("content_not_acceptable", func(t *testing.T) {
		w := httptest.NewRecorder()
		writeContent(w, httptest.NewRequest(http.MethodGet, "/testposts/x", nil), "text/markdown", &testPost{Title: "X"})
		wantEnvelopeE(t, w, http.StatusNotAcceptable, ErrNotAcceptable, problemContentNotAcceptable, nil)
	})
}

func TestPhaseThreeE_Middleware(t *testing.T) {
	t.Run("csrf_invalid", func(t *testing.T) {
		h := CSRF(CookieSession("session", testSecret))(okHandler("ok"))
		r := httptest.NewRequest(http.MethodPost, "/submit", nil)
		r.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "abc123"})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		wantEnvelopeE(t, w, http.StatusForbidden, ErrForbidden, problemCsrfInvalid, nil)
	})
	t.Run("rate_limited", func(t *testing.T) {
		h := RateLimit(1, time.Minute)(okHandler("ok"))
		var w *httptest.ResponseRecorder
		for range 2 {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = "10.0.0.7:9999"
			w = httptest.NewRecorder()
			h.ServeHTTP(w, r)
		}
		wantEnvelopeE(t, w, http.StatusTooManyRequests, ErrTooManyRequests, problemRateLimited, nil)
		if w.Header().Get("Retry-After") == "" {
			t.Error("Retry-After missing")
		}
	})
	t.Run("auth_unauthenticated", func(t *testing.T) {
		h := newCookieManifestHandler("test.com", nil, ManifestAuth(BearerHMAC("test-secret-long-enough")))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/.well-known/cookies.json", nil))
		wantEnvelopeE(t, w, http.StatusUnauthorized, ErrUnauth, problemAuthUnauthenticated, nil)
	})
}

func TestPhaseThreeE_Slugs(t *testing.T) {
	t.Run("slug_taken", func(t *testing.T) {
		repo := NewMemoryRepo[*testPost]()
		m := newWebModule(repo)
		seedPost(t, repo, "About", Published)
		draft := seedPost(t, repo, "About", Draft)
		err := m.MCPPublish(hookCtx(), draft.ID, "")
		wantTypedE(t, err, problemSlugTaken,
			map[string]string{"prefix": "/testposts", "slug": "about", "suggested_slug": "about-2"},
			fmt.Errorf("%w: the URL /testposts/about is already published; give this item another slug, for example /testposts/about-2", ErrConflict).Error())
		if !errors.Is(err, ErrConflict) {
			t.Errorf("err = %v, want ErrConflict", err)
		}
	})
	t.Run("slug_ambiguous", func(t *testing.T) {
		repo := NewMemoryRepo[*testPost]()
		m := newWebModule(repo)
		seedPost(t, repo, "About", Draft)
		seedPost(t, repo, "About", Draft)
		_, err := m.MCPGet(hookCtx(), "about")
		wantTypedE(t, err, problemSlugAmbiguous, map[string]string{"slug": "about"},
			fmt.Errorf("%w: the slug %q names more than one item you can read; use its ID", ErrConflict, "about").Error())
		if !errors.Is(err, ErrConflict) {
			t.Errorf("err = %v, want ErrConflict", err)
		}
	})
}

func TestPhaseThreeE_Fields(t *testing.T) {
	t.Run("field_immutable", func(t *testing.T) {
		repo := NewMemoryRepo[*testPost]()
		m := newTestModule(repo)
		seed := seedPost(t, repo, "Item", Draft)
		_, err := m.MCPUpdate(hookCtx(), seed.Slug, map[string]any{"ID": "other"})
		wantTypedE(t, err, problemFieldImmutable, nil, "validation failed: id: cannot be changed by an update")
		wantFieldE(t, err, "id")
	})
	t.Run("field_too_short", func(t *testing.T) {
		type short struct {
			Body string `smeldr:"min=10"`
		}
		err := RunValidation(&short{Body: "hi"})
		wantTypedE(t, err, problemFieldTooShort, map[string]string{"min": "10"}, "validation failed: Body: too short (min 10)")
		wantFieldE(t, err, "Body")
	})
	t.Run("item_locked_state", func(t *testing.T) {
		db := newSQLiteDB(t)
		if err := CreateStateFlowTables(db); err != nil {
			t.Fatal(err)
		}
		app := New(Config{BaseURL: "https://example.com", Secret: []byte(redirectModuleSecret), DB: db})
		repo := NewMemoryRepo[*testPost]()
		m := newTestModule(repo)
		app.Content(m)
		if err := app.RegisterFlow(StateFlow{
			Name: "testpost-locked", TypeName: m.contentTypeName,
			States:      []State{{Name: "draft", IsInitial: true}, {Name: "published", Locked: true}},
			Transitions: []Transition{{From: "draft", To: "published"}},
		}); err != nil {
			t.Fatal(err)
		}
		p := seedPost(t, repo, "Fixed", Published)
		_, err := m.MCPUpdate(hookCtx(), p.ID, map[string]any{"Title": "Changed"})
		wantTypedE(t, err, problemItemLockedState,
			map[string]string{"type": m.contentTypeName, "record": p.ID, "state": "published"},
			fmt.Errorf("%w: %s %q is locked in state %q \u2014 its content may not be modified; create a new item that supersedes it instead",
				ErrConflict, m.contentTypeName, p.ID, Published).Error())
		if !errors.Is(err, ErrConflict) {
			t.Errorf("err = %v, want ErrConflict", err)
		}
	})
}
