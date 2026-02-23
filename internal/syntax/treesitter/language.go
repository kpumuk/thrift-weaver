// Package treesitter wraps go-tree-sitter parser primitives for Thrift syntax parsing.
package treesitter

import (
	sitter "github.com/tree-sitter/go-tree-sitter"

	treesitterthrift "github.com/kpumuk/thrift-weaver/grammar/tree-sitter-thrift"
)

// Language returns the Thrift tree-sitter language instance.
func Language() *sitter.Language {
	return treesitterthrift.Language()
}
