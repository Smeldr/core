// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// RecordShapeMigrationOptions configures [MigrateRecordShape].
type RecordShapeMigrationOptions struct {
	// DryRun runs the whole migration and rolls it back, so the report shows
	// exactly what an apply would do and nothing is written.
	DryRun bool
	// AmendmentTypeMap maps an Amendment, by address ("A493") or number
	// ("493"), to a type in [AmendmentTypes]. It applies only to an Amendment
	// whose stored type is not on the list, and wins over the built-in rules.
	// Every entry is checked before anything is written; one bad entry refuses
	// the whole run. Run again after an apply, the same map is refused, since
	// each Amendment it names now has a valid type; run without it.
	AmendmentTypeMap map[string]string
}

// RecordShapeMigrationReport is what [MigrateRecordShape] did, or in a dry
// run what it would do. It names records by type, ID and address and carries
// the old and new value of each changed title, summary or type; never body
// text beyond a derived title.
type RecordShapeMigrationReport struct {
	DryRun bool `json:"dry_run"`
	// DurationMS is how long the migration's one transaction was open.
	DurationMS int64 `json:"duration_ms"`
	// Titles lists every title set from the record's text, and every
	// Amendment summary cut to [TitleMaxLength] characters.
	Titles []RecordShapeChange `json:"titles"`
	// AmendmentTypes lists every amendment_type mapped to the closed list.
	AmendmentTypes []RecordShapeChange `json:"amendment_types"`
	// Untitled lists records left without a title: no usable text.
	Untitled []RecordShapeNote `json:"untitled"`
	// UnmappedAmendmentTypes lists amendment_type values left as they are,
	// for a person to map.
	UnmappedAmendmentTypes []RecordShapeNote `json:"unmapped_amendment_types"`
}

// RecordShapeChange is one value the migration changed.
type RecordShapeChange struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Address string `json:"address"`
	Field   string `json:"field"`
	Old     string `json:"old"`
	New     string `json:"new"`
	// BodyKept is set when a long summary was cut and its full text was
	// put first in the body, so no text was lost.
	BodyKept bool `json:"body_kept,omitempty"`
	// Source says how an amendment_type was mapped: "rule" for the built-in
	// mapping, "type_map" for [RecordShapeMigrationOptions.AmendmentTypeMap].
	Source string `json:"source,omitempty"`
}

// RecordShapeNote is one value the migration left alone, with the reason.
type RecordShapeNote struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Address string `json:"address"`
	Field   string `json:"field"`
	Value   string `json:"value"`
	Reason  string `json:"reason"`
}

// amendmentTypeMapping maps today's amendment_type variants to the closed
// list (D112). A value in [AmendmentTypes] is kept; anything else not here
// is reported, not guessed.
var amendmentTypeMapping = map[string]string{
	"feat":   "feature",
	"bugfix": "fix",
	"deploy": "release",
}

