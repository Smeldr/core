package smeldr

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// adminDecisionCtx is an admin caller for a Decision module's MCP writes.
func adminDecisionCtx(t *testing.T, store *RoleStore) Context {
	t.Helper()
	govGrant(t, store, "tok-admin", "admin")
	return NewTestContext(User{ID: "tok-admin", Name: "Admin", Roles: []Role{Admin}})
}

// wantRevConflict asserts err is a RevConflictError naming sent and current.
func wantRevConflict(t *testing.T, err error, sent, current int) *RevConflictError {
	t.Helper()
	var rc *RevConflictError
	if !errors.As(err, &rc) || !errors.Is(err, ErrRevConflict) {
		t.Fatalf("err = %v; want a RevConflictError", err)
	}
	if rc.Sent != sent || rc.Current != current {
		t.Errorf("conflict sent %d current %d; want %d and %d", rc.Sent, rc.Current, sent, current)
	}
	if !strings.Contains(rc.Error(), "Read it again") || !strings.Contains(rc.Error(), "Never resend the same change unread") {
		t.Errorf("the message must say what to do: %s", rc.Error())
	}
	return rc
}

// hasRevMissing reports whether a write's result carries rev_missing on rev.
func hasRevMissing(res any) bool { return warningsOf(res)["rev_missing"] == "rev" }

// A Decision update that names the rev it read is saved; a stale one is a
// RevConflictError; none is a warning, and a refusal after the cutover.
func TestRevRequired_Update(t *testing.T) {
	t.Run("matches", func(t *testing.T) {
		m, store, d := decisionModuleAtStatus(t, "proposed")
		ctx := adminDecisionCtx(t, store)
		res, err := m.MCPUpdate(ctx, d.Slug, map[string]any{"Body": "edit", "rev": d.Rev})
		if err != nil {
			t.Fatalf("with the rev it read: %v", err)
		}
		if hasRevMissing(res) {
			t.Error("no rev_missing warning when the rev is sent")
		}
	})
	t.Run("stale", func(t *testing.T) {
		m, store, d := decisionModuleAtStatus(t, "proposed")
		ctx := adminDecisionCtx(t, store)
		if _, err := m.MCPUpdate(ctx, d.Slug, map[string]any{"Body": "first", "rev": d.Rev}); err != nil {
			t.Fatal(err)
		}
		_, err := m.MCPUpdate(ctx, d.Slug, map[string]any{"Body": "second", "rev": d.Rev})
		wantRevConflict(t, err, d.Rev, d.Rev+1)
		got, _ := m.repo.FindByID(context.Background(), d.ID)
		if got.Body != "first" {
			t.Errorf("Body = %q; the stale write must not land", got.Body)
		}
	})
	t.Run("missing warns", func(t *testing.T) {
		m, store, d := decisionModuleAtStatus(t, "proposed")
		ctx := adminDecisionCtx(t, store)
		res, err := m.MCPUpdate(ctx, d.Slug, map[string]any{"Body": "no rev"})
		if err != nil {
			t.Fatalf("before the cutover: %v", err)
		}
		if !hasRevMissing(res) {
			t.Errorf("want a rev_missing warning, got %+v", res)
		}
	})
	t.Run("missing refused after the cutover", func(t *testing.T) {
		m, store, d := decisionModuleAtStatus(t, "proposed")
		m.setRevCutover(time.Now().Add(-time.Hour))
		ctx := adminDecisionCtx(t, store)
		_, err := m.MCPUpdate(ctx, d.Slug, map[string]any{"Body": "no rev"})
		var ve *ValidationError
		if !errors.As(err, &ve) || !strings.Contains(err.Error(), "rev") {
			t.Fatalf("err = %v; want a ValidationError on rev", err)
		}
		got, _ := m.repo.FindByID(context.Background(), d.ID)
		if got.Body == "no rev" {
			t.Error("the refused write landed")
		}
	})
	t.Run("other types unchanged", func(t *testing.T) {
		repo := NewMemoryRepo[*testPost]()
		p := seedPost(t, repo, "Post", Draft)
		m := newTestModule(repo, MCP(MCPRead, MCPWrite))
		res, err := m.MCPUpdate(NewTestContext(User{ID: "e", Roles: []Role{Editor}}), p.Slug, map[string]any{"Title": "Post edited"})
		if err != nil {
			t.Fatal(err)
		}
		if hasRevMissing(res) {
			t.Error("a type outside the governance list gets no rev_missing")
		}
	})
}

