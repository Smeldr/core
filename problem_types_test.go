package smeldr

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

var updateProblemTypesDoc = flag.Bool("update", false, "regenerate docs/PROBLEM_TYPES.md")

// problemTypesDocPath is the generated list, the one brand and Design key
// their text on.
const problemTypesDocPath = "docs/PROBLEM_TYPES.md"

// The closed list is well formed: ids unique and dotted, a 4xx status, a
// title, a known audience and class on every entry, value names well formed
// and unique within their type, and a replacement that exists.
func TestProblemTypes_List(t *testing.T) {
	idForm := regexp.MustCompile(`^[a-z][a-z_]*\.[a-z][a-z_]*$`)
	valueForm := regexp.MustCompile(`^[a-z][a-z_]*$`)
	seen := map[ProblemType]bool{}
	for _, p := range ProblemTypes() {
		names := map[string]bool{}
		for _, v := range p.Values {
			if !valueForm.MatchString(v) || names[v] {
				t.Errorf("%s: value name %q malformed or twice", p.ID, v)
			}
			names[v] = true
		}
		for _, v := range p.AlwaysValues {
			if !names[v] {
				t.Errorf("%s: always-sent value %q is not one of its values", p.ID, v)
			}
		}
		switch {
		case seen[p.ID]:
			t.Errorf("%s: listed twice", p.ID)
		case !idForm.MatchString(string(p.ID)):
			t.Errorf("%s: not area.reason", p.ID)
		case p.Status < 400 || p.Status > 499:
			t.Errorf("%s: status %d is not 4xx", p.ID, p.Status)
		case p.Title == "":
			t.Errorf("%s: no title", p.ID)
		case p.Audience != "person" && p.Audience != "developer" && p.Audience != "operator":
			t.Errorf("%s: audience %q", p.ID, p.Audience)
		case p.Class != problemClassRefusal && p.Class != problemClassCorrection && p.Class != problemClassConflict && p.Class != problemClassFailure:
			t.Errorf("%s: class %q", p.ID, p.Class)
		case p.ReplacedBy != "" && problemTypeIndex[p.ReplacedBy].ID == "":
			t.Errorf("%s: replaced by %s, which is not listed", p.ID, p.ReplacedBy)
		case constNameOf(p.ID) == "":
			t.Errorf("%s: no constant name", p.ID)
		}
		seen[p.ID] = true
	}
	if len(seen) != len(problemTypeIndex) {
		t.Errorf("index has %d entries, list %d", len(problemTypeIndex), len(seen))
	}
	// ProblemTypes is a copy, its value names too.
	l := ProblemTypes()
	l[0].Title = "changed"
	if problemTypeList[0].Title == "changed" {
		t.Error("ProblemTypes returned the list itself")
	}
	for i := range l {
		if len(l[i].Values) > 0 {
			l[i].Values[0] = "changed"
			if problemTypeList[i].Values[0] == "changed" {
				t.Error("ProblemTypes shares the value names")
			}
			break
		}
	}
}

