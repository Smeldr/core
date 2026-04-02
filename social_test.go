package forge

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
	"time"
)

// execForgeHead parses and executes the forge:head partial with h as data.
// The TemplateFuncMap is registered so forge_rfc3339 and other helpers resolve.
func execForgeHead(t *testing.T, h Head) string {
	t.Helper()
	tmpl, err := template.New("").Funcs(TemplateFuncMap()).Parse(forgeHeadTmpl)
	if err != nil {
		t.Fatalf("parse forgeHeadTmpl: %v", err)
	}
	data := TemplateData[any]{Head: h}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "forge:head", data); err != nil {
		t.Fatalf("execute forge:head: %v", err)
	}
	return buf.String()
}

func TestSocialOption(t *testing.T) {
	cases := []struct {
		name     string
		features []SocialFeature
	}{
		{"open_graph", []SocialFeature{OpenGraph}},
		{"twitter_card", []SocialFeature{TwitterCard}},
		{"both", []SocialFeature{OpenGraph, TwitterCard}},
		{"empty", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opt := Social(tc.features...)
			if _, ok := opt.(socialOption); !ok {
				t.Fatalf("Social() returned %T, want socialOption", opt)
			}
		})
	}
}

func TestForgeHeadOGRendering(t *testing.T) {
	h := Head{
		Title:       "Hello World",
		Description: "A great post.",
		Canonical:   "https://example.com/posts/hello-world",
		Image: Image{
			URL:    "https://example.com/img/cover.jpg",
			Width:  1200,
			Height: 630,
		},
	}
	out := execForgeHead(t, h)

	cases := []struct {
		label string
		want  string
	}{
		{"og:title", `property="og:title" content="Hello World"`},
		{"og:description", `property="og:description" content="A great post."`},
		{"og:url", `property="og:url" content="https://example.com/posts/hello-world"`},
		{"og:image", `property="og:image" content="https://example.com/img/cover.jpg"`},
		{"og:image:width", `property="og:image:width" content="1200"`},
		{"og:image:height", `property="og:image:height" content="630"`},
		{"twitter:title", `name="twitter:title" content="Hello World"`},
		{"twitter:image", `name="twitter:image" content="https://example.com/img/cover.jpg"`},
		{"twitter:card default", `name="twitter:card" content="summary_large_image"`},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			if !strings.Contains(out, c.want) {
				t.Errorf("forge:head output missing %s\ngot:\n%s", c.want, out)
			}
		})
	}
}

func TestForgeHeadTwitterRendering(t *testing.T) {
	h := Head{
		Title: "Hello World",
		Social: SocialOverrides{
			Twitter: TwitterMeta{
				Card:    SummaryLargeImage,
				Creator: "@alice",
			},
		},
	}
	out := execForgeHead(t, h)

	cases := []struct {
		label string
		want  string
	}{
		{"explicit card", `name="twitter:card" content="summary_large_image"`},
		{"creator", `name="twitter:creator" content="@alice"`},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			if !strings.Contains(out, c.want) {
				t.Errorf("forge:head output missing %s\ngot:\n%s", c.want, out)
			}
		})
	}

	// No image → twitter:card falls back to "summary".
	t.Run("summary fallback no image", func(t *testing.T) {
		out2 := execForgeHead(t, Head{Title: "No Image"})
		if !strings.Contains(out2, `content="summary"`) {
			t.Errorf("expected twitter:card=summary when no image, got:\n%s", out2)
		}
	})
}

func TestForgeHeadArticleMeta(t *testing.T) {
	published := time.Date(2026, 1, 15, 9, 0, 0, 0, time.UTC)
	h := Head{
		Title:     "My Article",
		Author:    "Alice",
		Type:      Article,
		Published: published,
		Tags:      []string{"go", "cms"},
	}
	out := execForgeHead(t, h)

	cases := []struct {
		label string
		want  string
	}{
		{"article:author", `property="article:author" content="Alice"`},
		{"article:tag go", `property="article:tag" content="go"`},
		{"article:tag cms", `property="article:tag" content="cms"`},
		{"article:published_time", `property="article:published_time" content="2026-01-15`},
		{"og:type article", `property="og:type" content="Article"`},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			if !strings.Contains(out, c.want) {
				t.Errorf("forge:head output missing %s\ngot:\n%s", c.want, out)
			}
		})
	}
}

func TestForgeHeadNoOGWithoutTitle(t *testing.T) {
	out := execForgeHead(t, Head{})

	forbidden := []string{
		`property="og:title"`,
		`property="og:description"`,
		`name="twitter:title"`,
		`name="twitter:card"`,
	}
	for _, f := range forbidden {
		if strings.Contains(out, f) {
			t.Errorf("forge:head should not emit %q when Title is empty\ngot:\n%s", f, out)
		}
	}
}
