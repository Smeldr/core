package main

import (
	"os"
	"reflect"
	"testing"
)

// — parseFrontmatter ——————————————————————————————————————————————————————————

func TestParseFrontmatter_bothSections(t *testing.T) {
	input := "---\nTitle: My Post\nSlug: my-post\n---\nMarkdown body here.\n"
	fields, body, err := parseFrontmatter(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fields["Title"] != "My Post" {
		t.Errorf("Title = %q, want %q", fields["Title"], "My Post")
	}
	if fields["Slug"] != "my-post" {
		t.Errorf("Slug = %q, want %q", fields["Slug"], "my-post")
	}
	if body != "Markdown body here.\n" {
		t.Errorf("body = %q, want %q", body, "Markdown body here.\n")
	}
}

func TestParseFrontmatter_headerOnly(t *testing.T) {
	input := "---\nTitle: My Post\n---\n"
	fields, body, err := parseFrontmatter(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fields["Title"] != "My Post" {
		t.Errorf("Title = %q, want %q", fields["Title"], "My Post")
	}
	if body != "" {
		t.Errorf("body = %q, want empty", body)
	}
}

func TestParseFrontmatter_arrayField(t *testing.T) {
	input := "---\nTags: [go, forge, cms]\n---\n"
	fields, _, err := parseFrontmatter(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"go", "forge", "cms"}
	got, ok := fields["Tags"].([]string)
	if !ok {
		t.Fatalf("Tags type = %T, want []string", fields["Tags"])
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tags = %v, want %v", got, want)
	}
}

func TestParseFrontmatter_emptyArray(t *testing.T) {
	input := "---\nTags: []\n---\n"
	fields, _, err := parseFrontmatter(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := fields["Tags"].([]string)
	if !ok {
		t.Fatalf("Tags type = %T, want []string", fields["Tags"])
	}
	if len(got) != 0 {
		t.Errorf("Tags = %v, want empty slice", got)
	}
}

func TestParseFrontmatter_noClosingDelimiter(t *testing.T) {
	input := "---\nTitle: Only Header\n"
	fields, body, err := parseFrontmatter(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fields["Title"] != "Only Header" {
		t.Errorf("Title = %q, want %q", fields["Title"], "Only Header")
	}
	if body != "" {
		t.Errorf("body = %q, want empty", body)
	}
}

func TestParseFrontmatter_noFrontmatter(t *testing.T) {
	_, _, err := parseFrontmatter("just plain text")
	if err == nil {
		t.Error("expected error for content without --- prefix")
	}
}

func TestParseFrontmatter_lowercaseKeys(t *testing.T) {
	input := "---\ntitle: low\nbody: text\n---\n"
	fields, _, err := parseFrontmatter(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fields["title"] != "low" {
		t.Errorf("title = %q, want %q", fields["title"], "low")
	}
}

func TestParseFrontmatter_colonInValue(t *testing.T) {
	input := "---\nURL: http://example.com\n---\n"
	fields, _, err := parseFrontmatter(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// strings.Cut on first ":" leaves "http://example.com" as value
	if fields["URL"] != "http://example.com" {
		t.Errorf("URL = %q, want %q", fields["URL"], "http://example.com")
	}
}

func TestParseFrontmatter_bodyWithLeadingNewline(t *testing.T) {
	input := "---\nTitle: Post\n---\n\nBody after blank line.\n"
	_, body, err := parseFrontmatter(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Newline after closing --- is stripped; remaining body starts with blank line
	if body == "" {
		t.Error("expected non-empty body")
	}
}

// — mergeFields ————————————————————————————————————————————————————————————————

func TestMergeFields_caseInsensitiveOverride(t *testing.T) {
	dst := map[string]any{"Title": "Old", "Body": "Keep"}
	src := map[string]any{"title": "New"} // lowercase key
	mergeFields(dst, src)
	if dst["Title"] != "New" {
		t.Errorf("Title = %q, want %q", dst["Title"], "New")
	}
	if dst["Body"] != "Keep" {
		t.Errorf("Body = %q, want %q", dst["Body"], "Keep")
	}
	if _, exists := dst["title"]; exists {
		t.Error("dst must not gain a duplicate lowercase key")
	}
}

func TestMergeFields_newKey(t *testing.T) {
	dst := map[string]any{"Title": "Post"}
	src := map[string]any{"Excerpt": "Short summary"}
	mergeFields(dst, src)
	if dst["Excerpt"] != "Short summary" {
		t.Errorf("Excerpt = %q, want %q", dst["Excerpt"], "Short summary")
	}
}

// — loadEnvFile ————————————————————————————————————————————————————————————————

func TestLoadEnvFile_setsUnsetVars(t *testing.T) {
	f, err := os.CreateTemp("", "forge-cli-env-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString("TEST_FORGE_CLI_X=hello\n# comment line\nTEST_FORGE_CLI_Y=world\n")
	f.Close()

	os.Unsetenv("TEST_FORGE_CLI_X")
	os.Unsetenv("TEST_FORGE_CLI_Y")

	loadEnvFile(f.Name())

	if got := os.Getenv("TEST_FORGE_CLI_X"); got != "hello" {
		t.Errorf("TEST_FORGE_CLI_X = %q, want %q", got, "hello")
	}
	if got := os.Getenv("TEST_FORGE_CLI_Y"); got != "world" {
		t.Errorf("TEST_FORGE_CLI_Y = %q, want %q", got, "world")
	}
}

func TestLoadEnvFile_doesNotOverrideSet(t *testing.T) {
	f, err := os.CreateTemp("", "forge-cli-env-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString("TEST_FORGE_CLI_Z=from-file\n")
	f.Close()

	os.Setenv("TEST_FORGE_CLI_Z", "already-set")
	defer os.Unsetenv("TEST_FORGE_CLI_Z")

	loadEnvFile(f.Name())

	if got := os.Getenv("TEST_FORGE_CLI_Z"); got != "already-set" {
		t.Errorf("TEST_FORGE_CLI_Z = %q, want %q", got, "already-set")
	}
}

func TestLoadEnvFile_missingFile(t *testing.T) {
	// Must not panic or error when the file does not exist.
	loadEnvFile("__nonexistent_forge_cli_env__")
}
