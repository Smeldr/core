package forgemcp

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/forge-cms/forge"
)

// toolName builds the MCP tool name for a given operation and content type.
// The type name is converted to lower_snake_case by snakeCase; for example,
// toolName("create", "BlogPost") returns "create_blog_post".
func toolName(operation, typeName string) string {
	return operation + "_" + snakeCase(typeName)
}

// parseToolName splits a tool name of the form "operation_type_snake" on the
// first underscore. Returns ok=false when name contains no underscore.
func parseToolName(name string) (op, typeSnake string, ok bool) {
	op, typeSnake, ok = strings.Cut(name, "_")
	return
}

// moduleForType returns the first MCPWrite module whose TypeName, when
// converted to lower_snake_case, equals typeSnake.
// Returns (nil, false) when no matching module is found.
func (s *Server) moduleForType(typeSnake string) (forge.MCPModule, bool) {
	for _, m := range s.modules {
		if hasMCPOp(m, forge.MCPWrite) && snakeCase(m.MCPMeta().TypeName) == typeSnake {
			return m, true
		}
	}
	return nil, false
}

// moduleForAdminList returns the MCPWrite module for list_{type}s tool names.
// The list tool appends "s" to the type's snake_case name (e.g. "list_posts"
// targets the "post" type), so this helper tries typeSnake with a trailing
// "s" stripped when a direct lookup fails.
func (s *Server) moduleForAdminList(typeSnake string) (forge.MCPModule, bool) {
	if m, ok := s.moduleForType(typeSnake); ok {
		return m, true
	}
	if strings.HasSuffix(typeSnake, "s") {
		return s.moduleForType(typeSnake[:len(typeSnake)-1])
	}
	return nil, false
}

// authorise returns a -32001 error when the caller lacks the Author role,
// which is the minimum required for any MCPWrite operation.
func (s *Server) authorise(ctx forge.Context) *jsonRPCError {
	if forge.HasRole(ctx.User().Roles, forge.Author) {
		return nil
	}
	return &jsonRPCError{Code: -32001, Message: "forbidden"}
}

// authoriseEditor returns a -32001 error when the caller lacks Editor role.
// Editor is the minimum role required for admin read tools (list_{type}s,
// get_{type}). Admin also satisfies this check via the hierarchical role system.
func (s *Server) authoriseEditor(ctx forge.Context) *jsonRPCError {
	if forge.HasRole(ctx.User().Roles, forge.Editor) {
		return nil
	}
	return &jsonRPCError{Code: -32001, Message: "forbidden"}
}

// authoriseAdmin returns a -32001 error when the caller lacks Admin role.
// Admin is required for token management operations (create_token, list_tokens,
// revoke_token).
func (s *Server) authoriseAdmin(ctx forge.Context) *jsonRPCError {
	if forge.HasRole(ctx.User().Roles, forge.Admin) {
		return nil
	}
	return &jsonRPCError{Code: -32001, Message: "forbidden"}
}

// errorFor maps a forge error to a JSON-RPC error:
//   - [forge.ValidationError] → -32602 (invalid params) with the validation message
//   - [forge.ErrNotFound]      → -32001 (resource not found)
//   - [forge.ErrForbidden]     → -32001 (permission denied)
//   - all other errors         → -32603 (internal error)
func errorFor(err error) *jsonRPCError {
	var ve *forge.ValidationError
	if errors.As(err, &ve) {
		return &jsonRPCError{Code: -32602, Message: ve.Error()}
	}
	if errors.Is(err, forge.ErrNotFound) {
		return &jsonRPCError{Code: -32001, Message: "not found"}
	}
	if errors.Is(err, forge.ErrForbidden) {
		return &jsonRPCError{Code: -32001, Message: "forbidden"}
	}
	return &jsonRPCError{Code: -32603, Message: "internal error: " + err.Error()}
}