// transition_item with a rev: applied and advanced when it matches, a
// RevConflictError when stale; every move advances rev.
func TestRevRequired_Transition(t *testing.T) {
	app, _, repo := newTransitionHookApp(t, nil)
	ctx := TrustedRead(hookCtx())
	seed := seedPost(t, repo, "Item", Draft)
	start, _ := repo.FindBySlug(context.Background(), seed.Slug)

	res, err := app.TransitionItemAt(ctx, "mcp", "testPost", seed.Slug, string(Published), "", start.Rev)
	if err != nil {
		t.Fatalf("with the rev it read: %v", err)
	}
	if res["rev"] != start.Rev+1 {
		t.Errorf("result rev = %v; want %d", res["rev"], start.Rev+1)
	}
	_, err = app.TransitionItemAt(ctx, "mcp", "testPost", seed.Slug, string(Archived), "", start.Rev)
	wantRevConflict(t, err, start.Rev, start.Rev+1)
	if got, _ := repo.FindBySlug(context.Background(), seed.Slug); got.Status != Published {
		t.Errorf("status = %q after the stale move; want published", got.Status)
	}

	// A move without a rev still advances it, so a stale Save behind it fails.
	stale, _ := repo.FindBySlug(context.Background(), seed.Slug)
	if _, err := app.TransitionItem(ctx, "testPost", seed.Slug, string(Archived)); err != nil {
		t.Fatal(err)
	}
	stale.Title = "stale edit"
	if err := repo.Save(context.Background(), stale); !errors.Is(err, ErrRevConflict) {
		t.Errorf("a Save with the rev from before the move: err = %v; want ErrRevConflict", err)
	}
}

