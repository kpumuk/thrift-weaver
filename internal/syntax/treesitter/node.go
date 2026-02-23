package treesitter

import (
	"math"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/kpumuk/thrift-weaver/internal/text"
)

// Node wraps a tree-sitter node.
type Node struct {
	inner *sitter.Node
}

func wrapNode(n *sitter.Node) Node {
	return Node{inner: n}
}

// IsZero reports whether the node wrapper is empty.
func (n Node) IsZero() bool {
	return n.inner == nil
}

// Inner returns the wrapped go-tree-sitter node pointer.
func (n Node) Inner() *sitter.Node {
	return n.inner
}

// Kind returns the tree-sitter node kind name.
func (n Node) Kind() string {
	if n.inner == nil {
		return ""
	}
	return n.inner.Kind()
}

// KindID returns the tree-sitter node kind id.
func (n Node) KindID() uint16 {
	if n.inner == nil {
		return 0
	}
	return n.inner.KindId()
}

// Span returns the node byte span.
func (n Node) Span() text.Span {
	if n.inner == nil {
		return text.Span{}
	}
	return text.Span{
		Start: byteOffsetFromTS(n.inner.StartByte()),
		End:   byteOffsetFromTS(n.inner.EndByte()),
	}
}

// HasError reports whether the node subtree contains parse errors.
func (n Node) HasError() bool {
	return n.inner != nil && n.inner.HasError()
}

// IsError reports whether the node itself is an error node.
func (n Node) IsError() bool {
	return n.inner != nil && n.inner.IsError()
}

// IsMissing reports whether the node is a missing/recovered node.
func (n Node) IsMissing() bool {
	return n.inner != nil && n.inner.IsMissing()
}

// IsNamed reports whether the node is named.
func (n Node) IsNamed() bool {
	return n.inner != nil && n.inner.IsNamed()
}

// ChildCount returns the number of children.
func (n Node) ChildCount() uint {
	if n.inner == nil {
		return 0
	}
	return n.inner.ChildCount()
}

// NamedChildCount returns the number of named children.
func (n Node) NamedChildCount() uint {
	if n.inner == nil {
		return 0
	}
	return n.inner.NamedChildCount()
}

// Children returns the node's children in source order.
func (n Node) Children() []Node {
	if n.inner == nil {
		return nil
	}
	cursor := n.inner.Walk()
	defer cursor.Close()
	children := n.inner.Children(cursor)
	out := make([]Node, 0, len(children))
	for i := range children {
		child := children[i]
		out = append(out, Node{inner: &child})
	}
	return out
}

// Sexp returns the node's S-expression.
func (n Node) Sexp() string {
	if n.inner == nil {
		return ""
	}
	return n.inner.ToSexp()
}

func byteOffsetFromTS(v uint) text.ByteOffset {
	if uint64(v) > uint64(math.MaxInt) {
		return text.ByteOffset(math.MaxInt)
	}
	return text.ByteOffset(int(v))
}
