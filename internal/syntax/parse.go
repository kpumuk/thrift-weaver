package syntax

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/kpumuk/thrift-weaver/internal/lexer"
	ts "github.com/kpumuk/thrift-weaver/internal/syntax/treesitter"
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
	rawTree, err := parser.Parse(ctx, src, nil)
	if err != nil {
		parser.Close()
		return nil, err
	}
	out, err := buildSyntaxTreeFromRawWithLexResult(src, opts, rawTree, lexRes)
	if err != nil {
		rawTree.Close()
		parser.Close()
		return nil, err
	}
	adoptRuntimeTree(out, &parseRuntimeState{
		parser:             parser,
		rawTree:            rawTree,
		incrementalEnabled: true,
	})
	emitReparseEvent(ReparseEvent{Mode: "full"})

	if err := ctx.Err(); err != nil {
		out.closeRuntime()
		return nil, err
	}
	return out, nil
}

// Reparse reparses from scratch when no incremental edit set is available.
func Reparse(ctx context.Context, old *Tree, src []byte, opts ParseOptions) (*Tree, error) {
	return ApplyIncrementalEditsAndReparse(ctx, old, src, opts, nil)
}

// ApplyIncrementalEditsAndReparse applies edits, reparses with old-tree reuse, and verifies consistency.
func ApplyIncrementalEditsAndReparse(ctx context.Context, old *Tree, src []byte, opts ParseOptions, edits []InputEdit) (*Tree, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if old == nil {
		return Parse(ctx, src, opts)
	}
	if err := validateIncrementalEdits(edits, len(old.Source)); err != nil {
		return fallbackIncrementalReparse(
			ctx,
			old,
			src,
			opts,
			ReparseEvent{Mode: "fallback_full", FallbackReason: "invalid_edit"},
			"incremental edit validation failed; using full parse",
		)
	}
	if len(edits) == 0 {
		return fallbackIncrementalReparse(
			ctx,
			old,
			src,
			opts,
			ReparseEvent{Mode: "fallback_full", ProvidedOldTree: true, FallbackReason: "no_edits"},
			"",
		)
	}

	state := runtimeStateFromTree(old)
	if errNoIncrementalState(old) != nil {
		return fallbackIncrementalReparse(
			ctx,
			old,
			src,
			opts,
			ReparseEvent{Mode: "fallback_full", ProvidedOldTree: true, FallbackReason: "no_runtime_state"},
			"incremental parser state unavailable; using full parse",
		)
	}
	if !state.incrementalEnabled {
		return fallbackIncrementalReparse(
			ctx,
			old,
			src,
			opts,
			ReparseEvent{Mode: "fallback_full", ProvidedOldTree: true, FallbackReason: "incremental_disabled"},
			"incremental mode disabled for this document; using full parse",
		)
	}

	for _, edit := range edits {
		if err := state.rawTree.ApplyEdit(ctx, toTSEdit(edit)); err != nil {
			state.incrementalEnabled = false
			return fallbackIncrementalReparse(
				ctx,
				old,
				src,
				opts,
				ReparseEvent{Mode: "fallback_full", ProvidedOldTree: true, FallbackReason: "tree_edit_failed"},
				"incremental tree edit failed; using full parse",
			)
		}
	}

	incrementalRaw, err := state.parser.Parse(ctx, src, state.rawTree)
	if err != nil {
		state.incrementalEnabled = false
		return fallbackIncrementalReparse(
			ctx,
			old,
			src,
			opts,
			ReparseEvent{Mode: "fallback_full", ProvidedOldTree: true, AppliedTreeEdits: len(edits), FallbackReason: "parse_with_old_failed"},
			"incremental parse failed; using full parse",
		)
	}

	changed, err := state.rawTree.ChangedRanges(ctx, incrementalRaw)
	if err != nil {
		incrementalRaw.Close()
		state.incrementalEnabled = false
		return fallbackIncrementalReparse(
			ctx,
			old,
			src,
			opts,
			ReparseEvent{Mode: "fallback_full", ProvidedOldTree: true, AppliedTreeEdits: len(edits), FallbackReason: "changed_ranges_failed"},
			"changed-range extraction failed; using full parse",
		)
	}
	changedSpans, err := spansFromChangedRanges(changed)
	if err != nil || validateChangedRanges(changedSpans, len(src)) != nil {
		incrementalRaw.Close()
		state.incrementalEnabled = false
		return fallbackIncrementalReparse(
			ctx,
			old,
			src,
			opts,
			ReparseEvent{Mode: "fallback_full", ProvidedOldTree: true, AppliedTreeEdits: len(edits), FallbackReason: "changed_ranges_invalid"},
			"changed-range verification failed; using full parse",
		)
	}

	out, err := buildSyntaxTreeFromRaw(src, opts, incrementalRaw)
	if err != nil {
		incrementalRaw.Close()
		return nil, err
	}
	out.ChangedRanges = changedSpans

	nextState := &parseRuntimeState{
		parser:             state.parser,
		rawTree:            incrementalRaw,
		incrementalEnabled: state.incrementalEnabled,
		reparseCount:       state.reparseCount + 1,
	}
	adoptRuntimeTree(out, nextState)

	verificationRun, verifiedTree, err := verifyIncrementalReparse(ctx, out, nextState, src, opts)
	if err != nil {
		return nil, err
	}
	if verifiedTree != nil {
		if state.rawTree != nil {
			state.rawTree.Close()
			state.rawTree = nil
		}
		old.runtime = nil
		emitReparseEvent(ReparseEvent{
			Mode:               "incremental",
			ProvidedOldTree:    true,
			AppliedTreeEdits:   len(edits),
			ChangedRangeCount:  len(changedSpans),
			VerificationRun:    true,
			VerificationFailed: true,
		})
		return verifiedTree, nil
	}

	if state.rawTree != nil {
		state.rawTree.Close()
		state.rawTree = nil
	}
	old.runtime = nil
	emitReparseEvent(ReparseEvent{
		Mode:              "incremental",
		ProvidedOldTree:   true,
		AppliedTreeEdits:  len(edits),
		ChangedRangeCount: len(changedSpans),
		VerificationRun:   verificationRun,
	})
	return out, nil
}

