package lint

import (
	"strings"

	"github.com/kpumuk/thrift-weaver/internal/syntax"
	itext "github.com/kpumuk/thrift-weaver/internal/text"
)

type localDeclKind uint8

const (
	localDeclUnknown localDeclKind = iota
	localDeclTypedef
	localDeclEnum
	localDeclSenum
	localDeclStruct
	localDeclUnion
	localDeclException
	localDeclService
)

type localSymbols struct {
	decls map[string]localDeclKind
}

func collectLocalSymbols(tree *syntax.Tree) localSymbols {
	symbols := localSymbols{decls: make(map[string]localDeclKind)}
	if tree == nil {
		return symbols
	}

	for _, id := range tree.TopLevelDeclarationIDs() {
		n := tree.NodeByID(id)
		if n == nil || hasErrorFlags(n.Flags) {
			continue
		}

		kind := localDeclKindForNodeKind(syntax.KindName(n.Kind))
		if kind == localDeclUnknown {
			continue
		}

		name := firstChildTextByKind(tree, n.ID, "identifier")
		if name == "" {
			continue
		}
		if _, exists := symbols.decls[name]; exists {
			continue
		}
		symbols.decls[name] = kind
	}

	return symbols
}

func localDeclKindForNodeKind(kind string) localDeclKind {
	switch kind {
	case "typedef_declaration":
		return localDeclTypedef
	case "enum_definition":
		return localDeclEnum
	case "senum_definition":
		return localDeclSenum
	case "struct_definition":
		return localDeclStruct
	case "union_definition":
		return localDeclUnion
	case "exception_definition":
		return localDeclException
	case "service_definition":
		return localDeclService
	default:
		return localDeclUnknown
	}
}

func (k localDeclKind) isDataType() bool {
	switch k {
	case localDeclUnknown, localDeclService:
		return false
	case localDeclTypedef, localDeclEnum, localDeclSenum, localDeclStruct, localDeclUnion, localDeclException:
		return true
	}
	return false
}

func (k localDeclKind) isExceptionType() bool {
	return k == localDeclException
}

func (k localDeclKind) isService() bool {
	return k == localDeclService
}

func (s localSymbols) lookupLocal(name string) (localDeclKind, bool) {
	if !isLocallyResolvableName(name) {
		return localDeclUnknown, false
	}
	kind, ok := s.decls[name]
	return kind, ok
}

func lookupResolvableScopedIdentifier(tree *syntax.Tree, symbols localSymbols, spanStartNode syntax.NodeID) (itext.Span, localDeclKind, bool) {
	span := firstChildSpanByKind(tree, spanStartNode, "scoped_identifier")
	if !span.IsValid() {
		return invalidSpan, localDeclUnknown, false
	}
	return lookupResolvableScopedIdentifierSpan(tree, symbols, span)
}

func lookupResolvableScopedIdentifierSpan(tree *syntax.Tree, symbols localSymbols, span itext.Span) (itext.Span, localDeclKind, bool) {
	if !span.IsValid() {
		return invalidSpan, localDeclUnknown, false
	}

	name := strings.TrimSpace(textForSpan(tree.Source, span))
	if !isLocallyResolvableName(name) {
		return invalidSpan, localDeclUnknown, false
	}

	kind, ok := symbols.lookupLocal(name)
	return span, kind, ok
}

func isLocallyResolvableName(name string) bool {
	name = strings.TrimSpace(name)
	return name != "" && !strings.Contains(name, ".")
}

func appendDirectTypeDiagnostics(
	tree *syntax.Tree,
	symbols localSymbols,
	parent syntax.NodeID,
	code syntax.DiagnosticCode,
	message string,
	out *[]syntax.Diagnostic,
) {
	for _, childID := range tree.ChildNodeIDs(parent) {
		if !isTypeNodeKind(tree, childID) && !hasNodeKind(tree, childID, "return_type") {
			continue
		}
		appendTypeDiagnostics(tree, symbols, childID, code, message, out)
	}
}

func appendTypeDiagnostics(
	tree *syntax.Tree,
	symbols localSymbols,
	nodeID syntax.NodeID,
	code syntax.DiagnosticCode,
	message string,
	out *[]syntax.Diagnostic,
) {
	n := tree.NodeByID(nodeID)
	if n == nil || hasErrorFlags(n.Flags) {
		return
	}

	switch syntax.KindName(n.Kind) {
	case "base_type":
		return
	case "scoped_identifier":
		span, kind, ok := lookupResolvableScopedIdentifierSpan(tree, symbols, n.Span)
		if ok && kind.isDataType() {
			return
		}
		if !span.IsValid() {
			return
		}
		*out = append(*out, newRecoverableError(code, message, span))
	case "type", "map_type", "list_type", "set_type", "return_type":
		for _, childID := range tree.ChildNodeIDs(nodeID) {
			if !isTypeNodeKind(tree, childID) {
				continue
			}
			appendTypeDiagnostics(tree, symbols, childID, code, message, out)
		}
	}
}

func isTypeNodeKind(tree *syntax.Tree, nodeID syntax.NodeID) bool {
	if tree == nil {
		return false
	}
	n := tree.NodeByID(nodeID)
	if n == nil {
		return false
	}
	switch syntax.KindName(n.Kind) {
	case "type", "base_type", "map_type", "list_type", "set_type", "scoped_identifier":
		return true
	default:
		return false
	}
}

func hasNodeKind(tree *syntax.Tree, nodeID syntax.NodeID, want string) bool {
	if tree == nil || want == "" {
		return false
	}
	n := tree.NodeByID(nodeID)
	return n != nil && syntax.KindName(n.Kind) == want
}

func hasAncestorKind(tree *syntax.Tree, nodeID syntax.NodeID, want string) bool {
	for n := tree.NodeByID(nodeID); n != nil && n.Parent != syntax.NoNode; n = tree.NodeByID(n.Parent) {
		parent := tree.NodeByID(n.Parent)
		if parent == nil {
			return false
		}
		if syntax.KindName(parent.Kind) == want {
			return true
		}
	}
	return false
}
