package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
)

// IdentityMigrationOptions configure [App.MigrateIdentities].
type IdentityMigrationOptions struct {
	// DryRun reports what the migration would do and writes nothing.
	DryRun bool
}

// MigratedIdentity is one identity the migration creates.
type MigratedIdentity struct {
	// ID is the identity's id: the user_id its tokens already carry.
	ID string `json:"id"`
	// Address is the address it gets (in a dry run, the one it would get).
	Address string `json:"address"`
	// Name is its newest token's name.
	Name string `json:"name"`
	// Class is its tokens' class, or "unclassified".
	Class string `json:"class"`
	// Tokens is how many tokens it holds.
	Tokens int `json:"tokens"`
}

// IdentityMigrationReport is what [App.MigrateIdentities] did, or would do.
// Lists of identities name them by address.
type IdentityMigrationReport struct {
	// DryRun is true when nothing was written.
	DryRun bool `json:"dry_run"`
	// Created are the identities made, oldest first.
	Created []MigratedIdentity `json:"created"`
	// Unclassified are the created identities of class unclassified: each is
	// to be retired or replaced (T998).
	Unclassified []string `json:"unclassified"`
	// OperatorNotSet are the created agent and job identities, whose operator
	// an administrator sets after the migration.
	OperatorNotSet []string `json:"operator_not_set"`
	// ClassDisagreements are the created identities whose tokens carried
	// different classes; each took its newest token's class.
	ClassDisagreements []string `json:"class_disagreements"`
	// TokensWithoutUserID are the tokens (name and fingerprint) minted before
	// user_id was stored: they cannot be given an identity and stay without.
	TokensWithoutUserID []string `json:"tokens_without_user_id"`
	// OrphanGrantees are grantee ids that name neither a token nor an
	// identity. Their grants are left as they are.
	OrphanGrantees []string `json:"orphan_grantees"`
	// TokensLinked is how many tokens were given their identity.
	TokensLinked int `json:"tokens_linked"`
}

// migrationToken is one token row the migration reads.
type migrationToken struct {
	fingerprint, name, class, createdAt string
	userID, identityID                  sql.NullString
}

// MigrateIdentities turns every existing token's user_id into one identity
// with the same id (D165), so no grant or provenance row is rewritten. The
// name is its newest token's name, the class its tokens' class or
// unclassified, the attester the actor of its first token's mint record; the
// operator is left for an administrator to set. Each token is then linked to
// its identity. Tokens with no user_id, class disagreements and grants held by
// no token are reported, not changed.
//
//	report, err := app.MigrateIdentities(ctx, smeldr.IdentityMigrationOptions{DryRun: true})
//
// Idempotent: a second run creates nothing. It needs [EnsureIdentities] to
// have run. It is a write to every token row, so run it deliberately: it is
// not on the boot path.
func (a *App) MigrateIdentities(ctx context.Context, opts IdentityMigrationOptions) (*IdentityMigrationReport, error) {
	db := a.cfg.DB
	if db == nil {
		return nil, fmt.Errorf("%w: MigrateIdentities needs a database", ErrInternal)
	}
	rep := &IdentityMigrationReport{DryRun: opts.DryRun, Created: []MigratedIdentity{}, Unclassified: []string{},
		OperatorNotSet: []string{}, ClassDisagreements: []string{}, TokensWithoutUserID: []string{}, OrphanGrantees: []string{}}
	if ok, err := tableExists(ctx, db, "smeldr_identities"); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("%w: MigrateIdentities: no smeldr_identities table; run EnsureIdentities first", ErrBadRequest)
	}
	tokens, err := migrationTokens(ctx, db)
	if err != nil {
		return nil, err
	}
	existing, err := identityIDs(ctx, db)
	if err != nil {
		return nil, err
	}
	withProvenance, err := tableExists(ctx, db, "smeldr_provenance")
	if err != nil {
		return nil, err
	}

	// Group by user_id; tokens come newest first.
	groups := map[string][]migrationToken{}
	var order []string
	for _, t := range tokens {
		if !t.userID.Valid || t.userID.String == "" {
			rep.TokensWithoutUserID = append(rep.TokensWithoutUserID, t.name+" ("+t.fingerprint[:min(12, len(t.fingerprint))]+")")
			continue
		}
		if _, seen := groups[t.userID.String]; !seen {
			order = append(order, t.userID.String)
		}
		groups[t.userID.String] = append(groups[t.userID.String], t)
	}
	// Oldest identity first, so addresses follow the order the tokens came in.
	oldest := func(id string) string { g := groups[id]; return g[len(g)-1].createdAt }
	sort.SliceStable(order, func(i, j int) bool { return oldest(order[i]) < oldest(order[j]) })

	prefix, next, err := identityAddressNext(ctx, db)
	if err != nil {
		return nil, err
	}
	var create []*Identity
	for _, id := range order {
		g := groups[id]
		for _, t := range g {
			if !t.identityID.Valid || t.identityID.String == "" {
				rep.TokensLinked++
			}
		}
		if existing[id] {
			continue
		}
		class := g[0].class
		for _, t := range g[1:] {
			if t.class != class {
				rep.ClassDisagreements = append(rep.ClassDisagreements, prefix+strconv.FormatInt(next, 10))
				break
			}
		}
		if class == "" {
			class = IdentityUnclassified
		}
		ident := &Identity{ID: id, Name: g[0].name, Class: class, Status: IdentityActive}
		if withProvenance {
			if ident.AttestedBy, err = mintActor(ctx, db, g[len(g)-1].fingerprint); err != nil {
				return nil, err
			}
		}
		address := prefix + strconv.FormatInt(next, 10)
		next++
		rep.Created = append(rep.Created, MigratedIdentity{ID: id, Address: address, Name: ident.Name, Class: class, Tokens: len(g)})
		switch class {
		case IdentityUnclassified:
			rep.Unclassified = append(rep.Unclassified, address)
		case string(Agent), string(Job):
			rep.OperatorNotSet = append(rep.OperatorNotSet, address)
		}
		create = append(create, ident)
	}
	if rep.OrphanGrantees, err = orphanGrantees(ctx, db); err != nil {
		return nil, err
	}
	if opts.DryRun {
		return rep, nil
	}

	exec, commit, rollback, err := conflictTx(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("%w: MigrateIdentities: begin: %s", ErrInternal, err)
	}
	defer rollback()
	for i, ident := range create {
		if err := insertIdentity(ctx, exec, ident); err != nil {
			return nil, err
		}
		rep.Created[i].Address = ident.Address
	}
	if _, err := exec.ExecContext(ctx,
		`UPDATE smeldr_tokens SET identity_id = user_id WHERE (identity_id IS NULL OR identity_id = '') AND user_id IS NOT NULL AND user_id <> ''`); err != nil {
		return nil, fmt.Errorf("%w: MigrateIdentities: link tokens: %s", ErrInternal, err)
	}
	if err := commit(); err != nil {
		return nil, fmt.Errorf("%w: MigrateIdentities: commit: %s", ErrInternal, err)
	}
	return rep, nil
}

