// Package main is the forge-cli operator tool. It provides a terminal interface
// for managing content and tokens on a running Forge instance over HTTP.
//
// Configuration is loaded from environment variables, falling back to a
// .forge-cli.env file in the working directory:
//
//	FORGE_URL     — base URL of the running Forge instance (required)
//	FORGE_TOKEN   — bearer token with appropriate role (required)
//	FORGE_MCP_URL — MCP message endpoint (default: FORGE_URL/mcp/message)
//
// Usage:
//
//	forge-cli <type> <verb> [slug] [flags]
//	forge-cli token <verb> [args...]
//	forge-cli status
package main

import (
	"fmt"
	"os"
)

const cliVersion = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		printUsage(os.Stderr)
		os.Exit(1)
	}
	switch os.Args[1] {
	case "-h", "--help", "help":
		printUsage(os.Stdout)
	case "-v", "--version", "version":
		fmt.Fprintln(os.Stdout, "forge-cli v"+cliVersion)
	case "status":
		runStatus(os.Args[2:])
	case "token":
		runTokenCommand(os.Args[2:])
	case "media":
		runMediaCommand(os.Args[2:])
	default:
		runContentCommand(os.Args[1], os.Args[2:])
	}
}

func printUsage(w *os.File) {
	fmt.Fprintf(w, `forge-cli v%s — Forge operator CLI

Usage:
  forge-cli <type> <verb> [slug] [flags]   content operations
  forge-cli token <verb> [args]            token management
  forge-cli status                         connectivity check

Content verbs (type is the URL path segment, e.g. "posts", "doc-pages"):
  create    --from <file>                  create a new draft
  update    <slug> --from <file>           update fields (preserves absent fields)
  publish   <slug>                         transition to published
  unpublish <slug>                         revert published item to draft
  archive   <slug>                         transition to archived
  delete    <slug>                         permanently delete
  list      [--status draft|published|archived|scheduled]
  get       <slug>

Token verbs (Admin role required):
  create <name> <role> <ttl-days>          issue a new named token
  list                                     list all tokens
  revoke <id>                              revoke a token by fingerprint ID

Media subcommands:
  upload <file> [--description <text>]     upload a file to the media library
  list [--type image|document|video|other] list media records
  delete <id>                              permanently delete a media record

Environment variables:
  FORGE_URL      base URL of the running Forge instance (required)
  FORGE_TOKEN    bearer token with appropriate role (required)
  FORGE_MCP_URL  MCP message endpoint (default: FORGE_URL/mcp/message)

Configuration can also be stored in .forge-cli.env in the working directory.
`, cliVersion)
}
