package smeldr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// T914 (D148): pinned external sources on a Decision.

const testSHA = "0123456789abcdef0123456789abcdef01234567"

func testSource(path string) map[string]any {
	return map[string]any{"repository": "acme/app", "commit": testSHA, "path": path, "role": "source"}
}

func TestNormalizeDecisionSources(t *testing.T) {
	good := DecisionSource{Repository: " Acme/App ", Commit: strings.ToUpper(testSHA), Path: " docs/adr/7.md ", Anchor: " ## Money ", Role: " evidence ", Hash: "SHA256:" + strings.Repeat("AB", 32)}
	got, err := normalizeDecisionSources(DecisionSources{good})
	if err != nil {
		t.Fatal(err)
	}
	want := DecisionSource{Repository: "acme/app", Commit: testSHA, Path: "docs/adr/7.md", Anchor: "## Money", Role: "evidence", Hash: "sha256:" + strings.Repeat("ab", 32)}
	if got[0] != want {
		t.Errorf("got %+v, want %+v", got[0], want)
	}
	if got, err := normalizeDecisionSources(nil); err != nil || got == nil || len(got) != 0 {
		t.Errorf("nil: %v %v", got, err)
	}
	ok := DecisionSource{Repository: "acme/app", Commit: testSHA, Path: "a.md", Role: "source"}
	with := func(f func(*DecisionSource)) DecisionSources { s := ok; f(&s); return DecisionSources{s} }
	many := DecisionSources{}
	for i := 0; i <= DecisionSourcesMax; i++ {
		s := ok
		s.Anchor = strings.Repeat("x", i+1)
		many = append(many, s)
	}
	for _, tc := range []struct {
		name, field string
		in          DecisionSources
	}{
		{"repository", "sources[0].repository", with(func(s *DecisionSource) { s.Repository = "acme" })},
		{"branch name", "sources[0].commit", with(func(s *DecisionSource) { s.Commit = "main" })},
		{"short SHA", "sources[0].commit", with(func(s *DecisionSource) { s.Commit = testSHA[:7] })},
		{"wildcard path", "sources[0].path", with(func(s *DecisionSource) { s.Path = "docs/*.md" })},
		{"leading slash", "sources[0].path", with(func(s *DecisionSource) { s.Path = "/a.md" })},
		{"dot dot", "sources[0].path", with(func(s *DecisionSource) { s.Path = "../a.md" })},
		{"empty path", "sources[0].path", with(func(s *DecisionSource) { s.Path = "" })},
		{"long anchor", "sources[0].anchor", with(func(s *DecisionSource) { s.Anchor = strings.Repeat("a", 201) })},
		{"two-line anchor", "sources[0].anchor", with(func(s *DecisionSource) { s.Anchor = "a\nb" })},
		{"role", "sources[0].role", with(func(s *DecisionSource) { s.Role = "inspiration" })},
		{"hash length", "sources[0].hash", with(func(s *DecisionSource) { s.Hash = "sha256:abc" })},
		{"hash algorithm", "sources[0].hash", with(func(s *DecisionSource) { s.Hash = "sha1:" + testSHA })},
		{"too many", "sources", many},
		{"duplicate", "sources[1].path", DecisionSources{ok, ok}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := normalizeDecisionSources(tc.in)
			var ve *ValidationError
			if !errors.As(err, &ve) || !strings.Contains(err.Error(), tc.field) {
				t.Errorf("err = %v, want a validation error on %s", err, tc.field)
			}
		})
	}
}

