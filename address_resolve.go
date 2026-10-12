// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// addressRe is an address's one spelling: 1 to 4 letters, then a number with
// no leading zero.
var addressRe = regexp.MustCompile(`^([A-Za-z]{1,4})([1-9][0-9]*)$`)

// ParseAddress parses s as an address (D110): 1 to 4 letters and a number,
// such as "D110" or "inv27". It returns the prefix upper-cased and the
// number. A number of 0 or one with a leading zero ("D0", "D007") is not an
// address, so every address has exactly one spelling.
func ParseAddress(s string) (prefix string, no int64, ok bool) {
	m := addressRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", 0, false
	}
	n, err := strconv.ParseInt(m[2], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return strings.ToUpper(m[1]), n, true
}

// addressIdent returns ident as the canonical address of a type whose prefix
// is prefix, and false when ident is not one: it does not parse, or its prefix
// is another type's. So a slug such as "abcd1234" is never looked up as an
// address on a type whose prefix is "T".
func addressIdent(ident, prefix string) (string, bool) {
	p, no, ok := ParseAddress(ident)
	if !ok || prefix == "" || p != prefix {
		return "", false
	}
	return p + strconv.FormatInt(no, 10), true
}

// legacyHumanIDColumns maps a core type to its legacy human-ID column
// (T253). Transitional (D110): the resolvers try it last, after address, slug
// and ID, so a record that has no address yet (before the migration) is still
// found by its old number; the D110 Phase 4 cut removes it. Explicit, never
// derived from a naming convention.
var legacyHumanIDColumns = map[string]string{
	"Task":      "task_id",
	"Goal":      "goal_id",
	"Decision":  "decision_number",
	"Amendment": "amendment_number",
}

// identCandidates is the D110 lookup order for ident on a core type's table:
// address (only when ident carries the type's own prefix), slug, ID, then the
// transitional legacy column.
func identCandidates(typeName, ident string) [][2]string {
	var c [][2]string
	if addr, ok := addressIdent(ident, coreAddressPrefixes[typeName]); ok {
		c = append(c, [2]string{"address", addr})
	}
	c = append(c, [2]string{"slug", ident}, [2]string{"id", ident})
	if col, ok := legacyHumanIDColumns[typeName]; ok {
		c = append(c, [2]string{col, ident})
	}
	return c
}

// legacyHumanIDField is implemented by the four core types with a legacy
// human-ID field.
type legacyHumanIDField interface {
	legacyHumanID() (field string, value *string)
}

func (d *Decision) legacyHumanID() (string, *string)  { return "decision_number", &d.DecisionNumber }
func (t *Task) legacyHumanID() (string, *string)      { return "task_id", &t.TaskID }
func (g *Goal) legacyHumanID() (string, *string)      { return "goal_id", &g.GoalID }
func (a *Amendment) legacyHumanID() (string, *string) { return "amendment_number", &a.AmendmentNumber }

// mirrorLegacyHumanID applies the transition rule (D110 7.1) to a new item
// that has just been given address: a different value the caller sent in the
// legacy field moves to external_ref (unless external_ref was sent too), and
// the legacy field is set to the address, so a reader still on the legacy
// field sees the right number until the cut. It returns the legacy field's
// name and the value that was sent ("" when none).
func mirrorLegacyHumanID(item any, address string) (field, sent string) {
	l, ok := item.(legacyHumanIDField)
	if !ok {
		return "", ""
	}
	field, p := l.legacyHumanID()
	sent = strings.TrimSpace(*p)
	if sent != "" && sent != address {
		if r, ok := item.(interface{ setExternalRefIfEmpty(string) bool }); ok {
			r.setExternalRefIfEmpty(sent)
		}
	}
	*p = address
	return field, sent
}

// legacySent returns the legacy human-ID field of an addressed module's new
// item and the value the caller sent in it ("" when none), read before the
// create mirrors the address into it.
func (m *Module[T]) legacySent(item T) (field, sent string) {
	l, ok := any(item).(legacyHumanIDField)
	if !ok || !m.addressed {
		return "", ""
	}
	field, p := l.legacyHumanID()
	return field, strings.TrimSpace(*p)
}

// setExternalRefIfEmpty sets ExternalRef to v when it is empty and reports
// whether it did.
func (a *RecordAddress) setExternalRefIfEmpty(v string) bool {
	if a.ExternalRef != "" {
		return false
	}
	a.ExternalRef = v
	return true
}

// CreateWarning is one entry of a create result's "warnings" array (D110
// 7.1): Code "deprecated_field", the Field sent, a Message saying what
// happened to its value, and the Address the server assigned.
type CreateWarning struct {
	Code    string `json:"code"`
	Field   string `json:"field"`
	Message string `json:"message"`
	Address string `json:"address"`
}

