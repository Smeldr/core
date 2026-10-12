// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// AddressMigrationOptions configures [MigrateAddresses].
type AddressMigrationOptions struct {
	// DryRun runs the whole migration and rolls it back, so the report shows
	// exactly what an apply would do and nothing is written.
	DryRun bool
}

// AddressMigrationReport is what [MigrateAddresses] did, or in a dry run what
// it would do. It carries names and addresses only, never body text.
type AddressMigrationReport struct {
	DryRun bool `json:"dry_run"`
	// DurationMS is how long the migration's one transaction was open: the
	// time the database's writer was held (SQLite has one).
	DurationMS int64               `json:"duration_ms"`
	Types      []TypeAddressReport `json:"types"`
	Rewritten  []AddressRefRewrite `json:"rewritten"`
	LeftAlone  []AddressRefNote    `json:"left_alone"`
}

// TypeAddressReport is one type's part of an [AddressMigrationReport].
type TypeAddressReport struct {
	TypeName string `json:"type_name"`
	Prefix   string `json:"prefix"`
	// Kept counts rows whose legacy human ID became their address unchanged.
	Kept int `json:"kept"`
	// Renumbered counts rows of a type with a legacy field that got a new
	// number (their old value, if any, is in external_ref).
	Renumbered int `json:"renumbered"`
	// NewlyNumbered counts rows of a type without a legacy field that got a
	// number.
	NewlyNumbered int               `json:"newly_numbered"`
	Before        AddressCounts     `json:"before"`
	After         AddressCounts     `json:"after"`
	Renumbers     []AddressRenumber `json:"renumbers"`
	Error         string            `json:"error,omitempty"`
}

// AddressCounts counts a type's rows with and without an address.
type AddressCounts struct {
	WithAddress    int `json:"with_address"`
	WithoutAddress int `json:"without_address"`
}

// AddressRenumber is one row that got a number other than its legacy value.
type AddressRenumber struct {
	ID  string `json:"id"`
	Old string `json:"old"`
	New string `json:"new"`
}

// AddressRefRewrite is one stored reference the migration rewrote.
type AddressRefRewrite struct {
	Table  string `json:"table"`
	Column string `json:"column"`
	ID     string `json:"id"`
	Old    string `json:"old"`
	New    string `json:"new"`
}

// AddressRefNote is one stored value that looked like a reference to a
// renumbered record and was left alone, with the reason.
type AddressRefNote struct {
	Table  string `json:"table"`
	Column string `json:"column"`
	ID     string `json:"id"`
	Value  string `json:"value"`
	Reason string `json:"reason"`
}

// migrationType is one type the migration numbers.
type migrationType struct {
	typeName, table, legacy string
	dynamic                 bool
}

// MigrateAddresses gives every record that has no address yet its address
// (D110 design section 10), per type, in created_at order, in one transaction:
//   - a type with a legacy human-ID field keeps a legacy value that parses with
//     the type's prefix and is unique; every other row gets a number through
//     the counter, above every number taken so far, and its old value moves
//     to external_ref; the legacy field is set to the address;
//   - a type without one (Signal, Run, a dynamic record type) numbers its rows
//     1..N from the range reserved for them at registration, then through the
//     counter;
//   - a stored reference to a renumbered record is rewritten where it names
//     exactly one (Amendment.amends through its amends edge, Signal.task_ref,
//     Signal.subject_id, Task.note_ref); anything ambiguous is reported.
//
// It is idempotent: a row with an address is skipped. A dry run runs the same
// code and rolls back. The tables it probes are checked on db before the
// transaction opens.
func MigrateAddresses(ctx context.Context, db DB, opts AddressMigrationOptions) (*AddressMigrationReport, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: MigrateAddresses needs a database", ErrBadRequest)
	}
	types, err := migrationTypes(ctx, db)
	if err != nil {
		return nil, err
	}
	hasRelations, err := tableExists(ctx, db, "smeldr_relations")
	if err != nil {
		return nil, fmt.Errorf("%w: MigrateAddresses: %s", ErrInternal, err)
	}

	start := time.Now()
	exec, commit, rollback, err := conflictTx(ctx, db)
	if err != nil {
		return nil, err
	}
	defer rollback()

	rep := &AddressMigrationReport{DryRun: opts.DryRun, Rewritten: []AddressRefRewrite{}, LeftAlone: []AddressRefNote{}}
	renumbered := map[string]string{}  // record ID -> new address
	oldValues := map[string][]string{} // old legacy value -> new addresses
	for _, t := range types {
		tr, err := migrateType(ctx, exec, t, renumbered, oldValues)
		if err != nil {
			return nil, err
		}
		rep.Types = append(rep.Types, tr)
	}
	if err := rewriteAddressRefs(ctx, exec, rep, renumbered, oldValues, hasRelations); err != nil {
		return nil, err
	}
	if opts.DryRun {
		rollback()
	} else if err := commit(); err != nil {
		return nil, fmt.Errorf("%w: MigrateAddresses commit: %s", ErrInternal, err)
	}
	rep.DurationMS = time.Since(start).Milliseconds()
	return rep, nil
}

