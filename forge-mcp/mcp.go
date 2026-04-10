// Package forgemcp implements an MCP (Model Context Protocol) server for Forge
// applications. It exposes content modules registered with forge.MCP(...) as
// MCP resources and tools, enabling AI assistants to query and manage content
// through a structured protocol.
package forgemcp

import (
	"bytes"
	"encoding/json"
	"log"

	"github.com/forge-cms/forge"
)

// ServerOption configures a [Server]. Use [WithSecret] to override the HMAC
// secret used for SSE bearer-token authentication.
type ServerOption func(*Server)

// WithSecret overrides the HMAC secret used to verify SSE bearer tokens.
// The secret must match Config.Secret from the forge.App passed to New.
// Tokens minted by [forge.SignToken] with a different secret will fail SSE
// verification with HTTP 401. WithSecret is only needed for secret rotation.
func WithSecret(secret []byte) ServerOption {
	return func(s *Server) { s.secret = secret }
}

// Server wraps a set of [forge.MCPModule] values and serves the MCP protocol
// over stdio (see [Server.ServeStdio]) or HTTP SSE (see [Server.Handler]).
type Server struct {
	modules    []forge.MCPModule
	secret     []byte            // HMAC secret for SSE bearer-token verification
	tokenStore *forge.TokenStore // non-nil when the app has a TokenStore configured
}

// New creates a Server for the given forge App, collecting all content modules
// registered with forge.MCP(...). The App's HMAC secret (Config.Secret) is
// inherited automatically and used for SSE bearer-token verification.
// Pass [WithSecret] to override (e.g. during secret rotation).
func New(app *forge.App, opts ...ServerOption) *Server {
	s := &Server{
		modules:    app.MCPModules(),
		secret:     app.Secret(),
		tokenStore: app.TokenStore(),
	}
	for _, o := range opts {
		o(s)
	}
	if len(s.secret) > 0 && !bytes.Equal(s.secret, app.Secret()) {
		log.Printf("forge-mcp: WithSecret value differs from app Config.Secret — " +
			"tokens minted by forge.SignToken will fail SSE verification")
	}
	return s
}

// mcpResource is the internal representation of a single MCP resource entry.
type mcpResource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType"`
}

// mcpTool is the internal representation of a single MCP tool definition.
type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// allResources iterates MCPRead modules and builds the full resource list.
// Only Published items are included — lifecycle enforcement is unconditional.
func (s *Server) allResources(ctx forge.Context) []mcpResource {
	var out []mcpResource
	for _, m := range s.modules {
		if !hasMCPOp(m, forge.MCPRead) {
			continue
		}
		items, err := m.MCPList(ctx, forge.Published)
		if err != nil {
			continue
		}
		prefix := m.MCPMeta().Prefix
		typeName := m.MCPMeta().TypeName
		for _, item := range items {
			slug := slugOf(item)
			if slug == "" {
				continue
			}
			out = append(out, mcpResource{
				URI:      "forge:/" + prefix + "/" + slug,
				Name:     typeName + " — " + slug,
				MimeType: "application/json",
			})
		}
	}
	return out
}

// mcpToolDefs returns the tool definitions for a module that has MCPWrite.
func mcpToolDefs(m forge.MCPModule) []mcpTool {
	meta := m.MCPMeta()
	typeSnake := snakeCase(meta.TypeName)
	schema := m.MCPSchema()

	slugOnly := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"slug": map[string]any{"type": "string"},
		},
		"required": []string{"slug"},
	}

	return []mcpTool{
		{
			Name:        "create_" + typeSnake,
			Description: "Create a new " + meta.TypeName + " content item.",
			InputSchema: inputSchema(schema),
		},
		{
			Name:        "update_" + typeSnake,
			Description: "Partially update a " + meta.TypeName + " by slug.",
			InputSchema: inputSchemaUpdate(schema),
		},
		{
			Name:        "publish_" + typeSnake,
			Description: "Publish a " + meta.TypeName + " by slug.",
			InputSchema: slugOnly,
		},
		{
			Name:        "schedule_" + typeSnake,
			Description: "Schedule a " + meta.TypeName + " for future publication.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"slug":         map[string]any{"type": "string"},
					"scheduled_at": map[string]any{"type": "string", "format": "date-time"},
				},
				"required": []string{"slug", "scheduled_at"},
			},
		},
		{
			Name:        "archive_" + typeSnake,
			Description: "Archive a " + meta.TypeName + " by slug.",
			InputSchema: slugOnly,
		},
	}
}