// MigrateRecordShape brings existing records to D112's shape, in one
// transaction:
//   - a Task or Goal without a title gets one from the first line of its
//     description, a Decision without a title one from the first line of its
//     body: heading and list marks removed, cut at a word boundary at
//     [TitleMaxLength] characters; a record with no usable text is reported
//     and left untitled;
//   - an Amendment summary longer than [TitleMaxLength] characters is cut the
//     same way, and the full summary is put first in the body unless the body
//     already contains it, so no text is lost;
//   - an amendment_type outside [AmendmentTypes] is mapped where the meaning
//     is clear (feat to feature, bugfix to fix, deploy to release) and reported
//     otherwise; [RecordShapeMigrationOptions.AmendmentTypeMap] maps the
//     rest by a person's decision.
//
// It is idempotent: a second run changes nothing. A dry run runs the same code
// and rolls back. It needs the title column on smeldr_tasks and smeldr_goals
// ([EnsureTaskGoalTitleColumns]); a table that does not exist is skipped.
func MigrateRecordShape(ctx context.Context, db DB, opts RecordShapeMigrationOptions) (*RecordShapeMigrationReport, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: MigrateRecordShape needs a database", ErrBadRequest)
	}
	type titleSource struct{ typeName, table, source string }
	var sources []titleSource
	for _, s := range []titleSource{
		{"Task", "smeldr_tasks", "description"},
		{"Goal", "smeldr_goals", "description"},
		{"Decision", "smeldr_decisions", "body"},
	} {
		ok, err := columnExists(ctx, db, s.table, "title")
		if err != nil {
			return nil, fmt.Errorf("%w: MigrateRecordShape: %s", ErrInternal, err)
		}
		if ok {
			sources = append(sources, s)
			continue
		}
		if exists, err := tableExists(ctx, db, s.table); err != nil {
			return nil, fmt.Errorf("%w: MigrateRecordShape: %s", ErrInternal, err)
		} else if exists {
			return nil, fmt.Errorf("%w: MigrateRecordShape: %s has no title column; run EnsureTaskGoalTitleColumns first", ErrBadRequest, s.table)
		}
	}
	hasAmendments, err := tableExists(ctx, db, "smeldr_amendments")
	if err != nil {
		return nil, fmt.Errorf("%w: MigrateRecordShape: %s", ErrInternal, err)
	}
	if len(opts.AmendmentTypeMap) > 0 && !hasAmendments {
		return nil, fmt.Errorf("%w: MigrateRecordShape: AmendmentTypeMap given but there is no smeldr_amendments table", ErrBadRequest)
	}
	// The report names records by address where the table has the column
	// (D110); a database migrated before it reports the ID only.
	addressExprs := map[string]string{}
	amendmentsHaveAddress := false
	for _, table := range []string{"smeldr_tasks", "smeldr_goals", "smeldr_decisions", "smeldr_amendments"} {
		ok, err := columnExists(ctx, db, table, "address")
		if err != nil {
			return nil, fmt.Errorf("%w: MigrateRecordShape: %s", ErrInternal, err)
		}
		addressExprs[table] = "''"
		if ok {
			addressExprs[table] = "COALESCE(address, '')"
			amendmentsHaveAddress = amendmentsHaveAddress || table == "smeldr_amendments"
		}
	}

	start := time.Now()
	exec, commit, rollback, err := conflictTx(ctx, db)
	if err != nil {
		return nil, err
	}
	defer rollback()

	rep := &RecordShapeMigrationReport{DryRun: opts.DryRun, Titles: []RecordShapeChange{}, AmendmentTypes: []RecordShapeChange{},
		Untitled: []RecordShapeNote{}, UnmappedAmendmentTypes: []RecordShapeNote{}}
	// The Amendments are read and the type map checked before any write, so a
	// bad map entry refuses the run with nothing changed.
	var amendments []amendmentRow
	var typeMap map[string]string
	if hasAmendments {
		if amendments, err = readAmendments(ctx, exec, addressExprs["smeldr_amendments"]); err != nil {
			return nil, err
		}
		if typeMap, err = resolveAmendmentTypeMap(opts.AmendmentTypeMap, amendments, amendmentsHaveAddress); err != nil {
			return nil, err
		}
	}
	for _, s := range sources {
		if err := migrateTitles(ctx, exec, rep, s.typeName, s.table, s.source, addressExprs[s.table]); err != nil {
			return nil, err
		}
	}
	if hasAmendments {
		if err := migrateAmendments(ctx, exec, rep, amendments, typeMap); err != nil {
			return nil, err
		}
	}
	if opts.DryRun {
		rollback()
	} else if err := commit(); err != nil {
		return nil, fmt.Errorf("%w: MigrateRecordShape commit: %s", ErrInternal, err)
	}
	rep.DurationMS = time.Since(start).Milliseconds()
	return rep, nil
}

// MigrateRecordShape runs [MigrateRecordShape] on the App's database.
func (a *App) MigrateRecordShape(ctx context.Context, opts RecordShapeMigrationOptions) (*RecordShapeMigrationReport, error) {
	return MigrateRecordShape(ctx, a.cfg.DB, opts)
}

