# Changelog — forge-cli

All notable changes to the `forge-cli` module are documented here.

Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versioning: [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [0.1.0] — 2026-04-07

Initial release — operator CLI for Forge instances (Decision 28).

### Added

- `forge-cli <type> create [--from file]` — create a Draft via `POST /{prefix}`
- `forge-cli <type> update <slug> [--from file]` — GET-then-PUT field overlay
- `forge-cli <type> publish <slug>` — GET-then-PUT with `Status: published`
- `forge-cli <type> unpublish <slug>` — GET-then-PUT with `Status: draft`
- `forge-cli <type> archive <slug>` — GET-then-PUT with `Status: archived`
- `forge-cli <type> delete <slug>` — `DELETE /{prefix}/{slug}`
- `forge-cli <type> list [--status <s>]` — list items with optional status filter
- `forge-cli <type> get <slug>` — print a single item as JSON
- `forge-cli token create --name <n> --role <r> [--ttl <d>]` — issue a token via MCP
- `forge-cli token list` — list tokens via MCP
- `forge-cli token revoke <id>` — revoke a token via MCP
- `forge-cli status` — `GET /_health`, print JSON
- Config via `FORGE_URL`, `FORGE_TOKEN`, `FORGE_MCP_URL` env vars or `.forge-cli.env`
- YAML-subset frontmatter parser (no external dependencies)
- Pure stdlib — zero third-party dependencies
