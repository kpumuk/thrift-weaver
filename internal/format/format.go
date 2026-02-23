package format

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/kpumuk/thrift-weaver/internal/syntax"
	"github.com/kpumuk/thrift-weaver/internal/text"
)

// Document formats a full syntax tree.
func Document(ctx context.Context, tree *syntax.Tree, opts Options) (Result, error) {
	normOpts, policy, diags, err := prepareFormatting(ctx, tree, opts)
	if err != nil {
		return Result{Diagnostics: diags}, err
	}

	out, err := formatSyntaxTree(tree, normOpts, policy)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Output:      out,
		Changed:     !bytes.Equal(out, tree.Source),
		Diagnostics: diags,
	}, nil
}

// Range formats a source range by returning byte edits.
func Range(ctx context.Context, tree *syntax.Tree, r text.Span, opts Options) (RangeResult, error) {
	normOpts, policy, diags, err := prepareFormatting(ctx, tree, opts)
	if err != nil {
		return RangeResult{Diagnostics: diags}, err
	}
	if err := r.Validate(); err != nil {
		return RangeResult{}, fmt.Errorf("invalid range: %w", err)
	}
	srcSpan := sourceSpan(tree.Source)
	if !srcSpan.ContainsSpan(r) {
		return RangeResult{}, fmt.Errorf("range %s out of bounds for source length %d", r, len(tree.Source))
	}

	ancestor, widenDiag, err := findRangeFormatAncestor(tree, r)
	if widenDiag.Code != "" {
		diags = append(diags, widenDiag)
	}
	if err != nil {
		return RangeResult{Diagnostics: diags}, err
	}

	out, err := formatNodeRange(tree, ancestor, normOpts, policy)
	if err != nil {
		return RangeResult{Diagnostics: diags}, err
	}
	n := tree.NodeByID(ancestor)
	if n == nil {
		return RangeResult{}, errors.New("range ancestor node not found")
	}
	old := tree.Source[n.Span.Start:n.Span.End]
	if bytes.Equal(out, old) {
		return RangeResult{Diagnostics: diags}, nil
	}
	return RangeResult{
		Edits: []text.ByteEdit{{
			Span:    n.Span,
			NewText: out,
		}},
		Diagnostics: diags,
	}, nil
}

// Source parses and formats source bytes in one step.
func Source(ctx context.Context, src []byte, uri string, opts Options) (Result, error) {
	tree, err := syntax.Parse(ctx, src, syntax.ParseOptions{URI: uri})
	if err != nil {
		return Result{}, err
	}
	return Document(ctx, tree, opts)
}

func hasUnsafeSyntaxDiagnostics(diags []syntax.Diagnostic) bool {
	for _, d := range diags {
		if d.Source == "formatter" {
			continue
		}
		if !d.Recoverable {
			return true
		}
		if d.Source == "lexer" && isUnsafeLexerDiagnostic(d) {
			return true
		}
	}
	return false
}

func prepareFormatting(ctx context.Context, tree *syntax.Tree, opts Options) (Options, SourcePolicy, []syntax.Diagnostic, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Options{}, SourcePolicy{}, nil, err
	}
	if tree == nil {
		return Options{}, SourcePolicy{}, nil, errors.New("nil syntax tree")
	}

	normOpts, err := normalizeOptions(opts)
	if err != nil {
		return Options{}, SourcePolicy{}, nil, err
	}

	diags := append([]syntax.Diagnostic(nil), tree.Diagnostics...)
	policy, policyDiags := analyzeSourcePolicy(tree.Source)
	diags = append(diags, policyDiags...)

	switch {
	case !policy.ValidUTF8:
		return normOpts, policy, diags, unsafeFormattingErr(UnsafeReasonInvalidUTF8, "input contains invalid UTF-8 bytes")
	case tree.Root == syntax.NoNode:
		return normOpts, policy, diags, unsafeFormattingErr(UnsafeReasonSyntaxErrors, "syntax tree root is missing")
	case hasUnsafeSyntaxDiagnostics(tree.Diagnostics):
		return normOpts, policy, diags, unsafeFormattingErr(UnsafeReasonSyntaxErrors, "unsafe lexer/parser diagnostics present")
	default:
		return normOpts, policy, diags, nil
	}
}

func isUnsafeLexerDiagnostic(d syntax.Diagnostic) bool {
	switch string(d.Code) {
	case "LEX_UNTERMINATED_STRING", "LEX_UNTERMINATED_BLOCK_COMMENT":
		return true
	default:
		return false
	}
}

func unsafeFormattingErr(reason UnsafeReason, msg string) *ErrUnsafeToFormat {
	return &ErrUnsafeToFormat{
		Reason:  reason,
		Message: msg,
	}
}