// problemTypesDoc renders the list as docs/PROBLEM_TYPES.md.
func problemTypesDoc() string {
	var b strings.Builder
	b.WriteString(`# Problem types

<!-- Generated from problem_types.go by: go test -run TestProblemTypesDoc -update. Do not edit by hand. -->

Every refusal and validation error Smeldr core answers with carries a stable **type** (D171, T1039): an id such as ` + "`decision.place_missing`" + ` that never changes when the English message does. A UI keys its own text on the id; core's message stays the API's own words.

- Over HTTP the JSON error envelope carries ` + "`type`" + ` (the RFC 9457 URI ` + "`https://smeldr.dev/problems/<id>`" + `), ` + "`id`" + `, ` + "`class`" + `, ` + "`rule`" + ` and ` + "`values`" + ` for a typed error, and each field its own ` + "`type`" + `, ` + "`id`" + ` and ` + "`values`" + `; a request with ` + "`Accept: application/problem+json`" + ` gets an RFC 9457 problem+json body with the same members, where an error with no type is ` + "`about:blank`" + `.
- In Go, ` + "`smeldr.ProblemTypeOf(err)`" + ` returns the id, ` + "`smeldr.ProblemValuesOf(err)`" + ` the values, and ` + "`smeldr.ProblemTypes()`" + ` the list below.
- **Class** says where a UI shows it (D174): ` + "`refusal`" + ` (a rule says no), ` + "`correction`" + ` (fix the input, at the field), ` + "`conflict`" + ` (the record moved: read it again) or ` + "`failure`" + ` (outside the caller's reach). A UI with no text yet for an id shows its class's shared text.
- **Values** are the named values a type carries, for a UI to fill its own text: each a string (a record as its address, a number in decimal, a list joined with ", "). A type lists every name its refusals use; one refusal sends those its message states, so a UI's text must read without any one of them. A value never says more than the message does, and never carries an item's title or text. A plain field's own name is in the field entry, not in its values.
- **Bold** marks a value every refusal of the type sends (` + "`ProblemTypeInfo.AlwaysValues`" + `): a UI's text may rely on it. A test holds the list to what every site writes.
- An id or a value name is never renamed or reused. A value name may be added to a type. A type that goes out of use is retired: it stays in this list with the version it was retired in, and what replaced it.
- Audience: ` + "`person`" + ` is met in a UI; ` + "`developer`" + ` means a program called the API wrongly; ` + "`operator`" + ` means a migration or setup command.

| Id | Class | Status | Audience | Values | Rule | When | Retired |
|---|---|---|---|---|---|---|---|
`)
	for _, p := range problemTypeList {
		retired := ""
		if p.Retired != "" {
			retired = p.Retired
			if p.ReplacedBy != "" {
				retired += ", replaced by `" + string(p.ReplacedBy) + "`"
			}
		}
		values := make([]string, len(p.Values))
		for i, v := range p.Values {
			values[i] = "`" + v + "`"
			if slices.Contains(p.AlwaysValues, v) {
				values[i] = "**" + values[i] + "**"
			}
		}
		fmt.Fprintf(&b, "| `%s` | %s | %d | %s | %s | %s | %s | %s |\n", p.ID, p.Class, p.Status, p.Audience, strings.Join(values, ", "), p.Rule, p.Title, retired)
	}
	return b.String()
}

// docs/PROBLEM_TYPES.md is the generated list, and no id or value name it
// holds has left the list: an id is retired, never removed, and a value name
// stays on its type.
func TestProblemTypesDoc(t *testing.T) {
	old, err := os.ReadFile(problemTypesDocPath)
	if err != nil && !*updateProblemTypesDoc {
		t.Fatal(err)
	}
	for _, missing := range removedFromDoc(string(old)) {
		t.Errorf("%s (in %s): retire a type, never remove it, and never remove a value name", missing, problemTypesDocPath)
	}
	want := problemTypesDoc()
	if *updateProblemTypesDoc {
		if err := os.WriteFile(problemTypesDocPath, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if string(old) != want {
		t.Errorf("%s is out of date: run go test -run TestProblemTypesDoc -update", problemTypesDocPath)
	}
}

// removedFromDoc returns each id, and each id's value name, that doc lists
// but the list no longer has. A row before the Class column (no values)
// names only its id.
func removedFromDoc(doc string) []string {
	var out []string
	for _, line := range strings.Split(doc, "\n") {
		m := regexp.MustCompile("^\\| `([a-z_.]+)` \\|").FindStringSubmatch(line)
		if m == nil {
			continue
		}
		p, ok := problemTypeIndex[ProblemType(m[1])]
		if !ok {
			out = append(out, m[1]+" is no longer listed")
			continue
		}
		cells := strings.Split(line, " | ")
		if len(cells) < 8 {
			continue
		}
		have := map[string]bool{}
		for _, v := range p.Values {
			have[v] = true
		}
		for _, v := range regexp.MustCompile("`([a-z_]+)`").FindAllStringSubmatch(cells[4], -1) {
			if !have[v[1]] {
				out = append(out, m[1]+" no longer declares value "+v[1])
			}
		}
	}
	return out
}

// ProblemTypeOf reads the type each constructor gives, looks through
// wrapping, gives the two single-meaning sentinels and the two conflict types
// their type, and returns "" for an untyped error.
func TestProblemTypeOf(t *testing.T) {
	wrapped := problem(problemDecisionPlaceMissing, fmt.Errorf("%w: ratify needs a place", ErrConflict), problemValues{"decision": "D1"})
	tests := []struct {
		name string
		err  error
		want ProblemType
	}{
		{"nil", nil, ""},
		{"plain", errors.New("x"), ""},
		{"untyped sentinel", ErrForbidden, ""},
		{"problem", wrapped, problemDecisionPlaceMissing},
		{"problem wrapped again", fmt.Errorf("ratify D1: %w", wrapped), problemDecisionPlaceMissing},
		{"refuse", refuse(problemTransitionGate, "you lack ratify", nil), problemTransitionGate},
		{"Refused", Refused("no"), ""},
		{"fieldErr", fieldErr(problemFieldRequired, "title", "required", nil), problemFieldRequired},
		{"Err", Err("title", "required"), ""},
		{"several typed fields", Require(fieldErr(problemFieldRequired, "a", "required", nil), Err("b", "x")), problemValidationFailed},
		{"several untyped fields", Require(Err("a", "x"), Err("b", "y")), ""},
		{"rev conflict error", &RevConflictError{Type: "Decision", ID: "D1", Sent: 1, Current: 2}, problemRevConflict},
		{"rev conflict sentinel", fmt.Errorf("save: %w", ErrRevConflict), problemRevConflict},
		{"last admin", ErrLastAdmin, problemTokenLastAdmin},
		{"live relation", &liveRelationExists{ID: "r1"}, problemRelationAlreadyExists},
		{"typed beats sentinel", problem(problemSlugTaken, ErrRevConflict, nil), problemSlugTaken},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ProblemTypeOf(tc.err); got != tc.want {
				t.Errorf("ProblemTypeOf = %q, want %q", got, tc.want)
			}
		})
	}
}

