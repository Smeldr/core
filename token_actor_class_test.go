// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

// newClassTokensDB is a smeldr_tokens table with user_id and actor_class, the
// shape an application has after both Ensure functions ran.
func newClassTokensDB(t *testing.T) *sql.DB {
	t.Helper()
	db := newTestTokensDBWithUserID(t)
	if err := EnsureTokenActorClassColumn(context.Background(), db); err != nil {
		t.Fatalf("EnsureTokenActorClassColumn: %v", err)
	}
	return db
}

func mintUser(t *testing.T, ts *TokenStore, name, role string, class Role) (User, string) {
	t.Helper()
	raw, _, err := ts.CreateClassified(context.Background(), name, role, class, time.Hour)
	if err != nil {
		t.Fatalf("CreateClassified(%s, %s, %q): %v", name, role, class, err)
	}
	u, ok := VerifyTokenString(raw, []byte(testSecret), ts)
	if !ok {
		t.Fatalf("minted token did not verify")
	}
	return u, raw
}

// The classification is the second entry, the permission role stays first, and
// it survives verification; the kind provenance records follows it.
func TestCreateClassified_RolesAndKind(t *testing.T) {
	ts := NewTokenStore(newClassTokensDB(t), testSecret)
	tests := []struct {
		role     string
		class    Role
		wantRole []Role
		wantKind string
	}{
		{"editor", Agent, []Role{Editor, Agent}, "agent"},
		{"editor", Job, []Role{Editor, Job}, "job"},
		{"admin", Agent, []Role{Admin, Agent}, "agent"},
		{"editor", Human, []Role{Editor, Human}, "human"},
		{"author", "", []Role{Author}, "unclassified"},
	}
	for _, tt := range tests {
		t.Run(tt.role+"/"+string(tt.class), func(t *testing.T) {
			u, _ := mintUser(t, ts, "tok", tt.role, tt.class)
			if len(u.Roles) != len(tt.wantRole) {
				t.Fatalf("roles = %v, want %v", u.Roles, tt.wantRole)
			}
			for i := range tt.wantRole {
				if u.Roles[i] != tt.wantRole[i] {
					t.Fatalf("roles = %v, want %v (permission role first)", u.Roles, tt.wantRole)
				}
			}
			if got := actorKindFor(u.ID, u.Roles); got != tt.wantKind {
				t.Errorf("actor kind = %q, want %q", got, tt.wantKind)
			}
		})
	}
}

// The classification never changes a permission: every built-in role check and
// the exact-match check give the same answer with and without the tag.
func TestCreateClassified_NeverChangesPermissions(t *testing.T) {
	ts := NewTokenStore(newClassTokensDB(t), testSecret)
	for _, role := range []string{"author", "editor", "admin"} {
		plain, _ := mintUser(t, ts, "plain", role, "")
		for _, class := range []Role{Agent, Job, Human} {
			tagged, _ := mintUser(t, ts, "tagged", role, class)
			for _, r := range []Role{Guest, Author, Editor, Admin, Agent, Job, "nonsense"} {
				if plain.HasRole(r) != tagged.HasRole(r) {
					t.Errorf("%s+%s: HasRole(%s) = %v, untagged %v", role, class, r, tagged.HasRole(r), plain.HasRole(r))
				}
			}
			for _, r := range []Role{Author, Editor, Admin} {
				if plain.Is(r) != tagged.Is(r) {
					t.Errorf("%s+%s: Is(%s) differs from the untagged token", role, class, r)
				}
			}
		}
	}
	// A bare classification tag is no permission at all.
	if HasRole([]Role{Agent}, Guest) || HasRole([]Role{Job}, Guest) {
		t.Error("a classification tag alone must satisfy no role")
	}
}

