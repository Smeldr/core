package smeldr

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// — Test content types ————————————————————————————————————————————————————

type schemaArticle struct{ Node }

type schemaFAQ struct {
	Node
	entries []FAQEntry
}

func (s *schemaFAQ) FAQEntries() []FAQEntry { return s.entries }

type schemaHowTo struct {
	Node
	steps []HowToStep
}

func (s *schemaHowTo) HowToSteps() []HowToStep { return s.steps }

type schemaEvent struct {
	Node
	details EventDetails
}

func (s *schemaEvent) EventDetails() EventDetails { return s.details }

type schemaRecipe struct {
	Node
	details RecipeDetails
}

func (s *schemaRecipe) RecipeDetails() RecipeDetails { return s.details }

type schemaReview struct {
	Node
	details ReviewDetails
}

func (s *schemaReview) ReviewDetails() ReviewDetails { return s.details }

type schemaOrg struct {
	Node
	details OrganizationDetails
}

func (s *schemaOrg) OrganizationDetails() OrganizationDetails { return s.details }

// noProvider does not implement any provider interface.
type noProvider struct{ Node }

// — Helpers ———————————————————————————————————————————————————————————————

// mustJSON validates that s is a non-empty string containing valid JSON.
func mustJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	if s == "" {
		t.Fatal("expected non-empty JSON string")
	}
	// Extract JSON from the script tag.
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}") + 1
	if start < 0 || end <= start {
		t.Fatalf("no JSON object found in: %s", s)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s[start:end]), &m); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, s)
	}
	return m
}

// — Tests —————————————————————————————————————————————————————————————————

func TestSchemaFor_EmptyType(t *testing.T) {
	got := SchemaFor(Head{}, nil)
	if got != "" {
		t.Errorf("SchemaFor with empty Type = %q; want empty string", got)
	}
}

func TestSchemaFor_UnknownType(t *testing.T) {
	got := SchemaFor(Head{Type: "NonExistent"}, nil)
	if got != "" {
		t.Errorf("SchemaFor with unknown Type = %q; want empty string", got)
	}
}

func TestSchemaFor_Article(t *testing.T) {
	pub := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	head := Head{
		Type:        Article,
		Title:       "Hello World",
		Description: "A test article.",
		Author:      "Alice",
		Published:   pub,
		Canonical:   "/posts/hello-world",
		Image:       Image{URL: "/img/cover.jpg", Width: 1200, Height: 630},
	}
	got := SchemaFor(head, &schemaArticle{})
	m := mustJSON(t, got)

	if m["@type"] != "Article" {
		t.Errorf("@type = %v; want Article", m["@type"])
	}
	if m["headline"] != "Hello World" {
		t.Errorf("headline = %v; want Hello World", m["headline"])
	}
	author, _ := m["author"].(map[string]any)
	if author["name"] != "Alice" {
		t.Errorf("author.name = %v; want Alice", author["name"])
	}
	if m["datePublished"] != "2026-01-01T00:00:00Z" {
		t.Errorf("datePublished = %v; want 2026-01-01T00:00:00Z", m["datePublished"])
	}
	if !strings.HasPrefix(got, `<script type="application/ld+json">`) {
		t.Error("output should start with <script> tag")
	}
}

func TestSchemaFor_Product(t *testing.T) {
	head := Head{
		Type:      Product,
		Title:     "Widget Pro",
		Canonical: "/products/widget-pro",
	}
	got := SchemaFor(head, nil)
	m := mustJSON(t, got)
	if m["@type"] != "Product" {
		t.Errorf("@type = %v; want Product", m["@type"])
	}
	if m["name"] != "Widget Pro" {
		t.Errorf("name = %v; want Widget Pro", m["name"])
	}
}

