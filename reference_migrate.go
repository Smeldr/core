// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ReferenceMigrationOptions configures [MigrateReferences].
type ReferenceMigrationOptions struct {
	// DryRun runs the whole migration and rolls it back, so the report shows
	// exactly what an apply would do and nothing is written.
	DryRun bool
	// RefFields names runtime-defined reference fields that their schema does
	// not declare yet, as "type.field" to the type it names, for example
	// {"task_plan.task_ref": "Task"}, so the backfill need not wait for a
	// redefinition. Fields a schema declares ([SchemaField.References]) are
	// always included.
	RefFields map[string]string
}

// ReferenceMigrationReport is what [MigrateReferences] did, or in a dry run
// what it would do.
type ReferenceMigrationReport struct {
	DryRun bool `json:"dry_run"`
	// DurationMS is how long the migration's one transaction was open.
	DurationMS int64 `json:"duration_ms"`
	// Created lists every edge the migration wrote.
	Created []ReferenceEdgeChange `json:"created"`
	// Present lists every reference whose edge was already there.
	Present []ReferenceEdgeChange `json:"present"`
	// Rewritten lists every Signal subject_id stored by slug or address and
	// rewritten to the subject's ID, so the read rule sees it.
	Rewritten []ReferenceRewrite `json:"rewritten"`
	// Unresolved lists every reference value that names no record, or that is
	// left alone on purpose, with the reason. Nothing is guessed.
	Unresolved []ReferenceNote `json:"unresolved"`
}

// ReferenceEdgeChange is one reference and its edge.
type ReferenceEdgeChange struct {
	SourceType    string `json:"source_type"`
	SourceID      string `json:"source_id"`
	SourceAddress string `json:"source_address"`
	Field         string `json:"field"`
	Kind          string `json:"kind"`
	TargetType    string `json:"target_type"`
	TargetID      string `json:"target_id"`
}

// ReferenceRewrite is one stored reference value the migration rewrote.
type ReferenceRewrite struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Address string `json:"address"`
	Field   string `json:"field"`
	Old     string `json:"old"`
	New     string `json:"new"`
}

// ReferenceNote is one reference value the migration left alone.
type ReferenceNote struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Address string `json:"address"`
	Field   string `json:"field"`
	Value   string `json:"value"`
	Reason  string `json:"reason"`
}

// referenceRow is one stored reference the migration considers.
type referenceRow struct {
	sourceType, id, address, field, value string
	// targets are the types value may name, tried in order.
	targets []string
	// kind returns the edge kind for the resolved target type, or "" to only
	// report the reference.
	kind func(targetType string) string
	// reverse makes the target the edge's source (ships_as, Task to Amendment).
	reverse bool
	// rewriteToID rewrites a value that resolves but is not the ID.
	rewriteToID bool
	// table is the source table, for a rewrite.
	table string
}