func TestCreateClassified_Validation(t *testing.T) {
	db := newClassTokensDB(t)
	ts := NewTokenStore(db, testSecret)
	ctx := context.Background()
	for _, tt := range []struct {
		name, role string
		class      Role
	}{
		{"a class that is no tag", "editor", "person"},
		{"a permission role is not a class", "editor", Admin},
		{"garbage class", "editor", "robot"},
		{"classified token needs a permission role", "human", Human},
		{"empty role", "", Job},
	} {
		t.Run(tt.name, func(t *testing.T) {
			raw, _, err := ts.CreateClassified(ctx, "x", tt.role, tt.class, time.Hour)
			var ve *ValidationError
			if !errors.As(err, &ve) || raw != "" {
				t.Errorf("err = %v raw = %q, want a ValidationError and no token", err, raw)
			}
		})
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_tokens`).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d tokens stored after refused mints (%v), want 0", n, err)
	}
}

// List reports the stored class; unclassified, NULL and pre-column rows are "".
func TestTokenStore_List_ActorClass(t *testing.T) {
	db := newClassTokensDB(t)
	ts := NewTokenStore(db, testSecret)
	ctx := context.Background()
	mintUser(t, ts, "bot", "editor", Agent)
	mintUser(t, ts, "cron", "editor", Job)
	if _, err := ts.Create(ctx, "human-ish", "author", time.Hour); err != nil {
		t.Fatalf("Create: %v", err)
	}
	recs, err := ts.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := map[string]string{}
	for _, r := range recs {
		got[r.Name] = r.ActorClass
	}
	want := map[string]string{"bot": "agent", "cron": "job", "human-ish": ""}
	for name, w := range want {
		if g, ok := got[name]; !ok || g != w {
			t.Errorf("ActorClass of %s = %q (present %v), want %q", name, g, ok, w)
		}
	}
}

// A table that predates actor_class: unclassified mints and List keep working,
// a classified mint is refused with the named error and stores nothing.
func TestCreateClassified_TableWithoutColumn(t *testing.T) {
	for name, db := range map[string]*sql.DB{
		"user_id only": newTestTokensDBWithUserID(t),
		"legacy":       newTestTokensDB(t),
	} {
		t.Run(name, func(t *testing.T) {
			ts := NewTokenStore(db, testSecret)
			ctx := context.Background()
			if _, err := ts.Create(ctx, "plain", "editor", time.Hour); err != nil {
				t.Fatalf("an unclassified mint must keep working: %v", err)
			}
			if _, _, err := ts.CreateClassified(ctx, "same", "editor", "", time.Hour); err != nil {
				t.Fatalf("CreateClassified with no class is an unclassified mint: %v", err)
			}
			raw, _, err := ts.CreateClassified(ctx, "bot", "editor", Agent, time.Hour)
			var ve *ValidationError
			if !errors.As(err, &ve) || raw != "" || !containsText(err.Error(), "EnsureTokenActorClassColumn") {
				t.Fatalf("classified mint = %q, %v, want a ValidationError naming EnsureTokenActorClassColumn", raw, err)
			}
			recs, err := ts.List(ctx)
			if err != nil || len(recs) != 2 {
				t.Fatalf("List = %d records, %v, want the two unclassified", len(recs), err)
			}
			for _, r := range recs {
				if r.ActorClass != "" {
					t.Errorf("%s ActorClass = %q on a table without the column", r.Name, r.ActorClass)
				}
			}
		})
	}
}

func containsText(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestEnsureTokenActorClassColumn_Idempotent(t *testing.T) {
	db := newTestTokensDBWithUserID(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := EnsureTokenActorClassColumn(ctx, db); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
	if ok, err := columnExists(ctx, db, "smeldr_tokens", "actor_class"); err != nil || !ok {
		t.Error("the column is missing after Ensure")
	}
	if err := EnsureTokenActorClassColumn(ctx, &queryFailDB{}); err == nil {
		t.Error("a failing database must surface an error")
	}
}

// A token carrying both tags is read as job; hand-signed with the tag already
// records agent today, with no mint involved.
func TestActorKind_TagPrecedenceAndHandSigned(t *testing.T) {
	if got := actorKindFor("u", []Role{Editor, Agent, Job}); got != "job" {
		t.Errorf("both tags = %q, want job", got)
	}
	raw, err := SignToken(User{ID: "u1", Roles: []Role{Editor, Agent}}, testSecret, time.Hour)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	u, ok := VerifyTokenString(raw, []byte(testSecret), nil)
	if !ok || actorKindFor(u.ID, u.Roles) != "agent" {
		t.Errorf("hand-signed token = %v, %v, want agent", u, ok)
	}
}

// The classification reaches a real provenance record, and the SignalEvent
// keeps the permission role as the actor's role.
func TestClassifiedToken_RecordsAgentInProvenance(t *testing.T) {
	ts := NewTokenStore(newClassTokensDB(t), testSecret)
	u, _ := mintUser(t, ts, "core-implementer", "editor", Agent)

	app, db, rs := setupTransitionItemApp(t)
	prov := &fakeProvenanceStore{}
	app.Provenance(prov)
	ratifyDecision(t, app, db, rs, "dec-c", "dec-c-slug")
	before := len(prov.Appended())

	ctx := NewTestContext(u)
	if _, err := app.TransitionItemVia(ctx, "mcp", "Decision", "dec-c-slug", "pending-re-evaluation", ""); err != nil {
		t.Fatalf("TransitionItemVia: %v", err)
	}
	var rec *ProvenanceRecord
	for _, r := range prov.Appended()[before:] {
		if r.Verb == "transition" {
			r := r
			rec = &r
		}
	}
	if rec == nil {
		t.Fatal("no transition record written")
	}
	if rec.ActorKind != "agent" || rec.ActorID != u.ID {
		t.Errorf("record actor = %q/%q, want agent/%s", rec.ActorKind, rec.ActorID, u.ID)
	}
}

// signals.go reads Roles[0] as the actor's role: a classified token keeps the
// permission role there and carries the whole list as ActorRoles.
func TestClassifiedToken_SignalEventKeepsPermissionRole(t *testing.T) {
	ts := NewTokenStore(newClassTokensDB(t), testSecret)
	u, _ := mintUser(t, ts, "bot", "editor", Agent)
	ev := buildSignalEvent(NewTestContext(u), AfterCreate, afterHookMeta{}, &Node{ID: "n1", Slug: "s"}, "http://x")
	if ev.ActorRole != "editor" {
		t.Errorf("SignalEvent actor role = %q, want editor (the permission role, not the tag)", ev.ActorRole)
	}
	if actorKindFor(ev.ActorID, ev.ActorRoles) != "agent" {
		t.Errorf("ActorRoles = %v, want the agent tag to survive", ev.ActorRoles)
	}
}

// D104: the order when several tags are present is job, agent, human, so an
// automated actor never reads as a person; no tag is unclassified, not human.
func TestActorKindFor_Precedence(t *testing.T) {
	for _, tt := range []struct {
		name  string
		roles []Role
		want  string
	}{
		{"no tag", []Role{Editor}, "unclassified"},
		{"human tag", []Role{Editor, Human}, "human"},
		{"human and agent", []Role{Editor, Human, Agent}, "agent"},
		{"agent first in the list, human second", []Role{Editor, Agent, Human}, "agent"},
		{"human and job", []Role{Human, Editor, Job}, "job"},
		{"all three", []Role{Editor, Human, Agent, Job}, "job"},
		{"a bare tag", []Role{Human}, "human"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := actorKindFor("u1", tt.roles); got != tt.want {
				t.Errorf("actorKindFor(%v) = %q, want %q", tt.roles, got, tt.want)
			}
		})
	}
	if got := actorKindFor("", []Role{Editor, Human}); got != "" {
		t.Errorf("an empty actor id is unattributable whatever the tags: %q", got)
	}
}
