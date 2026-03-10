package index

import (
	"sort"
	"strings"

	"github.com/kpumuk/thrift-weaver/internal/syntax"
	"github.com/kpumuk/thrift-weaver/internal/text"
)

// Diagnostic codes emitted by the workspace index foundation.
const (
	DiagnosticIncludeMissing            = "INDEX_INCLUDE_MISSING"
	DiagnosticIncludeAliasConflict      = "INDEX_INCLUDE_ALIAS_CONFLICT"
	DiagnosticIncludeDuplicatePath      = "INDEX_INCLUDE_DUPLICATE_PATH"
	DiagnosticDocumentPathCollision     = "INDEX_DOCUMENT_PATH_COLLISION"
	DiagnosticQualifiedReferenceUnknown = "INDEX_QUALIFIED_REFERENCE_UNKNOWN"
	DiagnosticQualifiedReferenceAmbig   = "INDEX_QUALIFIED_REFERENCE_AMBIGUOUS"
	DiagnosticReferenceUnsupported      = "INDEX_REFERENCE_UNSUPPORTED"
)

func newDiagnostic(uri, code, message string, severity syntax.Severity, span text.Span) IndexDiagnostic {
	return IndexDiagnostic{
		URI:      strings.TrimSpace(uri),
		Code:     strings.TrimSpace(code),
		Message:  strings.TrimSpace(message),
		Severity: severity,
		Span:     span,
	}
}

func sortDiagnostics(diags []IndexDiagnostic) {
	if len(diags) < 2 {
		return
	}
	sort.SliceStable(diags, func(i, j int) bool {
		a := diags[i]
		b := diags[j]
		if a.URI != b.URI {
			return a.URI < b.URI
		}
		if a.Span.Start != b.Span.Start {
			return a.Span.Start < b.Span.Start
		}
		if a.Span.End != b.Span.End {
			return a.Span.End < b.Span.End
		}
		if a.Severity != b.Severity {
			return a.Severity < b.Severity
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.Message < b.Message
	})
}

func cloneDiagnostics(in []IndexDiagnostic) []IndexDiagnostic {
	out := make([]IndexDiagnostic, len(in))
	copy(out, in)
	return out
}

// ViewForDocument returns the indexed document view for uri within snapshot.
func ViewForDocument(snapshot *WorkspaceSnapshot, uri string) (*DocumentView, bool, error) {
	if snapshot == nil {
		return nil, false, nil
	}
	_, key, err := CanonicalizeDocumentURI(uri)
	if err != nil {
		return nil, false, err
	}
	doc := snapshot.Documents[key]
	if doc == nil {
		return nil, false, nil
	}
	return &DocumentView{Document: doc, Snapshot: snapshot}, true, nil
}
