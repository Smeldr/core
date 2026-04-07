package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// parseFrontmatterFile reads the file at path (or stdin when path is "-"),
// parses the YAML-subset frontmatter, and returns the field map and body.
//
// See [parseFrontmatter] for the supported format.
func parseFrontmatterFile(path string) (map[string]any, string, error) {
	var data []byte
	if path == "-" {
		var err error
		data, err = io.ReadAll(os.Stdin)
		if err != nil {
			return nil, "", fmt.Errorf("read stdin: %w", err)
		}
	} else {
		var err error
		data, err = os.ReadFile(path)
		if err != nil {
			return nil, "", fmt.Errorf("read %q: %w", path, err)
		}
	}
	return parseFrontmatter(string(data))
}

// parseFrontmatter parses a YAML-subset frontmatter string and returns the
// field map and any body text that follows the closing delimiter.
//
// Format:
//
//	---
//	title: My Post
//	tags: [go, forge]
//	---
//	Markdown body here...
//
// Supported value types:
//   - Lists:  [a, b, c] → []string (split on comma; inner whitespace trimmed)
//   - String: everything else (value trimmed of leading/trailing whitespace)
//
// The function returns an error if the content does not begin with "---\n".
// When a body section is present after the closing "---", it is returned as
// bodyStr. The caller decides which field name to use for the body.
func parseFrontmatter(content string) (map[string]any, string, error) {
	if !strings.HasPrefix(content, "---\n") {
		return nil, "", fmt.Errorf("content must begin with ---")
	}
	rest := content[4:] // strip opening "---\n"

	// Find the closing "---" delimiter.
	end := strings.Index(rest, "\n---")
	if end == -1 {
		end = len(rest) // no closing delimiter — all content is the header
	}

	header := rest[:end]
	var body string
	if end < len(rest) {
		after := rest[end+4:] // skip "\n---"
		if strings.HasPrefix(after, "\n") {
			after = after[1:]
		}
		body = after
	}

	fields := make(map[string]any)
	scanner := bufio.NewScanner(strings.NewReader(header))
	for scanner.Scan() {
		line := scanner.Text()
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" {
			continue
		}
		if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") {
			inner := v[1 : len(v)-1]
			var items []string
			for _, part := range strings.Split(inner, ",") {
				if s := strings.TrimSpace(part); s != "" {
					items = append(items, s)
				}
			}
			fields[k] = items
		} else {
			fields[k] = v
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, "", fmt.Errorf("parse frontmatter: %w", err)
	}
	return fields, body, nil
}
