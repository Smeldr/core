package smeldr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DynamicTypeRepo is a per-type repository for runtime-defined content types.
// It wraps the shared smeldr_dynamic_content table, filtering every query by
// type_name. Obtain one from [App.DynamicContentRepo] or [NewDynamicTypeRepo].
type DynamicTypeRepo struct {
	db       DB
	typeName string
	schema   *ContentTypeSchema // used for title-Role slug generation; may be nil
	rs       *RoleStore         // nil unless WithGovernance was called
}

// NewDynamicTypeRepo returns a DynamicTypeRepo bound to the given type name.
// schema may be nil; when non-nil its title-Role field drives auto-slug generation.
func NewDynamicTypeRepo(db DB, typeName string, schema *ContentTypeSchema) *DynamicTypeRepo {
	return &DynamicTypeRepo{db: db, typeName: typeName, schema: schema}
}

// WithGovernance returns a shallow copy of r configured to enforce required_role
// checks on [DynamicTypeRepo.SetStatus]. Pass nil to obtain a copy without
// governance enforcement (identical to the default state).
//
// When the caller does not provide a smeldr.Context (e.g. system-initiated code
// using a plain context.Context), the actorID is empty and the required_role
// check is skipped, matching the behaviour of any other non-authenticated caller.
func (r *DynamicTypeRepo) WithGovernance(rs *RoleStore) *DynamicTypeRepo {
	cp := *r
	cp.rs = rs
	return &cp
}

// CreateDraft inserts a new DynamicNode with status Draft. The slug is derived
// from the field with Role "title"; collisions are resolved by appending -2, -3,
// and so on. The node ID and timestamps are set automatically.
// Returns a [*ValidationError] when the fields map does not conform to the schema.
func (r *DynamicTypeRepo) CreateDraft(ctx context.Context, fields map[string]any) (*DynamicNode, error) {
	if ve := ValidateFields(r.schema, fields); ve != nil {
		return nil, ve
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("smeldr: CreateDraft marshal: %w", err)
	}
	base := titleSlug(r.schema, fields)
	slug := r.uniqueSlug(ctx, base)
	now := time.Now().UTC()
	node := &DynamicNode{
		Node: Node{
			ID:        NewID(),
			Slug:      slug,
			Status:    Draft,
			CreatedAt: now,
			UpdatedAt: now,
		},
		TypeName: r.typeName,
		Fields:   json.RawMessage(raw),
	}
	repo := NewDynamicContentRepo(r.db)
	if err := repo.Save(ctx, node); err != nil {
		return nil, fmt.Errorf("smeldr: CreateDraft save: %w", err)
	}
	return node, nil
}

// GetBySlug returns the node with the given slug for this type, at any status.
// Returns [ErrNotFound] when no matching node exists.
func (r *DynamicTypeRepo) GetBySlug(ctx context.Context, slug string) (*DynamicNode, error) {
	rows, err := Query[*DynamicNode](ctx, r.db,
		"SELECT * FROM smeldr_dynamic_content WHERE type_name = $1 AND slug = $2",
		r.typeName, slug)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	return rows[0], nil
}

// GetByID returns the node with the given ID for this type, at any status.
// Returns [ErrNotFound] when no matching node exists.
func (r *DynamicTypeRepo) GetByID(ctx context.Context, id string) (*DynamicNode, error) {
	rows, err := Query[*DynamicNode](ctx, r.db,
		"SELECT * FROM smeldr_dynamic_content WHERE id = $1 AND type_name = $2",
		id, r.typeName)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	return rows[0], nil
}