// A type changes nothing else about an error: the same text, errors.Is and
// errors.As as before.
func TestProblem_KeepsTheError(t *testing.T) {
	inner := fmt.Errorf("%w: slug taken", ErrConflict)
	err := problem(problemSlugTaken, inner, nil)
	if err.Error() != inner.Error() {
		t.Errorf("Error() = %q, want %q", err.Error(), inner.Error())
	}
	if !errors.Is(err, ErrConflict) {
		t.Error("errors.Is(ErrConflict) lost")
	}
	var fe Error
	if !errors.As(err, &fe) || fe.Code() != "conflict" {
		t.Errorf("errors.As(Error) = %v", fe)
	}
	r := refuse(problemTransitionGate, "you lack ratify", nil)
	if r.Error() != Refused("you lack ratify").Error() || !errors.Is(r, ErrForbidden) {
		t.Errorf("refuse = %v", r)
	}
	f := fieldErr(problemFieldRequired, "title", "required", nil)
	if f.Error() != Err("title", "required").Error() {
		t.Errorf("fieldErr = %v", f)
	}
	if got := f.Fields(); got[0].ID != problemFieldRequired || got[0].Type != "https://smeldr.dev/problems/field.required" {
		t.Errorf("Fields = %+v", got)
	}
	if got := Err("title", "required").Fields(); got[0].ID != "" || got[0].Type != "" {
		t.Errorf("untyped Fields = %+v", got)
	}
}

func TestProblemType_URI(t *testing.T) {
	if got := ProblemType("").URI(); got != "about:blank" {
		t.Errorf("empty URI = %q", got)
	}
	if got := problemSlugTaken.URI(); got != "https://smeldr.dev/problems/slug.taken" {
		t.Errorf("URI = %q", got)
	}
	if got := problemTitle("", http.StatusTeapot); got != http.StatusText(http.StatusTeapot) {
		t.Errorf("untyped title = %q", got)
	}
}

// writeErr runs WriteError for err with the Accept header, and returns the
// response.
func writeErr(err error, accept string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if accept != "" {
		r.Header.Set("Accept", accept)
	}
	r.Header.Set("X-Request-ID", "req-1")
	WriteError(w, r, err)
	return w
}

