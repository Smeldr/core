package smeldr

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfig writes content to a temp file and returns the path.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "smeldr.config")
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

// TestLoadConfigFile_unknownKeyRefused: an unknown key is an error naming
// its line and the key, and listing the accepted keys (D178).
func TestLoadConfigFile_unknownKeyRefused(t *testing.T) {
	path := writeConfig(t, "# comment\nbase_url = https://example.com\nbase_ur = https://typo.example\n")
	_, err := loadConfigFile(path)
	if err == nil {
		t.Fatal("an unknown key was accepted")
	}
	for _, want := range []string{"line 3", `unknown key "base_ur"`, "accepted: base_url, https, nav_mode"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

// TestLoadConfigFile_everyAcceptedKeyParses: each key the unknown-key
// refusal lists is one the parser accepts, so the list and the switch agree.
func TestLoadConfigFile_everyAcceptedKeyParses(t *testing.T) {
	valid := map[string]string{"https": "true", "nav_mode": "db", "media_max_size": "10", "dev": "true"}
	for _, key := range configFileKeys {
		value, ok := valid[key]
		if !ok {
			value = "x"
		}
		if _, err := loadConfigFile(writeConfig(t, key+" = "+value+"\n")); err != nil {
			t.Errorf("%s: %v", key, err)
		}
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

// TestLoadConfigFile_secretRefused: the key "secret" is an error naming its
// line, never a panic, and the error does not contain the value (D178).
func TestLoadConfigFile_secretRefused(t *testing.T) {
	path := writeConfig(t, "base_url = https://example.com\nsecret = supersecret\n")
	_, err := loadConfigFile(path)
	if err == nil {
		t.Fatal("the secret key was accepted")
	}
	if !strings.Contains(err.Error(), "line 2") || !strings.Contains(err.Error(), "SECRET environment variable") {
		t.Errorf("error = %q", err)
	}
	if strings.Contains(err.Error(), "supersecret") {
		t.Errorf("error shows the secret: %q", err)
	}
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

func TestMergeFileConfig_ogImageOverridesGoCode(t *testing.T) {
	// File og_image must override Go-code Image.URL.
	goCfg := Config{OGDefaults: &OGDefaults{Image: Image{URL: "https://example.com/old.png"}}}
	fileCfg := Config{OGDefaults: &OGDefaults{Image: Image{URL: "/new.png"}}}
	result := mergeFileConfig(goCfg, fileCfg)
	if result.OGDefaults == nil {
		t.Fatal("expected OGDefaults to be non-nil")
	}
	if result.OGDefaults.Image.URL != "/new.png" {
		t.Errorf("og_image: file should override Go code; got %q", result.OGDefaults.Image.URL)
	}
}

func TestMergeFileConfig_ogImagePreservesOtherFields(t *testing.T) {
	// File og_image overrides Image.URL but leaves TwitterSite intact.
	goCfg := Config{OGDefaults: &OGDefaults{
		Image:       Image{URL: "https://example.com/old.png"},
		TwitterSite: "@gocode",
	}}
	fileCfg := Config{OGDefaults: &OGDefaults{Image: Image{URL: "/new.png"}}}
	result := mergeFileConfig(goCfg, fileCfg)
	if result.OGDefaults.Image.URL != "/new.png" {
		t.Errorf("Image.URL: file should win; got %q", result.OGDefaults.Image.URL)
	}
	if result.OGDefaults.TwitterSite != "@gocode" {
		t.Errorf("TwitterSite: Go-code value should be preserved; got %q", result.OGDefaults.TwitterSite)
	}
}

func TestMergeFileConfig_ogImageAbsentGoCodePreserved(t *testing.T) {
	// When file has no og_image, Go-code Image.URL is preserved unchanged.
	goCfg := Config{OGDefaults: &OGDefaults{Image: Image{URL: "https://example.com/go.png"}}}
	fileCfg := Config{OGDefaults: &OGDefaults{TwitterSite: "@file"}} // og_image absent
	result := mergeFileConfig(goCfg, fileCfg)
	if result.OGDefaults.Image.URL != "https://example.com/go.png" {
		t.Errorf("Image.URL: Go-code value should be preserved when og_image absent; got %q", result.OGDefaults.Image.URL)
	}
}

func TestMergeFileConfig_ogImageGoCodeNilFileApplied(t *testing.T) {
	// Regression guard: when Go code has no OGDefaults, file config is used wholesale.
	goCfg := Config{}
	fileCfg := Config{OGDefaults: &OGDefaults{Image: Image{URL: "/from-file.png"}, TwitterSite: "@file"}}
	result := mergeFileConfig(goCfg, fileCfg)
	if result.OGDefaults == nil {
		t.Fatal("expected OGDefaults from file when Go code has none")
	}
	if result.OGDefaults.Image.URL != "/from-file.png" {
		t.Errorf("Image.URL: file should apply; got %q", result.OGDefaults.Image.URL)
	}
	if result.OGDefaults.TwitterSite != "@file" {
		t.Errorf("TwitterSite: file should apply; got %q", result.OGDefaults.TwitterSite)
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

func TestLoadConfigFile_mediaPath(t *testing.T) {
	path := writeConfig(t, "media_path = ./uploads\n")
	cfg, err := loadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MediaPath != "./uploads" {
		t.Errorf("MediaPath: want %q, got %q", "./uploads", cfg.MediaPath)
	}
}

func TestLoadConfigFile_mediaMaxSize(t *testing.T) {
	path := writeConfig(t, "media_max_size = 10485760\n")
	cfg, err := loadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MediaMaxSize != 10485760 {
		t.Errorf("MediaMaxSize: want 10485760, got %d", cfg.MediaMaxSize)
	}
}

func TestLoadConfigFile_mediaMaxSize_invalid(t *testing.T) {
	path := writeConfig(t, "media_max_size = notanumber\n")
	_, err := loadConfigFile(path)
	if err == nil {
		t.Fatal("expected error for invalid media_max_size, got nil")
	}
	if !strings.Contains(err.Error(), "media_max_size") {
		t.Errorf("error should mention key name, got: %v", err)
	}
}

// TestLoadConfigFile_noEqualsLine: a line that is not "key = value" is an
// error naming its line (D178).
func TestLoadConfigFile_noEqualsLine(t *testing.T) {
	path := writeConfig(t, "base_url = https://example.com\nbase_url https://typo.example\n")
	_, err := loadConfigFile(path)
	if err == nil || !strings.Contains(err.Error(), `line 2: want "key = value"`) {
		t.Fatalf("err = %v, want line 2 refused", err)
	}
}

// TestLoadConfigFile_unreadable: a path that cannot be read as a file is an
// error, unlike a missing one.
func TestLoadConfigFile_unreadable(t *testing.T) {
	_, err := loadConfigFile(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "cannot read") {
		t.Fatalf("err = %v, want cannot read", err)
	}
}

// TestValidateConfig_defaultFile: without SMELDR_CONFIG, an error in
// ./smeldr.config refuses as smeldr_config (D178), while a missing default
// file is skipped.
func TestValidateConfig_defaultFile(t *testing.T) {
	t.Setenv("SMELDR_CONFIG", "")
	ok := Config{BaseURL: "https://example.com", Secret: []byte("0123456789abcdef")}
	t.Chdir(t.TempDir())
	if _, err := ValidateConfig(ok); err != nil {
		t.Fatalf("missing default file: %v", err)
	}
	if err := os.WriteFile("smeldr.config", []byte("nav_mod = db\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ValidateConfig(ok)
	var ve *ValidationError
	if !errors.As(err, &ve) || len(ve.Fields()) != 1 || ve.Fields()[0].Field != "smeldr_config" {
		t.Fatalf("err = %v, want a ValidationError on smeldr_config", err)
	}
	if !strings.Contains(err.Error(), `unknown key "nav_mod"`) {
		t.Errorf("err = %v", err)
	}
}

func TestLoadConfigFile_dev_invalid(t *testing.T) {
	// "dev = maybe" hits the default branch — covers the dev-invalid-value error path.
	path := writeConfig(t, "dev = maybe\n")
	_, err := loadConfigFile(path)
	if err == nil {
		t.Fatal("expected error for invalid dev value, got nil")
	}
	if !strings.Contains(err.Error(), `"maybe"`) {
		t.Errorf("error should mention invalid value; got: %s", err.Error())
	}
}

func TestMergeFileConfig_mediaPath_fileApplied(t *testing.T) {
	// goCfg.MediaPath is empty — covers "goCfg.MediaPath = fileCfg.MediaPath" assignment.
	goCfg := Config{}
	fileCfg := Config{MediaPath: "./from-file"}
	result := mergeFileConfig(goCfg, fileCfg)
	if result.MediaPath != "./from-file" {
		t.Errorf("MediaPath: file should apply; got %q", result.MediaPath)
	}
}

func TestMergeFileConfig_mediaPath_goCodeWins(t *testing.T) {
	goCfg := Config{MediaPath: "./custom"}
	fileCfg := Config{MediaPath: "./from-file"}
	result := mergeFileConfig(goCfg, fileCfg)
	if result.MediaPath != "./custom" {
		t.Errorf("MediaPath: Go code should win; got %q", result.MediaPath)
	}
}

func TestMergeFileConfig_mediaMaxSize_fileApplied(t *testing.T) {
	goCfg := Config{} // MediaMaxSize is zero
	fileCfg := Config{MediaMaxSize: 2097152}
	result := mergeFileConfig(goCfg, fileCfg)
	if result.MediaMaxSize != 2097152 {
		t.Errorf("MediaMaxSize: file should apply to zero Go code; got %d", result.MediaMaxSize)
	}
}
