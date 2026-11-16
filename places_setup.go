// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// placesSetupActor is the job that creates Organization and the default Domain.
const placesSetupActor = "setup"

// defaultDomainSlug and defaultDomainName name the Domain created with Organization.
// The word is brand's to propose (D111); D141 then renames it here too.
const (
	defaultDomainSlug = "general"
	defaultDomainName = "General"
)

// domainOrganizationEdge is the edge that places Domain id in Organization org,
// written by the setup job.
func domainOrganizationEdge(id, org string) RelationEdge {
	job := placesSetupActor
	return RelationEdge{
		SourceType: "Domain", SourceID: id, TargetType: "Organization", TargetID: org,
		RelationKind: "belongs_to_organization", EdgeClass: "asserted", CreatedByJob: &job, Attributes: json.RawMessage("{}"),
	}
}

// domainInOrganization places a new Domain in the instance's Organization
// (D142, T1321): it writes belongs_to_organization on the create's own
// transaction, so the Domain is never stored outside the Organization. Without
// relations, or unless the instance has exactly one Organization (the schema
// holds one, idx_smeldr_organizations_one), it writes nothing and the caller
// places the Domain. An update writes nothing.
func (a *App) domainInOrganization(ctx Context, exec DB, existing, item any) error {
	d, ok := item.(*Domain)
	if existing != nil || !ok || d.ID == "" || a.relationStore == nil {
		return nil
	}
	orgs, err := organizationIDs(ctx, exec)
	if err != nil {
		return fmt.Errorf("%w: place the Domain in the Organization: %s", ErrInternal, err)
	}
	if len(orgs) != 1 {
		return nil
	}
	if _, _, err := writeEdgeRow(ctx, exec, domainOrganizationEdge(d.ID, orgs[0])); err != nil {
		return fmt.Errorf("%w: place the Domain in the Organization: %s", ErrInternal, err)
	}
	return nil
}

// announceDomainPlace announces the edge [App.domainInOrganization] wrote, once
// the create has committed. Fail-open: the edge is stored.
func (a *App) announceDomainPlace(ctx Context, db DB, rs *RelationStore, existing, item any) {
	d, ok := item.(*Domain)
	if existing != nil || !ok || rs == nil {
		return
	}
	orgs, err := organizationIDs(ctx, db)
	if err != nil || len(orgs) != 1 {
		return
	}
	if edge, err := liveEdge(ctx, db, domainOrganizationEdge(d.ID, orgs[0])); err == nil {
		rs.announceEdge(ctx, edge, true)
	}
}

