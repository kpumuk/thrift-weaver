package lint

import (
	"github.com/kpumuk/thrift-weaver/internal/syntax"
	itext "github.com/kpumuk/thrift-weaver/internal/text"
)

func forEachNamedNode(tree *syntax.Tree, fn func(n *syntax.Node, kind string)) {
	if tree == nil || fn == nil {
		return
	}
	for i := 1; i < len(tree.Nodes); i++ {
		n := &tree.Nodes[i]
		if !n.Flags.Has(syntax.NodeFlagNamed) {
			continue
		}
		fn(n, syntax.KindName(n.Kind))
	}
}

func hasAnyNodeFlag(flags syntax.NodeFlags, mask syntax.NodeFlags) bool {
	return flags&mask != 0
}

func hasChildByKind(tree *syntax.Tree, parent syntax.NodeID, want string) bool {
	ids := tree.ChildNodeIDs(parent)
	for _, id := range ids {
		child := tree.NodeByID(id)
		if child == nil || syntax.KindName(child.Kind) != want {
			continue
		}
		return true
	}
	return false
}

func firstChildSpanByKind(tree *syntax.Tree, parent syntax.NodeID, want string) itext.Span {
	ids := tree.ChildNodeIDs(parent)
	for _, id := range ids {
		child := tree.NodeByID(id)
		if child == nil || syntax.KindName(child.Kind) != want {
			continue
		}
		return child.Span
	}
	return itext.Span{}
}
