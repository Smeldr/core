// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// placesSetupActor is the job that creates Root and the default Domain.
const placesSetupActor = "setup"

// defaultDomainSlug and defaultDomainName name the Domain created with Root.
// The word is brand's to propose (D111); D141 then renames it here too.
const (
	defaultDomainSlug = "default"
	defaultDomainName = "Default"
)

// EnsureRoot makes sure the instance has its Root (D142): when none exists it
// creates Root with name, a default Domain, and the belongs_to_root edge from
// the Domain to Root, in one transaction, recording provenance for each as
// the setup job. An existing Root is left as it is and returned, so the call
// is safe on every boot. An empty name is refused; on an instance that has not
// run [App.MigratePlaces] yet it is [ErrConflict], and the migration creates
// Root itself. Call after
// [CreateGovernanceTables] and [RegisterGovernanceTypes]; the edge needs the
// relation tables ([CreateRelationTables]) and is skipped without them.
func (a *App) EnsureRoot(ctx context.Context, name string) (*Root, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, Err("name", "is required: the organization's name")
	}
	db := a.cfg.DB
	if db == nil {
		return nil, fmt.Errorf("%w: EnsureRoot needs a database", ErrInternal)
	}
	if existing, err := QueryOne[*Root](ctx, db, `SELECT * FROM smeldr_roots LIMIT 1`); err == nil {
		return existing, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("%w: EnsureRoot: read Root: %s", ErrInternal, err)
	}
	// An instance that still keeps the runtime domain type has not run
	// MigratePlaces: the Domain address prefix is not free yet.
	if held, err := heldByLegacyPlaceType(ctx, db, "Domain", coreAddressPrefixes["Domain"]); err != nil {
		return nil, err
	} else if held {
		return nil, fmt.Errorf("%w: the runtime domain type still holds the DOM prefix; run migrate-places, which creates Root", ErrConflict)
	}
	relations, err := tableExists(ctx, db, "smeldr_relations")
	if err != nil {
		return nil, fmt.Errorf("%w: EnsureRoot: %s", ErrInternal, err)
	}
	now := time.Now().UTC()
	root := &Root{Node: Node{ID: NewID(), Slug: "root", Status: "active", CreatedAt: now, UpdatedAt: now}, Name: name}
	domain := &Domain{Node: Node{ID: NewID(), Slug: defaultDomainSlug, Status: "active", CreatedAt: now, UpdatedAt: now}, Name: defaultDomainName}
	var edge RelationEdge
	err = runInTx(ctx, db, func(exec DB) error {
		if err := insertPlace(ctx, exec, "Root", "smeldr_roots", &root.Node, &root.RecordAddress, root.Name); err != nil {
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
			SourceType: "Domain", SourceID: domain.ID, TargetType: "Root", TargetID: root.ID,
			RelationKind: "belongs_to_root", EdgeClass: "asserted", CreatedByJob: &job, Attributes: json.RawMessage("{}"),
		})
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("%w: EnsureRoot: %s", ErrInternal, err)
	}
	for _, rec := range []struct{ typeName, id string }{{"Root", root.ID}, {"Domain", domain.ID}} {
		recordProvenance(ctx, a.provenanceStore, ProvenanceRecord{
			SubjectType: rec.typeName, SubjectID: rec.id, Verb: "create",
			ActorKind: "job", ActorID: placesSetupActor, Reason: "the organization's Root and default Domain (D142)",
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
