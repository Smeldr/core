package smeldr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// defaultBlockMaxDepth bounds both the batched load and the render recursion. It
// protects against composition cycles (a shared block that contains an ancestor)
// and runaway nesting. Real pages are 2–3 levels deep; 16 is a generous backstop.
const defaultBlockMaxDepth = 16

// blockFieldFormats records which of a block type's [DynamicNode.Fields] keys are
// Markdown (rendered to safe HTML before reaching the template), which are trusted
// raw HTML (emitted verbatim), and which are reference fields (a "{Name}ID" string
// that resolves to a ".{Name}" sub-object). Keys are PascalCase — see
// [blockFieldRegistry].
type blockFieldFormats struct {
	markdown []string
	html     []string

	// refs are reference field names of the form "{Name}ID" (e.g. "ImageID").
	// At render time each resolves to a ".{Name}" sub-object (e.g. ".Image")
	// holding the referenced Published block's buildData output. See
	// [BlockRenderer.renderBlock].
	refs []string
}

// blockFieldRegistry is the interim source of per-type field-format metadata for
// the block system, derived from the block-system.md type tables. It tells
// [BlockRenderer] which Fields to render as Markdown and which to pass through as
// raw HTML. The content_type_schemas table (T32 component 7) will replace it.
//
// Field keys are PascalCase: block Fields are stored with PascalCase keys (e.g.
// "Title", "Body"), the canonical convention that lets templates access them as
// .Title / .Body. A block stored with snake_case keys would not bind.
var blockFieldRegistry = map[string]blockFieldFormats{
	"content_block":   {markdown: []string{"Body"}, refs: []string{"ImageID"}},
	"image":           {markdown: []string{"Caption"}},
	"link_item":       {markdown: []string{"Body"}},
	"html_block":      {html: []string{"HTML"}},
	"quote":           {markdown: []string{"QuoteText", "Context"}},
	"contact_card":    {markdown: []string{"Body"}, refs: []string{"ImageID"}},
	"faq_item":        {markdown: []string{"Answer"}},
	"hero":            {markdown: []string{"Subtext"}, refs: []string{"ImageID"}},
	"footer":          {markdown: []string{"Body"}},
	"content_grid":    {markdown: []string{"Subtitle"}},
	"gallery":         {markdown: []string{"Subtitle"}},
	"link_collection": {markdown: []string{"Subtitle"}},
	"html_grid":       {markdown: []string{"Subtitle"}},
	"faq":             {markdown: []string{"Subtitle"}},
	"team":            {markdown: []string{"Subtitle"}},
	"content_list":    {},
}

// BlockRenderer assembles a page from blocks and composition edges and renders it
// to HTML using convention templates (one templates/blocks/<type_name>.html per
// block type). Obtain one with [App.ServeBlocks]. A BlockRenderer is safe for
// concurrent use after construction (its templates and registry are read-only).
type BlockRenderer struct {
	repo      *SQLRepo[*DynamicNode]
	edges     *ContentEdgeStore
	db        DB
	templates map[string]*template.Template // type_name → parsed template
	maxDepth  int
}

// ServeBlocks ensures the block tables exist, parses the block templates in dir
// (one <type_name>.html per block type), and returns a [BlockRenderer].
//
// ServeBlocks is an app subsystem: it calls [CreateBlockTables] on the App's
// configured database. It returns an error if no database is configured or a
// template fails to parse.
//
//	r, err := app.ServeBlocks("templates/blocks")
//	html, err := r.Render(ctx, "page", pageID)
func (a *App) ServeBlocks(dir string) (*BlockRenderer, error) {
	db := a.Config().DB
	if db == nil {
		return nil, fmt.Errorf("smeldr: ServeBlocks requires a database (Config.DB is nil)")
	}
	if err := CreateBlockTables(db); err != nil {
		return nil, fmt.Errorf("smeldr: ServeBlocks create tables: %w", err)
	}
	tmpls, err := parseBlockTemplates(dir)
	if err != nil {
		return nil, err
	}
	return &BlockRenderer{
		repo:      NewDynamicContentRepo(db),
		edges:     NewContentEdgeStore(db),
		db:        db,
		templates: tmpls,
		maxDepth:  defaultBlockMaxDepth,
	}, nil
}

