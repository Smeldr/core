package forge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
)

// — rebuilder ———————————————————————————————————————————————————————————

// rebuilder is implemented by [Module][T] to support startup regeneration of
// all derived content (sitemap fragment, AI index, RSS feed) from the current
// repository state. [App.Handler] calls rebuildAll once so that items inserted
// directly into the repository (e.g. seed data, fixtures) appear in sitemaps
// and feeds without requiring a manual publish event.
type rebuilder interface {
	rebuildAll(ctx Context)
}

// stoppable is implemented by [Module][T] to allow [App.Run] to stop background
// goroutines (cache sweep, pending debounce timer) during graceful shutdown.
type stoppable interface {
	Stop()
}

// — contentNegotiator —————————————————————————————————————————————————————

// contentNegotiator carries the pre-compiled content-type capabilities for a
// module. Built once at [NewModule] time; never mutated after construction.
type contentNegotiator struct {
	md   bool // true if the prototype T implements Markdownable
	html bool // true if forge.Templates option is set (Milestone 3)
}

// negotiate returns the canonical content-type string to serve for r.
// The result is used to select the response serialiser. Called once per
// request on the hot path — allocation-free.
//
// Content-type branches are gated on the module's capability flags (A35):
// text/html is only selected when forge.Templates is registered (n.html),
// text/markdown only when the content type implements Markdownable (n.md).
// When an Accept header requests a format the module cannot produce, the
// negotiator falls through to the next candidate rather than returning a
// type that will unconditionally 406.
//
// When Accept is absent or "*/*", the server returns its most useful
// representation: HTML for modules with templates, JSON otherwise.
// This matches RFC 9110 — "*/*" means anything acceptable, so the server
// picks its preferred format. Ensures crawlers that omit Accept receive HTML
// and see structured data in <head> (Amendment A53).
func (n contentNegotiator) negotiate(r *http.Request) string {
	a := r.Header.Get("Accept")
	if a == "" || a == "*/*" {
		if n.html {
			return "text/html"
		}
		return "application/json"
	}
	if strings.Contains(a, "application/json") {
		return "application/json"
	}
	if n.html && strings.Contains(a, "text/html") {
		return "text/html"
	}
	if n.md && strings.Contains(a, "text/markdown") {
		return "text/markdown"
	}
	if strings.Contains(a, "text/plain") {
		return "text/plain"
	}
	return "application/json"
}

// — Module Option types ———————————————————————————————————————————————————

// atOption carries the URL prefix for a Module. Use [At] to create one.
type atOption struct{ prefix string }

func (atOption) isOption() {}

// At returns an [Option] that sets the URL prefix for a module.
// The prefix must start with "/" and must not end with "/".
// Example: forge.At("/posts")
func At(prefix string) Option { return atOption{prefix: prefix} }

// moduleCacheOption carries the TTL for a module-level [CacheStore].
// Use [Cache] to create one.
type moduleCacheOption struct{ ttl time.Duration }

func (moduleCacheOption) isOption() {}

// Cache returns an [Option] that enables a per-module LRU response cache with
// the given TTL. Cached entries are flushed automatically on any create, update,
// or delete operation. The cache holds at most 1000 entries (LRU eviction).
func Cache(ttl time.Duration) Option { return moduleCacheOption{ttl: ttl} }

// middlewareModuleOption carries per-module middleware. Use [Middleware] to
// create one.
type middlewareModuleOption struct {
	mws []func(http.Handler) http.Handler
}

func (middlewareModuleOption) isOption() {}

// Middleware returns an [Option] that wraps every route in this module with the
// provided middleware. Applied in the same order as [Chain] (index 0 is outermost).
func Middleware(mws ...func(http.Handler) http.Handler) Option {
	return middlewareModuleOption{mws: mws}
}

// authOption groups per-module role options. Use [Auth] to create one.
type authOption struct{ opts []Option }

func (authOption) isOption() {}

// Auth returns an [Option] that sets the minimum role for each HTTP operation on
// this module. Accepts [Read], [Write], and [Delete] role options.
//
//	forge.Auth(
//	    forge.Read(forge.Guest),
//	    forge.Write(forge.Author),
//	    forge.Delete(forge.Editor),
//	)
func Auth(opts ...Option) Option { return authOption{opts: opts} }

// repoOption carries the [Repository] for a [Module]. Use [Repo] to create one.
// Application code never calls [Repo] directly — [App.Content] supplies it.
// In unit tests, supply it explicitly:
//
//	m := forge.NewModule(&Post{}, forge.Repo(forge.NewMemoryRepo[*Post]()))
type repoOption[T any] struct{ repo Repository[T] }

func (repoOption[T]) isOption() {}

// Repo returns an [Option] that provides the [Repository] for a [Module].
// This is called internally by App.Content. In unit tests pass a [MemoryRepo]:
//
//	forge.Repo(forge.NewMemoryRepo[*Post]())
func Repo[T any](r Repository[T]) Option { return repoOption[T]{repo: r} }

// — Node field reflection cache ———————————————————————————————————————————

// nodeFields holds the struct field index paths for the three required Node fields.
// Index paths are []int because fields may be in embedded structs (e.g. Node.ID).
type nodeFields struct{ id, slug, status []int }

// nodeFieldCache caches field index paths keyed by reflect.Type.
var nodeFieldCache sync.Map

// getNodeFields returns cached field index paths for t's underlying struct type.
// Traverses pointer types automatically. Panics (at construction time, not
// request time) when ID, Slug, or Status fields are absent.
func getNodeFields(t reflect.Type) nodeFields {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if v, ok := nodeFieldCache.Load(t); ok {
		return v.(nodeFields)
	}
	idF, idOK := t.FieldByName("ID")
	slugF, slugOK := t.FieldByName("Slug")
	statusF, statusOK := t.FieldByName("Status")
	if !idOK || !slugOK || !statusOK {
		panic("forge: content type must embed forge.Node (missing ID, Slug, or Status field)")
	}
	f := nodeFields{id: idF.Index, slug: slugF.Index, status: statusF.Index}
	nodeFieldCache.Store(t, f)
	return f
}

// elemValue dereferences any number of pointer types and returns the
// underlying struct reflect.Value.
func elemValue(v any) reflect.Value {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	return rv
}

// nodeStatusOf returns the Status field of v.
func nodeStatusOf(v any) Status {
	rv := elemValue(v)
	f := getNodeFields(rv.Type())
	return rv.FieldByIndex(f.status).Interface().(Status)
}

// nodeIDOf returns the ID field of v.
func nodeIDOf(v any) string {
	rv := elemValue(v)
	f := getNodeFields(rv.Type())
	return rv.FieldByIndex(f.id).String()
}

// autoSlugCache stores the index path of the slug-source field per struct type.
var autoSlugCache sync.Map

