package forgemcp

import (
	"encoding/json"
	"strings"

	"forge-cms.dev/forge"
)

// resourceContent is the wire format for a single item returned by resources/read.
type resourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType"`
	Text     string `json:"text"` // JSON-encoded content item
}

// resourceTemplate is the wire format for a single entry in resources/templates/list.
type resourceTemplate struct {
	URITemplate string `json:"uriTemplate"` // e.g. "forge://posts/{slug}"
	Name        string `json:"name"`
	Description string `json:"description"`
	MimeType    string `json:"mimeType"`
}

// handleResourcesList returns all Published items from MCPRead modules as MCP
// resources. Lifecycle enforcement (Published-only) is unconditional and
// delegated to allResources.
func (s *Server) handleResourcesList(ctx forge.Context) any {
	return map[string]any{"resources": s.allResources(ctx)}
}

// handleResourcesTemplatesList returns one URI template per MCPRead module.
// No repo access is required; templates are derived from module metadata alone.
func (s *Server) handleResourcesTemplatesList() any {
	var templates []resourceTemplate
	for _, m := range s.modules {
		if !hasMCPOp(m, forge.MCPRead) {
			continue
		}
		meta := m.MCPMeta()
		templates = append(templates, resourceTemplate{
			URITemplate: "forge:/" + meta.Prefix + "/{slug}",
			Name:        meta.TypeName + " by slug",
			Description: "Retrieve a single " + meta.TypeName + " content item by its slug.",
			MimeType:    "application/json",
		})
	}
	return map[string]any{"resourceTemplates": templates}
}

// parseResourceURI resolves a forge:// URI to its module and slug.
// It iterates all MCPRead modules and matches on the module's prefix.
// Returns (nil, "", false) for an unknown prefix, empty slug, or a slug
// that contains extra path segments.
func (s *Server) parseResourceURI(uri string) (forge.MCPModule, string, bool) {
	for _, m := range s.modules {
		if !hasMCPOp(m, forge.MCPRead) {
			continue
		}
		prefix := m.MCPMeta().Prefix
		after, ok := strings.CutPrefix(uri, "forge:/"+prefix+"/")
		if !ok || after == "" || strings.Contains(after, "/") {
			continue
		}
		return m, after, true
	}
	return nil, "", false
}

// handleResourcesRead returns the JSON content of a single Published item
// identified by its forge:// URI. Returns a -32001 error if the URI is
// malformed, the item does not exist, or the item is not Published.
func (s *Server) handleResourcesRead(ctx forge.Context, params json.RawMessage) (any, *jsonRPCError) {
	var p struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.URI == "" {
		return nil, &jsonRPCError{Code: -32001, Message: "invalid params: uri required"}
	}

	m, slug, ok := s.parseResourceURI(p.URI)
	if !ok {
		return nil, &jsonRPCError{Code: -32001, Message: "resource not found: " + p.URI}
	}

	item, err := m.MCPGet(ctx, slug)
	if err != nil {
		return nil, &jsonRPCError{Code: -32001, Message: "resource not found: " + slug}
	}

	// Callers are responsible for lifecycle enforcement. We enforce Published here.
	type statuser interface{ GetStatus() forge.Status }
	if st, ok := item.(statuser); !ok || st.GetStatus() != forge.Published {
		return nil, &jsonRPCError{Code: -32001, Message: "resource not found: " + slug}
	}

	b, err := json.Marshal(item)
	if err != nil {
		return nil, &jsonRPCError{Code: -32001, Message: "internal error marshalling resource"}
	}

	return map[string]any{
		"contents": []resourceContent{{
			URI:      p.URI,
			MimeType: "application/json",
			Text:     string(b),
		}},
	}, nil
}

// handleResourceMethod dispatches resource-related JSON-RPC methods.
// Returns (response, true) when the method is handled, (zero, false) otherwise.
// This allows the main handle switch in mcp.go to delegate cleanly.
func (s *Server) handleResourceMethod(ctx forge.Context, req jsonRPCRequest) (jsonRPCResponse, bool) {
	switch req.Method {
	case "resources/list":
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  s.handleResourcesList(ctx),
		}, true
	case "resources/templates/list":
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  s.handleResourcesTemplatesList(),
		}, true
	case "resources/read":
		result, rpcErr := s.handleResourcesRead(ctx, req.Params)
		if rpcErr != nil {
			return jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: rpcErr}, true
		}
		return jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: result}, true
	}
	return jsonRPCResponse{}, false
}