// legacyFieldWarning says what became of a value sent in a legacy field.
func legacyFieldWarning(field, sent, address, externalRef string) CreateWarning {
	var what string
	switch {
	case sent == address:
		what = "it equals the assigned address, so it was not stored separately"
	case externalRef == sent:
		what = fmt.Sprintf("it was not used as the address; it is stored in external_ref (%q)", sent)
	default:
		what = "it was not used as the address, and not stored, because external_ref was sent as well"
	}
	return CreateWarning{
		Code:  "deprecated_field",
		Field: field,
		Message: fmt.Sprintf("%s is deprecated (D110): the server assigns the address (%s) and returns it in the result; %s. "+
			"%s will be removed at the D110 cut; send your own numbering as external_ref.", field, address, what, field),
		Address: address,
	}
}

// withCreateWarnings returns item's JSON object with a "warnings" array added,
// the shape mcp renders as the create result (D110 7.1). Only a create that
// produced a warning returns it, so every other create's result is unchanged.
func withCreateWarnings(item any, warnings []CreateWarning) map[string]any {
	out := map[string]any{}
	if b, err := json.Marshal(item); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	out["warnings"] = warnings
	return out
}

// externalRefOf returns item's external reference, "" when it has none.
func externalRefOf(item any) string {
	if r, ok := item.(interface{ itemExternalRef() string }); ok {
		return r.itemExternalRef()
	}
	return ""
}

// deprecatedUse remembers which token used which deprecated input on which UTC
// day, so each is logged once.
var deprecatedUse sync.Map

// noteDeprecatedUse logs, once per token, per field, per UTC day, that the
// caller in ctx still sent a deprecated input (D110 7.1), naming the token, so
// every caller on the old form is known before the cut.
func noteDeprecatedUse(ctx context.Context, typeName, field string) {
	actorID, _ := actorFromContext(ctx)
	key := actorID + "|" + typeName + "|" + field + "|" + time.Now().UTC().Format("2006-01-02")
	if _, seen := deprecatedUse.LoadOrStore(key, true); seen {
		return
	}
	slog.WarnContext(ctx, "smeldr: a deprecated input was sent (D110); it is removed at the cut",
		"token", actorID, "type", typeName, "field", field)
}

// RetiredFieldGuidance returns the guiding error for a retired legacy
// human-ID field (D110 7.1), for the cut, when a create that still sends one is
// refused: the message a caller (or a model) can correct its call from, and the
// data object carried with the error. ok is false for any other field. It is
// prepared for the cut and not used before it.
func RetiredFieldGuidance(field string) (message string, data map[string]any, ok bool) {
	switch field {
	case "decision_number", "task_id", "goal_id", "amendment_number":
	default:
		return "", nil, false
	}
	return fmt.Sprintf("%s is no longer accepted: addresses are assigned by the server (D110), and the new item's address is returned in the result. To keep your own number, send it as external_ref.", field),
		map[string]any{"field": field, "replacement": "external_ref", "decision": "D110"}, true
}

// lookupAddressByID reads the address of the item of typeName with this ID,
// for an event that did not load the item. "" when it has none or the read
// fails; a failure is logged at debug level, never returned (an event is
// fail-open).
func lookupAddressByID(ctx context.Context, db DB, typeName, id string, dynamic bool) string {
	if db == nil || id == "" {
		return ""
	}
	table := ""
	for _, t := range orchestrationAddressTables {
		if t.typeName == typeName {
			table = t.table
		}
	}
	if table == "" && dynamic {
		table = "smeldr_dynamic_content"
	}
	if table == "" {
		return ""
	}
	var address string
	err := db.QueryRowContext(ctx, `SELECT address FROM `+table+` WHERE id = $1`, id).Scan(&address)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		slog.DebugContext(ctx, "smeldr: the address for a transition event could not be read; the event omits it",
			"type", typeName, "id", id, "error", err)
	}
	return address
}

// transitionAddress is the address a transition event carries: the loaded
// item's, or, when the event did not load it, one indexed read by ID, so a
// stream consumer sees the address on a transition as it does on a create.
func (a *App) transitionAddress(ctx context.Context, t statusTransition) string {
	if t.item != nil {
		return addressOf(t.item)
	}
	dynamic := false
	if a.typeRegistry != nil {
		if desc := a.typeRegistry.Lookup(t.typeName); desc != nil {
			dynamic = desc.Kind == "content"
		}
	}
	return lookupAddressByID(ctx, a.cfg.DB, t.typeName, t.id, dynamic)
}
