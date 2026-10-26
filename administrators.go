package smeldr

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"time"
)

// InstanceAdministrator is one holder of administer over the instance, as
// [App.Administrators] returns it (D154): whom to ask for a grant, a token or
// a change to the instance's setup. It carries nothing else about the holder
// or the grant: no grant ID, end date, grantor, reason, role or token detail.
type InstanceAdministrator struct {
	// Name is the holder's token name.
	Name string `json:"name"`
	// Class is the holder's token class: human, agent, job or unclassified.
	Class string `json:"class"`
}

// Administrators returns who administers the instance (D154): the holders of
// an unexpired grant whose role holds the administer operation at global
// scope, which is what [RoleStore.Authorized] answers administer by. A static
// or dynamic grant does not administer the instance, and neither does a
// token's legacy role without a grant. Each holder is named by its newest
// live token; a holder with none is left out, as it cannot be asked. The list
// is sorted by name.
//
// Any authenticated caller may ask, also one that reads nothing yet: the
// member with no grant is the one who most needs to know whom to ask. A
// caller with no user ID is [ErrForbidden]. Without governance the list is
// empty.
func (a *App) Administrators(ctx context.Context) ([]InstanceAdministrator, error) {
	sc, ok := ctx.(Context)
	if !ok || sc.User().ID == "" {
		return nil, fmt.Errorf("%w: who administers here is answered to an authenticated caller", ErrForbidden)
	}
	if a.cfg.DB == nil {
		return nil, fmt.Errorf("%w: Administrators needs a database", ErrInternal)
	}
	out := []InstanceAdministrator{}
	if a.governance == nil {
		return out, nil
	}
	holders, err := a.administratorHolders(ctx)
	if err != nil || len(holders) == 0 {
		return out, err
	}
	tokens, err := a.liveTokens(ctx, holders)
	if err != nil {
		return nil, err
	}
	for _, h := range holders {
		if tok, ok := tokens[h]; ok {
			out = append(out, InstanceAdministrator{Name: tok.name, Class: tok.class})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Class < out[j].Class
	})
	return out, nil
}

// administratorHolders returns, once each and in grant order, the holders of
// an unexpired global grant whose role's operations hold administer. A role
// whose operations do not parse is skipped, as Authorized skips it.
func (a *App) administratorHolders(ctx context.Context) ([]string, error) {
	rows, err := a.cfg.DB.QueryContext(ctx,
		`SELECT g.token_id, r.operations
		   FROM smeldr_role_grants g JOIN smeldr_roles r ON r.id = g.role_id
		  WHERE r.scope_mode = $1 AND r.operations LIKE '%"administer"%'
		    AND (g.expires_at IS NULL OR g.expires_at > $2)
		  ORDER BY g.created_at, g.id`,
		string(ScopeGlobal), time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("%w: the administer grants: %v", ErrInternal, err)
	}
	defer rows.Close()
	var out []string
	seen := map[string]bool{}
	for rows.Next() {
		var holder, opsJSON string
		if err := rows.Scan(&holder, &opsJSON); err != nil {
			return nil, fmt.Errorf("%w: the administer grants: %v", ErrInternal, err)
		}
		var ops []string
		if json.Unmarshal([]byte(opsJSON), &ops) != nil || !slices.Contains(ops, "administer") || seen[holder] {
			continue
		}
		seen[holder] = true
		out = append(out, holder)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: the administer grants: %v", ErrInternal, err)
	}
	return out, nil
}