// List returns items for this type as type-erased maps. Each map contains the
// stored fields merged with node metadata keys (ID, Slug, TypeName, Status,
// PublishedAt). ListOptions controls status filter, ordering, and pagination.
func (r *DynamicTypeRepo) List(ctx context.Context, opts ListOptions) ([]map[string]any, error) {
	query := "SELECT * FROM smeldr_dynamic_content WHERE type_name = $1"
	args := []any{r.typeName}
	n := 2

	if len(opts.Status) > 0 {
		placeholders := make([]string, len(opts.Status))
		for i, s := range opts.Status {
			placeholders[i] = fmt.Sprintf("$%d", n)
			args = append(args, string(s))
			n++
		}
		query += " AND status IN (" + strings.Join(placeholders, ", ") + ")"
	}

	switch opts.OrderBy {
	case "CreatedAt", "created_at":
		if opts.Desc {
			query += " ORDER BY created_at DESC"
		} else {
			query += " ORDER BY created_at ASC"
		}
	default:
		if opts.Desc {
			query += " ORDER BY published_at DESC, created_at DESC"
		} else {
			query += " ORDER BY published_at DESC, created_at DESC"
		}
	}

	if opts.PerPage > 0 {
		query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", n, n+1)
		args = append(args, opts.PerPage, opts.Offset())
	}

	nodes, err := Query[*DynamicNode](ctx, r.db, query, args...)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(nodes))
	for _, node := range nodes {
		m := make(map[string]any)
		if len(node.Fields) > 0 {
			if err := json.Unmarshal(node.Fields, &m); err != nil {
				continue
			}
		}
		m["ID"] = node.ID
		m["Slug"] = node.Slug
		m["TypeName"] = node.TypeName
		m["Status"] = string(node.Status)
		if !node.PublishedAt.IsZero() {
			m["PublishedAt"] = node.PublishedAt
		}
		out = append(out, m)
	}
	return out, nil
}

// UpdateFields merges patch onto the stored fields for the node with the given
// ID. Keys present in patch overwrite their stored counterparts; absent keys
// are preserved. UpdatedAt is set to the current UTC time.
// Returns a [*ValidationError] when the patch contains unknown or mistyped fields.
func (r *DynamicTypeRepo) UpdateFields(ctx context.Context, id string, patch map[string]any) error {
	if ve := ValidatePartialFields(r.schema, patch); ve != nil {
		return ve
	}
	node, err := r.GetByID(ctx, id)
	if err != nil {
		return err
	}
	existing := make(map[string]any)
	if len(node.Fields) > 0 {
		if err := json.Unmarshal(node.Fields, &existing); err != nil {
			return fmt.Errorf("smeldr: UpdateFields unmarshal existing: %w", err)
		}
	}
	for k, v := range patch {
		existing[k] = v
	}
	merged, err := json.Marshal(existing)
	if err != nil {
		return fmt.Errorf("smeldr: UpdateFields marshal: %w", err)
	}
	_, err = r.db.ExecContext(ctx,
		"UPDATE smeldr_dynamic_content SET fields = $1, updated_at = $2 WHERE id = $3 AND type_name = $4",
		json.RawMessage(merged), time.Now().UTC(), id, r.typeName)
	return err
}

// SetStatus transitions the node to the given status. When transitioning to
// Published and PublishedAt is zero, it is set to the current UTC time.
// The transition is validated against the registered flow before the update is applied.
//
// When [WithGovernance] has been called and the transition carries a required_role,
// the actor's token ID is extracted from ctx if it implements the smeldr.Context
// interface. Callers that pass a plain context.Context (system-initiated paths)
// get an empty actorID, which skips the required_role check.
func (r *DynamicTypeRepo) SetStatus(ctx context.Context, id string, status Status) error {
	node, err := r.GetByID(ctx, id)
	if err != nil {
		return err
	}
	// Extract actor ID if ctx carries a smeldr.Context (MCP request path).
	// Plain context.Context (system-initiated paths) → actorID "" → skip required_role.
	type smeldrCtxAccessor interface {
		User() User
	}
	actorID := ""
	if sc, ok := ctx.(smeldrCtxAccessor); ok {
		actorID = sc.User().ID
	}
	if err := validateTransition(ctx, r.db, r.rs, actorID, r.typeName, string(node.Status), string(status)); err != nil {
		return err
	}
	if err := applyConflictPolicy(ctx, r.db, nil, r.typeName, string(status), id); err != nil {
		return err
	}
	now := time.Now().UTC()
	publishedAt := node.PublishedAt
	if status == Published && publishedAt.IsZero() {
		publishedAt = now
	}
	_, err = r.db.ExecContext(ctx,
		"UPDATE smeldr_dynamic_content SET status = $1, published_at = $2, updated_at = $3 WHERE id = $4 AND type_name = $5",
		string(status), publishedAt, now, id, r.typeName)
	if err != nil {
		return err
	}
	fireAsyncTriggers(ctx, r.db, r.typeName, string(node.Status), string(status), id)
	return nil
}

