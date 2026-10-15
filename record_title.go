package smeldr

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Titles and closed vocabularies (D112, design/record-shape-v1.md P2, P6).

// TitleMaxLength is the longest title a record may carry, in characters.
const TitleMaxLength = 120

// TitleGuidance is the guided text of a title error, after
// [Config.TitleCutover]: what the field must hold, so a caller (or a model)
// can correct its call.
const TitleGuidance = "a title is required: one line of at most 120 characters, a short statement of what the record is (D112)"

// AmendmentTypes is the closed list of [Amendment.AmendmentType] values (D112).
var AmendmentTypes = []string{"feature", "fix", "docs", "chore", "test", "ci", "refactor", "release"}

// recordTitled is a record type whose title D112 requires: the JSON name of
// its title field and the value.
type recordTitled interface {
	recordTitle() (field, value string)
}

func (d Decision) recordTitle() (string, string)  { return "title", d.Title }
func (t Task) recordTitle() (string, string)      { return "title", t.Title }
func (g Goal) recordTitle() (string, string)      { return "title", g.Title }
func (a Amendment) recordTitle() (string, string) { return "summary", a.Summary }

// ContentTitle implements [Titled] for webhook bodies and signal events.
func (d Decision) ContentTitle() string { return d.Title }

// ContentTitle implements [Titled] for webhook bodies and signal events.
func (t Task) ContentTitle() string { return t.Title }

// ContentTitle implements [Titled] for webhook bodies and signal events.
func (g Goal) ContentTitle() string { return g.Title }

// ContentTitle implements [Titled]: an Amendment's summary is its title.
func (a Amendment) ContentTitle() string { return a.Summary }

// titleWarnings returns item's title warnings: title_missing for an empty or
// blank title, title_too_long for one longer than [TitleMaxLength]
// characters. Nil for a type with no title rule.
func titleWarnings(item any) []CreateWarning {
	t, ok := item.(recordTitled)
	if !ok {
		return nil
	}
	field, value := t.recordTitle()
	switch n := utf8.RuneCountInString(value); {
	case strings.TrimSpace(value) == "":
		return []CreateWarning{{Code: "title_missing", Field: field,
			Message: fmt.Sprintf("%s is missing: %s", field, TitleGuidance)}}
	case n > TitleMaxLength:
		return []CreateWarning{{Code: "title_too_long", Field: field,
			Message: fmt.Sprintf("%s is %d characters, the limit is %d: %s", field, n, TitleMaxLength, TitleGuidance)}}
	}
	return nil
}

// titleCheck applies the title rule to a write: before cutover (zero, or in
// the future) it returns the warnings; after it, a title warning is a
// [ValidationError] on the title field with [TitleGuidance].
func titleCheck(item any, cutover time.Time) ([]CreateWarning, error) {
	ws := titleWarnings(item)
	if len(ws) == 0 {
		return nil, nil
	}
	if !cutover.IsZero() && time.Now().After(cutover) {
		return nil, Err(ws[0].Field, ws[0].Message)
	}
	return ws, nil
}

// titleFieldTouched reports whether a partial update's fields name item's
// title field, matched the way encoding/json matches keys (any case).
func titleFieldTouched(item any, fields map[string]any) bool {
	t, ok := item.(recordTitled)
	if !ok {
		return false
	}
	field, _ := t.recordTitle()
	for k := range fields {
		if strings.EqualFold(k, field) {
			return true
		}
	}
	return false
}

// retiredFieldWarnings returns a deprecated_field warning for each retired
// field the request sent: today Amendment's pilot (D112).
func retiredFieldWarnings(item any, fields map[string]any) []CreateWarning {
	if _, ok := item.(*Amendment); !ok {
		return nil
	}
	for k := range fields {
		if strings.EqualFold(k, "pilot") {
			return []CreateWarning{{Code: "deprecated_field", Field: "pilot",
				Message: "pilot is retired (D112) and was not stored: band and actor say who did the work"}}
		}
	}
	return nil
}

// checkAmendmentType enforces the closed list on a create, and on an update
// that changes the value; an older row keeps its value until it is changed.
// An update that sends no value keeps the stored one, as amends does, so a
// full-replace PUT that does not know the field does not clear it.
func checkAmendmentType(existing, item *Amendment) error {
	if existing != nil && item.AmendmentType == "" {
		item.AmendmentType = existing.AmendmentType
	}
	if existing != nil && existing.AmendmentType == item.AmendmentType {
		return nil
	}
	if slices.Contains(AmendmentTypes, item.AmendmentType) {
		return nil
	}
	return Err("amendment_type", fmt.Sprintf("%q is not an amendment type: use one of %s (D112)",
		item.AmendmentType, strings.Join(AmendmentTypes, ", ")))
}

// titleChanged reports whether a full replace changes the record's title.
func titleChanged(existing, item any) bool {
	a, ok := existing.(recordTitled)
	b, ok2 := item.(recordTitled)
	if !ok || !ok2 {
		return false
	}
	_, before := a.recordTitle()
	_, after := b.recordTitle()
	return before != after
}

// titleWarningSeen dedupes the HTTP title-warning log to one line per actor,
// type, code and UTC day, as [noteDeprecatedUse] does for D110.
var titleWarningSeen sync.Map

// noteTitleWarnings logs title warnings for a surface whose response carries
// none (HTTP, D110 7.1), once per actor, type, code and day.
func noteTitleWarnings(ctx context.Context, typeName string, ws []CreateWarning) {
	for _, w := range ws {
		actorID, _ := actorFromContext(ctx)
		key := actorID + "|" + typeName + "|" + w.Code + "|" + time.Now().UTC().Format("2006-01-02")
		if _, seen := titleWarningSeen.LoadOrStore(key, true); seen {
			continue
		}
		slog.WarnContext(ctx, "smeldr: a record was written without a valid title (D112); refused after the title cutover",
			"token", actorID, "type", typeName, "code", w.Code, "field", w.Field)
	}
}

// setTitleCutover stores [Config.TitleCutover] on the module (App.Content).
func (m *Module[T]) setTitleCutover(t time.Time) { m.titleCutover = t }