// A Decision moved without a rev gets a warning, and is refused after the
// cutover; with one, no warning.
func TestRevRequired_TransitionDecision(t *testing.T) {
	app, db, _ := setupTransitionItemApp(t)
	repo := NewSQLRepo[*Decision](db, Table("smeldr_decisions"))
	seed := func(slug string) *Decision {
		d := &Decision{Node: Node{ID: NewID(), Slug: slug, Status: "proposed"}, Title: "A decision", Scope: "core"}
		if err := repo.Save(context.Background(), d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	ctx := TrustedRead(context.Background())

	d := seed("warned")
	res, err := app.TransitionItem(ctx, "Decision", d.Slug, "archived")
	if err != nil {
		t.Fatal(err)
	}
	if !hasRevMissing(res) {
		t.Errorf("want rev_missing, got %+v", res)
	}

	d = seed("named")
	got, _ := repo.FindByID(context.Background(), d.ID)
	res, err = app.TransitionItemAt(ctx, "mcp", "Decision", d.Slug, "archived", "", got.Rev)
	if err != nil {
		t.Fatal(err)
	}
	if hasRevMissing(res) {
		t.Error("no warning when the rev is named")
	}

	app.cfg.RevCutover = time.Now().Add(-time.Hour)
	d = seed("refused")
	_, err = app.TransitionItem(ctx, "Decision", d.Slug, "archived")
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("after the cutover: err = %v; want a ValidationError on rev", err)
	}
	if now, _ := repo.FindByID(context.Background(), d.ID); now.Status != "proposed" {
		t.Errorf("status = %q; the refused move landed", now.Status)
	}
}

// A ratification's conflict adds the line about what the person read.
func TestRevConflictError_Ratify(t *testing.T) {
	e := &RevConflictError{Type: "Decision", ID: "D118", Sent: 4, Current: 6, Ratify: true}
	if !strings.Contains(e.Error(), "you sent rev 4, it is now rev 6") || !strings.Contains(e.Error(), "show them what changed before they ratify again") {
		t.Errorf("message: %s", e.Error())
	}
	if e.Code() != "rev_conflict" || e.HTTPStatus() != http.StatusConflict || e.Public() != e.Error() {
		t.Errorf("code %q status %d", e.Code(), e.HTTPStatus())
	}
	if strings.Contains((&RevConflictError{Type: "Decision"}).Error(), "ratify again") {
		t.Error("only a ratification carries the ratify line")
	}
}

// The conflict policy's losers advance their rev.
func TestTransition_LosersAdvanceRev(t *testing.T) {
	db, store, prov := signalSchedApp(t, ConflictSupersede)
	insertSignal(t, db, "old", "old-slug", "published")
	scheduleSignal(t, db, "due", "due-slug")
	m := signalModule(db, store, prov, nil)
	revOf := func(id string) int {
		var r int
		if err := db.QueryRow(`SELECT rev FROM smeldr_signals WHERE id = ?`, id).Scan(&r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	before := revOf("old")
	if _, _, err := m.processScheduled(NewBackgroundContext("example.com"), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if after := revOf("old"); after != before+1 {
		t.Errorf("superseded item's rev = %d; want %d", after, before+1)
	}
}

// bumpRev's edges: no rev column, a vanished row, a failed lookup.
func TestBumpRev(t *testing.T) {
	db := newSQLiteDB(t)
	mustExecDB(t, db, `CREATE TABLE no_revs (id TEXT PRIMARY KEY)`)
	mustExecDB(t, db, `INSERT INTO no_revs (id) VALUES ('a')`)
	ctx := context.Background()
	if _, has, err := bumpRev(ctx, db, "no_revs", "NoRev", "a", nil); err != nil || has {
		t.Errorf("no rev column: has=%v err=%v; want false, nil", has, err)
	}
	mustExecDB(t, db, `CREATE TABLE some_revs (id TEXT PRIMARY KEY, rev INTEGER NOT NULL DEFAULT 0)`)
	if _, has, err := bumpRev(ctx, db, "some_revs", "SomeRev", "gone", nil); err != nil || !has {
		t.Errorf("a vanished row with no rev named: has=%v err=%v; want true, nil", has, err)
	}
	if _, _, err := bumpRev(ctx, failingQueryDB{db, "UPDATE"}, "some_revs", "SomeRev", "a", nil); !errors.Is(err, ErrInternal) {
		t.Errorf("a failed update: err = %v; want ErrInternal", err)
	}
	want := 0
	if _, _, err := bumpRev(ctx, db, "some_revs", "SomeRev", "gone", &want); !errors.Is(err, ErrInternal) {
		t.Errorf("a named rev on a vanished row: err = %v; want ErrInternal (the current rev cannot be read)", err)
	}
}

func TestSentRev(t *testing.T) {
	for _, c := range []struct {
		fields map[string]any
		want   int
		ok     bool
	}{
		{map[string]any{"rev": float64(3)}, 3, true},
		{map[string]any{"Rev": 4}, 4, true},
		{map[string]any{"REV": int64(5)}, 5, true},
		{map[string]any{"rev": "x"}, 0, true},
		{map[string]any{"Body": "b"}, 0, false},
	} {
		if got, ok := sentRev(c.fields); got != c.want || ok != c.ok {
			t.Errorf("sentRev(%v) = %d, %v; want %d, %v", c.fields, got, ok, c.want, c.ok)
		}
	}
}

// The rev rule's remaining paths: a future cutover names its date; a stale rev
// on a runtime type; a named rev on a table with no rev column, or one whose
// column check fails, fails closed.
func TestRevRequired_Edges(t *testing.T) {
	future := time.Now().Add(24 * time.Hour)
	w, err := revMissing("Decision", "D1", "update_decision", future)
	if err != nil || w == nil || !strings.Contains(w.Message, future.UTC().Format(time.RFC3339)) {
		t.Errorf("a future cutover: %+v, %v; want a warning naming the date", w, err)
	}

	app, _, repo, _ := dynSchedApp(t)
	n := newDraft(t, repo, "Dynamic")
	_, err = app.TransitionItemAt(TrustedRead(context.Background()), "mcp", repo.typeName, n.Slug, string(Published), "", n.Rev+5)
	wantRevConflict(t, err, n.Rev+5, n.Rev)

	tapp, db, _ := setupTransitionItemApp(t)
	mustExecDB(t, db, `CREATE TABLE no_rev_items (id TEXT PRIMARY KEY, slug TEXT NOT NULL, status TEXT NOT NULL, updated_at DATETIME)`)
	mustExecDB(t, db, `INSERT INTO no_rev_items (id, slug, status) VALUES ('x', 'x', 'draft')`)
	tapp.TypeRegistry().Register(&TypeDescriptor{Name: "NoRevItem", Kind: "compiled"})
	ctx := TrustedRead(context.Background())
	if _, err := tapp.TransitionItemAt(ctx, "mcp", "NoRevItem", "x", string(Published), "", 0); !errors.Is(err, ErrInternal) || !strings.Contains(err.Error(), "no rev column") {
		t.Errorf("a named rev, no rev column: err = %v; want ErrInternal", err)
	}
	if _, err := tapp.TransitionItem(ctx, "NoRevItem", "x", string(Published)); err != nil {
		t.Errorf("no rev named, no rev column: %v; want the move as before", err)
	}
	tapp.cfg.DB = failingQueryDB{db, `."rev" FROM`}
	mustExecDB(t, db, `UPDATE no_rev_items SET status = 'draft' WHERE id = 'x'`)
	if _, err := tapp.TransitionItemAt(ctx, "mcp", "NoRevItem", "x", string(Published), "", 0); !errors.Is(err, ErrInternal) || !strings.Contains(err.Error(), "rev check") {
		t.Errorf("a failed column check with a named rev: err = %v; want ErrInternal", err)
	}
}

func TestNodeRevOf(t *testing.T) {
	type noRev struct{ Title string }
	if got := nodeRevOf(&noRev{}); got != 0 {
		t.Errorf("a type with no Rev: %d; want 0", got)
	}
	if got := nodeRevOf(&testPost{Node: Node{Rev: 3}}); got != 3 {
		t.Errorf("nodeRevOf = %d; want 3", got)
	}
}