// MigrateReferences turns the references existing records carry as text into
// the edges D113 makes the system assert, in one transaction, reporting what
// it cannot resolve rather than guessing:
//   - a Signal's subject becomes an "about" edge, and a subject_id stored as a
//     slug or address is rewritten to the subject's ID; its task_ref becomes an
//     "about" edge when it names a Task;
//   - a Task's note_ref whose whole value names a Decision becomes
//     "implements", a Goal "derives_from"; one naming a Task is reported only,
//     and free text is reported, never parsed;
//   - an Amendment's amends without a live edge becomes "amends", and a
//     task_ref (D113) without one becomes "ships_as" from the Task;
//   - a runtime-defined field declared with [SchemaField.References], or named
//     in opts.RefFields, becomes "refers_to".
//
// Resolution is by address, slug, ID and legacy number, without a read scope:
// it is an operator's migration. It is idempotent (an edge already there is
// reported as present) and a dry run runs the same code and rolls back. It
// needs smeldr_relations ([App.Relations]); a table that does not exist is
// skipped. A malformed RefFields entry is [ErrBadRequest] before anything runs.
func MigrateReferences(ctx context.Context, db DB, opts ReferenceMigrationOptions) (*ReferenceMigrationReport, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: MigrateReferences needs a database", ErrBadRequest)
	}
	refFields, err := parseRefFields(opts.RefFields)
	if err != nil {
		return nil, err
	}
	if ok, err := tableExists(ctx, db, "smeldr_relations"); err != nil {
		return nil, fmt.Errorf("%w: MigrateReferences: %s", ErrInternal, err)
	} else if !ok {
		return nil, fmt.Errorf("%w: MigrateReferences: there is no smeldr_relations table; wire App.Relations first", ErrBadRequest)
	}
	// Everything that needs a table check is read here, on the base handle.
	if err := schemaRefFields(ctx, db, refFields); err != nil {
		return nil, err
	}
	present := map[string]bool{}
	tables := referenceTables{}
	needTable := func(typeName string) error {
		if _, ok := tables[typeName]; ok {
			return nil
		}
		rt, err := recordTableOf(ctx, db, typeName)
		if err != nil {
			return err
		}
		tables[typeName] = rt
		return nil
	}
	for _, t := range []string{"smeldr_signals", "smeldr_tasks", "smeldr_amendments", "smeldr_dynamic_content"} {
		ok, err := tableExists(ctx, db, t)
		if err != nil {
			return nil, fmt.Errorf("%w: MigrateReferences: %s", ErrInternal, err)
		}
		present[t] = ok
	}
	for _, t := range []string{"Task", "Goal", "Decision", "Amendment"} {
		if err := needTable(t); err != nil {
			return nil, err
		}
	}
	for _, target := range refFields {
		if err := needTable(target); err != nil {
			return nil, err
		}
	}
	if present["smeldr_signals"] {
		subjectTypes, err := distinctSubjectTypes(ctx, db)
		if err != nil {
			return nil, err
		}
		for _, t := range subjectTypes {
			if err := needTable(t); err != nil {
				return nil, err
			}
		}
	}

	start := time.Now()
	exec, commit, rollback, err := conflictTx(ctx, db)
	if err != nil {
		return nil, err
	}
	defer rollback()

	var rows []referenceRow
	if present["smeldr_signals"] {
		if rows, err = appendSignalReferenceRows(ctx, exec, rows); err != nil {
			return nil, err
		}
	}
	if present["smeldr_tasks"] {
		if rows, err = appendQueryRows(ctx, exec, rows, "SELECT id, COALESCE(address, ''), note_ref FROM smeldr_tasks WHERE TRIM(note_ref) <> '' ORDER BY id",
			func(id, address, value string) referenceRow {
				return referenceRow{sourceType: "Task", id: id, address: address, field: "note_ref", value: value,
					targets: []string{"Decision", "Goal", "Task"}, kind: noteRefKind}
			}); err != nil {
			return nil, err
		}
	}
	if present["smeldr_amendments"] {
		if rows, err = appendQueryRows(ctx, exec, rows, "SELECT id, COALESCE(address, ''), amends FROM smeldr_amendments WHERE TRIM(amends) <> '' ORDER BY id",
			func(id, address, value string) referenceRow {
				return referenceRow{sourceType: "Amendment", id: id, address: address, field: "amends", value: value,
					targets: []string{"Decision"}, kind: func(string) string { return "amends" }}
			}); err != nil {
			return nil, err
		}
		if rows, err = appendQueryRows(ctx, exec, rows, "SELECT id, COALESCE(address, ''), task_ref FROM smeldr_amendments WHERE TRIM(task_ref) <> '' ORDER BY id",
			func(id, address, value string) referenceRow {
				return referenceRow{sourceType: "Amendment", id: id, address: address, field: "task_ref", value: value,
					targets: []string{"Task"}, kind: func(string) string { return "ships_as" }, reverse: true}
			}); err != nil {
			return nil, err
		}
	}
	if present["smeldr_dynamic_content"] && len(refFields) > 0 {
		if rows, err = appendDynamicReferenceRows(ctx, exec, rows, refFields); err != nil {
			return nil, err
		}
	}

	rep := &ReferenceMigrationReport{DryRun: opts.DryRun, Created: []ReferenceEdgeChange{}, Present: []ReferenceEdgeChange{},
		Rewritten: []ReferenceRewrite{}, Unresolved: []ReferenceNote{}}
	for _, r := range rows {
		if err := migrateReference(ctx, exec, tables, rep, r); err != nil {
			return nil, err
		}
	}
	if opts.DryRun {
		rollback()
	} else if err := commit(); err != nil {
		return nil, fmt.Errorf("%w: MigrateReferences commit: %s", ErrInternal, err)
	}
	rep.DurationMS = time.Since(start).Milliseconds()
	return rep, nil
}

// MigrateReferences runs [MigrateReferences] on the App's database.
func (a *App) MigrateReferences(ctx context.Context, opts ReferenceMigrationOptions) (*ReferenceMigrationReport, error) {
	return MigrateReferences(ctx, a.cfg.DB, opts)
}

// noteRefKind is the edge a note_ref naming targetType becomes: a Task in a
// note_ref is not a dependency, so it is reported only.
func noteRefKind(targetType string) string {
	switch targetType {
	case "Decision":
		return "implements"
	case "Goal":
		return "derives_from"
	}
	return ""
}

