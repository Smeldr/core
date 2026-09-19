# CLI / MCP parity review

One-session gap review (Task `t54-cli-mcp-parity-review`, 2026-09-19).
Compares the real MCP tool catalog against `smeldr-cli`'s real command
dispatch — not the aspirational lists in either module's own README.

**Scope note:** this review inventories the *historical* gap. It does
not implement CLI parity for anything found below — each row is real,
separate follow-up work. Going forward, `CLAUDE.md`'s own "CLI and MCP
tool parity" rule already requires new MCP tools to ship their CLI
equivalent in the same release; this review only covers what predates
that discipline being enforced consistently.

## Method

- MCP tool catalog: every real `Name: "..."` tool registration across
  `mcp/*.go` (excluding `*_test.go`) — ~75 tools.
- CLI command catalog: `cli/cmd/smeldr-cli/main.go`'s real dispatch
  table — `init`, the generic `<type> <verb>` content command, `token`,
  `media`, `webhook`, `preview`, `social`, `block` (nodes + section/item
  composition), `nav`, `redirect`, `audit`, `oauth`, `transition`,
  `logs`, `status`.
- The generic `<type> <verb>` content command (`content.go`) works over
  **HTTP REST routes**, not MCP tool names — it only covers a content
  type when that type has a registered `smeldr.At(prefix)` URL prefix.
  Confirmed directly: `Signal`, `Task`, `Decision`, `Amendment`, `Goal`,
  and `Run` (`RegisterOrchestrationTypes`, `core/orchestration.go`) all
  register a prefix (`/signals`, `/tasks`, etc.), so basic CRUD on every
  orchestration type is already covered by the generic content command
  — `smeldr-cli signal create --from ...`, `smeldr-cli goal list`, etc.
  all work today without a dedicated CLI subcommand.

## Confirmed covered

| CLI command group | MCP tool family |
|---|---|
| Generic `<type> <verb>` (create/update/publish/unpublish/archive/delete/list/get) | `create_*`/`update_*`/`publish_*`/`archive_*`/`get_*`/`delete_*`/`list_*` per-type tools, including all six orchestration types (`Signal`, `Task`, `Decision`, `Amendment`, `Goal`, `Run`) — each has a registered HTTP prefix |
| `token` | `create_token`, `list_tokens`, `revoke_token` |
| `webhook` | `create_webhook`, `list_webhooks`, `delete_webhook`, `list_webhook_deliveries`, `retry_webhook` |
| `preview` | `create_preview_url` |
| `social` | social's own MCP tool set (`create_scheduled_post`, `create_platform_config`, etc.) |
| `media upload` | `create_upload_token` — the CLI command does the full token-mint-then-POST flow in one step, not a separate exposed step |
| `block node` | `create_node`, `update_node`, `get_node`, `list_nodes`, `publish_node`, `archive_node` |
| `block section`/`block item` | `add_section`, `reorder_sections`, `remove_section`, `add_item`, `reorder_items`, `remove_item` (via `edge.go`) |
| `nav` | `list_nav_items`, `create_nav_item`, `update_nav_item`, `delete_nav_item` |
| `redirect` | `create_redirect`, `list_redirects`, `delete_redirect` |
| `transition` | `transition_item` |
| `oauth` (CLI) | CIMD/OAuth flows — a different surface entirely, not a 1:1 tool mapping, not a gap |

## Real gaps — MCP tools with no CLI command at all

| MCP tool(s) | Feature area | Suggested CLI command |
|---|---|---|
| `grant_role`, `list_grants`, `revoke_grant` | RoleStore governance grants | `smeldr-cli grant <verb>` |
| `set_page_meta`, `get_page_meta`, `delete_page_meta`, `list_page_meta` | Per-path SEO overrides | `smeldr-cli pagemeta <verb>` |
| `assert_relation`, `propose_relation`, `observe_relation`, `get_relations`, `preview_impact`, `upsert_relation_kind`, `list_relation_kinds` | Relation graph (distinct from block section/item composition — `edge.go` only covers block composition, not the relation graph). `get_goal_context`'s own linked-item assembly depends on this same graph and has no CLI equivalent for the same reason — not a separate gap, a consequence of this one. | `smeldr-cli relation <verb>` |
| `get_valid_transitions`, `list_items_by_state`, `define_state_flow` | State-flow introspection/definition — `transition` (CLI) only executes a transition, it doesn't let an operator inspect or define the flow itself | `smeldr-cli flow <verb>` |
| `get_content_type_schema`, `list_content_type_schemas` | Dynamic content type schema introspection | `smeldr-cli schema <verb>` |
| `get_check_status` | Check (governance precondition) results | `smeldr-cli check status` |
| `get_sweep_run` | Structural sweep run results | `smeldr-cli sweep status` |
| `get_stewardship_inbox` | Stewardship/role-grant context for the caller's own token | `smeldr-cli stewardship` |

## Not applicable

- `list_type_tools` — a discovery meta-tool for an MCP client enumerating
  another tool's siblings. No operator workflow needs this from a
  terminal; it exists for AI assistants navigating the tool catalog
  programmatically.

## Not a gap (resolved during this review)

- `create_signal`/`list_signals` — `Signal` has a registered HTTP prefix
  (`/signals`); `smeldr-cli signal create --from ...` already works via
  the generic content command.
- `create_upload_token` — `smeldr-cli media upload` already performs the
  full mint-token-then-POST flow; exposing the raw token step separately
  would be a regression in ergonomics, not a gap to close.