func TestSchemaFor_FAQPage(t *testing.T) {
	content := &schemaFAQ{entries: []FAQEntry{
		{Question: "What is Forge?", Answer: "A Go CMS framework."},
		{Question: "Is it fast?", Answer: "Yes, zero dependencies."},
	}}
	head := Head{Type: FAQPage, Title: "FAQ"}
	got := SchemaFor(head, content)
	if got == "" {
		t.Fatal("SchemaFor FAQPage returned empty string")
	}
	if !strings.Contains(got, "What is Forge?") {
		t.Error("FAQ question not found in output")
	}
	if !strings.Contains(got, "A Go CMS framework.") {
		t.Error("FAQ answer not found in output")
	}
	m := mustJSON(t, got)
	if m["@type"] != "FAQPage" {
		t.Errorf("@type = %v; want FAQPage", m["@type"])
	}
	me, _ := m["mainEntity"].([]any)
	if len(me) != 2 {
		t.Errorf("mainEntity len = %d; want 2", len(me))
	}
}

func TestSchemaFor_MissingProvider(t *testing.T) {
	tests := []struct {
		name    string
		head    Head
		content any
	}{
		{"FAQPage no provider", Head{Type: FAQPage}, &noProvider{}},
		{"HowTo no provider", Head{Type: HowTo}, &noProvider{}},
		{"Event no provider", Head{Type: Event}, &noProvider{}},
		{"Recipe no provider", Head{Type: Recipe}, &noProvider{}},
		{"Review no provider", Head{Type: Review}, &noProvider{}},
		{"Organization no provider", Head{Type: Organization}, &noProvider{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SchemaFor(tt.head, tt.content)
			if got != "" {
				t.Errorf("SchemaFor = %q; want empty string when provider is missing", got)
			}
		})
	}
}

func TestSchemaFor_HowTo(t *testing.T) {
	content := &schemaHowTo{steps: []HowToStep{
		{Name: "Step 1", Text: "Mix ingredients."},
		{Name: "Step 2", Text: "Bake at 180°C."},
	}}
	head := Head{Type: HowTo, Title: "Bake Bread"}
	got := SchemaFor(head, content)
	m := mustJSON(t, got)
	if m["@type"] != "HowTo" {
		t.Errorf("@type = %v; want HowTo", m["@type"])
	}
	steps, _ := m["step"].([]any)
	if len(steps) != 2 {
		t.Errorf("step len = %d; want 2", len(steps))
	}
}

func TestSchemaFor_Event(t *testing.T) {
	start := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 1, 18, 0, 0, 0, time.UTC)
	content := &schemaEvent{details: EventDetails{
		StartDate: start, EndDate: end,
		Location: "Conference Hall", Address: "123 Main St",
	}}
	head := Head{Type: Event, Title: "Go Conference"}
	got := SchemaFor(head, content)
	m := mustJSON(t, got)
	if m["@type"] != "Event" {
		t.Errorf("@type = %v; want Event", m["@type"])
	}
	if m["startDate"] != "2026-06-01T10:00:00Z" {
		t.Errorf("startDate = %v; want 2026-06-01T10:00:00Z", m["startDate"])
	}
}

func TestSchemaFor_Recipe(t *testing.T) {
	content := &schemaRecipe{details: RecipeDetails{
		Ingredients: []string{"flour", "water", "salt"},
		Steps:       []HowToStep{{Name: "Mix", Text: "Mix all ingredients."}},
	}}
	head := Head{Type: Recipe, Title: "Simple Bread", Author: "Baker"}
	got := SchemaFor(head, content)
	m := mustJSON(t, got)
	if m["@type"] != "Recipe" {
		t.Errorf("@type = %v; want Recipe", m["@type"])
	}
	ingredients, _ := m["recipeIngredient"].([]any)
	if len(ingredients) != 3 {
		t.Errorf("recipeIngredient len = %d; want 3", len(ingredients))
	}
}

func TestSchemaFor_Review(t *testing.T) {
	content := &schemaReview{details: ReviewDetails{
		Body: "Excellent product.", Rating: 4.5, BestRating: 5, WorstRating: 1,
	}}
	head := Head{Type: Review, Title: "Widget Review", Author: "Reviewer"}
	got := SchemaFor(head, content)
	m := mustJSON(t, got)
	if m["@type"] != "Review" {
		t.Errorf("@type = %v; want Review", m["@type"])
	}
	rating, _ := m["reviewRating"].(map[string]any)
	if rating["ratingValue"] != 4.5 {
		t.Errorf("ratingValue = %v; want 4.5", rating["ratingValue"])
	}
}

