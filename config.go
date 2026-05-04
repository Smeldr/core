package forge

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// loadConfigFile parses a forge.config file at path and returns the values as
// a [Config]. Keys not present in the file are left as zero values. If the file
// does not exist, a zero Config and nil error are returned. Panics immediately
// if the file contains the key "secret".
//
// File format: one "key = value" pair per line. Lines beginning with "#" and
// blank lines are ignored. Keys and values are whitespace-trimmed. Values may
// contain "=" — only the first "=" is used as the separator. Unknown keys are
// silently ignored for forward compatibility.
func loadConfigFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("forge.config: cannot read %q: %w", path, err)
	}

	var cfg Config
	var schema AppSchema
	var og OGDefaults
	hasSchema, hasOG := false, false

	for i, raw := range strings.Split(string(data), "\n") {
		lineNum := i + 1
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		value := strings.TrimSpace(line[idx+1:])

		switch key {
		case "secret":
			panic("forge.config: \"secret\" must not be stored in a config file — use an environment variable or inject it directly in Go code")
		case "base_url":
			cfg.BaseURL = value
		case "https":
			switch value {
			case "true":
				cfg.HTTPS = true
			case "false":
				// explicit false: already the zero value, no-op
			default:
				return Config{}, fmt.Errorf("forge.config line %d: invalid value %q for key \"https\" — expected \"true\" or \"false\"", lineNum, value)
			}
		case "nav_mode":
			switch value {
			case "db":
				cfg.NavMode = NavModeDB
			case "code":
				cfg.NavMode = NavModeCode
			default:
				return Config{}, fmt.Errorf("forge.config line %d: invalid value %q for key \"nav_mode\" — expected \"db\" or \"code\"", lineNum, value)
			}
		case "org_name":
			schema.Name = value
			hasSchema = true
		case "org_type":
			schema.Type = value
			hasSchema = true
		case "twitter_site":
			og.TwitterSite = value
			hasOG = true
		case "og_image":
			og.Image.URL = value
			hasOG = true
		case "media_path":
			cfg.MediaPath = value
		case "media_max_size":
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return Config{}, fmt.Errorf("forge.config line %d: invalid value %q for key \"media_max_size\" — expected an integer number of bytes", lineNum, value)
			}
			cfg.MediaMaxSize = n
		case "dev":
			switch value {
			case "true":
				cfg.Dev = true
			case "false":
				// explicit false: zero value, no-op
			default:
				return Config{}, fmt.Errorf("forge.config line %d: invalid value %q for key \"dev\" — expected \"true\" or \"false\"", lineNum, value)
			}
			// unknown keys are silently ignored (forward compatibility)
		}
	}

	if hasSchema {
		s := schema
		cfg.AppSchema = &s
	}
	if hasOG {
		o := og
		cfg.OGDefaults = &o
	}

	return cfg, nil
}

// mergeFileConfig returns goCfg with zero-value fields replaced by the
// corresponding values from fileCfg. Fields already set in Go code take
// precedence and are never overwritten.
func mergeFileConfig(goCfg, fileCfg Config) Config {
	if goCfg.BaseURL == "" && fileCfg.BaseURL != "" {
		goCfg.BaseURL = fileCfg.BaseURL
	}
	if !goCfg.HTTPS && fileCfg.HTTPS {
		goCfg.HTTPS = true
	}
	if goCfg.NavMode == 0 && fileCfg.NavMode != 0 {
		goCfg.NavMode = fileCfg.NavMode
	}
	if goCfg.AppSchema == nil && fileCfg.AppSchema != nil {
		goCfg.AppSchema = fileCfg.AppSchema
	}
	if goCfg.OGDefaults == nil && fileCfg.OGDefaults != nil {
		goCfg.OGDefaults = fileCfg.OGDefaults
	}
	if goCfg.MediaPath == "" && fileCfg.MediaPath != "" {
		goCfg.MediaPath = fileCfg.MediaPath
	}
	if goCfg.MediaMaxSize == 0 && fileCfg.MediaMaxSize != 0 {
		goCfg.MediaMaxSize = fileCfg.MediaMaxSize
	}
	if !goCfg.Dev && fileCfg.Dev {
		goCfg.Dev = true
	}
	return goCfg
}
