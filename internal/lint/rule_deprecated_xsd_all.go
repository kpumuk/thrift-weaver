package lint

import (
	"context"

	"github.com/kpumuk/thrift-weaver/internal/syntax"
)

const (
	// DiagnosticDeprecatedXSDAll reports deprecated xsd_all definition usage.
	DiagnosticDeprecatedXSDAll syntax.DiagnosticCode = "LINT_DEPRECATED_XSD_ALL"
)

// DeprecatedXSDAllRule warns when deprecated xsd_all is used on struct/union definitions.
type DeprecatedXSDAllRule struct{}

// ID returns the stable rule identifier.
func (DeprecatedXSDAllRule) ID() string {
	return "deprecated_xsd_all"
}

// Description returns a human-readable rule summary.
func (DeprecatedXSDAllRule) Description() string {
	return "deprecated definition modifier xsd_all should not be used"
}

// Run evaluates the rule against a syntax tree.
func (DeprecatedXSDAllRule) Run(ctx context.Context, tree *syntax.Tree) ([]syntax.Diagnostic, error) {
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	out := make([]syntax.Diagnostic, 0, 4)
	forEachNamedNode(tree, func(n *syntax.Node, kind string) {
		if kind != "struct_definition" && kind != "union_definition" {
			return
		}
		if hasErrorFlags(n.Flags) {
			return
		}

		xsdAllSpan := firstChildSpanByKind(tree, n.ID, "xsd_all")
		if !xsdAllSpan.IsValid() {
			return
		}

		out = append(out, newRecoverableWarning(
			DiagnosticDeprecatedXSDAll,
			"deprecated definition modifier `xsd_all` should not be used",
			xsdAllSpan,
		))
	})

	return out, nil
}
