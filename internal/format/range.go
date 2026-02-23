package format

import (
	"errors"
	"fmt"

	"github.com/kpumuk/thrift-weaver/internal/lexer"
	"github.com/kpumuk/thrift-weaver/internal/syntax"
	"github.com/kpumuk/thrift-weaver/internal/text"
)

const (
	// DiagnosticFormatterRangeNoSafeAncestor reports that range widening found no format-safe ancestor.
	DiagnosticFormatterRangeNoSafeAncestor syntax.DiagnosticCode = "FMT_RANGE_NO_SAFE_ANCESTOR"
	// DiagnosticFormatterRangeUnboundedNode reports token/span coverage mismatch for the widened ancestor.
	DiagnosticFormatterRangeUnboundedNode syntax.DiagnosticCode = "FMT_RANGE_UNBOUNDED_ANCESTOR"
)

func findRangeFormatAncestor(tree *syntax.Tree, r text.Span) (syntax.NodeID, syntax.Diagnostic, error) {
	if tree == nil {
		return syntax.NoNode, syntax.Diagnostic{}, errors.New("nil syntax tree")
	}

	best := syntax.NoNode
	bestLen := text.ByteOffset(-1)
	for i := 1; i < len(tree.Nodes); i++ {
		id := syntax.NodeID(i)
		n := tree.NodeByID(id)
		if n == nil || !isFormatSafeAncestorKind(syntax.KindName(n.Kind)) {
			continue
		}
		if !nodeContainsRange(n, r) {
			continue
		}
		if best == syntax.NoNode || n.Span.Len() < bestLen {
			best = id
			bestLen = n.Span.Len()
		}
	}

	if best == syntax.NoNode {
		return rangeBlockingFailure(
			DiagnosticFormatterRangeNoSafeAncestor,
			r,
			"selected range cannot be widened to a format-safe ancestor",
			"selected range cannot be widened to a format-safe ancestor",
		)
	}

	n := tree.NodeByID(best)
	if !hasBoundedTokenCoverage(tree, n) {
		span := r
		if n != nil {
			span = n.Span
		}
		return rangeBlockingFailure(
			DiagnosticFormatterRangeUnboundedNode,
			span,
			"range formatting ancestor does not have fully bounded token coverage",
			"range formatting requires a format-safe ancestor with fully bounded token coverage",
		)
	}

	return best, syntax.Diagnostic{}, nil
}

func formatNodeRange(tree *syntax.Tree, id syntax.NodeID, opts Options, policy SourcePolicy) ([]byte, error) {
	n := tree.NodeByID(id)
	if n == nil {
		return nil, fmt.Errorf("range ancestor node %d not found", id)
	}
	if !hasBoundedTokenCoverage(tree, n) {
		return nil, fmt.Errorf("node %d does not have bounded token coverage", id)
	}
	if int(n.FirstToken) >= len(tree.Tokens) || int(n.LastToken) >= len(tree.Tokens) || n.LastToken < n.FirstToken {
		return nil, fmt.Errorf("node %d token range out of bounds", id)
	}

	hints := collectFormatHints(tree)
	indentLevel := indentLevelBeforeToken(hints, n.FirstToken)
	writerAtLineStart := isLineStartOffset(tree.Source, n.Span.Start)
	w := newTokenWriter(policy.Newline, opts.Indent, opts.MaxBlankLines)
	w.atLineStart = writerAtLineStart

	var prevKind lexer.TokenKind
	var havePrev bool

	for ti := n.FirstToken; ti <= n.LastToken; ti++ {
		if int(ti) >= len(tree.Tokens) {
			return nil, fmt.Errorf("token index %d out of bounds", ti)
		}
		tok := tree.Tokens[ti]
		if tok.Kind == lexer.TokenEOF {
			break
		}

		if spec, ok := hints.declBlockClose[ti]; ok && spec.HasMembers {
			if indentLevel > 0 {
				indentLevel--
			}
			if ti != n.FirstToken {
				w.requestBreaks(1)
			}
		}
		if ti != n.FirstToken {
			if _, ok := hints.memberStart[ti]; ok {
				w.requestBreaks(1)
			}
			// For subtree formatting, top-level blank line spacing is intentionally omitted;
			// surrounding whitespace remains outside the edit span.
		}

		if ti != n.FirstToken {
			if err := w.emitLeadingTrivia(tree.Source, tok.Leading, indentLevel, false); err != nil {
				return nil, err
			}
		}
		if havePrev && shouldInsertSpace(prevKind, tok.Kind) {
			w.requestSpace()
		}

		raw := tok.Bytes(tree.Source)
		if raw == nil {
			return nil, fmt.Errorf("invalid token span %s at index %d", tok.Span, ti)
		}
		w.writeRaw(indentLevel, raw)

		if spec, ok := hints.declBlockOpen[ti]; ok && spec.HasMembers {
			indentLevel++
			w.requestBreaks(1)
		}

		prevKind = tok.Kind
		havePrev = true
	}

	return w.finish(), nil
}