// autoSlugFieldPath returns the cached reflect index path of the best field to
// derive a slug from for struct type t. Checks Title, Name, Headline in order,
// then falls back to the first top-level string field tagged forge:"required".
// Returns nil when no suitable field is found. Result is immutable once stored.
func autoSlugFieldPath(t reflect.Type) []int {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if v, ok := autoSlugCache.Load(t); ok {
		return v.([]int)
	}
	for _, name := range []string{"Title", "Name", "Headline"} {
		if sf, ok := t.FieldByName(name); ok && sf.Type.Kind() == reflect.String {
			autoSlugCache.Store(t, sf.Index)
			return sf.Index
		}
	}
	for i := 0; i < t.NumField(); i++ {
		fi := t.Field(i)
		if fi.Type.Kind() == reflect.String && strings.Contains(fi.Tag.Get("forge"), "required") {
			path := []int{i}
			autoSlugCache.Store(t, path)
			return path
		}
	}
	autoSlugCache.Store(t, ([]int)(nil))
	return nil
}

// autoSlug derives a URL slug from the first suitable string field of rv.
// Field selection is cached per type via autoSlugFieldPath.
func autoSlug(rv reflect.Value) string {
	for rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	path := autoSlugFieldPath(rv.Type())
	if path == nil {
		return ""
	}
	s := rv.FieldByIndex(path).String()
	if s == "" {
		return ""
	}
	return GenerateSlug(s)
}