// placeDomainsNeverPlaced gives belongs_to_organization to every Domain that
// never had one (T1321): a Domain created before a create placed it. A Domain
// whose edge was ended on purpose keeps its history and is left alone. It
// returns the addresses (or IDs) of the Domains it placed.
func (a *App) placeDomainsNeverPlaced(ctx context.Context, db DB, org string) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT d.id, COALESCE(d.address, '') FROM smeldr_domains d WHERE NOT EXISTS (
		   SELECT 1 FROM smeldr_relations r WHERE r.source_type = 'Domain' AND r.source_id = d.id
		     AND r.relation_kind = 'belongs_to_organization')
		 ORDER BY d.created_at, d.id`)
	if err != nil {
		return nil, err
	}
	type domain struct{ id, address string }
	var todo []domain
	for rows.Next() {
		var d domain
		if err := rows.Scan(&d.id, &d.address); err != nil {
			rows.Close()
			return nil, err
		}
		todo = append(todo, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var placed []string
	for _, d := range todo {
		edge, _, err := writeEdgeRow(ctx, db, domainOrganizationEdge(d.id, org))
		if err != nil {
			return placed, err
		}
		if a.relationStore != nil {
			a.relationStore.announceEdge(ctx, edge, true)
		}
		placed = append(placed, addressOrID(d.address, d.id))
	}
	return placed, nil
}

// repairDomainPlaces runs [App.placeDomainsNeverPlaced] for an existing
// Organization at boot and logs one Info line naming the Domains it placed;
// nothing when there were none or there are no relations. A failure is a
// warning: the boot goes on, and the next boot tries again.
func (a *App) repairDomainPlaces(ctx context.Context, db DB, org string) {
	relations, err := tableExists(ctx, db, "smeldr_relations")
	if err != nil || !relations {
		return
	}
	placed, err := a.placeDomainsNeverPlaced(ctx, db, org)
	if len(placed) > 0 {
		slog.InfoContext(ctx, "smeldr: Domains placed in the Organization, which they were never in", "count", len(placed), "domains", placed)
	}
	if err != nil {
		slog.WarnContext(ctx, "smeldr: Domains not placed in the Organization", "error", err)
	}
}

// EnsureOrganization makes sure the instance has its Organization (D142): when none exists it
// creates Organization with name, a default Domain, and the belongs_to_organization edge from
// the Domain to Organization, in one transaction, recording provenance for each as
// the setup job. An existing Organization is left as it is and returned, so the call
// is safe on every boot. An empty name is refused; on an instance that has not
// run [App.MigratePlaces] yet it is [ErrConflict], and the migration creates
// Organization itself. Call after
// [CreateGovernanceTables] and [RegisterGovernanceTypes]; the edge needs the
// relation tables ([CreateRelationTables]) and is skipped without them.
func (a *App) EnsureOrganization(ctx context.Context, name string) (*OrganizationRecord, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fieldErr(problemFieldRequired, "name", "is required: the organization's name", nil)
	}
	db := a.cfg.DB
	if db == nil {
		return nil, fmt.Errorf("%w: EnsureOrganization needs a database", ErrInternal)
	}
	if existing, err := QueryOne[*OrganizationRecord](ctx, db, `SELECT * FROM smeldr_organizations LIMIT 1`); err == nil {
		a.repairDomainPlaces(ctx, db, existing.ID)
		return existing, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("%w: EnsureOrganization: read Organization: %s", ErrInternal, err)
	}
	// An instance that still keeps the runtime domain type has not run
	// MigratePlaces: the Domain address prefix is not free yet.
	if held, err := heldByLegacyPlaceType(ctx, db, "Domain", coreAddressPrefixes["Domain"]); err != nil {
		return nil, err
	} else if held {
		return nil, problem(problemPlacesDomainPrefixHeld,
			fmt.Errorf("%w: the runtime domain type still holds the DOM prefix; run migrate-places, which creates Organization", ErrConflict),
			problemValues{"prefix": "DOM"})
	}
	relations, err := tableExists(ctx, db, "smeldr_relations")
	if err != nil {
		return nil, fmt.Errorf("%w: EnsureOrganization: %s", ErrInternal, err)
	}
	now := time.Now().UTC()
	root := &OrganizationRecord{Node: Node{ID: NewID(), Slug: "organization", Status: "active", CreatedAt: now, UpdatedAt: now}, Name: name}
	domain := &Domain{Node: Node{ID: NewID(), Slug: defaultDomainSlug, Status: "active", CreatedAt: now, UpdatedAt: now}, Name: defaultDomainName}
	var edge RelationEdge
	err = runInTx(ctx, db, func(exec DB) error {
		if err := insertPlace(ctx, exec, "Organization", "smeldr_organizations", &root.Node, &root.RecordAddress, root.Name); err != nil {
			return err
		}
		if err := insertPlace(ctx, exec, "Domain", "smeldr_domains", &domain.Node, &domain.RecordAddress, domain.Name); err != nil {
			return err
		}
		if !relations {
			return nil
		}
		job := placesSetupActor
		edge, _, err = writeEdgeRow(ctx, exec, RelationEdge{
			SourceType: "Domain", SourceID: domain.ID, TargetType: "Organization", TargetID: root.ID,
			RelationKind: "belongs_to_organization", EdgeClass: "asserted", CreatedByJob: &job, Attributes: json.RawMessage("{}"),
		})
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("%w: EnsureOrganization: %s", ErrInternal, err)
	}
	for _, rec := range []struct{ typeName, id string }{{"Organization", root.ID}, {"Domain", domain.ID}} {
		recordProvenance(ctx, a.provenanceStore, ProvenanceRecord{
			SubjectType: rec.typeName, SubjectID: rec.id, Verb: "create",
			ActorKind: "job", ActorID: placesSetupActor, Reason: "the organization's Organization and default Domain",
		})
	}
	if a.relationStore != nil && edge.ID != "" {
		a.relationStore.announceEdge(ctx, edge, true)
	}
	return root, nil
}

// insertPlace inserts one place record with its address, taken in the same
// transaction (D110).
func insertPlace(ctx context.Context, exec DB, typeName, table string, n *Node, addr *RecordAddress, name string) error {
	address, no, err := allocateAddress(ctx, exec, typeName)
	if err != nil {
		return err
	}
	addr.assignAddress(address, no)
	_, err = exec.ExecContext(ctx,
		`INSERT INTO `+table+` (id, slug, status, created_at, updated_at, rev, name, description, last_actor, address, address_no, external_ref)
		 VALUES ($1, $2, $3, $4, $5, 0, $6, '', '', $7, $8, '')`,
		n.ID, n.Slug, string(n.Status), n.CreatedAt, n.UpdatedAt, name, address, no)
	if err != nil {
		return fmt.Errorf("insert %s: %w", typeName, err)
	}
	return nil
}
