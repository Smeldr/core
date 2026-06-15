package smeldr

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// TypeDescriptor is the extensible type envelope for a content type registered
// with the App. The registry is the linchpin shared by T96 (ContentList
// resolver) and T06 (relation endpoints).
//
// The key space is dual: compiled modules register with their Go struct name
// (PascalCase, e.g. "BlogPost"); runtime-defined types register with a
// snake_case name (e.g. "blog_post"). The Prefix space is unified and must be
// globally unique — two types with the same prefix would produce conflicting
// routes.
//
// Reserved zero-value facets for later tasks (do not add columns to
// smeldr_content_type_schemas for these — each owning task adds its column):
//
//   - RolePolicy (T49): per-operation role lookup, currently returns Admin
//   - StateFlowRef (T23): custom state-flow declarations per type
//   - RenderFlags (T72): HTML on/off + layout template selection
type TypeDescriptor struct {
	Name   string             // canonical type name
	Prefix string             // URL prefix (operator-definable; default: pluralized Name)
	Schema *ContentTypeSchema // schema descriptor; nil for compiled modules in this increment
	Kind   string             // "block" | "content"
	// Fetch returns Published items as type-erased maps for the ContentList
	// block resolver (T96). Nil for runtime-defined types (T104 increment 2
	// will handle those via DynamicContentRepo). Set at App.Content() time
	// when the module implements ContentLister.
	Fetch func(ctx context.Context, opts ListOptions) ([]map[string]any, error)
}

// ContentLister is implemented by Module[T] to expose its repo's Published
// items as type-erased maps for the ContentList block resolver (T96).
type ContentLister interface {
	listPublished(ctx context.Context, opts ListOptions) ([]map[string]any, error)
}

// ContentTypeRegistry is a concurrency-safe name → *TypeDescriptor registry on
// App. Compiled modules are registered at Content() time; runtime-defined types
// are registered when define_content_type is called (T104 increment 2+).
//
// Access the registry via App.TypeRegistry().
//
// The prefixes map uses slash-stripped keys so LookupByPrefix("posts") finds a
// TypeDescriptor registered with Prefix "/posts".
type ContentTypeRegistry struct {
	mu       sync.RWMutex
	types    map[string]*TypeDescriptor // name → descriptor
	prefixes map[string]string          // slash-stripped prefix → name (reverse index)
}

func newContentTypeRegistry() *ContentTypeRegistry {
	return &ContentTypeRegistry{
		types:    make(map[string]*TypeDescriptor),
		prefixes: make(map[string]string),
	}
}

// Register adds a descriptor to the registry. It panics if the descriptor's
// Name or Prefix is already registered (name-collision guard: a runtime type
// cannot claim a name or prefix held by a compiled module, and vice versa).
func (r *ContentTypeRegistry) Register(d *TypeDescriptor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.types[d.Name]; exists {
		panic(fmt.Sprintf("smeldr: content type %q already registered", d.Name))
	}
	key := strings.TrimPrefix(d.Prefix, "/")
	if existing, exists := r.prefixes[key]; exists {
		panic(fmt.Sprintf("smeldr: content type prefix %q already claimed by %q", d.Prefix, existing))
	}
	r.types[d.Name] = d
	if key != "" {
		r.prefixes[key] = d.Name
	}
}

// Lookup returns the descriptor for the given type name, or nil when not found.
func (r *ContentTypeRegistry) Lookup(name string) *TypeDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.types[name]
}

// RegisterPrefix adds an additional URL prefix alias for an existing type name.
// It is used when the same compiled module type (same Go struct, same TypeName)
// is registered at more than one prefix. Panics if prefix is already claimed
// by a different type name, or if name is not registered.
func (r *ContentTypeRegistry) RegisterPrefix(prefix, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.types[name]; !exists {
		panic(fmt.Sprintf("smeldr: cannot add prefix %q: type %q not registered", prefix, name))
	}
	key := strings.TrimPrefix(prefix, "/")
	if existing, exists := r.prefixes[key]; exists {
		if existing == name {
			return // same name — idempotent
		}
		panic(fmt.Sprintf("smeldr: content type prefix %q already claimed by %q", prefix, existing))
	}
	r.prefixes[key] = name
}

// LookupByPrefix returns the descriptor whose Prefix matches the given prefix,
// or nil when not found. T96 uses this because ContentList.ContentType holds
// the prefix (e.g. "posts", "stories"), not the type name. Both "posts" and
// "/posts" are accepted — the leading slash is stripped before lookup.
func (r *ContentTypeRegistry) LookupByPrefix(prefix string) *TypeDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	key := strings.TrimPrefix(prefix, "/")
	name, ok := r.prefixes[key]
	if !ok {
		return nil
	}
	return r.types[name]
}

// All returns a snapshot of all registered descriptors in unspecified order.
func (r *ContentTypeRegistry) All() []*TypeDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*TypeDescriptor, 0, len(r.types))
	for _, d := range r.types {
		out = append(out, d)
	}
	return out
}
