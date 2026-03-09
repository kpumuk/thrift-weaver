package lint

import (
	"context"
	"strings"

	"github.com/kpumuk/thrift-weaver/internal/syntax"
)

const (
	// DiagnosticUnionFieldRequired reports explicit required union fields.
	DiagnosticUnionFieldRequired syntax.DiagnosticCode = "LINT_UNION_FIELD_REQUIRED"
)

// UnionFieldRequirednessRule warns when union fields are marked required.
type UnionFieldRequirednessRule struct{}

// ID returns the stable rule identifier.
func (UnionFieldRequirednessRule) ID() string {
	return "union_field_requiredness"
}

// Description returns a human-readable rule summary.
func (UnionFieldRequirednessRule) Description() string {
	return "union fields are implicitly optional and should not be marked required"
}

// Run evaluates the rule against a syntax tree.
func (UnionFieldRequirednessRule) Run(ctx context.Context, tree *syntax.Tree) ([]syntax.Diagnostic, error) {
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	out := make([]syntax.Diagnostic, 0, 4)
	forEachNamedNode(tree, func(n *syntax.Node, kind string) {
		if kind != "union_definition" {
			return
		}
		if hasErrorFlags(n.Flags) {
			return
		}

		for _, memberID := range tree.MemberNodeIDs(n.ID) {
			member := tree.NodeByID(memberID)
			if member == nil || syntax.KindName(member.Kind) != "field" {
				continue
			}
			if hasErrorFlags(member.Flags) {
				continue
			}

			requirednessSpan := firstChildSpanByKind(tree, member.ID, "requiredness")
			if !requirednessSpan.IsValid() {
				continue
			}
			if strings.TrimSpace(textForSpan(tree.Source, requirednessSpan)) != "required" {
				continue
			}

			out = append(out, newRecoverableWarning(
				DiagnosticUnionFieldRequired,
				"union fields are implicitly optional; explicit `required` should not be used",
				requirednessSpan,
			))
		}
	})

	return out, nil
}
