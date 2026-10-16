// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// References between records are relations the system creates and checks
// (D113). A reference field names another record by address, slug or ID; a
// write that sets it resolves the name under the writer's read scope before its
// transaction, stores the target's address (a Signal's subject_id: its ID), and
// writes the edge row in the same transaction as the record, so the record and
// its edge commit together. The edge is announced (provenance, the relation
// event) after the commit. Every reference field is write-once.

// referencesJob is the created_by_job of every edge the system asserts for a
// reference field, so provenance shows the edge as system-made while the
// writer stays recorded in created_by.
const referencesJob = "references"

// referenceField is one reference field of a compiled type.
type referenceField struct {
	// name is the field's JSON name, used in errors.
	name string
	// value returns the field on item, which is always of the field's type.
	value func(item any) *string
	// targets returns the types the value may name, tried in order.
	targets func(item any) []string
	// byID stores the target's ID rather than its address.
	byID bool
	// edge builds the edge for item and its resolved target.
	edge func(item any, targetType, targetID string) RelationEdge
	// refusal, when set, is the guided message for a value that names no
	// readable target; the default names the type and the three ways to name it.
	refusal func(value string) string
}

// taskReferences are [Task]'s reference fields.
func taskReferences() []referenceField {
	return []referenceField{{
		name:    "implements",
		value:   func(item any) *string { return &item.(*Task).Implements },
		targets: func(any) []string { return []string{"Decision", "Goal"} },
		edge: func(item any, targetType, targetID string) RelationEdge {
			kind := "implements"
			if targetType == "Goal" {
				kind = "derives_from"
			}
			return RelationEdge{SourceType: "Task", SourceID: item.(*Task).ID, TargetType: targetType, TargetID: targetID, RelationKind: kind}
		},
		refusal: func(v string) string {
			return fmt.Sprintf("no Decision or Goal %q you can read; name the one this Task implements by address (D113, G12), slug or ID", v)
		},
	}}
}

// amendmentReferences are [Amendment]'s reference fields.
func amendmentReferences() []referenceField {
	return []referenceField{{
		name:    "task_ref",
		value:   func(item any) *string { return &item.(*Amendment).TaskRef },
		targets: func(any) []string { return []string{"Task"} },
		edge: func(item any, _, targetID string) RelationEdge {
			return RelationEdge{SourceType: "Task", SourceID: targetID, TargetType: "Amendment", TargetID: item.(*Amendment).ID, RelationKind: "ships_as"}
		},
	}}
}

// signalTaskRefRefusal is the guided refusal of a Signal task_ref that names
// no readable Task. Agents send Signals all day, so it says what to send
// instead (task_plan item-c087cdf5).
func signalTaskRefRefusal(v string) string {
	return fmt.Sprintf("no Task %q you can read; send a Task's address (T273), slug or ID in task_ref, and name any other record in subject_type and subject_id", v)
}

// signalReferences are [Signal]'s reference fields: its subject and its Task.
func signalReferences() []referenceField {
	signal := func(item any) *Signal { return item.(*Signal) }
	about := func(field string) func(item any, targetType, targetID string) RelationEdge {
		return func(item any, targetType, targetID string) RelationEdge {
			return RelationEdge{SourceType: "Signal", SourceID: signal(item).ID, TargetType: targetType, TargetID: targetID,
				RelationKind: "about", Attributes: json.RawMessage(`{"field":"` + field + `"}`)}
		}
	}
	return []referenceField{{
		name:    "subject_id",
		value:   func(item any) *string { return &signal(item).SubjectID },
		targets: func(item any) []string { return []string{signal(item).SubjectType} },
		byID:    true,
		edge:    about("subject_id"),
	}, {
		name:    "task_ref",
		value:   func(item any) *string { return &signal(item).TaskRef },
		targets: func(any) []string { return []string{"Task"} },
		edge:    about("task_ref"),
		refusal: signalTaskRefRefusal,
	}}
}

