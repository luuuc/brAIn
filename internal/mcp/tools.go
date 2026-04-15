package mcp

// Tool schema definitions for MCP tools/list responses.

type toolDefinition struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	InputSchema toolSchema `json:"inputSchema"`
}

type toolSchema struct {
	Type       string                    `json:"type"`
	Properties map[string]schemaProperty `json:"properties,omitempty"`
	Required   []string                  `json:"required,omitempty"`
}

type schemaProperty struct {
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Enum        []string `json:"enum,omitempty"`
	Default     any      `json:"default,omitempty"`
}

var brainTools = []toolDefinition{
	{
		Name:        "brain_remember",
		Description: "Store a new memory in brAIn. Memories persist across sessions and are ranked by layer authority (corrections > decisions > lessons > facts).",
		InputSchema: toolSchema{
			Type: "object",
			Properties: map[string]schemaProperty{
				"content": {
					Type:        "string",
					Description: "The memory content to store.",
				},
				"domain": {
					Type:        "string",
					Description: "The project domain this memory belongs to (e.g. \"database\", \"frontend\", \"testing\").",
				},
				"layer": {
					Type:        "string",
					Description: "Memory layer. If omitted, brAIn auto-detects from content signals.",
					Enum:        []string{"fact", "lesson", "decision", "effectiveness", "correction"},
				},
				"tags": {
					Type:        "string",
					Description: "Comma-separated tags for the memory.",
				},
			},
			Required: []string{"content", "domain"},
		},
	},
	{
		Name:        "brain_recall",
		Description: "Retrieve relevant memories from brAIn, ranked by authority (corrections first, then decisions, lessons, facts). Use this to load project context before starting work.",
		InputSchema: toolSchema{
			Type: "object",
			Properties: map[string]schemaProperty{
				"domain": {
					Type:        "string",
					Description: "Filter to a specific domain.",
				},
				"query": {
					Type:        "string",
					Description: "Search query to match against memory titles and tags.",
				},
				"layer": {
					Type:        "string",
					Description: "Filter to a specific layer.",
					Enum:        []string{"fact", "lesson", "decision", "effectiveness", "correction"},
				},
				"limit": {
					Type:        "number",
					Description: "Maximum number of memories to return.",
					Default:     5,
				},
			},
		},
	},
	{
		Name:        "brain_list",
		Description: "List all memories in brAIn, optionally filtered by layer or domain. Unlike recall, list does not rank or search — it returns everything matching the filter.",
		InputSchema: toolSchema{
			Type: "object",
			Properties: map[string]schemaProperty{
				"layer": {
					Type:        "string",
					Description: "Filter to a specific layer.",
					Enum:        []string{"fact", "lesson", "decision", "effectiveness", "correction"},
				},
				"domain": {
					Type:        "string",
					Description: "Filter to a specific domain.",
				},
				"include_retired": {
					Type:        "boolean",
					Description: "Include retired memories in the list.",
					Default:     false,
				},
			},
		},
	},
	{
		Name:        "brain_forget",
		Description: "Soft-retire a memory. The memory is not deleted — it is marked as retired and excluded from future recall unless explicitly requested.",
		InputSchema: toolSchema{
			Type: "object",
			Properties: map[string]schemaProperty{
				"path": {
					Type:        "string",
					Description: "Path of the memory to retire (e.g. \"facts/users-table.md\").",
				},
				"reason": {
					Type:        "string",
					Description: "Optional reason for retiring this memory.",
				},
			},
			Required: []string{"path"},
		},
	},
}

func (s *Server) handleToolsList(req *jsonrpcRequest) {
	s.sendResult(req.ID, map[string]any{
		"tools": brainTools,
	})
}
