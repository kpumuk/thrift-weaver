package lint

import (
	"context"
	"math/big"
	"strings"

	"github.com/kpumuk/thrift-weaver/internal/syntax"
	itext "github.com/kpumuk/thrift-weaver/internal/text"
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

	byParent := make(map[syntax.NodeID]map[string][]itext.Span)
	forEachNamedNode(tree, func(n *syntax.Node, kind string) {
		if kind != "field" {
			return
		}
		if hasErrorFlags(n.Flags) {
			return
		}

		fieldIDSpan := firstChildSpanByKind(tree, n.ID, "field_id")
		if !fieldIDSpan.IsValid() {
			return
		}

		fieldIDKey := normalizedIntegerLiteral(textForSpan(tree.Source, fieldIDSpan))
		if fieldIDKey == "" {
			return
		}

		if byParent[n.Parent] == nil {
			byParent[n.Parent] = make(map[string][]itext.Span)
		}
		byParent[n.Parent][fieldIDKey] = append(byParent[n.Parent][fieldIDKey], fieldIDSpan)
	})

	out := make([]syntax.Diagnostic, 0, 4)
	for _, byValue := range byParent {
		for _, spans := range byValue {
			if len(spans) < 2 {
				continue
			}
			for _, span := range spans {
				out = append(out, newRecoverableError(
					DiagnosticFieldIDDuplicate,
					"explicit field id is duplicated within the same containing field list",
					span,
				))
			}
		}
	}

	return out, nil
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
