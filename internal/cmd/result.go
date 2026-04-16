package cmd

// Result types for JSON output. Shared between subcommands and tests.

// RememberResult is the JSON output of brain remember.
type RememberResult struct {
	Path     string   `json:"path"`
	Layer    string   `json:"layer"`
	Domain   string   `json:"domain"`
	Warnings []string `json:"warnings,omitempty"`
}

// RecallResult is the JSON output of brain recall.
type RecallResult struct {
	Memories []RecallMemory `json:"memories"`
}

// RecallMemory is a single memory in recall output.
type RecallMemory struct {
	Path   string   `json:"path"`
	Layer  string   `json:"layer"`
	Domain string   `json:"domain"`
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	Tags   []string `json:"tags,omitempty"`
}

// ListResult is the JSON output of brain list.
type ListResult struct {
	Memories []ListMemory `json:"memories"`
	Count    int          `json:"count"`
}

// ListMemory is a single memory in list output.
type ListMemory struct {
	Path    string `json:"path"`
	Layer   string `json:"layer"`
	Domain  string `json:"domain"`
	Created string `json:"created"`
	Retired bool   `json:"retired,omitempty"`
}

// ForgetResult is the JSON output of brain forget.
type ForgetResult struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// Trust-command JSON payloads are built by trust.DecisionJSON and assembled
// inline in trust.go. Keeping those shapes in typed structs here would
// fork the schema — the shared helper is the one source of truth.
