package smeldr

import (
	"encoding/json"
	"html/template"
	"strings"
	"time"
)

// schema.go provides Go types for Google-supported JSON-LD rich results.
// Use SchemaFor to generate a <script type="application/ld+json"> block
// from a smeldr.Head and an optional content value.

// — Provider interfaces ———————————————————————————————————————————————————

// FAQEntry is a single question-and-answer pair for FAQPage rich results.
type FAQEntry struct {
	Question string
	Answer   string
}

// FAQProvider is implemented by content types that supply FAQ structured data.
// Return a non-empty slice to enable FAQPage JSON-LD generation via SchemaFor.
type FAQProvider interface{ FAQEntries() []FAQEntry }

// HowToStep is a single step in a HowTo or Recipe structured data block.
type HowToStep struct {
	Name string // short label for the step
	Text string // full instruction text
}

// HowToProvider is implemented by content types that supply step-by-step
// structured data for HowTo rich results.
type HowToProvider interface{ HowToSteps() []HowToStep }

// EventDetails carries the extra fields required for Event rich results.
type EventDetails struct {
	StartDate time.Time
	EndDate   time.Time
	Location  string // venue name
	Address   string // street address or city
}

// EventProvider is implemented by content types that supply event structured data.
type EventProvider interface{ EventDetails() EventDetails }

// RecipeDetails carries the extra fields required for Recipe rich results.
type RecipeDetails struct {
	Ingredients []string
	Steps       []HowToStep
}

// RecipeProvider is implemented by content types that supply recipe structured data.
type RecipeProvider interface{ RecipeDetails() RecipeDetails }

// ReviewDetails carries the extra fields required for Review rich results.
type ReviewDetails struct {
	Body        string
	Rating      float64
	BestRating  float64
	WorstRating float64
}

// ReviewProvider is implemented by content types that supply review structured data.
type ReviewProvider interface{ ReviewDetails() ReviewDetails }

// OrganizationDetails carries the extra fields required for Organization rich results.
type OrganizationDetails struct {
	Name        string
	URL         string
	Description string
	Logo        Image
}

// OrganizationProvider is implemented by content types that supply organization
// structured data.
type OrganizationProvider interface{ OrganizationDetails() OrganizationDetails }

// — Internal JSON-LD types ————————————————————————————————————————————————

type ldNode struct {
	Context string `json:"@context"`
	Type    string `json:"@type"`
}

type ldPerson struct {
	Type string `json:"@type"`
	Name string `json:"name,omitempty"`
}