// handleToolsList returns the tools/list result: a "tools" array containing
// one entry per MCPWrite operation per registered MCPWrite module, plus two
// admin read tools (list_{type}s, get_{type}) per MCPWrite module. When
// the server has a TokenStore, three additional Admin-only token management
// tools are appended (create_token, list_tokens, revoke_token). When the
// server has a NavTree, nav management tools are appended (always
// list_nav_items; create/update/delete_nav_item only when the tree is DB-backed).
func (s *Server) handleToolsList() any {
	var tools []mcpTool
	for _, m := range s.modules {
		if !hasMCPOp(m, forge.MCPWrite) {
			continue
		}
		tools = append(tools, mcpToolDefs(m)...)
		tools = append(tools, mcpAdminReadToolDefs(m)...)
	}
	if s.tokenStore != nil {
		tools = append(tools, tokenToolDefs()...)
	}
	if s.navTree != nil {
		tools = append(tools, navToolDefs(s.navTree.HasDB())...)
	}
	return map[string]any{"tools": tools}
}

// handleToolsCall dispatches a tools/call request to the appropriate module
// operation. Author-level access is enforced before any module method is
// called.
//
// NOTE (zero-value limitation): the update operation works by JSON-merging the
// caller's fields onto the stored item. Fields with required or minimum-length
// constraints cannot be cleared to "" — the merge overlay will trigger a
// -32602 validation error. Unconstrained integer fields set to 0 and
// unconstrained string fields set to "" are accepted through the overlay.
// Callers that need to reset a required field must delete and recreate the
// item.
func (s *Server) handleToolsCall(ctx forge.Context, params json.RawMessage) (any, *jsonRPCError) {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: " + err.Error()}
	}
	if p.Name == "" {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: name required"}
	}

	// Token management tools require Admin role and are dispatched before
	// module-scoped tool authorisation.
	if s.tokenStore != nil {
		switch p.Name {
		case "create_token", "list_tokens", "revoke_token":
			if rpcErr := s.authoriseAdmin(ctx); rpcErr != nil {
				return nil, rpcErr
			}
			args := p.Arguments
			if args == nil {
				args = map[string]any{}
			}
			return s.handleTokenTool(ctx, p.Name, args)
		}
	}

	// Nav tools require Editor role and are dispatched before module-scoped
	// tool authorisation.
	if s.navTree != nil {
		switch p.Name {
		case "list_nav_items", "create_nav_item", "update_nav_item", "delete_nav_item":
			if rpcErr := s.authoriseEditor(ctx); rpcErr != nil {
				return nil, rpcErr
			}
			navArgs := p.Arguments
			if navArgs == nil {
				navArgs = map[string]any{}
			}
			return s.handleNavTool(ctx, p.Name, navArgs)
		}
	}

	if rpcErr := s.authorise(ctx); rpcErr != nil {
		return nil, rpcErr
	}

	op, typeSnake, ok := parseToolName(p.Name)
	if !ok {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid tool name: " + p.Name}
	}

	m, ok := s.moduleForType(typeSnake)
	if !ok && op != "list" {
		return nil, &jsonRPCError{Code: -32602, Message: "unknown tool: " + p.Name}
	}

	args := p.Arguments
	if args == nil {
		args = map[string]any{}
	}

	switch op {
	case "create":
		item, err := m.MCPCreate(ctx, args)
		if err != nil {
			return nil, errorFor(err)
		}
		return toolResult(item), nil

	case "update":
		slug, ok := stringArg(args, "slug")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: slug required"}
		}
		item, err := m.MCPUpdate(ctx, slug, args)
		if err != nil {
			return nil, errorFor(err)
		}
		return toolResult(item), nil

	case "publish":
		slug, ok := stringArg(args, "slug")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: slug required"}
		}
		// Idempotency: avoid double AfterPublish fire and PublishedAt re-stamp
		// when the item is already Published (Flag H).
		existing, err := m.MCPGet(ctx, slug)
		if err != nil {
			return nil, errorFor(err)
		}
		type statuser interface{ GetStatus() forge.Status }
		if st, isStatuser := existing.(statuser); isStatuser && st.GetStatus() == forge.Published {
			return toolResult(map[string]any{"slug": slug, "status": "published"}), nil
		}
		if err := m.MCPPublish(ctx, slug); err != nil {
			return nil, errorFor(err)
		}
		return toolResult(map[string]any{"slug": slug, "status": "published"}), nil

	case "schedule":
		slug, ok := stringArg(args, "slug")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: slug required"}
		}
		atStr, ok := stringArg(args, "scheduled_at")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: scheduled_at required"}
		}
		t, err := time.Parse(time.RFC3339, atStr)
		if err != nil {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: scheduled_at must be RFC3339"}
		}
		if err := m.MCPSchedule(ctx, slug, t); err != nil {
			return nil, errorFor(err)
		}
		return toolResult(map[string]any{"slug": slug, "status": "scheduled", "scheduled_at": atStr}), nil

	case "archive":
		slug, ok := stringArg(args, "slug")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: slug required"}
		}
		if err := m.MCPArchive(ctx, slug); err != nil {
			return nil, errorFor(err)
		}
		return toolResult(map[string]any{"slug": slug, "status": "archived"}), nil

	case "delete":
		if rpcErr := s.authoriseEditor(ctx); rpcErr != nil {
			return nil, rpcErr
		}
		slug, ok := stringArg(args, "slug")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: slug required"}
		}
		if err := m.MCPDelete(ctx, slug); err != nil {
			return nil, errorFor(err)
		}
		return toolResult(map[string]any{"deleted": true, "slug": slug}), nil

	case "list":
		lm, ok := s.moduleForAdminList(typeSnake)
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "unknown tool: " + p.Name}
		}
		if rpcErr := s.authoriseEditor(ctx); rpcErr != nil {
			return nil, rpcErr
		}
		var statuses []forge.Status
		if statusStr, ok := stringArg(args, "status"); ok {
			statuses = []forge.Status{forge.Status(statusStr)}
		}
		items, err := lm.MCPList(ctx, statuses...)
		if err != nil {
			return nil, errorFor(err)
		}
		if items == nil {
			items = []any{}
		}
		return toolResult(map[string]any{"items": items}), nil

	case "get":
		gm, ok := s.moduleForType(typeSnake)
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "unknown tool: " + p.Name}
		}
		if rpcErr := s.authoriseEditor(ctx); rpcErr != nil {
			return nil, rpcErr
		}
		slug, ok := stringArg(args, "slug")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: slug required"}
		}
		item, err := gm.MCPGet(ctx, slug)
		if err != nil {
			return nil, errorFor(err)
		}
		return toolResult(item), nil

	default:
		return nil, &jsonRPCError{Code: -32602, Message: "unknown operation: " + op}
	}
}

