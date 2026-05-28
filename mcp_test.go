package smeldr

import (
	"testing"
	"time"
)

// TestMCP verifies that MCP returns a valid Option for all valid combinations
// of MCPOperation arguments, including zero arguments, and that the ops are
// stored on the returned mcpOption.
func TestMCP(t *testing.T) {
	tests := []struct {
		name string
		ops  []MCPOperation
	}{
		{"MCPRead only", []MCPOperation{MCPRead}},
		{"MCPWrite only", []MCPOperation{MCPWrite}},
		{"MCPRead and MCPWrite", []MCPOperation{MCPRead, MCPWrite}},
		{"no args", []MCPOperation{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opt := MCP(tc.ops...)
			if opt == nil {
				t.Fatal("MCP() returned nil, want non-nil Option")
			}
			mo, ok := opt.(mcpOption)
			if !ok {
				t.Errorf("MCP() type = %T, want mcpOption", opt)
			}
			if len(mo.ops) != len(tc.ops) {
				t.Errorf("ops length = %d, want %d", len(mo.ops), len(tc.ops))
			}
		})
	}
}

// TestMCPModuleMeta verifies MCPMeta() on a Module registered with MCP options.
func TestMCPModuleMeta(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := newTestModule(repo, MCP(MCPRead, MCPWrite))

	meta := m.MCPMeta()
	if meta.Prefix != "/testposts" {
		t.Errorf("Prefix = %q, want /testposts", meta.Prefix)
	}
	if meta.TypeName != "testPost" {
		t.Errorf("TypeName = %q, want testPost", meta.TypeName)
	}
	if len(meta.Operations) != 2 {
		t.Fatalf("Operations length = %d, want 2", len(meta.Operations))
	}
	if meta.Operations[0] != MCPRead || meta.Operations[1] != MCPWrite {
		t.Errorf("Operations = %v, want [read write]", meta.Operations)
	}
}

// TestMCPModuleMetaNone verifies that a Module without MCP returns a zero MCPMeta.
func TestMCPModuleMetaNone(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := newTestModule(repo)
	meta := m.MCPMeta()
	if len(meta.Operations) != 0 {
		t.Errorf("Operations = %v, want empty", meta.Operations)
	}
}

