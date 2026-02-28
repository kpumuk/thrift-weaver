// Package backend defines parser backend abstractions for syntax parsing.
package backend

import (
	"context"

	ts "github.com/kpumuk/thrift-weaver/internal/syntax/treesitter"
)

// Parser is a low-level tree-sitter parser contract used by syntax.Parse/Reparse.
type Parser interface {
	Parse(ctx context.Context, src []byte, old *ts.Tree) (*ts.Tree, error)
	Close()
}

// Factory creates parser instances for a specific backend implementation.
type Factory interface {
	Name() string
	NewParser() (Parser, error)
}
