package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A typed refusal sends its values over HTTP only where the message shown
// carries the detail (T1066): a reason, a field message, a rev conflict, a
// 400 or a 409. A bare 403 or 404 sends its type, id, class and rule, no
// values, in the JSON envelope and in problem+json alike.
func TestWriteError_ValuesOnlyWhereShown(t *testing.T) {
	// One real 404 site: Withdraw names the relation in its values.
	ctx := WithReadScope(context.Background(), AllReadScope())
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if err := CreateRelationTables(db); err != nil {
		t.Fatal(err)
	}
	rels, err := NewRelationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	withdrawErr := rels.Withdraw(ctx, "no-such-relation", "gone")
	if ProblemValuesOf(withdrawErr)["relation"] != "no-such-relation" {
		t.Fatalf("Withdraw: %v %v; want relation.not_found with its relation", withdrawErr, ProblemValuesOf(withdrawErr))
	}

	tests := []struct {
		name    string
		err     error
		status  int
		id      ProblemType
		values  bool
		message string
	}{
		{"bare 403", problem(problemTransitionGate, fmt.Errorf("%w: transition a→b requires operation %q", ErrForbidden, "manage"),
			problemValues{"type": "Task", "from": "a", "to": "b", "operation": "manage"}), 403, problemTransitionGate, false, "Forbidden"},
		{"bare 404, real site", withdrawErr, 404, problemRelationNotFound, false, "Not found"},
		{"403 with a reason", fmt.Errorf("Grant: %w", refuse(problemGrantJobNeverGrants, "a job token never grants a role",
			problemValues{"role": "editor"})), 403, problemGrantJobNeverGrants, true, "a job token never grants a role"},
		{"409", problem(problemDecisionPlaceMissing, fmt.Errorf("%w: Decision D1 has no place", ErrConflict),
			problemValues{"decision": "D1"}), 409, problemDecisionPlaceMissing, true, "Conflict"},
		{"400", problem(problemLimitPathsPerCall, fmt.Errorf("%w: at most 200 paths", ErrBadRequest),
			problemValues{"limit": "200"}), 400, problemLimitPathsPerCall, true, "Bad request"},
		{"rev conflict", &RevConflictError{Type: "Decision", ID: "D1", Sent: 1, Current: 2}, 409, problemRevConflict, true, ""},
	}
	for _, tc := range tests {
		for _, accept := range []string{"application/json", problemJSON} {
			t.Run(tc.name+" "+accept, func(t *testing.T) {
				r := httptest.NewRequest(http.MethodGet, "/", nil)
				r.Header.Set("Accept", accept)
				w := httptest.NewRecorder()
				WriteError(w, r, tc.err)
				if w.Code != tc.status {
					t.Fatalf("status %d; want %d (%s)", w.Code, tc.status, w.Body.String())
				}
				var body struct {
					Error  *problemWireBody `json:"error"`
					Detail string           `json:"detail"`
					problemWireBody
				}
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatalf("decode %s: %v", w.Body.String(), err)
				}
				got, message := body.problemWireBody, body.Detail
				if body.Error != nil {
					got, message = *body.Error, body.Error.Message
				}
				if got.ID != tc.id || got.Type != tc.id.URI() || got.Class != problemClass(tc.id) || got.Rule != problemRule(tc.id) {
					t.Errorf("type members %+v; want %s with its class and rule", got, tc.id)
				}
				if (len(got.Values) > 0) != tc.values {
					t.Errorf("values %v; want present %v", got.Values, tc.values)
				}
				if tc.message != "" && message != tc.message {
					t.Errorf("message %q; want %q unchanged", message, tc.message)
				}
			})
		}
	}

	// A ValidationError keeps its field values; an untyped 403 has no type.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	WriteError(w, r, fieldErr(problemRelationToItself, "target_id", "a relation cannot run from a record to itself",
		problemValues{"record": "D1"}))
	var ve struct {
		Error struct {
			Fields []struct {
				Values map[string]string `json:"values"`
			} `json:"fields"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &ve); err != nil || len(ve.Error.Fields) != 1 || ve.Error.Fields[0].Values["record"] != "D1" {
		t.Errorf("validation body %s; want the field's values", w.Body.String())
	}
	w = httptest.NewRecorder()
	WriteError(w, r, Refused("no"))
	if w.Code != 403 || containsKey(w.Body.Bytes(), "id") {
		t.Errorf("untyped refusal body %s; want no type", w.Body.String())
	}
}

// problemWireBody is the type members of an error response.
type problemWireBody struct {
	Type    string            `json:"type"`
	ID      ProblemType       `json:"id"`
	Class   string            `json:"class"`
	Rule    string            `json:"rule"`
	Values  map[string]string `json:"values"`
	Message string            `json:"message"`
}

// containsKey reports whether the JSON envelope's error object has key.
func containsKey(b []byte, key string) bool {
	var env struct {
		Error map[string]any `json:"error"`
	}
	if json.Unmarshal(b, &env) != nil {
		return false
	}
	_, ok := env.Error[key]
	return ok
}

// valuesShown decides by the error the caller is answered with.
func TestValuesShown(t *testing.T) {
	tests := []struct {
		fe   Error
		want bool
	}{
		{Refused("why"), true},
		{&ValidationError{}, true},
		{&RevConflictError{}, true},
		{ErrBadRequest, true},
		{ErrConflict, true},
		{ErrLastAdmin, true},
		{ErrForbidden, false},
		{ErrNotFound, false},
		{ErrUnauth, false},
		{ErrGone, false},
		{ErrNotAcceptable, false},
		{ErrRequestTooLarge, false},
		{ErrTooManyRequests, false},
	}
	for _, tc := range tests {
		if got := valuesShown(tc.fe); got != tc.want {
			t.Errorf("valuesShown(%s %d) = %v; want %v", tc.fe.Code(), tc.fe.HTTPStatus(), got, tc.want)
		}
	}
}
