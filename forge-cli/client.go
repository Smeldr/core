package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// Config holds the runtime configuration for forge-cli.
type Config struct {
	ForgeURL string // FORGE_URL — base URL of the Forge instance
	Token    string // FORGE_TOKEN — bearer token
	MCPURL   string // FORGE_MCP_URL — MCP message endpoint
}

// loadConfig reads FORGE_URL, FORGE_TOKEN, and FORGE_MCP_URL from the
// environment, falling back to a .forge-cli.env file in the working directory.
// FORGE_URL and FORGE_TOKEN must be non-empty or an error is returned.
func loadConfig() (Config, error) {
	loadEnvFile(".forge-cli.env")

	cfg := Config{
		ForgeURL: strings.TrimRight(os.Getenv("FORGE_URL"), "/"),
		Token:    os.Getenv("FORGE_TOKEN"),
		MCPURL:   os.Getenv("FORGE_MCP_URL"),
	}
	if cfg.ForgeURL == "" {
		return Config{}, fmt.Errorf("FORGE_URL is not set")
	}
	if cfg.Token == "" {
		return Config{}, fmt.Errorf("FORGE_TOKEN is not set")
	}
	if cfg.MCPURL == "" {
		cfg.MCPURL = cfg.ForgeURL + "/mcp/message"
	}
	return cfg, nil
}

// loadEnvFile reads key=value lines from path and sets environment variables
// that are not already set. Silently ignores files that do not exist.
func loadEnvFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if os.Getenv(k) == "" {
			os.Setenv(k, v) //nolint:errcheck
		}
	}
}

// request performs an authenticated HTTP request to url. body is JSON-encoded
// when non-nil. Returns the raw response bytes and HTTP status code.
func request(cfg Config, method, url string, body any) ([]byte, int, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal body: %w", err)
		}
		r = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, url, r)
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}
	return raw, resp.StatusCode, nil
}

// getItem GETs url and decodes the JSON response into a map.
func getItem(cfg Config, url string) (map[string]any, error) {
	raw, code, err := request(cfg, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if code >= 400 {
		return nil, fmt.Errorf("server returned %d: %s", code, strings.TrimSpace(string(raw)))
	}
	var item map[string]any
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return item, nil
}

// mergeFields overlays the src map onto dst using case-insensitive key matching.
// When a src key matches an existing dst key (case-insensitively), the dst key
// name is preserved and its value is replaced. New src keys with no match in
// dst are added as-is.
func mergeFields(dst map[string]any, src map[string]any) {
	lower := make(map[string]string, len(dst))
	for k := range dst {
		lower[strings.ToLower(k)] = k
	}
	for srcKey, val := range src {
		if dstKey, ok := lower[strings.ToLower(srcKey)]; ok {
			dst[dstKey] = val
		} else {
			dst[srcKey] = val
		}
	}
}

// printJSON pretty-prints raw JSON bytes to stdout.
func printJSON(raw []byte) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		fmt.Println(string(raw))
		return nil
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}

// fatal prints a formatted error message to stderr and exits with code 1.
func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "forge-cli: "+format+"\n", args...)
	os.Exit(1)
}
