package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// runContentCommand dispatches content subcommands. typePath is the URL path
// segment for the content type (e.g. "posts", "doc-pages"). args are the
// remaining command-line arguments starting with the verb.
func runContentCommand(typePath string, args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: forge-cli %s <verb> [args]\n", typePath)
		fmt.Fprintf(os.Stderr, "Verbs: create update publish unpublish archive delete list get\n")
		os.Exit(1)
	}
	verb := args[0]
	rest := args[1:]

	switch verb {
	case "-h", "--help", "help":
		printContentHelp(typePath)
	case "create":
		runCreate(typePath, rest)
	case "update":
		runUpdate(typePath, rest)
	case "publish":
		runLifecycle(typePath, "publish", rest)
	case "unpublish":
		runLifecycle(typePath, "unpublish", rest)
	case "archive":
		runLifecycle(typePath, "archive", rest)
	case "delete":
		runDelete(typePath, rest)
	case "list":
		runList(typePath, rest)
	case "get":
		runGet(typePath, rest)
	default:
		fatal("unknown verb %q — use: create update publish unpublish archive delete list get", verb)
	}
}

func printContentHelp(typePath string) {
	fmt.Fprintf(os.Stdout, `forge-cli %s — content operations

Verbs:
  create    --from <file>       create a new draft
  update    <slug> --from <file>  update fields (absent fields preserved)
  publish   <slug>              transition to published
  unpublish <slug>              revert to draft
  archive   <slug>              transition to archived
  delete    <slug>              permanently delete
  list      [--status <s>]      list items; status: draft|published|archived|scheduled
  get       <slug>              fetch a single item
`, typePath)
}

// runCreate parses --from <file>, builds the JSON body, and POSTs to {type}.
func runCreate(typePath string, args []string) {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	from := fs.String("from", "", "frontmatter file (use - for stdin)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: forge-cli %s create --from <file>\n", typePath)
		fs.PrintDefaults()
	}
	fs.Parse(args) //nolint:errcheck

	if *from == "" {
		fatal("--from <file> is required for create")
	}

	fields, body, err := parseFrontmatterFile(*from)
	if err != nil {
		fatal("%v", err)
	}
	// Map body text to the "body" field when present and the frontmatter
	// does not already supply it.
	if strings.TrimSpace(body) != "" {
		if _, exists := fields["body"]; !exists {
			if _, exists := fields["Body"]; !exists {
				fields["Body"] = body
			}
		}
	}

	cfg, err := loadConfig()
	if err != nil {
		fatal("%v", err)
	}

	raw, code, err := request(cfg, http.MethodPost, cfg.ForgeURL+"/"+typePath, fields)
	if err != nil {
		fatal("%v", err)
	}
	if code >= 400 {
		fatal("server returned %d: %s", code, strings.TrimSpace(string(raw)))
	}
	if err := printJSON(raw); err != nil {
		fatal("%v", err)
	}
}

// runUpdate does a GET-then-PUT, overlaying frontmatter fields onto the
// existing item. Fields absent from the frontmatter file are preserved.
func runUpdate(typePath string, args []string) {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	from := fs.String("from", "", "frontmatter file (use - for stdin)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: forge-cli %s update <slug> --from <file>\n", typePath)
		fs.PrintDefaults()
	}
	fs.Parse(args) //nolint:errcheck

	if fs.NArg() < 1 {
		fatal("update requires a slug argument")
	}
	slug := fs.Arg(0)

	if *from == "" {
		fatal("--from <file> is required for update")
	}

	fields, body, err := parseFrontmatterFile(*from)
	if err != nil {
		fatal("%v", err)
	}

	cfg, err := loadConfig()
	if err != nil {
		fatal("%v", err)
	}

	url := cfg.ForgeURL + "/" + typePath + "/" + slug
	existing, err := getItem(cfg, url)
	if err != nil {
		fatal("GET failed: %v", err)
	}

	// Overlay frontmatter fields onto existing item (case-insensitive key match).
	mergeFields(existing, fields)

	// Map body text to the canonical key in the existing map when present.
	if strings.TrimSpace(body) != "" {
		if bodyKey := findKey(existing, "body"); bodyKey != "" {
			existing[bodyKey] = body
		} else {
			existing["Body"] = body
		}
	}

	raw, code, err := request(cfg, http.MethodPut, url, existing)
	if err != nil {
		fatal("%v", err)
	}
	if code >= 400 {
		fatal("server returned %d: %s", code, strings.TrimSpace(string(raw)))
	}
	if err := printJSON(raw); err != nil {
		fatal("%v", err)
	}
}