// handleToolMethod dispatches tool-related JSON-RPC methods.
// Returns (response, true) when the method is handled, (zero, false) otherwise.
// This allows the main handle switch in mcp.go to delegate cleanly.
func (s *Server) handleToolMethod(ctx forge.Context, req jsonRPCRequest) (jsonRPCResponse, bool) {
	switch req.Method {
	case "tools/list":
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  s.handleToolsList(),
		}, true
	case "tools/call":
		result, rpcErr := s.handleToolsCall(ctx, req.Params)
		if rpcErr != nil {
			return jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: rpcErr}, true
		}
		return jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: result}, true
	}
	return jsonRPCResponse{}, false
}

// toolResult wraps v in the MCP CallToolResult envelope that MCP clients
// require for tools/call responses. The payload is marshalled to JSON and
// embedded as the text of a single content item. This format applies to all
// successful results: create, update, publish, schedule, archive, delete,
// list, and get.
func toolResult(v any) map[string]any {
	data, _ := json.Marshal(v)
	return map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": string(data)},
		},
		"isError": false,
	}
}

// stringArg extracts a non-empty string value from args under the given key.
// Returns ("", false) if the key is absent, the value is not a string, or the
// value is an empty string.
func stringArg(args map[string]any, key string) (string, bool) {
	v, ok := args[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok && s != ""
}

// tokenToolDefs returns the three Admin-only token management tool definitions
// appended by [handleToolsList] when the server has a TokenStore configured.
func tokenToolDefs() []mcpTool {
	return []mcpTool{
		{
			Name:        "create_token",
			Description: "Create a named, revocable bearer token. Requires Admin role. Returns the raw token — store it securely; it cannot be retrieved again.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": `Human-readable label for this token (e.g. "GitHub Actions CI").`,
					},
					"role": map[string]any{
						"type":        "string",
						"enum":        []string{"author", "editor", "admin"},
						"description": "Role assigned to this token.",
					},
					"expires_in_days": map[string]any{
						"type":        "number",
						"description": "Token lifetime in days (e.g. 90).",
					},
				},
				"required": []string{"name", "role", "expires_in_days"},
			},
		},
		{
			Name:        "list_tokens",
			Description: "List all named bearer tokens. Requires Admin role. Includes revoked and expired tokens.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "revoke_token",
			Description: "Revoke a named bearer token by its fingerprint ID. Requires Admin role. Revoked tokens are rejected immediately on the next request.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "SHA-256 hex fingerprint of the token (from list_tokens).",
					},
				},
				"required": []string{"id"},
			},
		},
	}
}