type ldImage struct {
	Type   string `json:"@type"`
	URL    string `json:"url,omitempty"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

type ldArticle struct {
	ldNode
	Headline      string   `json:"headline,omitempty"`
	Description   string   `json:"description,omitempty"`
	Author        ldPerson `json:"author,omitempty"`
	DatePublished string   `json:"datePublished,omitempty"`
	DateModified  string   `json:"dateModified,omitempty"`
	Image         *ldImage `json:"image,omitempty"`
	URL           string   `json:"url,omitempty"`
}

type ldProduct struct {
	ldNode
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	Image       *ldImage `json:"image,omitempty"`
	URL         string   `json:"url,omitempty"`
}

type ldAnswer struct {
	Type string `json:"@type"`
	Text string `json:"text"`
}

type ldFAQEntry struct {
	Type           string   `json:"@type"`
	Name           string   `json:"name"`
	AcceptedAnswer ldAnswer `json:"acceptedAnswer"`
}

type ldFAQPage struct {
	ldNode
	MainEntity []ldFAQEntry `json:"mainEntity"`
}

type ldHowToStep struct {
	Type string `json:"@type"`
	Name string `json:"name,omitempty"`
	Text string `json:"text"`
}

type ldHowTo struct {
	ldNode
	Name        string        `json:"name,omitempty"`
	Description string        `json:"description,omitempty"`
	Step        []ldHowToStep `json:"step"`
}

type ldPlace struct {
	Type    string `json:"@type"`
	Name    string `json:"name,omitempty"`
	Address string `json:"address,omitempty"`
}

type ldEvent struct {
	ldNode
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	StartDate   string   `json:"startDate,omitempty"`
	EndDate     string   `json:"endDate,omitempty"`
	Location    *ldPlace `json:"location,omitempty"`
	Image       *ldImage `json:"image,omitempty"`
	URL         string   `json:"url,omitempty"`
}

type ldRecipe struct {
	ldNode
	Name               string        `json:"name,omitempty"`
	Description        string        `json:"description,omitempty"`
	RecipeIngredient   []string      `json:"recipeIngredient,omitempty"`
	RecipeInstructions []ldHowToStep `json:"recipeInstructions,omitempty"`
	Author             ldPerson      `json:"author,omitempty"`
	Image              *ldImage      `json:"image,omitempty"`
}

type ldRating struct {
	Type        string  `json:"@type"`
	RatingValue float64 `json:"ratingValue"`
	BestRating  float64 `json:"bestRating,omitempty"`
	WorstRating float64 `json:"worstRating,omitempty"`
}

type ldReview struct {
	ldNode
	Name         string   `json:"name,omitempty"`
	ReviewBody   string   `json:"reviewBody,omitempty"`
	Author       ldPerson `json:"author,omitempty"`
	ReviewRating ldRating `json:"reviewRating,omitempty"`
}

type ldOrganization struct {
	ldNode
	Name        string   `json:"name,omitempty"`
	URL         string   `json:"url,omitempty"`
	Description string   `json:"description,omitempty"`
	Logo        *ldImage `json:"logo,omitempty"`
}

type ldBreadcrumbItem struct {
	Type     string `json:"@type"`
	Position int    `json:"position"`
	Name     string `json:"name"`
	ID       string `json:"@id"`
}

type ldBreadcrumbList struct {
	ldNode
	ItemListElement []ldBreadcrumbItem `json:"itemListElement"`
}

// — Helpers ———————————————————————————————————————————————————————————————

func toImage(img Image) *ldImage {
	if img.URL == "" {
		return nil
	}
	return &ldImage{Type: "ImageObject", URL: img.URL, Width: img.Width, Height: img.Height}
}

func toPerson(name string) ldPerson {
	return ldPerson{Type: "Person", Name: name}
}

func toRFC3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func toHowToSteps(steps []HowToStep) []ldHowToStep {
	out := make([]ldHowToStep, len(steps))
	for i, s := range steps {
		out[i] = ldHowToStep{Type: "HowToStep", Name: s.Name, Text: s.Text}
	}
	return out
}

func scriptBlock(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return `<script type="application/ld+json">` + string(b) + `</script>`
}

// — SchemaFor —————————————————————————————————————————————————————————————

// SchemaFor generates one or two <script type="application/ld+json"> blocks
// for the given head and content value.
//
// The primary block is determined by head.Type (Article, Product, FAQPage,
// HowTo, Event, Recipe, Review, Organization). An empty head.Type returns "".
// Unknown types return "". Types that require a provider interface (FAQPage,
// HowTo, Event, Recipe, Review, Organization) return "" when content does not
// implement the required interface.
//
// A second BreadcrumbList block is appended (separated by "\n") when
// head.Breadcrumbs is non-empty.
//
// SchemaFor never panics.
func SchemaFor(head Head, content any) string {
	var primary string

	switch head.Type {
	case Article:
		a := ldArticle{
			ldNode:        ldNode{Context: "https://schema.org", Type: "Article"},
			Headline:      head.Title,
			Description:   head.Description,
			Author:        toPerson(head.Author),
			DatePublished: toRFC3339(head.Published),
			DateModified:  toRFC3339(head.Modified),
			Image:         toImage(head.Image),
			URL:           head.Canonical,
		}
		primary = scriptBlock(a)

	case Product:
		p := ldProduct{
			ldNode:      ldNode{Context: "https://schema.org", Type: "Product"},
			Name:        head.Title,
			Description: head.Description,
			Image:       toImage(head.Image),
			URL:         head.Canonical,
		}
		primary = scriptBlock(p)

	case FAQPage:
		fp, ok := content.(FAQProvider)
		if !ok {
			return ""
		}
		entries := fp.FAQEntries()
		items := make([]ldFAQEntry, len(entries))
		for i, e := range entries {
			items[i] = ldFAQEntry{
				Type:           "Question",
				Name:           e.Question,
				AcceptedAnswer: ldAnswer{Type: "Answer", Text: e.Answer},
			}
		}
		primary = scriptBlock(ldFAQPage{
			ldNode:     ldNode{Context: "https://schema.org", Type: "FAQPage"},
			MainEntity: items,
		})

	case HowTo:
		hp, ok := content.(HowToProvider)
		if !ok {
			return ""
		}
		primary = scriptBlock(ldHowTo{
			ldNode:      ldNode{Context: "https://schema.org", Type: "HowTo"},
			Name:        head.Title,
			Description: head.Description,
			Step:        toHowToSteps(hp.HowToSteps()),
		})

	case Event:
		ep, ok := content.(EventProvider)
		if !ok {
			return ""
		}
		d := ep.EventDetails()
		var place *ldPlace
		if d.Location != "" || d.Address != "" {
			place = &ldPlace{Type: "Place", Name: d.Location, Address: d.Address}
		}
		primary = scriptBlock(ldEvent{
			ldNode:      ldNode{Context: "https://schema.org", Type: "Event"},
			Name:        head.Title,
			Description: head.Description,
			StartDate:   toRFC3339(d.StartDate),
			EndDate:     toRFC3339(d.EndDate),
			Location:    place,
			Image:       toImage(head.Image),
			URL:         head.Canonical,
		})

	case Recipe:
		rp, ok := content.(RecipeProvider)
		if !ok {
			return ""
		}
		d := rp.RecipeDetails()
		primary = scriptBlock(ldRecipe{
			ldNode:             ldNode{Context: "https://schema.org", Type: "Recipe"},
			Name:               head.Title,
			Description:        head.Description,
			RecipeIngredient:   d.Ingredients,
			RecipeInstructions: toHowToSteps(d.Steps),
			Author:             toPerson(head.Author),
			Image:              toImage(head.Image),
		})

	case Review:
		rp, ok := content.(ReviewProvider)
		if !ok {
			return ""
		}
		d := rp.ReviewDetails()
		primary = scriptBlock(ldReview{
			ldNode:     ldNode{Context: "https://schema.org", Type: "Review"},
			Name:       head.Title,
			ReviewBody: d.Body,
			Author:     toPerson(head.Author),
			ReviewRating: ldRating{
				Type:        "Rating",
				RatingValue: d.Rating,
				BestRating:  d.BestRating,
				WorstRating: d.WorstRating,
			},
		})

	case Organization:
		op, ok := content.(OrganizationProvider)
		if !ok {
			return ""
		}
		d := op.OrganizationDetails()
		primary = scriptBlock(ldOrganization{
			ldNode:      ldNode{Context: "https://schema.org", Type: "Organization"},
			Name:        d.Name,
			URL:         d.URL,
			Description: d.Description,
			Logo:        toImage(d.Logo),
		})

	default:
		return ""
	}

	if primary == "" {
		return ""
	}

	if len(head.Breadcrumbs) == 0 {
		return primary
	}

	items := make([]ldBreadcrumbItem, len(head.Breadcrumbs))
	for i, b := range head.Breadcrumbs {
		items[i] = ldBreadcrumbItem{
			Type:     "ListItem",
			Position: i + 1,
			Name:     b.Label,
			ID:       b.URL,
		}
	}
	crumbScript := scriptBlock(ldBreadcrumbList{
		ldNode:          ldNode{Context: "https://schema.org", Type: "BreadcrumbList"},
		ItemListElement: items,
	})

	var sb strings.Builder
	sb.WriteString(primary)
	sb.WriteByte('\n')
	sb.WriteString(crumbScript)
	return sb.String()
}

// — AppSchema —————————————————————————————————————————————————————————————

// AppSchema registers app-level JSON-LD structured data emitted in every
// page's <head> by forge:head. Use it to declare site-wide Organisation or
// WebSite metadata once rather than per content type.
//
// Apply via [App.SEO]:
//
//	app.SEO(&smeldr.AppSchema{
//	    Type: "Organization",
//	    Name: "Acme Corp",
//	    URL:  "https://acme.com",
//	    Logo: "https://acme.com/logo.png",
//	})
type AppSchema struct {
	// Type is the JSON-LD @type, e.g. "Organization" or "WebSite".
	Type string

	// Name is the human-readable name of the organisation or site.
	Name string

	// URL is the canonical URL of the organisation or site's home page.
	URL string

	// Logo is the absolute URL of the organisation's logo image.
	Logo string
}

// applySEO stores a in the app-level SEO state. Satisfies [SEOOption].
func (a *AppSchema) applySEO(s *seoState) { s.appSchema = a }

// renderAppSchema returns a pre-rendered, HTML-safe
// <script type="application/ld+json"> block for a. Returns an empty
// template.HTML when a is nil or a.Type is empty.
func renderAppSchema(a *AppSchema) template.HTML {
	if a == nil || a.Type == "" {
		return ""
	}
	type ldAppOrg struct {
		Context string   `json:"@context"`
		Type    string   `json:"@type"`
		Name    string   `json:"name,omitempty"`
		URL     string   `json:"url,omitempty"`
		Logo    *ldImage `json:"logo,omitempty"`
	}
	var logo *ldImage
	if a.Logo != "" {
		logo = &ldImage{Type: "ImageObject", URL: a.Logo}
	}
	block := scriptBlock(ldAppOrg{
		Context: "https://schema.org",
		Type:    a.Type,
		Name:    a.Name,
		URL:     a.URL,
		Logo:    logo,
	})
	return template.HTML(block) //nolint:gosec
}