// parseRefFields checks opts.RefFields: each key "type.field", each value a
// type name.
func parseRefFields(in map[string]string) (map[string]string, error) {
	out := map[string]string{}
	var bad []string
	for k, v := range in {
		typeName, field, ok := strings.Cut(strings.TrimSpace(k), ".")
		if !ok || typeName == "" || field == "" || strings.TrimSpace(v) == "" {
			bad = append(bad, k)
			continue
		}
		out[typeName+"."+field] = strings.TrimSpace(v)
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return nil, fmt.Errorf("%w: MigrateReferences: RefFields entries must be \"type.field\" to a type name: %s", ErrBadRequest, strings.Join(bad, ", "))
	}
	return out, nil
}

// schemaRefFields adds every reference field the runtime-defined schemas
// declare to refFields.
func schemaRefFields(ctx context.Context, db DB, refFields map[string]string) error {
	ok, err := tableExists(ctx, db, "smeldr_content_type_schemas")
	if err != nil {
		return fmt.Errorf("%w: MigrateReferences: %s", ErrInternal, err)
	}
	if !ok {
		return nil
	}
	schemas, err := NewSchemaStore(db).AllByKind(ctx, "content")
	if err != nil {
		return fmt.Errorf("%w: MigrateReferences: read schemas: %s", ErrInternal, err)
	}
	for _, s := range schemas {
		fields, err := s.ParseFields()
		if err != nil {
			continue
		}
		for _, f := range fields {
			if f.References != "" {
				refFields[s.TypeName+"."+f.Name] = f.References
			}
		}
	}
	return nil
}

// distinctSubjectTypes returns the subject types the Signals name, read on the
// base handle.
func distinctSubjectTypes(ctx context.Context, db DB) ([]string, error) {
	rs, err := db.QueryContext(ctx, "SELECT DISTINCT subject_type FROM smeldr_signals WHERE TRIM(subject_type) <> '' ORDER BY subject_type")
	if err != nil {
		return nil, fmt.Errorf("%w: MigrateReferences: %s", ErrInternal, err)
	}
	defer rs.Close()
	var out []string
	for rs.Next() {
		var t string
		if err := rs.Scan(&t); err != nil {
			return nil, fmt.Errorf("%w: MigrateReferences: %s", ErrInternal, err)
		}
		out = append(out, t)
	}
	if err := rs.Err(); err != nil {
		return nil, fmt.Errorf("%w: MigrateReferences: %s", ErrInternal, err)
	}
	return out, nil
}

// appendQueryRows appends a referenceRow for each (id, address, value) row
// query returns.
func appendQueryRows(ctx context.Context, exec DB, rows []referenceRow, query string, row func(id, address, value string) referenceRow) ([]referenceRow, error) {
	rs, err := exec.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("%w: MigrateReferences: %s", ErrInternal, err)
	}
	defer rs.Close()
	for rs.Next() {
		var id, address, value string
		if err := rs.Scan(&id, &address, &value); err != nil {
			return nil, fmt.Errorf("%w: MigrateReferences: %s", ErrInternal, err)
		}
		rows = append(rows, row(id, address, strings.TrimSpace(value)))
	}
	if err := rs.Err(); err != nil {
		return nil, fmt.Errorf("%w: MigrateReferences: %s", ErrInternal, err)
	}
	return rows, nil
}

// appendSignalReferenceRows appends every Signal's subject and task_ref.
func appendSignalReferenceRows(ctx context.Context, exec DB, rows []referenceRow) ([]referenceRow, error) {
	rs, err := exec.QueryContext(ctx,
		"SELECT id, COALESCE(address, ''), subject_type, subject_id, task_ref FROM smeldr_signals "+
			"WHERE TRIM(subject_id) <> '' OR TRIM(task_ref) <> '' ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("%w: MigrateReferences: %s", ErrInternal, err)
	}
	defer rs.Close()
	about := func(string) string { return "about" }
	for rs.Next() {
		var id, address, subjectType, subjectID, taskRef string
		if err := rs.Scan(&id, &address, &subjectType, &subjectID, &taskRef); err != nil {
			return nil, fmt.Errorf("%w: MigrateReferences: %s", ErrInternal, err)
		}
		if s := strings.TrimSpace(subjectID); s != "" {
			rows = append(rows, referenceRow{sourceType: "Signal", id: id, address: address, field: "subject_id", value: s,
				targets: []string{subjectType}, kind: about, rewriteToID: true, table: "smeldr_signals"})
		}
		if v := strings.TrimSpace(taskRef); v != "" {
			rows = append(rows, referenceRow{sourceType: "Signal", id: id, address: address, field: "task_ref", value: v,
				targets: []string{"Task"}, kind: about})
		}
	}
	if err := rs.Err(); err != nil {
		return nil, fmt.Errorf("%w: MigrateReferences: %s", ErrInternal, err)
	}
	return rows, nil
}

