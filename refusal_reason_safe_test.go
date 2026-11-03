package smeldr

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// place.authority_required shows a reason and values (T1067): the place only
// when the caller may read it, the act text only when it names nothing else
// the caller has not read.
func TestRequireActOver_ReasonSafeToShow(t *testing.T) {
	f := newAreaGateFixture(t)
	// A human with no grants: lacks ratify over every place.
	actor := actorCtx("no-grants", Human)
	place := placeAct{act: "deleting ARE1", code: "delete_area", over: "ARE1", place: readableRecord("Area", f.area), actRead: true, decision: "D146"}
	unread := place
	unread.actRead = false

	tests := []struct {
		name   string
		scope  *ReadScope
		p      placeAct
		reason string
		values map[string]string
	}{
		{"place readable", AllReadScope(), place, "deleting ARE1 needs ratify over ARE1 (D146)",
			map[string]string{"act": "delete_area", "operation": "ratify", "place": "ARE1"}},
		{"place readable, act names an unread record", AllReadScope(), unread, "this needs ratify over ARE1 (D146)",
			map[string]string{"act": "delete_area", "operation": "ratify", "place": "ARE1"}},
		{"place not readable", &ReadScope{}, place, "this needs ratify over its place (D146)",
			map[string]string{"act": "delete_area", "operation": "ratify"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := requireActOver(WithReadScope(actor, tc.scope), f.rs, OperationRatify, AuthTarget{TypeName: "Area", ID: f.area}, tc.p)
			var re *RefusalError
			if !errors.As(err, &re) || !errors.Is(err, ErrForbidden) || ProblemTypeOf(err) != problemPlaceAuthorityRequired {
				t.Fatalf("err = %v; want a place.authority_required refusal", err)
			}
			if re.Reason != tc.reason {
				t.Errorf("reason %q; want %q", re.Reason, tc.reason)
			}
			if got := ProblemValuesOf(err); len(got) != len(tc.values) || got["place"] != tc.values["place"] || got["act"] != tc.values["act"] {
				t.Errorf("values %v; want %v", got, tc.values)
			}

			// Over HTTP the reason is the message and the values are sent.
			w := httptest.NewRecorder()
			WriteError(w, httptest.NewRequest(http.MethodGet, "/", nil), err)
			var body struct {
				Error struct {
					Message string            `json:"message"`
					Values  map[string]string `json:"values"`
				} `json:"error"`
			}
			if json.Unmarshal(w.Body.Bytes(), &body) != nil || w.Code != 403 || body.Error.Message != tc.reason || len(body.Error.Values) != len(tc.values) {
				t.Errorf("HTTP %d %s; want 403 with the reason and %d values", w.Code, w.Body.String(), len(tc.values))
			}
			if tc.values["place"] == "" && strings.Contains(w.Body.String(), "ARE1") {
				t.Errorf("an unreadable place reached the body: %s", w.Body.String())
			}
		})
	}
}

// readableArea finds a coverage rule's Area by address or ID before asking
// the scope; an Area that cannot be found is not readable.
func TestReadableArea(t *testing.T) {
	f := newAreaGateFixture(t)
	ctx := context.Background()
	for _, ref := range []string{"ARE1", f.area} {
		if !readableArea(ctx, f.db, ref)(AllReadScope()) {
			t.Errorf("%s not readable to a reader of everything", ref)
		}
		if readableArea(ctx, f.db, ref)(&ReadScope{}) {
			t.Errorf("%s readable to an empty scope", ref)
		}
	}
	if readableArea(ctx, f.db, "ARE-none")(AllReadScope()) {
		t.Error("a missing Area reads as readable")
	}
}

// wantPlaceHidden checks a place.authority_required refusal that must not
// name hidden (the place, or the flagged Decision) in its values or reason,
// in Go and over HTTP.
func wantPlaceHidden(t *testing.T, err error, hidden string) {
	t.Helper()
	var re *RefusalError
	if !errors.As(err, &re) || ProblemTypeOf(err) != problemPlaceAuthorityRequired {
		t.Fatalf("err = %v; want a place.authority_required refusal", err)
	}
	if v := ProblemValuesOf(err); v["place"] != "" || v["act"] == "" || v["operation"] == "" {
		t.Errorf("values %v; want act and operation, no place", v)
	}
	if strings.Contains(re.Reason, hidden) {
		t.Errorf("reason %q names %s", re.Reason, hidden)
	}
	w := httptest.NewRecorder()
	WriteError(w, httptest.NewRequest(http.MethodGet, "/", nil), err)
	if w.Code != 403 || strings.Contains(w.Body.String(), hidden) || !strings.Contains(w.Body.String(), `"values"`) {
		t.Errorf("HTTP %d %s; want 403 with values and no %s", w.Code, w.Body.String(), hidden)
	}
}

