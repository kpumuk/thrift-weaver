package lsp

import (
	"context"
	"errors"
	"sort"

	"github.com/kpumuk/thrift-weaver/internal/lexer"
	"github.com/kpumuk/thrift-weaver/internal/syntax"
	itext "github.com/kpumuk/thrift-weaver/internal/text"
)

var (
	functionNameBoundaryTokens = map[lexer.TokenKind]struct{}{
		lexer.TokenLParen: {},
	}
	fieldNameBoundaryTokens = map[lexer.TokenKind]struct{}{
		lexer.TokenEqual:  {},
		lexer.TokenLParen: {},
		lexer.TokenComma:  {},
		lexer.TokenSemi:   {},
		lexer.TokenRParen: {},
		lexer.TokenRBrace: {},
	}
	typedefConstNameBoundaryTokens = map[lexer.TokenKind]struct{}{
		lexer.TokenEqual:  {},
		lexer.TokenLParen: {},
		lexer.TokenComma:  {},
		lexer.TokenSemi:   {},
	}
)

// DocumentSymbol handles textDocument/documentSymbol.
func (s *Server) DocumentSymbol(ctx context.Context, p DocumentSymbolParams) ([]DocumentSymbol, error) {
	tree, err := s.queryTreeWithContext(ctx, p.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	return lspDocumentSymbolsFromSyntax(tree)
}

// FoldingRange handles textDocument/foldingRange.
func (s *Server) FoldingRange(ctx context.Context, p FoldingRangeParams) ([]FoldingRange, error) {
	tree, err := s.queryTreeWithContext(ctx, p.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	return lspFoldingRangesFromSyntax(tree)
}

// SelectionRange handles textDocument/selectionRange.
func (s *Server) SelectionRange(ctx context.Context, p SelectionRangeParams) ([]SelectionRange, error) {
	tree, err := s.queryTreeWithContext(ctx, p.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	return lspSelectionRangesFromSyntax(tree, p.Positions)
}

func (s *Server) queryTree(uri string) (*syntax.Tree, error) {
	snap, err := s.formattingSnapshot(uri, nil)
	if err != nil {
		return nil, err
	}
	return snap.Tree, nil
}

func (s *Server) queryTreeWithContext(ctx context.Context, uri string) (*syntax.Tree, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tree, err := s.queryTree(uri)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return tree, nil
}

func lspDocumentSymbolsFromSyntax(tree *syntax.Tree) ([]DocumentSymbol, error) {
	if tree == nil {
		return nil, errors.New("nil syntax tree")
	}

	top := tree.TopLevelDeclarationIDs()
	out := make([]DocumentSymbol, 0, len(top))
	for _, id := range top {
		sym, ok, err := buildDocumentSymbol(tree, id)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		out = append(out, sym)
	}
	return out, nil
}

func buildDocumentSymbol(tree *syntax.Tree, id syntax.NodeID) (DocumentSymbol, bool, error) {
	n := tree.NodeByID(id)
	if n == nil {
		return DocumentSymbol{}, false, nil
	}

	kindName := syntax.KindName(n.Kind)
	kind, ok := lspSymbolKindForNodeKind(kindName)
	if !ok {
		return DocumentSymbol{}, false, nil
	}

	name, selSpan := symbolNameAndSpanForNode(tree, id)
	if name == "" {
		name = kindName
		selSpan = n.Span
	}

	rng, err := lspRangeFromSpan(tree.LineIndex, n.Span)
	if err != nil {
		return DocumentSymbol{}, false, err
	}
	selRange, err := lspRangeFromSpan(tree.LineIndex, selSpan)
	if err != nil {
		return DocumentSymbol{}, false, err
	}

	sym := DocumentSymbol{
		Name:           name,
		Kind:           kind,
		Range:          rng,
		SelectionRange: selRange,
	}

	members := tree.MemberNodeIDs(id)
	if len(members) > 0 {
		children := make([]DocumentSymbol, 0, len(members))
		for _, childID := range members {
			childSym, ok, err := buildDocumentSymbol(tree, childID)
			if err != nil {
				return DocumentSymbol{}, false, err
			}
			if !ok {
				continue
			}
			children = append(children, childSym)
		}
		if len(children) > 0 {
			sym.Children = children
		}
	}

	return sym, true, nil
}

func lspFoldingRangesFromSyntax(tree *syntax.Tree) ([]FoldingRange, error) {
	if tree == nil {
		return nil, errors.New("nil syntax tree")
	}
	li := lineIndexOrBuild(tree)

	out := make([]FoldingRange, 0, 16)
	for i := 1; i < len(tree.Nodes); i++ {
		n := tree.Nodes[i]
		if !isFoldableNodeKind(syntax.KindName(n.Kind)) {
			continue
		}
		fr, ok, err := foldingRangeFromSpan(li, n.Span)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, fr)
		}
	}

	commentRanges, err := commentFoldingRanges(tree, li)
	if err != nil {
		return nil, err
	}
	out = append(out, commentRanges...)

	sort.Slice(out, func(i, j int) bool {
		if out[i].StartLine != out[j].StartLine {
			return out[i].StartLine < out[j].StartLine
		}
		if out[i].EndLine != out[j].EndLine {
			return out[i].EndLine < out[j].EndLine
		}
		if out[i].StartCharacter != out[j].StartCharacter {
			return out[i].StartCharacter < out[j].StartCharacter
		}
		return out[i].EndCharacter < out[j].EndCharacter
	})
	return out, nil
}

func lspSelectionRangesFromSyntax(tree *syntax.Tree, positions []Position) ([]SelectionRange, error) {
	if tree == nil {
		return nil, errors.New("nil syntax tree")
	}
	if len(positions) == 0 {
		return []SelectionRange{}, nil
	}
	li := lineIndexOrBuild(tree)

	out := make([]SelectionRange, 0, len(positions))
	for _, p := range positions {
		off, err := li.UTF16PositionToOffset(itext.UTF16Position{Line: p.Line, Character: p.Character})
		if err != nil {
			return nil, err
		}
		sr, err := selectionRangeForOffset(tree, li, off)
		if err != nil {
			return nil, err
		}
		out = append(out, sr)
	}
	return out, nil
}

func lspSymbolKindForNodeKind(kind string) (int, bool) {
	switch kind {
	case "struct_definition", "union_definition", "exception_definition":
		return 23, true // Struct
	case "enum_definition", "senum_definition":
		return 10, true // Enum
	case "service_definition":
		return 11, true // Interface
	case "typedef_declaration":
		return 26, true // TypeParameter (closest available)
	case "const_declaration":
		return 14, true // Constant
	case "function_definition":
		return 6, true // Method
	case "field":
		return 8, true // Field
	case "enum_value", "senum_value":
		return 22, true // EnumMember
	default:
		return 0, false
	}
}

func symbolNameAndSpanForNode(tree *syntax.Tree, id syntax.NodeID) (string, itext.Span) {
	n := tree.NodeByID(id)
	if n == nil {
		return "", itext.Span{}
	}
	kind := syntax.KindName(n.Kind)
	switch kind {
	case "struct_definition", "union_definition", "exception_definition", "enum_definition", "senum_definition", "service_definition", "enum_value":
		return firstTokenName(tree, n, lexer.TokenIdentifier)
	case "senum_value":
		return firstStringLiteralName(tree, n)
	case "function_definition":
		return lastIdentifierBeforeBoundary(tree, n, functionNameBoundaryTokens)
	case "field":
		return lastIdentifierBeforeBoundary(tree, n, fieldNameBoundaryTokens)
	case "typedef_declaration", "const_declaration":
		return lastIdentifierBeforeBoundary(tree, n, typedefConstNameBoundaryTokens)
	default:
		return "", itext.Span{}
	}
}

func firstTokenName(tree *syntax.Tree, n *syntax.Node, want lexer.TokenKind) (string, itext.Span) {
	for _, ti := range nodeTokenIndexes(tree, n) {
		tok := tree.Tokens[ti]
		if tok.Kind != want {
			continue
		}
		return string(tok.Bytes(tree.Source)), tok.Span
	}
	return "", itext.Span{}
}

func firstStringLiteralName(tree *syntax.Tree, n *syntax.Node) (string, itext.Span) {
	for _, ti := range nodeTokenIndexes(tree, n) {
		tok := tree.Tokens[ti]
		if tok.Kind != lexer.TokenStringLiteral {
			continue
		}
		raw := string(tok.Bytes(tree.Source))
		return trimMatchingQuotes(raw), tok.Span
	}
	return "", itext.Span{}
}

func lastIdentifierBeforeBoundary(tree *syntax.Tree, n *syntax.Node, boundaries map[lexer.TokenKind]struct{}) (string, itext.Span) {
	var (
		candidate    string
		candidatePos itext.Span
		parenDepth   int
		bracketDepth int
		braceDepth   int
		angleDepth   int
	)

	for _, ti := range nodeTokenIndexes(tree, n) {
		tok := tree.Tokens[ti]
		if tok.Kind == lexer.TokenEOF {
			break
		}
		if parenDepth == 0 && bracketDepth == 0 && braceDepth == 0 && angleDepth == 0 {
			if _, stop := boundaries[tok.Kind]; stop {
				break
			}
			if tok.Kind == lexer.TokenIdentifier {
				candidate = string(tok.Bytes(tree.Source))
				candidatePos = tok.Span
			}
		}

		//nolint:exhaustive // only delimiter tokens affect depth tracking here
		switch tok.Kind {
		case lexer.TokenLParen:
			parenDepth++
		case lexer.TokenRParen:
			if parenDepth > 0 {
				parenDepth--
			}
		case lexer.TokenLBracket:
			bracketDepth++
		case lexer.TokenRBracket:
			if bracketDepth > 0 {
				bracketDepth--
			}
		case lexer.TokenLBrace:
			braceDepth++
		case lexer.TokenRBrace:
			if braceDepth > 0 {
				braceDepth--
			}
		case lexer.TokenLAngle:
			angleDepth++
		case lexer.TokenRAngle:
			if angleDepth > 0 {
				angleDepth--
			}
		default:
			// no-op
		}
	}
	return candidate, candidatePos
}

func nodeTokenIndexes(tree *syntax.Tree, n *syntax.Node) []int {
	if tree == nil || n == nil || len(tree.Tokens) == 0 {
		return nil
	}
	start := int(n.FirstToken)
	end := int(n.LastToken)
	if start < 0 {
		start = 0
	}
	if end >= len(tree.Tokens) {
		end = len(tree.Tokens) - 1
	}
	if start > end {
		return nil
	}
	out := make([]int, 0, end-start+1)
	for i := start; i <= end; i++ {
		out = append(out, i)
	}
	return out
}

func lineIndexOrBuild(tree *syntax.Tree) *itext.LineIndex {
	if tree == nil {
		return nil
	}
	if tree.LineIndex != nil {
		return tree.LineIndex
	}
	return itext.NewLineIndex(tree.Source)
}

func trimMatchingQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func isFoldableNodeKind(kind string) bool {
	switch kind {
	case "field_block", "function_block", "enum_block", "const_list", "const_map":
		return true
	default:
		return false
	}
}

func foldingRangeFromSpan(li *itext.LineIndex, sp itext.Span) (FoldingRange, bool, error) {
	rng, err := lspRangeFromSpan(li, sp)
	if err != nil {
		return FoldingRange{}, false, err
	}
	endLine := rng.End.Line
	endChar := rng.End.Character
	if endChar == 0 && endLine > rng.Start.Line {
		endLine--
		endChar = 0
	}
	if endLine <= rng.Start.Line {
		return FoldingRange{}, false, nil
	}
	return FoldingRange{
		StartLine:      rng.Start.Line,
		EndLine:        endLine,
		StartCharacter: rng.Start.Character,
		EndCharacter:   endChar,
	}, true, nil
}

func commentFoldingRanges(tree *syntax.Tree, li *itext.LineIndex) ([]FoldingRange, error) {
	type lineGroup struct {
		startLine int
		endLine   int
	}

	var (
		out       []FoldingRange
		pending   *lineGroup
		flushLine = func() {
			if pending == nil || pending.endLine <= pending.startLine {
				pending = nil
				return
			}
			out = append(out, FoldingRange{StartLine: pending.startLine, EndLine: pending.endLine})
			pending = nil
		}
	)

	for _, tok := range tree.Tokens {
		for _, tr := range tok.Leading {
			//nolint:exhaustive // only comment trivia kinds participate in folding aggregation
			switch tr.Kind {
			case lexer.TriviaLineComment, lexer.TriviaHashComment:
				rng, err := lspRangeFromSpan(li, tr.Span)
				if err != nil {
					return nil, err
				}
				line := rng.Start.Line
				if pending != nil && line == pending.endLine+1 {
					pending.endLine = line
					continue
				}
				flushLine()
				pending = &lineGroup{startLine: line, endLine: line}
			case lexer.TriviaBlockComment, lexer.TriviaDocComment:
				flushLine()
				fr, ok, err := foldingRangeFromSpan(li, tr.Span)
				if err != nil {
					return nil, err
				}
				if ok {
					out = append(out, fr)
				}
			default:
				// Keep line-comment grouping across whitespace/newlines within leading trivia.
			}
		}
	}
	flushLine()
	return out, nil
}

func selectionRangeForOffset(tree *syntax.Tree, li *itext.LineIndex, off itext.ByteOffset) (SelectionRange, error) {
	if tree == nil {
		return SelectionRange{}, errors.New("nil syntax tree")
	}
	ids := namedNodeAncestorChainAtOffset(tree, off)
	if len(ids) == 0 && off > 0 {
		ids = namedNodeAncestorChainAtOffset(tree, off-1)
	}
	if len(ids) == 0 {
		if tree.Root == syntax.NoNode {
			return SelectionRange{}, errors.New("no root node")
		}
		ids = []syntax.NodeID{tree.Root}
	}

	// ids are inner -> outer. Build linked result from outer -> inner.
	var parent *SelectionRange
	for i := len(ids) - 1; i >= 0; i-- {
		n := tree.NodeByID(ids[i])
		if n == nil {
			continue
		}
		rng, err := lspRangeFromSpan(li, n.Span)
		if err != nil {
			return SelectionRange{}, err
		}
		cur := &SelectionRange{Range: rng, Parent: parent}
		parent = cur
	}
	if parent == nil {
		return SelectionRange{}, errors.New("no selection range")
	}
	return *parent, nil
}

func namedNodeAncestorChainAtOffset(tree *syntax.Tree, off itext.ByteOffset) []syntax.NodeID {
	best := syntax.NoNode
	bestLen := -1
	bestDepth := -1

	for i := 1; i < len(tree.Nodes); i++ {
		n := &tree.Nodes[i]
		if !n.Flags.Has(syntax.NodeFlagNamed) {
			continue
		}
		if !spanContainsOffset(n.Span, off) {
			continue
		}
		spanLen := int(n.Span.Len())
		depth := nodeDepth(tree, n.ID)
		if best == syntax.NoNode || spanLen < bestLen || (spanLen == bestLen && depth > bestDepth) {
			best = n.ID
			bestLen = spanLen
			bestDepth = depth
		}
	}
	if best == syntax.NoNode {
		return nil
	}

	out := make([]syntax.NodeID, 0, 8)
	prevSpan := itext.Span{}
	for cur := best; cur != syntax.NoNode; {
		n := tree.NodeByID(cur)
		if n == nil {
			break
		}
		next := n.Parent
		if !n.Flags.Has(syntax.NodeFlagNamed) {
			cur = next
			continue
		}
		if len(out) == 0 || n.Span != prevSpan {
			out = append(out, cur)
			prevSpan = n.Span
		}
		cur = next
	}
	return out
}

func nodeDepth(tree *syntax.Tree, id syntax.NodeID) int {
	depth := 0
	for cur := id; cur != syntax.NoNode; {
		n := tree.NodeByID(cur)
		if n == nil {
			break
		}
		depth++
		cur = n.Parent
	}
	return depth
}

func spanContainsOffset(sp itext.Span, off itext.ByteOffset) bool {
	if !sp.IsValid() {
		return false
	}
	if sp.IsEmpty() {
		return sp.Start == off
	}
	return sp.Contains(off)
}
