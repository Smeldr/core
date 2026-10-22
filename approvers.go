package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// PlaceApprover is one holder of approval authority over a place, as
// [App.ApproversOf] returns it (D147): whom to ask. It carries nothing else
// about the holder or the grant: no grant ID, end date, grantor or token
// detail.
type PlaceApprover struct {
	// Name is the holder's token name.
	Name string `json:"name"`
	// Role is the role the holder has the authority by: approver,
	// domain-approver or area-approver.
	Role string `json:"role"`
	// Class is the holder's token class: human, agent, job or unclassified.
	Class string `json:"class"`
	// AnchorAddress is the address of the place the grant sits on (the
	// Organization for an approver, the Domain, the Area), or empty when the
	// caller may not read that place.
	AnchorAddress string `json:"anchor_address"`
}

// approverRoleOrder is the order [App.ApproversOf] lists the roles in.
var approverRoleOrder = map[string]int{RoleApprover: 0, RoleDomainApprover: 1, RoleAreaApprover: 2}

// approverPlace is a place [App.ApproversOf] resolved.
type approverPlace struct {
	typeName, id, address string
}

// ApproversOf returns who holds approval authority over a place (D147): the
// approver holders (they hold it on the Organization), the domain-approver
// holders anchored on the place's Domain and, for an Area, the area-approver
// holders anchored on it. place is an Organization, Domain or Area by
// address, slug or ID.
//
// It answers only when the caller may read the place: an unknown or
// unreadable place is [ErrNotFound] alike, so nothing is revealed. It does
// not need administer. Expired grants are left out, as [RoleStore.Authorized]
// leaves them out (a revoked grant no longer exists), and so is a holder
// with no live token, who cannot answer. Each holder is named by its newest
// live token. Without governance the list is empty.
func (a *App) ApproversOf(ctx context.Context, place string) ([]PlaceApprover, error) {
	place = strings.TrimSpace(place)
	if place == "" {
		return nil, fmt.Errorf("%w: name a place: an Organization, Domain or Area by address, slug or ID", ErrBadRequest)
	}
	if a.cfg.DB == nil {
		return nil, fmt.Errorf("%w: ApproversOf needs a database", ErrInternal)
	}
	scope, err := scopeFor(ctx, a.governance)
	if err != nil {
		return nil, err
	}
	p, err := a.resolveApproverPlace(ctx, place)
	if err != nil {
		return nil, err
	}
	if p.id == "" || !scope.Readable(p.typeName, p.id) {
		return nil, fmt.Errorf("%w: no place %q you can read (an Organization, Domain or Area by address, slug or ID)", ErrNotFound, place)
	}
	out := []PlaceApprover{}
	if a.governance == nil {
		return out, nil
	}
	// The anchors each role is listed for, by place type.
	anchors := map[string]approverPlace{}
	var domainID, areaID string
	switch p.typeName {
	case "Domain":
		domainID = p.id
		anchors[p.id] = p
	case "Area":
		areaID = p.id
		anchors[p.id] = p
		domains, err := a.areaDomains(ctx, []string{p.id})
		if err != nil {
			return nil, err
		}
		if d, ok := domains[p.id]; ok {
			domainID = d.id
			anchors[d.id] = approverPlace{typeName: "Domain", id: d.id, address: d.address}
		}
	}
	org, err := a.organizationPlace(ctx)
	if err != nil {
		return nil, err
	}
	holders, err := a.approverGrants(ctx, domainID, areaID)
	if err != nil {
		return nil, err
	}
	if len(holders) == 0 {
		return out, nil
	}
	ids := make([]string, 0, len(holders))
	for _, h := range holders {
		ids = append(ids, h.holder)
	}
	tokens, err := a.liveTokens(ctx, ids)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, h := range holders {
		tok, ok := tokens[h.holder]
		if !ok || seen[h.holder+"\x00"+h.role] {
			continue
		}
		seen[h.holder+"\x00"+h.role] = true
		anchor := org
		if h.role != RoleApprover {
			anchor = anchors[h.anchorID]
		}
		e := PlaceApprover{Name: tok.name, Role: h.role, Class: tok.class}
		if anchor.id != "" && scope.Readable(anchor.typeName, anchor.id) {
			e.AnchorAddress = anchor.address
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Role != out[j].Role {
			return approverRoleOrder[out[i].Role] < approverRoleOrder[out[j].Role]
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// resolveApproverPlace finds place among the Organization, the Domains and
// the Areas, by address, slug or ID. Not found is a zero place.
func (a *App) resolveApproverPlace(ctx context.Context, place string) (approverPlace, error) {
	for _, t := range []struct{ typeName, table string }{
		{"Area", "smeldr_areas"}, {"Domain", "smeldr_domains"}, {"Organization", "smeldr_organizations"},
	} {
		p := approverPlace{typeName: t.typeName}
		err := a.cfg.DB.QueryRowContext(ctx,
			`SELECT id, address FROM `+t.table+` WHERE address = $1 OR slug = $1 OR id = $1 LIMIT 1`, place).Scan(&p.id, &p.address)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			continue
		case err != nil:
			return approverPlace{}, fmt.Errorf("%w: the place %q cannot be read: %v", ErrInternal, place, err)
		}
		return p, nil
	}
	return approverPlace{}, nil
}

// organizationPlace is the instance's Organization, or a zero place when it
// has none yet.
func (a *App) organizationPlace(ctx context.Context) (approverPlace, error) {
	p := approverPlace{typeName: "Organization"}
	err := a.cfg.DB.QueryRowContext(ctx, `SELECT id, address FROM smeldr_organizations LIMIT 1`).Scan(&p.id, &p.address)
	if errors.Is(err, sql.ErrNoRows) {
		return approverPlace{}, nil
	}
	if err != nil {
		return approverPlace{}, fmt.Errorf("%w: the Organization cannot be read: %v", ErrInternal, err)
	}
	return p, nil
}

// approverGrant is one unexpired grant [App.ApproversOf] lists.
type approverGrant struct {
	holder, role, anchorID string
}

// approverGrants returns the unexpired approver grants, the domain-approver
// grants anchored on domainID and the area-approver grants anchored on
// areaID, in grant order. An empty ID matches no grant: an anchored grant
// always has its anchor.
func (a *App) approverGrants(ctx context.Context, domainID, areaID string) ([]approverGrant, error) {
	rows, err := a.cfg.DB.QueryContext(ctx,
		`SELECT g.token_id, r.name, COALESCE(g.scope_anchor_id, '')
		   FROM smeldr_role_grants g JOIN smeldr_roles r ON r.id = g.role_id
		  WHERE (g.expires_at IS NULL OR g.expires_at > $1)
		    AND (r.name = $2 OR (r.name = $3 AND g.scope_anchor_id = $4) OR (r.name = $5 AND g.scope_anchor_id = $6))
		  ORDER BY g.created_at, g.id`,
		time.Now().UTC(), RoleApprover, RoleDomainApprover, domainID, RoleAreaApprover, areaID)
	if err != nil {
		return nil, fmt.Errorf("%w: approver grants: %v", ErrInternal, err)
	}
	defer rows.Close()
	var out []approverGrant
	for rows.Next() {
		var g approverGrant
		if err := rows.Scan(&g.holder, &g.role, &g.anchorID); err != nil {
			return nil, fmt.Errorf("%w: approver grants: %v", ErrInternal, err)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: approver grants: %v", ErrInternal, err)
	}
	return out, nil
}

// liveToken is the newest live token of a holder.
type liveToken struct{ name, class string }

// liveTokens returns, per holder (a token's user_id), its newest token that
// is neither revoked nor expired. A holder with none is absent. A missing
// actor_class column reads as unclassified.
func (a *App) liveTokens(ctx context.Context, holders []string) (map[string]liveToken, error) {
	args := []any{time.Now().UTC().Format(time.RFC3339)}
	for _, h := range holders {
		args = append(args, h)
	}
	where := ` FROM smeldr_tokens WHERE revoked_at IS NULL AND expires_at > $1 AND user_id IN (` + inMarks(2, len(holders)) + `)
		ORDER BY created_at DESC, id DESC`
	withClass := true
	rows, err := a.cfg.DB.QueryContext(ctx, `SELECT user_id, name, COALESCE(actor_class, '')`+where, args...)
	if err != nil && isNoSuchColumn(err, "actor_class") {
		withClass = false
		rows, err = a.cfg.DB.QueryContext(ctx, `SELECT user_id, name`+where, args...)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: the approvers' tokens: %v", ErrInternal, err)
	}
	defer rows.Close()
	out := map[string]liveToken{}
	for rows.Next() {
		var holder string
		var t liveToken
		dest := []any{&holder, &t.name}
		if withClass {
			dest = append(dest, &t.class)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("%w: the approvers' tokens: %v", ErrInternal, err)
		}
		if _, ok := out[holder]; ok {
			continue
		}
		if t.class == "" {
			t.class = "unclassified"
		}
		out[holder] = t
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: the approvers' tokens: %v", ErrInternal, err)
	}
	return out, nil
}
