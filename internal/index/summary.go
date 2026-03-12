package index

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/kpumuk/thrift-weaver/internal/syntax"
	"github.com/kpumuk/thrift-weaver/internal/text"
)

var dataTypeKinds = []SymbolKind{
	SymbolKindTypedef,
	SymbolKindEnum,
	SymbolKindSenum,
	SymbolKindStruct,
	SymbolKindUnion,
	SymbolKindException,
}

// ParseAndSummarize parses a document and extracts its summary.
func ParseAndSummarize(ctx context.Context, key DocumentKey, in DocumentInput) (*DocumentSummary, error) {
	tree, err := syntax.Parse(ctx, in.Source, syntax.ParseOptions{URI: in.URI, Version: in.Version})
	if err != nil {
		return nil, err
	}
	defer tree.Close()
	return SummarizeTree(key, in, tree)
}

// ParseAndSummarizeWithParser parses a document through a reusable full parser
// and extracts its summary.
func ParseAndSummarizeWithParser(ctx context.Context, parser *syntax.ReusableParser, key DocumentKey, in DocumentInput) (*DocumentSummary, error) {
	if parser == nil {
		return ParseAndSummarize(ctx, key, in)
	}

	tree, err := parser.Parse(ctx, in.Source, syntax.ParseOptions{URI: in.URI, Version: in.Version})
	if err != nil {
		return nil, err
	}
	defer tree.Close()
	return SummarizeTree(key, in, tree)
}

// SummarizeTree extracts a document summary from a parsed syntax tree.
func SummarizeTree(key DocumentKey, in DocumentInput, tree *syntax.Tree) (*DocumentSummary, error) {
	if tree == nil {
		return nil, errors.New("nil syntax tree")
	}
	if key == "" {
		return nil, errors.New("empty document key")
	}

	sum := &DocumentSummary{
		Key:          key,
		URI:          in.URI,
		Version:      in.Version,
		Generation:   in.Generation,
		ContentHash:  sha256.Sum256(tree.Source),
		ParseTainted: len(tree.Diagnostics) > 0,
	}

	for _, id := range tree.TopLevelDeclarationIDs() {
		n := tree.NodeByID(id)
		if n == nil {
			continue
		}

		switch syntax.KindName(n.Kind) {
		case "include_declaration":
			if inc, ok := summarizeInclude(tree, n); ok {
				sum.Includes = append(sum.Includes, inc)
			}
		case "namespace_declaration":
			if ns, ok := summarizeNamespace(tree, n); ok {
				sum.Namespaces = append(sum.Namespaces, ns)
			}
		case "typedef_declaration", "const_declaration", "enum_definition", "senum_definition", "struct_definition", "union_definition", "exception_definition", "service_definition":
			if sym, ok := summarizeSymbol(sum, tree, n); ok {
				sum.Declarations = append(sum.Declarations, sym)
			}
		}
	}

	forEachNamedNode(tree, func(n *syntax.Node, kind string) {
		switch kind {
		case "field":
			if hasAncestorKind(tree, n.ID, "throws_clause") {
				return
			}
			appendDirectTypeReferences(sum, tree, n.ID, dataTypeKinds)
		case "typedef_declaration":
			appendDirectTypeReferences(sum, tree, n.ID, dataTypeKinds)
		case "const_declaration":
			appendDirectTypeReferences(sum, tree, n.ID, dataTypeKinds)
		case "function_definition":
			appendDirectTypeReferences(sum, tree, n.ID, dataTypeKinds)
			appendThrowsReferences(sum, tree, n.ID)
		case "service_definition":
			appendServiceExtendsReference(sum, tree, n.ID)
		}
	})

	sortDiagnostics(sum.Diagnostics)
	return sum, nil
}

func summarizeInclude(tree *syntax.Tree, n *syntax.Node) (IncludeEdge, bool) {
	if tree == nil || n == nil {
		return IncludeEdge{}, false
	}
	span := firstChildSpanByKind(tree, n.ID, "string_literal")
	raw := strings.TrimSpace(textForSpan(tree.Source, span))
	if raw == "" {
		return IncludeEdge{}, false
	}
	unquoted, err := strconv.Unquote(raw)
	if err != nil {
		unquoted = strings.Trim(raw, `"'`)
	}
	alias := strings.TrimSuffix(path.Base(unquoted), path.Ext(unquoted))
	return IncludeEdge{
		RawPath: raw,
		Alias:   alias,
		Span:    span,
		Status:  IncludeStatusUnknown,
	}, true
}

