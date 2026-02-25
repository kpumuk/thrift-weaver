package lsp

import (
	"context"
	"errors"
	"math"
	"slices"
	"sort"

	sitter "github.com/tree-sitter/go-tree-sitter"

	treesitterthrift "github.com/kpumuk/thrift-weaver/grammar/tree-sitter-thrift"
	"github.com/kpumuk/thrift-weaver/internal/syntax"
	ts "github.com/kpumuk/thrift-weaver/internal/syntax/treesitter"
	itext "github.com/kpumuk/thrift-weaver/internal/text"
)

var (
	semanticTokenTypes = []string{
		"comment",
		"string",
		"number",
		"keyword",
		"type",
		"method",
		"property",
		"decorator",
		"variable",
	}
	semanticTokenModifiers = []string{
		"declaration",
		"readonly",
		"defaultLibrary",
	}
	semanticTokenTypeIndex = indexStringsUint32(semanticTokenTypes)
	semanticTokenModIndex  = indexStringsUint32(semanticTokenModifiers)
)

type semanticCaptureSpec struct {
	tokenType string
	modifiers []string
	priority  int
}

type semanticAbsToken struct {
	line      uint32
	startChar uint32
	length    uint32
	tokenType uint32
	modBits   uint32
	priority  int
}

type semanticSpanKey struct {
	line      uint32
	startChar uint32
	length    uint32
}

func semanticTokenLegendTypes() []string {
	return slices.Clone(semanticTokenTypes)
}

func semanticTokenLegendModifiers() []string {
	return slices.Clone(semanticTokenModifiers)
}

// SemanticTokensFull handles textDocument/semanticTokens/full.
func (s *Server) SemanticTokensFull(ctx context.Context, p SemanticTokensParams) (SemanticTokens, error) {
	tree, err := s.queryTreeWithContext(ctx, p.TextDocument.URI)
	if err != nil {
		return SemanticTokens{}, err
	}
	return lspSemanticTokensFromSyntax(ctx, tree)
}

func lspSemanticTokensFromSyntax(ctx context.Context, tree *syntax.Tree) (SemanticTokens, error) {
	if tree == nil {
		return SemanticTokens{}, errors.New("nil syntax tree")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return SemanticTokens{}, err
	}

	querySource, err := treesitterthrift.QuerySource("highlights")
	if err != nil {
		return SemanticTokens{}, err
	}
	query, qErr := sitter.NewQuery(ts.Language(), querySource)
	if qErr != nil {
		return SemanticTokens{}, qErr
	}
	defer query.Close()

	parser, err := ts.NewParser()
	if err != nil {
		return SemanticTokens{}, err
	}
	defer parser.Close()
	rawTree, err := parser.Parse(ctx, tree.Source, nil)
	if err != nil {
		return SemanticTokens{}, err
	}
	defer rawTree.Close()

	root := rawTree.Inner().RootNode()
	if root == nil {
		return SemanticTokens{}, errors.New("nil tree-sitter root")
	}

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()
	captures := cursor.Captures(query, root, tree.Source)
	captureNames := query.CaptureNames()
	captureNameCount, ok := uint32FromNonNegativeInt(len(captureNames))
	if !ok {
		return SemanticTokens{}, errors.New("too many query capture names")
	}

	bySpan := make(map[semanticSpanKey]semanticAbsToken)
	for match, captureIndex := captures.Next(); match != nil; match, captureIndex = captures.Next() {
		if err := ctx.Err(); err != nil {
			return SemanticTokens{}, err
		}
		if captureIndex >= uint(len(match.Captures)) {
			continue
		}
		capture := match.Captures[captureIndex]
		if capture.Index >= captureNameCount {
			continue
		}
		spec, ok := semanticCaptureMapping(captureNames[capture.Index])
		if !ok {
			continue
		}
		sp := tsNodeSpan(capture.Node)
		if !sp.IsValid() || sp.IsEmpty() {
			continue
		}
		segments, err := semanticLineSegments(tree.Source, sp)
		if err != nil {
			return SemanticTokens{}, err
		}
		for _, seg := range segments {
			tok, ok, err := semanticTokenForSpan(tree.LineIndex, seg, spec)
			if err != nil {
				return SemanticTokens{}, err
			}
			if !ok {
				continue
			}
			key := semanticSpanKey{line: tok.line, startChar: tok.startChar, length: tok.length}
			if prev, exists := bySpan[key]; exists && prev.priority >= tok.priority {
				continue
			}
			bySpan[key] = tok
		}
	}

	if len(bySpan) == 0 {
		return SemanticTokens{Data: []uint32{}}, nil
	}

	abs := make([]semanticAbsToken, 0, len(bySpan))
	for _, tok := range bySpan {
		abs = append(abs, tok)
	}
	sort.Slice(abs, func(i, j int) bool {
		if abs[i].line != abs[j].line {
			return abs[i].line < abs[j].line
		}
		if abs[i].startChar != abs[j].startChar {
			return abs[i].startChar < abs[j].startChar
		}
		if abs[i].length != abs[j].length {
			return abs[i].length < abs[j].length
		}
		if abs[i].tokenType != abs[j].tokenType {
			return abs[i].tokenType < abs[j].tokenType
		}
		return abs[i].modBits < abs[j].modBits
	})

	data := encodeSemanticTokens(abs)
	return SemanticTokens{Data: data}, nil
}

