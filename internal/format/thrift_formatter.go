package format

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/kpumuk/thrift-weaver/internal/lexer"
	"github.com/kpumuk/thrift-weaver/internal/syntax"
)

type formatHints struct {
	topLevelStart  map[uint32]int
	topLevelBreaks map[uint32]int
	memberStart    map[uint32]struct{}
	wrapListStart  map[uint32]struct{}
	wrapListOpen   map[uint32]struct{}
	wrapListClose  map[uint32]struct{}
	declBlockOpen  map[uint32]declBlockSpec
	declBlockClose map[uint32]declBlockSpec
}

func (h formatHints) topLevelBreakCount(tok uint32) int {
	if breaks := h.topLevelBreaks[tok]; breaks > 0 {
		return breaks
	}
	return 2
}

type declBlockSpec struct {
	CloseToken uint32
	HasMembers bool
}

type tokenWriter struct {
	buf           bytes.Buffer
	newline       string
	indent        string
	maxBlankLines int
	atLineStart   bool
	pendingSpace  bool
	pendingBreaks int
}

func newTokenWriter(newline, indent string, maxBlankLines int) *tokenWriter {
	return &tokenWriter{
		newline:       newline,
		indent:        indent,
		maxBlankLines: maxBlankLines,
		atLineStart:   true,
	}
}

func (w *tokenWriter) requestSpace() {
	if w.atLineStart || w.pendingBreaks > 0 {
		return
	}
	w.pendingSpace = true
}

func (w *tokenWriter) requestBreaks(lines int) {
	if lines <= 0 {
		return
	}
	if lines > w.pendingBreaks {
		w.pendingBreaks = lines
	}
	w.pendingSpace = false
}

func (w *tokenWriter) addBreak() {
	w.pendingBreaks++
	w.pendingSpace = false
}

func (w *tokenWriter) flushBeforeContent(indentLevel int) {
	if w.pendingBreaks > 0 {
		w.buf.WriteString(repeatString(w.newline, w.cappedBreaks()))
		w.atLineStart = true
		w.pendingBreaks = 0
	}
	if w.atLineStart {
		if indentLevel > 0 {
			w.buf.WriteString(repeatString(w.indent, indentLevel))
		}
		w.atLineStart = false
		w.pendingSpace = false
		return
	}
	if w.pendingSpace {
		w.buf.WriteByte(' ')
		w.pendingSpace = false
	}
}

func (w *tokenWriter) writeRaw(indentLevel int, raw []byte) {
	if len(raw) == 0 {
		return
	}
	w.flushBeforeContent(indentLevel)
	w.buf.Write(raw)
	w.pendingSpace = false
	w.atLineStart = endsWithLineBreak(raw)
}

func (w *tokenWriter) emitLeadingTrivia(src []byte, trivia []lexer.Trivia, indentLevel int, preserveNewlines bool) error {
	hasComment := triviaHasComment(trivia)
	for _, tr := range trivia {
		switch tr.Kind {
		case lexer.TriviaWhitespace:
			if hasComment {
				w.requestSpace()
			}
		case lexer.TriviaNewline:
			if hasComment || preserveNewlines {
				w.addBreak()
			}
		case lexer.TriviaLineComment, lexer.TriviaHashComment, lexer.TriviaBlockComment, lexer.TriviaDocComment:
			raw := tr.Bytes(src)
			if raw == nil {
				return fmt.Errorf("invalid trivia span %s", tr.Span)
			}
			w.writeRaw(indentLevel, raw)
		default:
			// Ignore unknown trivia conservatively.
		}
	}
	return nil
}

func (w *tokenWriter) finish() []byte {
	if w.pendingBreaks > 0 {
		w.buf.WriteString(repeatString(w.newline, w.cappedBreaks()))
		w.pendingBreaks = 0
		w.pendingSpace = false
		w.atLineStart = true
	}
	return w.buf.Bytes()
}