// appendDynamicReferenceRows appends each runtime-defined reference field's
// value.
func appendDynamicReferenceRows(ctx context.Context, exec DB, rows []referenceRow, refFields map[string]string) ([]referenceRow, error) {
	byType := map[string][]string{}
	for k := range refFields {
		typeName, field, _ := strings.Cut(k, ".")
		byType[typeName] = append(byType[typeName], field)
	}
	types := make([]string, 0, len(byType))
	for t := range byType {
		types = append(types, t)
	}
	sort.Strings(types)
	for _, typeName := range types {
		fieldNames := byType[typeName]
		sort.Strings(fieldNames)
		rs, err := exec.QueryContext(ctx,
			"SELECT id, COALESCE(address, ''), fields FROM smeldr_dynamic_content WHERE type_name = $1 ORDER BY id", typeName)
		if err != nil {
			return nil, fmt.Errorf("%w: MigrateReferences: %s", ErrInternal, err)
		}
		for rs.Next() {
			var id, address, raw string
			if err := rs.Scan(&id, &address, &raw); err != nil {
				rs.Close()
				return nil, fmt.Errorf("%w: MigrateReferences: %s", ErrInternal, err)
			}
			var fields map[string]any
			_ = json.Unmarshal([]byte(raw), &fields)
			for _, name := range fieldNames {
				v, _ := fields[name].(string)
				if v = strings.TrimSpace(v); v == "" {
					continue
				}
				rows = append(rows, referenceRow{sourceType: typeName, id: id, address: address, field: name, value: v,
					targets: []string{refFields[typeName+"."+name]}, kind: func(string) string { return "refers_to" }})
			}
		}
		err = rs.Err()
		rs.Close()
		if err != nil {
			return nil, fmt.Errorf("%w: MigrateReferences: %s", ErrInternal, err)
		}
	}
	return rows, nil
}

// migrateReference resolves one stored reference and writes its edge, or
// reports it.
func migrateReference(ctx context.Context, exec DB, tables referenceTables, rep *ReferenceMigrationReport, r referenceRow) error {
	note := func(reason string) {
		rep.Unresolved = append(rep.Unresolved, ReferenceNote{Type: r.sourceType, ID: r.id, Address: r.address, Field: r.field, Value: r.value, Reason: reason})
	}
	for _, typeName := range r.targets {
		rt := tables[typeName]
		targetID, _, err := rt.lookup(ctx, exec, r.value)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		var ve *ValidationError
		if errors.As(err, &ve) {
			note(ve.Error())
			return nil
		}
		if err != nil {
			return err
		}
		kind := r.kind(typeName)
		if kind == "" {
			note(fmt.Sprintf("names %s %s, which is not a relation this field makes; left as text", typeName, targetID))
			return nil
		}
		if r.rewriteToID && targetID != r.value {
			if _, err := exec.ExecContext(ctx, "UPDATE "+r.table+" SET "+r.field+" = $1 WHERE id = $2", targetID, r.id); err != nil {
				return fmt.Errorf("%w: MigrateReferences: rewrite %s %s: %s", ErrInternal, r.sourceType, r.id, err)
			}
			rep.Rewritten = append(rep.Rewritten, ReferenceRewrite{Type: r.sourceType, ID: r.id, Address: r.address, Field: r.field, Old: r.value, New: targetID})
		}
		job := referencesJob
		attrs, _ := json.Marshal(map[string]string{"field": r.field})
		edge := RelationEdge{SourceType: r.sourceType, SourceID: r.id, TargetType: typeName, TargetID: targetID,
			RelationKind: kind, EdgeClass: "asserted", CreatedByJob: &job, Attributes: attrs}
		if r.reverse {
			edge.SourceType, edge.SourceID, edge.TargetType, edge.TargetID = typeName, targetID, r.sourceType, r.id
		}
		_, isNew, err := writeEdgeRow(ctx, exec, edge)
		if err != nil {
			return fmt.Errorf("%w: MigrateReferences: write %s edge for %s %s: %s", ErrInternal, kind, r.sourceType, r.id, err)
		}
		change := ReferenceEdgeChange{SourceType: r.sourceType, SourceID: r.id, SourceAddress: r.address, Field: r.field, Kind: kind, TargetType: typeName, TargetID: targetID}
		if isNew {
			rep.Created = append(rep.Created, change)
		} else {
			rep.Present = append(rep.Present, change)
		}
		return nil
	}
	note(fmt.Sprintf("names no %s", strings.Join(r.targets, " or ")))
	return nil
}