// ScheduleContent transitions the node to [Scheduled] status and records the
// time at which the scheduler should publish it. The transition is validated
// against the registered state flow (same as [SetStatus]).
func (r *DynamicTypeRepo) ScheduleContent(ctx context.Context, id string, scheduledAt time.Time) error {
	node, err := r.GetByID(ctx, id)
	if err != nil {
		return err
	}
	type smeldrCtxAccessor interface {
		User() User
	}
	actorID := ""
	if sc, ok := ctx.(smeldrCtxAccessor); ok {
		actorID = sc.User().ID
	}
	if err := validateTransition(ctx, r.db, r.rs, actorID, r.typeName, string(node.Status), string(Scheduled)); err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = r.db.ExecContext(ctx,
		"UPDATE smeldr_dynamic_content SET status = $1, scheduled_at = $2, updated_at = $3 WHERE id = $4 AND type_name = $5",
		string(Scheduled), scheduledAt, now, id, r.typeName)
	if err != nil {
		return err
	}
	fireAsyncTriggers(ctx, r.db, r.typeName, string(node.Status), string(Scheduled), id)
	return nil
}

// — slug helpers ——————————————————————————————————————————————————————————————

var slugClean = regexp.MustCompile(`[^a-z0-9]+`)

// titleSlug derives an initial slug from the schema's title-Role field value.
// Falls back to "item" when no title field is found or its value is empty.
func titleSlug(schema *ContentTypeSchema, fields map[string]any) string {
	if schema != nil {
		parsed, err := schema.ParseFields()
		if err == nil {
			for _, f := range parsed {
				if f.Role == "title" {
					if v, ok := fields[f.Name].(string); ok && v != "" {
						s := slugClean.ReplaceAllString(strings.ToLower(v), "-")
						s = strings.Trim(s, "-")
						if s != "" {
							return s
						}
					}
					break
				}
			}
		}
	}
	return "item"
}

// PluralSnake returns the English plural of a snake_case or lower-cased type
// name. Consonant+y endings use the -ies rule; all others get a plain -s.
// Utility function for generating URL suggestions (e.g. "story" → "stories");
// not used internally for routing (operators set URLPrefix on ContentTypeSchema).
func PluralSnake(name string) string {
	if len(name) >= 2 && name[len(name)-1] == 'y' {
		prev := name[len(name)-2]
		vowels := "aeiou"
		if !strings.ContainsRune(vowels, rune(prev)) {
			return name[:len(name)-1] + "ies"
		}
	}
	return name + "s"
}

func (r *DynamicTypeRepo) uniqueSlug(ctx context.Context, base string) string {
	if !r.slugExists(ctx, base) {
		return base
	}
	for i := 2; i <= 100; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !r.slugExists(ctx, candidate) {
			return candidate
		}
	}
	return base + "-" + NewID()[:8]
}

func (r *DynamicTypeRepo) slugExists(ctx context.Context, slug string) bool {
	rows, err := Query[*DynamicNode](ctx, r.db,
		"SELECT id FROM smeldr_dynamic_content WHERE type_name = $1 AND slug = $2",
		r.typeName, slug)
	return err == nil && len(rows) > 0
}

// — App methods ———————————————————————————————————————————————————————————————