func (w *tokenWriter) cappedBreaks() int {
	return min(w.pendingBreaks, max(w.maxBlankLines, 1))
}

func formatSyntaxTree(tree *syntax.Tree, opts Options, policy SourcePolicy) ([]byte, error) {
	if tree == nil {
		return nil, errors.New("nil syntax tree")
	}
	if len(tree.Tokens) == 0 || tree.Root == syntax.NoNode {
		return bytes.Clone(tree.Source), nil
	}

	hints := collectFormatHints(tree, opts)
	w := newTokenWriter(policy.Newline, opts.Indent, opts.MaxBlankLines)
	if policy.HasBOM {
		w.buf.WriteString(utf8BOM)
		w.atLineStart = false
	}

	indentLevel := 0
	var prevKind lexer.TokenKind
	var havePrev bool

	for i := range tree.Tokens {
		idx := uint32(i)
		tok := tree.Tokens[i]
		if tok.Kind == lexer.TokenEOF {
			if err := w.emitLeadingTrivia(tree.Source, tok.Leading, indentLevel, true); err != nil {
				return nil, err
			}
			break
		}
		leadingHasComment := triviaHasComment(tok.Leading)

		if spec, ok := hints.declBlockClose[idx]; ok && spec.HasMembers {
			if indentLevel > 0 {
				indentLevel--
			}
			if !leadingHasComment {
				w.requestBreaks(1)
			}
		}
		if _, ok := hints.wrapListClose[idx]; ok {
			if indentLevel > 0 {
				indentLevel--
			}
			if !leadingHasComment {
				w.requestBreaks(1)
			}
		}
		if order, ok := hints.topLevelStart[idx]; ok && order > 0 {
			breaks := hints.topLevelBreakCount(idx)
			if leadingHasComment {
				breaks = max(breaks-leadingNewlinesBeforeFirstComment(tok.Leading), 0)
			}
			w.requestBreaks(breaks)
		} else if !leadingHasComment {
			if _, ok := hints.memberStart[idx]; ok {
				w.requestBreaks(1)
			} else if _, ok := hints.wrapListStart[idx]; ok {
				w.requestBreaks(1)
			}
		}

		if err := w.emitLeadingTrivia(tree.Source, tok.Leading, indentLevel, false); err != nil {
			return nil, err
		}
		if havePrev && shouldInsertSpace(prevKind, tok.Kind) {
			w.requestSpace()
		}

		raw := tok.Bytes(tree.Source)
		if raw == nil {
			return nil, fmt.Errorf("invalid token span %s at index %d", tok.Span, i)
		}
		w.writeRaw(indentLevel, raw)

		if spec, ok := hints.declBlockOpen[idx]; ok && spec.HasMembers {
			indentLevel++
			if !nextTokenHasLeadingComment(tree.Tokens, i+1) {
				w.requestBreaks(1)
			}
		}
		if _, ok := hints.wrapListOpen[idx]; ok {
			indentLevel++
			if !nextTokenHasLeadingComment(tree.Tokens, i+1) {
				w.requestBreaks(1)
			}
		}

		prevKind = tok.Kind
		havePrev = true
	}

	return bytes.Clone(w.finish()), nil
}

