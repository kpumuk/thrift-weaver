package lsp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/kpumuk/thrift-weaver/internal/index"
	itext "github.com/kpumuk/thrift-weaver/internal/text"
)

// PrepareRename handles textDocument/prepareRename.
func (s *Server) PrepareRename(ctx context.Context, p PrepareRenameParams) (*PrepareRenameResult, error) {
	manager, doc, err := s.queryDocumentForWorkspaceRequest(ctx, p.TextDocument.URI)
	if err != nil {
		return nil, err
	}

	result, _, err := manager.PrepareRename(ctx, doc, utf16PositionFromLSP(p.Position))
	if err != nil {
		return nil, renameQueryError(err, blockersFromPrepareRename(result))
	}

	location, err := s.lspLocationFromIndexLocation(index.Location{URI: doc.URI, Span: result.Span})
	if err != nil {
		return nil, err
	}
	return &PrepareRenameResult{
		Range:       location.Range,
		Placeholder: result.Placeholder,
	}, nil
}

// Rename handles textDocument/rename.
func (s *Server) Rename(ctx context.Context, p RenameParams) (*WorkspaceEdit, error) {
	manager, doc, err := s.queryDocumentForWorkspaceRequest(ctx, p.TextDocument.URI)
	if err != nil {
		return nil, err
	}

	result, _, err := manager.Rename(ctx, doc, utf16PositionFromLSP(p.Position), p.NewName)
	if err != nil {
		return nil, renameQueryError(err, blockersFromRename(result))
	}
	return s.workspaceEditFromRenameResult(result)
}

func renameQueryError(err error, blockers []index.IndexDiagnostic) error {
	if len(blockers) == 0 {
		return err
	}

	if !errors.Is(err, index.ErrRenameBlocked) && !errors.Is(err, index.ErrContentModified) {
		return err
	}

	message := blockerMessages(blockers)
	if message == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, message)
}

func blockersFromPrepareRename(result *index.PrepareRenameResult) []index.IndexDiagnostic {
	if result == nil {
		return nil
	}
	return result.Blockers
}

func blockersFromRename(result *index.RenameResult) []index.IndexDiagnostic {
	if result == nil {
		return nil
	}
	return result.Blockers
}

func blockerMessages(blockers []index.IndexDiagnostic) string {
	parts := make([]string, 0, len(blockers))
	for _, blocker := range blockers {
		if msg := strings.TrimSpace(blocker.Message); msg != "" {
			parts = append(parts, msg)
		}
	}
	return strings.Join(parts, "; ")
}

func (s *Server) workspaceEditFromRenameResult(result *index.RenameResult) (*WorkspaceEdit, error) {
	if result == nil || len(result.Documents) == 0 {
		return &WorkspaceEdit{}, nil
	}

	changes := make([]TextDocumentEdit, 0, len(result.Documents))
	for _, doc := range result.Documents {
		edits, err := s.lspTextEditsFromByteEdits(doc.URI, doc.Edits)
		if err != nil {
			return nil, err
		}
		changes = append(changes, TextDocumentEdit{
			TextDocument: OptionalVersionedTextDocumentIdentifier{
				URI:     doc.URI,
				Version: doc.Version,
			},
			Edits: edits,
		})
	}
	return &WorkspaceEdit{DocumentChanges: changes}, nil
}

func (s *Server) lspTextEditsFromByteEdits(uri string, edits []itext.ByteEdit) ([]TextEdit, error) {
	if len(edits) == 0 {
		return []TextEdit{}, nil
	}

	li, err := s.lineIndexForDocumentURI(uri)
	if err != nil {
		return nil, err
	}

	sorted := slices.Clone(edits)
	slices.SortFunc(sorted, func(a, b itext.ByteEdit) int {
		if a.Span.Start < b.Span.Start {
			return -1
		}
		if a.Span.Start > b.Span.Start {
			return 1
		}
		if a.Span.End < b.Span.End {
			return -1
		}
		if a.Span.End > b.Span.End {
			return 1
		}
		return 0
	})

	out := make([]TextEdit, 0, len(sorted))
	for _, edit := range sorted {
		rng, err := lspRangeFromSpan(li, edit.Span)
		if err != nil {
			return nil, err
		}
		out = append(out, TextEdit{Range: rng, NewText: string(edit.NewText)})
	}
	return out, nil
}
