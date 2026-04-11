package forge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfig writes content to a temp file and returns the path.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "forge.config")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfigFile_comments(t *testing.T) {
	path := writeConfig(t, "# this is a comment\n# another comment\n")
	cfg, err := loadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != "" || cfg.HTTPS || cfg.NavMode != 0 {
		t.Errorf("expected zero Config from comment-only file, got %+v", cfg)
	}
}

func TestLoadConfigFile_emptyLines(t *testing.T) {
	path := writeConfig(t, "\n   \n\t\n")
	cfg, err := loadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != "" {
		t.Errorf("expected zero Config from blank file, got %+v", cfg)
	}
}

func TestLoadConfigFile_baseURL(t *testing.T) {
	path := writeConfig(t, "base_url = https://example.com\n")
	cfg, err := loadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != "https://example.com" {
		t.Errorf("BaseURL: want %q, got %q", "https://example.com", cfg.BaseURL)
	}
}

func TestLoadConfigFile_https_true(t *testing.T) {
	path := writeConfig(t, "https = true\n")
	cfg, err := loadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.HTTPS {
		t.Error("expected HTTPS = true")
	}
}

func TestLoadConfigFile_https_false(t *testing.T) {
	path := writeConfig(t, "https = false\n")
	cfg, err := loadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPS {
		t.Error("expected HTTPS = false")
	}
}

func TestLoadConfigFile_https_invalid(t *testing.T) {
	path := writeConfig(t, "https = yes\n")
	_, err := loadConfigFile(path)
	if err == nil {
		t.Fatal("expected error for invalid https value")
	}
	if want := `"yes"`; !strings.Contains(err.Error(), want) {
		t.Errorf("error should mention the invalid value %s; got: %s", want, err.Error())
	}
	if !strings.Contains(err.Error(), "line 1") {
		t.Errorf("error should mention line number; got: %s", err.Error())
	}
}

func TestLoadConfigFile_navMode_db(t *testing.T) {
	path := writeConfig(t, "nav_mode = db\n")
	cfg, err := loadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NavMode != NavModeDB {
		t.Errorf("expected NavModeDB, got %v", cfg.NavMode)
	}
}

func TestLoadConfigFile_navMode_code(t *testing.T) {
	path := writeConfig(t, "nav_mode = code\n")
	cfg, err := loadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NavMode != NavModeCode {
		t.Errorf("expected NavModeCode, got %v", cfg.NavMode)
	}
}

func TestLoadConfigFile_navMode_invalid(t *testing.T) {
	path := writeConfig(t, "nav_mode = auto\n")
	_, err := loadConfigFile(path)
	if err == nil {
		t.Fatal("expected error for invalid nav_mode value")
	}
	if !strings.Contains(err.Error(), `"auto"`) {
		t.Errorf("error should mention the invalid value; got: %s", err.Error())
	}
}

func TestLoadConfigFile_unknownKeys(t *testing.T) {
	path := writeConfig(t, "future_feature = on\nanother_unknown = value\n")
	cfg, err := loadConfigFile(path)
	if err != nil {
		t.Fatalf("unknown keys should be silently ignored, got error: %v", err)
	}
	if cfg.BaseURL != "" || cfg.HTTPS || cfg.NavMode != 0 {
		t.Errorf("unexpected non-zero Config from unknown keys: %+v", cfg)
	}
}

func TestLoadConfigFile_valueContainsEquals(t *testing.T) {
	// Value contains "=" — only the first "=" should be the separator.
	path := writeConfig(t, "base_url = https://example.com/path?a=1&b=2\n")
	cfg, err := loadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "https://example.com/path?a=1&b=2"
	if cfg.BaseURL != want {
		t.Errorf("BaseURL: want %q, got %q", want, cfg.BaseURL)
	}
}

func TestLoadConfigFile_secret_panics(t *testing.T) {
	path := writeConfig(t, "secret = supersecret\n")
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for 'secret' key in config file")
		}
	}()
	loadConfigFile(path) //nolint:errcheck
}

func TestLoadConfigFile_missingFile(t *testing.T) {
	cfg, err := loadConfigFile(filepath.Join(t.TempDir(), "nonexistent.config"))
	if err != nil {
		t.Fatalf("missing file should return nil error, got: %v", err)
	}
	if cfg.BaseURL != "" || cfg.HTTPS || cfg.NavMode != 0 {
		t.Errorf("missing file should return zero Config, got %+v", cfg)
	}
}

func TestLoadConfigFile_orgName(t *testing.T) {
	path := writeConfig(t, "org_name = Acme Corp\n")
	cfg, err := loadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AppSchema == nil {
		t.Fatal("expected AppSchema to be non-nil")
	}
	if cfg.AppSchema.Name != "Acme Corp" {
		t.Errorf("AppSchema.Name: want %q, got %q", "Acme Corp", cfg.AppSchema.Name)
	}
}

func TestLoadConfigFile_orgType(t *testing.T) {
	path := writeConfig(t, "org_type = Organization\n")
	cfg, err := loadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AppSchema == nil {
		t.Fatal("expected AppSchema to be non-nil")
	}
	if cfg.AppSchema.Type != "Organization" {
		t.Errorf("AppSchema.Type: want %q, got %q", "Organization", cfg.AppSchema.Type)
	}
}