// checkSignalSubjectPair refuses a Signal that names half a subject.
func checkSignalSubjectPair(s *Signal) error {
	switch {
	case s.SubjectType != "" && strings.TrimSpace(s.SubjectID) == "":
		return Err("subject_id", "is required with subject_type: name the record the Signal is about by address, slug or ID")
	case s.SubjectType == "" && strings.TrimSpace(s.SubjectID) != "":
		return Err("subject_type", "is required with subject_id: name the type of the record the Signal is about")
	}
	return nil
}

// referenceTarget is a record a reference names.
type referenceTarget struct{ Type, ID, Address string }

// stored is the form a reference field keeps: the address, or the ID when
// byID is set or the record has no address.
func (t referenceTarget) stored(byID bool) string {
	if byID || t.Address == "" {
		return t.ID
	}
	return t.Address
}

// referenceSaveHooks returns the hooks that check and relate fields on a
// compiled type: prepare (before the transaction, under the writer's read
// scope), write (in the transaction) and announce (after the commit). base, when
// not nil, is the type's own hooks (it has no prepare step); its before and
// after run first.
func (a *App) referenceSaveHooks(fields []referenceField, base *saveHooksOption) saveHooksOption {
	h := saveHooksOption{
		prepare: func(ctx Context, existing, item any) error {
			if s, ok := item.(*Signal); ok {
				if err := checkSignalSubjectPair(s); err != nil {
					return err
				}
			}
			return a.prepareReferences(ctx, fields, existing, item)
		},
		before: func(ctx Context, db DB, existing, item any) error {
			if base != nil && base.before != nil {
				if err := base.before(ctx, db, existing, item); err != nil {
					return err
				}
			}
			return a.writeReferenceEdges(ctx, db, fields, existing, item)
		},
		after: func(ctx Context, db DB, rs *RelationStore, existing, item any) {
			if base != nil && base.after != nil {
				base.after(ctx, db, rs, existing, item)
			}
			a.announceReferenceEdges(ctx, fields, existing, item)
		},
	}
	if base != nil {
		h.statusChanged = base.statusChanged
	}
	return h
}

// newReferences returns the reference fields item sets for the first time (an
// empty old value) with their current values, skipping unchanged ones.
func newReferences(fields []referenceField, existing, item any) []referenceField {
	var out []referenceField
	for _, f := range fields {
		if *f.value(item) == "" || existing != nil && *f.value(existing) != "" {
			continue
		}
		out = append(out, f)
	}
	return out
}

// prepareReferences resolves each reference field item sets under the caller's
// read scope and writes it back in its stored form. A field already set on
// existing is write-once: the same target (by any of its names) is kept, a
// different one or an empty value is refused. A name that resolves to nothing
// the caller can read is refused with one message, whether it is missing or
// hidden.
func (a *App) prepareReferences(ctx context.Context, fields []referenceField, existing, item any) error {
	for _, f := range fields {
		p := f.value(item)
		var old string
		if existing != nil {
			old = *f.value(existing)
		}
		v := strings.TrimSpace(*p)
		if v == "" {
			if old != "" {
				return Err(f.name, fmt.Sprintf("is write-once: it names %s and cannot be cleared; correct a wrong reference with the relation tools", old))
			}
			*p = ""
			continue
		}
		if v == old {
			continue
		}
		t, err := a.resolveReference(ctx, f.targets(item), v)
		if err != nil {
			return f.refuse(v, f.targets(item), err)
		}
		stored := t.stored(f.byID)
		if old != "" && stored != old {
			return Err(f.name, fmt.Sprintf("is write-once: it names %s; correct a wrong reference with the relation tools", old))
		}
		*p = stored
	}
	return nil
}

