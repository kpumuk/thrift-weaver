package lint

import (
	"context"

	"github.com/kpumuk/thrift-weaver/internal/syntax"
	itext "github.com/kpumuk/thrift-weaver/internal/text"
)

var invalidSpan = itext.Span{Start: -1, End: -1}

func normalizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func newRecoverableWarning(code syntax.DiagnosticCode, message string, span itext.Span) syntax.Diagnostic {
	return syntax.Diagnostic{
		Code:        code,
		Message:     message,
		Severity:    syntax.SeverityWarning,
		Span:        span,
		Recoverable: true,
	}
}

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

func hasErrorFlags(flags syntax.NodeFlags) bool {
	const errorMask = syntax.NodeFlagError | syntax.NodeFlagMissing | syntax.NodeFlagRecovered
	return flags&errorMask != 0
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
	return invalidSpan
}

func textForSpan(src []byte, sp itext.Span) string {
	if !sp.IsValid() {
		return ""
	}
	start := int(sp.Start)
	end := int(sp.End)
	if start < 0 || end < start || end > len(src) {
		return ""
	}
	return string(src[start:end])
}
