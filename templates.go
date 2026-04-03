package forge

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"sort"
)

// — Option types ——————————————————————————————————————————————————————————

// templateParser is implemented by [*Module] once a Templates or
// TemplatesOptional option is provided. [App.Content] appends each
// implementing module to its internal list; [App.Run] calls
// parseTemplates on all of them before the server starts.
type templateParser interface {
	parseTemplates() error
}

// templatesOption carries the directory path and required flag for HTML
// templates. Created by [Templates] or [TemplatesOptional].
type templatesOption struct {
	dir      string
	required bool
}

func (templatesOption) isOption() {}

// Templates returns an [Option] that sets the directory containing HTML
// templates for a module. The directory must contain list.html and show.html;
// if either file is absent [App.Run] returns an error before the server starts.
//
// Template files are parsed once at startup. The expected layout is:
//
//	{dir}/list.html        — rendered for GET /{prefix}
//	{dir}/show.html        — rendered for GET /{prefix}/{slug}
//	{dir}/errors/404.html  — (optional) custom error page for 404 responses
//
// Use [TemplatesOptional] during development when template files are added
// incrementally.
func Templates(dir string) Option { return templatesOption{dir: dir, required: true} }

// TemplatesOptional returns an [Option] that sets the template directory but
// treats absent files as a silent no-op. HTML content negotiation is only
// enabled for a handler when its corresponding template file is found.
//
// Use this during development when templates are added incrementally.
func TemplatesOptional(dir string) Option { return templatesOption{dir: dir, required: false} }

// TemplatesWatch is deferred to Milestone 5. It will provide hot-reload of
// template files during development without restarting the server.

// — forge:head template ———————————————————————————————————————————————————

// forgeHeadTmpl is the named template injected into every parsed template set
// as "forge:head". Developers invoke it inside their own <head> element:
//
//	{{template "forge:head" .}}
//
// The template receives the full [TemplateData] value and renders: title,
// description meta, canonical link, Open Graph tags, Twitter Card tags
// (including the app-level twitter:site from [OGDefaults]), app-level
// JSON-LD from [AppSchema], and a robots noindex tag when [Head.NoIndex]
// is true. JSON-LD for individual content items is not emitted here —
// place {{forge_meta .Head .Content}} in the template body to control
// JSON-LD placement and schema type.
const forgeHeadTmpl = `{{define "forge:head"}}<title>{{.Head.Title}}</title>
{{- if .Head.Description}}
<meta name="description" content="{{.Head.Description}}">
{{- end}}
{{- if .Head.Canonical}}
<link rel="canonical" href="{{.Head.Canonical}}">
{{- end}}
{{- if .Head.Title}}
<meta property="og:title" content="{{.Head.Title}}">
{{- if .Head.Description}}
<meta property="og:description" content="{{.Head.Description}}">
{{- end}}
{{- if .Head.Canonical}}
<meta property="og:url" content="{{.Head.Canonical}}">
{{- end}}
{{- if .Head.Image.URL}}
<meta property="og:image" content="{{.Head.Image.URL}}">
{{- if gt .Head.Image.Width 0}}
<meta property="og:image:width" content="{{.Head.Image.Width}}">
<meta property="og:image:height" content="{{.Head.Image.Height}}">
{{- end}}
{{- end}}
<meta property="og:type" content="{{if .Head.Type}}{{.Head.Type}}{{else}}website{{end}}">
{{- if eq .Head.Type "Article"}}
{{- if gt .Head.Published.Year 1}}
<meta property="article:published_time" content="{{forge_rfc3339 .Head.Published}}">
{{- end}}
{{- if .Head.Author}}
<meta property="article:author" content="{{.Head.Author}}">
{{- end}}
{{- range .Head.Tags}}
<meta property="article:tag" content="{{.}}">
{{- end}}
{{- end}}
{{- if .Head.Social.Twitter.Card}}
<meta name="twitter:card" content="{{.Head.Social.Twitter.Card}}">
{{- else if or (eq .Head.Type "Article") (eq .Head.Type "Product") .Head.Image.URL}}
<meta name="twitter:card" content="summary_large_image">
{{- else}}
<meta name="twitter:card" content="summary">
{{- end}}
<meta name="twitter:title" content="{{.Head.Title}}">
{{- if .Head.Description}}
<meta name="twitter:description" content="{{.Head.Description}}">
{{- end}}
{{- if .Head.Image.URL}}
<meta name="twitter:image" content="{{.Head.Image.URL}}">
{{- end}}
{{- if .Head.Social.Twitter.Creator}}
<meta name="twitter:creator" content="{{.Head.Social.Twitter.Creator}}">
{{- end}}
{{- if .OGDefaults}}
{{- if .OGDefaults.TwitterSite}}
<meta name="twitter:site" content="{{.OGDefaults.TwitterSite}}">
{{- end}}
{{- end}}
{{- end}}
{{- if .AppSchema}}{{.AppSchema}}
{{- end}}
{{- if .HeadAssets}}
{{- range .HeadAssets.Preconnect}}<link rel="preconnect" href="{{.}}">
{{- end}}
{{- range .HeadAssets.Stylesheets}}<link rel="stylesheet" href="{{.}}">
{{- end}}
{{- range .HeadAssets.Favicons}}<link rel="{{.Rel}}"{{if .Type}} type="{{.Type}}"{{end}}{{if .Sizes}} sizes="{{.Sizes}}"{{end}} href="{{.Href}}">
{{- end}}
{{- range .HeadAssets.Scripts}}
{{- if .Src}}<script src="{{.Src}}"{{if .Async}} async{{end}}{{if .Defer}} defer{{end}}></script>
{{- else}}<script>{{.Body}}</script>{{end}}
{{- end}}
{{- end}}
{{- if .Head.NoIndex}}
<meta name="robots" content="noindex, nofollow">
{{- end}}
{{end}}`