// refuse turns a resolution failure into the field's guided refusal. Not found
// (missing or hidden) and an unregistered type are the caller's to correct;
// anything else is returned unchanged.
func (f referenceField) refuse(v string, types []string, err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		if f.refusal != nil {
			return Err(f.name, f.refusal(v))
		}
		return Err(f.name, fmt.Sprintf("no %s %q you can read; name it by address, slug or ID", strings.Join(types, " or "), v))
	case errors.Is(err, ErrBadRequest):
		if f.name == "subject_id" {
			return Err("subject_type", "names no registered content type")
		}
		return Err(f.name, err.Error())
	}
	return err
}

// liveEdge returns the stored live row of edge's tuple.
func liveEdge(ctx context.Context, db DB, edge RelationEdge) (RelationEdge, error) {
	err := db.QueryRowContext(ctx,
		`SELECT id, created_at FROM smeldr_relations WHERE source_type=$1 AND source_id=$2 `+
			`AND target_type=$3 AND target_id=$4 AND relation_kind=$5 AND edge_class=$6 AND invalid_at IS NULL`,
		edge.SourceType, edge.SourceID, edge.TargetType, edge.TargetID, edge.RelationKind, edge.EdgeClass,
	).Scan(&edge.ID, scanDest(&edge.CreatedAt))
	if errors.Is(err, sql.ErrNoRows) {
		return edge, ErrNotFound
	}
	return edge, err
}

// recordTable is where a type's records live, read on a base handle (table
// checks cannot run inside a transaction) so a lookup inside one can use it.
type recordTable struct {
	typeName string
	name     string // "" when the type has no table: every lookup misses
	dynamic  bool   // the shared smeldr_dynamic_content table, filtered by type_name
	address  bool   // the table has an address column
	prefix   string // the type's address prefix
}

// recordTableOf reads typeName's table facts on db, which must be a base
// handle, not a transaction.
func recordTableOf(ctx context.Context, db DB, typeName string) (recordTable, error) {
	rt := recordTable{typeName: typeName, prefix: coreAddressPrefixes[typeName]}
	if db == nil || typeName == "" {
		return rt, nil
	}
	rt.name = resolveItemTable(ctx, db, typeName)
	if rt.name == "smeldr_dynamic_content" {
		rt.dynamic = true
		ok, err := tableExists(ctx, db, rt.name)
		if err != nil {
			return rt, fmt.Errorf("%w: lookup %s: %s", ErrInternal, typeName, err)
		}
		if !ok {
			rt.name = ""
			return rt, nil
		}
		if rt.prefix, err = addressPrefixOf(ctx, db, typeName); err != nil {
			return rt, err
		}
	}
	ok, err := columnExists(ctx, db, rt.name, "address")
	if err != nil {
		return rt, fmt.Errorf("%w: lookup %s: %s", ErrInternal, typeName, err)
	}
	rt.address = ok
	return rt, nil
}

// lookup finds the record of rt that ident names on db (any handle, a
// transaction included), without a read scope (the caller has checked it, or
// the write is the system's): its address when ident carries the type's
// prefix, then its slug, then its ID, then the legacy number. It returns the
// record's ID and address. ErrNotFound when nothing matches; a slug held by
// several records is a ValidationError.
func (rt recordTable) lookup(ctx context.Context, db DB, ident string) (id, address string, err error) {
	ident = strings.TrimSpace(ident)
	if db == nil || rt.name == "" || ident == "" {
		return "", "", ErrNotFound
	}
	where, args := "", []any{}
	if rt.dynamic {
		where, args = " AND type_name = $2", []any{rt.typeName}
	}
	addressCol := "''"
	if rt.address {
		addressCol = "COALESCE(address, '')"
	}
	var candidates [][2]string
	if p, no, ok := ParseAddress(ident); ok && rt.address && rt.prefix != "" && p == rt.prefix {
		candidates = append(candidates, [2]string{"address", fmt.Sprintf("%s%d", p, no)})
	}
	candidates = append(candidates, [2]string{"slug", ident}, [2]string{"id", ident})
	for _, c := range candidates {
		rows, err := db.QueryContext(ctx, "SELECT id, "+addressCol+" FROM "+quoteIdent(rt.name)+" WHERE "+quoteIdent(c[0])+" = $1"+where+" LIMIT 2",
			append([]any{c[1]}, args...)...)
		if err != nil {
			return "", "", fmt.Errorf("%w: lookup %s: %s", ErrInternal, rt.typeName, err)
		}
		var hits [][2]string
		for rows.Next() {
			var h [2]string
			if err := rows.Scan(&h[0], &h[1]); err != nil {
				rows.Close()
				return "", "", fmt.Errorf("%w: lookup %s: %s", ErrInternal, rt.typeName, err)
			}
			hits = append(hits, h)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return "", "", fmt.Errorf("%w: lookup %s: %s", ErrInternal, rt.typeName, err)
		}
		switch len(hits) {
		case 1:
			return hits[0][0], hits[0][1], nil
		case 2:
			return "", "", Err(c[0], fmt.Sprintf("%q names more than one %s; name it by its address or ID", ident, rt.typeName))
		}
	}
	if rt.dynamic {
		return "", "", ErrNotFound
	}
	legacyID, err := legacyLookupID(ctx, db, rt.typeName, quoteIdent(rt.name), ident, nil)
	if err != nil {
		return "", "", err
	}
	return rt.lookup(ctx, db, legacyID)
}

