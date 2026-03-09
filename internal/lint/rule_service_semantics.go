package lint

import (
	"context"
	"strings"

	"github.com/kpumuk/thrift-weaver/internal/syntax"
)

const (
	// DiagnosticServiceOnewayReturnNotVoid reports oneway functions with a non-void return type.
	DiagnosticServiceOnewayReturnNotVoid syntax.DiagnosticCode = "LINT_SERVICE_ONEWAY_RETURN_NOT_VOID"
	// DiagnosticServiceOnewayHasThrows reports oneway functions that declare throws.
	DiagnosticServiceOnewayHasThrows syntax.DiagnosticCode = "LINT_SERVICE_ONEWAY_HAS_THROWS"
	// DiagnosticServiceThrowsUnknown reports unresolved locally referenceable throws types.
	DiagnosticServiceThrowsUnknown syntax.DiagnosticCode = "LINT_SERVICE_THROWS_UNKNOWN"
	// DiagnosticServiceThrowsNotException reports throws types that resolve to a non-exception declaration.
	DiagnosticServiceThrowsNotException syntax.DiagnosticCode = "LINT_SERVICE_THROWS_NOT_EXCEPTION"
	// DiagnosticServiceExtendsUnknown reports unresolved locally referenceable service super types.
	DiagnosticServiceExtendsUnknown syntax.DiagnosticCode = "LINT_SERVICE_EXTENDS_UNKNOWN"
	// DiagnosticServiceExtendsNotService reports service super types that resolve to a non-service declaration.
	DiagnosticServiceExtendsNotService syntax.DiagnosticCode = "LINT_SERVICE_EXTENDS_NOT_SERVICE"
)

// ServiceSemanticsRule validates local service constraints that do not require cross-file indexing.
type ServiceSemanticsRule struct{}

// ID returns the stable rule identifier.
func (ServiceSemanticsRule) ID() string {
	return "service_semantics"
}

// Description returns a human-readable rule summary.
func (ServiceSemanticsRule) Description() string {
	return "service declarations must satisfy local oneway, throws, and extends constraints"
}

// Run evaluates the rule against a syntax tree.
func (ServiceSemanticsRule) Run(ctx context.Context, tree *syntax.Tree) ([]syntax.Diagnostic, error) {
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	symbols := collectLocalSymbols(tree)
	out := make([]syntax.Diagnostic, 0, 6)
	forEachNamedNode(tree, func(n *syntax.Node, kind string) {
		if kind != "service_definition" || hasErrorFlags(n.Flags) {
			return
		}
		appendServiceExtendsDiagnostics(tree, symbols, n.ID, &out)
		for _, memberID := range tree.MemberNodeIDs(n.ID) {
			member := tree.NodeByID(memberID)
			if member == nil || syntax.KindName(member.Kind) != "function_definition" || hasErrorFlags(member.Flags) {
				continue
			}
			appendOnewayDiagnostics(tree, member.ID, &out)
			appendThrowsDiagnostics(tree, symbols, member.ID, &out)
		}
	})

	return out, nil
}

func appendServiceExtendsDiagnostics(tree *syntax.Tree, symbols localSymbols, serviceID syntax.NodeID, out *[]syntax.Diagnostic) {
	superSpan, kind, ok := lookupResolvableScopedIdentifier(tree, symbols, serviceID)
	if !superSpan.IsValid() {
		return
	}
	if !ok {
		*out = append(*out, newRecoverableError(
			DiagnosticServiceExtendsUnknown,
			"extended service is unknown in the current document",
			superSpan,
		))
		return
	}
	if kind.isService() {
		return
	}

	*out = append(*out, newRecoverableError(
		DiagnosticServiceExtendsNotService,
		"extended declaration must be a service",
		superSpan,
	))
}

func appendOnewayDiagnostics(tree *syntax.Tree, functionID syntax.NodeID, out *[]syntax.Diagnostic) {
	modifierSpan := firstChildSpanByKind(tree, functionID, "function_modifier")
	if !modifierSpan.IsValid() || strings.TrimSpace(textForSpan(tree.Source, modifierSpan)) != "oneway" {
		return
	}

	returnSpan := firstChildSpanByKind(tree, functionID, "return_type")
	if returnSpan.IsValid() && strings.TrimSpace(textForSpan(tree.Source, returnSpan)) != "void" {
		*out = append(*out, newRecoverableError(
			DiagnosticServiceOnewayReturnNotVoid,
			"oneway functions must return `void`",
			returnSpan,
		))
	}

	throwsSpan := firstChildSpanByKind(tree, functionID, "throws_clause")
	if !throwsSpan.IsValid() {
		return
	}
	*out = append(*out, newRecoverableError(
		DiagnosticServiceOnewayHasThrows,
		"oneway functions must not declare `throws`",
		throwsSpan,
	))
}

func appendThrowsDiagnostics(tree *syntax.Tree, symbols localSymbols, functionID syntax.NodeID, out *[]syntax.Diagnostic) {
	throwsID, ok := firstChildNodeIDByKind(tree, functionID, "throws_clause")
	if !ok {
		return
	}
	paramsID, ok := firstChildNodeIDByKind(tree, throwsID, "parameter_list")
	if !ok {
		return
	}

	for _, fieldID := range tree.ChildNodeIDs(paramsID) {
		field := tree.NodeByID(fieldID)
		if field == nil || syntax.KindName(field.Kind) != "field" || hasErrorFlags(field.Flags) {
			continue
		}

		typeID, ok := firstDirectTypeChildNodeID(tree, field.ID)
		if !ok {
			continue
		}
		typeID, ok = unwrapTypeNodeID(tree, typeID)
		if !ok {
			continue
		}

		typeNode := tree.NodeByID(typeID)
		if typeNode == nil {
			continue
		}
		if syntax.KindName(typeNode.Kind) != "scoped_identifier" {
			*out = append(*out, newRecoverableError(
				DiagnosticServiceThrowsNotException,
				"`throws` parameters must use exception types",
				typeNode.Span,
			))
			continue
		}

		span, kind, ok := lookupResolvableScopedIdentifierSpan(tree, symbols, typeNode.Span)
		if !span.IsValid() {
			continue
		}
		if !ok {
			*out = append(*out, newRecoverableError(
				DiagnosticServiceThrowsUnknown,
				"`throws` type is unknown in the current document",
				span,
			))
			continue
		}
		if kind.isExceptionType() {
			continue
		}

		*out = append(*out, newRecoverableError(
			DiagnosticServiceThrowsNotException,
			"`throws` parameters must use exception types",
			span,
		))
	}
}
