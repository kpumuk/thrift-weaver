package lint

import (
	"context"
	"math/big"
	"strings"

	"github.com/kpumuk/thrift-weaver/internal/syntax"
)

const (
	// DiagnosticFieldIDDuplicate reports duplicated explicit field ids within one field list.
	DiagnosticFieldIDDuplicate syntax.DiagnosticCode = "LINT_FIELD_ID_DUPLICATE"
)

// FieldIDUniqueRule enforces uniqueness of explicit field ids within one containing field list.
type FieldIDUniqueRule struct{}

// ID returns the stable rule identifier.
func (FieldIDUniqueRule) ID() string {
	return "field_id_unique"
}

// Description returns a human-readable rule summary.
func (FieldIDUniqueRule) Description() string {
	return "explicit field ids must be unique within the same containing field list"
}

// Run evaluates the rule against a syntax tree.
func (FieldIDUniqueRule) Run(ctx context.Context, tree *syntax.Tree) ([]syntax.Diagnostic, error) {
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return duplicateFieldChildDiagnostics(
		tree,
		"field_id",
		normalizedIntegerLiteral,
		DiagnosticFieldIDDuplicate,
		"explicit field id is duplicated within the same containing field list",
	), nil
}

func normalizedIntegerLiteral(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	var value big.Int
	if _, ok := value.SetString(raw, 0); ok {
		return value.Text(10)
	}
	return raw
}