// migrationTokens reads every token, newest first.
func migrationTokens(ctx context.Context, db DB) ([]migrationToken, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, COALESCE(actor_class, ''), created_at, user_id, identity_id FROM smeldr_tokens ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("%w: MigrateIdentities: read tokens (run EnsureTokenUserIDColumn, EnsureTokenActorClassColumn and EnsureIdentities first): %s", ErrInternal, err)
	}
	defer rows.Close()
	var out []migrationToken
	for rows.Next() {
		var t migrationToken
		if err := rows.Scan(&t.fingerprint, &t.name, &t.class, &t.createdAt, &t.userID, &t.identityID); err != nil {
			return nil, fmt.Errorf("%w: MigrateIdentities: read tokens: %s", ErrInternal, err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: MigrateIdentities: read tokens: %s", ErrInternal, err)
	}
	return out, nil
}

// identityIDs reads the ids of the identities that exist.
func identityIDs(ctx context.Context, db DB) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT id FROM smeldr_identities`)
	if err != nil {
		return nil, fmt.Errorf("%w: MigrateIdentities: read identities: %s", ErrInternal, err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("%w: MigrateIdentities: read identities: %s", ErrInternal, err)
		}
		out[id] = true
	}
	return out, rows.Err()
}

// identityAddressNext returns the identity prefix and the number the next
// identity gets.
func identityAddressNext(ctx context.Context, db DB) (string, int64, error) {
	var prefix string
	var last int64
	err := db.QueryRowContext(ctx, `SELECT prefix, last_no FROM smeldr_address_counters WHERE type_name = 'Identity'`).Scan(&prefix, &last)
	if err != nil {
		return "", 0, fmt.Errorf("%w: MigrateIdentities: the identity address counter (run EnsureIdentities): %s", ErrInternal, err)
	}
	return prefix, last + 1, nil
}

// mintActor returns the actor of the token's mint record, "" when it has none.
func mintActor(ctx context.Context, db DB, fingerprint string) (string, error) {
	var actor string
	err := db.QueryRowContext(ctx,
		`SELECT actor_id FROM smeldr_provenance WHERE subject_type = 'Token' AND subject_id = $1 AND verb = 'assert' ORDER BY timestamp, id LIMIT 1`,
		fingerprint).Scan(&actor)
	if err == nil || errors.Is(err, sql.ErrNoRows) {
		return actor, nil
	}
	return "", fmt.Errorf("%w: MigrateIdentities: read the mint record of a token: %s", ErrInternal, err)
}

// orphanGrantees returns the grantee ids that name neither a token nor an
// identity; none when there is no grants table.
func orphanGrantees(ctx context.Context, db DB) ([]string, error) {
	out := []string{}
	ok, err := tableExists(ctx, db, "smeldr_role_grants")
	if err != nil || !ok {
		return out, err
	}
	rows, err := db.QueryContext(ctx,
		`SELECT DISTINCT g.token_id FROM smeldr_role_grants g
		  WHERE NOT EXISTS (SELECT 1 FROM smeldr_tokens t WHERE t.user_id = g.token_id)
		    AND NOT EXISTS (SELECT 1 FROM smeldr_identities i WHERE i.id = g.token_id)
		  ORDER BY g.token_id`)
	if err != nil {
		return nil, fmt.Errorf("%w: MigrateIdentities: read grantees: %s", ErrInternal, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("%w: MigrateIdentities: read grantees: %s", ErrInternal, err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
