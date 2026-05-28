package smeldr

import (
	"html/template"
	"strings"
	"testing"
	"time"
)

// tdPost is a minimal content type used by TemplateData tests.
type tdPost struct {
	Node
	Title string
}

func (p *tdPost) Head() Head {
	return Head{Title: p.Title}
}

func TestTemplateData_show(t *testing.T) {
	post := &tdPost{Title: "Hello"}
	post.Slug = "hello"

	u := User{ID: "u1", Name: "Alice", Roles: []Role{Editor}}
	ctx := NewTestContext(u)

	h := Head{Title: "Hello", Description: "desc"}
	td := NewTemplateData(ctx, post, h, "example.com")

	if td.Content != post {
		t.Errorf("Content = %v, want %v", td.Content, post)
	}
	if td.Head.Title != "Hello" {
		t.Errorf("Head.Title = %q, want %q", td.Head.Title, "Hello")
	}
	if td.User.ID != "u1" {
		t.Errorf("User.ID = %q, want %q", td.User.ID, "u1")
	}
	if td.Request != ctx.Request() {
		t.Error("Request is not the context's request")
	}
	if td.SiteName != "example.com" {
		t.Errorf("SiteName = %q, want %q", td.SiteName, "example.com")
	}
}

func TestTemplateData_list(t *testing.T) {
	posts := []*tdPost{
		{Title: "Post 1"},
		{Title: "Post 2"},
	}

	ctx := NewTestContext(GuestUser)

	h := Head{Title: "All Posts"}
	td := NewTemplateData(ctx, posts, h, "blog.example.com")

	if len(td.Content) != 2 {
		t.Errorf("len(Content) = %d, want 2", len(td.Content))
	}
	if td.Head.Title != "All Posts" {
		t.Errorf("Head.Title = %q, want %q", td.Head.Title, "All Posts")
	}
	if td.SiteName != "blog.example.com" {
		t.Errorf("SiteName = %q, want %q", td.SiteName, "blog.example.com")
	}
}

func TestTemplateData_guest(t *testing.T) {
	ctx := NewTestContext(GuestUser)

	td := NewTemplateData(ctx, (*tdPost)(nil), Head{}, "example.com")

	if td.User.ID != "" {
		t.Errorf("User.ID = %q, want empty for guest", td.User.ID)
	}
	if td.User.Name != "" {
		t.Errorf("User.Name = %q, want empty for guest", td.User.Name)
	}
	if len(td.User.Roles) != 0 {
		t.Errorf("User.Roles = %v, want nil for guest", td.User.Roles)
	}
}

func TestTemplateData_siteName(t *testing.T) {
	ctx := NewTestContext(GuestUser)

	td := NewTemplateData(ctx, struct{}{}, Head{}, "my-site.io")
	if td.SiteName != "my-site.io" {
		t.Errorf("SiteName = %q, want %q", td.SiteName, "my-site.io")
	}
}

func TestTemplateData_headFields(t *testing.T) {
	ctx := NewTestContext(GuestUser)

	pub := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	h := Head{
		Title:       "My Page",
		Description: "A description",
		Author:      "Alice",
		Published:   pub,
		Type:        Article,
		NoIndex:     true,
		Canonical:   "https://example.com/my-page",
	}

	td := NewTemplateData(ctx, struct{}{}, h, "example.com")

	if td.Head.Title != "My Page" {
		t.Errorf("Head.Title = %q", td.Head.Title)
	}
	if !td.Head.NoIndex {
		t.Error("Head.NoIndex should be true")
	}
	if td.Head.Type != Article {
		t.Errorf("Head.Type = %q, want %q", td.Head.Type, Article)
	}
	if td.Head.Canonical != "https://example.com/my-page" {
		t.Errorf("Head.Canonical = %q", td.Head.Canonical)
	}
	if !td.Head.Published.Equal(pub) {
		t.Errorf("Head.Published = %v, want %v", td.Head.Published, pub)
	}
}

// TestTemplateData_PageHead_embedding confirms that a custom data struct
// embedding PageHead can be passed to smeldr:head and that Go's html/template
// engine promotes the embedded fields to the top level, making .Head,
// .OGDefaults, .AppSchema, and .HeadAssets accessible without qualification.
func TestTemplateData_PageHead_embedding(t *testing.T) {
	type homeData struct {
		PageHead
		Extra string
	}

	data := homeData{
		PageHead: PageHead{
			Head: Head{Title: "Home"},
			HeadAssets: &HeadAssets{
				Stylesheets: []string{"/static/app.css"},
			},
		},
		Extra: "ignored",
	}

	tmpl := template.Must(template.New("test").Funcs(TemplateFuncMap()).Parse(smeldrHeadTmpl))

	var buf strings.Builder
	if err := tmpl.ExecuteTemplate(&buf, "smeldr:head", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}

	out := buf.String()

	if !strings.Contains(out, "<title>Home</title>") {
		t.Errorf("missing <title>: %s", out)
	}
	if !strings.Contains(out, `<link rel="stylesheet" href="/static/app.css">`) {
		t.Errorf("missing stylesheet link: %s", out)
	}
}

// Compile-time check: TemplateData[*Node] is a valid instantiation.
var _ TemplateData[*Node]