func TestLoadConfigFile_twitterSite(t *testing.T) {
	path := writeConfig(t, "twitter_site = @mycompany\n")
	cfg, err := loadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OGDefaults == nil {
		t.Fatal("expected OGDefaults to be non-nil")
	}
	if cfg.OGDefaults.TwitterSite != "@mycompany" {
		t.Errorf("OGDefaults.TwitterSite: want %q, got %q", "@mycompany", cfg.OGDefaults.TwitterSite)
	}
}

func TestLoadConfigFile_ogImage(t *testing.T) {
	path := writeConfig(t, "og_image = /static/og.png\n")
	cfg, err := loadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OGDefaults == nil {
		t.Fatal("expected OGDefaults to be non-nil")
	}
	// Parser stores value as-is; resolution happens in Handler().
	if cfg.OGDefaults.Image.URL != "/static/og.png" {
		t.Errorf("OGDefaults.Image.URL: want %q, got %q", "/static/og.png", cfg.OGDefaults.Image.URL)
	}
}

func TestLoadConfigFile_multipleKeys(t *testing.T) {
	content := "base_url = https://example.com\nhttps = true\nnav_mode = db\norg_name = Acme\norg_type = Organization\ntwitter_site = @acme\nog_image = /og.png\n"
	path := writeConfig(t, content)
	cfg, err := loadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != "https://example.com" {
		t.Errorf("BaseURL: want %q, got %q", "https://example.com", cfg.BaseURL)
	}
	if !cfg.HTTPS {
		t.Error("expected HTTPS = true")
	}
	if cfg.NavMode != NavModeDB {
		t.Errorf("expected NavModeDB, got %v", cfg.NavMode)
	}
	if cfg.AppSchema == nil || cfg.AppSchema.Name != "Acme" || cfg.AppSchema.Type != "Organization" {
		t.Errorf("unexpected AppSchema: %+v", cfg.AppSchema)
	}
	if cfg.OGDefaults == nil || cfg.OGDefaults.TwitterSite != "@acme" || cfg.OGDefaults.Image.URL != "/og.png" {
		t.Errorf("unexpected OGDefaults: %+v", cfg.OGDefaults)
	}
}

func TestMergeFileConfig_goCodeWins(t *testing.T) {
	goCfg := Config{
		BaseURL:    "https://go-code.com",
		HTTPS:      true,
		NavMode:    NavModeCode,
		AppSchema:  &AppSchema{Name: "GoCode"},
		OGDefaults: &OGDefaults{TwitterSite: "@gocode"},
	}
	fileCfg := Config{
		BaseURL:    "https://file.com",
		HTTPS:      false, // can't override true → false (merge only sets from file when Go is false)
		NavMode:    NavModeDB,
		AppSchema:  &AppSchema{Name: "File"},
		OGDefaults: &OGDefaults{TwitterSite: "@file"},
	}
	result := mergeFileConfig(goCfg, fileCfg)
	if result.BaseURL != "https://go-code.com" {
		t.Errorf("BaseURL: Go code should win; got %q", result.BaseURL)
	}
	if !result.HTTPS {
		t.Error("HTTPS: Go code true should not be overridden by file false")
	}
	if result.NavMode != NavModeCode {
		t.Errorf("NavMode: Go code should win; got %v", result.NavMode)
	}
	if result.AppSchema.Name != "GoCode" {
		t.Errorf("AppSchema: Go code should win; got %q", result.AppSchema.Name)
	}
	if result.OGDefaults.TwitterSite != "@gocode" {
		t.Errorf("OGDefaults: Go code should win; got %q", result.OGDefaults.TwitterSite)
	}
}

func TestMergeFileConfig_fileApplied(t *testing.T) {
	goCfg := Config{} // all zero
	fileCfg := Config{
		BaseURL:    "https://file.com",
		HTTPS:      true,
		NavMode:    NavModeDB,
		AppSchema:  &AppSchema{Name: "File"},
		OGDefaults: &OGDefaults{TwitterSite: "@file"},
	}
	result := mergeFileConfig(goCfg, fileCfg)
	if result.BaseURL != "https://file.com" {
		t.Errorf("BaseURL: file should apply; got %q", result.BaseURL)
	}
	if !result.HTTPS {
		t.Error("HTTPS: file should apply")
	}
	if result.NavMode != NavModeDB {
		t.Errorf("NavMode: file should apply; got %v", result.NavMode)
	}
	if result.AppSchema == nil || result.AppSchema.Name != "File" {
		t.Errorf("AppSchema: file should apply; got %+v", result.AppSchema)
	}
	if result.OGDefaults == nil || result.OGDefaults.TwitterSite != "@file" {
		t.Errorf("OGDefaults: file should apply; got %+v", result.OGDefaults)
	}
}

func TestMergeFileConfig_navModeZeroValue(t *testing.T) {
	// Go code has zero NavMode (no nav); file sets NavModeCode — file wins.
	goCfg := Config{}
	fileCfg := Config{NavMode: NavModeCode}
	result := mergeFileConfig(goCfg, fileCfg)
	if result.NavMode != NavModeCode {
		t.Errorf("NavMode: file NavModeCode should apply to zero Go code; got %v", result.NavMode)
	}
}
