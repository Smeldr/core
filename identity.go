package smeldr

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Identity statuses (D165). Only an active identity's tokens verify and only
// an active identity takes a new token.
const (
	// IdentityActive is an identity in use.
	IdentityActive = "active"
	// IdentitySuspended is an identity whose every token is refused at once,
	// its history kept.
	IdentitySuspended = "suspended"
	// IdentityRetired is an identity that takes no new token and whose tokens
	// no longer verify, its history kept.
	IdentityRetired = "retired"
)

// IdentityUnclassified is the class of an identity migrated from a token minted
// without a class ([MigrateIdentities]). No new identity is ever unclassified,
// and an unclassified identity takes no new token.
const IdentityUnclassified = "unclassified"

// Identity is one person, agent or job (D165): the record every token belongs
// to, that grants are held by, and that provenance names as the actor. Its ID
// is the User.ID its tokens are signed with, the same value every grant
// ([RoleGrant.TokenID]), provenance row and last_actor already holds, so
// identities came in without rewriting any of them.
//
// The class is fixed for the identity's life: a wrong class means a new
// identity, never a change.
type Identity struct {
	// ID is the User.ID of the identity's tokens.
	ID string `json:"id"`
	// Address is the server-assigned address (IDN12, D110).
	Address string `json:"address"`
	// Name is the display name.
	Name string `json:"name"`
	// Class is "human", "agent" or "job", or [IdentityUnclassified] for a
	// migrated identity.
	Class string `json:"class"`
	// Status is [IdentityActive], [IdentitySuspended] or [IdentityRetired].
	Status string `json:"status"`
	// AttestedBy is the identity that created this one and attests its class,
	// or the mechanism that minted it ("token-bootstrap"). Empty when not
	// known (an identity migrated from a token with no mint record).
	AttestedBy string `json:"attested_by"`
	// OperatorID is the human identity responsible for an agent or job. Empty
	// when not set yet: the directory shows "operator not set".
	OperatorID string `json:"operator_id"`
	// Purpose is one line saying what the identity is for.
	Purpose string `json:"purpose"`
	// Contact is a human identity's contact, shown only to administrators.
	Contact string `json:"contact,omitempty"`
	// Rev is the record's revision (T1010).
	Rev int64 `json:"rev"`
	// CreatedAt and UpdatedAt are the record's times, UTC.
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// identityTableStmts create the identity record and its index.
var identityTableStmts = []string{
	`CREATE TABLE IF NOT EXISTS smeldr_identities (
		id          TEXT NOT NULL PRIMARY KEY,
		name        TEXT NOT NULL,
		class       TEXT NOT NULL,
		status      TEXT NOT NULL DEFAULT 'active',
		attested_by TEXT NOT NULL DEFAULT '',
		operator_id TEXT NOT NULL DEFAULT '',
		purpose     TEXT NOT NULL DEFAULT '',
		contact     TEXT NOT NULL DEFAULT '',
		rev         INTEGER NOT NULL DEFAULT 0,
		created_at  TIMESTAMPTZ NOT NULL,
		updated_at  TIMESTAMPTZ NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_smeldr_identities_operator ON smeldr_identities (operator_id)`,
}

// EnsureIdentities creates the identity record (D165) with its address storage
// (IDN), and adds identity_id to smeldr_tokens when that table exists.
// Idempotent, safe to call on every boot; call it before the [TokenStore] is
// made. It creates no identity: [App.MigrateIdentities] turns the existing
// tokens into identities, and every classified mint after this creates or
// joins one.
func EnsureIdentities(ctx context.Context, db DB) error {
	if db == nil {
		return nil
	}
	for _, stmt := range identityTableStmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("smeldr: EnsureIdentities: %w", err)
		}
	}
	if err := registerCoreAddresses(ctx, db, "Identity"); err != nil {
		return fmt.Errorf("smeldr: EnsureIdentities: %w", err)
	}
	if err := EnsureColumn(ctx, db, "smeldr_tokens", "identity_id", "TEXT"); err != nil {
		if isNoSuchTable(errors.Unwrap(err)) || isNoSuchTable(err) {
			return nil // no token store on this App
		}
		return fmt.Errorf("smeldr: EnsureIdentities: %w", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_smeldr_tokens_identity ON smeldr_tokens (identity_id)`); err != nil {
		return fmt.Errorf("smeldr: EnsureIdentities: %w", err)
	}
	return nil
}

// identityColumns is the column list [scanIdentity] reads, in order.
const identityColumns = `id, address, name, class, status, attested_by, operator_id, purpose, contact, rev, created_at, updated_at`

// scanIdentity reads one row of identityColumns.
func scanIdentity(row interface{ Scan(...any) error }) (*Identity, error) {
	var id Identity
	if err := row.Scan(&id.ID, &id.Address, &id.Name, &id.Class, &id.Status, &id.AttestedBy, &id.OperatorID,
		&id.Purpose, &id.Contact, &id.Rev, scanDest(&id.CreatedAt), scanDest(&id.UpdatedAt)); err != nil {
		return nil, err
	}
	return &id, nil
}

// lookupIdentity reads the identity whose id or address is ref, or nil when
// there is none, or no identities table.
func lookupIdentity(ctx context.Context, db DB, ref string) (*Identity, error) {
	id, err := scanIdentity(db.QueryRowContext(ctx,
		`SELECT `+identityColumns+` FROM smeldr_identities WHERE id = $1 OR address = $1`, ref))
	switch {
	case err == nil:
		return id, nil
	case errors.Is(err, sql.ErrNoRows), isNoSuchTable(err):
		return nil, nil
	default:
		return nil, fmt.Errorf("%w: read identity %s: %s", ErrInternal, ref, err)
	}
}

// Identity returns the identity whose id or address is ref (D165). A ref that
// names none is [ErrNotFound]. Any authenticated caller may read an identity:
// names, classes and operators are visible to every member (design section 8);
// the contact is left out unless the caller administers.
func (a *App) Identity(ctx context.Context, ref string) (*Identity, error) {
	if a.cfg.DB == nil {
		return nil, fmt.Errorf("%w: Identity needs a database", ErrInternal)
	}
	id, err := lookupIdentity(ctx, a.cfg.DB, strings.TrimSpace(ref))
	if err != nil {
		return nil, err
	}
	if id == nil {
		return nil, fmt.Errorf("%w: no identity %q", ErrNotFound, ref)
	}
	if !a.callerAdministers(ctx) {
		id.Contact = ""
	}
	return id, nil
}

// callerAdministers reports whether the caller holds the admin role, by its
// grants when governance is wired and by its token role otherwise. In-process
// Go code with no request user (a job, a migration) administers.
func (a *App) callerAdministers(ctx context.Context) bool {
	sc, ok := ctx.(Context)
	if !ok {
		return true
	}
	u := sc.User()
	if a.governance != nil {
		if granted, err := a.governance.RoleGranted(ctx, u.ID, "admin", AuthTarget{}); err == nil && granted {
			return true
		}
	}
	return HasRole(u.Roles, Admin)
}

// tokenState is what verification reads about a stored token: whether it is
// revoked, and its identity's class and status when it has one.
type tokenState struct {
	revoked bool
	class   string
	status  string
}

// readTokenState reads the token with fingerprint fp and its identity in one
// query. found is false when the store holds no such token. A store without
// the identities table (or without identity_id) reads revocation alone, and
// remembers that, so the hot path stays one query.
func (ts *TokenStore) readTokenState(ctx context.Context, fp string) (st tokenState, found bool, err error) {
	var revokedAt any
	if !ts.noIdentities.Load() {
		var class, status sql.NullString
		err = ts.db.QueryRowContext(ctx,
			`SELECT t.revoked_at, i.class, i.status FROM smeldr_tokens t
			   LEFT JOIN smeldr_identities i ON i.id = t.identity_id WHERE t.id = $1`, fp).
			Scan(&revokedAt, &class, &status)
		if err == nil {
			return tokenState{revoked: revokedAt != nil, class: class.String, status: status.String}, true, nil
		}
		if !isNoSuchTable(err) && !isNoSuchColumn(err, "identity_id") {
			if errors.Is(err, sql.ErrNoRows) {
				return tokenState{}, false, nil
			}
			return tokenState{}, false, err
		}
		ts.noIdentities.Store(true)
	}
	err = ts.db.QueryRowContext(ctx, `SELECT revoked_at FROM smeldr_tokens WHERE id = $1`, fp).Scan(&revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return tokenState{}, false, nil
	}
	if err != nil {
		return tokenState{}, false, err
	}
	return tokenState{revoked: revokedAt != nil}, true, nil
}

// usable reports whether a token in state st may act: not revoked, and its
// identity, if it has one, active.
func (st tokenState) usable() bool {
	return !st.revoked && (st.status == "" || st.status == IdentityActive)
}

// withIdentityClass returns roles with the class tag set from the identity's
// class: the token takes its class from its identity (D165). An empty class
// (no identity) leaves the signed roles as they are; an unclassified identity
// carries no class tag.
func withIdentityClass(roles []Role, class string) []Role {
	if class == "" {
		return roles
	}
	out := make([]Role, 0, len(roles)+1)
	for _, r := range roles {
		if r != Human && r != Agent && r != Job {
			out = append(out, r)
		}
	}
	if class != IdentityUnclassified {
		out = append(out, Role(class))
	}
	return out
}

// tokenFingerprint is the stored id of a raw token: its SHA-256, hex.
func tokenFingerprint(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// verifyStored checks a decoded token against the store: present, not
// revoked, its identity active. It returns the user with the identity's class
// and the token's fingerprint ([User.TokenID]).
func (ts *TokenStore) verifyStored(ctx context.Context, token string, user User) (User, bool) {
	fp := tokenFingerprint(token)
	st, found, err := ts.readTokenState(ctx, fp)
	if err != nil || !found || !st.usable() {
		return GuestUser, false
	}
	user.Roles = withIdentityClass(user.Roles, st.class)
	user.TokenID = fp
	return user, true
}

// identityStore reports whether the token store's database has the identities
// table, probing it once per call (mints are rare; the hot path does not use
// this).
func (ts *TokenStore) identityStore(ctx context.Context) (bool, error) {
	var n int
	err := ts.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_identities WHERE 1 = 0`).Scan(&n)
	switch {
	case err == nil:
		return true, nil
	case isNoSuchTable(err):
		return false, nil
	default:
		return false, fmt.Errorf("%w: probe smeldr_identities: %s", ErrInternal, err)
	}
}