func TestDecisionSources_Storage(t *testing.T) {
	var s DecisionSources
	for _, src := range []any{nil, "", []byte("  "), `[]`, `null`} {
		if err := s.Scan(src); err != nil || s == nil || len(s) != 0 {
			t.Errorf("Scan(%v) = %v, %v", src, s, err)
		}
	}
	if err := s.Scan(`[{"repository":"a/b","commit":"c","path":"p","role":"source"}]`); err != nil || len(s) != 1 || s[0].Repository != "a/b" {
		t.Errorf("Scan JSON = %v, %v", s, err)
	}
	if err := s.Scan(42); err == nil {
		t.Error("Scan(int): no error")
	}
	if err := s.Scan("{"); err == nil {
		t.Error("Scan(bad JSON): no error")
	}
	if v, err := DecisionSources(nil).Value(); err != nil || v != "[]" {
		t.Errorf("nil Value = %v, %v", v, err)
	}
	if v, err := (DecisionSources{{Repository: "a/b"}}).Value(); err != nil || !strings.Contains(v.(string), `"a/b"`) {
		t.Errorf("Value = %v, %v", v, err)
	}
	if b, err := json.Marshal(struct{ S DecisionSources }{}); err != nil || string(b) != `{"S":[]}` {
		t.Errorf("nil marshals as %s, %v", b, err)
	}
	if b, err := json.Marshal(DecisionSources{{Repository: "a/b"}}); err != nil || !strings.Contains(string(b), `"repository":"a/b"`) {
		t.Errorf("marshal = %s, %v", b, err)
	}
	// An old table gains the column, once.
	db := newSQLiteDB(t)
	if _, err := db.Exec(`CREATE TABLE smeldr_decisions (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := EnsureDecisionSourcesColumn(context.Background(), db); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO smeldr_decisions (id) VALUES ('d')`); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := db.QueryRow(`SELECT sources FROM smeldr_decisions`).Scan(&stored); err != nil || stored != "[]" {
		t.Errorf("default = %q, %v", stored, err)
	}
	if err := EnsureDecisionSourcesColumn(context.Background(), failingDB{DB: db, match: "smeldr_decisions", err: errors.New("down")}); err == nil {
		t.Error("failing database: no error")
	}
}