// DefineContentType registers a new runtime-defined content type. The schema is
// written to smeldr_content_type_schemas (kind forced to "content") and the type
// is registered in the App's ContentTypeRegistry with a Fetch function backed by
// [DynamicTypeRepo.List]. When schema.URLPrefix is non-empty, public GET routes
// are registered on the app's mux for that prefix. Returns an error on invalid
// schema, DB failure, or name/prefix collision; never panics.
func (a *App) DefineContentType(ctx context.Context, schema *ContentTypeSchema) (*TypeDescriptor, error) {
	if a.cfg.DB == nil {
		return nil, fmt.Errorf("smeldr: DefineContentType requires Config.DB")
	}
	if schema.TypeName == "" {
		return nil, fmt.Errorf("smeldr: DefineContentType: TypeName is required")
	}
	schema.Kind = "content"
	if len(schema.Fields) == 0 {
		schema.Fields = json.RawMessage("[]")
	}
	if err := ValidateSchemaDef(schema); err != nil {
		return nil, err
	}
	if a.typeRegistry.Lookup(schema.TypeName) != nil {
		return nil, fmt.Errorf("smeldr: content type %q already registered", schema.TypeName)
	}
	prefix := schema.URLPrefix // may be empty; empty = no public URL
	if prefix != "" && a.typeRegistry.LookupByPrefix(prefix) != nil {
		return nil, fmt.Errorf("smeldr: content type prefix %q already claimed", prefix)
	}
	store := NewSchemaStore(a.cfg.DB)
	if err := store.Save(ctx, schema); err != nil {
		return nil, fmt.Errorf("smeldr: DefineContentType save: %w", err)
	}
	repo := NewDynamicTypeRepo(a.cfg.DB, schema.TypeName, schema)
	desc := &TypeDescriptor{
		Name:   schema.TypeName,
		Prefix: prefix,
		Schema: schema,
		Kind:   "content",
		Fetch:  repo.List,
	}
	a.typeRegistry.Register(desc)
	if prefix != "" {
		appRef := a
		a.mux.Handle("GET "+prefix, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			serveDynamicList(w, r, appRef, desc)
		}))
		a.mux.Handle("GET "+prefix+"/{slug}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			serveDynamicItem(w, r, appRef, desc, r.PathValue("slug"))
		}))
		insertDynamicRoutes(ctx, a.cfg.DB, schema.TypeName, prefix)
		if a.llmsStore != nil {
			a.llmsStore.registerCompact()
			a.llmsStore.SetCompact(prefix, []LLMsEntry{})
		}
	}
	return desc, nil
}

// DynamicContentRepo returns a [DynamicTypeRepo] for the named runtime-defined
// content type. Returns an error when the type is not registered, is a compiled
// type (not runtime-defined), or Config.DB is nil.
func (a *App) DynamicContentRepo(typeName string) (*DynamicTypeRepo, error) {
	desc := a.typeRegistry.Lookup(typeName)
	if desc == nil {
		return nil, fmt.Errorf("smeldr: content type %q not registered", typeName)
	}
	if desc.Kind != "content" {
		return nil, fmt.Errorf("smeldr: %q is a compiled type; use its module directly", typeName)
	}
	if a.cfg.DB == nil {
		return nil, fmt.Errorf("smeldr: DynamicContentRepo requires Config.DB")
	}
	repo := NewDynamicTypeRepo(a.cfg.DB, typeName, desc.Schema)
	if a.governance != nil {
		repo = repo.WithGovernance(a.governance)
	}
	return repo, nil
}

// loadDynamicTypes queries smeldr_content_type_schemas for all kind='content'
// rows and registers each as a TypeDescriptor backed by [DynamicTypeRepo.List].
// When a schema has a non-empty URLPrefix, public GET routes are registered on
// the app's mux for that prefix. Idempotent; skips types already in the registry.
// Called by Handler() when ServeDynamicContent was called.
func (a *App) loadDynamicTypes(ctx context.Context) {
	if a.dynamicTypesLoaded || a.cfg.DB == nil {
		return
	}
	a.dynamicTypesLoaded = true
	store := NewSchemaStore(a.cfg.DB)
	schemas, err := store.AllByKind(ctx, "content")
	if err != nil {
		slog.WarnContext(ctx, "smeldr: loadDynamicTypes", "err", err)
		return
	}
	for _, schema := range schemas {
		if a.typeRegistry.Lookup(schema.TypeName) != nil {
			continue
		}
		prefix := schema.URLPrefix // may be empty; empty = no public URL
		if prefix != "" && a.typeRegistry.LookupByPrefix(prefix) != nil {
			slog.WarnContext(ctx, "smeldr: loadDynamicTypes prefix collision",
				"type", schema.TypeName, "prefix", prefix)
			continue
		}
		repo := NewDynamicTypeRepo(a.cfg.DB, schema.TypeName, schema)
		desc := &TypeDescriptor{
			Name:   schema.TypeName,
			Prefix: prefix,
			Schema: schema,
			Kind:   "content",
			Fetch:  repo.List,
		}
		a.typeRegistry.Register(desc)
		if prefix != "" {
			appRef := a
			d := desc // capture loop variable
			a.mux.Handle("GET "+prefix, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				serveDynamicList(w, r, appRef, d)
			}))
			a.mux.Handle("GET "+prefix+"/{slug}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				serveDynamicItem(w, r, appRef, d, r.PathValue("slug"))
			}))
			insertDynamicRoutes(ctx, a.cfg.DB, schema.TypeName, prefix)
			if a.llmsStore != nil {
				a.llmsStore.registerCompact()
				a.llmsStore.SetCompact(prefix, []LLMsEntry{})
			}
		}
	}
}