// newIdentityFor works out the new identity a classified mint creates without
// one being named: its attester and, for an agent or job, its operator, from
// the caller (D165, design section 5.3). A human caller operates what it
// mints; an agent's mint gets the agent's operator; a mechanism leaves the
// operator unset.
func (ts *TokenStore) newIdentityFor(ctx context.Context, id, name string, class Role, mechanism string) (*Identity, error) {
	_, attester := actActor(ctx, mechanism)
	ident := &Identity{ID: id, Name: name, Class: string(class), Status: IdentityActive, AttestedBy: attester}
	if class != Agent && class != Job {
		return ident, nil
	}
	callerID, kind := actorFromContext(ctx)
	switch kind {
	case string(Human):
		ident.OperatorID = callerID
	case string(Agent):
		caller, err := lookupIdentity(ctx, ts.db, callerID)
		if err != nil {
			return nil, err
		}
		if caller != nil {
			ident.OperatorID = caller.OperatorID
		}
	}
	return ident, nil
}

// insertIdentity writes ident with a new address, inside the mint's
// transaction.
func insertIdentity(ctx context.Context, exec DB, ident *Identity) error {
	address, no, err := allocateAddress(ctx, exec, "Identity")
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	ident.Address, ident.CreatedAt, ident.UpdatedAt = address, now, now
	if _, err := exec.ExecContext(ctx,
		`INSERT INTO smeldr_identities (id, address, address_no, name, class, status, attested_by, operator_id, purpose, contact, rev, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 0, $11, $12)`,
		ident.ID, address, no, ident.Name, ident.Class, ident.Status, ident.AttestedBy, ident.OperatorID,
		ident.Purpose, ident.Contact, now, now); err != nil {
		return fmt.Errorf("%w: create identity: %s", ErrInternal, err)
	}
	return nil
}