func collectFormatHints(tree *syntax.Tree, opts Options) formatHints {
	hints := formatHints{
		topLevelStart:  make(map[uint32]int),
		topLevelBreaks: make(map[uint32]int),
		memberStart:    make(map[uint32]struct{}),
		wrapListStart:  make(map[uint32]struct{}),
		wrapListOpen:   make(map[uint32]struct{}),
		wrapListClose:  make(map[uint32]struct{}),
		declBlockOpen:  make(map[uint32]declBlockSpec),
		declBlockClose: make(map[uint32]declBlockSpec),
	}

	prevTopKind := ""
	for order, id := range tree.TopLevelDeclarationIDs() {
		n := tree.NodeByID(id)
		if n == nil {
			continue
		}
		kind := syntax.KindName(n.Kind)
		hints.topLevelStart[n.FirstToken] = order
		if order > 0 {
			breaks := topLevelBreakCount(prevTopKind, kind)
			if prevTopKind == "const_declaration" && kind == "const_declaration" && int(n.FirstToken) < len(tree.Tokens) {
				breaks = max(leadingNewlinesBeforeFirstComment(tree.Tokens[n.FirstToken].Leading), 1)
			}
			hints.topLevelBreaks[n.FirstToken] = breaks
		}
		prevTopKind = kind
	}

	for i := 1; i < len(tree.Nodes); i++ {
		id := syntax.NodeID(i)
		for _, memberID := range tree.MemberNodeIDs(id) {
			member := tree.NodeByID(memberID)
			if member == nil {
				continue
			}
			hints.memberStart[member.FirstToken] = struct{}{}
			if syntax.KindName(member.Kind) == "function_definition" {
				addWrappedFunctionSignatureHints(tree, opts, &hints, member)
			}
		}
	}

	for i := 1; i < len(tree.Nodes); i++ {
		n := &tree.Nodes[i]
		switch syntax.KindName(n.Kind) {
		case "field_block", "function_block", "enum_block":
			if spec, ok := declBlockSpecFromBraces(tree, n.FirstToken, n.LastToken, countNamedChildNodes(tree, n.ID)); ok {
				hints.declBlockOpen[n.FirstToken] = spec
				hints.declBlockClose[spec.CloseToken] = spec
			}
		case "senum_definition":
			memberCount := len(tree.MemberNodeIDs(n.ID))
			if openTok, spec, ok := declBlockSpecFromNodeTokenScan(tree, n.FirstToken, n.LastToken, memberCount); ok {
				hints.declBlockOpen[openTok] = spec
				hints.declBlockClose[spec.CloseToken] = spec
			}
		}
	}

	return hints
}

func addWrappedFunctionSignatureHints(tree *syntax.Tree, opts Options, hints *formatHints, fn *syntax.Node) {
	if tree == nil || hints == nil || fn == nil {
		return
	}
	if opts.LineWidth <= 0 {
		return
	}
	if !functionDefinitionExceedsLineWidth(tree, fn, opts) {
		return
	}

	for _, childID := range tree.ChildNodeIDs(fn.ID) {
		child := tree.NodeByID(childID)
		if child == nil {
			continue
		}
		switch syntax.KindName(child.Kind) {
		case "parameter_list":
			addWrappedParameterListHints(tree, hints, child)
		case "throws_clause":
			for _, throwsChildID := range tree.ChildNodeIDs(child.ID) {
				throwsChild := tree.NodeByID(throwsChildID)
				if throwsChild == nil || syntax.KindName(throwsChild.Kind) != "parameter_list" {
					continue
				}
				addWrappedParameterListHints(tree, hints, throwsChild)
			}
		}
	}
}

func addWrappedParameterListHints(tree *syntax.Tree, hints *formatHints, list *syntax.Node) {
	if tree == nil || hints == nil || list == nil {
		return
	}

	openTok, closeTok, ok := parenListTokenBounds(tree, list)
	if !ok {
		return
	}

	hints.wrapListOpen[openTok] = struct{}{}
	hints.wrapListClose[closeTok] = struct{}{}
	for _, childID := range tree.ChildNodeIDs(list.ID) {
		child := tree.NodeByID(childID)
		if child == nil || syntax.KindName(child.Kind) != "field" {
			continue
		}
		hints.wrapListStart[child.FirstToken] = struct{}{}
	}
}

