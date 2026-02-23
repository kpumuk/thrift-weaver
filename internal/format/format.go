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
// v1 Track A performs safety checks and returns the original source bytes unchanged.
func Document(ctx context.Context, tree *syntax.Tree, opts Options) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if tree == nil {
		return Result{}, errors.New("nil syntax tree")
	}
	normOpts, err := normalizeOptions(opts)
	if err != nil {
		return Result{}, err
	}

	diags := append([]syntax.Diagnostic(nil), tree.Diagnostics...)
	policy, policyDiags := analyzeSourcePolicy(tree.Source)
	diags = append(diags, policyDiags...)

	if !policy.ValidUTF8 {
		return unsafeResult(diags, UnsafeReasonInvalidUTF8, "input contains invalid UTF-8 bytes")
	}
	if hasUnsafeSyntaxDiagnostics(tree.Diagnostics) {
		return unsafeResult(diags, UnsafeReasonSyntaxErrors, "syntax diagnostics present (fail-closed v1 policy)")
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
// v1 Track A returns no edits after safety checks.
func Range(ctx context.Context, tree *syntax.Tree, r text.Span, opts Options) (RangeResult, error) {
	if tree == nil {
		return RangeResult{}, errors.New("nil syntax tree")
	}
	if err := r.Validate(); err != nil {
		return RangeResult{}, fmt.Errorf("invalid range: %w", err)
	}
	srcSpan := sourceSpan(tree.Source)
	if !srcSpan.ContainsSpan(r) {
		return RangeResult{}, fmt.Errorf("range %s out of bounds for source length %d", r, len(tree.Source))
	}

	res, err := Document(ctx, tree, opts)
	return RangeResult{
		Diagnostics: res.Diagnostics,
	}, err
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
		if d.Severity == syntax.SeverityError && d.Source != "formatter" {
			return true
		}
	}
	return false
}

func unsafeResult(diags []syntax.Diagnostic, reason UnsafeReason, msg string) (Result, error) {
	return Result{
			Output:      nil,
			Changed:     false,
			Diagnostics: diags,
		}, &ErrUnsafeToFormat{
			Reason:  reason,
			Message: msg,
		}
}
