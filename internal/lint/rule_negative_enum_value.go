package lint

import (
	"context"
	"strings"

	"github.com/kpumuk/thrift-weaver/internal/syntax"
)

const (
	// DiagnosticNegativeEnumValue reports explicit negative enum values.
	DiagnosticNegativeEnumValue syntax.DiagnosticCode = "LINT_ENUM_VALUE_NEGATIVE"
)

// NegativeEnumValueRule warns when an enum value is assigned a negative integer.
type NegativeEnumValueRule struct{}

// ID returns the stable rule identifier.
func (NegativeEnumValueRule) ID() string {
	return "negative_enum_value"
}

// Description returns a human-readable rule summary.
func (NegativeEnumValueRule) Description() string {
	return "explicit enum values must not be negative"
}

// Run evaluates the rule against a syntax tree.
func (NegativeEnumValueRule) Run(ctx context.Context, tree *syntax.Tree) ([]syntax.Diagnostic, error) {
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	out := make([]syntax.Diagnostic, 0, 4)
	forEachNamedNode(tree, func(n *syntax.Node, kind string) {
		if kind != "enum_definition" {
			return
		}
		if hasErrorFlags(n.Flags) {
			return
		}

		for _, memberID := range tree.MemberNodeIDs(n.ID) {
			member := tree.NodeByID(memberID)
			if member == nil || syntax.KindName(member.Kind) != "enum_value" {
				continue
			}
			if hasErrorFlags(member.Flags) {
				continue
			}

			valueSpan := firstChildSpanByKind(tree, member.ID, "int_literal")
			if !valueSpan.IsValid() {
				continue
			}
			if !strings.HasPrefix(strings.TrimSpace(textForSpan(tree.Source, valueSpan)), "-") {
				continue
			}

			out = append(out, newRecoverableWarning(
				DiagnosticNegativeEnumValue,
				"explicit enum values must not be negative",
				valueSpan,
			))
		}
	})

	return out, nil
}