// MigrateAddresses runs [MigrateAddresses] on the App's database.
func (a *App) MigrateAddresses(ctx context.Context, opts AddressMigrationOptions) (*AddressMigrationReport, error) {
	return MigrateAddresses(ctx, a.cfg.DB, opts)
}

// migrationTypes lists the existing orchestration tables, then each dynamic
// record type (no url_prefix), read on the base handle.
func migrationTypes(ctx context.Context, db DB) ([]migrationType, error) {
	var out []migrationType
	for _, t := range orchestrationAddressTables {
		ok, err := tableExists(ctx, db, t.table)
		if err != nil {
			return nil, fmt.Errorf("%w: MigrateAddresses: %s", ErrInternal, err)
		}
		if ok {
			out = append(out, migrationType{typeName: t.typeName, table: t.table, legacy: t.legacy})
		}
	}
	schemas, err := tableExists(ctx, db, "smeldr_content_type_schemas")
	if err != nil {
		return nil, fmt.Errorf("%w: MigrateAddresses: %s", ErrInternal, err)
	}
	content, err := tableExists(ctx, db, "smeldr_dynamic_content")
	if err != nil {
		return nil, fmt.Errorf("%w: MigrateAddresses: %s", ErrInternal, err)
	}
	if !schemas || !content {
		return out, nil
	}
	rows, err := db.QueryContext(ctx,
		`SELECT type_name FROM smeldr_content_type_schemas WHERE kind = 'content' AND url_prefix = '' ORDER BY type_name`)
	if err != nil {
		return nil, fmt.Errorf("%w: MigrateAddresses: read record types: %s", ErrInternal, err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("%w: MigrateAddresses: read record types: %s", ErrInternal, err)
		}
		out = append(out, migrationType{typeName: name, table: "smeldr_dynamic_content", dynamic: true})
	}
	return out, rows.Err()
}

// typeScope is the WHERE clause narrowing t's table to its own rows, with
// its argument.
func (t migrationType) scope() (string, []any) {
	if t.dynamic {
		return " AND type_name = $1 AND slug <> ''", []any{t.typeName}
	}
	return "", nil
}

// addressCounts counts t's rows with and without an address.
func addressCounts(ctx context.Context, exec DB, t migrationType) (AddressCounts, error) {
	where, args := t.scope()
	var c AddressCounts
	if err := exec.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+t.table+` WHERE address <> ''`+where, args...).Scan(&c.WithAddress); err != nil {
		return c, err
	}
	err := exec.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+t.table+` WHERE address = ''`+where, args...).Scan(&c.WithoutAddress)
	return c, err
}