// referenceTables holds the table facts of a write's reference targets, read
// before its transaction.
type referenceTables map[string]recordTable

// referenceTablesFor reads the tables of every type fields may name on item,
// on db (a base handle).
func referenceTablesFor(ctx context.Context, db DB, fields []referenceField, item any) (referenceTables, error) {
	out := referenceTables{}
	for _, f := range fields {
		if *f.value(item) == "" {
			continue
		}
		for _, typeName := range f.targets(item) {
			if _, ok := out[typeName]; ok {
				continue
			}
			rt, err := recordTableOf(ctx, db, typeName)
			if err != nil {
				return nil, err
			}
			out[typeName] = rt
		}
	}
	return out, nil
}

// resolveReference resolves ident to the first of types it names that the
// caller may read ([App.ResolveReadableID]), with that record's address.
// ErrNotFound when no type has it; ErrBadRequest when a type is not
// registered; ErrInternal for a store failure.
func (a *App) resolveReference(ctx context.Context, types []string, ident string) (referenceTarget, error) {
	for _, typeName := range types {
		id, err := a.ResolveReadableID(ctx, typeName, ident)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return referenceTarget{}, err
		}
		rt, err := a.referenceTable(ctx, typeName)
		if err != nil {
			return referenceTarget{}, err
		}
		_, address, err := rt.lookup(ctx, a.cfg.DB, id)
		if err != nil {
			return referenceTarget{}, err
		}
		return referenceTarget{Type: typeName, ID: id, Address: address}, nil
	}
	return referenceTarget{}, ErrNotFound
}

// referenceTable returns typeName's table facts, read once on the App's base
// handle and kept: a write reads them before its transaction, so the lookup
// inside the transaction ([App.writeReferenceEdges]) needs no table check.
func (a *App) referenceTable(ctx context.Context, typeName string) (recordTable, error) {
	if v, ok := a.referenceTables.Load(typeName); ok {
		return v.(recordTable), nil
	}
	rt, err := recordTableOf(ctx, a.cfg.DB, typeName)
	if err != nil {
		return rt, err
	}
	if rt.name != "" {
		a.referenceTables.Store(typeName, rt)
	}
	return rt, nil
}

// cachedTables returns the kept table facts of every type fields may name on
// item. [App.prepareReferences] has read them for every field it resolved.
func (a *App) cachedTables(fields []referenceField, item any) referenceTables {
	out := referenceTables{}
	for _, f := range fields {
		if *f.value(item) == "" {
			continue
		}
		for _, typeName := range f.targets(item) {
			if v, ok := a.referenceTables.Load(typeName); ok {
				out[typeName] = v.(recordTable)
			}
		}
	}
	return out
}