// readsOnly is a scope that reads exactly the items given as type, id pairs.
func readsOnly(pairs ...string) *ReadScope {
	s := &ReadScope{items: map[string]map[string]bool{}, anyOfTyp: map[string]bool{}}
	for i := 0; i+1 < len(pairs); i += 2 {
		s.add(pairs[i], pairs[i+1])
	}
	return s
}

// change_sources by a caller who reads the Decision but not its place.
func TestPlaceHidden_ChangeSourcesUnreadablePlace(t *testing.T) {
	f := newPlaceAuthorityFixture(t)
	f.decision(t, "rat", "ratified")
	if err := f.place(as("human-1", Human), "Decision", "rat", "Domain", f.domain, "asserted"); err != nil {
		t.Fatal(err)
	}
	old := Decision{Node: Node{ID: "rat", Status: "ratified"},
		Sources: DecisionSources{{Repository: "acme/app", Commit: testSHA, Path: "a.md", Role: "source"}}}
	next := old
	next.Sources = DecisionSources{{Repository: "acme/app", Commit: testSHA, Path: "b.md", Role: "source"}}
	ctx := WithReadScope(actorCtx("plain-1"), readsOnly("Decision", "rat")).(Context)
	wantPlaceHidden(t, f.app.prepareDecision(ctx, &old, &next), "DOM1")
}

// ConfirmPremiseCondition by a caller granted the condition but not the
// Decision it flags: a condition is read only with its subject, so the
// caller gets not found, and the flagged Decision's label never reaches it.
// With the Decision readable too, the refusal names it.
func TestPlaceHidden_PremiseConditionUnreadableDecision(t *testing.T) {
	f := newPremiseFixture(t)
	f.placedDecision(t, "d", "ratified")
	f.placedDecision(t, "p", "proposed")
	f.rest(t, "d", "grounded_in", "p")
	sigID, address := f.openPremiseSignal(t, "d")
	label := recordLabel(context.Background(), f.db, "smeldr_decisions", "d")

	err := f.app.ConfirmPremiseCondition(WithReadScope(as("plain-1"), readsOnly("Signal", sigID, "Decision", "p")), address, "why")
	if !errors.Is(err, ErrNotFound) || ProblemTypeOf(err) != problemPremiseConditionNotFound {
		t.Fatalf("Decision unreadable: %v; want premise.condition_not_found", err)
	}
	w := httptest.NewRecorder()
	WriteError(w, httptest.NewRequest(http.MethodGet, "/", nil), err)
	if w.Code != 404 || strings.Contains(w.Body.String(), label) || strings.Contains(w.Body.String(), `"values"`) {
		t.Errorf("HTTP %d %s; want a bare 404 without %s", w.Code, w.Body.String(), label)
	}

	err = f.app.ConfirmPremiseCondition(WithReadScope(as("plain-1"), readsOnly("Signal", sigID, "Decision", "d", "Decision", "p")), address, "why")
	var re *RefusalError
	if !errors.As(err, &re) || ProblemValuesOf(err)["place"] != label || !strings.Contains(re.Reason, label) {
		t.Errorf("Decision readable: %v %v; want the refusal to name %s", err, ProblemValuesOf(err), label)
	}
}

// A coverage rule changed by a caller who reads the rule but not its Area.
func TestPlaceHidden_CoverageRuleUnreadableArea(t *testing.T) {
	f := newAreaGateFixture(t)
	out, err := f.rules.MCPCreate(actorCtx("agent-x", Agent), map[string]any{"area": "ARE1", "repository": "acme/app", "pattern": "a/**"})
	if err != nil {
		t.Fatal(err)
	}
	rule := asItem[*CoverageRule](out)
	ctx := WithReadScope(actorCtx("plain-1", Human), readsOnly("CoverageRule", rule.ID)).(Context)
	_, err = f.rules.MCPUpdate(ctx, rule.Slug, map[string]any{"pattern": "b/**"})
	wantPlaceHidden(t, err, "ARE1")
	err = f.rules.MCPDelete(ctx, rule.Slug)
	wantPlaceHidden(t, err, "ARE1")
}
