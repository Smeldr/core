package smeldr

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Titles and field limits on runtime-defined types (D112, D114): a record
// type's title-role field and any field with a declared MaxLength follow the
// same warning-then-refusal rule as the compiled types' titles.

// TitleFieldWarnings returns a title_field_missing warning for a record type
// (a content type with no URL prefix) that declares no string field with role
// "title"; nil for a web content type, a block type, or a record type that has
// one. A record type is named in one line (D112), so it declares its title
// field at define. [App.DefineContentType] refuses such a definition after
// [Config.TitleCutover]; a redefinition is never refused for it.
func TitleFieldWarnings(schema *ContentTypeSchema) []CreateWarning {
	if schema == nil || schema.Kind != "content" || !isAddressedSchema(schema) {
		return nil
	}
	fields, err := schema.ParseFields()
	if err != nil {
		return nil
	}
	for _, f := range fields {
		if f.Role == "title" && f.Type == "string" {
			return nil
		}
	}
	return []CreateWarning{{Code: "title_field_missing", Field: "fields",
		Message: fmt.Sprintf("record type %q declares no title field: give one string field the role \"title\" (D112), at most %d characters", schema.TypeName, TitleMaxLength)}}
}

// dynamicFieldWarnings returns the title and length warnings of a dynamic
// write. fields is the item's full field map after the write; touched reports
// whether the write set a field (every field on create, the patched ones on
// update), so an update is not warned about a field it did not change.
func dynamicFieldWarnings(schema *ContentTypeSchema, fields map[string]any, touched func(string) bool) []CreateWarning {
	if schema == nil {
		return nil
	}
	parsed, err := schema.ParseFields()
	if err != nil {
		return nil
	}
	record := schema.Kind == "content" && isAddressedSchema(schema)
	var ws []CreateWarning
	for _, f := range parsed {
		if !touched(f.Name) || f.Type != "string" {
			continue
		}
		value, _ := fields[f.Name].(string)
		n := utf8.RuneCountInString(value)
		if f.Role == "title" {
			if !record {
				continue
			}
			limit := TitleMaxLength
			if f.MaxLength > 0 {
				limit = f.MaxLength
			}
			switch {
			case strings.TrimSpace(value) == "":
				ws = append(ws, CreateWarning{Code: "title_missing", Field: f.Name,
					Message: fmt.Sprintf("%s is missing: %s", f.Name, TitleGuidance)})
			case n > limit:
				ws = append(ws, CreateWarning{Code: "title_too_long", Field: f.Name,
					Message: fmt.Sprintf("%s is %d characters, the limit is %d: %s", f.Name, n, limit, TitleGuidance)})
			}
			continue
		}
		if f.MaxLength > 0 && n > f.MaxLength {
			ws = append(ws, CreateWarning{Code: "field_too_long", Field: f.Name,
				Message: fmt.Sprintf("%s is %d characters, the limit is %d", f.Name, n, f.MaxLength)})
		}
	}
	return ws
}

// warningsCheck applies the cut to a write's warnings: before cutover (zero,
// or in the future) it returns them; after it, the first one is a
// [ValidationError] on its field.
func warningsCheck(ws []CreateWarning, cutover time.Time) ([]CreateWarning, error) {
	if len(ws) == 0 {
		return nil, nil
	}
	if !cutover.IsZero() && time.Now().After(cutover) {
		return nil, Err(ws[0].Field, ws[0].Message)
	}
	return ws, nil
}