// migrateType numbers one type's unaddressed rows and reports it. A type with
// no registered prefix is reported with an error and skipped.
func migrateType(ctx context.Context, exec DB, t migrationType, renumbered map[string]string, oldValues map[string][]string) (TypeAddressReport, error) {
	tr := TypeAddressReport{TypeName: t.typeName, Renumbers: []AddressRenumber{}}
	fail := func(step string, err error) (TypeAddressReport, error) {
		return tr, fmt.Errorf("%w: MigrateAddresses %s: %s: %s", ErrInternal, t.typeName, step, err)
	}
	var prefix string
	var reserved int64
	err := exec.QueryRowContext(ctx, `SELECT prefix, reserved_no FROM smeldr_address_counters WHERE type_name = $1`, t.typeName).Scan(&prefix, &reserved)
	if errors.Is(err, sql.ErrNoRows) {
		tr.Error = "the type has no address prefix registered; it was skipped"
		return tr, nil
	}
	if err != nil {
		return fail("read prefix", err)
	}
	tr.Prefix = prefix
	if tr.Before, err = addressCounts(ctx, exec, t); err != nil {
		return fail("count", err)
	}

	where, args := t.scope()
	taken := map[string]bool{}
	addrs, err := exec.QueryContext(ctx, `SELECT address FROM `+t.table+` WHERE address <> ''`+where, args...)
	if err != nil {
		return fail("read addresses", err)
	}
	for addrs.Next() {
		var a string
		if err := addrs.Scan(&a); err != nil {
			addrs.Close()
			return fail("read addresses", err)
		}
		taken[a] = true
	}
	addrs.Close()

	legacyCol := t.legacy
	if legacyCol == "" {
		legacyCol = "''"
	}
	legacyCount := map[string]int{}
	if t.legacy != "" {
		vals, err := exec.QueryContext(ctx, `SELECT `+t.legacy+` FROM `+t.table)
		if err != nil {
			return fail("read legacy values", err)
		}
		for vals.Next() {
			var v string
			if err := vals.Scan(&v); err != nil {
				vals.Close()
				return fail("read legacy values", err)
			}
			legacyCount[strings.TrimSpace(v)]++
		}
		vals.Close()
	}

	type pending struct{ id, legacy, externalRef string }
	var todo []pending
	rows, err := exec.QueryContext(ctx,
		`SELECT id, `+legacyCol+`, external_ref FROM `+t.table+` WHERE address = ''`+where+` ORDER BY created_at, id`, args...)
	if err != nil {
		return fail("read rows", err)
	}
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.legacy, &p.externalRef); err != nil {
			rows.Close()
			return fail("read rows", err)
		}
		p.legacy = strings.TrimSpace(p.legacy)
		todo = append(todo, p)
	}
	rows.Close()

	var nextReserved int64 = 1
	for _, p := range todo {
		var address string
		var no int64
		kept := false
		switch {
		case t.legacy != "":
			if a, ok := addressIdent(p.legacy, prefix); ok && legacyCount[p.legacy] == 1 && !taken[a] {
				address, kept = a, true
				_, no, _ = ParseAddress(a)
			}
		default:
			for nextReserved <= reserved && taken[prefix+strconv.FormatInt(nextReserved, 10)] {
				nextReserved++
			}
			if nextReserved <= reserved {
				no, address = nextReserved, prefix+strconv.FormatInt(nextReserved, 10)
				nextReserved++
			}
		}
		if address == "" {
			if address, no, err = allocateAddress(ctx, exec, t.typeName); err != nil {
				return tr, err
			}
		}
		taken[address] = true
		externalRef := p.externalRef
		if !kept && p.legacy != "" && externalRef == "" {
			externalRef = p.legacy
		}
		set := `address = $1, address_no = $2, external_ref = $3`
		upArgs := []any{address, no, externalRef}
		if t.legacy != "" {
			set += `, ` + t.legacy + ` = $1`
		}
		upArgs = append(upArgs, p.id)
		if _, err := exec.ExecContext(ctx, `UPDATE `+t.table+` SET `+set+` WHERE id = $4`, upArgs...); err != nil {
			return fail("write address", err)
		}
		switch {
		case kept:
			tr.Kept++
		case t.legacy != "":
			tr.Renumbered++
			tr.Renumbers = append(tr.Renumbers, AddressRenumber{ID: p.id, Old: p.legacy, New: address})
			renumbered[p.id] = address
			if p.legacy != "" {
				oldValues[p.legacy] = append(oldValues[p.legacy], address)
			}
		default:
			tr.NewlyNumbered++
		}
	}
	if reserved > 0 {
		if _, err := exec.ExecContext(ctx, `UPDATE smeldr_address_counters SET reserved_no = 0 WHERE type_name = $1`, t.typeName); err != nil {
			return fail("release the reserve", err)
		}
	}
	if tr.After, err = addressCounts(ctx, exec, t); err != nil {
		return fail("count", err)
	}
	return tr, nil
}