// mcpAdminReadToolDefs returns the admin tool definitions for a module that
// has MCPWrite. These tools require Editor or Admin role and bypass the
// Published-only restriction, making them suitable for content management
// dashboards and admin AI assistants. Three tools are generated per MCPWrite
// module:
//
//   - list_{type}s — list all items; optional status filter ("draft",
//     "scheduled", "published", "archived")
//   - get_{type} — fetch a single item by slug regardless of status
//   - delete_{type} — permanently delete an item by slug
func mcpAdminReadToolDefs(m forge.MCPModule) []mcpTool {
	meta := m.MCPMeta()
	typeSnake := snakeCase(meta.TypeName)
	slugOnly := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"slug": map[string]any{"type": "string"},
		},
		"required": []string{"slug"},
	}
	return []mcpTool{
		{
			Name:        "list_" + typeSnake + "s",
			Description: "List all " + meta.TypeName + " items. Requires Editor or Admin role. Returns items at any lifecycle status.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"status": map[string]any{
						"type":        "string",
						"enum":        []string{"draft", "scheduled", "published", "archived"},
						"description": "Filter by lifecycle status. Omit to return all statuses.",
					},
				},
			},
		},
		{
			Name:        "get_" + typeSnake,
			Description: "Get a single " + meta.TypeName + " by slug. Requires Editor or Admin role. Returns the item at any lifecycle status.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"slug": map[string]any{"type": "string"},
				},
				"required": []string{"slug"},
			},
		},
		{
			Name:        "delete_" + typeSnake,
			Description: "Delete a " + meta.TypeName + " permanently. Requires Editor or Admin role.",
			InputSchema: slugOnly,
		},
	}
}

// inputSchema converts []forge.MCPField to a JSON Schema object suitable for
// MCP tools/list, marking Required fields in the required array.
// When a field carries a forge_format or forge_description tag, a
// "description" key is added to the property using the priority rules from
// Decision 27:
//   - Both present: forge_description + " (" + forge_format + ")"
//   - Only forge_format: "(" + forge_format + ")"
//   - Neither: no description key emitted
func inputSchema(fields []forge.MCPField) map[string]any {
	props := make(map[string]any, len(fields))
	var required []string
	for _, f := range fields {
		var prop map[string]any
		if f.Type == "array" {
			prop = map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			}
		} else if f.Type == "datetime" {
			// "datetime" is an internal Forge type identifier. JSON Schema
			// requires the RFC 3339 date-time format expressed as a string.
			prop = map[string]any{"type": "string", "format": "date-time"}
		} else {
			prop = map[string]any{"type": f.Type}
			if f.MinLength > 0 {
				prop["minLength"] = f.MinLength
			}
			if f.MaxLength > 0 {
				prop["maxLength"] = f.MaxLength
			}
			if len(f.Enum) > 0 {
				prop["enum"] = f.Enum
			}
		}
		if desc := fieldDescription(f); desc != "" {
			prop["description"] = desc
		}
		props[f.JSONName] = prop
		if f.Required {
			required = append(required, f.JSONName)
		}
	}
	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// inputSchemaUpdate builds an update tool schema: adds a required "slug" field
// and makes all content fields optional (partial-update semantics).
// Description hints from forge_format and forge_description tags are applied
// using the same priority rules as [inputSchema] (Decision 27).
func inputSchemaUpdate(fields []forge.MCPField) map[string]any {
	props := map[string]any{
		"slug": map[string]any{"type": "string"},
	}
	for _, f := range fields {
		var prop map[string]any
		if f.Type == "array" {
			prop = map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			}
		} else if f.Type == "datetime" {
			// "datetime" is an internal Forge type identifier. JSON Schema
			// requires the RFC 3339 date-time format expressed as a string.
			prop = map[string]any{"type": "string", "format": "date-time"}
		} else {
			prop = map[string]any{"type": f.Type}
			if f.MinLength > 0 {
				prop["minLength"] = f.MinLength
			}
			if f.MaxLength > 0 {
				prop["maxLength"] = f.MaxLength
			}
			if len(f.Enum) > 0 {
				prop["enum"] = f.Enum
			}
		}
		if desc := fieldDescription(f); desc != "" {
			prop["description"] = desc
		}
		props[f.JSONName] = prop
	}
	return map[string]any{
		"type":       "object",
		"properties": props,
		"required":   []string{"slug"},
	}
}