// joinableIdentity resolves ref for a token that joins it, refusing an
// identity that is not active or has no class (D165).
func (ts *TokenStore) joinableIdentity(ctx context.Context, ref string) (*Identity, error) {
	ident, err := lookupIdentity(ctx, ts.db, strings.TrimSpace(ref))
	if err != nil {
		return nil, err
	}
	if ident == nil {
		return nil, fmt.Errorf("create token: %w: no identity %q", ErrNotFound, ref)
	}
	if ident.Status != IdentityActive {
		return nil, fmt.Errorf("create token: %w", Refused(fmt.Sprintf("identity %s is %s: it takes no new token", ident.Address, ident.Status)))
	}
	if ident.Class == IdentityUnclassified {
		return nil, fmt.Errorf("create token: %w", Refused(fmt.Sprintf("identity %s has no class: an unclassified identity takes no new token; create an identity with a class and retire this one", ident.Address)))
	}
	return ident, nil
}

// CreateForIdentity mints a token that joins an existing identity (D165): the
// token is signed with the identity's id, takes its class, and acts with the
// grants the identity holds. identity is the identity's id or address. A
// token minted this way is a rotation's new half: mint, then revoke the old
// token, and the identity's grants and history stay as they are.
//
//	raw, err := tokens.CreateForIdentity(ctx, "IDN4", "core-implementer-2026-11", "editor", 90*24*time.Hour, "rotation")
//
// Refused when the identity is not active or is unclassified, and under the
// same mint rules as [TokenStore.CreateClassifiedWithReason] applied to the
// identity's class (D142, D156: an agent joins only an agent or job, a job
// never mints). An identity that does not exist is [ErrNotFound].
func (ts *TokenStore) CreateForIdentity(ctx context.Context, identity, name, role string, ttl time.Duration, reason string) (string, error) {
	reason, err := actReason(reason)
	if err != nil {
		return "", err
	}
	switch Role(role) {
	case Author, Editor, Admin:
	default:
		return "", Err("role", `must be "author", "editor" or "admin"`)
	}
	ident, err := ts.joinableIdentity(ctx, identity)
	if err != nil {
		return "", err
	}
	raw, _, err := ts.createToken(ctx, name, role, Role(ident.Class), ttl, reason, mintActorStore, ident)
	return raw, err
}