// stripMarkdown removes common Markdown formatting from s for text/plain output.
func stripMarkdown(s string) string {
	s = strings.NewReplacer(
		"**", "", "__", "", "*", "", "_", "",
		"### ", "", "## ", "", "# ",
		"",
	).Replace(s)
	var out strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '[' {
			closeIdx := strings.Index(s[i:], "](")
			if closeIdx > 0 {
				out.WriteString(s[i+1 : i+closeIdx])
				rest := s[i+closeIdx+2:]
				if parenEnd := strings.Index(rest, ")"); parenEnd >= 0 {
					i = i + closeIdx + 2 + parenEnd + 1
					continue
				}
			}
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

// — Module ————————————————————————————————————————————————————————————————

// Module is the core routing and lifecycle unit for a content type T.
// T must embed [Node] — its struct must have exported ID, Slug, and Status fields.
// Use [NewModule] to construct; Registration onto a ServeMux is done via [Register].
// App.Content handles both steps automatically.
type Module[T any] struct {
	prefix      string
	repo        Repository[T]
	readRole    Role
	writeRole   Role
	deleteRole  Role
	signals     map[Signal][]signalHandler
	cache       *CacheStore // nil when no Cache option was given
	middlewares []func(http.Handler) http.Handler
	neg         contentNegotiator
	debounce    *debouncer
	proto       reflect.Type
	headFunc    any // nil, or func(Context, T) Head set via HeadFunc option

	templateDir      string             // set by Templates or TemplatesOptional option
	templateRequired bool               // true when Templates (not TemplatesOptional) was used
	tplList          *template.Template // nil until parseTemplates succeeds
	tplShow          *template.Template // nil until parseTemplates succeeds
	tplMu            sync.RWMutex       // guards tplList, tplShow reads and swaps
	siteName         string             // set by App.Content via setSiteName
	ogDefaults       *OGDefaults        // nil until App.Handler via setSEODefaults
	appSchema        *AppSchema         // nil until App.Handler via setSEODefaults

	sitemapCfg   *SitemapConfig  // nil when no SitemapConfig option given
	sitemapStore *SitemapStore   // set by App.Content via setSitemap
	baseURL      string          // set by App.Content via setSitemap or setAIRegistry
	social       []SocialFeature // nil when no Social option given

	aiFeatures []AIFeature // nil when no AIIndex option given
	llmsStore  *LLMsStore  // set by App.Content via setAIRegistry
	withoutID  bool        // true when WithoutID() option was given

	feedCfg   *FeedConfig // nil when no Feed option, or when DisableFeed was called
	feedStore *FeedStore  // set by App.Content via setFeedStore

	mcpOps []MCPOperation // non-nil when MCP(...) option was given

	stopCh chan struct{} // closed by Stop() to terminate the cache sweep goroutine
}

// NewModule constructs a [Module] for content type T.
//
// proto is a representative value of T (typically a nil pointer: (*Post)(nil))
// used to derive the default URL prefix and to detect capabilities.
//
// Required options (supplied automatically by App.Content):
//   - [Repo]: provides the Repository[T]
//
// Optional options:
//   - [At]: override URL prefix (default: "/"+lowercase(TypeName)+"s")
//   - [Auth]: set per-operation role requirements
//   - [Cache]: enable per-module LRU response cache
//   - [Middleware]: wrap all routes with the given middleware
//   - [On]: register signal handlers
//
// Panics if no [Repo] option is present — this is a programming error caught
// at startup, never at request time.
func NewModule[T any](proto T, opts ...Option) *Module[T] {
	t := reflect.TypeOf(proto)

	// Validate Node fields eagerly — fail at startup, not request time.
	getNodeFields(t)

	// Determine default URL prefix from the type name.
	typeName := t.Name()
	if t.Kind() == reflect.Ptr {
		typeName = t.Elem().Name()
	}
	prefix := "/" + strings.ToLower(typeName) + "s"

	m := &Module[T]{
		prefix:     prefix,
		readRole:   Guest,
		writeRole:  Author,
		deleteRole: Editor,
		signals:    make(map[Signal][]signalHandler),
		proto:      t,
	}

	// Detect Markdownable capability.
	_, m.neg.md = any(proto).(Markdownable)

	// Parse options.
	var repoFound bool
	for _, o := range opts {
		switch v := o.(type) {
		case atOption:
			m.prefix = v.prefix
		case moduleCacheOption:
			m.cache = NewCacheStore(v.ttl, 1000)
		case middlewareModuleOption:
			m.middlewares = v.mws
		case authOption:
			for _, ao := range v.opts {
				if ro, ok := ao.(roleOption); ok {
					switch ro.signal {
					case "read":
						m.readRole = ro.role
					case "write":
						m.writeRole = ro.role
					case "delete":
						m.deleteRole = ro.role
					}
				}
			}
		case signalOption:
			m.signals[v.signal] = append(m.signals[v.signal], v.handler)
		case SitemapConfig:
			cfg := v
			m.sitemapCfg = &cfg
		case templatesOption:
			m.templateDir = v.dir
			m.templateRequired = v.required
		case socialOption:
			m.social = v.features
		case aiIndexOption:
			m.aiFeatures = v.features
		case withoutIDOption:
			m.withoutID = true
		case feedOption:
			cfg := v.cfg
			m.feedCfg = &cfg
		case feedDisabledOption:
			m.feedCfg = nil
		case mcpOption:
			m.mcpOps = v.ops
		}
		// repoOption[T] requires a concrete type assertion — handled separately.
		if ro, ok := o.(repoOption[T]); ok {
			m.repo = ro.repo
			repoFound = true
		}
		// headFuncOption[T] is generic — handled via direct type assertion.
		if hfo, ok := o.(headFuncOption[T]); ok {
			m.headFunc = hfo.fn
		}
	}

	if !repoFound {
		panic("forge: Module[T] requires a Repository; use forge.Repo(...) or App.Content")
	}

	// A36: detect capability mismatches at startup — programmer errors caught
	// before any request is served (consistent with getNodeFields and repoFound).
	// SitemapConfig requires T to implement SitemapNode (needs Head() forge.Head).
	if m.sitemapCfg != nil {
		if _, ok := any(proto).(SitemapNode); !ok {
			panic(fmt.Sprintf(
				"forge: %s has SitemapConfig but does not implement SitemapNode "+
					"(add a Head() forge.Head method); sitemap would be silently empty",
				typeName,
			))
		}
	}
	// AIIndex(LLMsTxtFull) requires T to implement Markdownable (Markdown() string).
	if hasAIFeature(m.aiFeatures, LLMsTxtFull) && !m.neg.md {
		panic(fmt.Sprintf(
			"forge: %s has AIIndex(LLMsTxtFull) but does not implement Markdownable "+
				"(add a Markdown() string method); /llms-full.txt would be silently empty",
			typeName,
		))
	}

	// A39: stopCh is always initialised so Stop() is safe to call on any module.
	m.stopCh = make(chan struct{})

	// Background sweep for module cache.
	if m.cache != nil {
		go func() {
			ticker := time.NewTicker(60 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					m.cache.Sweep()
				case <-m.stopCh:
					return
				}
			}
		}()
	}

	// Debouncer for sitemap regeneration (Decision 9: event-driven, 2-second delay).
	// A41: use NewBackgroundContext at fire time so the debounce callback always
	// runs with a live context. Stashing the request Context is unsafe — its
	// underlying context.Context is cancelled as soon as the handler returns,
	// causing SQLRepo queries to fail silently 2 seconds later.
	m.debounce = newDebouncer(2*time.Second, func() {
		ctx := NewBackgroundContext(m.siteName)
		dispatchAfter(ctx, m.signals[SitemapRegenerate], nil)
		m.regenerateSitemap(ctx)
		m.regenerateAI(ctx)
		m.regenerateFeed(ctx)
	})

	return m
}

// Stop terminates background goroutines started by this module (cache sweep
// ticker and any pending debounce timer). It is called automatically by
// [App.Run] during graceful shutdown. Stop is idempotent — calling it more
// than once is safe.
func (m *Module[T]) Stop() {
	select {
	case <-m.stopCh:
		// already closed — no-op
	default:
		close(m.stopCh)
	}
	m.debounce.Stop()
}

// Register mounts the five standard routes for this module onto mux.
// Called automatically by App.Content.
//
//	GET    /{prefix}          → list
//	GET    /{prefix}/{slug}   → show
//	POST   /{prefix}          → create
//	PUT    /{prefix}/{slug}   → update
//	DELETE /{prefix}/{slug}   → delete
func (m *Module[T]) Register(mux *http.ServeMux) {
	list := http.HandlerFunc(m.listHandler)
	show := http.HandlerFunc(m.showHandler)
	create := http.HandlerFunc(m.createHandler)
	update := http.HandlerFunc(m.updateHandler)
	del := http.HandlerFunc(m.deleteHandler)

	if len(m.middlewares) > 0 {
		list = http.HandlerFunc(Chain(list, m.middlewares...).ServeHTTP)
		show = http.HandlerFunc(Chain(show, m.middlewares...).ServeHTTP)
		create = http.HandlerFunc(Chain(create, m.middlewares...).ServeHTTP)
		update = http.HandlerFunc(Chain(update, m.middlewares...).ServeHTTP)
		del = http.HandlerFunc(Chain(del, m.middlewares...).ServeHTTP)
	}

	mux.Handle("GET "+m.prefix, list)
	mux.Handle("GET "+m.prefix+"/{slug}", show)
	mux.Handle("POST "+m.prefix, create)
	mux.Handle("PUT "+m.prefix+"/{slug}", update)
	mux.Handle("DELETE "+m.prefix+"/{slug}", del)
	// A33: guard on sitemapCfg only — sitemapStore is injected by App.Content
	// after Register returns, so the store is always nil at mount time.
	// The handler reads m.sitemapStore lazily at request time.
	if m.sitemapCfg != nil {
		mux.Handle("GET "+m.prefix+"/sitemap.xml", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if m.sitemapStore == nil {
				WriteError(w, r, ErrInternal)
				return
			}
			m.sitemapStore.Handler().ServeHTTP(w, r)
		}))
	}
	if hasAIFeature(m.aiFeatures, AIDoc) {
		aidoc := http.HandlerFunc(m.aiDocHandler)
		if len(m.middlewares) > 0 {
			aidoc = http.HandlerFunc(Chain(aidoc, m.middlewares...).ServeHTTP)
		}
		mux.Handle("GET "+m.prefix+"/{slug}/aidoc", aidoc)
	}
	// A33: guard on feedCfg only — feedStore is injected by App.Content after
	// Register returns, so the store is always nil at mount time.
	// The handler reads m.feedStore lazily at request time.
	if m.feedCfg != nil {
		mux.Handle("GET "+m.prefix+"/feed.xml", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if m.feedStore == nil {
				WriteError(w, r, ErrInternal)
				return
			}
			m.feedStore.ModuleHandler(m.prefix).ServeHTTP(w, r)
		}))
	}
}

// setSitemap is called by [App.Content] to inject the shared [SitemapStore]
// and application BaseURL into this module.
func (m *Module[T]) setSitemap(store *SitemapStore, baseURL string) {
	m.sitemapStore = store
	m.baseURL = baseURL
}

// setAIRegistry is called by [App.Content] to inject the shared [LLMsStore]
// and application BaseURL into this module. Registers compact and/or full
// feature flags on the store so [App.Handler] knows which endpoints to mount.
func (m *Module[T]) setAIRegistry(store *LLMsStore, baseURL string) {
	m.llmsStore = store
	if m.baseURL == "" {
		m.baseURL = baseURL
	}
	if hasAIFeature(m.aiFeatures, LLMsTxt) {
		store.registerCompact()
	}
	if hasAIFeature(m.aiFeatures, LLMsTxtFull) {
		store.registerFull()
	}
}

// setFeedStore is called by [App.Content] to inject the shared [FeedStore]
// and application BaseURL into this module. Registers the prefix so
// [App.Handler] knows to mount GET /feed.xml when at least one feed exists.
func (m *Module[T]) setFeedStore(store *FeedStore, baseURL string) {
	m.feedStore = store
	if m.baseURL == "" {
		m.baseURL = baseURL
	}
	if m.feedCfg != nil {
		// Register the prefix now (items populated by first regenerateFeed call).
		store.Set(m.prefix, *m.feedCfg, nil)
	}
}

