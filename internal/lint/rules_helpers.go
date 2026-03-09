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
	return newRecoverableDiagnostic(code, message, syntax.SeverityWarning, span)
}

func newRecoverableError(code syntax.DiagnosticCode, message string, span itext.Span) syntax.Diagnostic {
	return newRecoverableDiagnostic(code, message, syntax.SeverityError, span)
}

func newRecoverableDiagnostic(code syntax.DiagnosticCode, message string, severity syntax.Severity, span itext.Span) syntax.Diagnostic {
	return syntax.Diagnostic{
		Code:        code,
		Message:     message,
		Severity:    severity,
		Span:        span,
		Recoverable: true,
	}
}

func duplicateFieldChildSpans(
	tree *syntax.Tree,
	childKind string,
	normalize func(string) string,
) [][]itext.Span {
	if tree == nil || childKind == "" {
		return nil
	}
	if normalize == nil {
		normalize = func(raw string) string { return raw }
	}

	byParent := make(map[syntax.NodeID]map[string][]itext.Span)
	forEachNamedNode(tree, func(n *syntax.Node, kind string) {
		if kind != "field" || hasErrorFlags(n.Flags) {
			return
		}

		childSpan := firstChildSpanByKind(tree, n.ID, childKind)
		if !childSpan.IsValid() {
			return
		}

		key := normalize(textForSpan(tree.Source, childSpan))
		if key == "" {
			return
		}

		if byParent[n.Parent] == nil {
			byParent[n.Parent] = make(map[string][]itext.Span)
		}
		byParent[n.Parent][key] = append(byParent[n.Parent][key], childSpan)
	})

	out := make([][]itext.Span, 0, len(byParent))
	for _, byValue := range byParent {
		for _, spans := range byValue {
			if len(spans) < 2 {
				continue
			}
			out = append(out, spans)
		}
	}
	return out
}

func duplicateFieldChildDiagnostics(
	tree *syntax.Tree,
	childKind string,
	normalize func(string) string,
	code syntax.DiagnosticCode,
	message string,
) []syntax.Diagnostic {
	duplicates := duplicateFieldChildSpans(tree, childKind, normalize)
	out := make([]syntax.Diagnostic, 0, 4)
	for _, spans := range duplicates {
		for _, span := range spans {
			out = append(out, newRecoverableError(code, message, span))
		}
	}
	return out
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
