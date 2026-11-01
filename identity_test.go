package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// identityFixture is a database with the token table, the identity record
// (EnsureIdentities), governance and provenance, and a token store over it.
type identityFixture struct {
	db    *sql.DB
	app   *App
	store *TokenStore
}

func newIdentityFixture(t *testing.T) identityFixture {
	t.Helper()
	ctx := context.Background()
	db := newSQLiteDB(t)
	if _, err := db.Exec(`CREATE TABLE smeldr_tokens (id TEXT PRIMARY KEY, name TEXT NOT NULL, role TEXT NOT NULL,
		expires_at TEXT NOT NULL, revoked_at TEXT, created_at TEXT NOT NULL, user_id TEXT, actor_class TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := EnsureIdentities(ctx, db); err != nil {
		t.Fatalf("EnsureIdentities: %v", err)
	}
	if err := migrateGovernance(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := CreateProvenanceTable(db); err != nil {
		t.Fatal(err)
	}
	store := NewTokenStore(db, testSecret)
	app := New(Config{BaseURL: "https://example.com", DB: db, Secret: []byte(testSecret), TokenStore: store})
	return identityFixture{db: db, app: app, store: store}
}

// verify verifies raw against the fixture's store.
func (f identityFixture) verify(t *testing.T, raw string) (User, bool) {
	t.Helper()
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer "+raw)
	return VerifyBearerToken(r, []byte(testSecret), f.store)
}

func (f identityFixture) identity(t *testing.T, ref string) *Identity {
	t.Helper()
	ident, err := f.app.Identity(context.Background(), ref)
	if err != nil {
		t.Fatalf("Identity(%s): %v", ref, err)
	}
	return ident
}

func TestEnsureIdentities(t *testing.T) {
	f := newIdentityFixture(t)
	if err := EnsureIdentities(context.Background(), f.db); err != nil {
		t.Errorf("a second call: %v", err)
	}
	bare := newSQLiteDB(t)
	if err := EnsureIdentities(context.Background(), bare); err != nil {
		t.Errorf("no token table: %v", err)
	}
	if err := EnsureIdentities(context.Background(), nil); err != nil {
		t.Errorf("a nil database: %v", err)
	}
}

// A classified mint creates its identity: the class, the attester and, for an
// agent or job, the operator derived from the caller.
func TestIdentity_MintCreates(t *testing.T) {
	f := newIdentityFixture(t)
	human := as("h-1", Admin, Human)
	_, agentID, err := f.store.CreateClassifiedWithReason(human, "core-implementer", "editor", Agent, time.Hour, "an agent")
	if err != nil {
		t.Fatal(err)
	}
	agent := f.identity(t, agentID)
	if agent.Class != "agent" || agent.OperatorID != "h-1" || agent.AttestedBy != "h-1" || agent.Address != "IDN1" ||
		agent.Name != "core-implementer" || agent.Status != IdentityActive {
		t.Errorf("agent identity = %+v", agent)
	}
	// An agent's mint gets the agent's operator.
	_, jobID, err := f.store.CreateClassifiedWithReason(as(agentID, Agent), "relay", "editor", Job, time.Hour, "")
	if err != nil {
		t.Fatal(err)
	}
	if job := f.identity(t, "IDN2"); job.ID != jobID || job.OperatorID != "h-1" || job.AttestedBy != agentID {
		t.Errorf("job identity = %+v", job)
	}
	// A mechanism attests; no operator.
	_, otherID, err := f.store.CreateClassified(context.Background(), "importer", "editor", Job, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if other := f.identity(t, otherID); other.OperatorID != "" || other.AttestedBy != mintActorStore {
		t.Errorf("mechanism-minted identity = %+v", other)
	}
	// A human identity has no operator.
	_, personID, err := f.store.CreateClassified(human, "Ada", "editor", Human, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if person := f.identity(t, personID); person.OperatorID != "" || person.Class != "human" {
		t.Errorf("human identity = %+v", person)
	}
	// An unknown identity is not found.
	if _, err := f.app.Identity(context.Background(), "IDN99"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown identity: %v; want ErrNotFound", err)
	}
}

// A token minted for an identity joins it: signed with its id, its class, the
// identity's grants, its name in lookups, and its fingerprint as TokenID.
func TestIdentity_MintJoins(t *testing.T) {
	f := newIdentityFixture(t)
	ctx := as("h-1", Admin, Human)
	first, agentID, err := f.store.CreateClassified(ctx, "core-implementer", "editor", Agent, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.store.CreateForIdentity(ctx, "IDN1", "core-implementer-2026-11", "editor", time.Hour, "rotation")
	if err != nil {
		t.Fatalf("CreateForIdentity: %v", err)
	}
	for _, raw := range []string{first, second} {
		u, ok := f.verify(t, raw)
		if !ok || u.ID != agentID || !IsRole(u.Roles, Agent) || u.TokenID != tokenFingerprint(raw) {
			t.Errorf("verify: %+v, %v", u, ok)
		}
	}
	records, err := f.store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range records {
		if rec.IdentityID == nil || *rec.IdentityID != agentID || rec.ActorClass != "agent" {
			t.Errorf("token record %s: identity %v, class %q", rec.Name, rec.IdentityID, rec.ActorClass)
		}
	}
	names, err := f.store.NamesForUserIDs(context.Background(), []string{agentID})
	if err != nil || names[agentID] != "core-implementer" {
		t.Errorf("names = %v, %v; want the identity's name", names, err)
	}
	if err := f.app.Governance(NewRoleStore(f.db)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.governance.Grant(context.Background(), RoleGrant{TokenID: agentID, RoleName: "editor"}); err != nil {
		t.Fatalf("grant to the identity: %v", err)
	}
	if ok, err := f.app.governance.RoleGranted(context.Background(), agentID, "editor", AuthTarget{}); !ok || err != nil {
		t.Errorf("the identity's grant: %v, %v", ok, err)
	}
}

// What a join refuses.
func TestIdentity_JoinRefused(t *testing.T) {
	f := newIdentityFixture(t)
	human := as("h-1", Admin, Human)
	if _, _, err := f.store.CreateClassified(human, "bot", "editor", Agent, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.store.CreateClassified(human, "Ada", "editor", Human, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO smeldr_identities (id, address, name, class, status, created_at, updated_at)
		VALUES ('old', 'IDN50', 'old token', 'unclassified', 'active', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	var refusal *RefusalError
	for _, c := range []struct {
		name, ref, role string
		ctx             context.Context
		check           func(error) bool
	}{
		{"unknown", "IDN99", "editor", human, func(err error) bool { return errors.Is(err, ErrNotFound) }},
		{"unclassified", "IDN50", "editor", human, func(err error) bool { return errors.As(err, &refusal) }},
		{"bad role", "IDN1", "owner", human, func(err error) bool { var ve *ValidationError; return errors.As(err, &ve) }},
		{"an agent joins a human", "IDN2", "editor", as("a-9", Agent), func(err error) bool { return errors.As(err, &refusal) }},
		{"a job mints", "IDN1", "editor", as("j-9", Job), func(err error) bool { return errors.As(err, &refusal) }},
	} {
		if _, err := f.store.CreateForIdentity(c.ctx, c.ref, "t", c.role, time.Hour, ""); !c.check(err) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	if _, err := f.store.CreateForIdentity(human, "IDN1", "t", "editor", time.Hour, string(make([]byte, 1001))); err == nil {
		t.Error("an over-long reason: want an error")
	}
	for _, status := range []string{IdentitySuspended, IdentityRetired} {
		if _, err := f.db.Exec(`UPDATE smeldr_identities SET status = $1 WHERE address = 'IDN1'`, status); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.CreateForIdentity(human, "IDN1", "t", "editor", time.Hour, ""); !errors.As(err, &refusal) {
			t.Errorf("%s identity: %v; want a refusal", status, err)
		}
	}
}

// Verification takes the class from the identity and refuses a token whose
// identity is not active.
func TestIdentity_ClassAndStatusOnVerify(t *testing.T) {
	f := newIdentityFixture(t)
	raw, id, err := f.store.CreateClassified(as("h-1", Admin, Human), "bot", "editor", Agent, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	set := func(col, val string) {
		t.Helper()
		if _, err := f.db.Exec(`UPDATE smeldr_identities SET `+col+` = $1 WHERE id = $2`, val, id); err != nil {
			t.Fatal(err)
		}
	}
	set("class", "job")
	if u, ok := f.verify(t, raw); !ok || !IsRole(u.Roles, Job) || IsRole(u.Roles, Agent) || !IsRole(u.Roles, Editor) {
		t.Errorf("the identity's class must rule: %+v, %v", u.Roles, ok)
	}
	set("class", IdentityUnclassified)
	if u, ok := f.verify(t, raw); !ok || IsRole(u.Roles, Agent) || IsRole(u.Roles, Job) {
		t.Errorf("an unclassified identity carries no class: %+v, %v", u.Roles, ok)
	}
	if gone, err := f.store.IsRevoked(context.Background(), tokenFingerprint(raw)); gone || err != nil {
		t.Errorf("IsRevoked of an active identity's token = %v, %v", gone, err)
	}
	for _, status := range []string{IdentitySuspended, IdentityRetired} {
		set("status", status)
		if _, ok := f.verify(t, raw); ok {
			t.Errorf("a %s identity's token must not verify", status)
		}
		if _, ok := VerifyTokenString(raw, []byte(testSecret), f.store); ok {
			t.Errorf("VerifyTokenString: a %s identity's token must not verify", status)
		}
		if gone, err := f.store.IsRevoked(context.Background(), tokenFingerprint(raw)); !gone || err != nil {
			t.Errorf("IsRevoked of a %s identity's token = %v, %v", status, gone, err)
		}
	}
	if gone, err := f.store.IsRevoked(context.Background(), "no-such-token"); gone || err != nil {
		t.Errorf("IsRevoked of an unknown token = %v, %v", gone, err)
	}
}

// Without the identities table everything works as before identities.
func TestIdentity_VerifyFallback(t *testing.T) {
	db := newSQLiteDB(t)
	if _, err := db.Exec(`CREATE TABLE smeldr_tokens (id TEXT PRIMARY KEY, name TEXT NOT NULL, role TEXT NOT NULL,
		expires_at TEXT NOT NULL, revoked_at TEXT, created_at TEXT NOT NULL, user_id TEXT, actor_class TEXT)`); err != nil {
		t.Fatal(err)
	}
	store := NewTokenStore(db, testSecret)
	raw, id, err := store.CreateClassified(context.Background(), "bot", "editor", Agent, time.Hour)
	if err != nil {
		t.Fatalf("a classified mint without identities: %v", err)
	}
	for i := 0; i < 2; i++ { // the second call reads revocation alone
		u, ok := VerifyTokenString(raw, []byte(testSecret), store)
		if !ok || u.ID != id || !IsRole(u.Roles, Agent) {
			t.Errorf("verify without identities: %+v, %v", u, ok)
		}
	}
	if !store.noIdentities.Load() {
		t.Error("the store must remember that it has no identities table")
	}
	records, err := store.List(context.Background())
	if err != nil || len(records) != 1 || records[0].IdentityID != nil || records[0].ActorClass != "agent" {
		t.Errorf("List without identities: %+v, %v", records, err)
	}
	names, err := store.NamesForUserIDs(context.Background(), []string{id})
	if err != nil || names[id] != "bot" {
		t.Errorf("names without identities: %v, %v", names, err)
	}
	if _, err := store.CreateForIdentity(context.Background(), "IDN1", "t", "editor", time.Hour, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("join without identities: %v; want ErrNotFound", err)
	}
}

// An unclassified mint makes no identity (phase 1, D165).
func TestIdentity_UnclassifiedMint(t *testing.T) {
	f := newIdentityFixture(t)
	raw, id, err := f.store.CreateWithID(context.Background(), "plain", "editor", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.Identity(context.Background(), id); !errors.Is(err, ErrNotFound) {
		t.Errorf("an unclassified mint made an identity: %v", err)
	}
	if u, ok := f.verify(t, raw); !ok || u.ID != id {
		t.Errorf("an unclassified token must still verify: %+v, %v", u, ok)
	}
}

// A grant is held by an identity: a grantee that is neither an identity nor a
// token's user_id is refused once identities exist.
func TestIdentity_GrantNeedsIdentity(t *testing.T) {
	f := newIdentityFixture(t)
	rs := NewRoleStore(f.db)
	_, agentID, err := f.store.CreateClassified(as("h-1", Admin, Human), "bot", "editor", Agent, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, plainID, err := f.store.CreateWithID(context.Background(), "plain", "editor", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, id := range []string{agentID, plainID} {
		if _, err := rs.Grant(ctx, RoleGrant{TokenID: id, RoleName: "editor"}); err != nil {
			t.Errorf("grant to %s: %v", id, err)
		}
	}
	var ve *ValidationError
	if _, err := rs.Grant(ctx, RoleGrant{TokenID: "nobody", RoleName: "editor"}); !errors.As(err, &ve) {
		t.Errorf("grant to no identity: %v; want a ValidationError", err)
	}
	// Without identities the grantee is not checked, as before.
	bare := newSQLiteDB(t)
	if err := migrateGovernance(ctx, bare); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRoleStore(bare).Grant(ctx, RoleGrant{TokenID: "nobody", RoleName: "editor"}); err != nil {
		t.Errorf("grant without identities: %v", err)
	}
	// Inside a transaction the probe does not run.
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := checkGrantee(ctx, tx, "nobody"); err != nil {
		t.Errorf("checkGrantee in a transaction: %v", err)
	}
}

// Provenance names the identity and records the token as the means.
func TestIdentity_ProvenanceMeans(t *testing.T) {
	f := newIdentityFixture(t)
	ps := NewProvenanceStore(f.db)
	ctx := NewTestContext(User{ID: "ident-1", Roles: []Role{Editor, Agent}, TokenID: "fp-1"})
	for _, r := range []ProvenanceRecord{
		{ID: "p1", Timestamp: time.Now(), SubjectType: "Task", SubjectID: "t1", Verb: "create", ActorKind: "agent", ActorID: "ident-1"},
		{ID: "p2", Timestamp: time.Now(), SubjectType: "Task", SubjectID: "t1", Verb: "update", ActorKind: "job", ActorID: "sweeper"},
	} {
		if err := ps.Append(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	recs, err := ps.List(context.Background(), ProvenanceFilter{SubjectID: "t1"})
	if err != nil {
		t.Fatal(err)
	}
	means := map[string]string{}
	for _, r := range recs {
		means[r.ID] = r.Means
	}
	if means["p1"] != "fp-1" || means["p2"] != "" {
		t.Errorf("means = %v; want the request's token on its own act only", means)
	}
	// A table from before the means column.
	old := newSQLiteDB(t)
	if _, err := old.Exec(`CREATE TABLE smeldr_provenance (id TEXT PRIMARY KEY, timestamp TIMESTAMPTZ NOT NULL, subject_type TEXT NOT NULL,
		subject_id TEXT NOT NULL, verb TEXT NOT NULL, from_state TEXT NOT NULL, to_state TEXT NOT NULL, actor_kind TEXT NOT NULL,
		actor_id TEXT NOT NULL, surface TEXT NOT NULL, reason TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	ops := NewProvenanceStore(old)
	if err := ops.Append(ctx, ProvenanceRecord{ID: "p3", Timestamp: time.Now(), SubjectType: "Task", SubjectID: "t2", Verb: "create", ActorID: "ident-1"}); err != nil {
		t.Fatalf("append without the means column: %v", err)
	}
	if recs, err := ops.List(context.Background(), ProvenanceFilter{SubjectID: "t2"}); err != nil || len(recs) != 1 || recs[0].Means != "" {
		t.Errorf("list without the means column: %+v, %v", recs, err)
	}
}

// The readers that assumed one token per user_id read the identity.
func TestIdentity_ClassAndNameReaders(t *testing.T) {
	f := newIdentityFixture(t)
	_, id, err := f.store.CreateClassified(as("h-1", Admin, Human), "bot", "editor", Agent, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateForIdentity(as("h-1", Admin, Human), id, "bot-2", "editor", time.Hour, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE smeldr_identities SET name = 'Bot', class = 'job' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if class, err := actorClass(context.Background(), f.db, id); class != "job" || err != nil {
		t.Errorf("actorClass = %q, %v; want the identity's", class, err)
	}
	if _, err := f.db.Exec(`UPDATE smeldr_identities SET class = 'unclassified' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if class, err := actorClass(context.Background(), f.db, id); class != "" || err != nil {
		t.Errorf("actorClass of an unclassified identity = %q, %v", class, err)
	}
	live, err := f.app.liveTokens(context.Background(), []string{id})
	if err != nil || live[id].name != "Bot" || live[id].class != IdentityUnclassified {
		t.Errorf("liveTokens = %+v, %v; want the identity's name and class", live, err)
	}
	if got, err := identitiesOf(context.Background(), f.db, nil); err != nil || len(got) != 0 {
		t.Errorf("identitiesOf(nil) = %v, %v", got, err)
	}
}

// The contact is shown to administrators only.
func TestIdentity_ContactForAdministrators(t *testing.T) {
	f := newIdentityFixture(t)
	_, id, err := f.store.CreateClassified(context.Background(), "Ada", "editor", Human, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE smeldr_identities SET contact = 'ada@example.com' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if got := f.identity(t, id); got.Contact != "ada@example.com" {
		t.Errorf("in-process read: contact %q", got.Contact)
	}
	for _, c := range []struct {
		ctx  context.Context
		want string
	}{
		{NewTestContext(User{ID: "x", Roles: []Role{Editor}}), ""},
		{NewTestContext(User{ID: "x", Roles: []Role{Admin}}), "ada@example.com"},
	} {
		got, err := f.app.Identity(c.ctx, id)
		if err != nil || got.Contact != c.want {
			t.Errorf("contact = %q, %v; want %q", got.Contact, err, c.want)
		}
	}
	if err := f.app.Governance(NewRoleStore(f.db)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.governance.Grant(context.Background(), RoleGrant{TokenID: id, RoleName: "admin"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.app.Identity(NewTestContext(User{ID: id, Roles: []Role{Editor}}), id); got.Contact == "" {
		t.Error("an admin by grant must see the contact")
	}
	if _, err := New(Config{BaseURL: "https://example.com", Secret: []byte(testSecret)}).Identity(context.Background(), id); err == nil {
		t.Error("Identity without a database: want an error")
	}
}

// The migration: one identity per user_id, same id, dry run first, idempotent.
func TestMigrateIdentities(t *testing.T) {
	f := newIdentityFixture(t)
	ctx := context.Background()
	for _, tok := range []struct{ fp, name, user, class, created string }{
		{"fp-a1", "peter-old", "u-peter", "human", "2026-01-01T00:00:00Z"},
		{"fp-a2", "peter", "u-peter", "agent", "2026-02-01T00:00:00Z"},
		{"fp-b", "legacy", "u-legacy", "", "2026-01-15T00:00:00Z"},
		{"fp-c", "relay", "u-relay", "job", "2026-03-01T00:00:00Z"},
	} {
		if _, err := f.db.Exec(`INSERT INTO smeldr_tokens (id, name, role, expires_at, created_at, user_id, actor_class) VALUES ($1, $2, 'editor', '2099-01-01T00:00:00Z', $3, $4, $5)`,
			tok.fp, tok.name, tok.created, tok.user, tok.class); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.db.Exec(`INSERT INTO smeldr_tokens (id, name, role, expires_at, created_at) VALUES ('fp-nouser-000000', 'ancient', 'admin', '2099-01-01T00:00:00Z', '2025-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO smeldr_role_grants (id, token_id, role_id, created_at) SELECT 'g1', 'ghost', id, datetime('now') FROM smeldr_roles WHERE name = 'editor'`); err != nil {
		t.Fatal(err)
	}
	if err := NewProvenanceStore(f.db).Append(ctx, ProvenanceRecord{ID: "pv1", Timestamp: time.Now(), SubjectType: "Token", SubjectID: "fp-a1", Verb: "assert", ActorKind: "job", ActorID: "minter"}); err != nil {
		t.Fatal(err)
	}

	dry, err := f.app.MigrateIdentities(ctx, IdentityMigrationOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !dry.DryRun || len(dry.Created) != 3 || dry.Created[0].ID != "u-peter" || dry.Created[0].Address != "IDN1" ||
		dry.Created[0].Class != "agent" || dry.Created[0].Name != "peter" || dry.Created[0].Tokens != 2 {
		t.Fatalf("dry run = %+v", dry)
	}
	if !slices.Equal(dry.Unclassified, []string{"IDN2"}) || !slices.Equal(dry.OperatorNotSet, []string{"IDN1", "IDN3"}) ||
		!slices.Equal(dry.ClassDisagreements, []string{"IDN1"}) || len(dry.TokensWithoutUserID) != 1 ||
		!slices.Equal(dry.OrphanGrantees, []string{"ghost"}) || dry.TokensLinked != 4 {
		t.Errorf("dry run lists = %+v", dry)
	}
	var n int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_identities`).Scan(&n)
	if n != 0 {
		t.Fatalf("a dry run wrote %d identities", n)
	}

	rep, err := f.app.MigrateIdentities(ctx, IdentityMigrationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.DryRun || len(rep.Created) != 3 || rep.Created[2].Address != "IDN3" {
		t.Errorf("apply = %+v", rep)
	}
	peter := f.identity(t, "u-peter")
	if peter.Address != "IDN1" || peter.AttestedBy != "minter" || peter.Class != "agent" {
		t.Errorf("migrated identity = %+v", peter)
	}
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_tokens WHERE identity_id = user_id`).Scan(&n)
	if n != 4 {
		t.Errorf("%d tokens linked; want 4", n)
	}
	again, err := f.app.MigrateIdentities(ctx, IdentityMigrationOptions{})
	if err != nil || len(again.Created) != 0 || again.TokensLinked != 0 {
		t.Errorf("a second run = %+v, %v; want nothing", again, err)
	}

	// Without the identities table it says what to run.
	bare := newSQLiteDB(t)
	if _, err := New(Config{BaseURL: "https://example.com", Secret: []byte(testSecret), DB: bare}).MigrateIdentities(ctx, IdentityMigrationOptions{}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("no identities table: %v; want ErrBadRequest", err)
	}
	if _, err := New(Config{BaseURL: "https://example.com", Secret: []byte(testSecret)}).MigrateIdentities(ctx, IdentityMigrationOptions{}); err == nil {
		t.Error("no database: want an error")
	}
}

// Verification reads the token and its identity in one query (D165).
func BenchmarkVerifyTokenString_Identity(b *testing.B) {
	for _, withIdentities := range []bool{false, true} {
		name := "without identities"
		if withIdentities {
			name = "with identities"
		}
		b.Run(name, func(b *testing.B) {
			db := newSQLiteDB(b)
			if _, err := db.Exec(`CREATE TABLE smeldr_tokens (id TEXT PRIMARY KEY, name TEXT NOT NULL, role TEXT NOT NULL,
				expires_at TEXT NOT NULL, revoked_at TEXT, created_at TEXT NOT NULL, user_id TEXT, actor_class TEXT)`); err != nil {
				b.Fatal(err)
			}
			if withIdentities {
				if err := EnsureIdentities(context.Background(), db); err != nil {
					b.Fatal(err)
				}
			}
			store := NewTokenStore(db, testSecret)
			raw, _, err := store.CreateClassified(context.Background(), "bot", "editor", Agent, time.Hour)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, ok := VerifyTokenString(raw, []byte(testSecret), store); !ok {
					b.Fatal("verify")
				}
			}
		})
	}
}

// identityFaultDB fails every statement whose text contains match, with an
// error that is not "no such table", and passes the rest through.
type identityFaultDB struct {
	DB
	match string
}

var errIdentityFault = errors.New("fault injected")

func (d identityFaultDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	if strings.Contains(q, d.match) {
		return nil, errIdentityFault
	}
	return d.DB.ExecContext(ctx, q, args...)
}

func (d identityFaultDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if strings.Contains(q, d.match) {
		return nil, errIdentityFault
	}
	return d.DB.QueryContext(ctx, q, args...)
}

func (d identityFaultDB) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	if strings.Contains(q, d.match) {
		return d.DB.QueryRowContext(ctx, "SELECT fault_injected(") // a syntax error, not a missing table
	}
	return d.DB.QueryRowContext(ctx, q, args...)
}

// Every database failure on the identity paths is an error, never a silent
// "no identity".
func TestIdentity_DatabaseFailures(t *testing.T) {
	ctx := context.Background()
	t.Run("migration", func(t *testing.T) {
		for _, match := range []string{
			`"smeldr_identities" WHERE 1=0`,
			"ORDER BY created_at DESC, id DESC",
			"SELECT id FROM smeldr_identities",
			`"smeldr_provenance" WHERE 1=0`,
			"WHERE type_name = 'Identity'",
			"verb = 'assert'",
			`"smeldr_role_grants" WHERE 1=0`,
			"SELECT DISTINCT g.token_id",
			"INSERT INTO smeldr_identities",
			"UPDATE smeldr_tokens SET identity_id",
			"UPDATE smeldr_address_counters",
		} {
			f := newIdentityFixture(t)
			if _, err := f.db.Exec(`INSERT INTO smeldr_tokens (id, name, role, expires_at, created_at, user_id, actor_class) VALUES ('fp-1', 'bot', 'editor', '2099-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 'u-1', 'agent')`); err != nil {
				t.Fatal(err)
			}
			if err := NewProvenanceStore(f.db).Append(ctx, ProvenanceRecord{ID: "pv", Timestamp: time.Now(), SubjectType: "Token", SubjectID: "fp-1", Verb: "assert", ActorID: "m"}); err != nil {
				t.Fatal(err)
			}
			app := New(Config{BaseURL: "https://example.com", Secret: []byte(testSecret), DB: identityFaultDB{f.db, match}})
			if _, err := app.MigrateIdentities(ctx, IdentityMigrationOptions{}); err == nil {
				t.Errorf("%s: want an error", match)
			}
		}
	})
	t.Run("mint", func(t *testing.T) {
		for _, match := range []string{
			"FROM smeldr_identities WHERE 1 = 0",
			"WHERE id = $1 OR address = $1",
			"INSERT INTO smeldr_identities",
			"INSERT INTO smeldr_tokens",
			"UPDATE smeldr_address_counters",
		} {
			f := newIdentityFixture(t)
			store := NewTokenStore(identityFaultDB{f.db, match}, testSecret)
			if _, _, err := store.CreateClassified(as("a-1", Agent), "relay", "editor", Job, time.Hour); err == nil {
				t.Errorf("%s: want an error", match)
			}
		}
		f := newIdentityFixture(t)
		if _, err := NewTokenStore(identityFaultDB{f.db, "WHERE id = $1 OR address = $1"}, testSecret).
			CreateForIdentity(ctx, "IDN1", "t", "editor", time.Hour, ""); err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("a failed identity read on join: %v; want an internal error", err)
		}
		if _, err := New(Config{BaseURL: "https://example.com", Secret: []byte(testSecret), DB: identityFaultDB{f.db, "WHERE id = $1 OR address = $1"}}).
			Identity(ctx, "IDN1"); err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("a failed identity read: %v; want an internal error", err)
		}
	})
	t.Run("verify", func(t *testing.T) {
		f := newIdentityFixture(t)
		raw, _, err := f.store.CreateClassified(ctx, "bot", "editor", Agent, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := VerifyTokenString(raw, []byte(testSecret), NewTokenStore(identityFaultDB{f.db, "LEFT JOIN smeldr_identities i ON i.id = t.identity_id WHERE"}, testSecret)); ok {
			t.Error("a failed state read must not verify")
		}
		bare := NewTokenStore(identityFaultDB{f.db, "SELECT revoked_at FROM smeldr_tokens WHERE id"}, testSecret)
		bare.noIdentities.Store(true)
		if _, ok := VerifyTokenString(raw, []byte(testSecret), bare); ok {
			t.Error("a failed revocation read must not verify")
		}
		if _, err := bare.IsRevoked(ctx, tokenFingerprint(raw)); err == nil {
			t.Error("IsRevoked on a failed read: want an error")
		}
	})
	t.Run("readers", func(t *testing.T) {
		f := newIdentityFixture(t)
		if _, err := identitiesOf(ctx, identityFaultDB{f.db, "WHERE id IN ("}, []string{"a"}); err == nil {
			t.Error("identitiesOf: want an error")
		}
		if _, err := identitiesOf(ctx, identityFaultDB{f.db, `"smeldr_identities" WHERE 1=0`}, []string{"a"}); err == nil {
			t.Error("identitiesOf probe: want an error")
		}
		for _, match := range []string{`"smeldr_identities" WHERE 1=0`, "FROM smeldr_identities WHERE id = $1", "FROM smeldr_tokens WHERE user_id = $1"} {
			if err := checkGrantee(ctx, identityFaultDB{f.db, match}, "x"); err == nil {
				t.Errorf("checkGrantee %s: want an error", match)
			}
		}
		if _, err := NewTokenStore(identityFaultDB{f.db, "t.identity_id, COALESCE(i.class"}, testSecret).List(ctx); err == nil {
			t.Error("List: want an error")
		}
	})
	t.Run("ensure", func(t *testing.T) {
		for _, match := range []string{
			"CREATE TABLE IF NOT EXISTS smeldr_identities",
			"smeldr_address_counters",
			`ALTER TABLE "smeldr_tokens"`,
			"CREATE INDEX IF NOT EXISTS idx_smeldr_tokens_identity",
		} {
			db := newSQLiteDB(t)
			if _, err := db.Exec(`CREATE TABLE smeldr_tokens (id TEXT PRIMARY KEY, name TEXT NOT NULL, role TEXT NOT NULL,
				expires_at TEXT NOT NULL, revoked_at TEXT, created_at TEXT NOT NULL, user_id TEXT, actor_class TEXT)`); err != nil {
				t.Fatal(err)
			}
			if err := EnsureIdentities(ctx, identityFaultDB{db, match}); err == nil {
				t.Errorf("%s: want an error", match)
			}
		}
	})
}

// A classified mint on a token table without the columns it needs is refused
// with a message naming the call that adds them.
func TestIdentity_MintNeedsColumns(t *testing.T) {
	for _, c := range []struct{ name, ddl, field string }{
		{"no identity_id", `CREATE TABLE smeldr_tokens (id TEXT PRIMARY KEY, name TEXT NOT NULL, role TEXT NOT NULL,
			expires_at TEXT NOT NULL, revoked_at TEXT, created_at TEXT NOT NULL, user_id TEXT, actor_class TEXT)`, "identity"},
		{"no actor_class", `CREATE TABLE smeldr_tokens (id TEXT PRIMARY KEY, name TEXT NOT NULL, role TEXT NOT NULL,
			expires_at TEXT NOT NULL, revoked_at TEXT, created_at TEXT NOT NULL, user_id TEXT)`, "actor_class"},
	} {
		db := newSQLiteDB(t)
		// The identities first, the token table after: identity_id is never added.
		if err := EnsureIdentities(context.Background(), db); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(c.ddl); err != nil {
			t.Fatal(err)
		}
		_, _, err := NewTokenStore(db, testSecret).CreateClassified(context.Background(), "bot", "editor", Agent, time.Hour)
		var ve *ValidationError
		if !errors.As(err, &ve) || !strings.Contains(err.Error(), c.field) {
			t.Errorf("%s: %v; want a ValidationError on %s", c.name, err, c.field)
		}
	}
}

// A provenance read that fails is an error, with or without the means column;
// a token of an unclassified identity lists with no class.
func TestIdentity_ReadFailuresAndUnclassifiedList(t *testing.T) {
	f := newIdentityFixture(t)
	ctx := context.Background()
	withMeans := NewProvenanceStore(identityFaultDB{f.db, "surface, reason, means"})
	if _, err := withMeans.List(ctx, ProvenanceFilter{}); err == nil {
		t.Error("a failed read with the means column: want an error")
	}
	bare := &sqlProvenanceStore{db: identityFaultDB{f.db, "FROM smeldr_provenance WHERE 1=1"}}
	bare.noMeans.Store(true)
	if _, err := bare.List(ctx, ProvenanceFilter{}); err == nil {
		t.Error("a failed read without the means column: want an error")
	}
	if _, err := f.db.Exec(`INSERT INTO smeldr_tokens (id, name, role, expires_at, created_at, user_id) VALUES ('fp-u', 'legacy', 'editor', '2099-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 'u-legacy')`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.MigrateIdentities(ctx, IdentityMigrationOptions{}); err != nil {
		t.Fatal(err)
	}
	recs, err := f.store.List(ctx)
	if err != nil || len(recs) != 1 || recs[0].ActorClass != "" || recs[0].IdentityID == nil {
		t.Errorf("an unclassified identity's token = %+v, %v", recs, err)
	}
}
