package smeldr

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"testing"
	"time"
)

// T1061 phase 2: each refusal type of the approval, governance, identity,
// auth, capability and administrators sites, driven through a real site.

// phaseTwoACase is one refusal: the error a real call returns, its type and
// values, and the old behaviour (the sentinel or the field, and the message).
type phaseTwoACase struct {
	name     string
	err      error
	typ      ProblemType
	values   map[string]string
	sentinel error  // nil for a ValidationError
	field    string // the field of a ValidationError
	msg      string // the exact Error() text
	prefix   string // a stable prefix of Error(), when msg embeds a Go error
}

// wantTypedA checks err's refusal type and exactly its values.
func wantTypedA(t *testing.T, err error, typ ProblemType, values map[string]string) {
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

// runPhaseTwoA checks each case: type, values, sentinel or field, message.
func runPhaseTwoA(t *testing.T, cases []phaseTwoACase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantTypedA(t, c.err, c.typ, c.values)
			if c.sentinel != nil {
				if !errors.Is(c.err, c.sentinel) {
					t.Errorf("errors.Is(%v, %v) = false", c.err, c.sentinel)
				}
			} else {
				var ve *ValidationError
				if !errors.As(c.err, &ve) {
					t.Fatalf("err = %v; want a ValidationError", c.err)
				}
				if f := ve.Fields(); len(f) != 1 || f[0].Field != c.field {
					t.Errorf("fields = %+v; want one on %q", f, c.field)
				}
			}
			switch {
			case c.prefix != "":
				if !strings.HasPrefix(c.err.Error(), c.prefix) {
					t.Errorf("Error() = %q; want prefix %q", c.err.Error(), c.prefix)
				}
			case c.err.Error() != c.msg:
				t.Errorf("Error() = %q\nwant      %q", c.err.Error(), c.msg)
			}
		})
	}
}

// The agent rule's refusals, the role and grant lookups and GrantFounder.
func TestPhaseTwoA_ApprovalAndGovernance(t *testing.T) {
	f := newApprovalFixture(t)
	ctx := context.Background()
	human := as("human-1", Human)
	if _, err := f.rs.Grant(ctx, RoleGrant{TokenID: "human-1", RoleName: RoleDomainApprover, ScopeAnchorID: f.domain}); err != nil {
		t.Fatal(err)
	}
	grant := func(ctx context.Context, g RoleGrant) error {
		_, err := f.rs.Grant(ctx, g)
		return err
	}
	_, roleErr := f.rs.GetRole(ctx, "nope")
	_, grantErr := f.rs.GetGrant(ctx, "no-such-grant")
	forbidden := ErrForbidden.Error()
	cases := []phaseTwoACase{
		{name: "agent grants an approval role",
			err: grant(as("agent-1", Agent), RoleGrant{TokenID: "human-2", RoleName: RoleApprover}),
			typ: problemGrantAgentGrantsApprovalRole, values: map[string]string{"role": RoleApprover, "operation": OperationRatify},
			sentinel: ErrForbidden,
			msg:      `Grant: forbidden: an agent token never grants a role that ratifies or approves changes (D142): role "approver" holds ratify`},
		{name: "automated holds ratify",
			err: grant(human, RoleGrant{TokenID: "agent-1", RoleName: RoleApprover}),
			typ: problemGrantAutomatedHoldsRatify, values: map[string]string{"token_class": "agent", "role": RoleApprover},
			sentinel: ErrForbidden,
			msg:      `Grant: forbidden: an agent token never holds ratify (D142): role "approver" holds it`},
		{name: "automated approve-change not on an Area",
			err:      grant(human, RoleGrant{TokenID: "agent-1", RoleName: RoleAreaApprover}),
			typ:      problemGrantAutomatedApproveChangeNotArea,
			values:   map[string]string{"token_class": "agent", "role": RoleAreaApprover, "suggested_role": RoleAreaApprover},
			sentinel: ErrForbidden,
			msg:      `Grant: forbidden: an agent token holds approve-change only within one Area: role "area-approver" is not an Area role anchored on an Area (use area-approver)`},
		{name: "automated needs a human grantor",
			err:      grant(as("plain-1"), RoleGrant{TokenID: "agent-1", RoleName: RoleAreaApprover, ScopeAnchorID: f.area}),
			typ:      problemGrantAutomatedNeedsHumanGrantor,
			values:   map[string]string{"token_class": "agent", "role": RoleAreaApprover, "grantor_class": "unclassified"},
			sentinel: ErrForbidden,
			msg:      "Grant: forbidden: an agent token's area-approver grant must come from a human token; the grantor is unclassified"},
		{name: "grantor lacks Area authority",
			err: grant(as("human-2", Human), RoleGrant{TokenID: "agent-1", RoleName: RoleAreaApprover, ScopeAnchorID: f.area}),
			typ: problemGrantGrantorLacksAreaAuthority, values: map[string]string{"area": f.area, "token_class": "agent"},
			sentinel: ErrForbidden,
			msg:      "Grant: forbidden: the grantor holds no approve-change over Area " + f.area + ", so cannot delegate it to an agent token"},
		{name: "job never grants",
			err: grant(as("job-1", Job), RoleGrant{TokenID: "human-2", RoleName: "editor"}),
			typ: problemGrantJobNeverGrants, values: map[string]string{"role": "editor"},
			sentinel: ErrForbidden,
			msg:      `Grant: forbidden: a job token never grants a role (D156): role "editor"`},
		{name: "grant of an unknown role",
			err: grant(human, RoleGrant{TokenID: "human-2", RoleName: "nope"}),
			typ: problemRoleNotFound, values: map[string]string{"role": "nope"},
			sentinel: ErrNotFound,
			msg:      fmt.Sprintf("smeldr: Grant: role %q: %v", "nope", ErrNotFound)},
		{name: "GetRole of an unknown role",
			err: roleErr, typ: problemRoleNotFound, values: map[string]string{"role": "nope"},
			sentinel: ErrNotFound,
			msg:      fmt.Sprintf("smeldr: GetRole: role %q: %v", "nope", ErrNotFound)},
		{name: "GetGrant of an unknown grant",
			err: grantErr, typ: problemGrantNotFound, values: map[string]string{"grant": "no-such-grant"},
			sentinel: ErrNotFound,
			msg:      fmt.Sprintf("smeldr: GetGrant: grant %q: %v", "no-such-grant", ErrNotFound)},
		{name: "GrantFounder without a user",
			err: f.app.GrantFounder(human, ""), typ: problemFieldRequired, values: nil,
			field: "user_id", msg: "validation failed: user_id: is required: the founder's user ID"},
	}
	runPhaseTwoA(t, cases)

	// A grantee class that cannot be read refuses, fail closed (an integer
	// overflow in SQLite, as TestGrant_AgentRuleClassErrors provokes).
	if _, err := f.db.Exec(`DROP TABLE smeldr_tokens`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`CREATE VIEW smeldr_tokens AS SELECT 'agent-1' AS user_id, 'agent' AS actor_class UNION ALL SELECT 'x', abs(-9223372036854775808)`); err != nil {
		t.Fatal(err)
	}
	runPhaseTwoA(t, []phaseTwoACase{{name: "grantee class unreadable",
		err: grant(ctx, RoleGrant{TokenID: "x", RoleName: RoleApprover}),
		typ: problemGrantAuthorityUnreadable, values: map[string]string{"grantee": "x"},
		sentinel: ErrForbidden,
		prefix:   forbidden + ": Grant: the class of x cannot be read: "}})
}