func TestDecisionSources_WhoMayChange(t *testing.T) {
	f := newPlaceAuthorityFixture(t)
	decisions := findModule[*Decision](t, f.app)
	ctx := context.Background()
	out, err := decisions.MCPCreate(actorCtx("plain-1"), map[string]any{"title": "Money in cents", "sources": []any{testSource("docs/money.md")}})
	if err != nil {
		t.Fatalf("Author creates with sources: %v", err)
	}
	d := asItem[*Decision](out)
	if len(d.Sources) != 1 || d.Sources[0].Commit != testSHA {
		t.Fatalf("sources = %+v", d.Sources)
	}
	// Proposed: the Author changes them freely.
	if _, err := decisions.MCPUpdate(actorCtx("plain-1"), d.Slug, map[string]any{"sources": []any{testSource("docs/money.md"), testSource("docs/cents.md")}}); err != nil {
		t.Fatalf("proposed edit: %v", err)
	}
	// An invalid source is refused.
	bad := map[string]any{"repository": "acme/app", "commit": "main", "path": "a.md", "role": "source"}
	if _, err := decisions.MCPUpdate(actorCtx("plain-1"), d.Slug, map[string]any{"sources": []any{bad}}); err == nil || !strings.Contains(err.Error(), "never a branch") {
		t.Errorf("branch name: %v", err)
	}
	// Placed in dom-1 and ratified: the state is locked, so an update of the
	// content, sources included, is refused for everyone.
	if err := f.place(ctx, "Decision", d.ID, "Domain", f.domain, "asserted"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE smeldr_decisions SET status = 'ratified' WHERE id = $1`, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := decisions.MCPUpdate(actorCtx("human-1", Human), d.Slug, map[string]any{"sources": []any{}}); !errors.Is(err, ErrConflict) {
		t.Errorf("update of a ratified Decision: %v, want the lock's conflict", err)
	}

	// The gate itself (D148), for a write that does reach it: a transition
	// that also carries content.
	ratified := *d
	ratified.Status = "ratified"
	ratified.Sources = DecisionSources{{Repository: "acme/app", Commit: testSHA, Path: "docs/money.md", Role: "source"}}
	change := func(c Context, old Decision, sources ...string) error {
		next := old
		next.Sources = nil
		for _, p := range sources {
			next.Sources = append(next.Sources, DecisionSource{Repository: "acme/app", Commit: testSHA, Path: p, Role: "source"})
		}
		return f.app.prepareDecision(c, &old, &next)
	}
	wantGateRefusal(t, change(actorCtx("plain-1"), ratified, "docs/other.md"), "sources needs ratify over DOM1 (D148)")
	wantGateRefusal(t, change(actorCtx("agent-1", Agent), ratified, "docs/other.md"), "never ratifies")
	if err := change(actorCtx("plain-1"), ratified, "docs/money.md"); err != nil {
		t.Errorf("unchanged sources: %v", err)
	}
	if err := change(actorCtx("human-2", Human), ratified, "docs/other.md"); err != nil {
		t.Errorf("domain-approver of its Domain: %v", err)
	}
	if err := change(actorCtx("human-1", Human), ratified); err != nil {
		t.Errorf("approver clears them: %v", err)
	}
	empty := ratified
	empty.Sources = DecisionSources{}
	if err := change(actorCtx("plain-1"), empty); err != nil {
		t.Errorf("empty to empty: %v", err)
	}
	wantGateRefusal(t, change(actorCtx("plain-1"), empty, "docs/new.md"), "needs ratify")
	unplaced := ratified
	unplaced.ID = "unplaced"
	if err := change(actorCtx("human-1", Human), unplaced, "a.md"); !errors.Is(err, ErrForbidden) || !strings.Contains(err.Error(), "it has none") {
		t.Errorf("unplaced: %v", err)
	}
	if err := f.app.prepareDecision(actorCtx("plain-1"), &ratified, &Decision{Sources: DecisionSources{{Commit: "x"}}}); err == nil {
		t.Error("invalid sources passed the hook")
	}

	// Through HTTP: moving ratified -> pending-re-evaluation while changing
	// the sources reaches the gate.
	put := func(user string, roles ...Role) int {
		var rev int
		if err := f.db.QueryRow(`SELECT rev FROM smeldr_decisions WHERE id = $1`, d.ID).Scan(&rev); err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(map[string]any{"Title": d.Title, "Status": "pending-re-evaluation", "Rev": rev, "Sources": []any{testSource("docs/moved.md")}})
		w := httptest.NewRecorder()
		r := withUser(httptest.NewRequest(http.MethodPut, "/decisions/"+d.Slug, bytes.NewReader(body)), User{ID: user, Roles: append([]Role{Editor}, roles...)})
		r.SetPathValue("slug", d.Slug)
		decisions.updateHandler(w, r)
		return w.Code
	}
	if code := put("plain-1"); code != http.StatusForbidden {
		t.Errorf("Author re-pins through a transition: %d, want 403", code)
	}
	if code := put("human-1", Human); code != http.StatusOK {
		t.Errorf("approver re-pins through a transition: %d, want 200", code)
	}

	// A place that cannot be read fails closed.
	if _, err := f.db.Exec(`ALTER TABLE smeldr_relations RENAME TO relations_gone`); err != nil {
		t.Fatal(err)
	}
	if err := change(actorCtx("human-1", Human), ratified, "docs/x.md"); !errors.Is(err, ErrInternal) {
		t.Errorf("place unreadable: %v", err)
	}
}

func TestDecisionSources_NoGovernance(t *testing.T) {
	app, _, _ := placesApp(t)
	old := &Decision{Node: Node{ID: "d", Status: "ratified"}}
	next := &Decision{Node: Node{ID: "d", Status: "ratified"}, Sources: DecisionSources{{Repository: "acme/app", Commit: testSHA, Path: "a.md", Role: "source"}}}
	if err := app.prepareDecision(authorCtx(), old, next); err != nil {
		t.Errorf("no governance: %v", err)
	}
}

func TestDecisionSources_MCPSchema(t *testing.T) {
	app, _, _ := placesApp(t)
	for _, f := range findModule[*Decision](t, app).MCPSchema() {
		if f.JSONName != "sources" {
			if f.Items != nil {
				t.Errorf("%s has items", f.JSONName)
			}
			continue
		}
		props, _ := f.Items["properties"].(map[string]any)
		if f.Type != "array" || len(props) != 6 {
			t.Fatalf("sources schema: %+v", f)
		}
		if role := props["role"].(map[string]any); len(role["enum"].([]string)) != 3 {
			t.Errorf("role: %v", role)
		}
		if req := f.Items["required"].([]string); strings.Join(req, ",") != "repository,commit,path,role" {
			t.Errorf("required: %v", req)
		}
		return
	}
	t.Error("no sources field")
}