// writeReferenceEdges runs in the write's transaction: for each reference item
// sets for the first time it reads the stored target again on db, so a target
// deleted since [App.prepareReferences] refuses the write, and writes the
// edge row there. Without a relation store ([App.Relations]) it writes no edge.
func (a *App) writeReferenceEdges(ctx context.Context, db DB, fields []referenceField, existing, item any) error {
	tables := a.cachedTables(fields, item)
	for _, f := range newReferences(fields, existing, item) {
		edge, err := referenceEdgeOn(ctx, db, tables, f, item)
		if err != nil {
			return err
		}
		if a.relationStore == nil {
			continue
		}
		if _, _, err := writeEdgeRow(ctx, db, edge); err != nil {
			return fmt.Errorf("%w: write the %s edge for %s: %s", ErrInternal, edge.RelationKind, f.name, err)
		}
	}
	return nil
}

// referenceEdgeOn reads f's stored target on db with tables and returns its
// edge. A target that is gone is f's guided refusal.
func referenceEdgeOn(ctx context.Context, db DB, tables referenceTables, f referenceField, item any) (RelationEdge, error) {
	stored := *f.value(item)
	for _, typeName := range f.targets(item) {
		id, _, err := tables[typeName].lookup(ctx, db, stored)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return RelationEdge{}, err
		}
		return referenceEdge(f, item, typeName, id), nil
	}
	return RelationEdge{}, f.refuse(stored, f.targets(item), ErrNotFound)
}

// referenceEdge is f's system-asserted edge from item to the target.
func referenceEdge(f referenceField, item any, targetType, targetID string) RelationEdge {
	edge := f.edge(item, targetType, targetID)
	job := referencesJob
	edge.EdgeClass, edge.CreatedByJob = "asserted", &job
	if edge.Attributes == nil {
		edge.Attributes = json.RawMessage("{}")
	}
	return edge
}

// announceReferenceEdges records provenance and sends the relation event for
// each edge [App.writeReferenceEdges] wrote, after the commit. Fail-open: the
// write has committed.
func (a *App) announceReferenceEdges(ctx context.Context, fields []referenceField, existing, item any) {
	rs := a.relationStore
	if rs == nil {
		return
	}
	tables := a.cachedTables(fields, item)
	for _, f := range newReferences(fields, existing, item) {
		edge, err := referenceEdgeOn(ctx, rs.db, tables, f, item)
		if err == nil {
			edge, err = liveEdge(ctx, rs.db, edge)
		}
		if err != nil {
			slog.WarnContext(ctx, "smeldr: references: the edge was written but not announced", "field", f.name, "error", err)
			continue
		}
		rs.announceEdge(ctx, edge, true)
	}
}

// signalReferencePlan is what [InsertSignalWithWarnings] read before its
// transaction for the Signal's references.
type signalReferencePlan struct {
	tables    referenceTables
	relations bool // smeldr_relations exists: write the about edges
	system    bool // core's own Signal: its subject is an ID and is not looked up
}