// An agent token is refused a ratify transition, whatever its grants.
func TestPhaseTwoA_RatifyAutomatedClass(t *testing.T) {
	app, db, rs := setupTransitionItemApp(t)
	insertDecision(t, db, "dec-a", "dec-a-slug", "proposed")
	placeDecision(t, db, "dec-a")
	if err := RegisterApproverRole(context.Background(), rs); err != nil {
		t.Fatal(err)
	}
	_, err := app.TransitionItemVia(TrustedRead(as("tok", Agent)), "mcp", "Decision", "dec-a-slug", "ratified", "")
	wantTypedA(t, err, problemRatifyAutomatedClass, map[string]string{"token_class": "agent"})
	var re *RefusalError
	if !errors.Is(err, ErrForbidden) || !errors.As(err, &re) ||
		re.Reason != "an agent token never ratifies, supersedes or amends a Decision (D142); a human approver does" {
		t.Errorf("err = %v; want the D142 refusal", err)
	}
	if !strings.Contains(err.Error(), "forbidden: "+re.Reason) {
		t.Errorf("Error() = %q; want it to carry the refusal", err.Error())
	}
}

// A static scope pattern that is malformed, names nothing, or names an
// unregistered type is a field refusal on scope_static.
func TestPhaseTwoA_StaticScope(t *testing.T) {
	ctx := context.Background()
	db := addressDB(t)
	setupTokensTable(t, db)
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	RegisterOrchestrationTypes(app, db)
	rs := NewRoleStore(db)
	if err := app.Governance(rs); err != nil {
		t.Fatal(err)
	}
	_ = app.Handler()
	if err := RegisterScopedReaderRole(ctx, rs); err != nil {
		t.Fatal(err)
	}
	grant := func(p string) error {
		_, err := rs.Grant(ctx, RoleGrant{TokenID: "tok-bad", RoleName: "scoped-reader", ScopeStatic: []string{p}})
		return err
	}
	runPhaseTwoA(t, []phaseTwoACase{
		{name: "malformed", err: grant("Goal"), typ: problemFieldInvalidFormat, values: map[string]string{"value": "Goal"},
			field: "scope_static", msg: `validation failed: scope_static: pattern "Goal" must be "type:item" or "type:*"`},
		{name: "names nothing", err: grant("Goal:G99"), typ: problemReferenceNotFound,
			values: map[string]string{"value": "Goal:G99", "type": "Goal"},
			field:  "scope_static", msg: `validation failed: scope_static: pattern "Goal:G99" names no Goal record: name it by its address, slug or ID`},
		{name: "unregistered type", err: grant("Nope:x"), typ: problemFieldNotInList, values: map[string]string{"value": "Nope:x"},
			field: "scope_static", msg: `validation failed: scope_static: pattern "Nope:x" names a type that is not registered`},
	})
}

