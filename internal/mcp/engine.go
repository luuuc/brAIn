package mcp

import (
	"context"

	"github.com/luuuc/brain/internal/engine"
	"github.com/luuuc/brain/internal/memory"
)

// Engine is the subset of engine.Engine that the MCP server uses.
// The concrete *engine.Engine satisfies this interface. Defined here
// so handler tests can supply a stub without touching the filesystem.
type Engine interface {
	Remember(ctx context.Context, m memory.Memory) (engine.RememberResult, error)
	Recall(ctx context.Context, opts engine.RecallOptions) ([]memory.Memory, error)
	Forget(ctx context.Context, path, reason string) error
}
