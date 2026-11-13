package smeldr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// T1061 phase 3: the refusal types of the dynamic content, schema, title,
// signal, scheduler, role and config sites, each driven through a real site.

// wantTypedF checks err's refusal type and exactly its values.
func wantTypedF(t *testing.T, err error, typ ProblemType, values map[string]string) {
	t.Helper()
	if err == nil {
		t.Fatalf("err = nil; want %s", typ)
	}
	if got := ProblemTypeOf(err); got != typ {
		t.Errorf("ProblemTypeOf = %q; want %q (err %v)", got, typ, err)
	}
	if got := ProblemValuesOf(err); !maps.Equal(got, values) || (got == nil) != (values == nil) {
		t.Errorf("ProblemValuesOf = %#v; want %#v", got, values)
	}
}

// wantTypedFSentinel checks the old behaviour of a wrapped sentinel: errors.Is
// and the exact Error() text.
func wantTypedFSentinel(t *testing.T, err, sentinel error, msg string) {
	t.Helper()
	if !errors.Is(err, sentinel) {
		t.Errorf("errors.Is(%v, %v) = false", err, sentinel)
	}
	if err.Error() != msg {
		t.Errorf("Error() = %q\nwant      %q", err.Error(), msg)
	}
}

// wantTypedFField checks the old behaviour of a ValidationError: one field,
// and the exact Error() text.
func wantTypedFField(t *testing.T, err error, field, msg string) {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v; want a ValidationError", err)
	}
	if f := ve.Fields(); len(f) != 1 || f[0].Field != field {
		t.Errorf("fields = %+v; want one on %q", f, field)
	}
	if err.Error() != msg {
		t.Errorf("Error() = %q\nwant      %q", err.Error(), msg)
	}
}