// rewriteAddressRefs rewrites the stored references to renumbered records.
func rewriteAddressRefs(ctx context.Context, exec DB, rep *AddressMigrationReport, renumbered map[string]string, oldValues map[string][]string, hasRelations bool) error {
	fail := func(step string, err error) error {
		return fmt.Errorf("%w: MigrateAddresses references: %s: %s", ErrInternal, step, err)
	}
	// Amendment.amends, through its live amends edge (the raw Decision ID).
	if ok := hasTable(rep, "Amendment") && hasTable(rep, "Decision"); ok {
		type ref struct{ id, amends, target string }
		var refs []ref
		rows, err := exec.QueryContext(ctx, `SELECT id, amends FROM smeldr_amendments WHERE amends <> ''`)
		if err != nil {
			return fail("read amends", err)
		}
		for rows.Next() {
			var r ref
			if err := rows.Scan(&r.id, &r.amends); err != nil {
				rows.Close()
				return fail("read amends", err)
			}
			refs = append(refs, r)
		}
		rows.Close()
		for i := range refs {
			if !hasRelations {
				continue
			}
			err := exec.QueryRowContext(ctx,
				`SELECT target_id FROM smeldr_relations WHERE source_type = 'Amendment' AND source_id = $1 AND relation_kind = 'amends'
				 AND target_type = 'Decision' AND (invalid_at IS NULL OR invalid_at > $2) ORDER BY created_at LIMIT 1`,
				refs[i].id, time.Now().UTC()).Scan(&refs[i].target)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return fail("read amends edge", err)
			}
		}
		for _, r := range refs {
			if r.target == "" {
				rep.LeftAlone = append(rep.LeftAlone, AddressRefNote{Table: "smeldr_amendments", Column: "amends", ID: r.id, Value: r.amends,
					Reason: "no live amends edge names the Decision, so it is not rewritten"})
				continue
			}
			if next, ok := renumbered[r.target]; ok && next != r.amends {
				if _, err := exec.ExecContext(ctx, `UPDATE smeldr_amendments SET amends = $1 WHERE id = $2`, next, r.id); err != nil {
					return fail("rewrite amends", err)
				}
				rep.Rewritten = append(rep.Rewritten, AddressRefRewrite{Table: "smeldr_amendments", Column: "amends", ID: r.id, Old: r.amends, New: next})
			}
		}
	}
	// Text references that equal a renumbered record's old value.
	for _, c := range []struct{ typeName, table, column string }{
		{"Signal", "smeldr_signals", "task_ref"},
		{"Signal", "smeldr_signals", "subject_id"},
		{"Task", "smeldr_tasks", "note_ref"},
	} {
		if !hasTable(rep, c.typeName) {
			continue
		}
		type ref struct{ id, value string }
		var refs []ref
		rows, err := exec.QueryContext(ctx, `SELECT id, `+c.column+` FROM `+c.table+` WHERE `+c.column+` <> ''`)
		if err != nil {
			return fail("read "+c.column, err)
		}
		for rows.Next() {
			var r ref
			if err := rows.Scan(&r.id, &r.value); err != nil {
				rows.Close()
				return fail("read "+c.column, err)
			}
			refs = append(refs, r)
		}
		rows.Close()
		for _, r := range refs {
			news := oldValues[strings.TrimSpace(r.value)]
			switch len(news) {
			case 0:
				continue
			case 1:
				if _, err := exec.ExecContext(ctx, `UPDATE `+c.table+` SET `+c.column+` = $1 WHERE id = $2`, news[0], r.id); err != nil {
					return fail("rewrite "+c.column, err)
				}
				rep.Rewritten = append(rep.Rewritten, AddressRefRewrite{Table: c.table, Column: c.column, ID: r.id, Old: r.value, New: news[0]})
			default:
				rep.LeftAlone = append(rep.LeftAlone, AddressRefNote{Table: c.table, Column: c.column, ID: r.id, Value: r.value,
					Reason: fmt.Sprintf("the old value was held by %d renumbered records (%s), so it names none", len(news), strings.Join(news, ", "))})
			}
		}
	}
	return nil
}

// hasTable reports whether the migration covered typeName.
func hasTable(rep *AddressMigrationReport, typeName string) bool {
	for _, t := range rep.Types {
		if t.TypeName == typeName && t.Error == "" {
			return true
		}
	}
	return false
}
