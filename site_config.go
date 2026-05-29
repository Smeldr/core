package smeldr

import "context"

// SiteConfig holds site-wide defaults configurable via MCP after first deploy.
// Register with [NewSiteConfigModule] so operators can update site-level settings
// without code changes or redeployment.
//
// The backing table must exist before the first request is served.
// Create it with [CreateSiteConfigTable], or run the DDL directly.
type SiteConfig struct {
	Node
	SiteName       string `db:"site_name"       json:"site_name"       smeldr_description:"Site name appended to all page titles. Empty = no suffix added."`
	TitleSeparator string `db:"title_separator" json:"title_separator" smeldr_description:"Separator between page title and site name, e.g. \" | \". Ignored when SiteName is empty. Default \" | \" applied at render time if empty."`
	OGImage        string `db:"og_image"        json:"og_image"        smeldr_description:"Relative URL of the global fallback OG image, e.g. /media/og.png. Used on pages without an explicit OG image."`
	XHandle        string `db:"x_handle"        json:"x_handle"        smeldr_description:"X (formerly Twitter) site handle, e.g. @smeldr. Rendered as twitter:site meta tag."`
	HeadScript     string `db:"head_script"     json:"head_script"     smeldr_description:"Raw script snippet injected verbatim into <head>. Use for analytics (Google Analytics, GoatCounter, Plausible, etc.) or any custom head content."`
}

// Head implements [Content] for the MCP and admin surface.
// SiteConfig is a configuration type — its Head is intentionally minimal.
func (s *SiteConfig) Head() Head {
	return Head{Title: "Site Configuration"}
}

// NewSiteConfigModule returns a [Module] for [SiteConfig] pre-configured as a
// [SingleInstance] backed by a [NewSQLRepo] on the smeldr_site_configs table.
//
// Call [CreateSiteConfigTable] once at startup before passing the module to
// [App.Content]:
//
//	if err := smeldr.CreateSiteConfigTable(db); err != nil {
//	    log.Fatal(err)
//	}
//	app.Content(smeldr.NewSiteConfigModule(db))
func NewSiteConfigModule(db DB) *Module[SiteConfig] {
	return NewModule(SiteConfig{},
		At("/site-config"),
		Repo(NewSQLRepo[SiteConfig](db, Table("smeldr_site_configs"))),
		SingleInstance(),
	)
}

// CreateSiteConfigTable creates the smeldr_site_configs table if it does not
// exist. Call once at application startup before [NewSiteConfigModule].
func CreateSiteConfigTable(db DB) error {
	_, err := db.ExecContext(context.Background(), `
CREATE TABLE IF NOT EXISTS smeldr_site_configs (
	id               TEXT NOT NULL PRIMARY KEY,
	slug             TEXT NOT NULL DEFAULT 'site-config',
	status           TEXT NOT NULL DEFAULT 'draft',
	created_at       DATETIME NOT NULL,
	updated_at       DATETIME NOT NULL,
	published_at     DATETIME,
	site_name        TEXT NOT NULL DEFAULT '',
	title_separator  TEXT NOT NULL DEFAULT '',
	og_image         TEXT NOT NULL DEFAULT '',
	x_handle         TEXT NOT NULL DEFAULT '',
	head_script      TEXT NOT NULL DEFAULT ''
)`)
	return err
}
