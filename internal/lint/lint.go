// Package lint provides syntax-tree based lint diagnostics for thrift source files.
package lint

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/kpumuk/thrift-weaver/internal/syntax"
)

const (
	// DiagnosticSource is the LSP diagnostic source used by lint rules.
	DiagnosticSource = "thriftls.lint"
)

// Rule is a lint check that can emit diagnostics for a syntax tree.
type Rule interface {
	ID() string
	Description() string
	Run(ctx context.Context, tree *syntax.Tree) ([]syntax.Diagnostic, error)
}

// Runner executes lint rules and returns aggregated diagnostics.
type Runner struct {
	rules []Rule
}

// NewRunner builds a lint runner from a rule set.
func NewRunner(rules ...Rule) *Runner {
	copied := slices.Clone(rules)
	return &Runner{rules: copied}
}

// NewDefaultRunner builds the default lint rule set.
func NewDefaultRunner() *Runner {
	return NewRunner(
		FieldIDRequiredRule{},
		DeprecatedFieldModifiersRule{},
	)
}

// Run executes all configured rules and returns a sorted diagnostic list.
func (r *Runner) Run(ctx context.Context, tree *syntax.Tree) ([]syntax.Diagnostic, error) {
	if tree == nil {
		return nil, errors.New("nil syntax tree")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r == nil || len(r.rules) == 0 {
		return []syntax.Diagnostic{}, nil
	}

	out := make([]syntax.Diagnostic, 0, 8)
	for _, rule := range r.rules {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		diags, err := rule.Run(ctx, tree)
		if err != nil {
			return nil, fmt.Errorf("rule %s: %w", rule.ID(), err)
		}
		for i := range diags {
			if diags[i].Source == "" {
				diags[i].Source = DiagnosticSource
			}
		}
		out = append(out, diags...)
	}

	SortDiagnostics(out)

	return out, nil
}

// SortDiagnostics orders diagnostics deterministically for stable output.
func SortDiagnostics(diags []syntax.Diagnostic) {
	if len(diags) < 2 {
		return
	}

	sort.SliceStable(diags, func(i, j int) bool {
		a := diags[i]
		b := diags[j]
		if a.Span.Start != b.Span.Start {
			return a.Span.Start < b.Span.Start
		}
		if a.Span.End != b.Span.End {
			return a.Span.End < b.Span.End
		}
		if a.Severity != b.Severity {
			return a.Severity < b.Severity
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.Message < b.Message
	})
}