func parenListTokenBounds(tree *syntax.Tree, list *syntax.Node) (uint32, uint32, bool) {
	if tree == nil || list == nil {
		return 0, 0, false
	}
	if int(list.FirstToken) >= len(tree.Tokens) || int(list.LastToken) >= len(tree.Tokens) || list.LastToken < list.FirstToken {
		return 0, 0, false
	}
	open := -1
	closeTok := -1
	for i := list.FirstToken; i <= list.LastToken && int(i) < len(tree.Tokens); i++ {
		//exhaustive:ignore We intentionally care only about the paren delimiters here.
		switch tree.Tokens[i].Kind {
		case lexer.TokenLParen:
			if open == -1 {
				open = int(i)
			}
		case lexer.TokenRParen:
			closeTok = int(i)
		default:
			// Ignore non-paren tokens while scanning the parameter list bounds.
		}
	}
	if open == -1 || closeTok == -1 || closeTok < open {
		return 0, 0, false
	}
	return uint32(open), uint32(closeTok), true
}

func functionDefinitionExceedsLineWidth(tree *syntax.Tree, fn *syntax.Node, opts Options) bool {
	if tree == nil || fn == nil || opts.LineWidth <= 0 {
		return false
	}
	flat, ok := flatTokenText(tree, fn.FirstToken, fn.LastToken)
	if !ok {
		return false
	}
	indentWidth := len(opts.Indent) // service members are rendered at indent level 1 in v1 formatter.
	return indentWidth+len(flat) > opts.LineWidth
}

func flatTokenText(tree *syntax.Tree, first, last uint32) (string, bool) {
	if tree == nil || int(first) >= len(tree.Tokens) || int(last) >= len(tree.Tokens) || last < first {
		return "", false
	}

	var b strings.Builder
	var prev lexer.TokenKind
	havePrev := false

	for i := first; i <= last && int(i) < len(tree.Tokens); i++ {
		tok := tree.Tokens[i]
		if tok.Kind == lexer.TokenEOF {
			break
		}
		if i != first && triviaHasComment(tok.Leading) {
			return "", false
		}
		if havePrev && shouldInsertSpace(prev, tok.Kind) {
			b.WriteByte(' ')
		}
		raw := tok.Bytes(tree.Source)
		if raw == nil {
			return "", false
		}
		b.Write(raw)
		prev = tok.Kind
		havePrev = true
	}

	return b.String(), true
}

func topLevelBreakCount(prevKind, currKind string) int {
	if sameTopLevelDirectiveGroup(prevKind, currKind) {
		return 1
	}
	return 2
}

func sameTopLevelDirectiveGroup(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return topLevelDirectiveGroup(a) != "" && topLevelDirectiveGroup(a) == topLevelDirectiveGroup(b)
}

func topLevelDirectiveGroup(kind string) string {
	switch kind {
	case "include_declaration", "cpp_include_declaration":
		return "include"
	case "namespace_declaration":
		return "namespace"
	case "typedef_declaration":
		return "typedef"
	default:
		return ""
	}
}

func countNamedChildNodes(tree *syntax.Tree, parent syntax.NodeID) int {
	count := 0
	for _, id := range tree.ChildNodeIDs(parent) {
		n := tree.NodeByID(id)
		if n != nil && n.Flags.Has(syntax.NodeFlagNamed) {
			count++
		}
	}
	return count
}

func declBlockSpecFromBraces(tree *syntax.Tree, openTok, closeTok uint32, memberCount int) (declBlockSpec, bool) {
	if int(openTok) >= len(tree.Tokens) || int(closeTok) >= len(tree.Tokens) {
		return declBlockSpec{}, false
	}
	if tree.Tokens[openTok].Kind != lexer.TokenLBrace || tree.Tokens[closeTok].Kind != lexer.TokenRBrace {
		return declBlockSpec{}, false
	}
	return declBlockSpec{CloseToken: closeTok, HasMembers: memberCount > 0}, true
}