// parseBlockTemplates parses every *.html file in dir into its own template set
// (with [TemplateFuncMap]), keyed by the file's base name without the extension
// (the block type_name).
func parseBlockTemplates(dir string) (map[string]*template.Template, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("smeldr: ServeBlocks read template dir %q: %w", dir, err)
	}
	out := make(map[string]*template.Template)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".html") {
			continue
		}
		typeName := strings.TrimSuffix(e.Name(), ".html")
		path := filepath.Join(dir, e.Name())
		tpl, err := template.New(e.Name()).Funcs(TemplateFuncMap()).ParseFiles(path)
		if err != nil {
			return nil, fmt.Errorf("smeldr: ServeBlocks parse %s: %w", e.Name(), err)
		}
		out[typeName] = tpl
	}
	return out, nil
}

// Render assembles the page identified by (pageType, pageID) into HTML: the
// ordered concatenation of its rendered, Published direct child blocks (its
// sections), with each collection block's items rendered in turn.
//
// Render degrades gracefully and never returns a partial-failure error: a block
// that is unpublished, missing, malformed, or whose template is missing or errors
// is skipped (and logged via slog) — it never takes down the page. The returned
// error is non-nil only for an underlying database fault while loading the page's
// top-level edges. pageType is reserved for future region scoping; the lookup
// keys on pageID.
func (r *BlockRenderer) Render(ctx context.Context, pageType, pageID string) (template.HTML, error) {
	_ = pageType // reserved for future region scoping; pageID is the unique key.

	sectionEdges, err := r.edges.Children(ctx, pageID)
	if err != nil {
		return "", err
	}
	if len(sectionEdges) == 0 {
		return "", nil // empty page — not an error
	}

	blocks, childEdges := r.loadTree(ctx, sectionEdges)

	var out strings.Builder
	path := make(map[string]bool)
	for _, e := range sectionEdges {
		out.WriteString(string(r.renderBlock(e.ChildID, blocks, childEdges, path, 0)))
	}
	return template.HTML(out.String()), nil
}

// loadTree batch-loads the full block tree reachable from the given section edges,
// one level at a time (no N+1): per level, one IN() query for the blocks and one
// ChildrenOf query for their item edges. It stops at maxDepth or when no new
// blocks are reachable, so a composition cycle cannot cause unbounded loading.
// Only Published blocks are loaded; unpublished and missing blocks are simply
// absent and are skipped at render.
func (r *BlockRenderer) loadTree(ctx context.Context, sectionEdges []ContentEdge) (map[string]*DynamicNode, map[string][]ContentEdge) {
	blocks := make(map[string]*DynamicNode)
	childEdges := make(map[string][]ContentEdge)
	seen := make(map[string]bool)

	frontier := childIDsOf(sectionEdges)
	for depth := 0; depth < r.maxDepth && len(frontier) > 0; depth++ {
		toLoad := make([]string, 0, len(frontier))
		for _, id := range frontier {
			if !seen[id] {
				seen[id] = true
				toLoad = append(toLoad, id)
			}
		}
		if len(toLoad) == 0 {
			break
		}
		for _, b := range r.loadBlocks(ctx, toLoad) {
			blocks[b.ID] = b
		}
		edges, err := r.edges.ChildrenOf(ctx, toLoad)
		if err != nil {
			break // graceful: stop descending on a load fault
		}
		next := make([]string, 0, len(edges))
		for _, e := range edges {
			childEdges[e.ParentID] = append(childEdges[e.ParentID], e)
			next = append(next, e.ChildID)
		}
		frontier = next
	}

	// Reference-field resolution load: collect every declared reference id across
	// the loaded tree and batch-load the referenced Published blocks in ONE IN()
	// query. Referenced blocks land in `blocks` (used only for resolution in
	// renderBlock — they are not in childEdges, so never rendered standalone).
	// A single pass suffices for the current type set: the only reference field
	// is ImageID and Image (L2) declares no refs, so referenced blocks have no
	// further references to follow.
	var refIDs []string
	pendingRef := make(map[string]bool)
	for _, b := range blocks {
		for _, id := range r.refIDsOf(b) {
			if _, loaded := blocks[id]; loaded || pendingRef[id] {
				continue
			}
			pendingRef[id] = true
			refIDs = append(refIDs, id)
		}
	}
	for _, b := range r.loadBlocks(ctx, refIDs) {
		blocks[b.ID] = b
	}

	return blocks, childEdges
}