func summarizeNamespace(tree *syntax.Tree, n *syntax.Node) (NamespaceDecl, bool) {
	if tree == nil || n == nil {
		return NamespaceDecl{}, false
	}
	scope := strings.TrimSpace(textForSpan(tree.Source, firstChildSpanByKind(tree, n.ID, "namespace_named_scope")))
	target := strings.TrimSpace(textForSpan(tree.Source, firstChildSpanByKind(tree, n.ID, "namespace_target")))
	if scope == "" && target == "" {
		return NamespaceDecl{}, false
	}
	return NamespaceDecl{
		Scope:  scope,
		Target: target,
		Span:   n.Span,
	}, true
}

func summarizeSymbol(sum *DocumentSummary, tree *syntax.Tree, n *syntax.Node) (Symbol, bool) {
	if tree == nil || n == nil || hasErrorFlags(n.Flags) {
		return Symbol{}, false
	}
	nameSpan := firstChildSpanByKind(tree, n.ID, "identifier")
	name := strings.TrimSpace(textForSpan(tree.Source, nameSpan))
	if name == "" {
		return Symbol{}, false
	}
	kind, ok := symbolKindForNodeKind(syntax.KindName(n.Kind))
	if !ok {
		return Symbol{}, false
	}
	return Symbol{
		ID:       newSymbolID(sum.Key, kind, nameSpan),
		Key:      sum.Key,
		URI:      sum.URI,
		Kind:     kind,
		Name:     name,
		QName:    QualifiedName{DeclaringURI: sum.URI, Name: name},
		NameSpan: nameSpan,
		FullSpan: n.Span,
	}, true
}

func appendDirectTypeReferences(sum *DocumentSummary, tree *syntax.Tree, parent syntax.NodeID, expected []SymbolKind) {
	for _, childID := range tree.ChildNodeIDs(parent) {
		if !isTypeNodeKind(tree, childID) && !hasNodeKind(tree, childID, "return_type") {
			continue
		}
		appendTypeReferences(sum, tree, childID, ReferenceKindType, expected)
	}
}

func appendTypeReferences(sum *DocumentSummary, tree *syntax.Tree, nodeID syntax.NodeID, context ReferenceKind, expected []SymbolKind) {
	n := tree.NodeByID(nodeID)
	if n == nil {
		return
	}
	switch syntax.KindName(n.Kind) {
	case "base_type":
		return
	case "scoped_identifier":
		ref := newReferenceSite(sum, tree, n, context, expected)
		sum.References = append(sum.References, ref)
	case "type", "map_type", "list_type", "set_type", "return_type":
		for _, childID := range tree.ChildNodeIDs(nodeID) {
			if !isTypeNodeKind(tree, childID) {
				continue
			}
			appendTypeReferences(sum, tree, childID, context, expected)
		}
	}
}

func appendServiceExtendsReference(sum *DocumentSummary, tree *syntax.Tree, serviceID syntax.NodeID) {
	if span := firstChildSpanByKind(tree, serviceID, "scoped_identifier"); span.IsValid() {
		n := tree.NodeByID(firstChildNodeIDBySpan(tree, serviceID, span))
		if n != nil {
			sum.References = append(sum.References, newReferenceSite(sum, tree, n, ReferenceKindServiceExtends, []SymbolKind{SymbolKindService}))
		}
	}
}

func appendThrowsReferences(sum *DocumentSummary, tree *syntax.Tree, functionID syntax.NodeID) {
	throwsID, ok := firstChildNodeIDByKind(tree, functionID, "throws_clause")
	if !ok {
		return
	}
	paramsID, ok := firstChildNodeIDByKind(tree, throwsID, "parameter_list")
	if !ok {
		return
	}
	for _, fieldID := range tree.ChildNodeIDs(paramsID) {
		field := tree.NodeByID(fieldID)
		if field == nil || syntax.KindName(field.Kind) != "field" {
			continue
		}
		typeID, ok := firstDirectTypeChildNodeID(tree, field.ID)
		if !ok {
			continue
		}
		typeID, ok = unwrapTypeNodeID(tree, typeID)
		if !ok {
			continue
		}
		typeNode := tree.NodeByID(typeID)
		if typeNode == nil || syntax.KindName(typeNode.Kind) != "scoped_identifier" {
			continue
		}
		sum.References = append(sum.References, newReferenceSite(sum, tree, typeNode, ReferenceKindThrowsType, []SymbolKind{SymbolKindException}))
	}
}