func verifyIncrementalReparse(ctx context.Context, incrementalTree *Tree, state *parseRuntimeState, src []byte, opts ParseOptions) (bool, *Tree, error) {
	if !shouldVerifyWithFullParse(state) {
		return false, nil, nil
	}

	// Verification is best-effort; failures only annotate diagnostics and keep incremental output.
	fullRaw, _ := state.parser.Parse(ctx, src, nil)
	if fullRaw == nil {
		incrementalTree.Diagnostics = append(incrementalTree.Diagnostics, parserWarningDiagnostic("full-parse verification failed; keeping incremental result"))
		return true, nil, nil
	}

	fullTree, buildErr := buildSyntaxTreeFromRaw(src, opts, fullRaw)
	if buildErr != nil {
		fullRaw.Close()
		return true, nil, buildErr
	}
	if equivalentTrees(incrementalTree, fullTree) {
		fullRaw.Close()
		return true, nil, nil
	}

	if state.rawTree != nil {
		state.rawTree.Close()
		state.rawTree = nil
	}
	adoptRuntimeTree(fullTree, &parseRuntimeState{
		parser:             state.parser,
		rawTree:            fullRaw,
		incrementalEnabled: false,
		reparseCount:       state.reparseCount,
	})
	fullTree.Diagnostics = append(fullTree.Diagnostics, parserWarningDiagnostic("incremental verification mismatch; switched to full parse"))
	return true, fullTree, nil
}

func fallbackIncrementalReparse(
	ctx context.Context,
	old *Tree,
	src []byte,
	opts ParseOptions,
	event ReparseEvent,
	reason string,
) (*Tree, error) {
	emitReparseEvent(event)
	return fullReparseWithExistingParser(ctx, old, src, opts, reason)
}

func buildSyntaxTreeFromRaw(src []byte, opts ParseOptions, rawTree *ts.Tree) (*Tree, error) {
	lexRes := lexer.Lex(src)
	return buildSyntaxTreeFromRawWithLexResult(src, opts, rawTree, lexRes)
}