// prepareSignalReferences resolves a Signal's subject and task_ref for
// [InsertSignalWithWarnings], before its transaction. A caller with an identity
// (a [Context] or an attached [ReadScope]) is held to its read scope; a plain
// internal context is not, but its names must still resolve. Core's own
// emitters (withSignalSystem) name subjects by ID, possibly of a type with no
// table, so theirs are kept as given. The subject is stored as its ID, task_ref
// as the Task's address. A name that resolves to nothing the caller can read is
// refused with the field's guided message.
func prepareSignalReferences(ctx context.Context, db DB, s *Signal) (signalReferencePlan, error) {
	plan := signalReferencePlan{system: isSignalSystem(ctx)}
	if err := checkSignalSubjectPair(s); err != nil {
		return plan, err
	}
	s.SubjectID, s.TaskRef = strings.TrimSpace(s.SubjectID), strings.TrimSpace(s.TaskRef)
	if s.SubjectID == "" && s.TaskRef == "" {
		return plan, nil
	}
	if refuseInTx(db, "reference check") != nil {
		// Inside a caller's transaction no table can be checked. Core's own
		// Signals (MigrateSignalKinds) keep their subject and get their edge
		// from MigrateReferences; anyone else's references cannot be checked.
		if plan.system {
			return plan, nil
		}
		return plan, fmt.Errorf("%w: InsertSignal cannot check a Signal's subject or task_ref inside a caller's transaction; insert it on the base handle", ErrBadRequest)
	}
	ok, err := tableExists(ctx, db, "smeldr_relations")
	if err != nil {
		return plan, fmt.Errorf("%w: InsertSignal: %s", ErrInternal, err)
	}
	plan.relations = ok
	if plan.system {
		return plan, nil
	}
	scope, err := signalCallerScope(ctx, db)
	if err != nil {
		return plan, err
	}
	fields := signalReferences()
	if plan.tables, err = referenceTablesFor(ctx, db, fields, s); err != nil {
		return plan, err
	}
	for _, f := range fields {
		p := f.value(s)
		if *p == "" {
			continue
		}
		types := f.targets(s)
		id, address, err := plan.tables[types[0]].lookup(ctx, db, *p)
		if err == nil && scope != nil && !scope.All() && !scope.Readable(types[0], id) {
			err = ErrNotFound // hidden answers as missing
		}
		if err != nil {
			return plan, f.refuse(*p, types, err)
		}
		*p = referenceTarget{Type: types[0], ID: id, Address: address}.stored(f.byID)
	}
	return plan, nil
}

// signalCallerScope is the read scope a Signal insert is held to: the attached
// one, the caller's own when ctx is a [Context], or none for a plain internal
// context.
func signalCallerScope(ctx context.Context, db DB) (*ReadScope, error) {
	if s, ok := ReadScopeFrom(ctx); ok {
		return s, nil
	}
	if _, ok := ctx.(Context); !ok {
		return nil, nil
	}
	return packageScope(ctx, db)
}

// writeSignalReferenceEdges writes a Signal's about edges in its insert
// transaction, reading each target again on exec (core's own Signals are not
// looked up). A database without smeldr_relations gets no edge.
func writeSignalReferenceEdges(ctx context.Context, exec DB, plan signalReferencePlan, s *Signal) error {
	for _, f := range newReferences(signalReferences(), nil, s) {
		var edge RelationEdge
		if plan.system {
			edge = referenceEdge(f, s, f.targets(s)[0], *f.value(s))
		} else {
			var err error
			if edge, err = referenceEdgeOn(ctx, exec, plan.tables, f, s); err != nil {
				return err
			}
		}
		if !plan.relations {
			continue
		}
		if _, _, err := writeEdgeRow(ctx, exec, edge); err != nil {
			return fmt.Errorf("%w: InsertSignal: write the about edge for %s: %s", ErrInternal, f.name, err)
		}
	}
	return nil
}

// announceSignalReferenceEdges records provenance and sends relation.asserted
// (new_row true) for the about edges [InsertSignalWithWarnings] wrote for the
// Signal signalID, after its commit (T875). A Signal created through the
// Signal module announces through its save hooks instead; this serves the
// raw inserts: [App.NotifySignalCreated] (mcp's create_signal) and core's own
// conditions. Fail-open, logged at Warn: the Signal has committed.
func announceSignalReferenceEdges(ctx context.Context, rs *RelationStore, signalID string) {
	if rs == nil || signalID == "" {
		return
	}
	rows, err := rs.db.QueryContext(ctx,
		"SELECT "+relationColumns+" FROM smeldr_relations WHERE source_type = 'Signal' AND source_id = $1 AND created_by_job = $2 AND invalid_at IS NULL",
		signalID, referencesJob)
	if err != nil {
		slog.WarnContext(ctx, "smeldr: references: a Signal's about edges were written but not announced", "signal", signalID, "error", err)
		return
	}
	var edges []RelationEdge
	for rows.Next() {
		e, err := scanEdge(rows)
		if err != nil {
			slog.WarnContext(ctx, "smeldr: references: a Signal's about edges were written but not announced", "signal", signalID, "error", err)
			break
		}
		edges = append(edges, e)
	}
	_ = rows.Close()
	for _, e := range edges {
		rs.announceEdge(ctx, e, true)
	}
}