// resolveHead returns the Head for item using the highest-priority source available:
//  1. HeadFunc option — explicit module-level override, receives request context
//  2. Headable interface on T — type-level default, no context required
//  3. Zero Head
//
// Called by regenerateFeed, regenerateAI, aiDocHandler, and showHandler.
// HeadFunc takes priority when both are present — no behaviour change for
// existing code that supplies HeadFunc.
func (m *Module[T]) resolveHead(ctx Context, item T) Head {
	if m.headFunc != nil {
		if fn, ok := m.headFunc.(func(Context, T) Head); ok {
			return fn(ctx, item)
		}
	}
	if h, ok := any(item).(Headable); ok {
		return h.Head()
	}
	return Head{}
}

// regenerateFeed rebuilds the RSS 2.0 item list for this module and stores it
// in the shared [FeedStore]. Called by the debouncer after every write event.
// Skips silently when the store, repo, or FeedConfig is absent.
func (m *Module[T]) regenerateFeed(ctx Context) {
	if m.feedStore == nil || m.feedCfg == nil || m.repo == nil {
		return
	}
	items, err := m.repo.FindAll(ctx, ListOptions{Status: []Status{Published}})
	if err != nil {
		return
	}
	rssItems := make([]rssItem, 0, len(items))
	for _, item := range items {
		head := m.resolveHead(ctx, item)
		n := extractNode(item)
		canonical := head.Canonical
		if canonical == "" && n.Slug != "" {
			canonical = strings.TrimRight(m.baseURL, "/") + m.prefix + "/" + n.Slug
		}
		rssItems = append(rssItems, buildRSSItem(head, n, canonical))
	}
	m.feedStore.Set(m.prefix, *m.feedCfg, rssItems)
}

// regenerateAI rebuilds compact and/or full AI fragments for this module and
// stores them in the shared [LLMsStore]. Called by the debouncer after every
// write event. Skips silently when prerequisites are absent.
func (m *Module[T]) regenerateAI(ctx Context) {
	if m.llmsStore == nil || m.repo == nil {
		return
	}
	if !hasAIFeature(m.aiFeatures, LLMsTxt) && !hasAIFeature(m.aiFeatures, LLMsTxtFull) {
		return
	}
	items, err := m.repo.FindAll(ctx, ListOptions{Status: []Status{Published}})
	if err != nil {
		return
	}

	type resolved struct {
		item T
		head Head
		node Node
	}
	rs := make([]resolved, 0, len(items))
	for _, item := range items {
		rs = append(rs, resolved{item: item, head: m.resolveHead(ctx, item), node: extractNode(item)})
	}

	if hasAIFeature(m.aiFeatures, LLMsTxt) {
		entries := make([]LLMsEntry, 0, len(rs))
		for _, r := range rs {
			if r.head.Title == "" {
				continue
			}
			canonical := r.head.Canonical
			if canonical == "" && r.node.Slug != "" {
				canonical = strings.TrimRight(m.baseURL, "/") + m.prefix + "/" + r.node.Slug
			}
			var summary string
			if as, ok := any(r.item).(AIDocSummary); ok {
				summary = as.AISummary()
			} else {
				summary = Excerpt(r.head.Description, 120)
			}
			entries = append(entries, LLMsEntry{Title: r.head.Title, URL: canonical, Summary: summary})
		}
		m.llmsStore.SetCompact(m.prefix, entries)
	}

	if hasAIFeature(m.aiFeatures, LLMsTxtFull) {
		var buf strings.Builder
		for _, r := range rs {
			if r.head.Title != "" {
				fmt.Fprintf(&buf, "## %s\n", r.head.Title)
			}
			canonical := r.head.Canonical
			if canonical == "" && r.node.Slug != "" {
				canonical = strings.TrimRight(m.baseURL, "/") + m.prefix + "/" + r.node.Slug
			}
			if canonical != "" {
				fmt.Fprintf(&buf, "URL: %s\n", canonical)
			}
			if !r.node.PublishedAt.IsZero() {
				fmt.Fprintf(&buf, "Published: %s\n", r.node.PublishedAt.Format("2006-01-02"))
			}
			buf.WriteString("\n")
			if md, ok := any(r.item).(Markdownable); ok {
				buf.WriteString(md.Markdown())
			} else if r.head.Description != "" {
				buf.WriteString(r.head.Description)
			}
			buf.WriteString("\n---\n\n")
		}
		m.llmsStore.SetFull(m.prefix, buf.String())
	}
}

// aiDocHandler serves GET /{prefix}/{slug}.aidoc.
// Returns the AIDoc format for Published content; 404 for all other statuses.
func (m *Module[T]) aiDocHandler(w http.ResponseWriter, r *http.Request) {
	ctx := ContextFrom(w, r)
	slug := r.PathValue("slug")
	item, err := m.repo.FindBySlug(ctx, slug)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	if !isVisible(item, ctx.User()) {
		WriteError(w, r, ErrNotFound)
		return
	}
	head := m.resolveHead(ctx, item)
	n := extractNode(item)
	renderAIDoc(w, r, head, n, item, m.withoutID)
}

// regenerateSitemap rebuilds the fragment sitemap for this module and stores
// the result in the shared [SitemapStore]. Called by the debouncer after every
// publish/unpublish event. Skips silently when the store, repo, SitemapConfig,
// or SitemapNode satisfaction is absent.
func (m *Module[T]) regenerateSitemap(ctx Context) {
	if m.sitemapStore == nil || m.repo == nil || m.sitemapCfg == nil {
		return
	}
	items, err := m.repo.FindAll(ctx, ListOptions{Status: []Status{Published}})
	if err != nil {
		return
	}
	cfg := *m.sitemapCfg
	freq := cfg.ChangeFreq
	if freq == "" {
		freq = Weekly
	}
	var entries []SitemapEntry
	for _, item := range items {
		sn, ok := any(item).(SitemapNode)
		if !ok {
			return
		}
		loc := sn.Head().Canonical
		if loc == "" {
			loc = strings.TrimRight(m.baseURL, "/") + "/" + sn.GetSlug()
		}
		priority := cfg.Priority
		if sp, ok := any(item).(SitemapPrioritiser); ok {
			priority = sp.SitemapPriority()
		} else if priority <= 0 {
			priority = 0.5
		}
		entries = append(entries, SitemapEntry{
			Loc:        loc,
			LastMod:    sn.GetPublishedAt(),
			ChangeFreq: freq,
			Priority:   priority,
		})
	}
	var buf bytes.Buffer
	if err := WriteSitemapFragment(&buf, entries); err != nil {
		return
	}
	m.sitemapStore.Set(m.prefix+"/sitemap.xml", buf.Bytes())
}

// — Cache helpers —————————————————————————————————————————————————————————

