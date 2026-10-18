// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

// PlaceMigrationOptions configures [App.MigratePlaces].
type PlaceMigrationOptions struct {
	// DryRun reports what the migration would do and writes nothing.
	DryRun bool
	// RootName is the organization's name, for the Root the migration
	// creates when the instance has none (required then).
	RootName string
}

// PlaceMigrationReport is what [App.MigratePlaces] did, or would do.
type PlaceMigrationReport struct {
	DryRun bool `json:"dry_run"`
	// Moved are the runtime records copied to the compiled place types, with
	// their IDs and addresses unchanged.
	Moved []PlaceMove `json:"moved"`
	// Rewritten counts the rows whose type name changed from the runtime name
	// to the compiled one, per table ("smeldr_relations",
	// "smeldr_provenance", "smeldr_events", "smeldr_role_grants",
	// "smeldr_address_counters").
	Rewritten map[string]int `json:"rewritten"`
	// RetiredSchemas are the runtime schemas set to kind "retired": kept, no
	// longer loaded.
	RetiredSchemas []string `json:"retired_schemas"`
	// Root is the Root's address after the migration ("" in a dry run when
	// none exists yet), and RootEdges the Domains related to it.
	Root      string `json:"root,omitempty"`
	RootEdges int    `json:"root_edges"`
}

// PlaceMove is one runtime record copied to its compiled place type.
type PlaceMove struct {
	FromType string `json:"from_type"`
	Type     string `json:"type"`
	ID       string `json:"id"`
	Address  string `json:"address"`
}

// legacyPlaceRow is one runtime place record read for the migration.
type legacyPlaceRow struct {
	id, slug, status, fields, address, externalRef string
	addressNo                                      int64
	createdAt, updatedAt                           time.Time
}

// MigratePlaces moves an instance from the runtime domain, area and set types
// it defined before D142 to the compiled [Domain], [Area] and [Set], once:
//
//   - every runtime record is copied to the compiled table with the same ID,
//     slug, timestamps, address and number (its name and description from
//     its fields; an archived record stays archived, any other is active);
//   - relation ends, provenance subjects, event-log subjects, static grant
//     patterns ("domain:<id>") and the address counter change from the
//     runtime name to the compiled one; IDs do not change, so dynamic grants
//     anchored on a place keep working;
//   - the runtime schemas are set to kind "retired": kept, no longer loaded;
//   - Root and the default Domain are created ([App.EnsureRoot]) and every
//     Domain that does not yet belong to Root gets belongs_to_root.
//
// All of it but Root runs in one transaction. A second run moves nothing.
// DryRun reports and writes nothing. The runtime rows stay in
// smeldr_dynamic_content, so a rollback is the reverse rewrite plus kind
// "content" on the schemas (REFERENCE, "Places").
func (a *App) MigratePlaces(ctx context.Context, opts PlaceMigrationOptions) (*PlaceMigrationReport, error) {
	db := a.cfg.DB
	if db == nil {
		return nil, fmt.Errorf("%w: MigratePlaces needs a database", ErrInternal)
	}
	rep := &PlaceMigrationReport{DryRun: opts.DryRun, Moved: []PlaceMove{}, Rewritten: map[string]int{}, RetiredSchemas: []string{}}
	tables := map[string]bool{}
	for _, t := range []string{"smeldr_dynamic_content", "smeldr_content_type_schemas", "smeldr_relations", "smeldr_provenance", "smeldr_events", "smeldr_role_grants"} {
		ok, err := tableExists(ctx, db, t)
		if err != nil {
			return nil, fmt.Errorf("%w: MigratePlaces: %s", ErrInternal, err)
		}
		tables[t] = ok
	}
	legacyNames := make([]string, 0, len(legacyPlaceTypes))
	for compiled := range legacyPlaceTypes {
		legacyNames = append(legacyNames, compiled)
	}
	sort.Strings(legacyNames)
	plan := map[string][]legacyPlaceRow{}
	for _, compiled := range legacyNames {
		rows, err := a.legacyPlaceRows(ctx, db, tables, compiled)
		if err != nil {
			return nil, err
		}
		plan[compiled] = rows
	}
	work := func(exec DB) error {
		for _, compiled := range legacyNames {
			legacy := legacyPlaceTypes[compiled]
			for _, r := range plan[compiled] {
				if err := copyLegacyPlace(ctx, exec, compiled, r); err != nil {
					return err
				}
				rep.Moved = append(rep.Moved, PlaceMove{FromType: legacy, Type: compiled, ID: r.id, Address: r.address})
			}
			if err := rewritePlaceTypeName(ctx, exec, tables, rep, legacy, compiled); err != nil {
				return err
			}
		}
		return nil
	}
	if opts.DryRun {
		// Count in a transaction that is rolled back: the report is exact
		// and nothing stays written.
		errDry := errors.New("dry run")
		if err := runInTx(ctx, db, func(exec DB) error {
			if err := work(exec); err != nil {
				return err
			}
			return errDry
		}); err != nil && !errors.Is(err, errDry) {
			return nil, fmt.Errorf("%w: MigratePlaces: %s", ErrInternal, err)
		}
		if root, err := QueryOne[*Root](ctx, db, `SELECT * FROM smeldr_roots LIMIT 1`); err == nil {
			rep.Root = root.Address
		}
		return rep, nil
	}
	if err := runInTx(ctx, db, work); err != nil {
		return nil, fmt.Errorf("%w: MigratePlaces: %s", ErrInternal, err)
	}
	// The counters of place types the instance never defined (Root, and Set
	// on process.smeldr.dev) exist only now that no runtime type holds a
	// prefix.
	if err := registerCoreAddresses(ctx, db, compiledPlaceTypes...); err != nil {
		return nil, err
	}
	root, err := a.EnsureRoot(ctx, opts.RootName)
	if err != nil {
		return nil, err
	}
	rep.Root = root.Address
	if tables["smeldr_relations"] {
		n, err := a.relateDomainsToRoot(ctx, db, root.ID)
		if err != nil {
			return nil, err
		}
		rep.RootEdges = n
	}
	return rep, nil
}

