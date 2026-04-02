package forge

// — OGDefaults ————————————————————————————————————————————————————————————

// OGDefaults sets app-level Open Graph and Twitter Card fallback values.
// Apply via [App.SEO]; values are merged into every page's [Head] by
// forge:head when the content item does not supply its own.
//
//   - Image — fallback og:image when [Head.Image].URL is empty.
//   - TwitterSite — twitter:site handle (e.g. "@mycompany"); always
//     app-level, emitted on every page.
//   - TwitterCreator — fallback twitter:creator when
//     [Head.Social].Twitter.Creator is empty.
//
// Example:
//
//	app.SEO(&forge.OGDefaults{
//	    Image:          forge.Image{URL: "https://example.com/og.png", Width: 1200, Height: 630},
//	    TwitterSite:    "@mycompany",
//	    TwitterCreator: "@editor",
//	})
type OGDefaults struct {
	// Image is the fallback og:image used when a content item's Head.Image.URL
	// is empty. Width and Height are recommended for optimal Twitter Card display.
	Image Image

	// TwitterSite is the twitter:site handle for the site (e.g. "@mycompany").
	// Always emitted on every page; not overridable per item.
	TwitterSite string

	// TwitterCreator is the fallback twitter:creator handle used when the
	// content item's Head.Social.Twitter.Creator is empty.
	TwitterCreator string
}

// applySEO stores d in the app-level SEO state. Satisfies [SEOOption].
func (d *OGDefaults) applySEO(s *seoState) { s.ogDefaults = d }

// mergeOGDefaults returns a copy of head with fallback values from d applied:
//   - Head.Image is replaced when its URL is empty and d.Image.URL is set.
//   - Head.Social.Twitter.Creator is replaced when empty and d.TwitterCreator is set.
//
// d may be nil; in that case head is returned unchanged.
func mergeOGDefaults(head Head, d *OGDefaults) Head {
	if d == nil {
		return head
	}
	if head.Image.URL == "" && d.Image.URL != "" {
		head.Image = d.Image
	}
	if head.Social.Twitter.Creator == "" && d.TwitterCreator != "" {
		head.Social.Twitter.Creator = d.TwitterCreator
	}
	return head
}

// — SocialFeature —————————————————————————————————————————————————————————

// SocialFeature selects which social sharing meta tags forge:head emits for
// a module. Use the predefined constants [OpenGraph] and [TwitterCard].
type SocialFeature int

const (
	// OpenGraph enables Open Graph meta tags (og:title, og:description,
	// og:image, og:type, og:url, and article:* for Article content).
	OpenGraph SocialFeature = 1

	// TwitterCard enables Twitter Card meta tags (twitter:card, twitter:title,
	// twitter:description, twitter:image, twitter:creator).
	TwitterCard SocialFeature = 2
)

// socialOption carries the SocialFeature flags for a module.
// Created by [Social]; applied in NewModule.
type socialOption struct{ features []SocialFeature }

func (socialOption) isOption() {}

// Social returns an [Option] that documents which social sharing tag sets a
// module emits. The forge:head partial always renders Open Graph and Twitter
// Card tags when [Head.Title] is non-empty — Social() is declarative metadata
// that makes intent explicit at the call site.
//
//	app.Content(&BlogPost{},
//	    forge.At("/posts"),
//	    forge.Social(forge.OpenGraph, forge.TwitterCard),
//	)
//
// To customise per-item Twitter output, set [Head.Social] on the content type's
// Head() method:
//
//	func (p *BlogPost) Head() forge.Head {
//	    return forge.Head{
//	        // ...
//	        Social: forge.SocialOverrides{
//	            Twitter: forge.TwitterMeta{
//	                Card:    forge.SummaryLargeImage,
//	                Creator: "@alice",
//	            },
//	        },
//	    }
//	}
func Social(features ...SocialFeature) Option { return socialOption{features: features} }