// handleTokenTool dispatches create_token, list_tokens, and revoke_token
// requests. Called only when s.tokenStore is non-nil and the caller holds
// Admin role (checked by the caller).
func (s *Server) handleTokenTool(ctx forge.Context, name string, args map[string]any) (any, *jsonRPCError) {
	switch name {
	case "create_token":
		tokenName, ok := stringArg(args, "name")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: name required"}
		}
		role, ok := stringArg(args, "role")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: role required"}
		}
		days, ok := args["expires_in_days"].(float64)
		if !ok || days <= 0 {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: expires_in_days must be a positive number"}
		}
		ttl := time.Duration(float64(24*time.Hour) * days)
		raw, err := s.tokenStore.Create(ctx, tokenName, role, ttl)
		if err != nil {
			return nil, errorFor(err)
		}
		return toolResult(map[string]any{
			"token":   raw,
			"message": "Store this token securely — it cannot be retrieved again.",
		}), nil

	case "list_tokens":
		records, err := s.tokenStore.List(ctx)
		if err != nil {
			return nil, errorFor(err)
		}
		if records == nil {
			records = []forge.TokenRecord{}
		}
		return toolResult(map[string]any{"tokens": records}), nil

	case "revoke_token":
		id, ok := stringArg(args, "id")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: id required"}
		}
		if err := s.tokenStore.Revoke(ctx, id); err != nil {
			if errors.Is(err, forge.ErrLastAdmin) {
				return nil, &jsonRPCError{Code: -32602, Message: "Cannot revoke token: it is the last active admin token. Create a replacement admin token before revoking this one."}
			}
			return nil, errorFor(err)
		}
		return toolResult(map[string]any{"revoked": true, "id": id}), nil

	default:
		return nil, &jsonRPCError{Code: -32602, Message: "unknown token tool: " + name}
	}
}

// navToolDefs returns the nav tool definitions. list_nav_items is always
// included. create_nav_item, update_nav_item, and delete_nav_item are included
// only when hasDB is true (i.e. the NavTree is in NavModeDB).
//
// All nav tools require Editor or Admin role.
func navToolDefs(hasDB bool) []mcpTool {
	tools := []mcpTool{
		{
			Name:        "list_nav_items",
			Description: "List all navigation items in the site navigation tree. Requires Editor or Admin role.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
	if !hasDB {
		return tools
	}
	tools = append(tools,
		mcpTool{
			Name:        "create_nav_item",
			Description: "Create a new navigation item. Requires Editor or Admin role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"label":      map[string]any{"type": "string", "description": "Display text for this navigation item."},
					"path":       map[string]any{"type": "string", "description": "URL path prefix, e.g. /learn. Leave empty for a ghost (non-clickable) item."},
					"parent_id":  map[string]any{"type": "string", "description": "ID of the parent item. Omit or leave empty for a top-level item."},
					"module":     map[string]any{"type": "string", "description": "Forge module table name this item maps to. Omit for custom or ghost items."},
					"hidden":     map[string]any{"type": "boolean", "description": "Exclude from navigation while keeping in breadcrumbs."},
					"ghost":      map[string]any{"type": "boolean", "description": "Non-clickable structural grouping node. Still appears in navigation unless also hidden."},
					"sort_order": map[string]any{"type": "number", "description": "Display order within the same parent level (lower = earlier)."},
				},
				"required": []string{"label"},
			},
		},
		mcpTool{
			Name:        "update_nav_item",
			Description: "Update an existing navigation item by ID. Requires Editor or Admin role. Absent fields are preserved from the stored item.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":         map[string]any{"type": "string", "description": "ID of the navigation item to update."},
					"label":      map[string]any{"type": "string"},
					"path":       map[string]any{"type": "string"},
					"parent_id":  map[string]any{"type": "string"},
					"module":     map[string]any{"type": "string"},
					"hidden":     map[string]any{"type": "boolean"},
					"ghost":      map[string]any{"type": "boolean"},
					"sort_order": map[string]any{"type": "number"},
				},
				"required": []string{"id"},
			},
		},
		mcpTool{
			Name:        "delete_nav_item",
			Description: "Permanently delete a navigation item and all its descendants. Requires Editor or Admin role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "description": "ID of the navigation item to delete."},
				},
				"required": []string{"id"},
			},
		},
	)
	return tools
}

