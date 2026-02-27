//go:build cgo && thriftweaver_cgo

package syntax

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/kpumuk/thrift-weaver/internal/lexer"
	"github.com/kpumuk/thrift-weaver/internal/text"
)

// Parse tokenizes and parses src into a CST-oriented syntax tree.
func Parse(ctx context.Context, src []byte, opts ParseOptions) (*Tree, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	lexRes := lexer.Lex(src)

	parser, err := currentParserFactory().NewParser()
	if err != nil {
		return nil, fmt.Errorf("init parser: %w", err)
	}
	defer parser.Close()

	rawTree, err := parser.Parse(ctx, src, nil)
	if err != nil {
		return nil, err
	}
	defer rawTree.Close()

	sourceCopy := slices.Clone(src)
	out := &Tree{
		URI:       opts.URI,
		Version:   opts.Version,
		Source:    sourceCopy,
		Tokens:    append([]lexer.Token(nil), lexRes.Tokens...),
		Nodes:     []Node{{}}, // sentinel at index 0
		LineIndex: text.NewLineIndex(sourceCopy),
	}

	out.Diagnostics = append(out.Diagnostics, mapLexerDiagnostics(lexRes.Diagnostics)...)

	alignmentDiags := validateTokenInvariants(sourceCopy, out.Tokens)
	out.Diagnostics = append(out.Diagnostics, alignmentDiags...)

	builder := cstBuilder{
		tokens: out.Tokens,
		nodes:  &out.Nodes,
	}
	root := rawTree.Root().Inner()
	if root == nil {
		return nil, errors.New("tree-sitter root node is nil")
	}
	out.Root = builder.buildNode(root, NoNode)
	out.Diagnostics = append(out.Diagnostics, builder.diagnostics...)
	out.Diagnostics = append(out.Diagnostics, collectParserDiagnostics(root)...)

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Reparse reparses from scratch in v1 while preserving Parse-equivalent behavior.
func Reparse(ctx context.Context, old *Tree, src []byte, opts ParseOptions) (*Tree, error) {
	_ = old
	return Parse(ctx, src, opts)
}

type cstBuilder struct {
	tokens      []lexer.Token
	nodes       *[]Node
	diagnostics []Diagnostic
}

func (b *cstBuilder) buildNode(raw *sitter.Node, parent NodeID) NodeID {
	id := nodeIDFromLen(len(*b.nodes))
	sp := spanFromTSNode(raw)
	firstTok, lastTok, rangeErr := tokenRangeForSpan(b.tokens, sp)
	if rangeErr != nil {
		b.diagnostics = append(b.diagnostics, internalAlignmentDiag(sp, rangeErr.Error()))
	}

	flags := nodeFlagsFromTS(raw)
	if raw.HasError() {
		flags |= NodeFlagRecovered
	}
	*b.nodes = append(*b.nodes, Node{
		ID:         id,
		Kind:       NodeKind(raw.KindId()),
		Span:       sp,
		FirstToken: firstTok,
		LastToken:  lastTok,
		Parent:     parent,
		Flags:      flags,
	})

	children := childrenOf(raw)
	if len(children) == 0 {
		// Leaf nodes expose token refs directly for deterministic token lookup at the leaves.
		leaf := &(*b.nodes)[id]
		for i := firstTok; i <= lastTok && int(i) < len(b.tokens); i++ {
			if b.tokens[i].Kind == lexer.TokenEOF {
				continue
			}
			if !leaf.Span.IsEmpty() && !leaf.Span.Intersects(b.tokens[i].Span) {
				continue
			}
			leaf.Children = append(leaf.Children, ChildRef{IsToken: true, Index: i})
		}
		return id
	}

	childRefs := make([]ChildRef, 0, len(children))
	for i := range children {
		childID := b.buildNode(&children[i], id)
		childRefs = append(childRefs, ChildRef{IsToken: false, Index: uint32(childID)})
	}
	(*b.nodes)[id].Children = childRefs
	return id
}

func childrenOf(raw *sitter.Node) []sitter.Node {
	if raw == nil {
		return nil
	}
	cursor := raw.Walk()
	defer cursor.Close()
	children := raw.Children(cursor)
	if len(children) == 0 {
		return nil
	}

	out := children[:0]
	for i := range children {
		if children[i].IsExtra() {
			continue
		}
		out = append(out, children[i])
	}
	return out
}

func nodeFlagsFromTS(n *sitter.Node) NodeFlags {
	var flags NodeFlags
	if n == nil {
		return flags
	}
	if n.IsNamed() {
		flags |= NodeFlagNamed
	}
	if n.IsError() {
		flags |= NodeFlagError
	}
	if n.IsMissing() {
		flags |= NodeFlagMissing
	}
	return flags
}

func spanFromTSNode(n *sitter.Node) text.Span {
	if n == nil {
		return text.Span{}
	}
	return text.Span{Start: byteOffsetFromTS(n.StartByte()), End: byteOffsetFromTS(n.EndByte())}
}

func mapLexerDiagnostics(in []lexer.Diagnostic) []Diagnostic {
	out := make([]Diagnostic, 0, len(in))
	for _, d := range in {
		out = append(out, Diagnostic{
			Code:        DiagnosticCode(d.Code),
			Message:     d.Message,
			Severity:    SeverityError,
			Span:        d.Span,
			Source:      "lexer",
			Recoverable: true,
		})
	}
	return out
}

func collectParserDiagnostics(root *sitter.Node) []Diagnostic {
	if root == nil {
		return nil
	}
	var out []Diagnostic
	walkTS(root, func(n *sitter.Node) {
		sp := spanFromTSNode(n)
		switch {
		case n.IsMissing():
			out = append(out, Diagnostic{
				Code:        DiagnosticParserMissingNode,
				Message:     "missing " + n.Kind(),
				Severity:    SeverityError,
				Span:        sp,
				Source:      "parser",
				Recoverable: true,
			})
		case n.IsError():
			out = append(out, Diagnostic{
				Code:        DiagnosticParserErrorNode,
				Message:     "syntax error",
				Severity:    SeverityError,
				Span:        sp,
				Source:      "parser",
				Recoverable: true,
			})
		}
	})
	return out
}

func walkTS(root *sitter.Node, visit func(*sitter.Node)) {
	if root == nil {
		return
	}
	visit(root)
	cursor := root.Walk()
	defer cursor.Close()
	children := root.Children(cursor)
	for i := range children {
		child := children[i]
		walkTS(&child, visit)
	}
}

func validateTokenInvariants(src []byte, tokens []lexer.Token) []Diagnostic {
	if len(tokens) == 0 {
		return []Diagnostic{internalAlignmentDiag(text.Span{Start: 0, End: 0}, "lexer returned no tokens")}
	}

	var diags []Diagnostic
	prevStart := text.ByteOffset(0)
	prevEnd := text.ByteOffset(0)
	for i, tok := range tokens {
		if !tok.Span.IsValid() || tok.Span.End > text.ByteOffset(len(src)) {
			diags = append(diags, internalAlignmentDiag(tok.Span, fmt.Sprintf("invalid token span at index %d", i)))
			continue
		}
		if i > 0 && tok.Span.Start < prevStart {
			diags = append(diags, internalAlignmentDiag(tok.Span, fmt.Sprintf("token starts out of order at index %d", i)))
		}
		if i > 0 && tok.Span.Start < prevEnd {
			diags = append(diags, internalAlignmentDiag(tok.Span, fmt.Sprintf("overlapping token span at index %d", i)))
		}
		prevStart, prevEnd = tok.Span.Start, tok.Span.End
	}
	last := tokens[len(tokens)-1]
	if last.Kind != lexer.TokenEOF {
		diags = append(diags, internalAlignmentDiag(last.Span, "last token is not EOF"))
	}
	eof := text.ByteOffset(len(src))
	if last.Span.Start != eof || last.Span.End != eof {
		diags = append(diags, internalAlignmentDiag(last.Span, "EOF token span does not match source length"))
	}
	return diags
}

func tokenRangeForSpan(tokens []lexer.Token, sp text.Span) (uint32, uint32, error) {
	if len(tokens) == 0 {
		return 0, 0, errors.New("no tokens available for span mapping")
	}
	if !sp.IsValid() {
		idx := uint32FromInt(len(tokens) - 1)
		return idx, idx, fmt.Errorf("invalid node span %s", sp)
	}

	if sp.IsEmpty() {
		idx := nearestTokenIndex(tokens, sp.Start)
		return idx, idx, nil
	}

	first := -1
	last := -1
	for i, tok := range tokens {
		if tok.Kind == lexer.TokenEOF {
			break
		}
		if tok.Span.End <= sp.Start {
			continue
		}
		if tok.Span.Start >= sp.End {
			break
		}
		if tok.Span.Intersects(sp) || tok.Span.Contains(sp.Start) || sp.Contains(tok.Span.Start) {
			if first == -1 {
				first = i
			}
			last = i
		}
	}
	if first == -1 {
		idx := nearestTokenIndex(tokens, sp.Start)
		return idx, idx, fmt.Errorf("node span %s does not cover any lexer token", sp)
	}
	return uint32FromInt(first), uint32FromInt(last), nil
}

func nearestTokenIndex(tokens []lexer.Token, off text.ByteOffset) uint32 {
	for i, tok := range tokens {
		if tok.Span.Contains(off) || tok.Span.Start >= off {
			return uint32FromInt(i)
		}
	}
	return uint32FromInt(len(tokens) - 1)
}

func internalAlignmentDiag(span text.Span, msg string) Diagnostic {
	return Diagnostic{
		Code:        DiagnosticInternalAlignment,
		Message:     msg,
		Severity:    SeverityError,
		Span:        span,
		Source:      "parser",
		Recoverable: false,
	}
}

func byteOffsetFromTS(v uint) text.ByteOffset {
	if uint64(v) > uint64(math.MaxInt) {
		return text.ByteOffset(math.MaxInt)
	}
	return text.ByteOffset(int(v))
}

func uint32FromInt(v int) uint32 {
	if v <= 0 {
		return 0
	}
	if uint64(v) > uint64(math.MaxUint32) {
		return math.MaxUint32
	}
	return uint32(v)
}

func nodeIDFromLen(v int) NodeID {
	return NodeID(uint32FromInt(v))
}
