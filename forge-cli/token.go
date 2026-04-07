package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// runTokenCommand dispatches token subcommands. args begins with the verb.
func runTokenCommand(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: forge-cli token <verb> [args]\n")
		fmt.Fprintf(os.Stderr, "Verbs: create list revoke\n")
		os.Exit(1)
	}
	switch args[0] {
	case "-h", "--help", "help":
		printTokenHelp()
	case "create":
		runTokenCreate(args[1:])
	case "list":
		runTokenList(args[1:])
	case "revoke":
		runTokenRevoke(args[1:])
	default:
		fatal("unknown token verb %q — use: create list revoke", args[0])
	}
}

func printTokenHelp() {
	fmt.Fprint(os.Stdout, `forge-cli token — token management (Admin role required)

Verbs:
  create <name> <role> <ttl-days>   issue a new named token
  list                               list all tokens (incl. revoked/expired)
  revoke <id>                        revoke a token by fingerprint ID

The MCP endpoint is used for token operations (FORGE_MCP_URL).
`)
}

// runTokenCreate issues a new named token via the MCP create_token tool.
// Role must be one of: author, editor, admin.
func runTokenCreate(args []string) {
	fs := flag.NewFlagSet("token create", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: forge-cli token create <name> <role> <ttl-days>")
	}
	fs.Parse(args) //nolint:errcheck

	if fs.NArg() < 3 {
		fatal("token create requires: <name> <role> <ttl-days>")
	}
	name := fs.Arg(0)
	role := fs.Arg(1)
	ttlStr := fs.Arg(2)

	ttl, err := strconv.Atoi(ttlStr)
	if err != nil || ttl <= 0 {
		fatal("ttl-days must be a positive integer, got %q", ttlStr)
	}

	cfg, err := loadConfig()
	if err != nil {
		fatal("%v", err)
	}

	text, err := mcpCall(cfg, "create_token", map[string]any{
		"name":            name,
		"role":            role,
		"expires_in_days": ttl,
	})
	if err != nil {
		fatal("%v", err)
	}
	if err := printJSON([]byte(text)); err != nil {
		fatal("%v", err)
	}
}

// runTokenList lists all tokens via the MCP list_tokens tool.
func runTokenList(args []string) {
	fs := flag.NewFlagSet("token list", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: forge-cli token list")
	}
	fs.Parse(args) //nolint:errcheck

	cfg, err := loadConfig()
	if err != nil {
		fatal("%v", err)
	}

	text, err := mcpCall(cfg, "list_tokens", map[string]any{})
	if err != nil {
		fatal("%v", err)
	}
	if err := printJSON([]byte(text)); err != nil {
		fatal("%v", err)
	}
}

// runTokenRevoke revokes a token by fingerprint ID via the MCP revoke_token tool.
func runTokenRevoke(args []string) {
	fs := flag.NewFlagSet("token revoke", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: forge-cli token revoke <id>")
	}
	fs.Parse(args) //nolint:errcheck

	if fs.NArg() < 1 {
		fatal("token revoke requires a token fingerprint ID")
	}
	id := fs.Arg(0)

	cfg, err := loadConfig()
	if err != nil {
		fatal("%v", err)
	}

	text, err := mcpCall(cfg, "revoke_token", map[string]any{"id": id})
	if err != nil {
		fatal("%v", err)
	}
	if err := printJSON([]byte(text)); err != nil {
		fatal("%v", err)
	}
}

// mcpCall sends a JSON-RPC 2.0 tools/call request to cfg.MCPURL and returns
// the text content of the result. Returns an error for both transport failures
// and JSON-RPC-level errors.
func mcpCall(cfg Config, tool string, args map[string]any) (string, error) {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      tool,
			"arguments": args,
		},
	}

	raw, code, err := request(cfg, http.MethodPost, cfg.MCPURL, payload)
	if err != nil {
		return "", err
	}
	if code >= 400 {
		return "", fmt.Errorf("MCP server returned %d: %s", code, strings.TrimSpace(string(raw)))
	}

	var resp struct {
		Result *struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("decode MCP response: %w", err)
	}
	if resp.Error != nil {
		return "", fmt.Errorf("MCP error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	if resp.Result == nil || len(resp.Result.Content) == 0 {
		return "", nil
	}
	if resp.Result.IsError {
		return "", fmt.Errorf("tool error: %s", resp.Result.Content[0].Text)
	}
	return resp.Result.Content[0].Text, nil
}