func newReferenceSite(sum *DocumentSummary, tree *syntax.Tree, node *syntax.Node, context ReferenceKind, expected []SymbolKind) ReferenceSite {
	raw := strings.TrimSpace(textForSpan(tree.Source, node.Span))
	qualifier, name := splitScopedIdentifier(raw)
	return ReferenceSite{
		ID:            newReferenceSiteID(sum.Key, context, node.Span),
		URI:           sum.URI,
		Context:       context,
		RawText:       raw,
		Qualifier:     qualifier,
		Name:          name,
		Span:          node.Span,
		ExpectedKinds: cloneKinds(expected),
		Tainted:       nodeOrAncestorHasError(tree, node.ID),
		Binding:       BindingResult{Status: BindingStatusUnknown},
	}
}

func symbolKindForNodeKind(kind string) (SymbolKind, bool) {
	switch kind {
	case "typedef_declaration":
		return SymbolKindTypedef, true
	case "const_declaration":
		return SymbolKindConst, true
	case "enum_definition":
		return SymbolKindEnum, true
	case "senum_definition":
		return SymbolKindSenum, true
	case "struct_definition":
		return SymbolKindStruct, true
	case "union_definition":
		return SymbolKindUnion, true
	case "exception_definition":
		return SymbolKindException, true
	case "service_definition":
		return SymbolKindService, true
	default:
		return "", false
	}
}

func cloneKinds(in []SymbolKind) []SymbolKind {
	out := make([]SymbolKind, len(in))
	copy(out, in)
	return out
}

func splitScopedIdentifier(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	parts := strings.Split(raw, ".")
	if len(parts) < 2 {
		return "", raw
	}
	return parts[0], strings.Join(parts[1:], ".")
}

func newSymbolID(key DocumentKey, kind SymbolKind, span text.Span) SymbolID {
	return SymbolID(fmt.Sprintf("%s:%s:%d:%d", key, kind, span.Start, span.End))
}

func newReferenceSiteID(key DocumentKey, kind ReferenceKind, span text.Span) ReferenceSiteID {
	return ReferenceSiteID(fmt.Sprintf("%s:%s:%d:%d", key, kind, span.Start, span.End))
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

func nodeOrAncestorHasError(tree *syntax.Tree, id syntax.NodeID) bool {
	for node := tree.NodeByID(id); node != nil; node = tree.NodeByID(node.Parent) {
		if hasErrorFlags(node.Flags) {
			return true
		}
		if node.Parent == syntax.NoNode {
			return false
		}
	}
	return false
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

func hasNodeKind(tree *syntax.Tree, nodeID syntax.NodeID, want string) bool {
	n := tree.NodeByID(nodeID)
	return n != nil && syntax.KindName(n.Kind) == want
}

func firstChildSpanByKind(tree *syntax.Tree, parent syntax.NodeID, want string) text.Span {
	for _, id := range tree.ChildNodeIDs(parent) {
		child := tree.NodeByID(id)
		if child == nil || syntax.KindName(child.Kind) != want {
			continue
		}
		return child.Span
	}
	return text.Span{Start: -1, End: -1}
}

func firstChildNodeIDByKind(tree *syntax.Tree, parent syntax.NodeID, want string) (syntax.NodeID, bool) {
	for _, id := range tree.ChildNodeIDs(parent) {
		child := tree.NodeByID(id)
		if child == nil || syntax.KindName(child.Kind) != want {
			continue
		}
		return id, true
	}
	return syntax.NoNode, false
}

func firstChildNodeIDBySpan(tree *syntax.Tree, parent syntax.NodeID, span text.Span) syntax.NodeID {
	for _, id := range tree.ChildNodeIDs(parent) {
		child := tree.NodeByID(id)
		if child == nil {
			continue
		}
		if child.Span == span {
			return id
		}
	}
	return syntax.NoNode
}

func firstDirectTypeChildNodeID(tree *syntax.Tree, parent syntax.NodeID) (syntax.NodeID, bool) {
	for _, id := range tree.ChildNodeIDs(parent) {
		if !isTypeNodeKind(tree, id) {
			continue
		}
		return id, true
	}
	return syntax.NoNode, false
}

func unwrapTypeNodeID(tree *syntax.Tree, nodeID syntax.NodeID) (syntax.NodeID, bool) {
	current := nodeID
	for {
		n := tree.NodeByID(current)
		if n == nil {
			return syntax.NoNode, false
		}
		kind := syntax.KindName(n.Kind)
		if kind != "type" && kind != "return_type" {
			return current, true
		}
		childID, ok := firstDirectTypeChildNodeID(tree, current)
		if !ok {
			return syntax.NoNode, false
		}
		current = childID
	}
}

func isTypeNodeKind(tree *syntax.Tree, nodeID syntax.NodeID) bool {
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

func textForSpan(src []byte, sp text.Span) string {
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
