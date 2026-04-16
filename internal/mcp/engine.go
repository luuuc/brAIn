package mcp

import (
	"context"

	"github.com/luuuc/brain/internal/engine"
	"github.com/luuuc/brain/internal/memory"
	"github.com/luuuc/brain/internal/trust"
)

// Engine is the subset of engine.Engine that the MCP server uses.
// The concrete *engine.Engine satisfies this interface. Defined here
// so handler tests can supply a stub without touching the filesystem.
type Engine interface {
	Remember(ctx context.Context, m memory.Memory) (engine.RememberResult, error)
	Recall(ctx context.Context, opts engine.RecallOptions) ([]memory.Memory, error)
	Forget(ctx context.Context, path, reason string) error
}

// TrustEngine is the subset of trust.Engine that the MCP server uses.
// The concrete *trust.Engine satisfies this interface.
type TrustEngine interface {
	Check(ctx context.Context, domain string, opts trust.CheckOptions) (trust.Decision, error)
	Record(ctx context.Context, domain string, outcome trust.Outcome, opts trust.RecordOptions) (trust.RecordResult, error)
	Override(ctx context.Context, domain, reason string) (trust.Decision, error)
	List(ctx context.Context) ([]trust.Decision, error)
}
