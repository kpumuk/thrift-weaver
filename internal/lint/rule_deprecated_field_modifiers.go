package lint

import (
	"context"

	"github.com/kpumuk/thrift-weaver/internal/syntax"
)

const (
	// DiagnosticDeprecatedFieldXSDOptional reports deprecated xsd_optional field usage.
	DiagnosticDeprecatedFieldXSDOptional syntax.DiagnosticCode = "LINT_DEPRECATED_FIELD_XSD_OPTIONAL"
	// DiagnosticDeprecatedFieldXSDNillable reports deprecated xsd_nillable field usage.
	DiagnosticDeprecatedFieldXSDNillable syntax.DiagnosticCode = "LINT_DEPRECATED_FIELD_XSD_NILLABLE"
	// DiagnosticDeprecatedFieldXSDAttrs reports deprecated xsd_attrs field usage.
	DiagnosticDeprecatedFieldXSDAttrs syntax.DiagnosticCode = "LINT_DEPRECATED_FIELD_XSD_ATTRS"
)

var deprecatedFieldModifierDiagnostics = map[string]struct {
	code    syntax.DiagnosticCode
	message string
}{
	"xsd_optional": {
		code:    DiagnosticDeprecatedFieldXSDOptional,
		message: "deprecated field modifier `xsd_optional` should not be used",
	},
	"xsd_nillable": {
		code:    DiagnosticDeprecatedFieldXSDNillable,
		message: "deprecated field modifier `xsd_nillable` should not be used",
	},
	"xsd_attrs": {
		code:    DiagnosticDeprecatedFieldXSDAttrs,
		message: "deprecated field modifier `xsd_attrs` should not be used",
	},
}

func deprecatedFieldModifierDiagnostic(kind string) (syntax.DiagnosticCode, string, bool) {
	diagDef, ok := deprecatedFieldModifierDiagnostics[kind]
	if !ok {
		return "", "", false
	}
	return diagDef.code, diagDef.message, true
}

// DeprecatedFieldModifiersRule warns when deprecated xsd field modifiers are used.
type DeprecatedFieldModifiersRule struct{}

// ID returns the stable rule identifier.
func (DeprecatedFieldModifiersRule) ID() string {
	return "deprecated_field_modifiers"
}

// Description returns a human-readable rule summary.
func (DeprecatedFieldModifiersRule) Description() string {
	return "deprecated xsd field modifiers should not be used"
}

// Run evaluates the rule against a syntax tree.
func (DeprecatedFieldModifiersRule) Run(ctx context.Context, tree *syntax.Tree) ([]syntax.Diagnostic, error) {
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

		for _, childID := range tree.ChildNodeIDs(n.ID) {
			child := tree.NodeByID(childID)
			if child == nil {
				continue
			}
			code, message, ok := deprecatedFieldModifierDiagnostic(syntax.KindName(child.Kind))
			if !ok {
				continue
			}
			out = append(out, syntax.Diagnostic{
				Code:        code,
				Message:     message,
				Severity:    syntax.SeverityWarning,
				Span:        child.Span,
				Recoverable: true,
			})
		}
	})
	return out, nil
}