// EnsureReferenceColumns adds the reference columns D113 introduced to
// pre-existing databases: smeldr_tasks.implements and
// smeldr_amendments.task_ref. Fresh installs have them from
// [CreateOrchestrationTables]. A table that does not exist is skipped.
// Idempotent; call it on every boot, before serving.
func EnsureReferenceColumns(ctx context.Context, db DB) error {
	for _, c := range [][2]string{{"smeldr_tasks", "implements"}, {"smeldr_amendments", "task_ref"}} {
		exists, err := tableExists(ctx, db, c[0])
		if err != nil {
			return fmt.Errorf("smeldr: EnsureReferenceColumns: %s: %w", c[0], err)
		}
		if !exists {
			continue
		}
		if err := EnsureColumn(ctx, db, c[0], c[1], "TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("smeldr: EnsureReferenceColumns: %w", err)
		}
	}
	return nil
}

// checkSchemaReferences refuses a reference field that names a type that is
// neither registered nor the type being defined.
func (a *App) checkSchemaReferences(schema *ContentTypeSchema) error {
	fields, err := schema.ParseFields()
	if err != nil {
		return nil // ValidateSchemaDef reports it
	}
	for _, f := range fields {
		if f.References == "" || f.References == schema.TypeName {
			continue
		}
		if a.typeRegistry.Lookup(f.References) == nil {
			return Err("fields", fmt.Sprintf("%q references %q, which is not a registered content type", f.Name, f.References))
		}
	}
	return nil
}

// dynamicReference is one reference field a runtime-defined write sets for the
// first time, in its stored form.
type dynamicReference struct{ field, target, stored string }

// prepareReferences resolves each reference field ([SchemaField.References])
// that fields sets, under the caller's read scope, before the write's
// transaction, and writes it back in its stored form. existing is the item's
// stored fields on an update (nil on a create): a field already set there is
// write-once. It returns the references set for the first time. A repository
// without an App stores references as given.
func (r *DynamicTypeRepo) prepareReferences(ctx context.Context, existing, fields map[string]any) ([]dynamicReference, error) {
	if r.refApp == nil || r.schema == nil {
		return nil, nil
	}
	defs, _ := r.schema.ParseFields() // validated when the type was defined
	var out []dynamicReference
	for _, sf := range defs {
		if sf.References == "" {
			continue
		}
		raw, ok := fields[sf.Name]
		if !ok {
			continue
		}
		v, _ := raw.(string)
		v = strings.TrimSpace(v)
		old, _ := existing[sf.Name].(string)
		switch {
		case v == "" && old != "":
			return nil, Err(sf.Name, fmt.Sprintf("is write-once: it names %s and cannot be cleared; correct a wrong reference with the relation tools", old))
		case v == "":
			fields[sf.Name] = ""
			continue
		case v == old:
			continue
		}
		t, err := r.refApp.resolveReference(ctx, []string{sf.References}, v)
		if err != nil {
			f := referenceField{name: sf.Name}
			return nil, f.refuse(v, []string{sf.References}, err)
		}
		stored := t.stored(false)
		if old != "" && stored != old {
			return nil, Err(sf.Name, fmt.Sprintf("is write-once: it names %s; correct a wrong reference with the relation tools", old))
		}
		fields[sf.Name] = stored
		if old == "" {
			out = append(out, dynamicReference{field: sf.Name, target: sf.References, stored: stored})
		}
	}
	return out, nil
}