func buildSyntaxTreeFromRawWithLexResult(src []byte, opts ParseOptions, rawTree *ts.Tree, lexRes lexer.Result) (*Tree, error) {
	if rawTree == nil {
		return nil, errors.New("tree-sitter tree is nil")
	}

	sourceCopy := slices.Clone(src)
	out := &Tree{
		URI:       opts.URI,
		Version:   opts.Version,
		Source:    sourceCopy,
		Tokens:    append([]lexer.Token(nil), lexRes.Tokens...),
		LineIndex: text.NewLineIndex(sourceCopy),
	}

	out.Diagnostics = append(out.Diagnostics, mapLexerDiagnostics(lexRes.Diagnostics)...)
	out.Diagnostics = append(out.Diagnostics, validateTokenInvariants(sourceCopy, out.Tokens)...)

	builder := cstBuilder{
		tokens:    out.Tokens,
		nodes:     &out.Nodes,
		lineIndex: out.LineIndex,
	}
	root := rawTree.Root().Inner()
	if root == nil {
		return nil, errors.New("tree-sitter root node is nil")
	}
	out.Nodes = make([]Node, 1, 1+countRawNodes(root))
	out.Root = builder.buildNode(root, NoNode)
	out.Diagnostics = append(out.Diagnostics, builder.diagnostics...)
	out.Diagnostics = append(out.Diagnostics, collectParserDiagnostics(root, out.LineIndex)...)
	return out, nil
}

func countRawNodes(root *ts.RawNode) int {
	if root == nil {
		return 0
	}
	total := 1
	for _, child := range root.Children {
		if child == nil || child.IsExtra {
			continue
		}
		total += countRawNodes(child)
	}
	return total
}

type cstBuilder struct {
	tokens      []lexer.Token
	nodes       *[]Node
	lineIndex   *text.LineIndex
	diagnostics []Diagnostic
}

func (b *cstBuilder) buildNode(raw *ts.RawNode, parent NodeID) NodeID {
	id := nodeIDFromLen(len(*b.nodes))
	sp := spanFromRawNode(raw, b.lineIndex)
	firstTok, lastTok, rangeErr := tokenRangeForSpan(b.tokens, sp)
	if rangeErr != nil {
		b.diagnostics = append(b.diagnostics, internalAlignmentDiag(sp, rangeErr.Error()))
	}

	flags := nodeFlagsFromRaw(raw)
	if raw.HasError {
		flags |= NodeFlagRecovered
	}
	*b.nodes = append(*b.nodes, Node{
		ID:         id,
		Kind:       NodeKind(raw.KindID),
		Span:       sp,
		FirstToken: firstTok,
		LastToken:  lastTok,
		Parent:     parent,
		Flags:      flags,
	})

	children := raw.Children
	if len(children) == 0 {
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
	for _, child := range children {
		if child == nil || child.IsExtra {
			continue
		}
		childID := b.buildNode(child, id)
		childRefs = append(childRefs, ChildRef{IsToken: false, Index: uint32(childID)})
	}
	(*b.nodes)[id].Children = childRefs
	return id
}

func nodeFlagsFromRaw(n *ts.RawNode) NodeFlags {
	var flags NodeFlags
	if n == nil {
		return flags
	}
	if n.IsNamed {
		flags |= NodeFlagNamed
	}
	if n.IsError {
		flags |= NodeFlagError
	}
	if n.IsMissing {
		flags |= NodeFlagMissing
	}
	return flags
}

func spanFromRawNode(n *ts.RawNode, li *text.LineIndex) text.Span {
	if n == nil || li == nil {
		return text.Span{}
	}
	if n.StartByte >= 0 && n.EndByte >= n.StartByte && n.EndByte <= int(li.SourceLen()) {
		return text.Span{Start: text.ByteOffset(n.StartByte), End: text.ByteOffset(n.EndByte)}
	}
	start, err := li.PointToOffset(text.Point{Line: n.StartRow, Column: n.StartCol})
	if err != nil {
		return text.Span{}
	}
	end, err := li.PointToOffset(text.Point{Line: n.EndRow, Column: n.EndCol})
	if err != nil {
		return text.Span{}
	}
	return text.Span{Start: start, End: end}
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

func collectParserDiagnostics(root *ts.RawNode, li *text.LineIndex) []Diagnostic {
	if root == nil {
		return nil
	}
	var out []Diagnostic
	walkRaw(root, func(n *ts.RawNode) {
		sp := spanFromRawNode(n, li)
		switch {
		case n.IsMissing:
			out = append(out, Diagnostic{
				Code:        DiagnosticParserMissingNode,
				Message:     "missing " + n.Kind,
				Severity:    SeverityError,
				Span:        sp,
				Source:      "parser",
				Recoverable: true,
			})
		case n.IsError:
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

func walkRaw(root *ts.RawNode, visit func(*ts.RawNode)) {
	if root == nil {
		return
	}
	visit(root)
	for _, child := range root.Children {
		walkRaw(child, visit)
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