// handleNavTool dispatches list_nav_items, create_nav_item, update_nav_item,
// and delete_nav_item requests. Called only when s.navTree is non-nil and the
// caller holds Editor role (checked by the caller).
func (s *Server) handleNavTool(ctx forge.Context, name string, args map[string]any) (any, *jsonRPCError) {
	switch name {
	case "list_nav_items":
		items := s.navTree.List()
		if items == nil {
			items = []forge.NavItem{}
		}
		return toolResult(map[string]any{"items": items}), nil

	case "create_nav_item":
		label, ok := stringArg(args, "label")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: label required"}
		}
		item := forge.NavItem{
			Label:     label,
			Path:      stringArgOr(args, "path", ""),
			ParentID:  stringArgOr(args, "parent_id", ""),
			Module:    stringArgOr(args, "module", ""),
			Hidden:    boolArgOr(args, "hidden", false),
			Ghost:     boolArgOr(args, "ghost", false),
			SortOrder: intArgOr(args, "sort_order", 0),
		}
		created, err := s.navTree.Create(ctx, item)
		if err != nil {
			return nil, errorFor(err)
		}
		return toolResult(created), nil

	case "update_nav_item":
		id, ok := stringArg(args, "id")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: id required"}
		}
		// Fetch the existing item and apply the caller's fields as an overlay.
		existing, exists := s.navTree.Get(id)
		if !exists {
			return nil, &jsonRPCError{Code: -32001, Message: "not found"}
		}
		if sv, ok := args["label"].(string); ok {
			existing.Label = sv
		}
		if sv, ok := args["path"].(string); ok {
			existing.Path = sv
		}
		if sv, ok := args["parent_id"].(string); ok {
			existing.ParentID = sv
		}
		if sv, ok := args["module"].(string); ok {
			existing.Module = sv
		}
		if bv, ok := args["hidden"].(bool); ok {
			existing.Hidden = bv
		}
		if bv, ok := args["ghost"].(bool); ok {
			existing.Ghost = bv
		}
		if nv, ok := args["sort_order"].(float64); ok {
			existing.SortOrder = int(nv)
		}
		updated, err := s.navTree.Update(ctx, existing)
		if err != nil {
			return nil, errorFor(err)
		}
		return toolResult(updated), nil

	case "delete_nav_item":
		id, ok := stringArg(args, "id")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: id required"}
		}
		if err := s.navTree.Delete(ctx, id); err != nil {
			return nil, errorFor(err)
		}
		return toolResult(map[string]any{"deleted": true, "id": id}), nil
	}
	return nil, &jsonRPCError{Code: -32602, Message: "unknown nav tool: " + name}
}

// stringArgOr extracts a string from args under key, returning fallback when
// the key is absent or the value is not a string.
func stringArgOr(args map[string]any, key, fallback string) string {
	v, ok := args[key]
	if !ok {
		return fallback
	}
	s, ok := v.(string)
	if !ok {
		return fallback
	}
	return s
}

// boolArgOr extracts a bool from args under key, returning fallback when
// the key is absent or the value is not a bool.
func boolArgOr(args map[string]any, key string, fallback bool) bool {
	v, ok := args[key]
	if !ok {
		return fallback
	}
	b, ok := v.(bool)
	if !ok {
		return fallback
	}
	return b
}

// intArgOr extracts an int from args under key (JSON numbers arrive as float64),
// returning fallback when the key is absent or the value cannot be converted.
func intArgOr(args map[string]any, key string, fallback int) int {
	v, ok := args[key]
	if !ok {
		return fallback
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return fallback
}