// insertDynamicRoutes writes list and item rows for a dynamic content type into
// smeldr_routes. Uses INSERT ... ON CONFLICT DO NOTHING so it is safe to call
// on every restart (idempotent). No-op when prefix is empty or DB is nil.
func insertDynamicRoutes(ctx context.Context, db DB, typeName, prefix string) {
	if prefix == "" || db == nil {
		return
	}
	typeNames, _ := json.Marshal([]string{typeName})
	now := time.Now().UTC()
	db.ExecContext(ctx, //nolint:errcheck
		`INSERT INTO smeldr_routes
		    (id, path_pattern, route_type, view, type_names, created_at, updated_at)
		 VALUES
		    ($1, $2,           'content',  'list', $3,        $4,         $5),
		    ($6, $7,           'content',  'item', $8,        $9,         $10)
		 ON CONFLICT (path_pattern) DO NOTHING`,
		NewID(), prefix, string(typeNames), now, now,
		NewID(), prefix+"/{slug}", string(typeNames), now, now,
	)
}

// — public HTTP handlers ——————————————————————————————————————————————————————

// serveDynamicList serves a JSON list of Published items for the given type.
// Called from the type-specific GET /{prefix} route registered by DefineContentType.
func serveDynamicList(w http.ResponseWriter, r *http.Request, a *App, desc *TypeDescriptor) {
	opts := listOptsFromRequest(r)
	opts.Status = []Status{Published}
	items, err := desc.Fetch(r.Context(), opts)
	if err != nil {
		slog.WarnContext(r.Context(), "smeldr: dynamic list", "type", desc.Name, "err", err)
		WriteError(w, r, ErrInternal)
		return
	}
	if items == nil {
		items = []map[string]any{}
	}
	writeDynamicJSON(w, map[string]any{"items": items, "total": len(items)})
}

// serveDynamicItem serves a single Published item by slug as JSON.
// Called from the type-specific GET /{prefix}/{slug} route registered by DefineContentType.
func serveDynamicItem(w http.ResponseWriter, r *http.Request, a *App, desc *TypeDescriptor, slug string) {
	repo, err := a.DynamicContentRepo(desc.Name)
	if err != nil {
		WriteError(w, r, ErrInternal)
		return
	}
	node, err := repo.GetBySlug(r.Context(), slug)
	if err != nil || node.Status != Published {
		WriteError(w, r, ErrNotFound)
		return
	}
	m := make(map[string]any)
	if len(node.Fields) > 0 {
		_ = json.Unmarshal(node.Fields, &m)
	}
	m["ID"] = node.ID
	m["Slug"] = node.Slug
	m["TypeName"] = node.TypeName
	m["Status"] = string(node.Status)
	if !node.PublishedAt.IsZero() {
		m["PublishedAt"] = node.PublishedAt
	}
	writeDynamicJSON(w, m)
}

func listOptsFromRequest(r *http.Request) ListOptions {
	q := r.URL.Query()
	var opts ListOptions
	if v := q.Get("page"); v != "" {
		opts.Page, _ = strconv.Atoi(v)
	}
	if v := q.Get("per_page"); v != "" {
		opts.PerPage, _ = strconv.Atoi(v)
	}
	if v := q.Get("order_by"); v != "" {
		opts.OrderBy = v
	}
	if v := q.Get("desc"); v == "1" || v == "true" {
		opts.Desc = true
	}
	return opts
}

func writeDynamicJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("smeldr: writeDynamicJSON encode", "err", err)
	}
}

// — /_content/ admin HTTP handlers ————————————————————————————————————————————

// newDefineTypeHandler serves POST /_content/types — defines a new content type.
// Requires Admin role.
func newDefineTypeHandler(a *App, auth AuthFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.authenticate(r)
		if !ok || !HasRole(user.Roles, Admin) {
			WriteError(w, r, ErrForbidden)
			return
		}
		var req struct {
			TypeName  string          `json:"type_name"`
			Label     string          `json:"label"`
			URLPrefix string          `json:"url_prefix"`
			Fields    json.RawMessage `json:"fields"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, r, ErrBadRequest)
			return
		}
		schema := &ContentTypeSchema{
			TypeName:  req.TypeName,
			Label:     req.Label,
			URLPrefix: req.URLPrefix,
			Fields:    req.Fields,
		}
		desc, err := a.DefineContentType(r.Context(), schema)
		if err != nil {
			slog.WarnContext(r.Context(), "smeldr: define_content_type", "err", err)
			WriteError(w, r, ErrBadRequest)
			return
		}
		writeDynamicJSON(w, map[string]any{
			"type_name": desc.Name,
			"prefix":    desc.Prefix,
			"kind":      desc.Kind,
		})
	})
}

// newAdminListHandler serves GET /_content/{type} — lists items at any status.
// Supports ?status= filter. Requires Editor role.
func newAdminListHandler(a *App, auth AuthFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.authenticate(r)
		if !ok || !HasRole(user.Roles, Editor) {
			WriteError(w, r, ErrForbidden)
			return
		}
		typeName := r.PathValue("type")
		desc := a.typeRegistry.Lookup(typeName)
		if desc == nil || desc.Kind != "content" {
			WriteError(w, r, ErrNotFound)
			return
		}
		repo, err := a.DynamicContentRepo(desc.Name)
		if err != nil {
			WriteError(w, r, ErrInternal)
			return
		}
		opts := listOptsFromRequest(r)
		if sv := r.URL.Query().Get("status"); sv != "" {
			opts.Status = []Status{Status(sv)}
		}
		items, err := repo.List(r.Context(), opts)
		if err != nil {
			slog.WarnContext(r.Context(), "smeldr: admin list", "type", desc.Name, "err", err)
			WriteError(w, r, ErrInternal)
			return
		}
		if items == nil {
			items = []map[string]any{}
		}
		writeDynamicJSON(w, map[string]any{"items": items, "total": len(items)})
	})
}

// newAdminGetHandler serves GET /_content/{type}/{id} — gets a node by ID.
// Requires Editor role.
func newAdminGetHandler(a *App, auth AuthFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.authenticate(r)
		if !ok || !HasRole(user.Roles, Editor) {
			WriteError(w, r, ErrForbidden)
			return
		}
		typeName := r.PathValue("type")
		desc := a.typeRegistry.Lookup(typeName)
		if desc == nil || desc.Kind != "content" {
			WriteError(w, r, ErrNotFound)
			return
		}
		repo, err := a.DynamicContentRepo(desc.Name)
		if err != nil {
			WriteError(w, r, ErrInternal)
			return
		}
		id := r.PathValue("id")
		node, err := repo.GetByID(r.Context(), id)
		if err != nil {
			WriteError(w, r, ErrNotFound)
			return
		}
		m := nodeToMap(node)
		writeDynamicJSON(w, m)
	})
}

// newCreateContentHandler serves POST /_content/{type} — creates a draft.
// Requires Editor role.
func newCreateContentHandler(a *App, auth AuthFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.authenticate(r)
		if !ok || !HasRole(user.Roles, Editor) {
			WriteError(w, r, ErrForbidden)
			return
		}
		typeName := r.PathValue("type")
		desc := a.typeRegistry.Lookup(typeName)
		if desc == nil || desc.Kind != "content" {
			WriteError(w, r, ErrNotFound)
			return
		}
		repo, err := a.DynamicContentRepo(desc.Name)
		if err != nil {
			WriteError(w, r, ErrInternal)
			return
		}
		var fields map[string]any
		if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
			WriteError(w, r, ErrBadRequest)
			return
		}
		node, err := repo.CreateDraft(r.Context(), fields)
		if err != nil {
			slog.WarnContext(r.Context(), "smeldr: create_content", "type", desc.Name, "err", err)
			WriteError(w, r, ErrInternal)
			return
		}
		w.WriteHeader(http.StatusCreated)
		writeDynamicJSON(w, nodeToMap(node))
	})
}

// newUpdateContentHandler serves PATCH /_content/{type}/{id} — patches fields.
// Requires Editor role.
func newUpdateContentHandler(a *App, auth AuthFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.authenticate(r)
		if !ok || !HasRole(user.Roles, Editor) {
			WriteError(w, r, ErrForbidden)
			return
		}
		typeName := r.PathValue("type")
		desc := a.typeRegistry.Lookup(typeName)
		if desc == nil || desc.Kind != "content" {
			WriteError(w, r, ErrNotFound)
			return
		}
		repo, err := a.DynamicContentRepo(desc.Name)
		if err != nil {
			WriteError(w, r, ErrInternal)
			return
		}
		id := r.PathValue("id")
		var patch map[string]any
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			WriteError(w, r, ErrBadRequest)
			return
		}
		if err := repo.UpdateFields(r.Context(), id, patch); err != nil {
			if err == ErrNotFound {
				WriteError(w, r, ErrNotFound)
			} else {
				slog.WarnContext(r.Context(), "smeldr: update_content", "id", id, "err", err)
				WriteError(w, r, ErrInternal)
			}
			return
		}
		node, err := repo.GetByID(r.Context(), id)
		if err != nil {
			WriteError(w, r, ErrInternal)
			return
		}
		writeDynamicJSON(w, nodeToMap(node))
	})
}

// newSetStatusHandler serves POST /_content/{type}/{id}/status — sets status.
// Requires Editor role.
func newSetStatusHandler(a *App, auth AuthFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.authenticate(r)
		if !ok || !HasRole(user.Roles, Editor) {
			WriteError(w, r, ErrForbidden)
			return
		}
		typeName := r.PathValue("type")
		desc := a.typeRegistry.Lookup(typeName)
		if desc == nil || desc.Kind != "content" {
			WriteError(w, r, ErrNotFound)
			return
		}
		repo, err := a.DynamicContentRepo(desc.Name)
		if err != nil {
			WriteError(w, r, ErrInternal)
			return
		}
		id := r.PathValue("id")
		var body struct {
			Status string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			WriteError(w, r, ErrBadRequest)
			return
		}
		if body.Status == "" {
			WriteError(w, r, ErrBadRequest)
			return
		}
		st := Status(body.Status)
		if err := repo.SetStatus(r.Context(), id, st); err != nil {
			switch {
			case errors.Is(err, ErrNotFound):
				WriteError(w, r, ErrNotFound)
			case errors.Is(err, ErrConflict):
				WriteError(w, r, err)
			default:
				slog.WarnContext(r.Context(), "smeldr: set_content_status", "id", id, "err", err)
				WriteError(w, r, ErrInternal)
			}
			return
		}
		writeDynamicJSON(w, map[string]any{"id": id, "status": body.Status})
		go a.rebuildDynamicSitemap(context.Background(), desc)
		go a.rebuildDynamicAIIndex(context.Background(), desc)
	})
}

// rebuildDynamicSitemap updates the sitemap fragment for the given content type.
// A no-op when the type has no URLPrefix or when the sitemapStore is nil.
// Called in a goroutine from newSetStatusHandler so it does not block responses.
func (a *App) rebuildDynamicSitemap(ctx context.Context, desc *TypeDescriptor) {
	if desc.Prefix == "" || a.sitemapStore == nil {
		return
	}
	repo, err := a.DynamicContentRepo(desc.Name)
	if err != nil {
		return
	}
	items, err := repo.List(ctx, ListOptions{Status: []Status{Published}})
	if err != nil {
		slog.WarnContext(ctx, "smeldr: rebuildDynamicSitemap", "type", desc.Name, "err", err)
		return
	}
	baseURL := strings.TrimRight(a.cfg.BaseURL, "/")
	entries := make([]SitemapEntry, 0, len(items))
	for _, item := range items {
		slug, _ := item["Slug"].(string)
		if slug == "" {
			continue
		}
		e := SitemapEntry{
			Loc:      baseURL + desc.Prefix + "/" + slug,
			Priority: 0.5,
		}
		if pa, ok := item["PublishedAt"].(time.Time); ok {
			e.LastMod = pa
		}
		entries = append(entries, e)
	}
	var buf bytes.Buffer
	if err := WriteSitemapFragment(&buf, entries); err != nil {
		slog.WarnContext(ctx, "smeldr: rebuildDynamicSitemap write", "err", err)
		return
	}
	a.sitemapStore.Set(desc.Prefix+"/sitemap.xml", buf.Bytes())
}

// rebuildDynamicAIIndex regenerates the /llms.txt compact fragment for the
// given content type. A no-op when the type has no URLPrefix or when the
// llmsStore is nil. Called in a goroutine so it does not block responses.
func (a *App) rebuildDynamicAIIndex(ctx context.Context, desc *TypeDescriptor) {
	if desc.Prefix == "" || a.llmsStore == nil {
		return
	}
	repo, err := a.DynamicContentRepo(desc.Name)
	if err != nil {
		return
	}
	items, err := repo.List(ctx, ListOptions{Status: []Status{Published}})
	if err != nil {
		slog.WarnContext(ctx, "smeldr: rebuildDynamicAIIndex", "type", desc.Name, "err", err)
		return
	}
	baseURL := strings.TrimRight(a.cfg.BaseURL, "/")
	entries := make([]LLMsEntry, 0, len(items))
	for _, item := range items {
		slug, _ := item["Slug"].(string)
		if slug == "" {
			continue
		}
		e := LLMsEntry{URL: baseURL + desc.Prefix + "/" + slug}
		// Use the title-role field value as the entry Title; fall back to slug.
		if desc.Schema != nil {
			if fields, err := desc.Schema.ParseFields(); err == nil {
				for _, f := range fields {
					if f.Role == "title" {
						if t, ok := item[f.Name].(string); ok && t != "" {
							e.Title = t
						}
						break
					}
				}
			}
		}
		if e.Title == "" {
			e.Title = slug
		}
		// Use the description-role field value as the entry Summary when present.
		if desc.Schema != nil {
			if fields, err := desc.Schema.ParseFields(); err == nil {
				for _, f := range fields {
					if f.Role == "description" {
						if s, ok := item[f.Name].(string); ok {
							e.Summary = s
						}
						break
					}
				}
			}
		}
		entries = append(entries, e)
	}
	a.llmsStore.SetCompact(desc.Prefix, entries)
}

// RefreshContentIndex rebuilds the sitemap fragment and /llms.txt compact
// fragment for the named dynamic content type. A no-op when the type is not
// registered, is not a content-kind type, or has an empty URLPrefix. Intended
// to be called in a goroutine from MCP tool handlers after a status change.
func (a *App) RefreshContentIndex(ctx context.Context, typeName string) {
	desc := a.typeRegistry.Lookup(typeName)
	if desc == nil || desc.Kind != "content" || desc.Prefix == "" {
		return
	}
	a.rebuildDynamicSitemap(ctx, desc)
	a.rebuildDynamicAIIndex(ctx, desc)
}

// nodeToMap converts a DynamicNode to a type-erased map for JSON responses.
func nodeToMap(node *DynamicNode) map[string]any {
	m := make(map[string]any)
	if len(node.Fields) > 0 {
		_ = json.Unmarshal(node.Fields, &m)
	}
	m["ID"] = node.ID
	m["Slug"] = node.Slug
	m["TypeName"] = node.TypeName
	m["Status"] = string(node.Status)
	m["CreatedAt"] = node.CreatedAt
	m["UpdatedAt"] = node.UpdatedAt
	if !node.PublishedAt.IsZero() {
		m["PublishedAt"] = node.PublishedAt
	}
	return m
}