func TestSchemaFor_Organization(t *testing.T) {
	content := &schemaOrg{details: OrganizationDetails{
		Name: "Acme Corp", URL: "https://acme.com", Description: "We make widgets.",
		Logo: Image{URL: "/logo.png", Width: 200, Height: 60},
	}}
	head := Head{Type: Organization}
	got := SchemaFor(head, content)
	m := mustJSON(t, got)
	if m["@type"] != "Organization" {
		t.Errorf("@type = %v; want Organization", m["@type"])
	}
	if m["name"] != "Acme Corp" {
		t.Errorf("name = %v; want Acme Corp", m["name"])
	}
}

func TestSchemaFor_BreadcrumbList_appended(t *testing.T) {
	head := Head{
		Type:  Article,
		Title: "Post",
		Breadcrumbs: Crumbs(
			Crumb("Home", "/"),
			Crumb("Posts", "/posts"),
			Crumb("Post", "/posts/post"),
		),
	}
	got := SchemaFor(head, &schemaArticle{})
	if !strings.Contains(got, "BreadcrumbList") {
		t.Error("BreadcrumbList not found in output")
	}
	// Two <script> blocks separated by \n
	count := strings.Count(got, `<script type="application/ld+json">`)
	if count != 2 {
		t.Errorf("script block count = %d; want 2", count)
	}
	// Verify positions are 1-based
	if !strings.Contains(got, `"position":1`) {
		t.Error("position 1 not found in BreadcrumbList")
	}
	if !strings.Contains(got, `"position":3`) {
		t.Error("position 3 not found in BreadcrumbList")
	}
}

func TestSchemaFor_BreadcrumbList_omitted(t *testing.T) {
	head := Head{Type: Article, Title: "Post"}
	got := SchemaFor(head, &schemaArticle{})
	if strings.Contains(got, "BreadcrumbList") {
		t.Error("BreadcrumbList should not appear when Breadcrumbs is empty")
	}
	count := strings.Count(got, `<script type="application/ld+json">`)
	if count != 1 {
		t.Errorf("script block count = %d; want 1", count)
	}
}

// — Benchmark —————————————————————————————————————————————————————————————

func BenchmarkSchemaFor_Article(b *testing.B) {
	head := Head{
		Type:        Article,
		Title:       "Benchmark Article",
		Description: "Testing SchemaFor performance.",
		Author:      "Benchmarker",
		Published:   time.Now(),
		Canonical:   "/posts/benchmark",
		Image:       Image{URL: "/img/bench.jpg", Width: 1200, Height: 630},
		Breadcrumbs: Crumbs(Crumb("Home", "/"), Crumb("Posts", "/posts")),
	}
	content := &schemaArticle{}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		SchemaFor(head, content)
	}
}

func TestRenderAppSchema_nilOrEmpty(t *testing.T) {
	if got := renderAppSchema(nil); got != "" {
		t.Errorf("renderAppSchema(nil) = %q, want empty", got)
	}
	if got := renderAppSchema(&AppSchema{}); got != "" {
		t.Errorf("renderAppSchema(&AppSchema{}) = %q, want empty (Type is empty)", got)
	}
}

func TestRenderAppSchema_noLogo(t *testing.T) {
	got := renderAppSchema(&AppSchema{Type: "Organization", Name: "Acme", URL: "https://acme.com"})
	if got == "" {
		t.Fatal("renderAppSchema returned empty string")
	}
	s := string(got)
	if !strings.Contains(s, "Organization") {
		t.Errorf("output missing \"Organization\": %s", s)
	}
	if !strings.Contains(s, "Acme") {
		t.Errorf("output missing \"Acme\": %s", s)
	}
	if strings.Contains(s, "ImageObject") {
		t.Errorf("output should not contain \"ImageObject\" when Logo is empty: %s", s)
	}
}

func TestRenderAppSchema_withLogo(t *testing.T) {
	got := renderAppSchema(&AppSchema{Type: "Organization", Name: "Acme", Logo: "https://acme.com/logo.png"})
	s := string(got)
	if !strings.Contains(s, "ImageObject") {
		t.Errorf("output missing \"ImageObject\": %s", s)
	}
	if !strings.Contains(s, "https://acme.com/logo.png") {
		t.Errorf("output missing logo URL: %s", s)
	}
}
