package lint

import (
	"context"

	"github.com/kpumuk/thrift-weaver/internal/syntax"
)

const (
	// DiagnosticFieldIDRequired reports fields without an explicit numeric id.
	DiagnosticFieldIDRequired syntax.DiagnosticCode = "LINT_FIELD_ID_REQUIRED"
)

// FieldIDRequiredRule enforces explicit field ids on all field declarations.
type FieldIDRequiredRule struct{}

// ID returns the stable rule identifier.
func (FieldIDRequiredRule) ID() string {
	return "field_id_required"
}

// Description returns a human-readable rule summary.
func (FieldIDRequiredRule) Description() string {
	return "all field declarations must define an explicit numeric field id"
}

// Run evaluates the rule against a syntax tree.
func (FieldIDRequiredRule) Run(ctx context.Context, tree *syntax.Tree) ([]syntax.Diagnostic, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	out := make([]syntax.Diagnostic, 0, 8)
	forEachNamedNode(tree, func(n *syntax.Node, kind string) {
		if kind != "field" {
			return
		}
		if hasAnyNodeFlag(n.Flags, syntax.NodeFlagError|syntax.NodeFlagMissing|syntax.NodeFlagRecovered) {
			return
		}
		if hasChildByKind(tree, n.ID, "field_id") {
			return
		}

		span := firstChildSpanByKind(tree, n.ID, "field_name")
		if !span.IsValid() {
			span = n.Span
		}
		out = append(out, syntax.Diagnostic{
			Code:        DiagnosticFieldIDRequired,
			Message:     "field is missing an explicit field id (for example: `1:`)",
			Severity:    syntax.SeverityWarning,
			Span:        span,
			Recoverable: true,
		})
	})
	return out, nil
}