func indentLevelBeforeToken(hints formatHints, startTok uint32) int {
	level := 0
	for tok := uint32(0); tok < startTok; {
		if spec, ok := hints.declBlockClose[tok]; ok && spec.HasMembers {
			if level > 0 {
				level--
			}
		}
		if spec, ok := hints.declBlockOpen[tok]; ok && spec.HasMembers {
			level++
		}
		tok++
	}
	return level
}

func hasBoundedTokenCoverage(tree *syntax.Tree, n *syntax.Node) bool {
	if tree == nil || n == nil {
		return false
	}
	if !n.Span.IsValid() {
		return false
	}
	if int(n.FirstToken) >= len(tree.Tokens) || int(n.LastToken) >= len(tree.Tokens) || n.LastToken < n.FirstToken {
		return false
	}

	first := tree.Tokens[n.FirstToken]
	last := tree.Tokens[n.LastToken]
	if first.Kind == lexer.TokenEOF || last.Kind == lexer.TokenEOF {
		return false
	}
	if first.Span.Start != n.Span.Start || last.Span.End != n.Span.End {
		return false
	}
	for i := n.FirstToken; i <= n.LastToken; i++ {
		tok := tree.Tokens[i]
		if tok.Kind == lexer.TokenEOF || !n.Span.ContainsSpan(tok.Span) {
			return false
		}
	}
	return true
}

func nodeContainsRange(n *syntax.Node, r text.Span) bool {
	if n == nil || !n.Span.IsValid() || !r.IsValid() {
		return false
	}
	if r.IsEmpty() {
		return n.Span.Start <= r.Start && r.Start <= n.Span.End
	}
	return n.Span.ContainsSpan(r)
}

func isFormatSafeAncestorKind(kind string) bool {
	switch kind {
	case "include_declaration", "cpp_include_declaration", "namespace_declaration", "typedef_declaration", "const_declaration",
		"enum_definition", "senum_definition", "struct_definition", "union_definition", "exception_definition", "service_definition",
		"field_block", "function_block", "enum_block",
		"field", "function_definition", "enum_value", "senum_value",
		"const_list", "const_map", "annotations":
		return true
	default:
		return false
	}
}

func isLineStartOffset(src []byte, off text.ByteOffset) bool {
	if !off.IsValid() {
		return false
	}
	if off == 0 {
		return true
	}
	i := int(off)
	if i <= 0 || i > len(src) {
		return false
	}
	return src[i-1] == '\n' || src[i-1] == '\r'
}

func rangeBlockingDiagnostic(code syntax.DiagnosticCode, sp text.Span, msg string) syntax.Diagnostic {
	return syntax.Diagnostic{
		Code:        code,
		Message:     msg,
		Severity:    syntax.SeverityError,
		Span:        sp,
		Source:      "formatter",
		Recoverable: false,
	}
}

func rangeBlockingFailure(code syntax.DiagnosticCode, sp text.Span, diagMsg, errMsg string) (syntax.NodeID, syntax.Diagnostic, error) {
	diag := rangeBlockingDiagnostic(code, sp, diagMsg)
	return syntax.NoNode, diag, unsafeFormattingErr(UnsafeReasonSyntaxErrors, errMsg)
}