// cacheKey returns the cache key for r.
func cacheKey(r *http.Request) string {
	return r.Method + " " + r.URL.RequestURI() + " " + r.Header.Get("Accept")
}

// serveCached returns true if the response was served from the module cache.
// Sets X-Cache: HIT and writes the cached response to w.
func (m *Module[T]) serveCached(w http.ResponseWriter, r *http.Request) bool {
	if m.cache == nil {
		return false
	}
	key := cacheKey(r)
	m.cache.mu.Lock()
	entry := m.cache.get(key)
	m.cache.mu.Unlock()
	if entry == nil {
		return false
	}
	for k, vals := range entry.header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("X-Cache", "HIT")
	w.WriteHeader(entry.status)
	w.Write(entry.body) //nolint:errcheck
	return true
}

// storeCache stores a captured response in the module cache if status is 200.
func (m *Module[T]) storeCache(r *http.Request, rec *cacheRecorder) {
	if m.cache == nil || rec.status != http.StatusOK {
		return
	}
	h := make(http.Header)
	for k, v := range rec.ResponseWriter.Header() {
		h[k] = v
	}
	e := &cacheEntry{
		key:     cacheKey(r),
		body:    rec.body,
		header:  h,
		status:  rec.status,
		expires: time.Now().Add(m.cache.ttl),
	}
	m.cache.mu.Lock()
	m.cache.set(e)
	m.cache.mu.Unlock()
}

// invalidateCache flushes the module cache after a write operation.
func (m *Module[T]) invalidateCache() {
	if m.cache != nil {
		m.cache.Flush()
	}
}

// — Content-type helpers ——————————————————————————————————————————————————

// writeJSON writes a JSON response with the given status code.
// Sets Content-Type: application/json and Vary: Accept.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Vary", "Accept")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

// writeContent serialises v to w using ct content-type.
// Sets Vary: Accept and Content-Type on every response.
func writeContent(w http.ResponseWriter, r *http.Request, ct string, v any) {
	w.Header().Set("Vary", "Accept")
	switch ct {
	case "text/html":
		WriteError(w, r, ErrNotAcceptable)
	case "text/markdown":
		if md, ok := v.(Markdownable); ok {
			w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(md.Markdown())) //nolint:errcheck
		} else {
			WriteError(w, r, ErrNotAcceptable)
		}
	case "text/plain":
		var body string
		if md, ok := v.(Markdownable); ok {
			body = stripMarkdown(md.Markdown())
		} else {
			b, _ := json.Marshal(v)
			body = string(b)
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body)) //nolint:errcheck
	default: // application/json
		writeJSON(w, http.StatusOK, v)
	}
}

// writeContentCached writes a negotiated response through a cacheRecorder if
// caching is enabled, then stores the response in the module cache on 200.
func (m *Module[T]) writeContentCached(w http.ResponseWriter, r *http.Request, ct string, v any) {
	if m.cache != nil {
		w.Header().Set("X-Cache", "MISS")
		rec := newCacheRecorder(w)
		writeContent(rec, r, ct, v)
		m.storeCache(r, rec)
	} else {
		writeContent(w, r, ct, v)
	}
}

// — Lifecycle helper ——————————————————————————————————————————————————————

// isVisible reports whether item should be visible to the calling user.
// Guests see only Published content. Authors and above see all statuses.
func isVisible(item any, user User) bool {
	if nodeStatusOf(item) == Published {
		return true
	}
	return user.HasRole(Author)
}

// — rebuild + debounce helpers ————————————————————————————————————————————

// rebuildAll triggers immediate regeneration of the sitemap fragment, AI index,
// and RSS feed for this module from the current repository state. Called once at
// startup by [App.Handler] so that items already present in the repository
// (seed data, pre-loaded fixtures) are reflected in all derived content before
// the server begins accepting requests. Implements [rebuilder].
func (m *Module[T]) rebuildAll(ctx Context) {
	m.regenerateSitemap(ctx)
	m.regenerateAI(ctx)
	m.regenerateFeed(ctx)
}

// triggerRebuild schedules a debounced regeneration of all derived content
// (sitemap, AI index, RSS feed). The debounce callback always runs with a
// background context — it must not use the triggering request context, which
// will be cancelled by the time the callback fires. (Amendment A41)
func (m *Module[T]) triggerRebuild() {
	m.debounce.Trigger()
}

// — Scheduler support ——————————————————————————————————————————————————————

// setNodeStatus sets the Status field on a pointer-to-struct content item.
func setNodeStatus(item any, s Status) {
	rv := reflect.ValueOf(item)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	if path := goFieldPath(rv.Type(), "Status"); path != nil {
		rv.FieldByIndex(path).Set(reflect.ValueOf(s))
	}
}

// setNodeTime sets a time.Time field by Go field name on a pointer-to-struct
// content item.
func setNodeTime(item any, field string, t time.Time) {
	rv := reflect.ValueOf(item)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	if path := goFieldPath(rv.Type(), field); path != nil {
		rv.FieldByIndex(path).Set(reflect.ValueOf(t))
	}
}

// setNodeTimePtr sets a *time.Time field by Go field name on a pointer-to-struct
// content item. Passing nil clears the field to the zero pointer value.
func setNodeTimePtr(item any, field string, t *time.Time) {
	rv := reflect.ValueOf(item)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	if path := goFieldPath(rv.Type(), field); path != nil {
		fv := rv.FieldByIndex(path)
		if t == nil {
			fv.Set(reflect.Zero(fv.Type()))
		} else {
			fv.Set(reflect.ValueOf(t))
		}
	}
}

// processScheduled implements [schedulableModule]. It queries the module's
// repository for all Scheduled items, transitions any whose ScheduledAt
// is at or before now to Published, fires [AfterPublish] for each, and
// triggers sitemap/feed debounce regeneration when at least one item
// is published.
//
// Returns the count of items published, the soonest remaining ScheduledAt
// across all still-scheduled items (nil when none remain), and any storage
// error encountered during the transition.
func (m *Module[T]) processScheduled(ctx Context, now time.Time) (int, *time.Time, error) {
	items, err := m.repo.FindAll(ctx.Request().Context(), ListOptions{Status: []Status{Scheduled}})
	if err != nil {
		return 0, nil, err
	}

	published := 0
	var next *time.Time

	for _, item := range items {
		rv := reflect.ValueOf(item)
		if rv.Kind() == reflect.Ptr {
			rv = rv.Elem()
		}
		path := goFieldPath(rv.Type(), "ScheduledAt")
		if path == nil {
			continue
		}
		fv := rv.FieldByIndex(path)
		if fv.IsNil() {
			continue
		}
		scheduledAt := fv.Elem().Interface().(time.Time)

		if scheduledAt.After(now) {
			// Not yet due — record as candidate for next timer wake.
			if next == nil || scheduledAt.Before(*next) {
				t := scheduledAt
				next = &t
			}
			continue
		}

		// Due — transition to Published.
		setNodeStatus(item, Published)
		setNodeTime(item, "PublishedAt", now)
		setNodeTimePtr(item, "ScheduledAt", nil)

		if err := m.repo.Save(ctx.Request().Context(), item); err != nil {
			return published, next, err
		}
		dispatchAfter(ctx, m.signals[AfterPublish], item)
		published++
	}

	if published > 0 {
		m.invalidateCache()
		m.triggerRebuild()
	}
	return published, next, nil
}

