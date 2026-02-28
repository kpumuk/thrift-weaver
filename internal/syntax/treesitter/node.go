package treesitter

import "github.com/kpumuk/thrift-weaver/internal/text"

// RawNode is a parsed syntax node produced by the in-process wasm parser backend.
type RawNode struct {
	Kind      string
	KindID    uint16
	StartByte int
	EndByte   int
	StartRow  int
	StartCol  int
	EndRow    int
	EndCol    int
	IsNamed   bool
	IsError   bool
	IsMissing bool
	IsExtra   bool
	HasError  bool
	Children  []*RawNode
}

// Node wraps a parsed node.
type Node struct {
	inner *RawNode
}

func wrapNode(n *RawNode) Node {
	return Node{inner: n}
}

// IsZero reports whether the node wrapper is empty.
func (n Node) IsZero() bool {
	return n.inner == nil
}

// Inner returns the wrapped raw node pointer.
func (n Node) Inner() *RawNode {
	return n.inner
}

// Kind returns the node kind name.
func (n Node) Kind() string {
	if n.inner == nil {
		return ""
	}
	return n.inner.Kind
}

// KindID returns the node kind id.
func (n Node) KindID() uint16 {
	if n.inner == nil {
		return 0
	}
	return n.inner.KindID
}

// Span returns the node byte span.
func (n Node) Span() text.Span {
	if n.inner == nil {
		return text.Span{}
	}
	return text.Span{
		Start: text.ByteOffset(n.inner.StartByte),
		End:   text.ByteOffset(n.inner.EndByte),
	}
}

// HasError reports whether the node subtree contains parse errors.
func (n Node) HasError() bool {
	return n.inner != nil && n.inner.HasError
}

// IsError reports whether the node itself is an error node.
func (n Node) IsError() bool {
	return n.inner != nil && n.inner.IsError
}

// IsMissing reports whether the node is a missing/recovered node.
func (n Node) IsMissing() bool {
	return n.inner != nil && n.inner.IsMissing
}

// IsNamed reports whether the node is named.
func (n Node) IsNamed() bool {
	return n.inner != nil && n.inner.IsNamed
}

// ChildCount returns the number of children.
func (n Node) ChildCount() uint {
	if n.inner == nil {
		return 0
	}
	return uint(len(n.inner.Children))
}

// NamedChildCount returns the number of named children.
func (n Node) NamedChildCount() uint {
	if n.inner == nil {
		return 0
	}
	count := uint(0)
	for _, child := range n.inner.Children {
		if child.IsNamed {
			count++
		}
	}
	return count
}

// Children returns the node's children in source order.
func (n Node) Children() []Node {
	if n.inner == nil {
		return nil
	}
	out := make([]Node, 0, len(n.inner.Children))
	for _, child := range n.inner.Children {
		out = append(out, wrapNode(child))
	}
	return out
}

// Sexp returns a debug representation.
func (n Node) Sexp() string {
	if n.inner == nil {
		return ""
	}
	return n.inner.Kind
}