// wantTypedFHTTP decodes a JSON error envelope and checks status, code, the
// message, the type id and exactly its values.
func wantTypedFHTTP(t *testing.T, w *httptest.ResponseRecorder, status int, code, message string, typ ProblemType, values map[string]string) {
	t.Helper()
	if w.Code != status {
		t.Errorf("status = %d; want %d (body %s)", w.Code, status, w.Body.String())
	}
	var env struct {
		Error struct {
			Code    string            `json:"code"`
			Message string            `json:"message"`
			ID      ProblemType       `json:"id"`
			Class   string            `json:"class"`
			Values  map[string]string `json:"values"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	e := env.Error
	if e.Code != code || e.Message != message {
		t.Errorf("code, message = %q, %q; want %q, %q", e.Code, e.Message, code, message)
	}
	if e.ID != typ {
		t.Errorf("id = %q; want %q", e.ID, typ)
	}
	if e.Class != problemClass(typ) {
		t.Errorf("class = %q; want %q", e.Class, problemClass(typ))
	}
	if !maps.Equal(e.Values, values) || (e.Values == nil) != (values == nil) {
		t.Errorf("values = %#v; want %#v", e.Values, values)
	}
}

// serveTypedF serves body to h with the path values set.
func serveTypedF(h http.Handler, method, body string, pathValues map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/_content", strings.NewReader(body))
	for k, v := range pathValues {
		r.SetPathValue(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// The /_content admin handlers: role floor, define failure, missing status.
func TestPhaseThreeF_AdminHandlers(t *testing.T) {
	app := dynamicTitleApp(t, time.Time{})
	defineTicket(t, app)
	admin := authFuncFor(User{ID: "admin", Roles: []Role{Admin}})
	editor := authFuncFor(User{ID: "editor", Roles: []Role{Editor}})

	t.Run("role insufficient", func(t *testing.T) {
		w := serveTypedF(newDefineTypeHandler(app, editor), http.MethodPost, `{"type_name":"story"}`, nil)
		wantTypedFHTTP(t, w, http.StatusForbidden, "forbidden", "Forbidden", problemRoleInsufficient, nil)
	})
	t.Run("content type define failed", func(t *testing.T) {
		w := serveTypedF(newDefineTypeHandler(app, admin), http.MethodPost, `{"type_name":"ticket","fields":[]}`, nil)
		wantTypedFHTTP(t, w, http.StatusBadRequest, "bad_request", "Bad request", problemContentTypeDefineFailed, nil)
	})
	t.Run("transition status required", func(t *testing.T) {
		w := serveTypedF(newSetStatusHandler(app, editor), http.MethodPost, `{"status":""}`, map[string]string{"type": "ticket", "id": "any"})
		wantTypedFHTTP(t, w, http.StatusBadRequest, "bad_request", "Bad request", problemTransitionStatusRequired, nil)
	})
}

// A governed reader with no grant on the type: the read scope refuses it.
func TestPhaseThreeF_ScopeTypeNotReadable(t *testing.T) {
	f := newScopedFixture(t)
	reader := User{ID: "reader"}
	_, err := f.app.adminReadScope(context.Background(), reader, true, "invoice")
	wantTypedF(t, err, problemScopeTypeNotReadable, nil)
	wantTypedFSentinel(t, err, ErrForbidden, ErrForbidden.Error())

	w := serveTypedF(newAdminListHandler(f.app, authFuncFor(reader)), http.MethodGet, "", map[string]string{"type": "invoice"})
	wantTypedFHTTP(t, w, http.StatusForbidden, "forbidden", "Forbidden", problemScopeTypeNotReadable, nil)
}

// RedefineContentTypeVia: an unknown type, a compiled type, and a field whose
// type would change.
func TestPhaseThreeF_Redefine(t *testing.T) {
	ctx := context.Background()
	t.Run("content type not found", func(t *testing.T) {
		app := dynamicTitleApp(t, time.Time{})
		_, err := app.RedefineContentTypeVia(ctx, "", &ContentTypeSchema{TypeName: "nope"}, "")
		wantTypedF(t, err, problemContentTypeNotFound, map[string]string{"type": "nope"})
		wantTypedFSentinel(t, err, ErrNotFound, `Not found: content type "nope" not registered`)
	})
	t.Run("content type not runtime", func(t *testing.T) {
		f := newScopedFixture(t)
		_, err := f.app.RedefineContentTypeVia(ctx, "", &ContentTypeSchema{TypeName: "Task"}, "")
		wantTypedF(t, err, problemContentTypeNotRuntime, map[string]string{"type": "Task"})
		wantTypedFSentinel(t, err, ErrBadRequest, `Bad request: "Task" is not a runtime-defined type; only those can be redefined`)
	})
	t.Run("redefine incompatible", func(t *testing.T) {
		app := dynamicTitleApp(t, time.Time{})
		defineTicket(t, app)
		_, err := app.RedefineContentTypeVia(ctx, "", &ContentTypeSchema{TypeName: "ticket",
			Fields: json.RawMessage(`[{"name":"Title","type":"string","role":"title"},{"name":"Summary","type":"string","role":"summary","max_length":10},{"name":"Body","type":"integer"}]`)}, "")
		wantTypedF(t, err, problemContentTypeRedefineIncompatible, map[string]string{"from_type": "string", "to_type": "integer"})
		wantTypedFField(t, err, "Body", `validation failed: Body: type cannot change from "string" to "integer": existing items hold the old type`)
	})
}

// A dynamic item's fields checked against its schema: an unknown field and a
// value of the wrong type.
func TestPhaseThreeF_SchemaFields(t *testing.T) {
	_, _, repo, _ := dynSchedApp(t)
	ctx := context.Background()
	t.Run("field unknown", func(t *testing.T) {
		_, err := repo.CreateDraft(ctx, map[string]any{"Title": "x", "Nope": "y"})
		wantTypedF(t, err, problemFieldUnknown, nil)
		wantTypedFField(t, err, "Nope", "validation failed: Nope: unknown field")
	})
	t.Run("field type mismatch", func(t *testing.T) {
		_, err := repo.CreateDraft(ctx, map[string]any{"Title": 5.0})
		wantTypedF(t, err, problemFieldTypeMismatch, map[string]string{"expected": "string"})
		wantTypedFField(t, err, "Title", "validation failed: Title: expected string")
	})
}

// SetSlugVia on a record type and on a published item.
func TestPhaseThreeF_SetSlug(t *testing.T) {
	ctx := context.Background()
	t.Run("slug record type fixed", func(t *testing.T) {
		repo := defineTicket(t, dynamicTitleApp(t, time.Time{}))
		err := repo.SetSlugVia(ctx, "", "any", "new-slug")
		wantTypedF(t, err, problemSlugRecordTypeFixed, map[string]string{"type": "ticket"})
		wantTypedFSentinel(t, err, ErrBadRequest, "Bad request: ticket is a record type; its address is its identity and its slug does not change")
	})
	t.Run("slug published immutable", func(t *testing.T) {
		_, _, repo, _ := dynSchedApp(t)
		n := newDraft(t, repo, "Pasta")
		if err := repo.SetStatus(ctx, n.ID, Published); err != nil {
			t.Fatal(err)
		}
		err := repo.SetSlugVia(ctx, "", n.ID, "pasta-2")
		wantTypedF(t, err, problemSlugPublishedImmutable, nil)
		wantTypedFSentinel(t, err, ErrConflict, "Conflict: a published item keeps its URL; its slug does not change")
	})
}

// A raw move into scheduled with no scheduled_at.
func TestPhaseThreeF_ScheduleNeedsTime(t *testing.T) {
	_, _, repo, _ := dynSchedApp(t)
	n := newDraft(t, repo, "Later")
	err := repo.SetStatus(context.Background(), n.ID, Scheduled)
	wantTypedF(t, err, problemScheduleNeedsTime, map[string]string{"type": "memo", "record": n.ID})
	wantTypedFField(t, err, "ScheduledAt", fmt.Sprintf(`validation failed: ScheduledAt: memo %q cannot move to scheduled without a scheduled_at: it would never be published; use schedule_content (MCP) or ScheduleContent, which sets the time`, n.ID))
}

// warningsCheck after each cutover: a missing title, a history signal_type, a
// message over the limit.
func TestPhaseThreeF_Cutovers(t *testing.T) {
	ctx := context.Background()
	past := time.Now().Add(-time.Hour)
	t.Run("title cutover", func(t *testing.T) {
		repo := defineTicket(t, dynamicTitleApp(t, past))
		_, _, err := repo.CreateDraftWithWarnings(ctx, "", map[string]any{"Body": "x"})
		wantTypedF(t, err, problemTitleCutover, nil)
		wantTypedFField(t, err, "Title", "validation failed: Title: Title is missing: "+TitleGuidance)
	})
	t.Run("signal history type refused", func(t *testing.T) {
		_, err := CheckSignal(ctx, &Signal{SignalType: "plan-ready", Message: "x"}, SignalCutovers{Event: past})
		wantTypedF(t, err, problemSignalHistoryTypeRefused, nil)
		wantTypedFField(t, err, "signal_type", `validation failed: signal_type: signal_type "plan-ready" is a history type: it is accepted until the instance's signal event cutover, then refused`)
	})
	t.Run("signal message too long", func(t *testing.T) {
		msg := strings.Repeat("m", SignalMessageMaxLength+1)
		_, err := CheckSignal(ctx, &Signal{SignalType: "question", Message: msg}, SignalCutovers{Message: past})
		wantTypedF(t, err, problemSignalMessageTooLong, nil)
		wantTypedFField(t, err, "message", fmt.Sprintf("validation failed: message: message is %d characters, the limit is %d: put the detail in the item the Signal names", SignalMessageMaxLength+1, SignalMessageMaxLength))
	})
}

