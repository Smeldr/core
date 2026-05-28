package smeldr

import "time"

// MCPOperation is an option flag for the [MCP] function. Only [MCPRead] and
// [MCPWrite] are defined.
type MCPOperation string

const (
	// MCPRead signals that this module should be exposed as a read-only MCP
	// resource. The forge-mcp server will include it in resources/list and
	// resources/read responses. See [MCPModule].
	MCPRead MCPOperation = "read"

	// MCPWrite signals that this module should be exposed as a read+write MCP
	// resource. The forge-mcp server will generate tools for create, update,
	// publish, schedule, archive, and delete operations. See [MCPModule].
	MCPWrite MCPOperation = "write"
)

// mcpOption is the concrete [Option] returned by [MCP]. It carries the set of
// [MCPOperation] values requested by the caller.
type mcpOption struct{ ops []MCPOperation }

func (mcpOption) isOption() {}

// MCP marks a module as an MCP (Model Context Protocol) resource.
// Pass [MCPRead] to expose content as resources, [MCPWrite] to also generate
// write tools. See [MCPModule] for the interface implemented by [Module].
//
// Example:
//
//	app.Content(&BlogPost{},
//	    smeldr.At("/posts"),
//	    smeldr.MCP(smeldr.MCPRead, smeldr.MCPWrite),
//	)
func MCP(ops ...MCPOperation) Option {
	return mcpOption{ops: ops}
}

// MCPMeta describes the MCP registration of a content module.
// Returned by [MCPModule.MCPMeta].
type MCPMeta struct {
	Prefix         string         // URL prefix, e.g. "/posts"
	TypeName       string         // content type name, e.g. "BlogPost"
	Operations     []MCPOperation // MCPRead and/or MCPWrite
	SingleInstance bool           // true when the module uses the SingleInstance() option
}

// MCPField describes a single field in a content type's MCP schema, derived
// automatically from the Go struct type and forge: struct tags.
// Returned by [MCPModule.MCPSchema].
//
// The optional [MCPField.Format] and [MCPField.Description] fields are
// populated from the smeldr_format and smeldr_description struct tags
// respectively (Decision 27). Both are hints only — Forge performs no
// validation based on either value.
type MCPField struct {
	Name        string // Go field name
	JSONName    string // lowercase snake_case name used in MCP messages
	Type        string // "string" | "number" | "boolean" | "datetime"
	Format      string // "" when no smeldr_format tag present; e.g. "markdown", "html"
	Description string // "" when no smeldr_description tag present; free-text authoring guidance
	Required    bool
	MinLength   int      // 0 = no constraint
	MaxLength   int      // 0 = no constraint
	Enum        []string // nil = no constraint
}

// MCPModule is implemented by any [Module][T] that has been registered with
// [MCP]. forge-mcp reads this interface to build MCP resources and tools
// without accessing Module internals directly.
//
// All methods receive a [Context] carrying the authenticated user. Callers
// must construct the Context with the appropriate [Role] before calling any
// mutating method — the MCPModule implementation enforces roles and validation
// identically to the HTTP layer.
type MCPModule interface {
	// MCPMeta returns the module's MCP registration metadata.
	MCPMeta() MCPMeta
	// MCPSchema returns the field schema derived from the content type's
	// struct tags.
	MCPSchema() []MCPField
	// MCPList returns all items matching the given statuses (all statuses if
	// none are given).
	MCPList(ctx Context, status ...Status) ([]any, error)
	// MCPGet returns the item with the given slug.
	// MCPGet does not filter by lifecycle status — it returns the item
	// regardless of status. Callers are responsible for enforcing lifecycle
	// rules (e.g. forge-mcp checks that the item is Published before
	// including it in a resources/read response).
	MCPGet(ctx Context, slug string) (any, error)
	// MCPCreate creates a new item from the given fields map.
	MCPCreate(ctx Context, fields map[string]any) (any, error)
	// MCPUpdate applies a partial update to the item with the given slug.
	MCPUpdate(ctx Context, slug string, fields map[string]any) (any, error)
	// MCPPublish transitions the item with the given slug to Published.
	MCPPublish(ctx Context, slug string) error
	// MCPSchedule sets the item with the given slug to publish at the given time.
	MCPSchedule(ctx Context, slug string, at time.Time) error
	// MCPArchive transitions the item with the given slug to Archived.
	MCPArchive(ctx Context, slug string) error
	// MCPDelete permanently deletes the item with the given slug.
	MCPDelete(ctx Context, slug string) error
}