// Identity reads and joins, token mints, and grants to what is no identity.
func TestPhaseTwoA_IdentityAndTokens(t *testing.T) {
	f := newIdentityFixture(t)
	ctx := context.Background()
	human := as("h-1", Admin, Human)
	if _, _, err := f.store.CreateClassified(human, "Ada", "editor", Human, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO smeldr_identities (id, address, name, class, status, created_at, updated_at)
		VALUES ('old', 'IDN50', 'old token', 'unclassified', 'active', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	_, plainID, err := f.store.CreateWithID(ctx, "plain", "editor", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	rs := NewRoleStore(f.db)
	join := func(ref, role string) error {
		_, err := f.store.CreateForIdentity(human, ref, "t", role, time.Hour, "")
		return err
	}
	mint := func(ctx context.Context, role string, class Role) error {
		_, _, err := f.store.CreateClassifiedWithReason(ctx, "t", role, class, time.Hour, "")
		return err
	}
	_, identErr := f.app.Identity(ctx, "IDN99")
	unclassifiedErr := join("IDN50", "editor")
	badRoleErr := join("IDN1", "owner")
	unknownErr := join("IDN99", "editor")
	rev := 0
	_, _, revErr := rs.GrantAt(ctx, RoleGrant{TokenID: plainID, RoleName: "editor"}, &rev)
	_, granteeErr := rs.Grant(ctx, RoleGrant{TokenID: "nobody", RoleName: "editor"})
	cases := []phaseTwoACase{
		{name: "Identity of an unknown ref", err: identErr, typ: problemIdentityNotFound, values: map[string]string{"identity": "IDN99"},
			sentinel: ErrNotFound, msg: fmt.Sprintf("%v: no identity %q", ErrNotFound, "IDN99")},
		{name: "join an unknown identity", err: unknownErr, typ: problemIdentityNotFound, values: map[string]string{"identity": "IDN99"},
			sentinel: ErrNotFound, msg: fmt.Sprintf("create token: %v: no identity %q", ErrNotFound, "IDN99")},
		{name: "join an unclassified identity", err: unclassifiedErr, typ: problemTokenIdentityUnclassified,
			values: map[string]string{"identity": "IDN50"}, sentinel: ErrForbidden,
			msg: "create token: forbidden: identity IDN50 has no class: an unclassified identity takes no new token; create an identity with a class and retire this one"},
		{name: "join with a role not in the list", err: badRoleErr, typ: problemFieldNotInList,
			values: map[string]string{"allowed": "author, editor, admin"},
			field:  "role", msg: `validation failed: role: must be "author", "editor" or "admin"`},
		{name: "mint with a class not in the list", err: mint(human, "editor", Role("robot")), typ: problemFieldNotInList,
			values: map[string]string{"allowed": "agent, job, human"},
			field:  "actor_class", msg: `validation failed: actor_class: must be "agent", "job" or "human" (leave it empty for an unclassified token)`},
		{name: "job mints", err: mint(as("j-9", Job), "editor", Agent), typ: problemTokenJobNeverMints, values: nil,
			sentinel: ErrForbidden, msg: "create token: forbidden: a job token never creates a token (D156)"},
		{name: "agent mints a human", err: mint(as("a-9", Agent), "editor", Human), typ: problemTokenAgentMintsAutomatedOnly,
			values: map[string]string{"token_class": "human"}, sentinel: ErrForbidden,
			msg: "create token: forbidden: an agent token mints only agent or job tokens (D142): the new token would be human"},
		{name: "grant to no identity", err: granteeErr, typ: problemGrantGranteeNotIdentity, values: map[string]string{"grantee": "nobody"},
			field: "token_id", msg: `validation failed: token_id: "nobody" is no identity: a grant is held by an identity (D165); mint a token for it first and grant to the id the mint returned`},
		{name: "rev for no identity", err: revErr, typ: problemRevNotApplicable, values: map[string]string{"grantee": plainID},
			field: "rev", msg: fmt.Sprintf("validation failed: rev: %q is no identity, so there is no rev to name: leave rev out", plainID)},
	}
	runPhaseTwoA(t, cases)

	// An identity that is not active takes no new token.
	if _, err := f.db.Exec(`UPDATE smeldr_identities SET status = $1 WHERE address = 'IDN1'`, IdentitySuspended); err != nil {
		t.Fatal(err)
	}
	runPhaseTwoA(t, []phaseTwoACase{{name: "join a suspended identity", err: join("IDN1", "editor"),
		typ: problemTokenIdentityNotActive, values: map[string]string{"identity": "IDN1", "state": IdentitySuspended},
		sentinel: ErrForbidden,
		msg:      "create token: forbidden: identity IDN1 is suspended: it takes no new token"}})
}

// A classified mint on a token table without actor_class is refused before
// anything is written.
func TestPhaseTwoA_IdentitySchemaNotReady(t *testing.T) {
	db := newSQLiteDB(t)
	if err := EnsureIdentities(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE smeldr_tokens (id TEXT PRIMARY KEY, name TEXT NOT NULL, role TEXT NOT NULL,
		expires_at TEXT NOT NULL, revoked_at TEXT, created_at TEXT NOT NULL, user_id TEXT)`); err != nil {
		t.Fatal(err)
	}
	_, _, err := NewTokenStore(db, testSecret).CreateClassified(context.Background(), "bot", "editor", Agent, time.Hour)
	runPhaseTwoA(t, []phaseTwoACase{{name: "no actor_class column", err: err, typ: problemIdentitySchemaNotReady, values: nil,
		field: "actor_class",
		msg:   "validation failed: actor_class: smeldr_tokens has no actor_class column: run EnsureTokenActorClassColumn (and EnsureTokenUserIDColumn) before minting a classified token"}})
}

// A signed token that does not verify keeps its sentinel's text exactly.
func TestPhaseTwoA_AuthTokens(t *testing.T) {
	_, tokErr := decodeToken("garbage", testSecret)
	_, _, _, previewErr := decodePreviewToken("garbage", []byte(testSecret))
	uploadErr := decodeUploadToken("garbage", []byte(testSecret))
	runPhaseTwoA(t, []phaseTwoACase{
		{name: "bearer", err: tokErr, typ: problemAuthTokenInvalid, sentinel: ErrUnauth, msg: ErrUnauth.Error()},
		{name: "preview", err: previewErr, typ: problemAuthPreviewTokenInvalid, sentinel: ErrNotFound, msg: ErrNotFound.Error()},
		{name: "upload", err: uploadErr, typ: problemAuthUploadTokenInvalid, sentinel: ErrUnauth, msg: ErrUnauth.Error()},
	})
}

// AuthorizeCapability's refusals, and Administrators asked by no one.
func TestPhaseTwoA_CapabilityAndAdministrators(t *testing.T) {
	f := newCapabilityFixture(t)
	legacy := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")})
	_, adminErr := f.app.Administrators(as(""))
	runPhaseTwoA(t, []phaseTwoACase{
		{name: "unknown capability", err: f.app.AuthorizeCapability(f.global, "nope", VerbGet, AuthTarget{}),
			typ: problemCapabilityUnknown, values: map[string]string{"resource": "nope", "verb": VerbGet},
			sentinel: ErrBadRequest, msg: fmt.Sprintf("%v: unknown capability %q %q", ErrBadRequest, "nope", VerbGet)},
		{name: "denied without governance",
			err: legacy.AuthorizeCapability(NewTestContext(User{ID: "u", Roles: []Role{Guest}}), ResourceItem, VerbDelete, AuthTarget{}),
			typ: problemCapabilityDenied, sentinel: ErrForbidden, msg: ErrForbidden.Error()},
		{name: "denied an unreadable type", err: f.app.AuthorizeCapability(f.reader, ResourceItem, VerbList, AuthTarget{TypeName: "Goal"}),
			typ: problemCapabilityDenied, sentinel: ErrForbidden, msg: ErrForbidden.Error()},
		{name: "hidden item", err: f.app.AuthorizeCapability(f.reader, ResourceItem, VerbGet, f.item(f.hidden)),
			typ: problemItemNotFound, sentinel: ErrNotFound, msg: ErrNotFound.Error()},
		{name: "administrators unauthenticated", err: adminErr, typ: problemAdministratorsUnauthenticated,
			sentinel: ErrForbidden, msg: fmt.Sprintf("%v: who administers here is answered to an authenticated caller", ErrForbidden)},
	})
}