// runLifecycle implements publish, unpublish, and archive by doing a
// GET-then-PUT with the appropriate status value.
func runLifecycle(typePath, verb string, args []string) {
	fs := flag.NewFlagSet(verb, flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: forge-cli %s %s <slug>\n", typePath, verb)
	}
	fs.Parse(args) //nolint:errcheck

	if fs.NArg() < 1 {
		fatal("%s requires a slug argument", verb)
	}
	slug := fs.Arg(0)

	var newStatus string
	switch verb {
	case "publish":
		newStatus = "published"
	case "unpublish":
		newStatus = "draft"
	case "archive":
		newStatus = "archived"
	}

	cfg, err := loadConfig()
	if err != nil {
		fatal("%v", err)
	}

	url := cfg.ForgeURL + "/" + typePath + "/" + slug
	existing, err := getItem(cfg, url)
	if err != nil {
		fatal("GET failed: %v", err)
	}

	// Set the Status field using the exact key name from the GET response.
	if statusKey := findKey(existing, "status"); statusKey != "" {
		existing[statusKey] = newStatus
	} else {
		existing["Status"] = newStatus
	}

	raw, code, err := request(cfg, http.MethodPut, url, existing)
	if err != nil {
		fatal("%v", err)
	}
	if code >= 400 {
		fatal("server returned %d: %s", code, strings.TrimSpace(string(raw)))
	}
	if err := printJSON(raw); err != nil {
		fatal("%v", err)
	}
}

// runDelete permanently deletes an item by slug.
func runDelete(typePath string, args []string) {
	fs := flag.NewFlagSet("delete", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: forge-cli %s delete <slug>\n", typePath)
	}
	fs.Parse(args) //nolint:errcheck

	if fs.NArg() < 1 {
		fatal("delete requires a slug argument")
	}
	slug := fs.Arg(0)

	cfg, err := loadConfig()
	if err != nil {
		fatal("%v", err)
	}

	url := cfg.ForgeURL + "/" + typePath + "/" + slug
	raw, code, err := request(cfg, http.MethodDelete, url, nil)
	if err != nil {
		fatal("%v", err)
	}
	if code >= 400 {
		fatal("server returned %d: %s", code, strings.TrimSpace(string(raw)))
	}
	// 204 No Content — emit a confirmation.
	out, _ := json.MarshalIndent(map[string]any{"deleted": true, "slug": slug}, "", "  ")
	fmt.Println(string(out))
}

// runList fetches all items of typePath, applies an optional --status filter,
// and prints the result as JSON.
func runList(typePath string, args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	statusFilter := fs.String("status", "", "filter by status: draft|published|archived|scheduled")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: forge-cli %s list [--status <status>]\n", typePath)
		fs.PrintDefaults()
	}
	fs.Parse(args) //nolint:errcheck

	cfg, err := loadConfig()
	if err != nil {
		fatal("%v", err)
	}

	raw, code, err := request(cfg, http.MethodGet, cfg.ForgeURL+"/"+typePath, nil)
	if err != nil {
		fatal("%v", err)
	}
	if code >= 400 {
		fatal("server returned %d: %s", code, strings.TrimSpace(string(raw)))
	}

	var items []any
	if err := json.Unmarshal(raw, &items); err != nil {
		fatal("decode list: %v", err)
	}

	if *statusFilter != "" {
		var filtered []any
		for _, item := range items {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			// Status field may be "Status" (PascalCase, no JSON tag on Node).
			if statusVal, _ := m[findKeyIn(m, "status")].(string); statusVal == *statusFilter {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}

	out, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		fatal("encode output: %v", err)
	}
	fmt.Println(string(out))
}

// runGet fetches a single item by slug and prints it as JSON.
func runGet(typePath string, args []string) {
	fs := flag.NewFlagSet("get", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: forge-cli %s get <slug>\n", typePath)
	}
	fs.Parse(args) //nolint:errcheck

	if fs.NArg() < 1 {
		fatal("get requires a slug argument")
	}
	slug := fs.Arg(0)

	cfg, err := loadConfig()
	if err != nil {
		fatal("%v", err)
	}

	url := cfg.ForgeURL + "/" + typePath + "/" + slug
	raw, code, err := request(cfg, http.MethodGet, url, nil)
	if err != nil {
		fatal("%v", err)
	}
	if code >= 400 {
		fatal("server returned %d: %s", code, strings.TrimSpace(string(raw)))
	}
	if err := printJSON(raw); err != nil {
		fatal("%v", err)
	}
}

// findKey returns the key in m whose lowercase form matches query, or "".
func findKey(m map[string]any, query string) string {
	return findKeyIn(m, query)
}

// findKeyIn returns the map key that case-insensitively matches query, or "".
func findKeyIn(m map[string]any, query string) string {
	q := strings.ToLower(query)
	for k := range m {
		if strings.ToLower(k) == q {
			return k
		}
	}
	return ""
}