// migrateTitles gives every untitled row of table a title from source.
func migrateTitles(ctx context.Context, exec DB, rep *RecordShapeMigrationReport, typeName, table, source, addressExpr string) error {
	type row struct{ id, address, text string }
	rows, err := exec.QueryContext(ctx, "SELECT id, "+addressExpr+", "+source+" FROM "+table+" WHERE TRIM(title) = '' ORDER BY id")
	if err != nil {
		return fmt.Errorf("%w: MigrateRecordShape %s: %s", ErrInternal, table, err)
	}
	var todo []row
	for rows.Next() {
		var r row
		var text sql.NullString
		if err := rows.Scan(&r.id, &r.address, &text); err != nil {
			rows.Close()
			return fmt.Errorf("%w: MigrateRecordShape %s: %s", ErrInternal, table, err)
		}
		r.text = text.String
		todo = append(todo, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("%w: MigrateRecordShape %s: %s", ErrInternal, table, err)
	}
	rows.Close()
	for _, r := range todo {
		title := cutAtWord(firstTextLine(r.text), TitleMaxLength)
		if title == "" {
			rep.Untitled = append(rep.Untitled, RecordShapeNote{Type: typeName, ID: r.id, Address: r.address, Field: "title",
				Reason: "no text to take a title from"})
			continue
		}
		if _, err := exec.ExecContext(ctx, "UPDATE "+table+" SET title = $1 WHERE id = $2", title, r.id); err != nil {
			return fmt.Errorf("%w: MigrateRecordShape %s: %s", ErrInternal, table, err)
		}
		rep.Titles = append(rep.Titles, RecordShapeChange{Type: typeName, ID: r.id, Address: r.address, Field: "title", New: title})
	}
	return nil
}

// amendmentRow is one Amendment as the migration reads it.
type amendmentRow struct{ id, address, number, summary, body, kind string }

// readAmendments reads every Amendment the migration may change.
func readAmendments(ctx context.Context, exec DB, addressExpr string) ([]amendmentRow, error) {
	rows, err := exec.QueryContext(ctx, "SELECT id, "+addressExpr+", amendment_number, summary, COALESCE(body, ''), amendment_type FROM smeldr_amendments ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("%w: MigrateRecordShape smeldr_amendments: %s", ErrInternal, err)
	}
	defer rows.Close()
	var all []amendmentRow
	for rows.Next() {
		var r amendmentRow
		if err := rows.Scan(&r.id, &r.address, &r.number, &r.summary, &r.body, &r.kind); err != nil {
			return nil, fmt.Errorf("%w: MigrateRecordShape smeldr_amendments: %s", ErrInternal, err)
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: MigrateRecordShape smeldr_amendments: %s", ErrInternal, err)
	}
	return all, nil
}

// isDigits reports whether s is one or more ASCII digits.
func isDigits(s string) bool {
	return s != "" && strings.IndexFunc(s, func(r rune) bool { return r < '0' || r > '9' }) < 0
}

// resolveAmendmentTypeMap checks a [RecordShapeMigrationOptions.AmendmentTypeMap]
// against the Amendments read and returns it keyed by Amendment ID. A key
// names an Amendment by address where the table has one (D110), else by
// amendment_number. Every problem is reported in one ErrBadRequest.
func resolveAmendmentTypeMap(given map[string]string, rows []amendmentRow, byAddress bool) (map[string]string, error) {
	if len(given) == 0 {
		return nil, nil
	}
	resolved := make(map[string]string, len(given))
	named := map[string]bool{} // Amendment IDs a key has named
	var problems []string
	for _, key := range slices.Sorted(maps.Keys(given)) {
		value := given[key]
		ref := strings.TrimSpace(key)
		if isDigits(ref) {
			ref = "A" + ref
		}
		if !strings.HasPrefix(ref, "A") || !isDigits(ref[1:]) {
			problems = append(problems, fmt.Sprintf("%s: not an Amendment address or number", key))
			continue
		}
		if !slices.Contains(AmendmentTypes, value) {
			problems = append(problems, fmt.Sprintf("%s: %q is not a type (%s)", key, value, strings.Join(AmendmentTypes, ", ")))
			continue
		}
		var matches []amendmentRow
		for _, r := range rows {
			if (byAddress && r.address == ref) || (!byAddress && r.number == ref) {
				matches = append(matches, r)
			}
		}
		switch {
		case len(matches) == 0:
			problems = append(problems, fmt.Sprintf("%s: no such Amendment", key))
		case len(matches) > 1:
			problems = append(problems, fmt.Sprintf("%s: matches %d Amendments", key, len(matches)))
		case named[matches[0].id]:
			problems = append(problems, fmt.Sprintf("%s: names %s twice", key, ref))
		case slices.Contains(AmendmentTypes, matches[0].kind):
			named[matches[0].id] = true
			problems = append(problems, fmt.Sprintf("%s: already has type %s", key, matches[0].kind))
		default:
			named[matches[0].id] = true
			resolved[matches[0].id] = value
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("%w: MigrateRecordShape: AmendmentTypeMap: %s", ErrBadRequest, strings.Join(problems, "; "))
	}
	return resolved, nil
}

// migrateAmendments cuts long summaries and maps amendment_type values, by
// typeMap (Amendment ID to type) first and the built-in rules second.
func migrateAmendments(ctx context.Context, exec DB, rep *RecordShapeMigrationReport, all []amendmentRow, typeMap map[string]string) error {
	for _, r := range all {
		if utf8.RuneCountInString(r.summary) > TitleMaxLength {
			cut := cutAtWord(r.summary, TitleMaxLength)
			body := r.body
			kept := !strings.Contains(body, r.summary)
			if kept {
				body = strings.TrimSpace(r.summary + "\n\n" + body)
			}
			if _, err := exec.ExecContext(ctx, `UPDATE smeldr_amendments SET summary = $1, body = $2 WHERE id = $3`, cut, body, r.id); err != nil {
				return fmt.Errorf("%w: MigrateRecordShape smeldr_amendments: %s", ErrInternal, err)
			}
			rep.Titles = append(rep.Titles, RecordShapeChange{Type: "Amendment", ID: r.id, Address: r.address, Field: "summary",
				Old: r.summary, New: cut, BodyKept: kept})
		}
		if slices.Contains(AmendmentTypes, r.kind) {
			continue
		}
		mapped, source := typeMap[r.id], "type_map"
		if mapped == "" {
			mapped, source = amendmentTypeMapping[r.kind], "rule"
		}
		if mapped == "" {
			rep.UnmappedAmendmentTypes = append(rep.UnmappedAmendmentTypes, RecordShapeNote{Type: "Amendment", ID: r.id, Address: r.address,
				Field: "amendment_type", Value: r.kind, Reason: "not a type in the closed list and no clear mapping; map it by hand"})
			continue
		}
		if _, err := exec.ExecContext(ctx, `UPDATE smeldr_amendments SET amendment_type = $1 WHERE id = $2`, mapped, r.id); err != nil {
			return fmt.Errorf("%w: MigrateRecordShape smeldr_amendments: %s", ErrInternal, err)
		}
		rep.AmendmentTypes = append(rep.AmendmentTypes, RecordShapeChange{Type: "Amendment", ID: r.id, Address: r.address,
			Field: "amendment_type", Old: r.kind, New: mapped, Source: source})
	}
	return nil
}

// firstTextLine returns text's first line with words, without markdown
// heading, list or quote marks and emphasis.
func firstTextLine(text string) string {
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "#>-*+ \t")
		line = strings.TrimSpace(strings.NewReplacer("**", "", "__", "", "`", "").Replace(line))
		if strings.IndexFunc(line, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0 {
			return line
		}
	}
	return ""
}

// cutAtWord returns s cut to at most limit characters, at the last word
// boundary when one falls in the second half, with no ellipsis.
func cutAtWord(s string, limit int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	cut := r[:limit]
	if i := strings.LastIndexFunc(string(cut), unicode.IsSpace); i > 0 && utf8.RuneCountInString(string(cut)[:i]) >= limit/2 {
		return strings.TrimRight(string(cut)[:i], " \t,;:-")
	}
	return strings.TrimSpace(string(cut))
}
