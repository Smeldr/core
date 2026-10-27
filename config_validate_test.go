package smeldr

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestValidateConfig (T969): MustConfig's rules as an error, with the
// smeldr.config merge, so a caller can refuse before it writes anything.
func TestValidateConfig(t *testing.T) {
	secret := []byte("supersecretkey16")
	for _, tc := range []struct {
		name  string
		cfg   Config
		field string
	}{
		{"valid", Config{BaseURL: "https://example.com", Secret: secret}, ""},
		{"missing BaseURL", Config{Secret: secret}, "base_url"},
		{"relative BaseURL", Config{BaseURL: "/x", Secret: secret}, "base_url"},
		{"short Secret", Config{BaseURL: "https://example.com", Secret: []byte("short")}, "secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			got, err := ValidateConfig(tc.cfg)
			var ve *ValidationError
			switch {
			case tc.field == "" && (err != nil || got.BaseURL != tc.cfg.BaseURL):
				t.Errorf("got %+v, %v", got, err)
			case tc.field != "" && (!errors.As(err, &ve) || len(ve.fields) != 1 || ve.fields[0].field != tc.field):
				t.Errorf("err = %v, want a ValidationError on %s", err, tc.field)
			}
		})
	}

	t.Run("base_url from smeldr.config", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		if err := os.WriteFile(filepath.Join(dir, "smeldr.config"), []byte("base_url = https://from-file.example\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := ValidateConfig(Config{Secret: secret})
		if err != nil || got.BaseURL != "https://from-file.example" {
			t.Errorf("got %q, %v", got.BaseURL, err)
		}
	})
	t.Run("SMELDR_CONFIG names a file with an invalid value", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "bad.config")
		if err := os.WriteFile(path, []byte("https = maybe\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("SMELDR_CONFIG", path)
		var ve *ValidationError
		if _, err := ValidateConfig(Config{BaseURL: "https://example.com", Secret: secret}); !errors.As(err, &ve) || ve.fields[0].field != "smeldr_config" {
			t.Errorf("err = %v", err)
		}
	})
}