// — newItemPtr allocates a *struct for T via reflection. ——————————————————

// newItemPtr returns a pointer to a newly allocated zero value of T's
// underlying struct type. Used for JSON decoding.
func (m *Module[T]) newItemPtr() (reflect.Value, reflect.Type) {
	t := m.proto
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return reflect.New(t), t
}

// ptrToT converts a *struct reflect.Value back into T.
func ptrToT[T any](pv reflect.Value, _ reflect.Type) T {
	var item T
	rv := reflect.ValueOf(&item).Elem()
	if rv.Kind() == reflect.Ptr {
		rv.Set(pv)
	} else {
		rv.Set(pv.Elem())
	}
	return item
}

// — Handlers ——————————————————————————————————————————————————————————————

// listHandler serves GET /{prefix}.
func (m *Module[T]) listHandler(w http.ResponseWriter, r *http.Request) {
	ctx := ContextFrom(w, r)

	if m.serveCached(w, r) {
		return
	}

	// Lifecycle filter: push status constraint into the repository query.
	// Guests see only Published; Authors and above see all statuses.
	var statuses []Status
	if !ctx.User().HasRole(Author) {
		statuses = []Status{Published}
	}
	items, err := m.repo.FindAll(ctx, ListOptions{Status: statuses})
	if err != nil {
		WriteError(w, r, err)
		return
	}

	ct := m.neg.negotiate(r)
	if ct == "text/html" {
		m.renderListHTML(w, r, ctx, items)
		return
	}
	m.writeContentCached(w, r, ct, items)
}

// showHandler serves GET /{prefix}/{slug}.
func (m *Module[T]) showHandler(w http.ResponseWriter, r *http.Request) {
	ctx := ContextFrom(w, r)

	if m.serveCached(w, r) {
		return
	}

	slug := r.PathValue("slug")
	item, err := m.repo.FindBySlug(ctx, slug)
	if err != nil {
		WriteError(w, r, err)
		return
	}

	// Lifecycle enforcement: non-Published → 404 for Guest.
	// 404 is intentional — do not leak the existence of non-public content.
	if !isVisible(item, ctx.User()) {
		WriteError(w, r, ErrNotFound)
		return
	}

	ct := m.neg.negotiate(r)
	if ct == "text/html" {
		m.renderShowHTML(w, r, ctx, item)
		return
	}
	m.writeContentCached(w, r, ct, item)
}

// createHandler serves POST /{prefix}.
func (m *Module[T]) createHandler(w http.ResponseWriter, r *http.Request) {
	ctx := ContextFrom(w, r)

	if !ctx.User().HasRole(m.writeRole) {
		WriteError(w, r, ErrForbidden)
		return
	}

	pv, elemType := m.newItemPtr()
	if err := json.NewDecoder(r.Body).Decode(pv.Interface()); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			WriteError(w, r, ErrRequestTooLarge)
		} else {
			WriteError(w, r, ErrBadRequest)
		}
		return
	}

	// Assign a new ID and auto-generate a unique slug if not provided.
	f := getNodeFields(elemType)
	id := NewID()
	pv.Elem().FieldByIndex(f.id).SetString(id)
	if pv.Elem().FieldByIndex(f.slug).String() == "" {
		slug := autoSlug(pv.Elem())
		if slug == "" {
			slug = id[:8]
		}
		// Guarantee uniqueness by appending a counter suffix when the slug exists.
		slug = UniqueSlug(slug, func(s string) bool {
			_, err := m.repo.FindBySlug(ctx, s)
			return err == nil
		})
		pv.Elem().FieldByIndex(f.slug).SetString(slug)
	}

	item := ptrToT[T](pv, m.proto)

	// Validate before any persistence.
	if err := RunValidation(item); err != nil {
		WriteError(w, r, err)
		return
	}

	// BeforeCreate hooks (synchronous — first error aborts).
	if err := dispatchBefore(ctx, m.signals[BeforeCreate], item); err != nil {
		WriteError(w, r, err)
		return
	}

	if err := m.repo.Save(ctx, item); err != nil {
		WriteError(w, r, err)
		return
	}

	// AfterCreate hooks (asynchronous).
	dispatchAfter(ctx, m.signals[AfterCreate], item)

	// Status-based publish hooks.
	if nodeStatusOf(item) == Published {
		dispatchAfter(ctx, m.signals[AfterPublish], item)
	}

	m.invalidateCache()
	m.triggerRebuild()

	writeJSON(w, http.StatusCreated, item)
}

// updateHandler serves PUT /{prefix}/{slug}.
func (m *Module[T]) updateHandler(w http.ResponseWriter, r *http.Request) {
	ctx := ContextFrom(w, r)

	if !ctx.User().HasRole(m.writeRole) {
		WriteError(w, r, ErrForbidden)
		return
	}

	slug := r.PathValue("slug")
	existing, err := m.repo.FindBySlug(ctx, slug)
	if err != nil {
		WriteError(w, r, err)
		return
	}

	// Allocate a new item and decode the request body into it.
	pv, elemType := m.newItemPtr()
	if err := json.NewDecoder(r.Body).Decode(pv.Interface()); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			WriteError(w, r, ErrRequestTooLarge)
		} else {
			WriteError(w, r, ErrBadRequest)
		}
		return
	}

	// Preserve the original ID and Slug — URL is the source of truth.
	f := getNodeFields(elemType)
	pv.Elem().FieldByIndex(f.id).SetString(nodeIDOf(existing))
	pv.Elem().FieldByIndex(f.slug).SetString(slug)

	item := ptrToT[T](pv, m.proto)

	if err := RunValidation(item); err != nil {
		WriteError(w, r, err)
		return
	}

	// Save previous status for post-save signal dispatch.
	prevStatus := nodeStatusOf(existing)
	newStatus := nodeStatusOf(item)

	if err := dispatchBefore(ctx, m.signals[BeforeUpdate], item); err != nil {
		WriteError(w, r, err)
		return
	}

	if err := m.repo.Save(ctx, item); err != nil {
		WriteError(w, r, err)
		return
	}

	// Set PublishedAt on manual status transition to Published — mirrors the
	// scheduler's behaviour (see processScheduled). Must happen before signals
	// so AfterPublish handlers see the correct timestamp.
	if prevStatus != Published && newStatus == Published {
		setNodeTime(item, "PublishedAt", time.Now().UTC())
		if err := m.repo.Save(ctx, item); err != nil {
			WriteError(w, r, err)
			return
		}
	}

	dispatchAfter(ctx, m.signals[AfterUpdate], item)

	// Status-transition hooks.
	if prevStatus != Published && newStatus == Published {
		dispatchAfter(ctx, m.signals[AfterPublish], item)
	} else if prevStatus == Published && newStatus != Published {
		dispatchAfter(ctx, m.signals[AfterUnpublish], item)
	}
	if newStatus == Archived {
		dispatchAfter(ctx, m.signals[AfterArchive], item)
	}

	m.invalidateCache()
	m.triggerRebuild()

	writeJSON(w, http.StatusOK, item)
}

