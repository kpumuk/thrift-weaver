package lint

import (
	"context"
	"strings"

	"github.com/kpumuk/thrift-weaver/internal/syntax"
)

const (
	// DiagnosticFieldNameDuplicate reports duplicated field names within one field list.
	DiagnosticFieldNameDuplicate syntax.DiagnosticCode = "LINT_FIELD_NAME_DUPLICATE"
)

// FieldNameUniqueRule enforces uniqueness of field names within one containing field list.
type FieldNameUniqueRule struct{}

// ID returns the stable rule identifier.
func (FieldNameUniqueRule) ID() string {
	return "field_name_unique"
}

// Description returns a human-readable rule summary.
func (FieldNameUniqueRule) Description() string {
	return "field names must be unique within the same containing field list"
}

// Run evaluates the rule against a syntax tree.
func (FieldNameUniqueRule) Run(ctx context.Context, tree *syntax.Tree) ([]syntax.Diagnostic, error) {
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return duplicateFieldChildDiagnostics(
		tree,
		"field_name",
		strings.TrimSpace,
		DiagnosticFieldNameDuplicate,
		"field name is duplicated within the same containing field list",
	), nil
}
