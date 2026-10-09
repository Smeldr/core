package smeldr

import (
	"context"
	"fmt"
)

// eventContentTypeRedefined is the event [App.RedefineContentType] sends after
// a runtime-defined type's schema changed: to the event stream on the
// [eventStreamChannelTypes] topic and to webhook endpoints subscribed to it,
// so a client that holds a schema (an MCP client, Cloud) can re-read it.
const eventContentTypeRedefined = "content_type.redefined"

// contentTypeEventData is the data object of [eventContentTypeRedefined].
type contentTypeEventData struct {
	Type      string `json:"type"` // always "content_type"
	Name      string `json:"name"`
	Reason    string `json:"reason,omitempty"`
	ActorID   string `json:"actor_id,omitempty"`
	ActorKind string `json:"actor_kind,omitempty"`
}

// RedefineContentType replaces the schema of a registered runtime-defined
// content type, in the store and in the running App, without a restart. It is
// [App.RedefineContentTypeVia] with no surface.
func (a *App) RedefineContentType(ctx context.Context, schema *ContentTypeSchema, reason string) (*TypeDescriptor, error) {
	return a.RedefineContentTypeVia(ctx, "", schema, reason)
}

// RedefineContentTypeVia replaces the schema of the registered runtime-defined
// type schema.TypeName. A redefinition may loosen and add, never take away or
// tighten what existing items rely on:
//   - allowed: the label, a field's role, format, description and relation,
//     a required field becoming optional, and new optional fields;
//   - refused, as a [*ValidationError] on the field with nothing saved:
//     removing a field, changing its type, making it required, adding a
//     required field, and any change of url_prefix (setting, moving or
//     removing a public prefix decides what is published, which a schema edit
//     must not do).
//
// An empty Label keeps the stored one. The stored ID and creation time are
// kept. On success the App uses the new schema at once: validation, slugs and
// stream routing (the "channel" role) of later writes. Existing items are not
// revalidated. The change is in this process's registry only: another process
// on the same database sees it after a restart. Redefinitions in one App run
// one at a time, so each is checked against the schema the previous one left;
// the public GET routes look the type up per request and serve the new
// descriptor at once.
//
// It writes one [ProvenanceRecord] (subject "ContentType" / the type name, verb
// "update", the caller as actor, surface, reason) and sends
// "content_type.redefined" on the "types" topic and to webhooks. reason is
// optional, trimmed and at most 1000 characters. An unknown type is
// [ErrNotFound]; a compiled one is [ErrBadRequest]. Gated like
// define_content_type (the "define-type" operation).
func (a *App) RedefineContentTypeVia(ctx context.Context, surface string, schema *ContentTypeSchema, reason string) (*TypeDescriptor, error) {
	if a.cfg.DB == nil {
		return nil, fmt.Errorf("smeldr: RedefineContentType requires Config.DB")
	}
	if schema == nil || schema.TypeName == "" {
		return nil, Err("type_name", "required")
	}
	// One redefinition at a time, from reading the current schema to swapping
	// in the new one: two concurrent ones checked against the same old schema
	// would both pass, and the later save would drop the earlier one's field.
	a.redefineMu.Lock()
	defer a.redefineMu.Unlock()
	desc := a.typeRegistry.Lookup(schema.TypeName)
	if desc == nil {
		return nil, fmt.Errorf("%w: content type %q not registered", ErrNotFound, schema.TypeName)
	}
	if desc.Kind != "content" || desc.Schema == nil {
		return nil, fmt.Errorf("%w: %q is not a runtime-defined type; only those can be redefined", ErrBadRequest, schema.TypeName)
	}
	reason, err := actReason(reason)
	if err != nil {
		return nil, err
	}
	old := desc.Schema
	next := *schema
	next.Kind = "content"
	if len(next.Fields) == 0 {
		next.Fields = []byte("[]")
	}
	if next.Label == "" {
		next.Label = old.Label
	}
	if err := ValidateSchemaDef(&next); err != nil {
		return nil, err
	}
	if next.URLPrefix != old.URLPrefix {
		return nil, Err("url_prefix", "cannot be changed by a redefinition: setting, moving or removing a public prefix changes what is published")
	}
	if err := checkRedefinition(old, &next); err != nil {
		return nil, err
	}
	next.ID = old.ID
	next.CreatedAt = old.CreatedAt
	if err := NewSchemaStore(a.cfg.DB).Save(ctx, &next); err != nil {
		return nil, fmt.Errorf("smeldr: RedefineContentType save: %w", err)
	}
	repo := NewDynamicTypeRepo(a.cfg.DB, next.TypeName, &next)
	nd := &TypeDescriptor{
		Name:   next.TypeName,
		Prefix: desc.Prefix,
		Schema: &next,
		Kind:   "content",
		Fetch:  repo.List,
	}
	a.typeRegistry.replace(nd)

	actorID, actorKind := actorFromContext(ctx)
	recordProvenance(ctx, a.provenanceStore, ProvenanceRecord{
		SubjectType: "ContentType",
		SubjectID:   next.TypeName,
		Verb:        "update",
		ActorKind:   actorKind,
		ActorID:     actorID,
		Surface:     surface,
		Reason:      reason,
	})
	dispatchEventFrom(ctx, a.webhookStore, a.webhookPool, a.eventBroadcaster, actorID, eventStreamChannelTypes,
		eventContentTypeRedefined, contentTypeEventData{
			Type:      "content_type",
			Name:      next.TypeName,
			Reason:    reason,
			ActorID:   actorID,
			ActorKind: actorKind,
		})
	return nd, nil
}

// checkRedefinition reports what next would take away from or tighten on old:
// a removed field, a changed type, a field made required, or a new required
// field, each as a [*ValidationError] on that field (all of them at once).
func checkRedefinition(old, next *ContentTypeSchema) error {
	oldFields, err := old.ParseFields()
	if err != nil {
		return fmt.Errorf("smeldr: stored schema of %q unreadable: %w", old.TypeName, err)
	}
	newFields, err := next.ParseFields()
	if err != nil {
		return Err("fields", "invalid JSON")
	}
	byName := make(map[string]SchemaField, len(newFields))
	for _, f := range newFields {
		byName[f.Name] = f
	}
	var errs []error
	known := make(map[string]bool, len(oldFields))
	for _, of := range oldFields {
		known[of.Name] = true
		nf, ok := byName[of.Name]
		switch {
		case !ok:
			errs = append(errs, Err(of.Name, "cannot be removed by a redefinition: existing items hold it"))
		case nf.Type != of.Type:
			errs = append(errs, Err(of.Name, fmt.Sprintf("type cannot change from %q to %q: existing items hold the old type", of.Type, nf.Type)))
		case nf.Required && !of.Required:
			errs = append(errs, Err(of.Name, "cannot become required: existing items may not have it"))
		}
	}
	for _, nf := range newFields {
		if !known[nf.Name] && nf.Required {
			errs = append(errs, Err(nf.Name, "a new field must be optional: existing items do not have it"))
		}
	}
	return Require(errs...)
}