// fieldDescription builds the JSON Schema "description" string for a field
// from its Format and Description hints (Decision 27):
//   - Both non-empty: Description + " (" + Format + ")"
//   - Only Format: "(" + Format + ")"
//   - Neither: ""
func fieldDescription(f forge.MCPField) string {
	switch {
	case f.Description != "" && f.Format != "":
		return f.Description + " (" + f.Format + ")"
	case f.Format != "":
		return "(" + f.Format + ")"
	default:
		return ""
	}
}

// handle dispatches a JSON-RPC request to the appropriate handler.
// Returns a jsonRPCResponse ready for serialisation. Full dispatch logic is
// implemented in Steps 2–4; this stub returns a method-not-found error for any
// method other than "initialize".
func (s *Server) handle(ctx forge.Context, req jsonRPCRequest) jsonRPCResponse {
	switch req.Method {
	case "initialize":
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  s.handleInitialize(),
		}
	default:
		if r, ok := s.handleToolMethod(ctx, req); ok {
			return r
		}
		if r, ok := s.handleResourceMethod(ctx, req); ok {
			return r
		}
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &jsonRPCError{
				Code:    -32601,
				Message: "method not found: " + req.Method,
			},
		}
	}
}

// handleInitialize returns the MCP initialize response payload.
func (s *Server) handleInitialize() any {
	return map[string]any{
		"protocolVersion": "2024-11-05",
		"serverInfo":      map[string]any{"name": "forge-mcp", "version": "1.0.0"},
		"capabilities": map[string]any{
			"resources": map[string]any{"subscribe": false, "listChanged": false},
			"tools":     map[string]any{"listChanged": false},
		},
	}
}

// jsonRPCRequest is the wire format for an incoming JSON-RPC 2.0 request.
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonRPCResponse is the wire format for an outgoing JSON-RPC 2.0 response.
type jsonRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      any           `json:"id"`
	Result  any           `json:"result,omitempty"`
	Error   *jsonRPCError `json:"error,omitempty"`
}

// jsonRPCError is the error object within a JSON-RPC 2.0 response.
type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// hasMCPOp reports whether m's Operations slice contains op.
func hasMCPOp(m forge.MCPModule, op forge.MCPOperation) bool {
	for _, o := range m.MCPMeta().Operations {
		if o == op {
			return true
		}
	}
	return false
}

// slugOf extracts the Slug field from an item via the forge.Node GetSlug method.
// Returns "" when the item does not implement the interface.
func slugOf(item any) string {
	type slugger interface{ GetSlug() string }
	if s, ok := item.(slugger); ok {
		return s.GetSlug()
	}
	return ""
}

// snakeCase converts a PascalCase or camelCase string to lower_snake_case.
// Consecutive uppercase letters are treated as a single word:
//
//	BlogPost → blog_post
//	MCPPost  → mcp_post
//
// NOTE: This function is intentionally duplicated in module.go (forge core).
// The two packages cannot import each other, so each carries its own copy.
// Any change to the algorithm here must be mirrored in module.go, and vice versa.
func snakeCase(s string) string {
	runes := []rune(s)
	var out []rune
	for i, r := range runes {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				prev := runes[i-1]
				prevLow := (prev >= 'a' && prev <= 'z') || (prev >= '0' && prev <= '9')
				prevUp := prev >= 'A' && prev <= 'Z'
				if prevLow {
					out = append(out, '_')
				} else if prevUp {
					if i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z' {
						out = append(out, '_')
					}
				}
			}
			out = append(out, r+32)
		} else {
			out = append(out, r)
		}
	}
	return string(out)
}
