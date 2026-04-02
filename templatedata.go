package forge

import (
	"html/template"
	"net/http"
)

// TemplateData is the value passed to every HTML template rendered by Forge.
// T is the content type for show handlers (e.g. *BlogPost) or a slice type
// for list handlers (e.g. []*BlogPost).
//
// Show handler:
//
//	TemplateData[*BlogPost]{
//	    Content:  post,
//	    Head:     post.Head(),         // merged with module HeadFunc when set
//	    User:     ctx.User(),
//	    Request:  r,
//	    SiteName: "example.com",
//	}
//
// List handler:
//
//	TemplateData[[]*BlogPost]{
//	    Content:  posts,
//	    Head:     forge.Head{Title: "All Posts"},
//	    User:     ctx.User(),
//	    Request:  r,
//	    SiteName: "example.com",
//	}
//
// In templates:
//
//	{{template "forge:head" .Head}}
//	<h1>{{.Content.Title}}</h1>
//	<p>Welcome, {{.User.Name}}</p>
type TemplateData[T any] struct {
	// Content is the page payload — a single item for show templates,
	// a slice for list templates.
	Content T

	// Head carries all SEO and social metadata for this page, merged from
	// the content type's Head() method and any module-level HeadFunc.
	Head Head

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

	// OGDefaults holds the app-level Open Graph and Twitter Card fallback
	// values set via [App.SEO]. forge:head uses OGDefaults.TwitterSite to emit
	// twitter:site on every page; image and creator fallbacks are already merged
	// into [Head] before TemplateData is constructed.
	OGDefaults *OGDefaults

	// AppSchema is a pre-rendered <script type="application/ld+json"> block for
	// the app-level structured data set via [App.SEO] with [AppSchema]. It is
	// emitted automatically by the forge:head partial on every page.
	// The value is safe HTML produced by [renderAppSchema]; it is empty when
	// no [AppSchema] was registered.
	AppSchema template.HTML
}

// NewTemplateData constructs a [TemplateData][T] for the given context,
// content, merged head, and site name.
//
// siteName should be the hostname extracted from [Config.BaseURL]
// (e.g. "example.com"), set once at module registration.
func NewTemplateData[T any](ctx Context, content T, head Head, siteName string) TemplateData[T] {
	return TemplateData[T]{
		Content:  content,
		Head:     head,
		User:     ctx.User(),
		Request:  ctx.Request(),
		SiteName: siteName,
	}
}