// — Module template methods ————————————————————————————————————————————————

// setSiteName stores the site name (the hostname from [Config.BaseURL]) so it
// can be passed to [NewTemplateData] during HTML rendering.
// Called by [App.Content] as part of module wiring.
func (m *Module[T]) setSiteName(name string) {
	m.siteName = name
}

// setPartials stores the pre-loaded partial template sources so they are
// registered into each module template set during [Module.parseTemplates].
// Called by [App.Run] after [App.Partials] has loaded the partials directory.
func (m *Module[T]) setPartials(p []string) {
	m.partials = p
}

// loadPartials reads all *.html files from dir alphabetically and returns
// their raw contents as a slice. Returns an error if dir does not exist or
// any file cannot be read. Returns (nil, nil) when dir is the empty string.
func loadPartials(dir string) ([]string, error) {
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("forge: partials directory %q: %w", dir, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".html" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	srcs := make([]string, 0, len(names))
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("forge: partial %q: %w", name, err)
		}
		srcs = append(srcs, string(data))
	}
	return srcs, nil
}

// setSEODefaults stores the app-level OG defaults, AppSchema, and HeadAssets
// so they can be merged and rendered into [TemplateData] at HTML render time.
// Called by [App.Handler] after all [App.SEO] options have been applied.
func (m *Module[T]) setSEODefaults(d *OGDefaults, a *AppSchema, ha *HeadAssets) {
	m.ogDefaults = d
	m.appSchema = a
	m.headAssets = ha
}

// parseTemplates loads list.html and show.html from the module's template
// directory, registers the forge:head named partial and any shared partials
// (set via [setPartials]) in both template sets, and stores them
// thread-safely under tplMu.
//
// Called by [App.Run] before the server starts. Returns a descriptive error
// when a required file is absent or a template fails to parse. Returns nil
// immediately when no template directory is configured.
func (m *Module[T]) parseTemplates() error {
	if m.templateDir == "" {
		return nil
	}

	listPath := filepath.Join(m.templateDir, "list.html")
	showPath := filepath.Join(m.templateDir, "show.html")

	tplList, err := parseOneTemplate(listPath, m.templateRequired, m.partials)
	if err != nil {
		return fmt.Errorf("forge: templates for %s: %w", m.prefix, err)
	}

	tplShow, err := parseOneTemplate(showPath, m.templateRequired, m.partials)
	if err != nil {
		return fmt.Errorf("forge: templates for %s: %w", m.prefix, err)
	}

	m.tplMu.Lock()
	m.tplList = tplList
	m.tplShow = tplShow
	if tplList != nil || tplShow != nil {
		m.neg.html = true
	}
	m.tplMu.Unlock()

	return nil
}