// TestSnakeCase verifies the snakeCase helper covers common cases.
func TestSnakeCase(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"BlogPost", "blog_post"},
		{"MCPPost", "mcp_post"},
		{"ID", "id"},
		{"BlogID", "blog_id"},
		{"blogPost", "blog_post"},
		{"post", "post"},
		{"Post2", "post2"},
		{"URLPath", "url_path"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got := snakeCase(tc.in)
			if got != tc.want {
				t.Errorf("snakeCase(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestMCPSchema verifies that MCPSchema derives the correct fields from a
// content type's struct definition and forge: tags.
func TestMCPSchema(t *testing.T) {
	// testPost has: Node (embedded), Title forge:"required", Body (no tag).
	repo := NewMemoryRepo[*testPost]()
	m := newTestModule(repo)
	fields := m.MCPSchema()

	// Build a lookup map by Name for easy assertions.
	byName := make(map[string]MCPField, len(fields))
	for _, f := range fields {
		byName[f.Name] = f
	}

	// Node fields that must be present.
	for _, nodeName := range []string{"Slug", "Status", "PublishedAt", "ScheduledAt"} {
		if _, ok := byName[nodeName]; !ok {
			t.Errorf("MCPSchema missing Node field %q", nodeName)
		}
	}
	// ID must be absent.
	if _, ok := byName["ID"]; ok {
		t.Error("MCPSchema must not include Node.ID")
	}
	// CreatedAt / UpdatedAt must be absent.
	for _, name := range []string{"CreatedAt", "UpdatedAt"} {
		if _, ok := byName[name]; ok {
			t.Errorf("MCPSchema must not include Node.%s", name)
		}
	}

	// Title: required, type string.
	tf, ok := byName["Title"]
	if !ok {
		t.Fatal("MCPSchema missing Title field")
	}
	if !tf.Required {
		t.Error("Title.Required = false, want true")
	}
	if tf.Type != "string" {
		t.Errorf("Title.Type = %q, want string", tf.Type)
	}
	if tf.JSONName != "title" {
		t.Errorf("Title.JSONName = %q, want title", tf.JSONName)
	}

	// Body: not required, type string.
	bf, ok := byName["Body"]
	if !ok {
		t.Fatal("MCPSchema missing Body field")
	}
	if bf.Required {
		t.Error("Body.Required = true, want false")
	}

	// PublishedAt: type datetime.
	paf := byName["PublishedAt"]
	if paf.Type != "datetime" {
		t.Errorf("PublishedAt.Type = %q, want datetime", paf.Type)
	}
}

// TestMCPSchemaWithConstraints verifies min, max, and oneof constraints.
func TestMCPSchemaWithConstraints(t *testing.T) {
	type constrainedPost struct {
		Node
		Title    string `forge:"required,min=3,max=100"`
		Category string `forge:"oneof=news|blog|tutorial"`
	}
	repo := NewMemoryRepo[*constrainedPost]()
	m := NewModule((*constrainedPost)(nil), Repo(repo), At("/cposts"))
	fields := m.MCPSchema()

	byName := make(map[string]MCPField)
	for _, f := range fields {
		byName[f.Name] = f
	}

	tf := byName["Title"]
	if tf.MinLength != 3 {
		t.Errorf("Title.MinLength = %d, want 3", tf.MinLength)
	}
	if tf.MaxLength != 100 {
		t.Errorf("Title.MaxLength = %d, want 100", tf.MaxLength)
	}

	cf := byName["Category"]
	if len(cf.Enum) != 3 {
		t.Errorf("Category.Enum length = %d, want 3", len(cf.Enum))
	}
}

// TestMCPSchema_formatTags verifies that smeldr_format and smeldr_description
// struct tags are reflected in MCPField.Format and MCPField.Description
// (Decision 27).
func TestMCPSchema_formatTags(t *testing.T) {
	type richPost struct {
		Node
		Body  string `forge:"required" smeldr_format:"markdown" smeldr_description:"Write content in Markdown."`
		Embed string `smeldr_format:"html"`
		Plain string
	}
	repo := NewMemoryRepo[*richPost]()
	m := NewModule((*richPost)(nil), Repo(repo), At("/richposts"))
	fields := m.MCPSchema()

	byName := make(map[string]MCPField)
	for _, f := range fields {
		byName[f.Name] = f
	}

	body := byName["Body"]
	if body.Format != "markdown" {
		t.Errorf("Body.Format = %q, want markdown", body.Format)
	}
	if body.Description != "Write content in Markdown." {
		t.Errorf("Body.Description = %q, want \"Write content in Markdown.\"", body.Description)
	}

	embed := byName["Embed"]
	if embed.Format != "html" {
		t.Errorf("Embed.Format = %q, want html", embed.Format)
	}
	if embed.Description != "" {
		t.Errorf("Embed.Description = %q, want empty", embed.Description)
	}

	plain := byName["Plain"]
	if plain.Format != "" {
		t.Errorf("Plain.Format = %q, want empty", plain.Format)
	}
	if plain.Description != "" {
		t.Errorf("Plain.Description = %q, want empty", plain.Description)
	}
}

// TestAppMCPModules verifies that App.MCPModules returns only modules that have
// MCP operations registered.
func TestAppMCPModules(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")})

	// Module 1: MCPRead only.
	m1 := newTestModule(NewMemoryRepo[*testPost](), At("/posts"), MCP(MCPRead))
	// Module 2: MCPRead+MCPWrite.
	m2 := newTestModule(NewMemoryRepo[*testPost](), At("/drafts"), MCP(MCPRead, MCPWrite))
	// Module 3: no MCP.
	m3 := newTestModule(NewMemoryRepo[*testPost](), At("/other"))

	app.Content(m1)
	app.Content(m2)
	app.Content(m3)

	got := app.MCPModules()
	if len(got) != 2 {
		t.Fatalf("MCPModules length = %d, want 2", len(got))
	}
	// Verify the returned modules have the expected prefixes.
	prefixes := map[string]bool{"/posts": true, "/drafts": true}
	for _, mm := range got {
		if !prefixes[mm.MCPMeta().Prefix] {
			t.Errorf("unexpected MCPModule prefix %q", mm.MCPMeta().Prefix)
		}
	}
}

// TestMCPModuleInterface verifies that MCPList, MCPGet, MCPCreate, MCPUpdate,
// MCPPublish, MCPSchedule, MCPArchive, and MCPDelete work end-to-end with a
// MemoryRepo.
func TestMCPModuleInterface(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := newTestModule(repo, At("/posts"), MCP(MCPRead, MCPWrite))
	ctx := NewTestContext(User{ID: "u1", Roles: []Role{Admin}})

	// Create.
	created, err := m.MCPCreate(ctx, map[string]any{"title": "Hello World"})
	if err != nil {
		t.Fatalf("MCPCreate: %v", err)
	}
	post := created.(*testPost)
	if post.Title != "Hello World" {
		t.Errorf("Title = %q, want Hello World", post.Title)
	}
	if post.Status != Draft {
		t.Errorf("Status = %q, want draft", post.Status)
	}
	slug := post.Slug

	// List — returns 1 item.
	items, err := m.MCPList(ctx)
	if err != nil {
		t.Fatalf("MCPList: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("MCPList len = %d, want 1", len(items))
	}

	// Get.
	got, err := m.MCPGet(ctx, slug)
	if err != nil {
		t.Fatalf("MCPGet: %v", err)
	}
	if got.(*testPost).Title != "Hello World" {
		t.Errorf("MCPGet title = %q", got.(*testPost).Title)
	}

	// Update.
	updated, err := m.MCPUpdate(ctx, slug, map[string]any{"title": "Updated"})
	if err != nil {
		t.Fatalf("MCPUpdate: %v", err)
	}
	if updated.(*testPost).Title != "Updated" {
		t.Errorf("MCPUpdate title = %q", updated.(*testPost).Title)
	}
	// Verify Status was preserved.
	if updated.(*testPost).Status != Draft {
		t.Errorf("MCPUpdate Status = %q, want draft (must not change)", updated.(*testPost).Status)
	}

	// Publish.
	if err := m.MCPPublish(ctx, slug); err != nil {
		t.Fatalf("MCPPublish: %v", err)
	}
	published, _ := m.MCPGet(ctx, slug)
	if published.(*testPost).Status != Published {
		t.Errorf("after MCPPublish Status = %q, want published", published.(*testPost).Status)
	}
	if published.(*testPost).PublishedAt.IsZero() {
		t.Error("PublishedAt is zero after MCPPublish")
	}

	// Archive.
	if err := m.MCPArchive(ctx, slug); err != nil {
		t.Fatalf("MCPArchive: %v", err)
	}
	archived, _ := m.MCPGet(ctx, slug)
	if archived.(*testPost).Status != Archived {
		t.Errorf("after MCPArchive Status = %q, want archived", archived.(*testPost).Status)
	}

	// Schedule.
	when := time.Now().Add(24 * time.Hour).UTC()
	if err := m.MCPSchedule(ctx, slug, when); err != nil {
		t.Fatalf("MCPSchedule: %v", err)
	}
	scheduled, _ := m.MCPGet(ctx, slug)
	if scheduled.(*testPost).Status != Scheduled {
		t.Errorf("after MCPSchedule Status = %q, want scheduled", scheduled.(*testPost).Status)
	}

	// Delete.
	if err := m.MCPDelete(ctx, slug); err != nil {
		t.Fatalf("MCPDelete: %v", err)
	}
	if _, err := m.MCPGet(ctx, slug); err == nil {
		t.Error("MCPGet after MCPDelete should return error")
	}
}
