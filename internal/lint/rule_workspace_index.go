package lint

import (
	"context"

	"github.com/kpumuk/thrift-weaver/internal/index"
	"github.com/kpumuk/thrift-weaver/internal/syntax"
	"github.com/kpumuk/thrift-weaver/internal/text"
)

const (
	// DiagnosticSourceWorkspace is the diagnostic source used by workspace-aware lint rules.
	DiagnosticSourceWorkspace = "thriftls.workspace-lint"

	// DiagnosticIncludeTargetUnknown reports include directives whose targets cannot be resolved.
	DiagnosticIncludeTargetUnknown syntax.DiagnosticCode = "LINT_INCLUDE_TARGET_UNKNOWN"
	// DiagnosticQualifiedReferenceUnknown reports qualified references that cannot be resolved through includes.
	DiagnosticQualifiedReferenceUnknown syntax.DiagnosticCode = "LINT_QUALIFIED_REFERENCE_UNKNOWN"
	// DiagnosticQualifiedReferenceAmbiguous reports qualified references that resolve ambiguously through includes.
	DiagnosticQualifiedReferenceAmbiguous syntax.DiagnosticCode = "LINT_QUALIFIED_REFERENCE_AMBIGUOUS"
)

// IncludeResolutionWorkspaceRule surfaces include-resolution diagnostics from the workspace index.
type IncludeResolutionWorkspaceRule struct{}

// QualifiedReferenceWorkspaceRule validates qualified type references that require include resolution.
type QualifiedReferenceWorkspaceRule struct{}

// WorkspaceServiceSemanticsRule validates qualified service extends/throws references across files.
type WorkspaceServiceSemanticsRule struct{}

// ID returns the stable rule identifier.
func (IncludeResolutionWorkspaceRule) ID() string {
	return "workspace_include_resolution"
}

// Description returns a human-readable rule summary.
func (IncludeResolutionWorkspaceRule) Description() string {
	return "include directives must resolve within the workspace"
}

// RunWorkspace evaluates the rule against an indexed document view.
func (IncludeResolutionWorkspaceRule) RunWorkspace(ctx context.Context, view *index.DocumentView) ([]syntax.Diagnostic, error) {
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	doc := workspaceDocument(view)
	if doc == nil {
		return []syntax.Diagnostic{}, nil
	}

	out := make([]syntax.Diagnostic, 0, len(doc.Diagnostics))
	for _, diag := range doc.Diagnostics {
		switch diag.Code {
		case index.DiagnosticIncludeMissing:
			out = append(out, newWorkspaceDiagnostic(DiagnosticIncludeTargetUnknown, diag.Message, diag.Severity, diag.Span))
		}
	}
	return out, nil
}

// ID returns the stable rule identifier.
func (QualifiedReferenceWorkspaceRule) ID() string {
	return "workspace_qualified_references"
}

// Description returns a human-readable rule summary.
func (QualifiedReferenceWorkspaceRule) Description() string {
	return "qualified type references must resolve through include aliases"
}

// RunWorkspace evaluates the rule against an indexed document view.
func (QualifiedReferenceWorkspaceRule) RunWorkspace(ctx context.Context, view *index.DocumentView) ([]syntax.Diagnostic, error) {
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	doc := workspaceDocument(view)
	if doc == nil {
		return []syntax.Diagnostic{}, nil
	}

	out := make([]syntax.Diagnostic, 0, len(doc.References))
	for _, ref := range doc.References {
		if ref.Qualifier == "" || ref.Context != index.ReferenceKindType || ref.Tainted {
			continue
		}
		switch ref.Binding.Status {
		case index.BindingStatusUnknown, index.BindingStatusBound, index.BindingStatusTainted, index.BindingStatusUnsupported:
			continue
		case index.BindingStatusAmbiguous:
			out = append(out, newRecoverableError(
				DiagnosticQualifiedReferenceAmbiguous,
				"qualified reference is ambiguous within the include graph",
				ref.Span,
			))
		case index.BindingStatusUnresolved:
			out = append(out, newRecoverableError(
				DiagnosticQualifiedReferenceUnknown,
				"qualified reference is unknown in the workspace",
				ref.Span,
			))
		}
	}
	return out, nil
}

// ID returns the stable rule identifier.
func (WorkspaceServiceSemanticsRule) ID() string {
	return "workspace_service_semantics"
}

// Description returns a human-readable rule summary.
func (WorkspaceServiceSemanticsRule) Description() string {
	return "qualified service extends and throws references must resolve to compatible declarations"
}

// RunWorkspace evaluates the rule against an indexed document view.
func (WorkspaceServiceSemanticsRule) RunWorkspace(ctx context.Context, view *index.DocumentView) ([]syntax.Diagnostic, error) {
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	doc := workspaceDocument(view)
	if doc == nil {
		return []syntax.Diagnostic{}, nil
	}

	out := make([]syntax.Diagnostic, 0, len(doc.References))
	for _, ref := range doc.References {
		if ref.Qualifier == "" || ref.Tainted {
			continue
		}

		switch ref.Context {
		case index.ReferenceKindType, index.ReferenceKindOpaque:
			continue
		case index.ReferenceKindServiceExtends:
			appendWorkspaceServiceDiagnostic(ref, DiagnosticServiceExtendsUnknown, "extended service is unknown in the workspace", DiagnosticServiceExtendsNotService, "extended declaration must be a service", &out)
		case index.ReferenceKindThrowsType:
			appendWorkspaceServiceDiagnostic(ref, DiagnosticServiceThrowsUnknown, "`throws` type is unknown in the workspace", DiagnosticServiceThrowsNotException, "`throws` parameters must use exception types", &out)
		}
	}
	return out, nil
}

func appendWorkspaceServiceDiagnostic(ref index.ReferenceSite, unknownCode syntax.DiagnosticCode, unknownMessage string, kindCode syntax.DiagnosticCode, kindMessage string, out *[]syntax.Diagnostic) {
	switch ref.Binding.Status {
	case index.BindingStatusUnknown:
		return
	case index.BindingStatusBound, index.BindingStatusTainted, index.BindingStatusUnsupported:
		return
	case index.BindingStatusAmbiguous:
		*out = append(*out, newRecoverableError(
			DiagnosticQualifiedReferenceAmbiguous,
			"qualified reference is ambiguous within the include graph",
			ref.Span,
		))
	case index.BindingStatusUnresolved:
		if ref.Binding.Reason == "declaration kind does not match expected context" {
			*out = append(*out, newRecoverableError(kindCode, kindMessage, ref.Span))
			return
		}
		*out = append(*out, newRecoverableError(unknownCode, unknownMessage, ref.Span))
	}
}

func workspaceDocument(view *index.DocumentView) *index.DocumentSummary {
	if view == nil {
		return nil
	}
	return view.Document
}

func newWorkspaceDiagnostic(code syntax.DiagnosticCode, message string, severity syntax.Severity, span text.Span) syntax.Diagnostic {
	return syntax.Diagnostic{
		Code:        code,
		Message:     message,
		Severity:    severity,
		Span:        span,
		Recoverable: true,
	}
}
