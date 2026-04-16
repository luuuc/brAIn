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
	// Note: `brain trust repair` is intentionally NOT exposed as an MCP
	// tool. Repair is an operator recovery action — any AI workflow
	// hitting a trust-state error should escalate to a human rather than
	// auto-repair. See .doc/definition/06-mcp-and-cli.md (CLI-only table)
	// for the full rationale.
	{
		Name:        "brain_trust",
		Description: "Check the trust level for a domain. Returns level (ask/notify/auto_ship/full_auto) and a recommendation (escalate/ship_notify/ship). Urgency=hotfix raises the floor to ship_notify.",
		InputSchema: toolSchema{
			Type: "object",
			Properties: map[string]schemaProperty{
				"domain": {
					Type:        "string",
					Description: "The trust domain to check (e.g. \"database\").",
				},
				"urgency": {
					Type:        "string",
					Description: "Optional urgency override.",
					Enum:        []string{"hotfix"},
				},
				"verbose": {
					Type:        "boolean",
					Description: "Include the domain's event history.",
					Default:     false,
				},
			},
			Required: []string{"domain"},
		},
	},
	{
		Name:        "brain_trust_record",
		Description: "Record an outcome for a trust domain. A clean outcome may promote; a failure immediately demotes the domain to ask. Duplicate refs are deduplicated.",
		InputSchema: toolSchema{
			Type: "object",
			Properties: map[string]schemaProperty{
				"domain": {
					Type:        "string",
					Description: "The trust domain.",
				},
				"outcome": {
					Type:        "string",
					Description: "clean or failure.",
					Enum:        []string{"clean", "failure"},
				},
				"ref": {
					Type:        "string",
					Description: "Optional reference (e.g. \"PR #42\") — used to deduplicate repeated reports.",
				},
				"reason": {
					Type:        "string",
					Description: "Optional reason (typically used on failures).",
				},
			},
			Required: []string{"domain", "outcome"},
		},
	},
	{
		Name:        "brain_trust_override",
		Description: "Record a human override for a trust domain. Writes a correction memory and appends an override event to the domain's history. Does not change trust level.",
		InputSchema: toolSchema{
			Type: "object",
			Properties: map[string]schemaProperty{
				"domain": {
					Type:        "string",
					Description: "The trust domain.",
				},
				"reason": {
					Type:        "string",
					Description: "Why the human overrode.",
				},
			},
			Required: []string{"domain", "reason"},
		},
	},
}

func (s *Server) handleToolsList(req *jsonrpcRequest) {
	s.sendResult(req.ID, map[string]any{
		"tools": brainTools,
	})
}