// insertClassified stores a classified token: with its identity, created here
// in the same transaction unless join names one, when the identities table
// exists; as before identities otherwise.
func (ts *TokenStore) insertClassified(ctx context.Context, id, name, role string, class Role, expiresAt, now time.Time, userID, mechanism string, join *Identity) error {
	withIdentities, err := ts.identityStore(ctx)
	if err != nil {
		return err
	}
	var ident *Identity
	if withIdentities && join == nil {
		if ident, err = ts.newIdentityFor(ctx, userID, name, class, mechanism); err != nil {
			return err
		}
	}
	exec, commit, rollback, err := conflictTx(ctx, ts.db)
	if err != nil {
		return ErrInternal
	}
	defer rollback()
	if ident != nil {
		if err := insertIdentity(ctx, exec, ident); err != nil {
			return err
		}
	}
	if withIdentities {
		_, err = exec.ExecContext(ctx,
			`INSERT INTO smeldr_tokens (id, name, role, expires_at, created_at, user_id, actor_class, identity_id) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			id, name, role, expiresAt.Format(time.RFC3339), now.Format(time.RFC3339), userID, string(class), userID)
	} else {
		_, err = exec.ExecContext(ctx,
			`INSERT INTO smeldr_tokens (id, name, role, expires_at, created_at, user_id, actor_class) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			id, name, role, expiresAt.Format(time.RFC3339), now.Format(time.RFC3339), userID, string(class))
	}
	switch {
	case isNoSuchColumn(err, "actor_class") || isNoSuchColumn(err, "user_id"):
		// Refuse rather than mint a token whose list entry would not say
		// what it is: the signed payload would carry the class, the table
		// would not.
		return Err("actor_class", "smeldr_tokens has no actor_class column: run EnsureTokenActorClassColumn (and EnsureTokenUserIDColumn) before minting a classified token")
	case isNoSuchColumn(err, "identity_id"):
		return Err("identity", "smeldr_tokens has no identity_id column: run EnsureIdentities after the token table exists")
	case err != nil:
		return ErrInternal
	}
	if err := commit(); err != nil {
		return ErrInternal
	}
	return nil
}

// identitiesOf reads the identities among ids, by id. A database without the
// identities table reads none. Not for use inside a transaction.
func identitiesOf(ctx context.Context, db DB, ids []string) (map[string]*Identity, error) {
	out := map[string]*Identity{}
	if len(ids) == 0 {
		return out, nil
	}
	ok, err := tableExists(ctx, db, "smeldr_identities")
	if err != nil || !ok {
		return out, err
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := db.QueryContext(ctx, `SELECT `+identityColumns+` FROM smeldr_identities WHERE id IN (`+inMarks(1, len(ids))+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("%w: read identities: %s", ErrInternal, err)
	}
	defer rows.Close()
	for rows.Next() {
		ident, err := scanIdentity(rows)
		if err != nil {
			return nil, fmt.Errorf("%w: read identities: %s", ErrInternal, err)
		}
		out[ident.ID] = ident
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: read identities: %s", ErrInternal, err)
	}
	return out, nil
}

// checkGrantee refuses a grant to an id that is neither an identity nor the
// user_id of a token (D165: a grant is held by an identity), once the
// identities table exists. Before it, and inside a transaction where the
// probe may not run, nothing is checked, as before identities.
func checkGrantee(ctx context.Context, db DB, grantee string) error {
	if _, inTx := db.(*sql.Tx); inTx {
		return nil
	}
	ok, err := tableExists(ctx, db, "smeldr_identities")
	if err != nil || !ok {
		return err
	}
	var n int
	err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_identities WHERE id = $1`, grantee).Scan(&n)
	if err != nil {
		return fmt.Errorf("%w: check the grantee: %s", ErrInternal, err)
	}
	if n > 0 {
		return nil
	}
	err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_tokens WHERE user_id = $1`, grantee).Scan(&n)
	switch {
	case isNoSuchTable(err) || isNoSuchColumn(err, "user_id"):
		n = 0
	case err != nil:
		return fmt.Errorf("%w: check the grantee: %s", ErrInternal, err)
	}
	if n == 0 {
		return Err("token_id", fmt.Sprintf("%q is no identity: a grant is held by an identity (D165); mint a token for it first and grant to the id the mint returned", grantee))
	}
	return nil
}