// refIDsOf returns the non-empty string values of b's declared reference fields
// (per [blockFieldRegistry]). Returns nil when the type has no reference fields
// or Fields cannot be decoded.
func (r *BlockRenderer) refIDsOf(b *DynamicNode) []string {
	refs := blockFieldRegistry[b.TypeName].refs
	if len(refs) == 0 || len(b.Fields) == 0 {
		return nil
	}
	var fields map[string]any
	if err := json.Unmarshal(b.Fields, &fields); err != nil {
		return nil
	}
	var ids []string
	for _, name := range refs {
		if id, ok := fields[name].(string); ok && id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// loadBlocks batch-loads the Published blocks with the given IDs in one IN()
// query. A load fault yields no blocks (the page degrades, it does not crash).
func (r *BlockRenderer) loadBlocks(ctx context.Context, ids []string) []*DynamicNode {
	if len(ids) == 0 {
		return nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+1)
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args = append(args, id)
	}
	args = append(args, string(Published))
	query := "SELECT * FROM smeldr_dynamic_content WHERE id IN (" +
		strings.Join(placeholders, ", ") + ") AND status = $" + fmt.Sprint(len(ids)+1)
	nodes, err := Query[*DynamicNode](ctx, r.db, query, args...)
	if err != nil {
		slog.Warn("serveblocks: batch block load failed", "err", err)
		return nil
	}
	return nodes
}

// renderBlock renders one block to HTML from the pre-loaded maps. It returns ""
// (skipped) for a missing/unpublished block, a cycle on the current path, a
// missing template, malformed Fields, or a template execution error — each logged
// where it is a real fault. path is the set of block IDs on the current DFS path,
// used to break cycles; it is mutated and restored across recursion.
func (r *BlockRenderer) renderBlock(id string, blocks map[string]*DynamicNode, childEdges map[string][]ContentEdge, path map[string]bool, depth int) template.HTML {
	if depth >= r.maxDepth || path[id] {
		return ""
	}
	block, ok := blocks[id]
	if !ok {
		return "" // unpublished, missing, or dangling — skip
	}
	tpl, ok := r.templates[block.TypeName]
	if !ok {
		slog.Warn("serveblocks: no template for block type", "type_name", block.TypeName, "id", id)
		return ""
	}
	data, ok := r.buildData(block)
	if !ok {
		return "" // malformed Fields — skip (logged in buildData)
	}

	// Resolve declared reference fields: {Name}ID → .{Name} sub-object holding the
	// referenced Published block's buildData. An absent / unpublished / dangling
	// reference leaves no .{Name} key, so a {{ with .Name }} guard renders nothing.
	for _, refName := range blockFieldRegistry[block.TypeName].refs {
		refID, _ := data[refName].(string)
		refBlock, found := blocks[refID]
		if refID == "" || !found {
			continue
		}
		if refData, okRef := r.buildData(refBlock); okRef {
			data[strings.TrimSuffix(refName, "ID")] = refData
		}
	}

	if items := childEdges[id]; len(items) > 0 {
		path[id] = true
		rendered := make([]template.HTML, 0, len(items))
		for _, e := range items {
			if h := r.renderBlock(e.ChildID, blocks, childEdges, path, depth+1); h != "" {
				rendered = append(rendered, h)
			}
		}
		delete(path, id)
		data["Items"] = rendered
	}

	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		slog.Warn("serveblocks: template execution failed", "type_name", block.TypeName, "id", id, "err", err)
		return ""
	}
	return template.HTML(buf.String())
}

// buildData constructs the per-block template payload per the block-system.md
// template data contract: Node fields (ID/Slug/Status), the decoded Fields
// promoted to top level, AnchorID always present, and Markdown / raw-HTML fields
// converted to template.HTML per [blockFieldRegistry]. Returns ok=false (skip)
// only when Fields is present but not valid JSON.
func (r *BlockRenderer) buildData(b *DynamicNode) (map[string]any, bool) {
	data := make(map[string]any)
	if len(b.Fields) > 0 {
		if err := json.Unmarshal(b.Fields, &data); err != nil {
			slog.Warn("serveblocks: malformed Fields JSON", "id", b.ID, "type_name", b.TypeName, "err", err)
			return nil, false
		}
	}
	data["ID"] = b.ID
	data["Slug"] = b.Slug
	data["Status"] = string(b.Status)
	if _, ok := data["AnchorID"]; !ok {
		data["AnchorID"] = ""
	}

	formats := blockFieldRegistry[b.TypeName]
	for _, f := range formats.markdown {
		if s, ok := data[f].(string); ok {
			data[f] = renderMarkdown(s)
		}
	}
	for _, f := range formats.html {
		if s, ok := data[f].(string); ok {
			data[f] = template.HTML(s) //nolint:gosec // trusted raw-HTML field per block schema
		}
	}
	return data, true
}

// childIDsOf returns the child IDs of the given edges in order.
func childIDsOf(edges []ContentEdge) []string {
	ids := make([]string, len(edges))
	for i, e := range edges {
		ids[i] = e.ChildID
	}
	return ids
}