// The JSON envelope gains type, id and rule, and keeps code and message.
func TestWriteError_EnvelopeCarriesType(t *testing.T) {
	w := writeErr(problem(problemDecisionPlaceMissing, fmt.Errorf("%w: ratify needs a place", ErrConflict), problemValues{"decision": "D1"}), "")
	var body struct {
		Error struct {
			Code, Message, Type, ID, Class, Rule, RequestID string
			Values                                          map[string]string
			Fields                                          []FieldError
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	e := body.Error
	if w.Code != 409 || e.Code != "conflict" || e.Message != "Conflict" || e.ID != "decision.place_missing" ||
		e.Type != "https://smeldr.dev/problems/decision.place_missing" || !strings.Contains(e.Rule, "D142") ||
		e.Class != problemClassRefusal || e.Values["decision"] != "D1" {
		t.Errorf("envelope = %d %s", w.Code, w.Body.String())
	}
	w = writeErr(fieldErr(problemFieldTooLong, "title", "too long", problemValues{"limit": "120"}), "")
	body.Error.Fields, body.Error.Values = nil, nil
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if e := body.Error; e.Values["limit"] != "120" || len(e.Fields) != 1 || e.Fields[0].Values["limit"] != "120" || e.Class != problemClassCorrection {
		t.Errorf("field envelope = %s", w.Body.String())
	}
	w = writeErr(ErrNotFound, "")
	for _, k := range []string{`"type"`, `"id"`, `"class"`, `"values"`} {
		if strings.Contains(w.Body.String(), k) {
			t.Errorf("untyped envelope has %s: %s", k, w.Body.String())
		}
	}
}

// Accept: application/problem+json gets an RFC 9457 body; a 5xx never
// carries a type.
func TestWriteError_ProblemJSON(t *testing.T) {
	w := writeErr(refuse(problemTransitionGate, "you lack ratify over D1's place", problemValues{"operation": "ratify"}), "application/problem+json")
	if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var p problemDetails
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Type != "https://smeldr.dev/problems/transition.gate" || p.ID != problemTransitionGate || p.Status != 403 ||
		p.Detail != "you lack ratify over D1's place" || p.Code != "forbidden" || p.Title == "" || p.RequestID != "req-1" ||
		p.Class != problemClassRefusal || p.Values["operation"] != "ratify" {
		t.Errorf("problem = %+v", p)
	}

	w = writeErr(Require(fieldErr(problemFieldRequired, "title", "required", nil), fieldErr(problemFieldTooLong, "body", "too long", problemValues{"limit": "120"})), "application/problem+json")
	p = problemDetails{}
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.ID != problemValidationFailed || p.Status != 422 || len(p.Fields) != 2 || p.Fields[1].ID != problemFieldTooLong ||
		p.Values != nil || p.Fields[1].Values["limit"] != "120" || p.Fields[0].Values != nil {
		t.Errorf("validation problem = %+v", p)
	}

	w = writeErr(problem(problemSlugTaken, fmt.Errorf("%w: boom", ErrInternal), nil), "application/problem+json")
	p = problemDetails{}
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if w.Code != 500 || p.Type != "about:blank" || p.ID != "" || p.Title != "Internal Server Error" || p.Class != "" || p.Values != nil {
		t.Errorf("500 problem = %d %+v", w.Code, p)
	}

	w = writeErr(errors.New("unknown"), "application/problem+json")
	if w.Code != 500 || !strings.Contains(w.Body.String(), `"type":"about:blank"`) {
		t.Errorf("unhandled problem = %d %s", w.Code, w.Body.String())
	}
}

// Asking for both problem+json and HTML gets problem+json; HTML alone stays
// HTML.
func TestWriteError_AcceptOrder(t *testing.T) {
	if ct := writeErr(ErrNotFound, "text/html, application/problem+json").Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("both: Content-Type = %q", ct)
	}
	if ct := writeErr(ErrNotFound, "text/html").Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("html: Content-Type = %q", ct)
	}
}