// deleteHandler serves DELETE /{prefix}/{slug}.
func (m *Module[T]) deleteHandler(w http.ResponseWriter, r *http.Request) {
	ctx := ContextFrom(w, r)

	if !ctx.User().HasRole(m.deleteRole) {
		WriteError(w, r, ErrForbidden)
		return
	}

	slug := r.PathValue("slug")
	existing, err := m.repo.FindBySlug(ctx, slug)
	if err != nil {
		WriteError(w, r, err)
		return
	}

	if err := dispatchBefore(ctx, m.signals[BeforeDelete], existing); err != nil {
		WriteError(w, r, err)
		return
	}

	id := nodeIDOf(existing)
	if err := m.repo.Delete(ctx, id); err != nil {
		WriteError(w, r, err)
		return
	}

	dispatchAfter(ctx, m.signals[AfterDelete], existing)

	m.invalidateCache()
	m.triggerRebuild()

	w.WriteHeader(http.StatusNoContent)
}

// — MCP implementation ————————————————————————————————————————————————————

// Compile-time assertion: *Module[T] must satisfy MCPModule.
var _ MCPModule = (*Module[struct{ Node }])(nil)

// typeName returns the unqualified type name of t, dereferencing pointer types.
func typeName(t reflect.Type) string {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t.Name()
}

// snakeCase converts a PascalCase or camelCase identifier to lower_snake_case.
// Consecutive uppercase letters are treated as a single word:
//
//	BlogPost → blog_post
//	MCPPost  → mcp_post
//	BlogID   → blog_id
//
// NOTE: This function is intentionally duplicated in forge-mcp/mcp.go.
// The two packages cannot import each other, so each carries its own copy.
// Any change to the algorithm here must be mirrored in forge-mcp/mcp.go, and vice versa.
func snakeCase(s string) string {
	runes := []rune(s)
	var b strings.Builder
	for i, r := range runes {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				prev := runes[i-1]
				prevLow := (prev >= 'a' && prev <= 'z') || (prev >= '0' && prev <= '9')
				prevUp := prev >= 'A' && prev <= 'Z'
				if prevLow {
					b.WriteByte('_')
				} else if prevUp {
					// Acronym boundary: insert _ before the last cap of an uppercase
					// run when it is immediately followed by a lowercase letter.
					if i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z' {
						b.WriteByte('_')
					}
				}
			}
			b.WriteRune(r + 32)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// mcpGoTypeStr maps a reflect.Type to an MCP schema type string.
// Pointer types are dereferenced before the lookup.
func mcpGoTypeStr(t reflect.Type) string {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t == reflect.TypeOf(time.Time{}) {
		return "datetime"
	}
	switch t.Kind() { //nolint:exhaustive
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice:
		return "array"
	default:
		return "string"
	}
}

// mcpJSONName returns the JSON key for a struct field: the json: tag name when
// present, otherwise the snakeCase conversion of the Go field name.
func mcpJSONName(sf reflect.StructField) string {
	if tag := sf.Tag.Get("json"); tag != "" && tag != "-" {
		if name := strings.SplitN(tag, ",", 2)[0]; name != "" {
			return name
		}
	}
	return snakeCase(sf.Name)
}

// mcpParseForgeTag extracts constraint metadata from a forge: struct tag value.
func mcpParseForgeTag(tag string) (required bool, minLen, maxLen int, enum []string) {
	for _, part := range strings.Split(tag, ",") {
		part = strings.TrimSpace(part)
		switch {
		case part == "required":
			required = true
		case strings.HasPrefix(part, "min="):
			minLen, _ = strconv.Atoi(strings.TrimPrefix(part, "min="))
		case strings.HasPrefix(part, "max="):
			maxLen, _ = strconv.Atoi(strings.TrimPrefix(part, "max="))
		case strings.HasPrefix(part, "oneof="):
			enum = strings.Split(strings.TrimPrefix(part, "oneof="), "|")
		}
	}
	return
}

// mcpStructField builds an MCPField descriptor from a reflect.StructField.
func mcpStructField(sf reflect.StructField) MCPField {
	f := MCPField{
		Name:     sf.Name,
		JSONName: mcpJSONName(sf),
		Type:     mcpGoTypeStr(sf.Type),
	}
	if tag := sf.Tag.Get("forge"); tag != "" {
		f.Required, f.MinLength, f.MaxLength, f.Enum = mcpParseForgeTag(tag)
	}
	return f
}

// MCPMeta returns the MCP registration metadata for this module.
func (m *Module[T]) MCPMeta() MCPMeta {
	t := m.proto
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return MCPMeta{
		Prefix:     m.prefix,
		TypeName:   typeName(t),
		Operations: m.mcpOps,
	}
}

// MCPSchema derives the field schema for this module's content type from Go
// struct fields and forge: struct tags. The embedded forge.Node fields Slug,
// Status, PublishedAt, and ScheduledAt are included; ID, CreatedAt, and
// UpdatedAt are omitted because they are managed by the framework.
func (m *Module[T]) MCPSchema() []MCPField {
	t := m.proto
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	nodeType := reflect.TypeOf(Node{})
	nodeSkip := map[string]bool{"ID": true, "CreatedAt": true, "UpdatedAt": true}

	var fields []MCPField
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		ft := sf.Type
		for ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		if sf.Anonymous && ft == nodeType {
			// Expand the embedded Node: include Slug, Status, PublishedAt,
			// ScheduledAt; skip ID, CreatedAt, UpdatedAt.
			for j := 0; j < nodeType.NumField(); j++ {
				nf := nodeType.Field(j)
				if !nf.IsExported() || nodeSkip[nf.Name] {
					continue
				}
				fields = append(fields, mcpStructField(nf))
			}
			continue
		}
		if sf.Anonymous {
			continue // skip other embedded types
		}
		fields = append(fields, mcpStructField(sf))
	}
	return fields
}

// MCPList returns all content items matching the given statuses. If no
// statuses are provided, items of all statuses are returned.
func (m *Module[T]) MCPList(ctx Context, status ...Status) ([]any, error) {
	opts := ListOptions{}
	if len(status) > 0 {
		opts.Status = status
	}
	items, err := m.repo.FindAll(ctx, opts)
	if err != nil {
		return nil, err
	}
	result := make([]any, len(items))
	for i, item := range items {
		result[i] = item
	}
	return result, nil
}