// legacyPlaceRows reads the runtime records of compiled's runtime type that the
// compiled table does not hold yet. A record whose ID is already there was
// moved by an earlier run; one whose address is held by another compiled row
// is a conflict.
func (a *App) legacyPlaceRows(ctx context.Context, db DB, tables map[string]bool, compiled string) ([]legacyPlaceRow, error) {
	if !tables["smeldr_dynamic_content"] {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx,
		`SELECT id, slug, status, fields, address, address_no, external_ref, created_at, updated_at
		   FROM smeldr_dynamic_content WHERE type_name = $1 ORDER BY address_no, id`, legacyPlaceTypes[compiled])
	if err != nil {
		return nil, fmt.Errorf("%w: MigratePlaces: read %s: %s", ErrInternal, legacyPlaceTypes[compiled], err)
	}
	var all []legacyPlaceRow
	for rows.Next() {
		var r legacyPlaceRow
		if err := rows.Scan(&r.id, &r.slug, &r.status, &r.fields, &r.address, &r.addressNo, &r.externalRef,
			scanDest(&r.createdAt), scanDest(&r.updatedAt)); err != nil {
			rows.Close()
			return nil, fmt.Errorf("%w: MigratePlaces: read %s: %s", ErrInternal, legacyPlaceTypes[compiled], err)
		}
		all = append(all, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, fmt.Errorf("%w: MigratePlaces: read %s: %s", ErrInternal, legacyPlaceTypes[compiled], err)
	}
	table := placeTable(compiled)
	var todo []legacyPlaceRow
	for _, r := range all {
		var holder string
		err := db.QueryRowContext(ctx, `SELECT id FROM `+table+` WHERE id = $1 OR (address <> '' AND address = $2)`, r.id, r.address).Scan(&holder)
		switch {
		case err == nil && holder == r.id:
			continue // moved by an earlier run
		case err == nil:
			return nil, Err("address", fmt.Sprintf("%s %s is already held by %s %s: resolve it before migrating", legacyPlaceTypes[compiled], r.address, compiled, holder))
		case !errors.Is(err, sql.ErrNoRows):
			return nil, fmt.Errorf("%w: MigratePlaces: check %s: %s", ErrInternal, table, err)
		}
		todo = append(todo, r)
	}
	return todo, nil
}

// placeTable is the table of a compiled place type.
func placeTable(typeName string) string {
	for _, t := range coreAddressTables {
		if t.typeName == typeName {
			return t.table
		}
	}
	return ""
}

// copyLegacyPlace inserts r into compiled's table.
func copyLegacyPlace(ctx context.Context, exec DB, compiled string, r legacyPlaceRow) error {
	var fields struct {
		Name        string `json:"name"`
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	_ = json.Unmarshal([]byte(r.fields), &fields)
	name := fields.Name
	if name == "" {
		name = fields.Title
	}
	if name == "" {
		name = r.slug
	}
	status := "active"
	if r.status == string(Archived) || r.status == "archived" {
		status = "archived"
	}
	_, err := exec.ExecContext(ctx,
		`INSERT INTO `+placeTable(compiled)+` (id, slug, status, created_at, updated_at, rev, name, description, last_actor, address, address_no, external_ref)
		 VALUES ($1, $2, $3, $4, $5, 0, $6, $7, '', $8, $9, $10)`,
		r.id, r.slug, status, r.createdAt, r.updatedAt, name, fields.Description, r.address, r.addressNo, r.externalRef)
	if err != nil {
		return fmt.Errorf("copy %s %s: %w", compiled, r.id, err)
	}
	return nil
}

// rewritePlaceTypeName changes legacy to compiled wherever a type name is
// stored, and retires legacy's schema.
func rewritePlaceTypeName(ctx context.Context, exec DB, tables map[string]bool, rep *PlaceMigrationReport, legacy, compiled string) error {
	type rewrite struct{ table, query string }
	var steps []rewrite
	if tables["smeldr_relations"] {
		steps = append(steps,
			rewrite{"smeldr_relations", `UPDATE smeldr_relations SET target_type = $1 WHERE target_type = $2`},
			rewrite{"smeldr_relations", `UPDATE smeldr_relations SET source_type = $1 WHERE source_type = $2`})
	}
	if tables["smeldr_provenance"] {
		steps = append(steps, rewrite{"smeldr_provenance", `UPDATE smeldr_provenance SET subject_type = $1 WHERE subject_type = $2`})
	}
	if tables["smeldr_events"] {
		steps = append(steps,
			rewrite{"smeldr_events", `UPDATE smeldr_events SET subject_type = $1 WHERE subject_type = $2`},
			rewrite{"smeldr_events", `UPDATE smeldr_events SET other_type = $1 WHERE other_type = $2`})
	}
	if tables["smeldr_role_grants"] {
		steps = append(steps, rewrite{"smeldr_role_grants",
			`UPDATE smeldr_role_grants SET scope_static = REPLACE(scope_static, '"' || $2 || ':', '"' || $1 || ':') WHERE scope_static <> REPLACE(scope_static, '"' || $2 || ':', '"' || $1 || ':')`})
	}
	for _, st := range steps {
		res, err := exec.ExecContext(ctx, st.query, compiled, legacy)
		if err != nil {
			return fmt.Errorf("rewrite %s in %s: %w", legacy, st.table, err)
		}
		n, _ := res.RowsAffected()
		rep.Rewritten[st.table] += int(n)
	}
	// The counter: the runtime type's row becomes the compiled type's, so the
	// next number follows the last one. A compiled row with no address yet
	// gives way.
	var legacyNo int64
	err := exec.QueryRowContext(ctx, `SELECT last_no FROM smeldr_address_counters WHERE type_name = $1`, legacy).Scan(&legacyNo)
	switch {
	case err == nil:
		if _, err := exec.ExecContext(ctx, `DELETE FROM smeldr_address_counters WHERE type_name = $1 AND last_no = 0`, compiled); err != nil {
			return fmt.Errorf("counter of %s: %w", compiled, err)
		}
		if _, err := exec.ExecContext(ctx, `UPDATE smeldr_address_counters SET type_name = $1 WHERE type_name = $2`, compiled, legacy); err != nil {
			return fmt.Errorf("counter of %s: %w", legacy, err)
		}
		rep.Rewritten["smeldr_address_counters"]++
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("counter of %s: %w", legacy, err)
	}
	if tables["smeldr_content_type_schemas"] {
		res, err := exec.ExecContext(ctx, `UPDATE smeldr_content_type_schemas SET kind = 'retired' WHERE type_name = $1 AND kind = 'content'`, legacy)
		if err != nil {
			return fmt.Errorf("retire schema %s: %w", legacy, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			rep.RetiredSchemas = append(rep.RetiredSchemas, legacy)
		}
	}
	return nil
}

// relateDomainsToRoot gives every Domain that does not belong to Root its
// belongs_to_root edge, and returns how many it added.
func (a *App) relateDomainsToRoot(ctx context.Context, db DB, rootID string) (int, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT d.id FROM smeldr_domains d WHERE NOT EXISTS (
		   SELECT 1 FROM smeldr_relations r WHERE r.source_id = d.id AND r.target_id = $1
		     AND r.relation_kind = 'belongs_to_root' AND r.invalid_at IS NULL)`, rootID)
	if err != nil {
		return 0, fmt.Errorf("%w: MigratePlaces: read Domains: %s", ErrInternal, err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, fmt.Errorf("%w: MigratePlaces: read Domains: %s", ErrInternal, err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, fmt.Errorf("%w: MigratePlaces: read Domains: %s", ErrInternal, err)
	}
	job := placesSetupActor
	for _, id := range ids {
		edge, _, err := writeEdgeRow(ctx, db, RelationEdge{
			SourceType: "Domain", SourceID: id, TargetType: "Root", TargetID: rootID,
			RelationKind: "belongs_to_root", EdgeClass: "asserted", CreatedByJob: &job, Attributes: json.RawMessage("{}"),
		})
		if err != nil {
			return 0, fmt.Errorf("%w: MigratePlaces: relate Domain %s to Root: %s", ErrInternal, id, err)
		}
		if a.relationStore != nil {
			a.relationStore.announceEdge(ctx, edge, true)
		}
	}
	return len(ids), nil
}