func declBlockSpecFromNodeTokenScan(tree *syntax.Tree, first, last uint32, memberCount int) (uint32, declBlockSpec, bool) {
	if int(first) >= len(tree.Tokens) || int(last) >= len(tree.Tokens) || last < first {
		return 0, declBlockSpec{}, false
	}
	open := -1
	closeTok := -1
	for i := first; i <= last && int(i) < len(tree.Tokens); i++ {
		kind := tree.Tokens[i].Kind
		if kind == lexer.TokenLBrace {
			if open == -1 {
				open = int(i)
			}
			continue
		}
		if kind == lexer.TokenRBrace {
			closeTok = int(i)
		}
	}
	if open == -1 || closeTok == -1 || closeTok < open {
		return 0, declBlockSpec{}, false
	}
	return uint32(open), declBlockSpec{CloseToken: uint32(closeTok), HasMembers: memberCount > 0}, true
}

func shouldInsertSpace(prev, cur lexer.TokenKind) bool {
	switch {
	case cur == lexer.TokenComma || cur == lexer.TokenSemi || cur == lexer.TokenColon:
		return false
	case cur == lexer.TokenRParen || cur == lexer.TokenRBracket || cur == lexer.TokenRBrace || cur == lexer.TokenRAngle:
		return false
	case cur == lexer.TokenDot:
		return false
	case prev == lexer.TokenDot:
		return false
	case prev == lexer.TokenEqual || cur == lexer.TokenEqual:
		return true
	case prev == lexer.TokenColon || prev == lexer.TokenComma || prev == lexer.TokenSemi:
		return true
	case cur == lexer.TokenLParen || cur == lexer.TokenLBracket || cur == lexer.TokenLAngle:
		return prev == lexer.TokenRBrace
	case prev == lexer.TokenLParen || prev == lexer.TokenLBracket || prev == lexer.TokenLAngle || prev == lexer.TokenLBrace:
		return false
	case cur == lexer.TokenLBrace:
		return isWordLike(prev) || isClosingDelimiter(prev)
	case prev == lexer.TokenPlus || prev == lexer.TokenMinus:
		return false
	case cur == lexer.TokenPlus || cur == lexer.TokenMinus:
		return false
	case isWordLike(prev) && isWordLike(cur):
		return true
	case isClosingDelimiter(prev) && isWordLike(cur):
		return true
	case prev == lexer.TokenStar || cur == lexer.TokenStar:
		return true
	default:
		return false
	}
}

func isWordLike(k lexer.TokenKind) bool {
	switch {
	case k == lexer.TokenIdentifier, k == lexer.TokenIntLiteral, k == lexer.TokenFloatLiteral, k == lexer.TokenStringLiteral:
		return true
	case k == lexer.TokenError:
		return true
	case k >= lexer.TokenKwInclude && k <= lexer.TokenKwFalse:
		return true
	default:
		return false
	}
}

func isClosingDelimiter(k lexer.TokenKind) bool {
	return k == lexer.TokenRParen || k == lexer.TokenRBracket || k == lexer.TokenRBrace || k == lexer.TokenRAngle
}

func isCommentTrivia(k lexer.TriviaKind) bool {
	return k == lexer.TriviaLineComment || k == lexer.TriviaHashComment || k == lexer.TriviaBlockComment || k == lexer.TriviaDocComment
}

func triviaHasComment(trivia []lexer.Trivia) bool {
	for _, tr := range trivia {
		if isCommentTrivia(tr.Kind) {
			return true
		}
	}
	return false
}

func leadingNewlinesBeforeFirstComment(trivia []lexer.Trivia) int {
	count := 0
	for _, tr := range trivia {
		if isCommentTrivia(tr.Kind) {
			return count
		}
		if tr.Kind == lexer.TriviaNewline {
			count++
		}
	}
	return count
}

func nextTokenHasLeadingComment(tokens []lexer.Token, next int) bool {
	if next < 0 || next >= len(tokens) {
		return false
	}
	if tokens[next].Kind == lexer.TokenEOF {
		return false
	}
	return triviaHasComment(tokens[next].Leading)
}

func repeatString(s string, count int) string {
	if count <= 0 || s == "" {
		return ""
	}
	return strings.Repeat(s, count)
}