// referenceEdgeOn reads ref's target on db and returns the refers_to edge from
// the item id. A target that is gone is the field's guided refusal.
func (r *DynamicTypeRepo) referenceEdgeOn(ctx context.Context, db DB, id string, ref dynamicReference) (RelationEdge, error) {
	rt, _ := r.refApp.referenceTables.Load(ref.target)
	table, _ := rt.(recordTable)
	targetID, _, err := table.lookup(ctx, db, ref.stored)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			f := referenceField{name: ref.field}
			return RelationEdge{}, f.refuse(ref.stored, []string{ref.target}, err)
		}
		return RelationEdge{}, err
	}
	attrs, _ := json.Marshal(map[string]string{"field": ref.field})
	job := referencesJob
	return RelationEdge{SourceType: r.typeName, SourceID: id, TargetType: ref.target, TargetID: targetID,
		RelationKind: "refers_to", EdgeClass: "asserted", CreatedByJob: &job, Attributes: attrs}, nil
}

// writeReferenceEdges runs in the write's transaction: it reads each new
// reference's target again on exec and writes its refers_to edge there (no
// edge without a relation store).
func (r *DynamicTypeRepo) writeReferenceEdges(ctx context.Context, exec DB, id string, refs []dynamicReference) error {
	for _, ref := range refs {
		edge, err := r.referenceEdgeOn(ctx, exec, id, ref)
		if err != nil {
			return err
		}
		if r.refApp.relationStore == nil {
			continue
		}
		if _, _, err := writeEdgeRow(ctx, exec, edge); err != nil {
			return fmt.Errorf("%w: write the refers_to edge for %s: %s", ErrInternal, ref.field, err)
		}
	}
	return nil
}

// announceReferenceEdges records provenance and sends the relation event for
// each new reference edge, after the commit. Fail-open.
func (r *DynamicTypeRepo) announceReferenceEdges(ctx context.Context, id string, refs []dynamicReference) {
	if len(refs) == 0 || r.refApp.relationStore == nil {
		return
	}
	rs := r.refApp.relationStore
	for _, ref := range refs {
		edge, err := r.referenceEdgeOn(ctx, rs.db, id, ref)
		if err == nil {
			edge, err = liveEdge(ctx, rs.db, edge)
		}
		if err != nil {
			slog.WarnContext(ctx, "smeldr: references: the edge was written but not announced", "type", r.typeName, "field", ref.field, "error", err)
			continue
		}
		rs.announceEdge(ctx, edge, true)
	}
}

// endReader decides, for one walk, whether an end of an edge is readable: the
// item itself under scope and, for a Signal, its subject too (D113). Signal
// subjects are read once each, in a batch where the walk knows them up front.
type endReader struct {
	ctx       context.Context
	db        DB
	scope     *ReadScope
	subjectOK func(subjectType, subjectID string) bool
	signals   map[string]bool // Signal ID to whether its subject is readable
}

// newEndReader returns the endReader for scope on db.
func newEndReader(ctx context.Context, db DB, scope *ReadScope) *endReader {
	return &endReader{ctx: ctx, db: db, scope: scope, subjectOK: subjectRule(scope), signals: map[string]bool{}}
}

// prime reads the subjects of the Signals ids names, in one statement.
// Its callers have a subject rule (a scope that reads everything has none and
// never reaches it).
func (r *endReader) prime(ids []string) error {
	var todo []string
	for _, id := range ids {
		if _, ok := r.signals[id]; !ok {
			r.signals[id] = false
			todo = append(todo, id)
		}
	}
	readable, err := signalsWithReadableSubject(r.ctx, r.db, todo, r.subjectOK)
	if err != nil {
		return err
	}
	for id := range readable {
		r.signals[id] = true
	}
	return nil
}

// readable reports whether the item (typeName, id) is a readable end.
func (r *endReader) readable(typeName, id string) (bool, error) {
	if !r.scope.Readable(typeName, id) {
		return false, nil
	}
	if typeName != "Signal" || r.subjectOK == nil {
		return true, nil
	}
	if _, ok := r.signals[id]; !ok {
		if err := r.prime([]string{id}); err != nil {
			return false, err
		}
	}
	return r.signals[id], nil
}
