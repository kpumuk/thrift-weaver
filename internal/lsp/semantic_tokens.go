//go:build !cgo || !thriftweaver_cgo

package lsp

import (
	"context"
	"errors"
	"math"
	"slices"
	"sort"

	"github.com/kpumuk/thrift-weaver/internal/lexer"
	"github.com/kpumuk/thrift-weaver/internal/syntax"
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

type semanticAbsToken struct {
	line      uint32
	startChar uint32
	length    uint32
	tokenType uint32
	modBits   uint32
}

type semanticSpanKey struct {
	line      uint32
	startChar uint32
	length    uint32
	tokenType uint32
	modBits   uint32
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
	return lspSemanticTokensFromSyntax(tree)
}

func lspSemanticTokensFromSyntax(tree *syntax.Tree) (SemanticTokens, error) {
	if tree == nil {
		return SemanticTokens{}, errors.New("nil syntax tree")
	}
	if tree.LineIndex == nil {
		return SemanticTokens{}, errors.New("nil line index")
	}

	bySpan := make(map[semanticSpanKey]semanticAbsToken, len(tree.Tokens))
	for i := range tree.Tokens {
		tok := tree.Tokens[i]
		for _, tr := range tok.Leading {
			if !isCommentTrivia(tr.Kind) {
				continue
			}
			addTokenSpans(bySpan, tree.Source, tree.LineIndex, tr.Span, "comment", nil)
		}

		tokenType := semanticTokenKindForToken(tree.Tokens, i)
		if tokenType == "" {
			continue
		}
		addTokenSpans(bySpan, tree.Source, tree.LineIndex, tok.Span, tokenType, nil)
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

	return SemanticTokens{Data: encodeSemanticTokens(abs)}, nil
}

func isCommentTrivia(kind lexer.TriviaKind) bool {
	//exhaustive:ignore Trivia whitespace/newline are non-comment cases.
	switch kind {
	case lexer.TriviaHashComment, lexer.TriviaLineComment, lexer.TriviaBlockComment, lexer.TriviaDocComment:
		return true
	default:
		return false
	}
}

func semanticTokenKindForToken(tokens []lexer.Token, i int) string {
	tok := tokens[i]
	//exhaustive:ignore Only token kinds mapped to semantic tokens are handled.
	switch tok.Kind {
	case lexer.TokenStringLiteral:
		return "string"
	case lexer.TokenIntLiteral, lexer.TokenFloatLiteral:
		return "number"
	}

	if isKeywordToken(tok.Kind) {
		if isBuiltinTypeToken(tok.Kind) {
			return "type"
		}
		return "keyword"
	}

	if tok.Kind == lexer.TokenIdentifier && isMethodIdentifier(tokens, i) {
		return "method"
	}
	return ""
}

func isKeywordToken(kind lexer.TokenKind) bool {
	return kind >= lexer.TokenKwInclude && kind <= lexer.TokenKwFalse
}

func isBuiltinTypeToken(kind lexer.TokenKind) bool {
	//exhaustive:ignore Only builtin type token kinds are handled.
	switch kind {
	case lexer.TokenKwVoid, lexer.TokenKwBool, lexer.TokenKwByte, lexer.TokenKwi8, lexer.TokenKwi16,
		lexer.TokenKwi32, lexer.TokenKwi64, lexer.TokenKwDouble, lexer.TokenKwString,
		lexer.TokenKwBinary, lexer.TokenKwUUID, lexer.TokenKwMap, lexer.TokenKwList, lexer.TokenKwSet:
		return true
	default:
		return false
	}
}

func isMethodIdentifier(tokens []lexer.Token, i int) bool {
	if i <= 0 || i+1 >= len(tokens) {
		return false
	}
	if tokens[i].Kind != lexer.TokenIdentifier || tokens[i+1].Kind != lexer.TokenLParen {
		return false
	}

	// Method declarations follow a return type token and then an identifier + '('.
	prev := tokens[i-1].Kind
	return prev == lexer.TokenIdentifier || isBuiltinTypeToken(prev)
}

func addTokenSpans(bySpan map[semanticSpanKey]semanticAbsToken, src []byte, li *itext.LineIndex, sp itext.Span, tokenType string, modifiers []string) {
	typeIdx, ok := semanticTokenTypeIndex[tokenType]
	if !ok {
		return
	}
	modBits := modifierBits(modifiers)
	segments, err := semanticLineSegments(src, sp)
	if err != nil {
		return
	}
	for _, seg := range segments {
		tok, ok := semanticTokenForSpan(li, seg, typeIdx, modBits)
		if !ok {
			continue
		}
		bySpan[semanticSpanKey(tok)] = tok
	}
}

func semanticTokenForSpan(li *itext.LineIndex, sp itext.Span, tokenType uint32, modBits uint32) (semanticAbsToken, bool) {
	if li == nil || !sp.IsValid() || sp.IsEmpty() {
		return semanticAbsToken{}, false
	}
	start, err := li.OffsetToUTF16Position(sp.Start)
	if err != nil {
		return semanticAbsToken{}, false
	}
	end, err := li.OffsetToUTF16Position(sp.End)
	if err != nil {
		return semanticAbsToken{}, false
	}
	if start.Line != end.Line || end.Character <= start.Character {
		return semanticAbsToken{}, false
	}

	line, ok := uint32FromNonNegativeInt(start.Line)
	if !ok {
		return semanticAbsToken{}, false
	}
	startChar, ok := uint32FromNonNegativeInt(start.Character)
	if !ok {
		return semanticAbsToken{}, false
	}
	length, ok := uint32FromNonNegativeInt(end.Character - start.Character)
	if !ok || length == 0 {
		return semanticAbsToken{}, false
	}

	return semanticAbsToken{
		line:      line,
		startChar: startChar,
		length:    length,
		tokenType: tokenType,
		modBits:   modBits,
	}, true
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

func modifierBits(modifiers []string) uint32 {
	modBits := uint32(0)
	for _, mod := range modifiers {
		idx, ok := semanticTokenModIndex[mod]
		if !ok {
			continue
		}
		modBits |= 1 << idx
	}
	return modBits
}

func indexStringsUint32(in []string) map[string]uint32 {
	out := make(map[string]uint32, len(in))
	for i, value := range in {
		idx, ok := uint32FromNonNegativeInt(i)
		if !ok {
			continue
		}
		out[value] = idx
	}
	return out
}

func uint32FromNonNegativeInt(v int) (uint32, bool) {
	if v < 0 || v > math.MaxUint32 {
		return 0, false
	}
	return uint32(v), true
}
