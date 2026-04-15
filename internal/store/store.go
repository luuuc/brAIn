package store

import (
	"context"
	"errors"

	"github.com/luuuc/brain/internal/memory"
)

// Sentinel errors for Store implementations.
var (
	ErrNotFound = errors.New("memory not found")
	ErrConflict = errors.New("memory conflict")
)

// Store defines the storage interface for memory files. Both the markdown
// adapter and the future PG adapter implement this interface.
type Store interface {
	// Write creates or updates a memory. If m.Path is empty, a new file is
	// created and the generated path is returned. If m.Path is set, the
	// existing file is overwritten.
	Write(ctx context.Context, m memory.Memory) (path string, err error)

	// Read loads a single memory by its path. Returns ErrNotFound if the
	// path does not exist.
	Read(ctx context.Context, path string) (memory.Memory, error)

	// List returns all memories matching the filter. Returns an empty slice
	// (not nil) if no memories match.
	List(ctx context.Context, f Filter) ([]memory.Memory, error)

	// Delete removes a memory by path. Returns ErrNotFound if the path
	// does not exist.
	Delete(ctx context.Context, path string) error
}

// Filter defines optional push-down filters for List. Using a struct (not a
// predicate function) lets both adapters optimize: the markdown adapter skips
// layer subdirectories that don't match, the future PG adapter pushes filters
// into SQL.
type Filter struct {
	Layer  *memory.Layer // filter by layer
	Domain *string       // filter by domain
	Tags   []string      // filter by tags (all must match)
}