// The closed amendment_type list on a create.
func TestPhaseThreeF_AmendmentTypeNotInList(t *testing.T) {
	err := checkAmendmentType(nil, &Amendment{AmendmentType: "feat"})
	allowed := strings.Join(AmendmentTypes, ", ")
	wantTypedF(t, err, problemAmendmentTypeNotInList, map[string]string{"value": "feat", "allowed": allowed})
	wantTypedFField(t, err, "amendment_type", `validation failed: amendment_type: "feat" is not an amendment type: use one of `+allowed)
}

// A custom role registered again at another level.
func TestPhaseThreeF_RoleLevelMismatch(t *testing.T) {
	const name = "phase3f_role"
	t.Cleanup(func() {
		roleMu.Lock()
		delete(roleLevels, Role(name))
		roleMu.Unlock()
	})
	if _, err := NewRole(name).Above(Guest).Register(); err != nil {
		t.Fatal(err)
	}
	_, err := NewRole(name).Above(Author).Register()
	wantTypedF(t, err, problemRoleLevelMismatch, nil)
	wantTypedFField(t, err, "role", "validation failed: role: already registered with a different level")
}

// ValidateConfig without a BaseURL.
func TestPhaseThreeF_ConfigInvalid(t *testing.T) {
	t.Setenv("SMELDR_CONFIG", "")
	_, err := ValidateConfig(Config{Secret: []byte("supersecretkey16")})
	wantTypedF(t, err, problemConfigInvalid, nil)
	wantTypedFField(t, err, "base_url", `validation failed: base_url: Config.BaseURL is required (e.g. "https://example.com")`)
}