func semanticCaptureMapping(name string) (semanticCaptureSpec, bool) {
	switch name {
	case "comment":
		return semanticCaptureSpec{tokenType: "comment", priority: 10}, true
	case "string":
		return semanticCaptureSpec{tokenType: "string", priority: 10}, true
	case "string.special":
		return semanticCaptureSpec{tokenType: "string", priority: 20}, true
	case "number", "number.float":
		return semanticCaptureSpec{tokenType: "number", priority: 10}, true
	case "constant.builtin.boolean":
		return semanticCaptureSpec{tokenType: "keyword", modifiers: []string{"defaultLibrary"}, priority: 20}, true
	case "keyword":
		return semanticCaptureSpec{tokenType: "keyword", priority: 5}, true
	case "type.builtin":
		return semanticCaptureSpec{tokenType: "type", modifiers: []string{"defaultLibrary"}, priority: 25}, true
	case "type.definition":
		return semanticCaptureSpec{tokenType: "type", modifiers: []string{"declaration"}, priority: 30}, true
	case "function":
		return semanticCaptureSpec{tokenType: "method", priority: 30}, true
	case "property":
		return semanticCaptureSpec{tokenType: "property", priority: 30}, true
	case "attribute":
		return semanticCaptureSpec{tokenType: "decorator", priority: 30}, true
	case "constant":
		return semanticCaptureSpec{tokenType: "variable", modifiers: []string{"declaration", "readonly"}, priority: 30}, true
	default:
		return semanticCaptureSpec{}, false
	}
}

func semanticTokenForSpan(li *itext.LineIndex, sp itext.Span, spec semanticCaptureSpec) (semanticAbsToken, bool, error) {
	if li == nil {
		return semanticAbsToken{}, false, errors.New("nil line index")
	}
	start, err := li.OffsetToUTF16Position(sp.Start)
	if err != nil {
		return semanticAbsToken{}, false, err
	}
	end, err := li.OffsetToUTF16Position(sp.End)
	if err != nil {
		return semanticAbsToken{}, false, err
	}
	if start.Line != end.Line {
		return semanticAbsToken{}, false, errors.New("semantic token span crosses lines")
	}
	if end.Character <= start.Character {
		return semanticAbsToken{}, false, nil
	}

	typeIdx, ok := semanticTokenTypeIndex[spec.tokenType]
	if !ok {
		return semanticAbsToken{}, false, nil
	}
	modBits := uint32(0)
	for _, mod := range spec.modifiers {
		idx, ok := semanticTokenModIndex[mod]
		if !ok {
			continue
		}
		modBits |= 1 << idx
	}

	line, ok := uint32FromNonNegativeInt(start.Line)
	if !ok {
		return semanticAbsToken{}, false, nil
	}
	startChar, ok := uint32FromNonNegativeInt(start.Character)
	if !ok {
		return semanticAbsToken{}, false, nil
	}
	length, ok := uint32FromNonNegativeInt(end.Character - start.Character)
	if !ok || length == 0 {
		return semanticAbsToken{}, false, nil
	}

	return semanticAbsToken{
		line:      line,
		startChar: startChar,
		length:    length,
		tokenType: typeIdx,
		modBits:   modBits,
		priority:  spec.priority,
	}, true, nil
}

func semanticLineSegments(src []byte, sp itext.Span) ([]itext.Span, error) {
	if !sp.IsValid() {
		return nil, errors.New("invalid span")
	}
	if sp.IsEmpty() {
		return nil, nil
	}
	start := int(sp.Start)
	end := int(sp.End)
	if start < 0 || end < start || end > len(src) {
		return nil, errors.New("span out of bounds")
	}

	out := make([]itext.Span, 0, 2)
	segStart := start
	for i := start; i < end; i++ {
		if src[i] != '\n' {
			continue
		}
		segEnd := i
		if segEnd > segStart && src[segEnd-1] == '\r' {
			segEnd--
		}
		if segEnd > segStart {
			out = append(out, itext.Span{Start: itext.ByteOffset(segStart), End: itext.ByteOffset(segEnd)})
		}
		segStart = i + 1
	}
	if segStart < end {
		out = append(out, itext.Span{Start: itext.ByteOffset(segStart), End: itext.ByteOffset(end)})
	}
	return out, nil
}

func encodeSemanticTokens(tokens []semanticAbsToken) []uint32 {
	if len(tokens) == 0 {
		return []uint32{}
	}
	data := make([]uint32, 0, len(tokens)*5)
	var prevLine uint32
	var prevStart uint32
	for i, tok := range tokens {
		deltaLine := tok.line
		deltaStart := tok.startChar
		if i > 0 {
			deltaLine = tok.line - prevLine
			if deltaLine == 0 {
				deltaStart = tok.startChar - prevStart
			}
		}
		data = append(data, deltaLine, deltaStart, tok.length, tok.tokenType, tok.modBits)
		prevLine = tok.line
		prevStart = tok.startChar
	}
	return data
}

func tsNodeSpan(n sitter.Node) itext.Span {
	start := tsByteOffsetToInt(n.StartByte())
	end := tsByteOffsetToInt(n.EndByte())
	return itext.Span{Start: itext.ByteOffset(start), End: itext.ByteOffset(end)}
}

func tsByteOffsetToInt(v uint) int {
	if uint64(v) > uint64(math.MaxInt) {
		return math.MaxInt
	}
	return int(v)
}

func indexStringsUint32(in []string) map[string]uint32 {
	out := make(map[string]uint32, len(in))
	for i, s := range in {
		out[s] = uint32(i)
	}
	return out
}

func uint32FromNonNegativeInt(v int) (uint32, bool) {
	if v < 0 || uint64(v) > uint64(math.MaxUint32) {
		return 0, false
	}
	return uint32(v), true
}