// MCPGet returns the item with the given slug regardless of its lifecycle
// status. The caller is responsible for enforcing visibility rules.
func (m *Module[T]) MCPGet(ctx Context, slug string) (any, error) {
	item, err := m.repo.FindBySlug(ctx, slug)
	if err != nil {
		return nil, err
	}
	return item, nil
}

// coerceSliceFields normalises fields destined for MCPCreate / MCPUpdate:
// any map value for a []string struct field that arrives as a plain string is
// split on "," and replaced with []any so the subsequent Marshal → Unmarshal
// round-trip decodes correctly. This tolerates MCP clients that serialise
// multi-value fields as comma-separated strings.
func coerceSliceFields(fields map[string]any, t reflect.Type) {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		ft := sf.Type
		for ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		if ft.Kind() != reflect.Slice {
			continue
		}
		key := mcpJSONName(sf)
		v, ok := fields[key]
		if !ok {
			continue
		}
		s, isStr := v.(string)
		if !isStr {
			continue
		}
		parts := strings.Split(s, ",")
		out := make([]any, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		fields[key] = out
	}
}

// MCPCreate creates a new content item from the supplied fields map. A new ID
// is always generated; the slug is auto-derived when absent. The item is
// validated before persistence. AfterCreate signals are dispatched
// asynchronously.
func (m *Module[T]) MCPCreate(ctx Context, fields map[string]any) (any, error) {
	pv, elemType := m.newItemPtr()
	coerceSliceFields(fields, elemType)
	data, err := json.Marshal(fields)
	if err != nil {
		return nil, ErrBadRequest
	}
	if err := json.Unmarshal(data, pv.Interface()); err != nil {
		return nil, ErrBadRequest
	}

	f := getNodeFields(elemType)
	id := NewID()
	pv.Elem().FieldByIndex(f.id).SetString(id)
	if pv.Elem().FieldByIndex(f.slug).String() == "" {
		slug := autoSlug(pv.Elem())
		if slug == "" {
			slug = id[:8]
		}
		slug = UniqueSlug(slug, func(s string) bool {
			_, err := m.repo.FindBySlug(ctx, s)
			return err == nil
		})
		pv.Elem().FieldByIndex(f.slug).SetString(slug)
	}
	if pv.Elem().FieldByIndex(f.status).String() == "" {
		pv.Elem().FieldByIndex(f.status).Set(reflect.ValueOf(Draft))
	}

	item := ptrToT[T](pv, m.proto)
	if err := RunValidation(item); err != nil {
		return nil, err
	}
	if err := m.repo.Save(ctx, item); err != nil {
		return nil, err
	}
	dispatchAfter(ctx, m.signals[AfterCreate], item)
	m.invalidateCache()
	return item, nil
}

// MCPUpdate applies a partial update to the item with the given slug. Fields
// present in the map overlay the existing item; absent fields are preserved.
// Node.ID, Node.Slug, and Node.Status are always restored after the merge —
// use the dedicated lifecycle methods to change status.
func (m *Module[T]) MCPUpdate(ctx Context, slug string, fields map[string]any) (any, error) {
	existing, err := m.repo.FindBySlug(ctx, slug)
	if err != nil {
		return nil, err
	}

	pv, elemType := m.newItemPtr()
	// Seed pv from the existing item so that fields absent from the map are
	// preserved (partial-update semantics).
	if seed, merr := json.Marshal(existing); merr == nil {
		json.Unmarshal(seed, pv.Interface()) //nolint:errcheck
	}
	coerceSliceFields(fields, elemType)
	data, err := json.Marshal(fields)
	if err != nil {
		return nil, ErrBadRequest
	}
	if err := json.Unmarshal(data, pv.Interface()); err != nil {
		return nil, ErrBadRequest
	}

	// Restore identity and lifecycle so callers cannot overwrite them.
	f := getNodeFields(elemType)
	pv.Elem().FieldByIndex(f.id).SetString(nodeIDOf(existing))
	pv.Elem().FieldByIndex(f.slug).SetString(slug)
	pv.Elem().FieldByIndex(f.status).Set(reflect.ValueOf(nodeStatusOf(existing)))

	item := ptrToT[T](pv, m.proto)
	if err := RunValidation(item); err != nil {
		return nil, err
	}
	if err := m.repo.Save(ctx, item); err != nil {
		return nil, err
	}
	dispatchAfter(ctx, m.signals[AfterUpdate], item)
	m.invalidateCache()
	return item, nil
}

// MCPPublish transitions the item with the given slug to Published, sets
// PublishedAt to now, fires AfterPublish, and triggers derived-content rebuild.
func (m *Module[T]) MCPPublish(ctx Context, slug string) error {
	item, err := m.repo.FindBySlug(ctx, slug)
	if err != nil {
		return err
	}
	setNodeStatus(item, Published)
	setNodeTime(item, "PublishedAt", time.Now().UTC())
	if err := m.repo.Save(ctx, item); err != nil {
		return err
	}
	dispatchAfter(ctx, m.signals[AfterPublish], item)
	m.invalidateCache()
	m.triggerRebuild()
	return nil
}

// MCPSchedule sets the item with the given slug to Scheduled and records
// the time at which it will be automatically published.
func (m *Module[T]) MCPSchedule(ctx Context, slug string, at time.Time) error {
	item, err := m.repo.FindBySlug(ctx, slug)
	if err != nil {
		return err
	}
	setNodeStatus(item, Scheduled)
	atCopy := at
	setNodeTimePtr(item, "ScheduledAt", &atCopy)
	if err := m.repo.Save(ctx, item); err != nil {
		return err
	}
	m.invalidateCache()
	return nil
}

// MCPArchive transitions the item with the given slug to Archived, fires
// AfterArchive, and triggers derived-content rebuild.
func (m *Module[T]) MCPArchive(ctx Context, slug string) error {
	item, err := m.repo.FindBySlug(ctx, slug)
	if err != nil {
		return err
	}
	setNodeStatus(item, Archived)
	if err := m.repo.Save(ctx, item); err != nil {
		return err
	}
	dispatchAfter(ctx, m.signals[AfterArchive], item)
	m.invalidateCache()
	m.triggerRebuild()
	return nil
}

// MCPDelete permanently removes the item with the given slug, fires
// AfterDelete, and triggers derived-content rebuild.
func (m *Module[T]) MCPDelete(ctx Context, slug string) error {
	item, err := m.repo.FindBySlug(ctx, slug)
	if err != nil {
		return err
	}
	if err := m.repo.Delete(ctx, nodeIDOf(item)); err != nil {
		return err
	}
	dispatchAfter(ctx, m.signals[AfterDelete], item)
	m.invalidateCache()
	m.triggerRebuild()
	return nil
}
