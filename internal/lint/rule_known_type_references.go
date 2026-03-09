package lint

import (
	"context"

	"github.com/kpumuk/thrift-weaver/internal/syntax"
)

const (
	// DiagnosticTypeUnknown reports an unqualified local type reference that cannot be resolved.
	DiagnosticTypeUnknown syntax.DiagnosticCode = "LINT_TYPE_UNKNOWN"
	// DiagnosticTypedefUnknownBase reports an unresolved typedef base type.
	DiagnosticTypedefUnknownBase syntax.DiagnosticCode = "LINT_TYPEDEF_UNKNOWN_BASE"
)

// UnknownTypeRule validates locally resolvable type references outside typedef declarations and throws clauses.
type UnknownTypeRule struct{}

// TypedefUnknownBaseRule validates locally resolvable typedef base types.
type TypedefUnknownBaseRule struct{}

// ID returns the stable rule identifier.
func (UnknownTypeRule) ID() string {
	return "unknown_type"
}

// Description returns a human-readable rule summary.
func (UnknownTypeRule) Description() string {
	return "unqualified local type references must resolve within the current document"
}

// Run evaluates the rule against a syntax tree.
func (UnknownTypeRule) Run(ctx context.Context, tree *syntax.Tree) ([]syntax.Diagnostic, error) {
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	symbols := collectLocalSymbols(tree)
	out := make([]syntax.Diagnostic, 0, 4)
	forEachNamedNode(tree, func(n *syntax.Node, kind string) {
		if hasErrorFlags(n.Flags) {
			return
		}

		switch kind {
		case "field":
			if hasAncestorKind(tree, n.ID, "throws_clause") {
				return
			}
			appendDirectTypeDiagnostics(tree, symbols, n.ID, DiagnosticTypeUnknown, "referenced type is unknown in the current document", &out)
		case "const_declaration", "function_definition":
			appendDirectTypeDiagnostics(tree, symbols, n.ID, DiagnosticTypeUnknown, "referenced type is unknown in the current document", &out)
		}
	})

	return out, nil
}

// ID returns the stable rule identifier.
func (TypedefUnknownBaseRule) ID() string {
	return "typedef_unknown_base"
}

// Description returns a human-readable rule summary.
func (TypedefUnknownBaseRule) Description() string {
	return "typedef base types must resolve within the current document when locally referenceable"
}

// Run evaluates the rule against a syntax tree.
func (TypedefUnknownBaseRule) Run(ctx context.Context, tree *syntax.Tree) ([]syntax.Diagnostic, error) {
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	symbols := collectLocalSymbols(tree)
	out := make([]syntax.Diagnostic, 0, 2)
	forEachNamedNode(tree, func(n *syntax.Node, kind string) {
		if kind != "typedef_declaration" || hasErrorFlags(n.Flags) {
			return
		}
		appendDirectTypeDiagnostics(tree, symbols, n.ID, DiagnosticTypedefUnknownBase, "typedef base type is unknown in the current document", &out)
	})

	return out, nil
}
