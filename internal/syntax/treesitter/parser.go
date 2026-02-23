package treesitter

import (
	"context"
	"errors"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// Parser wraps a tree-sitter parser configured for Thrift.
type Parser struct {
	inner *sitter.Parser
}

// NewParser creates a Thrift parser with the grammar language preloaded.
func NewParser() (*Parser, error) {
	p := sitter.NewParser()
	if err := p.SetLanguage(Language()); err != nil {
		p.Close()
		return nil, err
	}
	return &Parser{inner: p}, nil
}

// Close releases parser resources.
func (p *Parser) Close() {
	if p == nil || p.inner == nil {
		return
	}
	p.inner.Close()
	p.inner = nil
}

// Tree wraps a parsed tree-sitter tree.
type Tree struct {
	inner *sitter.Tree
}

// Close releases tree resources.
func (t *Tree) Close() {
	if t == nil || t.inner == nil {
		return
	}
	t.inner.Close()
	t.inner = nil
}

// Inner returns the wrapped go-tree-sitter tree pointer.
func (t *Tree) Inner() *sitter.Tree {
	if t == nil {
		return nil
	}
	return t.inner
}

// Root returns the wrapped root node.
func (t *Tree) Root() Node {
	if t == nil || t.inner == nil {
		return Node{}
	}
	return wrapNode(t.inner.RootNode())
}

// Parse parses src and returns a raw tree wrapper. old may be nil.
func (p *Parser) Parse(ctx context.Context, src []byte, old *Tree) (*Tree, error) {
	if p == nil || p.inner == nil {
		return nil, errors.New("nil parser")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var oldInner *sitter.Tree
	if old != nil {
		oldInner = old.inner
	}

	raw := p.inner.ParseWithOptions(func(i int, _ sitter.Point) []byte {
		if i >= len(src) {
			return nil
		}
		return src[i:]
	}, oldInner, &sitter.ParseOptions{
		ProgressCallback: func(_ sitter.ParseState) bool {
			return ctx.Err() != nil
		},
	})
	if err := ctx.Err(); err != nil {
		if raw != nil {
			raw.Close()
		}
		return nil, err
	}
	if raw == nil {
		return nil, errors.New("tree-sitter parse returned nil tree")
	}
	return &Tree{inner: raw}, nil
}