// parseOneTemplate parses a single HTML template file, registers the
// forge:head sub-template, and then registers each shared partial in the
// returned template set. When partials is nil, no shared partials are added.
//
// When required is false and the file does not exist, (nil, nil) is returned.
// When required is true and the file does not exist, an error is returned.
func parseOneTemplate(path string, required bool, partials []string) (*template.Template, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if required {
			return nil, fmt.Errorf("file not found: %s", path)
		}
		return nil, nil
	}

	tpl, err := template.New(filepath.Base(path)).Funcs(TemplateFuncMap()).ParseFiles(path)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}

	// Register the forge:head partial into this template set so every
	// developer template can call {{template "forge:head" .}}.
	if _, err := tpl.Parse(forgeHeadTmpl); err != nil {
		return nil, fmt.Errorf("register forge:head in %s: %w", filepath.Base(path), err)
	}

	// Register each shared partial (loaded from the app-level partials
	// directory) so they are available inside list.html and show.html.
	for i, src := range partials {
		if _, err := tpl.Parse(src); err != nil {
			return nil, fmt.Errorf("register partial[%d] in %s: %w", i, filepath.Base(path), err)
		}
	}

	return tpl, nil
}

// renderListHTML renders tplList with a TemplateData[[]T] payload.
// If tplList is nil the request receives a 406 Not Acceptable response.
// Template execution errors produce a 500; the response buffer is flushed
// only on success so Content-Type is not written on error.
func (m *Module[T]) renderListHTML(w http.ResponseWriter, r *http.Request, ctx Context, items []T) {
	m.tplMu.RLock()
	tpl := m.tplList
	m.tplMu.RUnlock()

	if tpl == nil {
		WriteError(w, r, ErrNotAcceptable)
		return
	}

	data := NewTemplateData(ctx, items, Head{}, m.siteName)
	data.OGDefaults = m.ogDefaults
	data.AppSchema = renderAppSchema(m.appSchema)
	data.HeadAssets = m.headAssets
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		WriteError(w, r, fmt.Errorf("forge: list template execution: %w", err))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(buf.Bytes()) //nolint:errcheck
}

// renderShowHTML renders tplShow with a TemplateData[T] payload.
// Head is resolved via [resolveHead]: HeadFunc takes priority, then [Headable],
// then a zero Head. If tplShow is nil the request receives a 406 Not Acceptable response.
func (m *Module[T]) renderShowHTML(w http.ResponseWriter, r *http.Request, ctx Context, item T) {
	m.tplMu.RLock()
	tpl := m.tplShow
	m.tplMu.RUnlock()

	if tpl == nil {
		WriteError(w, r, ErrNotAcceptable)
		return
	}

	head := m.resolveHead(ctx, item)
	head = mergeOGDefaults(head, m.ogDefaults)
	data := NewTemplateData(ctx, item, head, m.siteName)
	data.OGDefaults = m.ogDefaults
	data.AppSchema = renderAppSchema(m.appSchema)
	data.HeadAssets = m.headAssets
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		WriteError(w, r, fmt.Errorf("forge: show template execution: %w", err))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(buf.Bytes()) //nolint:errcheck
}

// errorTemplate searches the module's template directory for
// errors/{status}.html and parses it on demand. Returns nil when the file
// is absent or the module has no template directory.
//
// Called lazily by the [errorTemplateLookup] closure set in [App.Handler].
func (m *Module[T]) errorTemplate(status int) *template.Template {
	if m.templateDir == "" {
		return nil
	}
	path := filepath.Join(m.templateDir, "errors", fmt.Sprintf("%d.html", status))
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	tpl, err := template.New(filepath.Base(path)).ParseFiles(path)
	if err != nil {
		return nil
	}
	return tpl
}

// bindErrorTemplates sets the package-level [errorTemplateLookup] closure.
// It iterates the given modules searching for errors/{status}.html in each
// module's template directory and returns the first match found.
//
// Called once by [App.Handler] when at least one module has a template
// directory registered.
func bindErrorTemplates(modules []templateParser) {
	setErrorTemplateLookup(func(status int) *template.Template {
		for _, tp := range modules {
			type errorTemplater interface {
				errorTemplate(int) *template.Template
			}
			if m, ok := tp.(errorTemplater); ok {
				if tpl := m.errorTemplate(status); tpl != nil {
					return tpl
				}
			}
		}
		return nil
	})
}
