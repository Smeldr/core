package forge

import (
	"net/http"
)

// TemplateData is the value passed to every HTML template rendered by Forge.
// T is the content type for show handlers (e.g. *BlogPost) or a slice type
// for list handlers (e.g. []*BlogPost).
//
// The framework-owned head fields (Head, OGDefaults, AppSchema, HeadAssets)
// are promoted from the embedded [PageHead] field and remain accessible at the
// top level of the struct — existing template calls like {{.Head.Title}} are
// unchanged.
//
// To use {{template "forge:head" .}} in a custom handler without TemplateData,
// embed [PageHead] directly in your own data struct:
//
//	type homeData struct {
//	    forge.PageHead
//	    Posts []*Post
//	}
//
// Show handler:
//
//	TemplateData[*BlogPost]{
//	    PageHead: forge.PageHead{Head: post.Head()},
//	    Content:  post,
//	    User:     ctx.User(),
//	    Request:  r,
//	    SiteName: "example.com",
//	}
//
// In templates:
//
//	{{template "forge:head" .}}
//	<h1>{{.Content.Title}}</h1>
//	<p>Welcome, {{.User.Name}}</p>
type TemplateData[T any] struct {
	// PageHead promotes Head, OGDefaults, AppSchema, and HeadAssets to the
	// top level of TemplateData. Templates access them as .Head, .OGDefaults,
	// .AppSchema, and .HeadAssets — identical to before embedding was used.
	PageHead

	// Content is the page payload — a single item for show templates,
	// a slice for list templates.
	Content T

	// User is the authenticated user for this request. Zero value ([GuestUser])
	// when the request is unauthenticated.
	User User

	// Request is the live *http.Request for this response. Use it in
	// templates for URL introspection, query parameters, or helpers that
	// require the request (e.g. [forge_csrf_token]).
	Request *http.Request

	// SiteName is the hostname extracted from [Config.BaseURL] at module
	// registration time (e.g. "example.com"). Uses the hostname rather than
	// [Context.SiteName] because SiteName() always returns "" in v1.
	SiteName string

	// Extra holds the value returned by the [ContextFunc] option for this
	// request. It is nil when no ContextFunc is configured. Templates access
	// it as {{.Extra}} and may assign it to a typed variable using a template
	// helper or direct assignment:
	//
	//	{{- $nav := .Extra}}
	//	{{template "sidebar" $nav}}
	Extra any
}

// NewTemplateData constructs a [TemplateData][T] for the given context,
// content, merged head, and site name.
//
// siteName should be the hostname extracted from [Config.BaseURL]
// (e.g. "example.com"), set once at module registration.
func NewTemplateData[T any](ctx Context, content T, head Head, siteName string) TemplateData[T] {
	return TemplateData[T]{
		PageHead: PageHead{Head: head},
		Content:  content,
		User:     ctx.User(),
		Request:  ctx.Request(),
		SiteName: siteName,
	}
}