// ProblemValuesOf reads the values each constructor gives, looks through
// wrapping, gives the two conflict types theirs from their fields, keeps a
// several-field error's values on its fields, and returns a copy.
func TestProblemValuesOf(t *testing.T) {
	placed := problem(problemDecisionPlaceMissing, fmt.Errorf("%w: ratify needs a place", ErrConflict), problemValues{"decision": "D1"})
	tests := []struct {
		name string
		err  error
		want map[string]string
	}{
		{"nil", nil, nil},
		{"plain", errors.New("x"), nil},
		{"untyped", Refused("no"), nil},
		{"problem", placed, map[string]string{"decision": "D1"}},
		{"wrapped again", fmt.Errorf("ratify: %w", placed), map[string]string{"decision": "D1"}},
		{"empty values", problem(problemSlugTaken, ErrConflict, problemValues{}), nil},
		{"refuse", refuse(problemRatifyAutomatedClass, "an agent never ratifies", problemValues{"token_class": "agent"}), map[string]string{"token_class": "agent"}},
		{"fieldErr", fieldErr(problemFieldTooLong, "title", "too long", problemValues{"limit": "120"}), map[string]string{"limit": "120"}},
		{"several fields", Require(fieldErr(problemFieldTooLong, "a", "too long", problemValues{"limit": "1"}), Err("b", "x")), nil},
		{"rev conflict", &RevConflictError{Type: "Decision", ID: "D1", Sent: 3, Current: 4},
			map[string]string{"type": "Decision", "record": "D1", "rev_sent": "3", "rev_current": "4"}},
		{"rev conflict under a type with none", problem(problemRevConflict, &RevConflictError{Type: "Area", ID: "AR1", Sent: 1, Current: 2}, nil),
			map[string]string{"type": "Area", "record": "AR1", "rev_sent": "1", "rev_current": "2"}},
		{"live relation", &liveRelationExists{ID: "r1"}, map[string]string{"relation": "r1"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ProblemValuesOf(tc.err)
			if fmt.Sprint(got) != fmt.Sprint(tc.want) || (got == nil) != (tc.want == nil) {
				t.Errorf("ProblemValuesOf = %v, want %v", got, tc.want)
			}
		})
	}
	ProblemValuesOf(placed)["decision"] = "changed"
	if ProblemValuesOf(placed)["decision"] != "D1" {
		t.Error("ProblemValuesOf returned the values themselves")
	}
}

// The site check holds a typed call's values to its type's declared names:
// literal keys, each declared, none twice; a declared name may be left out.
func TestCheckValuesArg(t *testing.T) {
	// A type that declares at least two values, and one that declares none.
	var with, without string
	for _, p := range problemTypeList {
		switch {
		case with == "" && len(p.Values) > 1:
			with = constNameOf(p.ID)
		case without == "" && len(p.Values) == 0:
			without = constNameOf(p.ID)
		}
	}
	if with == "" || without == "" {
		t.Fatal("need a type with two values and one without")
	}
	var full []string
	for _, v := range declaredValues[with] {
		full = append(full, strconv.Quote(v)+": x")
	}
	lit := func(entries ...string) string { return "problemValues{" + strings.Join(entries, ", ") + "}" }
	plus := func(extra string) []string { return append(append([]string(nil), full...), extra) }
	tests := []struct {
		name, constName, expr string
		ok                    bool
	}{
		{"nil for none", without, "nil", true},
		{"nil for some", with, "nil", true},
		{"exact set", with, lit(full...), true},
		{"one left out", with, lit(full[1:]...), true},
		{"one extra", with, lit(plus(`"zz_extra": x`)...), false},
		{"twice", with, lit(plus(full[0])...), false},
		{"computed key", with, lit(plus("k: x")...), false},
		{"a variable", with, "v", false},
		{"another literal type", with, "map[string]string{}", false},
		{"not key value", with, "problemValues{x}", false},
		{"values for none", without, lit(`"a": x`), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e, err := parser.ParseExpr(tc.expr)
			if err != nil {
				t.Fatal(err)
			}
			if msg := checkValuesArg(tc.constName, e); (msg == "") != tc.ok {
				t.Errorf("checkValuesArg(%s) = %q", tc.expr, msg)
			}
		})
	}

	field := func(src string) string {
		e, err := parser.ParseExpr(src)
		if err != nil {
			t.Fatal(err)
		}
		return checkFieldErrorValues(e.(*ast.CompositeLit))
	}
	if msg := field("fieldError{field: f, typ: " + without + "}"); msg != "" {
		t.Errorf("fieldError with no values for none = %q", msg)
	}
	if msg := field("fieldError{typ: " + with + ", values: " + lit(plus(`"zz_extra": x`)...) + "}"); msg == "" {
		t.Error("fieldError with an undeclared value passed")
	}
	if msg := field("fieldError{typ: " + with + ", values: " + lit(full...) + "}"); msg != "" {
		t.Errorf("fieldError with its values = %q", msg)
	}
	if msg := field("fieldError{typ: someVar}"); msg == "" {
		t.Error("fieldError with an unlisted typ passed")
	}
}
